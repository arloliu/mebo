package section

import (
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/stretchr/testify/require"
)

func TestParseTextIndexEntry(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	entry := TextIndexEntry{MetricID: 0x1122334455667788, Count: 42, Offset: 1234}

	b := make([]byte, TextIndexEntrySize)
	require.NoError(t, entry.WriteToSlice(b, engine))

	t.Run("round trip", func(t *testing.T) {
		got, err := ParseTextIndexEntry(b, engine)
		require.NoError(t, err)
		require.Equal(t, entry.MetricID, got.MetricID)
		require.Equal(t, entry.Count, got.Count)
		require.Equal(t, entry.Offset, got.Offset)
	})

	t.Run("short input", func(t *testing.T) {
		_, err := ParseTextIndexEntry(b[:TextIndexEntrySize-1], engine)
		require.ErrorIs(t, err, errs.ErrInvalidIndexEntrySize)
	})

	t.Run("non-zero reserved bytes", func(t *testing.T) {
		for _, i := range []int{10, 11} {
			corrupt := append([]byte(nil), b...)
			corrupt[i] = 0x01

			_, err := ParseTextIndexEntry(corrupt, engine)
			require.ErrorIs(t, err, errs.ErrInvalidReservedBytes, "byte %d", i)
		}
	})
}
