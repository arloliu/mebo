# ForEach: Callback-Style Iteration API

| | |
|---|---|
| **Date** | 2026-06-13 |
| **Platform** | AMD Ryzen 9 9950X3D (Zen 5), linux/amd64, Go 1.26.1 |
| **Scope** | `blob.NumericBlob` / `blob.NumericBlobSet` — push iteration API (`ForEach`, `ForEachValues`, `ForEachTimestamps`, +`ByName`); single-column & BlobSet variants added 2026-06-21 |
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

## Why two APIs (`All*` vs `ForEach*`), not just a faster `All*`

`ForEach*` is **behaviorally identical** to the matching `All*` — same values,
same order, same indices (the equivalence tests assert this byte-for-byte). So
the natural question is: why ship a parallel API instead of making `All*`
faster?

Because the cost is the `iter.Seq` **contract**, not the implementation. The
two allocations and the slower hot loop are baked into the signature and no
internal optimization removes them:

1. `AllValues` returns `iter.Seq[float64]` = `func(yield func(float64) bool)`.
   That returned closure must be heap-allocated — it captures the decoder,
   payload slice and count and is returned across the method boundary.
2. The caller's `for v := range blob.AllValues(id)` body is compiled into a
   `func(float64) bool` and handed to that opaque iterator, so the loop body
   escapes to the heap too.
3. For the stateful codecs (Gorilla/Chimp/Delta) the decode cursor lives
   *inside* the returned heap closure, so the hot loop does heap loads/stores
   instead of register operations — this is the bulk of the wall-clock gap, not
   just allocation amortization.

`ForEach*` is the only shape that removes all three: it returns nothing (no
iterator closure), takes the callback as a plain parameter down a static call
chain (the callback stays on the caller's stack), and runs the decode loop in a
static function (cursor in registers). **You cannot reach this floor without
changing the signature** — which is why it is an API addition, not an internal
tweak.

They are the two standard Go iteration idioms, and they are not redundant:

| | `All*` (pull / `iter.Seq`) | `ForEach*` (push / callback) |
|---|---|---|
| Idiom | `for v := range blob.AllValues(id)` | `blob.ForEachValues(id, fn)` |
| Composes with | `slices.Collect`, stdlib iterator adapters; can be stored and passed around | nothing — must be consumed inline |
| Allocation | ~3 allocs/call + heap-resident decode cursor | ~0 allocs/call + register-resident cursor |
| Use it for | ergonomics, general use, composition | hot read paths (TSDB scans) |

`All*` stays the idiomatic default; `ForEach*` is the zero-allocation escape
hatch for the path that matters. Behavior is locked together by the equivalence
tests, so `ForEach*` can never silently drift from `All*`.

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

## Extension: single-column and BlobSet ForEach (2026-06-21)

The original round added `ForEach`/`ForEachByName` (full data point). This
extension applies the same push-API shape to the single-column iterators and to
`NumericBlobSet`, so the zero-allocation path is available whether a caller
wants both columns, only values, or only timestamps.

New public methods (`blob` package):

- `NumericBlob.ForEachValues` / `ForEachValuesByName`
- `NumericBlob.ForEachTimestamps` / `ForEachTimestampsByName`
- `NumericBlobSet.ForEach` / `ForEachValues` / `ForEachTimestamps` (+ `…ByName`)

Implementation notes:

- Single-column dispatch (`forEachValuesFromEntry` / `forEachTimestampsFromEntry`)
  mirrors `decodeValues` / `allTimestampsFromEntry` and routes to static decode
  functions in `internal/encoding/fused_each.go`: Gorilla→`FusedGorillaEach`,
  Chimp→`FusedChimpEach`, raw→`RawValuesEach`/`RawTimestampsEach`,
  delta→`FusedDeltaEach`, deltaPacked→`FusedDeltaPackedEach` (new). The
  shared-timestamp cache fast path is honored. ALP (and any future codec
  without a static `Each`) drains `decodeValues` — identical to `AllValues`
  (no `iter.Pull` for a single column), so no regression, it just forgoes the
  stack-state speedup.
- Unlike the data-point path there is **no adapter hop** to remove for a single
  column (`decoder.All` already yields the scalar directly). The win is
  therefore the stack-resident decode cursor (≈ the same effect documented in
  [ITERATE_CLOSURE_OPTIMIZATION.md](ITERATE_CLOSURE_OPTIMIZATION.md)) plus the
  elimination of the per-call iterator and loop-body allocations.
- `RawTimestampsEach` carries a `len(data)%8 != 0` guard to stay byte-identical
  with `TimestampRawDecoder.All` (which rejects unaligned payloads); the raw
  *value* decoder does not reject them, so `RawValuesEach` deliberately omits
  the guard.
- `NumericBlobSet.ForEach*` delegate to the per-blob `ForEach*` and remap each
  blob's local index to a continuous global index (matching the set's `All`).
  They compound two wins: removing the set-level iterator closure *and* reaching
  the per-blob static loops. The six methods share one generic engine,
  `forEachAcrossBlobs[K,T]`, with the per-blob method passed as a method
  expression so each call site is a one-liner. The generic form's closure
  carries a runtime dictionary that adds ~16 B per metric (≈ +800 B/scan) of
  transient garbage versus a hand-written method, with **no change in
  allocation count or wall time** — accepted as a readability trade.

### Single-blob results (200 metrics × 200 points, delta+gorilla)

| Path | ns/op | B/op | allocs/op |
|---|---|---|---|
| `for v := range nb.AllValues(id)` | 184 µs | 16.0 KB | 601 |
| `nb.ForEachValues(id, fn)` | 137 µs | 24 B | 2 |
| `for ts := range nb.AllTimestamps(id)` | 148 µs | 16.0 KB | 601 |
| `nb.ForEachTimestamps(id, fn)` | 120 µs | 24 B | 2 |

Values −25%, timestamps −18%; 601→2 allocs, 16 KB→24 B (the residual 2 allocs
are the hoisted callback, once per scan).

### BlobSet results (10 blobs × 50 metrics × 200 points, delta+gorilla)

| Path | ns/op | B/op | allocs/op |
|---|---|---|---|
| `set.AllValues` | 527 µs | 49.6 KB | 1601 |
| `set.ForEachValues` | 407 µs | 2.4 KB | 102 |
| `set.AllTimestamps` | 388 µs | 49.6 KB | 1601 |
| `set.ForEachTimestamps` | 285 µs | 2.4 KB | 102 |
| `set.All` (data points) | 730 µs | 66.4 KB | 1651 |
| `set.ForEach` | 577 µs | 2.4 KB | 102 |

−21% to −26% wall time, 1601→102 allocs (~94% fewer); the residual ~2 allocs
per metric are the set's per-call global-index adapter closure.

Verification: `TestNumericBlob_ForEachValues_MatchesAll` /
`…ForEachTimestamps_MatchesAll` (all 9 ts×val combos, ±tags, shared-TS cache),
early-stop / not-found / nil-yield / ByName, and the BlobSet equivalence tests
including the sparse (metric-absent-in-some-blobs) global-index case. Permanent
benchmarks: `BenchmarkE2E{ForEach,Iterate}{Values,Timestamps}_DeltaGorilla` and
`BenchmarkNumericBlobSet_{All,ForEach}{,Values,Timestamps}`.

## Remaining levers

1. **SIMD plan Phase 3** (AVX-512 delta-packed decoder).
2. measurev2 currently scores iteration via `All()`; adding a callback-path
   measurement would surface ForEach on the official scoreboard (harness
   decision).
3. Static inline loops for the remaining XOR combos (raw+gorilla/chimp,
   deltapacked+gorilla/chimp) would shave ~1 ns/pt each; deferred since the
   delta combos are the production defaults.
