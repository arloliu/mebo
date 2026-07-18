package metadata

import (
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

func TestMetadataContracts(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	t.Cleanup(encoder.Finish)

	encoder.WriteSlice([]string{"host=api-1", "region=tw"})
	decoder := NewTagDecoder(engine)
	require.Equal(t, []string{"host=api-1", "region=tw"}, collect(decoder.All(encoder.Bytes(), encoder.Len())))

	names := []string{"cpu.usage", "memory.total"}
	encoded, err := EncodeMetricNames(names, engine)
	require.NoError(t, err)
	decoded, _, err := DecodeMetricNames(encoded, engine)
	require.NoError(t, err)
	require.Equal(t, names, decoded)
	require.NoError(t, VerifyMetricNamesHashes(decoded, []uint64{9, 12}, func(name string) uint64 {
		return uint64(len(name))
	}))
}

func collect(sequence func(func(string) bool)) []string {
	var values []string
	sequence(func(value string) bool {
		values = append(values, value)
		return true
	})

	return values
}
