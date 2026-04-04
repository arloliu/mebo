# Performance Guide

> **Auto-generated** by the `update-performance-report` agent skill from benchmark data.
> To regenerate: run `tests/measurev2/` and use the skill.

| | |
|---|---|
| **Benchmark Date** | 2026-04-04 |
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
| **Best Compression** | 6.35 bytes/point (60.5% savings) | Shared Delta + Chimp |
| **Best Balance** | 6.35 bytes/point (60.5% savings) | Shared DeltaPacked + Chimp |
| **Fastest Encode** | 485,698 ns/op | DeltaPacked + Raw |
| **Baseline** | 16.08 bytes/point | Raw + Raw |

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
| Shared Delta + Chimp | 6.35 | 60.5% | 2.53× | 828,570 | 3,580 | 599,745 |
| Shared DeltaPacked + Chimp | 6.35 | 60.5% | 2.53× | 858,745 | 3,603 | 657,169 |
| Shared Raw + Chimp | 6.38 | 60.3% | 2.52× | 1,016,351 | 3,776 | 573,366 |
| Shared Delta + Gorilla | 6.60 | 59.0% | 2.44× | 756,586 | 3,650 | 491,302 |
| Shared DeltaPacked + Gorilla | 6.60 | 59.0% | 2.44× | 744,055 | 3,653 | 524,261 |
| Shared Raw + Gorilla | 6.63 | 58.8% | 2.43× | 903,312 | 3,574 | 496,375 |
| Shared Delta + Raw | 8.10 | 49.6% | 1.99× | 529,410 | 3,581 | 267,518 |
| Shared DeltaPacked + Raw | 8.10 | 49.6% | 1.98× | 550,688 | 3,664 | 267,274 |
| Shared Raw + Raw | 8.13 | 49.4% | 1.98× | 691,464 | 3,621 | 294,812 |
| Delta + Chimp | 8.30 | 48.4% | 1.94× | 777,413 | 5,590 | 601,819 |
| DeltaPacked + Chimp | 8.49 | 47.2% | 1.89× | 834,520 | 5,683 | 661,719 |
| Delta + Gorilla | 8.54 | 46.9% | 1.88× | 720,336 | 5,660 | 491,926 |
| DeltaPacked + Gorilla | 8.73 | 45.7% | 1.84× | 735,070 | 5,829 | 548,611 |
| Delta + Raw | 10.05 | 37.5% | 1.60× | 492,588 | 5,823 | 265,970 |
| DeltaPacked + Raw | 10.24 | 36.3% | 1.57× | 485,698 | 5,702 | 275,557 |
| Raw + Chimp | 14.32 | 10.9% | 1.12× | 838,255 | 5,501 | 573,953 |
| Raw + Gorilla | 14.57 | 9.4% | 1.10× | 807,659 | 5,558 | 499,479 |
| Raw + Raw | 16.08 | 0.0% | 1.00× | 623,091 | 5,441 | 290,203 |

### Key Observations

- **Best compression**: Shared Delta + Chimp achieves 6.35 bytes/point (60.5% savings vs raw-raw baseline). Shared timestamp deduplication eliminates redundant timestamp storage across 200 metrics.
- **Shared timestamps**: Enabling `WithSharedTimestamps()` provides 23% additional savings over the best non-shared configuration (Delta + Chimp at 8.30 bytes/point). The savings come from storing the timestamp column once instead of 200 times.
- **Chimp vs Gorilla**: Chimp consistently outperforms Gorilla by ~2.9% in compression. For example, Delta + Chimp (8.30 BPP) vs Delta + Gorilla (8.54 BPP). Both use XOR-based floating-point encoding.
- **DeltaPacked vs Delta**: DeltaPacked shows ~2.3% larger encoded size than Delta (8.49 vs 8.30 BPP). DeltaPacked's advantage is **decode/iteration speed** via Group Varint batch decoding, not compression ratio.
- **Encode speed tradeoff**: DeltaPacked + Raw encodes fastest at 485698 ns/op. Raw + Raw baseline (623091 ns/op) is not the fastest because larger raw data requires more memory allocation (4,331,523 B/op vs 2,283,184 B/op).
- **Decode speed**: Shared-TS combos decode ~34% faster than non-shared (3574 vs 5441 ns/op) due to smaller blob size and shared timestamp index.

## Encode Performance

Encoding speed and memory allocation for each combination:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| DeltaPacked + Raw | 485,698 | 2,283,184 | 50 |
| Delta + Raw | 492,588 | 2,275,077 | 50 |
| Shared Delta + Raw | 529,410 | 2,657,668 | 79 |
| Shared DeltaPacked + Raw | 550,688 | 2,665,760 | 79 |
| Raw + Raw | 623,091 | 4,331,523 | 62 |
| Shared Raw + Raw | 691,464 | 4,724,084 | 91 |
| Delta + Gorilla | 720,336 | 1,815,580 | 49 |
| DeltaPacked + Gorilla | 735,070 | 1,823,819 | 49 |
| Shared DeltaPacked + Gorilla | 744,055 | 2,149,814 | 78 |
| Shared Delta + Gorilla | 756,586 | 2,141,789 | 78 |
| Delta + Chimp | 777,413 | 1,486,910 | 47 |
| Raw + Gorilla | 807,659 | 3,880,968 | 61 |
| Shared Delta + Chimp | 828,570 | 2,125,543 | 78 |
| DeltaPacked + Chimp | 834,520 | 1,495,107 | 47 |
| Raw + Chimp | 838,255 | 3,545,654 | 60 |
| Shared DeltaPacked + Chimp | 858,745 | 2,133,801 | 78 |
| Shared Raw + Gorilla | 903,312 | 4,208,053 | 90 |
| Shared Raw + Chimp | 1,016,351 | 4,191,694 | 90 |

## Decode Performance

Decoding speed (NewDecoder + Decode) and memory allocation:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Shared Raw + Gorilla | 3,574 | 15,664 | 5 |
| Shared Delta + Chimp | 3,580 | 15,664 | 5 |
| Shared Delta + Raw | 3,581 | 15,664 | 5 |
| Shared DeltaPacked + Chimp | 3,603 | 15,664 | 5 |
| Shared Raw + Raw | 3,621 | 15,664 | 5 |
| Shared Delta + Gorilla | 3,650 | 15,664 | 5 |
| Shared DeltaPacked + Gorilla | 3,653 | 15,664 | 5 |
| Shared DeltaPacked + Raw | 3,664 | 15,664 | 5 |
| Shared Raw + Chimp | 3,776 | 15,664 | 5 |
| Raw + Raw | 5,441 | 32,824 | 7 |
| Raw + Chimp | 5,501 | 32,824 | 7 |
| Raw + Gorilla | 5,558 | 32,824 | 7 |
| Delta + Chimp | 5,590 | 32,824 | 7 |
| Delta + Gorilla | 5,660 | 32,824 | 7 |
| DeltaPacked + Chimp | 5,683 | 32,824 | 7 |
| DeltaPacked + Raw | 5,702 | 32,824 | 7 |
| Delta + Raw | 5,823 | 32,824 | 7 |
| DeltaPacked + Gorilla | 5,829 | 32,824 | 7 |

## Iteration Performance

Sequential iteration speed (iterating all data points via `blob.All(metricID)`):

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Delta + Raw | 265,970 | 64,008 | 1001 |
| Shared DeltaPacked + Raw | 267,274 | 64,008 | 1001 |
| Shared Delta + Raw | 267,518 | 64,008 | 1001 |
| DeltaPacked + Raw | 275,557 | 64,008 | 1001 |
| Raw + Raw | 290,203 | 67,208 | 1001 |
| Shared Raw + Raw | 294,812 | 67,208 | 1001 |
| Shared Delta + Gorilla | 491,302 | 80,008 | 1401 |
| Delta + Gorilla | 491,926 | 80,008 | 1401 |
| Shared Raw + Gorilla | 496,375 | 60,808 | 801 |
| Raw + Gorilla | 499,479 | 60,808 | 801 |
| Shared DeltaPacked + Gorilla | 524,261 | 80,008 | 1401 |
| DeltaPacked + Gorilla | 548,611 | 80,008 | 1401 |
| Shared Raw + Chimp | 573,366 | 60,808 | 801 |
| Raw + Chimp | 573,953 | 60,808 | 801 |
| Shared Delta + Chimp | 599,745 | 80,008 | 1401 |
| Delta + Chimp | 601,819 | 80,008 | 1401 |
| Shared DeltaPacked + Chimp | 657,169 | 80,008 | 1401 |
| DeltaPacked + Chimp | 661,719 | 80,008 | 1401 |

**Note:** Compressed encodings can iterate faster than raw due to reduced memory bandwidth — smaller data fits better in CPU cache.

## Scaling Analysis

How bytes-per-point changes as points-per-metric increases, for each encoding combination.
The fixed per-metric overhead amortizes differently depending on the encoding.

### Standard Encodings

| Points/Metric | raw-raw | raw-gorilla | raw-chimp | delta-raw | delta-gorilla | delta-chimp | deltapacked-raw | deltapacked-gorilla | deltapacked-chimp |
|---------------|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 32.16 | 32.16 | 32.16 | 32.16 | 32.16 | 32.16 | 32.16 | 32.16 | 32.16 |
| 2 | 24.08 | 23.94 | 23.58 | 21.58 | 21.44 | 21.08 | 21.58 | 21.44 | 21.08 |
| 5 | 19.23 | 18.32 | 17.95 | 14.59 | 13.68 | 13.31 | 14.76 | 13.84 | 13.47 |
| 10 | 17.62 | 16.37 | 16.10 | 12.26 | 11.01 | 10.74 | 12.42 | 11.17 | 10.90 |
| 20 | 16.81 | 15.37 | 15.16 | 11.10 | 9.66 | 9.45 | 11.29 | 9.85 | 9.64 |
| 50 | 16.32 | 14.78 | 14.60 | 10.40 | 8.86 | 8.68 | 10.58 | 9.04 | 8.86 |
| 100 | 16.16 | 14.62 | 14.42 | 10.17 | 8.63 | 8.43 | 10.36 | 8.82 | 8.62 |
| 150 | 16.11 | 14.58 | 14.36 | 10.09 | 8.57 | 8.34 | 10.28 | 8.75 | 8.53 |
| 200 | 16.08 | 14.57 | 14.32 | 10.05 | 8.54 | 8.30 | 10.24 | 8.73 | 8.49 |

### Shared-Timestamp Encodings

| Points/Metric | shared-raw-raw | shared-raw-gorilla | shared-raw-chimp | shared-delta-raw | shared-delta-gorilla | shared-delta-chimp | shared-deltapacked-raw | shared-deltapacked-gorilla | shared-deltapacked-chimp |
|---------------|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 26.22 | 26.22 | 26.22 | 26.22 | 26.22 | 26.22 | 26.22 | 26.22 | 26.22 |
| 2 | 17.13 | 16.97 | 16.63 | 17.12 | 16.96 | 16.62 | 17.12 | 16.96 | 16.62 |
| 5 | 11.68 | 10.78 | 10.39 | 11.65 | 10.75 | 10.37 | 11.65 | 10.76 | 10.37 |
| 10 | 9.86 | 8.63 | 8.33 | 9.83 | 8.60 | 8.30 | 9.83 | 8.60 | 8.30 |
| 20 | 8.95 | 7.53 | 7.31 | 8.92 | 7.50 | 7.28 | 8.92 | 7.50 | 7.28 |
| 50 | 8.40 | 6.88 | 6.69 | 8.37 | 6.85 | 6.66 | 8.37 | 6.85 | 6.66 |
| 100 | 8.22 | 6.69 | 6.48 | 8.19 | 6.66 | 6.45 | 8.19 | 6.66 | 6.46 |
| 150 | 8.16 | 6.64 | 6.41 | 8.13 | 6.61 | 6.38 | 8.13 | 6.61 | 6.39 |
| 200 | 8.13 | 6.63 | 6.38 | 8.10 | 6.60 | 6.35 | 8.10 | 6.60 | 6.35 |

### Key Insights

- **Overhead becomes acceptable at ~20 PPM**: Shared Delta + Chimp reaches 7.28 bytes/point (within 30% of converged value 6.35).
- **Diminishing returns above ~50 PPM**: BPP converges to 6.35 (within 5% threshold reached at 50 PPM with 6.66 BPP).
- **Shared timestamps scale with metric count**: At 200 PPM, Shared Delta + Chimp achieves 6.35 BPP vs Delta + Chimp at 8.30 BPP — a 23% additional saving from timestamp deduplication across 200 metrics.
- **Fixed overhead dominates at low PPM**: At 1 PPM, even the best combo (Shared Delta + Chimp) costs 26.22 bytes/point vs 6.35 converged — 4.1× overhead from per-metric headers.
- **Raw vs compressed convergence**: Raw + Raw overhead amortizes to 16.08 BPP (16 bytes per point for 8-byte timestamp + 8-byte float64). Compressed combos converge much lower because they also amortize encoding metadata while compressing the data itself.

## Choosing an Encoding Strategy

### Decision Tree

```
What is your priority?
├─ Smallest encoded size?
│  ├─ All metrics share timestamps? → Shared Delta + Chimp (6.35 BPP, 60.5% savings)
│  └─ Independent timestamps?      → Delta + Chimp (8.30 BPP, 48.4% savings)
│
├─ Fastest encode?
│  └─ DeltaPacked + Raw (485,698 ns/op, 10.24 BPP)
│
├─ Fastest iteration / decode?
│  ├─ Sequential scan → Delta + Raw (265,970 ns/op)
│  └─ Random access  → Shared Raw + Chimp (6.38 BPP, O(1) TimestampAt/ValueAt)
│
└─ Best balance (size + speed)?
   ├─ With shared TS → Shared Delta + Chimp (6.35 BPP, 599,745 ns/op iter)
   └─ Without        → Delta + Chimp (8.30 BPP, 601,819 ns/op iter)
```

### Configuration Selection

| Use Case | Configuration | Key Metric | Rationale |
|----------|---------------|------------|-----------|
| **Best compression** | Shared Delta + Chimp | 6.35 BPP (60.5% savings) | Lowest bytes/point; shared timestamps eliminate redundant storage |
| **Fastest iteration** | Delta + Raw | 265,970 ns/op | Fastest sequential scan; raw values avoid decode overhead |
| **Fastest encode** | DeltaPacked + Raw | 485,698 ns/op | Minimal encode computation; delta reduces buffer size |
| **Best balance** | Shared DeltaPacked + Chimp | 6.35 BPP, 657,169 ns/op iter | Top ranks in both compression and iteration speed |
| **Random access** | Shared Raw + Chimp | 6.38 BPP | O(1) `TimestampAt`/`ValueAt`; raw timestamps support direct indexing |
| **Maximum throughput** | Raw + Raw | 623,091 ns/op encode | Baseline; no encoding overhead but largest output |

### Points-per-Metric Guidelines

Using Shared Delta + Chimp scaling data (converged: 6.35 bytes/point):

| Zone | PPM Range | BPP Range | Overhead | Recommendation |
|------|-----------|-----------|----------|----------------|
| **Poor** | 1–2 | 26.22–16.62 | 162–313% | Batch more points if possible; fixed overhead dominates |
| **Moderate** | 5–10 | 10.37–8.30 | 31–63% | Acceptable for low-frequency metrics |
| **Good** | 20 | 7.28 | 15–15% | Good efficiency; recommended minimum for most use cases |
| **Optimal** | 50–200 | 6.66–6.35 | 0–5% | Excellent efficiency; diminishing returns beyond this range |
