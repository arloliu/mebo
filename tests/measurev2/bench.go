package main

import (
	"math/rand"
	"testing"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
)

// encodeBlob encodes test data with the given combo and returns raw blob bytes.
// This is the shared encoding logic used by both benchmark and size measurement functions.
func encodeBlob(combo EncodingCombo, data *TestData) ([]byte, error) {
	opts := []blob.NumericEncoderOption{
		blob.WithTimestampEncoding(combo.TSEncoding),
		blob.WithTimestampCompression(format.CompressionNone),
		blob.WithValueEncoding(combo.ValEncoding),
		blob.WithValueCompression(format.CompressionNone),
	}
	if combo.SharedTS {
		opts = append(opts, blob.WithSharedTimestamps())
	}

	encoder, err := blob.NewNumericEncoder(
		data.StartTime,
		opts...,
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

// randomAccessPattern returns one uniformly random point index per metric,
// using a seed derived from the data's own seed so the pattern is reproducible
// across runs but distinct from the data-generation RNG stream.
//
// A single random index per metric (rather than a fixed first/middle/last
// probe) is deliberate: ValueAt/TimestampAt's cost for the sequential-decode
// encodings (Gorilla, Chimp, Delta, DeltaPacked) scales with the index itself,
// so cherry-picking index 0 would understate their real cost and always
// picking the last index would overstate it. A uniformly random index across
// the whole metric reports the realistic average a "random access" workload
// actually sees.
func randomAccessPattern(data *TestData) []int {
	rng := rand.New(rand.NewSource(data.Config.Seed + 1)) //nolint:gosec // seeded PRNG for reproducible benchmark access pattern
	ppm := data.Config.PointsPerMetric
	indices := make([]int, len(data.MetricIDs))
	for i := range indices {
		indices[i] = rng.Intn(ppm)
	}

	return indices
}

// benchRandomAccessValue runs a Go benchmark measuring ValueAt at a random
// index per metric. It pre-encodes and pre-decodes, then benchmarks only the
// lookup. See randomAccessPattern for why the index is randomized per metric
// rather than fixed.
func benchRandomAccessValue(combo EncodingCombo, data *TestData) (BenchMetrics, error) {
	blobData, err := encodeBlob(combo, data)
	if err != nil {
		return BenchMetrics{}, err
	}

	numericBlob, err := decodeBlob(blobData)
	if err != nil {
		return BenchMetrics{}, err
	}

	metricIDs := data.MetricIDs
	indices := randomAccessPattern(data)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			var total float64
			for i, metricID := range metricIDs {
				val, ok := numericBlob.ValueAt(metricID, indices[i])
				if !ok {
					b.Fatalf("ValueAt failed for metric %d index %d", metricID, indices[i])
				}
				total += val
			}

			// Prevent compiler from optimizing away
			if total == -1 {
				b.Fatal("unreachable")
			}
		}
	})

	return toBenchMetrics(result), nil
}

// benchRandomAccessTimestamp runs a Go benchmark measuring TimestampAt at a
// random index per metric, using the same access pattern as
// benchRandomAccessValue so the two are directly comparable.
func benchRandomAccessTimestamp(combo EncodingCombo, data *TestData) (BenchMetrics, error) {
	blobData, err := encodeBlob(combo, data)
	if err != nil {
		return BenchMetrics{}, err
	}

	numericBlob, err := decodeBlob(blobData)
	if err != nil {
		return BenchMetrics{}, err
	}

	metricIDs := data.MetricIDs
	indices := randomAccessPattern(data)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			var total int64
			for i, metricID := range metricIDs {
				ts, ok := numericBlob.TimestampAt(metricID, indices[i])
				if !ok {
					b.Fatalf("TimestampAt failed for metric %d index %d", metricID, indices[i])
				}
				total += ts
			}

			// Prevent compiler from optimizing away
			if total == -1 {
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
		BytesPerOp:  r.AllocedBytesPerOp(),
		AllocsPerOp: r.AllocsPerOp(),
	}
}
