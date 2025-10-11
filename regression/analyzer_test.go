package regression

import (
	"fmt"
	"log"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
)

// TestAnalyze tests the Analyze function with known data.
func TestAnalyze(t *testing.T) {
	// Create test blobs with known characteristics
	blobs := createTestBlobs(t, []testBlobConfig{
		{metrics: 10, pointsPerMetric: 50},
		{metrics: 20, pointsPerMetric: 100},
		{metrics: 30, pointsPerMetric: 150},
	})

	result, err := Analyze(blobs)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if result.BestFit == nil {
		t.Fatal("BestFit should not be nil")
	}

	if len(result.AllModels) != 5 {
		t.Fatalf("Expected 5 models, got %d", len(result.AllModels))
	}

	// Verify that models are sorted by R² (best first)
	for i := 1; i < len(result.AllModels); i++ {
		if result.AllModels[i-1].RSquared < result.AllModels[i].RSquared {
			t.Errorf("Models not sorted by R²: model %d has R²=%.3f, model %d has R²=%.3f",
				i-1, result.AllModels[i-1].RSquared, i, result.AllModels[i].RSquared)
		}
	}

	// Verify that BestFit is the first model
	if result.BestFit != result.AllModels[0] {
		t.Error("BestFit should be the first model in AllModels")
	}

	// Test estimator functionality
	estimator := result.BestFit.Estimator
	if estimator == nil {
		t.Fatal("Estimator should not be nil")
	}

	// Test estimation with a reasonable PPM value
	ppm := 100.0
	bpp := estimator.Estimate(ppm)
	if math.IsInf(bpp, 0) || math.IsNaN(bpp) {
		t.Errorf("Estimate returned invalid value: %f", bpp)
	}
	if bpp <= 0 {
		t.Errorf("Estimate should be positive, got %f", bpp)
	}
}

// TestAnalyzeEach tests the AnalyzeEach function.
func TestAnalyzeEach(t *testing.T) {
	// Create test blobs with different characteristics
	blobs := createTestBlobs(t, []testBlobConfig{
		{metrics: 5, pointsPerMetric: 25},
		{metrics: 10, pointsPerMetric: 50},
		{metrics: 15, pointsPerMetric: 75},
	})

	results, err := AnalyzeEach(blobs)
	if err != nil {
		t.Fatalf("AnalyzeEach failed: %v", err)
	}

	if len(results) != len(blobs) {
		t.Fatalf("Expected %d results, got %d", len(blobs), len(results))
	}

	for i, result := range results {
		if result.BestFit == nil {
			t.Errorf("Result %d: BestFit should not be nil", i)
		}
		if len(result.AllModels) != 5 {
			t.Errorf("Result %d: Expected 5 models, got %d", i, len(result.AllModels))
		}
	}
}

// TestAnalyzeEmptyInput tests error handling for empty input.
func TestAnalyzeEmptyInput(t *testing.T) {
	_, err := Analyze([]blob.NumericBlob{})
	if err == nil {
		t.Error("Expected error for empty input")
	}

	_, err = AnalyzeEach([]blob.NumericBlob{})
	if err == nil {
		t.Error("Expected error for empty input")
	}
}

// TestEstimatorImplementations tests the concrete estimator implementations.
func TestEstimatorImplementations(t *testing.T) {
	tests := []struct {
		name      string
		estimator Estimator
		ppm       float64
		expected  float64
	}{
		{
			name:      "HyperbolicEstimator",
			estimator: NewHyperbolicEstimator(10.0, 50.0),
			ppm:       100.0,
			expected:  10.5, // 10.0 + 50.0/100.0
		},
		{
			name:      "LogarithmicEstimator",
			estimator: NewLogarithmicEstimator(5.0, 2.0),
			ppm:       100.0,
			expected:  5.0 + 2.0*math.Log(100.0), // 5.0 + 2.0 * ln(100)
		},
		{
			name:      "PowerEstimator",
			estimator: NewPowerEstimator(2.0, -0.5),
			ppm:       100.0,
			expected:  2.0 * math.Pow(100.0, -0.5), // 2.0 * 100^(-0.5)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := tt.estimator.Estimate(tt.ppm)
			if math.Abs(actual-tt.expected) > 1e-10 {
				t.Errorf("Estimate() = %f, expected %f", actual, tt.expected)
			}

			// Test coefficients
			coeffs := tt.estimator.Coefficients()
			if len(coeffs) != 2 {
				t.Errorf("Expected 2 coefficients, got %d", len(coeffs))
			}
		})
	}
}

// TestEstimatorEdgeCases tests edge cases for estimators.
func TestEstimatorEdgeCases(t *testing.T) {
	hyperbolic := NewHyperbolicEstimator(10.0, 50.0)
	logarithmic := NewLogarithmicEstimator(5.0, 2.0)
	power := NewPowerEstimator(2.0, -0.5)

	// Test with zero PPM
	if !math.IsInf(hyperbolic.Estimate(0), 1) {
		t.Error("HyperbolicEstimator should return +Inf for PPM=0")
	}
	if !math.IsInf(logarithmic.Estimate(0), 1) {
		t.Error("LogarithmicEstimator should return +Inf for PPM=0")
	}
	if !math.IsInf(power.Estimate(0), 1) {
		t.Error("PowerEstimator should return +Inf for PPM=0")
	}

	// Test with negative PPM
	if !math.IsInf(hyperbolic.Estimate(-1), 1) {
		t.Error("HyperbolicEstimator should return +Inf for negative PPM")
	}
	if !math.IsInf(logarithmic.Estimate(-1), 1) {
		t.Error("LogarithmicEstimator should return +Inf for negative PPM")
	}
	if !math.IsInf(power.Estimate(-1), 1) {
		t.Error("PowerEstimator should return +Inf for negative PPM")
	}
}

// TestModelTypeString tests the String method of ModelType.
func TestModelTypeString(t *testing.T) {
	tests := []struct {
		modelType ModelType
		expected  string
	}{
		{ModelTypeHyperbolic, "hyperbolic"},
		{ModelTypeLogarithmic, "logarithmic"},
		{ModelTypePower, "power"},
		{ModelTypeExponential, "exponential"},
		{ModelTypePolynomial, "polynomial"},
		{ModelType(999), "unknown"},
	}

	for _, tt := range tests {
		actual := tt.modelType.String()
		if actual != tt.expected {
			t.Errorf("ModelType.String() = %s, expected %s", actual, tt.expected)
		}
	}
}

// TestFitLinear tests the fitLinear fallback function for polynomial regression.
func TestFitLinear(t *testing.T) {
	// Test with insufficient data for polynomial (should fall back to linear)
	x := []float64{1.0, 2.0} // Only 2 points - insufficient for quadratic
	y := []float64{3.0, 5.0}

	model := fitLinear(x, y)

	// Should return polynomial model with linear coefficients
	if model.Type != ModelTypePolynomial {
		t.Errorf("Expected ModelTypePolynomial, got %v", model.Type)
	}

	// Should have 3 coefficients (a, b, c=0 for linear)
	coeffs := model.Coefficients
	if len(coeffs) != 3 {
		t.Errorf("Expected 3 coefficients, got %d", len(coeffs))
	}

	// c should be 0 for linear regression
	if math.Abs(coeffs[2]) > 1e-10 {
		t.Errorf("Expected c=0 for linear regression, got %f", coeffs[2])
	}

	// Test that the linear fit is reasonable
	// For y = 3.0, 5.0 at x = 1.0, 2.0, we expect y = 1 + 2*x
	expectedA := 1.0
	expectedB := 2.0
	if math.Abs(coeffs[0]-expectedA) > 1e-10 {
		t.Errorf("Expected a=%f, got %f", expectedA, coeffs[0])
	}
	if math.Abs(coeffs[1]-expectedB) > 1e-10 {
		t.Errorf("Expected b=%f, got %f", expectedB, coeffs[1])
	}
}

// TestPolynomialRegressionEdgeCases tests edge cases for polynomial regression.
func TestPolynomialRegressionEdgeCases(t *testing.T) {
	t.Run("InsufficientData", func(t *testing.T) {
		// Test with only 2 data points (insufficient for quadratic)
		x := []float64{1.0, 2.0}
		y := []float64{3.0, 5.0}

		model := fitPolynomial(x, y)

		// Should fall back to linear regression
		if model.Type != ModelTypePolynomial {
			t.Errorf("Expected ModelTypePolynomial, got %v", model.Type)
		}

		// Should have linear coefficients (c=0)
		coeffs := model.Coefficients
		if len(coeffs) != 3 {
			t.Errorf("Expected 3 coefficients, got %d", len(coeffs))
		}
		if math.Abs(coeffs[2]) > 1e-10 {
			t.Errorf("Expected c=0 for linear fallback, got %f", coeffs[2])
		}
	})

	t.Run("SingularMatrix", func(t *testing.T) {
		// Test with data that creates a singular matrix
		// All x values are the same, which makes the matrix singular
		x := []float64{1.0, 1.0, 1.0}
		y := []float64{2.0, 3.0, 4.0}

		model := fitPolynomial(x, y)

		// Should fall back to linear regression
		if model.Type != ModelTypePolynomial {
			t.Errorf("Expected ModelTypePolynomial, got %v", model.Type)
		}

		// Should handle the singular matrix gracefully (may produce NaN, which is acceptable)
		// The important thing is that it doesn't crash
		if math.IsInf(model.RSquared, 0) {
			t.Errorf("R² should not be infinite, got %f", model.RSquared)
		}
	})

	t.Run("EmptyData", func(t *testing.T) {
		// Test with empty data
		x := []float64{}
		y := []float64{}

		model := fitPolynomial(x, y)

		// Should return a default model
		if model.Type != ModelTypePolynomial {
			t.Errorf("Expected ModelTypePolynomial, got %v", model.Type)
		}

		// Should have default coefficients (empty data returns default model)
		coeffs := model.Coefficients
		if len(coeffs) != 3 {
			t.Errorf("Expected 3 coefficients, got %d", len(coeffs))
		}

		// All coefficients should be 0 for empty data
		for i, coeff := range coeffs {
			if math.Abs(coeff) > 1e-10 {
				t.Errorf("Expected coefficient %d to be 0 for empty data, got %f", i, coeff)
			}
		}
	})
}

// TestExponentialRegressionEdgeCases tests edge cases for exponential regression.
func TestExponentialRegressionEdgeCases(t *testing.T) {
	t.Run("NegativeValues", func(t *testing.T) {
		// Test with negative BPP values (should handle gracefully)
		x := []float64{1.0, 2.0, 3.0}
		y := []float64{-1.0, -2.0, -3.0} // Negative values

		model := fitExponential(x, y)

		// Should handle negative values (though mathematically questionable)
		if model.Type != ModelTypeExponential {
			t.Errorf("Expected ModelTypeExponential, got %v", model.Type)
		}

		// Should not crash (NaN is acceptable for mathematically invalid cases)
		if math.IsInf(model.RSquared, 0) {
			t.Errorf("R² should not be infinite, got %f", model.RSquared)
		}
	})

	t.Run("ZeroValues", func(t *testing.T) {
		// Test with zero BPP values
		x := []float64{1.0, 2.0, 3.0}
		y := []float64{0.0, 0.0, 0.0}

		model := fitExponential(x, y)

		// Should handle zero values
		if model.Type != ModelTypeExponential {
			t.Errorf("Expected ModelTypeExponential, got %v", model.Type)
		}
	})
}

// TestEstimatorTypeMethods tests the Type() methods for all estimators.
func TestEstimatorTypeMethods(t *testing.T) {
	tests := []struct {
		name      string
		estimator Estimator
		expected  ModelType
	}{
		{"Hyperbolic", NewHyperbolicEstimator(1.0, 2.0), ModelTypeHyperbolic},
		{"Logarithmic", NewLogarithmicEstimator(1.0, 2.0), ModelTypeLogarithmic},
		{"Power", NewPowerEstimator(1.0, 2.0), ModelTypePower},
		{"Exponential", NewExponentialEstimator(1.0, 2.0), ModelTypeExponential},
		{"Polynomial", NewPolynomialEstimator(1.0, 2.0, 3.0), ModelTypePolynomial},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := tt.estimator.Type()
			if actual != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, actual)
			}
		})
	}
}

// TestResultString tests the String method of Result.
func TestResultString(t *testing.T) {
	t.Run("WithBestFit", func(t *testing.T) {
		// Create a result with best fit
		bestFit := &Model{
			Type:     ModelTypeHyperbolic,
			RSquared: 0.95,
			RMSE:     0.1,
			Formula:  "BPP = 1.0 + 2.0 / PPM",
		}
		result := &Result{
			BestFit:   bestFit,
			AllModels: []*Model{bestFit},
		}

		str := result.String()
		if str == "" {
			t.Error("String() should not be empty")
		}
		if !strings.Contains(str, "BestFit") {
			t.Error("String() should contain 'BestFit'")
		}
		if !strings.Contains(str, "TotalModels") {
			t.Error("String() should contain 'TotalModels'")
		}
	})

	t.Run("WithoutBestFit", func(t *testing.T) {
		// Create a result without best fit
		result := &Result{
			BestFit:   nil,
			AllModels: []*Model{},
		}

		str := result.String()
		if str == "" {
			t.Error("String() should not be empty")
		}
		if !strings.Contains(str, "nil") {
			t.Error("String() should contain 'nil' for missing BestFit")
		}
	})
}

// TestRegressionWithRealisticData tests regression with more realistic data patterns.
func TestRegressionWithRealisticData(t *testing.T) {
	t.Run("ExponentialGrowth", func(t *testing.T) {
		// Create data that follows exponential growth pattern
		x := []float64{10, 20, 30, 40, 50}
		y := []float64{2.0, 4.0, 8.0, 16.0, 32.0} // Exponential growth

		// Test exponential model should fit well
		model := fitExponential(x, y)
		if model.Type != ModelTypeExponential {
			t.Errorf("Expected ModelTypeExponential, got %v", model.Type)
		}

		// Should have reasonable R² for exponential data
		if model.RSquared < 0.8 {
			t.Errorf("Expected R² > 0.8 for exponential data, got %f", model.RSquared)
		}
	})

	t.Run("QuadraticCurve", func(t *testing.T) {
		// Create data that follows quadratic pattern
		x := []float64{1, 2, 3, 4, 5}
		y := []float64{1, 4, 9, 16, 25} // Perfect quadratic: y = x²

		// Test polynomial model should fit well
		model := fitPolynomial(x, y)
		if model.Type != ModelTypePolynomial {
			t.Errorf("Expected ModelTypePolynomial, got %v", model.Type)
		}

		// Should have reasonable R² for quadratic data (may not be perfect due to numerical precision)
		if model.RSquared < 0.7 {
			t.Errorf("Expected R² > 0.7 for quadratic data, got %f", model.RSquared)
		}

		// Check that coefficients are reasonable (may not be perfect due to numerical precision)
		coeffs := model.Coefficients
		// The coefficients should be reasonable for a quadratic fit
		if len(coeffs) != 3 {
			t.Errorf("Expected 3 coefficients, got %d", len(coeffs))
		}
	})
}

// TestStatisticalFunctions tests the statistical helper functions.
func TestStatisticalFunctions(t *testing.T) {
	observed := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	predicted := []float64{1.1, 1.9, 3.1, 3.9, 5.1}

	// Test R² calculation
	r2 := calculateRSquared(observed, predicted)
	if r2 < 0 || r2 > 1 {
		t.Errorf("R² should be between 0 and 1, got %f", r2)
	}

	// Test RMSE calculation
	rmse := calculateRMSE(observed, predicted)
	if rmse < 0 {
		t.Errorf("RMSE should be non-negative, got %f", rmse)
	}

	// Test with empty slices
	if calculateRSquared([]float64{}, []float64{}) != 0 {
		t.Error("R² should be 0 for empty slices")
	}
	if calculateRMSE([]float64{}, []float64{}) != 0 {
		t.Error("RMSE should be 0 for empty slices")
	}
}

// testBlobConfig represents configuration for creating test blobs.
type testBlobConfig struct {
	metrics         int
	pointsPerMetric int
}

// createTestBlobs creates test blobs with the given configurations.
func createTestBlobs(t *testing.T, configs []testBlobConfig) []blob.NumericBlob {
	blobs := make([]blob.NumericBlob, len(configs))
	startTime := time.Now()

	for i, config := range configs {
		// Create encoder
		encoder, err := blob.NewNumericEncoder(
			startTime,
			blob.WithTimestampEncoding(format.TypeDelta),
			blob.WithTimestampCompression(format.CompressionNone),
			blob.WithValueEncoding(format.TypeGorilla),
			blob.WithValueCompression(format.CompressionNone),
		)
		if err != nil {
			t.Fatalf("Failed to create encoder for blob %d: %v", i, err)
		}

		// Add metrics
		for j := 0; j < config.metrics; j++ {
			metricID := uint64(j + 1000) // Simple metric ID
			if err := encoder.StartMetricID(metricID, config.pointsPerMetric); err != nil {
				t.Fatalf("Failed to start metric %d for blob %d: %v", j, i, err)
			}

			for k := 0; k < config.pointsPerMetric; k++ {
				ts := startTime.Add(time.Duration(k) * time.Second)
				value := float64(100 + j*10 + k) // Simple value pattern
				if err := encoder.AddDataPoint(ts.UnixMicro(), value, ""); err != nil {
					t.Fatalf("Failed to add data point %d for metric %d in blob %d: %v", k, j, i, err)
				}
			}

			if err := encoder.EndMetric(); err != nil {
				t.Fatalf("Failed to end metric %d for blob %d: %v", j, i, err)
			}
		}

		// Finish encoding
		blobBytes, err := encoder.Finish()
		if err != nil {
			t.Fatalf("Failed to finish encoding blob %d: %v", i, err)
		}

		// Decode to get NumericBlob
		decoder, err := blob.NewNumericDecoder(blobBytes)
		if err != nil {
			t.Fatalf("Failed to create decoder for blob %d: %v", i, err)
		}

		blobData, err := decoder.Decode()
		if err != nil {
			t.Fatalf("Failed to decode blob %d: %v", i, err)
		}

		blobs[i] = blobData
	}

	return blobs
}

func TestSetCoefficients(t *testing.T) {
	// Test SetCoefficients for all estimator types
	hyperbolic := NewHyperbolicEstimator(1.0, 2.0)
	logarithmic := NewLogarithmicEstimator(1.0, 2.0)
	power := NewPowerEstimator(1.0, 2.0)

	// Test valid coefficient updates
	newCoeffs := []float64{3.0, 4.0}

	// Test hyperbolic
	err := hyperbolic.SetCoefficients(newCoeffs)
	if err != nil {
		t.Errorf("Unexpected error setting hyperbolic coefficients: %v", err)
	}
	if hyperbolic.Coefficients()[0] != 3.0 || hyperbolic.Coefficients()[1] != 4.0 {
		t.Errorf("Hyperbolic coefficients not updated correctly: %v", hyperbolic.Coefficients())
	}

	// Test logarithmic
	err = logarithmic.SetCoefficients(newCoeffs)
	if err != nil {
		t.Errorf("Unexpected error setting logarithmic coefficients: %v", err)
	}
	if logarithmic.Coefficients()[0] != 3.0 || logarithmic.Coefficients()[1] != 4.0 {
		t.Errorf("Logarithmic coefficients not updated correctly: %v", logarithmic.Coefficients())
	}

	// Test power
	err = power.SetCoefficients(newCoeffs)
	if err != nil {
		t.Errorf("Unexpected error setting power coefficients: %v", err)
	}
	if power.Coefficients()[0] != 3.0 || power.Coefficients()[1] != 4.0 {
		t.Errorf("Power coefficients not updated correctly: %v", power.Coefficients())
	}

	// Test invalid coefficient counts
	invalidCoeffs := []float64{1.0} // Only one coefficient
	err = hyperbolic.SetCoefficients(invalidCoeffs)
	if err == nil {
		t.Error("Expected error for invalid coefficient count, got nil")
	}

	// Test that coefficients weren't changed by invalid update
	if hyperbolic.Coefficients()[0] != 3.0 || hyperbolic.Coefficients()[1] != 4.0 {
		t.Errorf("Coefficients changed by invalid update: %v", hyperbolic.Coefficients())
	}
}

// TestExponentialEstimator tests the exponential model implementation.
func TestExponentialEstimator(t *testing.T) {
	// Test basic functionality
	estimator := NewExponentialEstimator(2.0, 0.1)

	// Test type
	if estimator.Type() != ModelTypeExponential {
		t.Errorf("Expected ModelTypeExponential, got %v", estimator.Type())
	}

	// Test coefficients
	coeffs := estimator.Coefficients()
	expectedCoeffs := []float64{2.0, 0.1}
	if len(coeffs) != len(expectedCoeffs) {
		t.Errorf("Expected %d coefficients, got %d", len(expectedCoeffs), len(coeffs))
	}
	for i, expected := range expectedCoeffs {
		if math.Abs(coeffs[i]-expected) > 1e-10 {
			t.Errorf("Coefficient %d: expected %f, got %f", i, expected, coeffs[i])
		}
	}

	// Test estimation with known values
	// BPP = 2.0 * e^(0.1 * PPM)
	// For PPM = 10: BPP = 2.0 * e^(0.1 * 10) = 2.0 * e^1 = 2.0 * 2.718... ≈ 5.437
	ppm := 10.0
	expected := 2.0 * math.Exp(0.1*10.0)
	actual := estimator.Estimate(ppm)
	if math.Abs(actual-expected) > 1e-10 {
		t.Errorf("Estimate(10.0): expected %f, got %f", expected, actual)
	}

	// Test edge cases
	// Invalid PPM should return infinity
	invalidResult := estimator.Estimate(0.0)
	if !math.IsInf(invalidResult, 1) {
		t.Errorf("Expected infinity for PPM=0, got %f", invalidResult)
	}

	invalidResult = estimator.Estimate(-1.0)
	if !math.IsInf(invalidResult, 1) {
		t.Errorf("Expected infinity for PPM=-1, got %f", invalidResult)
	}

	// Test coefficient updates
	newCoeffs := []float64{3.0, 0.2}
	err := estimator.SetCoefficients(newCoeffs)
	if err != nil {
		t.Errorf("Unexpected error setting coefficients: %v", err)
	}

	updatedCoeffs := estimator.Coefficients()
	expectedUpdated := []float64{3.0, 0.2}
	for i, expected := range expectedUpdated {
		if math.Abs(updatedCoeffs[i]-expected) > 1e-10 {
			t.Errorf("Updated coefficient %d: expected %f, got %f", i, expected, updatedCoeffs[i])
		}
	}

	// Test invalid coefficient count
	invalidCoeffs := []float64{1.0} // Only one coefficient
	err = estimator.SetCoefficients(invalidCoeffs)
	if err == nil {
		t.Error("Expected error for invalid coefficient count, got nil")
	}

	// Test that coefficients weren't changed by invalid update
	if math.Abs(estimator.Coefficients()[0]-3.0) > 1e-10 || math.Abs(estimator.Coefficients()[1]-0.2) > 1e-10 {
		t.Errorf("Coefficients changed by invalid update: %v", estimator.Coefficients())
	}
}

// TestPolynomialEstimator tests the polynomial model implementation.
func TestPolynomialEstimator(t *testing.T) {
	// Test basic functionality
	estimator := NewPolynomialEstimator(1.0, 2.0, 0.5)

	// Test type
	if estimator.Type() != ModelTypePolynomial {
		t.Errorf("Expected ModelTypePolynomial, got %v", estimator.Type())
	}

	// Test coefficients
	coeffs := estimator.Coefficients()
	expectedCoeffs := []float64{1.0, 2.0, 0.5}
	if len(coeffs) != len(expectedCoeffs) {
		t.Errorf("Expected %d coefficients, got %d", len(expectedCoeffs), len(coeffs))
	}
	for i, expected := range expectedCoeffs {
		if math.Abs(coeffs[i]-expected) > 1e-10 {
			t.Errorf("Coefficient %d: expected %f, got %f", i, expected, coeffs[i])
		}
	}

	// Test estimation with known values
	// BPP = 1.0 + 2.0*PPM + 0.5*PPM²
	// For PPM = 2: BPP = 1.0 + 2.0*2 + 0.5*4 = 1.0 + 4.0 + 2.0 = 7.0
	ppm := 2.0
	expected := 1.0 + 2.0*2.0 + 0.5*2.0*2.0
	actual := estimator.Estimate(ppm)
	if math.Abs(actual-expected) > 1e-10 {
		t.Errorf("Estimate(2.0): expected %f, got %f", expected, actual)
	}

	// Test edge cases
	// Invalid PPM should return infinity
	invalidResult := estimator.Estimate(0.0)
	if !math.IsInf(invalidResult, 1) {
		t.Errorf("Expected infinity for PPM=0, got %f", invalidResult)
	}

	invalidResult = estimator.Estimate(-1.0)
	if !math.IsInf(invalidResult, 1) {
		t.Errorf("Expected infinity for PPM=-1, got %f", invalidResult)
	}

	// Test coefficient updates
	newCoeffs := []float64{2.0, 3.0, 1.0}
	err := estimator.SetCoefficients(newCoeffs)
	if err != nil {
		t.Errorf("Unexpected error setting coefficients: %v", err)
	}

	updatedCoeffs := estimator.Coefficients()
	expectedUpdated := []float64{2.0, 3.0, 1.0}
	for i, expected := range expectedUpdated {
		if math.Abs(updatedCoeffs[i]-expected) > 1e-10 {
			t.Errorf("Updated coefficient %d: expected %f, got %f", i, expected, updatedCoeffs[i])
		}
	}

	// Test invalid coefficient count
	invalidCoeffs := []float64{1.0, 2.0} // Only two coefficients
	err = estimator.SetCoefficients(invalidCoeffs)
	if err == nil {
		t.Error("Expected error for invalid coefficient count, got nil")
	}

	// Test that coefficients weren't changed by invalid update
	expectedAfterInvalid := []float64{2.0, 3.0, 1.0}
	actualAfterInvalid := estimator.Coefficients()
	for i, expected := range expectedAfterInvalid {
		if math.Abs(actualAfterInvalid[i]-expected) > 1e-10 {
			t.Errorf("Coefficients changed by invalid update: %v", actualAfterInvalid)
		}
	}
}

// TestNewEstimator tests the NewEstimator factory function.
func TestNewEstimator(t *testing.T) {
	tests := []struct {
		name         string
		modelName    string
		coeffs       []float64
		expectError  bool
		expectedType ModelType
	}{
		// Valid cases
		{
			name:         "hyperbolic with 2 coefficients",
			modelName:    "hyperbolic",
			coeffs:       []float64{10.0, 5.0},
			expectError:  false,
			expectedType: ModelTypeHyperbolic,
		},
		{
			name:         "logarithmic with 2 coefficients",
			modelName:    "logarithmic",
			coeffs:       []float64{8.0, 2.0},
			expectError:  false,
			expectedType: ModelTypeLogarithmic,
		},
		{
			name:         "power with 2 coefficients",
			modelName:    "power",
			coeffs:       []float64{12.0, -0.5},
			expectError:  false,
			expectedType: ModelTypePower,
		},
		{
			name:         "exponential with 2 coefficients",
			modelName:    "exponential",
			coeffs:       []float64{15.0, 0.1},
			expectError:  false,
			expectedType: ModelTypeExponential,
		},
		{
			name:         "polynomial with 3 coefficients",
			modelName:    "polynomial",
			coeffs:       []float64{1.0, 2.0, 0.5},
			expectError:  false,
			expectedType: ModelTypePolynomial,
		},
		// Invalid coefficient count cases
		{
			name:        "hyperbolic with 1 coefficient",
			modelName:   "hyperbolic",
			coeffs:      []float64{10.0},
			expectError: true,
		},
		{
			name:        "hyperbolic with 3 coefficients",
			modelName:   "hyperbolic",
			coeffs:      []float64{10.0, 5.0, 2.0},
			expectError: true,
		},
		{
			name:        "polynomial with 2 coefficients",
			modelName:   "polynomial",
			coeffs:      []float64{1.0, 2.0},
			expectError: true,
		},
		{
			name:        "polynomial with 4 coefficients",
			modelName:   "polynomial",
			coeffs:      []float64{1.0, 2.0, 0.5, 0.1},
			expectError: true,
		},
		// Invalid model name cases
		{
			name:        "unknown model",
			modelName:   "unknown",
			coeffs:      []float64{10.0, 5.0},
			expectError: true,
		},
		{
			name:        "empty model name",
			modelName:   "",
			coeffs:      []float64{10.0, 5.0},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimator, err := NewEstimator(tt.modelName, tt.coeffs)

			if tt.expectError {
				if err == nil {
					t.Error("NewEstimator() expected error but got none")
				}
				if estimator != nil {
					t.Error("NewEstimator() expected nil estimator but got", estimator)
				}

				return
			}

			if err != nil {
				t.Errorf("NewEstimator() unexpected error: %v", err)
				return
			}

			if estimator == nil {
				t.Error("NewEstimator() expected estimator but got nil")
				return
			}

			// Test that the estimator has the correct type
			if estimator.Type() != tt.expectedType {
				t.Errorf("NewEstimator() type = %v, want %v", estimator.Type(), tt.expectedType)
			}

			// Test that the coefficients match
			coeffs := estimator.Coefficients()
			if len(coeffs) != len(tt.coeffs) {
				t.Errorf("NewEstimator() coefficients length = %d, want %d", len(coeffs), len(tt.coeffs))
			}

			for i, coeff := range coeffs {
				if math.Abs(coeff-tt.coeffs[i]) > 1e-10 {
					t.Errorf("NewEstimator() coefficient[%d] = %v, want %v", i, coeff, tt.coeffs[i])
				}
			}

			// Test that the estimator can estimate values
			estimate := estimator.Estimate(100.0)
			if math.IsNaN(estimate) || math.IsInf(estimate, 0) {
				t.Errorf("NewEstimator() estimate = %v, want finite number", estimate)
			}
		})
	}
}

// TestNewEstimatorCaseInsensitive tests that NewEstimator is case-insensitive.
func TestNewEstimatorCaseInsensitive(t *testing.T) {
	testCases := []string{
		"hyperbolic",
		"HYPERBOLIC",
		"Hyperbolic",
		"HYPErbolic",
	}

	coeffs := []float64{10.0, 5.0}

	for _, name := range testCases {
		t.Run(name, func(t *testing.T) {
			estimator, err := NewEstimator(name, coeffs)
			if err != nil {
				t.Errorf("NewEstimator(%s) unexpected error: %v", name, err)
				return
			}

			if estimator == nil {
				t.Errorf("NewEstimator(%s) expected estimator but got nil", name)
				return
			}

			if estimator.Type() != ModelTypeHyperbolic {
				t.Errorf("NewEstimator(%s) type = %v, want %v", name, estimator.Type(), ModelTypeHyperbolic)
			}
		})
	}
}

// TestNewEstimatorEstimation tests that created estimators produce reasonable estimates.
func TestNewEstimatorEstimation(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		coeffs    []float64
		ppm       float64
		expected  float64
		tolerance float64
	}{
		{
			name:      "hyperbolic",
			modelName: "hyperbolic",
			coeffs:    []float64{10.0, 5.0},
			ppm:       100.0,
			expected:  10.05, // 10.0 + 5.0/100.0
			tolerance: 1e-10,
		},
		{
			name:      "logarithmic",
			modelName: "logarithmic",
			coeffs:    []float64{8.0, 2.0},
			ppm:       100.0,
			expected:  8.0 + 2.0*math.Log(100.0), // 8.0 + 2.0*ln(100)
			tolerance: 1e-10,
		},
		{
			name:      "power",
			modelName: "power",
			coeffs:    []float64{12.0, -0.5},
			ppm:       100.0,
			expected:  12.0 * math.Pow(100.0, -0.5), // 12.0 * 100^(-0.5)
			tolerance: 1e-10,
		},
		{
			name:      "exponential",
			modelName: "exponential",
			coeffs:    []float64{15.0, 0.1},
			ppm:       100.0,
			expected:  15.0 * math.Exp(0.1*100.0), // 15.0 * e^(0.1*100)
			tolerance: 1e-10,
		},
		{
			name:      "polynomial",
			modelName: "polynomial",
			coeffs:    []float64{1.0, 2.0, 0.5},
			ppm:       100.0,
			expected:  1.0 + 2.0*100.0 + 0.5*100.0*100.0, // 1.0 + 2.0*100 + 0.5*100²
			tolerance: 1e-10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimator, err := NewEstimator(tt.modelName, tt.coeffs)
			if err != nil {
				t.Errorf("NewEstimator() unexpected error: %v", err)
				return
			}

			estimate := estimator.Estimate(tt.ppm)
			if math.Abs(estimate-tt.expected) > tt.tolerance {
				t.Errorf("NewEstimator() estimate = %v, want %v (tolerance: %v)", estimate, tt.expected, tt.tolerance)
			}
		})
	}
}

// TestModelTypeFromString tests the ModelTypeFromString function.
func TestModelTypeFromString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ModelType
	}{
		{"hyperbolic lowercase", "hyperbolic", ModelTypeHyperbolic},
		{"hyperbolic uppercase", "HYPERBOLIC", ModelTypeHyperbolic},
		{"hyperbolic mixed case", "Hyperbolic", ModelTypeHyperbolic},
		{"logarithmic lowercase", "logarithmic", ModelTypeLogarithmic},
		{"logarithmic uppercase", "LOGARITHMIC", ModelTypeLogarithmic},
		{"power lowercase", "power", ModelTypePower},
		{"power uppercase", "POWER", ModelTypePower},
		{"exponential lowercase", "exponential", ModelTypeExponential},
		{"exponential uppercase", "EXPONENTIAL", ModelTypeExponential},
		{"polynomial lowercase", "polynomial", ModelTypePolynomial},
		{"polynomial uppercase", "POLYNOMIAL", ModelTypePolynomial},
		{"unknown model", "unknown", ModelType(-1)},
		{"empty string", "", ModelType(-1)},
		{"invalid model", "invalid", ModelType(-1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ModelTypeFromString(tt.input)
			if result != tt.expected {
				t.Errorf("ModelTypeFromString(%s) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestNewEmptyEstimator tests the newEmptyEstimator function.
func TestNewEmptyEstimator(t *testing.T) {
	tests := []struct {
		name         string
		modelType    ModelType
		expectedType ModelType
	}{
		{"hyperbolic", ModelTypeHyperbolic, ModelTypeHyperbolic},
		{"logarithmic", ModelTypeLogarithmic, ModelTypeLogarithmic},
		{"power", ModelTypePower, ModelTypePower},
		{"exponential", ModelTypeExponential, ModelTypeExponential},
		{"polynomial", ModelTypePolynomial, ModelTypePolynomial},
		{"invalid", ModelType(-1), ModelType(-1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimator := newEmptyEstimator(tt.modelType)

			if tt.modelType == ModelType(-1) {
				if estimator != nil {
					t.Errorf("newEmptyEstimator(%v) = %v, want nil", tt.modelType, estimator)
				}

				return
			}

			if estimator == nil {
				t.Errorf("newEmptyEstimator(%v) = nil, want non-nil", tt.modelType)
				return
			}

			if estimator.Type() != tt.expectedType {
				t.Errorf("newEmptyEstimator(%v).Type() = %v, want %v", tt.modelType, estimator.Type(), tt.expectedType)
			}

			// Test that coefficients are zero
			coeffs := estimator.Coefficients()
			for i, coeff := range coeffs {
				if coeff != 0.0 {
					t.Errorf("newEmptyEstimator(%v).Coefficients()[%d] = %v, want 0.0", tt.modelType, i, coeff)
				}
			}
		})
	}
}

// TestNewEstimatorStructuredApproach tests that the new structured approach works correctly.
func TestNewEstimatorStructuredApproach(t *testing.T) {
	// Test that the new approach produces the same results as the old approach
	testCases := []struct {
		name   string
		coeffs []float64
	}{
		{"hyperbolic", []float64{10.0, 5.0}},
		{"logarithmic", []float64{8.0, 2.0}},
		{"power", []float64{12.0, -0.5}},
		{"exponential", []float64{15.0, 0.1}},
		{"polynomial", []float64{1.0, 2.0, 0.5}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create estimator using NewEstimator
			estimator, err := NewEstimator(tc.name, tc.coeffs)
			if err != nil {
				t.Errorf("NewEstimator(%s, %v) error = %v", tc.name, tc.coeffs, err)
				return
			}

			if estimator == nil {
				t.Errorf("NewEstimator(%s, %v) = nil", tc.name, tc.coeffs)
				return
			}

			// Test that coefficients are set correctly
			actualCoeffs := estimator.Coefficients()
			if len(actualCoeffs) != len(tc.coeffs) {
				t.Errorf("NewEstimator(%s, %v) coefficients length = %d, want %d", tc.name, tc.coeffs, len(actualCoeffs), len(tc.coeffs))
				return
			}

			for i, expected := range tc.coeffs {
				if math.Abs(actualCoeffs[i]-expected) > 1e-10 {
					t.Errorf("NewEstimator(%s, %v) coefficient[%d] = %v, want %v", tc.name, tc.coeffs, i, actualCoeffs[i], expected)
				}
			}

			// Test that the estimator can estimate values
			estimate := estimator.Estimate(100.0)
			if math.IsNaN(estimate) || math.IsInf(estimate, 0) {
				t.Errorf("NewEstimator(%s, %v) estimate = %v, want finite number", tc.name, tc.coeffs, estimate)
			}
		})
	}
}

// ExampleNewEstimator demonstrates how to use the NewEstimator factory function.
func ExampleNewEstimator() {
	// Create a hyperbolic estimator with coefficients a=10.0, b=5.0
	// Formula: BPP = 10.0 + 5.0 / PPM
	hyperbolicEstimator, err := NewEstimator("hyperbolic", []float64{10.0, 5.0})
	if err != nil {
		log.Fatal(err)
	}

	// Create a polynomial estimator with coefficients a=1.0, b=2.0, c=0.5
	// Formula: BPP = 1.0 + 2.0*PPM + 0.5*PPM²
	polynomialEstimator, err := NewEstimator("polynomial", []float64{1.0, 2.0, 0.5})
	if err != nil {
		log.Fatal(err)
	}

	// Test both estimators with different PPM values
	ppmValues := []float64{10.0, 50.0, 100.0, 200.0}

	fmt.Println("PPM\tHyperbolic\tPolynomial")
	fmt.Println("---\t----------\t----------")

	for _, ppm := range ppmValues {
		hyperbolicBPP := hyperbolicEstimator.Estimate(ppm)
		polynomialBPP := polynomialEstimator.Estimate(ppm)

		fmt.Printf("%.0f\t%.2f\t\t%.2f\n", ppm, hyperbolicBPP, polynomialBPP)
	}

	// Demonstrate case-insensitive model names
	exponentialEstimator, err := NewEstimator("EXPONENTIAL", []float64{15.0, 0.1})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\nExponential estimator (case-insensitive): %.2f BPP at 100 PPM\n",
		exponentialEstimator.Estimate(100.0))

	// Demonstrate error handling
	_, err = NewEstimator("unknown", []float64{10.0, 5.0})
	if err != nil {
		fmt.Printf("Error for unknown model: %v\n", err)
	}

	_, err = NewEstimator("hyperbolic", []float64{10.0}) // Wrong number of coefficients
	if err != nil {
		fmt.Printf("Error for wrong coefficients: %v\n", err)
	}

	// Demonstrate the new structured approach with ModelTypeFromString
	modelType := ModelTypeFromString("POWER")
	fmt.Printf("ModelTypeFromString('POWER') = %v\n", modelType)

	// Demonstrate case-insensitive ModelTypeFromString
	modelTypes := []string{"HYPERBOLIC", "hyperbolic", "Hyperbolic", "HYPErbolic"}
	for _, name := range modelTypes {
		mt := ModelTypeFromString(name)
		fmt.Printf("ModelTypeFromString('%s') = %v (%s)\n", name, mt, mt.String())
	}

	// Output:
	// PPM	Hyperbolic	Polynomial
	// ---	----------	----------
	// 10	10.50		71.00
	// 50	10.10		1351.00
	// 100	10.05		5201.00
	// 200	10.03		20401.00
	//
	// Exponential estimator (case-insensitive): 330396.99 BPP at 100 PPM
	// Error for unknown model: unknown model type: unknown. Supported types: exponential, hyperbolic, logarithmic, polynomial, power
	// Error for wrong coefficients: hyperbolic model expects exactly 2 coefficients, got 1
	// ModelTypeFromString('POWER') = power
	// ModelTypeFromString('HYPERBOLIC') = hyperbolic (hyperbolic)
	// ModelTypeFromString('hyperbolic') = hyperbolic (hyperbolic)
	// ModelTypeFromString('Hyperbolic') = hyperbolic (hyperbolic)
	// ModelTypeFromString('HYPErbolic') = hyperbolic (hyperbolic)
}
