# Design: ALP with run-length front end (`TypeALPRLE`, 0x7)

**Date:** 2026-10-03
**Status:** Phases 0–4 done on `feat/alp-rle` (2026-10-04).
The ratio gate and all three speed gates pass,
measured layout-averaged through the real blob encoder in Phase 3.
The `DecodeAll` gate was restated against Chimp by the owner on 2026-10-03.

## Goal

Add one value encoding, `TypeALPRLE` (0x7), that puts a run-length front end in front of the existing ALP codec.
It targets the one shape where ALP loses to Chimp and Gorilla:
columns where many consecutive points repeat the previous value.
With that gap closed, switching from Chimp to ALP-family encoding carries no ratio downside on any measured shape.

Production context (owner, 2026-10-03):
blobs average about 100 metrics × 150 points, encoded with shared DeltaPacked timestamps and **Chimp** values,
and run at about 3.3 B/point over the long term.
Real data cannot be exported, so every number below comes from synthetic shapes calibrated to that figure.
Background: `tmp/next-release-codec-research/entropy-ceiling.md` (gitignored working notes)
and the PoC tests in `internal/encoding/research/poc_entropy_test.go`.

## Evidence

### Why the production figure points here

Under the production encoder settings, full-precision data costs about 6.4 B/point with Chimp,
so the 3.3 B/point production average means the data is far smoother than the synthetic gauges:
either many repeated values, or few decimals with small steps.
Whole-blob bytes/point at 100 × 150 with the exact production options:

| data | chimp | gorilla | alp |
|---|---|---|---|
| 2-decimal gauge (measurev2 `decimal_gauge_2dp`) | 6.22 | 6.58 | 1.73 |
| constant-dominated (`sparse_constant`) | **0.74** | **0.64** | 1.24 |
| full precision (`worst_case`) | 6.38 | 6.60 | 6.51 |
| 2-decimal gauge, 50% of points hold the previous value | 3.35 | 3.43 | 1.67 |
| 1-decimal gauge, ±0.03% steps | 3.99 | 4.06 | 0.81 |

Plain ALP already wins on every decimal shape except the constant-dominated one,
where it is 1.7–1.9× larger than Chimp and Gorilla.
Production data with many repeats would hit exactly that case.

### Ratio gate (passed)

Gate, stated before measuring (`TestPOCRLEALPVariants`):
with per-column selection, the new encoding must never be larger than plain ALP beyond its selector,
and must beat Chimp on every production-calibrated shape.

Layout comparison (`TestPOCRLEALPVariants`, a bit-level cost model used only to choose bitmap over lengths).
Values only, bytes/point, n = 150, 100 columns; ALP sizes come from the production encoder:

| shape | chimp | gorilla | alp | ALP+RLE (lengths) | ALP+RLE (bitmap) | runs / points |
|---|---|---|---|---|---|---|
| decimal_gauge_2dp | 6.08 | 6.46 | 1.59 | 1.59 | 1.59 | 1.00 |
| counter | 1.93 | 1.58 | 1.36 | 1.36 | 1.36 | 1.00 |
| sparse_constant | 0.58 | 0.48 | 1.12 | **0.22** | 0.30 | 0.05 |
| worst_case | 6.25 | 6.45 | 6.35 | 6.35 | 6.35 | 1.00 |
| 2dp, hold 30% | 4.35 | 4.51 | 1.56 | 1.36 | **1.25** | 0.69 |
| 2dp, hold 50% | 3.17 | 3.24 | 1.53 | 1.03 | **0.95** | 0.49 |
| 2dp, hold 70% | 2.09 | 2.08 | 1.47 | 0.71 | **0.67** | 0.31 |
| 2dp, ±0.005% steps | 4.27 | 4.32 | 0.77 | 0.76 | **0.74** | 0.76 |
| 1dp, ±0.03% steps | 3.86 | 3.94 | 0.68 | 0.66 | **0.63** | 0.64 |
| 1dp, ±0.01% steps | 1.95 | 1.90 | 0.49 | 0.40 | **0.38** | 0.30 |

The two RLE columns include per-column selection against plain ALP (+2 bits).
That model undercharges the real layout by 3 bytes per runs column and ignores encoder pruning,
so the gate is decided by the exact measurement below, not by this table.

Exact measurement (`TestPOCRLEALPExact`): real scheme-3 bytes (`5 + ceil(count/8) + nested column`),
real production ALP columns, selection by exact byte count, with and without the encoder's pruning rule:

| shape | chimp | alp | ALP-RLE, always try | ALP-RLE, pruned | pruning loss | vs Chimp | columns tried |
|---|---|---|---|---|---|---|---|
| decimal_gauge_2dp | 6.08 | 1.59 | 1.59 | 1.59 | 0.00% | −73.8% | 0/100 |
| counter | 1.93 | 1.36 | 1.36 | 1.36 | 0.00% | −29.4% | 0/100 |
| sparse_constant | 0.58 | 1.12 | 0.32 | 0.32 | 0.00% | −44.2% | 100/100 |
| bursty_scrape | 6.07 | 1.60 | 1.60 | 1.60 | +0.31% | −73.6% | 0/100 |
| worst_case | 6.25 | 6.35 | 6.35 | 6.35 | 0.00% | +1.5% | 0/100 |
| 2dp, hold 30% | 4.35 | 1.56 | 1.27 | 1.27 | 0.00% | −70.7% | 100/100 |
| 2dp, hold 50% | 3.17 | 1.53 | 0.97 | 0.97 | 0.00% | −69.5% | 100/100 |
| 2dp, hold 70% | 2.09 | 1.47 | 0.69 | 0.69 | 0.00% | −67.0% | 100/100 |
| 2dp, ±0.005% steps | 4.27 | 0.77 | 0.75 | 0.75 | +0.40% | −82.4% | 29/100 |
| 1dp, ±0.03% steps | 3.86 | 0.68 | 0.64 | 0.64 | 0.00% | −83.3% | 55/100 |
| 1dp, ±0.01% steps | 1.95 | 0.49 | 0.39 | 0.40 | +0.30% | −79.8% | 91/100 |

The gate passes: every calibrated shape beats Chimp by 67–83%, and no column is larger than plain ALP.
`worst_case` (full precision, no repeats) stays at plain ALP, 1.5% above Chimp; it is not a calibrated shape.
On the hold-50% shape, ALP-RLE is 37% smaller than plain ALP and 70% smaller than Chimp.
With the measured ~0.18 B/point of blob overhead added,
the **estimated** whole-blob figure is about 1.15 B/point against Chimp's 3.35.
That is an estimate on calibrated synthetic data, not a production measurement.

Measured in Phase 3 through the real blob encoder (whole blob, bytes/point, 100 metrics × 150 points,
shared DeltaPacked timestamps, no compression, no tags; `tests/measurev2` profiles):

| profile | chimp | gorilla | alp | alp-rle | vs ALP | vs Chimp |
|---|---|---|---|---|---|---|
| decimal_gauge_2dp | 6.22 | 6.58 | 1.73 | 1.73 | +0.0% | −72.1% |
| counter | 2.07 | 1.72 | 1.50 | 1.50 | +0.0% | −27.6% |
| sparse_constant | 0.74 | 0.64 | 1.24 | 0.46 | −62.9% | −37.7% |
| worst_case | 6.38 | 6.60 | 6.51 | 6.51 | +0.0% | +2.0% |
| cal_2dp_hold30 | 4.52 | 4.69 | 1.73 | 1.44 | −16.9% | −68.2% |
| cal_2dp_hold50 | 3.38 | 3.44 | 1.68 | 1.12 | −33.1% | −66.8% |
| cal_2dp_hold70 | 2.18 | 2.17 | 1.62 | 0.82 | −49.6% | −62.6% |
| cal_2dp_step0.005 | 4.42 | 4.47 | 0.95 | 0.90 | −4.7% | −79.6% |
| cal_1dp_step0.03 | 4.01 | 4.08 | 0.83 | 0.79 | −4.8% | −80.3% |
| cal_1dp_step0.01 | 2.07 | 2.01 | 0.63 | 0.53 | −15.7% | −74.2% |

The hold-50% estimate holds: 1.12 B/point against Chimp's 3.38.
No profile is larger than plain ALP, and `sparse_constant`, the one shape where ALP lost to Chimp and Gorilla, now beats both.

### Speed gates

Final measurement is layout-averaged (4 layouts, n = 12) in Phase 3, against plain ALP on the same columns:

- `DecodeAll` on a runs column: faster than Chimp, the codec production uses today.
  (Originally "at most 1.2× plain ALP"; restated by the owner on 2026-10-03 after the pre-check below,
  because the two-pass decode cannot reach 1.2× without a fused kernel and still beats Chimp by about 3×.)
- `ValueAt` on a runs column at 150 points: at most 2× plain ALP.
- Encode on run-free data (the column ends up plain): at most 1.1× plain ALP.

If a speed gate fails, the codec stays in-tree, validated, but unwired, as BP128 did.

Pre-check (2026-10-03, single binary, 6 runs, Ryzen 9 9950X3D; `BenchmarkPOCRLEDecode`, `BenchmarkPOCRLEEncodeRunFree`),
100 columns × 150 points, hold-50% shape for decode, run-free 2-decimal gauges for encode:

| measurement | plain ALP | runs layout | ratio | gate |
|---|---|---|---|---|
| `DecodeAll`, ns/point | 0.54 | 0.84–0.87 | 1.55–1.6× | faster than Chimp (2.58) — passes |
| `At`, ns/lookup | 8.4 | 9.5 | 1.13× | ≤ 2× — passes |
| encode, run-free columns | 795 µs | 817 µs | 1.03× | ≤ 1.1× — passes (with the pruning rule below) |
| reference: Chimp `DecodeAll`, ns/point | 2.58 | | | |

The runs layout decodes in two passes (nested ALP, then expansion at about 0.30 ns/point),
so it cannot reach 1.2× of a single 0.54 ns/point pass without a fused kernel,
but it is about 3× faster than Chimp.
Single-binary results can be off by 20–40% from code placement, so these numbers only show direction.
The decode benchmark also uses prebuilt `[]uint64` bitmaps and preallocated scratch,
and the encode benchmark builds fresh ALP columns instead of reusing the blob encoder;
header parsing, bitmap-tail handling and scratch acquisition are only measured in Phase 3.
A fused decode kernel is out of scope for this design; it can follow if plain-ALP parity ever matters.

Phase 3 result (2026-10-04): layout-averaged, 4 code layouts × 3 rounds, medians of n = 12,
`taskset -c 6`, Ryzen 9 9950X3D, Go 1.26.7;
`internal/encoding/value/alp/alp_runs_bench_test.go` and `blob/numeric_alp_bench_test.go`,
100 metrics × 150 points, production blob options for the blob-level rows:

| gate | judged at | measured | result |
|---|---|---|---|
| `DecodeAll` faster than Chimp | codec `DecodeAll` | runs 0.90 ns/pt, Chimp 2.70, plain ALP 0.56 | passes, 3.0× faster than Chimp |
| `ValueAt` ≤ 2× plain ALP | `NumericBlob.ValueAt` | ALP-RLE 32.1 ns, ALP 28.3, Chimp 428 | passes, 1.14× |
| run-free encode ≤ 1.1× plain ALP | codec and whole-blob encode | 1.009× and 1.008× | passes |

At the codec level alone, runs `At` is 13.2 ns against 7.3 for plain ALP (1.81×), also within the gate.
Two results outside the gates:
`ForEachValues` on a runs column is 6.36 ns/pt against Chimp's 4.73, and allocates per metric under both ALP types;
encoding a hold-50% column costs 2.03× plain ALP, because the (e, f) search dominates at 150 points
and the nested column pays it again.
Working report: `tmp/alp-rle-phase3-measurement.md` (gitignored).

Both were addressed after Phase 3, measured the same layout-averaged way:

- `ForEachValues` on ALP and ALP-RLE columns now decodes each column with `DecodeAll` into a pooled buffer
  (`1737133`, `29ae0b8`).
  On the hold-50% shape, ALP-RLE went from 6.4 to about 3.2 ns/pt and plain ALP from 4.0 to about 3.0,
  against Chimp's 4.8, with no allocations per metric (300 per 100 metrics before).
  Columns over 8,192 points still use the per-point iterator, so the pooled buffers stay bounded.
- The nested column's (e, f) search is seeded with the plain column's (e, f) (`3030e3b`, see §Encoder).
  Whole-blob encode of the hold-50% shape went from 1,579 to 1,188 µs, 1.53× plain ALP's 776 µs;
  run-free encode is unchanged at about 1.01×.
  Rerunning the whole-blob table above, only `cal_2dp_hold30` changes at two decimals (1.44 to 1.43 B/point),
  and a few percentages move by 0.1 point;
  the current table is in `docs/performance.md` ("ALP-RLE on repeat-heavy data").

## Key decisions

- **A new encoding byte, not a fourth ALP scheme.**
  `internal/encoding/value/alp/alp.go:38-52` declares the 0x6 scheme set closed.
  Worse, the codec's `All`, `DecodeAll` and `At` fall through silently on an unknown scheme
  (`alp.go:949-963`, `:1302-1303`, `:1324-1325`).
  A v1.9.0+ reader would reject a new 0x6 scheme only because `validateALPColumns` runs when the blob is opened;
  v1.8.0 has no open-time ALP validation and would mishandle it silently or panic (see Phase 0).
  Either way, operators would hit that break without changing any configuration.
  A new byte makes the new layout opt-in, and old readers reject it with `ErrInvalidHeaderFlags`.
  The value-encoding field is 4 bits (`section/numeric_flag.go:181-189`), and 0x7 is unassigned.
- **The 0x7 column format is a superset of 0x6.**
  Scheme bytes 0, 1 and 2 mean exactly what they mean under 0x6, byte for byte; scheme 3 is the runs layout.
  Because the encoder always builds the exact plain column and keeps a runs column only when it is smaller in full bytes,
  a 0x7 column is never larger than the 0x6 column for the same values.
  That guarantee is per uncompressed column.
  With zstd, S2 or LZ4 value compression,
  the compressor runs over the whole value payload (`blob/numeric_encoder.go:825`),
  and a smaller input is not guaranteed to compress smaller.
- **Bitmap, not run lengths.**
  The bitmap is smaller on every production-calibrated shape (by 0.03–0.11 B/point)
  and keeps `ValueAt` a rank query instead of a search.
  It loses to lengths only on `sparse_constant` (0.30 against 0.22 in the bit-level model),
  where it still beats Chimp (0.58) and Gorilla (0.48).
  One layout, not two.
- **Run equality is bitwise** (`math.Float64bits`).
  This keeps −0.0 distinct from +0.0 and keeps NaN payloads, matching ALP's lossless guarantee.
- **No nesting.**
  The run values are stored as a plain ALP column (scheme 0, 1 or 2), never as another runs column.

## Column layout (scheme 3)

```
[scheme=3 : 1 byte]
[nRuns    : uint32, 4 bytes, blob byte order]
[bitmap   : ceil(count/8) bytes, LSB-first, little-endian regardless of blob byte order (like alpPackBits)]
[nested   : an ALP column (scheme 0, 1 or 2) holding nRuns values]
```

- Bit i of the bitmap is 1 when point i starts a new run, so bit 0 is always 1, and bits beyond `count` are 0.
- The nested column is a complete ALP column, laid out exactly as under 0x6, with `count = nRuns`.
  Its exception positions index runs, not points.
- Fixed overhead over the nested column: 5 bytes plus the byte-rounded bitmap, 24 bytes in total at 150 points.
- Scheme 3 is never written for `count == 0` (the existing empty-column handling is unchanged)
  or for `count == 1`, where it is always larger than plain ALP.

## Encoder

- One extra pass counts runs (bitwise comparison with the previous value).
- The plain column is always encoded first.
- The run values are encoded only when the repeats could pay for the runs overhead.
  For each repeated point, the plain column's cost of that point is its packed bits plus its exception entry, if any
  (main: `width`, +96 for an exception; RD: `rbw + codeBits`, +48 for an exception; raw: 64).
  The runs layout is tried only when the sum over repeated points exceeds `8 × (5 + ceil(count/8))` bits.
- This is a deliberately lossy heuristic:
  it ignores second-order effects such as the nested column choosing a different (e, f).
  `TestPOCRLEALPExact` measures its loss against always trying at 0–0.4% per shape,
  and fails if any shape loses more than 1%, any column grows past plain ALP,
  or a calibrated shape stops beating Chimp.
  An earlier rule that used the average bits per point skipped repeated exceptions,
  which cost far more than the average; the review's counterexample (plain 83 bytes, runs 71 bytes) is now tried.
  Without pruning, columns with a handful of repeats paid a second encode for nothing (1.29× plain in the pre-check).
- The nested column's (e, f) search is seeded with the plain column's (e, f), which usually prunes most other candidates early.
  It returns the same (e, f) as an unseeded search unless the seed ties the minimum estimate, and then keeps the seed;
  a differential test pins this.
  Measured 2026-10-04: encoding shapes with runs got 10–32% faster, run-free and special-value shapes were unchanged,
  and 1,794 of 1,800 test columns were byte-identical, 6 smaller and none larger.
- When both exist, the encoder keeps the smaller one in exact bytes; ties go to plain.
- The choice is per column, like ALP's existing main/RD/raw choice (`alp.go:242-292`).

## Decoder

- `DecodeAll` keeps the existing contract (`alp.go:1272`):
  it writes `min(count, len(dst))` values, returns that number, and still parses the column with the full `count`.
  For `n = min(count, len(dst))` it decodes the first `r = rank(n-1)` run values into `dst[n-r:n]`,
  then expands forward in place without branches, `idx += bit(i); dst[i] = runs[idx]`, stopping at the destination length.
  The expansion never overwrites a run value before reading it, so it needs no scratch (`decodeRunsInto`).
  Phase 1 tests must cover empty, short and oversized destinations, including one that ends inside a bitmap word;
  the PoC's expansion helpers already pass such a test (`TestPOCRLEExpandShortDst`).
  Processing one bitmap word at a time with bounds checks hoisted measured 0.30 ns/point,
  against 0.44 for the per-point loop and 0.33–0.49 for a run-fill loop.
- `All`: the same expansion, yielding per point.
- `At(i)`: out-of-range indexes keep the existing guards and return `(0, false)`;
  otherwise rank = popcount of bitmap bits 0..i, then nested `At(rank-1)`.
  This is O(i/64) popcounts with no stored rank directory: at most 3 at 150 points, about 160 at 10,000.
  A rank directory is reconsidered only if a column of 10,000 points or more fails the `ValueAt` gate.

## Validation

The codec fails silently on malformed input, so validation at blob open is mandatory.
`validateALPColumns` (`blob/numeric_decoder.go:599-750`) is extended,
and the open-time gate at `blob/numeric_decoder.go:217-221` becomes `TypeALP || TypeALPRLE`.

- Scheme 3 is accepted only under `TypeALPRLE`; under `TypeALP` it remains `ErrInvalidALPScheme`.
- `1 ≤ nRuns ≤ count`, and the bitmap is fully present (`ceil(count/8)` bytes).
- Bit 0 is set, padding bits are 0, and `popcount(bitmap) == nRuns`.
- The nested scheme is 0, 1 or 2, and the nested column passes the existing checks with `count = nRuns`,
  including the platform bound `maxALPColumnBits` (`blob/numeric_decoder.go:555-559`).
  The nested decoders rely on that bound to keep `count × width` representable on 32-bit, exactly as for 0x6 columns.
- Failures return `ErrInvalidALPColumn`; no input may panic.
- Implementation order: check the fixed 5-byte header before reading `nRuns`,
  require a nested scheme byte to be present,
  compute every length with overflow-safe arithmetic before slicing or allocating,
  and handle the short final bitmap word without reading nested-column bytes.

## Phases

### Phase 0 — compat harness covers ALP (prerequisite, done 2026-10-03)

`tests/compat` had no ALP scenarios, so even 0x6 was not cross-version tested.
Added:

- `scenarios_alp.go` (build tag `alp`, applied to v1.8.0+): 17 `alp-*` scenarios.
  They cover the main, RD and raw schemes, main and RD exceptions, and special values (±0, ±Inf, NaN payload, subnormals);
  both byte orders, V1/V2/shared-timestamp layouts, the extended 32-byte V2 index, tags, zstd and a 1,500-point column.
- Capability buckets in `run_compat.sh` (`CAPABILITY_BUCKETS`):
  a bucket is dropped from Matrix 2 when OLD lacks its tag,
  and the new Matrix 3b requires OLD to decode it (tag present) or reject it gracefully (tag absent).
- Two Graceful ALP corruption fixtures seeded from `alp-v2-mixed`.
  v1.8.0 panics on one of them (it has no open-time ALP validation; v1.9.0 added it).
  The panic is tolerated only on that fixture and only for binaries without the `alpvalidate` tag (v1.9.0+).
- Each run now clears its own output directories, so fixtures from an earlier version pair are not re-checked.
- A bucket that NEW supports must be non-empty with a blob for every manifest, and copy errors fail the run,
  so Matrix 3b cannot pass without testing anything.
- Refs containing `/` (branch names) are turned into safe directory names.

Verified passing: v1.4.3, v1.7.1, v1.8.0, v1.9.0 and v1.10.0, each against v1.11.0.

For 0x7, Phase 2 adds one tag gate and one bucket entry (`"alprle:alprle-:needs_alprle_tag"`),
plus a tag-gated `scenarios_alprle.go`.

### Phase 1 — codec

In `internal/encoding/value/alp/`:

- The encoder takes an option that allows scheme 3; `TypeALP` never sets it.
- The decoder handles scheme 3 in `All`, `DecodeAll` and `At`.
- Unit tests: round trip including −0.0, NaN payloads and all-equal columns; exception positions inside the nested column;
  both byte orders; xxhash golden outputs for the new scheme next to the existing ones (`alp_test.go:1436-1526`).
- Validation tests mirroring `blob/numeric_alp_validate_test.go` for every rule above.

### Phase 2 — wiring

Each item is a `switch` site or allow-list today; missing one shows up as a silent default branch.

1. `format/types.go`: `TypeALPRLE EncodingType = 0x7`, and its `String()`.
2. `section/numeric_flag.go:39-44`: add it to the value allow-list.
3. `blob/numeric_encoder_config.go:108-127`: accept it in `setValueEncoding`.
4. `blob/numeric_encoder.go:230-244`: constructor switch;
   also the encoder hooks at `:238`, `:496-497`, `:557`, `:788`, `:1252`, `:1305`.
5. `blob/numeric_blob.go`: the decode switches whose default branches are at `:609`, `:1253` and `:1320`,
   and the ALP bulk-decode dispatch in `All()` at `:647`;
   without it, `All()` stays correct but falls back to per-point `iter.Pull`.
6. `blob/numeric_blob_foreach.go`: the `ForEach` and `ForEachValues` dispatch.
7. `blob/numeric_decoder.go:217-221`: open-time validation for the new type.
8. `internal/encoding/facade.go`: constants and constructors next to the ALP ones (`:23-36`, `:221-228`).
9. `errs/errors.go`: reuse `ErrInvalidALPScheme` and `ErrInvalidALPColumn`; no new errors expected.
10. `mebo.go`, `blob/doc.go`, `encoding/doc.go`: public docs and option lists.
11. `tests/measurev2/types.go`: add the type to both combo grids.

Tests: extend `blob/numeric_alp_wiring_test.go`
so `AllValues`, `ValueAt`, `ForEach` and `Materialize` agree under the new type,
and `blob/numeric_alp_scheme_test.go` so scheme 3 is rejected under 0x6 and accepted under 0x7.

### Phase 3 — measurement (done 2026-10-04)

- Generators first:
  add the calibrated shapes (hold 30/50/70%, 1-decimal small steps) to `tests/measurev2` as named profiles.
- Run the three speed gates above, layout-averaged.
- Rerun the ratio table through the real blob encoder at 100 × 150 with the production options.

### Phase 4 — docs

User docs say when to pick `TypeALPRLE` over `TypeALP`:
each uncompressed column is never larger, and encoding costs one extra pass
(about 1% on run-free data, about 1.5× plain ALP when half the points repeat, after the seeded search).
They also say that with value compression enabled the whole payload is not guaranteed to shrink,
and that readers older than this encoding reject the blob.
Add the performance numbers from Phase 3.

Done 2026-10-04: `README.md`, `docs/design.md`, `docs/best_practices.md`,
and `docs/performance.md` (regenerated, with ALP-RLE in every table and an ALP-RLE subsection under "Codec Selection by Data Shape");
the `update-performance-report` skill now knows ALP-RLE and the O(1) shared-timestamp `TimestampAt`.

## Rejected alternatives

- **A fourth scheme under 0x6**: the scheme set is closed (`alp.go:38-52`),
  and old readers would break without any opt-in.
- **Run lengths instead of a bitmap**: larger on every production-calibrated shape, and `ValueAt` would need a search.
- **Dictionary in front of ALP**: larger than plain ALP on every shape at n = 150
  (for example 2.66 against 1.69 B/point on the 2-decimal gauge).
- **Delta of ALP integers with anchors**: −14.7% against the best codec at n = 150 with anchors every 32 points,
  just under the 15% bar on synthetic data; deferred until real data is available.
- **zstd on value payloads**: the block-layer PoC only helped with a trained dictionary,
  which needs an owner for the dictionary.

## Out of scope

Entropy coding of residuals, SIMD decode kernels, FastLanes-style layouts,
delta-coded ALP integers, and timestamp codecs.
