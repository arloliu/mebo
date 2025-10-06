# Delta Offset Design

**Key Design Principle:** Delta offset encoding creates order dependencies that prevent sorting

## Core Concept

The Mebo format uses **delta offset encoding** for both `TimestampOffset` and `ValueOffset` fields in `IndexEntry`. This design choice creates a fundamental constraint: **entries must be stored in insertion order** (the order metrics were added during encoding).

## How Delta Encoding Works

### Encoder Side (Writing)

```go
// NumericEncoder.EndMetric() - lines 151-162
tsOffsetDelta := e.tsOffset - e.lastTsOffset       // delta from PREVIOUS metric
valOffsetDelta := e.valOffset - e.lastValOffset    // delta from PREVIOUS metric

entry := section.NewIndexEntry(e.curMetricID, uint16(curTsLen))
entry.TimestampOffset = uint16(tsOffsetDelta)      // store DELTA, not absolute
entry.ValueOffset = uint16(valOffsetDelta)         // store DELTA, not absolute

e.addEntryIndex(entry)

// Update state for NEXT metric
e.lastTsOffset = e.tsOffset
e.lastValOffset = e.valOffset
```

**Key Point:** Each metric's offsets depend on the previous metric's absolute position. The encoder maintains running state (`lastTsOffset`, `lastValOffset`) that accumulates as metrics are added.

### Decoder Side (Reading)

```go
// NumericDecoder.Decode() - lines 66-82
blob.indexEntryMap = make(map[uint64]section.NumericIndexEntry)

var lastTsOffset, lastValOffset uint16 = 0, 0

for i := 0; i < d.metricCount; i++ {
    var entry section.NumericIndexEntry
    entry.Parse(indexData[...], d.engine)

    // Reconstruct absolute offsets by ACCUMULATING deltas
    entry.TimestampOffset += lastTsOffset
    entry.ValueOffset += lastValOffset

    blob.indexEntryMap[entry.MetricID] = entry

    // Update state for NEXT metric
    lastTsOffset = entry.TimestampOffset
    lastValOffset = entry.ValueOffset
}
```

**Key Point:** The decoder MUST process entries in the same order they were written. Each entry's absolute offset is computed by adding its delta to the accumulated offset from previous entries.

## Benefits of Delta Encoding

Delta encoding provides significant advantages:

### 1. Extended Addressing Range

Instead of being limited to 65,535 bytes total for each payload, delta encoding allows:

```
Theoretical Maximum Per Metric:
  - Each metric can have up to 65,535 bytes delta
  - Total payload can be much larger (N × 65,535 bytes in best case)

Practical Example (1,000 metrics):
  - If each metric uses 800 bytes
  - Total payload: 800KB
  - Per-metric delta: 800 bytes (well within uint16 range)
  - Addressing extended by 12.5× compared to absolute offsets
```

### 2. Better Compression

Delta values are typically smaller than absolute offsets:

```
Absolute offsets (would need larger integers):
  [0, 40, 64, 120, 176, ...]  → requires more bits

Delta offsets (fit in uint16):
  [0, 40, 24, 56, 56, ...]    → smaller values compress better
```

### 3. Efficient Sequential Access

Sequential storage with delta encoding is cache-friendly:
- Encoder writes entries sequentially (no random access)
- Decoder reads entries sequentially (predictable memory access pattern)
- CPU cache prefetching works optimally

## Alternative Design (Not Used)

To enable binary search, the format would need to store **absolute offsets** instead of deltas:

```go
// Hypothetical absolute offset design
type IndexEntry struct {
    MetricID        uint64
    Count           uint16
    TimestampOffset uint32  // ABSOLUTE offset (uint32 needed)
    ValueOffset     uint32  // ABSOLUTE offset (uint32 needed)
    _ [2]byte              // still need padding
}
// Size: 18 bytes (vs 16 bytes with delta encoding)
```

**Trade-offs:**
- ✅ Could sort entries by MetricID
- ✅ Binary search possible: O(log N) lookup without hash map
- ✅ Zero memory overhead for lookups (no hash map)
- ❌ Larger index entries (18 bytes vs 16 bytes)
- ❌ Smaller addressing range (uint32 = 4GB, but limited per-metric size)
- ❌ Less compression-friendly (larger offset values)
- ❌ More complex 64-bit handling on 32-bit systems

## Current Design Rationale

The mebo format prioritizes:

1. **Maximum Efficiency:** uint16 deltas keep index entries compact (16 bytes)
2. **Extended Range:** Delta encoding allows addressing larger payloads
3. **O(1) Lookups:** Hash map provides fastest possible access
4. **Typical Use Case:** Blobs are decoded once, queried many times (~24 bytes per metric is negligible)

The insertion-order constraint is a direct consequence of the delta encoding optimization, and the hash map lookup strategy makes this constraint irrelevant for query performance.

## Documentation References

### Primary Documentation
- `docs/DESIGN.md`: Complete format specification with delta encoding details

### Code Implementation
- `blob/float_value_encoder.go`: Delta calculation and insertion-order writing
- `blob/float_value_decoder.go`: Sequential decode and delta accumulation
- `section/index_entry.go`: IndexEntry structure definition with delta offset fields

### Test Coverage
- `blob/float_value_encoder_test.go`: Tests for delta encoding correctness
- `blob/float_value_decoder_test.go`: Tests for delta reconstruction
- `blob/float_value_offset_delta_test.go`: Comprehensive delta encoding test suite

---

**Key Takeaway:** The delta offset design creates a "linked list" structure where each entry depends on the previous one. This makes insertion order a fundamental requirement, not a choice. The hash map lookup strategy elegantly handles this constraint while providing optimal O(1) performance.
