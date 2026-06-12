# Performance Guide

> **Auto-generated** by the `update-performance-report` agent skill from benchmark data.
> To regenerate: run `tests/measurev2/` and use the skill.

| | |
|---|---|
| **Benchmark Date** | 2026-06-13 |
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
- [Scaling Analysis](#scaling-analysis)
- [Choosing an Encoding Strategy](#choosing-an-encoding-strategy)

## Quick Reference

**TL;DR — Recommended Configurations:**

| Metric | Value | Configuration |
|--------|-------|---------------|
| **Best Compression** | 6.349 bytes/point (60.5% savings) | Shared Delta + Chimp |
| **Best Balance** | 6.350 bytes/point (60.5% savings) | Shared DeltaPacked + Chimp |
| **Fastest Encode** | 311,757 ns/op | Raw + Raw |
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

All 18 valid encoding combinations (9 standard timestamp × value + 9 with shared timestamps), benchmarked without additional compression codecs.
Shared-timestamp combos use `WithSharedTimestamps()` to deduplicate identical timestamp sequences across metrics.

Sorted by encoded size (most efficient first):

| Configuration | Bytes/Point | Space Savings | vs Raw | Encode (ns/op) | Decode (ns/op) | Iterate (ns/op) |
|---------------|-------------|---------------|--------|----------------|----------------|-----------------|
| Shared Delta + Chimp | 6.349 | 60.5% | 2.533× | 587,175 | 6,291 | 374,705 |
| Shared DeltaPacked + Chimp | 6.350 | 60.5% | 2.532× | 631,675 | 6,349 | 438,578 |
| Shared Raw + Chimp | 6.380 | 60.3% | 2.521× | 605,937 | 6,135 | 422,355 |
| Shared Delta + Gorilla | 6.597 | 59.0% | 2.438× | 479,282 | 6,399 | 271,042 |
| Shared DeltaPacked + Gorilla | 6.598 | 59.0% | 2.437× | 494,732 | 6,256 | 327,111 |
| Shared Raw + Gorilla | 6.627 | 58.8% | 2.427× | 478,990 | 6,260 | 306,949 |
| Shared Delta + Raw | 8.101 | 49.6% | 1.985× | 351,912 | 6,229 | 266,850 |
| Shared DeltaPacked + Raw | 8.102 | 49.6% | 1.985× | 378,584 | 6,241 | 215,342 |
| Shared Raw + Raw | 8.131 | 49.4% | 1.978× | 339,625 | 6,225 | 284,482 |
| Delta + Chimp | 8.297 | 48.4% | 1.938× | 553,944 | 5,583 | 369,526 |
| DeltaPacked + Chimp | 8.487 | 47.2% | 1.895× | 587,425 | 5,554 | 434,248 |
| Delta + Gorilla | 8.544 | 46.9% | 1.882× | 438,013 | 5,523 | 271,036 |
| DeltaPacked + Gorilla | 8.734 | 45.7% | 1.841× | 466,154 | 5,520 | 333,347 |
| Delta + Raw | 10.054 | 37.5% | 1.599× | 328,954 | 5,612 | 273,011 |
| DeltaPacked + Raw | 10.244 | 36.3% | 1.570× | 344,399 | 5,545 | 212,790 |
| Raw + Chimp | 14.324 | 10.9% | 1.123× | 545,619 | 5,621 | 422,408 |
| Raw + Gorilla | 14.571 | 9.4% | 1.104× | 430,182 | 5,632 | 305,896 |
| Raw + Raw | 16.081 | 0.0% | 1.000× | 311,757 | 5,453 | 284,213 |

### Key Observations

- **Best compression**: Shared Delta + Chimp achieves 6.349 bytes/point (60.5% savings vs raw-raw baseline). Shared timestamp deduplication eliminates redundant timestamp storage across 200 metrics.
- **Shared timestamps**: Enabling `WithSharedTimestamps()` provides 23% additional savings over the best non-shared configuration (Delta + Chimp at 8.297 bytes/point). The savings come from storing the timestamp column once instead of 200 times.
- **Chimp vs Gorilla**: Chimp consistently outperforms Gorilla by ~2.9% in compression. For example, Delta + Chimp (8.297 BPP) vs Delta + Gorilla (8.544 BPP). Both use XOR-based floating-point encoding.
- **DeltaPacked vs Delta**: DeltaPacked shows ~2.3% larger encoded size than Delta (8.487 vs 8.297 BPP). DeltaPacked's advantage is **decode/iteration speed** via Group Varint batch decoding, not compression ratio.
- **Encode speed tradeoff**: Raw + Raw encodes fastest at 311757 ns/op. Raw + Raw baseline (311757 ns/op) is not the fastest because larger raw data requires more memory allocation (698,410 B/op vs 698,410 B/op).
- **Decode speed**: Shared-TS combos decode ~-13% faster than non-shared (6135 vs 5453 ns/op) due to smaller blob size and shared timestamp index.

## Encode Performance

Encoding speed and memory allocation for each combination:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Raw + Raw | 311,757 | 698,410 | 34 |
| Delta + Raw | 328,954 | 458,487 | 34 |
| Shared Raw + Raw | 339,625 | 1,083,501 | 64 |
| DeltaPacked + Raw | 344,399 | 466,149 | 34 |
| Shared Delta + Raw | 351,912 | 840,655 | 64 |
| Shared DeltaPacked + Raw | 378,584 | 850,073 | 64 |
| Raw + Gorilla | 430,182 | 638,524 | 34 |
| Delta + Gorilla | 438,013 | 389,317 | 34 |
| DeltaPacked + Gorilla | 466,154 | 398,016 | 34 |
| Shared Raw + Gorilla | 478,990 | 962,253 | 64 |
| Shared Delta + Gorilla | 479,282 | 716,731 | 64 |
| Shared DeltaPacked + Gorilla | 494,732 | 725,900 | 63 |
| Raw + Chimp | 545,619 | 622,030 | 34 |
| Delta + Chimp | 553,944 | 382,777 | 34 |
| Shared Delta + Chimp | 587,175 | 699,904 | 64 |
| DeltaPacked + Chimp | 587,425 | 391,351 | 34 |
| Shared Raw + Chimp | 605,937 | 947,068 | 64 |
| Shared DeltaPacked + Chimp | 631,675 | 710,124 | 64 |

## Decode Performance

Decoding speed (NewDecoder + Decode) and memory allocation:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Raw + Raw | 5,453 | 32,824 | 7 |
| DeltaPacked + Gorilla | 5,520 | 32,824 | 7 |
| Delta + Gorilla | 5,523 | 32,824 | 7 |
| DeltaPacked + Raw | 5,545 | 32,824 | 7 |
| DeltaPacked + Chimp | 5,554 | 32,824 | 7 |
| Delta + Chimp | 5,583 | 32,824 | 7 |
| Delta + Raw | 5,612 | 32,824 | 7 |
| Raw + Chimp | 5,621 | 32,824 | 7 |
| Raw + Gorilla | 5,632 | 32,824 | 7 |
| Shared Raw + Chimp | 6,135 | 22,696 | 11 |
| Shared Raw + Raw | 6,225 | 22,696 | 11 |
| Shared Delta + Raw | 6,229 | 22,696 | 11 |
| Shared DeltaPacked + Raw | 6,241 | 22,696 | 11 |
| Shared DeltaPacked + Gorilla | 6,256 | 22,696 | 11 |
| Shared Raw + Gorilla | 6,260 | 22,696 | 11 |
| Shared Delta + Chimp | 6,291 | 22,696 | 11 |
| Shared DeltaPacked + Chimp | 6,349 | 22,696 | 11 |
| Shared Delta + Gorilla | 6,399 | 22,696 | 11 |

## Iteration Performance

Sequential iteration speed (iterating all data points via `blob.All(metricID)`):

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| DeltaPacked + Raw | 212,790 | 25,608 | 801 |
| Shared DeltaPacked + Raw | 215,342 | 25,608 | 801 |
| Shared Delta + Raw | 266,850 | 25,608 | 801 |
| Delta + Gorilla | 271,036 | 19,208 | 601 |
| Shared Delta + Gorilla | 271,042 | 19,208 | 601 |
| Delta + Raw | 273,011 | 25,608 | 801 |
| Raw + Raw | 284,213 | 28,808 | 801 |
| Shared Raw + Raw | 284,482 | 28,808 | 801 |
| Raw + Gorilla | 305,896 | 22,408 | 601 |
| Shared Raw + Gorilla | 306,949 | 22,408 | 601 |
| Shared DeltaPacked + Gorilla | 327,111 | 19,208 | 601 |
| DeltaPacked + Gorilla | 333,347 | 19,208 | 601 |
| Delta + Chimp | 369,526 | 19,208 | 601 |
| Shared Delta + Chimp | 374,705 | 19,208 | 601 |
| Shared Raw + Chimp | 422,355 | 22,408 | 601 |
| Raw + Chimp | 422,408 | 22,408 | 601 |
| DeltaPacked + Chimp | 434,248 | 19,208 | 601 |
| Shared DeltaPacked + Chimp | 438,578 | 19,208 | 601 |

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
│  └─ Raw + Raw (311,757 ns/op, 16.081 BPP)
│
├─ Fastest iteration / decode?
│  ├─ Sequential scan → DeltaPacked + Raw (212,790 ns/op)
│  └─ Random access  → Shared Raw + Chimp (6.380 BPP, O(1) TimestampAt/ValueAt)
│
└─ Best balance (size + speed)?
   ├─ With shared TS → Shared Delta + Chimp (6.349 BPP, 374,705 ns/op iter)
   └─ Without        → Delta + Chimp (8.297 BPP, 369,526 ns/op iter)
```

### Configuration Selection

| Use Case | Configuration | Key Metric | Rationale |
|----------|---------------|------------|-----------|
| **Best compression** | Shared Delta + Chimp | 6.349 BPP (60.5% savings) | Lowest bytes/point; shared timestamps eliminate redundant storage |
| **Fastest iteration** | DeltaPacked + Raw | 212,790 ns/op | Fastest sequential scan; raw values avoid decode overhead |
| **Fastest encode** | Raw + Raw | 311,757 ns/op | Minimal encode computation; delta reduces buffer size |
| **Best balance** | Shared Delta + Gorilla | 6.597 BPP, 271,042 ns/op iter | Top ranks in both compression and iteration speed |
| **Random access** | Shared Raw + Chimp | 6.380 BPP | O(1) `TimestampAt`/`ValueAt`; raw timestamps support direct indexing |
| **Maximum throughput** | Raw + Raw | 311,757 ns/op encode | Baseline; no encoding overhead but largest output |

### Points-per-Metric Guidelines

Using Shared Delta + Chimp scaling data (converged: 6.349 bytes/point):

| Zone | PPM Range | BPP Range | Overhead | Recommendation |
|------|-----------|-----------|----------|----------------|
| **Poor** | 1–2 | 26.220–16.617 | 162–313% | Batch more points if possible; fixed overhead dominates |
| **Moderate** | 5–10 | 10.371–8.303 | 31–63% | Acceptable for low-frequency metrics |
| **Good** | 20 | 7.280 | 15–15% | Good efficiency; recommended minimum for most use cases |
| **Optimal** | 50–200 | 6.661–6.349 | 0–5% | Excellent efficiency; diminishing returns beyond this range |
