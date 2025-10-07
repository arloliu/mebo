package section

import (
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
)

// TextFlag represents the packed field for various flags in the text header.
// This is specific to text value blobs and simpler than the float value NumericFlag.
type TextFlag struct {
	// Options is a packed field for various options.
	// Bit 0 is tag flag, 0 means no tags, 1 means per-point tags are present.
	// Bit 1 is endianness flag, 0 means little-endian, 1 means big-endian.
	// Bit 2 is metric names payload flag, 0 means no metric names, 1 means metric names payload is present.
	// Bit 3 is reserved for future use, must be set to 0.
	// Bits 4-15 are magic number to identify the blob format:
	//   - 0xEB10 (0b1110_1011_0001_0000): Text value blob format v1
	Options uint16

	// TimestampEncoding indicates the encoding used for timestamps.
	// Valid values: TypeRaw, TypeDelta
	TimestampEncoding uint8

	// DataCompression indicates the compression used for the data section.
	// Valid values: CompressionNone, CompressionZstd, CompressionS2, CompressionLz4
	DataCompression uint8
}

// NewTextFlag creates a new TextFlag with default settings.
func NewTextFlag() TextFlag {
	flag := TextFlag{
		Options:           MagicTextV1Opt,
		TimestampEncoding: uint8(format.TypeRaw),
		DataCompression:   uint8(format.CompressionZstd),
	}
	flag.WithLittleEndian()

	return flag
}

// HasTag returns whether tag is enabled.
func (f TextFlag) HasTag() bool {
	return (f.Options & TagMask) != 0
}

// WithTag enables tag support.
func (f *TextFlag) WithTag() {
	f.Options |= TagMask
}

// WithoutTag disables tag support.
func (f *TextFlag) WithoutTag() {
	f.Options &^= TagMask
}

// HasMetricNames returns whether metric names payload is enabled.
// When enabled, the blob includes a metric names section for collision detection and verification.
func (f TextFlag) HasMetricNames() bool {
	return (f.Options & MetricNamesMask) != 0
}

// SetHasMetricNames enables or disables metric names payload.
// The metric names payload is used to store original metric name strings for collision detection.
func (f *TextFlag) SetHasMetricNames(enabled bool) {
	if enabled {
		f.Options |= MetricNamesMask
	} else {
		f.Options &^= MetricNamesMask
	}
}

// IsValidMagicNumber checks if the magic number in the Options field is valid.
func (f TextFlag) IsValidMagicNumber() bool {
	return f.GetMagicNumber() == MagicTextV1Opt
}

// IsLittleEndian returns whether the data is little-endian.
func (f TextFlag) IsLittleEndian() bool {
	return (f.Options & EndiannessMask) == 0
}

// IsBigEndian returns whether the data is big-endian.
func (f TextFlag) IsBigEndian() bool {
	return (f.Options & EndiannessMask) != 0
}

// WithLittleEndian sets little-endian byte order.
func (f *TextFlag) WithLittleEndian() {
	f.Options &= ^uint16(EndiannessMask)
}

// WithBigEndian sets big-endian byte order.
func (f *TextFlag) WithBigEndian() {
	f.Options |= EndiannessMask
}

// GetMagicNumber returns the magic number from the Options field.
func (f TextFlag) GetMagicNumber() uint16 {
	return f.Options & MagicNumberMask
}

// SetTimestampEncoding sets the timestamp encoding type.
func (f *TextFlag) SetTimestampEncoding(encoding format.EncodingType) {
	f.TimestampEncoding = uint8(encoding)
}

// GetTimestampEncoding returns the timestamp encoding type.
func (f TextFlag) GetTimestampEncoding() format.EncodingType {
	return format.EncodingType(f.TimestampEncoding)
}

// SetDataCompression sets the data compression type.
func (f *TextFlag) SetDataCompression(compression format.CompressionType) {
	f.DataCompression = uint8(compression)
}

// GetDataCompression returns the data compression type.
func (f TextFlag) GetDataCompression() format.CompressionType {
	return format.CompressionType(f.DataCompression)
}

// Validate checks if the flag header contains valid values.
func (f TextFlag) Validate() error {
	// Check magic number
	if f.GetMagicNumber() != MagicTextV1Opt {
		return errs.ErrInvalidHeaderFlags
	}

	// Check reserved bits are zero
	if (f.Options & ReservedBitsMask) != 0 {
		return errs.ErrInvalidHeaderFlags
	}

	// Validate timestamp encoding
	if _, ok := validTimestampEncodings[f.TimestampEncoding]; !ok {
		return errs.ErrInvalidHeaderFlags
	}

	// Validate data compression (use same map as timestamp compressions - they're the same)
	if _, ok := validTimestampCompressions[f.DataCompression]; !ok {
		return errs.ErrInvalidHeaderFlags
	}

	return nil
}
