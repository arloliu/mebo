package encoding

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// genJitterTestData encodes count timestamps at 1s intervals with the given
// jitter fraction and returns both the encoded bytes and the original values.
func genJitterTestData(count int, jitterPct float64, seed int64) ([]byte, []int64) {
	rng := rand.New(rand.NewSource(seed))
	ts := time.Unix(1700000000, 0).UnixMicro()
	interval := int64(time.Second / time.Microsecond)

	timestamps := make([]int64, count)
	for i := range count {
		jitter := int64(float64(interval) * jitterPct * (rng.Float64()*2 - 1))
		ts += interval + jitter
		timestamps[i] = ts
	}

	enc := NewTimestampDeltaEncoder()
	enc.WriteSlice(timestamps)
	out := append([]byte(nil), enc.Bytes()...)
	enc.Finish()

	return out, timestamps
}

// TestTimestampDeltaDecoder_DecodeAll_JitterRegimes exercises DecodeAll across
// varint-width regimes (1/2/3-byte dominated and mixed-width streams),
// validating against the original timestamps.
func TestTimestampDeltaDecoder_DecodeAll_JitterRegimes(t *testing.T) {
	var dec TimestampDeltaDecoder

	for _, count := range []int{1, 2, 3, 63, 64, 65, 66, 67, 127, 128, 129, 200, 1000, 10000} {
		// jitter 0.009 straddles the 2-byte/3-byte varint boundary (mixed widths).
		for _, jitter := range []float64{0, 0.001, 0.009, 0.05, 0.3} {
			data, want := genJitterTestData(count, jitter, 42)

			got := make([]int64, count)
			n := dec.DecodeAll(data, count, got)
			require.Equal(t, count, n, "count=%d jitter=%v", count, jitter)
			require.Equal(t, want, got, "count=%d jitter=%v", count, jitter)
		}
	}
}

// TestTimestampDeltaDecoder_DecodeAll_JitterTruncated verifies graceful
// partial decode when high-jitter data is truncated mid-stream.
func TestTimestampDeltaDecoder_DecodeAll_JitterTruncated(t *testing.T) {
	var dec TimestampDeltaDecoder

	data, want := genJitterTestData(1000, 0.05, 7)

	for _, cut := range []int{len(data) - 1, len(data) / 2, len(data) / 4} {
		got := make([]int64, 1000)
		n := dec.DecodeAll(data[:cut], 1000, got)
		require.Less(t, n, 1000, "cut=%d", cut)
		require.Greater(t, n, 0, "cut=%d", cut)
		require.Equal(t, want[:n], got[:n], "cut=%d: decoded prefix must match", cut)
	}
}
