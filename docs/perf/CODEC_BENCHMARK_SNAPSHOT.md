# Codec Benchmark Snapshot

End-to-end encoding-matrix measurement across realistic data profiles, produced by
[`tests/measurev2`](../../tests/measurev2). This snapshot reflects the state **after** both
the ALP encode speedup and the ALP **decode** speedup (5–14× — the streaming bit reader),
and exists to motivate per-column adaptive value-codec selection: **no single value codec
wins across data shapes.**

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
| decimal_gauge_2dp | raw | 7,521 | 6,351 | 34 |
|  | gorilla | 10,562 | 6,343 | 34 |
|  | chimp | 13,435 | 9,076 | 34 |
|  | alp | 79,456 | 7,377 | 1,119 |
| decimal_gauge_4dp | raw | 7,474 | 6,407 | 34 |
|  | gorilla | 10,660 | 6,275 | 34 |
|  | chimp | 13,590 | 9,091 | 34 |
|  | alp | 136,398 | 7,280 | 24,116 |
| counter | raw | 7,539 | 6,431 | 34 |
|  | gorilla | 10,463 | 7,156 | 34 |
|  | chimp | 9,823 | 6,524 | 34 |
|  | alp | 70,747 | 7,221 | 244 |
| sparse_constant | raw | 7,548 | 6,586 | 34 |
|  | gorilla | 7,134 | 4,777 | 34 |
|  | chimp | 7,119 | 4,703 | 34 |
|  | alp | 72,625 | 7,267 | 3,578 |
| worst_case | raw | 7,284 | 6,318 | 34 |
|  | gorilla | 10,517 | 6,211 | 34 |
|  | chimp | 13,454 | 9,082 | 34 |
|  | alp | 145,950 | 8,093 | 21,130 |

## Takeaways

- **ALP is the compression champion on decimal & counter data** (2.5–6× smaller than the
  next-best codec). Encode still costs **7–19× more CPU** than raw with high alloc counts on
  the ALP-RD path (4dp, worst_case) — the remaining optimization target.
- **ALP decode is now competitive**: after the streaming-reader speedup, ALP sequential
  iterate on decimals is ~7,377 ns/1k — between gorilla (~6,343) and chimp
  (~9,076), no longer the read-laggard it was (~19,700 ns/1k before). This removes the
  read-cost objection to selecting ALP.
- **gorilla is the all-rounder**: best on sparse in *both* size and speed, cheap to encode,
  fastest iterate after raw.
- **chimp** narrowly wins full-precision size; otherwise slower to iterate than gorilla.
- **Choosing ALP blindly is still a trap on the wrong shape**: on `worst_case` it is *larger*
  than chimp; ALP only pays where the ratio win justifies the (now much lower) read cost and
  the encode cost.
- This data-dependence is the empirical case for **per-column adaptive value-codec selection**
  with raw kept as a hard floor. See
  [`ADAPTIVE_SELECTOR_EXPERIMENTS.md`](ADAPTIVE_SELECTOR_EXPERIMENTS.md) and the
  [implementation plan](../plans/2026-06-15-adaptive-value-codec-selection.md).
