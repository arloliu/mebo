package blob

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// ==============================================================================
// Helper Functions
func createTestBlob(t *testing.T, tsEncoding, valEncoding format.EncodingType) NumericBlob { //nolint: unparam
	blobTs := time.Now()
	encoder, err := NewNumericEncoder(blobTs,
		WithTimestampEncoding(tsEncoding),
		WithValueEncoding(valEncoding))
	require.NoError(t, err)

	// Metric 1: Multiple data points
	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.UnixMicro(), 1.5, "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(time.Second).UnixMicro(), 2.5, "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(2*time.Second).UnixMicro(), 3.5, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 2: Single data point
	err = encoder.StartMetricID(67890, 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(3*time.Second).UnixMicro(), 4.5, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 3: Different timestamps
	err = encoder.StartMetricID(11111, 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(-time.Second).UnixMicro(), 5.5, "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(blobTs.Add(5*time.Second).UnixMicro(), 6.5, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	return blob
}

func createTestEncoder(t *testing.T) *NumericEncoder {
	encoder, err := NewNumericEncoder(time.Now())
	require.NoError(t, err)
	require.NotNil(t, encoder)

	return encoder
}

// ==============================================================================
// Encoder Tests
// ==============================================================================

func TestNewNumericEncoder(t *testing.T) {
	startTime := time.Now()

	t.Run("ValidConfiguration", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)
		require.NotNil(t, encoder)
		require.Equal(t, 0, encoder.MetricCount()) // No metrics added yet
		require.Equal(t, uint64(0), encoder.curMetricID)
		require.Equal(t, 0, encoder.claimed)
		require.Len(t, encoder.indexEntries, 0)
		require.Equal(t, initialIndexCapacity, cap(encoder.indexEntries)) // Initial capacity is 16
	})

	t.Run("DynamicGrowth", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)
		require.NotNil(t, encoder)
		require.Equal(t, 0, encoder.MetricCount())

		// After adding metrics, count should reflect actual metrics
		// (This is tested in other test cases)
	})

	t.Run("NegativeMetricCount", func(t *testing.T) {
		// No longer relevant - removed metricCount parameter
		// Dynamic growth handles all valid cases
	})

	t.Run("TooManyMetrics", func(t *testing.T) {
		// Test will be added later to check MaxMetricCount limit
		// during StartMetric* operations
	})

	t.Run("WithOptions", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime,
			WithTimestampEncoding(format.TypeDelta),
			WithValueEncoding(format.TypeRaw),
			WithTimestampCompression(format.CompressionNone),
			WithValueCompression(format.CompressionNone))
		require.NoError(t, err)
		require.NotNil(t, encoder)
	})
}

func TestNumericEncoder_StartMetricID(t *testing.T) {
	encoder := createTestEncoder(t)

	t.Run("ValidStart", func(t *testing.T) {
		err := encoder.StartMetricID(123, 10)
		require.NoError(t, err)
		require.Equal(t, uint64(123), encoder.curMetricID)
		require.Equal(t, 10, encoder.claimed)
	})

	t.Run("MetricAlreadyStarted", func(t *testing.T) {
		encoder := createTestEncoder(t)
		// First start should succeed
		err := encoder.StartMetricID(456, 5)
		require.NoError(t, err)

		// Second start should fail
		err = encoder.StartMetricID(789, 3)
		require.Error(t, err)
		require.Contains(t, err.Error(), "already started")
	})

	t.Run("ZeroMetricID", func(t *testing.T) {
		encoder := createTestEncoder(t)
		err := encoder.StartMetricID(0, 5)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid metric ID")
	})

	t.Run("InvalidDataPointsCount", func(t *testing.T) {
		encoder := createTestEncoder(t)
		// Zero data points
		err := encoder.StartMetricID(123, 0)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid number of data points")

		// Negative data points
		err = encoder.StartMetricID(124, -1)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid number of data points")

		// Too many data points (> uint16 max)
		err = encoder.StartMetricID(125, 70000)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid number of data points")
	})

	t.Run("ExceedMaxMetricCount", func(t *testing.T) {
		// Test that exceeding MaxMetricCount (65536) is rejected
		encoder := createTestEncoder(t)

		// Add MaxMetricCount metrics successfully
		// (This would take too long, so we'll just test the logic)
		// Instead, verify the error message format

		// Simulate having MaxMetricCount metrics already added
		for i := 0; i < MaxMetricCount; i++ {
			encoder.indexEntries = append(encoder.indexEntries, section.NumericIndexEntry{})
		}

		// Next metric should fail
		err := encoder.StartMetricID(uint64(MaxMetricCount+1), 1)
		require.Error(t, err)
		require.Contains(t, err.Error(), "metric count exceeded")
		require.Contains(t, err.Error(), "65536") // MaxMetricCount
	})
}

func TestNumericEncoder_StartMetricName(t *testing.T) {
	encoder := createTestEncoder(t)

	t.Run("ValidMetricName", func(t *testing.T) {
		err := encoder.StartMetricName("cpu.usage", 5)
		require.NoError(t, err)
		require.NotEqual(t, uint64(0), encoder.curMetricID)
		require.Equal(t, 5, encoder.claimed)
	})

	t.Run("SameNameGeneratesSameID", func(t *testing.T) {
		encoder1 := createTestEncoder(t)
		encoder2 := createTestEncoder(t)

		err := encoder1.StartMetricName("memory.usage", 3)
		require.NoError(t, err)
		id1 := encoder1.curMetricID

		err = encoder2.StartMetricName("memory.usage", 3)
		require.NoError(t, err)
		id2 := encoder2.curMetricID

		require.Equal(t, id1, id2)
	})
}

func TestNumericEncoder_AddDataPoint(t *testing.T) {
	encoder := createTestEncoder(t)

	t.Run("NoMetricStarted", func(t *testing.T) {
		e := createTestEncoder(t)
		err := e.AddDataPoint(time.Now().UnixMicro(), 42.5, "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "too many data points")
	})

	t.Run("ValidDataPoint", func(t *testing.T) {
		err := encoder.StartMetricID(123, 3)
		require.NoError(t, err)

		timestamp := time.Now().UnixMicro()
		value := 85.7

		err = encoder.AddDataPoint(timestamp, value, "")
		require.NoError(t, err)

		// Verify encoder state
		expectedTsLen := encoder.ts.length + 1
		expectedValLen := encoder.val.length + 1
		require.Equal(t, expectedTsLen, encoder.tsEncoder.Len())
		require.Equal(t, expectedValLen, encoder.valEncoder.Len())
	})

	t.Run("TooManyDataPoints", func(t *testing.T) {
		encoder := createTestEncoder(t)
		err := encoder.StartMetricID(456, 2)
		require.NoError(t, err)

		// Add allowed number of data points
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 1.0, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 2.0, "")
		require.NoError(t, err)

		// This should fail
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 3.0, "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "too many data points")
	})
}

func TestNumericEncoder_AddDataPoints(t *testing.T) {
	encoder := createTestEncoder(t)

	t.Run("EmptySlices", func(t *testing.T) {
		err := encoder.StartMetricID(123, 5)
		require.NoError(t, err)

		err = encoder.AddDataPoints([]int64{}, []float64{}, nil)
		require.NoError(t, err) // Should be no-op
	})

	t.Run("MismatchedLengths", func(t *testing.T) {
		e := createTestEncoder(t)
		err := e.StartMetricID(123, 5)
		require.NoError(t, err)

		timestamps := []int64{1, 2, 3}
		values := []float64{1.0, 2.0} // One less value

		err = e.AddDataPoints(timestamps, values, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "mismatched lengths")
	})

	t.Run("ValidDataPoints", func(t *testing.T) {
		encoder := createTestEncoder(t)
		err := encoder.StartMetricID(456, 3)
		require.NoError(t, err)

		now := time.Now().UnixMicro()
		timestamps := []int64{now, now + 1000000, now + 2000000}
		values := []float64{10.5, 20.3, 30.7}

		err = encoder.AddDataPoints(timestamps, values, nil)
		require.NoError(t, err)

		// Verify all data was added
		expectedTsLen := encoder.ts.length + 3
		expectedValLen := encoder.val.length + 3
		require.Equal(t, expectedTsLen, encoder.tsEncoder.Len())
		require.Equal(t, expectedValLen, encoder.valEncoder.Len())
	})

	t.Run("TooManyDataPoints", func(t *testing.T) {
		encoder := createTestEncoder(t)
		err := encoder.StartMetricID(789, 2)
		require.NoError(t, err)

		timestamps := []int64{1, 2, 3, 4} // More than claimed (2)
		values := []float64{1.0, 2.0, 3.0, 4.0}

		err = encoder.AddDataPoints(timestamps, values, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "too many data points")
	})
}

func TestNumericEncoder_EndMetric(t *testing.T) {
	t.Run("NoMetricStarted", func(t *testing.T) {
		encoder := createTestEncoder(t)
		err := encoder.EndMetric()
		require.Error(t, err)
		require.Contains(t, err.Error(), "no metric started")
	})

	t.Run("NoDataPointsAdded", func(t *testing.T) {
		encoder := createTestEncoder(t)
		err := encoder.StartMetricID(123, 2)
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.Error(t, err)
		require.Contains(t, err.Error(), "no data points added")
	})

	t.Run("DataPointCountMismatch", func(t *testing.T) {
		encoder := createTestEncoder(t)
		err := encoder.StartMetricID(123, 3)
		require.NoError(t, err)

		// Add only 2 data points when 3 were claimed
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 1.0, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 2.0, "")
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.Error(t, err)
		require.Contains(t, err.Error(), "data point count mismatch")
	})

	t.Run("ValidEndMetric", func(t *testing.T) {
		encoder := createTestEncoder(t)
		err := encoder.StartMetricID(456, 2)
		require.NoError(t, err)

		// Add exact number of claimed data points
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 10.5, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro()+1000000, 20.3, "")
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)

		// Verify state reset
		require.Equal(t, uint64(0), encoder.curMetricID)
		require.Equal(t, 0, encoder.claimed)
		require.Len(t, encoder.indexEntries, 1)

		// Verify index entry
		entry := encoder.indexEntries[0]
		require.Equal(t, uint64(456), entry.MetricID)
		require.Equal(t, 2, entry.Count)
	})

	t.Run("MultipleMetrics", func(t *testing.T) {
		encoder := createTestEncoder(t)
		now := time.Now().UnixMicro()

		// First metric
		err := encoder.StartMetricID(100, 2)
		require.NoError(t, err)
		err = encoder.AddDataPoint(now, 1.0, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(now+1000000, 2.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Second metric
		err = encoder.StartMetricID(200, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(now+2000000, 3.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Verify both metrics were recorded
		require.Len(t, encoder.indexEntries, 2)

		// Verify offset tracking (second metric should have non-zero offsets)
		entry1 := encoder.indexEntries[0]
		entry2 := encoder.indexEntries[1]

		require.Equal(t, uint64(100), entry1.MetricID)
		require.Equal(t, uint64(200), entry2.MetricID)

		// Second metric's offsets should be after first metric's data
		require.True(t, entry2.TimestampOffset > entry1.TimestampOffset)
		require.True(t, entry2.ValueOffset > entry1.ValueOffset)
	})
}

func TestNumericEncoder_Finish(t *testing.T) {
	t.Run("MetricNotEnded", func(t *testing.T) {
		encoder := createTestEncoder(t)
		err := encoder.StartMetricID(123, 1)
		require.NoError(t, err)

		_, err = encoder.Finish()
		require.Error(t, err)
		require.Contains(t, err.Error(), "unended metric")
	})

	t.Run("NoMetricsAdded", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Try to finish without adding any metrics
		_, err := encoder.Finish()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrNoMetricsAdded)
	})

	t.Run("ValidFinish", func(t *testing.T) {
		encoder := createTestEncoder(t)
		now := time.Now().UnixMicro()

		// Add first metric
		err := encoder.StartMetricID(100, 2)
		require.NoError(t, err)
		err = encoder.AddDataPoints([]int64{now, now + 1000000}, []float64{1.0, 2.0}, nil)
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Add second metric
		err = encoder.StartMetricID(200, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(now+2000000, 3.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Finish encoding
		blob, err := encoder.Finish()
		require.NoError(t, err)
		require.NotNil(t, blob)
		require.Greater(t, len(blob), 0)

		// Verify blob contains expected sections
		// At minimum: header (32 bytes) + 2 index entries (32 bytes) + compressed data
		minExpectedSize := 32 + 2*16 // header + index entries
		require.GreaterOrEqual(t, len(blob), minExpectedSize)
	})

	t.Run("EmptyEncoder", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Finish without adding any metrics should now return error
		blob, err := encoder.Finish()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrNoMetricsAdded)
		require.Nil(t, blob)
	})
}

func TestNumericEncoder_ValidationMethods(t *testing.T) {
	encoder := createTestEncoder(t)

	t.Run("validateMetricData", func(t *testing.T) {
		// Set up a proper encoder with a started metric for 3 data points
		err := encoder.StartMetricID(123, 3)
		require.NoError(t, err)

		// Valid data - matching counts and matches claimed count
		err = encoder.validateMetricData(3, 3, 3)
		require.NoError(t, err)

		// Test with a fresh encoder for other error cases
		encoder2 := createTestEncoder(t)
		err = encoder2.StartMetricID(456, 2) // Claim 2 data points
		require.NoError(t, err)

		// No timestamps
		err = encoder2.validateMetricData(0, 3, 3)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no data points added")

		// No values
		err = encoder2.validateMetricData(3, 0, 3)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no data points added")

		// Count mismatch
		err = encoder2.validateMetricData(3, 2, 3)
		require.Error(t, err)
		require.Contains(t, err.Error(), "data point count mismatch")

		// Data points count mismatch with claimed
		err = encoder2.validateMetricData(1, 1, 2) // Only 1 but claimed 2
		require.Error(t, err)
		require.Contains(t, err.Error(), "data point count mismatch")
	})
}

func TestNumericEncoder_ConfigurationMethods(t *testing.T) {
	encoder := createTestEncoder(t)

	t.Run("setTimestampEncoding", func(t *testing.T) {
		err := encoder.NumericEncoderConfig.setTimestampEncoding(format.TypeRaw)
		require.NoError(t, err)

		err = encoder.NumericEncoderConfig.setTimestampEncoding(format.TypeDelta)
		require.NoError(t, err)

		err = encoder.NumericEncoderConfig.setTimestampEncoding(format.TypeGorilla)
		require.Error(t, err)
		require.Contains(t, err.Error(), "gorilla encoding is not supported")

		err = encoder.NumericEncoderConfig.setTimestampEncoding(format.EncodingType(99))
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid timestamp encoding")
	})

	t.Run("setValueEncoding", func(t *testing.T) {
		err := encoder.NumericEncoderConfig.setValueEncoding(format.TypeRaw)
		require.NoError(t, err)

		err = encoder.NumericEncoderConfig.setValueEncoding(format.TypeGorilla)
		require.NoError(t, err)
	})

	t.Run("setTimestampCompression", func(t *testing.T) {
		for _, compression := range []format.CompressionType{
			format.CompressionNone,
			format.CompressionZstd,
			format.CompressionS2,
			format.CompressionLZ4,
		} {
			err := encoder.NumericEncoderConfig.setTimestampCompression(compression)
			require.NoError(t, err)
		}
	})

	t.Run("setValueCompression", func(t *testing.T) {
		err := encoder.NumericEncoderConfig.setValueCompression(format.CompressionNone)
		require.NoError(t, err)

		err = encoder.NumericEncoderConfig.setValueCompression(format.CompressionZstd)
		require.NoError(t, err)
	})

	t.Run("setEndianess", func(t *testing.T) {
		// These should not return errors
		encoder.NumericEncoderConfig.setEndianess(littleEndianOpt)
		encoder.NumericEncoderConfig.setEndianess(bigEndianOpt)
		encoder.NumericEncoderConfig.setEndianess(endianness(99)) // Should default to little endian
	})
}

func TestNumericEncoder_Integration(t *testing.T) {
	t.Run("CompleteWorkflow", func(t *testing.T) {
		encoder, err := NewNumericEncoder(time.Now())
		require.NoError(t, err)

		now := time.Now().UnixMicro()

		// Metric 1: CPU usage with 3 data points
		err = encoder.StartMetricName("cpu.usage", 3)
		require.NoError(t, err)

		err = encoder.AddDataPoint(now, 85.5, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(now+1000000, 90.2, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(now+2000000, 87.8, "")
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)

		// Metric 2: Memory usage with bulk data points
		err = encoder.StartMetricName("memory.usage", 2)
		require.NoError(t, err)

		timestamps := []int64{now + 3000000, now + 4000000}
		values := []float64{75.3, 78.1}
		err = encoder.AddDataPoints(timestamps, values, nil)
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)

		// Finish and get blob
		blob, err := encoder.Finish()
		require.NoError(t, err)
		require.NotNil(t, blob)
		require.Greater(t, len(blob), 64) // Should be substantial size

		// Verify blob structure (basic checks)
		// First 32 bytes should be header
		require.GreaterOrEqual(t, len(blob), 32)
	})
}

// ==============================================================================
// NumericBlobSet Tests
// ==============================================================================

// ==============================================================================
// Offset Delta Encoding Tests
// ==============================================================================

func TestNumericEncoder_TimestampOffsetDelta(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)

	// Metric 1: 5 data points (timestamps: 40 bytes, values: 40 bytes with raw encoding)
	metric1Name := "cpu.usage"
	metric1ID := hash.ID(metric1Name)
	require.NoError(t, encoder.StartMetricID(metric1ID, 5))

	ts1 := startTime.UnixMicro()
	for i := 0; i < 5; i++ {
		require.NoError(t, encoder.AddDataPoint(ts1+int64(i)*1000000, float64(i)*1.5, ""))
	}
	require.NoError(t, encoder.EndMetric())

	// Metric 2: 3 data points (timestamps: 24 bytes, values: 24 bytes)
	metric2Name := "memory.usage"
	metric2ID := hash.ID(metric2Name)
	require.NoError(t, encoder.StartMetricID(metric2ID, 3))

	ts2 := ts1 + 10000000
	for i := 0; i < 3; i++ {
		require.NoError(t, encoder.AddDataPoint(ts2+int64(i)*1000000, float64(i)*2.0, ""))
	}
	require.NoError(t, encoder.EndMetric())

	// Metric 3: 7 data points (timestamps: 56 bytes, values: 56 bytes)
	metric3Name := "disk.usage"
	metric3ID := hash.ID(metric3Name)
	require.NoError(t, encoder.StartMetricID(metric3ID, 7))

	ts3 := ts2 + 20000000
	for i := 0; i < 7; i++ {
		require.NoError(t, encoder.AddDataPoint(ts3+int64(i)*1000000, float64(i)*3.0, ""))
	}
	require.NoError(t, encoder.EndMetric())

	// Get encoded data
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Verify index entries have delta offsets stored
	entries := encoder.indexEntries
	require.Len(t, entries, 3)

	// First metric: offset delta should equal absolute offset (since lastTsOffset and lastValOffset were 0)
	// With raw encoding, each timestamp is 8 bytes and each value is 8 bytes
	entry1 := entries[0]
	require.Equal(t, metric1ID, entry1.MetricID)
	require.Equal(t, 5, entry1.Count)
	expectedTsOffset1 := 0 // First metric starts at 0
	expectedValOffset1 := 0
	require.Equal(t, expectedTsOffset1, entry1.TimestampOffset, "First metric TimestampOffset should be 0 (delta from 0)")
	require.Equal(t, expectedValOffset1, entry1.ValueOffset, "First metric ValueOffset should be 0 (delta from 0)")

	// Second metric: offset delta should be the size of metric1's timestamps/values
	entry2 := entries[1]
	require.Equal(t, metric2ID, entry2.MetricID)
	require.Equal(t, 3, entry2.Count)
	expectedTsDelta2 := 5 * 8  // 5 timestamps × 8 bytes each = 40 bytes
	expectedValDelta2 := 5 * 8 // 5 values × 8 bytes each = 40 bytes
	require.Equal(t, expectedTsDelta2, entry2.TimestampOffset, "Second metric TimestampOffset should be delta of 40 bytes")
	require.Equal(t, expectedValDelta2, entry2.ValueOffset, "Second metric ValueOffset should be delta of 40 bytes")

	// Third metric: offset delta should be the size of metric2's timestamps/values
	entry3 := entries[2]
	require.Equal(t, metric3ID, entry3.MetricID)
	require.Equal(t, 7, entry3.Count)
	expectedTsDelta3 := 3 * 8  // 3 timestamps × 8 bytes each = 24 bytes
	expectedValDelta3 := 3 * 8 // 3 values × 8 bytes each = 24 bytes
	require.Equal(t, expectedTsDelta3, entry3.TimestampOffset, "Third metric TimestampOffset should be delta of 24 bytes")
	require.Equal(t, expectedValDelta3, entry3.ValueOffset, "Third metric ValueOffset should be delta of 24 bytes")

	// Now decode and verify absolute offsets are reconstructed correctly
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify decoded entries have absolute offsets for both timestamps and values
	decodedEntry1, ok := blob.index.byID[metric1ID]
	require.True(t, ok)
	require.Equal(t, 0, decodedEntry1.TimestampOffset, "Decoded metric1 should have absolute TimestampOffset 0")
	require.Equal(t, 0, decodedEntry1.ValueOffset, "Decoded metric1 should have absolute ValueOffset 0")

	decodedEntry2, ok := blob.index.byID[metric2ID]
	require.True(t, ok)
	require.Equal(t, 40, decodedEntry2.TimestampOffset, "Decoded metric2 should have absolute TimestampOffset 40")
	require.Equal(t, 40, decodedEntry2.ValueOffset, "Decoded metric2 should have absolute ValueOffset 40")

	decodedEntry3, ok := blob.index.byID[metric3ID]
	require.True(t, ok)
	require.Equal(t, 64, decodedEntry3.TimestampOffset, "Decoded metric3 should have absolute TimestampOffset 64 (40+24)")
	require.Equal(t, 64, decodedEntry3.ValueOffset, "Decoded metric3 should have absolute ValueOffset 64 (40+24)")

	// Verify we can correctly read timestamps using reconstructed offsets
	timestamps1 := make([]int64, 0, 5)
	for ts := range blob.AllTimestamps(metric1ID) {
		timestamps1 = append(timestamps1, ts)
	}
	require.Len(t, timestamps1, 5)
	require.Equal(t, ts1, timestamps1[0])

	timestamps2 := make([]int64, 0, 3)
	for ts := range blob.AllTimestamps(metric2ID) {
		timestamps2 = append(timestamps2, ts)
	}
	require.Len(t, timestamps2, 3)
	require.Equal(t, ts2, timestamps2[0])

	timestamps3 := make([]int64, 0, 7)
	for ts := range blob.AllTimestamps(metric3ID) {
		timestamps3 = append(timestamps3, ts)
	}
	require.Len(t, timestamps3, 7)
	require.Equal(t, ts3, timestamps3[0])
}

func TestNumericEncoder_TimestampOffsetDelta_SingleMetric(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)

	metricID := hash.ID("single.metric")
	require.NoError(t, encoder.StartMetricID(metricID, 10))

	ts := startTime.UnixMicro()
	for i := 0; i < 10; i++ {
		require.NoError(t, encoder.AddDataPoint(ts+int64(i)*1000000, float64(i), ""))
	}
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	// First metric should have both offset deltas of 0 (starts at beginning)
	entries := encoder.indexEntries
	require.Len(t, entries, 1)
	require.Equal(t, 0, entries[0].TimestampOffset)
	require.Equal(t, 0, entries[0].ValueOffset)

	// Decode and verify
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	decodedEntry, ok := blob.index.byID[metricID]
	require.True(t, ok)
	require.Equal(t, 0, decodedEntry.TimestampOffset)
	require.Equal(t, 0, decodedEntry.ValueOffset)

	timestamps := make([]int64, 0, 10)
	for ts := range blob.AllTimestamps(metricID) {
		timestamps = append(timestamps, ts)
	}
	require.Len(t, timestamps, 10)
	require.Equal(t, ts, timestamps[0])
}

func TestNumericEncoder_TimestampOffsetDelta_VaryingDataPoints(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricCount := 5

	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)

	metricSizes := []int{1, 10, 3, 20, 5}
	metricIDs := make([]uint64, metricCount)
	expectedAbsoluteTsOffsets := make([]int, metricCount)
	expectedAbsoluteValOffsets := make([]int, metricCount)

	currentAbsoluteTsOffset := 0
	currentAbsoluteValOffset := 0
	baseTS := startTime.UnixMicro()

	for i, size := range metricSizes {
		metricName := "metric" + string(rune('A'+i))
		metricIDs[i] = hash.ID(metricName)
		expectedAbsoluteTsOffsets[i] = currentAbsoluteTsOffset
		expectedAbsoluteValOffsets[i] = currentAbsoluteValOffset

		require.NoError(t, encoder.StartMetricID(metricIDs[i], size))

		ts := baseTS + int64(i)*10000000
		for j := 0; j < size; j++ {
			require.NoError(t, encoder.AddDataPoint(ts+int64(j)*1000000, float64(j), ""))
		}
		require.NoError(t, encoder.EndMetric())

		// Calculate next absolute offsets (raw encoding: 8 bytes per timestamp and value)
		currentAbsoluteTsOffset += size * 8
		currentAbsoluteValOffset += size * 8
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Verify encoder stored delta offsets
	entries := encoder.indexEntries
	require.Len(t, entries, metricCount)

	// First entry should have deltas = 0
	require.Equal(t, 0, entries[0].TimestampOffset)
	require.Equal(t, 0, entries[0].ValueOffset)

	// Subsequent entries should have deltas equal to previous metric's size
	for i := 1; i < metricCount; i++ {
		expectedDelta := metricSizes[i-1] * 8
		require.Equal(t, expectedDelta, entries[i].TimestampOffset,
			"Metric %d TimestampOffset should have delta = %d bytes (previous metric had %d timestamps)",
			i, expectedDelta, metricSizes[i-1])
		require.Equal(t, expectedDelta, entries[i].ValueOffset,
			"Metric %d ValueOffset should have delta = %d bytes (previous metric had %d values)",
			i, expectedDelta, metricSizes[i-1])
	}

	// Decode and verify absolute offsets are reconstructed
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	for i, metricID := range metricIDs {
		decodedEntry, ok := blob.index.byID[metricID]
		require.True(t, ok, "Metric %d should exist in decoded blob", i)
		require.Equal(t, expectedAbsoluteTsOffsets[i], decodedEntry.TimestampOffset,
			"Decoded metric %d should have absolute TimestampOffset %d", i, expectedAbsoluteTsOffsets[i])
		require.Equal(t, expectedAbsoluteValOffsets[i], decodedEntry.ValueOffset,
			"Decoded metric %d should have absolute ValueOffset %d", i, expectedAbsoluteValOffsets[i])

		// Verify data can be read correctly
		var timestamps []int64
		for ts := range blob.AllTimestamps(metricID) {
			timestamps = append(timestamps, ts)
		}
		require.Len(t, timestamps, metricSizes[i], "Metric %d should have %d timestamps", i, metricSizes[i])
	}
}

func TestNumericEncoder_TimestampOffsetDelta_WithDeltaEncoding(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricCount := 3

	encoder, err := NewNumericEncoder(startTime, WithTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)

	metricIDs := make([]uint64, metricCount)
	baseTS := startTime.UnixMicro()

	// Metric 1: 5 sequential timestamps (highly compressible)
	metricIDs[0] = hash.ID("metric1")
	require.NoError(t, encoder.StartMetricID(metricIDs[0], 5))
	for i := 0; i < 5; i++ {
		require.NoError(t, encoder.AddDataPoint(baseTS+int64(i)*1000000, float64(i), ""))
	}
	require.NoError(t, encoder.EndMetric())

	// Metric 2: 3 timestamps
	metricIDs[1] = hash.ID("metric2")
	require.NoError(t, encoder.StartMetricID(metricIDs[1], 3))
	for i := 0; i < 3; i++ {
		require.NoError(t, encoder.AddDataPoint(baseTS+int64(i)*2000000, float64(i), ""))
	}
	require.NoError(t, encoder.EndMetric())

	// Metric 3: 4 timestamps
	metricIDs[2] = hash.ID("metric3")
	require.NoError(t, encoder.StartMetricID(metricIDs[2], 4))
	for i := 0; i < 4; i++ {
		require.NoError(t, encoder.AddDataPoint(baseTS+int64(i)*500000, float64(i), ""))
	}
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	// With delta encoding, the sizes will be variable (not fixed 8 bytes per timestamp)
	// Verify encoder stored deltas
	entries := encoder.indexEntries
	require.Len(t, entries, 3)

	// First metric: delta = 0 (absolute offset at start)
	require.Equal(t, 0, entries[0].TimestampOffset)

	// Second and third metrics: deltas should be non-zero and different from raw encoding
	// We can't predict exact sizes with delta encoding, but we can verify logic consistency
	offset1 := entries[0].TimestampOffset
	delta2 := entries[1].TimestampOffset
	delta3 := entries[2].TimestampOffset

	require.Greater(t, delta2, 0, "Second metric should have non-zero delta")
	require.Greater(t, delta3, 0, "Third metric should have non-zero delta")

	// Decode and verify absolute offsets are reconstructed correctly
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify reconstructed absolute offsets
	decodedEntry1, ok := blob.index.byID[metricIDs[0]]
	require.True(t, ok)
	require.Equal(t, offset1, decodedEntry1.TimestampOffset, "First metric absolute offset should be 0")

	decodedEntry2, ok := blob.index.byID[metricIDs[1]]
	require.True(t, ok)
	expectedAbsOffset2 := offset1 + delta2
	require.Equal(t, expectedAbsOffset2, decodedEntry2.TimestampOffset,
		"Second metric absolute offset should be sum of deltas")

	decodedEntry3, ok := blob.index.byID[metricIDs[2]]
	require.True(t, ok)
	expectedAbsOffset3 := expectedAbsOffset2 + delta3
	require.Equal(t, expectedAbsOffset3, decodedEntry3.TimestampOffset,
		"Third metric absolute offset should be sum of all deltas")

	// Verify data integrity - can read all timestamps correctly
	for i, metricID := range metricIDs {
		var timestamps []int64
		for ts := range blob.AllTimestamps(metricID) {
			timestamps = append(timestamps, ts)
		}
		require.NotEmpty(t, timestamps, "Metric %d should have timestamps", i)
	}
}

func TestNumericEncoder_TimestampOffsetDelta_RoundTrip(t *testing.T) {
	testCases := []struct {
		name        string
		metricCount int
		dataSizes   []int // number of data points per metric
	}{
		{
			name:        "Two metrics equal size",
			metricCount: 2,
			dataSizes:   []int{10, 10},
		},
		{
			name:        "Three metrics varying sizes",
			metricCount: 3,
			dataSizes:   []int{5, 15, 8},
		},
		{
			name:        "Five metrics mixed sizes",
			metricCount: 5,
			dataSizes:   []int{1, 20, 3, 10, 7},
		},
		{
			name:        "Ten metrics",
			metricCount: 10,
			dataSizes:   []int{2, 4, 6, 8, 10, 12, 14, 16, 18, 20},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			encoder, err := NewNumericEncoder(startTime)
			require.NoError(t, err)

			metricIDs := make([]uint64, tc.metricCount)
			expectedData := make(map[uint64][]float64)

			baseTS := startTime.UnixMicro()
			for i := 0; i < tc.metricCount; i++ {
				metricName := "metric" + string(rune('0'+i))
				metricIDs[i] = hash.ID(metricName)

				require.NoError(t, encoder.StartMetricID(metricIDs[i], tc.dataSizes[i]))

				values := make([]float64, tc.dataSizes[i])
				ts := baseTS + int64(i)*10000000
				for j := 0; j < tc.dataSizes[i]; j++ {
					val := float64(i*100 + j)
					values[j] = val
					require.NoError(t, encoder.AddDataPoint(ts+int64(j)*1000000, val, ""))
				}
				expectedData[metricIDs[i]] = values

				require.NoError(t, encoder.EndMetric())
			}

			data, err := encoder.Finish()
			require.NoError(t, err)

			// Decode
			decoder, err := NewNumericDecoder(data)
			require.NoError(t, err)

			blob, err := decoder.Decode()
			require.NoError(t, err)

			// Verify all metrics can be read with correct data
			for i, metricID := range metricIDs {
				var timestamps []int64
				for ts := range blob.AllTimestamps(metricID) {
					timestamps = append(timestamps, ts)
				}
				require.Len(t, timestamps, tc.dataSizes[i],
					"Metric %d should have %d timestamps", i, tc.dataSizes[i])

				var values []float64
				for val := range blob.AllValues(metricID) {
					values = append(values, val)
				}
				require.Len(t, values, tc.dataSizes[i],
					"Metric %d should have %d values", i, tc.dataSizes[i])

				// Verify values match
				expectedValues := expectedData[metricID]
				for j, val := range values {
					require.Equal(t, expectedValues[j], val,
						"Metric %d value %d mismatch", i, j)
				}
			}
		})
	}
}

// TestNumericEncoderCollisionDetection tests that collision detection works correctly
func TestNumericEncoderCollisionDetection(t *testing.T) {
	encoder, err := NewNumericEncoder(time.Now())
	require.NoError(t, err)

	// Start first metric
	err = encoder.StartMetricName("test.metric", 1)
	require.NoError(t, err)
	err = encoder.AddDataPoint(time.Now().UnixMicro(), 1.0, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Try to start metric with same name (should fail with duplicate error)
	err = encoder.StartMetricName("test.metric", 1)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)

	// HasMetricNames should still be false (no real collision)
	require.False(t, encoder.header.Flag.HasMetricNames())
}

// TestNumericEncoderNoCollision tests normal encoding without collisions
func TestNumericEncoderNoCollision(t *testing.T) {
	encoder, err := NewNumericEncoder(time.Now())
	require.NoError(t, err)

	metrics := []string{"cpu.usage", "memory.total", "disk.io"}
	ts := time.Now().UnixMicro()

	for _, metricName := range metrics {
		err = encoder.StartMetricName(metricName, 2)
		require.NoError(t, err)

		err = encoder.AddDataPoint(ts, 100.0, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(ts+1000, 200.0, "")
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	// Should not have metric names payload (no collision)
	require.False(t, encoder.header.Flag.HasMetricNames())

	// Finish encoding
	data, err := encoder.Finish()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Decode and verify
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, 3, len(blob.index.byID))
}

// TestNumericEncoderWithMetricNamesPayload tests encoding with metric names payload
// This test simulates the case where metric names payload is explicitly enabled
func TestNumericEncoderWithMetricNamesPayload(t *testing.T) {
	encoder, err := NewNumericEncoder(time.Now())
	require.NoError(t, err)

	// Manually enable metric names payload (simulating collision detection)
	encoder.header.Flag.SetHasMetricNames(true)

	metrics := []string{"test.metric.one", "test.metric.two"}
	ts := time.Now().UnixMicro()

	for _, metricName := range metrics {
		err = encoder.StartMetricName(metricName, 1)
		require.NoError(t, err)

		err = encoder.AddDataPoint(ts, 42.0, "")
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	// Should have metric names payload
	require.True(t, encoder.header.Flag.HasMetricNames())

	// Finish encoding
	data, err := encoder.Finish()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Decode and verify
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, 2, len(blob.index.byID))

	// Header should indicate metric names present
	require.True(t, blob.flag.HasMetricNames())
}

// TestNumericEncoderDecoderRoundTrip tests full encode/decode cycle
func TestNumericEncoderDecoderRoundTrip(t *testing.T) {
	tests := []struct {
		name                string
		metrics             []string
		dataPointsPerMetric int
		enableMetricNames   bool
	}{
		{
			name:                "small blob without metric names",
			metrics:             []string{"m1", "m2", "m3"},
			dataPointsPerMetric: 5,
			enableMetricNames:   false,
		},
		{
			name:                "small blob with metric names",
			metrics:             []string{"m1", "m2", "m3"},
			dataPointsPerMetric: 5,
			enableMetricNames:   true,
		},
		{
			name:                "large blob without metric names",
			metrics:             generateMetricNames(100),
			dataPointsPerMetric: 10,
			enableMetricNames:   false,
		},
		{
			name:                "large blob with metric names",
			metrics:             generateMetricNames(100),
			dataPointsPerMetric: 10,
			enableMetricNames:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoder, err := NewNumericEncoder(time.Now())
			require.NoError(t, err)

			if tt.enableMetricNames {
				encoder.header.Flag.SetHasMetricNames(true)
			}

			baseTs := time.Now().UnixMicro()

			// Encode all metrics
			for _, metricName := range tt.metrics {
				err = encoder.StartMetricName(metricName, tt.dataPointsPerMetric)
				require.NoError(t, err)

				for i := range tt.dataPointsPerMetric {
					ts := baseTs + int64(i*1000)
					value := float64(i) * 10.0
					err = encoder.AddDataPoint(ts, value, "")
					require.NoError(t, err)
				}

				err = encoder.EndMetric()
				require.NoError(t, err)
			}

			// Finish encoding
			data, err := encoder.Finish()
			require.NoError(t, err)
			require.NotEmpty(t, data)

			// Decode
			decoder, err := NewNumericDecoder(data)
			require.NoError(t, err)

			blob, err := decoder.Decode()
			require.NoError(t, err)
			require.Equal(t, len(tt.metrics), len(blob.index.byID))

			// Verify flag
			require.Equal(t, tt.enableMetricNames, blob.flag.HasMetricNames())
		})
	}
}

// TestNumericEncoderMetricNamesOrdering tests that metric names maintain order
func TestNumericEncoderMetricNamesOrdering(t *testing.T) {
	encoder, err := NewNumericEncoder(time.Now())
	require.NoError(t, err)

	encoder.header.Flag.SetHasMetricNames(true)

	// Add metrics in specific order
	orderedMetrics := []string{
		"zzz.last",
		"aaa.first",
		"mmm.middle",
		"xyz.random",
		"abc.sorted",
	}

	ts := time.Now().UnixMicro()
	for _, metricName := range orderedMetrics {
		err = encoder.StartMetricName(metricName, 1)
		require.NoError(t, err)

		err = encoder.AddDataPoint(ts, 1.0, "")
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	// Verify metricNamesList maintains insertion order
	require.Equal(t, orderedMetrics, encoder.collisionTracker.GetMetricNames())

	// Encode and decode
	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, len(orderedMetrics), len(blob.index.byID))
}

// TestNumericEncoderEmptyMetricName tests that empty metric names are rejected
func TestNumericEncoderEmptyMetricName(t *testing.T) {
	encoder, err := NewNumericEncoder(time.Now())
	require.NoError(t, err)

	err = encoder.StartMetricName("", 1)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidMetricName)
}

// Helper function to generate metric names
func generateMetricNames(count int) []string {
	names := make([]string, count)
	for i := range count {
		// Generate unique names using index to avoid duplicates
		names[i] = "metric." + string(rune('a'+i%26)) + "." + string(rune('0'+i/26))
	}

	return names
}

// TestNumericEncoder_HeaderImmutability verifies that the original header
// remains unchanged after Finish() is called. This ensures the encoder is
// prepared for future stateless patterns where encoders can be reused.
func TestNumericEncoder_HeaderImmutability(t *testing.T) {
	t.Run("HeaderUnchangedAfterFinish", func(t *testing.T) {
		startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Capture original header state before encoding
		originalMetricCount := encoder.header.MetricCount
		originalIndexOffset := encoder.header.IndexOffset
		originalTsOffset := encoder.header.TimestampPayloadOffset
		originalValOffset := encoder.header.ValuePayloadOffset
		originalTagOffset := encoder.header.TagPayloadOffset
		originalHasMetricNames := encoder.header.Flag.HasMetricNames()

		// Add some metrics
		err = encoder.StartMetricID(123, 2)
		require.NoError(t, err)
		err = encoder.AddDataPoint(1000, 1.0, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(2000, 2.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Call Finish - this should clone header, not mutate original
		blob, err := encoder.Finish()
		require.NoError(t, err)
		require.NotNil(t, blob)

		// Verify original header fields remain unchanged
		require.Equal(t, originalMetricCount, encoder.header.MetricCount,
			"original header MetricCount should not be mutated")
		require.Equal(t, originalIndexOffset, encoder.header.IndexOffset,
			"original header IndexOffset should not be mutated")
		require.Equal(t, originalTsOffset, encoder.header.TimestampPayloadOffset,
			"original header TimestampPayloadOffset should not be mutated")
		require.Equal(t, originalValOffset, encoder.header.ValuePayloadOffset,
			"original header ValuePayloadOffset should not be mutated")
		require.Equal(t, originalTagOffset, encoder.header.TagPayloadOffset,
			"original header TagPayloadOffset should not be mutated")
		require.Equal(t, originalHasMetricNames, encoder.header.Flag.HasMetricNames(),
			"original header HasMetricNames flag should not be mutated")
	})

	t.Run("CollisionFlagNotMutatingOriginalHeader", func(t *testing.T) {
		startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Capture original flag state
		originalHasMetricNames := encoder.header.Flag.HasMetricNames()
		require.False(t, originalHasMetricNames, "should start with HasMetricNames=false")

		// Add metrics that will cause collision
		// These two strings have different hashes, but we'll use StartMetricName
		// to trigger collision detection logic
		err = encoder.StartMetricName("metric.test.1", 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(1000, 1.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Verify hasCollision flag might be set internally, but header unchanged
		require.Equal(t, originalHasMetricNames, encoder.header.Flag.HasMetricNames(),
			"original header HasMetricNames should not be mutated during StartMetricName")

		// After Finish, verify original header still unchanged
		blob, err := encoder.Finish()
		require.NoError(t, err)
		require.NotNil(t, blob)

		require.Equal(t, originalHasMetricNames, encoder.header.Flag.HasMetricNames(),
			"original header HasMetricNames should remain unchanged after Finish")
	})

	t.Run("MultipleMetricsHeaderImmutability", func(t *testing.T) {
		startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Capture all original header values
		originalHeader := &struct {
			MetricCount            uint32
			IndexOffset            uint32
			TimestampPayloadOffset uint32
			ValuePayloadOffset     uint32
			TagPayloadOffset       uint32
			HasMetricNames         bool
		}{
			MetricCount:            encoder.header.MetricCount,
			IndexOffset:            encoder.header.IndexOffset,
			TimestampPayloadOffset: encoder.header.TimestampPayloadOffset,
			ValuePayloadOffset:     encoder.header.ValuePayloadOffset,
			TagPayloadOffset:       encoder.header.TagPayloadOffset,
			HasMetricNames:         encoder.header.Flag.HasMetricNames(),
		}

		// Add multiple metrics
		for i := uint64(100); i < 105; i++ {
			err = encoder.StartMetricID(i, 3)
			require.NoError(t, err)
			for j := int64(0); j < 3; j++ {
				err = encoder.AddDataPoint(j*1000, float64(j), "")
				require.NoError(t, err)
			}
			err = encoder.EndMetric()
			require.NoError(t, err)
		}

		// Finish encoding
		blob, err := encoder.Finish()
		require.NoError(t, err)
		require.NotNil(t, blob)

		// Verify ALL original header fields remain unchanged
		require.Equal(t, originalHeader.MetricCount, encoder.header.MetricCount)
		require.Equal(t, originalHeader.IndexOffset, encoder.header.IndexOffset)
		require.Equal(t, originalHeader.TimestampPayloadOffset, encoder.header.TimestampPayloadOffset)
		require.Equal(t, originalHeader.ValuePayloadOffset, encoder.header.ValuePayloadOffset)
		require.Equal(t, originalHeader.TagPayloadOffset, encoder.header.TagPayloadOffset)
		require.Equal(t, originalHeader.HasMetricNames, encoder.header.Flag.HasMetricNames())
	})
}

// TestNumericEncoder_ExclusiveIdentifierMode tests that StartMetricID and StartMetricName
// cannot be mixed in the same encoder instance.
func TestNumericEncoder_ExclusiveIdentifierMode(t *testing.T) {
	t.Run("IDModeToNameModeFails", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Start with ID mode
		err := encoder.StartMetricID(123, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 1.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Try to switch to name mode - should fail
		err = encoder.StartMetricName("metric.name", 1)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrMixedIdentifierMode)
		require.Contains(t, err.Error(), "cannot use StartMetricName after StartMetricID")
	})

	t.Run("NameModeToIDModeFails", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Start with name mode
		err := encoder.StartMetricName("metric.one", 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 1.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Try to switch to ID mode - should fail
		err = encoder.StartMetricID(456, 1)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrMixedIdentifierMode)
		require.Contains(t, err.Error(), "cannot use StartMetricID after StartMetricName")
	})

	t.Run("IDModeConsistentAllowed", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Multiple ID mode calls should work
		err := encoder.StartMetricID(123, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 1.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		err = encoder.StartMetricID(456, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 2.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		err = encoder.StartMetricID(789, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 3.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		blob, err := encoder.Finish()
		require.NoError(t, err)
		require.NotNil(t, blob)
	})

	t.Run("NameModeConsistentAllowed", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Multiple name mode calls should work
		err := encoder.StartMetricName("metric.one", 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 1.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		err = encoder.StartMetricName("metric.two", 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 2.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		err = encoder.StartMetricName("metric.three", 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 3.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		blob, err := encoder.Finish()
		require.NoError(t, err)
		require.NotNil(t, blob)
	})

	t.Run("ModeIsSetCorrectly", func(t *testing.T) {
		encoder1 := createTestEncoder(t)
		encoder2 := createTestEncoder(t)

		// Initially undefined
		require.Equal(t, modeUndefined, encoder1.identifierMode)
		require.Equal(t, modeUndefined, encoder2.identifierMode)

		// After StartMetricID
		err := encoder1.StartMetricID(123, 1)
		require.NoError(t, err)
		require.Equal(t, modeUserID, encoder1.identifierMode)

		// After StartMetricName
		err = encoder2.StartMetricName("metric.name", 1)
		require.NoError(t, err)
		require.Equal(t, modeNameManaged, encoder2.identifierMode)
	})
}

// TestNumericEncoder_IDModeOptimization tests that ID mode doesn't create collision tracker
// and uses simple map for duplicate detection.
func TestNumericEncoder_IDModeOptimization(t *testing.T) {
	t.Run("IDModeNoCollisionTracker", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Start with ID mode
		err := encoder.StartMetricID(123, 1)
		require.NoError(t, err)

		// Verify collision tracker is NOT created
		require.Nil(t, encoder.collisionTracker, "ID mode should not create collision tracker")

		// Verify usedIDs is created
		require.NotNil(t, encoder.usedIDs, "ID mode should create usedIDs map")

		err = encoder.AddDataPoint(time.Now().UnixMicro(), 1.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Verify ID was tracked in usedIDs
		_, exists := encoder.usedIDs[123]
		require.True(t, exists, "Metric ID should be tracked in usedIDs")
	})

	t.Run("IDModeDuplicateDetection", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Add first metric
		err := encoder.StartMetricID(123, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 1.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Try to add duplicate ID - should fail
		err = encoder.StartMetricID(123, 1)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrHashCollision)
		require.Contains(t, err.Error(), "metric ID 0x000000000000007b already used")
	})

	t.Run("IDModeMultipleMetrics", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Add multiple metrics
		for i := 0; i < 5; i++ {
			err := encoder.StartMetricID(uint64(100+i), 2)
			require.NoError(t, err)
			err = encoder.AddDataPoint(time.Now().UnixMicro(), float64(i), "")
			require.NoError(t, err)
			err = encoder.AddDataPoint(time.Now().UnixMicro(), float64(i+1), "")
			require.NoError(t, err)
			err = encoder.EndMetric()
			require.NoError(t, err)
		}

		// Verify all IDs tracked
		require.Len(t, encoder.usedIDs, 5, "Should track all 5 metric IDs")
		require.Nil(t, encoder.collisionTracker, "Should not create collision tracker")

		// Finish should succeed
		blob, err := encoder.Finish()
		require.NoError(t, err)
		require.NotNil(t, blob)

		// Verify no metric names in header
		require.False(t, encoder.header.Flag.HasMetricNames(), "ID mode should not have metric names")
	})
}

// TestNumericEncoder_NameModeOptimization tests that Name mode creates collision tracker
// and tracks metric names properly.
func TestNumericEncoder_NameModeOptimization(t *testing.T) {
	t.Run("NameModeHasCollisionTracker", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Start with Name mode
		err := encoder.StartMetricName("metric.one", 1)
		require.NoError(t, err)

		// Verify collision tracker IS created
		require.NotNil(t, encoder.collisionTracker, "Name mode should create collision tracker")

		// Verify usedIDs is NOT created
		require.Nil(t, encoder.usedIDs, "Name mode should not create usedIDs map")

		err = encoder.AddDataPoint(time.Now().UnixMicro(), 1.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Verify metric name was tracked
		require.Equal(t, 1, encoder.collisionTracker.Count(), "Should track one metric name")
	})

	t.Run("NameModeMultipleMetrics", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Add multiple metrics
		metricNames := []string{"cpu.usage", "memory.usage", "disk.io", "network.rx", "network.tx"}
		for i, name := range metricNames {
			err := encoder.StartMetricName(name, 2)
			require.NoError(t, err)
			err = encoder.AddDataPoint(time.Now().UnixMicro(), float64(i), "")
			require.NoError(t, err)
			err = encoder.AddDataPoint(time.Now().UnixMicro(), float64(i+1), "")
			require.NoError(t, err)
			err = encoder.EndMetric()
			require.NoError(t, err)
		}

		// Verify all names tracked
		require.Equal(t, 5, encoder.collisionTracker.Count(), "Should track all 5 metric names")
		require.Nil(t, encoder.usedIDs, "Should not create usedIDs map")

		// Finish should succeed
		blob, err := encoder.Finish()
		require.NoError(t, err)
		require.NotNil(t, blob)
	})

	t.Run("NameModeDuplicateNameDetection", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Add first metric
		err := encoder.StartMetricName("cpu.usage", 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(time.Now().UnixMicro(), 1.0, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Try to add duplicate name - should fail
		err = encoder.StartMetricName("cpu.usage", 1)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)
	})
}

// TestNumericEncoder_FinishWithDifferentModes tests Finish() behavior in both modes.
func TestNumericEncoder_FinishWithDifferentModes(t *testing.T) {
	t.Run("FinishIDModeNoMetricNames", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// ID mode - add some metrics
		for i := 0; i < 3; i++ {
			err := encoder.StartMetricID(uint64(100+i), 2)
			require.NoError(t, err)
			err = encoder.AddDataPoint(time.Now().UnixMicro(), float64(i), "")
			require.NoError(t, err)
			err = encoder.AddDataPoint(time.Now().UnixMicro(), float64(i+1), "")
			require.NoError(t, err)
			err = encoder.EndMetric()
			require.NoError(t, err)
		}

		// Finish should succeed
		blob, err := encoder.Finish()
		require.NoError(t, err)
		require.NotNil(t, blob)

		// Verify no metric names in header
		require.False(t, encoder.header.Flag.HasMetricNames(), "ID mode should not have metric names")

		// Verify collision tracker was never created
		require.Nil(t, encoder.collisionTracker, "ID mode should not create collision tracker")
	})

	t.Run("FinishNameModeNoCollision", func(t *testing.T) {
		encoder := createTestEncoder(t)

		// Name mode - add some metrics (no collision)
		for i := 0; i < 3; i++ {
			err := encoder.StartMetricName(string(rune('a'+i))+".metric", 2)
			require.NoError(t, err)
			err = encoder.AddDataPoint(time.Now().UnixMicro(), float64(i), "")
			require.NoError(t, err)
			err = encoder.AddDataPoint(time.Now().UnixMicro(), float64(i+1), "")
			require.NoError(t, err)
			err = encoder.EndMetric()
			require.NoError(t, err)
		}

		// Finish should succeed
		blob, err := encoder.Finish()
		require.NoError(t, err)
		require.NotNil(t, blob)

		// Verify collision tracker was created
		require.NotNil(t, encoder.collisionTracker, "Name mode should create collision tracker")

		// No collision, so no metric names payload needed
		require.False(t, encoder.header.Flag.HasMetricNames(), "No collision means no metric names payload")
	})
}

// TestNumericEncoder_TagsDisabled tests encoding/decoding with tags disabled.
func TestNumericEncoder_TagsDisabled(t *testing.T) {
	startTime := time.Now()

	// Create encoder WITHOUT WithTagsEnabled(true) - tags should be disabled by default
	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)

	// Add first metric with tags (should be ignored)
	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)

	err = encoder.AddDataPoint(1000, 1.1, "tag1")
	require.NoError(t, err)
	err = encoder.AddDataPoint(2000, 2.2, "tag2")
	require.NoError(t, err)
	err = encoder.AddDataPoint(3000, 3.3, "tag3")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Add second metric with tags (should be ignored)
	err = encoder.StartMetricID(67890, 2)
	require.NoError(t, err)

	err = encoder.AddDataPoint(4000, 4.4, "tag4")
	require.NoError(t, err)
	err = encoder.AddDataPoint(5000, 5.5, "tag5")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Finish encoding
	data, err := encoder.Finish()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Decode the blob
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify flag indicates tags are disabled
	require.False(t, blob.flag.HasTag())

	// Verify first metric - timestamps and values should work, tags should be empty
	timestamps := make([]int64, 0, 3)
	values := make([]float64, 0, 3)
	tags := make([]string, 0, 3)

	for idx, dp := range blob.All(12345) {
		require.Less(t, idx, 3)
		timestamps = append(timestamps, dp.Ts)
		values = append(values, dp.Val)
		tags = append(tags, dp.Tag)
	}

	require.Equal(t, []int64{1000, 2000, 3000}, timestamps)
	require.Equal(t, []float64{1.1, 2.2, 3.3}, values)
	require.Equal(t, []string{"", "", ""}, tags) // All tags should be empty

	// Verify second metric
	timestamps = timestamps[:0]
	values = values[:0]
	tags = tags[:0]

	for idx, dp := range blob.All(67890) {
		require.Less(t, idx, 2)
		timestamps = append(timestamps, dp.Ts)
		values = append(values, dp.Val)
		tags = append(tags, dp.Tag)
	}

	require.Equal(t, []int64{4000, 5000}, timestamps)
	require.Equal(t, []float64{4.4, 5.5}, values)
	require.Equal(t, []string{"", ""}, tags) // All tags should be empty

	// Verify AllTags returns empty iterator
	tagCount := 0
	for range blob.AllTags(12345) {
		tagCount++
	}
	require.Equal(t, 0, tagCount)

	// Verify TagAt returns false
	tag, ok := blob.TagAt(12345, 0)
	require.False(t, ok)
	require.Equal(t, "", tag)
}

// TestNumericEncoder_TagsEnabled tests encoding/decoding with tags enabled.
func TestNumericEncoder_TagsEnabled(t *testing.T) {
	startTime := time.Now()

	// Create encoder WITH WithTagsEnabled(true) - tags should be enabled
	encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
	require.NoError(t, err)

	// Add first metric with tags
	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)

	err = encoder.AddDataPoint(1000, 1.1, "tag1")
	require.NoError(t, err)
	err = encoder.AddDataPoint(2000, 2.2, "tag2")
	require.NoError(t, err)
	err = encoder.AddDataPoint(3000, 3.3, "tag3")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Add second metric with tags
	err = encoder.StartMetricID(67890, 2)
	require.NoError(t, err)

	err = encoder.AddDataPoint(4000, 4.4, "tag4")
	require.NoError(t, err)
	err = encoder.AddDataPoint(5000, 5.5, "tag5")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Finish encoding
	data, err := encoder.Finish()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Decode the blob
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify flag indicates tags are enabled
	require.True(t, blob.flag.HasTag())

	// Verify first metric - all data should be present
	timestamps := make([]int64, 0, 3)
	values := make([]float64, 0, 3)
	tags := make([]string, 0, 3)

	for idx, dp := range blob.All(12345) {
		require.Less(t, idx, 3)
		timestamps = append(timestamps, dp.Ts)
		values = append(values, dp.Val)
		tags = append(tags, dp.Tag)
	}

	require.Equal(t, []int64{1000, 2000, 3000}, timestamps)
	require.Equal(t, []float64{1.1, 2.2, 3.3}, values)
	require.Equal(t, []string{"tag1", "tag2", "tag3"}, tags)

	// Verify second metric
	timestamps = timestamps[:0]
	values = values[:0]
	tags = tags[:0]

	for idx, dp := range blob.All(67890) {
		require.Less(t, idx, 2)
		timestamps = append(timestamps, dp.Ts)
		values = append(values, dp.Val)
		tags = append(tags, dp.Tag)
	}

	require.Equal(t, []int64{4000, 5000}, timestamps)
	require.Equal(t, []float64{4.4, 5.5}, values)
	require.Equal(t, []string{"tag4", "tag5"}, tags)

	// Verify AllTags works
	allTags := make([]string, 0, 3)
	for tag := range blob.AllTags(12345) {
		allTags = append(allTags, tag)
	}
	require.Equal(t, []string{"tag1", "tag2", "tag3"}, allTags)

	// Verify TagAt works
	tag, ok := blob.TagAt(12345, 0)
	require.True(t, ok)
	require.Equal(t, "tag1", tag)

	tag, ok = blob.TagAt(12345, 2)
	require.True(t, ok)
	require.Equal(t, "tag3", tag)
}

// TestNumericEncoder_TagsDisabled_DeltaEncoding tests tags disabled with delta encoding.
func TestNumericEncoder_TagsDisabled_DeltaEncoding(t *testing.T) {
	startTime := time.Now()

	// Create encoder with delta encoding and tags disabled
	encoder, err := NewNumericEncoder(startTime, WithTimestampEncoding(format.TypeDelta))
	require.NoError(t, err)

	// Add metric with tags (should be ignored)
	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)

	err = encoder.AddDataPoint(1000, 1.1, "tag1")
	require.NoError(t, err)
	err = encoder.AddDataPoint(2000, 2.2, "tag2")
	require.NoError(t, err)
	err = encoder.AddDataPoint(3000, 3.3, "tag3")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Finish encoding
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode and verify
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify tags are disabled
	require.False(t, blob.flag.HasTag())

	// Verify data - use All() which calls allDataPointsDeltaRaw
	timestamps := make([]int64, 0, 3)
	values := make([]float64, 0, 3)
	tags := make([]string, 0, 3)

	for idx, dp := range blob.All(12345) {
		require.Less(t, idx, 3)
		timestamps = append(timestamps, dp.Ts)
		values = append(values, dp.Val)
		tags = append(tags, dp.Tag)
	}

	require.Equal(t, []int64{1000, 2000, 3000}, timestamps)
	require.Equal(t, []float64{1.1, 2.2, 3.3}, values)
	require.Equal(t, []string{"", "", ""}, tags) // All tags should be empty
}

// TestNumericEncoder_TagsEnabled_DeltaEncoding tests tags enabled with delta encoding.
func TestNumericEncoder_TagsEnabled_DeltaEncoding(t *testing.T) {
	startTime := time.Now()

	// Create encoder with delta encoding and tags enabled
	encoder, err := NewNumericEncoder(startTime, WithTimestampEncoding(format.TypeDelta), WithTagsEnabled(true))
	require.NoError(t, err)

	// Add metric with tags
	err = encoder.StartMetricID(12345, 3)
	require.NoError(t, err)

	err = encoder.AddDataPoint(1000, 1.1, "tag1")
	require.NoError(t, err)
	err = encoder.AddDataPoint(2000, 2.2, "tag2")
	require.NoError(t, err)
	err = encoder.AddDataPoint(3000, 3.3, "tag3")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Finish encoding
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode and verify
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify tags are enabled
	require.True(t, blob.flag.HasTag())

	// Verify all data is present - use All() which calls allDataPointsDeltaRaw
	timestamps := make([]int64, 0, 3)
	values := make([]float64, 0, 3)
	tags := make([]string, 0, 3)

	for idx, dp := range blob.All(12345) {
		require.Less(t, idx, 3)
		timestamps = append(timestamps, dp.Ts)
		values = append(values, dp.Val)
		tags = append(tags, dp.Tag)
	}

	require.Equal(t, []int64{1000, 2000, 3000}, timestamps)
	require.Equal(t, []float64{1.1, 2.2, 3.3}, values)
	require.Equal(t, []string{"tag1", "tag2", "tag3"}, tags)
}

// TestNumericEncoder_EmptyTagsOptimization tests the dynamic tag optimization feature.
// When tag support is enabled but all tags are empty, the encoder should automatically
// disable tag support in the final blob to save space and improve decoding performance.
func TestNumericEncoder_EmptyTagsOptimization(t *testing.T) {
	startTime := time.Now()

	t.Run("AllEmptyTags_AddDataPoint", func(t *testing.T) {
		// Create encoder with tags enabled
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		// Add metric with all empty tags using AddDataPoint
		err = encoder.StartMetricID(12345, 3)
		require.NoError(t, err)

		err = encoder.AddDataPoint(1000, 1.1, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(2000, 2.2, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(3000, 3.3, "")
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)

		// Finish encoding
		data, err := encoder.Finish()
		require.NoError(t, err)

		// Decode and verify tag support is DISABLED (optimized away)
		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Tag support should be automatically disabled
		require.False(t, blob.flag.HasTag(), "Expected HasTag() to be false when all tags are empty")

		// Verify data is still correct
		timestamps := make([]int64, 0, 3)
		values := make([]float64, 0, 3)

		for _, dp := range blob.All(12345) {
			timestamps = append(timestamps, dp.Ts)
			values = append(values, dp.Val)
			// Tag field should be empty string (default value)
			require.Equal(t, "", dp.Tag)
		}

		require.Equal(t, []int64{1000, 2000, 3000}, timestamps)
		require.Equal(t, []float64{1.1, 2.2, 3.3}, values)
	})

	t.Run("AllEmptyTags_AddDataPoints", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		err = encoder.StartMetricID(12345, 3)
		require.NoError(t, err)

		err = encoder.AddDataPoints(
			[]int64{1000, 2000, 3000},
			[]float64{1.1, 2.2, 3.3},
			[]string{"", "", ""},
		)
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		require.False(t, blob.flag.HasTag())
	})

	t.Run("AllEmptyTags_NoTagsProvided", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		err = encoder.StartMetricID(12345, 3)
		require.NoError(t, err)

		err = encoder.AddDataPoints(
			[]int64{1000, 2000, 3000},
			[]float64{1.1, 2.2, 3.3},
			nil,
		)
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		require.False(t, blob.flag.HasTag())
	})

	t.Run("MixedTags_SomeNonEmpty", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		err = encoder.StartMetricID(12345, 3)
		require.NoError(t, err)

		err = encoder.AddDataPoint(1000, 1.1, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(2000, 2.2, "tag2")
		require.NoError(t, err)
		err = encoder.AddDataPoint(3000, 3.3, "")
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		require.True(t, blob.flag.HasTag())

		tags := make([]string, 0, 3)
		for _, dp := range blob.All(12345) {
			tags = append(tags, dp.Tag)
		}

		require.Equal(t, []string{"", "tag2", ""}, tags)
	})

	t.Run("AllNonEmptyTags", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		err = encoder.StartMetricID(12345, 3)
		require.NoError(t, err)

		err = encoder.AddDataPoints(
			[]int64{1000, 2000, 3000},
			[]float64{1.1, 2.2, 3.3},
			[]string{"tag1", "tag2", "tag3"},
		)
		require.NoError(t, err)

		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		require.True(t, blob.flag.HasTag())

		tags := make([]string, 0, 3)
		for _, dp := range blob.All(12345) {
			tags = append(tags, dp.Tag)
		}

		require.Equal(t, []string{"tag1", "tag2", "tag3"}, tags)
	})

	t.Run("MultipleMetrics_AllEmptyTags", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		for i := 0; i < 5; i++ {
			metricID := uint64(10000 + i)
			err = encoder.StartMetricID(metricID, 2)
			require.NoError(t, err)

			err = encoder.AddDataPoints(
				[]int64{1000, 2000},
				[]float64{1.1, 2.2},
				[]string{"", ""},
			)
			require.NoError(t, err)

			err = encoder.EndMetric()
			require.NoError(t, err)
		}

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		require.False(t, blob.flag.HasTag())
	})

	t.Run("MultipleMetrics_OneWithNonEmptyTag", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime, WithTagsEnabled(true))
		require.NoError(t, err)

		err = encoder.StartMetricID(10000, 2)
		require.NoError(t, err)
		err = encoder.AddDataPoints(
			[]int64{1000, 2000},
			[]float64{1.1, 2.2},
			[]string{"", ""},
		)
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		err = encoder.StartMetricID(10001, 2)
		require.NoError(t, err)
		err = encoder.AddDataPoints(
			[]int64{1000, 2000},
			[]float64{3.3, 4.4},
			[]string{"", "important"},
		)
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		err = encoder.StartMetricID(10002, 2)
		require.NoError(t, err)
		err = encoder.AddDataPoints(
			[]int64{1000, 2000},
			[]float64{5.5, 6.6},
			[]string{"", ""},
		)
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		require.True(t, blob.flag.HasTag())

		tags := make([]string, 0, 2)
		for _, dp := range blob.All(10001) {
			tags = append(tags, dp.Tag)
		}

		require.Equal(t, []string{"", "important"}, tags)
	})
}
