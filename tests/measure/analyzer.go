package main

import (
	"fmt"
	"math"
	"slices"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/regression"
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
// This is a compatibility wrapper around the regression package's Model.
type FormulaFit struct {
	Type         string    // Formula type: "logarithmic", "power", "hyperbolic", "exponential", "polynomial"
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
//  2. Uses the regression package to fit multiple models (hyperbolic, logarithmic, power, exponential, polynomial)
//  3. Selects the best fit based on R² value
//  4. Converts regression results to FormulaFit format for compatibility
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

	// Use the regression package to perform analysis
	// Create blob sets from our measurement data and use regression.Analyze()
	regressionResult := performRegressionAnalysisWithBlobs(results)

	// Convert regression results to FormulaFit format
	var allFits []FormulaFit
	for _, model := range regressionResult.AllModels {
		fit := convertModelToFormulaFit(model)
		allFits = append(allFits, fit)
	}

	// The best fit is already selected by the regression package
	bestFit := convertModelToFormulaFit(regressionResult.BestFit)

	return AnalysisResult{
		Measurements: results,
		Statistics:   stats,
		BestFit:      bestFit,
		AllFits:      allFits,
	}
}

// performRegressionAnalysisWithBlobs creates numeric blobs from measurement results and uses regression.Analyze().
func performRegressionAnalysisWithBlobs(results []MeasurementResult) *regression.Result {
	// Extract numeric blobs from our measurement results
	// Each measurement result now contains a numeric blob
	var numericBlobs []blob.NumericBlob

	for _, result := range results {
		// Add the numeric blob from the measurement result
		numericBlobs = append(numericBlobs, result.NumericBlob)
	}

	if len(numericBlobs) == 0 {
		// No numeric blobs found, fall back to direct analysis
		// This means the numeric blobs are empty or not being created properly
		return performDirectRegressionAnalysis(results)
	}

	regressionResult, err := regression.Analyze(numericBlobs)
	if err != nil {
		// Fall back to direct analysis if regression.Analyze fails
		return performDirectRegressionAnalysis(results)
	}

	return regressionResult
}

// performDirectRegressionAnalysis performs direct regression analysis as a fallback.
func performDirectRegressionAnalysis(results []MeasurementResult) *regression.Result {
	// Extract BPP and PPM values
	bppValues := make([]float64, len(results))
	ppmValues := make([]float64, len(results))
	for i, r := range results {
		bppValues[i] = r.BytesPerPoint
		ppmValues[i] = float64(r.PointsPerMetric)
	}

	// Create models for all 5 types
	models := []*regression.Model{
		fitHyperbolicModel(ppmValues, bppValues),
		fitLogarithmicModel(ppmValues, bppValues),
		fitPowerModel(ppmValues, bppValues),
		fitExponentialModel(ppmValues, bppValues),
		fitPolynomialModel(ppmValues, bppValues),
	}

	// Sort models by R² (best first)
	slices.SortFunc(models, func(a, b *regression.Model) int {
		if a.RSquared > b.RSquared {
			return -1
		}
		if a.RSquared < b.RSquared {
			return 1
		}
		return 0
	})

	return &regression.Result{
		BestFit:   models[0],
		AllModels: models,
	}
}

// convertModelToFormulaFit converts a regression.Model to FormulaFit for compatibility.
func convertModelToFormulaFit(model *regression.Model) FormulaFit {
	if model == nil {
		return FormulaFit{}
	}

	return FormulaFit{
		Type:         model.Type.String(),
		Coefficients: model.Coefficients,
		RSquared:     model.RSquared,
		RMSE:         model.RMSE,
		Formula:      model.Formula,
	}
}

// fitHyperbolicModel fits the hyperbolic model using regression package logic.
func fitHyperbolicModel(x, y []float64) *regression.Model {
	n := len(x)
	if n == 0 {
		return &regression.Model{Type: regression.ModelTypeHyperbolic, RSquared: 0, RMSE: 0, Formula: "BPP = 0 + 0 / PPM"}
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

	return &regression.Model{
		Type:         regression.ModelTypeHyperbolic,
		Coefficients: []float64{a, b},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    regression.NewHyperbolicEstimator(a, b),
	}
}

// fitLogarithmicModel fits the logarithmic model using regression package logic.
func fitLogarithmicModel(x, y []float64) *regression.Model {
	n := len(x)
	if n == 0 {
		return &regression.Model{Type: regression.ModelTypeLogarithmic, RSquared: 0, RMSE: 0, Formula: "BPP = 0 + 0 * ln(PPM)"}
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

	return &regression.Model{
		Type:         regression.ModelTypeLogarithmic,
		Coefficients: []float64{a, b},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    regression.NewLogarithmicEstimator(a, b),
	}
}

// fitPowerModel fits the power model using regression package logic.
func fitPowerModel(x, y []float64) *regression.Model {
	n := len(x)
	if n == 0 {
		return &regression.Model{Type: regression.ModelTypePower, RSquared: 0, RMSE: 0, Formula: "BPP = 0 * PPM^0"}
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

	return &regression.Model{
		Type:         regression.ModelTypePower,
		Coefficients: []float64{a, b},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    regression.NewPowerEstimator(a, b),
	}
}

// fitExponentialModel fits the exponential model using regression package logic.
func fitExponentialModel(x, y []float64) *regression.Model {
	n := len(x)
	if n == 0 {
		return &regression.Model{Type: regression.ModelTypeExponential, RSquared: 0, RMSE: 0, Formula: "BPP = 0 * e^(0 * PPM)"}
	}

	// Transform: ln(y) = ln(a) + b*x
	var sumX, sumY, sumXY, sumX2 float64
	for i := 0; i < n; i++ {
		xi := x[i]
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
		predicted[i] = a * math.Exp(b*x[i])
	}
	r2 := calculateRSquared(y, predicted)
	rmse := calculateRMSE(y, predicted)

	formula := fmt.Sprintf("BPP = %.2f * e^(%.3f * PPM)", a, b)

	return &regression.Model{
		Type:         regression.ModelTypeExponential,
		Coefficients: []float64{a, b},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    regression.NewExponentialEstimator(a, b),
	}
}

// fitPolynomialModel fits the polynomial model using regression package logic.
func fitPolynomialModel(x, y []float64) *regression.Model {
	n := len(x)
	if n == 0 {
		return &regression.Model{Type: regression.ModelTypePolynomial, RSquared: 0, RMSE: 0, Formula: "BPP = 0 + 0*PPM + 0*PPM²"}
	}

	// For polynomial regression, we need at least 3 points for a quadratic fit
	if n < 3 {
		// Fall back to linear regression
		return fitLinearModel(x, y)
	}

	// Build the normal equations for polynomial regression
	var sumX, sumX2, sumX3, sumX4, sumY, sumXY, sumX2Y float64
	for i := 0; i < n; i++ {
		xi := x[i]
		xi2 := xi * xi
		xi3 := xi2 * xi
		xi4 := xi3 * xi
		yi := y[i]

		sumX += xi
		sumX2 += xi2
		sumX3 += xi3
		sumX4 += xi4
		sumY += yi
		sumXY += xi * yi
		sumX2Y += xi2 * yi
	}

	// Solve the 3x3 system using Cramer's rule
	det := float64(n)*sumX2*sumX4 + sumX*sumX3*sumX2 + sumX2*sumX*sumX3 -
		(sumX2*sumX2*float64(n) + sumX*sumX*sumX4 + sumX3*sumX3*sumX2)

	if math.Abs(det) < 1e-10 {
		// Matrix is singular, fall back to linear regression
		return fitLinearModel(x, y)
	}

	// Calculate coefficients using Cramer's rule
	detA := sumY*sumX2*sumX4 + sumXY*sumX3*sumX2 + sumX2Y*sumX*sumX3 -
		(sumX2Y*sumX2*sumY + sumXY*sumX*sumX4 + sumY*sumX3*sumX3)
	a := detA / det

	detB := float64(n)*sumXY*sumX4 + sumY*sumX3*sumX2 + sumX2*sumX2Y*sumX -
		(sumX2*sumXY*float64(n) + sumY*sumX*sumX4 + sumX2Y*sumX3*sumX2)
	b := detB / det

	detC := float64(n)*sumX2*sumX2Y + sumX*sumXY*sumX2 + sumY*sumX*sumX3 -
		(sumX2*sumX2*sumY + sumX*sumXY*sumX2 + sumY*sumX3*sumX2)
	c := detC / det

	// Calculate R² and RMSE
	predicted := make([]float64, n)
	for i := 0; i < n; i++ {
		predicted[i] = a + b*x[i] + c*x[i]*x[i]
	}
	r2 := calculateRSquared(y, predicted)
	rmse := calculateRMSE(y, predicted)

	formula := fmt.Sprintf("BPP = %.2f + %.2f*PPM + %.2f*PPM²", a, b, c)

	return &regression.Model{
		Type:         regression.ModelTypePolynomial,
		Coefficients: []float64{a, b, c},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    regression.NewPolynomialEstimator(a, b, c),
	}
}

// fitLinearModel performs linear regression as a fallback for polynomial regression.
func fitLinearModel(x, y []float64) *regression.Model {
	n := len(x)
	if n == 0 {
		return &regression.Model{Type: regression.ModelTypePolynomial, RSquared: 0, RMSE: 0, Formula: "BPP = 0 + 0*PPM"}
	}

	// Simple linear regression: y = a + b*x
	var sumX, sumY, sumXY, sumX2 float64
	for i := 0; i < n; i++ {
		xi := x[i]
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
		predicted[i] = a + b*x[i]
	}
	r2 := calculateRSquared(y, predicted)
	rmse := calculateRMSE(y, predicted)

	formula := fmt.Sprintf("BPP = %.2f + %.2f*PPM", a, b)

	return &regression.Model{
		Type:         regression.ModelTypePolynomial,
		Coefficients: []float64{a, b, 0}, // c=0 for linear
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    regression.NewPolynomialEstimator(a, b, 0),
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
