// Package mebo provides a high-performance, space-efficient binary format for storing
// time-series metric data.
//
// Mebo is optimized for scenarios with many metrics but relatively few data points per
// metric (e.g., 150 metrics × 10 points), providing excellent compression ratios and
// fast lookup performance through hash-based identification and columnar storage.
//
// # Core Features
//
//   - Hash-based metric identification (64-bit xxHash64) for O(1) lookups
//   - Columnar storage with separate timestamp and value encoding
//   - Flexible per-blob encoding strategies:
//     timestamps use Raw, Delta, or DeltaPacked;
//     values use Raw, Gorilla, Chimp, or ALP
//   - Optional compression (None, Zstd, S2, LZ4)
//   - Tag support for additional metadata
//   - Memory-efficient fixed-size structures
//
// # Basic Usage
//
// Creating and encoding numeric metrics:
//
//	import "github.com/arloliu/mebo"
//
//	// Create encoder with default settings
//	startTime := time.Now()
//	encoder, _ := mebo.NewDefaultNumericEncoder(startTime)
//
//	// Add "cpu.usage" metric with 10 data points
//	metricID := mebo.MetricID("cpu.usage")
//	encoder.StartMetricID(metricID, 10)
//	for i := 0; i < 10; i++ {
//	    ts := startTime.Add(time.Duration(i) * time.Second)
//	    encoder.AddDataPoint(ts.UnixMicro(), float64(i*10), "")
//	}
//	encoder.EndMetric()
//
//	// Add "memory.usage" metric with 10 data points
//	metricID = mebo.MetricID("memory.usage")
//	encoder.StartMetricID(metricID, 10)
//	for i := 0; i < 10; i++ {
//	    ts := startTime.Add(time.Duration(i+10) * time.Second)
//	    encoder.AddDataPoint(ts.UnixMicro(), float64(i*10), "")
//	}
//	encoder.EndMetric()
//
//	// Finish and get the encoded blob bytes
//	data, _ := encoder.Finish()
//
// Decoding numeric metrics:
//
//	decoder, _ := mebo.NewNumericDecoder(data)
//	decoded, _ := decoder.Decode()
//	for _, dp := range decoded.All(metricID) {
//	    fmt.Printf("ts=%d, val=%f\n", dp.Ts, dp.Val)
//	}
//
// # Package Structure
//
// This package provides convenient top-level wrappers around the blob package,
// simplifying the most common use cases. For advanced usage and fine-grained
// control, use the blob package directly.
package mebo

import (
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
)

var defaultNumericOptions = []blob.NumericEncoderOption{
	blob.WithLittleEndian(),
	blob.WithTagsEnabled(false),
	blob.WithTimestampEncoding(format.TypeDelta),
	blob.WithTimestampCompression(format.CompressionNone),
	blob.WithValueEncoding(format.TypeGorilla),
	blob.WithValueCompression(format.CompressionNone),
}

var defaultTextOptions = []blob.TextEncoderOption{
	blob.WithTextLittleEndian(),
	blob.WithTextTagsEnabled(false),
	blob.WithTextTimestampEncoding(format.TypeDelta),
	blob.WithTextDataCompression(format.CompressionZstd),
}

// NewNumericEncoder creates a new numeric metric encoder with custom options.
//
// This is the most flexible factory function, allowing full control over encoding
// parameters through options. Use this when you need specific encoding strategies,
// compression algorithms, or other custom configurations.
//
// Parameters:
//   - startTime: The earliest timestamp in the blob (used for blob sorting)
//   - opts: Optional configuration functions (see blob.NumericEncoderOption)
//
// Returns:
//   - *blob.NumericEncoder: The created numeric encoder.
//   - error: An error if the configuration is invalid.
//
// Available options:
//   - blob.WithLittleEndian() / blob.WithBigEndian()
//   - blob.WithTimestampEncoding(format.TypeRaw|TypeDelta|TypeDeltaPacked)
//   - blob.WithValueEncoding(format.TypeRaw|TypeGorilla|TypeChimp|TypeALP)
//   - blob.WithTimestampCompression(format.CompressionNone|Zstd|S2|LZ4)
//   - blob.WithValueCompression(format.CompressionNone|Zstd|S2|LZ4)
//   - blob.WithTagsEnabled(true|false)
//   - blob.WithMetricNames()
//   - blob.WithBlobLayoutV2()
//   - blob.WithSharedTimestamps()
//
// Returns an error if the configuration is invalid.
//
// Example:
//
//	encoder, err := mebo.NewNumericEncoder(time.Now(),
//	    blob.WithValueEncoding(format.TypeGorilla),
//	    blob.WithValueCompression(format.CompressionZstd),
//	)
func NewNumericEncoder(startTime time.Time, opts ...blob.NumericEncoderOption) (*blob.NumericEncoder, error) {
	return blob.NewNumericEncoder(startTime, opts...)
}

// NewDefaultNumericEncoder creates a numeric encoder with recommended default settings.
//
// This is the recommended factory function for most use cases. It uses:
//   - Little-endian byte order (native on x86/x64/ARM)
//   - Delta encoding for timestamps (excellent compression for regular intervals)
//   - Gorilla encoding for values (optimal for slowly-changing floats)
//   - No compression (encoding provides sufficient compression)
//   - Tags disabled (for simple use cases)
//
// Use this when:
//   - You want optimal performance without manual tuning
//   - Your metrics don't need tags
//   - You're storing typical numeric time-series data
//
// For tagged metrics, use NewTaggedNumericEncoder instead.
//
// Parameters:
//   - startTime: The earliest timestamp in the blob
//
// Returns:
//   - *blob.NumericEncoder: The created numeric encoder.
//   - error: An error if the configuration is invalid.
//
// Example:
//
//	encoder, err := mebo.NewDefaultNumericEncoder(time.Now())
//	if err != nil {
//	    log.Fatal(err)
//	}
func NewDefaultNumericEncoder(startTime time.Time) (*blob.NumericEncoder, error) {
	return blob.NewNumericEncoder(startTime, defaultNumericOptions...)
}

// NewTaggedNumericEncoder creates a numeric encoder with tag support enabled.
//
// Use this when your metrics need additional metadata (tags) alongside timestamps
// and values. Tags are stored as strings and are optional per data point.
//
// Tags add storage overhead that depends on the tag content.
// Each tag is stored as a length-prefixed string in a payload that is always zstd-compressed,
// and the payload is omitted entirely when every tag is empty.
// Tags enable rich metadata:
//   - Host/instance identifiers
//   - Deployment environments (prod, staging, dev)
//   - Application versions
//   - Any contextual string data
//
// The encoder inherits default settings (Delta timestamps, Gorilla values, no compression)
// but you can override them with additional options.
//
// Parameters:
//   - startTime: The earliest timestamp in the blob
//   - opts: Optional configuration functions (see blob.NumericEncoderOption)
//
// Returns:
//   - *blob.NumericEncoder: The created numeric encoder.
//   - error: An error if the configuration is invalid.
//
// Example:
//
//	startTime := time.Now()
//	encoder, err := mebo.NewTaggedNumericEncoder(startTime,
//	    blob.WithValueCompression(format.CompressionZstd),
//	)
//	encoder.StartMetricID(metricID, 100)
//	for i := 0; i < 100; i++ {
//	    ts := startTime.Add(time.Duration(i) * time.Second)
//	    encoder.AddDataPoint(ts.UnixMicro(), 42.0+float64(i), "host=server1")
//	}
func NewTaggedNumericEncoder(startTime time.Time, opts ...blob.NumericEncoderOption) (*blob.NumericEncoder, error) {
	allOpts := append(append(defaultNumericOptions, blob.WithTagsEnabled(true)), opts...)
	return blob.NewNumericEncoder(startTime, allOpts...)
}

// NewNumericDecoder creates a decoder for reading numeric metric blobs.
//
// The decoder automatically detects the blob's encoding configuration from the header.
// Call Decode once to obtain a blob.NumericBlob, which provides sequential iteration and random access.
// A decoder is single-use and not safe for concurrent use;
// share the decoded blob instead, which is safe for concurrent reads.
//
// Parameters:
//   - data: The raw blob bytes (from encoder.Finish() or storage)
//
// Returns:
//   - *blob.NumericDecoder: The created numeric decoder.
//   - error: An error if the header is invalid.
//
// The decoded blob provides two access patterns:
//  1. Sequential iteration: decoded.All(metricID) - O(n), optimized for full scans
//  2. Random access: decoded.ValueAt(metricID, index) - O(1) for Raw values,
//     O(1) plus O(log k) exception search for ALP values,
//     and O(index) for Gorilla and Chimp values
//
// Example:
//
//	decoder, err := mebo.NewNumericDecoder(blobData)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	decoded, err := decoder.Decode()
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Sequential access (preferred)
//	for _, dp := range decoded.All(metricID) {
//	    fmt.Printf("ts=%d, val=%f\n", dp.Ts, dp.Val)
//	}
func NewNumericDecoder(data []byte) (*blob.NumericDecoder, error) {
	return blob.NewNumericDecoder(data)
}

// NewTextEncoder creates a new text metric encoder with custom options.
//
// Text encoders store string values instead of numeric values, suitable for:
//   - Log levels (INFO, WARN, ERROR)
//   - Status messages (OK, FAILED, PENDING)
//   - Event types or categories
//   - Any time-series data with string values
//
// Parameters:
//   - startTime: The earliest timestamp in the blob
//   - opts: Optional configuration functions (see blob.TextEncoderOption)
//
// Returns:
//   - *blob.TextEncoder: The created text encoder.
//   - error: An error if the configuration is invalid.
//
// Available options:
//   - blob.WithTextLittleEndian() / blob.WithTextBigEndian()
//   - blob.WithTextTimestampEncoding(format.TypeRaw|TypeDelta)
//   - blob.WithTextDataCompression(format.CompressionNone|Zstd|S2|LZ4)
//   - blob.WithTextTagsEnabled(true|false)
//   - blob.WithoutMetricNames()
//
// Note: Text values are stored as length-prefixed strings. Compression is highly
// recommended for text data (CompressionZstd or CompressionS2).
//
// Example:
//
//	encoder, err := mebo.NewTextEncoder(time.Now(),
//	    blob.WithTextDataCompression(format.CompressionZstd),
//	)
func NewTextEncoder(startTime time.Time, opts ...blob.TextEncoderOption) (*blob.TextEncoder, error) {
	return blob.NewTextEncoder(startTime, opts...)
}

// NewDefaultTextEncoder creates a text encoder with recommended default settings.
//
// Default configuration:
//   - Little-endian byte order
//   - Delta encoding for timestamps
//   - Zstd compression for text data
//   - Tags disabled
//
// Use this when:
//   - You want optimal performance without manual tuning
//   - Your metrics don't need tags
//   - You're storing typical text time-series data
//
// Parameters:
//   - startTime: The earliest timestamp in the blob
//
// Returns:
//   - *blob.TextEncoder: The created text encoder.
//   - error: An error if the configuration is invalid.
//
// Example:
//
//	startTime := time.Now()
//	encoder, err := mebo.NewDefaultTextEncoder(startTime)
//	encoder.StartMetricID(metricID, 10)
//	for i := 0; i < 10; i++ {
//	    ts := startTime.Add(time.Duration(i) * time.Second)
//	    status := "OK"
//	    if i%3 == 0 {
//	        status = "WARN"
//	    }
//	    encoder.AddDataPoint(ts.UnixMicro(), status, "")
//	}
func NewDefaultTextEncoder(startTime time.Time) (*blob.TextEncoder, error) {
	return blob.NewTextEncoder(startTime, defaultTextOptions...)
}

// NewTaggedTextEncoder creates a text encoder with tag support enabled.
//
// Similar to NewTaggedNumericEncoder but for string values. Use when you need both
// string values and metadata tags.
// It inherits default settings (Delta timestamps, Zstd compression) with tags enabled.
//
// Parameters:
//   - startTime: The earliest timestamp in the blob
//   - opts: Optional configuration functions (see blob.TextEncoderOption)
//
// Returns:
//   - *blob.TextEncoder: The created text encoder.
//   - error: An error if the configuration is invalid.
//
// Example:
//
//	startTime := time.Now()
//	encoder, err := mebo.NewTaggedTextEncoder(startTime,
//	    blob.WithTextDataCompression(format.CompressionZstd),
//	)
//	encoder.StartMetricID(metricID, 100)
//	for i := 0; i < 100; i++ {
//	    ts := startTime.Add(time.Duration(i) * time.Second)
//	    encoder.AddDataPoint(ts.UnixMicro(), "ERROR", "service=api")
//	}
func NewTaggedTextEncoder(startTime time.Time, opts ...blob.TextEncoderOption) (*blob.TextEncoder, error) {
	allOpts := append(append(defaultTextOptions, blob.WithTextTagsEnabled(true)), opts...)
	return blob.NewTextEncoder(startTime, allOpts...)
}

// NewTextDecoder creates a decoder for reading text metric blobs.
//
// Automatically detects encoding configuration from the blob header.
// Call Decode once to obtain a blob.TextBlob, which provides access to string values.
// A decoder is single-use and not safe for concurrent use;
// share the decoded blob instead, which is safe for concurrent reads.
//
// Parameters:
//   - data: The raw blob bytes
//
// Returns:
//   - *blob.TextDecoder: The created text decoder.
//   - error: An error if the header is invalid.
//
// Example:
//
//	decoder, err := mebo.NewTextDecoder(blobData)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	decoded, err := decoder.Decode()
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	for _, dp := range decoded.All(metricID) {
//	    fmt.Printf("ts=%d, val=%s\n", dp.Ts, dp.Val)
//	}
func NewTextDecoder(data []byte) (*blob.TextDecoder, error) {
	return blob.NewTextDecoder(data)
}

// NewNumericBlobSet creates a set of numeric blobs for multi-blob operations.
//
// Use this when you have multiple blobs covering different time ranges for the same
// metrics and want to query across them. The blob set provides:
//   - Unified iteration across time-ordered blobs
//   - Cross-blob metric queries
//   - Efficient access patterns for time-range queries
//
// Blobs are automatically sorted by start time.
// The set only rejects an empty slice;
// it does not check the blobs against each other.
//
// Parameters:
//   - blobs: Decoded NumericBlob instances (from NumericDecoder.Decode)
//
// Returns:
//   - blob.NumericBlobSet: The created numeric blob set (immutable, safe for concurrent reads).
//   - error: An error if the blobs slice is empty.
//
// Example:
//
//	data1, _ := encoder1.Finish()
//	data2, _ := encoder2.Finish()
//
//	decoder1, _ := mebo.NewNumericDecoder(data1)
//	blob1, _ := decoder1.Decode()
//	decoder2, _ := mebo.NewNumericDecoder(data2)
//	blob2, _ := decoder2.Decode()
//
//	blobSet, err := mebo.NewNumericBlobSet([]blob.NumericBlob{blob1, blob2})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Query across all blobs
//	for _, dp := range blobSet.All(metricID) {
//	    fmt.Printf("ts=%d, val=%f\n", dp.Ts, dp.Val)
//	}
func NewNumericBlobSet(blobs []blob.NumericBlob) (blob.NumericBlobSet, error) {
	return blob.NewNumericBlobSet(blobs)
}

// NewMaterializedNumericBlobSet creates a materialized view of numeric blobs for O(1) random access.
//
// Materialization decodes all data points from all blobs into memory, providing:
//   - O(1) random access: ValueAt(metricID, globalIndex) → ~5ns
//   - O(1) timestamp access: TimestampAt(metricID, globalIndex) → ~5ns
//   - O(1) tag access: TagAt(metricID, globalIndex) → ~5ns
//   - Global indexing across all blobs chronologically
//
// Use materialization when:
//   - You need frequent random access to many data points
//   - Memory is available (~16 bytes × total points)
//   - The cost of materialization is amortized over many accesses
//
// Avoid materialization when:
//   - You only need sequential iteration (use NumericBlobSet.All)
//   - Memory is constrained
//   - You're only accessing a few data points
//
// Parameters:
//   - blobs: Array of NumericBlob instances to materialize
//
// Returns:
//   - blob.MaterializedNumericBlobSet: The materialized blob set.
//   - error: An error if the blobs are invalid.
//
// Example:
//
//	mat, err := mebo.NewMaterializedNumericBlobSet(blobs)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// O(1) random access - very fast!
//	val, ok := mat.ValueAt(metricID, 500)  // Access 501st point globally
//	ts, ok := mat.TimestampAt(metricID, 500)
func NewMaterializedNumericBlobSet(blobs []blob.NumericBlob) (blob.MaterializedNumericBlobSet, error) {
	blobSet, err := blob.NewNumericBlobSet(blobs)
	if err != nil {
		return blob.MaterializedNumericBlobSet{}, err
	}

	return blobSet.Materialize(), nil
}

// NewTextBlobSet creates a set of text blobs for multi-blob operations.
//
// Similar to NewNumericBlobSet but for text metrics. Provides unified access
// to string values across multiple time-ordered blobs.
//
// Parameters:
//   - blobs: Array of TextBlob instances
//
// Returns:
//   - blob.TextBlobSet: The created text blob set (immutable, safe for concurrent reads).
//   - error: An error if the blobs slice is empty.
//
// Example:
//
//	blobSet, err := mebo.NewTextBlobSet(textBlobs)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	for _, dp := range blobSet.All(metricID) {
//	    fmt.Printf("ts=%d, val=%s\n", dp.Ts, dp.Val)
//	}
func NewTextBlobSet(blobs []blob.TextBlob) (blob.TextBlobSet, error) {
	return blob.NewTextBlobSet(blobs)
}

// NewMaterializedTextBlobSet creates a materialized view of text blobs for O(1) random access.
//
// Similar to NewMaterializedNumericBlobSet but for text metrics. Provides constant-time
// random access to string values.
//
// Use when you need frequent random access to text metrics across multiple blobs.
//
// Parameters:
//   - blobs: Array of TextBlob instances to materialize
//
// Returns:
//   - blob.MaterializedTextBlobSet: The materialized text blob set.
//   - error: An error if the blobs are invalid.
//
// Example:
//
//	mat, err := mebo.NewMaterializedTextBlobSet(textBlobs)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	val, ok := mat.ValueAt(metricID, 100)  // O(1) access
func NewMaterializedTextBlobSet(blobs []blob.TextBlob) (blob.MaterializedTextBlobSet, error) {
	blobSet, err := blob.NewTextBlobSet(blobs)
	if err != nil {
		return blob.MaterializedTextBlobSet{}, err
	}

	return blobSet.Materialize(), nil
}

// NewBlobSet creates a heterogeneous set containing both numeric and text blobs.
//
// Use this when you have metrics of different types (numeric and text) that you want
// to manage together. The blob set provides type-safe access through separate methods
// for numeric and text metrics.
//
// The blob set maintains separate sorted arrays for each type and provides methods to:
//   - Access numeric blobs: bs.NumericBlobs()
//   - Access text blobs: bs.TextBlobs()
//   - Materialize each type separately: bs.MaterializeNumeric() / bs.MaterializeText()
//   - Query individual metrics by type
//
// Parameters:
//   - numericBlobs: Array of NumericBlob instances (can be nil/empty)
//   - textBlobs: Array of TextBlob instances (can be nil/empty)
//
// Returns:
//   - blob.BlobSet: The created blob set.
//
// Example:
//
//	blobSet := mebo.NewBlobSet(numericBlobs, textBlobs)
//
//	// Iterate numeric metrics
//	for _, dp := range blobSet.AllNumerics(cpuMetricID) {
//	    fmt.Printf("CPU: %f\n", dp.Val)
//	}
//
//	// Iterate text metrics
//	for _, dp := range blobSet.AllTexts(statusMetricID) {
//	    fmt.Printf("Status: %s\n", dp.Val)
//	}
//
//	// Random access by global index
//	if dp, ok := blobSet.NumericAt(cpuMetricID, 0); ok {
//	    fmt.Printf("first CPU value: %f\n", dp.Val)
//	}
//
//	// Materialize for random access
//	numMat := blobSet.MaterializeNumeric()
//	textMat := blobSet.MaterializeText()
func NewBlobSet(numericBlobs []blob.NumericBlob, textBlobs []blob.TextBlob) blob.BlobSet {
	return blob.NewBlobSet(numericBlobs, textBlobs)
}

// MetricID converts a metric name string to its 64-bit hash identifier.
//
// Mebo uses xxHash64 to convert metric names to fixed-size IDs for:
//   - Fast O(1) hash map lookups
//   - Fixed-size index entries (16 bytes each, or 32 bytes for V2 extended entries)
//   - Consistent metric identification across blobs
//
// The hash function guarantees:
//   - Deterministic: same input always produces same output
//   - Collision-resistant: extremely low probability of collisions
//   - Fast: ~1-2 ns per hash on modern CPUs
//
// Collision handling:
//   - With StartMetricName, a collision makes the encoder store metric names
//     in the blob's metric-names payload automatically
//   - With MetricID and StartMetricID, the encoder cannot tell names apart,
//     so a duplicate ID returns ErrHashCollision
//   - When names are stored, the decoder verifies them against their hashes
//   - Collision probability: ~1 in 2^64 (negligible for practical use)
//
// When to use:
//
// If the application doesn't have unsigned 64-bit IDs for metrics, use MetricID to
// generate them from human-readable names. This is common in scenarios where:
//   - Metrics are defined by users or external systems
//   - Metric names are hierarchical (e.g., "service.api.request.count")
//   - The set of metrics is dynamic or not known in advance
//
// Use this function to:
//   - Convert metric names to IDs before encoding
//   - Generate consistent IDs for queries
//   - Pre-compute IDs for frequently-used metrics
//
// Example:
//
//	cpuID := mebo.MetricID("cpu.usage")
//	memID := mebo.MetricID("memory.bytes")
//
//	encoder.StartMetricID(cpuID, 100)
//	// ... add 100 data points, then call EndMetric ...
//	encoder.StartMetricID(memID, 100)
//	// ... add 100 data points, then call EndMetric ...
//
//	// Query the decoded blob with the same ID
//	for _, dp := range decoded.All(cpuID) {
//	    fmt.Println(dp.Ts, dp.Val)
//	}
func MetricID(name string) uint64 {
	return hash.ID(name)
}
