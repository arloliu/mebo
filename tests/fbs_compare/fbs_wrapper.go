package fbscompare

import (
	"fmt"
	"iter"

	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/arloliu/mebo/compress"
	"github.com/arloliu/mebo/tests/fbscompare/fbs/numericblob"
)

// FBSBlob wraps FlatBuffers-encoded metric data with optional compression.
// This provides an API similar to mebo's NumericBlob for fair benchmarking.
//
// Real-world usage pattern:
// 1. EncodeFBS() - creates blob with optional compression
// 2. Decode() - decompresses data ONCE and caches it
// 3. All/AllTimestamps/AllValues/ValueAt/TimestampAt - uses cached data (no repeated decompression)
type FBSBlob struct {
	data           []byte // Raw FlatBuffers data
	compressed     []byte // Compressed FlatBuffers data (if compression is used)
	compression    string // Compression type: "none", "zstd", "s2", "lz4"
	codec          compress.Codec
	decodedData    []byte                        // Cached decompressed data after Decode()
	decodedMetrics *numericblob.NumericMetricSet // Cached FlatBuffers root
	decoded        bool                          // Whether Decode() has been called
}

// MetricData represents a single metric with its timestamps and values.
type MetricData struct {
	ID         uint64
	Timestamps []int64
	Values     []float64
}

// EncodeFBS creates a FlatBuffers blob from metric data.
// The data map keys are metric IDs, values are (timestamps, values) pairs.
func EncodeFBS(metrics []MetricData, compression string) (*FBSBlob, error) {
	if len(metrics) == 0 {
		return nil, fmt.Errorf("no metrics to encode")
	}

	// Create FlatBuffers builder
	builder := flatbuffers.NewBuilder(1024)

	// Build each metric
	metricOffsets := make([]flatbuffers.UOffsetT, 0, len(metrics))
	for _, metric := range metrics {
		if len(metric.Timestamps) != len(metric.Values) {
			return nil, fmt.Errorf("metric %d: timestamp and value count mismatch", metric.ID)
		}

		// Create timestamp vector
		numericblob.NumericMetricStartTimestampVector(builder, len(metric.Timestamps))
		for i := len(metric.Timestamps) - 1; i >= 0; i-- {
			builder.PrependInt64(metric.Timestamps[i])
		}
		tsOffset := builder.EndVector(len(metric.Timestamps))

		// Create value vector
		numericblob.NumericMetricStartValueVector(builder, len(metric.Values))
		for i := len(metric.Values) - 1; i >= 0; i-- {
			builder.PrependFloat64(metric.Values[i])
		}
		valOffset := builder.EndVector(len(metric.Values))

		// Create metric
		numericblob.NumericMetricStart(builder)
		numericblob.NumericMetricAddId(builder, metric.ID)
		numericblob.NumericMetricAddTimestamp(builder, tsOffset)
		numericblob.NumericMetricAddValue(builder, valOffset)
		metricOffsets = append(metricOffsets, numericblob.NumericMetricEnd(builder))
	}

	// Create metrics vector
	numericblob.NumericMetricSetStartMetricsVector(builder, len(metricOffsets))
	for i := len(metricOffsets) - 1; i >= 0; i-- {
		builder.PrependUOffsetT(metricOffsets[i])
	}
	metricsVecOffset := builder.EndVector(len(metricOffsets))

	// Create metric set
	numericblob.NumericMetricSetStart(builder)
	numericblob.NumericMetricSetAddMetrics(builder, metricsVecOffset)
	metricSetOffset := numericblob.NumericMetricSetEnd(builder)

	// Finish building
	builder.Finish(metricSetOffset)

	// Get the raw bytes
	rawData := builder.FinishedBytes()

	// Apply compression if needed
	blob := &FBSBlob{
		data:        rawData,
		compression: compression,
	}

	if compression != "none" {
		if err := blob.applyCompression(); err != nil {
			return nil, fmt.Errorf("compression failed: %w", err)
		}
	}

	return blob, nil
}

// applyCompression compresses the raw FlatBuffers data.
func (b *FBSBlob) applyCompression() error {
	var codec compress.Codec
	switch b.compression {
	case "zstd":
		codec = compress.NewZstdCompressor()
	case "s2":
		codec = compress.NewS2Compressor()
	case "lz4":
		codec = compress.NewLZ4Compressor()
	case "none":
		return nil
	default:
		return fmt.Errorf("unknown compression type: %s", b.compression)
	}

	compressed, err := codec.Compress(b.data)
	if err != nil {
		return err
	}

	b.compressed = compressed
	b.codec = codec

	return nil
}

// decompress returns decompressed FlatBuffers data (internal use only).
func (b *FBSBlob) decompress() ([]byte, error) {
	if b.compression == "none" {
		return b.data, nil
	}

	if b.codec == nil {
		return nil, fmt.Errorf("codec not initialized")
	}

	return b.codec.Decompress(b.compressed)
}

// Decode decompresses the blob data once and caches it for subsequent operations.
// This mirrors the real-world usage pattern: decompress once, then use many times.
// Must be called before any All/AllTimestamps/AllValues/ValueAt/TimestampAt operations.
func (b *FBSBlob) Decode() error {
	if b.decoded {
		return nil // Already decoded
	}

	data, err := b.decompress()
	if err != nil {
		return fmt.Errorf("decompression failed: %w", err)
	}

	b.decodedData = data
	metricSet := numericblob.GetRootAsNumericMetricSet(data, 0)
	b.decodedMetrics = metricSet
	b.decoded = true

	return nil
}

// Size returns the size of the encoded blob (compressed if compression is used).
func (b *FBSBlob) Size() int {
	if b.compression != "none" && len(b.compressed) > 0 {
		return len(b.compressed)
	}

	return len(b.data)
}

// UncompressedSize returns the size of the raw FlatBuffers data.
func (b *FBSBlob) UncompressedSize() int {
	return len(b.data)
}

// findMetric finds a metric by ID in the decoded FlatBuffers data.
// Note: Decode() must be called first.
func (b *FBSBlob) findMetric(metricID uint64) (*numericblob.NumericMetric, error) {
	if !b.decoded {
		return nil, fmt.Errorf("blob not decoded, call Decode() first")
	}

	numMetrics := b.decodedMetrics.MetricsLength()

	var metric numericblob.NumericMetric
	for i := 0; i < numMetrics; i++ {
		if b.decodedMetrics.Metrics(&metric, i) && metric.Id() == metricID {
			return &metric, nil
		}
	}

	return nil, fmt.Errorf("metric %d not found", metricID)
}

// AllMetricIDs returns all metric IDs in the blob.
// This is useful for iterating through all metrics in benchmarks.
// Note: Decode() must be called first.
func (b *FBSBlob) AllMetricIDs() ([]uint64, error) {
	if !b.decoded {
		return nil, fmt.Errorf("blob not decoded, call Decode() first")
	}

	numMetrics := b.decodedMetrics.MetricsLength()
	ids := make([]uint64, 0, numMetrics)

	var metric numericblob.NumericMetric
	for i := 0; i < numMetrics; i++ {
		if b.decodedMetrics.Metrics(&metric, i) {
			ids = append(ids, metric.Id())
		}
	}

	return ids, nil
}

// All returns an iterator over all (timestamp, value) pairs for the given metric.
// This mirrors mebo's NumericBlob.All() API.
func (b *FBSBlob) All(metricID uint64) iter.Seq2[int64, float64] {
	metric, err := b.findMetric(metricID)
	if err != nil {
		return func(yield func(int64, float64) bool) {}
	}

	count := metric.TimestampLength()

	return func(yield func(int64, float64) bool) {
		for i := 0; i < count; i++ {
			ts := metric.Timestamp(i)
			val := metric.Value(i)
			if !yield(ts, val) {
				return
			}
		}
	}
}

// AllTimestamps returns an iterator over all timestamps for the given metric.
// This mirrors mebo's NumericBlob.AllTimestamps() API.
func (b *FBSBlob) AllTimestamps(metricID uint64) iter.Seq[int64] {
	metric, err := b.findMetric(metricID)
	if err != nil {
		return func(yield func(int64) bool) {}
	}

	count := metric.TimestampLength()

	return func(yield func(int64) bool) {
		for i := 0; i < count; i++ {
			if !yield(metric.Timestamp(i)) {
				return
			}
		}
	}
}

// AllValues returns an iterator over all values for the given metric.
// This mirrors mebo's NumericBlob.AllValues() API.
func (b *FBSBlob) AllValues(metricID uint64) iter.Seq[float64] {
	metric, err := b.findMetric(metricID)
	if err != nil {
		return func(yield func(float64) bool) {}
	}

	count := metric.ValueLength()

	return func(yield func(float64) bool) {
		for i := 0; i < count; i++ {
			if !yield(metric.Value(i)) {
				return
			}
		}
	}
}

// TimestampAt returns the timestamp at the given index for the metric.
// This mirrors mebo's NumericBlob.TimestampAt() API.
func (b *FBSBlob) TimestampAt(metricID uint64, index int) (int64, error) {
	metric, err := b.findMetric(metricID)
	if err != nil {
		return 0, err
	}

	if index < 0 || index >= metric.TimestampLength() {
		return 0, fmt.Errorf("index %d out of range [0, %d)", index, metric.TimestampLength())
	}

	return metric.Timestamp(index), nil
}

// ValueAt returns the value at the given index for the metric.
// This mirrors mebo's NumericBlob.ValueAt() API.
func (b *FBSBlob) ValueAt(metricID uint64, index int) (float64, error) {
	metric, err := b.findMetric(metricID)
	if err != nil {
		return 0, err
	}

	if index < 0 || index >= metric.ValueLength() {
		return 0, fmt.Errorf("index %d out of range [0, %d)", index, metric.ValueLength())
	}

	return metric.Value(index), nil
}
