package fbscompare

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
	"github.com/stretchr/testify/require"
)

// TestTextBlobIntegration_BasicEncoding tests basic text blob encoding and decoding
func TestTextBlobIntegration_BasicEncoding(t *testing.T) {
	numMetrics := 10
	numPoints := 5
	cfg := DefaultTestDataConfig(numMetrics, numPoints)
	testData := GenerateTextTestData(cfg)

	// Test with raw encoding, no compression
	blob, err := createMeboTextBlob(testData, format.TypeRaw, format.CompressionNone)
	require.NoError(t, err)
	require.NotEmpty(t, blob)

	// Decode and verify
	textBlob, err := decodeMeboTextBlob(blob)
	require.NoError(t, err)

	// Verify all metrics
	for _, metric := range testData {
		count := 0
		for idx, dp := range textBlob.All(metric.ID) {
			require.Equal(t, idx, count, "iterator index should match count")
			require.Equal(t, metric.Timestamps[count], dp.Ts)
			require.Equal(t, metric.Values[count], dp.Val)
			count++
		}
		require.Equal(t, numPoints, count, "should have %d points", numPoints)
	}
}

// TestTextBlobIntegration_CompressionVariants tests all compression types
func TestTextBlobIntegration_CompressionVariants(t *testing.T) {
	numMetrics := 50
	numPoints := 10
	cfg := DefaultTestDataConfig(numMetrics, numPoints)
	testData := GenerateTextTestData(cfg)

	compressions := []struct {
		name string
		comp format.CompressionType
	}{
		{"none", format.CompressionNone},
		{"zstd", format.CompressionZstd},
		{"s2", format.CompressionS2},
		{"lz4", format.CompressionLZ4},
	}

	for _, tc := range compressions {
		t.Run(tc.name, func(t *testing.T) {
			blob, err := createMeboTextBlob(testData, format.TypeRaw, tc.comp)
			require.NoError(t, err)

			textBlob, err := decodeMeboTextBlob(blob)
			require.NoError(t, err)

			// Verify first metric
			metric := testData[0]
			count := 0
			for idx, dp := range textBlob.All(metric.ID) {
				require.Equal(t, idx, count, "iterator index should match count")
				require.Equal(t, metric.Timestamps[count], dp.Ts)
				require.Equal(t, metric.Values[count], dp.Val)
				count++
			}
			require.Equal(t, numPoints, count)
		})
	}
}

// TestTextBlobIntegration_TimestampEncodings tests different timestamp encodings
func TestTextBlobIntegration_TimestampEncodings(t *testing.T) {
	numMetrics := 20
	numPoints := 10
	cfg := DefaultTestDataConfig(numMetrics, numPoints)
	testData := GenerateTextTestData(cfg)

	encodings := []struct {
		name string
		enc  format.EncodingType
	}{
		{"raw", format.TypeRaw},
		{"delta", format.TypeDelta},
	}

	for _, tc := range encodings {
		t.Run(tc.name, func(t *testing.T) {
			blob, err := createMeboTextBlob(testData, tc.enc, format.CompressionNone)
			require.NoError(t, err)

			textBlob, err := decodeMeboTextBlob(blob)
			require.NoError(t, err)

			// Verify all metrics
			for _, metric := range testData {
				count := 0
				for idx, dp := range textBlob.All(metric.ID) {
					require.Equal(t, idx, count, "iterator index should match count")
					require.Equal(t, metric.Timestamps[count], dp.Ts)
					require.Equal(t, metric.Values[count], dp.Val)
					count++
				}
				require.Equal(t, numPoints, count)
			}
		})
	}
}

// TestTextBlobIntegration_RandomAccess tests ValueAt and TimestampAt
func TestTextBlobIntegration_RandomAccess(t *testing.T) {
	numMetrics := 10
	numPoints := 20
	cfg := DefaultTestDataConfig(numMetrics, numPoints)
	testData := GenerateTextTestData(cfg)

	blob, err := createMeboTextBlob(testData, format.TypeRaw, format.CompressionZstd)
	require.NoError(t, err)

	textBlob, err := decodeMeboTextBlob(blob)
	require.NoError(t, err)

	// Test random access
	metric := testData[0]

	// Test first point
	ts, ok := textBlob.TimestampAt(metric.ID, 0)
	require.True(t, ok)
	require.Equal(t, metric.Timestamps[0], ts)

	val, ok := textBlob.ValueAt(metric.ID, 0)
	require.True(t, ok)
	require.Equal(t, metric.Values[0], val)

	// Test last point
	ts, ok = textBlob.TimestampAt(metric.ID, numPoints-1)
	require.True(t, ok)
	require.Equal(t, metric.Timestamps[numPoints-1], ts)

	val, ok = textBlob.ValueAt(metric.ID, numPoints-1)
	require.True(t, ok)
	require.Equal(t, metric.Values[numPoints-1], val)

	// Test out of range
	_, ok = textBlob.ValueAt(metric.ID, numPoints)
	require.False(t, ok)

	_, ok = textBlob.TimestampAt(metric.ID, -1)
	require.False(t, ok)
}

// TestTextBlobIntegration_Iterator tests iterator methods
func TestTextBlobIntegration_Iterator(t *testing.T) {
	numMetrics := 10
	numPoints := 15
	cfg := DefaultTestDataConfig(numMetrics, numPoints)
	testData := GenerateTextTestData(cfg)

	blob, err := createMeboTextBlob(testData, format.TypeDelta, format.CompressionS2)
	require.NoError(t, err)

	textBlob, err := decodeMeboTextBlob(blob)
	require.NoError(t, err)

	metric := testData[0]

	// Test AllTimestamps
	count := 0
	for ts := range textBlob.AllTimestamps(metric.ID) {
		require.Equal(t, metric.Timestamps[count], ts)
		count++
	}
	require.Equal(t, numPoints, count)

	// Test AllValues
	count = 0
	for val := range textBlob.AllValues(metric.ID) {
		require.Equal(t, metric.Values[count], val)
		count++
	}
	require.Equal(t, numPoints, count)
}

// TestTextBlobIntegration_LargeDataset tests with larger dataset
func TestTextBlobIntegration_LargeDataset(t *testing.T) {
	numMetrics := 200
	numPoints := 100
	cfg := DefaultTestDataConfig(numMetrics, numPoints)
	testData := GenerateTextTestData(cfg)

	blob, err := createMeboTextBlob(testData, format.TypeDelta, format.CompressionZstd)
	require.NoError(t, err)

	t.Logf("Blob size: %d bytes for %d metrics × %d points", len(blob), numMetrics, numPoints)

	textBlob, err := decodeMeboTextBlob(blob)
	require.NoError(t, err)

	// Verify a few random metrics
	for i := 0; i < 5; i++ {
		metric := testData[i*40] // Sample every 40th metric
		count := 0
		for idx, dp := range textBlob.All(metric.ID) {
			require.Equal(t, idx, count, "iterator index should match count")
			require.Equal(t, metric.Timestamps[count], dp.Ts)
			require.Equal(t, metric.Values[count], dp.Val)
			count++
		}
		require.Equal(t, numPoints, count)
	}
}

// TestTextBlobIntegration_MeboVsFBS compares mebo and FlatBuffers output
func TestTextBlobIntegration_MeboVsFBS(t *testing.T) {
	numMetrics := 50
	numPoints := 20
	cfg := DefaultTestDataConfig(numMetrics, numPoints)
	testData := GenerateTextTestData(cfg)

	// Create mebo blob
	meboBlob, err := createMeboTextBlob(testData, format.TypeRaw, format.CompressionZstd)
	require.NoError(t, err)

	// Create FBS blob
	fbsBlob, err := EncodeTextFBS(testData, "zstd")
	require.NoError(t, err)

	t.Logf("Mebo blob size: %d bytes", len(meboBlob))
	t.Logf("FBS blob size: %d bytes", fbsBlob.Size())
	t.Logf("Size ratio (mebo/fbs): %.2f", float64(len(meboBlob))/float64(fbsBlob.Size()))

	// Decode both
	meboDecoded, err := decodeMeboTextBlob(meboBlob)
	require.NoError(t, err)

	err = fbsBlob.Decode()
	require.NoError(t, err)

	// Verify data matches
	metric := testData[0]

	// Verify mebo
	meboCount := 0
	for idx, dp := range meboDecoded.All(metric.ID) {
		require.Equal(t, idx, meboCount, "iterator index should match count")
		require.Equal(t, metric.Timestamps[meboCount], dp.Ts)
		require.Equal(t, metric.Values[meboCount], dp.Val)
		meboCount++
	}

	// Verify FBS
	fbsCount := 0
	for ts, val := range fbsBlob.All(metric.ID) {
		require.Equal(t, metric.Timestamps[fbsCount], ts)
		require.Equal(t, metric.Values[fbsCount], val)
		fbsCount++
	}

	require.Equal(t, meboCount, fbsCount, "both should have same number of points")
}

// Helper: Create mebo text blob from test data
func createMeboTextBlob(metrics []TextMetricData, tsEncoding format.EncodingType, compression format.CompressionType) ([]byte, error) {
	if len(metrics) == 0 {
		return nil, nil
	}

	startTimeMicro := metrics[0].Timestamps[0]
	startTime := time.UnixMicro(startTimeMicro)

	encoder, err := blob.NewTextEncoder(
		startTime,
		blob.WithTextTimestampEncoding(tsEncoding),
		blob.WithTextDataCompression(compression),
	)
	if err != nil {
		return nil, err
	}

	for _, metric := range metrics {
		if err := encoder.StartMetricID(metric.ID, len(metric.Timestamps)); err != nil {
			return nil, err
		}

		for i := range metric.Timestamps {
			tag := ""
			if i < len(metric.Tags) {
				tag = metric.Tags[i]
			}
			if err := encoder.AddDataPoint(metric.Timestamps[i], metric.Values[i], tag); err != nil {
				return nil, err
			}
		}

		if err := encoder.EndMetric(); err != nil {
			return nil, err
		}
	}

	return encoder.Finish()
}

// Helper: Decode mebo text blob
func decodeMeboTextBlob(data []byte) (*blob.TextBlob, error) {
	decoder, err := blob.NewTextDecoder(data)
	if err != nil {
		return nil, err
	}

	decoded, err := decoder.Decode()
	if err != nil {
		return nil, err
	}

	return &decoded, nil
}
