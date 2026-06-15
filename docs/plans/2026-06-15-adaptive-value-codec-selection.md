# Per-Column Adaptive Value-Codec Selection — Implementation Plan

> **For agentic workers:** Implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> Each task is a self-contained, committable unit with a failing test first (TDD).

**Goal:** Add a `TypeAdaptive` value encoding that, per metric column, automatically picks the smallest of `{raw, gorilla, chimp, alp}` and records the choice in a 1-byte per-column tag — so a blob gets the best codec for each column without the user choosing, robust to mixed-type blobs.

**Architecture:** A self-contained **meta-codec** in `internal/encoding` (`NumericAdaptiveEncoder`/`NumericAdaptiveDecoder`). The encoder buffers each column's `[]float64` (exactly like the ALP encoder), and on flush trial-encodes every candidate, keeps the smallest lossless result, and appends `[tagByte][encodedBytes]`. The decoder reads `tagByte` and delegates `All`/`At`/`DecodeAll` to the concrete codec. Because the meta-codec encapsulates the tag, the blob-layer wiring is **identical in shape to the existing `TypeALP` wiring** (factory case, flush hook, 3 decode-dispatch sites, materialized iterator routing). No per-metric index-entry change: the tag lives in the value payload, included in each metric's `ValueLength`.

**Tech stack:** Go 1.25/1.26; `internal/encoding` columnar codecs; `format.EncodingType` (4-bit value nibble); `section.NumericFlag`; `endian.EndianEngine`; `pool.ByteBuffer`. Tests with `testing` + `stretchr/testify`; lint `go tool -modfile=linter.go.mod golangci-lint run`; full check `make test`.

**Empirical grounding (see `docs/perf/ADAPTIVE_SELECTOR_EXPERIMENTS.md` + `..._INVESTIGATION.md`):**
- Winner is profile-dependent (ALP decimals, gorilla sparse, chimp full-precision), nearly invariant within a homogeneous column — per-column tuning *within* a workload buys ~0.25%.
- **Per-column is required** because per-blob loses **up to 36–80%** when ALP-friendly (decimal gauge) and gorilla-friendly (sparse/constant) metrics co-mingle in one blob (heterogeneity experiment, high confidence, reproduced).
- Estimator choice barely affects quality; **full per-column trial-encode** is the simplest accurate selector, and is now cheap because the ALP encoder was sped up ~3–4× (`docs/perf` benchstat). So v1 uses full trial-encode (no sampling).
- Candidate set is `{raw, gorilla, chimp, alp}`. **float32 is excluded** — it never won any profile (revisit only if a float32-precision profile is added).
- **`raw` is the hard floor** (always lossless, 8n) → selection is strictly non-regressive vs raw.
- Objective is **ratio-only** for v1. Random access does *not* enter the objective (ALP is already small *and* O(1); the O(i) penalty is specific to gorilla/chimp). An opt-in RA-aware policy is a deferred follow-up.

**On-disk byte:** `TypeAdaptive = 0x7`. (0x7 was informally reserved for a deferred BP128 *timestamp* codec; BP128 remains deferred and, if ever revived, takes the next free value. Update the project memory note accordingly.) Forward-incompatible addition — same stance already accepted for `TypeALP`; old readers reject `0x7`.

---

## File structure

**Create:**
- `internal/encoding/numeric_adaptive.go` — the meta-codec (encoder + decoder + per-column selection). One file, one responsibility.
- `internal/encoding/numeric_adaptive_test.go` — unit tests (round-trip, per-column tags, selection correctness, raw floor, edge cases).
- `blob/numeric_adaptive_wiring_test.go` — blob-level dispatch-parity + e2e heterogeneity test.

**Modify:**
- `format/types.go`, `format/types_test.go` — register `TypeAdaptive`.
- `section/numeric_flag.go`, `section/numeric_flag_test.go` — value-encoding allow-list.
- `blob/numeric_encoder_config.go` — accept `TypeAdaptive` for values.
- `blob/numeric_encoder.go` — factory case + `EndMetric` flush hook.
- `blob/numeric_blob.go` — 3 decode-dispatch cases + `allDataPoints` routing.
- `blob/numeric_blob_foreach.go` — `forEachDataPoint` routing.
- `tests/measurev2/types.go` — add `adaptive` to the value-codec matrix.
- `CHANGELOG.md` — note the feature.

**Model/effort guidance (workflow-friendly):**

| Task | Model | Effort | Why |
|---|---|---|---|
| 0.1–0.3 format/section/config | Sonnet | low | mechanical, mirror TypeALP |
| 1.1 adaptive encoder + selection | Opus | high | core logic, FP/buffer-recycle hazards |
| 1.2 adaptive decoder + delegation | Opus | medium | tag dispatch, edge cases |
| 1.3 selection-correctness tests | Opus | medium | proves the value proposition |
| 2.1–2.3 blob wiring | Sonnet | low | byte-for-byte mirror of ALP wiring |
| 2.4 parity + e2e + measurev2 | Opus | medium | the safety net + the heterogeneity proof |
| 3.x verify + docs | Opus | medium | full suite/race/lint + honest CHANGELOG |

---

## Phase 0 — Register `TypeAdaptive` (mechanical, mirrors `TypeALP`)

### Task 0.1: Add the format type

**Files:**
- Modify: `format/types.go` (const block ~line 16; `String()` ~line 37)
- Test: `format/types_test.go`

- [ ] **Step 1: Write the failing test** — append to `format/types_test.go`:
```go
func TestTypeAdaptiveString(t *testing.T) {
	if format.TypeAdaptive != 0x7 {
		t.Fatalf("TypeAdaptive must be 0x7, got %#x", byte(format.TypeAdaptive))
	}
	if format.TypeAdaptive.String() != "Adaptive" {
		t.Fatalf("got %q", format.TypeAdaptive.String())
	}
}
```
- [ ] **Step 2: Run to verify it fails** — `go test ./format/ -run TestTypeAdaptiveString` → FAIL (undefined `TypeAdaptive`).
- [ ] **Step 3: Implement** — in `format/types.go`, add after the `TypeALP` const:
```go
	TypeAdaptive    EncodingType = 0x7 // TypeAdaptive selects the best per-column value codec (self-describing tag).
```
and in `String()` before `default:`:
```go
	case TypeAdaptive:
		return "Adaptive"
```
- [ ] **Step 4: Run to verify it passes** — `go test ./format/ -run TestTypeAdaptiveString` → PASS.
- [ ] **Step 5: Commit** — `git add format/ && git commit -m "feat(format): add TypeAdaptive=0x7 value encoding"`

### Task 0.2: Allow `TypeAdaptive` as a value encoding in the section flag

**Files:**
- Modify: `section/numeric_flag.go` (`validValueEncodings` ~line 35)
- Test: `section/numeric_flag_test.go`

- [ ] **Step 1: Write the failing test** — append:
```go
func TestNumericFlag_AdaptiveValueEncodingValid(t *testing.T) {
	f := NewNumericFlag()
	f.SetValueEncoding(format.TypeAdaptive)
	if f.ValueEncoding() != format.TypeAdaptive {
		t.Fatalf("got %v", f.ValueEncoding())
	}
	if err := f.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
```
- [ ] **Step 2: Run to verify it fails** — `go test ./section/ -run TestNumericFlag_AdaptiveValueEncodingValid` → FAIL (Validate rejects 0x7).
- [ ] **Step 3: Implement** — add to `validValueEncodings`:
```go
		uint8(format.TypeAdaptive): {},
```
- [ ] **Step 4: Run to verify it passes** → PASS.
- [ ] **Step 5: Commit** — `git add section/ && git commit -m "feat(section): accept TypeAdaptive in value-encoding validation"`

### Task 0.3: Accept `TypeAdaptive` in the encoder config

**Files:**
- Modify: `blob/numeric_encoder_config.go` (`setValueEncoding` ~line 88)
- Test: `blob/numeric_encoder_test.go`

- [ ] **Step 1: Write the failing test** — add a test that `NewNumericEncoder(start, WithValueEncoding(format.TypeAdaptive))` returns no error (and that `WithTimestampEncoding(format.TypeAdaptive)` *does* error — adaptive is value-only).
```go
func TestNumericEncoder_AdaptiveValueAccepted(t *testing.T) {
	_, err := NewNumericEncoder(time.Unix(0, 0), WithValueEncoding(format.TypeAdaptive))
	if err != nil {
		t.Fatalf("adaptive value encoding should be accepted: %v", err)
	}
}
```
- [ ] **Step 2: Run to verify it fails** — `go test ./blob/ -run TestNumericEncoder_AdaptiveValueAccepted` → FAIL ("invalid value encoding"). (It will still fail after this task on the *factory* switch until Task 2.1 — keep the test but expect it to pass only after 2.1; to keep TDD green per-task, scope this step's assertion to `setValueEncoding` via a direct config unit test instead, then the factory in 2.1.)

  Direct config unit test (passes after Step 3):
```go
func TestSetValueEncoding_Adaptive(t *testing.T) {
	c := &NumericEncoderConfig{header: section.NewNumericHeader(time.Unix(0, 0))}
	if err := c.setValueEncoding(format.TypeAdaptive); err != nil {
		t.Fatalf("got %v", err)
	}
}
```
- [ ] **Step 3: Implement** — in `setValueEncoding`, add `format.TypeAdaptive` to the accepted `case`:
```go
	case format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeAdaptive:
```
- [ ] **Step 4: Run to verify it passes** — `go test ./blob/ -run TestSetValueEncoding_Adaptive` → PASS.
- [ ] **Step 5: Commit** — `git add blob/numeric_encoder_config.go blob/numeric_encoder_test.go && git commit -m "feat(blob): accept TypeAdaptive in encoder config (value-only)"`

---

## Phase 1 — The adaptive meta-codec (`internal/encoding`)

### Task 1.1: `NumericAdaptiveEncoder` + per-column selection

**Files:**
- Create: `internal/encoding/numeric_adaptive.go`
- Test: `internal/encoding/numeric_adaptive_test.go`

**Design notes:**
- Mirrors `NumericALPEncoder`: buffers `pending []float64`, flushes on `Bytes()`, `Reset()` clears pending and keeps the buffer (so per-column `[tag][bytes]` segments concatenate, one per metric).
- `encodeColumn` trial-encodes each candidate, keeps the smallest lossless bytes, writes `[byte(tag)][bytes]`.
- **Hazard 1 (buffer recycling):** a candidate encoder's `Bytes()` returns a pooled buffer; **copy** the bytes (`append([]byte(nil), b...)`) before `Finish()`.
- **Hazard 2 (losslessness):** `raw` is lossless by construction and is the floor; `gorilla`/`chimp`/`alp` are lossless codecs by construction. v1 trusts that and keeps `raw` as the guaranteed floor (so output is never larger than raw). No per-encode round-trip needed; selection is ratio-only.

- [ ] **Step 1: Write the failing test** — `internal/encoding/numeric_adaptive_test.go`:
```go
package encoding

import (
	"math"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/format"
)

func adaptiveRoundTrip(t *testing.T, vals []float64) (tag byte, size int) {
	t.Helper()
	eng := endian.GetLittleEndianEngine()
	e := NewNumericAdaptiveEncoder(eng)
	e.WriteSlice(vals)
	data := append([]byte(nil), e.Bytes()...)
	e.Finish()
	if len(vals) > 0 && len(data) == 0 {
		t.Fatal("empty output for non-empty input")
	}
	d := NewNumericAdaptiveDecoder(eng)
	dst := make([]float64, len(vals))
	n := d.DecodeAll(data, len(vals), dst)
	if n != len(vals) {
		t.Fatalf("DecodeAll n=%d want %d", n, len(vals))
	}
	for i := range vals {
		if math.Float64bits(dst[i]) != math.Float64bits(vals[i]) {
			t.Fatalf("value %d: got %v want %v", i, dst[i], vals[i])
		}
	}
	if len(vals) > 0 {
		return data[0], len(data)
	}
	return 0, len(data)
}

func TestAdaptive_RoundTrip_Decimal2dp(t *testing.T) {
	vals := make([]float64, 1000)
	cur := 100.0
	for i := range vals {
		cur += 0.13
		vals[i] = math.Round(cur*100) / 100
	}
	tag, size := adaptiveRoundTrip(t, vals)
	if format.EncodingType(tag) != format.TypeALP {
		t.Fatalf("decimal data should pick ALP, got tag %#x", tag)
	}
	// raw floor: must be <= 8n + 1 tag byte
	if size > len(vals)*8+1 {
		t.Fatalf("adaptive larger than raw floor: %d", size)
	}
}
```
- [ ] **Step 2: Run to verify it fails** — `go test ./internal/encoding/ -run TestAdaptive_RoundTrip_Decimal2dp` → FAIL (undefined `NewNumericAdaptiveEncoder`/`Decoder`).
- [ ] **Step 3: Implement the encoder** — `internal/encoding/numeric_adaptive.go` (encoder half):
```go
package encoding

import (
	"github.com/arloliu/mebo/encoding"
	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/pool"
)

// adaptiveCandidates is the value-codec menu, in tie-break order (first wins on equal size).
// raw is first so it is the guaranteed lossless floor.
var adaptiveCandidates = []format.EncodingType{
	format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP,
}

type NumericAdaptiveEncoder struct {
	engine   endian.EndianEngine
	buf      *pool.ByteBuffer
	pending  []float64
	count    int
	seqCount int
	flushed  bool
}

var _ encoding.ColumnarEncoder[float64] = (*NumericAdaptiveEncoder)(nil)

func NewNumericAdaptiveEncoder(engine endian.EndianEngine) *NumericAdaptiveEncoder {
	return &NumericAdaptiveEncoder{engine: engine, buf: pool.GetBlobBuffer()}
}

func (e *NumericAdaptiveEncoder) Write(value float64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write after Finish()")
	}
	e.count++
	e.seqCount++
	e.flushed = false
	e.pending = append(e.pending, value)
}

func (e *NumericAdaptiveEncoder) WriteSlice(values []float64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write after Finish()")
	}
	if len(values) == 0 {
		return
	}
	e.count += len(values)
	e.seqCount += len(values)
	e.flushed = false
	e.pending = append(e.pending, values...)
}

func (e *NumericAdaptiveEncoder) Bytes() []byte {
	if e.buf == nil {
		panic("encoder already finished - cannot access bytes after Finish()")
	}
	e.flush()

	return e.buf.Bytes()
}

func (e *NumericAdaptiveEncoder) flush() {
	if e.flushed || e.seqCount == 0 {
		return
	}
	tag, body := selectBestColumn(e.pending, e.engine)
	e.buf.B = append(e.buf.B, byte(tag))
	e.buf.B = append(e.buf.B, body...)
	e.flushed = true
}

func (e *NumericAdaptiveEncoder) Len() int  { return e.count }
func (e *NumericAdaptiveEncoder) Size() int { return e.buf.Len() }

func (e *NumericAdaptiveEncoder) Reset() {
	e.seqCount = 0
	e.pending = e.pending[:0]
	e.flushed = false
}

func (e *NumericAdaptiveEncoder) Finish() {
	if e.buf != nil {
		pool.PutBlobBuffer(e.buf)
		e.buf = nil
	}
	e.count = 0
	e.seqCount = 0
	e.pending = nil
	e.flushed = false
}

// selectBestColumn trial-encodes every candidate and returns the tag + bytes of
// the smallest. raw is the lossless floor; first-listed wins ties.
func selectBestColumn(values []float64, engine endian.EndianEngine) (format.EncodingType, []byte) {
	bestTag := format.TypeRaw
	best := encodeCandidate(format.TypeRaw, values, engine)
	for _, t := range adaptiveCandidates[1:] {
		b := encodeCandidate(t, values, engine)
		if len(b) < len(best) {
			best, bestTag = b, t
		}
	}

	return bestTag, best
}

// encodeCandidate encodes values with codec t and returns a COPY of the bytes
// (the encoder's buffer is pooled and recycled on Finish()).
func encodeCandidate(t format.EncodingType, values []float64, engine endian.EndianEngine) []byte {
	var enc encoding.ColumnarEncoder[float64]
	switch t { //nolint:exhaustive // adaptive menu is raw/gorilla/chimp/alp only
	case format.TypeRaw:
		enc = NewNumericRawEncoder(engine)
	case format.TypeGorilla:
		enc = NewNumericGorillaEncoder()
	case format.TypeChimp:
		enc = NewNumericChimpEncoder()
	case format.TypeALP:
		enc = NewNumericALPEncoder(engine)
	default:
		enc = NewNumericRawEncoder(engine)
	}
	enc.WriteSlice(values)
	out := append([]byte(nil), enc.Bytes()...)
	enc.Finish()

	return out
}
```
- [ ] **Step 4: Run** — fails until decoder exists (Task 1.2). Proceed to 1.2; this task's commit happens after 1.2 compiles.
- [ ] **Step 5: Commit** — deferred to end of Task 1.2 (encoder+decoder land together so the package compiles).

### Task 1.2: `NumericAdaptiveDecoder` (tag read + delegation)

**Files:**
- Modify: `internal/encoding/numeric_adaptive.go` (add decoder)
- Test: `internal/encoding/numeric_adaptive_test.go`

- [ ] **Step 1: Write the failing test** — add round-trips that force each codec and check `At`/`All` agree with `DecodeAll`:
```go
func TestAdaptive_RoundTrip_Sparse(t *testing.T) {
	vals := make([]float64, 1000) // long constant runs -> gorilla wins
	v := 42.0
	for i := range vals {
		if i%200 == 0 {
			v += 1
		}
		vals[i] = v
	}
	tag, _ := adaptiveRoundTrip(t, vals)
	if format.EncodingType(tag) != format.TypeGorilla {
		t.Fatalf("sparse data should pick Gorilla, got tag %#x", tag)
	}
}

func TestAdaptive_AtMatchesAll(t *testing.T) {
	vals := []float64{1.5, 2.25, 3.0, -4.5, 5.5, 6.0, 7.25}
	eng := endian.GetLittleEndianEngine()
	e := NewNumericAdaptiveEncoder(eng)
	e.WriteSlice(vals)
	data := append([]byte(nil), e.Bytes()...)
	e.Finish()
	d := NewNumericAdaptiveDecoder(eng)
	i := 0
	for got := range d.All(data, len(vals)) {
		at, ok := d.At(data, i, len(vals))
		if !ok || math.Float64bits(at) != math.Float64bits(got) {
			t.Fatalf("At(%d)=%v ok=%v != All=%v", i, at, ok, got)
		}
		i++
	}
	if i != len(vals) {
		t.Fatalf("iterated %d want %d", i, len(vals))
	}
}
```
- [ ] **Step 2: Run to verify it fails** — FAIL (undefined `NewNumericAdaptiveDecoder`).
- [ ] **Step 3: Implement the decoder** — append to `numeric_adaptive.go`:
```go
import "iter" // add to the import block

type NumericAdaptiveDecoder struct {
	engine endian.EndianEngine
}

var _ encoding.ColumnarDecoder[float64] = NumericAdaptiveDecoder{}

func NewNumericAdaptiveDecoder(engine endian.EndianEngine) NumericAdaptiveDecoder {
	return NumericAdaptiveDecoder{engine: engine}
}

func (d NumericAdaptiveDecoder) All(data []byte, count int) iter.Seq[float64] {
	if len(data) == 0 || count == 0 {
		return func(yield func(float64) bool) {}
	}
	body := data[1:]
	switch format.EncodingType(data[0]) { //nolint:exhaustive // adaptive menu only
	case format.TypeRaw:
		return NewNumericRawDecoder(d.engine).All(body, count)
	case format.TypeGorilla:
		return NewNumericGorillaDecoder().All(body, count)
	case format.TypeChimp:
		return NewNumericChimpDecoder().All(body, count)
	case format.TypeALP:
		return NewNumericALPDecoder(d.engine).All(body, count)
	default:
		return func(yield func(float64) bool) {}
	}
}

func (d NumericAdaptiveDecoder) At(data []byte, index, count int) (float64, bool) {
	if len(data) == 0 {
		return 0, false
	}
	body := data[1:]
	switch format.EncodingType(data[0]) { //nolint:exhaustive // adaptive menu only
	case format.TypeRaw:
		return NewNumericRawDecoder(d.engine).At(body, index, count)
	case format.TypeGorilla:
		return NewNumericGorillaDecoder().At(body, index, count)
	case format.TypeChimp:
		return NewNumericChimpDecoder().At(body, index, count)
	case format.TypeALP:
		return NewNumericALPDecoder(d.engine).At(body, index, count)
	default:
		return 0, false
	}
}

func (d NumericAdaptiveDecoder) DecodeAll(data []byte, count int, dst []float64) int {
	if len(data) == 0 || count == 0 {
		return 0
	}
	body := data[1:]
	switch format.EncodingType(data[0]) { //nolint:exhaustive // adaptive menu only
	case format.TypeRaw:
		return NewNumericRawDecoder(d.engine).DecodeAll(body, count, dst)
	case format.TypeGorilla:
		return NewNumericGorillaDecoder().DecodeAll(body, count, dst)
	case format.TypeChimp:
		return NewNumericChimpDecoder().DecodeAll(body, count, dst)
	case format.TypeALP:
		return NewNumericALPDecoder(d.engine).DecodeAll(body, count, dst)
	default:
		return 0
	}
}
```
- [ ] **Step 4: Run to verify it passes** — `go test ./internal/encoding/ -run TestAdaptive` → PASS (all round-trip + At/All tests).
- [ ] **Step 5: Commit** — `git add internal/encoding/numeric_adaptive.go internal/encoding/numeric_adaptive_test.go && git commit -m "feat(encoding): adaptive meta-codec (per-column raw/gorilla/chimp/alp selection)"`

### Task 1.3: Selection-correctness + edge-case tests

**Files:**
- Test: `internal/encoding/numeric_adaptive_test.go`

- [ ] **Step 1: Write tests** for: full-precision data → chimp tag; a column where nothing beats raw (e.g. random NaN/large bits) → raw tag and size `≤ 8n+1`; multi-flush per-column tags differ when columns differ (encode column A then `Reset()` then column B in one encoder, decode each metric's slice independently and assert the two tags can differ); empty column (`count==0`) → `Bytes()` empty, `DecodeAll(...,0,...)==0`; single value.
```go
func TestAdaptive_FullPrecisionPicksChimp(t *testing.T) {
	rng := newDeterministicRand(7) // reuse an existing helper or math/rand with fixed seed
	vals := make([]float64, 1000)
	cur := 100.0
	for i := range vals {
		cur += cur * (rng.Float64()*2 - 1) * 0.005 // unrounded -> full precision
		vals[i] = cur
	}
	tag, _ := adaptiveRoundTrip(t, vals)
	if format.EncodingType(tag) != format.TypeChimp {
		t.Fatalf("full-precision should pick Chimp, got %#x", tag)
	}
}

func TestAdaptive_RawFloor(t *testing.T) {
	// high-entropy bit patterns: no codec beats raw
	vals := []float64{
		math.Float64frombits(0x1234567890abcdef),
		math.Float64frombits(0xfedcba0987654321),
		math.Float64frombits(0x0f1e2d3c4b5a6978),
	}
	eng := endian.GetLittleEndianEngine()
	e := NewNumericAdaptiveEncoder(eng)
	e.WriteSlice(vals)
	data := append([]byte(nil), e.Bytes()...)
	e.Finish()
	if format.EncodingType(data[0]) != format.TypeRaw {
		t.Logf("note: a codec beat raw on this input, tag=%#x", data[0]) // acceptable; floor means <= raw
	}
	if len(data) > len(vals)*8+1 {
		t.Fatalf("exceeded raw floor: %d", len(data))
	}
}
```
- [ ] **Step 2–4: Run, implement (if a helper is missing, use `math/rand` with a fixed seed), pass.**
- [ ] **Step 5: Commit** — `git commit -am "test(encoding): adaptive selection-correctness + raw-floor + edge cases"`

---

## Phase 2 — Blob wiring (byte-for-byte mirror of the `TypeALP` wiring)

### Task 2.1: Encoder factory + `EndMetric` flush hook

**Files:**
- Modify: `blob/numeric_encoder.go` (factory switch ~line 233; flush hook ~line 421)

- [ ] **Step 1: Write the failing test** — re-enable `TestNumericEncoder_AdaptiveValueAccepted` (Task 0.3) and add an encode-then-decode blob test (one metric of decimal data, assert it round-trips and the blob reports `ValueEncoding()==TypeAdaptive`). Put it in `blob/numeric_adaptive_wiring_test.go`.
- [ ] **Step 2: Run to verify it fails** — FAIL ("invalid value encoding %s" from the factory `default`).
- [ ] **Step 3: Implement** — in the factory `switch enc` add after the `TypeALP` case:
```go
	case format.TypeAdaptive:
		encoder.valEncoder = ienc.NewNumericAdaptiveEncoder(encoder.engine)
```
and in `EndMetric`'s flush condition add `TypeAdaptive` (it buffers like ALP):
```go
	if valEnc == format.TypeGorilla || valEnc == format.TypeChimp || valEnc == format.TypeALP || valEnc == format.TypeAdaptive {
		_ = e.valEncoder.Bytes()
	}
```
- [ ] **Step 4: Run to verify it passes** — `go test ./blob/ -run 'Adaptive'` → PASS (encode side works; decode wired in 2.2).
- [ ] **Step 5: Commit** — `git add blob/numeric_encoder.go blob/numeric_adaptive_wiring_test.go && git commit -m "feat(blob): construct + flush adaptive value encoder"`

### Task 2.2: Decoder dispatch — `valueAt`, `decodeValues`, `decodeValuesSlice`

**Files:**
- Modify: `blob/numeric_blob.go` (`valueAtFromEntry` switch ~line 582; `decodeValues` ~line 1240; `decodeValuesSlice` ~line 1304)

- [ ] **Step 1: Write the failing test** — in the wiring test, assert `ValueAt(metricID, i)` and `AllValues`/materialize agree for an adaptive blob across several metrics with different data shapes.
- [ ] **Step 2: Run to verify it fails** — FAIL (default case returns zero/empty for `TypeAdaptive`).
- [ ] **Step 3: Implement** — add to each of the three switches, before `default:` (mirroring the existing `TypeALP` case):
```go
	case format.TypeAdaptive:
		engine := b.Engine()
		decoder := ienc.NewNumericAdaptiveDecoder(engine)
		// valueAtFromEntry: valBytes = b.valPayload[valStart:]; return decoder.At(valBytes, index, count)
		// decodeValues:     return decoder.All(valBytes, count)
		// decodeValuesSlice:return decoder.DecodeAll(valBytes, count, dst)
```
(Use the exact return shape of each site — identical to its `TypeALP` case; for `valueAtFromEntry` set `valBytes = b.valPayload[valStart:]` then `return decoder.At(valBytes, index, count)`.)
- [ ] **Step 4: Run to verify it passes** → PASS.
- [ ] **Step 5: Commit** — `git add blob/numeric_blob.go blob/numeric_adaptive_wiring_test.go && git commit -m "feat(blob): dispatch adaptive at value At/iterator/slice sites"`

### Task 2.3: `allDataPoints` + `forEachDataPoint` routing (via materialized, like ALP)

**Files:**
- Modify: `blob/numeric_blob.go` (`allDataPoints` ~line 663), `blob/numeric_blob_foreach.go` (`forEachDataPoint` ~line 107)

- [ ] **Step 1: Write the failing test** — assert `All(metricID)` (iter.Seq2) and `ForEach(metricID)` produce identical points to materialize, for an adaptive blob with delta timestamps (the default), across several metrics.
- [ ] **Step 2: Run to verify it fails** — FAIL or wrong values (generic path can't handle the tag without the adaptive decoder; routing absent).
- [ ] **Step 3: Implement** — make `TypeAdaptive` ride the materialized path exactly like `TypeALP`. In `allDataPoints`, extend the first check:
```go
	if b.ValueEncoding() == format.TypeALP || b.ValueEncoding() == format.TypeAdaptive {
		return b.allDataPointsMaterialized(tsBytes, valBytes, tagBytes, count)
	}
```
and the same one-line extension in `forEachDataPoint`:
```go
	if b.ValueEncoding() == format.TypeALP || b.ValueEncoding() == format.TypeAdaptive {
		b.allDataPointsMaterialized(tsBytes, valBytes, tagBytes, count)(yield)
		return
	}
```
(`allDataPointsMaterialized` calls `decodeValuesSlice`, which now handles `TypeAdaptive` from Task 2.2 — no other changes needed.)
- [ ] **Step 4: Run to verify it passes** → PASS.
- [ ] **Step 5: Commit** — `git add blob/numeric_blob.go blob/numeric_blob_foreach.go blob/numeric_adaptive_wiring_test.go && git commit -m "feat(blob): route adaptive through materialized All/ForEach"`

### Task 2.4: Dispatch-parity safety net + e2e heterogeneity proof + measurev2

**Files:**
- Test: `blob/numeric_adaptive_wiring_test.go`
- Modify: `tests/measurev2/types.go` (add `adaptive` to `AllCombos`/`SharedTSCombos` value list)

- [ ] **Step 1: Write the tests:**
  - **Parity matrix** (mirror `TestNumericBlob_ALP_DispatchParity`): for a blob built with `WithValueEncoding(TypeAdaptive)`, assert `All` == `At`-loop == `materialize`(`DecodeAll`) == `ForEach` for every metric, with and without tags, delta + raw + deltapacked timestamps.
  - **Heterogeneity e2e:** build a blob mixing decimal-gauge metrics and sparse/constant metrics; assert the adaptive blob's value-payload size is within ~1% of the per-column oracle and strictly smaller than the best *single* fixed codec (proves the per-column win the plan exists for). Use byte sizes from `b.valPayload` length or encoded blob size.
- [ ] **Step 2: Run to verify it fails** (if any site was missed, parity catches it).
- [ ] **Step 3: Implement** — add `{format.TypeAdaptive, "adaptive"}` to the value-encoding lists in `tests/measurev2/types.go` (`AllCombos` and `SharedTSCombos`). No blob code changes expected; if parity fails, fix the missed dispatch site.
- [ ] **Step 4: Run to verify it passes** — `go test ./blob/ -run Adaptive -race` and `cd tests/measurev2 && go test ./...` → PASS.
- [ ] **Step 5: Commit** — `git add blob/ tests/measurev2/ && git commit -m "test: adaptive dispatch-parity + heterogeneity e2e + measurev2 matrix"`

---

## Phase 3 — Verification & docs

### Task 3.1: Full verification

- [ ] **Step 1:** `make test` (race + CGO=0 + SIMD + measurev2 submodule) → all pass.
- [ ] **Step 2:** `go tool -modfile=linter.go.mod golangci-lint run ./...` → 0 issues. Fix any `exhaustive` switch that now needs a `TypeAdaptive` case or `//nolint:exhaustive`.
- [ ] **Step 3:** Run measurev2 with a mixed-profile dataset and capture the adaptive vs best-single-codec blob sizes; confirm the heterogeneity win matches the experiment (per-blob single-codec loses up to 36–80% on gauge+sparse mixes; adaptive recovers it). Record numbers.
- [ ] **Step 4: Commit** any lint fixes — `git commit -am "chore: lint/exhaustive fixes for TypeAdaptive"`

### Task 3.2: Docs + memory

- [ ] **Step 1:** CHANGELOG `[Unreleased]` → add `TypeAdaptive = 0x7`: per-column automatic value-codec selection (raw/gorilla/chimp/alp), forward-incompatible additive type; note the heterogeneity motivation and that it never regresses vs raw.
- [ ] **Step 2:** Update `docs/perf/ADAPTIVE_SELECTOR_EXPERIMENTS.md` "chosen mechanism" → "implemented" with the measured e2e numbers.
- [ ] **Step 3:** Update the project memory note: `0x7` is now `TypeAdaptive` (BP128 reservation moved); adaptive selector shipped per-column with full trial-encode.
- [ ] **Step 4: Commit** — `git commit -am "docs(perf): record adaptive value-codec selection as shipped"`

---

## Deferred (out of scope for this plan, recorded for later)

- **Random-access-aware policy** — a `WithSelectionPolicy(ratio|balanced|random-access)` knob that excludes/penalizes gorilla/chimp for point-lookup workloads (E4: ~2000× late-lookup penalty). v1 is ratio-only.
- **Sampling estimator** — replace full trial-encode with the validated ~512-contiguous-value sampler (E2: 98.3% correct) if encode latency becomes a concern; only worthwhile after further ALP speedups. v1 uses full trial-encode (now cheap).
- **float32 candidate** — add only if a genuinely float32-precision profile is added to measurev2 and shown to win.
- **Re-validate** E2/E3 once BP128/Simple8b widen the codec menu.

---

## Self-review

- **Spec coverage:** per-column selection (Tasks 1.1/1.3/2.x), candidate set raw/gorilla/chimp/alp (1.1), raw floor (1.1/1.3), ratio-only objective (1.1), `TypeAdaptive=0x7` format byte (0.1), section/config allow-lists (0.2/0.3), all decode-dispatch sites + materialized routing + ForEach (2.2/2.3), parity safety net + heterogeneity proof (2.4), forward-incompat handled like ALP (0.1). All covered.
- **Placeholder scan:** core encoder/decoder code is complete; wiring tasks show the exact code to add and reference the mirrored `TypeALP` case at named sites. No TODO/TBD.
- **Type consistency:** `NumericAdaptiveEncoder`/`NumericAdaptiveDecoder`, `selectBestColumn`, `encodeCandidate`, `adaptiveCandidates`, `format.TypeAdaptive`, `NewNumericAdaptiveEncoder/Decoder` used consistently across tasks; constructors match the `ColumnarEncoder[float64]`/`ColumnarDecoder[float64]` interfaces.
- **Risk note:** verify `internal/encoding` may import `format` (no cycle — `format` has no deps on `encoding`); if a layering rule forbids it, define the tag as a local `byte` const set mirroring the EncodingType values. Confirm in Task 1.1 Step 3.
