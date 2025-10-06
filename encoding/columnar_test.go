package encoding

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGrowBuffer_SufficientCapacity(t *testing.T) {
	buf := make([]byte, 10, 100)
	result := growBuffer(buf, 50)

	// Should return the same buffer since capacity is sufficient
	require.Equal(t, &buf[0], &result[0], "should return same buffer")
	require.Equal(t, 10, len(result))
	require.Equal(t, 100, cap(result))
}

func TestGrowBuffer_SmallBuffer_DefaultGrowth(t *testing.T) {
	buf := make([]byte, 10, 20)
	originalCap := cap(buf)
	result := growBuffer(buf, 50)

	// Should grow by 256 bytes (default for small buffers)
	require.Equal(t, 10, len(result), "length should be preserved")
	require.Greater(t, cap(result), originalCap, "capacity should increase")
	require.GreaterOrEqual(t, cap(result), 10+256, "capacity should grow by at least 256")

	// Data should be preserved
	buf = []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	result = growBuffer(buf, 50)
	require.Equal(t, buf, result[:10], "data should be preserved")
}

func TestGrowBuffer_LargeBuffer_PercentageGrowth(t *testing.T) {
	// Buffer larger than 4KB
	buf := make([]byte, 4000, 5000)
	originalCap := cap(buf)
	result := growBuffer(buf, 2000)

	// Should grow by 25% of capacity (5000 / 4 = 1250)
	// Since required is 2000 > 1250, it should grow by at least 2000
	require.Equal(t, 4000, len(result), "length should be preserved")
	require.Greater(t, cap(result), originalCap, "capacity should increase")
	require.GreaterOrEqual(t, cap(result)-len(result), 2000, "should have at least required capacity")
}

func TestGrowBuffer_GrowByRequiredBytes(t *testing.T) {
	// Case where default growth (512) is less than required bytes
	buf := make([]byte, 10, 20)
	originalCap := cap(buf)
	result := growBuffer(buf, 1000)

	// Should grow by at least requiredBytes
	require.Equal(t, 10, len(result), "length should be preserved")
	require.Greater(t, cap(result), originalCap, "capacity should increase")
	require.GreaterOrEqual(t, cap(result)-len(result), 1000, "should have at least required capacity")
}

func TestGrowBuffer_LargeBuffer_GrowByRequiredBytes(t *testing.T) {
	// Case where 25% growth is less than required bytes
	buf := make([]byte, 4000, 5000)
	originalCap := cap(buf)
	result := growBuffer(buf, 2000)

	// 25% of 5000 = 1250, but we need 2000, so should grow by at least 2000
	require.Equal(t, 4000, len(result), "length should be preserved")
	require.Greater(t, cap(result), originalCap, "capacity should increase")
	require.GreaterOrEqual(t, cap(result)-len(result), 2000, "should have at least required capacity")
}

func TestGrowBuffer_ZeroRequiredBytes(t *testing.T) {
	buf := make([]byte, 10, 20)
	result := growBuffer(buf, 0)

	// Should return same buffer when no bytes required
	require.Equal(t, &buf[0], &result[0], "should return same buffer")
	require.Equal(t, 10, len(result))
	require.Equal(t, 20, cap(result))
}

func TestGrowBuffer_EmptyBuffer(t *testing.T) {
	buf := make([]byte, 0)
	result := growBuffer(buf, 100)

	// Should grow empty buffer
	require.Equal(t, 0, len(result), "length should remain 0")
	require.GreaterOrEqual(t, cap(result), 100, "should have sufficient capacity")
}

func TestGrowBuffer_PreservesData(t *testing.T) {
	// Create buffer with test data
	buf := make([]byte, 0, 10)
	testData := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	buf = append(buf, testData...)

	// Grow the buffer
	result := growBuffer(buf, 100)

	// Data should be preserved
	require.Equal(t, testData, result[:len(testData)], "data should be preserved after growth")
}

// Benchmark to ensure growBuffer is efficient
func BenchmarkGrowBuffer_NoGrowth(b *testing.B) {
	buf := make([]byte, 10, 1000)

	b.ResetTimer()
	for b.Loop() {
		_ = growBuffer(buf, 100)
	}
}

func BenchmarkGrowBuffer_SmallGrowth(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		buf := make([]byte, 10, 20)
		_ = growBuffer(buf, 50)
	}
}

func BenchmarkGrowBuffer_LargeGrowth(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		buf := make([]byte, 4000, 5000)
		_ = growBuffer(buf, 2000)
	}
}

// Tests for ensureBufferCapacity

func TestEnsureBufferCapacity_SufficientCapacity(t *testing.T) {
	buf := make([]byte, 10, 100)
	result := ensureBufferCapacity(buf, 50)

	// Should return the same buffer since capacity is sufficient
	require.Equal(t, &buf[0], &result[0], "should return same buffer")
	require.Equal(t, 10, len(result))
	require.Equal(t, 100, cap(result))
}

func TestEnsureBufferCapacity_InsufficientCapacity(t *testing.T) {
	buf := make([]byte, 10, 20)
	result := ensureBufferCapacity(buf, 50)

	// Should allocate new buffer with exact capacity
	require.Equal(t, 10, len(result), "length should be preserved")
	require.Equal(t, 60, cap(result), "capacity should be len + additionalBytes")
}

func TestEnsureBufferCapacity_ExactCapacity(t *testing.T) {
	buf := make([]byte, 10, 20)
	result := ensureBufferCapacity(buf, 10)

	// Should return same buffer when capacity is exactly enough
	require.Equal(t, &buf[0], &result[0], "should return same buffer")
	require.Equal(t, 10, len(result))
	require.Equal(t, 20, cap(result))
}

func TestEnsureBufferCapacity_ZeroAdditional(t *testing.T) {
	buf := make([]byte, 10, 20)
	result := ensureBufferCapacity(buf, 0)

	// Should return same buffer when no additional bytes needed
	require.Equal(t, &buf[0], &result[0], "should return same buffer")
	require.Equal(t, 10, len(result))
	require.Equal(t, 20, cap(result))
}

func TestEnsureBufferCapacity_EmptyBuffer(t *testing.T) {
	buf := make([]byte, 0)
	result := ensureBufferCapacity(buf, 100)

	// Should allocate buffer with exact capacity
	require.Equal(t, 0, len(result), "length should remain 0")
	require.Equal(t, 100, cap(result), "capacity should equal additionalBytes")
}

func TestEnsureBufferCapacity_PreservesData(t *testing.T) {
	buf := make([]byte, 0, 10)
	testData := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	buf = append(buf, testData...)

	result := ensureBufferCapacity(buf, 100)

	// Data should be preserved
	require.Equal(t, testData, result[:len(testData)], "data should be preserved")
}

// Benchmarks for ensureBufferCapacity

func BenchmarkEnsureBufferCapacity_NoGrowth(b *testing.B) {
	buf := make([]byte, 10, 1000)

	b.ResetTimer()
	for b.Loop() {
		_ = ensureBufferCapacity(buf, 100)
	}
}

func BenchmarkEnsureBufferCapacity_SmallGrowth(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		buf := make([]byte, 10, 20)
		_ = ensureBufferCapacity(buf, 100)
	}
}

func BenchmarkEnsureBufferCapacity_LargeGrowth(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		buf := make([]byte, 4000, 5000)
		_ = ensureBufferCapacity(buf, 10000)
	}
}
