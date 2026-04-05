# Advanced Usage

This document covers advanced features of Mebo beyond the basic encode/decode workflow described in the [README](../README.md).

## Table of Contents

- [Bulk Insertion](#bulk-insertion)
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
