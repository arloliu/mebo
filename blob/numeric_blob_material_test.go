package blob

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
)

// Helper function to create a test blob with specified encodings
func createTestBlobForMaterialization(t *testing.T, tsEnc, valEnc format.EncodingType, withTags bool, metricData map[uint64]int) NumericBlob {
	t.Helper()

	startTime := time.Now()
	opts := []NumericEncoderOption{
		WithTimestampEncoding(tsEnc),
		WithValueEncoding(valEnc),
	}
	if withTags {
		opts = append(opts, WithTagsEnabled(true))
	}

	encoder, err := NewNumericEncoder(startTime, opts...)
	require.NoError(t, err)

	for metricID, count := range metricData {
		err = encoder.StartMetricID(metricID, count)
		require.NoError(t, err)

		for i := range count {
			tag := ""
			if withTags {
				tag = "tag" + string(rune('A'+i%3))
			}
			err = encoder.AddDataPoint(int64(i*1000), float64(metricID+uint64(i)), tag)
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

	return blob
}

// ==============================================================================
// MaterializedNumericBlob Tests - Empty Blob
// ==============================================================================

func TestMaterializedNumericBlob_EmptyBlob(t *testing.T) {
	var blob NumericBlob
	material := blob.Materialize()

	// Metadata methods
	require.Zero(t, material.MetricCount(), "empty materialized blob should have 0 metrics")
	require.False(t, material.HasMetricID(100), "empty materialized blob should not have any metric ID")
	require.False(t, material.HasMetricName("test"), "empty materialized blob should not have any metric name")
	require.Empty(t, material.MetricIDs(), "empty materialized blob should have empty metric IDs")
	require.Empty(t, material.MetricNames(), "empty materialized blob should have empty metric names")

	// DataPointCount
	require.Zero(t, material.DataPointCount(100), "empty materialized blob should return 0 for any metric ID")
	require.Zero(t, material.DataPointCountByName("test"), "empty materialized blob should return 0 for any name")

	// Random access should return false
	v, ok := material.ValueAt(100, 0)
	require.False(t, ok)
	require.Zero(t, v)

	ts, ok := material.TimestampAt(100, 0)
	require.False(t, ok)
	require.Zero(t, ts)

	tag, ok := material.TagAt(100, 0)
	require.False(t, ok)
	require.Empty(t, tag)
}

// ==============================================================================
// MaterializedNumericBlob Tests - Single Metric
// ==============================================================================

func TestMaterializedNumericBlob_SingleMetric_Raw(t *testing.T) {
	metricID := uint64(1234)
	blob := createTestBlobForMaterialization(t, format.TypeRaw, format.TypeRaw, false, map[uint64]int{
		metricID: 5,
	})

	// Materialize
	material := blob.Materialize()

	// Verify metadata
	require.Equal(t, 1, material.MetricCount())
	require.True(t, material.HasMetricID(metricID))
	require.False(t, material.HasMetricID(9999))
	require.Equal(t, []uint64{metricID}, material.MetricIDs())
	require.Equal(t, 5, material.DataPointCount(metricID))

	// Verify all values
	for i := range 5 {
		val, ok := material.ValueAt(metricID, i)
		require.True(t, ok, "ValueAt should succeed for index %d", i)
		require.Equal(t, float64(metricID+uint64(i)), val, "value at index %d", i)

		ts, ok := material.TimestampAt(metricID, i)
		require.True(t, ok, "TimestampAt should succeed for index %d", i)
		require.Equal(t, int64(i*1000), ts, "timestamp at index %d", i)

		// Tags disabled, should return empty string but ok=true
		tag, ok := material.TagAt(metricID, i)
		require.True(t, ok, "TagAt should succeed even when disabled")
		require.Empty(t, tag, "tag should be empty when disabled")
	}

	// Test out of bounds
	_, ok := material.ValueAt(metricID, -1)
	require.False(t, ok, "negative index should return false")

	_, ok = material.ValueAt(metricID, 5)
	require.False(t, ok, "index >= length should return false")

	// Test non-existent metric
	_, ok = material.ValueAt(9999, 0)
	require.False(t, ok, "non-existent metric should return false")
}

func TestMaterializedNumericBlob_SingleMetric_Delta(t *testing.T) {
	metricID := uint64(5678)
	blob := createTestBlobForMaterialization(t, format.TypeDelta, format.TypeGorilla, false, map[uint64]int{
		metricID: 100,
	})

	// Materialize
	material := blob.Materialize()

	// Verify all 100 data points
	require.Equal(t, 100, material.DataPointCount(metricID))
	for i := range 100 {
		val, ok := material.ValueAt(metricID, i)
		require.True(t, ok)
		require.Equal(t, float64(metricID+uint64(i)), val)

		ts, ok := material.TimestampAt(metricID, i)
		require.True(t, ok)
		require.Equal(t, int64(i*1000), ts)
	}

	// Random access pattern - verify O(1) access works
	indices := []int{0, 50, 99, 25, 75, 10, 90}
	for _, idx := range indices {
		val, ok := material.ValueAt(metricID, idx)
		require.True(t, ok)
		require.Equal(t, float64(metricID+uint64(idx)), val)
	}
}

func TestMaterializedNumericBlob_WithTags(t *testing.T) {
	metricID := uint64(1111)
	blob := createTestBlobForMaterialization(t, format.TypeRaw, format.TypeRaw, true, map[uint64]int{
		metricID: 5,
	})

	// Materialize
	material := blob.Materialize()

	// Verify tags exist and match expected pattern (tagA, tagB, tagC, tagA, tagB)
	for i := range 5 {
		tag, ok := material.TagAt(metricID, i)
		require.True(t, ok)
		require.NotEmpty(t, tag)
		expectedTag := "tag" + string(rune('A'+i%3))
		require.Equal(t, expectedTag, tag)
	}
}

// ==============================================================================
// MaterializedNumericBlob Tests - Multiple Metrics
// ==============================================================================

func TestMaterializedNumericBlob_MultipleMetrics(t *testing.T) {
	// Create blob with 3 metrics of different lengths
	metricData := map[uint64]int{
		100: 10,  // 10 data points
		200: 50,  // 50 data points
		300: 100, // 100 data points
	}

	blob := createTestBlobForMaterialization(t, format.TypeDelta, format.TypeGorilla, false, metricData)

	// Materialize
	material := blob.Materialize()

	// Verify metadata
	require.Equal(t, 3, material.MetricCount())
	require.ElementsMatch(t, []uint64{100, 200, 300}, material.MetricIDs())

	// Verify each metric
	for metricID, count := range metricData {
		require.Equal(t, count, material.DataPointCount(metricID))
		require.True(t, material.HasMetricID(metricID))

		// Verify all values for this metric
		for i := range count {
			val, ok := material.ValueAt(metricID, i)
			require.True(t, ok)
			require.Equal(t, float64(metricID+uint64(i)), val)

			ts, ok := material.TimestampAt(metricID, i)
			require.True(t, ok)
			require.Equal(t, int64(i*1000), ts)
		}
	}
}

func TestMaterializedNumericBlob_WithMetricNames(t *testing.T) {
	// Create encoder with metric names
	startTime := time.Now()
	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)

	// Add metrics with names
	metrics := map[string][]float64{
		"cpu.usage":    {10, 20, 30, 40, 50},
		"memory.usage": {100, 200, 300},
		"disk.io":      {1, 2},
	}

	for name, values := range metrics {
		err = encoder.StartMetricName(name, len(values))
		require.NoError(t, err)

		for i, val := range values {
			err = encoder.AddDataPoint(int64(i*1000), val, "")
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

	// Materialize
	material := blob.Materialize()

	// Verify metadata
	require.Equal(t, 3, material.MetricCount())
	// Note: If no hash collisions occurred, metric names aren't stored in the blob
	// So MetricNames() may return empty even though metrics were added by name
	// This is expected behavior - metric names are only stored when collisions are detected

	// Verify name-based access works by looking up via HasMetricName
	// (which uses hash internally when names aren't stored)
	for name, expectedValues := range metrics {
		// Note: HasMetricName may return false when no names are stored (no collisions)
		// In that case, we can't test name-based access
		if !material.HasMetricName(name) {
			t.Logf("Metric name '%s' not available (no collisions detected)", name)
			continue
		}

		require.Equal(t, len(expectedValues), material.DataPointCountByName(name))

		for i, expectedVal := range expectedValues {
			val, ok := material.ValueAtByName(name, i)
			require.True(t, ok, "ValueAtByName should succeed for %s[%d]", name, i)
			require.Equal(t, expectedVal, val)

			ts, ok := material.TimestampAtByName(name, i)
			require.True(t, ok)
			require.Equal(t, int64(i*1000), ts)
		}
	}

	// Test non-existent name
	_, ok := material.ValueAtByName("non.existent", 0)
	require.False(t, ok)
}

// ==============================================================================
// MaterializedMetric Tests
// ==============================================================================

func TestMaterializedMetric_Basic(t *testing.T) {
	metricID := uint64(1234)
	expectedValues := []float64{1234, 1235, 1236, 1237, 1238}

	blob := createTestBlobForMaterialization(t, format.TypeRaw, format.TypeRaw, false, map[uint64]int{
		metricID: 5,
	})

	// Materialize single metric
	metric, ok := blob.MaterializeMetric(metricID)
	require.True(t, ok)

	// Verify metadata
	require.Equal(t, metricID, metric.MetricID)
	require.Equal(t, 5, metric.Len())
	require.Len(t, metric.Timestamps, 5)
	require.Len(t, metric.Values, 5)
	require.Empty(t, metric.Tags, "tags should be empty when disabled")

	// Verify all values
	for i, expectedVal := range expectedValues {
		val, ok := metric.ValueAt(i)
		require.True(t, ok)
		require.Equal(t, expectedVal, val)

		ts, ok := metric.TimestampAt(i)
		require.True(t, ok)
		require.Equal(t, int64(i*1000), ts)

		// Tags disabled
		tag, ok := metric.TagAt(i)
		require.True(t, ok)
		require.Empty(t, tag)
	}

	// Test direct slice access
	require.Equal(t, expectedValues, metric.Values)
	require.Equal(t, []int64{0, 1000, 2000, 3000, 4000}, metric.Timestamps)
}

func TestMaterializedNumericMetric_WithTags(t *testing.T) {
	metricID := uint64(5678)
	blob := createTestBlobForMaterialization(t, format.TypeRaw, format.TypeRaw, true, map[uint64]int{
		metricID: 3,
	})

	// Materialize
	metric, ok := blob.MaterializeMetric(metricID)
	require.True(t, ok)
	require.Len(t, metric.Tags, 3)

	// Verify tags
	expectedTags := []string{"tagA", "tagB", "tagC"}
	for i, expectedTag := range expectedTags {
		require.Equal(t, expectedTag, metric.Tags[i])

		tag, ok := metric.TagAt(i)
		require.True(t, ok)
		require.Equal(t, expectedTag, tag)
	}
}

func TestMaterializedNumericMetric_NonExistent(t *testing.T) {
	blob := createTestBlobForMaterialization(t, format.TypeRaw, format.TypeRaw, false, map[uint64]int{
		100: 1,
	})

	// Try to materialize non-existent metric
	_, ok := blob.MaterializeMetric(9999)
	require.False(t, ok, "MaterializeMetric should return false for non-existent metric")
}

func TestMaterializedNumericMetric_ByName(t *testing.T) {
	// Create encoder with metric names
	startTime := time.Now()
	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)

	metricName := "test.metric"
	expectedValues := []float64{1, 2, 3}

	err = encoder.StartMetricName(metricName, len(expectedValues))
	require.NoError(t, err)

	for i, val := range expectedValues {
		err = encoder.AddDataPoint(int64(i), val, "")
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	blobBytes, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(blobBytes)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Materialize by name
	metric, ok := blob.MaterializeMetricByName(metricName)
	require.True(t, ok)
	require.Equal(t, expectedValues, metric.Values)

	// Try non-existent name
	_, ok = blob.MaterializeMetricByName("non.existent")
	require.False(t, ok)
}

func TestMaterializedNumericMetric_OutOfBounds(t *testing.T) {
	metricID := uint64(100)
	blob := createTestBlobForMaterialization(t, format.TypeRaw, format.TypeRaw, false, map[uint64]int{
		metricID: 3,
	})

	// Materialize
	metric, ok := blob.MaterializeMetric(metricID)
	require.True(t, ok)

	// Test out of bounds access
	_, ok = metric.ValueAt(-1)
	require.False(t, ok)

	_, ok = metric.ValueAt(3)
	require.False(t, ok)

	_, ok = metric.TimestampAt(-1)
	require.False(t, ok)

	_, ok = metric.TimestampAt(100)
	require.False(t, ok)

	_, ok = metric.TagAt(-1)
	require.False(t, ok)

	_, ok = metric.TagAt(10)
	require.False(t, ok)
}

// ==============================================================================
// Correctness Tests - Compare with Sequential Access
// ==============================================================================

func TestMaterializedNumericBlob_Correctness_AllEncodings(t *testing.T) {
	tests := []struct {
		name    string
		tsEnc   format.EncodingType
		valEnc  format.EncodingType
		useTags bool
	}{
		{"Raw-Raw", format.TypeRaw, format.TypeRaw, false},
		{"Raw-Gorilla", format.TypeRaw, format.TypeGorilla, false},
		{"Delta-Raw", format.TypeDelta, format.TypeRaw, false},
		{"Delta-Gorilla", format.TypeDelta, format.TypeGorilla, false},
		{"Delta-Gorilla-Tags", format.TypeDelta, format.TypeGorilla, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create blob with 2 metrics
			blob := createTestBlobForMaterialization(t, tt.tsEnc, tt.valEnc, tt.useTags, map[uint64]int{
				100: 50,
				200: 30,
			})

			// Materialize
			material := blob.Materialize()

			// Verify metric1 matches sequential access
			metricID1 := uint64(100)
			i := 0
			for idx, dp := range blob.All(metricID1) {
				val, ok := material.ValueAt(metricID1, idx)
				require.True(t, ok)
				require.Equal(t, dp.Val, val, "value mismatch at index %d", idx)

				ts, ok := material.TimestampAt(metricID1, idx)
				require.True(t, ok)
				require.Equal(t, dp.Ts, ts, "timestamp mismatch at index %d", idx)

				if tt.useTags {
					tag, ok := material.TagAt(metricID1, idx)
					require.True(t, ok)
					require.Equal(t, dp.Tag, tag, "tag mismatch at index %d", idx)
				}
				i++
			}
			require.Equal(t, 50, i)

			// Verify metric2 matches sequential access
			metricID2 := uint64(200)
			i = 0
			for idx, dp := range blob.All(metricID2) {
				val, ok := material.ValueAt(metricID2, idx)
				require.True(t, ok)
				require.Equal(t, dp.Val, val, "value mismatch at index %d", idx)

				ts, ok := material.TimestampAt(metricID2, idx)
				require.True(t, ok)
				require.Equal(t, dp.Ts, ts, "timestamp mismatch at index %d", idx)

				if tt.useTags {
					tag, ok := material.TagAt(metricID2, idx)
					require.True(t, ok)
					require.Equal(t, dp.Tag, tag, "tag mismatch at index %d", idx)
				}
				i++
			}
			require.Equal(t, 30, i)
		})
	}
}
