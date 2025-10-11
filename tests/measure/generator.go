package main

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/arloliu/mebo/internal/hash"
)

// Config holds the configuration parameters for test data generation.
type Config struct {
	NumMetrics      int      // Number of metrics to generate
	MaxPoints       int      // Maximum points per metric
	ValueJitter     float64  // Value jitter percentage (e.g., 5.0 = 5%)
	TimestampJitter float64  // Timestamp jitter percentage (e.g., 2.0 = 2%)
	Seed            int64    // Random seed for reproducibility
	DataSource      string   // Data source: "simulated" or filename
	TimeUnit        TimeUnit // Time unit for input data (only used with real-world data)
}

// TestData holds the generated test data for measurements.
// All data is generated at once with MaxPoints per metric to ensure
// consistency when testing different point counts via slicing.
type TestData struct {
	MetricIDs  []uint64  // Metric IDs (length: NumMetrics)
	Timestamps []int64   // Timestamps in microseconds (length: NumMetrics * MaxPoints)
	Values     []float64 // Values (length: NumMetrics * MaxPoints)
	StartTime  time.Time // Base start time
	Config     Config    // Configuration used to generate this data
}

// GenerateTestData creates test data according to the given configuration.
//
// The function generates realistic time-series data with configurable jitter:
//   - Metric IDs are generated using hash.ID() for consistency
//   - Timestamps follow 1-second intervals with configurable jitter
//   - Values follow a random walk pattern with configurable jitter
//   - All data uses a fixed seed for reproducibility
//
// Parameters:
//   - config: Configuration parameters for data generation
//
// Returns:
//   - *TestData: Generated test data ready for measurements
//
// Example:
//
//	config := Config{
//	    NumMetrics:      200,
//	    MaxPoints:       200,
//	    ValueJitter:     5.0,
//	    TimestampJitter: 2.0,
//	    Seed:            42,
//	}
//	data := GenerateTestData(config)
func GenerateTestData(config Config) *TestData {
	totalPoints := config.NumMetrics * config.MaxPoints
	rng := rand.New(rand.NewSource(config.Seed))

	data := &TestData{
		MetricIDs:  make([]uint64, config.NumMetrics),
		Timestamps: make([]int64, totalPoints),
		Values:     make([]float64, totalPoints),
		StartTime:  time.Unix(1700000000, 0), // Fixed start time for consistency
		Config:     config,
	}

	// Generate metric IDs
	for i := 0; i < config.NumMetrics; i++ {
		name := fmt.Sprintf("metric.%d", i+1000)
		data.MetricIDs[i] = hash.ID(name)
	}

	// Generate timestamps and values
	baseInterval := time.Second
	jitterPercent := config.TimestampJitter / 100.0
	baseValue := 100.0
	deltaPercent := config.ValueJitter / 100.0

	for i := 0; i < config.NumMetrics; i++ {
		currentTime := data.StartTime
		currentValue := baseValue + float64(i)*10.0

		for j := 0; j < config.MaxPoints; j++ {
			idx := i*config.MaxPoints + j

			// Timestamp with jitter (microseconds)
			// Jitter range: ±jitterPercent of baseInterval
			jitterFactor := (rng.Float64()*2.0 - 1.0) * jitterPercent
			jitter := time.Duration(float64(baseInterval) * jitterFactor)
			currentTime = currentTime.Add(baseInterval + jitter)
			data.Timestamps[idx] = currentTime.UnixMicro()

			// Value with small delta (random walk)
			// Delta range: ±deltaPercent of currentValue
			deltaFactor := (rng.Float64()*2.0 - 1.0) * deltaPercent
			delta := currentValue * deltaFactor
			currentValue += delta
			data.Values[idx] = currentValue
		}
	}

	return data
}

// SlicePoints creates a new TestData with only the first numPoints per metric.
//
// This enables fair comparison across different point counts by using
// the same underlying data, just sliced to different lengths.
//
// Parameters:
//   - numPoints: Number of points per metric to include (must be <= MaxPoints)
//
// Returns:
//   - TestData: New TestData with sliced timestamps and values
//
// Example:
//
//	fullData := GenerateTestData(config)  // 200 points per metric
//	data10 := fullData.SlicePoints(10)    // First 10 points per metric
//	data50 := fullData.SlicePoints(50)    // First 50 points per metric
func (td *TestData) SlicePoints(numPoints int) TestData {
	if numPoints > td.Config.MaxPoints {
		numPoints = td.Config.MaxPoints
	}

	numMetrics := len(td.MetricIDs)
	totalPoints := numMetrics * numPoints

	sliced := TestData{
		MetricIDs:  td.MetricIDs, // Share metric IDs (no copy needed)
		Timestamps: make([]int64, totalPoints),
		Values:     make([]float64, totalPoints),
		StartTime:  td.StartTime,
		Config:     td.Config,
	}

	// Copy first numPoints for each metric
	for i := 0; i < numMetrics; i++ {
		for j := 0; j < numPoints; j++ {
			srcIdx := i*td.Config.MaxPoints + j
			dstIdx := i*numPoints + j
			sliced.Timestamps[dstIdx] = td.Timestamps[srcIdx]
			sliced.Values[dstIdx] = td.Values[srcIdx]
		}
	}

	return sliced
}
