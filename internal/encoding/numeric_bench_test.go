package encoding

import (
	"testing"

	"github.com/arloliu/mebo/endian"
)

// Benchmark data sizes representing different use cases
var benchmarkSizes = []struct {
	name string
	size int
}{
	{"10_values", 10},       // Typical metric (design spec: 10 points)
	{"100_values", 100},     // Medium batch
	{"1000_values", 1000},   // Large batch
	{"10000_values", 10000}, // Very large batch
}

// Generate test data with realistic float64 values
func generateFloatValues(n int) []float64 {
	values := make([]float64, n)
	for i := range values {
		// Generate realistic time-series data (temperature, prices, etc.)
		values[i] = 20.5 + float64(i)*0.1 + float64(i%10)*0.01
	}

	return values
}

// Benchmark Write method (single float64 value)
func BenchmarkValueRawEncoderWrite(b *testing.B) {
	testValue := 3.14159265359

	b.Run("Write", func(b *testing.B) {
		encoder := NewNumericRawEncoder(endian.GetLittleEndianEngine())
		b.ResetTimer()
		b.ReportAllocs()
		for b.Loop() {
			encoder.Reset()
			encoder.Write(testValue)
		}
	})
}

// Benchmark WriteSlice method (slice of float64 values)
func BenchmarkValueRawEncoderrWriteSlice(b *testing.B) {
	for _, size := range benchmarkSizes {
		b.Run(size.name, func(b *testing.B) {
			encoder := NewNumericRawEncoder(endian.GetLittleEndianEngine())
			values := generateFloatValues(size.size)

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				encoder.Reset()
				encoder.WriteSlice(values)
				_ = encoder.Bytes() // Force evaluation
			}
		})
	}
}

// Benchmark pool reuse pattern (realistic usage: create, encode, finish, repeat)
func BenchmarkNumericRawEncoder_PoolReuse(b *testing.B) {
	for _, size := range benchmarkSizes {
		b.Run(size.name, func(b *testing.B) {
			values := generateFloatValues(size.size)

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				// Realistic pattern: Get from pool, use, return to pool
				encoder := NewNumericRawEncoder(endian.GetLittleEndianEngine())
				encoder.WriteSlice(values)
				_ = encoder.Bytes()
				encoder.Finish() // Returns buffer to pool
			}
		})
	}
}

// Benchmark repeated Write calls (amortized growth test)
func BenchmarkNumericRawEncoder_RepeatedWrites(b *testing.B) {
	for _, size := range benchmarkSizes {
		b.Run(size.name, func(b *testing.B) {
			values := generateFloatValues(size.size)

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				encoder := NewNumericRawEncoder(endian.GetLittleEndianEngine())
				// Write values one by one to test amortized growth
				for _, v := range values {
					encoder.Write(v)
				}
				_ = encoder.Bytes()
				encoder.Finish()
			}
		})
	}
}

// Benchmark Write vs WriteSlice comparison
func BenchmarkNumericRawEncoder_WriteVsWriteSlice(b *testing.B) {
	testSizes := []int{10, 100, 1000}

	for _, size := range testSizes {
		values := generateFloatValues(size)

		b.Run("Write_"+benchmarkSizes[0].name, func(b *testing.B) {
			if size != 10 {
				return
			}
			b.ReportAllocs()
			for b.Loop() {
				encoder := NewNumericRawEncoder(endian.GetLittleEndianEngine())
				for _, v := range values {
					encoder.Write(v)
				}
				_ = encoder.Bytes()
				encoder.Finish()
			}
		})

		b.Run("WriteSlice_"+benchmarkSizes[0].name, func(b *testing.B) {
			if size != 10 {
				return
			}
			b.ReportAllocs()
			for b.Loop() {
				encoder := NewNumericRawEncoder(endian.GetLittleEndianEngine())
				encoder.WriteSlice(values)
				_ = encoder.Bytes()
				encoder.Finish()
			}
		})
	}
}

// Benchmark memory profile (typical use case: 150 metrics × 10 points)
func BenchmarkNumericRawEncoder_RealWorldPattern(b *testing.B) {
	const numMetrics = 150
	const pointsPerMetric = 10

	// Pre-generate data for all metrics
	metricsData := make([][]float64, numMetrics)
	for i := range metricsData {
		metricsData[i] = generateFloatValues(pointsPerMetric)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Encode all metrics (realistic pattern)
		for _, values := range metricsData {
			encoder := NewNumericRawEncoder(endian.GetLittleEndianEngine())
			encoder.WriteSlice(values)
			_ = encoder.Bytes()
			encoder.Finish()
		}
	}
}
