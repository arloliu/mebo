package regression_test

import (
	"fmt"
	"log"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/regression"
)

// ExampleAnalyze demonstrates basic usage of the Analyze function.
func ExampleAnalyze() {
	// Create some test blobs (in production, these would be your actual encoded blobs)
	blobs := createExampleBlobs()

	// Analyze all blobs together to get a single best-fit model
	result, err := regression.Analyze(blobs)
	if err != nil {
		log.Fatal(err)
	}

	// Print the best-fit model
	fmt.Printf("Best-fit model: %s\n", result.BestFit)
	fmt.Printf("Formula: %s\n", result.BestFit.Formula)
	fmt.Printf("R²: %.4f\n", result.BestFit.RSquared)

	// Use the estimator to predict blob size for different PPM values
	estimator := result.BestFit.Estimator
	fmt.Printf("Estimated BPP for 100 PPM: %.2f\n", estimator.Estimate(100.0))
	fmt.Printf("Estimated BPP for 200 PPM: %.2f\n", estimator.Estimate(200.0))

	// Output:
	// Best-fit model: Model{Type: hyperbolic, R²: 0.0000, RMSE: 0.0000, Formula: BPP = 16.00 + 0.00 / PPM}
	// Formula: BPP = 16.00 + 0.00 / PPM
	// R²: 0.0000
	// Estimated BPP for 100 PPM: 16.00
	// Estimated BPP for 200 PPM: 16.00
}

// ExampleAnalyzeEach demonstrates per-blob analysis for drift detection.
func ExampleAnalyzeEach() {
	// Create test blobs representing different time periods
	blobs := createExampleBlobs()

	// Analyze each blob separately to detect formula drift
	results, err := regression.AnalyzeEach(blobs)
	if err != nil {
		log.Fatal(err)
	}

	// Check for drift in the best-fit models
	for i, result := range results {
		bestModel := result.BestFit
		fmt.Printf("Blob %d: %s (R²=%.4f)\n", i, bestModel.Type, bestModel.RSquared)

		// Compare coefficients to detect drift
		if len(bestModel.Coefficients) >= 2 {
			a, b := bestModel.Coefficients[0], bestModel.Coefficients[1]
			fmt.Printf("  Coefficients: a=%.2f, b=%.2f\n", a, b)
		}
	}

	// Output:
	// Blob 0: hyperbolic (R²=0.0000)
	//   Coefficients: a=12.59, b=170.67
	// Blob 1: hyperbolic (R²=0.0000)
	//   Coefficients: a=16.00, b=-0.00
	// Blob 2: hyperbolic (R²=0.0000)
	//   Coefficients: a=16.00, b=0.00
}

// ExampleNewHyperbolicEstimator demonstrates how to use the Estimator interface.
func ExampleNewHyperbolicEstimator() {
	// Create a hyperbolic estimator with known coefficients
	estimator := regression.NewHyperbolicEstimator(9.98, 23.50)

	// Use the estimator to predict blob sizes
	ppmValues := []float64{10, 50, 100, 200, 500}
	fmt.Println("PPM -> BPP predictions:")
	for _, ppm := range ppmValues {
		bpp := estimator.Estimate(ppm)
		fmt.Printf("%3.0f PPM -> %.2f BPP\n", ppm, bpp)
	}

	// Get model metadata
	fmt.Printf("Model type: %s\n", estimator.Type())
	fmt.Printf("Coefficients: %v\n", estimator.Coefficients())

	// Output:
	// PPM -> BPP predictions:
	//  10 PPM -> 12.33 BPP
	//  50 PPM -> 10.45 BPP
	// 100 PPM -> 10.21 BPP
	// 200 PPM -> 10.10 BPP
	// 500 PPM -> 10.03 BPP
	// Model type: hyperbolic
	// Coefficients: [9.98 23.5]
}

// ExampleAnalyze_modelComparison demonstrates comparing different model types.
func ExampleAnalyze_modelComparison() {
	// Create test data
	blobs := createExampleBlobs()
	result, err := regression.Analyze(blobs)
	if err != nil {
		log.Fatal(err)
	}

	// Compare all candidate models
	fmt.Println("Model comparison (ranked by R²):")
	for i, model := range result.AllModels {
		fmt.Printf("%d. %s: R²=%.4f, RMSE=%.4f\n", i+1, model.Type, model.RSquared, model.RMSE)
		fmt.Printf("   Formula: %s\n", model.Formula)
	}

	// Test predictions with different models
	ppm := 100.0
	fmt.Printf("\nPredictions for %.0f PPM:\n", ppm)
	for _, model := range result.AllModels {
		prediction := model.Estimator.Estimate(ppm)
		fmt.Printf("  %s: %.2f BPP\n", model.Type, prediction)
	}

	// Output:
	// Model comparison (ranked by R²):
	// 1. hyperbolic: R²=0.0000, RMSE=0.0000
	//    Formula: BPP = 16.00 + 0.00 / PPM
	// 2. logarithmic: R²=0.0000, RMSE=0.0000
	//    Formula: BPP = 16.00 + 0.00 * ln(PPM)
	// 3. power: R²=0.0000, RMSE=0.0000
	//    Formula: BPP = 16.00 * PPM^-0.000
	// 4. exponential: R²=0.0000, RMSE=0.0000
	//    Formula: BPP = 16.00 * e^(0.000 * PPM)
	// 5. polynomial: R²=0.0000, RMSE=4.9434
	//    Formula: BPP = -0.00 + 0.12*PPM + 0.00*PPM²
	//
	// Predictions for 100 PPM:
	//   hyperbolic: 16.00 BPP
	//   logarithmic: 16.00 BPP
	//   power: 16.00 BPP
	//   exponential: 16.00 BPP
	//   polynomial: 11.83 BPP
}

// createExampleBlobs creates example blobs for demonstration purposes.
func createExampleBlobs() []blob.NumericBlob {
	// Create 3 blobs with different characteristics
	configs := []struct {
		metrics         int
		pointsPerMetric int
	}{
		{10, 50},  // Small blob
		{20, 100}, // Medium blob
		{30, 150}, // Large blob
	}

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
			panic(err)
		}

		// Add metrics with realistic data patterns
		for j := 0; j < config.metrics; j++ {
			metricID := uint64(j + 1000)
			if err := encoder.StartMetricID(metricID, config.pointsPerMetric); err != nil {
				panic(err)
			}

			// Generate realistic time-series data
			baseValue := 100.0 + float64(j)*10.0
			for k := 0; k < config.pointsPerMetric; k++ {
				ts := startTime.Add(time.Duration(k) * time.Second)
				// Add some realistic variation
				value := baseValue + float64(k)*0.1 + float64(j%10)
				if err := encoder.AddDataPoint(ts.UnixMicro(), value, ""); err != nil {
					panic(err)
				}
			}

			if err := encoder.EndMetric(); err != nil {
				panic(err)
			}
		}

		// Finish encoding
		blobBytes, err := encoder.Finish()
		if err != nil {
			panic(err)
		}

		// Decode to get NumericBlob
		decoder, err := blob.NewNumericDecoder(blobBytes)
		if err != nil {
			panic(err)
		}

		blobData, err := decoder.Decode()
		if err != nil {
			panic(err)
		}

		blobs[i] = blobData
	}

	return blobs
}
