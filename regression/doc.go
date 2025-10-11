// Package regression provides blob size estimation through regression analysis of encoded mebo blobs.
//
// This package enables offline regression analysis of production data to continuously
// improve blob size prediction accuracy. It analyzes the relationship between points-per-metric (PPM)
// and bytes-per-point (BPP) to derive mathematical formulas for blob size estimation.
//
// # Key Features
//
//   - **Multiple Model Types**: Supports hyperbolic, logarithmic, and power regression models
//   - **Automatic Model Selection**: Chooses the best-fit model based on R² coefficient
//   - **Production-Ready**: Works directly with encoded mebo blobs
//   - **Flexible Analysis**: Supports both aggregated and per-blob analysis
//   - **Precise Sampling**: Intelligent PPM sampling based on input data characteristics
//
// # Usage Patterns
//
// ## Basic Analysis
//
// Analyze multiple blobs together to get a single best-fit model:
//
//	blobs := []blob.NumericBlob{blob1, blob2, blob3}
//	result, err := regression.Analyze(blobs)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Use the best-fit estimator for predictions
//	estimator := result.BestFit.Estimator
//	bpp := estimator.Estimate(100.0) // Estimate BPP for 100 PPM
//
// ## Per-Blob Analysis
//
// Analyze each blob separately for drift detection:
//
//	results, err := regression.AnalyzeEach(blobs)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	for i, result := range results {
//	    fmt.Printf("Blob %d: %s (R²=%.4f)\n", i, result.BestFit.Type, result.BestFit.RSquared)
//	}
//
// ## Model Comparison
//
// Compare all candidate models to understand their performance:
//
//	for _, model := range result.AllModels {
//	    fmt.Printf("%s: R²=%.4f, Formula=%s\n", model.Type, model.RSquared, model.Formula)
//	}
//
// # Model Types
//
// The package supports three regression models:
//
//   - **Hyperbolic**: BPP = a + b / PPM (typically best for mebo data)
//   - **Logarithmic**: BPP = a + b * ln(PPM)
//   - **Power**: BPP = a * PPM^b
//
// The best-fit model is automatically selected based on the highest R² coefficient.
//
// # Performance Characteristics
//
//   - **Analysis Time**: O(n) where n is the total number of data points across all blobs
//   - **Memory Usage**: Minimal - processes blobs sequentially without loading all data into memory
//   - **Accuracy**: R² typically > 0.98 for hyperbolic model with sufficient data points
//
// # Formula Derivation Methodology
//
// The regression analysis follows the methodology described in the mebo measurement tool:
//
//  1. Extract PPM and BPP pairs from each blob's metrics
//  2. Apply least-squares regression to fit each model type
//  3. Calculate R² and RMSE for model comparison
//  4. Select the model with the highest R² as the best fit
//
// For detailed methodology and benchmark results, see the measurement tool documentation
// at tests/measure/README.md.
//
// # Production Use Cases
//
//   - **Storage Planning**: Predict storage costs for production workloads
//   - **Configuration Tuning**: Choose optimal blob sizes based on historical data
//   - **Cost Optimization**: Balance write frequency vs storage efficiency
//   - **Capacity Planning**: Estimate infrastructure requirements
//   - **Drift Detection**: Monitor formula changes over time to detect data pattern shifts
//
// # Example: Continuous Improvement
//
// Use this package in a feedback loop to continuously improve blob size estimation:
//
//	// 1. Collect production blobs
//	blobs := collectProductionBlobs()
//
//	// 2. Analyze to get updated formula
//	result, err := regression.Analyze(blobs)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// 3. Update your estimation logic with new coefficients
//	estimator := result.BestFit.Estimator
//	newFormula := result.BestFit.Formula
//	fmt.Printf("Updated formula: %s (R²=%.4f)\n", newFormula, result.BestFit.RSquared)
//
//	// 4. Use for future predictions
//	predictedBPP := estimator.Estimate(expectedPPM)
//	estimatedBlobSize := predictedBPP * expectedTotalPoints
//
// This enables your blob size estimation to become more accurate over time as you
// collect more production data.
package regression
