package encoding

import (
	"math"
	"math/rand"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

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

// TestNumericALP_Ratio proves the production codec picks the right scheme: ALP
// beats Chimp on decimal data and stays competitive on full-precision data.
func TestNumericALP_Ratio(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	schemeName := map[byte]string{0: "main", 1: "rd", 2: "raw"}

	t.Logf("%-18s %6s | %9s %9s %9s | %s", "dataset", "n", "gorilla", "chimp", "ALP", "scheme")
	t.Logf("%s", "--------------------------------------------------------------------------")
	for _, d := range pocFloatDatasets() {
		n := len(d.values)
		g := float64(gorillaSize(d.values)) / float64(n)
		c := float64(chimpSize(d.values)) / float64(n)
		data := alpEncodeSlice(d.values, eng)
		a := float64(len(data)) / float64(n)
		t.Logf("%-18s %6d | %8.3fB %8.3fB %8.3fB | %s", d.name, n, g, c, a, schemeName[data[0]])

		// Decimal datasets: ALP must beat Chimp. Full-precision: within 15% of Chimp.
		switch d.name {
		case "cpu_util_q2", "net_tput_q1", "real_cpu", "real_mem":
			require.Lessf(t, a, c, "%s: ALP (%.3f) should beat Chimp (%.3f)", d.name, a, c)
		case "randwalk_fp", "battery_fp", "cpu_util_fp":
			require.LessOrEqualf(t, a, c*1.15, "%s: ALP (%.3f) should be within 15%% of Chimp (%.3f)", d.name, a, c)
		default:
			// other datasets: logged only, no hard assertion
		}
	}
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
	// The four golden datasets (numeric_alp_golden_test.go): decimal (main,
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
// numeric_alp.go. This test pins that every one of those three methods
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

// BenchmarkALPAt measures single-index random access on a 10k-point 2dp
// column (ALP main, width >= 7): an O(1) windowed bit read (alpReadBitsFast)
// plus an O(log k) binary search over the exception sidecar (k = exceptions
// in the column). See At/atMain in numeric_alp.go for the current code path.
func BenchmarkALPAt(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	vals := genALPColumns(1, 10000, 2, 123)[0]
	data := alpEncodeSlice(vals, eng)
	dec := NewNumericALPDecoder(eng)

	rng := rand.New(rand.NewSource(1))
	idx := make([]int, 4096)
	for i := range idx {
		idx[i] = rng.Intn(len(vals))
	}

	b.ReportAllocs()
	var s float64
	var ok bool
	for i := 0; b.Loop(); i++ {
		v, o := dec.At(data, idx[i%len(idx)], len(vals))
		s += v
		ok = o
	}
	_ = s
	_ = ok
}

func BenchmarkALPVsChimp(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	vals := quantize(generateSmallJitterMetric(200, 42.0, 0.003, 0.001), 2) // decimal

	b.Run("ALP_Encode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			enc := NewNumericALPEncoder(eng)
			enc.WriteSlice(vals)
			_ = enc.Bytes()
			enc.Finish()
		}
	})
	b.Run("Chimp_Encode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			enc := NewNumericChimpEncoder()
			enc.WriteSlice(vals)
			_ = enc.Bytes()
			enc.Finish()
		}
	})

	alpData := alpEncodeSlice(vals, eng)
	chimpEnc := NewNumericChimpEncoder()
	chimpEnc.WriteSlice(vals)
	chimpData := append([]byte(nil), chimpEnc.Bytes()...)
	chimpEnc.Finish()

	b.Run("ALP_Decode", func(b *testing.B) {
		dec := NewNumericALPDecoder(eng)
		b.ReportAllocs()
		var s float64
		for b.Loop() {
			for v := range dec.All(alpData, len(vals)) {
				s += v
			}
		}
		_ = s
	})
	b.Run("Chimp_Decode", func(b *testing.B) {
		dec := NewNumericChimpDecoder()
		b.ReportAllocs()
		var s float64
		for b.Loop() {
			for v := range dec.All(chimpData, len(vals)) {
				s += v
			}
		}
		_ = s
	})
}
