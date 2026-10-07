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
| `-profile` | `mix_monitoring` | Data profile; empty selects `legacy_random_walk`; an error with `-profiles` |
| `-metrics` | 100 | Number of metrics to generate |
| `-points` | 150 | Points per metric |
| `-value-jitter` | 0.5 | `legacy_random_walk` only: value jitter % (±0.5% random walk) |
| `-ts-jitter` | 0.1 | `legacy_random_walk` only: timestamp jitter % (±0.1% of the 1 s interval) |
| `-output` | stdout | Output JSON file path; an error with `-profiles` |
| `-pretty` | false | Pretty-print JSON |
| `-verbose` | false | Progress output on stderr |
| `-benchtime` | `1s` | Target time of each `testing.Benchmark` call, 10ms to 10s |

Without `-profiles` the tool measures one data set with every cell timed and writes the legacy schema, as it always has.

### Several data sets in one process (`-profiles`)

| Flag | Default | Description |
|------|---------|-------------|
| `-profiles` | — | `report` (the frozen report manifest, `reportProfiles` in `manifest.go`), `all`, or a comma list |
| `-outdir` | — | Required; must not exist. Gets `profiles/matrix_<profile>.json`, and `main.json` when `mix_monitoring` is listed |
| `-cells` | `full` | `full` (30 combos × 5 operations everywhere), `report` (420 cells) or `wide` (780 cells) |
| `-order` | `forward` | `reverse` runs data sets, combos and operations backwards |
| `-sizes-only` | false | Sizes and scaling only; nothing is timed |
| `-run-id`, `-source`, `-tools`, `-layout`, `-round`, `-rounds` | generated, empty, empty, -1, -1, 0 | Provenance that `layouts.sh` sets |

With `-cells report` the main data set times every combo and operation,
and every other profile times encode, iterate and `ValueAt` for the five Shared DeltaPacked combos and Delta + Gorilla;
`-cells wide` adds iterate for the other 24 combos.
Sizes and scaling are measured for every combo in every mode.
These files use the version-1 schema (`format_version: 1`), described in `docs/specs/measurev2-fast-report-runs-design.md`.

## Layout-averaged report runs

```bash
# From the repository root: check the report tools, then about 10 minutes of pinned benchmarks.
make bench-report
# or
tests/measurev2/layouts.sh -o $TMPDIR/perf [-cpu 6] [-benchtime 50ms] [-cells report] [-rounds 4|2]
```

`layouts.sh` (implemented in `layouts.py`) stages the source the repository reports with `git ls-files`,
builds four binaries whose repository code sits at different addresses (a padding function of 0, 1, 2 or 4 steps),
checks that every repository function moved, and runs the schedule:
each layout once per round, pinned to one verified CPU with `GOMAXPROCS=1`, alternating forward and reverse order.
It then merges the runs with `merge_layouts.py`:

| Path | Content |
|------|---------|
| `OUTDIR/merged/` | `main.json` and `profiles/`: medians, per-layout medians and every run of each cell |
| `OUTDIR/raw/r<R>_L<K>/` | One invocation's output plus `wrapper.json` (binary SHA-256, peak RSS) |
| `OUTDIR/layouts/` | Each binary's symbol table and `go version -m`, and the movement check |
| `OUTDIR/provenance.json` | Source and tool hashes, stage times, skipped files, invocations |

A failure in any step leaves no `merged/` directory.
`validate.sh -o DIR` runs the acceptance gates (sizes, short benchtime, one process, reproducibility, movement, time);
`validate.sh --calibrate` prints the distributions the thresholds in `acceptance_thresholds.json` are set from.
Run either with nothing else on the machine at all:
even a short script that the kernel places on the pinned core's SMT sibling costs the `TimestampAt` cells 10%.
Gate 2 takes each side from the median of three runs (A↑ A↓ A↑₂ at 1 s, B↓ B↑ B↓₂ at 50 ms),
because one cell can lose up to two seconds to the core's op-cache fetch episode
(`docs/specs/index-entry-by-pointer-design.md`, "Validation without the exemptions").

`validate.sh` instruments its pinned invocations (gates 2 and 3) unless `--no-instrument` is given:
each runs under `perf stat -I 100`,
leaving `DIR.perf.csv`, its stamped `-verbose` progress lines in `DIR.stderr.log` and its wall times in `DIR.times.txt`
beside its output directory,
while `cellperf.py monitor` samples the machine every 100 ms into `OUTDIR/monitor.csv`
(busy shares of the pinned core, its sibling and the busiest other CPU,
the core's clock, Tctl, its interrupt deltas, the sibling's idle states).
`cellperf.py events` picks five hardware events (the NMI watchdog holds the sixth core counter):
on Zen 5 cycles, instructions, branch misses, decoder-sourced ops and SMT-contention slots;
elsewhere cycles, instructions, branches and branch misses; nothing when perf cannot count.
At the end `cellperf.py analyze` lists, per 1 s invocation, the main-data-set cells whose 100 ms buckets show
a mid-cell throughput episode or contention, with the bucket rows, so a failed cell is attributable from the log;
`cellperf.py analyze DIR... --all` prints every cell's rows afterwards.
The `layouts.sh` invocations of gate 4 and of report runs are not instrumented (50 ms cells, twenty times less exposed).

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

Ask the agent to "use the update-performance-report skill":
the skill at `.agents/skills/update-performance-report/` renders `docs/performance.md` from a `layouts.sh` run's `merged/` directory.

## Makefile Integration

```bash
make bench-measure   # one legacy run of the default profile, to .benchmarks/measure_results.json
make bench-report    # the layout-averaged report run, to .benchmarks/report-<time>/ (or REPORT_OUT=dir)
```
