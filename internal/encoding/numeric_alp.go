package encoding

import (
	"encoding/binary"
	"iter"
	"math"
	"math/bits"
	"sort"

	"github.com/arloliu/mebo/encoding"
	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/pool"
)

// ALP (Adaptive Lossless floating-Point) value encoding.
//
// Reference: Afroozeh, Kuffó, Boncz — SIGMOD 2024 (cwida/ALP). Two schemes,
// chosen per column by estimated size:
//
//   - ALP main: most real-world float64s are decimals stored as doubles. Find
//     exponents (e,f) so digit = round(v·10^e·10^-f) is an integer that decodes
//     back exactly; Frame-of-Reference + bit-pack the digits; values that don't
//     round-trip are stored as exceptions. Huge wins on decimal data.
//   - ALP-RD: for genuine full-precision doubles, split each 64-bit pattern into
//     a dictionary-coded left part + bit-packed right part.
//   - raw: escape hatch when neither beats storing raw float64.
//
// On-disk column layout (count comes from the index):
//
//	[scheme:1]
//	main: [e:1][f:1][width:1][nExc:4][min:8] [FOR codes: count×width bits]
//	      [exceptions: nExc×(pos:4, value:8)]
//	rd:   [rbw:1][codeBits:1][nDict:1][nExc:4] [dict: nDict×2]
//	      [left codes: count×codeBits bits] [right: count×rbw bits]
//	      [exceptions: nExc×(pos:4, left:2)]
//	raw:  [count×8 raw float64]
const (
	alpSchemeMain byte = 0
	alpSchemeRD   byte = 1
	alpSchemeRaw  byte = 2

	alpMaxExponent   = 18
	alpRDCutLimit    = 16 // left part is 1..16 bits
	alpRDMaxDictSize = 8  // ≤8 dictionary entries
)

var alpPow10 = [...]float64{
	1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
	1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18,
}

var alpInvPow10 = [...]float64{
	1e0, 1e-1, 1e-2, 1e-3, 1e-4, 1e-5, 1e-6, 1e-7, 1e-8, 1e-9,
	1e-10, 1e-11, 1e-12, 1e-13, 1e-14, 1e-15, 1e-16, 1e-17, 1e-18,
}

// alpEncodeDigit computes i = round(v·10^e·10^-f); ok is false if v does not
// round-trip bit-exactly (an exception).
func alpEncodeDigit(v float64, e, f int) (int64, bool) {
	scaled := v * alpPow10[e] * alpInvPow10[f]
	r := math.Round(scaled)
	if math.Abs(r) >= 9.2e18 {
		return 0, false
	}
	i := int64(r)
	// Bit-level comparison (not float !=) so e.g. negative zero, which compares
	// equal to +0.0, is treated as an exception and preserved bit-exactly.
	if math.Float64bits(float64(i)*alpPow10[f]*alpInvPow10[e]) != math.Float64bits(v) {
		return 0, false
	}

	return i, true
}

func alpCodeBits(n int) int {
	if n <= 1 {
		return 1
	}

	return bits.Len64(uint64(n - 1)) //nolint:gosec
}

// ---- encoder ----

type NumericALPEncoder struct {
	buf      *pool.ByteBuffer
	engine   endian.EndianEngine
	count    int
	seqCount int
	pending  []float64
	flushed  bool
}

var _ encoding.ColumnarEncoder[float64] = (*NumericALPEncoder)(nil)

func NewNumericALPEncoder(engine endian.EndianEngine) *NumericALPEncoder {
	return &NumericALPEncoder{engine: engine, buf: pool.GetBlobBuffer()}
}

func (e *NumericALPEncoder) Write(value float64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write after Finish()")
	}
	e.count++
	e.seqCount++
	e.flushed = false
	e.pending = append(e.pending, value)
}

func (e *NumericALPEncoder) WriteSlice(values []float64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write after Finish()")
	}
	if len(values) == 0 {
		return
	}
	e.count += len(values)
	e.seqCount += len(values)
	e.flushed = false
	e.pending = append(e.pending, values...)
}

func (e *NumericALPEncoder) Bytes() []byte {
	if e.buf == nil {
		panic("encoder already finished - cannot access bytes after Finish()")
	}
	e.flush()

	return e.buf.Bytes()
}

func (e *NumericALPEncoder) flush() {
	if e.flushed || e.seqCount == 0 {
		return
	}
	e.encodeColumn(e.pending)
	e.flushed = true
}

const (
	alpSamplesPerVector = 32
	alpMainHeaderBits   = 8 + 8 + 8 + 32 + 64 // e,f,width,nExc,min
	alpExcBitsMain      = 32 + 64             // pos + raw value
	alpRDHeaderBits     = 8 + 8 + 8 + 32      // rbw,codeBits,nDict,nExc
	alpExcBitsRD        = 32 + 16             // pos + left value
)

func alpSampleStride(n int) int {
	if n > alpSamplesPerVector {
		return n / alpSamplesPerVector
	}

	return 1
}

// encodeColumn picks the smaller of ALP main / ALP-RD / raw and appends it. Each
// scheme's parameters are searched ONCE on a ~32-value sample and reused for both
// the size estimate and the encoding (no redundant exhaustive re-search).
func (e *NumericALPEncoder) encodeColumn(values []float64) {
	n := len(values)
	stride := alpSampleStride(n)

	ee, ff := alpBestEF(values, stride)
	main := alpMainStats(values, ee, ff)
	mainBits := math.MaxInt
	if main.ok {
		mainBits = alpMainHeaderBits + n*main.width + main.nExc*alpExcBitsMain
	}

	// Fast path: ALP main with zero exceptions means clean decimal data that ALP
	// main compresses far better than ALP-RD ever could — skip the costly RD
	// dictionary search entirely.
	if main.ok && main.nExc == 0 && mainBits <= n*64 {
		e.buf.B = append(e.buf.B, alpSchemeMain)
		e.encodeMain(values, ee, ff, main.mn, main.width)

		return
	}

	rd := alpRDPlan(values, stride)
	rdBits := alpRDHeaderBits + len(rd.dict)*16 + n*alpCodeBits(len(rd.dict)) + n*rd.rbw + rd.nExc*alpExcBitsRD

	rawBits := n * 64

	switch {
	case main.ok && mainBits <= rdBits && mainBits <= rawBits:
		e.buf.B = append(e.buf.B, alpSchemeMain)
		e.encodeMain(values, ee, ff, main.mn, main.width)
	case rdBits <= rawBits:
		e.buf.B = append(e.buf.B, alpSchemeRD)
		e.encodeRD(values, rd.rbw, rd.dict, rd.codeOf)
	default:
		e.buf.B = append(e.buf.B, alpSchemeRaw)
		e.encodeRaw(values)
	}
}

// alpMainCand holds ALP-main parameters computed over a full column.
type alpMainCand struct {
	mn    int64
	width int
	nExc  int
	ok    bool
}

// alpRDCand holds ALP-RD parameters computed for a full column.
type alpRDCand struct {
	rbw    int
	dict   []uint64
	codeOf map[uint64]int
	nExc   int
}

func (e *NumericALPEncoder) encodeRaw(values []float64) {
	for _, v := range values {
		e.buf.B = e.engine.AppendUint64(e.buf.B, math.Float64bits(v))
	}
}

// ---- ALP main ----

// alpBestEF searches (e,f), f<=e, minimizing estimated size over a strided sample.
func alpBestEF(values []float64, stride int) (bestE, bestF int) {
	best := math.MaxFloat64
	for e := 0; e <= alpMaxExponent; e++ {
		// Hoist the e-indexed table loads out of the f loop. pe/ie/pf/iff are the
		// same table values bound to locals, so every rounded product below is
		// bit-identical to alpPow10[e]*alpInvPow10[f] (FP is not associative — the
		// multiply ORDER must match alpEncodeDigit exactly; binding to locals does
		// not reorder anything).
		pe := alpPow10[e]
		ie := alpInvPow10[e]
		for f := 0; f <= e; f++ {
			pf := alpPow10[f]
			iff := alpInvPow10[f]
			var nExc, cnt int
			mn := int64(math.MaxInt64)
			mx := int64(math.MinInt64)
			pruned := false
			for i := 0; i < len(values); i += stride {
				cnt++
				// Fast estimate: plain float compare (the bit-exact check is only
				// needed in the final encode; this only steers (e,f) selection).
				v := values[i]
				r := math.Round(v * pe * iff)
				// Exception when out of int64 range or the round-trip disagrees.
				// Prune: est = cnt*width + nExc*96 >= nExc*96, so once nExc*96 >= best
				// this (e,f) cannot beat the incumbent — skip the rest of the sample.
				// Only candidates whose est >= best are pruned (they would never
				// update best), so the selection is unchanged.
				if math.Abs(r) >= 9.2e18 {
					nExc++
					if float64(nExc)*96 >= best {
						pruned = true

						break
					}

					continue
				}
				d := int64(r)
				if float64(d)*pf*ie != v {
					nExc++
					if float64(nExc)*96 >= best {
						pruned = true

						break
					}

					continue
				}
				if d < mn {
					mn = d
				}
				if d > mx {
					mx = d
				}
			}
			if pruned {
				continue
			}
			width := 0
			if nExc < cnt && mx >= mn {
				width = bits.Len64(uint64(mx - mn)) //nolint:gosec
			}
			est := float64(cnt*width + nExc*96)
			if est < best {
				best = est
				bestE, bestF = e, f
			}
		}
	}

	return bestE, bestF
}

// alpMainStats computes the FOR minimum, bit width, and exception count for the
// chosen (e,f) over ALL values. ok is false if every value is an exception.
func alpMainStats(values []float64, ee, ff int) alpMainCand {
	n := len(values)
	mn := int64(math.MaxInt64)
	mx := int64(math.MinInt64)
	nExc := 0
	for _, v := range values {
		d, good := alpEncodeDigit(v, ee, ff)
		if !good {
			nExc++
			continue
		}
		if d < mn {
			mn = d
		}
		if d > mx {
			mx = d
		}
	}
	if nExc == n {
		return alpMainCand{nExc: nExc, ok: false}
	}
	width := 0
	if mx >= mn {
		width = bits.Len64(uint64(mx - mn)) //nolint:gosec
	}

	return alpMainCand{mn: mn, width: width, nExc: nExc, ok: true}
}

func (e *NumericALPEncoder) encodeMain(values []float64, ee, ff int, mn int64, width int) {
	n := len(values)
	codes := make([]uint64, n)
	var excPos []int
	for i, v := range values {
		d, ok := alpEncodeDigit(v, ee, ff)
		if !ok {
			codes[i] = 0
			excPos = append(excPos, i)

			continue
		}
		codes[i] = uint64(d - mn) //nolint:gosec
	}

	eng := e.engine
	e.buf.B = append(e.buf.B, byte(ee), byte(ff), byte(width))
	e.buf.B = eng.AppendUint32(e.buf.B, uint32(len(excPos))) //nolint:gosec
	e.buf.B = eng.AppendUint64(e.buf.B, uint64(mn))          //nolint:gosec
	e.buf.B = alpPackBits(e.buf.B, codes, width)
	for _, i := range excPos {
		e.buf.B = eng.AppendUint32(e.buf.B, uint32(i)) //nolint:gosec
		e.buf.B = eng.AppendUint64(e.buf.B, math.Float64bits(values[i]))
	}
}

// ---- ALP-RD ----

func alpRDBuildDict(patterns []uint64, rbw int) (dict []uint64, codeOf map[uint64]int) {
	freq := make(map[uint64]int, len(patterns))
	for _, p := range patterns {
		freq[p>>uint(rbw)]++ //nolint:gosec
	}
	type lv struct {
		left  uint64
		count int
	}
	lvs := make([]lv, 0, len(freq))
	for l, c := range freq {
		lvs = append(lvs, lv{l, c})
	}
	sort.Slice(lvs, func(i, j int) bool {
		if lvs[i].count != lvs[j].count {
			return lvs[i].count > lvs[j].count
		}

		return lvs[i].left < lvs[j].left
	})
	nDict := min(alpRDMaxDictSize, len(lvs))
	dict = make([]uint64, nDict)
	codeOf = make(map[uint64]int, nDict)
	for i := range nDict {
		dict[i] = lvs[i].left
		codeOf[lvs[i].left] = i
	}

	return dict, codeOf
}

// alpRDBestCut returns the right bit width minimizing the estimated ALP-RD size,
// and that estimated size in bits.
func alpRDBestCut(patterns []uint64) (rbw, totalBits int) {
	best := math.MaxInt
	for i := 1; i <= alpRDCutLimit; i++ {
		r := 64 - i
		dict, codeOf := alpRDBuildDict(patterns, r)
		ex := 0
		for _, p := range patterns {
			if _, ok := codeOf[p>>uint(r)]; !ok { //nolint:gosec
				ex++
			}
		}
		codeBits := alpCodeBits(len(dict))
		n := len(patterns)
		total := 8 + 8 + 8 + 32 + len(dict)*16 + n*codeBits + n*r + ex*(32+16)
		if total < best {
			best = total
			rbw, totalBits = r, total
		}
	}

	return rbw, totalBits
}

// alpRDPlan searches the right bit width on a strided sample, then builds the
// dictionary and counts exceptions over ALL values at the chosen cut.
func alpRDPlan(values []float64, stride int) alpRDCand {
	n := len(values)
	sample := make([]uint64, 0, n/stride+1)
	for i := 0; i < n; i += stride {
		sample = append(sample, math.Float64bits(values[i]))
	}
	rbw, _ := alpRDBestCut(sample)

	full := make([]uint64, n)
	for i, v := range values {
		full[i] = math.Float64bits(v)
	}
	dict, codeOf := alpRDBuildDict(full, rbw)
	nExc := 0
	for _, p := range full {
		if _, ok := codeOf[p>>uint(rbw)]; !ok { //nolint:gosec
			nExc++
		}
	}

	return alpRDCand{rbw: rbw, dict: dict, codeOf: codeOf, nExc: nExc}
}

func (e *NumericALPEncoder) encodeRD(values []float64, rbw int, dict []uint64, codeOf map[uint64]int) {
	n := len(values)
	codeBits := alpCodeBits(len(dict))
	rightMask := (uint64(1) << uint(rbw)) - 1 //nolint:gosec

	leftCodes := make([]uint64, n)
	rights := make([]uint64, n)
	var excPos []uint32
	var excLeft []uint16
	for i, v := range values {
		p := math.Float64bits(v)
		left := p >> uint(rbw) //nolint:gosec
		rights[i] = p & rightMask
		if c, ok := codeOf[left]; ok {
			leftCodes[i] = uint64(c) //nolint:gosec
		} else {
			leftCodes[i] = 0
			excPos = append(excPos, uint32(i))      //nolint:gosec
			excLeft = append(excLeft, uint16(left)) //nolint:gosec
		}
	}

	eng := e.engine
	e.buf.B = append(e.buf.B, byte(rbw), byte(codeBits), byte(len(dict)))
	e.buf.B = eng.AppendUint32(e.buf.B, uint32(len(excPos))) //nolint:gosec
	for _, d := range dict {
		e.buf.B = eng.AppendUint16(e.buf.B, uint16(d)) //nolint:gosec
	}
	e.buf.B = alpPackBits(e.buf.B, leftCodes, codeBits)
	e.buf.B = alpPackBits(e.buf.B, rights, rbw)
	for i := range excPos {
		e.buf.B = eng.AppendUint32(e.buf.B, excPos[i])
		e.buf.B = eng.AppendUint16(e.buf.B, excLeft[i])
	}
}

func (e *NumericALPEncoder) Len() int { return e.count }

func (e *NumericALPEncoder) Size() int {
	if e.buf == nil {
		panic("encoder already finished - cannot access size after Finish()")
	}

	return e.buf.Len()
}

func (e *NumericALPEncoder) Reset() {
	e.seqCount = 0
	e.pending = e.pending[:0]
	e.flushed = false
}

func (e *NumericALPEncoder) Finish() {
	if e.buf != nil {
		pool.PutBlobBuffer(e.buf)
		e.buf = nil
	}
	e.count = 0
	e.seqCount = 0
	e.pending = nil
	e.flushed = false
}

// alpPackBits appends codes bit-packed LSB-first at the given width. It packs
// word-at-a-time through a 64-bit accumulator, flushing whole bytes, which is
// byte-identical to a naive bit-by-bit packer but ~width× fewer operations.
func alpPackBits(dst []byte, codes []uint64, width int) []byte {
	if width == 0 {
		return dst
	}
	start := len(dst)
	nbytes := (len(codes)*width + 7) / 8
	dst = append(dst, make([]byte, nbytes)...)

	mask := ^uint64(0)
	if width < 64 {
		mask = (uint64(1) << width) - 1
	}

	pos := start
	var acc uint64 // pending bits, valid in the low nbits
	nbits := 0     // invariant: < 8 at loop top
	for _, c := range codes {
		c &= mask
		acc |= c << nbits
		if nbits+width >= 64 {
			// acc now holds 64 valid bits; the high (nbits+width-64) bits of c were
			// dropped by the shift. Emit the 8 full bytes, then keep the dropped bits.
			dst[pos] = byte(acc)
			dst[pos+1] = byte(acc >> 8)
			dst[pos+2] = byte(acc >> 16)
			dst[pos+3] = byte(acc >> 24)
			dst[pos+4] = byte(acc >> 32)
			dst[pos+5] = byte(acc >> 40)
			dst[pos+6] = byte(acc >> 48)
			dst[pos+7] = byte(acc >> 56)
			pos += 8
			acc = c >> (64 - nbits) // dropped high bits (0 when nbits+width==64)
			nbits += width - 64
		} else {
			nbits += width
			for nbits >= 8 {
				dst[pos] = byte(acc)
				acc >>= 8
				pos++
				nbits -= 8
			}
		}
	}
	for nbits > 0 {
		dst[pos] = byte(acc)
		acc >>= 8
		pos++
		nbits -= 8
	}

	return dst
}

// ---- decoder ----

type NumericALPDecoder struct {
	engine endian.EndianEngine
}

var _ encoding.ColumnarDecoder[float64] = NumericALPDecoder{}

func NewNumericALPDecoder(engine endian.EndianEngine) NumericALPDecoder {
	return NumericALPDecoder{engine: engine}
}

// alpReadBitsFast reads width bits LSB-first at absolute bitpos. The hot path is
// a single unaligned 8-byte load + shift + mask (an intrinsic MOVQ) and is small
// enough to inline; the tail (<8 readable bytes) and the rare width-near-64 case
// (the field straddles the 8-byte window) fall through to alpReadBitsSlow.
// Engine-independent — matches alpPackBits LSB-first byte order.
func alpReadBitsFast(src []byte, bitpos, width int, mask uint64) uint64 {
	bp := bitpos >> 3
	sh := bitpos & 7
	if bp+8 <= len(src) && sh+width <= 64 {
		return (binary.LittleEndian.Uint64(src[bp:bp+8]) >> sh) & mask
	}

	return alpReadBitsSlow(src, bitpos, width, mask)
}

// alpReadBitsSlow is the cold fallback for alpReadBitsFast: it byte-assembles the
// window (tail-safe) and applies a second-word fixup when the field straddles the
// 64-bit boundary (sh+width > 64, reachable only for large widths such as RD rbw).
func alpReadBitsSlow(src []byte, bitpos, width int, mask uint64) uint64 {
	bp := bitpos >> 3
	sh := bitpos & 7
	clen := len(src)
	var w uint64
	for k := 0; bp+k < clen && k < 8; k++ {
		w |= uint64(src[bp+k]) << (8 * k)
	}
	code := (w >> sh) & mask
	if sh+width > 64 {
		rem := sh + width - 64
		bp2 := bp + 8
		var w2 uint64
		for k := 0; bp2+k < clen && k < 8; k++ {
			w2 |= uint64(src[bp2+k]) << (8 * k)
		}
		code |= (w2 & ((uint64(1) << rem) - 1)) << (64 - sh)
	}

	return code
}

// alpReadBitsAt reads a width-bit LSB-first value at absolute bit offset bitpos.
func alpReadBitsAt(src []byte, bitpos, width int) uint64 {
	var c uint64
	for b := range width {
		bp := bitpos + b
		if src[bp>>3]&(1<<uint(bp&7)) != 0 { //nolint:gosec
			c |= uint64(1) << uint(b) //nolint:gosec
		}
	}

	return c
}

// All streams decoded values one at a time. Zero-allocation on the common path
// (no exceptions); only the rare exception path reads from a sidecar region.
func (d NumericALPDecoder) All(data []byte, count int) iter.Seq[float64] {
	return func(yield func(float64) bool) {
		if count <= 0 || len(data) == 0 {
			return
		}
		switch data[0] {
		case alpSchemeRaw:
			off := 1
			for range count {
				if !yield(math.Float64frombits(d.engine.Uint64(data[off : off+8]))) {
					return
				}
				off += 8
			}
		case alpSchemeMain:
			d.allMain(data[1:], count, yield)
		case alpSchemeRD:
			d.allRD(data[1:], count, yield)
		default:
		}
	}
}

func (d NumericALPDecoder) allMain(data []byte, count int, yield func(float64) bool) {
	ee := int(data[0])
	ff := int(data[1])
	width := int(data[2])
	nExc := int(d.engine.Uint32(data[3:7]))
	mn := int64(d.engine.Uint64(data[7:15])) //nolint:gosec
	codes := data[15:]
	exc := data[15+(count*width+7)/8:]

	mask := ^uint64(0)
	if width < 64 {
		mask = (uint64(1) << width) - 1
	}
	clen := len(codes)

	nextExc := -1
	if nExc > 0 {
		nextExc = int(d.engine.Uint32(exc[0:4]))
	}
	excIdx, bitpos := 0, 0
	for i := range count {
		var v float64
		if i == nextExc {
			p := excIdx * 12
			v = math.Float64frombits(d.engine.Uint64(exc[p+4 : p+12]))
			excIdx++
			if excIdx < nExc {
				nextExc = int(d.engine.Uint32(exc[excIdx*12 : excIdx*12+4]))
			} else {
				nextExc = -1
			}
		} else {
			// Inlined hot read (this is the common decimal path): single unaligned
			// 8-byte load + shift + mask; cold cases share alpReadBitsSlow.
			bp := bitpos >> 3
			sh := bitpos & 7
			var code uint64
			if bp+8 <= clen && sh+width <= 64 {
				code = (binary.LittleEndian.Uint64(codes[bp:bp+8]) >> sh) & mask
			} else {
				code = alpReadBitsSlow(codes, bitpos, width, mask)
			}
			v = float64(int64(code)+mn) * alpPow10[ff] * alpInvPow10[ee] //nolint:gosec
		}
		bitpos += width
		if !yield(v) {
			return
		}
	}
}

func (d NumericALPDecoder) allRD(data []byte, count int, yield func(float64) bool) {
	rbw := int(data[0])
	codeBits := int(data[1])
	nDict := int(data[2])
	nExc := int(d.engine.Uint32(data[3:7]))
	off := 7
	var dict [alpRDMaxDictSize]uint64 // ≤8 entries; stack-allocated
	for i := range nDict {
		dict[i] = uint64(d.engine.Uint16(data[off : off+2]))
		off += 2
	}
	leftCodes := data[off:]
	rights := data[off+(count*codeBits+7)/8:]
	exc := data[off+(count*codeBits+7)/8+(count*rbw+7)/8:]

	rightMask := ^uint64(0)
	if rbw < 64 {
		rightMask = (uint64(1) << rbw) - 1
	}
	codeMask := ^uint64(0)
	if codeBits < 64 {
		codeMask = (uint64(1) << codeBits) - 1
	}

	nextExc := -1
	if nExc > 0 {
		nextExc = int(d.engine.Uint32(exc[0:4]))
	}
	excIdx := 0
	for i := range count {
		right := alpReadBitsFast(rights, i*rbw, rbw, rightMask)
		var left uint64
		if i == nextExc {
			left = uint64(d.engine.Uint16(exc[excIdx*6+4 : excIdx*6+6]))
			excIdx++
			if excIdx < nExc {
				nextExc = int(d.engine.Uint32(exc[excIdx*6 : excIdx*6+4]))
			} else {
				nextExc = -1
			}
		} else {
			left = dict[alpReadBitsFast(leftCodes, i*codeBits, codeBits, codeMask)]
		}
		if !yield(math.Float64frombits((left << rbw) | right)) {
			return
		}
	}
}

// DecodeAll decodes count values from data directly into dst, returning the
// number written. Allocation-free; used by the blob materialize path.
func (d NumericALPDecoder) DecodeAll(data []byte, count int, dst []float64) int {
	i := 0
	for v := range d.All(data, count) {
		if i >= len(dst) {
			break
		}
		dst[i] = v
		i++
	}

	return i
}

// At decodes a single value directly — O(1) for ALP main/RD (compute the bit
// offset), unlike the O(n) sequential XOR chains of Gorilla/Chimp.
func (d NumericALPDecoder) At(data []byte, index int, count int) (float64, bool) {
	if index < 0 || index >= count || len(data) == 0 {
		return 0, false
	}
	switch data[0] {
	case alpSchemeRaw:
		off := 1 + index*8

		return math.Float64frombits(d.engine.Uint64(data[off : off+8])), true
	case alpSchemeMain:
		return d.atMain(data[1:], index, count), true
	case alpSchemeRD:
		return d.atRD(data[1:], index, count), true
	default:
		return 0, false
	}
}

func (d NumericALPDecoder) atMain(data []byte, index, count int) float64 {
	ee := int(data[0])
	ff := int(data[1])
	width := int(data[2])
	nExc := int(d.engine.Uint32(data[3:7]))
	mn := int64(d.engine.Uint64(data[7:15])) //nolint:gosec
	codes := data[15:]
	exc := data[15+(count*width+7)/8:]
	for k := range nExc {
		if int(d.engine.Uint32(exc[k*12:k*12+4])) == index {
			return math.Float64frombits(d.engine.Uint64(exc[k*12+4 : k*12+12]))
		}
	}
	code := alpReadBitsAt(codes, index*width, width)

	return float64(int64(code)+mn) * alpPow10[ff] * alpInvPow10[ee] //nolint:gosec
}

func (d NumericALPDecoder) atRD(data []byte, index, count int) float64 {
	rbw := int(data[0])
	codeBits := int(data[1])
	nDict := int(data[2])
	nExc := int(d.engine.Uint32(data[3:7]))
	off := 7
	var dict [alpRDMaxDictSize]uint64
	for i := range nDict {
		dict[i] = uint64(d.engine.Uint16(data[off : off+2]))
		off += 2
	}
	leftCodes := data[off:]
	rights := data[off+(count*codeBits+7)/8:]
	exc := data[off+(count*codeBits+7)/8+(count*rbw+7)/8:]
	right := alpReadBitsAt(rights, index*rbw, rbw)
	for k := range nExc {
		if int(d.engine.Uint32(exc[k*6:k*6+4])) == index {
			left := uint64(d.engine.Uint16(exc[k*6+4 : k*6+6]))

			return math.Float64frombits((left << uint(rbw)) | right) //nolint:gosec
		}
	}
	left := dict[alpReadBitsAt(leftCodes, index*codeBits, codeBits)]

	return math.Float64frombits((left << uint(rbw)) | right) //nolint:gosec
}
