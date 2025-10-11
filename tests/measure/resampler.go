package main

import (
	"errors"
	"fmt"
	"math"
)

// ResampleStrategy defines how to select points when resampling.
type ResampleStrategy string

const (
	// ResampleStrategyFirst takes the first N points from each metric.
	ResampleStrategyFirst ResampleStrategy = "first"
	// ResampleStrategyEvenly selects points evenly distributed across the time range.
	ResampleStrategyEvenly ResampleStrategy = "evenly"
)

// CalculateTestPoints generates appropriate test point counts based on max points.
//
// This function creates a reasonable set of test point counts to measure
// compression efficiency at different scales.
//
// Parameters:
//   - maxPoints: Maximum points available per metric
//
// Returns:
//   - []int: Slice of point counts to test
//
// Example:
//
//	points := CalculateTestPoints(200)
//	// Returns: [1, 2, 5, 10, 20, 50, 100, 150, 200]
func CalculateTestPoints(maxPoints int) []int {
	// Standard test points
	standardPoints := []int{1, 2, 5, 10, 20, 50, 100, 150, 200, 500, 1000, 2000, 5000}

	// Filter points that are <= maxPoints
	var validPoints []int
	for _, pts := range standardPoints {
		if pts <= maxPoints {
			validPoints = append(validPoints, pts)
		}
	}

	// If maxPoints is not in the list and we have room, add it
	if len(validPoints) > 0 && validPoints[len(validPoints)-1] != maxPoints {
		// Only add if it's meaningfully different from the last point
		lastPoint := validPoints[len(validPoints)-1]
		if maxPoints > lastPoint && float64(maxPoints)/float64(lastPoint) > 1.2 {
			validPoints = append(validPoints, maxPoints)
		}
	}

	// Ensure we have at least one point count to test
	if len(validPoints) == 0 {
		validPoints = []int{maxPoints}
	}

	return validPoints
}

// ResampleTestData resamples test data to create a new TestData with the specified
// max points per metric using the given strategy.
//
// This is useful when loading real-world data that may have irregular point counts
// across metrics. The resampling ensures all metrics have consistent point counts.
//
// Parameters:
//   - data: Original test data
//   - maxPoints: Maximum points per metric in resampled data
//   - strategy: Resampling strategy (first, evenly)
//
// Returns:
//   - *TestData: Resampled test data
//   - error: Error if resampling fails
//
// Example:
//
//	original, _ := LoadInputData("metrics.json", TimeUnitMicrosecond)
//	resampled, _ := ResampleTestData(original, 100, ResampleStrategyFirst)
func ResampleTestData(data *TestData, maxPoints int, strategy ResampleStrategy) (*TestData, error) {
	if maxPoints <= 0 {
		return nil, fmt.Errorf("maxPoints must be positive, got %d", maxPoints)
	}

	numMetrics := len(data.MetricIDs)
	if numMetrics == 0 {
		return nil, errors.New("no metrics in test data")
	}

	// Calculate current points per metric (assuming uniform distribution in original data)
	totalPoints := len(data.Timestamps)
	currentPointsPerMetric := totalPoints / numMetrics

	// If we already have the right number of points or fewer, just use SlicePoints
	if currentPointsPerMetric <= maxPoints {
		sliced := data.SlicePoints(currentPointsPerMetric)
		return &sliced, nil
	}

	// Create resampled data structure
	resampledTotalPoints := numMetrics * maxPoints
	resampled := &TestData{
		MetricIDs:  data.MetricIDs, // Share metric IDs
		Timestamps: make([]int64, resampledTotalPoints),
		Values:     make([]float64, resampledTotalPoints),
		StartTime:  data.StartTime,
		Config: Config{
			NumMetrics: numMetrics,
			MaxPoints:  maxPoints,
		},
	}

	// Resample each metric
	for i := 0; i < numMetrics; i++ {
		srcStart := i * currentPointsPerMetric
		srcEnd := srcStart + currentPointsPerMetric
		dstStart := i * maxPoints

		switch strategy {
		case ResampleStrategyFirst:
			// Simply take the first maxPoints
			for j := 0; j < maxPoints; j++ {
				resampled.Timestamps[dstStart+j] = data.Timestamps[srcStart+j]
				resampled.Values[dstStart+j] = data.Values[srcStart+j]
			}

		case ResampleStrategyEvenly:
			// Select points evenly distributed across the range
			err := resampleEvenly(
				data.Timestamps[srcStart:srcEnd],
				data.Values[srcStart:srcEnd],
				resampled.Timestamps[dstStart:dstStart+maxPoints],
				resampled.Values[dstStart:dstStart+maxPoints],
				maxPoints,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to resample metric %d: %w", i, err)
			}

		default:
			return nil, fmt.Errorf("unknown resampling strategy: %s", strategy)
		}
	}

	return resampled, nil
}

// resampleEvenly selects points evenly distributed across the time range.
func resampleEvenly(srcTimestamps []int64, srcValues []float64, dstTimestamps []int64, dstValues []float64, numPoints int) error {
	srcLen := len(srcTimestamps)
	if srcLen < numPoints {
		return fmt.Errorf("source has fewer points (%d) than requested (%d)", srcLen, numPoints)
	}

	if numPoints == 1 {
		// Special case: just take the first point
		dstTimestamps[0] = srcTimestamps[0]
		dstValues[0] = srcValues[0]
		return nil
	}

	// Calculate step size as a float for even distribution
	step := float64(srcLen-1) / float64(numPoints-1)

	for i := 0; i < numPoints; i++ {
		// Calculate source index
		srcIdx := int(math.Round(float64(i) * step))
		if srcIdx >= srcLen {
			srcIdx = srcLen - 1
		}

		dstTimestamps[i] = srcTimestamps[srcIdx]
		dstValues[i] = srcValues[srcIdx]
	}

	return nil
}

// NormalizeTestData ensures that all metrics have the same number of points
// by finding the minimum point count and truncating all metrics to that length.
//
// This is useful when loading real-world data where different metrics may have
// different numbers of points.
//
// Parameters:
//   - data: Original test data with potentially varying point counts
//
// Returns:
//   - *TestData: Normalized test data where all metrics have the same point count
//   - int: The number of points per metric after normalization
//
// Example:
//
//	original, _ := LoadInputData("metrics.json", TimeUnitMicrosecond)
//	normalized, pointsPerMetric := NormalizeTestData(original)
func NormalizeTestData(data *TestData) (*TestData, int) {
	numMetrics := len(data.MetricIDs)
	if numMetrics == 0 {
		return data, 0
	}

	// For now, assume all metrics have MaxPoints (since our loader ensures this)
	// This function is here for future enhancement if we support irregular data
	pointsPerMetric := data.Config.MaxPoints

	// Use SlicePoints to normalize
	normalized := data.SlicePoints(pointsPerMetric)

	return &normalized, pointsPerMetric
}
