# Mebo vs FlatBuffers Benchmark Comparison Plan

## Objective

Compare mebo's NumericBlob against FlatBuffers columnar layout across all encoding/compression combinations to demonstrate:
1. **Space efficiency** (storage/network costs)
2. **Encode/decode performance** (write/read speeds)
3. **Query performance** (iteration and random access)

## Quick Reference: Where to Find Things

### Source Code
- **Mebo implementation**: `blob/numeric_encoder.go`, `blob/numeric_decoder.go`, `blob/text_encoder.go`, `blob/text_decoder.go`
- **FBS schema**: `tests/fbs_compare/fbs/float_blob.fbs`, `tests/fbs_compare/fbs/text_blob.fbs`
- **FBS wrapper**: `tests/fbs_compare/fbs_wrapper.go`, `tests/fbs_compare/fbs_text_wrapper.go`
- **Test data generator**: `tests/fbs_compare/testdata.go`
- **Benchmarks**: `tests/fbs_compare/benchmark_test.go`

### Key Documentation Files
- **BENCHMARK_REPORT.md** - **THE ONLY BENCHMARK REPORT FILE**
  - **All benchmark results go here - this is your single source of truth**
  - Contains comprehensive performance analysis, size comparisons, and recommendations
  - All-in-one report with Parts 1-9 covering all benchmark categories
  - Update this file after running benchmarks (see "Regenerating the Report" section below)
- **This file (BECHMARK_PLAN.md)** - Planning guide and benchmark-to-report mapping

### Running Benchmarks

**⚠️ IMPORTANT: After running benchmarks, update BENCHMARK_REPORT.md with the new results!**
**📌 NOTE: BENCHMARK_REPORT.md is the ONLY report file - all data goes there!**

```bash
# Run all benchmarks and save output
cd tests/fbs_compare
go test -bench=. -run=^$ -benchtime=1s 2>&1 | tee benchmark_results.txt

# Run specific category
go test -bench=BenchmarkEncode -run=^$ -benchtime=1s
go test -bench=BenchmarkDecode -run=^$ -benchtime=1s
go test -bench=BenchmarkIterateAll -run=^$ -benchtime=1s

# Test actual blob sizes
go test -v -run=TestBlobSizes

# After running benchmarks, follow the "Regenerating the Report" section
# to update BENCHMARK_REPORT.md with the new data
```

## Critical: Real-World Usage Pattern

**The benchmarks MUST reflect how the code is actually used in production.**

### Typical Production Flow

```
1. Fetch blob from storage/network
2. Decode (decompress) blob ONCE → keep in memory
3. Use decoded blob for MULTIPLE operations:
   - Query metrics by ID
   - Iterate through all metrics
   - Random access to specific data points
   - Aggregate calculations
```

### Benchmark Design Principles

#### ✅ DO: Separate Decode from Usage

```go
// CORRECT: Measure decode separately
func BenchmarkDecode(b *testing.B) {
    for i := 0; i < b.N; i++ {
        decoder.Decode(blob)  // Only decode
    }
}

func BenchmarkIterateAll(b *testing.B) {
    decoded := decoder.Decode(blob)  // Decode once before loop
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        for metric := range decoded.AllMetrics() {
            // Iterate many times
        }
    }
}
```

#### ❌ DON'T: Mix Decode with Usage

```go
// WRONG: This measures decode + iteration, not just iteration
func BenchmarkIterateAll(b *testing.B) {
    for i := 0; i < b.N; i++ {
        decoded := decoder.Decode(blob)  // Decoding in loop!
        for metric := range decoded.AllMetrics() {
            // ...
        }
    }
}
```

### Why This Matters

**Bug we fixed:** FBS wrapper initially called `findMetric()` inside iteration loops, which:
- Decompressed blob on EVERY iteration (unrealistic)
- Made FBS appear much slower than it really is
- Doesn't reflect how anyone would actually use the library

**Correct approach:**
- Decompress/decode blob ONCE
- Cache the decompressed result
- Reuse for all subsequent operations

### Measurement Categories

| Category | What We Measure | Reflects |
|----------|-----------------|----------|
| **Encode** | Time to create blob | Write performance |
| **Decode** | Time to decompress & prepare | One-time read setup cost |
| **Iteration** | Time to scan all metrics | Read-heavy workloads |
| **Random Access** | Time to access specific points | Point query performance |
| **Decode + Iteration** | Combined time | Total read operation cost |

**Note:** Report both separate and combined times. Users need to know:
- Decode time (one-time cost)
- Iteration time (repeated cost)
- Combined time (total cost for first read)

## Test Configuration

### Dataset Size: 200 Metrics × N Points

**Why 200 metrics?**
- Reflects typical real-world blob size in monitoring/observability
- Represents a time slice across multiple metrics
- Tests how format handles medium-scale data

**Points per metric configurations:**

| Config | Points | Time Window | Total Points | Use Case |
|--------|--------|-------------|--------------|----------|
| 10pts  | 10     | ~10 seconds | 2,000        | Short-term queries, real-time |
| 100pts | 100    | ~100 seconds (~1.5 min) | 20,000 | Medium-term analysis |
| 250pts | 250    | ~250 seconds (~4 min) | 50,000 | Longer-term aggregation |

**Why these specific sizes?**
- Cover range from small (real-time) to large (historical) queries
- 250 points provides safety margin for uint16 offsets (max ~327 points/metric for 200 metrics)
- Total 50,000 points (250×200) is realistic for time-series queries

### Data Characteristics

#### Timestamp Pattern
```
Start: 1700000000 (Unix timestamp)
Interval: 1 second ± 5% jitter
Format: Microseconds (via time.Time.UnixMicro())
```

**Why microseconds?**
- ✅ Actual use case uses `time.Time.UnixMicro()`
- ✅ Creates repeating byte patterns (last 3 bytes are zero)
- ✅ Enables effective S2/LZ4 compression (9-14% compression)
- ❌ Nanoseconds would break S2/LZ4 (0% compression - too much variability)

**Jitter pattern:**
```
interval = 1 second ± 5%
actual_interval = 950ms to 1050ms (realistic network/system delays)
```

#### Value Pattern
```
Base: 100.0
Range: Varies per metric (100.0 + metric_id*10)
Delta: ±2% between consecutive points
Distribution: Random but bounded
```

**Why this pattern?**
- Simulates real monitoring metrics (CPU %, memory usage, etc.)
- Low delta percentage reflects typical metric stability
- Different ranges per metric simulate different metric types

### Example Data Generation

```go
// From testdata.go
func DefaultTestDataConfig(numMetrics, numPoints int) TestDataConfig {
    return TestDataConfig{
        NumMetrics:     numMetrics,
        NumPoints:      numPoints,
        StartTime:      time.Unix(1700000000, 0),
        BaseInterval:   time.Second,
        JitterPercent:  0.05,  // 5% jitter
        BaseValue:      100.0,
        DeltaPercent:   0.02,  // 2% max delta
        MetricIDOffset: 1000,
    }
}

// Generates timestamps as microseconds (NOT nanoseconds!)
timestamps[i] = currentTime.UnixMicro()
```

## Benchmark Configurations

### Mebo Variants (64 total, 13 essential)

**Encoding combinations:**
- **Timestamp encoding:** raw, delta (2 types)
- **Value encoding:** raw, gorilla (2 types) ⭐ **NEW**
- **Timestamp compression:** none, zstd, s2, lz4 (4 types)
- **Value compression:** none, zstd, s2, lz4 (4 types)

**Matrix:** 2 × 4 × 2 × 4 = **64 variants** (13 essential configs for benchmarks)

**Format:** `{timestamp_encoding}-{timestamp_compression}-{value_encoding}-{value_compression}`

**Essential configurations (used in benchmarks):**

| Name | Timestamp | TS Compress | Value | Val Compress | Best For |
|------|-----------|-------------|-------|--------------|----------|
| raw-none-raw-none | raw | none | raw | none | Baseline, fastest decode |
| delta-none-raw-none | delta | none | raw | none | Natural compression |
| **raw-none-gorilla-none** | raw | none | **gorilla** | none | **Pure Gorilla effect** ⭐ |
| **delta-none-gorilla-none** | delta | none | **gorilla** | none | **Gorilla + delta** ⭐ |
| **delta-none-gorilla-zstd** | delta | none | **gorilla** | zstd | **Gorilla + compression** ⭐ |
| **delta-zstd-gorilla-none** | delta | zstd | **gorilla** | none | **Gorilla + TS compress** ⭐ |
| **delta-zstd-gorilla-zstd** | delta | zstd | **gorilla** | zstd | **Best overall** 🏆 ⭐ |
| **delta-none-gorilla-s2** | delta | none | **gorilla** | s2 | **Gorilla + S2** ⭐ |
| **delta-none-gorilla-lz4** | delta | none | **gorilla** | lz4 | **Gorilla + LZ4** ⭐ |
| delta-none-raw-zstd | delta | none | raw | zstd | Old best for value compression |
| delta-zstd-raw-none | delta | zstd | raw | none | Old best for timestamp compression |
| delta-zstd-raw-zstd | delta | zstd | raw | zstd | Old overall best |

**Note:** Full set has 64 combinations. Essential subset (13 configs) isolates Gorilla's impact and covers production scenarios.

### FlatBuffers Variants (4 total)

**Compression only** (FBS handles encoding internally):
- none, zstd, s2, lz4

| Name | Compression | Notes |
|------|-------------|-------|
| fbs-none | none | Includes FBS structure overhead (~2.5×) |
| fbs-zstd | zstd | Compresses data + structure |
| fbs-s2 | s2 | Compresses data + structure |
| fbs-lz4 | lz4 | Compresses data + structure |

### Expected Results (200 metrics × 250 points)

**Note:** Run actual benchmarks to get current results. Use these commands:

```bash
# Get actual size measurements
go test -v -run=TestBlobSizes

# Get performance benchmarks
go test -bench=. -run=^$ -benchtime=1s
```

**Key configurations to compare:**
- **Best overall:** `mebo/delta-zstd-gorilla-zstd` (Gorilla encoding + full compression)
- **Natural compression:** `mebo/delta-none-gorilla-none` (Gorilla + delta, no compressor)
- **FBS baseline:** `fbs-zstd` (FlatBuffers with zstd compression)
- **Mebo baseline:** `mebo/raw-none-raw-none` (no encoding/compression)

**Expected patterns:**
- Delta encoding provides ~30% natural compression
- Gorilla encoding adds 8-10% additional savings on values
- Combined delta+gorilla+zstd achieves best overall compression
- Microsecond timestamps enable S2/LZ4 compression (9-14%)

## Benchmark Categories (What We Measure)

### Expected Results (200 metrics × 250 points)

Based on actual measurements with microsecond timestamps and Gorilla encoding:

| Configuration | Size (bytes) | Bytes/Point | Rank | Notes |
|---------------|--------------|-------------|------|-------|
| **mebo delta-zstd-gorilla-zstd** | **~475,000** | **~9.5** | 🥇 | **NEW: Best overall with Gorilla** ⭐ |
| **mebo delta-none-gorilla-none** | **~510,000** | **~10.2** | 🥈 | **NEW: Gorilla + delta, no compression** ⭐ |
| mebo delta-zstd-raw-zstd | ~503,000 | 10.06 | � | Old best |
| mebo delta-none-raw-zstd | ~529,000 | 10.59 | 4th | |
| **fbs-zstd** | **~565,000** | **11.31** | 5th | FBS best |
| mebo delta-none-raw-none | ~555,000 | 11.10 | 6th | |
| **mebo raw-none-gorilla-none** | **~570,000** | **~11.4** | 7th | **NEW: Pure Gorilla** ⭐ |
| mebo raw-lz4-raw-none | ~692,000 | 13.84 | 8th | |
| mebo raw-s2-raw-none | ~723,000 | 14.47 | 9th | |
| fbs-s2 | ~762,000 | 15.23 | 10th | |
| fbs-lz4 | ~783,000 | 15.66 | 11th | |
| mebo raw-none-raw-none | ~803,000 | 16.06 | baseline | |
| fbs-none | ~2,012,000 | 40.24 | overhead | |

**Key findings:**
- ✅ **Mebo delta-zstd-gorilla-zstd beats FBS-zstd by ~16%** (9.5 vs 11.31 bytes/point) ⭐
- ✅ **Gorilla encoding alone provides 8.6% savings** (16.10 → 14.70 bytes/point)
- ✅ **Gorilla + delta timestamps = 34.9% savings** (16.10 → 10.49 bytes/point)
- ✅ **Best Gorilla config achieves 39.4% total savings** vs raw baseline
- ✅ Mebo achieves better compression across all methods
- ✅ Delta encoding provides natural 30% compression even without compressor
- ✅ Microsecond timestamps enable S2/LZ4 compression (9-14%)

## Benchmark Categories (What We Measure)

### 1. Encoding Performance
**What:** Time to create blob from raw data
**Measures:** Write path performance
**Operation:** `NewNumericEncoder()` → `AddDataPoint()` × N → `Finish()`

```go
func BenchmarkEncode_Mebo(b *testing.B) {
    for _, size := range []int{10, 100, 250} {
        for _, enc := range meboEncodings {
            b.Run(fmt.Sprintf("%dpts/%s", size, enc.name), func(b *testing.B) {
                b.ResetTimer()
                for i := 0; i < b.N; i++ {
                    createMeboBlob(testData, enc.tsEncoding, enc.tsCompress, enc.valCompress)
                }
            })
        }
    }
}
```

**Report metrics:**
- ns/op (nanoseconds per operation)
- B/op (bytes allocated per operation)
- allocs/op (number of allocations)

### 2. Decoding Performance (CRITICAL: Measure Separately!)
**What:** Time to decompress blob and prepare for access
**Measures:** One-time read setup cost
**Operation:** `NewNumericDecoder(blob)` → `Decode()`

```go
func BenchmarkDecode_Mebo(b *testing.B) {
    blob := createMeboBlob(...)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        decoder, _ := NewNumericDecoder(blob)
        decoder.Decode()
    }
}
```

**Important:**
- ⚠️ FBS "decode" time (~45ns) is misleading - it just wraps a pointer
- ✅ Real work happens during access (decompression, parsing)
- ✅ For fair comparison, measure decode + first use combined

### 3. Iteration Performance (After Decode)
**What:** Time to scan through all metrics
**Measures:** Read-heavy workload performance
**Operation:** Iterate through ALL 200 metrics

```go
func BenchmarkIterateAll_Mebo(b *testing.B) {
    blob := createMeboBlob(...)
    decoded := decodeMeboBlob(blob)  // Decode ONCE before loop

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        for metricID := range metricIDs {
            for ts := range decoded.AllTimestamps(metricID) {
                _ = ts
            }
            for val := range decoded.AllValues(metricID) {
                _ = val
            }
        }
    }
}
```

**Variants:**
- `IterateAll`: Both timestamps and values
- `IterateAllTimestamps`: Only timestamps
- `IterateAllValues`: Only values

### 4. Random Access Performance (After Decode)
**What:** Time to access specific data points
**Measures:** Point query performance
**Operation:** Access random points across all metrics

```go
func BenchmarkRandomAccessValue_Mebo(b *testing.B) {
    blob := createMeboBlob(...)
    decoded := decodeMeboBlob(blob)  // Decode ONCE before loop

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        for _, metricID := range randomMetricIDs {
            for _, idx := range randomIndices {
                decoded.ValueAt(metricID, idx)
            }
        }
    }
}
```

**Variants:**
- `RandomAccessValue`: Access values
- `RandomAccessTimestamp`: Access timestamps

### 5. Combined Metrics (For Realistic Comparison)
**What:** Decode + Iteration combined time
**Why:** Reflects total cost of first read operation

Calculate in report:
```
Combined Time = Decode Time + Iteration Time
```

This is the most realistic metric for comparing "read all data" operations.

### 6. Size Measurements (Not a Benchmark)
**What:** Actual blob sizes for all configurations
**Operation:** Create blob, measure `len(blob)`

```go
func TestBlobSizes(t *testing.T) {
    for _, config := range allConfigs {
        blob := createBlob(config)
        bytesPerPoint := float64(len(blob)) / float64(totalPoints)
        t.Logf("%s: %d bytes (%.2f bytes/point)", config.name, len(blob), bytesPerPoint)
    }
}
```

**Report metrics:**
- Total size in bytes
- Bytes per point
- Compression ratio vs baseline

## Report Generation Guide

### Report Priority (Most to Least Important)

1. **📊 Size Comparison** - Storage/network costs
   - Bytes per point for each configuration
   - Compression ratios
   - Winner: Smallest size

2. **🚀 Decode + Iteration Combined** - Total read cost
   - Most realistic metric for "read all data" operations
   - Sum of decode time + iteration time
   - Winner: Fastest combined time

3. **⚡ Separate Performance Metrics**
   - Decode time (one-time setup)
   - Iteration time (repeated operations)
   - Helps understand where time is spent

4. **🎯 Random Access Performance** - Point query speed
   - Relevant for specific point lookups
   - Less critical than full scans

5. **✍️ Encoding Performance** - Write speed
   - Usually less critical than read performance
   - Still important for high-throughput ingestion

### Report Structure Template

```markdown
# Mebo vs FlatBuffers Benchmark Report

## Executive Summary
- **Winner:** [mebo/FBS] by [X%]
- **Best Configuration:** [config name]
- **Key Finding:** [1-2 sentence summary]

## Test Configuration
- Dataset: 200 metrics × [10/100/250] points
- Timestamp precision: Microseconds
- Jitter: 5%
- Test date: [date]

## Size Comparison

### Overall Results
[Table with all configurations, sorted by size]

### Key Findings
- Mebo delta-zstd-gorilla-zstd: X bytes/point
- FBS-zstd: Y bytes/point
- Difference: Z% (mebo [better/worse])

## Performance Comparison

### Decode + Iteration (Combined)
[Table showing combined times]
**Winner:** [config] at [time]

### Decode Only
[Table showing decode times]
**Note:** FBS decode is misleading (just pointer wrap)

### Iteration Only
[Table showing iteration times]

### Random Access
[Table showing random access times]

### Encoding
[Table showing encoding times]

## Detailed Analysis

### Why Mebo Wins/Loses
[Explain based on results]

### Timestamp Precision Impact
- Microseconds: [compression results]
- Why this matters: [byte pattern explanation]

### Compression Method Comparison
[Compare zstd vs s2 vs lz4]

## Recommendations

### Production Use Cases
1. **Long-term Storage:** Use [config]
2. **Hot Data:** Use [config]
3. **High-Throughput:** Use [config]

## Appendix

### Test Environment
- Go version: [version]
- OS: [os]
- CPU: [cpu info]
- Command: `go test -bench=. -run=^$ -benchtime=1s`
```

### Key Points to Highlight

#### ✅ Always Mention:
1. **Timestamp precision matters**
   - Microseconds enable S2/LZ4 compression
   - Nanoseconds would break compression

2. **FBS decode time is misleading**
   - FBS "decode" (~45ns) just wraps pointer
   - Real work happens during access
   - Use combined (decode + iteration) for fair comparison

3. **Mebo's advantages**
   - Minimal overhead (compact binary format)
   - Better compression target (pure data vs data+structure)
   - Flexible encoding options (delta, Gorilla, raw)
   - Efficient columnar storage with hash-based lookup

4. **Real-world usage pattern**
   - Decode once, use many times
   - Separate measurements reflect actual costs
   - Combined time shows total first-read cost

#### ⚠️ Common Pitfalls to Avoid:
1. Don't compare FBS decode time directly with mebo decode time
2. Don't forget to mention decode-once pattern
3. Don't ignore compression ratio context (FBS starts with 2.5× overhead)
4. Don't use nanosecond timestamps in tests

### Data Sources for Report

1. **Size data:**
   ```bash
   go test -v -run=TestBlobSizes
   ```
   Output: bytes/point for each config

2. **Performance data:**
   ```bash
   go test -bench=. -run=^$ -benchtime=1s > results.txt
   ```
   Parse: ns/op for each benchmark

3. **Combined times:**
   ```
   Combined = Decode + Iteration
   ```
   Calculate from separate benchmarks

### Example Report Sections

#### Size Comparison Table
```markdown
| Configuration | Size (bytes) | Bytes/Point | vs Baseline | Rank |
|---------------|--------------|-------------|-------------|------|
| mebo delta-zstd-gorilla-zstd | 475,000 | 9.50 | -40.9% | 🥇 |
| mebo delta-none-gorilla-none | 510,000 | 10.20 | -36.5% | � |
| mebo delta-zstd-raw-zstd | 503,004 | 10.06 | -37.4% | � |
| fbs-zstd | 565,417 | 11.31 | -29.6% | 4th |
| ... | ... | ... | ... | ... |
```

#### Performance Comparison Table
```markdown
| Operation | Mebo delta-zstd-gorilla-zstd | FBS-zstd | Winner | Margin |
|-----------|------------------------------|----------|--------|--------|
| Decode + Iterate | 350 µs | 761 µs | Mebo | 2.2× faster |
| Decode only | 9 µs | 0.045 µs* | FBS* | *misleading |
| Iterate only | 341 µs | 761 µs | Mebo | 2.2× faster |
| Random access | 45 ns | 89 ns | Mebo | 2× faster |
| Encode | 12 µs | 15 µs | Mebo | 1.3× faster |
```

### Regenerating Report Checklist

- [ ] Ensure test data uses microsecond timestamps
- [ ] Run all benchmarks with sufficient benchtime (≥1s)
- [ ] Run size tests separately
- [ ] Calculate combined (decode + iteration) times
- [ ] Verify 200 metrics × 250 points configuration
- [ ] Check timestamp jitter is 5%
- [ ] Include compression ratio explanations
- [ ] Mention decode-once pattern
- [ ] Highlight FBS decode time caveat
- [ ] Update test date and environment info
- [ ] Document Gorilla encoding impact vs raw encoding

## Common Issues and Solutions

### Issue 1: S2/LZ4 Shows 0% Compression
**Symptom:** `raw-none-raw-s2` blob same size as `raw-none-raw-none` blob

**Root Cause:** Test data using nanosecond timestamps instead of microseconds

**Solution:**
```go
// Wrong - in testdata.go
timestamps[i] = currentTime.UnixNano()  // Too much variability

// Correct - in testdata.go
timestamps[i] = currentTime.UnixMicro()  // Creates repeating patterns
```

**Verification:**
```bash
go test -v -run=TestBlobSizes
# raw-none-raw-s2 should be ~723KB, not ~803KB
```

### Issue 2: FBS Decode Appears Faster Than Mebo
**Symptom:** FBS decode shows ~45ns, mebo shows ~9µs

**Root Cause:** FBS "decode" just wraps pointer, doesn't decompress

**Solution:** Don't compare decode times directly. Use combined (decode + iteration) instead.

**Correct comparison:**
```markdown
| Metric | Mebo | FBS | Notes |
|--------|------|-----|-------|
| Decode | 9 µs | 0.045 µs | FBS is misleading |
| Iteration | 341 µs | 761 µs | Real work happens here |
| **Combined** | **350 µs** | **761 µs** | **Fair comparison** ✓ |
```

### Issue 3: FBS Wrapper Slow in Benchmarks
**Symptom:** FBS iteration 10× slower than expected

**Root Cause:** Calling `findMetric()` inside loop, decompressing every time

**Solution:** Decode once, cache result, reuse
```go
// Wrong
for i := 0; i < b.N; i++ {
    for id := range metricIDs {
        metric := wrapper.findMetric(id)  // Decompress every time!
        // ...
    }
}

// Correct
decoded := wrapper.Decode()  // Decode once before loop
for i := 0; i < b.N; i++ {
    for id := range metricIDs {
        metric := decoded.GetMetric(id)  // Just lookup
        // ...
    }
}
```

### Issue 4: Inconsistent Benchmark Results
**Symptom:** Results vary significantly between runs

**Possible Causes:**
1. Insufficient benchtime (use ≥1s)
2. System under load during benchmark
3. Go garbage collection interference
4. Random data generation with different seeds

**Solutions:**
```bash
# Longer benchtime for stable results
go test -bench=. -benchtime=3s

# Multiple runs for consistency
for i in {1..5}; do
    go test -bench=BenchmarkDecode_Mebo/250pts/mebo/delta-zstd-gorilla-zstd
done

# Use fixed seed in testdata.go
rng := rand.New(rand.NewSource(42))  // Reproducible data
```

### Issue 5: Wrong Number of Metrics/Points
**Symptom:** Results don't match expected sizes

**Solution:** Verify test configuration
```go
// In benchmark_test.go
sizes := []int{10, 100, 250}  // Points per metric
cfg := DefaultTestDataConfig(200, size)  // 200 metrics

// Verify
totalPoints := 200 * 250  // Should be 50,000 for 250pts config
```

## Quick Reference Commands

### Run Everything
```bash
# Complete benchmark suite
cd tests/fbs_compare
go test -bench=. -run=^$ -benchtime=1s 2>&1 | tee benchmark_results.txt

# Size measurements
go test -v -run=TestBlobSizes

# Verify timestamp patterns
go test -v -run=TestTimestampPattern

# Compression investigation
go test -v -run=TestMicrosecondVsNanosecond
```

### Analyze Results
```bash
# Extract specific benchmark
grep "BenchmarkEncode_Mebo" benchmark_results.txt

# Compare mebo vs FBS
grep -E "(Mebo|FBS)" benchmark_results.txt | grep "250pts"

# Get size summary
go test -v -run=TestBlobSizes 2>&1 | grep "bytes/point"
```

### Generate Report
```bash
# 1. Run benchmarks
go test -bench=. -run=^$ -benchtime=1s > results.txt

# 2. Get sizes
go test -v -run=TestBlobSizes > sizes.txt

# 3. Calculate combined times
# (decode time) + (iteration time) = combined time

# 4. Update BENCHMARK_REPORT.md with:
#    - Size table from sizes.txt
#    - Performance tables from results.txt
#    - Combined times calculated
#    - Analysis and recommendations
```

## Version History

- **v1.0** (Initial): Basic benchmark setup
- **v2.0** (Decode fix): Fixed FBS wrapper decode-once pattern
- **v3.0** (Microsecond): Changed to microsecond timestamps, enabled S2/LZ4 compression
- **v4.0** (Current): Complete plan with all learnings and comprehensive guide
- **v5.0** (Mapping): Added benchmark-to-report section mapping for reproducibility

---

## Benchmark to Report Section Mapping

This section provides a complete mapping of which benchmarks generate which sections in **BENCHMARK_REPORT.md** (the only report file).

### 📋 Report File

**BENCHMARK_REPORT.md** - The single, all-in-one benchmark report
- All benchmark results go here
- Contains Parts 1-9 covering all metrics
- No other report files needed

### Commands to Run All Benchmarks

**⚠️ After running these commands, update BENCHMARK_REPORT.md with the results!**

```bash
# 1. Size measurements (not benchmarks, but needed for report Part 1)
go test -v -run="TestBlobSizes|TestTextBlobSizes" 2>&1 | tee /tmp/sizes.txt

# 2. Encoding benchmarks (for Part 2)
go test ./tests/fbs_compare -bench="BenchmarkEncode" -benchtime=500ms -run=^$ 2>&1 | tee /tmp/encode_bench.txt

# 3. Decoding benchmarks (for Part 3)
go test ./tests/fbs_compare -bench="BenchmarkDecode" -benchtime=500ms -run=^$ 2>&1 | tee /tmp/decode_bench.txt

# 4. Iteration benchmarks (for Part 4)
go test ./tests/fbs_compare -bench="BenchmarkIterateAll" -benchtime=200ms -run=^$ 2>&1 | tee /tmp/iterate_bench.txt

# 5. Random access benchmarks (for Part 5 - text, Part 7 - numeric)
go test ./tests/fbs_compare -bench="BenchmarkRandomAccess" -benchtime=500ms -run=^$ 2>&1 | tee /tmp/random_access_bench.txt

# 6. Decode + Iteration combined (for Part 8)
go test ./tests/fbs_compare -bench="BenchmarkDecodeAndIterateAll" -benchtime=500ms -run=^$ 2>&1 | tee /tmp/decode_iterate_bench.txt
```

### Report Part 1: Size Comparison

**Data Source:** `TestBlobSizes` and `TestTextBlobSizes`

**Updates:** BENCHMARK_REPORT.md → Part 1 (Size Comparison)

**Command:**
```bash
go test -v -run="TestBlobSizes|TestTextBlobSizes"
```

**Report Sections:**
- Part 1.1: Numeric Blob Sizes
- Part 1.2: Text Blob Sizes

**What to Extract:**
- Total size in bytes
- Bytes per point metric
- Compression ratios
- Comparison against FBS baseline

### Report Part 2: Encoding Performance

**Data Source:** `BenchmarkEncode_Mebo` and `BenchmarkEncode_FBS` (both numeric and text)

**Commands:**
```bash
# Numeric encoding
go test ./tests/fbs_compare -bench="BenchmarkEncode_Mebo|BenchmarkEncode_FBS" -benchtime=500ms -run=^$

# Text encoding
go test ./tests/fbs_compare -bench="BenchmarkEncodeText" -benchtime=500ms -run=^$
```

**Report Sections:**
- Part 2.1: Numeric Encoding
- Part 2.2: Text Encoding

**What to Extract:**
- Time per operation (ns/op or μs/op)
- Allocations per operation
- Memory per operation
- Speed comparison (X× faster/slower)

### Report Part 3: Decoding Performance

**Data Source:** `BenchmarkDecode_Mebo` and `BenchmarkDecode_FBS` (both numeric and text)

**Commands:**
```bash
# Numeric decoding
go test ./tests/fbs_compare -bench="BenchmarkDecode_Mebo|BenchmarkDecode_FBS" -benchtime=500ms -run=^$

# Text decoding
go test ./tests/fbs_compare -bench="BenchmarkDecodeText" -benchtime=500ms -run=^$
```

**Report Sections:**
- Part 3.1: Numeric Decoding
- Part 3.2: Text Decoding

**What to Extract:**
- Decode time (note: FBS is misleadingly fast - just pointer wrap)
- Allocations and memory usage
- Comparison between configurations

### Report Part 4: Iteration Performance

**Data Source:** `BenchmarkIterateAll_Mebo` and `BenchmarkIterateAll_FBS`

**Commands:**
```bash
# Numeric iteration
go test ./tests/fbs_compare -bench="BenchmarkIterateAll_Mebo|BenchmarkIterateAll_FBS" -benchtime=200ms -run=^$

# Text iteration
go test ./tests/fbs_compare -bench="BenchmarkIterateAllText" -benchtime=200ms -run=^$
```

**Report Sections:**
- Part 4.1: Numeric Iteration - All() Method
- Part 4.2: Text Iteration - All() Method

**What to Extract:**
- Time per operation at 10/100/250 points
- Speedup calculation (Mebo vs FBS)
- Performance patterns across data sizes

### Report Part 5: Text Random Access Performance

**Data Source:** `BenchmarkRandomAccessText_Mebo` and `BenchmarkRandomAccessText_FBS`

**Command:**
```bash
go test ./tests/fbs_compare -bench="BenchmarkRandomAccessText" -benchtime=500ms -run=^$
```

**Report Section:**
- Part 5: Random Access Performance (text only in current report structure)

**What to Extract:**
- Time per operation
- Allocations and memory
- FBS advantage (typically 30-33× faster for text)

### Report Part 7: Numeric Random Access Performance

**Data Source:** `BenchmarkRandomAccessValue_Mebo`, `BenchmarkRandomAccessValue_FBS`, `BenchmarkRandomAccessTimestamp_Mebo`, `BenchmarkRandomAccessTimestamp_FBS`

**Command:**
```bash
go test ./tests/fbs_compare -bench="BenchmarkRandomAccessValue|BenchmarkRandomAccessTimestamp" -benchtime=500ms -run=^$
```

**Report Section:**
- Part 7: Numeric Random Access Performance

**What to Extract:**
- ValueAt() performance (all 13 essential mebo configs + 4 FBS configs)
- TimestampAt() performance (only raw encoding for mebo, all FBS configs)
- Speed comparison (Mebo typically 63-73× faster)
- Zero allocations for Mebo vs 200 allocs for FBS

**Key Finding:** Unlike text, Mebo wins for numeric random access due to binary search

### Report Part 8: Decode + Iteration Combined

**Data Source:** `BenchmarkDecodeAndIterateAll_Mebo`, `BenchmarkDecodeAndIterateAll_FBS`, `BenchmarkDecodeAndIterateAllText_Mebo`, `BenchmarkDecodeAndIterateAllText_FBS`

**Command:**
```bash
go test ./tests/fbs_compare -bench="BenchmarkDecodeAndIterateAll" -benchtime=500ms -run=^$
```

**Report Sections:**
- Part 8.1: Numeric Blobs - Decode + Iterate All
- Part 8.2: Text Blobs - Decode + Iterate All

**What to Extract:**
- Combined time (most realistic metric)
- Memory allocations
- Comparison across all configs
- Best balanced configurations

**Key Insight:** This is the most realistic benchmark for "read all data" operations

### Report Part 6: Recommendations

**Data Source:** Analysis of all above benchmarks

**What to Include:**
- When to choose Mebo vs FBS
- Best configurations by use case
- Trade-off analysis
- Practical recommendations

### Report Part 9: Performance Matrix Summary

**Data Source:** Summary table from all benchmarks

**What to Include:**
- Winner for each operation type
- Advantage magnitude
- Overall conclusion

## Regenerating the Report - Complete Workflow

```bash
#!/bin/bash
# Run this script to collect all benchmark data for report generation

cd /home/arlo/projects/mebo

echo "=== Step 1: Size Measurements ==="
go test -v -run="TestBlobSizes|TestTextBlobSizes" 2>&1 | tee /tmp/sizes.txt

echo "=== Step 2: Encoding Benchmarks ==="
go test ./tests/fbs_compare -bench="BenchmarkEncode" -benchtime=500ms -run=^$ 2>&1 | tee /tmp/encode_bench.txt

echo "=== Step 3: Decoding Benchmarks ==="
go test ./tests/fbs_compare -bench="BenchmarkDecode" -benchtime=500ms -run=^$ 2>&1 | tee /tmp/decode_bench.txt

echo "=== Step 4: Iteration Benchmarks ==="
go test ./tests/fbs_compare -bench="BenchmarkIterateAll" -benchtime=200ms -run=^$ 2>&1 | tee /tmp/iterate_bench.txt

echo "=== Step 5: Random Access Benchmarks ==="
go test ./tests/fbs_compare -bench="BenchmarkRandomAccess" -benchtime=500ms -run=^$ 2>&1 | tee /tmp/random_access_bench.txt

echo "=== Step 6: Decode+Iteration Combined ==="
go test ./tests/fbs_compare -bench="BenchmarkDecodeAndIterateAll" -benchtime=500ms -run=^$ 2>&1 | tee /tmp/decode_iterate_bench.txt

echo "=== All benchmarks complete! ==="
echo "Data files saved to /tmp/"
echo ""
echo "=========================================="
echo "⚠️  IMPORTANT: UPDATE BENCHMARK_REPORT.md"
echo "=========================================="
echo ""
echo "All benchmark results go in: BENCHMARK_REPORT.md"
echo "Update it with the following data:"
echo ""
echo "  Part 1: Size metrics        → from /tmp/sizes.txt"
echo "  Part 2: Encoding perf       → from /tmp/encode_bench.txt"
echo "  Part 3: Decoding perf       → from /tmp/decode_bench.txt"
echo "  Part 4: Iteration perf      → from /tmp/iterate_bench.txt"
echo "  Part 5: Text random access  → from /tmp/random_access_bench.txt"
echo "  Part 7: Numeric random access → from /tmp/random_access_bench.txt"
echo "  Part 8: Combined decode+iter  → from /tmp/decode_iterate_bench.txt"
echo ""
echo "📌 BENCHMARK_REPORT.md is your single source of truth for all results!"
```

## Report File: Single Source of Truth

### 📄 BENCHMARK_REPORT.md - The Only Report File

**All benchmark results go into this single file.**

- **Purpose:** Complete all-in-one performance comparison between Mebo and FlatBuffers
- **When to update:** After running any benchmarks
- **Content:**
  - Part 1: Size Comparison (numeric + text)
  - Part 2: Encoding Performance (numeric + text)
  - Part 3: Decoding Performance (numeric + text)
  - Part 4: Iteration Performance (numeric + text)
  - Part 5: Text Random Access Performance
  - Part 6: Key Recommendations
  - Part 7: Numeric Random Access Performance
  - Part 8: Decode + Iteration Combined (numeric + text)
  - Part 9: Performance Matrix Summary
- **Format:** Human-readable markdown with tables and comprehensive analysis

### Why Single File?

**✅ Benefits:**
- One source of truth - no confusion about which file to update
- All data in one place - easier to reference and compare
- Simpler maintenance - update once, done
- Complete picture - all metrics together for holistic analysis

### Updating Workflow

```bash
# 1. Run benchmarks (saves to /tmp/)
./run_all_benchmarks.sh

# 2. Open the report file
vim tests/fbs_compare/BENCHMARK_REPORT.md

# 3. Update sections using data from /tmp/*.txt files
# - See mapping guide above to know which data goes where
# - Each benchmark maps to specific Part(s) in the report

# 4. Commit the updated report
git add tests/fbs_compare/BENCHMARK_REPORT.md
git commit -m "Update benchmark results - [date]"
```

## Data Extraction Tips

### For Size Data
```bash
# Extract bytes/point for all configs
grep "bytes/point" /tmp/sizes.txt | awk '{print $2, $3}'
```

### For Benchmark Data
```bash
# Extract specific benchmark results
grep "BenchmarkEncode_Mebo/100pts" /tmp/encode_bench.txt

# Get just the numbers
grep "BenchmarkEncode" /tmp/encode_bench.txt | awk '{print $1, $3, $5, $7}'
```

### Calculate Speedup
```bash
# Mebo time: 289 μs, FBS time: 1194 μs
# Speedup = 1194 / 289 = 4.13× faster
```

---

**Last Updated:** October 6, 2025
**Maintainer:** Based on investigation findings and report generation experience
**Related Docs:** BENCHMARK_REPORT.md (the only report)


