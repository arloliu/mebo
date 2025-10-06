# Mebo

<p align="center">
  <img src="docs/mebo_logo.png" alt="Mebo Logo" width="300"/>
</p>

[![Go Reference](https://pkg.go.dev/badge/github.com/arloliu/mebo.svg)](https://pkg.go.dev/github.com/arloliu/mebo)
[![Go Report Card](https://goreportcard.com/badge/github.com/arloliu/mebo)](https://goreportcard.com/report/github.com/arloliu/mebo)
[![License: Apache](https://img.shields.io/badge/License-Apache-blue.svg)](LICENSE)

A high-performance, space-efficient binary format for storing time-series metric data in Go.

Mebo is optimized for multiple scenarios, providing excellent compression ratios and fast lookup performance through hash-based identification and columnar storage.

## Design Philosophy

**Mebo is designed for batch processing of already-collected metrics**, not for streaming ingestion. The typical workflow is:

1. **Collect metrics** in memory (from monitoring agents, APIs, or other sources)
2. **Pack metrics** into one or more blobs using Mebo encoders
3. **Persist blobs** to storage (databases, object stores, file systems)
4. **Query blobs** later by decoding them on-demand

This design makes Mebo ideal for:
- 📦 **Batch metric ingestion**: Collect 10 seconds/1 minute/5 minutes of metrics, then encode into single or multiple blobs
- 🗄️ **Time-series databases**: Store compressed metric data with minimal space overhead
- ☁️ **Object storage**: Save blobs to S3/GCS/Azure Blob with excellent compression
- 📊 **Metrics aggregation**: Combine metrics from multiple sources before storage
- 🔄 **ETL pipelines**: Transform and compress metrics between systems

**Important**: Because Mebo works with pre-collected data, you must **declare the number of data points** for each metric upfront using `StartMetricID(metricID, count)` or `StartMetricName(name, count)`, and complete the metric with `EndMetric()`. This allows Mebo to:
- Pre-allocate buffers efficiently
- Validate data completeness
- Optimize compression strategies
- Ensure data integrity

## Features

- 🚀 **High Performance**: 25-50M ops/sec encoding, 40-100M ops/sec decoding
- ⚡ **Zero-Allocation Iteration**: Decode and iterate in-memory without allocating per data point—just read compressed bytes directly
- 💾 **Space Efficient**: 42% smaller than raw storage with Gorilla+Delta encoding
- 🔍 **Fast Lookups**: O(1) metric lookup via 64-bit xxHash64
- 📊 **Columnar Storage**: Separate timestamp and value encoding for optimal compression
- 🎯 **Flexible Encoding**: Choose between Raw, Delta, and Gorilla encodings per blob
- 🗜️ **Optional Compression**: Zstd, S2, LZ4, or no compression
- 🏷️ **Tag Support**: Optional metadata per data point
- 🔋 **Low Memory Footprint**: Minimal allocations with internal buffer pooling
- 🧵 **Thread-Safe**: Immutable blobs, safe concurrent reads
- 🎨 **Type Support**: Numeric (float64) and text (string) metrics

## Installation

```bash
go get github.com/arloliu/mebo
```

**Requirements:** Go 1.24.0 or higher

### Performance Tip

**Enable CGO for optimized Zstd compression:**

If you're using Zstd compression, enable CGO to use the high-performance C implementation:

```bash
CGO_ENABLED=1 go build
```

This provides significant performance improvements (2-3× faster compression/decompression) compared to the pure Go implementation. The pure Go fallback is used when `CGO_ENABLED=0`.

## Quick Start

**Important Note**: Mebo requires you to declare the number of data points for each metric when starting. This is because Mebo is designed for encoding **already-collected metrics** (batch processing), not for streaming/real-time ingestion. Always follow the pattern:

```go
encoder.StartMetricID(metricID, count)  // Declare: "This metric will have 'count' points"
// ... add exactly 'count' data points ...
encoder.EndMetric()                      // Complete: "This metric is done"
```

### Encoding Numeric Metrics

```go
package main

import (
    "fmt"
    "time"
    "github.com/arloliu/mebo"
)

func main() {
    // Create encoder with default settings (Delta timestamps, Gorilla values)
    startTime := time.Now()
    encoder, _ := mebo.NewDefaultNumericEncoder(startTime)

    // Add "cpu.usage" metric by ID with 10 data points
    metricID := mebo.MetricID("cpu.usage")
    encoder.StartMetricID(metricID, 10)
    for i := 0; i < 10; i++ {
        ts := startTime.Add(time.Duration(i) * time.Second)
        encoder.AddDataPoint(ts.UnixMicro(), float64(i*10), "")
    }
    encoder.EndMetric()

    // Add another "process.latency" metric by name with 20 data points
    encoder.StartMetricName(("process.latency", 20)
    for i := 0; i < 20; i++ {
        ts := startTime.Add(time.Duration(i) * time.Second)
        encoder.AddDataPoint(ts.UnixMicro(), float64(i*10), "")
    }
    encoder.EndMetric()

    // Finish and get blob
    blob, _ := encoder.Finish()
    fmt.Printf("Encoded blob: %d bytes\n", len(blob.Bytes()))
}
```

### Decoding Numeric Metrics

```go
// Create decoder from blob
decoder, _ := mebo.NewNumericDecoder(blob.Bytes())

// Sequential iteration (most efficient)
metricID := mebo.MetricID("cpu.usage")
for dp := range decoder.All(metricID) {
    fmt.Printf("timestamp=%d, value=%f\n", dp.Ts, dp.Val)
}

// Random access (when supported by encoding)
value, ok := decoder.ValueAt(metricID, 5)  // Get 6th value
timestamp, ok := decoder.TimestampAt(metricID, 5)
```

### Working with Multiple Blobs

```go
// Create blob set from time-ordered blobs
blobSet, _ := blob.NewNumericBlobSet([]blob.NumericBlob{blob1, blob2, blob3})

// Query across all blobs chronologically
for dp := range blobSet.All(metricID) {
    // Automatically iterates through blob1, blob2, blob3 in order
    fmt.Printf("timestamp=%d, value=%f\n", dp.Ts, dp.Val)
}

// Get time range
start, end := blobSet.TimeRange()
fmt.Printf("Data from %s to %s\n", start, end)
```

### Fast Random Access with Materialization

```go
// One-time materialization cost: ~100μs per metric per blob
materialized := blobSet.Materialize()

// O(1) random access after materialization (~5ns per access)
value, ok := materialized.ValueAt(metricID, 500)     // Very fast!
timestamp, ok := materialized.TimestampAt(metricID, 500)
```

### Bulk Operations for Better Performance

**Metric Lifecycle**: Every metric must follow the `Start → Add → End` pattern:

```go
startTime := time.Now()
encoder, _ := mebo.NewDefaultNumericEncoder(startTime)
metricID := mebo.MetricID("cpu.usage")

// Step 1: Start metric and declare data point count
encoder.StartMetricID(metricID, 1000)  // "I will add 1000 points"

// Step 2: Add data points (single or bulk)
// Single data point insertion (use for streaming data)
for i := 0; i < 1000; i++ {
    ts := startTime.Add(time.Duration(i) * time.Second)
    value := float64(i * 10)
    encoder.AddDataPoint(ts.UnixMicro(), value, "")  // Empty string for no tag
}

// Step 3: Complete the metric
encoder.EndMetric()  // "I'm done with this metric"
```

**Bulk Insertion (2-3× faster)**:

```go
// Bulk insertion with AddDataPoints (more efficient for batch data)
encoder.StartMetricID(metricID, 1000)
timestamps := make([]int64, 1000)
values := make([]float64, 1000)
for i := 0; i < 1000; i++ {
    ts := startTime.Add(time.Duration(i) * time.Second)
    timestamps[i] = ts.UnixMicro()
    values[i] = float64(i * 10)
}
encoder.AddDataPoints(timestamps, values, nil)  // nil for no tags
encoder.EndMetric()

// Bulk insertion with tags
encoder.StartMetricID(metricID, 1000)
// ... prepare timestamps and values same as above ...
tags := make([]string, 1000)
for i := 0; i < 1000; i++ {
    tags[i] = fmt.Sprintf("host=server%d", i%10)
}
encoder.AddDataPoints(timestamps, values, tags)
encoder.EndMetric()
```
```

**Performance Tip**: Use `AddDataPoints` for bulk operations when you have all data ready. It's 2-3× faster than individual `AddDataPoint` calls due to reduced function call overhead and better memory locality.

## Performance

**Benchmark Conditions:**
- **CPU:** Intel Core i7-9700K @ 3.60GHz
- **Go Version:** 1.24+
- **Timestamps:** Microseconds with 1-second intervals ± 5% jitter
- **Values:** Base 100.0, ±2% delta between consecutive points (simulates real monitoring metrics)
- **Dataset:** 200 metrics × 250 points = 50,000 data points
- **📊 Detailed Analysis:** See [docs/METRICS_TO_POINTS_ANALYSIS.md](docs/METRICS_TO_POINTS_ANALYSIS.md) for comprehensive ratio impact analysis

### Compression Ratios

| Configuration | Bytes/Point | Space Savings | Use Case |
|--------------|-------------|---------------|----------|
| **Delta + Gorilla + Zstd** | **9.32** | **42.0%** | 🏆 **Best overall** |
| Delta + Gorilla | 9.65 | 39.9% | CPU-efficient |
| Delta + Raw + Zstd | 10.04 | 37.5% | Fast random access |
| Delta + Raw | 10.93 | 32.0% | Baseline compression |
| Raw + Raw | 16.06 | 0% (baseline) | No compression |

**Text metrics** with Zstd achieve up to **85% space savings**.

### Impact of Metrics-to-Points Ratio

**The ratio of metrics to points per metric is the single most important factor for compression efficiency.** Target: **<1:1 ratio** (more points than metrics).

#### Compression Efficiency by Configuration

Benchmark results using **Delta+Gorilla** (production default, no additional compression):

**Quick Reference (200 metrics):**

| Points/Metric | Total Points | Bytes/Point | Space Savings | Efficiency |
|---------------|--------------|-------------|---------------|------------|
| **10** | 2,000 | **12.48** | **22.0%** | ❌ Poor |
| **100** | 20,000 | **9.81** | **38.7%** | ✅ Good |
| **250** | 50,000 | **9.69** | **39.4%** | ✅ Optimal |

**Comprehensive Results (all combinations tested):**

| Configuration | Bytes/Point | Space Savings | Grade |
|---------------|-------------|---------------|-------|
| ❌ **Terrible**: Any × 1 point | **32-35** | Negative (worse than raw!) | ❌❌ |
| ⚠️ **Poor**: Any × 5 points | **15.4-15.8** | Only 1-4% | ⚠️ |
| ⚠️ **Acceptable**: Any × 10 points | **12.5-12.7** | 20-22% | ⚠️ |
| ✅ **Good**: Any × 100 points | **9.8-9.9** | 38-39% | ✅ |
| ✅ **Optimal**: Any × 100-250 points | **9.68-9.81** | 38-39% | ✅✅ |

#### Key Insights

**1. Points per metric matters 30× more than metric count**

Whether you have 10 or 400 metrics, if each has 100+ points, you'll get ~9.8 bytes/point. The number of points per metric dominates compression efficiency:

- **1 → 5 points**: 52% improvement
- **5 → 10 points**: 19% improvement
- **10 → 100 points**: 21% improvement
- **100 → 250 points**: Only 1% improvement (diminishing returns)

**2. Sweet spot: 100-250 points per metric**

After 100 points, compression efficiency plateaus. Further increases provide minimal benefit.

**3. Minimum threshold: 10 points per metric**

Below 10 points, fixed overhead dominates and compression becomes inefficient.

#### Why More Points = Better Compression

1. **Fixed overhead amortization**: Each metric has fixed costs (metric ID, index entry, flags, metadata) totaling ~34-44 bytes. With more points, this overhead is spread across more data.
2. **Better pattern detection**: Delta and Gorilla encoding work better with larger datasets to identify and exploit patterns.
3. **Reduced metadata ratio**: Index size grows linearly with metric count, but compression benefits scale with total data points.

#### Practical Recommendations

**✅ DO: Optimal Configurations**

| Scenario | Configuration | Result |
|----------|--------------|---------|
| **Best Practice** | 100-250 points/metric | 9.68-9.81 bytes/point (38-39% savings) |
| **Minimum Acceptable** | 10+ points/metric | 12.46-12.74 bytes/point (20-22% savings) |
| **Target Ratio** | <1:1 (metrics:points) | Ensures good compression |

**Example**: 100 metrics × 100 points (1:1) = 9.81 bytes/point ✅

**❌ DON'T: Anti-Patterns**

| Scenario | Configuration | Result | Why It Fails |
|----------|--------------|---------|-------------|
| **Never use** | Any × 1 point | 32+ bytes/point | All overhead, no compression |
| **Avoid** | Any × 5 points | 15.4-15.8 bytes/point | Only 1-4% savings |
| **Avoid high ratios** | 500 metrics × 5 points | ~15.5 bytes/point | Poor compression |

#### Real-World Use Cases

**Scenario 1: Real-time Dashboard (1-minute windows)**
- ❌ Bad: 500 metrics × 6 points (10-second intervals) = ~15.5 bytes/point
- ✅ Good: 500 metrics × 60 points (1-second intervals) = ~9.7 bytes/point
- **Recommendation**: Collect at higher frequency (1Hz) for better compression

**Scenario 2: Long-term Storage (1-hour windows)**
- ❌ Bad: 1000 metrics × 12 points (5-minute intervals) = ~12.5 bytes/point
- ✅ Good: 1000 metrics × 60 points (1-minute intervals) = ~9.8 bytes/point
- ✅ Better: 1000 metrics × 360 points (10-second intervals) = ~9.7 bytes/point
- **Recommendation**: Store higher resolution data, compression makes it cheaper

**Scenario 3: Sparse Metrics**
- ❌ Bad: 1000 metrics × 2 points (only start/end) = ~20+ bytes/point
- ✅ Workaround: Batch multiple time windows together
  - Instead of: 10 blobs × (1000 metrics × 2 points)
  - Do: 1 blob × (1000 metrics × 20 points)
- **Recommendation**: Accumulate before encoding

> 📊 **Comprehensive Analysis:** For detailed benchmarks covering 20 different combinations of metric counts (10/100/200/400) and point sizes (1/5/10/100/250), including ratio analysis and practical recommendations, see [docs/METRICS_TO_POINTS_ANALYSIS.md](docs/METRICS_TO_POINTS_ANALYSIS.md).

### Encoding Performance

| Operation | Speed | Latency | Notes |
|-----------|-------|---------|-------|
| Timestamp (Delta) | ~25M ops/sec | ~40 ns/op | 60-87% compression |
| Numeric (Gorilla) | ~25M ops/sec | ~40 ns/op | 70-85% compression |
| Text (Zstd) | ~20M ops/sec | ~50 ns/op | High compression |
| Tag encoding | ~20M ops/sec | ~50 ns/op | Optional metadata |

### Decoding Performance

**🔥 Hot Feature: Zero-Allocation In-Memory Iteration** — Mebo decodes compressed data on-the-fly without allocating memory per data point. Just read bytes directly from the blob!

| Operation | Speed | Latency | Notes |
|-----------|-------|---------|-------|
| Sequential (Delta) | ~40M ops/sec | ~25 ns/op | Zero allocation, in-memory decode |
| Sequential (Gorilla) | ~50M ops/sec | ~20 ns/op | Zero allocation, less memory bandwidth |
| Random access (Raw) | ~100M ops/sec | ~10 ns/op | O(1) access |
| Random access (Delta) | Varies | O(n) | Must scan from start |
| Materialized access | ~200M ops/sec | ~5 ns/op | Direct array indexing (allocates once) |

### Materialization

- **Cost**: ~100 μs per metric per blob (one-time)
- **Memory**: ~16 bytes/point (numeric), ~24 bytes/point (text)
- **Access**: O(1), ~5 ns per access
- **Speedup**: 820× faster than decoding for random access patterns

### Mebo vs FlatBuffers

Comprehensive benchmarks with 200 metrics × 250 points (50,000 data points):

**Benchmark Conditions:**
- **CPU:** Intel Core i7-9700K @ 3.60GHz
- **Timestamps:** Microseconds with 1-second intervals ± 5% jitter
- **Values:** Base 100.0, ±2% delta between consecutive points (simulates real monitoring metrics)

#### Space Efficiency

| Format | Bytes/Point | Space Savings | Winner |
|--------|-------------|---------------|--------|
| **Mebo (Delta + Gorilla + Zstd)** | **9.32** | **42.0%** | 🏆 |
| Mebo (Delta + Gorilla) | 9.65 | 39.9% | ✓ |
| FlatBuffers + Zstd | ~11-12 | ~30% (est.) | |
| Mebo (Raw) | 16.06 | 0% (baseline) | |

**Result**: Mebo achieves **10-15% better compression** than FlatBuffers with comparable settings.

#### Read Performance (Decode + Iterate All Data)

| Format | Total Time | Winner |
|--------|------------|--------|
| **Mebo (Delta + Gorilla)** | **523 μs** | 🏆 **2.6× faster** |
| Mebo (Raw + Gorilla) | 374 μs | 🏆 **3.6× faster** |
| Mebo (Delta + Gorilla + Zstd) | 999 μs | ✓ **2.4× faster** |
| FlatBuffers + Zstd | 2,377 μs | |
| FlatBuffers (no compression) | 1,337 μs | |

**Result**: Mebo is **2.4-3.6× faster** for reading and iterating data, even with compression enabled.

#### Why Mebo Outperforms FlatBuffers

1. **Zero-Allocation In-Memory Iteration**: Decode compressed data on-the-fly without allocating memory per data point. Just read bytes directly from the blob—no deserialization overhead!

2. **Highly Optimized Gorilla Encoding**: Compressed values iterate faster than uncompressed due to:
   - Less memory bandwidth (smaller data transfers)
   - Better CPU cache utilization (more values per cache line)
   - Fast XOR-based decompression (~20ns per value)

3. **Columnar Storage**: Separate timestamp/value encoding enables:
   - Independent compression strategies
   - Better compression ratios per data type
   - More efficient iteration patterns

3. **Optimized for Time-Series**: Purpose-built for metric data with:
   - Delta-of-delta timestamp compression
   - Value redundancy elimination (Gorilla)
   - Specialized for monitoring use cases

**Key Insight**: Mebo's counter-intuitive result - Gorilla-compressed data (358 μs) iterates **2.3× faster** than raw uncompressed data (816 μs) due to reduced memory bandwidth requirements.

**See detailed benchmarks**: [Full Benchmark Report](_tests/fbs_compare/BENCHMARK_REPORT.md)

## Encoding Strategies

### Timestamp Encodings

**Raw Encoding** - No compression (8 bytes/timestamp)
- Use when: Random access required, irregular timestamps
- Performance: O(1) access, ~10 ns decoding

**Delta Encoding** - Delta-of-delta compression (1-5 bytes/timestamp)
- Use when: Regular intervals (monitoring, metrics)
- Compression: 60-87% space savings
- Performance: O(n) access, ~25 ns decoding

### Value Encodings

**Raw Encoding** - No compression (8 bytes/value)
- Use when: Rapidly changing values, random access required
- Performance: O(1) access, ~10 ns decoding

**Gorilla Encoding** - Facebook's XOR compression (1-8 bytes/value)
- Use when: Slowly changing values (CPU, memory, temperature)
- Compression: 70-85% typical, up to 99.98% for unchanged values
- Performance: O(n) access, ~20 ns decoding
- Best for: Typical monitoring metrics

### Compression Algorithms

| Algorithm | Ratio | Speed | Latency | Best For |
|-----------|-------|-------|---------|----------|
| **None** | 1.0× | Fastest | 0 μs | CPU-constrained, pre-compressed data |
| **LZ4** | 1.3-2.0× | Very Fast | 0.1-0.3 ms | Query-heavy, low-latency |
| **S2** | 1.5-2.5× | Fast | 0.2-0.5 ms | Balanced, real-time ingestion |
| **Zstd** | 2.0-4.0× | Moderate | 0.5-2 ms | Storage-constrained, cold storage |

## Configuration Examples

### High Compression (Storage-Optimized)

```go
encoder, _ := mebo.NewNumericEncoder(time.Now(),
    blob.WithTimestampEncoding(format.TypeDelta),
    blob.WithTimestampCompression(format.CompressionZstd),
    blob.WithValueEncoding(format.TypeGorilla),
    blob.WithValueCompression(format.CompressionZstd),
)
```

**Result**: 9.32 bytes/point (42% savings), best compression ratio, but the decode+iteration slower than no compression.

### Balanced (Production Default)

```go
encoder, _ := mebo.NewDefaultNumericEncoder(time.Now())
```

**Configuration**: Delta timestamps (no compression), Gorilla values (no compression)
**Result**: 9.65 bytes/point (40% savings), minimal CPU overhead, compression ratio very close to zstd.

### Fast Access (Query-Optimized)

```go
encoder, _ := mebo.NewNumericEncoder(time.Now(),
    blob.WithTimestampEncoding(format.TypeRaw),
    blob.WithValueEncoding(format.TypeRaw),
)
```

**Result**: 16.06 bytes/point (no compression), O(1) random access

### Text Metrics

```go
encoder, _ := mebo.NewDefaultTextEncoder(time.Now())
```

**Configuration**: Delta timestamps, Zstd compression
**Result**: 8.71 bytes/point (85% savings for typical text)

## Advanced Usage

### Multi-Blob Queries

```go
// Create blobs for different time windows
blob1, _ := createBlobForHour(startTime, 0, "cpu.usage", "memory.used")
blob2, _ := createBlobForHour(startTime, 1, "cpu.usage", "memory.used")
blob3, _ := createBlobForHour(startTime, 2, "cpu.usage", "memory.used")

// Create blob set (automatically sorted by start time)
blobSet, _ := blob.NewNumericBlobSet([]blob.NumericBlob{blob3, blob1, blob2})

// Query seamlessly across all time windows
cpuID := hash.ID("cpu.usage")
for dp := range blobSet.All(cpuID) {
    fmt.Printf("CPU at %s: %.2f%%\n", time.Unix(0, dp.Ts*1000), dp.Val)
}
```

### Tags Support

```go
startTime := time.Now()
metricID := mebo.MetricID("cpu.usage")

// Enable tags in encoder
encoder, _ := mebo.NewNumericEncoder(startTime,
    blob.WithTagsEnabled(true),
)
// Or use factory function
// encoder, _ := mebo.NewTaggedNumericEncoder(startTime)

// Add tagged values
encoder.StartMetricID(metricID, 10)
for i := 0; i < 10; i++ {
    ts := startTime.Add(time.Duration(i) * time.Second)
    encoder.AddDataPoint(ts.UnixMicro(), float64(i*10), fmt.Sprintf("host=server%d", i%3))
}
encoder.EndMetric()

// Read tags during decoding
for dp := range decoder.AllWithTags(metricID) {
    fmt.Printf("value=%f, tag=%s\n", dp.Val, dp.Tag)
}
```

### Custom Hash IDs

```go
// Use numeric IDs directly (if you have your own hash scheme)
var metricID uint64 = 12345678
encoder.StartMetricID(metricID, 100)

// Or use string names (automatically hashed with xxHash64)
metricID = mebo.MetricID("cpu.usage")  // Returns uint64
```

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     mebo package                        │
│         (Convenience wrappers, MetricID helper)         │
└────────────────────────┬────────────────────────────────┘
                         │
┌────────────────────────┴────────────────────────────────┐
│                     blob package                        │
│    (High-level API: Encoders, Decoders, BlobSets)      │
│  NumericEncoder, NumericDecoder, NumericBlobSet, etc.   │
└────────┬──────────────────────────┬─────────────────────┘
         │                          │
         │                          │
┌────────┴───────────┐    ┌─────────┴──────────┐
│ encoding package   │    │ compress package   │
│  (Columnar algos)  │    │  (Zstd, S2, LZ4)   │
│ Delta, Gorilla,    │    │                    │
│ Raw, VarString     │    │                    │
└────────────────────┘    └────────────────────┘
         │                          │
         └───────────┬──────────────┘
                     │
         ┌───────────┴───────────┐
         │   section package     │
         │ (Binary structures)   │
         │ Headers, Flags, Index │
         └───────────────────────┘
```

### Package Overview

- **mebo**: Top-level convenience API
- **blob**: High-level encoders/decoders, blob management
- **encoding**: Low-level columnar encoding algorithms
- **compress**: Compression layer (Zstd, S2, LZ4)
- **section**: Binary format structures and constants
- **format**: Type definitions and constants

## Best Practices

1. **Always declare data point count**: Call `StartMetricID(id, count)` or `StartMetricName(name, count)` with accurate count, then call `EndMetric()` after adding exactly that many points. This is required for Mebo's batch processing design.
2. **Collect before encoding**: Gather all metric data in memory first, then encode in batches. Mebo is designed for batch processing, not streaming ingestion.
3. **Choose appropriate encoding**: Delta for regular intervals, Gorilla for slowly-changing values, Raw for random access needs.
4. **Batch metrics**: Group related metrics in the same blob for better compression (e.g., all metrics from one time window).
5. **Use bulk operations**: Call `AddDataPoints` instead of multiple `AddDataPoint` calls when you have all data ready (2-3× faster).
6. **Pre-allocate accurately**: Accurate count in `StartMetricID` enables buffer pre-allocation and better performance.
7. **Optimize metrics-to-points ratio**: Each metric should contain at least **10 data points**, with **100-250 points** being optimal. Target **<1:1 ratio** (more points than metrics) for best compression. See [Impact of Metrics-to-Points Ratio](#impact-of-metrics-to-points-ratio) section for detailed analysis.
8. **Use blob sets**: For multi-blob queries, blob sets are more efficient than manual iteration.
8. **Materialize wisely**: Only materialize when random access pattern justifies the cost (>100 accesses).
9. **Monitor memory**: Materialization can use significant memory for large datasets (~16 bytes/point).
10. **Use tags judiciously**: Tags add 8-16 bytes overhead per point; only enable when needed.
11. **Profile your workload**: Test different configurations with your actual data to find optimal settings.

## Thread Safety

- ✅ **Encoders**: Not thread-safe. Use one encoder per goroutine.
- ✅ **Decoders**: Safe for concurrent reads from different goroutines.
- ✅ **Blobs**: Immutable and thread-safe once created.
- ✅ **BlobSets**: Safe for concurrent reads.
- ✅ **MaterializedBlobSets**: Safe for concurrent reads.

## Documentation

- 📚 [API Documentation](https://pkg.go.dev/github.com/arloliu/mebo)
- 📖 [Design Document](docs/DESIGN.md)
- 🧪 [Benchmark Report](_tests/fbs_compare/BENCHMARK_REPORT.md)
- 💡 [Examples](examples/)
  - [Blob Set Demo](examples/blob_set_demo/) - Multi-blob queries and materialization

## Contributing

**Development Guidelines:**

- Follow the [Go style guide](https://golang.org/doc/effective_go.html)
- Run `make lint` before submitting (uses golangci-lint)
- Ensure `make test` passes
- Add tests for new features
- Update documentation as needed

## Testing

```bash
# Run all tests
make test

# Run linters
make lint
```

## Dependencies

Mebo uses minimal, well-maintained dependencies:

- [cespare/xxhash](https://github.com/cespare/xxhash) - Fast hash function
- [klauspost/compress](https://github.com/klauspost/compress) - S2 and Zstd compression
- [pierrec/lz4](https://github.com/pierrec/lz4) - LZ4 compression
- [valyala/gozstd](https://github.com/valyala/gozstd) - Fast CGO-based Zstd

## License

This project is licensed under the Apache License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- **Gorilla compression** algorithm from Facebook's [Gorilla paper](http://www.vldb.org/pvldb/vol8/p1816-teller.pdf) (VLDB 2015)
- **Delta-of-delta encoding** inspiration from time-series databases
- **xxHash64** from [Yann Collet](https://github.com/Cyan4973/xxHash)
