package encoding

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Cross-version ALP verification harness.
//
// This is the hard gate any change to the ALP encoder's rounding
// (alpEncodeDigit / alpBestEF) must clear before it changes a single output
// byte. It proves the three invariants that stand in for the older
// byte-identical-encoder guarantee:
//
//  1. Lossless: encode -> decode is bit-exact (math.Float64bits equality) for
//     every value, every scheme, both endian engines, through DecodeAll, All,
//     and At (TestALPCrossVer_Lossless).
//  2. Every digit where production disagrees with the frozen math.Round
//     rounding reference still round-trips bit-exactly -- either through the
//     digit production chose, or (if treated as an exception) verbatim through
//     the exception sidecar (TestALPCrossVer_DigitDivergence).
//  3. Ratio parity: corpus aggregate encoded size stays within +0.5% of the
//     baseline constant recorded below (TestALPCrossVer_SizeParity).
//
// Everything is seeded and deterministic. n defaults to alpCrossVerDefaultN
// (100,000 values per distribution); env MEBO_ALP_VERIFY_N overrides it. The
// one-time GATE run uses MEBO_ALP_VERIFY_N=10000000 (takes minutes; not part
// of the default `make test` path):
//
//	MEBO_ALP_VERIFY_N=10000000 go test ./internal/encoding/ -run TestALPCrossVer -v
// ---------------------------------------------------------------------------

const (
	// alpCrossVerDefaultN is the default per-distribution corpus size. Kept
	// small enough that the full harness runs in seconds under `make test`;
	// MEBO_ALP_VERIFY_N raises it for the one-time gate run.
	alpCrossVerDefaultN = 100_000

	// alpCrossVerSeed seeds the single rand.Rand every distribution in
	// alpCrossVerCorpus is drawn from, in order -- the whole corpus is a
	// deterministic function of (seed, n).
	alpCrossVerSeed int64 = 20260716

	// alpCrossVerBaselineBytes is the aggregate encoded byte count (little-endian
	// engine, every corpus column summed) of alpCrossVerCorpus(alpCrossVerSeed,
	// alpCrossVerDefaultN), recorded ONCE before the fast-round change landed,
	// by running:
	//
	//	MEBO_ALP_VERIFY_RECORD=1 go test ./internal/encoding/ -run TestALPCrossVer_SizeParity -v
	//
	// and pasting the printed "RECORD MODE" value here. TestALPCrossVer_SizeParity
	// asserts later runs (at this SAME default n) stay within +0.5% of this
	// constant; a rounding change may spend that headroom on different
	// tie-breaks, never more. This constant is ONLY meaningful at
	// n == alpCrossVerDefaultN -- at any other MEBO_ALP_VERIFY_N, the
	// size-parity test logs the aggregate instead of asserting against it
	// (see TestALPCrossVer_SizeParity).
	alpCrossVerBaselineBytes = 7_868_099
)

// alpCorpusCol is one named, deterministic distribution in the cross-version
// verification corpus.
type alpCorpusCol struct {
	name   string
	values []float64
}

// refEncodeDigitRound is a FROZEN, verbatim copy of alpEncodeDigit
// (numeric_alp.go) as it existed before the fast-round change (pure
// math.Round, round-half-away-from-zero). It must NEVER be edited again: it
// is the fixed reference point TestALPCrossVer_DigitDivergence diffs the
// production alpEncodeDigit against. If a future change needs a different
// reference, it must be a NEW function, not an edit to this one.
func refEncodeDigitRound(v float64, e, f int) (int64, bool) {
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

// alpCrossVerN resolves the corpus scale for this test run: MEBO_ALP_VERIFY_N
// overrides alpCrossVerDefaultN when set. The one-time gate run sets it to
// 10000000.
func alpCrossVerN(t *testing.T) int {
	t.Helper()

	s := os.Getenv("MEBO_ALP_VERIFY_N")
	if s == "" {
		return alpCrossVerDefaultN
	}

	n, err := strconv.Atoi(s)
	require.NoErrorf(t, err, "MEBO_ALP_VERIFY_N=%q must be an integer", s)
	require.Positivef(t, n, "MEBO_ALP_VERIFY_N=%q must be positive", s)

	return n
}

// ---- corpus distributions ----
//
// Each alpCV* generator advances the SAME shared rng passed to it, so the
// corpus as a whole is a deterministic function of (seed, n): calling
// alpCrossVerCorpus twice with the same arguments always reproduces
// bit-identical columns.

// alpCVDecimalWalk returns an n-point +/-0.5% random walk quantized to dp
// decimal places -- the "clean sensor decimal" shape ALP main is built for
// (mirrors genALPColumns in numeric_alp_packbits_test.go).
func alpCVDecimalWalk(rng *rand.Rand, n, dp int) []float64 {
	scale := math.Pow10(dp)
	out := make([]float64, n)
	cur := 100.0
	for i := range out {
		cur += cur * (rng.Float64()*2 - 1) * 0.005
		out[i] = math.Round(cur*scale) / scale
	}

	return out
}

// alpCVMixedDP returns a column where consecutive values are quantized to a
// DIFFERENT number of decimal places (0,1,2,4, cycling). A single ALP-main
// column must pick one (e,f) for the whole column, so mixed-dp data stresses
// that shared choice: the encoder should land on enough precision (f=4) to
// keep every value -- including the 0dp ones, exactly representable at f=4 --
// losslessly round-trippable.
func alpCVMixedDP(rng *rand.Rand, n int) []float64 {
	dps := [...]int{0, 1, 2, 4}
	out := make([]float64, n)
	cur := 100.0
	for i := range out {
		cur += cur * (rng.Float64()*2 - 1) * 0.005
		scale := math.Pow10(dps[i%len(dps)])
		out[i] = math.Round(cur*scale) / scale
	}

	return out
}

// alpCVUniform returns n independent draws from rng.Float64() ([0,1),
// full-precision, not decimal).
func alpCVUniform(rng *rand.Rand, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = rng.Float64()
	}

	return out
}

// alpCVNormal returns n independent draws from rng.NormFloat64() (standard
// normal, full-precision).
func alpCVNormal(rng *rand.Rand, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = rng.NormFloat64()
	}

	return out
}

// alpCVLogNormal returns n log-normal magnitudes (exp of a scaled normal
// draw) -- the heavy-tailed-magnitude shape real latency/throughput metrics
// often have.
func alpCVLogNormal(rng *rand.Rand, n int) []float64 {
	const mu, sigma = 2.0, 1.5
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Exp(rng.NormFloat64()*sigma + mu)
	}

	return out
}

// alpCVExactIntegers returns n exact (sign-randomized) integers up to 2^53 --
// the largest magnitude every integer is still exactly representable in
// float64.
func alpCVExactIntegers(rng *rand.Rand, n int) []float64 {
	const maxExact = int64(1) << 53
	out := make([]float64, n)
	for i := range out {
		v := rng.Int63n(maxExact)
		if rng.Intn(2) == 0 {
			v = -v
		}
		out[i] = float64(v)
	}

	return out
}

// alpCVLargeMagnitudeBracket returns n exact integers spread across
// [2^49, 2^53], with the exact boundary values around 2^51 pinned at fixed
// positions. These are exact integers, so ALP main's natural (e=0,f=0) choice
// makes the "digit" the raw value itself (no rounding at all) -- exactly
// bracketing the fast-round domain guard (|scaled| < 2^51): values below it
// take the magic-number fast-round path, values at or above it take the
// legacy math.Round fallback, giving TestALPCrossVer_DigitDivergence
// deterministic, boundary-precise coverage of both paths.
func alpCVLargeMagnitudeBracket(rng *rand.Rand, n int) []float64 {
	const (
		lo = int64(1) << 49
		hi = int64(1) << 53
	)
	out := make([]float64, n)
	for i := range out {
		v := lo + rng.Int63n(hi-lo)
		if rng.Intn(2) == 0 {
			v = -v
		}
		out[i] = float64(v)
	}

	boundary := []float64{
		float64(int64(1) << 51),
		float64(int64(1)<<51 - 1),
		float64(int64(1)<<51 + 1),
		-float64(int64(1) << 51),
	}
	for i, v := range boundary {
		if i < len(out) {
			out[i] = v
		}
	}

	return out
}

// alpCVHalfTies returns a column of decimal half-integer values (k+0.5)/10^dp
// for pseudo-random k -- the standard "X.5" rounding-tie shape at dp decimal
// places, and the only shape where round-half-even (the magic-number fast
// round) and round-half-away-from-zero (math.Round) are even CAPABLE of
// disagreeing.
//
// Caveat, confirmed empirically: for
// this regular k-progression, alpBestEF's own search usually finds a
// higher-precision (e,f) pair where every value becomes an EXACT integer
// digit (e.g. dp=1 picks (e,f) with one extra digit of precision, turning
// every (k+0.5)/10 into the exact integer 10k+5) -- so under the search's OWN
// chosen (e,f), few or no genuine ties survive, and TestALPCrossVer_DigitDivergence
// logs 0 divergences for these columns for a structural reason (the search
// escapes the tie), not because the rounding modes agree. That's still a
// legitimate, useful column (half-integer-shaped decimal round-trip
// coverage); it just means alpCVLargeMagnitudeBracket -- via the domain-guard
// boundary, not the rounding-mode difference -- is the more reliable source
// of expected divergences. Kept simple rather than hand-engineered to defeat
// alpBestEF's escape, which would require reverse-engineering floating-point
// rounding behavior specific to the fast-round algorithm.
func alpCVHalfTies(rng *rand.Rand, n, dp int) []float64 {
	scale := math.Pow10(dp)
	out := make([]float64, n)
	for i := range out {
		k := rng.Int63n(1_000_000) - 500_000
		out[i] = (float64(k) + 0.5) / scale
	}

	return out
}

// alpCVRandomWalk returns a smooth, UNQUANTIZED (full-precision) sensor-like
// random walk -- the shape that routes to ALP-RD in production (see
// TestNumericALP_Ratio's randwalk_fp/battery_fp/cpu_util_fp cases, which get
// the "within 15% of Chimp" tolerance rather than the "beats Chimp" decimal
// assertion).
func alpCVRandomWalk(rng *rand.Rand, n int) []float64 {
	out := make([]float64, n)
	cur := 100.0
	for i := range out {
		cur += cur * (rng.Float64()*2 - 1) * 0.0005
		out[i] = cur
	}

	return out
}

// alpCVConstant returns n copies of the same value -- the degenerate
// FOR-min-equals-max / bit-width-0 ALP main case.
func alpCVConstant(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = 123.45
	}

	return out
}

// alpCVSpecialsList returns the fixed set of special float64 values mirrored
// from numeric_alp_golden_test.go's mixedExceptions fixture, extended with
// signed zero, signed MaxFloat64, and signed denormals.
func alpCVSpecialsList() []float64 {
	return []float64{
		0.0,
		math.Copysign(0, -1),
		math.Inf(1),
		math.Inf(-1),
		math.NaN(),
		math.MaxFloat64,
		-math.MaxFloat64,
		math.SmallestNonzeroFloat64,
		-math.SmallestNonzeroFloat64,
	}
}

// alpCVMixedSpecials mirrors numeric_alp_golden_test.go's mixedExceptions
// fixture: a clean decimal2dp base column with every 97th (1-indexed) value
// replaced by a rotating special value, forcing ALP main's exception sidecar
// path while most of the column stays a clean, zero-exception decimal.
func alpCVMixedSpecials(rng *rand.Rand, n int) []float64 {
	out := alpCVDecimalWalk(rng, n, 2)
	specials := alpCVSpecialsList()
	for i := range out {
		if (i+1)%97 == 0 {
			out[i] = specials[i%len(specials)]
		}
	}

	return out
}

// alpCVPureSpecials is a column of NOTHING but the special values above,
// cycled to length n -- the degenerate case where a column contains no
// "normal" decimal data at all.
func alpCVPureSpecials(n int) []float64 {
	specials := alpCVSpecialsList()
	out := make([]float64, n)
	for i := range out {
		out[i] = specials[i%len(specials)]
	}

	return out
}

// alpCVRandomBitsRaw returns n fully-random (non-NaN/Inf) float64 bit
// patterns. This is the one column engineered to reliably force ALP's "raw"
// scheme (mirrors the raw_scheme case in TestNumericALP_DecodeAll_MatchesAll):
// independent random bit patterns have far more than 8 distinct upper-bit
// clusters, defeating both ALP main (not decimal) and ALP-RD's <=8-entry
// dictionary, so the "all three schemes occur" requirement on the corpus
// doesn't depend on chance.
func alpCVRandomBitsRaw(rng *rand.Rand, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		for {
			bits := rng.Uint64()
			if (bits>>52)&0x7FF == 0x7FF { // reject Inf/NaN
				continue
			}
			out[i] = math.Float64frombits(bits)

			break
		}
	}

	return out
}

// alpCrossVerCorpus returns one column per named, deterministic distribution,
// all drawn in order from a single rand.New(rand.NewSource(seed)) so the
// entire corpus is a pure function of (seed, n).
func alpCrossVerCorpus(seed int64, n int) []alpCorpusCol {
	rng := rand.New(rand.NewSource(seed))

	return []alpCorpusCol{
		{"decimal_0dp", alpCVDecimalWalk(rng, n, 0)},
		{"decimal_1dp", alpCVDecimalWalk(rng, n, 1)},
		{"decimal_2dp", alpCVDecimalWalk(rng, n, 2)},
		{"decimal_4dp", alpCVDecimalWalk(rng, n, 4)},
		{"mixed_dp", alpCVMixedDP(rng, n)},
		{"full_precision_uniform", alpCVUniform(rng, n)},
		{"full_precision_normal", alpCVNormal(rng, n)},
		{"log_normal", alpCVLogNormal(rng, n)},
		{"exact_integers", alpCVExactIntegers(rng, n)},
		{"large_magnitude_digit_bracket", alpCVLargeMagnitudeBracket(rng, n)},
		{"half_tie_0dp", alpCVHalfTies(rng, n, 0)},
		{"half_tie_1dp", alpCVHalfTies(rng, n, 1)},
		{"half_tie_2dp", alpCVHalfTies(rng, n, 2)},
		{"half_tie_4dp", alpCVHalfTies(rng, n, 4)},
		{"random_walk", alpCVRandomWalk(rng, n)},
		{"constant", alpCVConstant(n)},
		{"mixed_specials_every97th", alpCVMixedSpecials(rng, n)},
		{"pure_specials", alpCVPureSpecials(n)},
		{"random_bits_raw", alpCVRandomBitsRaw(rng, n)},
	}
}

// ---- shared test helpers ----

// alpCVEngine pairs a human-readable label with an endian engine, so tests
// can log which engine produced a failure.
type alpCVEngine struct {
	name string
	eng  endian.EndianEngine
}

// alpCVEngines returns both endian engines the harness must cover.
func alpCVEngines() []alpCVEngine {
	return []alpCVEngine{
		{"LE", endian.GetLittleEndianEngine()},
		{"BE", endian.GetBigEndianEngine()},
	}
}

// alpCVSchemeName maps an ALP scheme byte to its name for logging.
func alpCVSchemeName(b byte) string {
	switch b {
	case alpSchemeMain:
		return "main"
	case alpSchemeRD:
		return "rd"
	case alpSchemeRaw:
		return "raw"
	default:
		return fmt.Sprintf("unknown(%d)", b)
	}
}

// alpCVCheckDecodeAll asserts DecodeAll reproduces want bit-exactly.
func alpCVCheckDecodeAll(t *testing.T, label string, dec NumericALPDecoder, data []byte, want []float64) {
	t.Helper()

	dst := make([]float64, len(want))
	got := dec.DecodeAll(data, len(want), dst)
	if got != len(want) {
		t.Fatalf("%s: DecodeAll returned count=%d, want %d", label, got, len(want))
	}
	for i, w := range want {
		if math.Float64bits(dst[i]) != math.Float64bits(w) {
			t.Fatalf("%s: DecodeAll[%d] = 0x%x, want 0x%x", label, i, math.Float64bits(dst[i]), math.Float64bits(w))
		}
	}
}

// alpCVCheckAllIter asserts the All() pull iterator reproduces want
// bit-exactly, in order.
func alpCVCheckAllIter(t *testing.T, label string, dec NumericALPDecoder, data []byte, want []float64) {
	t.Helper()

	i := 0
	for v := range dec.All(data, len(want)) {
		if i >= len(want) {
			t.Fatalf("%s: All() yielded more than %d values", label, len(want))
		}
		if math.Float64bits(v) != math.Float64bits(want[i]) {
			t.Fatalf("%s: All()[%d] = 0x%x, want 0x%x", label, i, math.Float64bits(v), math.Float64bits(want[i]))
		}
		i++
	}
	if i != len(want) {
		t.Fatalf("%s: All() yielded %d values, want %d", label, i, len(want))
	}
}

// alpCVCheckAt asserts At() agrees with want bit-exactly at up to 64
// pseudo-random indices.
func alpCVCheckAt(t *testing.T, label string, dec NumericALPDecoder, data []byte, want []float64, rng *rand.Rand) {
	t.Helper()

	count := len(want)
	if count == 0 {
		return
	}
	checks := 64
	if checks > count {
		checks = count
	}
	for range checks {
		idx := rng.Intn(count)
		v, ok := dec.At(data, idx, count)
		if !ok {
			t.Fatalf("%s: At(%d) ok=false", label, idx)
		}
		if math.Float64bits(v) != math.Float64bits(want[idx]) {
			t.Fatalf("%s: At(%d) = 0x%x, want 0x%x", label, idx, math.Float64bits(v), math.Float64bits(want[idx]))
		}
	}
}

// ---- Step 2: TestALPCrossVer_Lossless ----

// TestALPCrossVer_Lossless proves encode -> decode is bit-exact for every
// corpus column, through both endian engines and all three of DecodeAll, All,
// and At -- the "lossless" leg of the replacement contract. It also logs
// each column's chosen scheme and requires that main, RD, and raw all occur
// somewhere in the corpus, so scheme coverage is visible and enforced.
func TestALPCrossVer_Lossless(t *testing.T) {
	n := alpCrossVerN(t)
	corpus := alpCrossVerCorpus(alpCrossVerSeed, n)
	rng := rand.New(rand.NewSource(alpCrossVerSeed + 1))

	seen := make(map[byte]bool, 3)
	for _, col := range corpus {
		var colScheme byte
		for engIdx, ce := range alpCVEngines() {
			data := alpEncodeSlice(col.values, ce.eng)
			require.NotEmptyf(t, data, "%s/%s: encoded data must not be empty", col.name, ce.name)
			if engIdx == 0 {
				colScheme = data[0]
			} else {
				require.Equalf(t, colScheme, data[0], "%s: scheme must not depend on the endian engine", col.name)
			}
			seen[data[0]] = true

			dec := NewNumericALPDecoder(ce.eng)
			label := col.name + "/" + ce.name
			alpCVCheckDecodeAll(t, label, dec, data, col.values)
			alpCVCheckAllIter(t, label, dec, data, col.values)
			alpCVCheckAt(t, label, dec, data, col.values, rng)
		}
		t.Logf("%-32s n=%-9d scheme=%s", col.name, len(col.values), alpCVSchemeName(colScheme))
	}

	require.Lenf(t, seen, 3, "corpus must exercise all three ALP schemes (main/rd/raw); saw scheme bytes=%v", seen)
}

// ---- Step 3: TestALPCrossVer_DigitDivergence ----

// TestALPCrossVer_DigitDivergence re-derives each main-scheme column's chosen
// (e,f) and compares the production alpEncodeDigit against the frozen
// refEncodeDigitRound reference, value by value. Divergences are LEGAL
// (rounding ties, domain-guard cases) and are only logged, never hard-failed
// on: the one assertion that always holds is that whatever production
// actually chose for a divergent value still round-trips bit-exactly, either
// via the digit it picked or (if it treated the value as an exception) via
// the verbatim exception sidecar.
//
// Before the fast-round change, refEncodeDigitRound was byte-for-byte
// identical to alpEncodeDigit, so every column's divergence count HAD to be
// 0; that run was the harness's own soundness proof. This test intentionally
// does NOT hard-code that expectation, because with the fast-round change in
// place divergences are legal and expected.
func TestALPCrossVer_DigitDivergence(t *testing.T) {
	n := alpCrossVerN(t)
	corpus := alpCrossVerCorpus(alpCrossVerSeed, n)
	eng := endian.GetLittleEndianEngine()

	for _, col := range corpus {
		data := alpEncodeSlice(col.values, eng)
		if data[0] != alpSchemeMain {
			t.Logf("%-32s scheme=%-4s (skipped: digit-divergence audit is main-scheme only)",
				col.name, alpCVSchemeName(data[0]))

			continue
		}

		stride := alpSampleStride(len(col.values))
		ee, ff := alpBestEF(col.values, stride)
		decoded := alpDecodeAll(data, len(col.values), eng)

		divergences := 0
		for i, v := range col.values {
			prodDigit, prodOK := alpEncodeDigit(v, ee, ff)
			refDigit, refOK := refEncodeDigitRound(v, ee, ff)
			if prodOK == refOK && prodDigit == refDigit {
				continue
			}
			divergences++
			if math.Float64bits(decoded[i]) != math.Float64bits(v) {
				t.Fatalf("%s[%d]: divergent value did not round-trip bit-exactly (e=%d f=%d, prod=(%d,%v) ref=(%d,%v))",
					col.name, i, ee, ff, prodDigit, prodOK, refDigit, refOK)
			}
		}
		t.Logf("%-32s e=%-2d f=%-2d n=%-9d divergences=%d", col.name, ee, ff, len(col.values), divergences)
	}
}

// ---- Step 4: TestALPCrossVer_SizeParity ----

// TestALPCrossVer_SizeParity encodes the full corpus and asserts the
// aggregate encoded size stays within +0.5% of alpCrossVerBaselineBytes -- the
// "ratio parity" leg of the replacement contract. It only asserts when
// running at the exact scale alpCrossVerBaselineBytes was recorded at
// (n == alpCrossVerDefaultN); at any other MEBO_ALP_VERIFY_N the aggregate
// isn't comparable to that constant, so it is logged instead.
//
// Set MEBO_ALP_VERIFY_RECORD=1 to print the aggregate for pasting into
// alpCrossVerBaselineBytes instead of asserting against it (used exactly
// once, before the fast-round change landed).
func TestALPCrossVer_SizeParity(t *testing.T) {
	n := alpCrossVerN(t)
	corpus := alpCrossVerCorpus(alpCrossVerSeed, n)
	eng := endian.GetLittleEndianEngine()

	var aggregate int64
	for _, col := range corpus {
		data := alpEncodeSlice(col.values, eng)
		aggregate += int64(len(data))
	}

	if os.Getenv("MEBO_ALP_VERIFY_RECORD") == "1" {
		t.Logf("RECORD MODE: alpCrossVerBaselineBytes = %d (n=%d)", aggregate, n)
		if n != alpCrossVerDefaultN {
			t.Logf("WARNING: recorded at n=%d, not the default %d -- alpCrossVerBaselineBytes must be captured "+
				"at the DEFAULT n or this test's assertion is not meaningful", n, alpCrossVerDefaultN)
		}

		return
	}

	if n != alpCrossVerDefaultN {
		t.Logf("n=%d != default %d: alpCrossVerBaselineBytes was pinned at the default n, so the +0.5%% budget "+
			"is not comparable at this scale; logging aggregate=%d bytes only (no assertion)",
			n, alpCrossVerDefaultN, aggregate)

		return
	}

	budget := float64(alpCrossVerBaselineBytes) * 1.005
	t.Logf("aggregate=%d bytes, baseline constant=%d, +0.5%% budget=%.0f", aggregate, alpCrossVerBaselineBytes, budget)
	require.LessOrEqualf(t, float64(aggregate), budget,
		"corpus aggregate size %d exceeds the +0.5%% budget (%.0f bytes) over the baseline constant %d",
		aggregate, budget, alpCrossVerBaselineBytes)
}
