package encoding

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/arloliu/mebo/endian"
)

// === TimestampDeltaEncoder Benchmarks ===

func BenchmarkTimestampDeltaEncoder_Write(b *testing.B) {
	b.Run("Sequential", func(b *testing.B) {
		start := time.Now().UnixMicro()
		encoder := NewTimestampDeltaEncoder()
		b.ResetTimer()

		for b.Loop() {
			encoder.Write(start + int64(b.N)*1000000)
		}
	})

	b.Run("Random", func(b *testing.B) {
		encoder := NewTimestampDeltaEncoder()
		b.ResetTimer()

		for b.Loop() {
			// Generate pseudo-random timestamp based on iteration
			timestamp := time.Now().UnixMicro() + int64(b.N*12345)
			encoder.Write(timestamp)
		}
	})
}

func BenchmarkTimestampDeltaEncoder_WriteSlice(b *testing.B) {
	generateTimestamps := func(count int, interval int64) []int64 {
		timestamps := make([]int64, count)
		start := time.Now().UnixMicro()
		for i := range timestamps {
			timestamps[i] = start + int64(i)*interval
		}

		return timestamps
	}

	benchSizes := []int{10, 100, 1000, 10000}

	for _, size := range benchSizes {
		b.Run("Sequential", func(b *testing.B) {
			b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
				timestamps := generateTimestamps(size, 1000000) // 1-second intervals
				b.ResetTimer()

				for b.Loop() {
					encoder := NewTimestampDeltaEncoder()
					encoder.WriteSlice(timestamps)
					_ = encoder.Bytes()
				}
			})
		})

		b.Run("Irregular", func(b *testing.B) {
			b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
				// Generate irregular intervals
				timestamps := make([]int64, size)
				start := time.Now().UnixMicro()
				current := start
				for i := range timestamps {
					timestamps[i] = current
					// Irregular intervals: 1-10 seconds
					current += int64((i%10 + 1) * 1000000)
				}
				b.ResetTimer()

				for b.Loop() {
					encoder := NewTimestampDeltaEncoder()
					encoder.WriteSlice(timestamps)
					_ = encoder.Bytes()
				}
			})
		})
	}
}

func BenchmarkTimestampDeltaEncoder_CompressionRatio(b *testing.B) {
	// This benchmark measures compression efficiency, not speed
	sizes := []int{100, 1000, 10000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			// Generate sequential timestamps (best case for compression)
			timestamps := make([]int64, size)
			start := time.Now().UnixMicro()
			for i := range timestamps {
				timestamps[i] = start + int64(i)*1000000 // 1-second intervals
			}

			encoder := NewTimestampDeltaEncoder()
			encoder.WriteSlice(timestamps)

			compressedSize := encoder.Size()
			rawSize := len(timestamps) * 8
			ratio := float64(compressedSize) / float64(rawSize)

			b.ReportMetric(float64(compressedSize), "compressed-bytes")
			b.ReportMetric(float64(rawSize), "raw-bytes")
			b.ReportMetric(ratio, "compression-ratio")

			// The actual benchmark loop (for timing measurement)
			for b.Loop() {
				encoder := NewTimestampDeltaEncoder()
				encoder.WriteSlice(timestamps)
				_ = encoder.Size()
			}
		})
	}
}

func BenchmarkTimestampDeltaEncoder_Reset_vs_New(b *testing.B) {
	timestamps := make([]int64, 100)
	start := time.Now().UnixMicro()
	for i := range timestamps {
		timestamps[i] = start + int64(i)*1000000
	}

	b.Run("ReuseWithReset", func(b *testing.B) {
		encoder := NewTimestampDeltaEncoder()
		b.ResetTimer()

		for b.Loop() {
			encoder.Reset()
			encoder.WriteSlice(timestamps)
			_ = encoder.Bytes()
		}
	})

	b.Run("CreateNew", func(b *testing.B) {
		for b.Loop() {
			encoder := NewTimestampDeltaEncoder()
			encoder.WriteSlice(timestamps)
			_ = encoder.Bytes()
		}
	})
}

// === TimestampDeltaDecoder Benchmarks ===

func BenchmarkTimestampDeltaDecoder_All(b *testing.B) {
	// Generate test data
	timestamps := make([]int64, 1000)
	start := time.Now().UnixMicro()
	for i := range timestamps {
		timestamps[i] = start + int64(i)*1000000 // 1-second intervals
	}

	// Encode once
	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(timestamps)
	encodedData := encoder.Bytes()

	// Benchmark decoding
	decoder := NewTimestampDeltaDecoder()
	b.ResetTimer()

	for b.Loop() {
		count := 0
		for range decoder.All(encodedData, len(timestamps)) {
			count++
		}
		if count != len(timestamps) {
			b.Fatalf("Expected %d timestamps, got %d", len(timestamps), count)
		}
	}
}

func BenchmarkTimestampDeltaDecoder_vs_Slice(b *testing.B) {
	// Generate test data
	timestamps := make([]int64, 1000)
	start := time.Now().UnixMicro()
	for i := range timestamps {
		timestamps[i] = start + int64(i)*1000000
	}

	// Encode once
	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(timestamps)
	encodedData := encoder.Bytes()

	b.Run("Iterator", func(b *testing.B) {
		decoder := NewTimestampDeltaDecoder()
		for b.Loop() {
			count := 0
			for range decoder.All(encodedData, len(timestamps)) {
				count++
			}
		}
	})

	b.Run("SliceAllocation", func(b *testing.B) {
		decoder := NewTimestampDeltaDecoder()
		for b.Loop() {
			result := slices.Collect(decoder.All(encodedData, len(timestamps)))
			_ = result
		}
	})
}

// === TimestampDeltaDecoder At Method Benchmarks ===

func BenchmarkTimestampDeltaDecoder_At(b *testing.B) {
	// Generate test data with different sizes
	sizes := []int{10, 100, 1000, 10000}

	for _, size := range sizes {
		// Generate sequential timestamps
		timestamps := make([]int64, size)
		start := time.Now().UnixMicro()
		for i := range timestamps {
			timestamps[i] = start + int64(i)*1000000 // 1-second intervals
		}

		encoder := NewTimestampDeltaEncoder()
		encoder.WriteSlice(timestamps)
		encodedData := encoder.Bytes()
		decoder := NewTimestampDeltaDecoder()

		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			b.Run("FirstIndex", func(b *testing.B) {
				// Benchmark accessing first index (best case)
				for b.Loop() {
					_, ok := decoder.At(encodedData, 0, size)
					if !ok {
						b.Fatal("Failed to get timestamp at index 0")
					}
				}
			})

			b.Run("MiddleIndex", func(b *testing.B) {
				// Benchmark accessing middle index
				middleIndex := size / 2
				for b.Loop() {
					_, ok := decoder.At(encodedData, middleIndex, size)
					if !ok {
						b.Fatalf("Failed to get timestamp at index %d", middleIndex)
					}
				}
			})

			b.Run("LastIndex", func(b *testing.B) {
				// Benchmark accessing last index (worst case)
				lastIndex := size - 1
				for b.Loop() {
					_, ok := decoder.At(encodedData, lastIndex, size)
					if !ok {
						b.Fatalf("Failed to get timestamp at index %d", lastIndex)
					}
				}
			})
		})
	}
}

func BenchmarkTimestampDeltaDecoder_At_vs_All(b *testing.B) {
	// Compare At() vs All() for different access patterns
	size := 1000
	timestamps := make([]int64, size)
	start := time.Now().UnixMicro()
	for i := range timestamps {
		timestamps[i] = start + int64(i)*1000000
	}

	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(timestamps)
	encodedData := encoder.Bytes()
	decoder := NewTimestampDeltaDecoder()

	b.Run("SingleRandomAccess_At", func(b *testing.B) {
		// Benchmark single random access using At()
		targetIndex := size / 2
		for b.Loop() {
			_, ok := decoder.At(encodedData, targetIndex, len(timestamps))
			if !ok {
				b.Fatal("Failed to get timestamp")
			}
		}
	})

	b.Run("SingleRandomAccess_All", func(b *testing.B) {
		// Benchmark single random access using All() (inefficient)
		targetIndex := size / 2
		for b.Loop() {
			i := 0
			for ts := range decoder.All(encodedData, size) {
				if i == targetIndex {
					_ = ts
					break
				}
				i++
			}
		}
	})

	b.Run("MultipleRandomAccess_At", func(b *testing.B) {
		// Benchmark multiple random accesses using At()
		indices := []int{10, 50, 100, 500, 900}
		for b.Loop() {
			for _, index := range indices {
				_, ok := decoder.At(encodedData, index, len(timestamps))
				if !ok {
					b.Fatalf("Failed to get timestamp at index %d", index)
				}
			}
		}
	})

	b.Run("MultipleRandomAccess_All", func(b *testing.B) {
		// Benchmark multiple random accesses using All() (very inefficient)
		indices := []int{10, 50, 100, 500, 900}
		for b.Loop() {
			for _, targetIndex := range indices {
				i := 0
				for ts := range decoder.All(encodedData, size) {
					if i == targetIndex {
						_ = ts
						break
					}
					i++
				}
			}
		}
	})
}

func BenchmarkTimestampDeltaDecoder_At_DataPatterns(b *testing.B) {
	// Test different data patterns that affect decoding performance
	size := 1000
	decoder := NewTimestampDeltaDecoder()
	targetIndex := size / 2

	b.Run("Sequential", func(b *testing.B) {
		// Sequential timestamps (small deltas, fast varint decoding)
		timestamps := make([]int64, size)
		start := time.Now().UnixMicro()
		for i := range timestamps {
			timestamps[i] = start + int64(i)*1000000 // 1-second intervals
		}

		encoder := NewTimestampDeltaEncoder()
		encoder.WriteSlice(timestamps)
		encodedData := encoder.Bytes()
		b.ResetTimer()

		for b.Loop() {
			_, ok := decoder.At(encodedData, targetIndex, len(timestamps))
			if !ok {
				b.Fatal("Failed to get timestamp")
			}
		}
	})

	b.Run("LargeDeltas", func(b *testing.B) {
		// Large irregular deltas (larger varint encoding)
		timestamps := make([]int64, size)
		start := time.Now().UnixMicro()
		current := start
		for i := range timestamps {
			timestamps[i] = current
			// Large irregular intervals: 1 hour to 1 day
			current += int64((3600 + i*3600) * 1000000)
		}

		encoder := NewTimestampDeltaEncoder()
		encoder.WriteSlice(timestamps)
		encodedData := encoder.Bytes()
		b.ResetTimer()

		for b.Loop() {
			_, ok := decoder.At(encodedData, targetIndex, len(timestamps))
			if !ok {
				b.Fatal("Failed to get timestamp")
			}
		}
	})

	b.Run("NegativeDeltas", func(b *testing.B) {
		// Mix of positive and negative deltas
		timestamps := make([]int64, size)
		start := time.Now().UnixMicro()
		current := start
		for i := range timestamps {
			timestamps[i] = current
			// Alternating positive and negative deltas
			if i%2 == 0 {
				current += int64(1000000) // +1 second
			} else {
				current -= int64(500000) // -0.5 second
			}
		}

		encoder := NewTimestampDeltaEncoder()
		encoder.WriteSlice(timestamps)
		encodedData := encoder.Bytes()
		b.ResetTimer()

		for b.Loop() {
			_, ok := decoder.At(encodedData, targetIndex, len(timestamps))
			if !ok {
				b.Fatal("Failed to get timestamp")
			}
		}
	})
}

// BenchmarkTimestampDeltaEncoder_RealWorldPattern benchmarks realistic usage pattern:
// 150 metrics × 10 timestamps each (as per design spec: many metrics, few points)
func BenchmarkTimestampDeltaEncoder_RealWorldPattern(b *testing.B) {
	const numMetrics = 150
	const pointsPerMetric = 10

	// Pre-generate data for all metrics
	metricsData := make([][]int64, numMetrics)
	base := int64(1609459200000000) // 2021-01-01 00:00:00 UTC in microseconds
	for i := range metricsData {
		metricsData[i] = make([]int64, pointsPerMetric)
		for j := range metricsData[i] {
			metricsData[i][j] = base + int64(j)*1000000 // 1-second intervals
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Encode all metrics (realistic pattern)
		for _, timestamps := range metricsData {
			encoder := NewTimestampDeltaEncoder()
			encoder.WriteSlice(timestamps)
			_ = encoder.Bytes()
			encoder.Finish()
		}
	}
}

// === Size Efficiency Benchmarks ===

// BenchmarkTimestampEncodingSize benchmarks the size efficiency for reporting
func BenchmarkTimestampEncodingSize(b *testing.B) {
	engine := endian.GetLittleEndianEngine()

	scenarios := []struct {
		name      string
		generator func(int) []int64
	}{
		{"Regular1s", func(n int) []int64 { return generateTimestampsWithJitter(n, 1_000_000, 0.0) }},
		{"Regular1min", func(n int) []int64 { return generateTimestampsWithJitter(n, 60_000_000, 0.0) }},
		{"Jitter5pct", func(n int) []int64 { return generateTimestampsWithJitter(n, 1_000_000, 0.05) }},
		{"Irregular", generateIrregularTimestamps},
	}

	sizes := []int{100, 250, 1000}

	for _, scenario := range scenarios {
		for _, size := range sizes {
			b.Run(fmt.Sprintf("%s/%dpts", scenario.name, size), func(b *testing.B) {
				timestamps := scenario.generator(size)

				b.Run("DeltaOfDelta", func(b *testing.B) {
					var finalSize int
					for b.Loop() {
						encoder := NewTimestampDeltaEncoder()
						encoder.WriteSlice(timestamps)
						finalSize = encoder.Size()
					}

					rawSize := size * 8
					b.ReportMetric(float64(finalSize), "compressed-bytes")
					b.ReportMetric(float64(rawSize), "raw-bytes")
					b.ReportMetric(float64(finalSize)/float64(size), "bytes-per-ts")
					b.ReportMetric((1.0-float64(finalSize)/float64(rawSize))*100, "savings-pct")
				})

				b.Run("Raw", func(b *testing.B) {
					var finalSize int
					for b.Loop() {
						encoder := NewTimestampRawEncoder(engine)
						encoder.WriteSlice(timestamps)
						finalSize = encoder.Size()
					}

					b.ReportMetric(float64(finalSize), "total-bytes")
					b.ReportMetric(float64(finalSize)/float64(size), "bytes-per-ts")
				})
			})
		}
	}
}

// BenchmarkTimestampEncodingSizeRealWorld benchmarks realistic production scenarios
// following the pattern from BENCHMARK_PLAN.md: 200 metrics × N points
func BenchmarkTimestampEncodingSizeRealWorld(b *testing.B) {
	engine := endian.GetLittleEndianEngine()

	configs := []struct {
		metrics int
		points  int
		desc    string
	}{
		{200, 10, "200metrics×10pts_short-term"},
		{200, 100, "200metrics×100pts_medium-term"},
		{200, 250, "200metrics×250pts_long-term"},
	}

	for _, cfg := range configs {
		b.Run(cfg.desc, func(b *testing.B) {
			// Generate data for all metrics (realistic time-series pattern)
			allMetricsData := make([][]int64, cfg.metrics)
			for i := range allMetricsData {
				allMetricsData[i] = generateTimestampsWithJitter(cfg.points, 1_000_000, 0.05)
			}

			b.Run("DeltaOfDelta", func(b *testing.B) {
				totalSize := 0
				for b.Loop() {
					totalSize = 0
					for _, timestamps := range allMetricsData {
						encoder := NewTimestampDeltaEncoder()
						encoder.WriteSlice(timestamps)
						totalSize += encoder.Size()
						encoder.Finish()
					}
				}

				totalPoints := cfg.metrics * cfg.points
				rawTotalSize := totalPoints * 8
				b.ReportMetric(float64(totalSize), "total-bytes")
				b.ReportMetric(float64(totalSize)/float64(totalPoints), "bytes-per-point")
				b.ReportMetric((1.0-float64(totalSize)/float64(rawTotalSize))*100, "savings-pct")
			})

			b.Run("Raw", func(b *testing.B) {
				totalSize := 0
				for b.Loop() {
					totalSize = 0
					for _, timestamps := range allMetricsData {
						encoder := NewTimestampRawEncoder(engine)
						encoder.WriteSlice(timestamps)
						totalSize += encoder.Size()
						encoder.Finish()
					}
				}

				totalPoints := cfg.metrics * cfg.points
				b.ReportMetric(float64(totalSize), "total-bytes")
				b.ReportMetric(float64(totalSize)/float64(totalPoints), "bytes-per-point")
			})
		})
	}
}

// === Helper Functions for Size Benchmarks ===

func generateTimestampsWithJitter(count int, intervalUs int64, jitterPct float64) []int64 {
	timestamps := make([]int64, count)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro()
	current := start

	for i := range timestamps {
		timestamps[i] = current

		// Apply jitter
		jitter := int64(0)
		if jitterPct > 0 {
			maxJitter := int64(float64(intervalUs) * jitterPct)
			// Use deterministic jitter based on index for reproducible results
			jitter = (int64(i*1234567) % (maxJitter * 2)) - maxJitter
		}

		current += intervalUs + jitter
	}

	return timestamps
}

func generateIrregularTimestamps(count int) []int64 {
	timestamps := make([]int64, count)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro()
	current := start

	for i := range timestamps {
		timestamps[i] = current
		// Irregular intervals: 100ms to 10 seconds
		interval := int64((i%100 + 1) * 100_000) // 100ms increments
		current += interval
	}

	return timestamps
}

func generateAcceleratingTimestamps(count int) []int64 {
	timestamps := make([]int64, count)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro()
	current := start

	for i := range timestamps {
		timestamps[i] = current
		// Accelerating: interval increases linearly
		interval := int64(1_000_000 + i*10_000) // Start at 1s, increase by 10ms each
		current += interval
	}

	return timestamps
}
