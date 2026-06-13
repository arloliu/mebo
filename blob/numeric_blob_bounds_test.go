package blob

import (
	"math"
	"testing"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/section"
	"github.com/stretchr/testify/require"
)

// TestNumericBlob_CorruptIndexEntryNoPanic verifies that every public iteration
// and random-access path returns empty/false instead of panicking when an index
// entry's offsets/lengths fall outside the payloads. The decoder normally
// rejects such entries at parse time; these guards are defense-in-depth against
// any path that constructs a blob with unvalidated entries, and keep behavior
// consistent across ForEach, All*, and *At.
func TestNumericBlob_CorruptIndexEntryNoPanic(t *testing.T) {
	const metricID = uint64(12345)

	newBlob := func(entry section.NumericIndexEntry) NumericBlob {
		return NumericBlob{
			blobBase: blobBase{
				tsEncType: format.TypeRaw,
				flags:     section.FlagTagEnabled, // exercise the tag suffix guard too
			},
			index: indexMaps[section.NumericIndexEntry]{
				byID: map[uint64]section.NumericIndexEntry{metricID: entry},
			},
			tsPayload:  make([]byte, 32),
			valPayload: make([]byte, 32),
			tagPayload: make([]byte, 32),
		}
	}

	assertEmpty := func(t *testing.T, b NumericBlob) {
		t.Helper()

		count := 0
		for range b.All(metricID) {
			count++
		}
		require.Equal(t, 0, count, "All should yield nothing")

		count = 0
		for range b.AllTimestamps(metricID) {
			count++
		}
		require.Equal(t, 0, count, "AllTimestamps should yield nothing")

		count = 0
		for range b.AllValues(metricID) {
			count++
		}
		require.Equal(t, 0, count, "AllValues should yield nothing")

		count = 0
		for range b.AllTags(metricID) {
			count++
		}
		require.Equal(t, 0, count, "AllTags should yield nothing")

		count = 0
		ok := b.ForEach(metricID, func(_ int, _ NumericDataPoint) bool {
			count++
			return true
		})
		require.True(t, ok, "ForEach reports the metric exists")
		require.Equal(t, 0, count, "ForEach should not invoke the callback")

		_, tsOk := b.TimestampAt(metricID, 0)
		require.False(t, tsOk, "TimestampAt should fail cleanly")

		_, tagOk := b.TagAt(metricID, 0)
		require.False(t, tagOk, "TagAt should fail cleanly")
	}

	t.Run("offset far beyond payload", func(t *testing.T) {
		assertEmpty(t, newBlob(section.NumericIndexEntry{
			MetricID:        metricID,
			Count:           4,
			TimestampOffset: 1 << 20,
			TimestampLength: 32,
			ValueOffset:     1 << 20,
			ValueLength:     32,
			TagOffset:       1 << 20,
			TagLength:       16,
		}))
	})

	t.Run("length overflows payload without integer overflow", func(t *testing.T) {
		// offset within payload but length absurdly large: the bounds check must
		// not overflow int when computing offset+length.
		assertEmpty(t, newBlob(section.NumericIndexEntry{
			MetricID:        metricID,
			Count:           4,
			TimestampOffset: 0,
			TimestampLength: math.MaxInt,
			ValueOffset:     0,
			ValueLength:     math.MaxInt,
			TagOffset:       1 << 20,
			TagLength:       16,
		}))
	})
}
