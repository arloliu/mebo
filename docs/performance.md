# Performance Guide

> Most of this document is **auto-generated** by the `update-performance-report` agent skill
> from benchmark data — everything from [Quick Reference](#quick-reference) through
> [Scaling Analysis](#scaling-analysis). To regenerate those sections: run `tests/measurev2/`
> and use the skill.
>
> The [Codec Selection by Data Shape](#codec-selection-by-data-shape) section is composed
> manually from `tests/measurev2`'s profile-based benchmarks (its "Provenance" box has the
> reproduce recipe) — the skill does not regenerate it. **Regenerating this document wipes that
> section** — re-add it from `tests/measurev2/results/matrix_*.json` (regenerate those first via
> the reproduce recipe; they're gitignored) after running the skill.

| | |
|---|---|
| **Benchmark Date** | 2026-07-16 |
| **Platform** | linux/amd64 (32 CPUs), Go go1.26.1 |
| **Data** | 200 metrics × 200 points = 40,000 total data points |
| **Value Jitter** | ±0.5% per point (random walk) |
| **Timestamp Jitter** | ±0.1% of 1s interval |
| **Compression Codecs** | None (testing encoding algorithms only) |

This document provides encoding benchmark results, scaling analysis, and best practices for Mebo.

## Table of Contents

- [Quick Reference](#quick-reference)
- [Benchmark Methodology](#benchmark-methodology)
- [Encoding Comparison](#encoding-comparison)
- [Encode Performance](#encode-performance)
- [Decode Performance](#decode-performance)
- [Iteration Performance](#iteration-performance)
- [Random Access Performance](#random-access-performance)
- [Scaling Analysis](#scaling-analysis)
- [Codec Selection by Data Shape](#codec-selection-by-data-shape)
- [Choosing an Encoding Strategy](#choosing-an-encoding-strategy)

## Quick Reference

**TL;DR — Recommended Configurations:**

| Metric | Value | Configuration |
|--------|-------|---------------|
| **Best Compression** | 6.349 bytes/point (60.5% savings) | Shared Delta + Chimp |
| **Best Balance** | 6.350 bytes/point (60.5% savings) | Shared DeltaPacked + Chimp |
| **Fastest Encode** | 323,247 ns/op | Delta + Raw |
| **Baseline** | 16.081 bytes/point | Raw + Raw |

## Benchmark Methodology

### Test Environment

| Parameter | Value | Description |
|-----------|-------|-------------|
| **Go Version** | go1.26.1 | Compiler and runtime |
| **OS / Arch** | linux/amd64 | Operating system and CPU architecture |
| **CPU Cores** | 32 | Available logical CPUs |
| **Metrics** | 200 | Number of independent sensor metrics |
| **Points/Metric** | 200 | Data points per metric (for matrix benchmarks) |
| **Value Jitter** | ±0.5% | Per-point random walk delta (models semiconductor sensor noise) |
| **Timestamp Jitter** | ±0.1% | Variation in 1-second sampling interval (models industrial protocol jitter) |
| **Sampling Interval** | 1 second | Base interval between data points |
| **Seed** | 42 | Fixed for reproducibility |
| **Compression** | None | No codec layer — testing encoding algorithms only |

### Test Data Characteristics

**Realistic Time-Series Simulation:**

- **Timestamps**: 1-second intervals with configurable jitter (simulates real monitoring scrape intervals)
- **Values**: Random walk with configurable jitter (simulates slowly-changing metrics like CPU, memory)
- **Metric IDs**: xxHash64 hashed from sequential names
- **Seed**: Fixed (42) for reproducibility

### Running Benchmarks

```bash
# Quick benchmark (small data)
cd tests/measurev2 && go run . -metrics 50 -points 100 -pretty -verbose

# Full benchmark (default settings)
cd tests/measurev2 && go run . -pretty -verbose -output results.json

# Via Makefile
make bench-measure
```

## Encoding Comparison

All 24 valid encoding combinations (12 standard timestamp × value + 12 with shared timestamps — 3 timestamp encodings × 4 value encodings: Raw, Gorilla, Chimp, ALP), benchmarked without additional compression codecs.
Shared-timestamp combos use `WithSharedTimestamps()` to deduplicate identical timestamp sequences across metrics.

Sorted by encoded size (most efficient first):

| Configuration | Bytes/Point | Space Savings | vs Raw | Encode (ns/op) | Decode (ns/op) | Iterate (ns/op) |
|---------------|-------------|---------------|--------|----------------|----------------|-----------------|
| Shared Delta + Chimp | 6.349 | 60.5% | 2.533× | 597,248 | 6,608 | 404,413 |
| Shared DeltaPacked + Chimp | 6.350 | 60.5% | 2.532× | 625,257 | 6,477 | 453,305 |
| Shared Raw + Chimp | 6.380 | 60.3% | 2.521× | 600,777 | 6,317 | 444,593 |
| Shared Delta + ALP | 6.473 | 59.7% | 2.484× | 2,588,528 | 7,178 | 243,346 |
| Shared DeltaPacked + ALP | 6.474 | 59.7% | 2.484× | 2,589,407 | 7,128 | 231,307 |
| Shared Raw + ALP | 6.503 | 59.6% | 2.473× | 2,576,237 | 7,034 | 216,149 |
| Shared Delta + Gorilla | 6.597 | 59.0% | 2.438× | 475,523 | 6,523 | 285,011 |
| Shared DeltaPacked + Gorilla | 6.598 | 59.0% | 2.437× | 513,795 | 7,100 | 339,714 |
| Shared Raw + Gorilla | 6.627 | 58.8% | 2.427× | 483,255 | 6,329 | 320,446 |
| Shared Delta + Raw | 8.101 | 49.6% | 1.985× | 378,187 | 6,567 | 287,253 |
| Shared DeltaPacked + Raw | 8.102 | 49.6% | 1.985× | 374,029 | 6,429 | 221,050 |
| Shared Raw + Raw | 8.131 | 49.4% | 1.978× | 363,323 | 6,306 | 297,406 |
| Delta + Chimp | 8.297 | 48.4% | 1.938× | 560,241 | 5,751 | 384,687 |
| Delta + ALP | 8.423 | 47.6% | 1.909× | 2,589,267 | 6,881 | 248,120 |
| DeltaPacked + Chimp | 8.487 | 47.2% | 1.895× | 597,265 | 5,677 | 453,548 |
| Delta + Gorilla | 8.544 | 46.9% | 1.882× | 441,727 | 5,769 | 286,369 |
| DeltaPacked + ALP | 8.613 | 46.4% | 1.867× | 2,583,224 | 6,495 | 241,719 |
| DeltaPacked + Gorilla | 8.734 | 45.7% | 1.841× | 465,912 | 5,679 | 347,566 |
| Delta + Raw | 10.054 | 37.5% | 1.599× | 323,247 | 5,740 | 285,955 |
| DeltaPacked + Raw | 10.244 | 36.3% | 1.570× | 345,228 | 6,320 | 218,864 |
| Raw + Chimp | 14.324 | 10.9% | 1.123× | 571,197 | 5,635 | 442,786 |
| Raw + ALP | 14.449 | 10.1% | 1.113× | 2,524,299 | 6,441 | 205,270 |
| Raw + Gorilla | 14.571 | 9.4% | 1.104× | 429,457 | 5,637 | 315,722 |
| Raw + Raw | 16.081 | 0.0% | 1.000× | 339,509 | 5,656 | 291,066 |

### Key Observations

- **Best compression**: Shared Delta + Chimp achieves 6.349 bytes/point (60.5% savings vs raw-raw baseline). Shared timestamp deduplication eliminates redundant timestamp storage across 200 metrics.
- **Shared timestamps**: Enabling `WithSharedTimestamps()` provides 23% additional savings over the best non-shared configuration (Delta + Chimp at 8.297 bytes/point). The savings come from storing the timestamp column once instead of 200 times.
- **Chimp vs Gorilla**: Chimp consistently outperforms Gorilla by ~2.9% in compression. For example, Delta + Chimp (8.297 BPP) vs Delta + Gorilla (8.544 BPP). Both use XOR-based floating-point encoding.
- **ALP on this dataset**: Delta + ALP is 8.423 BPP, 1.5% larger than Chimp on this dataset — this benchmark's data is a full-precision random walk, not decimal-quantized, which is not ALP's strength. ALP's main scheme wins big (4–6× smaller than raw, 1–2.5× smaller than the next-best codec) specifically on decimal-quantized sensor data; see the "Codec Selection by Data Shape" section below for the profile-based comparison where it does shine. ALP's encode is also markedly slower here (2,589,267 vs 560,241 ns/op for Chimp) due to its per-column (e,f) search.
- **DeltaPacked vs Delta**: DeltaPacked shows ~2.3% larger encoded size than Delta (8.487 vs 8.297 BPP). DeltaPacked's advantage is **decode/iteration speed** via Group Varint batch decoding, not compression ratio.
- **Encode speed tradeoff**: Delta + Raw encodes fastest at 323,247 ns/op, ahead of the Raw + Raw baseline (339,509 ns/op) — despite doing more computation, it allocates less per op (456,662 B/op vs 699,259 B/op).
- **Decode speed**: The fastest shared-TS combo decodes ~12% close to (slightly slower than) the fastest non-shared combo (6,306 vs 5,635 ns/op) — decode speed is dominated by header-parsing overhead, so blob-size differences from timestamp dedup show up more in memory footprint than in raw decode latency at this scale.

## Encode Performance

Encoding speed and memory allocation for each combination:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Delta + Raw | 323,247 | 456,662 | 34 |
| Raw + Raw | 339,509 | 699,259 | 34 |
| DeltaPacked + Raw | 345,228 | 465,857 | 34 |
| Shared Raw + Raw | 363,323 | 1,083,776 | 64 |
| Shared DeltaPacked + Raw | 374,029 | 846,420 | 64 |
| Shared Delta + Raw | 378,187 | 840,091 | 64 |
| Raw + Gorilla | 429,457 | 641,233 | 34 |
| Delta + Gorilla | 441,727 | 390,184 | 34 |
| DeltaPacked + Gorilla | 465,912 | 397,974 | 34 |
| Shared Delta + Gorilla | 475,523 | 719,636 | 64 |
| Shared Raw + Gorilla | 483,255 | 962,326 | 64 |
| Shared DeltaPacked + Gorilla | 513,795 | 727,310 | 64 |
| Delta + Chimp | 560,241 | 384,718 | 34 |
| Raw + Chimp | 571,197 | 622,698 | 34 |
| Shared Delta + Chimp | 597,248 | 700,862 | 64 |
| DeltaPacked + Chimp | 597,265 | 389,297 | 34 |
| Shared Raw + Chimp | 600,777 | 946,720 | 64 |
| Shared DeltaPacked + Chimp | 625,257 | 712,119 | 64 |
| Raw + ALP | 2,524,299 | 691,339 | 203 |
| Shared Raw + ALP | 2,576,237 | 1,040,105 | 228 |
| DeltaPacked + ALP | 2,583,224 | 451,222 | 202 |
| Shared Delta + ALP | 2,588,528 | 777,999 | 227 |
| Delta + ALP | 2,589,267 | 444,421 | 202 |
| Shared DeltaPacked + ALP | 2,589,407 | 779,577 | 227 |

## Decode Performance

Decoding speed (NewDecoder + Decode) and memory allocation:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Raw + Chimp | 5,635 | 32,824 | 7 |
| Raw + Gorilla | 5,637 | 32,824 | 7 |
| Raw + Raw | 5,656 | 32,824 | 7 |
| DeltaPacked + Chimp | 5,677 | 32,824 | 7 |
| DeltaPacked + Gorilla | 5,679 | 32,824 | 7 |
| Delta + Raw | 5,740 | 32,824 | 7 |
| Delta + Chimp | 5,751 | 32,824 | 7 |
| Delta + Gorilla | 5,769 | 32,824 | 7 |
| Shared Raw + Raw | 6,306 | 22,696 | 11 |
| Shared Raw + Chimp | 6,317 | 22,696 | 11 |
| DeltaPacked + Raw | 6,320 | 32,824 | 7 |
| Shared Raw + Gorilla | 6,329 | 22,696 | 11 |
| Shared DeltaPacked + Raw | 6,429 | 22,696 | 11 |
| Raw + ALP | 6,441 | 32,824 | 7 |
| Shared DeltaPacked + Chimp | 6,477 | 22,696 | 11 |
| DeltaPacked + ALP | 6,495 | 32,824 | 7 |
| Shared Delta + Gorilla | 6,523 | 22,696 | 11 |
| Shared Delta + Raw | 6,567 | 22,696 | 11 |
| Shared Delta + Chimp | 6,608 | 22,696 | 11 |
| Delta + ALP | 6,881 | 32,824 | 7 |
| Shared Raw + ALP | 7,034 | 22,696 | 11 |
| Shared DeltaPacked + Gorilla | 7,100 | 22,696 | 11 |
| Shared DeltaPacked + ALP | 7,128 | 22,696 | 11 |
| Shared Delta + ALP | 7,178 | 22,696 | 11 |

## Iteration Performance

Sequential iteration speed (iterating all data points via `blob.All(metricID)`):

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Raw + ALP | 205,270 | 737,879 | 1003 |
| Shared Raw + ALP | 216,149 | 738,116 | 1003 |
| DeltaPacked + Raw | 218,864 | 25,608 | 801 |
| Shared DeltaPacked + Raw | 221,050 | 25,608 | 801 |
| Shared DeltaPacked + ALP | 231,307 | 738,136 | 1003 |
| DeltaPacked + ALP | 241,719 | 738,028 | 1003 |
| Shared Delta + ALP | 243,346 | 738,108 | 1003 |
| Delta + ALP | 248,120 | 738,011 | 1003 |
| Shared Delta + Gorilla | 285,011 | 19,208 | 601 |
| Delta + Raw | 285,955 | 25,608 | 801 |
| Delta + Gorilla | 286,369 | 19,208 | 601 |
| Shared Delta + Raw | 287,253 | 25,608 | 801 |
| Raw + Raw | 291,066 | 28,808 | 801 |
| Shared Raw + Raw | 297,406 | 28,808 | 801 |
| Raw + Gorilla | 315,722 | 22,408 | 601 |
| Shared Raw + Gorilla | 320,446 | 22,408 | 601 |
| Shared DeltaPacked + Gorilla | 339,714 | 19,208 | 601 |
| DeltaPacked + Gorilla | 347,566 | 19,208 | 601 |
| Delta + Chimp | 384,687 | 19,208 | 601 |
| Shared Delta + Chimp | 404,413 | 19,208 | 601 |
| Raw + Chimp | 442,786 | 22,408 | 601 |
| Shared Raw + Chimp | 444,593 | 22,408 | 601 |
| Shared DeltaPacked + Chimp | 453,305 | 19,208 | 601 |
| DeltaPacked + Chimp | 453,548 | 19,208 | 601 |

**Note:** Compressed encodings can iterate faster than raw due to reduced memory bandwidth — smaller data fits better in CPU cache.

## Random Access Performance

`ValueAt`/`TimestampAt` at a uniformly random index per metric (not a fixed first/last probe —
see `randomAccessPattern` in `tests/measurev2/bench.go`). This matters because the encodings
have fundamentally different random-access complexity, not just different constants:

| Encoding | `At()` complexity | Why |
|---|---|---|
| Raw (timestamp or value) | O(1) | Direct offset into a fixed-width array |
| ALP (value) | O(1) + O(log k) | O(1) windowed bit read, plus binary search over that column's exception sidecar (k = exceptions in that column, not n) |
| Delta / DeltaPacked (timestamp) | O(index) | Must sequentially decode every delta from the start — each value depends on the accumulated sum before it |
| Gorilla / Chimp (value) | O(index) | Must sequentially decode the XOR chain from the start of the column |

A uniformly random index makes the O(index) encodings pay their realistic *average* cost across
a column, not a cherry-picked best (index 0) or worst (last index) case.

| Configuration | ValueAt (ns/op) | Value complexity | TimestampAt (ns/op) | Timestamp complexity |
|---|---:|---|---:|---|
| Raw + Raw | 4,626 | O(1) | 4,197 | O(1) |
| Shared Raw + Raw | 4,681 | O(1) | 4,486 | O(1) |
| Shared Raw + ALP | 6,418 | O(1) + O(log k) exceptions | 4,488 | O(1) |
| Raw + ALP | 8,250 | O(1) + O(log k) exceptions | 4,305 | O(1) |
| Shared DeltaPacked + Raw | 4,831 | O(1) | 31,253 | O(index), sequential decode from the start |
| DeltaPacked + Raw | 4,612 | O(1) | 31,619 | O(index), sequential decode from the start |
| Shared DeltaPacked + ALP | 6,445 | O(1) + O(log k) exceptions | 30,935 | O(index), sequential decode from the start |
| DeltaPacked + ALP | 8,495 | O(1) + O(log k) exceptions | 32,245 | O(index), sequential decode from the start |
| Shared Delta + Raw | 4,680 | O(1) | 37,314 | O(index), sequential decode from the start |
| Shared Delta + ALP | 6,442 | O(1) + O(log k) exceptions | 37,520 | O(index), sequential decode from the start |
| Delta + Raw | 4,649 | O(1) | 43,943 | O(index), sequential decode from the start |
| Delta + ALP | 8,252 | O(1) + O(log k) exceptions | 46,979 | O(index), sequential decode from the start |
| Raw + Gorilla | 183,840 | O(index), sequential XOR decode from the start | 4,364 | O(1) |
| Shared Raw + Gorilla | 186,651 | O(index), sequential XOR decode from the start | 4,451 | O(1) |
| Shared DeltaPacked + Gorilla | 179,735 | O(index), sequential XOR decode from the start | 31,011 | O(index), sequential decode from the start |
| Shared Delta + Gorilla | 178,526 | O(index), sequential XOR decode from the start | 37,327 | O(index), sequential decode from the start |
| Raw + Chimp | 212,147 | O(index), sequential XOR decode from the start | 4,451 | O(1) |
| DeltaPacked + Gorilla | 184,745 | O(index), sequential XOR decode from the start | 32,032 | O(index), sequential decode from the start |
| Shared Raw + Chimp | 214,618 | O(index), sequential XOR decode from the start | 4,472 | O(1) |
| Delta + Gorilla | 182,524 | O(index), sequential XOR decode from the start | 43,657 | O(index), sequential decode from the start |
| Shared DeltaPacked + Chimp | 212,995 | O(index), sequential XOR decode from the start | 30,927 | O(index), sequential decode from the start |
| Shared Delta + Chimp | 210,376 | O(index), sequential XOR decode from the start | 37,515 | O(index), sequential decode from the start |
| DeltaPacked + Chimp | 216,893 | O(index), sequential XOR decode from the start | 31,475 | O(index), sequential decode from the start |
| Delta + Chimp | 211,531 | O(index), sequential XOR decode from the start | 43,657 | O(index), sequential decode from the start |

## Scaling Analysis

How bytes-per-point changes as points-per-metric increases, for each encoding combination.
The fixed per-metric overhead amortizes differently depending on the encoding.

### Standard Encodings

| Points/Metric | raw-raw | raw-gorilla | raw-chimp | raw-alp | delta-raw | delta-gorilla | delta-chimp | delta-alp | deltapacked-raw | deltapacked-gorilla | deltapacked-chimp | deltapacked-alp |
|---------------|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 32.160 | 32.160 | 32.160 | 33.160 | 32.160 | 32.160 | 32.160 | 33.160 | 32.160 | 32.160 | 32.160 | 33.160 |
| 2 | 24.080 | 23.942 | 23.582 | 24.580 | 21.580 | 21.442 | 21.082 | 22.080 | 21.580 | 21.442 | 21.082 | 22.080 |
| 5 | 19.232 | 18.315 | 17.946 | 19.432 | 14.592 | 13.675 | 13.306 | 14.792 | 14.761 | 13.844 | 13.475 | 14.961 |
| 10 | 17.616 | 16.369 | 16.097 | 17.131 | 12.262 | 11.014 | 10.743 | 11.777 | 12.419 | 11.171 | 10.900 | 11.934 |
| 20 | 16.808 | 15.369 | 15.159 | 15.664 | 11.097 | 9.658 | 9.447 | 9.952 | 11.291 | 9.852 | 9.642 | 10.146 |
| 50 | 16.323 | 14.783 | 14.602 | 14.825 | 10.401 | 8.861 | 8.679 | 8.902 | 10.581 | 9.041 | 8.860 | 9.083 |
| 100 | 16.162 | 14.621 | 14.418 | 14.553 | 10.169 | 8.629 | 8.426 | 8.561 | 10.360 | 8.819 | 8.616 | 8.751 |
| 150 | 16.108 | 14.582 | 14.356 | 14.502 | 10.092 | 8.567 | 8.340 | 8.487 | 10.280 | 8.754 | 8.527 | 8.674 |
| 200 | 16.081 | 14.571 | 14.324 | 14.449 | 10.054 | 8.544 | 8.297 | 8.423 | 10.244 | 8.734 | 8.487 | 8.613 |

### Shared-Timestamp Encodings

| Points/Metric | shared-raw-raw | shared-raw-gorilla | shared-raw-chimp | shared-raw-alp | shared-delta-raw | shared-delta-gorilla | shared-delta-chimp | shared-delta-alp | shared-deltapacked-raw | shared-deltapacked-gorilla | shared-deltapacked-chimp | shared-deltapacked-alp |
|---------------|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 26.220 | 26.220 | 26.220 | 27.220 | 26.220 | 26.220 | 26.220 | 27.220 | 26.220 | 26.220 | 26.220 | 27.220 |
| 2 | 17.130 | 16.970 | 16.630 | 17.630 | 17.117 | 16.957 | 16.617 | 17.617 | 17.117 | 16.957 | 16.617 | 17.617 |
| 5 | 11.676 | 10.777 | 10.394 | 11.876 | 11.653 | 10.754 | 10.371 | 11.853 | 11.654 | 10.755 | 10.372 | 11.854 |
| 10 | 9.858 | 8.627 | 8.331 | 9.351 | 9.831 | 8.600 | 8.303 | 9.323 | 9.832 | 8.601 | 8.305 | 9.325 |
| 20 | 8.949 | 7.529 | 7.309 | 7.807 | 8.920 | 7.500 | 7.280 | 7.778 | 8.921 | 7.501 | 7.282 | 7.779 |
| 50 | 8.404 | 6.881 | 6.691 | 6.909 | 8.374 | 6.851 | 6.661 | 6.880 | 8.375 | 6.852 | 6.662 | 6.880 |
| 100 | 8.222 | 6.693 | 6.485 | 6.615 | 8.192 | 6.663 | 6.455 | 6.585 | 8.193 | 6.664 | 6.455 | 6.586 |
| 150 | 8.161 | 6.643 | 6.415 | 6.545 | 8.131 | 6.613 | 6.385 | 6.514 | 8.132 | 6.614 | 6.386 | 6.515 |
| 200 | 8.131 | 6.627 | 6.380 | 6.503 | 8.101 | 6.597 | 6.349 | 6.473 | 8.102 | 6.598 | 6.350 | 6.474 |

### Key Insights

- **Overhead becomes acceptable at ~20 PPM**: Shared Delta + Chimp reaches 7.280 bytes/point (within 30% of converged value 6.349).
- **Diminishing returns above ~50 PPM**: BPP converges to 6.349 (within 5% threshold reached at 50 PPM with 6.661 BPP).
- **Shared timestamps scale with metric count**: At 200 PPM, Shared Delta + Chimp achieves 6.349 BPP vs Delta + Chimp at 8.297 BPP — a 23% additional saving from timestamp deduplication across 200 metrics.
- **Fixed overhead dominates at low PPM**: At 1 PPM, even the best combo (Shared Delta + Chimp) costs 26.220 bytes/point vs 6.349 converged — 4.1× overhead from per-metric headers.
- **Raw vs compressed convergence**: Raw + Raw overhead amortizes to 16.081 BPP (16 bytes per point for 8-byte timestamp + 8-byte float64). Compressed combos converge much lower because they also amortize encoding metadata while compressing the data itself.

## Codec Selection by Data Shape

The tables above use a single data profile: a full-precision random walk. Real metrics come in
different shapes — decimal-quantized sensor readings, monotonic counters, mostly-constant
values, genuinely full-precision noise — and **no single value codec wins across all of them.**
This section benchmarks five realistic profiles via
[`tests/measurev2`](../tests/measurev2)'s profile generators to show where each codec actually
wins, and explains why ALP doesn't appear in the [Encoding Comparison](#encoding-comparison)
matrix's top ranks above — that matrix's data isn't decimal-quantized, and decimal-quantized
data is exactly the shape ALP is built for.

**Provenance:** 200 metrics × 200 points, seed 42, value-jitter 0.5%, ts-jitter 0.1%, same
environment as above (go1.26.1, linux/amd64, 32 CPUs, 2026-07-16). The raw JSON is gitignored
(regenerate it yourself, don't expect it committed). Reproduce with:

```bash
cd tests/measurev2
for p in decimal_gauge_2dp decimal_gauge_4dp counter sparse_constant worst_case; do
  go run . -profile "$p" -metrics 200 -points 200 -pretty -output "results/matrix_$p.json"
done
```

### Best combo per profile

Non-shared-timestamp combos only, to isolate the value-codec comparison. Shared timestamps
(see [Scaling Analysis](#scaling-analysis) above) compress every one of these further still —
these are not the absolute smallest a given profile can reach, just the best without that extra
lever.

| Profile | Best combo | Bytes/point | vs Raw+Raw | Winning value codec |
|---|---|---:|---:|---|
| `decimal_gauge_2dp` — 2dp gauge random-walk, 15s scrape | Delta + ALP | 2.854 | 5.6× | ALP |
| `decimal_gauge_4dp` — 4dp gauge random-walk, 15s scrape | Delta + ALP | 3.802 | 4.2× | ALP |
| `counter` — monotonic integer counter, 15s scrape | Delta + ALP | 2.581 | 6.2× | ALP |
| `sparse_constant` — mostly-constant value, 60s scrape | Delta + Gorilla | 1.605 | 10.0× | Gorilla |
| `worst_case` — full-precision random walk, 1s | Delta + Chimp | 7.369 | 2.2× | Chimp |

Delta is the best timestamp tier in every profile (DeltaPacked trades ~0.25 B/pt for iteration
speed). The winning *value* codec changes with the data: **ALP** for decimals and counters,
**Gorilla** for sparse/constant data, **Chimp** for genuinely full-precision data. ALP's margin
over the *next-best codec* (not raw) varies a lot by shape — 2.53× on `decimal_gauge_2dp`
(vs Chimp's 7.212), 1.94× on `decimal_gauge_4dp` (vs Chimp's 7.367), but only 1.05× on `counter`
(vs Gorilla's 2.718, its closest competitor there). The "5.6–6.2×" figures above are all **vs
the Raw+Raw baseline**, not vs the next-best codec — don't conflate the two.

### Full compression grids (bytes/point)

<details>
<summary>Per-profile timestamp × value grids</summary>

#### decimal_gauge_2dp

| ts \ val | Raw | Gorilla | Chimp | ALP |
|---|---:|---:|---:|---:|
| Raw | 16.081 | 14.547 | 14.162 | 9.804 |
| Delta | 9.131 | 7.597 | 7.212 | **2.854** |
| DeltaPacked | 9.381 | 7.847 | 7.462 | 3.104 |

#### decimal_gauge_4dp

| ts \ val | Raw | Gorilla | Chimp | ALP |
|---|---:|---:|---:|---:|
| Raw | 16.081 | 14.567 | 14.317 | 10.752 |
| Delta | 9.131 | 7.617 | 7.367 | **3.802** |
| DeltaPacked | 9.381 | 7.867 | 7.617 | 4.052 |

#### counter

| ts \ val | Raw | Gorilla | Chimp | ALP |
|---|---:|---:|---:|---:|
| Raw | 16.081 | 9.668 | 9.991 | 9.531 |
| Delta | 9.131 | 2.718 | 3.041 | **2.581** |
| DeltaPacked | 9.381 | 2.968 | 3.291 | 2.831 |

#### sparse_constant

| ts \ val | Raw | Gorilla | Chimp | ALP |
|---|---:|---:|---:|---:|
| Raw | 16.081 | 8.555 | 8.658 | 9.205 |
| Delta | 9.131 | **1.605** | 1.708 | 2.255 |
| DeltaPacked | 9.381 | 1.855 | 1.958 | 2.505 |

#### worst_case

| ts \ val | Raw | Gorilla | Chimp | ALP |
|---|---:|---:|---:|---:|
| Raw | 16.081 | 14.571 | 14.324 | 14.449 |
| Delta | 9.126 | 7.616 | **7.369** | 7.494 |
| DeltaPacked | 9.376 | 7.866 | 7.619 | 7.744 |

</details>

### Speed by profile (encode & iterate, ns per 1,000 points, Delta timestamps)

`decode` (opening a blob via `NewDecoder`+`Decode`) is omitted — it's dominated by header
parsing rather than codec, so it's a roughly flat cost regardless of which value codec is
chosen (see the codec-dependent [Decode Performance](#decode-performance) table above for exact
per-combo numbers on the main matrix; this profile data follows the same pattern). The real read
cost that varies by codec is **iterate** — a full sequential `All()` materialization over every
point. Allocs are per whole-blob encode (200 columns). **Bold** marks the fastest iterate on
that profile.

| Profile | Codec | Encode ns/1k | Iterate ns/1k | Encode allocs/blob |
|---|---|---:|---:|---:|
| decimal_gauge_2dp | Raw | 7,881 | 6,603 | 34 |
| | Gorilla | 10,416 | 6,365 | 34 |
| | Chimp | 13,463 | 9,415 | 34 |
| | ALP | 41,394 | **5,341** | 62 |
| decimal_gauge_4dp | Raw | 7,703 | 6,419 | 34 |
| | Gorilla | 10,355 | 6,318 | 34 |
| | Chimp | 13,098 | 9,083 | 34 |
| | ALP | 49,606 | **5,460** | 221 |
| counter | Raw | 8,011 | 6,458 | 34 |
| | Gorilla | 10,613 | 7,356 | 34 |
| | Chimp | 9,726 | 6,741 | 34 |
| | ALP | 20,844 | **5,493** | 44 |
| sparse_constant | Raw | 7,633 | 6,270 | 34 |
| | Gorilla | 7,019 | **4,868** | 34 |
| | Chimp | 7,151 | 4,889 | 34 |
| | ALP | 30,622 | 5,490 | 76 |
| worst_case | Raw | 7,579 | **6,266** | 34 |
| | Gorilla | 10,228 | 6,391 | 34 |
| | Chimp | 13,347 | 9,216 | 34 |
| | ALP | 62,771 | 6,514 | 202 |

### Takeaways

- **ALP is the compression champion on decimal & counter data** (1.05–2.53× smaller than the
  next-best codec on this run — see the per-profile ratios above — and 4.2–6.2× smaller than the
  Raw+Raw baseline) and, after the July 2026 encode/decode optimization passes (see
  [`alp_optimization_history.md`](perf/alp_optimization_history.md)), its iteration got dramatically
  faster too: **fastest of any codec on the 3 profiles it also wins on size** (decimal_gauge_2dp,
  decimal_gauge_4dp, counter). Its allocation footprint dropped sharply as well (925→62
  allocs/blob on `decimal_gauge_2dp` since the June 2026 snapshot).
- **ALP is NOT the fastest to iterate on every profile** — on `sparse_constant` and `worst_case`
  (the two profiles where it also doesn't win on size), Gorilla/Chimp/Raw all iterate faster than
  ALP. Its decode-side wins are real but shape-dependent, same as its compression wins.
- **ALP's encode is still the slowest by a wide margin**: ~2.1–4.7× Chimp's encode cost across
  profiles (lowest gap on `counter` at 2.14×, widest on `worst_case` at 4.70×) — its per-column
  (e,f) search is real CPU work, not yet vectorized. Fine for batch/offline encoding; a poor fit
  if encode latency is on a hot path.
- **ALP is also the far better pick for random access** — see
  [Random Access Performance](#random-access-performance) above. Its `ValueAt` is O(1) + O(log k)
  (k = exceptions in the column), measured at 6,400–8,500 ns/op on the main matrix's 200-point
  columns; Gorilla/Chimp `ValueAt` is O(index) — a sequential XOR-chain decode from the start of
  the column — measured at 178,000–217,000 ns/op on the same columns, **25–35× slower than ALP**.
  This holds regardless of data shape, since it's a property of the decode algorithm, not the
  data. Raw is fastest of all (~4,600–4,800 ns/op) but has none of ALP's compression.
- **Gorilla is the all-rounder**: best on sparse data in *both* size and speed, cheap to encode.
- **Chimp** narrowly wins full-precision size; otherwise similar to Gorilla.
- **Choosing ALP blindly is still a trap on the wrong shape**: on `worst_case` (genuinely
  full-precision data) it's *larger* than Chimp — ALP only pays off where the data is actually
  decimal-quantized.
- This data-dependence is the empirical case for **per-column adaptive value-codec selection**,
  with Raw kept as a hard floor. See
  [`adaptive_selector_experiments.md`](perf/adaptive_selector_experiments.md) and the
  [implementation plan](plans/2026-06-15-adaptive-value-codec-selection.md) — not yet wired,
  tracked as follow-up work.



## Choosing an Encoding Strategy

### Decision Tree

```
What is your priority?
├─ Smallest encoded size?
│  ├─ All metrics share timestamps? → Shared Delta + Chimp (6.349 BPP, 60.5% savings)
│  └─ Independent timestamps?      → Delta + Chimp (8.297 BPP, 48.4% savings)
│
├─ Fastest encode?
│  └─ Delta + Raw (323,247 ns/op, 10.054 BPP)
│
├─ Fastest iteration / decode?
│  ├─ Sequential scan → Raw + ALP (205,270 ns/op)
│  └─ Random access  → Raw + Raw (ValueAt 4,626 ns/op [O(1)], TimestampAt 4,197 ns/op [O(1)])
│
└─ Best balance (size + speed)?
   ├─ With shared TS → Shared Delta + Chimp (6.349 BPP, 404,413 ns/op iter)
   └─ Without        → Delta + Chimp (8.297 BPP, 384,687 ns/op iter)
```

### Configuration Selection

| Use Case | Configuration | Key Metric | Rationale |
|----------|---------------|------------|-----------|
| **Best compression** | Shared Delta + Chimp | 6.349 BPP (60.5% savings) | Lowest bytes/point; shared timestamps eliminate redundant storage |
| **Fastest iteration** | Raw + ALP | 205,270 ns/op | Fastest sequential scan of any combo tested |
| **Fastest encode** | Delta + Raw | 323,247 ns/op | Fastest encode of any combo tested, for its compression tier |
| **Best balance** | Shared DeltaPacked + ALP | 6.474 BPP, 231,307 ns/op iter | Top-5 ranked in both compression and iteration speed |
| **Random access** | Raw + Raw | ValueAt 4,626 ns/op, TimestampAt 4,197 ns/op | Value: O(1); Timestamp: O(1) |
| **Maximum throughput** | Raw + Raw | 339,509 ns/op encode | Baseline; no encoding overhead but largest output |

### Points-per-Metric Guidelines

Using Shared Delta + Chimp scaling data (converged: 6.349 bytes/point):

| Zone | PPM Range | BPP Range | Overhead | Recommendation |
|------|-----------|-----------|----------|----------------|
| **Poor** | 1–2 | 26.220–16.617 | 162–313% | Batch more points if possible; fixed overhead dominates |
| **Moderate** | 5–10 | 10.371–8.303 | 31–63% | Acceptable for low-frequency metrics |
| **Good** | 20 | 7.280 | 15–15% | Good efficiency; recommended minimum for most use cases |
| **Optimal** | 50–200 | 6.661–6.349 | 0–5% | Excellent efficiency; diminishing returns beyond this range |
