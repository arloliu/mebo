package section

import (
	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
)

// NumericFlag represents the packed field for various flags in the numeric header.
type NumericFlag struct {
	// Options is a packed field for various options.
	// Bit 0 is tag support flag, 0 means no tag, 1 means tag enabled.
	// Bit 1 is endianness flag, 0 means little-endian, 1 means big-endian.
	// Bit 2-3 are reserved for future use, must be set to 0.
	// Bit 4-15 are magic number to identify the blob format:
	//   - 0xEA10 (0b1110_1010_0001_0000): Float value blob format v1
	//   - 0xEA20 (0b1110_1010_0010_0000): Text value blob format v1
	Options uint16

	// EncodingType is an enum indicating the encoding used for this metric blob.
	// bit 0-3 for timestamp encoding, bit 4-7 for value format.
	EncodingType uint8
	// CompressionType is an enum indicating the compression used for this metric blob.
	// bit 0-3 for timestamp compression, bit 4-7 for value compression.
	CompressionType uint8
}

var (
	validTimestampEncodings = map[uint8]struct{}{
		uint8(format.TypeRaw):   {},
		uint8(format.TypeDelta): {},
	}

	validValueEncodings = map[uint8]struct{}{
		uint8(format.TypeRaw):     {},
		uint8(format.TypeGorilla): {},
	}

	validTimestampCompressions = map[uint8]struct{}{
		uint8(format.CompressionNone): {},
		uint8(format.CompressionZstd): {},
		uint8(format.CompressionS2):   {},
		uint8(format.CompressionLZ4):  {},
	}

	validValueCompressions = map[uint8]struct{}{
		uint8(format.CompressionNone): {},
		uint8(format.CompressionZstd): {},
		uint8(format.CompressionS2):   {},
		uint8(format.CompressionLZ4):  {},
	}
)

// NewNumericFlag creates a new NumericFlag with default settings.
func NewNumericFlag() NumericFlag {
	flag := NumericFlag{
		Options:         MagicFloatV1Opt,
		EncodingType:    TimestampEncodingNRaw | ValueTypeRaw,
		CompressionType: TimestampCompressionNone | ValueCompressionZstd,
	}
	flag.WithLittleEndian()

	return flag
}

// HasTag returns whether tag is enabled.
func (f NumericFlag) HasTag() bool {
	return (f.Options & TagMask) != 0
}

// WithTag enables tag support.
func (f *NumericFlag) WithTag() {
	f.Options |= TagMask
}

// WithoutTag disables tag support.
func (f *NumericFlag) WithoutTag() {
	f.Options &^= TagMask
}

// HasMetricNames returns whether metric names payload is enabled.
// When enabled, the blob includes a metric names section for collision detection and verification.
func (f NumericFlag) HasMetricNames() bool {
	return (f.Options & MetricNamesMask) != 0
}

// SetHasMetricNames enables or disables metric names payload.
// The metric names payload is used to store original metric name strings for collision detection.
func (f *NumericFlag) SetHasMetricNames(enabled bool) {
	if enabled {
		f.Options |= MetricNamesMask
	} else {
		f.Options &^= MetricNamesMask
	}
}

// IsLittleEndian returns whether the data is little-endian.
func (f NumericFlag) IsLittleEndian() bool {
	return (f.Options & EndiannessMask) == 0
}

// IsBigEndian returns whether the data is big-endian.
func (f NumericFlag) IsBigEndian() bool {
	return (f.Options & EndiannessMask) != 0
}

// WithLittleEndian sets little-endian byte order.
func (f *NumericFlag) WithLittleEndian() {
	f.Options &= ^uint16(EndiannessMask)
}

// WithBigEndian sets big-endian byte order.
func (f *NumericFlag) WithBigEndian() {
	f.Options |= EndiannessMask
}

// GetMagicNumber returns the magic number from the Options field.
func (f NumericFlag) GetMagicNumber() uint16 {
	return f.Options & MagicNumberMask
}

// TimestampEncoding returns the timestamp encoding type from bits 0-3 of EncodingType.
func (f NumericFlag) TimestampEncoding() format.EncodingType {
	return format.EncodingType(f.EncodingType & 0x0F)
}

// SetTimestampEncoding sets the timestamp encoding type in bits 0-3 of EncodingType.
func (f *NumericFlag) SetTimestampEncoding(enc format.EncodingType) {
	f.EncodingType &^= 0x0F // Clear bits 0-3
	f.EncodingType |= (uint8(enc) & 0x0F)
}

// ValueEncoding returns the value encoding type from bits 4-7 of EncodingType.
func (f NumericFlag) ValueEncoding() format.EncodingType {
	return format.EncodingType((f.EncodingType >> 4) & 0x0F)
}

// SetValueEncoding sets the value encoding type in bits 4-7 of EncodingType.
func (f *NumericFlag) SetValueEncoding(enc format.EncodingType) {
	f.EncodingType &^= 0xF0 // Clear bits 4-7
	f.EncodingType |= (uint8(enc) & 0x0F) << 4
}

// TimestampCompression returns the timestamp compression type from bits 0-3 of CompressionType.
func (f NumericFlag) TimestampCompression() format.CompressionType {
	return format.CompressionType(f.CompressionType & 0x0F)
}

// SetTimestampCompression sets the timestamp compression type in bits 0-3 of CompressionType.
func (f *NumericFlag) SetTimestampCompression(compression format.CompressionType) {
	f.CompressionType &^= 0x0F // Clear bits 0-3
	f.CompressionType |= (uint8(compression) & 0x0F)
}

// ValueCompression returns the value compression type from bits 4-7 of CompressionType.
func (f NumericFlag) ValueCompression() format.CompressionType {
	return format.CompressionType((f.CompressionType >> 4) & 0x0F)
}

// SetValueCompression sets the value compression type in bits 4-7 of CompressionType.
func (f *NumericFlag) SetValueCompression(compression format.CompressionType) {
	f.CompressionType &^= 0xF0 // Clear bits 4-7
	f.CompressionType |= (uint8(compression) & 0x0F) << 4
}

// IsValidMagicNumber checks if the magic number is valid.
func (f NumericFlag) IsValidMagicNumber() bool {
	return f.GetMagicNumber() == MagicFloatV1Opt
}

// IsValidEncoding checks if the encoding types are valid.
func (f NumericFlag) IsValidEncoding() bool {
	timestampEncoding := f.EncodingType & 0x0F
	valueEncoding := (f.EncodingType >> 4) & 0x0F

	_, validTimestamp := validTimestampEncodings[timestampEncoding]
	_, validValue := validValueEncodings[valueEncoding]

	return validTimestamp && validValue
}

// IsValidCompression checks if the compression types are valid.
func (f NumericFlag) IsValidCompression() bool {
	timestampCompression := f.CompressionType & 0x0F
	valueCompression := (f.CompressionType >> 4) & 0x0F

	_, validTimestamp := validTimestampCompressions[timestampCompression]
	_, validValue := validValueCompressions[valueCompression]

	return validTimestamp && validValue
}

// Validate checks if the flag header contains valid values.
func (f NumericFlag) Validate() error {
	if !f.IsValidMagicNumber() {
		return errs.ErrInvalidHeaderFlags
	}

	if !f.IsValidEncoding() {
		return errs.ErrInvalidHeaderFlags
	}

	if !f.IsValidCompression() {
		return errs.ErrInvalidHeaderFlags
	}

	return nil
}

// GetEndianEngine returns the appropriate endian engine based on the flag.
func (f NumericFlag) GetEndianEngine() endian.EndianEngine {
	if f.IsLittleEndian() {
		return endian.GetLittleEndianEngine()
	}

	return endian.GetBigEndianEngine()
}
