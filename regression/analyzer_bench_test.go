package regression

import (
	"fmt"
	"math"
	"testing"
)

// BenchmarkPolynomialFitting benchmarks polynomial regression specifically
func BenchmarkPolynomialFitting(b *testing.B) {
	sizes := []int{10, 100, 1000, 5000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Points_%d", size), func(b *testing.B) {
			x, y := generateBenchmarkData(size)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				fitPolynomial(x, y)
			}
		})
	}
}

// BenchmarkExponentialFitting benchmarks exponential regression
func BenchmarkExponentialFitting(b *testing.B) {
	sizes := []int{10, 100, 1000, 5000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Points_%d", size), func(b *testing.B) {
			x, y := generateBenchmarkData(size)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				fitExponential(x, y)
			}
		})
	}
}

// BenchmarkHyperbolicFitting benchmarks hyperbolic regression
func BenchmarkHyperbolicFitting(b *testing.B) {
	sizes := []int{10, 100, 1000, 5000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Points_%d", size), func(b *testing.B) {
			x, y := generateBenchmarkData(size)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				fitHyperbolic(x, y)
			}
		})
	}
}

// BenchmarkLogarithmicFitting benchmarks logarithmic regression
func BenchmarkLogarithmicFitting(b *testing.B) {
	sizes := []int{10, 100, 1000, 5000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Points_%d", size), func(b *testing.B) {
			x, y := generateBenchmarkData(size)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				fitLogarithmic(x, y)
			}
		})
	}
}

// BenchmarkPowerFitting benchmarks power regression
func BenchmarkPowerFitting(b *testing.B) {
	sizes := []int{10, 100, 1000, 5000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Points_%d", size), func(b *testing.B) {
			x, y := generateBenchmarkData(size)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				fitPower(x, y)
			}
		})
	}
}

// BenchmarkEstimatorEstimate benchmarks estimator calculations
func BenchmarkEstimatorEstimate(b *testing.B) {
	estimators := []struct {
		name string
		est  Estimator
	}{
		{"Hyperbolic", NewHyperbolicEstimator(10.0, 5.0)},
		{"Logarithmic", NewLogarithmicEstimator(8.0, 2.0)},
		{"Power", NewPowerEstimator(12.0, -0.5)},
		{"Exponential", NewExponentialEstimator(15.0, 0.1)},
		{"Polynomial", NewPolynomialEstimator(1.0, 2.0, 0.5)},
	}

	ppmValues := []float64{10, 50, 100, 200, 500, 1000}

	for _, est := range estimators {
		b.Run(est.name, func(b *testing.B) {
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				for _, ppm := range ppmValues {
					_ = est.est.Estimate(ppm)
				}
			}
		})
	}
}

// BenchmarkMemoryAllocations benchmarks memory allocation patterns
func BenchmarkMemoryAllocations(b *testing.B) {
	b.Run("Coefficients", func(b *testing.B) {
		est := NewHyperbolicEstimator(10.0, 5.0)
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_ = est.Coefficients()
		}
	})

	b.Run("SetCoefficients", func(b *testing.B) {
		est := NewHyperbolicEstimator(10.0, 5.0)
		coeffs := []float64{10.0, 5.0}
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_ = est.SetCoefficients(coeffs)
		}
	})
}

// BenchmarkStatisticalCalculations benchmarks R² and RMSE calculations
func BenchmarkStatisticalCalculations(b *testing.B) {
	sizes := []int{100, 1000, 10000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			observed, predicted := generateBenchmarkData(size)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_ = calculateRSquared(observed, predicted)
				_ = calculateRMSE(observed, predicted)
			}
		})
	}
}

// generateBenchmarkData creates test data for regression benchmarking
func generateBenchmarkData(size int) (x, y []float64) {
	x = make([]float64, size)
	y = make([]float64, size)

	for i := 0; i < size; i++ {
		// Generate polynomial-like data: y = 1 + 2x + 0.5x² + noise
		xi := float64(i+1) * 0.1
		x[i] = xi
		y[i] = 1.0 + 2.0*xi + 0.5*xi*xi + 0.1*math.Sin(float64(i))
	}

	return x, y
}
