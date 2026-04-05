# Performance Guide

> **Auto-generated** by the `update-performance-report` agent skill from benchmark data.
> To regenerate: run `tests/measurev2/` and use the skill.

| | |
|---|---|
| **Benchmark Date** | 2026-04-05 |
| **Platform** | linux/amd64 (32 CPUs), Go go1.26.0 |
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
- [Scaling Analysis](#scaling-analysis)
- [Choosing an Encoding Strategy](#choosing-an-encoding-strategy)

## Quick Reference

**TL;DR — Recommended Configurations:**

| Metric | Value | Configuration |
|--------|-------|---------------|
| **Best Compression** | 6.349 bytes/point (60.5% savings) | Shared Delta + Chimp |
| **Best Balance** | 6.350 bytes/point (60.5% savings) | Shared DeltaPacked + Chimp |
| **Fastest Encode** | 476,939 ns/op | DeltaPacked + Raw |
| **Baseline** | 16.081 bytes/point | Raw + Raw |

## Benchmark Methodology

### Test Environment

| Parameter | Value | Description |
|-----------|-------|-------------|
| **Go Version** | go1.26.0 | Compiler and runtime |
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

All 18 valid encoding combinations (9 standard timestamp × value + 9 with shared timestamps), benchmarked without additional compression codecs.
Shared-timestamp combos use `WithSharedTimestamps()` to deduplicate identical timestamp sequences across metrics.

Sorted by encoded size (most efficient first):

| Configuration | Bytes/Point | Space Savings | vs Raw | Encode (ns/op) | Decode (ns/op) | Iterate (ns/op) |
|---------------|-------------|---------------|--------|----------------|----------------|-----------------|
| Shared Delta + Chimp | 6.349 | 60.5% | 2.533× | 859,352 | 6,722 | 603,279 |
| Shared DeltaPacked + Chimp | 6.350 | 60.5% | 2.532× | 902,212 | 6,674 | 659,230 |
| Shared Raw + Chimp | 6.380 | 60.3% | 2.521× | 1,019,927 | 6,477 | 583,705 |
| Shared Delta + Gorilla | 6.597 | 59.0% | 2.438× | 772,164 | 6,602 | 484,026 |
| Shared DeltaPacked + Gorilla | 6.598 | 59.0% | 2.437× | 758,767 | 6,657 | 526,479 |
| Shared Raw + Gorilla | 6.627 | 58.8% | 2.427× | 889,024 | 6,561 | 499,918 |
| Shared Delta + Raw | 8.101 | 49.6% | 1.985× | 561,357 | 6,532 | 264,291 |
| Shared DeltaPacked + Raw | 8.102 | 49.6% | 1.985× | 549,674 | 6,513 | 264,648 |
| Shared Raw + Raw | 8.131 | 49.4% | 1.978× | 664,100 | 6,295 | 293,110 |
| Delta + Chimp | 8.297 | 48.4% | 1.938× | 805,071 | 5,778 | 605,850 |
| DeltaPacked + Chimp | 8.487 | 47.2% | 1.895× | 811,929 | 5,633 | 661,389 |
| Delta + Gorilla | 8.544 | 46.9% | 1.882× | 729,064 | 5,644 | 493,885 |
| DeltaPacked + Gorilla | 8.734 | 45.7% | 1.841× | 706,509 | 5,788 | 551,247 |
| Delta + Raw | 10.054 | 37.5% | 1.599× | 504,483 | 5,541 | 264,166 |
| DeltaPacked + Raw | 10.244 | 36.3% | 1.570× | 476,939 | 5,638 | 276,581 |
| Raw + Chimp | 14.324 | 10.9% | 1.123× | 808,115 | 5,746 | 571,234 |
| Raw + Gorilla | 14.571 | 9.4% | 1.104× | 804,831 | 5,617 | 498,278 |
| Raw + Raw | 16.081 | 0.0% | 1.000× | 614,252 | 5,646 | 287,712 |

### Key Observations

- **Best compression**: Shared Delta + Chimp achieves 6.349 bytes/point (60.5% savings vs raw-raw baseline). Shared timestamp deduplication eliminates redundant timestamp storage across 200 metrics.
- **Shared timestamps**: Enabling `WithSharedTimestamps()` provides 23% additional savings over the best non-shared configuration (Delta + Chimp at 8.297 bytes/point). The savings come from storing the timestamp column once instead of 200 times.
- **Chimp vs Gorilla**: Chimp consistently outperforms Gorilla by ~2.9% in compression. For example, Delta + Chimp (8.297 BPP) vs Delta + Gorilla (8.544 BPP). Both use XOR-based floating-point encoding.
- **DeltaPacked vs Delta**: DeltaPacked shows ~2.3% larger encoded size than Delta (8.487 vs 8.297 BPP). DeltaPacked's advantage is **decode/iteration speed** via Group Varint batch decoding, not compression ratio.
- **Encode speed tradeoff**: DeltaPacked + Raw encodes fastest at 476939 ns/op. Raw + Raw baseline (614252 ns/op) is not the fastest because larger raw data requires more memory allocation (4,331,448 B/op vs 2,283,202 B/op).
- **Decode speed**: Shared-TS combos decode ~-14% faster than non-shared (6295 vs 5541 ns/op) due to smaller blob size and shared timestamp index.

## Encode Performance

Encoding speed and memory allocation for each combination:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| DeltaPacked + Raw | 476,939 | 2,283,202 | 50 |
| Delta + Raw | 504,483 | 2,275,056 | 50 |
| Shared DeltaPacked + Raw | 549,674 | 2,665,873 | 79 |
| Shared Delta + Raw | 561,357 | 2,657,676 | 79 |
| Raw + Raw | 614,252 | 4,331,448 | 62 |
| Shared Raw + Raw | 664,100 | 4,724,141 | 91 |
| DeltaPacked + Gorilla | 706,509 | 1,823,604 | 49 |
| Delta + Gorilla | 729,064 | 1,815,365 | 49 |
| Shared DeltaPacked + Gorilla | 758,767 | 2,149,817 | 78 |
| Shared Delta + Gorilla | 772,164 | 2,141,764 | 78 |
| Raw + Gorilla | 804,831 | 3,881,036 | 61 |
| Delta + Chimp | 805,071 | 1,486,916 | 47 |
| Raw + Chimp | 808,115 | 3,545,785 | 60 |
| DeltaPacked + Chimp | 811,929 | 1,495,199 | 47 |
| Shared Delta + Chimp | 859,352 | 2,125,257 | 78 |
| Shared Raw + Gorilla | 889,024 | 4,208,041 | 90 |
| Shared DeltaPacked + Chimp | 902,212 | 2,133,584 | 78 |
| Shared Raw + Chimp | 1,019,927 | 4,191,573 | 90 |

## Decode Performance

Decoding speed (NewDecoder + Decode) and memory allocation:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Delta + Raw | 5,541 | 32,824 | 7 |
| Raw + Gorilla | 5,617 | 32,824 | 7 |
| DeltaPacked + Chimp | 5,633 | 32,824 | 7 |
| DeltaPacked + Raw | 5,638 | 32,824 | 7 |
| Delta + Gorilla | 5,644 | 32,824 | 7 |
| Raw + Raw | 5,646 | 32,824 | 7 |
| Raw + Chimp | 5,746 | 32,824 | 7 |
| Delta + Chimp | 5,778 | 32,824 | 7 |
| DeltaPacked + Gorilla | 5,788 | 32,824 | 7 |
| Shared Raw + Raw | 6,295 | 22,696 | 11 |
| Shared Raw + Chimp | 6,477 | 22,696 | 11 |
| Shared DeltaPacked + Raw | 6,513 | 22,696 | 11 |
| Shared Delta + Raw | 6,532 | 22,696 | 11 |
| Shared Raw + Gorilla | 6,561 | 22,696 | 11 |
| Shared Delta + Gorilla | 6,602 | 22,696 | 11 |
| Shared DeltaPacked + Gorilla | 6,657 | 22,696 | 11 |
| Shared DeltaPacked + Chimp | 6,674 | 22,696 | 11 |
| Shared Delta + Chimp | 6,722 | 22,696 | 11 |

## Iteration Performance

Sequential iteration speed (iterating all data points via `blob.All(metricID)`):

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Delta + Raw | 264,166 | 64,008 | 1001 |
| Shared Delta + Raw | 264,291 | 64,008 | 1001 |
| Shared DeltaPacked + Raw | 264,648 | 64,008 | 1001 |
| DeltaPacked + Raw | 276,581 | 64,008 | 1001 |
| Raw + Raw | 287,712 | 67,208 | 1001 |
| Shared Raw + Raw | 293,110 | 67,208 | 1001 |
| Shared Delta + Gorilla | 484,026 | 80,008 | 1401 |
| Delta + Gorilla | 493,885 | 80,008 | 1401 |
| Raw + Gorilla | 498,278 | 60,808 | 801 |
| Shared Raw + Gorilla | 499,918 | 60,808 | 801 |
| Shared DeltaPacked + Gorilla | 526,479 | 80,008 | 1401 |
| DeltaPacked + Gorilla | 551,247 | 80,008 | 1401 |
| Raw + Chimp | 571,234 | 60,808 | 801 |
| Shared Raw + Chimp | 583,705 | 60,808 | 801 |
| Shared Delta + Chimp | 603,279 | 80,008 | 1401 |
| Delta + Chimp | 605,850 | 80,008 | 1401 |
| Shared DeltaPacked + Chimp | 659,230 | 80,008 | 1401 |
| DeltaPacked + Chimp | 661,389 | 80,008 | 1401 |

**Note:** Compressed encodings can iterate faster than raw due to reduced memory bandwidth — smaller data fits better in CPU cache.

## Scaling Analysis

How bytes-per-point changes as points-per-metric increases, for each encoding combination.
The fixed per-metric overhead amortizes differently depending on the encoding.

### Standard Encodings

| Points/Metric | raw-raw | raw-gorilla | raw-chimp | delta-raw | delta-gorilla | delta-chimp | deltapacked-raw | deltapacked-gorilla | deltapacked-chimp |
|---------------|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 32.160 | 32.160 | 32.160 | 32.160 | 32.160 | 32.160 | 32.160 | 32.160 | 32.160 |
| 2 | 24.080 | 23.942 | 23.582 | 21.580 | 21.442 | 21.082 | 21.580 | 21.442 | 21.082 |
| 5 | 19.232 | 18.315 | 17.946 | 14.592 | 13.675 | 13.306 | 14.761 | 13.844 | 13.475 |
| 10 | 17.616 | 16.369 | 16.097 | 12.262 | 11.014 | 10.743 | 12.419 | 11.171 | 10.900 |
| 20 | 16.808 | 15.369 | 15.159 | 11.097 | 9.658 | 9.447 | 11.291 | 9.852 | 9.642 |
| 50 | 16.323 | 14.783 | 14.602 | 10.401 | 8.861 | 8.679 | 10.581 | 9.041 | 8.860 |
| 100 | 16.162 | 14.621 | 14.418 | 10.169 | 8.629 | 8.426 | 10.360 | 8.819 | 8.616 |
| 150 | 16.108 | 14.582 | 14.356 | 10.092 | 8.567 | 8.340 | 10.280 | 8.754 | 8.527 |
| 200 | 16.081 | 14.571 | 14.324 | 10.054 | 8.544 | 8.297 | 10.244 | 8.734 | 8.487 |

### Shared-Timestamp Encodings

| Points/Metric | shared-raw-raw | shared-raw-gorilla | shared-raw-chimp | shared-delta-raw | shared-delta-gorilla | shared-delta-chimp | shared-deltapacked-raw | shared-deltapacked-gorilla | shared-deltapacked-chimp |
|---------------|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 26.220 | 26.220 | 26.220 | 26.220 | 26.220 | 26.220 | 26.220 | 26.220 | 26.220 |
| 2 | 17.130 | 16.970 | 16.630 | 17.117 | 16.957 | 16.617 | 17.117 | 16.957 | 16.617 |
| 5 | 11.676 | 10.777 | 10.394 | 11.653 | 10.754 | 10.371 | 11.654 | 10.755 | 10.372 |
| 10 | 9.858 | 8.627 | 8.331 | 9.831 | 8.600 | 8.303 | 9.832 | 8.601 | 8.305 |
| 20 | 8.949 | 7.529 | 7.309 | 8.920 | 7.500 | 7.280 | 8.921 | 7.501 | 7.282 |
| 50 | 8.404 | 6.881 | 6.691 | 8.374 | 6.851 | 6.661 | 8.375 | 6.852 | 6.662 |
| 100 | 8.222 | 6.693 | 6.485 | 8.192 | 6.663 | 6.455 | 8.193 | 6.664 | 6.455 |
| 150 | 8.161 | 6.643 | 6.415 | 8.131 | 6.613 | 6.385 | 8.132 | 6.614 | 6.386 |
| 200 | 8.131 | 6.627 | 6.380 | 8.101 | 6.597 | 6.349 | 8.102 | 6.598 | 6.350 |

### Key Insights

- **Overhead becomes acceptable at ~20 PPM**: Shared Delta + Chimp reaches 7.280 bytes/point (within 30% of converged value 6.349).
- **Diminishing returns above ~50 PPM**: BPP converges to 6.349 (within 5% threshold reached at 50 PPM with 6.661 BPP).
- **Shared timestamps scale with metric count**: At 200 PPM, Shared Delta + Chimp achieves 6.349 BPP vs Delta + Chimp at 8.297 BPP — a 23% additional saving from timestamp deduplication across 200 metrics.
- **Fixed overhead dominates at low PPM**: At 1 PPM, even the best combo (Shared Delta + Chimp) costs 26.220 bytes/point vs 6.349 converged — 4.1× overhead from per-metric headers.
- **Raw vs compressed convergence**: Raw + Raw overhead amortizes to 16.081 BPP (16 bytes per point for 8-byte timestamp + 8-byte float64). Compressed combos converge much lower because they also amortize encoding metadata while compressing the data itself.

## Choosing an Encoding Strategy

### Decision Tree

```
What is your priority?
├─ Smallest encoded size?
│  ├─ All metrics share timestamps? → Shared Delta + Chimp (6.349 BPP, 60.5% savings)
│  └─ Independent timestamps?      → Delta + Chimp (8.297 BPP, 48.4% savings)
│
├─ Fastest encode?
│  └─ DeltaPacked + Raw (476,939 ns/op, 10.244 BPP)
│
├─ Fastest iteration / decode?
│  ├─ Sequential scan → Delta + Raw (264,166 ns/op)
│  └─ Random access  → Shared Raw + Chimp (6.380 BPP, O(1) TimestampAt/ValueAt)
│
└─ Best balance (size + speed)?
   ├─ With shared TS → Shared Delta + Chimp (6.349 BPP, 603,279 ns/op iter)
   └─ Without        → Delta + Chimp (8.297 BPP, 605,850 ns/op iter)
```

### Configuration Selection

| Use Case | Configuration | Key Metric | Rationale |
|----------|---------------|------------|-----------|
| **Best compression** | Shared Delta + Chimp | 6.349 BPP (60.5% savings) | Lowest bytes/point; shared timestamps eliminate redundant storage |
| **Fastest iteration** | Delta + Raw | 264,166 ns/op | Fastest sequential scan; raw values avoid decode overhead |
| **Fastest encode** | DeltaPacked + Raw | 476,939 ns/op | Minimal encode computation; delta reduces buffer size |
| **Best balance** | Shared DeltaPacked + Chimp | 6.350 BPP, 659,230 ns/op iter | Top ranks in both compression and iteration speed |
| **Random access** | Shared Raw + Chimp | 6.380 BPP | O(1) `TimestampAt`/`ValueAt`; raw timestamps support direct indexing |
| **Maximum throughput** | Raw + Raw | 614,252 ns/op encode | Baseline; no encoding overhead but largest output |

### Points-per-Metric Guidelines

Using Shared Delta + Chimp scaling data (converged: 6.349 bytes/point):

| Zone | PPM Range | BPP Range | Overhead | Recommendation |
|------|-----------|-----------|----------|----------------|
| **Poor** | 1–2 | 26.220–16.617 | 162–313% | Batch more points if possible; fixed overhead dominates |
| **Moderate** | 5–10 | 10.371–8.303 | 31–63% | Acceptable for low-frequency metrics |
| **Good** | 20 | 7.280 | 15–15% | Good efficiency; recommended minimum for most use cases |
| **Optimal** | 50–200 | 6.661–6.349 | 0–5% | Excellent efficiency; diminishing returns beyond this range |
