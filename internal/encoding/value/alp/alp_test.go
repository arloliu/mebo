package alp

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/arch"
	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/require"
)

func TestALPCodecContract(t *testing.T) {
	values := []float64{12.34, 12.34, 12.35, 12.36}
	encoder := NewNumericALPEncoder(endian.GetLittleEndianEngine())
	t.Cleanup(encoder.Finish)
	encoder.WriteSlice(values)

	decoder := NewNumericALPDecoder(endian.GetLittleEndianEngine())
	decoded := make([]float64, len(values))
	require.Equal(t, len(values), decoder.DecodeAll(encoder.Bytes(), len(values), decoded))
	require.Equal(t, values, decoded)

	at, ok := decoder.At(encoder.Bytes(), 2, len(values))
	require.True(t, ok)
	require.Equal(t, values[2], at)
	require.Zero(t, decoder.DecodeAll([]byte{0xff}, len(values), decoded))
}

func alpDecodeAll(data []byte, count int, eng endian.EndianEngine) []float64 {
	dec := NewNumericALPDecoder(eng)
	out := make([]float64, 0, count)
	for v := range dec.All(data, count) {
		out = append(out, v)
	}

	return out
}

func alpEncodeSlice(values []float64, eng endian.EndianEngine) []byte {
	enc := NewNumericALPEncoder(eng)
	enc.WriteSlice(values)
	data := append([]byte(nil), enc.Bytes()...)
	enc.Finish()

	return data
}

// TestNumericALP_RoundTrip_DecimalAndFullPrecision proves lossless round-trip on
// both ALP-favourable decimal data and ALP-hostile full-precision data (which
// must route through ALP-RD).
func TestNumericALP_RoundTrip_DecimalAndFullPrecision(t *testing.T) {
	for _, eng := range []endian.EndianEngine{endian.GetLittleEndianEngine(), endian.GetBigEndianEngine()} {
		for _, d := range pocFloatDatasets() {
			data := alpEncodeSlice(d.values, eng)
			got := alpDecodeAll(data, len(d.values), eng)
			require.Equalf(t, d.values, got, "%s: ALP round-trip must be bit-exact", d.name)

			// At() must agree with All() at every index.
			dec := NewNumericALPDecoder(eng)
			for i := range d.values {
				v, ok := dec.At(data, i, len(d.values))
				require.Truef(t, ok, "%s At(%d)", d.name, i)
				require.Equalf(t, d.values[i], v, "%s At(%d)", d.name, i)
			}
		}
	}
}

func TestNumericALP_EdgeCases(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	cases := [][]float64{
		{},
		{42.5},
		{1.0, 1.0, 1.0},
		{0.0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1)},
		{math.MaxFloat64, math.SmallestNonzeroFloat64, -math.MaxFloat64},
		{1.5, 2.25, 3.125, 100.0, 0.001},
	}
	for ci, vals := range cases {
		data := alpEncodeSlice(vals, eng)
		got := alpDecodeAll(data, len(vals), eng)
		if len(vals) == 0 {
			require.Empty(t, got, "case %d", ci)
			continue
		}
		require.Equalf(t, vals, got, "case %d round-trip", ci)
	}
}

// TestNumericALP_BitExact checks values whose bit pattern must survive even when
// float == would treat them as equal (negative zero is the classic case).
func TestNumericALP_BitExact(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	vals := []float64{math.Copysign(0, -1), 1.5, 0.0, 100.25}
	data := alpEncodeSlice(vals, eng)
	got := alpDecodeAll(data, len(vals), eng)
	for i := range vals {
		require.Equalf(t, math.Float64bits(vals[i]), math.Float64bits(got[i]),
			"index %d must be bit-exact (got %v want %v)", i, got[i], vals[i])
	}
}

func TestNumericALP_MultiSegmentReset(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	segA := quantize(generateSmallJitterMetric(200, 42.0, 0.003, 0.001), 2) // decimal -> main
	segB := randomWalk(50, 100, 0.5, 9)                                     // full precision -> RD

	enc := NewNumericALPEncoder(eng)
	enc.WriteSlice(segA)
	_ = enc.Bytes()
	lenA := enc.Size()
	enc.Reset()
	enc.WriteSlice(segB)
	full := append([]byte(nil), enc.Bytes()...)
	require.Equal(t, len(segA)+len(segB), enc.Len())
	enc.Finish()

	require.Equal(t, segA, alpDecodeAll(full[:lenA], len(segA), eng))
	require.Equal(t, segB, alpDecodeAll(full[lenA:], len(segB), eng))
}

func TestNumericALP_LargeColumnExceptions(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	const n = 70000 // > 65535 so positions exceed uint16
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = float64(i) * 0.25 // clean decimals -> ALP main
	}
	vals[66000] = 1.2345678901234e300 // a forced exception PAST position 65535
	data := alpEncodeSlice(vals, eng)
	got := alpDecodeAll(data, n, eng)
	require.Equal(t, vals, got, "exception past pos 65535 must round-trip")
}

// TestNumericALP_At_MatchesAll pins At's per-index output against All's
// batch decode before the windowed-read/binary-search rewrite: every index
// of every dataset below must produce the identical value through both
// paths, for both ALP schemes and (for one dataset) both endian engines.
func TestNumericALP_At_MatchesAll(t *testing.T) {
	// The four golden datasets (alp_test.go): decimal (main,
	// no exceptions), full precision (routes to RD), main with a forced
	// exception sidecar, and a width-0 constant column.
	decimal2dp := genALPColumns(1, alpGoldenPoints, 2, alpGoldenSeed)[0]
	fullPrecision := genALPColumns(1, alpGoldenPoints, -1, alpGoldenSeed)[0]

	mixedExceptions := append([]float64(nil), decimal2dp...)
	for i := range mixedExceptions {
		if (i+1)%97 == 0 {
			mixedExceptions[i] = math.Pi * 1e17
		}
	}

	constant := make([]float64, alpGoldenPoints)
	for i := range constant {
		constant[i] = 123.45
	}

	// >70,000 points (sidecar positions exceed uint16) with exceptions both
	// below and past 65535, exercising ALP main's 32-bit position field.
	const nLarge = 70007
	large := make([]float64, nLarge)
	for i := range large {
		large[i] = float64(i) * 0.25
	}
	large[3] = 9.87654321e250
	large[66000] = 1.2345678901234e300

	// RD with scattered exceptions: a smooth full-precision random walk (RD's
	// best case) plus fully-random doubles whose upper bits miss the top-8
	// dictionary, forcing RD's exception sidecar (mirrors the rd_exceptions
	// case in TestNumericALP_DecodeAll_MatchesAll).
	rdRng := rand.New(rand.NewSource(7))
	randomDouble := func() float64 {
		for {
			bits := rdRng.Uint64()
			if (bits>>52)&0x7FF == 0x7FF { // reject Inf/NaN
				continue
			}

			return math.Float64frombits(bits)
		}
	}
	rdBase := genALPColumns(1, 300, -1, 42)[0]
	rdExceptions := append([]float64(nil), rdBase...)
	for i := 0; i < 12; i++ {
		rdExceptions[i*20+3] = randomDouble()
	}

	cases := []struct {
		name        string
		values      []float64
		bothEngines bool
	}{
		{"decimal2dp", decimal2dp, true}, // covers both endian engines
		{"fullPrecision", fullPrecision, false},
		{"mixedExceptions", mixedExceptions, true}, // big-endian witness for the exception binary-search hit path
		{"constant", constant, false},
		{"large_with_exceptions", large, false},
		{"rd_with_exceptions", rdExceptions, true}, // big-endian witness for the exception binary-search hit path
	}

	for _, tc := range cases {
		engines := []endian.EndianEngine{endian.GetLittleEndianEngine()}
		if tc.bothEngines {
			engines = append(engines, endian.GetBigEndianEngine())
		}
		for _, eng := range engines {
			data := alpEncodeSlice(tc.values, eng)
			dec := NewNumericALPDecoder(eng)
			want := alpDecodeAll(data, len(tc.values), eng)
			require.Lenf(t, want, len(tc.values), "%s: All() length", tc.name)
			for i := range tc.values {
				got, ok := dec.At(data, i, len(tc.values))
				require.Truef(t, ok, "%s At(%d)", tc.name, i)
				require.Equalf(t, want[i], got, "%s At(%d)", tc.name, i)
			}

			// Out-of-range index and unknown scheme must still return (0, false).
			_, ok := dec.At(data, -1, len(tc.values))
			require.Falsef(t, ok, "%s At(-1)", tc.name)
			_, ok = dec.At(data, len(tc.values), len(tc.values))
			require.Falsef(t, ok, "%s At(len)", tc.name)

			unknown := append([]byte(nil), data...)
			unknown[0] = 0xFF
			_, ok = dec.At(unknown, 0, len(tc.values))
			require.Falsef(t, ok, "%s At() with unknown scheme byte", tc.name)
		}
	}
}

// TestNumericALP_UnknownScheme_Decoders pins the internal decoders' behavior
// on an unknown scheme byte (the first one past ALPMaxSchemeByte): the
// blob-layer validation (blob/numeric_decoder.go's validateALPColumns) is
// what actually surfaces this as an error, but that seam deliberately keeps
// All/DecodeAll/At themselves error-free, per the alpScheme* doc comment in
// alp.go. This test pins that every one of those three methods
// still degrades the same documented way — All yields nothing, DecodeAll
// writes nothing and returns 0, At returns (0, false) — so a future change
// can't silently start panicking or returning garbage for corrupt data
// instead.
func TestNumericALP_UnknownScheme_Decoders(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	values := []float64{1.5, 2.25, 3.125, 4.0, 5.75}
	data := alpEncodeSlice(values, eng)
	require.LessOrEqualf(t, data[0], ALPMaxSchemeByte, "sanity: encoder must emit a known scheme byte")

	unknown := append([]byte(nil), data...)
	unknown[0] = ALPMaxSchemeByte + 1 // first unknown scheme byte (3)

	dec := NewNumericALPDecoder(eng)

	t.Run("All", func(t *testing.T) {
		var got []float64
		for v := range dec.All(unknown, len(values)) {
			got = append(got, v)
		}
		require.Empty(t, got, "All must yield nothing for an unknown scheme byte")
	})

	t.Run("DecodeAll", func(t *testing.T) {
		const sentinel = -999999.5
		dst := make([]float64, len(values))
		for i := range dst {
			dst[i] = sentinel
		}
		n := dec.DecodeAll(unknown, len(values), dst)
		require.Zerof(t, n, "DecodeAll must return 0 for an unknown scheme byte")
		for i := range dst {
			require.Equalf(t, sentinel, dst[i], "DecodeAll must not write dst[%d] for an unknown scheme byte", i)
		}
	})

	t.Run("At", func(t *testing.T) {
		for i := range values {
			v, ok := dec.At(unknown, i, len(values))
			require.Falsef(t, ok, "At(%d) must return ok=false for an unknown scheme byte", i)
			require.Zerof(t, v, "At(%d) must return 0 for an unknown scheme byte", i)
		}
	})
}

// alpDecodeAllCase drives DecodeAll against All() for a single (data, count)
// pair across three dst shapes: exact length, shorter than count (must return
// len(dst) and only fill that many), and longer than count (must return count
// and leave the tail untouched). This pins DecodeAll's clamping semantics
// before decodeMainInto/decodeRDInto replace the iterator-wrapping
// implementation.
func alpDecodeAllCase(t *testing.T, name string, data []byte, count int) {
	t.Helper()
	dec := NewNumericALPDecoder(endian.GetLittleEndianEngine())

	want := make([]float64, 0, count)
	for v := range dec.All(data, count) {
		want = append(want, v)
	}

	// exact-length dst
	exact := make([]float64, count)
	n := dec.DecodeAll(data, count, exact)
	require.Equalf(t, count, n, "%s: exact dst count", name)
	require.Equalf(t, want, exact, "%s: exact dst values", name)

	// dst longer than count: prefix must match, tail must be untouched.
	const sentinel = -999999.5
	longer := make([]float64, count+5)
	for i := range longer {
		longer[i] = sentinel
	}
	n = dec.DecodeAll(data, count, longer)
	require.Equalf(t, count, n, "%s: longer dst count", name)
	require.Equalf(t, want, longer[:count], "%s: longer dst prefix", name)
	for i := count; i < len(longer); i++ {
		require.Equalf(t, sentinel, longer[i], "%s: longer dst tail[%d] must be untouched", name, i)
	}

	// dst shorter than count: only len(dst) values decoded, return len(dst).
	if count > 0 {
		short := make([]float64, count-1)
		n = dec.DecodeAll(data, count, short)
		require.Equalf(t, count-1, n, "%s: short dst count", name)
		require.Equalf(t, want[:count-1], short, "%s: short dst values", name)
	}
}

func TestNumericALP_DecodeAll_MatchesAll(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	for _, d := range pocFloatDatasets() {
		data := alpEncodeSlice(d.values, eng)
		alpDecodeAllCase(t, d.name, data, len(d.values))
	}

	// count 0: DecodeAll must write nothing and return 0, regardless of dst length.
	zeroData := alpEncodeSlice([]float64{1, 2, 3}, eng)
	alpDecodeAllCase(t, "count0", zeroData, 0)

	// ALP main with exceptions: every 37th value is an irrational constant that
	// cannot round-trip through the chosen (e,f) digit encoding, forcing the
	// exception sidecar path (verified: scheme=main, nExc=8).
	mainBase := genALPColumns(1, 300, 2, 42)[0]
	mainExc := append([]float64(nil), mainBase...)
	for i := range mainExc {
		if (i+1)%37 == 0 {
			mainExc[i] = math.Pi * 1e17
		}
	}
	mainExcData := alpEncodeSlice(mainExc, eng)
	require.Equalf(t, alpSchemeMain, mainExcData[0], "main_exceptions: expected scheme=main")
	alpDecodeAllCase(t, "main_exceptions", mainExcData, len(mainExc))

	// ALP-RD with exceptions: a smooth full-precision random walk (RD's best
	// case) plus scattered fully-random doubles whose upper bits miss the
	// top-8 dictionary, forcing RD's exception sidecar path (verified:
	// scheme=rd, nExc=5).
	rng := rand.New(rand.NewSource(7))
	randomDouble := func() float64 {
		for {
			bits := rng.Uint64()
			if (bits>>52)&0x7FF == 0x7FF { // reject Inf/NaN
				continue
			}

			return math.Float64frombits(bits)
		}
	}
	rdBase := genALPColumns(1, 300, -1, 42)[0]
	rdExc := append([]float64(nil), rdBase...)
	for i := 0; i < 12; i++ {
		rdExc[i*20+3] = randomDouble()
	}
	rdExcData := alpEncodeSlice(rdExc, eng)
	require.Equalf(t, alpSchemeRD, rdExcData[0], "rd_exceptions: expected scheme=rd")
	alpDecodeAllCase(t, "rd_exceptions", rdExcData, len(rdExc))

	// raw scheme: fully random bit patterns defeat both ALP main and ALP-RD,
	// falling back to the raw escape hatch (verified: scheme=raw).
	rawRng := rand.New(rand.NewSource(99))
	raw := make([]float64, 200)
	for i := range raw {
		for {
			bits := rawRng.Uint64()
			if (bits>>52)&0x7FF == 0x7FF {
				continue
			}
			raw[i] = math.Float64frombits(bits)

			break
		}
	}
	rawData := alpEncodeSlice(raw, eng)
	require.Equalf(t, alpSchemeRaw, rawData[0], "raw_scheme: expected scheme=raw")
	alpDecodeAllCase(t, "raw_scheme", rawData, len(raw))

	// width 0: a constant column drives FOR min == max, i.e. bit width 0 — the
	// degenerate bit-unpacking edge case (verified: scheme=main, width=0).
	constant := make([]float64, 50)
	for i := range constant {
		constant[i] = 123.45
	}
	constantData := alpEncodeSlice(constant, eng)
	require.Equalf(t, alpSchemeMain, constantData[0], "width0: expected scheme=main")
	require.Equalf(t, byte(0), constantData[3], "width0: expected width=0")
	alpDecodeAllCase(t, "width0_constant", constantData, len(constant))

	// width 0 with exceptions: same constant base, but one value mid-column is
	// un-encodable under the column's chosen (e,f), forcing the exception
	// sidecar while every other value still shares a single FOR-subtracted
	// code, so width stays 0 even though nExc >= 1. This exercises the scalar
	// width-0 bulk loop (mask == 0, no fused-kernel dispatch since width < 1)
	// together with the exception post-pass (verified: scheme=main, width=0,
	// nExc>=1).
	width0Exc := make([]float64, 50)
	for i := range width0Exc {
		width0Exc[i] = 123.45
	}
	width0Exc[25] = math.Pi * 1e17 // irrational full-precision value can't round-trip the constant's (e,f)
	width0ExcData := alpEncodeSlice(width0Exc, eng)
	require.Equalf(t, alpSchemeMain, width0ExcData[0], "width0_exceptions: expected scheme=main")
	require.Equalf(t, byte(0), width0ExcData[3], "width0_exceptions: expected width=0")
	require.Greaterf(t, eng.Uint32(width0ExcData[4:8]), uint32(0), "width0_exceptions: expected nExc>=1")
	alpDecodeAllCase(t, "width0_exceptions", width0ExcData, len(width0Exc))
}

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
//	MEBO_ALP_VERIFY_N=10000000 go test ./internal/encoding/value/alp -run TestALPCrossVer -v
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
	//	MEBO_ALP_VERIFY_RECORD=1 go test ./internal/encoding/value/alp -run TestALPCrossVer_SizeParity -v
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
// (alp.go) as it existed before the fast-round change (pure
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
// (mirrors genALPColumns in alp_test.go).
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
// from alp_test.go's mixedExceptions fixture, extended with
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

// alpCVMixedSpecials mirrors alp_test.go's mixedExceptions
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

// ---------------------------------------------------------------------------
// AVX-512 fused ALP-main decode kernel — differential test.
//
// The kernel (alpFusedDecodeAVX512 -> alpFusedDecodeAVX512Asm) must reproduce
// the scalar decode of decodeMainInto bit-for-bit (math.Float64bits equality)
// for the whole-group prefix it decodes; the caller finishes the remainder and
// the <8-value tail in Go. This test calls the kernel function DIRECTLY under
// the arch gate (so it exercises the real asm on AVX-512DQ hardware whether or
// not the dispatch is wired) and reconstructs the exact caller contract: kernel
// prefix + scalar tail, compared to a pure scalar reference over all N values.
//
// Coverage: every width 1..64 (including the {59,61,62,63} lane-straddle widths
// the two-gather funnel must handle) x counts spanning full groups and odd
// tails (n%8 != 0) x adversarial FOR minimums (incl. MinInt64/MaxInt64 wrap)
// x pf/ie scale combos x random codes (masked to width, so max-width all-ones
// values are exercised). A tight-buffer variant witnesses the wrapper's gather
// over-read bound is memory-safe (no fault, still bit-equal).
// ---------------------------------------------------------------------------

// alpDecodeTailScalar decodes values [start,n) exactly as decodeMainInto's
// scalar remainder loop does, so the test reproduces the real caller contract
// (kernel prefix + Go tail) rather than short-circuiting the seam.
func alpDecodeTailScalar(codes []byte, start, n, width int, mn int64, pf, ie float64, dst []float64) {
	mask := maskW(width)
	clen := len(codes)
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
		dst[i] = float64(int64(code)+mn) * pf * ie
		bitpos += width
	}
}

func TestALPFusedDecodeAVX512_Differential(t *testing.T) {
	if !arch.X86HasAVX512DQ() {
		t.Skip("AVX-512DQ not available on this CPU; kernel path cannot be exercised")
	}

	rng := rand.New(rand.NewSource(20260716))
	counts := []int{8, 9, 15, 16, 17, 24, 31, 32, 64, 255, 256, 1000, 1024}
	mns := []int64{0, 42, -1234567, math.MinInt64, math.MaxInt64, math.MinInt64 / 3}
	scales := []struct{ pf, ie float64 }{
		{1, 1},
		{alpPow10[2], alpInvPow10[0]},  // 2dp-like
		{alpPow10[7], alpInvPow10[4]},  // full-precision-like
		{alpPow10[18], alpInvPow10[0]}, // extreme magnitude
	}

	const poison = 0xDEADBEEFCAFEBABE

	for w := 1; w <= 64; w++ {
		mask := maskW(w)
		// Over-read slack the asm gather needs beyond the exact ceil(n*w/8)
		// packed bytes: hi qword of the last lane plus its 8-byte load.
		pad := ((7 * w) >> 3) + 16 + 8
		for _, n := range counts {
			codes := make([]uint64, n)
			for i := range codes {
				codes[i] = rng.Uint64() & mask
			}
			packed := alpPackBits(nil, codes, w) // exactly ceil(n*w/8) bytes

			// (a) padded buffer: over-read region is real, noise-filled memory,
			// so the kernel covers the maximum number of groups.
			padded := make([]byte, len(packed)+pad)
			copy(padded, packed)
			for i := len(packed); i < len(padded); i++ {
				padded[i] = byte(rng.Intn(256))
			}

			for _, mn := range mns {
				for _, s := range scales {
					for variant, buf := range [][]byte{padded, packed} {
						got := make([]float64, n)
						want := make([]float64, n)
						for i := range got {
							got[i] = math.Float64frombits(poison)
						}

						nk := alpFusedDecodeAVX512(buf, n, w, mn, s.pf, s.ie, got)
						if nk%8 != 0 || nk < 0 || nk > n {
							t.Fatalf("w=%d n=%d variant=%d: bad decoded count %d", w, n, variant, nk)
						}
						// Finish the tail exactly as the caller would.
						alpDecodeTailScalar(buf, nk, n, w, mn, s.pf, s.ie, got)

						// Pure scalar reference over all N. alpDecodeCodesScalarRef is
						// itself pinned bit-equal to the generated kernels (alpFusedUnpack)
						// by the pre-existing TestALPKernel_Differential, so this asm-vs-oracle
						// comparison transitively proves asm-vs-generated-kernels equivalence.
						alpDecodeCodesScalarRef(buf, n, w, mn, s.pf, s.ie, want)

						for i := 0; i < n; i++ {
							if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
								t.Fatalf("w=%d n=%d mn=%d pf=%g ie=%g variant=%d nk=%d idx=%d: got %#016x want %#016x",
									w, n, mn, s.pf, s.ie, variant, nk, i,
									math.Float64bits(got[i]), math.Float64bits(want[i]))
							}
						}
					}
				}
			}
		}
	}
}

// TestALPFusedDecodeAVX512_Guards pins the wrapper's decline paths: sub-group
// counts, out-of-range widths, and buffers too short for even one group's
// gather must return 0 (caller decodes everything scalar) and never fault.
func TestALPFusedDecodeAVX512_Guards(t *testing.T) {
	if !arch.X86HasAVX512DQ() {
		t.Skip("AVX-512DQ not available on this CPU")
	}
	dst := make([]float64, 64)
	big := make([]byte, 4096)

	if got := alpFusedDecodeAVX512(big, 7, 8, 0, 1, 1, dst); got != 0 {
		t.Fatalf("n<8 should decline, got %d", got)
	}
	if got := alpFusedDecodeAVX512(big, 64, 0, 0, 1, 1, dst); got != 0 {
		t.Fatalf("width 0 should decline, got %d", got)
	}
	if got := alpFusedDecodeAVX512(big, 64, 65, 0, 1, 1, dst); got != 0 {
		t.Fatalf("width>64 should decline, got %d", got)
	}
	// Buffer too short for a single width-64 group's two-gather reach.
	short := make([]byte, 8)
	if got := alpFusedDecodeAVX512(short, 8, 64, 0, 1, 1, dst); got != 0 {
		t.Fatalf("short buffer should decline, got %d", got)
	}
}

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

// TestNumericALP_GoldenBytes pins NumericALPEncoder's output — for four
// representative datasets, encoded with both endian engines — to recorded
// xxhash.Sum64 constants. This is the safety net for the ALP performance
// work: every Phase 1-2 optimization to alp.go must keep the
// encoder's bytes identical, and this test fails the instant a single
// output byte changes.
//
// To regenerate the constants after an INTENTIONAL wire-format change, run
//
//	go test ./internal/encoding/value/alp -run TestNumericALP_GoldenBytes -v
//
// read the "hash: 0x..." lines from the log output, and paste the new
// values in below.
//
// 2026-07: the fast-round optimization (magic-number round with a
// legacy math.Round fallback for |scaled| >= 2^51) was adopted in
// alpEncodeDigit/alpBestEF and the encoder's output was verified
// byte-identical on this corpus — the constants below did NOT need
// regeneration. The byte-identity contract is now enforced empirically by
// alp_test.go (lossless, digit-divergence, and size-parity
// tests, gate-run at MEBO_ALP_VERIFY_N=10000000) rather than by policy.
const (
	alpGoldenPoints = 1000
	alpGoldenSeed   = 42

	alpGoldenDecimal2dpLE      uint64 = 0xc642985c2b608526
	alpGoldenDecimal2dpBE      uint64 = 0xa1b2f948018ababa
	alpGoldenFullPrecisionLE   uint64 = 0x7a1535f788abf63e
	alpGoldenFullPrecisionBE   uint64 = 0x012f9aeaafd7889e
	alpGoldenMixedExceptionsLE uint64 = 0x6ce54b0b6aeb51c0
	alpGoldenMixedExceptionsBE uint64 = 0x00a6c863bdfd2940
	alpGoldenConstantLE        uint64 = 0x9569360489db2778
	alpGoldenConstantBE        uint64 = 0xf24ee91274dbe21e
)

// alpGoldenHash encodes values with the given engine and returns the
// xxhash.Sum64 of the resulting bytes.
func alpGoldenHash(values []float64, eng endian.EndianEngine) uint64 {
	enc := NewNumericALPEncoder(eng)
	enc.WriteSlice(values)
	h := xxhash.Sum64(enc.Bytes())
	enc.Finish()

	return h
}

func TestNumericALP_GoldenBytes(t *testing.T) {
	decimal2dp := genALPColumns(1, alpGoldenPoints, 2, alpGoldenSeed)[0]
	fullPrecision := genALPColumns(1, alpGoldenPoints, -1, alpGoldenSeed)[0]

	// Same 2dp random walk, but every 97th value (1-indexed) is replaced by a
	// full-precision irrational constant that cannot round-trip through ALP
	// main's (e,f) digit encoding — forcing the exception sidecar path.
	mixedExceptions := append([]float64(nil), decimal2dp...)
	for i := range mixedExceptions {
		if (i+1)%97 == 0 {
			mixedExceptions[i] = math.Pi * 1e17
		}
	}

	// A constant column drives FOR min == max, i.e. bit width 0 — the
	// degenerate alpPackBits early-return path.
	constant := make([]float64, alpGoldenPoints)
	for i := range constant {
		constant[i] = 123.45
	}

	cases := []struct {
		name   string
		values []float64
		wantLE uint64
		wantBE uint64
	}{
		{"decimal2dp", decimal2dp, alpGoldenDecimal2dpLE, alpGoldenDecimal2dpBE},
		{"fullPrecision", fullPrecision, alpGoldenFullPrecisionLE, alpGoldenFullPrecisionBE},
		{"mixedExceptions", mixedExceptions, alpGoldenMixedExceptionsLE, alpGoldenMixedExceptionsBE},
		{"constant", constant, alpGoldenConstantLE, alpGoldenConstantBE},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLE := alpGoldenHash(tc.values, endian.GetLittleEndianEngine())
			gotBE := alpGoldenHash(tc.values, endian.GetBigEndianEngine())
			t.Logf("%s little-endian hash: 0x%016x", tc.name, gotLE)
			t.Logf("%s big-endian hash: 0x%016x", tc.name, gotBE)
			require.Equalf(t, tc.wantLE, gotLE,
				"%s: little-endian ALP output changed (got hash 0x%016x)", tc.name, gotLE)
			require.Equalf(t, tc.wantBE, gotBE,
				"%s: big-endian ALP output changed (got hash 0x%016x)", tc.name, gotBE)
		})
	}
}

// ---- frozen references (the pre-kernel scalar decode that must be preserved) ----

// alpDecodeCodesScalarRef is the exact per-value scalar decode of decodeMainInto's
// bulk loop (windowed 8-byte load + shift/mask, alpReadBitsSlow tail fallback,
// then float64(int64(code)+mn)*pf*ie). It is the value-level oracle the fused
// kernels must reproduce bit-for-bit.
func alpDecodeCodesScalarRef(codes []byte, count, width int, mn int64, pf, ie float64, dst []float64) {
	mask := ^uint64(0)
	if width < 64 {
		mask = (uint64(1) << uint(width)) - 1
	}
	clen := len(codes)
	bitpos := 0
	for i := 0; i < count; i++ {
		bp := bitpos >> 3
		sh := bitpos & 7
		var code uint64
		if bp+8 <= clen && sh+width <= 64 {
			code = (binary.LittleEndian.Uint64(codes[bp:bp+8]) >> sh) & mask
		} else {
			code = alpReadBitsSlow(codes, bitpos, width, mask)
		}
		dst[i] = float64(int64(code)+mn) * pf * ie
		bitpos += width
	}
}

// refDecodeMainInto is a frozen byte-for-byte copy of decodeMainInto as it stood
// before kernel dispatch was wired in (the scalar loop + post-pass exception
// patch). TestALPDecodeMainInto_Kernels pins the wired decoder against it.
func refDecodeMainInto(eng endian.EndianEngine, data []byte, count int, dst []float64) int {
	ee := int(data[0])
	ff := int(data[1])
	width := int(data[2])
	nExc := int(eng.Uint32(data[3:7]))
	mn := int64(eng.Uint64(data[7:15]))
	codes := data[15:]
	exc := data[15+(count*width+7)/8:]

	mask := ^uint64(0)
	if width < 64 {
		mask = (uint64(1) << uint(width)) - 1
	}
	clen := len(codes)

	n := count
	if n > len(dst) {
		n = len(dst)
	}
	bitpos := 0
	for i := 0; i < n; i++ {
		bp := bitpos >> 3
		sh := bitpos & 7
		var code uint64
		if bp+8 <= clen && sh+width <= 64 {
			code = (binary.LittleEndian.Uint64(codes[bp:bp+8]) >> sh) & mask
		} else {
			code = alpReadBitsSlow(codes, bitpos, width, mask)
		}
		dst[i] = float64(int64(code)+mn) * alpPow10[ff] * alpInvPow10[ee]
		bitpos += width
	}
	for k := 0; k < nExc; k++ {
		p := int(eng.Uint32(exc[k*12 : k*12+4]))
		if p < n {
			dst[p] = math.Float64frombits(eng.Uint64(exc[k*12+4 : k*12+12]))
		}
	}

	return n
}

// buildALPMain assembles a synthetic ALP-main body in the exact layout
// decodeMainInto parses (header + LSB-first packed codes + 12-byte exception
// records), letting the tests sweep every width/count/exception shape directly
// rather than relying on the encoder's data-driven width choice. It returns the
// data[1:] form (no scheme byte). Exception positions must be < len(codes); the
// code stored at those positions is irrelevant (the patch overwrites them).
func buildALPMain(eng endian.EndianEngine, ee, ff, width int, mn int64, codes []uint64, excPos []int, excVal []float64) []byte {
	var hdr [15]byte
	hdr[0] = byte(ee)
	hdr[1] = byte(ff)
	hdr[2] = byte(width)
	eng.PutUint32(hdr[3:7], uint32(len(excPos)))
	eng.PutUint64(hdr[7:15], uint64(mn))
	data := append([]byte(nil), hdr[:]...)
	data = alpPackBits(data, codes, width)
	for i := range excPos {
		var rec [12]byte
		eng.PutUint32(rec[0:4], uint32(excPos[i]))
		eng.PutUint64(rec[4:12], math.Float64bits(excVal[i]))
		data = append(data, rec[:]...)
	}

	return data
}

func maskW(width int) uint64 {
	if width >= 64 {
		return ^uint64(0)
	}

	return (uint64(1) << uint(width)) - 1
}

// TestALPKernel_Differential drives every non-nil width kernel directly and
// checks it reproduces the scalar decode bit-for-bit. Each case packs exactly
// ceil(n*w/8) code bytes plus the minimal over-read slack (8*ceil(w/8)-w bytes
// of random noise) — so the last group's array-pointer conversion lands exactly
// at the end of the slice. If the slack math were off by one, the conversion
// would panic here (this is the over-read boundary witness), and if the kernel
// depended on the noise bytes the value comparison would fail.
func TestALPKernel_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(20260715))
	counts := []int{8, 16, 1000, 1024}
	mns := []int64{-1234567, 42, 0}
	scales := []struct{ pf, ie float64 }{
		{1, 1},
		{alpPow10[2], alpInvPow10[0]}, // 2dp-like
		{alpPow10[7], alpInvPow10[4]},
	}
	for w := 1; w <= 64; w++ {
		kern := alpFusedUnpack[w]
		if kern == nil {
			continue
		}
		mask := maskW(w)
		nwords := (w + 7) / 8
		slack := nwords*8 - w
		for _, n := range counts {
			codes := make([]uint64, n)
			for i := range codes {
				codes[i] = rng.Uint64() & mask
			}
			packed := alpPackBits(nil, codes, w) // exactly ceil(n*w/8) bytes
			buf := make([]byte, len(packed)+slack)
			copy(buf, packed)
			for i := len(packed); i < len(buf); i++ {
				buf[i] = byte(rng.Intn(256)) // noise in the over-read region
			}
			for _, mn := range mns {
				for _, s := range scales {
					got := make([]float64, n)
					want := make([]float64, n)
					kern(buf, n, mn, s.pf, s.ie, got)
					alpDecodeCodesScalarRef(buf, n, w, mn, s.pf, s.ie, want)
					for i := 0; i < n; i++ {
						if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
							t.Fatalf("kernel w=%d n=%d mn=%d pf=%g ie=%g idx=%d: got %#016x want %#016x",
								w, n, mn, s.pf, s.ie, i, math.Float64bits(got[i]), math.Float64bits(want[i]))
						}
					}
				}
			}
		}
	}
}

// TestALPDecodeMainInto_Kernels pins the kernel-dispatched decodeMainInto against
// the frozen scalar reference across every width, a count set spanning multiples
// and non-multiples of 8 (tail handling) and the count<8 kernel-skip guard,
// positive/negative min, exceptions present/absent, and full/partial dst
// (clamping + exception p<n guard). The two must agree bit-for-bit.
func TestALPDecodeMainInto_Kernels(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	dec := NewNumericALPDecoder(eng)
	rng := rand.New(rand.NewSource(987654321))

	counts := []int{1, 3, 7, 8, 16, 1000, 1003, 1024}
	mns := []int64{-98765, 0, 31337}
	efs := []struct{ ee, ff int }{{0, 0}, {2, 0}, {7, 3}}

	for w := 1; w <= 64; w++ {
		mask := maskW(w)
		for _, count := range counts {
			codes := make([]uint64, count)
			for i := range codes {
				codes[i] = rng.Uint64() & mask
			}
			// exception shapes: none, and a spread including first/last.
			excShapes := [][]int{nil}
			if count >= 1 {
				spread := []int{0}
				if count > 2 {
					spread = append(spread, count/2)
				}
				if count > 1 {
					spread = append(spread, count-1)
				}
				excShapes = append(excShapes, spread)
			}
			for _, mn := range mns {
				ef := efs[(w+count)%len(efs)] // rotate e/f so all get exercised
				for _, excPos := range excShapes {
					excVal := make([]float64, len(excPos))
					for i := range excVal {
						excVal[i] = rng.NormFloat64() * math.Pi * 1e9
					}
					data := buildALPMain(eng, ef.ee, ef.ff, w, mn, codes, excPos, excVal)
					for _, dstLen := range []int{count, count - 3} {
						if dstLen <= 0 {
							continue
						}
						got := make([]float64, dstLen)
						want := make([]float64, dstLen)
						ng := dec.decodeMainInto(data, count, got)
						nw := refDecodeMainInto(eng, data, count, want)
						if ng != nw {
							t.Fatalf("w=%d count=%d dstLen=%d: n mismatch got %d want %d", w, count, dstLen, ng, nw)
						}
						for i := 0; i < dstLen; i++ {
							if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
								t.Fatalf("w=%d count=%d mn=%d ef=%v nExc=%d dstLen=%d idx=%d: got %#016x want %#016x",
									w, count, mn, ef, len(excPos), dstLen, i,
									math.Float64bits(got[i]), math.Float64bits(want[i]))
							}
						}
					}
				}
			}
		}
	}
}

// ---- reference implementations of the pre-optimization algorithms ----
// These pin the BEHAVIOR the optimized versions must preserve byte-for-byte.

// alpPackBitsRef is the original naive bit-by-bit packer.
func alpPackBitsRef(dst []byte, codes []uint64, width int) []byte {
	if width == 0 {
		return dst
	}
	start := len(dst)
	nbytes := (len(codes)*width + 7) / 8
	dst = append(dst, make([]byte, nbytes)...)
	bitpos := 0
	for _, c := range codes {
		for b := range width {
			if c&(uint64(1)<<uint(b)) != 0 {
				dst[start+(bitpos>>3)] |= 1 << uint(bitpos&7)
			}
			bitpos++
		}
	}

	return dst
}

// alpBestEFRef is the original exhaustive (non-pruned) (e,f) search.
func alpBestEFRef(values []float64, stride int) (bestE, bestF int) {
	best := math.MaxFloat64
	for e := 0; e <= alpMaxExponent; e++ {
		for f := 0; f <= e; f++ {
			var nExc, cnt int
			mn := int64(math.MaxInt64)
			mx := int64(math.MinInt64)
			for i := 0; i < len(values); i += stride {
				cnt++
				v := values[i]
				scaled := v * alpPow10[e] * alpInvPow10[f]
				var d int64
				if math.Abs(scaled) < 1<<51 {
					// Hot path: magic-number fast round (see alpEncodeDigit).
					d = int64(alpFastRound(scaled))
				} else {
					// Rare slow path: original math.Round semantics (see alpEncodeDigit).
					r := math.Round(scaled)
					if math.Abs(r) >= 9.2e18 {
						nExc++

						continue
					}
					d = int64(r)
				}
				if float64(d)*alpPow10[f]*alpInvPow10[e] != v {
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
			width := 0
			if nExc < cnt && mx >= mn {
				width = bits.Len64(uint64(mx - mn))
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

// TestAlpPackBits_Differential fuzzes the optimized word-at-a-time packer against
// the naive bit-by-bit reference across random widths, lengths, and code values.
func TestAlpPackBits_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for range 20000 {
		width := rng.Intn(65) // 0..64
		n := rng.Intn(40)
		codes := make([]uint64, n)
		for i := range codes {
			codes[i] = rng.Uint64()
		}
		// Random non-empty prefix so packing starts at a non-zero offset too.
		prefix := make([]byte, rng.Intn(5))
		for i := range prefix {
			prefix[i] = byte(rng.Intn(256))
		}
		got := alpPackBits(append([]byte(nil), prefix...), codes, width)
		want := alpPackBitsRef(append([]byte(nil), prefix...), codes, width)
		if string(got) != string(want) {
			t.Fatalf("alpPackBits mismatch: width=%d n=%d\n got=%x\nwant=%x", width, n, got, want)
		}
	}
}

// TestAlpBestEF_Differential fuzzes the pruned (e,f) search against the exhaustive
// reference across several value distributions; selection must be identical.
func TestAlpBestEF_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	gens := []func() float64{
		func() float64 { return math.Round(rng.Float64()*1000) / 100 },  // 2dp
		func() float64 { return math.Round(rng.Float64()*1e6) / 1e4 },   // 4dp
		func() float64 { return float64(rng.Intn(1 << 20)) },            // integers
		func() float64 { return rng.NormFloat64() * 1e6 },               // full precision
		func() float64 { return rng.NormFloat64() * 1e-9 },              // tiny
		func() float64 { return math.Round(rng.Float64() * 10) },        // 0dp
		func() float64 { return -math.Round(rng.Float64()*1000) / 100 }, // negative decimals
		func() float64 { return rng.Float64()*2e19 - 1e19 },             // huge magnitude, straddles the 9.2e18 estimator exception threshold
	}
	check := func(t *testing.T, col []float64) {
		t.Helper()
		n := len(col)
		stride := alpSampleStride(n)
		ge, gf := alpBestEF(col, stride)
		we, wf := alpBestEFRef(col, stride)
		if ge != we || gf != wf {
			t.Fatalf("alpBestEF mismatch n=%d: got (%d,%d) want (%d,%d)", n, ge, gf, we, wf)
		}
	}
	for range 4000 {
		n := 1 + rng.Intn(300)
		col := make([]float64, n)
		g := gens[rng.Intn(len(gens))]
		for i := range col {
			col[i] = g()
		}
		// occasionally inject a constant column, a sign mix, or sprinkle
		// mixed exceptions (full-precision noise) into otherwise-clean data
		switch rng.Intn(6) {
		case 0:
			for i := range col {
				col[i] = math.Copysign(col[i], float64(1-2*(i&1)))
			}
		case 1:
			c := g()
			for i := range col {
				col[i] = c // constant column
			}
		case 2:
			for i := range col {
				if i%7 == 0 {
					col[i] = rng.NormFloat64() * math.Pi * 1e11 // mixed exceptions
				}
			}
		default:
			// no perturbation: use the generator's raw output as-is
		}
		check(t, col)
	}

	// Deterministic boundary sweep: n<32, the 33..63 stride-1 full-copy region,
	// the n=64 stride-jump boundary, and large n, crossed with every value
	// shape above (plus a constant column) so the pruning state machine is
	// exercised right at alpSampleStride's stride transitions.
	for _, n := range []int{1, 2, 3, 16, 31, 32, 33, 47, 63, 64, 65, 127, 128, 191, 200, 300, 1000} {
		for _, g := range gens {
			col := make([]float64, n)
			for i := range col {
				col[i] = g()
			}
			check(t, col)
		}
		c := gens[rng.Intn(len(gens))]()
		col := make([]float64, n)
		for i := range col {
			col[i] = c // constant column at this size
		}
		check(t, col)
	}
}

func TestAlpBestEF_DifferentialBoundaries(t *testing.T) {
	const two51 = float64(int64(1) << 51)
	const two52 = float64(int64(1) << 52)
	const conversionGuard = float64(9.2e18)
	repeated := func(value float64) []float64 {
		return []float64{value, value, value, value, value, value}
	}

	cases := []struct {
		name   string
		values []float64
	}{
		{
			name: "rounding_boundaries",
			values: []float64{
				math.Nextafter(0.5, 0), 0.5, math.Nextafter(0.5, 1),
				math.Nextafter(-0.5, -1), -0.5, math.Nextafter(-0.5, 0),
				1.005, 2.675, -1.005, -2.675,
			},
		},
		{
			name: "specials_and_signed_zero",
			values: []float64{
				0, math.Copysign(0, -1), math.NaN(), math.Inf(1), math.Inf(-1),
				math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64,
			},
		},
		{
			name: "integer_magnitude_boundaries",
			values: []float64{
				math.Nextafter(two51, 0), two51, math.Nextafter(two51, math.Inf(1)),
				math.Nextafter(two52, 0), two52, math.Nextafter(two52, math.Inf(1)),
				float64(math.MaxInt64), math.Nextafter(float64(math.MaxInt64), 0),
				-float64(math.MaxInt64), math.MaxFloat64, -math.MaxFloat64,
			},
		},
		{
			name:   "conversion_guard_positive_below",
			values: repeated(math.Nextafter(conversionGuard, 0)),
		},
		{
			name:   "conversion_guard_positive_exact",
			values: repeated(conversionGuard),
		},
		{
			name:   "conversion_guard_positive_above",
			values: repeated(math.Nextafter(conversionGuard, math.Inf(1))),
		},
		{
			name:   "conversion_guard_negative_below",
			values: repeated(math.Nextafter(-conversionGuard, 0)),
		},
		{
			name:   "conversion_guard_negative_exact",
			values: repeated(-conversionGuard),
		},
		{
			name:   "conversion_guard_negative_above",
			values: repeated(math.Nextafter(-conversionGuard, math.Inf(-1))),
		},
		{
			name:   "negative_decimals",
			values: []float64{-100.25, -100.24, -1.01, -1, -0.01, -0.001},
		},
		{
			name: "tiny_values",
			values: []float64{
				1e-18, -1e-18, math.Nextafter(1e-18, 0),
				math.Nextafter(1e-18, math.Inf(1)), 1e-100, -1e-100,
			},
		},
		{name: "constant_tie", values: []float64{42, 42, 42, 42}},
		{
			name: "mixed_exceptions",
			values: []float64{
				100.01, 100.02, math.Pi * 1e17, 100.03, math.NaN(),
				100.04, math.Copysign(0, -1), 100.05, math.Inf(1),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, stride := range []int{1, 2, 3} {
				if stride > len(tc.values) {
					continue
				}

				gotE, gotF := alpBestEF(tc.values, stride)
				wantE, wantF := alpBestEFRef(tc.values, stride)
				require.Equalf(t, [2]int{wantE, wantF}, [2]int{gotE, gotF},
					"stride=%d", stride)
			}
		})
	}

	// Integer constants tie at the zero-bit estimate for many candidates. The
	// exhaustive oracle's strict less-than update keeps the first lexicographic
	// winner, and an optimized order must keep that same winner explicitly.
	gotE, gotF := alpBestEF([]float64{1, 1, 1, 1}, 1)
	require.Equal(t, [2]int{0, 0}, [2]int{gotE, gotF})
}

// ---- encode benchmarks (regression guards for the optimization) ----

// genALPColumns mimics measurev2's gauge generator: ±0.5% random walk from 100,
// quantized to `decimals` places (decimals<0 = full precision).
func genALPColumns(nCols, nPts, decimals int, seed int64) [][]float64 {
	rng := rand.New(rand.NewSource(seed))
	scale := math.Pow(10, float64(decimals))
	cols := make([][]float64, nCols)
	for c := range cols {
		col := make([]float64, nPts)
		cur := 100.0 + float64(c)*10.0
		for j := range col {
			cur += cur * (rng.Float64()*2.0 - 1.0) * 0.005
			if decimals >= 0 {
				col[j] = math.Round(cur*scale) / scale
			} else {
				col[j] = cur
			}
		}
		cols[c] = col
	}

	return cols
}

// ---- frozen reference (the pre-bulk-unpack decodeRDInto this task must preserve) ----

// refDecodeRDInto is a frozen byte-for-byte copy of decodeRDInto as it stood
// before the two-stream bulk unpack was wired in: two windowed per-value
// reads (alpReadBitsFast) for the left code and the right part, straight into
// dst, then the exception patch loop. TestALPDecodeRDInto_BulkUnpack pins the
// bulk-unpack decoder against this reference.
func refDecodeRDInto(eng endian.EndianEngine, data []byte, count int, dst []float64) int {
	rbw := int(data[0])
	codeBits := int(data[1])
	nDict := int(data[2])
	nExc := int(eng.Uint32(data[3:7]))
	off := 7
	var dict [alpRDMaxDictSize]uint64
	for i := 0; i < nDict; i++ {
		dict[i] = uint64(eng.Uint16(data[off : off+2]))
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

	for i := 0; i < n; i++ {
		right := alpReadBitsFast(rights, i*rbw, rbw, rightMask)
		left := dict[alpReadBitsFast(leftCodes, i*codeBits, codeBits, codeMask)]
		dst[i] = math.Float64frombits((left << rbw) | right)
	}
	for k := 0; k < nExc; k++ {
		p := int(eng.Uint32(exc[k*6 : k*6+4]))
		if p < n {
			left := uint64(eng.Uint16(exc[k*6+4 : k*6+6]))
			right := alpReadBitsFast(rights, p*rbw, rbw, rightMask)
			dst[p] = math.Float64frombits((left << rbw) | right)
		}
	}

	return n
}

// alpUnpackBitsScalarRef extracts n w-bit codes LSB-first from codes into dst
// using the same windowed-read building block the RD scalar path uses
// (alpReadBitsFast/alpReadBitsSlow) — the oracle alpUnpackBits[w] must match
// bit-for-bit.
func alpUnpackBitsScalarRef(codes []byte, n, w int, dst []uint64) {
	mask := maskW(w)
	for i := 0; i < n; i++ {
		dst[i] = alpReadBitsFast(codes, i*w, w, mask)
	}
}

// buildALPRD assembles a synthetic ALP-RD body in the exact layout
// decodeRDInto parses (header + dict + LSB-first packed left codes + LSB-first
// packed rights + 6-byte exception records), letting the tests sweep every
// (rbw, codeBits, count, exception) shape directly rather than relying on the
// encoder's data-driven parameter choice. It returns the data[1:] form (no
// scheme byte). nDict is taken as len(dict); exception positions must be <
// count.
func buildALPRD(eng endian.EndianEngine, rbw, codeBits int, dict, leftCodes, rights []uint64, excPos []int, excLeft []uint64) []byte {
	nDict := len(dict)
	var hdr [7]byte
	hdr[0] = byte(rbw)
	hdr[1] = byte(codeBits)
	hdr[2] = byte(nDict)
	eng.PutUint32(hdr[3:7], uint32(len(excPos)))
	data := append([]byte(nil), hdr[:]...)
	for i := 0; i < nDict; i++ {
		var b [2]byte
		eng.PutUint16(b[:], uint16(dict[i]))
		data = append(data, b[:]...)
	}
	data = alpPackBits(data, leftCodes, codeBits)
	data = alpPackBits(data, rights, rbw)
	for i := range excPos {
		var rec [6]byte
		eng.PutUint32(rec[0:4], uint32(excPos[i]))
		eng.PutUint16(rec[4:6], uint16(excLeft[i]))
		data = append(data, rec[:]...)
	}

	return data
}

// TestALPUnpackBitsKernel_Differential drives every non-nil width kernel in
// alpUnpackBits directly and checks it reproduces the scalar windowed-read
// extraction bit-for-bit. Each case packs exactly ceil(n*w/8) code bytes plus
// the minimal over-read slack (8*ceil(w/8)-w bytes of random noise) — the
// over-read boundary witness: an off-by-one in slack would panic at the last
// group's array-pointer conversion, and any dependence on the noise bytes
// would fail the value check (mirrors TestALPKernel_Differential in
// alp_test.go for the fused ALP-main kernels).
func TestALPUnpackBitsKernel_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(20260716))
	counts := []int{8, 16, 1000, 1024}
	for w := 1; w <= 64; w++ {
		kern := alpUnpackBits[w]
		if kern == nil {
			continue
		}
		mask := maskW(w)
		nwords := (w + 7) / 8
		slack := nwords*8 - w
		for _, n := range counts {
			codes := make([]uint64, n)
			for i := range codes {
				codes[i] = rng.Uint64() & mask
			}
			packed := alpPackBits(nil, codes, w) // exactly ceil(n*w/8) bytes
			buf := make([]byte, len(packed)+slack)
			copy(buf, packed)
			for i := len(packed); i < len(buf); i++ {
				buf[i] = byte(rng.Intn(256)) // noise in the over-read region
			}
			got := make([]uint64, n)
			want := make([]uint64, n)
			kern(buf, n, got)
			alpUnpackBitsScalarRef(buf, n, w, want)
			for i := 0; i < n; i++ {
				if got[i] != want[i] {
					t.Fatalf("unpack kernel w=%d n=%d idx=%d: got %#x want %#x", w, n, i, got[i], want[i])
				}
			}
		}
	}
}

// TestALPDecodeRDInto_BulkUnpack pins the kernel-dispatched decodeRDInto
// against the frozen scalar reference across every reachable (rbw, codeBits)
// combination (rbw 48..63, codeBits 1..3 — see alp.go's alpRDCutLimit
// and alpRDMaxDictSize), a count set spanning the count<8 kernel-skip guard,
// multiples and non-multiples of 8 (tail handling), exceptions present/
// absent, and full/partial dst (clamping + exception p<n guard). The two must
// agree bit-for-bit.
func TestALPDecodeRDInto_BulkUnpack(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	dec := NewNumericALPDecoder(eng)
	rng := rand.New(rand.NewSource(424242))

	counts := []int{1, 7, 8, 16, 1000, 1003, 1024}

	for rbw := 48; rbw <= 63; rbw++ {
		rMask := maskW(rbw)
		for codeBits := 1; codeBits <= 3; codeBits++ {
			cMask := maskW(codeBits)
			nDict := int(cMask) + 1
			if nDict > alpRDMaxDictSize {
				nDict = alpRDMaxDictSize
			}
			dict := make([]uint64, nDict)
			for i := range dict {
				dict[i] = rng.Uint64()
			}
			for _, count := range counts {
				leftCodes := make([]uint64, count)
				rights := make([]uint64, count)
				for i := range leftCodes {
					leftCodes[i] = rng.Uint64() & cMask
					rights[i] = rng.Uint64() & rMask
				}
				for _, withExc := range []bool{false, true} {
					var excPos []int
					var excLeft []uint64
					if withExc {
						seen := map[int]bool{}
						add := func(p int) {
							if p >= 0 && p < count && !seen[p] {
								seen[p] = true
								excPos = append(excPos, p)
							}
						}
						add(0)
						if count > 2 {
							add(count / 2)
						}
						if count > 1 {
							add(count - 1)
						}
						sort.Ints(excPos)
						for range excPos {
							excLeft = append(excLeft, rng.Uint64()&0xFFFF)
						}
					}
					data := buildALPRD(eng, rbw, codeBits, dict, leftCodes, rights, excPos, excLeft)
					for _, dstLen := range []int{count, count - 3} {
						if dstLen <= 0 {
							continue
						}
						got := make([]float64, dstLen)
						want := make([]float64, dstLen)
						ng := dec.decodeRDInto(data, count, got)
						nw := refDecodeRDInto(eng, data, count, want)
						if ng != nw {
							t.Fatalf("rbw=%d codeBits=%d count=%d dstLen=%d: n mismatch got %d want %d",
								rbw, codeBits, count, dstLen, ng, nw)
						}
						for i := 0; i < dstLen; i++ {
							if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
								t.Fatalf("rbw=%d codeBits=%d count=%d withExc=%v dstLen=%d idx=%d: got %#016x want %#016x",
									rbw, codeBits, count, withExc, dstLen, i,
									math.Float64bits(got[i]), math.Float64bits(want[i]))
							}
						}
					}
				}
			}
		}
	}
}

// TestALPDecodeRDInto_WholePipeline drives the full encode -> DecodeAll path
// (not the synthetic buildALPRD harness) for genuinely RD-routed full-
// precision columns at sizes that cross the pooled-scratch group boundary
// multiple times, and checks DecodeAll agrees with All() bit-for-bit.
func TestALPDecodeRDInto_WholePipeline(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	dec := NewNumericALPDecoder(eng)
	for _, n := range []int{8, 9, 50, 300, 1000, 2000, 4096} {
		values := genALPColumns(1, n, -1, 12345)[0]
		data := alpEncodeSlice(values, eng)
		require.Equalf(t, alpSchemeRD, data[0], "n=%d: expected RD scheme", n)

		want := alpDecodeAll(data, len(values), eng)
		got := make([]float64, len(values))
		ng := dec.DecodeAll(data, len(values), got)
		require.Equalf(t, len(values), ng, "n=%d: DecodeAll count", n)
		for i := range want {
			require.Equalf(t, math.Float64bits(want[i]), math.Float64bits(got[i]), "n=%d idx=%d", n, i)
		}
	}
}

// ---- reference implementations of the pre-optimization ALP-RD planner ----
//
// alpRDBuildDictRef and alpRDBestCutRef are verbatim copies of the shipped
// map+sort implementations of alpRDBuildDict/alpRDBestCut (the ones that
// produced the frozen TestNumericALP_GoldenBytes constants for the
// fullPrecision dataset). They pin the BEHAVIOR the map-free rewrite must
// preserve byte-for-byte: identical dictionary contents AND order, identical
// chosen rbw, identical estimated totalBits.
//
// The rewrite is byte-identical because the dictionary comparator
// (count DESC, then left ASC) is a TOTAL order with no ties — left values are
// unique map keys — so selecting the top-8 by that comparator yields the exact
// same 8 entries in the exact same order regardless of the order the distinct
// lefts are visited in. The differential tests below deliberately include
// count-tie inputs (multiple lefts sharing a count) to prove the left-ASC
// tie-break survives the switch from sort.Slice to insertion selection.

// alpRDBuildDictRef is the shipped map+sort dictionary builder.
func alpRDBuildDictRef(patterns []uint64, rbw int) (dict []uint64, codeOf map[uint64]int) {
	freq := make(map[uint64]int, len(patterns))
	for _, p := range patterns {
		freq[p>>uint(rbw)]++
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

// alpRDBestCutRef is the shipped map+sort best-cut search.
func alpRDBestCutRef(patterns []uint64) (rbw, totalBits int) {
	best := math.MaxInt
	n := len(patterns)
	freq := make(map[uint64]int, n)
	type lv struct {
		left  uint64
		count int
	}
	var lvs []lv
	var top [alpRDMaxDictSize]uint64
	for i := 1; i <= alpRDCutLimit; i++ {
		r := 64 - i
		clear(freq)
		for _, p := range patterns {
			freq[p>>r]++
		}
		lvs = lvs[:0]
		for l, c := range freq {
			lvs = append(lvs, lv{l, c})
		}
		sort.Slice(lvs, func(a, b int) bool {
			if lvs[a].count != lvs[b].count {
				return lvs[a].count > lvs[b].count
			}

			return lvs[a].left < lvs[b].left
		})
		nDict := min(alpRDMaxDictSize, len(lvs))
		for k := range nDict {
			top[k] = lvs[k].left
		}
		ex := 0
		for _, p := range patterns {
			l := p >> r
			found := false
			for k := range nDict {
				if top[k] == l {
					found = true

					break
				}
			}
			if !found {
				ex++
			}
		}
		codeBits := alpCodeBits(nDict)
		total := 8 + 8 + 8 + 32 + nDict*16 + n*codeBits + n*r + ex*(32+16)
		if total < best {
			best = total
			rbw, totalBits = r, total
		}
	}

	return rbw, totalBits
}

// ---- pattern-set generators (shared by the differential tests) ----

// alpRDRandPatterns returns n fully random 64-bit patterns.
func alpRDRandPatterns(rng *rand.Rand, n int) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		out[i] = rng.Uint64()
	}

	return out
}

// alpRDTemplatePatterns returns n patterns whose top 16 bits are drawn from a
// pool of nLefts distinct templates and whose low 48 bits are random. Repeating
// templates creates count ties (exact ties at cut width 16 / rbw 48) and lets
// the caller dial distinct-left cardinality independent of n.
func alpRDTemplatePatterns(rng *rand.Rand, n, nLefts int) []uint64 {
	tops := make([]uint64, nLefts)
	seen := make(map[uint64]bool, nLefts)
	for i := range tops {
		for {
			t := uint64(rng.Intn(1 << 16))
			if !seen[t] {
				seen[t] = true
				tops[i] = t

				break
			}
		}
	}
	out := make([]uint64, n)
	for i := range out {
		out[i] = (tops[rng.Intn(nLefts)] << 48) | (rng.Uint64() >> 16)
	}

	return out
}

// alpRDExactTiePatterns returns nLefts distinct 16-bit-top templates, each
// repeated exactly countEach times (low 48 bits random). At rbw 48 every left
// has the identical count — maximal tie stress for the left-ASC tie-break.
func alpRDExactTiePatterns(rng *rand.Rand, nLefts, countEach int) []uint64 {
	tops := make([]uint64, nLefts)
	seen := make(map[uint64]bool, nLefts)
	for i := range tops {
		for {
			t := uint64(rng.Intn(1 << 16))
			if !seen[t] {
				seen[t] = true
				tops[i] = t

				break
			}
		}
	}
	out := make([]uint64, 0, nLefts*countEach)
	for _, t := range tops {
		for range countEach {
			out = append(out, (t<<48)|(rng.Uint64()>>16))
		}
	}
	// Shuffle so first-seen order differs from value order — proves the
	// selection is insensitive to visitation order.
	rng.Shuffle(len(out), func(a, b int) { out[a], out[b] = out[b], out[a] })

	return out
}

// ---- known-vector sanity: pins the Ref builder against hand-computed dicts ----
//
// This runs GREEN against the shipped code before the map-free swap. It proves
// the Ref capture and my understanding of the (count DESC, left ASC) total
// order are correct, so the differential comparison below is grounded in an
// independent oracle rather than being circular.
func TestAlpRDBuildDictRef_KnownVectors(t *testing.T) {
	const rbw = 60                                       // left = p>>60, i.e. the top 4 bits (values 0..15)
	mk := func(left uint64) uint64 { return left << 60 } // low bits irrelevant to the dict

	tests := []struct {
		name     string
		patterns []uint64
		wantDict []uint64
	}{
		{
			// left 2 ×3, left 5 ×3 (tie, count 3), left 1 ×1.
			// count DESC: {2,5} then 1; left ASC breaks the 3-way tie: 2 before 5.
			name:     "count_tie_left_asc",
			patterns: []uint64{mk(5), mk(2), mk(5), mk(1), mk(2), mk(5), mk(2)},
			wantDict: []uint64{2, 5, 1},
		},
		{
			// single distinct left (nDict == 1 -> codeBits == 1 downstream).
			name:     "single_left",
			patterns: []uint64{mk(7), mk(7), mk(7), mk(7), mk(7)},
			wantDict: []uint64{7},
		},
		{
			// 9 distinct lefts (0..8), all count 1: overflow by one, keep the 8
			// smallest by the left-ASC tie-break, drop 8.
			name:     "overflow_drop_largest",
			patterns: []uint64{mk(8), mk(0), mk(7), mk(1), mk(6), mk(2), mk(5), mk(3), mk(4)},
			wantDict: []uint64{0, 1, 2, 3, 4, 5, 6, 7},
		},
		{
			// full-8 tie set displaced by a higher-count late entry: left 9 has
			// count 3, so it takes code 0 and the largest count-1 left (8) drops.
			name:     "high_count_displaces_full_dict",
			patterns: []uint64{mk(1), mk(2), mk(3), mk(4), mk(5), mk(6), mk(7), mk(8), mk(9), mk(9), mk(9)},
			wantDict: []uint64{9, 1, 2, 3, 4, 5, 6, 7},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dict, codeOf := alpRDBuildDictRef(tc.patterns, rbw)
			require.Equalf(t, tc.wantDict, dict, "%s: dict contents/order", tc.name)
			require.Lenf(t, codeOf, len(tc.wantDict), "%s: codeOf size", tc.name)
			for i, l := range tc.wantDict {
				require.Equalf(t, i, codeOf[l], "%s: codeOf[%d]", tc.name, l)
			}
		})
	}
}

// alpRDBuildDictNew drives the map-free alpRDBuildDict with fresh scratch and
// returns the dict slice + the reconstructed codeOf + the reported coverage, so
// it can be compared against the map-returning Ref directly.
func alpRDBuildDictNew(patterns []uint64, rbw int) (dict []uint64, codeOf map[uint64]int, covered int) {
	var arr [alpRDMaxDictSize]uint64
	var lefts []uint64
	var cnts []int32
	nDict, cov := alpRDBuildDict(patterns, rbw, &lefts, &cnts, &arr)
	dict = append([]uint64(nil), arr[:nDict]...)
	codeOf = make(map[uint64]int, nDict)
	for i, l := range dict {
		codeOf[l] = i
	}

	return dict, codeOf, cov
}

// alpRDCoveredRef independently counts how many patterns' left parts fall in the
// reference dictionary — the oracle for alpRDBuildDict's covered return (and, via
// len(patterns)-covered, for the exception total the size estimate depends on).
func alpRDCoveredRef(patterns []uint64, rbw int, codeOf map[uint64]int) int {
	covered := 0
	for _, p := range patterns {
		if _, ok := codeOf[p>>uint(rbw)]; ok {
			covered++
		}
	}

	return covered
}

// TestAlpRDBuildDict_Differential proves the map-free dictionary builder is
// byte-identical to the shipped map+sort builder across seeded random pattern
// sets — every cut width, varied distinct-left cardinality (1, 2, ~8, 9, ~20,
// 200+), and deliberate count ties. Dict contents AND order must match, and the
// reconstructed codeOf must agree, or a downstream encoded byte would differ.
func TestAlpRDBuildDict_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(202))
	// A spread of column sizes that (across the 16 cut widths) exercises the
	// full cardinality ladder, including >8 distinct (overflow) and 200+.
	sizes := []int{1, 2, 3, 8, 9, 16, 20, 50, 200, 400}
	kinds := 4
	for _, n := range sizes {
		for range 300 {
			var patterns []uint64
			switch rng.Intn(kinds) {
			case 0:
				patterns = alpRDRandPatterns(rng, n)
			case 1:
				patterns = alpRDTemplatePatterns(rng, n, 1+rng.Intn(12))
			case 2:
				// Force exact count ties; total size ~= n.
				each := 1 + rng.Intn(6)
				patterns = alpRDExactTiePatterns(rng, max(1, n/each), each)
			default:
				// Low-cardinality: few templates so many cut widths yield ≤8
				// distinct, exercising the nDict < 8 path.
				patterns = alpRDTemplatePatterns(rng, n, 1+rng.Intn(3))
			}
			for rbw := 64 - alpRDCutLimit; rbw <= 63; rbw++ {
				gotDict, gotCodeOf, gotCovered := alpRDBuildDictNew(patterns, rbw)
				wantDict, wantCodeOf := alpRDBuildDictRef(patterns, rbw)
				require.Equalf(t, wantDict, gotDict, "buildDict dict n=%d rbw=%d", n, rbw)
				require.Equalf(t, wantCodeOf, gotCodeOf, "buildDict codeOf n=%d rbw=%d", n, rbw)
				require.Equalf(t, alpRDCoveredRef(patterns, rbw, wantCodeOf), gotCovered,
					"buildDict covered n=%d rbw=%d", n, rbw)
			}
		}
	}
}

// TestAlpRDBestCut_Differential proves the map-free best-cut search returns an
// identical (rbw, totalBits) to the shipped map+sort search. Inputs are capped
// at 60 patterns — alpRDBestCut only ever runs on the strided sample, which is
// bounded at 63 entries (TestAlpRDSampleBound); its fixed [64] scratch arrays
// must not be fed more. Ties and mixed cardinalities are included.
func TestAlpRDBestCut_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(303))
	for range 20000 {
		n := 1 + rng.Intn(60)
		var patterns []uint64
		switch rng.Intn(4) {
		case 0:
			patterns = alpRDRandPatterns(rng, n)
		case 1:
			patterns = alpRDTemplatePatterns(rng, n, 1+rng.Intn(10))
		case 2:
			each := 1 + rng.Intn(5)
			nl := max(1, min(n, 12))
			patterns = alpRDExactTiePatterns(rng, nl, each)
			if len(patterns) > 60 {
				patterns = patterns[:60]
			}
		default:
			patterns = alpRDTemplatePatterns(rng, n, 1+rng.Intn(3))
		}
		gotRbw, gotBits := alpRDBestCut(patterns)
		wantRbw, wantBits := alpRDBestCutRef(patterns)
		require.Equalf(t, wantRbw, gotRbw, "bestCut rbw (n=%d)", len(patterns))
		require.Equalf(t, wantBits, gotBits, "bestCut totalBits (n=%d)", len(patterns))
	}
}

// TestAlpRDSampleBound pins the invariant the fixed [64] sample/scratch arrays
// depend on: the strided sample alpRDPlan/alpRDBestCut operate on never exceeds
// 63 entries (so ≤63 distinct lefts). It checks every n in 1..200 plus a spread
// of large n. If alpSampleStride ever changes such that the sample can reach 64,
// this fails before any [64] array can overflow in production.
func TestAlpRDSampleBound(t *testing.T) {
	check := func(n int) {
		stride := alpSampleStride(n)
		count := 0
		for i := 0; i < n; i += stride {
			count++
		}
		require.LessOrEqualf(t, count, 64, "sample count for n=%d (stride=%d) must fit [64]", n, stride)
		// Tighter documented bound: at most 63.
		require.LessOrEqualf(t, count, 63, "sample count for n=%d (stride=%d) must be ≤63", n, stride)
	}
	for n := 1; n <= 200; n++ {
		check(n)
	}
	for _, n := range []int{201, 255, 256, 1000, 1023, 1024, 4095, 65535, 65536, 1 << 20} {
		check(n)
	}
}

// TestAlpReadBitsFast_Differential fuzzes the unaligned word-at-a-time reader
// against the naive bit-by-bit reference across every width (0..64), random bit
// offsets, and buffer tails (so the <8-byte fallback and the width-near-64 fixup
// are both exercised). The two must agree bit-for-bit.
func TestAlpReadBitsFast_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for range 200000 {
		width := rng.Intn(65) // 0..64
		// Buffer large enough to hold the read, plus a random short tail sometimes.
		nbytes := (64+width)/8 + 1 + rng.Intn(6)
		src := make([]byte, nbytes)
		for i := range src {
			src[i] = byte(rng.Intn(256))
		}
		// bitpos anywhere the width-bit field still fits in the buffer.
		maxBit := nbytes*8 - width
		if maxBit <= 0 {
			continue
		}
		bitpos := rng.Intn(maxBit)

		mask := ^uint64(0)
		if width < 64 {
			mask = (uint64(1) << uint(width)) - 1
		}
		got := alpReadBitsFast(src, bitpos, width, mask)
		want := alpReadBitsAt(src, bitpos, width)
		if got != want {
			t.Fatalf("mismatch: width=%d bitpos=%d got=%#x want=%#x", width, bitpos, got, want)
		}
	}
}
func quantize(values []float64, d int) []float64 {
	out := make([]float64, len(values))
	scale := math.Pow(10, float64(d))
	for i, v := range values {
		out[i] = math.Round(v*scale) / scale
	}

	return out
}

func generateSmallJitterMetric(count int, base, driftRate, noiseRate float64) []float64 {
	values := make([]float64, count)
	for i := range values {
		drift := math.Sin(float64(i)*0.07) * base * driftRate
		noise := math.Cos(float64(i)*0.23) * base * noiseRate
		values[i] = base + drift + noise
	}

	return values
}

func generateDriftingMetric(count int, base, slope, noiseRate float64) []float64 {
	values := make([]float64, count)
	for i := range values {
		trend := float64(i) * slope
		noise := math.Sin(float64(i)*0.13) * base * noiseRate
		values[i] = base + trend + noise
	}

	return values
}

func generateIntegerLikeMetric(count int, base, rangeVal, driftRate float64) []float64 {
	values := make([]float64, count)
	for i := range values {
		drift := float64(i) * driftRate
		variation := math.Round(math.Sin(float64(i)*0.19) * rangeVal)
		values[i] = math.Round(base + drift + variation)
	}

	return values
}

// randomWalk reproduces mebo's measurev2 generator shape: ±jitterPct random walk,
// producing full-precision (non-decimal) doubles — ALP's worst case.
func randomWalk(n int, base, jitterPct float64, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	cur := base
	for i := range out {
		cur += cur * (rng.Float64()*2 - 1) * jitterPct / 100
		out[i] = cur
	}

	return out
}

type pocFloatDataset struct {
	name   string
	values []float64
}

func pocFloatDatasets() []pocFloatDataset {
	ds := []pocFloatDataset{
		// --- full-precision realistic (sin/cos noise -> non-decimal doubles) ---
		{"cpu_util_fp", generateSmallJitterMetric(200, 42.0, 0.003, 0.001)},
		{"mem_drift_fp", generateDriftingMetric(500, 67.2, 0.0006, 0.002)},
		{"temp_fp", generateSmallJitterMetric(200, 23.4, 0.0005, 0.0002)},
		{"battery_fp", generateSmallJitterMetric(200, 3.72, 0.0003, 0.0001)},
		// --- integer-like (already rounded) ---
		{"disk_iops_int", generateIntegerLikeMetric(500, 4200, 50, 0.1)},
		// --- random-walk (mebo measurev2 default shape) ---
		{"randwalk_fp", randomWalk(200, 100, 0.5, 42)},
		// --- real JSON sample values (clean 1-decimal sensor data) ---
		{"real_cpu", []float64{45.2, 46.1, 45.8, 47.3, 46.5, 48.2, 47.9, 46.7, 45.5, 46.3}},
		{"real_mem", []float64{2048.5, 2050.1, 2047.8, 2051.2, 2049.6, 2052.3, 2050.8, 2048.1, 2046.9, 2049.4}},
		{"real_latency", []float64{10.5, 11.2, 10.8, 12.1, 11.5}},
	}

	// quantized variants: real sensors usually report 1-2 decimals
	for _, base := range []pocFloatDataset{
		{"cpu_util", generateSmallJitterMetric(200, 42.0, 0.003, 0.001)},
		{"temp", generateSmallJitterMetric(200, 23.4, 0.0005, 0.0002)},
		{"net_tput", generateSmallJitterMetric(300, 950.0, 0.005, 0.002)},
		{"battery", generateSmallJitterMetric(200, 3.72, 0.0003, 0.0001)},
	} {
		ds = append(ds, pocFloatDataset{base.name + "_q1", quantize(base.values, 1)})
		ds = append(ds, pocFloatDataset{base.name + "_q2", quantize(base.values, 2)})
	}

	return ds
}
