package regression

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/arloliu/mebo/blob"
)

// Analyze aggregates all blobs and returns a single best-fit model.
//
// This function combines all blobs into a single dataset and performs
// regression analysis to find the best-fit formula for blob size estimation.
//
// Parameters:
//   - blobs: Slice of numeric blobs to analyze
//
// Returns:
//   - *Result: Analysis result with best-fit model and all candidate models
//   - error: Analysis error if any
//
// Example:
//
//	blobs := []blob.NumericBlob{blob1, blob2, blob3}
//	result, err := regression.Analyze(blobs)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	bpp := result.BestFit.Estimator.Estimate(100.0) // Estimate BPP for 100 PPM
func Analyze(blobs []blob.NumericBlob) (*Result, error) {
	if len(blobs) == 0 {
		return nil, errors.New("no blobs provided")
	}

	// Extract data points from all blobs
	ppmValues, bppValues, err := extractDataPoints(blobs)
	if err != nil {
		return nil, fmt.Errorf("failed to extract data points: %w", err)
	}

	if len(ppmValues) == 0 {
		return nil, errors.New("no data points found in blobs")
	}

	// Perform regression analysis
	return performRegression(ppmValues, bppValues)
}

// AnalyzeEach analyzes each blob separately and returns per-blob models.
//
// This function analyzes each blob individually, useful for detecting
// formula drift over time or comparing different time periods.
//
// Parameters:
//   - blobs: Slice of numeric blobs to analyze
//
// Returns:
//   - []*Result: Slice of analysis results, one per blob
//   - error: Analysis error if any
//
// Example:
//
//	blobs := []blob.NumericBlob{blob1, blob2, blob3}
//	results, err := regression.AnalyzeEach(blobs)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for i, result := range results {
//	    fmt.Printf("Blob %d: %s\n", i, result.BestFit)
//	}
func AnalyzeEach(blobs []blob.NumericBlob) ([]*Result, error) {
	if len(blobs) == 0 {
		return nil, errors.New("no blobs provided")
	}

	results := make([]*Result, len(blobs))

	for i, b := range blobs {
		// For single blob, we need to create multiple data points
		// by sampling different PPM values from the blob's metrics
		ppmValues, bppValues, err := extractDataPointsFromBlob(b)
		if err != nil {
			return nil, fmt.Errorf("failed to extract data points from blob %d: %w", i, err)
		}

		if len(ppmValues) == 0 {
			return nil, fmt.Errorf("no data points found in blob %d", i)
		}

		// Perform regression analysis for this blob
		result, err := performRegression(ppmValues, bppValues)
		if err != nil {
			return nil, fmt.Errorf("failed to analyze blob %d: %w", i, err)
		}

		results[i] = result
	}

	return results, nil
}

// extractDataPoints extracts PPM and BPP values from all blobs.
//
// This function aggregates data points from multiple blobs to create a single
// dataset for regression analysis. It combines all metrics from all blobs
// to provide a comprehensive view of the compression efficiency relationship.
//
// Parameters:
//   - blobs: Slice of numeric blobs to analyze
//
// Returns:
//   - ppmValues: Points per metric values from all blobs
//   - bppValues: Bytes per point values from all blobs
//   - err: Error if data extraction fails
func extractDataPoints(blobs []blob.NumericBlob) (ppmValues, bppValues []float64, err error) {
	var allPPM, allBPP []float64

	for i, b := range blobs {
		ppmValues, bppValues, err := extractDataPointsFromBlob(b)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to extract data points from blob %d: %w", i, err)
		}

		allPPM = append(allPPM, ppmValues...)
		allBPP = append(allBPP, bppValues...)
	}

	return allPPM, allBPP, nil
}

// extractDataPointsFromBlob extracts PPM and BPP values from a single blob.
//
// This function creates multiple data points by sampling different PPM values
// from the blob's metrics to enable regression analysis. For each metric in
// the blob, it calculates the points per metric (PPM) and estimates the bytes
// per point (BPP) based on the blob's total size and point count.
//
// Parameters:
//   - b: Single numeric blob to analyze
//
// Returns:
//   - ppmValues: Points per metric for each metric in the blob
//   - bppValues: Estimated bytes per point for each metric
//   - err: Error if data extraction fails
//
// Note: BPP estimation uses a simplified approach based on total blob size
// and point count. For production use, consider storing actual blob sizes.
func extractDataPointsFromBlob(b blob.NumericBlob) (ppmValues, bppValues []float64, err error) {
	// Get blob size - we need to get the raw blob data size
	// Since we don't have direct access to the raw bytes, we'll estimate based on the blob structure
	// For now, we'll use a simplified approach by getting the total points and estimating size
	metricIDs := b.MetricIDs()
	if len(metricIDs) == 0 {
		return nil, nil, errors.New("no metrics found in blob")
	}

	// Calculate total points and estimate blob size
	totalPoints := 0
	for _, metricID := range metricIDs {
		totalPoints += b.Len(metricID)
	}

	if totalPoints == 0 {
		return nil, nil, errors.New("no data points found in blob")
	}

	// Estimate blob size based on the structure
	// This is a simplified estimation - in practice, you might want to store the actual blob size
	estimatedBlobSize := totalPoints * 16 // Rough estimate: 8 bytes timestamp + 8 bytes value

	// Calculate PPM and BPP for each metric
	var localPPM, localBPP []float64

	for _, metricID := range metricIDs {
		pointCount := b.Len(metricID)
		if pointCount > 0 {
			// Calculate PPM and BPP for this metric
			ppm := float64(pointCount)
			bpp := float64(estimatedBlobSize) / float64(totalPoints) // Share blob size across all metrics

			localPPM = append(localPPM, ppm)
			localBPP = append(localBPP, bpp)
		}
	}

	return localPPM, localBPP, nil
}

// performRegression performs regression analysis on the given data points.
//
// This function fits three different regression models (hyperbolic, logarithmic,
// and power) to the provided PPM-BPP data and selects the best-fit model based
// on the highest R² value. The function returns both the best model and all
// candidate models for comparison.
//
// Parameters:
//   - ppmValues: Points per metric values (independent variable)
//   - bppValues: Bytes per point values (dependent variable)
//
// Returns:
//   - *Result: Analysis result containing best-fit model and all candidates
//   - error: Error if regression analysis fails
//
// The function fits three models:
//   - Hyperbolic: BPP = a + b / PPM
//   - Logarithmic: BPP = a + b * ln(PPM)
//   - Power: BPP = a * PPM^b
//
// Models are ranked by R² (coefficient of determination) with the highest
// R² value selected as the best fit.
func performRegression(ppmValues, bppValues []float64) (*Result, error) {
	if len(ppmValues) != len(bppValues) {
		return nil, fmt.Errorf("mismatched data lengths: %d PPM vs %d BPP", len(ppmValues), len(bppValues))
	}

	if len(ppmValues) < 2 {
		return nil, fmt.Errorf("insufficient data points for regression: %d", len(ppmValues))
	}

	// Fit all five models
	models := []*Model{
		fitHyperbolic(ppmValues, bppValues),
		fitLogarithmic(ppmValues, bppValues),
		fitPower(ppmValues, bppValues),
		fitExponential(ppmValues, bppValues),
		fitPolynomial(ppmValues, bppValues),
	}

	// Sort models by R² (best first)
	slices.SortFunc(models, func(a, b *Model) int {
		if a.RSquared > b.RSquared {
			return -1
		}
		if a.RSquared < b.RSquared {
			return 1
		}

		return 0
	})

	return &Result{
		BestFit:   models[0],
		AllModels: models,
	}, nil
}

// fitHyperbolic fits the hyperbolic model: BPP = a + b / PPM
//
// This function performs linear regression on the transformed data where
// X' = 1/PPM and Y = BPP, fitting the model BPP = a + b * (1/PPM).
// The hyperbolic model is particularly effective for compression data where
// efficiency improves non-linearly with increasing points per metric.
//
// Parameters:
//   - x: PPM values (points per metric)
//   - y: BPP values (bytes per point)
//
// Returns:
//   - *Model: Fitted hyperbolic model with coefficients, R², RMSE, and estimator
//
// The model uses least squares regression on the transformed variables:
//   - X' = 1/x (inverse of PPM)
//   - Y = y (BPP values)
//   - Fits: Y = a + b*X'
func fitHyperbolic(x, y []float64) *Model {
	n := len(x)
	if n == 0 {
		return &Model{Type: ModelTypeHyperbolic, RSquared: 0, RMSE: 0, Formula: "BPP = 0 + 0 / PPM"}
	}

	// Transform: X' = 1/x, fit y = a + b*X'
	var sumX, sumY, sumXY, sumX2 float64
	for i := range n {
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
	for i := range n {
		predicted[i] = a + b/x[i]
	}
	r2 := calculateRSquared(y, predicted)
	rmse := calculateRMSE(y, predicted)

	formula := fmt.Sprintf("BPP = %.2f + %.2f / PPM", a, b)

	return &Model{
		Type:         ModelTypeHyperbolic,
		Coefficients: []float64{a, b},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    NewHyperbolicEstimator(a, b),
	}
}

// fitLogarithmic fits the logarithmic model: BPP = a + b * ln(PPM)
//
// This function performs linear regression on the transformed data where
// X' = ln(PPM) and Y = BPP, fitting the model BPP = a + b * ln(PPM).
// The logarithmic model captures diminishing returns in compression efficiency
// as the number of points per metric increases.
//
// Parameters:
//   - x: PPM values (points per metric)
//   - y: BPP values (bytes per point)
//
// Returns:
//   - *Model: Fitted logarithmic model with coefficients, R², RMSE, and estimator
//
// The model uses least squares regression on the transformed variables:
//   - X' = ln(x) (natural logarithm of PPM)
//   - Y = y (BPP values)
//   - Fits: Y = a + b*X'
func fitLogarithmic(x, y []float64) *Model {
	n := len(x)
	if n == 0 {
		return &Model{Type: ModelTypeLogarithmic, RSquared: 0, RMSE: 0, Formula: "BPP = 0 + 0 * ln(PPM)"}
	}

	// Transform: X' = ln(x), fit y = a + b*X'
	var sumX, sumY, sumXY, sumX2 float64
	for i := range n {
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

	return &Model{
		Type:         ModelTypeLogarithmic,
		Coefficients: []float64{a, b},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    NewLogarithmicEstimator(a, b),
	}
}

// fitPower fits the power model: BPP = a * PPM^b
//
// This function performs linear regression on the log-transformed data where
// X' = ln(PPM) and Y' = ln(BPP), fitting the model ln(BPP) = ln(a) + b * ln(PPM).
// The power model captures exponential relationships between compression efficiency
// and the number of points per metric.
//
// Parameters:
//   - x: PPM values (points per metric)
//   - y: BPP values (bytes per point)
//
// Returns:
//   - *Model: Fitted power model with coefficients, R², RMSE, and estimator
//
// The model uses least squares regression on the log-transformed variables:
//   - X' = ln(x) (natural logarithm of PPM)
//   - Y' = ln(y) (natural logarithm of BPP)
//   - Fits: Y' = ln(a) + b*X'
//   - Transforms back to: BPP = a * PPM^b
func fitPower(x, y []float64) *Model {
	n := len(x)
	if n == 0 {
		return &Model{Type: ModelTypePower, RSquared: 0, RMSE: 0, Formula: "BPP = 0 * PPM^0"}
	}

	// Transform: ln(y) = ln(a) + b*ln(x)
	var sumX, sumY, sumXY, sumX2 float64
	for i := range n {
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

	return &Model{
		Type:         ModelTypePower,
		Coefficients: []float64{a, b},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    NewPowerEstimator(a, b),
	}
}

// fitExponential fits the exponential model: BPP = a * e^(b * PPM)
//
// This function performs linear regression on the log-transformed data where
// X' = PPM and Y' = ln(BPP), fitting the model ln(BPP) = ln(a) + b * PPM.
// The exponential model captures exponential growth or decay in compression
// efficiency as the number of points per metric changes.
//
// Parameters:
//   - x: PPM values (points per metric)
//   - y: BPP values (bytes per point)
//
// Returns:
//   - *Model: Fitted exponential model with coefficients, R², RMSE, and estimator
//
// The model uses least squares regression on the log-transformed variables:
//   - X' = x (PPM values)
//   - Y' = ln(y) (natural logarithm of BPP)
//   - Fits: Y' = ln(a) + b*X'
//   - Transforms back to: BPP = a * e^(b * PPM)
func fitExponential(x, y []float64) *Model {
	n := len(x)
	if n == 0 {
		return &Model{Type: ModelTypeExponential, RSquared: 0, RMSE: 0, Formula: "BPP = 0 * e^(0 * PPM)"}
	}

	// Transform: ln(y) = ln(a) + b*x
	var sumX, sumY, sumXY, sumX2 float64
	for i := range n {
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

	return &Model{
		Type:         ModelTypeExponential,
		Coefficients: []float64{a, b},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    NewExponentialEstimator(a, b),
	}
}

// fitPolynomial fits the polynomial model: BPP = a + b*PPM + c*PPM²
//
// This function performs polynomial regression using the normal equations
// to fit a quadratic polynomial. The polynomial model captures non-linear
// relationships with curvature between compression efficiency and points per metric.
//
// Parameters:
//   - x: PPM values (points per metric)
//   - y: BPP values (bytes per point)
//
// Returns:
//   - *Model: Fitted polynomial model with coefficients, R², RMSE, and estimator
//
// The model uses least squares regression on the polynomial variables:
//   - X₁ = x (PPM values)
//   - X₂ = x² (squared PPM values)
//   - Y = y (BPP values)
//   - Fits: Y = a + b*X₁ + c*X₂
func fitPolynomial(x, y []float64) *Model {
	n := len(x)
	if n == 0 {
		return &Model{
			Type:         ModelTypePolynomial,
			Coefficients: []float64{0, 0, 0},
			RSquared:     0,
			RMSE:         0,
			Formula:      "BPP = 0 + 0*PPM + 0*PPM²",
			Estimator:    NewPolynomialEstimator(0, 0, 0),
		}
	}

	// For polynomial regression, we need at least 3 points for a quadratic fit
	if n < 3 {
		// Fall back to linear regression if insufficient data
		return fitLinear(x, y)
	}

	// Build the normal equations for polynomial regression
	// We solve: [n    Σx   Σx²] [a]   [Σy]
	//          [Σx   Σx²  Σx³] [b] = [Σxy]
	//          [Σx²  Σx³  Σx⁴] [c]   [Σx²y]
	var sumX, sumX2, sumX3, sumX4, sumY, sumXY, sumX2Y float64
	for i := range n {
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
	// Matrix: [n    sumX  sumX2]
	//         [sumX sumX2 sumX3]
	//         [sumX2 sumX3 sumX4]
	det := float64(n)*sumX2*sumX4 + sumX*sumX3*sumX2 + sumX2*sumX*sumX3 -
		(sumX2*sumX2*float64(n) + sumX*sumX*sumX4 + sumX3*sumX3*sumX2)

	if math.Abs(det) < 1e-10 {
		// Matrix is singular, fall back to linear regression
		return fitLinear(x, y)
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

	// Optimized R² and RMSE calculation in single pass
	r2, rmse := calculateStatsOptimized(x, y, a, b, c)

	formula := fmt.Sprintf("BPP = %.2f + %.2f*PPM + %.2f*PPM²", a, b, c)

	return &Model{
		Type:         ModelTypePolynomial,
		Coefficients: []float64{a, b, c},
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    NewPolynomialEstimator(a, b, c),
	}
}

// fitLinear performs linear regression as a fallback for polynomial regression.
// This is used when there's insufficient data for polynomial fitting.
func fitLinear(x, y []float64) *Model {
	n := len(x)
	if n == 0 {
		return &Model{Type: ModelTypePolynomial, RSquared: 0, RMSE: 0, Formula: "BPP = 0 + 0*PPM"}
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

	return &Model{
		Type:         ModelTypePolynomial,
		Coefficients: []float64{a, b, 0}, // c=0 for linear
		RSquared:     r2,
		RMSE:         rmse,
		Formula:      formula,
		Estimator:    NewPolynomialEstimator(a, b, 0),
	}
}

// calculateRSquared calculates the coefficient of determination (R²).
//
// R² measures the proportion of variance in the dependent variable (BPP)
// that is predictable from the independent variable (PPM). Values range from
// 0 to 1, where 1 indicates perfect fit and 0 indicates no linear relationship.
//
// Formula: R² = 1 - (SS_res / SS_tot)
//   - SS_res: Sum of squares of residuals (observed - predicted)²
//   - SS_tot: Total sum of squares (observed - mean)²
//
// Parameters:
//   - observed: Actual BPP values from the data
//   - predicted: BPP values predicted by the model
//
// Returns:
//   - float64: R² value between 0 and 1 (higher is better)
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

// calculateRMSE calculates the root mean square error.
//
// RMSE measures the standard deviation of the residuals (prediction errors).
// It provides an estimate of how far the predicted values deviate from the
// observed values on average. Lower RMSE values indicate better model fit.
//
// Formula: RMSE = √(Σ(observed - predicted)² / n)
//
// Parameters:
//   - observed: Actual BPP values from the data
//   - predicted: BPP values predicted by the model
//
// Returns:
//   - float64: RMSE value (lower is better, same units as BPP)
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

// calculateMean calculates the arithmetic mean.
//
// This function computes the average value of a slice of floating-point numbers.
// It is used internally by other statistical functions for calculating R².
//
// Parameters:
//   - values: Slice of floating-point numbers
//
// Returns:
//   - float64: Arithmetic mean of the values (0 if slice is empty)
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

// calculateStatsOptimized calculates R² and RMSE in a single optimized pass.
//
// This function performs both R² and RMSE calculations in a single pass over the data,
// reducing memory allocations and improving performance for polynomial regression.
//
// Parameters:
//   - x: Input values (PPM)
//   - y: Observed values (BPP)
//   - a, b, c: Polynomial coefficients
//
// Returns:
//   - r2: Coefficient of determination
//   - rmse: Root mean square error
func calculateStatsOptimized(x, y []float64, a, b, c float64) (r2, rmse float64) {
	n := len(x)
	if n == 0 {
		return 0, 0
	}

	// Calculate mean of observed values
	meanY := 0.0
	for _, yi := range y {
		meanY += yi
	}
	meanY /= float64(n)

	// Single-pass calculation of R² and RMSE
	ssTot := 0.0 // Total sum of squares
	ssRes := 0.0 // Residual sum of squares
	sumSq := 0.0 // Sum of squared residuals for RMSE

	for i := 0; i < n; i++ {
		xi := x[i]
		yi := y[i]

		// Calculate predicted value: a + b*x + c*x²
		predicted := a + b*xi + c*xi*xi

		// Accumulate for R²
		ssTot += (yi - meanY) * (yi - meanY)
		residual := yi - predicted
		ssRes += residual * residual

		// Accumulate for RMSE
		sumSq += residual * residual
	}

	// Calculate R²
	if ssTot == 0 {
		r2 = 0
	} else {
		r2 = 1.0 - (ssRes / ssTot)
	}

	// Calculate RMSE
	rmse = math.Sqrt(sumSq / float64(n))

	return r2, rmse
}
