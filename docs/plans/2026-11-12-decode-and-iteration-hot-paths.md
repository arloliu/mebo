# Decode and Iteration Hot Paths Implementation Plan

**Status:** Tasks 0 to 4 implemented, measured and reviewed; of Task 5 the compatibility runs and the comparison with v1.14.0 are done.
Three external review passes of the plan, the last with no finding; the owner's decisions of 2026-11-12 are recorded at the end.

**Goal:** Make `DecodeBlobSet` and point iteration cheaper on blobs that share timestamps,
without changing the wire format, the public API, or the result of any call.

**Baseline:** commit `fe0ebb0` (v1.14.0), Go 1.26, amd64.
Every reference implementation below is that commit, and so is every "today" number.
Those numbers and the prototype results are single-binary measurements from the investigations;
they say where to look and are not acceptance results.

## Background

Two independent investigations on 2026-11-11 and 2026-11-12 measured the paths the fdc consumer exercises.
Their notes, scratch benchmarks and prototype patches are in `tmp/fdc-lookup-path-2026-11-11/` (not tracked):
`findings.md` with `prototype-shared-groups-and-foreach.patch`,
and `codex-independent-research.md` with its `codex-*.patch` files.

The consumer encodes with little endian, the V2 layout, shared DeltaPacked timestamps and no compression.
It starts metrics by name without storing the names; a names payload appears only on a hash collision.
One of its two numeric encoders writes tags.
The set measured is 2880 blobs of 200 metrics and 30 points.

| Finding | Cost today | Cause |
|---|---|---|
| Building the shared timestamp groups | 34% of the decode profile, 4.8 KB per blob, on blobs with the shared flag | `buildSharedTimestamps` counts offsets in two maps to find the ones several metrics reference |
| Point iteration decodes timestamps the blob already decoded | a prototype that skips it takes 2.5 ns off 11.8 ns per point | `ForEach` and the handle's point walk run the fused timestamp-and-value loop and ignore the group pre-decoded at open |
| Index parsing | 47% of the decode profile | the entry parser is called through a func value, returns the entry by value, and reads five fields through the byte-order interface |
| A constant ALP column decodes slower than a bit-packed one | 3.27 against 1.5 ns per point at 150 points | width 0 goes to the scalar loop, which reads zero bits per value through the slow reader |

The second finding was reproduced independently, on another fixture, another core and another binary:
20% to 28% off `ForEachByName` and the handle's `ForEach` for Chimp, ALP and ALP-RLE.
Both results are single-binary, so Task 2 still carries a layout-averaged gate.

**Tech stack:** Go, the existing `blob` and `internal/encoding/value/alp` packages,
`make test`, `make lint`, `make fuzz`, `tests/compat`.

## Global Constraints

- **Same result.**
  No encoded byte and no public signature changes.
  Every changed path returns what the baseline returns:
  the same indexes, timestamps, value bits, tags, errors and early-stop behaviour.
  This holds for three classes of input, each with its own tests:
  1. blobs `Decode` accepts, well-formed or not;
  2. bytes `Decode` rejects, which must be rejected with the same error;
  3. the in-memory states the existing regression tests craft after decoding, such as a shortened column.
- **A removed check is not a saving.**
  Every validation the decoders run today still runs, in the same order.
- **A frozen reference per task.**
  Each task compares against the baseline:
  the baseline implementation of what it replaces, kept in the test file, or a baseline loop the task leaves unchanged.
  Agreement between two changed paths proves nothing.
- **Contracts of the handle stay as released.**
  Resolution allocates at most once.
  `ForEach` allocates only in the cases its godoc lists: a cold pool, a column past the pool's cap, tag strings.
  A handle stays safe for concurrent reads, so no task may write to a blob or a handle during iteration.
- **Measurement.**
  A task is accepted against the revision just before it, on the same benchmarks and the same padding variants.
  The pull request also reports the cumulative result against `fe0ebb0`, with both commit IDs for every comparison.
  An acceptance time comes only from a layout-averaged run.
  Four layouts per side come from a padding function in `internal/encoding/internal/varint`;
  `go tool nm` must show that it moves the measured functions.
  The layouts run interleaved under `taskset -c 6`, six runs each, merged per side before `benchstat`, with nothing else running.
  "Improves by X" means the median improves by at least X in every layout.
  "Does not slow down" means no layout's median is more than 5% above the baseline's.
  Per-layout medians are kept beside the merged result.
  Allocation counts and bytes per operation are exact and may be quoted from one binary.
  These thresholds accept a task; they do not replace the 20% rule of the performance report.
- The gate for every task is `make test`, `make fuzz` and `make lint`; `go test ./blob` alone is not the gate.
- Each task gets an external review before it is reported done.
- Commits follow the conventional format, one scope per commit, without attribution trailers.

## File structure

| File | Change |
|---|---|
| `blob/numeric_decoder.go` | `buildSharedTimestamps` without maps (Task 1); `parseIndexEntries` loop (Task 3) |
| `blob/numeric_blob_foreach.go` | point iteration over the pre-decoded group (Task 2) |
| `blob/numeric_metric.go` | the handle's point walk takes the same shortcut (Task 2) |
| `internal/encoding/value/alp/alp.go` | fill for a zero-width column (Task 4) |
| `blob/numeric_metric_bench_test.go` | a set fixture with the consumer's options, decode and iteration benchmarks (Task 0) |
| `blob/numeric_decoder_test.go`, `blob/numeric_blob_foreach_test.go`, `blob/numeric_metric_test.go` | reference comparisons per task |
| `internal/encoding/value/alp/*_test.go` | zero-width decode tests (Task 4) |
| `CHANGELOG.md`, `docs/performance.md` | release notes and the regenerated report (Task 5) |

---

### Task 0: Benchmarks that match the consumer

**Files:**
- Modify: `blob/numeric_metric_bench_test.go`

**Why first:** `BenchmarkNumericMetric_LargeSet` stores metric names and uses the V1 layout.
Names, their hash check and the set's binding check are a third of its `DecodeBlobSet` profile,
and none of it is a cost for a consumer that stores no names.

- [ ] Add a fixture builder for a set of blobs with the consumer's options:
  little endian, V2 layout, shared DeltaPacked timestamps, no compression, no stored names.
  Parameters: blobs, metrics, points, value encoding, tags on or off, and the sharing pattern:
  one group for every metric, one group per pair of metrics, or no two metrics alike.
  Values change on about half the points; an all-zero or constant fixture hides the value codec.
- [ ] Add `BenchmarkNumericMetric_SetDecode` over 2880 blobs of 30 points, reporting allocations, bytes and ns per blob:
  200 metrics in one group for Chimp, Chimp with tags and ALP-RLE;
  200 metrics in 100 groups; 200 metrics without a shared offset; 255, 256, 257 and 1000 metrics in one group.
  The 255 to 1000 metric rows use 288 blobs: the figure compared is per blob, and 2880 blobs of 1000 metrics take minutes to build.
  The row without a shared offset does not reach `buildSharedTimestamps`:
  the encoder sets the shared flag only when it finds a group, and no public option makes a flagged blob without one.
  The tags in the tagged row are 2 bytes long; the tag payload is always compressed, so longer tags move that row.
- [ ] Add `BenchmarkNumericMetric_SetForEach` for one metric of the set, reporting ns per point:
  the handle's `ForEach` and `ForEach` blob by blob, for Chimp and ALP-RLE, at 10, 30 and 150 points,
  with and without shared timestamps, with and without tags, walked to the end and stopped at the first point.
- [ ] Add `BenchmarkNumericMetric_SetResolveUnnamed`: `NumericMetricByName` on a set that stores no names,
  at 120, 960 and 2880 blobs, alone and followed by two searches and a 600-point `ForEachValuesRange`,
  reporting bytes and ns per blob.
  No task changes this path; the benchmark records what a windowed reader pays, about 125 µs and 632 KiB at 2880 blobs.
- [ ] Record the baseline at `fe0ebb0` in the pull request description.
  Expected for one group of 200: about 20 ms, 34 ms and 29 ms for the decode, 11.8 and 8.0 ns per point for the walk.
- [ ] Run `make lint`; commit `test(blob): benchmark set decode and iteration without stored names`.

### Task 1: Find shared timestamp groups without maps

**Files:**
- Modify: `blob/numeric_decoder.go` (`buildSharedTimestamps`)
- Test: `blob/numeric_decoder_test.go`

**Contract to keep.**
The function runs only on a blob with the shared-timestamp flag.
The groups are the timestamp offsets that more than one index entry references, after the shared table has been applied.
Each group is decoded from the first entry in index order with that offset.
The table lists the groups sorted by offset, and `first` repeats the group at position 0.
A blob without such an offset keeps a nil table.
A group's slice is non-nil even when it holds no timestamp.
`sorted` records whether the decoded slice is non-decreasing.
Readers depend on each of these.
Lookup tells an empty group from an absent one, and switches from a scan to a bisection above eight groups.
Timestamp iteration and materialization read a group without falling back.
The handle's sortedness check reads `sorted`.

- [ ] Write the reference test first.
  Copy the baseline function into the test file as `referenceSharedTimestamps`.
  For each fixture, compare whether the table is nil, `first`,
  and for every group its offset, whether its slice is nil, its contents and `sorted`.
  Fixtures, built as index entries and a timestamp payload passed to both functions:
  - one group shared by every metric; several groups; a group interleaved with unshared metrics; no shared offset;
  - offsets out of order after the table is applied, such as `[B, A, B]`;
  - a group whose first referencing entry comes before the table's canonical entry;
  - an entry with a count of zero that shares an offset with an entry that has points, in both orders;
  - a group that decodes to no timestamp, and one that decodes to fewer than `Count`;
  - a group whose timestamps are not sorted;
  - 8 and 9 groups; 255, 256, 257 and 1000 metrics.

  Run it against the baseline function before changing it; it must pass.
  Also assert that the index entries and the timestamp payload are unchanged after the call,
  and include a blob with no metric and an entry slice longer than the metric count, of which only the prefix is read.
- [ ] Replace the two maps: copy the offsets into a stack-backed slice, sort it, keep one of each run longer than one,
  then walk the entries in index order and decode each kept offset the first time it appears,
  stopping once every group is filled.
- [ ] Confirm with `-gcflags=-m` that the offset slice stays on the stack for 256 metrics or fewer.
- [ ] Measure with `BenchmarkNumericMetric_SetDecode`.
  Acceptance, all of:
  - allocations per decode of 2880 blobs of 200 metrics in one group fall from 31.7k to at most 23.1k, and bytes fall;
  - that decode improves by at least 15% for Chimp without tags, and by at least 10% with tags and with ALP-RLE
    (the prototype measured 33%, 19% and 21% from one binary);
  - 100 groups and 255, 256, 257 and 1000 metrics do not slow down.
    The path that sorts and finds no group has no benchmark row; the reference test covers its result.

  If a row slows down, fix it or keep the map path for that shape, and say which.
- [ ] Run `make test`, `make fuzz`, `make lint`; commit `perf(blob): find shared timestamp groups without maps`.

### Task 2: Iterate points over the pre-decoded timestamps

**Files:**
- Modify: `blob/numeric_blob_foreach.go` (`forEachFromEntry`), `blob/numeric_metric.go` (`forEachPoints`)
- Test: `blob/numeric_blob_foreach_test.go`, `blob/numeric_metric_test.go`

**Design.**
One helper pairs a value column with a timestamp slice that is already decoded.
The two callers above call it before the encoding dispatch, after their existing range checks,
when every condition below holds.
Otherwise they call the dispatch as today.
On the handle, the existing branch that pairs two materialized columns keeps its place ahead of the helper.
The dispatch tables of `forEachDataPoint` and `allDataPoints` are not edited and stay in step with each other.
`All` and `AllByName` are not changed: they keep yielding what `ForEach` yields, through their own path.

**When the shortcut is taken.**
1. The blob has no tags.
2. A decoded timestamp slice of exactly `Count` elements exists for the entry:
   the entry's shared group, or, on a handle, the part's timestamps decoded by `Materialize`.
   Nothing is decoded or stored to make one; iteration never writes to the blob or the handle.
3. The value encoding is one of the rows below, and its extra condition holds.

| Values | Baseline with the timestamps complete | Shortcut | Extra condition |
|---|---|---|---|
| Gorilla, Chimp | yields until the value stream ends | the value-only loop yields until the same decoder fails | none |
| ALP, ALP-RLE | decodes the whole value column before the first callback, then yields the shorter length | decodes the whole value column before the first callback, then yields its length | `Count` is within the decode pool's cap of 8192 points |
| Raw | yields `Count` points, reading each value by index | the value-only loop yields nothing for a short column | the value range is exactly `Count*8` bytes |

Notes on the two conditions.
Past the pool's cap the value-only ALP walk streams the column and can call back before it meets a fault,
where the baseline decodes everything first; those columns take the dispatch.
The ALP decoders trust a column that passed the check at open,
so a column shortened in memory afterwards can panic in the baseline;
the shortcut must then panic too, before any callback.
The Raw condition is stricter than needed and excludes no column `Decode` accepts.
It is kept exact because the baseline's indexed read is not a plain prefix read:
in native byte order a range that is not a multiple of 8 bytes reads every value as 0, including the ones that are present.

The shortcut also relies on the pre-decoded slice holding the timestamps the baseline's timestamp walk would yield.
The group is decoded at open by the bulk column decoder,
and the baseline walks the same bytes with its own decoder for each encoding pair.
The tests below pin that the two agree whenever the bulk decoder returns `Count` elements.

**One accepted difference.**
The shortcut trusts the decoded timestamps, not the timestamp bytes.
A test can cut an entry's timestamp bytes in memory after decoding and leave the decoded group whole.
The shortcut then yields all `Count` points with the decoded timestamps, which is what `ForEachTimestamps` already yields in that state.
The baseline walk depends on the encodings.
With Delta or DeltaPacked timestamps, or ALP or ALP-RLE values, it yields only the points the cut bytes still decode to.
With Raw timestamps beside Raw, Gorilla or Chimp values it yields `Count` points and reads a missing timestamp as 0.
`All` and `AllByName` keep the baseline outcome.
No wire input reaches this state, because the group is decoded from those bytes, and no existing test crafted it.
The owner accepted the difference on 2026-11-12; a test pins the new outcome.

**Reach.**
`ForEach` and `ForEachByName` on a blob and on a numeric blob set, which all go through `forEachFromEntry`,
and on the handle `ForEach` and `ForEachRange`, which both go through `forEachPoints`.
On a blob with tags the callers skip the helper, so a tagged walk pays no extra call.
`All`, `AllByName`, `ForEachValues`, `ForEachTimestamps` and their range forms are not touched.
Blobs with tags keep the baseline path: tagged numeric blobs are very rare at the consumer.

- [ ] Write the reference tests first.
  The reference for the rows of one entry is the unchanged dispatch:
  call `forEachDataPoint` on the entry's byte ranges and record what happens.
  An outcome is the sequence of calls (index, timestamp, value bits, tag) and how the walk ended:
  it returned, the callback stopped it, or it panicked, and after how many calls.
  Around that, each of the four entry points has its own frozen expectations:
  the return value, the result for a missing metric and a nil callback, the range checks,
  and on the handle the declared base of each member, the clamping of a range,
  and a stop that ends the whole walk, at a member boundary too.
  Compare outcomes for a full walk and for a stop at the first, a middle and the last point.
  Fixtures:
  - every timestamp encoding (Raw, Delta, DeltaPacked) with every value encoding (Raw, Gorilla, Chimp, ALP, ALP-RLE),
    with and without shared timestamps, V2 compact and extended entries, both byte orders;
  - duplicate metric IDs, a missing metric, a nil callback, a count of 0 and of 1,
    and 8191, 8192 and 8193 points around the pool's cap;
  - accepted but short streams:
    a shared group that decodes to fewer than `Count`, a truncated XOR value stream, both at once;
  - crafted states, with the group complete: every truncation of the value column for each value encoding,
    as a slice that keeps its capacity and as one clamped to its length;
    for Raw with a count of 2, lengths 0, 7, 8, 9, 15, 16, 17 and 24 in both byte orders;
    for ALP and ALP-RLE at 8192 and 8193 points, the main, RD and raw schemes and a runs column,
    each cut inside its sidecar and its nested column;
  - on the handle: before and after `Materialize`, a copy of a handle, a set that mixes tagged and untagged members,
    a member whose column is short followed by a complete one (the next member starts at its declared base),
    timestamps decoded by `Materialize` beside values that stay encoded (Raw, ALP, ALP-RLE),
    Gorilla and Chimp values decoded by `Materialize`, which keep the existing paired walk,
    clamped and empty ranges.
  Narrower than this list in the tests as written:
  cuts of a long timestamp column are sampled every 97 bytes and at both ends,
  the large handle comparisons leave out the stops at the first and a middle point,
  the large ALP layout cuts use DeltaPacked timestamps in little endian,
  and stored bytes are altered in compact index entries only.
  A count of 0 is built in memory, although `Decode` accepts such an entry from the wire; the encoder never writes one.
- [ ] Pin the decoder agreement the shortcut relies on, for Raw, Delta and DeltaPacked:
  whenever the bulk decoder returns `Count` timestamps, the baseline walk over the same bytes, given a complete value column,
  yields the same `Count` timestamps.
  Inputs: every truncation of a column, varints that are not minimal, a tenth varint byte that overflows, trailing bytes,
  every packed lane width, partial groups, sums that wrap,
  at 8191, 8192 and 8193 points, on the scalar path and on each SIMD path the machine has.
- [ ] Add a rejection test for each malformed wire input the fixtures derive from:
  `Decode` returns the same error as at the baseline.
- [ ] Add the helper and call it from `forEachFromEntry` and `forEachPoints`.
- [ ] Assert the allocation contract: zero allocations for `ForEach` with Gorilla and Chimp on the shortcut,
  with a capturing callback built inside the measured closure and the race-tag skip the existing pool assertions use;
  for ALP up to 8192 points, zero once the pool is warm.
- [ ] Run the package under the race detector with concurrent readers of one handle on the shortcut,
  and start a second walk from inside an ALP callback: the pooled buffer of the outer walk must stay intact.
- [ ] Measure with `BenchmarkNumericMetric_SetForEach`.
  Acceptance: the full walk improves by at least 10% for Chimp and for ALP-RLE at 30 points.
  The walks at 10 and 150 points, blobs without shared timestamps, blobs with tags,
  and the stop at the first point do not slow down.
  If the improvement does not hold, drop the task and record the result.
- [ ] Run `make test`, `make fuzz`, `make lint`;
  commit `perf(blob): iterate points over the pre-decoded shared timestamps`.

### Task 3: Parse index entries without the indirect call

**Files:**
- Modify: `blob/numeric_decoder.go` (`parseIndexEntries`)
- Test: `blob/numeric_decoder_test.go`

**Scope.**
Only the extraction of an entry's fields changes.
The public `section.NumericIndexEntry` and the one slice of entries per blob stay;
a smaller private index is a separate design.
The validation sequence stays exactly as it is:
1. the bounds check of the whole index;
2. the overlap check against the timestamp payload;
3. the per-entry parse and its errors;
4. the accumulation of delta offsets into `int`, and the optional ID slice;
5. the rejection of a decreasing ID, on V2 and V2Ext only, then of a decreasing offset;
6. the final payload-bound and negative-offset checks.

- [ ] Profile `BenchmarkNumericMetric_SetDecode` after Task 1.
  Take the cumulative share of `parseIndexEntries`, which includes the parser it calls, less the allocation of the slice.
  Go on only if it is above 25% of the decode.
- [ ] Write the reference test first.
  Copy the baseline `parseIndexEntries` into the test file as the reference.
  Compare the returned entries (absolute offsets and derived lengths), the ID slice and whether it is nil, and the error.
  Fixtures:
  - compact and extended entries in both byte orders;
  - a nonzero first offset; compact offsets that accumulate past 65535; zero deltas;
  - duplicate IDs; decreasing IDs on V1 (accepted) and on V2 (rejected); `needMetricIDs` true and false;
  - a truncated index; an index that overlaps the timestamp payload;
  - inputs with two faults at once, to pin which error wins:
    a truncated index that also overlaps the payload,
    a decreasing ID followed by a bad extended entry,
    an extended entry with nonzero reserved bytes and a field that overflows;
  - an extended entry with a nonzero reserved field.

  Two inputs of this list cannot exist where `int` is 64 bits, so no fixture exercises them:
  a decreasing offset (the stored deltas are unsigned and the index ends before the 32-bit payload offset),
  and the overflow error of an extended field, which leaves the reserved-bytes error alone in that fixture.
  The checks themselves are not touched.
- [ ] Split the loop by entry size and byte order.
  The compact little-endian case reads its fields with `binary.LittleEndian`, straight into the entry in place:
  `MetricID` at `[0:8]`, then `Count` and the three offsets as `uint16` at `[8:10]`, `[10:12]`, `[12:14]` and `[14:16]`.
  Every other case calls the current parser.
- [ ] Measure with `BenchmarkNumericMetric_SetDecode`.
  Acceptance: the decode of 200 metrics in one group improves by at least 10%, and no other row slows down.
  If not, drop the task and record the result.
- [ ] Run `make test`, `make fuzz`, `make lint`; commit `perf(blob): parse compact index entries in place`.

### Task 4: Fill a zero-width ALP column

**Files:**
- Modify: `internal/encoding/value/alp/alp.go` (`decodeMainInto`)
- Test: `internal/encoding/value/alp/` tests

**Applies to:** ALP and ALP-RLE values where every point of a column encodes to the same integer,
a constant column for example.
Width 0 means every code is 0.
The decoded value is the frame minimum scaled, not zero, and the column may still carry exceptions.

**Scope.**
Only `decodeMainInto`, the bulk reader, changes.
`allMain` and `atMain` read a zero-width code per value in the same way; this task leaves them as they are.

| Public path | ALP reader | Changed |
|---|---|---|
| Blob and flat set materialization | `DecodeAll`, `decodeMainInto` | yes |
| `ForEach`, `ForEachByName`, `All`, `AllByName`, the handle's `ForEach` and `ForEachRange` | bulk decode, `decodeMainInto` | yes |
| The `ForEachValues` family up to 8192 points | bulk decode, `decodeMainInto` | yes |
| The `ForEachValues` family past 8192 points, the `AllValues` family | `Each` or `All`, `allMain` | no |
| `ValueAt` and `At` | `atMain` | no |
| A runs column | its bulk reader reaches `decodeMainInto`; its streaming and random readers reach `allMain` and `atMain` | bulk only |

The handle's `Materialize` leaves ALP values encoded, so it does not decode them at all.

- [ ] Write the reference test first:
  the baseline loop, copied into the test file, against the new code, compared with `math.Float64bits`.
  Fixtures:
  - both byte orders; positive, negative and extreme frame minima; several exponent and factor pairs;
  - `dst` of length 0, 1, `count-1`, `count` and `count+1`;
  - exceptions inside and beyond the written prefix, including signed zero and NaN payloads;
  - a runs column whose nested main column has width 0 and exceptions.

  Read the same fixtures through a bulk point walk, a flat materialization, the codec's `Each` and `At`:
  all four give the baseline's bits, and only the bulk reader's code changed.
- [ ] In `decodeMainInto`, when `width` is 0, compute `float64(mn) * alpPow10[ff] * alpInvPow10[ee]` once, in that order,
  and fill `dst[:n]` with it, where `n` is the smaller of `count` and `len(dst)`.
  The exception sidecar is still located by `count`, the existing patch still runs, and `dst` beyond `n` is not written.
  The fill is a doubling copy of the filled prefix, chosen over a store loop to meet the acceptance below.
  At 30 points it is about 12% slower than the store loop (15.5 against 13.9 ns) and four times faster than the baseline;
  the trade was taken for the 150-point column.
- [ ] Benchmark a constant column at 30 and 150 points.
  Acceptance, in every layout:
  the constant column at 150 points takes less time per point than a bit-packed column of 150 points measured in the same run,
  and the bit-packed column does not slow down.
- [ ] Run `make test`, `make fuzz`, the encoding package's SIMD test target, and `make lint`.
  `make fuzz` includes `FuzzALPDecode`, which already feeds width 0.
  Commit `perf(encoding): fill a constant ALP column instead of reading zero bits`.

### Task 5: Verify and release

- [ ] Run `tests/compat/run_compat.sh v1.14.0 <branch>`:
  the baseline and the branch read each other's blobs to the same points, ALP-RLE included.
- [ ] Run `tests/compat/run_compat.sh v1.10.0 <branch>`:
  the oldest reader in production still reads what the branch writes.
  It does not cover ALP-RLE, which v1.10.0 rejects by design.
- [ ] Regenerate `docs/performance.md` with the report skill if any number it quotes moved.
- [ ] Add the CHANGELOG entry under a performance heading: what got faster and by how much, measured how.
- [ ] Squash by scope, open the pull request with the baseline and the results, get the external review,
  and ask the owner before merging.
- [ ] Release as v1.14.1, a patch with performance changes only, following the release convention v1.12.1 used.
  Tagging and publishing need the owner's go-ahead at that point.

---

## Results

Layout-averaged as the constraints describe: four padded layouts per side, six runs each, one pinned core.
Raw output is in `tmp/fdc-lookup-path-2026-11-11/acceptance/` (not tracked).
The table of the tasks comes first, then the direct comparison of `fe0ebb0` with the branch.
Each task was measured on the branch as it was committed, before its history was squashed for the pull request,
so the table names the changes and not commit IDs; the production code of each step is the same after the squash.

| Task | Compared | Row | Before | After | Change | Per layout |
|---|---|---|---|---|---|---|
| 1 | the benchmarks, then the shared groups | decode, 200 metrics in one group, Chimp | 20.4 ms | 14.1 ms | -31.0% | -30.8 to -31.5 |
| 1 | | same with tags | 35.3 ms | 28.7 ms | -18.6% | -18.6 to -18.8 |
| 1 | | same with ALP-RLE | 27.1 ms | 20.2 ms | -25.4% | -25.1 to -25.7 |
| 1 | | 200 metrics in 100 groups | 67.9 ms | 52.6 ms | -22.6% | -21.9 to -23.2 |
| 4 | the shared groups, then the fill | constant column, 150 points | 269.8 ns | 27.6 ns | -89.8% | -89.6 to -90.0 |
| 4 | | constant column, 30 points | 62.8 ns | 15.5 ns | -75.3% | -74.9 to -75.7 |
| 4 | | bit-packed column, 150 points | 44.8 ns | 45.3 ns | +1.1% | see below |
| 3 | the fill, then the index parse | decode, 200 metrics in one group, Chimp | 14.07 ms | 8.05 ms | -42.8% | -42.1 to -43.6 |
| 3 | | same with tags | 28.7 ms | 23.3 ms | -18.7% | -17.5 to -19.8 |
| 3 | | same with ALP-RLE | 20.3 ms | 14.2 ms | -29.9% | -28.7 to -30.6 |
| 2 | the index parse, then the point walk | handle `ForEach`, 30 points, Chimp | 11.97 ns/point | 9.48 | -20.8% | -19.7 to -21.3 |
| 2 | | blob by blob, 30 points, Chimp | 12.28 | 9.72 | -20.8% | -12.6 to -22.8 |
| 2 | | handle `ForEach`, 30 points, ALP-RLE | 7.29 | 5.74 | -21.3% | -14.8 to -25.8 |
| 2 | | blob by blob, 30 points, ALP-RLE | 7.01 | 5.90 | -15.8% | -10.3 to -18.9 |

Task 1: allocations per decode fall from 31,697 to 23,056, and the rows of 255 to 1000 metrics improve by 30% to 34%.
Task 3: the profile gate read 56%, every other decode row improves by 10% to 52%, and allocations do not change.
Task 4: in every layout the constant column at 150 points (27.5 to 27.6 ns) is faster than the bit-packed one (45.2 to 47.0 ns).
Task 2: on shared blobs without tags a walk stopped at its first point improves by 38% to 69%,
and the walks at 10 and 150 points, measured before the tag guard was added, improve by 19% to 30% and by 3% to 12%.

### Against v1.14.0

`fe0ebb0` with the new benchmark files laid over it, against the branch after the point walk, measured the same way.

| Row | v1.14.0 | Branch | Change | Per layout |
|---|---|---|---|---|
| decode, 200 metrics in one group, Chimp | 20.42 ms | 8.03 ms | -60.7% | -60.1 to -61.0 |
| same with tags | 35.35 ms | 23.15 ms | -34.5% | -34.1 to -35.1 |
| same with ALP-RLE | 27.11 ms | 14.20 ms | -47.6% | -47.2 to -47.9 |
| 200 metrics in 100 groups | 67.73 ms | 46.71 ms | -31.0% | -30.3 to -31.7 |
| 200 metrics, no shared offset | 11.27 ms | 5.32 ms | -52.8% | -52.2 to -52.8 |
| 1000 metrics in one group, 288 blobs | 9.25 ms | 3.10 ms | -66.5% | -66.2 to -66.6 |
| handle `ForEach`, 30 points, Chimp | 11.98 ns/point | 9.49 | -20.8% | -20.2 to -22.1 |
| blob by blob, 30 points, Chimp | 12.37 | 9.70 | -21.6% | -19.4 to -22.6 |
| handle `ForEach`, 30 points, ALP-RLE | 7.02 | 5.55 | -21.0% | -19.0 to -21.3 |
| blob by blob, 30 points, ALP-RLE | 6.87 | 5.57 | -18.8% | -9.8 to -26.2 |
| handle walk stopped at the first point, Chimp | 76.5 ns | 24.2 ns | -68.4% | -68.3 to -69.3 |
| constant ALP column, 150 points | 268.0 ns | 27.6 ns | -89.7% | -89.5 to -90.0 |
| constant ALP column, 30 points | 62.7 ns | 15.5 ns | -75.3% | -74.9 to -75.8 |
| bit-packed ALP column, 150 points | 44.8 ns | 45.2 ns | +0.7% | +0.6 to +1.5 |

Allocations per decode of the first row fall from 31,696 to 23,056 and its bytes from 63.2 to 49.1 MB.
`NumericMetricByName` across 2880 blobs reads 18% faster (131 to 108 µs, -16% to -21% per layout) although no task changed it,
and is within 3% at 120 and 960 blobs; the likely cause is that a decode now allocates less between the index entries a resolution reads.
Both compatibility runs pass, 19 of 19: `v1.14.0` and `v1.10.0` against the branch.

### Two readings

Two readings that the rule "no layout more than 5% above the baseline" does not settle by itself:

- The bit-packed control of Task 4 has two modes, about 45.0 and 48.8 ns, on both sides and in a different layout each session.
  Comparing the fast modes, the difference is about 1%.
- The rows of Task 2 that walk blob by blob and stop at the first point have a slow mode about 29% above the fast one.
  The baseline is in it in one layout and the change in another, in both sessions,
  so one layout reads +11% to +31% and another -8% to -20% on rows whose path did not change.
  Mode against mode, those rows cost 1% to 3% more, about 2 ns per call, for the check that precedes the dispatch.
  The full walks of blobs with tags or without a shared group are within 1% on the handle.
  The task is kept on that reading; the rule as written is not met in the layout where only the change is in the slow mode.

---

## Out of scope, recorded

- **A cursor for increasing timestamp searches.**
  A prototype takes an ordered search from 24.5 ns to 5.7 ns (`codex-timestamp-cursor.patch`).
  It is a new public API, and the owner declined it on 2026-11-11.
  The consumer reaches 2.3 ns a lookup without it,
  with flat columns from `MaterializeNumericMetricByName` and an index kept between lookups.
  Recorded for the owner to reconsider if the consumer cannot hold flat columns.
- **Tags that alias the payload.**
  Iterating 600 tagged points costs 12.4 µs and 600 allocations.
  A prototype that borrows the tag strings costs 5.0 µs and none (`codex-borrowed-tag-iteration.patch`).
  The released godoc promises a copied string, so this needs a separately named API and its own design.
  Tagged numeric blobs are very rare at the consumer, so it is not pursued.
- **The exponent search in the ALP encoder.**
  It costs about 0.85 µs per column at both 30 and 150 points.
  With the RD cut search it is most of the encode at 30 points,
  where ALP encodes 3.5 times and ALP-RLE 4.4 times slower than Chimp.
  A prototype that seeds the search from the previous column moved the time by 1% and changed the encoded bytes.
  Any shortcut has to produce the same bytes as the full search, which needs its own design.
- **The tail of a short ALP column.**
  The last values of a 30-point column take the slow bit reader, 9% of its decode.
- **The ALP column check at open**, 15 ns per column.
  It is a deliberate check; only a cheaper form of the same check would count.
- **The tag payload**, one zstd decompress per blob, 5 µs and 17.6 KB.
  The tag codec is fixed by the format.
- **`ApplySharedTimestampTable`**, 8% of the decode: a status slice per blob and one interface call per member.
- **A runs column cut in memory reads a pooled buffer's old contents.**
  The runs layout of ALP-RLE finds its nested column empty, decodes nothing, and expands the runs from whatever the buffer held,
  so two walks of the same cut column can yield different values.
  The check at open rejects such a column on the wire; only a test that edits a decoded blob reaches it.
  It was there before this plan and both the baseline walk and the shortcut behave the same way.
- **A smaller private index.**
  It changes a representation that lookups and validation read, and was not prototyped.
- **Decoding a set while borrowing the names payload.**
  It saves one allocation and 2.6 KB per blob that stores names; the consumer stores none.
- **Consumer-side changes.**
  The consumer's nearest-timestamp rule scans the whole target chart per source timestamp today.
  Its fix needs no mebo change, as above.
  The choice between Chimp, ALP and ALP-RLE values is the consumer's and depends on its real value distribution.
- **A cheaper resolution of a handle across a set.**
  With the consumer's options it costs 125 µs and 632 KiB for 2880 blobs, 43 ns and 224 bytes per member.
  Resolving and reading a 600-point window costs 127 µs, so for a metric referenced once the resolution is the whole cost.
  The work is memory: one cold index entry read per member, and a part written per member.
  Measured candidates:
  one pass instead of two gives nothing at 2880 members (`prototype-single-pass-resolve-rejected.patch`);
  a part of about 136 bytes would save 39% of the bytes and, by the measured slope, 10% to 15% of the time,
  at the price of moving the materialized columns out of the part, which every lookup after `Materialize` reads.
  The large gain is resolving only the members a time range overlaps: about 21 of 2880 for a 600-point window.
  That is a new public API, so it cannot ship in a patch release; it is a candidate for the next minor and needs its own design.
- **Ruled out by measurement:** a windowed Chimp `At`, changes to the `Materialize` algorithm,
  an ordinal fast path in the set's collision probe (3% to 6%, one binary).

## Decisions

Taken by the owner on 2026-11-12.

1. Tagged numeric blobs are very rare at the consumer.
   Its decode is the untagged row, Task 2 stays limited to blobs without tags, and tags that alias the payload are not pursued.
2. The dataloader's inject handler does not decode large sets per request; the size follows the context.
   Task 1 and Task 3 matter there in proportion to the context.
3. The work ships as v1.14.1.
4. A cheaper handle resolution may join the plan.
   The measurements taken after that decision are in "Out of scope": no candidate that fits a patch release gives a large gain.
   So v1.14.1 leaves the resolution as it is and only adds the benchmark of Task 0.
   Resolving the members a time range overlaps gets its own design, for the next minor.
5. Point iteration may differ from the baseline in one state that only a test can build:
   timestamp bytes cut in memory beside a complete decoded group.
   Task 2 describes it.
