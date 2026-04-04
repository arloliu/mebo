package main

import (
	"time"

	"github.com/arloliu/mebo/format"
)

// EncodingCombo defines a timestamp+value encoding pair to benchmark.
type EncodingCombo struct {
	TSEncoding  format.EncodingType
	ValEncoding format.EncodingType
	Label       string
	SharedTS    bool // Enable shared timestamp deduplication
}

// AllCombos returns all valid timestamp×value encoding combinations.
func AllCombos() []EncodingCombo {
	tsEncodings := []struct {
		enc   format.EncodingType
		label string
	}{
		{format.TypeRaw, "raw"},
		{format.TypeDelta, "delta"},
		{format.TypeDeltaPacked, "deltapacked"},
	}

	valEncodings := []struct {
		enc   format.EncodingType
		label string
	}{
		{format.TypeRaw, "raw"},
		{format.TypeGorilla, "gorilla"},
		{format.TypeChimp, "chimp"},
	}

	combos := make([]EncodingCombo, 0, len(tsEncodings)*len(valEncodings))
	for _, ts := range tsEncodings {
		for _, val := range valEncodings {
			combos = append(combos, EncodingCombo{
				TSEncoding:  ts.enc,
				ValEncoding: val.enc,
				Label:       ts.label + "-" + val.label,
			})
		}
	}

	return combos
}

// SharedTSCombos returns encoding combinations with shared timestamps enabled.
// These use the same ts×val grid but with WithSharedTimestamps() for timestamp deduplication.
func SharedTSCombos() []EncodingCombo {
	tsEncodings := []struct {
		enc   format.EncodingType
		label string
	}{
		{format.TypeRaw, "raw"},
		{format.TypeDelta, "delta"},
		{format.TypeDeltaPacked, "deltapacked"},
	}

	valEncodings := []struct {
		enc   format.EncodingType
		label string
	}{
		{format.TypeRaw, "raw"},
		{format.TypeGorilla, "gorilla"},
		{format.TypeChimp, "chimp"},
	}

	combos := make([]EncodingCombo, 0, len(tsEncodings)*len(valEncodings))
	for _, ts := range tsEncodings {
		for _, val := range valEncodings {
			combos = append(combos, EncodingCombo{
				TSEncoding:  ts.enc,
				ValEncoding: val.enc,
				Label:       "shared-" + ts.label + "-" + val.label,
				SharedTS:    true,
			})
		}
	}

	return combos
}

// DataConfig holds data generation parameters.
type DataConfig struct {
	NumMetrics      int     `json:"num_metrics"`
	PointsPerMetric int     `json:"points_per_metric"`
	ValueJitterPct  float64 `json:"value_jitter_pct"`
	TSJitterPct     float64 `json:"ts_jitter_pct"`
	Seed            int64   `json:"seed"`
}

// ReportMetadata holds metadata about the benchmark run.
type ReportMetadata struct {
	GoVersion string     `json:"go_version"`
	OS        string     `json:"os"`
	Arch      string     `json:"arch"`
	NumCPU    int        `json:"num_cpu"`
	Timestamp time.Time  `json:"timestamp"`
	Data      DataConfig `json:"data_config"`
}

// BenchMetrics holds standard Go benchmark metrics.
type BenchMetrics struct {
	NsPerOp     float64 `json:"ns_per_op"`
	BytesPerOp  int64   `json:"bytes_per_op"`
	AllocsPerOp int64   `json:"allocs_per_op"`
}

// MatrixResult holds all benchmark results for one encoding combo at a fixed data size.
type MatrixResult struct {
	Label           string `json:"label"`
	TSEncoding      string `json:"ts_encoding"`
	ValEncoding     string `json:"val_encoding"`
	NumMetrics      int    `json:"num_metrics"`
	PointsPerMetric int    `json:"points_per_metric"`
	TotalPoints     int    `json:"total_points"`

	// Size metrics
	EncodedBytes    int     `json:"encoded_bytes"`
	BytesPerPoint   float64 `json:"bytes_per_point"`
	VsRawRatio      float64 `json:"vs_raw_ratio"`
	SpaceSavingsPct float64 `json:"space_savings_pct"`

	// Benchmark results
	Encode  BenchMetrics `json:"encode"`
	Decode  BenchMetrics `json:"decode"`
	IterSeq BenchMetrics `json:"iter_seq"`
}

// ScalingPoint holds encoded size data at a specific points-per-metric count.
type ScalingPoint struct {
	PointsPerMetric int     `json:"points_per_metric"`
	EncodedBytes    int     `json:"encoded_bytes"`
	BytesPerPoint   float64 `json:"bytes_per_point"`
}

// ScalingResult holds scaling analysis for one encoding combo.
type ScalingResult struct {
	Label       string         `json:"label"`
	TSEncoding  string         `json:"ts_encoding"`
	ValEncoding string         `json:"val_encoding"`
	NumMetrics  int            `json:"num_metrics"`
	Series      []ScalingPoint `json:"points_series"`
}

// FullReport is the top-level JSON output.
type FullReport struct {
	Metadata ReportMetadata  `json:"metadata"`
	Matrix   []MatrixResult  `json:"matrix"`
	Scaling  []ScalingResult `json:"scaling"`
}
