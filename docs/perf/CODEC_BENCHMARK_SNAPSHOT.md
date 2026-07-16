# Codec Benchmark Snapshot

End-to-end encoding-matrix measurement across realistic data profiles, produced by
[`tests/measurev2`](../../tests/measurev2). This snapshot reflects the current state of the
ALP encode and decode optimizations (the streaming decode reader, 5–14×, plus the encode
prune + digit-cache + RD map-reuse), and exists to motivate per-column adaptive value-codec
selection: **no single value codec wins across data shapes.**

## Provenance

- **Date:** 2026-06-15
- **Go:** go1.26.1  •  **Platform:** linux/amd64  •  **CPU cores:** 32
- **Data size:** 200 metrics × 200 points = 40,000 points/blob
- **Generator:** seed 42, value-jitter 0.5%, ts-jitter 0.1%
- **Raw JSON (machine-readable, includes shared-timestamp combos):** [`tests/measurev2/results/`](../../tests/measurev2/results)

### Reproduce

```bash
cd tests/measurev2
for p in decimal_gauge_2dp decimal_gauge_4dp counter sparse_constant worst_case; do
  go run . -profile "$p" -metrics 200 -points 200 -pretty \
    -output "results/matrix_$p.json"
done
```

Each profile runs the full 3 timestamp × 4 value matrix (raw / delta / deltapacked  ×  raw /
gorilla / chimp / alp), plus the shared-timestamp variants. Tables below report the
**regular-timestamp** combos; shared-timestamp numbers live in the raw JSON.

## Compression — best combo per profile

| profile | best combo | B/pt | vs raw-raw | winning value codec |
|---|---|---|---|---|
| decimal_gauge_2dp | **delta-alp** | 2.854 | 5.6× | alp |
| decimal_gauge_4dp | **delta-alp** | 3.802 | 4.2× | alp |
| counter | **delta-alp** | 2.581 | 6.2× | alp |
| sparse_constant | **delta-gorilla** | 1.605 | 10.0× | gorilla |
| worst_case | **delta-chimp** | 7.369 | 2.2× | chimp |

`delta` is the best timestamp tier in every profile (deltapacked trades ~0.25 B/pt for speed).
The winning *value* codec changes with the data: **alp** for decimals/counters, **gorilla** for
sparse, **chimp** for full-precision.

## Compression — full grids (bytes/point)

### decimal_gauge_2dp

*2dp gauge random-walk, 15s scrape — the canonical decimal sensor stream*

| ts \ val | raw | gorilla | chimp | alp |
|---|---|---|---|---|
| raw | 16.081 | 14.547 | 14.162 | 9.804 |
| delta | 9.131 | 7.597 | 7.212 | **2.854** |
| deltapacked | 9.381 | 7.847 | 7.462 | 3.104 |

### decimal_gauge_4dp

*4dp gauge random-walk, 15s scrape — higher precision decimals*

| ts \ val | raw | gorilla | chimp | alp |
|---|---|---|---|---|
| raw | 16.081 | 14.567 | 14.317 | 10.752 |
| delta | 9.131 | 7.617 | 7.367 | **3.802** |
| deltapacked | 9.381 | 7.867 | 7.617 | 4.052 |

### counter

*monotonic integer counter, 15s scrape*

| ts \ val | raw | gorilla | chimp | alp |
|---|---|---|---|---|
| raw | 16.081 | 9.668 | 9.991 | 9.531 |
| delta | 9.131 | 2.718 | 3.041 | **2.581** |
| deltapacked | 9.381 | 2.968 | 3.291 | 2.831 |

### sparse_constant

*mostly-constant value, 60s scrape — long runs of repeats*

| ts \ val | raw | gorilla | chimp | alp |
|---|---|---|---|---|
| raw | 16.081 | 8.555 | 8.658 | 9.205 |
| delta | 9.131 | **1.605** | 1.708 | 2.255 |
| deltapacked | 9.381 | 1.855 | 1.958 | 2.505 |

### worst_case

*full-precision random walk, 1s — incompressible IEEE-754 mantissas*

| ts \ val | raw | gorilla | chimp | alp |
|---|---|---|---|---|
| raw | 16.081 | 14.571 | 14.324 | 14.449 |
| delta | 9.126 | 7.616 | **7.369** | 7.494 |
| deltapacked | 9.376 | 7.866 | 7.619 | 7.744 |

## Speed — encode & sequential iterate (ns per 1,000 points, delta timestamps)

`decode` is omitted: mebo decodes lazily, so `benchDecode` only **opens** the blob and is
codec-independent (~5.7 µs/blob flat). The real read cost is **iterate** — a full sequential
`All()` materialization over every point. Allocs are per whole-blob encode (200 columns).

| profile | codec | encode ns/1k | iterate ns/1k | encode allocs/blob |
|---|---|---|---|---|
| decimal_gauge_2dp | raw | 7,433 | 6,264 | 34 |
|  | gorilla | 10,586 | 6,326 | 34 |
|  | chimp | 13,545 | 9,161 | 34 |
|  | alp | 50,498 | 7,205 | 925 |
| decimal_gauge_4dp | raw | 7,483 | 6,239 | 34 |
|  | gorilla | 10,645 | 6,323 | 34 |
|  | chimp | 13,598 | 9,037 | 34 |
|  | alp | 108,623 | 7,224 | 24,082 |
| counter | raw | 7,480 | 6,233 | 34 |
|  | gorilla | 10,439 | 7,061 | 34 |
|  | chimp | 9,497 | 6,548 | 34 |
|  | alp | 28,041 | 7,229 | 44 |
| sparse_constant | raw | 7,448 | 6,367 | 34 |
|  | gorilla | 7,133 | 4,664 | 34 |
|  | chimp | 7,175 | 4,686 | 34 |
|  | alp | 49,816 | 7,284 | 3,403 |
| worst_case | raw | 7,490 | 6,379 | 34 |
|  | gorilla | 10,755 | 6,229 | 34 |
|  | chimp | 13,838 | 9,090 | 34 |
|  | alp | 132,222 | 8,110 | 21,074 |

## Takeaways

- **ALP is the compression champion on decimal & counter data** (2.5–6× smaller than the
  next-best codec). After the encode prune + digit-cache, ALP encode now costs ~4–18× raw
  (down from ~7–19×); the ALP-RD path (4dp, worst_case) is still the most alloc-heavy and the
  remaining optimization target.
- **ALP decode is competitive**: the streaming-reader speedup put ALP sequential iterate at
  ~7,205 ns/1k on decimals — between gorilla (~6,326) and chimp (~9,161), no
  longer the read-laggard (~19,700 before). The read-cost objection to selecting ALP is gone.
- **gorilla is the all-rounder**: best on sparse in *both* size and speed, cheap to encode,
  fastest iterate after raw.
- **chimp** narrowly wins full-precision size; otherwise slower to iterate than gorilla.
- **Choosing ALP blindly is still a trap on the wrong shape**: on `worst_case` it is *larger*
  than chimp; ALP only pays where the ratio win justifies the (now much lower) encode/read cost.
- This data-dependence is the empirical case for **per-column adaptive value-codec selection**
  with raw kept as a hard floor. See
  [`ADAPTIVE_SELECTOR_EXPERIMENTS.md`](ADAPTIVE_SELECTOR_EXPERIMENTS.md) and the
  [implementation plan](../plans/2026-06-15-adaptive-value-codec-selection.md).

## 2026-07-15 addendum — ALP decode-into and validation micro-benchmarks

The tables above are still the 2026-06-15 measurev2 E2E matrix (not re-run this pass). This
addendum covers `internal/encoding` unit benchmarks from an ALP optimization pass: decode-into
paths, fast `At`, scheme validation, 8-byte pack-bit flushes, single-pass `encodeMain`, map-free
ALP-RD planning, and a contiguous (e,f) sample buffer. **All changes are byte-identical**, gated
by `TestNumericALP_GoldenBytes` and related golden/differential tests — none of the ratios in the
tables above change.

**Environment:** go1.26.1 · linux/amd64 · AMD Ryzen 9 9950X3D 16-Core Processor.
**Method:** baseline recorded before the pass; results recorded after it, `go test
./internal/encoding/ -bench BenchmarkALP -benchmem -count 5`, compared with `benchstat`.
`Encode`/`DecodeAll`/`DecodeIterate` process 100 columns ×
1,000 points/column (800,000 bytes of `float64` input per op); `Decimal2dp` exercises the ALP-main
path, `FullPrecision` exercises ALP-RD.

| benchmark | before | after | delta |
|---|---|---|---|
| `ALPEncode_Decimal2dp` | 1.392ms (575 MB/s), 307 allocs | 1.286ms (622 MB/s), 229 allocs | −7.6% time, −25.4% allocs |
| `ALPEncode_FullPrecision` | 4.007ms (200 MB/s), 3,795 allocs | 2.438ms (328 MB/s), 1,356 allocs | −39.2% time, −64.3% allocs |
| `ALPDecodeAll_Decimal2dp` | 268.5µs, 0 allocs | 115.4µs, 0 allocs | −57.0% |
| `ALPDecodeAll_FullPrecision` | 365.4µs, 0 allocs | 226.7µs, 0 allocs | −38.0% |
| `ALPDecodeIterate_Decimal2dp` | 250.0µs, 0 allocs | 248.4µs, 0 allocs | −0.7% (already optimized before this pass) |
| `ALPDecodeIterate_FullPrecision` | 345.7µs, 0 allocs | 342.3µs, 0 allocs | −1.0% (already optimized before this pass) |
| `ALPAt` (random access, 10k-pt column) | 13.98ns (before, early measurement) | 6.58ns, 0 allocs | ~2.1× faster |

`ALPEncode_MixedExceptions` (1.686ms, 1,326 allocs) and the `ALPVsChimp` sub-benchmarks are
unchanged within noise (ALP encode −6.0%, ALP/Chimp decode flat) and have no directly comparable
baseline entry or are net-new regression guards added during this pass.

### Blob-level E2E: ALP added to the value-encoding matrix

`blob/numeric_e2e_bench_test.go` gained explicit-opt-in ALP rows (`WithValueEncoding(format.TypeALP)`)
alongside the existing Gorilla/Raw rows, same 200-metric × 200-point shape, no compression:

| benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| `E2EEncode_DeltaGorilla` (existing, for reference) | 473,883 | 393,962 | 34 |
| `E2EEncode_DeltaRaw` (existing, for reference) | 340,808 | 461,230 | 34 |
| `E2EEncode_DeltaALP` (new) | 2,997,260 | 441,460 | 168 |
| `E2EEncode_DeltaPackedALP` (new) | 2,983,541 | 460,605 | 168 |

ALP's whole-blob encode cost is still ~6× Gorilla's at this shape (expected — ALP does per-column
(e,f) search plus digit/exception encoding) but reflects the improved cost from this pass; see
the per-codec `internal/encoding` numbers above for the improvement over the prior baseline.

## 2026-07-15 addendum, part 2 — ALP generated-kernel decode

A follow-up pass targeted `DecodeAll`'s bulk-decode inner loop with generated, width-specialized
kernels: fused unpack+decode kernels for the ALP-main path and a bulk two-stream unpack for the
ALP-RD path. Both are decode-side only and **byte-identical**, gated by
`TestNumericALP_GoldenBytes` and the round-trip/differential tests — no wire-format or
compression-ratio change; only `DecodeAll` speed moves. `DecodeIterate`'s streaming `All()` path
is untouched by either change (still ~248–249µs / ~338–342µs, flat within noise of the numbers
above) since the generated kernels are wired into `DecodeAll` only.

**Environment:** go1.26.1 · linux/amd64 · AMD Ryzen 9 9950X3D 16-Core Processor (same machine as
above).
**Method:** `go test ./internal/encoding/ -run '^$' -bench BenchmarkALPDecodeAll -benchmem -count 5`;
medians of 5 runs shown. "before" column is the post-decode-into-pass value from the table above.

| benchmark | before | after | delta |
|---|---|---|---|
| `ALPDecodeAll_Decimal2dp` | 115.4µs, 0 allocs | 39.5µs, 0 allocs | −65.8% |
| `ALPDecodeAll_FullPrecision` | 226.7µs, 0 allocs | 95.0µs, 0 allocs | −58.1% |

Each change's win is isolated to its own scheme, as expected from a decode-side, per-scheme
kernel swap: after the ALP-main kernel landed, `ALPDecodeAll_FullPrecision` was still at
212.5µs — the RD path was untouched at that point, since that change only wires kernels into
`decodeMainInto`. After the ALP-RD kernel landed, `ALPDecodeAll_Decimal2dp` was unchanged at
39.2µs — the main path is unaffected by the RD-only change. Both benchmarks report
steady-state 0 allocs/op (a few runs of `ALPDecodeAll_FullPrecision` show 2–3 B/op with 0
allocs/op, an artifact of concurrent-goroutine memstat noise, not a real per-op allocation).

## 2026-07-16 addendum — ALP encode: fast rounding + AVX-512 verify kernel

This pass targets `alpEncodeDigit`/`alpBestEF`'s `math.Round`/domain-guard cost and the
`alpMainStats` verify pass — the two costs the decode-focused work above left untouched (these
are encode-only). Two changes landed and are wired into the default build; one decode-side
kernel was prototyped and parked unwired.

**Byte-identity:** both landed changes are **byte-identical** to prior (v1.8) output. All 8
`TestNumericALP_GoldenBytes` hashes are unchanged (no regeneration). The 19-column cross-version
corpus (`numeric_alp_crossver_test.go`) reports exact size parity: at the default (100,000-value)
scale, aggregate `7,868,099` bytes — an exact match (0.0000% delta, not just within the +0.5%
budget) against the pinned `alpCrossVerBaselineBytes` constant; at the `MEBO_ALP_VERIFY_N=10000000`
gate scale — which has no pinned constant, so the size-parity assertion is skipped and the
aggregate is only logged — `821,797,301` bytes, an exact match against the gate-run value
recorded before the change — and 0 digit divergences on every main-scheme column. Unlike the
earlier decode work, this byte-identity is no longer guaranteed by construction: the fast-round
path rounds half-to-even where the original
`math.Round` rounded half-away-from-zero, so a future dataset with a value that (a) lands exactly
on a `.5` tie post-scaling and (b) survives `alpBestEF`'s (e,f) search without being pruned could
legally encode to different (still lossless, still old-reader-decodable) bytes. The contract is now
enforced empirically by `numeric_alp_crossver_test.go`'s gate run rather than by policy — see that
file and `TestNumericALP_GoldenBytes`'s updated doc comment.

- **Fast rounding**: adopted the magic-number fast-round trick
  (`(x + 2^52+2^51) - 2^52+2^51`) in `alpEncodeDigit` and `alpBestEF`'s estimator, landing as a
  **hybrid**: fast round for `|scaled| < 2^51`, the exact original `math.Round` + `9.2e18`
  overflow guard otherwise. (A pure domain-guard-tightening first attempt was measured +0.6355%
  over the ratio budget on two adversarial exact-large-integer corpus columns and was rejected
  before any golden/commit changes; the hybrid was the fix.)
- **AVX-512 verify kernel**: vectorized `alpMainStats`'s verify pass with a
  hand-written AVX-512DQ kernel (masked classify into good/verify-fail/guard-fail lanes, scalar
  rescue for guard-fail and the `n%8` tail), gated on `internal/arch.X86HasAVX512DQ()`, with a
  scalar fallback everywhere else. Differential-tested bit-equal to the scalar oracle
  (`numeric_alp_encsimd_test.go`).
- **Branch-free exception-index collection** (a third candidate): measured **+2.9% slower**
  than the branchy baseline and **dropped** — never committed as a wired change.
- **AVX-512 fused decode kernel**: built and differential-tested bit-equal to the
  generated scalar decode kernels, but measured **0.873× geomean** against them across widths
  8–64 (`BenchmarkALPDecodeKernelWidths`, table below) — below the 1.5× wire gate (`VPGATHERQQ`
  gather cost on Zen 5 outweighs the fully-unrolled scalar shift/mask the generated kernels
  compile to). **Parked unwired**: the `.s` kernel and differential test are committed in-tree,
  but `decodeMainInto`'s dispatch is untouched and `DecodeAll`/`DecodeIterate` performance is
  unchanged by this pass.

**Environment:** go1.26.1 · linux/amd64 · AMD Ryzen 9 9950X3D 16-Core Processor (same machine as
the sections above). **Method:** `go test ./internal/encoding/ -run '^$' -bench BenchmarkALP
-benchmem -count 5`, medians of 5 runs, `benchstat` of a baseline recorded immediately before
the changes vs. a fresh run after them, same session and machine.

| benchmark | before (v1.8) | after (this pass) | delta |
|---|---|---|---|
| `ALPEncode_Decimal2dp` | 1259.2µs (635 MB/s), 230 allocs | 968.9µs (826 MB/s), 233 allocs | **−23.1%** |
| `ALPEncode_FullPrecision` | 2.469ms (324 MB/s), 1356 allocs | 2.315ms (346 MB/s), 1363 allocs | **−6.3%** |
| `ALPEncode_MixedExceptions` | 1.696ms, 1326 allocs | 1.360ms, 1331 allocs | **−19.8%** |
| `ALPVsChimp/ALP_Encode` (32-pt micro-batch) | 4.742µs | 3.070µs | −35.3% |
| `ALPDecodeAll_Decimal2dp` | 39.55µs, 0 allocs | 39.64µs, 0 allocs | ~ (p=0.31, not significant) |
| `ALPDecodeAll_FullPrecision` | 95.90µs, 0 allocs | 97.06µs, 0 allocs | +1.2% (layout noise, see below) |
| `ALPDecodeIterate_Decimal2dp` | 251.3µs | 252.9µs | +0.7% (noise) |
| `ALPDecodeIterate_FullPrecision` | 345.0µs | 348.9µs | +1.1% (noise) |
| `ALPAt` | 6.613ns | 6.731ns | +1.8% (noise) |

Isolating each change's own contribution (`benchstat` between benchmark runs taken after each
landed): the fast-round change alone is `ALPEncode_Decimal2dp` **−8.03%**
and `ALPEncode_FullPrecision` **−11.77%**; the kernel alone (on top of it) is `ALPEncode_Decimal2dp` a
further **−20.5%** but `ALPEncode_FullPrecision` **+5.1%** (not a regression — a CPU profile
shows FullPrecision routes to ALP-RD, where `alpMainStats` is under 10% of the benchmark's time
and `alpRDPlan` — untouched here — dominates; a same-session stash comparison put the +5.1%
inside that benchmark's own run-to-run CI). Chained, the two are roughly **−27%** on 2dp end-to-end
and **~−6 to −12%** on FullPrecision (varies run to run since the RD path dominates and is
untouched), consistent with the baseline-to-final deltas measured directly in the table
above.

**Encode speedup is ~1.25× end-to-end.** A CPU profile of the
final `Decimal2dp` encode shows `alpBestEF` (the (e,f) search estimator) is now **59%** of
encode CPU, `alpPackBits` ~10%, and the new kernel itself (`alpMainStatsAVX512`) only **2.76%** —
the verify pass this pass targeted is now near-free. Any remaining encode headroom is
estimator-bound (`alpBestEF`'s candidate search), which would need its
own vectorization/pruning work.

**Decode is unchanged.** No production decode code was touched by either landed change; the small
(0.7–1.8%) `DecodeAll`/`DecodeIterate`/`At` deltas above are attributed to binary code-layout
shift from the changed encoder function sizes, not a real decode-path change — the same pattern
appears on completely untouched codecs in the same run (e.g. `ALPVsChimp/Chimp_Encode` and
`Chimp_Decode` also move ±2–3.5% run to run) and is confirmed harmless by the crossver harness's
bit-exact lossless check.

### AVX-512 decode kernel vs. generated scalar kernels (parked, not wired)

`BenchmarkALPDecodeKernelWidths`, asm kernel vs. the generated
scalar kernel `alpFusedUnpack[w]`, same N=1024 whole-group column, count=5 medians:

| width | scalar (ns/op) | asm (ns/op) | scalar/asm |
|---:|---:|---:|---:|
| 8 | 328.0 | 436.1 | 0.75× |
| 16 | 357.9 | 435.8 | 0.82× |
| 32 | 357.9 | 432.6 | 0.83× |
| 48 | 424.9 | 436.5 | 0.97× |
| 56 | 470.2 | 442.6 | 1.06× |
| 64 | 323.1 | 425.3 | 0.76× |

Geomean **0.873×** across all measured widths (8, 12, 16, 20, 24, 32, 40, 48, 56, 64) — below the
1.5× wire gate. The flat ~430–440ns asm curve (vs. the scalar curve tracking bytes touched) is
the signature of a gather bottleneck: two `VPGATHERQQ` per 8-value group cost more on Zen 5 than
the fully-unrolled constant-shift loads the generated kernels compile to. Kept in-tree, unwired,
with a differential test that exercises the real asm directly (does not silently no-op when
unwired).

**Related follow-up (out of scope here):** a per-vector ALP-v2 PoC
(`poc_alpv2_ratio_test.go`) measured **−7.73%**
aggregate corpus size vs. the current whole-column ALP — past the ≥5% ratio-improvement trigger
that would justify pursuing a per-vector encoding design. Not implemented here;
flagged for future work.
