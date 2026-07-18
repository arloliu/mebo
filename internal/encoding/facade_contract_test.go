package encoding

import (
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/encoding/timestamp/delta"
	"github.com/arloliu/mebo/internal/encoding/timestamp/deltapacked"
	"github.com/arloliu/mebo/internal/encoding/value/alp"
	"github.com/stretchr/testify/require"
)

func TestMetadataFacadeContracts(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	t.Cleanup(encoder.Finish)
	encoder.Write("host=api-1")

	decoder := NewTagDecoder(engine)
	requireTagDecoderAlias(t, decoder)
	tags := make([]string, 0, encoder.Len())
	for tag := range decoder.All(encoder.Bytes(), encoder.Len()) {
		tags = append(tags, tag)
	}
	require.Equal(t, []string{"host=api-1"}, tags)

	strings := NewVarStringEncoder(engine)
	t.Cleanup(strings.Reset)
	require.NoError(t, strings.Write("ok"))
	require.Equal(t, []byte{2, 'o', 'k'}, strings.Bytes())

	names := []string{"cpu.usage"}
	encoded, err := EncodeMetricNames(names, engine)
	require.NoError(t, err)
	decoded, _, err := DecodeMetricNames(encoded, engine)
	require.NoError(t, err)
	require.Equal(t, names, decoded)
	require.NoError(t, VerifyMetricNamesHashes(decoded, []uint64{9}, func(name string) uint64 {
		return uint64(len(name))
	}))
	require.Equal(t, 255, MaxTextLength)
}

func TestTimestampRawFacadeContracts(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	t.Cleanup(encoder.Finish)
	encoder.WriteSlice([]int64{-1, 0, 1})

	safe := NewTimestampRawDecoder(engine)
	unsafe := NewTimestampRawUnsafeDecoder(engine)
	values := make([]int64, encoder.Len())
	require.Equal(t, encoder.Len(), safe.DecodeAll(encoder.Bytes(), encoder.Len(), values))
	require.Equal(t, []int64{-1, 0, 1}, values)

	got, ok := unsafe.At(encoder.Bytes(), 2, encoder.Len())
	require.True(t, ok)
	require.Equal(t, int64(1), got)

	var indexes []int
	RawTimestampsEach(encoder.Bytes(), encoder.Len(), engine, false, func(index int, _ int64) bool {
		indexes = append(indexes, index)
		return true
	})
	require.Equal(t, []int{0, 1, 2}, indexes)
}

func TestTimestampSimple8bFacadeContracts(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampSimple8bEncoder(engine)
	t.Cleanup(encoder.Finish)
	timestamps := []int64{1_000_000, 2_000_000, 3_000_100, 4_000_000}
	encoder.WriteSlice(timestamps)

	decoder := NewTimestampSimple8bDecoder(engine)
	decoded := make([]int64, 0, len(timestamps))
	for timestamp := range decoder.All(encoder.Bytes(), encoder.Len()) {
		decoded = append(decoded, timestamp)
	}
	require.Equal(t, timestamps, decoded)
}

func TestTimestampDeltaFacadeContracts(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
	requireDeltaFacadeTypes(t, encoder, NewTimestampDeltaDecoder())
	t.Cleanup(encoder.Finish)
	encoder.WriteSlice([]int64{1_000_000, 2_000_000, 3_000_100})

	decoder := NewTimestampDeltaDecoder()
	values := make([]int64, encoder.Len())
	require.Equal(t, encoder.Len(), decoder.DecodeAll(encoder.Bytes(), encoder.Len(), values))
	require.Equal(t, []int64{1_000_000, 2_000_000, 3_000_100}, values)

	state, ok := NewDeltaTsState(encoder.Bytes())
	require.True(t, ok)
	require.True(t, state.Next(encoder.Bytes()))
	require.Equal(t, int64(2_000_000), state.Ts())
}

func requireDeltaFacadeTypes(t *testing.T, encoder *delta.TimestampDeltaEncoder, decoder delta.TimestampDeltaDecoder) {
	t.Helper()
	require.NotNil(t, encoder)
	require.NotNil(t, decoder.All)
}

func TestTimestampDeltaPackedFacadeContracts(t *testing.T) {
	encoder := NewTimestampDeltaPackedEncoder()
	requireDeltaPackedFacadeTypes(t, encoder, NewTimestampDeltaPackedDecoder())
	t.Cleanup(encoder.Finish)
	encoder.WriteSlice([]int64{1_000_000, 2_000_000, 3_000_100, 4_000_000})

	decoder := NewTimestampDeltaPackedDecoder()
	values := make([]int64, encoder.Len())
	require.Equal(t, encoder.Len(), decoder.DecodeAll(encoder.Bytes(), encoder.Len(), values))
	require.Equal(t, []int64{1_000_000, 2_000_000, 3_000_100, 4_000_000}, values)
}

func requireDeltaPackedFacadeTypes(
	t *testing.T,
	encoder *deltapacked.TimestampDeltaPackedEncoder,
	decoder deltapacked.TimestampDeltaPackedDecoder,
) {
	t.Helper()
	require.NotNil(t, encoder)
	require.NotNil(t, decoder.All)
}

func TestNumericRawFacadeContracts(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	t.Cleanup(encoder.Finish)
	encoder.WriteSlice([]float64{-1.5, 0, 1.5})

	safe := NewNumericRawDecoder(engine)
	unsafe := NewNumericRawUnsafeDecoder(engine)
	values := make([]float64, encoder.Len())
	require.Equal(t, encoder.Len(), safe.DecodeAll(encoder.Bytes(), encoder.Len(), values))
	require.Equal(t, []float64{-1.5, 0, 1.5}, values)

	got, ok := unsafe.At(encoder.Bytes(), 2, encoder.Len())
	require.True(t, ok)
	require.Equal(t, 1.5, got)

	var indexes []int
	RawValuesEach(encoder.Bytes(), encoder.Len(), engine, false, func(index int, _ float64) bool {
		indexes = append(indexes, index)
		return true
	})
	require.Equal(t, []int{0, 1, 2}, indexes)
}

func TestNumericGorillaFacadeContracts(t *testing.T) {
	encoder := NewNumericGorillaEncoder()
	t.Cleanup(encoder.Finish)
	encoder.WriteSlice([]float64{1.5, 1.5, 2.75})

	decoder := NewNumericGorillaDecoder()
	values := make([]float64, encoder.Len())
	require.Equal(t, encoder.Len(), decoder.DecodeAll(encoder.Bytes(), encoder.Len(), values))
	require.Equal(t, []float64{1.5, 1.5, 2.75}, values)

	state, ok := NewGorillaValState(encoder.Bytes())
	require.True(t, ok)
	state.SetCount(encoder.Len())
	require.True(t, state.Next())
	require.Equal(t, 1.5, state.Val())
}

func TestNumericChimpFacadeContracts(t *testing.T) {
	encoder := NewNumericChimpEncoder()
	t.Cleanup(encoder.Finish)
	encoder.WriteSlice([]float64{1.5, 1.5, 2.75})

	decoder := NewNumericChimpDecoder()
	values := make([]float64, encoder.Len())
	require.Equal(t, encoder.Len(), decoder.DecodeAll(encoder.Bytes(), encoder.Len(), values))
	require.Equal(t, []float64{1.5, 1.5, 2.75}, values)

	state, ok := NewChimpValState(encoder.Bytes())
	require.True(t, ok)
	state.SetCount(encoder.Len())
	require.True(t, state.Next())
	require.Equal(t, 1.5, state.Val())
}

func TestNumericALPFacadeContracts(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericALPEncoder(engine)
	requireALPFacadeTypes(t, encoder, NewNumericALPDecoder(engine))
	t.Cleanup(encoder.Finish)
	encoder.WriteSlice([]float64{12.34, 12.34, 12.35})

	decoder := NewNumericALPDecoder(engine)
	values := make([]float64, encoder.Len())
	require.Equal(t, encoder.Len(), decoder.DecodeAll(encoder.Bytes(), encoder.Len(), values))
	require.Equal(t, []float64{12.34, 12.34, 12.35}, values)
}

func requireALPFacadeTypes(t *testing.T, encoder *alp.NumericALPEncoder, decoder alp.NumericALPDecoder) {
	t.Helper()
	require.NotNil(t, encoder)
	require.NotNil(t, decoder.All)
}

func requireTagDecoderAlias(t *testing.T, decoder TagDecoder) {
	t.Helper()
	require.NotNil(t, decoder.All)
}
