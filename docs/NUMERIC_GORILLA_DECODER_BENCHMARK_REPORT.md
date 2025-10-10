# Numeric Gorilla Decoder Benchmark Report

**Date:** 2025-10-10
**Current commit:** `11dd16bda4f1362f42d5be34fea3575792a4bee2`

This document compares the performance of the optimized Numeric Gorilla decoder against the baseline implementation using the dedicated benchmark suite in `internal/encoding/numeric_gorilla_bench_test.go`.

## Test Environment
- Host: Linux (amd64)
- CPU: Intel(R) Core(TM) i7-9700K CPU @ 3.60GHz
- Go: 1.24.x toolchain reported by `go test`
- Benchmarks executed with: `go test -run=^$ -bench=NumericGorillaDecoder -benchmem -count=10 ./internal/encoding`

## Methodology
1. Copied the benchmark file into a detached baseline worktree at the target commit.
2. Ran the benchmark command above for both the baseline worktree and the current workspace, each with 10 iterations and memory statistics enabled.
3. Aggregated and compared the two result sets using `benchstat`.

> **Automation:** The whole workflow can now be executed via `make bench-gorilla-decoder BASELINE=<ref> [COUNT=<n> OUTPUT=<dir> EXTRA_FLAGS="--cpuprofile --memprofile"]`, which wraps `scripts/bench_numeric_gorilla_decoder.sh`.
> The script emits text, CSV, and markdown summaries and can optionally capture Go CPU and heap profiles for both baseline and current runs.

Raw outputs:
- 11:24 run (adaptive heuristics only): `.benchmarks/20251010_112438/`
- 11:49 run (zero-hit fast exit): `.benchmarks/20251010_114915/`
- 12:04 run (heuristics removed): `.benchmarks/20251010_120403/`
- Profiles captured for the 11:24 run: `.benchmarks/20251010_112438/{baseline,current}_{cpu,mem}.pprof`

> The script timestamps each run under `.benchmarks/<timestamp>/`; rerun the target before releasing new numbers.

## Results Overview

Full benchstat tables now live in [`NUMERIC_GORILLA_DECODER_BENCHSTAT.md`](./NUMERIC_GORILLA_DECODER_BENCHSTAT.md); this section summarizes the headline movements for quick reference.

### Highlights
- `NumericGorillaDecoderAll/steady_10`: +1.0% — near parity, indicating the small-path optimization doesn’t add overhead after the revert.
- `NumericGorillaDecoderAll/seasonal_150`: +3.9% — markedly better than the +9% regression seen with reuse heuristics, though still slightly slower than baseline.
- `NumericGorillaDecoderAll/alternating_256`: +8.1% — alternating workloads remain the toughest case but improved by ~3 pp versus the heuristic runs.
- `NumericGorillaDecoderAll/alternating_bursts_512`: +4.1% — mixed bursts now fall within a low-single-digit regression.
- `NumericGorillaDecoderAll/repeated_runs_1000`: **−3.5%** — long streams with heavy reuse regain their advantage.
- `NumericGorillaDecoderAt/repeated_runs_1000`: **−5.6%** — random-access on repeated data benefits from the leaner loop.
- `NumericGorillaDecoderAt/alternating_256`: +8.5% — still slower, but materially better than the +13% observed with heuristics.
- **Geometric mean**: +1.9% — overall slowdown dropped from roughly +5.5% (with heuristics) to about +2%.

### Additional metrics
- `B/op`: unchanged (`80 B/op` for streaming benchmarks, `0` for random-access).
- `allocs/op`: unchanged (`3` for streaming benchmarks, `0` for random-access).

## Recent Updates

- Removed the adaptive block-reuse heuristics, returning to the original Gorilla decode loop while keeping the small-sequence fast path.
- Introduced the `alternating_bursts_512` dataset that mixes alternating spikes with long steady tails to stress-test decoder behavior.
- Benchmark automation continues to emit markdown-ready tables (`comparison.md`) and optional CPU/heap profiles for baseline and current runs.

## Profiling Summary

- **CPU (current vs. baseline):** `bitReader.readBits` remains the largest hotspot (~33% vs. 35%), while `gorillaBlockState.next` now accounts for 6.6% of samples versus being negligible before—the reuse heuristics spend measurable time evaluating block reuse before falling back. Decoder entry points (`decodeAllLarge`, `NumericGorillaDecoder.At`) each contribute 8–10% more than the baseline, reflecting the additional control flow introduced by the adaptive gating.
- **Heap:** Allocation patterns are unchanged. Both runs spend ~59% of allocation volume in `NumericGorillaDecoder.All`, confirming that the heuristics preserved the zero-allocation guardrails beyond the benchmark harness itself.
- **Takeaway:** The heuristics successfully cap work on small inputs, but when reuse hits stay low (seasonal/alternating datasets) the extra branching now shows up in the profile. Future work should consider detecting "always miss" patterns sooner to skip `gorillaBlockState.next` entirely.

## Interpretation
- Small sequences (≤64 points) still benefit: both streaming and random-access `steady_10` cases retain their speed-ups from the small-path optimization.
- Seasonal workloads remain a few percent slower, showing that even without heuristics the extra control flow in large sequences still costs some cycles.
- Alternating workloads now sit in the +4–8% range rather than +11–13%, indicating the revert removed most of the added overhead but the inherent branching cost in Gorilla’s format still dominates worst-case patterns.
- Long repeated runs move back into the win column, confirming the leaner loop helps scenarios with frequent reuse hits.
- With the heuristics gone, further improvements should come from reducing per-bit overhead (e.g., tighter `bitReader.readBits` implementation) instead of more branch-heavy gating.

## Reproduction Steps

```bash
# Run the automated comparison (profiles optional)
COUNT=10 make bench-gorilla-decoder BASELINE=HEAD~1

# Optionally add CPU/heap profiles
COUNT=5 EXTRA_FLAGS="--cpuprofile --memprofile" make bench-gorilla-decoder BASELINE=HEAD~1 OUTPUT=.benchmarks/$(date +%Y%m%d_%H%M%S)

# Inspect the emitted markdown table
cat .benchmarks/<timestamp>/comparison.md

# Compare CPU profiles (optional when profiles were captured)
go tool pprof -top .benchmarks/<timestamp>/baseline_cpu.pprof
go tool pprof -top .benchmarks/<timestamp>/current_cpu.pprof
```

## Next Steps
- Profile `bitReader.readBits` to identify opportunities for bit-buffer specialization or branch reduction (still >30% of CPU time in earlier profiles).
- Evaluate alternating and seasonal workloads with real customer traces to ensure synthetic regressions mirror production.
- Consider optional, opt-in reuse heuristics guarded behind benchmarks if future tuning justifies the added complexity.
