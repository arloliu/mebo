# ForEach: Callback-Style Iteration API

| | |
|---|---|
| **Date** | 2026-06-13 |
| **Platform** | AMD Ryzen 9 9950X3D (Zen 5), linux/amd64, Go 1.26.1 |
| **Scope** | `blob.NumericBlob` — new public API (`ForEach`, `ForEachByName`) |
| **Format impact** | None — read-side only |
| **Predecessor** | [ITERATE_CLOSURE_OPTIMIZATION.md](ITERATE_CLOSURE_OPTIMIZATION.md) ("Remaining levers" #1) |

> **User guide**: see [Callback Iteration with ForEach](../ADVANCED_USAGE.md#callback-iteration-with-foreach).

## Summary

Round 3 established that `All()`'s residual cost is the `iter.Seq2` API
itself: the returned iterator must be a heap-allocated closure, the caller's
range loop body escapes to the heap because it is passed to a dynamic func
value, and one adapter hop per element converts the decoder's `(ts, val)`
into `NumericDataPoint`. A direct callback chain measured 4.9 ns/pt against
`All()`'s 6.8 — closable only by an API addition.

`ForEach(metricID, yield func(int, NumericDataPoint) bool) bool` is that
addition. The signature mirrors `All` exactly (same index, same struct), so
migration between the two is mechanical. `ForEachByName` covers name lookup.
Implementation notes:

- `forEachDataPoint` mirrors the `allDataPoints` dispatch but invokes the
  combo-specific iterator bodies directly with the user's yield. The
  `allDataPoints*` variants are inlinable, so their closures are constructed
  and consumed in-frame — they never escape.
- The two flagship combos (delta+gorilla, delta+chimp, no tags) run the
  fused decode loop **inline in a static function**
  (`forEachDeltaGorilla` / `forEachDeltaChimp`) via exported incremental
  decoder states (`DeltaTsState`, `GorillaValState`, `ChimpValState`), making
  the user's yield the only indirect call per element. This is the same loop
  shape that measured ~20% *slower* inside a heap-closure body in round 3 —
  in a static function it recovers the adapter hop instead (243 → 207 µs).
  The states' doc comment records this constraint.
- Other combos reuse the inlinable Seq2 variants; the packed-timestamp
  combos keep their priming logic in one place (`fused_each.go`), and
  deltapacked-raw keeps its SIMD bulk path.

## Results (200 metrics × 200 points, delta+gorilla, full scan of all fields)

| Path | ns/op | ns/pt | B/op | allocs/op |
|---|---|---|---|---|
| `for i, dp := range nb.All(id)` | 281 µs | 7.0 | 20.8 KB | 601 |
| `nb.ForEach(id, fn)` (hoisted fn) | 208 µs | 5.2 | 40 B | 2 |
| codec floor (direct fused callback) | 196 µs | 4.9 | 0 | 0 |

−26% wall time vs `All()`, allocation-free in practice (the 2 allocs are the
user's callback closure escaping once per scan — hoist it outside the
metric loop, as the e2e benchmark does). ForEach reaches within 6% of the
measured codec floor; the remainder is the `NumericDataPoint` pass and the
per-metric dispatch.

## Verification

- `TestNumericBlob_ForEach_MatchesAll`: ForEach output equals All across all
  9 timestamp×value encoding combos, with and without tags.
- Early-stop, metric-not-found, and ByName tests.
- Full test suite and `make lint` (0 issues) pass.
- Permanent benchmark: `BenchmarkE2EForEach_DeltaGorilla`.

## Remaining levers

1. **SIMD plan Phase 3** (AVX-512 delta-packed decoder).
2. measurev2 currently scores iteration via `All()`; adding a callback-path
   measurement would surface ForEach on the official scoreboard (harness
   decision).
3. Static inline loops for the remaining XOR combos (raw+gorilla/chimp,
   deltapacked+gorilla/chimp) would shave ~1 ns/pt each; deferred since the
   delta combos are the production defaults.
