---
name: update-performance-report
description: Run encoding benchmarks and update docs/PERFORMANCE_V2.md with fresh data
---

# Update Performance Report

This skill runs the encoding benchmark matrix tool and generates `docs/PERFORMANCE_V2.md` from a template + benchmark data.

## Prerequisites

- The workspace must be the `mebo` project root
- `tests/measurev2/` must exist with the benchmark tool

## Workflow

### Step 1: Run the benchmark tool

```bash
cd tests/measurev2 && go run . -pretty -verbose -output /tmp/mebo_bench_results.json 2>&1
```

Wait for completion. With default settings (200 metrics × 200 points, 18 combos: 9 standard + 9 shared-timestamp), this takes ~3-6 minutes.

### Step 2: Generate the report

Run the generation script, which reads the benchmark JSON and template, fills all `{{PLACEHOLDER}}` values, and writes the output:

```bash
python3 .agents/skills/update-performance-report/scripts/generate_report.py \
  /tmp/mebo_bench_results.json \
  .agents/skills/update-performance-report/PERFORMANCE_TEMPLATE.md \
  docs/PERFORMANCE_V2.md
```

The script exits with code 0 on success and prints a summary (combo count, best/worst BPP).
If any placeholders remain unfilled, it exits with code 1.

**Do NOT modify `docs/PERFORMANCE.md`** — that is the original document.

### Reference: JSON structure and placeholders

The benchmark JSON has this structure:
```
{
  "metadata": { "go_version", "os", "arch", "num_cpu", "timestamp", "data_config" },
  "matrix": [ { per-combo benchmark results (18 entries: 9 standard + 9 shared-TS) } ],
  "scaling": [ { per-combo bytes/point at different point counts (18 entries) } ]
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
- How does DeltaPacked compare to Delta? (small improvement in size, advantage is decode speed)
- Is encode speed vs compression a meaningful tradeoff?
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

#### `{{SCALING_TABLE_STANDARD}}`

Create a pivot table from `scaling` data for **standard (non-shared) combos only**:

```markdown
| Points/Metric | raw-raw | raw-gorilla | raw-chimp | delta-raw | delta-gorilla | delta-chimp | deltapacked-raw | deltapacked-gorilla | deltapacked-chimp |
|---------------|---------|-------------|-----------|-----------|---------------|-------------|-----------------|---------------------|-------------------|
| 1 | 32.16 | ... | ... | ... | ... | ... | ... | ... | ... |
```

Format `bytes_per_point` to 2 decimal places.

#### `{{SCALING_TABLE_SHARED}}`

Create a pivot table from `scaling` data for **shared-timestamp combos only**:

```markdown
| Points/Metric | shared-raw-raw | shared-raw-gorilla | shared-raw-chimp | shared-delta-raw | shared-delta-gorilla | shared-delta-chimp | shared-deltapacked-raw | shared-deltapacked-gorilla | shared-deltapacked-chimp |
|---------------|----------------|--------------------|----|----------|------------|----------|------------|------------|----------|
| 1 | 26.22 | ... | ... | ... | ... | ... | ... | ... | ... |
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
- **Random access**: Raw-timestamp combos (support O(1) `TimestampAt`/`ValueAt`)

Important domain knowledge:
- DeltaPacked's advantage over Delta is **decode/iteration speed** (Group Varint batch decoding), not compression ratio
- Raw timestamp combos support O(1) random access; Delta/DeltaPacked require sequential scan
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
| Random access | Best Raw-timestamp combo by `bytes_per_point` | BPP + note O(1) access |
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

After writing, read back `docs/PERFORMANCE_V2.md` and verify:
1. All placeholders are replaced (no `{{...}}` remains)
2. Tables render correctly
3. Numbers look reasonable (bytes/point should be 6-17 range for 200 PPM; shared-TS combos will be lower than non-shared)
4. Scaling table shows decreasing BPP as points increase (two tables: standard and shared-TS)
5. Decision tree and config table reference actual benchmark numbers
6. Shared-TS combos show ~20-25% additional savings over equivalent non-shared combos
7. Shared-TS combos show faster decode (smaller blobs) but similar iteration speed

## Domain Knowledge for Interpreting Results

These hints help generate accurate observations from the data:

- **DeltaPacked vs Delta**: DeltaPacked uses Group Varint encoding for **faster decode/iteration**, not for better compression. Size difference is marginal.
- **Chimp vs Gorilla**: Chimp typically achieves slightly better compression ratio. Both use XOR-based encoding.
- **Shared Timestamps**: `WithSharedTimestamps()` deduplicates identical timestamp sequences across metrics. When all metrics share the same sampling schedule (typical in monitoring), the timestamp column is stored once instead of N times. Savings scale with metric count: more metrics = greater benefit. Expect ~20-25% additional savings over non-shared equivalents at 200 metrics.
- **Shared-TS decode advantage**: Shared-TS combos decode faster because the blob is smaller (less data to parse). The decode memory footprint is also smaller (shared timestamp index vs per-metric copies).
- **Scaling**: Below ~10 points/metric, fixed per-metric overhead dominates. Above ~100, diminishing returns.
- **Encode speed**: Raw encoding is fastest (no computation). Compressed encodings trade CPU for space. Shared-TS combos have slightly higher encode cost due to the deduplication detection logic.
- **Decode speed**: All combos within the same group (shared vs non-shared) tend to have similar decode speed since it's dominated by header parsing overhead. Shared-TS combos decode faster than non-shared due to smaller blob size.
- **Iteration**: Compressed data can iterate faster than raw due to reduced memory bandwidth — smaller data fits better in CPU cache.

