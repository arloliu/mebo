package compress

import (
	"fmt"
	"testing"

	"github.com/arloliu/mebo/format"
)

// generateBenchmarkData creates test data for benchmarks
func generateBenchmarkData(size int, compressibility string) []byte {
	data := make([]byte, size)

	switch compressibility {
	case "highly_compressible":
		// All zeros - maximum compression
		// data already initialized to zeros
	case "compressible":
		// Repeated pattern - good compression
		pattern := []byte("Time series data with timestamp 1234567890 and value 3.14159")
		for i := range data {
			data[i] = pattern[i%len(pattern)]
		}
	case "semi_compressible":
		// Semi-random data - moderate compression
		for i := range data {
			if i%100 < 50 {
				data[i] = byte(i % 256)
			} else {
				data[i] = byte((i*7 + i*i) % 256)
			}
		}
	default:
		// Default to incompressible
		for i := range data {
			data[i] = byte((i*31 + i*i*7 + i*i*i*3) % 256)
		}
	}

	return data
}

// Benchmark NoOpCompressor to demonstrate its performance
func BenchmarkNoOpCompressor_Compress(b *testing.B) {
	compressor := NewNoOpCompressor()

	benchSizes := []int{1024, 4096, 16384, 65536} // 1KB, 4KB, 16KB, 64KB

	for _, size := range benchSizes {
		data := make([]byte, size)
		// Fill with some test data
		for i := range data {
			data[i] = byte(i % 256)
		}

		b.Run(fmt.Sprintf("%dKB", size/1024), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ResetTimer()

			for b.Loop() {
				_, err := compressor.Compress(data)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkNoOpCompressor_Decompress(b *testing.B) {
	compressor := NewNoOpCompressor()

	benchSizes := []int{1024, 4096, 16384, 65536} // 1KB, 4KB, 16KB, 64KB

	for _, size := range benchSizes {
		data := make([]byte, size)
		// Fill with some test data
		for i := range data {
			data[i] = byte(i % 256)
		}

		b.Run(fmt.Sprintf("%dKB", size/1024), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ResetTimer()

			for b.Loop() {
				_, err := compressor.Decompress(data)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAllCodecs_Compress benchmarks compression for all codecs with various data patterns
func BenchmarkAllCodecs_Compress(b *testing.B) {
	sizes := []int{
		1024,    // 1 KB
		16384,   // 16 KB
		65536,   // 64 KB
		262144,  // 256 KB
		1048576, // 1 MB
	}

	compressibilities := []string{
		"highly_compressible",
		"compressible",
		"semi_compressible",
		"incompressible",
	}

	codecs := getAllCodecs()

	for codecName, codec := range codecs {
		b.Run(codecName, func(b *testing.B) {
			for _, size := range sizes {
				for _, comp := range compressibilities {
					testName := fmt.Sprintf("%dKB_%s", size/1024, comp)
					b.Run(testName, func(b *testing.B) {
						data := generateBenchmarkData(size, comp)

						b.ResetTimer()
						b.ReportAllocs()
						b.SetBytes(int64(len(data)))

						for b.Loop() {
							_, err := codec.Compress(data)
							if err != nil {
								b.Fatal(err)
							}
						}
					})
				}
			}
		})
	}
}

// BenchmarkAllCodecs_Decompress benchmarks decompression for all codecs
func BenchmarkAllCodecs_Decompress(b *testing.B) {
	sizes := []int{
		1024,    // 1 KB
		16384,   // 16 KB
		65536,   // 64 KB
		262144,  // 256 KB
		1048576, // 1 MB
	}

	compressibilities := []string{
		"highly_compressible",
		"compressible",
		"semi_compressible",
		"incompressible",
	}

	codecs := getAllCodecs()

	for codecName, codec := range codecs {
		b.Run(codecName, func(b *testing.B) {
			for _, size := range sizes {
				for _, comp := range compressibilities {
					testName := fmt.Sprintf("%dKB_%s", size/1024, comp)
					b.Run(testName, func(b *testing.B) {
						data := generateBenchmarkData(size, comp)

						// Pre-compress the data
						compressed, err := codec.Compress(data)
						if err != nil {
							b.Fatal(err)
						}

						b.ResetTimer()
						b.ReportAllocs()
						b.SetBytes(int64(len(data)))

						for b.Loop() {
							_, err := codec.Decompress(compressed)
							if err != nil {
								b.Fatal(err)
							}
						}
					})
				}
			}
		})
	}
}

// BenchmarkAllCodecs_RoundTrip benchmarks full compress/decompress cycle
func BenchmarkAllCodecs_RoundTrip(b *testing.B) {
	sizes := []int{
		1024,    // 1 KB
		16384,   // 16 KB
		65536,   // 64 KB
		262144,  // 256 KB
		1048576, // 1 MB
	}

	compressibilities := []string{
		"highly_compressible",
		"compressible",
		"semi_compressible",
		"incompressible",
	}

	codecs := getAllCodecs()

	for codecName, codec := range codecs {
		b.Run(codecName, func(b *testing.B) {
			for _, size := range sizes {
				for _, comp := range compressibilities {
					testName := fmt.Sprintf("%dKB_%s", size/1024, comp)
					b.Run(testName, func(b *testing.B) {
						data := generateBenchmarkData(size, comp)

						b.ResetTimer()
						b.ReportAllocs()
						b.SetBytes(int64(len(data)))

						for b.Loop() {
							compressed, err := codec.Compress(data)
							if err != nil {
								b.Fatal(err)
							}
							_, err = codec.Decompress(compressed)
							if err != nil {
								b.Fatal(err)
							}
						}
					})
				}
			}
		})
	}
}

// BenchmarkAllCodecs_CompressionRatio benchmarks and reports compression ratios
func BenchmarkAllCodecs_CompressionRatio(b *testing.B) {
	size := 1048576 // 1 MB

	compressibilities := []string{
		"highly_compressible",
		"compressible",
		"semi_compressible",
		"incompressible",
	}

	codecs := getAllCodecs()

	for codecName, codec := range codecs {
		b.Run(codecName, func(b *testing.B) {
			for _, comp := range compressibilities {
				b.Run(comp, func(b *testing.B) {
					data := generateBenchmarkData(size, comp)

					// Measure compression once to report ratio
					compressed, err := codec.Compress(data)
					if err != nil {
						b.Fatal(err)
					}

					ratio := float64(len(compressed)) / float64(len(data)) * 100
					b.ReportMetric(ratio, "ratio%")
					b.ReportMetric(float64(len(compressed)), "compressed_bytes")

					b.ResetTimer()
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))

					for b.Loop() {
						_, err := codec.Compress(data)
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

// BenchmarkAllCodecs_SmallPayloads benchmarks small payloads typical in time-series
func BenchmarkAllCodecs_SmallPayloads(b *testing.B) {
	// Small sizes typical for time-series metric data
	sizes := []int{
		64,   // 64 bytes
		128,  // 128 bytes
		256,  // 256 bytes
		512,  // 512 bytes
		1024, // 1 KB
	}

	codecs := getAllCodecs()

	for codecName, codec := range codecs {
		b.Run(codecName, func(b *testing.B) {
			for _, size := range sizes {
				testName := fmt.Sprintf("%d_bytes", size)
				b.Run(testName, func(b *testing.B) {
					data := generateBenchmarkData(size, "compressible")

					b.ResetTimer()
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))

					for b.Loop() {
						compressed, err := codec.Compress(data)
						if err != nil {
							b.Fatal(err)
						}
						_, err = codec.Decompress(compressed)
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

// BenchmarkAllCodecs_Parallel benchmarks parallel compression performance
func BenchmarkAllCodecs_Parallel(b *testing.B) {
	size := 65536 // 64 KB
	data := generateBenchmarkData(size, "compressible")

	codecs := getAllCodecs()

	for codecName, codec := range codecs {
		b.Run(codecName+"_Compress", func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, err := codec.Compress(data)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		})

		b.Run(codecName+"_Decompress", func(b *testing.B) {
			compressed, err := codec.Compress(data)
			if err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, err := codec.Decompress(compressed)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// ==============================================================================
// Benchmark Data Generation
// ==============================================================================

// generateTestData creates test data of specified size with some compressibility.
// The data has patterns similar to time-series data (repeated structures, delta patterns).
func generateTestData(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		// Pattern that compresses well (simulates delta-encoded timestamps)
		data[i] = byte(i % 256)
	}

	return data
}

// ==============================================================================
// Zstd Pooling Benchmarks
// ==============================================================================

func BenchmarkZstdCompress(b *testing.B) {
	sizes := []int{
		1 * 1024,   // 1KB - small payload
		8 * 1024,   // 8KB - typical timestamp payload
		64 * 1024,  // 64KB - large payload
		512 * 1024, // 512KB - very large payload
	}

	for _, size := range sizes {
		data := generateTestData(size)
		compressor := NewZstdCompressor()

		b.Run(formatSize(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for b.Loop() {
				_, _ = compressor.Compress(data)
			}
		})
	}
}

func BenchmarkZstdDecompress(b *testing.B) {
	sizes := []int{
		1 * 1024,   // 1KB
		8 * 1024,   // 8KB - typical for 150 metrics × 10 points
		64 * 1024,  // 64KB
		512 * 1024, // 512KB
	}

	for _, size := range sizes {
		data := generateTestData(size)
		compressor := NewZstdCompressor()
		compressed, _ := compressor.Compress(data)

		b.Run(formatSize(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(compressed)))
			b.ResetTimer()

			for b.Loop() {
				_, _ = compressor.Decompress(compressed)
			}
		})
	}
}

// BenchmarkZstdDecompress_Sequential simulates real-world usage:
// decoding many blobs sequentially (pool reuse scenario).
func BenchmarkZstdDecompress_Sequential(b *testing.B) {
	// Simulate 150 metrics with 10 points each (8 bytes per point)
	// Total: 150 * 10 * 8 = 12KB per payload
	const payloadSize = 12 * 1024
	data := generateTestData(payloadSize)
	compressor := NewZstdCompressor()
	compressed, _ := compressor.Compress(data)

	b.Run("150metrics", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(compressed)))
		b.ResetTimer()

		// Simulate decoding 150 blobs
		for b.Loop() {
			for range 150 {
				_, _ = compressor.Decompress(compressed)
			}
		}
	})
}

// ==============================================================================
// LZ4 Pooling Benchmarks
// ==============================================================================

func BenchmarkLZ4Compress(b *testing.B) {
	sizes := []int{
		1 * 1024,
		8 * 1024,
		64 * 1024,
		512 * 1024,
	}

	for _, size := range sizes {
		data := generateTestData(size)
		compressor := NewLZ4Compressor()

		b.Run(formatSize(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for b.Loop() {
				_, _ = compressor.Compress(data)
			}
		})
	}
}

func BenchmarkLZ4Decompress(b *testing.B) {
	sizes := []int{
		1 * 1024,
		8 * 1024,
		64 * 1024,
		512 * 1024,
	}

	for _, size := range sizes {
		data := generateTestData(size)
		compressor := NewLZ4Compressor()
		compressed, _ := compressor.Compress(data)

		b.Run(formatSize(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(compressed)))
			b.ResetTimer()

			for b.Loop() {
				_, _ = compressor.Decompress(compressed)
			}
		})
	}
}

// ==============================================================================
// S2 Benchmarks (baseline - no pooling needed, stateless functions)
// ==============================================================================

func BenchmarkS2Compress(b *testing.B) {
	sizes := []int{
		1 * 1024,
		8 * 1024,
		64 * 1024,
		512 * 1024,
	}

	for _, size := range sizes {
		data := generateTestData(size)
		compressor := NewS2Compressor()

		b.Run(formatSize(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for b.Loop() {
				_, _ = compressor.Compress(data)
			}
		})
	}
}

func BenchmarkS2Decompress(b *testing.B) {
	sizes := []int{
		1 * 1024,
		8 * 1024,
		64 * 1024,
		512 * 1024,
	}

	for _, size := range sizes {
		data := generateTestData(size)
		compressor := NewS2Compressor()
		compressed, _ := compressor.Compress(data)

		b.Run(formatSize(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(compressed)))
			b.ResetTimer()

			for b.Loop() {
				_, _ = compressor.Decompress(compressed)
			}
		})
	}
}

// ==============================================================================
// Comparison Benchmarks (All Codecs)
// ==============================================================================

func BenchmarkCodecComparison_Compress(b *testing.B) {
	const size = 8 * 1024 // 8KB - typical payload
	data := generateTestData(size)

	codecs := []struct {
		name string
		typ  format.CompressionType
	}{
		{"NoOp", format.CompressionNone},
		{"LZ4", format.CompressionLZ4},
		{"S2", format.CompressionS2},
		{"Zstd", format.CompressionZstd},
	}

	for _, codec := range codecs {
		c, _ := CreateCodec(codec.typ, "test")

		b.Run(codec.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for b.Loop() {
				_, _ = c.Compress(data)
			}
		})
	}
}

func BenchmarkCodecComparison_Decompress(b *testing.B) {
	const size = 8 * 1024 // 8KB - typical payload
	data := generateTestData(size)

	codecs := []struct {
		name string
		typ  format.CompressionType
	}{
		{"NoOp", format.CompressionNone},
		{"LZ4", format.CompressionLZ4},
		{"S2", format.CompressionS2},
		{"Zstd", format.CompressionZstd},
	}

	for _, codec := range codecs {
		c, _ := CreateCodec(codec.typ, "test")
		compressed, _ := c.Compress(data)

		b.Run(codec.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(compressed)))
			b.ResetTimer()

			for b.Loop() {
				_, _ = c.Decompress(compressed)
			}
		})
	}
}

// ==============================================================================
// Pool Effectiveness Benchmarks
// ==============================================================================

// BenchmarkZstdDecompress_Parallel tests pool behavior under concurrent load.
func BenchmarkZstdDecompress_Parallel(b *testing.B) {
	const size = 8 * 1024
	data := generateTestData(size)
	compressor := NewZstdCompressor()
	compressed, _ := compressor.Compress(data)

	b.ReportAllocs()
	b.SetBytes(int64(len(compressed)))
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = compressor.Decompress(compressed)
		}
	})
}

// BenchmarkLZ4Compress_Parallel tests LZ4 pool behavior under concurrent load.
func BenchmarkLZ4Compress_Parallel(b *testing.B) {
	const size = 8 * 1024
	data := generateTestData(size)
	compressor := NewLZ4Compressor()

	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = compressor.Compress(data)
		}
	})
}

func formatSize(size int) string {
	if size < 1024 {
		return string(rune(size)) + "B"
	}

	if size < 1024*1024 {
		return string(rune(size/1024)) + "KB"
	}

	return string(rune(size/(1024*1024))) + "MB"
}
