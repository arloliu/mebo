package simple8b

import (
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

func TestSimple8bTimestampContract(t *testing.T) {
	values := []int64{1_700_000_000_000_000, 1_700_000_001_000_000, 1_700_000_002_000_000}
	encoder := NewTimestampSimple8bEncoder(endian.GetBigEndianEngine())
	t.Cleanup(encoder.Finish)
	encoder.WriteSlice(values)

	decoder := NewTimestampSimple8bDecoder(endian.GetBigEndianEngine())
	decoded := make([]int64, 0, len(values))
	for value := range decoder.All(encoder.Bytes(), len(values)) {
		decoded = append(decoded, value)
	}
	require.Equal(t, values, decoded)

	value, ok := decoder.At(encoder.Bytes(), 1, len(values))
	require.True(t, ok)
	require.Equal(t, values[1], value)
}
