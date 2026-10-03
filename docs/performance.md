# Performance Guide

> Most of this document is **auto-generated** by the `update-performance-report` agent skill
> from benchmark data — everything from [Quick Reference](#quick-reference) through
> [Scaling Analysis](#scaling-analysis).
> To regenerate those sections: run `tests/measurev2/` and use the skill.
>
> The [Codec Selection by Data Shape](#codec-selection-by-data-shape) section is composed manually from `tests/measurev2`'s profile-based benchmarks
> (its "Provenance" boxes have the reproduce recipes);
> the skill does not regenerate it.
> **Regenerating this document wipes that section:**
> re-add it after running the skill, from the per-profile JSON its recipes produce (gitignored, so regenerate them first).

| | |
|---|---|
| **Benchmark Date** | 2026-10-04 |
| **Platform** | linux/amd64 (32 CPUs), Go go1.26.7 |
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
| **Fastest Encode** | 330,719 ns/op | Raw + Raw |
| **Baseline** | 16.081 bytes/point | Raw + Raw |

## Benchmark Methodology

### Test Environment

| Parameter | Value | Description |
|-----------|-------|-------------|
| **Go Version** | go1.26.7 | Compiler and runtime |
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

All 30 valid encoding combinations (15 standard timestamp × value + 15 with shared timestamps — 3 timestamp encodings × 5 value encodings: Raw, Gorilla, Chimp, ALP, ALP-RLE), benchmarked without additional compression codecs.
Shared-timestamp combos use `WithSharedTimestamps()` to deduplicate identical timestamp sequences across metrics.

Sorted by encoded size (most efficient first):

| Configuration | Bytes/Point | Space Savings | vs Raw | Encode (ns/op) | Decode (ns/op) | Iterate (ns/op) |
|---------------|-------------|---------------|--------|----------------|----------------|-----------------|
| Shared Delta + Chimp | 6.349 | 60.5% | 2.533× | 655,662 | 8,523 | 394,402 |
| Shared DeltaPacked + Chimp | 6.350 | 60.5% | 2.532× | 725,679 | 6,512 | 425,515 |
| Shared Raw + Chimp | 6.380 | 60.3% | 2.521× | 623,826 | 6,643 | 426,661 |
| Shared Delta + ALP | 6.473 | 59.7% | 2.484× | 2,606,317 | 8,378 | 230,673 |
| Shared Delta + ALP-RLE | 6.473 | 59.7% | 2.484× | 2,567,791 | 8,393 | 237,016 |
| Shared DeltaPacked + ALP | 6.474 | 59.7% | 2.484× | 2,635,973 | 8,997 | 237,268 |
| Shared DeltaPacked + ALP-RLE | 6.474 | 59.7% | 2.484× | 2,601,354 | 8,512 | 225,337 |
| Shared Raw + ALP | 6.503 | 59.6% | 2.473× | 2,807,426 | 9,072 | 218,424 |
| Shared Raw + ALP-RLE | 6.503 | 59.6% | 2.473× | 2,627,707 | 8,427 | 203,582 |
| Shared Delta + Gorilla | 6.597 | 59.0% | 2.438× | 516,650 | 7,298 | 241,357 |
| Shared DeltaPacked + Gorilla | 6.598 | 59.0% | 2.437× | 515,290 | 7,068 | 329,716 |
| Shared Raw + Gorilla | 6.627 | 58.8% | 2.427× | 511,913 | 6,489 | 311,529 |
| Shared Delta + Raw | 8.101 | 49.6% | 1.985× | 384,398 | 6,695 | 252,818 |
| Shared DeltaPacked + Raw | 8.102 | 49.6% | 1.985× | 385,158 | 6,791 | 274,108 |
| Shared Raw + Raw | 8.131 | 49.4% | 1.978× | 348,399 | 6,534 | 284,632 |
| Delta + Chimp | 8.297 | 48.4% | 1.938× | 577,737 | 5,389 | 351,583 |
| Delta + ALP | 8.423 | 47.6% | 1.909× | 2,454,952 | 6,955 | 238,195 |
| Delta + ALP-RLE | 8.423 | 47.6% | 1.909× | 2,508,364 | 7,011 | 238,077 |
| DeltaPacked + Chimp | 8.487 | 47.2% | 1.895× | 613,064 | 5,557 | 426,200 |
| Delta + Gorilla | 8.544 | 46.9% | 1.882× | 452,225 | 5,379 | 243,102 |
| DeltaPacked + ALP | 8.613 | 46.4% | 1.867× | 2,519,224 | 7,389 | 224,124 |
| DeltaPacked + ALP-RLE | 8.613 | 46.4% | 1.867× | 2,526,722 | 6,978 | 222,664 |
| DeltaPacked + Gorilla | 8.734 | 45.7% | 1.841× | 470,626 | 5,388 | 326,890 |
| Delta + Raw | 10.054 | 37.5% | 1.599× | 350,880 | 5,798 | 251,160 |
| DeltaPacked + Raw | 10.244 | 36.3% | 1.570× | 354,547 | 5,442 | 269,766 |
| Raw + Chimp | 14.324 | 10.9% | 1.123× | 627,848 | 5,581 | 425,323 |
| Raw + ALP | 14.449 | 10.1% | 1.113× | 2,453,389 | 7,084 | 198,948 |
| Raw + ALP-RLE | 14.449 | 10.1% | 1.113× | 2,495,077 | 7,163 | 199,246 |
| Raw + Gorilla | 14.571 | 9.4% | 1.104× | 444,661 | 5,654 | 311,985 |
| Raw + Raw | 16.081 | 0.0% | 1.000× | 330,719 | 5,632 | 279,892 |

### Key Observations

- **Best compression**: Shared Delta + Chimp achieves 6.349 bytes/point (60.5% savings vs raw-raw baseline). Shared timestamp deduplication eliminates redundant timestamp storage across 200 metrics.
- **Shared timestamps**: Enabling `WithSharedTimestamps()` provides 23% additional savings over the best non-shared configuration (Delta + Chimp at 8.297 bytes/point). The savings come from storing the timestamp column once instead of 200 times.
- **Chimp vs Gorilla**: Chimp consistently outperforms Gorilla by ~2.9% in compression. For example, Delta + Chimp (8.297 BPP) vs Delta + Gorilla (8.544 BPP). Both use XOR-based floating-point encoding.
- **ALP on this dataset**: Delta + ALP is 8.423 BPP, 1.5% larger than Chimp on this dataset — this benchmark's data is a full-precision random walk, not decimal-quantized, which is not ALP's strength. ALP's main scheme wins big (4–6× smaller than raw, 1–2.5× smaller than the next-best codec) specifically on decimal-quantized sensor data; see the "Codec Selection by Data Shape" section below for the profile-based comparison where it does shine. ALP's encode is also markedly slower here (2,454,952 vs 577,737 ns/op for Chimp) due to its per-column (e,f) search.
- **ALP-RLE on this dataset**: Delta + ALP-RLE is 8.423 BPP, the same size as Delta + ALP, and encodes at 1.02× its cost — the same size means few or no columns had enough repeats for the runs layout. ALP-RLE pays off where many consecutive points repeat; see "Codec Selection by Data Shape" below.
- **DeltaPacked vs Delta**: DeltaPacked shows ~2.3% larger encoded size than Delta (8.487 vs 8.297 BPP). DeltaPacked's advantage is **decode/iteration speed** via Group Varint batch decoding, not compression ratio.
- **Encode speed tradeoff**: Raw + Raw encodes fastest at 330,719 ns/op — no delta/XOR/digit computation, just a byte copy, even though its allocation footprint (698,289 B/op) is larger than most compressed combos (uncompressed data is bigger to begin with).
- **Decode speed**: The fastest shared-TS combo decodes ~21% slower than the fastest non-shared combo (6,489 vs 5,379 ns/op) — shared-TS blobs are smaller, but opening one also decodes the shared timestamp columns into the cache behind its O(1) `TimestampAt`, so a smaller blob does not mean a faster open.

## Encode Performance

Encoding speed and memory allocation for each combination:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Raw + Raw | 330,719 | 698,289 | 34 |
| Shared Raw + Raw | 348,399 | 1,083,761 | 64 |
| Delta + Raw | 350,880 | 454,930 | 34 |
| DeltaPacked + Raw | 354,547 | 463,353 | 34 |
| Shared Delta + Raw | 384,398 | 837,641 | 63 |
| Shared DeltaPacked + Raw | 385,158 | 848,399 | 64 |
| Raw + Gorilla | 444,661 | 636,957 | 34 |
| Delta + Gorilla | 452,225 | 393,033 | 34 |
| DeltaPacked + Gorilla | 470,626 | 398,827 | 34 |
| Shared Raw + Gorilla | 511,913 | 963,045 | 64 |
| Shared DeltaPacked + Gorilla | 515,290 | 725,349 | 64 |
| Shared Delta + Gorilla | 516,650 | 718,731 | 64 |
| Delta + Chimp | 577,737 | 382,664 | 34 |
| DeltaPacked + Chimp | 613,064 | 391,813 | 34 |
| Shared Raw + Chimp | 623,826 | 946,941 | 64 |
| Raw + Chimp | 627,848 | 623,078 | 34 |
| Shared Delta + Chimp | 655,662 | 701,316 | 64 |
| Shared DeltaPacked + Chimp | 725,679 | 711,865 | 64 |
| Raw + ALP | 2,453,389 | 676,957 | 203 |
| Delta + ALP | 2,454,952 | 445,101 | 202 |
| Raw + ALP-RLE | 2,495,077 | 681,055 | 203 |
| Delta + ALP-RLE | 2,508,364 | 441,409 | 202 |
| DeltaPacked + ALP | 2,519,224 | 437,044 | 202 |
| DeltaPacked + ALP-RLE | 2,526,722 | 447,270 | 202 |
| Shared Delta + ALP-RLE | 2,567,791 | 777,434 | 227 |
| Shared DeltaPacked + ALP-RLE | 2,601,354 | 780,961 | 227 |
| Shared Delta + ALP | 2,606,317 | 781,212 | 227 |
| Shared Raw + ALP-RLE | 2,627,707 | 1,034,886 | 228 |
| Shared DeltaPacked + ALP | 2,635,973 | 777,925 | 227 |
| Shared Raw + ALP | 2,807,426 | 1,033,219 | 228 |

## Decode Performance

Decoding speed (NewDecoder + Decode) and memory allocation:

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Delta + Gorilla | 5,379 | 18,616 | 7 |
| DeltaPacked + Gorilla | 5,388 | 18,616 | 7 |
| Delta + Chimp | 5,389 | 18,616 | 7 |
| DeltaPacked + Raw | 5,442 | 18,616 | 7 |
| DeltaPacked + Chimp | 5,557 | 18,616 | 7 |
| Raw + Chimp | 5,581 | 18,616 | 7 |
| Raw + Raw | 5,632 | 18,616 | 7 |
| Raw + Gorilla | 5,654 | 18,616 | 7 |
| Delta + Raw | 5,798 | 18,616 | 7 |
| Shared Raw + Gorilla | 6,489 | 22,696 | 11 |
| Shared DeltaPacked + Chimp | 6,512 | 22,696 | 11 |
| Shared Raw + Raw | 6,534 | 22,696 | 11 |
| Shared Raw + Chimp | 6,643 | 22,696 | 11 |
| Shared Delta + Raw | 6,695 | 22,696 | 11 |
| Shared DeltaPacked + Raw | 6,791 | 22,696 | 11 |
| Delta + ALP | 6,955 | 18,616 | 7 |
| DeltaPacked + ALP-RLE | 6,978 | 18,616 | 7 |
| Delta + ALP-RLE | 7,011 | 18,616 | 7 |
| Shared DeltaPacked + Gorilla | 7,068 | 22,696 | 11 |
| Raw + ALP | 7,084 | 18,616 | 7 |
| Raw + ALP-RLE | 7,163 | 18,616 | 7 |
| Shared Delta + Gorilla | 7,298 | 22,696 | 11 |
| DeltaPacked + ALP | 7,389 | 18,616 | 7 |
| Shared Delta + ALP | 8,378 | 22,696 | 11 |
| Shared Delta + ALP-RLE | 8,393 | 22,696 | 11 |
| Shared Raw + ALP-RLE | 8,427 | 22,696 | 11 |
| Shared DeltaPacked + ALP-RLE | 8,512 | 22,696 | 11 |
| Shared Delta + Chimp | 8,523 | 22,696 | 11 |
| Shared DeltaPacked + ALP | 8,997 | 22,696 | 11 |
| Shared Raw + ALP | 9,072 | 22,696 | 11 |

## Iteration Performance

Sequential iteration speed (iterating all data points via `blob.All(metricID)`):

| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |
|---------------|---------------|---------------|-----------|
| Raw + ALP | 198,948 | 737,864 | 1003 |
| Raw + ALP-RLE | 199,246 | 737,845 | 1002 |
| Shared Raw + ALP-RLE | 203,582 | 738,131 | 1003 |
| Shared Raw + ALP | 218,424 | 738,130 | 1003 |
| DeltaPacked + ALP-RLE | 222,664 | 738,057 | 1003 |
| DeltaPacked + ALP | 224,124 | 738,014 | 1003 |
| Shared DeltaPacked + ALP-RLE | 225,337 | 738,150 | 1003 |
| Shared Delta + ALP | 230,673 | 738,154 | 1003 |
| Shared Delta + ALP-RLE | 237,016 | 738,124 | 1003 |
| Shared DeltaPacked + ALP | 237,268 | 738,151 | 1003 |
| Delta + ALP-RLE | 238,077 | 738,037 | 1003 |
| Delta + ALP | 238,195 | 738,033 | 1003 |
| Shared Delta + Gorilla | 241,357 | 19,208 | 601 |
| Delta + Gorilla | 243,102 | 19,208 | 601 |
| Delta + Raw | 251,160 | 25,608 | 801 |
| Shared Delta + Raw | 252,818 | 25,608 | 801 |
| DeltaPacked + Raw | 269,766 | 54,408 | 1401 |
| Shared DeltaPacked + Raw | 274,108 | 54,408 | 1401 |
| Raw + Raw | 279,892 | 28,808 | 801 |
| Shared Raw + Raw | 284,632 | 28,808 | 801 |
| Shared Raw + Gorilla | 311,529 | 22,408 | 601 |
| Raw + Gorilla | 311,985 | 22,408 | 601 |
| DeltaPacked + Gorilla | 326,890 | 19,208 | 601 |
| Shared DeltaPacked + Gorilla | 329,716 | 19,208 | 601 |
| Delta + Chimp | 351,583 | 19,208 | 601 |
| Shared Delta + Chimp | 394,402 | 19,208 | 601 |
| Raw + Chimp | 425,323 | 22,408 | 601 |
| Shared DeltaPacked + Chimp | 425,515 | 19,208 | 601 |
| DeltaPacked + Chimp | 426,200 | 19,208 | 601 |
| Shared Raw + Chimp | 426,661 | 22,408 | 601 |

**Note:** Compressed encodings can iterate faster than raw due to reduced memory bandwidth — smaller data fits better in CPU cache.

## Random Access Performance

`ValueAt`/`TimestampAt` at a uniformly random index per metric (not a fixed first/last probe —
see `randomAccessPattern` in `tests/measurev2/bench.go`). This matters because the encodings
have fundamentally different random-access complexity, not just different constants:

| Encoding | `At()` complexity | Why |
|---|---|---|
| Raw (timestamp or value) | O(1) | Direct offset into a fixed-width array |
| ALP (value) | O(1) + O(log k) | O(1) windowed bit read, plus binary search over that column's exception sidecar (k = exceptions in that column, not n) |
| ALP-RLE (value) | O(index/64) + O(log k) | A column with repeats ranks the run-start bitmap one 64-bit word at a time (at most 3 popcounts at 150 points), then reads that run like ALP; a column without repeats is plain ALP |
| Delta / DeltaPacked (timestamp) | O(index) | Must sequentially decode every delta from the start — each value depends on the accumulated sum before it |
| Shared timestamps (any encoding) | O(1) | Decoded once into a cache when the blob is opened; `TimestampAt` reads the cache |
| Gorilla / Chimp (value) | O(index) | Must sequentially decode the XOR chain from the start of the column |

A uniformly random index makes the O(index) encodings pay their realistic *average* cost across
a column, not a cherry-picked best (index 0) or worst (last index) case.

| Configuration | ValueAt (ns/op) | Value complexity | TimestampAt (ns/op) | Timestamp complexity |
|---|---:|---|---:|---|
| Shared Raw + Raw | 4,684 | O(1) | 4,304 | O(1), cached when the blob is opened |
| Shared DeltaPacked + Raw | 4,750 | O(1) | 4,254 | O(1), cached when the blob is opened |
| Shared Delta + Raw | 4,860 | O(1) | 4,360 | O(1), cached when the blob is opened |
| Shared DeltaPacked + ALP-RLE | 6,400 | O(index/64) bitmap rank + O(1) + O(log k) exceptions | 4,262 | O(1), cached when the blob is opened |
| Shared Raw + ALP-RLE | 6,386 | O(index/64) bitmap rank + O(1) + O(log k) exceptions | 4,277 | O(1), cached when the blob is opened |
| Shared Delta + ALP | 6,426 | O(1) + O(log k) exceptions | 4,272 | O(1), cached when the blob is opened |
| Shared Delta + ALP-RLE | 6,467 | O(index/64) bitmap rank + O(1) + O(log k) exceptions | 4,272 | O(1), cached when the blob is opened |
| Raw + Raw | 5,129 | O(1) | 5,636 | O(1) |
| Shared Raw + ALP | 6,482 | O(1) + O(log k) exceptions | 4,344 | O(1), cached when the blob is opened |
| Shared DeltaPacked + ALP | 6,514 | O(1) + O(log k) exceptions | 4,349 | O(1), cached when the blob is opened |
| Raw + ALP-RLE | 8,210 | O(index/64) bitmap rank + O(1) + O(log k) exceptions | 5,630 | O(1) |
| Raw + ALP | 8,234 | O(1) + O(log k) exceptions | 5,672 | O(1) |
| DeltaPacked + Raw | 5,485 | O(1) | 30,015 | O(index), sequential decode from the start |
| DeltaPacked + ALP | 8,207 | O(1) + O(log k) exceptions | 29,887 | O(index), sequential decode from the start |
| DeltaPacked + ALP-RLE | 8,222 | O(index/64) bitmap rank + O(1) + O(log k) exceptions | 30,035 | O(index), sequential decode from the start |
| Delta + Raw | 5,102 | O(1) | 48,819 | O(index), sequential decode from the start |
| Delta + ALP | 8,344 | O(1) + O(log k) exceptions | 48,415 | O(index), sequential decode from the start |
| Delta + ALP-RLE | 8,091 | O(index/64) bitmap rank + O(1) + O(log k) exceptions | 48,806 | O(index), sequential decode from the start |
| Shared Delta + Gorilla | 173,270 | O(index), sequential XOR decode from the start | 4,255 | O(1), cached when the blob is opened |
| Shared Raw + Gorilla | 175,697 | O(index), sequential XOR decode from the start | 4,270 | O(1), cached when the blob is opened |
| Raw + Gorilla | 176,583 | O(index), sequential XOR decode from the start | 5,793 | O(1) |
| Shared DeltaPacked + Gorilla | 181,273 | O(index), sequential XOR decode from the start | 5,229 | O(1), cached when the blob is opened |
| DeltaPacked + Gorilla | 176,764 | O(index), sequential XOR decode from the start | 30,112 | O(index), sequential decode from the start |
| Shared Raw + Chimp | 202,898 | O(index), sequential XOR decode from the start | 4,261 | O(1), cached when the blob is opened |
| Shared Delta + Chimp | 203,481 | O(index), sequential XOR decode from the start | 4,322 | O(1), cached when the blob is opened |
| Shared DeltaPacked + Chimp | 204,988 | O(index), sequential XOR decode from the start | 4,246 | O(1), cached when the blob is opened |
| Raw + Chimp | 203,790 | O(index), sequential XOR decode from the start | 5,783 | O(1) |
| Delta + Gorilla | 177,582 | O(index), sequential XOR decode from the start | 48,767 | O(index), sequential decode from the start |
| DeltaPacked + Chimp | 203,941 | O(index), sequential XOR decode from the start | 30,033 | O(index), sequential decode from the start |
| Delta + Chimp | 202,245 | O(index), sequential XOR decode from the start | 48,835 | O(index), sequential decode from the start |

## Scaling Analysis

How bytes-per-point changes as points-per-metric increases, for each encoding combination.
The fixed per-metric overhead amortizes differently depending on the encoding.

### Standard Encodings

| Points/Metric | raw-raw | raw-gorilla | raw-chimp | raw-alp | raw-alprle | delta-raw | delta-gorilla | delta-chimp | delta-alp | delta-alprle | deltapacked-raw | deltapacked-gorilla | deltapacked-chimp | deltapacked-alp | deltapacked-alprle |
|---------------|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 32.160 | 32.160 | 32.160 | 33.160 | 33.160 | 32.160 | 32.160 | 32.160 | 33.160 | 33.160 | 32.160 | 32.160 | 32.160 | 33.160 | 33.160 |
| 2 | 24.080 | 23.942 | 23.582 | 24.580 | 24.580 | 21.580 | 21.442 | 21.082 | 22.080 | 22.080 | 21.580 | 21.442 | 21.082 | 22.080 | 22.080 |
| 5 | 19.232 | 18.315 | 17.946 | 19.432 | 19.432 | 14.592 | 13.675 | 13.306 | 14.792 | 14.792 | 14.761 | 13.844 | 13.475 | 14.961 | 14.961 |
| 10 | 17.616 | 16.369 | 16.097 | 17.131 | 17.131 | 12.262 | 11.014 | 10.743 | 11.777 | 11.777 | 12.419 | 11.171 | 10.900 | 11.934 | 11.934 |
| 20 | 16.808 | 15.369 | 15.159 | 15.664 | 15.664 | 11.097 | 9.658 | 9.447 | 9.952 | 9.952 | 11.291 | 9.852 | 9.642 | 10.146 | 10.146 |
| 50 | 16.323 | 14.783 | 14.602 | 14.825 | 14.825 | 10.401 | 8.861 | 8.679 | 8.902 | 8.902 | 10.581 | 9.041 | 8.860 | 9.083 | 9.083 |
| 100 | 16.162 | 14.621 | 14.418 | 14.553 | 14.553 | 10.169 | 8.629 | 8.426 | 8.561 | 8.561 | 10.360 | 8.819 | 8.616 | 8.751 | 8.751 |
| 150 | 16.108 | 14.582 | 14.356 | 14.502 | 14.502 | 10.092 | 8.567 | 8.340 | 8.487 | 8.487 | 10.280 | 8.754 | 8.527 | 8.674 | 8.674 |
| 200 | 16.081 | 14.571 | 14.324 | 14.449 | 14.449 | 10.054 | 8.544 | 8.297 | 8.423 | 8.423 | 10.244 | 8.734 | 8.487 | 8.613 | 8.613 |

### Shared-Timestamp Encodings

| Points/Metric | shared-raw-raw | shared-raw-gorilla | shared-raw-chimp | shared-raw-alp | shared-raw-alprle | shared-delta-raw | shared-delta-gorilla | shared-delta-chimp | shared-delta-alp | shared-delta-alprle | shared-deltapacked-raw | shared-deltapacked-gorilla | shared-deltapacked-chimp | shared-deltapacked-alp | shared-deltapacked-alprle |
|---------------|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 26.220 | 26.220 | 26.220 | 27.220 | 27.220 | 26.220 | 26.220 | 26.220 | 27.220 | 27.220 | 26.220 | 26.220 | 26.220 | 27.220 | 27.220 |
| 2 | 17.130 | 16.970 | 16.630 | 17.630 | 17.630 | 17.117 | 16.957 | 16.617 | 17.617 | 17.617 | 17.117 | 16.957 | 16.617 | 17.617 | 17.617 |
| 5 | 11.676 | 10.777 | 10.394 | 11.876 | 11.876 | 11.653 | 10.754 | 10.371 | 11.853 | 11.853 | 11.654 | 10.755 | 10.372 | 11.854 | 11.854 |
| 10 | 9.858 | 8.627 | 8.331 | 9.351 | 9.351 | 9.831 | 8.600 | 8.303 | 9.323 | 9.323 | 9.832 | 8.601 | 8.305 | 9.325 | 9.325 |
| 20 | 8.949 | 7.529 | 7.309 | 7.807 | 7.807 | 8.920 | 7.500 | 7.280 | 7.778 | 7.778 | 8.921 | 7.501 | 7.282 | 7.779 | 7.779 |
| 50 | 8.404 | 6.881 | 6.691 | 6.909 | 6.909 | 8.374 | 6.851 | 6.661 | 6.880 | 6.880 | 8.375 | 6.852 | 6.662 | 6.880 | 6.880 |
| 100 | 8.222 | 6.693 | 6.485 | 6.615 | 6.615 | 8.192 | 6.663 | 6.455 | 6.585 | 6.585 | 8.193 | 6.664 | 6.455 | 6.586 | 6.586 |
| 150 | 8.161 | 6.643 | 6.415 | 6.545 | 6.545 | 8.131 | 6.613 | 6.385 | 6.514 | 6.514 | 8.132 | 6.614 | 6.386 | 6.515 | 6.515 |
| 200 | 8.131 | 6.627 | 6.380 | 6.503 | 6.503 | 8.101 | 6.597 | 6.349 | 6.473 | 6.473 | 8.102 | 6.598 | 6.350 | 6.474 | 6.474 |

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
Six calibrated profiles (`cal_*`) add decimal gauges where many points repeat the previous value
or move in very small steps, the shapes ALP-RLE is built for.

**Provenance:** 200 metrics × 200 points, seed 42,
same environment as above (go1.26.7, linux/amd64, 32 CPUs, 2026-10-04).
Each profile sets its own scrape interval, value steps and repeat rate (`tests/measurev2/types.go`),
and each timestamp advances by the interval ±500 ns, stored in microseconds (`GenerateProfile` in `tests/measurev2/generator.go`);
the `-value-jitter` and `-ts-jitter` defaults recorded in the JSON metadata do not apply to profiles.
The raw JSON is gitignored (regenerate it yourself, don't expect it committed).
Reproduce with:

```bash
cd tests/measurev2
for p in decimal_gauge_2dp decimal_gauge_4dp counter sparse_constant worst_case \
         cal_2dp_hold30 cal_2dp_hold50 cal_2dp_hold70 \
         cal_2dp_step0.005 cal_1dp_step0.03 cal_1dp_step0.01; do
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
| `sparse_constant` — mostly-constant value, 60s scrape | Delta + ALP-RLE | 1.419 | 11.3× | ALP-RLE |
| `worst_case` — full-precision random walk, 1s | Delta + Chimp | 7.369 | 2.2× | Chimp |
| `cal_2dp_hold30` — 2dp gauge, 30% of points repeat the previous value | Delta + ALP-RLE | 2.477 | 6.5× | ALP-RLE |
| `cal_2dp_hold50` — 2dp gauge, 50% of points repeat the previous value | Delta + ALP-RLE | 2.138 | 7.5× | ALP-RLE |
| `cal_2dp_hold70` — 2dp gauge, 70% of points repeat the previous value | Delta + ALP-RLE | 1.816 | 8.9× | ALP-RLE |
| `cal_2dp_step0.005` — 2dp gauge, steps of at most ±0.005% | Delta + ALP-RLE | 2.004 | 8.0× | ALP-RLE |
| `cal_1dp_step0.03` — 1dp gauge, steps of at most ±0.03% | Delta + ALP-RLE | 1.879 | 8.6× | ALP-RLE |
| `cal_1dp_step0.01` — 1dp gauge, steps of at most ±0.01% | Delta + ALP-RLE | 1.632 | 9.9× | ALP-RLE |

Delta is the best timestamp tier in every profile (DeltaPacked trades ~0.25 B/pt for iteration speed).
The winning *value* codec changes with the data:
**ALP** for decimals and counters, **ALP-RLE** wherever many points repeat or barely move,
and **Chimp** for genuinely full-precision data.
ALP-RLE ties ALP byte for byte on the decimal and counter profiles, which have too few repeats for its runs layout.
ALP's margin over the *next-best non-ALP codec* (not raw) varies a lot by shape:
2.53× on `decimal_gauge_2dp` (vs Chimp's 7.212), 1.94× on `decimal_gauge_4dp` (vs Chimp's 7.367),
but only 1.05× on `counter` (vs Gorilla's 2.718, its closest competitor there).
On `sparse_constant`, ALP-RLE is 1.13× smaller than Gorilla (1.605), where plain ALP was 1.4× larger.
The "×" column above is **vs the Raw+Raw baseline**, not vs the next-best codec — don't conflate the two.

### Full compression grids (bytes/point)

<details>
<summary>Per-profile timestamp × value grids</summary>

#### decimal_gauge_2dp

| ts \ val | Raw | Gorilla | Chimp | ALP | ALP-RLE |
|---|---:|---:|---:|---:|---:|
| Raw | 16.081 | 14.547 | 14.162 | 9.804 | 9.804 |
| Delta | 9.131 | 7.597 | 7.212 | **2.854** | **2.854** |
| DeltaPacked | 9.381 | 7.847 | 7.462 | 3.104 | 3.104 |

#### decimal_gauge_4dp

| ts \ val | Raw | Gorilla | Chimp | ALP | ALP-RLE |
|---|---:|---:|---:|---:|---:|
| Raw | 16.081 | 14.567 | 14.317 | 10.752 | 10.752 |
| Delta | 9.131 | 7.617 | 7.367 | **3.802** | **3.802** |
| DeltaPacked | 9.381 | 7.867 | 7.617 | 4.052 | 4.052 |

#### counter

| ts \ val | Raw | Gorilla | Chimp | ALP | ALP-RLE |
|---|---:|---:|---:|---:|---:|
| Raw | 16.081 | 9.668 | 9.991 | 9.531 | 9.531 |
| Delta | 9.131 | 2.718 | 3.041 | **2.581** | **2.581** |
| DeltaPacked | 9.381 | 2.968 | 3.291 | 2.831 | 2.831 |

#### sparse_constant

| ts \ val | Raw | Gorilla | Chimp | ALP | ALP-RLE |
|---|---:|---:|---:|---:|---:|
| Raw | 16.081 | 8.555 | 8.658 | 9.205 | 8.369 |
| Delta | 9.131 | 1.605 | 1.708 | 2.255 | **1.419** |
| DeltaPacked | 9.381 | 1.855 | 1.958 | 2.505 | 1.669 |

#### worst_case

| ts \ val | Raw | Gorilla | Chimp | ALP | ALP-RLE |
|---|---:|---:|---:|---:|---:|
| Raw | 16.081 | 14.571 | 14.324 | 14.449 | 14.449 |
| Delta | 9.126 | 7.616 | **7.369** | 7.494 | 7.494 |
| DeltaPacked | 9.376 | 7.866 | 7.619 | 7.744 | 7.744 |

#### cal_2dp_hold30

| ts \ val | Raw | Gorilla | Chimp | ALP | ALP-RLE |
|---|---:|---:|---:|---:|---:|
| Raw | 16.081 | 12.653 | 12.458 | 9.761 | 9.427 |
| Delta | 9.131 | 5.703 | 5.508 | 2.811 | **2.477** |
| DeltaPacked | 9.381 | 5.953 | 5.758 | 3.061 | 2.727 |

#### cal_2dp_hold50

| ts \ val | Raw | Gorilla | Chimp | ALP | ALP-RLE |
|---|---:|---:|---:|---:|---:|
| Raw | 16.081 | 11.380 | 11.296 | 9.711 | 9.088 |
| Delta | 9.131 | 4.430 | 4.346 | 2.761 | **2.138** |
| DeltaPacked | 9.381 | 4.680 | 4.596 | 3.011 | 2.388 |

#### cal_2dp_hold70

| ts \ val | Raw | Gorilla | Chimp | ALP | ALP-RLE |
|---|---:|---:|---:|---:|---:|
| Raw | 16.081 | 10.127 | 10.139 | 9.672 | 8.766 |
| Delta | 9.131 | 3.177 | 3.189 | 2.722 | **1.816** |
| DeltaPacked | 9.381 | 3.427 | 3.439 | 2.972 | 2.066 |

#### cal_2dp_step0.005

| ts \ val | Raw | Gorilla | Chimp | ALP | ALP-RLE |
|---|---:|---:|---:|---:|---:|
| Raw | 16.081 | 12.973 | 12.773 | 8.994 | 8.954 |
| Delta | 9.131 | 6.023 | 5.823 | 2.044 | **2.004** |
| DeltaPacked | 9.381 | 6.273 | 6.073 | 2.294 | 2.254 |

#### cal_1dp_step0.03

| ts \ val | Raw | Gorilla | Chimp | ALP | ALP-RLE |
|---|---:|---:|---:|---:|---:|
| Raw | 16.081 | 12.737 | 12.447 | 8.859 | 8.829 |
| Delta | 9.131 | 5.787 | 5.497 | 1.909 | **1.879** |
| DeltaPacked | 9.381 | 6.037 | 5.747 | 2.159 | 2.129 |

#### cal_1dp_step0.01

| ts \ val | Raw | Gorilla | Chimp | ALP | ALP-RLE |
|---|---:|---:|---:|---:|---:|
| Raw | 16.081 | 11.025 | 11.004 | 8.671 | 8.582 |
| Delta | 9.131 | 4.075 | 4.054 | 1.721 | **1.632** |
| DeltaPacked | 9.381 | 4.325 | 4.304 | 1.971 | 1.882 |

</details>

### Speed by profile (encode & iterate, ns per 1,000 points, Delta timestamps)

`decode` (opening a blob via `NewDecoder`+`Decode`) is omitted — it's dominated by header
parsing rather than codec, so it's a roughly flat cost regardless of which value codec is
chosen (see the codec-dependent [Decode Performance](#decode-performance) table above for exact
per-combo numbers on the main matrix; this profile data follows the same pattern). The real read
cost that varies by codec is **iterate** — a full sequential `All()` materialization over every
point. Allocs are per whole-blob encode (200 columns). **Bold** marks the fastest iterate on
that profile.
These are single-run numbers, which can move by 20–40% with code placement alone.
Where ALP and ALP-RLE are the same size, their columns are identical and decode through the same code,
so a gap between their iterate times there is noise;
[ALP-RLE on repeat-heavy data](#alp-rle-on-repeat-heavy-data) below has layout-averaged numbers.

| Profile | Codec | Encode ns/1k | Iterate ns/1k | Encode allocs/blob |
|---|---|---:|---:|---:|
| decimal_gauge_2dp | Raw | 7,482 | 5,753 | 34 |
|  | Gorilla | 10,490 | 5,545 | 34 |
|  | Chimp | 13,279 | 8,309 | 34 |
|  | ALP | 39,973 | 4,725 | 62 |
|  | ALP-RLE | 40,138 | **4,675** | 62 |
| decimal_gauge_4dp | Raw | 7,564 | 5,729 | 34 |
|  | Gorilla | 10,466 | 5,515 | 34 |
|  | Chimp | 13,379 | 8,283 | 34 |
|  | ALP | 49,271 | 5,139 | 221 |
|  | ALP-RLE | 50,048 | **4,696** | 221 |
| counter | Raw | 7,712 | 5,582 | 34 |
|  | Gorilla | 10,263 | 6,393 | 34 |
|  | Chimp | 9,568 | 5,901 | 34 |
|  | ALP | 19,424 | **4,788** | 44 |
|  | ALP-RLE | 21,789 | 5,954 | 44 |
| sparse_constant | Raw | 7,794 | 5,716 | 34 |
|  | Gorilla | 7,223 | 4,098 | 34 |
|  | Chimp | 7,266 | **3,998** | 34 |
|  | ALP | 30,198 | 4,626 | 76 |
|  | ALP-RLE | 41,198 | 5,221 | 81 |
| worst_case | Raw | 7,878 | 5,695 | 34 |
|  | Gorilla | 10,601 | **5,519** | 34 |
|  | Chimp | 13,603 | 8,370 | 34 |
|  | ALP | 61,357 | 5,721 | 202 |
|  | ALP-RLE | 61,653 | 5,979 | 203 |
| cal_2dp_hold50 | Raw | 7,606 | 5,688 | 34 |
|  | Gorilla | 12,025 | 8,071 | 34 |
|  | Chimp | 13,812 | 8,515 | 34 |
|  | ALP | 42,824 | 6,049 | 56 |
|  | ALP-RLE | 60,114 | **5,135** | 65 |

### ALP-RLE on repeat-heavy data

ALP-RLE (`format.TypeALPRLE`) is ALP with a run-length front end.
For each column, the encoder keeps a runs layout (a run-start bitmap plus one ALP value per run)
only when it is smaller than the plain ALP column, so each uncompressed column is never larger than under ALP.
It targets the one shape where ALP lost to Chimp and Gorilla above: columns where many points repeat the previous value.
When to pick it, and its compatibility cost, are in [Best Practices § ALP or ALP-RLE?](best_practices.md#alp-or-alp-rle).

**Provenance:** unlike the tables above, these numbers use 100 metrics × 150 points with shared DeltaPacked timestamps,
no compression and no tags, matching a production-like blob.
Sizes are whole-blob bytes/point from `tests/measurev2` (deterministic, seed 42).
Speeds are layout-averaged to remove code-placement noise: 4 code layouts × 3 rounds, medians of n = 12,
`taskset -c 6`, `-test.cpu 1`, AMD Ryzen 9 9950X3D, Go 1.26.7, 2026-10-04;
benchmarks `BenchmarkALPRuns_*` in `internal/encoding/value/alp/alp_runs_bench_test.go`
and `BenchmarkALPRLEGate_*` in `blob/numeric_alp_bench_test.go`.
The design, the gates these numbers were judged against, and the method are in
[`specs/alp-rle-design.md`](specs/alp-rle-design.md).

Reproduce the sizes with:

```bash
cd tests/measurev2
for p in decimal_gauge_2dp counter sparse_constant worst_case \
         cal_2dp_hold30 cal_2dp_hold50 cal_2dp_hold70 \
         cal_2dp_step0.005 cal_1dp_step0.03 cal_1dp_step0.01; do
  go run . -profile "$p" -metrics 100 -points 150 -pretty -output "results/prod_$p.json"
done
```

Each row reads the `shared-deltapacked-*` entries.

| profile | chimp | gorilla | alp | alp-rle | vs ALP | vs Chimp |
|---|---:|---:|---:|---:|---:|---:|
| `decimal_gauge_2dp` | 6.22 | 6.58 | 1.73 | 1.73 | +0.0% | −72.1% |
| `counter` | 2.07 | 1.72 | 1.50 | 1.50 | +0.0% | −27.6% |
| `sparse_constant` | 0.74 | 0.64 | 1.24 | 0.46 | −62.9% | −37.7% |
| `worst_case` | 6.38 | 6.60 | 6.51 | 6.51 | +0.0% | +2.0% |
| `cal_2dp_hold30` | 4.52 | 4.69 | 1.73 | 1.43 | −17.0% | −68.3% |
| `cal_2dp_hold50` | 3.38 | 3.44 | 1.68 | 1.12 | −33.1% | −66.8% |
| `cal_2dp_hold70` | 2.18 | 2.17 | 1.62 | 0.82 | −49.6% | −62.6% |
| `cal_2dp_step0.005` | 4.42 | 4.47 | 0.95 | 0.90 | −4.7% | −79.6% |
| `cal_1dp_step0.03` | 4.01 | 4.08 | 0.83 | 0.79 | −4.9% | −80.3% |
| `cal_1dp_step0.01` | 2.07 | 2.01 | 0.63 | 0.53 | −15.9% | −74.3% |

- No profile is larger than plain ALP; profiles without runs are byte-identical to it.
- `sparse_constant`, where ALP was 1.7–1.9× larger than Chimp and Gorilla, is now smaller than both.
- `worst_case` (full-precision, no repeats) stays at plain ALP, slightly larger than Chimp.

Speed on a 2-decimal gauge where half the points repeat (the `cal_2dp_hold50` shape, from the benchmarks' own generator), 100 metrics × 150 points.
The encode row without forced repeats uses the same gauge with no holds; no column there takes the runs layout.

| measurement | level | Chimp | ALP | ALP-RLE | ALP-RLE vs ALP | ALP-RLE vs Chimp |
|---|---|---:|---:|---:|---:|---:|
| `DecodeAll`, ns/point | codec | 2.67 | 0.55 | 0.91 | 1.64× | 2.9× faster |
| `At`, ns/lookup | codec | 416 | 7.3 | 13.1 | 1.80× | 32× faster |
| `ValueAt`, ns/lookup | blob | 437 | 28.8 | 32.7 | 1.14× | 13× faster |
| `ForEachValues`, ns/point | blob | 4.82 | 2.91 | 3.18 | 1.09× | 1.5× faster |
| `Materialize`, ns/point | blob | 4.48 | 2.18 | 2.56 | 1.17× | 1.8× faster |
| encode, half the points repeat, µs/blob | blob | 138 | 774 | 1,186 | 1.53× | 8.6× slower |
| encode, no forced repeats, µs/blob | blob | 177 | 779 | 786 | 1.01× | 4.4× slower |

- Every read path is faster than Chimp's.
  ALP-RLE decodes in two passes (the nested ALP column, then the run expansion), so its `DecodeAll` takes 1.64× plain ALP's time.
- Encoding costs more: a runs column needs a second ALP encode of the run values.
  On data without repeats, the encoder skips that attempt and the cost is about 1% over ALP.
- With value compression (Zstd, S2, LZ4) the codec runs over the whole value payload,
  and a smaller input is not guaranteed to compress smaller.

### Takeaways

- **The ALP family is the compression champion on decimal & counter data**:
  1.05–2.53× smaller than the next-best non-ALP codec on this run (see the per-profile ratios above),
  and 4.2–6.2× smaller than the Raw+Raw baseline.
  After the July 2026 encode/decode optimization passes
  (see [`alp_optimization_history.md`](perf/alp_optimization_history.md)),
  it also iterates fastest of any codec on those 3 profiles (decimal_gauge_2dp, decimal_gauge_4dp, counter).
- **ALP-RLE is the smallest codec on every profile with repeats**,
  including `sparse_constant`, where plain ALP was larger than Chimp and Gorilla;
  on the other profiles it is byte-identical to ALP.
- **The ALP family is NOT the fastest to iterate on every profile**:
  on `sparse_constant` and `worst_case`, Gorilla or Chimp iterates faster in this run.
  Its decode-side wins are real but shape-dependent, same as its compression wins.
- **ALP's encode is still the slowest by a wide margin**:
  ~2.0–4.5× Chimp's encode cost across the five original profiles
  (lowest gap on `counter` at 2.03×, widest on `worst_case` at 4.51×).
  Its per-column (e,f) search is real CPU work, not yet vectorized,
  and ALP-RLE adds a second ALP encode on columns that take the runs layout.
  Fine for batch/offline encoding; a poor fit if encode latency is on a hot path.
- **ALP and ALP-RLE are also the far better pick for random access**
  (see [Random Access Performance](#random-access-performance) above).
  ALP's `ValueAt` is O(1) + O(log k) (k = exceptions in the column),
  measured at 6,400–8,300 ns/op on the main matrix's 200-point columns.
  Gorilla/Chimp `ValueAt` is O(index), a sequential XOR-chain decode from the start of the column,
  measured at 173,000–205,000 ns/op on the same columns:
  **21–32× slower than ALP** with the same timestamp setup.
  The complexity classes hold for any data, but the multiplier depends on it:
  on `sparse_constant`, where long repeats make the XOR chain cheap to walk, Gorilla/Chimp `ValueAt` is 5–11× slower than ALP.
  Raw is fastest of all (~4,700–5,500 ns/op) but has none of ALP's compression.
- **Gorilla and Chimp remain cheap to encode** and iterate fastest on sparse data,
  but ALP-RLE is now smaller there.
- **Chimp** narrowly wins full-precision size; otherwise similar to Gorilla.
- **Choosing ALP or ALP-RLE blindly is still a trap on the wrong shape**:
  on `worst_case` (genuinely full-precision data with no repeats) both are *larger* than Chimp.
  ALP pays off where the data is decimal-quantized;
  ALP-RLE can also shrink full-precision columns with many repeats, but this profile has none.
- This data-dependence is the empirical case for **per-column adaptive value-codec selection**,
  with Raw kept as a hard floor.
  See [`adaptive_selector_experiments.md`](perf/adaptive_selector_experiments.md)
  and the [implementation plan](plans/2026-06-15-adaptive-value-codec-selection.md);
  it is not yet wired and is tracked as follow-up work.

## Choosing an Encoding Strategy

### Decision Tree

```
What is your priority?
├─ Smallest encoded size?
│  ├─ All metrics share timestamps? → Shared Delta + Chimp (6.349 BPP, 60.5% savings)
│  └─ Independent timestamps?      → Delta + Chimp (8.297 BPP, 48.4% savings)
│
├─ Fastest encode?
│  └─ Raw + Raw (330,719 ns/op, 16.081 BPP)
│
├─ Fastest iteration / decode?
│  ├─ Sequential scan → Raw + ALP (198,948 ns/op)
│  └─ Random access  → Shared Raw + Raw (ValueAt 4,684 ns/op [O(1)], TimestampAt 4,304 ns/op [O(1), cached when the blob is opened])
│
└─ Best balance (size + speed)?
   ├─ With shared TS → Shared Delta + Chimp (6.349 BPP, 394,402 ns/op iter)
   └─ Without        → Delta + Chimp (8.297 BPP, 351,583 ns/op iter)
```

### Configuration Selection

| Use Case | Configuration | Key Metric | Rationale |
|----------|---------------|------------|-----------|
| **Best compression** | Shared Delta + Chimp | 6.349 BPP (60.5% savings) | Lowest bytes/point; shared timestamps eliminate redundant storage |
| **Fastest iteration** | Raw + ALP | 198,948 ns/op | Fastest sequential scan of any combo tested |
| **Fastest encode** | Raw + Raw | 330,719 ns/op | No delta/XOR/digit computation, just a byte copy |
| **Best balance** | Shared DeltaPacked + Chimp | 6.350 BPP, 425,515 ns/op iter | Second-best compression; no combo ranked in the top 5 for both size and iteration speed this run, so this favors compression — its iteration speed (425,515 ns/op) is not notable |
| **Random access** | Shared Raw + Raw | ValueAt 4,684 ns/op, TimestampAt 4,304 ns/op | Value: O(1); Timestamp: O(1), cached when the blob is opened |
| **Maximum throughput** | Raw + Raw | 330,719 ns/op encode | Baseline; no encoding overhead but largest output |

### Points-per-Metric Guidelines

Using Shared Delta + Chimp scaling data (converged: 6.349 bytes/point):

| Zone | PPM Range | BPP Range | Overhead | Recommendation |
|------|-----------|-----------|----------|----------------|
| **Poor** | 1–2 | 26.220–16.617 | 162–313% | Batch more points if possible; fixed overhead dominates |
| **Moderate** | 5–10 | 10.371–8.303 | 31–63% | Acceptable for low-frequency metrics |
| **Good** | 20 | 7.280 | 15–15% | Good efficiency; recommended minimum for most use cases |
| **Optimal** | 50–200 | 6.661–6.349 | 0–5% | Excellent efficiency; diminishing returns beyond this range |
