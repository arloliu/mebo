package blob

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
)

// Helper function to create a test NumericBlobSet with specified number of blobs
func createTestBlobSetForMaterialization(t *testing.T, numBlobs int, tsEnc, valEnc format.EncodingType, withTags bool, metricsPerBlob map[uint64]int) NumericBlobSet {
	t.Helper()

	blobs := make([]NumericBlob, numBlobs)
	baseTime := time.Now()

	for blobIdx := range numBlobs {
		startTime := baseTime.Add(time.Duration(blobIdx) * time.Hour)
		opts := []NumericEncoderOption{
			WithTimestampEncoding(tsEnc),
			WithValueEncoding(valEnc),
		}
		if withTags {
			opts = append(opts, WithTagsEnabled(true))
		}

		encoder, err := NewNumericEncoder(startTime, opts...)
		require.NoError(t, err)

		for metricID, count := range metricsPerBlob {
			err = encoder.StartMetricID(metricID, count)
			require.NoError(t, err)

			for i := range count {
				// Offset timestamps and values by blob index to make them unique across blobs
				ts := int64(blobIdx*1000000 + i*1000)
				val := float64(metricID) + float64(blobIdx*1000) + float64(i)
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

		decoder, err := NewNumericDecoder(blobBytes)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		blobs[blobIdx] = blob
	}

	blobSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)

	return blobSet
}

// ==============================================================================
// MaterializedNumericBlobSet Tests - Empty BlobSet
// ==============================================================================

func TestMaterializedNumericBlobSet_EmptyBlobSet(t *testing.T) {
	// Create BlobSet with one blob that has a metric but no data points won't work
	// Instead, test with a valid blob set and verify it handles missing metrics correctly
	metricID := uint64(1234)
	blobSet := createTestBlobSetForMaterialization(t, 1, format.TypeRaw, format.TypeRaw, false, map[uint64]int{
		metricID: 10,
	})

	material := blobSet.Materialize()

	// Test non-existent metric (simulates empty behavior for that metric)
	nonExistentID := uint64(9999)
	require.Zero(t, material.DataPointCount(nonExistentID), "non-existent metric should return 0")
	require.False(t, material.HasMetricID(nonExistentID), "non-existent metric should not be found")
	require.False(t, material.HasMetricName("nonexistent"), "non-existent name should not be found")

	// Random access should return false for non-existent metric
	v, ok := material.ValueAt(nonExistentID, 0)
	require.False(t, ok)
	require.Zero(t, v)

	ts, ok := material.TimestampAt(nonExistentID, 0)
	require.False(t, ok)
	require.Zero(t, ts)

	tag, ok := material.TagAt(nonExistentID, 0)
	require.False(t, ok)
	require.Empty(t, tag)
}

// ==============================================================================
// MaterializedNumericBlobSet Tests - Single Blob
// ==============================================================================

func TestMaterializedNumericBlobSet_SingleBlob(t *testing.T) {
	metricID := uint64(1234)
	blobSet := createTestBlobSetForMaterialization(t, 1, format.TypeRaw, format.TypeRaw, false, map[uint64]int{
		metricID: 10,
	})

	material := blobSet.Materialize()

	// Verify metric count
	require.Equal(t, 1, material.MetricCount())
	require.True(t, material.HasMetricID(metricID))

	// Verify total data point count
	require.Equal(t, 10, material.DataPointCount(metricID))

	// Verify all values are accessible
	for i := range 10 {
		val, ok := material.ValueAt(metricID, i)
		require.True(t, ok, "index %d should be accessible", i)
		expectedVal := float64(metricID) + float64(i)
		require.Equal(t, expectedVal, val, "value at index %d should match", i)

		ts, ok := material.TimestampAt(metricID, i)
		require.True(t, ok)
		require.Equal(t, int64(i*1000), ts)
	}
}

// ==============================================================================
// MaterializedNumericBlobSet Tests - Multiple Blobs with Same Metric
// ==============================================================================

func TestMaterializedNumericBlobSet_MultipleBlobs_SameMetric(t *testing.T) {
	metricID := uint64(1234)
	blobSet := createTestBlobSetForMaterialization(t, 3, format.TypeRaw, format.TypeRaw, false, map[uint64]int{
		metricID: 100,
	})

	material := blobSet.Materialize()

	// Verify metric count
	require.Equal(t, 1, material.MetricCount())
	require.True(t, material.HasMetricID(metricID))

	// Verify total data point count across all blobs (3 blobs × 100 points)
	require.Equal(t, 300, material.DataPointCount(metricID))

	// Verify global indexing works correctly
	// Blob 0: indices 0-99
	val, ok := material.ValueAt(metricID, 50)
	require.True(t, ok)
	require.Equal(t, float64(metricID)+float64(50), val) // blob 0 value

	// Blob 1: indices 100-199
	val, ok = material.ValueAt(metricID, 150)
	require.True(t, ok)
	require.Equal(t, float64(metricID)+float64(1000)+float64(50), val) // blob 1 value

	// Blob 2: indices 200-299
	val, ok = material.ValueAt(metricID, 250)
	require.True(t, ok)
	require.Equal(t, float64(metricID)+float64(2000)+float64(50), val) // blob 2 value

	// Verify timestamps
	ts, ok := material.TimestampAt(metricID, 150)
	require.True(t, ok)
	require.Equal(t, int64(1000000+50*1000), ts) // blob 1 timestamp
}

// ==============================================================================
// MaterializedNumericBlobSet Tests - Multiple Blobs with Sparse Metrics
// ==============================================================================

func TestMaterializedNumericBlobSet_MultipleBlobs_SparseMetrics(t *testing.T) {
	metricA := uint64(1111)
	metricB := uint64(2222)
	metricC := uint64(3333)

	// Create 3 blobs with different metrics present in each
	blobs := make([]NumericBlob, 3)
	baseTime := time.Now()

	// Blob 0: metric A (10 points), metric B (10 points)
	{
		encoder, err := NewNumericEncoder(baseTime, WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw))
		require.NoError(t, err)

		// Metric A
		err = encoder.StartMetricID(metricA, 10)
		require.NoError(t, err)
		for i := range 10 {
			err = encoder.AddDataPoint(int64(i*1000), float64(metricA)+float64(i), "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Metric B
		err = encoder.StartMetricID(metricB, 10)
		require.NoError(t, err)
		for i := range 10 {
			err = encoder.AddDataPoint(int64(i*1000), float64(metricB)+float64(i), "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)

		blobBytes, err := encoder.Finish()
		require.NoError(t, err)
		decoder, err := NewNumericDecoder(blobBytes)
		require.NoError(t, err)
		blobs[0], err = decoder.Decode()
		require.NoError(t, err)
	}

	// Blob 1: metric B (10 points), metric C (10 points) - metric A missing
	{
		encoder, err := NewNumericEncoder(baseTime.Add(time.Hour), WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw))
		require.NoError(t, err)

		// Metric B
		err = encoder.StartMetricID(metricB, 10)
		require.NoError(t, err)
		for i := range 10 {
			err = encoder.AddDataPoint(int64(1000000+i*1000), float64(metricB)+1000+float64(i), "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Metric C
		err = encoder.StartMetricID(metricC, 10)
		require.NoError(t, err)
		for i := range 10 {
			err = encoder.AddDataPoint(int64(1000000+i*1000), float64(metricC)+1000+float64(i), "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)

		blobBytes, err := encoder.Finish()
		require.NoError(t, err)
		decoder, err := NewNumericDecoder(blobBytes)
		require.NoError(t, err)
		blobs[1], err = decoder.Decode()
		require.NoError(t, err)
	}

	// Blob 2: metric A (10 points), metric C (10 points) - metric B missing
	{
		encoder, err := NewNumericEncoder(baseTime.Add(2*time.Hour), WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw))
		require.NoError(t, err)

		// Metric A
		err = encoder.StartMetricID(metricA, 10)
		require.NoError(t, err)
		for i := range 10 {
			err = encoder.AddDataPoint(int64(2000000+i*1000), float64(metricA)+2000+float64(i), "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Metric C
		err = encoder.StartMetricID(metricC, 10)
		require.NoError(t, err)
		for i := range 10 {
			err = encoder.AddDataPoint(int64(2000000+i*1000), float64(metricC)+2000+float64(i), "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)

		blobBytes, err := encoder.Finish()
		require.NoError(t, err)
		decoder, err := NewNumericDecoder(blobBytes)
		require.NoError(t, err)
		blobs[2], err = decoder.Decode()
		require.NoError(t, err)
	}

	blobSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)

	material := blobSet.Materialize()

	// Verify metric count (3 unique metrics)
	require.Equal(t, 3, material.MetricCount())

	// Metric A: blob 0 (10 points) + blob 2 (10 points) = 20 points
	require.Equal(t, 20, material.DataPointCount(metricA))

	// Metric B: blob 0 (10 points) + blob 1 (10 points) = 20 points
	require.Equal(t, 20, material.DataPointCount(metricB))

	// Metric C: blob 1 (10 points) + blob 2 (10 points) = 20 points
	require.Equal(t, 20, material.DataPointCount(metricC))

	// Verify global indices work correctly for sparse metrics
	// Metric A: first 10 points from blob 0, next 10 from blob 2
	val, ok := material.ValueAt(metricA, 5)
	require.True(t, ok)
	require.Equal(t, float64(metricA)+5, val) // blob 0

	val, ok = material.ValueAt(metricA, 15)
	require.True(t, ok)
	require.Equal(t, float64(metricA)+2000+5, val) // blob 2
}

// ==============================================================================
// MaterializedNumericBlobSet Tests - Tags
// ==============================================================================

func TestMaterializedNumericBlobSet_WithTags(t *testing.T) {
	metricID := uint64(1234)
	blobSet := createTestBlobSetForMaterialization(t, 2, format.TypeRaw, format.TypeRaw, true, map[uint64]int{
		metricID: 10,
	})

	material := blobSet.Materialize()

	// Verify tags are accessible
	// Total: 20 points (2 blobs × 10 points)
	// Each blob resets the tag index
	for i := range 20 {
		tag, ok := material.TagAt(metricID, i)
		require.True(t, ok)
		// Tags cycle within each blob independently: tagA, tagB, tagC, tagA, tagB, tagC, ...
		localIdx := i % 10 // Index within the blob
		expectedTag := "tag" + string(rune('A'+localIdx%3))
		require.Equal(t, expectedTag, tag)
	}
}

// ==============================================================================
// MaterializedNumericBlobSet Tests - Different Encodings
// ==============================================================================

func TestMaterializedNumericBlobSet_DifferentEncodings(t *testing.T) {
	metricID := uint64(1234)

	// Create 3 blobs with different encodings
	blobs := make([]NumericBlob, 3)
	baseTime := time.Now()

	// Blob 0: Raw-Raw
	{
		encoder, err := NewNumericEncoder(baseTime, WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw))
		require.NoError(t, err)
		err = encoder.StartMetricID(metricID, 10)
		require.NoError(t, err)
		for i := range 10 {
			err = encoder.AddDataPoint(int64(i*1000), float64(metricID)+float64(i), "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)
		blobBytes, err := encoder.Finish()
		require.NoError(t, err)
		decoder, err := NewNumericDecoder(blobBytes)
		require.NoError(t, err)
		blobs[0], err = decoder.Decode()
		require.NoError(t, err)
	}

	// Blob 1: Delta-Gorilla
	{
		encoder, err := NewNumericEncoder(baseTime.Add(time.Hour), WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeGorilla))
		require.NoError(t, err)
		err = encoder.StartMetricID(metricID, 10)
		require.NoError(t, err)
		for i := range 10 {
			err = encoder.AddDataPoint(int64(1000000+i*1000), float64(metricID)+1000+float64(i), "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)
		blobBytes, err := encoder.Finish()
		require.NoError(t, err)
		decoder, err := NewNumericDecoder(blobBytes)
		require.NoError(t, err)
		blobs[1], err = decoder.Decode()
		require.NoError(t, err)
	}

	// Blob 2: Raw-Gorilla
	{
		encoder, err := NewNumericEncoder(baseTime.Add(2*time.Hour), WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeGorilla))
		require.NoError(t, err)
		err = encoder.StartMetricID(metricID, 10)
		require.NoError(t, err)
		for i := range 10 {
			err = encoder.AddDataPoint(int64(2000000+i*1000), float64(metricID)+2000+float64(i), "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)
		blobBytes, err := encoder.Finish()
		require.NoError(t, err)
		decoder, err := NewNumericDecoder(blobBytes)
		require.NoError(t, err)
		blobs[2], err = decoder.Decode()
		require.NoError(t, err)
	}

	blobSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)

	material := blobSet.Materialize()

	// Verify all 30 points are accessible (10 from each blob)
	require.Equal(t, 30, material.DataPointCount(metricID))

	// Verify values from different encodings
	for i := range 30 {
		val, ok := material.ValueAt(metricID, i)
		require.True(t, ok, "index %d should be accessible", i)

		var expectedVal float64
		if i < 10 {
			expectedVal = float64(metricID) + float64(i)
		} else if i < 20 {
			expectedVal = float64(metricID) + 1000 + float64(i-10)
		} else {
			expectedVal = float64(metricID) + 2000 + float64(i-20)
		}
		require.Equal(t, expectedVal, val)
	}
}

// ==============================================================================
// MaterializedNumericBlobSet Tests - Out of Bounds
// ==============================================================================

func TestMaterializedNumericBlobSet_OutOfBounds(t *testing.T) {
	metricID := uint64(1234)
	blobSet := createTestBlobSetForMaterialization(t, 2, format.TypeRaw, format.TypeRaw, false, map[uint64]int{
		metricID: 10,
	})

	material := blobSet.Materialize()

	totalCount := material.DataPointCount(metricID)
	require.Equal(t, 20, totalCount)

	// Negative index
	val, ok := material.ValueAt(metricID, -1)
	require.False(t, ok)
	require.Zero(t, val)

	// Index at boundary (should work)
	_, ok = material.ValueAt(metricID, totalCount-1)
	require.True(t, ok)

	// Index beyond boundary
	val, ok = material.ValueAt(metricID, totalCount)
	require.False(t, ok)
	require.Zero(t, val)
}

// ==============================================================================
// MaterializedNumericBlobSet Tests - Non-Existent Metric
// ==============================================================================

func TestMaterializedNumericBlobSet_NonExistentMetric(t *testing.T) {
	metricID := uint64(1234)
	blobSet := createTestBlobSetForMaterialization(t, 2, format.TypeRaw, format.TypeRaw, false, map[uint64]int{
		metricID: 10,
	})

	material := blobSet.Materialize()

	// Non-existent metric
	nonExistentID := uint64(9999)

	val, ok := material.ValueAt(nonExistentID, 0)
	require.False(t, ok)
	require.Zero(t, val)

	ts, ok := material.TimestampAt(nonExistentID, 0)
	require.False(t, ok)
	require.Zero(t, ts)

	tag, ok := material.TagAt(nonExistentID, 0)
	require.False(t, ok)
	require.Empty(t, tag)

	require.Zero(t, material.DataPointCount(nonExistentID))
	require.False(t, material.HasMetricID(nonExistentID))
}

// ==============================================================================
// MaterializedNumericBlobSet Tests - Metadata Methods
// ==============================================================================

func TestMaterializedNumericBlobSet_MetadataMethods(t *testing.T) {
	metricA := uint64(1111)
	metricB := uint64(2222)
	blobSet := createTestBlobSetForMaterialization(t, 2, format.TypeRaw, format.TypeRaw, false, map[uint64]int{
		metricA: 10,
		metricB: 15,
	})

	material := blobSet.Materialize()

	// MetricCount
	require.Equal(t, 2, material.MetricCount())

	// MetricIDs
	ids := material.MetricIDs()
	require.Len(t, ids, 2)
	require.Contains(t, ids, metricA)
	require.Contains(t, ids, metricB)

	// HasMetricID
	require.True(t, material.HasMetricID(metricA))
	require.True(t, material.HasMetricID(metricB))
	require.False(t, material.HasMetricID(9999))

	// DataPointCount
	require.Equal(t, 20, material.DataPointCount(metricA)) // 2 blobs × 10 points
	require.Equal(t, 30, material.DataPointCount(metricB)) // 2 blobs × 15 points
}

// ==============================================================================
// MaterializedNumericBlobSet Tests - Correctness Validation
// ==============================================================================

func TestMaterializedNumericBlobSet_Correctness_AllEncodings(t *testing.T) {
	tests := []struct {
		name   string
		tsEnc  format.EncodingType
		valEnc format.EncodingType
	}{
		{"Raw-Raw", format.TypeRaw, format.TypeRaw},
		{"Raw-Gorilla", format.TypeRaw, format.TypeGorilla},
		{"Delta-Raw", format.TypeDelta, format.TypeRaw},
		{"Delta-Gorilla", format.TypeDelta, format.TypeGorilla},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metricID := uint64(1234)
			blobSet := createTestBlobSetForMaterialization(t, 3, tt.tsEnc, tt.valEnc, true, map[uint64]int{
				metricID: 50,
			})

			material := blobSet.Materialize()

			// Collect all values via sequential iteration
			expectedValues := []float64{}
			expectedTimestamps := []int64{}
			expectedTags := []string{}

			for i := range blobSet.blobs {
				blob := &blobSet.blobs[i]
				for _, dp := range blob.All(metricID) {
					expectedValues = append(expectedValues, dp.Val)
					expectedTimestamps = append(expectedTimestamps, dp.Ts)
					expectedTags = append(expectedTags, dp.Tag)
				}
			}

			// Verify materialized values match sequential iteration
			require.Equal(t, len(expectedValues), material.DataPointCount(metricID))

			for i := range len(expectedValues) {
				val, ok := material.ValueAt(metricID, i)
				require.True(t, ok)
				require.Equal(t, expectedValues[i], val, "value mismatch at index %d", i)

				ts, ok := material.TimestampAt(metricID, i)
				require.True(t, ok)
				require.Equal(t, expectedTimestamps[i], ts, "timestamp mismatch at index %d", i)

				tag, ok := material.TagAt(metricID, i)
				require.True(t, ok)
				require.Equal(t, expectedTags[i], tag, "tag mismatch at index %d", i)
			}
		})
	}
}
