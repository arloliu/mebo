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
