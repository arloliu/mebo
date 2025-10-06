package pool

import (
	"bytes"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// ByteBuffer Tests
// =============================================================================

func TestNewByteBuffer(t *testing.T) {
	capacity := 1024
	bb := NewByteBuffer(capacity)

	require.NotNil(t, bb)
	require.NotNil(t, bb.B)
	assert.Equal(t, 0, len(bb.B), "new buffer should have zero length")
	assert.Equal(t, capacity, cap(bb.B), "new buffer should have specified capacity")
}

func TestByteBuffer_Bytes(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	bb.B = append(bb.B, []byte("hello")...)

	bytes := bb.Bytes()

	assert.Equal(t, []byte("hello"), bytes)
	// Should return the same underlying slice
	assert.True(t, &bb.B[0] == &bytes[0], "Bytes() should return the same underlying slice")
}

func TestByteBuffer_Reset(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	bb.B = append(bb.B, []byte("some data")...)
	originalCap := cap(bb.B)

	bb.Reset()

	assert.Equal(t, 0, len(bb.B), "Reset should clear the buffer length")
	assert.Equal(t, originalCap, cap(bb.B), "Reset should preserve capacity")
}

func TestByteBuffer_Len(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)

	assert.Equal(t, 0, bb.Len(), "empty buffer should have zero length")

	bb.B = append(bb.B, []byte("test")...)
	assert.Equal(t, 4, bb.Len(), "buffer length should match data")

	bb.B = append(bb.B, []byte(" data")...)
	assert.Equal(t, 9, bb.Len(), "buffer length should update after append")
}

func TestByteBuffer_MustWrite(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)

	bb.MustWrite([]byte("hello"))
	assert.Equal(t, []byte("hello"), bb.B)

	bb.MustWrite([]byte(" world"))
	assert.Equal(t, []byte("hello world"), bb.B)
}

func TestByteBuffer_MustWrite_EmptyData(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)

	bb.MustWrite([]byte{})
	assert.Equal(t, 0, bb.Len())

	bb.MustWrite([]byte("data"))
	bb.MustWrite([]byte{})
	assert.Equal(t, []byte("data"), bb.B)
}

func TestByteBuffer_Write(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)

	n, err := bb.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, []byte("hello"), bb.B)
}

func TestByteBuffer_Write_Multiple(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)

	n1, err1 := bb.Write([]byte("hello"))
	require.NoError(t, err1)
	assert.Equal(t, 5, n1)

	n2, err2 := bb.Write([]byte(" world"))
	require.NoError(t, err2)
	assert.Equal(t, 6, n2)

	assert.Equal(t, []byte("hello world"), bb.B)
	assert.Equal(t, 11, bb.Len())
}

func TestByteBuffer_WriteTo(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	bb.B = append(bb.B, []byte("test data")...)

	var buf bytes.Buffer
	n, err := bb.WriteTo(&buf)

	require.NoError(t, err)
	assert.Equal(t, int64(9), n)
	assert.Equal(t, "test data", buf.String())
}

func TestByteBuffer_WriteTo_EmptyBuffer(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)

	var buf bytes.Buffer
	n, err := bb.WriteTo(&buf)

	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
	assert.Equal(t, "", buf.String())
}

func TestByteBuffer_WriteTo_ErrorPropagation(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	bb.B = append(bb.B, []byte("test")...)

	// errorWriter always returns an error
	errorWriter := &errorWriter{err: io.ErrShortWrite}
	n, err := bb.WriteTo(errorWriter)

	assert.Error(t, err)
	assert.Equal(t, io.ErrShortWrite, err)
	assert.Equal(t, int64(0), n)
}

// =============================================================================
// ByteBuffer Grow Tests
// =============================================================================

func TestByteBuffer_Grow_SufficientCapacity(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	originalCap := cap(bb.B)

	bb.Grow(100) // Request growth smaller than available capacity

	assert.Equal(t, originalCap, cap(bb.B), "should not reallocate when capacity is sufficient")
}

func TestByteBuffer_Grow_SmallBuffer(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	bb.B = append(bb.B, make([]byte, BlobBufferDefaultSize)...) // Fill to capacity

	bb.Grow(1024) // Request 1KB more

	assert.GreaterOrEqual(t, cap(bb.B), BlobBufferDefaultSize+1024, "should have at least requested capacity")
	assert.Equal(t, BlobBufferDefaultSize, len(bb.B), "length should not change")
}

func TestByteBuffer_Grow_LargeBuffer(t *testing.T) {
	// Create buffer larger than 4*BlobBufferDefaultSize (64KB for 16KB default)
	bb := NewByteBuffer(BlobBufferDefaultSize)
	largeSize := 4*BlobBufferDefaultSize + 1024
	bb.B = make([]byte, largeSize)

	bb.Grow(2048) // Request 2KB more

	// For large buffers, should grow by exactly what's needed
	assert.GreaterOrEqual(t, cap(bb.B), largeSize+2048, "should have at least requested capacity")
}

func TestByteBuffer_Grow_ExactRequiredBytes(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	bb.B = append(bb.B, make([]byte, BlobBufferDefaultSize)...) // Fill to capacity

	bb.Grow(1) // Request just 1 byte more

	assert.Greater(t, cap(bb.B), BlobBufferDefaultSize, "should have grown")
}

func TestByteBuffer_Grow_MoreThanDefaultGrowth(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	bb.B = append(bb.B, make([]byte, BlobBufferDefaultSize)...) // Fill to capacity

	hugeSize := BlobBufferDefaultSize * 10
	bb.Grow(hugeSize)

	assert.GreaterOrEqual(t, cap(bb.B), BlobBufferDefaultSize+hugeSize, "should accommodate huge growth request")
}

func TestByteBuffer_Grow_PreservesData(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	testData := []byte("important data that must be preserved")
	bb.B = append(bb.B, testData...)

	bb.Grow(BlobBufferDefaultSize * 2) // Force reallocation

	assert.Equal(t, testData, bb.B, "data should be preserved after growth")
}

func TestByteBuffer_Grow_ZeroBytes(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	originalCap := cap(bb.B)

	bb.Grow(0)

	assert.Equal(t, originalCap, cap(bb.B), "Grow(0) should not change capacity")
}

// =============================================================================
// Pool Tests
// =============================================================================

func TestGetBlobBuffer(t *testing.T) {
	bb := GetBlobBuffer()

	require.NotNil(t, bb)
	require.NotNil(t, bb.B)
	assert.Equal(t, 0, len(bb.B), "pooled buffer should be empty")
	assert.GreaterOrEqual(t, cap(bb.B), BlobBufferDefaultSize, "pooled buffer should have at least default capacity")
}

func TestPutBlobBuffer_NilBuffer(t *testing.T) {
	// Should not panic
	assert.NotPanics(t, func() {
		PutBlobBuffer(nil)
	})
}

func TestGetPut_BufferReuse(t *testing.T) {
	// Get a buffer and write some data
	bb1 := GetBlobBuffer()
	bb1.B = append(bb1.B, []byte("test data")...)
	capacity1 := cap(bb1.B)

	// Return it to the pool
	PutBlobBuffer(bb1)

	// Get another buffer - might be the same one
	bb2 := GetBlobBuffer()
	assert.Equal(t, 0, len(bb2.B), "buffer from pool should be reset")

	// If we got the same buffer, capacity should match
	if capacity1 == cap(bb2.B) {
		// Likely the same buffer was reused
		t.Log("Buffer was likely reused from pool")
	}
}

func TestPool_ResetsClearsData(t *testing.T) {
	bb := GetBlobBuffer()
	bb.B = append(bb.B, []byte("sensitive data")...)

	PutBlobBuffer(bb)

	// Get a buffer (might be the same one)
	bb2 := GetBlobBuffer()
	assert.Equal(t, 0, len(bb2.B), "buffer should be empty after retrieval from pool")

	// Even if we got a different buffer, verify the original was reset
	assert.Equal(t, 0, len(bb.B), "PutBlobBuffer should reset the buffer")
}

func TestPool_MultipleGetsAndPuts(t *testing.T) {
	buffers := make([]*ByteBuffer, 10)

	// Get multiple buffers
	for i := range buffers {
		buffers[i] = GetBlobBuffer()
		require.NotNil(t, buffers[i])
		buffers[i].MustWrite([]byte("data"))
	}

	// Return all to pool
	for _, bb := range buffers {
		PutBlobBuffer(bb)
	}

	// Get them again - they should all be reset
	for i := 0; i < 10; i++ {
		bb := GetBlobBuffer()
		assert.Equal(t, 0, bb.Len(), "each buffer should be reset")
		PutBlobBuffer(bb)
	}
}

func TestPool_ConcurrentAccess(t *testing.T) {
	const numGoroutines = 100
	const numIterations = 1000

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				bb := GetBlobBuffer()
				bb.MustWrite([]byte("data"))
				assert.Equal(t, 4, bb.Len())
				PutBlobBuffer(bb)
			}
		}()
	}

	wg.Wait()
}

// =============================================================================
// ByteBufferPool Tests (New Refactored API)
// =============================================================================

func TestNewByteBufferPool(t *testing.T) {
	pool := NewByteBufferPool(8192, 65536)

	require.NotNil(t, pool)

	// Get a buffer and verify size
	bb := pool.Get()
	require.NotNil(t, bb)
	assert.GreaterOrEqual(t, cap(bb.B), 8192, "buffer should have at least default size")

	pool.Put(bb)
}

func TestByteBufferPool_CustomSizes(t *testing.T) {
	tests := []struct {
		name         string
		defaultSize  int
		maxThreshold int
	}{
		{"Small pool", 1024, 4096},
		{"Medium pool", 16384, 131072},
		{"Large pool", 1048576, 8388608},
		{"No threshold", 8192, 0}, // 0 means no limit
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := NewByteBufferPool(tt.defaultSize, tt.maxThreshold)
			bb := pool.Get()
			assert.GreaterOrEqual(t, cap(bb.B), tt.defaultSize)
			pool.Put(bb)
		})
	}
}

func TestByteBufferPool_MaxThreshold_Discard(t *testing.T) {
	pool := NewByteBufferPool(1024, 4096)

	// Get a buffer and grow it beyond maxThreshold
	bb := pool.Get()
	bb.Grow(10000) // Grow beyond 4096 threshold

	assert.Greater(t, cap(bb.B), 4096, "buffer should have grown beyond threshold")

	// Put it back - should be discarded
	pool.Put(bb)

	// Get another buffer - should be a fresh one (not the large one)
	bb2 := pool.Get()
	assert.LessOrEqual(t, cap(bb2.B), 4096*2, "should not reuse buffer larger than threshold")
}

func TestByteBufferPool_MaxThreshold_Accept(t *testing.T) {
	pool := NewByteBufferPool(1024, 4096)

	// Get a buffer - it should have default capacity of 1024
	bb := pool.Get()
	initialCap := cap(bb.B)

	// Write some data but stay well below threshold
	bb.MustWrite(make([]byte, 500))

	capacity1 := cap(bb.B)
	t.Logf("Buffer capacity after write: %d (threshold: %d)", capacity1, 4096)

	// Put it back - should be accepted if under threshold
	pool.Put(bb)

	// Get another buffer
	bb2 := pool.Get()
	capacity2 := cap(bb2.B)
	t.Logf("Next buffer capacity: %d", capacity2)

	// If capacities match and buffer is under threshold, it was likely reused
	if capacity1 <= 4096 && capacity2 == capacity1 {
		t.Log("Buffer was reused (capacity matches and under threshold)")
	} else if capacity2 == initialCap {
		t.Log("Got a fresh buffer with initial capacity")
	}
}

func TestByteBufferPool_MaxThreshold_Zero(t *testing.T) {
	pool := NewByteBufferPool(1024, 0) // 0 means no limit

	// Get a buffer and grow it very large
	bb := pool.Get()
	bb.Grow(1024 * 1024) // 1MB

	assert.Greater(t, cap(bb.B), 100000, "buffer should have grown to large size")

	// Put it back - should be accepted (no threshold)
	pool.Put(bb)

	// Get another buffer
	bb2 := pool.Get()
	// With no threshold, the large buffer should be reused
	assert.NotNil(t, bb2)
}

func TestGetBlobSetBuffer(t *testing.T) {
	bb := GetBlobSetBuffer()

	require.NotNil(t, bb)
	require.NotNil(t, bb.B)
	assert.Equal(t, 0, len(bb.B), "blob set buffer should be empty")
	assert.GreaterOrEqual(t, cap(bb.B), BlobSetBufferDefaultSize, "blob set buffer should have at least default size")
}

func TestPutBlobSetBuffer(t *testing.T) {
	bb := GetBlobSetBuffer()
	bb.MustWrite([]byte("test data"))

	// Should not panic
	assert.NotPanics(t, func() {
		PutBlobSetBuffer(bb)
	})

	// Verify buffer was reset
	assert.Equal(t, 0, len(bb.B), "PutBlobSetBuffer should reset the buffer")
}

func TestBlobSetBuffer_ReusePattern(t *testing.T) {
	// Get a blob set buffer and write data
	bb1 := GetBlobSetBuffer()
	bb1.MustWrite(make([]byte, 500*1024)) // 500KB
	capacity1 := cap(bb1.B)

	// Return to pool
	PutBlobSetBuffer(bb1)

	// Get another one
	bb2 := GetBlobSetBuffer()
	assert.Equal(t, 0, len(bb2.B), "buffer should be reset")

	// If capacities match, likely the same buffer
	if cap(bb2.B) == capacity1 {
		t.Log("Blob set buffer was likely reused from pool")
	}
}

func TestBlobSetBuffer_MaxThreshold(t *testing.T) {
	// Get a blob set buffer and grow it beyond max threshold
	bb := GetBlobSetBuffer()
	bb.Grow(10 * 1024 * 1024) // 10MB, beyond BlobSetBufferMaxThreshold (8MB)

	assert.Greater(t, cap(bb.B), BlobSetBufferMaxThreshold, "buffer should have grown beyond threshold")

	// Put it back - should be discarded
	PutBlobSetBuffer(bb)

	// Get another buffer - should be fresh (not the huge one)
	bb2 := GetBlobSetBuffer()
	assert.LessOrEqual(t, cap(bb2.B), BlobSetBufferMaxThreshold*2, "should not reuse overly large buffer")
}

func TestDefaultPools_Independence(t *testing.T) {
	// Get blob buffer
	blobBuf := GetBlobBuffer()
	blobCap := cap(blobBuf.B)

	// Get blob set buffer
	blobSetBuf := GetBlobSetBuffer()
	blobSetCap := cap(blobSetBuf.B)

	// They should have different capacities (16KB vs 1MB defaults)
	assert.NotEqual(t, blobCap, blobSetCap, "blob and blob set buffers should have different default sizes")
	assert.GreaterOrEqual(t, blobCap, BlobBufferDefaultSize, "blob buffer should be >= 16KB")
	assert.GreaterOrEqual(t, blobSetCap, BlobSetBufferDefaultSize, "blob set buffer should be >= 1MB")

	PutBlobBuffer(blobBuf)
	PutBlobSetBuffer(blobSetBuf)
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestByteBuffer_LargeDataWrite(t *testing.T) {
	bb := GetBlobBuffer()
	defer PutBlobBuffer(bb)

	// Write 1MB of data
	largeData := make([]byte, 1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	bb.MustWrite(largeData)

	assert.Equal(t, len(largeData), bb.Len())
	assert.Equal(t, largeData, bb.B)
}

func TestByteBuffer_GrowAndWrite(t *testing.T) {
	bb := GetBlobBuffer()
	defer PutBlobBuffer(bb)

	// Pre-grow for large write
	bb.Grow(100 * 1024)
	initialCap := cap(bb.B)

	// Write data that fits in pre-grown buffer
	data := make([]byte, 50*1024)
	bb.MustWrite(data)

	// Should not have reallocated
	assert.Equal(t, initialCap, cap(bb.B))
	assert.Equal(t, 50*1024, bb.Len())
}

func TestByteBuffer_MultipleWritesCauseGrowth(t *testing.T) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	initialCap := cap(bb.B)

	// Write data larger than initial capacity
	largeData := make([]byte, BlobBufferDefaultSize+1000)
	bb.MustWrite(largeData)

	assert.Greater(t, cap(bb.B), initialCap, "buffer should have grown")
	assert.Equal(t, len(largeData), bb.Len())
}

func TestByteBuffer_ResetAndReuse(t *testing.T) {
	bb := GetBlobBuffer()
	defer PutBlobBuffer(bb)

	// First use
	bb.MustWrite([]byte("first"))
	assert.Equal(t, 5, bb.Len())

	// Reset and reuse
	bb.Reset()
	assert.Equal(t, 0, bb.Len())

	bb.MustWrite([]byte("second"))
	assert.Equal(t, 6, bb.Len())
	assert.Equal(t, []byte("second"), bb.B)
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkByteBuffer_Write(b *testing.B) {
	data := []byte("benchmark data for testing write performance")

	b.ResetTimer()
	for b.Loop() {
		bb := NewByteBuffer(BlobBufferDefaultSize)
		_, _ = bb.Write(data)
	}
}

func BenchmarkByteBuffer_Write_Small(b *testing.B) {
	bb := GetBlobBuffer()
	defer PutBlobBuffer(bb)
	data := []byte("small data")

	b.ResetTimer()
	for b.Loop() {
		bb.Reset()
		bb.MustWrite(data)
	}
}

func BenchmarkByteBuffer_Write_Large(b *testing.B) {
	bb := GetBlobBuffer()
	defer PutBlobBuffer(bb)
	data := make([]byte, 64*1024) // 64KB

	b.ResetTimer()
	for b.Loop() {
		bb.Reset()
		bb.MustWrite(data)
	}
}

func BenchmarkByteBuffer_WriteTo(b *testing.B) {
	bb := NewByteBuffer(BlobBufferDefaultSize)
	bb.B = append(bb.B, make([]byte, 1024)...) // 1KB data

	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		_, _ = bb.WriteTo(&buf)
	}
}

func BenchmarkByteBuffer_Grow(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		bb := NewByteBuffer(BlobBufferDefaultSize)
		bb.Grow(1024 * 1024) // 1MB
	}
}

func BenchmarkGetPut_Reuse(b *testing.B) {
	for b.Loop() {
		bb := GetBlobBuffer()
		bb.MustWrite([]byte("benchmark data"))
		PutBlobBuffer(bb)
	}
}

func BenchmarkNewBuffer_NoPool(b *testing.B) {
	for b.Loop() {
		bb := NewByteBuffer(BlobBufferDefaultSize)
		bb.MustWrite([]byte("benchmark data"))
		_ = bb
	}
}

func BenchmarkPool_GetPut(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		bb := GetBlobBuffer()
		PutBlobBuffer(bb)
	}
}

func BenchmarkPool_GetWritePut(b *testing.B) {
	data := []byte("benchmark data")

	b.ResetTimer()
	for b.Loop() {
		bb := GetBlobBuffer()
		bb.MustWrite(data)
		PutBlobBuffer(bb)
	}
}

func BenchmarkPool_vs_NewBuffer(b *testing.B) {
	data := make([]byte, 1024)

	b.Run("WithPool", func(b *testing.B) {
		for b.Loop() {
			bb := GetBlobBuffer()
			bb.MustWrite(data)
			PutBlobBuffer(bb)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		for b.Loop() {
			bb := NewByteBuffer(BlobBufferDefaultSize)
			bb.MustWrite(data)
		}
	})
}

func BenchmarkByteBuffer_LargeWrites(b *testing.B) {
	sizes := []int{
		1024,        // 1KB
		8192,        // 8KB
		64 * 1024,   // 64KB
		256 * 1024,  // 256KB
		1024 * 1024, // 1MB
	}

	for _, size := range sizes {
		data := make([]byte, size)
		b.Run(formatBytes(size), func(b *testing.B) {
			for b.Loop() {
				bb := GetBlobBuffer()
				bb.MustWrite(data)
				PutBlobBuffer(bb)
			}
		})
	}
}

// =============================================================================
// ByteBuffer vs Native Slice Comparison Benchmarks
// =============================================================================

func BenchmarkByteBuffer_vs_NativeSlice_SingleWrite(b *testing.B) {
	data := []byte("benchmark data for testing write performance")

	b.Run("ByteBuffer/Write", func(b *testing.B) {
		bb := NewByteBuffer(BlobBufferDefaultSize)
		for b.Loop() {
			_, _ = bb.Write(data)
			bb.Reset()
		}
	})

	b.Run("ByteBuffer/MustWrite", func(b *testing.B) {
		bb := NewByteBuffer(BlobBufferDefaultSize)
		for b.Loop() {
			bb.MustWrite(data)
			bb.Reset()
		}
	})

	b.Run("NativeSlice/Append", func(b *testing.B) {
		slice := make([]byte, 0, BlobBufferDefaultSize)
		for b.Loop() {
			slice = append(slice, data...)
			slice = slice[:0]
		}
	})
}

func BenchmarkWrite_WithPool(b *testing.B) {
	for b.Loop() {
		bb := GetBlobBuffer()
		bb.MustWrite([]byte("test data"))
		_ = bb
	}
}

func BenchmarkWrite_WithPool_GetPut(b *testing.B) {
	for b.Loop() {
		bb := GetBlobBuffer()
		bb.MustWrite([]byte("test data"))
		PutBlobBuffer(bb)
	}
}

func BenchmarkWrite_NativeSlice(b *testing.B) {
	for b.Loop() {
		slice := make([]byte, 0, BlobBufferDefaultSize)
		slice = append(slice, []byte("test data")...)
		_ = slice
	}
}

func BenchmarkWrite_LargeData_WithPool(b *testing.B) {
	largeData := make([]byte, 1024) // 1KB

	b.Run("PooledBuffer", func(b *testing.B) {
		for b.Loop() {
			bb := GetBlobBuffer()
			for i := 0; i < 1000; i++ {
				bb.MustWrite(largeData)
			}
			PutBlobBuffer(bb)
		}
	})
}

func BenchmarkWrite_LargeData_NoPool(b *testing.B) {
	largeData := make([]byte, 1024) // 1KB

	b.Run("NonPooledBuffer", func(b *testing.B) {
		for b.Loop() {
			bb := NewByteBuffer(BlobBufferDefaultSize)
			for i := 0; i < 1000; i++ {
				bb.MustWrite(largeData)
			}
			_ = bb
		}
	})
}

func BenchmarkWrite_RealWorldPattern(b *testing.B) {
	// Simulate real-world pattern: create, write multiple times, discard
	data1 := []byte("timestamp:1234567890|")
	data2 := []byte("value:42.5|")
	data3 := []byte("tags:host=server1,region=us-west")

	b.Run("WithPool", func(b *testing.B) {
		for b.Loop() {
			bb := GetBlobBuffer()
			bb.MustWrite(data1)
			bb.MustWrite(data2)
			bb.MustWrite(data3)
			PutBlobBuffer(bb)
		}
	})

	b.Run("WithoutPool", func(b *testing.B) {
		for b.Loop() {
			slice := make([]byte, 0, 128)
			slice = append(slice, data1...)
			slice = append(slice, data2...)
			slice = append(slice, data3...)
			_ = slice
		}
	})
}

func BenchmarkConcurrentGetPut(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			bb := GetBlobBuffer()
			bb.MustWrite([]byte("concurrent test data"))
			PutBlobBuffer(bb)
		}
	})
}

// =============================================================================
// Helper Types and Functions
// =============================================================================

// errorWriter is a writer that always returns an error
type errorWriter struct {
	err error
}

func (ew *errorWriter) Write(p []byte) (n int, err error) {
	return 0, ew.err
}

// formatBytes formats a byte count as a human-readable string
func formatBytes(b int) string {
	const unit = 1024
	if b < unit {
		return bytesToString(b) + "B"
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return bytesToString(b/int(div)) + []string{"K", "M", "G"}[exp] + "B"
}

func bytesToString(n int) string {
	// Simple integer to string conversion
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	// Reverse
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}

	return string(digits)
}
