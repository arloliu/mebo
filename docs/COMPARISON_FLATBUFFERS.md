# Mebo vs FlatBuffers: Benchmark Comparison

This document benchmarks Mebo against FlatBuffers for time-series numeric data storage. All results are from a fresh benchmark run on the current development machine.

**Platform**: AMD Ryzen 9 9950X3D 16-Core Processor, linux/amd64, Go 1.26.0
**Dataset**: 200 metrics, benchmarked at 50 and 200 points/metric (10,000 and 40,000 total data points)
**Benchmark date**: April 2026

> **Scope note**: This comparison uses Mebo encodings that have a direct FlatBuffers equivalent (Raw, Delta, Gorilla with optional codec compression). Mebo-only capabilities — Chimp encoding, DeltaPacked, and shared timestamps — are excluded from this comparison since FlatBuffers has no equivalent. For those configurations, see [Performance Guide](PERFORMANCE_V2.md).

---

## Space Efficiency

Mebo Delta+Gorilla with no codec achieves better compression than FlatBuffers+Zstd, without any codec overhead on decode:

| Format | Config | Bytes/Point (50 PPM) | Bytes/Point (200 PPM) |
|--------|--------|---------------------:|----------------------:|
| Mebo | Delta + Gorilla (no codec) | 9.995 | 9.690 |
| Mebo | Delta + Gorilla + Zstd | 9.996 | 9.691 |
| FlatBuffers | + Zstd | 11.32 | 11.15 |
| FlatBuffers | + S2 | 14.44 | 14.81 |
| FlatBuffers | + LZ4 | 13.65 | 14.34 |
| Mebo | Raw + Raw (baseline) | 16.32 | 16.08 |
| FlatBuffers | no compression | 16.88 | 16.22 |

Key observations:
- Mebo's Delta+Gorilla encoding alone (no codec) achieves **9.69 bytes/point**, which is **13% smaller than FlatBuffers+Zstd** (11.15 bytes/point) at 200 PPM.
- Adding Zstd to Mebo provides no meaningful additional benefit on already-compressed numeric data (~0.001 bytes/point difference).
- FlatBuffers without compression is slightly larger than Mebo Raw+Raw because FlatBuffers encodes both timestamps and values as raw 8-byte doubles with schema overhead.

---

## Decode + Iterate Performance (primary read workload)

The most common production read pattern: decode the blob, then iterate all metrics and data points.

| Format | Config | 50 PPM (ns/op) | 200 PPM (ns/op) |
|--------|--------|---------------:|----------------:|
| Mebo | Raw + Raw | 60,651 | 127,835 |
| Mebo | Delta + Raw (no codec) | 90,314 | 244,371 |
| Mebo | Delta + Gorilla (no codec) | 169,467 | 555,757 |
| FlatBuffers | no compression | 414,301 | 667,612 |
| FlatBuffers | + S2 | 451,560 | — |
| FlatBuffers | + LZ4 | 456,473 | — |
| FlatBuffers | + Zstd | 502,206 | 932,387 |

Key observations:
- Mebo Delta+Gorilla (no codec) is **2.4× faster** than FlatBuffers (no compression) at 50 PPM, and **1.2× faster** at 200 PPM for the combined decode+iterate workload.
- Mebo Raw+Raw is **6.8× faster** than FlatBuffers (no compression) — the smallest blob wins when no computation is needed.
- FlatBuffers' read performance degrades approximately linearly with data size; Mebo with Gorilla degrades similarly but from a much lower base.

---

## Random Access Performance (ValueAt)

Random access to individual data points by index. Benchmarked across 200 metric IDs with random indices.

| Format | Config | 50 PPM (ns/op) | 200 PPM (ns/op) | Allocs/op |
|--------|--------|---------------:|----------------:|----------:|
| Mebo | Raw timestamp + any value | 4,956–4,988 | 4,972–5,007 | 0 |
| Mebo | Gorilla value (any TS) | 52,916–53,784 | 168,279–169,689 | 0 |
| FlatBuffers | any compression | 156,048–157,251 | 158,359–158,796 | 200 |

Key observations:
- Mebo with raw timestamps provides **O(1)** random access: ~5 ns/op regardless of point count, zero allocations.
- Mebo with Gorilla values requires sequential unshuffle: cost grows with point count (52 µs at 50 PPM → 169 µs at 200 PPM), still zero allocations.
- FlatBuffers random access costs ~157 ns, stable across point sizes because FlatBuffers stores values at fixed offsets. It allocates 200 objects per call (one per metric), adding GC pressure under load.
- Mebo Raw+Raw is **31× faster** than FlatBuffers for numeric random access and allocates nothing.

---

## Summary

| Operation | Best Mebo Config | Mebo (ns/op, 200 PPM) | FlatBuffers best (ns/op, 200 PPM) | Advantage |
|-----------|------------------|-----------------------:|-----------------------------------:|-----------|
| Space | Delta + Gorilla | 9.690 bytes/point | 11.15 bytes/point (Zstd) | 13% smaller |
| Decode + iterate | Raw + Raw | 127,835 | 667,612 (no compression) | 5.2× faster |
| Decode + iterate | Delta + Gorilla | 555,757 | 667,612 (no compression) | 1.2× faster |
| Random access | Raw timestamp | 4,972 | 158,796 (no compression) | 31× faster, 0 allocs |

### When to choose Mebo

- **Numeric time-series data with regular intervals**: Delta+Gorilla encoding achieves better compression than FlatBuffers+Zstd without codec overhead.
- **Sequential read workloads**: Mebo's in-memory iteration is significantly faster for full-scan patterns.
- **High-frequency random access**: O(1) access with zero allocations (Raw timestamp encoding).
- **Advanced compression needs**: Chimp encoding and shared timestamps (no FlatBuffers equivalent) can achieve up to 60.5% space savings; see [Performance Guide](PERFORMANCE_V2.md).

### When FlatBuffers may be preferable

- **Schema evolution**: FlatBuffers has a formal schema language and native forward/backward compatibility tools.
- **Cross-language interoperability**: FlatBuffers has first-class support in many languages; Mebo is Go-only.
- **Mixed data types in a single record**: FlatBuffers tables can mix numeric, string, and nested types naturally; Mebo separates numeric and text into distinct blob types.

---

*Benchmark source: `tests/fbs_compare/`.*
*For Mebo-only encoding benchmarks (Chimp, DeltaPacked, Shared Timestamps), see [Performance Guide](PERFORMANCE_V2.md).*
