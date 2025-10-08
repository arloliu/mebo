package blob

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// ==============================================================================
// NumericBlob Tests - Empty Blob
// ==============================================================================

func TestNumericBlob_EmptyBlob(t *testing.T) {
	var blob NumericBlob

	// Type identification methods - should work on empty blob
	require.True(t, blob.IsNumeric(), "empty blob should be numeric type")
	require.False(t, blob.IsText(), "empty blob should not be text type")

	numBlob, ok := blob.AsNumeric()
	require.True(t, ok, "empty blob should convert to NumericBlob")
	require.Equal(t, blob, numBlob)

	textBlob, ok := blob.AsText()
	require.False(t, ok, "empty blob should not convert to TextBlob")
	require.Zero(t, textBlob)

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
		require.Zero(t, v, "ValueAt should return 0.0 for empty blob")

		v, ok = blob.ValueAt(100, 100)
		require.False(t, ok, "ValueAt should return false for any index")
		require.Zero(t, v, "ValueAt should return 0.0 for any index")
	})

	t.Run("ValueAtByName", func(t *testing.T) {
		v, ok := blob.ValueAtByName("test", 0)
		require.False(t, ok, "ValueAtByName should return false for empty blob")
		require.Zero(t, v, "ValueAtByName should return 0.0 for empty blob")

		v, ok = blob.ValueAtByName("test", 100)
		require.False(t, ok, "ValueAtByName should return false for any index")
		require.Zero(t, v, "ValueAtByName should return 0.0 for any index")
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

// ==============================================================================
// NumericBlob Tests - AllTimestamps Methods
// ==============================================================================

func TestNumericBlob_AllTimestamps(t *testing.T) {
	t.Run("ValidMetricID_RawEncoding", func(t *testing.T) {
		blob := createTestBlob(t, format.TypeRaw, format.TypeRaw)

		// Test existing metric ID
		timestamps := make([]int64, 0)
		for ts := range blob.AllTimestamps(12345) {
			timestamps = append(timestamps, ts)
		}
		require.Len(t, timestamps, 3)

		// Verify timestamps are in order
		require.True(t, timestamps[0] < timestamps[1])
		require.True(t, timestamps[1] < timestamps[2])
	})

	t.Run("ValidMetricID_DeltaEncoding", func(t *testing.T) {
		blob := createTestBlob(t, format.TypeDelta, format.TypeRaw)

		// Test existing metric ID
		timestamps := make([]int64, 0)
		for ts := range blob.AllTimestamps(67890) {
			timestamps = append(timestamps, ts)
		}
		require.Len(t, timestamps, 1)
	})

	t.Run("NonExistentMetricID", func(t *testing.T) {
		blob := createTestBlob(t, format.TypeRaw, format.TypeRaw)

		// Test non-existent metric ID
		count := 0
		for range blob.AllTimestamps(99999) {
			count++
		}
		require.Equal(t, 0, count)
	})

	t.Run("EmptyIndexEntries", func(t *testing.T) {
		// Create empty blob
		blob := NumericBlob{
			blobBase: blobBase{
				engine:    endian.GetLittleEndianEngine(),
				tsEncType: format.TypeRaw,
			},
			index: indexMaps[section.NumericIndexEntry]{
				byID: make(map[uint64]section.NumericIndexEntry),
			},
			tsPayload:  []byte{},
			valPayload: []byte{},
			valEncType: format.TypeRaw,
		}

		count := 0
		for range blob.AllTimestamps(12345) {
			count++
		}
		require.Equal(t, 0, count)
	})

	t.Run("ZeroCount", func(t *testing.T) {
		// Create blob with entry that has zero count
		entry := section.NumericIndexEntry{
			MetricID:        12345,
			Count:           0,
			TimestampOffset: 0,
			ValueOffset:     0,
		}

		blob := NumericBlob{
			blobBase: blobBase{
				engine:    endian.GetLittleEndianEngine(),
				tsEncType: format.TypeRaw,
			},
			index: indexMaps[section.NumericIndexEntry]{
				byID: map[uint64]section.NumericIndexEntry{entry.MetricID: entry},
			},
			tsPayload:  []byte{0, 0, 0, 0, 0, 0, 0, 0}, // Some data
			valPayload: []byte{},
			valEncType: format.TypeRaw,
		}

		count := 0
		for range blob.AllTimestamps(12345) {
			count++
		}
		require.Equal(t, 0, count)
	})

	t.Run("InsufficientPayloadData", func(t *testing.T) {
		// Create blob with entry that requires more data than available
		entry := section.NumericIndexEntry{
			MetricID:        12345,
			Count:           2, // Requires 16 bytes (2 * 8)
			TimestampOffset: 0,
			ValueOffset:     0,
		}

		blob := NumericBlob{
			blobBase: blobBase{
				engine:    endian.GetLittleEndianEngine(),
				tsEncType: format.TypeRaw,
			},
			index: indexMaps[section.NumericIndexEntry]{
				byID: map[uint64]section.NumericIndexEntry{entry.MetricID: entry},
			},
			tsPayload:  []byte{0, 0, 0, 0}, // Only 4 bytes
			valPayload: []byte{},
			valEncType: format.TypeRaw,
		}

		count := 0
		for range blob.AllTimestamps(12345) {
			count++
		}
		require.Equal(t, 0, count)
	})

	t.Run("MultipleMetrics", func(t *testing.T) {
		blob := createTestBlob(t, format.TypeRaw, format.TypeRaw)

		// Test all metrics
		metricIDs := []uint64{11111, 12345, 67890} // Should be sorted by encoder
		expectedCounts := []int{2, 3, 1}

		for i, metricID := range metricIDs {
			count := 0
			for range blob.AllTimestamps(metricID) {
				count++
			}
			require.Equal(t, expectedCounts[i], count, "Metric ID %d should have %d timestamps", metricID, expectedCounts[i])
		}
	})
}

func TestNumericBlob_allRawTimestamps(t *testing.T) {
	t.Run("SameByteOrder", func(t *testing.T) {
		blob := createTestBlob(t, format.TypeRaw, format.TypeRaw)

		// For same byte order, should use fast path
		require.True(t, blob.sameByteOrder)

		timestamps := make([]int64, 0)
		for ts := range blob.AllTimestamps(12345) {
			timestamps = append(timestamps, ts)
		}
		require.Len(t, timestamps, 3)
	})

	t.Run("DifferentByteOrder", func(t *testing.T) {
		// Test the slow path by creating blob with different byte order setting
		// Note: This test simulates different byte order without actually corrupting data
		blob := createTestBlob(t, format.TypeRaw, format.TypeRaw)
		originalSameByteOrder := blob.sameByteOrder
		blob.sameByteOrder = false // Force slow path

		// Verify we still get timestamps (slow path should work)
		timestamps := make([]int64, 0)
		for ts := range blob.AllTimestamps(12345) {
			timestamps = append(timestamps, ts)
		}
		require.Len(t, timestamps, 3)

		// Restore original setting for good measure
		blob.sameByteOrder = originalSameByteOrder
	})
}

func TestNumericBlob_allDeltaTimestamps(t *testing.T) {
	t.Run("DeltaEncoding", func(t *testing.T) {
		blob := createTestBlob(t, format.TypeDelta, format.TypeRaw)

		timestamps := make([]int64, 0)
		for ts := range blob.AllTimestamps(12345) {
			timestamps = append(timestamps, ts)
		}
		require.Len(t, timestamps, 3)

		// Verify timestamps are in order
		require.True(t, timestamps[0] < timestamps[1])
		require.True(t, timestamps[1] < timestamps[2])
	})
}

// ==============================================================================
// NumericBlob Tests - All Method (Combined Timestamps & Values)
// ==============================================================================

func TestNumericBlob_All(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Test data
	timestamps := []int64{
		startTime.UnixMicro(),
		startTime.Add(1 * time.Minute).UnixMicro(),
		startTime.Add(2 * time.Minute).UnixMicro(),
		startTime.Add(3 * time.Minute).UnixMicro(),
		startTime.Add(4 * time.Minute).UnixMicro(),
	}
	values := []float64{1.1, 2.2, 3.3, 4.4, 5.5}

	t.Run("RawTimestamps_RawValues", func(t *testing.T) {
		// Create encoder with raw encoding for both
		encoder, err := NewNumericEncoder(startTime,
			WithTimestampEncoding(format.TypeRaw),
			WithValueEncoding(format.TypeRaw),
		)
		require.NoError(t, err)

		// Encode data
		err = encoder.StartMetricID(metricID, len(timestamps))
		require.NoError(t, err)

		for i := range timestamps {
			err = encoder.AddDataPoint(timestamps[i], values[i], "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		// Decode and test All()
		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Collect all pairs
		var gotTimestamps []int64
		var gotValues []float64
		for _, dp := range blob.All(metricID) {
			gotTimestamps = append(gotTimestamps, dp.Ts)
			gotValues = append(gotValues, dp.Val)
		}

		require.Equal(t, timestamps, gotTimestamps)
		require.Equal(t, values, gotValues)
	})

	t.Run("DeltaTimestamps_RawValues", func(t *testing.T) {
		// Create encoder with delta timestamps, raw values
		encoder, err := NewNumericEncoder(startTime,
			WithTimestampEncoding(format.TypeDelta),
			WithValueEncoding(format.TypeRaw),
		)
		require.NoError(t, err)

		// Encode data
		err = encoder.StartMetricID(metricID, len(timestamps))
		require.NoError(t, err)

		for i := range timestamps {
			err = encoder.AddDataPoint(timestamps[i], values[i], "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		// Decode and test All()
		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Collect all pairs
		var gotTimestamps []int64
		var gotValues []float64
		for _, dp := range blob.All(metricID) {
			gotTimestamps = append(gotTimestamps, dp.Ts)
			gotValues = append(gotValues, dp.Val)
		}

		require.Equal(t, timestamps, gotTimestamps)
		require.Equal(t, values, gotValues)
	})

	t.Run("EmptyMetric", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Finish without metrics should now return error
		data, err := encoder.Finish()
		require.Error(t, err)
		require.Nil(t, data)
	})

	t.Run("NonExistentMetric", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		otherID := hash.ID("other.metric")
		err = encoder.StartMetricID(otherID, 1)
		require.NoError(t, err)

		err = encoder.AddDataPoint(timestamps[0], values[0], "")
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Should return empty iterator for non-existent metric
		count := 0
		for range blob.All(metricID) {
			count++
		}
		require.Equal(t, 0, count)
	})

	t.Run("EarlyStopIteration", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		err = encoder.StartMetricID(metricID, len(timestamps))
		require.NoError(t, err)

		for i := range timestamps {
			err = encoder.AddDataPoint(timestamps[i], values[i], "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Test early stop by breaking after 3 items
		var gotTimestamps []int64
		var gotValues []float64
		for _, dp := range blob.All(metricID) {
			gotTimestamps = append(gotTimestamps, dp.Ts)
			gotValues = append(gotValues, dp.Val)
			if len(gotTimestamps) >= 3 {
				break
			}
		}

		require.Equal(t, 3, len(gotTimestamps))
		require.Equal(t, timestamps[:3], gotTimestamps)
		require.Equal(t, values[:3], gotValues)
	})
}

func TestNumericBlob_All_MultipleMetrics(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create multiple metrics with different data
	metrics := []struct {
		name       string
		id         uint64
		timestamps []int64
		values     []float64
	}{
		{
			name: "metric.one",
			id:   hash.ID("metric.one"),
			timestamps: []int64{
				startTime.UnixMicro(),
				startTime.Add(1 * time.Minute).UnixMicro(),
				startTime.Add(2 * time.Minute).UnixMicro(),
			},
			values: []float64{10.0, 20.0, 30.0},
		},
		{
			name: "metric.two",
			id:   hash.ID("metric.two"),
			timestamps: []int64{
				startTime.UnixMicro(),
				startTime.Add(1 * time.Minute).UnixMicro(),
			},
			values: []float64{100.0, 200.0},
		},
		{
			name: "metric.three",
			id:   hash.ID("metric.three"),
			timestamps: []int64{
				startTime.UnixMicro(),
				startTime.Add(1 * time.Minute).UnixMicro(),
				startTime.Add(2 * time.Minute).UnixMicro(),
				startTime.Add(3 * time.Minute).UnixMicro(),
			},
			values: []float64{1.5, 2.5, 3.5, 4.5},
		},
	}

	// Create encoder
	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)

	// Encode all metrics
	for _, m := range metrics {
		err = encoder.StartMetricID(m.id, len(m.timestamps))
		require.NoError(t, err)

		for i := range m.timestamps {
			err = encoder.AddDataPoint(m.timestamps[i], m.values[i], "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify each metric can be retrieved independently
	for _, m := range metrics {
		var gotTimestamps []int64
		var gotValues []float64

		for _, dp := range blob.All(m.id) {
			gotTimestamps = append(gotTimestamps, dp.Ts)
			gotValues = append(gotValues, dp.Val)
		}

		require.Equal(t, m.timestamps, gotTimestamps, "metric: %s", m.name)
		require.Equal(t, m.values, gotValues, "metric: %s", m.name)
	}
}

// ==============================================================================
// NumericBlob Tests - ValueAt/TimestampAt Methods
// ==============================================================================

func TestNumericBlob_ValueAt(t *testing.T) {
	blobkTs := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Create test data
	timestamps := []int64{
		blobkTs.UnixMicro(),
		blobkTs.Add(1 * time.Minute).UnixMicro(),
		blobkTs.Add(2 * time.Minute).UnixMicro(),
		blobkTs.Add(3 * time.Minute).UnixMicro(),
		blobkTs.Add(4 * time.Minute).UnixMicro(),
	}
	values := []float64{10.5, 20.3, 30.7, 40.2, 50.9}

	blob := createBlobWithTimestamp(t, blobkTs, metricName, timestamps, values)

	t.Run("ValidIndices", func(t *testing.T) {
		for i, expectedValue := range values {
			value, ok := blob.ValueAt(metricID, i)
			require.True(t, ok, "Index %d should be valid", i)
			require.Equal(t, expectedValue, value, "Value at index %d", i)
		}
	})

	t.Run("FirstIndex", func(t *testing.T) {
		value, ok := blob.ValueAt(metricID, 0)
		require.True(t, ok)
		require.Equal(t, 10.5, value)
	})

	t.Run("LastIndex", func(t *testing.T) {
		value, ok := blob.ValueAt(metricID, 4)
		require.True(t, ok)
		require.Equal(t, 50.9, value)
	})

	t.Run("NegativeIndex", func(t *testing.T) {
		value, ok := blob.ValueAt(metricID, -1)
		require.False(t, ok)
		require.Equal(t, float64(0), value)
	})

	t.Run("OutOfBoundsIndex", func(t *testing.T) {
		value, ok := blob.ValueAt(metricID, 5)
		require.False(t, ok)
		require.Equal(t, float64(0), value)

		value, ok = blob.ValueAt(metricID, 100)
		require.False(t, ok)
		require.Equal(t, float64(0), value)
	})

	t.Run("NonExistentMetric", func(t *testing.T) {
		nonExistentID := uint64(99999999)
		value, ok := blob.ValueAt(nonExistentID, 0)
		require.False(t, ok)
		require.Equal(t, float64(0), value)
	})
}

func TestNumericBlob_TimestampAt(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Create test data
	timestamps := []int64{
		startTime.UnixMicro(),
		startTime.Add(1 * time.Minute).UnixMicro(),
		startTime.Add(2 * time.Minute).UnixMicro(),
		startTime.Add(3 * time.Minute).UnixMicro(),
		startTime.Add(4 * time.Minute).UnixMicro(),
	}
	values := []float64{10.5, 20.3, 30.7, 40.2, 50.9}

	blob := createBlobWithTimestamp(t, startTime, metricName, timestamps, values)

	t.Run("ValidIndices", func(t *testing.T) {
		for i, expectedTS := range timestamps {
			ts, ok := blob.TimestampAt(metricID, i)
			require.True(t, ok, "Index %d should be valid", i)
			require.Equal(t, expectedTS, ts, "Timestamp at index %d", i)
		}
	})

	t.Run("FirstIndex", func(t *testing.T) {
		ts, ok := blob.TimestampAt(metricID, 0)
		require.True(t, ok)
		require.Equal(t, timestamps[0], ts)
	})

	t.Run("LastIndex", func(t *testing.T) {
		ts, ok := blob.TimestampAt(metricID, 4)
		require.True(t, ok)
		require.Equal(t, timestamps[4], ts)
	})

	t.Run("NegativeIndex", func(t *testing.T) {
		ts, ok := blob.TimestampAt(metricID, -1)
		require.False(t, ok)
		require.Equal(t, int64(0), ts)
	})

	t.Run("OutOfBoundsIndex", func(t *testing.T) {
		ts, ok := blob.TimestampAt(metricID, 5)
		require.False(t, ok)
		require.Equal(t, int64(0), ts)

		ts, ok = blob.TimestampAt(metricID, 100)
		require.False(t, ok)
		require.Equal(t, int64(0), ts)
	})

	t.Run("NonExistentMetric", func(t *testing.T) {
		nonExistentID := uint64(99999999)
		ts, ok := blob.TimestampAt(nonExistentID, 0)
		require.False(t, ok)
		require.Equal(t, int64(0), ts)
	})
}

// ==============================================================================
// NumericBlob Tests - Tag Support
// ==============================================================================

func TestNumericBlob_TagSupport(t *testing.T) {
	startTime := time.Now().Truncate(time.Second)

	t.Run("AllWithTags", func(t *testing.T) {
		// Create encoder with tags
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		metricID := hash.ID("test.metric")
		timestamps := []int64{1000, 2000, 3000, 4000, 5000}
		values := []float64{10.0, 20.0, 30.0, 40.0, 50.0}
		tags := []string{"tag1", "tag2", "tag3", "tag4", "tag5"}

		// Start encoding this metric
		err = encoder.StartMetricID(metricID, len(timestamps))
		require.NoError(t, err)

		// Add data points with tags
		for i := range timestamps {
			err = encoder.AddDataPoint(timestamps[i], values[i], tags[i])
			require.NoError(t, err)
		}

		// End metric encoding
		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		// Decode
		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Verify All() returns correct data points with tags
		var gotDataPoints []NumericDataPoint
		for _, dp := range blob.All(metricID) {
			gotDataPoints = append(gotDataPoints, dp)
		}

		require.Len(t, gotDataPoints, 5)
		for i := range gotDataPoints {
			require.Equal(t, timestamps[i], gotDataPoints[i].Ts, "timestamp mismatch at index %d", i)
			require.Equal(t, values[i], gotDataPoints[i].Val, "value mismatch at index %d", i)
			require.Equal(t, tags[i], gotDataPoints[i].Tag, "tag mismatch at index %d", i)
		}
	})

	t.Run("AllTagsIterator", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		metricID := hash.ID("test.metric")
		timestamps := []int64{1000, 2000, 3000}
		values := []float64{10.0, 20.0, 30.0}
		tags := []string{"alpha", "beta", "gamma"}

		err = encoder.StartMetricID(metricID, len(timestamps))
		require.NoError(t, err)

		for i := range timestamps {
			err = encoder.AddDataPoint(timestamps[i], values[i], tags[i])
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Test AllTags() iterator
		var gotTags []string
		for tag := range blob.AllTags(metricID) {
			gotTags = append(gotTags, tag)
		}

		require.Equal(t, tags, gotTags)
	})

	t.Run("TagAtMethod", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		metricID := hash.ID("test.metric")
		timestamps := []int64{1000, 2000, 3000, 4000}
		values := []float64{10.0, 20.0, 30.0, 40.0}
		tags := []string{"one", "two", "three", "four"}

		err = encoder.StartMetricID(metricID, len(timestamps))
		require.NoError(t, err)

		for i := range timestamps {
			err = encoder.AddDataPoint(timestamps[i], values[i], tags[i])
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Test TagAt() for valid indices
		for i := range tags {
			tag, ok := blob.TagAt(metricID, i)
			require.True(t, ok, "TagAt should succeed for index %d", i)
			require.Equal(t, tags[i], tag, "tag mismatch at index %d", i)
		}

		// Test TagAt() for invalid indices
		tag, ok := blob.TagAt(metricID, -1)
		require.False(t, ok)
		require.Empty(t, tag)

		tag, ok = blob.TagAt(metricID, 100)
		require.False(t, ok)
		require.Empty(t, tag)

		// Test TagAt() for non-existent metric
		tag, ok = blob.TagAt(99999, 0)
		require.False(t, ok)
		require.Empty(t, tag)
	})

	t.Run("EmptyTags", func(t *testing.T) {
		// Test with empty tags - these should be optimized away
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		metricID := hash.ID("test.metric")
		timestamps := []int64{1000, 2000, 3000}
		values := []float64{10.0, 20.0, 30.0}

		err = encoder.StartMetricID(metricID, len(timestamps))
		require.NoError(t, err)

		// Add data points with empty tags
		for i := range timestamps {
			err = encoder.AddDataPoint(timestamps[i], values[i], "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		// OPTIMIZATION: When all tags are empty, tag support is automatically disabled
		// This saves space and improves decoding performance
		require.False(t, blob.flag.HasTag(), "Expected tags to be optimized away when all empty")

		// Verify all tags are empty strings in data points
		for _, dp := range blob.All(metricID) {
			require.Empty(t, dp.Tag)
		}

		// AllTags should return empty iterator when tags are optimized away
		// This is consistent with tags being disabled
		var gotTags []string
		for tag := range blob.AllTags(metricID) {
			gotTags = append(gotTags, tag)
		}
		require.Len(t, gotTags, 0, "Expected no tags when optimized away")
	})

	t.Run("MixedTags", func(t *testing.T) {
		// Test with mixed empty and non-empty tags
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		metricID := hash.ID("test.metric")
		timestamps := []int64{1000, 2000, 3000, 4000}
		values := []float64{10.0, 20.0, 30.0, 40.0}
		tags := []string{"tag1", "", "tag3", ""}

		err = encoder.StartMetricID(metricID, len(timestamps))
		require.NoError(t, err)

		for i := range timestamps {
			err = encoder.AddDataPoint(timestamps[i], values[i], tags[i])
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Verify mixed tags
		var gotDataPoints []NumericDataPoint
		for _, dp := range blob.All(metricID) {
			gotDataPoints = append(gotDataPoints, dp)
		}

		require.Len(t, gotDataPoints, 4)
		for i := range gotDataPoints {
			require.Equal(t, tags[i], gotDataPoints[i].Tag, "tag mismatch at index %d", i)
		}
	})
}

// ==============================================================================
// NumericBlob Tests - ByName Methods
// ==============================================================================

// TestNumericBlobByNameMethods tests all ByName methods when metric names are available
func TestNumericBlobByNameMethods(t *testing.T) {
	encoder, err := NewNumericEncoder(time.Now(), WithTagsEnabled(true))
	require.NoError(t, err)

	// Enable metric names payload
	encoder.header.Flag.SetHasMetricNames(true)

	// Add metrics with known data
	metrics := []struct {
		name   string
		values []float64
		tags   []string
	}{
		{
			name:   "cpu.usage",
			values: []float64{10.5, 20.5, 30.5},
			tags:   []string{"tag1", "tag2", "tag3"},
		},
		{
			name:   "memory.total",
			values: []float64{100.0, 200.0, 300.0},
			tags:   []string{"mem1", "mem2", "mem3"},
		},
		{
			name:   "disk.io",
			values: []float64{1.1, 2.2, 3.3},
			tags:   []string{"disk1", "disk2", "disk3"},
		},
	}

	baseTs := time.Now().UnixMicro()

	for _, m := range metrics {
		err = encoder.StartMetricName(m.name, len(m.values))
		require.NoError(t, err)

		for i, value := range m.values {
			ts := baseTs + int64(i*1000)
			err = encoder.AddDataPoint(ts, value, m.tags[i])
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	// Encode
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test AllByName for each metric
	for _, m := range metrics {
		t.Run("AllByName_"+m.name, func(t *testing.T) {
			idx := 0
			for i, dp := range blob.AllByName(m.name) {
				require.Equal(t, idx, i)
				require.Equal(t, baseTs+int64(idx*1000), dp.Ts)
				require.Equal(t, m.values[idx], dp.Val)
				require.Equal(t, m.tags[idx], dp.Tag)
				idx++
			}
			require.Equal(t, len(m.values), idx)
		})
	}

	// Test AllTimestampsByName
	for _, m := range metrics {
		t.Run("AllTimestampsByName_"+m.name, func(t *testing.T) {
			idx := 0
			for ts := range blob.AllTimestampsByName(m.name) {
				require.Equal(t, baseTs+int64(idx*1000), ts)
				idx++
			}
			require.Equal(t, len(m.values), idx)
		})
	}

	// Test AllValuesByName
	for _, m := range metrics {
		t.Run("AllValuesByName_"+m.name, func(t *testing.T) {
			idx := 0
			for val := range blob.AllValuesByName(m.name) {
				require.Equal(t, m.values[idx], val)
				idx++
			}
			require.Equal(t, len(m.values), idx)
		})
	}

	// Test AllTagsByName
	for _, m := range metrics {
		t.Run("AllTagsByName_"+m.name, func(t *testing.T) {
			idx := 0
			for tag := range blob.AllTagsByName(m.name) {
				require.Equal(t, m.tags[idx], tag)
				idx++
			}
			require.Equal(t, len(m.values), idx)
		})
	}

	// Test LenByName
	for _, m := range metrics {
		t.Run("LenByName_"+m.name, func(t *testing.T) {
			length := blob.LenByName(m.name)
			require.Equal(t, len(m.values), length)
		})
	}
}

// TestNumericBlobByNameMethodsNotAvailable tests ByName methods when metric names are NOT available
// With the fallback mechanism, ByName methods will hash the metric name and use ID-based lookup
func TestNumericBlobByNameMethodsNotAvailable(t *testing.T) {
	encoder, err := NewNumericEncoder(time.Now())
	require.NoError(t, err)

	// Do NOT enable metric names payload
	baseTs := time.Now().UnixMicro()

	// Add metrics
	err = encoder.StartMetricName("cpu.usage", 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(baseTs, 10.0, "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(baseTs+1000, 20.0, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Encode
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// With fallback: ByName methods hash the name and use ID-based lookup
	// So they should return data, not empty iterators!
	t.Run("AllByName_uses_fallback", func(t *testing.T) {
		count := 0
		expectedValues := []float64{10.0, 20.0}
		for i, dp := range blob.AllByName("cpu.usage") {
			require.Equal(t, count, i)
			require.Equal(t, baseTs+int64(count*1000), dp.Ts)
			require.Equal(t, expectedValues[count], dp.Val)
			count++
		}
		require.Equal(t, 2, count)
	})

	t.Run("AllTimestampsByName_uses_fallback", func(t *testing.T) {
		count := 0
		for ts := range blob.AllTimestampsByName("cpu.usage") {
			require.Equal(t, baseTs+int64(count*1000), ts)
			count++
		}
		require.Equal(t, 2, count)
	})

	t.Run("AllValuesByName_uses_fallback", func(t *testing.T) {
		count := 0
		expectedValues := []float64{10.0, 20.0}
		for val := range blob.AllValuesByName("cpu.usage") {
			require.Equal(t, expectedValues[count], val)
			count++
		}
		require.Equal(t, 2, count)
	})

	t.Run("AllTagsByName_uses_fallback", func(t *testing.T) {
		// Tags not enabled, should return empty
		count := 0
		for range blob.AllTagsByName("cpu.usage") {
			count++
		}
		require.Equal(t, 0, count)
	})

	t.Run("LenByName_uses_fallback", func(t *testing.T) {
		length := blob.LenByName("cpu.usage")
		require.Equal(t, 2, length)
	})
}

// TestNumericBlobByNameNonExistentMetric tests ByName methods with non-existent metric name
func TestNumericBlobByNameNonExistentMetric(t *testing.T) {
	encoder, err := NewNumericEncoder(time.Now())
	require.NoError(t, err)

	// Enable metric names payload
	encoder.header.Flag.SetHasMetricNames(true)

	// Add one metric
	baseTs := time.Now().UnixMicro()
	err = encoder.StartMetricName("cpu.usage", 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(baseTs, 10.0, "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(baseTs+1000, 20.0, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Encode
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// All ByName methods should return empty iterators for non-existent metric
	nonExistentMetric := "does.not.exist"

	t.Run("AllByName_nonexistent", func(t *testing.T) {
		count := 0
		for range blob.AllByName(nonExistentMetric) {
			count++
		}
		require.Equal(t, 0, count)
	})

	t.Run("AllTimestampsByName_nonexistent", func(t *testing.T) {
		count := 0
		for range blob.AllTimestampsByName(nonExistentMetric) {
			count++
		}
		require.Equal(t, 0, count)
	})

	t.Run("AllValuesByName_nonexistent", func(t *testing.T) {
		count := 0
		for range blob.AllValuesByName(nonExistentMetric) {
			count++
		}
		require.Equal(t, 0, count)
	})

	t.Run("AllTagsByName_nonexistent", func(t *testing.T) {
		count := 0
		for range blob.AllTagsByName(nonExistentMetric) {
			count++
		}
		require.Equal(t, 0, count)
	})

	t.Run("LenByName_nonexistent", func(t *testing.T) {
		length := blob.LenByName(nonExistentMetric)
		require.Equal(t, 0, length)
	})
}

// TestNumericBlobCollisionHandling tests that ByName methods work correctly with hash collisions
func TestNumericBlobCollisionHandling(t *testing.T) {
	encoder, err := NewNumericEncoder(time.Now(), WithTagsEnabled(true))
	require.NoError(t, err)

	// Enable metric names payload (simulating collision detected)
	encoder.header.Flag.SetHasMetricNames(true)

	// Add two metrics - simulate they have same hash by using same ID
	// In real scenario, collision tracker would detect this and set flag
	metric1 := "metric.one"
	metric2 := "metric.two"

	// Calculate their actual hashes
	hash1 := hash.ID(metric1)
	hash2 := hash.ID(metric2)

	baseTs := time.Now().UnixMicro()

	// Add first metric
	err = encoder.StartMetricName(metric1, 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(baseTs, 10.0, "tag1")
	require.NoError(t, err)
	err = encoder.AddDataPoint(baseTs+1000, 20.0, "tag2")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Add second metric
	err = encoder.StartMetricName(metric2, 3)
	require.NoError(t, err)
	err = encoder.AddDataPoint(baseTs, 100.0, "tagA")
	require.NoError(t, err)
	err = encoder.AddDataPoint(baseTs+1000, 200.0, "tagB")
	require.NoError(t, err)
	err = encoder.AddDataPoint(baseTs+2000, 300.0, "tagC")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Encode
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify both metrics exist in indexEntryMap (by hash)
	_, ok1 := blob.index.byID[hash1]
	require.True(t, ok1)
	_, ok2 := blob.index.byID[hash2]
	require.True(t, ok2)

	// Test that ByName methods return correct data for each metric
	t.Run("metric.one_by_name", func(t *testing.T) {
		count := 0
		expectedValues := []float64{10.0, 20.0}
		expectedTags := []string{"tag1", "tag2"}

		for i, dp := range blob.AllByName(metric1) {
			require.Equal(t, count, i)
			require.Equal(t, baseTs+int64(count*1000), dp.Ts)
			require.Equal(t, expectedValues[count], dp.Val)
			require.Equal(t, expectedTags[count], dp.Tag)
			count++
		}
		require.Equal(t, 2, count)
		require.Equal(t, 2, blob.LenByName(metric1))
	})

	t.Run("metric.two_by_name", func(t *testing.T) {
		count := 0
		expectedValues := []float64{100.0, 200.0, 300.0}
		expectedTags := []string{"tagA", "tagB", "tagC"}

		for i, dp := range blob.AllByName(metric2) {
			require.Equal(t, count, i)
			require.Equal(t, baseTs+int64(count*1000), dp.Ts)
			require.Equal(t, expectedValues[count], dp.Val)
			require.Equal(t, expectedTags[count], dp.Tag)
			count++
		}
		require.Equal(t, 3, count)
		require.Equal(t, 3, blob.LenByName(metric2))
	})

	// Verify that both metrics can also be accessed by hash (ID)
	t.Run("metric.one_by_id", func(t *testing.T) {
		count := 0
		expectedValues := []float64{10.0, 20.0}

		for val := range blob.AllValues(hash1) {
			require.Equal(t, expectedValues[count], val)
			count++
		}
		require.Equal(t, 2, count)
		require.Equal(t, 2, blob.Len(hash1))
	})

	t.Run("metric.two_by_id", func(t *testing.T) {
		count := 0
		expectedValues := []float64{100.0, 200.0, 300.0}

		for val := range blob.AllValues(hash2) {
			require.Equal(t, expectedValues[count], val)
			count++
		}
		require.Equal(t, 3, count)
		require.Equal(t, 3, blob.Len(hash2))
	})
}

// TestNumericBlobByNameWithoutTags tests ByName methods when tags are disabled
func TestNumericBlobByNameWithoutTags(t *testing.T) {
	encoder, err := NewNumericEncoder(time.Now())
	require.NoError(t, err)

	// Enable metric names payload but not tags
	encoder.header.Flag.SetHasMetricNames(true)

	baseTs := time.Now().UnixMicro()

	// Add metrics without tags
	err = encoder.StartMetricName("cpu.usage", 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(baseTs, 10.0, "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(baseTs+1000, 20.0, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Encode
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// AllTagsByName should return empty iterator (tags disabled)
	count := 0
	for range blob.AllTagsByName("cpu.usage") {
		count++
	}
	require.Equal(t, 0, count)

	// But other methods should work
	t.Run("AllByName_works", func(t *testing.T) {
		idx := 0
		for i, dp := range blob.AllByName("cpu.usage") {
			require.Equal(t, idx, i)
			require.Equal(t, "", dp.Tag) // Empty tag
			idx++
		}
		require.Equal(t, 2, idx)
	})
}

// TestNumericBlob_MetricCount tests the MetricCount method
func TestNumericBlob_MetricCount(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("WithMetricIDs", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Add 3 metrics by ID
		require.NoError(t, encoder.StartMetricID(100, 2))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 2.0, ""))
		require.NoError(t, encoder.EndMetric())

		require.NoError(t, encoder.StartMetricID(200, 1))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 3.0, ""))
		require.NoError(t, encoder.EndMetric())

		require.NoError(t, encoder.StartMetricID(300, 1))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 4.0, ""))
		require.NoError(t, encoder.EndMetric())

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)
		blob, err := decoder.Decode()
		require.NoError(t, err)

		require.Equal(t, 3, blob.MetricCount())
	})

	t.Run("WithMetricNames", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Enable metric names payload (simulating collision detection)
		encoder.header.Flag.SetHasMetricNames(true)

		// Add 2 metrics by name
		require.NoError(t, encoder.StartMetricName("cpu.usage", 1))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 50.0, ""))
		require.NoError(t, encoder.EndMetric())

		require.NoError(t, encoder.StartMetricName("memory.usage", 1))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 80.0, ""))
		require.NoError(t, encoder.EndMetric())

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)
		blob, err := decoder.Decode()
		require.NoError(t, err)

		require.Equal(t, 2, blob.MetricCount())
	})
}

// TestNumericBlob_HasMetricID tests the HasMetricID method
func TestNumericBlob_HasMetricID(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)

	// Add metrics with IDs 100, 200, 300
	require.NoError(t, encoder.StartMetricID(100, 1))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
	require.NoError(t, encoder.EndMetric())

	require.NoError(t, encoder.StartMetricID(200, 1))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 2.0, ""))
	require.NoError(t, encoder.EndMetric())

	require.NoError(t, encoder.StartMetricID(300, 1))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 3.0, ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test existing metric IDs
	require.True(t, blob.HasMetricID(100))
	require.True(t, blob.HasMetricID(200))
	require.True(t, blob.HasMetricID(300))

	// Test non-existent metric IDs
	require.False(t, blob.HasMetricID(999))
	require.False(t, blob.HasMetricID(0))
	require.False(t, blob.HasMetricID(150))
}

// TestNumericBlob_HasMetricName tests the HasMetricName method
func TestNumericBlob_HasMetricName(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("WithMetricNames", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Enable metric names payload (simulating collision detection)
		encoder.header.Flag.SetHasMetricNames(true)

		// Add metrics with names
		require.NoError(t, encoder.StartMetricName("cpu.usage", 1))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 50.0, ""))
		require.NoError(t, encoder.EndMetric())

		require.NoError(t, encoder.StartMetricName("memory.usage", 1))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 80.0, ""))
		require.NoError(t, encoder.EndMetric())

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)
		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Test existing metric names
		require.True(t, blob.HasMetricName("cpu.usage"))
		require.True(t, blob.HasMetricName("memory.usage"))

		// Test non-existent metric name
		require.False(t, blob.HasMetricName("disk.io"))
		require.False(t, blob.HasMetricName(""))
	})

	t.Run("WithoutMetricNames", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Add metrics by ID only (no metric names map)
		require.NoError(t, encoder.StartMetricID(100, 1))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
		require.NoError(t, encoder.EndMetric())

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)
		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Should return false for any name when metricNameMap is nil
		require.False(t, blob.HasMetricName("any.metric"))
		require.False(t, blob.HasMetricName("cpu.usage"))
	})
}

// TestNumericBlob_MetricIDs tests the MetricIDs method
func TestNumericBlob_MetricIDs(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("MultipleMetrics", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		expectedIDs := []uint64{100, 200, 300}

		for _, id := range expectedIDs {
			require.NoError(t, encoder.StartMetricID(id, 1))
			require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
			require.NoError(t, encoder.EndMetric())
		}

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)
		blob, err := decoder.Decode()
		require.NoError(t, err)

		ids := blob.MetricIDs()
		require.Len(t, ids, 3)

		// Check that all expected IDs are in the result (order may vary)
		for _, expectedID := range expectedIDs {
			require.Contains(t, ids, expectedID)
		}
	})

	t.Run("SingleMetric", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		require.NoError(t, encoder.StartMetricID(999, 1))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 123.45, ""))
		require.NoError(t, encoder.EndMetric())

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)
		blob, err := decoder.Decode()
		require.NoError(t, err)

		ids := blob.MetricIDs()
		require.Len(t, ids, 1)
		require.Contains(t, ids, uint64(999))
	})
}

// TestNumericBlob_MetricNames tests the MetricNames method
func TestNumericBlob_MetricNames(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("WithMetricNames", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Enable metric names payload (simulating collision detection)
		encoder.header.Flag.SetHasMetricNames(true)

		expectedNames := []string{"cpu.usage", "memory.usage", "disk.io"}

		for _, name := range expectedNames {
			require.NoError(t, encoder.StartMetricName(name, 1))
			require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
			require.NoError(t, encoder.EndMetric())
		}

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)
		blob, err := decoder.Decode()
		require.NoError(t, err)

		names := blob.MetricNames()
		require.Len(t, names, 3)

		// Check that all expected names are in the result (order may vary)
		for _, expectedName := range expectedNames {
			require.Contains(t, names, expectedName)
		}
	})

	t.Run("WithoutMetricNames", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Add metrics by ID only (no metric names map)
		require.NoError(t, encoder.StartMetricID(100, 1))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 42.0, ""))
		require.NoError(t, encoder.EndMetric())

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)
		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Should return empty slice when metricNameMap is nil
		names := blob.MetricNames()
		require.Empty(t, names)
	})
}

// TestNumericBlobOffsetWithBenchmarkData replicates the exact benchmark scenario
// to understand why metric #98, index 56 fails with 200 metrics but works with 98 metrics.
func TestNumericBlobOffsetWithBenchmarkData(t *testing.T) {
	tests := []struct {
		name       string
		numMetrics int
		expectFail bool
	}{
		{
			name:       "98 metrics (user says this works)",
			numMetrics: 98,
			expectFail: false, // With uint16 offsets, 98 metrics should work
		},
		{
			name:       "99 metrics (user says this fails - FIXED!)",
			numMetrics: 99,
			expectFail: false,
		},
		{
			name:       "200 metrics (benchmark default - FIXED!)",
			numMetrics: 200,
			expectFail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Generate test data EXACTLY like the benchmark does
			numPoints := 100
			startTime := time.Unix(1700000000, 0)
			baseValue := 100.0
			deltaPercent := 0.02
			jitterPercent := 0.05
			baseInterval := time.Second

			encoder, err := NewNumericEncoder(
				startTime,
				WithTimestampEncoding(format.TypeRaw),
				WithValueEncoding(format.TypeGorilla),
			)
			require.NoError(t, err)

			// Fixed seed for reproducibility (same as benchmark)
			rng := rand.New(rand.NewSource(42))

			// Track value offsets for debugging
			valueOffsets := make([]int, tt.numMetrics)
			cumulativeOffset := 0

			for m := 0; m < tt.numMetrics; m++ {
				metricID := uint64(1000 + m)
				err := encoder.StartMetricID(metricID, numPoints)
				require.NoError(t, err)

				currentTime := startTime
				currentValue := baseValue + float64(m)*10.0

				for i := 0; i < numPoints; i++ {
					jitterRange := float64(baseInterval) * jitterPercent
					jitter := time.Duration((rng.Float64()*2 - 1) * jitterRange)
					currentTime = currentTime.Add(baseInterval + jitter)

					deltaRange := currentValue * deltaPercent
					delta := (rng.Float64()*2 - 1) * deltaRange
					currentValue += delta

					err := encoder.AddDataPoint(currentTime.UnixMicro(), currentValue, "")
					require.NoError(t, err)
				}

				err = encoder.EndMetric()
				require.NoError(t, err)

				// Estimate value bytes for this metric (Gorilla compressed)
				// For jittered data with 2% delta, Gorilla typically achieves 1.5-2 bytes per value
				estimatedBytes := numPoints * 2
				valueOffsets[m] = cumulativeOffset
				cumulativeOffset += estimatedBytes
			}

			data, err := encoder.Finish()
			require.NoError(t, err)

			t.Logf("Encoded %d metrics: blob size=%d bytes", tt.numMetrics, len(data))
			t.Logf("Estimated cumulative value offset for metric #98: %d bytes (uint16 max: 65535)",
				valueOffsets[min(97, tt.numMetrics-1)])
			if tt.numMetrics >= 99 {
				t.Logf("Estimated cumulative value offset for metric #99: %d bytes", valueOffsets[98])
			}

			// Decode
			decoder, err := NewNumericDecoder(data)
			require.NoError(t, err)
			blob, err := decoder.Decode()
			require.NoError(t, err)

			// Test accessing metric #98 at index 56 (the failing case from benchmark)
			// Note: With N metrics, we have metric IDs 1000 to 1000+N-1
			// So for 98 metrics, we have 1000-1097 (metric #0 to #97)
			// For 99+ metrics, we have metric #98 (ID 1098)

			// Test the last metric AND metric #98 if it exists
			lastMetricID := uint64(1000 + tt.numMetrics - 1)
			testIndex := 56

			// Test last metric (should always work if no overflow)
			val, ok := blob.ValueAt(lastMetricID, testIndex)
			if !ok {
				t.Errorf("Failed to access LAST metric #%d (ID %d) at index %d",
					tt.numMetrics-1, lastMetricID, testIndex)
			} else {
				t.Logf("✓ Successfully accessed LAST metric #%d (ID %d): val=%v",
					tt.numMetrics-1, lastMetricID, val)
			}

			// Debug: Check all metrics to find where it starts failing
			t.Logf("Testing all %d metrics to find failure boundary...", tt.numMetrics)
			firstFailMetricNum := -1
			for m := 0; m < tt.numMetrics; m++ {
				mid := uint64(1000 + m)
				_, ok := blob.ValueAt(mid, testIndex)
				if !ok {
					firstFailMetricNum = m
					t.Logf("❌ First failing metric: #%d (ID %d), estimated offset: %d bytes",
						m, mid, valueOffsets[m])

					break
				}
			}
			if firstFailMetricNum == -1 {
				t.Logf("✓ All %d metrics accessible!", tt.numMetrics)
			}

			// If we have 99+ metrics, specifically test metric #98 (the previously problematic one)
			if tt.numMetrics > 98 {
				testMetricID := uint64(1098) // metric #98
				val, ok := blob.ValueAt(testMetricID, testIndex)

				if !ok {
					t.Errorf("Failed to access metric #98 (ID %d)!", testMetricID)
				} else {
					t.Logf("✓ Successfully accessed metric #98: val=%v", val)
				}
			}
		})
	}
}

// TestNumericBlobOffsetDebug provides detailed debugging of the offset calculation issue.
func TestNumericBlobOffsetDebug(t *testing.T) {
	t.Run("Find exact failure point", func(t *testing.T) {
		numPoints := 100
		startTime := time.Unix(1700000000, 0)
		baseValue := 100.0
		deltaPercent := 0.02
		jitterPercent := 0.05
		baseInterval := time.Second

		encoder, err := NewNumericEncoder(
			startTime,
			WithTimestampEncoding(format.TypeRaw),
			WithValueEncoding(format.TypeGorilla),
		)
		require.NoError(t, err)

		rng := rand.New(rand.NewSource(42))

		// Try different numbers of metrics to find the boundary
		maxMetrics := 110
		for m := 0; m < maxMetrics; m++ {
			metricID := uint64(1000 + m)
			err := encoder.StartMetricID(metricID, numPoints)
			require.NoError(t, err)

			currentTime := startTime
			currentValue := baseValue + float64(m)*10.0

			for i := 0; i < numPoints; i++ {
				jitterRange := float64(baseInterval) * jitterPercent
				jitter := time.Duration((rng.Float64()*2 - 1) * jitterRange)
				currentTime = currentTime.Add(baseInterval + jitter)

				deltaRange := currentValue * deltaPercent
				delta := (rng.Float64()*2 - 1) * deltaRange
				currentValue += delta

				err := encoder.AddDataPoint(currentTime.UnixMicro(), currentValue, "")
				require.NoError(t, err)
			}

			err = encoder.EndMetric()
			require.NoError(t, err)
		}

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)
		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Find first failing metric
		testIndex := 56
		firstFail := -1
		for m := 0; m < maxMetrics; m++ {
			metricID := uint64(1000 + m)
			_, ok := blob.ValueAt(metricID, testIndex)
			if !ok {
				firstFail = m
				break
			}
		}

		if firstFail >= 0 {
			t.Logf("✓ Found boundary: Random access FAILS starting at metric #%d", firstFail)
			t.Logf("  This means 0-%d work, %d+ fail", firstFail-1, firstFail)
		} else {
			t.Logf("All %d metrics work with random access", maxMetrics)
		}
	})
}

// TestNumericBlobRawVsGorillaOffset compares Raw vs Gorilla encoding offset behavior.
func TestNumericBlobRawVsGorillaOffset(t *testing.T) {
	numMetrics := 100
	numPoints := 100
	startTime := time.Unix(1700000000, 0)

	for _, encoding := range []format.EncodingType{format.TypeRaw, format.TypeGorilla} {
		t.Run(fmt.Sprintf("%s encoding", encoding), func(t *testing.T) {
			encoder, err := NewNumericEncoder(
				startTime,
				WithTimestampEncoding(format.TypeRaw),
				WithValueEncoding(encoding),
			)
			require.NoError(t, err)

			rng := rand.New(rand.NewSource(42))

			for m := 0; m < numMetrics; m++ {
				metricID := uint64(1000 + m)
				err := encoder.StartMetricID(metricID, numPoints)
				require.NoError(t, err)

				currentTime := startTime
				currentValue := 100.0 + float64(m)*10.0

				for i := 0; i < numPoints; i++ {
					jitterRange := float64(time.Second) * 0.05
					jitter := time.Duration((rng.Float64()*2 - 1) * jitterRange)
					currentTime = currentTime.Add(time.Second + jitter)

					deltaRange := currentValue * 0.02
					delta := (rng.Float64()*2 - 1) * deltaRange
					currentValue += delta

					err := encoder.AddDataPoint(currentTime.UnixMicro(), currentValue, "")
					require.NoError(t, err)
				}

				err = encoder.EndMetric()
				require.NoError(t, err)
			}

			data, err := encoder.Finish()
			require.NoError(t, err)

			decoder, err := NewNumericDecoder(data)
			require.NoError(t, err)
			blob, err := decoder.Decode()
			require.NoError(t, err)

			// Test random access
			testIndex := 56
			failures := 0
			for m := 0; m < numMetrics; m++ {
				metricID := uint64(1000 + m)
				_, ok := blob.ValueAt(metricID, testIndex)
				if !ok {
					failures++
				}
			}

			t.Logf("%s: blob size=%d bytes, random access failures=%d/%d metrics",
				encoding, len(data), failures, numMetrics)
		})
	}
}

// TestNumericBlobOffsetLimit tests the uint16 offset limitation for different encodings.
//
// Background:
// - NumericIndexEntry stores ValueOffset as uint16 (max 65535 bytes)
// - ValueOffset is the delta offset from the previous metric's value data
// - When cumulative value data exceeds 65535 bytes, overflow occurs
//
// Why Gorilla compression makes this MORE likely to hit the limit:
// - Raw encoding: 100 points × 8 bytes = 800 bytes per metric → 65535/800 ≈ 81 metrics max
// - Gorilla encoding: ~100-200 bytes per metric → 65535/150 ≈ 436 metrics max
//
// Counter-intuitively, Gorilla's better compression means we can fit MORE metrics
// before hitting the uint16 limit. However, the RANDOM ACCESS pattern (At() method)
// requires calculating absolute offsets by summing deltas, and if the total exceeds
// uint16 range during intermediate calculations, it causes issues.
func TestNumericBlobOffsetLimit(t *testing.T) {
	tests := []struct {
		name          string
		numMetrics    int
		numPoints     int
		valEncoding   format.EncodingType
		expectFailure bool
		failureReason string
	}{
		{
			name:          "Raw encoding - 98 metrics × 100 points (within limit)",
			numMetrics:    98,
			numPoints:     100,
			valEncoding:   format.TypeRaw,
			expectFailure: false,
		},
		{
			name:          "Raw encoding - 99 metrics × 100 points (approaching limit)",
			numMetrics:    99,
			numPoints:     100,
			valEncoding:   format.TypeRaw,
			expectFailure: false, // Raw: 99 × 800 = 79,200 bytes - EXCEEDS uint16!
		},
		{
			name:          "Gorilla encoding - 98 metrics × 100 points (within limit)",
			numMetrics:    98,
			numPoints:     100,
			valEncoding:   format.TypeGorilla,
			expectFailure: false,
		},
		{
			name:          "Gorilla encoding - 99 metrics × 100 points (near limit)",
			numMetrics:    99,
			numPoints:     100,
			valEncoding:   format.TypeGorilla,
			expectFailure: false, // Gorilla: ~99 × 150 = ~14,850 bytes - should be OK, but fails
			failureReason: "offset calculation overflow in random access",
		},
		{
			name:          "Gorilla encoding - 200 metrics × 100 points",
			numMetrics:    200,
			numPoints:     100,
			valEncoding:   format.TypeGorilla,
			expectFailure: false,
			failureReason: "exceeds uint16 offset limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Generate test data
			startTime := time.Unix(1700000000, 0)
			encoder, err := NewNumericEncoder(
				startTime,
				WithTimestampEncoding(format.TypeRaw),
				WithValueEncoding(tt.valEncoding),
			)
			require.NoError(t, err)

			// Track actual bytes used
			totalValueBytes := 0

			for m := 0; m < tt.numMetrics; m++ {
				metricID := uint64(1000 + m)
				err := encoder.StartMetricID(metricID, tt.numPoints)
				require.NoError(t, err)

				// Generate simple incrementing values
				for i := 0; i < tt.numPoints; i++ {
					ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
					val := 100.0 + float64(m)*10.0 + float64(i)
					err := encoder.AddDataPoint(ts, val, "")
					require.NoError(t, err)
				}

				err = encoder.EndMetric()
				require.NoError(t, err)

				// Track bytes (approximation)
				if tt.valEncoding == format.TypeRaw {
					totalValueBytes += tt.numPoints * 8
				} else {
					// Gorilla varies, but typically ~1.5-2 bytes per point for incrementing data
					totalValueBytes += tt.numPoints * 2
				}
			}

			data, err := encoder.Finish()
			require.NoError(t, err)

			t.Logf("Encoded %d metrics × %d points with %s encoding: blob size = %d bytes",
				tt.numMetrics, tt.numPoints, tt.valEncoding, len(data))
			t.Logf("Estimated total value data: ~%d bytes (uint16 max: 65535)",
				totalValueBytes)

			// Decode and test random access
			decoder, err := NewNumericDecoder(data)
			require.NoError(t, err)
			blob, err := decoder.Decode()
			require.NoError(t, err)

			// Test accessing the LAST metric (metric #98 or #99) at a middle index
			lastMetricID := uint64(1000 + tt.numMetrics - 1)
			testIndex := 56 // This is the failing index from the benchmark

			val, ok := blob.ValueAt(lastMetricID, testIndex)

			if tt.expectFailure {
				if ok {
					t.Errorf("Expected failure (%s) but got successful read: val=%v",
						tt.failureReason, val)
				} else {
					t.Logf("✓ Expected failure occurred: %s", tt.failureReason)
				}
			} else {
				if !ok {
					t.Errorf("Expected success but got failure. This indicates offset overflow!")
					// Try to access earlier metrics to see where it breaks
					for m := tt.numMetrics - 1; m >= 0; m-- {
						metricID := uint64(1000 + m)
						_, ok := blob.ValueAt(metricID, testIndex)
						if ok {
							t.Logf("Last working metric: #%d", m)
							break
						}
					}
				} else {
					expectedVal := 100.0 + float64(tt.numMetrics-1)*10.0 + float64(testIndex)
					require.InDelta(t, expectedVal, val, 0.001, "Value mismatch")
					t.Logf("✓ Successfully accessed value: %v", val)
				}
			}
		})
	}
}

// TestNumericBlobOffsetLimitDetailed provides detailed analysis of the offset limit issue.
func TestNumericBlobOffsetLimitDetailed(t *testing.T) {
	t.Run("Analyze offset accumulation for different encodings", func(t *testing.T) {
		encodings := []format.EncodingType{
			format.TypeRaw,
			format.TypeGorilla,
		}

		for _, encoding := range encodings {
			t.Run(fmt.Sprintf("encoding=%s", encoding), func(t *testing.T) {
				startTime := time.Unix(1700000000, 0)
				encoder, err := NewNumericEncoder(
					startTime,
					WithTimestampEncoding(format.TypeRaw),
					WithValueEncoding(encoding),
				)
				require.NoError(t, err)

				// Encode metrics until we find the breaking point
				const numPoints = 100
				const maxMetrics = 120

				for m := 0; m < maxMetrics; m++ {
					metricID := uint64(1000 + m)
					err := encoder.StartMetricID(metricID, numPoints)
					require.NoError(t, err)

					for i := 0; i < numPoints; i++ {
						ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
						val := 100.0 + float64(m)*10.0 + float64(i)
						err := encoder.AddDataPoint(ts, val, "")
						require.NoError(t, err)
					}

					err = encoder.EndMetric()
					require.NoError(t, err)
				}

				data, err := encoder.Finish()
				require.NoError(t, err)

				// Decode
				decoder, err := NewNumericDecoder(data)
				require.NoError(t, err)
				blob, err := decoder.Decode()
				require.NoError(t, err)

				// Find where random access starts failing
				testIndex := 56
				firstFailMetric := -1
				for m := 0; m < maxMetrics; m++ {
					metricID := uint64(1000 + m)
					_, ok := blob.ValueAt(metricID, testIndex)
					if !ok {
						firstFailMetric = m
						break
					}
				}

				if firstFailMetric >= 0 {
					t.Logf("Encoding %s: Random access FAILS starting at metric #%d",
						encoding, firstFailMetric)
					t.Logf("Maximum safe metrics: %d (with %d points each)",
						firstFailMetric, numPoints)
				} else {
					t.Logf("Encoding %s: Random access works for all %d metrics",
						encoding, maxMetrics)
				}

				// Calculate theoretical limit
				if encoding == format.TypeRaw {
					bytesPerMetric := numPoints * 8
					theoreticalMax := 65535 / bytesPerMetric
					t.Logf("Theoretical max (raw): %d bytes/metric → %d metrics",
						bytesPerMetric, theoreticalMax)
				} else {
					// Gorilla varies based on data pattern
					t.Logf("Theoretical max (Gorilla): varies by data pattern, typically 300-400 metrics")
				}
			})
		}
	})
}

// TestNumericBlobOffsetOverflowScenarios tests specific overflow scenarios.
func TestNumericBlobOffsetOverflowScenarios(t *testing.T) {
	t.Run("Exact boundary - 82 metrics with raw encoding", func(t *testing.T) {
		// Raw: 82 metrics × 800 bytes = 65,600 bytes (just over uint16 max)
		testOffsetBoundary(t, 82, 100, format.TypeRaw, false)
	})

	t.Run("Safe boundary - 81 metrics with raw encoding", func(t *testing.T) {
		// Raw: 81 metrics × 800 bytes = 64,800 bytes (just under uint16 max)
		testOffsetBoundary(t, 81, 100, format.TypeRaw, false)
	})

	t.Run("Gorilla boundary - 99 metrics", func(t *testing.T) {
		// This is the user's failing case
		testOffsetBoundary(t, 99, 100, format.TypeGorilla, false)
	})

	t.Run("Gorilla safe - 98 metrics", func(t *testing.T) {
		// This works
		testOffsetBoundary(t, 98, 100, format.TypeGorilla, false)
	})
}

func testOffsetBoundary(t *testing.T, numMetrics, _ int, encoding format.EncodingType, _ bool) {
	t.Helper()

	const numPoints = 100       // Always 100 in all test cases
	const expectFailure = false // Reserved for future use

	startTime := time.Unix(1700000000, 0)
	encoder, err := NewNumericEncoder(
		startTime,
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(encoding),
	)
	require.NoError(t, err)

	for m := 0; m < numMetrics; m++ {
		metricID := uint64(1000 + m)
		err := encoder.StartMetricID(metricID, numPoints)
		require.NoError(t, err)

		for i := 0; i < numPoints; i++ {
			ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
			val := 100.0 + float64(m)*10.0 + float64(i)
			err := encoder.AddDataPoint(ts, val, "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Test last metric
	lastMetricID := uint64(1000 + numMetrics - 1)
	_, ok := blob.ValueAt(lastMetricID, 56)

	if expectFailure {
		require.False(t, ok, "Expected random access to fail due to offset overflow")
	} else {
		require.True(t, ok, "Expected random access to succeed")
	}
}
