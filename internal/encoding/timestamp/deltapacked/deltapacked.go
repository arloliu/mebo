package deltapacked

import (
	"encoding/binary"
	"iter"

	"github.com/arloliu/mebo/encoding"
	"github.com/arloliu/mebo/internal/encoding/internal/deltadelta"
	"github.com/arloliu/mebo/internal/encoding/internal/varint"
	"github.com/arloliu/mebo/internal/pool"
)

// Group Varint control byte layout:
// Each control byte describes 4 values, using 2 bits per value.
// The 2 bits encode the byte length of the zigzag-encoded value:
//
//	00 = 1 byte  (values 0-255)
//	01 = 2 bytes (values 256-65535)
//	10 = 4 bytes (values 65536-4294967295)
//	11 = 8 bytes (values > 4294967295)
//
// Control byte bit layout: [val3:2][val2:2][val1:2][val0:2]
// The data bytes for all 4 values immediately follow the control byte.
const groupSize = 4 // Number of values packed per control byte

// groupVarintLengths maps 2-bit tag to byte length.
var groupVarintLengths = [4]int{1, 2, 4, 8}

func nextDeltaOfDelta(ts int64, prevTS *int64, prevDelta *int64) int64 {
	delta := ts - *prevTS
	deltaOfDelta := delta - *prevDelta
	*prevTS = ts
	*prevDelta = delta

	return deltaOfDelta
}

// TimestampDeltaPackedEncoder implements TimestampEncoder using delta-of-delta encoding
// with Group Varint packing for the delta-of-delta values.
//
// Compared to TimestampDeltaEncoder which uses LEB128 varint per value, this encoder
// packs every 4 delta-of-delta values with a single control byte, eliminating per-byte
// continuation-bit branches during decoding and enabling byte-aligned bulk reads.
//
// Encoding format:
//  1. First timestamp: Full varint-encoded value (same as TimestampDeltaEncoder)
//  2. Second timestamp: Zigzag + varint encoded delta (same as TimestampDeltaEncoder)
//  3. Remaining timestamps: Grouped into blocks of 4 delta-of-deltas, each block has:
//     - 1 control byte (2 bits per value encoding byte-length)
//     - N data bytes (1/2/4/8 bytes per value, little-endian)
//  4. Tail values (< 4 remaining): Packed with a partial control byte
//
// Space characteristics vs LEB128 varint:
//   - Regular intervals (DoD=0): 1.25 bytes/value (1 control byte per 4 values + 1 data byte each)
//     vs 1 byte/value for LEB128. Slight overhead at small N.
//   - Semi-regular intervals: Comparable or better depending on value distribution
//   - The win is in decode throughput, not encode size
type TimestampDeltaPackedEncoder struct {
	prevTS    int64
	prevDelta int64
	buf       *pool.ByteBuffer
	count     int
	seqCount  int
	// Accumulator for grouping delta-of-deltas before writing
	pending    [groupSize]uint64
	pendingLen int
}

var _ encoding.ColumnarEncoder[int64] = (*TimestampDeltaPackedEncoder)(nil)

// NewTimestampDeltaPackedEncoder creates a new delta-of-delta timestamp encoder
// with Group Varint packing for improved decode throughput.
//
// Returns:
//   - *TimestampDeltaPackedEncoder: A new encoder instance ready for timestamp encoding
func NewTimestampDeltaPackedEncoder() *TimestampDeltaPackedEncoder {
	return &TimestampDeltaPackedEncoder{
		buf: pool.GetBlobBuffer(),
	}
}

// Write encodes a single timestamp using delta-of-delta with Group Varint packing.
//
// Parameters:
//   - timestampUs: Timestamp in microseconds since Unix epoch
func (e *TimestampDeltaPackedEncoder) Write(timestampUs int64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write after Finish()")
	}

	e.count++
	e.seqCount++

	if e.seqCount == 1 {
		e.appendUvarint(uint64(timestampUs)) //nolint:gosec
		e.prevTS = timestampUs
		return
	}

	delta := timestampUs - e.prevTS

	if e.seqCount == 2 {
		zigzag := (delta << 1) ^ (delta >> 63)
		e.appendUvarint(uint64(zigzag)) //nolint:gosec
		e.prevTS = timestampUs
		e.prevDelta = delta

		return
	}

	// Delta-of-delta: accumulate into group
	deltaOfDelta := delta - e.prevDelta
	zigzag := uint64((deltaOfDelta << 1) ^ (deltaOfDelta >> 63)) //nolint:gosec

	e.pending[e.pendingLen] = zigzag
	e.pendingLen++

	if e.pendingLen == groupSize {
		e.flushGroup(groupSize)
	}

	e.prevTS = timestampUs
	e.prevDelta = delta
}

// WriteSlice encodes a slice of timestamps using delta-of-delta with Group Varint packing.
//
// Parameters:
//   - timestampsUs: Slice of timestamps in microseconds since Unix epoch
func (e *TimestampDeltaPackedEncoder) WriteSlice(timestampsUs []int64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write after Finish()")
	}

	tsLen := len(timestampsUs)
	if tsLen == 0 {
		return
	}

	currentSeqCount := e.seqCount
	e.count += tsLen
	e.seqCount += tsLen

	// Conservative pre-allocation: control bytes + max 8 bytes per value + header
	e.buf.Grow(tsLen*9 + binary.MaxVarintLen64)

	prevTS := e.prevTS
	prevDelta := e.prevDelta
	startIdx := 0

	// First timestamp: full varint
	if currentSeqCount == 0 {
		ts := timestampsUs[0]
		e.appendUvarint(uint64(ts)) //nolint:gosec
		prevTS = ts
		startIdx = 1
		currentSeqCount++
	}

	// Second timestamp: zigzag+varint delta
	if startIdx < tsLen && currentSeqCount == 1 {
		ts := timestampsUs[startIdx]
		delta := ts - prevTS
		zigzag := (delta << 1) ^ (delta >> 63)
		e.appendUvarint(uint64(zigzag)) //nolint:gosec
		prevTS = ts
		prevDelta = delta
		startIdx++
	}

	remaining := timestampsUs[startIdx:]
	if shouldUseDeltaPackedEncodeSIMD(len(remaining)) {
		prevTS, prevDelta = e.writeSliceSIMDFused(remaining, prevTS, prevDelta)
	} else if deltadelta.ShouldUse(len(remaining)) {
		var deltaBuf [deltadelta.ChunkSize]int64

		for len(remaining) > 0 {
			n := min(len(remaining), deltadelta.ChunkSize)
			prevTS, prevDelta = deltadelta.IntoActive(deltaBuf[:n], remaining[:n], prevTS, prevDelta)

			for _, deltaOfDelta := range deltaBuf[:n] {
				zigzag := uint64((deltaOfDelta << 1) ^ (deltaOfDelta >> 63)) //nolint:gosec

				e.pending[e.pendingLen] = zigzag
				e.pendingLen++

				if e.pendingLen == groupSize {
					e.flushGroup(groupSize)
				}
			}

			remaining = remaining[n:]
		}
	} else {
		for _, ts := range remaining {
			deltaOfDelta := nextDeltaOfDelta(ts, &prevTS, &prevDelta)
			zigzag := uint64((deltaOfDelta << 1) ^ (deltaOfDelta >> 63)) //nolint:gosec

			e.pending[e.pendingLen] = zigzag
			e.pendingLen++

			if e.pendingLen == groupSize {
				e.flushGroup(groupSize)
			}
		}
	}

	e.prevTS = prevTS
	e.prevDelta = prevDelta
}

// writeSliceSIMDFused encodes remaining timestamps using the SIMD-fused pipeline:
// DoD computation (SIMD) → zigzag + tag-classify + pack (AVX2 kernel).
//
// This avoids the per-element zigzag loop and per-group flushGroup overhead by
// delegating the entire encode to the AVX2 kernel which handles zigzag encoding,
// branchless tag classification, and variable-width packing in a single pass.
func (e *TimestampDeltaPackedEncoder) writeSliceSIMDFused(
	remaining []int64,
	prevTS int64,
	prevDelta int64,
) (lastTS int64, lastDelta int64) {
	var deltaBuf [deltadelta.ChunkSize]int64

	for len(remaining) > 0 {
		n := min(len(remaining), deltadelta.ChunkSize)
		prevTS, prevDelta = deltadelta.IntoActive(deltaBuf[:n], remaining[:n], prevTS, prevDelta)

		// SIMD-fused encode for full groups
		nGroups := n / groupSize
		if nGroups > 0 {
			nValues := nGroups * groupSize
			// Worst case: 1 control byte + 32 payload bytes per group + 8 bytes write slack
			maxBytes := nGroups*33 + 8
			startLen := len(e.buf.B)
			e.buf.Grow(maxBytes)
			e.buf.B = e.buf.B[:startLen+maxBytes]
			written := encodeDeltaPackedGroupsSIMD(e.buf.B[startLen:], deltaBuf[:nValues], nGroups)
			e.buf.B = e.buf.B[:startLen+written]
		}

		// Tail (< groupSize values) via scalar path
		for i := nGroups * groupSize; i < n; i++ {
			zigzag := uint64((deltaBuf[i] << 1) ^ (deltaBuf[i] >> 63)) //nolint:gosec
			e.pending[e.pendingLen] = zigzag
			e.pendingLen++

			if e.pendingLen == groupSize {
				e.flushGroup(groupSize)
			}
		}

		remaining = remaining[n:]
	}

	return prevTS, prevDelta
}

// Bytes returns the encoded byte slice. Any pending partial group is flushed first.
//
// Returns:
//   - []byte: Encoded byte slice
func (e *TimestampDeltaPackedEncoder) Bytes() []byte {
	if e.buf == nil {
		panic("encoder already finished - cannot access bytes after Finish()")
	}

	// Flush any partial group
	if e.pendingLen > 0 {
		e.flushGroup(e.pendingLen)
	}

	return e.buf.Bytes()
}

// Len returns the number of encoded timestamps.
//
// Returns:
//   - int: Number of timestamps written since last Finish
func (e *TimestampDeltaPackedEncoder) Len() int {
	return e.count
}

// Size returns the size in bytes of encoded timestamps.
//
// Returns:
//   - int: Total bytes written to internal buffer since last Finish
func (e *TimestampDeltaPackedEncoder) Size() int {
	if e.buf == nil {
		panic("encoder already finished - cannot access size after Finish()")
	}

	return e.buf.Len()
}

// Reset clears the encoder state, allowing reuse for a new sequence.
func (e *TimestampDeltaPackedEncoder) Reset() {
	e.prevTS = 0
	e.prevDelta = 0
	e.seqCount = 0
	e.pendingLen = 0
}

// Finish finalizes the encoding and returns buffer resources to the pool.
func (e *TimestampDeltaPackedEncoder) Finish() {
	if e.buf != nil {
		pool.PutBlobBuffer(e.buf)
		e.buf = nil
	}
	e.prevTS = 0
	e.prevDelta = 0
	e.count = 0
	e.seqCount = 0
	e.pendingLen = 0
}

// flushGroup writes n pending values as a Group Varint block.
func (e *TimestampDeltaPackedEncoder) flushGroup(n int) {
	if n == groupSize {
		tag0 := encodeTag(e.pending[0])
		tag1 := encodeTag(e.pending[1])
		tag2 := encodeTag(e.pending[2])
		tag3 := encodeTag(e.pending[3])
		controlByte := tag0 | (tag1 << 2) | (tag2 << 4) | (tag3 << 6)
		totalDataBytes := groupVarintLengths[tag0] +
			groupVarintLengths[tag1] +
			groupVarintLengths[tag2] +
			groupVarintLengths[tag3]

		startLen := len(e.buf.B)
		e.buf.Grow(1 + totalDataBytes + 8)
		e.buf.B = e.buf.B[:startLen+1+totalDataBytes+8]
		e.buf.B[startLen] = controlByte

		offset := startLen + 1
		binary.LittleEndian.PutUint64(e.buf.B[offset:offset+8], e.pending[0])
		offset += groupVarintLengths[tag0]
		binary.LittleEndian.PutUint64(e.buf.B[offset:offset+8], e.pending[1])
		offset += groupVarintLengths[tag1]
		binary.LittleEndian.PutUint64(e.buf.B[offset:offset+8], e.pending[2])
		offset += groupVarintLengths[tag2]
		binary.LittleEndian.PutUint64(e.buf.B[offset:offset+8], e.pending[3])
		offset += groupVarintLengths[tag3]

		e.buf.B = e.buf.B[:offset]
		e.pendingLen = 0

		return
	}

	var controlByte byte
	var totalDataBytes int

	// Build control byte and calculate total data size
	for i := range n {
		tag := encodeTag(e.pending[i])
		controlByte |= tag << (uint(i) * 2) //nolint:gosec // i is bounded by groupSize (0-3)
		totalDataBytes += groupVarintLengths[tag]
	}

	// Allocate: 1 control byte + data bytes + 8 bytes slack for branchless writes.
	// The branchless write always writes 8 bytes per value (PutUint64), but only
	// advances offset by the actual tag length. The extra bytes are harmlessly
	// overwritten by the next value. The slack ensures the last value's 8-byte
	// write doesn't go out of bounds.
	startLen := len(e.buf.B)
	e.buf.Grow(1 + totalDataBytes + 8)
	e.buf.B = e.buf.B[:startLen+1+totalDataBytes+8]
	e.buf.B[startLen] = controlByte

	// Branchless write: always PutUint64, advance by actual byte length per tag.
	offset := startLen + 1
	for i := range n {
		tag := (controlByte >> (uint(i) * 2)) & 0x03 //nolint:gosec // i is bounded by groupSize (0-3)
		binary.LittleEndian.PutUint64(e.buf.B[offset:offset+8], e.pending[i])
		offset += groupVarintLengths[tag]
	}

	// Trim buffer back to actual data length (remove slack)
	e.buf.B = e.buf.B[:startLen+1+totalDataBytes]

	e.pendingLen = 0
}

// appendUvarint appends a uvarint to the buffer with a fast path for single-byte values.
func (e *TimestampDeltaPackedEncoder) appendUvarint(value uint64) {
	if value <= 0x7F {
		idx := len(e.buf.B)
		e.buf.ExtendOrGrow(1)
		e.buf.B[idx] = byte(value)

		return
	}

	e.buf.Grow(binary.MaxVarintLen64)
	e.buf.B = binary.AppendUvarint(e.buf.B, value)
}

// encodeTag returns the 2-bit tag for a zigzag-encoded value based on its magnitude.
// Branchless: uses bit manipulation to determine byte-width category.
func encodeTag(v uint64) byte {
	if v <= 0xFF {
		return 0
	}

	if v <= 0xFFFF {
		return 1
	}

	if v <= 0xFFFFFFFF {
		return 2
	}

	return 3
}

// TimestampDeltaPackedDecoder provides high-performance decoding of Group Varint packed
// delta-of-delta timestamps.
type TimestampDeltaPackedDecoder struct{}

// DeltaPackedTsState incrementally decodes a Group Varint timestamp stream.
type DeltaPackedTsState struct {
	data      []byte
	curTS     int64
	prevDelta int64
	offset    int
	seqCount  int
	control   byte
	groupIdx  int
	groupLen  int
}

var _ encoding.ColumnarDecoder[int64] = TimestampDeltaPackedDecoder{}

// NewTimestampDeltaPackedDecoder creates a new Group Varint delta-of-delta timestamp decoder.
//
// Returns:
//   - TimestampDeltaPackedDecoder: A new decoder instance (stateless, can be reused)
func NewTimestampDeltaPackedDecoder() TimestampDeltaPackedDecoder {
	return TimestampDeltaPackedDecoder{}
}

// NewDeltaPackedTsState initializes a timestamp cursor with the first timestamp.
func NewDeltaPackedTsState(data []byte) (DeltaPackedTsState, bool) {
	first, offset, ok := varint.DecodeU64(data, 0)
	if !ok {
		return DeltaPackedTsState{}, false
	}

	return DeltaPackedTsState{data: data, curTS: int64(first), offset: offset, seqCount: 1}, true //nolint:gosec
}

// Next decodes the next timestamp. remaining includes the timestamp being decoded.
func (s *DeltaPackedTsState) Next(remaining int) bool {
	if s.seqCount == 1 {
		zigzag, offset, ok := varint.DecodeU64(s.data, s.offset)
		if !ok {
			return false
		}

		s.prevDelta = varint.DecodeZigZag64(zigzag)
		s.curTS += s.prevDelta
		s.offset = offset
		s.seqCount++

		return true
	}

	if s.groupIdx == 0 {
		if s.offset >= len(s.data) {
			return false
		}
		s.control = s.data[s.offset]
		s.offset++
		s.groupLen = min(groupSize, remaining)
	}

	tag := (s.control >> (uint(s.groupIdx) * 2)) & 0x03 //nolint:gosec
	byteLen := groupVarintLengths[tag]
	if s.offset+byteLen > len(s.data) {
		return false
	}

	var zigzag uint64
	switch tag {
	case 0:
		zigzag = uint64(s.data[s.offset])
	case 1:
		zigzag = uint64(binary.LittleEndian.Uint16(s.data[s.offset:]))
	case 2:
		zigzag = uint64(binary.LittleEndian.Uint32(s.data[s.offset:]))
	case 3:
		zigzag = binary.LittleEndian.Uint64(s.data[s.offset:])
	default:
		return false
	}

	s.offset += byteLen
	s.prevDelta += varint.DecodeZigZag64(zigzag)
	s.curTS += s.prevDelta
	s.groupIdx++
	if s.groupIdx == s.groupLen {
		s.groupIdx = 0
	}

	return true
}

// Ts returns the most recently decoded timestamp.
func (s DeltaPackedTsState) Ts() int64 {
	return s.curTS
}

// All returns an iterator that yields all timestamps from Group Varint packed data.
//
// The decoder reads one control byte per group of 4 values, then uses typed
// fixed-width reads (Uint16, Uint32, Uint64) to decode each value without
// the per-byte continuation-bit loop of LEB128 varint.
//
// Parameters:
//   - data: Encoded byte slice from TimestampDeltaPackedEncoder.Bytes()
//   - count: Expected number of timestamps
//
// Returns:
//   - iter.Seq[int64]: Iterator yielding decoded timestamps
func (d TimestampDeltaPackedDecoder) All(data []byte, count int) iter.Seq[int64] { //nolint:cyclop // packed decode has intentional scalar tail and SIMD branches
	return func(yield func(int64) bool) {
		if len(data) == 0 || count <= 0 {
			return
		}

		// Decode first timestamp (full varint)
		first, offset, ok := varint.DecodeU64(data, 0)
		if !ok {
			return
		}

		curTS := int64(first) //nolint:gosec
		if !yield(curTS) {
			return
		}

		if count == 1 {
			return
		}

		// Decode second timestamp (zigzag + varint delta)
		zigzag, offset, ok := varint.DecodeU64(data, offset)
		if !ok {
			return
		}

		delta := varint.DecodeZigZag64(zigzag)
		curTS += delta
		if !yield(curTS) {
			return
		}

		prevDelta := delta
		remaining := count - 2

		// SIMD bulk path: decode into a scratch buffer, then yield from it.
		if shouldUseDeltaPackedDecodeSIMD(remaining) {
			var scratch [deltaPackedDecodeSIMDScratchSize]int64

			for remaining > 0 {
				n := min(remaining, deltaPackedDecodeSIMDScratchSize)

				bulk, bulkOK := decodeDeltaPackedIntoActive(
					scratch[:n], data[offset:], n, curTS, prevDelta,
				)
				if !bulkOK || bulk.produced == 0 {
					break
				}

				for _, ts := range scratch[:bulk.produced] {
					if !yield(ts) {
						return
					}
				}

				offset += bulk.consumed
				curTS = bulk.lastTS
				prevDelta = bulk.lastDelta
				remaining -= bulk.produced
			}

			if remaining == 0 {
				return
			}
		}

		// Scalar full-group path
		for remaining >= groupSize && offset < len(data) {
			var zz [groupSize]uint64
			var byteLen [groupSize]int

			consumed, ok2 := decodePackedGroupScalar(data, offset, &zz, &byteLen)
			if !ok2 {
				return
			}

			offset += consumed
			remaining -= groupSize

			for i := range groupSize {
				deltaOfDelta := varint.DecodeZigZag64(zz[i])
				prevDelta += deltaOfDelta
				curTS += prevDelta

				if !yield(curTS) {
					return
				}
			}
		}

		// Scalar tail: partial group (< 4 values)
		if remaining > 0 && offset < len(data) {
			cb := data[offset]
			offset++

			for i := range remaining {
				tag := (cb >> (uint(i) * 2)) & 0x03 //nolint:gosec // i is bounded by groupSize (0-3)
				byteLen := groupVarintLengths[tag]

				if offset+byteLen > len(data) {
					return
				}

				var zz uint64
				switch tag {
				case 0:
					zz = uint64(data[offset])
				case 1:
					zz = uint64(binary.LittleEndian.Uint16(data[offset:]))
				case 2:
					zz = uint64(binary.LittleEndian.Uint32(data[offset:]))
				case 3:
					zz = binary.LittleEndian.Uint64(data[offset:])
				default:
					return
				}
				offset += byteLen

				deltaOfDelta := varint.DecodeZigZag64(zz)
				prevDelta += deltaOfDelta
				curTS += prevDelta

				if !yield(curTS) {
					return
				}
			}
		}
	}
}

// DecodeAll decodes all timestamps from Group Varint packed data directly into the destination slice.
//
// This method is optimized for bulk decoding when the caller needs all values in a slice,
// avoiding the per-element yield overhead of the All() iterator.
//
// Parameters:
//   - data: Encoded byte slice from TimestampDeltaPackedEncoder.Bytes()
//   - count: Total number of timestamps in the encoded data
//   - dst: Pre-allocated destination slice (must have len >= count)
//
// Returns:
//   - int: Number of values successfully decoded into dst
func (d TimestampDeltaPackedDecoder) DecodeAll(data []byte, count int, dst []int64) int { //nolint:cyclop // packed decode has intentional scalar tail and SIMD branches
	if len(data) == 0 || count <= 0 || len(dst) < count {
		return 0
	}

	// Decode first timestamp (full varint)
	first, offset, ok := varint.DecodeU64(data, 0)
	if !ok {
		return 0
	}

	curTS := int64(first) //nolint:gosec
	dst[0] = curTS

	if count == 1 {
		return 1
	}

	// Decode second timestamp (zigzag + varint delta)
	zigzag, offset, ok := varint.DecodeU64(data, offset)
	if !ok {
		return 1
	}

	delta := varint.DecodeZigZag64(zigzag)
	curTS += delta
	dst[1] = curTS

	prevDelta := delta
	produced := 2
	remaining := count - 2

	// SIMD bulk path
	if shouldUseDeltaPackedDecodeSIMD(remaining) {
		bulk, ok2 := decodeDeltaPackedIntoActive(
			dst[produced:produced+remaining], data[offset:], remaining, curTS, prevDelta,
		)
		if ok2 && bulk.produced > 0 {
			produced += bulk.produced
			offset += bulk.consumed
			curTS = bulk.lastTS
			prevDelta = bulk.lastDelta
			remaining -= bulk.produced
		}
	}

	// Scalar full-group path
	for remaining >= groupSize && offset < len(data) {
		var zz [groupSize]uint64
		var byteLen [groupSize]int

		consumed, ok2 := decodePackedGroupScalar(data, offset, &zz, &byteLen)
		if !ok2 {
			return produced
		}

		offset += consumed
		remaining -= groupSize

		for i := range groupSize {
			deltaOfDelta := varint.DecodeZigZag64(zz[i])
			prevDelta += deltaOfDelta
			curTS += prevDelta
			dst[produced] = curTS
			produced++
		}
	}

	// Scalar tail: partial group (< 4 values)
	if remaining > 0 && offset < len(data) {
		cb := data[offset]
		offset++

		for i := range remaining {
			tag := (cb >> (uint(i) * 2)) & 0x03 //nolint:gosec // i is bounded by groupSize (0-3)
			byteLen := groupVarintLengths[tag]

			if offset+byteLen > len(data) {
				return produced
			}

			var zz uint64
			switch tag {
			case 0:
				zz = uint64(data[offset])
			case 1:
				zz = uint64(binary.LittleEndian.Uint16(data[offset:]))
			case 2:
				zz = uint64(binary.LittleEndian.Uint32(data[offset:]))
			case 3:
				zz = binary.LittleEndian.Uint64(data[offset:])
			default:
				return produced
			}
			offset += byteLen

			deltaOfDelta := varint.DecodeZigZag64(zz)
			prevDelta += deltaOfDelta
			curTS += prevDelta
			dst[produced] = curTS
			produced++
		}
	}

	return produced
}

// decodePackedGroupScalar reads one full 4-value Group Varint group from data[offset:],
// storing zigzag values in zz and byte widths in byteLen.
// Returns (total bytes consumed including control byte, ok).
func decodePackedGroupScalar(data []byte, offset int, zz *[groupSize]uint64, byteLen *[groupSize]int) (int, bool) {
	if offset >= len(data) {
		return 0, false
	}

	cb := data[offset]
	pos := offset + 1

	for i := range groupSize {
		tag := (cb >> (uint(i) * 2)) & 0x03 //nolint:gosec // i is bounded by groupSize (0-3)
		bl := groupVarintLengths[tag]
		byteLen[i] = bl

		if pos+bl > len(data) {
			return 0, false
		}

		switch tag {
		case 0:
			zz[i] = uint64(data[pos])
		case 1:
			zz[i] = uint64(binary.LittleEndian.Uint16(data[pos:]))
		case 2:
			zz[i] = uint64(binary.LittleEndian.Uint32(data[pos:]))
		case 3:
			zz[i] = binary.LittleEndian.Uint64(data[pos:])
		default:
			return 0, false
		}

		pos += bl
	}

	return pos - offset, true
}

// At returns the timestamp at the specified index.
//
// Parameters:
//   - data: Encoded byte slice from TimestampDeltaPackedEncoder.Bytes()
//   - index: Zero-based index of the timestamp to retrieve
//   - count: Total number of timestamps in the encoded data
//
// Returns:
//   - int64: The timestamp at the specified index
//   - bool: true if successfully decoded, false otherwise
func (d TimestampDeltaPackedDecoder) At(data []byte, index int, count int) (int64, bool) { //nolint:cyclop // inherent complexity of Group Varint decoding
	if index < 0 || index >= count || len(data) == 0 {
		return 0, false
	}

	// Decode first timestamp
	first, offset, ok := varint.DecodeU64(data, 0)
	if !ok {
		return 0, false
	}

	curTS := int64(first) //nolint:gosec
	if index == 0 {
		return curTS, true
	}

	// Decode second timestamp
	zigzag, offset, ok := varint.DecodeU64(data, offset)
	if !ok {
		return 0, false
	}

	delta := varint.DecodeZigZag64(zigzag)
	curTS += delta
	if index == 1 {
		return curTS, true
	}

	// Decode through Group Varint blocks until target index
	prevDelta := delta
	produced := 2
	target := index + 1

	// Process full groups that fit entirely within the target range
	for produced+groupSize <= target && offset < len(data) {
		cb := data[offset]
		offset++

		for i := range groupSize {
			tag := (cb >> (uint(i) * 2)) & 0x03 //nolint:gosec // i is bounded by groupSize (0-3)
			byteLen := groupVarintLengths[tag]
			if offset+byteLen > len(data) {
				return 0, false
			}

			var zz uint64
			switch tag {
			case 0:
				zz = uint64(data[offset])
			case 1:
				zz = uint64(binary.LittleEndian.Uint16(data[offset:]))
			case 2:
				zz = uint64(binary.LittleEndian.Uint32(data[offset:]))
			case 3:
				zz = binary.LittleEndian.Uint64(data[offset:])
			default:
				return 0, false
			}
			offset += byteLen

			deltaOfDelta := varint.DecodeZigZag64(zz)
			prevDelta += deltaOfDelta
			curTS += prevDelta
		}

		produced += groupSize
	}

	// Partial group: decode remaining values up to target
	if produced < target && offset < len(data) {
		cb := data[offset]
		offset++

		need := target - produced
		for i := range need {
			tag := (cb >> (uint(i) * 2)) & 0x03 //nolint:gosec // i is bounded by groupSize (0-3)
			byteLen := groupVarintLengths[tag]
			if offset+byteLen > len(data) {
				return 0, false
			}

			var zz uint64
			switch tag {
			case 0:
				zz = uint64(data[offset])
			case 1:
				zz = uint64(binary.LittleEndian.Uint16(data[offset:]))
			case 2:
				zz = uint64(binary.LittleEndian.Uint32(data[offset:]))
			case 3:
				zz = binary.LittleEndian.Uint64(data[offset:])
			default:
				return 0, false
			}
			offset += byteLen

			deltaOfDelta := varint.DecodeZigZag64(zz)
			prevDelta += deltaOfDelta
			curTS += prevDelta
			produced++
		}
	}

	if produced == target {
		return curTS, true
	}

	return 0, false
}

const (
	// batchEncodeMaxGroups is the maximum number of groups (4 values each) that can be
	// processed in a single batch call. Matches deltadelta.ChunkSize / groupSize.
	batchEncodeMaxGroups = deltadelta.ChunkSize / groupSize // 64

	// deltaPackedEncodeSIMDMinLen is the minimum number of delta-of-delta values
	// required before the SIMD encode path is used. Below this threshold, the scalar
	// per-group flushGroup path is faster due to SIMD setup overhead.
	//
	// Benchmarked crossover: SIMD-fused beats scalar at ~20 values in the full pipeline
	// (encoder allocation + header + DoD + serialize). 32 gives a safety margin.
	deltaPackedEncodeSIMDMinLen = 32
)

// shouldUseDeltaPackedEncodeSIMD reports whether the SIMD encode path should be used
// for the given number of remaining values.
func shouldUseDeltaPackedEncodeSIMD(count int) bool {
	return hasDeltaPackedEncodeSIMD() && count >= deltaPackedEncodeSIMDMinLen
}

// encodeDeltaPackedGroupsBatch encodes pre-computed delta-of-delta int64 values into
// Group Varint format in a single batch, eliminating per-group Grow() overhead.
//
// This is the fused pipeline: zigzag → tag-classify → control-byte → pack, batched
// across all groups with a single buffer pre-allocation.
//
// Parameters:
//   - buf: output byte buffer (data is appended)
//   - dods: pre-computed delta-of-delta values (len must be a multiple of groupSize)
//
// The caller must ensure len(dods) is a multiple of groupSize and <= deltadelta.ChunkSize.
func encodeDeltaPackedGroupsBatch(buf *pool.ByteBuffer, dods []int64) {
	nValues := len(dods)
	nGroups := nValues / groupSize
	if nGroups == 0 {
		return
	}

	nValues = nGroups * groupSize

	// --- Phase 1: Batch zigzag encode ---
	var zigzags [deltadelta.ChunkSize]uint64
	for i := range nValues {
		dod := dods[i]
		zigzags[i] = uint64((dod << 1) ^ (dod >> 63)) //nolint:gosec
	}

	// --- Phase 2: Batch tag classify + control bytes + total size ---
	var controlBytes [batchEncodeMaxGroups]byte
	totalPayload := 0

	for g := range nGroups {
		base := g * groupSize
		tag0 := encodeTag(zigzags[base])
		tag1 := encodeTag(zigzags[base+1])
		tag2 := encodeTag(zigzags[base+2])
		tag3 := encodeTag(zigzags[base+3])
		controlBytes[g] = tag0 | (tag1 << 2) | (tag2 << 4) | (tag3 << 6)
		totalPayload += groupVarintLengths[tag0] +
			groupVarintLengths[tag1] +
			groupVarintLengths[tag2] +
			groupVarintLengths[tag3]
	}

	// --- Phase 3: Single pre-allocation for entire batch ---
	// nGroups control bytes + totalPayload data bytes + 8 bytes slack for last PutUint64
	startLen := len(buf.B)
	needed := nGroups + totalPayload + 8
	buf.Grow(needed)
	buf.B = buf.B[:startLen+needed]

	// --- Phase 4: Pack all groups ---
	offset := startLen
	for g := range nGroups {
		base := g * groupSize
		cb := controlBytes[g]
		buf.B[offset] = cb
		offset++

		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], zigzags[base])
		offset += groupVarintLengths[(cb>>0)&0x03]

		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], zigzags[base+1])
		offset += groupVarintLengths[(cb>>2)&0x03]

		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], zigzags[base+2])
		offset += groupVarintLengths[(cb>>4)&0x03]

		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], zigzags[base+3])
		offset += groupVarintLengths[(cb>>6)&0x03]
	}

	// --- Phase 5: Trim to actual size (remove slack) ---
	buf.B = buf.B[:offset]
}

// encodeDeltaPackedGroupsScalar is the scalar fallback for encodeDeltaPackedGroupsSIMD.
// It encodes delta-of-delta values into Group Varint format using pure Go.
//
// Parameters:
//   - dst: output buffer, must have capacity >= nGroups * 33 bytes
//   - dods: delta-of-delta int64 values, len must be >= nGroups * 4
//   - nGroups: number of 4-value groups to encode
//
// Returns: bytes written to dst
func encodeDeltaPackedGroupsScalar(dst []byte, dods []int64, nGroups int) int {
	offset := 0

	for g := range nGroups {
		base := g * groupSize

		// Zigzag encode
		zz0 := uint64((dods[base] << 1) ^ (dods[base] >> 63))     //nolint:gosec
		zz1 := uint64((dods[base+1] << 1) ^ (dods[base+1] >> 63)) //nolint:gosec
		zz2 := uint64((dods[base+2] << 1) ^ (dods[base+2] >> 63)) //nolint:gosec
		zz3 := uint64((dods[base+3] << 1) ^ (dods[base+3] >> 63)) //nolint:gosec

		// Tag classify
		tag0 := encodeTag(zz0)
		tag1 := encodeTag(zz1)
		tag2 := encodeTag(zz2)
		tag3 := encodeTag(zz3)

		// Control byte
		cb := tag0 | (tag1 << 2) | (tag2 << 4) | (tag3 << 6)
		dst[offset] = cb
		offset++

		// Pack values (branchless 8-byte write, advance by actual width)
		binary.LittleEndian.PutUint64(dst[offset:offset+8], zz0)
		offset += groupVarintLengths[tag0]
		binary.LittleEndian.PutUint64(dst[offset:offset+8], zz1)
		offset += groupVarintLengths[tag1]
		binary.LittleEndian.PutUint64(dst[offset:offset+8], zz2)
		offset += groupVarintLengths[tag2]
		binary.LittleEndian.PutUint64(dst[offset:offset+8], zz3)
		offset += groupVarintLengths[tag3]
	}

	return offset
}
