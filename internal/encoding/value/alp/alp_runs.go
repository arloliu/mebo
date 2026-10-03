package alp

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
)

// Runs layout (scheme 3), written only by an encoder built with NewNumericALPRLEEncoder:
//
//	[scheme=3:1][nRuns:4, blob byte order]
//	[bitmap: ceil(count/8) bytes, LSB-first, little-endian regardless of byte order]
//	[nested: a scheme 0, 1 or 2 ALP column holding the nRuns run values]
//
// Bit i of the bitmap is set when point i starts a new run,
// so bit 0 is always set and the padding bits past count are zero.
// Run equality is bitwise (math.Float64bits), so −0.0 and +0.0 stay distinct and NaN payloads survive.
// The nested column never uses scheme 3 itself.
const (
	// ALPRLEMaxSchemeByte is the highest scheme byte value a TypeALPRLE column may declare.
	// Scheme 3 (runs) is valid only under TypeALPRLE; TypeALP columns stay bounded by ALPMaxSchemeByte.
	ALPRLEMaxSchemeByte = alpSchemeRuns

	alpSchemeRuns byte = 3

	// alpRunsHeaderSize is the runs header after the scheme byte: the uint32 run count.
	alpRunsHeaderSize = 4
)

// NewNumericALPRLEEncoder creates an ALP encoder that may also write the runs layout (scheme 3).
//
// For each column it encodes the plain ALP column first,
// and keeps a runs column instead only when that is strictly smaller in bytes.
// Its output is therefore never larger than NewNumericALPEncoder's for the same column,
// and is byte-identical whenever the plain column wins.
// The output must only be read under an encoding type that allows scheme 3.
//
// Parameters:
//   - engine: byte order for the column headers and exception entries
//
// Returns:
//   - *NumericALPEncoder: the encoder
func NewNumericALPRLEEncoder(engine endian.EndianEngine) *NumericALPEncoder {
	enc := NewNumericALPEncoder(engine)
	enc.runs = true

	return enc
}

// ValidateRunsColumn checks the runs envelope of a scheme-3 column body (the bytes after the scheme byte).
//
// It checks, in order: the fixed header is present, 1 ≤ nRuns ≤ count,
// the bitmap and a nested scheme byte are present, bit 0 is set, padding bits are zero,
// the bitmap's popcount equals nRuns, and the nested scheme is 0, 1 or 2.
// It does not check the nested column's own structure;
// the caller validates nested as a plain ALP column of nRuns values,
// including the platform column-size bound that keeps the main and RD decoders' count × width arithmetic representable on 32-bit.
//
// Parameters:
//   - body: the column without its scheme byte
//   - count: the column's point count from the index
//   - engine: byte order of the nRuns field
//
// Returns:
//   - int: the run count
//   - []byte: the nested ALP column, starting at its scheme byte
//   - error: wraps errs.ErrInvalidALPColumn if any check fails
func ValidateRunsColumn(body []byte, count int, engine endian.EndianEngine) (int, []byte, error) {
	if len(body) < alpRunsHeaderSize {
		return 0, nil, fmt.Errorf("%w: ALP runs column body of %d bytes, want at least %d (fixed header)",
			errs.ErrInvalidALPColumn, len(body), alpRunsHeaderSize)
	}

	// Keep nRuns unsigned until bounded: int(uint32) is negative on 32-bit platforms for values >= 1<<31.
	nRuns := uint64(engine.Uint32(body[:alpRunsHeaderSize]))
	if count < 1 || nRuns < 1 || nRuns > uint64(count) {
		return 0, nil, fmt.Errorf("%w: ALP runs column has %d runs, want 1..%d",
			errs.ErrInvalidALPColumn, nRuns, count)
	}

	// count fits an int, so its byte-rounded bitmap length does too.
	bmLen := (uint64(count) + 7) / 8
	if uint64(len(body)) < alpRunsHeaderSize+bmLen+1 {
		return 0, nil, fmt.Errorf("%w: ALP runs column body of %d bytes, want at least %d (bitmap of %d points and a nested scheme byte)",
			errs.ErrInvalidALPColumn, len(body), alpRunsHeaderSize+bmLen+1, count)
	}

	bm := body[alpRunsHeaderSize : alpRunsHeaderSize+int(bmLen)] //nolint:gosec // bmLen < len(body)
	if bm[0]&1 == 0 {
		return 0, nil, fmt.Errorf("%w: ALP runs bitmap does not start a run at point 0", errs.ErrInvalidALPColumn)
	}

	if tail := uint(count & 7); tail != 0 && bm[len(bm)-1]>>tail != 0 {
		return 0, nil, fmt.Errorf("%w: ALP runs bitmap has bits set past point %d", errs.ErrInvalidALPColumn, count)
	}

	ones := 0
	for _, b := range bm {
		ones += bits.OnesCount8(b)
	}
	if uint64(ones) != nRuns {
		return 0, nil, fmt.Errorf("%w: ALP runs bitmap starts %d runs, header declares %d",
			errs.ErrInvalidALPColumn, ones, nRuns)
	}

	nested := body[alpRunsHeaderSize+int(bmLen):] //nolint:gosec // bmLen < len(body)
	if nested[0] > ALPMaxSchemeByte {
		return 0, nil, fmt.Errorf("%w: ALP runs column nests scheme %d, want 0 (main), 1 (rd), or 2 (raw)",
			errs.ErrInvalidALPColumn, nested[0])
	}

	return int(nRuns), nested, nil //nolint:gosec // nRuns <= count, an int
}

// alpRunsBitmapLen returns ceil(count/8), the bitmap's byte length, without overflowing for any count >= 0.
// The usual (count+7)/8 wraps for counts near math.MaxInt, which a 32-bit blob can declare.
func alpRunsBitmapLen(count int) int {
	return count/8 + (count%8+7)/8
}

// alpRunsWord returns bitmap word w (bits 64w..64w+63), byte-assembling a short final word.
// The decoders pass the bitmap together with the nested column that follows it,
// so a valid column always takes the single-load path:
// the nested column is at least 9 bytes, which covers a final word of up to 7 bytes.
// Bits read past the bitmap are never used, because every caller masks or stops at its last point.
func alpRunsWord(bm []byte, w int) uint64 {
	off := w * 8
	if off+8 <= len(bm) {
		return binary.LittleEndian.Uint64(bm[off : off+8])
	}

	var word uint64
	for k := 0; off+k < len(bm) && k < 8; k++ {
		word |= uint64(bm[off+k]) << (8 * k)
	}

	return word
}

// alpRunsRank returns the number of set bits in bitmap bits 0..i, which is the 1-based run index of point i.
func alpRunsRank(bm []byte, i int) int {
	w := i >> 6
	r := 0
	for k := range w {
		r += bits.OnesCount64(alpRunsWord(bm, k))
	}

	return r + bits.OnesCount64(alpRunsWord(bm, w)<<(63-uint(i&63)))
}

// alpRunsWorthTrying is the encoder's pruning rule.
// Dropping a repeated point saves at most its cost in the plain column:
// its packed bits plus its exception entry, if any.
// The runs layout is tried only when that sum over all repeated points
// exceeds the runs overhead, 8 × (5 + ceil(count/8)) bits.
// The rule ignores second-order effects such as the nested column choosing a different (e, f).
func alpRunsWorthTrying(values []float64, col []byte, engine endian.EndianEngine) bool {
	n := len(values)
	repeats := 0
	for i := 1; i < n; i++ {
		if math.Float64bits(values[i]) == math.Float64bits(values[i-1]) {
			repeats++
		}
	}
	if repeats == 0 {
		return false
	}

	body := col[1:]
	var perPoint, excSize, excBits, excStart, nExc int
	switch col[0] {
	case alpSchemeMain:
		width := int(body[2])
		perPoint = width
		nExc = int(engine.Uint32(body[3:7]))
		excStart, excSize, excBits = 15+(n*width+7)/8, 12, alpExcBitsMain
	case alpSchemeRD:
		rbw, codeBits, nDict := int(body[0]), int(body[1]), int(body[2])
		perPoint = rbw + codeBits
		nExc = int(engine.Uint32(body[3:7]))
		excStart, excSize, excBits = 7+2*nDict+(n*codeBits+7)/8+(n*rbw+7)/8, 6, alpExcBitsRD
	default:
		perPoint = 64
	}

	saved := repeats * perPoint
	for k := range nExc {
		p := int(engine.Uint32(body[excStart+k*excSize:]))
		if p > 0 && math.Float64bits(values[p]) == math.Float64bits(values[p-1]) {
			saved += excBits
		}
	}

	return saved > 8*(1+alpRunsHeaderSize+alpRunsBitmapLen(n))
}

// alpRunsParse splits a scheme-3 column (scheme byte included) into nRuns, bitmap and nested column.
// The returned bitmap runs on into the nested column (see alpRunsWord);
// only its first ceil(count/8) bytes are the bitmap.
// Like the other decode paths it trusts the column; the blob validates it at open.
func (d NumericALPDecoder) alpRunsParse(data []byte, count int) (nRuns int, bm, nested []byte) {
	nRuns = int(d.engine.Uint32(data[1 : 1+alpRunsHeaderSize]))
	bmEnd := 1 + alpRunsHeaderSize + alpRunsBitmapLen(count)

	return nRuns, data[1+alpRunsHeaderSize:], data[bmEnd:]
}

// decodePlainInto decodes a scheme 0, 1 or 2 column into dst, writing min(count, len(dst)) values.
// It never decodes scheme 3, so a corrupt nested column cannot recurse.
func (d NumericALPDecoder) decodePlainInto(data []byte, count int, dst []float64) int {
	if count <= 0 || len(data) == 0 {
		return 0
	}
	switch data[0] {
	case alpSchemeRaw:
		n := min(count, len(dst))
		off := 1
		for i := range n {
			dst[i] = math.Float64frombits(d.engine.Uint64(data[off : off+8]))
			off += 8
		}

		return n
	case alpSchemeMain:
		return d.decodeMainInto(data[1:], count, dst)
	case alpSchemeRD:
		return d.decodeRDInto(data[1:], count, dst)
	default:
		return 0
	}
}

// decodeRunsInto decodes a scheme-3 column into dst, writing min(count, len(dst)) values, with no scratch.
//
// For n = min(count, len(dst)) points it needs the first r = rank(n-1) run values.
// It decodes them into dst[n-r:n], then expands forward in place.
// Point i reads run slot n-r+rank(i)-1, which is never below i,
// because the repeats among points 0..i never outnumber the n-r repeats among points 0..n-1.
// So every slot is read before the expansion overwrites it.
func (d NumericALPDecoder) decodeRunsInto(data []byte, count int, dst []float64) int {
	n := min(count, len(dst))
	if n == 0 {
		return 0
	}
	nRuns, bm, nested := d.alpRunsParse(data, count)
	off := n - alpRunsRank(bm, n-1)
	d.decodePlainInto(nested, nRuns, dst[off:n])

	// Word bounds are derived from n without forming w*64+64 or similar sums,
	// which wrap for n near math.MaxInt on 32-bit platforms.
	idx := off - 1
	for w := range n/64 + (n%64+63)/64 {
		base := w * 64
		word := alpRunsWord(bm, w)
		out := dst[base : base+min(64, n-base)]
		for j := range out {
			idx += int(word & 1)
			word >>= 1
			out[j] = dst[idx]
		}
	}

	return n
}

// allRuns streams a scheme-3 column: it walks the nested column's values once
// and yields each one for every point of its run, stopping after count points.
func (d NumericALPDecoder) allRuns(data []byte, count int, yield func(float64) bool) {
	nRuns, bm, nested := d.alpRunsParse(data, count)
	pos := 0
	emit := func(v float64) bool {
		for {
			if !yield(v) {
				pos = count

				return false
			}
			pos++
			if pos >= count {
				return false
			}
			if bm[pos>>3]>>(pos&7)&1 != 0 {
				return true
			}
		}
	}

	switch nested[0] {
	case alpSchemeRaw:
		off := 1
		for range nRuns {
			if !emit(math.Float64frombits(d.engine.Uint64(nested[off : off+8]))) {
				return
			}
			off += 8
		}
	case alpSchemeMain:
		d.allMain(nested[1:], nRuns, emit)
	case alpSchemeRD:
		d.allRD(nested[1:], nRuns, emit)
	default:
	}
}

// atRuns looks up one point of a scheme-3 column: rank the bitmap, then read that run from the nested column.
// It parses the header inline rather than through alpRunsParse, which does not inline.
func (d NumericALPDecoder) atRuns(data []byte, index, count int) (float64, bool) {
	bm := data[1+alpRunsHeaderSize:]
	run := alpRunsRank(bm, index) - 1
	nRuns := int(d.engine.Uint32(data[1 : 1+alpRunsHeaderSize]))
	nested := bm[alpRunsBitmapLen(count):]
	if run < 0 || run >= nRuns || len(nested) == 0 {
		return 0, false
	}

	switch nested[0] {
	case alpSchemeRaw:
		off := 1 + run*8

		return math.Float64frombits(d.engine.Uint64(nested[off : off+8])), true
	case alpSchemeMain:
		return d.atMain(nested[1:], run, nRuns), true
	case alpSchemeRD:
		return d.atRD(nested[1:], run, nRuns), true
	default:
		return 0, false
	}
}

// encodeColumnRuns encodes the plain column, then replaces it with the runs layout when that is strictly smaller.
// It never writes scheme 3 for fewer than two points.
func (e *NumericALPEncoder) encodeColumnRuns(values []float64) {
	start := len(e.buf.B)
	e.encodeColumn(values)

	n := len(values)
	if n < 2 {
		return
	}
	if !alpRunsWorthTrying(values, e.buf.B[start:], e.engine) {
		return
	}

	runVals := e.runScratch[:0]
	mid := len(e.buf.B)
	e.buf.B = append(e.buf.B, alpSchemeRuns, 0, 0, 0, 0)
	bmStart := len(e.buf.B)
	e.buf.B = append(e.buf.B, make([]byte, alpRunsBitmapLen(n))...)
	bm := e.buf.B[bmStart:]
	prev := math.Float64bits(values[0])
	runVals = append(runVals, values[0])
	bm[0] = 1
	for i := 1; i < n; i++ {
		b := math.Float64bits(values[i])
		if b != prev {
			runVals = append(runVals, values[i])
			bm[i>>3] |= 1 << uint(i&7)
			prev = b
		}
	}
	e.runScratch = runVals
	e.engine.PutUint32(e.buf.B[mid+1:mid+1+alpRunsHeaderSize], uint32(len(runVals))) //nolint:gosec // len(runVals) <= n, and counts fit uint32 on the wire
	e.seeded = true
	e.encodeColumn(runVals)
	e.seeded = false

	runsLen := len(e.buf.B) - mid
	if runsLen < mid-start {
		copy(e.buf.B[start:], e.buf.B[mid:])
		e.buf.B = e.buf.B[:start+runsLen]
	} else {
		e.buf.B = e.buf.B[:mid]
	}
}
