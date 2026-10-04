# Mebo

<p align="center">
  <img src="docs/mebo_logo.png" alt="Mebo Logo" width="300"/>
</p>

[![Go Reference](https://pkg.go.dev/badge/github.com/arloliu/mebo.svg)](https://pkg.go.dev/github.com/arloliu/mebo)
[![Go Report Card](https://goreportcard.com/badge/github.com/arloliu/mebo)](https://goreportcard.com/report/github.com/arloliu/mebo)
[![License: Apache](https://img.shields.io/badge/License-Apache-blue.svg)](LICENSE)

A high-performance, space-efficient binary format for storing time-series metric data in Go.
Through columnar encoding alone, without codec compression,
it stores a calibrated mix of monitoring metrics in 2.59 bytes/point, 84% less than raw timestamps and values.

## Design Philosophy

Mebo is designed for **batch processing of already-collected metrics**, not streaming ingestion. The workflow is collect → encode → persist → query.

- **Batch-first model**: Declare the number of data points for each metric upfront via `StartMetricID(id, count)`, add exactly that many points, then call `EndMetric()`. This allows Mebo to pre-allocate buffers, validate completeness, and compress the full sequence.
- **Columnar storage**: Timestamps and values are encoded separately, enabling independent compression strategies and better cache utilization during iteration.
- **Zero-allocation iteration**: Compressed data is decoded on-the-fly directly from the blob without per-point memory allocations.

## Features

**Storage format**
- Binary blob format with compact index (16 bytes per metric entry; 32 bytes for V2 extended entries)
- O(1) metric lookup via 64-bit xxHash64 identifiers
- Separate numeric (float64) and text (string) blob types
- BlobSet: unified multi-blob access with global indexing across time windows

**Encoding**
- Timestamp encodings: Raw, Delta, DeltaPacked (Group Varint)
- Value encodings: Raw, Gorilla (XOR), Chimp (improved XOR, VLDB 2022), ALP (Adaptive Lossless floating-Point, SIGMOD 2024) for decimal-quantized data, and ALP-RLE (ALP with a run-length front end) for columns where many points repeat the previous value
- Optional codec compression: Zstd, S2, LZ4
- Shared timestamps: deduplicates identical timestamp columns across metrics
- Optional per-point tag support

**Access patterns**
- Sequential iteration: O(n), zero allocations
- Random access by index: O(1) for Raw (timestamp or value); ALP values add O(log k) for that column's exceptions, and ALP-RLE columns with runs add an O(index/64) bitmap rank; shared timestamps (any encoding) are O(1) from a cache built when the blob is opened; other Delta/DeltaPacked timestamps and Gorilla/Chimp values are O(index) (sequential decode from the start) — see [Performance Guide § Random Access Performance](docs/performance.md#random-access-performance) for measured ns/op
- Materialized random access: O(1) ~5 ns after one-time decode cost
- Safe concurrent reads from all decoded blob types

## Installation

```bash
go get github.com/arloliu/mebo
```

**Requirements:** Go 1.25.0 or higher

Mebo is pure Go and needs no CGO.
Zstd compression uses the pure-Go [klauspost/compress](https://github.com/klauspost/compress) implementation.

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

    data, err := encoder.Finish()
    if err != nil {
        panic(err)
    }
    fmt.Printf("Encoded: %d bytes\n", len(data))
}
```

### Decoding

```go
decoder, err := mebo.NewNumericDecoder(data)
if err != nil {
    panic(err)
}
decoded, err := decoder.Decode()
if err != nil {
    panic(err)
}

// Sequential iteration — most efficient, zero allocations
cpuID := mebo.MetricID("cpu.usage")
for _, dp := range decoded.All(cpuID) {
    fmt.Printf("ts=%d, val=%f\n", dp.Ts, dp.Val)
}

// Random access — O(index) here since the default encoder uses Gorilla values
// (sequential XOR decode from the start of the column); use Raw or ALP values
// for O(1)/O(1)+O(log k) random access instead
value, ok := decoded.ValueAt(cpuID, 5)
```

For bulk insertion, buffer reuse with `FinishInto`, callback iteration with `ForEach`, multi-blob queries, materialization, tags, and custom IDs, see [Advanced Usage](docs/advanced_usage.md).

## Performance

Benchmark: the `mix_monitoring` profile, 100 metrics × 150 points (15,000 data points), AMD Ryzen 9 9950X3D, Go go1.26.7.
The mix holds 2-decimal gauges, counters, mostly-constant values and full-precision gauges,
in shares calibrated so that Chimp costs about 3.8 bytes/point.

| Configuration | Bytes/Point | Space Savings | Notes |
|---------------|------------:|:-------------:|-------|
| Shared DeltaPacked + ALP-RLE | 2.593 | 83.9% | Smallest; needs readers that know ALP-RLE and shared timestamps; encodes 5.1× slower than Chimp |
| Delta + ALP-RLE | 3.797 | 76.4% | Smallest without shared timestamps |
| Shared DeltaPacked + Chimp | 3.846 | 76.1% | Smallest with an XOR codec |
| Delta + Gorilla | 5.096 | 68.4% | Default (`NewDefaultNumericEncoder`) |
| Raw + Raw | 16.109 | 0% | Baseline; fastest encode (120,033 ns/op) |

How much ALP-RLE saves depends on the data:
12.8–47.5% against Chimp across four calibrated mixes, up to 80% on a single decimal gauge,
and 2% more than Chimp on full-precision values that never repeat.
See [Performance Guide § Codec Selection by Data Shape](docs/performance.md#codec-selection-by-data-shape)
for the full breakdown across data shapes (decimals, counters, sparse data, repeated values, full-precision noise).

- Full benchmark tables, scaling analysis, and decision tree: [Performance Guide](docs/performance.md)
- Mebo vs FlatBuffers head-to-head: [Comparison](docs/comparison_flatbuffers.md)

## Encoding Strategies

### Timestamp Encodings

| Encoding | Size | Random access | Best for |
|----------|------|----------------|----------|
| Raw | 8 bytes fixed | O(1) | Irregular timestamps, random access needed |
| Delta | ~1 byte typical, 10 bytes worst case | O(index) | Regular intervals (monitoring, 1-second cadence) |
| DeltaPacked | ~1.25 bytes typical, ~8.25 bytes worst case | O(index) | Regular intervals; Group Varint batch layout |

Delta and DeltaPacked differ little in size: on the benchmark mix DeltaPacked costs 0.2 bytes/point more per metric, and 0.002 more with shared timestamps.
DeltaPacked's Group Varint layout is meant for faster decode,
but the 2026-10-04 benchmark run measured it iterating slower than Delta with Gorilla and Chimp;
measure your own workload before choosing it for throughput.

### Value Encodings

| Encoding | Size | Random access | Best for |
|----------|------|----------------|----------|
| Raw | 8 bytes fixed | O(1) | Rapidly changing values, random access |
| Gorilla | 1–8 bytes | O(index) | Slowly changing values (CPU, memory); XOR-based, VLDB 2015 |
| Chimp | 1–8 bytes | O(index) | Same as Gorilla; 0.8–2.6% smaller on the benchmark mixes, larger on counters; VLDB 2022 |
| ALP | Variable | O(1) + O(log k)* | Decimal-quantized data (2–4 dp) and counters: 2.4–3.6× smaller than Chimp on 2- and 4-dp gauges. Larger than Chimp on full-precision values that repeat, and costs more to encode; see [Performance Guide](docs/performance.md#codec-selection-by-data-shape) |
| ALP-RLE | Variable | O(index/64) + O(log k)† | Columns where many points repeat the previous value: each uncompressed column is never larger than ALP; a 2-dp gauge where half the points repeat is 1.12 B/pt vs 3.38 for Chimp (100 × 150 blob). Older readers reject it; see [ALP or ALP-RLE?](docs/best_practices.md#alp-or-alp-rle) |

\* k = exceptions in that column, not its length — measured 4.8–23× faster than Gorilla/Chimp's
`ValueAt` on the benchmark profiles' 150-point columns, least on mostly-constant data; see [Performance Guide § Random Access Performance](docs/performance.md#random-access-performance).

† A column with runs first ranks its run-start bitmap, one 64-bit word at a time (at most 3 words at 150 points);
a column without enough repeats is stored and read exactly like ALP.

### Compression Algorithms

Mebo's encoding algorithms save 68–84% on the benchmark mix without any codec, from Delta + Gorilla to Shared DeltaPacked + ALP-RLE.
Codec compression adds CPU overhead on both encode and decode for minimal additional benefit on already-compressed numeric data.

| Algorithm | Additional ratio | Best for |
|-----------|-----------------|----------|
| None | — | Default; numeric data already well-compressed |
| Zstd | ~5% on top | Cold storage where decode latency is acceptable |
| S2 | ~2% on top | Balanced; faster than Zstd |
| LZ4 | ~1% on top | Fast decompression priority |

## Configuration Examples

### Best Compression (Shared Timestamps + DeltaPacked + ALP-RLE)

```go
encoder, _ := mebo.NewNumericEncoder(time.Now(),
    blob.WithTimestampEncoding(format.TypeDeltaPacked),
    blob.WithValueEncoding(format.TypeALPRLE),
    blob.WithSharedTimestamps(),
)
```

**Result**: 2.593 bytes/point (83.9% savings) on the benchmark mix, when metrics share the same sampling schedule.
Encoding takes about 5× as long as with Chimp.
For full-precision values that never repeat, `format.TypeChimp` is 2% smaller.

All consumers must be upgraded to a Mebo version that decodes V2 blobs and ALP-RLE **before** enabling this on producers.
See [Best Practices](docs/best_practices.md#shared-timestamps-upgrade-consumers-before-producers)
and [ALP-RLE: upgrade consumers before producers](docs/best_practices.md#alp-rle-upgrade-consumers-before-producers).

### Balanced Default (Delta + Gorilla)

```go
encoder, _ := mebo.NewDefaultNumericEncoder(time.Now())
```

**Configuration**: Delta timestamps, Gorilla values, no codec compression.
**Result**: 5.096 bytes/point (68.4% savings) on the benchmark mix.
Readers need no shared-timestamp or ALP-RLE support.

### Fast Iteration Without a Value Codec (DeltaPacked + Raw)

```go
encoder, _ := mebo.NewNumericEncoder(time.Now(),
    blob.WithTimestampEncoding(format.TypeDeltaPacked),
    blob.WithValueEncoding(format.TypeRaw),
)
```

**Result**: 9.540 bytes/point (40.8% savings), 106,493 ns/op sequential iteration on the benchmark mix.
DeltaPacked's Group Varint batch decoding is optimized for read throughput, not encode speed;
if encode speed is the priority, plain Raw + Raw is fastest to encode (120,033 ns/op) at the cost of no compression.
In the 2026-10-04 run, the ALP and ALP-RLE combos iterate faster at under half the size (Delta + ALP: 79,941 ns/op, 4.026 bytes/point);
see [Performance Guide § Iteration Performance](docs/performance.md#iteration-performance).

### Query-Optimized (Raw + Raw)

```go
encoder, _ := mebo.NewNumericEncoder(time.Now(),
    blob.WithTimestampEncoding(format.TypeRaw),
    blob.WithValueEncoding(format.TypeRaw),
)
```

**Result**: 16.109 bytes/point, O(1) random access to both timestamps and values.

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
│ Chimp, ALP,        │    │                    │
│ ALP-RLE,           │    │                    │
│ DeltaPacked        │    │                    │
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
| `encoding` | Columnar encoding algorithms (Delta, DeltaPacked, Gorilla, Chimp, ALP, ALP-RLE, Raw) |
| `compress` | Codec compression layer (Zstd, S2, LZ4) |
| `section` | Binary format structures, headers, index |
| `format` | Encoding and compression type constants |

## Best Practices

The four most important rules:

1. **Declare the point count upfront** — `StartMetricID(id, count)` must be called before adding any points. This is required, not optional.
2. **Batch before encoding** — collect all metric data in memory first; Mebo compresses complete sequences, not streams.
3. **Target 50–200 points per metric** — below 10 points, fixed per-metric overhead dominates and compression degrades sharply.
4. **Upgrade consumers before enabling shared timestamps** — the V2 decoder reads both V1 and V2 blobs; the V1 decoder cannot read V2 blobs.

For full guidance on encoding selection, materialization thresholds, tags overhead, and operational deployment, see [Best Practices](docs/best_practices.md).

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
- [Performance Guide](docs/performance.md) — full benchmark tables and scaling analysis
- [Advanced Usage](docs/advanced_usage.md) — BlobSet, materialization, tags, bulk insertion, FinishInto buffer reuse, ForEach callback iteration
- [Best Practices](docs/best_practices.md) — encoding selection, operational guidance
- [FlatBuffers Comparison](docs/comparison_flatbuffers.md) — head-to-head benchmark
- [Shared Timestamps Guide](docs/shared_timestamps.md) — V2 format and deployment
- [Metric Names Guide](docs/metric_names.md) — storage, enumeration/membership semantics, and stripping
- [Design Document](docs/design.md)
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
- [klauspost/compress](https://github.com/klauspost/compress) — S2 and Zstd (pure Go)
- [pierrec/lz4](https://github.com/pierrec/lz4) — LZ4

## License

Apache License — see [LICENSE](LICENSE) for details.

## Acknowledgments

- Gorilla compression algorithm — [Facebook/Gorilla paper](http://www.vldb.org/pvldb/vol8/p1816-teller.pdf), VLDB 2015
- Chimp compression algorithm — [CHIMP paper](https://www.vldb.org/pvldb/vol15/p3058-liakos.pdf), VLDB 2022
- ALP compression algorithm — Afroozeh, Kuffó & Boncz, ["ALP: Adaptive Lossless floating-Point Compression"](https://doi.org/10.1145/3626717), SIGMOD 2024; reference implementation [cwida/ALP](https://github.com/cwida/ALP)
- Delta-of-delta encoding — inspiration from InfluxDB and Prometheus storage engines
- xxHash64 — [Yann Collet](https://github.com/Cyan4973/xxHash)
