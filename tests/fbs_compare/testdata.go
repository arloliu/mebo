package fbscompare

import (
	"fmt"
	"math/rand"
	"time"
)

// TestDataConfig configures the generation of realistic test data.
type TestDataConfig struct {
	NumMetrics     int           // Number of metrics to generate
	NumPoints      int           // Number of data points per metric
	StartTime      time.Time     // Starting timestamp
	BaseInterval   time.Duration // Base interval between points (e.g., 1 second)
	JitterPercent  float64       // Jitter as percentage of base interval (e.g., 0.05 for 5%)
	BaseValue      float64       // Starting value for metrics
	DeltaPercent   float64       // Maximum delta percentage between consecutive points
	MetricIDOffset uint64        // Starting metric ID
}

// DefaultTestDataConfig returns a configuration matching the PLAN.md requirements:
// - Regular 1-second intervals with 5% jitter (950-1050ms)
// - Random values with low delta percentage between points
// - Typical real-world scenario: 200 metrics
func DefaultTestDataConfig(numMetrics, numPoints int) TestDataConfig {
	return TestDataConfig{
		NumMetrics:     numMetrics,
		NumPoints:      numPoints,
		StartTime:      time.Unix(1700000000, 0), // Fixed timestamp for reproducibility
		BaseInterval:   time.Second,
		JitterPercent:  0.05, // 5% jitter (950-1050ms)
		BaseValue:      100.0,
		DeltaPercent:   0.02, // 2% max delta between points
		MetricIDOffset: 1000,
	}
}

// GenerateTestData generates realistic numeric metric data for benchmarking.
// Returns a slice of MetricData with timestamps and values.
func GenerateTestData(cfg TestDataConfig) []MetricData {
	// Fixed seed for reproducibility
	rng := rand.New(rand.NewSource(42)) //nolint: gosec
	metrics := make([]MetricData, cfg.NumMetrics)

	for m := 0; m < cfg.NumMetrics; m++ {
		metricID := cfg.MetricIDOffset + uint64(m) //nolint: gosec
		timestamps := make([]int64, cfg.NumPoints)
		values := make([]float64, cfg.NumPoints)

		currentTime := cfg.StartTime
		currentValue := cfg.BaseValue + float64(m)*10.0 // Different base for each metric

		for i := 0; i < cfg.NumPoints; i++ {
			// Add jittered timestamp: base interval ± jitter
			jitterRange := float64(cfg.BaseInterval) * cfg.JitterPercent
			jitter := time.Duration((rng.Float64()*2 - 1) * jitterRange) // -jitter to +jitter
			currentTime = currentTime.Add(cfg.BaseInterval + jitter)
			timestamps[i] = currentTime.UnixMicro() // Use microseconds like actual use case

			// Add small random delta to value
			deltaRange := currentValue * cfg.DeltaPercent
			delta := (rng.Float64()*2 - 1) * deltaRange // -delta% to +delta%
			currentValue += delta
			values[i] = currentValue
		}

		metrics[m] = MetricData{
			ID:         metricID,
			Timestamps: timestamps,
			Values:     values,
		}
	}

	return metrics
}

// GenerateTextTestData generates realistic text metric data for benchmarking.
// Text values are formatted as "value_<metricID>_<pointIndex>" to simulate real log/status messages.
// Returns a slice of TextMetricData with timestamps and text values.
func GenerateTextTestData(cfg TestDataConfig) []TextMetricData {
	// Fixed seed for reproducibility
	rng := rand.New(rand.NewSource(42)) //nolint: gosec
	metrics := make([]TextMetricData, cfg.NumMetrics)

	// Common status/log prefixes for realistic text values
	prefixes := []string{"INFO", "WARN", "DEBUG", "ERROR", "TRACE"}

	for m := 0; m < cfg.NumMetrics; m++ {
		metricID := cfg.MetricIDOffset + uint64(m) //nolint: gosec
		timestamps := make([]int64, cfg.NumPoints)
		values := make([]string, cfg.NumPoints)

		currentTime := cfg.StartTime

		for i := 0; i < cfg.NumPoints; i++ {
			// Add jittered timestamp: base interval ± jitter
			jitterRange := float64(cfg.BaseInterval) * cfg.JitterPercent
			jitter := time.Duration((rng.Float64()*2 - 1) * jitterRange) // -jitter to +jitter
			currentTime = currentTime.Add(cfg.BaseInterval + jitter)
			timestamps[i] = currentTime.UnixMicro() // Use microseconds

			// Generate realistic text value (e.g., log message)
			prefix := prefixes[rng.Intn(len(prefixes))]
			values[i] = fmt.Sprintf("[%s] metric_%d event_%d processing_complete", prefix, metricID, i)
		}

		metrics[m] = TextMetricData{
			ID:         metricID,
			Timestamps: timestamps,
			Values:     values,
			Tags:       nil, // No tags by default
		}
	}

	return metrics
}

// BenchmarkSizes returns typical real-world benchmark data sizes.
// Per PLAN.md:
// - 10 points: Small time window (10 seconds of 1s interval data)
// - 100 points: Medium time window (~1.5 minutes)
// - 250 points: Large time window (~4 minutes)
// Note: 200 metrics × 250 points = 50,000 < 65,535 (uint16 offset limit with safety margin)
func BenchmarkSizes() []struct {
	Name   string
	Points int
} {
	return []struct {
		Name   string
		Points int
	}{
		{"10pts", 10},
		{"100pts", 100},
		{"250pts", 250},
	}
}
