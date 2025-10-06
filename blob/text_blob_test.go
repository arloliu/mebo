package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/stretchr/testify/require"
)

func TestTextBlob_EmptyBlob(t *testing.T) {
	var blob TextBlob

	// Type identification methods - should work on empty blob
	require.False(t, blob.IsNumeric(), "empty blob should not be numeric")
	require.True(t, blob.IsText(), "empty blob should be text type")

	numBlob, ok := blob.AsNumeric()
	require.False(t, ok, "empty blob should not convert to NumericBlob")
	require.Zero(t, numBlob)

	textBlob, ok := blob.AsText()
	require.True(t, ok, "empty blob should convert to TextBlob")
	require.Equal(t, blob, textBlob)

	// Metadata methods - should return zero/empty values
	require.Zero(t, blob.StartTime(), "empty blob should have zero start time")
	require.Zero(t, blob.MetricCount(), "empty blob should have 0 metrics")

	// Has* methods - should return false
	require.False(t, blob.HasMetricID(100), "empty blob should not have any metric ID")
	require.False(t, blob.HasMetricID(0), "empty blob should not have any metric ID")
	require.False(t, blob.HasMetricName("test"), "empty blob should not have any metric name")
	require.False(t, blob.HasMetricName(""), "empty blob should not have any metric name")

	// Slice methods - should return empty slices
	require.Empty(t, blob.MetricIDs(), "empty blob should have empty metric IDs")
	require.Empty(t, blob.MetricNames(), "empty blob should have empty metric names")

	// Len methods - should return 0
	require.Zero(t, blob.Len(100), "empty blob should return 0 length for any metric ID")
	require.Zero(t, blob.Len(0), "empty blob should return 0 length for zero metric ID")
	require.Zero(t, blob.LenByName("test"), "empty blob should return 0 length for any metric name")
	require.Zero(t, blob.LenByName(""), "empty blob should return 0 length for empty metric name")

	// Iteration methods - All* - should return empty iterators (no panic)
	t.Run("All", func(t *testing.T) {
		count := 0
		for range blob.All(100) {
			count++
		}
		require.Zero(t, count, "All should return empty iterator for empty blob")
	})

	t.Run("AllByName", func(t *testing.T) {
		count := 0
		for range blob.AllByName("test") {
			count++
		}
		require.Zero(t, count, "AllByName should return empty iterator for empty blob")
	})

	t.Run("AllTimestamps", func(t *testing.T) {
		count := 0
		for range blob.AllTimestamps(100) {
			count++
		}
		require.Zero(t, count, "AllTimestamps should return empty iterator for empty blob")
	})

	t.Run("AllTimestampsByName", func(t *testing.T) {
		count := 0
		for range blob.AllTimestampsByName("test") {
			count++
		}
		require.Zero(t, count, "AllTimestampsByName should return empty iterator for empty blob")
	})

	t.Run("AllValues", func(t *testing.T) {
		count := 0
		for range blob.AllValues(100) {
			count++
		}
		require.Zero(t, count, "AllValues should return empty iterator for empty blob")
	})

	t.Run("AllValuesByName", func(t *testing.T) {
		count := 0
		for range blob.AllValuesByName("test") {
			count++
		}
		require.Zero(t, count, "AllValuesByName should return empty iterator for empty blob")
	})

	t.Run("AllTags", func(t *testing.T) {
		count := 0
		for range blob.AllTags(100) {
			count++
		}
		require.Zero(t, count, "AllTags should return empty iterator for empty blob")
	})

	t.Run("AllTagsByName", func(t *testing.T) {
		count := 0
		for range blob.AllTagsByName("test") {
			count++
		}
		require.Zero(t, count, "AllTagsByName should return empty iterator for empty blob")
	})

	// Random access methods - *At - should return false
	t.Run("ValueAt", func(t *testing.T) {
		v, ok := blob.ValueAt(100, 0)
		require.False(t, ok, "ValueAt should return false for empty blob")
		require.Empty(t, v, "ValueAt should return empty string for empty blob")

		v, ok = blob.ValueAt(100, 100)
		require.False(t, ok, "ValueAt should return false for any index")
		require.Empty(t, v, "ValueAt should return empty string for any index")
	})

	t.Run("ValueAtByName", func(t *testing.T) {
		v, ok := blob.ValueAtByName("test", 0)
		require.False(t, ok, "ValueAtByName should return false for empty blob")
		require.Empty(t, v, "ValueAtByName should return empty string for empty blob")

		v, ok = blob.ValueAtByName("test", 100)
		require.False(t, ok, "ValueAtByName should return false for any index")
		require.Empty(t, v, "ValueAtByName should return empty string for any index")
	})

	t.Run("TimestampAt", func(t *testing.T) {
		ts, ok := blob.TimestampAt(100, 0)
		require.False(t, ok, "TimestampAt should return false for empty blob")
		require.Zero(t, ts, "TimestampAt should return 0 for empty blob")

		ts, ok = blob.TimestampAt(100, 100)
		require.False(t, ok, "TimestampAt should return false for any index")
		require.Zero(t, ts, "TimestampAt should return 0 for any index")
	})

	t.Run("TimestampAtByName", func(t *testing.T) {
		ts, ok := blob.TimestampAtByName("test", 0)
		require.False(t, ok, "TimestampAtByName should return false for empty blob")
		require.Zero(t, ts, "TimestampAtByName should return 0 for empty blob")

		ts, ok = blob.TimestampAtByName("test", 100)
		require.False(t, ok, "TimestampAtByName should return false for any index")
		require.Zero(t, ts, "TimestampAtByName should return 0 for any index")
	})

	t.Run("TagAt", func(t *testing.T) {
		tag, ok := blob.TagAt(100, 0)
		require.False(t, ok, "TagAt should return false for empty blob")
		require.Empty(t, tag, "TagAt should return empty string for empty blob")

		tag, ok = blob.TagAt(100, 100)
		require.False(t, ok, "TagAt should return false for any index")
		require.Empty(t, tag, "TagAt should return empty string for any index")
	})

	t.Run("TagAtByName", func(t *testing.T) {
		tag, ok := blob.TagAtByName("test", 0)
		require.False(t, ok, "TagAtByName should return false for empty blob")
		require.Empty(t, tag, "TagAtByName should return empty string for empty blob")

		tag, ok = blob.TagAtByName("test", 100)
		require.False(t, ok, "TagAtByName should return false for any index")
		require.Empty(t, tag, "TagAtByName should return empty string for any index")
	})
}

// TestTextBlob_All tests the All iterator method
func TestTextBlob_All(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create encoder and add data
	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(100, 3))
	require.NoError(t, encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), "value1", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.Add(2*time.Second).UnixMicro(), "value2", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.Add(3*time.Second).UnixMicro(), "value3", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode blob
	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test All iterator
	points := make([]TextDataPoint, 0, 3) // 3 data points
	for _, dp := range blob.All(100) {
		points = append(points, dp)
	}

	require.Len(t, points, 3)
	require.Equal(t, "value1", points[0].Val)
	require.Equal(t, "value2", points[1].Val)
	require.Equal(t, "value3", points[2].Val)
}

// TestTextBlob_AllTimestamps tests the AllTimestamps iterator
func TestTextBlob_AllTimestamps(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create encoder with delta encoding
	encoder, err := NewTextEncoder(startTime, WithTextTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(200, 2))
	ts1 := startTime.Add(time.Second).UnixMicro()
	ts2 := startTime.Add(2 * time.Second).UnixMicro()
	require.NoError(t, encoder.AddDataPoint(ts1, "val1", ""))
	require.NoError(t, encoder.AddDataPoint(ts2, "val2", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode and test
	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	timestamps := make([]int64, 0, 2) // 2 timestamps
	for ts := range blob.AllTimestamps(200) {
		timestamps = append(timestamps, ts)
	}

	require.Len(t, timestamps, 2)
	require.Equal(t, ts1, timestamps[0])
	require.Equal(t, ts2, timestamps[1])
}

// TestTextBlob_AllValues tests the AllValues iterator
func TestTextBlob_AllValues(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(300, 3))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "first", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "second", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "third", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	values := make([]string, 0, 3) // 3 values
	for val := range blob.AllValues(300) {
		values = append(values, val)
	}

	require.Equal(t, []string{"first", "second", "third"}, values)
}

// TestTextBlob_AllTags tests the AllTags iterator with tags enabled
func TestTextBlob_AllTags(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime, WithTextTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(400, 2))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val1", "tag1"))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val2", "tag2"))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	tags := make([]string, 0, 2) // 2 tags
	for tag := range blob.AllTags(400) {
		tags = append(tags, tag)
	}

	require.Equal(t, []string{"tag1", "tag2"}, tags)
}

// TestTextBlob_AllTags_Disabled tests AllTags when tags are disabled
func TestTextBlob_AllTags_Disabled(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime) // Tags disabled by default
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(500, 1))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "value", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Should return empty iterator
	count := 0
	for range blob.AllTags(500) {
		count++
	}

	require.Equal(t, 0, count)
}

// TestTextBlob_AllByName tests the AllByName iterator
func TestTextBlob_AllByName(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricName("cpu.usage", 2))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "10.5", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), "15.2", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	values := make([]string, 0, 2) // 2 values for cpu.usage
	for _, dp := range blob.AllByName("cpu.usage") {
		values = append(values, dp.Val)
	}

	require.Equal(t, []string{"10.5", "15.2"}, values)
}

// TestTextBlob_AllTimestampsByName tests AllTimestampsByName
func TestTextBlob_AllTimestampsByName(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricName("memory.used", 2))
	ts1 := startTime.UnixMicro()
	ts2 := startTime.Add(time.Minute).UnixMicro()
	require.NoError(t, encoder.AddDataPoint(ts1, "1024", ""))
	require.NoError(t, encoder.AddDataPoint(ts2, "2048", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	timestamps := make([]int64, 0, 2) // 2 timestamps for memory.used
	for ts := range blob.AllTimestampsByName("memory.used") {
		timestamps = append(timestamps, ts)
	}

	require.Equal(t, []int64{ts1, ts2}, timestamps)
}

// TestTextBlob_AllValuesByName tests AllValuesByName
func TestTextBlob_AllValuesByName(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricName("disk.free", 3))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "100GB", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "95GB", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "90GB", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	values := make([]string, 0, 3) // 3 values for disk.free
	for val := range blob.AllValuesByName("disk.free") {
		values = append(values, val)
	}

	require.Equal(t, []string{"100GB", "95GB", "90GB"}, values)
}

// TestTextBlob_AllTagsByName tests AllTagsByName
func TestTextBlob_AllTagsByName(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime, WithTextTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricName("network.error", 2))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "timeout", "eth0"))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "refused", "eth1"))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	tags := make([]string, 0, 2) // 2 tags for network.error
	for tag := range blob.AllTagsByName("network.error") {
		tags = append(tags, tag)
	}

	require.Equal(t, []string{"eth0", "eth1"}, tags)
}

// TestTextBlob_Len tests the Len method
func TestTextBlob_Len(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(100, 5))
	for i := range 5 {
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val", ""))
		_ = i
	}
	require.NoError(t, encoder.EndMetric())

	require.NoError(t, encoder.StartMetricID(200, 3))
	for i := range 3 {
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val", ""))
		_ = i
	}
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	require.Equal(t, 5, blob.Len(100))
	require.Equal(t, 3, blob.Len(200))
	require.Equal(t, 0, blob.Len(999)) // Non-existent metric
}

// TestTextBlob_LenByName tests the LenByName method
func TestTextBlob_LenByName(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricName("metric.a", 4))
	for i := range 4 {
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val", ""))
		_ = i
	}
	require.NoError(t, encoder.EndMetric())

	require.NoError(t, encoder.StartMetricName("metric.b", 7))
	for i := range 7 {
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val", ""))
		_ = i
	}
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	require.Equal(t, 4, blob.LenByName("metric.a"))
	require.Equal(t, 7, blob.LenByName("metric.b"))
	require.Equal(t, 0, blob.LenByName("nonexistent"))
}

// TestTextBlob_NonExistentMetric tests iterators with non-existent metrics
func TestTextBlob_NonExistentMetric(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(100, 1))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "value", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// All should return empty iterators
	count := 0
	for range blob.All(999) {
		count++
	}
	require.Equal(t, 0, count)

	count = 0
	for range blob.AllTimestamps(999) {
		count++
	}
	require.Equal(t, 0, count)

	count = 0
	for range blob.AllValues(999) {
		count++
	}
	require.Equal(t, 0, count)

	count = 0
	for range blob.AllTags(999) {
		count++
	}
	require.Equal(t, 0, count)
}

// TestTextBlob_RawTimestampEncoding tests iterators with raw timestamp encoding
func TestTextBlob_RawTimestampEncoding(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime, WithTextTimestampEncoding(format.TypeRaw))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(100, 2))
	ts1 := startTime.Add(time.Hour).UnixMicro()
	ts2 := startTime.Add(2 * time.Hour).UnixMicro()
	require.NoError(t, encoder.AddDataPoint(ts1, "val1", ""))
	require.NoError(t, encoder.AddDataPoint(ts2, "val2", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Check timestamps are decoded correctly
	timestamps := make([]int64, 0, 2) // 2 timestamps
	for ts := range blob.AllTimestamps(100) {
		timestamps = append(timestamps, ts)
	}

	require.Equal(t, []int64{ts1, ts2}, timestamps)
}

// TestTextBlob_ValueAt tests random access to values
func TestTextBlob_ValueAt(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(100, 5))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "first", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "second", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "third", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "fourth", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "fifth", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test valid indices
	val, ok := blob.ValueAt(100, 0)
	require.True(t, ok)
	require.Equal(t, "first", val)

	val, ok = blob.ValueAt(100, 2)
	require.True(t, ok)
	require.Equal(t, "third", val)

	val, ok = blob.ValueAt(100, 4)
	require.True(t, ok)
	require.Equal(t, "fifth", val)

	// Test out of bounds
	_, ok = blob.ValueAt(100, -1)
	require.False(t, ok)

	_, ok = blob.ValueAt(100, 5)
	require.False(t, ok)

	// Test non-existent metric
	_, ok = blob.ValueAt(999, 0)
	require.False(t, ok)
}

// TestTextBlob_TimestampAt tests random access to timestamps
func TestTextBlob_TimestampAt(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime, WithTextTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(200, 3))
	ts1 := startTime.Add(time.Second).UnixMicro()
	ts2 := startTime.Add(2 * time.Second).UnixMicro()
	ts3 := startTime.Add(3 * time.Second).UnixMicro()
	require.NoError(t, encoder.AddDataPoint(ts1, "val1", ""))
	require.NoError(t, encoder.AddDataPoint(ts2, "val2", ""))
	require.NoError(t, encoder.AddDataPoint(ts3, "val3", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test valid indices
	ts, ok := blob.TimestampAt(200, 0)
	require.True(t, ok)
	require.Equal(t, ts1, ts)

	ts, ok = blob.TimestampAt(200, 1)
	require.True(t, ok)
	require.Equal(t, ts2, ts)

	ts, ok = blob.TimestampAt(200, 2)
	require.True(t, ok)
	require.Equal(t, ts3, ts)

	// Test out of bounds
	_, ok = blob.TimestampAt(200, -1)
	require.False(t, ok)

	_, ok = blob.TimestampAt(200, 3)
	require.False(t, ok)

	// Test non-existent metric
	_, ok = blob.TimestampAt(999, 0)
	require.False(t, ok)
}

// TestTextBlob_TagAt tests random access to tags
func TestTextBlob_TagAt(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime, WithTextTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(300, 4))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val1", "tag1"))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val2", "tag2"))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val3", "tag3"))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val4", "tag4"))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test valid indices
	tag, ok := blob.TagAt(300, 0)
	require.True(t, ok)
	require.Equal(t, "tag1", tag)

	tag, ok = blob.TagAt(300, 2)
	require.True(t, ok)
	require.Equal(t, "tag3", tag)

	tag, ok = blob.TagAt(300, 3)
	require.True(t, ok)
	require.Equal(t, "tag4", tag)

	// Test out of bounds
	_, ok = blob.TagAt(300, -1)
	require.False(t, ok)

	_, ok = blob.TagAt(300, 4)
	require.False(t, ok)

	// Test non-existent metric
	_, ok = blob.TagAt(999, 0)
	require.False(t, ok)
}

// TestTextBlob_TagAt_Disabled tests TagAt when tags are disabled
func TestTextBlob_TagAt_Disabled(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime) // Tags disabled
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(400, 1))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "value", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Should return false when tags are disabled
	_, ok := blob.TagAt(400, 0)
	require.False(t, ok)
}

// TestTextBlob_RandomAccess_RawTimestamps tests random access with raw timestamp encoding
func TestTextBlob_RandomAccess_RawTimestamps(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime, WithTextTimestampEncoding(format.TypeRaw))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(500, 3))
	ts1 := startTime.Add(time.Hour).UnixMicro()
	ts2 := startTime.Add(2 * time.Hour).UnixMicro()
	ts3 := startTime.Add(3 * time.Hour).UnixMicro()
	require.NoError(t, encoder.AddDataPoint(ts1, "a", ""))
	require.NoError(t, encoder.AddDataPoint(ts2, "b", ""))
	require.NoError(t, encoder.AddDataPoint(ts3, "c", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test all indices
	ts, ok := blob.TimestampAt(500, 0)
	require.True(t, ok)
	require.Equal(t, ts1, ts)

	val, ok := blob.ValueAt(500, 1)
	require.True(t, ok)
	require.Equal(t, "b", val)

	ts, ok = blob.TimestampAt(500, 2)
	require.True(t, ok)
	require.Equal(t, ts3, ts)
}

// TestTextBlob_ValueAtByName tests random access to values by metric name
func TestTextBlob_ValueAtByName(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)

	// Add metrics with names
	require.NoError(t, encoder.StartMetricName("cpu.usage", 3))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "low", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "medium", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "high", ""))
	require.NoError(t, encoder.EndMetric())

	require.NoError(t, encoder.StartMetricName("memory.used", 2))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "1GB", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "2GB", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test valid indices for cpu.usage
	val, ok := blob.ValueAtByName("cpu.usage", 0)
	require.True(t, ok)
	require.Equal(t, "low", val)

	val, ok = blob.ValueAtByName("cpu.usage", 1)
	require.True(t, ok)
	require.Equal(t, "medium", val)

	val, ok = blob.ValueAtByName("cpu.usage", 2)
	require.True(t, ok)
	require.Equal(t, "high", val)

	// Test valid indices for memory.used
	val, ok = blob.ValueAtByName("memory.used", 0)
	require.True(t, ok)
	require.Equal(t, "1GB", val)

	val, ok = blob.ValueAtByName("memory.used", 1)
	require.True(t, ok)
	require.Equal(t, "2GB", val)

	// Test out of bounds
	_, ok = blob.ValueAtByName("cpu.usage", -1)
	require.False(t, ok)

	_, ok = blob.ValueAtByName("cpu.usage", 3)
	require.False(t, ok)

	// Test non-existent metric
	_, ok = blob.ValueAtByName("nonexistent", 0)
	require.False(t, ok)
}

// TestTextBlob_TimestampAtByName tests random access to timestamps by metric name
func TestTextBlob_TimestampAtByName(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime, WithTextTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)

	// Add metrics with names
	require.NoError(t, encoder.StartMetricName("disk.io", 4))
	ts1 := startTime.Add(time.Minute).UnixMicro()
	ts2 := startTime.Add(2 * time.Minute).UnixMicro()
	ts3 := startTime.Add(3 * time.Minute).UnixMicro()
	ts4 := startTime.Add(4 * time.Minute).UnixMicro()
	require.NoError(t, encoder.AddDataPoint(ts1, "read", ""))
	require.NoError(t, encoder.AddDataPoint(ts2, "write", ""))
	require.NoError(t, encoder.AddDataPoint(ts3, "read", ""))
	require.NoError(t, encoder.AddDataPoint(ts4, "write", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test valid indices
	ts, ok := blob.TimestampAtByName("disk.io", 0)
	require.True(t, ok)
	require.Equal(t, ts1, ts)

	ts, ok = blob.TimestampAtByName("disk.io", 1)
	require.True(t, ok)
	require.Equal(t, ts2, ts)

	ts, ok = blob.TimestampAtByName("disk.io", 2)
	require.True(t, ok)
	require.Equal(t, ts3, ts)

	ts, ok = blob.TimestampAtByName("disk.io", 3)
	require.True(t, ok)
	require.Equal(t, ts4, ts)

	// Test out of bounds
	_, ok = blob.TimestampAtByName("disk.io", -1)
	require.False(t, ok)

	_, ok = blob.TimestampAtByName("disk.io", 4)
	require.False(t, ok)

	// Test non-existent metric
	_, ok = blob.TimestampAtByName("nonexistent", 0)
	require.False(t, ok)
}

// TestTextBlob_TagAtByName tests random access to tags by metric name
func TestTextBlob_TagAtByName(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime, WithTextTagsEnabled(true))
	require.NoError(t, err)

	// Add metrics with names and tags
	require.NoError(t, encoder.StartMetricName("http.requests", 5))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "GET", "200"))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "POST", "201"))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "GET", "404"))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "DELETE", "204"))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "PUT", "200"))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test valid indices
	tag, ok := blob.TagAtByName("http.requests", 0)
	require.True(t, ok)
	require.Equal(t, "200", tag)

	tag, ok = blob.TagAtByName("http.requests", 1)
	require.True(t, ok)
	require.Equal(t, "201", tag)

	tag, ok = blob.TagAtByName("http.requests", 2)
	require.True(t, ok)
	require.Equal(t, "404", tag)

	tag, ok = blob.TagAtByName("http.requests", 3)
	require.True(t, ok)
	require.Equal(t, "204", tag)

	tag, ok = blob.TagAtByName("http.requests", 4)
	require.True(t, ok)
	require.Equal(t, "200", tag)

	// Test out of bounds
	_, ok = blob.TagAtByName("http.requests", -1)
	require.False(t, ok)

	_, ok = blob.TagAtByName("http.requests", 5)
	require.False(t, ok)

	// Test non-existent metric
	_, ok = blob.TagAtByName("nonexistent", 0)
	require.False(t, ok)
}

// TestTextBlob_TagAtByName_Disabled tests TagAtByName when tags are disabled
func TestTextBlob_TagAtByName_Disabled(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime) // Tags disabled by default
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricName("test.metric", 2))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "value1", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "value2", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Should return false when tags are disabled
	_, ok := blob.TagAtByName("test.metric", 0)
	require.False(t, ok)
}

// TestTextBlob_AtByName_WithoutMetricNameMap tests ByName methods when metricNameMap is nil
func TestTextBlob_AtByName_WithoutMetricNameMap(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)

	// Add metric by ID (no names)
	require.NoError(t, encoder.StartMetricID(100, 2))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val1", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "val2", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// All ByName methods should fail when metricNameMap is nil
	_, ok := blob.ValueAtByName("any.metric", 0)
	require.False(t, ok)

	_, ok = blob.TimestampAtByName("any.metric", 0)
	require.False(t, ok)

	_, ok = blob.TagAtByName("any.metric", 0)
	require.False(t, ok)
}

// TestTextBlob_AtByName_RawTimestampEncoding tests ByName methods with raw timestamp encoding
func TestTextBlob_AtByName_RawTimestampEncoding(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewTextEncoder(startTime,
		WithTextTimestampEncoding(format.TypeRaw),
		WithTextTagsEnabled(true),
	)
	require.NoError(t, err)

	require.NoError(t, encoder.StartMetricName("network.bandwidth", 3))
	ts1 := startTime.Add(time.Hour).UnixMicro()
	ts2 := startTime.Add(2 * time.Hour).UnixMicro()
	ts3 := startTime.Add(3 * time.Hour).UnixMicro()
	require.NoError(t, encoder.AddDataPoint(ts1, "10Mbps", "upload"))
	require.NoError(t, encoder.AddDataPoint(ts2, "20Mbps", "download"))
	require.NoError(t, encoder.AddDataPoint(ts3, "15Mbps", "upload"))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test all ByName methods work with raw timestamp encoding
	ts, ok := blob.TimestampAtByName("network.bandwidth", 0)
	require.True(t, ok)
	require.Equal(t, ts1, ts)

	val, ok := blob.ValueAtByName("network.bandwidth", 1)
	require.True(t, ok)
	require.Equal(t, "20Mbps", val)

	tag, ok := blob.TagAtByName("network.bandwidth", 2)
	require.True(t, ok)
	require.Equal(t, "upload", tag)
}

// TestTextBlob_ByName_HashFallback tests that ByName methods work with hash fallback
// when StartMetricID is used instead of StartMetricName (no collision, no byName map).
func TestTextBlob_ByName_HashFallback(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Use StartMetricID instead of StartMetricName
	// This means no metric names payload, byName will be nil
	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)

	metricName := "cpu.usage"
	metricID := hash.ID(metricName)

	require.NoError(t, encoder.StartMetricID(metricID, 3))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "10.5", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), "15.2", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.Add(2*time.Second).UnixMicro(), "20.1", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify byName is nil (no metric names payload)
	require.Nil(t, blob.index.byName, "byName should be nil when using StartMetricID")

	// Test AllByName - should work via hash fallback
	t.Run("AllByName with hash fallback", func(t *testing.T) {
		values := make([]string, 0, 3)
		for _, dp := range blob.AllByName(metricName) {
			values = append(values, dp.Val)
		}
		require.Equal(t, []string{"10.5", "15.2", "20.1"}, values,
			"AllByName should work via hash fallback when byName is nil")
	})

	// Test AllTimestampsByName - should work via hash fallback
	t.Run("AllTimestampsByName with hash fallback", func(t *testing.T) {
		timestamps := make([]int64, 0, 3)
		for ts := range blob.AllTimestampsByName(metricName) {
			timestamps = append(timestamps, ts)
		}
		expected := []int64{
			startTime.UnixMicro(),
			startTime.Add(time.Second).UnixMicro(),
			startTime.Add(2 * time.Second).UnixMicro(),
		}
		require.Equal(t, expected, timestamps,
			"AllTimestampsByName should work via hash fallback when byName is nil")
	})

	// Test AllValuesByName - should work via hash fallback
	t.Run("AllValuesByName with hash fallback", func(t *testing.T) {
		values := make([]string, 0, 3)
		for val := range blob.AllValuesByName(metricName) {
			values = append(values, val)
		}
		require.Equal(t, []string{"10.5", "15.2", "20.1"}, values,
			"AllValuesByName should work via hash fallback when byName is nil")
	})

	// Test LenByName - should work via hash fallback
	t.Run("LenByName with hash fallback", func(t *testing.T) {
		length := blob.LenByName(metricName)
		require.Equal(t, 3, length,
			"LenByName should work via hash fallback when byName is nil")
	})

	// Test ValueAtByName - should work via hash fallback
	t.Run("ValueAtByName with hash fallback", func(t *testing.T) {
		val, ok := blob.ValueAtByName(metricName, 0)
		require.True(t, ok, "ValueAtByName should work via hash fallback when byName is nil")
		require.Equal(t, "10.5", val)

		val, ok = blob.ValueAtByName(metricName, 1)
		require.True(t, ok)
		require.Equal(t, "15.2", val)

		val, ok = blob.ValueAtByName(metricName, 2)
		require.True(t, ok)
		require.Equal(t, "20.1", val)
	})

	// Test TimestampAtByName - should work via hash fallback
	t.Run("TimestampAtByName with hash fallback", func(t *testing.T) {
		ts, ok := blob.TimestampAtByName(metricName, 0)
		require.True(t, ok, "TimestampAtByName should work via hash fallback when byName is nil")
		require.Equal(t, startTime.UnixMicro(), ts)

		ts, ok = blob.TimestampAtByName(metricName, 1)
		require.True(t, ok)
		require.Equal(t, startTime.Add(time.Second).UnixMicro(), ts)
	})

	// Test HasMetricName - should work via hash fallback
	t.Run("HasMetricName with hash fallback", func(t *testing.T) {
		require.True(t, blob.HasMetricName(metricName),
			"HasMetricName should work via hash fallback when byName is nil")
		require.False(t, blob.HasMetricName("nonexistent.metric"),
			"HasMetricName should return false for nonexistent metrics")
	})
}

// TestTextBlob_ByName_WithMetricNames tests that ByName methods work correctly
// when metric names payload is created via StartMetricName.
func TestTextBlob_ByName_WithMetricNames(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Use StartMetricName which enables collision detection
	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)

	// Add metrics - collision will be detected if it occurs
	require.NoError(t, encoder.StartMetricName("metric.a", 2))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "value.a.1", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), "value.a.2", ""))
	require.NoError(t, encoder.EndMetric())

	require.NoError(t, encoder.StartMetricName("metric.b", 2))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), "value.b.1", ""))
	require.NoError(t, encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), "value.b.2", ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify byName is populated (metric names payload exists)
	require.NotNil(t, blob.index.byName, "byName should be populated when using StartMetricName")
	require.Equal(t, 2, len(blob.index.byName), "should have 2 metric names")

	// Test that ByName methods work with direct name lookup
	t.Run("AllByName with direct lookup", func(t *testing.T) {
		valuesA := make([]string, 0, 2)
		for _, dp := range blob.AllByName("metric.a") {
			valuesA = append(valuesA, dp.Val)
		}
		require.Equal(t, []string{"value.a.1", "value.a.2"}, valuesA)

		valuesB := make([]string, 0, 2)
		for _, dp := range blob.AllByName("metric.b") {
			valuesB = append(valuesB, dp.Val)
		}
		require.Equal(t, []string{"value.b.1", "value.b.2"}, valuesB)
	})

	t.Run("LenByName with direct lookup", func(t *testing.T) {
		require.Equal(t, 2, blob.LenByName("metric.a"))
		require.Equal(t, 2, blob.LenByName("metric.b"))
		require.Equal(t, 0, blob.LenByName("nonexistent"))
	})
}
