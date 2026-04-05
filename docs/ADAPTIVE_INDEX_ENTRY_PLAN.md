# Adaptive Index Entry Design — Implementation Plan

## Problem

The current `NumericIndexEntry` uses uint16 for offset deltas (TimestampOffset, ValueOffset, TagOffset), limiting each per-metric delta to 65,535 bytes. With worst-case encoding (~9–10 bytes/point), a single metric caps at ~6,553–7,281 data points. The encoder works around this with `MaxDataPoints()`, an encoding-aware limit that leaks internal format knowledge into the public API.

The V2 format is unreleased, so we have the opportunity to fix this at the format level.

## Decision Summary

| Topic | Decision | Rationale |
|---|---|---|
| **Tier count** | 2 (16B and 32B) | 32B = power-of-2, fits 2 entries per 64B L1 cache line, shift-based indexing |
| **Count field** | uint16 (compact) / uint32 (extended) | Extended format explicitly uses uint32 to avoid ambiguity on 64-bit systems |
| **Mode selection** | Auto at `Finish()` time | Scan buffered entries for max delta and count; no user-facing option needed |
| **Signaling** | Magic number: `0xEA20` (compact) vs `0xEA30` (extended) | Decoder dispatches on magic; `0xEA30` uses bits 4-15 only (avoids TagMask collision on bit 0) |
| **Section ordering** | Keep current layout | Reordering doesn't avoid copies — compression is the binding constraint |
| **MaxDataPoints()** | Version-aware | V1: encoding-aware limit (existing). V2: returns `math.MaxUint32` (uint32 Count ceiling) |
| **Encoder helpers** | Extract version-specific logic | `validateV1OffsetDeltas()`, `selectIndexFormat()`, `writeIndexEntries()` keep hot paths clean |

## Format Specification

### Compact Index Entry (16 bytes) — `0xEA20`

No change from current V2. Used when all per-metric deltas fit in uint16.

```
Offset  Size  Field
0       8     MetricID        (uint64)
8       2     Count           (uint16, max 65535)
10      2     TimestampOffset (uint16, delta-encoded)
12      2     ValueOffset     (uint16, delta-encoded)
14      2     TagOffset       (uint16, delta-encoded)
```

### Extended Index Entry (32 bytes) — `0xEA30`

Used when any per-metric delta exceeds uint16 range or any count exceeds uint16. All entries in the blob use the same size.

```
Offset  Size  Field
0       8     MetricID        (uint64)
8       4     Count           (uint32, max 4,294,967,295)
12      4     TimestampOffset (uint32, delta-encoded)
16      4     ValueOffset     (uint32, delta-encoded)
20      4     TagOffset       (uint32, delta-encoded)
24      8     Reserved        (must be 0, future use)
```

- 32 = 2^5, enabling shift-based index calculation (`i << 5` instead of `i * 32`)
- 2 entries per 64B L1 cache line — no entry straddles a cache boundary
- Count widened to uint32 — explicit on all architectures, no ambiguity
- Reserved 8 bytes at end available for future per-entry flags or extended metadata
- Delta encoding still applies — each metric stores the delta from the previous metric's offset
- Per-metric delta max: 4,294,967,295 bytes (~4 GB per field)

**Note:** The practical per-metric ceiling is further constrained by the header's `uint32` section offsets
(TimestampPayloadOffset, ValuePayloadOffset, TagPayloadOffset), which cap total blob size at ~4 GB.
The extended entry removes the per-metric bottleneck but does not make the format unbounded.

### V2 Layout (Updated)

```
| Section                    | Size                     | Description                              |
|----------------------------|--------------------------|------------------------------------------|
| Blob Header                | 32 bytes (fixed)         | Magic = 0xEA20 or 0xEA30                |
| Metric Names Payload       | Variable (optional)      | Same as V1                               |
| Metric Index               | N × 16 or N × 32 bytes  | Entry size determined by magic number    |
| Shared Timestamp Table     | Variable (optional)      | Same as current V2                       |
| Timestamps Payload         | Variable                 | Same as current                          |
| Values Payload             | Variable                 | Same as current                          |
| Tags Payload               | Variable (optional)      | Same as current                          |
```

### Magic Number Registry

| Magic    | Version | Index Entry | Count Type | Status |
|----------|---------|-------------|------------|--------|
| `0xEA10` | V1      | 16B compact | uint16     | Released (stable) |
| `0xEA20` | V2      | 16B compact | uint16     | Unreleased |
| `0xEA30` | V2-ext  | 32B extended | uint32    | Implemented |

## Scope: V2 Only

Adaptive index entries are a **V2-only capability**. V1 format behavior is completely unchanged:

- V1 retains `NumericMaxOffset` (uint16) validation in `EndMetric()`
- V1 retains `ErrOffsetOutOfRange` when any delta exceeds 65,535
- V1 index entries are always 16 bytes with magic `0xEA10`
- The adaptive mode selection in `Finish()` only runs when `layoutVersion >= 2`

## Non-Goals / Unchanged Limits

- Header remains 32 bytes
- Section offsets remain `uint32` (max ~4 GB total blob)
- Shared timestamp table format is unchanged
- Metric count remains `uint32` in header
- Text blob format is unaffected (already uses uint32 absolute offsets)

## Encoder Changes

### Single-Pass Encoding Preserved

The mode decision happens in `Finish()` using already-buffered in-memory data. The output is still written in a single forward pass:

```
EndMetric()  → buffer entries in e.indexEntries (in-memory, deltas stored as int)
Finish():
  1. magic, entrySize := e.selectIndexFormat()
     - V1: returns (0xEA10, 16)
     - V2: scans entries for max delta > 65535 or count > 65535
       → compact (0xEA20, 16) or extended (0xEA30, 32)
  2. Set magic number on cloned header (V2 only)
  3. Compute section offsets using chosen entry size
  4. e.writeIndexEntries(blob, offset, entrySize)
     → dispatches to WriteToSlice() or WriteToSlice32()
  5. Write header → names → index → shared table → payloads (single forward pass)
```

### Extracted Encoder Helpers

Version-specific logic is extracted into small unexported methods that the Go compiler can inline:

| Method | Purpose | Called from |
|--------|---------|-------------|
| `validateV1OffsetDeltas(ts, val, tag int) error` | V1 uint16 range guard; no-op for V2+ | `EndMetric()` |
| `selectIndexFormat() (magic uint16, entrySize int)` | Returns magic + entry size based on version and data ranges | `Finish()` |
| `writeIndexEntries(blob []byte, offset, entrySize int)` | Dispatches to `WriteToSlice` vs `WriteToSlice32` | `Finish()` |

### Specific Code Changes

#### 1. `MaxDataPoints()` — `blob/numeric_encoder.go`

Keep method but make version-aware:
- V1: returns encoding-aware limit based on `NumericMaxOffset / maxBytesPerPoint`
- V2: returns `section.NumericMaxCount` (`math.MaxUint32`) — the uint32 Count ceiling

#### 2. Scope uint16 delta guard to V1 only in `EndMetric()` — `blob/numeric_encoder.go`

Extracted into `validateV1OffsetDeltas()` — returns nil for V2+:
```go
func (e *NumericEncoder) validateV1OffsetDeltas(tsDelta, valDelta, tagDelta int) error {
    if e.layoutVersion >= 2 {
        return nil
    }
    if tsDelta > section.NumericMaxOffset || ... {
        return fmt.Errorf("%w: ...", errs.ErrOffsetOutOfRange)
    }
    return nil
}
```

#### 3. Add mode selection in `Finish()` — `blob/numeric_encoder.go`

Extracted into `selectIndexFormat()`:
```go
func (e *NumericEncoder) selectIndexFormat() (magic uint16, entrySize int) {
    if e.layoutVersion < 2 {
        return section.MagicNumericV1Opt, section.NumericIndexEntrySize
    }
    for i := range e.indexEntries {
        entry := &e.indexEntries[i]
        if entry.TimestampOffset > section.NumericMaxOffset ||
            entry.ValueOffset > section.NumericMaxOffset ||
            entry.TagOffset > section.NumericMaxOffset ||
            entry.Count > math.MaxUint16 {
            return section.MagicNumericV2ExtOpt, section.NumericExtIndexEntrySize
        }
    }
    return section.MagicNumericV2Opt, section.NumericIndexEntrySize
}
```

#### 4. Add serialization methods — `section/numeric_index_entry.go`

Add `Bytes32()`, `WriteToSlice32()`, and `ParseNumericIndexEntryExt()` alongside existing 16B methods.
The 32B layout: MetricID(8) + Count(uint32, 4) + TsOffset(4) + ValOffset(4) + TagOffset(4) + Reserved(8).

#### 5. Add index entry write dispatch in `Finish()` — `blob/numeric_encoder.go`

Extracted into `writeIndexEntries()`:
```go
func (e *NumericEncoder) writeIndexEntries(blob []byte, offset, entrySize int) {
    if entrySize == section.NumericExtIndexEntrySize {
        for i, entry := range e.indexEntries {
            entry.WriteToSlice32(blob, offset+i*entrySize, e.engine)
        }
    } else {
        for i, entry := range e.indexEntries {
            entry.WriteToSlice(blob, offset+i*entrySize, e.engine)
        }
    }
}
```

## Decoder Changes

#### 7. Dispatch on magic — `blob/numeric_decoder.go`

In `parseIndexEntries()`, use `d.header.Flag.IndexEntrySize()` for stride and call the appropriate parse function. The rest of the decoder is unaffected — after parsing, all index entries are `NumericIndexEntry` with `int` fields, regardless of on-disk width.

Update shared timestamp table location calculation:
```go
indexEnd := indexOffset + d.metricCount * d.header.Flag.IndexEntrySize()
```

## Constants and Flag Changes

#### 8. Add constants — `section/const.go`

```go
MagicNumericV2ExtOpt      = 0xEA30
NumericExtIndexEntrySize  = 32
NumericExtMaxOffset       = math.MaxUint32
NumericMaxCount           = math.MaxUint32
```

#### 9. Add magic detection — `section/numeric_flag.go`

```go
func (f NumericFlag) IsV2Ext() bool {
    return f.GetMagicNumber() == MagicNumericV2ExtOpt
}

func (f NumericFlag) IndexEntrySize() int {
    if f.IsV2Ext() {
        return NumericExtIndexEntrySize
    }
    return NumericIndexEntrySize
}
```

Update `IsV2()` to cover both `0xEA20` and `0xEA30`. Update `IsValidMagicNumber()` to accept `0xEA30`.

#### 10. Update `IsNumericBlob()` — `section/numeric_header.go`

```go
return magicNumber == MagicNumericV1Opt || magicNumber == MagicNumericV2Opt || magicNumber == MagicNumericV2ExtOpt
```
```

## Validation Changes

#### 11. Update `StartMetricID` / `StartMetricName` — `blob/numeric_encoder.go`

Use `e.MaxDataPoints()` which returns:
- V1: encoding-aware limit (`section.NumericMaxOffset / maxBytesPerPoint`)
- V2: `section.NumericMaxCount` (`math.MaxUint32`)

## Files Changed

| File | Changes |
|---|---|
| `section/const.go` | Add `MagicNumericV2ExtOpt = 0xEA30`, `NumericExtIndexEntrySize = 32`, `NumericExtMaxOffset`, `NumericMaxCount` |
| `section/numeric_flag.go` | Add `IsV2Ext()`, `IndexEntrySize()`, update `IsV2()`, `IsValidMagicNumber()` |
| `section/numeric_header.go` | Update `IsNumericBlob()` to accept `0xEA30` |
| `section/numeric_index_entry.go` | Add `Bytes32()`, `WriteToSlice32()`, `ParseNumericIndexEntryExt()` (32B layout with uint32 Count) |
| `blob/numeric_encoder.go` | Version-aware `MaxDataPoints()`, extract `validateV1OffsetDeltas()`, `selectIndexFormat()`, `writeIndexEntries()` |
| `blob/numeric_decoder.go` | Use `IndexEntrySize()` for stride, dispatch to correct parser |
| `errs/errors.go` | Generalize `ErrInvalidIndexEntrySize` message |
| `blob/numeric_encoder_test.go` | Add V2ExtendedMode_AutoTrigger, V2CompactMode_BelowThreshold, V2ExtendedMode_MixedMetrics, V1_DeltaGuardPreserved |
| `section/numeric_flag_test.go` | Tests for `IsValidMagicNumber`, `IsV2`, `IsV2Ext`, `IndexEntrySize` |
| `section/numeric_index_entry_test.go` | `Bytes32` / `WriteToSlice32` / `ParseNumericIndexEntryExt` tests |
| `section/numeric_header_test.go` | `IsNumericBlob` test for `0xEA30` magic |

## Testing Strategy

1. **Compact round-trip** — existing tests continue to pass (no behavioral change for typical workloads)
2. **Extended auto-trigger** — encode a single metric with >8,191 raw data points, verify magic is `0xEA30`, verify 32B entries, decode and compare
3. **Boundary test** — encode at exactly 8,191 points (raw) → should stay compact; 8,192 → should switch to extended
4. **Mixed metrics** — one metric with many points (triggers extended), others with few; verify all decode correctly
5. **V2 features in extended mode** — shared timestamps + extended entries combined; sorted index + extended entries combined
6. **V1 delta guard preserved** — V1 encoder rejects data exceeding uint16 offset range at `StartMetricID` (fail-fast via `MaxDataPoints`)
7. **Backward compat** — V1 decoder rejects `0xEA30` blobs (already handled by magic validation)

## Migration / Compatibility

- V1 decoders (`0xEA10`) reject V2/V2-ext blobs — no change needed
- V2 decoders must be updated to accept `0xEA30` before producers start emitting it
- The encoder auto-selects compact mode for typical workloads — no API change for users
- `MaxDataPoints()` is version-aware: V1 returns encoding-aware limit, V2 returns `math.MaxUint32`

### Auto-Upgrade Policy

The encoder auto-upgrades from compact (`0xEA20`) to extended (`0xEA30`) when any metric's offset
delta exceeds uint16 or any metric's count exceeds uint16. This is transparent to the user — no
explicit opt-in required. The trigger conditions:

- Any `TimestampOffset`, `ValueOffset`, or `TagOffset` delta > 65,535
- Any `Count` > 65,535

Deployment rule: upgrade all consumers to `0xEA30`-capable before deploying producers
that encode metrics with >8,191 data points (raw encoding) or >65,535 points per metric.

## Implementation Context

This section documents the final implemented state for reference.

### Implemented Constants — `section/const.go`

```go
MagicNumericV2ExtOpt = 0xEA30  // V2 extended magic (bits 4-15 only, avoids TagMask bit 0)
NumericExtIndexEntrySize = 32  // 32B entry, 2 per 64B L1 cache line
NumericExtMaxOffset = math.MaxUint32
NumericMaxCount = math.MaxUint32  // uint32 Count in extended entries
```

### Key Design Invariants

- Single forward pass in `Finish()` — `selectIndexFormat()` scans buffered entries, then `writeIndexEntries()` writes in one pass
- In-memory `NumericIndexEntry` always uses `int` fields — only the on-disk format varies (uint16 vs uint32)
- All entries in a blob use the same size (compact or extended) — no mixing within a blob
- V1 behavior is completely unchanged — `validateV1OffsetDeltas()` returns nil for V2+
- The `layoutVersion` field determines V1 vs V2; `selectIndexFormat()` further distinguishes V2 compact vs V2 extended
- Magic `0xEA30` chosen because `MagicNumberMask = 0xFFF0` covers bits 4-15 only; `0xEA21` was rejected because bit 0 is TagMask and `GetMagicNumber()` would return `0xEA20`

### Build & Validate Commands

```bash
make lint    # Must show 0 issues
make test    # Must pass all tests
```
