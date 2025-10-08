# NumericBlobSet Documentation

## Overview

`NumericBlobSet` is a collection type that manages multiple `NumericBlob` instances representing time-windowed metric data. It provides seamless iteration across all blobs in chronological order.

## Use Case

Time-series databases typically store data in time windows (e.g., 1-hour or 1-day blobs). When querying data that spans multiple time windows, you need to:

1. Fetch multiple blobs covering the requested time range
2. Iterate through them in chronological order
3. Handle sparse data (not all metrics exist in all blobs)

`NumericBlobSet` solves this by:
- **Automatic sorting**: Sorts blobs by `startTime` in ascending order
- **Seamless iteration**: Provides `All()`, `AllTimestamps()`, `AllValues()` that iterate across all blobs
- **Sparse data handling**: Automatically skips blobs that don't contain the requested metric

## API

### Constructor

```go
func NewNumericBlobSet(blobs []NumericBlob) (NumericBlobSet, error)
```

Creates a new immutable `NumericBlobSet` and automatically sorts blobs by their `startTime` field.

**Parameters:**
- `blobs`: Slice of `NumericBlob` instances (can be in any order)

**Returns:**
- `NumericBlobSet`: The created blob set (immutable, safe for concurrent reads)
- `error`: Returns error if `blobs` slice is empty

**Example:**
```go
blob1 := createBlobForHour(startTime, 0, "cpu.usage")
blob2 := createBlobForHour(startTime, 1, "cpu.usage")
blob3 := createBlobForHour(startTime, 2, "cpu.usage")

// Blobs can be in any order - they'll be sorted automatically
blobSet, err := blob.NewNumericBlobSet([]blob.NumericBlob{blob3, blob1, blob2})
if err != nil {
    log.Fatal(err)
}
// blobSet is now immutable and safe for concurrent reads
```

### Iteration Methods

#### All(metricID uint64) iter.Seq2[int64, float64]

Returns an iterator that yields (timestamp, value) pairs for the specified metric across all blobs in chronological order.

**Example:**
```go
metricID := MetricID("cpu.usage")
for timestamp, value := range blobSet.All(metricID) {
    fmt.Printf("Time: %v, Value: %.2f\n", time.UnixMicro(timestamp), value)
}
```

#### AllTimestamps(metricID uint64) iter.Seq[int64]

Returns an iterator that yields only timestamps for the specified metric across all blobs.

**Example:**
```go
metricID := MetricID("cpu.usage")
for timestamp := range blobSet.AllTimestamps(metricID) {
    fmt.Println(time.UnixMicro(timestamp))
}
```

#### AllValues(metricID uint64) iter.Seq[float64]

Returns an iterator that yields only values for the specified metric across all blobs.

**Example:**
```go
metricID := MetricID("cpu.usage")
for value := range blobSet.AllValues(metricID) {
    fmt.Printf("%.2f\n", value)
}
```

### Random Access Methods

#### ValueAt(metricID uint64, index int) (float64, bool)

Returns the value at the specified global index across all blobs for the given metric. The index is 0-based and spans across all blobs in chronological order.

**Index mapping example:**
```
Blob 0: 10 points (indices 0-9)
Blob 1: 5 points (indices 10-14)
Blob 2: 8 points (indices 15-22)
```

**Returns:**
- `(value, true)` if the index is valid
- `(0, false)` if:
  - The metric doesn't exist in any blob
  - The index is out of bounds
  - The index falls within a blob that doesn't contain this metric
  - The encoding doesn't support random access (only raw encoding supported)

**Performance:** O(n) to find the blob + O(1) to access within blob, where n is the number of blobs. In practice, ~26ns for first blob, ~235ns for middle blob (50 blobs), ~423ns for last blob.

**Example:**
```go
metricID := MetricID("cpu.usage")

// Access first data point
value, ok := blobSet.ValueAt(metricID, 0)
if ok {
    fmt.Printf("First value: %.2f\n", value)
}

// Access 100th data point (could be in any blob)
value, ok = blobSet.ValueAt(metricID, 99)
if ok {
    fmt.Printf("100th value: %.2f\n", value)
}
```

#### TimestampAt(metricID uint64, index int) (int64, bool)

Returns the timestamp at the specified global index across all blobs for the given metric. The index is 0-based and spans across all blobs in chronological order.

**Returns:**
- `(timestamp, true)` if the index is valid
- `(0, false)` if:
  - The metric doesn't exist in any blob
  - The index is out of bounds
  - The index falls within a blob that doesn't contain this metric
  - The encoding doesn't support random access (only raw encoding supported)

**Performance:** O(n) to find the blob + O(1) to access within blob. Same performance as `ValueAt`.

**Example:**
```go
metricID := MetricID("cpu.usage")

// Access timestamp at index 50
ts, ok := blobSet.TimestampAt(metricID, 50)
if ok {
    fmt.Printf("Timestamp: %s\n", time.UnixMicro(ts))
}
```

### Helper Methods

#### Len() int

Returns the number of blobs in the set.

```go
fmt.Printf("BlobSet contains %d blobs\n", blobSet.Len())
```

#### TimeRange() (time.Time, time.Time)

Returns the start time of the first blob and the start time of the last blob.

```go
start, end := blobSet.TimeRange()
fmt.Printf("Data spans from %s to %s\n", start, end)
```

#### BlobAt(index int) *NumericBlob

Returns a pointer to the blob at the specified index (0-based). Returns `nil` if index is out of range.

```go
firstBlob := blobSet.BlobAt(0)
if firstBlob != nil {
    fmt.Println("First blob accessed")
}
```

#### Blobs() []NumericBlob

Returns a copy of all blobs in the set (in sorted order).

```go
allBlobs := blobSet.Blobs()
fmt.Printf("Retrieved %d blobs\n", len(allBlobs))
```

## Sparse Data Handling

One of the key features of `NumericBlobSet` is its ability to handle sparse data naturally:

```go
// Blob 1: contains metric1
blob1 := createBlob(time1, "metric1", timestamps1, values1)

// Blob 2: contains metric2 (NOT metric1)
blob2 := createBlob(time2, "metric2", timestamps2, values2)

// Blob 3: contains metric1 again
blob3 := createBlob(time3, "metric1", timestamps3, values3)

blobSet, _ := blob.NewNumericBlobSet([]blob.NumericBlob{blob1, blob2, blob3})

// Query metric1 - automatically skips blob2 which doesn't have this metric
metricID := MetricID("metric1")
count := 0
for range blobSet.All(metricID) {
    count++
}
// count will be: len(timestamps1) + len(timestamps3)
```

## Implementation Details

### Sorting

Blobs are sorted by their `startTime` field using `slices.SortFunc()`:

```go
slices.SortFunc(sortedBlobs, func(a, b NumericBlob) int {
    return a.startTime.Compare(b.startTime)
})
```

This ensures chronological iteration regardless of the order blobs are provided to the constructor.

### Memory Efficiency

- Blobs are stored as values (not pointers) to minimize allocations
- Helper methods like `BlobAt()` return pointers for efficient access without copying
- `Blobs()` returns a copy to prevent external modification

### Iteration Pattern

All iteration methods use a similar pattern:

```go
func (s *NumericBlobSet) All(metricID uint64) iter.Seq2[int64, float64] {
    return func(yield func(int64, float64) bool) {
        for i := range s.blobs {
            blob := &s.blobs[i]
            for ts, val := range blob.All(metricID) {
                if !yield(ts, val) {
                    return
                }
            }
        }
    }
}
```

This pattern:
- Iterates through blobs in sorted order
- Delegates to each blob's iterator
- Supports early termination via `yield` return value
- Automatically skips blobs without the requested metric (empty iterator)

## Performance Characteristics

- **Construction**: O(n log n) due to sorting, where n is the number of blobs
- **All() iterator**: O(m × p) where m is the number of blobs and p is the average points per blob
- **ValueAt() / TimestampAt()**: O(n) to find blob + O(1) to access, where n is the number of blobs
  - First blob: ~26ns (0 allocs)
  - Middle blob (50 blobs): ~235ns (0 allocs)
  - Last blob (50 blobs): ~423ns (0 allocs)
- **TimeRange()**: O(1) - just returns first and last blob start times
- **BlobAt()**: O(1) - direct array access
- **Len()**: O(1)

### Random Access vs Iteration

When to use `ValueAt`/`TimestampAt` vs iteration:

**Use random access when:**
- You need specific indices (e.g., every 10th point)
- You need to sample data points
- You need to access data in non-sequential order
- Performance: 10 random accesses ~2μs (0 allocs)

**Use iteration when:**
- You need all data points sequentially
- You need the first N points (early termination)
- Performance: Iterate all 500 points ~9.5μs (153 allocs), first 10 points ~290ns (6 allocs)

**Example - Random Sampling:**
```go
// Get every 10th value (much faster with ValueAt than iteration)
for i := 0; i < 100; i += 10 {
    value, ok := blobSet.ValueAt(metricID, i)
    if ok {
        fmt.Printf("Sample %d: %.2f\n", i, value)
    }
}
```

## Complete Example

See `examples/blob_set_demo/main.go` for a complete working example demonstrating:
- Creating multiple time-windowed blobs
- Constructing a `NumericBlobSet`
- Querying metrics that exist in all blobs
- Querying metrics that exist in only some blobs
- Using helper methods

Run the example:
```bash
cd examples/blob_set_demo
go run main.go
```

## Test Coverage

The `NumericBlobSet` implementation has comprehensive test coverage:

- **Constructor tests**: Valid blobs, empty blobs, automatic sorting
- **Iteration tests**: Single blob, multiple blobs, sparse data, early termination
- **Helper method tests**: All public APIs tested
- **Edge cases**: Non-existent metrics, out-of-range indices

Coverage: 100% on most methods (84.9% overall for blob package)

Run tests:
```bash
go test -v -run TestNumericBlobSet ./blob/
```
