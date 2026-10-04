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
- Run nothing else CPU-heavy while benchmarks run (other benchmarks, `make test`, an outside review);
  timings are single-run and shift with load.

## Step 1: Run the benchmarks

Run the main data set and every profile, one after another, into a directory outside the repository:

```bash
OUT=$TMPDIR/perf && mkdir -p $OUT/profiles && cd tests/measurev2 && go build -o $OUT/measurev2 . && \
$OUT/measurev2 -pretty -verbose -output $OUT/main.json && \
for p in decimal_gauge_2dp decimal_gauge_4dp counter sparse_constant worst_case \
         cal_2dp_hold30 cal_2dp_hold50 cal_2dp_hold70 cal_2dp_step0.005 cal_1dp_step0.03 cal_1dp_step0.01 \
         mix_monitoring mix_sensor mix_integer mix_fullprec legacy_random_walk; do
  $OUT/measurev2 -profile "$p" -pretty -output "$OUT/profiles/matrix_$p.json" || echo "FAIL $p"
done
```

Each run takes about 3 minutes (30 combos × encode, decode, iterate, `ValueAt`, `TimestampAt`, plus scaling).
The main data set is the default profile, `mix_monitoring`, at 100 metrics × 150 points;
`go run . -help` lists the profiles, and `tests/measurev2/README.md` describes them.

## Step 2: Render the tables

```bash
python3 .agents/skills/update-performance-report/scripts/generate_report.py \
  --main $OUT/main.json --profiles $OUT/profiles \
  --template .agents/skills/update-performance-report/PERFORMANCE_TEMPLATE.md \
  --out docs/performance.md --digest $OUT/digest.md
```

The script fills every table placeholder, leaves the `{{LLM:...}}` placeholders, and lists them.
The digest ranks every combo per data set (smallest, fastest encode/decode/iterate/`ValueAt`/`TimestampAt`,
the size/iterate Pareto front, and the change against Shared DeltaPacked + Chimp and against the default encoder).
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
- **Separate deterministic sizes from single-run timings.**
  Sizes are exact; timings move by 20–40% with code placement, so treat gaps under about 20% as ties.
  Where two codecs encode identical columns (ALP and ALP-RLE on data without repeats), any timing gap is noise.
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
  "metadata": { "go_version", "os", "arch", "num_cpu", "timestamp", "data_config", "profile_spec" },
  "matrix":   [ per-combo results: label, bytes_per_point, encode, decode, iter_seq, random_value_at, random_timestamp_at ],
  "scaling":  [ per-combo bytes/point at 1, 2, 5, 10, 20, 50, 100, 150 points per metric ]
}
```

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
  The shared columns are decoded once into `sharedTsCache` when the blob is opened (`blob/numeric_decoder.go`),
  and `TimestampAt` reads the cache; the script's `ts_complexity()` applies this to every `shared-*` label.

When a codec is added, verify its `At()` complexity in the decoder source before adding it to `AT_COMPLEXITY`.

### Domain knowledge

- **DeltaPacked vs Delta**: DeltaPacked's Group Varint layout is meant for faster decode and iteration, not size; the size difference is small.
  Check the iterate columns before repeating the speed claim:
  the 2026-10-04 run measured DeltaPacked iterating 1.1–1.8× slower than Delta with Gorilla and Chimp,
  in one binary, so code placement may explain it.
- **Chimp vs Gorilla**: both XOR-based; Chimp is usually slightly smaller.
- **ALP** (`format.TypeALP`): wins on decimal-quantized values and integers.
  On full-precision values it is about Chimp's size,
  and on full-precision values with frequent repeats it is much larger, because XOR codecs store a repeat in one bit.
  Its per-column (e,f) search makes encoding several times slower than Chimp.
- **ALP-RLE** (`format.TypeALPRLE`): ALP with a run-length front end.
  Each column keeps the runs layout only when it is smaller than the plain ALP column,
  so it is never larger than ALP per uncompressed column and pays off where many consecutive points repeat;
  with value compression the compressed payload is not guaranteed to shrink.
  Encoding costs about 1% over ALP on data without repeats and about 1.5× ALP when half the points repeat.
- **Shared timestamps**: `WithSharedTimestamps()` stores identical timestamp columns once; savings grow with the number of metrics.
  Opening a shared-TS blob also decodes the shared columns into `sharedTsCache`,
  so a smaller blob does not mean a faster open; don't claim shared-TS decodes faster unless the data shows it.
- **Scaling**: below about 10 points per metric, fixed per-metric overhead dominates.
- **Iteration**: compressed data can iterate faster than raw, because less memory is read.
