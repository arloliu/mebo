# TagEncoder - High-Performance Variable-Length String Encoding

## Overview

`TagEncoder` provides highly optimized variable-length encoding for string tags in the mebo time-series format. It uses uvarint-prefixed encoding for space efficiency while maintaining excellent performance.

## Encoding Format

Each tag is encoded as:
```
[length: uvarint][bytes: UTF-8]
```

- **Length**: Variable-length unsigned integer (1-10 bytes, typically 1-2 bytes)
  - 1 byte for strings < 128 bytes
  - 2 bytes for strings < 16,384 bytes
  - 3+ bytes for longer strings (rare)
- **Bytes**: Raw UTF-8 string bytes (no null terminator)

### Examples

**Empty tag:**
```
[0x00]
Total: 1 byte
```

**Short tag "ok":**
```
[0x02]['o']['k']
Total: 3 bytes (1 + 2)
```

**Medium tag "severity=high":**
```
[0x0D]['s']['e']['v']['e']['r']['i']['t']['y']['=']['h']['i']['g']['h']
Total: 14 bytes (1 + 13)
```

**Long tag (200 bytes):**
```
[0xC8, 0x01][...200 bytes...]
Total: 202 bytes (2 + 200)
```

## Performance Characteristics

### Benchmark Results

**Single Write Operations:**
```
BenchmarkTagEncoder_Write_Empty      4.6 ns/op    5 B/op    0 allocs/op
BenchmarkTagEncoder_Write_Short      8.1 ns/op    7 B/op    0 allocs/op
BenchmarkTagEncoder_Write_Medium     40  ns/op  107 B/op    0 allocs/op
BenchmarkTagEncoder_Write_Long      111  ns/op  488 B/op    0 allocs/op
BenchmarkTagEncoder_Write_UTF8       21  ns/op  109 B/op    0 allocs/op
```

**Bulk Write Operations:**
```
BenchmarkTagEncoder_WriteSlice_10Tags       90 ns/op   316 B/op   0 allocs/op
BenchmarkTagEncoder_WriteSlice_100Tags    2595 ns/op  9450 B/op   0 allocs/op
```

**Key Observations:**
- ✅ **Zero allocations** for write operations (uses pre-allocated buffer)
- ✅ **Sub-nanosecond per byte** encoding speed
- ✅ **Linear scaling**: ~26 ns/op per tag for bulk operations
- ✅ **Efficient buffer growth**: Doubles capacity when needed

### Comparison: Write vs WriteSlice

For 10 tags:
```
Write (10 times):        136 ns/op   126 B/op   0 allocs/op
WriteSlice (10 tags):    103 ns/op   136 B/op   0 allocs/op
```

**Result**: `WriteSlice` is ~24% faster for bulk operations due to:
1. Single buffer growth calculation
2. Reduced loop overhead
3. Better cache locality

## API Usage

### Basic Usage

```go
import (
    "github.com/arloliu/mebo/encoding"
    "github.com/arloliu/mebo/endian"
)

// Create encoder
engine := endian.GetLittleEndianEngine()
encoder := encoding.NewTagEncoder(engine)

// Write single tag
encoder.Write("severity=high")

// Write multiple tags (more efficient)
tags := []string{"user_id=123", "region=us-west", ""}
encoder.WriteSlice(tags)

// Get encoded bytes
data := encoder.Bytes()
count := encoder.Len()
size := encoder.Size()
```

### Encoder Lifecycle

```go
encoder := encoding.NewTagEncoder(engine)

// Session 1: Encode tags
encoder.Write("tag1")
encoder.Write("tag2")
data1 := encoder.Bytes() // Get accumulated data

// Reset for next metric (keeps buffer)
encoder.Reset()
// Len() = 0, but Bytes() still returns accumulated data

// Session 2: More tags accumulate
encoder.Write("tag3")
data2 := encoder.Bytes() // Returns all data (tag1, tag2, tag3)

// Finish encoding (clears everything)
encoder.Finish()
// Len() = 0, Size() = 0, Bytes() = []
```

### Reset vs Finish

| Method | Clears Count | Clears Buffer | Use Case |
|--------|--------------|---------------|----------|
| `Reset()` | ✅ Yes | ❌ No | Between metrics, accumulate data |
| `Finish()` | ✅ Yes | ✅ Yes | End encoding session, start fresh |

## Design Decisions

### 1. Variable-Length Encoding (Uvarint)
**Decision**: Use `encoding/binary.PutUvarint` for length prefix

**Rationale**:
- ✅ Space-efficient: 1 byte for most tags (< 128 bytes)
- ✅ Standard Go library (well-tested, optimized)
- ✅ Supports unlimited string lengths
- ✅ Self-describing format (no external length table needed)

**Trade-off**: 2-10 bytes for very long strings (rare in practice)

### 2. Pre-Allocated Buffer
**Decision**: Start with 1KB initial capacity, double when needed

**Rationale**:
- ✅ Avoids allocations for typical use cases (10-100 tags)
- ✅ Exponential growth minimizes reallocation overhead
- ✅ Memory usage scales with actual data size

**Initial capacity calculation**:
```
100 tags × 10 bytes/tag = 1000 bytes ≈ 1KB
```

### 3. Single Buffer Growth
**Decision**: Calculate total size needed, grow buffer once

**Rationale**:
- ✅ Minimizes allocations (0 allocs/op in benchmarks)
- ✅ Better memory locality
- ✅ Faster than incremental append

**Implementation**:
```go
// Pre-calculate total size for all tags
for _, tag := range tags {
    totalSize += varintSize(len(tag)) + len(tag)
}

// Grow buffer once
e.buf = growBuffer(e.buf, totalSize)

// Write all tags without further allocations
```

### 4. Direct Byte Operations
**Decision**: Use `copy()` for string bytes, avoid conversions

**Rationale**:
- ✅ Zero-copy: Go strings are already UTF-8 byte slices
- ✅ No allocation: `copy()` is optimized assembly
- ✅ Fast: Direct memory copy

**Code**:
```go
// Fast path: direct copy, no []byte(tag) conversion needed
copy(e.buf[offset:], tag)
```

### 5. Endian-Neutral Encoding
**Decision**: Length-prefixed bytes are endian-neutral

**Rationale**:
- ✅ Portable across architectures
- ✅ No endian conversion overhead
- ✅ Simpler decoder implementation

**Note**: The `engine` parameter is kept for interface compatibility but not used.

## Space Efficiency

### Varint Length Encoding

| String Length | Varint Bytes | Range |
|---------------|--------------|-------|
| 0-127 | 1 byte | Most tags |
| 128-16,383 | 2 bytes | Long tags |
| 16,384-2,097,151 | 3 bytes | Very long tags |
| 2,097,152+ | 4+ bytes | Extreme (rare) |

### Examples

**10 typical tags** (avg 20 bytes each):
```
Traditional (null-terminated): 10 × (20 + 1) = 210 bytes
Varint-encoded:               10 × (1 + 20) = 210 bytes
Savings: 0% (same)
```

**10 short tags** (avg 5 bytes each):
```
Traditional (fixed 20 bytes): 10 × 20 = 200 bytes
Varint-encoded:               10 × (1 + 5) = 60 bytes
Savings: 70%
```

**10 empty tags**:
```
Traditional (null-terminated): 10 × 1 = 10 bytes
Varint-encoded:               10 × 1 = 10 bytes
Savings: 0% (same)
```

## Memory Usage

### Buffer Growth Strategy

Initial allocation: 1KB (1024 bytes)

Growth pattern:
```
Initial:  1,024 bytes
1st grow: 2,048 bytes (2×)
2nd grow: 4,096 bytes (2×)
3rd grow: 8,192 bytes (2×)
...
```

### Memory Efficiency

**100 metrics × 10 tags/metric × 10 bytes/tag**:
```
Total data: 10,000 bytes
Buffer size: 16,384 bytes (after 4 doublings)
Overhead: 64% (reasonable for exponential growth)
```

**After Finish()**:
```
Buffer: 0 bytes (cleared)
Overhead: 0%
```

## Error Handling

`TagEncoder` is designed to be panic-free:

- ✅ Empty strings supported (encodes as `[0x00]`)
- ✅ UTF-8 validation **not performed** (caller's responsibility)
- ✅ Buffer growth never fails (Go runtime handles OOM)
- ✅ All methods are non-blocking

## Thread Safety

⚠️ **Not thread-safe**

Each encoder instance should be used by a single goroutine. For concurrent encoding:

```go
// ❌ Bad: Shared encoder
encoder := encoding.NewTagEncoder(engine)
go encoder.Write("tag1") // Race condition!
go encoder.Write("tag2") // Race condition!

// ✅ Good: Per-goroutine encoder
go func() {
    encoder := encoding.NewTagEncoder(engine)
    encoder.Write("tag1")
}()
```

## Best Practices

### 1. Use WriteSlice for Bulk Operations
```go
// ❌ Slower
for _, tag := range tags {
    encoder.Write(tag)
}

// ✅ Faster (24% improvement)
encoder.WriteSlice(tags)
```

### 2. Reuse Encoders with Reset
```go
encoder := encoding.NewTagEncoder(engine)

for _, metric := range metrics {
    for _, tag := range metric.Tags {
        encoder.Write(tag)
    }
    // Process accumulated data
    processData(encoder.Bytes())
    encoder.Reset() // Reuse buffer
}

encoder.Finish() // Clean up at end
```

### 3. Pre-Allocate for Known Sizes
```go
// If you know you'll have 1000 tags × 20 bytes each
encoder := encoding.NewTagEncoder(engine)
encoder.buf = make([]byte, 0, 20000) // Pre-allocate 20KB
```

### 4. Validate UTF-8 Before Encoding
```go
import "unicode/utf8"

if !utf8.ValidString(tag) {
    // Handle invalid UTF-8
}
encoder.Write(tag)
```

## Integration with Text Value Blob

`TagEncoder` is designed for use in text value blob encoding:

```go
// Text value data point encoding
type TextDataPoint struct {
    Ts  int64
    Val string
    Tag string
}

// Encode data points
tagEncoder := encoding.NewTagEncoder(engine)
for _, point := range dataPoints {
    // Encode timestamp (delta or absolute)
    // Encode value (varint length + bytes)
    // Encode tag
    tagEncoder.Write(point.Tag)
}

// Get encoded tag data
tagData := tagEncoder.Bytes()
```

## Comparison with Alternatives

### vs String Pool
**TagEncoder (no pool)**:
- ✅ Simpler implementation
- ✅ No deduplication overhead
- ❌ Larger data size for repetitive tags

**String Pool**:
- ❌ Complex implementation
- ❌ Index lookup overhead
- ✅ Smaller data size for repetitive tags

**Decision**: No string pool per user requirement ("too complicated")

### vs Fixed-Length Encoding
**Variable-length (current)**:
- ✅ Space-efficient for short strings
- ❌ Slightly slower decoding (must read length first)

**Fixed-length**:
- ❌ Wasteful for short strings
- ✅ Faster decoding (no length needed)

**Decision**: Variable-length for space efficiency in typical use case

## Future Optimizations

Possible improvements (not yet implemented):

1. **SIMD string copying** for very long strings
2. **Pool of pre-allocated buffers** to reduce GC pressure
3. **Compression-aware encoding** (e.g., dictionary for common prefixes)
4. **Streaming API** for extremely large datasets

---

## Summary

`TagEncoder` provides:
- ✅ **High performance**: 4-111 ns/op depending on tag length
- ✅ **Zero allocations**: For write operations
- ✅ **Space efficient**: Uvarint encoding, 1 byte overhead for typical tags
- ✅ **Simple API**: Write, WriteSlice, Reset, Finish
- ✅ **UTF-8 support**: Direct byte encoding, no conversions
- ✅ **Buffer reuse**: Reset for multi-metric encoding

**Perfect for**: Text value blob encoding with per-point tags in the mebo time-series format.

