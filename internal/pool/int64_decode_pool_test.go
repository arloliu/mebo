package pool

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetDecodeInt64Slice(t *testing.T) {
	t.Run("returns slice with requested length", func(t *testing.T) {
		for _, size := range []int{0, 1, 150, MaxPooledDecodeInt64s, MaxPooledDecodeInt64s + 1} {
			ptr := GetDecodeInt64Slice(size)
			require.Lenf(t, *ptr, size, "size %d", size)
			PutDecodeInt64Slice(ptr)
		}
	})

	t.Run("grows when the pooled array is too small", func(t *testing.T) {
		PutDecodeInt64Slice(GetDecodeInt64Slice(4))
		ptr := GetDecodeInt64Slice(4096)
		defer PutDecodeInt64Slice(ptr)
		require.Len(t, *ptr, 4096)
	})

	t.Run("nil put is ignored", func(t *testing.T) {
		require.NotPanics(t, func() { PutDecodeInt64Slice(nil) })
	})

	t.Run("never retains a buffer above the cap", func(t *testing.T) {
		huge := make([]int64, 4*MaxPooledDecodeInt64s)
		PutDecodeInt64Slice(&huge)
		PutDecodeInt64Slice(GetDecodeInt64Slice(MaxPooledDecodeInt64s + 1))
		// Drain without putting back, so the private slot and the shared list are both inspected.
		held := make([]*[]int64, 0, 64)
		for range 64 {
			ptr := GetDecodeInt64Slice(1)
			require.LessOrEqual(t, cap(*ptr), MaxPooledDecodeInt64s)
			held = append(held, ptr)
		}
		for _, ptr := range held {
			PutDecodeInt64Slice(ptr)
		}
	})

	t.Run("is separate from the encoder's int64 pool", func(t *testing.T) {
		big := GetDecodeInt64Slice(MaxPooledDecodeInt64s)
		PutDecodeInt64Slice(big)
		for range 64 {
			s, cleanup := GetInt64Slice(1)
			require.Less(t, cap(s), MaxPooledDecodeInt64s, "a decode buffer must not reach the encoder pool")
			cleanup()
		}
	})

	t.Run("warm Get+Put pair is allocation-free", func(t *testing.T) {
		if raceEnabled {
			t.Skip("sync.Pool intentionally drops Puts under the race detector; the zero-alloc invariant only holds without -race")
		}
		for range 10 {
			PutDecodeInt64Slice(GetDecodeInt64Slice(1000))
		}
		allocs := testing.AllocsPerRun(1000, func() {
			PutDecodeInt64Slice(GetDecodeInt64Slice(1000))
		})
		require.Zero(t, allocs)
	})
}
