package main

import (
	"testing"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
)

// encodeBlob encodes test data with the given combo and returns raw blob bytes.
// This is the shared encoding logic used by both benchmark and size measurement functions.
func encodeBlob(combo EncodingCombo, data *TestData) ([]byte, error) {
	encoder, err := blob.NewNumericEncoder(
		data.StartTime,
		blob.WithTimestampEncoding(combo.TSEncoding),
		blob.WithTimestampCompression(format.CompressionNone),
		blob.WithValueEncoding(combo.ValEncoding),
		blob.WithValueCompression(format.CompressionNone),
	)
	if err != nil {
		return nil, err
	}

	numMetrics := len(data.MetricIDs)
	ppm := data.Config.PointsPerMetric

	for i := range numMetrics {
		metricID := data.MetricIDs[i]
		if err = encoder.StartMetricID(metricID, ppm); err != nil {
			return nil, err
		}

		for j := range ppm {
			idx := i*ppm + j
			if err = encoder.AddDataPoint(data.Timestamps[idx], data.Values[idx], ""); err != nil {
				return nil, err
			}
		}

		if err = encoder.EndMetric(); err != nil {
			return nil, err
		}
	}

	return encoder.Finish()
}

// decodeBlob creates a decoder and decodes the blob data.
func decodeBlob(blobData []byte) (blob.NumericBlob, error) {
	decoder, err := blob.NewNumericDecoder(blobData)
	if err != nil {
		return blob.NumericBlob{}, err
	}

	return decoder.Decode()
}

// measureEncodedSize encodes data with the given combo and returns the blob size.
func measureEncodedSize(combo EncodingCombo, data *TestData) (int, error) {
	blobData, err := encodeBlob(combo, data)
	if err != nil {
		return 0, err
	}

	return len(blobData), nil
}

// benchEncode runs a Go benchmark measuring encode speed and memory.
func benchEncode(combo EncodingCombo, data *TestData) BenchMetrics {
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			blobData, err := encodeBlob(combo, data)
			if err != nil {
				b.Fatal(err)
			}

			// Prevent compiler from optimizing away
			if len(blobData) == 0 {
				b.Fatal("empty blob")
			}
		}
	})

	return toBenchMetrics(result)
}

// benchDecode runs a Go benchmark measuring decode speed and memory.
// It pre-encodes the data, then benchmarks only the decode path.
func benchDecode(combo EncodingCombo, data *TestData) (BenchMetrics, error) {
	blobData, err := encodeBlob(combo, data)
	if err != nil {
		return BenchMetrics{}, err
	}

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			numericBlob, err := decodeBlob(blobData)
			if err != nil {
				b.Fatal(err)
			}

			if numericBlob.MetricCount() != len(data.MetricIDs) {
				b.Fatalf("metric count mismatch: got %d, want %d", numericBlob.MetricCount(), len(data.MetricIDs))
			}
		}
	})

	return toBenchMetrics(result), nil
}

// benchIterSeq runs a Go benchmark measuring sequential iteration speed and memory.
// It pre-encodes and pre-decodes, then benchmarks only the iteration.
func benchIterSeq(combo EncodingCombo, data *TestData) (BenchMetrics, error) {
	blobData, err := encodeBlob(combo, data)
	if err != nil {
		return BenchMetrics{}, err
	}

	numericBlob, err := decodeBlob(blobData)
	if err != nil {
		return BenchMetrics{}, err
	}

	metricIDs := data.MetricIDs

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			totalValue := 0.0
			for _, metricID := range metricIDs {
				for _, dp := range numericBlob.All(metricID) {
					totalValue += dp.Val
				}
			}

			// Prevent compiler from optimizing away
			if totalValue == -1 {
				b.Fatal("unreachable")
			}
		}
	})

	return toBenchMetrics(result), nil
}

// toBenchMetrics converts a testing.BenchmarkResult to BenchMetrics.
func toBenchMetrics(r testing.BenchmarkResult) BenchMetrics {
	n := int64(r.N)
	if n == 0 {
		n = 1
	}

	return BenchMetrics{
		NsPerOp:     float64(r.T.Nanoseconds()) / float64(n),
		BytesPerOp:  int64(r.MemBytes) / n,
		AllocsPerOp: int64(r.MemAllocs) / n,
	}
}
