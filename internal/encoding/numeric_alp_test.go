package encoding

import (
	"math"
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

func TestNumericALP_DecodeAll_MatchesAll(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	for _, d := range pocFloatDatasets() {
		data := alpEncodeSlice(d.values, eng)
		dec := NewNumericALPDecoder(eng)
		dst := make([]float64, len(d.values))
		n := dec.DecodeAll(data, len(d.values), dst)
		require.Equalf(t, len(d.values), n, "%s: DecodeAll count", d.name)
		require.Equalf(t, d.values, dst, "%s: DecodeAll must match input", d.name)
	}
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
