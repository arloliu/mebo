# BlobSet Materialization Implementation Plan

## Overview

This document outlines the plan to add materialization support to:
1. **`NumericBlobSet`** and **`TextBlobSet`** (type-specific collections) - Full implementation
2. **`BlobSet`** (unified container) - Thin wrapper methods only

The goal is to enable O(1) random access across multiple blobs while maintaining API consistency with single-blob materialization.

**See Also:** `BLOBSET_UNIFIED_MATERIALIZATION_ANALYSIS.md` for detailed analysis of BlobSet-specific design decisions.

## Problem Statement

### Current BlobSet Random Access Performance

**Existing Implementation:**
- `ValueAt(metricID, index)` uses O(n) blob scanning to find the target blob
- For each access: iterates through blobs accumulating counts until finding the right blob
- Performance: O(n) where n = number of blobs to traverse

**Example Scenario:**
```go
set, _ := NewNumericBlobSet([]NumericBlob{blob1, blob2, blob3})
// blob1: 1000 points, blob2: 1000 points, blob3: 1000 points
// Total: 3000 points

// Access index 2500 (in blob3):
// Current: Iterate blob1 (1000), blob2 (1000), then blob3 (500) → O(n) per access
// With materialization: Direct array indexing → O(1) per access
```

**Pain Points:**
1. Random access across large BlobSets is expensive (O(n) blob scanning)
2. Delta/Gorilla-encoded blobs add O(m) decoding overhead per access
3. Combined: O(n × m) for random access to Delta-encoded multi-blob data
4. Repeated random access patterns become performance bottlenecks

### Target Use Cases

1. **Time-series queries with complex predicates**
   - Need to randomly sample data points across 24-hour BlobSet
   - Currently: expensive iteration + O(n) blob scanning

2. **Statistical analysis over multiple time windows**
   - Calculate percentiles, correlations across hour-long blobs
   - Need efficient random access to arbitrary indices

3. **Sparse metric access patterns**
   - Access specific timestamps across many blobs
   - Global index lookup more efficient than per-blob coordination

## Design Decisions

### 1. Materialization Scope: Full BlobSet Only

**Decision:** Provide only full BlobSet materialization, NOT per-metric materialization.

**Rationale:**

**Against Per-Metric BlobSet Materialization:**
- **Memory efficiency unclear:** If user needs one metric from a 24-blob set, they can materialize each blob individually using `blob.MaterializeMetric(metricID)` and manage the results themselves
- **API complexity:** Would require managing 24 `MaterializedMetric` structs and coordinating global indices
- **Questionable value:** The per-metric blob API already exists and is sufficient
- **Optimization overkill:** If accessing one metric across many blobs, iterating blobs is still reasonable

**For Full BlobSet Materialization:**
- **Clear use case:** When analyzing multiple metrics across entire time range (e.g., 24-hour dataset)
- **Significant speedup:** Delta-encoded blobs benefit from one-time decode + O(1) access
- **Predictable memory:** Total memory = sum of all blob materialization costs
- **Simpler API:** One method, one return type, clear semantics

**Alternative for single-metric access:**
```go
// If you only need one metric across many blobs, use existing per-blob API:
metrics := make([]MaterializedMetric, len(blobSet.Blobs()))
for i, blob := range blobSet.Blobs() {
    metric, ok := blob.MaterializeMetric(metricID)
    if ok {
        metrics[i] = metric
    }
}
// User controls memory, timing, and coordination
```

**Conclusion:** Only implement `NumericBlobSet.Materialize()` and `TextBlobSet.Materialize()` for full-set materialization.

### 2. Materialized Structure: Flattened Global Arrays

**Decision:** Store all data in continuous global arrays, not per-blob structures.

**Option A: Per-Blob Storage (REJECTED)**
```go
type MaterializedNumericBlobSet struct {
    blobs []MaterializedNumericBlob  // Keep blob boundaries
    offsets []int                     // Offset mapping for global indices
}
```
**Cons:**
- Still requires O(log n) binary search for blob lookup
- More complex index translation logic
- Cache locality worse (data spread across blob boundaries)
- Memory overhead for offset arrays and per-blob metadata

**Option B: Flattened Global Arrays (SELECTED)**
```go
type MaterializedNumericBlobSet struct {
    data  map[uint64]materializedNumericMetricSet  // Metric ID → global arrays
    names map[string]uint64                        // Name → ID mapping
}

type materializedNumericMetricSet struct {
    timestamps []int64   // All timestamps concatenated across all blobs
    values     []float64 // All values concatenated across all blobs
    tags       []string  // All tags concatenated across all blobs (if enabled)
}
```
**Pros:**
- ✅ True O(1) random access (direct array indexing, no blob lookup)
- ✅ Better cache locality (continuous memory layout)
- ✅ Simpler implementation (no offset management)
- ✅ Consistent with single-blob `MaterializedNumericBlob` design
- ✅ Optimal memory layout for sequential scans

**Trade-offs:**
- Memory allocated upfront for entire dataset
- Loses blob boundary information (acceptable - materialization is for pure data access)

**Conclusion:** Use flattened global arrays for optimal performance.

### 3. API Design: Mirror Single-Blob API

**Decision:** Maintain exact same method signatures as `MaterializedNumericBlob` and `MaterializedTextBlob`.

**API Consistency:**

| Single Blob API | BlobSet API (Same) |
|-----------------|-------------------|
| `blob.Materialize()` → `MaterializedNumericBlob` | `blobSet.Materialize()` → `MaterializedNumericBlobSet` |
| `material.ValueAt(metricID, index)` | `material.ValueAt(metricID, index)` |
| `material.TimestampAt(metricID, index)` | `material.TimestampAt(metricID, index)` |
| `material.TagAt(metricID, index)` | `material.TagAt(metricID, index)` |
| `material.ValueAtByName(name, index)` | `material.ValueAtByName(name, index)` |

**Rationale:**
- ✅ Zero learning curve for users familiar with single-blob API
- ✅ Easy to migrate from single blob to BlobSet
- ✅ Consistent mental model: global index always means global index
- ✅ Same accessor methods, same return types, same semantics

**Key Difference:**
- **Blob:** `index` is local to the blob (0 to blob.Len()-1)
- **BlobSet:** `index` is global across all blobs (0 to total_count-1)

This is already how existing BlobSet methods work (`ValueAt`, `TimestampAt`, etc.), so it's consistent with the current design.

### 4. Location: Co-located with BlobSet Types

**Decision:** New files in `blob/` package:
- `blob/numeric_blob_set_material.go` (implementation)
- `blob/numeric_blob_set_material_test.go` (tests)
- `blob/text_blob_set_material.go` (implementation)
- `blob/text_blob_set_material_test.go` (tests)

**Rationale:**
- ✅ Follows 3-file maximum rule (impl + test per component)
- ✅ Same pattern as single-blob materialization
- ✅ Co-located with BlobSet types for discoverability

## Implementation Plan

### Phase 1: NumericBlobSet Materialization (Core Implementation)

**File:** `blob/numeric_blob_set_material.go`

**1.1 Define MaterializedNumericBlobSet Type**
```go
// MaterializedNumericBlobSet provides O(1) random access to all data points across all blobs.
// Created by calling NumericBlobSet.Materialize().
//
// All data from all blobs is decoded and flattened into continuous arrays,
// providing constant-time access at the cost of memory (~16 bytes per data point).
//
// Safe for concurrent read access after creation.
type MaterializedNumericBlobSet struct {
    data  map[uint64]materializedNumericMetricSet
    names map[string]uint64 // metricName → metricID (if available)
}

type materializedNumericMetricSet struct {
    timestamps []int64   // All timestamps from all blobs, concatenated
    values     []float64 // All values from all blobs, concatenated
    tags       []string  // All tags from all blobs, concatenated (empty if tags disabled)
}
```

**1.2 Implement Materialize() Method**
```go
// Materialize decodes all metrics from all blobs in the set and returns a
// MaterializedNumericBlobSet that supports O(1) random access.
//
// Performance:
//   - Materialization cost: ~100μs per metric per blob (one-time)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~16 bytes per data point × total data points across all blobs
//
// Use this when:
//   - You need random access to multiple metrics across the entire time range
//   - You will access data points multiple times
//   - Memory is available for pre-decoded data
//
// Example:
//   material := blobSet.Materialize()
//   // Access any data point across all blobs in O(1) time
//   val, ok := material.ValueAt(metricID, 1500)  // Could be in blob 2
//   ts, ok := material.TimestampAt(metricID, 2500)  // Could be in blob 3
func (s *NumericBlobSet) Materialize() MaterializedNumericBlobSet {
    // Implementation strategy:
    // 1. Identify all unique metric IDs across all blobs
    // 2. Pre-calculate total capacity for each metric
    // 3. Pre-allocate slices with exact capacity
    // 4. Iterate through blobs in order, appending data to global arrays
    // 5. Build metric name mapping (if available)
}
```

**Implementation Steps:**
1. Collect all unique metric IDs from all blobs
2. Calculate total data point count per metric across all blobs
3. Pre-allocate slices for each metric with exact capacity
4. Iterate through blobs in chronological order
5. For each blob, materialize all metrics and append to global arrays
6. Build metric name mapping if available in any blob
7. Return MaterializedNumericBlobSet

**1.3 Implement Accessor Methods**

Copy all accessor methods from `MaterializedNumericBlob`:
- `ValueAt(metricID, index) (float64, bool)`
- `TimestampAt(metricID, index) (int64, bool)`
- `TagAt(metricID, index) (string, bool)`
- `ValueAtByName(name, index) (float64, bool)`
- `TimestampAtByName(name, index) (int64, bool)`
- `TagAtByName(name, index) (string, bool)`
- `DataPointCount(metricID) int`
- `DataPointCountByName(name) int`
- `MetricCount() int`
- `HasMetricID(metricID) bool`
- `HasMetricName(name) bool`
- `MetricIDs() []uint64`
- `MetricNames() []string`

**Implementation:** Direct array indexing, identical to single-blob implementation.

**Time Estimate:** 4 hours

### Phase 2: NumericBlobSet Tests

**File:** `blob/numeric_blob_set_material_test.go`

**Test Coverage:**

1. **Empty BlobSet**
   - Materialize empty blob set
   - Verify all accessor methods return empty/false

2. **Single Blob BlobSet**
   - Materialize BlobSet with one blob
   - Verify identical behavior to single-blob materialization
   - Verify global indices match blob-local indices

3. **Multiple Blobs with Same Metric**
   - 3 blobs, each with 100 points for metric_1
   - Verify global index 0-99 → blob 0
   - Verify global index 100-199 → blob 1
   - Verify global index 200-299 → blob 2
   - Verify all 300 values accessible via ValueAt

4. **Multiple Blobs with Different Metrics (Sparse)**
   - Blob 0: metric_1, metric_2
   - Blob 1: metric_2, metric_3 (metric_1 missing)
   - Blob 2: metric_1, metric_3 (metric_2 missing)
   - Verify each metric has correct total count
   - Verify global indices handle gaps correctly

5. **Multiple Blobs with Tags**
   - 3 blobs with tags enabled
   - Verify TagAt returns correct tags across all blobs

6. **Multiple Blobs with Metric Names**
   - BlobSet with metric names across all blobs
   - Verify ByName methods work with global indices

7. **Different Encoding Types**
   - Blob 0: Raw-Raw
   - Blob 1: Delta-Gorilla
   - Blob 2: Raw-Gorilla
   - Verify all encodings decoded correctly into global arrays

8. **Out of Bounds Access**
   - Verify index -1 returns false
   - Verify index >= totalCount returns false

9. **Non-Existent Metric**
   - Verify ValueAt/TimestampAt for non-existent metric returns false

10. **Correctness Validation**
    - Compare materialized access vs sequential iteration
    - Verify every data point matches across all blobs

11. **Metadata Methods**
    - Verify MetricCount, MetricIDs, MetricNames
    - Verify DataPointCount returns sum across all blobs

**Time Estimate:** 3 hours

### Phase 3: NumericBlobSet Benchmarks

**File:** `blob/numeric_blob_set_material_bench_test.go`

**Benchmarks:**

1. **Materialization Overhead**
   - `BenchmarkNumericBlobSet_Materialize` (10 blobs × 150 metrics × 100 points)
   - Measure time and memory for full materialization

2. **Random Access Comparison**
   - `BenchmarkNumericBlobSet_ValueAt_NonMaterialized` (existing method)
   - `BenchmarkNumericBlobSet_ValueAt_Materialized` (new method)
   - Compare 100 random accesses across 10 blobs
   - Measure speedup for Delta-encoded blobs

3. **Sequential vs Random Access**
   - `BenchmarkNumericBlobSet_AllValues` (sequential iteration)
   - `BenchmarkMaterializedNumericBlobSet_Sequential` (sequential array access)
   - Compare iteration performance

4. **Multi-Metric Workload**
   - Random access to 10 metrics across 10 blobs
   - 10 random indices per metric
   - Measure total time and allocations

**Time Estimate:** 2 hours

### Phase 4: TextBlobSet Materialization

**Files:**
- `blob/text_blob_set_material.go` (implementation)
- `blob/text_blob_set_material_test.go` (tests)

**Implementation:**
- Mirror NumericBlobSet implementation exactly
- Use `[]string` for values instead of `[]float64`
- Same test structure with text values
- Memory cost: ~32 bytes per data point (varies with string length)

**Time Estimate:** 3 hours (implementation + tests)

### Phase 5: Documentation

**Tasks:**

1. **Update MATERIALIZATION_PROGRESS.md**
   - Add Phase 6-7 (BlobSet implementation)
   - Document performance benchmarks
   - Add usage examples

2. **Update docs/MATERIALIZATION_DESIGN.md**
   - Add BlobSet materialization section
   - Document design decisions
   - Add API examples

3. **Verify Godoc Comments**
   - Ensure all public types have comprehensive docs
   - Add usage examples in doc comments
   - Verify O(1) performance mentioned where applicable

**Time Estimate:** 1 hour

## API Usage Examples

### Basic BlobSet Materialization

```go
// Create BlobSet from multiple time-window blobs
blobs := []NumericBlob{hourBlob1, hourBlob2, hourBlob3}
blobSet, _ := NewNumericBlobSet(blobs)

// Materialize entire BlobSet (all blobs, all metrics)
material := blobSet.Materialize()

// O(1) random access across all blobs
// Indices are global: 0 to (total points across all blobs - 1)
val, ok := material.ValueAt(metricID, 150)   // Could be in any blob
ts, ok := material.TimestampAt(metricID, 250)
tag, ok := material.TagAt(metricID, 350)

// By name
val, ok = material.ValueAtByName("cpu.usage", 500)

// Metadata
totalPoints := material.DataPointCount(metricID)  // Sum across all blobs
metricCount := material.MetricCount()              // Unique metrics
allMetrics := material.MetricIDs()
```

### When to Use BlobSet Materialization

**Use BlobSet Materialization:**
```go
// ✅ Analyzing multiple metrics across entire time range
material := blobSet.Materialize()
for _, metricID := range material.MetricIDs() {
    count := material.DataPointCount(metricID)
    for i := range count {
        val, _ := material.ValueAt(metricID, i)  // O(1)
        analyze(val)
    }
}

// ✅ Random sampling across 24-hour dataset
indices := []int{100, 500, 1000, 2000, 5000}
for _, idx := range indices {
    val, _ := material.ValueAt(metricID, idx)  // O(1) per access
}

// ✅ Statistical analysis requiring random access
median := material.ValueAt(metricID, totalCount/2)
q1 := material.ValueAt(metricID, totalCount/4)
q3 := material.ValueAt(metricID, 3*totalCount/4)
```

**Use Per-Blob Materialization Instead:**
```go
// ✅ Only need one metric across time range
for _, blob := range blobSet.Blobs() {
    metric, ok := blob.MaterializeMetric(metricID)
    if ok {
        // Process this metric in this blob
        for i := range metric.Len() {
            val, _ := metric.ValueAt(i)
        }
    }
}

// ✅ Need blob boundary information (e.g., hourly aggregation)
for hour, blob := range blobSet.Blobs() {
    material := blob.Materialize()
    hourlyStats := calculateStats(material, metricID)
    report[hour] = hourlyStats
}
```

### Migration from Non-Materialized BlobSet

**Before (Existing Code):**
```go
blobSet, _ := NewNumericBlobSet(blobs)

// O(n) blob scanning per access
val, ok := blobSet.ValueAt(metricID, 1500)  // Scans through blobs
ts, ok := blobSet.TimestampAt(metricID, 2500)
```

**After (With Materialization):**
```go
blobSet, _ := NewNumericBlobSet(blobs)

// One-time materialization cost
material := blobSet.Materialize()

// O(1) random access
val, ok := material.ValueAt(metricID, 1500)  // Direct array indexing
ts, ok := material.TimestampAt(metricID, 2500)
```

**Same API, same semantics, better performance!**

## Performance Expectations

### Materialization Cost

**NumericBlobSet (10 blobs × 150 metrics × 100 points = 150,000 points):**
- Time: ~45ms (10 × 4.5ms per blob)
- Memory: 2.4 MB (150,000 × 16 bytes)
- Allocations: ~18,000 (10 × 1,800 per blob)

**TextBlobSet (10 blobs × 150 metrics × 100 points = 150,000 points):**
- Time: ~50ms (similar to numeric)
- Memory: 4.8 MB (150,000 × 32 bytes, varies with string length)
- Allocations: ~20,000

### Random Access Performance

| Scenario | Non-Materialized | Materialized | Speedup |
|----------|-----------------|--------------|---------|
| **Raw encoding (10 blobs)** | ~500 ns | ~5 ns | **100×** |
| **Delta encoding (10 blobs)** | ~50,000 ns | ~5 ns | **10,000×** |

**Why such huge speedup?**
- Non-materialized: O(n) blob scanning + O(m) decoding per access
- Materialized: O(1) array indexing (already decoded)

### Break-Even Analysis

**When to materialize:**
- **Multiple random accesses:** After ~3-4 accesses, materialization pays off
- **Delta/Gorilla encoding:** Always beneficial (10,000× speedup)
- **Large BlobSets:** More blobs → more scanning overhead → bigger win
- **Statistical analysis:** Percentiles, correlations, etc. need random access

**When NOT to materialize:**
- **Single sequential pass:** Just iterate normally
- **One-time access:** Sequential iteration is cheaper
- **Memory constrained:** 16-32 bytes per point might be too much

## Implementation Checklist

### Phase 1: NumericBlobSet Core (4 hours)
- [ ] Create `blob/numeric_blob_set_material.go`
- [ ] Define `MaterializedNumericBlobSet` type
- [ ] Define `materializedNumericMetricSet` type
- [ ] Implement `NumericBlobSet.Materialize()` method
- [ ] Implement accessor methods (ValueAt, TimestampAt, TagAt)
- [ ] Implement ByName variants
- [ ] Implement metadata methods
- [ ] Verify compilation

### Phase 2: NumericBlobSet Tests (3 hours)
- [ ] Create `blob/numeric_blob_set_material_test.go`
- [ ] Test: Empty BlobSet
- [ ] Test: Single blob BlobSet
- [ ] Test: Multiple blobs with same metric
- [ ] Test: Multiple blobs with sparse metrics
- [ ] Test: Multiple blobs with tags
- [ ] Test: Multiple blobs with metric names
- [ ] Test: Different encoding types
- [ ] Test: Out of bounds access
- [ ] Test: Non-existent metric
- [ ] Test: Correctness validation
- [ ] Test: Metadata methods
- [ ] All tests pass

### Phase 3: NumericBlobSet Benchmarks (2 hours)
- [ ] Create `blob/numeric_blob_set_material_bench_test.go`
- [ ] Benchmark: Materialization overhead
- [ ] Benchmark: Random access comparison
- [ ] Benchmark: Sequential vs random
- [ ] Benchmark: Multi-metric workload
- [ ] Document performance results

### Phase 4: TextBlobSet Implementation (3 hours)
- [ ] Create `blob/text_blob_set_material.go`
- [ ] Implement MaterializedTextBlobSet (mirror numeric)
- [ ] Create `blob/text_blob_set_material_test.go`
- [ ] All tests pass
- [ ] Document memory characteristics

### Phase 5: BlobSet Wrapper Methods (1 hour)
- [ ] Add `BlobSet.MaterializeNumeric()` method to `blob/blob_set.go`
- [ ] Add `BlobSet.MaterializeText()` method to `blob/blob_set.go`
- [ ] Add `BlobSet.NumericBlobs()` accessor (if not already public)
- [ ] Add `BlobSet.TextBlobs()` accessor (if not already public)
- [ ] Add tests for wrapper methods
- [ ] Verify delegation works correctly

### Phase 6: Documentation (1 hour)
- [ ] Update MATERIALIZATION_PROGRESS.md
- [ ] Update docs/MATERIALIZATION_DESIGN.md
- [ ] Verify all godoc comments complete
- [ ] Add usage examples
- [ ] Final review and commit

**Total Estimated Time:** 14 hours (13 hours for typed BlobSets + 1 hour for BlobSet wrappers)

## Summary

**Goal:** Add O(1) random access materialization support to NumericBlobSet and TextBlobSet.

**Key Design Points:**
1. ✅ Full BlobSet materialization only (no per-metric variant)
2. ✅ Flattened global arrays (optimal O(1) performance)
3. ✅ API mirrors single-blob materialization (zero learning curve)
4. ✅ Co-located in blob/ package (follows 3-file rule)

**Expected Benefits:**
- 100-10,000× speedup for random access across multi-blob datasets
- Consistent API with single-blob materialization
- Predictable memory cost: ~16 bytes/point (numeric) or ~32 bytes/point (text)

**Next Step:** Begin Phase 1 implementation after approval.
