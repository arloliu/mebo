package encoding

import (
	"fmt"
	"testing"

	"github.com/arloliu/mebo/endian"
)

// BenchmarkTimestampRawEncoder_Write benchmarks single Write operations
func BenchmarkTimestampRawEncoder_Write(b *testing.B) {
	encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
	defer encoder.Finish()

	timestamp := int64(1609459200000) // 2021-01-01 00:00:00 UTC

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		encoder.Write(timestamp)
	}
}

// BenchmarkTimestampRawEncoder_WriteSlice benchmarks batch WriteSlice operations
func BenchmarkTimestampRawEncoder_WriteSlice(b *testing.B) {
	// Test different slice sizes
	sizes := []struct {
		name  string
		count int
	}{
		{"10_timestamps", 10},
		{"100_timestamps", 100},
		{"1000_timestamps", 1000},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
			defer encoder.Finish()

			// Generate test data
			timestamps := make([]int64, size.count)
			base := int64(1609459200000) // 2021-01-01 00:00:00 UTC
			for i := range timestamps {
				timestamps[i] = base + int64(i*1000)
			}

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				encoder.WriteSlice(timestamps)
				encoder.Finish()
			}
		})
	}
}

// BenchmarkTimestampRawEncoder_Reset_vs_New compares Reset() vs creating new encoder
func BenchmarkTimestampRawEncoder_Reset_vs_New(b *testing.B) {
	timestamps := []int64{
		1609459200000,
		1609459201000,
		1609459202000,
		1609459203000,
		1609459204000,
	}

	b.Run("with_reset", func(b *testing.B) {
		encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
		defer encoder.Finish()

		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			for _, ts := range timestamps {
				encoder.Write(ts)
			}
			_ = encoder.Bytes()
			encoder.Finish()
		}
	})

	b.Run("with_new", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
			for _, ts := range timestamps {
				encoder.Write(ts)
			}
			_ = encoder.Bytes()
			encoder.Finish()
		}
	})
}

// BenchmarkTimestampRawEncoder_BufferGrowth benchmarks buffer growth patterns
func BenchmarkTimestampRawEncoder_BufferGrowth(b *testing.B) {
	sizes := []struct {
		name  string
		count int
	}{
		{"grow_from_8_to_64", 8},       // Stays within initial capacity
		{"grow_from_8_to_128", 16},     // Triggers first growth
		{"grow_from_8_to_1024", 128},   // Multiple growths
		{"grow_from_8_to_10000", 1250}, // Large growth
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
			defer encoder.Finish()

			timestamps := make([]int64, size.count)
			base := int64(1609459200000)
			for i := range timestamps {
				timestamps[i] = base + int64(i*1000)
			}

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				encoder.WriteSlice(timestamps)
				encoder.Finish()
			}
		})
	}
}

// BenchmarkTimestampRawEncoder_Bytes benchmarks the Bytes() method
func BenchmarkTimestampRawEncoder_Bytes(b *testing.B) {
	encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
	defer encoder.Finish()

	// Write some data
	for i := 0; i < 100; i++ {
		encoder.Write(int64(1609459200000 + i*1000))
	}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		_ = encoder.Bytes()
	}
}

// BenchmarkTimestampRawDecoder_All benchmarks decoding all timestamps
func BenchmarkTimestampRawDecoder_All(b *testing.B) {
	sizes := []struct {
		name  string
		count int
	}{
		{"10_timestamps", 10},
		{"100_timestamps", 100},
		{"1000_timestamps", 1000},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			// Prepare test data
			encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
			defer encoder.Finish()

			timestamps := make([]int64, size.count)
			base := int64(1609459200000)
			for i := range timestamps {
				timestamps[i] = base + int64(i*1000)
			}
			encoder.WriteSlice(timestamps)
			data := encoder.Bytes()

			// Create decoder
			decoder := NewTimestampRawDecoder(endian.GetLittleEndianEngine())

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				for ts := range decoder.All(data, size.count) {
					_ = ts // Consume timestamp
				}
			}
		})
	}
}

// BenchmarkTimestampRawDecoder_At benchmarks random access
func BenchmarkTimestampRawDecoder_At(b *testing.B) {
	// Prepare test data
	count := 1000
	encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
	defer encoder.Finish()

	timestamps := make([]int64, count)
	base := int64(1609459200000)
	for i := range timestamps {
		timestamps[i] = base + int64(i*1000)
	}
	encoder.WriteSlice(timestamps)
	data := encoder.Bytes()

	// Create decoder
	decoder := NewTimestampRawDecoder(endian.GetLittleEndianEngine())

	b.ResetTimer()
	b.ReportAllocs()

	idx := 0
	for b.Loop() {
		_, _ = decoder.At(data, idx, count)
		idx = (idx + 1) % count
	}
}

// BenchmarkTimestampRawEncoder_vs_Delta compares raw vs delta encoding
func BenchmarkTimestampRawEncoder_vs_Delta(b *testing.B) {
	count := 100
	timestamps := make([]int64, count)
	base := int64(1609459200000)
	for i := range timestamps {
		timestamps[i] = base + int64(i*1000) // Regular interval
	}

	b.Run("raw_encoding", func(b *testing.B) {
		encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
		defer encoder.Finish()

		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			encoder.WriteSlice(timestamps)
			_ = encoder.Bytes()
			encoder.Finish()
		}
	})

	b.Run("delta_encoding", func(b *testing.B) {
		encoder := NewTimestampDeltaEncoder()
		defer encoder.Finish()

		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			encoder.WriteSlice(timestamps)
			_ = encoder.Bytes()
			encoder.Finish()
		}
	})
}

// BenchmarkTimestampRawEncoder_RepeatedWrite benchmarks calling Write() repeatedly
// to test the amortized buffer growth strategy
func BenchmarkTimestampRawEncoder_RepeatedWrite(b *testing.B) {
	counts := []struct {
		name  string
		count int
	}{
		{"write_10_times", 10},
		{"write_100_times", 100},
		{"write_1000_times", 1000},
		{"write_10000_times", 10000},
	}

	for _, tc := range counts {
		b.Run(tc.name, func(b *testing.B) {
			base := int64(1609459200000)

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
				for i := range tc.count {
					encoder.Write(base + int64(i*1000))
				}
				_ = encoder.Bytes()
				encoder.Finish()
			}
		})
	}
}

// BenchmarkTimestampDeltaEncoder_RepeatedWrite benchmarks calling Write() repeatedly
// to test the amortized buffer growth strategy
func BenchmarkTimestampDeltaEncoder_RepeatedWrite(b *testing.B) {
	counts := []struct {
		name  string
		count int
	}{
		{"write_10_times", 10},
		{"write_100_times", 100},
		{"write_1000_times", 1000},
		{"write_10000_times", 10000},
	}

	for _, tc := range counts {
		b.Run(tc.name, func(b *testing.B) {
			base := int64(1609459200000)

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				encoder := NewTimestampDeltaEncoder()
				for i := range tc.count {
					encoder.Write(base + int64(i*1000))
				}
				_ = encoder.Bytes()
				encoder.Finish()
			}
		})
	}
}

// BenchmarkTimestampRawEncoder_RepeatedWrite_vs_WriteSlice compares repeated Write vs single WriteSlice
func BenchmarkTimestampRawEncoder_RepeatedWrite_vs_WriteSlice(b *testing.B) {
	count := 1000
	base := int64(1609459200000)

	b.Run("repeated_write", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
			for i := range count {
				encoder.Write(base + int64(i*1000))
			}
			_ = encoder.Bytes()
			encoder.Finish()
		}
	})

	b.Run("write_slice", func(b *testing.B) {
		timestamps := make([]int64, count)
		for i := range timestamps {
			timestamps[i] = base + int64(i*1000)
		}

		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
			encoder.WriteSlice(timestamps)
			_ = encoder.Bytes()
			encoder.Finish()
		}
	})
}

// BenchmarkTimestampDeltaEncoder_RepeatedWrite_vs_WriteSlice compares repeated Write vs single WriteSlice
func BenchmarkTimestampDeltaEncoder_RepeatedWrite_vs_WriteSlice(b *testing.B) {
	count := 1000
	base := int64(1609459200000)

	b.Run("repeated_write", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			encoder := NewTimestampDeltaEncoder()
			for i := range count {
				encoder.Write(base + int64(i*1000))
			}
			_ = encoder.Bytes()
			encoder.Finish()
		}
	})

	b.Run("write_slice", func(b *testing.B) {
		timestamps := make([]int64, count)
		for i := range timestamps {
			timestamps[i] = base + int64(i*1000)
		}

		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			encoder := NewTimestampDeltaEncoder()
			encoder.WriteSlice(timestamps)
			_ = encoder.Bytes()
			encoder.Finish()
		}
	})
}

// BenchmarkTimestampRawEncoder_GrowthPattern shows the allocation pattern
func BenchmarkTimestampRawEncoder_GrowthPattern(b *testing.B) {
	counts := []int{8, 9, 64, 65, 128, 512, 513, 1000}

	for _, count := range counts {
		b.Run(fmt.Sprintf("write_%d_times", count), func(b *testing.B) {
			base := int64(1609459200000)

			b.ReportAllocs()

			for b.Loop() {
				encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
				for i := range count {
					encoder.Write(base + int64(i*1000))
				}
				_ = encoder.Bytes()
				encoder.Finish()
			}
		})
	}
}

// BenchmarkTimestampRawEncoder_RealWorldPattern benchmarks realistic usage pattern:
// 150 metrics × 10 timestamps each (as per design spec: many metrics, few points)
func BenchmarkTimestampRawEncoder_RealWorldPattern(b *testing.B) {
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
			encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
			encoder.WriteSlice(timestamps)
			_ = encoder.Bytes()
			encoder.Finish()
		}
	}
}
