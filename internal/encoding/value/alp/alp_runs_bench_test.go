package alp

import (
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/encoding/value/chimp"
)

// Runs-layout speed checks at the production shape, 100 columns × 150 points.
// Each benchmark compares a plain sub-benchmark (NewNumericALPEncoder)
// with a runs one (NewNumericALPRLEEncoder) on the same columns.
// Decode uses the hold-50% shape, where every column takes the runs layout;
// encode uses run-free 2-decimal gauges, where every column stays plain.
// DecodeAll and At also run Chimp on the same columns, the production codec the DecodeAll gate compares against.
// Single-binary results only show direction; the gates are decided layout-averaged.

const (
	alpRunsBenchCols = 100
	alpRunsBenchPts  = 150
)

func alpRunsBenchHoldCols() [][]float64 {
	cols := make([][]float64, alpRunsBenchCols)
	for i := range cols {
		cols[i] = alpRunsHold(alpRunsBenchPts, 0.5, int64(i))
	}

	return cols
}

func alpRunsBenchEncodeCols(cols [][]float64, eng endian.EndianEngine, runs bool) [][]byte {
	out := make([][]byte, len(cols))
	for i, c := range cols {
		out[i] = alpRunsEncode(c, eng, runs)
	}

	return out
}

// alpRunsBenchChimpCols encodes cols with the Chimp codec.
func alpRunsBenchChimpCols(cols [][]float64) [][]byte {
	out := make([][]byte, len(cols))
	for i, c := range cols {
		enc := chimp.NewNumericChimpEncoder()
		enc.WriteSlice(c)
		out[i] = append([]byte(nil), enc.Bytes()...)
		enc.Finish()
	}

	return out
}

func alpRunsBenchModes(b *testing.B, f func(b *testing.B, runs bool)) {
	b.Helper()
	b.Run("plain", func(b *testing.B) { f(b, false) })
	b.Run("runs", func(b *testing.B) { f(b, true) })
}

func BenchmarkALPRuns_DecodeAll(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	cols := alpRunsBenchHoldCols()
	alpRunsBenchModes(b, func(b *testing.B, runs bool) {
		encoded := alpRunsBenchEncodeCols(cols, eng, runs)
		dec := NewNumericALPDecoder(eng)
		dst := make([]float64, alpRunsBenchPts)
		b.ReportAllocs()
		for b.Loop() {
			for _, col := range encoded {
				dec.DecodeAll(col, alpRunsBenchPts, dst)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*alpRunsBenchCols*alpRunsBenchPts), "ns/pt")
	})
	b.Run("chimp", func(b *testing.B) {
		encoded := alpRunsBenchChimpCols(cols)
		dec := chimp.NewNumericChimpDecoder()
		dst := make([]float64, alpRunsBenchPts)
		b.ReportAllocs()
		for b.Loop() {
			for _, col := range encoded {
				dec.DecodeAll(col, alpRunsBenchPts, dst)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*alpRunsBenchCols*alpRunsBenchPts), "ns/pt")
	})
}

func BenchmarkALPRuns_All(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	cols := alpRunsBenchHoldCols()
	alpRunsBenchModes(b, func(b *testing.B, runs bool) {
		encoded := alpRunsBenchEncodeCols(cols, eng, runs)
		dec := NewNumericALPDecoder(eng)
		var sink float64
		b.ReportAllocs()
		for b.Loop() {
			for _, col := range encoded {
				for v := range dec.All(col, alpRunsBenchPts) {
					sink += v
				}
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*alpRunsBenchCols*alpRunsBenchPts), "ns/pt")
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})
}

func BenchmarkALPRuns_At(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	cols := alpRunsBenchHoldCols()
	alpRunsBenchModes(b, func(b *testing.B, runs bool) {
		encoded := alpRunsBenchEncodeCols(cols, eng, runs)
		dec := NewNumericALPDecoder(eng)
		var sink float64
		b.ReportAllocs()
		for b.Loop() {
			for i, col := range encoded {
				v, _ := dec.At(col, (i*37)%alpRunsBenchPts, alpRunsBenchPts)
				sink += v
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*alpRunsBenchCols), "ns/lookup")
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})
	b.Run("chimp", func(b *testing.B) {
		encoded := alpRunsBenchChimpCols(cols)
		dec := chimp.NewNumericChimpDecoder()
		var sink float64
		b.ReportAllocs()
		for b.Loop() {
			for i, col := range encoded {
				v, _ := dec.At(col, (i*37)%alpRunsBenchPts, alpRunsBenchPts)
				sink += v
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*alpRunsBenchCols), "ns/lookup")
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})
}

func BenchmarkALPRuns_EncodeRunFree(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	cols := genALPColumns(alpRunsBenchCols, alpRunsBenchPts, 2, 42)
	alpRunsBenchModes(b, func(b *testing.B, runs bool) {
		newEnc := NewNumericALPEncoder
		if runs {
			newEnc = NewNumericALPRLEEncoder
		}
		b.ReportAllocs()
		for b.Loop() {
			// One encoder per batch: Reset keeps the encoded bytes, so a shared encoder would grow without bound.
			enc := newEnc(eng)
			for _, c := range cols {
				enc.WriteSlice(c)
				_ = enc.Bytes()
				enc.Reset()
			}
			enc.Finish()
		}
	})
}
