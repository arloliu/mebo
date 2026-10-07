---
name: update-performance-report
description: Run the tests/measurev2 benchmarks and regenerate docs/performance.md, with script-rendered tables and agent-written recommendations
---

# Update Performance Report

`docs/performance.md` has two kinds of content:

- **Tables**, rendered by `scripts/generate_report.py` from `tests/measurev2` JSON.
  Never edit them by hand; rerun the script.
- **Judgment sections** (`{{LLM:NAME}}` placeholders in `PERFORMANCE_TEMPLATE.md`),
  written by the agent from those tables and the facts digest, following the rules below.
  A script cannot weigh data shape, encode cost and reader compatibility against each other;
  an earlier version tried, picked "best compression" as the smallest combo of one full-precision random walk,
  and recommended Chimp while every realistic data shape favored the ALP family.

## Prerequisites

- Run from the mebo repository root.
- Run nothing else CPU-heavy while benchmarks run (other benchmarks, `make test`, an outside review):
  every invocation is pinned to one core, and other load on the machine still moves its timings.

## Step 1: Check the tools, then run the benchmarks

```bash
python3 .agents/skills/update-performance-report/scripts/check_report_tools.py && \
OUT=$TMPDIR/perf && tests/measurev2/layouts.sh -o $OUT
```

`make bench-report` runs the same two commands.
The tool check comes first, so a broken merge or renderer stops the report before any benchmark runs.
`layouts.sh` builds `tests/measurev2` in four code layouts and runs each layout four times,
pinned to CPU 6 with `GOMAXPROCS=1`, with every report data set in one process per run;
then it merges the 16 runs into `$OUT/merged/` (about 10 minutes in all).
The data sets are the report manifest, `reportProfiles` in `tests/measurev2/manifest.go`:
the main data set, `mix_monitoring` at 100 metrics × 150 points, and 15 data-shape profiles.
The main data set times all 30 combos × 5 operations;
the other profiles time encode, iterate and `ValueAt` for the five Shared DeltaPacked combos and Delta + Gorilla.
Sizes and scaling are measured for every combo of every data set.
`tests/measurev2/README.md` describes the flags, the output and the validation script.

## Step 2: Render the tables

```bash
python3 .agents/skills/update-performance-report/scripts/generate_report.py \
  --main $OUT/merged/main.json --profiles $OUT/merged/profiles \
  --template .agents/skills/update-performance-report/PERFORMANCE_TEMPLATE.md \
  --out docs/performance.md --digest $OUT/digest.md
```

The script validates the input set before writing anything:
it accepts only a merged `layouts.sh` run or a complete legacy set,
and rejects raw invocations, `-sizes-only` output, mixtures, and any file whose sizes disagree with their identities.
It fills every table placeholder, leaves the `{{LLM:...}}` placeholders, and lists them.
The digest ranks every combo per data set (smallest, fastest encode/decode/iterate/`ValueAt`/`TimestampAt`,
the size/iterate Pareto fronts, and the change against Shared DeltaPacked + Chimp and against the default encoder),
each speed comparison labelled with its outcome under the comparison rule below.
Keep the digest out of the repository.

## Step 3: Write the judgment sections

Replace each `{{LLM:...}}` placeholder in `docs/performance.md`.
Read the rendered tables and the digest first; the previous `docs/performance.md` (`git show HEAD:docs/performance.md`)
shows the expected depth, but reuse none of its numbers.

| Placeholder | What it covers |
|---|---|
| `QUICK_REFERENCE` | A short table: the recommended configuration for the production-like case, the best for data without repeats or decimals, the fastest to encode, the fastest for random access, and the library default, each with its key number and data set |
| `KEY_OBSERVATIONS` | 4–6 bullets on the main matrix: which value codec wins and by how much against Chimp; what shared timestamps save; what timestamp encodings cost; encode cost of the ALP family; anything surprising |
| `SCALING_INSIGHTS` | Where per-metric overhead stops dominating, for the recommended combo and for Chimp, from the scaling tables |
| `ALPRLE_LAYOUT_AVERAGED` | Carry the layout-averaged speed table, its provenance box and its bullets forward from the previous report unchanged, unless the layout-averaged ALP-RLE harness was rerun (`docs/specs/alp-rle-design.md`, "Speed gates"); sizes come from `PROFILE_PRODUCTION_SIZES`, so add no size table here |
| `PROFILE_TAKEAWAYS` | Which codec wins on which shapes, how stable that is across the four mixes, and where each codec is a trap |
| `DECISION_TREE` | A text tree (`├─`, `└─`, `│`) that branches on data shape first, then on shared timestamps, encode budget and reader versions |
| `CONFIGURATION_SELECTION` | A use-case table (configuration, key numbers, rationale with the cost) |
| `PPM_GUIDELINES` | Zones of points per metric (poor, moderate, good, optimal) from the recommended combo's scaling series |

Rules:

- **Recommend by data shape.**
  The main data set is one calibrated mix; check every recommendation against the profile tables
  and say when it holds for some shapes only.
- **Name the data set and configuration of every number** (for example "`mix_monitoring`, Shared DeltaPacked").
- **Never let `worst_case` or `legacy_random_walk` drive a headline.**
  They are references for full-precision data without repeats.
- **State the cost of each recommendation:** encode time against Chimp;
  ALP-RLE blobs need readers that know the encoding, and shared timestamps need V2 readers;
  plain ALP is much larger than Chimp on full-precision data with repeats.
- **Recommend only configurations someone would run.**
  Raw timestamps with a compressed value codec, or Raw values chosen only for speed, are not "best balance" answers.
- **Separate deterministic sizes from timings, and quote timings by their comparison outcome.**
  Sizes are exact.
  A speed comparison between two cells is one of three outcomes (`compare` in `scripts/report_schema.py`):
  **decided** when the gap is at least 20% and, for layout-averaged data, every layout's median puts the same combo ahead;
  **equivalent** when the gap is under 20%;
  **inconclusive** when the gap is at least 20% but not every layout agrees.
  A speed claim ("faster", "slower", "N× the encode time") needs a decided outcome;
  never write an equivalent or inconclusive comparison as "same speed" or "faster".
  The digest labels every ranking and reference comparison with its outcome.
  Where two codecs encode identical columns (ALP and ALP-RLE on data without repeats), any timing gap is noise.
- **Say how the timings were taken.**
  The numbers are pinned to one core with `GOMAXPROCS=1`, so the garbage collector shares the measured core
  and allocation-heavy operations read slower than in reports measured before 2026-10;
  the first report on this method says so, and none compares its timings with an older report's.
- **Do not change** the template's static text to fit a conclusion; change the template itself if it is wrong.

## Step 4: Verify

1. `python3 .agents/skills/update-performance-report/scripts/generate_report.py --check docs/performance.md` reports no placeholders.
2. Every number in the judgment sections appears in a rendered table or the digest, or is computed from them; recompute ratios.
3. `semlf --base HEAD` reports no fused or wrapped lines in the prose you wrote.
4. Update `README.md` (Performance table, Configuration Examples) and `docs/best_practices.md`
   (scaling table, encoding guidance) when the numbers or the recommendations they repeat changed.
5. Send the result to an outside reviewer (the `post-impl-review` skill) with the JSON directory,
   and fold its findings in before reporting done.

## Reference

### JSON structure

```
{
  "format_version": 1, "method": "testing.Benchmark, layout-averaged", "run_id",
  "common":      { provenance, platform, runtime environment, benchtime, rounds, cells, profiles, data configs },
  "invocations": [ one per (round, layout): layout, round, order, start, end, binary_sha256, peak_rss_kb ],
  "layouts":     { layout number: binary SHA-256 },
  "allocation_disagreements": [ cell ids whose allocs/op differed between runs ],
  "raw_raw_bytes", "metadata": { "go_version", "os", "arch", "num_cpu", "timestamp", "data_config", "profile_spec" },
  "matrix":   [ per-combo results: label, sizes, and each timed operation (encode, decode, iter_seq, random_value_at,
                random_timestamp_at) as { ns_per_op, bytes_per_op, allocs_per_op, runs, layout_ns_per_op, iqr_rel } ],
  "scaling":  [ per-combo bytes/point at 1, 2, 5, 10, 20, 50, 100, 150 points per metric ]
}
```

An untimed operation's key is absent.
Legacy files (before 2026-10-05) have only `metadata`, `matrix` and `scaling`,
with every operation as `{ ns_per_op, bytes_per_op, allocs_per_op }`;
the renderer still accepts a complete legacy set and describes it as single runs.

Labels are `<ts>-<val>` with `shared-` prepended for shared timestamps (for example `shared-deltapacked-alprle`).
`profile_spec` is the full profile definition, mixed parts included; the script describes the data from it.

### `At()` complexity

`AT_COMPLEXITY` in the script holds each codec's random-access complexity,
verified against the decoder implementations, not inferred from names or numbers:

- Raw (timestamp or value): O(1), a direct offset into a fixed-width array.
- ALP (value): O(1) windowed bit read + O(log k) binary search over that column's exception sidecar
  (k = exceptions in that column, not n); not a plain O(1).
- ALP-RLE (value): a column that uses the runs layout first ranks its run-start bitmap with a word-wise popcount over bits 0..index
  (`alpRunsRank` in `internal/encoding/value/alp/alp_runs.go`, no rank directory),
  so O(index/64), at most 3 popcounts at 150 points,
  then does the nested ALP lookup; a column without enough repeats stays a plain ALP column.
- Gorilla, Chimp (value): O(index), a sequential XOR-chain decode from the start of the column,
  whatever timestamp encoding the combo pairs it with.
- Delta, DeltaPacked (timestamp): O(index), since each value depends on the accumulated sum before it.
- Shared timestamps (any timestamp encoding): O(1).
  The shared columns are decoded once into the blob's shared-timestamp groups when the blob is opened
  (`buildSharedTimestamps` in `blob/numeric_decoder.go`), and `TimestampAt` reads them;
  the script's `ts_complexity()` applies this to every `shared-*` label.
  Until 2026-10-07 their measured times were bimodal per binary file (about 1,630 or 2,105 ns on the main data set);
  `docs/specs/index-entry-by-pointer-design.md` removed the cause (about 1,065 ns),
  and its follow-up replaced the map that found the group with an inlined lookup (about 950 ns),
  so a report from before those changes is not comparable on these cells.

When a codec is added, verify its `At()` complexity in the decoder source before adding it to `AT_COMPLEXITY`.

### Domain knowledge

- **DeltaPacked vs Delta**: DeltaPacked's Group Varint layout is meant for faster decode and iteration, not size; the size difference is small.
  Check the iterate columns before repeating the speed claim:
  the 2026-10-07 layout-averaged run measured DeltaPacked iterating 1.28–1.35× slower than Delta with Gorilla and Chimp,
  decided in every layout, and equivalent to it with ALP and ALP-RLE.
- **Chimp vs Gorilla**: both XOR-based; Chimp is usually slightly smaller.
- **ALP** (`format.TypeALP`): wins on decimal-quantized values and integers.
  On full-precision values it is about Chimp's size,
  and on full-precision values with frequent repeats it is much larger, because XOR codecs store a repeat in one bit.
  Its per-column (e,f) search is most of its encode cost.
  On amd64 with AVX-512DQ and POPCNT an AVX-512 kernel runs that search,
  and on the four mixes a blob encodes in about 1.4–1.5× Chimp's time (ALP-RLE 1.6–2.0×),
  up to 1.9× (ALP-RLE 2.6×) on `sparse_constant` (layout-averaged report run, 2026-10-06).
  A controlled before/after run on 2026-10-06 measured Gorilla and Chimp encode about 14% faster (median of 69 cells)
  once their spills stopped taking GC write barriers (`docs/specs/encoder-write-barriers-design.md`),
  with the ALP family's encode unchanged;
  the earlier figures of 1.1–1.2× (ALP-RLE 1.3–1.6×) came from `BenchmarkBlobEncodeMixes` on 2026-10-04,
  a different harness, so don't read the whole difference as that one change.
  Elsewhere the scalar search runs; on the same machine with `GODEBUG=cpu.avx512dq=off`,
  ALP took about 3.5–4.3× Chimp's time on the four mixes (ALP-RLE 3.7–6.6×) before that Chimp speed-up,
  and other targets will differ.
  Say which machine the report ran on when quoting encode ratios.
- **ALP-RLE** (`format.TypeALPRLE`): ALP with a run-length front end.
  Each column keeps the runs layout only when it is smaller than the plain ALP column,
  so it is never larger than ALP per uncompressed column and pays off where many consecutive points repeat;
  with value compression the compressed payload is not guaranteed to shrink.
  Encoding costs about 4% over ALP on data without repeats and about 1.8× ALP when half the points repeat
  (layout-averaged with the AVX-512 search; about 1% and 1.5× with the scalar search).
- **Shared timestamps**: `WithSharedTimestamps()` stores identical timestamp columns once; savings grow with the number of metrics.
  Opening a shared-TS blob also decodes the shared columns into the blob's shared-timestamp groups,
  so a smaller blob does not mean a faster open; don't claim shared-TS decodes faster unless the data shows it.
- **Scaling**: below about 10 points per metric, fixed per-metric overhead dominates.
- **Iteration**: compressed data can iterate faster than raw, because less memory is read.
