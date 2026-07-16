package pool

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetUint64Slice(t *testing.T) {
	t.Run("returns slice with correct size", func(t *testing.T) {
		ptr := GetUint64Slice(100)
		defer PutUint64Slice(ptr)

		require.Equal(t, 100, len(*ptr))
		require.GreaterOrEqual(t, cap(*ptr), 100)
	})

	t.Run("reuses pooled slice when capacity sufficient", func(t *testing.T) {
		// First allocation
		ptr1 := GetUint64Slice(50)
		arrPtr1 := &(*ptr1)[0]
		PutUint64Slice(ptr1)

		// Second allocation should reuse the same underlying array
		ptr2 := GetUint64Slice(50)
		defer PutUint64Slice(ptr2)
		arrPtr2 := &(*ptr2)[0]

		require.Equal(t, arrPtr1, arrPtr2, "should reuse same underlying array")
	})

	t.Run("allocates new slice when capacity insufficient", func(t *testing.T) {
		// First allocation with small size
		ptr1 := GetUint64Slice(10)
		PutUint64Slice(ptr1)

		// Second allocation with larger size should allocate new slice
		ptr2 := GetUint64Slice(1000)
		defer PutUint64Slice(ptr2)

		require.Equal(t, 1000, len(*ptr2))
		require.GreaterOrEqual(t, cap(*ptr2), 1000)
	})

	t.Run("PutUint64Slice returns slice to pool", func(t *testing.T) {
		ptr := GetUint64Slice(100)
		require.NotNil(t, ptr)

		// Should not panic
		PutUint64Slice(ptr)
	})

	t.Run("PutUint64Slice(nil) is a no-op", func(t *testing.T) {
		require.NotPanics(t, func() { PutUint64Slice(nil) })
	})

	t.Run("warm Get+Put pair is allocation-free", func(t *testing.T) {
		if raceEnabled {
			t.Skip("sync.Pool intentionally drops Puts under the race detector; the zero-alloc invariant only holds without -race")
		}

		// Warm up so the pool holds a correctly-sized backing array.
		for i := 0; i < 10; i++ {
			p1 := GetUint64Slice(1000)
			p2 := GetUint64Slice(1000)
			PutUint64Slice(p2)
			PutUint64Slice(p1)
		}

		allocs := testing.AllocsPerRun(1000, func() {
			p1 := GetUint64Slice(1000)
			p2 := GetUint64Slice(1000)
			(*p1)[0] = 1
			(*p2)[0] = 2
			PutUint64Slice(p2)
			PutUint64Slice(p1)
		})
		require.Zerof(t, allocs, "warm GetUint64Slice/PutUint64Slice must not allocate (got %v allocs/op)", allocs)
	})
}
