package main

import (
	"strings"
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
		{format.TypeALP, "alp"},
		{format.TypeALPRLE, "alprle"},
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
		{format.TypeALP, "alp"},
		{format.TypeALPRLE, "alprle"},
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

// Profile names a realistic generator combination.
type Profile struct {
	Name       string
	Decimals   int    // value quantization (decimal places); <0 = full precision
	ValueKind  string // "gauge" | "counter" | "sparse"
	IntervalMs int64  // scrape interval
	BurstyGaps bool   // inject periodic gaps (large dod spikes)
	// Hold is the probability that a gauge point repeats the previous value (0 = never).
	Hold float64
	// StepPct is a gauge's maximum step in percent of the current value (0 = the default 0.5).
	StepPct float64
}

// Profiles returns the catalog of realistic generator profiles.
func Profiles() []Profile {
	return []Profile{
		{Name: "decimal_gauge_2dp", Decimals: 2, ValueKind: "gauge", IntervalMs: 15000},
		{Name: "decimal_gauge_4dp", Decimals: 4, ValueKind: "gauge", IntervalMs: 15000},
		{Name: "counter", Decimals: 0, ValueKind: "counter", IntervalMs: 15000},
		{Name: "sparse_constant", Decimals: 2, ValueKind: "sparse", IntervalMs: 60000},
		{Name: "regular_scrape_60s", Decimals: 2, ValueKind: "gauge", IntervalMs: 60000},
		{Name: "bursty_scrape", Decimals: 2, ValueKind: "gauge", IntervalMs: 15000, BurstyGaps: true},
		{Name: "worst_case", Decimals: -1, ValueKind: "gauge", IntervalMs: 1000}, // full-precision random walk (old default)
		// Calibrated production-like shapes (docs/specs/alp-rle-design.md):
		// under the production encoder options, Chimp costs about 2-4.5 B/point on these,
		// bracketing the ~3.3 B/point production figure.
		{Name: "cal_2dp_hold30", Decimals: 2, ValueKind: "gauge", IntervalMs: 15000, Hold: 0.3},
		{Name: "cal_2dp_hold50", Decimals: 2, ValueKind: "gauge", IntervalMs: 15000, Hold: 0.5},
		{Name: "cal_2dp_hold70", Decimals: 2, ValueKind: "gauge", IntervalMs: 15000, Hold: 0.7},
		{Name: "cal_2dp_step0.005", Decimals: 2, ValueKind: "gauge", IntervalMs: 15000, StepPct: 0.005},
		{Name: "cal_1dp_step0.03", Decimals: 1, ValueKind: "gauge", IntervalMs: 15000, StepPct: 0.03},
		{Name: "cal_1dp_step0.01", Decimals: 1, ValueKind: "gauge", IntervalMs: 15000, StepPct: 0.01},
	}
}

// DataConfig holds data generation parameters.
type DataConfig struct {
	NumMetrics      int     `json:"num_metrics"`
	PointsPerMetric int     `json:"points_per_metric"`
	ValueJitterPct  float64 `json:"value_jitter_pct"`
	TSJitterPct     float64 `json:"ts_jitter_pct"`
	Seed            int64   `json:"seed"`
	Profile         string  `json:"profile,omitempty"` // realistic profile name; empty = legacy generators
}

// findProfile returns the named profile from the catalog.
func findProfile(name string) (Profile, bool) {
	for _, p := range Profiles() {
		if p.Name == name {
			return p, true
		}
	}

	return Profile{}, false
}

// profileNames lists the available profile names for help/error text.
func profileNames() string {
	names := make([]string, 0, len(Profiles()))
	for _, p := range Profiles() {
		names = append(names, p.Name)
	}

	return strings.Join(names, ", ")
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

	// Random-access lookup at a uniformly random index per metric (see
	// randomAccessPattern in bench.go for why the index isn't fixed).
	RandomValueAt     BenchMetrics `json:"random_value_at"`
	RandomTimestampAt BenchMetrics `json:"random_timestamp_at"`
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
