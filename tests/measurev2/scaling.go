package main

// scalingPointCounts returns the point counts to test for scaling analysis.
// These are capped by maxPoints.
func scalingPointCounts(maxPoints int) []int {
	standard := []int{1, 2, 5, 10, 20, 50, 100, 150, 200, 500, 1000}

	result := make([]int, 0, len(standard)+1)
	for _, pts := range standard {
		if pts <= maxPoints {
			result = append(result, pts)
		}
	}

	// Add maxPoints if not already in the list
	if len(result) > 0 && result[len(result)-1] != maxPoints {
		if float64(maxPoints)/float64(result[len(result)-1]) > 1.2 {
			result = append(result, maxPoints)
		}
	}

	if len(result) == 0 {
		result = []int{maxPoints}
	}

	return result
}

// runScaling runs the scaling analysis for one encoding combo.
// It measures encoded size at different points-per-metric counts.
func runScaling(combo EncodingCombo, data *TestData) (ScalingResult, error) {
	pointCounts := scalingPointCounts(data.Config.PointsPerMetric)

	series := make([]ScalingPoint, 0, len(pointCounts))
	for _, pts := range pointCounts {
		sliced := data.SlicePoints(pts)
		encodedSize, err := measureEncodedSize(combo, sliced)
		if err != nil {
			return ScalingResult{}, err
		}

		totalPoints := len(sliced.MetricIDs) * pts
		bpp := float64(encodedSize) / float64(totalPoints)

		series = append(series, ScalingPoint{
			PointsPerMetric: pts,
			EncodedBytes:    encodedSize,
			BytesPerPoint:   bpp,
		})
	}

	return ScalingResult{
		Label:       combo.Label,
		TSEncoding:  combo.TSEncoding.String(),
		ValEncoding: combo.ValEncoding.String(),
		NumMetrics:  data.Config.NumMetrics,
		Series:      series,
	}, nil
}
