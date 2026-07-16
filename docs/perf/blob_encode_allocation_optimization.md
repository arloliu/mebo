# Blob Encode Allocation & Hot-Path Optimization Report

| | |
|---|---|
| **Date** | 2026-06-12 |
| **Platform** | AMD Ryzen 9 9950X3D (Zen 5), linux/amd64, Go 1.26.1 |
| **Scope** | `internal/pool`, `blob.NumericEncoder` per-point path, `TimestampDeltaEncoder` varint emission |
| **Format impact** | None — encoded bytes identical across all 18 measurev2 combos |
| **Predecessor** | [xor_codec_bitpack_optimization.md](xor_codec_bitpack_optimization.md) (same investigation, round 1) |

## Summary

After the XOR codec rewrite, profiling the end-to-end encode flow (200 metrics
× 200 points, per-point `AddDataPoint`) showed the next bottleneck was not CPU
work but **allocation churn: 78% of all allocated bytes came from
`pool.ByteBuffer.Grow`**. The payload buffers (~240 KiB for Gorilla values)
exceeded the pool's 128 KiB retention threshold, so every encode dropped the
grown buffer and re-grew from 16 KiB through ~10 reallocs with 25% growth
steps — ~1.1 MiB of copy traffic per blob, plus the GC pressure to match
(~15% of encode CPU in GC/runtime frames).

Three independent fixes, each verified with benchmarks:

1. **Pool retention threshold 128 KiB → 1 MiB** (`BlobBufferMaxThreshold`):
   payload-sized buffers are now returned to the pool and reused, making
   steady-state buffer growth zero. `sync.Pool` still releases idle buffers at
   GC, so retention is temporary and bounded.
2. **Pool growth policy: double instead of +25%** for buffers > 64 KiB: first
   touch (or oversized blobs) now take ~4 reallocs instead of ~10.
3. **Hot-path micro-fixes**:
   - `AddDataPoint` limit check uses a struct counter (`curPoints`) instead of
     an interface `tsEncoder.Len()` call per point.
   - `header.Flag.HasTag()` cached as a bool field for the per-point path.
   - `AddDataPoints` skips tag encoding entirely when tag support is disabled
     (previously it allocated `make([]string, n)` and encoded n empty tags
     into a buffer that `Finish` never reads).
   - `TimestampDeltaEncoder` varint emission unrolled for 1/2/3-byte values
     (the ≥ 99% case for real timestamps) in `appendUnsigned`,
     `writeVarintBatch`, and `writeVarintDirect`, replacing the per-byte
     `binary.AppendUvarint` loop. Identical bytes (LEB128 is canonical).

A permanent end-to-end benchmark (`blob/numeric_e2e_bench_test.go`) mirroring
the measurev2 reference workload was added so this flow can be profiled
in-repo with `-cpuprofile`.

## Results

### In-repo e2e benchmarks (40,000 points, per-point AddDataPoint)

| Benchmark | Before round 2 | After | Δ |
|---|---|---|---|
| E2EEncode_DeltaGorilla | 581 µs / 1.82 MB / 49 allocs | 448 µs / 0.39 MB / 34 allocs | −23% time, −78% bytes |
| E2EEncode_DeltaRaw | 486 µs / 2.28 MB / 51 allocs | 334 µs / 0.46 MB / 34 allocs | −31% time, −80% bytes |
| TimestampDeltaEncoder WriteSlice 10k irregular | 20.7 µs (baseline) | 15.4 µs | −26% |

### measurev2 scoreboard — cumulative for the whole investigation (rounds 1+2)

Encode / iterate ns per point, 200×200, vs the session baseline:

| Combo | Encode | Iterate | Alloc/encode |
|---|---|---|---|
| raw-raw | 17.3 → 7.9 (−54%) | unchanged | 4.2 MB → 0.7 MB |
| delta-raw | 13.4 → 8.1 (−39%) | unchanged | 2.2 MB → 0.4 MB |
| delta-gorilla (default) | 17.9 → 11.2 (−38%) | 11.8 → 7.5 (−36%) | 1.8 MB → 0.4 MB |
| delta-chimp | 19.4 → 13.9 (−28%) | 14.5 → 10.0 (−32%) | 1.5 MB → 0.4 MB |
| deltapacked-raw | 11.9 → 8.7 (−27%) | unchanged | 2.2 MB → 0.5 MB |
| deltapacked-gorilla | 17.5 → 11.5 (−34%) | 13.9 → 8.9 (−36%) | 1.8 MB → 0.4 MB |

All 18 combos improved on encode (−25% to −54%); all 9 Gorilla/Chimp combos
improved on iterate (−21% to −38%); raw-value iterate unchanged as expected;
`encoded_bytes` identical for every combo.

### Verification

- Full test suite and `make lint` pass; pool and blob tests unchanged.
- Byte-for-byte format stability confirmed via measurev2 `encoded_bytes`
  equality across all combos.
- The `AddFromRows` vs manual-loop comparison showed the bulk `WriteSlice`
  path is only ~7% faster than per-point `AddDataPoint` after these fixes,
  confirming interface dispatch per point is no longer a major cost.

## Remaining levers (verified, not yet implemented)

1. **Iterate closure chain**: `All()` allocates ~5 closures per metric
   (`All` → `allFromEntry` → `allDataPoints` → codec-specific wrapper → fused
   iterator), ~1.4k allocs per 200-metric scan, est. ≤ 1 ns/pt. A
   callback-with-index fused variant could remove one hop per element.
2. **Finish-side copies**: the final blob `make` + `copy` (~0.4 MB/op) is the
   remaining allocation; unavoidable while `Finish()` returns a fresh slice,
   but a `FinishInto(dst []byte)` API could let callers reuse buffers.
3. **SIMD plan Phase 3** (AVX-512 delta-packed decoder) and **Phase 6**
   (batch-fused decoders) from `docs/plans/2026-04-10-simd-optimization.md`.
