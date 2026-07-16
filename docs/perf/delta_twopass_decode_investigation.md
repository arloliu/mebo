# Delta DecodeAll Two-Pass Investigation (SIMD Plan Phase 4) — Negative Result

| | |
|---|---|
| **Date** | 2026-06-13 |
| **Platform** | AMD Ryzen 9 9950X3D (Zen 5), linux/amd64, Go 1.26.1 |
| **Scope** | `TimestampDeltaDecoder.DecodeAll` (investigation only — no production change) |
| **Outcome** | **Rejected after full prototype + same-binary A/B**; plan Phase 4 closed |
| **Plan** | [SIMD_OPTIMIZATION_PLAN.md Phase 4](../plans/2026-04-10-simd-optimization.md#phase-4-simd-delta-decode-for-unpacked-format) |

## What was tried

The plan's Phase 4: split the fused varint-decode-and-accumulate loop into
(1) a scalar varint+zigzag pass into a flat delta-of-delta buffer and (2) an
AVX-512 dual 8-wide prefix sum converting DoDs to timestamps
(`prefixSumDoDIntoASMAVX512`, the decode-direction inverse of the Phase 2
encode kernel). A full prototype was built, parity-verified across counts,
jitter regimes, and chunk boundaries, and integrated behind an
average-varint-width heuristic before benchmarking killed it.

## Why it was rejected

1. **The projected 1.4× was against a stale baseline.** Phase 4 was scoped
   in April against 12,491 ns/10k; the unrolled 1/2/3-byte varint ladder
   (landed June, commit f2d0cc2 round) already brought the one-pass loop to
   ~5,200–7,700 ns/10k, removing most of the headroom the two-pass targeted.
2. **The two-pass win exists only for *unpredictably mixed* varint widths.**
   With random ±5% jitter (widths mix 2/3-byte at random positions —
   branch-miss-bound), two-pass measured −23%. But:
   - uniform 2-byte streams (the measurev2 reference, ±0.1% jitter): **+5–10% slower**
   - the project's own irregular-data suite, where jitter is structured and
     the branch predictor learns the width pattern (same-binary A/B):
     Jitter_5pct_5k **+30%**, HighVariance_2k **+17%**, BurstyTraffic_12k +3%
3. **The deciding factor is width *predictability*, not width.** An
   average-bytes-per-value heuristic (free upfront from
   `len(data)/count`) selects correctly between 2-byte and 3-byte regimes
   but cannot distinguish predictable from unpredictable 3-byte streams —
   and picking wrong costs up to 30%.

The buffer round-trip costs ~0.3 ns/value; the accumulation it removes from
pass 1 is effectively free on a wide out-of-order core unless the loop is
already stalled on branch misses.

What survives from the investigation: `TestTimestampDeltaDecoder_DecodeAll_JitterRegimes`
and `..._JitterTruncated` (`ts_delta_jitter_test.go`) — parity and truncation
coverage across varint-width regimes that did not exist before.

## Codex consultation (same session)

Codex (GPT-5-class, via the codex plugin) adversarially reviewed the Phase 3
AVX-512 packed decoder kernel landed in commit d84f6d6 across six axes
(memory safety, VPERMB sentinel masking, prefix-sum/carry math, VALIGNQ
operand order, Go ABI, wrapper progress merging). Verdict: kernel correct;
one real low-severity finding — the wrapper's fallback guard required 66
readable bytes where the kernel's window check needs only 65, costing one
AVX-512 iteration on exact-tail streams. Fixed in this commit
(`deltaPackedDecodeSIMDSafeLoadWindowAVX512+1`). A BP frame-pointer clobber
in the same kernel ($0 frame with BP as scratch) was caught and fixed by
inspection just before the review (frame size now $8, which makes the
assembler save/restore BP).

## Conclusions for future work

- Re-evaluate SIMD varint decode only via the "Masked VByte" style approach
  (plan Phase 4 alternative), which vectorizes the *width classification*
  itself and is immune to branch-predictability effects — a substantially
  larger undertaking.
- Any future "separate the passes" idea must benchmark against the
  *structured*-jitter suite cases, not only random-jitter generators; the
  two disagree by up to 50 percentage points.
