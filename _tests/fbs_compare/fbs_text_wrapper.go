package fbscompare

import (
	"fmt"
	"iter"

	flatbuffers "github.com/google/flatbuffers/go"

	"github.com/arloliu/mebo/compress"
	"github.com/arloliu/mebo/tests/fbs_compare/fbs/textblob"
)

// FBSTextBlob wraps FlatBuffers-encoded text metric data with optional compression.
// This provides an API similar to mebo's TextBlob for fair benchmarking.
//
// Real-world usage pattern:
// 1. EncodeTextFBS() - creates blob with optional compression
// 2. Decode() - decompresses data ONCE and caches it
// 3. All/AllTimestamps/AllValues/ValueAt/TimestampAt - uses cached data (no repeated decompression)
type FBSTextBlob struct {
	data           []byte // Raw FlatBuffers data
	compressed     []byte // Compressed FlatBuffers data (if compression is used)
	compression    string // Compression type: "none", "zstd", "s2", "lz4"
	codec          compress.Codec
	decodedData    []byte                // Cached decompressed data after Decode()
	decodedMetrics *textblob.TextBlobSet // Cached FlatBuffers root
	decoded        bool                  // Whether Decode() has been called
}

// TextMetricData represents a single text metric with its timestamps and values.
type TextMetricData struct {
	ID         uint64
	Timestamps []int64
	Values     []string
	Tags       []string
}

// EncodeTextFBS creates a FlatBuffers blob from text metric data.
func EncodeTextFBS(metrics []TextMetricData, compression string) (*FBSTextBlob, error) {
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
		textblob.TextMetricStartTimestampVector(builder, len(metric.Timestamps))
		for i := len(metric.Timestamps) - 1; i >= 0; i-- {
			builder.PrependInt64(metric.Timestamps[i])
		}
		tsOffset := builder.EndVector(len(metric.Timestamps))

		// Create value vector
		valueOffsets := make([]flatbuffers.UOffsetT, len(metric.Values))
		for i := range metric.Values {
			valueOffsets[i] = builder.CreateString(metric.Values[i])
		}
		textblob.TextMetricStartValueVector(builder, len(metric.Values))
		for i := len(valueOffsets) - 1; i >= 0; i-- {
			builder.PrependUOffsetT(valueOffsets[i])
		}
		valOffset := builder.EndVector(len(metric.Values))

		// Create tag vector if tags exist
		var tagOffset flatbuffers.UOffsetT
		if len(metric.Tags) > 0 {
			tagOffsets := make([]flatbuffers.UOffsetT, len(metric.Tags))
			for i := range metric.Tags {
				tagOffsets[i] = builder.CreateString(metric.Tags[i])
			}
			textblob.TextMetricStartTagVector(builder, len(metric.Tags))
			for i := len(tagOffsets) - 1; i >= 0; i-- {
				builder.PrependUOffsetT(tagOffsets[i])
			}
			tagOffset = builder.EndVector(len(metric.Tags))
		}

		// Create metric
		textblob.TextMetricStart(builder)
		textblob.TextMetricAddId(builder, metric.ID)
		textblob.TextMetricAddTimestamp(builder, tsOffset)
		textblob.TextMetricAddValue(builder, valOffset)
		if len(metric.Tags) > 0 {
			textblob.TextMetricAddTag(builder, tagOffset)
		}
		metricOffsets = append(metricOffsets, textblob.TextMetricEnd(builder))
	}

	// Create metrics vector
	textblob.TextBlobSetStartMetricsVector(builder, len(metricOffsets))
	for i := len(metricOffsets) - 1; i >= 0; i-- {
		builder.PrependUOffsetT(metricOffsets[i])
	}
	metricsVecOffset := builder.EndVector(len(metricOffsets))

	// Create blob set
	textblob.TextBlobSetStart(builder)
	textblob.TextBlobSetAddMetrics(builder, metricsVecOffset)
	blobSetOffset := textblob.TextBlobSetEnd(builder)

	// Finish building
	builder.Finish(blobSetOffset)

	// Get the raw bytes
	rawData := builder.FinishedBytes()

	// Apply compression if needed
	blob := &FBSTextBlob{
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
func (b *FBSTextBlob) applyCompression() error {
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
func (b *FBSTextBlob) decompress() ([]byte, error) {
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
func (b *FBSTextBlob) Decode() error {
	if b.decoded {
		return nil // Already decoded
	}

	data, err := b.decompress()
	if err != nil {
		return fmt.Errorf("decompression failed: %w", err)
	}

	b.decodedData = data
	blobSet := textblob.GetRootAsTextBlobSet(data, 0)
	b.decodedMetrics = blobSet
	b.decoded = true

	return nil
}

// Size returns the size of the encoded blob (compressed if compression is used).
func (b *FBSTextBlob) Size() int {
	if b.compression != "none" && len(b.compressed) > 0 {
		return len(b.compressed)
	}

	return len(b.data)
}

// UncompressedSize returns the size of the raw FlatBuffers data.
func (b *FBSTextBlob) UncompressedSize() int {
	return len(b.data)
}

// findMetric finds a metric by ID in the decoded FlatBuffers data.
// Note: Decode() must be called first.
func (b *FBSTextBlob) findMetric(metricID uint64) (*textblob.TextMetric, error) {
	if !b.decoded {
		return nil, fmt.Errorf("blob not decoded, call Decode() first")
	}

	numMetrics := b.decodedMetrics.MetricsLength()

	var metric textblob.TextMetric
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
func (b *FBSTextBlob) AllMetricIDs() ([]uint64, error) {
	if !b.decoded {
		return nil, fmt.Errorf("blob not decoded, call Decode() first")
	}

	numMetrics := b.decodedMetrics.MetricsLength()
	ids := make([]uint64, 0, numMetrics)

	var metric textblob.TextMetric
	for i := 0; i < numMetrics; i++ {
		if b.decodedMetrics.Metrics(&metric, i) {
			ids = append(ids, metric.Id())
		}
	}

	return ids, nil
}

// All returns an iterator over all (timestamp, value) pairs for the given metric.
// This mirrors mebo's TextBlob.All() API.
func (b *FBSTextBlob) All(metricID uint64) iter.Seq2[int64, string] {
	metric, err := b.findMetric(metricID)
	if err != nil {
		return func(yield func(int64, string) bool) {}
	}

	count := metric.TimestampLength()

	return func(yield func(int64, string) bool) {
		for i := 0; i < count; i++ {
			ts := metric.Timestamp(i)
			val := string(metric.Value(i))
			if !yield(ts, val) {
				return
			}
		}
	}
}

// AllTimestamps returns an iterator over all timestamps for the given metric.
// This mirrors mebo's TextBlob.AllTimestamps() API.
func (b *FBSTextBlob) AllTimestamps(metricID uint64) iter.Seq[int64] {
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
// This mirrors mebo's TextBlob.AllValues() API.
func (b *FBSTextBlob) AllValues(metricID uint64) iter.Seq[string] {
	metric, err := b.findMetric(metricID)
	if err != nil {
		return func(yield func(string) bool) {}
	}

	count := metric.ValueLength()

	return func(yield func(string) bool) {
		for i := 0; i < count; i++ {
			if !yield(string(metric.Value(i))) {
				return
			}
		}
	}
}

// TimestampAt returns the timestamp at the given index for the metric.
// This mirrors mebo's TextBlob.TimestampAt() API.
func (b *FBSTextBlob) TimestampAt(metricID uint64, index int) (int64, error) {
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
// This mirrors mebo's TextBlob.ValueAt() API.
func (b *FBSTextBlob) ValueAt(metricID uint64, index int) (string, error) {
	metric, err := b.findMetric(metricID)
	if err != nil {
		return "", err
	}

	if index < 0 || index >= metric.ValueLength() {
		return "", fmt.Errorf("index %d out of range [0, %d)", index, metric.ValueLength())
	}

	return string(metric.Value(index)), nil
}

// TagAt returns the tag at the given index for the metric.
// This mirrors mebo's TextBlob.TagAt() API.
func (b *FBSTextBlob) TagAt(metricID uint64, index int) (string, error) {
	metric, err := b.findMetric(metricID)
	if err != nil {
		return "", err
	}

	if index < 0 || index >= metric.TagLength() {
		return "", fmt.Errorf("index %d out of range [0, %d)", index, metric.TagLength())
	}

	return string(metric.Tag(index)), nil
}
