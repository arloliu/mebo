package blob

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"slices"
	"strconv"
	"sync"
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
	panicRaw any    // the panic value itself
}

// chimpGroupUse is what chimpGroupObserve reported while one metric was materialized.
type chimpGroupUse struct {
	grouped int // calls of the hook: 1 when the grouped loop ran, 0 when the members were appended one at a time
	groups  int // groups of members the grouped loop asked to decode ahead
	ahead   int // groups it decoded ahead; in every other group no lane was filled
	columns int // columns that entered the kernel
	steps   int // steps the kernel completed
	calls   int // kernel calls
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

// TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReference compares the materialize entry points with
// referenceMaterializeMetric on well-formed sets in the consumer's shape: counts that are equal and that differ,
// set sizes around eight blobs, one- and two-point columns, the columns at the end of a value payload,
// and metrics that some or all blobs lack.
// It also pins what the group decode does for each: the groups of members the grouped loop asks to decode ahead
// and those it does decode ahead, the columns the kernel takes, the steps it completes and its calls.
// A group is decoded ahead when at least four of its columns can enter the kernel,
// and the first group that is not is the last one asked.
// The dense columns are full-precision walks between 64 and 512, whose values all fit the kernel's 64-bit window,
// so the kernel leaves a group only when fewer than four of its columns are unfinished.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReference(t *testing.T) {
	names := payloadOrderedNames(t, 5)

	// Two groups of eight columns of 30 values: 29 steps each after the first value, in one call each.
	t.Run("equal counts", func(t *testing.T) {
		blobs := decodeSetTestMembers(t, denseSetMembers(names, slices.Repeat([]int{30}, 16)))
		for _, name := range names {
			require.True(t, requireMaterializeMatchesReference(t, blobs, name).ok, name)
		}
		require.False(t, requireMaterializeMatchesReference(t, blobs, "absent").ok, "a metric in no blob")
		requireDenseSetUses(t, blobs, names, 4, chimpGroupUse{grouped: 1, groups: 2, ahead: 2, columns: 16, steps: 2 * 29, calls: 2})
	})

	// Five groups of eight.
	// Their counts in rising order, with the fourth-largest in brackets:
	// 30 32 33 35 [36] 37 39 40, 30 31 33 34 [36] 37 38 40, 30 31 32 34 [35] 37 38 39,
	// 31 32 33 35 [36] 38 39 40 and 30 32 33 34 [36] 37 39 40.
	// That is 35+35+34+35+35 steps, and five calls a group, one per count up to the bracket.
	t.Run("counts from 30 to 40", func(t *testing.T) {
		counts := make([]int, 40)
		for i := range counts {
			counts[i] = 30 + (i*7)%11
		}
		blobs := decodeSetTestMembers(t, denseSetMembers(names, counts))
		requireDenseSetUses(t, blobs, names, 4, chimpGroupUse{grouped: 1, groups: 5, ahead: 5, columns: 40, steps: 174, calls: 25})
	})

	// Member i holds 31+i%5 values, so a full group holds 31 31 32 32 [33] 33 34 35: 32 steps in three calls.
	// A set of one or three members takes the member-by-member loop.
	// The ninth member of nine is asked as a group of one, which is not decoded ahead.
	// Four members hold [31] 32 33 34, five [32], and seven 31 31 32 [32] 33 34 35.
	// The second group of 13 holds 31 [32] 33 34 35, that of 20 holds 31 31 32 33 [34] 34 35 35,
	// and its third group [32] 33 34 35.
	t.Run("blob counts around a group of eight", func(t *testing.T) {
		wants := map[int]chimpGroupUse{
			1:  {},
			3:  {},
			4:  {grouped: 1, groups: 1, ahead: 1, columns: 4, steps: 30, calls: 1},
			5:  {grouped: 1, groups: 1, ahead: 1, columns: 5, steps: 31, calls: 2},
			7:  {grouped: 1, groups: 1, ahead: 1, columns: 7, steps: 31, calls: 2},
			8:  {grouped: 1, groups: 1, ahead: 1, columns: 8, steps: 32, calls: 3},
			9:  {grouped: 1, groups: 2, ahead: 1, columns: 8, steps: 32, calls: 3},
			13: {grouped: 1, groups: 2, ahead: 2, columns: 13, steps: 32 + 31, calls: 3 + 2},
			20: {grouped: 1, groups: 3, ahead: 3, columns: 20, steps: 32 + 33 + 31, calls: 3 + 4 + 1},
		}
		for _, numBlobs := range []int{1, 3, 4, 5, 7, 8, 9, 13, 20} {
			counts := make([]int, numBlobs)
			for i := range counts {
				counts[i] = 31 + i%5
			}
			requireDenseSetUses(t, decodeSetTestMembers(t, denseSetMembers(names, counts)), names, 4, wants[numBlobs])
		}
	})

	// A one-point column cannot enter the kernel, so the first group of one-point columns is the only one asked.
	// Two-point columns take one step a group.
	// In the mixed set the first group has six columns of two or more values:
	// one step finishes the two-point column and 28 more the others.
	// The second group has three, too few for the kernel, and is not decoded ahead.
	// The fourth name's column has only the last name's behind it, 15 or 16 bytes when that one holds two points.
	t.Run("one-point and two-point columns", func(t *testing.T) {
		for _, c := range []struct {
			counts []int
			want   chimpGroupUse
		}{
			{slices.Repeat([]int{1}, 16), chimpGroupUse{grouped: 1, groups: 1}},
			{slices.Repeat([]int{2}, 16), chimpGroupUse{grouped: 1, groups: 2, ahead: 2, columns: 16, steps: 2, calls: 2}},
			{
				[]int{30, 1, 30, 2, 30, 1, 30, 30, 1, 1, 1, 30, 30, 1, 2, 1},
				chimpGroupUse{grouped: 1, groups: 2, ahead: 1, columns: 6, steps: 29, calls: 2},
			},
		} {
			requireDenseSetUses(t, decodeSetTestMembers(t, denseSetMembers(names, c.counts)), names, 3, c.want)
		}
	})

	// A group of eight and a group of four, all of 36 values: 35 steps each.
	// The last column has no byte after it, and the one before a one-point column has 8, so neither enters the kernel.
	// Before a two-point column there are 15 or 16 bytes, and before one of 35 points far more.
	t.Run("columns at the end of the payload", func(t *testing.T) {
		want := chimpGroupUse{grouped: 1, groups: 2, ahead: 2, columns: 12, steps: 2 * 35, calls: 2}
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
			roomy := 3
			if last == 35 {
				roomy = 4
			}
			requireDenseSetUses(t, blobs, names, roomy, want)
		}
	})

	// The second name is held by 19 members: groups of eight, eight and three, the last too small for the kernel.
	// The others are held by all 30: groups of eight, eight, eight and six.
	// The last name's column ends every payload, so its first group is not decoded ahead and no other is asked.
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
		blobs := decodeSetTestMembers(t, members)
		all := chimpGroupUse{grouped: 1, groups: 4, ahead: 4, columns: 30, steps: 4 * 29, calls: 4}
		requireMaterializeUses(t, blobs, names[0], all)
		requireMaterializeUses(t, blobs, names[1], chimpGroupUse{grouped: 1, groups: 3, ahead: 2, columns: 16, steps: 2 * 29, calls: 2})
		requireMaterializeUses(t, blobs, names[2], all)
		requireMaterializeUses(t, blobs, names[3], all)
		requireMaterializeUses(t, blobs, names[4], chimpGroupUse{grouped: 1, groups: 1})
		requireMaterializeUses(t, blobs, "absent", chimpGroupUse{})
	})

	// The second name is held by the first eight of ten members: one group, decoded ahead.
	// The two blobs behind it hold no member of the metric, and no group is asked for them.
	t.Run("a metric missing from the last blobs", func(t *testing.T) {
		members := denseSetMembers(names, slices.Repeat([]int{30}, 10))
		for _, i := range []int{8, 9} {
			members[i].names = slices.Delete(slices.Clone(members[i].names), 1, 2)
			members[i].cols = slices.Delete(slices.Clone(members[i].cols), 1, 2)
		}
		blobs := decodeSetTestMembers(t, members)
		requireMaterializeUses(t, blobs, names[1], chimpGroupUse{grouped: 1, groups: 1, ahead: 1, columns: 8, steps: 29, calls: 1})
		requireMaterializeUses(t, blobs, names[0], chimpGroupUse{grouped: 1, groups: 2, ahead: 1, columns: 8, steps: 29, calls: 1})
	})

	// The grouped loop is chosen by the Chimp members that hold the metric, not by those in the set:
	// a metric three of eight members hold is appended member by member.
	t.Run("a metric held by three of eight members", func(t *testing.T) {
		members := denseSetMembers(names, slices.Repeat([]int{30}, 8))
		for _, i := range []int{0, 1, 3, 5, 6} {
			members[i].names = slices.Delete(slices.Clone(members[i].names), 1, 2)
			members[i].cols = slices.Delete(slices.Clone(members[i].cols), 1, 2)
		}
		blobs := decodeSetTestMembers(t, members)
		requireMaterializeUses(t, blobs, names[0], chimpGroupUse{grouped: 1, groups: 1, ahead: 1, columns: 8, steps: 29, calls: 1})
		got := requireMaterializeUses(t, blobs, names[1], chimpGroupUse{})
		require.Len(t, got.metric.Values, 3*30)
	})
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceValueKinds compares the materialize entry points with
// referenceMaterializeMetric on well-formed sets whose columns the kernel does not take whole:
// constant and near-constant columns, which a group leaves to the scalar loop, and random bit patterns.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceValueKinds(t *testing.T) {
	names := payloadOrderedNames(t, 5)

	// A group enters the kernel only when its columns average 12 bits a value after the first.
	// An unchanged value takes 2 bits, and a changed value of these two-decimal columns about 45 and at most 53.
	// The constant column has 2 bits a value, the near-constant one about 4 with a change in 20 values,
	// the column that changes every sixth value at most (5*53+30*2)/35 = 9.3, and all three stay out.
	// Their first group is not decoded ahead, and it is the only one asked; so is the first group of the last name's column,
	// which ends every payload.
	// The column that changes every third value has about (11*45+24*2)/35 = 15.5 and enters:
	// two groups of eight columns of 36 values.
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
		blobs := decodeSetTestMembers(t, members)
		for _, name := range []string{names[0], names[1], names[2], names[4]} {
			requireMaterializeUses(t, blobs, name, chimpGroupUse{grouped: 1, groups: 1})
		}
		requireMaterializeUses(t, blobs, names[3], chimpGroupUse{grouped: 1, groups: 2, ahead: 2, columns: 16, steps: 2 * 35, calls: 2})
		requireMaterializeUses(t, blobs, "absent", chimpGroupUse{})
	})

	// Every column holds 30 values or more and, but for the last name's, has another column of at least 16 bytes
	// behind it, and random patterns take far more than 12 bits a value: the two full groups of every set enter the kernel.
	// How far the kernel gets depends on the patterns, many of which are wider than its window, so the steps are not pinned.
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
			blobs := decodeSetTestMembers(t, members)
			for _, name := range names[:4] {
				requireKernelColumns(t, blobs, name, 16, 16)
			}
			requireMaterializeUses(t, blobs, names[4], chimpGroupUse{grouped: 1, groups: 1})
			requireMaterializeUses(t, blobs, "absent", chimpGroupUse{})
		}
	})
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceMixedEncodings compares the materialize entry points with
// referenceMaterializeMetric on a set whose Chimp members differ in byte order, layout, timestamps, tags and
// compression, interleaved with ALP, ALP-RLE, Gorilla and Raw members.
// The Chimp members share the kernel whatever their variant, and the members between them do not break a group.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceMixedEncodings(t *testing.T) {
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

	// Every third member holds another encoding, and member i holds 30+i%9 values.
	// The Chimp members of the four full groups, with the fourth-largest count in brackets:
	// 30 31 [33] 34 36 37, 30 [31] 33 34 36, 30 [31] 33 34 37 and 30 31 [33] 34 36 37.
	// That is 22 columns, 32+30+30+32 steps and 3+2+2+3 calls.
	// The last group has two Chimp members, too few: it is asked and not decoded ahead.
	// The last name's column ends every payload, so its first group is not decoded ahead and no other is asked.
	// The gauges and the counter of these columns take 12 bits a value or more and fit the kernel's window.
	for _, name := range names[:3] {
		for i := range blobs {
			if blobs[i].ValueEncoding() == format.TypeChimp {
				require.GreaterOrEqual(t, valueSlackAfter(&blobs[i], name), 16, "member %d: %s is followed by another column", i, name)
			}
		}
		requireMaterializeUses(t, blobs, name, chimpGroupUse{grouped: 1, groups: 5, ahead: 4, columns: 22, steps: 124, calls: 10})
	}
	requireMaterializeUses(t, blobs, names[3], chimpGroupUse{grouped: 1, groups: 1})
	requireMaterializeUses(t, blobs, "absent", chimpGroupUse{})

	// A value payload can have capacity past its length: the tag payload of an uncompressed blob with tags,
	// or what a decompressor left over.
	// The room after a column is counted to the length only, so the last column of eight such members stays out
	// of the kernel, and the column before it enters: one group of eight columns of 32 values.
	for _, v := range []setTestMember{variants[4], variants[7]} {
		members = members[:0]
		for i := range 8 {
			v.names = names
			v.cols = make([][]float64, len(names))
			for k := range names {
				v.cols[k] = setValueColumn(setKindFullPrec, 32, 100+float64(i*len(names)+k), int64(i*len(names)+k)+1)
			}
			members = append(members, v)
		}
		blobs = decodeSetTestMembers(t, members)
		for i := range blobs {
			require.Zero(t, valueSlackAfter(&blobs[i], names[3]), "member %d: the last column ends the payload", i)
			require.GreaterOrEqual(t, cap(blobs[i].valPayload), len(blobs[i].valPayload)+16, "member %d: capacity past the length", i)
		}
		requireMaterializeUses(t, blobs, names[3], chimpGroupUse{grouped: 1, groups: 1})
		requireMaterializeUses(t, blobs, names[2], chimpGroupUse{grouped: 1, groups: 1, ahead: 1, columns: 8, steps: 31, calls: 1})
	}
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceDamaged compares the materialize entry points with
// referenceMaterializeMetric on sets with damaged members, early and late in a group of eight:
// value bytes flipped before Decode, which accepts them, and index entries edited after it,
// with counts raised and lowered and value columns cut short.
// A damaged member that comes up short moves the groups behind it, so only the first group of a set is pinned.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceDamaged(t *testing.T) {
	names := payloadOrderedNames(t, 5)
	rng := rand.New(rand.NewSource(53))
	// requireFirstGroup compares every name with the reference and requires that the kernel took at least
	// minColumns columns of each name but the last, and none of the last name's.
	// That column ends its payload; a cut leaves room behind it in at most two members of any eight, too few for the kernel.
	requireFirstGroup := func(blobs []NumericBlob, minColumns int) {
		t.Helper()
		for _, name := range names[:4] {
			requireKernelColumns(t, blobs, name, minColumns, len(blobs))
		}
		requireKernelColumns(t, blobs, names[4], 0, 0)
		requireMaterializeUses(t, blobs, "absent", chimpGroupUse{})
	}
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

	// A flipped bit changes no count and no length, so the eight columns of the first group still enter the kernel.
	t.Run("value bytes flipped", func(t *testing.T) {
		for range 12 {
			encoded := encodeSet()
			for _, i := range []int{1, 6, 11, rng.Intn(21)} {
				start, end := valuePayloadRange(t, encoded[i])
				for range 1 + rng.Intn(4) {
					encoded[i][start+rng.Intn(end-start)] ^= 1 << uint(rng.Intn(8))
				}
			}
			requireFirstGroup(decodeSet(encoded), 8)
		}
	})

	// Members 1 and 6 of the first group each have one entry edited, which can keep its column out of the kernel:
	// at least six columns of every name are left to enter it.
	// Members 1, 6, 9, 14 and 20 are edited, and no eight members in a row hold more than two of them.
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
			requireFirstGroup(blobs, 6)
		}
	})

	// A member that yields fewer values than its count leaves the tags it decoded past the rows it keeps.
	// A tagless member after it pads only the rows it yields, so those tags stay in the unused capacity.
	// Six complete members come first, so the two short members end a set of eight;
	// nothing is appended after them that could overwrite the tags left behind.
	// The six whole columns are enough to decode the group ahead, and they enter the kernel.
	// The two cut columns end with their first value: they claim more values than they have bits and stay scalar.
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
		got := requireMaterializeUses(t, blobs, names[0],
			chimpGroupUse{grouped: 1, groups: 1, ahead: 1, columns: 6, steps: 29, calls: 1})
		require.False(t, got.panicked)
		require.Len(t, got.metric.Values, 6*30+2)
		require.Equal(t, 8*30, cap(got.metric.Tags))
		require.NotEmpty(t, got.metric.Tags[:cap(got.metric.Tags)][6*30+2], "a tag of the short member with tags past the rows")
	})
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceCrafted compares the materialize entry points with
// referenceMaterializeMetric on states made by editing decoded blobs, each built for one case:
// a decoder that reads its destination before writing it, after members that yield fewer rows than their count;
// a member with two faults; counts that do not fit, are negative, or overflow a sum.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceCrafted(t *testing.T) {
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
	// The first group holds seven Chimp members, and all seven columns enter the kernel,
	// so the second member's values are decoded ahead into the range the ALP-RLE member reads.
	// The kernel leaves before its first step, at the end of the cut column.
	// The three members of the second group are too few for it: that group is asked and not decoded ahead.
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

		// The cut member stays scalar; the kernel takes the eight-value column and the five whole ones,
		// seven steps with six lanes and 22 more with five.
		got := requireMaterializeUses(t, blobs, target,
			chimpGroupUse{grouped: 1, groups: 2, ahead: 1, columns: 6, steps: 29, calls: 2})
		require.True(t, got.ok)
		require.Len(t, got.metric.Values, 1+8+4+8*30)
		require.Equal(t, []float64{101, 102, 103, 104, 105, 106, 107, 108}, got.metric.Values[1:9])
		for _, v := range got.metric.Values[9:13] {
			require.Zero(t, math.Float64bits(v), "the cut ALP-RLE member yields zeros")
		}
	})

	// The last member of the first group of eight leaves 35 decoded values past its rows;
	// the cut ALP-RLE member of the next group reads one of them and repeats it.
	// No group may be decoded ahead over those values: the three members behind the long one are appended one at a time,
	// until the rows have passed its range, and the last five form a group.
	// With a Chimp long member the first group has eight columns in the kernel, which leaves when seven are complete;
	// with a Gorilla one, which is decoded in its turn, it has seven.
	// Both groups take 29 steps in one call.
	for _, long := range []struct {
		encoding format.EncodingType
		want     chimpGroupUse
	}{
		{format.TypeChimp, chimpGroupUse{grouped: 1, groups: 2, ahead: 2, columns: 8 + 5, steps: 2 * 29, calls: 2}},
		{format.TypeGorilla, chimpGroupUse{grouped: 1, groups: 2, ahead: 2, columns: 7 + 5, steps: 2 * 29, calls: 2}},
	} {
		t.Run(fmt.Sprintf("an ALP-RLE column cut at its nested column across groups, behind a long %v member", long.encoding), func(t *testing.T) {
			longValues := dense(7, 40)
			members := complete(0, 7)
			members = append(members,
				craftedMember([]NumericEncoderOption{
					WithLittleEndian(), WithBlobLayoutV2(), WithTimestampEncoding(format.TypeDelta), WithValueEncoding(long.encoding),
				}, longValues),
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

			got := requireMaterializeUses(t, blobs, target, long.want)
			require.True(t, got.ok)
			off := 7*30 + 5 + 1
			require.Len(t, got.metric.Values, off+20+6*30)
			for _, v := range got.metric.Values[off : off+20] {
				require.Equal(t, math.Float64bits(longValues[25]), math.Float64bits(v), "the cut ALP-RLE member repeats a left-over value")
			}
		})
	}

	// The first member of a single group yields one row of its 30.
	// The seven members behind it were decoded ahead and are decoded again, each 29 rows lower,
	// so the last 29 elements of the capacity belong to no member: they must be zero, as the baseline leaves them.
	t.Run("a short first member with nothing behind its group", func(t *testing.T) {
		blobs := decodeSetTestMembers(t, complete(0, 8))
		blobs[0].index.entryByName(target).ValueLength = 8

		got := requireMaterializeUses(t, blobs, target,
			chimpGroupUse{grouped: 1, groups: 1, ahead: 1, columns: 7, steps: 29, calls: 1})
		require.Len(t, got.metric.Values, 1+7*30)
		require.Equal(t, 8*30, cap(got.metric.Values))
		for _, v := range got.metric.Values[1+7*30 : 8*30] {
			require.Zero(t, math.Float64bits(v), "the capacity past the rows")
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

// TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceGroupsLeftAlone compares the materialize entry points with
// referenceMaterializeMetric on sets with a group that cannot share the kernel, and pins what the grouped loop does
// with it: the group is asked, no lane is filled for it, and no group is asked after it.
// The sets put such a group first, behind groups that were decoded ahead, and behind one that was cleared,
// and they stand on either side of each rule a group enters the kernel by:
// four columns, 16 bytes after a column, and 12 bits a value.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceGroupsLeftAlone(t *testing.T) {
	const target = craftedTarget
	chimp := consumerShapeOptions(format.TypeChimp)
	alpRLE := consumerShapeOptions(format.TypeALPRLE)
	dense := func(i, n int) []float64 { return setValueColumn(setKindFullPrec, n, 1000+float64(i), int64(i)+1) }
	// members returns n Chimp members numbered from first, each holding what col returns for its number.
	members := func(first, n int, col func(i int) []float64) []setTestMember {
		out := make([]setTestMember, n)
		for i := range out {
			out[i] = craftedMember(chimp, col(first+i))
		}

		return out
	}
	whole := func(i int) []float64 { return dense(i, 30) }
	constant := func(i int) []float64 { return slices.Repeat([]float64{float64(i) + 0.25}, 30) }
	// leftAlone is a call whose first group is asked and not decoded ahead: no lane is filled, and no other group is asked.
	leftAlone := chimpGroupUse{grouped: 1, groups: 1}

	// One member in eight holds Chimp values, so every group of eight has one Chimp column.
	// The ALP-RLE columns are full-precision values, long enough to pass for dense Chimp columns by their lengths alone:
	// a count that took them for Chimp columns would decode the group ahead.
	t.Run("one Chimp member in eight", func(t *testing.T) {
		set := make([]setTestMember, 40)
		for i := range set {
			opts := alpRLE
			if i%8 == 0 {
				opts = chimp
			}
			set[i] = craftedMember(opts, whole(i))
		}
		blobs := decodeSetTestMembers(t, set)
		for i := range blobs {
			entry := blobs[i].index.entryByName(target)
			require.GreaterOrEqual(t, valueSlackAfter(&blobs[i], target), 16, "member %d: the target's column is followed by another", i)
			require.GreaterOrEqual(t, (entry.ValueLength-8)*8, 12*(entry.Count-1), "member %d: bits of the column", i)
		}
		got := requireMaterializeUses(t, blobs, target, leftAlone)
		require.Len(t, got.metric.Values, 40*30)
	})

	// The sixteen dense members behind the constant group would fill two groups of the kernel:
	// they are appended one at a time, which is what a sparse first group costs a set.
	t.Run("a sparse group, then dense members", func(t *testing.T) {
		blobs := decodeSetTestMembers(t, append(members(0, 8, constant), members(8, 16, whole)...))
		got := requireMaterializeUses(t, blobs, target, leftAlone)
		require.Len(t, got.metric.Values, 24*30)
	})

	// Two dense groups are decoded ahead, the constant group is asked and left alone,
	// and the dense group behind it is not asked.
	t.Run("dense groups, then a sparse group, then a dense group", func(t *testing.T) {
		set := append(members(0, 16, whole), members(16, 8, constant)...)
		blobs := decodeSetTestMembers(t, append(set, members(24, 8, whole)...))
		got := requireMaterializeUses(t, blobs, target,
			chimpGroupUse{grouped: 1, groups: 3, ahead: 2, columns: 16, steps: 2 * 29, calls: 2})
		require.Len(t, got.metric.Values, 32*30)
	})

	// The fourth member of the first group yields one value of its 30, so the values decoded ahead behind it are cleared
	// and the last four members of the group are decoded again; the cut column stays scalar,
	// and the kernel had decoded the seven others.
	// The rows end where the group's last member ends, so the next eight members are asked at once:
	// they are constant, and end the grouping.
	t.Run("a group cleared after a short member, then a sparse group", func(t *testing.T) {
		set := append(members(0, 8, whole), members(8, 8, constant)...)
		blobs := decodeSetTestMembers(t, append(set, members(16, 8, whole)...))
		blobs[3].index.entryByName(target).ValueLength = 8
		got := requireMaterializeUses(t, blobs, target,
			chimpGroupUse{grouped: 1, groups: 2, ahead: 1, columns: 7, steps: 29, calls: 1})
		require.Len(t, got.metric.Values, 23*30+1)
	})

	// The first member declares eight values and yields one, the second holds 101 to 108,
	// and the ALP-RLE member behind them reads its own range before it writes it: the baseline returns zeros for it.
	// The group holds two Chimp columns, so it is left alone, and it must be left alone before a lane has written:
	// the second member's values, decoded seven rows above where it ends up, would be what the ALP-RLE member reads.
	t.Run("a short member and a cut ALP-RLE column in a group that is left alone", func(t *testing.T) {
		runValue := math.Pi * 1e300 // plain ALP stores every copy in full, so the ALP-RLE encoder writes one run
		set := []setTestMember{
			craftedMember(chimp, dense(0, 8)),
			craftedMember(chimp, []float64{101, 102, 103, 104, 105, 106, 107, 108}),
			craftedMember(alpRLE, slices.Repeat([]float64{runValue}, 4)),
		}
		for i := 3; i < 8; i++ {
			set = append(set, craftedMember(alpRLE, whole(i)))
		}
		blobs := decodeSetTestMembers(t, append(set, members(8, 4, whole)...))
		blobs[0].index.entryByName(target).ValueLength = 8
		cutAtNestedColumn(t, &blobs[2], target)

		got := requireMaterializeUses(t, blobs, target, leftAlone)
		require.Len(t, got.metric.Values, 1+8+4+9*30)
		require.Equal(t, []float64{101, 102, 103, 104, 105, 106, 107, 108}, got.metric.Values[1:9])
		for _, v := range got.metric.Values[9:13] {
			require.Zero(t, math.Float64bits(v), "the cut ALP-RLE member yields zeros")
		}
	})

	// The hook counts groups, not lanes: a group filled into its lanes and then left undecoded would report the same.
	// So decodeChimpGroupAhead is asked directly, with marks in the group and in the lane numbers that must survive.
	t.Run("no lane is filled for a group that is left alone", func(t *testing.T) {
		for name, tc := range map[string]struct {
			col      func(i int) []float64
			capacity int
		}{
			"sparse columns":          {col: constant, capacity: 8 * 30},
			"one element of capacity": {col: whole, capacity: 8*30 - 1},
		} {
			blobs := decodeSetTestMembers(t, members(0, 8, tc.col))
			var (
				group   ienc.ChimpColumnGroup
				set     [8]*NumericBlob
				entries [8]section.NumericIndexEntry
				lanes   [8]int
			)
			for i := range blobs {
				set[i], entries[i] = &blobs[i], *blobs[i].index.entryByName(target)
				group.Count[i], group.Off[i], group.Len[i], lanes[i] = -7, -7, -7, 99
			}
			marked := group

			_, ahead := decodeChimpGroupAhead(&group, set[:], entries[:], &lanes, make([]float64, 0, tc.capacity))
			require.False(t, ahead, name)
			require.Equal(t, marked, group, "%s: the group", name)
			require.Equal(t, [8]int{99, 99, 99, 99, 99, 99, 99, 99}, lanes, "%s: the lane numbers", name)
		}
	})

	// A one-point column cannot enter the kernel: three whole columns among five of one point are too few, four are enough.
	t.Run("three and four columns", func(t *testing.T) {
		for wholeColumns, want := range map[int]chimpGroupUse{
			3: leftAlone,
			4: {grouped: 1, groups: 1, ahead: 1, columns: 4, steps: 29, calls: 1},
		} {
			blobs := decodeSetTestMembers(t, members(0, 8, func(i int) []float64 {
				if i%2 == 0 && i/2 < wholeColumns {
					return whole(i)
				}

				return dense(i, 1)
			}))
			requireMaterializeUses(t, blobs, target, want)
		}
	})

	// A value payload cut 15 bytes behind the target's column keeps that column out of the kernel, and 16 bytes do not.
	// The bytes cut off stay in the payload's capacity, which does not count.
	// With three columns that have 16 bytes the group is left alone; with four it is decoded ahead,
	// and the four columns that have 15 are decoded ahead too, by the scalar loop.
	t.Run("15 and 16 bytes after a column", func(t *testing.T) {
		for roomy, want := range map[int]chimpGroupUse{
			3: leftAlone,
			4: {grouped: 1, groups: 1, ahead: 1, columns: 4, steps: 29, calls: 1},
		} {
			blobs := decodeSetTestMembers(t, members(0, 8, whole))
			for i := range blobs {
				entry := blobs[i].index.entryByName(target)
				after := 15
				if i < roomy {
					after = 16
				}
				require.Greater(t, valueSlackAfter(&blobs[i], target), 16, "member %d: bytes after the target's column", i)
				blobs[i].valPayload = blobs[i].valPayload[:entry.ValueOffset+entry.ValueLength+after]
				require.Equal(t, after, valueSlackAfter(&blobs[i], target))
			}
			requireMaterializeUses(t, blobs, target, want)
		}
	})

	// The columns change every sixth value and take about 9 bits a value, too few.
	// Counts lowered on the index entries leave the bytes as they are and raise the bits a value:
	// the group enters with the most values that still average 12 bits, and stays out with one value more.
	// A member with a lowered count yields exactly that many rows, so nothing is cleared.
	t.Run("12 bits a value", func(t *testing.T) {
		for _, over := range []int{0, 1} {
			blobs := decodeSetTestMembers(t, members(0, 8, func(i int) []float64 {
				col := make([]float64, 36)
				for j := range col {
					col[j] = 100.25 + float64(i) + float64(j/6)*0.01
				}

				return col
			}))
			encodedBits := 0
			for i := range blobs {
				entry := blobs[i].index.entryByName(target)
				encodedBits += (entry.ValueLength - 8) * 8
			}
			values := encodedBits/12 + over // values after the first of each column, over the group
			require.Less(t, values, 8*35, "the columns are under 12 bits a value at their full counts")
			require.Greater(t, values, 8*16, "the lowered counts leave every column 16 values or more")
			for i := range blobs {
				count := 1 + values/8
				if i < values%8 {
					count++
				}
				blobs[i].index.entryByName(target).Count = count
			}

			want := leftAlone
			if over == 0 {
				want = denseSetUse(blobs, target)
				require.Equal(t, 1, want.ahead, "the rules decode the group ahead")
				require.Equal(t, 8, want.columns)
			}
			got := requireMaterializeUses(t, blobs, target, want)
			require.Len(t, got.metric.Values, values+8)
		}
	})
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupDamagedTimestamps compares the materialize entry points with
// referenceMaterializeMetric on groups of eight whose Chimp value columns are whole and whose timestamps are not:
// a member's own timestamp column cut short, a shared timestamp group short of its count,
// and timestamp bytes flipped before Decode, which accepts them.
// The whole columns enter the kernel, so the group is decoded ahead of the member its timestamps trim,
// and the values decoded ahead behind that member are cleared and decoded again.
// The sets end with their group, so a value decoded ahead and left behind would show in the capacity past the rows.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupDamagedTimestamps(t *testing.T) {
	const target = craftedTarget
	// A Raw column cut short yields no timestamp; a Delta or DeltaPacked one yields the five its prefix holds.
	encodings := []struct {
		ts    format.EncodingType
		short int
	}{{format.TypeRaw, 0}, {format.TypeDelta, 5}, {format.TypeDeltaPacked, 5}}
	// The first, a middle and the last member of the group, and two members.
	placements := [][]int{{0}, {3}, {7}, {2, 5}}
	whole := chimpGroupUse{grouped: 1, groups: 1, ahead: 1, columns: 8, steps: 29, calls: 1}
	chimpWith := func(ts format.EncodingType) func(int) []NumericEncoderOption {
		return func(int) []NumericEncoderOption { return ownTimestampOptions(ts, format.TypeChimp) }
	}

	t.Run("a member's own timestamp column", func(t *testing.T) {
		for _, enc := range encodings {
			for _, at := range placements {
				blobs := decodeSetTestMembers(t, craftedDenseMembers(8, chimpWith(enc.ts)))
				for _, i := range at {
					cutTimestampsTo(t, &blobs[i], target, enc.short)
				}
				got := requireMaterializeUses(t, blobs, target, whole)
				require.Len(t, got.metric.Values, 8*30-len(at)*(30-enc.short), "%v timestamps cut in members %v", enc.ts, at)
			}
		}
	})

	// In the consumer's shape a member's metrics share one timestamp group, decoded when the blob is opened.
	t.Run("a shared timestamp group", func(t *testing.T) {
		for _, at := range placements {
			blobs := decodeSetTestMembers(t, craftedDenseMembers(8, func(int) []NumericEncoderOption {
				return consumerShapeOptions(format.TypeChimp)
			}))
			for _, i := range at {
				shared := blobs[i].sharedTs
				require.NotNil(t, shared, "member %d", i)
				require.Len(t, shared.groups, 1, "member %d", i)
				require.Equal(t, shared.first.offset, blobs[i].index.entryByName(target).TimestampOffset)
				group := sharedTimestampGroup{offset: shared.first.offset, ts: shared.first.ts[:5]}
				blobs[i].sharedTs = &sharedTimestamps{first: group, groups: []sharedTimestampGroup{group}}
			}
			got := requireMaterializeUses(t, blobs, target, whole)
			require.Len(t, got.metric.Values, 8*30-len(at)*25, "shared groups short in members %v", at)
		}
	})

	// The flips leave the value payloads and the index entries as encoded,
	// so the first group's eight columns enter the kernel.
	// A flip can end a Delta or DeltaPacked column early, which trims its member: at least one round must.
	t.Run("timestamp bytes flipped", func(t *testing.T) {
		rng := rand.New(rand.NewSource(71))
		changed, trimmed := 0, 0
		for round := range 30 {
			members := craftedDenseMembers(16, chimpWith(encodings[round%len(encodings)].ts))
			encoded := make([][]byte, len(members))
			for i, m := range members {
				encoded[i] = encodeSetTestMember(t, i, m)
			}
			intact := craftedReferenceOutcome(t, decodeEncodedMembers(t, encoded))
			for _, i := range []int{1, 6, rng.Intn(len(encoded))} {
				flipped := slices.Clone(encoded[i])
				applyMaterializeFuzzFlip(t, flipped, materializeFuzzStep{op: 1, metric: byte(rng.Intn(8)), arg: uint16(rng.Intn(1 << 16))})
				if decodeAccepts(flipped) {
					encoded[i] = flipped
				}
			}
			blobs := decodeEncodedMembers(t, encoded)
			requireKernelColumns(t, blobs, target, 8, 16)
			got := craftedReferenceOutcome(t, blobs)
			if !sameMaterialization(intact, got) {
				changed++
			}
			if len(got.metric.Values) < 16*30 {
				trimmed++
			}
		}
		t.Logf("%d of 30 rounds changed the result, %d trimmed a member", changed, trimmed)
		require.Positive(t, changed, "rounds whose flips changed the result")
		require.Positive(t, trimmed, "rounds whose flips trimmed a member")
	})

	// One member yields five of its 30 timestamps and another one of its 30 values, in either order,
	// with a Gorilla member between them or not.
	// The value column cut to its first value stays out of the kernel, and so does the Gorilla column.
	t.Run("short timestamps and short values in one group", func(t *testing.T) {
		for _, c := range []struct {
			name                     string
			tsShort, valShort, other int // members; other holds Gorilla values, or is -1
		}{
			{"timestamps, then values", 2, 5, -1},
			{"values, then timestamps", 5, 2, -1},
			{"timestamps, Gorilla, then values", 2, 5, 3},
			{"values, Gorilla, then timestamps", 5, 2, 4},
		} {
			blobs := decodeSetTestMembers(t, craftedDenseMembers(8, func(i int) []NumericEncoderOption {
				if i == c.other {
					return ownTimestampOptions(format.TypeDelta, format.TypeGorilla)
				}

				return ownTimestampOptions(format.TypeDelta, format.TypeChimp)
			}))
			cutTimestampsTo(t, &blobs[c.tsShort], target, 5)
			blobs[c.valShort].index.entryByName(target).ValueLength = 8
			columns := 7
			if c.other >= 0 {
				columns = 6
			}
			got := requireMaterializeUses(t, blobs, target,
				chimpGroupUse{grouped: 1, groups: 1, ahead: 1, columns: columns, steps: 29, calls: 1})
			require.Len(t, got.metric.Values, 8*30-25-29, c.name)
		}
	})
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupDamagedTags compares the materialize entry points with
// referenceMaterializeMetric on groups of eight that mix members with tags and members without,
// each with a member whose value column is cut to its first value, before or after the tag damage:
// a tagged member whose tag range lies outside the tag payload, which yields no row,
// one whose tag column is cut short, which yields fewer rows than its count, or no tag damage.
// The members without tags are padded with as many empty tags as they yield values.
// The cut value column stays out of the kernel and the other seven enter it.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupDamagedTags(t *testing.T) {
	const target = craftedTarget
	untagged := []int{3, 6}
	build := func() []NumericBlob {
		members := craftedDenseMembers(8, func(int) []NumericEncoderOption {
			return ownTimestampOptions(format.TypeDeltaPacked, format.TypeChimp)
		})
		for i := range members {
			members[i].tags = !slices.Contains(untagged, i)
		}

		return decodeSetTestMembers(t, members)
	}
	// Each damage returns the rows its member yields.
	damages := []struct {
		name   string
		damage func(blob *NumericBlob) int
	}{
		{"no tag damage", func(*NumericBlob) int { return 30 }},
		{"a tag range outside the tag payload", func(blob *NumericBlob) int {
			blob.index.entryByName(target).TagOffset = len(blob.tagPayload) + 3
			return 0
		}},
		{"a tag column cut short", func(blob *NumericBlob) int {
			entry := blob.index.entryByName(target)
			entry.TagLength /= 2
			rows := blob.decodeTagsInto(*entry, make([]string, entry.Count))
			require.Positive(t, rows)
			require.Less(t, rows, entry.Count)

			return rows
		}},
	}
	for _, d := range damages {
		// The value column is cut after the tags, before them in an untagged member, and before them in a tagged one.
		for _, c := range []struct{ tags, values int }{{1, 6}, {5, 3}, {2, 0}} {
			blobs := build()
			rows := d.damage(&blobs[c.tags])
			blobs[c.values].index.entryByName(target).ValueLength = 8
			got := requireMaterializeUses(t, blobs, target,
				chimpGroupUse{grouped: 1, groups: 1, ahead: 1, columns: 7, steps: 29, calls: 1})
			require.Len(t, got.metric.Values, 8*30-(30-rows)-29, "%s in member %d, values cut in member %d", d.name, c.tags, c.values)
		}
	}

	// The first group's constant columns are too sparse for the kernel, so it is left alone
	// and the members behind it are appended one at a time, by appendMembersInTurn.
	// The tagged member there yields one value of 30 and leaves the tags it decoded past its row in the capacity;
	// the member without tags behind it, also cut to one value, pads one row, so those tags stay.
	t.Run("short members with and without tags behind a group left alone", func(t *testing.T) {
		members := craftedDenseMembers(10, func(int) []NumericEncoderOption {
			return ownTimestampOptions(format.TypeDelta, format.TypeChimp)
		})
		for i := range 8 {
			members[i].cols[0] = slices.Repeat([]float64{float64(i) + 0.25}, 30)
		}
		for i := range 9 {
			members[i].tags = true
		}
		blobs := decodeSetTestMembers(t, members)
		for _, i := range []int{8, 9} {
			blobs[i].index.entryByName(target).ValueLength = 8
		}
		got := requireMaterializeUses(t, blobs, target, chimpGroupUse{grouped: 1, groups: 1})
		require.Len(t, got.metric.Values, 8*30+2)
		require.NotEmpty(t, got.metric.Tags[:cap(got.metric.Tags)][8*30+2], "a tag of the short member with tags past the rows")
	})
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupPanics compares the materialize entry points with
// referenceMaterializeMetric on groups of eight with faulty members, which must panic as the reference does,
// with the same value: a timestamp range outside its payload, a value range outside its payload,
// and an ALP column cut in memory, which its decoder reads past;
// one member with one fault or two, and two faulty members, the later a Chimp member whose whole column gets a lane.
// Each fault has bounds of its own, so the panic value tells which fault was met first.
// The fourth member holds ALP values and the others Chimp values.
// Without a fault the group is decoded ahead with the kernel's seven Chimp columns, and so it is with the faults,
// up to the member that panics: the hook reports nothing then, so the set without a fault pins the kernel.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupPanics(t *testing.T) {
	const target = craftedTarget
	type fault struct {
		member int
		kind   string
	}
	faults := map[string]func(blob *NumericBlob){
		"timestamps": func(blob *NumericBlob) { blob.index.entryByName(target).TimestampOffset = cap(blob.tsPayload) + 1 },
		"values":     func(blob *NumericBlob) { blob.index.entryByName(target).ValueOffset = cap(blob.valPayload) + 3 },
		"ALP cut":    func(blob *NumericBlob) { blob.index.entryByName(target).ValueLength /= 2 },
		"short":      func(blob *NumericBlob) { blob.index.entryByName(target).ValueLength = 8 }, // one value, no panic
	}
	build := func(with ...fault) []NumericBlob {
		blobs := decodeSetTestMembers(t, craftedDenseMembers(8, func(i int) []NumericEncoderOption {
			if i == 3 {
				return ownTimestampOptions(format.TypeDelta, format.TypeALP)
			}

			return ownTimestampOptions(format.TypeDelta, format.TypeChimp)
		}))
		for _, f := range with {
			faults[f.kind](&blobs[f.member])
		}

		return blobs
	}
	panicOf := func(with ...fault) string {
		t.Helper()
		out := requireMaterializeMatchesReference(t, build(with...), target)
		require.True(t, out.panicked, "%v", with)

		return out.panicVal
	}

	requireMaterializeUses(t, build(), target, chimpGroupUse{grouped: 1, groups: 1, ahead: 1, columns: 7, steps: 29, calls: 1})

	single := map[fault]string{}
	for _, f := range []fault{{3, "timestamps"}, {3, "values"}, {3, "ALP cut"}, {5, "timestamps"}, {5, "values"}} {
		single[f] = panicOf(f)
	}
	for a, va := range single {
		for b, vb := range single {
			if a != b {
				require.NotEqual(t, va, vb, "%v and %v panic alike", a, b)
			}
		}
	}

	// Two faults of the ALP member, each pair with the fault that is met first.
	// The value range is sliced before the column is decoded, so with an ALP cut it panics at the cut length.
	ts, values, cut := fault{3, "timestamps"}, fault{3, "values"}, fault{3, "ALP cut"}
	require.Equal(t, single[ts], panicOf(ts, values), "timestamps and values")
	require.Equal(t, single[ts], panicOf(ts, cut), "timestamps and an ALP cut")
	valueRangePanic := func(with ...fault) string {
		blob := build(with...)[3]
		entry := blob.index.entryByName(target)

		return fmt.Sprintf("runtime.boundsError: runtime error: slice bounds out of range [:%d] with capacity %d",
			entry.ValueOffset+entry.ValueLength, cap(blob.valPayload))
	}
	require.Equal(t, valueRangePanic(values), single[values])
	require.Equal(t, valueRangePanic(values, cut), panicOf(values, cut), "values and an ALP cut")
	require.NotEqual(t, single[values], panicOf(values, cut), "the cut length is in the value range's bounds")

	// Two faulty members: the later Chimp member's column is whole, so it has a lane and is decoded ahead,
	// and its own fault, met later, must not be the panic.
	later := fault{5, "timestamps"}
	require.Equal(t, single[cut], panicOf(cut, later), "an ALP cut before a Chimp member with a lane")
	require.Equal(t, single[values], panicOf(values, later), "values before a Chimp member with a lane")
	// A member that comes up short clears the values decoded ahead; the later fault panics on the rows as they are then.
	require.Equal(t, single[later], panicOf(fault{1, "short"}, later), "a short member before a Chimp member with a lane")
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupDamageMatrix damages one member of a group of eight before Decode,
// a member of every value encoding on each side that applies to it: its value, timestamp and tag payloads,
// the timestamp side once per timestamp encoding.
// The damaged member is the fourth; the seven others hold whole Chimp columns, which enter the kernel.
// Bits are flipped until Decode accepts the member and the reference's result for the metric changes.
// A cell where no flip changed it fails the test: a damage that changes nothing tests nothing.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupDamageMatrix(t *testing.T) {
	const target = craftedTarget
	sides := []string{"value", "timestamp", "tag"} // the ops of applyMaterializeFuzzFlip
	type cell struct {
		val, ts format.EncodingType
		side    byte
	}
	var cells []cell
	for _, val := range []format.EncodingType{format.TypeChimp, format.TypeALP, format.TypeALPRLE, format.TypeGorilla, format.TypeRaw} {
		cells = append(cells, cell{val, format.TypeDelta, 0}, cell{val, format.TypeDelta, 2})
		for _, ts := range []format.EncodingType{format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked} {
			cells = append(cells, cell{val, ts, 1})
		}
	}

	rng := rand.New(rand.NewSource(83))
	var unchanged []string
	for _, c := range cells {
		members := craftedDenseMembers(8, func(i int) []NumericEncoderOption {
			if i == 3 {
				return ownTimestampOptions(c.ts, c.val)
			}

			return ownTimestampOptions(format.TypeDelta, format.TypeChimp)
		})
		members[3].tags = true
		encoded := make([][]byte, len(members))
		for i, m := range members {
			encoded[i] = encodeSetTestMember(t, i, m)
		}
		intact := craftedReferenceOutcome(t, decodeEncodedMembers(t, encoded))
		member := encoded[3]
		tries, accepted := 0, 0
		changed := false
		for ; tries < 200 && !changed; tries++ {
			encoded[3] = slices.Clone(member)
			applyMaterializeFuzzFlip(t, encoded[3], materializeFuzzStep{op: c.side, metric: byte(rng.Intn(8)), arg: uint16(rng.Intn(1 << 16))})
			if !decodeAccepts(encoded[3]) {
				continue
			}
			accepted++
			blobs := decodeEncodedMembers(t, encoded)
			if sameMaterialization(intact, craftedReferenceOutcome(t, blobs)) {
				continue
			}
			changed = true
			requireKernelColumns(t, blobs, target, 7, 8)
		}
		what := fmt.Sprintf("%v values, %v timestamps, %s side", c.val, c.ts, sides[c.side])
		t.Logf("%s: changed=%v after %d flips, %d accepted", what, changed, tries, accepted)
		if !changed {
			unchanged = append(unchanged, what)
		}
	}
	require.Empty(t, unchanged, "cells whose damage changed nothing")
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupOnByDefault pins that the public entry points use the group decode
// as the package initialises it, without a test setting the switch: on a machine that has the kernel,
// a group of eight dense Chimp members is decoded ahead with its eight columns in the kernel.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupOnByDefault(t *testing.T) {
	require.Equal(t, ienc.ChimpGroupDecodeEnabled(), chimpGroupDecode, "the switch as the package initialises it")
	blobs := decodeSetTestMembers(t, craftedDenseMembers(8, func(int) []NumericEncoderOption {
		return consumerShapeOptions(format.TypeChimp)
	}))
	var columns int
	chimpGroupObserve = func(_, _, c, _, _ int) { columns += c }
	defer func() { chimpGroupObserve = nil }()
	metric, ok := NewBlobSet(blobs, nil).MaterializeNumericMetricByName(craftedTarget)
	require.True(t, ok)
	require.Len(t, metric.Values, 8*30)
	if chimpGroupKernelRuns(t) {
		require.Equal(t, 8, columns, "columns in the kernel")
	}
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceRandomSets compares the materialize entry points with
// referenceMaterializeMetric on 400 random sets of 1 to 30 members.
// Every other set is all Chimp in the consumer's shape; the others mix every variant of mixedSetVariants.
// Members hold 1 to 45 points of every kind of value, and all but the first metric are missing from some members.
// Every third set is damaged like a program of FuzzNumericBlobSetMaterializeMetric, in members of any encoding:
// a bit of a value, timestamp or tag payload flipped before Decode, or an index entry edited after it.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupMatchesReferenceRandomSets(t *testing.T) {
	rng := rand.New(rand.NewSource(67))
	names := materializeFuzzNames
	variants := mixedSetVariants()
	var total chimpGroupUse
	for round := range 400 {
		encoded := make([][]byte, 1+rng.Intn(30))
		for i := range encoded {
			m := variants[rng.Intn(len(variants))]
			if rng.Intn(3) > 0 {
				m = variants[rng.Intn(mixedSetChimpVariants)]
			}
			if round%2 == 0 {
				m = variants[0]
			}
			n := 30 + rng.Intn(11)
			switch rng.Intn(10) {
			case 0:
				n = 1 + rng.Intn(3)
			case 1:
				n = 1 + rng.Intn(materializeFuzzMaxCount)
			default:
			}
			kind := byte(rng.Intn(materializeFuzzKinds))
			for k, name := range names {
				if k == 0 || rng.Intn(8) > 0 { // every member holds the first metric
					m.names = append(m.names, name)
					m.cols = append(m.cols, materializeFuzzColumn(kind, round*30+i, k, n))
				}
			}
			encoded[i] = encodeSetTestMember(t, i, m)
		}

		type edit struct {
			member int
			step   materializeFuzzStep
		}
		var edits []edit
		if round%3 == 0 {
			for range 1 + rng.Intn(3) {
				i := rng.Intn(len(encoded))
				step := materializeFuzzStep{op: byte(rng.Intn(materializeFuzzOps)), metric: byte(rng.Intn(256)), arg: uint16(rng.Intn(1 << 16))}
				if step.op >= 3 {
					edits = append(edits, edit{i, step})

					continue
				}
				// A flip Decode rejects never reaches a set: the member keeps its bytes.
				flipped := slices.Clone(encoded[i])
				applyMaterializeFuzzFlip(t, flipped, step)
				if dec, err := NewNumericDecoder(flipped); err == nil {
					if _, err = dec.Decode(); err == nil {
						encoded[i] = flipped
					}
				}
			}
		}
		blobs := make([]NumericBlob, len(encoded))
		for i, data := range encoded {
			blobs[i] = decodeSetTestMember(t, data)
		}
		for _, e := range edits {
			applyMaterializeFuzzEdit(&blobs[e.member], e.step)
		}

		for _, name := range names {
			_, uses := materializeAgainstReference(t, blobs, name)
			total.grouped += uses[0].grouped
			total.groups += uses[0].groups
			total.ahead += uses[0].ahead
			total.columns += uses[0].columns
			total.steps += uses[0].steps
			total.calls += uses[0].calls
		}
		requireMaterializeUses(t, blobs, "absent", chimpGroupUse{})
	}

	t.Logf("the grouped loop ran %d times and decoded %d of %d groups ahead; the kernel took %d columns and completed %d steps in %d calls",
		total.grouped, total.ahead, total.groups, total.columns, total.steps, total.calls)
	require.Positive(t, total.grouped, "runs of the grouped loop")
	require.Positive(t, total.ahead, "groups decoded ahead")
	require.Less(t, total.ahead, total.groups, "groups decoded ahead, of the groups asked")
	if chimpGroupKernelRuns(t) {
		require.Positive(t, total.columns, "columns in the kernel")
		require.Positive(t, total.steps, "kernel steps")
	}
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupGuardPage ends every member's value payload at an inaccessible page,
// so a read past a payload faults.
// The last two metrics hold one point, which leaves 16 bytes of the payload after the third metric's column
// and 8 after the fourth's; with the payload's last byte dropped they are 15 and 7.
// Only a column with 16 bytes or more after it may enter the kernel.
// Every column claims eight more points than it holds, so the kernel walks to the column's last bit
// and reads the bytes after it.
// A member that claims more than it holds comes up short, and the rows never catch up with its range again:
// only the first group of eight members is asked. Where its columns may enter the kernel it is decoded ahead,
// in one kernel call, which completes at least as many steps as its shortest column holds values after the first.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupGuardPage(t *testing.T) {
	names := payloadOrderedNames(t, 5)
	kernel := chimpGroupKernelRuns(t)
	for round := range 12 {
		for _, drop := range []int{0, 1} {
			counts := make([]int, 12)
			for i := range counts {
				counts[i] = 30 + (i+round)%9
			}
			members := denseSetMembers(names, counts)
			blobs := make([]NumericBlob, len(members))
			for i, m := range members {
				m.cols[3], m.cols[4] = m.cols[3][:1], m.cols[4][:1]
				blobs[i] = decodeSetTestMember(t, encodeSetTestMember(t, i, m))
				// Decode rejects a blob cut short, so the payload is moved and shortened after decoding,
				// and the last column's entry follows.
				guarded, ok := fuzzGuardedCopy(t, blobs[i].valPayload[:len(blobs[i].valPayload)-drop])
				if !ok {
					t.Skip("guard pages are unavailable")
				}
				blobs[i].valPayload = guarded
				blobs[i].index.entryByName(names[4]).ValueLength -= drop
				require.Equal(t, 16-drop, valueSlackAfter(&blobs[i], names[2]))
				require.Equal(t, 8-drop, valueSlackAfter(&blobs[i], names[3]))
				require.Zero(t, valueSlackAfter(&blobs[i], names[4]))
				for k := range blobs[i].index.sorted {
					blobs[i].index.sorted[k].Count += 8
				}
			}

			minSteps := slices.Min(counts[:8]) - 1
			for k, name := range names {
				out, uses := materializeAgainstReference(t, blobs, name)
				require.False(t, out.panicked, "%s with %d bytes dropped: %s", name, drop, out.panicVal)
				for _, use := range uses {
					enters := k < 2 || k == 2 && drop == 0
					require.Equal(t, 1, use.grouped, name)
					require.Equal(t, 1, use.groups, "%s with %d bytes dropped: groups asked", name, drop)
					require.Equal(t, enters, use.ahead == 1, "%s with %d bytes dropped: groups decoded ahead: %d", name, drop, use.ahead)
					switch {
					case !kernel:
					case enters:
						require.Equal(t, 8, use.columns, "%s with %d bytes dropped: columns in the kernel", name, drop)
						require.Equal(t, 1, use.calls, "%s with %d bytes dropped: kernel calls", name, drop)
						require.GreaterOrEqual(t, use.steps, minSteps, "%s with %d bytes dropped: kernel steps", name, drop)
					default:
						require.Zero(t, use.columns, "%s with %d bytes dropped entered the kernel", name, drop)
					}
				}
			}
		}
	}
}

// TestNumericBlobSet_MaterializeMetric_ConcurrentMatchesReference materializes the metrics of one set from eight
// goroutines at once through the grouped loop, and compares every result with the reference.
// Each call keeps its group on its own stack, so the calls share nothing but the set, which they only read.
// One member is cut short, so the calls also clear and decode again.
func TestNumericBlobSet_MaterializeMetric_ConcurrentMatchesReference(t *testing.T) {
	names := payloadOrderedNames(t, 5)
	counts := make([]int, 40)
	for i := range counts {
		counts[i] = 30 + (i*7)%11
	}
	blobs := decodeSetTestMembers(t, denseSetMembers(names, counts))
	blobs[9].index.entryByName(names[0]).ValueLength = 8
	numericSet, err := NewNumericBlobSet(blobs)
	require.NoError(t, err)
	blobSet := NewBlobSet(blobs, nil)
	want := make([]materializeOutcome, len(names))
	for k, name := range names {
		want[k] = catchMaterialize(func() (MaterializedNumericMetric, bool) {
			return referenceMaterializeMetricByName(&numericSet, name)
		})
		require.True(t, want[k].ok, name)
	}

	defer func(prev bool) { chimpGroupDecode = prev }(chimpGroupDecode)
	chimpGroupDecode = true
	const goroutines, rounds = 8, 25
	got := make([][]materializeOutcome, goroutines)
	var wg sync.WaitGroup
	for g := range got {
		got[g] = make([]materializeOutcome, rounds)
		wg.Go(func() {
			for r := range got[g] {
				name := names[(g+r)%len(names)]
				got[g][r] = catchMaterialize(func() (MaterializedNumericMetric, bool) {
					return blobSet.MaterializeNumericMetricByName(name)
				})
			}
		})
	}
	wg.Wait()

	for g := range got {
		for r, out := range got[g] {
			k := (g + r) % len(names)
			requireSameMaterialization(t, fmt.Sprintf("goroutine %d, call %d, %s", g, r, names[k]), want[k], out)
		}
	}
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupFuzzSeeds pins that the seed corpus of
// FuzzNumericBlobSetMaterializeMetric reaches the group decode:
// the kernel takes columns in at least one seed, and the two crafted seeds decode a group ahead that holds
// a member which comes up short.
// In the first, the short member opens the only group with four Chimp columns, and the kernel takes that group's seven:
// they were decoded ahead of a short member, so the values behind it were cleared and decoded again.
// The three members behind that group are asked as a group and not decoded ahead.
// In the second, the kernel takes the eight columns of the first group and the five of the last and no others,
// and only those two groups are asked, so the three members behind the short one were appended one at a time.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupFuzzSeeds(t *testing.T) {
	seeds := materializeFuzzSeeds(t)
	withColumns := 0
	for _, seed := range seeds {
		blobs := buildMaterializeFuzzSet(t, seed)
		columns := 0
		for _, name := range materializeFuzzNames {
			_, uses := materializeAgainstReference(t, blobs, name)
			columns += uses[0].columns
		}
		if columns > 0 {
			withColumns++
		}
	}
	if chimpGroupKernelRuns(t) {
		require.Positive(t, withColumns, "seeds in which the kernel takes columns")
	}

	name := materializeFuzzNames[materializeFuzzSeedMetric]
	short := requireMaterializeUses(t, buildMaterializeFuzzSet(t, seeds[len(seeds)-2]), name,
		chimpGroupUse{grouped: 1, groups: 2, ahead: 1, columns: 6, steps: 29, calls: 2})
	require.Len(t, short.metric.Values, 1+8+4+8*30)
	require.Equal(t, 8+8+4+8*30, cap(short.metric.Values), "the first member yields one row of its eight")

	across := requireMaterializeUses(t, buildMaterializeFuzzSet(t, seeds[len(seeds)-1]), name,
		chimpGroupUse{grouped: 1, groups: 2, ahead: 2, columns: 8 + 5, steps: 2 * 29, calls: 2})
	require.Len(t, across.metric.Values, 7*30+5+1+20+6*30)
	require.Equal(t, 7*30+40+8+20+6*30, cap(across.metric.Values), "the eighth member yields five rows of its 40")
}

// TestNumericBlobSet_MaterializeMetric_ChimpGroupAllocations pins that materializing a metric of a set in the
// consumer's shape allocates what the reference allocates, with the group decode off and with it on:
// the group and its scratch rows stay on the stack.
func TestNumericBlobSet_MaterializeMetric_ChimpGroupAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("race detector adds allocations")
	}
	set := buildSetFixture(t, consumerSetFixture())
	numericSet, err := NewNumericBlobSet(set.NumericBlobs())
	require.NoError(t, err)

	defer func(prev bool) { chimpGroupDecode = prev }(chimpGroupDecode)
	// A gauge, a counter, a near-constant column and a full-precision gauge of the mix.
	for _, m := range []int{0, 40, 60, 99} {
		name := setFixtureName(m)
		want := testing.AllocsPerRun(5, func() {
			referenceMaterializeMetricByName(&numericSet, name)
		})
		for _, on := range []bool{false, true} {
			chimpGroupDecode = on
			got := testing.AllocsPerRun(5, func() {
				set.MaterializeNumericMetricByName(name)
			})
			require.Equal(t, want, got, "%s with the group decode on=%v: allocations per call", name, on)
		}
	}
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
			out = materializeOutcome{panicked: true, panicVal: fmt.Sprintf("%T: %v", r, r), panicRaw: r}
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
		// Two values can print alike: the values themselves are compared too.
		require.Equal(t, want.panicRaw, got.panicRaw, "%s: panic value behind the message", what)

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
// Every entry point is called twice, with chimpGroupDecode off and with it on,
// so both loops of materializeMetricCore are compared with the reference on every machine:
// whether a group is decoded ahead does not depend on the CPU,
// so without the kernel the grouped loop still decodes the same groups ahead, one column at a time.
// The ID entry points get the ID the by-name ones resolve, or the name's hash when no blob has the name:
// MaterializeMetricByName returns before materializeMetricCore for an absent name, and MaterializeMetric does not.
// It returns the reference's outcome by name, so a fixture can pin the result it was built for.
func requireMaterializeMatchesReference(t *testing.T, blobs []NumericBlob, name string) materializeOutcome {
	t.Helper()
	out, _ := materializeAgainstReference(t, blobs, name)

	return out
}

// requireMaterializeUses is requireMaterializeMatchesReference that also pins what the group decode did
// for each of the four entry points: whether the grouped loop ran, the groups it asked to decode ahead
// and the groups it did decode ahead, which no machine's CPU changes, and,
// on a machine that has the kernel, the columns the kernel took, the steps it completed and its calls.
func requireMaterializeUses(t *testing.T, blobs []NumericBlob, name string, want chimpGroupUse) materializeOutcome {
	t.Helper()
	out, uses := materializeAgainstReference(t, blobs, name)
	kernel := chimpGroupKernelRuns(t)
	for i, use := range uses {
		require.Equal(t, want.grouped, use.grouped, "%s: runs of the grouped loop for entry point %d", name, i)
		require.Equal(t, want.groups, use.groups, "%s: groups asked by entry point %d", name, i)
		require.Equal(t, want.ahead, use.ahead, "%s: groups decoded ahead by entry point %d", name, i)
		if kernel {
			require.Equal(t, want, use, "%s: use of the group decode by entry point %d", name, i)
		}
	}

	return out
}

// requireKernelColumns is requireMaterializeMatchesReference for a metric that at least four Chimp members hold,
// on a set where what the kernel does depends on the data.
// It requires that the grouped loop ran and, on a machine that has the kernel,
// that the kernel took between minColumns and maxColumns columns, for each of the four entry points.
func requireKernelColumns(t *testing.T, blobs []NumericBlob, name string, minColumns, maxColumns int) {
	t.Helper()
	_, uses := materializeAgainstReference(t, blobs, name)
	kernel := chimpGroupKernelRuns(t)
	for i, use := range uses {
		require.Equal(t, 1, use.grouped, "%s: runs of the grouped loop for entry point %d", name, i)
		if kernel {
			require.GreaterOrEqual(t, use.columns, minColumns, "%s: columns in the kernel for entry point %d", name, i)
			require.LessOrEqual(t, use.columns, maxColumns, "%s: columns in the kernel for entry point %d", name, i)
		}
	}
}

// denseSetUse returns what the group decode does for the named metric of a set of well-formed Chimp members
// whose columns average 12 bits a value or more and hold only values the kernel takes,
// which is what the full-precision columns of denseSetMembers are.
// It applies the rules, and decodes nothing.
// The members that hold the metric are taken eight at a time, and the grouped loop runs when there are at least four.
// A column can enter the kernel when it has two values or more and 16 bytes of its payload after it,
// and a group's columns enter when at least four of them can: the group is then decoded ahead.
// The first group that is not decoded ahead is the last one asked.
// The kernel runs in rounds, each as long as the fewest values a column still wants, until fewer than four columns
// are unfinished: so it completes as many steps as the group's fourth-largest count less the first value,
// in one call per distinct count up to that one.
func denseSetUse(blobs []NumericBlob, name string) chimpGroupUse {
	var counts []int // per member that holds the metric: its count, or 0 for a column that cannot enter the kernel
	for i := range blobs {
		entry := blobs[i].index.entryByName(name)
		if entry == nil {
			continue
		}
		count := entry.Count
		if count < 2 || valueSlackAfter(&blobs[i], name) < 16 {
			count = 0
		}
		counts = append(counts, count)
	}
	if len(counts) < 4 {
		return chimpGroupUse{}
	}

	use := chimpGroupUse{grouped: 1}
	for group := range slices.Chunk(counts, 8) {
		use.groups++
		lanes := slices.DeleteFunc(slices.Sorted(slices.Values(group)), func(count int) bool { return count == 0 })
		if len(lanes) < 4 {
			break
		}
		use.ahead++
		use.columns += len(lanes)
		use.steps += lanes[len(lanes)-4] - 1
		use.calls += len(slices.Compact(lanes[:len(lanes)-3]))
	}

	return use
}

// requireDenseSetUses runs requireMaterializeUses for every name of a set of dense Chimp members,
// and for a name no blob has.
// The columns of names[:roomy] have more than 16 bytes of their payload after them in every member,
// and want is what the group decode does for each of them, derived by hand for the fixture.
// The last name's column ends every payload, so it never enters the kernel:
// where the grouped loop runs, its first group is not decoded ahead and is the only one asked.
// For the names between, the room depends on the encoded size of the columns behind, and denseSetUse applies the rules.
func requireDenseSetUses(t *testing.T, blobs []NumericBlob, names []string, roomy int, want chimpGroupUse) {
	t.Helper()
	last := len(names) - 1
	for k, name := range names {
		switch {
		case k < roomy:
			require.Equal(t, want, denseSetUse(blobs, name), "%s: the rules give what the fixture was derived for", name)
			requireMaterializeUses(t, blobs, name, want)
		case k < last:
			requireMaterializeUses(t, blobs, name, denseSetUse(blobs, name))
		default:
			require.Zero(t, denseSetUse(blobs, name).columns, "%s ends its payload", name)
			requireMaterializeUses(t, blobs, name, chimpGroupUse{grouped: want.grouped, groups: want.grouped})
		}
	}
	requireMaterializeUses(t, blobs, "absent", chimpGroupUse{})
}

// chimpGroupKernelRuns reports whether the group decode's kernel runs on this machine.
// Where it does not, a test still compares every result with the reference, and skips what it pins about the kernel.
// MEBO_REQUIRE_AVX512=1 turns that skip into a failure:
// set it on a runner meant to execute the kernel, so its pins cannot pass by being skipped.
func chimpGroupKernelRuns(tb testing.TB) bool {
	tb.Helper()
	if ienc.ChimpGroupDecodeEnabled() {
		return true
	}
	if os.Getenv("MEBO_REQUIRE_AVX512") == "1" {
		tb.Fatal("the Chimp group decode kernel cannot run on this machine, and MEBO_REQUIRE_AVX512=1")
	}

	return false
}

// materializeAgainstReference does the comparison of requireMaterializeMatchesReference.
// It returns the reference's outcome by name and, for each of the four entry points in the order given there,
// what the call with chimpGroupDecode on reported to chimpGroupObserve.
// With the switch off the hook must stay silent: the grouped loop is not taken.
func materializeAgainstReference(t *testing.T, blobs []NumericBlob, name string) (materializeOutcome, [4]chimpGroupUse) {
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
	calls := [4]struct {
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

	var uses [4]chimpGroupUse
	defer func(prev bool) { chimpGroupDecode, chimpGroupObserve = prev, nil }(chimpGroupDecode)
	for _, on := range []bool{false, true} {
		chimpGroupDecode = on
		for i, c := range calls {
			var use chimpGroupUse
			chimpGroupObserve = func(groups, ahead, columns, steps, kernelCalls int) {
				use.grouped++
				use.groups += groups
				use.ahead += ahead
				use.columns += columns
				use.steps += steps
				use.calls += kernelCalls
			}
			got := catchMaterialize(c.call)
			chimpGroupObserve = nil
			what := fmt.Sprintf("%s through %s with the group decode on=%v", name, c.what, on)
			requireSameMaterialization(t, what, c.want, got)
			if on {
				uses[i] = use
			} else {
				require.Zero(t, use, "%s: the grouped loop ran", what)
			}
		}
	}

	return byName, uses
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

// craftedDenseMembers returns n crafted members of 30 full-precision points,
// member i with the options opts returns for it.
func craftedDenseMembers(n int, opts func(i int) []NumericEncoderOption) []setTestMember {
	members := make([]setTestMember, n)
	for i := range members {
		members[i] = craftedMember(opts(i), setValueColumn(setKindFullPrec, 30, 1000+float64(i), int64(i)+1))
	}

	return members
}

// ownTimestampOptions are the options of a little-endian V2 member whose metrics have timestamp columns of their own.
func ownTimestampOptions(ts, val format.EncodingType) []NumericEncoderOption {
	return []NumericEncoderOption{WithLittleEndian(), WithBlobLayoutV2(), WithTimestampEncoding(ts), WithValueEncoding(val)}
}

// decodeAccepts reports whether Decode accepts an encoded member.
func decodeAccepts(data []byte) bool {
	dec, err := NewNumericDecoder(data)
	if err != nil {
		return false
	}
	_, err = dec.Decode()

	return err == nil
}

// decodeEncodedMembers decodes every encoded member and fails if Decode rejects one.
func decodeEncodedMembers(tb testing.TB, encoded [][]byte) []NumericBlob {
	tb.Helper()
	blobs := make([]NumericBlob, len(encoded))
	for i, data := range encoded {
		blobs[i] = decodeSetTestMember(tb, data)
	}

	return blobs
}

// craftedReferenceOutcome materializes craftedTarget of a set of the given blobs with referenceMaterializeMetricByName.
func craftedReferenceOutcome(tb testing.TB, blobs []NumericBlob) materializeOutcome {
	tb.Helper()
	set, err := NewNumericBlobSet(blobs)
	require.NoError(tb, err)

	return catchMaterialize(func() (MaterializedNumericMetric, bool) {
		return referenceMaterializeMetricByName(&set, craftedTarget)
	})
}

// sameMaterialization reports whether two outcomes are equal in their rows:
// the panic, ok, timestamps, tags and value bits.
func sameMaterialization(a, b materializeOutcome) bool {
	return a.panicked == b.panicked && a.panicVal == b.panicVal && a.ok == b.ok &&
		slices.Equal(a.metric.Timestamps, b.metric.Timestamps) && slices.Equal(a.metric.Tags, b.metric.Tags) &&
		slices.EqualFunc(a.metric.Values, b.metric.Values, func(x, y float64) bool { return math.Float64bits(x) == math.Float64bits(y) })
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
		applyMaterializeFuzzFlip(tb, encoded[int(s.member)%len(encoded)], s)
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

// applyMaterializeFuzzFlip applies one flip step of a fuzz program to an encoded member.
// A step that is not a flip leaves the bytes as they are.
func applyMaterializeFuzzFlip(tb testing.TB, data []byte, s materializeFuzzStep) {
	tb.Helper()
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
		return
	}
	if start < end && end <= len(data) {
		data[start+int(s.arg)%(end-start)] ^= 1 << (s.metric % 8)
	}
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
