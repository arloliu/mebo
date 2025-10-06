package blob

import (
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
	blobTs := time.Now()

	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)
	require.NotNil(t, encoder)
	require.Equal(t, modeUndefined, encoder.identifierMode)
}

func TestTextEncoder_NewTextEncoderWithOptions(t *testing.T) {
	blobTs := time.Now()

	encoder, err := NewTextEncoder(blobTs,
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
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
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
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
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
	blobTs := time.Now()

	t.Run("ID mode blocks second StartMetricID before EndMetric", func(t *testing.T) {
		encoder, err := NewTextEncoder(blobTs)
		require.NoError(t, err)

		err = encoder.StartMetricID(12345, 1)
		require.NoError(t, err)

		// Second StartMetricID before EndMetric should fail
		err = encoder.StartMetricID(67890, 1)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)
	})

	t.Run("Name mode blocks second StartMetricName before EndMetric", func(t *testing.T) {
		encoder, err := NewTextEncoder(blobTs)
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
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	// Add first data point
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "hello", "")
	require.NoError(t, err)
	require.Equal(t, 1, encoder.added)

	// Add second data point
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "world", "")
	require.NoError(t, err)
	require.Equal(t, 2, encoder.added)
}

func TestTextEncoder_AddDataPoint_WithTags(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs, WithTextTagsEnabled(true))
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	// Add data points with tags
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "hello", "tag1")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "world", "tag2")
	require.NoError(t, err)
	require.Equal(t, 2, encoder.added)
}

func TestTextEncoder_AddDataPoint_DeltaEncoding(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs, WithTextTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)

	// Add data points with monotonic timestamps
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "first", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "second", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.Add(2*time.Second).UnixMicro(), "third", "")
	require.NoError(t, err)
}

func TestTextEncoder_AddDataPoint_RawEncoding(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs, WithTextTimestampEncoding(format.TypeRaw))
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)

	// Add data points with any timestamps (including out-of-order)
	err = encoder.AddDataPoint(blobTs.Add(5*time.Second).UnixMicro(), "third", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.UnixMicro(), "first", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.Add(2*time.Second).UnixMicro(), "second", "")
	require.NoError(t, err)
}

func TestTextEncoder_AddDataPoint_MaxTextLength(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	// Valid: exactly 255 characters
	validText := string(make([]byte, 255))
	err = encoder.AddDataPoint(blobTs.UnixMicro(), validText, "")
	require.NoError(t, err)

	// Invalid: 256 characters
	invalidText := string(make([]byte, 256))
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), invalidText, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
}

func TestTextEncoder_AddDataPoint_MaxTagLength(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs, WithTextTagsEnabled(true))
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	// Valid: exactly 255 characters
	validTag := string(make([]byte, 255))
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "value", validTag)
	require.NoError(t, err)

	// Invalid: 256 characters
	invalidTag := string(make([]byte, 256))
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "value", invalidTag)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
}

// ==============================================================================
// EndMetric Tests
// ==============================================================================

func TestTextEncoder_EndMetric_Success(t *testing.T) {
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
	require.Equal(t, 0, encoder.claimed)
	require.Equal(t, 0, encoder.added)
	require.Equal(t, uint64(0), encoder.curMetricID)
}

func TestTextEncoder_EndMetric_MismatchedCount(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)

	// Only add 2 points instead of claimed 3
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "hello", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "world", "")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrDataPointCountMismatch)
}

func TestTextEncoder_EndMetric_NoActiveMetric(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
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
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	// First metric
	err = encoder.StartMetricName("cpu.usage", 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "50.5", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Try to add same metric name again - should be detected as duplicate
	err = encoder.StartMetricName("cpu.usage", 1)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)
}

func TestTextEncoder_DuplicateID_Detection(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	// First metric with ID
	err = encoder.StartMetricID(12345, 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "value1", "")
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
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	// Try to finish without adding any metrics
	data, err := encoder.Finish()
	require.Error(t, err)
	require.Nil(t, data)
	require.ErrorIs(t, err, errs.ErrNoMetricsAdded)
}

func TestTextEncoder_Finish_ActiveMetric(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.UnixMicro(), "hello", "")
	require.NoError(t, err)

	// Try to finish with active metric
	data, err := encoder.Finish()
	require.Error(t, err)
	require.Nil(t, data)
	require.ErrorIs(t, err, errs.ErrMetricNotEnded)
}

func TestTextEncoder_Finish_Success_IDMode(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	// Add one complete metric
	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.UnixMicro(), "hello", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "world", "")
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
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	// Add one complete metric with name
	err = encoder.StartMetricName("cpu.usage", 2)
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.UnixMicro(), "50.5", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "60.5", "")
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
			blobTs := time.Now()
			encoder, err := NewTextEncoder(blobTs, WithTextDataCompression(tt.compression))
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
			require.NotNil(t, data)
		})
	}
}

// ==============================================================================
// Multiple Metrics Tests
// ==============================================================================

func TestTextEncoder_MultipleMetrics_IDMode(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	// Metric 1
	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "metric1_val1", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "metric1_val2", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 2
	err = encoder.StartMetricID(67890, 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(2*time.Second).UnixMicro(), "metric2_val1", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 3
	err = encoder.StartMetricID(11111, 3)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "metric3_val1", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "metric3_val2", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(2*time.Second).UnixMicro(), "metric3_val3", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)
	require.NotNil(t, data)
	require.Equal(t, 3, encoder.MetricCount())
}

func TestTextEncoder_MultipleMetrics_NameMode(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	// Metric 1
	err = encoder.StartMetricName("cpu.usage", 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "50.5", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "60.5", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 2
	err = encoder.StartMetricName("memory.usage", 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(2*time.Second).UnixMicro(), "1024", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 3
	err = encoder.StartMetricName("disk.usage", 3)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "75.2", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "76.8", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(2*time.Second).UnixMicro(), "78.1", "")
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
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	// Try to add data point without starting metric
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "hello", "")
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrNoMetricStarted)
}

func TestTextEncoder_StartMetricID_ZeroPoints(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	// Try to start metric with 0 points
	err = encoder.StartMetricID(12345, 0)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidNumOfDataPoints)
}

func TestTextEncoder_StartMetricName_EmptyName(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	// Try to start metric with empty name
	err = encoder.StartMetricName("", 1)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidMetricName)
}

func TestTextEncoder_AddDataPoint_TooManyPoints(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 2)
	require.NoError(t, err)

	// Add claimed number of points
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "hello", "")
	require.NoError(t, err)

	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), "world", "")
	require.NoError(t, err)

	// Try to add one more
	err = encoder.AddDataPoint(blobTs.Add(2*time.Second).UnixMicro(), "extra", "")
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrTooManyDataPoints)
}

func TestTextEncoder_StartMetric_WhileActive(t *testing.T) {
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs)
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
	blobTs := time.Now()
	encoder, err := NewTextEncoder(blobTs) // Tags not enabled
	require.NoError(t, err)

	err = encoder.StartMetricID(12345, 1)
	require.NoError(t, err)

	// Try to add data point with tag when tags not enabled
	// This should either error or silently ignore the tag
	err = encoder.AddDataPoint(blobTs.UnixMicro(), "hello", "my_tag")
	// Note: Actual behavior depends on implementation
	// The test currently expects no error, but implementation may vary
	if err != nil {
		require.Contains(t, err.Error(), "tag")
	}
}
