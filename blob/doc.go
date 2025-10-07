// Package blob provides high-level APIs for encoding, decoding, and managing mebo time-series blobs.
//
// This package is the primary interface for working with mebo's binary time-series format.
// It provides encoder/decoder APIs for both numeric (float64) and text (string) metrics,
// along with powerful blob set abstractions for working with multiple blobs.
//
// # Core Types
//
// Encoders - Create blobs from time-series data:
//   - NumericEncoder: Encodes float64 metrics with configurable compression
//   - TextEncoder: Encodes string metrics with configurable compression
//
// Decoders - Read data from blobs:
//   - NumericDecoder: Decodes numeric blobs with sequential and random access
//   - TextDecoder: Decodes text blobs with sequential access
//
// Blobs - Immutable binary containers:
//   - NumericBlob: Contains encoded numeric metrics
//   - TextBlob: Contains encoded text metrics
//
// Blob Sets - Multi-blob collections:
//   - NumericBlobSet: Unified access across multiple numeric blobs
//   - TextBlobSet: Unified access across multiple text blobs
//   - BlobSet: Heterogeneous collection of both numeric and text blobs
//
// Materialized Views - O(1) random access:
//   - MaterializedNumericBlobSet: Pre-decoded numeric data for fast random access
//   - MaterializedTextBlobSet: Pre-decoded text data for fast random access
//
// # Encoding Workflow
//
// The encoding process follows a simple pattern:
//
//	// 1. Create encoder with configuration
//	startTime := time.Now()
//	encoder, _ := mebo.NewDefaultNumericEncoder(startTime)
//
//	// 2. Start metrics and add data points
//	// Add "cpu.usage" metric by ID with 10 data points
//	metricID := mebo.MetricID("cpu.usage")
//	encoder.StartMetricID(metricID, 10)
//
//	// 3. Write data points
//	for i := 0; i < 10; i++ {
//		ts := startTime.Add(time.Duration(i) * time.Second)
//		encoder.AddDataPoint(ts.UnixMicro(), float64(i*10), "")
//	}
//
//	// 4. End the current metric
//	encoder.EndMetric()
//
//	// 5. (Optional) Start another metric
//	// Add another "process.latency" metric by name with 20 data points
//	encoder.StartMetricName(("process.latency", 20)
//
//	// 6. Write data points for the new metric
//	for i := 0; i < 20; i++ {
//		ts := startTime.Add(time.Duration(i) * time.Second)
//		encoder.AddDataPoint(ts.UnixMicro(), float64(i*10), "")
//	}
//
//	// 7. End the current metric
//	encoder.EndMetric()
//
//	// 8. Finish and get blob
//	blob, _ := encoder.Finish()
//
// # Decoding Workflow
//
// Decoding provides both sequential iteration and random access:
//
//	// Create decoder
//	decoder, err := blob.NewNumericDecoder(blobData)
//
//	// Sequential iteration (preferred for full scans)
//	for dp := range decoder.All(metricID) {
//	    fmt.Printf("ts=%d, val=%f\n", dp.Ts, dp.Val)
//	}
//
//	// Random access (O(log n) to O(n) depending on encoding)
//	val, ok := decoder.ValueAt(metricID, 50) // Get 51st point
//	ts, ok := decoder.TimestampAt(metricID, 50)
//
// # Blob Sets
//
// Blob sets provide unified access to multiple time-ordered blobs:
//
//	// Create type-specific blob sets
//	numericSet := blob.NewNumericBlobSet([]blob.NumericBlob{blob1, blob2, blob3})
//	textSet := blob.NewTextBlobSet([]blob.TextBlob{textBlob1, textBlob2})
//
//	// Create heterogeneous blob set from decoded blobs
//	blobSet := blob.NewBlobSet(
//	    []blob.NumericBlob{numBlob1, numBlob2},
//	    []blob.TextBlob{textBlob1, textBlob2},
//	)
//
//	// Or decode from raw byte slices
//	blobSet, err := blob.DecodeBlobSet(rawBlob1, rawBlob2, rawBlob3)
//	// Automatically detects and separates numeric vs text blobs
//
//	// Query across all blobs chronologically
//	for dp := range blobSet.AllNumerics(metricID) {
//	    // Iterates through blob1, then blob2, then blob3
//	    fmt.Printf("ts=%d, val=%f\n", dp.Ts, dp.Val)
//	}
//
//	// Get specific data point by name access
//	val, ok := blobSet.NumericValueAtByName("cpu.usage", 500)
//
// # Materialization
//
// For frequent random access, materialize blob sets into memory:
//
//	// One-time materialization cost: ~100μs per metric per blob
//	mat := blobSet.Materialize()
//
//	// O(1) random access (~5ns per access)
//	val, ok := mat.ValueAt(metricID, 500)     // Very fast!
//	ts, ok := mat.TimestampAt(metricID, 500)  // Direct array indexing
//	tag, ok := mat.TagAt(metricID, 500)       // If tags enabled
//
// Use materialization when:
//   - You need frequent random access (>100 accesses per metric)
//   - Memory is available (~16 bytes per numeric point, ~24 bytes per text point)
//   - The materialization cost is amortized over many accesses
//
// Avoid materialization when:
//   - You only need sequential iteration
//   - Memory is constrained
//   - You're accessing only a few data points
//
// # Configuration Options
//
// Numeric Encoder Options:
//   - blob.WithLittleEndian() / blob.WithBigEndian() - Byte order
//   - blob.WithTimestampEncoding(format.TypeRaw|TypeDelta) - Timestamp encoding
//   - blob.WithValueEncoding(format.TypeRaw|TypeGorilla) - Value encoding
//   - blob.WithTimestampCompression(format.CompressionNone|Zstd|S2|LZ4) - Timestamp compression
//   - blob.WithValueCompression(format.CompressionNone|Zstd|S2|LZ4) - Value compression
//   - blob.WithTagsEnabled(true|false) - Enable/disable tags
//
// Text Encoder Options:
//   - blob.WithTextLittleEndian() / blob.WithTextBigEndian() - Byte order
//   - blob.WithTextTimestampEncoding(format.TypeRaw|TypeDelta) - Timestamp encoding
//   - blob.WithTextDataCompression(format.CompressionNone|Zstd|S2|LZ4) - Data compression
//   - blob.WithTextTagsEnabled(true|false) - Enable/disable tags
//
// # Performance Characteristics
//
// Encoding:
//   - Numeric (Gorilla+Delta): ~40 ns/point, ~1-4 bytes/point
//   - Text (Delta+Zstd): ~100 ns/point, varies with string length
//   - Tag overhead: ~8-16 bytes per tagged point
//
// Sequential Decoding:
//   - Numeric: ~20 ns/point
//   - Text: ~50 ns/point
//
// Random Access:
//   - Raw encoding: O(1), ~10 ns
//   - Delta encoding: O(n), must scan from start
//   - Gorilla encoding: O(n), must decompress from start
//   - Materialized: O(1), ~5 ns (direct array access)
//
// Materialization:
//   - Cost: ~100 μs per metric per blob
//   - Memory: ~16 bytes/point (numeric), ~24 bytes/point (text)
//   - Access: O(1), ~5 ns per access
//
// # Thread Safety
//
// Encoders: Not thread-safe. Use one encoder per goroutine.
//
// Decoders: Safe for concurrent reads from different goroutines.
//
// Blobs: Immutable and thread-safe once created.
//
// BlobSets: Safe for concurrent reads.
//
// MaterializedBlobSets: Safe for concurrent reads.
//
// # Memory Management
//
// The package uses internal buffer pooling for:
//   - Encoder byte buffers
//   - Decoder temporary buffers
//   - Materialization scratch space
//
// Buffers are automatically returned to pools when encoders/decoders are finalized.
//
// # Best Practices
//
//  1. Always declare data point count: Call StartMetricID(id, count) or StartMetricName(name, count)
//     with accurate count, then call EndMetric() after adding exactly that many points.
//     This is required for Mebo's batch processing design.
//  2. Collect before encoding: Gather all metric data in memory first, then encode in batches.
//     Mebo is designed for batch processing, not streaming ingestion.
//  3. Choose appropriate encoding: Delta for regular intervals, Gorilla for slowly-changing values,
//     Raw for random access needs.
//  4. Batch metrics: Group related metrics in the same blob for better compression.
//  5. Use bulk operations: Call AddDataPoints instead of multiple AddDataPoint calls when you have
//     all data ready (2-3× faster).
//  6. Pre-allocate accurately: Accurate count in StartMetricID enables buffer pre-allocation and
//     better performance.
//  7. Optimize metrics-to-points ratio: Each metric should contain at least 10 data points,
//     with 100-250 points being optimal. Target <1:1 ratio (more points than metrics) for best compression.
//  8. Use blob sets: For multi-blob queries, blob sets are more efficient than manual iteration.
//  9. Materialize wisely: Only materialize when random access pattern justifies the cost (>100 accesses).
//  10. Monitor memory: Materialization can use significant memory for large datasets (~16 bytes/point).
//  11. Use tags judiciously: Tags add 8-16 bytes overhead per point; only enable when needed.
//  12. Profile your workload: Test different configurations with your actual data to find optimal settings.
//
// # Error Handling
//
// Common errors:
//   - ErrInvalidBlobFormat: Blob header is corrupted or has wrong magic number
//   - ErrChecksumMismatch: Data corruption detected (CRC32 validation failed)
//   - ErrUnsupportedEncoding: Blob uses an encoding this version doesn't support
//   - ErrMetricNotFound: Requested metric ID doesn't exist in the blob
//   - ErrInvalidIndex: Index is out of bounds for the metric
//
// All errors are wrapped using the errs package for proper error chain handling.
//
// # Examples
//
// See the examples directory for complete working examples:
//   - examples/blob_set_demo: Multi-blob queries and materialization
//   - examples/compress_demo: Different compression strategies
//   - examples/options_demo: Configuration options and their effects
package blob
