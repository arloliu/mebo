package mebo

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
)

// TestNewDefaultNumericEncoder verifies the default encoder is created with expected settings
func TestNewDefaultNumericEncoder(t *testing.T) {
	startTime := time.Now()

	encoder, err := NewDefaultNumericEncoder(startTime)

	require.NoError(t, err)
	require.NotNil(t, encoder)
}

// TestNewTaggedNumericEncoder verifies tagged encoder enables tags
func TestNewTaggedNumericEncoder(t *testing.T) {
	startTime := time.Now()

	encoder, err := NewTaggedNumericEncoder(startTime)
	require.NoError(t, err)
	require.NotNil(t, encoder)

	// Verify tags are enabled by encoding with a tag
	metricID := MetricID("test.metric")
	err = encoder.StartMetricID(metricID, 1)
	require.NoError(t, err)

	err = encoder.AddDataPoint(startTime.UnixMicro(), 42.0, "host=server1")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)
	require.NotEmpty(t, data)
}

// TestNewNumericEncoder verifies custom encoder creation
func TestNewNumericEncoder(t *testing.T) {
	startTime := time.Now()

	encoder, err := NewNumericEncoder(startTime,
		blob.WithValueEncoding(format.TypeRaw),
		blob.WithValueCompression(format.CompressionZstd),
	)

	require.NoError(t, err)
	require.NotNil(t, encoder)
}

// TestNewDefaultTextEncoder verifies the default text encoder is created
func TestNewDefaultTextEncoder(t *testing.T) {
	startTime := time.Now()

	encoder, err := NewDefaultTextEncoder(startTime)

	require.NoError(t, err)
	require.NotNil(t, encoder)
}

// TestNewTaggedTextEncoder verifies tagged text encoder enables tags
func TestNewTaggedTextEncoder(t *testing.T) {
	startTime := time.Now()

	encoder, err := NewTaggedTextEncoder(startTime)
	require.NoError(t, err)
	require.NotNil(t, encoder)

	// Verify tags are enabled
	metricID := MetricID("test.status")
	err = encoder.StartMetricID(metricID, 1)
	require.NoError(t, err)

	err = encoder.AddDataPoint(startTime.UnixMicro(), "OK", "region=us-west")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)
	require.NotEmpty(t, data)
}

// TestNewTextEncoder verifies custom text encoder creation
func TestNewTextEncoder(t *testing.T) {
	startTime := time.Now()

	encoder, err := NewTextEncoder(startTime,
		blob.WithTextDataCompression(format.CompressionS2),
	)

	require.NoError(t, err)
	require.NotNil(t, encoder)
}

// TestNumericEncoderDecoder verifies basic encode/decode workflow
func TestNumericEncoderDecoder(t *testing.T) {
	startTime := time.Now()
	metricID := MetricID("cpu.usage")

	// Encode
	encoder, err := NewDefaultNumericEncoder(startTime)
	require.NoError(t, err)

	err = encoder.StartMetricID(metricID, 3)
	require.NoError(t, err)

	for i := range 3 {
		ts := startTime.Add(time.Duration(i) * time.Second)
		err = encoder.AddDataPoint(ts.UnixMicro(), float64(i*10), "")
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	decodedBlob, err := decoder.Decode()
	require.NoError(t, err)

	count := 0
	for _, dp := range decodedBlob.All(metricID) {
		require.Equal(t, float64(count*10), dp.Val)
		count++
	}
	require.Equal(t, 3, count)
}

// TestTextEncoderDecoder verifies basic text encode/decode workflow
func TestTextEncoderDecoder(t *testing.T) {
	startTime := time.Now()
	metricID := MetricID("service.status")

	// Encode
	encoder, err := NewDefaultTextEncoder(startTime)
	require.NoError(t, err)

	err = encoder.StartMetricID(metricID, 3)
	require.NoError(t, err)

	statuses := []string{"OK", "WARN", "ERROR"}
	for i, status := range statuses {
		ts := startTime.Add(time.Duration(i) * time.Second)
		err = encoder.AddDataPoint(ts.UnixMicro(), status, "")
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)

	decodedBlob, err := decoder.Decode()
	require.NoError(t, err)

	count := 0
	for _, dp := range decodedBlob.All(metricID) {
		require.Equal(t, statuses[count], dp.Val)
		count++
	}
	require.Equal(t, 3, count)
}

// TestNewNumericBlobSet verifies blob set creation
func TestNewNumericBlobSet(t *testing.T) {
	startTime := time.Now()

	// Create two blobs
	blob1 := createTestNumericBlob(t, startTime, 0)
	blob2 := createTestNumericBlob(t, startTime.Add(time.Hour), 1)

	blobSet, err := NewNumericBlobSet([]blob.NumericBlob{blob1, blob2})

	require.NoError(t, err)
	require.Equal(t, 2, blobSet.Len())
}

// TestNewTextBlobSet verifies text blob set creation
func TestNewTextBlobSet(t *testing.T) {
	startTime := time.Now()

	// Create two blobs
	blob1 := createTestTextBlob(t, startTime, 0)
	blob2 := createTestTextBlob(t, startTime.Add(time.Hour), 1)

	blobSet, err := NewTextBlobSet([]blob.TextBlob{blob1, blob2})

	require.NoError(t, err)
	require.Equal(t, 2, blobSet.Len())
}

// TestNewMaterializedNumericBlobSet verifies materialized numeric blob set
func TestNewMaterializedNumericBlobSet(t *testing.T) {
	startTime := time.Now()
	metricID := MetricID("test.metric")

	blob1 := createTestNumericBlob(t, startTime, 0)
	blob2 := createTestNumericBlob(t, startTime.Add(time.Hour), 1)

	mat, err := NewMaterializedNumericBlobSet([]blob.NumericBlob{blob1, blob2})

	require.NoError(t, err)

	// Test random access
	val, ok := mat.ValueAt(metricID, 0)
	require.True(t, ok)
	require.Equal(t, 0.0, val)

	// Verify we have points from both blobs
	val, ok = mat.ValueAt(metricID, 2)
	require.True(t, ok)
	require.Equal(t, 10.0, val) // First point from second blob
}

// TestNewMaterializedTextBlobSet verifies materialized text blob set
func TestNewMaterializedTextBlobSet(t *testing.T) {
	startTime := time.Now()
	metricID := MetricID("test.status")

	blob1 := createTestTextBlob(t, startTime, 0)
	blob2 := createTestTextBlob(t, startTime.Add(time.Hour), 1)

	mat, err := NewMaterializedTextBlobSet([]blob.TextBlob{blob1, blob2})

	require.NoError(t, err)

	// Test random access
	val, ok := mat.ValueAt(metricID, 0)
	require.True(t, ok)
	require.Equal(t, "status_0", val)

	// Verify we have points from both blobs
	val, ok = mat.ValueAt(metricID, 2)
	require.True(t, ok)
	require.Equal(t, "status_10", val) // First point from second blob
}

// TestNewBlobSet verifies heterogeneous blob set creation
func TestNewBlobSet(t *testing.T) {
	startTime := time.Now()

	numericBlob := createTestNumericBlob(t, startTime, 0)
	textBlob := createTestTextBlob(t, startTime, 0)

	blobSet := NewBlobSet([]blob.NumericBlob{numericBlob}, []blob.TextBlob{textBlob})

	numericBlobs := blobSet.NumericBlobs()
	require.Equal(t, 1, len(numericBlobs))

	textBlobs := blobSet.TextBlobs()
	require.Equal(t, 1, len(textBlobs))
}

// TestMetricID verifies hash generation is deterministic
func TestMetricID(t *testing.T) {
	name := "test.metric.name"

	id1 := MetricID(name)
	id2 := MetricID(name)

	require.Equal(t, id1, id2, "MetricID should be deterministic")
	require.NotZero(t, id1, "MetricID should not be zero")

	// Different names should produce different IDs
	differentID := MetricID("different.metric")
	require.NotEqual(t, id1, differentID)
}

// Helper function to create test numeric blob
func createTestNumericBlob(t *testing.T, startTime time.Time, offset int) blob.NumericBlob {
	t.Helper()

	encoder, err := NewDefaultNumericEncoder(startTime)
	require.NoError(t, err)

	metricID := MetricID("test.metric")
	err = encoder.StartMetricID(metricID, 2)
	require.NoError(t, err)

	for i := range 2 {
		ts := startTime.Add(time.Duration(i) * time.Minute)
		err = encoder.AddDataPoint(ts.UnixMicro(), float64(offset*10+i), "")
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	decodedBlob, err := decoder.Decode()
	require.NoError(t, err)

	return decodedBlob
}

// Helper function to create test text blob
func createTestTextBlob(t *testing.T, startTime time.Time, offset int) blob.TextBlob {
	t.Helper()

	encoder, err := NewDefaultTextEncoder(startTime)
	require.NoError(t, err)

	metricID := MetricID("test.status")
	err = encoder.StartMetricID(metricID, 2)
	require.NoError(t, err)

	for i := range 2 {
		ts := startTime.Add(time.Duration(i) * time.Minute)
		status := fmt.Sprintf("status_%d", offset*10+i)
		err = encoder.AddDataPoint(ts.UnixMicro(), status, "")
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)

	decodedBlob, err := decoder.Decode()
	require.NoError(t, err)

	return decodedBlob
}
