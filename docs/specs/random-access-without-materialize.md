# Design: Random access without materializing

**Date:** 2026-10-03
**Status:** Draft.
The shared-timestamp `TimestampAt` fix is implemented; the metric handle API is proposed, not started.

## Goal

Let callers that read only a few points per blob skip `Materialize()`,
which decodes every metric and allocates the whole blob,
when the blob's encodings already support cheap random access.
`Materialize()` itself keeps its current contract.

## Evidence

Production shape: 100 metrics × 150 points per blob,
shared DeltaPacked timestamps, 2-decimal values with half the points repeating the previous value.
Each operation reads value and timestamp at k random (metric, index) pairs.
Single-binary scratch measurement, 3 runs each; read the numbers as orders of magnitude:

| k lookups | ALP, `NumericBlob` | ALP, `NumericBlob` with cached shared timestamps | ALP, `Materialize()` then look up | Chimp, `NumericBlob` | Chimp, `Materialize()` then look up |
|---|---|---|---|---|---|
| 1 | 112 ns | 41 ns | 42 µs | 258 ns | 66 µs |
| 100 | 15.8 µs | 4.2 µs | 39 µs | 50 µs | 69 µs |
| 1,000 | 167 µs | 44 µs | 54 µs | 604 µs | 83 µs |
| 15,000 (every point) | 2.6 ms | 0.92 ms | 0.28 ms | 9.4 ms | 0.33 ms |

`Materialize()` allocates 264 KB per call on this blob.

- With ALP values and cached shared timestamps, direct lookups beat `Materialize()` up to about 900 lookups,
  about 6% of the blob's points.
- With Chimp, `ValueAt` replays the XOR chain from the start of the column (O(index)),
  so the break-even is about 150 lookups.
- Each direct lookup costs about 41 ns, while ALP's `At` itself costs about 8 ns.
  The rest is per-call setup: the metric ID map lookup, payload slicing and decoder construction.

## Decisions

- **`Materialize()` is not made lazy.**
  Its documented contract is "everything decoded, owned, safe for concurrent reads".
  A `NumericBlob` may alias the caller's input bytes:
  uncompressed payloads go through the no-op decompressor (`compress/noop.go:61`) with either decoder constructor,
  and `NewNumericDecoderBorrowed` also borrows metric names.
  A materialized blob is the documented way to drop that dependency on the input buffer;
  a lazy result would keep it, and would turn a one-time decode cost into per-lookup costs hidden behind the same API.
- **Shared timestamps are read from the open-time cache** (implemented).
  `NumericDecoder` already pre-decodes every timestamp sequence shared by more than one metric (`buildSharedTsCache`),
  and `AllTimestamps`, `ForEachTimestamps` and `Materialize` already read it, but `TimestampAt` re-decoded the payload.
  (`All` and the full-point `ForEach` still decode the timestamp payload.)
  It now checks the cache first, so `TimestampAt` on a shared-timestamp metric is O(1)
  for every timestamp encoding; `BlobSet` lookups go through the same function.
  `BenchmarkNumericBlob_TimestampAt_SharedTimestamps`, 6 runs:
  index 0: 25.6 → 20.1 ns, index 75: 95.5 → 19.9 ns, index 149: 168 → 21.2 ns.

## Proposal: metric handle

A handle resolves a metric once and then serves lookups without per-call setup:

```go
h, ok := blob.Metric(metricID)    // or blob.MetricByName(name)
v, ok := h.ValueAt(i)
ts, ok := h.TimestampAt(i)
n := h.Len()
```

- The handle holds the metric's index entry, its value and timestamp slices, and its cached shared timestamps, if any.
- `ValueAt` dispatches on the value encoding once per call without map lookups:
  ALP (and the proposed ALP-RLE) reads bits directly, Raw reads an offset, Gorilla and Chimp stay O(index).
- The handle is a value type, valid as long as the blob is; it aliases the blob's bytes exactly as `NumericBlob` does.
- It is safe for concurrent reads, like `NumericBlob`.

Gate, measured on the production shape before the API is exported:

- direct ALP `ValueAt` + `TimestampAt` through a handle at most 12 ns per lookup (currently 41 ns);
- break-even against `Materialize()` at 20% of the blob's points or more.

If the gate fails, the handle is not exported; the timestamp fix and the guidance below stand on their own.

## Guidance (user docs)

| encodings | few lookups per blob | most points, or repeated passes |
|---|---|---|
| ALP or Raw values, shared or Raw timestamps | `NumericBlob.ValueAt` / `TimestampAt` | `Materialize()` |
| Gorilla or Chimp values, or per-metric Delta/DeltaPacked timestamps | `Materialize()` beyond ~150 lookups | `Materialize()` |

## Out of scope

Lazy or per-metric cached materialization, changes to `MaterializedNumericBlob`,
and random access for per-metric Delta/DeltaPacked timestamps.
