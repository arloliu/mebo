package bitstream

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReaderReadsMostSignificantBitsAcrossBytes(t *testing.T) {
	reader := NewReader([]byte{0xAA, 0xCC})

	value, ok := reader.ReadBits(4)
	require.True(t, ok)
	require.Equal(t, uint64(0xA), value)

	value, ok = reader.ReadBits(8)
	require.True(t, ok)
	require.Equal(t, uint64(0xAC), value)

	value, ok = reader.ReadBits(4)
	require.True(t, ok)
	require.Equal(t, uint64(0xC), value)
}

func TestReaderRejectsTruncatedReadWithoutProducingValue(t *testing.T) {
	reader := NewReader([]byte{0xFF})

	_, ok := reader.ReadBits(8)
	require.True(t, ok)

	value, ok := reader.ReadBit()
	require.False(t, ok)
	require.Equal(t, uint64(0), value)
}

func TestPeekBits64ReadsPastTailAsZeroPadded(t *testing.T) {
	value := PeekBits64([]byte{0xAA, 0xCC}, 4)
	require.Equal(t, uint64(0xACC0000000000000), value)
}
