# Best Practices

Detailed guidance for getting the best performance, efficiency, and reliability from Mebo. See also the [Performance Guide](performance.md) for benchmark data backing these recommendations.

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
| 10            | ~8.1                      | Acceptable |
| 50            | ~5.5                      | Good |
| 100           | ~5.2                      | Excellent |
| 150           | ~5.1                      | Optimal — the longest columns measured; still falling slowly |

These are bytes/point for the default encoder on the Performance Guide's benchmark mix (100 metrics).

For full scaling data, see [Performance Guide — Scaling Analysis](performance.md#scaling-analysis).

### Choose the right encoding for your data

| Data pattern | Recommended encoding | Why |
|---|---|---|
| Regular 1-second intervals | Delta or DeltaPacked timestamp | ~1 byte/ts on regular data vs 8 bytes raw (worst case 10 bytes for Delta, ~8.25 for DeltaPacked) |
| Full-precision floats that rarely repeat (computed rates, ratios) | Chimp or Gorilla value | XOR compression; on a full-precision gauge, Chimp is 2% smaller than ALP and ALP-RLE, and ALP-RLE takes 1.4× its encode time (several times longer on CPUs without AVX-512DQ) |
| Rapidly changing or discontinuous values | Raw value | No decompression overhead |
| Metrics that share the same sampling schedule | `WithSharedTimestamps()` | Deduplicate timestamp column across metrics; saves about 1.2 bytes/point on the 100-metric benchmark mix, 24% with Chimp and 32% with ALP-RLE |
| Decimal-quantized sensor data (2–4 dp) | ALP value | 2.4–3.8× smaller than Chimp/Gorilla on the 2- and 4-dp gauge profiles with shared timestamps; costs more to encode |
| Decimal data where many points repeat the previous value, or gauges, counters and held values mixed in one blob | ALP-RLE value | Stores each run of repeats once; smallest on all four benchmark mixes, 12.8–47.5% below Chimp; see [ALP or ALP-RLE?](#alp-or-alp-rle) |
| Frequent random-access timestamps | Raw timestamp | O(1) `TimestampAt`; Delta/DeltaPacked must sequentially decode from the start (O(index)) |
| Frequent random-access values | Raw, ALP or ALP-RLE value | Raw is O(1); ALP is O(1) + O(log k) (k = exceptions in the column); ALP-RLE adds a bitmap rank of O(index/64) on columns with runs — all far ahead of Gorilla/Chimp, which must sequentially decode the XOR chain from the start (O(index)) |

DeltaPacked vs Delta: DeltaPacked uses Group Varint, meant for **faster decode/iteration**, not better compression.
Size difference is marginal: about 0.2 bytes/point per metric on the benchmark mix, and 0.002 with shared timestamps.
The 2026-10-06 layout-averaged benchmark run measured DeltaPacked iterating 1.26–1.35× slower than Delta with Gorilla and Chimp,
in every code layout, while with ALP and ALP-RLE the gap stays under the 20% that the report counts as a difference;
prefer Delta unless your own measurements show otherwise.

Chimp vs Gorilla: Chimp is 0.8–2.6% smaller on the benchmark mixes, but Gorilla is smaller on counters and mostly-constant values.
Both use XOR-based encoding.
Choose based on whether the marginal size reduction justifies the slightly different algorithm.

Random access is not just a timestamp-encoding question — the *value* encoding matters just as
much, and Gorilla/Chimp are the slow axis there (see
[Performance Guide § Random Access Performance](performance.md#random-access-performance) for
measured ns/op across every combination).

### ALP or ALP-RLE?

`format.TypeALPRLE` is ALP with a run-length front end.
For each column, the encoder builds the plain ALP column and keeps a runs layout instead only when that is smaller:
a bitmap marks where each run of identical values starts, and the run values are stored once as an ALP column.

Pick ALP-RLE over ALP when many consecutive points repeat the previous value,
such as held gauges, status values, or slow sensors scraped faster than they change.

- **Size:** each uncompressed column is never larger than under ALP.
  On a 2-decimal gauge where half the points repeat, a whole blob of 100 metrics × 150 points is 1.12 bytes/point,
  against 1.68 for ALP and 3.38 for Chimp.
  Columns without enough repeats stay plain ALP columns, byte for byte.
- **Value compression:** with Zstd, S2 or LZ4 the codec compresses the whole value payload,
  and a smaller input is not guaranteed to compress smaller, so the compressed payload is not guaranteed to shrink.
- **Encode cost:** one extra pass counts the runs, about 4% on data without repeats.
  When half the points repeat, encoding is about 1.8× ALP, because the run values get their own ALP column.
  Both ratios come from a CPU with AVX-512DQ;
  with the scalar (e, f) search both codecs encode several times slower,
  and the overhead measured before the AVX-512 search was about 1% and 1.5×.
- **Read cost:** on that half-repeated gauge, `DecodeAll` is 3× faster than Chimp, `ValueAt` is 1.14× ALP,
  and `ForEachValues` is about 3.2 ns/point against Chimp's 4.7.
- **Compatibility:** readers older than this encoding reject the blob; see [ALP-RLE: upgrade consumers before producers](#alp-rle-upgrade-consumers-before-producers).

The measurements and their method are in the [Performance Guide](performance.md#alp-rle-speed-layout-averaged).

### Codec compression is optional

Mebo's encoding algorithms already save 68–84% on the benchmark mix without any codec layer, from Delta + Gorilla to Shared DeltaPacked + ALP-RLE.
Codec compression (Zstd, S2, LZ4) adds CPU cost on encode and decode for marginal additional savings on already-compressed numeric data.

The default (`NewDefaultNumericEncoder`) uses no codec compression and is the recommended choice for most workloads. Add a codec only when storage cost outweighs CPU budget — typically for cold storage of historical data.

---

## Operational Concerns

### Shared timestamps: upgrade consumers before producers

`WithSharedTimestamps()` enables V2 blob format. The V2 decoder reads both V1 and V2 blobs, but a V1 decoder cannot read V2 blobs. The safe upgrade sequence is:

1. Deploy all consumers on a Mebo version that supports V2 decoding.
2. Verify consumers are running and handling V1 blobs correctly.
3. Enable `WithSharedTimestamps()` on producers.

Do not enable shared timestamps on producers until all consumers have been upgraded. The decoder will return an error when a V1-only decoder encounters a V2 blob.

### ALP-RLE: upgrade consumers before producers

Blobs encoded with `format.TypeALPRLE` use a value-encoding flag that older readers do not know,
so a decoder older than this encoding rejects the blob with an invalid-header-flags error.
Upgrade every consumer before switching producers to ALP-RLE, as for shared timestamps above.
Readers that support it decode ALP and ALP-RLE blobs alike.

### Materialize only when random access is frequent

Materialization decodes all data into memory once (~100 µs per metric per blob; ~16 bytes/point memory). It enables O(1) random access (~5 ns/op).

The break-even point is roughly 100 random accesses on a dataset: the one-time materialization cost is recovered after that many `ValueAt` or `TimestampAt` calls. For purely sequential workloads, skip materialization and use `blob.All()` directly.

### Tags add overhead — enable them only when needed

Tags are stored as a length-prefixed string per data point.
The numeric tag payload is always Zstd-compressed, so the real overhead depends on tag length and repetitiveness:
repeated tags such as `host=server1` compress to a small fraction of their raw size,
while long unique tags cost close to their full length.
If every tag in a blob is empty, the encoder drops the tag payload entirely.

Use `NewDefaultNumericEncoder` (tags disabled by default) and switch to `NewTaggedNumericEncoder` only when per-point metadata is required.

### Thread safety model

| Object | Thread safety |
|--------|--------------|
| Encoders (`NumericEncoder`, `TextEncoder`) | Not thread-safe. Use one encoder per goroutine. |
| Blobs (`NumericBlob`, `TextBlob`) | Immutable and safe for concurrent reads once created. |
| Decoders (`NumericDecoder`, `TextDecoder`) | Not thread-safe and single-use. Create one decoder per blob and call `Decode()` once from one goroutine. |
| BlobSets | Safe for concurrent reads. |
| MaterializedBlobSets | Safe for concurrent reads. |

Encoders are not safe to share across goroutines. If you need parallel encoding of multiple metrics, create one encoder per goroutine and merge the blobs into a BlobSet afterward.

Decoders follow the same rule.
Decode each blob once and share the resulting `NumericBlob` or `TextBlob` between goroutines instead of sharing the decoder.

### Monitor memory for large materializations

Materialized blob sets hold all data points in memory. For large datasets:

- Numeric: ~16 bytes per data point
- Text: ~24 bytes per data point (includes string pointer + backing storage)

For 200 metrics × 1000 points, a numeric materialized set uses ~3.2 MB. Plan accordingly and avoid materializing datasets that would exhaust available memory when combined with other in-flight work.
