# Metric Names

This document describes the metric-names lifecycle in Mebo — when metric name strings are stored
on the wire, what enumeration and membership guarantees they provide, and what is lost when they
are removed.

## Overview

Every metric in a blob has a 64-bit `MetricID` (either supplied directly via `StartMetricID`, or
computed as the xxHash64 hash of a string via `StartMetricName`). The *name itself* — the
original string — is only optionally stored alongside it, in a dedicated names payload between
the header and the metric index (flag bit `MetricNamesMask`, `0x0004`). Whether that payload is
present depends on encoder mode and options; see [When Names Are Stored](#when-names-are-stored)
below.

The distinction matters because `HasMetricName` / `GetByName` / `*ByName` iterators answer
differently depending on whether the names payload is present:

- **Names present:** exact string match. `HasMetricName("foo")` is `false` unless `"foo"` was
  literally one of the stored names.
- **Names absent:** hash membership. `HasMetricName("foo")` hashes `"foo"` and checks whether
  that hash equals a stored `MetricID`. This is indistinguishable from exact match *unless* the
  query string happens to hash-collide with a name that actually was stored — an astronomically
  unlikely but real possibility with a 64-bit hash space. See
  [Enumeration and Membership](#enumeration-and-membership).

## When Names Are Stored

| Encoder                       | Default                              | Opt-in / opt-out                    | Collision override                                 |
|--------------------------------|---------------------------------------|--------------------------------------|-----------------------------------------------------|
| Numeric, ID mode (`StartMetricID`) | Never stored (no name exists)     | N/A                                   | N/A — ID mode never tracks names                     |
| Numeric, Name mode (`StartMetricName`) | Not stored, unless a collision occurs | `blob.WithMetricNames()` forces it on | A detected hash collision always forces names on, option or not |
| Text, Name mode (`StartMetricName`) | **Always stored**                | `blob.WithoutMetricNames()` opts out  | A detected hash collision always forces names on, option or not |

"Collision" here means two *distinct* names hashing to the same 64-bit `MetricID` — the only case
where the ID alone cannot tell the metrics apart. When that happens, storing the names is not
optional: it is the only way a decoder can later resolve `GetByName` to the right entry instead of
an arbitrary one. `WithMetricNames()` / `WithoutMetricNames()` control the *no-collision* case only.

```go
// Numeric: force names on even when no collision occurs.
enc, _ := blob.NewNumericEncoder(start,
    blob.WithMetricNames(),
)
// blob.WithMetricNames() is Name-mode only: StartMetricID returns
// ErrMetricNamesUnavailable once it is set — ID mode has no name to store.

// Text: opt out of the default (names are stored unless a collision forces them back on).
txtEnc, _ := blob.NewTextEncoder(start,
    blob.WithoutMetricNames(),
)
```

## Enumeration and Membership

- **`MetricNames()`** returns every stored name, in index order. If the blob has no names payload
  (no `WithMetricNames`, no collision, or after a strip), it returns no names at all —
  there is no way to recover a name from a hash, so enumeration is lost entirely, not degraded.
  The empty result is not the same value on every type:
  - `NumericBlob` and `TextBlob` return an empty, non-nil slice (`[]string{}`).
  - `MaterializedNumericBlob`, `MaterializedTextBlob`, `NumericBlobSet`, `TextBlobSet`,
    `MaterializedNumericBlobSet`, and `MaterializedTextBlobSet` return `nil`.

  Test for "no names" with `len(names) == 0`, not `names == nil`.
- **`HasMetricName` / `GetByName` / `*ByName` iterators** keep working either way (they never
  simply fail on a names-free blob), but their *precision* differs:
  - With names retained: the decoder hashes the query, locates the candidate entry, and
    additionally string-compares the query against the entry's retained stored name before
    returning — exact membership, negligible extra cost (a few nanoseconds; see
    [Performance](#performance)) over the collision-only path.
  - With names absent (never stored, or stripped): only the hash comparison happens. A query that
    hash-collides with a stored name will false-positive.
  - Blob sets and materialized blob sets keep the same hash fallback for names-free members,
    so a set that mixes names-bearing and stripped blobs still answers `*ByName` queries for metrics that only the stripped blobs carry.

This is why `WithMetricNames()` exists as a first-class *feature*, not just a debugging aid: a
service that needs `GetByName` to reject an unknown-but-hash-colliding name has to keep the names
around. Everything else — the overwhelming majority of workloads, where the query set is exactly
the set of names that were stored — gets identical answers either way.

## Stripping Names

`StripMetricNames` (allocating, `dst`/`src`) and `StripMetricNamesInPlace` (in-place, `buf`)
remove the names payload from an already-encoded blob when it is safe to do so, without a
decode/re-encode round trip:

```go
out, stripped, err := blob.StripMetricNames(nil, encoded)
if err != nil {
    // encoded is malformed within strip's validation boundary — see the doc comment
    // on StripMetricNames for exactly what is (and is not) checked.
}
if stripped {
    // out is byte-for-byte what the encoder would have produced without names.
} else {
    // Load-bearing names (a real collision) or already names-free — out == encoded, unchanged.
    // This is a normal outcome, not an error: keep using the blob as-is.
}
```

Strip only ever removes a payload that is provably redundant: it re-verifies that every stored
name hashes to its entry's `MetricID` before concluding it is safe to drop them. Two distinct
outcomes fall out of that check, and they are not the same thing:

- **Duplicate name** (the same name string stored twice) means the blob is malformed — strip
  returns `err = errs.ErrDuplicateMetricName` and drops nothing.
- **Duplicate ID with distinct names** (a real collision: two different names hashing to the same
  `MetricID`) is not malformed — it means the names are load-bearing. Stripping is refused
  (`stripped == false`, `err == nil`) because that blob cannot correctly answer `GetByName` by hash
  alone.

### What stripping costs

Both of these apply the moment a blob's names payload is gone — whether that blob was encoded
without names in the first place, or was names-bearing and then stripped. There is no difference
in behavior between the two; strip does not create a degraded blob, it produces exactly the
ordinary no-names blob the encoder would have produced.

1. **Enumeration.** `MetricNames()` returns no names:
   an empty slice or `nil`, depending on the type (see [Enumeration and Membership](#enumeration-and-membership)).
   A consumer that needs to enumerate metrics by name needs a side dictionary
   (e.g. keep the original name list wherever it decided to strip).
2. **Exact negative membership.** `HasMetricName` / `GetByName` / `*ByName` degrade from exact
   string match to hash membership, per [Enumeration and Membership](#enumeration-and-membership)
   above. Concretely:

   ```go
   // Before stripping: only "cpu.usage" was ever stored.
   b.HasMetricName("cpu.usage")     // true  — matches the stored name
   b.HasMetricName("unrelated.xyz") // false — no stored name hashes to this query, and even if
                                     //         it did, the exact-match check would reject it

   // After stripping:
   b.HasMetricName("cpu.usage")     // true  — same answer, hash membership happens to agree
   b.HasMetricName("unrelated.xyz") // false, UNLESS "unrelated.xyz" happens to hash-collide with
                                     //         "cpu.usage" under xxHash64 — then this is now true,
                                     //         a false positive that was impossible before stripping
   ```

   This is intrinsic to removing the names — it is exactly how every blob that was never
   names-bearing in the first place already behaves. It is not a strip-specific weakness; it is
   what "the names are gone" *means*. Callers that need collision-proof membership after this
   point must not strip (keep the names), or must switch to ID-based access
   (`HasMetricID` / `GetByID`), which was never approximate.

`stripped == true` means "names removed; the blob now has ordinary no-names semantics" — never
"the blob is otherwise unchanged for `*ByName` callers."

## Decode Cost and Borrowed Decode

Carrying names costs real decode time and allocations — see [Performance](#performance) — because
`NewNumericDecoder` / `NewTextDecoder` copy every name into an owned string. `NewNumericDecoderBorrowed`
and `NewTextDecoderBorrowed` decode names as zero-copy strings that **alias the input buffer**
instead:

```go
dec, err := blob.NewNumericDecoderBorrowed(data)
// The caller must keep `data` alive and unmutated for the lifetime of the decoded blob and
// anything derived directly from its names.
```

Materializing a borrowed blob (`Materialize()`, producing a `MaterializedNumericBlob` /
`MaterializedTextBlob`) **clones** its names, so materialized objects are always independently
owned — the borrowed-lifetime rule never propagates past the raw blob. The regular (owning)
constructors are unchanged; both pairs are pinned by compile-time signature assertions so they
stay interchangeable as typed function values.

## Performance

Measured on `go1.26.1 linux/amd64`, AMD Ryzen 9 9950X3D (`go test ./blob/ -bench ...`); numbers
will vary by hardware and are directional, not guarantees (see [API_STABILITY.md](../API_STABILITY.md)).

### Size overhead of storing names

Little-endian, default (delta timestamp / Gorilla value) encoding, no tags:

| Shape                        | No names | With names | Delta            |
|-------------------------------|---------:|-----------:|-------------------|
| 200 metrics × 10 pts, 25-char names  | 21.8 KB | 27.2 KB | +5.4 KB (+25%)  |
| 200 metrics × 10 pts, 40-char names  | 21.8 KB | 30.2 KB | +8.4 KB (+38%)  |
| 200 metrics × 100 pts, 25-char names | 185.7 KB | 191.1 KB | +5.4 KB (+3%) |
| 50 metrics × 10 pts, 25-char names   | 5.5 KB | 6.8 KB | +1.4 KB (+25%)    |
| 1000 metrics × 10 pts, 30-char names | 107.6 KB | 139.6 KB | +32.0 KB (+30%) |

The names payload is the only section Mebo never compresses, so the overhead is proportional to
metric count and name length, and shrinks (as a percentage) as points-per-metric grows — more
payload amortizes the fixed names cost.

### Encode cost

Turning names on (`WithMetricNames()` / text's names-on-by-default vs `WithoutMetricNames()`)
costs close to nothing at encode time — within noise across three repeated runs at both shapes:

| Shape                | No names   | With names | Delta       |
|-----------------------|-----------:|-----------:|------------:|
| Numeric, 10m × 100p   | 112 005 ns | 113 617 ns | +1.4%       |
| Numeric, 200m × 10p   | 308 175 ns | 317 942 ns | +3.2%       |
| Text, 10m × 100p      | 151 077 ns | 151 829 ns | +0.5%       |
| Text, 200m × 10p      | 341 208 ns | 350 134 ns | +2.6%       |

Name-mode encoding already computes `hash.ID(name)` for every metric whether or not names are
stored; `WithMetricNames()` only adds appending the name to an entry-parallel slice, so the
marginal encode cost is negligible regardless of shape.

### Decode cost: names vs no names, and owning vs borrowed

The overhead below is strictly tied to whether the decoded bytes actually contain a names
section — not to whether Name-mode was used to identify metrics, or whether the metric-names
feature exists in the codebase. A numeric blob with no `WithMetricNames()` and no collision
carries zero names bytes and decodes exactly as fast as a plain ID-mode blob (verified directly:
`StartMetricID` vs `StartMetricName`-without-`WithMetricNames()` decode within noise of each
other, same allocation count) — the cost only appears once names are actually on the wire, and
it then scales with **metric count**, not point count, since it's one string allocation per name:

| Shape               | No names  | With names | Delta  |
|----------------------|----------:|-----------:|-------:|
| Numeric, 10m × 100p  |  4 877 ns |   5 094 ns | +4.4%  |
| Numeric, 200m × 10p  | 14 989 ns |  21 352 ns | +42.5% |
| Text, 10m × 100p     |  8 315 ns |   8 917 ns | +7.2%  |
| Text, 200m × 10p     | 14 405 ns |  20 111 ns | +39.6% |

Text stores names **by default** (opt-out via `WithoutMetricNames()`), so an unmodified
`NewTextEncoder` pays this cost unless you opt out; numeric is opt-in only
(`WithMetricNames()`), so it costs nothing unless you ask for it or a collision forces it on.

At 200 metrics × 10 points, owning vs borrowed decode of that names-bearing blob:

| Decoder            | ns/op  | B/op   | allocs/op |
|---------------------|-------:|-------:|----------:|
| Numeric, owning     | 17 542 | 49 928 | 206       |
| Numeric, borrowed    | 14 298 | 35 518 | 6         |
| Text, owning         | 17 027 | 52 245 | 210       |
| Text, borrowed       | 14 543 | 41 215 | 10        |

Roughly 200 of the owning path's allocations are the per-name string copies; the borrowed
constructors eliminate essentially all of them — use them whenever the input buffer's lifetime
outlives the decoded blob and you have many metrics. `HasMetricName` / `GetByName` on a
names-bearing, no-collision blob cost ~16-19 ns/op with zero allocations either way — the
string-compare added by [exact membership](#enumeration-and-membership) is negligible.

### Accessing a decoded blob: by ID vs by name

Once decoded, `All(id)`/`ValueAt(id, ...)` and their `AllByName`/`ValueAtByName` equivalents
resolve to the same index entry and share identical per-point decode work afterward — only
entry resolution differs, and `ByName`'s extra hash + string-compare (see the prior section) is
a **fixed cost per call**, not per point:

| Access                        | By ID    | By name  | Delta       |
|--------------------------------|---------:|---------:|------------:|
| `ValueAt`, 10m × 100p metric   |  22.3 ns |  29.2 ns | +6.9 ns     |
| `ValueAt`, 200m × 10p metric   |  21.2 ns |  31.4 ns | +10.2 ns    |
| `All`, 10m × 100p metric       | 762.8 ns | 767.5 ns | within noise |
| `All`, 200m × 10p metric       | 173.4 ns | 171.6 ns | within noise |

The single-point-access delta (~7-10 ns) is the clean signal — it doesn't scale with point
count or metric count, since it's the same one-time hash+compare regardless of blob size. Full
iteration (`All`) pays that same fixed cost once before iterating every point, so as a
percentage it shrinks with more points per metric and is within measurement noise here; prefer
`ByName` freely unless you're calling it in a tight per-point loop instead of once per metric.

### Materialize cost

`Materialize()` clones the whole names slice once, rather than per name, so its overhead vs a
names-free blob is within noise at both shapes — unlike decode, it does not scale with metric
count:

| Shape               | No names  | With names |
|----------------------|----------:|-----------:|
| Numeric, 10m × 100p  |  3 433 ns |   3 138 ns |
| Numeric, 200m × 10p  | 16 115 ns |  17 116 ns |
| Text, 10m × 100p     | 27 620 ns |  27 008 ns |
| Text, 200m × 10p     | 74 610 ns |  75 658 ns |

### Strip cost

200-metric blob:

| Operation                          | ns/op | allocs/op |
|--------------------------------------|------:|----------:|
| Validate only (no output produced)    | ~1 440 | 0        |
| `StripMetricNames` (allocates output) | ~2 000 | 1        |
| `StripMetricNamesInPlace`              | ~1 900 | 1        |
| Full decode + re-encode without names | ~185 000 | 1 042 |

Stripping is roughly two orders of magnitude cheaper than a decode/re-encode round trip, and the
validation step itself allocates nothing — the cost is dominated by re-hashing every stored name
to prove the strip is safe, not by copying bytes.

## Standardised Collision Behaviour

v1.10.0 also standardises previously undefined behaviour around collided metric IDs (two distinct
names hashing to one ID) — which entry an ID-keyed lookup resolves to, how `MetricCount` /
`MetricIDs()` count a collided ID, and rejection of malformed/unsorted input. These apply
regardless of whether names are stored or stripped and are documented in
[API_STABILITY.md — Standardised Behaviour](../API_STABILITY.md#standardised-behaviour-v1100), not
duplicated here.

## Cross-Version Compatibility

Every wire-format concept described here (the names payload, its flag bit, V1/V2/V2Ext layouts)
already existed before v1.10.0 — a pre-v1.10.0 decoder already knows how to read a names-bearing
or names-free blob, because that is exactly what the collision-triggered path already produced.
v1.10.0 only adds new *public* ways to reach those shapes (`WithMetricNames`, `WithoutMetricNames`,
strip, the out-of-order-insertion fix) plus new decode-time rejections for previously-silently-
mishandled input (duplicate names, unsorted V2 index). Concretely, for a v1.9.0 ↔ v1.10.0 pair:

- A v1.9.0 reader decodes any v1.10.0-produced names-bearing, names-free, or stripped blob.
- A v1.9.0 reader decodes a v1.10.0 `WithMetricNames` blob built from out-of-order name insertion
  (the class of blob a pre-v1.10.0 *encoder* could corrupt when forced to store names — v1.10.0
  fixes production, not just consumption).
- A v1.10.0 reader rejects two shapes no valid producer (any version) ever created: a blob with a
  repeated stored name, and a V2/V2Ext index whose `MetricID`s are not sorted.

See `tests/compat/run_compat.sh` (and the `metricnames`-tagged scenarios in
`tests/compat/scenarios_metricnames.go` / `tests/compat/mncorrupt_metricnames.go`) for the
executable version of this matrix.

## Related Documentation

- [design.md — Metric Names Payload](design.md#metric-names-payload-optional): binary format
  specification for the names section.
- [API_STABILITY.md](../API_STABILITY.md): additive symbols and standardised behaviour for
  v1.10.0.
- [shared_timestamps.md](shared_timestamps.md): another optional V2 payload, for comparison of
  style/structure.
