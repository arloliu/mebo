package pool

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetInt64Slice(t *testing.T) {
	t.Run("returns slice with correct size", func(t *testing.T) {
		slice, cleanup := GetInt64Slice(100)
		defer cleanup()

		require.Equal(t, 100, len(slice))
		require.GreaterOrEqual(t, cap(slice), 100)
	})

	t.Run("reuses pooled slice when capacity sufficient", func(t *testing.T) {
		// First allocation
		slice1, cleanup1 := GetInt64Slice(50)
		ptr1 := &slice1[0]
		cleanup1()

		// Second allocation should reuse the same underlying array
		slice2, cleanup2 := GetInt64Slice(50)
		defer cleanup2()
		ptr2 := &slice2[0]

		require.Equal(t, ptr1, ptr2, "should reuse same underlying array")
	})

	t.Run("allocates new slice when capacity insufficient", func(t *testing.T) {
		// First allocation with small size
		_, cleanup1 := GetInt64Slice(10)
		cleanup1()

		// Second allocation with larger size should allocate new slice
		slice2, cleanup2 := GetInt64Slice(1000)
		defer cleanup2()

		require.Equal(t, 1000, len(slice2))
		require.GreaterOrEqual(t, cap(slice2), 1000)
	})

	t.Run("cleanup returns slice to pool", func(t *testing.T) {
		slice, cleanup := GetInt64Slice(100)
		require.NotNil(t, slice)

		// Should not panic
		cleanup()
	})
}

func TestGetFloat64Slice(t *testing.T) {
	t.Run("returns slice with correct size", func(t *testing.T) {
		slice, cleanup := GetFloat64Slice(100)
		defer cleanup()

		require.Equal(t, 100, len(slice))
		require.GreaterOrEqual(t, cap(slice), 100)
	})

	t.Run("reuses pooled slice when capacity sufficient", func(t *testing.T) {
		// First allocation
		slice1, cleanup1 := GetFloat64Slice(50)
		ptr1 := &slice1[0]
		cleanup1()

		// Second allocation should reuse the same underlying array
		slice2, cleanup2 := GetFloat64Slice(50)
		defer cleanup2()
		ptr2 := &slice2[0]

		require.Equal(t, ptr1, ptr2, "should reuse same underlying array")
	})

	t.Run("allocates new slice when capacity insufficient", func(t *testing.T) {
		// First allocation with small size
		_, cleanup1 := GetFloat64Slice(10)
		cleanup1()

		// Second allocation with larger size should allocate new slice
		slice2, cleanup2 := GetFloat64Slice(1000)
		defer cleanup2()

		require.Equal(t, 1000, len(slice2))
		require.GreaterOrEqual(t, cap(slice2), 1000)
	})

	t.Run("cleanup returns slice to pool", func(t *testing.T) {
		slice, cleanup := GetFloat64Slice(100)
		require.NotNil(t, slice)

		// Should not panic
		cleanup()
	})
}

func TestGetStringSlice(t *testing.T) {
	t.Run("returns slice with correct size", func(t *testing.T) {
		slice, cleanup := GetStringSlice(100)
		defer cleanup()

		require.Equal(t, 100, len(slice))
		require.GreaterOrEqual(t, cap(slice), 100)
	})

	t.Run("reuses pooled slice when capacity sufficient", func(t *testing.T) {
		// First allocation
		slice1, cleanup1 := GetStringSlice(50)
		ptr1 := &slice1[0]
		cleanup1()

		// Second allocation should reuse the same underlying array
		slice2, cleanup2 := GetStringSlice(50)
		defer cleanup2()
		ptr2 := &slice2[0]

		require.Equal(t, ptr1, ptr2, "should reuse same underlying array")
	})

	t.Run("allocates new slice when capacity insufficient", func(t *testing.T) {
		// First allocation with small size
		_, cleanup1 := GetStringSlice(10)
		cleanup1()

		// Second allocation with larger size should allocate new slice
		slice2, cleanup2 := GetStringSlice(1000)
		defer cleanup2()

		require.Equal(t, 1000, len(slice2))
		require.GreaterOrEqual(t, cap(slice2), 1000)
	})

	t.Run("cleanup returns slice to pool", func(t *testing.T) {
		slice, cleanup := GetStringSlice(100)
		require.NotNil(t, slice)

		// Should not panic
		cleanup()
	})
}

func TestGetUint32Slice(t *testing.T) {
	t.Run("returns slice with correct size", func(t *testing.T) {
		slice, cleanup := GetUint32Slice(100)
		defer cleanup()

		require.Equal(t, 100, len(slice))
		require.GreaterOrEqual(t, cap(slice), 100)
	})

	t.Run("reuses pooled slice when capacity sufficient", func(t *testing.T) {
		slice1, cleanup1 := GetUint32Slice(50)
		ptr1 := &slice1[0]
		cleanup1()

		slice2, cleanup2 := GetUint32Slice(50)
		defer cleanup2()
		ptr2 := &slice2[0]

		require.Equal(t, ptr1, ptr2, "should reuse same underlying array")
	})

	t.Run("allocates new slice when capacity insufficient", func(t *testing.T) {
		_, cleanup1 := GetUint32Slice(10)
		cleanup1()

		slice2, cleanup2 := GetUint32Slice(1000)
		defer cleanup2()

		require.Equal(t, 1000, len(slice2))
		require.GreaterOrEqual(t, cap(slice2), 1000)
	})

	// Sparse counting tables rely on the all-zero-at-rest invariant: a holder
	// that resets exactly the entries it touched must hand back an all-zero
	// backing array, so the next holder sees zero everywhere.
	t.Run("reused slice is all zero when the prior holder reset its writes", func(t *testing.T) {
		slice1, cleanup1 := GetUint32Slice(256)
		// Simulate a sparse holder that touches a subset then resets it.
		touched := []int{0, 3, 200, 255}
		for _, i := range touched {
			slice1[i] = uint32(i + 1)
		}
		for _, i := range touched {
			slice1[i] = 0
		}
		cleanup1()

		slice2, cleanup2 := GetUint32Slice(256)
		defer cleanup2()
		for i := range slice2 {
			require.Zerof(t, slice2[i], "reused counting table must be zero at %d", i)
		}
	})
}

func TestSlicePoolConcurrency(t *testing.T) {
	t.Run("concurrent access to int64 pool", func(t *testing.T) {
		const goroutines = 100
		done := make(chan bool, goroutines)

		for range goroutines {
			go func() {
				slice, cleanup := GetInt64Slice(50)
				defer cleanup()

				// Write to slice to ensure it's usable
				for j := range slice {
					slice[j] = int64(j)
				}

				done <- true
			}()
		}

		for range goroutines {
			<-done
		}
	})

	t.Run("concurrent access to float64 pool", func(t *testing.T) {
		const goroutines = 100
		done := make(chan bool, goroutines)

		for range goroutines {
			go func() {
				slice, cleanup := GetFloat64Slice(50)
				defer cleanup()

				// Write to slice to ensure it's usable
				for j := range slice {
					slice[j] = float64(j)
				}

				done <- true
			}()
		}

		for range goroutines {
			<-done
		}
	})

	t.Run("concurrent access to string pool", func(t *testing.T) {
		const goroutines = 100
		done := make(chan bool, goroutines)

		for range goroutines {
			go func() {
				slice, cleanup := GetStringSlice(50)
				defer cleanup()

				// Write to slice to ensure it's usable
				for j := range slice {
					slice[j] = "test"
				}

				done <- true
			}()
		}

		for range goroutines {
			<-done
		}
	})
}
