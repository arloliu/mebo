package fbscompare

import (
	"fmt"
	"testing"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
)

// Text blob benchmark configurations
const (
	numTextMetrics = 200 // Same as numeric benchmarks
)

// Text blob encoding combinations: 2 timestamp encodings × 4 compressions = 8 variants
var textMeboEncodings = []struct {
	name        string
	tsEncoding  format.EncodingType
	compression format.CompressionType
}{
	// Raw timestamp encoding
	{"mebo/raw-none", format.TypeRaw, format.CompressionNone},
	{"mebo/raw-zstd", format.TypeRaw, format.CompressionZstd},
	{"mebo/raw-s2", format.TypeRaw, format.CompressionS2},
	{"mebo/raw-lz4", format.TypeRaw, format.CompressionLZ4},
	// Delta timestamp encoding
	{"mebo/delta-none", format.TypeDelta, format.CompressionNone},
	{"mebo/delta-zstd", format.TypeDelta, format.CompressionZstd},
	{"mebo/delta-s2", format.TypeDelta, format.CompressionS2},
	{"mebo/delta-lz4", format.TypeDelta, format.CompressionLZ4},
}

// Helper: Create mebo text blob from test data
func createMeboTextBlobBench(metrics []TextMetricData, tsEncoding format.EncodingType, compression format.CompressionType) ([]byte, error) {
	if len(metrics) == 0 {
		return nil, fmt.Errorf("no metrics provided")
	}

	startTimeMicro := metrics[0].Timestamps[0]
	startTime := time.UnixMicro(startTimeMicro)

	encoder, err := blob.NewTextEncoder(
		startTime,
		blob.WithTextTimestampEncoding(tsEncoding),
		blob.WithTextDataCompression(compression),
	)
	if err != nil {
		return nil, err
	}

	for _, metric := range metrics {
		if err := encoder.StartMetricID(metric.ID, len(metric.Timestamps)); err != nil {
			return nil, err
		}

		for i := range metric.Timestamps {
			tag := ""
			if i < len(metric.Tags) {
				tag = metric.Tags[i]
			}
			if err := encoder.AddDataPoint(metric.Timestamps[i], metric.Values[i], tag); err != nil {
				return nil, err
			}
		}

		if err := encoder.EndMetric(); err != nil {
			return nil, err
		}
	}

	return encoder.Finish()
}

// Helper: Decode mebo text blob
func decodeMeboTextBlobBench(data []byte) (*blob.TextBlob, error) {
	decoder, err := blob.NewTextDecoder(data)
	if err != nil {
		return nil, err
	}
	decoded, err := decoder.Decode()
	if err != nil {
		return nil, err
	}

	return &decoded, nil
}

// Helper: Get all metric IDs from text test data
func getAllTextMetricIDs(metrics []TextMetricData) []uint64 {
	ids := make([]uint64, len(metrics))
	for i, m := range metrics {
		ids[i] = m.ID
	}

	return ids
}

// =============================================================================
// 1. Encoding Benchmarks
// =============================================================================

// BenchmarkEncodeText_Mebo benchmarks mebo text encoding with all combinations
func BenchmarkEncodeText_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)

		for _, enc := range textMeboEncodings {
			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_, err := createMeboTextBlobBench(testData, enc.tsEncoding, enc.compression)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkEncodeText_FBS benchmarks FlatBuffers text encoding with compression variants
func BenchmarkEncodeText_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)

		for _, compression := range fbsCompressions {
			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_, err := EncodeTextFBS(testData, compression)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// =============================================================================
// 2. Decode Benchmarks (SEPARATE from iteration)
// =============================================================================

// BenchmarkDecodeText_Mebo benchmarks mebo text decode (decompression) only
func BenchmarkDecodeText_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)

		for _, enc := range textMeboEncodings {
			data, err := createMeboTextBlobBench(testData, enc.tsEncoding, enc.compression)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_, err := decodeMeboTextBlobBench(data)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkDecodeText_FBS benchmarks FlatBuffers text decode (decompression) only
func BenchmarkDecodeText_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeTextFBS(testData, compression)
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
// 3. Iteration Benchmarks - ALL METRICS (after decode)
// =============================================================================

// BenchmarkIterateAllText_Mebo benchmarks iterating All() through ALL 200 text metrics
func BenchmarkIterateAllText_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)
		metricIDs := getAllTextMetricIDs(testData)

		b.Logf("Testing size=%s, numMetrics=%d, points=%d, total=%d", size.name, numTextMetrics, size.points, numTextMetrics*size.points)

		for _, enc := range textMeboEncodings {
			data, err := createMeboTextBlobBench(testData, enc.tsEncoding, enc.compression)
			if err != nil {
				b.Fatal(err)
			}

			// Decode once (not measured)
			decoded, err := decodeMeboTextBlobBench(data)
			if err != nil {
				b.Fatalf("Decode failed for %s: %v", enc.name, err)
			}

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					// Iterate through ALL metrics
					for _, metricID := range metricIDs {
						for _, dp := range decoded.All(metricID) {
							_ = dp.Ts
							_ = dp.Val // Consume values
						}
					}
				}
			})
		}
	}
}

// BenchmarkIterateAllText_FBS benchmarks FlatBuffers text iteration through ALL 200 metrics
func BenchmarkIterateAllText_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)
		metricIDs := getAllTextMetricIDs(testData)

		b.Logf("Testing size=%s, numMetrics=%d, points=%d, total=%d", size.name, numTextMetrics, size.points, numTextMetrics*size.points)

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeTextFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			// Decode once (not measured)
			if err := fbsBlob.Decode(); err != nil {
				b.Fatalf("Decode failed for fbs-%s: %v", compression, err)
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

// =============================================================================
// 4. Random Access Benchmarks - Single Metric
// =============================================================================

// BenchmarkRandomAccessText_Mebo benchmarks ValueAt/TimestampAt for mebo text blobs
func BenchmarkRandomAccessText_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)

		for _, enc := range textMeboEncodings {
			data, err := createMeboTextBlobBench(testData, enc.tsEncoding, enc.compression)
			if err != nil {
				b.Fatal(err)
			}

			// Decode once (not measured)
			decoded, err := decodeMeboTextBlobBench(data)
			if err != nil {
				b.Fatalf("Decode failed for %s: %v", enc.name, err)
			}

			metricID := testData[0].ID

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					// Access all points randomly
					for i := 0; i < size.points; i++ {
						_, _ = decoded.ValueAt(metricID, i)
						_, _ = decoded.TimestampAt(metricID, i)
					}
				}
			})
		}
	}
}

// BenchmarkRandomAccessText_FBS benchmarks ValueAt/TimestampAt for FlatBuffers text blobs
func BenchmarkRandomAccessText_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeTextFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			// Decode once (not measured)
			if err := fbsBlob.Decode(); err != nil {
				b.Fatalf("Decode failed for fbs-%s: %v", compression, err)
			}

			metricID := testData[0].ID

			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					// Access all points randomly
					for i := 0; i < size.points; i++ {
						_, _ = fbsBlob.ValueAt(metricID, i)
						_, _ = fbsBlob.TimestampAt(metricID, i)
					}
				}
			})
		}
	}
}

// =============================================================================
// 5. Specialized Iterator Benchmarks
// =============================================================================

// BenchmarkIterateTimestampsText_Mebo benchmarks AllTimestamps() iterator for mebo
func BenchmarkIterateTimestampsText_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)

		for _, enc := range textMeboEncodings {
			data, err := createMeboTextBlobBench(testData, enc.tsEncoding, enc.compression)
			if err != nil {
				b.Fatal(err)
			}

			// Decode once (not measured)
			decoded, err := decodeMeboTextBlobBench(data)
			if err != nil {
				b.Fatalf("Decode failed for %s: %v", enc.name, err)
			}

			metricID := testData[0].ID

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for ts := range decoded.AllTimestamps(metricID) {
						_ = ts // Consume value
					}
				}
			})
		}
	}
}

// BenchmarkIterateValuesText_Mebo benchmarks AllValues() iterator for mebo
func BenchmarkIterateValuesText_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)

		for _, enc := range textMeboEncodings {
			data, err := createMeboTextBlobBench(testData, enc.tsEncoding, enc.compression)
			if err != nil {
				b.Fatal(err)
			}

			// Decode once (not measured)
			decoded, err := decodeMeboTextBlobBench(data)
			if err != nil {
				b.Fatalf("Decode failed for %s: %v", enc.name, err)
			}

			metricID := testData[0].ID

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for val := range decoded.AllValues(metricID) {
						_ = val // Consume value
					}
				}
			})
		}
	}
}

// BenchmarkIterateTimestampsText_FBS benchmarks AllTimestamps() iterator for FlatBuffers
func BenchmarkIterateTimestampsText_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeTextFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			// Decode once (not measured)
			if err := fbsBlob.Decode(); err != nil {
				b.Fatalf("Decode failed for fbs-%s: %v", compression, err)
			}

			metricID := testData[0].ID

			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for ts := range fbsBlob.AllTimestamps(metricID) {
						_ = ts // Consume value
					}
				}
			})
		}
	}
}

// BenchmarkIterateValuesText_FBS benchmarks AllValues() iterator for FlatBuffers
func BenchmarkIterateValuesText_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeTextFBS(testData, compression)
			if err != nil {
				b.Fatal(err)
			}

			// Decode once (not measured)
			if err := fbsBlob.Decode(); err != nil {
				b.Fatalf("Decode failed for fbs-%s: %v", compression, err)
			}

			metricID := testData[0].ID

			name := fmt.Sprintf("%s/fbs-%s", size.name, compression)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for val := range fbsBlob.AllValues(metricID) {
						_ = val // Consume value
					}
				}
			})
		}
	}
}

// =============================================================================
// Decode + Iteration Combined (Realistic Read Workload)
// =============================================================================

// BenchmarkDecodeAndIterateAllText_Mebo measures the combined time to decode and iterate through all text metrics
// This reflects the realistic use case: decode blob once, then iterate through all data
func BenchmarkDecodeAndIterateAllText_Mebo(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)
		metricIDs := getAllTextMetricIDs(testData)

		for _, enc := range textMeboEncodings {
			data, err := createMeboTextBlobBench(testData, enc.tsEncoding, enc.compression)
			if err != nil {
				b.Fatal(err)
			}

			name := fmt.Sprintf("%s/%s", size.name, enc.name)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					// Decode blob (one-time cost)
					decoded, err := decodeMeboTextBlobBench(data)
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

// BenchmarkDecodeAndIterateAllText_FBS measures combined decode + iteration for FlatBuffers text
func BenchmarkDecodeAndIterateAllText_FBS(b *testing.B) {
	for _, size := range benchmarkSizes {
		cfg := DefaultTestDataConfig(numTextMetrics, size.points)
		testData := GenerateTextTestData(cfg)
		metricIDs := getAllTextMetricIDs(testData)

		for _, compression := range fbsCompressions {
			fbsBlob, err := EncodeTextFBS(testData, compression)
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
