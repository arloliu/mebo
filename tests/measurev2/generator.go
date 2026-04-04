package main

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/arloliu/mebo/internal/hash"
)

// TestData holds generated test data for benchmarks.
// All data is pre-allocated in flat arrays for cache-friendly access.
type TestData struct {
	MetricIDs  []uint64
	Timestamps []int64   // length: NumMetrics * PointsPerMetric
	Values     []float64 // length: NumMetrics * PointsPerMetric
	StartTime  time.Time
	Config     DataConfig
}

// GenerateTestData creates realistic time-series data for benchmarks.
//
// Characteristics:
//   - Timestamps: 1-second intervals with configurable jitter (simulates real monitoring)
//   - Values: random walk with configurable jitter (simulates slowly-changing metrics)
//   - Metric IDs: xxHash64 hashed from sequential names for consistency
//   - Fixed seed for reproducibility
func GenerateTestData(config DataConfig) *TestData {
	totalPoints := config.NumMetrics * config.PointsPerMetric
	rng := rand.New(rand.NewSource(config.Seed))

	data := &TestData{
		MetricIDs:  make([]uint64, config.NumMetrics),
		Timestamps: make([]int64, totalPoints),
		Values:     make([]float64, totalPoints),
		StartTime:  time.Unix(1700000000, 0),
		Config:     config,
	}

	// Generate metric IDs
	for i := range config.NumMetrics {
		name := fmt.Sprintf("metric.%d", i+1000)
		data.MetricIDs[i] = hash.ID(name)
	}

	// Generate timestamps and values
	baseInterval := time.Second
	jitterPct := config.TSJitterPct / 100.0
	baseValue := 100.0
	deltaPct := config.ValueJitterPct / 100.0

	for i := range config.NumMetrics {
		currentTime := data.StartTime
		currentValue := baseValue + float64(i)*10.0

		for j := range config.PointsPerMetric {
			idx := i*config.PointsPerMetric + j

			// Timestamp with jitter (microseconds)
			jitterFactor := (rng.Float64()*2.0 - 1.0) * jitterPct
			jitter := time.Duration(float64(baseInterval) * jitterFactor)
			currentTime = currentTime.Add(baseInterval + jitter)
			data.Timestamps[idx] = currentTime.UnixMicro()

			// Value with small delta (random walk)
			deltaFactor := (rng.Float64()*2.0 - 1.0) * deltaPct
			delta := currentValue * deltaFactor
			currentValue += delta
			data.Values[idx] = currentValue
		}
	}

	return data
}

// SlicePoints creates a view of the test data with only the first numPoints per metric.
func (td *TestData) SlicePoints(numPoints int) *TestData {
	if numPoints >= td.Config.PointsPerMetric {
		return td
	}

	totalPoints := td.Config.NumMetrics * numPoints
	sliced := &TestData{
		MetricIDs:  td.MetricIDs,
		Timestamps: make([]int64, totalPoints),
		Values:     make([]float64, totalPoints),
		StartTime:  td.StartTime,
		Config: DataConfig{
			NumMetrics:      td.Config.NumMetrics,
			PointsPerMetric: numPoints,
			ValueJitterPct:  td.Config.ValueJitterPct,
			TSJitterPct:     td.Config.TSJitterPct,
			Seed:            td.Config.Seed,
		},
	}

	for i := range td.Config.NumMetrics {
		for j := range numPoints {
			srcIdx := i*td.Config.PointsPerMetric + j
			dstIdx := i*numPoints + j
			sliced.Timestamps[dstIdx] = td.Timestamps[srcIdx]
			sliced.Values[dstIdx] = td.Values[srcIdx]
		}
	}

	return sliced
}
