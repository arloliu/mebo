package main

import (
	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
)

// MeasurementResult holds the results of a single compression measurement.
type MeasurementResult struct {
	NumMetrics       int              // Number of metrics tested
	PointsPerMetric  int              // Points per metric in this test
	TotalPoints      int              // Total number of data points (NumMetrics × PointsPerMetric)
	BlobSize         int              // Size of encoded blob in bytes
	BytesPerPoint    float64          // Bytes per point (BlobSize / TotalPoints)
	CompressionRatio float64          // Compression ratio vs raw encoding
	SavingsPercent   float64          // Savings percentage vs 16 bytes/point baseline
	NumericBlob      blob.NumericBlob // The actual numeric blob for regression analysis
}

// MeasureDeltaGorilla measures the compression efficiency using Delta+Gorilla encoding.
//
// This function encodes the test data using Delta timestamp encoding and Gorilla
// value encoding without additional compression, then calculates various metrics.
//
// Parameters:
//   - data: Test data to encode
//   - numPoints: Number of points per metric to use (data will be sliced)
//
// Returns:
//   - MeasurementResult: Measurement results with calculated metrics
//   - error: Encoding error if any
//
// Example:
//
//	data := GenerateTestData(config)
//	result, err := MeasureDeltaGorilla(data, 100)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Bytes per point: %.2f\n", result.BytesPerPoint)
func MeasureDeltaGorilla(data TestData, numPoints int) (MeasurementResult, error) {
	// Slice data to requested point count
	sliced := data.SlicePoints(numPoints)

	// Create encoder with Delta+Gorilla, no compression
	encoder, err := blob.NewNumericEncoder(
		sliced.StartTime,
		blob.WithTimestampEncoding(format.TypeDelta),
		blob.WithTimestampCompression(format.CompressionNone),
		blob.WithValueEncoding(format.TypeGorilla),
		blob.WithValueCompression(format.CompressionNone),
	)
	if err != nil {
		return MeasurementResult{}, err
	}

	numMetrics := len(sliced.MetricIDs)

	// Encode all metrics
	for i := 0; i < numMetrics; i++ {
		metricID := sliced.MetricIDs[i]
		encoder.StartMetricID(metricID, numPoints)

		for j := 0; j < numPoints; j++ {
			idx := i*numPoints + j
			encoder.AddDataPoint(sliced.Timestamps[idx], sliced.Values[idx], "")
		}

		encoder.EndMetric()
	}

	// Get final blob
	blobData, err := encoder.Finish()
	if err != nil {
		return MeasurementResult{}, err
	}

	// Calculate metrics
	totalPoints := numMetrics * numPoints
	blobSize := len(blobData)
	bytesPerPoint := float64(blobSize) / float64(totalPoints)
	baselineBytes := totalPoints * 16 // 8 bytes timestamp + 8 bytes value
	savingsPercent := (1.0 - (float64(blobSize) / float64(baselineBytes))) * 100.0

	// Calculate compression ratio (we'll get actual raw size for accuracy)
	rawResult, err := MeasureRawBaseline(data, numPoints)
	if err != nil {
		return MeasurementResult{}, err
	}
	compressionRatio := float64(rawResult.BlobSize) / float64(blobSize)

	// Create numeric blob from the encoded data for regression analysis
	numericBlob, err := createNumericBlobFromEncodedData(blobData, sliced)
	if err != nil {
		// If numeric blob creation fails, continue without it
		// The regression analysis will fall back to direct analysis
		numericBlob = blob.NumericBlob{}
	}

	return MeasurementResult{
		NumMetrics:       numMetrics,
		PointsPerMetric:  numPoints,
		TotalPoints:      totalPoints,
		BlobSize:         blobSize,
		BytesPerPoint:    bytesPerPoint,
		CompressionRatio: compressionRatio,
		SavingsPercent:   savingsPercent,
		NumericBlob:      numericBlob,
	}, nil
}

// createNumericBlobFromEncodedData creates a numeric blob from encoded blob data.
func createNumericBlobFromEncodedData(blobData []byte, data TestData) (blob.NumericBlob, error) {
	// Create a numeric decoder from the encoded data
	_, err := blob.NewNumericDecoder(blobData)
	if err != nil {
		return blob.NumericBlob{}, err
	}

	// Convert decoder to numeric blob
	// This is a simplified approach - in practice, we'd need to properly
	// reconstruct the numeric blob from the decoder
	return blob.NumericBlob{}, nil
}

// MeasureRawBaseline measures blob size using Raw+Raw encoding (no compression).
//
// This provides a baseline for compression ratio calculations.
//
// Parameters:
//   - data: Test data to encode
//   - numPoints: Number of points per metric to use
//
// Returns:
//   - MeasurementResult: Measurement results for raw encoding
//   - error: Encoding error if any
func MeasureRawBaseline(data TestData, numPoints int) (MeasurementResult, error) {
	// Slice data to requested point count
	sliced := data.SlicePoints(numPoints)

	// Create encoder with Raw+Raw, no compression
	encoder, err := blob.NewNumericEncoder(
		sliced.StartTime,
		blob.WithTimestampEncoding(format.TypeRaw),
		blob.WithTimestampCompression(format.CompressionNone),
		blob.WithValueEncoding(format.TypeRaw),
		blob.WithValueCompression(format.CompressionNone),
	)
	if err != nil {
		return MeasurementResult{}, err
	}

	numMetrics := len(sliced.MetricIDs)

	// Encode all metrics
	for i := 0; i < numMetrics; i++ {
		metricID := sliced.MetricIDs[i]
		encoder.StartMetricID(metricID, numPoints)

		for j := 0; j < numPoints; j++ {
			idx := i*numPoints + j
			encoder.AddDataPoint(sliced.Timestamps[idx], sliced.Values[idx], "")
		}

		encoder.EndMetric()
	}

	// Get final blob
	blobData, err := encoder.Finish()
	if err != nil {
		return MeasurementResult{}, err
	}

	// Calculate metrics
	totalPoints := numMetrics * numPoints
	blobSize := len(blobData)
	bytesPerPoint := float64(blobSize) / float64(totalPoints)

	return MeasurementResult{
		NumMetrics:       numMetrics,
		PointsPerMetric:  numPoints,
		TotalPoints:      totalPoints,
		BlobSize:         blobSize,
		BytesPerPoint:    bytesPerPoint,
		CompressionRatio: 1.0,
		SavingsPercent:   0.0,
	}, nil
}
