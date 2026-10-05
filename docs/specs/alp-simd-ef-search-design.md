# Design: faster ALP encoding — AVX-512 (e, f) search and a lower encode floor

**Date:** 2026-10-04
**Status:** implemented (Phases 0–4, each Codex-reviewed to merge); all four gates passed (see Results).
Spec v4 passed Codex review; the owner confirmed the gates.
v1 and v2 (a stats-returning kernel with Go-side selection) were Codex-reviewed twice;
v3 rewrote the kernel contract around the prototype that selects inside the kernel
and added three exact changes to the encode work outside the search.
v4 addresses the v3 review (`tmp/reviews/alp-simd-ef-search-spec-codex-review-v3.md`, not committed):
compiler fusion, CPU prerequisites, a portable framed corpus, memory-contract tests, and the forced-off benchmark mode.
Research notes and prototypes: `tmp/alp-encode-perf-research/` (not committed).

## Goal

Make ALP and ALP-RLE encoding several times faster without changing a single output byte.
Before this work ALP encoded 4–5× slower than Chimp at the blob level (`docs/performance.md` at e7965cf),
and almost all of that time is the per-column (e, f) search.
The search kernel needs amd64 with AVX-512DQ;
two of the three encode-floor changes are portable Go and help every target.

## Evidence

Measured 2026-10-04 on a Ryzen 9 9950X3D, Go 1.26.7, `tests/measurev2` profiles at 100 metrics × 150 points.
All numbers are single-binary runs: direction only until layout-averaged.

### Where the time goes today

The value codec alone, driven the way the blob encoder drives it:

| Profile | Chimp ns/pt | ALP ns/pt | ALP-RLE ns/pt |
|---|---:|---:|---:|
| mix_monitoring | 3.4 | 43.1 | 55.2 |
| mix_integer | 3.6 | 38.1 | 40.9 |
| mix_fullprec | 3.2 | 61.2 | 93.3 |
| mix_sensor | 2.8 | 47.6 | 64.1 |

CPU profiles put `alpBestEF` at 85–91% of ALP encode.
For ALP-RLE, `alpBestEF` plus the nested column's seeded search (`alpEFEstimate`) take 83–89%.

The search tries up to 190 (e, f) candidates on a strided sample of at most 63 values (38 at 150 points).
Pruning is weak by construction:
full-precision columns win at a large width, so neither prune rule fires early,
and the time goes into the start of each candidate's short loop:
a ~35-cycle FP dependency chain plus a mispredict at the prune exit.
Scalar restructurings measured slower, and seeding with the true best (e, f) cuts evaluations by only 30%.

### What the prototypes reach

Every prototype below is exact: same (e, f), same bytes.
They were checked on 3,932 encodings (ALP and ALP-RLE, both endians, corpus, special, random and adversarial columns).

The full search (`alpBestEF`, including the sample copy and selection), ns/pt:

| Step | ns/pt |
|---|---:|
| scalar today | 33–50 |
| one AVX-512 call over all candidates, streamed | 16.7 |
| `v·10^e` shared across one e, `VRNDSCALEPD`, NaN padding | 13.7 |
| one compare picks the slow path; float min/max; popcount on the integer ports | 8.8 (kernel only) |
| slow path skipped when no lane needs it; two candidates per loop | 7.4 |
| selection inside the kernel | 6.74 |

The encode floor (the search replaced by a replay of its answers), ns/pt:

| | mix_monitoring | mix_integer | mix_fullprec | mix_sensor |
|---|---:|---:|---:|---:|
| ALP today | 3.97 | 3.14 | 7.22 | 3.72 |
| ALP with the floor changes | 1.99 | 1.74 | 3.14 | 1.95 |
| ALP-RLE today | 6.13 | 4.17 | 12.97 | 6.30 |
| ALP-RLE with the floor changes | 3.81 | 2.73 | 6.57 | 4.13 |

Blob-level encode with both (Shared DeltaPacked, 5 runs), ns/pt and ratio to Chimp:

| | Chimp | ALP today | ALP new | ALP-RLE today | ALP-RLE new |
|---|---:|---:|---:|---:|---:|
| mix_monitoring | 13.6 | 55.3 (4.03×) | 18.0 (1.33×) | 67.3 (4.91×) | 22.4 (1.65×) |
| mix_integer | 12.9 | 50.0 (3.73×) | 17.6 (1.36×) | 53.4 (3.99×) | 18.9 (1.46×) |
| mix_fullprec | 14.6 | 73.1 (4.99×) | 19.9 (1.36×) | 106.2 (7.25×) | 27.1 (1.85×) |
| mix_sensor | 14.1 | 59.6 (4.24×) | 18.1 (1.29×) | 75.3 (5.36×) | 24.9 (1.77×) |

After both changes the kernel is 37% (ALP) and 43% (ALP-RLE) of blob encode on mix_monitoring;
everything else is spread out at a few percent each.
A last kernel idea, proving per candidate that no lane needs the slow path,
has a measured ceiling of 0–7% at the blob level,
so the search stops at this kernel.

## Key decisions

- **No pruning, same answer.**
  The kernel evaluates every candidate on every sample value.
  Selecting the first strictly smaller estimate in (e ascending, f ascending) order
  then returns exactly the (e, f) `alpBestEF` returns:
  a prune only fires when a partial bound reaches `best`, the partial bound never exceeds the final estimate,
  and strict `<` keeps the earlier candidate on ties.
  Codex confirmed this (2026-10-04), including the all-exception and out-of-int64 cases.
  `alpBestEFSeeded` uses the same two prune rules through `alpEFEstimate`, so the same argument covers it.
- **Selection happens inside the kernel.**
  Each candidate's estimate becomes a key `est<<9 | rank`, with rank 0 for the seed and `c+1` otherwise,
  and the kernel keeps the minimum key.
  One compare then implements both tie rules:
  the plain search keeps the earliest candidate, and the seeded search lets the seed win every tie.
  This replaces v2's per-candidate statistics and Go-side selection, which cost about 9% more.
- **Byte identity is a hard requirement.**
  No golden is regenerated; a moved golden or digest is a bug.
- **The nested ALP-RLE column uses the same kernel** with a seed.
  This replaces the nested-search skip that was rejected (see Rejected alternatives).
- **The floor changes are exact replacements** of existing code paths,
  each with an existing or extended differential test:
  the AVX-512 stats kernel handles every lane (no scalar rescue or tail),
  generated pack kernels write whole 64-code blocks,
  and the RD cut search skips cuts that can only have one distinct left part.
- **Scalar code stays as the oracle.**
  `alpBestEF`, `alpBestEFSeeded`, `alpEFEstimate` and `alpMainStatsScalar` keep their logic and remain the fallback.
- **Unfused arithmetic is the reference, on every build.**
  The Go spec lets the compiler fuse `x*y + z` into one FMA unless an explicit conversion forces the rounding.
  On `main` it already does so in the scalar encoder:
  `go build -gcflags=-S` shows FMA instructions in `alpBestEF` and `alpEFEstimate` under `GOAMD64=v3` and above,
  and in those two plus `alpEncodeDigit` on arm64, while `GOAMD64=v1` (the default) has none.
  A fused `v·10^e·10^-f + 0x1.8p52` rounds once instead of twice,
  so near a half-integer those builds can pick a different digit or (e, f) than a `v1` build or the existing AVX-512 stats kernel.
  The output stays lossless (every digit is verified back), but bytes depend on the build.
  Phase 0 adds explicit `float64(...)` conversions at those sites;
  `v1` builds compile to the same code, and `v3` and arm64 builds then match them.
  This deliberately changes `GOAMD64>=v3` and arm64 output on such inputs, toward the `v1` bytes;
  the byte-identity baseline is `main` at `e7965cf` built for `GOAMD64=v1`.
  Test-only generators that feed goldens (`genALPColumns`, `alpRunsHold`) get the same conversions:
  on `main`, `TestNumericALP_GoldenBytes/fullPrecision` already fails under `GOAMD64=v3` because its input differs, not its encoder.
- **Every instruction the kernels use is gated.**
  The stats kernel needs AVX-512F and DQ.
  The search kernel also needs POPCNT, so it runs only when `arch.X86HasPOPCNT()` holds too.
  It computes widths with `BSR` and an explicit zero case instead of `LZCNT`,
  which has its own CPUID bit and decodes as `BSR` on CPUs without it.

## Search kernel

### Contract

```go
// alpEFSearchAVX512 evaluates every (e, f) candidate on sample[0:ns] and returns the index of the winner.
// seed is a candidate index whose estimate wins every tie, or -1 for the plain search.
//go:noescape
func alpEFSearchAVX512(sample *[64]float64, ns int, factors *[alpEFCandidates][4]float64, seed int) int
```

- `alpEFCandidates` is 190; candidate `c` is the c-th (e, f) of `for e := 0..18 { for f := 0..e }`,
  so `c = e(e+1)/2 + f`, and that order is the selection order.
- `factors[c]` holds `{10^e, 10^-f, 10^f, 10^-e}`, built once at package init from `alpPow10` and `alpInvPow10`,
  so the bit patterns are the scalar tables' own values.
  It is a package-level array: the search allocates nothing.
  The kernel reads `10^e` from the first candidate of each e group, so the table must keep that order.
- `1 ≤ ns ≤ 63` (`TestAlpRDSampleBound`); the Go wrapper falls back to scalar outside that range.
- `sample` is the wrapper's own stack array, so every read stays inside those 512 bytes.
  The caller fills lanes `ns ≤ i < ⌈ns/8⌉·8` with NaN,
  and the kernel reads exactly `⌈ns/8⌉` vectors.
  NaN padding needs no valid-lane mask: a NaN lane is never good under the rules below,
  and the estimate counts exceptions as `ns − good`.
- Returns the winning index; the wrapper maps it back to (e, f) through a package-level table.

### Assembly layout

- ABI0, `TEXT ·alpEFSearchAVX512(SB), NOSPLIT, $512-40`, a leaf with no Go calls.
  The 512-byte local frame holds `v·10^e` for the current e (at most 8 vectors);
  the linker's nosplit check covers the frame size.
- Arguments: `sample+0(FP)`, `ns+8(FP)`, `factors+16(FP)`, `seed+24(FP)`, `ret+32(FP)`;
  `go vet` (asmdecl) checks the frame.
- `factors` stride is 32 bytes (`pe, iff, pf, ie` at offsets 0, 8, 16, 24).
- Constants (2^51, abs mask, sign mask, 9.2e18, 0.5, +Inf, −Inf) stay in registers across candidates;
  the register map is documented in the `.s` header like `alpMainStatsAVX512`'s.
- `VZEROUPPER` before `RET`.

### Per-lane semantics

For each e: `ve = v · 10^e` for every sample vector, stored in the frame and shared by the e+1 candidates of that e.
The candidates of one e run two at a time (f, f+1), sharing the loads of `ve` and `v`;
when e+1 is odd, the last candidate runs alone.
For each candidate and each vector, with `x` the scaled value:

1. `x = ve · 10^-f`.
   Together with the shared `ve` this is the scalar `v * pe * iff`: two separate multiplies, same order, never fused.
2. `big = |x| ≥ 2^51` (`GE_OQ`, so false for NaN).
3. `r = RNE(x)` (`VRNDSCALEPD` imm 0x08).
   For `|x| < 2^51` this equals the scalar magic-number round.
4. **Fast path**, taken when no lane of the vector (or of the pair's two vectors) is big:
   `back = (r · 10^f) · 10^-e`, two separate multiplies; `good = (back == v)` with `EQ_OQ`.
   This is the estimator's FP `!=`, not a bit compare:
   −0.0 verifies as good, and NaN (data or padding) never does.
   Nothing on this path can be out of the int64 range.
5. **Slow path**, otherwise:
   for big lanes `r = trunc(RZ(x + copysign(0.5, x)))` (`VADDPD` with round-toward-zero, then `VRNDSCALEPD` imm 0x0B);
   then `back` and `good` as in the fast path,
   and `good` also requires `|x| < 9.2e18` (`LT_OQ`, so false for NaN and ±Inf).
   - For `2^51 ≤ |x| < 2^52` doubles are multiples of 0.5, so the addition is exact,
     and truncation gives half away from zero, which is `math.Round`.
   - For `|x| ≥ 2^52`, `x` is integral and the exact sum is not representable,
     so round-toward-zero returns `x` itself, and `r = x = math.Round(x)`.
   - ±Inf stays infinite and fails the range compare.
     Comparing `|x|` instead of the scalar's `|round(x)|` against 9.2e18 is equivalent,
     because `round(x) = x` above 2^52 and both sides are below 9.2e18 under it.
6. Over good lanes: `VMINPD`/`VMAXPD` accumulate `r` as doubles, starting from +Inf and −Inf,
   and `KMOVB` + `POPCNT` count good lanes on the integer ports.
   `r` may be −0.0 where the scalar has the integer 0; both convert to 0, and the FP compare above treats them alike.
7. After the last vector: reduce min and max horizontally and convert both to int64.
   When `good > 0` the conversion is exact, because good values are integral and below 9.2e18 in magnitude.
   When `good == 0` the accumulators still hold ±Inf, the conversions yield the integer-indefinite value,
   and the result is discarded: width is forced to 0.
   Then `width = (good > 0 && max ≠ min) ? BSR(max − min) + 1 : 0`, with the difference taken as unsigned 64-bit,
   which is `bits.Len64(uint64(mx − mn))`;
   and `est = ns·width + (ns − good)·96`, the scalar estimate `ns*width + nExc*96`.
8. `key = est<<9 | rank`; keep the minimum.
   `est ≤ 63·64 + 63·96 = 10,080` and `rank ≤ 190 < 2^9`, so keys never collide or overflow.
   The kernel returns `rank − 1`, or `seed` when the minimum has rank 0.

## Encode floor

Three exact changes to the work outside the search.

### Stats kernel for every lane (amd64, AVX-512DQ)

`alpMainStatsAVX512` is replaced by a kernel that implements `alpEncodeDigit` for every lane,
so `alpMainStatsSIMD` loses its scalar rescue of guard lanes and its scalar tail.

```go
// alpMainStatsAVX512 computes alpEncodeDigit for values[0:n].
// Good digits are stored to dst; blockMask[g] gets block g's exception lanes; mnmx receives {min, max} over good digits.
// The result is the OR of all block masks.
//go:noescape
func alpMainStatsAVX512(values *float64, n int, factors *[4]float64, dst *uint64, blockMask *uint64, mnmx *[2]int64) uint64
```

Per lane, with `x = (v · pe) · iff`:

- `r = RNE(x)`; lanes with `2^51 ≤ |x| < 2^52` use `x + copysign(0.5, x)` instead,
  and the truncating convert finishes the round half away from zero;
  above 2^52 `r = x`.
- `inRange = |x| < 9.2e18` (`LT_OQ`); `d = VCVTTPD2QQ(r)`.
- `back = (float64(d) · pf) · ie`, compared **bitwise** (`VPCMPEQQ`) with `v`, as `alpEncodeDigit` does:
  converting through int64 turns −0.0 into +0.0, so a −0.0 input stays an exception.
- `good = valid ∧ inRange ∧ verified`; good digits are masked-stored, and signed min and max accumulate over good lanes.
- The last block loads with a fault-suppressing masked load, and stores never touch lanes at or past `n`.

The min and max accumulators start at MaxInt64 and MinInt64.
When no lane is good they come back unchanged,
and the wrapper returns `ok = false` (from `nExc == n`) before reading them, as `alpMainStatsScalar` does.
The Go wrapper builds `excPos` by walking `blockMask` in ascending order,
which keeps the decoder's ascending-position invariant.
The kernel handles every `n ≥ 1`; whether small columns (`n < 8`) should stay scalar is measured in Phase 2.

### Generated 64-code pack kernels (portable)

`alpPackBits` packs whole 64-code blocks with width-specialized functions,
then finishes the remainder with today's loop.
64 codes at width `w` fill exactly `w` words, so the stream stays word-aligned after each block
and the remainder loop starts with an empty accumulator, exactly as it would have.
Each block function masks the codes to `w` bits, as the loop does, and writes little-endian words.
`gen/alpkernels` emits them into `numeric_alp_kernels_gen.go`, next to the decode kernels,
as `alpPackBlock [65]func(out []byte, codes *[64]uint64)` with index 0 nil.
A branchless rewrite of the existing loop was 8–21% slower;
the generated blocks cut packing from 0.76–1.13 to 0.23–0.38 ns/code.

### RD cut search skips single-left cuts (portable)

`alpRDBestCut` first ORs `p XOR patterns[0]` over the sample.
A cut whose left part covers none of those bits has a single distinct left by construction,
so its histogram is set directly to `{patterns[0]>>r: n}` without the scan.
The histogram, and so the top-8 dictionary and the estimate, are identical.

## Dispatch and files

- `encodeColumn` calls a new `alpSearchEF(values, stride, seeded, seedE, seedF)` in `alp.go`.
  It tries `alpBestEFSIMD(values, stride, seed)` and falls back to `alpBestEFSeeded` or `alpBestEF`.
- `alpBestEFSIMD` lives in `alp_encsimd_amd64.go`:
  it copies the strided sample into a stack `[64]float64`, NaN-pads it, and calls the kernel directly;
  a call through a func value would move the array to the heap.
  It reports `ok = false` when the switch is off or `ns` is out of range.
  `alp_encsimd_noasm.go` gets the shim that always reports `ok = false`.
- A package-level switch, `alpEncAVX512`, initialized from `arch.X86HasAVX512DQ()`, gates both kernels,
  so tests can force the scalar paths on the same machine;
  the search kernel additionally checks `arch.X86HasPOPCNT()`, a new helper next to the existing ones (false off amd64).
  Tests that flip the switch run serially (no `t.Parallel` in them or their subtests) and restore it with `t.Cleanup`.
- Whole processes force both kernels off with `GODEBUG=cpu.avx512dq=off`, which `golang.org/x/sys/cpu` honors.
  This works for any revision, `main` included, so benchmarks compare like with like.
- Asm for both kernels goes into `alp_encsimd_amd64.s`.
  The factor table and the candidate-to-(e, f) table go in `alp.go`'s variable section, per the declaration-order rule.
- Tests go into the existing `alp_test.go` and `alp_runs_test.go`; benchmarks into `alp_bench_test.go`.
  Tests that call the kernels directly or need guard pages go into `alp_encsimd_amd64_test.go` (`//go:build linux`),
  the encoder SIMD component's own test file.
  Generated code goes into the existing generated file; there are no other new files in the package.
- Non-amd64 builds must keep compiling: every phase cross-compiles and vets for `GOARCH=arm64`.

## Validation

- **Corpus digest, the primary byte-identity gate.**
  Phase 0 adds deterministic in-package corpus generators and a digest test.
  - Inputs are portable: every product and sum in the generators is wrapped in an explicit `float64(...)` conversion,
    and they use only `math/rand` integers and `Float64`, deterministic binary64 operations with controlled rounding
    (`math.Round`, division by `alpPow10` constants, and the exact `math.FMA` of the fusion probe below),
    never `math.Pow`, `NormFloat64` or other transcendental functions.
    A separate input digest is pinned,
    so a platform that generates different inputs fails with that message instead of an encoder diff.
  - Each record hashes the group, case index, codec, endian, input count, output length and output bytes.
    One digest per group is pinned, which localizes a failure.
  - Columns are encoded blob-style: one encoder per group, `WriteSlice` + `Bytes` + `Reset` per column,
    so state carried between columns (scratch buffers, the nested seed) is covered.
    Groups alternate clean, exception-heavy, negative-minimum, RD, raw and runs columns.
  - The constants come from a clean worktree of `e7965cf` with only the test file added, built for `GOAMD64=v1`.
  The corpus covers:
  - columns shaped like the twelve `tests/measurev2` profiles at 100 × 150 (gauge, counter, sparse, hold and step shapes, and the four mixes);
  - random columns of lengths 1 to 1000;
  - special values: NaN with several payloads, ±Inf, −0.0, subnormals, ±1e300, ±MaxFloat64, and values whose `v · 10^e` overflows;
  - values constructed so that `x` lands exactly on and just either side of 2^51, 2^52 and 9.2e18 for specific (e, f), with both signs;
    the generator asserts that the scaled values actually hit those targets;
  - digits whose range needs width 64, and ranges whose unsigned difference crosses 2^63;
  - fusion-sensitive values, found by searching for `v` where `math.FMA(v·10^e, 10^-f, 0x1.8p52)` and the unfused sum round differently;
  - mixed adversarial columns (a held full-precision segment plus distinct 2-decimal values),
    the shape where the plain column picks RD and the nested column picks main with a seed different from its optimum;
  - the shapes of `alpRunsCases()`, rebuilt with the portable generators.
  The digest test runs under `GOAMD64=v1` and `GOAMD64=v3` on amd64, under `GOARCH=386` (native, the portable path),
  with `GODEBUG=cpu.avx512dq=off`, and in-process with `alpEncAVX512` forced off.
  Until Phase 0's fusion barriers land, `GOAMD64=v3` is expected to differ on the fusion-sensitive group;
  after them it must match.
- **No fusion left:** after Phase 0, `go build -gcflags=-S` for `GOAMD64=v3` and for `GOARCH=arm64` shows no FMA instruction in the encoder's functions.
- **Search selection:** `alpBestEFSIMD` equals `alpBestEF` (plain) and `alpBestEFSeeded` for every one of the 190 seeds,
  on the corpus columns and on columns of every length that yields `ns` from 1 to 63.
  Constructed cases cover seeds at the first and last candidate, equal estimates across a pair boundary, all-exception ties, zero-width ties,
  ranges 0, 1 and `1<<63`, width 64, all-NaN and all-infinite samples,
  and pairs where only one candidate has good lanes or needs the slow path.
  The kernel's only output is the winner,
  so a wrong estimate for a candidate that does not win is invisible on that input;
  the all-seed runs and the fuzz target are what expose it on others.
  Lanes of the wrapper's array past the last vector are poisoned to show they cannot change the result.
- **Fuzz target:** arbitrary float64 bit patterns; selection equals scalar for the plain search and a random seed.
- **Stats kernel:** direct kernel calls (below any dispatch threshold) for `n` from 1 to 17 and every tail residue,
  all-exception tails, and a sole good value in the last valid lane;
  all 190 (e, f) on the boundary columns; canaries around `dst` and `blockMask`;
  no invalid tail bit in `blockMask` or the returned OR;
  and, on Linux, the last valid input placed immediately before a `PROT_NONE` page to prove the masked tail load never faults.
  The existing `TestALPMainStatsAVX512_*` differential tests keep running through the dispatch wrapper.
- **Pack:** `TestAlpPackBits_Differential` covers lengths 0, 1, 63, 64, 65, 127, 128, 129, 192, 193 and 300 at every width 0–64,
  prefixes at every offset modulo 8, codes with bits above the width, and successive appends into a reused buffer.
- **RD cut:** `TestAlpRDBestCut_Differential` gains sample sizes 1 and 63, identical patterns,
  patterns sharing their top 1–16 bits (every transition between skipped and scanned cuts), and top-bit-only differences;
  it compares both the chosen cut and the estimated bits.
- **Byte identity:** the corpus digest, `TestNumericALP_GoldenBytes` and `TestNumericALPRuns_GoldenBytes`.
  `TestALPCrossVer_*` keeps passing as a losslessness and size check, not as a byte-identity check.
- **Allocations:** the value-only benchmark reports no more allocs/op than `main`, kernels on and forced off.
- **Compatibility:** `tests/compat` runs against the final commit, since it builds committed refs.
- **Builds:** `go vet` (asmdecl) and a native amd64 link, which checks the NOSPLIT chain;
  `make lint`; `make test`; `GOARCH=arm64 go vet ./...`.
  No arm64 or big-endian emulator is available on the development machine,
  so arm64 output is covered by the fusion check and the portable-path run on 386, not by executing arm64 code.

The stats-kernel tests need AVX-512F and DQ, and the direct search tests also need POPCNT;
they skip otherwise, so they must run on the 9950X3D before each phase is reported done.
A fresh process under `GODEBUG=cpu.popcnt=off` checks that the search declines the kernel while the stats kernel stays eligible.

## Gates

Confirmed by the owner on 2026-10-04.


1. Byte identity: the corpus digest and every golden unchanged, kernels on and forced off.
   This gate is hard.
2. Speed: ALP and ALP-RLE blob encode at least 2.5× faster than `main` at `e7965cf` on each of the four mixes.
   The prototypes measured 2.8–3.7× (ALP) and 2.8–3.9× (ALP-RLE) single-binary.
3. No slower path: with `GODEBUG=cpu.avx512dq=off` on both sides, blob encode is not slower than `main` by more than 3% on any mix.
4. No new allocations.

Measurement for gates 2 and 3:
`BenchmarkBlobEncodeMixes` in `tests/measurev2` (added in Phase 0, and copied into the baseline worktree, since it is test-only),
100 metrics × 150 points, Shared DeltaPacked timestamps, Chimp, ALP and ALP-RLE values, `-cpu 1` under `taskset -c 6`.
Each side is built in 4 layouts by a padding function in `internal/pool` (0, 1, 2 and 4 steps, as in the ALP-RLE harness),
3 interleaved rounds give n = 12 per side, and the gate uses the benchstat median ratio.

## Results

Measured 2026-10-04 after Phases 0–3, with the procedure under Gates
(raw data and scripts in `tmp/alp-simd-ef-search-measure/`, not committed).
Blob encode in ns/point, median of n = 12 per side:

| Mix | Codec | `e7965cf` | new | speedup | new vs Chimp | AVX-512 off: `e7965cf` → new |
|---|---|---:|---:|---:|---:|---:|
| mix_monitoring | ALP | 52.90 | 16.93 | 3.12× | 1.17× | 53.73 → 52.73 |
| mix_monitoring | ALP-RLE | 65.00 | 21.44 | 3.03× | 1.48× | 66.19 → 64.80 |
| mix_integer | ALP | 47.77 | 16.55 | 2.89× | 1.20× | 49.00 → 48.36 |
| mix_integer | ALP-RLE | 51.09 | 17.89 | 2.85× | 1.29× | 52.14 → 51.39 |
| mix_fullprec | ALP | 68.83 | 18.55 | 3.71× | 1.19× | 68.69 → 67.47 |
| mix_fullprec | ALP-RLE | 104.30 | 25.50 | 4.09× | 1.63× | 103.70 → 102.35 |
| mix_sensor | ALP | 56.61 | 16.91 | 3.35× | 1.13× | 57.65 → 56.47 |
| mix_sensor | ALP-RLE | 74.41 | 23.89 | 3.11× | 1.60× | 75.95 → 74.57 |

Chimp itself is unchanged (13.8–15.6 ns/point on both sides).

1. Byte identity: the identity digests (pinned from `e7965cf` at `GOAMD64=v1`) and every golden pass in every mode tried:
   `GOAMD64=v1` and `v3`, 386, `GODEBUG=cpu.avx512dq=off` and `cpu.popcnt=off`, and `alpEncAVX512` forced off.
   Passed.
2. Speed: 2.85–4.09× against the 2.5× gate; every one of the four layouts agrees within 0.15×.
   Passed.
3. With AVX-512 off on both sides the new code is 1–2% faster (the portable pack kernels and RD skip).
   Passed.
4. allocs/op is identical on every benchmark, in both modes.
   Passed.

The (e, f) search alone (`BenchmarkALPBestEF`, single binary) went from 34–52 to 6.9–7.2 ns/point on the four mixes.
The generated pack kernels add about 189 KB of text to a binary that links the encoder (110 KB of code plus tables).

The 12 single-kind profiles, measured the same way in Phase 4
(benchmark file kept outside the repository, `tmp/alp-simd-ef-search-measure/profiles_run.sh`, not committed), outside the gates:

| Profile | ALP `e7965cf` → new | speedup | ALP-RLE `e7965cf` → new | speedup | new vs Chimp, ALP / ALP-RLE |
|---|---:|---:|---:|---:|---:|
| counter | 24.66 → 15.22 | 1.62× | 25.03 → 15.62 | 1.60× | 1.29× / 1.32× |
| sparse_constant | 37.94 → 15.54 | 2.44× | 51.31 → 20.36 | 2.52× | 1.79× / 2.34× |
| cal_1dp_step0.01 | 46.30 → 15.41 | 3.00× | 66.30 → 24.09 | 2.75× | 1.31× / 2.06× |
| cal_2dp_step0.005 | 49.45 → 15.64 | 3.16× | 57.78 → 18.98 | 3.04× | 1.09× / 1.32× |
| cal_2dp_hold70 | 51.86 → 15.76 | 3.29× | 79.47 → 26.00 | 3.06× | 1.31× / 2.15× |
| cal_1dp_step0.03 | 53.25 → 15.38 | 3.46× | 66.50 → 20.89 | 3.18× | 1.00× / 1.36× |
| cal_2dp_hold30 | 54.42 → 15.68 | 3.47× | 80.55 → 25.34 | 3.18× | 0.97× / 1.57× |
| cal_2dp_hold50 | 54.41 → 15.68 | 3.47× | 82.15 → 25.43 | 3.23× | 1.05× / 1.70× |
| decimal_gauge_2dp | 55.14 → 15.44 | 3.57× | 55.39 → 15.86 | 3.49× | 0.93× / 0.95× |
| decimal_gauge_4dp | 72.15 → 18.48 | 3.90× | 72.51 → 18.95 | 3.83× | 1.09× / 1.12× |
| legacy_random_walk | 83.56 → 20.56 | 4.06× | 83.90 → 20.98 | 4.00× | 1.13× / 1.15× |
| worst_case | 82.58 → 19.62 | 4.21× | 82.87 → 20.04 | 4.14× | 1.16× / 1.18× |

- `counter` and `sparse_constant` gain least because `e7965cf` already encoded them fastest (24.66 and 37.94 ns/point for ALP),
  so the scalar search the kernel replaces was a smaller share of their time.
- With AVX-512 forced off the new code is 1–4% faster than `e7965cf` on every profile,
  and allocs/op is identical in both modes.
- Chimp moves by at most 1% between the two sides.

## Phases

### Phase 0 — scaffolding and fusion barriers

- Corpus generators and the digest test; its constants come from the clean `e7965cf` worktree before any encoder change.
- The fusion barriers in the scalar encoder, then the `GOAMD64=v3` digest run and the no-FMA check.
- The `alpEncAVX512` switch (gating the existing stats kernel for now) and `arch.X86HasPOPCNT`.
- Value-only encode benchmark for ALP, ALP-RLE and Chimp
  (one encoder per blob, per-metric `WriteSlice` + `Bytes` + `Reset`),
  `BenchmarkALPBestEF`, and `BenchmarkBlobEncodeMixes` in `tests/measurev2`.

### Phase 1 — portable floor

- Pack kernels in `gen/alpkernels`, regenerated output, and the `alpPackBits` block loop.
- The RD cut skip.
- Extended pack and RD differential tests; binary text-size change reported.

### Phase 2 — stats kernel

- The all-lane `alpMainStatsAVX512`, the simplified `alpMainStatsSIMD`, and the small-`n` decision.
- `alp_encsimd_amd64_test.go` with the direct-call, canary and guard-page tests.

### Phase 3 — search kernel

- `alpEFSearchAVX512`, the factor and index tables, `alpBestEFSIMD`, the shim, and `alpSearchEF`.
- Selection, seed and fuzz tests.

### Phase 4 — measurement and docs

- Layout-averaged blob encode on the four mixes and the single-kind profiles,
  kernels on and forced off, against the gates.
- Regenerate `docs/performance.md` with the `update-performance-report` skill;
  update the encode-cost wording in `README.md` and `docs/best_practices.md`.

Each phase gets an external Codex review before it is reported done.

## Risks

- Generated pack kernels add 64 functions; Phase 1 reports the text-size change.
- Intel server parts before Ice Lake lower the core clock under heavy AVX-512 use;
  one short burst per column is unlikely to matter, but encode-heavy services on those parts should measure.
- CI runners may lack AVX-512DQ, so the kernel tests can skip there; local runs on the 9950X3D are the gate.
- Benchmarks move ±20–40% with code layout; only layout-averaged numbers count toward the gates.

## Rejected alternatives

- **Per-candidate proof that no lane needs the slow path** (max `|v·10^e|` per e, rounding monotonicity):
  exact, but an ablation that drops the check entirely gains only 0–5% (ALP) and 2–7% (ALP-RLE) at the blob level.
- **Two-pass or progressive pruning** (a lower bound from 8 samples, then survivors):
  saves 20–35% of the search on decimal mixes and loses on full precision,
  where the winner's width keeps every bound weak.
  Exception-only bounds prune nothing, since decimal candidates at k ≥ decimals have no exceptions.
- **Per-candidate statistics with Go-side selection** (v2 of this spec): about 9% slower than selecting in the kernel.
- **Skipping the nested ALP-RLE search when the plain column is not ALP-main**:
  corpus bytes were unchanged, but mixed columns grew by up to 2,596 bytes, and no cheap signal separates them.
- **Seeding the search or passing (e, f) hints across blobs**: at most 1.4× even with a perfect seed.
- **Reusing recent winners or a 16-value sample**: bytes change, and the mixes grow by 3–13%.
- **Scalar restructurings** of the search and a branchless pack loop: measured slower.

## Out of scope

AVX2 and arm64 search kernels;
SIMD runs detection, SIMD RD encode, and FOR subtraction fused into packing (each about 0.3–0.6 ns/pt, the next floor);
any change to the estimator or to which (e, f) wins, apart from the Phase 0 normalization of fused builds; the decoder.
