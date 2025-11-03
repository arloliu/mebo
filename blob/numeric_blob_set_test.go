package blob

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
)

// ==============================================================================
// Helper Functions
// ==============================================================================

func createBlobWithTimestamp(t *testing.T, blobTS time.Time, metricName string, timestamps []int64, values []float64) NumericBlob {
	t.Helper()

	metricID := hash.ID(metricName)

	// Create encoder
	encoder, err := NewNumericEncoder(blobTS,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
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

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	return blob
}

func createTestBlobs(t *testing.T, count int) []NumericBlob {
	t.Helper()

	blobTS := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	blobs := make([]NumericBlob, count)

	for i := range count {
		metricName := "metric1"
		ts := []int64{
			blobTS.Add(time.Duration(i) * time.Hour).UnixMicro(),
			blobTS.Add(time.Duration(i)*time.Hour + 1*time.Minute).UnixMicro(),
		}
		val := []float64{float64(i * 10), float64(i*10 + 1)}

		blobs[i] = createBlobWithTimestamp(t, blobTS.Add(time.Duration(i)*time.Hour), metricName, ts, val)
	}

	return blobs
}

func TestNumericBlobSet_ValueAt(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Create 3 blobs with different number of points
	// Blob 0: 3 points (indices 0-2)
	ts1 := []int64{
		startTime.UnixMicro(),
		startTime.Add(1 * time.Minute).UnixMicro(),
		startTime.Add(2 * time.Minute).UnixMicro(),
	}
	val1 := []float64{10.0, 11.0, 12.0}
	blob1 := createBlobWithTimestamp(t, startTime, metricName, ts1, val1)

	// Blob 1: 5 points (indices 3-7)
	startTime2 := startTime.Add(1 * time.Hour)
	ts2 := []int64{
		startTime2.UnixMicro(),
		startTime2.Add(1 * time.Minute).UnixMicro(),
		startTime2.Add(2 * time.Minute).UnixMicro(),
		startTime2.Add(3 * time.Minute).UnixMicro(),
		startTime2.Add(4 * time.Minute).UnixMicro(),
	}
	val2 := []float64{20.0, 21.0, 22.0, 23.0, 24.0}
	blob2 := createBlobWithTimestamp(t, startTime2, metricName, ts2, val2)

	// Blob 2: 2 points (indices 8-9)
	startTime3 := startTime.Add(2 * time.Hour)
	ts3 := []int64{
		startTime3.UnixMicro(),
		startTime3.Add(1 * time.Minute).UnixMicro(),
	}
	val3 := []float64{30.0, 31.0}
	blob3 := createBlobWithTimestamp(t, startTime3, metricName, ts3, val3)

	blobSet, err := NewNumericBlobSet([]NumericBlob{blob1, blob2, blob3})
	require.NoError(t, err)

	t.Run("FirstBlob", func(t *testing.T) {
		// Index 0-2 should be in blob 0
		value, ok := blobSet.ValueAt(metricID, 0)
		require.True(t, ok)
		require.Equal(t, 10.0, value)

		value, ok = blobSet.ValueAt(metricID, 1)
		require.True(t, ok)
		require.Equal(t, 11.0, value)

		value, ok = blobSet.ValueAt(metricID, 2)
		require.True(t, ok)
		require.Equal(t, 12.0, value)
	})

	t.Run("SecondBlob", func(t *testing.T) {
		// Index 3-7 should be in blob 1
		value, ok := blobSet.ValueAt(metricID, 3)
		require.True(t, ok)
		require.Equal(t, 20.0, value)

		value, ok = blobSet.ValueAt(metricID, 4)
		require.True(t, ok)
		require.Equal(t, 21.0, value)

		value, ok = blobSet.ValueAt(metricID, 7)
		require.True(t, ok)
		require.Equal(t, 24.0, value)
	})

	t.Run("ThirdBlob", func(t *testing.T) {
		// Index 8-9 should be in blob 2
		value, ok := blobSet.ValueAt(metricID, 8)
		require.True(t, ok)
		require.Equal(t, 30.0, value)

		value, ok = blobSet.ValueAt(metricID, 9)
		require.True(t, ok)
		require.Equal(t, 31.0, value)
	})

	t.Run("BoundaryBetweenBlobs", func(t *testing.T) {
		// Test transitions between blobs
		// Last of blob 0
		value, ok := blobSet.ValueAt(metricID, 2)
		require.True(t, ok)
		require.Equal(t, 12.0, value)

		// First of blob 1
		value, ok = blobSet.ValueAt(metricID, 3)
		require.True(t, ok)
		require.Equal(t, 20.0, value)

		// Last of blob 1
		value, ok = blobSet.ValueAt(metricID, 7)
		require.True(t, ok)
		require.Equal(t, 24.0, value)

		// First of blob 2
		value, ok = blobSet.ValueAt(metricID, 8)
		require.True(t, ok)
		require.Equal(t, 30.0, value)
	})

	t.Run("NegativeIndex", func(t *testing.T) {
		value, ok := blobSet.ValueAt(metricID, -1)
		require.False(t, ok)
		require.Equal(t, float64(0), value)
	})

	t.Run("OutOfBoundsIndex", func(t *testing.T) {
		// Total points = 3 + 5 + 2 = 10 (indices 0-9)
		value, ok := blobSet.ValueAt(metricID, 10)
		require.False(t, ok)
		require.Equal(t, float64(0), value)

		value, ok = blobSet.ValueAt(metricID, 100)
		require.False(t, ok)
		require.Equal(t, float64(0), value)
	})

	t.Run("NonExistentMetric", func(t *testing.T) {
		nonExistentID := uint64(99999999)
		value, ok := blobSet.ValueAt(nonExistentID, 0)
		require.False(t, ok)
		require.Equal(t, float64(0), value)
	})

	t.Run("EmptyBlobSet", func(t *testing.T) {
		emptySet := &NumericBlobSet{blobs: []NumericBlob{}}
		value, ok := emptySet.ValueAt(metricID, 0)
		require.False(t, ok)
		require.Equal(t, float64(0), value)
	})
}

func TestNumericBlobSet_TimestampAt(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Create 3 blobs with different number of points
	// Blob 0: 3 points (indices 0-2)
	ts1 := []int64{
		startTime.UnixMicro(),
		startTime.Add(1 * time.Minute).UnixMicro(),
		startTime.Add(2 * time.Minute).UnixMicro(),
	}
	val1 := []float64{10.0, 11.0, 12.0}
	blob1 := createBlobWithTimestamp(t, startTime, metricName, ts1, val1)

	// Blob 1: 5 points (indices 3-7)
	startTime2 := startTime.Add(1 * time.Hour)
	ts2 := []int64{
		startTime2.UnixMicro(),
		startTime2.Add(1 * time.Minute).UnixMicro(),
		startTime2.Add(2 * time.Minute).UnixMicro(),
		startTime2.Add(3 * time.Minute).UnixMicro(),
		startTime2.Add(4 * time.Minute).UnixMicro(),
	}
	val2 := []float64{20.0, 21.0, 22.0, 23.0, 24.0}
	blob2 := createBlobWithTimestamp(t, startTime2, metricName, ts2, val2)

	// Blob 2: 2 points (indices 8-9)
	startTime3 := startTime.Add(2 * time.Hour)
	ts3 := []int64{
		startTime3.UnixMicro(),
		startTime3.Add(1 * time.Minute).UnixMicro(),
	}
	val3 := []float64{30.0, 31.0}
	blob3 := createBlobWithTimestamp(t, startTime3, metricName, ts3, val3)

	blobSet, err := NewNumericBlobSet([]NumericBlob{blob1, blob2, blob3})
	require.NoError(t, err)

	t.Run("FirstBlob", func(t *testing.T) {
		// Index 0-2 should be in blob 0
		ts, ok := blobSet.TimestampAt(metricID, 0)
		require.True(t, ok)
		require.Equal(t, ts1[0], ts)

		ts, ok = blobSet.TimestampAt(metricID, 1)
		require.True(t, ok)
		require.Equal(t, ts1[1], ts)

		ts, ok = blobSet.TimestampAt(metricID, 2)
		require.True(t, ok)
		require.Equal(t, ts1[2], ts)
	})

	t.Run("SecondBlob", func(t *testing.T) {
		// Index 3-7 should be in blob 1
		ts, ok := blobSet.TimestampAt(metricID, 3)
		require.True(t, ok)
		require.Equal(t, ts2[0], ts)

		ts, ok = blobSet.TimestampAt(metricID, 4)
		require.True(t, ok)
		require.Equal(t, ts2[1], ts)

		ts, ok = blobSet.TimestampAt(metricID, 7)
		require.True(t, ok)
		require.Equal(t, ts2[4], ts)
	})

	t.Run("ThirdBlob", func(t *testing.T) {
		// Index 8-9 should be in blob 2
		ts, ok := blobSet.TimestampAt(metricID, 8)
		require.True(t, ok)
		require.Equal(t, ts3[0], ts)

		ts, ok = blobSet.TimestampAt(metricID, 9)
		require.True(t, ok)
		require.Equal(t, ts3[1], ts)
	})

	t.Run("BoundaryBetweenBlobs", func(t *testing.T) {
		// Test transitions between blobs
		// Last of blob 0
		ts, ok := blobSet.TimestampAt(metricID, 2)
		require.True(t, ok)
		require.Equal(t, ts1[2], ts)

		// First of blob 1
		ts, ok = blobSet.TimestampAt(metricID, 3)
		require.True(t, ok)
		require.Equal(t, ts2[0], ts)

		// Last of blob 1
		ts, ok = blobSet.TimestampAt(metricID, 7)
		require.True(t, ok)
		require.Equal(t, ts2[4], ts)

		// First of blob 2
		ts, ok = blobSet.TimestampAt(metricID, 8)
		require.True(t, ok)
		require.Equal(t, ts3[0], ts)
	})

	t.Run("NegativeIndex", func(t *testing.T) {
		ts, ok := blobSet.TimestampAt(metricID, -1)
		require.False(t, ok)
		require.Equal(t, int64(0), ts)
	})

	t.Run("OutOfBoundsIndex", func(t *testing.T) {
		// Total points = 3 + 5 + 2 = 10 (indices 0-9)
		ts, ok := blobSet.TimestampAt(metricID, 10)
		require.False(t, ok)
		require.Equal(t, int64(0), ts)

		ts, ok = blobSet.TimestampAt(metricID, 100)
		require.False(t, ok)
		require.Equal(t, int64(0), ts)
	})

	t.Run("NonExistentMetric", func(t *testing.T) {
		nonExistentID := uint64(99999999)
		ts, ok := blobSet.TimestampAt(nonExistentID, 0)
		require.False(t, ok)
		require.Equal(t, int64(0), ts)
	})

	t.Run("EmptyBlobSet", func(t *testing.T) {
		emptySet := &NumericBlobSet{blobs: []NumericBlob{}}
		ts, ok := emptySet.TimestampAt(metricID, 0)
		require.False(t, ok)
		require.Equal(t, int64(0), ts)
	})
}

func TestNumericBlobSet_SparseData_At(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metric1Name := "metric1"
	metric1ID := hash.ID(metric1Name)
	metric2Name := "metric2"

	// Blob 0: has metric1 (3 points)
	ts1 := []int64{startTime.UnixMicro(), startTime.Add(1 * time.Minute).UnixMicro(), startTime.Add(2 * time.Minute).UnixMicro()}
	val1 := []float64{10.0, 11.0, 12.0}
	blob1 := createBlobWithTimestamp(t, startTime, metric1Name, ts1, val1)

	// Blob 1: has metric2 (NOT metric1)
	startTime2 := startTime.Add(1 * time.Hour)
	ts2 := []int64{startTime2.UnixMicro(), startTime2.Add(1 * time.Minute).UnixMicro()}
	val2 := []float64{20.0, 21.0}
	blob2 := createBlobWithTimestamp(t, startTime2, metric2Name, ts2, val2)

	// Blob 2: has metric1 again (2 points, so indices 3-4 for metric1)
	startTime3 := startTime.Add(2 * time.Hour)
	ts3 := []int64{startTime3.UnixMicro(), startTime3.Add(1 * time.Minute).UnixMicro()}
	val3 := []float64{30.0, 31.0}
	blob3 := createBlobWithTimestamp(t, startTime3, metric1Name, ts3, val3)

	blobSet, err := NewNumericBlobSet([]NumericBlob{blob1, blob2, blob3})
	require.NoError(t, err)

	t.Run("SparseMetric_ValueAt", func(t *testing.T) {
		// metric1 exists in blob 0 (3 points) and blob 2 (2 points)
		// blob 1 is skipped since it doesn't have metric1

		// Indices 0-2 from blob 0
		value, ok := blobSet.ValueAt(metric1ID, 0)
		require.True(t, ok)
		require.Equal(t, 10.0, value)

		value, ok = blobSet.ValueAt(metric1ID, 2)
		require.True(t, ok)
		require.Equal(t, 12.0, value)

		// Indices 3-4 from blob 2 (blob 1 was skipped)
		value, ok = blobSet.ValueAt(metric1ID, 3)
		require.True(t, ok)
		require.Equal(t, 30.0, value)

		value, ok = blobSet.ValueAt(metric1ID, 4)
		require.True(t, ok)
		require.Equal(t, 31.0, value)

		// Index 5 is out of bounds (only 5 total points for metric1)
		_, ok = blobSet.ValueAt(metric1ID, 5)
		require.False(t, ok)
	})

	t.Run("SparseMetric_TimestampAt", func(t *testing.T) {
		// Indices 0-2 from blob 0
		ts, ok := blobSet.TimestampAt(metric1ID, 0)
		require.True(t, ok)
		require.Equal(t, ts1[0], ts)

		ts, ok = blobSet.TimestampAt(metric1ID, 2)
		require.True(t, ok)
		require.Equal(t, ts1[2], ts)

		// Indices 3-4 from blob 2 (blob 1 was skipped)
		ts, ok = blobSet.TimestampAt(metric1ID, 3)
		require.True(t, ok)
		require.Equal(t, ts3[0], ts)

		ts, ok = blobSet.TimestampAt(metric1ID, 4)
		require.True(t, ok)
		require.Equal(t, ts3[1], ts)
	})
}

// ==============================================================================
// NumericBlobSet Tests
// ==============================================================================

func TestNewNumericBlobSet(t *testing.T) {
	t.Run("ValidBlobs", func(t *testing.T) {
		blobs := createTestBlobs(t, 3)

		blobSet, err := NewNumericBlobSet(blobs)
		require.NoError(t, err)
		require.Equal(t, 3, blobSet.Len())
	})

	t.Run("EmptyBlobs", func(t *testing.T) {
		blobSet, err := NewNumericBlobSet([]NumericBlob{})
		require.Error(t, err)
		require.Equal(t, NumericBlobSet{}, blobSet)
		require.Contains(t, err.Error(), "empty blobs")
	})

	t.Run("SortsByStartTime", func(t *testing.T) {
		// Create blobs with out-of-order start times
		blob1 := createBlobWithTimestamp(t, time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC), "metric1", []int64{1, 2}, []float64{1.0, 2.0})
		blob2 := createBlobWithTimestamp(t, time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC), "metric1", []int64{3, 4}, []float64{3.0, 4.0})
		blob3 := createBlobWithTimestamp(t, time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC), "metric1", []int64{5, 6}, []float64{5.0, 6.0})

		blobSet, err := NewNumericBlobSet([]NumericBlob{blob1, blob2, blob3})
		require.NoError(t, err)

		// Verify blobs are sorted by start time
		start, end := blobSet.TimeRange()
		require.Equal(t, time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC), start)
		require.Equal(t, time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC), end)
	})
}

func TestNumericBlobSet_All(t *testing.T) {
	t.Run("SingleBlob", func(t *testing.T) {
		startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		metricName := "test.metric"
		metricID := hash.ID(metricName)

		timestamps := []int64{
			startTime.UnixMicro(),
			startTime.Add(1 * time.Minute).UnixMicro(),
			startTime.Add(2 * time.Minute).UnixMicro(),
		}
		values := []float64{1.0, 2.0, 3.0}

		blob := createBlobWithTimestamp(t, startTime, metricName, timestamps, values)
		blobSet, err := NewNumericBlobSet([]NumericBlob{blob})
		require.NoError(t, err)

		// Collect all pairs
		var gotTimestamps []int64
		var gotValues []float64
		for _, dp := range blobSet.All(metricID) {
			gotTimestamps = append(gotTimestamps, dp.Ts)
			gotValues = append(gotValues, dp.Val)
		}

		require.Equal(t, timestamps, gotTimestamps)
		require.Equal(t, values, gotValues)
	})

	t.Run("MultipleBlobs_SameMetric", func(t *testing.T) {
		startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		metricName := "test.metric"
		metricID := hash.ID(metricName)

		// Blob 1: 00:00-00:02 (3 points)
		ts1 := []int64{
			startTime.UnixMicro(),
			startTime.Add(1 * time.Minute).UnixMicro(),
			startTime.Add(2 * time.Minute).UnixMicro(),
		}
		val1 := []float64{1.0, 2.0, 3.0}
		blob1 := createBlobWithTimestamp(t, startTime, metricName, ts1, val1)

		// Blob 2: 01:00-01:02 (3 points)
		startTime2 := startTime.Add(1 * time.Hour)
		ts2 := []int64{
			startTime2.UnixMicro(),
			startTime2.Add(1 * time.Minute).UnixMicro(),
			startTime2.Add(2 * time.Minute).UnixMicro(),
		}
		val2 := []float64{10.0, 20.0, 30.0}
		blob2 := createBlobWithTimestamp(t, startTime2, metricName, ts2, val2)

		// Blob 3: 02:00-02:02 (3 points)
		startTime3 := startTime.Add(2 * time.Hour)
		ts3 := []int64{
			startTime3.UnixMicro(),
			startTime3.Add(1 * time.Minute).UnixMicro(),
			startTime3.Add(2 * time.Minute).UnixMicro(),
		}
		val3 := []float64{100.0, 200.0, 300.0}
		blob3 := createBlobWithTimestamp(t, startTime3, metricName, ts3, val3)

		blobSet, err := NewNumericBlobSet([]NumericBlob{blob1, blob2, blob3})
		require.NoError(t, err)

		// Collect all pairs
		var gotTimestamps []int64
		var gotValues []float64
		for _, dp := range blobSet.All(metricID) {
			gotTimestamps = append(gotTimestamps, dp.Ts)
			gotValues = append(gotValues, dp.Val)
		}

		// Should have all 9 points in chronological order
		expectedTimestamps := append(append(ts1, ts2...), ts3...)
		expectedValues := append(append(val1, val2...), val3...)

		require.Equal(t, expectedTimestamps, gotTimestamps)
		require.Equal(t, expectedValues, gotValues)
	})

	t.Run("MetricNotInSomeBlobs", func(t *testing.T) {
		startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		metric1Name := "metric1"
		metric1ID := hash.ID(metric1Name)
		metric2Name := "metric2"

		// Blob 1: has metric1
		ts1 := []int64{startTime.UnixMicro(), startTime.Add(1 * time.Minute).UnixMicro()}
		val1 := []float64{1.0, 2.0}
		blob1 := createBlobWithTimestamp(t, startTime, metric1Name, ts1, val1)

		// Blob 2: has metric2 (not metric1)
		startTime2 := startTime.Add(1 * time.Hour)
		ts2 := []int64{startTime2.UnixMicro(), startTime2.Add(1 * time.Minute).UnixMicro()}
		val2 := []float64{10.0, 20.0}
		blob2 := createBlobWithTimestamp(t, startTime2, metric2Name, ts2, val2)

		// Blob 3: has metric1 again
		startTime3 := startTime.Add(2 * time.Hour)
		ts3 := []int64{startTime3.UnixMicro(), startTime3.Add(1 * time.Minute).UnixMicro()}
		val3 := []float64{100.0, 200.0}
		blob3 := createBlobWithTimestamp(t, startTime3, metric1Name, ts3, val3)

		blobSet, err := NewNumericBlobSet([]NumericBlob{blob1, blob2, blob3})
		require.NoError(t, err)

		// Query metric1 - should get data from blob1 and blob3, skip blob2
		var gotTimestamps []int64
		var gotValues []float64
		for _, dp := range blobSet.All(metric1ID) {
			gotTimestamps = append(gotTimestamps, dp.Ts)
			gotValues = append(gotValues, dp.Val)
		}

		// Should have 4 points (2 from blob1, 2 from blob3)
		expectedTimestamps := make([]int64, 0, len(ts1)+len(ts3))
		expectedTimestamps = append(expectedTimestamps, ts1...)
		expectedTimestamps = append(expectedTimestamps, ts3...)

		expectedValues := make([]float64, 0, len(val1)+len(val3))
		expectedValues = append(expectedValues, val1...)
		expectedValues = append(expectedValues, val3...)

		require.Equal(t, expectedTimestamps, gotTimestamps)
		require.Equal(t, expectedValues, gotValues)
	})

	t.Run("EarlyTermination", func(t *testing.T) {
		blobs := createTestBlobs(t, 5)
		blobSet, err := NewNumericBlobSet(blobs)
		require.NoError(t, err)

		metricID := hash.ID("metric1")

		// Stop after first 3 points
		count := 0
		for range blobSet.All(metricID) {
			count++
			if count >= 3 {
				break
			}
		}

		require.Equal(t, 3, count)
	})

	t.Run("NonExistentMetric", func(t *testing.T) {
		blobs := createTestBlobs(t, 3)
		blobSet, err := NewNumericBlobSet(blobs)
		require.NoError(t, err)

		nonExistentID := uint64(99999999)

		count := 0
		for range blobSet.All(nonExistentID) {
			count++
		}

		require.Equal(t, 0, count)
	})
}

func TestNumericBlobSet_AllTimestamps(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Create 3 blobs with different timestamps
	ts1 := []int64{startTime.UnixMicro(), startTime.Add(1 * time.Minute).UnixMicro()}
	val1 := []float64{1.0, 2.0}
	blob1 := createBlobWithTimestamp(t, startTime, metricName, ts1, val1)

	startTime2 := startTime.Add(1 * time.Hour)
	ts2 := []int64{startTime2.UnixMicro(), startTime2.Add(1 * time.Minute).UnixMicro()}
	val2 := []float64{10.0, 20.0}
	blob2 := createBlobWithTimestamp(t, startTime2, metricName, ts2, val2)

	startTime3 := startTime.Add(2 * time.Hour)
	ts3 := []int64{startTime3.UnixMicro(), startTime3.Add(1 * time.Minute).UnixMicro()}
	val3 := []float64{100.0, 200.0}
	blob3 := createBlobWithTimestamp(t, startTime3, metricName, ts3, val3)

	blobSet, err := NewNumericBlobSet([]NumericBlob{blob1, blob2, blob3})
	require.NoError(t, err)

	// Collect all timestamps
	gotTimestamps := make([]int64, 0, 6) // 2 points per blob × 3 blobs
	for ts := range blobSet.AllTimestamps(metricID) {
		gotTimestamps = append(gotTimestamps, ts)
	}

	expectedTimestamps := make([]int64, 0, len(ts1)+len(ts2)+len(ts3))
	expectedTimestamps = append(expectedTimestamps, ts1...)
	expectedTimestamps = append(expectedTimestamps, ts2...)
	expectedTimestamps = append(expectedTimestamps, ts3...)
	require.Equal(t, expectedTimestamps, gotTimestamps)
}

func TestNumericBlobSet_AllValues(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Create 3 blobs with different values
	ts1 := []int64{startTime.UnixMicro(), startTime.Add(1 * time.Minute).UnixMicro()}
	val1 := []float64{1.0, 2.0}
	blob1 := createBlobWithTimestamp(t, startTime, metricName, ts1, val1)

	startTime2 := startTime.Add(1 * time.Hour)
	ts2 := []int64{startTime2.UnixMicro(), startTime2.Add(1 * time.Minute).UnixMicro()}
	val2 := []float64{10.0, 20.0}
	blob2 := createBlobWithTimestamp(t, startTime2, metricName, ts2, val2)

	startTime3 := startTime.Add(2 * time.Hour)
	ts3 := []int64{startTime3.UnixMicro(), startTime3.Add(1 * time.Minute).UnixMicro()}
	val3 := []float64{100.0, 200.0}
	blob3 := createBlobWithTimestamp(t, startTime3, metricName, ts3, val3)

	blobSet, err := NewNumericBlobSet([]NumericBlob{blob1, blob2, blob3})
	require.NoError(t, err)

	// Collect all values
	gotValues := make([]float64, 0, 6) // 2 points per blob × 3 blobs
	for val := range blobSet.AllValues(metricID) {
		gotValues = append(gotValues, val)
	}

	expectedValues := make([]float64, 0, len(val1)+len(val2)+len(val3))
	expectedValues = append(expectedValues, val1...)
	expectedValues = append(expectedValues, val2...)
	expectedValues = append(expectedValues, val3...)
	require.Equal(t, expectedValues, gotValues)
}

func TestNumericBlobSet_Len(t *testing.T) {
	t.Run("SingleBlob", func(t *testing.T) {
		blobs := createTestBlobs(t, 1)
		blobSet, err := NewNumericBlobSet(blobs)
		require.NoError(t, err)
		require.Equal(t, 1, blobSet.Len())
	})

	t.Run("MultipleBlobs", func(t *testing.T) {
		blobs := createTestBlobs(t, 10)
		blobSet, err := NewNumericBlobSet(blobs)
		require.NoError(t, err)
		require.Equal(t, 10, blobSet.Len())
	})
}

func TestNumericBlobSet_TimeRange(t *testing.T) {
	startTime1 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	startTime2 := time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC)
	startTime3 := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	blob1 := createBlobWithTimestamp(t, startTime1, "metric1", []int64{1, 2}, []float64{1.0, 2.0})
	blob2 := createBlobWithTimestamp(t, startTime2, "metric1", []int64{3, 4}, []float64{3.0, 4.0})
	blob3 := createBlobWithTimestamp(t, startTime3, "metric1", []int64{5, 6}, []float64{5.0, 6.0})

	// Create with out-of-order blobs to test sorting
	blobSet, err := NewNumericBlobSet([]NumericBlob{blob3, blob1, blob2})
	require.NoError(t, err)

	start, end := blobSet.TimeRange()
	require.Equal(t, startTime1, start)
	require.Equal(t, startTime3, end)
}

func TestNumericBlobSet_BlobAt(t *testing.T) {
	blobs := createTestBlobs(t, 3)
	blobSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)

	t.Run("ValidIndex", func(t *testing.T) {
		blob := blobSet.BlobAt(0)
		require.NotNil(t, blob)

		blob = blobSet.BlobAt(1)
		require.NotNil(t, blob)

		blob = blobSet.BlobAt(2)
		require.NotNil(t, blob)
	})

	t.Run("InvalidIndex", func(t *testing.T) {
		blob := blobSet.BlobAt(-1)
		require.Nil(t, blob)

		blob = blobSet.BlobAt(3)
		require.Nil(t, blob)

		blob = blobSet.BlobAt(100)
		require.Nil(t, blob)
	})
}

func TestNumericBlobSet_Blobs(t *testing.T) {
	blobs := createTestBlobs(t, 5)
	blobSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)

	result := blobSet.Blobs()
	require.Len(t, result, 5)

	// Verify it's a copy (modifying result doesn't affect internal state)
	result[0] = NumericBlob{}
	require.Equal(t, 5, blobSet.Len())
}

// TestNumericBlobSet_MetricLen tests MetricLen and MetricLenByName methods
func TestNumericBlobSet_MetricLen(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create blobs with different metrics and counts
	blobs := make([]NumericBlob, 4)

	// Blob 0: metric1 with 3 points
	metricID1 := hash.ID("metric1")
	encoder, err := NewNumericEncoder(startTime, WithTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID1, 3))
	for i := range 3 {
		ts := startTime.Add(time.Duration(i) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, float64(i), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err := encoder.Finish()
	require.NoError(t, err)
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blobs[0], err = decoder.Decode()
	require.NoError(t, err)

	// Blob 1: metric1 with 5 points (same metric, different blob)
	encoder, err = NewNumericEncoder(startTime.Add(time.Hour), WithTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID1, 5))
	for i := range 5 {
		ts := startTime.Add(time.Hour).Add(time.Duration(i) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, float64(i+10), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err = encoder.Finish()
	require.NoError(t, err)
	decoder, err = NewNumericDecoder(data)
	require.NoError(t, err)
	blobs[1], err = decoder.Decode()
	require.NoError(t, err)

	// Blob 2: metric2 with 2 points
	metricID2 := hash.ID("metric2")
	encoder, err = NewNumericEncoder(startTime.Add(2*time.Hour), WithTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID2, 2))
	for i := range 2 {
		ts := startTime.Add(2 * time.Hour).Add(time.Duration(i) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, float64(i+20), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err = encoder.Finish()
	require.NoError(t, err)
	decoder, err = NewNumericDecoder(data)
	require.NoError(t, err)
	blobs[2], err = decoder.Decode()
	require.NoError(t, err)

	// Blob 3: metric1 with 4 points (same metric, third blob)
	encoder, err = NewNumericEncoder(startTime.Add(3*time.Hour), WithTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID1, 4))
	for i := range 4 {
		ts := startTime.Add(3 * time.Hour).Add(time.Duration(i) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, float64(i+30), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err = encoder.Finish()
	require.NoError(t, err)
	decoder, err = NewNumericDecoder(data)
	require.NoError(t, err)
	blobs[3], err = decoder.Decode()
	require.NoError(t, err)

	blobSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)

	t.Run("MetricLen sums across multiple blobs", func(t *testing.T) {
		// metric1 appears in blobs 0 (3 points), 1 (5 points), 3 (4 points) = 12 total
		require.Equal(t, 12, blobSet.MetricLen(metricID1))

		// metric2 appears only in blob 2 (2 points)
		require.Equal(t, 2, blobSet.MetricLen(metricID2))
	})

	t.Run("MetricLen for non-existent metric", func(t *testing.T) {
		require.Equal(t, 0, blobSet.MetricLen(hash.ID("non_existent")))
		require.Equal(t, 0, blobSet.MetricLen(99999))
	})

	t.Run("MetricLenByName", func(t *testing.T) {
		require.Equal(t, 12, blobSet.MetricLenByName("metric1"))
		require.Equal(t, 2, blobSet.MetricLenByName("metric2"))
		require.Equal(t, 0, blobSet.MetricLenByName("non_existent"))
	})

	t.Run("Empty BlobSet", func(t *testing.T) {
		emptyBlobs := []NumericBlob{}
		emptySet, err := NewNumericBlobSet(emptyBlobs)
		require.Error(t, err)
		require.Equal(t, 0, emptySet.MetricLen(metricID1))
	})
}

// TestNumericBlobSet_MetricDuration tests MetricDuration and MetricDurationByName methods
func TestNumericBlobSet_MetricDuration(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create blobs with known time spans
	blobs := make([]NumericBlob, 3)
	metricID1 := hash.ID("metric1")
	metricID2 := hash.ID("metric2")

	// Blob 0: metric1, timestamps 0, 10, 20 minutes
	encoder, err := NewNumericEncoder(startTime, WithTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID1, 3))
	for i := range 3 {
		ts := startTime.Add(time.Duration(i*10) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, float64(i), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err := encoder.Finish()
	require.NoError(t, err)
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blobs[0], err = decoder.Decode()
	require.NoError(t, err)

	// Blob 1: metric1, timestamps 1h0m, 1h15m, 1h30m
	encoder, err = NewNumericEncoder(startTime.Add(time.Hour), WithTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID1, 3))
	for i := range 3 {
		ts := startTime.Add(time.Hour).Add(time.Duration(i*15) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, float64(i+10), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err = encoder.Finish()
	require.NoError(t, err)
	decoder, err = NewNumericDecoder(data)
	require.NoError(t, err)
	blobs[1], err = decoder.Decode()
	require.NoError(t, err)

	// Blob 2: metric2, timestamps 2h0m, 2h5m
	encoder, err = NewNumericEncoder(startTime.Add(2*time.Hour), WithTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)
	require.NoError(t, encoder.StartMetricID(metricID2, 2))
	for i := range 2 {
		ts := startTime.Add(2 * time.Hour).Add(time.Duration(i*5) * time.Minute).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, float64(i+20), ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err = encoder.Finish()
	require.NoError(t, err)
	decoder, err = NewNumericDecoder(data)
	require.NoError(t, err)
	blobs[2], err = decoder.Decode()
	require.NoError(t, err)

	blobSet, err := NewNumericBlobSet(blobs)
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
		duration := blobSet.MetricDuration(hash.ID("non_existent"))
		require.Equal(t, int64(0), duration)
	})

	t.Run("MetricDurationByName", func(t *testing.T) {
		duration := blobSet.MetricDurationByName("metric1")
		require.Equal(t, int64(90*time.Minute/time.Microsecond), duration)

		duration = blobSet.MetricDurationByName("metric2")
		require.Equal(t, int64(5*time.Minute/time.Microsecond), duration)

		duration = blobSet.MetricDurationByName("non_existent")
		require.Equal(t, int64(0), duration)
	})

	t.Run("Single data point", func(t *testing.T) {
		// Create blob with single point
		singleEncoder, err := NewNumericEncoder(startTime, WithTimestampEncoding(format.TypeDelta))
		require.NoError(t, err)
		singleID := hash.ID("single")
		require.NoError(t, singleEncoder.StartMetricID(singleID, 1))
		require.NoError(t, singleEncoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
		require.NoError(t, singleEncoder.EndMetric())
		singleData, err := singleEncoder.Finish()
		require.NoError(t, err)
		singleDecoder, err := NewNumericDecoder(singleData)
		require.NoError(t, err)
		singleBlob, err := singleDecoder.Decode()
		require.NoError(t, err)

		singleSet, err := NewNumericBlobSet([]NumericBlob{singleBlob})
		require.NoError(t, err)

		duration := singleSet.MetricDuration(singleID)
		require.Equal(t, int64(0), duration, "Single point should have 0 duration")
	})
}
