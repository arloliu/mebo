---
name: update-performance-report
description: Run encoding benchmarks and update docs/performance.md with fresh data
---

# Update Performance Report

This skill runs the encoding benchmark matrix tool and generates `docs/performance.md` from a template + benchmark data.

## Prerequisites

- The workspace must be the `mebo` project root
- `tests/measurev2/` must exist with the benchmark tool

## Workflow

### Step 1: Run the benchmark tool

```bash
cd tests/measurev2 && go run . -pretty -verbose -output /tmp/mebo_bench_results.json 2>&1
```

Wait for completion. With default settings (200 metrics × 200 points, 24 combos: 12 standard + 12 shared-timestamp — 3 timestamp encodings × 4 value encodings: Raw, Gorilla, Chimp, ALP), this takes ~5-10 minutes (5 benchmarks per combo: encode, decode, iterate, random ValueAt, random TimestampAt).

### Step 2: Generate the report

Run the generation script, which reads the benchmark JSON and template, fills all `{{PLACEHOLDER}}` values, and writes the output:

```bash
python3 .agents/skills/update-performance-report/scripts/generate_report.py \
  /tmp/mebo_bench_results.json \
  .agents/skills/update-performance-report/PERFORMANCE_TEMPLATE.md \
  docs/performance.md
```

The script exits with code 0 on success and prints a summary (combo count, best/worst BPP).
If any placeholders remain unfilled, it exits with code 1.

### Reference: JSON structure and placeholders

The benchmark JSON has this structure:
```
{
  "metadata": { "go_version", "os", "arch", "num_cpu", "timestamp", "data_config" },
  "matrix": [ { per-combo benchmark results (24 entries: 12 standard + 12 shared-TS) } ],
  "scaling": [ { per-combo bytes/point at different point counts (24 entries) } ]
}
```

Shared-timestamp combos have labels prefixed with `shared-` (e.g., `shared-delta-chimp`).
They use `WithSharedTimestamps()` which deduplicates identical timestamp sequences across metrics,
storing the timestamp column once instead of per-metric. The benchmark generates separate test data
where all metrics share the same jittered timestamp sequence (typical monitoring scenario).

The following sections document what each placeholder produces (for reference when modifying the script or template).

#### `{{BENCHMARK_METADATA}}`

A prominent benchmark criteria summary box at the top of the report:

```markdown
| | |
|---|---|
| **Benchmark Date** | {timestamp} |
| **Platform** | {os}/{arch} ({num_cpu} CPUs), Go {go_version} |
| **Data** | {num_metrics} metrics × {points_per_metric} points = {total_points} total data points |
| **Value Jitter** | ±{value_jitter_pct}% per point (random walk) |
| **Timestamp Jitter** | ±{ts_jitter_pct}% of 1s interval |
| **Compression Codecs** | None (testing encoding algorithms only) |
```

#### `{{BENCHMARK_METADATA_DETAIL}}`

Expanded test environment details for the Methodology section:

```markdown
| Parameter | Value | Description |
|-----------|-------|-------------|
| **Go Version** | {go_version} | Compiler and runtime |
| **OS / Arch** | {os}/{arch} | Operating system and CPU architecture |
| **CPU Cores** | {num_cpu} | Available logical CPUs |
| **Metrics** | {num_metrics} | Number of independent sensor metrics |
| **Points/Metric** | {points_per_metric} | Data points per metric (for matrix benchmarks) |
| **Value Jitter** | ±{value_jitter_pct}% | Per-point random walk delta (models semiconductor sensor noise) |
| **Timestamp Jitter** | ±{ts_jitter_pct}% | Variation in 1-second sampling interval (models industrial protocol jitter) |
| **Sampling Interval** | 1 second | Base interval between data points |
| **Seed** | 42 | Fixed for reproducibility |
| **Compression** | None | No codec layer — testing encoding algorithms only |
```

#### `{{QUICK_REFERENCE}}`

Pick the top 3 results from `matrix` sorted by `bytes_per_point`, plus the baseline:

```markdown
| Metric | Value | Configuration |
|--------|-------|---------------|
| **Best Compression** | {best.bytes_per_point} bytes/point ({best.space_savings_pct}% savings) | {best.label} |
| **Best Balance** | {second.bytes_per_point} bytes/point | {second.label} |
| **Fastest Encode** | {fastest_enc.encode.ns_per_op} ns/op | {fastest_enc.label} |
| **Baseline** | {raw_raw.bytes_per_point} bytes/point | raw-raw |
```

#### `{{ENCODING_MATRIX}}`

Create a table from `matrix`, sorted by `bytes_per_point` ascending:

```markdown
| Configuration | Bytes/Point | Space Savings | vs Raw | Encode (ns/op) | Decode (ns/op) | Iterate (ns/op) |
|---------------|-------------|---------------|--------|----------------|----------------|-----------------|
```

Format rules:
- Sort by `bytes_per_point` ascending (most efficient first)
- `vs Raw` = `vs_raw_ratio` formatted as `%.2f×`
- `Space Savings` = `space_savings_pct` formatted as `%.1f%%`
- Round ns/op to nearest integer
- Label format: capitalize encoding names, e.g. "DeltaPacked + Chimp"

#### `{{ENCODING_OBSERVATIONS}}`

Generate 4-6 bullet points based on the data:
- Which combo achieves the best compression? By how much vs raw-raw?
- How much additional savings does shared timestamps provide over the best non-shared combo?
- How does Chimp compare to Gorilla? (they should be close)
- How does ALP compare to Chimp/Gorilla on this dataset? Note that this benchmark's data is a
  full-precision random walk, not decimal-quantized — ALP's main scheme only wins big on
  decimal-quantized data (see `docs/performance.md`'s "Codec Selection by Data Shape" section for
  the profile-based comparison where ALP does shine). Don't overstate ALP's ranking here if it
  isn't actually winning on this dataset.
- How does DeltaPacked compare to Delta? (small improvement in size, advantage is decode speed)
- Is encode speed vs compression a meaningful tradeoff? (ALP's encode is markedly slower than
  Gorilla/Chimp — its (e,f) search cost — worth calling out if ALP appears in the top compression
  ranks)
- Note the decode speed difference: shared-TS combos decode faster due to smaller blob size

#### `{{ENCODE_PERFORMANCE}}`

```markdown
| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
```

Sort by `encode.ns_per_op` ascending (fastest first).

#### `{{DECODE_PERFORMANCE}}`

Same format as encode, using `decode.*` fields. Sort by `decode.ns_per_op` ascending.

#### `{{ITERATION_PERFORMANCE}}`

Same format, using `iter_seq.*` fields. Sort by `iter_seq.ns_per_op` ascending.

#### `{{RANDOM_ACCESS_PERFORMANCE}}`

```markdown
| Configuration | ValueAt (ns/op) | Value complexity | TimestampAt (ns/op) | Timestamp complexity |
|---|---:|---|---:|---|
```

Sort by `random_value_at.ns_per_op + random_timestamp_at.ns_per_op` ascending. The two
"complexity" columns come from `AT_COMPLEXITY` (a fixed dict keyed by the value/timestamp
codec name, verified against the actual decoder implementations in `internal/encoding/`, NOT
inferred from the benchmark numbers) — **never assume a codec is O(1) for random access just
because its name suggests it or because a sibling axis (e.g. Raw timestamps) is O(1)**. Verified
complexity classes as of 2026-07:
- Raw (timestamp or value): true O(1), direct offset into a fixed-width array.
- ALP (value): O(1) windowed bit read + O(log k) binary search over that column's exception
  sidecar (k = exceptions in that column, not n). Not a plain O(1) — don't round it down.
- Gorilla, Chimp (value): O(index) — must sequentially decode the XOR chain from the start of
  the column. This is true regardless of which *timestamp* encoding the combo pairs it with;
  don't assume "Raw + Chimp" is O(1) just because the timestamp half is.
- Delta, DeltaPacked (timestamp): O(index) — must sequentially decode every delta from the
  start, since each value depends on the accumulated sum before it.

If a new value/timestamp encoding is ever added, its `At()` complexity MUST be verified by
reading the actual decoder source (look for a doc comment stating Big-O, or read the loop
structure directly) before adding it to `AT_COMPLEXITY` — do not guess from the encoding's name
or its compression characteristics.

#### `{{SCALING_TABLE_STANDARD}}`

Create a pivot table from `scaling` data for **standard (non-shared) combos only**:

```markdown
| Points/Metric | raw-raw | raw-gorilla | raw-chimp | raw-alp | delta-raw | delta-gorilla | delta-chimp | delta-alp | deltapacked-raw | deltapacked-gorilla | deltapacked-chimp | deltapacked-alp |
|---------------|---------|-------------|-----------|---------|-----------|---------------|-------------|-----------|-----------------|---------------------|-------------------|-----------------|
| 1 | 32.16 | ... | ... | ... | ... | ... | ... | ... | ... | ... | ... | ... |
```

Column set is derived from whatever labels are present in the JSON — don't hardcode this list in
the script; it's illustrative here only.

Format `bytes_per_point` to 2 decimal places.

#### `{{SCALING_TABLE_SHARED}}`

Create a pivot table from `scaling` data for **shared-timestamp combos only**:

```markdown
| Points/Metric | shared-raw-raw | shared-raw-gorilla | shared-raw-chimp | shared-raw-alp | shared-delta-raw | shared-delta-gorilla | shared-delta-chimp | shared-delta-alp | shared-deltapacked-raw | shared-deltapacked-gorilla | shared-deltapacked-chimp | shared-deltapacked-alp |
|---------------|----------------|--------------------|----|----|------------|------------|----------|----|------------|------------|----------|----|
| 1 | 26.22 | ... | ... | ... | ... | ... | ... | ... | ... | ... | ... | ... |
```

Format `bytes_per_point` to 2 decimal places.

#### `{{SCALING_INSIGHTS}}`

Generate 4-6 bullet points from the scaling data:
- Identify the PPM threshold where overhead becomes acceptable (BPP within 30% of converged)
- Identify diminishing returns threshold (BPP within 5% of converged)
- Compare how different combos converge: raw-raw amortizes only fixed overhead while compressed combos benefit from amortizing encoding metadata
- Call out the specific "sweet spot" PPM range with actual BPP numbers
- Compare shared-TS vs non-shared at converged PPM: cite the percentage savings from timestamp deduplication
- Note that shared-TS savings scale with the number of metrics (more metrics = more deduplication benefit)

#### `{{DECISION_TREE}}`

Generate a text-based decision tree (using `├─`, `└─`, `│` characters) from benchmark data.
Determine the recommendations by comparing matrix results:
- **Best compression**: combo with lowest `bytes_per_point`
- **Fastest encode**: combo with lowest `encode.ns_per_op`
- **Fastest decode/iteration**: combo with lowest `iter_seq.ns_per_op`
- **Random access**: combo with lowest `random_value_at.ns_per_op + random_timestamp_at.ns_per_op`
  across the WHOLE matrix — **do not pre-filter to Raw-timestamp combos**, see below

Important domain knowledge:
- DeltaPacked's advantage over Delta is **decode/iteration speed** (Group Varint batch decoding), not compression ratio
- **Do NOT assume Raw-timestamp combos are the best random-access pick, and do NOT assume any
  combo has "O(1) TimestampAt/ValueAt" as a blanket claim.** Random access has two independent
  axes — value encoding and timestamp encoding — with different codecs on each axis, and they
  don't share a complexity class just because they're paired in one combo. A prior version of
  this generator picked "best Raw-timestamp combo by BPP" and asserted flat O(1) for both axes;
  that was wrong (e.g. "Shared Raw + Chimp" was flagged as O(1) ValueAt when Chimp's `At()` is
  actually O(index), see `internal/encoding/numeric_chimp.go`'s `At` method — a sequential
  XOR-chain decode). Use the real measured `random_value_at`/`random_timestamp_at` fields and
  the `AT_COMPLEXITY` table (see `{{RANDOM_ACCESS_PERFORMANCE}}` above) instead.
- Shared-TS combos require all metrics to share identical timestamps; the tree should branch on this criterion first
- Use actual combo labels and benchmark numbers in the tree nodes

#### `{{CONFIGURATION_SELECTION}}`

Generate a use-case → configuration table. Derive each row from actual benchmark rankings:

| Row | How to determine | What to cite |
|-----|------------------|--------------|
| Best compression | Lowest `bytes_per_point` combo | Actual BPP and savings% |
| Fastest iteration | Lowest `iter_seq.ns_per_op` combo | Actual ns/op |
| Fastest encode | Lowest `encode.ns_per_op` combo | Actual ns/op |
| Best balance | Combo that ranks in top 3 for both size and iteration speed | Both metrics |
| Random access | Lowest `random_value_at.ns_per_op + random_timestamp_at.ns_per_op`, full matrix | Both measured ns/op + each axis's real `AT_COMPLEXITY`, not an assumed O(1) |
| Maximum throughput | `raw-raw` baseline | Encode ns/op |

All rationale should reference specific benchmark numbers, not generic descriptions.

#### `{{PPM_GUIDELINES}}`

Generate a PPM zone table from scaling data. Use the best-compression combo's scaling series:

1. Find the **converged value** = `bytes_per_point` at the highest tested PPM
2. Determine zone boundaries:
   - **Poor**: BPP > 2× converged value → cite actual PPM range and BPP
   - **Moderate**: BPP between 1.3-2× converged → cite actual PPM range and BPP
   - **Good**: BPP between 1.05-1.3× converged → cite actual PPM range and BPP
   - **Optimal**: BPP within 5% of converged → cite actual PPM range and BPP

Format as a table with Zone, PPM Range, BPP at boundaries, and recommendation.

### Step 3: Verify

After writing, read back `docs/performance.md` and verify:
1. All placeholders are replaced (no `{{...}}` remains)
2. Tables render correctly
3. Numbers look reasonable (bytes/point should be 6-17 range for 200 PPM; shared-TS combos will be lower than non-shared)
4. Scaling table shows decreasing BPP as points increase (two tables: standard and shared-TS)
5. Decision tree and config table reference actual benchmark numbers
6. Shared-TS combos show ~20-25% additional savings over equivalent non-shared combos
7. Shared-TS combos show faster decode (smaller blobs) but similar iteration speed
8. Random Access table: Raw (value or timestamp) should be near the fastest on its axis; Gorilla/Chimp
   ValueAt and Delta/DeltaPacked TimestampAt should be visibly slower (sequential decode) than Raw/ALP
   on the same axis — if a Gorilla/Chimp combo's ValueAt looks as fast as Raw's, something is wrong
   (check the access pattern actually varies the index; index 0 for every metric would hide the
   O(index) cost). ALP's ValueAt should sit between Raw and Gorilla/Chimp, closer to Raw.

## Domain Knowledge for Interpreting Results

These hints help generate accurate observations from the data:

- **DeltaPacked vs Delta**: DeltaPacked uses Group Varint encoding for **faster decode/iteration**, not for better compression. Size difference is marginal.
- **Chimp vs Gorilla**: Chimp typically achieves slightly better compression ratio. Both use XOR-based encoding.
- **ALP**: Adaptive Lossless floating-Point encoding (`format.TypeALP`). Wins big (2.5–6× smaller than the next-best codec) on **decimal-quantized** data — sensor readings rounded to a fixed number of decimal places. On this skill's default benchmark data (a full-precision random walk, not decimal-quantized), ALP will NOT show its real advantage and may rank worse than Chimp/Gorilla on both size and speed — that's expected, not a regression. ALP's encode is also markedly slower than Gorilla/Chimp (per-column (e,f) search cost). See `docs/performance.md`'s "Codec Selection by Data Shape" section (sourced from `tests/measurev2`'s realistic profiles, not this matrix) for where ALP actually wins.
- **Shared Timestamps**: `WithSharedTimestamps()` deduplicates identical timestamp sequences across metrics. When all metrics share the same sampling schedule (typical in monitoring), the timestamp column is stored once instead of N times. Savings scale with metric count: more metrics = greater benefit. Expect ~20-25% additional savings over non-shared equivalents at 200 metrics.
- **Shared-TS decode advantage**: Shared-TS combos decode faster because the blob is smaller (less data to parse). The decode memory footprint is also smaller (shared timestamp index vs per-metric copies).
- **Scaling**: Below ~10 points/metric, fixed per-metric overhead dominates. Above ~100, diminishing returns.
- **Encode speed**: Raw encoding is fastest (no computation). Compressed encodings trade CPU for space. Shared-TS combos have slightly higher encode cost due to the deduplication detection logic.
- **Decode speed**: All combos within the same group (shared vs non-shared) tend to have similar decode speed since it's dominated by header parsing overhead. Shared-TS combos decode faster than non-shared due to smaller blob size.
- **Iteration**: Compressed data can iterate faster than raw due to reduced memory bandwidth — smaller data fits better in CPU cache.

