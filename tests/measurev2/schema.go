package main

import "time"

// Version-1 schema identifiers.
const (
	// formatVersion is the format_version of every JSON file written with -profiles.
	formatVersion = 1
	// methodBenchmark marks a raw invocation's timings.
	methodBenchmark = "testing.Benchmark"
	// methodLayoutAveraged marks merge_layouts.py output.
	methodLayoutAveraged = "testing.Benchmark, layout-averaged"
	// methodSizesOnly marks -sizes-only output, which holds no timings and is never rendered.
	methodSizesOnly = "sizes-only"
)

// RawOp is one timed operation of one raw invocation, as testing.Benchmark measured it.
// NsPerOp is TNs / N; BytesPerOp and AllocsPerOp are testing's integer per-op values.
type RawOp struct {
	NsPerOp     float64 `json:"ns_per_op"`
	BytesPerOp  int64   `json:"bytes_per_op"`
	AllocsPerOp int64   `json:"allocs_per_op"`
	N           int64   `json:"n"`
	TNs         int64   `json:"t_ns"`
	MemAllocs   uint64  `json:"mem_allocs"`
	MemBytes    uint64  `json:"mem_bytes"`
}

// MergedRun is one raw operation inside a merged cell, tagged with the invocation it came from.
type MergedRun struct {
	Layout int `json:"layout"`
	Round  int `json:"round"`
	RawOp
}

// MergedOp is one layout-averaged operation, written by merge_layouts.py.
// NsPerOp is the median of the runs; LayoutNsPerOp maps a layout number to the median of that layout's runs.
type MergedOp struct {
	NsPerOp       float64            `json:"ns_per_op"`
	BytesPerOp    int64              `json:"bytes_per_op"`
	AllocsPerOp   int64              `json:"allocs_per_op"`
	Runs          []MergedRun        `json:"runs"`
	LayoutNsPerOp map[string]float64 `json:"layout_ns_per_op"`
	IQRRel        float64            `json:"iqr_rel"`
}

// MatrixRow is one combo's sizes plus the operations that were timed;
// an untimed operation's key is absent from the JSON.
type MatrixRow[Op any] struct {
	MatrixSizes

	Encode            *Op `json:"encode,omitempty"`
	Decode            *Op `json:"decode,omitempty"`
	IterSeq           *Op `json:"iter_seq,omitempty"`
	RandomValueAt     *Op `json:"random_value_at,omitempty"`
	RandomTimestampAt *Op `json:"random_timestamp_at,omitempty"`
}

// BuildSetting is one key of debug.BuildInfo.Settings.
type BuildSetting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Common is the metadata every file of a layout-averaged run must agree on.
// It is identical across the files one invocation writes and across the invocations merge_layouts.py combines.
type Common struct {
	RunID         string         `json:"run_id"`
	Source        string         `json:"source"`
	Tools         string         `json:"tools"`
	GOOS          string         `json:"goos"`
	GOARCH        string         `json:"goarch"`
	CPUModel      string         `json:"cpu_model"`
	GoVersion     string         `json:"go_version"`
	BuildSettings []BuildSetting `json:"build_settings"`
	GOMAXPROCS    int            `json:"gomaxprocs"`
	CPUAffinity   []int          `json:"cpu_affinity"`
	GOGC          string         `json:"gogc"`
	GOMEMLIMIT    string         `json:"gomemlimit"`
	GODEBUG       string         `json:"godebug"`
	Benchtime     string         `json:"benchtime"`
	Rounds        int            `json:"rounds"`
	Cells         string         `json:"cells"`
	CellsSHA256   string         `json:"cells_sha256"`
	Profiles      []string       `json:"profiles"`
	DataConfigs   []DataConfig   `json:"data_configs"`
}

// Invocation is the per-process provenance of a raw file; it is kept, not compared, when merging.
// Layout and Round are -1 for a standalone invocation.
type Invocation struct {
	Layout    int       `json:"layout"`
	Round     int       `json:"round"`
	Order     string    `json:"order"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Timestamp time.Time `json:"timestamp"`
}

// InvocationRecord is one merged input's invocation plus what the wrapper measured around it.
type InvocationRecord struct {
	Invocation

	BinarySHA256 string `json:"binary_sha256"`
	PeakRSSKB    int64  `json:"peak_rss_kb"`
}

// Report is a version-1 file for one data set.
// A raw invocation fills Invocation; a merged file fills Invocations, Layouts and AllocationDisagreements instead.
type Report[Op any] struct {
	FormatVersion           int                `json:"format_version"`
	Method                  string             `json:"method"`
	RunID                   string             `json:"run_id"`
	Common                  Common             `json:"common"`
	Invocation              *Invocation        `json:"invocation,omitempty"`
	Invocations             []InvocationRecord `json:"invocations,omitempty"`
	Layouts                 map[string]string  `json:"layouts,omitempty"`
	AllocationDisagreements *[]string          `json:"allocation_disagreements,omitempty"`
	RawRawBytes             int                `json:"raw_raw_bytes"`
	Metadata                ReportMetadata     `json:"metadata"`
	Matrix                  []MatrixRow[Op]    `json:"matrix"`
	Scaling                 []ScalingResult    `json:"scaling"`
}

// op returns the row's field for operation o.
func (r *MatrixRow[Op]) op(o operation) **Op {
	switch o {
	case opEncode:
		return &r.Encode
	case opDecode:
		return &r.Decode
	case opIterSeq:
		return &r.IterSeq
	case opValueAt:
		return &r.RandomValueAt
	case opTimestampAt:
		return &r.RandomTimestampAt
	}

	panic("unknown operation " + o.String())
}
