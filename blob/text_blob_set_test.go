package blob

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// createTestTextBlobs creates test blobs for TextBlobSet testing
func createTestTextBlobs(t *testing.T) []TextBlob {
	t.Helper()

	// Blob 1: startTime, 3 data points for metric 100
	blob1StartTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder1, err := NewTextEncoder(blob1StartTime)
	require.NoError(t, err)
	require.NoError(t, encoder1.StartMetricID(100, 3))
	require.NoError(t, encoder1.AddDataPoint(blob1StartTime.UnixMicro(), "val1", ""))
	require.NoError(t, encoder1.AddDataPoint(blob1StartTime.Add(time.Second).UnixMicro(), "val2", ""))
	require.NoError(t, encoder1.AddDataPoint(blob1StartTime.Add(2*time.Second).UnixMicro(), "val3", ""))
	require.NoError(t, encoder1.EndMetric())
	data1, err := encoder1.Finish()
	require.NoError(t, err)
	decoder1, err := NewTextDecoder(data1)
	require.NoError(t, err)
	blob1, err := decoder1.Decode()
	require.NoError(t, err)

	// Blob 2: startTime + 1 hour, 2 data points for metric 100
	blob2StartTime := blob1StartTime.Add(time.Hour)
	encoder2, err := NewTextEncoder(blob2StartTime)
	require.NoError(t, err)
	require.NoError(t, encoder2.StartMetricID(100, 2))
	require.NoError(t, encoder2.AddDataPoint(blob2StartTime.UnixMicro(), "val4", ""))
	require.NoError(t, encoder2.AddDataPoint(blob2StartTime.Add(time.Second).UnixMicro(), "val5", ""))
	require.NoError(t, encoder2.EndMetric())
	data2, err := encoder2.Finish()
	require.NoError(t, err)
	decoder2, err := NewTextDecoder(data2)
	require.NoError(t, err)
	blob2, err := decoder2.Decode()
	require.NoError(t, err)

	// Blob 3: startTime + 2 hours, 4 data points for metric 100
	blob3StartTime := blob1StartTime.Add(2 * time.Hour)
	encoder3, err := NewTextEncoder(blob3StartTime)
	require.NoError(t, err)
	require.NoError(t, encoder3.StartMetricID(100, 4))
	require.NoError(t, encoder3.AddDataPoint(blob3StartTime.UnixMicro(), "val6", ""))
	require.NoError(t, encoder3.AddDataPoint(blob3StartTime.Add(time.Second).UnixMicro(), "val7", ""))
	require.NoError(t, encoder3.AddDataPoint(blob3StartTime.Add(2*time.Second).UnixMicro(), "val8", ""))
	require.NoError(t, encoder3.AddDataPoint(blob3StartTime.Add(3*time.Second).UnixMicro(), "val9", ""))
	require.NoError(t, encoder3.EndMetric())
	data3, err := encoder3.Finish()
	require.NoError(t, err)
	decoder3, err := NewTextDecoder(data3)
	require.NoError(t, err)
	blob3, err := decoder3.Decode()
	require.NoError(t, err)

	return []TextBlob{blob1, blob2, blob3}
}

func TestTextBlobSet_NewTextBlobSet(t *testing.T) {
	blobs := createTestTextBlobs(t)

	// Test successful creation
	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)
	require.Equal(t, 3, blobSet.Len())

	// Test empty blobs error
	emptySet, err := NewTextBlobSet([]TextBlob{})
	require.Error(t, err)
	require.Equal(t, TextBlobSet{}, emptySet)
	require.Contains(t, err.Error(), "empty blobs")
}

func TestTextBlobSet_All(t *testing.T) {
	blobs := createTestTextBlobs(t)
	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	// Collect all data points
	values := make([]string, 0, 9) // 3 + 2 + 4 data points
	indices := make([]int, 0, 9)
	for idx, dp := range blobSet.All(100) {
		indices = append(indices, idx)
		values = append(values, dp.Val)
	}

	// Should have 3 + 2 + 4 = 9 total points
	require.Len(t, values, 9)
	require.Equal(t, []string{"val1", "val2", "val3", "val4", "val5", "val6", "val7", "val8", "val9"}, values)

	// Check indices are continuous
	expectedIndices := []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
	require.Equal(t, expectedIndices, indices)

	// Test non-existent metric
	count := 0
	for range blobSet.All(999) {
		count++
	}
	require.Equal(t, 0, count)
}

func TestTextBlobSet_AllTimestamps(t *testing.T) {
	blobs := createTestTextBlobs(t)
	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	// Collect all timestamps
	timestamps := make([]int64, 0, 9) // 3 + 2 + 4 data points
	for ts := range blobSet.AllTimestamps(100) {
		timestamps = append(timestamps, ts)
	}

	// Should have 9 timestamps
	require.Len(t, timestamps, 9)

	// Timestamps should be in ascending order
	for i := range len(timestamps) - 1 {
		require.True(t, timestamps[i] < timestamps[i+1], "timestamps should be in ascending order")
	}
}

func TestTextBlobSet_AllValues(t *testing.T) {
	blobs := createTestTextBlobs(t)
	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	// Collect all values
	values := make([]string, 0, 9) // 3 + 2 + 4 data points
	for val := range blobSet.AllValues(100) {
		values = append(values, val)
	}

	require.Equal(t, []string{"val1", "val2", "val3", "val4", "val5", "val6", "val7", "val8", "val9"}, values)
}

func TestTextBlobSet_AllTags(t *testing.T) {
	// Create blobs with tags
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder1, err := NewTextEncoder(startTime, WithTextTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, encoder1.StartMetricID(200, 2))
	require.NoError(t, encoder1.AddDataPoint(startTime.UnixMicro(), "v1", "tag1"))
	require.NoError(t, encoder1.AddDataPoint(startTime.UnixMicro(), "v2", "tag2"))
	require.NoError(t, encoder1.EndMetric())
	data1, err := encoder1.Finish()
	require.NoError(t, err)
	decoder1, err := NewTextDecoder(data1)
	require.NoError(t, err)
	blob1, err := decoder1.Decode()
	require.NoError(t, err)

	encoder2, err := NewTextEncoder(startTime.Add(time.Hour), WithTextTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, encoder2.StartMetricID(200, 2))
	require.NoError(t, encoder2.AddDataPoint(startTime.Add(time.Hour).UnixMicro(), "v3", "tag3"))
	require.NoError(t, encoder2.AddDataPoint(startTime.Add(time.Hour).UnixMicro(), "v4", "tag4"))
	require.NoError(t, encoder2.EndMetric())
	data2, err := encoder2.Finish()
	require.NoError(t, err)
	decoder2, err := NewTextDecoder(data2)
	require.NoError(t, err)
	blob2, err := decoder2.Decode()
	require.NoError(t, err)

	blobSet, err := NewTextBlobSet([]TextBlob{blob1, blob2})
	require.NoError(t, err)

	// Collect all tags
	tags := make([]string, 0, 4) // 2 + 2 tags
	for tag := range blobSet.AllTags(200) {
		tags = append(tags, tag)
	}

	require.Equal(t, []string{"tag1", "tag2", "tag3", "tag4"}, tags)
}

func TestTextBlobSet_AllByName(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create blobs with metric names
	encoder1, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder1.StartMetricName("cpu.usage", 2))
	require.NoError(t, encoder1.AddDataPoint(startTime.UnixMicro(), "10.5", ""))
	require.NoError(t, encoder1.AddDataPoint(startTime.UnixMicro(), "15.2", ""))
	require.NoError(t, encoder1.EndMetric())
	data1, err := encoder1.Finish()
	require.NoError(t, err)
	decoder1, err := NewTextDecoder(data1)
	require.NoError(t, err)
	blob1, err := decoder1.Decode()
	require.NoError(t, err)

	encoder2, err := NewTextEncoder(startTime.Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, encoder2.StartMetricName("cpu.usage", 1))
	require.NoError(t, encoder2.AddDataPoint(startTime.Add(time.Hour).UnixMicro(), "20.1", ""))
	require.NoError(t, encoder2.EndMetric())
	data2, err := encoder2.Finish()
	require.NoError(t, err)
	decoder2, err := NewTextDecoder(data2)
	require.NoError(t, err)
	blob2, err := decoder2.Decode()
	require.NoError(t, err)

	blobSet, err := NewTextBlobSet([]TextBlob{blob1, blob2})
	require.NoError(t, err)

	// Collect all values by name
	values := make([]string, 0, 3) // 3 data points for cpu.usage
	for _, dp := range blobSet.AllByName("cpu.usage") {
		values = append(values, dp.Val)
	}

	require.Equal(t, []string{"10.5", "15.2", "20.1"}, values)
}

func TestTextBlobSet_ValueAt(t *testing.T) {
	blobs := createTestTextBlobs(t)
	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	// Test values in first blob (indices 0-2)
	val, ok := blobSet.ValueAt(100, 0)
	require.True(t, ok)
	require.Equal(t, "val1", val)

	val, ok = blobSet.ValueAt(100, 2)
	require.True(t, ok)
	require.Equal(t, "val3", val)

	// Test values in second blob (indices 3-4)
	val, ok = blobSet.ValueAt(100, 3)
	require.True(t, ok)
	require.Equal(t, "val4", val)

	val, ok = blobSet.ValueAt(100, 4)
	require.True(t, ok)
	require.Equal(t, "val5", val)

	// Test values in third blob (indices 5-8)
	val, ok = blobSet.ValueAt(100, 5)
	require.True(t, ok)
	require.Equal(t, "val6", val)

	val, ok = blobSet.ValueAt(100, 8)
	require.True(t, ok)
	require.Equal(t, "val9", val)

	// Test out of bounds
	_, ok = blobSet.ValueAt(100, -1)
	require.False(t, ok)

	_, ok = blobSet.ValueAt(100, 9)
	require.False(t, ok)

	// Test non-existent metric
	_, ok = blobSet.ValueAt(999, 0)
	require.False(t, ok)
}

func TestTextBlobSet_TimestampAt(t *testing.T) {
	blobs := createTestTextBlobs(t)
	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	// Test timestamps across all blobs
	ts, ok := blobSet.TimestampAt(100, 0)
	require.True(t, ok)
	require.Greater(t, ts, int64(0))

	ts, ok = blobSet.TimestampAt(100, 4)
	require.True(t, ok)
	require.Greater(t, ts, int64(0))

	ts, ok = blobSet.TimestampAt(100, 8)
	require.True(t, ok)
	require.Greater(t, ts, int64(0))

	// Test out of bounds
	_, ok = blobSet.TimestampAt(100, 9)
	require.False(t, ok)
}

func TestTextBlobSet_TagAt(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create blobs with tags
	encoder1, err := NewTextEncoder(startTime, WithTextTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, encoder1.StartMetricID(300, 2))
	require.NoError(t, encoder1.AddDataPoint(startTime.UnixMicro(), "v1", "tag1"))
	require.NoError(t, encoder1.AddDataPoint(startTime.UnixMicro(), "v2", "tag2"))
	require.NoError(t, encoder1.EndMetric())
	data1, err := encoder1.Finish()
	require.NoError(t, err)
	decoder1, err := NewTextDecoder(data1)
	require.NoError(t, err)
	blob1, err := decoder1.Decode()
	require.NoError(t, err)

	encoder2, err := NewTextEncoder(startTime.Add(time.Hour), WithTextTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, encoder2.StartMetricID(300, 1))
	require.NoError(t, encoder2.AddDataPoint(startTime.Add(time.Hour).UnixMicro(), "v3", "tag3"))
	require.NoError(t, encoder2.EndMetric())
	data2, err := encoder2.Finish()
	require.NoError(t, err)
	decoder2, err := NewTextDecoder(data2)
	require.NoError(t, err)
	blob2, err := decoder2.Decode()
	require.NoError(t, err)

	blobSet, err := NewTextBlobSet([]TextBlob{blob1, blob2})
	require.NoError(t, err)

	// Test tags across blobs
	tag, ok := blobSet.TagAt(300, 0)
	require.True(t, ok)
	require.Equal(t, "tag1", tag)

	tag, ok = blobSet.TagAt(300, 1)
	require.True(t, ok)
	require.Equal(t, "tag2", tag)

	tag, ok = blobSet.TagAt(300, 2)
	require.True(t, ok)
	require.Equal(t, "tag3", tag)

	// Test out of bounds
	_, ok = blobSet.TagAt(300, 3)
	require.False(t, ok)
}

func TestTextBlobSet_Len(t *testing.T) {
	blobs := createTestTextBlobs(t)
	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	require.Equal(t, 3, blobSet.Len())
}

func TestTextBlobSet_TimeRange(t *testing.T) {
	blobs := createTestTextBlobs(t)
	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	start, end := blobSet.TimeRange()
	require.Equal(t, blobs[0].StartTime(), start)
	require.Equal(t, blobs[2].StartTime(), end)
}

func TestTextBlobSet_BlobAt(t *testing.T) {
	blobs := createTestTextBlobs(t)
	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	// Test valid indices
	blob := blobSet.BlobAt(0)
	require.NotNil(t, blob)
	require.Equal(t, blobs[0].StartTime(), blob.StartTime())

	blob = blobSet.BlobAt(2)
	require.NotNil(t, blob)
	require.Equal(t, blobs[2].StartTime(), blob.StartTime())

	// Test out of bounds
	blob = blobSet.BlobAt(-1)
	require.Nil(t, blob)

	blob = blobSet.BlobAt(3)
	require.Nil(t, blob)
}

func TestTextBlobSet_Blobs(t *testing.T) {
	blobs := createTestTextBlobs(t)
	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	returnedBlobs := blobSet.Blobs()
	require.Len(t, returnedBlobs, 3)

	// Verify it's a copy
	returnedBlobs[0] = TextBlob{}
	require.Equal(t, 3, blobSet.Len()) // Original should be unchanged
}

func TestTextBlobSet_BlobSorting(t *testing.T) {
	// Create blobs in reverse chronological order
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder3, err := NewTextEncoder(startTime.Add(2 * time.Hour))
	require.NoError(t, err)
	require.NoError(t, encoder3.StartMetricID(100, 1))
	require.NoError(t, encoder3.AddDataPoint(startTime.Add(2*time.Hour).UnixMicro(), "val3", ""))
	require.NoError(t, encoder3.EndMetric())
	data3, err := encoder3.Finish()
	require.NoError(t, err)
	decoder3, err := NewTextDecoder(data3)
	require.NoError(t, err)
	blob3, err := decoder3.Decode()
	require.NoError(t, err)

	encoder1, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder1.StartMetricID(100, 1))
	require.NoError(t, encoder1.AddDataPoint(startTime.UnixMicro(), "val1", ""))
	require.NoError(t, encoder1.EndMetric())
	data1, err := encoder1.Finish()
	require.NoError(t, err)
	decoder1, err := NewTextDecoder(data1)
	require.NoError(t, err)
	blob1, err := decoder1.Decode()
	require.NoError(t, err)

	encoder2, err := NewTextEncoder(startTime.Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, encoder2.StartMetricID(100, 1))
	require.NoError(t, encoder2.AddDataPoint(startTime.Add(time.Hour).UnixMicro(), "val2", ""))
	require.NoError(t, encoder2.EndMetric())
	data2, err := encoder2.Finish()
	require.NoError(t, err)
	decoder2, err := NewTextDecoder(data2)
	require.NoError(t, err)
	blob2, err := decoder2.Decode()
	require.NoError(t, err)

	// Create blob set with reverse order
	blobSet, err := NewTextBlobSet([]TextBlob{blob3, blob1, blob2})
	require.NoError(t, err)

	// Values should be in chronological order despite input order
	values := make([]string, 0, 3) // 3 blobs with 1 value each
	for val := range blobSet.AllValues(100) {
		values = append(values, val)
	}

	require.Equal(t, []string{"val1", "val2", "val3"}, values)
}

// TestTextBlobSet_MetricLen tests MetricLen and MetricLenByName methods
func TestTextBlobSet_MetricLen(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create blobs with different metrics and counts
	blobs := make([]TextBlob, 4)

	// Blob 0: metric1 with 3 points
	metricID1 := uint64(100)
	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID1, 3))
	for i := range 3 {
		ts := startTime.Add(time.Duration(i) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, fmt.Sprintf("val%d", i), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err := encoder.Finish()
	require.NoError(t, err)
	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blobs[0], err = decoder.Decode()
	require.NoError(t, err)

	// Blob 1: metric1 with 5 points (same metric, different blob)
	encoder, err = NewTextEncoder(startTime.Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID1, 5))
	for i := range 5 {
		ts := startTime.Add(time.Hour).Add(time.Duration(i) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, fmt.Sprintf("val%d", i+10), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err = encoder.Finish()
	require.NoError(t, err)
	decoder, err = NewTextDecoder(data)
	require.NoError(t, err)
	blobs[1], err = decoder.Decode()
	require.NoError(t, err)

	// Blob 2: metric2 with 2 points
	metricID2 := uint64(200)
	encoder, err = NewTextEncoder(startTime.Add(2 * time.Hour))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID2, 2))
	for i := range 2 {
		ts := startTime.Add(2 * time.Hour).Add(time.Duration(i) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, fmt.Sprintf("val%d", i+20), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err = encoder.Finish()
	require.NoError(t, err)
	decoder, err = NewTextDecoder(data)
	require.NoError(t, err)
	blobs[2], err = decoder.Decode()
	require.NoError(t, err)

	// Blob 3: metric1 with 4 points (same metric, third blob)
	encoder, err = NewTextEncoder(startTime.Add(3 * time.Hour))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID1, 4))
	for i := range 4 {
		ts := startTime.Add(3 * time.Hour).Add(time.Duration(i) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, fmt.Sprintf("val%d", i+30), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err = encoder.Finish()
	require.NoError(t, err)
	decoder, err = NewTextDecoder(data)
	require.NoError(t, err)
	blobs[3], err = decoder.Decode()
	require.NoError(t, err)

	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	t.Run("MetricLen sums across multiple blobs", func(t *testing.T) {
		// metric1 appears in blobs 0 (3 points), 1 (5 points), 3 (4 points) = 12 total
		require.Equal(t, 12, blobSet.MetricLen(metricID1))

		// metric2 appears only in blob 2 (2 points)
		require.Equal(t, 2, blobSet.MetricLen(metricID2))
	})

	t.Run("MetricLen for non-existent metric", func(t *testing.T) {
		require.Equal(t, 0, blobSet.MetricLen(99999))
	})

	t.Run("MetricLenByName", func(t *testing.T) {
		// Note: TextBlob doesn't store metric names, only IDs, so we need to create proper test
		// For this test, we'll use metric IDs as the name lookup would use hash
		require.Equal(t, 12, blobSet.MetricLen(metricID1))
		require.Equal(t, 2, blobSet.MetricLen(metricID2))
	})

	t.Run("Empty BlobSet", func(t *testing.T) {
		emptyBlobs := []TextBlob{}
		emptySet, err := NewTextBlobSet(emptyBlobs)
		require.Error(t, err)
		require.Equal(t, 0, emptySet.MetricLen(metricID1))
	})
}

// TestTextBlobSet_MetricDuration tests MetricDuration and MetricDurationByName methods
func TestTextBlobSet_MetricDuration(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create blobs with known time spans
	blobs := make([]TextBlob, 3)
	metricID1 := uint64(100)
	metricID2 := uint64(200)

	// Blob 0: metric1, timestamps 0, 10, 20 minutes
	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID1, 3))
	for i := range 3 {
		ts := startTime.Add(time.Duration(i*10) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, fmt.Sprintf("val%d", i), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err := encoder.Finish()
	require.NoError(t, err)
	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blobs[0], err = decoder.Decode()
	require.NoError(t, err)

	// Blob 1: metric1, timestamps 1h0m, 1h15m, 1h30m
	encoder, err = NewTextEncoder(startTime.Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID1, 3))
	for i := range 3 {
		ts := startTime.Add(time.Hour).Add(time.Duration(i*15) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, fmt.Sprintf("val%d", i+10), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err = encoder.Finish()
	require.NoError(t, err)
	decoder, err = NewTextDecoder(data)
	require.NoError(t, err)
	blobs[1], err = decoder.Decode()
	require.NoError(t, err)

	// Blob 2: metric2, timestamps 2h0m, 2h5m
	encoder, err = NewTextEncoder(startTime.Add(2 * time.Hour))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID2, 2))
	for i := range 2 {
		ts := startTime.Add(2 * time.Hour).Add(time.Duration(i*5) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, fmt.Sprintf("val%d", i+20), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err = encoder.Finish()
	require.NoError(t, err)
	decoder, err = NewTextDecoder(data)
	require.NoError(t, err)
	blobs[2], err = decoder.Decode()
	require.NoError(t, err)

	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	t.Run("MetricDuration across multiple blobs", func(t *testing.T) {
		// metric1: first point at 0m, last point at 1h30m = 90 minutes
		// Duration is returned in microseconds
		duration := blobSet.MetricDuration(metricID1)
		require.Equal(t, int64(90*time.Minute/time.Microsecond), duration)
	})

	t.Run("MetricDuration for single blob metric", func(t *testing.T) {
		// metric2: first point at 2h0m, last point at 2h5m = 5 minutes
		// Duration is returned in microseconds
		duration := blobSet.MetricDuration(metricID2)
		require.Equal(t, int64(5*time.Minute/time.Microsecond), duration)
	})

	t.Run("MetricDuration for non-existent metric", func(t *testing.T) {
		duration := blobSet.MetricDuration(99999)
		require.Equal(t, int64(0), duration)
	})

	t.Run("MetricDurationByName", func(t *testing.T) {
		// For TextBlob, we use metric IDs directly since names aren't stored
		duration := blobSet.MetricDuration(metricID1)
		require.Equal(t, int64(90*time.Minute/time.Microsecond), duration)

		duration = blobSet.MetricDuration(metricID2)
		require.Equal(t, int64(5*time.Minute/time.Microsecond), duration)
	})

	t.Run("Single data point", func(t *testing.T) {
		// Create blob with single point
		singleEncoder, err := NewTextEncoder(startTime)
		require.NoError(t, err)
		singleID := uint64(300)
		require.NoError(t, singleEncoder.StartMetricID(singleID, 1))
		require.NoError(t, singleEncoder.AddDataPoint(startTime.UnixMicro(), "single", ""))
		require.NoError(t, singleEncoder.EndMetric())
		singleData, err := singleEncoder.Finish()
		require.NoError(t, err)
		singleDecoder, err := NewTextDecoder(singleData)
		require.NoError(t, err)
		singleBlob, err := singleDecoder.Decode()
		require.NoError(t, err)

		singleSet, err := NewTextBlobSet([]TextBlob{singleBlob})
		require.NoError(t, err)

		duration := singleSet.MetricDuration(singleID)
		require.Equal(t, int64(0), duration, "Single point should have 0 duration")
	})
}
