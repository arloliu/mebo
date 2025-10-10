package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/section"
	"github.com/stretchr/testify/require"
)

func TestNewNumericDecoder(t *testing.T) {
	t.Run("ValidData", func(t *testing.T) {
		// Create valid test data using encoder
		startTime := time.Now()
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Add test data
		err = encoder.StartMetricID(12345, 2)
		require.NoError(t, err)
		err = encoder.AddDataPoint(startTime.UnixMicro(), 1.5, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.5, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		err = encoder.StartMetricID(67890, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(startTime.Add(2*time.Second).UnixMicro(), 3.5, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		// Test decoder creation
		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)
		require.NotNil(t, decoder)
		require.Equal(t, data, decoder.data)
		require.Equal(t, 2, decoder.metricCount)
		require.NotNil(t, decoder.header)
		require.NotNil(t, decoder.engine)
	})

	t.Run("EmptyData", func(t *testing.T) {
		decoder, err := NewNumericDecoder([]byte{})
		require.Error(t, err)
		require.Nil(t, decoder)
		require.ErrorIs(t, err, errs.ErrInvalidHeaderSize)
	})

	t.Run("InvalidHeaderSize", func(t *testing.T) {
		// Data too small for header
		data := make([]byte, section.HeaderSize-1)
		decoder, err := NewNumericDecoder(data)
		require.Error(t, err)
		require.Nil(t, decoder)
		require.ErrorIs(t, err, errs.ErrInvalidHeaderSize)
	})

	t.Run("InvalidHeaderData", func(t *testing.T) {
		// Create data with invalid header content
		data := make([]byte, section.HeaderSize)
		// Leave all bytes as zero (invalid header)

		decoder, err := NewNumericDecoder(data)
		require.Error(t, err)
		require.Nil(t, decoder)
	})

	t.Run("CorruptedData", func(t *testing.T) {
		// Create data with invalid header size (too small for proper parsing)
		data := make([]byte, section.HeaderSize)
		// Initialize with zeros which will fail header validation

		decoder, err := NewNumericDecoder(data)
		require.Error(t, err)
		require.Nil(t, decoder)
	})
}

func TestNumericDecoder_Decode(t *testing.T) {
	startTime := time.Now()
	testMetricID1 := uint64(12345)
	testMetricID2 := uint64(67890)

	// Helper function to create test data
	createTestData := func(tsEncoding, valEncoding format.EncodingType) []byte {
		encoder, err := NewNumericEncoder(startTime,
			WithTimestampEncoding(tsEncoding),
			WithValueEncoding(valEncoding))
		require.NoError(t, err)

		// Metric 1: Multiple data points
		err = encoder.StartMetricID(testMetricID1, 3)
		require.NoError(t, err)
		err = encoder.AddDataPoint(startTime.UnixMicro(), 1.5, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.5, "")
		require.NoError(t, err)
		err = encoder.AddDataPoint(startTime.Add(2*time.Second).UnixMicro(), 3.5, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		// Metric 2: Single data point
		err = encoder.StartMetricID(testMetricID2, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(startTime.Add(3*time.Second).UnixMicro(), 4.5, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		return data
	}

	t.Run("ValidDecode_RawEncoding", func(t *testing.T) {
		data := createTestData(format.TypeRaw, format.TypeRaw)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		// Verify blob structure
		require.Equal(t, format.TypeRaw, blob.tsEncType)
		require.Equal(t, format.TypeRaw, blob.ValueEncoding())
		require.Len(t, blob.index.byID, 2)
		require.NotEmpty(t, blob.tsPayload)
		require.NotEmpty(t, blob.valPayload)
		require.NotNil(t, blob.engine)
	})

	t.Run("ValidDecode_DeltaEncoding", func(t *testing.T) {
		data := createTestData(format.TypeDelta, format.TypeRaw)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		require.Equal(t, format.TypeDelta, blob.tsEncType)
		require.Equal(t, format.TypeRaw, blob.ValueEncoding())
		require.Len(t, blob.index.byID, 2)
	})

	t.Run("EmptyMetricList", func(t *testing.T) {
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		// Finish without metrics should now return error
		data, err := encoder.Finish()
		require.Error(t, err)
		require.Nil(t, data)
	})

	t.Run("InvalidTimestampPayloadOffset", func(t *testing.T) {
		data := createTestData(format.TypeRaw, format.TypeRaw)

		// Corrupt the timestamp payload offset in header
		engine := endian.GetLittleEndianEngine()
		engine.PutUint32(data[20:24], uint32(len(data)+100)) // TimestampPayloadOffset is at offset 20-24, beyond data length

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		_, err = decoder.Decode()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidTimestampPayloadOffset)
	})

	t.Run("InvalidValuePayloadOffset", func(t *testing.T) {
		data := createTestData(format.TypeRaw, format.TypeRaw)

		// Corrupt the value payload offset in header
		engine := endian.GetLittleEndianEngine()
		engine.PutUint32(data[24:28], uint32(len(data)+100)) // ValuePayloadOffset is at offset 24-28, beyond data length

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		_, err = decoder.Decode()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidValuePayloadOffset)
	})

	t.Run("InvalidIndexEntrySize", func(t *testing.T) {
		// Create data with more metrics declared than actual index data
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		err = encoder.StartMetricID(testMetricID1, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(startTime.UnixMicro(), 1.5, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		// Corrupt metric count to be larger than available index data
		engine := endian.GetLittleEndianEngine()
		engine.PutUint32(data[12:16], 100) // MetricCount is at offset 12-16, set unrealistic metric count

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		_, err = decoder.Decode()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidIndexEntrySize)
	})
}

func TestNumericDecoder_parseHeader(t *testing.T) {
	t.Run("ValidHeader", func(t *testing.T) {
		startTime := time.Now()
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		err = encoder.StartMetricID(12345, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(startTime.UnixMicro(), 1.5, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder := &NumericDecoder{data: data}
		err = decoder.parseHeader()
		require.NoError(t, err)
		require.NotNil(t, decoder.header)
		require.NotNil(t, decoder.engine)
		require.Equal(t, 1, decoder.metricCount)
	})

	t.Run("InvalidHeaderSize", func(t *testing.T) {
		decoder := &NumericDecoder{data: make([]byte, 10)} // Too small
		err := decoder.parseHeader()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidHeaderSize)
	})
}

func TestNumericDecoder_parsePayloads(t *testing.T) {
	t.Run("ValidPayloads", func(t *testing.T) {
		startTime := time.Now()
		encoder, err := NewNumericEncoder(startTime)
		require.NoError(t, err)

		err = encoder.StartMetricID(12345, 1)
		require.NoError(t, err)
		err = encoder.AddDataPoint(startTime.UnixMicro(), 1.5, "")
		require.NoError(t, err)
		err = encoder.EndMetric()
		require.NoError(t, err)

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder := &NumericDecoder{data: data}
		err = decoder.parsePayloads()
		require.NoError(t, err)
	})

	t.Run("InvalidHeaderSize", func(t *testing.T) {
		decoder := &NumericDecoder{data: make([]byte, 10)} // Too small
		err := decoder.parsePayloads()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidHeaderSize)
	})
}

// TestNumericDecoderBackwardCompatibility tests that old blobs (without metric names) decode correctly
func TestNumericDecoderBackwardCompatibility(t *testing.T) {
	// Create encoder without metric names
	encoder, err := NewNumericEncoder(time.Now())
	require.NoError(t, err)

	require.False(t, encoder.header.Flag.HasMetricNames())

	ts := time.Now().UnixMicro()

	// Add metrics
	err = encoder.StartMetricName("metric.one", 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(ts, 1.0, "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(ts+1000, 2.0, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	err = encoder.StartMetricName("metric.two", 2)
	require.NoError(t, err)
	err = encoder.AddDataPoint(ts, 10.0, "")
	require.NoError(t, err)
	err = encoder.AddDataPoint(ts+1000, 20.0, "")
	require.NoError(t, err)
	err = encoder.EndMetric()
	require.NoError(t, err)

	// Encode
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode and verify
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, 2, len(blob.index.byID))
	require.False(t, blob.HasMetricNames())
}

// TestNumericDecoder_PayloadLengths verifies that payload lengths are correctly
// calculated for all entries, including the last entry.
func TestNumericDecoder_PayloadLengths(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create encoder with 3 metrics of different sizes
	encoder, err := NewNumericEncoder(
		startTime,
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeRaw),
	)
	require.NoError(t, err)

	// Metric 1: 5 points → 40 bytes timestamps, 40 bytes values
	require.NoError(t, encoder.StartMetricID(1001, 5))
	for i := 0; i < 5; i++ {
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro()+int64(i)*1000000, float64(i), ""))
	}
	require.NoError(t, encoder.EndMetric())

	// Metric 2: 3 points → 24 bytes timestamps, 24 bytes values
	require.NoError(t, encoder.StartMetricID(1002, 3))
	for i := 0; i < 3; i++ {
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro()+int64(i)*1000000, float64(i+10), ""))
	}
	require.NoError(t, encoder.EndMetric())

	// Metric 3: 7 points → 56 bytes timestamps, 56 bytes values
	require.NoError(t, encoder.StartMetricID(1003, 7))
	for i := 0; i < 7; i++ {
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro()+int64(i)*1000000, float64(i+20), ""))
	}
	require.NoError(t, encoder.EndMetric())

	// Encode
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify payload lengths for all entries
	entry1, ok := blob.index.byID[1001]
	require.True(t, ok, "Entry 1 should exist")
	require.Equal(t, 5*8, entry1.TimestampLength, "Entry 1 timestamp length should be 40 bytes (5 timestamps × 8)")
	require.Equal(t, 5*8, entry1.ValueLength, "Entry 1 value length should be 40 bytes (5 values × 8)")
	require.Equal(t, 0, entry1.TimestampOffset, "Entry 1 should start at offset 0")
	require.Equal(t, 0, entry1.ValueOffset, "Entry 1 should start at offset 0")

	entry2, ok := blob.index.byID[1002]
	require.True(t, ok, "Entry 2 should exist")
	require.Equal(t, 3*8, entry2.TimestampLength, "Entry 2 timestamp length should be 24 bytes (3 timestamps × 8)")
	require.Equal(t, 3*8, entry2.ValueLength, "Entry 2 value length should be 24 bytes (3 values × 8)")
	require.Equal(t, 40, entry2.TimestampOffset, "Entry 2 should start at offset 40")
	require.Equal(t, 40, entry2.ValueOffset, "Entry 2 should start at offset 40")

	entry3, ok := blob.index.byID[1003]
	require.True(t, ok, "Entry 3 should exist")
	require.Equal(t, 7*8, entry3.TimestampLength, "Entry 3 timestamp length should be 56 bytes (7 timestamps × 8)")
	require.Equal(t, 7*8, entry3.ValueLength, "Entry 3 value length should be 56 bytes (7 values × 8)")
	require.Equal(t, 64, entry3.TimestampOffset, "Entry 3 should start at offset 64 (40+24)")
	require.Equal(t, 64, entry3.ValueOffset, "Entry 3 should start at offset 64 (40+24)")

	// Verify the last entry's length is correct (this was the bug!)
	t.Logf("Last entry (metric 3) lengths: Timestamp=%d, Value=%d", entry3.TimestampLength, entry3.ValueLength)
	require.Greater(t, entry3.TimestampLength, 0, "Last entry timestamp length must be > 0")
	require.Greater(t, entry3.ValueLength, 0, "Last entry value length must be > 0")
}

// TestNumericDecoder_PayloadLengths_Gorilla tests with variable-length Gorilla encoding
func TestNumericDecoder_PayloadLengths_Gorilla(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create encoder with Gorilla encoding (variable length)
	encoder, err := NewNumericEncoder(
		startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
	)
	require.NoError(t, err)

	// Create 3 metrics with different patterns
	for metricID := uint64(2001); metricID <= 2003; metricID++ {
		require.NoError(t, encoder.StartMetricID(metricID, 10))
		for i := 0; i < 10; i++ {
			ts := startTime.UnixMicro() + int64(i)*1000000
			val := float64(metricID)*100 + float64(i)*0.1 // Different patterns per metric
			require.NoError(t, encoder.AddDataPoint(ts, val, ""))
		}
		require.NoError(t, encoder.EndMetric())
	}

	// Encode
	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify all entries have positive lengths
	for metricID := uint64(2001); metricID <= 2003; metricID++ {
		entry, ok := blob.index.byID[metricID]
		require.True(t, ok, "Metric %d should exist", metricID)

		require.Greater(t, entry.TimestampLength, 0, "Metric %d: TimestampLength must be > 0", metricID)
		require.Greater(t, entry.ValueLength, 0, "Metric %d: ValueLength must be > 0", metricID)

		t.Logf("Metric %d: Offset=(ts:%d, val:%d), Length=(ts:%d, val:%d)",
			metricID, entry.TimestampOffset, entry.ValueOffset,
			entry.TimestampLength, entry.ValueLength)
	}

	// Verify we can actually read the data using the lengths
	for metricID := uint64(2001); metricID <= 2003; metricID++ {
		entry := blob.index.byID[metricID]

		// Verify we can slice the payload using offset + length
		tsEnd := entry.TimestampOffset + entry.TimestampLength
		valEnd := entry.ValueOffset + entry.ValueLength

		require.LessOrEqual(t, tsEnd, len(blob.tsPayload), "Timestamp slice should not exceed payload")
		require.LessOrEqual(t, valEnd, len(blob.valPayload), "Value slice should not exceed payload")

		tsData := blob.tsPayload[entry.TimestampOffset:tsEnd]
		valData := blob.valPayload[entry.ValueOffset:valEnd]

		require.NotEmpty(t, tsData, "Timestamp data should not be empty for metric %d", metricID)
		require.NotEmpty(t, valData, "Value data should not be empty for metric %d", metricID)
	}
}
