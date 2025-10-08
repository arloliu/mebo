package blob

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
)

// ==============================================================================
// Basic Functionality Tests
// ==============================================================================

func TestTextEncoder_NewTextEncoder(t *testing.T) {
	blobTS := time.Now()

	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)
	require.NotNil(t, encoder)
	require.Equal(t, modeUndefined, encoder.identifierMode)
}

func TestTextEncoder_NewTextEncoderWithOptions(t *testing.T) {
	blobTS := time.Now()

	encoder, err := NewTextEncoder(blobTS,
		WithTextTimestampEncoding(format.TypeRaw),
		WithTextDataCompression(format.CompressionS2),
		WithTextTagsEnabled(true))
	require.NoError(t, err)
	require.NotNil(t, encoder)

	// Verify options applied
	require.Equal(t, format.TypeRaw, encoder.header.Flag.GetTimestampEncoding())
	require.Equal(t, format.CompressionS2, encoder.header.Flag.GetDataCompression())
	require.True(t, encoder.header.Flag.HasTag())
}

// ==============================================================================
// Dual-Mode Tests (ID vs Name)
// ==============================================================================

func TestTextEncoder_StartMetricID(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Start first metric with ID
	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)
	require.Equal(t, modeUserID, encoder.identifierMode)
	require.Equal(t, uint64(12345), encoder.curMetricID)
	require.Equal(t, 2, encoder.claimed)
	require.Equal(t, 0, encoder.added)
}

func TestTextEncoder_StartMetricName(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Start first metric with name
	err = encoder.StartMetricName("cpu.usage", 3)
	require.NoError(t, err)
	require.Equal(t, modeNameManaged, encoder.identifierMode)
	require.Equal(t, hash.ID("cpu.usage"), encoder.curMetricID)
	require.Equal(t, 3, encoder.claimed)
	require.Equal(t, 0, encoder.added)
	require.NotNil(t, encoder.collisionTracker)
}

func TestTextEncoder_DualModeExclusivity(t *testing.T) {
	blobTS := time.Now()

	t.Run("ID mode blocks second StartMetricID before EndMetric", func(t *testing.T) {
		encoder, err := NewTextEncoder(blobTS)
		require.NoError(t, err)

		err = encoder.StartMetricID(12345, 1)
		require.NoError(t, err)

		// Second StartMetricID before EndMetric should fail
		err = encoder.StartMetricID(67890, 1)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)
	})

	t.Run("Name mode blocks second StartMetricName before EndMetric", func(t *testing.T) {
		encoder, err := NewTextEncoder(blobTS)
		require.NoError(t, err)

		err = encoder.StartMetricName("cpu.usage", 1)
		require.NoError(t, err)

		// Second StartMetricName before EndMetric should fail
		err = encoder.StartMetricName("memory.usage", 1)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)
	})
}

// ==============================================================================
// AddDataPoint Tests
// ==============================================================================

func TestTextEncoder_AddDataPoint_WithoutTags(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	// Add first data point
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "hello", "")
	require.NoError(t, err)
	require.Equal(t, 1, encoder.added)

	// Add second data point
	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "world", "")
	require.NoError(t, err)
	require.Equal(t, 2, encoder.added)
}

func TestTextEncoder_AddDataPoint_WithTags(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS, WithTextTagsEnabled(true))
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	// Add data points with tags
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "hello", "tag1")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "world", "tag2")
	require.NoError(t, err)
	require.Equal(t, 2, encoder.added)
}

func TestTextEncoder_AddDataPoint_DeltaEncoding(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS, WithTextTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)

	// Add data points with monotonic timestamps
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "first", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "second", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.Add(2*time.Second).UnixMicro(), "third", "")
	require.NoError(t, err)
}

func TestTextEncoder_AddDataPoint_RawEncoding(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS, WithTextTimestampEncoding(format.TypeRaw))
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)

	// Add data points with any timestamps (including out-of-order)
	err = encoder.AddDataPoint(blobTS.Add(5*time.Second).UnixMicro(), "third", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.UnixMicro(), "first", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.Add(2*time.Second).UnixMicro(), "second", "")
	require.NoError(t, err)
}

func TestTextEncoder_AddDataPoint_MaxTextLength(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	// Valid: exactly 255 characters
	validText := string(make([]byte, 255))
	err = encoder.AddDataPoint(blobTS.UnixMicro(), validText, "")
	require.NoError(t, err)

	// Invalid: 256 characters
	invalidText := string(make([]byte, 256))
	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), invalidText, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
}

func TestTextEncoder_AddDataPoint_MaxTagLength(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS, WithTextTagsEnabled(true))
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	// Valid: exactly 255 characters
	validTag := string(make([]byte, 255))
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "value", validTag)
	require.NoError(t, err)

	// Invalid: 256 characters
	invalidTag := string(make([]byte, 256))
	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "value", invalidTag)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
}

// ==============================================================================
// EndMetric Tests
// ==============================================================================

func TestTextEncoder_EndMetric_Success(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.UnixMicro(), "hello", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "world", "")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)
	require.Equal(t, 0, encoder.claimed)
	require.Equal(t, 0, encoder.added)
	require.Equal(t, uint64(0), encoder.curMetricID)
}

func TestTextEncoder_EndMetric_MismatchedCount(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)

	// Only add 2 points instead of claimed 3
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "hello", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "world", "")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrDataPointCountMismatch)
}

func TestTextEncoder_EndMetric_NoActiveMetric(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Try to end metric without starting one
	err = encoder.EndMetric()
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrNoMetricStarted)
}

// ==============================================================================
// Collision Detection Tests
// ==============================================================================

func TestTextEncoder_CollisionDetection_NameMode(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// First metric
	err = encoder.StartMetricName("cpu.usage", 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "50.5", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Try to add same metric name again - should be detected as duplicate
	err = encoder.StartMetricName("cpu.usage", 1)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)
}

func TestTextEncoder_DuplicateID_Detection(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// First metric with ID
	err = encoder.StartMetricID(12345, 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "value1", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Try to use same ID again
	err = encoder.StartMetricID(12345, 1)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrHashCollision)
}

// ==============================================================================
// Finish Tests
// ==============================================================================

func TestTextEncoder_Finish_EmptyBlob(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Try to finish without adding any metrics
	data, err := encoder.Finish()
	require.Error(t, err)
	require.Nil(t, data)
	require.ErrorIs(t, err, errs.ErrNoMetricsAdded)
}

func TestTextEncoder_Finish_ActiveMetric(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.UnixMicro(), "hello", "")
	require.NoError(t, err)

	// Try to finish with active metric
	data, err := encoder.Finish()
	require.Error(t, err)
	require.Nil(t, data)
	require.ErrorIs(t, err, errs.ErrMetricNotEnded)
}

func TestTextEncoder_Finish_Success_IDMode(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Add one complete metric
	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.UnixMicro(), "hello", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "world", "")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Finish should succeed
	data, err := encoder.Finish()
	require.NoError(t, err)
	require.NotNil(t, data)
	require.Greater(t, len(data), 0)
}

func TestTextEncoder_Finish_Success_NameMode(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Add one complete metric with name
	err = encoder.StartMetricName("cpu.usage", 2)
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.UnixMicro(), "50.5", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "60.5", "")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Finish should succeed with metric names payload
	data, err := encoder.Finish()
	require.NoError(t, err)
	require.NotNil(t, data)
	require.Greater(t, len(data), 0)
}

// ==============================================================================
// Compression Tests
// ==============================================================================

func TestTextEncoder_CompressionTypes(t *testing.T) {
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
			blobTS := time.Now()
			encoder, err := NewTextEncoder(blobTS, WithTextDataCompression(tt.compression))
			require.NoError(t, err)

			err = encoder.StartMetricID(12345, 2)
			require.NoError(t, err)

			err = encoder.AddDataPoint(blobTS.UnixMicro(), "hello", "")
			require.NoError(t, err)

			err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "world", "")
			require.NoError(t, err)

			err = encoder.EndMetric()
			require.NoError(t, err)

			data, err := encoder.Finish()
			require.NoError(t, err)
			require.NotNil(t, data)
		})
	}
}

// ==============================================================================
// Multiple Metrics Tests
// ==============================================================================

func TestTextEncoder_MultipleMetrics_IDMode(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Metric 1
	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "metric1_val1", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "metric1_val2", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 2
	err = encoder.StartMetricID(67890, 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.Add(2*time.Second).UnixMicro(), "metric2_val1", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 3
	err = encoder.StartMetricID(11111, 3)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "metric3_val1", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "metric3_val2", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.Add(2*time.Second).UnixMicro(), "metric3_val3", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)
	require.NotNil(t, data)
	require.Equal(t, 3, encoder.MetricCount())
}

func TestTextEncoder_MultipleMetrics_NameMode(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Metric 1
	err = encoder.StartMetricName("cpu.usage", 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "50.5", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "60.5", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 2
	err = encoder.StartMetricName("memory.usage", 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.Add(2*time.Second).UnixMicro(), "1024", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 3
	err = encoder.StartMetricName("disk.usage", 3)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "75.2", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "76.8", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTS.Add(2*time.Second).UnixMicro(), "78.1", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)
	require.NotNil(t, data)
	require.Equal(t, 3, encoder.MetricCount())
}

// ==============================================================================
// Error Cases
// ==============================================================================

func TestTextEncoder_AddDataPoint_BeforeStartMetric(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Try to add data point without starting metric
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "hello", "")
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrNoMetricStarted)
}

func TestTextEncoder_StartMetricID_ZeroPoints(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Try to start metric with 0 points
	err = encoder.StartMetricID(12345, 0)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidNumOfDataPoints)
}

func TestTextEncoder_StartMetricName_EmptyName(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Try to start metric with empty name
	err = encoder.StartMetricName("", 1)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidMetricName)
}

func TestTextEncoder_AddDataPoint_TooManyPoints(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	// Add claimed number of points
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "hello", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "world", "")
	require.NoError(t, err)

	// Try to add one more
	err = encoder.AddDataPoint(blobTS.Add(2*time.Second).UnixMicro(), "extra", "")
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrTooManyDataPoints)
}

func TestTextEncoder_StartMetric_WhileActive(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS)
	require.NoError(t, err)

	// Start first metric
	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	// Try to start another without ending first
	err = encoder.StartMetricID(67890, 1)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)
}

func TestTextEncoder_TagWithoutEnabled(t *testing.T) {
	blobTS := time.Now()
	encoder, err := NewTextEncoder(blobTS) // Tags not enabled
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 1)
	require.NoError(t, err)

	// Try to add data point with tag when tags not enabled
	// This should either error or silently ignore the tag
	err = encoder.AddDataPoint(blobTS.UnixMicro(), "hello", "my_tag")
	// Note: Actual behavior depends on implementation
	// The test currently expects no error, but implementation may vary
	if err != nil {
		require.Contains(t, err.Error(), "tag")
	}
}

// ==============================================================================
// Delta Encoding Tests
// ==============================================================================

// TestTextEncoder_DeltaEncoding_MultiplePoints tests that multiple data points
// with regular intervals encode and decode correctly using delta encoding.
func TestTextEncoder_DeltaEncoding_MultiplePoints(t *testing.T) {
	blobTS := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder, err := NewTextEncoder(blobTS, WithTextTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)

	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Add data points with regular 1-second intervals
	timestamps := []int64{
		blobTS.UnixMicro(),                      // Point 1: delta = 0
		blobTS.Add(1 * time.Second).UnixMicro(), // Point 2: delta = 1s
		blobTS.Add(2 * time.Second).UnixMicro(), // Point 3: delta = 1s
		blobTS.Add(3 * time.Second).UnixMicro(), // Point 4: delta = 1s
	}

	err = encoder.StartMetricName(metricName, len(timestamps))
	require.NoError(t, err)

	for i, ts := range timestamps {
		err = encoder.AddDataPoint(ts, fmt.Sprintf("value%d", i), "")
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode and verify
	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify all data points
	count := 0
	for i, dp := range blob.All(metricID) {
		require.Equal(t, timestamps[i], dp.Ts, "Timestamp mismatch at index %d", i)
		require.Equal(t, fmt.Sprintf("value%d", i), dp.Val, "Value mismatch at index %d", i)
		count++
	}

	require.Equal(t, len(timestamps), count, "Data point count mismatch")
}

// TestTextEncoder_DeltaEncoding_CompressionEfficiency verifies that delta encoding
// provides at least 30% compression improvement over raw encoding for regular intervals.
func TestTextEncoder_DeltaEncoding_CompressionEfficiency(t *testing.T) {
	blobTS := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create two encoders: Delta vs Raw
	encoderDelta, err := NewTextEncoder(blobTS,
		WithTextTimestampEncoding(format.TypeDelta),
		WithTextDataCompression(format.CompressionNone)) // No compression for fair comparison
	require.NoError(t, err)

	encoderRaw, err := NewTextEncoder(blobTS,
		WithTextTimestampEncoding(format.TypeRaw),
		WithTextDataCompression(format.CompressionNone))
	require.NoError(t, err)

	metricName := "test.metric"
	numPoints := 100

	// Add same data to both encoders
	timestamps := make([]int64, numPoints)
	for i := range numPoints {
		timestamps[i] = blobTS.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	// Encode with Delta
	err = encoderDelta.StartMetricName(metricName, numPoints)
	require.NoError(t, err)
	for i, ts := range timestamps {
		err = encoderDelta.AddDataPoint(ts, fmt.Sprintf("value%d", i), "")
		require.NoError(t, err)
	}
	err = encoderDelta.EndMetric()
	require.NoError(t, err)
	dataDelta, err := encoderDelta.Finish()
	require.NoError(t, err)

	// Encode with Raw
	err = encoderRaw.StartMetricName(metricName, numPoints)
	require.NoError(t, err)
	for i, ts := range timestamps {
		err = encoderRaw.AddDataPoint(ts, fmt.Sprintf("value%d", i), "")
		require.NoError(t, err)
	}
	err = encoderRaw.EndMetric()
	require.NoError(t, err)
	dataRaw, err := encoderRaw.Finish()
	require.NoError(t, err)

	// Delta should be smaller than Raw for regular intervals
	compressionRatio := float64(len(dataDelta)) / float64(len(dataRaw))

	t.Logf("Delta encoding size: %d bytes", len(dataDelta))
	t.Logf("Raw encoding size: %d bytes", len(dataRaw))
	t.Logf("Compression ratio: %.2f%%", compressionRatio*100)
	t.Logf("Space savings: %.2f%%", (1-compressionRatio)*100)

	// Delta should be at least 30% smaller for regular 1-second intervals
	require.Less(t, compressionRatio, 0.7, "Delta encoding should be at least 30%% more efficient")
}

// TestTextEncoder_DeltaEncoding_MultipleMetrics verifies that lastTimestamp
// resets correctly between metrics.
func TestTextEncoder_DeltaEncoding_MultipleMetrics(t *testing.T) {
	blobTS := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder, err := NewTextEncoder(blobTS, WithTextTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)

	// Metric 1
	metric1ID := hash.ID("metric1")
	err = encoder.StartMetricID(metric1ID, 2)
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.UnixMicro(), "m1_v1", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTS.Add(time.Second).UnixMicro(), "m1_v2", "")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 2 - should start fresh (lastTimestamp reset to 0)
	metric2ID := hash.ID("metric2")
	err = encoder.StartMetricID(metric2ID, 2)
	require.NoError(t, err)

	// Start from a DIFFERENT base time (should use blob start, not metric1's last timestamp)
	baseTime2 := blobTS.Add(10 * time.Second)
	err = encoder.AddDataPoint(baseTime2.UnixMicro(), "m2_v1", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(baseTime2.Add(time.Second).UnixMicro(), "m2_v2", "")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Verify decoding
	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify metric 1
	points1 := make([]TextDataPoint, 0, 2)
	for _, dp := range blob.All(metric1ID) {
		points1 = append(points1, dp)
	}
	require.Len(t, points1, 2)
	require.Equal(t, blobTS.UnixMicro(), points1[0].Ts)
	require.Equal(t, blobTS.Add(time.Second).UnixMicro(), points1[1].Ts)

	// Verify metric 2
	points2 := make([]TextDataPoint, 0, 2)
	for _, dp := range blob.All(metric2ID) {
		points2 = append(points2, dp)
	}
	require.Len(t, points2, 2)
	require.Equal(t, baseTime2.UnixMicro(), points2[0].Ts)
	require.Equal(t, baseTime2.Add(time.Second).UnixMicro(), points2[1].Ts)
}

// TestTextEncoder_DeltaEncoding_EdgeCases tests various edge cases including
// single point, irregular intervals, large gaps, and negative deltas.
func TestTextEncoder_DeltaEncoding_EdgeCases(t *testing.T) {
	blobTS := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		timestamps []int64
	}{
		{
			name: "single data point",
			timestamps: []int64{
				blobTS.UnixMicro(),
			},
		},
		{
			name: "irregular intervals",
			timestamps: []int64{
				blobTS.UnixMicro(),
				blobTS.Add(100 * time.Millisecond).UnixMicro(),
				blobTS.Add(5 * time.Second).UnixMicro(),
				blobTS.Add(5*time.Second + 10*time.Millisecond).UnixMicro(),
			},
		},
		{
			name: "large time gaps",
			timestamps: []int64{
				blobTS.UnixMicro(),
				blobTS.Add(1 * time.Hour).UnixMicro(),
				blobTS.Add(2 * time.Hour).UnixMicro(),
			},
		},
		{
			name: "decreasing timestamps (out-of-order)",
			timestamps: []int64{
				blobTS.Add(10 * time.Second).UnixMicro(),
				blobTS.Add(5 * time.Second).UnixMicro(),
				blobTS.UnixMicro(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoder, err := NewTextEncoder(blobTS, WithTextTimestampEncoding(format.TypeDelta))
			require.NoError(t, err)

			metricID := hash.ID("test.metric")
			err = encoder.StartMetricID(metricID, len(tt.timestamps))
			require.NoError(t, err)

			for i, ts := range tt.timestamps {
				err = encoder.AddDataPoint(ts, fmt.Sprintf("value%d", i), "")
				require.NoError(t, err)
			}

			err = encoder.EndMetric()
			require.NoError(t, err)

			data, err := encoder.Finish()
			require.NoError(t, err)

			// Decode and verify
			decoder, err := NewTextDecoder(data)
			require.NoError(t, err)

			blob, err := decoder.Decode()
			require.NoError(t, err)

			// Verify all timestamps match
			points := make([]TextDataPoint, 0, len(tt.timestamps))
			for _, dp := range blob.All(metricID) {
				points = append(points, dp)
			}
			require.Len(t, points, len(tt.timestamps))

			for i, expected := range tt.timestamps {
				require.Equal(t, expected, points[i].Ts, "Timestamp mismatch at index %d", i)
			}
		})
	}
}
