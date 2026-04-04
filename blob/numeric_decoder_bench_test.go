package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// BenchmarkSharedTimestamps_DecodeOnly isolates decoder overhead from iterator cost.
func BenchmarkSharedTimestamps_DecodeOnly(b *testing.B) {
	scenarios := []sharedTimestampBenchmarkScenario{
		{name: "150metrics_10points_DefaultCompression", metricCount: 150, pointsPerMetric: 10, compressionEnabled: true},
		{name: "150metrics_10points_NoCompression", metricCount: 150, pointsPerMetric: 10, compressionEnabled: false},
		{name: "150metrics_100points_DefaultCompression", metricCount: 150, pointsPerMetric: 100, compressionEnabled: true},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			for _, sharedEnabled := range []bool{false, true} {
				modeName := "V1_Default"
				if sharedEnabled {
					modeName = "V2_SharedTimestamps"
				}

				data := createSharedTimestampBenchmarkData(b, sc, sharedEnabled)
				b.Run(modeName, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))
					b.ResetTimer()

					for b.Loop() {
						decoder, err := NewNumericDecoder(data)
						if err != nil {
							b.Fatal(err)
						}

						blob, err := decoder.Decode()
						if err != nil {
							b.Fatal(err)
						}

						if blob.MetricCount() != sc.metricCount {
							b.Fatalf("unexpected metric count: got %d want %d", blob.MetricCount(), sc.metricCount)
						}
					}
				})
			}
		})
	}
}

// BenchmarkSharedTimestamps_DecodeAndIterate benchmarks full decode followed by
// iterating every point from every metric for repeated-timestamp workloads.
func BenchmarkSharedTimestamps_DecodeAndIterate(b *testing.B) {
	scenarios := []sharedTimestampBenchmarkScenario{
		{name: "150metrics_10points_DefaultCompression", metricCount: 150, pointsPerMetric: 10, compressionEnabled: true},
		{name: "150metrics_10points_NoCompression", metricCount: 150, pointsPerMetric: 10, compressionEnabled: false},
		{name: "150metrics_100points_DefaultCompression", metricCount: 150, pointsPerMetric: 100, compressionEnabled: true},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			for _, sharedEnabled := range []bool{false, true} {
				modeName := "V1_Default"
				if sharedEnabled {
					modeName = "V2_SharedTimestamps"
				}

				data := createSharedTimestampBenchmarkData(b, sc, sharedEnabled)
				b.Run(modeName, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))
					b.ResetTimer()

					for b.Loop() {
						decoder, err := NewNumericDecoder(data)
						if err != nil {
							b.Fatal(err)
						}

						blob, err := decoder.Decode()
						if err != nil {
							b.Fatal(err)
						}

						pointCount := 0
						totalValue := 0.0
						for metricIdx := range sc.metricCount {
							for _, dp := range blob.All(uint64(metricIdx + 1)) {
								pointCount++
								totalValue += dp.Val
							}
						}

						if pointCount != sc.metricCount*sc.pointsPerMetric {
							b.Fatalf("unexpected point count: got %d want %d", pointCount, sc.metricCount*sc.pointsPerMetric)
						}
						if totalValue < 0 {
							b.Fatal("unexpected negative total value")
						}
					}
				})
			}
		})
	}
}

// BenchmarkSharedTimestamps_Mixed_DecodeAndIterate measures decode plus full
// iteration for workloads combining shared groups with unique timestamp streams.
func BenchmarkSharedTimestamps_Mixed_DecodeAndIterate(b *testing.B) {
	scenarios := []mixedSharedTimestampBenchmarkScenario{
		{name: "150metrics_10points_3groups_30unique_DefaultCompression", metricCount: 150, pointsPerMetric: 10, sharedGroupCount: 3, uniqueMetricCount: 30, compressionEnabled: true},
		{name: "150metrics_10points_3groups_30unique_NoCompression", metricCount: 150, pointsPerMetric: 10, sharedGroupCount: 3, uniqueMetricCount: 30, compressionEnabled: false},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			for _, sharedEnabled := range []bool{false, true} {
				modeName := "V1_Default"
				if sharedEnabled {
					modeName = "V2_SharedTimestamps"
				}

				data := createMixedSharedTimestampBenchmarkData(b, sc, sharedEnabled)
				b.Run(modeName, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))
					b.ResetTimer()

					for b.Loop() {
						decoder, err := NewNumericDecoder(data)
						if err != nil {
							b.Fatal(err)
						}

						blob, err := decoder.Decode()
						if err != nil {
							b.Fatal(err)
						}

						pointCount := 0
						totalValue := 0.0
						for metricIdx := range sc.metricCount {
							for _, dp := range blob.All(uint64(metricIdx + 1)) {
								pointCount++
								totalValue += dp.Val
							}
						}

						if pointCount != sc.metricCount*sc.pointsPerMetric {
							b.Fatalf("unexpected point count: got %d want %d", pointCount, sc.metricCount*sc.pointsPerMetric)
						}
						if totalValue < 0 {
							b.Fatal("unexpected negative total value")
						}
					}
				})
			}
		})
	}
}

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
				_, _, _ = decoder.parseIndexEntries(indexOffset, len(payloads.tsPayload), len(payloads.valPayload), len(payloads.tagPayload), false)
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

// ==============================================================================
// V1 vs V2 Layout Decode Benchmarks
// ==============================================================================

// createLayoutBenchData creates encoded blob data for V1, V2, and V2+SharedTS comparison.
// Metrics are inserted in reverse ID order to exercise the V2 sorting path.
func createLayoutBenchData(tb testing.TB, metricCount, pointsPerMetric int) []struct {
	label string
	data  []byte
} {
	tb.Helper()

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	configs := layoutConfigs()
	result := make([]struct {
		label string
		data  []byte
	}, len(configs))

	for ci, cfg := range configs {
		opts := append([]NumericEncoderOption{
			WithTimestampEncoding(format.TypeDelta),
			WithValueEncoding(format.TypeGorilla),
		}, cfg.opts...)

		enc, err := NewNumericEncoder(startTime, opts...)
		if err != nil {
			tb.Fatalf("%s: encoder: %v", cfg.label, err)
		}

		for i := metricCount; i >= 1; i-- {
			if err = enc.StartMetricID(uint64(i), pointsPerMetric); err != nil {
				tb.Fatal(err)
			}
			for j := range pointsPerMetric {
				ts := startTime.Add(time.Duration(j) * time.Second).UnixMicro()
				if err = enc.AddDataPoint(ts, float64(i*1000+j), ""); err != nil {
					tb.Fatal(err)
				}
			}
			if err = enc.EndMetric(); err != nil {
				tb.Fatal(err)
			}
		}

		data, err := enc.Finish()
		if err != nil {
			tb.Fatal(err)
		}
		result[ci] = struct {
			label string
			data  []byte
		}{label: cfg.label, data: data}
	}

	return result
}

// BenchmarkV2Layout_DecodeOnly compares decode overhead for V1, V2, and V2+SharedTS.
func BenchmarkV2Layout_DecodeOnly(b *testing.B) {
	sizes := []struct {
		name    string
		metrics int
		points  int
	}{
		{"10metrics_10points", 10, 10},
		{"150metrics_10points", 150, 10},
		{"500metrics_10points", 500, 10},
		{"150metrics_100points", 150, 100},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			configs := createLayoutBenchData(b, sz.metrics, sz.points)

			for _, cfg := range configs {
				b.Run(cfg.label, func(b *testing.B) {
					data := cfg.data
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))
					b.ResetTimer()
					for b.Loop() {
						dec, err := NewNumericDecoder(data)
						if err != nil {
							b.Fatal(err)
						}
						blob, err := dec.Decode()
						if err != nil {
							b.Fatal(err)
						}
						if blob.MetricCount() != sz.metrics {
							b.Fatalf("got %d metrics, want %d", blob.MetricCount(), sz.metrics)
						}
					}
				})
			}
		})
	}
}

// BenchmarkV2Layout_DecodeAndIterate compares full decode + point iteration.
func BenchmarkV2Layout_DecodeAndIterate(b *testing.B) {
	const metricCount = 150
	const pointsPerMetric = 10

	configs := createLayoutBenchData(b, metricCount, pointsPerMetric)

	for _, cfg := range configs {
		b.Run(cfg.label, func(b *testing.B) {
			data := cfg.data
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for b.Loop() {
				dec, _ := NewNumericDecoder(data)
				blob, _ := dec.Decode()
				total := 0
				for i := range metricCount {
					for _, dp := range blob.All(uint64(i + 1)) {
						_ = dp.Val
						total++
					}
				}
				if total != metricCount*pointsPerMetric {
					b.Fatalf("got %d, want %d", total, metricCount*pointsPerMetric)
				}
			}
		})
	}
}

// BenchmarkV2Layout_EncodedSize reports byte sizes for V1, V2, and V2+SharedTS.
func BenchmarkV2Layout_EncodedSize(b *testing.B) {
	sizes := []struct {
		name    string
		metrics int
		points  int
	}{
		{"150metrics_10points", 150, 10},
		{"500metrics_10points", 500, 10},
		{"150metrics_100points", 150, 100},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			configs := createLayoutBenchData(b, sz.metrics, sz.points)

			b.ResetTimer()
			for b.Loop() {
			}

			for _, cfg := range configs {
				b.ReportMetric(float64(len(cfg.data)), cfg.label+"-bytes")
			}
		})
	}
}

// BenchmarkSharedTsCache_AllTimestamps benchmarks AllTimestamps iteration
// with and without the shared timestamp cache to measure the cache benefit.
func BenchmarkSharedTsCache_AllTimestamps(b *testing.B) {
	scenarios := []struct {
		name    string
		metrics int
		points  int
	}{
		{"150metrics_10points", 150, 10},
		{"150metrics_100points", 150, 100},
		{"200metrics_200points", 200, 200},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			// Create shared-TS blob
			data := createSharedTimestampBenchmarkData(b, sharedTimestampBenchmarkScenario{
				metricCount:        sc.metrics,
				pointsPerMetric:    sc.points,
				compressionEnabled: false,
			}, true)

			decoder, err := NewNumericDecoder(data)
			if err != nil {
				b.Fatal(err)
			}
			blob, err := decoder.Decode()
			if err != nil {
				b.Fatal(err)
			}

			// Collect metric IDs for iteration
			metricIDs := make([]uint64, sc.metrics)
			for i := range sc.metrics {
				metricIDs[i] = uint64(i + 1)
			}

			b.Run("AllTimestamps_WithCache", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for b.Loop() {
					total := int64(0)
					for _, id := range metricIDs {
						for ts := range blob.AllTimestamps(id) {
							total += ts
						}
					}
					if total == 0 {
						b.Fatal("unexpected zero total")
					}
				}
			})

			// Create a blob WITHOUT cache for comparison (V1 with same timestamps)
			dataV1 := createSharedTimestampBenchmarkData(b, sharedTimestampBenchmarkScenario{
				metricCount:        sc.metrics,
				pointsPerMetric:    sc.points,
				compressionEnabled: false,
			}, false)

			decoderV1, err := NewNumericDecoder(dataV1)
			if err != nil {
				b.Fatal(err)
			}
			blobV1, err := decoderV1.Decode()
			if err != nil {
				b.Fatal(err)
			}

			b.Run("AllTimestamps_NoCache_V1", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for b.Loop() {
					total := int64(0)
					for _, id := range metricIDs {
						for ts := range blobV1.AllTimestamps(id) {
							total += ts
						}
					}
					if total == 0 {
						b.Fatal("unexpected zero total")
					}
				}
			})

			// Also benchmark AllValues to show it's unaffected
			b.Run("AllValues_SharedTS", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for b.Loop() {
					total := float64(0)
					for _, id := range metricIDs {
						for val := range blob.AllValues(id) {
							total += val
						}
					}
					if total == 0 {
						b.Fatal("unexpected zero total")
					}
				}
			})
		})
	}
}
