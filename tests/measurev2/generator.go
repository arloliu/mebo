package main

import (
	"fmt"
	"math"
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
	rng := rand.New(rand.NewSource(config.Seed)) //nolint:gosec // seeded PRNG for reproducible test data

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

// GenerateSharedTimestampData creates test data where all metrics share identical timestamps.
// This models the typical monitoring scenario: all sensors sampled at the same schedule.
// Values still vary per metric (random walk with jitter).
func GenerateSharedTimestampData(config DataConfig) *TestData {
	totalPoints := config.NumMetrics * config.PointsPerMetric
	rng := rand.New(rand.NewSource(config.Seed)) //nolint:gosec // seeded PRNG for reproducible test data

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

	// Generate a single shared timestamp sequence with jitter
	baseInterval := time.Second
	jitterPct := config.TSJitterPct / 100.0
	sharedTS := make([]int64, config.PointsPerMetric)
	currentTime := data.StartTime

	for j := range config.PointsPerMetric {
		jitterFactor := (rng.Float64()*2.0 - 1.0) * jitterPct
		jitter := time.Duration(float64(baseInterval) * jitterFactor)
		currentTime = currentTime.Add(baseInterval + jitter)
		sharedTS[j] = currentTime.UnixMicro()
	}

	// Assign same timestamps to all metrics, generate independent values
	baseValue := 100.0
	deltaPct := config.ValueJitterPct / 100.0

	for i := range config.NumMetrics {
		currentValue := baseValue + float64(i)*10.0

		for j := range config.PointsPerMetric {
			idx := i*config.PointsPerMetric + j
			data.Timestamps[idx] = sharedTS[j]

			deltaFactor := (rng.Float64()*2.0 - 1.0) * deltaPct
			delta := currentValue * deltaFactor
			currentValue += delta
			data.Values[idx] = currentValue
		}
	}

	return data
}

// quantize rounds v to the given number of decimal places.
// When decimals < 0 the value is returned unchanged (full precision).
func quantize(v float64, decimals int) float64 {
	if decimals < 0 {
		return v
	}

	scale := math.Pow(10, float64(decimals))

	return math.Round(v*scale) / scale
}

// GenerateProfile creates test data according to a named Profile.
//
// ValueKind controls the value generation strategy:
//   - "gauge":   bounded small-step random walk, quantized to p.Decimals decimal places
//   - "counter": monotonic integer-valued series
//   - "sparse":  long runs of identical values (constant blocks) with rare steps
//
// When p.Decimals < 0, values are generated at full precision (old default behaviour).
// Timestamps use p.IntervalMs as the base scrape interval with a small sub-ms jitter.
// When p.BurstyGaps is true, a periodic +5 s gap is injected every ~50 points to
// simulate scrape misses.
func GenerateProfile(p Profile, cfg DataConfig) *TestData {
	totalPoints := cfg.NumMetrics * cfg.PointsPerMetric
	rng := rand.New(rand.NewSource(cfg.Seed)) //nolint:gosec // seeded PRNG for reproducible test data

	data := &TestData{
		MetricIDs:  make([]uint64, cfg.NumMetrics),
		Timestamps: make([]int64, totalPoints),
		Values:     make([]float64, totalPoints),
		StartTime:  time.Unix(1700000000, 0),
		Config:     cfg,
	}

	// Generate metric IDs (same hashing as GenerateTestData for consistency)
	for i := range cfg.NumMetrics {
		name := fmt.Sprintf("metric.%d", i+1000)
		data.MetricIDs[i] = hash.ID(name)
	}

	baseInterval := time.Duration(p.IntervalMs) * time.Millisecond
	// Sub-ms jitter: ±0.5 ms expressed as a fraction of the interval
	jitterFrac := 0.0005 * float64(time.Millisecond) / float64(baseInterval)

	const burstyPeriod = 50 // inject a gap every 50 points
	const burstyGap = 5 * time.Second

	for i := range cfg.NumMetrics {
		currentTime := data.StartTime
		currentValue := 100.0 + float64(i)*10.0

		for j := range cfg.PointsPerMetric {
			idx := i*cfg.PointsPerMetric + j

			// Timestamp
			advance := baseInterval
			if p.BurstyGaps && j > 0 && j%burstyPeriod == 0 {
				advance += burstyGap
			} else {
				jitterFactor := (rng.Float64()*2.0 - 1.0) * jitterFrac
				jitter := time.Duration(float64(baseInterval) * jitterFactor)
				advance += jitter
			}

			currentTime = currentTime.Add(advance)
			data.Timestamps[idx] = currentTime.UnixMicro()

			// Value
			switch p.ValueKind {
			case "counter":
				// Monotonic integer-valued; step size 1–10 per point
				step := math.Floor(rng.Float64()*10) + 1
				currentValue += step
				data.Values[idx] = currentValue

			case "sparse":
				// Constant for long runs; rare small step (1 in 20 chance)
				if rng.Float64() < 0.05 {
					step := (rng.Float64()*2.0 - 1.0) * 1.0
					currentValue += step
				}

				data.Values[idx] = quantize(currentValue, p.Decimals)

			default: // "gauge" and anything else
				// Bounded small-step random walk: ±0.5% of current value per step
				deltaFrac := (rng.Float64()*2.0 - 1.0) * 0.005
				currentValue += currentValue * deltaFrac
				data.Values[idx] = quantize(currentValue, p.Decimals)
			}
		}
	}

	return data
}

// shareTimestamps returns a copy of td where every metric reuses the first
// metric's timestamp series, for the shared-timestamp benchmark matrix.
func (td *TestData) shareTimestamps() *TestData {
	ppm := td.Config.PointsPerMetric
	ts := make([]int64, len(td.Timestamps))
	for i := range td.Config.NumMetrics {
		copy(ts[i*ppm:(i+1)*ppm], td.Timestamps[:ppm])
	}

	return &TestData{
		MetricIDs:  td.MetricIDs,
		Timestamps: ts,
		Values:     td.Values,
		StartTime:  td.StartTime,
		Config:     td.Config,
	}
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
