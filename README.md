# Mebo

<p align="center">
  <img src="docs/mebo_logo.png" alt="Mebo Logo" width="300"/>
</p>

[![Go Reference](https://pkg.go.dev/badge/github.com/arloliu/mebo.svg)](https://pkg.go.dev/github.com/arloliu/mebo)
[![Go Report Card](https://goreportcard.com/badge/github.com/arloliu/mebo)](https://goreportcard.com/report/github.com/arloliu/mebo)
[![License: Apache](https://img.shields.io/badge/License-Apache-blue.svg)](LICENSE)

A high-performance, space-efficient binary format for storing time-series metric data in Go.

Mebo is optimized for multiple scenarios, providing excellent compression ratios and fast lookup performance through hash-based identification and columnar storage.

## Features

- 🚀 **High Performance**: 25-50M ops/sec encoding, 40-100M ops/sec decoding
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

```go
startTime := time.Now()
encoder, _ := mebo.NewDefaultNumericEncoder(startTime)

// Single data point insertion (use for streaming data)
metricID := mebo.MetricID("cpu.usage")
encoder.StartMetricID(metricID, 1000)
for i := 0; i < 1000; i++ {
    ts := startTime.Add(time.Duration(i) * time.Second)
    value := float64(i * 10)
    encoder.AddDataPoint(ts.UnixMicro(), value, "")  // Empty string for no tag
}
encoder.EndMetric()

// Bulk insertion (2-3× faster for batch data)
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
tags := make([]string, 1000)
for i := 0; i < 1000; i++ {
    tags[i] = fmt.Sprintf("host=server%d", i%10)
}
encoder.AddDataPoints(timestamps, values, tags)
encoder.EndMetric()
```

**Performance Tip**: Use `AddDataPoints` for bulk operations when you have all data ready. It's 2-3× faster than individual `AddDataPoint` calls due to reduced function call overhead and better memory locality.

## Performance

### Compression Ratios

Benchmark with 200 metrics × 250 points (50,000 data points):

| Configuration | Bytes/Point | Space Savings | Use Case |
|--------------|-------------|---------------|----------|
| **Delta + Gorilla + Zstd** | **9.32** | **42.0%** | 🏆 **Best overall** |
| Delta + Gorilla | 9.65 | 39.9% | CPU-efficient |
| Delta + Raw + Zstd | 10.04 | 37.5% | Fast random access |
| Delta + Raw | 10.93 | 32.0% | Baseline compression |
| Raw + Raw | 16.06 | 0% (baseline) | No compression |

**Text metrics** with Zstd achieve up to **85% space savings**.

### Encoding Performance

| Operation | Speed | Latency | Notes |
|-----------|-------|---------|-------|
| Timestamp (Delta) | ~25M ops/sec | ~40 ns/op | 60-87% compression |
| Numeric (Gorilla) | ~25M ops/sec | ~40 ns/op | 70-85% compression |
| Text (Zstd) | ~20M ops/sec | ~50 ns/op | High compression |
| Tag encoding | ~20M ops/sec | ~50 ns/op | Optional metadata |

### Decoding Performance

| Operation | Speed | Latency | Notes |
|-----------|-------|---------|-------|
| Sequential (Delta) | ~40M ops/sec | ~25 ns/op | Most efficient |
| Sequential (Gorilla) | ~50M ops/sec | ~20 ns/op | Less memory bandwidth |
| Random access (Raw) | ~100M ops/sec | ~10 ns/op | O(1) access |
| Random access (Delta) | Varies | O(n) | Must scan from start |
| Materialized access | ~200M ops/sec | ~5 ns/op | Direct array indexing |

### Materialization

- **Cost**: ~100 μs per metric per blob (one-time)
- **Memory**: ~16 bytes/point (numeric), ~24 bytes/point (text)
- **Access**: O(1), ~5 ns per access
- **Speedup**: 820× faster than decoding for random access patterns

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

**Result**: 9.32 bytes/point (42% savings), best compression ratio

### Balanced (Production Default)

```go
encoder, _ := mebo.NewDefaultNumericEncoder(time.Now())
```

**Configuration**: Delta timestamps (no compression), Gorilla values (no compression)
**Result**: 9.65 bytes/point (40% savings), minimal CPU overhead

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

1. **Choose appropriate encoding**: Delta for regular intervals, Gorilla for slowly-changing values
2. **Batch metrics**: Group related metrics in the same blob for better compression
3. **Pre-allocate**: Use `StartMetricID` with accurate capacity for better performance
4. **Use blob sets**: For multi-blob queries, blob sets are more efficient than manual iteration
5. **Materialize wisely**: Only materialize when random access pattern justifies the cost (>100 accesses)
6. **Monitor memory**: Materialization can use significant memory for large datasets
7. **Use tags judiciously**: Tags add 8-16 bytes overhead per point; only enable when needed
8. **Profile your workload**: Test different configurations with your actual data

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
