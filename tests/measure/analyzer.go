package main

import (
	"fmt"
	"math"
	"slices"
)

// Statistics holds statistical summary of measurement results.
type Statistics struct {
	MinBytesPerPoint    float64
	MaxBytesPerPoint    float64
	AvgBytesPerPoint    float64
	MedianBytesPerPoint float64
	StdDeviation        float64
}

// FormulaFit holds information about a fitted formula.
type FormulaFit struct {
	Type         string    // Formula type: "logarithmic", "power", "hyperbolic", "exponential"
	Coefficients []float64 // Formula coefficients
	RSquared     float64   // R² goodness of fit (0-1, higher is better)
	RMSE         float64   // Root mean square error
	Formula      string    // Human-readable formula
}

// AnalysisResult holds the complete analysis of measurement results.
type AnalysisResult struct {
	Measurements []MeasurementResult
	Statistics   Statistics
	BestFit      FormulaFit
	AllFits      []FormulaFit // All attempted fits for comparison
}

// Analyze performs statistical analysis and formula fitting on measurement results.
//
// This function:
//  1. Calculates basic statistics (min, max, mean, median, std dev)
//  2. Attempts multiple formula fits (logarithmic, power, hyperbolic)
//  3. Selects the best fit based on R² value
//  4. Generates human-readable formulas
//
// Parameters:
//   - results: Slice of measurement results to analyze
//
// Returns:
//   - AnalysisResult: Complete analysis with statistics and best-fit formula
//
// Example:
//
//	var results []MeasurementResult
//	// ... collect measurements ...
//	analysis := Analyze(results)
//	fmt.Printf("Best fit: %s (R²=%.3f)\n", analysis.BestFit.Formula, analysis.BestFit.RSquared)
func Analyze(results []MeasurementResult) AnalysisResult {
	if len(results) == 0 {
		return AnalysisResult{}
	}

	// Extract BPP and PPM values
	bppValues := make([]float64, len(results))
	ppmValues := make([]float64, len(results))
	for i, r := range results {
		bppValues[i] = r.BytesPerPoint
		ppmValues[i] = float64(r.PointsPerMetric)
	}

	// Calculate statistics
	stats := Statistics{
		MinBytesPerPoint:    calculateMin(bppValues),
		MaxBytesPerPoint:    calculateMax(bppValues),
		AvgBytesPerPoint:    calculateMean(bppValues),
		MedianBytesPerPoint: calculateMedian(bppValues),
		StdDeviation:        calculateStdDev(bppValues, calculateMean(bppValues)),
	}

	// Try different formula fits
	var allFits []FormulaFit

	// 1. Logarithmic: BPP = a + b * ln(PPM)
	logFit := fitLogarithmic(ppmValues, bppValues)
	allFits = append(allFits, logFit)

	// 2. Power: BPP = a * PPM^b
	powerFit := fitPower(ppmValues, bppValues)
	allFits = append(allFits, powerFit)

	// 3. Hyperbolic: BPP = a + b / PPM
	hypFit := fitHyperbolic(ppmValues, bppValues)
	allFits = append(allFits, hypFit)

	// Select best fit (highest R²)
	bestFit := allFits[0]
	for _, fit := range allFits {
		if fit.RSquared > bestFit.RSquared {
			bestFit = fit
		}
	}

	return AnalysisResult{
		Measurements: results,
		Statistics:   stats,
		BestFit:      bestFit,
		AllFits:      allFits,
	}
}

// fitLogarithmic fits BPP = a + b * ln(PPM) using least squares.
func fitLogarithmic(x, y []float64) FormulaFit {
	n := len(x)
	if n == 0 {
		return FormulaFit{Type: "logarithmic"}
	}

	// Transform: X' = ln(x), fit y = a + b*X'
	var sumX, sumY, sumXY, sumX2 float64
	for i := 0; i < n; i++ {
		xi := math.Log(x[i])
		yi := y[i]
		sumX += xi
		sumY += yi
		sumXY += xi * yi
		sumX2 += xi * xi
	}

	// Least squares solution
	meanX := sumX / float64(n)
	meanY := sumY / float64(n)
	b := (sumXY - float64(n)*meanX*meanY) / (sumX2 - float64(n)*meanX*meanX)
	a := meanY - b*meanX

	// Calculate R² and RMSE
	predicted := make([]float64, n)
	for i := 0; i < n; i++ {
		predicted[i] = a + b*math.Log(x[i])
	}
	r2 := calculateRSquared(y, predicted)
	rmse := calculateRMSE(y, predicted)

	formula := fmt.Sprintf("BPP = %.2f + %.2f * ln(PPM)", a, b)

	return FormulaFit{
		Type:         "logarithmic",
		Coefficients: []float64{a, b},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
	}
}

// fitPower fits BPP = a * PPM^b using least squares in log-log space.
func fitPower(x, y []float64) FormulaFit {
	n := len(x)
	if n == 0 {
		return FormulaFit{Type: "power"}
	}

	// Transform: ln(y) = ln(a) + b*ln(x)
	var sumX, sumY, sumXY, sumX2 float64
	for i := 0; i < n; i++ {
		xi := math.Log(x[i])
		yi := math.Log(y[i])
		sumX += xi
		sumY += yi
		sumXY += xi * yi
		sumX2 += xi * xi
	}

	meanX := sumX / float64(n)
	meanY := sumY / float64(n)
	b := (sumXY - float64(n)*meanX*meanY) / (sumX2 - float64(n)*meanX*meanX)
	logA := meanY - b*meanX
	a := math.Exp(logA)

	// Calculate R² and RMSE
	predicted := make([]float64, n)
	for i := 0; i < n; i++ {
		predicted[i] = a * math.Pow(x[i], b)
	}
	r2 := calculateRSquared(y, predicted)
	rmse := calculateRMSE(y, predicted)

	formula := fmt.Sprintf("BPP = %.2f * PPM^%.3f", a, b)

	return FormulaFit{
		Type:         "power",
		Coefficients: []float64{a, b},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
	}
}

// fitHyperbolic fits BPP = a + b / PPM using least squares.
func fitHyperbolic(x, y []float64) FormulaFit {
	n := len(x)
	if n == 0 {
		return FormulaFit{Type: "hyperbolic"}
	}

	// Transform: X' = 1/x, fit y = a + b*X'
	var sumX, sumY, sumXY, sumX2 float64
	for i := 0; i < n; i++ {
		xi := 1.0 / x[i]
		yi := y[i]
		sumX += xi
		sumY += yi
		sumXY += xi * yi
		sumX2 += xi * xi
	}

	meanX := sumX / float64(n)
	meanY := sumY / float64(n)
	b := (sumXY - float64(n)*meanX*meanY) / (sumX2 - float64(n)*meanX*meanX)
	a := meanY - b*meanX

	// Calculate R² and RMSE
	predicted := make([]float64, n)
	for i := 0; i < n; i++ {
		predicted[i] = a + b/x[i]
	}
	r2 := calculateRSquared(y, predicted)
	rmse := calculateRMSE(y, predicted)

	formula := fmt.Sprintf("BPP = %.2f + %.2f / PPM", a, b)

	return FormulaFit{
		Type:         "hyperbolic",
		Coefficients: []float64{a, b},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
	}
}

// calculateMin returns the minimum value in a slice.
func calculateMin(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	for _, v := range values {
		if v < min {
			min = v
		}
	}
	return min
}

// calculateMax returns the maximum value in a slice.
func calculateMax(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	max := values[0]
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	return max
}

// calculateMean returns the arithmetic mean of values.
func calculateMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// calculateMedian returns the median value.
func calculateMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	slices.Sort(sorted)

	n := len(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2.0
	}
	return sorted[n/2]
}

// calculateStdDev returns the standard deviation.
func calculateStdDev(values []float64, mean float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sumSq := 0.0
	for _, v := range values {
		diff := v - mean
		sumSq += diff * diff
	}
	return math.Sqrt(sumSq / float64(len(values)))
}

// calculateRSquared calculates R² coefficient of determination.
func calculateRSquared(observed, predicted []float64) float64 {
	if len(observed) == 0 {
		return 0
	}

	mean := calculateMean(observed)
	ssTot := 0.0 // Total sum of squares
	ssRes := 0.0 // Residual sum of squares

	for i := range observed {
		ssTot += (observed[i] - mean) * (observed[i] - mean)
		ssRes += (observed[i] - predicted[i]) * (observed[i] - predicted[i])
	}

	if ssTot == 0 {
		return 0
	}
	return 1.0 - (ssRes / ssTot)
}

// calculateRMSE calculates root mean square error.
func calculateRMSE(observed, predicted []float64) float64 {
	if len(observed) == 0 {
		return 0
	}

	sumSq := 0.0
	for i := range observed {
		diff := observed[i] - predicted[i]
		sumSq += diff * diff
	}
	return math.Sqrt(sumSq / float64(len(observed)))
}
