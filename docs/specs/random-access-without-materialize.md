# Design: Random access without materializing

**Date:** 2026-10-03, revised 2026-10-08
**Status:** Design settled with the owner on 2026-10-08 (see Decisions); phases 1 to 4 of the implementation are on `feat/numeric-metric-handle`; every gate passed (see Gate results), and the release is next.
The shared-timestamp `TimestampAt` fix shipped in v1.12.0 and the pointer lookups in v1.12.1;
the metric handle below is proposed with scratch prototypes measured.

## Goal

Give a caller that reads one metric from a `BlobSet` or a `NumericBlob` a handle that is resolved once
and then serves random access and iteration without per-call name hashing, index searches, slicing or decoder construction,
and without decoding or allocating anything unless the caller asks.
`Materialize()`, `MaterializeMetric()` and their `BlobSet` forms keep their contracts.

The handle must decide, exactly and per metric, whether `TimestampAt` and `ValueAt` read the point directly or replay the column up to it.
That determination is the heart of this design, because the current `TimestampAt` is direct
only for a timestamp sequence the blob pre-decoded at open, and that depends on more than the encoder options.

## The consumer this serves

`fdc-kernel-platform/shared/tchart/mebo/mebo_tchart.go` wraps one metric name over a `*blob.BlobSet`:

- The metric spans the set's blobs; the wrapper accumulates a `base` offset across them.
- Random access goes through `BlobSet.NumericAtByName` and `TimestampAtByName`.
  Its callers do a binary search over `TimestampAt(mid)` (`search.go`), read `TimestampAt(0)` and `TimestampAt(Size()-1)`,
  and walk points with `NumericAt(i)`.
- Iteration goes blob by blob: `HasMetricName`, then `ForEachTimestampsByName` or `ForEachByName`, then `LenByName`,
  three name resolutions per blob.
- It keeps its own per-metric cache, `numericMetric *MaterializedNumericMetric`, filled by `Materialize()`,
  and every accessor branches on it first.
  Six indicator transforms call `Materialize()` before their loops; `Size()` and `Duration()` have 69 call sites.
- Its numeric blobs are little-endian, shared DeltaPacked timestamps, **Chimp** values, no compression, tags optional
  (`mebo_codec.go`), so for this consumer `ValueAt` is sequential on every blob and `Materialize()` is the normal path.
  It pins mebo v1.10.0; moving to the release that carries the handle crosses no incompatible change
  (Chimp and DeltaPacked bytes are unchanged since v1.10.0, and the v1.12.0 ALP byte change does not touch it).

So the handle lives at the `BlobSet` level, is keyed by name as well as ID, serves both random access and iteration,
and can materialize itself, tags included, so that the wrapper keeps one field and no branch for its numeric charts.

## Evidence

Every number below is a scratch, single-binary measurement at `0f83326` (v1.12.1), medians of three `-benchtime=200ms` runs,
Ryzen 9 9950X3D, Go 1.26.7; read gaps under about 20% as ties.
The prototypes are package-internal handles in temporary `_test.go` files; none was committed.

### One blob: what the current API costs per lookup

Production shape: 100 metrics × 150 points, shared DeltaPacked timestamps, 2-decimal values with half the points repeating the previous value.
One `ValueAt` plus one `TimestampAt` on 100 metrics, index spread over the column:

| blob | `NumericBlob` API | handle, pointer receiver | handle, value receiver |
|---|---:|---:|---:|
| shared DeltaPacked + ALP | 26.7 ns | 10.0 ns | 14.3 ns |
| shared DeltaPacked + ALP-RLE (every column in the runs layout) | 31.0 ns | 16.0 ns | 19.2 ns |
| shared DeltaPacked + Raw values | 21.6 ns | 4.6 ns | 8.2 ns |
| shared DeltaPacked + Chimp | 423 ns | 411 ns | 414 ns |
| Raw timestamps + ALP | 37.7 ns | 11.9 ns | 16.1 ns |
| per-metric Delta + ALP | 123 ns | 100 ns | 101 ns |
| per-metric DeltaPacked + ALP | 121 ns | 95 ns | 97 ns |

- The handle removes the per-call setup: the ID binary search, the shared-group lookup, the two payload slicings and the decoder construction.
- A value receiver costs about 4 ns per call on top of a pointer receiver; the handle's methods take a pointer receiver.
- Holding a pointer to the index entry and holding a copied `Count` measure the same (10.0 against 9.4 ns);
  the part copies what it needs (see Proposal), because `entryFor` and `entryForName` forbid retaining their pointer.
- Chimp and the per-metric timestamp codecs replay the column (sequential), and no handle changes that.

### A set: what the current `ByName` API costs per lookup

Four blobs of the production shape, ALP values, shared DeltaPacked timestamps, metric names retained; one metric of 600 points.

| operation | `BlobSet` API | resolved-once handle |
|---|---:|---:|
| `TimestampAtByName` at index 0 / 300 / 599 | 19 / 42 / 53 ns | `ValueAt` + `TimestampAt` together: 18 / 12 / 13 ns |
| `NumericAtByName` at index 0 / 300 / 599 | 31 / 55 / 67 ns | same |
| iterate every timestamp of the metric, the wrapper's blob-by-blob loop | 927 ns | 172 ns |
| iterate every timestamp, `AllTimestampsByName` | 1,698 ns | 172 ns |
| resolve the metric once | — | 221 ns, 7 allocations (the prototype grows its slices; the implementation counts first) |
| `MaterializeNumericMetricByName` | 1,172 ns, 9,752 B, 3 allocations | — |

The `ByName` cost grows with the number of blobs scanned:
each blob hashes the name, binary-searches its index and compares the string before the index can be placed,
so a point in the fourth blob pays four resolutions.
The handle places the index with a base-offset table and resolves nothing per call.

### The consumer's shape

Four blobs, Chimp values, shared DeltaPacked timestamps, tags on (`host=server1` on every point), names retained; one metric of 600 points.

| operation | cost |
|---|---:|
| `NumericAtByName` at index 0 / 300 / 599 (local index 0 / 0 / 149) | 45 / 68 / 1,204 ns |
| `TimestampAtByName` at index 0 / 300 / 599 | 18 / 44 / 54 ns |
| `MaterializeNumericMetricByName` | 16.7 µs, 29,400 B, 616 allocations (one string per tag) |
| `MaterializedNumericMetric` value + timestamp + tag at one index | 3.3 ns |
| iterate every point with tags, the wrapper's `ForEachByName` loop | 13.1 µs |
| iterate every point over the materialized metric | 1.4 µs |

Chimp makes `NumericAtByName` grow about 8 ns per point of local index, and the tag column costs one string per point to decode either way;
after `Materialize()` the handle's lookups and iteration should match the materialized metric's.

### The timestamp path alone, by index

`TimestampAt` through the per-blob handle, metric 1 and metric 100, index 0, 75 and 149:

| timestamp path | index 0 | index 75 | index 149 |
|---|---:|---:|---:|
| pre-decoded shared group | 1.4 ns | 1.3 ns | 1.4 ns |
| Raw | 3.2 ns | 3.0 ns | 3.0 ns |
| per-metric Delta | 6.8 ns | 88 ns | 166 ns |
| per-metric DeltaPacked | 6.7 ns | 76 ns | 150 ns |
| **shared blob, metric 100 with a unique sequence** | **6.8 ns** | **75 ns** | **150 ns** |

The last row is a blob encoded with `WithSharedTimestamps()` whose metrics 1–99 share one sequence
and whose metric 100 has its own, one microsecond apart.
Metric 1 reads the pre-decoded group; metric 100 walks its DeltaPacked payload, about 1 ns per point.
The encoder options do not decide the path; the metric's membership in a group does.

### The value path alone, by index

| value path | index 0 | index 75 | index 149 |
|---|---:|---:|---:|
| ALP (schemes 0–2) | 7.5 ns | 7.5 ns | 8.9 ns |
| ALP-RLE, runs layout (scheme 3) | 12.0 ns | 12.4 ns | 14.2 ns |
| Raw | 3.0 ns | 3.0 ns | 3.1 ns |
| Chimp | 5.3 ns | 331 ns | 659 ns |

### Materializing inside the handle

A handle that can decode its sequential axes into slices it owns, and then serve them by index, one concrete type throughout:

| blob | before `Materialize()` | after | `Materialize()` cost |
|---|---:|---:|---:|
| shared DeltaPacked + ALP (both axes direct) | 10.5 ns | 10.6 ns | 5.8 ns, no allocation (nothing to decode) |
| per-metric Delta + ALP (timestamps sequential) | 99.6 ns | 10.7 ns | 501 ns |
| shared DeltaPacked + Chimp (values sequential) | 409 ns | 2.6 ns | 645 ns |

The extra `len(values)` branch on the direct path costs about 0.5 ns, within noise.
The prototype held one slice per axis; the design keeps the group's shared slice and the owned slice apart (see the pseudocode above),
one more predictable branch on the timestamp path, to be confirmed within the gate.
The prototype decoded through `iter.Seq` and took five allocations; the implementation uses the bulk decoders and allocates once per axis.
A `MetricReader` interface over the handle and `MaterializedNumericMetric` was considered instead and rejected:
an interface call cannot inline the 10 ns path, and boxing the handle costs an allocation.
Generics do not help either; both candidates share the pointer GC shape, so the call goes through a dictionary.

### The per-metric fallback

| operation | cost | allocations |
|---|---:|---|
| per-blob handle creation | 16 ns | none |
| `MaterializeMetric` (one blob, ALP, cached shared timestamps) | 380 ns | 3 allocations, 2,584 B |
| `MaterializedNumericMetric.ValueAt` + `TimestampAt` | 2.3 ns | none |

The `MaterializeMetric` godoc says "~100 µs (one-time)"; that figure is wrong by two orders of magnitude for this shape and is corrected with this work.

## The decision: which path `TimestampAt` takes

The handle decides once per member blob, at `Metric()` or `MetricByName()`, from three facts the blob already holds:

1. `b.sharedTs.lookup(entry.TimestampOffset)`: the pre-decoded timestamps of this metric's group, or nil.
   `NumericDecoder.buildSharedTimestamps` fills a group only when the blob's shared-timestamp flag is set
   and more than one index entry references the offset (`blob/numeric_decoder.go`, the `refCount` loop).
   A found group's slice is never nil, even when it is empty, so nil means "no group".
2. `b.tsEncType`, the blob-wide timestamp encoding.
3. The entry's `TimestampOffset` and `TimestampLength`, which slice the metric's own payload when there is no group.

| encoder options | this metric in the blob | group at open | `TimestampAt` path | class |
|---|---|---|---|---|
| any | any, timestamp encoding Raw | — | 8-byte read at `index`, O(1) | direct |
| no `WithSharedTimestamps()` | own sequence | none (flag unset, V1 or V2) | Delta or DeltaPacked walk from the column start, O(index) | sequential |
| `WithSharedTimestamps()`, nothing identical found | own sequence | none (no table written, flag unset) | same walk | sequential |
| `WithSharedTimestamps()`, table written | sequence identical to at least one other metric | pre-decoded `[]int64` | `cached[index]`, O(1) | direct |
| `WithSharedTimestamps()`, table written | sequence unique in this blob | none for this offset | same walk | sequential |
| any | any, timestamp encoding other than Raw, Delta, DeltaPacked | — | none | unsupported |

Each member part stores the outcome as data, not as a flag to re-evaluate:
`shared []int64`, the group's pre-decoded timestamps when a group exists (nil otherwise, and never written by the handle),
and always `tsBytes []byte`, the metric's own range-validated slice, with the encoding.
`Materialize()` fills a separate `timestamps []int64` that the handle owns.
`TimestampAt` on a part then does what `timestampAtFromEntry` does today, with the owned slice checked first:

```go
func (p *numericMetricPart) timestampAt(i int) (int64, bool) {
    if i < 0 || i >= p.count { return 0, false }
    if i < len(p.timestamps) { return p.timestamps[i], true } // Materialize()'s full decode; nil never matches
    if i < len(p.shared) { return p.shared[i], true }         // pre-decoded group
    return p.tsDecodeAt(i)                                     // Raw read, or the Delta/DeltaPacked walk over p.tsBytes
}
```

A group whose pre-decoded slice is shorter than `count` (a malformed blob the open-time validation did not reject)
falls through to the payload decoder, as the current accessor does, so the handle and `NumericBlob.TimestampAt` agree on every input.

Two consequences the current API hides:

- Two metrics of one blob, and two parts of one handle, can be in different classes (the unique-sequence row),
  so the class is a property of the part, and the handle reports the worst of its parts.
- V1 blobs and blobs whose shared table was not written are always sequential for Delta and DeltaPacked,
  whatever the encoder was asked for.

## The decision for `ValueAt`

The value encoding is blob-wide, but ALP-RLE chooses the layout per column, so the class is read from the column's scheme byte at resolution:

| value encoding | column | `ValueAt` path | cost | class |
|---|---|---|---|---|
| Raw | — | 8-byte read | O(1) | direct |
| ALP, ALP-RLE | scheme 2 (raw) | 8-byte read | O(1) | direct |
| ALP, ALP-RLE | scheme 0 (main), 1 (RD) | bit unpack at `index`, then a binary search of the column's exception sidecar (`atMain`, `atRD`) | O(log k), k = exceptions in the column, independent of the index | direct |
| ALP-RLE | scheme 3 (runs) | popcount the run bitmap up to `index` (`alpRunsRank`), then the nested column's lookup at that run | O(index/64 + log k): one 64-bit word per 64 points, about 1 ns per word | direct |
| Gorilla, Chimp | — | XOR chain replay from the column start | O(index) | sequential |
| other | — | none | — | unsupported |

The class names the mechanism, not a complexity bound:
**direct** means the lookup never walks the column's points, so its cost does not grow with the index beyond the runs layout's one word per 64 points;
**sequential** means it replays the column from its start, about 1 ns per point for the timestamp codecs and 4 ns per point for Chimp.
A three-class model (direct, indexed, sequential) was considered for the ALP exception search and the runs rank
and rejected: the caller's decision is binary (keep the handle as is, or `Materialize()`), and both sublinear costs are a few nanoseconds at 150 points,
which the table above states exactly.

Tags (`tagAtFromEntry`) walk length-prefixed strings from the column start, so `TagAt` is sequential on every tagged blob.
On a blob without tags, `NumericBlob.TagAt` returns `("", true)` for every valid index, and the handle keeps that contract:
each part records `hasTag` at creation and answers `("", true)` without touching a tag slice.

## Decisions kept from the first draft

- **`Materialize()`, `MaterializeMetric()` and the `BlobSet` forms are not made lazy.**
  Their contract is "everything decoded, owned, safe for concurrent reads".
  A `NumericBlob` may alias the caller's input bytes
  (uncompressed payloads go through the no-op decompressor, and `NewNumericDecoderBorrowed` also borrows metric names);
  a materialized blob is the documented way to drop that dependency,
  and a lazy result would turn a one-time decode into per-lookup costs behind the same API.
- **Shared timestamps are read from the open-time groups** (shipped in v1.12.0, `sharedTimestamps` since v1.12.1).
- **The handle aliases the blobs' bytes**, exactly as `NumericBlob` does, and is valid as long as the blobs are;
  it inherits the blob's requirement that the backing buffer stays unmodified,
  including the lifetime rule of `NewNumericDecoderBorrowed`, and its godoc says so.
  Slices that `Materialize()` creates are owned by the handle and do not change that rule.
- **The handle is safe for concurrent reads**, like `NumericBlob`.
  `Materialize()` is a write: it must not run concurrently with any other method on the same handle value, and the caller serializes it,
  as the wrapper already does for its own cache.
  Copying a handle copies slice headers: copies made before `Materialize()` are independent and each may materialize on its own;
  copies made after share the decoded slices, which are never written again.
  The godoc states this, together with the zero value (every lookup returns false, `Len()` is 0, `ForEach*` yields nothing),
  the backing-byte lifetime, the unsupported-encoding result, what `Materialize()` decodes, and that a callback returning false stops the walk,
  as `NumericBlob.ForEach` documents.
- **Resolution follows the existing lookups**:
  by ID, `entryByID`, where a collided ID resolves to the first entry in index order, as `ValueAt` does today;
  by name, `entryByName`, the by-name map on a collision, hash plus string compare with retained names, and hash plus ID lookup without names,
  as `ValueAtByName` does today.
  On a set, `entryFor` and `entryForName` with the set's logical identity (`excludesStripped`, the numeric-before-text precedence),
  as `BlobSet.TimestampAtByName` does today, so the handle and the `BlobSet` accessors agree on which members contribute.

## Proposal: `NumericMetric`

One exported handle type for one metric, whether it comes from a blob or spans a set:

```go
h, ok := bs.NumericMetricByName(name)   // BlobSet, numeric members only; or bs.NumericMetric(id), nb.Metric(id), nb.MetricByName(name)
h.Materialize()                         // optional: decodes the sequential axes and the tags; no-op and no allocation when nothing needs it

n := h.Len()                        // MetricLenByName
d := h.Duration()                   // MetricDurationByName's numeric branch: last timestamp minus first, in the blob's timestamp unit; 0 when empty
ts, ok := h.TimestampAt(i)          // TimestampAtByName
v, ok := h.ValueAt(i)               // NumericValueAtByName
tag, ok := h.TagAt(i)               // TagAtByName
dp, ok := h.At(i)                   // NumericAtByName
h.ForEach(func(i int, dp NumericDataPoint) bool)   // the wrapper's blob-by-blob loop, base offsets handled; no return value
h.ForEachValues(func(i int, v float64) bool)       // the callback returns false to stop
h.ForEachTimestamps(func(i int, ts int64) bool)
h.TimestampAccess(), h.ValueAccess()               // AccessDirect, AccessSequential or AccessUnsupported; AccessClass has String()
```

`NumericBlob` keeps the short names (`Metric`, `MetricByName`) because its metrics are all numeric;
`BlobSet` carries `Numeric` in the name, as its `MaterializeNumericMetric` and `IsNumericMetric` do.

- `NumericBlob.Metric` / `MetricByName` and `BlobSet.NumericMetric` / `NumericMetricByName` return `(NumericMetric, bool)`.
  The set constructors follow `MaterializeNumericMetricByName` and `IsNumericMetricByName`: they resolve **numeric members only**,
  with the set's logical identity, and return false for a name that lives only in text members.
  `BlobSet.TimestampAtByName`'s fallback to text members is therefore not reproduced; the handle replaces the `Numeric*` accessors
  and the generic `TimestampAtByName`, `TagAtByName` and `MetricLenByName` for metrics that numeric members hold,
  which is every metric the consumer wraps as numeric.
- `NumericMetric` holds a first part inline and the remaining parts in a slice (`first numericMetricPart; rest []numericMetricPart`),
  the layout `sharedTimestamps` already uses.
  The blob form fills `first` only and allocates nothing; the set form counts the contributing members, then allocates `rest` once.
  Each part carries its own `base`, the points before it, so there is no second table.
  Index placement compares against `first`, then scans `rest`; sets are a handful of blobs.
- A part copies what it needs at resolution and holds no pointer into the index:
  `count` and `base`, the value, timestamp and tag slices cut from the payloads, `shared` (the group's pre-decoded timestamps, or nil),
  the owned `timestamps`, `values` and `tags` that `Materialize()` may fill, the blob's endian engine, the two encodings, and two flags.
  `entryFor` and `entryForName` state that their pointer must not be retained, and the copied-`count` variant measured the same as the pointer.
- Methods take a pointer receiver; the type is still used by value (`h := ...; h.ValueAt(i)`), as `bytes.Buffer` is.
- `ValueAt`, `TimestampAt` and `TagAt` dispatch on the part's stored encoding with no map or search per call.
- `ForEach`, `ForEachValues` and `ForEachTimestamps` chain the parts through the `*FromEntry` helpers in `numeric_blob_foreach.go`,
  which keep the decode loop on the stack; a part with pre-decoded or materialized slices ranges over them directly.
  They return nothing: `NumericBlob.ForEach`'s bool means "the metric exists", which a handle settled at construction,
  and a bool meaning "not stopped early" under the same name would mislead.
  The callback returns false to stop, as everywhere in the package.
  No `iter.Seq` forms for now: the consumer avoids `All*` for the heap-allocated iterator, and they can be added later.
- `Duration()` returns the last timestamp of the last part minus the first timestamp of the first part,
  in the blob's timestamp unit (mebo stores the `int64` the caller gave it; the consumer stores microseconds),
  the contract of `calculateDurationByName` behind the numeric branch of `BlobSet.MetricDurationByName`:
  0 when the metric is empty, when either end cannot be read, or when the last timestamp is not after the first (never negative).
  The text fallback of `MetricDurationByName` is not reproduced, as for every set accessor here.
  It reads through the part lookups, so it walks when an end is sequential, and caches nothing.
- `Materialize()` decodes, per part, every axis that is not already direct into slices the handle owns, using the bulk decoders:
  the timestamp axis when it is sequential, the value axis when it is sequential,
  and the tag column whenever the blob has tags (tags are always sequential, and the consumer's `NumericAt` and `AllNumerics` read them),
  as `[]string`, the representation `MaterializedNumericMetric.Tags` uses.
  It returns nothing: the decoders report corruption by stopping early, not by an error, and `TimestampAccess()` answers what happened.
  Direct axes are left alone, so a handle whose axes are all direct and whose blob has no tags allocates nothing.
  It is idempotent.
  A part's axis becomes direct only when its decode produced exactly `count` elements;
  a decoder that stops early on a corrupt stream (`decodeTimestampsSlice` documents this) leaves the owned slice short,
  the axis keeps its class, and lookups past the produced length fall through to the payload decoder, which returns false there,
  the behaviour the current accessors have on that input.
  `Len()` and `ForEach*` are not trimmed; `alignMemberRows` trims only inside `MaterializeMetric`'s owned copy, which keeps its contract.
  After a full `Materialize()`, `At`, `ValueAt`, `TimestampAt` and `TagAt` are all direct, and `TimestampAccess()` and `ValueAccess()` report `AccessDirect`.
  As implemented (2026-10-09): the parts after the first live in an array every copy of the handle shares,
  so `Materialize()` writes to a copy of that array, one allocation, and only when one of those parts has an axis to decode;
  that is what keeps a copy made before it independent.
  A part with no points whose encoding is readable decodes nothing and reports direct; an unsupported axis stays unsupported.
  `ForEach` zips the decoded slices when every axis of a part has one (timestamps decoded or pre-decoded in full),
  and otherwise walks the columns as before but attaches the decoded tags, so decoded tags are never copied again;
  `ForEachValues` and `ForEachTimestamps` range over a column decoded in full.
- `TimestampAccess()` and `ValueAccess()` return the worst class over the parts:
  `AccessSequential` if any part is sequential, `AccessUnsupported` if any is unsupported, otherwise `AccessDirect`.
  No per-part query: a caller can do nothing about one part except `Materialize()` the handle.
  They are for callers that must stay allocation-free and want to decide for themselves; the common caller just calls `Materialize()` or does not.
  `AccessClass` has a `String()`, like `format.EncodingType`.
  A `TagAccess()` is not offered: tags are sequential on every tagged blob, and the godoc says so.
- There is no automatic constructor that materializes on its own: `Materialize()` on a direct-only handle costs 5.8 ns and nothing else,
  so a caller that always calls it loses nothing, and the consumer already has a `Materialize()` hook.
- `NumericBlobSet` gets no constructor for now; it can delegate later in one line.
- The symbols are additive, so they ship in a minor release (`API_STABILITY.md`, "Adding new exported functions, types, or methods").
- Names: `NumericMetric` is the handle, `MaterializedNumericMetric` stays the owned, blob-independent copy.
  No `Set` in the name: like `MaterializedNumericMetric`, which both `NumericBlob` and `BlobSet` produce, the handle is about one metric.

Alternatives considered and rejected:
documentation only, with the guidance table keyed on encodings (the unique-sequence row cannot be expressed as guidance);
a `MetricReader` interface over the handle and `MaterializedNumericMetric` (no inlining, one boxing allocation, see Evidence);
a per-blob handle only (the consumer resolves by name over a set, and `ByName` costs grow with the blob count).

### What the consumer looks like afterwards

```go
type meboBaseTchart struct {
    name   string
    handle blob.NumericMetric   // from fullBlobSet.NumericMetricByName(name); text charts keep their current path
    ...
}

func (m *meboBaseTchart) TimestampAt(i int) (time.Time, error) {
    ts, ok := m.handle.TimestampAt(i)
    ...
}

func (m *meboBaseTchart) AllTimestamps() iter.Seq2[int, time.Time] {
    return func(yield func(int, time.Time) bool) {
        m.handle.ForEachTimestamps(func(i int, ts int64) bool { return yield(i, utils.ConvertUsToDatetime(ts)) })
    }
}

func (m *meboBaseTchart) Materialize() bool { m.handle.Materialize(); return true }
```

The `numericMetric` cache and the branch in every accessor go away.
The text metric keeps its current path until a text handle exists (see Out of scope).

### Gates, before the API is exported

Measured through the real implementation, layout-averaged (four code layouts, medians of their runs),
the method of `docs/specs/measurev2-fast-report-runs-design.md`:
a gate's figure is the median over the four layouts of each layout's median run, and a ratio gate divides the two figures so formed.
The scratch figures in parentheses are the single-binary measurements above and are references, not gate results.

Fixtures, all 100 metrics × 150 points per blob, little-endian, no compression, seed 1, values from `alpRLEGateColumns(0.5, 1)`:
**P** (production), one blob, ALP values, shared DeltaPacked timestamps, IDs only;
**S** (set), four blobs of P with retained names, one metric of 600 points;
**C** (consumer), S with Chimp values and tags on, `host=server1` on every point.
Lookup gates read metric 50 at index `(m*37) % 150` over the 100 metrics of P, and at index 599 (the fourth blob) on S and C.

1. P: `ValueAt` + `TimestampAt` through the handle, together, at most 12 ns per lookup (scratch: 10.0 ns; the current API: 26.7 ns).
2. S: `ValueAt` + `TimestampAt` together at index 599 at most 15 ns (scratch: 13 ns; `TimestampAtByName` alone: 53 ns),
   and `ForEachTimestamps` over the 600 points at most 700 ns, the callback floor of about 1 ns per point
   (phase 2 single binary: 600 ns; the wrapper's loop: 820–940 ns; the scratch 172 ns ranged a slice without a per-point call).
   C: `Materialize()` on a handle resolved fresh in every iteration, against `MaterializeNumericMetricByName` of the same metric in the same iteration shape,
   at most 1.2× (scratch reference: 16.7 µs, 616 allocations), with no more allocations (restated by the owner on 2026-10-09; first written as "the same allocation count");
   then `At` at index 599 at most 4 ns (scratch reference, materialized metric: 3.3 ns)
   and `ForEach` over the 600 points at most 1.7 µs (scratch reference, materialized metric: 1.4 µs; the wrapper's loop: 13.1 µs).
3. Resolution: the blob form at most 25 ns and no allocation (scratch: 16 ns);
   the set form no allocation when one member contributes and exactly one, for `rest`, when two or more do.
4. `Materialize()` on a direct-only, tagless handle: no allocation.
5. `NumericBlob` and `BlobSet` accessors and the iteration paths unchanged within the report's equivalence band,
   because the handle shares `entryByID`, `entryForName`, `sharedTimestamps.lookup` and the `*FromEntry` helpers.
6. `TimestampAccess()` and `ValueAccess()` pinned by tests for every row of the two tables,
   including the shared blob with one unique sequence, a V1 blob, a `WithSharedTimestamps()` blob where nothing was shared,
   an ALP-RLE blob whose columns mix plain and runs layouts, a tagless blob (`TagAt` gives `("", true)`), an empty metric,
   a set whose parts differ in class, and a set where the metric is in some members only.
7. Every handle accessor agrees with the accessor it replaces, on every test blob and set, before and after `Materialize()`:
   `NumericBlob.ValueAt`, `TimestampAt`, `TagAt`, `Len` and the `ForEach*` forms;
   `BlobSet.NumericValueAt`, `NumericAt`, `TimestampAt`, `TagAt`, `MetricLen` and their `ByName` forms, for metrics that numeric members hold.
   Text-only names are out of the comparison: the handle returns false where `BlobSet.TimestampAtByName` falls back to text members.

Every gate blocks export: 1–4 are the performance and allocation gates, 5–7 the correctness gates.
If any fails, the handle is not exported; the tables and the guidance below stand on their own.

**Phase 2 measurements (2026-10-08, single binary) and the decisions they led to (2026-10-09).**
`ForEachTimestamps` over the 600 points costs about 600 ns through the handle,
820–940 ns through the wrapper's blob-by-blob loop and 1,700 ns through `AllTimestampsByName`.
The floor is the per-point indirect call that the callback design requires, about 1 ns per point,
and the scratch figure of 172 ns can only have come from a loop without one.
The owner restated the threshold above to that floor; no iterator form is added (it keeps the per-point call and allocates its closure),
and a chunked callback that yields each part's slice is deferred until the consumer validation shows iteration dominating.
Separately, every `ForEach*` of `NumericBlob` and `NumericBlobSet` leaked its callback to the heap:
the range-over-func drains in the default branches of the column helpers, the generic set helpers' call through a func value,
and the dynamic call through the `allDataPoints*` closures all made escape analysis mark `yield` as leaking,
so a capturing callback literal cost one allocation per call on the existing API and on the handle alike.
The column forms (`ForEachValues`, `ForEachTimestamps`, by ID and by name, on blobs, sets and handles) are fixed on this branch:
the drains are gone (an unknown encoding yields nothing, as its iterator did), long ALP columns stream through a new `NumericALPDecoder.Each`,
and the set helpers call the member loops by name; call-site callback literals are pinned at zero allocations.
The point form (`ForEach`) leaked because none of the eleven `allDataPoints*` variants inlines,
so `forEachDataPoint` called their closures dynamically.
It is fixed on this branch before phase 3 (owner, 2026-10-09):
each closure body is a package-level loop called by name, which the `All*` closures call with the decoders they captured,
and `forEachDataPoint` calls with concrete decoders through a type parameter, so nothing is boxed.
A call-site callback literal is pinned at zero allocations on every encoding pair without tags.
Two costs remain and are pinned: ALP and ALP-RLE values decode both columns into two new slices first,
and on a tagged blob each point's tag is a string copied out of the payload, one allocation per point, which the owner kept.
The tagged ALP path walks the tags with `TagDecoder.Each` instead of `iter.Pull`, about four times faster (single binary, 50 points).
The timing gates run through the padded-layout harness the ALP-RLE gates used (`blob` package benchmarks, four layouts, medians);
the handle's cells are not added to `tests/measurev2` or `docs/performance.md`, which compare codecs, not API paths.

**Gate results (2026-10-09, branch at `343697a`).**
Four code layouts (`internal/pool` padded by 0, 1, 2 and 4 steps), three runs each, `-benchtime 200ms -cpu 1` pinned to one core;
each figure is the median over the layouts of each layout's median, and every layout agreed within 3% unless noted.

| gate | measured | threshold | result |
|---|---:|---:|---|
| 1. P: `ValueAt` + `TimestampAt` | 10.4 ns per lookup (the `NumericBlob` accessors: 26.4) | ≤ 12 ns | pass |
| 2. S: lookup pair at index 599 | 12.0 ns (`BlobSet` ByName: 115) | ≤ 15 ns | pass |
| 2. S: `ForEachTimestamps`, 600 points | 611 ns (blob-by-blob: 875; `AllTimestampsByName`: 1,806) | ≤ 700 ns | pass |
| 2. C: `Materialize()` vs `MaterializeNumericMetricByName` | 11.9 µs vs 12.2 µs, 0.98×; 610 vs 616 allocations | ≤ 1.2×, no more allocations | pass |
| 2. C: `At` at index 599 | 3.0 ns (`MaterializedNumericMetric`'s three accessors: 3.8) | ≤ 4 ns | pass |
| 2. C: `ForEach`, 600 points | 1.30 µs (blob-by-blob: 12.7 µs, 600 allocations; ranging the materialized slices: 305 ns) | ≤ 1.7 µs | pass |
| 3. resolution | blob 17.8 ns by ID, 21.4 ns by name, no allocation; set 169 and 192 ns, one allocation for four members | ≤ 25 ns, none; one for two or more members | pass |
| 4. `Materialize()`, direct and tagless | no allocation (blob and set forms, pinned by test) | none | pass |
| 5. existing accessors and iteration against `main` (`0f83326`) | every shared benchmark within 0.80–1.03× | within the 20% band | pass; the two outside the band are faster |
| 6., 7. classes and parity | pinned by tests, before and after `Materialize()` | — | pass |

`At` first measured 6.6 ns, because it placed the index three times through the three accessors;
it now answers from one placement when the part's columns are slices (`98e9126`).
The C allocation count differs by six in the handle's favour; the gate first asked for the same count,
and the owner restated it to "no more" on 2026-10-09:
the handle allocates its parts after the first once at resolution and once more to copy them before `Materialize()` writes,
then per member one slice for the values, one for the tags and one string per tag,
and it copies no timestamps, which the shared groups already hold.
Gate 5's two cells outside the band are `NumericBlob.ForEach` on 10-point Delta and Chimp columns, 0.80× (20% faster), the callback no longer allocating.

### Validation in the consumer (advisory)

Before the release, a branch of fdc-kernel-platform points `go.mod` at the mebo branch with a `replace` directive,
rewrites `mebo_tchart.go` as sketched above and runs its tests (`mebo_tchart_test.go`, `mebo_tchart_foreach_bench_test.go`).
The result and the branch commit are recorded here.
It does not block the release, because it lives in another repository, but it is the only evidence that the cache and the branches really go away.

**Result (2026-10-09).** A clone of fdc-kernel-platform at `daa524f4e`, branch `mebo-numeric-metric-handle` (local commit `0c95ae650`, not pushed),
with `replace github.com/arloliu/mebo => /home/arlo/projects/mebo` at `343697a`:
`mebo_tchart.go` reads numeric charts through a handle resolved in `NewMeboBaseTchart`, as sketched above,
and the `numericMetric` cache and its branch in every numeric accessor are gone (63 lines added, 185 removed, tests included).
The text path is unchanged.
Its tests pass, as do `shared/tchart/...` and `shared/repos/tchart`;
the tests needed two kinds of change, fixtures that built the chart by struct literal now resolve the handle,
and two tests that inspected the removed cache check the handle's classes instead.
Its fallback benchmarks (single binary, 100 and 1,000 points, with and without tags) run 2–25% faster, geomean −10%,
and the two allocations per call of the untagged point and value loops are gone;
the timestamp loop keeps three, from the wrapper's own iterator.

## Guidance (user docs), rewritten around the handle

| access pattern | use |
|---|---|
| a few lookups or one pass over a metric | `NumericMetricByName` and the handle as is |
| many lookups, a binary search, or repeated passes over one metric | the handle, then `Materialize()` once |
| a copy that must outlive the blobs | `MaterializeMetric` / `MaterializeNumericMetricByName` |
| most metrics of a blob, repeatedly | `Materialize()` on the blob or set |

`Materialize()` on the handle costs nothing when both axes are direct and the blob has no tags, and about one `MaterializeMetric` otherwise,
so calling it before a binary search is never the wrong choice.

## Documentation fixes that ride along

- `MaterializeMetric` godoc: "~100 µs (one-time)" and "~5 ns" random access are replaced by measured figures with their shape.
- `docs/best_practices.md` "Materialize only when random access is frequent": the break-even is per metric, not "roughly 100 random accesses on a dataset".
- `docs/best_practices.md` (the encoding table): Delta and DeltaPacked "always decode sequentially" omits the pre-decoded group case.
- `docs/shared_timestamps.md`: it still says `TimestampAt` re-decodes the canonical bytes on every call and names the old map cache;
  state that sequences shared by two or more metrics are pre-decoded into groups at open, that `TimestampAt` reads them,
  and that a metric with a unique sequence in a shared blob keeps a sequential `TimestampAt`.
- `API_STABILITY.md` (v1.12.0 behaviour changes): "`TimestampAt` on shared timestamps is O(1)" is qualified to a metric whose offset is referenced by at least two entries.

## Out of scope

Lazy or per-metric cached materialization inside `NumericBlob` or `BlobSet`, changes to `MaterializedNumericMetric`,
random access for per-metric Delta and DeltaPacked timestamps (an anchor table is a format change),
and a text handle.
`TextBlob` is row-based and sequential in every column, so a `TextMetric` with the same shape
(resolve once, `ForEach*`, `Materialize()`) would still remove the per-call resolution and the wrapper's second cache;
it is the natural second phase and is not designed here.

## Implementation phases

Each phase is reviewed externally before it is reported done, and each lands as its own commits by scope.

1. `numericMetricPart`, `NumericBlob.Metric` and `MetricByName`, `Len`, `ValueAt`, `TimestampAt`, `TagAt`, `At`,
   `AccessClass` with `TimestampAccess()` and `ValueAccess()`, and the tests that pin every row of the two decision tables.
   The type-level godoc lands here with the type: the backing-byte lifetime, the zero value, copying, concurrent reads,
   and the unsupported-encoding result; every exported method is documented in the phase that adds it (repository rule).
2. `BlobSet.NumericMetric` and `NumericMetricByName` with the `first`/`rest` layout, `Duration`, `ForEach`, `ForEachValues`, `ForEachTimestamps`
   (with the callback-stop semantics in their godoc), and the accessor-parity tests on sets.
3. `Materialize()`: owned slices, full-decode promotion, tags, idempotence,
   and its own godoc (what it decodes, that it is a write, the copy semantics before and after it), which also updates the type-level text.
4. The documentation fixes, the gate runs, the consumer validation, the `API_STABILITY.md` entry, and the v1.13.0 release.

## Decisions (2026-10-08, with the owner)

- Export `TimestampAccess()` and `ValueAccess()`; worst class over the parts; `AccessClass` has `String()`.
- The handle has `At` and `TagAt`; `Materialize()` decodes tags too, as `[]string`.
- The name is `NumericMetric`; constructors `NumericBlob.Metric`/`MetricByName` and `BlobSet.NumericMetric`/`NumericMetricByName`; no `NumericBlobSet` form.
- `Materialize()` is explicit, returns nothing, and has no automatic variant.
- Iteration is `ForEach*` with no return value; no `iter.Seq` forms.
- `Duration()` is provided, `int64` in the blob's timestamp unit, the numeric branch of the `MetricDurationByName` contract.
- The text handle is a second phase with its own design.
- The concurrency and copy contract above is accepted and goes into the godoc.
- Every gate blocks; the thresholds above stand; the consumer's shape is a gate; the ALP-RLE layout harness is reused.
- The consumer validation is advisory and is run on a fdc-kernel-platform branch with a `replace` directive.
- The handle ships alone in v1.13.0 with the documentation fixes.

## Decisions (2026-10-09, with the owner)

- Gate 2's iteration threshold is restated to the callback floor, 700 ns for 600 points; no iterator or slice form is added.
- The `ForEach*` callback leak is fixed as its own change before v1.13.0: the column forms on this branch, then the point form, before phase 3.
- The point form's tag strings stay copies, one allocation per tagged point; zero-copy tags would alias the blob's bytes and are not part of this work.
- A chunked callback form is deferred until the consumer validation shows iteration dominating.
- Gate 2's C allocation condition is restated from "the same allocation count" to "no more allocations" than `MaterializeNumericMetricByName`;
  the handle allocates six fewer (610 against 616), because it copies no shared timestamps.
