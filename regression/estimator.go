package regression

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// ModelType represents the type of regression model.
type ModelType int

const (
	// ModelTypeHyperbolic represents the hyperbolic model: BPP = a + b / PPM
	ModelTypeHyperbolic ModelType = iota
	// ModelTypeLogarithmic represents the logarithmic model: BPP = a + b * ln(PPM)
	ModelTypeLogarithmic
	// ModelTypePower represents the power model: BPP = a * PPM^b
	ModelTypePower
	// ModelTypeExponential represents the exponential model: BPP = a * e^(b * PPM)
	ModelTypeExponential
	// ModelTypePolynomial represents the polynomial model: BPP = a + b*PPM + c*PPM²
	ModelTypePolynomial
)

// modelTypeNames maps ModelType to their string representations.
var modelTypeNames = map[ModelType]string{
	ModelTypeHyperbolic:  "hyperbolic",
	ModelTypeLogarithmic: "logarithmic",
	ModelTypePower:       "power",
	ModelTypeExponential: "exponential",
	ModelTypePolynomial:  "polynomial",
}

// String returns the string representation of the model type.
func (mt ModelType) String() string {
	if name, exists := modelTypeNames[mt]; exists {
		return name
	}

	return "unknown"
}

// modelTypeFromString maps string names to ModelType.
var modelTypeFromString = map[string]ModelType{
	"hyperbolic":  ModelTypeHyperbolic,
	"logarithmic": ModelTypeLogarithmic,
	"power":       ModelTypePower,
	"exponential": ModelTypeExponential,
	"polynomial":  ModelTypePolynomial,
}

// ModelTypeFromString returns the ModelType for a given string name.
// Returns ModelType(-1) for unknown names.
func ModelTypeFromString(name string) ModelType {
	if modelType, exists := modelTypeFromString[strings.ToLower(name)]; exists {
		return modelType
	}

	return ModelType(-1) // Invalid ModelType
}

// newEmptyEstimator creates an empty estimator for the given ModelType.
// This is used internally by NewEstimator to create estimators and validate coefficients.
func newEmptyEstimator(modelType ModelType) Estimator {
	switch modelType {
	case ModelTypeHyperbolic:
		return NewHyperbolicEstimator(0, 0)
	case ModelTypeLogarithmic:
		return NewLogarithmicEstimator(0, 0)
	case ModelTypePower:
		return NewPowerEstimator(0, 0)
	case ModelTypeExponential:
		return NewExponentialEstimator(0, 0)
	case ModelTypePolynomial:
		return NewPolynomialEstimator(0, 0, 0)
	default:
		return nil
	}
}

// Estimator defines the interface for blob size estimation models.
type Estimator interface {
	// Estimate calculates the bytes per point (BPP) for a given points per metric (PPM).
	Estimate(ppm float64) float64
	// Type returns the model type.
	Type() ModelType
	// Coefficients returns the model coefficients.
	Coefficients() []float64
	// SetCoefficients updates the coefficients of the model.
	// This allows runtime updates to the estimator without creating a new instance.
	// The number of coefficients must match the model's expected count:
	// - 2 coefficients: hyperbolic, logarithmic, power, exponential
	// - 3 coefficients: polynomial (quadratic)
	SetCoefficients(coeffs []float64) error
}

// HyperbolicEstimator implements the hyperbolic model: BPP = a + b / PPM
type HyperbolicEstimator struct {
	a, b   float64
	coeffs []float64 // Cached coefficient slice to avoid allocations
}

// NewHyperbolicEstimator creates a new hyperbolic estimator with the given coefficients.
func NewHyperbolicEstimator(a, b float64) *HyperbolicEstimator {
	return &HyperbolicEstimator{
		a:      a,
		b:      b,
		coeffs: make([]float64, 2), // Pre-allocate coefficient slice
	}
}

// Estimate calculates BPP using the hyperbolic formula: BPP = a + b / PPM
func (h *HyperbolicEstimator) Estimate(ppm float64) float64 {
	if ppm <= 0 {
		return math.Inf(1) // Return infinity for invalid PPM
	}

	return h.a + h.b/ppm
}

// Type returns the model type.
func (h *HyperbolicEstimator) Type() ModelType {
	return ModelTypeHyperbolic
}

// Coefficients returns the model coefficients [a, b].
func (h *HyperbolicEstimator) Coefficients() []float64 {
	h.coeffs[0] = h.a
	h.coeffs[1] = h.b
	return h.coeffs
}

// SetCoefficients updates the coefficients of the hyperbolic model.
// Expects exactly 2 coefficients: [a, b] for the formula BPP = a + b / PPM.
func (h *HyperbolicEstimator) SetCoefficients(coeffs []float64) error {
	if len(coeffs) != 2 {
		return fmt.Errorf("hyperbolic model expects exactly 2 coefficients, got %d", len(coeffs))
	}
	h.a = coeffs[0]
	h.b = coeffs[1]

	return nil
}

// LogarithmicEstimator implements the logarithmic model: BPP = a + b * ln(PPM)
type LogarithmicEstimator struct {
	a, b   float64
	coeffs []float64 // Cached coefficient slice to avoid allocations
}

// NewLogarithmicEstimator creates a new logarithmic estimator with the given coefficients.
func NewLogarithmicEstimator(a, b float64) *LogarithmicEstimator {
	return &LogarithmicEstimator{
		a:      a,
		b:      b,
		coeffs: make([]float64, 2), // Pre-allocate coefficient slice
	}
}

// Estimate calculates BPP using the logarithmic formula: BPP = a + b * ln(PPM)
func (l *LogarithmicEstimator) Estimate(ppm float64) float64 {
	if ppm <= 0 {
		return math.Inf(1) // Return infinity for invalid PPM
	}

	return l.a + l.b*math.Log(ppm)
}

// Type returns the model type.
func (l *LogarithmicEstimator) Type() ModelType {
	return ModelTypeLogarithmic
}

// Coefficients returns the model coefficients [a, b].
func (l *LogarithmicEstimator) Coefficients() []float64 {
	l.coeffs[0] = l.a
	l.coeffs[1] = l.b
	return l.coeffs
}

// SetCoefficients updates the coefficients of the logarithmic model.
// Expects exactly 2 coefficients: [a, b] for the formula BPP = a + b * ln(PPM).
func (l *LogarithmicEstimator) SetCoefficients(coeffs []float64) error {
	if len(coeffs) != 2 {
		return fmt.Errorf("logarithmic model expects exactly 2 coefficients, got %d", len(coeffs))
	}
	l.a = coeffs[0]
	l.b = coeffs[1]

	return nil
}

// PowerEstimator implements the power model: BPP = a * PPM^b
type PowerEstimator struct {
	a, b   float64
	coeffs []float64 // Cached coefficient slice to avoid allocations
}

// NewPowerEstimator creates a new power estimator with the given coefficients.
func NewPowerEstimator(a, b float64) *PowerEstimator {
	return &PowerEstimator{
		a:      a,
		b:      b,
		coeffs: make([]float64, 2), // Pre-allocate coefficient slice
	}
}

// Estimate calculates BPP using the power formula: BPP = a * PPM^b
func (p *PowerEstimator) Estimate(ppm float64) float64 {
	if ppm <= 0 {
		return math.Inf(1) // Return infinity for invalid PPM
	}

	return p.a * math.Pow(ppm, p.b)
}

// Type returns the model type.
func (p *PowerEstimator) Type() ModelType {
	return ModelTypePower
}

// Coefficients returns the model coefficients [a, b].
func (p *PowerEstimator) Coefficients() []float64 {
	p.coeffs[0] = p.a
	p.coeffs[1] = p.b
	return p.coeffs
}

// SetCoefficients updates the coefficients of the power model.
// Expects exactly 2 coefficients: [a, b] for the formula BPP = a * PPM^b.
func (p *PowerEstimator) SetCoefficients(coeffs []float64) error {
	if len(coeffs) != 2 {
		return fmt.Errorf("power model expects exactly 2 coefficients, got %d", len(coeffs))
	}
	p.a = coeffs[0]
	p.b = coeffs[1]

	return nil
}

// ExponentialEstimator implements the exponential model: BPP = a * e^(b * PPM)
type ExponentialEstimator struct {
	a, b   float64
	coeffs []float64 // Cached coefficient slice to avoid allocations
}

// NewExponentialEstimator creates a new exponential estimator with the given coefficients.
func NewExponentialEstimator(a, b float64) *ExponentialEstimator {
	return &ExponentialEstimator{
		a:      a,
		b:      b,
		coeffs: make([]float64, 2), // Pre-allocate coefficient slice
	}
}

// Estimate calculates BPP using the exponential formula: BPP = a * e^(b * PPM)
func (e *ExponentialEstimator) Estimate(ppm float64) float64 {
	if ppm <= 0 {
		return math.Inf(1) // Return infinity for invalid PPM
	}

	return e.a * math.Exp(e.b*ppm)
}

// Type returns the model type.
func (e *ExponentialEstimator) Type() ModelType {
	return ModelTypeExponential
}

// Coefficients returns the model coefficients [a, b].
func (e *ExponentialEstimator) Coefficients() []float64 {
	e.coeffs[0] = e.a
	e.coeffs[1] = e.b
	return e.coeffs
}

// SetCoefficients updates the coefficients of the exponential model.
// Expects exactly 2 coefficients: [a, b] for the formula BPP = a * e^(b * PPM).
func (e *ExponentialEstimator) SetCoefficients(coeffs []float64) error {
	if len(coeffs) != 2 {
		return fmt.Errorf("exponential model expects exactly 2 coefficients, got %d", len(coeffs))
	}
	e.a = coeffs[0]
	e.b = coeffs[1]

	return nil
}

// PolynomialEstimator implements the polynomial model: BPP = a + b*PPM + c*PPM²
type PolynomialEstimator struct {
	a, b, c float64
	coeffs  []float64 // Cached coefficient slice to avoid allocations
}

// NewPolynomialEstimator creates a new polynomial estimator with the given coefficients.
func NewPolynomialEstimator(a, b, c float64) *PolynomialEstimator {
	return &PolynomialEstimator{
		a:      a,
		b:      b,
		c:      c,
		coeffs: make([]float64, 3), // Pre-allocate coefficient slice
	}
}

// Estimate calculates BPP using the polynomial formula: BPP = a + b*PPM + c*PPM²
func (p *PolynomialEstimator) Estimate(ppm float64) float64 {
	if ppm <= 0 {
		return math.Inf(1) // Return infinity for invalid PPM
	}

	return p.a + p.b*ppm + p.c*ppm*ppm
}

// Type returns the model type.
func (p *PolynomialEstimator) Type() ModelType {
	return ModelTypePolynomial
}

// Coefficients returns the model coefficients [a, b, c].
func (p *PolynomialEstimator) Coefficients() []float64 {
	p.coeffs[0] = p.a
	p.coeffs[1] = p.b
	p.coeffs[2] = p.c

	return p.coeffs
}

// SetCoefficients updates the coefficients of the polynomial model.
// Expects exactly 3 coefficients: [a, b, c] for the formula BPP = a + b*PPM + c*PPM².
func (p *PolynomialEstimator) SetCoefficients(coeffs []float64) error {
	if len(coeffs) != 3 {
		return fmt.Errorf("polynomial model expects exactly 3 coefficients, got %d", len(coeffs))
	}
	p.a = coeffs[0]
	p.b = coeffs[1]
	p.c = coeffs[2]

	return nil
}

// NewEstimator creates a new estimator by name and coefficients.
//
// This function provides a convenient factory method for creating estimator
// implementations dynamically based on the model name and provided coefficients.
//
// Parameters:
//   - name: The model name (case-insensitive). Supported names:
//   - "hyperbolic": Creates HyperbolicEstimator (expects 2 coefficients)
//   - "logarithmic": Creates LogarithmicEstimator (expects 2 coefficients)
//   - "power": Creates PowerEstimator (expects 2 coefficients)
//   - "exponential": Creates ExponentialEstimator (expects 2 coefficients)
//   - "polynomial": Creates PolynomialEstimator (expects 3 coefficients)
//   - coeffs: The model coefficients. The number of coefficients must match
//     the model's requirements (2 for most models, 3 for polynomial)
//
// Returns:
//   - Estimator: The created estimator instance
//   - error: Returns an error if the name is invalid or coefficients are invalid
//
// Example:
//
//	// Create a hyperbolic estimator
//	estimator, err := NewEstimator("hyperbolic", []float64{10.0, 5.0})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	bpp := estimator.Estimate(100.0) // Calculate BPP for 100 PPM
//
//	// Create a polynomial estimator
//	polyEstimator, err := NewEstimator("polynomial", []float64{1.0, 2.0, 0.5})
//	if err != nil {
//	    log.Fatal(err)
//	}
func NewEstimator(name string, coeffs []float64) (Estimator, error) {
	// Convert string name to ModelType
	modelType := ModelTypeFromString(name)
	if modelType == ModelType(-1) {
		// Build list of supported types for error message using modelTypeNames map
		var supportedTypes []string
		for _, modelTypeName := range modelTypeNames {
			supportedTypes = append(supportedTypes, modelTypeName)
		}
		// Sort to ensure consistent output order
		slices.Sort(supportedTypes)

		return nil, fmt.Errorf("unknown model type: %s. Supported types: %s", name, strings.Join(supportedTypes, ", "))
	}

	// Create empty estimator for the model type
	estimator := newEmptyEstimator(modelType)
	if estimator == nil {
		return nil, fmt.Errorf("failed to create estimator for model type: %s", name)
	}

	// Use SetCoefficients to validate and set coefficients
	if err := estimator.SetCoefficients(coeffs); err != nil {
		return nil, err
	}

	return estimator, nil
}
