# Gorilla/Chimp BMI2 Acceleration (SIMD Plan Phase 5) — Assessment & Closure

| | |
|---|---|
| **Date** | 2026-06-13 |
| **Platform** | AMD Ryzen 9 9950X3D (Zen 5), linux/amd64, Go 1.26.1 |
| **Scope** | Feasibility assessment only — no production change |
| **Outcome** | Phase closed as written; one successor idea recorded |
| **Method** | External design review (Codex, grounded in current code) + measurement |

## Why the phase is closed

Plan Phase 5 (April) proposed BMI2 PEXT/PDEP to accelerate the
"extract meaningful bits" step of Gorilla/Chimp decode. It was scoped against
the old stateful `bitReader` with per-call refill branches. The June rewrite
(`docs/perf/XOR_CODEC_BITPACK_OPTIMIZATION.md`) replaced that machinery with
windowed reads (`peekBits64`: one unaligned 8-byte load + ninth byte) and a
two-shift contiguous-field extraction:

```go
meaningful = (w << consumed) >> (64 - blockSize)
```

A Codex design review grounded in the current `decodeGorillaValue` /
`decodeChimpValue` concluded, and we concur:

- **PEXT: zero or negative.** The extracted field is contiguous; PEXT exists
  for sparse bit selection. It would replace two 1-cycle dependent shifts
  with mask construction + a multi-cycle PEXT. No surrounding work is
  eliminated.
- **Refill branches are already gone**; the remaining branches (zero-run,
  unchanged-run, reuse-vs-new block) are semantic dispatch and predictable
  on real data. Branchless rewrites would trade predictable branches for
  always-executed work.
- **Speculative window prefetch and a 13-bit header lookup table**: noise-level
  unless profiles show the new-block path is hot (it is not on
  random-walk data — block reuse dominates).

## Measured: GOAMD64=v3

The compiler emits BMI2 SHLX/SHRX (flag-free variable shifts) at
`GOAMD64=v3`. Same-machine A/B:

| Benchmark | v1 (default) | v3 | Δ |
|---|---|---|---|
| FusedDeltaGorillaEach (codec, 200 pts) | 935 ns | 909 ns | −2.8% |
| E2EForEach_DeltaGorilla (40k pts) | 209.0 µs | 204.7 µs | −2.1% |
| E2EIterate_DeltaGorilla (40k pts) | 281.4 µs | 277.7 µs | −1.3% |

`GOAMD64` is selected by the **consuming application's build**, not by this
library — so this is a deployment tip for users on x86-64-v3+ fleets, not a
code change.

## Recorded successor idea: two-stream interleaved decode

The XOR codecs are inherently serial per stream (each value's bit position
depends on the previous value's consumed bits). The one credible latency
lever is decoding **two independent metric streams alternately in one loop**,
overlapping one stream's load/shift/XOR chain with the other's. Estimated
5–15% codec-local, 2–5% end-to-end, and only where multiple metrics are
decoded together with array outputs — i.e. the materialization paths
(`Materialize`, blob-set materialization), not single-metric `All`/`ForEach`.
Moderate restructuring (metric pairing, odd-count handling, doubled decoder
state in one loop); prototype-and-measure before committing, per this
initiative's standing methodology — round 3 showed register allocation in
such widened loops can disappoint.
