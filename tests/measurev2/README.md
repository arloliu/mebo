# Encoding Benchmark Matrix (v2)

Measures all timestamp×value encoding combinations for comparison.

## Quick Start

```bash
cd tests/measurev2

# Quick run with small data
go run . -metrics 50 -points 100 -pretty -verbose

# Full benchmark (default: mix_monitoring, 100 metrics × 150 points)
go run . -pretty -verbose -output results.json

# One data shape, or the pre-2026-10 default
go run . -profile cal_2dp_hold50 -pretty -output results_hold50.json
go run . -profile legacy_random_walk -metrics 200 -points 200 -pretty -output results_legacy.json
```

## Data profiles

The default profile, `mix_monitoring`, is a mixed blob:
35% 2-decimal gauges (30% of points repeat the previous value), 20% integer counters,
18% mostly-constant values and 27% full-precision gauges.
The mixed profiles (`mix_monitoring`, `mix_sensor`, `mix_integer`, `mix_fullprec`) are calibrated to about 3.8 B/point for Chimp,
measured at 100 metrics × 150 points with shared DeltaPacked timestamps and no compression.
Their shares are assumptions; one aggregate figure cannot pin them down,
so the four mixes differ in structure and each lands near that target (`TestMixCalibration`).
Mixed blobs use aligned timestamps: 96% of points land exactly on the 15 s grid
and the rest miss it by 2–10 ms (Gorilla, PVLDB 2015, §4.1.1; Prometheus `--scrape.timestamp-tolerance`).

The single-shape profiles (`decimal_gauge_2dp`, `counter`, `sparse_constant`, `worst_case`, the `cal_*` set and others)
keep one kind of metric per blob; `go run . -help` lists them all.
`legacy_random_walk` is the pre-2026-10 default: a full-precision ±0.5% random walk at 1 s with ±0.1% timestamp jitter.
`TestProfilesByteIdentical` pins every profile's data, so published numbers stay reproducible.

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-profile` | `mix_monitoring` | Data profile; empty selects `legacy_random_walk` |
| `-metrics` | 100 | Number of metrics to generate |
| `-points` | 150 | Points per metric |
| `-value-jitter` | 0.5 | `legacy_random_walk` only: value jitter % (±0.5% random walk) |
| `-ts-jitter` | 0.1 | `legacy_random_walk` only: timestamp jitter % (±0.1% of the 1 s interval) |
| `-output` | stdout | Output JSON file path |
| `-pretty` | false | Pretty-print JSON |
| `-verbose` | false | Progress output on stderr |

## Encoding Matrix

Every timestamp × value encoding combination, each also measured with shared timestamps (`shared-` labels).
The table below shows the original nine; ALP (`alp`) and ALP-RLE (`alprle`) value encodings are measured the same way.

| Timestamp | Value | Label |
|-----------|-------|-------|
| Raw | Raw | `raw-raw` (baseline) |
| Raw | Gorilla | `raw-gorilla` |
| Raw | Chimp | `raw-chimp` |
| Delta | Raw | `delta-raw` |
| Delta | Gorilla | `delta-gorilla` |
| Delta | Chimp | `delta-chimp` |
| DeltaPacked | Raw | `deltapacked-raw` |
| DeltaPacked | Gorilla | `deltapacked-gorilla` |
| DeltaPacked | Chimp | `deltapacked-chimp` |

## Output Format

The tool outputs a single JSON document with two sections:

### `matrix` — Side-by-side comparison at fixed data size

For each encoding combo, benchmarks:
- **Encoded size**: bytes total, bytes/point, savings vs raw-raw
- **Encode speed**: ns/op, B/op, allocs/op
- **Decode speed**: ns/op, B/op, allocs/op
- **Sequential iteration**: ns/op, B/op, allocs/op

### `scaling` — Bytes/point vs points-per-metric curves

For each encoding combo, measures encoded size at point counts
`[1, 2, 5, 10, 20, 50, 100, 150, 200]` (capped by `-points`, so 150 by default).
Shows how overhead amortizes differently per encoding.

## Using with the Agent Skill

An agent skill at `.agents/skills/update-performance-report/` can consume
this tool's JSON output to auto-update `docs/performance.md`:

```bash
# Step 1: Run benchmarks
cd tests/measurev2 && go run . -pretty -output /tmp/mebo_bench_results.json -verbose

# Step 2: Use the agent skill to update docs/performance.md
# (Ask the agent: "use the update-performance-report skill")
```

## Makefile Integration

```bash
make bench-measure
```

Runs the benchmark and saves results to `.benchmarks/measure_results.json`.
