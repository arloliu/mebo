// Package simple8b implements the retained Simple8b timestamp experiment.
package simple8b

import (
	"iter"
	"math/bits"
	"slices"

	"github.com/arloliu/mebo/encoding"
	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/pool"
)

// Simple8b timestamp encoding.
//
// Pipeline: the first timestamp is stored as a fixed 64-bit value; every
// subsequent timestamp is reduced to a delta-of-delta (same preprocessing as the
// Delta encoder), zigzag-encoded, and the resulting stream is bit-packed with
// Simple8b (Anh & Moffat, SPE 2010): each 64-bit word carries a 4-bit selector
// plus up to 60 data bits, packing many small values per word.
//
// On-disk segment layout (one per metric column; count comes from the index):
//
//	[firstTS    : uint64]                       // index 0
//	[nExc       : uint16]                       // delta-of-delta exceptions (~always 0)
//	[exceptions : nExc × (pos:uint32, zz:uint64)] // dods whose zigzag needs >60 bits
//	[words      : K × uint64]                   // Simple8b-packed zigzag(dod), indices 1..count-1
//
// All multi-byte fields use the blob's endian engine (words are endian-sensitive,
// unlike the varint-based Delta/DeltaPacked encoders).
//
// Lifecycle mirrors TimestampDeltaPackedEncoder: values accumulate in a pending
// buffer and the whole segment is packed lazily on Bytes(). count is cumulative
// across metrics; Reset() clears per-segment state but keeps the shared buffer.
// Bytes() is terminal for a segment — Reset() must be called before writing the
// next metric (this is exactly how blob.NumericEncoder.EndMetric drives it).

// s8bSelNum[sel] = number of values packed per word for selector sel.
// s8bSelBits[sel] = bit width of each value for selector sel.
var s8bSelNum = [16]int{240, 120, 60, 30, 20, 15, 12, 10, 8, 7, 6, 5, 4, 3, 2, 1}
var s8bSelBits = [16]int{0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 15, 20, 30, 60}

const s8bMaxBits = 60

func s8bZigZag(v int64) uint64   { return uint64((v << 1) ^ (v >> 63)) } //nolint:gosec
func s8bUnZigZag(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }    //nolint:gosec

type TimestampSimple8bEncoder struct {
	buf       *pool.ByteBuffer
	engine    endian.EndianEngine
	count     int   // cumulative across all metric segments
	seqCount  int   // count within the current segment
	firstTS   int64 // first timestamp of the current segment
	prevTS    int64
	prevDelta int64
	pending   []uint64 // zigzag(dod) for current segment, one per index 1..seqCount-1
	orAll     uint64   // OR of all pending values; if it fits in 60 bits there are no exceptions
	flushed   bool     // current segment already packed into buf
}

var _ encoding.ColumnarEncoder[int64] = (*TimestampSimple8bEncoder)(nil)

// NewTimestampSimple8bEncoder creates a Simple8b timestamp encoder using the
// given endian engine for the fixed-width fields.
func NewTimestampSimple8bEncoder(engine endian.EndianEngine) *TimestampSimple8bEncoder {
	return &TimestampSimple8bEncoder{
		engine: engine,
		buf:    pool.GetBlobBuffer(),
	}
}

// Write encodes a single timestamp (microseconds since Unix epoch).
func (e *TimestampSimple8bEncoder) Write(timestampUs int64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write after Finish()")
	}

	e.count++
	e.seqCount++

	if e.seqCount == 1 {
		e.firstTS = timestampUs
		e.prevTS = timestampUs
		e.flushed = false

		return
	}

	delta := timestampUs - e.prevTS
	dod := delta - e.prevDelta // seqCount==2 → prevDelta==0 → dod==delta
	zz := s8bZigZag(dod)
	e.pending = append(e.pending, zz)
	e.orAll |= zz
	e.prevTS = timestampUs
	e.prevDelta = delta
}

// WriteSlice encodes a slice of timestamps. Bulk path: computes delta-of-deltas
// inline and pre-grows the pending buffer once (no per-value call overhead).
func (e *TimestampSimple8bEncoder) WriteSlice(timestampsUs []int64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write after Finish()")
	}

	tsLen := len(timestampsUs)
	if tsLen == 0 {
		return
	}
	e.count += tsLen
	startSeq := e.seqCount

	i := 0
	if startSeq == 0 {
		e.firstTS = timestampsUs[0]
		e.prevTS = timestampsUs[0]
		e.flushed = false
		i = 1
	}

	if rest := tsLen - i; rest > 0 {
		e.pending = slices.Grow(e.pending, rest)
	}

	prevTS := e.prevTS
	prevDelta := e.prevDelta
	pending := e.pending
	orAll := e.orAll
	for ; i < tsLen; i++ {
		ts := timestampsUs[i]
		delta := ts - prevTS
		zz := s8bZigZag(delta - prevDelta)
		pending = append(pending, zz)
		orAll |= zz
		prevTS = ts
		prevDelta = delta
	}
	e.pending = pending
	e.orAll = orAll

	e.seqCount = startSeq + tsLen // every input timestamp is one segment element
	e.prevTS = prevTS
	e.prevDelta = prevDelta
}

// Bytes packs any pending segment and returns the full accumulated buffer.
func (e *TimestampSimple8bEncoder) Bytes() []byte {
	if e.buf == nil {
		panic("encoder already finished - cannot access bytes after Finish()")
	}

	e.flush()

	return e.buf.Bytes()
}

func (e *TimestampSimple8bEncoder) flush() {
	if e.flushed || e.seqCount == 0 {
		return
	}

	eng := e.engine
	e.buf.B = eng.AppendUint64(e.buf.B, uint64(e.firstTS)) //nolint:gosec

	vals := e.pending

	// Common path: the OR of all values fits in 60 bits, so no value needs >60
	// bits and there are no exceptions — skip the per-value exception scan entirely.
	if bits.Len64(e.orAll) <= s8bMaxBits {
		e.buf.B = eng.AppendUint16(e.buf.B, 0)
	} else {
		// Rare: at least one zigzag(dod) needs >60 bits. Extract those as exceptions
		// (position + value) and pack a 0 in their slot.
		excCount := 0
		for _, v := range vals {
			if bits.Len64(v) > s8bMaxBits {
				excCount++
			}
		}
		e.buf.B = eng.AppendUint16(e.buf.B, uint16(excCount)) //nolint:gosec
		work := make([]uint64, len(vals))
		copy(work, vals)
		for i, v := range vals {
			if bits.Len64(v) > s8bMaxBits {
				e.buf.B = eng.AppendUint32(e.buf.B, uint32(i)) //nolint:gosec
				e.buf.B = eng.AppendUint64(e.buf.B, v)
				work[i] = 0
			}
		}
		vals = work
	}

	// Greedy Simple8b packing. Pre-grow the buffer for the worst case (1 value per
	// word) and write words via PutUint64 at offsets, avoiding per-word append
	// bounds checks (mirrors TimestampDeltaPackedEncoder.flushGroup). Trimmed after.
	n := len(vals)
	startLen := len(e.buf.B)
	e.buf.Grow(n*8 + 8)
	e.buf.B = e.buf.B[:startLen+n*8]
	off := startLen
	for i := 0; i < n; {
		sel, cnt := s8bSelectFor(vals[i:])
		word := uint64(sel) << 60 //nolint:gosec
		if bitw := s8bSelBits[sel]; bitw > 0 {
			for j := range cnt {
				word |= vals[i+j] << uint(j*bitw) //nolint:gosec
			}
		}
		eng.PutUint64(e.buf.B[off:off+8], word)
		off += 8
		i += cnt
	}
	e.buf.B = e.buf.B[:off]

	e.flushed = true
}

// Len returns the cumulative number of encoded timestamps.
func (e *TimestampSimple8bEncoder) Len() int { return e.count }

// Size returns the size in bytes of data flushed to the buffer.
func (e *TimestampSimple8bEncoder) Size() int {
	if e.buf == nil {
		panic("encoder already finished - cannot access size after Finish()")
	}

	return e.buf.Len()
}

// Reset clears per-segment state for the next metric while keeping the shared
// buffer and cumulative count (mirrors TimestampDeltaPackedEncoder).
func (e *TimestampSimple8bEncoder) Reset() {
	e.seqCount = 0
	e.firstTS = 0
	e.prevTS = 0
	e.prevDelta = 0
	e.pending = e.pending[:0]
	e.orAll = 0
	e.flushed = false
}

// Finish returns buffer resources to the pool. The encoder is unusable afterward.
func (e *TimestampSimple8bEncoder) Finish() {
	if e.buf != nil {
		pool.PutBlobBuffer(e.buf)
		e.buf = nil
	}
	e.count = 0
	e.seqCount = 0
	e.prevTS = 0
	e.prevDelta = 0
	e.pending = nil
	e.orAll = 0
	e.flushed = false
}

// s8bMaxVals[w] = the most values packable in one 60-bit word when the widest
// value needs w bits (0..60): the smallest-bit-width selector whose width >= w.
// s8bCountSel[q] = selector index whose count is the largest s8bSelNum entry <= q.
var (
	s8bMaxVals  [61]int
	s8bCountSel [241]int
)

func init() {
	for w := range s8bMaxVals {
		best := 1
		for s := range 16 {
			if s8bSelBits[s] >= w && s8bSelNum[s] > best {
				best = s8bSelNum[s]
			}
		}
		s8bMaxVals[w] = best
	}
	for q := range s8bCountSel {
		sel := 15 // count 1 (s8bSelNum is descending, so first entry <= q is the largest)
		for s := range 16 {
			if s8bSelNum[s] <= q {
				sel = s
				break
			}
		}
		s8bCountSel[q] = sel
	}
}

// s8bSelectFor returns the selector packing the most values from the front of
// vals, found in a single forward scan: extend the run while it still fits a
// word at the running max bit width, then map the run length to a selector. This
// is equivalent to the descending "first selector that fits" search but touches
// each value once. vals must contain only values <= s8bMaxBits bits (exceptions
// removed upstream).
func s8bSelectFor(vals []uint64) (sel, cnt int) {
	n := min(len(vals), s8bSelNum[0]) // cap at the largest selector count (240)
	maxbits := 0
	k := 0
	for k < n {
		nb := max(maxbits, bits.Len64(vals[k]))
		if k+1 > s8bMaxVals[nb] {
			break
		}
		maxbits = nb
		k++
	}

	sel = s8bCountSel[k]

	return sel, s8bSelNum[sel]
}

type TimestampSimple8bDecoder struct {
	engine endian.EndianEngine
}

var _ encoding.ColumnarDecoder[int64] = TimestampSimple8bDecoder{}

// NewTimestampSimple8bDecoder creates a Simple8b timestamp decoder. The engine
// must match the encoder's.
func NewTimestampSimple8bDecoder(engine endian.EndianEngine) TimestampSimple8bDecoder {
	return TimestampSimple8bDecoder{engine: engine}
}

// All yields all decoded timestamps. Zero-allocation on the common path (no
// delta-of-delta exceptions); exception patching allocates a small lookup.
func (d TimestampSimple8bDecoder) All(data []byte, count int) iter.Seq[int64] {
	return func(yield func(int64) bool) {
		if count <= 0 || len(data) < 8 {
			return
		}

		firstTS := int64(d.engine.Uint64(data[0:8])) //nolint:gosec
		if !yield(firstTS) {
			return
		}
		if count == 1 {
			return
		}

		off := 8
		if off+2 > len(data) {
			return
		}
		nExc := int(d.engine.Uint16(data[off : off+2]))
		off += 2

		var excPos []uint32
		var excVal []uint64
		if nExc > 0 {
			excPos = make([]uint32, nExc)
			excVal = make([]uint64, nExc)
			for i := range nExc {
				if off+12 > len(data) {
					return
				}
				excPos[i] = d.engine.Uint32(data[off : off+4])
				off += 4
				excVal[i] = d.engine.Uint64(data[off : off+8])
				off += 8
			}
		}

		need := count - 1
		prevTS := firstTS
		var prevDelta int64
		produced := 0
		excIdx := 0

		for produced < need {
			if off+8 > len(data) {
				return
			}
			word := d.engine.Uint64(data[off : off+8])
			off += 8
			sel := int(word >> 60) //nolint:gosec
			cnt := s8bSelNum[sel]
			bitw := s8bSelBits[sel]

			var mask uint64
			if bitw > 0 {
				mask = uint64(1)<<uint(bitw) - 1
			}

			for j := 0; j < cnt && produced < need; j++ {
				var zz uint64
				if bitw > 0 {
					zz = (word >> uint(j*bitw)) & mask //nolint:gosec
				}
				if excIdx < nExc && uint32(produced) == excPos[excIdx] { //nolint:gosec
					zz = excVal[excIdx]
					excIdx++
				}
				prevDelta += s8bUnZigZag(zz)
				prevTS += prevDelta
				if !yield(prevTS) {
					return
				}
				produced++
			}
		}
	}
}

// At returns the timestamp at index via an O(n) scan (consistent with the Delta
// and Gorilla decoders, which are also O(n) for random access).
func (d TimestampSimple8bDecoder) At(data []byte, index int, count int) (int64, bool) {
	if index < 0 || index >= count || len(data) < 8 {
		return 0, false
	}

	i := 0
	var result int64
	found := false
	for ts := range d.All(data, count) {
		if i == index {
			result = ts
			found = true
			break
		}
		i++
	}

	return result, found
}
