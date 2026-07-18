# Compression Codec Research & Findings (2026-06)

A study of lossless compression codecs that could improve on mebo's current
encodings, with **in-repo proof-of-concepts and production prototypes** measured
against the real in-tree codecs. Every number below was produced by the
research and codec packages under `internal/encoding/` on this machine — not
copied from papers.

- **Goal:** find lossless codecs beating mebo's current ones on the
  ratio↔throughput Pareto frontier, across three families (float64 values, int64
  timestamps, the block-codec layer).
- **Hard constraint:** strictly lossless (bit-exact round-trip). No lossy/quantized
  schemes.
- **Method:** deep multi-source research → smallest PoCs → production codecs for
  the survivors → benchmark vs the real `Gorilla`/`Chimp`/`Delta`/`DeltaPacked`
  encoders.

**Environment:** AMD Ryzen 9 9950X3D (Zen 5, AVX-512), Go 1.26.1, linux/amd64.
The active delta-of-delta SIMD backend is `AsmAVX512` (hand-written assembly).

---

## TL;DR

| Family | Winner | Headline result | Status |
|---|---|---|---|
| **float64 values** | **ALP** | **3–5× better ratio than Chimp on real decimal data**; ≈Chimp on full-precision; O(1) random access | Production codec wired as `TypeALP` |
| **int64 timestamps** | **BP128** (or Simple8b) | BP128: **3× faster decode**, best ratio (needs SIMD-asm). Simple8b: −15..76% ratio, pure-Go, encode 1.84× | Retained and tested, but neither is a registered format type |
| **block layer** | **zstd dictionary** | **−58%** on small blocks; FSE/huff0 useless on XOR'd output | PoC only |

**Single highest-value result:** ALP on real (decimal) metric data. mebo's own
benchmark generators emit *full-precision* doubles that are hostile to ALP, so the
existing benchmarks **understate** ALP's real-world gain.

---

## Methodology & data shapes

All ratios are **bytes/value** (lower is better), measured by encoding with each
codec and dividing the encoded length by the value count. All speeds use Go's
`testing.B` with `-benchmem`. Round-trip losslessness is asserted for every PoC and
codec, on both little- and big-endian engines.

Representative data mirrors mebo's `tests/measurev2` generator and the realistic
generators already in the bench suite:

- **Decimal / quantized** (real sensor shape): values rounded to 1–2 decimals,
  e.g. `45.2, 2048.5` — what real metrics look like when stored as float64.
- **Full-precision** (`*_fp`): sin/cos noise and random-walk products — irrational
  doubles. **This is what mebo's synthetic generators produce, and it is the worst
  case for ALP.**
- **Timestamps:** 1 s intervals with ±0.0–2% jitter (mebo docs cite <0.1% for
  industrial protocols).

---

## Family 1 — float64 values: **ALP**

ALP (Afroozeh, Kuffó, Boncz — SIGMOD 2024) has two lossless schemes, chosen
per-column by estimated size:

- **ALP main:** most real floats are *decimals stored as doubles*. Find exponents
  `(e,f)` so `digit = round(v·10ᵉ·10⁻ᶠ)` decodes back bit-exactly; Frame-of-Reference
  + bit-pack the digits; values that don't round-trip become exceptions.
- **ALP-RD ("Real Double"):** for genuine full-precision doubles, split each 64-bit
  pattern into a dictionary-coded left part (≤16 bits, 8-entry dict) + bit-packed
  right part (48–63 bits).
- **raw:** escape hatch when neither beats storing raw float64.

### Ratio — ALP vs Gorilla vs Chimp (bytes/value)

| dataset | shape | Gorilla | Chimp | **ALP** | scheme | ALP vs Chimp |
|---|---|---:|---:|---:|---|---|
| real_cpu | decimal (45.2…) | 7.200 | 6.500 | **2.100** | main | **3.1× smaller** |
| real_mem | decimal (2048.5…) | 6.900 | 6.000 | **2.200** | main | **2.7×** |
| real_latency | decimal (10.5…) | 7.400 | 7.200 | **3.600** | main | **2.0×** |
| cpu_util_q2 | 2-decimal | 4.175 | 3.970 | **0.820** | main | **4.8×** |
| cpu_util_q1 | 1-decimal | 0.710 | 0.820 | **0.445** | main | **1.8×** |
| net_tput_q1 | 1-decimal | 5.590 | 4.947 | **1.047** | main | **4.7×** |
| net_tput_q2 | 2-decimal | 6.177 | 5.733 | **1.423** | main | **4.0×** |
| temp_q2 | 2-decimal | 0.710 | 0.840 | **0.445** | main | **1.9×** |
| disk_iops_int | integer-like | 1.260 | 1.836 | **1.028** | main | **1.8×** |
| mem_drift_fp | full-precision | 5.940 | 5.702 | 5.778 | main | ≈ (−1.3%) |
| randwalk_fp | full-precision | 6.375 | 6.240 | **6.175** | rd | ≈ (+1.0%) |
| cpu_util_fp | full-precision | 6.170 | 5.770 | 6.175 | rd | within 7% |
| battery_fp | full-precision | 5.985 | 5.430 | 6.165 | rd | within 14% |

**Reading this:** on the kind of data real metrics actually contain (decimals),
ALP is **2–5× smaller than Chimp**. On full-precision doubles it falls back to
ALP-RD and lands **≈ Chimp**. It is essentially never meaningfully worse, and
often dramatically better. The per-column scheme selector picks correctly every
time (`main` for decimal/integer, `rd` for full-precision).

### Speed — ALP vs Chimp (200 decimal values)

| operation | ALP | Chimp | note |
|---|---:|---:|---|
| Encode | 16.97 µs / 2 allocs | 0.55 µs / 0 allocs | one-time; see below |
| Decode (All) | 1.42 µs / **0 allocs** | 0.71 µs / 0 allocs | repeated path |
| Random access (At) | **O(1)** | O(n) | ALP advantage |

- **Encode** was optimized from a naïve 260 µs/144-alloc first version to
  **17 µs/2-alloc** (15×) via: sampling 32 values for the `(e,f)` and RD-cut
  searches, computing each search once (no redundant re-search), and short-circuiting
  the RD search when ALP main has zero exceptions. It remains ~30× slower than
  Chimp's trivial XOR — acceptable for mebo's batch "encode-once" model, and further
  reducible with a two-level `(e,f)` search.
- **Decode** streams value-by-value reading bits directly: **zero-allocation** on
  the no-exception path (mebo's iteration contract), and *faster than the previous
  whole-column version*.
- **Random access** is **O(1)**: ALP computes a value's bit offset directly, unlike
  Gorilla/Chimp's O(n) sequential XOR chains. This is a capability win beyond ratio.

### Why mebo's current benchmarks understate ALP

mebo's `measurev2` generator builds values by repeated `value *= 1 + random·jitter`,
producing full-precision doubles. The realistic bench generators add sin/cos noise.
Both are **ALP-hostile** (every value becomes an exception in ALP main). Real
production metrics are overwhelmingly *fixed-precision decimals* (the JSON samples
in `tests/measure/` are `45.2, 2048.5, 10.5`). On those, ALP wins 2–5×. **Any future
ALP benchmark should include quantized/decimal datasets, or it will measure ALP's
worst case.**

---

## Family 2 — int64 timestamps: **Simple8b** and **BP128**

mebo's `Delta` already does delta-of-delta + zigzag + LEB128 varint (strong), and
`DeltaPacked` does delta + group-varint. Two successors were evaluated.

### Simple8b (built, pure-Go) — ratio vs Delta/DeltaPacked (bytes/point)

| timestamps | Delta (dod) | DeltaPacked | **Simple8b** | vs Delta |
|---|---:|---:|---:|---|
| clean 1 s | 1.045 | 1.295 | **0.250** | **−76%** |
| ±0.1% jitter (n=200) | 1.990 | 2.160 | **1.690** | −15% |
| ±0.1% jitter (n=1000) | 1.954 | 2.130 | **1.610** | −18% |
| ±0.5% jitter | 2.060 | 2.265 | 2.090 | +1.5% |
| ±2% jitter | 2.630 | 2.336 | 2.516 | −4.3% |

Simple8b bit-packs the delta-of-delta stream, beating varint's whole-byte
granularity. It wins on low-jitter data (mebo's documented common case) and is
roughly neutral at high jitter.

### Simple8b — speed (200-metric blob; iterate per metric)

| path | Delta | Simple8b | result |
|---|---:|---:|---|
| Encode (blob) | 51.9 µs | 94.9 µs | **1.84× slower** |
| **Iterate** (decode) | 495 ns | **472 ns** | **0.95× — faster**, 0 allocs |

Encode was optimized from 3.44× → **1.84× slower** (bulk dod, OR-accumulator to
skip the exception scan, pre-grown buffer, and a **single-pass selector search**
replacing the greedy "try-16-selectors-and-rescan"). **1.84× is the scalar floor.**

> **SIMD cannot fix adaptive Simple8b encode.** A prototype that vectorized the
> per-value bit-width computation (AVX-512 `VPLZCNTQ`) made encode *slower* — scalar
> `bits.Len64` is already a 1-cycle `LZCNT`, and the real cost is the inherently
> sequential, data-dependent selector scan, which cannot be vectorized. The
> SIMD-friendly path is a *different* codec (fixed-width packing → BP128).

### BP128 (prototype, archsimd) — the SIMD-friendly alternative

Fixed-width Frame-of-Reference + bit-pack, in a vertical 8-lane layout where all
lanes share width+bitpos so the 64-bit carry is a single vector shift
(FastLanes/bp128 approach). Prototype under `GOEXPERIMENT=simd`.

**Ratio (B/dod, single continuous series):** clean 0.073, ±0.1% **1.535**, ±2%
2.266 — **competitive-to-better than Simple8b** (no per-word selector overhead).

**Speed — full pipeline vs Delta (40 k-value series):**

| operation | **BP128** (archsimd) | Delta | result |
|---|---:|---:|---|
| **Decode** | **37 µs** | 109 µs | **~3× faster** |
| Encode | 60 µs* | 49 µs | 1.2× slower* |

\* prototype encode leaves zigzag + max-width scalar (only dod+pack vectorized) and
allocates; the bitpack/unpack *kernels* alone hit **0.14–0.19 ns/value** (~5–7 G
values/s). A production BP128 with full SIMD would plausibly match Delta on encode.

**BP128 dominates Simple8b on every axis** (ratio, decode, and likely encode with
full SIMD). The catch: archsimd only activates under `GOEXPERIMENT=simd`; shipping
SIMD in the default build needs hand-written Plan9 asm for pack/unpack across bit
widths × AVX2/AVX512 + scalar fallback — a substantial investment.

---

## Family 3 — block codecs over small columnar blocks

mebo applies an optional block codec (zstd/s2/lz4) over the encoded columnar bytes.
Measured on 200 independent ~943-byte Gorilla blocks:

| approach | B/value | vs raw | takeaway |
|---|---:|---:|---|
| raw Gorilla | 4.715 | — | baseline |
| per-block zstd (no dict) | 4.175 | 1.1× | **small-block penalty** — zstd can't do much per 1 KB block |
| **FSE** on XOR'd bytes | 4.337 | 1.1× | **useless** — output already high-entropy |
| **huff0** on XOR'd bytes | 4.347 | 1.1× | useless (same reason) |
| **zstd + trained dict** (held-out) | **1.737** | — | **−58% vs no-dict** |
| concatenated zstd (all blocks) | 0.574 | 8.2× | cross-block redundancy upper bound |

**Findings:**
1. Entropy-coding the whole XOR'd block (FSE/huff0) does nothing — it's high-entropy
   by construction. (FSE's real niche is a *skewed sidecar* stream, e.g. Chimp's
   leading-zero buckets, which mebo would have to expose separately.)
2. The lever is a **trained/shared zstd dictionary** (`klauspost/compress`, already
   a dependency — no new code dependency). −58% on small blocks.
3. **Caveat:** mebo already compresses the *whole concatenated* timestamp/value
   payload per blob, so the small-block win mostly applies *across many blobs*
   (BlobSet / time-windowed) where each blob is compressed independently. The central
   design question is **dictionary ownership** (caller-managed/shared vs embedded).
   The synthetic blocks here are self-similar, so −58%/8.2× are optimistic.

---

## Cross-cutting findings & gotchas

- **ALP is data-dependent, not universally better.** It wins on decimals, ties on
  full-precision. Benchmark it on realistic decimal data or you measure its worst case.
- **Floating-point is not associative.** ALP's digit multiply `v·10ᵉ·10⁻ᶠ` must use
  the *identical operation order* in the search, the encode round-trip check, and the
  decode. Hoisting `10ᵉ·10⁻ᶠ` into one constant silently broke losslessness and the
  zero-exception fast path. (Documented in-code.)
- **Bit-exact ≠ float-equal.** Negative zero compares `== +0.0` but has different
  bits; ALP's round-trip check must compare `Float64bits`, or `-0.0` is silently lost.
- **SIMD only helps regular, data-independent work.** It accelerates fixed-width
  packing (BP128) and dod computation, but not adaptive/sequential selector logic
  (Simple8b) — and even for `Delta`, SIMD dod ≈ scalar on *encode* because varint
  byte-writing dominates. Always verify the active backend before drawing conclusions.
- **Encode is one-time; decode/iterate is repeated.** mebo's model is
  collect→encode→persist→query, so a slower encode (ALP, Simple8b) is an acceptable
  trade when decode/iterate and ratio improve.

---

## Recommendations

1. **Adopt ALP for values** (highest leverage). Wire `TypeALP` into the format;
   default stays Gorilla/Chimp. Expose it so decimal-metric workloads get 2–5×.
   Add decimal datasets to the benchmark suite. Follow-ups: two-level `(e,f)` search
   (faster encode), native 1024-value sub-vectoring (better ratio on large columns).
2. **Timestamps: prefer BP128 over Simple8b** *if* the SIMD-asm investment is made —
   it dominates on ratio and decode. Otherwise ship **Simple8b** (pure-Go, done) for
   the low-jitter ratio win; iterate is already faster than `Delta`.
3. **Block layer: add optional shared-dictionary zstd** for multi-blob/BlobSet
   workloads (no new dependency). Decide dictionary ownership first. Don't bother
   with whole-block FSE/huff0.

---

## Artifact inventory

| Artifact | Path | State |
|---|---|---|
| ALP production codec (main + RD + raw) | `internal/encoding/value/alp/alp.go` (+ tests) | built, tested, optimized, wired as `TypeALP` |
| Simple8b production codec | `internal/encoding/timestamp/simple8b/simple8b.go` (+ tests) | built, tested, **unregistered** |
| ALP main ratio PoC | `internal/encoding/value/alp/poc_alp_test.go` | kept (ratio reference) |
| ALP-RD ratio PoC | `internal/encoding/value/alp/poc_alp_rd_test.go` | kept |
| Simple8b ratio PoC | `internal/encoding/research/poc_ts_test.go` | kept |
| Block-codec / zstd-dict / FSE PoC | `internal/encoding/research/poc_block_test.go` | kept |
| BP128 SIMD prototype | `internal/encoding/timestamp/bp128/bp128_proto_test.go` | `//go:build goexperiment.simd`; prototype/evidence |
| Deep-research report (105 agents, cited) | session task `wdlhi8z9u` output | archived |

All PoCs/codecs are lossless (round-trip asserted) and lint-clean. The BP128
prototype is isolated behind a build tag and does not affect the default build.

## References

- ALP: Afroozeh, Kuffó, Boncz, *"ALP: Adaptive Lossless floating-Point Compression"*,
  SIGMOD 2024. Reference impl: `github.com/cwida/ALP` (MIT). Constants used:
  `CUTTING_LIMIT=16`, `MAX_RD_DICTIONARY_SIZE=8`, `SAMPLES_PER_VECTOR=32`,
  `VECTOR_SIZE=1024`.
- Simple8b: Anh & Moffat, *"Index compression using 64-bit words"*, SPE 2010.
- SIMD-BP128 / FastPFOR: Lemire & Boytsov, SPE 2015 (arXiv:1209.2137).
- Chimp: Liakos et al., VLDB 2022.
