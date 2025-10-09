package blob

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
)

// Helper function to create a test TextBlobSet with specified number of blobs
func createTestTextBlobSetForMaterialization(t *testing.T, numBlobs int, tsEnc format.EncodingType, withTags bool, metricsPerBlob map[uint64]int) TextBlobSet {
	t.Helper()

	blobs := make([]TextBlob, numBlobs)
	baseTime := time.Now()

	for blobIdx := range numBlobs {
		startTime := baseTime.Add(time.Duration(blobIdx) * time.Hour)
		opts := []TextEncoderOption{
			WithTextTimestampEncoding(tsEnc),
		}
		if withTags {
			opts = append(opts, WithTextTagsEnabled(true))
		}

		encoder, err := NewTextEncoder(startTime, opts...)
		require.NoError(t, err)

		for metricID, count := range metricsPerBlob {
			err = encoder.StartMetricID(metricID, count)
			require.NoError(t, err)

			for i := range count {
				// Offset timestamps and values by blob index to make them unique across blobs
				ts := int64(blobIdx*1000000 + i*1000)
				val := "value_" + string(rune('A'+blobIdx)) + "_" + string(rune('0'+i%10))
				tag := ""
				if withTags {
					tag = "tag" + string(rune('A'+i%3))
				}
				err = encoder.AddDataPoint(ts, val, tag)
				require.NoError(t, err)
			}

			err = encoder.EndMetric()
			require.NoError(t, err)
		}

		blobBytes, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewTextDecoder(blobBytes)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		blobs[blobIdx] = blob
	}

	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	return blobSet
}

// TestMaterializedTextBlobSet_EmptyBlobSet tests behavior with non-existent metric
func TestMaterializedTextBlobSet_EmptyBlobSet(t *testing.T) {
	// Create blob set with one metric, but query for a different metric
	metricsPerBlob := map[uint64]int{
		100: 5,
	}
	blobSet := createTestTextBlobSetForMaterialization(t, 1, format.TypeRaw, false, metricsPerBlob)

	mat := blobSet.Materialize()

	// Query for non-existent metric
	nonExistentMetricID := uint64(999)
	_, ok := mat.ValueAt(nonExistentMetricID, 0)
	require.False(t, ok, "should return false for non-existent metric")

	_, ok = mat.TimestampAt(nonExistentMetricID, 0)
	require.False(t, ok, "should return false for non-existent metric")

	require.False(t, mat.HasMetricID(nonExistentMetricID))
	require.Equal(t, 0, mat.DataPointCount(nonExistentMetricID))
}

// TestMaterializedTextBlobSet_SingleBlob tests materialization with a single blob
func TestMaterializedTextBlobSet_SingleBlob(t *testing.T) {
	metricID := uint64(100)
	metricsPerBlob := map[uint64]int{
		metricID: 10,
	}

	blobSet := createTestTextBlobSetForMaterialization(t, 1, format.TypeRaw, false, metricsPerBlob)
	mat := blobSet.Materialize()

	// Verify data point count
	require.Equal(t, 10, mat.DataPointCount(metricID))

	// Verify all values and timestamps
	for i := range 10 {
		val, ok := mat.ValueAt(metricID, i)
		require.True(t, ok)
		expectedVal := "value_A_" + string(rune('0'+i))
		require.Equal(t, expectedVal, val)

		ts, ok := mat.TimestampAt(metricID, i)
		require.True(t, ok)
		expectedTs := int64(i * 1000)
		require.Equal(t, expectedTs, ts)
	}
}

// TestMaterializedTextBlobSet_MultipleBlobs_SameMetric tests with same metric across blobs
func TestMaterializedTextBlobSet_MultipleBlobs_SameMetric(t *testing.T) {
	metricID := uint64(100)
	pointsPerBlob := 100
	numBlobs := 3

	metricsPerBlob := map[uint64]int{
		metricID: pointsPerBlob,
	}

	blobSet := createTestTextBlobSetForMaterialization(t, numBlobs, format.TypeRaw, false, metricsPerBlob)
	mat := blobSet.Materialize()

	// Total points should be numBlobs * pointsPerBlob
	totalPoints := numBlobs * pointsPerBlob
	require.Equal(t, totalPoints, mat.DataPointCount(metricID))

	// Verify first point from first blob
	val, ok := mat.ValueAt(metricID, 0)
	require.True(t, ok)
	require.Equal(t, "value_A_0", val)

	// Verify first point from second blob (at index 100)
	val, ok = mat.ValueAt(metricID, 100)
	require.True(t, ok)
	require.Equal(t, "value_B_0", val)

	// Verify first point from third blob (at index 200)
	val, ok = mat.ValueAt(metricID, 200)
	require.True(t, ok)
	require.Equal(t, "value_C_0", val)

	// Verify last point
	val, ok = mat.ValueAt(metricID, totalPoints-1)
	require.True(t, ok)
	require.Equal(t, "value_C_9", val) // Last point (index 99 % 10 = 9)
}

// TestMaterializedTextBlobSet_MultipleBlobs_SparseMetrics tests sparse metric distribution
func TestMaterializedTextBlobSet_MultipleBlobs_SparseMetrics(t *testing.T) {
	// Different metrics per blob to test sparse distribution
	blobSet := createTestTextBlobSetForMaterialization(t, 3, format.TypeRaw, false, map[uint64]int{100: 10})
	blob2 := createTestTextBlobSetForMaterialization(t, 1, format.TypeRaw, false, map[uint64]int{200: 10})
	blob3 := createTestTextBlobSetForMaterialization(t, 1, format.TypeRaw, false, map[uint64]int{300: 10})

	// Combine blobs
	allBlobs := make([]TextBlob, 0, len(blobSet.blobs)+len(blob2.blobs)+len(blob3.blobs))
	allBlobs = append(allBlobs, blobSet.blobs...)
	allBlobs = append(allBlobs, blob2.blobs...)
	allBlobs = append(allBlobs, blob3.blobs...)
	combinedSet, err := NewTextBlobSet(allBlobs)
	require.NoError(t, err)

	mat := combinedSet.Materialize()

	// Should have 3 unique metrics
	require.Equal(t, 3, mat.MetricCount())

	// Each metric should have its own data
	require.Equal(t, 30, mat.DataPointCount(100)) // 3 blobs
	require.Equal(t, 10, mat.DataPointCount(200)) // 1 blob
	require.Equal(t, 10, mat.DataPointCount(300)) // 1 blob
}

// TestMaterializedTextBlobSet_WithTags tests tag support
func TestMaterializedTextBlobSet_WithTags(t *testing.T) {
	metricID := uint64(100)
	metricsPerBlob := map[uint64]int{
		metricID: 10,
	}

	blobSet := createTestTextBlobSetForMaterialization(t, 2, format.TypeRaw, true, metricsPerBlob)
	mat := blobSet.Materialize()

	// Verify tags are present
	for i := range 20 {
		tag, ok := mat.TagAt(metricID, i)
		require.True(t, ok)

		// Tag resets per blob
		localIdx := i % 10
		expectedTag := "tag" + string(rune('A'+localIdx%3))
		require.Equal(t, expectedTag, tag, "mismatch at index %d", i)
	}
}

// TestMaterializedTextBlobSet_DifferentEncodings tests different timestamp encodings
func TestMaterializedTextBlobSet_DifferentEncodings(t *testing.T) {
	metricID := uint64(100)
	metricsPerBlob := map[uint64]int{
		metricID: 10,
	}

	tests := []struct {
		name  string
		tsEnc format.EncodingType
	}{
		{"Raw", format.TypeRaw},
		{"Delta", format.TypeDelta},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blobSet := createTestTextBlobSetForMaterialization(t, 2, tt.tsEnc, false, metricsPerBlob)
			mat := blobSet.Materialize()

			// Verify all 20 data points are accessible
			require.Equal(t, 20, mat.DataPointCount(metricID))

			for i := range 20 {
				_, ok := mat.ValueAt(metricID, i)
				require.True(t, ok, "failed at index %d with encoding %s", i, tt.name)
			}
		})
	}
}

// TestMaterializedTextBlobSet_OutOfBounds tests boundary conditions
func TestMaterializedTextBlobSet_OutOfBounds(t *testing.T) {
	metricID := uint64(100)
	metricsPerBlob := map[uint64]int{
		metricID: 10,
	}

	blobSet := createTestTextBlobSetForMaterialization(t, 1, format.TypeRaw, false, metricsPerBlob)
	mat := blobSet.Materialize()

	// Test negative index
	_, ok := mat.ValueAt(metricID, -1)
	require.False(t, ok)

	// Test at boundary (should work)
	_, ok = mat.ValueAt(metricID, 9)
	require.True(t, ok)

	// Test beyond boundary
	_, ok = mat.ValueAt(metricID, 10)
	require.False(t, ok)
}

// TestMaterializedTextBlobSet_MetadataMethods tests metadata accessor methods
func TestMaterializedTextBlobSet_MetadataMethods(t *testing.T) {
	metricsPerBlob := map[uint64]int{
		100: 10,
		200: 20,
	}

	blobSet := createTestTextBlobSetForMaterialization(t, 2, format.TypeRaw, false, metricsPerBlob)
	mat := blobSet.Materialize()

	// Test MetricCount
	require.Equal(t, 2, mat.MetricCount())

	// Test MetricIDs
	metricIDs := mat.MetricIDs()
	require.Len(t, metricIDs, 2)
	require.Contains(t, metricIDs, uint64(100))
	require.Contains(t, metricIDs, uint64(200))

	// Test HasMetricID
	require.True(t, mat.HasMetricID(100))
	require.True(t, mat.HasMetricID(200))
	require.False(t, mat.HasMetricID(300))

	// Test DataPointCount
	require.Equal(t, 20, mat.DataPointCount(100)) // 2 blobs × 10 points
	require.Equal(t, 40, mat.DataPointCount(200)) // 2 blobs × 20 points
}

// TestMaterializedTextBlobSet_Correctness validates correctness against sequential iteration
func TestMaterializedTextBlobSet_Correctness(t *testing.T) {
	metricID := uint64(100)
	metricsPerBlob := map[uint64]int{
		metricID: 50,
	}

	encodings := []struct {
		name  string
		tsEnc format.EncodingType
	}{
		{"Raw", format.TypeRaw},
		{"Delta", format.TypeDelta},
	}

	for _, enc := range encodings {
		t.Run(enc.name, func(t *testing.T) {
			blobSet := createTestTextBlobSetForMaterialization(t, 3, enc.tsEnc, false, metricsPerBlob)
			mat := blobSet.Materialize()

			// Collect values via sequential iteration
			var seqValues []string
			var seqTimestamps []int64
			for _, point := range blobSet.All(metricID) {
				seqValues = append(seqValues, point.Val)
				seqTimestamps = append(seqTimestamps, point.Ts)
			}

			// Verify materialized matches sequential
			require.Equal(t, len(seqValues), mat.DataPointCount(metricID))

			for i := range seqValues {
				matVal, ok := mat.ValueAt(metricID, i)
				require.True(t, ok)
				require.Equal(t, seqValues[i], matVal, "value mismatch at index %d", i)

				matTs, ok := mat.TimestampAt(metricID, i)
				require.True(t, ok)
				require.Equal(t, seqTimestamps[i], matTs, "timestamp mismatch at index %d", i)
			}
		})
	}
}

// TestTextBlobSet_MaterializeMetric tests single metric materialization by ID
func TestTextBlobSet_MaterializeMetric(t *testing.T) {
	tests := []struct {
		name     string
		tsEnc    format.EncodingType
		withTags bool
	}{
		{
			name:     "delta encoding with tags",
			tsEnc:    format.TypeDelta,
			withTags: true,
		},
		{
			name:     "raw encoding without tags",
			tsEnc:    format.TypeRaw,
			withTags: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create blob set with 3 blobs, 2 metrics per blob
			metricID1 := uint64(100)
			metricID2 := uint64(200)
			metricsPerBlob := map[uint64]int{
				metricID1: 10,
				metricID2: 20,
			}

			blobSet := createTestTextBlobSetForMaterialization(t, 3, tt.tsEnc, tt.withTags, metricsPerBlob)

			// Materialize first metric
			metric, ok := blobSet.MaterializeMetric(metricID1)
			require.True(t, ok)
			require.Equal(t, metricID1, metric.MetricID)
			require.Len(t, metric.Timestamps, 30) // 3 blobs × 10 points
			require.Len(t, metric.Values, 30)

			if tt.withTags {
				require.Len(t, metric.Tags, 30)
			} else {
				require.Nil(t, metric.Tags)
			}

			// Verify O(1) access works
			val, ok := metric.ValueAt(0)
			require.True(t, ok)
			require.Equal(t, metric.Values[0], val)

			val, ok = metric.ValueAt(15)
			require.True(t, ok)
			require.Equal(t, metric.Values[15], val)

			// Materialize second metric
			metric2, ok := blobSet.MaterializeMetric(metricID2)
			require.True(t, ok)
			require.Equal(t, metricID2, metric2.MetricID)
			require.Len(t, metric2.Timestamps, 60) // 3 blobs × 20 points
			require.Len(t, metric2.Values, 60)

			// Test non-existent metric
			_, ok = blobSet.MaterializeMetric(999)
			require.False(t, ok)
		})
	}
}

// TestTextBlobSet_MaterializeMetricByName tests single metric materialization by name
func TestTextBlobSet_MaterializeMetricByName(t *testing.T) {
	// Create blob set with metric names
	baseTime := time.Now()
	blobs := make([]TextBlob, 2)

	for blobIdx := range 2 {
		startTime := baseTime.Add(time.Duration(blobIdx) * time.Hour)
		encoder, err := NewTextEncoder(
			startTime,
			WithTextTimestampEncoding(format.TypeDelta),
			WithTextTagsEnabled(false),
		)
		require.NoError(t, err)

		// Add metrics with names
		err = encoder.StartMetricName("system.load", 3)
		require.NoError(t, err)
		for range 3 {
			err = encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), "value", "")
			require.NoError(t, err)
			startTime = startTime.Add(time.Second)
		}
		require.NoError(t, encoder.EndMetric())

		err = encoder.StartMetricName("network.bytes", 2)
		require.NoError(t, err)
		for range 2 {
			err = encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), "data", "")
			require.NoError(t, err)
			startTime = startTime.Add(time.Second)
		}
		require.NoError(t, encoder.EndMetric())

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewTextDecoder(data)
		require.NoError(t, err)
		blob, err := decoder.Decode()
		require.NoError(t, err)
		blobs[blobIdx] = blob
	}

	blobSet, err := NewTextBlobSet(blobs)
	require.NoError(t, err)

	// Materialize by name
	metric, ok := blobSet.MaterializeMetricByName("system.load")
	require.True(t, ok)
	require.Len(t, metric.Values, 6) // 2 blobs × 3 points

	metric2, ok := blobSet.MaterializeMetricByName("network.bytes")
	require.True(t, ok)
	require.Len(t, metric2.Values, 4) // 2 blobs × 2 points

	// Non-existent metric name
	_, ok = blobSet.MaterializeMetricByName("nonexistent")
	require.False(t, ok)
}

// TestTextBlobSet_MaterializeMetric_EmptyBlobSet tests edge case with empty blob set
func TestTextBlobSet_MaterializeMetric_EmptyBlobSet(t *testing.T) {
	// Create blob set and try to materialize a metric that doesn't exist
	metricsPerBlob := map[uint64]int{
		uint64(100): 5,
	}

	blobSet := createTestTextBlobSetForMaterialization(t, 2, format.TypeDelta, false, metricsPerBlob)

	// Try to materialize a metric that doesn't exist
	_, ok := blobSet.MaterializeMetric(999)
	require.False(t, ok, "should return false for non-existent metric")
}

// TestTextBlobSet_MaterializeMetric_SingleBlob tests with just one blob
func TestTextBlobSet_MaterializeMetric_SingleBlob(t *testing.T) {
	metricID := uint64(100)
	metricsPerBlob := map[uint64]int{
		metricID: 10,
	}

	blobSet := createTestTextBlobSetForMaterialization(t, 1, format.TypeDelta, true, metricsPerBlob)

	metric, ok := blobSet.MaterializeMetric(metricID)
	require.True(t, ok)
	require.Len(t, metric.Timestamps, 10)
	require.Len(t, metric.Values, 10)
	require.Len(t, metric.Tags, 10)

	// Verify sequential access works
	for i := range 10 {
		val, ok := metric.ValueAt(i)
		require.True(t, ok)
		require.Equal(t, metric.Values[i], val)
	}
}
