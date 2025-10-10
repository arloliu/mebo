package section

import (
	"math"

	"github.com/arloliu/mebo/format"
)

const (
	// Bit masks
	TagMask          = 0x0001 // Mask for tag bit (bit 0)
	EndiannessMask   = 0x0002 // Mask for endianness bit (bit 1)
	MetricNamesMask  = 0x0004 // Mask for metric names payload bit (bit 2)
	ReservedBitsMask = 0x0008 // Mask for reserved bit (bit 3)
	MagicNumberMask  = 0xFFF0 // Mask for magic number (bits 4-15)

	// Magic numbers (bits 4-15)
	MagicNumericV1Opt = 0xEA10 // MagicFloatV1 is a version 1 magic number for float blob format.
	MagicTextV1Opt    = 0xEB10 // MagicTextV1 is a version 1 magic number for text blob format.

	// Timestamp encodings (bits 0-3) - using types package constants
	TimestampEncodingNRaw = uint8(format.TypeRaw)   // TimestampTypeRaw represents raw timestamps with no format.
	TimestampTypeDelta    = uint8(format.TypeDelta) // TimestampTypeDelta represents delta encoding for timestamps.

	// Value encodings (bits 4-7) - using types package constants
	// Only applicable to float value blobs
	ValueTypeRaw     = uint8(format.TypeRaw) << 4     // ValueTypeRaw represents raw values with no format.
	ValueTypeGorilla = uint8(format.TypeGorilla) << 4 // ValueTypeGorilla represents Gorilla compression for values.

	// Timestamp compression (bits 0-3) - using types package constants
	TimestampCompressionNone = uint8(format.CompressionNone) // TimestampCompressionNone represents no compression for timestamps.
	TimestampCompressionZstd = uint8(format.CompressionZstd) // TimestampCompressionZstd represents Zstandard compression for timestamps.
	TimestampCompressionS2   = uint8(format.CompressionS2)   // TimestampCompressionS2 represents S2 compression for timestamps.
	TimestampCompressionLZ4  = uint8(format.CompressionLZ4)  // TimestampCompressionLZ4 represents LZ4 compression for timestamps.

	// Value compression (bits 4-7) - using types package constants
	// Only applicable to float value blobs
	ValueCompressionNone = uint8(format.CompressionNone) << 4 // ValueCompressionNone represents no compression for values.
	ValueCompressionZstd = uint8(format.CompressionZstd) << 4 // ValueCompressionZstd represents Zstandard compression for values.
	ValueCompressionS2   = uint8(format.CompressionS2) << 4   // ValueCompressionS2 represents S2 compression for values.
	ValueCompressionLZ4  = uint8(format.CompressionLZ4) << 4  // ValueCompressionLZ4 represents LZ4 compression for values.

	// Blob flags for packed uint16 field (optimized struct fields)
	FlagEndianLittleEndian = 0x0001 // 0=little, 1=big
	FlagTsEncRaw           = 0x0002 // 0=raw, 1=delta
	FlagValEncRaw          = 0x0004 // 0=raw, 1=gorilla
	FlagTagEnabled         = 0x0008 // 0=disabled, 1=enabled
	FlagMetricNames        = 0x0010 // 0=disabled, 1=enabled
)

// offset and section sizes in the blob file
const (
	HeaderSize            = 32             // fixed header size in bytes (shared by all blob types)
	NumericIndexEntrySize = 16             // fixed index entry size for numeric value blob in bytes
	TextIndexEntrySize    = 16             // fixed index entry size for text value blob in bytes
	IndexOffsetOffset     = HeaderSize     // byte offset where index section starts
	NumericMaxOffset      = math.MaxUint16 // maximum offset value of float value blob index
	TextMaxOffset         = math.MaxUint32 // maximum offset value of text value blob index
)
