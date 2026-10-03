package pool

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetDecodeFloat64Slice(t *testing.T) {
	t.Run("returns slice with requested length", func(t *testing.T) {
		for _, size := range []int{0, 1, 150, MaxPooledDecodeFloat64s, MaxPooledDecodeFloat64s + 1} {
			ptr := GetDecodeFloat64Slice(size)
			require.Lenf(t, *ptr, size, "size %d", size)
			PutDecodeFloat64Slice(ptr)
		}
	})

	t.Run("grows when the pooled array is too small", func(t *testing.T) {
		PutDecodeFloat64Slice(GetDecodeFloat64Slice(4))
		ptr := GetDecodeFloat64Slice(4096)
		defer PutDecodeFloat64Slice(ptr)
		require.Len(t, *ptr, 4096)
	})

	t.Run("nil put is ignored", func(t *testing.T) {
		require.NotPanics(t, func() { PutDecodeFloat64Slice(nil) })
	})

	t.Run("never retains a buffer above the cap", func(t *testing.T) {
		huge := make([]float64, 4*MaxPooledDecodeFloat64s)
		PutDecodeFloat64Slice(&huge)
		PutDecodeFloat64Slice(GetDecodeFloat64Slice(MaxPooledDecodeFloat64s + 1))
		// Drain without putting back, so the private slot and the shared list are both inspected.
		held := make([]*[]float64, 0, 64)
		for range 64 {
			ptr := GetDecodeFloat64Slice(1)
			require.LessOrEqual(t, cap(*ptr), MaxPooledDecodeFloat64s)
			held = append(held, ptr)
		}
		for _, ptr := range held {
			PutDecodeFloat64Slice(ptr)
		}
	})

	t.Run("is separate from the encoder's float64 pool", func(t *testing.T) {
		big := GetDecodeFloat64Slice(MaxPooledDecodeFloat64s)
		PutDecodeFloat64Slice(big)
		for range 64 {
			s, cleanup := GetFloat64Slice(1)
			require.Less(t, cap(s), MaxPooledDecodeFloat64s, "a decode buffer must not reach the encoder pool")
			cleanup()
		}
	})

	t.Run("warm Get+Put pair is allocation-free", func(t *testing.T) {
		if raceEnabled {
			t.Skip("sync.Pool intentionally drops Puts under the race detector; the zero-alloc invariant only holds without -race")
		}
		for range 10 {
			PutDecodeFloat64Slice(GetDecodeFloat64Slice(1000))
		}
		allocs := testing.AllocsPerRun(1000, func() {
			PutDecodeFloat64Slice(GetDecodeFloat64Slice(1000))
		})
		require.Zero(t, allocs)
	})
}
