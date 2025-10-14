package regression

import "fmt"

// Model represents a regression model with metadata and the concrete estimator.
//
// A Model contains all the information needed to understand and use a fitted
// regression model for blob size estimation. It includes the mathematical
// formula, statistical metrics, and a concrete estimator for making predictions.
//
// Fields:
//   - Type: The mathematical model type (hyperbolic, logarithmic, power)
//   - Coefficients: The fitted parameters of the model
//   - RSquared: Coefficient of determination (0-1, higher is better)
//   - RMSE: Root mean square error (lower is better)
//   - Formula: Human-readable mathematical formula
//   - Estimator: Concrete implementation for making predictions
type Model struct {
	// Type is the model type (hyperbolic, logarithmic, power).
	Type ModelType
	// Coefficients contains the model coefficients.
	Coefficients []float64
	// RSquared is the coefficient of determination (goodness of fit, 0-1).
	RSquared float64
	// RMSE is the root mean square error.
	RMSE float64
	// Formula is a human-readable representation of the model.
	Formula string
	// Estimator is the concrete estimator implementation.
	Estimator Estimator
}

// String returns a string representation of the model.
//
// This method provides a human-readable summary of the model including
// its type, statistical metrics, and mathematical formula.
//
// Returns:
//   - string: Formatted model information
func (m *Model) String() string {
	return fmt.Sprintf("Model{Type: %s, R²: %.4f, RMSE: %.4f, Formula: %s}",
		m.Type, m.RSquared, m.RMSE, m.Formula)
}

// Result represents the result of a regression analysis.
//
// A Result contains the complete outcome of a regression analysis, including
// the best-fit model selected by the highest R² value and all candidate
// models for comparison. This allows users to evaluate model performance
// and choose alternative models if needed.
//
// Fields:
//   - BestFit: The model with the highest R² value (automatically selected)
//   - AllModels: All fitted models ranked by R² (best first)
//   - ChunkPPMs: PPM chunk sizes used to generate (PPM, BPP) points
type Result struct {
	// BestFit is the best-fit model (highest R²).
	BestFit *Model
	// AllModels contains all candidate models ranked by R² (best first).
	AllModels []*Model
	// ChunkPPMs holds the PPM chunk sizes used to generate (PPM, BPP) points.
	// This provides transparency into how data points were constructed.
	ChunkPPMs []int
}

// String returns a string representation of the result.
//
// This method provides a human-readable summary of the regression analysis
// result, including the best-fit model and the total number of candidate models.
//
// Returns:
//   - string: Formatted result information
func (r *Result) String() string {
	if r.BestFit == nil {
		return "Result{BestFit: nil}"
	}

	return fmt.Sprintf("Result{BestFit: %s, TotalModels: %d}",
		r.BestFit, len(r.AllModels))
}
