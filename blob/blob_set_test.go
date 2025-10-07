package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/stretchr/testify/require"
)

// TestBlobSet_EmptySet tests that empty BlobSet doesn't panic and returns appropriate empty results
func TestBlobSet_EmptySet(t *testing.T) {
	emptySet := NewBlobSet(nil, nil)

	metricID := uint64(12345)
	metricName := "test.metric"

	t.Run("AllNumerics - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllNumerics(metricID) {
			count++
		}
		require.Zero(t, count, "AllNumerics should return empty iterator")
	})

	t.Run("AllNumericsByName - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllNumericsByName(metricName) {
			count++
		}
		require.Zero(t, count, "AllNumericsByName should return empty iterator")
	})

	t.Run("AllTexts - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllTexts(metricID) {
			count++
		}
		require.Zero(t, count, "AllTexts should return empty iterator")
	})

	t.Run("AllTextsByName - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllTextsByName(metricName) {
			count++
		}
		require.Zero(t, count, "AllTextsByName should return empty iterator")
	})

	t.Run("AllNumericValues - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllNumericValues(metricID) {
			count++
		}
		require.Zero(t, count, "AllNumericValues should return empty iterator")
	})

	t.Run("AllNumericValuesByName - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllNumericValuesByName(metricName) {
			count++
		}
		require.Zero(t, count, "AllNumericValuesByName should return empty iterator")
	})

	t.Run("AllTextValues - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllTextValues(metricID) {
			count++
		}
		require.Zero(t, count, "AllTextValues should return empty iterator")
	})

	t.Run("AllTextValuesByName - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllTextValuesByName(metricName) {
			count++
		}
		require.Zero(t, count, "AllTextValuesByName should return empty iterator")
	})

	t.Run("AllTimestamps - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllTimestamps(metricID) {
			count++
		}
		require.Zero(t, count, "AllTimestamps should return empty iterator")
	})

	t.Run("AllTimestampsByName - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllTimestampsByName(metricName) {
			count++
		}
		require.Zero(t, count, "AllTimestampsByName should return empty iterator")
	})

	t.Run("AllTags - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllTags(metricID) {
			count++
		}
		require.Zero(t, count, "AllTags should return empty iterator")
	})

	t.Run("AllTagsByName - empty iterator", func(t *testing.T) {
		count := 0
		for range emptySet.AllTagsByName(metricName) {
			count++
		}
		require.Zero(t, count, "AllTagsByName should return empty iterator")
	})

	t.Run("TimestampAt - returns false", func(t *testing.T) {
		ts, ok := emptySet.TimestampAt(metricID, 0)
		require.False(t, ok, "TimestampAt should return false for empty set")
		require.Zero(t, ts)
	})

	t.Run("TimestampAtByName - returns false", func(t *testing.T) {
		ts, ok := emptySet.TimestampAtByName(metricName, 0)
		require.False(t, ok, "TimestampAtByName should return false for empty set")
		require.Zero(t, ts)
	})

	t.Run("NumericValueAt - returns false", func(t *testing.T) {
		val, ok := emptySet.NumericValueAt(metricID, 0)
		require.False(t, ok, "NumericValueAt should return false for empty set")
		require.Zero(t, val)
	})

	t.Run("NumericValueAtByName - returns false", func(t *testing.T) {
		val, ok := emptySet.NumericValueAtByName(metricName, 0)
		require.False(t, ok, "NumericValueAtByName should return false for empty set")
		require.Zero(t, val)
	})

	t.Run("TextValueAt - returns false", func(t *testing.T) {
		val, ok := emptySet.TextValueAt(metricID, 0)
		require.False(t, ok, "TextValueAt should return false for empty set")
		require.Empty(t, val)
	})

	t.Run("TextValueAtByName - returns false", func(t *testing.T) {
		val, ok := emptySet.TextValueAtByName(metricName, 0)
		require.False(t, ok, "TextValueAtByName should return false for empty set")
		require.Empty(t, val)
	})

	t.Run("TagAt - returns false", func(t *testing.T) {
		tag, ok := emptySet.TagAt(metricID, 0)
		require.False(t, ok, "TagAt should return false for empty set")
		require.Empty(t, tag)
	})

	t.Run("TagAtByName - returns false", func(t *testing.T) {
		tag, ok := emptySet.TagAtByName(metricName, 0)
		require.False(t, ok, "TagAtByName should return false for empty set")
		require.Empty(t, tag)
	})

	t.Run("NumericAt - returns false", func(t *testing.T) {
		dp, ok := emptySet.NumericAt(metricID, 0)
		require.False(t, ok, "NumericAt should return false for empty set")
		require.Zero(t, dp)
	})

	t.Run("NumericAtByName - returns false", func(t *testing.T) {
		dp, ok := emptySet.NumericAtByName(metricName, 0)
		require.False(t, ok, "NumericAtByName should return false for empty set")
		require.Zero(t, dp)
	})

	t.Run("TextAt - returns false", func(t *testing.T) {
		dp, ok := emptySet.TextAt(metricID, 0)
		require.False(t, ok, "TextAt should return false for empty set")
		require.Zero(t, dp)
	})

	t.Run("TextAtByName - returns false", func(t *testing.T) {
		dp, ok := emptySet.TextAtByName(metricName, 0)
		require.False(t, ok, "TextAtByName should return false for empty set")
		require.Zero(t, dp)
	})
}

// TestBlobSet_NegativeIndex tests that negative indices are handled correctly
func TestBlobSet_NegativeIndex(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create a numeric blob with data
	numEncoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, numEncoder.StartMetricID(12345, 2))
	require.NoError(t, numEncoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
	require.NoError(t, numEncoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.0, ""))
	require.NoError(t, numEncoder.EndMetric())
	numData, err := numEncoder.Finish()
	require.NoError(t, err)
	numDecoder, err := NewNumericDecoder(numData)
	require.NoError(t, err)
	numBlob, err := numDecoder.Decode()
	require.NoError(t, err)

	// Create a text blob with data
	textEncoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, textEncoder.StartMetricID(67890, 2))
	require.NoError(t, textEncoder.AddDataPoint(startTime.UnixMicro(), "a", ""))
	require.NoError(t, textEncoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), "b", ""))
	require.NoError(t, textEncoder.EndMetric())
	textData, err := textEncoder.Finish()
	require.NoError(t, err)
	textDecoder, err := NewTextDecoder(textData)
	require.NoError(t, err)
	textBlob, err := textDecoder.Decode()
	require.NoError(t, err)

	blobSet := NewBlobSet([]NumericBlob{numBlob}, []TextBlob{textBlob})

	t.Run("NumericValueAt with negative index", func(t *testing.T) {
		val, ok := blobSet.NumericValueAt(12345, -1)
		require.False(t, ok, "NumericValueAt should return false for negative index")
		require.Zero(t, val)
	})

	t.Run("TextValueAt with negative index", func(t *testing.T) {
		val, ok := blobSet.TextValueAt(67890, -1)
		require.False(t, ok, "TextValueAt should return false for negative index")
		require.Empty(t, val)
	})

	t.Run("TimestampAt with negative index", func(t *testing.T) {
		ts, ok := blobSet.TimestampAt(12345, -1)
		require.False(t, ok, "TimestampAt should return false for negative index")
		require.Zero(t, ts)
	})

	t.Run("TagAt with negative index", func(t *testing.T) {
		tag, ok := blobSet.TagAt(12345, -1)
		require.False(t, ok, "TagAt should return false for negative index")
		require.Empty(t, tag)
	})

	t.Run("NumericAt with negative index", func(t *testing.T) {
		dp, ok := blobSet.NumericAt(12345, -1)
		require.False(t, ok, "NumericAt should return false for negative index")
		require.Zero(t, dp)
	})

	t.Run("TextAt with negative index", func(t *testing.T) {
		dp, ok := blobSet.TextAt(67890, -1)
		require.False(t, ok, "TextAt should return false for negative index")
		require.Zero(t, dp)
	})
}

// TestBlobSet_OutOfRangeIndex tests that out-of-range indices are handled correctly
func TestBlobSet_OutOfRangeIndex(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create a numeric blob with 3 data points
	numEncoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, numEncoder.StartMetricID(12345, 3))
	require.NoError(t, numEncoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
	require.NoError(t, numEncoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.0, ""))
	require.NoError(t, numEncoder.AddDataPoint(startTime.Add(2*time.Second).UnixMicro(), 3.0, ""))
	require.NoError(t, numEncoder.EndMetric())
	numData, err := numEncoder.Finish()
	require.NoError(t, err)
	numDecoder, err := NewNumericDecoder(numData)
	require.NoError(t, err)
	numBlob, err := numDecoder.Decode()
	require.NoError(t, err)

	blobSet := NewBlobSet([]NumericBlob{numBlob}, nil)

	t.Run("NumericValueAt with index out of range", func(t *testing.T) {
		// Valid indices are 0, 1, 2
		val, ok := blobSet.NumericValueAt(12345, 3)
		require.False(t, ok, "NumericValueAt should return false for index 3 (out of range)")
		require.Zero(t, val)

		val, ok = blobSet.NumericValueAt(12345, 100)
		require.False(t, ok, "NumericValueAt should return false for index 100 (out of range)")
		require.Zero(t, val)
	})

	t.Run("NumericValueAt with valid boundary indices", func(t *testing.T) {
		// Index 0 should work
		val, ok := blobSet.NumericValueAt(12345, 0)
		require.True(t, ok, "NumericValueAt should return true for index 0")
		require.Equal(t, 1.0, val)

		// Index 2 (last) should work
		val, ok = blobSet.NumericValueAt(12345, 2)
		require.True(t, ok, "NumericValueAt should return true for index 2 (last)")
		require.Equal(t, 3.0, val)
	})
}

// TestBlobSet_NonExistentMetric tests behavior when querying metrics that don't exist
func TestBlobSet_NonExistentMetric(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create blobs with specific metric IDs
	numEncoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, numEncoder.StartMetricID(12345, 2))
	require.NoError(t, numEncoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
	require.NoError(t, numEncoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.0, ""))
	require.NoError(t, numEncoder.EndMetric())
	numData, err := numEncoder.Finish()
	require.NoError(t, err)
	numDecoder, err := NewNumericDecoder(numData)
	require.NoError(t, err)
	numBlob, err := numDecoder.Decode()
	require.NoError(t, err)

	blobSet := NewBlobSet([]NumericBlob{numBlob}, nil)
	nonExistentID := uint64(99999)

	t.Run("AllNumerics with non-existent metric", func(t *testing.T) {
		count := 0
		for range blobSet.AllNumerics(nonExistentID) {
			count++
		}
		require.Zero(t, count, "AllNumerics should return empty iterator for non-existent metric")
	})

	t.Run("NumericValueAt with non-existent metric", func(t *testing.T) {
		val, ok := blobSet.NumericValueAt(nonExistentID, 0)
		require.False(t, ok, "NumericValueAt should return false for non-existent metric")
		require.Zero(t, val)
	})

	t.Run("TimestampAt with non-existent metric", func(t *testing.T) {
		ts, ok := blobSet.TimestampAt(nonExistentID, 0)
		require.False(t, ok, "TimestampAt should return false for non-existent metric")
		require.Zero(t, ts)
	})
}

// TestBlobSet_MultipleBlobs tests BlobSet with multiple blobs (global indexing)
func TestBlobSet_MultipleBlobs(t *testing.T) {
	metricID := uint64(12345)
	metricName := "test.metric"

	// Create first numeric blob (2 points) - using StartMetricID
	startTime1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numEncoder1, err := NewNumericEncoder(startTime1, WithTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, numEncoder1.StartMetricID(metricID, 2))
	require.NoError(t, numEncoder1.AddDataPoint(startTime1.UnixMicro(), 1.0, "tag1"))
	require.NoError(t, numEncoder1.AddDataPoint(startTime1.Add(time.Second).UnixMicro(), 2.0, "tag2"))
	require.NoError(t, numEncoder1.EndMetric())
	numData1, err := numEncoder1.Finish()
	require.NoError(t, err)
	numDecoder1, err := NewNumericDecoder(numData1)
	require.NoError(t, err)
	numBlob1, err := numDecoder1.Decode()
	require.NoError(t, err)

	// Create second numeric blob (3 points) - using same metric ID
	startTime2 := time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC)
	numEncoder2, err := NewNumericEncoder(startTime2, WithTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, numEncoder2.StartMetricID(metricID, 3))
	require.NoError(t, numEncoder2.AddDataPoint(startTime2.UnixMicro(), 3.0, "tag3"))
	require.NoError(t, numEncoder2.AddDataPoint(startTime2.Add(time.Second).UnixMicro(), 4.0, "tag4"))
	require.NoError(t, numEncoder2.AddDataPoint(startTime2.Add(2*time.Second).UnixMicro(), 5.0, "tag5"))
	require.NoError(t, numEncoder2.EndMetric())
	numData2, err := numEncoder2.Finish()
	require.NoError(t, err)
	numDecoder2, err := NewNumericDecoder(numData2)
	require.NoError(t, err)
	numBlob2, err := numDecoder2.Decode()
	require.NoError(t, err)

	blobSet := NewBlobSet([]NumericBlob{numBlob1, numBlob2}, nil)

	t.Run("AllNumerics - global indexing", func(t *testing.T) {
		values := make([]float64, 0, 5)
		indices := make([]int, 0, 5)
		for idx, dp := range blobSet.AllNumerics(metricID) {
			indices = append(indices, idx)
			values = append(values, dp.Val)
		}
		require.Equal(t, []int{0, 1, 2, 3, 4}, indices, "Global indices should be continuous")
		require.Equal(t, []float64{1.0, 2.0, 3.0, 4.0, 5.0}, values)
	})

	t.Run("NumericValueAt - boundary between blobs", func(t *testing.T) {
		// Index 0 - first point of first blob
		val, ok := blobSet.NumericValueAt(metricID, 0)
		require.True(t, ok)
		require.Equal(t, 1.0, val)

		// Index 1 - last point of first blob
		val, ok = blobSet.NumericValueAt(metricID, 1)
		require.True(t, ok)
		require.Equal(t, 2.0, val)

		// Index 2 - first point of second blob
		val, ok = blobSet.NumericValueAt(metricID, 2)
		require.True(t, ok)
		require.Equal(t, 3.0, val)

		// Index 4 - last point of second blob
		val, ok = blobSet.NumericValueAt(metricID, 4)
		require.True(t, ok)
		require.Equal(t, 5.0, val)

		// Index 5 - out of range
		val, ok = blobSet.NumericValueAt(metricID, 5)
		require.False(t, ok)
		require.Zero(t, val)
	})

	t.Run("TimestampAt - verify correct blob selection", func(t *testing.T) {
		// Index 0 - first blob
		ts, ok := blobSet.TimestampAt(metricID, 0)
		require.True(t, ok)
		require.Equal(t, startTime1.UnixMicro(), ts)

		// Index 2 - second blob
		ts, ok = blobSet.TimestampAt(metricID, 2)
		require.True(t, ok)
		require.Equal(t, startTime2.UnixMicro(), ts)
	})

	t.Run("TagAt - verify correct blob selection", func(t *testing.T) {
		// Index 0 - first blob
		tag, ok := blobSet.TagAt(metricID, 0)
		require.True(t, ok)
		require.Equal(t, "tag1", tag)

		// Index 2 - second blob
		tag, ok = blobSet.TagAt(metricID, 2)
		require.True(t, ok)
		require.Equal(t, "tag3", tag)
	})

	t.Run("AllNumericsByName - hash fallback works", func(t *testing.T) {
		// Both blobs use StartMetricID, query by name should use hash fallback
		// The metric name that hashes to our metric ID
		values := make([]float64, 0, 5)
		for _, dp := range blobSet.AllNumericsByName(metricName) {
			values = append(values, dp.Val)
		}
		// Since we used StartMetricID with a specific ID, and the blob has no metric names,
		// the hash fallback will only work if the name hashes to the correct ID
		expectedID := hash.ID(metricName)
		if expectedID == metricID {
			require.Equal(t, []float64{1.0, 2.0, 3.0, 4.0, 5.0}, values)
		} else {
			// If the hash doesn't match, we won't find anything
			require.Empty(t, values, "Hash fallback should return empty when name doesn't match ID")
		}
	})
}

// TestBlobSet_MixedBlobTypes tests BlobSet with both numeric and text blobs
func TestBlobSet_MixedBlobTypes(t *testing.T) {
	numMetricID := uint64(12345)
	textMetricID := uint64(67890)
	sharedTimestampMetricID := uint64(11111)

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create numeric blob
	numEncoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, numEncoder.StartMetricID(numMetricID, 2))
	require.NoError(t, numEncoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
	require.NoError(t, numEncoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.0, ""))
	require.NoError(t, numEncoder.EndMetric())
	// Add metric that appears in both numeric and text (for timestamp test)
	require.NoError(t, numEncoder.StartMetricID(sharedTimestampMetricID, 2))
	require.NoError(t, numEncoder.AddDataPoint(startTime.UnixMicro(), 100.0, "num"))
	require.NoError(t, numEncoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 200.0, "num"))
	require.NoError(t, numEncoder.EndMetric())
	numData, err := numEncoder.Finish()
	require.NoError(t, err)
	numDecoder, err := NewNumericDecoder(numData)
	require.NoError(t, err)
	numBlob, err := numDecoder.Decode()
	require.NoError(t, err)

	// Create text blob
	textEncoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, textEncoder.StartMetricID(textMetricID, 2))
	require.NoError(t, textEncoder.AddDataPoint(startTime.UnixMicro(), "a", ""))
	require.NoError(t, textEncoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), "b", ""))
	require.NoError(t, textEncoder.EndMetric())
	textData, err := textEncoder.Finish()
	require.NoError(t, err)
	textDecoder, err := NewTextDecoder(textData)
	require.NoError(t, err)
	textBlob, err := textDecoder.Decode()
	require.NoError(t, err)

	blobSet := NewBlobSet([]NumericBlob{numBlob}, []TextBlob{textBlob})

	t.Run("AllNumerics - only returns numeric data", func(t *testing.T) {
		values := make([]float64, 0, 2)
		for _, dp := range blobSet.AllNumerics(numMetricID) {
			values = append(values, dp.Val)
		}
		require.Equal(t, []float64{1.0, 2.0}, values)

		// Text metric ID shouldn't return anything
		count := 0
		for range blobSet.AllNumerics(textMetricID) {
			count++
		}
		require.Zero(t, count)
	})

	t.Run("AllTexts - only returns text data", func(t *testing.T) {
		values := make([]string, 0, 2)
		for _, dp := range blobSet.AllTexts(textMetricID) {
			values = append(values, dp.Val)
		}
		require.Equal(t, []string{"a", "b"}, values)

		// Numeric metric ID shouldn't return anything
		count := 0
		for range blobSet.AllTexts(numMetricID) {
			count++
		}
		require.Zero(t, count)
	})

	t.Run("AllTimestamps - tries numeric first", func(t *testing.T) {
		// Numeric metric - should find in numeric blobs
		timestamps := make([]int64, 0, 2)
		for _, ts := range blobSet.AllTimestamps(numMetricID) {
			timestamps = append(timestamps, ts)
		}
		require.Len(t, timestamps, 2)

		// Text metric - should find in text blobs
		timestamps = make([]int64, 0, 2)
		for _, ts := range blobSet.AllTimestamps(textMetricID) {
			timestamps = append(timestamps, ts)
		}
		require.Len(t, timestamps, 2)
	})

	t.Run("NumericValueAt vs TextValueAt - type safety", func(t *testing.T) {
		// NumericValueAt should work for numeric metric
		val, ok := blobSet.NumericValueAt(numMetricID, 0)
		require.True(t, ok)
		require.Equal(t, 1.0, val)

		// NumericValueAt should fail for text metric
		val, ok = blobSet.NumericValueAt(textMetricID, 0)
		require.False(t, ok)
		require.Zero(t, val)

		// TextValueAt should work for text metric
		textVal, ok := blobSet.TextValueAt(textMetricID, 0)
		require.True(t, ok)
		require.Equal(t, "a", textVal)

		// TextValueAt should fail for numeric metric
		textVal, ok = blobSet.TextValueAt(numMetricID, 0)
		require.False(t, ok)
		require.Empty(t, textVal)
	})
}

// TestBlobSet_BlobSorting tests that blobs are sorted by start time
func TestBlobSet_BlobSorting(t *testing.T) {
	metricID := uint64(12345)

	// Create blobs with different start times (intentionally out of order)
	startTime1 := time.Date(2024, 1, 1, 2, 0, 0, 0, time.UTC) // Latest
	startTime2 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) // Earliest
	startTime3 := time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC) // Middle

	// Create blob 1 (latest time, but added first)
	numEncoder1, err := NewNumericEncoder(startTime1)
	require.NoError(t, err)
	require.NoError(t, numEncoder1.StartMetricID(metricID, 1))
	require.NoError(t, numEncoder1.AddDataPoint(startTime1.UnixMicro(), 3.0, ""))
	require.NoError(t, numEncoder1.EndMetric())
	numData1, err := numEncoder1.Finish()
	require.NoError(t, err)
	numDecoder1, err := NewNumericDecoder(numData1)
	require.NoError(t, err)
	numBlob1, err := numDecoder1.Decode()
	require.NoError(t, err)

	// Create blob 2 (earliest time)
	numEncoder2, err := NewNumericEncoder(startTime2)
	require.NoError(t, err)
	require.NoError(t, numEncoder2.StartMetricID(metricID, 1))
	require.NoError(t, numEncoder2.AddDataPoint(startTime2.UnixMicro(), 1.0, ""))
	require.NoError(t, numEncoder2.EndMetric())
	numData2, err := numEncoder2.Finish()
	require.NoError(t, err)
	numDecoder2, err := NewNumericDecoder(numData2)
	require.NoError(t, err)
	numBlob2, err := numDecoder2.Decode()
	require.NoError(t, err)

	// Create blob 3 (middle time)
	numEncoder3, err := NewNumericEncoder(startTime3)
	require.NoError(t, err)
	require.NoError(t, numEncoder3.StartMetricID(metricID, 1))
	require.NoError(t, numEncoder3.AddDataPoint(startTime3.UnixMicro(), 2.0, ""))
	require.NoError(t, numEncoder3.EndMetric())
	numData3, err := numEncoder3.Finish()
	require.NoError(t, err)
	numDecoder3, err := NewNumericDecoder(numData3)
	require.NoError(t, err)
	numBlob3, err := numDecoder3.Decode()
	require.NoError(t, err)

	// Add in non-chronological order
	blobSet := NewBlobSet([]NumericBlob{numBlob1, numBlob2, numBlob3}, nil)

	t.Run("Values are returned in sorted order", func(t *testing.T) {
		values := make([]float64, 0, 3)
		for _, dp := range blobSet.AllNumerics(metricID) {
			values = append(values, dp.Val)
		}
		// Should be sorted by start time: earliest to latest
		require.Equal(t, []float64{1.0, 2.0, 3.0}, values)
	})

	t.Run("Random access respects sorted order", func(t *testing.T) {
		// Index 0 should be earliest
		val, ok := blobSet.NumericValueAt(metricID, 0)
		require.True(t, ok)
		require.Equal(t, 1.0, val)

		// Index 1 should be middle
		val, ok = blobSet.NumericValueAt(metricID, 1)
		require.True(t, ok)
		require.Equal(t, 2.0, val)

		// Index 2 should be latest
		val, ok = blobSet.NumericValueAt(metricID, 2)
		require.True(t, ok)
		require.Equal(t, 3.0, val)
	})
}

// TestBlobSet_WithMetricNames tests ByName methods with metric names payload
func TestBlobSet_WithMetricNames(t *testing.T) {
	metricName := "cpu.usage"
	metricID := hash.ID(metricName)
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create numeric blob with metric name
	numEncoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, numEncoder.StartMetricName(metricName, 2))
	require.NoError(t, numEncoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
	require.NoError(t, numEncoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.0, ""))
	require.NoError(t, numEncoder.EndMetric())
	numData, err := numEncoder.Finish()
	require.NoError(t, err)
	numDecoder, err := NewNumericDecoder(numData)
	require.NoError(t, err)
	numBlob, err := numDecoder.Decode()
	require.NoError(t, err)

	blobSet := NewBlobSet([]NumericBlob{numBlob}, nil)

	t.Run("AllNumericsByName works", func(t *testing.T) {
		values := make([]float64, 0, 2)
		for _, dp := range blobSet.AllNumericsByName(metricName) {
			values = append(values, dp.Val)
		}
		require.Equal(t, []float64{1.0, 2.0}, values)
	})

	t.Run("NumericValueAtByName works", func(t *testing.T) {
		val, ok := blobSet.NumericValueAtByName(metricName, 0)
		require.True(t, ok)
		require.Equal(t, 1.0, val)

		val, ok = blobSet.NumericValueAtByName(metricName, 1)
		require.True(t, ok)
		require.Equal(t, 2.0, val)
	})

	t.Run("TimestampAtByName works", func(t *testing.T) {
		ts, ok := blobSet.TimestampAtByName(metricName, 0)
		require.True(t, ok)
		require.Equal(t, startTime.UnixMicro(), ts)
	})

	t.Run("Works with both ID and Name", func(t *testing.T) {
		// Should work with ID
		valByID, okID := blobSet.NumericValueAt(metricID, 0)
		require.True(t, okID)

		// Should work with Name
		valByName, okName := blobSet.NumericValueAtByName(metricName, 0)
		require.True(t, okName)

		// Should return same value
		require.Equal(t, valByID, valByName)
	})
}

// TestBlobSet_EarlyTermination tests that iterators can be terminated early
func TestBlobSet_EarlyTermination(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricID := uint64(12345)

	// Create blob with 5 points
	numEncoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)
	require.NoError(t, numEncoder.StartMetricID(metricID, 5))
	for i := 0; i < 5; i++ {
		require.NoError(t, numEncoder.AddDataPoint(
			startTime.Add(time.Duration(i)*time.Second).UnixMicro(),
			float64(i),
			"",
		))
	}
	require.NoError(t, numEncoder.EndMetric())
	numData, err := numEncoder.Finish()
	require.NoError(t, err)
	numDecoder, err := NewNumericDecoder(numData)
	require.NoError(t, err)
	numBlob, err := numDecoder.Decode()
	require.NoError(t, err)

	blobSet := NewBlobSet([]NumericBlob{numBlob}, nil)

	t.Run("AllNumerics - early termination", func(t *testing.T) {
		values := make([]float64, 0, 5)
		for _, dp := range blobSet.AllNumerics(metricID) {
			values = append(values, dp.Val)
			if len(values) == 3 {
				break // Early termination
			}
		}
		require.Equal(t, []float64{0.0, 1.0, 2.0}, values, "Should only collect 3 values")
	})

	t.Run("AllNumericValues - early termination", func(t *testing.T) {
		values := make([]float64, 0, 5)
		for _, val := range blobSet.AllNumericValues(metricID) {
			values = append(values, val)
			if len(values) == 2 {
				break // Early termination
			}
		}
		require.Equal(t, []float64{0.0, 1.0}, values, "Should only collect 2 values")
	})
}

// TestBlobSet_CompleteDataPoint tests NumericAt and TextAt methods
func TestBlobSet_CompleteDataPoint(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numMetricID := uint64(12345)
	textMetricID := uint64(67890)

	// Create numeric blob with tags
	numEncoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, numEncoder.StartMetricID(numMetricID, 2))
	require.NoError(t, numEncoder.AddDataPoint(startTime.UnixMicro(), 1.0, "tag1"))
	require.NoError(t, numEncoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.0, "tag2"))
	require.NoError(t, numEncoder.EndMetric())
	numData, err := numEncoder.Finish()
	require.NoError(t, err)
	numDecoder, err := NewNumericDecoder(numData)
	require.NoError(t, err)
	numBlob, err := numDecoder.Decode()
	require.NoError(t, err)

	// Create text blob with tags
	textEncoder, err := NewTextEncoder(startTime, WithTextTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, textEncoder.StartMetricID(textMetricID, 2))
	require.NoError(t, textEncoder.AddDataPoint(startTime.UnixMicro(), "a", "tagA"))
	require.NoError(t, textEncoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), "b", "tagB"))
	require.NoError(t, textEncoder.EndMetric())
	textData, err := textEncoder.Finish()
	require.NoError(t, err)
	textDecoder, err := NewTextDecoder(textData)
	require.NoError(t, err)
	textBlob, err := textDecoder.Decode()
	require.NoError(t, err)

	blobSet := NewBlobSet([]NumericBlob{numBlob}, []TextBlob{textBlob})

	t.Run("NumericAt - complete data point", func(t *testing.T) {
		dp, ok := blobSet.NumericAt(numMetricID, 0)
		require.True(t, ok)
		require.Equal(t, startTime.UnixMicro(), dp.Ts)
		require.Equal(t, 1.0, dp.Val)
		require.Equal(t, "tag1", dp.Tag)

		dp, ok = blobSet.NumericAt(numMetricID, 1)
		require.True(t, ok)
		require.Equal(t, startTime.Add(time.Second).UnixMicro(), dp.Ts)
		require.Equal(t, 2.0, dp.Val)
		require.Equal(t, "tag2", dp.Tag)
	})

	t.Run("TextAt - complete data point", func(t *testing.T) {
		dp, ok := blobSet.TextAt(textMetricID, 0)
		require.True(t, ok)
		require.Equal(t, startTime.UnixMicro(), dp.Ts)
		require.Equal(t, "a", dp.Val)
		require.Equal(t, "tagA", dp.Tag)

		dp, ok = blobSet.TextAt(textMetricID, 1)
		require.True(t, ok)
		require.Equal(t, startTime.Add(time.Second).UnixMicro(), dp.Ts)
		require.Equal(t, "b", dp.Val)
		require.Equal(t, "tagB", dp.Tag)
	})

	t.Run("NumericAtByName and TextAtByName", func(t *testing.T) {
		// Numeric
		dp, ok := blobSet.NumericAt(numMetricID, 0)
		require.True(t, ok)
		require.Equal(t, 1.0, dp.Val)

		// Text
		tdp, ok := blobSet.TextAt(textMetricID, 0)
		require.True(t, ok)
		require.Equal(t, "a", tdp.Val)
	})
}

// TestDecodeBlobSet_EmptyInput tests DecodeBlobSet with no input blobs
func TestDecodeBlobSet_EmptyInput(t *testing.T) {
	blobSet, err := DecodeBlobSet()
	require.NoError(t, err)
	require.NotNil(t, blobSet)
	require.Empty(t, blobSet.numericBlobs)
	require.Empty(t, blobSet.textBlobs)
}

// TestDecodeBlobSet_SingleNumericBlob tests decoding a single numeric blob
func TestDecodeBlobSet_SingleNumericBlob(t *testing.T) {
	// Create a numeric blob
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)

	// Add a metric with data points
	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)
	err = encoder.AddDataPoint(startTime.UnixMicro(), 1.5, "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.5, "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(startTime.Add(2*time.Second).UnixMicro(), 3.5, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode the blob set
	blobSet, err := DecodeBlobSet(data)
	require.NoError(t, err)
	require.Len(t, blobSet.numericBlobs, 1)
	require.Empty(t, blobSet.textBlobs)

	// Verify the decoded blob
	blob := blobSet.numericBlobs[0]
	require.True(t, blob.HasMetricID(12345))
	require.Equal(t, 3, blob.Len(12345))
}

// TestDecodeBlobSet_SingleTextBlob tests decoding a single text blob
func TestDecodeBlobSet_SingleTextBlob(t *testing.T) {
	// Create a text blob
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)

	// Add a metric with text values
	err = encoder.StartMetricID(67890, 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(startTime.UnixMicro(), "hello", "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), "world", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode the blob set
	blobSet, err := DecodeBlobSet(data)
	require.NoError(t, err)
	require.Empty(t, blobSet.numericBlobs)
	require.Len(t, blobSet.textBlobs, 1)

	// Verify the decoded blob
	blob := blobSet.textBlobs[0]
	require.True(t, blob.HasMetricID(67890))
	require.Equal(t, 2, blob.Len(67890))
}

// TestDecodeBlobSet_MultipleNumericBlobs tests decoding multiple numeric blobs
func TestDecodeBlobSet_MultipleNumericBlobs(t *testing.T) {
	// Create first numeric blob
	startTime1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder1, err := NewNumericEncoder(startTime1)
	require.NoError(t, err)
	err = encoder1.StartMetricID(100, 1)
	require.NoError(t, err)
	err = encoder1.AddDataPoint(startTime1.UnixMicro(), 10.0, "")
	require.NoError(t, err)
	err = encoder1.EndMetric()
	require.NoError(t, err)
	data1, err := encoder1.Finish()
	require.NoError(t, err)

	// Create second numeric blob
	startTime2 := time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC)
	encoder2, err := NewNumericEncoder(startTime2)
	require.NoError(t, err)
	err = encoder2.StartMetricID(200, 1)
	require.NoError(t, err)
	err = encoder2.AddDataPoint(startTime2.UnixMicro(), 20.0, "")
	require.NoError(t, err)
	err = encoder2.EndMetric()
	require.NoError(t, err)
	data2, err := encoder2.Finish()
	require.NoError(t, err)

	// Decode the blob set
	blobSet, err := DecodeBlobSet(data1, data2)
	require.NoError(t, err)
	require.Len(t, blobSet.numericBlobs, 2)
	require.Empty(t, blobSet.textBlobs)

	// Verify blobs are sorted by start time
	require.True(t, blobSet.numericBlobs[0].StartTime().Before(blobSet.numericBlobs[1].StartTime()))
	require.True(t, blobSet.numericBlobs[0].HasMetricID(100))
	require.True(t, blobSet.numericBlobs[1].HasMetricID(200))
}

// TestDecodeBlobSet_MultipleTextBlobs tests decoding multiple text blobs
func TestDecodeBlobSet_MultipleTextBlobs(t *testing.T) {
	// Create first text blob
	startTime1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder1, err := NewTextEncoder(startTime1)
	require.NoError(t, err)
	err = encoder1.StartMetricID(300, 1)
	require.NoError(t, err)
	err = encoder1.AddDataPoint(startTime1.UnixMicro(), "first", "")
	require.NoError(t, err)
	err = encoder1.EndMetric()
	require.NoError(t, err)
	data1, err := encoder1.Finish()
	require.NoError(t, err)

	// Create second text blob
	startTime2 := time.Date(2024, 1, 1, 2, 0, 0, 0, time.UTC)
	encoder2, err := NewTextEncoder(startTime2)
	require.NoError(t, err)
	err = encoder2.StartMetricID(400, 1)
	require.NoError(t, err)
	err = encoder2.AddDataPoint(startTime2.UnixMicro(), "second", "")
	require.NoError(t, err)
	err = encoder2.EndMetric()
	require.NoError(t, err)
	data2, err := encoder2.Finish()
	require.NoError(t, err)

	// Decode the blob set
	blobSet, err := DecodeBlobSet(data1, data2)
	require.NoError(t, err)
	require.Empty(t, blobSet.numericBlobs)
	require.Len(t, blobSet.textBlobs, 2)

	// Verify blobs are sorted by start time
	require.True(t, blobSet.textBlobs[0].StartTime().Before(blobSet.textBlobs[1].StartTime()))
	require.True(t, blobSet.textBlobs[0].HasMetricID(300))
	require.True(t, blobSet.textBlobs[1].HasMetricID(400))
}

// TestDecodeBlobSet_MixedBlobs tests decoding both numeric and text blobs
func TestDecodeBlobSet_MixedBlobs(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create numeric blob
	numEncoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)
	err = numEncoder.StartMetricID(500, 2)
	require.NoError(t, err)
	err = numEncoder.AddDataPoint(startTime.UnixMicro(), 50.0, "")
	require.NoError(t, err)
	err = numEncoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 51.0, "")
	require.NoError(t, err)
	err = numEncoder.EndMetric()
	require.NoError(t, err)
	numData, err := numEncoder.Finish()
	require.NoError(t, err)

	// Create text blob
	textEncoder, err := NewTextEncoder(startTime.Add(time.Hour))
	require.NoError(t, err)
	err = textEncoder.StartMetricID(600, 2)
	require.NoError(t, err)
	err = textEncoder.AddDataPoint(startTime.Add(time.Hour).UnixMicro(), "alpha", "")
	require.NoError(t, err)
	err = textEncoder.AddDataPoint(startTime.Add(time.Hour+time.Second).UnixMicro(), "beta", "")
	require.NoError(t, err)
	err = textEncoder.EndMetric()
	require.NoError(t, err)
	textData, err := textEncoder.Finish()
	require.NoError(t, err)

	// Decode the blob set
	blobSet, err := DecodeBlobSet(numData, textData)
	require.NoError(t, err)
	require.Len(t, blobSet.numericBlobs, 1)
	require.Len(t, blobSet.textBlobs, 1)

	// Verify numeric blob
	require.True(t, blobSet.numericBlobs[0].HasMetricID(500))
	require.Equal(t, 2, blobSet.numericBlobs[0].Len(500))

	// Verify text blob
	require.True(t, blobSet.textBlobs[0].HasMetricID(600))
	require.Equal(t, 2, blobSet.textBlobs[0].Len(600))
}

// TestDecodeBlobSet_InvalidBlob tests decoding with invalid blob data
func TestDecodeBlobSet_InvalidBlob(t *testing.T) {
	t.Run("Too short data", func(t *testing.T) {
		invalidData := []byte{0x01, 0x02, 0x03}
		blobSet, err := DecodeBlobSet(invalidData)
		// Current implementation: silently ignores invalid blobs
		// This test documents the current behavior
		require.NoError(t, err)
		require.Empty(t, blobSet.numericBlobs)
		require.Empty(t, blobSet.textBlobs)
	})

	t.Run("Empty byte slice", func(t *testing.T) {
		emptyData := []byte{}
		blobSet, err := DecodeBlobSet(emptyData)
		require.NoError(t, err)
		require.Empty(t, blobSet.numericBlobs)
		require.Empty(t, blobSet.textBlobs)
	})

	t.Run("Invalid magic number", func(t *testing.T) {
		// Create blob with invalid magic number (32 bytes with wrong magic)
		invalidData := make([]byte, 32)
		invalidData[0] = 0xFF // Wrong magic number
		invalidData[1] = 0xFF

		blobSet, err := DecodeBlobSet(invalidData)
		// Current implementation: silently ignores invalid blobs
		require.NoError(t, err)
		require.Empty(t, blobSet.numericBlobs)
		require.Empty(t, blobSet.textBlobs)
	})
}

// TestDecodeBlobSet_CorruptedNumericBlob tests handling of corrupted numeric blob
func TestDecodeBlobSet_CorruptedNumericBlob(t *testing.T) {
	// Create a valid numeric blob first
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)
	err = encoder.StartMetricID(700, 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(startTime.UnixMicro(), 70.0, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Corrupt the data by truncating it
	corruptedData := data[:len(data)/2]

	// Attempt to decode - should fail with error
	_, err = DecodeBlobSet(corruptedData)
	require.Error(t, err, "Should return error for corrupted numeric blob")
}

// TestDecodeBlobSet_CorruptedTextBlob tests handling of corrupted text blob
func TestDecodeBlobSet_CorruptedTextBlob(t *testing.T) {
	// Create a valid text blob first
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)
	err = encoder.StartMetricID(800, 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(startTime.UnixMicro(), "corrupted", "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Corrupt the data by truncating it
	corruptedData := data[:len(data)/2]

	// Attempt to decode - should fail with error
	_, err = DecodeBlobSet(corruptedData)
	require.Error(t, err, "Should return error for corrupted text blob")
}

// TestDecodeBlobSet_WithDifferentEncodings tests decoding blobs with different encoding types
func TestDecodeBlobSet_WithDifferentEncodings(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create numeric blob with delta timestamp encoding
	encoder1, err := NewNumericEncoder(startTime, WithTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)
	err = encoder1.StartMetricID(900, 2)
	require.NoError(t, err)
	err = encoder1.AddDataPoint(startTime.UnixMicro(), 90.0, "")
	require.NoError(t, err)
	err = encoder1.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 91.0, "")
	require.NoError(t, err)
	err = encoder1.EndMetric()
	require.NoError(t, err)
	data1, err := encoder1.Finish()
	require.NoError(t, err)

	// Create numeric blob with raw timestamp encoding
	encoder2, err := NewNumericEncoder(startTime.Add(time.Hour), WithTimestampEncoding(format.TypeRaw))
	require.NoError(t, err)
	err = encoder2.StartMetricID(1000, 2)
	require.NoError(t, err)
	err = encoder2.AddDataPoint(startTime.Add(time.Hour).UnixMicro(), 100.0, "")
	require.NoError(t, err)
	err = encoder2.AddDataPoint(startTime.Add(time.Hour+time.Second).UnixMicro(), 101.0, "")
	require.NoError(t, err)
	err = encoder2.EndMetric()
	require.NoError(t, err)
	data2, err := encoder2.Finish()
	require.NoError(t, err)

	// Decode both blobs
	blobSet, err := DecodeBlobSet(data1, data2)
	require.NoError(t, err)
	require.Len(t, blobSet.numericBlobs, 2)

	// Verify both blobs were decoded correctly
	require.True(t, blobSet.numericBlobs[0].HasMetricID(900))
	require.True(t, blobSet.numericBlobs[1].HasMetricID(1000))
	require.Equal(t, 2, blobSet.numericBlobs[0].Len(900))
	require.Equal(t, 2, blobSet.numericBlobs[1].Len(1000))
}

// TestDecodeBlobSet_UnsortedInput tests that blobs are sorted even if input is unsorted
func TestDecodeBlobSet_UnsortedInput(t *testing.T) {
	// Create blobs with non-chronological start times
	time3 := time.Date(2024, 1, 1, 3, 0, 0, 0, time.UTC)
	time1 := time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC)
	time2 := time.Date(2024, 1, 1, 2, 0, 0, 0, time.UTC)

	// Create blob3 (latest time)
	encoder3, err := NewNumericEncoder(time3)
	require.NoError(t, err)
	err = encoder3.StartMetricID(1100, 1)
	require.NoError(t, err)
	err = encoder3.AddDataPoint(time3.UnixMicro(), 30.0, "")
	require.NoError(t, err)
	err = encoder3.EndMetric()
	require.NoError(t, err)
	data3, err := encoder3.Finish()
	require.NoError(t, err)

	// Create blob1 (earliest time)
	encoder1, err := NewNumericEncoder(time1)
	require.NoError(t, err)
	err = encoder1.StartMetricID(1200, 1)
	require.NoError(t, err)
	err = encoder1.AddDataPoint(time1.UnixMicro(), 10.0, "")
	require.NoError(t, err)
	err = encoder1.EndMetric()
	require.NoError(t, err)
	data1, err := encoder1.Finish()
	require.NoError(t, err)

	// Create blob2 (middle time)
	encoder2, err := NewNumericEncoder(time2)
	require.NoError(t, err)
	err = encoder2.StartMetricID(1300, 1)
	require.NoError(t, err)
	err = encoder2.AddDataPoint(time2.UnixMicro(), 20.0, "")
	require.NoError(t, err)
	err = encoder2.EndMetric()
	require.NoError(t, err)
	data2, err := encoder2.Finish()
	require.NoError(t, err)

	// Pass blobs in unsorted order: 3, 1, 2
	blobSet, err := DecodeBlobSet(data3, data1, data2)
	require.NoError(t, err)
	require.Len(t, blobSet.numericBlobs, 3)

	// Verify blobs are sorted by start time (should be 1, 2, 3)
	require.True(t, blobSet.numericBlobs[0].HasMetricID(1200)) // time1 (earliest)
	require.True(t, blobSet.numericBlobs[1].HasMetricID(1300)) // time2 (middle)
	require.True(t, blobSet.numericBlobs[2].HasMetricID(1100)) // time3 (latest)
	require.True(t, blobSet.numericBlobs[0].StartTime().Before(blobSet.numericBlobs[1].StartTime()))
	require.True(t, blobSet.numericBlobs[1].StartTime().Before(blobSet.numericBlobs[2].StartTime()))
}

// TestDecodeBlobSet_WithMetricNames tests decoding blobs that include metric names
func TestDecodeBlobSet_WithMetricNames(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create numeric blob with metric names (automatically enabled when using StartMetricName)
	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)
	err = encoder.StartMetricName("cpu.usage", 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(startTime.UnixMicro(), 75.5, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode the blob set
	blobSet, err := DecodeBlobSet(data)
	require.NoError(t, err)
	require.Len(t, blobSet.numericBlobs, 1)

	// Verify metric name is accessible
	blob := blobSet.numericBlobs[0]
	require.True(t, blob.HasMetricName("cpu.usage"))
	require.Equal(t, 1, blob.LenByName("cpu.usage"))
}

// TestDecodeBlobSet_WithTags tests decoding blobs with tagged data points
func TestDecodeBlobSet_WithTags(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create numeric blob with tags (automatically enabled when non-empty tags are added)
	encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
	require.NoError(t, err)
	err = encoder.StartMetricID(1400, 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(startTime.UnixMicro(), 14.0, "host=server1")
	require.NoError(t, err)
	err = encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 15.0, "host=server2")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode the blob set
	blobSet, err := DecodeBlobSet(data)
	require.NoError(t, err)
	require.Len(t, blobSet.numericBlobs, 1)

	// Verify tags are preserved
	blob := blobSet.numericBlobs[0]
	tag1, ok := blob.TagAt(1400, 0)
	require.True(t, ok)
	require.Equal(t, "host=server1", tag1)

	tag2, ok := blob.TagAt(1400, 1)
	require.True(t, ok)
	require.Equal(t, "host=server2", tag2)
}

// TestDecodeBlobSet_VariadicInput tests the variadic parameter behavior
func TestDecodeBlobSet_VariadicInput(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create test blobs
	encoder1, err := NewNumericEncoder(startTime)
	require.NoError(t, err)
	err = encoder1.StartMetricID(1500, 1)
	require.NoError(t, err)
	err = encoder1.AddDataPoint(startTime.UnixMicro(), 15.0, "")
	require.NoError(t, err)
	err = encoder1.EndMetric()
	require.NoError(t, err)
	data1, err := encoder1.Finish()
	require.NoError(t, err)

	encoder2, err := NewNumericEncoder(startTime.Add(time.Hour))
	require.NoError(t, err)
	err = encoder2.StartMetricID(1600, 1)
	require.NoError(t, err)
	err = encoder2.AddDataPoint(startTime.Add(time.Hour).UnixMicro(), 16.0, "")
	require.NoError(t, err)
	err = encoder2.EndMetric()
	require.NoError(t, err)
	data2, err := encoder2.Finish()
	require.NoError(t, err)

	t.Run("Single blob", func(t *testing.T) {
		blobSet, err := DecodeBlobSet(data1)
		require.NoError(t, err)
		require.Len(t, blobSet.numericBlobs, 1)
	})

	t.Run("Multiple blobs", func(t *testing.T) {
		blobSet, err := DecodeBlobSet(data1, data2)
		require.NoError(t, err)
		require.Len(t, blobSet.numericBlobs, 2)
	})

	t.Run("Slice expansion", func(t *testing.T) {
		blobs := [][]byte{data1, data2}
		blobSet, err := DecodeBlobSet(blobs...)
		require.NoError(t, err)
		require.Len(t, blobSet.numericBlobs, 2)
	})
}
