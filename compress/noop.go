package compress

// NoOpCompressor provides a no-operation compressor that bypasses data without compression.
//
// This compressor is useful for:
//   - Testing and benchmarking scenarios where you want to measure overhead without compression
//   - Development environments where compression is disabled for debugging
//   - Scenarios where the data is already compressed or not suitable for compression
//   - Baseline performance measurements
type NoOpCompressor struct{}

var _ Codec = (*NoOpCompressor)(nil)

// NewNoOpCompressor creates a new no-operation compressor that bypasses data.
//
// The returned compressor implements all three interfaces (Compressor, Decompressor,
// and Codec) and simply copies data without any processing.
//
// Returns:
//   - NoOpCompressor: New no-op compressor instance
func NewNoOpCompressor() NoOpCompressor {
	return NoOpCompressor{}
}

// Compress bypasses compression and returns the input data directly without copying.
//
// This method returns the input slice as-is, without any processing or copying.
// This provides maximum performance for the no-op compressor by eliminating
// unnecessary memory allocations.
//
// Note: The returned slice shares the same underlying memory as the input.
// Callers should not modify the input data after calling this method if they
// plan to use the returned slice.
//
// Parameters:
//   - data: Input data (returned as-is)
//
// Returns:
//   - []byte: Same slice as input data
//   - error: Always nil
func (c NoOpCompressor) Compress(data []byte) ([]byte, error) {
	return data, nil
}

// Decompress bypasses decompression and returns the input data directly without copying.
//
// This method returns the input slice as-is, without any processing or copying.
// This provides maximum performance for the no-op compressor by eliminating
// unnecessary memory allocations.
//
// Note: The returned slice shares the same underlying memory as the input.
// Callers should not modify the input data after calling this method if they
// plan to use the returned slice.
//
// Parameters:
//   - data: Input data (returned as-is)
//
// Returns:
//   - []byte: Same slice as input data
//   - error: Always nil
func (c NoOpCompressor) Decompress(data []byte) ([]byte, error) {
	return data, nil
}
