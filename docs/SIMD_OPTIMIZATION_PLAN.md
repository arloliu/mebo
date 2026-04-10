# SIMD Optimization Plan — Toward World-Class Encoding Performance

## Date

2026-04-10

## Benchmark Platform

- CPU: AMD Ryzen 9 9950X3D (32 threads)
- OS: Linux 6.17.0-14-generic (amd64)
- Go: 1.26.0 (GOEXPERIMENT=simd available)

## Table of Contents

- [Executive Summary](#executive-summary)
- [Current State](#current-state)
- [Root-Cause Analysis: The Encoder Bottleneck](#root-cause-analysis-the-encoder-bottleneck)
- [Phase 1: Fused SIMD Group Varint Encode](#phase-1-fused-simd-group-varint-encode)
- [Phase 2: SIMD Prefix Sum for Decode Accumulation](#phase-2-simd-prefix-sum-for-decode-accumulation)
- [Phase 3: AVX-512 Packed Decoder](#phase-3-avx-512-packed-decoder)
- [Phase 4: SIMD Delta Decode for Unpacked Format](#phase-4-simd-delta-decode-for-unpacked-format)
- [Phase 5: Gorilla/Chimp BMI2 Acceleration](#phase-5-gorillachimp-bmi2-acceleration)
- [Phase 6: Fused Batch Decoder](#phase-6-fused-batch-decoder)
- [Phase 7: Cache and Memory Optimizations](#phase-7-cache-and-memory-optimizations)
- [Appendix A: Current Benchmark Baselines](#appendix-a-current-benchmark-baselines)
- [Appendix B: Reference Material](#appendix-b-reference-material)

## Executive Summary

The recent SIMD work (commits `2163b91`, `55742af`, `93f71d8`) introduced AVX2/AVX-512 acceleration
for delta-of-delta timestamp computation and AVX2 Group Varint packed decoding. The SIMD kernels
achieve 2.4-3.7x speedups in isolation. However, end-to-end encoder throughput improves only 3-8%
because **87% of encode time is spent in the scalar zigzag + serialization loop**, not in the
delta-of-delta computation.

This plan identifies 7 phases of optimization that collectively target **3-5x full-pipeline encode
speedup** and **2-3x decode speedup** beyond today's numbers. Phases are ordered by
impact-to-complexity ratio.

## Current State

### SIMD Coverage Matrix

| Component                  | Encode SIMD   | Decode SIMD      | Notes                          |
|----------------------------|---------------|------------------|--------------------------------|
| Delta-of-delta computation | AVX2, AVX-512 | N/A              | `ts_delta_simd_amd64.s`        |
| Delta varint serialization | None          | None             | `appendUnsigned()` is scalar   |
| Group Varint packing       | None          | AVX2             | `ts_delta_packed_simd_amd64.s` |
| Varint decode (unpacked)   | N/A           | None             | `decodeVarint64()` is scalar   |
| Delta chain accumulation   | N/A           | Scalar per-group | Sequential ADDQ in asm         |
| Gorilla XOR                | None          | None             | Bit-level, data-dependent      |
| Chimp XOR                  | None          | None             | Bit-level, data-dependent      |

### File Reference

| File                                             | Lines   | Role                                       |
|--------------------------------------------------|---------|--------------------------------------------|
| `internal/encoding/ts_delta_simd.go`             | 202     | DoD backend dispatch                       |
| `internal/encoding/ts_delta_simd_amd64.s`        | 140     | AVX2/AVX-512 DoD kernels                   |
| `internal/encoding/ts_delta_packed_simd.go`      | 301     | Packed decode dispatch + tables            |
| `internal/encoding/ts_delta_packed_simd_amd64.s` | 250     | AVX2 packed decode kernel                  |
| `internal/encoding/ts_delta_packed.go:260-325`   | 65      | `flushGroup()` — current encode bottleneck |
| `internal/encoding/ts_delta_packed.go:162-198`   | 36      | `WriteSlice()` SIMD + scalar loop          |
| `internal/encoding/ts_delta.go:228-254`          | 26      | Delta encoder SIMD + scalar loop           |
| `internal/encoding/ts_delta.go:256-271`          | 15      | `appendUnsigned()` — varint serialization  |
| `internal/encoding/ts_delta.go:524-566`          | 42      | `DecodeAll()` — scalar varint decode       |
| `internal/encoding/fused.go`                     | ~250    | Fused timestamp+value decoder              |
| `internal/arch/`                                 | 5 files | CPU capability detection                   |

## Root-Cause Analysis: The Encoder Bottleneck

### Measured Breakdown (10,000 timestamps)

```
DeltaPacked Encoder, Scalar backend:     16,232 ns total
DeltaPacked Encoder, AVX-512 backend:    15,026 ns total  (7.4% faster)

DoD kernel alone at 10k (AVX-512):          922 ns
DoD kernel alone at 10k (Scalar):         2,731 ns
```

The SIMD kernel saves **1,809 ns**, but the full pipeline only saves **1,206 ns** (some overhead
from buffer chunking). The remaining ~14,100 ns is:

```
Per-element zigzag:                ~200 ns   (trivial)
Per-element encodeTag (4 branches): ~800 ns
flushGroup (serial pack + write):  ~13,100 ns  <-- dominant cost
```

The `flushGroup()` function at `ts_delta_packed.go:260` is called once per 4 values. For 10k
timestamps, that's 2,500 calls. Each call:
1. Computes 4 tags via `encodeTag()` (4 branch-chains: 3 comparisons each)
2. Builds control byte (4 shifts + 3 ORs)
3. Sums `groupVarintLengths` for allocation (4 table lookups + 3 adds)
4. Grows buffer
5. Writes 4 `PutUint64` + advances offset by tag-dependent widths

Steps 1-3 are branchful scalar work that SIMD can eliminate. Steps 4-5 involve
buffer management overhead that batch processing can amortize.

### Key Insight

The current architecture computes DoDs in SIMD batches of 256, then falls back to a **per-element**
zigzag + per-4-element `flushGroup` loop (`ts_delta_packed.go:170-178`). The SIMD batch output is
consumed one-by-one. The fix is to fuse the entire pipeline: DoD + zigzag + tag-classify + pack,
all in one SIMD pass.

---

## Phase 1: Fused SIMD Group Varint Encode

**Impact: 2-3x full encode speedup** | Complexity: Medium | Priority: Highest

### Goal

Replace the per-element zigzag + `flushGroup()` loop with a SIMD kernel that processes 4 values
(one group) per vector iteration: delta-of-delta already computed, now zigzag, classify widths,
build control byte, and pack output — all in a single call to assembly.

### Design

#### New Function Signature

```go
// encodeDeltaPackedGroupsASMAVX2 encodes delta-of-delta values (already computed)
// into Group Varint format directly.
//
// Parameters:
//   - dst: output buffer (must have capacity for control bytes + payload)
//   - deltasOfDeltas: pre-computed delta-of-delta int64 values
//   - nGroups: number of 4-value groups to encode
//
// Returns:
//   - bytesWritten: total bytes written to dst
func encodeDeltaPackedGroupsASMAVX2(dst []byte, deltasOfDeltas []int64, nGroups int) int
```

#### Algorithm (per group of 4 int64 values)

```
Step 1: Zigzag encode (SIMD, branchless)
  VMOVDQU  (src), Y0            ; load 4 × int64 DoDs
  VPSLLQ   $1, Y0, Y1           ; Y1 = DoD << 1
  VPSRAQ   $63, Y0, Y2          ; Y2 = DoD >> 63 (arithmetic)
  VPXORQ   Y1, Y2, Y0           ; Y0 = zigzag encoded

Step 2: Width classification (SIMD, branchless)
  ; For each lane, determine minimum byte width: 1, 2, 4, or 8
  ; Use VLZCNTQ (AVX-512) or BSR cascade (AVX2) to find highest set bit
  ;
  ; AVX2 approach: compare against thresholds
  VPCMPGTQ  ymm_0xFF, Y0, Y3    ; mask: needs > 1 byte?
  VPCMPGTQ  ymm_0xFFFF, Y0, Y4  ; mask: needs > 2 bytes?
  VPCMPGTQ  ymm_0xFFFFFFFF, Y0, Y5 ; mask: needs > 4 bytes?
  ; tag = popcount of (mask3, mask4, mask5) per lane → 0,1,2,3
  ;
  ; AVX-512 approach: VLZCNTQ → shift to get byte width directly
  VLZCNTQ  Z0, Z1               ; leading zero count per lane
  VPSRLQ   $3, Z1, Z1           ; divide by 8 → unused byte count
  ; width = 8 - unusedBytes, then map to tag: 1→0, 2→1, 4→2, 8→3

Step 3: Build control byte
  ; Extract 4 tags to scalar, shift and OR into single byte
  ; Use VPMOVQB (AVX-512) or VPEXTRD + shifts (AVX2)

Step 4: Lookup totalBytes from deltaPackedDecodeTotalBytes[controlByte]
  ; Single byte lookup, already computed at init time

Step 5: Pack values into output
  ; Use inverse of the decode shuffle table (precomputed at init)
  ; OR: branchless PutUint64 + advance by tag width (current approach, but batched)
  ;
  ; Option A — VPSHUFB inverse (needs new table, ~8KB):
  ;   Build a 256-entry "encode shuffle" table, inverse of decode table.
  ;   VPSHUFB compacts 4×int64 lanes into variable-width packed format.
  ;   Single VMOVDQU to write, then trim.
  ;
  ; Option B — Scalar pack with batched buffer management:
  ;   After SIMD zigzag + tag classification, fall to scalar PutUint64 loop
  ;   but with pre-computed total size (no per-group Grow).
  ;   Simpler but less gain.
```

#### Recommended Approach: Hybrid (SIMD zigzag + classify, scalar pack)

Option A (full SIMD pack) is the ideal end-state but requires a new 8KB inverse shuffle table and
careful handling of the variable output width. Start with Option B:

1. **Batch outer loop**: Process 64 groups (256 values) at a time
2. **SIMD inner loop**: Zigzag encode + tag classify all 256 values → produce 64 control bytes +
   64 totalBytes counts in a single pass
3. **Pre-allocate**: Sum all 64 totalBytes + 64 control bytes → single `buf.Grow()`
4. **Scalar pack**: Write control byte + PutUint64-per-value with pre-known widths (no
   per-group buffer management)

This eliminates:
- Per-group `Grow()` calls (2,500 → ~39 for 10k values)
- Per-element `encodeTag()` branch chains (SIMD threshold comparison replaces 3 branches per value)
- Per-element zigzag in Go (moved to SIMD)

#### Files to Create/Modify

| File                                        | Action | Description                                           |
|---------------------------------------------|--------|-------------------------------------------------------|
| `ts_delta_packed_simd_encode.go`            | Create | Dispatch + Go wrappers for encode SIMD                |
| `ts_delta_packed_simd_encode_amd64.s`       | Create | AVX2 zigzag + classify kernel                         |
| `ts_delta_packed_simd_encode_stub.go`       | Create | Scalar fallback stub                                  |
| `ts_delta_packed.go:162-198`                | Modify | Wire SIMD encode path into `WriteSlice()`             |
| `ts_delta_packed.go:260-325`                | Modify | Extract batch flushGroup for SIMD pre-classified data |
| `ts_delta_packed_simd_encode_test.go`       | Create | Parity tests (SIMD vs scalar)                         |
| `ts_delta_packed_simd_encode_bench_test.go` | Create | Before/after benchmarks                               |

#### Integration Point

In `WriteSlice()` at `ts_delta_packed.go:162-198`, the current code does:

```go
for len(remaining) > 0 {
    n := min(len(remaining), deltaOfDeltaSIMDChunkSize)
    prevTS, prevDelta = deltaOfDeltaIntoActive(deltaBuf[:n], remaining[:n], prevTS, prevDelta)

    for _, deltaOfDelta := range deltaBuf[:n] {
        zigzag := uint64((deltaOfDelta << 1) ^ (deltaOfDelta >> 63))
        e.pending[e.pendingLen] = zigzag
        e.pendingLen++
        if e.pendingLen == groupSize {
            e.flushGroup(groupSize)
        }
    }
    remaining = remaining[n:]
}
```

Replace with:

```go
for len(remaining) > 0 {
    n := min(len(remaining), deltaOfDeltaSIMDChunkSize)
    prevTS, prevDelta = deltaOfDeltaIntoActive(deltaBuf[:n], remaining[:n], prevTS, prevDelta)

    nGroups := n / groupSize
    if nGroups > 0 && shouldUseDeltaPackedEncodeSIMD(nGroups) {
        written := encodeDeltaPackedGroupsActive(e.buf, deltaBuf[:nGroups*groupSize], nGroups)
        e.buf.B = e.buf.B[:len(e.buf.B)+written]
    }

    // Handle tail (< 4 values) via existing scalar path
    for i := nGroups * groupSize; i < n; i++ {
        zigzag := uint64((deltaBuf[i] << 1) ^ (deltaBuf[i] >> 63))
        e.pending[e.pendingLen] = zigzag
        e.pendingLen++
        if e.pendingLen == groupSize {
            e.flushGroup(groupSize)
        }
    }
    remaining = remaining[n:]
}
```

#### Target Performance

| Metric                                   | Current          | Target          | Improvement |
|------------------------------------------|------------------|-----------------|-------------|
| DeltaPacked encode 10k (scalar baseline) | 16,232 ns        | ~7,000 ns       | 2.3x        |
| DeltaPacked encode 10k (AVX-512 DoD)     | 15,026 ns        | ~5,500 ns       | 2.7x        |
| Throughput (10k values)                  | 0.62 M values/μs | 1.5 M values/μs | 2.4x        |

---

## Phase 2: SIMD Prefix Sum for Decode Accumulation

**Impact: 1.3-1.5x decode speedup** | Complexity: Low-Medium | Priority: High

### Problem

The packed decoder's AVX2 path (`ts_delta_packed_simd_amd64.s:85-103`) decodes zigzag values into
a stack buffer via VPSHUFB, then **sequentially** accumulates the delta chain:

```asm
MOVQ 0(SP), AX       ; load DoD[0]
ADDQ AX, R12         ; delta += DoD[0]
ADDQ R12, R11        ; ts += delta
MOVQ R11, (DI)       ; store ts[0]
MOVQ 8(SP), AX       ; load DoD[1]
ADDQ AX, R12         ; delta += DoD[1]
...
```

This 4-step serial chain (8 dependent ADDQs) is the inner-loop bottleneck after VPSHUFB. With
4 groups buffered (16 DoDs), a SIMD prefix sum can replace 32 sequential ADDQs with ~8 SIMD ops.

### Design

#### Prefix Sum for 4 int64 values (AVX2)

The delta chain has two levels of accumulation:
- Level 1: `delta[i] = delta[i-1] + DoD[i]` (prefix sum over DoDs)
- Level 2: `ts[i] = ts[i-1] + delta[i]` (prefix sum over deltas)

For 4 values in a YMM register, inclusive prefix sum requires 2 steps:

```
Input:   [a, b, c, d]

Step 1: shift-1 + add
  shift:  [0, a, b, c]
  result: [a, a+b, b+c, c+d]

Step 2: shift-2 + add
  shift:  [0, 0, a, a+b]
  result: [a, a+b, a+b+c, a+b+c+d]
```

Implementation:

```asm
; Y0 = [DoD0, DoD1, DoD2, DoD3]
; R12 = prevDelta (carry-in)

; Add carry-in to first element
VPBROADCASTQ R12, Y1        ; Y1 = [prevDelta, prevDelta, prevDelta, prevDelta]
VMOVDQU      shiftMask1, Y2 ; mask to zero-shift by 1 lane
VPERMQ       $0x39, Y0, Y3  ; Y3 = [DoD1, DoD2, DoD3, 0] (rotate)
; ... (prefix sum steps)

; After prefix sum: Y0 = [delta0, delta1, delta2, delta3]
; Repeat for ts accumulation with prevTS carry-in
```

#### Multi-Group Batching

Process 4 groups (16 values) before accumulating:
1. VPSHUFB + zigzag decode group 0 → store to scratch[0:4]
2. VPSHUFB + zigzag decode group 1 → store to scratch[4:8]
3. VPSHUFB + zigzag decode group 2 → store to scratch[8:12]
4. VPSHUFB + zigzag decode group 3 → store to scratch[12:16]
5. Load all 16 values → 4x prefix sum → store 16 timestamps

This increases the ratio of SIMD to scalar work and amortizes the carry-in/carry-out overhead.

#### Files to Modify

| File                                 | Action | Description                                                     |
|--------------------------------------|--------|-----------------------------------------------------------------|
| `ts_delta_packed_simd_amd64.s`       | Modify | Add prefix-sum accumulation after VPSHUFB                       |
| `ts_delta_packed_simd.go`            | Modify | Adjust `decodeDeltaPackedASMAVX2BulkGroups` signature if needed |
| `ts_delta_packed_simd_bench_test.go` | Modify | Add before/after comparison                                     |

#### Target Performance

| Metric                         | Current   | Target     | Improvement |
|--------------------------------|-----------|------------|-------------|
| Packed DecodeAll 10k (AVX2)    | 6,483 ns  | ~4,800 ns  | 1.35x       |
| Packed All iterator 10k (AVX2) | 17,494 ns | ~13,500 ns | 1.3x        |

---

## Phase 3: AVX-512 Packed Decoder

**Impact: 1.5x over AVX2 decode** | Complexity: Medium | Priority: Medium-High

### Problem

The packed decoder only has AVX2 and Scalar backends. AVX-512 is available on the target CPU
but unused for Group Varint decoding.

### Design

AVX-512 brings three advantages for packed decoding:

1. **VPERMB**: True byte-granular permute across all 64 bytes (no 128-bit lane crossing
   limitation of VPSHUFB). This eliminates the current LoDup/HiDup two-pass shuffle strategy.

2. **VLZCNTQ**: Hardware leading-zero count for all 8 int64 lanes, useful for width
   classification during encode.

3. **Double throughput**: Process 2 groups per iteration (8 values) in a single ZMM register.

#### Two-Group-Per-Iteration Strategy

```
Iteration:
  1. Load 2 control bytes: cb0, cb1
  2. Compute combined payload size: totalBytes[cb0] + totalBytes[cb1]
  3. Load up to 64 payload bytes into Z0
  4. Build a 64-byte VPERMB mask from both control bytes
     - Lanes 0-3: group 0 values (from cb0 shuffle table)
     - Lanes 4-7: group 1 values (from cb1 shuffle table, offsets adjusted)
  5. VPERMB Z_mask, Z0, Z1  → 8 zero-extended int64 values
  6. SIMD zigzag decode: VPSLLQ/VPSRAQ/VPXORQ
  7. 8-wide prefix sum for delta accumulation (3 shuffle+add steps)
  8. Store 8 timestamps
```

This doubles values-per-iteration from 4 to 8, reduces loop overhead by 50%, and replaces the
2-pass VPSHUFB+VPOR with a single VPERMB.

#### New Lookup Table

Create a 256-entry VPERMB mask table (64 bytes per entry = 16KB) indexed by control byte.
Each entry positions 4 values from their variable-width source into 4 zero-extended int64 lanes
within the low 256 bits of a ZMM register.

For the 2-group iteration, combine two table entries: the second entry's source byte indices
are offset by `totalBytes[cb0]`.

#### Files to Create/Modify

| File                                 | Action | Description                                 |
|--------------------------------------|--------|---------------------------------------------|
| `ts_delta_packed_simd.go`            | Modify | Add AVX-512 backend enum, detection, tables |
| `ts_delta_packed_simd_amd64.s`       | Modify | Add `decodeDeltaPackedASMAVX512BulkGroups`  |
| `ts_delta_packed_simd_bench_test.go` | Modify | Add AVX-512 benchmark sub-cases             |
| `ts_delta_packed_simd_test.go`       | Modify | Add AVX-512 parity tests                    |

#### Target Performance

| Metric                  | Current (AVX2) | Target (AVX-512) | Improvement |
|-------------------------|----------------|------------------|-------------|
| Packed DecodeAll 10k    | 6,483 ns       | ~4,200 ns        | 1.5x        |
| Packed All iterator 10k | 17,494 ns      | ~12,000 ns       | 1.5x        |

---

## Phase 4: SIMD Delta Decode for Unpacked Format

**Impact: 1.3-1.5x decode speedup** | Complexity: Low-Medium | Priority: Medium

### Problem

`TimestampDeltaDecoder.DecodeAll()` (`ts_delta.go:524-566`) is fully scalar. At 10k timestamps
with regular 1s intervals, it takes 12,491 ns (6.4 GB/s). The per-value loop:

```go
for produced := 2; produced < count; produced++ {
    deltaZigzag, nextOffset, ok := decodeVarint64(data, offset)
    offset = nextOffset
    deltaOfDelta := decodeZigZag64(deltaZigzag)
    prevDelta += deltaOfDelta
    curTS += prevDelta
    dst[produced] = curTS
}
```

The varint decode (`decodeVarint64` at `ts_delta.go:656`) is inherently serial (variable-length
encoding). However, the delta accumulation after decoding can be vectorized.

### Design: Two-Pass Decode

#### Pass 1: Scalar varint decode → flat DoD buffer

Decode all varints into a flat `[]int64` buffer of delta-of-deltas. This is the serial part —
each varint's length depends on its content — but we eliminate the interleaved accumulation.

#### Pass 2: SIMD prefix sum → timestamps

Reuse the same delta-of-delta SIMD infrastructure from Phase 2. The flat DoD buffer maps directly
to the prefix-sum input: two cascaded prefix sums (DoD→delta, delta→timestamp).

This separation improves IPC: Pass 1 has no dependency chain beyond the offset, and Pass 2 is
pure SIMD arithmetic.

#### Files to Modify

| File                     | Action | Description                           |
|--------------------------|--------|---------------------------------------|
| `ts_delta.go:524-566`    | Modify | Split DecodeAll into 2-pass           |
| `ts_delta_simd.go`       | Modify | Add prefix-sum kernel wrapper         |
| `ts_delta_simd_amd64.s`  | Modify | Add prefix-sum assembly               |
| `ts_delta_bench_test.go` | Modify | Add before/after DecodeAll benchmarks |

#### Alternative: SIMD Varint Decode

If the varint decode itself becomes the bottleneck (after Phase 2 removes the accumulation
overhead), consider SIMD-accelerated varint decoding. The "Masked VByte" technique:

1. Load 16 bytes from the encoded stream
2. Identify continuation bytes (bit 7 set) using PCMPGTB or PMOVMSKB
3. Use the resulting bitmask to index into a decode table that maps byte positions to value
   boundaries

This is a larger undertaking and may warrant a new `DeltaVByte` encoding variant optimized for
SIMD decode (separating length metadata from payload, similar to how DeltaPacked separates the
control byte).

**Recommendation**: Start with the 2-pass approach. Re-evaluate Masked VByte only if varint
decode becomes the dominant cost.

#### Target Performance

| Metric                           | Current   | Target     | Improvement |
|----------------------------------|-----------|------------|-------------|
| Delta DecodeAll 10k (Regular 1s) | 12,491 ns | ~9,000 ns  | 1.4x        |
| Delta All iterator 10k           | 19,646 ns | ~15,000 ns | 1.3x        |

---

## Phase 5: Gorilla/Chimp BMI2 Acceleration

**Impact: 1.5-2x numeric decode speedup** | Complexity: High | Priority: Medium

### Problem

Gorilla and Chimp encoders/decoders are the numeric value codecs. Current decode throughput:

| Dataset (count)    | Gorilla Decode        | Chimp Decode          |
|--------------------|-----------------------|-----------------------|
| cpu_util (150)     | 1,338 ns (8.9 ns/val) | 1,159 ns (7.7 ns/val) |
| mem_usage (500)    | 3,676 ns (7.4 ns/val) | 3,926 ns (7.9 ns/val) |
| latency (1000)     | 2,651 ns (2.7 ns/val) | 3,880 ns (3.9 ns/val) |
| request_rate (800) | 6,022 ns (7.5 ns/val) | 6,185 ns (7.7 ns/val) |

These codecs use per-value bit-level operations with data-dependent branching (unchanged/same-block/
new-block), which appears to preclude SIMD. However, BMI2 instructions can accelerate the
"extract meaningful bits" step:

### Design: BMI2 Bit Extraction

#### Gorilla Decode Hot Path

The current decode reads leading zero count (5 bits) and block length (6 bits) from the
bitstream, then extracts `blockSize` meaningful bits from position `leading`. With BMI2:

```
; Traditional: manual shift + mask to extract bits
SHRQ  CL, RAX        ; shift right by leading zeros
ANDQ  mask, RAX      ; mask to blockSize bits

; BMI2: single PEXT instruction
PEXT  RAX, mask, RDX ; extract bits at positions marked by mask
```

More importantly, `PDEP`/`PEXT` can handle the variable-position bit extraction that currently
requires computed shifts:

```go
// Current Go code (simplified)
meaningful := (bitBuf >> (64 - leading - blockSize)) & ((1 << blockSize) - 1)
value = prevValue ^ (meaningful << trailing)
```

```asm
; BMI2 equivalent
BEXTR  RAX, pos_len, RDX  ; extract blockSize bits starting at position
PDEP   RDX, trailingMask, RDX ; deposit at trailing position
XORQ   prevValue, RDX     ; XOR with previous
```

#### Pre-scan + Batch Decode Architecture

For larger datasets (200+ values), a two-pass approach:

**Pass 1 (Scalar)**: Scan the bitstream to identify control codes and extract:
- For each value: type (unchanged/same/new), leading, blockSize, bit offset
- Store into a compact metadata array

**Pass 2 (SIMD + BMI2)**: Process 4-8 values at a time:
- Batch-extract meaningful bits using computed masks (BMI2 PEXT)
- Batch-XOR with previous values (SIMD VPXORQ, but sequential dependency on prev)
- Batch-convert uint64 → float64 (no-op in memory, just reinterpret)

The sequential dependency on `prevValue` limits pure parallelism, but BMI2 reduces per-value
instruction count from ~15 to ~6.

#### Files to Create/Modify

| File                            | Action | Description                     |
|---------------------------------|--------|---------------------------------|
| `numeric_gorilla_bmi2.go`       | Create | BMI2-accelerated Gorilla decode |
| `numeric_gorilla_bmi2_amd64.s`  | Create | BMI2 PEXT/BEXTR assembly        |
| `numeric_gorilla_bmi2_stub.go`  | Create | Fallback for non-BMI2           |
| `internal/arch/x86_amd64.go`    | Modify | Add BMI2 detection              |
| `numeric_gorilla_bench_test.go` | Modify | Add BMI2 backend benchmarks     |

#### Target Performance

| Metric                    | Current  | Target    | Improvement |
|---------------------------|----------|-----------|-------------|
| Gorilla Decode 500 values | 3,676 ns | ~2,200 ns | 1.7x        |
| Chimp Decode 500 values   | 3,926 ns | ~2,400 ns | 1.6x        |

---

## Phase 6: Fused Batch Decoder

**Impact: 1.5-2x end-to-end iteration** | Complexity: High | Priority: Medium-Low

### Problem

`FusedDeltaGorillaAll()` (`fused.go:42-93`) decodes timestamp and value streams in lockstep,
one value at a time:

```go
for i := 1; i < count; i++ {
    if !decodeDeltaTimestamp(&ds, tsData) { return }
    if !decodeGorillaValue(&gs) { return }
    if !yield(ds.curTS, gs.prevFloat) { return }
}
```

Even with SIMD-accelerated timestamp decoding (Phases 2-4), the fused loop cannot exploit it
because it alternates between one timestamp and one value decode.

### Design: Batch-Fused Architecture

Restructure the fused decoder to decode in blocks:

```go
const batchSize = 64

for remaining > 0 {
    n := min(remaining, batchSize)

    // Batch decode N timestamps (SIMD-accelerated)
    tsDecoded := batchDecodeTimestamps(tsData[tsOffset:], tsBuf[:n], &tsState)

    // Batch decode N values (BMI2-accelerated or scalar)
    valDecoded := batchDecodeGorillaValues(valData, valBuf[:n], &valState)

    decoded := min(tsDecoded, valDecoded)
    for i := range decoded {
        if !yield(tsBuf[i], valBuf[i]) { return }
    }
    remaining -= decoded
}
```

This allows the timestamp decode to use SIMD bulk paths, and the value decode to use its
optimized batch path. The `yield` loop over materialized buffers is trivially fast.

#### Fused Variants to Implement

| Function                     | Timestamp Codec | Value Codec | Status                    |
|------------------------------|-----------------|-------------|---------------------------|
| `FusedDeltaGorillaAll`       | Delta           | Gorilla     | Upgrade to batch          |
| `FusedDeltaPackedGorillaAll` | DeltaPacked     | Gorilla     | New (highest decode perf) |
| `FusedDeltaChimpAll`         | Delta           | Chimp       | Upgrade to batch          |
| `FusedDeltaPackedChimpAll`   | DeltaPacked     | Chimp       | New                       |

#### Files to Modify

| File                  | Action | Description                                   |
|-----------------------|--------|-----------------------------------------------|
| `fused.go`            | Modify | Restructure all fused decoders to batch model |
| `fused_bench_test.go` | Create | End-to-end fused decode benchmarks            |

#### Target Performance

| Metric                             | Current (estimated) | Target    | Improvement |
|------------------------------------|---------------------|-----------|-------------|
| Fused Delta+Gorilla 1000 pts       | ~5,500 ns           | ~3,200 ns | 1.7x        |
| Fused DeltaPacked+Gorilla 1000 pts | ~4,500 ns           | ~2,500 ns | 1.8x        |

---

## Phase 7: Cache and Memory Optimizations

**Impact: 5-15% across all SIMD paths** | Complexity: Low | Priority: Parallel with above

### 7a. Table Alignment

The decode lookup tables are Go global arrays with no alignment guarantee:

```go
var deltaPackedDecodeShufflesLoDup [256 * 32]byte  // 8KB
var deltaPackedDecodeShufflesHiDup [256 * 32]byte  // 8KB
var deltaPackedDecodeTotalBytes [256]uint8          // 256B
var deltaPackedDecodeTable [256]deltaPackedDecodeMeta // 256×41 = ~10KB
```

**Fix**: Use `//go:align 64` directive (Go 1.25+) or allocate via `aligned_alloc` equivalent to
ensure 64-byte (cache-line) alignment. This prevents false sharing and ensures each table entry
begins at a cache-line boundary for the stride-32 tables.

```go
//go:align 64
var deltaPackedDecodeShufflesLoDup [256 * 32]byte
```

### 7b. Software Prefetch

In the packed decode assembly loop, each iteration loads from a different offset in the shuffle
table (depending on the control byte). Adding a prefetch for the next group's table entry hides
L1 miss latency:

```asm
; At top of groupLoop, after loading current cb:
MOVBQZX 1(SI)(BX*1), R15  ; peek next control byte (speculatively)
SHLQ    $5, R15
LEAQ    ·deltaPackedDecodeShufflesLoDup(SB), AX
PREFETCHT0 (AX)(R15*1)    ; prefetch next shuffle mask
```

### 7c. Non-Temporal Stores for Large Decodes

When decoding more than ~4000 values (32KB of output, exceeding L1), the output buffer will
evict useful data from cache. Non-temporal stores bypass the cache hierarchy:

```asm
; For large decode batches:
VMOVNTDQ Y5, (DI)    ; instead of VMOVDQU Y5, (DI)
; Requires SFENCE before reading the output
```

**Trade-off**: Only beneficial for large batches. Add a threshold check (e.g., nGroups > 1000)
to select between temporal and non-temporal stores.

### 7d. Reduce Metadata Struct Size

`deltaPackedDecodeMeta` is 41 bytes (non-power-of-2). The assembly computes entry addresses as
`cb * 41` using IMULQ. Padding to 64 bytes (cache-line-aligned) would allow shift-based indexing
(`cb << 6`) at the cost of ~6KB more memory. The flat shuffle tables (stride 32, shift by 5) are
already well-designed.

**Recommendation**: Keep the 41-byte struct for the Go-side table; the assembly primarily uses the
flat stride-32 shuffle tables already. Only pad if profiling shows IMULQ as a significant cost.

---

## Appendix A: Current Benchmark Baselines

Captured 2026-04-10 on AMD Ryzen 9 9950X3D (32 threads), Go 1.26.0.

### Delta-of-Delta Kernel (compute only)

| Size  | Scalar   | AVX2     | AVX-512 |
|-------|----------|----------|---------|
| 30    | 8.4 ns   | 5.6 ns   | 5.9 ns  |
| 100   | 27.3 ns  | 10.7 ns  | 10.7 ns |
| 200   | 63.8 ns  | 19.1 ns  | 20.3 ns |
| 1000  | 279.5 ns | 96.4 ns  | 75.8 ns |
| 10000 | 2,731 ns | 1,130 ns | 922 ns  |

### Full Encoder WriteSlice (10k timestamps)

| Backend | Delta Encoder | DeltaPacked Encoder |
|---------|---------------|---------------------|
| Scalar  | 20,692 ns     | 16,232 ns           |
| AVX2    | 20,831 ns     | 15,326 ns           |
| AVX-512 | 20,032 ns     | 15,026 ns           |

### Packed Decoder DecodeAll

| Size  | Scalar    | AVX2     | Speedup |
|-------|-----------|----------|---------|
| 30    | 48.9 ns   | 48.8 ns  | 1.0x    |
| 100   | 164.6 ns  | 74.3 ns  | 2.2x    |
| 200   | 311.8 ns  | 137.0 ns | 2.3x    |
| 1000  | 1,528 ns  | 653 ns   | 2.3x    |
| 10000 | 15,350 ns | 6,483 ns | 2.4x    |

### Packed Decoder All Iterator

| Size  | Scalar    | AVX2      | Speedup |
|-------|-----------|-----------|---------|
| 100   | 251.4 ns  | 213.8 ns  | 1.2x    |
| 1000  | 2,359 ns  | 1,807 ns  | 1.3x    |
| 10000 | 23,359 ns | 17,494 ns | 1.3x    |

### Delta Decoder (Unpacked, No SIMD)

| Workload         | All Iterator | DecodeAll Slice | Throughput (DecodeAll) |
|------------------|--------------|-----------------|------------------------|
| Regular 1s 10k   | 19,646 ns    | 12,491 ns       | 6.4 GB/s               |
| Jitter 5pct 5k   | 12,383 ns    | 8,409 ns        | 4.8 GB/s               |
| Bursty 12k       | 25,352 ns    | 16,504 ns       | 5.8 GB/s               |
| High Variance 2k | 5,774 ns     | 4,312 ns        | 3.7 GB/s               |

### Gorilla/Chimp Numeric Codecs

| Dataset (count)      | Gorilla Encode | Gorilla Decode | Chimp Encode | Chimp Decode |
|----------------------|----------------|----------------|--------------|--------------|
| cpu_util (150)       | 1,159 ns       | 1,338 ns       | 1,051 ns     | 1,159 ns     |
| mem_usage (500)      | 3,470 ns       | 3,676 ns       | 3,553 ns     | 3,926 ns     |
| temperature (200)    | 1,392 ns       | 1,522 ns       | 1,413 ns     | 1,546 ns     |
| net_throughput (300) | 2,224 ns       | 2,223 ns       | 2,219 ns     | 2,411 ns     |
| latency (1000)       | 2,098 ns       | 2,651 ns       | 2,391 ns     | 3,880 ns     |
| disk_iops (500)      | 2,647 ns       | 3,112 ns       | 3,083 ns     | 3,619 ns     |
| battery_volt (100)   | 756 ns         | 839 ns         | 724 ns       | 755 ns       |
| request_rate (800)   | 6,022 ns       | N/A            | 6,185 ns     | N/A          |

## Appendix B: Reference Material

### Academic References

- **Gorilla (Facebook)**: "Gorilla: A Fast, Scalable, In-Memory Time Series Database", Pelkonen et al., VLDB 2015
- **Chimp**: "Chimp: Efficient Lossless Floating Point Compression for Time Series Databases", Liakos et al., VLDB 2022
- **Stream VByte**: "Stream VByte: Faster Byte-Oriented Integer Compression", Lemire et al., Information Processing Letters 2018
- **Masked VByte**: "Vectorized VByte Decoding", Plaisance et al., SISAP 2015
- **SIMD Prefix Sum**: "Prefix Sums and Their Applications", Blelloch, 1990; modern AVX2 formulation in Langdale & Lemire, "Parsing Gigabytes of JSON per Second", 2019

### Comparable Systems (Performance Targets)

| System               | Encode Throughput | Decode Throughput | Notes                          |
|----------------------|-------------------|-------------------|--------------------------------|
| InfluxDB TSM (Go)    | ~200 MB/s         | ~400 MB/s         | Gorilla + delta, no SIMD       |
| QuestDB (Java + JNI) | ~1 GB/s           | ~2 GB/s           | Custom SIMD JNI kernels        |
| TimescaleDB (C)      | ~500 MB/s         | ~1 GB/s           | PostgreSQL-based, columnar     |
| Apache Arrow (C++)   | N/A               | ~3-5 GB/s         | Dictionary + RLE + delta, AVX2 |
| DuckDB (C++)         | ~1.5 GB/s         | ~4 GB/s           | Chimp + ALP + BitPacking, AVX2 |

### Current Mebo Throughput Estimates

| Operation          | Current                    | After All Phases |
|--------------------|----------------------------|------------------|
| DeltaPacked encode | ~5 GB/s                    | ~12-15 GB/s      |
| DeltaPacked decode | ~12 GB/s (AVX2)            | ~18-20 GB/s      |
| Delta decode       | ~6.4 GB/s                  | ~9 GB/s          |
| Gorilla decode     | ~1-3 GB/s (data-dependent) | ~2-5 GB/s        |
| Fused iteration    | ~2-4 GB/s                  | ~5-8 GB/s        |

"World-class" for a pure-Go time-series encoder with optional ASM is 10-20 GB/s for integer
codecs and 3-8 GB/s for floating-point XOR codecs. The full plan targets the upper end of
these ranges.
