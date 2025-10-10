# Numeric Gorilla Decoder Benchstat Tables

This appendix captures the raw `benchstat` markdown emitted by recent comparison runs. Each subsection links back to the timestamped artifacts under `.benchmarks/` for reproducibility.

---

## 2025-10-10 11:24:38 UTC — Miss-detector prototype

- **Command:** `go test -run=^$ -bench=NumericGorillaDecoder -benchmem -count=10 ./internal/encoding`
- **Artifacts:** `.benchmarks/20251010_112438/`
- **Context:** First run after adding the adaptive reuse hit heuristics.

### sec/op

| Benchmark | Baseline | Current | Δ vs baseline | p-value |
| --- | --- | --- | --- | --- |
| `NumericGorillaDecoderAll/steady_10` | 327.7 ns ±6% | 296.9 ns ±7% | **-9.40%** | 0.001 (n=10) |
| `NumericGorillaDecoderAll/seasonal_150` | 2.986 µs ±0% | 3.248 µs ±0% | +8.76% | <0.001 (n=10) |
| `NumericGorillaDecoderAll/repeated_runs_1000` | 8.342 µs ±2% | 8.471 µs ±0% | +1.55% | 0.017 (n=10) |
| `NumericGorillaDecoderAll/alternating_256` | 5.278 µs ±0% | 5.878 µs ±0% | +11.38% | <0.001 (n=10) |
| `NumericGorillaDecoderAll/alternating_bursts_512` | 10.23 µs ±0% | 11.08 µs ±0% | +8.26% | <0.001 (n=10) |
| `NumericGorillaDecoderAt/steady_10` | 1.023 µs ±1% | 1 µs ±1% | **-2.25%** | <0.001 (n=10) |
| `NumericGorillaDecoderAt/seasonal_150` | 34.29 µs ±0% | 36.83 µs ±1% | +7.43% | <0.001 (n=10) |
| `NumericGorillaDecoderAt/repeated_runs_1000` | 97.13 µs ±0% | 95.51 µs ±0% | **-1.67%** | <0.001 (n=10) |
| `NumericGorillaDecoderAt/alternating_256` | 72.13 µs ±1% | 81.65 µs ±1% | +13.21% | <0.001 (n=10) |
| `NumericGorillaDecoderAt/alternating_bursts_512` | 140.4 µs ±0% | 154.4 µs ±1% | +9.98% | <0.001 (n=10) |

**Geometric mean:** 10.43 µs vs 10.9 µs (+4.49%)

### B/op

| Benchmark | Baseline | Current | Δ vs baseline | p-value |
| --- | --- | --- | --- | --- |
| `NumericGorillaDecoderAll/steady_10` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/seasonal_150` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/repeated_runs_1000` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_256` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_bursts_512` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/steady_10` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/seasonal_150` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/repeated_runs_1000` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_256` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_bursts_512` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |

**Geometric mean:** — vs — (+0.00%)

### allocs/op

| Benchmark | Baseline | Current | Δ vs baseline | p-value |
| --- | --- | --- | --- | --- |
| `NumericGorillaDecoderAll/steady_10` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/seasonal_150` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/repeated_runs_1000` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_256` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_bursts_512` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/steady_10` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/seasonal_150` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/repeated_runs_1000` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_256` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_bursts_512` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |

**Geometric mean:** — vs — (+0.00%)

---

## 2025-10-10 11:49:15 UTC — Zero-hit fast exit

- **Command:** `go test -run=^$ -bench=NumericGorillaDecoder -benchmem -count=10 ./internal/encoding`
- **Artifacts:** `.benchmarks/20251010_114915/`
- **Context:** After gating the fast exit on “no reuse hits observed yet.”

### sec/op

| Benchmark | Baseline | Current | Δ vs baseline | p-value |
| --- | --- | --- | --- | --- |
| `NumericGorillaDecoderAll/steady_10` | 290.4 ns ±1% | 290 ns ±3% | ~ | 0.897 (n=10) |
| `NumericGorillaDecoderAll/seasonal_150` | 2.978 µs ±0% | 3.25 µs ±0% | +9.13% | <0.001 (n=10) |
| `NumericGorillaDecoderAll/repeated_runs_1000` | 8.34 µs ±0% | 8.456 µs ±0% | +1.38% | <0.001 (n=10) |
| `NumericGorillaDecoderAll/alternating_256` | 5.277 µs ±0% | 5.885 µs ±0% | +11.50% | <0.001 (n=10) |
| `NumericGorillaDecoderAll/alternating_bursts_512` | 10.33 µs ±1% | 11.11 µs ±1% | +7.54% | <0.001 (n=10) |
| `NumericGorillaDecoderAt/steady_10` | 1.022 µs ±0% | 999.1 ns ±0% | **-2.24%** | <0.001 (n=10) |
| `NumericGorillaDecoderAt/seasonal_150` | 34.24 µs ±0% | 36.82 µs ±1% | +7.51% | <0.001 (n=10) |
| `NumericGorillaDecoderAt/repeated_runs_1000` | 96.96 µs ±1% | 95.15 µs ±1% | **-1.86%** | <0.001 (n=10) |
| `NumericGorillaDecoderAt/alternating_256` | 72.02 µs ±0% | 81.72 µs ±0% | +13.47% | <0.001 (n=10) |
| `NumericGorillaDecoderAt/alternating_bursts_512` | 140.3 µs ±0% | 154.3 µs ±1% | +10.00% | <0.001 (n=10) |

**Geometric mean:** 10.3 µs vs 10.87 µs (+5.49%)

### B/op

| Benchmark | Baseline | Current | Δ vs baseline | p-value |
| --- | --- | --- | --- | --- |
| `NumericGorillaDecoderAll/steady_10` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/seasonal_150` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/repeated_runs_1000` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_256` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_bursts_512` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/steady_10` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/seasonal_150` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/repeated_runs_1000` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_256` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_bursts_512` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |

**Geometric mean:** — vs — (+0.00%)

### allocs/op

| Benchmark | Baseline | Current | Δ vs baseline | p-value |
| --- | --- | --- | --- | --- |
| `NumericGorillaDecoderAll/steady_10` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/seasonal_150` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/repeated_runs_1000` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_256` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_bursts_512` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/steady_10` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/seasonal_150` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/repeated_runs_1000` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_256` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_bursts_512` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |

**Geometric mean:** — vs — (+0.00%)

---

## 2025-10-10 12:04:03 UTC — Reverted reuse heuristics

- **Command:** `go test -run=^$ -bench=NumericGorillaDecoder -benchmem -count=10 ./internal/encoding`
- **Artifacts:** `.benchmarks/20251010_120403/`
- **Context:** After removing the adaptive reuse-hit heuristics and returning to the baseline decode loop.

### sec/op

| Benchmark | Baseline | Current | Δ vs baseline | p-value |
| --- | --- | --- | --- | --- |
| `NumericGorillaDecoderAll/steady_10` | 290.1 ns ±3% | 293.1 ns ±4% | ~ | 0.165 (n=10) |
| `NumericGorillaDecoderAll/seasonal_150` | 2.981 µs ±0% | 3.097 µs ±0% | +3.87% | <0.001 (n=10) |
| `NumericGorillaDecoderAll/repeated_runs_1000` | 8.317 µs ±0% | 8.03 µs ±1% | **-3.45%** | <0.001 (n=10) |
| `NumericGorillaDecoderAll/alternating_256` | 5.273 µs ±0% | 5.702 µs ±0% | +8.14% | <0.001 (n=10) |
| `NumericGorillaDecoderAll/alternating_bursts_512` | 10.23 µs ±0% | 10.65 µs ±0% | +4.14% | <0.001 (n=10) |
| `NumericGorillaDecoderAt/steady_10` | 1.023 µs ±1% | 994 ns ±0% | **-2.83%** | <0.001 (n=10) |
| `NumericGorillaDecoderAt/seasonal_150` | 34.25 µs ±0% | 35.12 µs ±1% | +2.52% | <0.001 (n=10) |
| `NumericGorillaDecoderAt/repeated_runs_1000` | 97.01 µs ±1% | 91.55 µs ±0% | **-5.63%** | <0.001 (n=10) |
| `NumericGorillaDecoderAt/alternating_256` | 72.01 µs ±0% | 78.1 µs ±0% | +8.45% | <0.001 (n=10) |
| `NumericGorillaDecoderAt/alternating_bursts_512` | 140.3 µs ±0% | 145.8 µs ±0% | +3.91% | <0.001 (n=10) |

**Geometric mean:** 10.29 µs vs 10.49 µs (+1.91%)

### B/op

| Benchmark | Baseline | Current | Δ vs baseline | p-value |
| --- | --- | --- | --- | --- |
| `NumericGorillaDecoderAll/steady_10` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/seasonal_150` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/repeated_runs_1000` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_256` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_bursts_512` | 80 ±0% | 80 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/steady_10` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/seasonal_150` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/repeated_runs_1000` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_256` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_bursts_512` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |

**Geometric mean:** — vs — (+0.00%)

### allocs/op

| Benchmark | Baseline | Current | Δ vs baseline | p-value |
| --- | --- | --- | --- | --- |
| `NumericGorillaDecoderAll/steady_10` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/seasonal_150` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/repeated_runs_1000` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_256` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAll/alternating_bursts_512` | 3 ±0% | 3 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/steady_10` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/seasonal_150` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/repeated_runs_1000` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_256` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |
| `NumericGorillaDecoderAt/alternating_bursts_512` | 0 ±0% | 0 ±0% | ~ | 1.000 (n=10) |

**Geometric mean:** — vs — (+0.00%)
