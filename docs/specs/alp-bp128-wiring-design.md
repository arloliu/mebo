# Design: Wire ALP (value codec); BP128 timestamp spike — deferred

**Date:** 2026-06-14
**Status:** ALP wiring + measurev2 refactor — design approved, pending final review.
BP128 timestamp codec — spike built and measured, **deferred** (evidence below).

## Goal

Land the **ALP** value codec into the mebo blob format as a first-class, user-selectable
encoding, and refactor the `tests/measurev2` harness so codecs are validated against data
that resembles real production time-series. A parallel **BP128** timestamp-codec spike was
run to settle whether it earns an on-disk byte; the answer (below) is **not yet**.

- **ALP** (Adaptive Lossless floating-Point) — a value codec alongside Gorilla/Chimp. The
  production codec already exists at `internal/encoding/numeric_alp.go` (encoder + decoder,
  both-endian, `At`/`All`, lossless including negative zero). **Measured: ~81% smaller blobs
  than Chimp on realistic 2-dp decimal data → ~5× faster retrieval on I/O-bound reads.** This
  is the primary deliverable.
- **BP128** (SIMD-BP128 fixed-width FOR + bit-pack + PFOR) — a candidate timestamp codec.
  Fully built and measured during this effort (scalar reference + first-delta extraction +
  PFOR + hand-written AVX-512 Plan9 asm, all in `internal/encoding/ts_bp128*.{go,s}`,
  bit-exact and lint-clean). **Deferred** — see "Spike outcome".

Background research and measurements: `docs/perf/codec_research_findings.md`.

## Spike outcome (2026-06-14) — why BP128 is deferred

The original gate was "BP128 must beat Delta". The honest, like-for-like measurement on this
machine (AMD Ryzen 9 9950X3D, AVX-512, default build) showed:

- **Decode is PAR with Delta**, not faster. The prototype's "~3× faster decode" was a
  measurement artifact — it compared BP128's batch decode against Delta's slow per-point
  `.All()` iterator (2571 ns/1000 pts) instead of Delta's fast `DecodeAll` batch (775 ns).
  Fairly measured, BP128 ≈ Delta (835 vs 775 ns @1k; 34.9 vs 35.8 µs @40k). The end-to-end
  bottleneck is the inherently-sequential delta-of-delta **prefix-sum**, identical for both
  codecs — so vectorizing the bit-unpack (BP128's whole advantage) does not move the total.
- **Encode is ~2× slower** than Delta (histogram + width search + PFOR + pack vs varint).
- **Ratio**: BP128 wins ~20% only for columns ≥256 points (clean *and* bursty, thanks to
  PFOR); it is par at the common ~200, and *worse* below ~150.

**I/O-aware re-evaluation (the right lens):** for I/O-bound retrieval, smaller blob = faster
read, and decode is par, so a ratio win with no CPU penalty is a net win. Quantified on a
realistic 100-metric blob (codec-level payload sums):

| | values | shared-TS blob | per-metric-TS blob |
|---|---|---|---|
| ALP vs Chimp (values) | **−81% / −82%** | — | — |
| BP128 vs Delta total, P=200 | — | −81.0% → −81.0% (TS = 0.35% of blob) | −60.3% → −59.8% (BP128 *worse*) |
| BP128 vs Delta total, P=1000 | — | −81.8% → −81.9% (negligible) | −61.1% → −66.2% (+5 pts) |

ALP dominates the blob in every layout (~5× retrieval win). BP128's contribution is a
rounding error with shared timestamps (mebo's common mode, TS ≈ 0.35% of the blob), helps
~5% only with **per-metric timestamps AND large columns (≥256)**, and *hurts* small
per-metric columns. That does not justify a permanent on-disk byte plus hand-maintained
AVX-512 assembly. **BP128 stays in-tree (validated, bit-exact, lint-clean, no byte); revisit
only if a per-metric-large-column workload appears.**

## Key constraints and decisions

- **Lossless only.** Round-trip bit-exact, both endians, including −0.0 / Inf.
- **On-disk encoding-type bytes are permanent.** A new `format.EncodingType` byte is a
  forever commitment — a codec must *earn* it with measured evidence. This principle is what
  deferred BP128: it did not earn one. (`TypeALP = 0x6`; `0x7` left unassigned.)
- **ALP selection = explicit opt-in only.** Callers select ALP exactly like Gorilla/Chimp
  (`WithValueEncoding(format.TypeALP)` / a `WithALP()` helper). No adaptive auto-selection
  and no default change in this effort — those are possible follow-ons.
- **measurev2 catalog = broad.** A named profile catalog covering multiple decimal
  precisions, counters, sparse/constant gauges, several scrape rates, and a bursty/gappy
  timestamp profile. The current full-precision random walk is retained but relabeled as
  `worst_case` and is no longer the default. It now also showcases ALP and keeps the
  large-column / per-metric profiles needed to revisit BP128 later.
- **BP128 and Simple8b both stay in-tree as validated reference codecs, with no on-disk
  byte.** Timestamps remain Delta/DeltaPacked in this effort.

## Architecture / sequencing

Generators-first, then ALP. The decimal profiles are how ALP's win is demonstrated through
the real blob API; building the realistic generators first gives a yardstick before wiring.

```
Phase 1: measurev2 broad-catalog generators        (no wiring; new realistic data)
   |
Phase 2: wire ALP (value codec) -> TypeALP = 0x6   (the deliverable; validate on decimal profiles)
   |
Phase 3: BP128 timestamp spike                      (DONE → deferred; see Spike outcome)
```

Rejected alternative:
- **Wire-first** (land codecs, then make data realistic): faster to "codec in tree," but
  measuring on unrealistic data would have hidden ALP's real win and BP128's real weakness.

## Phase 1 — measurev2 broad-catalog generators

Refactor `tests/measurev2/generator.go` and `tests/measurev2/types.go` into a named profile
catalog. Each profile declares its value generator, timestamp generator, precision, and
counts. Existing behavior (full-precision random walk) is preserved as `worst_case`.

**Value generators:**
- `decimal_gauge` at 1, 2, 3, 4 decimal places — bounded range, small per-step moves.
  ALP's main scheme should dominate here.
- `counter` — monotonically increasing, integer-valued float64 (e.g. `*_total` metrics).
- `sparse_constant` — gauge that rarely changes (long runs of identical values).
- `worst_case` — the current full-precision unbounded random walk, relabeled.

**Timestamp generators:**
- `regular_scrape` at 10s / 15s / 30s / 60s with sub-millisecond jitter — replaces the
  unrealistic 1s ± 0.1% default and matches real scrape schedules.
- `bursty_scrape` — fixed interval punctuated by gaps/restarts that produce large
  delta-of-delta spikes. **Reused verbatim as the BP128 ratio-on-bursty gate (Phase 3).**

**Realism fixes:**
- Quantize generated values to the profile's decimal precision (the single biggest realism
  gap today: full-precision random float64 is the worst case for every value codec).
- Default to shared/synchronized timestamps (one scrape schedule for all metrics).
- Per-profile metric and point counts; keep the scaling sweep.

At the end of Phase 1 the harness still exercises only the currently-wired codecs
(Raw/Gorilla/Chimp values, Raw/Delta/DeltaPacked timestamps). This immediately shows how the
existing codecs behave on realistic data and produces the `bursty_scrape` dataset the BP128
gate needs.

## Phase 2 — Wire ALP (value codec)

The codec exists, but one **correctness prerequisite** must land before wiring, then the
plumbing.

### Phase 2.0 — Prerequisite: widen ALP exception fields (correctness blocker)

ALP currently encodes the exception count (`nExc`) and each exception position (`pos`) as
`uint16` (`internal/encoding/numeric_alp.go:317,321` for main; the same 2-byte `pos`/`nExc`
in the ALP-RD layout). But a metric column may hold up to `section.NumericMaxCount =
math.MaxUint32` points (`section/const.go:63`). A column with >65535 points and an exception
at position ≥65536 — or with >65535 exceptions — silently truncates to a wrong position/count
and corrupts the column.

Because ALP is **unwired**, its on-disk layout is not frozen, so the clean fix is to **widen
`nExc` and exception `pos` to `uint32`** in both the main and RD layouts now (cost: a few
extra bytes only when exceptions exist — negligible). Add a round-trip test with >65535
points including an exception past position 65535 to lock it. Update the layout comment block
(`numeric_alp.go:27-35`). (Native ALP 1024-value sub-vector segmentation would also bound
positions to 16 bits and improve ratio on large columns — that remains a separate ratio
follow-on; the `uint32` widen is the minimal correctness fix.)

### Phase 2.1 — Wiring (plumbing)

1. **`format/types.go`** — add `TypeALP EncodingType = 0x6` and its `String()` case.
2. **`blob/numeric_encoder_config.go` / `blob/numeric_encoder.go`** — add the
   `WithValueEncoding(format.TypeALP)` path and a `WithALP()` helper; construct
   `encoding.NewNumericALPEncoder(...)` in the value-encoder factory switch.
3. **Decoder dispatch — there are FOUR paths that switch on the value-encoding byte, and a
   new codec must be added to every one or it silently falls through (returns 0 / (0,false)):**
   - `blob/numeric_decoder.go` — the decoder-factory / `DecodeAll` path (~`:434-437`).
   - `blob/numeric_blob.go` `decodeValuesSlice` materialize path (~`:1248-1271`, default
     returns `0` for unknown codecs) — used by `blob/numeric_blob_material.go:332-339`.
   - `blob/numeric_blob.go` value random-access `At` (~`:582-619`, default returns `(0,false)`)
     — ALP already implements `At` at `numeric_alp.go:621-636`, so this just needs the case.
   - `blob/numeric_blob_foreach.go` `forEachDataPoint` callback path (~`:105-161`).
   Add the `encoding.NewNumericALPDecoder(...)` case to each, and add `ForEach`/`ForEachByName`
   and materialized-slice parity tests so a missed path fails loudly.
4. **`section/numeric_flag.go` and `section/numeric_header.go`** — add `TypeALP` to the
   value-encoding **validation allow-lists**. These reject unknown bytes and are the
   easy-to-miss breakage point.
5. **Lifecycle / flush (explicit edit, not just a check).** The encoder flushes only
   Gorilla/Chimp values and DeltaPacked timestamps before computing `Size()`
   (`blob/numeric_encoder.go:415-429`). ALP emits its bytes only from `Bytes()`
   (`numeric_alp.go:122-137`) and **drops pending values on `Reset()`** (`:455-459`). So ALP
   must be **added to that flush switch** and flushed *before* size/offset accounting and
   before any `Reset()`, or pending data is silently dropped.
6. **Validation** — run the Phase-1 decimal profiles through the blob API and confirm the
   2–5× ratio over Chimp materializes end-to-end (not only in the internal unit tests).

## Phase 3 — BP128 timestamp spike (completed → deferred)

This phase was executed as a measurement spike rather than a wiring step, and concluded that
BP128 should **not** be wired now (see "Spike outcome" above for the numbers). What was built
and remains in-tree, fully validated, for a future revisit:

- `internal/encoding/ts_bp128.go` — scalar reference: vertical 8-lane pack/unpack kernels
  (`bp128PackBlockScalar` / `bp128UnpackBlockScalar`), the dispatch seam
  (`bp128PackBlock` / `bp128UnpackBlock`), and a reusable `bp128Codec` implementing the full
  pipeline: first-delta extraction (FOR base) + zigzag(delta-of-delta) + per-block PFOR
  exceptions, with the SIMD kernel kept pure fixed-width (outliers are masked out before
  packing and overwritten after unpacking, so PFOR never complicates the kernel).
- `internal/encoding/ts_bp128_simd_amd64.{s,go}` — hand-written AVX-512 Plan9 asm
  (`VMOVDQU64` / `VPSLLQ` / `VPSRLQ` / `VPANDQ` / `VPORQ` / `VPBROADCASTQ`) for the pack/unpack
  kernels, default-build-safe (no `GOEXPERIMENT`), CPU-gated via `arch.X86HasAVX512()` with
  scalar fallback (`ts_bp128_amd64.go` / `ts_bp128_noasm.go`).
- Tests: round-trip across all widths 0–64, both endians, PFOR exception coverage, bursty
  gaps, and a differential test proving the asm is **bit-exact with the scalar oracle for
  all 64 widths**. `internal/encoding/ts_bp128_bench_test.go` holds the BP128-vs-Delta
  benchmark over `{32,127,256,257,1000,40192}` × clean/bursty.

**Why deferred (not wired):** measured fairly (vs Delta's fast `DecodeAll`, not its slow
iterator), BP128 decode is par, encode is ~2× slower, and the ratio win (~20%, n≥256) is
negligible-to-the-blob with shared timestamps and only meaningful for per-metric large
columns. A permanent on-disk byte + AVX-512 maintenance is not justified by that. The lesson
— **always benchmark against the fast batch path, never the per-point iterator** — is
recorded here and in `docs/perf/codec_research_findings.md`.

**If revisited later**, the one untested lever that could let BP128 beat Delta on decode is a
SIMD 2-level prefix-sum fed by BP128's parallel unpack (Delta's serial varint can't feed
it). The wiring, if ever done, would follow the same checklist as ALP (Phase 2.1): the byte,
the encoder factory, **all four** decoder dispatch paths, the section allow-lists, and the
flush hook (BP128 buffers like ALP; `bp128Codec` flushes on `Bytes()`).

## Cross-cutting

- **Format compatibility.** The new `TypeALP = 0x6` byte is purely additive. A blob written
  with ALP cannot be read by older mebo versions (forward-incompatible by design); older blobs
  are unaffected. Document in `CHANGELOG.md`. No magic-number / format-version bump unless a
  hard gate is preferred.
- **Testing (ALP).** Round-trip, both endians, `At`/`All` parity, multi-segment reset.
  **Dispatch-parity tests are mandatory** — assert `All`, `At`, materialized-slice, and
  `ForEach`/`ForEachByName` all return identical data, so a missed dispatch path (the silent
  fall-through risk) fails loudly. Include the ALP >65535-point/exception test from Phase 2.0.
  Update any golden/compat tests that enumerate the valid `EncodingType` set. measurev2 carries
  ratio assertions on the decimal profiles.
- **Conventions.** Run the linter and fix issues before every commit. No attribution trailers
  in commit messages.

## Out of scope (possible follow-ons)

- **Wiring BP128** — built and validated this effort, deferred (see Phase 3 / Spike outcome).
  Revisit for per-metric large-column workloads; the one decode lever left is a SIMD 2-level
  prefix-sum fed by BP128's parallel unpack.
- Adaptive auto-selection of value codec (sample-then-choose ALP/Chimp/Gorilla per column).
- zstd shared-dictionary block codec (separate BlobSet-level design; dictionary ownership is
  the open question).
- ALP native sub-vector segmentation at 1024 values (v1 treats the whole column as one vector).
  Note the correctness fix for large columns (widening exception fields to `uint32`) is
  **in scope** as Phase 2.0; segmentation here is purely the later ratio optimization.
