# Shared Timestamps

This document describes the shared timestamp feature in Mebo — an optional optimization that deduplicates identical timestamp sequences across metrics within a single blob.

## Overview

In typical monitoring workloads, all metrics (CPU, memory, disk, network) are collected at the same intervals. Without deduplication, a blob with 150 metrics × 10 data points stores 150 identical copies of the same timestamp sequence. Shared timestamps detects this redundancy at encode time, stores the timestamps once, and maps all sharing metrics to the single copy.

**Key results:**
- **24–73% blob size savings** (depends on metrics-to-points ratio and timestamp encoding)
- **34% faster decode** due to smaller blob size
- **44–49% faster timestamp iteration** via pre-decoded cache (at 100–200 data points per metric)
- **Zero impact on non-shared workloads** — the optimization is entirely opportunistic

## When to Use

**Enable shared timestamps when:**
- Most metrics in a blob share the same collection intervals (same timestamps, same count)
- Blob size reduction is a priority (e.g., network transfer, storage cost)
- All consumers have been upgraded to a Mebo version that supports V2 decoding

**Skip shared timestamps when:**
- Each metric has unique timestamps (e.g., event-driven data)
- The blob contains very few metrics (< 5) — overhead outweighs benefit
- Consumers cannot be upgraded before producers

## Usage

```go
encoder, _ := mebo.NewNumericEncoder(time.Now(),
    blob.WithTimestampEncoding(format.TypeDelta),
    blob.WithValueEncoding(format.TypeGorilla),
    blob.WithSharedTimestamps(),  // Enables detection; implies V2 layout
)
```

`WithSharedTimestamps()` automatically enables V2 layout. Metrics with different timestamps are stored normally — only identical sequences are deduplicated.

### What Happens at Encode Time

After all metrics are added and `Finish()` is called:

1. Each metric's encoded timestamp bytes are hashed (xxHash64)
2. Metrics with matching hash and byte length are grouped as candidates
3. Candidates are verified with `bytes.Equal` (eliminates hash collisions)
4. Groups with ≥ 2 identical members become shared timestamp groups
5. The first member in each group becomes the **canonical** — its timestamp bytes are kept
6. Other members' timestamp bytes are removed; their index entries point to the canonical's data
7. A compact mapping table is written between the metric index and timestamp payload

If no sharing is detected (all metrics have unique timestamps), no table is written, no flag is set, and the blob is identical to a non-shared V2 blob.

### What Happens at Decode Time

1. The decoder checks the shared timestamps flag bit (bit 3 of Options)
2. If set, reads the mapping table from between the metric index and timestamp payload
3. `ApplySharedTimestampTable` mutates index entries in-place — shared metrics' `TimestampOffset` and `TimestampLength` are overwritten to match their canonical
4. `buildSharedTsCache` pre-decodes timestamps for offsets referenced by multiple metrics and stores them in a `map[int][]int64`
5. Subsequent `AllTimestamps`, `All`, or `Materialize` calls hit the cache instead of re-decoding

## Binary Format

The shared timestamp table is positioned between the Metric Index and the Timestamps Payload:

```
┌──────────────────────────┐
│       Blob Header        │
├──────────────────────────┤
│   Metric Names (opt.)    │
├──────────────────────────┤
│      Metric Index        │
├──────────────────────────┤ ← indexEnd
│ Shared Timestamp Table   │  ← Only present when flag bit 3 = 1
├──────────────────────────┤ ← TimestampPayloadOffset
│   Timestamps Payload     │
├──────────────────────────┤
│     Values Payload       │
├──────────────────────────┤
│      Tags Payload        │
└──────────────────────────┘
```

### Table Structure

```
[GroupCount: uint16]
  For each group:
    [CanonicalIndex: uint16]           // Index of the metric that stores the timestamp bytes
    [MemberCount: uint16]              // Number of shared (non-canonical) members
    [SharedIndices: uint16 × N]        // Indices of metrics referencing this canonical
```

**Size formula:** `2 + Σ(4 + 2 × MemberCount_i)` bytes

### Example

150 metrics, all sharing the same 10-point timestamp sequence:

```
GroupCount = 1
  Group 0:
    CanonicalIndex = 0             // Metric 0 stores the timestamp bytes
    MemberCount = 149              // Metrics 1–149 share metric 0's timestamps
    SharedIndices = [1, 2, ..., 149]

Table size = 2 + (2 + 2 + 149×2) = 304 bytes
Timestamp savings = 149 copies × ~80 bytes/sequence = ~11,920 bytes
Net savings = ~11,616 bytes
```

For the full binary format specification, see the [Shared Timestamp Table section in DESIGN.md](DESIGN.md#shared-timestamp-table-optional-v2-only).

## Performance

### Compression Savings

Benchmarked with 200 metrics × 250 data points (50,000 total points, no additional compression codec):

| Configuration                | Bytes/Point | Space Savings | vs Non-Shared |
|------------------------------|-------------|---------------|---------------|
| Shared Delta + Chimp         | 6.35        | 60.5%         | −23% smaller  |
| Shared Delta + Gorilla       | 6.60        | 59.0%         | −23% smaller  |
| Shared Delta + Raw           | 8.10        | 49.6%         | −25% smaller  |
| Delta + Chimp (no sharing)   | 8.30        | 48.4%         | —             |
| Delta + Gorilla (no sharing) | 8.63        | 46.3%         | —             |
| Delta + Raw (no sharing)     | 10.80       | 32.8%         | —             |
| Raw + Raw (baseline)         | 16.06       | 0%            | —             |

The savings scale with the metric-to-unique-timestamp ratio. With 150 metrics sharing one 10-point sequence, shared timestamps eliminate 149 out of 150 timestamp copies.

### Decode Cost

The one-time decode overhead of building the cache (150 metrics × 100 points, compressed):

| Stage       | Without Cache | With Cache | Δ    |
|-------------|---------------|------------|------|
| Decode only | 10.6 µs       | 13.7 µs    | +29% |

The +3 µs overhead comes from scanning index entries for duplicate offsets and pre-decoding the shared sequence into the cache.

### Iteration Benefit

AllTimestamps iteration across all metrics (V2 shared timestamps, compressed):

| Scenario              | Without Cache | With Cache | Speedup    |
|-----------------------|---------------|------------|------------|
| 150 metrics × 10 pts  | 14.8 µs       | 12.5 µs    | 15% faster |
| 150 metrics × 100 pts | 50.4 µs       | 28.3 µs    | 44% faster |
| 200 metrics × 200 pts | 117.4 µs      | 59.5 µs    | 49% faster |

### Combined Decode + Iteration

Net effect for a typical decode-then-iterate workflow (150 metrics × 100 points):

| Scans After Decode | Without Cache | With Cache | Net Speedup |
|--------------------|---------------|------------|-------------|
| 1                  | 61.1 µs       | 42.0 µs    | 31% faster  |
| 2                  | 111.5 µs      | 70.3 µs    | 37% faster  |
| 5                  | 262.7 µs      | 155.2 µs   | 41% faster  |

**Break-even point:** At 10 points per metric, the cache roughly breaks even on first use. At 100+ points, the cache is a clear win even on the first scan.

### Memory

| Scenario              | Without Cache      | With Cache          | Δ                   |
|-----------------------|--------------------|---------------------|---------------------|
| 150 metrics × 10 pts  | 15.0 KB / 6 allocs | 20.8 KB / 12 allocs | +5.8 KB / +6 allocs |
| 150 metrics × 100 pts | 32.1 KB / 6 allocs | 39.5 KB / 12 allocs | +7.4 KB / +6 allocs |

The extra allocations are: refcount map, cache map, and decoded `[]int64` slices (one per unique timestamp group).

## How It Works Internally

### Encoder: Detection and Deduplication

```
                    Finish()
                       │
                       ▼
        ┌──────────────────────────┐
        │  Encode all metrics      │
        │  (timestamps → bytes)    │
        └─────────────┬────────────┘
                      │
                      ▼
        ┌──────────────────────────┐
        │  detectSharedTimestamps  │
        │  Hash each metric's TS   │
        │  bytes with xxHash64     │
        │  Group by (hash, length) │
        │  Verify with bytes.Equal │
        └─────────────┬────────────┘
                      │
              ┌───────┴───────┐
              │ sharing found │
              │   ≥ 2 match?  │
              └───┬───────┬───┘
              No  │       │ Yes
              ▼   │       ▼
     Normal V2    │  buildDedupTsPayload
     blob output  │  - Keep canonical bytes only
                  │  - Remove shared copies
                  │  - Recompute index deltas
                  │  - Build mapping table
                  │  - Set flag bit 3
                  │       │
                  │       ▼
                  │  Write table between
                  │  index and TS payload
                  └───────┘
```

### Decoder: Apply and Cache

```
                    Decode()
                       │
                       ▼
        ┌──────────────────────────┐
        │  Parse header, index,    │
        │  decompress payloads     │
        └─────────────┬────────────┘
                      │
                      ▼
        ┌──────────────────────────┐
        │  Flag bit 3 set?         │
        │  ApplySharedTimestampTable│
        │  - Mutate shared entries │
        │    to canonical offset   │
        └─────────────┬────────────┘
                      │
                      ▼
        ┌──────────────────────────┐
        │  buildSharedTsCache      │
        │  - Count offset refs     │
        │  - Pre-decode offsets    │
        │    used by > 1 metric    │
        │  - Store in map[int][]i64│
        └─────────────┬────────────┘
                      │
                      ▼
        ┌──────────────────────────┐
        │  AllTimestamps /         │
        │  Materialize             │
        │  - Check cache first     │
        │  - Decode on miss        │
        └──────────────────────────┘
```

## Constraints and Edge Cases

| Scenario                           | Behavior                                                                      |
|------------------------------------|-------------------------------------------------------------------------------|
| Single metric                      | No detection runs (needs ≥ 2 metrics)                                         |
| All metrics unique timestamps      | Flag not set, no table written, blob identical to plain V2                    |
| Mixed: some shared, some unique    | Only shared groups are deduplicated; unique metrics keep their own timestamps |
| Different timestamp counts         | Not grouped (encoded byte length must match before hash comparison)           |
| Same values but different encoding | Not grouped (comparison is at the encoded byte level, not decoded values)     |
| Hash collision (xxHash64)          | Eliminated by `bytes.Equal` verification — zero false positives               |
| V1 decoder reading V2+shared blob  | Safely rejected (different magic number `0xEA20` vs `0xEA10`)                 |

## Backward Compatibility

```
                         Timeline
    ─────────────────────────────────────────────►

    Phase 1: Deploy V2-capable consumers
    ┌─────────────────────────────────────┐
    │  Consumers: V2 decoder (accepts V1) │
    │  Producers: V1 encoder (no change)  │
    └─────────────────────────────────────┘

    Phase 2: Enable shared timestamps on producers
    ┌─────────────────────────────────────┐
    │  Consumers: V2 decoder ✓            │
    │  Producers: WithSharedTimestamps()  │
    └─────────────────────────────────────┘
```

- V2 decoders accept both V1 and V2 formats transparently
- V1 decoders reject V2 blobs (different magic number) — no silent data corruption
- The shared timestamps flag is independent of V2 layout — `WithBlobLayoutV2()` alone does not enable sharing

## Related Documentation

- [DESIGN.md — Shared Timestamp Table](DESIGN.md#shared-timestamp-table-optional-v2-only): Binary format specification, validation rules, and error handling
- [PERFORMANCE_V2.md](PERFORMANCE_V2.md): Full encoding matrix with shared timestamp variants
- [DESIGN.md — Layout Version](DESIGN.md#layout-version): V1 vs V2 layout differences
