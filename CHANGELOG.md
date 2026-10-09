# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

A handle for random access to one numeric metric, resolved once and read by index, and `ForEach` callbacks that stay on the stack.
Every addition is new API; nothing existing changed signature, and encoded bytes are unchanged.

### Added

- **`blob.NumericMetric`**: a handle on one numeric metric of a `NumericBlob`, or across the numeric members of a `BlobSet`.
  `NumericBlob.Metric` / `MetricByName` and `BlobSet.NumericMetric` / `NumericMetricByName` resolve the metric's index entries,
  payload ranges and pre-decoded shared timestamps once;
  `Len`, `Duration`, `At`, `ValueAt`, `TimestampAt` and `TagAt` then do no name hashing, index search or decoder construction per call
  and allocate nothing, and `ForEach`, `ForEachValues` and `ForEachTimestamps` walk every blob with the handle's own indices.
  On the 100-metric benchmark blob a `ValueAt` + `TimestampAt` pair costs 10.4 ns through the handle against 26.4 ns through `NumericBlob`;
  at index 599 of a 600-point metric over four blobs, 12.0 ns against 115 ns through the `BlobSet` `ByName` accessors.
- **`NumericMetric.Materialize()`** decodes only the axes a lookup would replay (Gorilla and Chimp values, Delta and DeltaPacked timestamps of a metric's own)
  and the tags, into slices the handle owns; it allocates nothing when every axis is direct and the blobs have no tags,
  costs no more than `MaterializeNumericMetricByName` (0.98× its time on a 600-point Chimp metric with tags),
  and leaves copies made before it independent.
- **`blob.AccessClass`** (`AccessDirect`, `AccessSequential`, `AccessUnsupported`) with `NumericMetric.TimestampAccess()` and `ValueAccess()`:
  whether a lookup reads the point directly or replays the column, decided by the encodings and the shared-timestamp groups.

### Performance

- **`ForEach`, `ForEachValues` and `ForEachTimestamps` no longer move the caller's callback to the heap**,
  on `NumericBlob`, `NumericBlobSet` and the handle: a callback literal that captures locals costs no allocation per call.
  ALP and ALP-RLE values still decode both columns into two slices for `ForEach`, and a tagged blob still copies each tag string.
  Tagged ALP `ForEach` is about 4× faster, walking the tags without `iter.Pull`.

### Documentation

- The materialization godoc's "~100 µs per metric" and "~5 ns per access" are replaced by measured figures with their fixture:
  about 2–5 ns per point without tags (ALP to Chimp values) plus one string copy per tagged point, and about 1 ns per accessor.
  The text-metric figures are not yet measured and are unchanged.
- `docs/best_practices.md` and the package documentation guide random access through the handle,
  with the break-even per metric instead of "roughly 100 random accesses on a dataset".
- `docs/shared_timestamps.md` describes the groups pre-decoded at open that `TimestampAt` reads,
  and that a metric with a unique timestamp sequence keeps a sequential `TimestampAt`;
  `API_STABILITY.md` qualifies the v1.12.0 note accordingly and records the v1.13.0 additions.
- New design document: `docs/specs/random-access-without-materialize.md`, with the gate results.

## [1.12.1] - 2026-10-08

Faster encoding with Gorilla and Chimp, and faster point accessors, with no change to the API or the encoded bytes.

### Performance

- **Gorilla and Chimp encoding is about 14% faster** (median over 69 layout-averaged cells, −2.0% to −20.8%).
  Every 8-byte spill stored the whole slice header back into the encoder,
  so while the garbage collector was marking each spill took a write barrier (about 7,000 per 100-metric blob);
  spills now write only the slice length.
  Output bytes are unchanged, pinned by a bit-at-a-time reference encoder in the tests.
  Gorilla is the default value encoding, so `NewDefaultNumericEncoder` users get this without changing anything.
  See `docs/specs/encoder-write-barriers-design.md`.
- **Point accessors read index entries by pointer.**
  `TimestampAt`, `ValueAt`, `TagAt`, their `ByName` forms and the `BlobSet`, `NumericBlobSet` and `TextBlobSet` forms
  used to copy the index entry (64 bytes for numeric blobs), which the CPU spilled in 8-byte stores and re-read with 16-byte loads;
  store forwarding cannot serve that, so shared-timestamp `TimestampAt` ran at one of two speeds per binary
  (about 1,630 or 2,105 ns/op on the main benchmark set).
  `ValueAt` is 42% faster on Raw, 36% on ALP and 31% on ALP-RLE; encode, decode and iterate are unchanged.
  See `docs/specs/index-entry-by-pointer-design.md`.
- **Shared-timestamp lookup is inlined.**
  The per-blob map keyed by timestamp offset became groups sorted by offset, with the first one in a field,
  so the common one-group case is a compare; shared `TimestampAt` is about 950 ns/op on the main benchmark set.

### Changed

- `make bench-report` checks the report tools and runs the layout-averaged performance report
  (`tests/measurev2/layouts.sh`: four code layouts × four rounds, pinned to one core, about 10 minutes).
- `scripts/check-encoder-hotpath.sh` verifies that the Gorilla and Chimp bit-spill path stays inlined and barrier-free;
  run it after editing those encoders (it is not part of `make test`).

### Documentation

- `docs/performance.md` is regenerated with the layout-averaged method (run of 2026-10-07);
  its speed comparisons are classified as decided, equivalent or inconclusive, and README and `docs/best_practices.md` follow it.
- DeltaPacked timestamps measured iterating 1.28–1.35× slower than Delta with Gorilla and Chimp in every layout;
  the best-practices table now says to prefer Delta unless your own measurements differ.
- New design documents: `measurev2-fast-report-runs-design.md`, `encoder-write-barriers-design.md`
  and `index-entry-by-pointer-design.md`.

## [1.12.0] - 2026-10-05

This release adds ALP-RLE, a value encoding for metrics that often hold their previous value,
and makes ALP and ALP-RLE encoding several times faster on CPUs with AVX-512.

ALP-RLE blobs cannot be read by v1.11.0 or earlier: upgrade consumers before producers write them.
ALP output is unchanged on most builds;
on builds where Go fuses multiply-add, it can differ from v1.9.0–v1.11.0 for rare values, and every build now writes the same bytes.
Decoded values are unchanged.

See [API_STABILITY.md](API_STABILITY.md#additions-and-behaviour-changes-v1120) for the details.

### Added

- **ALP-RLE value encoding** (`format.TypeALPRLE = 0x7`), selected with `WithValueEncoding(format.TypeALPRLE)`.
  It is ALP with a run-length front end:
  each column keeps a runs layout (a run-start bitmap and one ALP value per run) only when that is smaller than the plain ALP column,
  so an uncompressed column is never larger than under ALP.
  With shared DeltaPacked timestamps it is 12.8–47.5% smaller than Chimp on the four benchmark mixes.
  **Forward-incompatible addition:** readers up to v1.11.0 reject these blobs;
  blobs written with other encodings are unaffected.
  See [Best Practices § ALP or ALP-RLE?](docs/best_practices.md#alp-or-alp-rle).

### Changed

- ALP encodes faster.
  On amd64 CPUs with AVX-512DQ and POPCNT, vector kernels search ALP's exponents and check every value,
  and bit-packing and the ALP-RD cut search are faster on every target.
  On the benchmark mixes (100 metrics × 150 points), an ALP blob encodes 2.9–3.7× faster with the kernels
  and about 1–2% faster without them.
- ALP writes the same bytes on every platform.
  From v1.9.0 to v1.11.0, the compiler could fuse a multiply and an add into one FMA instruction in some builds:
  arm64, ppc64x, s390x, riscv64, loong64, and amd64 with `GOAMD64=v3` or later.
  Those builds could pick a different exponent or digit for rare values than other builds.
  Blobs written by those builds stay readable and decode to the same values;
  only the bytes written for such values change.
- `TimestampAt` on shared timestamps reads the cache built when the blob is opened,
  so it is O(1) instead of O(index) for Delta and DeltaPacked:
  at index 149 of a 150-point column it takes 21 ns instead of 168 ns.
- `ForEachValues` on ALP columns decodes each column in bulk instead of draining an iterator.
  It no longer allocates per metric, as its documentation already promised,
  and it takes 25.9% less time in the layout-averaged benchmark (100 metrics × 150 points).
  The decode buffers come from their own pool, capped at 8,192 points per buffer,
  and longer columns stream through the iterator, so memory stays bounded.

### Documentation

- `docs/performance.md` is rebuilt on calibrated mixed-metric data (four mixes calibrated to about 3.8 bytes/point for Chimp)
  and 16 data-shape profiles (the four mixes and 12 single-kind shapes), with ALP-RLE throughout;
  its encode costs reflect the AVX-512 search and say where they need AVX-512DQ.
- Best practices explain when to choose ALP-RLE and how to roll it out.
- Design specs for the ALP run-length front end and the AVX-512 (e, f) search are in `docs/specs/`.

## [1.11.0] - 2026-10-03

This release hardens every decoder against corrupt and crafted input,
fixes several encoder and decoder defects that could corrupt or misread data,
and makes iteration faster.

Encoder defaults now match their documentation (Delta timestamps and Gorilla values, no value compression),
which changes the bytes a default encoder writes and makes `ValueAt`/`TimestampAt` on such blobs sequential.
Pass Raw encodings explicitly to keep the previous output.

Blobs written by earlier versions stay readable;
decoded results change only where earlier versions misread data, such as a text timestamp of 0.

Delta iteration is faster:
`NumericBlob.ForEach` over Delta+Gorilla or Delta+Chimp takes 15–39% less time at 100–1000 points,
and `NumericBlobSet.ForEach` about 25% less.

See [API_STABILITY.md](API_STABILITY.md#behaviour-changes-v1110) for the behaviour changes and migration notes.

### Changed

- **Encoder defaults now match their documentation.**
  `blob.NewNumericEncoder` without options now writes Delta timestamps and Gorilla values
  with no compression, as the `With*` option docs and `mebo.NewDefaultNumericEncoder` state.
  It previously wrote Raw timestamps and Raw values with zstd-compressed values.
  `blob.NewTextEncoder` without options now writes Delta timestamps (data compression stays zstd).
  It previously wrote Raw timestamps.
  Callers that relied on the old bytes should pass the encodings explicitly;
  blobs written with either setting decode the same way.
  Delta timestamps and Gorilla values decode sequentially,
  so `ValueAt` and `TimestampAt` on such blobs cost O(n) per call instead of Raw's O(1).
  Pass `WithTimestampEncoding(format.TypeRaw)` and `WithValueEncoding(format.TypeRaw)`
  when random access matters more than size.
- `TimestampEncoding()` on blobs now returns the exact encoding stored in the header,
  including `format.TypeDeltaPacked`.
  It previously reported DeltaPacked as `format.TypeDelta`.
- Faster iteration over Delta timestamps:
  the decode step for one- and two-byte delta-of-deltas (regular or lightly jittered intervals) is now inlined.
  Iterating Delta+Gorilla/Chimp/Raw blobs with `All` is about 9–20% faster,
  and `NumericBlobSet.ForEachTimestamps` about 27% faster.
- `NumericBlobSet.ForEachValues` and `ForEachTimestamps` call `yield` directly instead of through an adapter closure:
  about 17% and 25% faster again, with no per-call allocations for every timestamp encoding
  and for Gorilla, Chimp and Raw values (ALP values still decode through an iterator).
  Index and early-stop semantics are unchanged.
- Per-point codec methods inline into package `blob` again, as they did before v1.9.0 moved the codecs behind an internal facade.
  In benchmarks of 100 to 1000 points, `NumericBlob.ForEach` over Delta+Gorilla or Delta+Chimp blobs takes about 15–39% less time
  (`NumericBlobSet.ForEach` about 25% less), and text encoding about 4–8% less.

### Fixed

- **DeltaPacked timestamps: interleaving `AddDataPoint` and `AddDataPoints` on one metric no longer
  corrupts the stream on AVX2 hosts.**
  When a batch of 32 or more points followed single points
  that had not yet filled a 4-value group,
  the vectorized encoder wrote its groups ahead of the pending values,
  and decoders returned wrong timestamps from that point on.
  Output depended on the encoding CPU (non-AVX2 hosts were unaffected).
  Blobs already written this way cannot be repaired by a decoder change;
  re-encode them from source data.
- **Text blobs: a Delta-encoded timestamp of exactly 0 no longer corrupts the next point.**
  The decoder treated a previous timestamp of 0 as "no previous point"
  and re-based the next delta on the blob start time,
  so `[-5, 0, 3]` decoded as `[-5, 0, StartTime+3]`.
  The encoded bytes were always correct; existing blobs now decode correctly.
- Delta timestamps: `NumericBlob.All`, `ForEach` and `Materialize` now accept a 10-byte varint
  after the first timestamp (a delta-of-delta of 2^62 or more),
  as `AllTimestamps` and `TimestampAt` already did.
  These paths previously stopped early and returned fewer points.
- `MaterializedNumericBlobSet` and `MaterializedTextBlobSet` resolve `*ByName` lookups and
  `HasMetricName` for names-free members by hashing the name,
  matching the raw set and single-blob `Materialize()`.
  They previously reported every name as missing when no member stored names.
- **Text encoder: a data point rejected for an oversized value or tag no longer leaves its
  timestamp in the stream.**
  The timestamp was written before the length check, so later valid points decoded as garbage
  and `ValueAt` could panic on the resulting blob.
- **Numeric encoder: retrying `EndMetric` after `ErrDataPointCountMismatch` no longer corrupts
  DeltaPacked timestamps or ALP values.**
  EndMetric flushed the pending DeltaPacked group and the ALP column before checking the count,
  so points added after the error were encoded out of order or dropped.
- Encoders now reject cleanly, leaving their state usable:
  - `Finish` returning `ErrMetricNotEnded` or `ErrNoMetricsAdded` no longer tears the encoder down,
    so ending the metric (or adding one) and calling `Finish` again works instead of panicking.
  - A rejected `StartMetricID` no longer locks the encoder into ID mode.
  - `AddDataPoints` with empty timestamps but non-empty values now returns a length-mismatch error.
  - `StartMetricName` rejects a name whose hash is the reserved metric ID 0
    (`ErrInvalidMetricName`), which previously corrupted the following metric.
- `NumericEncoder.MaxDataPoints()` now uses true worst-case encoded sizes
  (10-byte Delta varints, ~8.25 bytes per DeltaPacked timestamp, 77-bit Gorilla values),
  so a V1 metric at the reported limit always fits its timestamp and value payloads.
  Tags are variable-length and not covered:
  a tagged metric below the limit can still overflow the V1 tag offset.
  The reported limits are lower than before.
- Set-level `AllTags` (`NumericBlobSet`, `TextBlobSet`, `BlobSet`, and `ByName` forms) now yield
  one empty tag per point for a member without tags when another member carries tags,
  matching `TagAt` and `Materialize`.
  The encoder drops the tag flag from a blob whose tags are all empty,
  so such a member used to shift every later tag to the wrong data point.
- Text decoder: a header naming DeltaPacked timestamps (numeric-only) is now rejected with
  `ErrInvalidHeaderFlags` instead of decoding as empty metrics,
  and a Delta timestamp varint longer than 10 bytes or cut short is now `ErrInvalidTimestampData`
  instead of a garbled timestamp.
- `DecodeBlobSet` now returns `ErrInvalidMagicNumber` for an input that is neither a numeric
  nor a text blob, as its godoc states, instead of silently skipping it.
- `NumericBlobSet.ForEach*` now apply the set's metric identity, like `All*` and `ValueAt`.
  A collided ID previously yielded every colliding name's points as one series,
  and a by-name call included stripped members that belong to the other colliding name.
- `BlobSet.TimestampAt`, `TagAt` (and their `ByName` forms) and `MetricDuration*`
  now serve a metric found in numeric members from those members only,
  like `MetricLen`, `AllTimestamps` and `AllTags`.
  When the same metric also existed in text members,
  an index past the numeric points used to return a text point,
  and a single-point numeric metric reported the text members' duration.
- **Decoders reject crafted index counts instead of trusting them.**
  `Decode` now checks every entry's point count against its payload ranges
  (at least one timestamp byte per point, exactly eight bytes per raw timestamp or value,
  one tag byte per point, a non-empty value column),
  checks text entries against their data bytes,
  and requires a shared-timestamp member to have the same count as its canonical entry.
  A tiny crafted blob could previously crash the process from `Decode` or `Materialize`
  with multi-gigabyte allocations, spin for seconds, read a neighboring metric's values,
  or pair timestamps with the wrong values; these now fail with `ErrInvalidNumOfDataPoints`
  or `ErrInvalidSharedTimestampTable`.
- Further crafted-input panics fixed:
  a tag length near 2^63 overflowed the tag bounds check;
  a corrupt text length byte pushed reads past the metric's data;
  `ValueAt`, `TagAt` and `AllTags` now read only the metric's own byte range;
  and on 32-bit platforms the index size, metric name lengths, ALP column sizes, ALP exception counts and exception positions
  no longer overflow `int`.
- Further decode hardening:
  `Decode` rejects payload sections that overlap the header or index,
  ALP exception positions that are not strictly ascending and below the point count,
  and ALP columns too large to address on the current platform;
  a varint whose tenth byte overflows uint64 now fails instead of decoding as a truncated value;
  every DeltaPacked decode path now yields the same prefix for a truncated group;
  and LZ4 decompression of corrupt input no longer grows its buffer beyond 255× the input
  (a 1-byte payload previously allocated 268 MB before failing).
- Row iterators and materialization yield only complete rows when a corrupt stream holds
  fewer points than its count, instead of zero-filled timestamps, empty text values
  or empty tags for a tag stream that ends early.
- `AddFromRows` and `AddFromRowsNoTag` reject a call that exceeds the metric's remaining
  points before adding any rows.
- `EncodeMetricNames` rejects a names payload larger than a blob can address,
  and the exported shared-timestamp parsers reject a negative metric count.
- Set materialization keeps timestamps, values and tags aligned when a member decodes
  fewer points than its count from a corrupt stream, instead of shifting later points.
- Numeric decoder: a header whose payload offsets are out of order
  now returns `ErrInvalidValuePayloadOffset` or `ErrInvalidTagPayloadOffset` instead of panicking.
- ALP decoding: a column header whose exponent or factor exceeds 18,
  whose packed width exceeds 64,
  or whose ALP-RD right width is outside 48..63
  now fails `Decode()` with `ErrInvalidALPColumn`.
  An out-of-range exponent or factor previously panicked at decode time.

### Documentation

- `section` package docs now describe the actual layout:
  no padding between sections, header field positions, the V2 magics,
  the Options field's fixed little-endian byte order, the shared-timestamps bit,
  the current encoding values, and the always-zstd tag payload.
  `docs/design.md` no longer claims 8-byte payload alignment.
- Corrected godoc for `TagAt` (O(index), not O(1)),
  `NumericHeader.MetricCount` (up to 65536) and `NumericIndexEntry.TagLength`.
- Every Go example in the godoc, README and `docs/advanced_usage.md` now compiles
  against the current API (decode via `Decode()`, `Finish()` returns `[]byte`, `range` over `iter.Seq2`).
- README, API_STABILITY and CONTRIBUTING now state Go 1.25 to match `go.mod`;
  the README no longer recommends CGO (zstd is pure Go).
- Docs now state the real decoder concurrency rule, error sentinels, encoding byte costs,
  tag cost, name-lookup fallback, `MetricNames()` result per type and blob-set access costs,
  and `encoding`/`compress` package docs describe only APIs that exist.

## [1.10.0] - 2026-07-26

This release makes metric-name storage collision-safe end to end: a hash collision between two
distinct metric names (rare, but possible with a 64-bit hash over unbounded name spaces) no
longer silently merges or misattributes data. It also adds explicit control over when names are
stored, a fast in-place way to strip them back out, and zero-copy decode for the common
read-once case.

### Added

**Metric-name storage control**
- `blob.WithMetricNames()` — numeric encoder option that forces the metric-names payload on even
  when no hash collision occurs (Name mode only; `StartMetricID` returns
  `ErrMetricNamesUnavailable` once set). Previously the numeric encoder stored names only when a
  collision was detected, with no way to opt in for e.g. offline verification tooling.
- `blob.WithoutMetricNames()` — text encoder option that opts out of the text encoder's default of
  always storing metric names. A detected hash collision still forces names on regardless of this
  option.
- `blob.StripMetricNames(dst, src []byte)` and `blob.StripMetricNamesInPlace(buf []byte)` —
  remove an already-encoded blob's metric-names payload without a decode/re-encode round trip,
  when the names aren't load-bearing (no real collision present). Roughly two orders of magnitude
  cheaper than decode + re-encode, with zero-allocation validation. Dropping the names payload
  converts `HasMetricName` / `GetByName` / `*ByName` iteration from exact string match to hash
  membership (a query that hash-collides with a formerly-stored name can false-positive after
  stripping) and empties `MetricNames()`. See `docs/metric_names.md` for the full lifecycle and
  cost.
- `blob.NewNumericDecoderBorrowed` and `blob.NewTextDecoderBorrowed` — zero-copy metric-name
  decode. The decoded blob's metric names alias the input buffer instead of owning independent
  copies, removing the per-name allocations that dominate names-bearing decode cost. The caller
  must keep the input buffer alive and unmutated for the blob's lifetime; materializing the blob
  clones its names, so materialized objects are always owning. The existing `NewNumericDecoder` /
  `NewTextDecoder` constructors are unchanged and keep copying names.
- `blob.MaxMetricNamesCount` (65535) — the metric/names-count ceiling that applies when a names
  payload must be written, tighter than `MaxMetricCount` (65536) because the on-wire count is a
  `uint16`.
- `blob.MaxMetricNameLength` (65535) — the maximum length in bytes of a single metric name, now
  validated in `StartMetricName`'s preflight on both encoders (before any state mutation) rather
  than only at `Finish`. An over-long name is therefore rejected immediately instead of after the
  rest of the blob has already been encoded, and the check applies regardless of
  `WithMetricNames()` / `WithoutMetricNames()`, so identical input no longer succeeds or fails
  depending on whether names happen to be stored.

**Blob-set enumeration**
- `MetricCount()`, `MetricIDs()`, `MetricNames()` and `HasMetricID()` on `NumericBlobSet` and
  `TextBlobSet`, plus `HasMetricName()` and `DataPointCountByName()` on
  `MaterializedTextBlobSet`. These report the set's **logical** metrics (see the set-identity
  note under Changed).

**Errors**
- New `errs` sentinels for the collision-correctness work (all reachable via `errors.Is`):
  `ErrMetricNamesUnavailable`, `ErrMetricNamesExtentMismatch`, `ErrDuplicateMetricName`,
  `ErrUnsortedIndex`, `ErrTooManyMetricNames`, `ErrCollisionNotSupported`.

**Docs**
- `docs/metric_names.md` — new doc covering the metric-names lifecycle: when names are stored by
  default/option, enumeration and membership semantics, what `StripMetricNames` /
  `StripMetricNamesInPlace` drop (including the exact-negative-membership loss above), and
  measured encode, decode, by-ID-vs-by-name access, and `Materialize()` costs.

### Fixed
- Numeric metric-names payload no longer desynchronizes from the index under the V2 MetricID
  sort. Names are now recorded per metric at `EndMetric` and permuted together with their index
  entries, so a names-bearing V2 blob produced from out-of-ID-order insertion decodes correctly.
- The encoder now rejects a repeated metric name even after a hash collision has been recorded
  for that name's ID (previously the third of `(A,H),(B,H),(A,H)` was silently appended a second
  time). Decoders now reject a blob that contains the same name twice with
  `ErrDuplicateMetricName`.
- Materialized numeric/text blobs no longer collapse two distinct metrics that share a hashed ID
  (a real collision); `MaterializeMetricByName` materializes the exact entry the name resolved to
  rather than re-resolving by ID.

### Changed
- Standardized previously undefined/inconsistent behavior on a collided metric ID (two distinct
  names hashing to one ID):
  - Every ID-keyed surface (`GetByID`, `Len`, `MaterializeMetric(id)`, materialized `*At`)
    resolves a collided ID to the **first entry in index order**.
  - `MetricCount` counts **one per index entry** — a within-blob collision counts as two — on
    both raw and materialized blobs.
  - `MetricIDs()` returns **one ID per entry** in index order (a collided ID therefore appears
    twice) on both raw and materialized blobs.
  - `MetricNames()` is deterministic (index order).
- Decoders now reject a V2/V2Ext blob whose index MetricIDs are not in non-descending order with
  `ErrUnsortedIndex` at decode; equal adjacent IDs (a legitimate collision) are still accepted.
  This also repairs a latent `GetByID` binary-search miss on unsorted foreign input. mebo's own
  encoder always emits sorted V2, so no valid producer is affected.
- `regression.Analyze`/`AnalyzeWithOptions` now reject collided input (within-blob, or a
  cross-member collision of distinct names) with `ErrCollisionNotSupported` instead of silently
  collapsing it; `AnalyzeEach`/`AnalyzeEachWithOptions` reject only within-blob collisions.
- A names-bearing blob with no collision no longer eagerly builds the internal name→entry map; it
  answers `GetByName`/`HasMetricName` by hashing the query and string-comparing against the
  retained stored name, preserving exact membership.
- **Blob-set logical identity.** A blob set deliberately merges the *same* metric across time
  windows, so a set's logical identity is now the metric **name** whenever the set carries names,
  falling back to the MetricID only for names-free sets. Consequences, all previously undefined
  on collided input:
  - The same name across members remains **one** set metric (the existing cross-window merge is
    preserved unchanged). Two *different* names colliding on one ID are **two** set metrics.
  - `MetricCount`/`MetricIDs`/`MetricNames` on a set count and enumerate per **logical identity**
    (name-based when available), whereas on a single blob they count per **index entry**. The two
    scopes intentionally differ.
  - Every ID-keyed set surface resolves a collided ID to the **first colliding name in canonical
    order** (members ordered by `StartTime`, caller slice order breaking ties) and returns that
    logical metric's series merged across windows.
  - A member whose names were stripped attaches its data to the **first** colliding name only,
    rather than to every name sharing that ID.
- Materialized blob **sets** no longer concatenate two distinct colliding metrics into a single
  series. Previously `MaterializedNumericBlobSet` unioned members by ID, so a cross-member `A/H`
  plus `B/H` produced one interleaved "frankenseries" containing both metrics' points; each name
  now materializes its own series. Callers materializing sets that contain a real hash collision
  will see a different (correct) series shape.

## [1.9.0] - 2026-07-19

### Added
- Exported `errs.ErrInvalidALPScheme` and `errs.ErrInvalidALPColumn` sentinels so callers can
  identify malformed ALP blobs with `errors.Is`.

### Fixed
- ALP blob opening now rejects unknown scheme bytes, truncated column bodies, invalid
  dictionary sizes, and unsafe packed-code widths instead of silently returning incorrect
  values or panicking during later access.

### Changed
- ALP encoder (`TypeALP`): two encode-only performance changes (decode paths untouched),
  byte-identical to v1.8 output (verified against golden hashes and a 19-column cross-version
  corpus at 10M values per column; all streams remain lossless and decodable by existing
  readers):
  - Adopted the magic-number fast-round technique (`(x + 2^52+2^51) - 2^52+2^51`) in the digit
    round-trip verify pass and the (e,f) search estimator, as a hybrid: fast round for
    `|scaled| < 2^51`, with a legacy `math.Round` fallback for the rare `|scaled| >= 2^51` domain.
  - Added a hand-written AVX-512 (F+DQ) verify-pass kernel for the encoder's digit round-trip
    check, gated on a runtime CPU feature probe; falls back to the existing scalar path on
    CPUs/architectures without AVX-512DQ.
  - On AVX-512DQ hardware, end-to-end ALP encode is ~23% faster on 2-decimal-place data, ~6%
    faster on full-precision data, and ~20% faster on mixed-exception data vs v1.8
    (measured). Without AVX-512DQ, the fast-round change alone accounts for ~8%
    (2dp) / ~12% (full-precision) of that. In isolation, the AVX-512 kernel is ~11.8× faster
    than the scalar verify pass it replaces on 2dp data (an isolated-pass figure, not
    end-to-end).
- Reorganized codec implementations into focused `internal/encoding` subpackages while
  preserving stable APIs, encoded bytes, and decoding behavior.

## [1.8.0] - 2026-06-22

### Added
- `TypeALP = 0x6`: ALP (Adaptive Lossless floating-Point) value codec is now a first-class,
  user-selectable value encoding. Select it via `WithValueEncoding(format.TypeALP)` on the
  numeric encoder. ALP typically achieves 3–5× better compression than Chimp/Gorilla on
  low-decimal-precision gauge data (2–4 dp). **Forward-incompatible addition**: blobs written
  with `TypeALP` cannot be read by older mebo versions; blobs written with prior encodings are
  entirely unaffected.
- Single-column callback iteration on `NumericBlob`: `ForEachValues` / `ForEachValuesByName`
  and `ForEachTimestamps` / `ForEachTimestampsByName` — zero-allocation push equivalents of
  `AllValues` / `AllTimestamps` (identical data, with a 0-based index). Hot-path scans avoid the
  `iter.Seq` iterator-closure and escaping range-body allocations and keep the decode cursor on
  the stack.
- Callback iteration on `NumericBlobSet`: `ForEach`, `ForEachValues`, `ForEachTimestamps` and
  their `…ByName` variants — push equivalents of the set's `All` / `AllValues` / `AllTimestamps`,
  preserving the continuous global index across blobs.

### Performance
- New `ForEach*` single-column and BlobSet iterators are allocation-free on the hot path. On
  the reference workload (delta+gorilla): `NumericBlob` values −25% / timestamps −18% with
  601→2 allocs per scan; `NumericBlobSet` values −22% / timestamps −26% / data points −21%
  with 1601→102 allocs (~94% fewer). Output is byte-identical to the corresponding `All*`
  methods. Backed by new static decoders in `internal/encoding` (`FusedDeltaPackedEach`,
  `RawValuesEach`, `RawTimestampsEach`).

## [1.7.1] - 2026-06-13

### Fixed
- Security: guard `ForEach`/`ForEachByName` against a nil yield function (previously panicked on the first data point)
- Security: bounds-check index entries before slicing payloads across all iteration and random-access paths (`All`, `AllTimestamps`, `AllValues`, `AllTags`, `ForEach`, `TimestampAt`, `TagAt`) via overflow-safe `safeSlice`/`safeSuffix` helpers, so crafted or corrupt blobs return cleanly instead of panicking; also fixes a latent `offset+length` integer overflow in the existing bounds check
- Security: cap the Gorilla/Chimp zero-run drain against trailing byte padding (`GorillaValState`/`ChimpValState` `remaining`/`SetCount`), so padding bits in the final byte are no longer decoded as phantom values
- Encoder point-count integrity: advance `curPoints` only after the timestamp/value/tag writes complete in `AddDataPoint`/`AddDataPoints`, so a panic mid-write cannot leave the metric with an inflated point count
- `TextEncoder.FinishInto` now returns the caller's buffer unchanged (not `nil`) on the late index-write error, restoring the documented unchanged-`dst` contract

### Performance
- Moved the Gorilla/Chimp count cap from the per-value decode primitives to the `GorillaValState`/`ChimpValState` `Next()` wrappers (the only unbounded-drain surface), removing it from the already count-bounded bulk fused loops: ~2.7–4.1% faster full iterate-decode on 1000-point Gorilla/Chimp columns, with no allocation change and byte-identical output

## [1.7.0] - 2026-06-13

### Added
- `ForEach` callback iteration API on `NumericBlob` and `TextBlob` — zero-allocation alternative to `All()`
- `FinishInto` on encoders for buffer-reusing blob finalization (eliminates alloc on repeated encode cycles)

### Changed
- Rewrote Gorilla/Chimp bit-reader/writer with windowed reads and accumulator writes (~15% decode speedup)
- Eliminated iterator closure heap escapes in `All()` hot paths
- Eliminated payload buffer realloc churn in pool and blob encode paths

### Performance
- AVX-512 VBMI backend for packed timestamp decoding
- Fixed AVX-512 packed decoder tail guard (correctness fix under non-aligned lengths)

## [1.6.0] - 2026-04-11

### Added
- SIMD acceleration for `DeltaPacked` timestamps: AVX2 group-varint encode kernel and AVX-512 decode kernel
- `internal/arch` package for CPU/SIMD capability detection

### Changed
- Inlined varint serialization in `TimestampDeltaEncoder.WriteSlice`
- Inlined varint decode in `DecodeAll` and Chimp bulk decode paths

### Fixed
- AVX2 decode kernel: replaced AVX-512-only instructions that caused illegal instruction faults
- Security: capped decompression output size and guarded header offset casts against overflow

### Infrastructure
- CI matrix updated to test Go 1.25 and 1.26
- `GOEXPERIMENT=simd` gated on Go ≥ 1.26

## [1.5.0] - 2026-04-06

### Added
- **V2 blob format**: Chimp XOR encoding, `DeltaPacked` timestamp encoding, shared-timestamp section, sorted index
- `DecodeAll` batch decode method on all decoders
- Shared timestamp cache (`AllTimestamps`) for V2 blobs — single decode amortized across all metrics
- Adaptive index entries for V2 format
- Cross-version compatibility test harness
- `ErrEmptyBlobSet`, `ErrInvalidTimestampData`, `ErrDataSizeMismatch` sentinel errors

### Changed
- Fused multi-stream decoders to eliminate `iter.Pull` goroutine overhead
- `BlobSet.Materialize` now uses `ForEach` internally for correct tag alignment

### Fixed
- Integer overflow in `BlobSet` sort comparators
- Option precedence in `NewTaggedNumericEncoder` / `NewTaggedTextEncoder`
- Error propagation from `WithTextTimestampEncoding` and `WithTextDataCompression`
- Index offset validation in numeric and text decoders (guards against malformed blobs)
- Stale `TagOffset` deltas when dynamically disabling empty-tag encoding
- `TagAt` returns empty string (not `false`) for tagless blobs

## [1.4.3] - 2025-11-28

### Fixed
- `TimestampDeltaEncoder.Reset` did not reset internal state correctly

## [1.4.2] - 2025-11-25

### Changed
- Removed `gozstd` (cgo zstd) entirely; pure-Go zstd only

## [1.4.1] - 2025-11-25

### Changed
- Disabled cgo zstd build path in preparation for full removal

## [1.4.0] - 2025-11-03

### Added
- Helper methods on `BlobSet`: iteration, length, and accessor utilities

## [1.3.2] - 2025-10-21

### Added
- JSON stream parser for large metric datasets in `measure` tooling

## [1.3.1] - 2025-10-15

### Changed
- Optimized `TimestampDeltaEncoder` buffer estimation (fewer reallocations)
- Optimized `TimestampDeltaDecoder` inner iteration loop

## [1.3.0] - 2025-10-14

### Added
- Options API and `ChunkPPMs` metric to regression analysis package

## [1.2.0] - 2025-10-12

### Added
- `regression` package: re-encode-based compression regression analysis

## [1.1.1] - 2025-10-12

### Fixed
- `BlobSet` methods incorrectly handled tag support flags

## [1.1.0] - 2025-10-11

### Added
- Selective metric materialization for `BlobSet` (`MaterializeMetrics`)
- `AddFromRows` on encoders with encoder-level slice caching
- Typed slice pool (`internal/pool`) for efficient memory reuse

### Changed
- Optimized Gorilla decoder: batch unchanged-value detection
- Introduced varint decode fast path in timestamp delta decoder
- Timestamp delta encoder fast paths (reduced branch overhead)
- Optimized `NumericDecoder` performance
- `NumericBlob` struct field reordering for better CPU cache locality
- Removed redundant engine field from blob structs

### Fixed
- Empty tag payload handling when tags are disabled
- `TagAt` now correctly returns `true` with empty string for tagless blobs

## [1.0.0] - 2025-10-08

### Added - Core Features
- **Hash-based Metric Identification**: 64-bit xxHash64 for O(1) metric lookups
- **Columnar Storage**: Separate timestamp and value encoding for optimal compression
- **Multiple Encoding Strategies**:
  - Raw encoding for uncompressed data
  - Delta encoding for sequential data
  - Gorilla encoding for high compression ratios
- **Multiple Compression Codecs**:
  - Zstd (balanced compression and speed)
  - S2 (fast compression)
  - LZ4 (ultra-fast compression)
  - None (no compression)
- **Tag Support**: Optional metadata per data point
- **BlobSet Support**: Unified access across multiple blobs with global indexing
- **Type Support**:
  - NumericBlob for float64 metrics
  - TextBlob for string metrics

### Added - API & Packages
- `blob` package: Main encoding/decoding logic for NumericBlob and TextBlob
- `encoding` package: Timestamp and value encoding strategies
- `compress` package: Compression codec implementations
- `section` package: Internal format structures and headers
- `endian` package: Endian engine utilities
- Root package: Convenience wrappers and helper functions

### Added - Developer Experience
- **Comprehensive Examples**:
  - `blob_set_demo`: Multi-blob operations
  - `compress_demo`: Compression comparison
  - `options_demo`: Configuration patterns
- **Testing Infrastructure**:
  - Comprehensive test suite (9 packages)
  - Benchmark suite for performance validation
  - GitHub Actions CI/CD pipeline
- **Development Tools**:
  - Makefile with comprehensive targets
  - golangci-lint v2 integration
  - Automated linting and testing
- **Documentation**:
  - 723-line comprehensive README
  - Package-level godoc documentation
  - API examples with expected outputs

### Added - Performance Features
- **Zero-Allocation Iteration**: Decode and iterate without per-point allocations
- **Buffer Pooling**: Internal buffer reuse for reduced GC pressure
- **Immutable Blobs**: Thread-safe concurrent reads
- **Optimized Layouts**:
  - Empty tag optimization (saves 20-60 bytes per blob)
  - Grouped length bytes in TextBlob
  - Delta-of-delta timestamp compression

### Performance Characteristics
- **Encoding**: 25-50M operations/second
- **Decoding**: 40-100M operations/second
- **Space Efficiency**: 42% smaller than raw storage with Gorilla+Delta encoding
- **Memory Footprint**: Minimal allocations with buffer pooling

### Changed - Optimizations
- Optimized `Finish()` by removing unnecessary pooled buffer copy
- Consolidated test and benchmark cases for better maintainability
- Improved Makefile with comprehensive targets and better organization

### Changed - Refactoring
- BlobSet types now use value receivers for immutability
- Renamed `DecodeVarint` to `decodeVarint` (unexported)
- Renamed `MaterializedMetric` to `MaterializedNumericMetric` for clarity
- Changed endianness to non-exported data type for encapsulation
- Fixed buffer pool issue in ColumnarEncoder implementations

### Documentation
- Standardized godoc format across all packages
- Added comprehensive performance analysis
- Added metrics-to-points ratio analysis
- Enhanced README with design philosophy and use cases
- Added BlobSet introduction and examples
- Documented zero-allocation iteration feature

### Infrastructure
- GitHub Actions CI workflow with linting and testing
- golangci-lint v2.5.0 migration with comprehensive linting rules
- Makefile targets for test, lint, coverage, and benchmarks
- Automated version checking for linter consistency

### Dependencies
- `github.com/cespare/xxhash/v2` v2.3.0 - Hash function
- `github.com/klauspost/compress` v1.18.0 - Zstd and S2 compression
- `github.com/pierrec/lz4/v4` v4.1.22 - LZ4 compression
- `github.com/stretchr/testify` v1.10.0 - Testing utilities

### Design Philosophy
Mebo is designed for **batch processing of already-collected metrics**, not streaming ingestion:
1. Collect metrics in memory (from monitoring agents, APIs, etc.)
2. Pack metrics into blobs using Mebo encoders
3. Persist blobs to storage (databases, object stores, file systems)
4. Query blobs later by decoding on-demand

This design makes Mebo ideal for:
- Batch metric ingestion (10 seconds to 5 minutes intervals)
- Time-series databases with compressed storage
- Object storage (S3/GCS/Azure Blob)
- Metrics aggregation and ETL pipelines

### API Stability
This is the first stable release. The public API is now locked and will follow semantic versioning:
- **MAJOR**: Breaking changes (v2.0.0+)
- **MINOR**: New features, backward compatible (v1.x.0)
- **PATCH**: Bug fixes, backward compatible (v1.0.x)

The following packages have stable APIs:
- `github.com/arloliu/mebo` (root package)
- `github.com/arloliu/mebo/blob`
- `github.com/arloliu/mebo/compress`
- `github.com/arloliu/mebo/encoding`

Packages under `internal/` are not covered by stability guarantees.

### Known Limitations
- Metrics must declare data point count upfront (batch processing design)
- Maximum blob size depends on available memory
- No built-in persistence layer (bring your own storage)
- Limited to Go 1.23+ (requires latest language features)

### License
Apache License 2.0

[Unreleased]: https://github.com/arloliu/mebo/compare/v1.12.1...HEAD
[1.12.1]: https://github.com/arloliu/mebo/compare/v1.12.0...v1.12.1
[1.12.0]: https://github.com/arloliu/mebo/compare/v1.11.0...v1.12.0
[1.11.0]: https://github.com/arloliu/mebo/compare/v1.10.0...v1.11.0
[1.10.0]: https://github.com/arloliu/mebo/compare/v1.9.0...v1.10.0
[1.9.0]: https://github.com/arloliu/mebo/compare/v1.8.0...v1.9.0
[1.8.0]: https://github.com/arloliu/mebo/compare/v1.7.1...v1.8.0
[1.7.1]: https://github.com/arloliu/mebo/compare/v1.7.0...v1.7.1
[1.7.0]: https://github.com/arloliu/mebo/compare/v1.6.0...v1.7.0
[1.6.0]: https://github.com/arloliu/mebo/compare/v1.5.0...v1.6.0
[1.5.0]: https://github.com/arloliu/mebo/compare/v1.4.3...v1.5.0
[1.4.3]: https://github.com/arloliu/mebo/compare/v1.4.2...v1.4.3
[1.4.2]: https://github.com/arloliu/mebo/compare/v1.4.1...v1.4.2
[1.4.1]: https://github.com/arloliu/mebo/compare/v1.4.0...v1.4.1
[1.4.0]: https://github.com/arloliu/mebo/compare/v1.3.2...v1.4.0
[1.3.2]: https://github.com/arloliu/mebo/compare/v1.3.1...v1.3.2
[1.3.1]: https://github.com/arloliu/mebo/compare/v1.3.0...v1.3.1
[1.3.0]: https://github.com/arloliu/mebo/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/arloliu/mebo/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/arloliu/mebo/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/arloliu/mebo/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/arloliu/mebo/releases/tag/v1.0.0
