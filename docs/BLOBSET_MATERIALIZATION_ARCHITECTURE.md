# BlobSet Materialization Architecture

## Current Structure

```
                                    BlobSet
                                       |
                    +------------------+------------------+
                    |                                     |
              NumericBlob[]                          TextBlob[]
                    |                                     |
        +-----------+-----------+             +-----------+-----------+
        |           |           |             |           |           |
    Blob 1      Blob 2      Blob 3       Blob 1      Blob 2      Blob 3
    (hour 1)    (hour 2)    (hour 3)    (hour 1)    (hour 2)    (hour 3)
        |           |           |             |           |           |
    Metric A    Metric A    Metric A      Metric X    Metric X    Metric X
    Metric B    Metric C    Metric B      Metric Y    Metric Z    Metric Y
```

**Key Points:**
- Metrics are EITHER numeric OR text (never both)
- Numeric and text have separate collections
- Each collection has its own global indexing (0...N for numeric, 0...M for text)

## Proposed Materialization Design

```
                                    BlobSet
                                       |
                    +------------------+------------------+
                    |                                     |
              numericBlobs[]                        textBlobs[]
                    |                                     |
                    v                                     v
         MaterializeNumeric()                  MaterializeText()
                    |                                     |
                    v                                     v
      +---------------------------------+    +---------------------------------+
      | MaterializedNumericBlobSet      |    | MaterializedTextBlobSet         |
      |---------------------------------|    |---------------------------------|
      | data: map[metricID]struct {     |    | data: map[metricID]struct {     |
      |   timestamps []int64            |    |   timestamps []int64            |
      |   values     []float64          |    |   values     []string           |
      |   tags       []string           |    |   tags       []string           |
      | }                               |    | }                               |
      +---------------------------------+    +---------------------------------+
                    |                                     |
                    v                                     v
         O(1) Random Access                    O(1) Random Access
         to Numeric Metrics                    to Text Metrics
```

## Implementation Strategy: Delegation

```go
// BlobSet (unified container)
type BlobSet struct {
    numericBlobs []NumericBlob
    textBlobs    []TextBlob
}

// Thin wrapper methods (delegate to existing implementations)
func (bs BlobSet) MaterializeNumeric() MaterializedNumericBlobSet {
    set, _ := NewNumericBlobSet(bs.numericBlobs)  // Create typed set
    return set.Materialize()                       // Use existing implementation
}

func (bs BlobSet) MaterializeText() MaterializedTextBlobSet {
    set, _ := NewTextBlobSet(bs.textBlobs)        // Create typed set
    return set.Materialize()                       // Use existing implementation
}
```

**Benefits:**
✅ Zero code duplication
✅ Single source of truth (NumericBlobSet.Materialize, TextBlobSet.Materialize)
✅ Simple to implement (~40 lines)
✅ Type-safe and explicit

## Data Flow Example

### Without Materialization (Current - O(n) per access)

```
User Request: Get value at index 1500 for metric A

BlobSet.NumericValueAt(metricA, 1500)
    ↓
Scan blob 1: Has metricA? Yes. Count = 1000. Index 1500 > 1000? Yes, continue.
    ↓
Scan blob 2: Has metricA? Yes. Count = 1000. Index 1500 < 2000? Yes!
    ↓
Calculate local index: 1500 - 1000 = 500
    ↓
blob2.ValueAt(metricA, 500)  ← Still O(n) if Delta-encoded!
    ↓
Decode from start to index 500
    ↓
Return value

Total: O(n_blobs) + O(n_points) = SLOW for random access
```

### With Materialization (Proposed - O(1) per access)

```
User Request: Get value at index 1500 for metric A

material := blobSet.MaterializeNumeric()  ← ONE-TIME COST
    ↓
Materialize blob 1 (1000 points) → material.data[metricA].values[0:1000]
Materialize blob 2 (1000 points) → material.data[metricA].values[1000:2000]
Materialize blob 3 (1000 points) → material.data[metricA].values[2000:3000]
    ↓
material.data[metricA] = {
    values: [val0, val1, ..., val2999]  ← Single continuous array
}

--- Later ---

material.ValueAt(metricA, 1500)
    ↓
return material.data[metricA].values[1500]  ← Direct array indexing!
    ↓
Return value

First access: O(1) (after one-time O(N) materialization)
Subsequent accesses: O(1)
Total for 100 accesses: O(N) + 100×O(1) vs 100×O(N) = 100× FASTER
```

## Memory Layout Comparison

### Non-Materialized (Blob-Oriented)

```
Blob 1 (compressed):      [compressed data for hour 1]
Blob 2 (compressed):      [compressed data for hour 2]
Blob 3 (compressed):      [compressed data for hour 3]

Memory: ~100KB (compressed)
Access: O(n) per access (decompress + scan)
```

### Materialized (Metric-Oriented)

```
Metric A: [ts0, ts1, ..., ts2999]  ← 3000 timestamps
          [v0,  v1,  ..., v2999]   ← 3000 values
          [t0,  t1,  ..., t2999]   ← 3000 tags

Metric B: [ts0, ts1, ..., ts1999]  ← 2000 timestamps
          [v0,  v1,  ..., v1999]   ← 2000 values
          [t0,  t1,  ..., t1999]   ← 2000 tags

Memory: ~2.4MB (decompressed, 150 metrics × 1000 points × 16 bytes)
Access: O(1) per access (direct array indexing)
```

**Trade-off:**
- Memory: 24× larger (but still reasonable: 2.4MB for 150K data points)
- Speed: 100-10,000× faster for random access
- **Break-even:** After ~3-4 random accesses, materialization pays for itself

## API Consistency

### Single Blob
```go
blob := createNumericBlob(...)
material := blob.Materialize()
val, ok := material.ValueAt(metricID, 500)
```

### Typed BlobSet
```go
blobSet := NewNumericBlobSet(blobs)
material := blobSet.Materialize()
val, ok := material.ValueAt(metricID, 1500)  // Global index across blobs
```

### Unified BlobSet
```go
blobSet := NewBlobSet(numericBlobs, textBlobs)
material := blobSet.MaterializeNumeric()  // Same API!
val, ok := material.ValueAt(metricID, 1500)
```

**Consistency:** Same method name (`Materialize` / `MaterializeNumeric`), same return type, same accessor methods!

## Type Safety

```go
// At compile time, type system ensures correctness:

numericMaterial := blobSet.MaterializeNumeric()
// Type: MaterializedNumericBlobSet

val, ok := numericMaterial.ValueAt(metricID, 500)
// val type: float64 ← Compiler knows this!

textMaterial := blobSet.MaterializeText()
// Type: MaterializedTextBlobSet

val, ok := textMaterial.ValueAt(metricID, 500)
// val type: string ← Compiler knows this!
```

**No type assertions, no runtime errors, all checked at compile time!**

## Implementation Effort Summary

| Component | Files | Lines | Effort |
|-----------|-------|-------|--------|
| NumericBlobSet Materialization | 3 | ~1300 | 9h |
| TextBlobSet Materialization | 2 | ~900 | 3h |
| **BlobSet Wrapper Methods** | **0** | **~40** | **1h** |
| Documentation | 0 | ~200 | 1h |
| **Total** | **5** | **~2440** | **14h** |

**BlobSet wrappers are < 2% of the implementation effort but provide 100% of the convenience!**
