# FinishInto: Buffer-Reusing Blob Finalization

| | |
|---|---|
| **Date** | 2026-06-13 |
| **Platform** | AMD Ryzen 9 9950X3D (Zen 5), linux/amd64, Go 1.26.1 |
| **Scope** | `blob.NumericEncoder`, `blob.TextEncoder` — new public API |
| **Format impact** | None — `Finish()` delegates to the same assembly code; byte-identical |
| **Predecessor** | [blob_encode_allocation_optimization.md](blob_encode_allocation_optimization.md) ("Remaining levers" #2) |

> **User guide**: see [Buffer Reuse with FinishInto](../advanced_usage.md#buffer-reuse-with-finishinto)
> for usage patterns and ownership rules.

## Summary

After round 2, the final blob `make([]byte, blobSize)` in `Finish()` was the
remaining allocation of the encode path — **88% of allocated bytes**
(~0.39 MB/op on the 200×200 reference workload). It is unavoidable while
`Finish()` must return a caller-owned fresh slice.

This round adds the documented `FinishInto(dst []byte) ([]byte, error)` API
to both encoders, with standard append semantics:

- The blob is appended to `dst`; content up to `len(dst)` is preserved.
- If `dst` has sufficient capacity, **no allocation occurs**.
- On error, `dst` is returned unchanged (safe for `buf, err = enc.FinishInto(buf)`).
- `Finish()` is now `finishAppend(nil)` — same code path, byte-identical
  output, no behavior or performance change.

Callers encoding blobs in a loop reuse one buffer:

```go
var buf []byte
for ... {
    enc, _ := blob.NewNumericEncoder(start, ...)
    ... add metrics ...
    buf, err = enc.FinishInto(buf[:0])
    // buf must not be reused while a decoder still references its contents
}
```

## Results (200 metrics × 200 points, delta+gorilla, per-point AddDataPoint)

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| E2EEncode (Finish) | 446 µs | 393 KB | 34 |
| E2EEncodeInto (FinishInto, reused buffer) | 422 µs | 42 KB | 33 |

−5% wall time, −89% allocated bytes per encode. The remaining 42 KB/op is
index-entry bookkeeping (`addEntryIndex`, `StartMetricID`), pool-miss noise,
and per-encoder setup.

## Verification

- `TestNumericEncoder_FinishInto` / `TestTextEncoder_FinishInto`:
  byte-equality with `Finish()`, prefix preservation, capacity reuse
  (same backing array), decoder round-trip, error path returns dst unchanged.
- Full test suite and `make lint` (0 issues) pass.
- Permanent benchmark: `BenchmarkE2EEncodeInto_DeltaGorilla`.

## Remaining levers

1. **SIMD plan Phase 3** (AVX-512 delta-packed decoder).
2. **Iterate consumer floor** — see
   [iterate_closure_optimization.md](iterate_closure_optimization.md):
   a public callback-style iteration API could close the remaining
   6.8 → 4.9 ns/pt gap (API decision).
3. Index-entry bookkeeping is now the largest encode allocation
   (~22 KB/op slice growth); a `WithMetricCountHint` option could pre-size
   it, but the absolute cost is small.
