package blob

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

const (
	// mixedSetChimpVariants is the number of Chimp variants at the start of mixedSetVariants.
	mixedSetChimpVariants = 8
	// craftedTarget is the metric a crafted test member holds beside craftedCompanion.
	craftedTarget = "crafted.target"
	// craftedCompanion's metric ID sorts after craftedTarget's,
	// so in a V2 payload, which orders columns by metric ID, the target's column is not the last.
	craftedCompanion = "crafted.companion"

	// Bounds of a FuzzNumericBlobSetMaterializeMetric program.
	materializeFuzzMaxMembers = 24
	materializeFuzzMaxCount   = 45
	materializeFuzzMaxSteps   = 32
	materializeFuzzStepSize   = 5 // op, member, metric or bit, and a two-byte argument
	materializeFuzzKinds      = 7 // the five setValueKinds, random bit patterns, and one run of a value ALP stores in full
	materializeFuzzOps        = 9 // three payload flips and six index entry edits
	// materializeFuzzSeedMetric indexes the metric the crafted seeds damage: "fuzz.1",
	// the middle column of a payload in either layout (V1 keeps the order the metrics were added in, V2 sorts by ID).
	materializeFuzzSeedMetric = 1
)

// materializeFuzzNames are the metrics every member of a fuzz set holds.
var materializeFuzzNames = []string{"fuzz.0", "fuzz.1", "fuzz.2"}

// materializeOutcome is what one materialize call returned, or the value it panicked with.
type materializeOutcome struct {
	metric   MaterializedNumericMetric
	ok       bool
	panicked bool
	panicVal string // the panic value's type and message
}

// materializeFuzzMember is one member of a fuzz program: a variant of mixedSetVariants,
// the count of every metric less one, and a kind of values, each taken modulo its bound.
type materializeFuzzMember struct {
	variant, count, kind byte
}

// materializeFuzzStep is one damage step of a fuzz program.
// Ops 0 to 2 flip bit metric%8 of the value, timestamp or tag payload at arg modulo its length.
// Ops 3 to 8 edit the entry of metric metric: count up, count down, value length, timestamp length, tag length,
// or a cut at an ALP-RLE column's nested column.
type materializeFuzzStep struct {
	op, member, metric byte
	arg                uint16
}

// setTestMember is one blob of a hand-built test set: metric names[k] holds cols[k], one point a second.
type setTestMember struct {
	opts  []NumericEncoderOption
	tags  bool
	names []string
	cols  [][]float64
}

// Helper function to create a test NumericBlobSet with specified number of blobs
func createTestBlobSetForMaterialization(t *testing.T, numBlobs int, tsEnc, valEnc format.EncodingType, withTags bool, metricsPerBlob map[uint64]int, extraOpts ...NumericEncoderOption) NumericBlobSet {
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
		opts = append(opts, extraOpts...)

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

// TestNumericBlobSet_MaterializeMetric tests single metric materialization by ID
func TestNumericBlobSet_MaterializeMetric(t *testing.T) {
	tests := []struct {
		name     string
		tsEnc    format.EncodingType
		valEnc   format.EncodingType
		withTags bool
	}{
		{
			name:     "delta encoding with tags",
			tsEnc:    format.TypeDelta,
			valEnc:   format.TypeGorilla,
			withTags: true,
		},
		{
			name:     "raw encoding without tags",
			tsEnc:    format.TypeRaw,
			valEnc:   format.TypeRaw,
			withTags: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create blob set with 3 blobs, 2 metrics per blob
			metricID1 := uint64(100)
			metricID2 := uint64(200)
			metricsPerBlob := map[uint64]int{
				metricID1: 5, // 5 points per blob
				metricID2: 3, // 3 points per blob
			}

			blobSet := createTestBlobSetForMaterialization(t, 3, tt.tsEnc, tt.valEnc, tt.withTags, metricsPerBlob)

			// Test: Materialize only metricID1
			metric, ok := blobSet.MaterializeMetric(metricID1)
			require.True(t, ok, "metric should be found")
			require.Equal(t, metricID1, metric.MetricID)
			require.Len(t, metric.Timestamps, 15, "should have 5 points × 3 blobs")
			require.Len(t, metric.Values, 15)

			// Verify data points match sequential iteration
			expectedValues := []float64{}
			expectedTimestamps := []int64{}
			for i := range blobSet.blobs {
				blob := &blobSet.blobs[i]
				for _, dp := range blob.All(metricID1) {
					expectedValues = append(expectedValues, dp.Val)
					expectedTimestamps = append(expectedTimestamps, dp.Ts)
				}
			}

			require.Equal(t, len(expectedValues), len(metric.Values))
			for i := range len(expectedValues) {
				require.Equal(t, expectedTimestamps[i], metric.Timestamps[i], "timestamp mismatch at index %d", i)
				require.Equal(t, expectedValues[i], metric.Values[i], "value mismatch at index %d", i)
			} // Verify tags
			if tt.withTags {
				require.Len(t, metric.Tags, 15)
				for i := range 15 {
					pointIdx := i % 5
					expectedTag := "tag" + string(rune('A'+pointIdx%3))
					require.Equal(t, expectedTag, metric.Tags[i], "tag mismatch at index %d", i)
				}
			} else {
				require.Empty(t, metric.Tags)
			}

			// Test: Access methods work without passing metric ID
			val, ok := metric.ValueAt(10)
			require.True(t, ok)
			require.Equal(t, metric.Values[10], val)

			ts, ok := metric.TimestampAt(5)
			require.True(t, ok)
			require.Equal(t, metric.Timestamps[5], ts)

			// Test: Materialize non-existent metric
			_, ok = blobSet.MaterializeMetric(999)
			require.False(t, ok, "non-existent metric should not be found")
		})
	}
}

// TestNumericBlobSet_MaterializeMetricByName tests single metric materialization by name
func TestNumericBlobSet_MaterializeMetricByName(t *testing.T) {
	// Create blob set with metric names
	baseTime := time.Now()
	blobs := make([]NumericBlob, 2)

	for blobIdx := range 2 {
		startTime := baseTime.Add(time.Duration(blobIdx) * time.Hour)
		encoder, err := NewNumericEncoder(
			startTime,
			WithTimestampEncoding(format.TypeDelta),
			WithValueEncoding(format.TypeGorilla),
			WithTagsEnabled(false),
		)
		require.NoError(t, err)

		// Add metrics with names
		err = encoder.StartMetricName("cpu.usage", 3)
		require.NoError(t, err)
		for i := range 3 {
			ts := int64(blobIdx*1000000 + i*1000)
			val := float64(50 + blobIdx*10 + i)
			err = encoder.AddDataPoint(ts, val, "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)

		err = encoder.StartMetricName("memory.usage", 2)
		require.NoError(t, err)
		for i := range 2 {
			ts := int64(blobIdx*1000000 + i*1000)
			val := float64(80 + blobIdx*10 + i)
			err = encoder.AddDataPoint(ts, val, "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blobs[blobIdx], err = decoder.Decode()
		require.NoError(t, err)
	}

	blobSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)

	// Test: Materialize by name
	metric, ok := blobSet.MaterializeMetricByName("cpu.usage")
	require.True(t, ok, "metric should be found by name")
	require.Len(t, metric.Timestamps, 6, "should have 3 points × 2 blobs")
	require.Len(t, metric.Values, 6)

	// Verify data
	expectedValues := []float64{50, 51, 52, 60, 61, 62}
	for i, expectedVal := range expectedValues {
		require.Equal(t, expectedVal, metric.Values[i], "value mismatch at index %d", i)
	}

	// Test: Access without metric name parameter
	val, ok := metric.ValueAt(3)
	require.True(t, ok)
	require.Equal(t, 60.0, val)

	// Test: Non-existent metric name
	_, ok = blobSet.MaterializeMetricByName("disk.usage")
	require.False(t, ok, "non-existent metric should not be found")
}

// TestNumericBlobSet_MaterializeMetric_EmptyBlobSet tests edge case with empty blob set
func TestNumericBlobSet_MaterializeMetric_EmptyBlobSet(t *testing.T) {
	// Create empty blob set would fail in NewNumericBlobSet, so test with metric not in any blob
	metricsPerBlob := map[uint64]int{
		uint64(100): 5,
	}

	blobSet := createTestBlobSetForMaterialization(t, 2, format.TypeDelta, format.TypeGorilla, false, metricsPerBlob)

	// Try to materialize a metric that doesn't exist
	_, ok := blobSet.MaterializeMetric(999)
	require.False(t, ok, "should return false for non-existent metric")
}

// TestNumericBlobSet_MaterializeMetric_SingleBlob tests with just one blob
func TestNumericBlobSet_MaterializeMetric_SingleBlob(t *testing.T) {
	metricID := uint64(100)
	metricsPerBlob := map[uint64]int{
		metricID: 10,
	}

	blobSet := createTestBlobSetForMaterialization(t, 1, format.TypeDelta, format.TypeGorilla, true, metricsPerBlob)

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

// ==============================================================================
// V2 Layout Materialization Tests
// ==============================================================================

func TestMaterializedNumericBlobSet_V2Layout(t *testing.T) {
	metricA := uint64(1000)
	metricB := uint64(2000)
	metricC := uint64(3000)

	metricsPerBlob := map[uint64]int{
		metricA: 10,
		metricB: 10,
		metricC: 10,
	}

	blobSet := createTestBlobSetForMaterialization(t, 3, format.TypeDelta, format.TypeGorilla, false, metricsPerBlob, WithBlobLayoutV2())

	material := blobSet.Materialize()

	// Verify all 3 metrics present
	require.Equal(t, 3, material.MetricCount())

	// Each metric: 3 blobs × 10 points = 30
	for _, id := range []uint64{metricA, metricB, metricC} {
		require.Equal(t, 30, material.DataPointCount(id), "metric %d", id)
	}

	// Spot-check values from each blob
	// Blob 0: val = metricID + 0*1000 + i
	val, ok := material.ValueAt(metricA, 5)
	require.True(t, ok)
	require.Equal(t, float64(metricA)+5, val)

	// Blob 1: val = metricID + 1*1000 + i, index offset = 10
	val, ok = material.ValueAt(metricA, 15)
	require.True(t, ok)
	require.Equal(t, float64(metricA)+1000+5, val)

	// Blob 2: val = metricID + 2*1000 + i, index offset = 20
	val, ok = material.ValueAt(metricB, 25)
	require.True(t, ok)
	require.Equal(t, float64(metricB)+2000+5, val)

	// Verify timestamps
	ts, ok := material.TimestampAt(metricC, 10)
	require.True(t, ok)
	require.Equal(t, int64(1000000), ts) // blob 1, first point
}

func TestMaterializedNumericBlobSet_V2Layout_WithTags(t *testing.T) {
	metricID := uint64(1234)

	blobSet := createTestBlobSetForMaterialization(t, 2, format.TypeDelta, format.TypeGorilla, true, map[uint64]int{
		metricID: 10,
	}, WithBlobLayoutV2())

	material := blobSet.Materialize()

	require.Equal(t, 20, material.DataPointCount(metricID))

	// Verify tags are aligned with timestamps and values
	for i := range 20 {
		tag, ok := material.TagAt(metricID, i)
		require.True(t, ok, "tag at index %d should exist", i)

		localIdx := i % 10
		expectedTag := "tag" + string(rune('A'+localIdx%3))
		require.Equal(t, expectedTag, tag, "tag mismatch at index %d", i)

		// Verify value also exists at same index
		_, ok = material.ValueAt(metricID, i)
		require.True(t, ok, "value at index %d should exist", i)
	}
}

func TestMaterializedNumericBlobSet_V2Layout_SparseMetrics(t *testing.T) {
	metricA := uint64(1111)
	metricB := uint64(2222)

	// Blob 0: both metrics, Blob 1: only metricA — V2 sorted index must handle this
	blobs := make([]NumericBlob, 2)
	baseTime := time.Now()

	{
		encoder, err := NewNumericEncoder(baseTime, WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeGorilla), WithBlobLayoutV2())
		require.NoError(t, err)
		err = encoder.StartMetricID(metricA, 5)
		require.NoError(t, err)
		for i := range 5 {
			err = encoder.AddDataPoint(int64(i*1000), float64(metricA)+float64(i), "")
			require.NoError(t, err)
		}
		err = encoder.EndMetric()
		require.NoError(t, err)

		err = encoder.StartMetricID(metricB, 5)
		require.NoError(t, err)
		for i := range 5 {
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

	{
		encoder, err := NewNumericEncoder(baseTime.Add(time.Hour), WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeGorilla), WithBlobLayoutV2())
		require.NoError(t, err)
		err = encoder.StartMetricID(metricA, 5)
		require.NoError(t, err)
		for i := range 5 {
			err = encoder.AddDataPoint(int64(1000000+i*1000), float64(metricA)+1000+float64(i), "")
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

	blobSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)

	material := blobSet.Materialize()

	// metricA: 5+5=10 points, metricB: 5 points (only in blob 0)
	require.Equal(t, 10, material.DataPointCount(metricA))
	require.Equal(t, 5, material.DataPointCount(metricB))

	// Verify metricB data comes only from blob 0
	val, ok := material.ValueAt(metricB, 3)
	require.True(t, ok)
	require.Equal(t, float64(metricB)+3, val)

	_, ok = material.ValueAt(metricB, 5)
	require.False(t, ok, "metricB should have only 5 points")
}

func TestNumericBlobSet_MaterializeMetric_V2Layout(t *testing.T) {
	metricA := uint64(1000)
	metricB := uint64(2000)

	blobSet := createTestBlobSetForMaterialization(t, 3, format.TypeDelta, format.TypeGorilla, true, map[uint64]int{
		metricA: 10,
		metricB: 10,
	}, WithBlobLayoutV2())

	metric, ok := blobSet.MaterializeMetric(metricA)
	require.True(t, ok)
	require.Len(t, metric.Timestamps, 30)
	require.Len(t, metric.Values, 30)
	require.Len(t, metric.Tags, 30)

	// Verify value from each blob
	val, ok := metric.ValueAt(5)
	require.True(t, ok)
	require.Equal(t, float64(metricA)+5, val)

	val, ok = metric.ValueAt(15)
	require.True(t, ok)
	require.Equal(t, float64(metricA)+1000+5, val)

	// Non-existent metric
	_, ok = blobSet.MaterializeMetric(9999)
	require.False(t, ok)
}

// ==============================================================================
// Mixed Tag Alignment Tests
// ==============================================================================

func TestMaterializedNumericBlobSet_MixedTagBlobs(t *testing.T) {
	metricID := uint64(1234)
	baseTime := time.Now()
	blobs := make([]NumericBlob, 2)

	// Blob 0: tags enabled
	{
		encoder, err := NewNumericEncoder(baseTime, WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw), WithTagsEnabled(true))
		require.NoError(t, err)
		err = encoder.StartMetricID(metricID, 5)
		require.NoError(t, err)
		for i := range 5 {
			err = encoder.AddDataPoint(int64(i*1000), float64(i), "tagX")
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

	// Blob 1: tags disabled
	{
		encoder, err := NewNumericEncoder(baseTime.Add(time.Hour), WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw))
		require.NoError(t, err)
		err = encoder.StartMetricID(metricID, 5)
		require.NoError(t, err)
		for i := range 5 {
			err = encoder.AddDataPoint(int64(1000000+i*1000), float64(100+i), "")
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

	blobSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)

	material := blobSet.Materialize()

	require.Equal(t, 10, material.DataPointCount(metricID))

	// First 5: tag-enabled blob → "tagX"
	for i := range 5 {
		tag, ok := material.TagAt(metricID, i)
		require.True(t, ok)
		require.Equal(t, "tagX", tag, "index %d should have tag from blob 0", i)
	}

	// Next 5: tag-disabled blob → empty strings for alignment
	for i := 5; i < 10; i++ {
		tag, ok := material.TagAt(metricID, i)
		require.True(t, ok)
		require.Empty(t, tag, "index %d should have empty tag from blob 1", i)
	}
}

func TestNumericBlobSet_MaterializeMetric_MixedTagBlobs(t *testing.T) {
	metricID := uint64(1234)
	baseTime := time.Now()
	blobs := make([]NumericBlob, 2)

	// Blob 0: tags disabled
	{
		encoder, err := NewNumericEncoder(baseTime, WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw))
		require.NoError(t, err)
		err = encoder.StartMetricID(metricID, 3)
		require.NoError(t, err)
		for i := range 3 {
			err = encoder.AddDataPoint(int64(i*1000), float64(i), "")
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

	// Blob 1: tags enabled
	{
		encoder, err := NewNumericEncoder(baseTime.Add(time.Hour), WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw), WithTagsEnabled(true))
		require.NoError(t, err)
		err = encoder.StartMetricID(metricID, 3)
		require.NoError(t, err)
		for i := range 3 {
			err = encoder.AddDataPoint(int64(1000000+i*1000), float64(100+i), "tagY")
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

	blobSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)

	metric, ok := blobSet.MaterializeMetric(metricID)
	require.True(t, ok)
	require.Len(t, metric.Timestamps, 6)
	require.Len(t, metric.Values, 6)
	require.Len(t, metric.Tags, 6)

	// First 3: no-tag blob → empty strings
	for i := range 3 {
		require.Empty(t, metric.Tags[i], "index %d should be empty", i)
	}

	// Next 3: tag blob → "tagY"
	for i := 3; i < 6; i++ {
		require.Equal(t, "tagY", metric.Tags[i], "index %d should have tagY", i)
	}
}

// TestMaterializedNumericBlobSet_ByName_NoNamesPayload pins that a materialized
// set built from names-free members resolves name lookups by hash, matching the
// raw set and the single-blob Materialize() result.
func TestMaterializedNumericBlobSet_ByName_NoNamesPayload(t *testing.T) {
	const metricName = "set.metric.no.names"
	base := time.Unix(1_700_000_000, 0).UTC()

	blobs := make([]NumericBlob, 0, 2)
	for b := range 2 {
		start := base.Add(time.Duration(b) * time.Hour)
		encoder, err := NewNumericEncoder(start)
		require.NoError(t, err)
		require.NoError(t, encoder.StartMetricName(metricName, 3))
		for i := range 3 {
			require.NoError(t, encoder.AddDataPoint(start.UnixMicro()+int64(i), float64(b*10+i), ""))
		}
		require.NoError(t, encoder.EndMetric())
		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)
		blob, err := decoder.Decode()
		require.NoError(t, err)
		require.False(t, blob.HasMetricNames(), "members must carry no names payload")
		blobs = append(blobs, blob)
	}

	set, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)
	require.Equal(t, 6, set.MetricLenByName(metricName), "raw set resolves the name by hash")

	mat := set.Materialize()
	require.True(t, mat.HasMetricName(metricName))
	require.Equal(t, 6, mat.DataPointCountByName(metricName))
	for i, want := range []float64{0, 1, 2, 10, 11, 12} {
		val, ok := mat.ValueAtByName(metricName, i)
		require.Truef(t, ok, "ValueAtByName(%d)", i)
		require.Equalf(t, want, val, "ValueAtByName(%d)", i)

		_, ok = mat.TimestampAtByName(metricName, i)
		require.Truef(t, ok, "TimestampAtByName(%d)", i)
	}

	require.False(t, mat.HasMetricName("set.metric.absent"))
}

// TestNumericBlobSet_MaterializeKeepsColumnsAligned pins that when a member
// decodes fewer timestamps or values than its Count (a corrupt but Count-valid
// entry), set materialization trims that member's rows so every later point
// keeps its own timestamp, value and tag.
func TestNumericBlobSet_MaterializeKeepsColumnsAligned(t *testing.T) {
	opts := []NumericEncoderOption{WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeChimp)}
	first := encodeHardeningBlob(t, opts...)
	// Five points claimed for three encoded; still within the timestamp bytes.
	corrupt, err := decodeHardening(t, patchCompactCount(first, 0, 5))
	require.NoError(t, err)

	enc, err := NewNumericEncoder(time.Unix(1_700_003_600, 0).UTC(), opts...)
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricID(1, 2))
	require.NoError(t, enc.AddDataPoint(5000, 50, ""))
	require.NoError(t, enc.AddDataPoint(5010, 51, ""))
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)
	second, err := decodeHardening(t, data)
	require.NoError(t, err)

	set, err := NewNumericBlobSet([]NumericBlob{corrupt, second})
	require.NoError(t, err)

	metric, ok := set.MaterializeMetric(1)
	require.True(t, ok)
	require.Len(t, metric.Values, len(metric.Timestamps), "MaterializeMetric column lengths")
	last := len(metric.Timestamps) - 1
	require.Equal(t, int64(5010), metric.Timestamps[last], "MaterializeMetric last timestamp")
	require.Equal(t, 51.0, metric.Values[last], "MaterializeMetric last value")

	mat := set.Materialize()
	n := mat.DataPointCount(1)
	ts, ok := mat.TimestampAt(1, n-1)
	require.True(t, ok)
	val, ok := mat.ValueAt(1, n-1)
	require.True(t, ok)
	require.Equal(t, int64(5010), ts)
	require.Equal(t, 51.0, val)
}

// TestNumericBlobSet_MaterializeMetric_MatchesReference compares the materialize entry points with
// referenceMaterializeMetric on well-formed sets in the consumer's shape: counts that are equal and that differ,
// set sizes around eight blobs, one- and two-point columns, the columns at the end of a value payload,
// metrics that some or all blobs lack, near-constant columns and random bit patterns.
func TestNumericBlobSet_MaterializeMetric_MatchesReference(t *testing.T) {
	names := payloadOrderedNames(t, 5)

	t.Run("equal counts", func(t *testing.T) {
		blobs := decodeSetTestMembers(t, denseSetMembers(names, slices.Repeat([]int{30}, 16)))
		for _, name := range names {
			require.True(t, requireMaterializeMatchesReference(t, blobs, name).ok, name)
		}
		require.False(t, requireMaterializeMatchesReference(t, blobs, "absent").ok, "a metric in no blob")
	})

	t.Run("counts from 30 to 40", func(t *testing.T) {
		counts := make([]int, 40)
		for i := range counts {
			counts[i] = 30 + (i*7)%11
		}
		requireAllMatchReference(t, decodeSetTestMembers(t, denseSetMembers(names, counts)), names)
	})

	t.Run("blob counts around a group of eight", func(t *testing.T) {
		for _, numBlobs := range []int{1, 3, 4, 5, 7, 8, 9, 13, 20} {
			counts := make([]int, numBlobs)
			for i := range counts {
				counts[i] = 31 + i%5
			}
			requireAllMatchReference(t, decodeSetTestMembers(t, denseSetMembers(names, counts)), names)
		}
	})

	t.Run("one-point and two-point columns", func(t *testing.T) {
		for _, counts := range [][]int{
			slices.Repeat([]int{1}, 16),
			slices.Repeat([]int{2}, 16),
			{30, 1, 30, 2, 30, 1, 30, 30, 1, 1, 1, 30, 30, 1, 2, 1},
		} {
			requireAllMatchReference(t, decodeSetTestMembers(t, denseSetMembers(names, counts)), names)
		}
	})

	t.Run("columns at the end of the payload", func(t *testing.T) {
		for _, last := range []int{1, 2, 35} {
			members := denseSetMembers(names, slices.Repeat([]int{36}, 12))
			for i := range members {
				members[i].cols[4] = members[i].cols[4][:last]
			}
			blobs := decodeSetTestMembers(t, members)
			for i := range blobs {
				require.Zero(t, valueSlackAfter(&blobs[i], names[4]), "the last column ends the payload")
				if last == 1 {
					require.Equal(t, 8, valueSlackAfter(&blobs[i], names[3]), "the column before a one-point column ends 8 bytes early")
				}
			}
			requireAllMatchReference(t, blobs, names)
		}
	})

	t.Run("a metric missing from some blobs", func(t *testing.T) {
		members := denseSetMembers(names, slices.Repeat([]int{30}, 30))
		missing := 0
		for i := range members {
			if i%3 == 0 || i == 28 {
				members[i].names = slices.Delete(slices.Clone(members[i].names), 1, 2)
				members[i].cols = slices.Delete(slices.Clone(members[i].cols), 1, 2)
				missing++
			}
		}
		require.Equal(t, 11, missing)
		requireAllMatchReference(t, decodeSetTestMembers(t, members), names)
	})

	t.Run("constant and near-constant columns", func(t *testing.T) {
		members := denseSetMembers(names, slices.Repeat([]int{36}, 16))
		for i := range members {
			members[i].cols[0] = slices.Repeat([]float64{float64(i) + 0.25}, 36)
			members[i].cols[1] = setValueColumn(setKindNearConst, 36, 100+2*float64(i), int64(i)+1)
			for j := range 36 {
				members[i].cols[2][j] = 100.25 + float64(i) + float64(j/6)*0.01
				members[i].cols[3][j] = 100.25 + float64(i) + float64(j/3)*0.01
			}
		}
		requireAllMatchReference(t, decodeSetTestMembers(t, members), names)
	})

	t.Run("random bit patterns", func(t *testing.T) {
		rng := rand.New(rand.NewSource(41))
		for range 20 {
			members := make([]setTestMember, 17)
			for i := range members {
				n := 30 + rng.Intn(11)
				cols := make([][]float64, len(names))
				for k := range cols {
					cols[k] = randomBitColumn(rng, n)
				}
				members[i] = setTestMember{opts: consumerShapeOptions(format.TypeChimp), names: names, cols: cols}
			}
			requireAllMatchReference(t, decodeSetTestMembers(t, members), names)
		}
	})
}

// TestNumericBlobSet_MaterializeMetric_MatchesReferenceMixedEncodings compares the materialize entry points with
// referenceMaterializeMetric on a set whose Chimp members differ in byte order, layout, timestamps, tags and
// compression, interleaved with ALP, ALP-RLE, Gorilla and Raw members.
func TestNumericBlobSet_MaterializeMetric_MatchesReferenceMixedEncodings(t *testing.T) {
	names := payloadOrderedNames(t, 4)
	variants := mixedSetVariants()
	members := make([]setTestMember, 0, 36)
	chimpMembers := 0
	for i := range 36 {
		v := variants[mixedSetChimpVariants+(i/3)%(len(variants)-mixedSetChimpVariants)]
		if i%3 != 2 {
			v = variants[chimpMembers%mixedSetChimpVariants]
			chimpMembers++
		}
		v.names = names
		v.cols = make([][]float64, len(names))
		for k := range names {
			v.cols[k] = setValueColumn(setMixKind(k*25), 30+i%9, 100+float64(i*len(names)+k), int64(i*len(names)+k)+1)
		}
		members = append(members, v)
	}
	require.Equal(t, 24, chimpMembers)
	blobs := decodeSetTestMembers(t, members)
	encodings := make(map[format.EncodingType]int)
	for i := range blobs {
		encodings[blobs[i].ValueEncoding()]++
	}
	require.Equal(t, 24, encodings[format.TypeChimp], "Chimp members")
	for _, enc := range []format.EncodingType{format.TypeALP, format.TypeALPRLE, format.TypeGorilla, format.TypeRaw} {
		require.Equal(t, 3, encodings[enc], "%v members", enc)
	}
	requireAllMatchReference(t, blobs, names)
}

// TestNumericBlobSet_MaterializeMetric_MatchesReferenceDamaged compares the materialize entry points with
// referenceMaterializeMetric on sets with damaged members, early and late in a group of eight:
// value bytes flipped before Decode, which accepts them, and index entries edited after it,
// with counts raised and lowered and value columns cut short.
func TestNumericBlobSet_MaterializeMetric_MatchesReferenceDamaged(t *testing.T) {
	names := payloadOrderedNames(t, 5)
	rng := rand.New(rand.NewSource(53))
	encodeSet := func() [][]byte {
		counts := make([]int, 21)
		for i := range counts {
			counts[i] = 30 + rng.Intn(11)
		}
		members := denseSetMembers(names, counts)
		encoded := make([][]byte, len(members))
		for i, m := range members {
			encoded[i] = encodeSetTestMember(t, i, m)
		}

		return encoded
	}
	decodeSet := func(encoded [][]byte) []NumericBlob {
		blobs := make([]NumericBlob, len(encoded))
		for i, data := range encoded {
			blobs[i] = decodeSetTestMember(t, data)
		}

		return blobs
	}

	t.Run("value bytes flipped", func(t *testing.T) {
		for range 12 {
			encoded := encodeSet()
			for _, i := range []int{1, 6, 11, rng.Intn(21)} {
				start, end := valuePayloadRange(t, encoded[i])
				for range 1 + rng.Intn(4) {
					encoded[i][start+rng.Intn(end-start)] ^= 1 << uint(rng.Intn(8))
				}
			}
			requireAllMatchReference(t, decodeSet(encoded), names)
		}
	})

	t.Run("index entries edited", func(t *testing.T) {
		for round := range 16 {
			blobs := decodeSet(encodeSet())
			for _, i := range []int{1, 6, 9, 14, 20} {
				entry := &blobs[i].index.sorted[rng.Intn(len(names))]
				switch (round + i) % 4 {
				case 0: // more points than the column holds
					entry.Count += 1 + rng.Intn(8)
				case 1: // fewer points than the column holds
					entry.Count = 1 + rng.Intn(entry.Count)
				case 2: // the column cut short
					entry.ValueLength = rng.Intn(entry.ValueLength + 1)
				default: // the column cut on a byte boundary, with every point still claimed
					entry.ValueLength = max(8, entry.ValueLength-1-rng.Intn(3))
				}
			}
			requireAllMatchReference(t, blobs, names)
		}
	})

	// A member that yields fewer values than its count leaves the tags it decoded past the rows it keeps.
	// A tagless member after it pads only the rows it yields, so those tags stay in the unused capacity.
	// Six complete members come first, so the two short members end a set of eight;
	// nothing is appended after them that could overwrite the tags left behind.
	t.Run("a short member without tags after a short member with tags", func(t *testing.T) {
		members := denseSetMembers(names, slices.Repeat([]int{30}, 8))
		members[6].tags = true
		blobs := decodeSetTestMembers(t, members)
		for i := range 6 {
			require.GreaterOrEqual(t, valueSlackAfter(&blobs[i], names[0]), 16, "member %d: the column is followed by another", i)
		}
		for _, i := range []int{6, 7} {
			blobs[i].index.entryByName(names[0]).ValueLength = 8 // the first value only
		}
		got := requireMaterializeMatchesReference(t, blobs, names[0])
		require.False(t, got.panicked)
		require.Len(t, got.metric.Values, 6*30+2)
		require.Equal(t, 8*30, cap(got.metric.Tags))
		require.NotEmpty(t, got.metric.Tags[:cap(got.metric.Tags)][6*30+2], "a tag of the short member with tags past the rows")
	})
}

// TestNumericBlobSet_MaterializeMetric_MatchesReferenceCrafted compares the materialize entry points with
// referenceMaterializeMetric on states made by editing decoded blobs, each built for one case:
// a decoder that reads its destination before writing it, after members that yield fewer rows than their count;
// a member with two faults; counts that do not fit, are negative, or overflow a sum.
func TestNumericBlobSet_MaterializeMetric_MatchesReferenceCrafted(t *testing.T) {
	const target = craftedTarget
	chimp := consumerShapeOptions(format.TypeChimp)
	alpRLE := consumerShapeOptions(format.TypeALPRLE)
	ownDelta := []NumericEncoderOption{
		WithLittleEndian(), WithBlobLayoutV2(), WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeChimp),
	}
	dense := func(i, n int) []float64 { return setValueColumn(setKindFullPrec, n, 1000+float64(i), int64(i)+1) }
	// No decimal exponent and factor represent runValue, so plain ALP stores every copy of it in full
	// and the ALP-RLE encoder writes one run instead.
	runValue := math.Pi * 1e300
	// complete returns n members of 30 dense Chimp points, numbered from first.
	complete := func(first, n int) []setTestMember {
		members := make([]setTestMember, n)
		for i := range members {
			members[i] = craftedMember(chimp, dense(first+i, 30))
		}

		return members
	}

	// The ALP-RLE member reads the value its runs expand from at the end of its own range,
	// which the baseline has not written yet: it returns zeros.
	t.Run("an ALP-RLE column cut at its nested column after a short member", func(t *testing.T) {
		members := []setTestMember{
			craftedMember(chimp, dense(0, 8)),
			craftedMember(chimp, []float64{101, 102, 103, 104, 105, 106, 107, 108}),
			craftedMember(alpRLE, slices.Repeat([]float64{runValue}, 4)),
		}
		members = append(members, complete(3, 8)...)
		blobs := decodeSetTestMembers(t, members)
		blobs[0].index.entryByName(target).ValueLength = 8 // eight values declared, one produced
		cutAtNestedColumn(t, &blobs[2], target)
		require.GreaterOrEqual(t, valueSlackAfter(&blobs[3], target), 16, "the target's column is followed by another")

		got := requireMaterializeMatchesReference(t, blobs, target)
		require.True(t, got.ok)
		require.Len(t, got.metric.Values, 1+8+4+8*30)
		require.Equal(t, []float64{101, 102, 103, 104, 105, 106, 107, 108}, got.metric.Values[1:9])
		for _, v := range got.metric.Values[9:13] {
			require.Zero(t, math.Float64bits(v), "the cut ALP-RLE member yields zeros")
		}
	})

	// The last member of the first group of eight leaves 35 decoded values past its rows;
	// the cut ALP-RLE member of the next group reads one of them and repeats it.
	t.Run("an ALP-RLE column cut at its nested column across groups", func(t *testing.T) {
		long := dense(7, 40)
		members := complete(0, 7)
		members = append(members,
			craftedMember(ownDelta, long),
			craftedMember(chimp, dense(8, 8)),
			craftedMember(alpRLE, slices.Repeat([]float64{runValue}, 20)),
		)
		members = append(members, complete(10, 6)...)
		blobs := decodeSetTestMembers(t, members)
		cutTimestampsTo(t, &blobs[7], target, 5)
		blobs[8].index.entryByName(target).ValueLength = 8
		cutAtNestedColumn(t, &blobs[9], target)
		for _, i := range []int{0, 7} {
			require.GreaterOrEqual(t, valueSlackAfter(&blobs[i], target), 16, "member %d: the target's column is followed by another", i)
		}

		got := requireMaterializeMatchesReference(t, blobs, target)
		require.True(t, got.ok)
		off := 7*30 + 5 + 1
		require.Len(t, got.metric.Values, off+20+6*30)
		for _, v := range got.metric.Values[off : off+20] {
			require.Equal(t, math.Float64bits(long[25]), math.Float64bits(v), "the cut ALP-RLE member repeats a left-over value")
		}
	})

	// The timestamp range is checked first, so the panic is the timestamp range's, not the value range's.
	t.Run("a member with a timestamp range and a value range outside its payloads", func(t *testing.T) {
		build := func(timestampFault bool) []NumericBlob {
			members := make([]setTestMember, 10)
			for i := range members {
				members[i] = craftedMember(ownDelta, dense(i, 30))
			}
			blobs := decodeSetTestMembers(t, members)
			entry := blobs[3].index.entryByName(target)
			entry.ValueOffset = cap(blobs[3].valPayload) + 3
			if timestampFault {
				entry.TimestampOffset = cap(blobs[3].tsPayload) + 1
			}

			return blobs
		}
		valueOnly := requireMaterializeMatchesReference(t, build(false), target)
		both := requireMaterializeMatchesReference(t, build(true), target)
		require.True(t, valueOnly.panicked)
		require.True(t, both.panicked)
		require.NotEqual(t, valueOnly.panicVal, both.panicVal)
	})

	// Counts 8 × 8 and -16 give a capacity of 48. The first member yields one row,
	// so the seventh member needs rows up to 49 and the baseline panics there.
	t.Run("a count that does not fit, behind a short member", func(t *testing.T) {
		members := make([]setTestMember, 9)
		for i := range members {
			members[i] = craftedMember(ownDelta, dense(i, 8))
		}
		blobs := decodeSetTestMembers(t, members)
		cutTimestampsTo(t, &blobs[0], target, 1)
		blobs[8].index.entryByName(target).Count = -16

		got := requireMaterializeMatchesReference(t, blobs, target)
		require.Equal(t, "runtime.boundsError: runtime error: slice bounds out of range [:49] with capacity 48", got.panicVal)
	})

	t.Run("a negative count in a group of eight", func(t *testing.T) {
		for _, at := range []int{0, 3, 7} {
			blobs := decodeSetTestMembers(t, complete(0, 12))
			blobs[at].index.entryByName(target).Count = -5
			requireMaterializeMatchesReference(t, blobs, target)
		}
	})

	// A count of the largest int inside a group, cancelled by a negative count after it,
	// keeps the capacity small while a sum of the group's counts overflows.
	t.Run("a count whose sum with the group's overflows", func(t *testing.T) {
		blobs := decodeSetTestMembers(t, complete(0, 12))
		blobs[2].index.entryByName(target).Count = math.MaxInt
		blobs[9].index.entryByName(target).Count = -math.MaxInt

		got := requireMaterializeMatchesReference(t, blobs, target)
		require.True(t, got.panicked)
	})

	// The same overflowing count, behind a first member whose timestamp range lies outside its payload.
	// Members are handled in order, so the panic is the first member's timestamp range, not the overflow:
	// code that sized the group's ranges before reaching the first member would report the overflow instead.
	t.Run("a count whose sum with the group's overflows, behind a timestamp range outside its payload", func(t *testing.T) {
		build := func(timestampFault, overflow bool) []NumericBlob {
			members := make([]setTestMember, 12)
			for i := range members {
				members[i] = craftedMember(ownDelta, dense(i, 30))
			}
			blobs := decodeSetTestMembers(t, members)
			if timestampFault {
				blobs[0].index.entryByName(target).TimestampOffset = cap(blobs[0].tsPayload) + 1
			}
			if overflow {
				blobs[2].index.entryByName(target).Count = math.MaxInt
				blobs[9].index.entryByName(target).Count = -math.MaxInt
			}

			return blobs
		}
		timestampOnly := requireMaterializeMatchesReference(t, build(true, false), target)
		overflowOnly := requireMaterializeMatchesReference(t, build(false, true), target)
		both := requireMaterializeMatchesReference(t, build(true, true), target)
		require.True(t, timestampOnly.panicked)
		require.True(t, overflowOnly.panicked)
		require.NotEqual(t, timestampOnly.panicVal, overflowOnly.panicVal)
		require.Equal(t, timestampOnly.panicVal, both.panicVal, "the first member's timestamp range panics first")
	})
}

// FuzzNumericBlobSetMaterializeMetric compares the four entry points that materialize one metric of a set with
// referenceMaterializeMetric, on sets the input describes.
// The input is read as a program: up to 24 members, each with a variant of mixedSetVariants, a count of 1 to 45
// and a kind of values, then up to 32 damage steps.
// A step flips a bit of a member's encoded value, timestamp or tag payload before Decode,
// or edits an index entry of a decoded member: its count, value length, timestamp length or tag length,
// or an ALP-RLE column cut at its nested column. A member Decode rejects is left out of the set.
func FuzzNumericBlobSetMaterializeMetric(f *testing.F) {
	for _, seed := range materializeFuzzSeeds(f) {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, program []byte) {
		blobs := buildMaterializeFuzzSet(t, program)
		if len(blobs) == 0 {
			return
		}
		requireAllMatchReference(t, blobs, materializeFuzzNames)
	})
}

// TestNumericBlobSet_MaterializeMetric_FuzzSeedsHoldCraftedSets pins that the last two seeds of
// FuzzNumericBlobSetMaterializeMetric describe the crafted ALP-RLE sets: the reference returns zeros for the column
// cut after a short member, and repeats a value left over past the rows of the last member of a group of eight.
func TestNumericBlobSet_MaterializeMetric_FuzzSeedsHoldCraftedSets(t *testing.T) {
	seeds := materializeFuzzSeeds(t)
	name := materializeFuzzNames[materializeFuzzSeedMetric]

	shortSet := buildMaterializeFuzzSet(t, seeds[len(seeds)-2])
	require.GreaterOrEqual(t, valueSlackAfter(&shortSet[3], name), 16, "the damaged metric's column is followed by another")
	short := requireMaterializeMatchesReference(t, shortSet, name)
	require.True(t, short.ok)
	require.Len(t, short.metric.Values, 1+8+4+8*30)
	for _, v := range short.metric.Values[9:13] {
		require.Zero(t, math.Float64bits(v), "the cut ALP-RLE member yields zeros")
	}

	acrossSet := buildMaterializeFuzzSet(t, seeds[len(seeds)-1])
	for _, i := range []int{0, 7} {
		require.GreaterOrEqual(t, valueSlackAfter(&acrossSet[i], name), 16, "member %d: the damaged metric's column is followed by another", i)
	}
	across := requireMaterializeMatchesReference(t, acrossSet, name)
	require.True(t, across.ok)
	off := 7*30 + 5 + 1
	require.Len(t, across.metric.Values, off+20+6*30)
	leftOver := materializeFuzzColumn(byte(setKindFullPrec), 7, materializeFuzzSeedMetric, 40)[25]
	for _, v := range across.metric.Values[off : off+20] {
		require.Equal(t, math.Float64bits(leftOver), math.Float64bits(v), "the cut ALP-RLE member repeats a left-over value")
	}
}

// referenceMaterializeMetric is a frozen copy of NumericBlobSet.materializeMetricCore, kept so that a change to
// that function can be compared with the code it replaced: the body below is copied verbatim and is never edited.
// Every entry point that reaches materializeMetricCore must return what this returns, element for element over
// the whole capacity of each slice, and panic where this panics, with the same value.
func referenceMaterializeMetric(s *NumericBlobSet, metricID uint64, resolve func(blob *NumericBlob) (section.NumericIndexEntry, bool)) (MaterializedNumericMetric, bool) {
	// Step 1: Check if metric exists in any blob and calculate total capacity
	capacity := 0
	for i := range s.blobs {
		blob := &s.blobs[i]
		if entry, ok := resolve(blob); ok {
			capacity += entry.Count
		}
	}

	// If metric not found in any blob, return false
	if capacity == 0 {
		return MaterializedNumericMetric{}, false
	}

	// Step 2: Check if any blob has tags enabled
	hasTags := false
	for i := range s.blobs {
		if s.blobs[i].HasTag() {
			hasTags = true
			break
		}
	}

	// Step 3: Pre-allocate slices with exact capacity
	timestamps := make([]int64, 0, capacity)
	values := make([]float64, 0, capacity)
	var tags []string
	if hasTags {
		tags = make([]string, 0, capacity)
	}

	// Step 4: Iterate through blobs in chronological order, appending data
	for i := range s.blobs {
		blob := &s.blobs[i]
		entry, ok := resolve(blob)
		if !ok {
			continue // This metric doesn't contribute to this logical metric
		}

		count := entry.Count

		// Decode timestamps: extend slice and decode directly into tail
		tsOff := len(timestamps)
		if cached := blob.sharedTs.lookup(entry.TimestampOffset); cached != nil {
			timestamps = append(timestamps, cached...)
		} else {
			tsBytes := blob.tsPayload[entry.TimestampOffset : entry.TimestampOffset+entry.TimestampLength]
			timestamps = timestamps[:tsOff+count]
			tsProduced := blob.decodeTimestampsSlice(tsBytes, count, timestamps[tsOff:])
			timestamps = timestamps[:tsOff+tsProduced]
		}

		// Decode values: extend slice and decode directly into tail
		valBytes := blob.valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength]
		valOff := len(values)
		values = values[:valOff+count]
		valProduced := blob.decodeValuesSlice(valBytes, count, values[valOff:])
		values = values[:valOff+valProduced]

		// Decode and append tags (if enabled)
		if hasTags && blob.HasTag() {
			tagOff := len(tags)
			tags = slices.Grow(tags, count)[:tagOff+count]
			tags = tags[:tagOff+blob.decodeTagsInto(entry, tags[tagOff:])]
		} else if hasTags {
			// This blob doesn't have tags, but other blobs do
			// Fill with empty strings to maintain index alignment
			for range valProduced {
				tags = append(tags, "")
			}
		}

		timestamps, values, tags = alignMemberRows(timestamps, values, tags, hasTags)
	}

	return MaterializedNumericMetric{
		MetricID:   metricID,
		Timestamps: timestamps,
		Values:     values,
		Tags:       tags,
	}, true
}

// referenceMaterializeMetricByName is NumericBlobSet.MaterializeMetricByName over referenceMaterializeMetric.
func referenceMaterializeMetricByName(s *NumericBlobSet, metricName string) (MaterializedNumericMetric, bool) {
	var metricID uint64
	found := false
	for i := range s.blobs {
		if entry, ok := s.blobs[i].index.GetByName(metricName); ok {
			metricID = entry.MetricID
			found = true

			break
		}
	}
	if !found {
		return MaterializedNumericMetric{}, false
	}

	skipStripped := s.identity.excludesStripped(metricName)

	return referenceMaterializeMetric(s, metricID, func(blob *NumericBlob) (section.NumericIndexEntry, bool) {
		return blob.index.resolveEntryByName(metricName, skipStripped)
	})
}

// referenceMaterializeMetricByID is NumericBlobSet.MaterializeMetric over referenceMaterializeMetric.
func referenceMaterializeMetricByID(s *NumericBlobSet, metricID uint64) (MaterializedNumericMetric, bool) {
	targetName, collided := s.identity.resolveID(metricID)

	return referenceMaterializeMetric(s, metricID, func(blob *NumericBlob) (section.NumericIndexEntry, bool) {
		return blob.index.resolveEntry(metricID, targetName, collided)
	})
}

// catchMaterialize runs call and records its result or its panic.
func catchMaterialize(call func() (MaterializedNumericMetric, bool)) (out materializeOutcome) {
	defer func() {
		if r := recover(); r != nil {
			out = materializeOutcome{panicked: true, panicVal: fmt.Sprintf("%T: %v", r, r)}
		}
	}()
	out.metric, out.ok = call()

	return out
}

// requireSameFullSlice requires got to match want in nil-ness, length and capacity,
// and element for element over the whole capacity, not only the length.
func requireSameFullSlice[T any](t *testing.T, what string, want, got []T, same func(a, b T) bool) {
	t.Helper()
	require.Equal(t, want == nil, got == nil, "%s: nil", what)
	require.Equal(t, len(want), len(got), "%s: length", what)
	require.Equal(t, cap(want), cap(got), "%s: capacity", what)
	length := len(got)
	want, got = want[:cap(want)], got[:cap(got)]
	for i := range want {
		if !same(want[i], got[i]) {
			require.Failf(t, "slices differ", "%s: element %d (length %d, capacity %d) is %v, want %v",
				what, i, length, cap(got), got[i], want[i])
		}
	}
}

// requireSameMaterialization requires got to be the outcome want is.
func requireSameMaterialization(t *testing.T, what string, want, got materializeOutcome) {
	t.Helper()
	require.Equal(t, want.panicked, got.panicked, "%s: panicked (want %q, got %q)", what, want.panicVal, got.panicVal)
	if want.panicked {
		require.Equal(t, want.panicVal, got.panicVal, "%s: panic value", what)

		return
	}
	require.Equal(t, want.ok, got.ok, "%s: ok", what)
	require.Equal(t, want.metric.MetricID, got.metric.MetricID, "%s: metric ID", what)
	same := func(a, b int64) bool { return a == b }
	requireSameFullSlice(t, what+": timestamps", want.metric.Timestamps, got.metric.Timestamps, same)
	requireSameFullSlice(t, what+": tags", want.metric.Tags, got.metric.Tags, func(a, b string) bool { return a == b })
	requireSameFullSlice(t, what+": value bits", want.metric.Values, got.metric.Values, func(a, b float64) bool {
		return math.Float64bits(a) == math.Float64bits(b)
	})
}

// requireMaterializeMatchesReference materializes the named metric of a set of the given blobs through the four
// public entry points, NumericBlobSet.MaterializeMetricByName and MaterializeMetric and
// BlobSet.MaterializeNumericMetricByName and MaterializeNumericMetric, and requires each to return or panic exactly as
// referenceMaterializeMetric does with the resolve function its entry point builds.
// The ID entry points get the ID the by-name ones resolve, or the name's hash when no blob has the name:
// MaterializeMetricByName returns before materializeMetricCore for an absent name, and MaterializeMetric does not.
// It returns the reference's outcome by name, so a fixture can pin the result it was built for.
func requireMaterializeMatchesReference(t *testing.T, blobs []NumericBlob, name string) materializeOutcome {
	t.Helper()
	numericSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)
	blobSet := NewBlobSet(blobs, nil)

	metricID := hash.ID(name)
	for i := range numericSet.blobs {
		if entry, ok := numericSet.blobs[i].index.GetByName(name); ok {
			metricID = entry.MetricID

			break
		}
	}

	byName := catchMaterialize(func() (MaterializedNumericMetric, bool) {
		return referenceMaterializeMetricByName(&numericSet, name)
	})
	byID := catchMaterialize(func() (MaterializedNumericMetric, bool) {
		return referenceMaterializeMetricByID(&numericSet, metricID)
	})
	calls := []struct {
		what string
		want materializeOutcome
		call func() (MaterializedNumericMetric, bool)
	}{
		{"NumericBlobSet.MaterializeMetricByName", byName, func() (MaterializedNumericMetric, bool) {
			return numericSet.MaterializeMetricByName(name)
		}},
		{"BlobSet.MaterializeNumericMetricByName", byName, func() (MaterializedNumericMetric, bool) {
			return blobSet.MaterializeNumericMetricByName(name)
		}},
		{"NumericBlobSet.MaterializeMetric", byID, func() (MaterializedNumericMetric, bool) {
			return numericSet.MaterializeMetric(metricID)
		}},
		{"BlobSet.MaterializeNumericMetric", byID, func() (MaterializedNumericMetric, bool) {
			return blobSet.MaterializeNumericMetric(metricID)
		}},
	}
	for _, c := range calls {
		requireSameMaterialization(t, name+" through "+c.what, c.want, catchMaterialize(c.call))
	}

	return byName
}

// requireAllMatchReference runs requireMaterializeMatchesReference for every name and for a name no blob has.
func requireAllMatchReference(t *testing.T, blobs []NumericBlob, names []string) {
	t.Helper()
	for _, name := range names {
		requireMaterializeMatchesReference(t, blobs, name)
	}
	requireMaterializeMatchesReference(t, blobs, "absent")
}

// consumerShapeOptions are the options of a blob in the consumer's shape with the given value encoding:
// little endian, V2 layout, shared DeltaPacked timestamps, no compression.
func consumerShapeOptions(valEnc format.EncodingType) []NumericEncoderOption {
	return []NumericEncoderOption{
		WithLittleEndian(), WithBlobLayoutV2(), WithSharedTimestamps(),
		WithTimestampEncoding(format.TypeDeltaPacked), WithTimestampCompression(format.CompressionNone),
		WithValueEncoding(valEnc), WithValueCompression(format.CompressionNone),
	}
}

// mixedSetVariants returns member options a test set may mix.
// The first mixedSetChimpVariants are Chimp, in both byte orders, both layouts, with shared and own timestamps,
// with tags and with zstd, S2 and LZ4; the others hold ALP, ALP-RLE, Gorilla and Raw values.
func mixedSetVariants() []setTestMember {
	chimp := WithValueEncoding(format.TypeChimp)
	opts := func(o ...NumericEncoderOption) []NumericEncoderOption { return o }

	return []setTestMember{
		{opts: consumerShapeOptions(format.TypeChimp)},
		{opts: opts(chimp, WithBigEndian(), WithBlobLayoutV2(), WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked))},
		{opts: opts(chimp, WithTimestampEncoding(format.TypeDelta))},
		{opts: opts(chimp, WithTimestampEncoding(format.TypeRaw), WithBigEndian())},
		{opts: opts(chimp, WithBlobLayoutV2(), WithTimestampEncoding(format.TypeDeltaPacked)), tags: true},
		{opts: opts(chimp, WithSharedTimestamps(), WithTimestampEncoding(format.TypeDelta), WithValueCompression(format.CompressionZstd))},
		{opts: opts(chimp, WithTimestampEncoding(format.TypeDeltaPacked), WithValueCompression(format.CompressionS2),
			WithTimestampCompression(format.CompressionLZ4))},
		{opts: opts(chimp, WithTimestampEncoding(format.TypeDelta), WithValueCompression(format.CompressionLZ4)), tags: true},

		{opts: opts(WithValueEncoding(format.TypeALP), WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked))},
		{opts: opts(WithValueEncoding(format.TypeALPRLE), WithBlobLayoutV2(), WithTimestampEncoding(format.TypeDelta)), tags: true},
		{opts: opts(WithValueEncoding(format.TypeGorilla), WithBigEndian(), WithTimestampEncoding(format.TypeDelta))},
		{opts: opts(WithValueEncoding(format.TypeRaw), WithTimestampEncoding(format.TypeRaw))},
	}
}

// encodeSetTestMember encodes member i of a test set; members start an hour apart, one point a second.
func encodeSetTestMember(tb testing.TB, i int, m setTestMember) []byte {
	tb.Helper()
	start := time.Unix(1_700_000_000+int64(i)*3600, 0).UTC()
	opts := m.opts
	if m.tags {
		opts = append(slices.Clone(opts), WithTagsEnabled(true))
	}
	enc, err := NewNumericEncoder(start, opts...)
	require.NoError(tb, err)
	for k, name := range m.names {
		require.NoError(tb, enc.StartMetricName(name, len(m.cols[k])))
		for j, v := range m.cols[k] {
			tag := ""
			if m.tags {
				tag = "host=" + strconv.Itoa(j%3)
			}
			require.NoError(tb, enc.AddDataPoint(start.UnixMicro()+int64(j)*1_000_000, v, tag))
		}
		require.NoError(tb, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(tb, err)

	return data
}

// decodeSetTestMember decodes one encoded member and fails if Decode rejects it.
func decodeSetTestMember(tb testing.TB, data []byte) NumericBlob {
	tb.Helper()
	dec, err := NewNumericDecoder(data)
	require.NoError(tb, err)
	blob, err := dec.Decode()
	require.NoError(tb, err)

	return blob
}

// decodeSetTestMembers encodes and decodes every member of a test set.
func decodeSetTestMembers(tb testing.TB, members []setTestMember) []NumericBlob {
	tb.Helper()
	blobs := make([]NumericBlob, len(members))
	for i, m := range members {
		blobs[i] = decodeSetTestMember(tb, encodeSetTestMember(tb, i, m))
	}

	return blobs
}

// denseSetMembers returns one member in the consumer's shape with Chimp values per count:
// member i holds every name with counts[i] full-precision points, each column from its own start and seed.
func denseSetMembers(names []string, counts []int) []setTestMember {
	members := make([]setTestMember, len(counts))
	for i, n := range counts {
		cols := make([][]float64, len(names))
		for k := range cols {
			u := i*len(names) + k
			cols[k] = setValueColumn(setKindFullPrec, n, 100+float64(u), int64(u)+1)
		}
		members[i] = setTestMember{opts: consumerShapeOptions(format.TypeChimp), names: names, cols: cols}
	}

	return members
}

// craftedMember returns a member that holds craftedTarget with col and craftedCompanion with the same count,
// so that a member with shared timestamps hands both the same timestamp group.
func craftedMember(opts []NumericEncoderOption, col []float64) setTestMember {
	other := make([]float64, len(col))
	for j := range other {
		other[j] = float64(j) + 0.5
	}

	return setTestMember{opts: opts, names: []string{craftedTarget, craftedCompanion}, cols: [][]float64{col, other}}
}

// payloadOrderedNames returns n metric names in the order their value columns take in a payload
// in the consumer's shape, which orders the columns by metric ID:
// names[n-1]'s column ends the payload of every member that holds all n names.
func payloadOrderedNames(tb testing.TB, n int) []string {
	tb.Helper()
	names := make([]string, n)
	for i := range names {
		names[i] = "set.test." + strconv.Itoa(i)
	}
	blob := decodeSetTestMembers(tb, denseSetMembers(names, []int{2}))[0]
	slices.SortFunc(names, func(a, b string) int {
		return blob.index.entryByName(a).ValueOffset - blob.index.entryByName(b).ValueOffset
	})

	return names
}

// valueSlackAfter returns how many bytes of the blob's value payload follow the named metric's column.
func valueSlackAfter(blob *NumericBlob, name string) int {
	entry := blob.index.entryByName(name)

	return len(blob.valPayload) - entry.ValueOffset - entry.ValueLength
}

// valuePayloadRange returns the byte range of an encoded blob's value payload.
func valuePayloadRange(tb testing.TB, data []byte) (start, end int) {
	tb.Helper()
	header, err := section.ParseNumericHeader(data)
	require.NoError(tb, err)
	start, end = int(header.ValuePayloadOffset), len(data)
	if tagStart := int(header.TagPayloadOffset); tagStart > start && tagStart < end {
		end = tagStart
	}
	require.Less(tb, start, end, "value payload")

	return start, end
}

// cutTimestampsTo shortens the named metric's own timestamp column to the shortest prefix that decodes to n timestamps.
func cutTimestampsTo(tb testing.TB, blob *NumericBlob, name string, n int) {
	tb.Helper()
	entry := blob.index.entryByName(name)
	require.Nil(tb, blob.sharedTs.lookup(entry.TimestampOffset), "the column must not be a shared group")
	dst := make([]int64, entry.Count)
	column := blob.tsPayload[entry.TimestampOffset : entry.TimestampOffset+entry.TimestampLength]
	for length := range len(column) {
		if blob.decodeTimestampsSlice(column[:length], entry.Count, dst) == n {
			entry.TimestampLength = length

			return
		}
	}
	require.FailNow(tb, "no prefix of the timestamp column decodes to the requested count", "%s: %d of %d", name, n, entry.Count)
}

// cutAtNestedColumn shortens the named metric's ALP-RLE column so that it ends where its nested column of run values
// begins: the scheme byte, the four-byte run count and the run bitmap stay whole, and no run value is left.
func cutAtNestedColumn(tb testing.TB, blob *NumericBlob, name string) {
	tb.Helper()
	entry := blob.index.entryByName(name)
	require.Equal(tb, format.TypeALPRLE, blob.ValueEncoding())
	require.Equal(tb, ienc.ALPRLEMaxSchemeByte, blob.valPayload[entry.ValueOffset], "the column has the runs layout")
	cut := 1 + 4 + (entry.Count+7)/8
	require.Greater(tb, entry.ValueLength, cut)
	entry.ValueLength = cut
}

// randomBitColumn returns n values of random bit patterns: every leading-zero bucket, 64-bit payloads and unchanged runs.
func randomBitColumn(rng *rand.Rand, n int) []float64 {
	col := make([]float64, n)
	for i := range col {
		switch rng.Intn(6) {
		case 0:
			col[i] = math.Float64frombits(rng.Uint64())
		case 1, 2:
			if i > 0 {
				col[i] = col[i-1]
			}
		case 3:
			col[i] = math.Float64frombits(rng.Uint64() << uint(rng.Intn(64)))
		default:
			if i > 0 {
				col[i] = math.Float64frombits(math.Float64bits(col[i-1]) ^ (rng.Uint64()>>uint(8+rng.Intn(56)))<<uint(rng.Intn(20)))
			}
		}
	}

	return col
}

// materializeFuzzSet decodes a program of FuzzNumericBlobSetMaterializeMetric into its members and damage steps.
func materializeFuzzSet(program []byte) ([]materializeFuzzMember, []materializeFuzzStep) {
	next := func() byte {
		if len(program) == 0 {
			return 0
		}
		b := program[0]
		program = program[1:]

		return b
	}
	members := make([]materializeFuzzMember, 1+int(next())%materializeFuzzMaxMembers)
	for i := range members {
		members[i] = materializeFuzzMember{variant: next(), count: next(), kind: next()}
	}
	var steps []materializeFuzzStep
	for len(program) >= materializeFuzzStepSize && len(steps) < materializeFuzzMaxSteps {
		steps = append(steps, materializeFuzzStep{op: next(), member: next(), metric: next(), arg: uint16(next()) | uint16(next())<<8})
	}

	return members, steps
}

// materializeFuzzProgram encodes members and damage steps as a program of FuzzNumericBlobSetMaterializeMetric.
func materializeFuzzProgram(members []materializeFuzzMember, steps []materializeFuzzStep) []byte {
	program := []byte{byte(len(members) - 1)}
	for _, m := range members {
		program = append(program, m.variant, m.count, m.kind)
	}
	for _, s := range steps {
		program = append(program, s.op, s.member, s.metric, byte(s.arg), byte(s.arg>>8))
	}

	return program
}

// materializeFuzzColumn returns the values of metric k of member i, n points of the given kind.
func materializeFuzzColumn(kind byte, i, k, n int) []float64 {
	u := i*len(materializeFuzzNames) + k
	switch kind % materializeFuzzKinds {
	case 0, 1, 2, 3, 4:
		return setValueColumn(setValueKind(kind%materializeFuzzKinds), n, 100+float64(u), int64(u)+1)
	case 5:
		return randomBitColumn(rand.New(rand.NewSource(int64(u)+1)), n)
	default: // one run of a value plain ALP stores in full, so ALP-RLE writes the runs layout
		return slices.Repeat([]float64{(math.Pi + float64(u)) * 1e300}, n)
	}
}

// materializeFuzzMemberBytes encodes member i of a fuzz program.
func materializeFuzzMemberBytes(tb testing.TB, i int, m materializeFuzzMember) []byte {
	tb.Helper()
	variants := mixedSetVariants()
	member := variants[int(m.variant)%len(variants)]
	n := 1 + int(m.count)%materializeFuzzMaxCount
	member.names = materializeFuzzNames
	member.cols = make([][]float64, len(materializeFuzzNames))
	for k := range member.cols {
		member.cols[k] = materializeFuzzColumn(m.kind, i, k, n)
	}

	return encodeSetTestMember(tb, i, member)
}

// buildMaterializeFuzzSet builds the members of a fuzz program, applies its damage steps,
// and returns the members Decode accepts, in order.
func buildMaterializeFuzzSet(tb testing.TB, program []byte) []NumericBlob {
	tb.Helper()
	members, steps := materializeFuzzSet(program)
	encoded := make([][]byte, len(members))
	for i, m := range members {
		encoded[i] = materializeFuzzMemberBytes(tb, i, m)
	}

	// Flips first, on the encoded bytes.
	for _, s := range steps {
		data := encoded[int(s.member)%len(encoded)]
		header, err := section.ParseNumericHeader(data)
		require.NoError(tb, err)
		var start, end int
		switch s.op % materializeFuzzOps {
		case 0:
			start, end = int(header.ValuePayloadOffset), int(header.TagPayloadOffset)
		case 1:
			start, end = int(header.TimestampPayloadOffset), int(header.ValuePayloadOffset)
		case 2:
			start, end = int(header.TagPayloadOffset), len(data)
		default:
			continue
		}
		if start < end && end <= len(data) {
			data[start+int(s.arg)%(end-start)] ^= 1 << (s.metric % 8)
		}
	}

	blobs := make([]NumericBlob, len(members))
	accepted := make([]bool, len(members))
	for i, data := range encoded {
		dec, err := NewNumericDecoder(data)
		if err != nil {
			continue
		}
		if blobs[i], err = dec.Decode(); err == nil {
			accepted[i] = true
		}
	}

	// Then the edits, on the decoded index entries.
	for _, s := range steps {
		i := int(s.member) % len(blobs)
		if !accepted[i] {
			continue
		}
		applyMaterializeFuzzEdit(&blobs[i], s)
	}

	kept := make([]NumericBlob, 0, len(blobs))
	for i := range blobs {
		if accepted[i] {
			kept = append(kept, blobs[i])
		}
	}

	return kept
}

// applyMaterializeFuzzEdit applies one edit step of a fuzz program to a decoded member.
// Counts move by at most 16 up, so a set's capacity stays small.
func applyMaterializeFuzzEdit(blob *NumericBlob, s materializeFuzzStep) {
	entry := blob.index.entryByName(materializeFuzzNames[int(s.metric)%len(materializeFuzzNames)])
	if entry == nil {
		return
	}
	arg := int(s.arg)
	switch s.op % materializeFuzzOps {
	case 3:
		entry.Count += 1 + arg%16
	case 4:
		entry.Count = arg % (entry.Count + 1)
	case 5:
		entry.ValueLength = arg % (entry.ValueLength + 1)
	case 6:
		entry.TimestampLength = arg % (entry.TimestampLength + 1)
	case 7:
		entry.TagLength = arg % (entry.TagLength + 1)
	case 8:
		cut := 1 + 4 + (entry.Count+7)/8
		if blob.ValueEncoding() == format.TypeALPRLE && entry.ValueLength > cut &&
			blob.valPayload[entry.ValueOffset] == ienc.ALPRLEMaxSchemeByte {
			entry.ValueLength = cut
		}
	default:
	}
}

// materializeFuzzSeeds returns the seed programs of FuzzNumericBlobSetMaterializeMetric:
// the fixtures of the reference tests that a program can describe.
func materializeFuzzSeeds(tb testing.TB) [][]byte {
	tb.Helper()
	const (
		consumer, ownDelta, alpRLE = 0, 2, 9
		fullPrec, counter, run     = byte(setKindFullPrec), byte(setKindCounter), 6
	)
	member := func(variant, count int, kind byte) materializeFuzzMember {
		return materializeFuzzMember{variant: byte(variant), count: byte(count - 1), kind: kind}
	}
	repeat := func(n, count int, kind byte) []materializeFuzzMember {
		return slices.Repeat([]materializeFuzzMember{member(consumer, count, kind)}, n)
	}
	// The crafted steps edit materializeFuzzSeedMetric, whose column is neither the first nor the last of a payload.
	cutValue := func(i int) materializeFuzzStep {
		return materializeFuzzStep{op: 5, member: byte(i), metric: materializeFuzzSeedMetric, arg: 8}
	}
	cutRuns := func(i int) materializeFuzzStep {
		return materializeFuzzStep{op: 8, member: byte(i), metric: materializeFuzzSeedMetric}
	}

	var seeds [][]byte
	seeds = append(seeds,
		materializeFuzzProgram(repeat(16, 30, fullPrec), nil),
		materializeFuzzProgram(repeat(9, 33, counter), nil),
		materializeFuzzProgram(repeat(16, 1, fullPrec), nil),
		materializeFuzzProgram(repeat(16, 2, fullPrec), nil),
		materializeFuzzProgram(repeat(16, 36, byte(setKindNearConst)), nil),
		materializeFuzzProgram(repeat(16, 36, byte(setKindConstant)), nil),
		materializeFuzzProgram(repeat(17, 35, 5), nil),
	)
	mixed := make([]materializeFuzzMember, materializeFuzzMaxMembers)
	for i := range mixed {
		mixed[i] = member(i%len(mixedSetVariants()), 30+i%9, byte(i))
	}
	seeds = append(seeds,
		materializeFuzzProgram(mixed, nil),
		materializeFuzzProgram(repeat(21, 34, fullPrec), []materializeFuzzStep{
			{op: 0, member: 1, metric: 3, arg: 77}, {op: 0, member: 6, metric: 5, arg: 401},
			{op: 3, member: 9, arg: 5}, {op: 4, member: 14, metric: 1, arg: 17}, {op: 5, member: 20, metric: 2, arg: 30},
		}),
	)

	// A short member, then an ALP-RLE column cut at its nested column, then complete members.
	short := append([]materializeFuzzMember{member(consumer, 8, fullPrec), member(consumer, 8, counter), member(alpRLE, 4, run)},
		repeat(8, 30, fullPrec)...)
	seeds = append(seeds, materializeFuzzProgram(short, []materializeFuzzStep{cutValue(0), cutRuns(2)}))

	// The same across groups: the last member of the first eight keeps 5 of its 40 rows.
	across := append(repeat(7, 30, fullPrec), member(ownDelta, 40, fullPrec), member(consumer, 8, fullPrec), member(alpRLE, 20, run))
	across = append(across, repeat(6, 30, fullPrec)...)
	long := decodeSetTestMember(tb, materializeFuzzMemberBytes(tb, 7, across[7]))
	cutTimestampsTo(tb, &long, materializeFuzzNames[materializeFuzzSeedMetric], 5)
	tsLength := long.index.entryByName(materializeFuzzNames[materializeFuzzSeedMetric]).TimestampLength
	seeds = append(seeds, materializeFuzzProgram(across, []materializeFuzzStep{
		{op: 6, member: 7, metric: materializeFuzzSeedMetric, arg: uint16(tsLength)}, cutValue(8), cutRuns(9),
	}))

	return seeds
}
