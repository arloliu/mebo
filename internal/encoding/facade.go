package encoding

import (
	"iter"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/encoding/fused"
	"github.com/arloliu/mebo/internal/encoding/metadata"
	"github.com/arloliu/mebo/internal/encoding/timestamp/delta"
	"github.com/arloliu/mebo/internal/encoding/timestamp/deltapacked"
	tsraw "github.com/arloliu/mebo/internal/encoding/timestamp/raw"
	"github.com/arloliu/mebo/internal/encoding/timestamp/simple8b"
	"github.com/arloliu/mebo/internal/encoding/value/alp"
	"github.com/arloliu/mebo/internal/encoding/value/chimp"
	"github.com/arloliu/mebo/internal/encoding/value/gorilla"
	valraw "github.com/arloliu/mebo/internal/encoding/value/raw"
)

const (
	// MaxTextLength is the maximum encoded byte length for text values and tags.
	MaxTextLength = metadata.MaxTextLength

	// ALPMaxSchemeByte is the highest valid ALP scheme byte.
	ALPMaxSchemeByte = alp.ALPMaxSchemeByte

	// ALPRDMaxDictSize is the maximum ALP-RD dictionary size.
	ALPRDMaxDictSize = alp.ALPRDMaxDictSize
)

// TagEncoder encodes tag strings in the established length-prefixed format.
type TagEncoder = metadata.TagEncoder

// TagDecoder decodes tag strings in the established length-prefixed format.
type TagDecoder = metadata.TagDecoder

// VarStringEncoder encodes variable-length text values and timestamp varints.
type VarStringEncoder = metadata.VarStringEncoder

// TimestampRawEncoder encodes fixed-width timestamps.
type TimestampRawEncoder = tsraw.TimestampRawEncoder

// TimestampRawDecoder decodes fixed-width timestamps safely.
type TimestampRawDecoder = tsraw.TimestampRawDecoder

// TimestampRawUnsafeDecoder decodes fixed-width native-endian timestamps.
type TimestampRawUnsafeDecoder = tsraw.TimestampRawUnsafeDecoder

// TimestampSimple8bEncoder encodes timestamps with Simple8b packing.
type TimestampSimple8bEncoder = simple8b.TimestampSimple8bEncoder

// TimestampSimple8bDecoder decodes Simple8b-packed timestamps.
type TimestampSimple8bDecoder = simple8b.TimestampSimple8bDecoder

// TimestampDeltaEncoder encodes delta-of-delta timestamps.
type TimestampDeltaEncoder = delta.TimestampDeltaEncoder

// TimestampDeltaDecoder decodes delta-of-delta timestamps.
type TimestampDeltaDecoder = delta.TimestampDeltaDecoder

// DeltaTsState incrementally decodes delta-of-delta timestamps.
type DeltaTsState = delta.DeltaTsState

// TimestampDeltaPackedEncoder encodes Group Varint packed delta-of-delta timestamps.
type TimestampDeltaPackedEncoder = deltapacked.TimestampDeltaPackedEncoder

// TimestampDeltaPackedDecoder decodes Group Varint packed delta-of-delta timestamps.
type TimestampDeltaPackedDecoder = deltapacked.TimestampDeltaPackedDecoder

// NumericRawEncoder encodes fixed-width float values.
type NumericRawEncoder = valraw.NumericRawEncoder

// NumericRawDecoder decodes fixed-width float values safely.
type NumericRawDecoder = valraw.NumericRawDecoder

// NumericRawUnsafeDecoder decodes fixed-width native-endian float values.
type NumericRawUnsafeDecoder = valraw.NumericRawUnsafeDecoder

// NumericGorillaEncoder encodes Gorilla-compressed float values.
type NumericGorillaEncoder = gorilla.NumericGorillaEncoder

// NumericGorillaDecoder decodes Gorilla-compressed float values.
type NumericGorillaDecoder = gorilla.NumericGorillaDecoder

// GorillaValState incrementally decodes Gorilla-compressed float values.
type GorillaValState = gorilla.GorillaValState

// NumericChimpEncoder encodes Chimp-compressed float values.
type NumericChimpEncoder = chimp.NumericChimpEncoder

// NumericChimpDecoder decodes Chimp-compressed float values.
type NumericChimpDecoder = chimp.NumericChimpDecoder

// ChimpValState incrementally decodes Chimp-compressed float values.
type ChimpValState = chimp.ChimpValState

// NumericALPEncoder encodes adaptive lossless floating-point values.
type NumericALPEncoder = alp.NumericALPEncoder

// NumericALPDecoder decodes adaptive lossless floating-point values.
type NumericALPDecoder = alp.NumericALPDecoder

// NewTagEncoder creates a tag encoder using engine.
func NewTagEncoder(engine endian.EndianEngine) *TagEncoder {
	return metadata.NewTagEncoder(engine)
}

// NewTagDecoder creates a tag decoder using engine.
func NewTagDecoder(engine endian.EndianEngine) TagDecoder {
	return metadata.NewTagDecoder(engine)
}

// NewVarStringEncoder creates a variable-length string encoder using engine.
func NewVarStringEncoder(engine endian.EndianEngine) *VarStringEncoder {
	return metadata.NewVarStringEncoder(engine)
}

// NewTimestampRawEncoder creates a fixed-width timestamp encoder using engine.
func NewTimestampRawEncoder(engine endian.EndianEngine) *TimestampRawEncoder {
	return tsraw.NewTimestampRawEncoder(engine)
}

// NewTimestampRawDecoder creates a safe fixed-width timestamp decoder using engine.
func NewTimestampRawDecoder(engine endian.EndianEngine) TimestampRawDecoder {
	return tsraw.NewTimestampRawDecoder(engine)
}

// NewTimestampRawUnsafeDecoder creates an unsafe fixed-width timestamp decoder.
func NewTimestampRawUnsafeDecoder(engine endian.EndianEngine) TimestampRawUnsafeDecoder {
	return tsraw.NewTimestampRawUnsafeDecoder(engine)
}

// NewTimestampSimple8bEncoder creates a Simple8b timestamp encoder.
func NewTimestampSimple8bEncoder(engine endian.EndianEngine) *TimestampSimple8bEncoder {
	return simple8b.NewTimestampSimple8bEncoder(engine)
}

// NewTimestampSimple8bDecoder creates a Simple8b timestamp decoder.
func NewTimestampSimple8bDecoder(engine endian.EndianEngine) TimestampSimple8bDecoder {
	return simple8b.NewTimestampSimple8bDecoder(engine)
}

// NewTimestampDeltaEncoder creates a delta-of-delta timestamp encoder.
func NewTimestampDeltaEncoder() *TimestampDeltaEncoder {
	return delta.NewTimestampDeltaEncoder()
}

// NewTimestampDeltaDecoder creates a delta-of-delta timestamp decoder.
func NewTimestampDeltaDecoder() TimestampDeltaDecoder {
	return delta.NewTimestampDeltaDecoder()
}

// NewTimestampDeltaPackedEncoder creates a Group Varint packed delta-of-delta timestamp encoder.
func NewTimestampDeltaPackedEncoder() *TimestampDeltaPackedEncoder {
	return deltapacked.NewTimestampDeltaPackedEncoder()
}

// NewTimestampDeltaPackedDecoder creates a Group Varint packed delta-of-delta timestamp decoder.
func NewTimestampDeltaPackedDecoder() TimestampDeltaPackedDecoder {
	return deltapacked.NewTimestampDeltaPackedDecoder()
}

// NewDeltaTsState creates an incremental delta-of-delta timestamp decoder.
func NewDeltaTsState(data []byte) (DeltaTsState, bool) {
	return delta.NewDeltaTsState(data)
}

// NewNumericRawEncoder creates a fixed-width float encoder using engine.
func NewNumericRawEncoder(engine endian.EndianEngine) *NumericRawEncoder {
	return valraw.NewNumericRawEncoder(engine)
}

// NewNumericRawDecoder creates a safe fixed-width float decoder using engine.
func NewNumericRawDecoder(engine endian.EndianEngine) NumericRawDecoder {
	return valraw.NewNumericRawDecoder(engine)
}

// NewNumericRawUnsafeDecoder creates an unsafe fixed-width float decoder.
func NewNumericRawUnsafeDecoder(engine endian.EndianEngine) NumericRawUnsafeDecoder {
	return valraw.NewNumericRawUnsafeDecoder(engine)
}

// NewNumericGorillaEncoder creates a Gorilla float encoder.
func NewNumericGorillaEncoder() *NumericGorillaEncoder {
	return gorilla.NewNumericGorillaEncoder()
}

// NewNumericGorillaDecoder creates a Gorilla float decoder.
func NewNumericGorillaDecoder() NumericGorillaDecoder {
	return gorilla.NewNumericGorillaDecoder()
}

// NewGorillaValState creates an incremental Gorilla decoder state.
func NewGorillaValState(data []byte) (GorillaValState, bool) {
	return gorilla.NewGorillaValState(data)
}

// NewNumericChimpEncoder creates a Chimp float encoder.
func NewNumericChimpEncoder() *NumericChimpEncoder {
	return chimp.NewNumericChimpEncoder()
}

// NewNumericChimpDecoder creates a Chimp float decoder.
func NewNumericChimpDecoder() NumericChimpDecoder {
	return chimp.NewNumericChimpDecoder()
}

// NewChimpValState creates an incremental Chimp decoder state.
func NewChimpValState(data []byte) (ChimpValState, bool) {
	return chimp.NewChimpValState(data)
}

// NewNumericALPEncoder creates an adaptive lossless floating-point encoder.
func NewNumericALPEncoder(engine endian.EndianEngine) *NumericALPEncoder {
	return alp.NewNumericALPEncoder(engine)
}

// NewNumericALPDecoder creates an adaptive lossless floating-point decoder.
func NewNumericALPDecoder(engine endian.EndianEngine) NumericALPDecoder {
	return alp.NewNumericALPDecoder(engine)
}

// FusedDeltaGorillaEach decodes Delta timestamps and Gorilla values together.
func FusedDeltaGorillaEach(tsData, valData []byte, count int, yield func(int, int64, float64) bool) {
	fused.FusedDeltaGorillaEach(tsData, valData, count, yield)
}

// FusedDeltaChimpEach decodes Delta timestamps and Chimp values together.
func FusedDeltaChimpEach(tsData, valData []byte, count int, yield func(int, int64, float64) bool) {
	fused.FusedDeltaChimpEach(tsData, valData, count, yield)
}

// FusedDeltaPackedGorillaEach decodes packed Delta timestamps and Gorilla values together.
func FusedDeltaPackedGorillaEach(tsData, valData []byte, count int, yield func(int, int64, float64) bool) {
	fused.FusedDeltaPackedGorillaEach(tsData, valData, count, yield)
}

// FusedDeltaPackedChimpEach decodes packed Delta timestamps and Chimp values together.
func FusedDeltaPackedChimpEach(tsData, valData []byte, count int, yield func(int, int64, float64) bool) {
	fused.FusedDeltaPackedChimpEach(tsData, valData, count, yield)
}

// FusedDeltaEach decodes Delta timestamps through yield.
func FusedDeltaEach(tsData []byte, count int, yield func(int, int64) bool) {
	fused.FusedDeltaEach(tsData, count, yield)
}

// FusedGorillaEach decodes Gorilla values through yield.
func FusedGorillaEach(valData []byte, count int, yield func(int, float64) bool) {
	fused.FusedGorillaEach(valData, count, yield)
}

// FusedChimpEach decodes Chimp values through yield.
func FusedChimpEach(valData []byte, count int, yield func(int, float64) bool) {
	fused.FusedChimpEach(valData, count, yield)
}

// FusedDeltaPackedEach decodes packed Delta timestamps through yield.
func FusedDeltaPackedEach(tsData []byte, count int, yield func(int, int64) bool) {
	fused.FusedDeltaPackedEach(tsData, count, yield)
}

// FusedDeltaGorillaAll returns fused Delta and Gorilla values.
func FusedDeltaGorillaAll(tsData, valData []byte, count int) iter.Seq2[int64, float64] {
	return fused.FusedDeltaGorillaAll(tsData, valData, count)
}

// FusedDeltaGorillaTagAll decodes Delta, Gorilla, and tags together.
func FusedDeltaGorillaTagAll(tsData, valData, tagData []byte, count int, yield func(int, int64, float64, string) bool) {
	fused.FusedDeltaGorillaTagAll(tsData, valData, tagData, count, yield)
}

// FusedDeltaTagAll decodes Delta timestamps and tags together.
func FusedDeltaTagAll(tsData, tagData []byte, count int, yield func(int, int64, string) bool) {
	fused.FusedDeltaTagAll(tsData, tagData, count, yield)
}

// FusedGorillaTagAll decodes Gorilla values and tags together.
func FusedGorillaTagAll(valData, tagData []byte, count int, yield func(int, float64, string) bool) {
	fused.FusedGorillaTagAll(valData, tagData, count, yield)
}

// FusedDeltaChimpAll returns fused Delta and Chimp values.
func FusedDeltaChimpAll(tsData, valData []byte, count int) iter.Seq2[int64, float64] {
	return fused.FusedDeltaChimpAll(tsData, valData, count)
}

// FusedDeltaChimpTagAll decodes Delta, Chimp, and tags together.
func FusedDeltaChimpTagAll(tsData, valData, tagData []byte, count int, yield func(int, int64, float64, string) bool) {
	fused.FusedDeltaChimpTagAll(tsData, valData, tagData, count, yield)
}

// FusedChimpTagAll decodes Chimp values and tags together.
func FusedChimpTagAll(valData, tagData []byte, count int, yield func(int, float64, string) bool) {
	fused.FusedChimpTagAll(valData, tagData, count, yield)
}

// FusedDeltaPackedGorillaAll returns fused packed Delta and Gorilla values.
func FusedDeltaPackedGorillaAll(tsData, valData []byte, count int) iter.Seq2[int64, float64] {
	return fused.FusedDeltaPackedGorillaAll(tsData, valData, count)
}

// FusedDeltaPackedGorillaTagAll decodes packed Delta, Gorilla, and tags together.
func FusedDeltaPackedGorillaTagAll(tsData, valData, tagData []byte, count int, yield func(int, int64, float64, string) bool) {
	fused.FusedDeltaPackedGorillaTagAll(tsData, valData, tagData, count, yield)
}

// FusedDeltaPackedChimpAll returns fused packed Delta and Chimp values.
func FusedDeltaPackedChimpAll(tsData, valData []byte, count int) iter.Seq2[int64, float64] {
	return fused.FusedDeltaPackedChimpAll(tsData, valData, count)
}

// FusedDeltaPackedChimpTagAll decodes packed Delta, Chimp, and tags together.
func FusedDeltaPackedChimpTagAll(tsData, valData, tagData []byte, count int, yield func(int, int64, float64, string) bool) {
	fused.FusedDeltaPackedChimpTagAll(tsData, valData, tagData, count, yield)
}

// FusedDeltaPackedTagAll decodes packed Delta timestamps and tags together.
func FusedDeltaPackedTagAll(tsData, tagData []byte, count int, yield func(int, int64, string) bool) {
	fused.FusedDeltaPackedTagAll(tsData, tagData, count, yield)
}

// RawTimestampsEach decodes raw timestamps and calls yield for each timestamp.
func RawTimestampsEach(data []byte, count int, engine endian.EndianEngine, nativeByteOrder bool, yield func(int, int64) bool) {
	tsraw.RawTimestampsEach(data, count, engine, nativeByteOrder, yield)
}

// RawValuesEach decodes raw values and calls yield for each value.
func RawValuesEach(data []byte, count int, engine endian.EndianEngine, nativeByteOrder bool, yield func(int, float64) bool) {
	valraw.RawValuesEach(data, count, engine, nativeByteOrder, yield)
}

// EncodeMetricNames encodes names into the established length-prefixed payload.
func EncodeMetricNames(names []string, engine endian.EndianEngine) ([]byte, error) {
	return metadata.EncodeMetricNames(names, engine)
}

// DecodeMetricNames decodes a length-prefixed metric-names payload.
func DecodeMetricNames(data []byte, engine endian.EndianEngine) ([]string, int, error) {
	return metadata.DecodeMetricNames(data, engine)
}

// VerifyMetricNamesHashes verifies names hash to the corresponding metric IDs.
func VerifyMetricNamesHashes(names []string, metricIDs []uint64, hashFunc func(string) uint64) error {
	return metadata.VerifyMetricNamesHashes(names, metricIDs, hashFunc)
}
