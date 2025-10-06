package format

type (
	EncodingType    uint8
	CompressionType uint8
)

const (
	TypeRaw     EncodingType = 0x1 // TypeRaw represents raw data with no format.
	TypeDelta   EncodingType = 0x2 // TypeDelta represents delta-of-delta encoding.
	TypeGorilla EncodingType = 0x3 // TypeGorilla represents Gorilla encoding.

	CompressionNone CompressionType = 0x1 // CompressionNone represents no compression.
	CompressionZstd CompressionType = 0x2 // CompressionZstd represents Zstandard compression.
	CompressionS2   CompressionType = 0x3 // CompressionS2 represents S2 compression.
	CompressionLZ4  CompressionType = 0x4 // CompressionLZ4 represents LZ4 compression.

)

func (e EncodingType) String() string {
	switch e {
	case TypeRaw:
		return "Raw"
	case TypeDelta:
		return "Delta"
	case TypeGorilla:
		return "Gorilla"
	default:
		return "Unknown"
	}
}

func (c CompressionType) String() string {
	switch c {
	case CompressionNone:
		return "None"
	case CompressionZstd:
		return "Zstd"
	case CompressionS2:
		return "S2"
	case CompressionLZ4:
		return "LZ4"
	default:
		return "Unknown"
	}
}
