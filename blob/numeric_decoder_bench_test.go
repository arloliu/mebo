package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// BenchmarkNumericDecoder_Decode benchmarks the full decode operation
// to measure the impact of slice pre-allocation optimization.
func BenchmarkNumericDecoder_Decode(b *testing.B) {
	scenarios := []struct {
		name        string
		metricCount int
		pointCount  int
	}{
		{"10metrics_10points", 10, 10},
		{"50metrics_10points", 50, 10},
		{"150metrics_10points", 150, 10},
		{"500metrics_5points", 500, 5},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			// Create test data
			data := createBenchmarkData(b, sc.metricCount, sc.pointCount)

			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()

			for b.Loop() {
				decoder, _ := NewNumericDecoder(data)
				_, _ = decoder.Decode()
			}
		})
	}
}

// BenchmarkNumericDecoder_ParseIndexEntries specifically benchmarks
// the index parsing with slice pre-allocation.
func BenchmarkNumericDecoder_ParseIndexEntries(b *testing.B) {
	metricCounts := []int{10, 50, 150, 500, 1000}

	for _, count := range metricCounts {
		b.Run(formatMetricCount(count), func(b *testing.B) {
			// Create test data
			data := createBenchmarkData(b, count, 10)
			decoder, _ := NewNumericDecoder(data)

			// Calculate index offset
			indexOffset := section.HeaderSize

			// Decompress payloads to get sizes (required for parseIndexEntries)
			tsOffset := int(decoder.header.TimestampPayloadOffset)
			valOffset := int(decoder.header.ValuePayloadOffset)
			tagOffset := int(decoder.header.TagPayloadOffset)
			payloads, _ := decoder.decompressPayloads(tsOffset, valOffset, tagOffset)

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				// Parse index entries (where our optimization is)
				_, _, _ = decoder.parseIndexEntries(indexOffset, len(payloads.tsPayload), len(payloads.valPayload), len(payloads.tagPayload))
			}
		})
	}
}

// BenchmarkNumericDecoder_Sequential simulates real-world sequential decoding
// of many blobs (e.g., reading from storage).
func BenchmarkNumericDecoder_Sequential(b *testing.B) {
	const metricCount = 150
	const pointCount = 10

	// Create test data
	data := createBenchmarkData(b, metricCount, pointCount)

	b.Run("150metrics_noReuse", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(data)))
		b.ResetTimer()

		// Simulate decoding many blobs sequentially (no decoder reuse)
		for b.Loop() {
			for range 100 {
				decoder, _ := NewNumericDecoder(data)
				_, _ = decoder.Decode()
			}
		}
	})
}

// Helper function to create benchmark data
func createBenchmarkData(tb testing.TB, metricCount, pointCount int) []byte {
	tb.Helper()

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime)
	if err != nil {
		tb.Fatalf("Failed to create encoder: %v", err)
	}

	// Create data for each metric
	for m := range metricCount {
		// Generate unique metric names to avoid hash collisions
		metricName := "benchmark.metric." + string(rune('0'+(m/100)%10)) +
			string(rune('0'+(m/10)%10)) + string(rune('0'+m%10))
		metricID := hash.ID(metricName)

		err = encoder.StartMetricID(metricID, pointCount)
		if err != nil {
			tb.Fatalf("Failed to start metric %d (%s): %v", m, metricName, err)
		}

		for p := range pointCount {
			ts := startTime.Add(time.Duration(p) * time.Minute).UnixMicro()
			value := float64(m*1000 + p)

			err = encoder.AddDataPoint(ts, value, "")
			if err != nil {
				tb.Fatalf("Failed to add data point: %v", err)
			}
		}

		err = encoder.EndMetric()
		if err != nil {
			tb.Fatalf("Failed to end metric: %v", err)
		}
	}

	data, err := encoder.Finish()
	if err != nil {
		tb.Fatalf("Failed to finish encoding: %v", err)
	}

	return data
}

func formatMetricCount(count int) string {
	return string(rune(count)) + "metrics"
}
