package blob

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
)

// ALP-RLE speed gates at the production shape (docs/specs/alp-rle-design.md, "Speed gates"):
// 100 metrics × 150 points, little-endian V2 with shared DeltaPacked timestamps, no compression, no tags.
// Decode-side benchmarks use the calibrated hold-50% shape, where every ALP-RLE column takes the runs layout;
// the run-free encode benchmark uses a 2-decimal gauge with no forced holds,
// whose few rounding repeats never pass the encoder's pruning rule, so every column stays plain.
// Each benchmark runs Chimp (production today), ALP and ALP-RLE on the same data.
// Single-binary results only show direction; the gates are decided layout-averaged.

const (
	alpRLEGateMetrics = 100
	alpRLEGatePoints  = 150
)

var alpRLEGateEncodings = []struct {
	name string
	enc  format.EncodingType
}{
	{"chimp", format.TypeChimp},
	{"alp", format.TypeALP},
	{"alprle", format.TypeALPRLE},
}

// alpRLEGateColumns returns 2-decimal gauges with ±0.5% steps where each point repeats the previous one with probability hold.
func alpRLEGateColumns(hold float64, seed int64) [][]float64 {
	rng := rand.New(rand.NewSource(seed))
	cols := make([][]float64, alpRLEGateMetrics)
	for m := range cols {
		col := make([]float64, alpRLEGatePoints)
		cur := 100.0 + float64(m)*10
		for i := range col {
			if i > 0 && rng.Float64() < hold {
				col[i] = col[i-1]
				continue
			}
			cur += cur * (rng.Float64()*2 - 1) * 0.005
			col[i] = math.Round(cur*100) / 100
		}
		cols[m] = col
	}

	return cols
}

func alpRLEGateEncode(tb testing.TB, cols [][]float64, valEnc format.EncodingType) []byte {
	tb.Helper()
	start := time.Unix(1700000000, 0)
	enc, err := NewNumericEncoder(start,
		WithLittleEndian(),
		WithSharedTimestamps(),
		WithTimestampEncoding(format.TypeDeltaPacked),
		WithValueEncoding(valEnc),
		WithTimestampCompression(format.CompressionNone),
		WithValueCompression(format.CompressionNone))
	require.NoError(tb, err)
	ts := make([]int64, alpRLEGatePoints)
	for i := range ts {
		ts[i] = start.Add(time.Duration(i) * 15 * time.Second).UnixMicro()
	}
	for m, col := range cols {
		require.NoError(tb, enc.StartMetricID(uint64(m+1), len(col)))
		require.NoError(tb, enc.AddDataPoints(ts, col, nil))
		require.NoError(tb, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(tb, err)

	return data
}

func alpRLEGateBlob(tb testing.TB, cols [][]float64, valEnc format.EncodingType) NumericBlob {
	tb.Helper()
	dec, err := NewNumericDecoder(alpRLEGateEncode(tb, cols, valEnc))
	require.NoError(tb, err)
	blob, err := dec.Decode()
	require.NoError(tb, err)

	return blob
}

// BenchmarkALPRLEGate_ValueAt measures one NumericBlob.ValueAt lookup per metric, spread over the whole column.
func BenchmarkALPRLEGate_ValueAt(b *testing.B) {
	cols := alpRLEGateColumns(0.5, 1)
	for _, e := range alpRLEGateEncodings {
		b.Run(e.name, func(b *testing.B) {
			blob := alpRLEGateBlob(b, cols, e.enc)
			var sink float64
			b.ReportAllocs()
			for b.Loop() {
				for m := range alpRLEGateMetrics {
					v, _ := blob.ValueAt(uint64(m+1), (m*37)%alpRLEGatePoints)
					sink += v
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*alpRLEGateMetrics), "ns/lookup")
			if sink == -1 {
				b.Fatal("unreachable")
			}
		})
	}
}

// BenchmarkALPRLEGate_Materialize measures materializing the whole blob, reported per point.
func BenchmarkALPRLEGate_Materialize(b *testing.B) {
	cols := alpRLEGateColumns(0.5, 1)
	for _, e := range alpRLEGateEncodings {
		b.Run(e.name, func(b *testing.B) {
			blob := alpRLEGateBlob(b, cols, e.enc)
			b.ReportAllocs()
			for b.Loop() {
				_ = blob.Materialize()
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*alpRLEGateMetrics*alpRLEGatePoints), "ns/pt")
		})
	}
}

// BenchmarkALPRLEGate_ForEachValues measures ForEachValues over every metric, reported per point.
func BenchmarkALPRLEGate_ForEachValues(b *testing.B) {
	cols := alpRLEGateColumns(0.5, 1)
	for _, e := range alpRLEGateEncodings {
		b.Run(e.name, func(b *testing.B) {
			blob := alpRLEGateBlob(b, cols, e.enc)
			var sink float64
			yield := func(_ int, v float64) bool {
				sink += v

				return true
			}
			b.ReportAllocs()
			for b.Loop() {
				for m := range alpRLEGateMetrics {
					blob.ForEachValues(uint64(m+1), yield)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*alpRLEGateMetrics*alpRLEGatePoints), "ns/pt")
			if sink == -1 {
				b.Fatal("unreachable")
			}
		})
	}
}

// BenchmarkALPRLEGate_EncodeRunFree measures encoding the whole blob from gauges without forced holds.
func BenchmarkALPRLEGate_EncodeRunFree(b *testing.B) {
	cols := alpRLEGateColumns(0, 2)
	for _, e := range alpRLEGateEncodings {
		b.Run(e.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = alpRLEGateEncode(b, cols, e.enc)
			}
		})
	}
}

// BenchmarkALPRLEGate_EncodeHold50 measures encoding the whole blob from the hold-50% gauges.
func BenchmarkALPRLEGate_EncodeHold50(b *testing.B) {
	cols := alpRLEGateColumns(0.5, 1)
	for _, e := range alpRLEGateEncodings {
		b.Run(e.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = alpRLEGateEncode(b, cols, e.enc)
			}
		})
	}
}
