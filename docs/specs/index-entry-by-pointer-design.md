# Design: look up index entries by pointer in the point accessors

**Date:** 2026-10-06
**Status:** implemented in 0b0e8af and measured (see Results);
approved by the owner on 2026-10-06 (fix the copy; scope below).
The spec's Codex review (`tmp/reviews/index-entry-by-pointer-spec-codex-review-v1.md`, not committed)
added the pointer receiver of `getOrdinal`, the parity test,
and checkable wording for the escape, allocation and resync steps.
The implementation's Codex review (`tmp/reviews/index-entry-by-pointer-impl-codex-review-v1.md`, verdict merge)
added the negative-index check to the parity test, the declaration order, and the saved validation outputs.
Owner request (2026-10-06): find why shared-timestamp `TimestampAt` runs at two speeds, then fix it.
Research artifacts (not committed): `tmp/timestampat-bimodality-2026-10-06/`
(`findings.md`, the classification and counter scripts, the scratch patch).
Earlier evidence: the "Reproducible" gate in `docs/specs/measurev2-fast-report-runs-design.md`.

## Goal

Remove the struct copy that makes `NumericBlob.TimestampAt` on shared timestamps run at one of two speeds 29% apart,
and the same copy in the other point accessors,
without changing any result or any exported signature.

## Mechanism

Measured on the Ryzen 9 9950X3D with `perf` at `main` 6dd328e, pinned to CPU 6.

- `indexMaps.GetByID` returns the 64-byte `section.NumericIndexEntry` by value, in eight registers.
  `TimestampAt` spills them to its frame with eight 8-byte stores,
  then copies the struct with 16-byte `MOVUPS` loads before it calls `timestampAtFromEntry`.
  A 16-byte load that spans two 8-byte stores cannot be served by store forwarding.
- `perf record -e bp_redirects.resync` puts 99% of `TimestampAt`'s pipeline resyncs on that first 16-byte load,
  about 2.3 million per run in the slow state and 0.48 million in the fast one.
  Branch misses, op-cache misses, instruction-cache misses and instruction-TLB misses do not differ between the states.
- The state follows the binary file's page-cache pages:
  evicting a file changed its state in 11 of 18 cases, against 1 of 18 for untouched files.
  How the placement changes the resync rate is not established.
- A scratch build that fetched the entry by pointer ran the 15 shared `TimestampAt` cells at 1,396–1,417 ns/op
  in 48 of 48 runs after the first pass (16 copies of the binary) and in 16 of 16 under `perf stat`,
  against about 1,667 (fast) and 2,100 (slow) before;
  `TimestampAt`'s share of the resyncs fell from 14.6–44.5% to 0.5%.
  The first pass, right after the copies were made, had three outliers at 1,693–1,896.

The same spill-then-wide-load sequence is in every point accessor that takes an entry from a by-value lookup.
A scan of the `blob` test binary's machine code for 16-byte loads of frame slots written by 8-byte stores finds it in
`TimestampAt`, `ValueAt`, `TagAt` and their `ByName` forms on `NumericBlob` and `TextBlob`,
and in the point accessors of `BlobSet`, `NumericBlobSet` and `TextBlobSet`.
`section.TextIndexEntry` is 24 bytes, so its copy is one 16-byte load.
`NumericBlob.ValueAt` holds 20–26% of a measurev2 run's resyncs, 97% of them on its own copy.

## Design

`indexMaps` gets four lookups that return a pointer into its `sorted` slice, or nil when the metric is absent:

| New lookup | By-value form it backs |
|---|---|
| `entryByID(metricID)` | `GetByID` |
| `entryByName(metricName)` | `GetByName` |
| `entryFor(metricID, targetName, collided)` | `resolveEntry` |
| `entryForName(metricName, skipStripped)` | `resolveEntryByName` |

- They have pointer receivers,
  so a call passes the address of the blob's `index` field instead of copying the 96-byte struct.
  `getOrdinal`, which they call, gets a pointer receiver for the same reason.
- The by-value forms stay, for the iteration and materialization paths, and become wrappers that dereference the pointer;
  the lookup rules then live in one place.
- The entry helpers of the point accessors take the pointer:
  `timestampAtFromEntry`, `valueAtFromEntry` and `tagAtFromEntry` on `NumericBlob` and `TextBlob`,
  and `numericPointFromEntry` and `textPointFromEntry`.
  Every caller of these helpers is a point accessor.
- The point accessors call the pointer lookups and pass the pointer on:
  the six `At` and `AtByName` methods of `NumericBlob` and of `TextBlob`,
  and the point accessors of `BlobSet`, `NumericBlobSet` and `TextBlobSet`.
- The pointers are read-only.
  A blob's index is never modified after decoding, and no accessor writes through the pointer or keeps it.

Out of scope:

- Iteration, `Len` and materialization, which look an entry up once per metric and then visit every point.
- Exported signatures and the value receivers of the blob types.
- `MaterializedNumericBlobSet`, whose accessors do not use index entries.

## Verification

1. Table-driven tests of the four pointer lookups on hand-built indexes:
   V1 (`byID`), V2 (`sortedIDs`), a collided ID (first entry wins by ID, each name resolves by name),
   retained names with a query that only hash-matches, a stripped member, and absent metrics.
   Each case checks nil or the identity of the returned pointer with the expected `sorted` element.
2. A parity test on decoded blobs and sets:
   every point accessor returns, index by index, what the iterator over the same metric yields
   (the iterators keep the by-value lookups), and reports absence one past the end.
   It covers `NumericBlob`, `TextBlob`, `NumericBlobSet`, `TextBlobSet` and `BlobSet`, by ID and by name,
   on V1 and V2 indexes, a collided ID, a retained name that only hash-matches, a stripped member and absent metrics.
   The same test passes on 6dd328e.
3. `make lint` and `make test`.
4. The machine-code scan above reports no 16-byte load of an 8-byte-stored slot that copies an index entry
   in the accessors in scope
   (`tmp/timestampat-bimodality-2026-10-06/scan_stlf.py` on `go tool objdump` of the `blob` test binary).
5. The compiler's escape report for `blob` (`go build -gcflags=-m ./blob`, its `moved to heap` and `escapes to heap` lines)
   is the same before and after.
   Allocations per operation are compared in step 6.
6. One layout-averaged `tests/measurev2/layouts.sh` run, on an otherwise idle machine:
   - the 15 shared `TimestampAt` cells show no slow layout file:
     for each cell, the medians of the four layouts are within 5% of one another;
   - against the stored run of the same day at 6dd328e (`tmp/measurev2-validation-2026-10-06b/gate4/run1`),
     no cell is slower by more than gate 4's per-cell limit, 11%
     (`cell_max` in `tests/measurev2/acceptance_thresholds.json`),
     allocs/op is equal for every cell, and B/op is within max(2%, 64 bytes).
7. `perf record -e bp_redirects.resync:u -c 200` on one `measurev2 -profile mix_monitoring -benchtime 10ms` run of the new build,
   summed by symbol (`perf report --sort sym`):
   `blob.NumericBlob.TimestampAt` and `blob.NumericBlob.ValueAt` each hold under 2% of the samples.

## Consequences

Decided by the owner on 2026-10-07, after the measurement:

- Gates 2 and 4 no longer exempt the shared `TimestampAt` cells from their per-cell limits,
  and the report template no longer says that those timings can read 29% high;
  the `validate.sh` run that confirms it is recorded under Results.
- `docs/performance.md` is regenerated from that validation's gate-4 run.
- The shared buffer pool is left as it is.
  A probe of the cold-pool cost (`tmp/pool-cold-encode-2026-10-06/`) found a cold encode 4–13 µs
  and 0.4–0.5 MB over a warm one at 100 × 150 on one core,
  and prototypes of separate pools, with and without a remembered capacity,
  were no better overall: the remembered capacity helped only Gorilla and Chimp on one core,
  and separate pools alone were slower after one GC with several cores.

## Results

Measured on 2026-10-06 at 0b0e8af, Ryzen 9 9950X3D, Go 1.26.7, pinned to CPU 6.
Raw data (not committed): `tmp/index-entry-by-pointer-2026-10-06/`;
its `validation/` holds the lint and test logs, the scans, the escape reports and the script that normalizes them.

- **Tests.**
  `make lint` reports no issues and `make test` passes.
  The parity test also passes on 6dd328e.
- **Machine code.**
  Before, the scan flags 30 of the 39 accessors and helpers it covers, 95 loads in all.
  After, it flags `BlobSet.TextAt` and `BlobSet.TextAtByName`, three loads each.
  Those copy the `TextBlob` argument of `textPointFromEntry`; no accessor copies an index entry any more.
- **Escapes.**
  The escape report for `blob` is the same before and after (319 distinct lines).
- **Layout-averaged run** (`perf/merged`, 420 cells, 6.9 minutes) against the stored run at 6dd328e,
  in which all four layout files were in the slow state:

  | Cells | n | Median change | Range |
  |---|---:|---:|---|
  | `TimestampAt`, shared timestamps | 15 | −49.6% | −49.7% to −49.2% |
  | `TimestampAt`, per-metric Raw | 5 | −45.9% | −46.5% to −45.4% |
  | `TimestampAt`, per-metric DeltaPacked | 5 | −10.8% | −11.9% to −10.0% |
  | `TimestampAt`, per-metric Delta | 5 | −9.7% | −9.8% to −7.9% |
  | `ValueAt`, Raw | 21 | −42.4% | −46.5% to −42.3% |
  | `ValueAt`, ALP | 21 | −35.8% | −36.8% to −32.5% |
  | `ValueAt`, ALP-RLE | 21 | −31.2% | −36.8% to −27.1% |
  | `ValueAt`, Chimp | 21 | −4.9% | −7.4% to −2.3% |
  | `ValueAt`, Gorilla | 36 | −2.2% | −10.6% to −0.6% |
  | encode | 120 | +0.2% | −0.5% to +2.2% |
  | decode | 30 | +0.4% | −1.2% to +0.8% |
  | iterate | 120 | +0.3% | −2.3% to +1.8% |

  The shared `TimestampAt` cells take 1,060–1,069 ns/op, against 2,101–2,111 in that run
  and about 1,630 in the old fast state.
  The medians of the four layouts are within 2.2% of one another for every one of those cells.
  No cell is slower by more than 5%, allocs/op is equal for every cell, and B/op is within the limit.
- **Speed states.**
  16 copies of the new binary, three passes of `measurev2 -profile mix_monitoring -benchtime 10ms`:
  all 48 runs put the shared `TimestampAt` median at 1,055–1,066 ns/op.
- **Resyncs.**
  One `perf record` run counts 1.85 million resyncs in the process,
  against 3.3 million (fast) and 5.6 million (slow) before.
  `NumericBlob.ValueAt` holds 0.38% of the samples, and no sample falls in `NumericBlob.TimestampAt`.
- **Earlier run.**
  The same change was first measured before `entryByID` and `entryByName` were moved below the exported methods
  (`pre-reorder-7c7f7ea/`); its median changes are within 1.5 points of the table's in every row.
  In its five passes over 16 copies, 66 of 80 runs were at 1,061–1,073 ns/op;
  the other 14, at 1,076–1,344 with fastest cells at 1,068–1,083, fell within 15 consecutive runs,
  and the same files ran at 1,061–1,068 in the two passes after that.
  That pattern points to a disturbance of the machine and not to a state of a file, but nothing recorded its cause.
