package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Fixture directories shared with the report tools' checker.
const (
	// v1FixtureDir holds the version-1 fixtures:
	// raw_mix_monitoring.json is written here by TestRawFixtureGolden,
	// merged_counter.json by check_report_tools.py --update.
	v1FixtureDir = "../../.agents/skills/update-performance-report/scripts/testdata/v1"
	// legacyFixtureDir holds the 2026-10-04 legacy main.json and its mix_monitoring profile file.
	legacyFixtureDir = "../../.agents/skills/update-performance-report/scripts/testdata/legacy"
)

// updateGolden rewrites testdata/v1/raw_mix_monitoring.json.
var updateGolden = flag.Bool("update", false, "rewrite the raw version-1 golden fixture")

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	return m
}

func strictDecode(t *testing.T, data []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(v))
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	require.True(t, ok, "%T is not an object", v)

	return m
}

func asList(t *testing.T, v any) []any {
	t.Helper()
	l, ok := v.([]any)
	require.True(t, ok, "%T is not an array", v)

	return l
}

func rowByLabel(t *testing.T, m map[string]any, label string) map[string]any {
	t.Helper()
	for _, r := range asList(t, m["matrix"]) {
		row := asMap(t, r)
		if row["label"] == label {
			return row
		}
	}
	t.Fatalf("no row %s", label)

	return nil
}

func TestVersion1Files(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	o := mustParse(t, "-profiles", "mix_monitoring,counter", "-cells", "report", "-outdir", dir, "-metrics", "4", "-points", "12",
		"-layout", "1", "-round", "2", "-rounds", "2", "-source", "src", "-tools", "tl")
	r := newRecordingRunner(t)
	r.result = RawOp{NsPerOp: 2500, BytesPerOp: 0, AllocsPerOp: 0, N: 20, TNs: 50_000, MemAllocs: 0, MemBytes: 0}
	require.NoError(t, runProfiles(o, r))

	m := readJSONMap(t, filepath.Join(dir, "profiles", "matrix_counter.json"))
	require.InDelta(t, 1, m["format_version"], 0)
	require.Equal(t, methodBenchmark, m["method"])
	require.Equal(t, o.runID, m["run_id"])
	common := asMap(t, m["common"])
	require.Equal(t, o.runID, common["run_id"])
	require.Equal(t, "src", common["source"])
	require.Equal(t, "tl", common["tools"])
	require.Equal(t, "report", common["cells"])
	require.Equal(t, []any{"mix_monitoring", "counter"}, common["profiles"])
	require.Equal(t, cellsDigest(manifestCells(o.profiles, o.cells)), common["cells_sha256"])
	inv := asMap(t, m["invocation"])
	require.InDelta(t, 1, inv["layout"], 0)
	require.InDelta(t, 2, inv["round"], 0)
	require.Equal(t, orderForward, inv["order"])
	for _, k := range []string{"invocations", "layouts", "allocation_disagreements"} {
		require.NotContains(t, m, k, "merged-only field in a raw file")
	}

	raw := rowByLabel(t, m, "raw-raw")
	require.InDelta(t, raw["encoded_bytes"], m["raw_raw_bytes"], 0)
	for _, op := range allOperations {
		require.NotContains(t, raw, op.String(), "untimed operations stay omitted")
	}
	alp := rowByLabel(t, m, "shared-deltapacked-alp")
	require.NotContains(t, alp, "decode")
	require.NotContains(t, alp, "random_timestamp_at")
	enc := asMap(t, alp["encode"])
	for _, k := range []string{"allocs_per_op", "bytes_per_op", "mem_allocs", "mem_bytes"} {
		require.Contains(t, enc, k, "measured zeros stay in the JSON")
		require.InDelta(t, 0, enc[k], 0)
	}
	require.InDelta(t, 20, enc["n"], 0)
	require.InDelta(t, 50_000, enc["t_ns"], 0)

	data, err := os.ReadFile(filepath.Join(dir, "main.json"))
	require.NoError(t, err)
	var report Report[RawOp]
	strictDecode(t, data, &report)
	require.Equal(t, formatVersion, report.FormatVersion)
	require.Len(t, report.Matrix, 30)
	for i := range report.Matrix {
		for _, op := range allOperations {
			require.NotNil(t, *report.Matrix[i].op(op), "main data set times every cell")
		}
	}
}

func TestSizesOnlyFilesHaveNoTimings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	o := mustParse(t, "-profiles", "counter", "-sizes-only", "-outdir", dir, "-metrics", "4", "-points", "12")
	require.NoError(t, runProfiles(o, newRecordingRunner(t)))
	m := readJSONMap(t, filepath.Join(dir, "profiles", "matrix_counter.json"))
	require.Equal(t, methodSizesOnly, m["method"])
	for _, r := range asList(t, m["matrix"]) {
		for _, op := range allOperations {
			require.NotContains(t, asMap(t, r), op.String())
		}
	}
}

// TestLegacyJSONStillDecodes checks that the 2026-10-04 complete JSONs decode into both schemas' Go types.
func TestLegacyJSONStillDecodes(t *testing.T) {
	for _, name := range []string{"main.json", "matrix_mix_monitoring.json"} {
		data, err := os.ReadFile(filepath.Join(legacyFixtureDir, name))
		require.NoError(t, err)

		var legacy FullReport
		strictDecode(t, data, &legacy)
		require.Len(t, legacy.Matrix, 30)
		require.Positive(t, legacy.Matrix[0].Encode.NsPerOp)

		var rows struct {
			Matrix []MatrixRow[RawOp] `json:"matrix"`
		}
		require.NoError(t, json.Unmarshal(data, &rows))
		for i := range rows.Matrix {
			for _, op := range allOperations {
				require.NotNil(t, *rows.Matrix[i].op(op))
			}
		}
	}
}

// TestLegacyOutputKeepsItsSchema checks that a run without -profiles writes exactly the keys of the 2026-10-04 JSON.
func TestLegacyOutputKeepsItsSchema(t *testing.T) {
	out := filepath.Join(t.TempDir(), "legacy.json")
	o := mustParse(t, "-output", out, "-pretty", "-metrics", "4", "-points", "12")
	require.NoError(t, runLegacy(o, newRecordingRunner(t)))

	got := readJSONMap(t, out)
	want := readJSONMap(t, filepath.Join(legacyFixtureDir, "main.json"))
	require.Equal(t, keyPaths(want, ""), keyPaths(got, ""))
}

// keyPaths lists every object key path in v; array elements share the path "[]".
func keyPaths(v any, prefix string) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			out = append(out, prefix+"."+k)
			out = append(out, keyPaths(child, prefix+"."+k)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, keyPaths(child, prefix+"[]")...)
		}
	default:
	}
	slices.Sort(out)

	return slices.Compact(out)
}

func TestMergedReportDecodes(t *testing.T) {
	merged := []byte(`{
	  "format_version": 1, "method": "testing.Benchmark, layout-averaged", "run_id": "r",
	  "common": {"run_id": "r", "source": "", "tools": "", "goos": "linux", "goarch": "amd64", "cpu_model": "x",
	             "go_version": "go1.26.7", "build_settings": [], "gomaxprocs": 1, "cpu_affinity": [6],
	             "gogc": "100", "gomemlimit": "", "godebug": "", "benchtime": "50ms", "rounds": 2, "cells": "report",
	             "cells_sha256": "h", "profiles": ["counter"], "data_configs": []},
	  "invocations": [{"layout": 0, "round": 1, "order": "forward", "start": "2026-10-05T10:00:00Z",
	                   "end": "2026-10-05T10:00:30Z", "timestamp": "2026-10-05T10:00:00Z", "binary_sha256": "b", "peak_rss_kb": 9}],
	  "layouts": {"0": "b"},
	  "allocation_disagreements": [],
	  "raw_raw_bytes": 10,
	  "metadata": {"go_version": "go1.26.7", "os": "linux", "arch": "amd64", "num_cpu": 32, "timestamp": "2026-10-05T10:00:00Z",
	               "data_config": {"num_metrics": 1, "points_per_metric": 1, "value_jitter_pct": 0.5, "ts_jitter_pct": 0.1, "seed": 42}},
	  "matrix": [{"label": "raw-raw", "ts_encoding": "Raw", "val_encoding": "Raw", "num_metrics": 1, "points_per_metric": 1,
	              "total_points": 1, "encoded_bytes": 10, "bytes_per_point": 10, "vs_raw_ratio": 1, "space_savings_pct": 0,
	              "encode": {"ns_per_op": 5, "bytes_per_op": 0, "allocs_per_op": 0,
	                         "runs": [{"layout": 0, "round": 1, "ns_per_op": 5, "bytes_per_op": 0, "allocs_per_op": 0,
	                                   "n": 2, "t_ns": 10, "mem_allocs": 0, "mem_bytes": 0}],
	                         "layout_ns_per_op": {"0": 5}, "iqr_rel": 0}}],
	  "scaling": []
	}`)
	var report Report[MergedOp]
	strictDecode(t, merged, &report)
	require.Equal(t, methodLayoutAveraged, report.Method)
	require.NotNil(t, report.AllocationDisagreements)
	require.Empty(t, *report.AllocationDisagreements)
	require.Nil(t, report.Matrix[0].Decode)
	require.InDelta(t, 5.0, report.Matrix[0].Encode.LayoutNsPerOp["0"], 0)

	again, err := json.Marshal(report)
	require.NoError(t, err)
	require.Contains(t, string(again), `"allocation_disagreements":[]`, "an empty list stays present in merged output")
}

// TestRawFixtureGolden pins a raw version-1 file, normalized for time and machine,
// which check_report_tools.py validates with the Python schema: Go writes, Python reads.
func TestRawFixtureGolden(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	o := mustParse(t, "-profiles", "mix_monitoring", "-cells", "report", "-outdir", dir, "-metrics", "4", "-points", "12",
		"-benchtime", "50ms", "-run-id", "golden", "-source", "head=0 status=0 tree=0", "-tools", "sha256=0",
		"-layout", "0", "-round", "1", "-rounds", "2")
	r := newRecordingRunner(t)
	r.result = RawOp{NsPerOp: 2500.5, BytesPerOp: 96, AllocsPerOp: 3, N: 2, TNs: 5001, MemAllocs: 7, MemBytes: 193}
	require.NoError(t, runProfiles(o, r))

	data, err := os.ReadFile(filepath.Join(dir, "main.json"))
	require.NoError(t, err)
	var report Report[RawOp]
	strictDecode(t, data, &report)
	normalizeMachine(&report)
	got, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')

	path := filepath.Join(v1FixtureDir, "raw_mix_monitoring.json")
	if *updateGolden {
		require.NoError(t, os.WriteFile(path, got, 0o600))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got), "rerun with -update after a deliberate schema change")
}

// TestMergedFixtureDecodes checks that merge_layouts.py output decodes strictly into the Go types and re-encodes
// to the same JSON: Python writes, Go reads.
func TestMergedFixtureDecodes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(v1FixtureDir, "merged_counter.json"))
	require.NoError(t, err)
	var report Report[MergedOp]
	strictDecode(t, data, &report)
	require.Equal(t, methodLayoutAveraged, report.Method)
	require.Len(t, report.Invocations, 8)
	require.NotNil(t, report.AllocationDisagreements)

	again, err := json.Marshal(report)
	require.NoError(t, err)
	var want, got any
	require.NoError(t, json.Unmarshal(data, &want))
	require.NoError(t, json.Unmarshal(again, &got))
	require.Equal(t, want, got)
}

// normalizeMachine replaces what differs between machines and runs with fixed values.
func normalizeMachine(r *Report[RawOp]) {
	fixed := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	r.Invocation.Start, r.Invocation.End, r.Invocation.Timestamp = fixed, fixed.Add(time.Second), fixed
	r.Metadata.Timestamp = fixed
	r.Metadata.GoVersion, r.Common.GoVersion = "go1.26.7", "go1.26.7"
	r.Metadata.NumCPU = 1
	r.Common.CPUModel = "Test CPU"
	r.Common.BuildSettings = []BuildSetting{{Key: "-buildmode", Value: "exe"}}
	r.Common.GOMAXPROCS = 1
	r.Common.CPUAffinity = []int{6}
	r.Common.GOGC, r.Common.GOMEMLIMIT, r.Common.GODEBUG = "100", "", ""
}
