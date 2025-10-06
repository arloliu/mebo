# Materialization Design & Implementation Guide

## Overview

This document describes the design, implementation, and usage of the materialization feature in mebo, which provides O(1) random access to time-series data by decoding all metrics into memory.

---

## Table of Contents

1. [Problem Statement](#problem-statement)
2. [Design Decisions](#design-decisions)
3. [API Design](#api-design)
4. [Implementation Guide](#implementation-guide)
5. [Performance Characteristics](#performance-characteristics)
6. [Usage Guide](#usage-guide)
7. [Implementation Checklist](#implementation-checklist)

---

## Problem Statement

### Current Limitations

Certain encoding formats require sequential access (O(N) complexity) for random access operations:

| Encoding | Random Access | Reason |
|----------|---------------|--------|
| **TimestampRaw** | ✅ O(1) | Fixed 8-byte offsets |
| **TimestampDelta** | ❌ O(N) | Delta-of-delta requires iteration |
| **NumericRaw** | ✅ O(1) | Fixed 8-byte offsets |
| **NumericGorilla** | ❌ O(N) | Variable-length bit-packed |
| **Tag** | ✅ O(1) | Varstring with length prefixes |

### Performance Impact

For Delta/Gorilla encodings with 1000 data points:
- **Current:** `ValueAt(index=500)` = ~1.8μs (requires iterating 0→500)
- **Desired:** `ValueAt(index=500)` = ~5ns (direct slice access)

**Use case:** When users need repeated random access to the same data (e.g., sampling, visualization, statistical analysis).

---

## Design Decisions

### Decision 1: Generic vs Blob-Specific Implementation

**Question:** Should we provide a generic materialized cache package?

**Decision:** ❌ **NO** - Use blob-specific implementation

**Rationale:**
- Only 2 blob types (Numeric, Text) - generic abstraction is overkill
- Performance > code reuse for hot paths (no interface overhead)
- Simpler code: 300 LOC vs 460 LOC for generic approach
- Type-safe, compiler can optimize better
- Follows Go idiom: "Prefer concrete types over generics for simple cases"
- Easy to refactor to generic later if we add more blob types

**Code size comparison:**
```
Generic approach:    460 LOC (300 generic + 80 + 80 integration)
Blob-specific:       300 LOC (150 + 150)
```

---

### Decision 2: Lazy vs Explicit Materialization

**Question:** Should we materialize separately and on-demand (lazy loading)?

**Decision:** ❌ **NO** to lazy loading, ✅ **YES** to explicit materialization

**Rationale:**

**Why NOT lazy loading:**
- High complexity (locking, double-check pattern, error-prone)
- Thread-safety overhead (RWMutex on every access)
- Unpredictable performance (first access slow, rest fast)
- Hidden state (violates principle of least surprise)
- Memory management concerns (when to evict?)

**Why explicit materialization:**
- Simple, predictable, straightforward
- One-time decode cost, then O(1) always
- User controls when materialization happens
- Thread-safe by default (read-only after creation)
- Clear memory cost and performance characteristics
- No hidden state

**Provide TWO explicit APIs:**

1. **Full Materialization** - Best for accessing many metrics
2. **Per-Metric Materialization** - Best for memory-constrained scenarios or few metrics

---

### Decision 3: Package Location

**Question:** Where should this functionality live?

**Decision:** ✅ **`blob/` package**

**File structure:**
```
blob/
├── numeric_blob.go                    # Existing - core implementation
├── numeric_blob_test.go               # Existing - tests
├── numeric_blob_material.go           # NEW - materialization
├── numeric_blob_material_test.go      # NEW - materialization tests
├── text_blob.go                       # Existing - core implementation
├── text_blob_test.go                  # Existing - tests
├── text_blob_material.go              # NEW - materialization
└── text_blob_material_test.go         # NEW - materialization tests
```

**Rationale:**
- Co-located with blob types (easy to find)
- Follows mebo's 3-file pattern (implementation, test, specialized feature)
- Can access blob internals directly without exporting
- Consistent with existing structure (`numeric_decoder.go`, `numeric_encoder.go`)

**Alternatives considered:**
- ❌ `internal/material/` - Overkill for simple materialization
- ❌ `internal/cache/` - Implies more than just materialization (eviction, TTL, etc.)

---

## API Design

### Full Blob Materialization

**Use when:** You need random access to many metrics multiple times

```go
// Materialize all metrics at once
material := blob.Materialize()

// Then O(1) random access (~5ns)
val, ok := material.ValueAt(metricID, index)
ts, ok := material.TimestampAt(metricID, index)
tag, ok := material.TagAt(metricID, index)

// Also supports name-based access
val, ok := material.ValueAtByName(metricName, index)
```

**API Surface:**

```go
type MaterializedNumericBlob struct {
    data  map[uint64]materializedNumericMetric
    names map[string]uint64  // metricName → metricID (if available)
}

// Core methods
func (b NumericBlob) Materialize() MaterializedNumericBlob
func (m MaterializedNumericBlob) ValueAt(metricID uint64, index int) (float64, bool)
func (m MaterializedNumericBlob) TimestampAt(metricID uint64, index int) (int64, bool)
func (m MaterializedNumericBlob) TagAt(metricID uint64, index int) (string, bool)
func (m MaterializedNumericBlob) ValueAtByName(name string, index int) (float64, bool)
func (m MaterializedNumericBlob) TimestampAtByName(name string, index int) (int64, bool)
func (m MaterializedNumericBlob) TagAtByName(name string, index int) (string, bool)

// Utility methods
func (m MaterializedNumericBlob) MetricCount() int
func (m MaterializedNumericBlob) HasMetricID(metricID uint64) bool
func (m MaterializedNumericBlob) HasMetricName(name string) bool
func (m MaterializedNumericBlob) MetricIDs() []uint64
func (m MaterializedNumericBlob) MetricNames() []string
```

---

### Per-Metric Materialization

**Use when:** You only need to access 1-5 metrics, or want fine-grained memory control

```go
// Materialize single metric
metric, ok := blob.MaterializeMetric(metricID)
if !ok {
    // Metric not found
}

// Then O(1) random access (~5ns)
val, ok := metric.ValueAt(index)
ts, ok := metric.TimestampAt(index)
tag, ok := metric.TagAt(index)

// Get length
count := metric.Len()
```

**API Surface:**

```go
type MaterializedMetric struct {
    MetricID   uint64
    Timestamps []int64
    Values     []float64
    Tags       []string
}

func (b NumericBlob) MaterializeMetric(metricID uint64) (MaterializedMetric, bool)
func (m MaterializedMetric) ValueAt(index int) (float64, bool)
func (m MaterializedMetric) TimestampAt(index int) (int64, bool)
func (m MaterializedMetric) TagAt(index int) (string, bool)
func (m MaterializedMetric) Len() int
```

---

## Implementation Guide

### Phase 1: NumericBlob Full Materialization (2-3 hours)

**File:** `blob/numeric_blob_material.go`

```go
package blob

// MaterializedNumericBlob provides O(1) random access to all data points.
// Created by calling NumericBlob.Materialize().
//
// Safe for concurrent read access after creation.
type MaterializedNumericBlob struct {
    data  map[uint64]materializedNumericMetric
    names map[string]uint64
}

type materializedNumericMetric struct {
    timestamps []int64
    values     []float64
    tags       []string
}

// Materialize decodes all metrics in the blob and returns a MaterializedNumericBlob
// that supports O(1) random access.
//
// Use this when:
//   - You need random access to many metrics
//   - You will access each metric multiple times
//   - Memory is available (~16 bytes per data point)
//
// Example:
//
//	material := blob.Materialize()
//	val, ok := material.ValueAt(metricID, 500)  // O(1), ~5ns
func (b NumericBlob) Materialize() MaterializedNumericBlob {
    material := MaterializedNumericBlob{
        data:  make(map[uint64]materializedNumericMetric, b.MetricCount()),
        names: make(map[string]uint64),
    }

    // Decode all metrics
    for metricID, entry := range b.index.byID {
        // Decode timestamps
        var timestamps []int64
        for ts := range b.allTimestampsFromEntry(entry) {
            timestamps = append(timestamps, ts)
        }

        // Decode values
        var values []float64
        for val := range b.allValuesFromEntry(entry) {
            values = append(values, val)
        }

        // Decode tags (if enabled)
        var tags []string
        if b.flag.HasTag() {
            for tag := range b.allTagsFromEntry(entry) {
                tags = append(tags, tag)
            }
        }

        material.data[metricID] = materializedNumericMetric{
            timestamps: timestamps,
            values:     values,
            tags:       tags,
        }
    }

    // Copy metric name mappings
    if b.index.byName != nil {
        for name, entry := range b.index.byName {
            material.names[name] = entry.MetricID
        }
    }

    return material
}

// ValueAt returns the value at the specified index. O(1) operation.
func (m MaterializedNumericBlob) ValueAt(metricID uint64, index int) (float64, bool) {
    metric, ok := m.data[metricID]
    if !ok {
        return 0, false
    }

    if index < 0 || index >= len(metric.values) {
        return 0, false
    }

    return metric.values[index], true
}

// TimestampAt returns the timestamp at the specified index. O(1) operation.
func (m MaterializedNumericBlob) TimestampAt(metricID uint64, index int) (int64, bool) {
    metric, ok := m.data[metricID]
    if !ok {
        return 0, false
    }

    if index < 0 || index >= len(metric.timestamps) {
        return 0, false
    }

    return metric.timestamps[index], true
}

// TagAt returns the tag at the specified index. O(1) operation.
func (m MaterializedNumericBlob) TagAt(metricID uint64, index int) (string, bool) {
    metric, ok := m.data[metricID]
    if !ok {
        return "", false
    }

    if index < 0 || index >= len(metric.tags) {
        return "", false
    }

    return metric.tags[index], true
}

// ValueAtByName returns the value at the specified index by metric name.
func (m MaterializedNumericBlob) ValueAtByName(metricName string, index int) (float64, bool) {
    metricID, ok := m.names[metricName]
    if !ok {
        return 0, false
    }
    return m.ValueAt(metricID, index)
}

// TimestampAtByName returns the timestamp at the specified index by metric name.
func (m MaterializedNumericBlob) TimestampAtByName(metricName string, index int) (int64, bool) {
    metricID, ok := m.names[metricName]
    if !ok {
        return 0, false
    }
    return m.TimestampAt(metricID, index)
}

// TagAtByName returns the tag at the specified index by metric name.
func (m MaterializedNumericBlob) TagAtByName(metricName string, index int) (string, bool) {
    metricID, ok := m.names[metricName]
    if !ok {
        return "", false
    }
    return m.TagAt(metricID, index)
}

// MetricCount returns the number of metrics in the materialized blob.
func (m MaterializedNumericBlob) MetricCount() int {
    return len(m.data)
}

// HasMetricID checks if the materialized blob contains the given metric ID.
func (m MaterializedNumericBlob) HasMetricID(metricID uint64) bool {
    _, ok := m.data[metricID]
    return ok
}

// HasMetricName checks if the materialized blob contains the given metric name.
func (m MaterializedNumericBlob) HasMetricName(metricName string) bool {
    _, ok := m.names[metricName]
    return ok
}

// MetricIDs returns a slice of all metric IDs in the materialized blob.
func (m MaterializedNumericBlob) MetricIDs() []uint64 {
    ids := make([]uint64, 0, len(m.data))
    for id := range m.data {
        ids = append(ids, id)
    }
    return ids
}

// MetricNames returns a slice of all metric names in the materialized blob.
// Returns empty slice if no metric names are available.
func (m MaterializedNumericBlob) MetricNames() []string {
    if len(m.names) == 0 {
        return nil
    }
    names := make([]string, 0, len(m.names))
    for name := range m.names {
        names = append(names, name)
    }
    return names
}
```

---

### Phase 2: NumericBlob Per-Metric API (1 hour)

**Add to:** `blob/numeric_blob_material.go`

```go
// MaterializedMetric represents a single materialized metric with O(1) random access.
type MaterializedMetric struct {
    MetricID   uint64
    Timestamps []int64
    Values     []float64
    Tags       []string
}

// MaterializeMetric decodes a single metric for O(1) random access.
//
// Use this when:
//   - You only need to access one or few metrics
//   - You want fine-grained control over memory usage
//
// Example:
//
//	metric, ok := blob.MaterializeMetric(metricID)
//	if ok {
//	    val, _ := metric.ValueAt(500)  // O(1)
//	}
func (b NumericBlob) MaterializeMetric(metricID uint64) (MaterializedMetric, bool) {
    entry, ok := b.index.GetByID(metricID)
    if !ok {
        return MaterializedMetric{}, false
    }

    var timestamps []int64
    for ts := range b.allTimestampsFromEntry(entry) {
        timestamps = append(timestamps, ts)
    }

    var values []float64
    for val := range b.allValuesFromEntry(entry) {
        values = append(values, val)
    }

    var tags []string
    if b.flag.HasTag() {
        for tag := range b.allTagsFromEntry(entry) {
            tags = append(tags, tag)
        }
    }

    return MaterializedMetric{
        MetricID:   metricID,
        Timestamps: timestamps,
        Values:     values,
        Tags:       tags,
    }, true
}

// ValueAt returns the value at the specified index. O(1) operation.
func (m MaterializedMetric) ValueAt(index int) (float64, bool) {
    if index < 0 || index >= len(m.Values) {
        return 0, false
    }
    return m.Values[index], true
}

// TimestampAt returns the timestamp at the specified index. O(1) operation.
func (m MaterializedMetric) TimestampAt(index int) (int64, bool) {
    if index < 0 || index >= len(m.Timestamps) {
        return 0, false
    }
    return m.Timestamps[index], true
}

// TagAt returns the tag at the specified index. O(1) operation.
func (m MaterializedMetric) TagAt(index int) (string, bool) {
    if index < 0 || index >= len(m.Tags) {
        return "", false
    }
    return m.Tags[index], true
}

// Len returns the number of data points in this metric.
func (m MaterializedMetric) Len() int {
    return len(m.Values)
}
```

---

### Phase 3: Tests (2 hours)

**File:** `blob/numeric_blob_material_test.go`

```go
package blob

import (
    "testing"
    "time"

    "github.com/stretchr/testify/require"
    "github.com/arloliu/mebo/internal/hash"
)

func TestNumericBlob_Materialize(t *testing.T) {
    // Create test blob with multiple metrics
    startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
    // ... create blob with test data ...

    // Materialize
    material := blob.Materialize()

    // Verify all metrics are materialized
    require.Equal(t, blob.MetricCount(), material.MetricCount())

    // Verify random access works
    val, ok := material.ValueAt(metricID, 50)
    require.True(t, ok)
    require.Equal(t, expectedValue, val)

    // Verify out of bounds
    _, ok = material.ValueAt(metricID, 1000)
    require.False(t, ok)

    // Verify missing metric
    _, ok = material.ValueAt(99999, 0)
    require.False(t, ok)
}

func TestNumericBlob_MaterializeMetric(t *testing.T) {
    // ... test per-metric materialization ...
}

func BenchmarkMaterialize(b *testing.B) {
    // Benchmark materialization cost
}

func BenchmarkMaterialized_ValueAt(b *testing.B) {
    // Benchmark O(1) random access
}
```

---

### Phase 4: TextBlob (2 hours)

**File:** `blob/text_blob_material.go`

Similar implementation for TextBlob with `MaterializedTextBlob` and appropriate string value handling.

---

## Performance Characteristics

### Materialization Cost

| Scenario | Decode Time | Memory | Notes |
|----------|-------------|--------|-------|
| **Full (150 metrics × 1000 points)** | ~15ms | ~2.4 MB | One-time cost |
| **Per-Metric (1 metric × 1000 points)** | ~100μs | ~16 KB | Per metric |

**Memory breakdown:**
```
Per data point: ~16 bytes (8 bytes timestamp + 8 bytes value)
1000 points: ~16 KB
150 metrics × 1000 points: ~2.4 MB
```

### Random Access Performance

| Operation | Current (Delta/Gorilla) | Materialized | Speedup |
|-----------|------------------------|--------------|---------|
| **ValueAt(index=0)** | ~13ns | ~5ns | 2.6× |
| **ValueAt(index=500)** | ~1.8μs | ~5ns | 360× |
| **ValueAt(index=1000)** | ~3.6μs | ~5ns | 720× |

### Break-Even Analysis

**When does materialization pay off?**

```
Materialization cost: 100μs (per metric)
Per-access savings: ~1.8μs (for index=500)

Break-even: 100μs / 1.8μs ≈ 56 accesses

Conclusion: Materialize if you'll access >56 random indices per metric
```

**For multiple metrics:**
```
Full materialization: 15ms (150 metrics)
Per-metric cost: 3.6μs × 150 = 540μs

Break-even: ~28 accesses across all metrics
```

---

## Usage Guide

### When to Use Materialization

#### ✅ Use `Materialize()` when:
- You need random access to many metrics
- You will access each metric multiple times (>50 accesses)
- Memory is available (~16 bytes per data point)
- Predictable O(1) performance is critical
- You're using Delta or Gorilla encoding

#### ✅ Use `MaterializeMetric()` when:
- You only need to access 1-5 metrics
- You want to minimize memory usage
- You need fine-grained control
- Memory is constrained

#### ❌ Don't materialize when:
- You're doing sequential iteration (use `All()` iterator instead)
- You only access data once or twice
- Memory is severely constrained
- You're already using Raw encoding (already O(1))

---

### Usage Examples

#### Example 1: Full Materialization for Sampling

```go
// Load blob with Delta encoding (O(N) random access)
blob := loadBlob()

// Materialize for O(1) random access
material := blob.Materialize()

// Sample every 10th data point (very efficient now)
for i := 0; i < 1000; i += 10 {
    val, ok := material.ValueAt(metricID, i)
    if ok {
        fmt.Printf("Sample %d: %.2f\n", i, val)
    }
}
```

#### Example 2: Per-Metric for Memory Efficiency

```go
// Only need one metric
metric, ok := blob.MaterializeMetric(targetMetricID)
if !ok {
    return fmt.Errorf("metric not found")
}

// Now access randomly
for _, idx := range randomIndices {
    val, _ := metric.ValueAt(idx)
    process(val)
}
```

#### Example 3: Visualization with Random Access

```go
// Materialize for visualization that needs random access
material := blob.Materialize()

// Generate chart with downsampling
for x := 0; x < width; x++ {
    // Calculate which data point to show
    index := x * totalPoints / width

    val, _ := material.ValueAt(metricID, index)
    chart.AddPoint(x, val)
}
```

#### Example 4: Migration from Iterator to Random Access

**Before (sequential only):**
```go
// Must iterate from beginning each time
for i := 0; i < 100; i += 10 {
    // O(N) - iterate from 0 to i each time
    count := 0
    for val := range blob.AllValues(metricID) {
        if count == i {
            process(val)
            break
        }
        count++
    }
}
```

**After (with materialization):**
```go
// Materialize once
material := blob.Materialize()

// O(1) random access
for i := 0; i < 100; i += 10 {
    val, _ := material.ValueAt(metricID, i)
    process(val)
}
```

---

## Implementation Checklist

### Phase 1: NumericBlob Full Materialization (1 hour)

- [ ] Create `blob/numeric_blob_material.go`
- [ ] Define `MaterializedNumericBlob` struct
- [ ] Define `materializedNumericMetric` struct (unexported)
- [ ] Implement `NumericBlob.Materialize()` method
- [ ] Implement `MaterializedNumericBlob.ValueAt()`
- [ ] Implement `MaterializedNumericBlob.TimestampAt()`
- [ ] Implement `MaterializedNumericBlob.TagAt()`
- [ ] Implement `MaterializedNumericBlob.ValueAtByName()`
- [ ] Implement `MaterializedNumericBlob.TimestampAtByName()`
- [ ] Implement `MaterializedNumericBlob.TagAtByName()`
- [ ] Implement `MaterializedNumericBlob.MetricCount()`
- [ ] Implement `MaterializedNumericBlob.HasMetricID()`
- [ ] Implement `MaterializedNumericBlob.HasMetricName()`
- [ ] Implement `MaterializedNumericBlob.MetricIDs()`
- [ ] Implement `MaterializedNumericBlob.MetricNames()`

### Phase 2: NumericBlob Per-Metric API (1 hour)

- [ ] Define `MaterializedMetric` struct
- [ ] Implement `NumericBlob.MaterializeMetric()`
- [ ] Implement `MaterializedMetric.ValueAt()`
- [ ] Implement `MaterializedMetric.TimestampAt()`
- [ ] Implement `MaterializedMetric.TagAt()`
- [ ] Implement `MaterializedMetric.Len()`

### Phase 3: Tests (2 hours)

- [ ] Create `blob/numeric_blob_material_test.go`
- [ ] Test full materialization correctness
- [ ] Test per-metric materialization correctness
- [ ] Test edge cases (empty blob, missing metrics, out of bounds)
- [ ] Test metric name lookups (if names available)
- [ ] Test all `At` methods (Value, Timestamp, Tag)
- [ ] Benchmark materialization cost (`BenchmarkNumericBlob_Materialize`)
- [ ] Benchmark random access (`BenchmarkMaterialized_ValueAt`)
- [ ] Compare with current approach (`BenchmarkValueAt_Materialized_vs_Current`)

### Phase 4: TextBlob (1 hour)

- [ ] Create `blob/text_blob_material.go`
- [ ] Define `MaterializedTextBlob` struct
- [ ] Define `materializedTextMetric` struct
- [ ] Implement methods (similar to NumericBlob)
- [ ] Create `blob/text_blob_material_test.go`
- [ ] Write tests and benchmarks

### Phase 5: Documentation (1 hour)

- [ ] Add godoc comments to all new types and methods
- [ ] Update `README.md` with materialization examples
- [ ] Create or update `PERFORMANCE_GUIDE.md`
- [ ] Document when to use materialization vs iteration
- [ ] Add code examples for common patterns

---

## Summary

### Key Design Principles

1. **Simplicity** - No generics, no lazy loading, no locks
2. **Explicitness** - User controls when materialization happens
3. **Flexibility** - Both full and per-metric materialization
4. **Performance** - Zero abstraction overhead, true O(1) access
5. **Maintainability** - Clear code, easy to understand

### Implementation Stats

| Metric | Value |
|--------|-------|
| **Total LOC** | ~800 lines (4 new files) |
| **Effort** | 6-8 hours (including tests and docs) |
| **New APIs** | 2 (full + per-metric) |
| **Memory overhead** | ~16 bytes per data point |
| **Performance gain** | Up to 720× for random access |
