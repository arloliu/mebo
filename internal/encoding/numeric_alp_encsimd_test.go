package encoding

import (
	"math"
	"math/rand"
	"testing"

	"github.com/arloliu/mebo/internal/arch"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// AVX-512 encode verify kernel — differential test.
//
// The contract the kernel + Go wrapper (alpMainStatsSIMD) must uphold is exact
// equality with the scalar reference (alpMainStatsScalar) for every input: the
// same recorded digits, the same ascending exception positions, and the same
// alpMainCand (min, width, nExc, ok). Those five outputs fully determine the
// encoded ALP-main bytes, so this is what pins the golden hashes and the
// cross-version size parity constant.
//
// Coverage swept below: every corpus-style distribution (clean decimal, full
// precision, giant integers that guard-fail, specials with NaN/±Inf/−0.0/
// denormals, all-exception and zero-exception columns) × a wide set of counts
// including odd tails (n%8 != 0), plus a forced (e,f) sweep that drives lanes
// deep into the guard-fail and in-domain-verify-fail regimes and mixes both
// inside a single 8-lane block.
// ---------------------------------------------------------------------------

// alpDiffPoison seeds both digit buffers so any lane that neither path writes
// (exceptions, guard-fails resolved to exceptions) is observably identical, and
// any spurious write by the kernel to a slot it must leave alone is caught.
const alpDiffPoison = uint64(0xDEADBEEFCAFEBABE)

// alpDiffCheck runs the scalar reference and the SIMD path over identical
// inputs and asserts the full output contract is byte-identical.
func alpDiffCheck(t *testing.T, name string, values []float64, ee, ff int) {
	t.Helper()
	n := len(values)

	dstS := make([]uint64, n)
	dstV := make([]uint64, n)
	for i := 0; i < n; i++ {
		dstS[i] = alpDiffPoison
		dstV[i] = alpDiffPoison
	}

	candS, excS := alpMainStatsScalar(values, ee, ff, dstS, nil)
	candV, excV := alpMainStatsSIMD(values, ee, ff, dstV, nil)

	require.Equalf(t, candS, candV, "%s (e=%d f=%d n=%d): alpMainCand mismatch", name, ee, ff, n)
	require.Equalf(t, excS, excV, "%s (e=%d f=%d n=%d): exception positions mismatch", name, ee, ff, n)
	require.Equalf(t, dstS, dstV, "%s (e=%d f=%d n=%d): digit buffer mismatch", name, ee, ff, n)
}

// ---- bespoke edge-case generators (guard-fail heavy) ----

// alpGenAllGiant returns exact integers whose magnitude sits in (2^51, 2^53],
// so at (e,f)=(0,0) every lane guard-fails (|scaled| >= 2^51) yet remains a
// valid, losslessly round-tripping digit — the hybrid design's giant-integer
// case the kernel must hand to the scalar fallback rather than treat as an
// exception.
func alpGenAllGiant(rng *rand.Rand, n int) []float64 {
	const lo = int64(1) << 51
	const hi = int64(1) << 53
	out := make([]float64, n)
	for i := range out {
		v := lo + rng.Int63n(hi-lo)
		if rng.Intn(2) == 0 {
			v = -v
		}
		out[i] = float64(v)
	}

	return out
}

// alpGenGiantVerifyMix interleaves giant integers (guard-fail at e=f=0) with
// fractional values that are in-domain but do not round-trip at e=f=0
// (verify-fail), guaranteeing 8-lane blocks that contain BOTH failure kinds —
// the dirty-block path where the wrapper must re-derive every lane scalar and
// still emit exceptions in ascending order.
func alpGenGiantVerifyMix(rng *rand.Rand, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		switch i % 3 {
		case 0:
			out[i] = float64((int64(1) << 52) + rng.Int63n(1<<40))
		case 1:
			out[i] = rng.Float64()*1000 + 0.123456789
		default:
			out[i] = math.Round(rng.Float64()*10000) / 100 // clean 2dp
		}
	}

	return out
}

// alpGenSpecialsDense fills the column with a rotating set of special values
// (signed zero, ±Inf, NaN, ±MaxFloat64, signed denormals) sprinkled into a
// clean decimal base so every block mixes good lanes with special-valued
// exception/guard-fail lanes.
func alpGenSpecialsDense(rng *rand.Rand, n int) []float64 {
	specials := []float64{
		math.Copysign(0, -1), // -0.0
		math.Inf(1),
		math.Inf(-1),
		math.NaN(),
		math.MaxFloat64,
		-math.MaxFloat64,
		math.SmallestNonzeroFloat64,
		-math.SmallestNonzeroFloat64,
	}
	out := make([]float64, n)
	cur := 100.0
	si := 0
	for i := range out {
		cur += cur * (rng.Float64()*2 - 1) * 0.005
		out[i] = math.Round(cur*100) / 100
		if i%5 == 0 {
			out[i] = specials[si%len(specials)]
			si++
		}
	}

	return out
}

// alpDiffCounts spans full blocks and every odd-tail residue (n%8 in 0..7),
// crossing the 8-lane block boundary at many scales.
var alpDiffCounts = []int{
	8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 23, 24, 25,
	31, 32, 33, 63, 64, 65, 100, 127, 128, 129,
	255, 256, 257, 511, 512, 513, 1000, 1023, 1024, 1031,
}

type alpDiffGen struct {
	name string
	fn   func(rng *rand.Rand, n int) []float64
}

func alpDiffGens() []alpDiffGen {
	return []alpDiffGen{
		{"decimal_0dp", func(r *rand.Rand, n int) []float64 { return alpCVDecimalWalk(r, n, 0) }},
		{"decimal_1dp", func(r *rand.Rand, n int) []float64 { return alpCVDecimalWalk(r, n, 1) }},
		{"decimal_2dp", func(r *rand.Rand, n int) []float64 { return alpCVDecimalWalk(r, n, 2) }},
		{"decimal_4dp", func(r *rand.Rand, n int) []float64 { return alpCVDecimalWalk(r, n, 4) }},
		{"mixed_dp", alpCVMixedDP},
		{"uniform", alpCVUniform},
		{"normal", alpCVNormal},
		{"log_normal", alpCVLogNormal},
		{"exact_integers", alpCVExactIntegers},
		{"large_magnitude_bracket", alpCVLargeMagnitudeBracket},
		{"half_tie_1dp", func(r *rand.Rand, n int) []float64 { return alpCVHalfTies(r, n, 1) }},
		{"random_walk", alpCVRandomWalk},
		{"constant", func(r *rand.Rand, n int) []float64 { return alpCVConstant(n) }},
		{"mixed_specials", alpCVMixedSpecials},
		{"pure_specials", func(r *rand.Rand, n int) []float64 { return alpCVPureSpecials(n) }},
		{"all_giant", alpGenAllGiant},
		{"giant_verify_mix", alpGenGiantVerifyMix},
		{"specials_dense", alpGenSpecialsDense},
	}
}

// TestALPMainStatsAVX512_Differential is the core contract: for every
// distribution × count, alpMainStatsSIMD (the AVX-512 kernel + rescue wrapper)
// produces byte-identical (digits, excPos, cand) to alpMainStatsScalar. The
// (e,f) used is the one alpBestEF actually picks for each column — the real
// encode-time choice.
func TestALPMainStatsAVX512_Differential(t *testing.T) {
	if !arch.X86HasAVX512DQ() {
		t.Skip("AVX-512DQ unavailable on this CPU")
	}

	for _, g := range alpDiffGens() {
		t.Run(g.name, func(t *testing.T) {
			for _, n := range alpDiffCounts {
				rng := rand.New(rand.NewSource(0xA51 + int64(n)))
				values := g.fn(rng, n)
				ee, ff := alpBestEF(values, alpSampleStride(n))
				alpDiffCheck(t, g.name, values, ee, ff)
			}
		})
	}
}

// TestALPMainStatsAVX512_ForcedEF forces (e,f) pairs that the estimator would
// not normally pick, driving lanes into the guard-fail region (large e blows
// |scaled| past 2^51 / to ±Inf) and the in-domain verify-fail region, on both
// decimal and giant columns and across full-block and odd-tail counts. This is
// where the kernel's domain-guard mask, the dirty-block scalar rescue, and the
// clean-block exception mask are exercised deliberately rather than by chance.
func TestALPMainStatsAVX512_ForcedEF(t *testing.T) {
	if !arch.X86HasAVX512DQ() {
		t.Skip("AVX-512DQ unavailable on this CPU")
	}

	efPairs := [][2]int{
		{0, 0}, {1, 0}, {2, 0}, {4, 2}, {6, 3},
		{10, 5}, {14, 7}, {18, 0}, {18, 9}, {18, 18},
	}
	counts := []int{8, 15, 16, 17, 64, 129, 256, 1000, 1031}

	gens := []alpDiffGen{
		{"decimal_2dp", func(r *rand.Rand, n int) []float64 { return alpCVDecimalWalk(r, n, 2) }},
		{"exact_integers", alpCVExactIntegers},
		{"all_giant", alpGenAllGiant},
		{"giant_verify_mix", alpGenGiantVerifyMix},
		{"specials_dense", alpGenSpecialsDense},
		{"uniform", alpCVUniform},
	}

	for _, g := range gens {
		t.Run(g.name, func(t *testing.T) {
			for _, n := range counts {
				rng := rand.New(rand.NewSource(0x5150 + int64(n)))
				values := g.fn(rng, n)
				for _, ef := range efPairs {
					alpDiffCheck(t, g.name, values, ef[0], ef[1])
				}
			}
		})
	}
}

// TestALPMainStatsAVX512_AllExceptionAndZeroException pins the two extremes the
// alloc-sensitive dispatch cares about: a column where every lane is an
// exception (nExc == n, ok == false) and a clean column with zero exceptions
// (the fast path). Both must match scalar exactly, and the zero-exception path
// must not have grown excPos at all (stays nil).
func TestALPMainStatsAVX512_AllExceptionAndZeroException(t *testing.T) {
	if !arch.X86HasAVX512DQ() {
		t.Skip("AVX-512DQ unavailable on this CPU")
	}

	for _, n := range []int{8, 16, 100, 1024, 1031} {
		// All-exception: irrational values that never round-trip at (e,f)=(0,0).
		allExc := make([]float64, n)
		for i := range allExc {
			allExc[i] = math.Pi * float64(i+1)
		}
		candS, excS := alpMainStatsScalar(allExc, 0, 0, make([]uint64, n), nil)
		candV, excV := alpMainStatsSIMD(allExc, 0, 0, make([]uint64, n), nil)
		require.Equalf(t, candS, candV, "all-exception n=%d cand", n)
		require.Equalf(t, excS, excV, "all-exception n=%d excPos", n)
		require.Falsef(t, candV.ok, "all-exception n=%d must report ok=false", n)
		require.Equalf(t, n, candV.nExc, "all-exception n=%d must have nExc==n", n)

		// Zero-exception: exact small integers all round-trip at (e,f)=(0,0),
		// so the fast path must never append to (and therefore never allocate)
		// excPos — it stays the nil slice it was handed.
		clean := make([]float64, n)
		for i := range clean {
			clean[i] = float64(i % 1000)
		}
		_, excZero := alpMainStatsSIMD(clean, 0, 0, make([]uint64, n), nil)
		require.Nilf(t, excZero, "zero-exception n=%d must not allocate/grow excPos", n)
	}
}
