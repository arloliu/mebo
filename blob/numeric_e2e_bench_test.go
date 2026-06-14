package blob

// End-to-end benchmarks mirroring the tests/measurev2 reference workload
// (200 metrics × 200 points, ±0.5% value random walk, ±0.1% timestamp jitter,
// per-point AddDataPoint, no compression). These exist so codec or blob-layer
// changes can be profiled in-repo with -cpuprofile without the external harness.

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
)

type e2eBenchData struct {
	metricIDs  []uint64
	timestamps []int64
	values     []float64
	ppm        int
	start      time.Time
}

//nolint:unparam // dimensions kept parametric for ad-hoc profiling runs
func genE2EBenchData(numMetrics, ppm int) *e2eBenchData {
	rng := rand.New(rand.NewSource(42))
	start := time.Unix(1700000000, 0).UTC()

	d := &e2eBenchData{
		metricIDs:  make([]uint64, numMetrics),
		timestamps: make([]int64, 0, numMetrics*ppm),
		values:     make([]float64, 0, numMetrics*ppm),
		ppm:        ppm,
		start:      start,
	}

	intervalUs := int64(time.Second / time.Microsecond)
	for i := range numMetrics {
		d.metricIDs[i] = rng.Uint64() | 1
		ts := start.UnixMicro()
		val := 50.0 + rng.Float64()*50.0
		for range ppm {
			jitter := int64(float64(intervalUs) * 0.001 * (rng.Float64()*2 - 1))
			ts += intervalUs + jitter
			val *= 1 + 0.005*(rng.Float64()*2-1)
			d.timestamps = append(d.timestamps, ts)
			d.values = append(d.values, val)
		}
	}

	return d
}

func e2eBenchEncode(b *testing.B, d *e2eBenchData, tsEnc, valEnc format.EncodingType) []byte {
	b.Helper()

	encoder, err := NewNumericEncoder(d.start,
		WithTimestampEncoding(tsEnc),
		WithTimestampCompression(format.CompressionNone),
		WithValueEncoding(valEnc),
		WithValueCompression(format.CompressionNone),
	)
	if err != nil {
		b.Fatal(err)
	}

	for i, id := range d.metricIDs {
		if err = encoder.StartMetricID(id, d.ppm); err != nil {
			b.Fatal(err)
		}
		base := i * d.ppm
		for j := range d.ppm {
			if err = encoder.AddDataPoint(d.timestamps[base+j], d.values[base+j], ""); err != nil {
				b.Fatal(err)
			}
		}
		if err = encoder.EndMetric(); err != nil {
			b.Fatal(err)
		}
	}

	blobBytes, err := encoder.Finish()
	if err != nil {
		b.Fatal(err)
	}

	return blobBytes
}

func benchmarkE2EEncode(b *testing.B, tsEnc, valEnc format.EncodingType) {
	b.Helper()

	d := genE2EBenchData(200, 200)
	b.ReportAllocs()
	for b.Loop() {
		if blobBytes := e2eBenchEncode(b, d, tsEnc, valEnc); len(blobBytes) == 0 {
			b.Fatal("empty blob")
		}
	}
}

func BenchmarkE2EEncode_DeltaGorilla(b *testing.B) {
	benchmarkE2EEncode(b, format.TypeDelta, format.TypeGorilla)
}

func BenchmarkE2EEncode_DeltaRaw(b *testing.B) {
	benchmarkE2EEncode(b, format.TypeDelta, format.TypeRaw)
}

func BenchmarkE2EEncode_DeltaPackedGorilla(b *testing.B) {
	benchmarkE2EEncode(b, format.TypeDeltaPacked, format.TypeGorilla)
}

// BenchmarkE2EEncodeInto_DeltaGorilla mirrors BenchmarkE2EEncode_DeltaGorilla
// but reuses a caller-provided buffer via FinishInto, eliminating the final
// blob allocation (the dominant allocation of the encode path).
func BenchmarkE2EEncodeInto_DeltaGorilla(b *testing.B) {
	d := genE2EBenchData(200, 200)
	b.ReportAllocs()

	var buf []byte
	for b.Loop() {
		encoder, err := NewNumericEncoder(d.start,
			WithTimestampEncoding(format.TypeDelta),
			WithTimestampCompression(format.CompressionNone),
			WithValueEncoding(format.TypeGorilla),
			WithValueCompression(format.CompressionNone),
		)
		if err != nil {
			b.Fatal(err)
		}

		for i, id := range d.metricIDs {
			if err = encoder.StartMetricID(id, d.ppm); err != nil {
				b.Fatal(err)
			}
			base := i * d.ppm
			for j := range d.ppm {
				if err = encoder.AddDataPoint(d.timestamps[base+j], d.values[base+j], ""); err != nil {
					b.Fatal(err)
				}
			}
			if err = encoder.EndMetric(); err != nil {
				b.Fatal(err)
			}
		}

		buf, err = encoder.FinishInto(buf[:0])
		if err != nil {
			b.Fatal(err)
		}
		if len(buf) == 0 {
			b.Fatal("empty blob")
		}
	}
}

// BenchmarkE2EForEach_DeltaGorilla mirrors BenchmarkE2EIterate_DeltaGorilla
// but consumes data points through the callback-style ForEach instead of the
// All iterator.
func BenchmarkE2EForEach_DeltaGorilla(b *testing.B) {
	d := genE2EBenchData(200, 200)
	blobBytes := e2eBenchEncode(b, d, format.TypeDelta, format.TypeGorilla)

	decoder, err := NewNumericDecoder(blobBytes)
	if err != nil {
		b.Fatal(err)
	}
	nb, err := decoder.Decode()
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var sink int64
		var vsink float64
		fn := func(_ int, dp NumericDataPoint) bool {
			sink += dp.Ts
			vsink += dp.Val

			return true
		}
		for _, id := range d.metricIDs {
			if !nb.ForEach(id, fn) {
				b.Fatal("metric not found")
			}
		}
		if sink == 0 && vsink == 0 {
			b.Fatal("no data")
		}
	}
}

func BenchmarkE2EIterate_DeltaGorilla(b *testing.B) {
	d := genE2EBenchData(200, 200)
	blobBytes := e2eBenchEncode(b, d, format.TypeDelta, format.TypeGorilla)

	decoder, err := NewNumericDecoder(blobBytes)
	if err != nil {
		b.Fatal(err)
	}
	nb, err := decoder.Decode()
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var sink int64
		var vsink float64
		for _, id := range d.metricIDs {
			for _, dp := range nb.All(id) {
				sink += dp.Ts
				vsink += dp.Val
			}
		}
		if sink == 0 && vsink == 0 {
			b.Fatal("no data")
		}
	}
}

// TestNumericBlob_ALP_BeatsChimpOnDecimals builds an ALP blob and a Chimp blob
// over the same 2-dp decimal gauge series (n=1000 points, single metric) and
// asserts that ALP's finished byte length is strictly smaller than Chimp's.
// ALP's integer-pair encoding is tailored for quantized decimals and should
// compress them far more efficiently than Chimp's XOR bit-packing.
func TestNumericBlob_ALP_BeatsChimpOnDecimals(t *testing.T) {
	const n = 1000
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(42))
	intervalUs := int64(15 * time.Second / time.Microsecond)

	timestamps := make([]int64, n)
	values := make([]float64, n)
	ts := startTime.UnixMicro()
	val := 50.0 + rng.Float64()*50.0
	for i := range n {
		ts += intervalUs
		timestamps[i] = ts
		// Small-step walk quantized to 2 decimal places.
		val += (rng.Float64()*2 - 1) * 0.5
		values[i] = math.Round(val*100) / 100
	}

	buildBlob := func(valEnc format.EncodingType) []byte {
		enc, err := NewNumericEncoder(startTime,
			WithTimestampEncoding(format.TypeDelta),
			WithTimestampCompression(format.CompressionNone),
			WithValueEncoding(valEnc),
			WithValueCompression(format.CompressionNone),
		)
		require.NoError(t, err)
		require.NoError(t, enc.StartMetricID(1, n))
		for i := range n {
			require.NoError(t, enc.AddDataPoint(timestamps[i], values[i], ""))
		}
		require.NoError(t, enc.EndMetric())
		data, err := enc.Finish()
		require.NoError(t, err)

		return data
	}

	alpData := buildBlob(format.TypeALP)
	chimpData := buildBlob(format.TypeChimp)

	require.Lessf(t, len(alpData), len(chimpData),
		"ALP (%d bytes) must beat Chimp (%d bytes) on 2-dp decimal data",
		len(alpData), len(chimpData))
}
