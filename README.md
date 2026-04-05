# Mebo

<p align="center">
  <img src="docs/mebo_logo.png" alt="Mebo Logo" width="300"/>
</p>

[![Go Reference](https://pkg.go.dev/badge/github.com/arloliu/mebo.svg)](https://pkg.go.dev/github.com/arloliu/mebo)
[![Go Report Card](https://goreportcard.com/badge/github.com/arloliu/mebo)](https://goreportcard.com/report/github.com/arloliu/mebo)
[![License: Apache](https://img.shields.io/badge/License-Apache-blue.svg)](LICENSE)

A high-performance, space-efficient binary format for storing time-series metric data in Go, achieving up to 60.5% space savings through columnar encoding without codec compression.

## Design Philosophy

Mebo is designed for **batch processing of already-collected metrics**, not streaming ingestion. The workflow is collect → encode → persist → query.

- **Batch-first model**: Declare the number of data points for each metric upfront via `StartMetricID(id, count)`, add exactly that many points, then call `EndMetric()`. This allows Mebo to pre-allocate buffers, validate completeness, and compress the full sequence.
- **Columnar storage**: Timestamps and values are encoded separately, enabling independent compression strategies and better cache utilization during iteration.
- **Zero-allocation iteration**: Compressed data is decoded on-the-fly directly from the blob without per-point memory allocations.

## Features

**Storage format**
- Binary blob format with compact index (16 bytes per metric entry)
- O(1) metric lookup via 64-bit xxHash64 identifiers
- Separate numeric (float64) and text (string) blob types
- BlobSet: unified multi-blob access with global indexing across time windows

**Encoding**
- Timestamp encodings: Raw, Delta, DeltaPacked (Group Varint)
- Value encodings: Raw, Gorilla (XOR), Chimp (improved XOR, VLDB 2022)
- Optional codec compression: Zstd, S2, LZ4
- Shared timestamps: deduplicates identical timestamp columns across metrics
- Optional per-point tag support

**Access patterns**
- Sequential iteration: O(n), zero allocations
- Random access by index: O(1) for Raw timestamps, O(n) for Delta/Gorilla
- Materialized random access: O(1) ~5 ns after one-time decode cost
- Safe concurrent reads from all decoded blob types

## Installation

```bash
go get github.com/arloliu/mebo
```

**Requirements:** Go 1.24.0 or higher

For Zstd compression, enable CGO for the high-performance C implementation (2-3x faster compression/decompression):

```bash
CGO_ENABLED=1 go build
```

## Quick Start

### Encoding

```go
package main

import (
    "fmt"
    "time"
    "github.com/arloliu/mebo"
)

func main() {
    startTime := time.Now()

    // Create encoder with default settings (Delta timestamps, Gorilla values, no codec)
    encoder, err := mebo.NewDefaultNumericEncoder(startTime)
    if err != nil {
        panic(err)
    }

    // Add "cpu.usage" metric — declare count upfront, then add exactly that many points
    cpuID := mebo.MetricID("cpu.usage")
    encoder.StartMetricID(cpuID, 10)
    for i := 0; i < 10; i++ {
        ts := startTime.Add(time.Duration(i) * time.Second)
        encoder.AddDataPoint(ts.UnixMicro(), float64(i*10), "")
    }
    encoder.EndMetric()

    // Add "process.latency" metric by name
    encoder.StartMetricName("process.latency", 20)
    for i := 0; i < 20; i++ {
        ts := startTime.Add(time.Duration(i) * time.Second)
        encoder.AddDataPoint(ts.UnixMicro(), float64(i)*0.5, "")
    }
    encoder.EndMetric()

    blob, err := encoder.Finish()
    if err != nil {
        panic(err)
    }
    fmt.Printf("Encoded: %d bytes\n", len(blob.Bytes()))
}
```

### Decoding

```go
decoder, err := mebo.NewNumericDecoder(blob.Bytes())
if err != nil {
    panic(err)
}
decoded, err := decoder.Decode()
if err != nil {
    panic(err)
}

// Sequential iteration — most efficient, zero allocations
cpuID := mebo.MetricID("cpu.usage")
for dp := range decoded.All(cpuID) {
    fmt.Printf("ts=%d, val=%f\n", dp.Ts, dp.Val)
}

// Random access (O(1) for Raw timestamp encoding; O(n) for Delta)
value, ok := decoded.ValueAt(cpuID, 5)
```

For bulk insertion, multi-blob queries, materialization, tags, and custom IDs, see [Advanced Usage](docs/ADVANCED_USAGE.md).

## Performance

Benchmark: 200 metrics x 200 points (40,000 total data points), AMD Ryzen 9 9950X3D, Go go1.26.0.

| Configuration | Bytes/Point | Space Savings | Notes |
|---------------|------------:|:-------------:|-------|
| Shared Delta + Chimp | 6.349 | 60.5% | Best compression; requires shared timestamps |
| Delta + Chimp | 8.302 | 48.4% | Best without shared timestamps |
| Delta + Gorilla | 8.540 | 46.9% | Default; well-tested XOR encoding |
| DeltaPacked + Raw | 10.244 | 36.3% | Fastest encode (476,939 ns/op) |
| Raw + Raw | 16.081 | 0% | Baseline |

- Full benchmark tables, scaling analysis, and decision tree: [Performance Guide](docs/PERFORMANCE_V2.md)
- Mebo vs FlatBuffers head-to-head: [Comparison](docs/COMPARISON_FLATBUFFERS.md)

## Encoding Strategies

### Timestamp Encodings

| Encoding | Size | Access | Best for |
|----------|------|--------|----------|
| Raw | 8 bytes fixed | O(1) | Irregular timestamps, random access needed |
| Delta | 1–5 bytes | O(n) | Regular intervals (monitoring, 1-second cadence) |
| DeltaPacked | 1–5 bytes | O(n) | Regular intervals; faster bulk decode via Group Varint |

Delta and DeltaPacked produce similar compression ratios (~2% difference). Use DeltaPacked when iteration throughput matters more than encoding speed.

### Value Encodings

| Encoding | Size | Best for |
|----------|------|----------|
| Raw | 8 bytes fixed | Rapidly changing values, random access |
| Gorilla | 1–8 bytes | Slowly changing values (CPU, memory); XOR-based, VLDB 2015 |
| Chimp | 1–8 bytes | Same as Gorilla; ~2.9% better compression; VLDB 2022 |

### Compression Algorithms

Mebo's encoding algorithms achieve 46–60% savings without any codec. Codec compression adds CPU overhead on both encode and decode for minimal additional benefit on already-compressed numeric data.

| Algorithm | Additional ratio | Best for |
|-----------|-----------------|----------|
| None | — | Default; numeric data already well-compressed |
| Zstd | ~5% on top | Cold storage where decode latency is acceptable |
| S2 | ~2% on top | Balanced; faster than Zstd |
| LZ4 | ~1% on top | Fast decompression priority |

## Configuration Examples

### Best Compression (Shared Timestamps + Delta + Chimp)

```go
encoder, _ := mebo.NewNumericEncoder(time.Now(),
    blob.WithTimestampEncoding(format.TypeDelta),
    blob.WithValueEncoding(format.TypeChimp),
    blob.WithSharedTimestamps(),
)
```

**Result**: 6.349 bytes/point (60.5% savings) when metrics share the same sampling schedule.

All consumers must be upgraded to a Mebo version that supports V2 decoding **before** enabling this on producers. See [Best Practices](docs/BEST_PRACTICES.md#shared-timestamps-upgrade-consumers-before-producers).

### Balanced Default (Delta + Gorilla)

```go
encoder, _ := mebo.NewDefaultNumericEncoder(time.Now())
```

**Configuration**: Delta timestamps, Gorilla values, no codec compression.
**Result**: 8.540 bytes/point (46.9% savings). Recommended for most workloads.

### Best Encode Speed (DeltaPacked + Raw)

```go
encoder, _ := mebo.NewNumericEncoder(time.Now(),
    blob.WithTimestampEncoding(format.TypeDeltaPacked),
    blob.WithValueEncoding(format.TypeRaw),
)
```

**Result**: 10.244 bytes/point (36.3% savings), fastest encoding throughput (476,939 ns/op for 200×200 dataset).

### Query-Optimized (Raw + Raw)

```go
encoder, _ := mebo.NewNumericEncoder(time.Now(),
    blob.WithTimestampEncoding(format.TypeRaw),
    blob.WithValueEncoding(format.TypeRaw),
)
```

**Result**: 16.081 bytes/point, O(1) random access to both timestamps and values.

### Text Metrics

```go
encoder, _ := mebo.NewDefaultTextEncoder(time.Now())
```

**Configuration**: Delta timestamps, Zstd compression on text data (string values compress far more than floats).
**Result**: Up to 85% savings for typical log-level or status text.

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
│ Chimp, DeltaPacked │    │                    │
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

| Package | Responsibility |
|---------|---------------|
| `mebo` | Top-level convenience API and `MetricID` helper |
| `blob` | High-level encoders, decoders, and BlobSet management |
| `encoding` | Columnar encoding algorithms (Delta, DeltaPacked, Gorilla, Chimp, Raw) |
| `compress` | Codec compression layer (Zstd, S2, LZ4) |
| `section` | Binary format structures, headers, index |
| `format` | Encoding and compression type constants |

## Best Practices

The four most important rules:

1. **Declare the point count upfront** — `StartMetricID(id, count)` must be called before adding any points. This is required, not optional.
2. **Batch before encoding** — collect all metric data in memory first; Mebo compresses complete sequences, not streams.
3. **Target 50–200 points per metric** — below 10 points, fixed per-metric overhead dominates and compression degrades sharply.
4. **Upgrade consumers before enabling shared timestamps** — the V2 decoder reads both V1 and V2 blobs; the V1 decoder cannot read V2 blobs.

For full guidance on encoding selection, materialization thresholds, tags overhead, and operational deployment, see [Best Practices](docs/BEST_PRACTICES.md).

## Thread Safety

| Object | Thread safety |
|--------|--------------|
| Encoders | Not thread-safe. Use one encoder per goroutine. |
| Decoders | Not thread-safe and not reusable. Create a new decoder for each decode operation. |
| Blobs | Immutable after decoding. Safe for concurrent reads from multiple goroutines. |
| BlobSets | Safe for concurrent reads. |
| MaterializedBlobSets | Safe for concurrent reads. |

## Stability & Versioning

Mebo follows [Semantic Versioning 2.0.0](https://semver.org/).

**Stable packages** (backward compatible within major version):
- `github.com/arloliu/mebo`
- `github.com/arloliu/mebo/blob`
- `github.com/arloliu/mebo/compress`
- `github.com/arloliu/mebo/encoding`
- `github.com/arloliu/mebo/endian`
- `github.com/arloliu/mebo/section`
- `github.com/arloliu/mebo/errs`

**Internal packages** (`internal/*`): no stability guarantee.

Deprecated features are maintained for at least 2 minor versions before removal. For full details, see [API_STABILITY.md](API_STABILITY.md).

## Documentation

- [API Reference](https://pkg.go.dev/github.com/arloliu/mebo)
- [Performance Guide](docs/PERFORMANCE_V2.md) — full benchmark tables and scaling analysis
- [Advanced Usage](docs/ADVANCED_USAGE.md) — BlobSet, materialization, tags, bulk insertion
- [Best Practices](docs/BEST_PRACTICES.md) — encoding selection, operational guidance
- [FlatBuffers Comparison](docs/COMPARISON_FLATBUFFERS.md) — head-to-head benchmark
- [Shared Timestamps Guide](docs/SHARED_TIMESTAMPS.md) — V2 format and deployment
- [Design Document](docs/DESIGN.md)
- [Examples](examples/)

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before starting. The short checklist:

```bash
make lint     # golangci-lint
make test     # all tests must pass
make coverage # target >80%
```

See [SECURITY.md](SECURITY.md) for the vulnerability reporting policy.

## Dependencies

- [cespare/xxhash](https://github.com/cespare/xxhash) — fast non-cryptographic hash
- [klauspost/compress](https://github.com/klauspost/compress) — S2 and Zstd
- [pierrec/lz4](https://github.com/pierrec/lz4) — LZ4
- [valyala/gozstd](https://github.com/valyala/gozstd) — CGO-based Zstd

## License

Apache License — see [LICENSE](LICENSE) for details.

## Acknowledgments

- Gorilla compression algorithm — [Facebook/Gorilla paper](http://www.vldb.org/pvldb/vol8/p1816-teller.pdf), VLDB 2015
- Chimp compression algorithm — [CHIMP paper](https://www.vldb.org/pvldb/vol15/p3058-liakos.pdf), VLDB 2022
- Delta-of-delta encoding — inspiration from InfluxDB and Prometheus storage engines
- xxHash64 — [Yann Collet](https://github.com/Cyan4973/xxHash)
