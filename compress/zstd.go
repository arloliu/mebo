package compress

// ZstdCompressor provides Zstandard compression optimized for mebo time-series data.
//
// This compressor is designed for scenarios where compression ratio is more important
// than compression speed, making it ideal for:
//   - Cold storage and archival of time-series data
//   - Long-term retention of historical metrics
//   - Network transmission where bandwidth is limited
//   - Scenarios where decompression happens infrequently
//
// Performance characteristics:
//   - Compression: ~5-20 ns/byte (depending on compression level)
//   - Decompression: ~2-5 ns/byte
//   - Compression ratio: 5:1 to 20:1 for delta-encoded timestamps
//   - Memory usage: Moderate (creates encoder/decoder per operation)
type ZstdCompressor struct{}

var _ Codec = (*ZstdCompressor)(nil)

// NewZstdCompressor creates a new Zstd compressor with default settings.
//
// Returns:
//   - ZstdCompressor: New Zstd compressor instance
//
// Example:
//
//	compressor := NewZstdCompressor()
//	compressed, err := compressor.Compress(data)
//	if err != nil {
//		return err
//	}
func NewZstdCompressor() ZstdCompressor {
	return ZstdCompressor{}
}
