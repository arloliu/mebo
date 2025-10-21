package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/arloliu/mebo/internal/hash"
)

// TimeUnit represents the unit of timestamps in the input data.
type TimeUnit string

const (
	TimeUnitSecond      TimeUnit = "s"
	TimeUnitMillisecond TimeUnit = "ms"
	TimeUnitMicrosecond TimeUnit = "us"
	TimeUnitNanosecond  TimeUnit = "ns"
)

// InputMetricColumnBased represents a metric in column-based format.
type InputMetricColumnBased struct {
	ID         *uint64   `json:"id"`         // Optional: metric ID
	Name       *string   `json:"name"`       // Optional: metric name
	Timestamps []int64   `json:"timestamps"` // Timestamps in specified time unit
	Values     []float64 `json:"values"`     // Values
}

// InputDataColumnBased represents the column-based input format.
type InputDataColumnBased struct {
	Metrics []InputMetricColumnBased `json:"metrics"`
}

// DataPoint represents a single timestamp-value pair in row-based format.
type DataPoint struct {
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

// InputMetricRowBased represents a metric in row-based format.
type InputMetricRowBased struct {
	ID     *uint64     `json:"id"`     // Optional: metric ID
	Name   *string     `json:"name"`   // Optional: metric name
	Points []DataPoint `json:"points"` // Array of timestamp-value pairs
}

// InputDataRowBased represents the row-based input format.
type InputDataRowBased struct {
	Metrics []InputMetricRowBased `json:"metrics"`
}

// LoadInputData loads and parses input data from a JSON file.
//
// The function auto-detects whether the file is in column-based or row-based
// format, validates the data, and converts it to the unified TestData structure.
//
// Parameters:
//   - filename: Path to the JSON file
//   - timeUnit: Unit of timestamps in the input data (s, ms, us, ns)
//
// Returns:
//   - *TestData: Loaded and converted test data
//   - error: Parse or validation error if any
//
// Example:
//
//	data, err := LoadInputData("metrics.json", TimeUnitMicrosecond)
//	if err != nil {
//	    log.Fatal(err)
//	}
func LoadInputData(filename string, timeUnit TimeUnit) (*TestData, error) {
	// Read file
	fileData, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Try to parse as a generic JSON to detect format
	var genericData map[string]any
	if err := json.Unmarshal(fileData, &genericData); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	metrics, ok := genericData["metrics"]
	if !ok {
		return nil, errors.New("JSON must contain a 'metrics' array")
	}

	metricsArray, ok := metrics.([]any)
	if !ok || len(metricsArray) == 0 {
		return nil, errors.New("'metrics' must be a non-empty array")
	}

	// Detect format by checking the first metric
	firstMetric, ok := metricsArray[0].(map[string]any)
	if !ok {
		return nil, errors.New("invalid metric format")
	}

	// Check if it's row-based (has 'points') or column-based (has 'timestamps' and 'values')
	if _, hasPoints := firstMetric["points"]; hasPoints {
		return loadRowBasedData(fileData, timeUnit)
	} else if _, hasTimestamps := firstMetric["timestamps"]; hasTimestamps {
		return loadColumnBasedData(fileData, timeUnit)
	}

	return nil, errors.New("unable to detect format: metrics must have either 'points' (row-based) or 'timestamps'+'values' (column-based)")
}

// loadColumnBasedData loads data in column-based format.
func loadColumnBasedData(fileData []byte, timeUnit TimeUnit) (*TestData, error) {
	var inputData InputDataColumnBased
	if err := json.Unmarshal(fileData, &inputData); err != nil {
		return nil, fmt.Errorf("failed to parse column-based format: %w", err)
	}

	if len(inputData.Metrics) == 0 {
		return nil, errors.New("no metrics found in input data")
	}

	// Calculate total points and find max points per metric
	numMetrics := len(inputData.Metrics)
	maxPointsPerMetric := 0
	totalPoints := 0

	for i, metric := range inputData.Metrics {
		if len(metric.Timestamps) != len(metric.Values) {
			return nil, fmt.Errorf("metric %d: timestamps and values length mismatch (%d vs %d)",
				i, len(metric.Timestamps), len(metric.Values))
		}
		if len(metric.Timestamps) == 0 {
			return nil, fmt.Errorf("metric %d: no data points", i)
		}
		numPoints := len(metric.Timestamps)
		totalPoints += numPoints
		if numPoints > maxPointsPerMetric {
			maxPointsPerMetric = numPoints
		}
	}

	// Convert to TestData
	testData := &TestData{
		MetricIDs:  make([]uint64, numMetrics),
		Timestamps: make([]int64, totalPoints),
		Values:     make([]float64, totalPoints),
		StartTime:  time.Time{}, // Will be set based on first timestamp
		Config: Config{
			NumMetrics: numMetrics,
			MaxPoints:  maxPointsPerMetric,
		},
	}

	// Process each metric
	offset := 0
	var minTimestamp int64 = -1

	for i, metric := range inputData.Metrics {
		// Get metric ID
		metricID, err := extractMetricID(metric.ID, metric.Name, i)
		if err != nil {
			return nil, err
		}
		testData.MetricIDs[i] = metricID

		// Convert and copy timestamps and values
		for j := 0; j < len(metric.Timestamps); j++ {
			ts := convertToMicroseconds(metric.Timestamps[j], timeUnit)
			testData.Timestamps[offset+j] = ts
			testData.Values[offset+j] = metric.Values[j]

			// Track minimum timestamp for StartTime
			if minTimestamp == -1 || ts < minTimestamp {
				minTimestamp = ts
			}

			// Validate monotonic timestamps
			if j > 0 && ts <= testData.Timestamps[offset+j-1] {
				return nil, fmt.Errorf("metric %d: timestamps must be monotonically increasing at index %d", i, j)
			}
		}

		offset += len(metric.Timestamps)
	}

	// Set start time based on minimum timestamp
	if minTimestamp > 0 {
		testData.StartTime = time.UnixMicro(minTimestamp)
	} else {
		testData.StartTime = time.Now()
	}

	return testData, nil
}

// loadRowBasedData loads data in row-based format.
func loadRowBasedData(fileData []byte, timeUnit TimeUnit) (*TestData, error) {
	var inputData InputDataRowBased
	if err := json.Unmarshal(fileData, &inputData); err != nil {
		return nil, fmt.Errorf("failed to parse row-based format: %w", err)
	}

	if len(inputData.Metrics) == 0 {
		return nil, errors.New("no metrics found in input data")
	}

	// Calculate total points and find max points per metric
	numMetrics := len(inputData.Metrics)
	maxPointsPerMetric := 0
	totalPoints := 0

	for i, metric := range inputData.Metrics {
		if len(metric.Points) == 0 {
			return nil, fmt.Errorf("metric %d: no data points", i)
		}
		numPoints := len(metric.Points)
		totalPoints += numPoints
		if numPoints > maxPointsPerMetric {
			maxPointsPerMetric = numPoints
		}
	}

	// Convert to TestData
	testData := &TestData{
		MetricIDs:  make([]uint64, numMetrics),
		Timestamps: make([]int64, totalPoints),
		Values:     make([]float64, totalPoints),
		StartTime:  time.Time{}, // Will be set based on first timestamp
		Config: Config{
			NumMetrics: numMetrics,
			MaxPoints:  maxPointsPerMetric,
		},
	}

	// Process each metric
	offset := 0
	var minTimestamp int64 = -1

	for i, metric := range inputData.Metrics {
		// Get metric ID
		metricID, err := extractMetricID(metric.ID, metric.Name, i)
		if err != nil {
			return nil, err
		}
		testData.MetricIDs[i] = metricID

		// Convert and copy points
		for j, point := range metric.Points {
			ts := convertToMicroseconds(point.Timestamp, timeUnit)
			testData.Timestamps[offset+j] = ts
			testData.Values[offset+j] = point.Value

			// Track minimum timestamp for StartTime
			if minTimestamp == -1 || ts < minTimestamp {
				minTimestamp = ts
			}

			// Validate monotonic timestamps
			if j > 0 && ts <= testData.Timestamps[offset+j-1] {
				return nil, fmt.Errorf("metric %d: timestamps must be monotonically increasing at index %d", i, j)
			}
		}

		offset += len(metric.Points)
	}

	// Set start time based on minimum timestamp
	if minTimestamp > 0 {
		testData.StartTime = time.UnixMicro(minTimestamp)
	} else {
		testData.StartTime = time.Now()
	}

	return testData, nil
}

// extractMetricID extracts or generates a metric ID from the input.
func extractMetricID(id *uint64, name *string, index int) (uint64, error) {
	if id != nil && name != nil {
		return 0, fmt.Errorf("metric %d: cannot have both 'id' and 'name' fields", index)
	}

	if id != nil {
		return *id, nil
	}

	if name != nil {
		return hash.ID(*name), nil
	}

	return 0, fmt.Errorf("metric %d: must have either 'id' or 'name' field", index)
}

// convertToMicroseconds converts a timestamp from the specified unit to microseconds.
func convertToMicroseconds(timestamp int64, unit TimeUnit) int64 {
	switch unit {
	case TimeUnitSecond:
		return timestamp * 1_000_000
	case TimeUnitMillisecond:
		return timestamp * 1_000
	case TimeUnitNanosecond:
		return timestamp / 1_000
	default:
		// TimeUnitMicrosecond or unknown, default to microseconds
		return timestamp
	}
}

// ValidateTimeUnit validates that the time unit is one of the supported values.
func ValidateTimeUnit(unit string) (TimeUnit, error) {
	switch TimeUnit(unit) {
	case TimeUnitSecond, TimeUnitMillisecond, TimeUnitMicrosecond, TimeUnitNanosecond:
		return TimeUnit(unit), nil
	default:
		return "", fmt.Errorf("invalid time unit '%s': must be one of 's', 'ms', 'us', 'ns'", unit)
	}
}
