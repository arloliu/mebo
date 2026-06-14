# ALP Value-Codec Wiring — Implementation Plan

> **Execution model (workflow-friendly):** This plan is a DAG of phases. Within a
> phase, tasks marked **∥ parallel** touch disjoint files and may be dispatched to
> concurrent agents; tasks marked **→ sequential** share a file or depend on a prior
> task's output. Each task is self-contained: it lists its files, a TDD step
> sequence, and a verification command that an agent can run to self-check. Each
> phase ends with a **verification gate** that must pass before the next phase.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the existing ALP codec into the mebo numeric blob format as an explicit, user-selectable value encoding (`TypeALP = 0x6`), and refactor `tests/measurev2` to validate it on realistic decimal data.

**Architecture:** ALP is already implemented and lossless at `internal/encoding/numeric_alp.go` (encoder + decoder, both-endian, `At`/`All`). Wiring = (a) one correctness prerequisite on the codec, (b) registering the byte and threading it through every encoder/decoder dispatch site that currently switches on `TypeGorilla`/`TypeChimp`, and (c) extending the benchmark harness. BP128 timestamps are **out of scope** (deferred — see `docs/specs/alp-bp128-wiring-design.md`).

**Tech Stack:** Go 1.26, `internal/encoding` (codec), `blob` (encoder/decoder/dispatch), `section` (flag validation), `format` (type bytes), `tests/measurev2` (harness). Linter: `go tool -modfile=linter.go.mod golangci-lint run`.

---

## Workflow orchestration (DAG)

```
Phase 0  ┌─ Track A (numeric_alp.go):  T0.1 uint32 widen → T0.2 DecodeAll     ─┐
(found.) └─ Track B (tests/measurev2): T0.3 generators (∥ with Track A)        ─┘
                                   │
Phase 1  T1.1 format byte (first) → { T1.2 section ∥ T1.3 encoder ∥ T1.4 decoder } → T1.5 parity test
(wiring)                           │
Phase 2  T2.1 e2e ratio validation → T2.2 measurev2 ALP matrix (needs T0.3) → T2.3 compat/CHANGELOG
```

- **Phase 0 gate:** `go test ./internal/encoding/ -run 'TestNumericALP' -count=1` passes; measurev2 builds (`go build ./tests/measurev2/`).
- **Phase 1 gate:** `go test ./blob/ ./section/ ./internal/encoding/ -count=1` passes; lint clean.
- **Phase 2 gate:** measurev2 reports ALP beating Chimp on decimal profiles; full `go test ./... -count=1` passes; lint clean.

## Execution configuration

Run as **phased workflows** — one `Workflow()` invocation per phase; the orchestrator runs the phase gate and commits between phases (the ∥ tasks within a phase touch disjoint files, so concurrent agents are safe). **Agents edit + self-verify** (run their task's test/build command) but do **not** `git commit` — the orchestrator commits per phase after the gate passes (avoids parallel-commit races).

Per-task model + effort (default model when unspecified: **Sonnet**; every task is test-first):

| Task | Model | Effort / rigor |
|---|---|---|
| T0.1 uint32 widen + strides + size formulas | **Opus** | High — offset arithmetic across many sites; losslessness + ratio critical |
| T0.2 `DecodeAll` | Sonnet | Standard TDD (trivial `All` wrap) |
| T0.3 measurev2 generators | Sonnet | Standard TDD |
| T1.1 format byte | Sonnet | Standard |
| T1.2 section allow-list | Sonnet | Standard |
| T1.3 encoder factory + flush + config | Sonnet | Standard (flush hook is the care point) |
| T1.4 decoder dispatch (3 sites + per-case engine) | **Opus** | High — missed-site / engine-scope / losslessness risk |
| T1.5 dispatch-parity test | **Opus** | High — the safety net; must exercise all four read paths |
| T2.1 e2e ratio | Sonnet | Standard |
| T2.2 measurev2 matrix | Sonnet | Standard |
| T2.3 compat + CHANGELOG | Sonnet | Standard |
| Final verification | **Opus** | Adversarial — full `go test ./...` + lint + review the branch diff for losslessness/format correctness |

## File structure

| File | Responsibility | Phase |
|---|---|---|
| `internal/encoding/numeric_alp.go` | widen exception fields to uint32; add `DecodeAll` | 0 |
| `internal/encoding/numeric_alp_test.go` | >65535 round-trip; `DecodeAll` parity | 0 |
| `tests/measurev2/generator.go`, `types.go` | named realistic profile catalog | 0 |
| `format/types.go` | `TypeALP = 0x6` + `String()` | 1 |
| `section/numeric_flag.go` | value-encoding allow-list | 1 |
| `blob/numeric_encoder.go`, `numeric_encoder_config.go` | factory case, flush hook, `WithALP`, config validation | 1 |
| `blob/numeric_blob.go` | decoder dispatch: `valueAt`, `decodeValues` (iterator→All/ForEach via generic), `decodeValuesSlice` (materialize) | 1 |
| `blob/numeric_alp_wiring_test.go` (new) | dispatch-parity (`All`/`At`/`DecodeAll`/`ForEach`) | 1 |
| `tests/measurev2/types.go`, `bench.go` | ALP in the codec matrix | 2 |
| `CHANGELOG.md` | additive `TypeALP` note | 2 |

---

## Phase 0 — Codec foundation

### Task 0.1 — Widen ALP exception fields to uint32  (→ sequential, Track A)

**Why:** `nExc` and exception `pos` are `uint16` (`numeric_alp.go:317,321` + the RD layout), but a column may hold up to `section.NumericMaxCount = MaxUint32` points. A column >65535 points with an exception past position 65535 silently corrupts. ALP is unwired so the layout isn't frozen — widen now (zero compat cost).

**Files:**
- Modify: `internal/encoding/numeric_alp.go`
- Test: `internal/encoding/numeric_alp_test.go`

- [ ] **Step 1: Write the failing test** — append to `numeric_alp_test.go`:

```go
func TestNumericALP_LargeColumnExceptions(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	const n = 70000 // > 65535 so positions exceed uint16
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = float64(i) * 0.25 // clean decimals -> ALP main
	}
	vals[66000] = 1.2345678901234e300 // a forced exception PAST position 65535
	data := alpEncodeSlice(vals, eng)
	got := alpDecodeAll(data, n, eng)
	require.Equal(t, vals, got, "exception past pos 65535 must round-trip")
}
```

- [ ] **Step 2: Run it, verify it fails** — `go test ./internal/encoding/ -run 'TestNumericALP_LargeColumnExceptions' -v`. Expected: FAIL (wrong value at index 66000 — pos truncated mod 65536).

- [ ] **Step 3: Widen the fields.** In `numeric_alp.go`, change every exception **count** (`nExc`) and **position** (`pos`) read/write from 16-bit to 32-bit, in `encodeMain`, `encodeRD`, `allMain`, `allRD`, `atMain`, `atRD`. Find them: `grep -nE "AppendUint16|\.Uint16\(" internal/encoding/numeric_alp.go`. For each `nExc`/`pos`: `eng.AppendUint16` → `eng.AppendUint32`, `eng.Uint16` → `eng.Uint32`. **Critically, also fix every hardcoded offset/stride that assumes the old 2-byte widths** (these are the easy-to-miss part):
  - **nExc field grows 2→4 bytes**, shifting subsequent header offsets by +2: in `atMain`/`allMain`, `mn := Uint64(data[5:13])` → `data[7:15]` and `codes := data[13:]` → `data[15:]` (and the matching encode-side offsets); in `atRD`/`allRD`, the dict base offset `5` → `7`.
  - **Exception stride grows** because `pos` goes 2→4 bytes: **main** exception stride `10` (pos:2+val:8) → `12` (pos:4+val:8) at the encode site (~`560-564`) and decode/At sites (`atMain` ~`647-649`, `allMain` ~`560`); the value within each entry moves to offset `+4`. **RD** exception stride `4` (pos:2+left:2) → `6` (pos:4+left:2) at encode (~`603-606`) and decode/At (`atRD` ~`672-674`); the `left` field moves to offset `+4`.
  - Update the on-disk layout comment block (`numeric_alp.go:27-35`): `nExc:2`→`nExc:4`, exception `pos:2`→`pos:4`.
  - **Leave the ALP-RD dictionary `left:2` code field alone** — it is a 16-bit dictionary code, not a position. (Recompute offsets carefully; the round-trip, bit-exact, `At`-parity, and the new >65535 test are the gate that catches any missed offset.)
  - **Update the size-estimate formulas too** (scheme selection uses them — if left at 16-bit, ALP picks suboptimal main/RD/raw and RD cuts): in `numeric_alp.go` (~lines 141-144) `alpMainHeaderBits` nExc `16`→`32`; `alpExcBitsMain` pos `16`→`32` (keep `+64`); `alpRDHeaderBits` nExc `16`→`32`; `alpExcBitsRD` pos `16`→`32` (keep `+16` for the dict `left` code). Also widen the inline estimates: in `alpBestEF` (~line 258, main path) `nExc*80` → `nExc*96` (pos 16→32 + value 64). In `alpRDBestCut` (~line 374) the formula is `total := 8 + 8 + 8 + 16 + len(dict)*16 + n*codeBits + n*r + ex*(16+16)` — change the standalone nExc term `16`→`32` and the exception term `ex*(16+16)`→`ex*(32+16)` (pos 16→32, RD `left` stays 16); **keep `len(dict)*16`** (dictionary codes stay 16-bit). These are ratio-quality (not losslessness) — add a sanity assertion that ALP still beats Chimp on a decimal dataset after the change.

- [ ] **Step 4: Run the new test + the full ALP suite** — `go test ./internal/encoding/ -run 'TestNumericALP' -count=1 -v`. Expected: all PASS (round-trip, bit-exact, edge cases, the new large-column test).

- [ ] **Step 5: Lint + commit**

```bash
go tool -modfile=linter.go.mod golangci-lint run ./internal/encoding/
git add internal/encoding/numeric_alp.go internal/encoding/numeric_alp_test.go
git commit -m "fix(encoding): widen ALP exception fields to uint32 for large columns"
```

### Task 0.2 — Add `DecodeAll` to the ALP decoder  (→ sequential after 0.1, Track A)

**Why:** the blob materialize path calls `decoder.DecodeAll(data, count, dst)` (see `numeric_blob.go:1261`); the ALP decoder only has `All`/`At`.

**Files:**
- Modify: `internal/encoding/numeric_alp.go`
- Test: `internal/encoding/numeric_alp_test.go`

- [ ] **Step 1: Write the failing test:**

```go
func TestNumericALP_DecodeAll_MatchesAll(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	for _, d := range pocFloatDatasets() {
		data := alpEncodeSlice(d.values, eng)
		dec := NewNumericALPDecoder(eng)
		dst := make([]float64, len(d.values))
		n := dec.DecodeAll(data, len(d.values), dst)
		require.Equalf(t, len(d.values), n, "%s: DecodeAll count", d.name)
		require.Equalf(t, d.values, dst, "%s: DecodeAll must match input", d.name)
	}
}
```

- [ ] **Step 2: Run it, verify it fails** — `go test ./internal/encoding/ -run 'TestNumericALP_DecodeAll_MatchesAll' -v`. Expected: FAIL (`dec.DecodeAll undefined`).

- [ ] **Step 3: Implement** — add to `numeric_alp.go` (mirrors `NumericChimpDecoder.DecodeAll` at `numeric_chimp.go:496`):

```go
// DecodeAll decodes count values from data directly into dst, returning the
// number written. Allocation-free; used by the blob materialize path.
func (d NumericALPDecoder) DecodeAll(data []byte, count int, dst []float64) int {
	i := 0
	for v := range d.All(data, count) {
		if i >= len(dst) {
			break
		}
		dst[i] = v
		i++
	}

	return i
}
```

- [ ] **Step 4: Run test** — `go test ./internal/encoding/ -run 'TestNumericALP_DecodeAll_MatchesAll' -v`. Expected: PASS.

- [ ] **Step 5: Lint + commit**

```bash
go tool -modfile=linter.go.mod golangci-lint run ./internal/encoding/
git add internal/encoding/numeric_alp.go internal/encoding/numeric_alp_test.go
git commit -m "feat(encoding): add DecodeAll to NumericALPDecoder for materialize path"
```

### Task 0.3 — measurev2 realistic profile catalog  (∥ parallel with Track A, Track B)

**Why:** ALP's win must be shown on realistic decimal data, not the current full-precision random walk. This task only changes data generation; the codec matrix gets ALP in Phase 2.

**Files:**
- Modify: `tests/measurev2/generator.go`, `tests/measurev2/types.go`
- (Read first: the current `GenerateTestData`/`GenerateSharedTimestampData` in `generator.go` and `DataConfig` in `types.go` to mirror style.)

- [ ] **Step 1:** Add a `Profile` struct and catalog in `types.go`:

```go
// Profile names a realistic generator combination.
type Profile struct {
	Name       string
	Decimals   int     // value quantization (decimal places); <0 = full precision
	ValueKind  string  // "gauge" | "counter" | "sparse"
	IntervalMs int64   // scrape interval
	BurstyGaps bool    // inject periodic gaps (large dod spikes)
}

func Profiles() []Profile {
	return []Profile{
		{"decimal_gauge_2dp", 2, "gauge", 15000, false},
		{"decimal_gauge_4dp", 4, "gauge", 15000, false},
		{"counter", 0, "counter", 15000, false},
		{"sparse_constant", 2, "sparse", 60000, false},
		{"regular_scrape_60s", 2, "gauge", 60000, false},
		{"bursty_scrape", 2, "gauge", 15000, true},
		{"worst_case", -1, "gauge", 1000, false}, // full-precision random walk (old default)
	}
}
```

- [ ] **Step 2:** In `generator.go`, add `GenerateProfile(p Profile, cfg DataConfig) *TestData` that builds values per `p.ValueKind`/`p.Decimals` (gauge = bounded small-step walk quantized to `p.Decimals`; counter = monotonic integer-valued; sparse = long identical runs; `Decimals < 0` = current full-precision walk) and timestamps at `p.IntervalMs` with sub-ms jitter, injecting periodic +5s gaps when `p.BurstyGaps`. Reuse the existing metric-ID hashing and `TestData` shape.

- [ ] **Step 3: Add a round-trip test** `tests/measurev2/generator_test.go`:

```go
func TestProfilesGenerateValidData(t *testing.T) {
	cfg := DataConfig{NumMetrics: 8, PointsPerMetric: 300, Seed: 42}
	for _, p := range Profiles() {
		d := GenerateProfile(p, cfg)
		if len(d.Values) != cfg.NumMetrics*cfg.PointsPerMetric {
			t.Fatalf("%s: wrong value count", p.Name)
		}
		// timestamps must be strictly increasing per metric
		// (spot-check first metric)
	}
}
```

- [ ] **Step 4: Run** — `go test ./tests/measurev2/ -run 'TestProfilesGenerateValidData' -v` and `go build ./tests/measurev2/`. Expected: PASS + builds.

- [ ] **Step 5: Lint + commit**

```bash
go tool -modfile=linter.go.mod golangci-lint run ./tests/measurev2/
git add tests/measurev2/
git commit -m "test(measurev2): add realistic profile catalog (decimal gauges, counters, bursty)"
```

**Phase 0 gate:** `go test ./internal/encoding/ -run TestNumericALP -count=1` and `go build ./tests/measurev2/` both succeed.

---

## Phase 1 — Wire ALP into the blob format

### Task 1.1 — Register `TypeALP = 0x6`  (→ first; everything else depends on it)

**Files:** Modify `format/types.go`. Test `format/types_test.go` (if present; else inline).

- [ ] **Step 1: Write/extend the failing test** (in `format/types_test.go`):

```go
func TestTypeALPString(t *testing.T) {
	if format.TypeALP != 0x6 {
		t.Fatalf("TypeALP must be 0x6, got %#x", byte(format.TypeALP))
	}
	if format.TypeALP.String() != "ALP" {
		t.Fatalf("got %q", format.TypeALP.String())
	}
}
```

- [ ] **Step 2: Run, verify it fails** — `go test ./format/ -run TestTypeALPString -v`. Expected: FAIL (`TypeALP` undefined).

- [ ] **Step 3: Implement** — in `format/types.go` add the constant after `TypeDeltaPacked` and a `String()` case:

```go
	TypeALP EncodingType = 0x6 // TypeALP represents Adaptive Lossless floating-Point encoding for numeric values.
```
```go
	case TypeALP:
		return "ALP"
```

- [ ] **Step 4: Run** — `go test ./format/ -run TestTypeALPString -v`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add format/types.go format/types_test.go
git commit -m "feat(format): add TypeALP=0x6 encoding type"
```

### Task 1.2 — Add `TypeALP` to value-encoding validation  (∥ parallel after 1.1)

**Files:** Modify `section/numeric_flag.go`. Test `section/numeric_flag_test.go`.

- [ ] **Step 1: Failing test** — assert a flag with value encoding `TypeALP` validates:

```go
func TestNumericFlag_ALPValueEncodingValid(t *testing.T) {
	var f NumericFlag
	f.SetValueEncoding(format.TypeALP)
	require.NoError(t, f.Validate()) // or the package's validation entry point
}
```

- [ ] **Step 2: Run, verify fail** — `go test ./section/ -run TestNumericFlag_ALPValueEncodingValid -v`. Expected: FAIL (rejected by allow-list).

- [ ] **Step 3: Implement** — in `section/numeric_flag.go`, add to `validValueEncodings` (around line 35-39):

```go
		uint8(format.TypeALP):     {},
```

- [ ] **Step 4: Run** — Expected: PASS.

- [ ] **Step 5: Commit** — `git add section/numeric_flag.go section/numeric_flag_test.go && git commit -m "feat(section): accept TypeALP in value-encoding validation"`

### Task 1.3 — Encoder factory + flush + config + `WithALP`  (∥ parallel after 1.1)

**Files:** Modify `blob/numeric_encoder.go`, `blob/numeric_encoder_config.go`. Test `blob/numeric_encoder_test.go`.

- [ ] **Step 1: Failing test** — encode a column with ALP and assert the flag records `TypeALP` and bytes are produced:

```go
func TestNumericEncoder_ALP(t *testing.T) {
	enc, err := NewNumericEncoder(WithValueEncoding(format.TypeALP))
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricID(1, 3))
	require.NoError(t, enc.AddDataPoints([]int64{1, 2, 3}, []float64{1.5, 2.5, 3.5}, nil))
	require.NoError(t, enc.EndMetric())
	_, err = enc.Finish()
	require.NoError(t, err)
}
```
(Adapt the encoder API calls to match the existing `numeric_encoder_test.go` style.)

- [ ] **Step 2: Run, verify fail** — Expected: FAIL (unsupported value encoding at the factory `default:`).

- [ ] **Step 3: Implement — three edits:**

(a) `numeric_encoder.go` value-encoder factory switch (~line 230), add:
```go
	case format.TypeALP:
		encoder.valEncoder = ienc.NewNumericALPEncoder(encoder.engine)
```

(b) `numeric_encoder.go` `EndMetric` flush hook (~line 419) — ALP buffers and flushes on `Bytes()` like Gorilla/Chimp:
```go
	if valEnc == format.TypeGorilla || valEnc == format.TypeChimp || valEnc == format.TypeALP {
		_ = e.valEncoder.Bytes() // Flush pending bits before length accounting
	}
```

(c) `numeric_encoder_config.go` — add `format.TypeALP` to the two value-encoding validation case lists (~lines 78 and 88). **There is no `WithChimp` helper to mirror;** callers select ALP via the existing generic `WithValueEncoding(format.TypeALP)` (`numeric_encoder_config.go:284`). A dedicated `WithALP()` convenience wrapper is optional and out of scope for this task — the test above already uses `WithValueEncoding`.

- [ ] **Step 4: Run** — `go test ./blob/ -run TestNumericEncoder_ALP -v`. Expected: PASS.

- [ ] **Step 5: Commit** — `git add blob/numeric_encoder.go blob/numeric_encoder_config.go blob/numeric_encoder_test.go && git commit -m "feat(blob): construct + flush ALP value encoder; validate ALP in config"`

### Task 1.4 — Decoder dispatch for ALP value reads  (∥ parallel after 1.1)

**Why:** the value-encoding byte is switched on in a few places; a missed one silently returns 0 / `(0,false)`. **ALP rides the generic decode path** — it must NOT be added to the combo-specific fused switches.

**Files:** Modify `blob/numeric_blob.go` only. Test in Task 1.5.

- [ ] **Step 1: The exact sites.** Add a `case format.TypeALP` to exactly these **three** switches in `blob/numeric_blob.go` (each currently has `TypeGorilla`/`TypeChimp` cases):
  - `valueAt` random access (~line 600).
  - `decodeValues` iterator (~line 1201) — this is what `allDataPointsGeneric` uses (`numeric_blob.go:1103`) and what `decodeValues` (~line 503) returns, so adding ALP here makes the `All` iterator **and** `ForEach` work automatically (both fall through to `allDataPointsGeneric`).
  - `decodeValuesSlice` materialize (~line 1261) — used by `Materialize`/`MaterializeMetric`.

  **Do NOT touch** the combo-specific fused switches at `numeric_blob.go:686` and `numeric_blob_foreach.go:148` — those are DeltaPacked×{Gorilla,Chimp,Raw} optimizations; ALP is intentionally absent and correctly falls through to `allDataPointsGeneric` (`numeric_blob.go:697`, `numeric_blob_foreach.go:160`). **Do NOT touch** `blob/numeric_decoder.go` — it is shared-timestamp cache code, not value dispatch.

- [ ] **Step 2: Implement.** At each of the three sites, mirror the adjacent `TypeChimp` case with two differences: (1) the ALP decoder needs the engine; (2) `engine` is **not** in scope in the Gorilla/Chimp cases (it is declared only inside the `TypeRaw` case at `:591`/`:1251`), so declare `engine := b.Engine()` inside each ALP case.

  `valueAt` (~600):
```go
	case format.TypeALP:
		engine := b.Engine()
		decoder := ienc.NewNumericALPDecoder(engine)
		valBytes = b.valPayload[valStart:]

		return decoder.At(valBytes, index, count)
```
  `decodeValues` iterator (~1201) — return the ALP decoder's `All` iterator (match the Chimp case's return shape):
```go
	case format.TypeALP:
		engine := b.Engine()
		decoder := ienc.NewNumericALPDecoder(engine)

		return decoder.All(valBytes, count)
```
  `decodeValuesSlice` materialize (~1261):
```go
	case format.TypeALP:
		engine := b.Engine()
		decoder := ienc.NewNumericALPDecoder(engine)

		return decoder.DecodeAll(valBytes, count, dst)
```
  (Read each adjacent `TypeChimp` case first and match its exact `valBytes`/return shape.)

- [ ] **Step 3: Build** — `go build ./blob/`. Expected: compiles (full verification in Task 1.5).

- [ ] **Step 4: Commit** — `git add blob/numeric_blob.go && git commit -m "feat(blob): dispatch ALP at value At/iterator/materialize sites"`

### Task 1.5 — Dispatch-parity test (the safety net)  (→ after 1.3 + 1.4)

**Files:** Create `blob/numeric_alp_wiring_test.go`.

- [ ] **Step 1: Write the test** — build an ALP-encoded blob, then assert **all four read paths agree** (this is what catches a missed dispatch site). **Use the real APIs, not invented helpers:** construct via the existing test helper (`createTestBlob` / `buildForEachTestBlob` — grep `blob/*_test.go` for the exact builder and how to set value encoding to `format.TypeALP`); materialize via `Materialize()` / `MaterializeMetric()` (`blob/numeric_blob_material.go:55,317`); iterate via the real public iterator + random-access methods (grep an existing Gorilla/Chimp blob test for the exact method names — e.g. the `All`/`AllValues` iterator and the value `At`); ForEach via the real callback signature at `blob/numeric_blob_foreach.go:34`. Skeleton (replace method names with the real ones found in existing tests):

```go
func TestNumericBlob_ALP_DispatchParity(t *testing.T) {
	ts := []int64{1, 2, 3, 4, 5}
	vals := []float64{1.5, 2.25, 3.125, 100.0, 0.001}
	blob := /* createTestBlob(...) configured with format.TypeALP values */

	// Collect values through each path; all four slices must equal `vals`:
	//  1) the public value iterator (All/AllValues)
	//  2) random access (value At) for every index
	//  3) materialization (Materialize / MaterializeMetric)
	//  4) ForEach (real callback shape from numeric_blob_foreach.go:34)
	// require.Equal(t, vals, <each collected slice>)
}
```
Mirror an existing Gorilla/Chimp parity or materialize test so the method names and blob construction are exact.

- [ ] **Step 2: Run, verify it fails if any site was missed**, then with 1.4 complete: `go test ./blob/ -run TestNumericBlob_ALP_DispatchParity -v`. Expected: PASS.

- [ ] **Step 3: Run the full affected suites** — `go test ./blob/ ./section/ ./format/ ./internal/encoding/ -count=1`. Expected: PASS.

- [ ] **Step 4: Lint + commit**

```bash
go tool -modfile=linter.go.mod golangci-lint run ./blob/ ./section/ ./format/
git add blob/numeric_alp_wiring_test.go
git commit -m "test(blob): ALP dispatch-parity across All/At/DecodeAll/ForEach"
```

**Phase 1 gate:** `go test ./blob/ ./section/ ./format/ ./internal/encoding/ -count=1` passes; lint clean.

---

## Phase 2 — Validate + integrate

### Task 2.1 — End-to-end ratio validation through the blob API  (→ after Phase 1)

**Files:** Create/extend `blob/numeric_e2e_bench_test.go` or a focused test.

- [ ] **Step 1: Write a ratio test** asserting an ALP blob is smaller than a Chimp blob on decimal data. Build both blobs with the **real** test builder (the same `createTestBlob`-style helper used in Task 1.5, configured once with `format.TypeALP` and once with `format.TypeChimp`) over a 2-dp decimal series (quantize a small-step gauge to 2 decimals inline — no invented helper), and compare the finished blob byte lengths:

```go
func TestNumericBlob_ALP_BeatsChimpOnDecimals(t *testing.T) {
	// ts: fixed-interval; vals: 2-dp decimal gauge (e.g. round(walk, 2)) for n=1000.
	// alpBlob  := <createTestBlob-style helper, value encoding = format.TypeALP>
	// chimpBlob:= <same helper, value encoding = format.TypeChimp>
	// require.Lessf(t, len(alpBlob.Bytes()), len(chimpBlob.Bytes()),
	//     "ALP (%d) must beat Chimp (%d) on decimals", len(alpBlob.Bytes()), len(chimpBlob.Bytes()))
}
```
Use the finished-blob byte length (`Bytes()` or the builder's returned size); mirror an existing size/ratio test in `blob/*_test.go` for the exact builder and size accessor.

- [ ] **Step 2: Run** — `go test ./blob/ -run TestNumericBlob_ALP_BeatsChimpOnDecimals -v`. Expected: PASS (ALP ~5× smaller on decimals).

- [ ] **Step 3: Commit** — `git add blob/ && git commit -m "test(blob): ALP beats Chimp end-to-end on decimal data"`

### Task 2.2 — Add ALP to the measurev2 codec matrix  (→ after 2.1 + 0.3)

**Files:** Modify `tests/measurev2/types.go` (combos), `tests/measurev2/bench.go` (encoder opts).

- [ ] **Step 1:** Add `format.TypeALP` to the value-encoding set in `AllCombos()`/`SharedTSCombos()` (`types.go`), so the matrix runs Raw/Gorilla/Chimp/**ALP** × timestamp encodings.

- [ ] **Step 2:** Confirm `bench.go`'s `encodeBlob` already routes value encoding through `blob.WithValueEncoding(combo.ValEncoding)` (it does) — no change needed beyond the combo set.

- [ ] **Step 3: Run the harness on a decimal profile** — `go run ./tests/measurev2 -metrics 50 -points 1000 -pretty` (or the profile flag added in 0.3) and confirm ALP rows appear with smaller bytes/point than Chimp on `decimal_gauge_*`.

- [ ] **Step 4: Commit** — `git add tests/measurev2/ && git commit -m "test(measurev2): add ALP to the value-codec matrix"`

### Task 2.3 — Compat tests + CHANGELOG  (→ after 2.2)

**Files:** any golden/compat test enumerating valid `EncodingType`s; `CHANGELOG.md`.

- [ ] **Step 1:** `grep -rn "TypeGorilla\|TypeChimp" --include=*_test.go .` — for any test that enumerates the valid value-encoding set (round-trip matrices, golden compat), add a `TypeALP` case.

- [ ] **Step 2:** Add a CHANGELOG entry: ALP value codec (`TypeALP=0x6`), additive/forward-incompatible (older mebo cannot read ALP blobs; older blobs unaffected).

- [ ] **Step 3: Full suite + lint** — `go test ./... -count=1` and `go tool -modfile=linter.go.mod golangci-lint run`. Expected: all PASS, lint clean.

- [ ] **Step 4: Commit** — `git add -A && git commit -m "docs+test: document TypeALP, extend compat coverage"`

**Phase 2 gate:** `go test ./... -count=1` passes; lint clean; measurev2 shows ALP < Chimp on decimal profiles.

---

## Self-review notes

- **Spec coverage:** Phase 2.0 (uint32 widen incl. strides/offsets) → T0.1; ALP `DecodeAll` → T0.2; measurev2 broad catalog → T0.3/T2.2; `TypeALP=0x6` → T1.1; section allow-list → T1.2; encoder factory + flush + config validation → T1.3; value decode dispatch (`valueAt`, `decodeValues` iterator [covers `All`+`ForEach` via `allDataPointsGeneric`], `decodeValuesSlice` materialize) → T1.4 + parity T1.5; e2e ratio → T2.1; compat/CHANGELOG → T2.3. BP128 intentionally excluded (deferred).
- **Type consistency:** decoder constructed as `ienc.NewNumericALPDecoder(engine)` with `engine := b.Engine()` declared inside each case (it is NOT in scope from the Gorilla/Chimp cases); encoder `ienc.NewNumericALPEncoder(encoder.engine)`; `DecodeAll(data, count, dst []float64) int` matches the Chimp signature; selection via `WithValueEncoding(format.TypeALP)` (no `WithChimp`/`WithALP` helper).
- **Corrected per Codex review (round 1):** ALP rides `allDataPointsGeneric` — do NOT edit the fused switches (`numeric_blob.go:686`, `numeric_blob_foreach.go:148`) or `numeric_decoder.go`; only the three switches in `numeric_blob.go`. Test snippets use real builders/methods (`createTestBlob`, `Materialize`, the real iterator/`At`/`ForEach`), not invented helpers. The dispatch-parity test (T1.5) is the backstop that makes any missed site fail loudly.
