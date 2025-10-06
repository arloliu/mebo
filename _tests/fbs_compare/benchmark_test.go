package fbscompare

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
)

// Benchmark configurations per BENCHMARK_PLAN.md
const (
	// Typical real-world scenario: 200 metrics per blob
	numMetrics = 200
)

// EncodingConfig represents a complete encoding configuration with independent dimensions
// Format: timestamp_encoding-timestamp_compression-value_encoding-value_compression
type EncodingConfig struct {
	name        string
	tsEncoding  format.EncodingType
	tsCompress  format.CompressionType
	valEncoding format.EncodingType
	valCompress format.CompressionType
}

var (
	// Data sizes: 10, 100, 250 points per metric
	// Note: 200 metrics × 250 points = 50,000 < 65,535 (uint16 offset limit with safety margin)
	benchmarkSizes = []struct {
		name   string
		points int
	}{
		{"10pts", 10},
		{"100pts", 100},
		{"250pts", 250},
	}

	// Mebo encoding combinations: Programmatically generated
	// 2 timestamp encodings × 4 timestamp compressions × 2 value encodings × 4 value compressions = 64 variants
	// Format: timestamp_encoding-timestamp_compression-value_encoding-value_compression
	meboEncodings = generateMeboEncodings(false) // Essential subset for benchmarks

	// Full encoding set for size comparison tests (all 64 combinations)
	meboEncodingsFull = generateMeboEncodings(true)

	// FlatBuffers compression variants: 4 variants
	fbsCompressions = []string{"none", "zstd", "s2", "lz4"}
)

// generateMeboEncodings generates encoding configurations programmatically
// If includeFull is true, returns all 64 combinations (2×4×2×4)
// If false, returns essential subset (~15 configs) for faster benchmarks
func generateMeboEncodings(includeFull bool) []EncodingConfig {
	tsEncodings := []struct {
		name string
		typ  format.EncodingType
	}{
		{"raw", format.TypeRaw},
		{"delta", format.TypeDelta},
	}

	valEncodings := []struct {
		name string
		typ  format.EncodingType
	}{
		{"raw", format.TypeRaw},
		{"gorilla", format.TypeGorilla},
	}

	compressions := []struct {
		name string
		typ  format.CompressionType
	}{
		{"none", format.CompressionNone},
		{"zstd", format.CompressionZstd},
		{"s2", format.CompressionS2},
		{"lz4", format.CompressionLZ4},
	}

	var configs []EncodingConfig

	// Generate all combinations
	for _, tsEnc := range tsEncodings {
		for _, tsComp := range compressions {
			for _, valEnc := range valEncodings {
				for _, valComp := range compressions {
					config := EncodingConfig{
						name:        fmt.Sprintf("mebo/%s-%s-%s-%s", tsEnc.name, tsComp.name, valEnc.name, valComp.name),
						tsEncoding:  tsEnc.typ,
						tsCompress:  tsComp.typ,
						valEncoding: valEnc.typ,
						valCompress: valComp.typ,
					}
					configs = append(configs, config)
				}
			}
		}
	}

	if includeFull {
		return configs // Return all 64 combinations
	}

	// Return essential subset for performance benchmarks
	return filterEssentialEncodings(configs)
}

// filterEssentialEncodings returns curated subset of essential configurations
// These configs isolate key factors and represent real-world production scenarios
func filterEssentialEncodings(configs []EncodingConfig) []EncodingConfig {
	essential := map[string]bool{
		// Baseline configurations
		"mebo/raw-none-raw-none":   true, // Pure baseline
		"mebo/delta-none-raw-none": true, // Delta timestamps only

		// Gorilla encoding tests (pure effect)
		"mebo/raw-none-gorilla-none":   true, // Pure Gorilla compression
		"mebo/delta-none-gorilla-none": true, // Gorilla + delta timestamps

		// Gorilla with compression (production candidates)
		"mebo/delta-none-gorilla-zstd": true, // Gorilla + value compression
		"mebo/delta-zstd-gorilla-none": true, // Gorilla + timestamp compression
		"mebo/delta-zstd-gorilla-zstd": true, // Best overall (likely winner)
		"mebo/delta-none-gorilla-s2":   true, // Gorilla + S2 compression
		"mebo/delta-none-gorilla-lz4":  true, // Gorilla + LZ4 compression

		// Legacy raw encoding for comparison
		"mebo/delta-none-raw-zstd": true, // Old best for value compression
		"mebo/delta-zstd-raw-none": true, // Old best for timestamp compression
		"mebo/delta-zstd-raw-zstd": true, // Old overall best
	}

	var result []EncodingConfig
	for _, cfg := range configs {
		if essential[cfg.name] {
			result = append(result, cfg)
		}
	}

	return result
}

// Helper: Create mebo blob from test data
func createMeboBlob(metrics []MetricData, tsEncoding format.EncodingType, tsCompress format.CompressionType, valEncoding format.EncodingType, valCompress format.CompressionType) ([]byte, error) {
	if len(metrics) == 0 {
		return nil, fmt.Errorf("no metrics provided")
	}

	startTimeMicro := metrics[0].Timestamps[0]
	startTime := time.UnixMicro(startTimeMicro)

	encoder, err := blob.NewNumericEncoder(
		startTime,
		blob.WithTimestampEncoding(tsEncoding),
		blob.WithValueEncoding(valEncoding),
		blob.WithTimestampCompression(tsCompress),
		blob.WithValueCompression(valCompress),
	)
	if err != nil {
		return nil, err
	}

	for _, metric := range metrics {
		if err := encoder.StartMetricID(metric.ID, len(metric.Timestamps)); err != nil {
			return nil, err
		}
		// Phase 1 Optimization: Use batch API instead of per-point loop
		// This reduces allocations from ~2 per point to ~3 per metric
		if err := encoder.AddDataPoints(metric.Timestamps, metric.Values, nil); err != nil {
			return nil, err
		}
		if err := encoder.EndMetric(); err != nil {
			return nil, err
		}
	}

	return encoder.Finish()
}

// Helper: Decode mebo blob
func decodeMeboBlob(data []byte) (*blob.NumericBlob, error) {
	decoder, err := blob.NewNumericDecoder(data)
	if err != nil {
		return nil, err
	}
	decoded, err := decoder.Decode()
	if err != nil {
		return nil, err
	}

	return &decoded, nil
}

// Helper: Get all metric IDs from test data
func getAllMetricIDs(metrics []MetricData) []uint64 {
	ids := make([]uint64, len(metrics))
	for i, m := range metrics {
		ids[i] = m.ID
	}

	return ids
}

// =============================================================================
// 1. Encoding Benchmarks
// =============================================================================

// BenchmarkEncode_Mebo benchmarks mebo encoding with all combinations
func BenchmarkEncode_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)

		for _, enc := range meboEncodings {
			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_, err := createMeboBlob(testData, enc.tsEncoding, enc.tsCompress, enc.valEncoding, enc.valCompress)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkEncode_FBS benchmarks FlatBuffers encoding with compression variants
func BenchmarkEncode_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)

		for _, compression := range fbsCompressions {
			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_, err := EncodeFBS(testData, compression)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// =============================================================================
// 2. Decode Benchmarks (SEPARATE from iteration per PLAN.md)
// =============================================================================

// BenchmarkDecode_Mebo benchmarks mebo decode (decompression) only
func BenchmarkDecode_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)

		for _, enc := range meboEncodings {
			data, err := createMeboBlob(testData, enc.tsEncoding, enc.tsCompress, enc.valEncoding, enc.valCompress)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_, err := decodeMeboBlob(data)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkDecode_FBS benchmarks FlatBuffers decode (decompression) only
func BenchmarkDecode_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := fbsBlob.Decode(); err != nil {
						b.Fatal(err)
					}
					// Reset decoded state for next iteration
					fbsBlob.decoded = false
				}
			})
		}
	}
}

// =============================================================================
// 3. Iteration Benchmarks - ALL METRICS (after decode, per PLAN.md)
// =============================================================================

// BenchmarkIterateAll_Mebo benchmarks iterating All() through ALL 200 metrics
func BenchmarkIterateAll_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		b.Logf("Testing size=%s, numMetrics=%d, points=%d, total=%d", size.name, numMetrics, size.points, numMetrics*size.points)

		for _, enc := range meboEncodings {
			data, err := createMeboBlob(testData, enc.tsEncoding, enc.tsCompress, enc.valEncoding, enc.valCompress)
			if err != nil {
				b.Fatal(err)
			} // Decode once (not measured)
			decoded, err := decodeMeboBlob(data)
			if err != nil {
				b.Fatalf("Decode failed for %s: %v", enc.name, err)
			}

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					// Iterate through ALL metrics
					for _, metricID := range metricIDs {
						for ts, val := range decoded.All(metricID) {
							_ = ts
							_ = val // Consume values
						}
					}
				}
			})
		}
	}
}

// BenchmarkIterateAll_FBS benchmarks iterating All() through ALL 200 metrics
func BenchmarkIterateAll_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			// Decode once (not measured)
			if err := fbsBlob.Decode(); err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					// Iterate through ALL metrics
					for _, metricID := range metricIDs {
						for ts, val := range fbsBlob.All(metricID) {
							_ = ts
							_ = val // Consume values
						}
					}
				}
			})
		}
	}
}

// BenchmarkIterateAllTimestamps_Mebo benchmarks AllTimestamps() for ALL 200 metrics
func BenchmarkIterateAllTimestamps_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		for _, enc := range meboEncodings {
			data, err := createMeboBlob(testData, enc.tsEncoding, enc.tsCompress, enc.valEncoding, enc.valCompress)
			if err != nil {
				b.Fatal(err)
			}

			decoded, err := decodeMeboBlob(data)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for _, metricID := range metricIDs {
						for ts := range decoded.AllTimestamps(metricID) {
							_ = ts // Consume timestamp
						}
					}
				}
			})
		}
	}
}

// BenchmarkIterateAllTimestamps_FBS benchmarks AllTimestamps() for ALL 200 metrics
func BenchmarkIterateAllTimestamps_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			if err := fbsBlob.Decode(); err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for _, metricID := range metricIDs {
						for ts := range fbsBlob.AllTimestamps(metricID) {
							_ = ts // Consume timestamp
						}
					}
				}
			})
		}
	}
}

// BenchmarkIterateAllValues_Mebo benchmarks AllValues() for ALL 200 metrics
func BenchmarkIterateAllValues_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		for _, enc := range meboEncodings {
			data, err := createMeboBlob(testData, enc.tsEncoding, enc.tsCompress, enc.valEncoding, enc.valCompress)
			if err != nil {
				b.Fatal(err)
			}

			decoded, err := decodeMeboBlob(data)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for _, metricID := range metricIDs {
						for val := range decoded.AllValues(metricID) {
							_ = val // Consume value
						}
					}
				}
			})
		}
	}
}

// BenchmarkIterateAllValues_FBS benchmarks AllValues() for ALL 200 metrics
func BenchmarkIterateAllValues_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			if err := fbsBlob.Decode(); err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for _, metricID := range metricIDs {
						for val := range fbsBlob.AllValues(metricID) {
							_ = val // Consume value
						}
					}
				}
			})
		}
	}
}

// =============================================================================
// 4. Random Access Benchmarks - ALL METRICS (after decode, per PLAN.md)
// =============================================================================

// BenchmarkRandomAccessValue_Mebo benchmarks ValueAt() across ALL 200 metrics
func BenchmarkRandomAccessValue_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		// Pre-generate random indices for each metric
		rng := rand.New(rand.NewSource(42))
		randomIndices := make([]int, len(metricIDs))
		for i := range randomIndices {
			randomIndices[i] = rng.Intn(size.points)
		}

		for _, enc := range meboEncodings {
			data, err := createMeboBlob(testData, enc.tsEncoding, enc.tsCompress, enc.valEncoding, enc.valCompress)
			if err != nil {
				b.Fatal(err)
			}

			decoded, err := decodeMeboBlob(data)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					// Random access across ALL metrics
					for i, metricID := range metricIDs {
						_, ok := decoded.ValueAt(metricID, randomIndices[i])
						if !ok {
							b.Fatalf("ValueAt failed for metric #%d (ID: %016x), index: %d", i, metricID, randomIndices[i])
						}
					}
				}
			})
		}
	}
}

// BenchmarkRandomAccessValue_FBS benchmarks ValueAt() across ALL 200 metrics
func BenchmarkRandomAccessValue_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		rng := rand.New(rand.NewSource(42))
		randomIndices := make([]int, len(metricIDs))
		for i := range randomIndices {
			randomIndices[i] = rng.Intn(size.points)
		}

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			if err := fbsBlob.Decode(); err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for i, metricID := range metricIDs {
						_, err := fbsBlob.ValueAt(metricID, randomIndices[i])
						if err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}

// BenchmarkRandomAccessTimestamp_Mebo benchmarks TimestampAt() across ALL 200 metrics
func BenchmarkRandomAccessTimestamp_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		rng := rand.New(rand.NewSource(42))
		randomIndices := make([]int, len(metricIDs))
		for i := range randomIndices {
			randomIndices[i] = rng.Intn(size.points)
		}

		for _, enc := range meboEncodings {
			// Skip delta encoding - it doesn't support random timestamp access
			if enc.tsEncoding == format.TypeDelta {
				continue
			}

			data, err := createMeboBlob(testData, enc.tsEncoding, enc.tsCompress, enc.valEncoding, enc.valCompress)
			if err != nil {
				b.Fatal(err)
			}

			decoded, err := decodeMeboBlob(data)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for i, metricID := range metricIDs {
						_, ok := decoded.TimestampAt(metricID, randomIndices[i])
						if !ok {
							b.Fatal("TimestampAt failed")
						}
					}
				}
			})
		}
	}
}

// BenchmarkRandomAccessTimestamp_FBS benchmarks TimestampAt() across ALL 200 metrics
func BenchmarkRandomAccessTimestamp_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		rng := rand.New(rand.NewSource(42))
		randomIndices := make([]int, len(metricIDs))
		for i := range randomIndices {
			randomIndices[i] = rng.Intn(size.points)
		}

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			if err := fbsBlob.Decode(); err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for i, metricID := range metricIDs {
						_, err := fbsBlob.TimestampAt(metricID, randomIndices[i])
						if err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}

// =============================================================================
// 5. Decode + Iteration Combined (Realistic Read Workload)
// =============================================================================

// BenchmarkDecodeAndIterateAll_Mebo measures the combined time to decode and iterate through all metrics
// This reflects the realistic use case: decode blob once, then iterate through all data
func BenchmarkDecodeAndIterateAll_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		for _, enc := range meboEncodings {
			data, err := createMeboBlob(testData, enc.tsEncoding, enc.tsCompress, enc.valEncoding, enc.valCompress)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					// Decode blob (one-time cost)
					decoded, err := decodeMeboBlob(data)
					if err != nil {
						b.Fatal(err)
					}

					// Iterate through ALL metrics (typical read workload)
					for _, metricID := range metricIDs {
						for ts := range decoded.AllTimestamps(metricID) {
							_ = ts
						}
						for val := range decoded.AllValues(metricID) {
							_ = val
						}
					}
				}
			})
		}
	}
}

// BenchmarkDecodeAndIterateAll_FBS measures combined decode + iteration for FlatBuffers
func BenchmarkDecodeAndIterateAll_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		metricIDs := getAllMetricIDs(testData)

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					// Decode blob (one-time cost)
					if err := fbsBlob.Decode(); err != nil {
						b.Fatal(err)
					}

					// Iterate through ALL metrics
					for _, metricID := range metricIDs {
						for ts := range fbsBlob.AllTimestamps(metricID) {
							_ = ts
						}
						for val := range fbsBlob.AllValues(metricID) {
							_ = val
						}
					}

					// Reset decoded state for next iteration
					fbsBlob.decoded = false
				}
			})
		}
	}
}

// =============================================================================
// 6. Size Benchmarks (Report encoding sizes)
// =============================================================================

// BenchmarkSize_Mebo reports mebo blob sizes for all combinations
func BenchmarkSize_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		totalPoints := numMetrics * size.points

		for _, enc := range meboEncodings {
			data, err := createMeboBlob(testData, enc.tsEncoding, enc.tsCompress, enc.valEncoding, enc.valCompress)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportMetric(float64(len(data)), "bytes")
				b.ReportMetric(float64(len(data))/float64(totalPoints), "bytes/point")
				b.ReportMetric(float64(numMetrics), "metrics")
			})
		}
	}
}

// BenchmarkSize_FBS reports FlatBuffers blob sizes
func BenchmarkSize_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numMetrics, size.points)
		testData := GenerateTestData(cfg)
		totalPoints := numMetrics * size.points

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportMetric(float64(fbsBlob.Size()), "bytes")
				b.ReportMetric(float64(fbsBlob.Size())/float64(totalPoints), "bytes/point")
				b.ReportMetric(float64(numMetrics), "metrics")
				if compression != "none" {
					ratio := float64(fbsBlob.UncompressedSize()) / float64(fbsBlob.Size())
					b.ReportMetric(ratio, "comp-ratio")
				}
			})
		}
	}
}
