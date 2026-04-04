# Performance Guide

> **Auto-generated** by the `update-performance-report` agent skill from benchmark data.
> To regenerate: run `tests/measurev2/` and use the skill.

| | |
|---|---|
| **Benchmark Date** | 2026-04-04T17:09:51+08:00 |
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
| **Best Compression** | 8.30 bytes/point (48.4% savings) | Delta + Chimp |
| **Fastest Iteration** | 264,411 ns/op | Delta + Raw |
| **Fastest Encode** | 508,519 ns/op | DeltaPacked + Raw |
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

All 9 valid timestamp × value encoding combinations, benchmarked without additional compression codecs.

Sorted by encoded size (most efficient first):

| Configuration | Bytes/Point | Space Savings | vs Raw | Encode (ns/op) | Decode (ns/op) | Iterate (ns/op) |
|---------------|-------------|---------------|--------|----------------|----------------|-----------------|
| Delta + Chimp | 8.30 | 48.4% | 1.94× | 793,987 | 5,692 | 602,502 |
| DeltaPacked + Chimp | 8.49 | 47.2% | 1.89× | 874,352 | 6,007 | 654,361 |
| Delta + Gorilla | 8.54 | 46.9% | 1.88× | 727,205 | 5,718 | 491,596 |
| DeltaPacked + Gorilla | 8.73 | 45.7% | 1.84× | 717,166 | 5,827 | 529,538 |
| Delta + Raw | 10.05 | 37.5% | 1.60× | 541,484 | 5,884 | 264,411 |
| DeltaPacked + Raw | 10.24 | 36.3% | 1.57× | 508,519 | 5,957 | 277,038 |
| Raw + Chimp | 14.32 | 10.9% | 1.12× | 967,214 | 6,138 | 574,823 |
| Raw + Gorilla | 14.57 | 9.4% | 1.10× | 835,219 | 5,994 | 551,422 |
| Raw + Raw | 16.08 | 0% | 1.00× | 658,318 | 5,741 | 292,451 |

### Key Observations

- **Delta + Chimp** achieves the best compression at **8.30 bytes/point** — a **48.4% space savings** over Raw + Raw (16.08 bpp), yielding a **1.94× size reduction**.
- **Chimp vs Gorilla**: Chimp consistently beats Gorilla by a small margin in compression (e.g., Delta + Chimp: 8.30 bpp vs Delta + Gorilla: 8.54 bpp — **2.8% smaller**). With low-jitter semiconductor sensor data, the Chimp encoding's leading zero optimization is particularly effective.
- **Delta vs DeltaPacked (size)**: Delta produces slightly smaller blobs than DeltaPacked (e.g., Delta + Chimp: 8.30 vs DeltaPacked + Chimp: 8.49 bpp). DeltaPacked's Group Varint format adds a small per-group overhead.
- **Delta vs DeltaPacked (speed)**: DeltaPacked is slightly faster to **encode** (e.g., DeltaPacked + Raw: 508,519 ns/op vs Delta + Raw: 541,484 ns/op — **6% faster**). However, Delta is faster to **iterate** (e.g., Delta + Raw: 264,411 ns/op vs DeltaPacked + Raw: 277,038 ns/op — **5% faster**). The Group Varint batch decoding advantage may be more pronounced with larger timestamp deltas or different data patterns.
- **Encode speed tradeoff**: DeltaPacked + Raw is the fastest encoder (508,519 ns/op) while achieving 36.3% savings. The best-compression combo (Delta + Chimp at 793,987 ns/op) is 1.56× slower but saves an additional 12 percentage points.
- **Decode speed**: All 9 combinations decode at nearly identical speeds (5,692–6,138 ns/op), confirming that decode is dominated by header/index parsing overhead, not per-point decoding.

## Encode Performance

Encoding speed and memory allocation for each combination:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| DeltaPacked + Raw | 508,519 | 2,283,564 | 51 |
| Delta + Raw | 541,484 | 2,275,442 | 51 |
| Raw + Raw | 658,318 | 4,331,686 | 62 |
| DeltaPacked + Gorilla | 717,166 | 1,824,640 | 49 |
| Delta + Gorilla | 727,205 | 1,816,082 | 49 |
| Delta + Chimp | 793,987 | 1,487,446 | 48 |
| Raw + Gorilla | 835,219 | 3,881,485 | 61 |
| DeltaPacked + Chimp | 874,352 | 1,495,492 | 47 |
| Raw + Chimp | 967,214 | 3,545,073 | 60 |

## Decode Performance

Decoding speed (NewDecoder + Decode) and memory allocation:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Delta + Chimp | 5,692 | 32,824 | 7 |
| Delta + Gorilla | 5,718 | 32,824 | 7 |
| Raw + Raw | 5,741 | 32,824 | 7 |
| DeltaPacked + Gorilla | 5,827 | 32,824 | 7 |
| Delta + Raw | 5,884 | 32,824 | 7 |
| DeltaPacked + Raw | 5,957 | 32,824 | 7 |
| Raw + Gorilla | 5,994 | 32,824 | 7 |
| DeltaPacked + Chimp | 6,007 | 32,824 | 7 |
| Raw + Chimp | 6,138 | 32,824 | 7 |

## Iteration Performance

Sequential iteration speed (iterating all data points via `blob.All(metricID)`):

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Delta + Raw | 264,411 | 64,008 | 1,001 |
| DeltaPacked + Raw | 277,038 | 64,008 | 1,001 |
| Raw + Raw | 292,451 | 67,208 | 1,001 |
| Delta + Gorilla | 491,596 | 80,008 | 1,401 |
| DeltaPacked + Gorilla | 529,538 | 80,008 | 1,401 |
| Raw + Gorilla | 551,422 | 60,808 | 801 |
| Raw + Chimp | 574,823 | 60,808 | 801 |
| Delta + Chimp | 602,502 | 80,008 | 1,401 |
| DeltaPacked + Chimp | 654,361 | 80,008 | 1,401 |

**Note:** Raw-value encodings iterate fastest because they decode values via direct memory access (O(1) per point). XOR-based encodings (Gorilla, Chimp) require sequential bit unpacking. Chimp is slower to iterate than Gorilla despite better compression because its decoding path is more complex.

## Scaling Analysis

How bytes-per-point changes as points-per-metric increases, for each encoding combination.
The fixed per-metric overhead amortizes differently depending on the encoding.

| Points/Metric | Raw + Raw | Raw + Gorilla | Raw + Chimp | Delta + Raw | Delta + Gorilla | Delta + Chimp | DeltaPacked + Raw | DeltaPacked + Gorilla | DeltaPacked + Chimp |
|---------------|-----------|---------------|-------------|-------------|-----------------|---------------|-------------------|-----------------------|---------------------|
| 1 | 32.16 | 32.16 | 32.16 | 32.16 | 32.16 | 32.16 | 32.16 | 32.16 | 32.16 |
| 2 | 24.08 | 23.94 | 23.58 | 21.58 | 21.44 | 21.08 | 21.58 | 21.44 | 21.08 |
| 5 | 19.23 | 18.32 | 17.95 | 14.59 | 13.68 | 13.31 | 14.76 | 13.84 | 13.47 |
| 10 | 17.62 | 16.37 | 16.10 | 12.26 | 11.01 | 10.74 | 12.42 | 11.17 | 10.90 |
| 20 | 16.81 | 15.37 | 15.16 | 11.10 | 9.66 | 9.45 | 11.29 | 9.85 | 9.64 |
| 50 | 16.32 | 14.78 | 14.60 | 10.40 | 8.86 | 8.68 | 10.58 | 9.04 | 8.86 |
| 100 | 16.16 | 14.62 | 14.42 | 10.17 | 8.63 | 8.43 | 10.36 | 8.82 | 8.62 |
| 150 | 16.11 | 14.58 | 14.36 | 10.09 | 8.57 | 8.34 | 10.28 | 8.75 | 8.53 |
| 200 | 16.08 | 14.57 | 14.32 | 10.05 | 8.54 | 8.30 | 10.24 | 8.73 | 8.49 |

### Key Insights

- **At 1 point/metric**: All encodings produce **32.16 bytes/point** — the fixed per-metric header overhead completely dominates, making encoding choice irrelevant.
- **At 5 points/metric**: Compressed combos begin to differentiate. Delta + Chimp reaches **13.31 bpp** while Raw + Raw is at **19.23 bpp** — a 31% gap already.
- **The "sweet spot" is 50–100 points/metric**: Delta + Chimp drops from 8.68 bpp (50 ppm) to 8.43 bpp (100 ppm), only a 2.9% improvement. Beyond 100, gains are <1% per 50 additional points.
- **Raw + Raw amortizes the slowest**: Without encoding benefits, it reduces from 32.16 → 16.08 bpp (50% reduction). Delta + Chimp achieves a 74% reduction (32.16 → 8.30 bpp) because XOR encoding benefits from longer sequences of similar values.
- **All combos converge by 150 ppm**: The difference between 150 and 200 ppm is <0.5% for all encodings.

## Choosing an Encoding Strategy

### Decision Tree

```
Do you need O(1) random access to individual timestamps/values by index?
├─ YES → Use Raw timestamp encoding
│   ├─ Need smallest size? → Raw + Chimp (14.32 bpp, 10.9% savings)
│   ├─ Need fastest iteration? → Raw + Raw (292,451 ns/op iter)
│   └─ Balanced? → Raw + Gorilla (14.57 bpp, 551,422 ns/op iter)
│
└─ NO → Sequential access is fine (most use cases)
    │
    ├─ Priority: SMALLEST SIZE
    │   └─ Delta + Chimp (8.30 bpp, 48.4% savings, 793,987 ns/op encode)
    │
    ├─ Priority: FASTEST ITERATION
    │   └─ Delta + Raw (264,411 ns/op iter, 10.05 bpp, 37.5% savings)
    │
    ├─ Priority: FASTEST ENCODE
    │   └─ DeltaPacked + Raw (508,519 ns/op encode, 10.24 bpp, 36.3% savings)
    │       Group Varint packing enables faster encoding than standard Delta
    │
    └─ Priority: BALANCED (good compression + good speed)
        └─ Delta + Gorilla (8.54 bpp, 46.9% savings, 491,596 ns/op iter)
```

### Configuration Selection

| Use Case | Configuration | Rationale |
|----------|---------------|-----------|
| **Best compression** | Delta + Chimp | 8.30 bpp, 48.4% savings — smallest encoded size of all combos |
| **Fastest iteration** | Delta + Raw | 264,411 ns/op — raw values decode via direct memory access (O(1) per point) |
| **Fastest encode** | DeltaPacked + Raw | 508,519 ns/op — 1.06× faster than Delta + Raw (541,484 ns/op) |
| **Best balance** | Delta + Gorilla | 8.54 bpp (46.9% savings) with 491,596 ns/op iteration — #3 in size, #4 in iteration |
| **Random access needed** | Raw + Chimp | 14.32 bpp (10.9% savings) with O(1) `TimestampAt`/`ValueAt` support |
| **Maximum throughput** | Raw + Raw | 658,318 ns/op encode, 292,451 ns/op iterate — zero encoding overhead, O(1) everything |

### Points-per-Metric Guidelines

Based on Delta + Chimp scaling data (converged value: **8.30 bpp** at 200 ppm):

| Zone | PPM Range | Bytes/Point | vs Converged | Recommendation |
|------|-----------|-------------|--------------|----------------|
| **❌ Poor** | 1–2 | 32.16–21.08 | 2.5–3.9× | Avoid — fixed overhead dominates, no encoding benefit |
| **⚠️ Moderate** | 5–10 | 13.31–10.74 | 1.29–1.60× | Acceptable for real-time/low-latency use cases only |
| **✅ Good** | 20–50 | 9.45–8.68 | 1.05–1.14× | Recommended — good compression, within 14% of optimal |
| **✅✅ Optimal** | 100–200+ | 8.43–8.30 | 1.00–1.02× | Best efficiency — diminishing returns after ~100 ppm |
