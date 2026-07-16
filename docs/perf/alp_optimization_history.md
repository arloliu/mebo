# ALP Optimization History

A running log of ALP (`format.TypeALP`) encode/decode speed optimization passes, in the order
they landed. This is development history, not current-state reference — for current benchmark
numbers (including the profile-based compression comparison this doc used to carry), see
[`docs/performance.md`](../performance.md#codec-selection-by-data-shape).

ALP's encoded output has been **byte-identical across every pass below** (verified by
`TestNumericALP_GoldenBytes` and the cross-version corpus harness), so none of this history
affects compression ratio — only encode/decode speed.

## 2026-07-15 addendum — ALP decode-into and validation micro-benchmarks

This addendum covers `internal/encoding` unit benchmarks from an ALP optimization pass: decode-into
paths, fast `At`, scheme validation, 8-byte pack-bit flushes, single-pass `encodeMain`, map-free
ALP-RD planning, and a contiguous (e,f) sample buffer. **All changes are byte-identical**, gated
by `TestNumericALP_GoldenBytes` and related golden/differential tests — the compression ratio is
unaffected.

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
