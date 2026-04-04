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

All 9 valid timestamp × value encoding combinations, benchmarked without additional compression codecs.

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

## Scaling Analysis

How bytes-per-point changes as points-per-metric increases, for each encoding combination.
The fixed per-metric overhead amortizes differently depending on the encoding.

{{SCALING_TABLE}}

### Key Insights

{{SCALING_INSIGHTS}}

## Choosing an Encoding Strategy

### Decision Tree

<!-- HINT: Generate a text-based decision tree from benchmark data.
     Determine categories by comparing matrix results:
     - "Best compression": combo with lowest bytes_per_point
     - "Fastest encode": combo with lowest encode.ns_per_op
     - "Fastest decode": combo with lowest decode.ns_per_op
     - "Fastest iteration": combo with lowest iter_seq.ns_per_op
     - "Random access": Raw timestamp combos support O(1) TimestampAt/ValueAt
     - Note: DeltaPacked's advantage is decode/iteration speed (Group Varint batch decoding), not size
     Use actual combo labels and numbers from the benchmark. -->

{{DECISION_TREE}}

### Configuration Selection

<!-- HINT: Generate a use-case → configuration mapping table from benchmark data.
     Derive each row from actual rankings:
     - "Best compression": lowest bytes_per_point combo, cite actual bpp and savings%
     - "Fastest iteration": lowest iter_seq.ns_per_op combo, cite actual speed
     - "Fastest encode": lowest encode.ns_per_op combo
     - "Best balance": combo that ranks well in both size and speed (not worst in either)
     - "Random access needed": best Raw-timestamp combo (O(1) access)
     - "Maximum throughput": raw-raw baseline, cite actual encode speed
     All rationale should reference specific benchmark numbers. -->

{{CONFIGURATION_SELECTION}}

### Points-per-Metric Guidelines

<!-- HINT: Generate a PPM zone table from scaling data.
     Use the scaling results for the best-compression combo to determine zones:
     - "Poor" zone: PPM range where bytes_per_point is >2× the converged value
     - "Moderate" zone: PPM range where bytes_per_point is 1.3-2× converged
     - "Good" zone: PPM range where bytes_per_point is 1.05-1.3× converged
     - "Optimal" zone: PPM range where bytes_per_point is within 5% of converged
     Cite actual bytes_per_point values at boundary points.
     The "converged value" is the bytes_per_point at the highest tested PPM. -->

{{PPM_GUIDELINES}}
