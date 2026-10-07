# measurev2 fast report runs: design

**Date:** 2026-10-05
**Status:** implemented (2026-10-05); every acceptance gate passed on the validation run (see Results).
The owner chose (2026-10-04) to keep `testing.Benchmark` as the only timing method,
to publish numbers pinned to one core with `GOMAXPROCS=1`,
and to retire the cross-timestamp iterate check from the default run, keeping it behind a flag.
The owner chose (2026-10-05) one comparison rule for every layout-averaged comparison:
a gap of at least 20% that every layout supports, with no reduced threshold.
Layout coverage is one global check that the padding moved every repository function.

Owner request (2026-10-04): the performance-report benchmark run takes 40–50 minutes;
find a way to finish in much less time while still giving enough information to analyze.

## Goal

Produce the data behind `docs/performance.md` in under 15 minutes on the development machine,
with layout-averaged timings instead of single runs,
and with sizes byte-identical to today's output.

## Where the time goes today

From the 2026-10-04 run (AMD Ryzen 9 9950X3D, Go 1.26.7).
Process durations come from file timestamps; the JSONs record no elapsed time, `GOMAXPROCS` or CPU affinity.

- `update-performance-report` runs `tests/measurev2` 17 times (SKILL.md Step 1): the main data set plus 16 profiles,
  each a separate process; consecutive profile files were written about 2 min 57 s apart.
- Each process measures 30 combos (`AllCombos`, `SharedTSCombos` in `tests/measurev2/types.go`)
  and calls `testing.Benchmark` 5 times per combo (`runMatrixBench` in `tests/measurev2/main.go`):
  encode, decode, sequential iteration, `ValueAt` and `TimestampAt`.
- Each call is one `b.Loop()` measurement of about 1 s, the default `benchtime`, with the iteration count ramping up inside it.
  The 2 min 57 s per process works out to about 1.18 s per call;
  how that splits between `b.Loop`'s overshoot (Go aims at about 1.2 × its estimate), GC and fixture setup is not recorded.
- Sizes and the scaling series need no timing (`measureEncodedSize`, `runScaling`).

What `generate_report.py` reads:

| Consumer | Data set | Fields |
|---|---|---|
| Encoding matrix; encode, decode, iterate and random-access tables | main | all 30 combos × 5 operations, B/op and allocs/op |
| Profile size tables and grids | profiles | sizes of all 30 combos |
| Speed by profile (`gen_profile_speed`) | profiles | `shared-deltapacked` × 5 codecs: encode ns and allocs, iterate, `ValueAt` |
| Facts digest (`digest_dataset`) | main and profiles | size rankings; fastest lists for 5 operations; the size/iterate Pareto front; the six smallest combos against two references |

The single runs are noisy even within one binary:
the main run and the `mix_monitoring` profile run used the same binary and data,
yet Shared Delta + ALP iterates in 82,331 and 92,451 ns/op (+12.3%), and encode B/op differs by up to 1.4%.
Code placement adds 20–40% on top (`docs/specs/alp-rle-design.md`, "Speed gates").
A longer benchtime removes neither error; repeated runs across code layouts address both.

## Design

The measurement method stays `testing.Benchmark` with `b.Loop()` and today's operation bodies in `tests/measurev2/bench.go`.
Four changes make the report run short and layout-averaged:
time only the cells the report reads, run every data set in one process, shorten `benchtime`,
and repeat the run over four code layouts and four rounds.

### measurev2 flags

| Flag | Values | Default | Notes |
|---|---|---|---|
| `-profile` | one profile name | `mix_monitoring` | today's flag; an error together with `-profiles` |
| `-output` | file | stdout | today's flag; an error together with `-profiles` |
| `-profiles` | `report`, `all`, or a comma list of profile names | unset | measures exactly the listed data sets; duplicates and unknown names are errors |
| `-outdir` | directory | — | required with `-profiles`; must not exist; see the start-up order below |
| `-cells` | `full`, `report`, `wide` | `full` | `report` and `wide` only with `-profiles` |
| `-benchtime` | 10ms–10s | `1s` | applied through `testing.Init()` and `flag.Set("test.benchtime", …)` |
| `-order` | `forward`, `reverse` | `forward` | `reverse` runs the whole cell sequence backwards: data sets, combos, and operations within a combo |
| `-sizes-only` | bool | false | measures sizes and scaling and never calls the timing runner |
| `-run-id`, `-source`, `-layout`, `-round` | strings, ints | generated, empty, -1, -1 | provenance, recorded in the JSON and set by `layouts.sh`; with `-profiles` and no `-run-id`, the tool generates one, so new output always has a `run_id` |
| `-rounds` | 0, 2, 4 | 0 | the run's total round count, recorded in the JSON; `0` means a standalone invocation; `-round` must not exceed it |

Without the new flags, `go run .` and `make bench-measure` behave exactly as today.

Start-up order with `-profiles`: validate the flags, create `-outdir` (failing, before touching anything else, if it exists),
measure, write the JSON files under `-outdir/.partial/`, and rename them into place; after any error nothing is renamed.
The tool writes `OUTDIR/profiles/matrix_<profile>.json` for every listed data set,
and also `OUTDIR/main.json` when the list includes `mix_monitoring`, the main data set;
`main.json` and `profiles/matrix_mix_monitoring.json` come from the same measurements.
`report` is the frozen manifest below; `all` is the generator catalog (18 profiles).
A cell id is `<profile>/<combo label>/<operation>`, for example `mix_fullprec/shared-deltapacked-alprle/encode`.

### Frozen manifests

- **Report profiles** (`-profiles report`), in run order:
  `mix_monitoring`, `mix_sensor`, `mix_integer`, `mix_fullprec`, `decimal_gauge_2dp`, `decimal_gauge_4dp`, `counter`,
  `sparse_constant`, `worst_case`, `cal_2dp_hold30`, `cal_2dp_hold50`, `cal_2dp_hold70`, `cal_2dp_step0.005`,
  `cal_1dp_step0.03`, `cal_1dp_step0.01`, `legacy_random_walk`.
  These are the 16 the skill measures today; `regular_scrape_60s` and `bursty_scrape` stay out.
  The list lives in one Go variable, and SKILL.md and the template point to it.
- **Cells** (data set × combo × operation):

| `-cells` | `mix_monitoring` | each other profile | total for `report` profiles |
|---|---|---|---:|
| `full` | 30 combos × 5 operations | 30 × 5 | 2,400 |
| `report` | 30 × 5 | `shared-deltapacked-{raw,gorilla,chimp,alp,alprle}` and `delta-gorilla` × {encode, iterate, `ValueAt`} = 18 | 420 |
| `wide` | 30 × 5 | `report` plus iterate for the other 24 combos = 42 | 780 |

`wide` restores the cross-timestamp iterate comparison the 2026-10-04 report cited once.
Sizes and scaling are measured for every combo of every data set in every mode.

Preparation stays exactly as in today's helpers, outside the `testing.Benchmark` call:
encode prepares nothing (it builds the blob inside its loop), decode pre-encodes the blob,
and iterate, `ValueAt` and `TimestampAt` pre-encode and pre-decode it.
Each helper drops its fixtures when it returns, so at most one cell's fixtures are live at a time, as today;
a data set's data is released before the next data set is generated.
Every `testing.Benchmark` call runs `runtime.GC()` before the benchmark function, as today.
Before its first timed cell, a `-profiles` invocation that times anything encodes every combo of the main data set once, untimed
(owner, 2026-10-05, after the calibration pass; see Results).
A fresh process spends its first ~100 ms of allocation-heavy work in a runtime GC state in which encodes overlap GC marking less often,
so without the warm-up the first data set a process measured encoded up to 16% faster than the same cells did later.
That GC moves `sync.Pool` contents to the victim cache, and unused entries there expire at the next GC,
but a buffer that `Get` retrieves and `Put` returns is back in the primary cache,
so reused scratch buffers can survive across cells and data sets, as they already do across combos in today's one-profile process.
The report's methodology line says the profiles ran in one process, and gate 3 measures the effect on every cell.
A benchmark result with `N == 0` is an error (today's `toBenchMetrics` turns it into 1).

### Time per run (estimate)

At the historical rates, 420 cells at `-benchtime 50ms` take about 420 × 60–65 ms ≈ 27 s per invocation,
plus sizes and scaling (seconds).
The slowest cell at the historical rate (`mix_fullprec`, Shared DeltaPacked + ALP-RLE encode, 402 µs/op)
would run about 124 operations in 50 ms; the pinned environment will differ.
Gate 6 measures the real stage times.

### Layout averaging: `tests/measurev2/layouts.sh`

```
layouts.sh -o OUTDIR [-cpu 6] [-benchtime 50ms] [-cells report] [-rounds 4|2]
```

`-cpu` must be one non-negative integer naming an online logical CPU; lists and ranges are errors.
Before any timing the wrapper launches a probe under the same `taskset` and checks that its affinity mask holds exactly that CPU,
and every invocation records its own `sched_getaffinity`, which the merge requires to be that single CPU.

1. **Provenance:** `git rev-parse HEAD`, `git status --porcelain`,
   and a SHA-256 over the sorted list and contents of every `*.go`, `*.s`, `go.mod` and `go.sum` file
   that `git ls-files -co --exclude-standard` reports; together they form `source`.
   `tools` records the SHA-256 of `layouts.sh`, `merge_layouts.py`, `generate_report.py`, `check_report_tools.py`,
   the Go file that defines the manifests, and `acceptance_thresholds.json`.
   A random `run_id` names this run.
2. **Stage** exactly the files `git ls-files -co --exclude-standard` reports (minus `tmp/`) into `$TMPDIR/measurev2-layouts/<run_id>/tree`,
   so ignored files never reach the build; recompute `source` over the staged tree and abort if it differs from step 1.
3. **Build** four binaries under the build contract:
   `env GOFLAGS= CGO_ENABLED=0 go build -trimpath -pgo=off -o measurev2_K .`, with no `-race`, `-cover`, `-asan`, `-msan` or `-buildmode`.
   The wrapper reads `go version -m` of each binary and aborts unless it shows `-trimpath=true`, `-pgo` absent or off,
   `CGO_ENABLED=0`, `-buildmode=exe`, and none of `-race`, `-cover`, `-asan`, `-msan`.
   Binary K carries a padding function of K = 0, 1, 2 and 4 steps (K = 0: no file),
   placed in the repository package whose code the linker puts first:
   the wrapper builds K = 0, finds the `github.com/arloliu/mebo/...` symbol with the lowest address in `go tool nm -n`,
   and writes `aa_pad.go` into that symbol's package.
   The generator writes the package variable before the function, as the repository's declaration order requires:
   `var layoutPadSink = layoutPad(uint64(len(os.Args)))`, then `//go:noinline func layoutPad(x uint64) uint64` with K mixing steps.
   Each binary's SHA-256 and `go version -m` output are recorded.
4. **Movement check:** for every repository text symbol (`github.com/arloliu/mebo/...` and package `main`)
   present in all four binaries with the same size, its address must take both possible values mod 64 across the layouts
   (on amd64 Go aligns functions to 32 bytes, so an entry mod 64 is 0 or 32).
   Equal size means equal code, so every instruction in the function moves with its entry, inlined code included.
   The run aborts if any such symbol does not move,
   or if a repository symbol is missing from a binary or changes size: the padding did not do its job.
   The padding itself is the only exception: `layoutPad`, and the padding package's compiler-generated initializer
   (`<package>.init`, which the `layoutPadSink` initialization creates or extends), may appear or change size;
   both are recorded, neither is measured code, and the exception names exactly those symbols.
   Runtime and standard-library code is not checked; varying its placement is not what the padding is for.
5. **Schedule:** `-rounds` is 4 (default) or 2; other values are errors.
   With 4 rounds, round r runs the layouts in row r of the Latin square 0 1 2 4 / 1 4 0 2 / 2 0 4 1 / 4 2 1 0,
   so each layout runs once in each position; rounds 1 and 3 use `-order forward`, rounds 2 and 4 `-order reverse`.
   With 2 rounds, rows 1 and 4 are used (one the reverse of the other), forward then reverse;
   each layout runs once in the first half and once in the second half of the sequence.
   Every invocation runs `taskset -c $CPU env -u GOMEMLIMIT GOMAXPROCS=1 GOGC=100 GODEBUG= ./measurev2_K` with `-profiles report`,
   `-cells`, `-benchtime`, `-order`, `-rounds`, `-run-id`, `-source`, `-layout`, `-round`,
   and its own new `-outdir OUTDIR/raw/r<R>_L<K>`,
   under `/usr/bin/time -v` for peak RSS; the wrapper logs every stage's start and end time.
6. **Merge** with `merge_layouts.py OUTDIR/raw`, which reads exactly the schedule's `r<R>_L<K>` directories
   (anything else under `raw/` is an error), into `OUTDIR/.partial/`,
   then renames it to `OUTDIR/merged/` only after every check passes.
   Any failure in steps 1–6 leaves no `merged/` directory.

Estimated total with the defaults: 16 invocations × about 27 s, builds about 1 min, merge seconds: about 8.5 min.

### Merge contract: `merge_layouts.py`

Every input must agree on the **common metadata**, compared field by field:
`run_id`, `source`, `tools`, `goos`, `goarch`, the CPU model, the Go version, the build settings (`debug.ReadBuildInfo`, without `vcs.*`),
`GOMAXPROCS`, CPU affinity (`sched_getaffinity`), `GOGC`, `GOMEMLIMIT` and `GODEBUG` as the process saw them,
`benchtime`, `rounds`, `cells` and the SHA-256 of the sorted cell ids, the requested profiles in manifest order,
the data configurations, and, as measurement outputs that must agree, every size and scaling value.
**Per layout**, identical across that layout's rounds: the binary SHA-256.
**Per invocation**, kept and not compared: `round`, `order`, start and end time, the existing `timestamp`, peak RSS.

The merge requires every input's `rounds` to equal the schedule's round count,
exactly one input per (layout, round) of that schedule,
every manifest cell in every input, and every timing positive and finite with `N > 0`.
`main.json`'s measurements must equal those of its `mix_monitoring` alias.
Otherwise it fails and writes nothing.

For each cell the merged operation object holds:

```json
"encode": {
  "ns_per_op": 336857.0,
  "bytes_per_op": 185320,
  "allocs_per_op": 108,
  "runs": [{"layout": 0, "round": 1, "ns_per_op": 336100.5, "n": 149, "t_ns": 50080000,
            "bytes_per_op": 185311, "allocs_per_op": 108, "mem_allocs": 16092, "mem_bytes": 27611339}],
  "layout_ns_per_op": {"0": 335000.1, "1": 338200.4, "2": 336100.5, "4": 337900.0},
  "iqr_rel": 0.021
}
```

- `ns_per_op`: median of the n runs; `layout_ns_per_op`: median per layout;
  `iqr_rel`: (75th − 25th percentile) / median, percentiles by linear interpolation (numpy's default), reported for reading only.
- `allocs_per_op` and `bytes_per_op`: medians of the runs, rounded half up;
  when the runs' `allocs_per_op` differ, the cell is listed under `allocation_disagreements`, which the digest shows.
- `n`, `t_ns`, `mem_allocs`, `mem_bytes` are `BenchmarkResult.N`, `T`, `MemAllocs` and `MemBytes`.

### Version-1 schema

Legacy JSON is exactly today's complete schema, with no `format_version`.
Version-1 files add or change only these fields; everything else (`metadata.data_config`, `profile_spec`, the matrix rows' size fields, `scaling`) keeps today's names and types.

| Field | Raw invocation | Merged | Type and meaning |
|---|---|---|---|
| `format_version` | required | required | integer `1` |
| `method` | `"testing.Benchmark"` | `"testing.Benchmark, layout-averaged"` | string |
| `run_id` | required | required | string |
| `common` | required | required | object with every common-metadata field of the merge contract, under those names |
| `invocation` | required | — | object: `layout`, `round`, `order`, `start`, `end`, `timestamp` |
| `invocations` | — | required | array of the inputs' `invocation` objects plus each one's binary SHA-256 and peak RSS |
| `layouts` | — | required | object: layout number → binary SHA-256 |
| `raw_raw_bytes` | required | required | integer, the `raw-raw` combo's encoded size, the denominator of `vs_raw_ratio` |
| `allocation_disagreements` | — | required, possibly empty | array of cell ids |
| operation object (`encode`, `decode`, `iter_seq`, `random_value_at`, `random_timestamp_at`) | present only if timed | present only if timed | see below |

A raw operation object has `ns_per_op` (float, `t_ns / n`), `bytes_per_op` and `allocs_per_op` (integers, as `testing` computes them),
and `n`, `t_ns`, `mem_allocs`, `mem_bytes` (integers from `BenchmarkResult`).
A merged operation object has `ns_per_op`, `bytes_per_op`, `allocs_per_op`, `runs` (the raw objects, each with `layout` and `round` added),
`layout_ns_per_op` (layout number as a string → float) and `iqr_rel` (float), as in the example above.
An untimed operation's key is absent; `null` is invalid; a timed operation with zero allocations has `allocs_per_op: 0`.
Go writes these with pointer fields and `omitempty`; the Python tools read and check them against fixtures of both kinds.


### Comparison rule

For two cells A and B the gap is max/min − 1 of their point estimates (pooled medians), and the pooled winner is the smaller one.
**Layout support** means every layout's median puts the pooled winner strictly ahead; an equal or reversed layout means no support.
Each comparison has exactly one of three outcomes, recorded in the digest with the rule and reason:

| Data | **decided** (faster / slower) | **equivalent** | **inconclusive** |
|---|---|---|---|
| merged (layout-averaged) | gap ≥ 20% with layout support | gap < 20% | gap ≥ 20% without layout support |
| legacy or single-invocation | gap ≥ 20% | gap < 20% | never |

The 20% threshold is the bound SKILL.md already applies to single runs;
layout averaging does not lower it, it adds the requirement that every layout agrees on the winner.
Missing fields are never filled in: a legacy JSON has no layouts, and its rule does not ask for them.

Uses of the outcomes:

- **Rankings** (the profile speed table, the digest's fastest lists) compare each entry with the fastest point estimate only,
  so non-transitive results never chain.
  The table bolds the fastest codec and every codec equivalent to it, and marks an inconclusive one with † and a footnote;
  the lists label each entry "equivalent to the fastest", "slower" or "inconclusive against the fastest".
- **Reference comparisons** in the digest compare each row directly with each of the two references,
  Shared DeltaPacked + Chimp and the library default, Delta + Gorilla,
  next to the ratio: "faster", "slower", "equivalent" or "inconclusive".
- **Pareto front:** the digest prints the point-estimate front as today, labelled "point estimates",
  and next to it the **decided front**: the timed combos that no other combo beats,
  where o beats r if o is no larger and decided faster, or strictly smaller and equivalent in time.
  An inconclusive comparison never makes one combo beat another, so both stay on the front, annotated.
- SKILL.md's judgment rules quote these outcomes;
  a speed claim needs a decided outcome, and "equivalent" or "inconclusive" may not be written as "same speed" or "faster".

### Digest and report with sparse profile timings

`generate_report.py` keeps every size ranking complete over all 30 combos;
computes fastest lists and the Pareto fronts over the timed combos only, labelled "among timed combos";
prints reference comparisons only for rows whose numerator and denominator were both timed and lists the rest as "not timed";
and refuses to render if a manifest cell is missing.
It validates its input set before writing anything:
for publication it accepts only all-legacy input (no `format_version`, every operation present)
or all-merged version-1 input (`method: "testing.Benchmark, layout-averaged"`, one shared `run_id` and `common`,
every layout and round of `common.rounds` present in `invocations`, and `runs` and `layout_ns_per_op` on every timed operation);
raw invocations, `-sizes-only` output, an unknown `format_version`, mixtures and duplicate combo labels within a file are errors.
Profile identity comes from `data_config.profile`;
`main.json` is an alias outside that uniqueness check, and exactly one alias pair exists (with `profiles/matrix_mix_monitoring.json`);
any other two files naming the same profile are an error.
For merged input, the `mix_monitoring` profile must equal `main.json` in every measurement and size;
legacy input measured them in separate processes, so there they may differ and are rendered as two runs, as today.
Legacy complete JSONs render as today, described as single runs, with no layout fields invented.
Before rendering, every matrix row is checked against the identities measurev2 computes today:
`total_points = num_metrics × points_per_metric`, `bytes_per_point = encoded_bytes / total_points`,
`vs_raw_ratio = raw_raw_bytes / encoded_bytes` and `space_savings_pct = (1 − encoded_bytes / raw_raw_bytes) × 100`,
with `raw_raw_bytes` (the `raw-raw` combo's size, as today) recorded explicitly in new JSON and taken from the `raw-raw` row in legacy JSON;
a disagreement beyond a relative 1e-9 rejects the input.
Combo labels, encodings, metric counts and profile names are checked against the manifest too.
The methodology lines of the template and the digest come from the metadata
(method, benchtime, layouts, rounds, CPU, `GOMAXPROCS`, one process for all data sets,
and that the layouts vary repository code placement only, not the runtime's or the standard library's),
and the reproduction block shows the `layouts.sh` command.

## Tests in `make test`

`make test` runs `tests/measurev2` with `-short -race`; nothing there may call `testing.Benchmark`, build, or run a layout.

- The timing calls go through a runner interface.
  `-sizes-only` installs a runner that fails the test if called,
  and with that runner installed,
  a parity test checks that the sizes path and the matrix path produce the same sizes and scaling series on a small data set.
- Benchmark-result conversion: fractional ns/op, measured zero allocations,
  allocation totals whose division by N falls just above and below an integer, and an error for `N == 0`.
- The manifests: 420, 780 and 2,400 cells for `report`, `wide` and `full`; the combos and operations `generate_report.py` reads;
  `mix_monitoring` measured once; a singleton non-main list such as `-profiles legacy_random_walk` measures that data set only and writes no `main.json`;
  duplicate and unknown names fail.
- A recording runner checks `-order forward` and `reverse` (the whole sequence reversed, each cell once),
  that each operation's preparation happens before its runner call, that lookup indices still come from `Seed + 1`,
  that the runner is called outside the timed operation bodies,
  and that the operation bodies keep today's checks (metric counts, complete iteration, one lookup per metric at the fixed indices).
- Data sets: the legacy shared-timestamp generator and non-legacy timestamp copying are unchanged, and scaling handles non-standard point counts.
- Flags: every conflict in the flag table is an error; an existing `-outdir` is an error and nothing is written;
  `-round` above `-rounds` is an error;
  `testing.Init()` runs before `flag.Parse()`, and after parsing `-benchtime 50ms` the `test.benchtime` flag reads `50ms`
  (checked through `flag.Lookup`, without running a benchmark);
  that the value actually changes `testing.Benchmark`'s target is checked by `validate.sh` in the acceptance workflow, not in `make test`.
- JSON: `format_version` and `run_id` are always present with `-profiles`, omitted operations stay omitted,
  measured zeros stay zero, and old complete JSONs decode.

`.agents/skills/update-performance-report/scripts/check_report_tools.py` (fixtures in `scripts/testdata/`, read-only, exit status 1 on any failure)
runs as the first command of SKILL.md Step 2, before `merge_layouts.py` and `generate_report.py`, so a failing tool stops the report;
the Phase 1 acceptance runs it too.
It tests the Python tools with fixtures:

- merge: pooled median versus median of medians, even-count medians, half-up allocation rounding,
  a missing round, a duplicate (layout, round), a `rounds` value that disagrees with the schedule,
  a changed binary hash within one layout, each common-metadata field changed alone, differing invocation timestamps (accepted),
  a missing cell, `N == 0`, non-finite or negative timings, `ns_per_op` that disagrees with `t_ns / n`, allocation disagreements,
  a divergent `mix_monitoring` alias, and a failure during publication that must leave no `merged/`;
- comparison rule: pooled medians whose winner every layout median contradicts (inconclusive), one equal and one reversed layout,
  a gap at exactly 20%, a three-cell chain whose neighbours are equivalent but whose ends differ,
  a noisy fastest cell that makes competitors inconclusive, and legacy pairs above and below 20%;
- Pareto and references: the two references with different timings (both ratios and outcomes appear, labelled),
  equal-size combos within 20%, a front member that changes under a tiny perturbation,
  two codecs equivalent to each other while both are decisively slower than the fastest,
  and a smaller combo whose comparison with a much faster one is inconclusive (both stay on the decided front);
- schema: raw and merged version-1 fixtures round-trip through Go and Python, raw totals are kept and `ns_per_op` is recomputed,
  a `null` operation, a wrong numeric type and a missing `common` field are rejected;
- input set: a complete raw `r1_L0` set is rejected; a merged set missing a layout, a round or `layout_ns_per_op` is rejected;
  an equal `main.json` and `matrix_mix_monitoring.json` pair is accepted, a divergent pair is rejected,
  and two files naming any other same profile are rejected;
- allocations: stable timings with combined and isolated allocations that differ fail gate 3;
  an allocation disagreement is marked in the rendered tables; a zero-allocation cell stays distinct from an untimed one;
- affinity: `-cpu 6,7` and `-cpu 6-7` are rejected, a probe whose mask holds more than one CPU stops the run, and one verified CPU is recorded;
- rendering: a sparse profile whose smallest combo is untimed, an untimed combo that would dominate the Pareto front,
  profiles without decode or `TimestampAt` timings, the 2026-10-04 legacy pair whose main and `mix_monitoring` timings differ (accepted),
  the same divergence under one `run_id` (rejected), mixed legacy and version-1 inputs, a version-1 file without `run_id`,
  an unknown `format_version`, a missing profile file, a duplicated combo, a cell-id hash with missing cells,
  and `-sizes-only` output (rejected);
- size identities: each derived field corrupted while `encoded_bytes` is kept (rejected), a wrong `total_points` (rejected),
  and the shared combos' raw baseline unchanged from today;
- a complete sparse-rendering run: every main table, all 30-combo size grids, empty profile decode and `TimestampAt` rankings,
  untimed reference rows, and both fronts;
- `layouts.sh` stages, through stubs: an ignored compilable Go file in the working tree is not staged,
  a staged-tree hash that differs from `source` aborts, builds that differ in race instrumentation, coverage, build mode or PGO abort,
  the pad lands in the package of the lowest-addressed repository symbol,
  a padding transformation that adds the package initializer (accepted), one that extends an existing initializer (accepted),
  a repository symbol that keeps its residue in all layouts aborts, any other symbol whose size differs or that disappears aborts,
  build, affinity and invocation failures, a non-empty output directory, a unique new `-outdir` on every invocation,
  a pre-existing per-invocation directory (stops before merging), and merge input that is exactly one complete set per (layout, round);
- acceptance checker: thresholds come from `acceptance_thresholds.json`, boundary cases at exactly 5%, 10%, 80% and 95%,
  and an operation whose 50 ms variance is systematically worse fails with an operation-specific message.

## Acceptance gates

Run on the 9950X3D with nothing else heavy running, through `tests/measurev2/validate.sh`,
which prints every gate's numbers and keeps the raw artifacts.
The candidate configuration is `-benchtime 50ms -rounds 4 -cells report`; every gate uses it.
A failing gate is investigated, not retried until it passes.
The numeric thresholds below (5%, 10%, ±3%, 80%, 95%) were **provisional**;
the owner approved them after the calibration pass with `cell_max` at 11% and the B/op tolerance at max(3%, 64 bytes)
(2026-10-05, `tests/measurev2/acceptance_thresholds.json`),
after the recalibration with the allocation tolerance of gates 2 and 3 and the gate-4 exemption below,
and after the validation with the B/op tolerance at max(4%, 64 bytes).
On 2026-10-06 the owner approved gate-3-only limits of 7% and 15% for one validation,
then, once the Gorilla/Chimp spill write barriers were gone and a per-cell warm-up was in place,
removed them and returned the B/op tolerance to max(2%, 64 bytes)
(`docs/specs/encoder-write-barriers-design.md`).
Phase 2 starts with one calibration pass of gates 2, 3 and 4 on the target machine;
it records the per-operation distributions, proposes thresholds with margins over them, and the owner approves them.
They are then frozen in `tests/measurev2/acceptance_thresholds.json`, which the checker reads, and the validation runs against them.
A later change to that file is a reviewed change, not a checker edit.
If the fallback configuration (`-benchtime 100ms -rounds 2`) is adopted instead, gates 2, 3, 4 and 6 run again with it.

1. **Sizes exact.**
   `-sizes-only -profiles report` reproduces every `encoded_bytes`, `bytes_per_point`, `vs_raw_ratio`, `space_savings_pct`,
   `total_points` and scaling value of the 2026-10-04 JSONs.
2. **Short benchtime is accurate.**
   One binary (layout 0), pinned, `GOMAXPROCS=1`, `-profiles report -cells report`, four runs in this order:
   A↑ B↓ B↑ A↓, where A is `-benchtime 1s`, B is `-benchtime 50ms`, ↑ is `-order forward` and ↓ is `-order reverse`.
   Per cell: A = mean of A↑ and A↓, B = mean of B↑ and B↓, r = B / A;
   the controls are cA = A↑ / A↓ and cB = B↑ / B↓.
   A cell is **stable** when |cA − 1| ≤ 5% and |cB − 1| ≤ 5%.
   The gate is inconclusive, and counts as failed, unless the stable cells are at least 80% of all cells and at least 50% of each operation's cells.
   It passes when |r − 1| ≤ 5% for at least 95% of the stable cells, |r − 1| ≤ 10% for every cell,
   and the median r of each operation (encode, decode, iterate, `ValueAt`, `TimestampAt`) is within ±3%.
   From 2026-10-06 to 2026-10-07 the 15 shared-timestamp `TimestampAt` cells were exempt from the 10% per-cell limit,
   as in gate 4, because a binary file could switch between their two speeds during a run
   (`docs/specs/encoder-write-barriers-design.md`);
   `docs/specs/index-entry-by-pointer-design.md` removed the two speeds, and the exemption with them.
   Allocations: allocs/op of B↑ and B↓ each lie within one of A↑'s,
   or, where A↑ and A↓ disagree, within one of the range between them;
   B/op within max(2% of A↑'s, 64 bytes), which also covers a zero baseline (4% from 2026-10-05 to 2026-10-06).
   Reverse order used to start each encode benchmark with a one-off allocation
   (0.35–1.3 MiB; forward order about 1 KiB), so B↓ read up to 3.3% high at 50 ms
   and published B/op of about 60 encode cells read about 1.5% high.
   The cause was cold `sync.Pool` buffers two GCs after the last encode, not the decode cell;
   since 2026-10-06 the runner calls each body twice, untimed, before timing it,
   and the reverse-order excess is at most 0.87% (median 0)
   (`docs/specs/encoder-write-barriers-design.md`, Part 2).
   The tolerance of one allocation (`allocs_abs`) exists because allocs/op truncates a mean:
   in gate 3 of the recalibration, two worst_case encode cells averaged 145.09–145.11 allocations in the combined process
   and 144.95–144.99 in the isolated one, a 0.1% difference that truncation turns into 145 against 144.
   `validate.sh` also confirms that `-benchtime` changed the benchmark's target (B's `t_ns` near 50 ms, A's near 1 s).
3. **One process for all data sets.**
   Layout 0, pinned, `-cells report -benchtime 50ms -order forward`:
   three alternations of one combined invocation (`-profiles report`) and the isolated set (one invocation per report profile, `-profiles <p>`).
   For every report cell, the median of its three combined runs over the median of its three isolated runs, c, must satisfy
   |c − 1| ≤ 5% for at least 95% of cells, |c − 1| ≤ 10% for every cell, and a median c within ±3% for each operation.
   Allocations: for every cell, combined allocs/op lie within one of the isolated runs' range (the gate-2 tolerance),
   and B/op is within max(2%, 64 bytes).
   The gate takes about 3 minutes.
   A lifecycle test (outside `make test`) holds `weak.Pointer`s to one data set's data and fixtures,
   forces a GC after the next data set starts, and requires them all to be collected.
4. **Reproducible.**
   Two complete `layouts.sh` runs with the candidate configuration.
   Over the 420 cells, d = |run 2 / run 1 − 1| of `ns_per_op`:
   d ≤ 5% for at least 95% of cells and ≤ 10% for every cell
   except `TimestampAt` on shared timestamps, which counts toward the 95% but has no per-cell limit;
   allocs/op is equal between the two runs for every cell, and B/op within max(2%, 64 bytes).
   Those 15 cells run at one of two speeds about 29% apart (about 1,630 and 2,105 ns on the main data set),
   chosen per binary file, not by its bytes or path:
   a byte-identical copy of a slow binary runs fast, about one fresh copy in three is slow,
   and a file can change state during a run.
   The suspected cause is where the kernel placed the file's page-cache pages;
   without hardware counters (`perf_event_paranoid` is 4) the mechanism is unconfirmed.
   (Found on 2026-10-06 with counters:
   the placement selected the rate of pipeline resyncs on a struct copy in `TimestampAt`, not a cache or TLB effect;
   `docs/specs/index-entry-by-pointer-design.md` removed the copy and the two speeds.)
   All 15 cells move together in one process, and no comparison was decided in opposite directions in any pass,
   but a run with two or more slow layout files moves their pooled medians by 14.5% or more.
   The report notes that these absolute timings can read up to 29% high.
   A cell in either run's `allocation_disagreements` is listed, and the report marks it wherever its allocations are shown.
   Every pair of cells within each report comparison (same data set and operation) is compared under the rule in both runs;
   no pair may be decided in opposite directions.
5. **Layout movement.**
   `validate.sh` runs step 4 on the real four binaries before any timing gate.
   Step 4 of `layouts.sh` passes in both gate-4 runs, and its symbol table is kept with the run.
6. **Time.** `layouts.sh` with the candidate configuration finishes in at most 15 minutes, builds included;
   stage times and peak RSS are recorded.

The 2026-10-04 JSONs are a sanity comparison only (codec order per profile where the old gap exceeded 40%), not a gate:
they are single runs whose `GOMAXPROCS` and affinity were not recorded.

## Phases

- **Phase 0 — measurev2:** the flags and start-up order, manifests, runner interface, order, provenance fields,
  optional operation fields, `format_version`, the `N == 0` error, and their tests.
- **Phase 1 — wrapper and report:** `layouts.sh` (provenance, staging, build contract, padding placement, movement check, schedule),
  `merge_layouts.py`, `generate_report.py` (input-set validation, size identities, sparse rendering, comparison outcomes),
  `check_report_tools.py`, `validate.sh`, SKILL.md (Step 2 tool check, comparison rule, layouts command),
  the template's methodology text, `tests/measurev2/README.md`, and a `make bench-report` target.
- **Phase 2 — validation:** the calibration pass and the owner's approval of `acceptance_thresholds.json`,
  then gates 1–6, results recorded here;
  `docs/performance.md` is regenerated only if the owner asks.

Each phase gets an external Codex review before it is reported done.
Benchmark runs never overlap other benchmark work on the machine.
The work goes on its own branch from `main`.

## Implementation notes

Decisions the design left open, recorded as implemented.

- **Flags.** measurev2 parses its flags on a private flag set;
  `main` calls `testing.Init()` first and then sets `-test.benchtime` with `flag.Set`, so the `testing` flags never appear in `-help`.
  `-benchtime` takes a duration only (count forms such as `100x` are errors), is normalized (`0.05s` becomes `50ms`),
  and works with or without `-profiles`.
  `-outdir`, `-sizes-only`, `-order`, `-run-id`, `-source`, `-tools`, `-layout`, `-round` and `-rounds` require `-profiles`,
  as do `-cells report` and `-cells wide`.
  `-round` is -1 (standalone) or 1 to `-rounds`; `-layout` is -1 or a layout number.
  A comma list given to `-profiles` is measured in manifest order:
  the report profiles in their run order, then the rest of the catalog.
- **`-tools`.** The merge compares `tools` field by field, so every invocation records it;
  `layouts.sh` passes it with `-tools`, next to `-source`.
- **Common metadata.** `common` also carries `run_id`, and build settings leave out the `vcs` and `vcs.*` keys.
  CPU affinity comes from `sched_getaffinity` on Linux (`golang.org/x/sys/unix`); elsewhere it is recorded as an empty list.
  `invocation.start`, `invocation.timestamp` and `metadata.timestamp` are the time measuring began, after `-outdir` was created.
- **`-sizes-only` output** has `method: "sizes-only"`, so the renderer and the merge reject it by its method.
- **Operation bodies.** Each operation's body is a closure that the runner calls once per `b.Loop` iteration,
  one indirect call per operation on top of today's code.
  It returns an error instead of calling `b.Fatal`; the runner stops the benchmark with `b.FailNow` and returns that error.
  Instead of the old `totalValue == -1` guards, each body stores a checksum in its fixtures
  (the value sum, or for `TimestampAt` the timestamp sum),
  which keeps the work observable and lets the tests confirm a complete iteration and one lookup per metric at the `Seed + 1` indices.
- **Publishing.** `-outdir` is created with `os.Mkdir`, so an existing directory fails before anything is measured;
  files are written under `.partial/` through an `os.Root` and renamed into place, the profiles directory first and `main.json` last.

- **Files.** `layouts.sh` is a thin entry point for `tests/measurev2/layouts.py`, so the checker can drive every step through stubs.
  `merge_layouts.py` and the shared schema module `report_schema.py` (manifest, schema checks, comparison rule)
  sit next to `generate_report.py` in the skill's `scripts/`;
  the gate computations live in `tests/measurev2/acceptance.py`, which `validate.sh` calls.
  `tools` hashes `layouts.sh`, `layouts.py`, `manifest.go`, `acceptance_thresholds.json`,
  `report_schema.py`, `merge_layouts.py`, `generate_report.py` and `check_report_tools.py`.
- **Staging** copies regular files only: sockets, device nodes and symbolic links that `git ls-files -co` reports are skipped
  and listed in `provenance.json` (inside the development sandbox, untracked dotfiles at the root are device nodes).
  `source` is `head=<commit> status=<SHA-256 prefix of git status --porcelain> tree=<SHA-256 of the Go sources>`.
- **Run directory.** Binaries go to `OUTDIR/bin/`, each layout's symbol table, `go version -m` and the movement summary to `OUTDIR/layouts/`,
  `/usr/bin/time -v` logs to `OUTDIR/logs/`, and stage times, skipped files and per-invocation peak RSS to `OUTDIR/provenance.json`.
  After each invocation the wrapper adds `wrapper.json` (binary SHA-256, peak RSS, the verified CPU) to its `raw/r<R>_L<K>/` directory,
  which the merge reads: every input's `cpu_affinity` must be exactly that one CPU.
  `--build-only` stops after the movement check, for `validate.sh`.
  Gate 6 uses the wrapper's wall time, which `validate.sh` measures around `layouts.sh` from start to exit;
  publication is also a timed stage in `provenance.json`. Any failure, even after `merged/` was renamed into place, removes `merged/` again
  (the output directory started empty, so a `merged/` there is the failing run's), and `provenance.json` records `published: false`.
- **Merge.** Directory names must be canonical (`r<R>_L<K>` without leading zeros), so two directories never claim one invocation.
  The schedule's round count comes from the directory names and must match every input's `rounds`;
  each input's `order` must match the schedule; merged `metadata.timestamp` is the earliest invocation start,
  and `allocation_disagreements` lists the cells of its own file.
- **Report.** The template gained `{{TIMING_METHOD}}`, `{{RUNNING_BENCHMARKS}}` and `{{PROFILE_SPEED_NOTE}}`,
  rendered from the metadata, so a legacy set keeps its single-run text and a merged set describes its layouts.
  Legacy input is also labelled with comparison outcomes under the single-run rule,
  so the profile speed table bolds every codec within 20% of the fastest.
  For merged input the platform line names the CPU model instead of `num_cpu`, which a pinned process reports as 1.
  `‡` marks an allocation disagreement and `—` an untimed cell; an untimed cell never gets a table row.
- **Tool check.** SKILL.md runs `check_report_tools.py` as the first command of Step 1, before `layouts.sh`,
  so a failing tool stops the report before any benchmark, as well as before the merge and the renderer.
  It checks the Python manifest against `manifest.go`, validates the raw file Go writes (`TestRawFixtureGolden`),
  and writes the merged fixture Go decodes (`TestMergedFixtureDecodes`), so both schema directions are covered.
- **Merged validation.** The renderer recomputes every merged summary from its runs:
  the pooled and per-layout medians, the half-up allocation medians, `iqr_rel`,
  and `allocation_disagreements`, which must list exactly the cells whose runs' allocs/op differ.
- **Acceptance inputs.** `acceptance.py` admits only the run each gate specifies:
  every report profile's file, each cell once, the manifest's 420 cells, and the gate's `-benchtime`, `-order` and `rounds`;
  all files of one directory must share one `run_id`, `common` and `invocation`,
  pinned to the gate's CPU with `GOMAXPROCS=1`, `GOGC=100` and no `GOMEMLIMIT` or `GODEBUG`;
  an isolated set needs one invocation per report profile.
  Gate 4 applies the 95%/5% and 10% limits (with the exemption above, while it lasted),
  the allocation rules and the opposite-direction check,
  but no per-operation median limit, which belongs to gates 2 and 3.
  `VALIDATE_BENCHTIME_A` and `VALIDATE_BENCHTIME_B` shorten a smoke run of `validate.sh`; only the defaults count.
- **Calibration** (`validate.sh --calibrate`) prints each gate statistic's distribution per operation
  and a threshold proposal, each value with the rule that produced it:
  `stable_control` from the 80th percentile of the order controls × 1.25,
  `cell_within` from the largest 95th percentile of |x − 1| over gates 2–4 × 1.25,
  `cell_max` from the largest |x − 1| × 1.25, `op_median_within` from the largest per-operation median deviation of gates 2 and 3 × 1.5,
  and the `t_ns` bounds from the observed range with 5% and 25% margins; the share limits, byte tolerances and `max_minutes` stay.
- **Acceptance thresholds** also hold `benchtime_t_min` and `benchtime_t_max`
  (every cell's `t_ns` must lie within those multiples of its `-benchtime`, which is how `validate.sh` confirms the flag took effect)
  and `max_minutes` for gate 6.

## Decided

- `testing.Benchmark` stays the only timing method.
- Published numbers are pinned to one core with `GOMAXPROCS=1`;
  GC work then shares the measured core, so allocation-heavy operations read slower than in the 2026-10-04 report,
  and the first report on the new method says so.
- The cross-timestamp iterate check leaves the default run; `-cells wide` keeps it.
- No reduced comparison threshold: layout-averaged data uses 20% plus layout support (owner, 2026-10-05).
- `-cells full` stays; whether to remove it is decided after one release.

## Risks

- A 50 ms benchtime may bias some operations; gate 2 measures it per operation against 1 s on the same binary.
- 9950X3D has one CCD with 3D V-cache and one without; every invocation pins the same core, and timings never run in parallel.
- Four layouts sample code placement; they do not remove it.
  The layout-support condition keeps a placement-dependent winner from being reported as decided.
- Runtime and standard-library placement is not varied, as today.

## Results

Measured on the 9950X3D, pinned to CPU 6, with the candidate configuration (`-benchtime 50ms -rounds 4 -cells report`).
Raw artifacts: `tmp/measurev2-calibration-2026-10-05/`, `tmp/measurev2-recalibration-2026-10-05/`
and `tmp/measurev2-validation-2026-10-05/` (each with its `validate.log`; the first two with `findings.md`,
the last with `evaluate-final.txt`, every gate evaluated against the final thresholds).

| Gate | Calibration (no warm-up) | Recalibration (warm-up) | Validation |
|---|---|---|---|
| 1 sizes exact | — | — | 2,550 size fields and 17 scaling sets identical |
| 2 stable / within 5% / worst | 96.4% / 99.8% / 8.41% | 85.5% / 98.9% / 39.89% | 95.0% / 100.0% / 5.23% |
| 2 worst B/op excess (B↓) | 2.87% | 2.87% | 3.02% |
| 3 within 5% / worst | 93.1% / 15.51% | 95.0% / 10.60% | 95.5% / 10.72% |
| 3 allocations | 145 against 144 (2 cells) | 145 against 144 (2 cells) | within one |
| 4 within 5% / worst / exempt worst | 100% / 1.73% / — | 96.4% / 3.38% / 15.41% | 96.4% / 4.50% / 14.59% |
| 4 pairs decided / opposite | 1,865 / 0 | 1,866 / 0 | 1,869 / 0 |
| 5 repository symbols moved | 657 | 657 | 657 |
| 6 wall time | 6.87 min | 6.87 min | 6.87 min, peak RSS 43 MiB |

Every gate passes on the validation run under the final thresholds.
A report run takes 6.9 minutes instead of 40–50, and its timings are medians over four code layouts and 16 invocations
instead of single runs.
In the validation, gate 2's per-operation median ratios were within 1.1% (encode 1.0103)
and gate 3's within 1.4% (encode 1.0136, the others within 0.4%).
The recalibration's gate-2 numbers came from a busy machine
(single-run spikes of 30–80%, load average about 2.6 with the benchmark contributing 1);
the validation ran with the machine otherwise idle.

What the passes found, and what changed because of it:

- **Start-up transient.**
  The calibration's gate 3 failed because the first data set a fresh process measured encoded up to 16% faster.
  For its first ~100–150 ms of allocation-heavy work,
  a fresh process overlaps encodes with GC marking less often than in steady state.
  The Gorilla and Delta encoders store their buffer's slice header into a heap object on every append
  (`internal/encoding/value/gorilla/gorilla.go:315`), and each such store takes a write barrier while a GC is marking;
  merged CPU profiles put runtime and GC time at 14.4% of encode time in a fresh process and 21.8% in steady state.
  The one-pass warm-up (see "Frozen manifests") moved gate 3 inside the approved limits;
  shared-timestamp Gorilla and Chimp encodes keep a residual of about 4% in later data sets.
  Keeping those buffers in locals inside the hot loops is a separate library follow-up, not part of this work.
- **Shared-timestamp `TimestampAt` is bimodal per binary file** (gate 4),
  and gate 4 exempts those 15 cells from `cell_max`
  (until 2026-10-07; see `docs/specs/index-entry-by-pointer-design.md`).
- **allocs/op truncates a mean** (gate 3), and gates 2 and 3 allow a difference of one.
- **Reverse order adds a one-off allocation to encode** (gate 2): the B/op tolerance became max(4%, 64 bytes),
  and the report said that B/op of some encode cells reads up to about 1.5% high.
  A per-cell warm-up removed the allocation on 2026-10-06;
  the tolerance is back to max(2%, 64 bytes), and the report no longer says so
  (`docs/specs/encoder-write-barriers-design.md`).
