# XOR Codec Bit-Machinery Optimization Report

| | |
|---|---|
| **Date** | 2026-06-12 |
| **Platform** | AMD Ryzen 9 9950X3D (Zen 5), linux/amd64, Go 1.26.1 |
| **Scope** | `internal/encoding`: Gorilla + Chimp encoders/decoders, fused decode paths |
| **Format impact** | None — encoded bytes are bit-identical before/after |

## Summary

Profiling the whole numeric encode/decode flow showed that the float XOR codecs
(Gorilla, Chimp) — not timestamps and not the SIMD kernels — dominate per-point
cost in the default configuration (`TypeDelta` + `TypeGorilla`). Inside those
codecs, the stateful bit-reader/bit-writer machinery accounted for ~60% of
decode time and ~50% of encode time.

This change replaces that machinery with:

- **Encode**: an MSB-aligned 64-bit bit accumulator (`appendBits`) that writes a
  whole value in ≤ 2 append operations, each with exactly one predictable spill
  branch storing 8 bytes unconditionally.
- **Decode**: windowed bit reads (`peekBits64`) against an absolute bit
  position — one unaligned 8-byte big-endian load yields a 64-bit window at any
  bit offset, so a changed value decodes with 1–2 loads instead of ~5 branchy
  `bitReader` calls. Runs of unchanged values are counted from a single window
  with `bits.LeadingZeros64`.

Codec-level effect: **decode 2.2–3.3× faster, encode 1.5–2.3× faster**, zero
allocations, across all eight benchmark data patterns.

## Root-cause analysis (measured)

CPU profiles of `BenchmarkChimpVsGorilla_DecodeAll` / `_Encode` before the change:

| Component | Share of decode time | Share of encode time |
|---|---|---|
| `bitReader.readBits` | 35% flat / 40% cum | — |
| `bitReader.readBit` + `read2/3/5/6Bits` + `fillBuffer` | ~22% | — |
| `writeBits` + `flushBits` + `writeBit` | — | ~40% |
| Gorilla `WriteSlice` run-detection lookahead | — | 14% flat |

The old `bitReader` refilled only when its buffer hit zero bits, so every read
of every field (control bit, reuse bit, 5-bit leading, 6-bit size, meaningful
bits) carried its own refill branch and function-call overhead. The old writer
mirrored this on the encode side. The `WriteSlice` lookahead scanned ahead for
identical values on every iteration — pure overhead on noisy data, and the
unchanged-value case is already handled by the `xor == 0` test.

Context that motivated looking here: end-to-end timestamps cost ~1 ns/pt
(already SIMD-accelerated), while Gorilla/Chimp cost ~7–8 ns/pt each way —
an ~8× imbalance on the default configuration.

## What changed

| File | Change |
|---|---|
| `internal/encoding/value/gorilla/gorilla.go` | Encoder: `appendBits` accumulator, single-pass `WriteSlice` (lookahead removed), tail-only `flushBits`. Decoder: windowed `DecodeAll` + `All`, zero-run batching. Added `peekBits64`. Removed `decodeAllSmall`/`decodeAllLarge`/`countLeadingZeroBits`. |
| `internal/encoding/value/chimp/chimp.go` | Same encoder treatment; windowed `DecodeAll` + `All` with 00-flag-pair run batching. Removed `chimpCountUnchangedRun`. |
| `internal/encoding/fused/fused.go` | `gorillaState`/`chimpState` converted from `bitReader` to windowed reads (`newGorillaState`/`newChimpState`); per-call zero-run draining. All 10 fused decode paths benefit (these back `NumericBlob.All`). |
| `internal/encoding/timestamp/raw/raw_bench_test.go` | Fixed benchmark that reused an encoder after `Finish()` (panicked, aborting full bench runs). |

Cold paths (`At`, `ByteLength`, `chimpDecodeNext`) intentionally keep the old
`bitReader` — they are not on the hot path and the reader is still correct.

## Correctness verification

The encoded format is unchanged, verified three independent ways:

1. **Gorilla**: a POC encoder/decoder was first proven byte-identical /
   value-identical against the *old* implementation on all 8 benchmark datasets
   plus edge cases (NaN, ±Inf, ±0, denormals, constants, alternating), then the
   production rewrite was proven identical to the POC.
2. **Chimp**: SHA-256 golden hashes of the old encoder's output (14 cases ×
   both `Write` and `WriteSlice` paths) captured before the rewrite and
   verified after.
3. **Blob level**: `tests/measurev2` reports identical `encoded_bytes` for all
   18 encoding combos before/after.

Full test suite, race-enabled tests, and `make lint` all pass.

## Results

### Codec level (`internal/encoding` benchmarks, ns/op)

Gorilla `DecodeAll`:

| Dataset | Before | After | Speedup |
|---|---|---|---|
| cpu_util_150 | 1233 | 370 | 3.3× |
| mem_usage_500 | 3168 | 1129 | 2.8× |
| temperature_200 | 1386 | 459 | 3.0× |
| net_throughput_300 | 2006 | 686 | 2.9× |
| latency_1000 | 1812 | 714 | 2.5× |
| disk_iops_500 | 2694 | 1225 | 2.2× |
| battery_volt_100 | 831 | 261 | 3.2× |
| request_rate_800 | 5369 | 1819 | 3.0× |

Gorilla encode (`WriteSlice`): 1.5–2.3× (e.g. cpu_util_150 1172→533 ns,
request_rate_800 6129→2716 ns). Chimp encode: 1.4–2.3×. Chimp decode via
`All` iterator: 1.7–2.0×. All paths remain 0 B/op.

### End-to-end (`tests/measurev2`, 200 metrics × 200 points, ns/point)

| Combo | Encode | Iterate |
|---|---|---|
| delta-gorilla (default) | 17.9 → 15.2 (−15%) | 11.8 → 7.6 (−35%) |
| delta-chimp | 19.4 → 16.9 (−13%) | 14.5 → 10.1 (−31%) |
| deltapacked-gorilla | 17.5 → 14.4 (−17%) | 13.9 → 9.1 (−35%) |
| raw-gorilla | 21.9 → 17.1 (−22%) | 12.6 → 7.7 (−38%) |
| shared-delta-gorilla | 19.0 → 15.4 (−19%) | 11.8 → 7.7 (−35%) |

All 9 Gorilla/Chimp combos improved (encode −10% to −22%, iterate −23% to
−38%); raw-value combos unchanged within noise; bytes/point identical
everywhere.

## Remaining verified levers (not yet implemented)

Measured during the same investigation, in rough order of expected impact:

1. **Blob-layer per-point encode overhead (~9–10 ns/pt end-to-end)** —
   `AddDataPoint` performs an interface `Len()` call per point for the limit
   check and a per-point `header.Flag.HasTag()` load; `AddDataPoints` allocates
   `make([]string, n)` and encodes empty tags even when tag support is
   disabled.
2. **Iterate yield chain (~3 ns/pt, 1401 allocs per 200-metric scan)** —
   full-scan callers could use `DecodeAll` materialization instead of nested
   iterator wrappers.
3. **SIMD plan Phase 3** — AVX-512 delta-packed decoder (Zen 5 has the full
   512-bit datapath); **Phase 6** — batch-fused decoders. Phase 5 (BMI2 for
   Gorilla/Chimp) is largely obsoleted by this rewrite.
