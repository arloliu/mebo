# AVX-512 Group Varint Packed Decoder (SIMD Plan Phase 3)

| | |
|---|---|
| **Date** | 2026-06-13 |
| **Platform** | AMD Ryzen 9 9950X3D (Zen 5, full 512-bit datapath), linux/amd64, Go 1.26.1 |
| **Scope** | `internal/encoding` delta-packed timestamp decode backend |
| **Format impact** | None — decode-side backend; parity-tested against scalar |
| **Plan** | [SIMD_OPTIMIZATION_PLAN.md Phase 3](../SIMD_OPTIMIZATION_PLAN.md#phase-3-avx-512-packed-decoder) |

## Summary

Adds a third decode backend for Group Varint packed timestamps
(`deltaPackedDecodeBackendAsmAVX512`), selected by default on CPUs with
AVX-512 F+BW+VBMI (detected via the new `arch.X86HasAVX512VBMI`). The kernel
(`decodeDeltaPackedASMAVX512BulkPairs`) processes **two groups (8 values) per
iteration**:

- One 64-byte ZMM load covers both payloads and the in-stream second control
  byte; pairs whose combined payload exceeds the window (both groups
  near-maximal width — absent in realistic timestamp data) exit to the AVX2
  path.
- A single **zeroing-masked VPERMB** replaces AVX2's two-pass
  VPSHUFB(LoDup)+VPSHUFB(HiDup)+VPOR. VPERMB has no 0x80 zeroing sentinel, so
  a new 256-entry `deltaPackedDecodeValidMasks` table supplies a 64-bit
  k-register per control-byte pair; the existing VPSHUFB index table is
  reused with the second group's indices offset by `totalBytes[cb0]+1` via
  masked VPADDB.
- Zigzag decode uses **VPSRAQ** (64-bit arithmetic shift, unavailable in
  AVX2 — the old kernel emulated it with VPCMPGTQ).
- Delta accumulation runs as **two 8-wide prefix sums** (3× VALIGNQ+VPADDQ
  each); the timestamp/delta carries stay broadcast in ZMM registers across
  iterations (VPERMQ lane-7 broadcast), so the loop has no scalar
  accumulation at all.

The Go wrapper chains any remainder (odd trailing group, fat pair, end-of-data
window) to the existing AVX2 path, which retains its per-group scalar
fallback. Non-VBMI CPUs keep AVX2; non-amd64 keeps scalar.

## Results

### Kernel level (`BenchmarkTimestampDeltaPackedDecoder_*_Backends`)

| Benchmark | Scalar | AVX2 | AVX-512 | AVX-512 vs AVX2 |
|---|---|---|---|---|
| DecodeAll 10k values | — | 6,417 ns | 4,694 ns | **1.37×** |
| DecodeAll 1k values | — | 653 ns | 487 ns | **1.34×** |
| All iterator 10k | 23,178 ns | 17,254 ns | 15,724 ns | 1.10× |

0.47 ns/value bulk decode (vs the plan's ~0.42 target; the residual gap is the
serial carry-broadcast chain between iterations).

### Product level

| Path | AVX2 | AVX-512 | Δ |
|---|---|---|---|
| `Materialize()` 100×5000 deltapacked blob | 715 µs | 627 µs | **−13%** |
| `Materialize()` 50×1000 deltapacked blob | ~112 µs | ~112 µs | unchanged |
| measurev2 iterate (200-pt metrics, all deltapacked combos) | — | — | unchanged (±2%) |

The honest scope of the win: **bulk decode paths** — `DecodeAll`,
`decodeTimestampsSlice` (materialization, shared-timestamp pre-decode) — and
larger blobs. The measurev2 `All()` iterate numbers do not move because at
200 points/metric the per-element yield overhead dominates and the XOR fused
paths decode group varint scalar per element by design.

## Verification

- All existing backend-parametrized tests automatically cover the new
  backend: parity vs scalar (regular/jitter/jumps/mixed-width/all-1-byte/
  all-8-byte data), exhaustive control bytes, truncated input, SIMD threshold
  boundaries, All-iterator equivalence. All pass on first run.
- Unsupported CPUs skip the backend in tests (`deltaPackedDecodeBackendSupported`).
- Full test suite (including `-race` on the package) and `make lint` pass.
- New permanent benchmarks: `BenchmarkMaterialize/{Medium,Large}_DeltaPacked-Raw_NoTags`.

## Remaining headroom

- 4-group (16-value) iterations would amortize the table lookups further but
  need a 128-byte window and double-length k-mask plumbing; the carry chain
  (VPERMQ broadcast → VPADDQ) stays serial regardless, capping returns.
- The All iterator at small counts is yield-bound, not decode-bound — the
  ForEach callback API is the lever there, not wider SIMD.
