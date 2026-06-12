# Blob Iteration Closure & Escape Optimization Report

| | |
|---|---|
| **Date** | 2026-06-13 |
| **Platform** | AMD Ryzen 9 9950X3D (Zen 5), linux/amd64, Go 1.26.1 |
| **Scope** | `blob.NumericBlob` All() iteration paths, `internal/encoding` fused decoders |
| **Format impact** | None — decode-side only; no encoder file touched |
| **Predecessors** | [XOR_CODEC_BITPACK_OPTIMIZATION.md](XOR_CODEC_BITPACK_OPTIMIZATION.md) (round 1), [BLOB_ENCODE_ALLOCATION_OPTIMIZATION.md](BLOB_ENCODE_ALLOCATION_OPTIMIZATION.md) (round 2) |

## Summary

Profiling `NumericBlob.All()` (the iterate scoreboard path) showed the blob
layer added **~35% on top of the raw codec decode cost** (307 µs vs a measured
228 µs floor for 40,000 points): 7 heap allocations per metric from the
iterator closure chain, several of which captured the ~400-byte `NumericBlob`
struct by value, plus one redundant indirect call per element.

Two structural fixes, both verified against an in-package floor benchmark:

1. **Hoist tag dispatch out of the returned closures.** Every
   `allDataPoints*` variant checked `b.HasTag()` *inside* the returned
   `iter.Seq2` closure, forcing the closure to capture the entire
   `NumericBlob`. The dispatch now happens before the closure is built, so
   the returned iterator captures only the payload slices and count.

2. **Callback-style fused decoders (`Each` variants).** The no-tag hot paths
   ranged over `iter.Seq2`-returning fused decoders
   (`for ts, val := range ienc.FusedDeltaGorillaAll(...)`). Escape analysis
   cannot keep a range-over-func loop body on the stack when the iterator is
   a dynamically-called closure — the loop body closure, its captured index,
   and the inner iterator all escaped to the heap per `All()` call
   (`-gcflags=-m`: "leaking param: yield", "moved to heap: i"). New
   callback-parameter forms — `FusedDeltaGorillaEach`, `FusedDeltaChimpEach`,
   `FusedDeltaPackedGorillaEach`, `FusedDeltaPackedChimpEach`,
   `FusedDeltaEach`, `FusedGorillaEach`, `FusedChimpEach` — are proven
   non-escaping ("yield does not escape"), keep the adapter and loop state on
   the stack, and are also ~14% faster than the Seq2 forms at codec level
   (4.6 vs 5.4 ns/pt) because the yield target stays stable in a register.
   The Seq2 `Fused*All` forms now delegate to the `Each` forms (one copy of
   each decode loop); their only remaining consumers are tests.

The SIMD-backed `TimestampDeltaPackedDecoder.All` path for
deltapacked-raw was deliberately left unchanged (scalar `Each` would lose the
SIMD bulk decode).

## Results

### In-repo e2e benchmark (200×200, `nb.All(id)` consuming all fields)

| Metric | Before | After | Δ |
|---|---|---|---|
| BenchmarkE2EIterate_DeltaGorilla | 307 µs | 282 µs | −8% |
| allocs/op | 1401 | 601 | −57% |
| bytes/op | 81.6 KB | 20.8 KB | −75% |

### measurev2 iterate ns/pt (vs round 2)

| Combo | Round 2 | Round 3 | Δ |
|---|---|---|---|
| delta-gorilla (default) | 7.5 | 6.8 | −9% |
| delta-chimp | 10.0 | 9.2 | −8% |
| deltapacked-gorilla | 8.9 | 8.3 | −7% |

Iterate allocations per scan dropped from ~1400 to 601 (XOR-value combos)
/ 801 (raw-value combos) across all 18 combos. Encode numbers unchanged
within noise, as expected for a decode-only change.

### Verification

- Full test suite and `make lint` (0 issues) pass.
- Format stability is structural: no encoder file changed
  (`git diff --stat`: `blob/numeric_blob.go` iterators and
  `internal/encoding/fused.go`/`fused_each.go` decoders only), and all
  round-trip tests pass.
- Permanent codec-level benchmarks added
  (`internal/encoding/fused_bench_test.go`).

## Verified-negative results (do not pursue)

Both alternatives were prototyped and benchmarked before being rejected:

1. **Batch-fused decoding** (decode 64 ts+val into stack arrays per batch,
   then yield from the arrays, amortizing the two non-inlined decode calls):
   ~6% *slower* than the per-point fused loop on the measurev2 data shape.
   The array stores/loads and batch bookkeeping cost more than the saved
   call overhead; SIMD-style batching only pays off when the decode kernel
   itself is vectorized (cf. delta-packed).
2. **Inline decode loops in `blob` via exported incremental decoder states**
   (`NewDeltaTsState`/`Next`/`Ts` etc., constructing `NumericDataPoint`
   inline so the user yield is the only indirect call): ~20% *slower*
   (330 µs) than the `Each`-adapter form (282 µs) despite one fewer indirect
   call per element and confirmed cross-package inlining of the wrappers.
   The compiler optimizes the static loop inside `internal/encoding`
   substantially better than the same loop in a heap-allocated closure body.

## Remaining levers

1. **Consumer-side floor**: a direct `Each`-style consumption of the fused
   decoder measures 197 µs (4.9 ns/pt) vs `All()`'s 282 µs — the residual
   cost is the `iter.Seq2` API itself (one adapter hop + the returned heap
   closure + ~3 allocs/metric, two of them on the caller's side of the range
   statement). Closing it would require a public callback-style API
   (e.g. `ForEach(metricID, func(i int, dp NumericDataPoint) bool)`); the
   scoreboard measures `All()`, so this is an API decision, not a pure
   optimization.
2. **`FinishInto(dst []byte)`** encode API (round-2 report).
3. **SIMD plan Phase 3** (AVX-512 delta-packed decoder).
