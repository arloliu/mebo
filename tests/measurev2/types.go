package main

import (
	"strings"
	"time"

	"github.com/arloliu/mebo/format"
)

// DefaultProfile is the profile used when -profile is not given.
const DefaultProfile = "mix_monitoring"

// Mixed-blob timestamps: Gorilla (PVLDB 8(12), 2015, §4.1.1) found about 96% of production timestamps
// on a regular delta-of-delta of zero, and Prometheus snaps scrape jitter within 2 ms to the schedule
// (--scrape.timestamp-tolerance), so the remaining 4% miss the grid by 2-10 ms (the 10 ms bound is an assumption).
const (
	mixTSJitterShare = 0.04
	mixTSJitterMinMs = 2
	mixTSJitterMaxMs = 10
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
	Name       string `json:"name"`
	Decimals   int    `json:"decimals"`              // value quantization (decimal places); <0 = full precision
	ValueKind  string `json:"value_kind,omitempty"`  // "gauge" | "counter" | "sparse"
	IntervalMs int64  `json:"interval_ms,omitempty"` // scrape interval
	BurstyGaps bool   `json:"bursty_gaps,omitempty"` // inject periodic gaps (large dod spikes)
	// Hold is the probability that a gauge point repeats the previous value (0 = never).
	Hold float64 `json:"hold,omitempty"`
	// StepPct is a gauge's maximum step in percent of the current value (0 = the default 0.5).
	StepPct float64 `json:"step_pct,omitempty"`
	// Parts makes this a mixed blob: each part generates its share of the metrics' values,
	// and every metric gets aligned timestamps (see TSJitterShare).
	Parts []ProfilePart `json:"parts,omitempty"`
	// TSJitterShare is the share of a mixed blob's timestamps that miss the scrape grid;
	// each miss is offset by TSJitterMinMs..TSJitterMaxMs in either direction, and every other point is exactly on the grid.
	TSJitterShare float64 `json:"ts_jitter_share,omitempty"`
	TSJitterMinMs float64 `json:"ts_jitter_min_ms,omitempty"`
	TSJitterMaxMs float64 `json:"ts_jitter_max_ms,omitempty"`
	// Legacy selects the original full-precision random walk (GenerateTestData),
	// the only generator that the -value-jitter and -ts-jitter flags affect.
	Legacy bool `json:"legacy,omitempty"`
}

// ProfilePart is one component of a mixed profile.
type ProfilePart struct {
	Share   float64 `json:"share"` // share of the blob's metrics
	Profile Profile `json:"profile"`
}

// mixGauge is a 15 s gauge component for the mixed profiles.
func mixGauge(decimals int, hold, stepPct float64) Profile {
	return Profile{Name: "gauge", Decimals: decimals, ValueKind: "gauge", IntervalMs: 15000, Hold: hold, StepPct: stepPct}
}

// mixProfile builds a 15 s mixed profile with aligned timestamps.
func mixProfile(name string, parts ...ProfilePart) Profile {
	return Profile{
		Name: name, IntervalMs: 15000, Parts: parts,
		TSJitterShare: mixTSJitterShare, TSJitterMinMs: mixTSJitterMinMs, TSJitterMaxMs: mixTSJitterMaxMs,
	}
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
		// The pre-2026-10 default: a full-precision ±0.5% random walk at 1 s with ±0.1% timestamp jitter.
		{Name: "legacy_random_walk", Decimals: -1, Legacy: true},
		// Mixed blobs calibrated to the owner's target of about 3.8 B/point for Chimp,
		// measured at 100 metrics × 150 points with shared DeltaPacked timestamps and no compression.
		// The shares are assumptions: one aggregate figure cannot pin them down,
		// so the four mixes differ in structure and each lands near the target.
		mixProfile("mix_monitoring",
			ProfilePart{0.35, mixGauge(2, 0.3, 0)},
			ProfilePart{0.20, Profile{Name: "counter", Decimals: 0, ValueKind: "counter", IntervalMs: 15000}},
			ProfilePart{0.18, Profile{Name: "sparse", Decimals: 2, ValueKind: "sparse", IntervalMs: 15000}},
			ProfilePart{0.27, mixGauge(-1, 0, 0)},
		),
		mixProfile("mix_sensor",
			ProfilePart{0.50, mixGauge(2, 0.3, 0)},
			ProfilePart{0.20, mixGauge(1, 0.5, 0.03)},
			ProfilePart{0.11, Profile{Name: "sparse", Decimals: 2, ValueKind: "sparse", IntervalMs: 15000}},
			ProfilePart{0.19, mixGauge(-1, 0, 0)},
		),
		mixProfile("mix_integer",
			ProfilePart{0.30, Profile{Name: "counter", Decimals: 0, ValueKind: "counter", IntervalMs: 15000}},
			ProfilePart{0.21, Profile{Name: "sparse", Decimals: 2, ValueKind: "sparse", IntervalMs: 15000}},
			ProfilePart{0.35, mixGauge(2, 0, 0)},
			ProfilePart{0.14, mixGauge(-1, 0, 0)},
		),
		mixProfile("mix_fullprec",
			ProfilePart{0.20, mixGauge(2, 0.5, 0)},
			ProfilePart{0.10, Profile{Name: "counter", Decimals: 0, ValueKind: "counter", IntervalMs: 15000}},
			ProfilePart{0.05, Profile{Name: "sparse", Decimals: 2, ValueKind: "sparse", IntervalMs: 15000}},
			ProfilePart{0.40, mixGauge(-1, 0.5, 0)},
			ProfilePart{0.25, mixGauge(-1, 0, 0)},
		),
	}
}

// DataConfig holds data generation parameters.
type DataConfig struct {
	NumMetrics      int     `json:"num_metrics"`
	PointsPerMetric int     `json:"points_per_metric"`
	ValueJitterPct  float64 `json:"value_jitter_pct"`
	TSJitterPct     float64 `json:"ts_jitter_pct"`
	Seed            int64   `json:"seed"`
	Profile         string  `json:"profile,omitempty"` // profile name; empty or legacy_random_walk = legacy generators
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
	// ProfileSpec is the full definition of the profile the data came from, mixed parts included.
	ProfileSpec *Profile `json:"profile_spec,omitempty"`
}

// BenchMetrics holds standard Go benchmark metrics.
type BenchMetrics struct {
	NsPerOp     float64 `json:"ns_per_op"`
	BytesPerOp  int64   `json:"bytes_per_op"`
	AllocsPerOp int64   `json:"allocs_per_op"`
}

// MatrixSizes holds one encoding combo's identity and encoded size at the fixed matrix data size.
// It is deterministic and measured for every combo in every mode.
type MatrixSizes struct {
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
}

// MatrixResult holds all benchmark results for one encoding combo at a fixed data size,
// in the legacy schema that a run without -profiles writes.
type MatrixResult struct {
	MatrixSizes

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
