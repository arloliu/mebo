package blob

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
)

// Helper function to create a test text blob
func createTestTextBlobForMaterialization(t *testing.T, withTags bool, metricData map[uint64][]string) TextBlob {
	t.Helper()

	startTime := time.Now()
	opts := []TextEncoderOption{
		WithTextTimestampEncoding(format.TypeRaw),
	}
	if withTags {
		opts = append(opts, WithTextTagsEnabled(true))
	}

	encoder, err := NewTextEncoder(startTime, opts...)
	require.NoError(t, err)

	for metricID, values := range metricData {
		err = encoder.StartMetricID(metricID, len(values))
		require.NoError(t, err)

		for i, val := range values {
			tag := ""
			if withTags {
				tag = "tag" + string(rune('A'+i%3))
			}
			err = encoder.AddDataPoint(int64(i*1000), val, tag)
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

	return blob
}

// ==============================================================================
// MaterializedTextBlob Tests - Empty Blob
// ==============================================================================

func TestMaterializedTextBlob_EmptyBlob(t *testing.T) {
	var blob TextBlob
	material := blob.Materialize()

	// Metadata methods
	require.Zero(t, material.MetricCount())
	require.False(t, material.HasMetricID(100))
	require.False(t, material.HasMetricName("test"))
	require.Empty(t, material.MetricIDs())
	require.Empty(t, material.MetricNames())

	// DataPointCount
	require.Zero(t, material.DataPointCount(100))
	require.Zero(t, material.DataPointCountByName("test"))

	// Random access should return false
	v, ok := material.ValueAt(100, 0)
	require.False(t, ok)
	require.Empty(t, v)

	ts, ok := material.TimestampAt(100, 0)
	require.False(t, ok)
	require.Zero(t, ts)

	tag, ok := material.TagAt(100, 0)
	require.False(t, ok)
	require.Empty(t, tag)
}

// ==============================================================================
// MaterializedTextBlob Tests - Single Metric
// ==============================================================================

func TestMaterializedTextBlob_SingleMetric(t *testing.T) {
	metricID := uint64(1234)
	values := []string{"value0", "value1", "value2", "value3", "value4"}

	blob := createTestTextBlobForMaterialization(t, false, map[uint64][]string{
		metricID: values,
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
	for i, expectedVal := range values {
		val, ok := material.ValueAt(metricID, i)
		require.True(t, ok, "ValueAt should succeed for index %d", i)
		require.Equal(t, expectedVal, val, "value at index %d", i)

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

func TestMaterializedTextBlob_WithTags(t *testing.T) {
	metricID := uint64(1111)
	values := []string{"a", "b", "c", "d", "e"}

	blob := createTestTextBlobForMaterialization(t, true, map[uint64][]string{
		metricID: values,
	})

	// Materialize
	material := blob.Materialize()

	// Verify tags exist and match expected pattern
	for i := range 5 {
		tag, ok := material.TagAt(metricID, i)
		require.True(t, ok)
		require.NotEmpty(t, tag)
		expectedTag := "tag" + string(rune('A'+i%3))
		require.Equal(t, expectedTag, tag)
	}
}

// ==============================================================================
// MaterializedTextBlob Tests - Multiple Metrics
// ==============================================================================

func TestMaterializedTextBlob_MultipleMetrics(t *testing.T) {
	// Create blob with 3 metrics of different lengths
	metricData := map[uint64][]string{
		100: {"a", "b", "c", "d", "e"},
		200: {"x", "y", "z"},
		300: {"1", "2"},
	}

	blob := createTestTextBlobForMaterialization(t, false, metricData)

	// Materialize
	material := blob.Materialize()

	// Verify metadata
	require.Equal(t, 3, material.MetricCount())
	require.ElementsMatch(t, []uint64{100, 200, 300}, material.MetricIDs())

	// Verify each metric
	for metricID, expectedValues := range metricData {
		require.Equal(t, len(expectedValues), material.DataPointCount(metricID))
		require.True(t, material.HasMetricID(metricID))

		// Verify all values for this metric
		for i, expectedVal := range expectedValues {
			val, ok := material.ValueAt(metricID, i)
			require.True(t, ok)
			require.Equal(t, expectedVal, val)

			ts, ok := material.TimestampAt(metricID, i)
			require.True(t, ok)
			require.Equal(t, int64(i*1000), ts)
		}
	}
}

func TestMaterializedTextBlob_WithMetricNames(t *testing.T) {
	// Create encoder with metric names
	startTime := time.Now()
	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)

	// Add metrics with names
	metrics := map[string][]string{
		"status":  {"ok", "error", "pending"},
		"message": {"hello", "world"},
		"code":    {"200"},
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

	decoder, err := NewTextDecoder(blobBytes)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Materialize
	material := blob.Materialize()

	// Verify metadata
	require.Equal(t, 3, material.MetricCount())

	// Verify name-based access if names are available
	for name, expectedValues := range metrics {
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
}

// ==============================================================================
// MaterializedTextMetric Tests
// ==============================================================================

func TestMaterializedTextMetric_Basic(t *testing.T) {
	metricID := uint64(1234)
	expectedValues := []string{"alpha", "beta", "gamma", "delta", "epsilon"}

	blob := createTestTextBlobForMaterialization(t, false, map[uint64][]string{
		metricID: expectedValues,
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

func TestMaterializedTextMetric_WithTags(t *testing.T) {
	metricID := uint64(5678)
	values := []string{"x", "y", "z"}

	blob := createTestTextBlobForMaterialization(t, true, map[uint64][]string{
		metricID: values,
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

func TestMaterializedTextMetric_NonExistent(t *testing.T) {
	blob := createTestTextBlobForMaterialization(t, false, map[uint64][]string{
		100: {"test"},
	})

	// Try to materialize non-existent metric
	_, ok := blob.MaterializeMetric(9999)
	require.False(t, ok, "MaterializeMetric should return false for non-existent metric")
}

func TestMaterializedTextMetric_ByName(t *testing.T) {
	// Create encoder with metric names
	startTime := time.Now()
	encoder, err := NewTextEncoder(startTime)
	require.NoError(t, err)

	metricName := "test.metric"
	expectedValues := []string{"one", "two", "three"}

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

	decoder, err := NewTextDecoder(blobBytes)
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

func TestMaterializedTextMetric_OutOfBounds(t *testing.T) {
	metricID := uint64(100)
	blob := createTestTextBlobForMaterialization(t, false, map[uint64][]string{
		metricID: {"a", "b", "c"},
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

func TestMaterializedTextBlob_Correctness(t *testing.T) {
	tests := []struct {
		name    string
		useTags bool
	}{
		{"NoTags", false},
		{"WithTags", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create blob with 2 metrics
			blob := createTestTextBlobForMaterialization(t, tt.useTags, map[uint64][]string{
				100: {"a", "b", "c", "d", "e"},
				200: {"x", "y", "z"},
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
			require.Equal(t, 5, i)

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
			require.Equal(t, 3, i)
		})
	}
}
