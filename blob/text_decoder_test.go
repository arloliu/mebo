package blob

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
)

// ==============================================================================
// Helper Functions
// ==============================================================================

// encodeTestTextBlob creates an encoded text blob for testing.
func encodeTestTextBlob(t *testing.T, opts ...TextEncoderOption) []byte {
	t.Helper()

	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs, opts...)
	require.NoError(t, err)

	// Metric 1: Multiple data points
	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "first", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "second", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(2*time.Second).UnixMicro(), "third", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 2: Single data point
	err = encoder.StartMetricID(67890, 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(3*time.Second).UnixMicro(), "single", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	return data
}

// encodeTestTextBlobWithNames creates an encoded text blob with metric names for testing.
func encodeTestTextBlobWithNames(t *testing.T, opts ...TextEncoderOption) []byte {
	t.Helper()

	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs, opts...)
	require.NoError(t, err)

	// Metric 1: cpu.usage
	err = encoder.StartMetricName("cpu.usage", 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "50.5", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "60.5", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 2: memory.usage
	err = encoder.StartMetricName("memory.usage", 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(2*time.Second).UnixMicro(), "1024", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	return data
}

// ==============================================================================
// Basic Decoder Tests
// ==============================================================================

func TestTextDecoder_NewTextDecoder(t *testing.T) {
	data := encodeTestTextBlob(t)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	require.NotNil(t, decoder)
	require.NotNil(t, decoder.header)
	require.Equal(t, 2, decoder.metricCount)
}

func TestTextDecoder_NewTextDecoder_InvalidData(t *testing.T) {
	t.Run("empty data", func(t *testing.T) {
		_, err := NewTextDecoder([]byte{})
		require.Error(t, err)
	})

	t.Run("truncated header", func(t *testing.T) {
		data := make([]byte, 20) // Less than 32 bytes
		_, err := NewTextDecoder(data)
		require.Error(t, err)
	})
}

func TestTextDecoder_Decode_IDMode(t *testing.T) {
	data := encodeTestTextBlob(t)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify blob structure
	require.Equal(t, 2, blob.MetricCount())
	require.True(t, blob.HasMetricID(12345))
	require.True(t, blob.HasMetricID(67890))
	require.False(t, blob.HasMetricID(99999))

	// Verify metric IDs
	metricIDs := blob.MetricIDs()
	require.Len(t, metricIDs, 2)
	require.Contains(t, metricIDs, uint64(12345))
	require.Contains(t, metricIDs, uint64(67890))
}

func TestTextDecoder_Decode_NameMode(t *testing.T) {
	data := encodeTestTextBlobWithNames(t)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify blob structure
	require.Equal(t, 2, blob.MetricCount())
	require.True(t, blob.HasMetricName("cpu.usage"))
	require.True(t, blob.HasMetricName("memory.usage"))
	require.False(t, blob.HasMetricName("disk.usage"))

	// Verify metric names
	metricNames := blob.MetricNames()
	require.Len(t, metricNames, 2)
	require.Contains(t, metricNames, "cpu.usage")
	require.Contains(t, metricNames, "memory.usage")
}

// ==============================================================================
// Compression Tests
// ==============================================================================

func TestTextDecoder_Decode_CompressionTypes(t *testing.T) {
	tests := []struct {
		name        string
		compression format.CompressionType
	}{
		{"None", format.CompressionNone},
		{"Zstd", format.CompressionZstd},
		{"S2", format.CompressionS2},
		{"LZ4", format.CompressionLZ4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := encodeTestTextBlob(t, WithTextDataCompression(tt.compression))

			decoder, err := NewTextDecoder(data)
			require.NoError(t, err)

			blob, err := decoder.Decode()
			require.NoError(t, err)
			require.Equal(t, 2, blob.MetricCount())
		})
	}
}

// ==============================================================================
// Timestamp Encoding Tests
// ==============================================================================

func TestTextDecoder_Decode_TimestampEncodingTypes(t *testing.T) {
	tests := []struct {
		name     string
		encoding format.EncodingType
	}{
		{"Delta", format.TypeDelta},
		{"Raw", format.TypeRaw},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := encodeTestTextBlob(t, WithTextTimestampEncoding(tt.encoding))

			decoder, err := NewTextDecoder(data)
			require.NoError(t, err)

			blob, err := decoder.Decode()
			require.NoError(t, err)
			require.Equal(t, 2, blob.MetricCount())
			require.Equal(t, tt.encoding, blob.tsEncType)
		})
	}
}

// ==============================================================================
// Tags Tests
// ==============================================================================

func TestTextDecoder_Decode_WithTags(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs, WithTextTagsEnabled(true))
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "value1", "tag1")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "value2", "tag2")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, 1, blob.MetricCount())
	require.True(t, blob.flag.HasTag())
}

// ==============================================================================
// Round-trip Tests
// ==============================================================================

func TestTextDecoder_RoundTrip_Simple(t *testing.T) {
	// Encode
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "hello", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "world", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify
	require.Equal(t, 1, blob.MetricCount())
	require.True(t, blob.HasMetricID(12345))
	// Compare times at microsecond precision (timestamps are stored as microseconds)
	require.Equal(t, blobTs.Truncate(time.Microsecond).UTC(), blob.StartTime())
}

func TestTextDecoder_RoundTrip_MultipleMetrics(t *testing.T) {
	// Encode
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	// Add 3 metrics
	for i := 0; i < 3; i++ {
		metricID := uint64(10000 + i)
		err = encoder.StartMetricID(metricID, 2)
		require.NoError(t, err)
		err = encoder.AddDataPoint(blobTs.UnixMicro(), "val1", "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "val2", "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify
	require.Equal(t, 3, blob.MetricCount())
	require.True(t, blob.HasMetricID(10000))
	require.True(t, blob.HasMetricID(10001))
	require.True(t, blob.HasMetricID(10002))
}

func TestTextDecoder_RoundTrip_WithNames(t *testing.T) {
	// Encode
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	err = encoder.StartMetricName("cpu.usage", 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "50.5", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	err = encoder.StartMetricName("memory.usage", 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "1024", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify
	require.Equal(t, 2, blob.MetricCount())
	require.True(t, blob.HasMetricName("cpu.usage"))
	require.True(t, blob.HasMetricName("memory.usage"))
	require.Len(t, blob.MetricNames(), 2)
}

func TestTextDecoder_RoundTrip_AllCompressionTypes(t *testing.T) {
	compressionTypes := []format.CompressionType{
		format.CompressionNone,
		format.CompressionZstd,
		format.CompressionS2,
		format.CompressionLZ4,
	}

	for _, compType := range compressionTypes {
		t.Run(compType.String(), func(t *testing.T) {
			// Encode
			blobTs := time.Now()
			encoder, err := NewTextEncoder(blobTs, WithTextDataCompression(compType))
			require.NoError(t, err)

			err = encoder.StartMetricID(12345, 2)
			require.NoError(t, err)
			err = encoder.AddDataPoint(blobTs.UnixMicro(), "test", "")
			require.NoError(t, err)
			err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "data", "")
			require.NoError(t, err)
			err = encoder.EndMetric()
			require.NoError(t, err)

			data, err := encoder.Finish()
			require.NoError(t, err)

			// Decode
			decoder, err := NewTextDecoder(data)
			require.NoError(t, err)

			blob, err := decoder.Decode()
			require.NoError(t, err)
			require.Equal(t, 1, blob.MetricCount())
		})
	}
}
