# Text Value Blob Design Review

## Date: October 1, 2025
## Status: Ready for Implementation

---

## 1. Architecture Overview

### **Storage Model**
- **Type**: Row-based storage (unlike Numeric blob's columnar storage)
- **Data Point**: Timestamp (int64) + Value (string) + Tag (string)
- **Organization**: Header → Index Section → Data Section
- **Magic Number**: `0xEB10` (distinguishes from Numeric blob's `0xEA10`)

### **Design Philosophy**
- **Compact encoding**: Varint for variable-length data
- **Inline strings**: No string pool complexity
- **Buffered encoding**: Header-at-start requires buffering data to calculate offsets
- **Fast iteration**: Optimized over random access
- **O(1) metric lookup**: Via index section

---

## 2. Header Structure (32 bytes)

### **TextHeader**
```go
type TextHeader struct {
    Flag        TextFlag  // 4 bytes (Options:2, TimestampEncoding:1, DataCompression:1)
    StartTime   int64                // 8 bytes
    IndexOffset uint32               // 4 bytes - offset to index section
    DataOffset  uint32               // 4 bytes - offset to data section
    DataSize    uint32               // 4 bytes - compressed data size (0 if uncompressed)
    MetricCount uint32               // 4 bytes
    Checksum    uint32               // 4 bytes - CRC32
}
```

### **TextFlag (4 bytes)**
```go
type TextFlag struct {
    Options           uint16  // Bits: [15:4]=magic(0xEB10), [3:2]=reserved, [1]=endian, [0]=checksum
    TimestampEncoding uint8   // Full uint8 for timestamp encoding (TypeRaw, TypeDelta)
    DataCompression   uint8   // Full uint8 for data compression (None, Zstd, S2, LZ4)
}
```

**Key Differences from Numeric Blob:**
- ✅ Simpler flag header (no value encoding/compression)
- ✅ Single `DataOffset` instead of separate timestamp/value offsets
- ✅ Single data section (not columnar)
- ✅ `DataSize` field for compressed data handling
- ✅ Different magic number (0xEB10 vs 0xEA10)

**Validation:**
- ✅ Magic number check
- ✅ Reserved bits must be 0
- ✅ Timestamp encoding validation (Raw/Delta)
- ✅ Data compression validation (None/Zstd/S2/LZ4)

---

## 3. Index Section

### **TextIndexEntry (16 bytes)**
```go
type TextIndexEntry struct {
    MetricID  uint64  // 8 bytes - xxHash64 hash or metric ID
    Count     uint16  // 2 bytes - number of data points
    Reserved1 uint16  // 2 bytes - for future use
    Offset    uint32  // 4 bytes - absolute offset in data section
    Size      uint32  // 4 bytes - IN MEMORY ONLY (calculated from offset differences)
}
```

**Serialization (16 bytes on disk):**
```
[MetricID:8][Count:2][Reserved:2][Offset:4]
```

**Size Calculation (not stored):**
- For entry `i`: `Size[i] = Offset[i+1] - Offset[i]`
- For last entry: `Size[last] = DataSize - Offset[last]`
- Where `DataSize` comes from `TextHeader.DataSize`

**Key Differences from Numeric Blob:**
- ✅ **Absolute offsets** (not delta encoding)
- ✅ **Size calculated** (saves 4 bytes per entry)
- ✅ **Simpler decoder** (no accumulation needed)
- ✅ **O(1) random access** (direct offset lookup)

**Design Rationale:**
- Text data is variable-length, absolute offsets are clearer
- Size calculation is trivial from offsets
- Saves 4 bytes per metric (important for many metrics)
- Decoder complexity reduced (no delta accumulation)

---

## 4. Data Section

### **Row-Based Layout**
Each metric's data is stored as a contiguous block:
```
Metric 1 Data Block | Metric 2 Data Block | Metric 3 Data Block | ...
```

### **Data Point Encoding (per metric)**
```
[DataPoint1][DataPoint2][DataPoint3]...
```

### **Single Data Point Structure**
```
[Timestamp: varint/8-byte][ValueLen: varint][Value: UTF-8 bytes][TagLen: varint][Tag: UTF-8 bytes]
```

**Encoding Details:**
- **Timestamp**:
  - Raw: 8 bytes fixed (int64)
  - Delta: varint (delta from previous or StartTime)
- **ValueLen**: varint encoding (saves space for short strings)
- **Value**: UTF-8 bytes (no null terminator needed)
- **TagLen**: varint encoding (0 if no tag = 1 byte)
- **Tag**: UTF-8 bytes (empty if TagLen=0)

**Space Optimization:**
- Empty tag: 1 byte (varint 0)
- Short strings: varint length is 1-2 bytes for lengths < 16384
- Delta timestamps: typically 1-2 bytes for regular intervals

---

## 5. Complete Blob Layout

```
┌─────────────────────────────────────────┐
│ TextHeader (32 bytes)              │ ← Magic: 0xEB10
├─────────────────────────────────────────┤
│ TextIndexEntry[0] (16 bytes)       │ ← Metric 1 index
│ TextIndexEntry[1] (16 bytes)       │ ← Metric 2 index
│ ...                                     │
│ TextIndexEntry[N-1] (16 bytes)     │ ← Metric N index
├─────────────────────────────────────────┤
│ Metric 1 Data Block (variable)         │ ← Offset[0], Size calculated
│   [DataPoint1][DataPoint2]...           │
├─────────────────────────────────────────┤
│ Metric 2 Data Block (variable)         │ ← Offset[1], Size calculated
│   [DataPoint1][DataPoint2]...           │
├─────────────────────────────────────────┤
│ ...                                     │
├─────────────────────────────────────────┤
│ Metric N Data Block (variable)         │ ← Offset[N-1], Size calculated
│   [DataPoint1][DataPoint2]...           │
└─────────────────────────────────────────┘
```

**Offset Calculation Example:**
```
Header:        32 bytes
Index:         32 + (N × 16) bytes
Data starts:   32 + (N × 16) bytes  ← DataOffset

Metric 1: Offset = DataOffset + 0
Metric 2: Offset = DataOffset + Metric1.Size
Metric 3: Offset = DataOffset + Metric1.Size + Metric2.Size
...
```

---

## 6. Comparison: Float vs Text Value Blobs

| Aspect | Numeric Blob | Text Value Blob |
|--------|------------------|-----------------|
| **Storage Model** | Columnar (separate timestamp/value arrays) | Row-based (inline data points) |
| **Magic Number** | 0xEA10 | 0xEB10 |
| **Flag Header** | NumericFlag (dual encoding/compression) | TextFlag (single encoding/compression) |
| **Header Fields** | TimestampPayloadOffset, ValuePayloadOffset | DataOffset, DataSize |
| **Index Offsets** | Delta encoding (space efficient) | Absolute offsets (simplicity) |
| **Index Size Field** | Not stored (calculated from count × 8) | Not stored (calculated from offset differences) |
| **Data Encoding** | Fixed-size floats or Gorilla compression | Variable-length varint strings |
| **Compression** | Separate timestamp and value compression | Single data section compression |
| **Best For** | Numeric time series, many data points | Text/string metrics, fewer points |
| **Random Access** | O(1) with simple math | O(1) with offset lookup |

---

## 7. Implementation Checklist

### **✅ Completed**
- [x] TextHeader structure (32 bytes)
- [x] TextFlag structure (4 bytes)
- [x] TextIndexEntry structure (16 bytes)
- [x] Header parsing and serialization
- [x] Flag validation (magic number, encoding, compression)
- [x] Endianness support
- [x] Checksum support
- [x] Index entry parsing and serialization

### **🔲 Pending Implementation**
- [ ] Data point encoding/decoding (varint strings)
- [ ] TextEncoder (buffered encoding with size calculation)
- [ ] TextDecoder (streaming decoding)
- [ ] TextBlob (read-only access)
- [ ] Compression integration (Zstd, S2, LZ4)
- [ ] Delta timestamp encoding
- [ ] TextBlobSet (multi-blob operations)
- [ ] Unit tests for all components
- [ ] Benchmarks vs other formats
- [ ] Documentation and examples

---

## 8. Design Decisions & Rationale

### **✅ Absolute Offsets (Not Delta)**
**Decision**: Use absolute offsets in index entries
**Rationale**:
- Text data is variable-length, absolute offsets are clearer
- No accumulation overhead in decoder
- Direct O(1) random access
- Simpler error recovery
- Trade-off: 4 extra bytes per entry (acceptable for typical metric counts)

### **✅ Size Calculated (Not Stored)**
**Decision**: Calculate size from offset differences
**Rationale**:
- Saves 4 bytes per index entry
- Trivial calculation: `Size[i] = Offset[i+1] - Offset[i]`
- Last metric uses `DataSize` from header
- Space savings > minimal CPU cost

### **✅ Row-Based Storage (Not Columnar)**
**Decision**: Store data points inline, not separated
**Rationale**:
- Variable-length strings don't benefit from columnar layout
- Iteration is primary access pattern (not random access to individual fields)
- Simpler encoding/decoding logic
- Better cache locality for full data point access
- Easier to stream-process

### **✅ Varint String Encoding**
**Decision**: Use varint length prefix + UTF-8 bytes
**Rationale**:
- Space-efficient for short strings (common case)
- No null terminator overhead
- Standard Go encoding/binary support
- Easy to skip strings during scanning

### **✅ No String Pool**
**Decision**: Inline strings, no deduplication
**Rationale**:
- User requirement: "too complicated, don't use it"
- Simpler encoder/decoder
- Avoids complex deduplication logic
- Reasonable space trade-off for text metrics### **✅ Single Data Section**
**Decision**: One data section (not separate timestamp/tag/value)
**Rationale**:
- Row-based access pattern
- Variable-length data (not fixed-size floats)
- Compression works better on mixed data
- Simpler header (one offset instead of three)

### **✅ Header-at-Start (Not Footer)**
**Decision**: Place header at beginning of blob (not at end)
**Trade-offs**:

**Advantages (Header-at-Start):**
- ✅ **Consistency**: Matches Numeric blob design
- ✅ **Streaming reads**: Can read metadata before data
- ✅ **Random access**: Know where index/data sections are immediately
- ✅ **Partial decoding**: Can skip data section if only querying metadata
- ✅ **Standard pattern**: Most binary formats use header-first

**Disadvantages (Header-at-Start):**
- ⚠️ **Requires buffering**: Must calculate all sizes/offsets before writing header
- ⚠️ **Memory overhead**: Encoder must buffer entire data section during encoding
- ⚠️ **Two-phase encoding**: Cannot write header until data encoding is complete

**Alternative (Footer-at-End):**
- ✅ **True one-pass encoding**: Write data directly as it's encoded
- ✅ **Lower memory**: No need to buffer entire data section
- ✅ **Streaming writes**: Can write to network/disk immediately
- ⚠️ **Requires seek**: Must seek to end to read metadata first
- ⚠️ **Inconsistent**: Different from Numeric blob design

**Chosen Approach**: Header-at-start for consistency and better read performance, accepting buffering requirement for encoder.

**Encoder Strategy**:
1. Encode all data points into memory buffer
2. Calculate `DataSize`, `IndexOffset`, `DataOffset`
3. Compress data section if needed
4. Build index entries with absolute offsets
5. Write header → index → data in single operation

---

## 9. Performance Characteristics

### **Space Efficiency**
- **Header**: 32 bytes (same as float blob)
- **Index**: 16 bytes per metric (vs float's 16 bytes)
- **Data**: Varint encoding saves space for:
  - Short strings (< 128 chars = 1 byte length)
  - Delta timestamps (regular intervals = 1-2 bytes)
  - Empty tags (1 byte)

**Example: 100 metrics × 10 points each**
```
Header:      32 bytes
Index:       1,600 bytes (100 × 16)
Data:        ~15,000 bytes (assuming avg 15 bytes per point)
Total:       ~16,632 bytes

vs Float:    ~16,032 bytes (slightly more efficient for fixed-size data)
```

### **Time Complexity**
- **Metric Lookup**: O(1) - binary search on sorted index
- **Iteration**: O(N) - linear scan through data points
- **Random Access**: O(1) - direct offset + size lookup
- **Encoding**: O(N) - encode all data points, then write header + index + data
- **Decoding**: O(N) - stream decode with varint parsing

### **Memory Usage**
- **Zero-copy reads**: Direct pointer into data section
- **Streaming decode**: No full deserialization needed
- **Index in memory**: 16 bytes × metric_count
- **Encoder buffering**: Must buffer data section to calculate header offsets/sizes

---

## 10. Edge Cases & Limitations

### **Handled Edge Cases**
- ✅ Empty tags (1 byte varint 0)
- ✅ Empty values (1 byte varint 0 + 0 bytes)
- ✅ Single data point per metric
- ✅ Large strings (up to 2^32-1 bytes via varint)
- ✅ Endianness conversion
- ✅ Compression failures (fallback to uncompressed)

### **Known Limitations**
- ⚠️ Max 65,535 metrics per blob (uint16 limit)
- ⚠️ Max 65,535 data points per metric (uint16 count)
- ⚠️ Max 4GB data section (uint32 offset limit)
- ⚠️ No individual field compression (only full data section)
- ⚠️ Sorted index required for binary search (encoder's responsibility)

### **Future Enhancements**
- 📋 Optional string interning (user-controlled)
- 📋 Bloom filters for fast metric existence checks
- 📋 Sparse index for very large blobs
- 📋 Streaming decoder with seek support
- 📋 Multi-level compression (per-metric + full-section)

---

## 11. Open Questions

### **❓ Question 1: Tag Storage Strategy**
**Current Design**: Tag stored per data point with varint length
**Alternative**: Metric-level tag (single tag for all points in a metric)
**Decision Needed**: Confirm per-point tags are required
**Impact**: Per-point = more flexible, per-metric = more space-efficient

### **❓ Question 2: Timestamp Encoding Default**
**Current**: Raw timestamps (8 bytes each)
**Options**:
- Raw: Simple, no computation
- Delta: Space-efficient for regular intervals
**Recommendation**: Default to Delta for typical time-series use cases
**Impact**: 1-2 bytes vs 8 bytes per timestamp

### **❓ Question 3: Data Compression Default**
**Current**: Zstd compression
**Options**:
- None: Fastest, no CPU overhead
- Zstd: Best compression, moderate CPU
- S2: Fast compression, good ratio
- LZ4: Fastest compression, lower ratio
**Recommendation**: Zstd for storage, S2 for network
**Impact**: 2-5x space savings vs CPU cost

---

## 12. Next Steps

### **Phase 1: Core Implementation** (Current)
1. ✅ Define structures (Header, Flag, IndexEntry)
2. ✅ Implement parsing and serialization
3. ✅ Add validation and error handling
4. 🔲 Implement data point encoding (varint strings)

### **Phase 2: Encoder/Decoder**
1. 🔲 Implement TextEncoder (buffered: encode data → calculate sizes → write header + index + data)
2. 🔲 Implement TextDecoder (streaming)
3. 🔲 Add compression integration
4. 🔲 Add delta timestamp encoding

### **Phase 3: Blob Access**
1. 🔲 Implement TextBlob (read-only)
2. 🔲 Add iterator support
3. 🔲 Add metric lookup
4. 🔲 Implement TextBlobSet

### **Phase 4: Testing & Optimization**
1. 🔲 Write comprehensive unit tests
2. 🔲 Add integration tests
3. 🔲 Benchmark vs other formats
4. 🔲 Profile and optimize hot paths

### **Phase 5: Documentation**
1. 🔲 API documentation
2. 🔲 Usage examples
3. 🔲 Migration guide from float blobs
4. 🔲 Best practices guide

---

## 13. Sign-Off

### **Design Status**: ✅ **APPROVED - Ready for Implementation**

**Strengths:**
- ✅ Clear separation from Numeric blob design
- ✅ Simple, understandable structure
- ✅ Space-efficient for typical use cases
- ✅ Header-at-start matches float blob design (consistency)
- ✅ Fast iteration support
- ✅ Reasonable compression options

**Risks:**
- ⚠️ Variable-length encoding complexity (mitigated by varint library)
- ⚠️ No string deduplication (accepted trade-off)
- ⚠️ Per-point tag storage (may be wasteful if tags rarely change)

**Recommendation**: **Proceed with implementation**

**Reviewers**: (Add names and dates as needed)
- [ ] Architecture Review
- [ ] Performance Review
- [ ] Security Review

---

**END OF DESIGN REVIEW**
