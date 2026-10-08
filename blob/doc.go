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
//	encoder.StartMetricName("process.latency", 20)
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
//	// 8. Finish and get the encoded blob bytes
//	data, _ := encoder.Finish()
//
// # Decoding Workflow
//
// A decoder turns blob bytes into a NumericBlob or TextBlob,
// which provides both sequential iteration and random access:
//
//	// Create decoder and decode the blob
//	decoder, err := blob.NewNumericDecoder(data)
//	if err != nil {
//	    return err
//	}
//	numBlob, err := decoder.Decode()
//	if err != nil {
//	    return err
//	}
//
//	// Sequential iteration (preferred for full scans)
//	for _, dp := range numBlob.All(metricID) {
//	    fmt.Printf("ts=%d, val=%f\n", dp.Ts, dp.Val)
//	}
//
//	// Random access — complexity depends on encoding: O(1) for Raw, O(1)+O(log k)
//	// for ALP, O(index) for Gorilla/Chimp and unshared Delta/DeltaPacked (see ValueAt/TimestampAt)
//	val, ok := numBlob.ValueAt(metricID, 50) // Get 51st point
//	ts, ok := numBlob.TimestampAt(metricID, 50)
//
// # Blob Sets
//
// Blob sets provide unified access to multiple time-ordered blobs:
//
//	// Create type-specific blob sets
//	numericSet, err := blob.NewNumericBlobSet([]blob.NumericBlob{blob1, blob2, blob3})
//	textSet, err := blob.NewTextBlobSet([]blob.TextBlob{textBlob1, textBlob2})
//
//	// Create heterogeneous blob set from decoded blobs
//	blobSet := blob.NewBlobSet(
//	    []blob.NumericBlob{numBlob1, numBlob2},
//	    []blob.TextBlob{textBlob1, textBlob2},
//	)
//
//	// Or decode from raw byte slices
//	blobSet, err = blob.DecodeBlobSet(rawBlob1, rawBlob2, rawBlob3)
//	// Automatically detects and separates numeric vs text blobs
//
//	// Query across all blobs chronologically
//	for _, dp := range blobSet.AllNumerics(metricID) {
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
//	// One-time materialization cost: about 2–5 ns per point without tags (150-point metrics)
//	mat := numericSet.Materialize() // or blobSet.MaterializeNumeric() on a BlobSet
//
//	// O(1) random access (about 1 ns per access)
//	val, ok := mat.ValueAt(metricID, 500)     // Very fast!
//	ts, ok := mat.TimestampAt(metricID, 500)  // Direct array indexing
//	tag, ok := mat.TagAt(metricID, 500)       // If tags enabled
//
// For one numeric metric, resolve a NumericMetric handle once instead
// (NumericBlob.Metric, BlobSet.NumericMetricByName and their siblings);
// its TimestampAccess and ValueAccess say whether lookups replay columns,
// and its Materialize decodes only the axes that do.
//
// Use materialization when:
//   - Lookups on a metric replay its column (Gorilla or Chimp values, or Delta and DeltaPacked
//     timestamps of the metric's own) and you read it many times or out of order;
//     direct axes (Raw, ALP and ALP-RLE values, Raw or shared timestamps) gain nothing from it
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
//   - blob.WithTimestampEncoding(format.TypeRaw|TypeDelta|TypeDeltaPacked) - Timestamp encoding
//   - blob.WithValueEncoding(format.TypeRaw|TypeGorilla|TypeChimp|TypeALP|TypeALPRLE) - Value encoding
//   - blob.WithTimestampCompression(format.CompressionNone|Zstd|S2|LZ4) - Timestamp compression
//   - blob.WithValueCompression(format.CompressionNone|Zstd|S2|LZ4) - Value compression
//   - blob.WithTagsEnabled(true|false) - Enable/disable tags
//   - blob.WithMetricNames() - Store metric names (StartMetricName only)
//   - blob.WithBlobLayoutV2() - Use the V2 layout (sorted index, wider offsets)
//   - blob.WithSharedTimestamps() - Deduplicate identical timestamp columns (implies V2)
//
// Text Encoder Options:
//   - blob.WithTextLittleEndian() / blob.WithTextBigEndian() - Byte order
//   - blob.WithTextTimestampEncoding(format.TypeRaw|TypeDelta) - Timestamp encoding
//   - blob.WithTextDataCompression(format.CompressionNone|Zstd|S2|LZ4) - Data compression
//   - blob.WithTextTagsEnabled(true|false) - Enable/disable tags
//   - blob.WithoutMetricNames() - Do not store metric names (stored by default in text blobs)
//
// # Performance Characteristics
//
// Encoding:
//   - Numeric (Gorilla+Delta): ~40 ns/point, ~8.5 bytes/point on the README benchmark data
//     (fewer for slowly changing or decimal-quantized values)
//   - Text (Delta+Zstd): ~100 ns/point, varies with string length
//   - Tag overhead: depends on tag content; the tag payload is always zstd-compressed
//     and omitted entirely when every tag is empty
//
// Sequential Decoding:
//   - Numeric: ~20 ns/point
//   - Text: ~50 ns/point
//
// Random Access (see docs/performance.md's Random Access Performance section for
// measured ns/op across every timestamp×value combination):
//   - Raw (timestamp or value): O(1), direct offset into a fixed-width array
//   - ALP (value): O(1) windowed bit read + O(log k) binary search over that
//     column's exceptions (k = exceptions in the column, not its length)
//   - Delta, DeltaPacked (timestamp): O(index), must sequentially decode from start,
//     except timestamps shared across metrics: O(1) from the groups pre-decoded at open
//   - Gorilla, Chimp (value): O(index), must decompress the XOR chain from start
//   - Materialized: O(1), about 1 ns (direct array access), regardless of the
//     underlying encoding — the one-time materialization cost decodes everything
//     into a flat array upfront
//
// Materialization:
//   - Cost: about 2–5 ns per numeric point without tags (ALP to Chimp values),
//     plus one string copy per point with tags
//     (measured 2026-10 on 150-point metrics with shared DeltaPacked timestamps, uncompressed, little-endian)
//   - Memory: ~16 bytes/point (numeric), ~24 bytes/point (text)
//   - Access: O(1), about 1 ns per access (same measurement)
//
// # Thread Safety
//
// Encoders: Not thread-safe. Use one encoder per goroutine.
//
// Decoders: Not safe for concurrent use and not reusable.
// Create one decoder per blob and call Decode once, from a single goroutine.
// To read a blob from several goroutines, decode it once and share the resulting NumericBlob or TextBlob.
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
//  11. Use tags judiciously: Tags add a compressed tag payload whose size depends on tag content;
//     only enable when needed.
//  12. Profile your workload: Test different configurations with your actual data to find optimal settings.
//
// # Error Handling
//
// Common decoding errors (sentinels in the errs package):
//   - ErrInvalidHeaderSize: Data is shorter than the 32-byte header
//   - ErrInvalidMagicNumber: Header has an unknown magic number
//   - ErrInvalidHeaderFlags: Header flags name an unknown encoding or compression
//   - ErrUnsupportedCompression: Blob uses a compression this version doesn't support
//   - ErrInvalidIndexEntrySize, ErrInvalidIndexOffsets, ErrInvalidTimestampPayloadOffset,
//     ErrInvalidValuePayloadOffset, ErrInvalidTagPayloadOffset: Index or payload offsets
//     are inconsistent with the data
//   - ErrInvalidNumOfDataPoints: A metric's data point count does not fit its payload
//   - ErrHashMismatch, ErrInvalidMetricNamesPayload: Metric names do not match their IDs
//     or cannot be parsed
//
// The blob format has no checksum,
// so decoding detects only structural inconsistencies, not arbitrary data corruption.
// Lookups by metric ID, metric name, or index do not return errors:
// a missing metric or an out-of-range index yields a zero value and false.
//
// Many errors are wrapped with context, so match them with errors.Is rather than ==.
//
// # Examples
//
// See the examples directory for complete working examples:
//   - examples/blob_set_demo: Multi-blob queries and materialization
//   - examples/compress_demo: Different compression strategies
//   - examples/options_demo: Configuration options and their effects
package blob
