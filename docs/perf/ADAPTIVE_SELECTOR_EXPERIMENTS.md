# Adaptive Per-Column Codec Selection — Empirical Feasibility Study

**Date:** 2026-06-15
**Companion to:** `ADAPTIVE_SELECTOR_INVESTIGATION.md` (the literature study). This is the
**"don't guess, verify it"** follow-up: 5 experiments run against the *actual* mebo codecs and the
`tests/measurev2` realistic profiles (100 metrics × 1000 points, Seed=42, all 7 profiles).
**Every experiment was independently re-implemented by a second agent and reproduced byte-for-byte**
(deterministic numbers identical; timing identical in ranking and order of magnitude). Confidence is
**high** on all deterministic numbers, **medium** only on the one denominator-sensitive ratio noted below.

> **Headline:** the experiments *revise* the investigation doc. The doc imagined an elaborate
> per-column, multi-objective (ratio + random-access) hybrid optimizer. The data says: **build the
> simple version.** The winning codec is strongly **profile**-dependent but nearly **invariant within a
> profile**, so per-column tuning buys almost nothing over picking the right codec per workload;
> per-block sub-selection is not worth it; `float32` never wins on these profiles; random-access does
> *not* belong in the default objective; and the only real cost is the ALP encoder.

---

## Decision: GO — build the simple version

Adaptive value-codec selection is worth building **as an automatic per-column (or per-blob) codec
picker** — its value is choosing the right codec *without the user knowing the workload*, since the
right codec differs ~3.5× across profiles. But the mechanism should be far simpler than the
investigation doc proposed:

| Dimension | Investigation doc (guessed) | Experiments say (verified) |
|---|---|---|
| Granularity | per-column, defer per-block | **per-column confirmed**; per-block buys ≤5–10% on 2 profiles, *hurts* sparse (+7%), codec rarely varies within a column → **not worth it** |
| Estimator | hybrid (analytic + sampled) for cheapness/accuracy | estimator choice **barely matters** — all strategies lose ≤5.4 KB of 1.60 MB. Cost is **entirely the ALP encoder**. Full-trial is fine for batch; the lever is ALP speed, not the estimator |
| Random access | "load-bearing" — must be first-class in the objective | **out of the default objective.** Ratio alone already picks ALP, which is uniquely *both* smallest *and* O(1). RA only matters as an opt-in guard against gorilla/chimp for point-lookup workloads |
| `float32` | build first (de-risking step) | **never the per-column best on any of the 7 profiles** — not data-justified yet (the catalog lacks a genuinely float32-precision profile) |
| `raw` floor | hard floor | keep — cheap, makes selection non-regressive |
| Upside | (unquantified) | per-column oracle beats best-single-per-profile by **0.25% aggregate** (0% in 5/7 profiles). The win is *per-workload* codec choice, not per-column tuning |

**Conditions:** re-validate once BP128/Simple8b widen the codec menu (5/7 profiles are currently
single-codec monocultures, which inflates sampler accuracy), and once ALP encode cost is reduced
(it dominates the entire estimator-cost picture).

---

## Verified results by experiment

### E1 — Oracle ratio map (per-column true-best codec) · *reproduced byte-exact*

Mean bytes/point and the per-column winner for each profile (value column only):

| Profile | raw | gorilla | chimp | alp | winner | per-column-oracle upside over best single codec |
|---|---|---|---|---|---|---|
| decimal_gauge_2dp | 8.00 | 6.57 | 6.06 | **1.72** | ALP 100/100 | 0.00% |
| decimal_gauge_4dp | 8.00 | 6.60 | 6.23 | **2.64** | ALP 100/100 | 0.00% |
| counter | 8.00 | 1.64 | 1.87 | **1.64** | ALP 57 / gorilla 43 | **2.31%** |
| sparse_constant | 8.00 | **0.44** | 0.55 | 1.24 | gorilla 100/100 | 0.00% |
| regular_scrape_60s | 8.00 | 6.57 | 6.06 | **1.72** | ALP 100/100 | 0.00% |
| bursty_scrape | 8.00 | 6.59 | 6.07 | **1.69** | ALP 100/100 | 0.00% |
| worst_case | 8.00 | 6.60 | 6.23 | 6.40 | **chimp** 88 / alp 12 | 0.03% |

- **Aggregate:** per-column oracle = **1,604,575 B** vs best-single-codec-per-profile = 1,608,528 B → **0.25% upside**.
- The winner is **strongly profile-dependent** (ALP 4/7, gorilla 1/7, chimp 1/7, counter mixed) but
  nearly **invariant within a profile**.
- **ALP inverts on sparse:** it is the *worst* of the three (1.24 vs gorilla 0.44) on constant runs —
  so a fixed global default is wrong for some workload.
- **`float32` is never the per-column best** on any profile (only lossless on `counter`, where ALP/gorilla beat its 4 B/pt floor).

### E2 — Sampling-estimator accuracy on time-series · *reproduced byte-exact*

Does a small sample pick the oracle codec on time-series (vs BtrBlocks' 77%/3.3% on BI data)?

| Sample strategy | overall correct | bytes-from-optimum |
|---|---|---|
| whole-column TRY_ALL (sanity) | 100.0% | 0.000% |
| BtrBlocks 10×64 = 640 contiguous | **98.3%** | 0.090% |
| 8×64 = 512 contiguous | 97.9% | 0.110% |
| 4×32 = 128 contiguous | 95.6% | 0.314% |
| **10 scattered single points (literal "1%")** | **79.1%** | **+5.608%** |
| └ on sparse_constant specifically | **0.0%** | **+181.9%** |

- BtrBlocks' 77% does **not** transfer down — mebo time-series is *easier* (98.3%), because 5/7 profiles are codec-monocultures.
- **Sample STRUCTURE dominates SIZE.** Contiguous runs preserve RLE/XOR structure; **scattered points are catastrophic** (sparse col: 10 distinct points → per-point scaling predicts ~12× the true size → rejects the correct codec).
- **Header de-bias is mandatory** (gorilla/chimp first value = 8 B uncompressed, ALP main header = 15 B); without subtracting the fixed header before per-value extrapolation, a small sample's header is multiplied by `n/sampleN` and grossly inflates the estimate.

### E3 — Estimator strategy: quality vs encode cost · *reproduced byte-exact*

| Strategy | correct-choice | bytes-from-optimum | wall-clock vs chimp-once (700 cols) |
|---|---|---|---|
| FULL_TRY_ALL (oracle) | 100% | +0.000% | ~14.0× (62 ms) |
| HYBRID | 92.7% | +0.186% (+2982 B) | ~13.2× (58 ms) |
| PURE_SAMPLING | 91.3% | +0.338% (+5417 B) | ~9.1× (40 ms) |

- **Quality is not a differentiator** — all three lose ≤5.4 KB of 1.60 MB.
- **Cost is entirely the ALP encoder:** isolated over 700 cols, gorilla-full 2.4 ms, chimp-full 4.4 ms, but **ALP-full 55 ms (12.6× chimp)** and even ALP-sample 38 ms. Analytic raw+float32 gating is free (0.14 ms).
- HYBRID is barely cheaper than full-trial *because it still does one real full ALP encode*. **The real lever is ALP per-call cost, not the estimator.**

### E4 — Random-access `At(i)` cost · *reproduced; one ratio medium-confidence*

| codec | At(i) shape | i=0 | i=500 | i=999 | random sweep vs 1 DecodeAll |
|---|---|---|---|---|---|
| raw | **O(1)** flat | 3.3 ns | 3.2 ns | 2.8 ns | 2.8× |
| alp | **O(1)** flat | 17 ns | 14 ns | 16 ns | **1.0–1.2×** |
| gorilla | **O(i)** linear | 5.9 ns | 3323 ns | 6604 ns | **1416×** |
| chimp | **O(i)** linear | 5.9 ns | 3454 ns | 7107 ns | 1169× |

- The O(1)-vs-O(i) gap is **real but codec-specific**, not "bit-packing in general": a single late-index lookup on gorilla/chimp is **~2000×** an O(1) lookup (medium confidence on the exact multiplier — denominator is a sub-4 ns raw baseline; order of magnitude is solid), and reconstructing a column via random `At` is **quadratic** (~1170–1420× a single DecodeAll).
- **ALP escapes the tension entirely:** it is *both* smallest on decimal (1391 B vs raw 8000) *and* genuinely O(1) (`At` sweep = 1.0–1.2× DecodeAll). So ratio alone already selects the O(1) codec on the ALP-dominant profiles.
- RA only matters as a guard against picking **gorilla/chimp** to save ~20–25% bytes at a ~2000×/lookup cost.

### E5 — Granularity: per-column vs per-256-block · *reproduced byte-exact*

| Profile | per-column | per-block(256) | Δ | columns whose best-codec varies across blocks |
|---|---|---|---|---|
| counter | 160316 | 144257 | **−10.02%** | 15/100 |
| decimal_gauge_2dp | 171851 | 162913 | −5.20% | 0/100 |
| bursty_scrape | 169080 | 162596 | −3.83% | 0/100 |
| decimal_gauge_4dp | 264289 | 256566 | −2.92% | 0/100 |
| worst_case | 623185 | 623031 | −0.02% | 54/100 |
| sparse_constant | 44003 | 47103 | **+7.04% (worse)** | 0/100 |

- Per-block buys **≤5–10% on two profiles, is negligible elsewhere, and is *worse* on sparse** (gorilla's per-column header re-paid 4×).
- The codec **actually changing across blocks within a column is rare** (0/100 on 4 profiles). The small gains come from a *single* adaptive codec re-fitting local residuals, not from switching codecs — mebo could capture that with smaller native blocks without a per-block selection mechanism.
- **Per-column selection is sufficient.**

---

## The chosen mechanism (verified)

1. **Granularity: per-column.** (Per-blob would likely suffice too, since columns within a workload
   agree — a cheap further simplification, but per-column is what the data directly supports.)
2. **Estimator: full per-column trial-encode is acceptable for mebo's batch-first, write-once model**
   (~88 µs/column). Quality is a non-issue across strategies. Keep a sampled path as a forward-compatible
   option, but it only becomes worthwhile *after* ALP encode cost is reduced. If sampling is used,
   sample **~512 values as contiguous runs (8×64)** with **fixed-header subtraction**, and **never
   isolated points**.
3. **Candidate set: `{raw, gorilla, chimp, ALP}`** for the current profiles. **Drop `float32`** unless/until
   a genuinely float32-precision profile is added to the catalog and shown to win — it never wins here.
4. **`raw` is the hard floor** (non-regressive safeguard).
5. **Objective: ratio only by default.** Random-access does *not* enter the default objective (ALP is
   already small *and* O(1)). Expose an **opt-in** policy that penalizes/excludes **gorilla and chimp**
   for point-lookup workloads.
6. **Format: reuse the existing self-describing `EncodingType` byte** per column (the ALP wiring
   already established this) — ~1 byte/column overhead.

**Highest-leverage follow-up (not part of the selector):** speed up the ALP encoder (currently 12.6×
chimp). It is the codec that wins the most profiles, escapes the random-access tension, *and* dominates
selection cost — improving it improves ratio, the cost of any try-all/hybrid selector, and decode-side
behavior simultaneously.

---

## Open risks / conditions (carried into any impl plan)

1. **Small codec menu.** 5/7 profiles are single-codec monocultures, inflating sampler accuracy (98.3%).
   Re-run E2/E3 once BP128/Simple8b widen the menu — accuracy will drop toward BtrBlocks' 77%.
2. **ALP cost is implementation-specific.** The whole estimator-cost ranking shifts if ALP is sped up;
   PURE_SAMPLING only becomes worthwhile then. Treat the estimator choice as revisitable.
3. **Single Seed=42 realization, value-column only.** Reproducible but not seed-averaged; timestamps,
   block/section headers, and metric-ID overhead are excluded — end-to-end blob ratios will differ.
4. **`float32` untested, not disproven.** The catalog has no genuinely float32-precision sensor profile;
   add one before concluding float32 has no value.
5. **ALP parameter generalization.** ALP re-searches (exponent, factor) per call; a sample-fitted ALP
   size assumes those generalize — held on smooth decimals here, but a column whose decimal scale shifts
   mid-stream could mispredict.

---

*Method: 5 experiments × (run-for-real in an isolated worktree → independent adversarial
re-implementation in a second worktree). All deterministic numbers reproduced byte-exact; all 5
verdicts "confirmed" at high confidence (one ratio at medium). Total: 11 agents.*
