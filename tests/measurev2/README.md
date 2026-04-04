# Encoding Benchmark Matrix (v2)

Measures all timestamp×value encoding combinations for comparison.

## Quick Start

```bash
cd tests/measurev2

# Quick run with small data
go run . -metrics 50 -points 100 -pretty -verbose

# Full benchmark (default: 200 metrics × 200 points)
go run . -pretty -verbose -output results.json
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-metrics` | 200 | Number of metrics to generate |
| `-points` | 200 | Points per metric |
| `-value-jitter` | 0.5 | Value jitter % (±0.5% random walk; semiconductor sensors: 0.01-0.5%) |
| `-ts-jitter` | 0.1 | Timestamp jitter % (±0.1% of interval; industrial protocols: <0.1%) |
| `-output` | stdout | Output JSON file path |
| `-pretty` | false | Pretty-print JSON |
| `-verbose` | false | Progress output on stderr |

## Encoding Matrix

All 9 valid timestamp × value encoding combinations:

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
`[1, 2, 5, 10, 20, 50, 100, 150, 200]` (capped by `-points`).
Shows how overhead amortizes differently per encoding.

## Using with the Agent Skill

An agent skill at `.agents/skills/update-performance-report/` can consume
this tool's JSON output to auto-update `docs/PERFORMANCE.md`:

```bash
# Step 1: Run benchmarks
cd tests/measurev2 && go run . -pretty -output /tmp/mebo_bench_results.json -verbose

# Step 2: Use the agent skill to update docs/PERFORMANCE.md
# (Ask the agent: "use the update-performance-report skill")
```

## Makefile Integration

```bash
make bench-measure
```

Runs the benchmark and saves results to `.benchmarks/measure_results.json`.
