# Advanced Usage

This document covers advanced features of Mebo beyond the basic encode/decode workflow described in the [README](../README.md).

## Table of Contents

- [Bulk Insertion](#bulk-insertion)
- [Buffer Reuse with FinishInto](#buffer-reuse-with-finishinto)
- [Callback Iteration with ForEach](#callback-iteration-with-foreach)
- [BlobSet: Multi-Blob Queries](#blobset-multi-blob-queries)
- [BlobSet: Materialized Random Access](#blobset-materialized-random-access)
- [Tags](#tags)
- [Custom Hash IDs](#custom-hash-ids)

---

## Bulk Insertion

`AddDataPoints` accepts pre-built slices of timestamps and values and is 2–3× faster than individual `AddDataPoint` calls for large datasets, due to reduced function call overhead and better memory locality.

```go
startTime := time.Now()
encoder, _ := mebo.NewDefaultNumericEncoder(startTime)
metricID := mebo.MetricID("cpu.usage")

// Prepare data in bulk
timestamps := make([]int64, 1000)
values := make([]float64, 1000)
for i := 0; i < 1000; i++ {
    timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
    values[i] = float64(i * 10)
}

// Declare, bulk-insert, and complete in one pass
encoder.StartMetricID(metricID, 1000)
encoder.AddDataPoints(timestamps, values, nil) // nil = no tags
encoder.EndMetric()
```

With tags:

```go
tags := make([]string, 1000)
for i := 0; i < 1000; i++ {
    tags[i] = fmt.Sprintf("host=server%d", i%10)
}

encoder.StartMetricID(metricID, 1000)
encoder.AddDataPoints(timestamps, values, tags)
encoder.EndMetric()
```

---

## Buffer Reuse with FinishInto

`Finish()` allocates a fresh slice for every blob it returns — on the 200-metric
reference workload that is ~0.4 MB of garbage per encode, which is ~88% of all
bytes the encode path allocates. That allocation is unavoidable for `Finish()`
because the caller owns the returned slice indefinitely, so the encoder can
never recycle it.

`FinishInto(dst []byte)` removes it by letting *you* own the memory. It appends
the encoded blob to `dst` and returns the extended slice, following the same
append semantics as the standard library (`strconv.AppendInt`,
`encoding/binary.Append`):

- Content up to `len(dst)` is preserved; the blob occupies the appended region.
- If `dst` has enough spare capacity, **no allocation happens at all**.
- On error, `dst` is returned unchanged, so `buf, err = encoder.FinishInto(buf)`
  is always safe.
- `FinishInto(nil)` is exactly equivalent to `Finish()`.

Both `NumericEncoder` and `TextEncoder` support it.

### Basic usage

```go
var buf []byte // reused across encodes

for batch := range incoming {
    encoder, _ := mebo.NewDefaultNumericEncoder(batch.StartTime)

    for _, m := range batch.Metrics {
        encoder.StartMetricID(m.ID, len(m.Points))
        for _, p := range m.Points {
            encoder.AddDataPoint(p.Ts, p.Val, "")
        }
        encoder.EndMetric()
    }

    blobBytes, err := encoder.FinishInto(buf[:0]) // reuse buf's capacity
    if err != nil {
        return err
    }

    writeToStorage(blobBytes) // copy out or finish using before next encode
    buf = blobBytes           // keep the grown buffer for the next round
}
```

The first iteration allocates once; every following iteration encodes with
zero blob allocation (the buffer is already large enough after a few rounds).
On the reference workload this cuts encode allocations from ~393 KB/op to
~42 KB/op (−89%) and wall time by ~5%.

### Ownership rule

The returned slice aliases `dst`. Do **not** reset and reuse the buffer while
anything still references the previous blob's contents — including a decoder:
`NewNumericDecoder(blobBytes)` keeps reading from that memory. Reuse the buffer
only after the blob has been fully consumed (written to disk/network, copied,
or all decoding finished).

### When to use it

| Use case | Why it helps |
|---|---|
| **Ingest pipeline / TSDB writer** — encode one blob per flush interval, write it to disk or object storage, repeat | The blob is consumed (written) immediately, so one buffer can serve every flush. Steady-state encode produces near-zero garbage, keeping GC pause pressure flat regardless of flush rate. |
| **Network shipping with framing** — append a length prefix or envelope header to `dst` first, then `FinishInto(dst)` | The blob is serialized directly behind your framing bytes in one contiguous buffer — no second copy to assemble the message. |
| **Batch re-encoding / compaction** — read N old blobs, re-encode into new ones in a loop | Same buffer cycles through the whole compaction run instead of allocating per blob. |
| **Concatenated blob files** — append multiple blobs back-to-back into one buffer before a single write | Call `FinishInto(buf)` repeatedly *without* resetting: each call appends the next blob after the previous one. |

### When to stick with Finish()

- One-off or low-frequency encoding — the single allocation is irrelevant.
- The blob's lifetime is unclear or long (cached, shared across goroutines):
  owning a fresh slice per blob is simpler and safer than tracking when a
  shared buffer may be recycled.

---

## Callback Iteration with ForEach

`All()` returns a Go 1.23 range-over-func iterator — idiomatic and pleasant,
but the `iter.Seq2` shape has an irreducible cost: the iterator is a
heap-allocated closure, and the compiler must also move your range loop body
to the heap because it is passed into a dynamically-called function. On a
200-metric scan that is ~600 small allocations and ~25% of iteration time.

`ForEach` is the callback (push) equivalent. It yields exactly the same
`(index, NumericDataPoint)` sequence, but through a static call chain that
keeps everything on the stack:

```go
// Iterator style (idiomatic, allocates per metric):
for i, dp := range nb.All(metricID) {
    process(i, dp)
}

// Callback style (hot paths, allocation-free):
nb.ForEach(metricID, func(i int, dp NumericDataPoint) bool {
    process(i, dp)
    return true // return false to stop early
})
```

`ForEach` returns `false` if the metric does not exist (it returns `true`
when iteration was merely stopped early). `ForEachByName` is the name-lookup
variant.

### Performance

On the 200×200 reference workload (delta+gorilla, the default configuration),
a full scan through `ForEach` runs **~26% faster** than `All()` —
5.2 vs 7.0 ns per point — and performs **2 allocations instead of ~600**.

To make a scan fully allocation-free, hoist the callback out of the metric
loop so its closure is created once:

```go
var sum float64
fn := func(_ int, dp NumericDataPoint) bool { // created once
    sum += dp.Val
    return true
}
for _, id := range metricIDs {
    nb.ForEach(id, fn)
}
```

### When to use which

- **`All()`** — application code, readability first. The cost is small in
  absolute terms and the `for range` form composes with `break`/`continue`
  naturally.
- **`ForEach`** — hot read paths that scan many metrics or run per query:
  query engines, downsampling/aggregation jobs, format converters, anything
  where iteration shows up in profiles.

---

## BlobSet: Multi-Blob Queries

A `NumericBlobSet` groups time-ordered blobs and provides unified iteration across all of them. Blobs are automatically sorted by start time.

### Numeric blobs across time windows

```go
blob1, _ := encoder1.Finish() // hour 0
blob2, _ := encoder2.Finish() // hour 1
blob3, _ := encoder3.Finish() // hour 2

// Pass in any order — BlobSet sorts by start time automatically
blobSet, _ := mebo.NewNumericBlobSet([]blob.NumericBlob{blob3, blob1, blob2})

cpuID := mebo.MetricID("cpu.usage")
for dp := range blobSet.All(cpuID) {
    fmt.Printf("ts=%d, val=%f\n", dp.Ts, dp.Val)
}

// Inspect the covered time range
start, end := blobSet.TimeRange()
fmt.Printf("data from %s to %s\n", start, end)
```

### Mixed numeric and text blobs

```go
blobSet := mebo.NewBlobSet(
    []blob.NumericBlob{numericBlob1, numericBlob2},
    []blob.TextBlob{textBlob1, textBlob2},
)

// Iterate numeric with global index
for index, dp := range blobSet.AllNumerics(metricID) {
    fmt.Printf("global[%d]: ts=%d, val=%f\n", index, dp.Ts, dp.Val)
}

// Iterate text with global index
for index, dp := range blobSet.AllTexts(statusID) {
    fmt.Printf("global[%d]: ts=%d, val=%s\n", index, dp.Ts, dp.Val)
}
```

### Global index random access

The global index addresses data points sequentially across all blobs. If `blob1` covers 100 points and `blob2` covers 150, then global indices 0–99 map to `blob1` and 100–249 map to `blob2`.

```go
value, ok := blobSet.NumericValueAt(metricID, 150) // blob2, local index 50
timestamp, ok := blobSet.TimestampAt(metricID, 50) // blob1, local index 50
dp, ok := blobSet.NumericAt(metricID, 125)         // full DataPoint with ts, val, tag
```

---

## BlobSet: Materialized Random Access

Materialization decodes all data points from all blobs into memory once, providing O(1) access (~5 ns/op) at the cost of ~16 bytes/point memory.

Use materialization when you need frequent random access to many data points (more than ~100 accesses justifies the upfront cost of ~100 µs per metric per blob).

```go
// One-time materialization cost (~100 µs per metric per blob)
matNumeric, err := mebo.NewMaterializedNumericBlobSet(numericBlobs)
if err != nil {
    log.Fatal(err)
}

// O(1) random access — ~5 ns per op
val, ok := matNumeric.ValueAt(metricID, 500)
ts, ok := matNumeric.TimestampAt(metricID, 500)
```

For text blobs:

```go
matText, err := mebo.NewMaterializedTextBlobSet(textBlobs)
val, ok := matText.ValueAt(statusID, 75)
```

Via a mixed BlobSet:

```go
blobSet := mebo.NewBlobSet(numericBlobs, textBlobs)
matNum := blobSet.MaterializeNumeric()
matTxt := blobSet.MaterializeText()
```

**Avoid materialization when:**
- Only sequential iteration is needed — use `BlobSet.All()` instead.
- Memory is constrained (~16 bytes × total data points).
- Only a handful of data points need to be accessed.

---

## Tags

Tags are optional per-point string metadata. Enable them on the encoder and read them back during decoding.

Tags add ~8–16 bytes of overhead per data point. Enable them only when per-point metadata is actually needed.

```go
startTime := time.Now()
metricID := mebo.MetricID("cpu.usage")

// Option 1: use the tagged factory function
encoder, _ := mebo.NewTaggedNumericEncoder(startTime)

// Option 2: pass the option manually
encoder, _ := mebo.NewNumericEncoder(startTime, blob.WithTagsEnabled(true))

encoder.StartMetricID(metricID, 10)
for i := 0; i < 10; i++ {
    ts := startTime.Add(time.Duration(i) * time.Second)
    encoder.AddDataPoint(ts.UnixMicro(), float64(i*10), fmt.Sprintf("host=server%d", i%3))
}
encoder.EndMetric()

blob, _ := encoder.Finish()

// Decode and read tags
decoder, _ := mebo.NewNumericDecoder(blob.Bytes())
decoded, _ := decoder.Decode()
for dp := range decoded.AllWithTags(metricID) {
    fmt.Printf("val=%f, tag=%s\n", dp.Val, dp.Tag)
}
```

---

## Custom Hash IDs

Mebo identifies metrics by `uint64` IDs. If your system already has numeric identifiers, pass them directly:

```go
var myMetricID uint64 = 123456789
encoder.StartMetricID(myMetricID, 100)
// ...
for dp := range decoder.All(myMetricID) { ... }
```

If you only have string names, convert them with `mebo.MetricID`, which applies xxHash64 and is deterministic across runs and machines:

```go
cpuID := mebo.MetricID("cpu.usage")    // uint64
memID := mebo.MetricID("memory.bytes") // uint64

encoder.StartMetricID(cpuID, 100)
```

The collision probability is negligible (~1 in 2⁶⁴). When a collision is detected, Mebo stores the metric name in the blob header for verification.
