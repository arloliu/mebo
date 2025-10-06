// Package compress provides compression and decompression codecs for mebo time-series payloads.
//
// This package offers multiple compression algorithms optimized for different characteristics
// of time-series data. Compression is applied at the payload level after encoding, providing
// an additional layer of space savings beyond the encoding strategies.
//
// # Overview
//
// Mebo applies a two-stage compression strategy:
//
//  1. **Encoding**: Exploits patterns in the data (delta, Gorilla, varint)
//  2. **Compression**: Further reduces encoded data using general-purpose algorithms
//
// The compress package implements the second stage, supporting multiple algorithms:
//   - None: No compression (fastest, largest)
//   - Zstd: Excellent compression ratio, moderate speed
//   - S2: Balanced compression and speed
//   - LZ4: Fast decompression, moderate compression
//
// # Architecture
//
// The package defines three core interfaces:
//
//	type Compressor interface {
//	    Compress(data []byte) ([]byte, error)
//	}
//
//	type Decompressor interface {
//	    Decompress(data []byte) ([]byte, error)
//	}
//
//	type Codec interface {
//	    Compressor
//	    Decompressor
//	}
//
// # Supported Algorithms
//
// **NoOp Compression** (format.CompressionNone)
//
//	codec := compress.NewNoOpCodec()
//	compressed, _ := codec.Compress(data)  // Returns data unchanged
//	original, _ := codec.Decompress(compressed)  // Returns data unchanged
//
// Use when:
//   - Data is already well-compressed by encoding
//   - CPU is more critical than storage
//   - Data is incompressible (random, encrypted)
//
// **Zstandard (Zstd)** (format.CompressionZstd)
//
//	codec := compress.NewZstdCodec()
//	compressed, _ := codec.Compress(data)  // Best compression ratio
//	original, _ := codec.Decompress(compressed)
//
// Characteristics:
//   - Compression: Excellent (typically 2-4x on top of encoding)
//   - Speed: Moderate (compression: ~400 MB/s, decompression: ~1000 MB/s)
//   - Memory: ~2-4 MB for compression, ~1-2 MB for decompression
//   - Latency: Medium (adds ~0.5-2ms for typical payloads)
//
// Use when:
//   - Storage cost is primary concern
//   - Network bandwidth is limited
//   - Can tolerate moderate compression overhead
//
// Best for:
//   - Text payloads (high compression ratio)
//   - Repetitive numeric data
//   - Cold storage / archival
//
// **S2 (Snappy Alternative)** (format.CompressionS2)
//
//	codec := compress.NewS2Codec()
//	compressed, _ := codec.Compress(data)  // Fast with good compression
//	original, _ := codec.Decompress(compressed)
//
// Characteristics:
//   - Compression: Good (typically 1.5-2.5x on top of encoding)
//   - Speed: Fast (compression: ~1000 MB/s, decompression: ~2000 MB/s)
//   - Memory: ~256KB for compression, ~64KB for decompression
//   - Latency: Low (adds ~0.2-0.5ms for typical payloads)
//
// Use when:
//   - Need balance between compression and speed
//   - Latency is important
//   - Moderate storage savings are acceptable
//
// Best for:
//   - Real-time metrics ingestion
//   - Hot path query responses
//   - Streaming applications
//
// **LZ4** (format.CompressionLZ4)
//
//	codec := compress.NewLZ4Codec()
//	compressed, _ := codec.Compress(data)  // Very fast decompression
//	original, _ := codec.Decompress(compressed)
//
// Characteristics:
//   - Compression: Moderate (typically 1.3-2x on top of encoding)
//   - Speed: Very fast decompression (~3000 MB/s), moderate compression (~800 MB/s)
//   - Memory: ~64KB for compression, ~16KB for decompression
//   - Latency: Very low (adds ~0.1-0.3ms for typical payloads)
//
// Use when:
//   - Read performance is critical
//   - Decompression speed matters more than compression ratio
//   - Low latency is required
//
// Best for:
//   - Query-heavy workloads
//   - Low-latency applications
//   - Cache-friendly scenarios
//
// # Algorithm Selection Guide
//
// **Choose based on workload**:
//
// | Workload Type          | Recommended | Reason                              |
// |------------------------|-------------|-------------------------------------|
// | Storage-constrained    | Zstd        | Best compression ratio              |
// | Real-time ingestion    | S2          | Balanced speed and compression      |
// | Query-heavy            | LZ4         | Fastest decompression               |
// | CPU-constrained        | None        | No compression overhead             |
// | Cold storage           | Zstd        | Maximize space savings              |
// | Hot path               | LZ4 or S2   | Minimize latency                    |
// | Network transmission   | Zstd        | Reduce bandwidth usage              |
//
// **Choose based on data characteristics**:
//
// | Data Type              | Recommended | Typical Ratio (after encoding) |
// |------------------------|-------------|--------------------------------|
// | Text values            | Zstd        | 3-5x                           |
// | Numeric values (Delta) | S2          | 1.5-2x                         |
// | Numeric values (Raw)   | Zstd        | 2-3x                           |
// | Tags                   | Zstd        | 3-4x                           |
// | Mixed                  | S2          | 1.8-2.5x                       |
//
// # Performance Benchmarks
//
// Based on typical 16KB time-series payloads (1000 points):
//
// **Timestamp Payload (Delta-encoded)**:
//
//	Algorithm  | Comp Time | Decomp Time | Ratio | Size
//	-----------|-----------|-------------|-------|-------
//	None       | 0 μs      | 0 μs        | 1.0x  | 1.2KB
//	LZ4        | 15 μs     | 5 μs        | 1.4x  | 0.9KB
//	S2         | 25 μs     | 8 μs        | 1.6x  | 0.8KB
//	Zstd       | 80 μs     | 20 μs       | 2.1x  | 0.6KB
//
// **Value Payload (Gorilla-encoded)**:
//
//	Algorithm  | Comp Time | Decomp Time | Ratio | Size
//	-----------|-----------|-------------|-------|-------
//	None       | 0 μs      | 0 μs        | 1.0x  | 2.8KB
//	LZ4        | 20 μs     | 7 μs        | 1.3x  | 2.2KB
//	S2         | 35 μs     | 12 μs       | 1.5x  | 1.9KB
//	Zstd       | 120 μs    | 30 μs       | 1.9x  | 1.5KB
//
// **Text Payload**:
//
//	Algorithm  | Comp Time | Decomp Time | Ratio | Size
//	-----------|-----------|-------------|-------|-------
//	None       | 0 μs      | 0 μs        | 1.0x  | 8.0KB
//	LZ4        | 40 μs     | 15 μs       | 2.0x  | 4.0KB
//	S2         | 60 μs     | 20 μs       | 2.5x  | 3.2KB
//	Zstd       | 200 μs    | 50 μs       | 4.0x  | 2.0KB
//
// # Memory Management
//
// All codec implementations use buffer pooling to minimize allocations:
//   - Compression buffers are sized based on input (typically 1-2x input size)
//   - Decompression buffers are pre-allocated based on compressed data header
//   - Buffers are returned to pools after use
//
// Memory overhead:
//   - NoOp: Zero overhead
//   - LZ4: ~64KB compression, ~16KB decompression
//   - S2: ~256KB compression, ~64KB decompression
//   - Zstd: ~2-4MB compression, ~1-2MB decompression
//
// # Thread Safety
//
// All codec implementations are thread-safe and can be safely shared across goroutines.
// However, for best performance, consider using a codec per goroutine to avoid
// internal lock contention.
//
// # Error Handling
//
// Compression errors are rare but can occur:
//   - Input too large (exceeds algorithm limits)
//   - Memory allocation failure
//
// Decompression errors are more common:
//   - Corrupted compressed data
//   - Invalid compression format
//   - Decompressed size exceeds limits
//   - Checksum validation failure (algorithm-dependent)
//
// All errors are wrapped with context for debugging.
//
// # Best Practices
//
//  1. **Profile your workload**: Different algorithms excel at different scenarios
//  2. **Consider total cost**: Factor in CPU, memory, storage, and network
//  3. **Use appropriate levels**: Higher compression levels may not be worth the CPU cost
//  4. **Monitor metrics**: Track compression ratios, latencies, and resource usage
//  5. **Test with real data**: Synthetic benchmarks may not represent your workload
//  6. **Cache decompressors**: Create once, reuse many times
//  7. **Match encoding**: Some encodings benefit more from compression than others
//
// # Integration with Blob Package
//
// The blob package uses this package internally. Configure compression via encoder options:
//
//	// Numeric blob with Zstd compression
//	encoder, _ := blob.NewNumericEncoder(time.Now(),
//	    blob.WithTimestampCompression(format.CompressionZstd),
//	    blob.WithValueCompression(format.CompressionZstd),
//	)
//
//	// Text blob with S2 compression (faster)
//	encoder, _ := blob.NewTextEncoder(time.Now(),
//	    blob.WithTextDataCompression(format.CompressionS2),
//	)
//
// Decoders automatically detect and use the correct decompression algorithm
// based on the blob header.
//
// # Advanced Usage
//
// For custom compression needs, implement the Compressor/Decompressor interfaces:
//
//	type MyCodec struct{}
//
//	func (c *MyCodec) Compress(data []byte) ([]byte, error) {
//	    // Custom compression logic
//	    return compressedData, nil
//	}
//
//	func (c *MyCodec) Decompress(data []byte) ([]byte, error) {
//	    // Custom decompression logic
//	    return originalData, nil
//	}
//
// Register with the format package if you want blob encoder/decoder integration.
//
// # Examples
//
// See compress_demo example for interactive compression comparison across algorithms.
package compress
