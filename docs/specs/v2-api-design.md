# Design: the mebo v2 API

**Date:** 2026-10-14
**Status:** Approved by the owner on 2026-10-20; every item under "Decisions" was taken as recommended.
Revised on 2026-10-14 after an external review
(owning extraction returns an error, text tag elision withdrawn, search precondition and consumer path restated, gates refined),
and on 2026-10-17 with the consumer session's answers to the question list
(duplicates are byte-identical on one ingest path only, the consumer keeps its linear scan,
a v1.10.0 reader gate is added).
Nothing is implemented; the owner has deferred implementation, and phase 1 starts on their word.
**Baseline:** `main` at v1.13.0-rc4 (2e1d2da),
which already carries `NumericMetric`, `blob.Verify`, exhaustive encoding dispatch and the decoder header checks.

## Goal

mebo v2 breaks the API and nothing else.
The wire format is unchanged:
every blob v1.4.3 or later wrote decodes in v2, and every blob v2 writes decodes in v1.13 and in the consumer's v1.10.0.
What changes is the shape of the public surface:

- every per-metric read goes through a handle resolved once (`NumericMetric`, and a new `TextMetric`);
  containers resolve handles, enumerate metrics and describe themselves, and do nothing else per metric;
- the `*ByName` / ID duplication, the four materialized container types and the typed blob sets go;
- the encoders stop exposing their configuration, options share names across numeric and text,
  and tags are explicit per call instead of a mandatory positional argument;
- format details live in one `Info()` value, and `format.EncodingType` splits into a timestamp type and a value type;
- the defaults become what the consumer writes today:
  sorted index layout with shared timestamps, DeltaPacked timestamps, Chimp values;
- the "V2 layout" is renamed so that it no longer collides with "mebo v2".

The only known consumer is `fdc-kernel-platform` (`shared/tchart/mebo/`),
so the migration cost is one wrapper and one import path.
There is no compatibility shim,
and the deprecation window in `API_STABILITY.md` is waived for this major version by owner decision.

Non-goals: new codecs, decode performance work and the decode-budget API stay in v1.x minor releases
and reach v2 by merging `main`.

## The consumer this serves

Facts from `fdc-kernel-platform` at f72391ab7
(`shared/tchart/mebo/mebo_codec.go`, `mebo_tchart.go`, `shared/tchart/tchart.go`,
`shared/tchart/search/search.go`, `shared/repos/tchart/tchart_inline_repo_cassandra.go`):

- It pins mebo v1.10.0 and writes `data_blob_decode_hint = "mebo-1.10.0"` next to each blob.
  The hint is never read: the reader scans it into a discarded variable and branches on nothing,
  so a reader on one mebo version decodes what a writer on another wrote, with no version gate between them.
  Production deploys the writer (`data-collection`) and the readers (`defense`, `datasink`, `controller`) separately,
  so a v1.10.0 reader reads v2-written blobs during a rolling deploy; the compat gate below covers that pair.
- **Writer**: one `EncodeMeboBlobs` call per upload batch;
  up to four encoders, numeric/text × tagged/untagged, one blob each.
  Numeric: little-endian, V2 layout, shared timestamps, DeltaPacked, Chimp, no compression.
  Text: little-endian, Delta, zstd, and the default names option,
  so its text blobs carry a metric-names payload while its numeric blobs do not.
  Metrics are started by name (`StartMetricName`); `AddDataPoint` is called per point with the point's tag.
  Whether a metric goes to a tagged encoder is decided from its **first point's** tag,
  so a metric whose later points carry tags loses them silently.
- **Reader**: `DecodeBlobSet(rawBlobs...)` over the blobs of one (tool, context),
  60 to 240 members (one per 60-second batch, estimated),
  cached as a `blob.BlobSet` value in an otter cache
  (`cache.Cache[blob.BlobSet]` is named in four DI files and two repository files).
  One wrapper per metric name holds `*blob.BlobSet` and resolves by name on every call.
  A wrapper is built per `GetTchart` call and lives for one plan or one request; only the set is cached.
  No caller reads the `bool` that the wrapper's `Materialize()` returns; the transforms call it and continue.
  No wrapper is shared across goroutines: a plan evaluates sequentially, and each chart transfer builds its own.
  It probes `IsNumericMetricByName` before `IsTextMetricByName`, so a name present in both kinds is numeric.
  Numeric iteration loops over `NumericBlobs()` with `HasMetricName` + `ForEachByName` + `LenByName`,
  which keeps the decode loop on the stack;
  text iteration has no such path and uses the heap-allocating `AllTextsByName`.
  Random access goes through `NumericAtByName`, `TimestampAtByName`, `TextAtByName`;
  `BinarySearchByTime` calls `TimestampAt(mid)` after `Materialize`, and already assumes monotone timestamps.
  In v1.10.0, `TimestampAtByName` past the numeric length restarted its index on the text members of the same name,
  so a wrapper marked numeric could read a text timestamp; v1.11.0 fixed that, and a typed handle cannot do it.
  `Size()` and `Duration()` are cached in the wrapper,
  because `MetricLenByName` and `MetricDurationByName` scan every member.
  `Materialize()` fills a `MaterializedNumericMetric` or `MaterializedTextMetric`, and every accessor branches on it.
- **Windows**: `NewTchart` finds its window by a linear scan over `AllTimestamps()` from index 0,
  and `iterHelper` skips from 0 again on each iteration.
- **Set shape**: members are stable-sorted by start time,
  which is the earliest first point over every metric of the batch, not the metric's own first point.
  Two ingest paths write blobs.
  On the SECS path the start time comes from the data,
  so a retried upload of the same request produces a byte-identical duplicate blob,
  given a deterministic encoder (gate 10 checks that).
  On the DKG path `EarliestTime` is never set and the codec falls back to `time.Now()`,
  so a resend differs in its start time and sorts by arrival, placing older points after newer members;
  the consumer will fix that path on its side.
  Within one batch the points of a metric are assumed sorted by time; nothing checks or enforces it.
  Overlapping windows cannot be ruled out from the consumer's code;
  the same name may be numeric in one batch and text in another.
- Blobs live in Cassandra, which checksums them; names are not stored; numeric payloads are not compressed.
  The gocql driver copies each blob out of its frame buffer and the repository starts every row from a nil slice,
  so the bytes handed to `DecodeBlobSet` belong to the consumer and are never reused.
  Nothing in the consumer reads a text blob's names payload
  (`MetricNames`, `HasMetricNames` and `MetricIDs` are unused; its tests use `HasMetricName`, which needs no payload).

The wrapper after v2 is the acceptance test for this design; see "The consumer afterwards".

## Public surface

### Packages

| package | v2 |
|---|---|
| `blob` | the API: decoding, containers, handles, encoders, options, `Info`, `Verify`, `MetricID` |
| `format` | `TimestampEncoding`, `ValueEncoding`, `CompressionType`, `Layout`, `ByteOrder` |
| `errs` | sentinel errors, unchanged |
| `mebo` (root) | removed: its constructors only duplicated `blob`'s defaults, and `MetricID` moves to `blob` |
| `section`, `compress`, `endian`, `encoding` | moved under `internal/`; nothing outside the module needs them (`tests/` and `examples/` are adjusted) |
| `regression` | moved to its own module, `tools/regression`, outside the API stability promise |

### `format`

```go
type TimestampEncoding uint8
const (
    TimestampRaw         TimestampEncoding = 0x1
    TimestampDelta       TimestampEncoding = 0x2
    TimestampDeltaPacked TimestampEncoding = 0x5 // numeric blobs only
)

type ValueEncoding uint8
const (
    ValueRaw     ValueEncoding = 0x1
    ValueGorilla ValueEncoding = 0x3
    ValueChimp   ValueEncoding = 0x4
    ValueALP     ValueEncoding = 0x6
    ValueALPRLE  ValueEncoding = 0x7
)

type CompressionType uint8 // unchanged: CompressionNone, CompressionZstd, CompressionS2, CompressionLZ4

type Layout uint8
const (
    LayoutLegacy         Layout = iota + 1 // magic 0xEA10: index in insertion order, hash lookup, no shared timestamps
    LayoutSorted                           // magic 0xEA20: index sorted by ID, 16-byte entries, shared timestamps allowed
    LayoutSortedExtended                   // magic 0xEA30: as LayoutSorted with 32-byte entries, chosen by the encoder when offsets or counts exceed 16 bits
)

type ByteOrder uint8 // LittleEndian, BigEndian
```

The wire values are written explicitly, never derived from declaration order; each type has `String()`.
`Layout` and `ByteOrder` are descriptive:
the decoder reports them, and the encoder takes `WithLegacyLayout()` and nothing else (see Defaults).

### Decoding

```go
func DecodeNumeric(data []byte, opts ...DecodeOption) (NumericBlob, error)
func DecodeText(data []byte, opts ...DecodeOption) (TextBlob, error)
func DecodeBlobSet(blobs [][]byte, opts ...DecodeOption) (BlobSet, error) // each blob dispatched on its magic
func NewBlobSet(numeric []NumericBlob, text []TextBlob) BlobSet
func Verify(data []byte) error                                            // unchanged from v1.13
func MetricID(name string) uint64                                         // moved from the root package

func WithVerify() DecodeOption // runs Verify before returning; for untrusted input
```

One decode path replaces `NewNumericDecoder` + `Decode()` and the `Borrowed` variants.
A decoded blob **may alias `data`**: uncompressed payloads and metric names are read in place,
so the caller keeps `data` unmodified for as long as the blob, any set containing it and any handle on it are in use.
v1 had two rules:
the default copied names, which made a blob whose payloads were all compressed independent of its input,
while an uncompressed payload aliased the input regardless.
v2 adopts the conservative rule uniformly, so the lifetime of a blob never depends on its compression settings.
`Columns()` on a handle is the way to data that outlives the input.
`DecodeOption` exists so that the decode-budget API planned for v1.x can land without another signature change.

`DecodeBlobSet` takes a slice, not a variadic, so options fit;
the consumer's call changes from `DecodeBlobSet(raw...)` to `DecodeBlobSet(raw)`.

### Containers

Containers resolve handles, enumerate and describe.
Every per-metric read is on a handle.

```go
type NumericBlob struct{ /* unexported */ }
func (b NumericBlob) Metric(name string) (NumericMetric, bool)
func (b NumericBlob) MetricByID(id uint64) (NumericMetric, bool)
func (b NumericBlob) Metrics() []MetricDescriptor   // index order
func (b NumericBlob) Info() NumericBlobInfo

type TextBlob struct{ /* unexported */ }
func (b TextBlob) Metric(name string) (TextMetric, bool)
func (b TextBlob) MetricByID(id uint64) (TextMetric, bool)
func (b TextBlob) Metrics() []MetricDescriptor
func (b TextBlob) Info() TextBlobInfo

type BlobSet struct{ /* unexported; a value, cheap to copy, immutable once built */ }
func (s BlobSet) NumericMetric(name string) (NumericMetric, bool)
func (s BlobSet) NumericMetricByID(id uint64) (NumericMetric, bool)
func (s BlobSet) TextMetric(name string) (TextMetric, bool)
func (s BlobSet) TextMetricByID(id uint64) (TextMetric, bool)
func (s BlobSet) Metrics() []MetricDescriptor       // canonical logical order: numeric metrics, then text metrics
func (s BlobSet) NumericBlobs() iter.Seq[NumericBlob] // members in start-time order, read-only
func (s BlobSet) TextBlobs() iter.Seq[TextBlob]
func (s BlobSet) NumericBlobCount() int
func (s BlobSet) TextBlobCount() int

type MetricKind uint8 // KindNumeric, KindText
type MetricDescriptor struct {
    Name string     // "" when no member carries the name
    ID   uint64
    Kind MetricKind
}
```

Name is the primary key and ID the secondary, so the suffix sits on the ID form; there are no `*ByName` methods.
`NumericBlobSet`, `TextBlobSet`, `MaterializedNumericBlob`, `MaterializedNumericBlobSet`,
`MaterializedTextBlob` and `MaterializedTextBlobSet` are removed:
`BlobSet` is the only set, a numeric-only caller builds `NewBlobSet(numeric, nil)`,
and bulk materialization is per handle.
`BlobAt` and the member slices go with them;
`NumericBlobs()` yields copies of the member values, so nothing a caller does to a yielded blob reaches the set.
`TimeRange()` goes too:
it reported member start-time bounds, not point bounds,
and the member iterators give the same information honestly.

### Handles

`NumericMetric` is the v1.13 handle with three additions
(`SearchTimestamp`, `Columns`, and `Materialize` returning an error);
`TextMetric` is new.

```go
type NumericMetric struct{ /* first part inline, rest in a slice; see Costs */ }
func (h *NumericMetric) Len() int
func (h *NumericMetric) Duration() int64                  // last timestamp minus first, in the encoder's unit; 0 when empty or unreadable, never negative
func (h *NumericMetric) At(i int) (NumericDataPoint, bool)
func (h *NumericMetric) ValueAt(i int) (float64, bool)
func (h *NumericMetric) TimestampAt(i int) (int64, bool)
func (h *NumericMetric) TagAt(i int) (string, bool)       // ("", true) on a blob without tags
func (h *NumericMetric) ForEach(yield func(i int, dp NumericDataPoint) bool)
func (h *NumericMetric) ForEachValues(yield func(i int, v float64) bool)
func (h *NumericMetric) ForEachTimestamps(yield func(i int, ts int64) bool)
func (h *NumericMetric) SearchTimestamp(ts int64) (i int, found bool) // see Time search
func (h *NumericMetric) TimestampAccess() AccessClass
func (h *NumericMetric) ValueAccess() AccessClass
func (h *NumericMetric) Materialize() error                // see Materialize and Columns
func (h *NumericMetric) Columns() (NumericColumns, error)

type NumericColumns struct {        // owning, flattened, independent of the blobs' bytes; replaces MaterializedNumericMetric
    Timestamps []int64
    Values     []float64
    Tags       []string             // nil on a blob without tags
}
func (c NumericColumns) Len() int

type TextMetric struct{ /* same part structure over text record ranges */ }
func (h *TextMetric) Len() int
func (h *TextMetric) Duration() int64
func (h *TextMetric) At(i int) (TextDataPoint, bool)
func (h *TextMetric) ValueAt(i int) (string, bool)
func (h *TextMetric) TimestampAt(i int) (int64, bool)
func (h *TextMetric) TagAt(i int) (string, bool)
func (h *TextMetric) ForEach(yield func(i int, dp TextDataPoint) bool)
func (h *TextMetric) ForEachValues(yield func(i int, v string) bool)
func (h *TextMetric) ForEachTimestamps(yield func(i int, ts int64) bool)
func (h *TextMetric) SearchTimestamp(ts int64) (i int, found bool)
func (h *TextMetric) Materialize() error
func (h *TextMetric) Columns() (TextColumns, error)

type TextColumns struct { Timestamps []int64; Values []string; Tags []string }
```

`NumericDataPoint{Ts, Val, Tag}` and `TextDataPoint{Ts, Val, Tag}` are unchanged.
Both handles keep the v1.13 rules:
pointer receivers on a type used by value, a zero value that is a handle on nothing,
safe for concurrent reads, `Materialize` as the one write, and copies made before it independent of it.

`TextMetric` has its own cost contract and does not pretend to be columnar:

- a text metric is a sequence of variable-length records
  (timestamp, value length, tag length, value bytes, tag bytes),
  so `ValueAt`, `TimestampAt`, `TagAt` and `At` walk the records up to the index:
  sequential on every axis until `Materialize`, after which they are direct.
  There is no `AccessClass` on `TextMetric`; the godoc states this once.
- every string a lookup or a walk returns is a copy; the handle never hands out a string aliasing the blob's bytes.
  `Materialize` copies each value column and each tag column once into one backing string per column kind
  (the numeric tag arena), then slices it,
  so a materialized text handle costs up to two string arenas, three slices,
  and one copy of the parts array when the handle spans several members (the numeric handle's rule), not one allocation per point.
  After `Materialize`, lookups return the owned immutable strings without copying again.
- `ForEach*` are static loops over the records, no `iter.Seq`, no closure escape, as the numeric forms are;
  they replace the consumer's `AllTextsByName` path.
- text timestamps are Raw or Delta; DeltaPacked remains rejected by the text encoder.

### Encoding

```go
func NewNumericEncoder(startTime time.Time, opts ...NumericOption) (*NumericEncoder, error)
func NewTextEncoder(startTime time.Time, opts ...TextOption) (*TextEncoder, error)

func (e *NumericEncoder) StartMetric(name string, points int) error
func (e *NumericEncoder) StartMetricByID(id uint64, points int) error
func (e *NumericEncoder) AddPoint(ts int64, v float64) error
func (e *NumericEncoder) AddTaggedPoint(ts int64, v float64, tag string) error
func (e *NumericEncoder) AddPoints(ts []int64, v []float64) error
func (e *NumericEncoder) AddTaggedPoints(ts []int64, v []float64, tags []string) error
func (e *NumericEncoder) EndMetric() error
func (e *NumericEncoder) Finish() ([]byte, error)
func (e *NumericEncoder) FinishInto(dst []byte) ([]byte, error)
func (e *NumericEncoder) MaxPoints() int
// TextEncoder: the same with string values
```

The configuration is unexported and set only through options;
`NumericHeader()`, `TimestampCodec()`, `ValueCodec()`, `DataCodec()`, `TextHeader()`
and the two `*EncoderConfig` types go.

Tags are explicit per call, with defined behaviour instead of silent padding and dropping:

| call | encoder without `WithTags()` | encoder with `WithTags()` |
|---|---|---|
| `AddPoint` | stored | stored with tag `""` |
| `AddTaggedPoint`, tag `""` | stored | stored with tag `""` |
| `AddTaggedPoint`, tag non-empty | **`errs.ErrTagsDisabled`** | stored |
| `AddPoints` | stored | stored with tags `""` |
| `AddTaggedPoints` | `errs.ErrTagsDisabled` if any tag is non-empty | stored |

A non-empty tag never disappears.
The numeric encoder already elides the tag column at `Finish` when no non-empty tag was stored
(it clears the header flag, zeroes the entries' tag offsets and writes no tag payload),
so a numeric blob written with `WithTags()` costs nothing when every tag is empty;
v2 documents that on `WithTags()` and on `NumericBlobInfo.HasTags`.
The text encoder cannot: a tagged text record carries its tag length byte inline,
so eliding tags would mean rewriting every record; v2 does not add that pass,
and zstd compresses the all-zero length bytes to almost nothing.

Insertion calls validate before they write, so a rejected call leaves the encoder exactly as it was:
the point count, the codec state and the payloads are unchanged, and the caller may retry or end the metric.
`AddPoints` and `AddTaggedPoints` require equal slice lengths (`errs.ErrInvalidDataPoints` otherwise);
`AddTaggedPoints(ts, v, nil)` is a length mismatch, not a shorthand for `AddPoints`.
A late non-empty tag on an untagged encoder, and a text value over the 255-byte limit,
are rejected at that call with the metric still open.
Starting a metric with an empty name is rejected, because `""` marks a missing name in descriptors.

Options share names between the two encoders,
and are typed so that a numeric-only option passed to the text encoder fails to compile:

```go
type NumericOption interface{ applyNumeric(*numericConfig) error }
type TextOption interface{ applyText(*textConfig) error }
type Option interface{ NumericOption; TextOption }   // what the shared options return

// shared
func WithTags() Option
func WithMetricNames() Option                              // always retain names; without it names are retained only on a collision
func WithTimestampEncoding(e format.TimestampEncoding) Option // text accepts Raw and Delta, numeric also DeltaPacked; checked at construction
// numeric only
func WithValueEncoding(e format.ValueEncoding) NumericOption
func WithTimestampCompression(c format.CompressionType) NumericOption
func WithValueCompression(c format.CompressionType) NumericOption
func WithLegacyLayout() NumericOption                      // write the insertion-ordered index of v1.4 and earlier, for fixtures
func WithoutSharedTimestamps() NumericOption               // skip the detection pass; no table is written
// text only
func WithDataCompression(c format.CompressionType) TextOption
```

Every option is parameterless unless it carries an enum.
`WithLittleEndian`, `WithBigEndian`, `WithTagsEnabled(bool)`, `WithBlobLayoutV2`, `WithSharedTimestamps`,
`WithoutMetricNames` and every `WithText*` go.

Option interactions, settled now:

- an enum option given twice: the last one wins; a nil option is rejected at construction (`errs.ErrInvalidOption`);
- `WithLegacyLayout()` writes no shared timestamp table, since the legacy layout has none;
  `WithoutSharedTimestamps()` next to it is redundant and accepted;
- `WithMetricNames()` followed by `StartMetricByID` is rejected (`errs.ErrMetricNamesUnavailable`),
  and name and ID starts cannot be mixed in one encoder (`errs.ErrMixedIdentifierMode`), as in v1;
- a `[]Option` cannot be expanded as `...NumericOption` or `...TextOption` (Go does not convert slices of interfaces);
  a caller that builds option lists keeps one typed slice per encoder, as the consumer does today.

### `Info`

```go
type NumericBlobInfo struct {
    Layout               format.Layout
    ByteOrder            format.ByteOrder
    TimestampEncoding    format.TimestampEncoding
    ValueEncoding        format.ValueEncoding
    TimestampCompression format.CompressionType
    ValueCompression     format.CompressionType
    HasTags              bool
    HasMetricNames       bool      // a names payload is present, whether retained by option or by collision
    SharedTimestamps     bool      // a shared timestamp table is present (LayoutSorted and LayoutSortedExtended only)
    StartTime            time.Time
    MetricCount          int
}

type TextBlobInfo struct {
    ByteOrder         format.ByteOrder
    TimestampEncoding format.TimestampEncoding
    DataCompression   format.CompressionType
    HasTags           bool
    HasMetricNames    bool
    StartTime         time.Time
    MetricCount       int
}
```

Two structs rather than one with not-applicable fields:
a numeric blob has two compressions and a layout, a text blob has one compression and one layout.
`Info()` is a value copied out of the header, immutable by construction.
It replaces `IsV2Layout`, `SameByteOrder`, `Engine`, `IsBigEndian`, `IsLittleEndian`, `TimestampEncoding`,
`TimestampEncodingType`, `ValueEncoding`, `HasTag`, `HasMetricNames`, `StartTime` and `MetricCount` on both blob types.
A set has no `Info`:
a mixed set has no single truthful layout or encoding, and each member's is one `Info()` away.

## Contracts

### Contract matrix

| | single blob | blob set |
|---|---|---|
| **numeric, borrowed** | `NumericBlob.Metric` → `NumericMetric` | `BlobSet.NumericMetric` → `NumericMetric` over the numeric members |
| **numeric, owning** | `NumericMetric.Columns()` → `NumericColumns` | same |
| **text, borrowed** | `TextBlob.Metric` → `TextMetric` | `BlobSet.TextMetric` → `TextMetric` over the text members |
| **text, owning** | `TextMetric.Columns()` → `TextColumns` | same |
| **heterogeneous** | not offered: a blob has one kind | not offered: the caller probes the typed resolvers in the order it wants |
| **enumeration** | `Metrics()` in index order, descriptors | `Metrics()` in canonical logical order, descriptors |
| **incomplete read** | lookups return `false` past the decoded points; `ForEach*` ends that member's walk; `Materialize` and `Columns` return an error; `Verify` says so up front | same |
| **bulk owning container** | not offered | not offered |

### Resolution and identity

Resolution rules are the v1.13 rules, restated so that handles and enumeration agree:

- **By name on a blob**: the by-name map on a collision,
  hash plus string compare when the blob retains names, hash plus ID lookup when it does not.
- **By ID on a blob**: the index entry with that ID;
  an ID two names collided on resolves to the first entry in index order.
- **On a set**: the set's logical identity.
  Name identity when any member carries names, ID identity otherwise;
  the same name across members is one metric with the members' points concatenated in start-time order;
  two names colliding on one ID are two metrics,
  and the ID form resolves to the first colliding name's metric in canonical order;
  a member that stripped its names attaches to that first name only.
- **Typed only**: `NumericMetric` consults numeric members, `TextMetric` text members.
  A name held by both kinds is two independent metrics, one per kind, and the set never picks one for the caller.
  The consumer's numeric-first probe stays in the consumer, where the policy belongs,
  and costs one extra resolution on the text path.
- **Not a merge**: a set concatenates members; it does not merge by timestamp, deduplicate or sort points.
  Overlapping windows and duplicate blobs produce the concatenated sequence they imply.
  See Time search.

### Enumeration

`Metrics()` returns descriptors, not IDs, so that enumeration preserves the logical identity a bare ID would lose:
a collided ID appears once per name, a name-free metric appears with `Name == ""`, and each descriptor states its kind.
The round trip is lossless:
`Name != ""` resolves through the name form, otherwise through the ID form,
and both land on the descriptor's metric.
Descriptors carry no length:
on a set a length is a walk over every member per metric, and the handle's `Len()` is O(1) once resolved.
Descriptor names are owning copies, so a descriptor outlives the input bytes.
`Metrics()` allocates; it is a metadata call, not a hot path.

Canonical order, the v1.13 order restated:

- members are sorted by start time, stable, so equal start times keep the caller's order;
- on a blob, descriptors follow the index order;
- on a set, within one kind: the named metrics in order of first appearance (member order, then index order within the member),
  then the ID-only metrics of members without names, in the same order;
  this is the order in which "first" is meant wherever a collided ID resolves to its first name;
- the set lists its numeric metrics, then its text metrics.

### Lifetime and ownership

- A decoded blob may alias its input; a set and a handle alias their blobs.
  The caller keeps the input bytes unmodified for all of their lifetimes.
- `Materialize()` decodes into slices the handle owns and never writes again;
  the handle still aliases the blobs for its other reads, so the input's lifetime rule stands.
- `Columns()` returns an owning copy: nothing in it points at the blobs.
  It is the only way to data that outlives the input.
- Nothing returned by a container or a handle points into the index: descriptors, infos and yielded blobs are copies.

### Materialize and Columns

Two operations with different names because they mean different things:

- `Materialize() error` **prepares the borrowed handle**:
  per member it decodes the sequential axes
  (Delta and DeltaPacked timestamps without a complete shared group; Gorilla and Chimp values; every text column)
  and the tags into handle-owned slices, so that every later lookup is direct.
  Direct axes are left alone,
  so a handle whose axes are all direct and whose blobs have no tags allocates nothing, and the call is idempotent.
  It returns `errs.ErrIncompleteMetric` (wrapped with the member and the axis)
  when a decoded column produced fewer points than the index declares;
  the handle stays usable with what was decoded, and lookups past it return `false`, as before.
  The handle remembers the outcome: a second call decodes nothing and returns the same error.
  The decoded columns of all members share one array per axis,
  so after a `Materialize` that decoded every member of an axis, a lookup on that axis indexes the array directly,
  with no member placement; an axis that was already direct keeps its per-member placement.
- `Columns() (…, error)` **produces an owning, flattened metric**:
  three slices holding every point of every member, with no aliasing.
  It decodes direct axes too (copying a shared timestamp group, unpacking ALP),
  and uses the materialized slices when they exist.
  It allocates on every call; a caller wanting one copy keeps it.
  Its result is all or nothing:
  when any member decodes short it returns the zero value and `errs.ErrIncompleteMetric`,
  so a caller never sees columns of unequal length or indices that drift from the handle's.
  `Timestamps` and `Values` have `Len()` elements;
  `Tags` is nil when no contributing member carries tags, and otherwise has `Len()` elements,
  with `""` at the positions of members without tags.

The materialized container types are gone,
so "materialize a whole set" is a loop over `Metrics()` and `Columns()`, which is what those types did internally.

### Incomplete reads

The hot path stays best effort:
`At` and the column lookups answer `(value, false)` past the decoded points,
`ForEach*` stops a member's walk where its stream ends, and neither carries an error,
because an error return on a per-point accessor would cost every correct read.
On a handle over several members, a member that ends early does not end the walk:
the next member starts at its declared index, so the indices a walk yields are the handle's own, with a gap,
and only a callback returning `false` stops the whole walk (the v1.13 behaviour).
Completeness has three explicit channels:
`Verify` (or `WithVerify()` at decode) for untrusted input, which walks every column once;
`Materialize()`'s error for the metric a caller is about to read;
and `Columns()`'s error, which refuses to hand out a flattened copy with holes.
A callback returning `false` and a stream ending early both end a member's walk;
a caller that needs to tell them apart checks `Materialize()` first.

### Time search

`SearchTimestamp(ts)` is the one new capability:
the consumer's window search is a linear scan over every timestamp, repeated per window.

- Semantics are those of `slices.BinarySearch`:
  the smallest index `i` with `TimestampAt(i) >= ts`, or `Len()` when there is none,
  and `found` when `TimestampAt(i) == ts`.
  A window `[begin, end]` is `lo := SearchTimestamp(begin)` and `hi := SearchTimestamp(end+1) - 1`,
  with `hi = Len()-1` when `end` is `math.MaxInt64`.
  A probe that cannot read its point (an unreadable or incomplete axis) ends the search with `(Len(), false)`.
- It reads through `TimestampAt`, so on a sequential timestamp axis each probe replays the column;
  the godoc tells callers to `Materialize()` first when `TimestampAccess()` is not `AccessDirect`.
  On a `TextMetric` every axis is sequential until `Materialize`, so its `SearchTimestamp` walks records per probe
  until then; the rule for text is to materialize before searching, and the godoc says so.
  The consumer enables sharing, but sharing is detected per blob at `Finish`:
  a metric whose timestamp sequence no other metric of the blob shares, or a blob with one metric, has no group
  and its axis is sequential.
  The caller decides on `TimestampAccess()`, not on what the writer was configured to do.
- **Precondition**: the handle's timestamps are non-decreasing over its whole index.
  Members are concatenated in start-time order, not merged,
  so a duplicate blob or an overlapping window breaks the precondition,
  and the result is then unspecified, exactly as `slices.BinarySearch` on unsorted input.
  mebo cannot detect this cheaply on a sequential axis and does not try;
  a caller that cannot guarantee monotone input keeps its linear scan for that handle.
  The consumer answered on 2026-10-17 that it cannot guarantee it:
  the set's sort key is the batch minimum, not the metric's first point, and DKG resends sort by arrival,
  so its `NewTchart` keeps the linear scan until its ingest can prove monotone input.
  It does not want an O(n) `IsSorted()` from mebo:
  its wrapper can walk `ForEachTimestamps` once and cache the answer per handle,
  which amortizes over the many windows a plan takes from one handle.
  A check in O(members) would need the first and last timestamp of each member,
  and on a sequential axis the last one costs a full decode of the member, so mebo does not offer one.
  The one place the consumer already accepts the monotone precondition is `BinarySearchByTime` after `Materialize`,
  which could call `SearchTimestamp`; nothing else in the consumer will at launch.
  Byte-identical duplicate blobs (the SECS resends) are the consumer's to drop before `DecodeBlobSet`
  (a hash of the raw bytes, in its repository layer on a cache miss; its owner accepted that on 2026-10-17);
  dropping them does not make overlapping, non-identical windows monotone, and mebo does not sort or merge points.
  The consumer's current linear scan is not correct on such input either:
  it stops at the first timestamp past the window, so a point of a later, overlapping member is missed.
  v2 does not change that; it gives a monotone handle a logarithmic search and leaves the non-monotone case where it is.
- Handle slicing (`h.Slice(lo, hi)` returning a sub-handle) was considered and left out:
  it puts a skip count into every decode loop, and index arithmetic in the consumer is two integers.

### Concurrency

Unchanged from v1.13:
blobs and sets are immutable after construction and safe for concurrent reads;
a handle is safe for concurrent reads;
`Materialize()` is a write the caller serializes against every other method on the same handle value.

### Costs

Measured in v1.13 (single binary, Ryzen 9 9950X3D), and what v2 keeps or changes:

- Resolution: 16 ns on a blob, no allocation;
  122 to 148 ns on a set with one allocation when more than one member contributes.
  v2 keeps the first-part-inline, rest-in-a-slice layout and the one allocation.
- The consumer's sets have 60 to 240 members, so a handle there has that many parts.
  A part copies what its lookups need
  (the blob base, the three byte ranges, the shared group, three owned slices, counts): about 220 bytes,
  so a 240-member handle is about 53 KB, allocated once at resolution.
  The consumer builds one wrapper, and so one handle, per `GetTchart` call and does not cache it with the set;
  a plan holds its handles until it finishes, with two plans in flight by default,
  and the controller holds one per request.
  The consumer sees no need to shrink the part: a 240-member handle is the same order as its materialized slices.
  The spec records the number so that gate 8 below can bound it.
- Placement of an index in `rest` is a linear scan in v1.13;
  v2 switches to a binary search on the parts' `base` when there are more than eight parts,
  so random access on a 240-member handle costs about 8 comparisons, not up to 240.
- Resolve-plus-one-scan is the consumer's hot path (one handle, one full `ForEach`).
  Construction must not cost more than the v1 blob-by-blob loop saved: gate 3 below.
- `Len()` is O(1), so the consumer's size cache goes.
  `Duration()` is two lookups: direct on a numeric handle with a shared group,
  but a record walk on a text handle until it materializes,
  so the consumer's duration cache stays useful for text metrics it does not materialize.
- `Materialize` on the consumer's handles decodes Chimp values and tags, and nothing else;
  the figures in the v1.13 spec hold.
- After `Materialize`, the consumer today reads a flat slice by index.
  A handle adds member placement to that read for axes that were already direct (its shared timestamps),
  while decoded axes index the shared array directly.
  Gate 9 measures this against the v1 wrapper;
  the consumer may also keep `Columns()` for its materialized path, which is exactly its v1 structure.

## Defaults and the layout rename

| setting | v1 default | v2 default | why |
|---|---|---|---|
| numeric layout | legacy (0xEA10) | `LayoutSorted` (0xEA20; extended entries chosen automatically) | what every v1.5+ consumer writes; shared timestamps need it |
| shared timestamps | off | on (detection pass at `Finish`; no table when nothing is shared) | the largest size win on the consumer's data; `WithoutSharedTimestamps()` opts out |
| numeric timestamps | Delta | DeltaPacked | consumer alignment; the perf report finds Delta and DeltaPacked equivalent under sharing |
| numeric values | Gorilla | Chimp | owner decision; a workload choice, not a universal win over Gorilla |
| numeric compression | none / none | none / none | unchanged; the codecs already compress |
| text timestamps | Delta | Delta | unchanged |
| text compression | zstd | zstd | unchanged; the consumer uses it |
| byte order | little-endian | little-endian, and the only order the encoders write | readers keep big-endian support for existing blobs |
| tags | off | off | opt-in; the tag column costs a byte per point when empty |
| metric names | numeric: collision-only; text: always | collision-only on both | one rule; the consumer never needed names in text blobs either |

The default change is a size change for anyone who relied on v1 defaults.
The consumer sets every numeric option explicitly, so its numeric bytes do not move;
its text blobs lose the names payload they carried by default, which nothing in it reads,
and it moves to one always-tagged encoder per kind, so a mixed batch costs one byte per untagged numeric point.
Chimp and sorted layouts are readable since v1.5.0,
so blobs written with v2 defaults decode on every v1 release the consumer could still be running.

**Rename**: "V2 layout" becomes `format.LayoutSorted` / `LayoutSortedExtended`, and "V1 layout" `format.LayoutLegacy`.
Every exported site changes:
`WithBlobLayoutV2` → `WithLegacyLayout` (inverted, since sorted is the default), `IsV2Layout` → `Info().Layout`.
Internal names (`formatVersion`, `layoutVersion`, `MagicNumericV2Opt`, `MagicNumericV2ExtOpt`, the compat scenario IDs `v2-*`)
follow in the same change, so that "V2" means the module version everywhere.
The README's "O(1) metric lookup" becomes O(log n) for the default layout,
with the note that the two are indistinguishable at a few hundred metrics.

## Removals

| v1 | v2 |
|---|---|
| every `*ByName` method (about 60) | the name form of the resolvers |
| `NumericBlob` / `TextBlob` per-metric methods: `All*`, `ForEach*`, `*At`, `Len`, `HasMetricID`, `HasMetricName`, `MetricIDs`, `MetricNames`, `Materialize`, `MaterializeMetric*` | handle methods, `Metrics()` |
| `AsNumeric`, `AsText`, `IsNumeric`, `IsText`, `BlobReader` | none; the typed containers and `MetricKind` on descriptors carry the kind |
| `IsV2Layout`, `SameByteOrder`, `Engine`, `IsBigEndian`, `IsLittleEndian`, `TimestampEncoding`, `TimestampEncodingType` (deprecated in rc4), `ValueEncoding`, `HasTag`, `HasMetricNames`, `StartTime`, `MetricCount` | `Info()` |
| `NumericBlobSet`, `TextBlobSet`, `BlobSetIterator`, `BlobSetIndexer` | `BlobSet` |
| `MaterializedNumericBlob`, `MaterializedNumericBlobSet`, `MaterializedTextBlob`, `MaterializedTextBlobSet` | per-handle `Columns()` |
| `MaterializedNumericMetric`, `MaterializedTextMetric` | `NumericColumns`, `TextColumns` (same slices, no `MetricID` field: the caller resolved it) |
| `BlobSet.NumericBlobs() []NumericBlob`, `TextBlobs()`, `BlobAt`, `Blobs`, `TimeRange`, `Len` | `iter.Seq` member iterators, member counts |
| `NewNumericDecoder`, `NewNumericDecoderBorrowed`, `NumericDecoder`, `Decode`, and the text forms | `DecodeNumeric`, `DecodeText` |
| `NumericEncoderConfig`, `TextEncoderConfig`, `NewNumericEncoderConfig`, `NewTextEncoderConfig`, their methods | unexported |
| `AddDataPoint(ts, v, tag)`, `AddDataPoints(ts, v, tags)`, `StartMetricName`, `StartMetricID`, `MaxDataPoints` | `AddPoint`, `AddTaggedPoint`, `AddPoints`, `AddTaggedPoints`, `StartMetric`, `StartMetricByID`, `MaxPoints` |
| `WithLittleEndian`, `WithBigEndian`, `WithTagsEnabled(bool)`, `WithBlobLayoutV2`, `WithSharedTimestamps`, `WithoutMetricNames`, `WithText*` | the shared option set |
| `format.EncodingType`, `format.Type*` | `TimestampEncoding`, `ValueEncoding` |
| root `mebo` constructors, `mebo.MetricID` | `blob.MetricID`; the root package goes |
| `regression`, `encoding`, `section`, `compress`, `endian` as public packages | nested module; `internal/` |
| `StripMetricNames`, `StripMetricNamesInPlace` | kept: they are a wire-level tool with no API-shape debt |
| `AddFromRows`, `AddFromRowsNoTag` | kept as `AddRows` / `AddTaggedRows`, following the point methods |

## The consumer afterwards

`mebo_tchart.go` loses the materialized-or-not branch in every accessor,
the per-call name resolution, the two caches and the text `All*` path:

```go
type meboBaseTchart struct {
    name    string
    typ     tch.TchartType
    numeric blob.NumericMetric // zero when typ is text
    text    blob.TextMetric    // zero when typ is numeric
}

func NewMeboBaseTchart(name string, set *blob.BlobSet) (tch.BaseTchart, error) {
    if h, ok := set.NumericMetric(name); ok {
        return &meboBaseTchart{name: name, typ: tch.TchartTypeNumeric, numeric: h}, nil
    }
    if h, ok := set.TextMetric(name); ok {
        return &meboBaseTchart{name: name, typ: tch.TchartTypeText, text: h}, nil
    }
    return nil, fmt.Errorf("tchart %s not found", name)
}

func (m *meboBaseTchart) AllNumerics() iter.Seq2[int, tch.NumericDataPoint] {
    return func(yield func(int, tch.NumericDataPoint) bool) {
        m.numeric.ForEach(func(i int, dp blob.NumericDataPoint) bool {
            return yield(i, tch.NumericDataPoint{V: dp.Val, Ts: utils.ConvertUsToDatetime(dp.Ts), T: dp.Tag})
        })
    }
}
// AllTexts, AllNumericValues, AllTextValues, AllTimestamps: the same shape over the other ForEach forms,
// AllTimestamps dispatching on typ.

func (m *meboBaseTchart) NumericAt(i int) (tch.NumericDataPoint, error) {
    dp, ok := m.numeric.At(i)
    if !ok {
        return tch.NumericDataPoint{}, m.newOutOfBoundError("value", i)
    }
    return tch.NumericDataPoint{V: dp.Val, Ts: utils.ConvertUsToDatetime(dp.Ts), T: dp.Tag}, nil
}

func (m *meboBaseTchart) Size() int { if m.typ == tch.TchartTypeNumeric { return m.numeric.Len() }; return m.text.Len() }
func (m *meboBaseTchart) Duration() time.Duration { /* numeric.Duration() or text.Duration(), times time.Microsecond */ }
func (m *meboBaseTchart) Materialize() bool { /* numeric.Materialize() == nil, or text's */ }
```

A wrapper that wants its v1 structure back keeps `Columns()` in its materialized path instead,
and reads flat slices after materialization as it does today.
`NewTchart` receives `tch.BaseTchart`, which has no search;
the window search moves behind that interface (a `SearchTimestamp(time.Time) (int, bool)` on `BaseTchart`,
answered by the handle when `TimestampAccess()` is direct and the wrapper knows the set is monotone,
and by the existing scan otherwise).
The consumer answered that none of its sets is known monotone today (see "Time search"),
so at launch `NewTchart` keeps the scan,
and the handle's search has one possible caller,
`BinarySearchByTime` after `Materialize`, which already assumes monotone input;
adding the method to `BaseTchart` touches six implementations and two mocks on its side.
`Materialize() bool` on the wrapper returns `false` on the handle's error;
no caller reads it today, and the consumer wants the error logged there later.
`mebo_codec.go` shrinks to one always-tagged numeric encoder and one always-tagged text encoder, the consumer's choice,
so the first-point tag probe goes away and `AddDataPoint(ts, v, tag)` becomes `AddTaggedPoint` on both;
the numeric blob of a batch without tags still elides its tag column at `Finish`.
Its explicit options collapse to `blob.WithTags()` on both encoders,
since every other option it sets is now the default,
and its text blobs lose the names payload they carried by default.
The four DI files and two repository files naming `cache.Cache[blob.BlobSet]` change their import path and nothing else:
`BlobSet` stays a value type in package `blob`.
Its codec test indexes `NumericBlobs()[0]`, which becomes a walk over the set's blob enumeration.

## Module, branches and tooling

- Module path `github.com/arloliu/mebo/v2`, developed on a `v2` branch with no `v2/` directory.
  `main` stays v1.x until v2.0.0 is released; then `main` carries v2 and a `release/v1` branch takes v1.x patches.
  The v2 branch merges `main` regularly so that codec and perf work lands in both.
- Nested modules update their requirement and replace: `tests/compat`, `tests/measurev2`, `tools/regression`.
  `tests/measure` and `tests/fbs_compare` do not build on `main` today;
  the v2 branch deletes them.
- **Compat harness**: one source cannot compile against both APIs, and v1 tags before v1.5 have no harness of their own.
  The v2 branch keeps two harnesses:
  `tests/compat/v1/`, a frozen copy of the v1.13 harness that builds against any v1 tag back to v1.4.3 with its capability tags,
  and `tests/compat/`, the v2 harness, with no capability tags because v2 has every capability.
  `run_compat.sh` picks the harness by the major version of each side,
  and the fixture and expected-result files stay the contract between them.
  Scenario IDs keep their names (`v2-*` included) on both sides,
  because the script selects layout buckets by filename prefix;
  the v2 harness maps them to the new option names internally, and the script asserts that every bucket is non-empty.
  The gate is v1.13.0 ↔ v2.0.0-rc1 in both directions, v1.4.3 → v2.0.0-rc1 (historical writer output),
  and v2.0.0-rc1 → v1.10.0, the consumer's production reader during a rolling deploy,
  which must decode v2 defaults it never wrote itself (text blobs without a names payload among them);
  the first two pairs are the promise the v1 runs make today.
  The consumer's alternative is to move to v1.13.0 before v2, which makes the third pair redundant.
- **measurev2**: hoisting resolution out of the timed body changes what `ValueAt` and `TimestampAt` measure.
  v2 publishes separate operations:
  resolve, lookup on a resolved handle, resolve-plus-lookup, full scan, resolve-plus-scan, materialize, columns.
  Its encoder today leaves the layout implicit and enables sharing per cell,
  so under the v2 defaults the historical cells would silently change format;
  the v2 adapter selects the layout and sharing explicitly for every cell, so that a cell's bytes are what the v1.13 run encoded.
  The v1 numbers in `docs/perf/` stay as the v1 record.
  Its generator stops importing `internal/hash` and uses `blob.MetricID`.
- `API_STABILITY.md` gains the v2 exception (no deprecation window, no shim, sole consumer),
  and the migration guide lands in `docs/migration-v2.md` and the CHANGELOG.

## Phases and gates

Each phase ends with `make test` and `make lint` green and an external review before it is reported done;
the branch is squashed by scope before the PR.

1. **Skeleton**: module path, `format` split and rename, package moves, removals;
   the handle and encoder surfaces as compiling stubs over the v1.13 internals.
   Every v1.13 test that exercised a kept behaviour is ported to the new names and passes;
   a test is deleted only when the behaviour it covered was removed, and the phase report lists those.
2. **Handles**: `TextMetric`, `Columns()`, `Materialize() error`, `SearchTimestamp`,
   part placement by binary search, descriptors and set enumeration,
   `DecodeNumeric` / `DecodeText` / `DecodeBlobSet` with `WithVerify`.
3. **Encoders**: option types, point methods, tag behaviour, defaults, `Info`.
4. **Tooling and docs**: compat harness per side, measurev2 operations,
   README and docs rewritten around handles, migration guide, `API_STABILITY.md`.
5. **Consumer path**: the wrapper above on a branch of `fdc-kernel-platform` with a `replace`, its tests green,
   and values, timestamps, tags, misses and window indices compared against the v1.10.0 wrapper on the same fixtures.
6. **Release**: v2.0.0-rc1, consumer validation, v2.0.0.

Gates, layout-averaged as in `docs/specs/measurev2-fast-report-runs-design.md`
(four code layouts, medians of medians), measured from an external package:

Fixtures: the consumer's shape, 150 metrics × 60 points per member, shared DeltaPacked, Chimp, no names,
in a tagged and an untagged variant, as a 4-member set, a 60-member set and a 240-member set
(every member contributes in each, and the 240-member set also has a sparse variant where every fourth member contributes);
text: 20 metrics × 60 points per member, 8-byte values, Delta, zstd, tagged and untagged.
Hardware and toolchain are pinned in the gate report as the v1.13 gate results were.

1. Resolution: hit on the 4-member and the 240-member sets within 1.1× of v1.13's `NumericMetricByName`,
   one allocation when several members contribute and none on a single blob;
   a miss, and a hit on the sparse set, recorded.
2. Lookups on a resolved handle: within 1.05× of v1.13 at index 0, the middle and the end of the 4-member set;
   on the 240-member set, every index within 1.5× of the 4-member figure at the same relative position
   (the binary-search placement), and index 0 and the last index within 2× of each other.
3. Resolve plus one full `ForEach` over the 4-member set, tagged and untagged:
   no slower than the consumer's v1 blob-by-blob loop (`HasMetricName` + `ForEachByName` + `LenByName` per member),
   with no allocation beyond resolution on the untagged fixture
   and one string copy per point on the tagged one (the v1.13 accounting; no callback escape in either).
   The same with the callback stopping at the first point of the second member.
4. `TextMetric.ForEach` over the text fixture: no slower than v1's `AllTextsByName`,
   and allocations no more than v1's minus the iterator (every string yielded is a copy in both).
5. `SearchTimestamp` on a direct axis over 3,600 points (60 members): under 200 ns,
   with correctness tests for both ends, a miss between points, a miss past the end, duplicate timestamps and an empty handle.
6. `Materialize` and `Columns` on the 4-member set, first call and repeated call, tagged and untagged, numeric and text:
   the first call within 1.1× of v1.13's `Materialize` (handle) and `MaterializeNumericMetricByName` / `MaterializeTextMetricByName`;
   the repeated `Materialize` under 10 ns.
7. The measurev2 operations whose meaning is unchanged (encode, decode-open, full scans, materialize),
   on cells whose layout and sharing the v2 adapter pins to what v1.13 encoded,
   are within the report's noise band of the v1.13 run.
8. Memory: a 240-member handle under 64 KB as a `-benchmem` benchmark's bytes-per-operation reports it, before any `Materialize`;
   recorded, not gated, for the consumer to size its request fan-out.
9. The consumer's wrapper, before and after `Materialize`, against its v1.10.0 wrapper on the same fixtures:
   random reads (`NumericAt`, `TimestampAt`) spread over the 60-member and 240-member sets,
   `BinarySearchByTime`, `Duration()` on numeric and text, and one full `AllNumerics` walk;
   no operation slower than 1.1× of v1 after `Materialize`, and none slower before it.
10. Correctness and ownership: every v1.13 handle test ported;
    encoding the same points twice yields identical bytes (the consumer's duplicate detection hashes raw bytes);
    `Columns()` independent of the input bytes (the input is overwritten after the call and the columns compared);
    a copy made before `Materialize` unchanged by it; `make test` under the race detector with concurrent readers on one handle.

## Decisions

Taken by the owner on 2026-10-20, each as recommended;
the sections above are written as decided, and this is the record.

1. **Root package**: removed; the public packages are `blob`, `format` and `errs`, and `MetricID` moves to `blob`.
   Moving `blob` to the root for a one-import API was the alternative;
   the constructor wrappers only duplicated `blob`'s defaults, so removal is the smaller move.
2. **Big-endian writer**: dropped; the encoders write little-endian only, and the readers keep big-endian for old blobs.
3. **`Materialize() error`**: kept;
   it is the one place an incomplete decode is reported per metric without taxing every lookup.
4. **Tag column elision**: numeric only, documented; no text elision.
   A text record carries its tag length inline, so eliding would need a pass over every record before compression,
   and zstd compresses the all-zero length bytes to almost nothing.
   The consumer runs one always-tagged encoder per kind (its answer of 2026-10-17)
   and accepts the uncompressed byte per untagged numeric point in a mixed batch, which it expects to be rare.
5. **Typed sets and materialized containers**: removed; the consumer uses neither.
6. **One decode path that may alias the input**: kept; the consumer confirmed that the bytes it passes are owned copies.
7. **`SearchTimestamp` with a monotone precondition and no detection**: kept.
   The consumer answered on 2026-10-17 that it cannot guarantee monotone input and keeps its linear scan in `NewTchart`;
   its first possible caller is `BinarySearchByTime` after `Materialize`.
8. **Text names default off**: kept; nothing in the consumer reads the payload.
9. **`tests/measure`, `tests/fbs_compare`**: deleted on the v2 branch.
10. **Branch model**: `v2` branch until release, then `main` carries v2 and `release/v1` takes v1.x patches.

## Alternatives considered

- **A generic `Metric(name)` on the set returning an interface or `any` values.**
  Rejected: it either picks numeric silently, losing the text series, or dispatches per point.
- **One permissive option bag for both encoders.**
  Rejected: numeric-only options on the text encoder would fail at runtime;
  the typed interfaces fail at compile time with the same names.
- **Per-metric tag flags.**
  The tag switch is a header bit with no per-entry presence flag, so this is a wire change and out of scope;
  the explicit tag calls and the numeric elision cover the consumer's case.
- **A deduplicating or merging `BlobSet`.**
  Rejected: the set's contract is concatenation in start-time order;
  byte-identical duplicates (the SECS resends) are a few lines in the consumer's repository layer,
  and merging overlapping windows is a policy mebo should not own.
- **`ForEachFrom(lo, yield)` on handles.**
  Proposed by the consumer: its window iteration restarts at index 0 and decodes every member before the window,
  so `SearchTimestamp` finds the start but the walk still pays for the skipped members.
  A walk starting at `lo` would place `lo` by the parts' binary search and skip inside the first member only,
  which is not the skip count in every decode loop that `Slice` was rejected for.
  Left out of v2 as additive; a minor release can add it.
- **`iter.Seq` forms on handles.**
  Left out as in v1.13: the consumer avoids them for the iterator allocation; they can be added in a minor release.
- **A physical `v2/` directory.**
  Rejected in favour of the branch; the Go major-version guide allows both and the directory doubles the tree.
