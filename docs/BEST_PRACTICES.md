# Best Practices

Detailed guidance for getting the best performance, efficiency, and reliability from Mebo. See also the [Performance Guide](PERFORMANCE_V2.md) for benchmark data backing these recommendations.

## Table of Contents

- [Encoding Fundamentals](#encoding-fundamentals)
- [Compression Efficiency](#compression-efficiency)
- [Operational Concerns](#operational-concerns)

---

## Encoding Fundamentals

### Always declare the data point count upfront

Mebo is a batch-processing library, not a streaming encoder. You must declare how many data points a metric will have before adding any of them:

```go
encoder.StartMetricID(metricID, 1000) // declare: 1000 points will follow
// ... add exactly 1000 points ...
encoder.EndMetric()
```

The count enables buffer pre-allocation, validates data completeness, and ensures data integrity. Passing a wrong count will result in an error at `EndMetric()`.

### Collect data before encoding

Gather all metric data in memory first, then encode in a single pass. Mebo's compression algorithms work best on complete datasets — Delta encoding computes differences across the full sequence, and Gorilla/Chimp exploit XOR patterns that are only visible with a complete run.

```go
// Correct: collect all points, then encode
points := collectMetricData(from, to)
encoder.StartMetricID(metricID, len(points))
for _, p := range points {
    encoder.AddDataPoint(p.Ts, p.Val, "")
}
encoder.EndMetric()
```

### Use `AddDataPoints` for bulk data

`AddDataPoints` accepts pre-built slices and is 2–3× faster than calling `AddDataPoint` in a loop, due to reduced function call overhead and better memory locality.

```go
// Preferred: batch API
encoder.StartMetricID(metricID, len(timestamps))
encoder.AddDataPoints(timestamps, values, nil)
encoder.EndMetric()
```

Use `AddDataPoint` (singular) only when you're computing values one at a time and a slice is impractical.

### Group related metrics in the same blob

Metrics collected in the same time window should share a blob. Index overhead is per-blob, so grouping 200 metrics in one blob is far more efficient than 200 single-metric blobs.

---

## Compression Efficiency

### Target 50–200 points per metric

Fixed per-metric overhead (index entry, header flags, metadata) totals ~34–44 bytes per metric. This overhead is amortized across the points in that metric:

| Points/Metric | Approx BPP (Delta+Gorilla) | Efficiency |
|---------------|---------------------------|------------|
| 1             | ~32                       | Poor — overhead dominates |
| 10            | ~10.7                     | Acceptable |
| 50            | ~8.9                      | Good |
| 100           | ~8.6                      | Excellent |
| 200           | ~8.5                      | Optimal — diminishing returns beyond this |

For full scaling data, see [Performance Guide — Scaling Analysis](PERFORMANCE_V2.md#scaling-analysis).

### Choose the right encoding for your data

| Data pattern | Recommended encoding | Why |
|---|---|---|
| Regular 1-second intervals | Delta or DeltaPacked timestamp | 1–5 bytes/ts vs 8 bytes raw |
| Slowly changing floats (CPU, memory) | Gorilla or Chimp value | XOR compression; ~2–5 bytes/val |
| Rapidly changing or discontinuous values | Raw value | No decompression overhead |
| Metrics that share the same sampling schedule | `WithSharedTimestamps()` | Deduplicate timestamp column across metrics; ~20–25% additional savings at 200 metrics |
| Frequent random access needed | Raw timestamp | O(1) `TimestampAt`; Delta/DeltaPacked require sequential scan |

DeltaPacked vs Delta: DeltaPacked uses Group Varint for **faster decode/iteration**, not better compression. Size difference is marginal (~2%). Choose DeltaPacked when iteration throughput matters more than encoding speed.

Chimp vs Gorilla: Chimp achieves ~2.9% better compression ratio. Both use XOR-based encoding. Choose based on whether the marginal size reduction justifies the slightly different algorithm.

### Codec compression is optional

Mebo's encoding algorithms (Delta + Chimp) already achieve 48–61% space savings without any codec layer. Codec compression (Zstd, S2, LZ4) adds CPU cost on encode and decode for marginal additional savings on already-compressed numeric data.

The default (`NewDefaultNumericEncoder`) uses no codec compression and is the recommended choice for most workloads. Add a codec only when storage cost outweighs CPU budget — typically for cold storage of historical data.

---

## Operational Concerns

### Shared timestamps: upgrade consumers before producers

`WithSharedTimestamps()` enables V2 blob format. The V2 decoder reads both V1 and V2 blobs, but a V1 decoder cannot read V2 blobs. The safe upgrade sequence is:

1. Deploy all consumers on a Mebo version that supports V2 decoding.
2. Verify consumers are running and handling V1 blobs correctly.
3. Enable `WithSharedTimestamps()` on producers.

Do not enable shared timestamps on producers until all consumers have been upgraded. The decoder will return an error when a V1-only decoder encounters a V2 blob.

### Materialize only when random access is frequent

Materialization decodes all data into memory once (~100 µs per metric per blob; ~16 bytes/point memory). It enables O(1) random access (~5 ns/op).

The break-even point is roughly 100 random accesses on a dataset: the one-time materialization cost is recovered after that many `ValueAt` or `TimestampAt` calls. For purely sequential workloads, skip materialization and use `blob.All()` directly.

### Tags add overhead — enable them only when needed

Tags are stored as a length-prefixed string per data point and add ~8–16 bytes of overhead per point. Enabling tags on a 200-metric × 200-point blob adds 320 KB–640 KB of overhead.

Use `NewDefaultNumericEncoder` (tags disabled by default) and switch to `NewTaggedNumericEncoder` only when per-point metadata is required.

### Thread safety model

| Object | Thread safety |
|--------|--------------|
| Encoders (`NumericEncoder`, `TextEncoder`) | Not thread-safe. Use one encoder per goroutine. |
| Blobs (`NumericBlob`, `TextBlob`) | Immutable and safe for concurrent reads once created. |
| Decoders (`NumericDecoder`, `TextDecoder`) | Safe for concurrent reads from different goroutines. |
| BlobSets | Safe for concurrent reads. |
| MaterializedBlobSets | Safe for concurrent reads. |

Encoders are not safe to share across goroutines. If you need parallel encoding of multiple metrics, create one encoder per goroutine and merge the blobs into a BlobSet afterward.

### Monitor memory for large materializations

Materialized blob sets hold all data points in memory. For large datasets:

- Numeric: ~16 bytes per data point
- Text: ~24 bytes per data point (includes string pointer + backing storage)

For 200 metrics × 1000 points, a numeric materialized set uses ~3.2 MB. Plan accordingly and avoid materializing datasets that would exhaust available memory when combined with other in-flight work.
