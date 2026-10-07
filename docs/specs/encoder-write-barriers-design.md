# Design: encoder write barriers, measurev2 encode warm-up, and gate 3 thresholds

**Date:** 2026-10-06
**Status:** implemented (Parts 1 and 2, each Codex-reviewed); validation passed 2026-10-06 (see Results).
Part 3's gate-3 limits served one validation and were then removed, with `bytes_rel` back at 2% (owner, 2026-10-06).
Gate 2 now exempts the shared-timestamp `TimestampAt` cells from its per-cell limit, as gate 4 does (owner, 2026-10-06).
Both exemptions were removed on 2026-10-07, after `docs/specs/index-entry-by-pointer-design.md` removed the two speeds.
Approved v2.1 by the owner on 2026-10-06 (the gate-3 limits and the order of work).
v2.1 addresses the confirmatory review of v2 (`tmp/reviews/encoder-write-barriers-spec-codex-review-v2.md`):
diagnostics for every inline variant, and the stale Chimp capacity comments.
v2 addresses the Codex review of v1 (`tmp/reviews/encoder-write-barriers-spec-codex-review-v1.md`, not committed):
a Chimp byte oracle, an explicit threshold approval path, a Part-1-only measurement,
an inline variant with more headroom, and corrected site and record-size details.
Owner request (2026-10-06): research the encoder write barriers and the one-off allocation in reverse-order encode cells,
and consider loosening gate 3.
Research artifacts (not committed): `tmp/encoder-write-barriers-research-2026-10-06/`
(barrier sites, prototype diffs, compiler diagnostics, byte-parity results, probe tests and their outputs).
Earlier evidence: `tmp/measurev2-calibration-2026-10-05/findings.md` and the Results of
`docs/specs/measurev2-fast-report-runs-design.md`.

## Goal

1. Remove the GC write barrier that Gorilla and Chimp encoders take on every 8-byte spill,
   without changing a single output byte or the encoders' inlining.
2. Remove the one-off cold-pool allocation from measurev2 encode cells in both run orders.
3. Give gate 3 margin over its known, systematic tail.

## Part 1: write barriers in the value encoders

### Mechanism

While a GC is marking, the runtime sets `runtime.writeBarrier`,
and every store of a pointer into a heap object checks it and calls `runtime.gcWriteBarrierN`,
which records the old and new pointers in a per-P buffer that `wbBufFlush1` drains.
How the compiler treats a store to a slice field `x.B` decides whether a hot path pays this:

| Form | Non-growing path stores | Barrier on the hot path |
|---|---|---|
| `x.B = append(x.B, ...)` (same expression both sides) | length only | no; only after `growslice` |
| `x.B = x.B[:n]` (self-reslice, low bound omitted) | length only | no |
| `x.B = f(x.B, ...)`, including an inlined `binary.BigEndian.AppendUint64` | pointer, length and capacity | yes, every call |

### Where encoders take barriers

`go build -gcflags=-S` at 902850b (Go 1.26.7, linux/amd64) lists every `gcWriteBarrier` call per function
(`write-barrier-sites-go1.26.7.txt`).
Sites inside `ByteBuffer.Grow` (`byte_buffer_pool.go:130`), constructors and `Finish` are left out below.
"Runs" is how often a site executes; "Matters" is whether it is frequent in the report's workloads.

| Encoder | Site | Runs | Matters |
|---|---|---|---|
| Gorilla | `gorilla.go:315` (`appendBits`), `:251` (unchanged-value spill) | every 8 output bytes | **yes** |
| Chimp | `chimp.go:305` (`appendBits`), `:241` (unchanged-value spill) | every 8 output bytes | **yes** |
| Delta timestamps | `delta.go:266`, `:295`, `:316` (`binary.AppendUvarint`) | first timestamp of a metric, and zigzag values ≥ 2²¹ | no |
| DeltaPacked | `deltapacked.go:410` (`binary.AppendUvarint`) | first two timestamps of a metric | no |
| Simple8b | `flush` header and exceptions | per metric, per exception | no |
| ALP | `encodeMain`/`encodeRD` headers and exceptions; `encodeRaw` (`alp.go:353`) | per column, per exception; **per value** in the raw scheme | no; the raw scheme is a rare fallback |
| ALP-RLE | `alp_runs.go:350`, `:352` | per column | no |
| BP128 | `c.words = bp128PackBlock(...)` (`bp128.go:249`), `ensureDods` | per 128-value block, per encode | no |
| Raw | none outside `Grow` | | no |
| `blob.NumericEncoder` | none in `AddDataPoint`; `StartMetricID`/`EndMetric` | per metric | no |

This corrects the 2026-10-05 findings and the measurev2 spec's Results,
which say the Delta timestamp encoder appends the same way as Gorilla:
its byte appends use the in-place form, so its non-growing path stores only the length.

The blob encoder calls the value encoder's `Write` once per point,
so a Gorilla or Chimp encode takes one barrier-guarded store per 8 bytes of value payload:
about 7,000 per encode on mix_monitoring (100 metrics × 150 points)
and about 12,000 on decimal_gauge_4dp, worst_case and legacy_random_walk (`spills-per-encode.txt`).

### Evidence that it matters

- Merged CPU profiles from 2026-10-05 (10 fresh against 10 post-history processes, Delta + Gorilla encode):
  runtime and GC flat time rose from 14.4% to 21.8% of encode time;
  the gap was `gcWriteBarrier`/`wbBufFlush1` (150 → 200 ms), mark assists (`gcDrainN`) and `mallocgc`.
- Gate 3 compares each cell in one process for all data sets against one process per data set.
  In the two passes with the start-up warm-up, every cell beyond 5%
  (21 in the recalibration, 19 in the validation)
  was a Gorilla or Chimp encode (delta-gorilla, shared-deltapacked-gorilla, shared-deltapacked-chimp),
  and every one was slower in the combined process.
  The worst cells are on worst_case, legacy_random_walk and decimal_gauge_4dp, the data sets with the most spills.
  The calibration without the warm-up had a wider tail of 29 cells, mostly the same encodes.

### Design

Replace the four spill sites with a self-reslice and an in-place store.
In `appendBits`, also update `e.bitCount` in place, which removes the `total` local and the `else` branch:

```go
func (e *NumericGorillaEncoder) appendBits(value uint64, numBits int) {
	m := value << (64 - uint(numBits))
	e.bitBuf |= m >> uint(e.bitCount)

	e.bitCount += numBits
	if e.bitCount >= 64 {
		e.bitCount -= 64
		n := len(e.buf.B)
		e.buf.B = e.buf.B[:n+8]
		binary.BigEndian.PutUint64(e.buf.B[n:], e.bitBuf)
		e.bitBuf = m << uint(numBits-e.bitCount) // numBits - (old+numBits-64) = 64 - old
	}
}
```

The two unchanged-value spills in `writeValue` use the same three lines.
Chimp's `appendBits` is identical.

Inline cost of `appendBits` (budget 80; Gorilla diagnostics for every row in `compiler/`,
Chimp's chosen variant in `candidate-headroom-m2.txt`):

| Variant | Cost | Inlinable |
|---|---:|---|
| baseline (`AppendUint64`) | 66 | yes |
| reslice + `PutUint64`, `spill` local kept | 83 | no |
| reslice + `PutUint64`, `spill` folded | 78 | yes |
| reslice + `PutUint64`, `bitCount` updated in place (chosen) | 69 | yes |
| 8-argument in-place `append` | 99 | no |

With the chosen variant, `appendBits` is inlined at all 14 call sites (6 Gorilla, 8 Chimp), as at baseline;
the only remaining barriers in the two encoders are in `Grow`, the constructors and `Finish`;
and the `internal/encoding` and `blob` tests pass (`prototype-headroom.diff`).

**Capacity invariant.**
The old form grows silently if a caller under-reserves; the new form panics on the bounds check instead.
The invariant already holds and the code comments state it:
`Write` reserves 16 bytes per value and `WriteSlice` reserves `len(values)*10 + 16`.
The largest record is 77 bits for Gorilla (13-bit header plus 64 bits)
and 69 bits for Chimp (the new-leading branch: 5-bit header plus at most 64 bits;
the 11-bit trailing-zero branch carries at most 57 significant bits, 68 in all).
With at most 63 bits pending, one value spills at most twice (16 bytes),
and n values spill at most 8 + 9.625n bytes, within `10n + 16`.
A growth fallback through a direct self-append would keep the non-growing path barrier-free,
but the 8-argument append exceeds the inline budget, so the bounds-check panic is kept as the invariant check.
The capacity comments in `chimp.go` still describe an 11-bit header plus 64 bits (`Write`) and about 75 bits (`WriteSlice`);
the same change corrects them to the 69-bit maximum.
A new test drives both encoders through `Write` and `WriteSlice` with attainable worst-case records,
two-spill values and runs of unchanged values, to hold the invariant at its maximum spill density.

**Byte oracle.**
Gorilla already compares against reference bytes (`gorilla_test.go`); Chimp checks only decoded values and lengths.
Before the code changes, the implementation adds a Chimp reference-byte test generated from the baseline encoder,
covering unchanged-value spills, new and reused windows, two-spill values, mixed `Write`/`WriteSlice`,
partial-byte flushes, and several metrics separated by `Bytes` and `Reset`.
The research prototype already passes a differential check against the baseline (`byte-parity.txt`):
1,805 encoder-level cases per codec over those patterns
and all 192 Gorilla/Chimp blobs of the report profiles hash identically.

**Hot-path guard.**
The inline margin can shrink with an edit or a Go release.
The implementation adds `scripts/check-encoder-hotpath.sh`,
run before each commit that touches these encoders and in the PR checklist, not in `make test`.
For `./internal/encoding/value/gorilla` and `./internal/encoding/value/chimp` it requires
`inlining call to (*Numeric{Gorilla,Chimp}Encoder).appendBits` at every call site in `Write`, `WriteSlice` and `writeValue`,
and no `gcWriteBarrier` in `appendBits` or `writeValue`;
barriers in `Write` and `WriteSlice` are allowed only on the `Grow` path.
It records the toolchain, architecture and flags it ran with.

**Out of scope.**
The other sites in the table keep their current form.
Keeping the buffer in a local across a loop helps only `WriteSlice`, which the blob encoder does not call.

### Verification

1. Output: the Chimp and Gorilla reference-byte tests,
   the existing codec and `blob` tests, `tests/compat/` (cross-version decoding)
   and measurev2 gate 1 (size fields) as separate evidence.
2. Allocations: allocs/op and B/op unchanged on every encode cell.
3. Part 1 alone, with the current harness, on an idle machine:
   the gate-3 commands of `validate.sh` (three combined and three isolated invocations, about 3 minutes),
   expecting the encode median ratio (1.0136 in the validation) to move toward 1.00
   and the Gorilla/Chimp encode tail to shrink;
   and one `make bench-report` compared with the report at 902850b on Gorilla and Chimp encode cells.
   Both sides of the gate-3 ratio run the same binary, so code placement does not affect it;
   the report is layout-averaged.
4. `make lint`, the hot-path guard, and `make test` run by the owner.

## Part 2: one-off allocation in measurev2 encode cells

### Mechanism

Verified with `runtime.ReadMemStats` byte counts in a scratch copy (`pool-gc-probe.txt`); nothing was timed.

- `testing.Benchmark` runs `runtime.GC()` once per call;
  a `b.Loop` benchmark function runs once and ramps up inside the loop.
- A `sync.Pool` entry left untouched survives one GC in the victim cache and is gone after a second.
  The pool contract allows entries to disappear at any time, and `ByteBufferPool.Put` drops buffers over 1 MiB,
  so the description below is what happens in the report configuration, not a guarantee.
- `measureDataset` encodes each combo for its sizes immediately before that combo's timed cells.
  In forward order the encode cell comes first, one GC later, and finds the buffers.
  In reverse order it comes last, after at least one other cell, so two or more GCs separate it from the last encode;
  it finds the pool empty, and its first iteration grows each buffer from 16 KiB (16, 32, 48, 64, 80, 160, 320 KiB),
  allocating 0.2–0.8 MiB extra on mix_monitoring.
  The decode cell before it is not the cause; any two GCs empty the pool.
- The blob encoder draws its timestamp and value buffers from the same pool,
  and a buffer sized for one role can come back for the other,
  so the iteration after a cold start grows again.
  For the same reason forward order is not clean either:
  after its one GC some combos still allocate up to 1.8% extra.

### Design

`benchmarkRunner.run` calls the body twice inside the benchmark function,
after `testing.Benchmark`'s `runtime.GC()` and before `b.ResetTimer()`;
a warm-up error is returned like a body error.
`ResetTimer` and the first `b.Loop` call reset elapsed time and allocation counters,
so warm-up time and allocations are excluded.

B/op excess over a steady-state reference, mix_monitoring, all 30 combos, 50 ms (`warmup-k-probe-50ms.txt`):

| State | Min | Median | Max |
|---|---:|---:|---:|
| forward, as today | −0.01% | +0.23% | +1.83% |
| reverse, as today | +0.63% | +1.14% | +2.93% |
| reverse, 1 warm-up call | +0.11% | +0.26% | +0.91% |
| reverse, 2 warm-up calls | −0.29% | +0.01% | +0.27% |
| reverse, 3 warm-up calls | −0.29% | +0.01% | +0.40% |

With two calls forward order measured −0.15% to +0.03% (`warmup-forward-k2.txt`), and a third call adds nothing,
so two calls are the warm-up policy for the report configuration.
The probe covered encode on the main data set only;
the all-profile validation in both orders is what confirms it.
They cost the sum of one op over all 420 cells twice, about 83 ms per invocation, or 1.3 s per report run.
The start-up warm-up (`warmUp`) stays: it addresses the runtime's GC state, not the pool.

The warm-up runs before every operation, not only encode.
The bodies repeat their work and overwrite their checksums, so this is functionally safe,
but it also warms caches and shifts GC state for decode, iteration and random access,
which is one more reason gates 2–4 run again.

### Consequences

- This changes the timing method,
  so gates 2, 3 and 4 run again against the frozen thresholds (`validate.sh`, about 34 minutes, idle machine).
- `bytes_rel` can return from 4% toward the spec's original 2%
  only through the approval path in "Order of work".
- The report's note that B/op of some encode cells reads about 1.5% high,
  and the corresponding sentences in the measurev2 spec, are removed
  only if that validation shows reverse-order encode B/op agreeing with forward order over all profiles.

## Part 3: gate 3 thresholds

### Evidence

Gate 3 ratio |c − 1| over 420 cells in each pass:

| Pass | Within 5% | 6% | 7% | 8% | p95 | Worst |
|---|---:|---:|---:|---:|---:|---:|
| calibration (no warm-up) | 93.1% | 94.0% | 95.2% | 96.7% | 6.79% | 15.51% |
| recalibration (warm-up) | 95.0% | 96.9% | 97.9% | 98.3% | 4.92% | 10.60% |
| validation (warm-up) | 95.5% | 96.7% | 97.9% | 98.8% | 4.57% | 10.72% |

The validation passes the current limits, so this is margin, not a repair:
95.5% against the required 95%, and a worst cell of 10.72% against 11%.
The tail is a systematic GC-state difference, not noise.
In both passes with the warm-up every tail cell is a Gorilla or Chimp encode, slower in the combined process.
The isolated baseline is the less representative side:
each of its invocations measures its data set shortly after process start,
while the combined process is closer to the steady state of a long-running encoder.

### Proposal

Gate 3 gets its own limits;
gates 2 and 4 keep the shared `cell_within` (5%) and `cell_max` (11%):

```json
"gate3": {"cell_within": 0.07, "cell_max": 0.15}
```

`acceptance.py` merges the `gate3` object over the shared values for gate 3 only,
accepts no keys other than `cell_within` and `cell_max` there,
and never changes the shared values.
A test confirms that gates 2 and 4 evaluate the stored validation data exactly as before.
The share (95%), `op_median_within` (3%) and the allocation rules stay.
On the two passes with the warm-up, 7% holds 97.9% of cells,
and 15% is 1.4 times the worst cell.
Re-evaluating the stored validation data needs no new benchmark run.

**Outcome (2026-10-06).**
The limits were frozen and used for the validation of Parts 1 and 2,
which put gate 3 at 99.8% within 5% with a worst cell of 6.32%, inside the shared limits.
With the tail gone, a 15% gate-3 limit would only hide a regression such as the spill barrier coming back,
so the owner had the `gate3` limits and their `acceptance.py` support removed
and `bytes_rel` returned to 2% (worst B/op deviation in that validation: 0.87%).
That validation counts as calibration evidence, and an independent `validate.sh` run validates the tightened values.

## Order of work

1. **Part 3.**
   The owner approves the gate-3 limits; they are frozen in `acceptance_thresholds.json` as a reviewed change,
   with the `acceptance.py` support and the measurev2 spec text.
   The stored validation data is re-evaluated against them.
2. **Part 1** on its own branch, with a Codex review.
   On an idle machine the owner confirms, the Part-1-only measurement (Verification 3) with the current harness,
   so any improvement is attributable to the barrier removal alone.
3. **Part 2**, with a Codex review.
4. **Validation.**
   On an idle machine, one `validate.sh` run (gates 1–6) against the frozen thresholds;
   this validates Parts 1 and 2 together.
5. **Optional tightening.**
   If that run suggests tighter limits (`bytes_rel` toward 2%, or gate 3 back to the shared limits),
   it counts as calibration evidence:
   the owner approves the new values, they are frozen, and an independent `validate.sh` run validates them.
6. **Report.**
   `make bench-report` on the final configuration and a regenerated `docs/performance.md`.

## Results

### Part 1 alone (2026-10-06)

Measured on the 9950X3D, pinned to CPU 6, with the harness of 902850b (before Part 2),
baseline 902850b against the Part 1 encoder commit in one session, machine otherwise idle.
Raw data: `tmp/encoder-write-barriers-research-2026-10-06/part1-measure/`
(`part1-measure.log`, `part1-report-comparison.txt`).

Gate 3 (three combined and three isolated invocations, layout 0):

| | Baseline | Part 1 |
|---|---:|---:|
| cells within 5% | 96.2% | 100.0% |
| worst cell | 13.69% | 3.55% |
| cells beyond 5% | 16, all Gorilla/Chimp encode | 0 |
| Gorilla/Chimp encode median ratio | 1.0360 | 1.0082 |
| all encode median ratio | 1.0131 | 1.0079 |

The baseline's worst cell was beyond the shared 11% limit,
so on this run the old gate 3 would have failed and the gate-3 limits of Part 3 were needed.

Layout-averaged report runs (`make bench-report`, 570 cells), Part 1 against baseline:

| Cells | n | Median | Range |
|---|---:|---:|---|
| Gorilla/Chimp encode | 69 | −14.0% | −20.8% to −2.0% |
| other encode | 81 | −0.2% | −1.0% to +1.3% |
| Gorilla/Chimp non-encode | 186 | −0.1% | −3.4% to +28.1% |
| other non-encode | 234 | +0.3% | −2.0% to +28.4% |

On mix_monitoring every Gorilla/Chimp encode combo is 10.9–15.1% faster.
The only non-encode cells beyond ±4% are the 15 shared-timestamp `TimestampAt` cells, all +28%,
which is the per-binary-file bimodality the measurev2 spec describes, on every value codec.
allocs/op is unchanged on every Gorilla/Chimp encode cell, and B/op within 0.35%.

### Validation of Parts 1 and 2 (2026-10-06)

`validate.sh` with Parts 1 and 2 against the thresholds then frozen (gate 3 with its 7%/15% limits), machine otherwise idle;
raw artifacts in `tmp/measurev2-validation-2026-10-06/` (`validate.log`, `followup-analysis.txt`).
Every gate passed.

| | Validation 2026-10-05 | Validation 2026-10-06 |
|---|---|---|
| gate 2 stable / within 5% / worst | 95.0% / 100.0% / 5.23% | 96.7% / 99.8% / 8.91% |
| gate 2 reverse-order encode B/op excess, median / worst | +1.03% / +3.02% | +0.00% / +0.87% |
| gate 3 within 5% / within 7% / worst | 95.5% / 97.9% / 10.72% | 99.8% / 100.0% / 6.32% |
| gate 4 within 5% / worst / exempt worst | 96.4% / 4.50% / 14.59% | 100.0% / 2.46% / 0.28% |
| gate 4 pairs decided / opposite | 1,869 / 0 | 1,872 / 0 |
| gate 6 wall time | 6.87 min | 6.89 min |

Reverse-order encode B/op now agrees with forward order over all report profiles,
so the report's note that B/op of some encode cells reads about 1.5% high is removed.
Gate 3 would also pass the shared limits (5% for 95% of cells, 11% for every cell).
Gate 4 lists four ALP encode cells whose allocs/op differed between runs (for example 145 and 144 around a mean of 145.03):
the truncated mean on an integer boundary that `allocs_abs` already allows for, not a failure.

### Independent validation of the tightened thresholds (2026-10-06)

`validate.sh` with the gate-3 limits removed and `bytes_rel` at 2%, machine otherwise idle;
raw artifacts in `tmp/measurev2-validation-2026-10-06b/`.
Gates 1, 3, 4, 5 and 6 passed: gate 3 had every cell within 5% (worst 4.88%),
gate 4 every cell within 5% (worst 2.27%, exempt worst 0.31%), 1,876 pairs decided and none in opposite directions.
**Gate 2 failed** on six cells beyond 11%, all shared-timestamp `TimestampAt` on `mix_monitoring`:
in the first run (A↑, 1 s) shared-delta-raw and the five shared-raw combos ran at about 2,105 ns,
then the binary switched to about 1,665 ns for the rest of that run and for the other three runs.
That is the per-binary-file bimodality the measurev2 spec describes for gate 4, which exempts these 15 cells from `cell_max`;
gate 2 has no such exemption.
The other 390 cells were within 4.96%, and the B/op tolerance was never the limit (worst deviation 0.87% in the previous validation).
The first validation's gate 2 saw these cells move too (63% of its `TimestampAt` cells stable) but stayed under 11% (worst 8.91%).
The owner approved extending gate 4's exemption to gate 2 (2026-10-06):
the 15 cells are exempt from gate 2's per-cell limit and still count toward its share and per-operation median.
The mechanism is the documented one rather than a value fitted to this run, so the stored run was re-evaluated, not repeated:
gate 2 passes with the cells held to the limit at worst 4.96% and the exempt cells at worst 11.62%,
and gates 3, 4, 5 and 6 pass as before (`gate2-reevaluated-exemption.txt`).

