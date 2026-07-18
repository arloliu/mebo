package encoding

import (
	"encoding/binary"
	"iter"
	"math"
	"math/bits"

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
//
// alpSchemeMain, alpSchemeRD, and alpSchemeRaw are the ONLY scheme bytes a
// TypeALP (encoding type 0x6) column payload may start with. This set is
// closed: do not add a fourth scheme under 0x6. Every decoder here (All,
// DecodeAll, At) falls through an unlabeled default: case for any other
// byte, decoding the column as empty/zero rather than erroring — and blobs
// already written by shipped v1.8.0+ encoders are read by shipped v1.8.0+
// decoders that only recognize these three, so a new scheme byte would be
// silently dropped by any reader built before it existed. If ALP's on-disk
// format ever needs a new scheme, introduce a new encoding type instead
// (see docs/specs/alp-bp128-wiring-design.md) so old readers
// reject it via section/numeric_flag.go's encoding-type allow-list instead
// of silently losing data. The blob layer validates the payload's first
// byte against ALPMaxSchemeByte once per column at blob open
// (blob/numeric_decoder.go) specifically because this default: fallthrough
// is otherwise indistinguishable from data loss.
const (
	alpSchemeMain byte = 0
	alpSchemeRD   byte = 1
	alpSchemeRaw  byte = 2

	// ALPMaxSchemeByte is the highest scheme byte value a TypeALP column may
	// declare (kept equal to alpSchemeRaw, the last entry above). Exported so
	// blob-layer readers — which sit outside this package and can't see the
	// unexported alpScheme* names — can validate a column's payload without
	// duplicating the magic number.
	ALPMaxSchemeByte = alpSchemeRaw

	// ALPRDMaxDictSize is the maximum number of ALP-RD dictionary entries a
	// column header may declare (mirrors alpRDMaxDictSize for blob-layer
	// validation, like ALPMaxSchemeByte above).
	ALPRDMaxDictSize = alpRDMaxDictSize

	alpMaxExponent   = 18
	alpRDCutLimit    = 16 // left part is 1..16 bits
	alpRDMaxDictSize = 8  // ≤8 dictionary entries

	// alpRDLeftTableSize is the counting-table length alpRDBuildDict always
	// requests from the pool: the widest possible left part is alpRDCutLimit=16
	// bits (rbw as low as 48), i.e. 1<<16 = 65536 distinct values. Requesting a
	// CONSTANT size — rather than 1<<(64-rbw), which varies per column — is what
	// lets the pooled slice actually be reused instead of reallocated whenever a
	// later column picks a wider cut. Every index l = p>>rbw is < 1<<(64-rbw) ≤
	// this, so it is always in bounds.
	alpRDLeftTableSize = 1 << alpRDCutLimit
)

var alpPow10 = [...]float64{
	1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
	1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18,
}

var alpInvPow10 = [...]float64{
	1e0, 1e-1, 1e-2, 1e-3, 1e-4, 1e-5, 1e-6, 1e-7, 1e-8, 1e-9,
	1e-10, 1e-11, 1e-12, 1e-13, 1e-14, 1e-15, 1e-16, 1e-17, 1e-18,
}

// alpFastRoundMagic is 2^52 + 2^51. Adding then subtracting it forces IEEE
// round-to-nearest-even at the ones place for |x| < 2^51 — the standard ALP
// rounding trick (cwida/ALP, DuckDB, Vortex). Outside that domain the result
// is wrong, so every caller guards |x| < 2^51 first. Two FP adds, no branch:
// this is what makes the verify pass vectorizable (numeric_alp_encsimd_amd64.s).
const alpFastRoundMagic = 0x1.8p52

func alpFastRound(x float64) float64 {
	return (x + alpFastRoundMagic) - alpFastRoundMagic
}

// alpEncodeDigit computes i = round(v·10^e·10^-f); ok is false if v does not
// round-trip bit-exactly (an exception).
func alpEncodeDigit(v float64, e, f int) (int64, bool) {
	scaled := v * alpPow10[e] * alpInvPow10[f]
	var i int64
	if math.Abs(scaled) < 1<<51 {
		// Hot path: magic-number fast round (branch-free, vectorizable).
		// The trick is only valid for |scaled| < 2^51, hence the guard.
		i = int64(alpFastRound(scaled))
	} else {
		// Rare: |scaled| >= 2^51, NaN/Inf. Keep the original math.Round
		// semantics exactly, so giant exact integers in [2^51, 2^53] keep
		// round-tripping as digits, byte-identical to the pure-math.Round
		// encoder.
		r := math.Round(scaled)
		if math.Abs(r) >= 9.2e18 {
			return 0, false
		}
		i = int64(r)
	}
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
	// codeScratch is the reused digit buffer for ALP-main (filled once by
	// alpMainStats, sized n). On the ALP-RD branch — where the main digits are
	// never packed — it is reused, grown to 2n, as encodeRD's leftCodes[:n] +
	// rights[n:2n] scratch (the two schemes never both consume it in one
	// encodeColumn call).
	codeScratch []uint64
	excScratch  []uint32 // reused exception-position buffer for ALP-main, filled by alpMainStats
	// ALP-RD planner scratch, all reused across columns to keep RD planning
	// allocation-free after warmup:
	patScratch []uint64 // full-column bit patterns (sized n)
	rdLefts    []uint64 // distinct left values (the counting table's touched list)
	rdCounts   []int32  // per-distinct-left counts, parallel to rdLefts
	rdExcPos   []uint32 // encodeRD exception positions
	rdExcLeft  []uint16 // encodeRD exception left values
	flushed    bool
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

	// Reusable digit scratch: alpMainStats records each value's ALP-main digit
	// here so encodeMain/encodeMainFast can pack without a second
	// alpEncodeDigit pass, whether or not the column has exceptions.
	if cap(e.codeScratch) < n {
		e.codeScratch = make([]uint64, n)
	}
	digits := e.codeScratch[:n]

	// excScratch is handed in reset ([:0]) and grown by alpMainStats via
	// append; the (possibly reallocated) slice is stored back immediately so
	// later columns reuse its backing array instead of reallocating.
	main, excPos := alpMainStats(values, ee, ff, digits, e.excScratch[:0])
	e.excScratch = excPos
	mainBits := math.MaxInt
	if main.ok {
		mainBits = alpMainHeaderBits + n*main.width + main.nExc*alpExcBitsMain
	}

	// Fast path: ALP main with zero exceptions means clean decimal data that ALP
	// main compresses far better than ALP-RD ever could — skip the costly RD
	// dictionary search entirely, and reuse the cached digits (no recompute).
	if main.ok && main.nExc == 0 && mainBits <= n*64 {
		e.buf.B = append(e.buf.B, alpSchemeMain)
		e.encodeMainFast(ee, ff, main.mn, main.width, digits)

		return
	}

	rd := e.alpRDPlan(values, stride)
	rdBits := alpRDHeaderBits + rd.nDict*16 + n*alpCodeBits(rd.nDict) + n*rd.rbw + rd.nExc*alpExcBitsRD

	rawBits := n * 64

	switch {
	case main.ok && mainBits <= rdBits && mainBits <= rawBits:
		e.buf.B = append(e.buf.B, alpSchemeMain)
		e.encodeMain(values, ee, ff, main.mn, main.width, digits, excPos)
	case rdBits <= rawBits:
		e.buf.B = append(e.buf.B, alpSchemeRD)
		e.encodeRD(values, rd.rbw, &rd.dict, rd.nDict)
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

// alpRDCand holds ALP-RD parameters computed for a full column. The dictionary
// is at most alpRDMaxDictSize entries, so it lives inline in a fixed array (with
// nDict live entries) instead of a slice+map — no allocation, and the decoder's
// linear dict lookup replaces the codeOf map probe.
type alpRDCand struct {
	rbw   int
	dict  [alpRDMaxDictSize]uint64
	nDict int
	nExc  int
}

func (e *NumericALPEncoder) encodeRaw(values []float64) {
	for _, v := range values {
		e.buf.B = e.engine.AppendUint64(e.buf.B, math.Float64bits(v))
	}
}

// ---- ALP main ----

// alpBestEF searches (e,f), f<=e, minimizing estimated size over a strided sample.
func alpBestEF(values []float64, stride int) (bestE, bestF int) {
	// Copy the strided sample into a contiguous stack array ONCE, then run all
	// up to 190 (e,f) candidate passes over that instead of re-gathering from
	// `values` (whole-column cache footprint) on every pass.
	var sbuf [64]float64 // ≤63 entries: alpSampleStride's bound (see TestAlpRDSampleBound)
	ns := 0
	for i := 0; i < len(values); i += stride {
		sbuf[ns] = values[i]
		ns++
	}
	sample := sbuf[:ns]

	best := math.MaxFloat64
	fullCnt := (len(values) + stride - 1) / stride
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
			var nExc int
			mn := int64(math.MaxInt64)
			mx := int64(math.MinInt64)
			pruned := false
			for _, v := range sample {
				// Fast estimate: plain float compare (the bit-exact check is only
				// needed in the final encode; this only steers (e,f) selection).
				scaled := v * pe * iff
				var d int64
				if math.Abs(scaled) < 1<<51 {
					// Hot path: magic-number fast round (see alpEncodeDigit).
					d = int64(alpFastRound(scaled))
				} else {
					// Rare: |scaled| >= 2^51, NaN/Inf. Keep the original math.Round
					// semantics exactly (see alpEncodeDigit); out of int64 range
					// counts as an exception, same as the old estimator.
					r := math.Round(scaled)
					if math.Abs(r) >= 9.2e18 {
						nExc++
						if float64(nExc)*96 >= best {
							pruned = true

							break
						}

						continue
					}
					d = int64(r)
				}
				if float64(d)*pf*ie != v {
					nExc++
					if float64(nExc)*96 >= best {
						pruned = true

						break
					}

					continue
				}
				upd := false
				if d < mn {
					mn = d
					upd = true
				}
				if d > mx {
					mx = d
					upd = true
				}
				// Width lower-bound prune: est = fullCnt*width + nExc*96, and both
				// width (=bits.Len64(mx-mn)) and nExc only grow as the scan proceeds,
				// so once the partial bound reaches best this (e,f) cannot win — skip
				// the rest. Catches zero-exception non-winners the nExc prune misses.
				// est uses fullCnt at loop end when not pruned, so selection is exact.
				if upd {
					wcur := bits.Len64(uint64(mx - mn)) //nolint:gosec
					if float64(fullCnt*wcur)+float64(nExc)*96 >= best {
						pruned = true

						break
					}
				}
			}
			if pruned {
				continue
			}
			// ns == ceil(len(values)/stride) == fullCnt whenever the scan above ran
			// to completion (not pruned) — the old `cnt` (incremented once per
			// sample, never touched on a pruned break) always equaled exactly this
			// at this point, so using ns here is value-identical, not just equal on
			// average.
			width := 0
			if nExc < ns && mx >= mn {
				width = bits.Len64(uint64(mx - mn)) //nolint:gosec
			}
			est := float64(ns*width + nExc*96)
			if est < best {
				best = est
				bestE, bestF = e, f
			}
		}
	}

	return bestE, bestF
}

// alpMainStats computes the FOR minimum, bit width, and exception positions for
// the chosen (e,f) over ALL values, dispatching to the AVX-512 verify kernel
// (with a scalar rescue pass for guard-failed blocks and a scalar tail for the
// n%8 remainder) or the plain scalar loop, depending on CPU support — see
// alpMainStatsSIMD/alpMainStatsScalar below. Either path records each good
// value's digit into dst (sized >= len(values)) and each exception's index
// into excPos, so encodeMain/encodeMainFast never need to recompute a digit.
// ok is false if every value is an exception (nExc == n; the caller falls
// back to ALP-RD/raw and the returned excPos is discarded).
//
// dst entries at exception positions are left untouched — i.e. they hold
// whatever the scratch buffer previously contained (stale digits from a prior
// column, or zero on first use). That's safe: encodeMain overwrites those
// exact slots with 0 after its FOR-subtraction pass, so the stale content
// never reaches the output. See encodeMain's comment for why the overwrite
// must happen AFTER subtraction, not before.
//
// excPos is grown with append starting from the slice the caller passes in
// (typically e.excScratch[:0] to reuse the encoder's backing array across
// columns); since append may reallocate, the caller MUST store the returned
// slice back.
func alpMainStats(values []float64, ee, ff int, dst []uint64, excPos []uint32) (alpMainCand, []uint32) {
	// alpMainStatsSIMD runs the AVX-512 verify kernel over the n/8*8 block
	// region when the CPU supports AVX-512DQ (finishing tail/exception lanes in
	// Go), and is the plain scalar loop everywhere else. It is guaranteed to
	// produce byte-identical (digits, excPos, min, width, nExc, ok) output to
	// alpMainStatsScalar — see numeric_alp_encsimd_test.go's differential test.
	return alpMainStatsSIMD(values, ee, ff, dst, excPos)
}

// alpMainStatsScalar is the reference implementation of alpMainStats: a single
// alpEncodeDigit pass recording each good value's digit into dst and each
// exception's index into excPos. It is the fallback for non-AVX-512 CPUs and
// the differential oracle the AVX-512 kernel is validated against.
func alpMainStatsScalar(values []float64, ee, ff int, dst []uint64, excPos []uint32) (alpMainCand, []uint32) {
	n := len(values)
	mn := int64(math.MaxInt64)
	mx := int64(math.MinInt64)
	for i, v := range values {
		d, good := alpEncodeDigit(v, ee, ff)
		if !good {
			excPos = append(excPos, uint32(i)) //nolint:gosec
			continue
		}
		dst[i] = uint64(d) //nolint:gosec
		if d < mn {
			mn = d
		}
		if d > mx {
			mx = d
		}
	}
	nExc := len(excPos)
	if nExc == n {
		return alpMainCand{nExc: nExc, ok: false}, excPos
	}
	width := 0
	if mx >= mn {
		width = bits.Len64(uint64(mx - mn)) //nolint:gosec
	}

	return alpMainCand{mn: mn, width: width, nExc: nExc, ok: true}, excPos
}

// encodeMainFast packs the ALP-main fast path (zero exceptions): it delegates
// to encodeMain with an empty exception list, so the FOR-subtraction/pack
// logic lives in exactly one place. Kept as a named entry point because
// encodeColumn's zero-exception branch calls it before the RD/raw estimates
// are even computed (see the early return there) — the name documents that
// short-circuit, even though the body is now a one-line forward.
func (e *NumericALPEncoder) encodeMainFast(ee, ff int, mn int64, width int, digits []uint64) {
	e.encodeMain(nil, ee, ff, mn, width, digits, nil)
}

// encodeMain packs the ALP-main column from digits — the digit scratch already
// filled by alpMainStats — plus excPos, the exception positions recorded in
// that same pass. This is pack-only: no alpEncodeDigit call happens here, for
// either the zero-exception path (excPos == nil, via encodeMainFast) or the
// exception-bearing path, eliminating the third full-column digit pass the
// original implementation needed.
//
// Byte-identity with the pre-optimization encoder (which wrote codes[i] = 0
// for exceptions BEFORE subtracting mn, i.e. a raw, un-adjusted 0) depends on
// doing these two loops in this order:
//  1. subtract mn from every digit slot, including exception slots, whose
//     content is stale garbage from alpMainStats (never written there) — the
//     result at those slots is garbage too, but it is about to be discarded;
//  2. THEN overwrite exception slots with a literal 0.
//
// Reversing the order (zeroing first, then subtracting mn from everything)
// would leave exception slots holding uint64(0-mn) instead of 0, changing the
// packed bytes. Do not reorder these two loops.
func (e *NumericALPEncoder) encodeMain(values []float64, ee, ff int, mn int64, width int, digits []uint64, excPos []uint32) {
	for i := range digits {
		digits[i] = uint64(int64(digits[i]) - mn) //nolint:gosec
	}
	for _, p := range excPos {
		digits[p] = 0 // byte-identical to the old codes[i]=0 for exceptions
	}

	eng := e.engine
	e.buf.B = append(e.buf.B, byte(ee), byte(ff), byte(width))
	e.buf.B = eng.AppendUint32(e.buf.B, uint32(len(excPos))) //nolint:gosec
	e.buf.B = eng.AppendUint64(e.buf.B, uint64(mn))          //nolint:gosec
	e.buf.B = alpPackBits(e.buf.B, digits, width)
	for _, p := range excPos {
		e.buf.B = eng.AppendUint32(e.buf.B, p)
		e.buf.B = eng.AppendUint64(e.buf.B, math.Float64bits(values[p]))
	}
}

// ---- ALP-RD ----

// alpRDTop8 keeps the 8 best lefts by (count DESC, then left ASC) via insertion
// into a fixed array — the map-free replacement for sort-everything-then-take-8.
//
// It is deterministic for any input order because the comparator is a TOTAL
// order: lefts are distinct, so no two candidates compare equal, and the
// resulting top-8 set and its order are the same whichever order the distinct
// lefts arrive in. That is the crux of the ALP-RD byte-identity argument — the
// dictionary (and therefore every encoded byte) matches the old sort-based
// builder regardless of histogram iteration order.
//
// lefts[j]/counts[j] are the j-th distinct left and its count. dict is filled
// best-first; nDict is min(8, len(lefts)). covered is the sum of the counts of
// the entries that ended up in the dict — i.e. how many input patterns the
// dictionary covers — so the caller gets the exception total (len(patterns) -
// covered) for free, without a second membership pass over the patterns.
func alpRDTop8(lefts []uint64, counts []int32, dict *[alpRDMaxDictSize]uint64) (nDict, covered int) {
	var cnts [alpRDMaxDictSize]int32
	for j, l := range lefts {
		c := counts[j]
		// Shift worse entries right to open a slot for (l, c). "Worse" means
		// lower count, or equal count with a larger left (so left ASC breaks
		// count ties). The i < alpRDMaxDictSize guard drops any entry shifted
		// past the 8-slot window.
		i := nDict
		for i > 0 && (cnts[i-1] < c || (cnts[i-1] == c && dict[i-1] > l)) {
			if i < alpRDMaxDictSize {
				dict[i], cnts[i] = dict[i-1], cnts[i-1]
			}
			i--
		}
		if i < alpRDMaxDictSize {
			dict[i], cnts[i] = l, c
			if nDict < alpRDMaxDictSize {
				nDict++
			}
		}
	}
	for i := 0; i < nDict; i++ {
		covered += int(cnts[i])
	}

	return nDict, covered
}

// alpRDLookup finds left's dictionary code by linear scan of the ≤8-entry dict —
// the map-free replacement for the codeOf map probe. With nDict ≤ 8 the scan is
// faster than hashing and allocates nothing.
func alpRDLookup(dict *[alpRDMaxDictSize]uint64, nDict int, left uint64) (code int, ok bool) {
	for i := 0; i < nDict; i++ {
		if dict[i] == left {
			return i, true
		}
	}

	return 0, false
}

// alpRDBuildDict builds the ≤8-entry ALP-RD dictionary over ALL n column
// patterns at the chosen cut, map- and sort-free. Left parts are 64-rbw ≤ 16
// bits, so it histograms them into a pooled counting table of 1<<(64-rbw) ≤
// 65536 uint32s, recording each distinct left the first time it is seen
// (leftScratch, the "touched list"). That touched list then (a) feeds the
// top-8 selection and (b) is walked to re-zero ONLY the entries this call
// wrote — O(distinct), not O(tablesize) — restoring the pool's all-zero-at-rest
// invariant before the table is returned.
//
// The resulting dict is byte-identical to the old sort-everything builder: see
// alpRDTop8 for why the (count DESC, left ASC) total order makes the top-8
// independent of histogram order.
//
// leftScratch/cntScratch are reused encoder buffers grown via append; the
// possibly-reallocated slices are stored back through the pointers so the next
// column reuses their backing arrays. covered is the number of patterns the
// dictionary covers (len(patterns) - covered = the exception count), returned
// so alpRDPlan needs no separate exception-counting pass.
func alpRDBuildDict(patterns []uint64, rbw int, leftScratch *[]uint64, cntScratch *[]int32, dict *[alpRDMaxDictSize]uint64) (nDict, covered int) {
	r := uint(rbw) //nolint:gosec
	// Always request the max table size (constant) so the pooled slice is reused
	// across columns instead of being reallocated when a wider cut needs a bigger
	// table; only indices below 1<<(64-rbw) are ever touched.
	counts, release := pool.GetUint32Slice(alpRDLeftTableSize)
	touched := (*leftScratch)[:0]
	// Re-zero only the touched entries then release, deferred so a panic mid-
	// build cannot poison the shared pool. No panic is actually reachable: l =
	// p>>rbw has exactly 64-rbw bits, so l < 1<<(64-rbw) ≤ len(counts) and every
	// index below is in bounds.
	defer func() {
		for _, l := range touched {
			counts[l] = 0
		}
		release()
	}()

	for _, p := range patterns {
		l := p >> r
		if counts[l] == 0 {
			touched = append(touched, l)
		}
		counts[l]++
	}
	*leftScratch = touched

	cnts := (*cntScratch)[:0]
	for _, l := range touched {
		cnts = append(cnts, int32(counts[l])) //nolint:gosec
	}
	*cntScratch = cnts

	return alpRDTop8(touched, cnts, dict)
}

// alpRDBestCut returns the right bit width minimizing the estimated ALP-RD size
// over the strided SAMPLE, and that estimate in bits. It is map- and sort-free:
// for each of the 16 candidate cuts it accumulates the sample's distinct lefts
// (with counts) into fixed [64] arrays by linear scan, top-8-selects the
// dictionary, and derives the exception count as n - covered (covered is a
// by-product of the top-8 selection, so no membership pass is needed).
//
// The [64] arrays are sound because the sample has at most 63 entries: with
// alpSampleStride, stride is 1 only when n ≤ 63 (≤63 samples) and is ≥2
// otherwise (ceil(n/stride) ≤ 48), so distinct lefts per cut ≤ 63 < 64 (proven
// in TestAlpRDSampleBound). This function must ONLY be called on sample-sized
// inputs. The chosen rbw and totalBits are byte-identical to building each
// cut's dictionary with a map+sort.
func alpRDBestCut(patterns []uint64) (rbw, totalBits int) {
	best := math.MaxInt
	n := len(patterns)
	var lefts [64]uint64
	var cnts [64]int32
	var top [alpRDMaxDictSize]uint64
	for i := 1; i <= alpRDCutLimit; i++ {
		r := 64 - i
		// Accumulate distinct lefts (first-seen order) with their counts. Only
		// entries [0:m) are read, so stale array slots from a prior cut are
		// never observed.
		m := 0
		for _, p := range patterns {
			l := p >> uint(r) //nolint:gosec
			k := 0
			for ; k < m; k++ {
				if lefts[k] == l {
					cnts[k]++

					break
				}
			}
			if k == m {
				lefts[m] = l
				cnts[m] = 1
				m++
			}
		}
		nDict, covered := alpRDTop8(lefts[:m], cnts[:m], &top)
		// Exceptions = patterns whose left is not in the dict = n - covered
		// (covered comes straight from the top-8 selection, no membership pass).
		ex := n - covered
		codeBits := alpCodeBits(nDict)
		total := 8 + 8 + 8 + 32 + nDict*16 + n*codeBits + n*r + ex*(32+16)
		if total < best {
			best = total
			rbw, totalBits = r, total
		}
	}

	return rbw, totalBits
}

// alpRDPlan searches the right bit width on a strided sample, then builds the
// dictionary and counts exceptions over ALL values at the chosen cut. Every
// buffer it needs is reused encoder scratch (the counting table comes from a
// shared pool), so after warmup it allocates nothing.
func (e *NumericALPEncoder) alpRDPlan(values []float64, stride int) alpRDCand {
	n := len(values)

	// Materialize all n bit patterns once into reusable scratch; the sample is
	// a strided view of them (no second Float64bits pass).
	if cap(e.patScratch) < n {
		e.patScratch = make([]uint64, n)
	}
	full := e.patScratch[:n]
	for i, v := range values {
		full[i] = math.Float64bits(v)
	}

	// The strided sample fits a fixed [64] stack array: at most 63 entries (see
	// alpRDBestCut's bound proof + TestAlpRDSampleBound).
	var sample [64]uint64
	m := 0
	for i := 0; i < n; i += stride {
		sample[m] = full[i]
		m++
	}
	rbw, _ := alpRDBestCut(sample[:m])

	var dict [alpRDMaxDictSize]uint64
	nDict, covered := alpRDBuildDict(full, rbw, &e.rdLefts, &e.rdCounts, &dict)

	// Exceptions = patterns not covered by the dictionary. buildDict already
	// summed the dictionary's coverage while selecting the top 8, so no second
	// membership pass over the n patterns is needed.
	return alpRDCand{rbw: rbw, dict: dict, nDict: nDict, nExc: n - covered}
}

// encodeRD packs the ALP-RD column. leftCodes and rights share the reused
// codeScratch buffer (grown to 2n): codeScratch[:n] holds the dict codes,
// codeScratch[n:2n] the right parts. This aliasing is safe because encodeRD only
// runs on the RD branch, where the ALP-main digit pass that also fills
// codeScratch is discarded (encodeMain is never called on this path). Exception
// positions/lefts are reused encoder scratch too, so a warmed encoder packs an
// RD column allocation-free.
func (e *NumericALPEncoder) encodeRD(values []float64, rbw int, dict *[alpRDMaxDictSize]uint64, nDict int) {
	n := len(values)
	codeBits := alpCodeBits(nDict)
	rightMask := (uint64(1) << uint(rbw)) - 1 //nolint:gosec

	if cap(e.codeScratch) < 2*n {
		e.codeScratch = make([]uint64, 2*n)
	}
	scratch := e.codeScratch[:2*n]
	leftCodes := scratch[:n]
	rights := scratch[n : 2*n]
	excPos := e.rdExcPos[:0]
	excLeft := e.rdExcLeft[:0]
	r := uint(rbw) //nolint:gosec
	for i, v := range values {
		p := math.Float64bits(v)
		left := p >> r
		rights[i] = p & rightMask
		if c, ok := alpRDLookup(dict, nDict, left); ok {
			leftCodes[i] = uint64(c) //nolint:gosec
		} else {
			leftCodes[i] = 0
			excPos = append(excPos, uint32(i))      //nolint:gosec
			excLeft = append(excLeft, uint16(left)) //nolint:gosec
		}
	}
	e.rdExcPos = excPos
	e.rdExcLeft = excLeft

	eng := e.engine
	e.buf.B = append(e.buf.B, byte(rbw), byte(codeBits), byte(nDict))
	e.buf.B = eng.AppendUint32(e.buf.B, uint32(len(excPos))) //nolint:gosec
	for k := 0; k < nDict; k++ {
		e.buf.B = eng.AppendUint16(e.buf.B, uint16(dict[k])) //nolint:gosec
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
	e.codeScratch = nil
	e.excScratch = nil
	e.patScratch = nil
	e.rdLefts = nil
	e.rdCounts = nil
	e.rdExcPos = nil
	e.rdExcLeft = nil
	e.flushed = false
}

// alpPackBits appends codes bit-packed LSB-first at the given width. It packs
// word-at-a-time through a 64-bit accumulator, flushing in 8-byte words, which is
// byte-identical to a naive bit-by-bit packer but ~width× fewer operations.
func alpPackBits(dst []byte, codes []uint64, width int) []byte {
	if width == 0 {
		return dst
	}
	start := len(dst)
	nbytes := (len(codes)*width + 7) / 8
	dst = append(dst, make([]byte, nbytes+7)...) // +7 slack so every flush is a full PutUint64

	mask := ^uint64(0)
	if width < 64 {
		mask = (uint64(1) << width) - 1
	}
	pos := start
	var acc uint64
	nbits := 0 // invariant: < 64 at loop top
	for _, c := range codes {
		c &= mask
		acc |= c << nbits
		if nbits+width >= 64 {
			binary.LittleEndian.PutUint64(dst[pos:], acc)
			pos += 8
			acc = c >> (64 - nbits) // Go defines shift-by-64 as 0 (nbits==0 case)
			nbits += width - 64
		} else {
			nbits += width
		}
	}
	if nbits > 0 {
		binary.LittleEndian.PutUint64(dst[pos:], acc) // slack absorbs the overshoot
	}

	return dst[:start+nbytes]
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

//go:generate go run ./gen/alpkernels

// decodeMainInto bulk-decodes an ALP-main column directly into dst, writing
// min(count, len(dst)) values and returning that number. count MUST be the
// column's true encoded record count (NOT pre-clamped to len(dst)): the
// packed-codes region and hence the exception sidecar's byte offset are
// sized by the real count regardless of how much of dst we fill, exactly as
// in allMain. Only the write loop, the exception guard, and the return value
// are bounded by len(dst) — mirroring allMain's header parsing but with no
// exception branch in the unpack loop: exceptions are rare, so the bulk loop
// always writes the FOR-decoded value and a post-pass overwrites the
// (ascending, sidecar-ordered) exception positions afterwards. Header parsing
// is kept separate from the unpack loop so the generated-kernel dispatch
// below replaces only the value loop.
func (d NumericALPDecoder) decodeMainInto(data []byte, count int, dst []float64) int {
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

	n := count
	if n > len(dst) {
		n = len(dst)
	}

	// Fused-kernel fast path (generated: numeric_alp_kernels_gen.go). A width-w
	// kernel decodes whole 8-value groups (byte-aligned: 8w bits == w bytes) with
	// constant shifts and no interior bounds checks. Each group loads the ceil(w/8)
	// covering words, so its last group over-reads up to slack = 8*ceil(w/8)-w
	// bytes past the group; dispatch only as many groups as stay within codes and
	// let the scalar loop below finish the remainder + the <8-value tail. Skipped
	// for n<8 (no full group) and width 0 (no bits — scalar handles it).
	start := 0
	if width >= 1 && width <= 64 && n >= 8 {
		if kern := alpFusedUnpack[width]; kern != nil {
			slack := 8*((width+7)/8) - width
			maxG := n >> 3
			if clen-slack >= 0 {
				if g := (clen - slack) / width; g < maxG {
					maxG = g
				}
			} else {
				maxG = 0
			}
			if maxG > 0 {
				start = maxG << 3
				kern(codes, start, mn, alpPow10[ff], alpInvPow10[ee], dst)
			}
		}
	}

	// bulk decode: no exception branch in the hot loop (scalar remainder + tail)
	bitpos := start * width
	for i := start; i < n; i++ {
		bp := bitpos >> 3
		sh := bitpos & 7
		var code uint64
		if bp+8 <= clen && sh+width <= 64 {
			code = (binary.LittleEndian.Uint64(codes[bp:bp+8]) >> sh) & mask
		} else {
			code = alpReadBitsSlow(codes, bitpos, width, mask)
		}
		dst[i] = float64(int64(code)+mn) * alpPow10[ff] * alpInvPow10[ee] //nolint:gosec
		bitpos += width
	}
	// patch exceptions by position afterwards (sidecar positions are ascending)
	for k := 0; k < nExc; k++ {
		p := int(d.engine.Uint32(exc[k*12 : k*12+4]))
		if p < n {
			dst[p] = math.Float64frombits(d.engine.Uint64(exc[k*12+4 : k*12+12]))
		}
	}

	return n
}

// decodeRDInto bulk-decodes an ALP-RD column directly into dst, writing
// min(count, len(dst)) values and returning that number. As with
// decodeMainInto, count MUST be the column's true encoded record count: the
// right-part bit-packed region and exception sidecar offsets are sized by it
// independent of len(dst). Mirrors allRD's header parsing.
//
// Bulk path (generated: numeric_alp_kernels_gen.go): the two streams — left
// dictionary codes (width codeBits) and right parts (width rbw) — are each
// unpacked in one pass with the pure alpUnpackBits kernels into pooled
// []uint64 scratch, then combined with a single
// dict[lc[i]]<<rbw|rt[i] loop, replacing the two per-value windowed reads
// (alpReadBitsFast) below for as many whole 8-value groups as both streams'
// over-read bounds allow. leftCodes and rights are each sized independently
// (see decodeMainInto for why this bound check, computed the same way per
// stream, is safe even though the last group's word loads read a few bytes
// past that stream's own region and into whatever follows it in the packed
// buffer — those extra bytes are never actually referenced by any of the 8
// per-group extraction formulas). The scalar loop below finishes the
// remainder + the <8-value tail and is also the sole path when either width
// has no kernel (0, or >64 — unreachable from the encoder but guarded
// defensively). Exceptions are still patched by the existing post-pass
// afterwards, recomputing the right part for each patched index since the
// bulk/scalar loop already wrote a (wrong) dict-code-derived value there.
func (d NumericALPDecoder) decodeRDInto(data []byte, count int, dst []float64) int {
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

	n := count
	if n > len(dst) {
		n = len(dst)
	}

	start := 0
	if codeBits >= 1 && codeBits <= 64 && rbw >= 1 && rbw <= 64 && n >= 8 {
		lkern := alpUnpackBits[codeBits]
		rkern := alpUnpackBits[rbw]
		if lkern != nil && rkern != nil {
			maxG := n >> 3
			lslack := 8*((codeBits+7)/8) - codeBits
			if lclen := len(leftCodes); lclen-lslack >= 0 {
				if g := (lclen - lslack) / codeBits; g < maxG {
					maxG = g
				}
			} else {
				maxG = 0
			}
			if maxG > 0 {
				rslack := 8*((rbw+7)/8) - rbw
				if rclen := len(rights); rclen-rslack >= 0 {
					if g := (rclen - rslack) / rbw; g < maxG {
						maxG = g
					}
				} else {
					maxG = 0
				}
			}
			if maxG > 0 {
				start = maxG << 3
				// Pointer-based Get/Put (not the GetInt64Slice-style
				// Get-plus-closure pools): a returned closure would escape to
				// the heap on every call, costing an allocation this hot
				// path can't afford even with a warm pool. See
				// internal/pool/uint64_slice_pool.go. defer (not an
				// immediate Put after the combine loop) so a panic between
				// Get and return can't leak the pooled buffers, mirroring
				// alpRDBuildDict's release discipline above.
				lcPtr := pool.GetUint64Slice(start)
				defer pool.PutUint64Slice(lcPtr)
				rtPtr := pool.GetUint64Slice(start)
				defer pool.PutUint64Slice(rtPtr)
				lc := *lcPtr
				rt := *rtPtr
				lkern(leftCodes, start, lc)
				rkern(rights, start, rt)
				for i := 0; i < start; i++ {
					dst[i] = math.Float64frombits((dict[lc[i]] << rbw) | rt[i])
				}
			}
		}
	}

	for i := start; i < n; i++ {
		right := alpReadBitsFast(rights, i*rbw, rbw, rightMask)
		left := dict[alpReadBitsFast(leftCodes, i*codeBits, codeBits, codeMask)]
		dst[i] = math.Float64frombits((left << rbw) | right)
	}
	for k := 0; k < nExc; k++ {
		p := int(d.engine.Uint32(exc[k*6 : k*6+4]))
		if p < n {
			left := uint64(d.engine.Uint16(exc[k*6+4 : k*6+6]))
			right := alpReadBitsFast(rights, p*rbw, rbw, rightMask)
			dst[p] = math.Float64frombits((left << rbw) | right)
		}
	}

	return n
}

// DecodeAll decodes count values from data directly into dst, writing
// min(count, len(dst)) values and returning that number. Allocation-free;
// used by the blob materialize path. Unlike All, this bulk-decodes into dst
// with a tight per-scheme loop (no per-value closure call) and patches
// exceptions in a post-pass, since the exception positions are rare and
// sidecar-ordered. count is passed through to decodeMainInto/decodeRDInto
// UNCLAMPED — it must stay the column's true record count so header/sidecar
// offset math (sized by the real count) stays correct even when dst is
// shorter; only the write loop and return value are bounded by len(dst).
func (d NumericALPDecoder) DecodeAll(data []byte, count int, dst []float64) int {
	if count <= 0 || len(data) == 0 {
		return 0
	}
	switch data[0] {
	case alpSchemeRaw:
		n := count
		if n > len(dst) {
			n = len(dst)
		}
		off := 1
		for i := 0; i < n; i++ {
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

// At decodes a single value directly — an O(1) windowed bit read plus an
// O(log k) binary search over the column's exception sidecar for ALP main/RD
// (k = exceptions in the column, not count; see atMain/atRD), unlike the O(n)
// sequential XOR chains of Gorilla/Chimp.
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

// atMain looks up a single index in an O(1) windowed read (alpReadBitsFast)
// plus a binary search of the exception sidecar. Exception positions are
// written in ascending index order by the encoder (mirrors allMain/
// decodeMainInto), which is what makes the binary search valid.
func (d NumericALPDecoder) atMain(data []byte, index, count int) float64 {
	ee := int(data[0])
	ff := int(data[1])
	width := int(data[2])
	nExc := int(d.engine.Uint32(data[3:7]))
	mn := int64(d.engine.Uint64(data[7:15])) //nolint:gosec
	codes := data[15:]
	exc := data[15+(count*width+7)/8:]

	lo, hi := 0, nExc
	for lo < hi {
		mid := (lo + hi) / 2
		if int(d.engine.Uint32(exc[mid*12:mid*12+4])) < index {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < nExc && int(d.engine.Uint32(exc[lo*12:lo*12+4])) == index {
		return math.Float64frombits(d.engine.Uint64(exc[lo*12+4 : lo*12+12]))
	}

	mask := ^uint64(0)
	if width < 64 {
		mask = (uint64(1) << width) - 1
	}
	code := alpReadBitsFast(codes, index*width, width, mask)

	return float64(int64(code)+mn) * alpPow10[ff] * alpInvPow10[ee] //nolint:gosec
}

// atRD looks up a single index in an O(1) windowed read plus a binary search
// of the exception sidecar (ascending index order, same invariant as
// atMain/allRD/decodeRDInto).
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

	rightMask := ^uint64(0)
	if rbw < 64 {
		rightMask = (uint64(1) << rbw) - 1
	}
	right := alpReadBitsFast(rights, index*rbw, rbw, rightMask)

	lo, hi := 0, nExc
	for lo < hi {
		mid := (lo + hi) / 2
		if int(d.engine.Uint32(exc[mid*6:mid*6+4])) < index {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < nExc && int(d.engine.Uint32(exc[lo*6:lo*6+4])) == index {
		left := uint64(d.engine.Uint16(exc[lo*6+4 : lo*6+6]))

		return math.Float64frombits((left << uint(rbw)) | right)
	}

	codeMask := ^uint64(0)
	if codeBits < 64 {
		codeMask = (uint64(1) << codeBits) - 1
	}
	left := dict[alpReadBitsFast(leftCodes, index*codeBits, codeBits, codeMask)]

	return math.Float64frombits((left << uint(rbw)) | right)
}
