# Design: an ID for the timestamp group a handle reads

**Date:** 2026-11-15
**Status:** implemented on `feat/timestamp-group-id`, not released.
Owner request (2026-11-15): design and implement the timestamp group ID the consumer asked for.
The consumer's requirement note of 2026-11-14 is in its repository, under `tmp/defense-path-cost/`.
The owner chose an opaque ID there, rather than returning the decoded timestamps.

## Goal

Let a reader learn that two metrics of one blob read the same timestamps, without reading them.

The consumer computes the largest gap between timestamps in a window, once per metric and blob.
That scan is about 70% of the compute of its defense path (the consumer's figure).
The result depends on the timestamps and the window alone,
and the 100 to 300 metrics of one of its blobs share their timestamps,
so one scan per group and blob can serve every metric of the group.

No encoder, format or decode change is needed.
The decoder already decodes each shared group once when it opens a blob (`buildSharedTimestamps`),
and each handle part already holds its group's slice (`numericMetricPart.shared`).
Only the identity of that slice was not exposed.

## API

```go
// TimestampGroupID identifies the timestamp sequence a handle on one blob reads.
type TimestampGroupID struct{ /* unexported, comparable */ }

// TimestampGroup returns the ID of the timestamp sequence the handle reads, and whether it has one.
func (h *NumericMetric) TimestampGroup() (id TimestampGroupID, ok bool)
```

Both live in `blob/numeric_metric.go`.
The addition is a new exported type and method, so it ships in a minor release.

## Contract

1. **Equal IDs mean the same timestamps.**
   Two handles with equal IDs answer the same on every timestamp accessor, before and after `Materialize`:
   `Len`, `TimestampAt`, `ForEachTimestamps`, `ForEachTimestampsRange`, `SearchTimestamp` and `TimestampsSorted`.
2. **The promise covers the timestamps and not the points.**
   `ForEach`, `ForEachRange` and `At` also read the metric's values and tags, and stop where those cannot be read.
   Two metrics of one group whose value columns end at different points walk different numbers of points;
   the decoder accepts such a blob, because it checks the length of a Gorilla or Chimp column only for being non-empty.
3. **Unequal IDs mean nothing.**
   The same bytes decoded twice give two blobs whose IDs differ.
   The consumer accepts this: a miss costs a scan and the result is still right.
4. **`ok` is false** when the handle has no single group, and the caller then reads the timestamps itself:
   - the metric has timestamps of its own;
   - the handle has points in more than one blob;
   - the handle has no points;
   - the group does not hold exactly the metric's points, which only a malformed blob has.
5. **The zero value is never returned with `ok`.**
6. **An ID keeps its group's decoded timestamps reachable.**
   A table keyed by IDs should not outlive the blobs it was filled from.
7. **Cost:** no timestamp is read and nothing is allocated.
   It is safe to call concurrently, as the other handle accessors are.

## Representation

```go
type TimestampGroupID struct {
    first *int64 // the first of the group's decoded timestamps
}
```

- The pointer is the group's identity.
  Each group of each decoded blob is its own allocation, so the pointer is unique to the group and the blob.
  It is taken with `&shared[0]`, which needs no `unsafe`; a group with no element reports `ok` false.
  The address of the group's struct would not do:
  `sharedTimestamps.first` is a copy of `groups[0]`, so one group has two addresses.
- A length is not part of the ID, because `ok` requires `len(shared) == count`: the pointer already fixes the length.
- Nothing about the metric's own timestamp range is part of the ID.
  Every timestamp accessor of a part with a full group reads the group's slice and never decodes that range:
  `TimestampAt` and `SearchTimestamp` index it, `ForEachTimestamps` and its range form walk it,
  `TimestampsSorted` reads it or the order the decoder recorded for it,
  and `Materialize` finds no timestamps to decode.
  A first draft carried the range's length to cover `ForEach`, which decodes the range in some cases.
  The review showed that `ForEach` cannot be promised anyway (rule 2),
  and that the length cannot differ in a decoded blob:
  the shared table gives every member its canonical entry's offset and length.

## Not built

- **Handles over several blobs.**
  The consumer reuses a result per blob, so that one metric losing points in one blob misses in that blob only.
  It resolves a handle per metric and blob for this, and does not need one ID for a whole set.
- **A lookup on the blob that skips the handle.**
  Resolving a handle on one blob costs tens of nanoseconds against a scan of 30 timestamps at about a microsecond.
  Add it if a profile of the consumer shows the resolve.
- **Text metrics.**
  Text blobs do not share timestamps.

## Tests

In `blob/numeric_metric_test.go`, over a blob without tags and a blob with tags
(the timestamp accessors do not look at the value encoding, so one is tested):

- two metrics of one group have equal IDs and answer the same on every timestamp accessor,
  with the timestamps that were encoded, before and after `Materialize`;
  two groups of one blob have different IDs; a metric with its own timestamps has none;
- the ID keys a map; a copy of the blob gives the same IDs;
- a handle from a set that holds the metric in one blob has that blob's ID;
- no ID for the zero value, a blob without groups, a handle over two blobs,
  a group shorter or longer than the count, and a handle without points;
- an unreadable timestamp range changes neither the ID nor the timestamps;
- a truncated value column keeps the ID and the timestamps while its point walk stops short (rule 2);
- the same bytes decoded twice give different IDs;
- the call allocates nothing.

The tests compare IDs with `==`.
`require.Equal` follows the pointer inside and would call two different groups with the same first timestamp equal.

## Review

The Codex review of the first draft (`tmp/timestamp-group-id_post_implementation_review_v1.md`, not committed)
found that "the same timestamps on every read" did not hold for `ForEach` on a blob the decoder accepts.
That narrowed rule 1 to the timestamp accessors, added rule 2, removed the range length from the ID,
and added the comparison of every timestamp accessor to the tests.
The second review (`tmp/timestamp-group-id_post_implementation_review_v2.md`, not committed)
found the narrowed rule to hold and returned merge-clean.
It named two gaps in the new tests, both closed since:
the search results had no expected value of their own, and the walks ignored the index they were called with.
