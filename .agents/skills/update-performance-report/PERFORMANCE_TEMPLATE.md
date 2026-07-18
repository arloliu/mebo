# Performance Guide

> **Auto-generated** by the `update-performance-report` agent skill from benchmark data.
> To regenerate: run `tests/measurev2/` and use the skill.

{{BENCHMARK_METADATA}}

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
- [Choosing an Encoding Strategy](#choosing-an-encoding-strategy)

## Quick Reference

**TL;DR — Recommended Configurations:**

{{QUICK_REFERENCE}}

## Benchmark Methodology

### Test Environment

{{BENCHMARK_METADATA_DETAIL}}

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

{{ENCODING_MATRIX}}

### Key Observations

{{ENCODING_OBSERVATIONS}}

## Encode Performance

Encoding speed and memory allocation for each combination:

{{ENCODE_PERFORMANCE}}

## Decode Performance

Decoding speed (NewDecoder + Decode) and memory allocation:

{{DECODE_PERFORMANCE}}

## Iteration Performance

Sequential iteration speed (iterating all data points via `blob.All(metricID)`):

{{ITERATION_PERFORMANCE}}

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

{{RANDOM_ACCESS_PERFORMANCE}}

## Scaling Analysis

How bytes-per-point changes as points-per-metric increases, for each encoding combination.
The fixed per-metric overhead amortizes differently depending on the encoding.

### Standard Encodings

{{SCALING_TABLE_STANDARD}}

### Shared-Timestamp Encodings

{{SCALING_TABLE_SHARED}}

### Key Insights

{{SCALING_INSIGHTS}}

## Choosing an Encoding Strategy

### Decision Tree

{{DECISION_TREE}}

### Configuration Selection

{{CONFIGURATION_SELECTION}}

### Points-per-Metric Guidelines

{{PPM_GUIDELINES}}
