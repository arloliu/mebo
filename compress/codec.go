package compress

import (
	"fmt"

	"github.com/arloliu/mebo/format"
)

// Compressor provides high-performance compression and decompression for mebo time-series data.
//
// The interface is optimized for mebo's columnar time-series data where:
//   - Timestamp payloads: Typically delta-encoded, highly compressible
//   - Value payloads: May be raw float64 or Gorilla-encoded
//   - Payload sizes: Usually 1KB-64KB per payload
type Compressor interface {
	// Compress compresses the input data and returns the compressed result.
	//
	// The input data typically represents a complete mebo payload (timestamps or values)
	// that has already been encoded using the appropriate encoding strategy.
	//
	// Memory management:
	//   - Returned slice is newly allocated and owned by the caller
	//   - Input slice is not modified
	//   - Internal buffers may be reused for efficiency
	Compress(data []byte) ([]byte, error)
}

// Decompressor provides high-performance decompression for mebo compressed data.
//
// This interface mirrors the Compressor interface but focuses on the decompression
// operation. Separate interfaces allow for asymmetric implementations where
// compression and decompression may have different performance characteristics
// or resource requirements.
//
// Example:
//
//	decompressor := NewZstdDecompressor()
//	originalData, err := decompressor.Decompress(compressedPayload)
//	if err != nil {
//	    return fmt.Errorf("decompression failed: %w", err)
//	}
//
// Thread Safety: Decompressor implementations must be safe for concurrent use
// or document their thread safety requirements clearly.
type Decompressor interface {
	// Decompress decompresses the input data and returns the original result.
	//
	// The input data should be previously compressed using the same compression
	// algorithm. The decompressor validates the data format and returns an error
	// if the data is corrupted or uses an incompatible format.
	//
	// Performance expectations:
	//   - Decompression is typically 2-5x faster than compression
	//   - Memory overhead: 1-2x output size for decompression buffers
	//   - Output size: Determined by original data size (stored in compressed format)
	//
	// Error conditions:
	//   - Returns error if input data is corrupted or invalid
	//   - Returns error if data was compressed with incompatible algorithm
	//   - Returns error if decompression buffer allocation fails
	//
	// Memory management:
	//   - Returned slice is newly allocated and owned by the caller
	//   - Input slice is not modified
	//   - Internal buffers may be reused for efficiency
	Decompress(data []byte) ([]byte, error)
}

// Codec combines both compression and decompression capabilities.
//
// This interface is useful for implementations that can handle both operations
// efficiently with shared internal state or optimizations.
type Codec interface {
	Compressor
	Decompressor
}

// CompressionStats provides detailed information about compression operations.
//
// This is useful for monitoring, profiling, and optimization of compression
// performance in production time-series systems.
type CompressionStats struct {
	// Algorithm identifies the compression algorithm used
	Algorithm format.CompressionType

	// OriginalSize is the size of input data before compression
	OriginalSize int64

	// CompressedSize is the size of data after compression
	CompressedSize int64

	// Ratio is the ratio of compressed size to original size (< 1.0 for compression)
	Ratio float64

	// CompressionTime is the time taken to compress the data
	CompressionTimeNs int64

	// DecompressionTime is the time taken to decompress the data (if applicable)
	DecompressionTimeNs int64
}

// CompressionRatio returns the compression ratio (compressed size / original size).
//
// Values less than 1.0 indicate successful compression.
// Values equal to 1.0 indicate no compression benefit.
// Values greater than 1.0 indicate compression overhead (rare for time-series data).
//
// Returns:
//   - float64: Compression ratio (0.0 if original size is zero)
func (s CompressionStats) CompressionRatio() float64 {
	if s.OriginalSize == 0 {
		return 0.0
	}

	return float64(s.CompressedSize) / float64(s.OriginalSize)
}

// SpaceSavings returns the space savings as a percentage (0-100%).
//
// Higher values indicate better compression.
//
// Returns:
//   - float64: Space savings percentage (0-100)
func (s CompressionStats) SpaceSavings() float64 {
	return (1.0 - s.CompressionRatio()) * 100.0
}

// CreateCodec is a factory function that creates a Codec based on the specified compression type.
//
// Parameters:
//   - compressionType: Type of compression (None, Zstd, S2, or LZ4)
//   - target: Description of target usage (for error messages)
//
// Returns:
//   - Codec: Compressor instance for the specified type
//   - error: Invalid compression type error
func CreateCodec(compressionType format.CompressionType, target string) (Codec, error) {
	switch compressionType {
	case format.CompressionNone:
		return NewNoOpCompressor(), nil
	case format.CompressionZstd:
		return NewZstdCompressor(), nil
	case format.CompressionS2:
		return NewS2Compressor(), nil
	case format.CompressionLZ4:
		return NewLZ4Compressor(), nil
	default:
		return nil, fmt.Errorf("invalid %s compression: %s", target, compressionType)
	}
}

var builtinCodecs = map[format.CompressionType]Codec{
	format.CompressionNone: NewNoOpCompressor(),
	format.CompressionZstd: NewZstdCompressor(),
	format.CompressionS2:   NewS2Compressor(),
	format.CompressionLZ4:  NewLZ4Compressor(),
}

// GetCodec retrieves a built-in Codec for the specified compression type.
func GetCodec(compressionType format.CompressionType) (Codec, error) {
	if codec, ok := builtinCodecs[compressionType]; ok {
		return codec, nil
	}

	return nil, fmt.Errorf("unsupported compression type: %s", compressionType)
}
