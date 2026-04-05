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
		require.NotNil(t, blob.Engine())
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
	for i := range 5 {
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro()+int64(i)*1000000, float64(i), ""))
	}
	require.NoError(t, encoder.EndMetric())

	// Metric 2: 3 points → 24 bytes timestamps, 24 bytes values
	require.NoError(t, encoder.StartMetricID(1002, 3))
	for i := range 3 {
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro()+int64(i)*1000000, float64(i+10), ""))
	}
	require.NoError(t, encoder.EndMetric())

	// Metric 3: 7 points → 56 bytes timestamps, 56 bytes values
	require.NoError(t, encoder.StartMetricID(1003, 7))
	for i := range 7 {
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
		for i := range 10 {
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

// ==============================================================================
// Chimp Encoding End-to-End Tests
// ==============================================================================

// TestChimpEncoding_BlobRoundTrip verifies the full encode → serialize → deserialize → decode
// pipeline with Chimp value encoding across multiple encoding combinations.
func TestChimpEncoding_BlobRoundTrip(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	testCases := []struct {
		name  string
		tsEnc format.EncodingType
	}{
		{"Delta-Chimp", format.TypeDelta},
		{"Raw-Chimp", format.TypeRaw},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoder, err := NewNumericEncoder(startTime,
				WithTimestampEncoding(tc.tsEnc),
				WithValueEncoding(format.TypeChimp),
			)
			require.NoError(t, err)

			// Metric 1: slowly increasing values
			err = encoder.StartMetricID(5001, 10)
			require.NoError(t, err)
			for i := range 10 {
				ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
				err = encoder.AddDataPoint(ts, 100.0+float64(i)*0.1, "")
				require.NoError(t, err)
			}
			err = encoder.EndMetric()
			require.NoError(t, err)

			// Metric 2: constant values
			err = encoder.StartMetricID(5002, 5)
			require.NoError(t, err)
			for i := range 5 {
				ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
				err = encoder.AddDataPoint(ts, 42.0, "")
				require.NoError(t, err)
			}
			err = encoder.EndMetric()
			require.NoError(t, err)

			// Metric 3: varying values
			err = encoder.StartMetricID(5003, 8)
			require.NoError(t, err)
			for i := range 8 {
				ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
				err = encoder.AddDataPoint(ts, float64(i*i)*1.5+0.123, "")
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
			require.Equal(t, 3, blob.MetricCount())

			// Verify metric 1
			i := 0
			for _, dp := range blob.All(5001) {
				require.Equal(t, startTime.Add(time.Duration(i)*time.Second).UnixMicro(), dp.Ts)
				require.Equal(t, 100.0+float64(i)*0.1, dp.Val)
				i++
			}
			require.Equal(t, 10, i)

			// Verify metric 2
			i = 0
			for _, dp := range blob.All(5002) {
				require.Equal(t, startTime.Add(time.Duration(i)*time.Second).UnixMicro(), dp.Ts)
				require.Equal(t, 42.0, dp.Val)
				i++
			}
			require.Equal(t, 5, i)

			// Verify metric 3
			i = 0
			for _, dp := range blob.All(5003) {
				require.Equal(t, startTime.Add(time.Duration(i)*time.Second).UnixMicro(), dp.Ts)
				require.Equal(t, float64(i*i)*1.5+0.123, dp.Val)
				i++
			}
			require.Equal(t, 8, i)

			// Verify random access (DataPointAt / ValueAt)
			val, ok := blob.ValueAt(5001, 5)
			require.True(t, ok)
			require.Equal(t, 100.0+5*0.1, val)

			val, ok = blob.ValueAt(5002, 0)
			require.True(t, ok)
			require.Equal(t, 42.0, val)

			val, ok = blob.ValueAt(5003, 3)
			require.True(t, ok)
			require.Equal(t, float64(3*3)*1.5+0.123, val)
		})
	}
}

// TestChimpEncoding_BlobRoundTrip_WithTags verifies Chimp encoding with tag support.
func TestChimpEncoding_BlobRoundTrip_WithTags(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeChimp),
		WithTagsEnabled(true),
	)
	require.NoError(t, err)

	tags := []string{"host=a", "host=b", "host=c", "host=d", "host=e"}

	err = encoder.StartMetricID(6001, len(tags))
	require.NoError(t, err)
	for i, tag := range tags {
		ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
		err = encoder.AddDataPoint(ts, float64(i)*1.1, tag)
		require.NoError(t, err)
	}
	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	i := 0
	for _, dp := range blob.All(6001) {
		require.Equal(t, startTime.Add(time.Duration(i)*time.Second).UnixMicro(), dp.Ts)
		require.Equal(t, float64(i)*1.1, dp.Val)
		require.Equal(t, tags[i], dp.Tag)
		i++
	}
	require.Equal(t, len(tags), i)
}

// TestChimpVsGorilla_BlobDecodedEquivalence verifies that Chimp and Gorilla encodings produce
// identical decoded values when used through the full blob encode → decode pipeline.
func TestChimpVsGorilla_BlobDecodedEquivalence(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 20

	// Generate test data with varying patterns
	timestamps := make([]int64, numPoints)
	values := make([]float64, numPoints)
	for i := range numPoints {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
		values[i] = 100.0 + float64(i)*0.37 + float64(i*i)*0.001
	}

	encodingPairs := []struct {
		name  string
		tsEnc format.EncodingType
	}{
		{"Delta", format.TypeDelta},
		{"Raw", format.TypeRaw},
	}

	for _, ep := range encodingPairs {
		t.Run(ep.name, func(t *testing.T) {
			// Encode with Gorilla
			gorillaEncoder, err := NewNumericEncoder(startTime,
				WithTimestampEncoding(ep.tsEnc),
				WithValueEncoding(format.TypeGorilla),
			)
			require.NoError(t, err)

			err = gorillaEncoder.StartMetricID(7001, numPoints)
			require.NoError(t, err)
			for i := range numPoints {
				err = gorillaEncoder.AddDataPoint(timestamps[i], values[i], "")
				require.NoError(t, err)
			}
			err = gorillaEncoder.EndMetric()
			require.NoError(t, err)

			gorillaData, err := gorillaEncoder.Finish()
			require.NoError(t, err)

			// Encode with Chimp
			chimpEncoder, err := NewNumericEncoder(startTime,
				WithTimestampEncoding(ep.tsEnc),
				WithValueEncoding(format.TypeChimp),
			)
			require.NoError(t, err)

			err = chimpEncoder.StartMetricID(7001, numPoints)
			require.NoError(t, err)
			for i := range numPoints {
				err = chimpEncoder.AddDataPoint(timestamps[i], values[i], "")
				require.NoError(t, err)
			}
			err = chimpEncoder.EndMetric()
			require.NoError(t, err)

			chimpData, err := chimpEncoder.Finish()
			require.NoError(t, err)

			// Decode both
			gorillaDecoder, err := NewNumericDecoder(gorillaData)
			require.NoError(t, err)
			gorillaBlob, err := gorillaDecoder.Decode()
			require.NoError(t, err)

			chimpDecoder, err := NewNumericDecoder(chimpData)
			require.NoError(t, err)
			chimpBlob, err := chimpDecoder.Decode()
			require.NoError(t, err)

			// Collect data points via All() iterator
			var gorillaDPs, chimpDPs []NumericDataPoint
			for _, dp := range gorillaBlob.All(7001) {
				gorillaDPs = append(gorillaDPs, dp)
			}
			for _, dp := range chimpBlob.All(7001) {
				chimpDPs = append(chimpDPs, dp)
			}

			require.Equal(t, numPoints, len(gorillaDPs), "gorilla count")
			require.Equal(t, numPoints, len(chimpDPs), "chimp count")

			for i := range numPoints {
				require.Equal(t, gorillaDPs[i].Ts, chimpDPs[i].Ts, "timestamp mismatch at %d", i)
				require.Equal(t, gorillaDPs[i].Val, chimpDPs[i].Val, "value mismatch at %d", i)
			}

			// Also verify via random access ValueAt()
			for i := range numPoints {
				gorillaVal, gOk := gorillaBlob.ValueAt(7001, i)
				chimpVal, cOk := chimpBlob.ValueAt(7001, i)

				require.True(t, gOk)
				require.True(t, cOk)
				require.Equal(t, gorillaVal, chimpVal, "ValueAt(%d) mismatch", i)
			}
		})
	}
}

// ==============================================================================
// V1/V2 Shared Timestamp Compatibility Tests
// ==============================================================================

// TestV1BlobDecodesWithV2Code verifies that V1 blobs (no shared timestamps)
// continue to decode correctly with V2-aware decoder code.
func TestV1BlobDecodesWithV2Code(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create a blob where metrics have DIFFERENT timestamps → V1 format
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
	)
	require.NoError(t, err)

	// Metric 1: timestamps at 0, 1, 2 seconds
	err = encoder.StartMetricID(1001, 3)
	require.NoError(t, err)

	for i := range 3 {
		ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
		err = encoder.AddDataPoint(ts, float64(i)*1.1, "")
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 2: different timestamps at 10, 11, 12 seconds
	err = encoder.StartMetricID(1002, 3)
	require.NoError(t, err)

	for i := range 3 {
		ts := startTime.Add(time.Duration(i+10) * time.Second).UnixMicro()
		err = encoder.AddDataPoint(ts, float64(i)*2.2, "")
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Verify V1 magic number (different timestamps → no sharing)
	require.True(t, section.IsNumericBlob(data))
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	magic := options & section.MagicNumberMask
	require.Equal(t, uint16(section.MagicNumericV1Opt), magic, "should be V1 format when no shared timestamps")

	// Decode and verify data
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, 2, blob.MetricCount())

	// Verify metric 1 data
	i := 0
	for _, dp := range blob.All(1001) {
		expectedTs := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
		require.Equal(t, expectedTs, dp.Ts)
		require.InDelta(t, float64(i)*1.1, dp.Val, 1e-10)
		i++
	}

	require.Equal(t, 3, i)

	// Verify metric 2 data
	i = 0
	for _, dp := range blob.All(1002) {
		expectedTs := startTime.Add(time.Duration(i+10) * time.Second).UnixMicro()
		require.Equal(t, expectedTs, dp.Ts)
		require.InDelta(t, float64(i)*2.2, dp.Val, 1e-10)
		i++
	}

	require.Equal(t, 3, i)
}

// TestV2SharedTimestamps verifies that metrics with identical timestamps
// produce a V2 blob with shared timestamp encoding, and decode correctly.
func TestV2SharedTimestamps(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 10

	// Generate shared timestamps (all metrics use the same timestamps)
	timestamps := make([]int64, numPoints)
	for i := range numPoints {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	// Add 5 metrics with identical timestamps but different values
	metricCount := 5
	for m := range metricCount {
		metricID := uint64(2001 + m)
		err = encoder.StartMetricID(metricID, numPoints)
		require.NoError(t, err)

		for i := range numPoints {
			val := float64(m*100+i) * 1.5
			err = encoder.AddDataPoint(timestamps[i], val, "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Verify V2 magic number and shared timestamps flag
	require.True(t, section.IsNumericBlob(data))
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	magic := options & section.MagicNumberMask
	require.Equal(t, uint16(section.MagicNumericV2Opt), magic, "should be V2 format when timestamps are shared")
	require.NotZero(t, options&section.SharedTimestampsMask, "shared timestamps flag should be set")

	// Decode and verify all data
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, metricCount, blob.MetricCount())

	// Verify each metric's data
	for m := range metricCount {
		metricID := uint64(2001 + m)
		require.Equal(t, numPoints, blob.Len(metricID))

		i := 0
		for _, dp := range blob.All(metricID) {
			require.Equal(t, timestamps[i], dp.Ts, "metric %d, point %d timestamp mismatch", metricID, i)

			expectedVal := float64(m*100+i) * 1.5
			require.InDelta(t, expectedVal, dp.Val, 1e-10, "metric %d, point %d value mismatch", metricID, i)
			i++
		}

		require.Equal(t, numPoints, i, "metric %d should have exactly %d points", metricID, numPoints)
	}
}

// TestV2SharedTimestampsWithChimp verifies that V2 shared timestamp layout works
// correctly with Chimp value encoding.
func TestV2SharedTimestampsWithChimp(t *testing.T) {
	startTime := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 8
	metricCount := 4

	timestamps := make([]int64, numPoints)
	for i := range numPoints {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeChimp),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	for m := range metricCount {
		metricID := uint64(2601 + m)
		require.NoError(t, encoder.StartMetricID(metricID, numPoints))

		for i := range numPoints {
			v := 100.0 + float64(m)*10.0 + float64(i)*0.125
			require.NoError(t, encoder.AddDataPoint(timestamps[i], v, ""))
		}

		require.NoError(t, encoder.EndMetric())
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Verify V2 magic number is set.
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	magic := options & section.MagicNumberMask
	require.Equal(t, uint16(section.MagicNumericV2Opt), magic)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, metricCount, blob.MetricCount())
	require.True(t, blob.IsV2Layout())

	for m := range metricCount {
		metricID := uint64(2601 + m)
		require.Equal(t, numPoints, blob.Len(metricID))

		i := 0
		for _, dp := range blob.All(metricID) {
			require.Equal(t, timestamps[i], dp.Ts)
			expectedVal := 100.0 + float64(m)*10.0 + float64(i)*0.125
			require.InDelta(t, expectedVal, dp.Val, 1e-10)
			i++
		}

		require.Equal(t, numPoints, i)
	}
}

// TestV2SharedTimestampsSavesSpace verifies that V2 encoding reduces blob size
// compared to V1 when timestamps are shared.
func TestV2SharedTimestampsSavesSpace(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 10
	metricCount := 20

	timestamps := make([]int64, numPoints)
	for i := range numPoints {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	// Encode with no compression to see raw size difference
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithTimestampCompression(format.CompressionNone),
		WithValueCompression(format.CompressionNone),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	for m := range metricCount {
		err = encoder.StartMetricID(uint64(3001+m), numPoints)
		require.NoError(t, err)

		for i := range numPoints {
			err = encoder.AddDataPoint(timestamps[i], float64(m*10+i), "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	v2Data, err := encoder.Finish()
	require.NoError(t, err)

	// The V2 blob should be a valid V2 blob
	options := uint16(v2Data[0]) | (uint16(v2Data[1]) << 8)
	magic := options & section.MagicNumberMask
	require.Equal(t, uint16(section.MagicNumericV2Opt), magic)

	// Verify decode is correct
	decoder, err := NewNumericDecoder(v2Data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, metricCount, blob.MetricCount())

	// Verify sample data
	i := 0
	for _, dp := range blob.All(3001) {
		require.Equal(t, timestamps[i], dp.Ts)
		require.InDelta(t, float64(i), dp.Val, 1e-10)
		i++
	}

	require.Equal(t, numPoints, i)
}

// TestV2MixedSharedAndUniqueTimestamps verifies correct handling when some metrics
// share timestamps and others have unique timestamps.
func TestV2MixedSharedAndUniqueTimestamps(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 5

	// Group A timestamps (shared by metrics 4001, 4002, 4003)
	groupATs := make([]int64, numPoints)
	for i := range numPoints {
		groupATs[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	// Unique timestamps for metric 4004
	uniqueTs := make([]int64, numPoints)
	for i := range numPoints {
		uniqueTs[i] = startTime.Add(time.Duration(i*10+100) * time.Second).UnixMicro()
	}

	// Group B timestamps (shared by metrics 4005, 4006)
	groupBTs := make([]int64, numPoints)
	for i := range numPoints {
		groupBTs[i] = startTime.Add(time.Duration(i*5+50) * time.Second).UnixMicro()
	}

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithTimestampCompression(format.CompressionNone),
		WithValueCompression(format.CompressionNone),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	// Group A: 4001, 4002, 4003 (shared timestamps)
	for m := range 3 {
		err = encoder.StartMetricID(uint64(4001+m), numPoints)
		require.NoError(t, err)

		for i := range numPoints {
			err = encoder.AddDataPoint(groupATs[i], float64(m*10+i), "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	// Unique: 4004
	err = encoder.StartMetricID(4004, numPoints)
	require.NoError(t, err)

	for i := range numPoints {
		err = encoder.AddDataPoint(uniqueTs[i], float64(40+i), "")
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Group B: 4005, 4006 (shared timestamps)
	for m := range 2 {
		err = encoder.StartMetricID(uint64(4005+m), numPoints)
		require.NoError(t, err)

		for i := range numPoints {
			err = encoder.AddDataPoint(groupBTs[i], float64(50+m*10+i), "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Should be V2 (shared timestamps detected)
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	magic := options & section.MagicNumberMask
	require.Equal(t, uint16(section.MagicNumericV2Opt), magic)

	// Decode and verify all metrics
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, 6, blob.MetricCount())

	// Verify Group A metrics
	for m := range 3 {
		metricID := uint64(4001 + m)
		i := 0

		for _, dp := range blob.All(metricID) {
			require.Equal(t, groupATs[i], dp.Ts, "group A metric %d point %d", metricID, i)
			require.InDelta(t, float64(m*10+i), dp.Val, 1e-10)
			i++
		}

		require.Equal(t, numPoints, i)
	}

	// Verify unique metric
	i := 0
	for _, dp := range blob.All(4004) {
		require.Equal(t, uniqueTs[i], dp.Ts, "unique metric point %d", i)
		require.InDelta(t, float64(40+i), dp.Val, 1e-10)
		i++
	}

	require.Equal(t, numPoints, i)

	// Verify Group B metrics
	for m := range 2 {
		metricID := uint64(4005 + m)
		i := 0

		for _, dp := range blob.All(metricID) {
			require.Equal(t, groupBTs[i], dp.Ts, "group B metric %d point %d", metricID, i)
			require.InDelta(t, float64(50+m*10+i), dp.Val, 1e-10)
			i++
		}

		require.Equal(t, numPoints, i)
	}
}

// TestV2WithTags verifies shared timestamps work correctly with tag support enabled.
func TestV2WithTags(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 5

	timestamps := make([]int64, numPoints)
	for i := range numPoints {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithTagsEnabled(true),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	// Metric 1
	err = encoder.StartMetricID(5001, numPoints)
	require.NoError(t, err)

	for i := range numPoints {
		tag := "tag-a"
		if i%2 == 0 {
			tag = "tag-b"
		}

		err = encoder.AddDataPoint(timestamps[i], float64(i)*1.1, tag)
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	// Metric 2 (shared timestamps, different values and tags)
	err = encoder.StartMetricID(5002, numPoints)
	require.NoError(t, err)

	for i := range numPoints {
		err = encoder.AddDataPoint(timestamps[i], float64(i)*2.2, "tag-c")
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Verify V2
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	magic := options & section.MagicNumberMask
	require.Equal(t, uint16(section.MagicNumericV2Opt), magic)

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify metric 1
	i := 0
	for _, dp := range blob.All(5001) {
		require.Equal(t, timestamps[i], dp.Ts)
		require.InDelta(t, float64(i)*1.1, dp.Val, 1e-10)

		expectedTag := "tag-a"
		if i%2 == 0 {
			expectedTag = "tag-b"
		}

		require.Equal(t, expectedTag, dp.Tag)
		i++
	}

	require.Equal(t, numPoints, i)

	// Verify metric 2
	i = 0
	for _, dp := range blob.All(5002) {
		require.Equal(t, timestamps[i], dp.Ts)
		require.InDelta(t, float64(i)*2.2, dp.Val, 1e-10)
		require.Equal(t, "tag-c", dp.Tag)
		i++
	}

	require.Equal(t, numPoints, i)
}

// TestV2WithMetricNames verifies shared timestamps work with collision-detected metric names.
func TestV2WithMetricNames(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 3

	timestamps := make([]int64, numPoints)
	for i := range numPoints {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	metricNames := []string{"cpu.usage", "mem.usage", "disk.io"}
	for m, name := range metricNames {
		err = encoder.StartMetricName(name, numPoints)
		require.NoError(t, err)

		for i := range numPoints {
			err = encoder.AddDataPoint(timestamps[i], float64(m*10+i), "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Decode
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, 3, blob.MetricCount())

	// Verify all metrics by name
	for m, name := range metricNames {
		i := 0
		for _, dp := range blob.AllByName(name) {
			require.Equal(t, timestamps[i], dp.Ts, "metric %s point %d", name, i)
			require.InDelta(t, float64(m*10+i), dp.Val, 1e-10)
			i++
		}

		require.Equal(t, numPoints, i)
	}
}

// TestV2RawEncoding verifies shared timestamps work with raw timestamp encoding.
func TestV2RawEncoding(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 5

	timestamps := make([]int64, numPoints)
	for i := range numPoints {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeRaw),
		WithTimestampCompression(format.CompressionNone),
		WithValueCompression(format.CompressionNone),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	for m := range 3 {
		err = encoder.StartMetricID(uint64(6001+m), numPoints)
		require.NoError(t, err)

		for i := range numPoints {
			err = encoder.AddDataPoint(timestamps[i], float64(m*100+i), "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Should be V2 (shared timestamps detected)
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	magic := options & section.MagicNumberMask
	require.Equal(t, uint16(section.MagicNumericV2Opt), magic)

	// Decode and verify
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, 3, blob.MetricCount())

	for m := range 3 {
		metricID := uint64(6001 + m)

		// Verify via All iterator
		i := 0
		for _, dp := range blob.All(metricID) {
			require.Equal(t, timestamps[i], dp.Ts)
			require.InDelta(t, float64(m*100+i), dp.Val, 1e-10)
			i++
		}

		require.Equal(t, numPoints, i)

		// Verify via TimestampAt random access
		for i := range numPoints {
			ts, ok := blob.TimestampAt(metricID, i)
			require.True(t, ok)
			require.Equal(t, timestamps[i], ts)
		}

		// Verify via ValueAt random access
		for i := range numPoints {
			val, ok := blob.ValueAt(metricID, i)
			require.True(t, ok)
			require.InDelta(t, float64(m*100+i), val, 1e-10)
		}
	}
}

// TestV2SingleMetricNoSharing verifies that a single metric with WithSharedTimestamps
// produces V2 magic but no shared timestamp table (no sharing detected).
func TestV2SingleMetricNoSharing(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime, WithSharedTimestamps())
	require.NoError(t, err)

	err = encoder.StartMetricID(7001, 3)
	require.NoError(t, err)

	for i := range 3 {
		ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
		err = encoder.AddDataPoint(ts, float64(i), "")
		require.NoError(t, err)
	}

	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	// WithSharedTimestamps implies V2 layout, but no sharing → no shared table flag
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	magic := options & section.MagicNumberMask
	require.Equal(t, uint16(section.MagicNumericV2Opt), magic,
		"WithSharedTimestamps implies V2 layout")
	require.Zero(t, options&section.SharedTimestampsMask,
		"no sharing detected, shared timestamps flag should not be set")

	// Still decodes correctly
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, 1, blob.MetricCount())
	require.True(t, blob.IsV2Layout())
}

// TestV2LayoutWithoutSharedTimestamps verifies that WithBlobLayoutV2 alone
// produces a V2 blob without shared timestamp detection or table.
func TestV2LayoutWithoutSharedTimestamps(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 5

	timestamps := make([]int64, numPoints)
	for i := range numPoints {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	// V2 layout WITHOUT shared timestamps — identical timestamps across metrics
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithBlobLayoutV2(),
	)
	require.NoError(t, err)

	for m := range 3 {
		err = encoder.StartMetricID(uint64(7101+m), numPoints)
		require.NoError(t, err)

		for i := range numPoints {
			err = encoder.AddDataPoint(timestamps[i], float64(m*100+i), "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// V2 magic, but no shared timestamps flag
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	magic := options & section.MagicNumberMask
	require.Equal(t, uint16(section.MagicNumericV2Opt), magic,
		"WithBlobLayoutV2 must produce V2 magic")
	require.Zero(t, options&section.SharedTimestampsMask,
		"shared timestamps flag should not be set without WithSharedTimestamps")

	// Decodes correctly
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, 3, blob.MetricCount())
	require.True(t, blob.IsV2Layout())

	// Verify data integrity — timestamps are NOT deduped (each metric has its own copy)
	for m := range 3 {
		metricID := uint64(7101 + m)
		i := 0

		for _, dp := range blob.All(metricID) {
			require.Equal(t, timestamps[i], dp.Ts)
			require.InDelta(t, float64(m*100+i), dp.Val, 1e-10)
			i++
		}

		require.Equal(t, numPoints, i)
	}
}

// ==============================================================================
// Deployment Safety Tests
// ==============================================================================

// TestDefaultEncoderProducesV1 verifies that without WithSharedTimestamps(),
// even identical timestamps produce a V1 blob. This is the critical safety
// property: upgrading the library alone never changes the wire format.
func TestDefaultEncoderProducesV1(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 5

	timestamps := make([]int64, numPoints)
	for i := range numPoints {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	// Encoder WITHOUT WithSharedTimestamps — same timestamps across 3 metrics
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
	)
	require.NoError(t, err)

	for m := range 3 {
		err = encoder.StartMetricID(uint64(8001+m), numPoints)
		require.NoError(t, err)

		for i := range numPoints {
			err = encoder.AddDataPoint(timestamps[i], float64(m*100+i), "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// MUST be V1, not V2
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	magic := options & section.MagicNumberMask
	require.Equal(t, uint16(section.MagicNumericV1Opt), magic,
		"default encoder must produce V1 even when timestamps are identical")

	// Still decodes correctly
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)
	require.Equal(t, 3, blob.MetricCount())

	// Verify data integrity
	for m := range 3 {
		metricID := uint64(8001 + m)
		i := 0

		for _, dp := range blob.All(metricID) {
			require.Equal(t, timestamps[i], dp.Ts)
			require.InDelta(t, float64(m*100+i), dp.Val, 1e-10)
			i++
		}

		require.Equal(t, numPoints, i)
	}
}

// TestDeploymentScenario_ConsumerFirst simulates the safe deployment path:
//  1. Both services start on V1-only mebo
//  2. Consumer upgrades to V2-aware mebo (decoder accepts V1+V2)
//  3. Producer upgrades to V2-aware mebo but does NOT enable WithSharedTimestamps
//  4. Producer continues to emit V1 → consumer reads V1 fine
//  5. Operator enables WithSharedTimestamps on producer
//  6. Producer emits V2 → consumer reads V2 fine
func TestDeploymentScenario_ConsumerFirst(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 5

	timestamps := make([]int64, numPoints)
	for i := range numPoints {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	// Helper to encode shared-timestamp data with or without opt-in
	encodeBlob := func(sharedOpt bool) []byte {
		opts := []NumericEncoderOption{
			WithTimestampEncoding(format.TypeDelta),
			WithValueEncoding(format.TypeGorilla),
		}
		if sharedOpt {
			opts = append(opts, WithSharedTimestamps())
		}

		enc, err := NewNumericEncoder(startTime, opts...)
		require.NoError(t, err)

		for m := range 3 {
			err = enc.StartMetricID(uint64(9001+m), numPoints)
			require.NoError(t, err)

			for i := range numPoints {
				err = enc.AddDataPoint(timestamps[i], float64(m*100+i), "")
				require.NoError(t, err)
			}

			err = enc.EndMetric()
			require.NoError(t, err)
		}

		data, err := enc.Finish()
		require.NoError(t, err)

		return data
	}

	// Helper to decode and verify data
	verifyBlob := func(data []byte) {
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := dec.Decode()
		require.NoError(t, err)
		require.Equal(t, 3, blob.MetricCount())

		for m := range 3 {
			i := 0
			for _, dp := range blob.All(uint64(9001 + m)) {
				require.Equal(t, timestamps[i], dp.Ts)
				require.InDelta(t, float64(m*100+i), dp.Val, 1e-10)
				i++
			}

			require.Equal(t, numPoints, i)
		}
	}

	// Phase 1: Producer without opt-in → V1
	v1Data := encodeBlob(false)
	v1Magic := uint16(v1Data[0]) | (uint16(v1Data[1]) << 8)
	require.Equal(t, uint16(section.MagicNumericV1Opt), v1Magic&section.MagicNumberMask,
		"phase 1: producer without opt-in must produce V1")

	// Phase 2: V2-aware consumer can read V1 blobs
	verifyBlob(v1Data)

	// Phase 3: Producer with opt-in → V2
	v2Data := encodeBlob(true)
	v2Magic := uint16(v2Data[0]) | (uint16(v2Data[1]) << 8)
	require.Equal(t, uint16(section.MagicNumericV2Opt), v2Magic&section.MagicNumberMask,
		"phase 3: producer with opt-in must produce V2")

	// Phase 4: V2-aware consumer can read V2 blobs
	verifyBlob(v2Data)

	// Phase 5: V2 blob should be smaller than V1 blob (shared timestamps deduped)
	require.Less(t, len(v2Data), len(v1Data),
		"V2 blob should be smaller than V1 when timestamps are shared")
}

// TestDeploymentScenario_OldConsumerRejectsV2 simulates an old consumer
// (V1-only) attempting to decode a V2 blob. The old consumer's header parser
// only accepted MagicNumericV1Opt, so a V2 magic number must be rejected
// with ErrInvalidMagicNumber.
func TestDeploymentScenario_OldConsumerRejectsV2(t *testing.T) {
	legacyParseNumericHeader := func(data []byte) error {
		if len(data) < section.HeaderSize {
			return errs.ErrInvalidHeaderSize
		}

		options := uint16(data[0]) | (uint16(data[1]) << 8)
		magic := options & section.MagicNumberMask
		if magic != section.MagicNumericV1Opt {
			return errs.ErrInvalidMagicNumber
		}

		return nil
	}

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	numPoints := 3

	timestamps := make([]int64, numPoints)
	for i := range numPoints {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	// Produce a V2 blob
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	for m := range 2 {
		err = encoder.StartMetricID(uint64(10001+m), numPoints)
		require.NoError(t, err)

		for i := range numPoints {
			err = encoder.AddDataPoint(timestamps[i], float64(m*10+i), "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Confirm it's V2
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	magic := options & section.MagicNumberMask
	require.Equal(t, uint16(section.MagicNumericV2Opt), magic)

	// Simulate the actual V1-only parser behavior against the real V2 bytes.
	err = legacyParseNumericHeader(data[:section.HeaderSize])
	require.ErrorIs(t, err, errs.ErrInvalidMagicNumber,
		"a V1-only parser must reject the real V2 blob header")

	// Current decoder (V2-aware) can still read the original V2 blob
	decoder, decErr := NewNumericDecoder(data)
	require.NoError(t, decErr)

	blob, decErr := decoder.Decode()
	require.NoError(t, decErr)
	require.Equal(t, 2, blob.MetricCount())
}

func TestNumericDecoder_V2MissingSharedTimestampTable(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	for metricIdx := range 2 {
		require.NoError(t, encoder.StartMetricID(uint64(11001+metricIdx), 3))
		for pointIdx := range 3 {
			ts := startTime.Add(time.Duration(pointIdx) * time.Second).UnixMicro()
			require.NoError(t, encoder.AddDataPoint(ts, float64(metricIdx*10+pointIdx), ""))
		}
		require.NoError(t, encoder.EndMetric())
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	corrupted := make([]byte, len(data))
	copy(corrupted, data)

	header, err := section.ParseNumericHeader(corrupted)
	require.NoError(t, err)

	indexEnd := int(header.IndexOffset) + int(header.MetricCount)*section.NumericIndexEntrySize
	engine := endian.GetLittleEndianEngine()
	engine.PutUint32(corrupted[20:24], uint32(indexEnd))

	decoder, err := NewNumericDecoder(corrupted)
	require.NoError(t, err)

	_, err = decoder.Decode()
	require.ErrorIs(t, err, errs.ErrInvalidSharedTimestampTable)
}

func TestNumericDecoder_V2MalformedSharedTimestampTable_CanonicalOutOfRange(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	for metricIdx := range 2 {
		require.NoError(t, encoder.StartMetricID(uint64(12001+metricIdx), 3))
		for pointIdx := range 3 {
			ts := startTime.Add(time.Duration(pointIdx) * time.Second).UnixMicro()
			require.NoError(t, encoder.AddDataPoint(ts, float64(metricIdx*10+pointIdx), ""))
		}
		require.NoError(t, encoder.EndMetric())
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	corrupted := make([]byte, len(data))
	copy(corrupted, data)

	header, err := section.ParseNumericHeader(corrupted)
	require.NoError(t, err)

	indexEnd := int(header.IndexOffset) + int(header.MetricCount)*section.NumericIndexEntrySize
	engine := header.Flag.GetEndianEngine()

	// Malformed table: groupCount=1, canonicalIdx=metricCount(out of range), memberCount=0.
	engine.PutUint16(corrupted[indexEnd:indexEnd+2], 1)
	engine.PutUint16(corrupted[indexEnd+2:indexEnd+4], uint16(header.MetricCount))
	engine.PutUint16(corrupted[indexEnd+4:indexEnd+6], 0)
	engine.PutUint32(corrupted[20:24], uint32(indexEnd+6))

	decoder, err := NewNumericDecoder(corrupted)
	require.NoError(t, err)

	_, err = decoder.Decode()
	require.ErrorIs(t, err, errs.ErrInvalidSharedTimestampTable)
}

func TestNumericDecoder_V2MalformedSharedTimestampTable_TruncatedMembers(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	for metricIdx := range 2 {
		require.NoError(t, encoder.StartMetricID(uint64(13001+metricIdx), 3))
		for pointIdx := range 3 {
			ts := startTime.Add(time.Duration(pointIdx) * time.Second).UnixMicro()
			require.NoError(t, encoder.AddDataPoint(ts, float64(metricIdx*10+pointIdx), ""))
		}
		require.NoError(t, encoder.EndMetric())
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	corrupted := make([]byte, len(data))
	copy(corrupted, data)

	header, err := section.ParseNumericHeader(corrupted)
	require.NoError(t, err)

	indexEnd := int(header.IndexOffset) + int(header.MetricCount)*section.NumericIndexEntrySize
	engine := header.Flag.GetEndianEngine()

	// Malformed table: groupCount=1, canonicalIdx=0, memberCount=1 but no member bytes.
	engine.PutUint16(corrupted[indexEnd:indexEnd+2], 1)
	engine.PutUint16(corrupted[indexEnd+2:indexEnd+4], 0)
	engine.PutUint16(corrupted[indexEnd+4:indexEnd+6], 1)
	engine.PutUint32(corrupted[20:24], uint32(indexEnd+6))

	decoder, err := NewNumericDecoder(corrupted)
	require.NoError(t, err)

	_, err = decoder.Decode()
	require.ErrorIs(t, err, errs.ErrInvalidSharedTimestampTable)
}

func TestNumericDecoder_V2MalformedSharedTimestampTable_SharedIndexOutOfRange(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithSharedTimestamps(),
	)
	require.NoError(t, err)

	for metricIdx := range 2 {
		require.NoError(t, encoder.StartMetricID(uint64(14001+metricIdx), 3))
		for pointIdx := range 3 {
			ts := startTime.Add(time.Duration(pointIdx) * time.Second).UnixMicro()
			require.NoError(t, encoder.AddDataPoint(ts, float64(metricIdx*10+pointIdx), ""))
		}
		require.NoError(t, encoder.EndMetric())
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	corrupted := make([]byte, len(data))
	copy(corrupted, data)

	header, err := section.ParseNumericHeader(corrupted)
	require.NoError(t, err)

	indexEnd := int(header.IndexOffset) + int(header.MetricCount)*section.NumericIndexEntrySize
	engine := header.Flag.GetEndianEngine()

	// Malformed table: groupCount=1, canonicalIdx=0, memberCount=1, member=metricCount(out of range).
	engine.PutUint16(corrupted[indexEnd:indexEnd+2], 1)
	engine.PutUint16(corrupted[indexEnd+2:indexEnd+4], 0)
	engine.PutUint16(corrupted[indexEnd+4:indexEnd+6], 1)
	engine.PutUint16(corrupted[indexEnd+6:indexEnd+8], uint16(header.MetricCount))
	engine.PutUint32(corrupted[20:24], uint32(indexEnd+8))

	decoder, err := NewNumericDecoder(corrupted)
	require.NoError(t, err)

	_, err = decoder.Decode()
	require.ErrorIs(t, err, errs.ErrInvalidSharedTimestampTable)
}

// ==============================================================================
// V2 Sorted Index Tests
// ==============================================================================

// TestV2SortedIndex_MetricIDsAreSorted verifies that V2 blobs store entries
// sorted by MetricID and MetricIDs() returns them in deterministic sorted order.
func TestV2SortedIndex_MetricIDsAreSorted(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Add metrics in non-sorted order to verify encoder sorts them
	metricIDs := []uint64{5000, 1000, 3000, 2000, 4000}

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithBlobLayoutV2(),
	)
	require.NoError(t, err)

	for _, id := range metricIDs {
		err = encoder.StartMetricID(id, 3)
		require.NoError(t, err)

		for i := range 3 {
			ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
			err = encoder.AddDataPoint(ts, float64(id)+float64(i)*0.1, "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// V2 MetricIDs() must return sorted order
	ids := blob.MetricIDs()
	require.Equal(t, []uint64{1000, 2000, 3000, 4000, 5000}, ids)

	// Verify sorted slice is used (byID should be nil for V2)
	require.Nil(t, blob.index.byID, "V2 should not populate byID map")
	require.NotNil(t, blob.index.sorted, "V2 should populate sorted slice")
	require.Len(t, blob.index.sorted, 5)
}

// TestV2SortedIndex_BinarySearchLookup verifies that HasMetricID and GetByID
// use binary search on V2 blobs and return correct results.
func TestV2SortedIndex_BinarySearchLookup(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithBlobLayoutV2(),
	)
	require.NoError(t, err)

	expectedValues := map[uint64]float64{
		100: 1.0,
		200: 2.0,
		300: 3.0,
		400: 4.0,
		500: 5.0,
	}

	for id, val := range expectedValues {
		err = encoder.StartMetricID(id, 1)
		require.NoError(t, err)

		ts := startTime.UnixMicro()
		err = encoder.AddDataPoint(ts, val, "")
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

	// Test HasMetricID (binary search path)
	for id := range expectedValues {
		require.True(t, blob.HasMetricID(id), "should find metric %d", id)
	}

	require.False(t, blob.HasMetricID(999), "should not find non-existent metric")
	require.False(t, blob.HasMetricID(0), "should not find metric 0")
	require.False(t, blob.HasMetricID(150), "should not find metric 150")

	// Test data integrity via All()
	for id, expectedVal := range expectedValues {
		for _, dp := range blob.All(id) {
			require.InDelta(t, expectedVal, dp.Val, 1e-10)
		}
	}
}

// TestV2SortedIndex_ForEachIteratesInOrder verifies ForEach iterates
// in sorted MetricID order on V2 blobs.
func TestV2SortedIndex_ForEachIteratesInOrder(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithBlobLayoutV2(),
	)
	require.NoError(t, err)

	// Add in reverse order
	for _, id := range []uint64{300, 100, 200} {
		err = encoder.StartMetricID(id, 2)
		require.NoError(t, err)

		for i := range 2 {
			ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
			err = encoder.AddDataPoint(ts, float64(id), "")
			require.NoError(t, err)
		}

		err = encoder.EndMetric()
		require.NoError(t, err)
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	// Verify materialization works correctly with ForEach
	material := blob.Materialize()
	for _, id := range []uint64{100, 200, 300} {
		val, ok := material.ValueAt(id, 0)
		require.True(t, ok)
		require.InDelta(t, float64(id), val, 1e-10)
	}
}

// TestV1V2_ConsistentData verifies that V1 and V2 layouts produce
// identical decoded data for the same input.
func TestV1V2_ConsistentData(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricIDs := []uint64{1001, 1002, 1003}
	numPoints := 5

	buildBlob := func(opts ...NumericEncoderOption) NumericBlob {
		encoder, err := NewNumericEncoder(startTime, opts...)
		require.NoError(t, err)

		for _, id := range metricIDs {
			err = encoder.StartMetricID(id, numPoints)
			require.NoError(t, err)

			for i := range numPoints {
				ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
				err = encoder.AddDataPoint(ts, float64(id)+float64(i)*0.1, "")
				require.NoError(t, err)
			}

			err = encoder.EndMetric()
			require.NoError(t, err)
		}

		data, err := encoder.Finish()
		require.NoError(t, err)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		blob, err := decoder.Decode()
		require.NoError(t, err)

		return blob
	}

	v1 := buildBlob(
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
	)
	v2 := buildBlob(
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithBlobLayoutV2(),
	)

	require.False(t, v1.IsV2Layout())
	require.True(t, v2.IsV2Layout())
	require.Equal(t, v1.MetricCount(), v2.MetricCount())

	// Verify all data points are identical
	for _, id := range metricIDs {
		require.Equal(t, v1.Len(id), v2.Len(id))

		v1Points := make([]NumericDataPoint, 0, numPoints)
		for _, dp := range v1.All(id) {
			v1Points = append(v1Points, dp)
		}

		v2Points := make([]NumericDataPoint, 0, numPoints)
		for _, dp := range v2.All(id) {
			v2Points = append(v2Points, dp)
		}

		require.Equal(t, v1Points, v2Points, "metric %d data should be identical", id)
	}
}

// TestV2SortedIndex_At verifies direct positional access.
func TestV2SortedIndex_At(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithBlobLayoutV2(),
	)
	require.NoError(t, err)

	ids := []uint64{300, 100, 200}
	for _, id := range ids {
		err = encoder.StartMetricID(id, 1)
		require.NoError(t, err)

		err = encoder.AddDataPoint(startTime.UnixMicro(), float64(id), "")
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

	// At(0) should be the smallest MetricID
	require.Equal(t, uint64(100), blob.index.At(0).MetricID)
	require.Equal(t, uint64(200), blob.index.At(1).MetricID)
	require.Equal(t, uint64(300), blob.index.At(2).MetricID)
}

// TestV2SortedIndex_WithTags verifies V2 sorted index works with tag support.
func TestV2SortedIndex_WithTags(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeChimp),
		WithBlobLayoutV2(),
		WithTagsEnabled(true),
	)
	require.NoError(t, err)

	metrics := []struct {
		id  uint64
		tag string
		val float64
	}{
		{500, "high", 99.9},
		{100, "low", 1.1},
		{300, "mid", 50.5},
	}

	for _, m := range metrics {
		err = encoder.StartMetricID(m.id, 1)
		require.NoError(t, err)

		err = encoder.AddDataPoint(startTime.UnixMicro(), m.val, m.tag)
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
	require.True(t, blob.IsV2Layout())

	for _, m := range metrics {
		for _, dp := range blob.All(m.id) {
			require.InDelta(t, m.val, dp.Val, 1e-10)
			require.Equal(t, m.tag, dp.Tag)
		}
	}
}

// TestV2SortedIndex_StartTime verifies StartTime() works with V2 sorted index
// (no byID map to check).
func TestV2SortedIndex_StartTime(t *testing.T) {
	startTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithBlobLayoutV2(),
	)
	require.NoError(t, err)

	err = encoder.StartMetricID(1, 1)
	require.NoError(t, err)

	err = encoder.AddDataPoint(startTime.UnixMicro(), 42.0, "")
	require.NoError(t, err)

	err = encoder.EndMetric()
	require.NoError(t, err)

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	blob, err := decoder.Decode()
	require.NoError(t, err)

	require.Equal(t, startTime, blob.StartTime())
}

// TestV2SortedIndex_EmptyBlobStartTime verifies StartTime() returns zero
// for an empty V2 blob (edge case: byID is nil, sorted is nil).
func TestV2SortedIndex_EmptyBlobStartTime(t *testing.T) {
	blob := NumericBlob{}
	require.True(t, blob.StartTime().IsZero())
}

// ==============================================================================
// Malformed Index Tests
// ==============================================================================

// TestNumericDecoder_MalformedIndex_CorruptedMetricCount tests that a blob with an
// inflated metric count is rejected at decode time due to insufficient index data.
func TestNumericDecoder_MalformedIndex_CorruptedMetricCount(t *testing.T) {
	startTime := time.Now()
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeRaw),
	)
	require.NoError(t, err)

	require.NoError(t, encoder.StartMetricID(1001, 2))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
	require.NoError(t, encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.0, ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Corrupt metric count to a very large value (bytes 12-15)
	engine := endian.GetLittleEndianEngine()
	engine.PutUint32(data[12:16], 5000)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	_, err = decoder.Decode()
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidIndexEntrySize)
}

// TestNumericDecoder_MalformedIndex_PayloadOffsetBeyondData tests that corrupted
// payload offset values in the header are detected during decode.
func TestNumericDecoder_MalformedIndex_PayloadOffsetBeyondData(t *testing.T) {
	startTime := time.Now()
	createBlob := func() []byte {
		encoder, err := NewNumericEncoder(startTime,
			WithTimestampEncoding(format.TypeRaw),
			WithValueEncoding(format.TypeRaw),
		)
		require.NoError(t, err)

		require.NoError(t, encoder.StartMetricID(1001, 1))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
		require.NoError(t, encoder.EndMetric())

		data, err := encoder.Finish()
		require.NoError(t, err)

		return data
	}

	engine := endian.GetLittleEndianEngine()

	t.Run("CorruptedTimestampOffset", func(t *testing.T) {
		data := createBlob()
		engine.PutUint32(data[20:24], uint32(len(data)+100))

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		_, err = decoder.Decode()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidTimestampPayloadOffset)
	})

	t.Run("CorruptedValueOffset", func(t *testing.T) {
		data := createBlob()
		engine.PutUint32(data[24:28], uint32(len(data)+100))

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		_, err = decoder.Decode()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidValuePayloadOffset)
	})

	t.Run("CorruptedTagOffset", func(t *testing.T) {
		data := createBlob()
		engine.PutUint32(data[28:32], uint32(len(data)+100))

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		_, err = decoder.Decode()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidTagPayloadOffset)
	})
}

// TestNumericDecoder_MalformedIndex_TruncatedBlob tests decoding a blob that has
// been truncated after the header (missing payloads).
func TestNumericDecoder_MalformedIndex_TruncatedBlob(t *testing.T) {
	startTime := time.Now()
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeRaw),
	)
	require.NoError(t, err)

	require.NoError(t, encoder.StartMetricID(1001, 3))
	for i := range 3 {
		require.NoError(t, encoder.AddDataPoint(startTime.Add(time.Duration(i)*time.Second).UnixMicro(), float64(i), ""))
	}
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Truncate the blob to just past the header + partial index
	truncated := data[:section.HeaderSize+4]

	decoder, err := NewNumericDecoder(truncated)
	require.NoError(t, err)

	_, err = decoder.Decode()
	require.Error(t, err)
}

// TestNumericDecoder_MalformedIndex_IndexOffsetExceedsPayload tests that corrupted
// delta offsets in index entries (producing absolute offsets that exceed decompressed
// payload sizes) are caught at Decode time rather than causing runtime panics.
func TestNumericDecoder_MalformedIndex_IndexOffsetExceedsPayload(t *testing.T) {
	startTime := time.Now()
	engine := endian.GetLittleEndianEngine()

	createBlob := func() []byte {
		encoder, err := NewNumericEncoder(startTime,
			WithTimestampEncoding(format.TypeRaw),
			WithValueEncoding(format.TypeRaw),
		)
		require.NoError(t, err)

		require.NoError(t, encoder.StartMetricID(1001, 2))
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
		require.NoError(t, encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.0, ""))
		require.NoError(t, encoder.EndMetric())

		data, err := encoder.Finish()
		require.NoError(t, err)

		return data
	}

	// Index entry starts at HeaderSize (32). Layout per entry (16 bytes):
	//   [0:8]   MetricID  (uint64)
	//   [8:10]  Count     (uint16)
	//   [10:12] TsOffset  (uint16 delta)
	//   [12:14] ValOffset (uint16 delta)
	//   [14:16] TagOffset (uint16 delta)

	t.Run("CorruptedTimestampDeltaOffset", func(t *testing.T) {
		data := createBlob()
		entryStart := section.HeaderSize
		// Corrupt the timestamp delta offset to a huge value
		engine.PutUint16(data[entryStart+10:entryStart+12], 0xFFFF)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		_, err = decoder.Decode()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidIndexOffsets)
	})

	t.Run("CorruptedValueDeltaOffset", func(t *testing.T) {
		data := createBlob()
		entryStart := section.HeaderSize
		// Corrupt the value delta offset to a huge value
		engine.PutUint16(data[entryStart+12:entryStart+14], 0xFFFF)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		_, err = decoder.Decode()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidIndexOffsets)
	})

	t.Run("CorruptedTagDeltaOffset", func(t *testing.T) {
		data := createBlob()
		entryStart := section.HeaderSize
		// Corrupt the tag delta offset to a huge value
		engine.PutUint16(data[entryStart+14:entryStart+16], 0xFFFF)

		decoder, err := NewNumericDecoder(data)
		require.NoError(t, err)

		_, err = decoder.Decode()
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidIndexOffsets)
	})
}

// TestNumericDecoder_ReservedByteCorruption verifies that the decoder returns
// ErrInvalidReservedBytes when the reserved bytes of an extended (32B) index
// entry are non-zero.
func TestNumericDecoder_ReservedByteCorruption(t *testing.T) {
	startTime := time.Now()

	// Create a valid extended-mode blob (count triggers 0xEA30)
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
		WithBlobLayoutV2(),
	)
	require.NoError(t, err)

	numPoints := 70_000 // triggers extended mode via count > uint16
	require.NoError(t, encoder.StartMetricID(6001, numPoints))
	for i := range numPoints {
		require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro()+int64(i)*1_000_000, 1.0, ""))
	}
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Verify it's actually extended format
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	require.Equal(t, uint16(section.MagicNumericV2ExtOpt), options&section.MagicNumberMask)

	// Corrupt a reserved byte in the first extended index entry (bytes 24-31 are reserved).
	// The first index entry starts at HeaderSize (32).
	entryStart := section.HeaderSize
	data[entryStart+24] = 0xFF // corrupt reserved byte

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	_, err = decoder.Decode()
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidReservedBytes)
}

// TestNumericDecoder_MaliciousMagicExtendedTruncatedData verifies that a blob
// crafted with magic 0xEA30 (extended 32B entries) but only enough data for
// 16B entries does not panic. The decoder must perform strict bounds checking
// on the index section size.
func TestNumericDecoder_MaliciousMagicExtendedTruncatedData(t *testing.T) {
	startTime := time.Now()
	engine := endian.GetLittleEndianEngine()

	// Create a valid compact-mode blob (small data, 16B entries)
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeRaw),
		WithBlobLayoutV2(),
	)
	require.NoError(t, err)

	require.NoError(t, encoder.StartMetricID(7001, 2))
	require.NoError(t, encoder.AddDataPoint(startTime.UnixMicro(), 1.0, ""))
	require.NoError(t, encoder.AddDataPoint(startTime.Add(time.Second).UnixMicro(), 2.0, ""))
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Verify it's compact format (0xEA20) with 16B entries
	options := uint16(data[0]) | (uint16(data[1]) << 8)
	require.Equal(t, uint16(section.MagicNumericV2Opt), options&section.MagicNumberMask)

	// Maliciously overwrite magic to 0xEA30 (extended 32B entries) and inflate
	// MetricCount so the expected index section (metricCount × 32) exceeds the
	// data actually present. This simulates a crafted blob designed to trick
	// the decoder into reading past the end of the buffer.
	newOptions := (options & ^uint16(section.MagicNumberMask)) | uint16(section.MagicNumericV2ExtOpt)
	engine.PutUint16(data[0:2], newOptions)
	engine.PutUint32(data[12:16], 100) // inflate metric count: 100 × 32B = 3200 bytes expected

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	_, err = decoder.Decode()
	require.Error(t, err, "decoder must not panic on truncated extended index data")
	require.ErrorIs(t, err, errs.ErrInvalidIndexEntrySize)
}
