# Adaptive Per-Column Codec Selection — Investigation Report

**Date:** 2026-06-14
**Scope:** Should mebo add an *adaptive selector* that, per column, samples or trial-encodes
the data and picks the best codec among `{ALP-main, ALP-RD, Chimp, Gorilla, float32-narrowing,
delta/delta-packed (timestamps), raw}` instead of forcing one codec everywhere?
**Method:** Multi-source web research with adversarial verification (5 angles → 17 sources →
81 candidate claims → 25 verified, 2-of-3 refute to kill → 21 confirmed, 4 killed).

> **Honesty note up front.** The verified evidence is dominated by two TUM systems —
> **BtrBlocks** (SIGMOD 2023) and its predecessor **Data Blocks** (SIGMOD 2016). These are
> peer-reviewed with verbatim-checked quotes, so confidence is high *for them*. The question
> also asked about Parquet, ORC, Arrow, DuckDB, Velox/Nimble, InfluxDB/IOx, ClickHouse, and the
> Gorilla/Chimp lineage — **no claims about those survived verification**; treat their absence as
> a coverage gap, not evidence they lack adaptive selection. All BtrBlocks numbers below were
> measured on the **Public BI Benchmark**, *not* time-series data, and must be re-measured before
> being trusted for mebo.

---

## 1. Executive summary

Adaptive per-column / per-block codec selection is a mature, well-understood technique. The
canonical modern design (BtrBlocks) is: **trial-compress a tiny (~1%) stratified-contiguous
sample of each block with every viable codec and keep the one with the best observed ratio**,
after a cheap single-pass statistics scan prunes obviously-unviable codecs. The headline result
is that this is **nearly free and nearly optimal**: ~1.2% of compression CPU, the *correct* codec
chosen 77% of the time, and output only 3.3% larger than the per-block optimum on average.

For mebo the technique is sound and the cost is affordable, but three things must shape the
design:

1. **The win is mostly on the read path.** BtrBlocks' measured advantage over Parquet (2.2× faster
   scans) came from *decompression throughput*, not ratio. This aligns with mebo's I/O-aware
   framing (smaller blob → faster retrieval) and its zero-alloc iteration goal — but it means the
   selector's value is realized at read time, so decode speed belongs in the objective.
2. **Ratio-vs-random-access is a real, documented tension.** Data Blocks *deliberately rejected*
   bit-packed (sub-byte) encodings to preserve byte-addressable O(1) point access. mebo's
   best-ratio candidates (ALP, Chimp, Gorilla) are bit-packed and do **not** give O(1) `At()` — we
   already hit this (ALP `At()` is O(nExc); materialized iteration was needed). A ratio-only
   selector will systematically pick codecs that degrade random access. **If O(1) `At()` matters
   for a workload, random-access cost must be part of the selection criterion**, not just ratio.
3. **mebo can do better than pure sampling**, because several candidates have *exact* closed-form
   sizes (raw = 8n; float32 = 4n with an O(n) lossless gate; delta is cheap to size) — so a
   **hybrid estimator** (analytic for raw/float32/delta, sampled trial-encode for ALP/Chimp/Gorilla)
   is both cheaper and more accurate than uniform sampling, with `raw` as the guaranteed floor.

**Bottom line:** adaptive selection is worth building for mebo, but as a *multi-objective*
(ratio + decode/random-access) per-column picker with a hybrid estimator and a hard `raw`
fallback — and its accuracy must be validated on the measurev2 time-series profiles before the
format byte is committed, since the published accuracy numbers are from BI data.

---

## 2. How selection works (mechanics)

There are three selection strategies in the literature; real systems mix them.

| Strategy | What it does | Who | Cost | Accuracy |
|---|---|---|---|---|
| **Statistics / cost-model** | Compute min/max/distinct/run-length, pick by formula | Data Blocks | cheapest | deterministic, narrow catalog |
| **Sampling trial-encode** | Compress a ~1% sample with each viable codec, keep best ratio | BtrBlocks (`SAMPLE`, default), Vortex | ~1.2% CPU | 77% correct |
| **Full trial-encode** | Compress the *whole* block with every codec, keep smallest | BtrBlocks (`TRY_ALL`, opt-in) | high | optimal-by-construction |

**BtrBlocks (default, high confidence — SIGMOD 2023 paper + source at `maxi-k/btrblocks`):**

- Selects an encoding **per fixed-size block** (default `block_size = 65,536` tuples; the paper
  rounds this to "64,000").
- First does a **single-pass statistics scan** (min, max, unique count, average run length) to
  *prune* non-viable codecs — e.g. exclude RLE when avg run length < 2, exclude Frequency Encoding
  when ≥50% of values are unique.
- Then **trial-compresses a ~1% stratified-contiguous sample** with each surviving codec and keeps
  the highest observed ratio. The sample is `sample_count = 10` runs of `sample_size = 64`
  *contiguous* values each (≈640 values), drawn at random offsets from 10 equal partitions
  (stratified/blocked random — *not* per-element, *not* a single prefix). If the block is smaller
  than the sample, it uses the whole block. The paper found sampling several small chunks across the
  block beats other strategies, with little difference once each chunk is ≥16 tuples.
- **Deliberately avoids cost models** ("our scheme selection algorithm avoids cost models and opts
  for an easily-extendible sampling-based approach").
- **Safeguards:** fall back to `UNCOMPRESSED` when the chosen output is larger than raw; throw if no
  codec reaches ratio ≥ 1.0.
- **Cascading** layers on top: the chosen codec's output is recursively re-encoded by re-running the
  same sampling picker, to a **max depth defaulting to 3**, after which data is left uncompressed.
  Each codec records what it cascaded into so decompression reverses the chain. BtrBlocks ships 8
  schemes (RLE, One-Value, Dictionary, Frequency, SIMD-FastPFOR, SIMD-FastBP128, FSST, Roaring,
  plus the new **Pseudodecimal** float codec).

**Vortex / SpiralDB (independent corroboration, high confidence):** a production BtrBlocks-style
compressor that "draws a small stratified sample and compresses that instead," targeting ~1% with a
**minimum of 1,024 values**, using **adjacent (contiguous) values, not scattered individuals**, then
trial-compresses the sample through the full cascade and picks by total output size. Confirmed in
shipped code (`SAMPLE_SIZE=64 × SAMPLE_COUNT=16 = 1024`). *(Vortex's blog throughput/ratio numbers —
"38% smaller than Parquet+ZSTD", "10-25× faster decode" — were **refuted** in verification and are
excluded.)*

**Data Blocks (foundational, high confidence — SIGMOD 2016):** selects independently **per column
AND per block**, choosing the codec that *minimizes memory* from just **three byte-addressable
candidates** (single-value/RLE, ordered dictionary, byte-truncation to 8/16/32-bit), leaving a block
uncompressed only when nothing helps. Selection is **deterministic stat-driven** (distinct-count /
value-range), not sampling. Default block size 2^16 records.

---

## 3. Cost / encode-time overhead

**High confidence (BtrBlocks, SIGMOD 2023, verbatim):** "we ... sample 10 × 64 tuples = 1% of each
block by default. This takes up **1.2% of CPU time during compression** and results in **77% correct
scheme choices**. With these choices, BtrBlocks compresses only **3.3% worse than the optimum** on
average." "Correct" = the optimal codec or one at most 2% worse.

Techniques the field uses to keep selection cheap:

- **Sampling** instead of full trial-encode (the 1.2% figure above).
- **Statistics-based pruning** before any trial-encode (drop codecs that cannot possibly win).
- **Cost models** where a codec's size is closed-form (Data Blocks' entire approach).
- **Bounded cascading** (depth 3) so recursive re-encoding cannot blow up.
- **`TRY_ALL` only when affordable** — full exhaustive encode is an explicit opt-in mode, not the
  default.

**For mebo:** even *full* trial-encode is plausibly affordable given the batch-first, write-once
model and a bounded candidate set — but several candidates need no trial at all (raw, float32,
delta size analytically), so the marginal cost is one sampled encode each for ALP/Chimp/Gorilla.

---

## 4. Benefit

**High confidence (BtrBlocks):** scans on the five largest Public BI Benchmark datasets are
**2.2× faster and 1.8× cheaper than Apache Parquet**, "due to its superior decompression
performance." Single-threaded *compression* speed is similar to Parquet — so the benefit is on the
**read/scan path**, driven by decompression bandwidth, **not** by compression ratio. The paper notes
that on broader data (e.g. TPC-H) Zstd-compressed Parquet/ORC can achieve *better* ratio; BtrBlocks
wins on decode speed.

**High confidence (Data Blocks):** per-block selection produces large layout diversity in practice
(**50+ distinct layouts** for TPC-H SF100 `lineitem`) because each block picks the best codec for its
*local* value range; block-wise selection beats relation-wide by amortizing local overheads, with
ratios **up to 5× vs uncompressed** (caveat: ~25% *more* space than Vectorwise's compressed storage —
the byte-addressability tax, see §5).

**General motivation (high confidence — Bullion, CIDR 2025):** "The composable and recursive nature
of encodings enables combinatorial patterns that can achieve superior data compression compared to
static, single-encoding approaches," and the search space grows with the catalog, "requiring systems
like Procella and BtrBlocks to employ sampling-based ... and heuristic approaches."

---

## 5. Pros / cons and failure modes

**Pros**
- **Robust across heterogeneous columns** — no single codec wins everywhere, and selection adapts
  per column (and per block) to local distribution.
- **Cheap** (sampling) yet **near-optimal** (within ~3% of best) for the BtrBlocks catalog.
- **Self-describing** — a per-column/per-block codec code makes the format extensible and the
  decoder a simple dispatch; metadata overhead is ~1 byte per unit.
- **Read-path dividend** — the right codec decodes faster, which is exactly what mebo's iteration
  path cares about.

**Cons / failure modes**
- **Sampling misprediction** — 23% of BtrBlocks choices are *not* optimal (though within 2% of it).
  A 1% sample can mis-rank codecs whose cost depends on global structure (run length, outlier rate).
- **Ratio-vs-random-access (the central tension).** Data Blocks **rejected sub-byte/bit-packed
  encodings (e.g. BitWeaving)** because they "increase the cost for point accesses and scans with
  low selectivities by orders of magnitude" — keeping everything byte-addressable for O(1) point
  access. (The headline microbenchmarks are more modest — ~1.8× faster predicate eval, >3× faster
  tuple extraction at 10% selectivity — the "orders of magnitude" applies to per-matching-tuple cost
  at low selectivity.) **This is the single most mebo-relevant finding:** ratio-optimal codecs are
  often the ones that destroy O(1) random access.
- **Metadata / format complexity** — per-unit codec codes, plus decoder support for every codec in
  the catalog, plus (for cascading) the chain each codec encoded into.
- **Forward/backward compatibility** — adding a codec code is forward-incompatible for old readers
  (the same stance mebo already took for `TypeALP`).
- **Encode determinism** — sampling with randomness makes output non-deterministic unless the seed
  is fixed.

**Refuted during verification (do not cite):** "BtrBlocks limits recursion to 1–2 levels" (actual
default is 3); Vortex's "38%/55% smaller than Parquet+ZSTD" and "10–25× faster decode"; a claimed
analytic-FOR estimation detail; a FastLanes per-block bit-packing-adaptivity claim.

---

## 6. How real systems implement it

| System | Adaptive selection? | Notes | Confidence |
|---|---|---|---|
| **BtrBlocks** (SIGMOD 2023) | Yes — sampling trial-encode, per 64k block, cascading depth 3 | The reference design; all numbers above | **High** |
| **Data Blocks** (SIGMOD 2016) | Yes — deterministic stat-driven, per column **and** block, byte-addressable only | Foundational; explicit ratio-vs-random-access tradeoff | **High** |
| **Vortex / SpiralDB** | Yes — BtrBlocks-style sampling (~1%, min 1024, contiguous) | Independent production reimplementation | **High** (design only) |
| **FastLanes** (PVLDB 2023) | **No** — explicitly *deferred* cascading/adaptive selection to future work | Describes the idea; doesn't implement/evaluate it | **High** |
| **Bullion / Procella** | Yes (mentioned) — sampling + heuristics over exhaustive search | Motivation only, no mebo-grade numbers | **High** (motivation) |
| **ALP** (SIGMOD 2024) | (per-block ALP-main vs ALP-RD selection exists) | **Not verified** — the ALP-paper URLs in this run were citation-mismatched | **Unverified** |
| Parquet, ORC, Arrow, DuckDB, Velox/Nimble, InfluxDB/IOx, ClickHouse, Gorilla/Chimp | — | **No claims survived verification** | **Coverage gap** |

> Two citation mismatches surfaced and are flagged for honesty: `arxiv 2404.08901` is the **Bullion**
> paper (CIDR 2025), not ALP; and `p2132-afroozeh.pdf` is the **FastLanes** paper (PVLDB 2023), not
> ALP. The actual ALP paper (Afroozeh, Kuffo & Boncz, SIGMOD 2024, DOI 10.1145/3626717) was not
> directly evidenced here, so any ALP-specific number (e.g. the "3–5× on decimals" figure in mebo's
> own notes) remains **unverified by this investigation**.

---

## 7. Synthesis for mebo

What transfers, given mebo is batch-first columnar, values **zero-alloc iteration**, **O(1) random
access**, and **stable on-disk encoding-type bytes**:

1. **Per-column, not per-sub-block.** BtrBlocks/Data Blocks pick per 64k row-group because their
   blocks are huge; mebo's per-metric column *is* the analogous unit. Start per-column; defer
   intra-column sub-blocking unless profiles show within-column distribution shifts.

2. **Hybrid estimator beats uniform sampling here.** mebo has candidates with *exact* sizes:
   `raw = 8n`, `float32 = 4n` (gated by an O(n) lossless check `f64(f32(v)) == v`), and delta sizes
   cheaply. Use closed-form sizes for those, and a single **stratified-contiguous sample
   trial-encode** (BtrBlocks' ~1% / Vortex's min-1024 recipe) for `ALP-main`, `ALP-RD`, `Chimp`,
   `Gorilla`. Pick the minimum; this is cheaper *and* more accurate than sampling everything.

3. **`raw` is the hard floor.** Adopt BtrBlocks' safeguard: if nothing beats `raw`, store `raw`.
   This makes the selector strictly non-regressive on size.

4. **Make decode/random-access first-class, not just ratio.** This is the load-bearing
   recommendation. ALP/Chimp/Gorilla are bit-packed and do not offer O(1) `At()` (mebo already
   observed this with ALP). `float32`, `raw`, and byte-addressable `delta` preserve O(1) access. A
   ratio-only objective will silently trade away random access. Options: (a) add a tie-break that
   prefers byte-addressable codecs when ratios are within a margin; (b) expose a per-blob policy knob
   (`optimize=ratio|balanced|random-access`); (c) feed an explicit decode-cost term into the score.
   Data Blocks chose the extreme (ban bit-packing entirely) to guarantee point access — mebo doesn't
   have to, but it must decide consciously.

5. **The format mechanism is already in place.** mebo's stable `EncodingType` bytes + the ALP scheme
   byte are exactly the self-describing per-column codec code BtrBlocks relies on. Metadata overhead
   is ~1 byte/column — negligible. A `TypeAdaptive` blob byte (per-column codec recorded in the
   index entry or as a payload prefix) follows the proven pattern; no new infrastructure.

6. **Validate accuracy on time-series before committing the byte.** The 77%-correct / 3.3%-from-optimum
   figures are *BI data*. Time-series has autocorrelation, and Chimp/Gorilla XOR-run + exception
   behavior may need a larger or differently-structured sample. The **measurev2** harness (now with
   realistic profiles: decimal gauges, counter, sparse-constant, bursty scrape) is the right vehicle:
   measure correct-choice rate and bytes-from-optimum per profile, and confirm the hybrid estimator
   tracks a `TRY_ALL` oracle.

**Suggested rollout (de-risking order):**
`float32` codec (simplest, exact size, high value, exercises the dispatch) → **per-column adaptive
selector** with hybrid estimator + `raw` floor + random-access-aware objective → optional sampling
(only if full/analytic estimation proves too slow) → optional cascading (only if profiles show a
clear second-level win).

---

## 8. Open questions (carried from the research)

1. **Production formats not covered** — Parquet, ORC, Arrow, DuckDB, Velox/Nimble (Alpha),
   InfluxDB/IOx, ClickHouse: do they do per-column codec selection, with what sample sizes and
   overheads? None were evidenced; each needs a targeted follow-up.
2. **Verified ALP numbers** — the real ALP / ALP-RD figures and the per-block ALP-main-vs-RD
   selection rule (SIGMOD 2024) were not verified here; directly relevant to mebo's candidate set.
3. **Sample adequacy for time-series** — does a ~1% stratified-contiguous sample preserve BtrBlocks'
   accuracy under time-series autocorrelation and XOR-exception codecs, or is a larger/structured
   sample needed?
4. **Random access with bit-packed candidates** — can a selector that includes both byte-addressable
   and bit-packed codecs still guarantee O(1) `At()`, or must random-access cost enter the selection
   score alongside ratio? (Leaning: it must.)

---

## 9. Sources

Primary (peer-reviewed / source code), high confidence:
- BtrBlocks (SIGMOD 2023) — paper `cs.cit.tum.de/.../btrblocks.pdf`, `dl.acm.org/doi/10.1145/3589263`,
  code `github.com/maxi-k/btrblocks`, CMU 15-721 notes.
- Data Blocks (SIGMOD 2016) — `db.in.tum.de/downloads/publications/datablocks.pdf`.
- FastLanes (PVLDB 2023) — `15721.../p2132-afroozeh.pdf` (deferred-selection evidence).
- Bullion (CIDR 2025) — `arxiv.org/pdf/2404.08901` (motivation).
- Vortex / SpiralDB — `spiraldb.com/post/cascading-compression-with-btrblocks`,
  `github.com/spiraldb/vortex` (design corroboration only; quantitative blog claims refuted).

Fetched but yielding no surviving mebo-relevant claim (coverage gap): DuckDB ALP page, Nimble
README/deepwiki, ClickHouse compression resources, InfluxDB IOx announcement, BtrBlocks-adjacent
benchmark PDFs.

*Research run: 5 angles, 17 sources, 81 claims extracted, 25 verified (21 confirmed / 4 refuted),
adversarial 2-of-3 voting.*
