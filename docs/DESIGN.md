# Mebo: High-Performance Time-Series Blob Format

## Overview

Mebo is a high-performance, space-efficient binary format for storing time-series metric data. The design is optimized for scenarios with many metrics but relatively few data points per metric (e.g., 150 metrics × 10 points), providing excellent compression ratios and fast lookup performance.

## Core Principles

- **Hash-Based Identification:** Metrics are identified by 64-bit xxHash64 hashes for fast O(1) lookups
- **Collision Detection:** Optional metric names payload for collision detection and verification (enabled when collisions occur)
- **Columnar Storage:** Timestamps and values are stored separately for optimal compression and access patterns
- **Flexible Encoding:** Per-blob configurable encoding strategies for both timestamps and values
- **Memory Efficiency:** Fixed-size structures enable single-pass encoding and O(1) hash map lookups

## Physical Layout

The blob is structured as a single contiguous memory block with 8-byte aligned payloads:

| Section                    | Size              | Description                                                              |
|----------------------------|-------------------|--------------------------------------------------------------------------|
| **Blob Header**            | 32 bytes (fixed)  | Metadata including flags, metric count, start time, and section offsets |
| **Metric Names Payload**   | Variable (optional)| Length-prefixed metric name strings (only when bit 2 = 1)               |
| **Metric Index**           | N × 16 bytes      | Array of IndexEntry structs with [Hash, Offsets, Count] in insertion order |
| *(Padding)*                | 0-7 bytes         | Padding to 8-byte boundary alignment                                    |
| **Timestamps Payload**     | Variable size     | All timestamps from all metrics, encoded + compressed                   |
| *(Padding)*                | 0-7 bytes         | Padding to 8-byte boundary alignment                                    |
| **Values Payload**         | Variable size     | All values from all metrics, encoded + compressed                       |

**Memory Layout Characteristics:**
-   **Byte Order (Endianness):** All multi-byte numeric values (integers and floating-point) use little-endian byte order by default, which is native to x86/x64 and ARM architectures. The header's `Flag.Options` field (bit 1) allows optional big-endian encoding: when bit 1 = 0 (default), little-endian is used; when bit 1 = 1, big-endian is used. This endianness applies consistently across all blob components: header fields, index entries, timestamps, and values. **Important:** For optimal performance, producers and consumers should use the same endianness to avoid conversion overhead. Mixed-endian environments should standardize on little-endian unless network byte order (big-endian) is specifically required.
-   **Single Contiguous Block:** The entire blob is designed as one continuous memory region for efficient I/O operations and memory mapping.
-   **8-Byte Alignment:** All major payload sections are aligned to 8-byte boundaries for optimal CPU cache line utilization and preventing unaligned memory access penalties.
-   **Sequential Layout:** The fixed-size-first, variable-size-last structure enables single-pass encoding without backtracking to update offsets.
-   **Direct Memory Access:** Fixed-size header and index entries enable O(1) random access via simple offset calculations, supporting zero-copy operations and memory-mapped file usage.

## Detailed Component Design

### Blob Header (32 bytes)

A small, fixed-size 32 bytes header containing critical metadata.

-   `Flags` (FlagHeader, 6 bytes): The flags (see the Go struct for details).
-   `MetricCount` (uint16): The number of unique metrics stored in the blob, max to 65535.
-   `StartTime` (int64): The earliest timestamps in the blob, unix timestamp in microseconds, allowing for fast sorting of multiple blobs.
-   `IndexOffset` / `TimestampPayloadOffset` / `ValuePayloadOffset` (uint32...): Byte offsets to the start of each major payload section.

```go
const (
	// Timestamp encodings (bits 0-3)
	TimestampEncodingNone  = 0x1
	TimestampTypeDelta = 0x2

	// Value encodings (bits 4-7)
	ValueEncodingNone    = TimestampEncodingNone << 4
	ValueTypeGorilla = TimestampTypeDelta << 4

	// Compression types (bits 0-3 for timestamp, 4-7 for value)
	CompressionNone   = 0x1
	CompressionZstd   = 0x2
	CompressionS2 = 0x3
	CompressionLZ4    = 0x4

	TimestampCompressionNone   = CompressionNone
	TimestampCompressionZstd   = CompressionZstd
	TimestampCompressionS2 = CompressionS2
	TimestampCompressionLZ4    = CompressionLZ4

	ValueCompressionNone   = CompressionNone << 4
	ValueCompressionZstd   = CompressionZstd << 4
	ValueCompressionS2 = CompressionS2 << 4
	ValueCompressionLZ4    = CompressionLZ4 << 4
)

type FlagHeader struct {
	// Options is a packed field for various options.
	// Bit 0 is tag support flag, 0 means no tag, 1 means tag enabled.
	// Bit 1 is endianness flag, 0 means little-endian, 1 means big-endian.
	// Bit 2 is metric names payload flag, 0 means no payload, 1 means metric names included (collision detection).
	// Bit 3 is reserved for future use, must be set to 0.
	// Bit 4-15 are magic number 0xEA10 to identify the blob format.
	Options uint16

	// EncodingType is an enum indicating the encoding used for this metric blob.
	// bit 0-3 for timestamp encoding, bit 4-7 for value format.
	EncodingType uint8
	// CompressionType is an enum indicating the compression used for this metric blob.
	// bit 0-3 for timestamp compression, bit 4-7 for value compression.
	CompressionType uint8
}

type Header struct {
	// StartTime is the start time of the metric. the unix timestamp in microseconds.
	StartTime int64
	// Flag is a packed field for various flags.
	Flag FlagHeader
	// IndexOffset is the byte offset to the start of the metric index section.
	IndexOffset uint32
	// TimestampPayloadOffset is the byte offset to the start of the timestamp payload section.
	TimestampPayloadOffset uint32
	// ValuePayloadOffset is the byte offset to the start of the value payload section.
	ValuePayloadOffset uint32
	// MetricCount is the number of unique metrics stored in the blob, max to 65535.
	MetricCount uint32
}
```

### Metric Names Payload (Optional)

**Purpose:** Store original metric name strings for hash collision detection and verification.

**When Enabled:**
- `Flag.Options` bit 2 = 1 (MetricNamesMask = 0x0004)
- Automatically enabled when encoder detects hash collision
- Can be manually enabled for additional verification

**Binary Format:**

Length-prefixed string list positioned immediately after header (32 bytes):

```
[Count: uint16] [Len1: uint16][Name1: UTF-8] [Len2: uint16][Name2: UTF-8] ...
```

**Field Descriptions:**
- `Count` (uint16): Number of metric names (must equal header's MetricCount)
- `Len` (uint16): Length of the following metric name string in bytes
- `Name` (UTF-8): Metric name string bytes (variable length, 0-65535 bytes)

**Example (3 metrics):**
```
Offset | Field       | Value (hex)     | Value (decoded)
-------|-------------|-----------------|------------------
0      | Count       | 0x03 0x00       | 3 (little-endian)
2      | Len1        | 0x09 0x00       | 9
4      | Name1       | 'c','p','u'...  | "cpu.usage"
13     | Len2        | 0x0C 0x00       | 12
15     | Name2       | 'm','e','m'...  | "memory.total"
27     | Len3        | 0x0B 0x00       | 11
29     | Name3       | 'd','i','s'...  | "disk.io.read"
```

**Ordering Requirement:**
- Metric names MUST be stored in the same order as index entries
- `metricNames[i]` corresponds to `indexEntries[i]`
- This allows sequential verification without additional index fields

**Storage Overhead:**

When enabled (collision case):
```
Size = 2 + Σ(2 + len(name_i))
```

For 150 metrics @ 30 char average:
```
Size = 2 + 150 × (2 + 30) = 4,802 bytes (~4.7 KB)
Relative increase ≈ 3.1% for typical 150 KB blob
```

When disabled (no collision, default):
```
Size = 0 bytes (zero overhead)
```

**Encoder Behavior:**
1. Start tracking metric names in `StartMetricName()`
2. Detect collision: `hash(name1) == hash(name2) && name1 != name2`
3. If collision: Set bit 2, prepare metric names payload
4. In `Finish()`: Encode payload if bit 2 = 1, update `IndexOffset`

**Decoder Behavior:**
1. Check `Flag.Options` bit 2
2. If bit 2 = 1: Parse metric names payload after header
3. Verify: For each `i`, check `hash(names[i]) == indexEntries[i].MetricID`
4. If mismatch: Return error with metric name and expected/actual hashes
5. `IndexOffset` = 32 (no names) or 32 + len(payload) (with names)

**Error Handling:**
- Truncated payload: `ErrInvalidMetricNamesPayload`
- Count mismatch: `ErrInvalidMetricNamesCount`
- Hash mismatch: `ErrHashMismatch` (includes metric name in error)
- Collision during encoding: `ErrHashCollision` (includes both names)

**See Also:**
- Collision analysis: `docs/HASH_COLLISION_ANALYSIS.md`
- Implementation: `encoding/metric_names.go`

### Metric Index

This is the core of the fast lookup system. The index is stored as a contiguous array of `IndexEntry` structs in insertion order in the blob, and during decoding it is loaded into a hash map for O(1) lookup performance.

#### Storage & Encoding:
1. Metrics are stored in **insertion order** (order they were added during encoding)
2. Index entries are written sequentially to the blob
3. **Cannot be reordered:** Delta offset encoding creates dependencies between consecutive entries
4. Each entry's offsets are deltas from the previous entry, requiring sequential decode

#### Lookup Process:
1. **At decode time:** Build hash map `map[uint64]IndexEntry` from index section (O(N) one-time cost)
   - Must process entries in order to accumulate offset deltas
2. **At query time:** Calculate xxHash64 64-bit hash of the target metric name
3. **Lookup:** Direct hash map access for O(1) retrieval of entry with data offsets

**Performance Characteristics:**
- **Decode time:** O(N) to build hash map and reconstruct absolute offsets from deltas
- **Lookup time:** O(1) hash map access
- **Memory overhead:** ~24 bytes per entry for map (8 bytes key + 16 bytes value)
- **Note:** Binary search is NOT possible due to delta offset encoding (cannot reorder entries)

#### Index Entry Structure (16 bytes):

-   `MetricID` (uint64): The unsigned 64-bit metricID or the xxHash64 64-bit hash of metric name string.
-   `Count` (uint16): The number of data points for this metric.
-   `TimestampOffset` (uint16): **Delta offset encoding** - Stores the offset delta from the previous metric's timestamp offset.
    -   **First metric**: Stores absolute offset from timestamp payload start (typically 0)
    -   **Subsequent metrics**: Stores delta = (current_offset - previous_offset)
    -   **Benefits**: Smaller delta values allow more efficient use of the uint16 range
    -   **Decoding**: Absolute offsets are reconstructed by accumulating deltas: `absolute_offset[i] = absolute_offset[i-1] + delta[i]`
-   `ValueOffset` (uint16): **Delta offset encoding** - Stores the offset delta from the previous metric's value offset.
    -   **First metric**: Stores absolute offset from value payload start (typically 0)
    -   **Subsequent metrics**: Stores delta = (current_offset - previous_offset)
    -   **Benefits**: Smaller delta values allow more efficient use of the uint16 range
    -   **Decoding**: Absolute offsets are reconstructed by accumulating deltas: `absolute_offset[i] = absolute_offset[i-1] + delta[i]`
-   Reserved 2 byte that padding to 16 bytes.

**Offset Delta Encoding Example:**
```
3 Metrics with raw encoding (8 bytes per timestamp, 8 bytes per value):
  Metric 1: 5 data points
    TimestampOffset stored as 0, ValueOffset stored as 0
  Metric 2: 3 data points
    TimestampOffset stored as 40 (delta: 5×8), ValueOffset stored as 40 (delta: 5×8)
  Metric 3: 7 data points
    TimestampOffset stored as 24 (delta: 3×8), ValueOffset stored as 24 (delta: 3×8)

Decoder reconstructs absolute offsets:
  Timestamps: [0, 40, 64]
  Values:     [0, 40, 64]
```
```go

type IndexEntry struct {
	// MetricID is the unsigned 64-bit  metric id or the hash of the metric name string.
	// It will use xxHash64 to hash the metric name string to a unsigned 64-bit integer.
	MetricID uint64
	// Count is the number of values for this metric.
	Count uint16
	// TimestampOffset stores the delta offset from the previous metric's timestamp offset.
	// First metric: absolute offset from payload start (typically 0)
	// Subsequent metrics: delta = (current_offset - previous_offset)
	// Decoder reconstructs: absolute_offset[i] = absolute_offset[i-1] + delta[i]
	TimestampOffset uint16
	// ValueOffset stores the delta offset from the previous metric's value offset.
	// First metric: absolute offset from payload start (typically 0)
	// Subsequent metrics: delta = (current_offset - previous_offset)
	// Decoder reconstructs: absolute_offset[i] = absolute_offset[i-1] + delta[i]
	ValueOffset uint16

	Reserved uint16 // 2 bytes (padding to 16 bytes)
}

```

#### Metric Name Hash Function

The design uses **xxHash64** (not xxHash64 as originally documented), chosen for:
- **Speed:** Extremely fast hashing (10+ GB/s throughput)
- **Quality:** Excellent avalanche characteristics and uniform distribution
- **Simplicity:** Simple algorithm, small code footprint

**Implementation:**
```go
func ID(data string) uint64 {
    return xxhash.Sum64String(data)
}
```

**Collision Handling:**

While xxHash64 provides excellent distribution, hash collisions are theoretically possible with any 64-bit hash function. Mebo handles this through:

1. **Collision Detection (Encoder):**
   - Tracks all metric names added to a blob
   - Detects when different names hash to the same ID
   - Automatically enables metric names payload when collision detected
   - Returns clear error message with both colliding metric names

2. **Metric Names Payload (Optional):**
   - Binary format positioned after header, before index
   - Only included when `Flag.Options` bit 2 = 1
   - Stores original metric name strings for verification
   - Zero storage overhead when no collisions (99.99%+ of blobs)

3. **Verification (Decoder):**
   - When metric names payload present, verifies hash(name) == MetricID
   - Detects data corruption or hash mismatches
   - Provides clear error messages for debugging

**Collision Probability:**

For typical workloads (150 metrics per blob):
- Single blob collision risk: ~10^-15 (negligible)
- 1 million blobs: Still negligible
- 50% collision probability: ~5 billion unique metrics (birthday paradox)

**See Also:** For detailed collision analysis and mitigation strategies, refer to `docs/HASH_COLLISION_ANALYSIS.md`.

-   Built into Go standard library (no dependencies)
-   Simple algorithm ensures cross-language compatibility

### Data Payloads

The time-series data is organized into two separate, columnar payloads to maximize compression efficiency and enable flexible encoding strategies.

#### Overall Strategy
- **Columnar Storage:** Timestamps and values are stored separately to optimize compression
- **Concatenated Layout:** All metrics' data within each payload type are concatenated sequentially
- **Index-Based Access:** Individual metric positions are tracked via offsets in the Metric Index
- **Unified Compression:** Each complete payload is compressed as a single block for maximum efficiency

#### Timestamps Payload

**Purpose:** Store all metric timestamps with optimized encoding for temporal data patterns.

**Layout Process:**
1. Concatenate all timestamps from all metrics sequentially
2. Apply encoding transformation (Raw or Delta-based)
3. Compress the entire payload as a single block
4. Track individual metric positions via `TimestampOffset` in index

**Encoding Options:**
- **Raw (0x1):** Direct int64 storage
  - **Pros:** Fastest access, O(1) random access, no decode overhead
  - **Cons:** Larger size (8 bytes per timestamp), no compression benefits
  - **Use Case:** When random timestamp access is critical or timestamps are highly irregular

- **Delta (0x2):** Delta-of-Delta + ZigZag + Varint encoding
  - **Algorithm:** First timestamp stored as full varint, second as delta, remaining as delta-of-deltas
  - **Pros:** Exceptional compression for regular intervals (~86% reduction), 1 byte per timestamp for regular data
  - **Space Savings:**
    - Regular intervals (1s, 1min): 86% compression (1 byte per timestamp after first two)
    - Semi-regular ±5% jitter: 75-85% compression
    - Irregular intervals: 40-60% compression
  - **Performance:** Sequential-only access, ~15% CPU overhead vs simple delta, O(N) decode
  - **Use Case:** Time-series metrics with regular or semi-regular sampling (recommended for 99% of cases)
  - **Details:** See `docs/DELTA_OF_DELTA_ENCODING.md` for complete algorithm and analysis

**Compression:** Applied after encoding using algorithm specified in header (Zstd, S2, LZ4, or None).

#### Values Payload

**Purpose:** Store all metric values with flexible encoding to balance performance and space efficiency.

**Layout Process:**
1. Concatenate all values from all metrics sequentially
2. Apply encoding transformation (Raw or Gorilla-based)
3. Optionally compress the entire payload as a single block
4. Track individual metric positions via `ValueOffset` in index

**Encoding Options:**
- **Raw (0x10):** Direct float64 storage
  - **Pros:** Fastest iteration, O(1) random access, no decode overhead
  - **Cons:** No space savings, 8 bytes per value
  - **Use Case:** Frequently accessed data, random patterns, maximum performance

- **Gorilla (0x20):** XOR-based compression
  - **Pros:** Excellent compression for stable/predictable values, ~70% size reduction
  - **Cons:** Sequential-only access, decode overhead
  - **Use Case:** Slowly changing metrics (temperature, voltage, system stats)

**Compression:** Optional second-stage compression (typically None for performance, or Zstd for cold storage).

#### Access Patterns

| Payload Type | Encoding | Random Access | Sequential Access | Decode Overhead |
|--------------|----------|---------------|-------------------|-----------------|
| Timestamps | Raw | O(1) | O(N) | None |
| Timestamps | Delta | O(N) | O(N) | Low |
| Values | Raw | O(1) | O(N) | None |
| Values | Gorilla | O(N) | O(N) | Medium |

#### Implementation Notes

- **Offset Calculation:** Each metric's data position is calculated as `PayloadStart + IndexEntry.Offset`
- **Payload Limits:** uint16 offsets limit each payload to 64KB maximum
- **Memory Alignment:** Payloads are padded to 8-byte boundaries for optimal CPU access
- **Compression Boundary:** Compression is applied to the complete payload, not per-metric

## Design Considerations and Limitations

### Blob Size Analysis

**Offset Delta Encoding Impact:**

With delta encoding for BOTH offsets, the effective addressable space is significantly larger than the naive uint16 limit:

**Theoretical Limits:**
- **Without delta encoding:** 64KB per payload (uint16 max = 65,535)
- **With delta encoding:** Limited by the size of individual metric deltas, not cumulative offset
  - If each metric delta ≤ 65,535 bytes, you can have unlimited metrics
  - Practical limit: Total payload size constrained by memory and compression/decompression buffer sizes

**Practical Limits:**
- **Per-metric delta:** Must fit in uint16 (≤65,535 bytes per metric for both timestamps and values)
- **Total payload:** Constrained by available memory, not by uint16 offset range
- **Example:** 10,000 metrics × 100 bytes each = 1MB per payload ✅ (each delta = 100 bytes)

**Component Size Limits:**
- **Header:** 32 bytes (fixed)
- **Index:** Unlimited by design (uint32 MetricCount supports 4.2B metrics, but practical limit ~10K metrics)
- **Timestamps Payload:** Effectively unlimited with delta encoding (each metric delta must fit in uint16)
- **Values Payload:** Effectively unlimited with delta encoding (each metric delta must fit in uint16)

**Maximum Blob Size Calculation:**
```
Max Blob Size = Header + Index + Timestamps + Values + Padding
              = 32 bytes + Index Size + 64KB + 64KB + ~24 bytes padding
              = 32 + Index Size + 131,096 + 24
              = 131,152 + Index Size
```

**Index Size by Metric Count:**
| Metrics | Index Size | Max Blob Size | Notes |
|---------|-----------|---------------|-------|
| 100 | 1,600 B | ~129 KB | Optimal range |
| 500 | 8,000 B | ~136 KB | Good performance |
| 1,000 | 16,000 B | ~144 KB | Near practical limit |
| 4,000 | 64,000 B | ~191 KB | Maximum recommended |

**Practical Blob Size:** 129KB - 191KB (depending on metric count)

**Data Capacity Examples (with delta encoding for both offsets):**
- **Raw Timestamps:** Effectively unlimited total, limited by per-metric size:
  - 100 metrics × 8,192 timestamps each = 6.4MB total payload ✅
  - Each metric delta = 64KB (8,192 × 8 bytes), fits in uint16
- **Raw Values:** Effectively unlimited total, limited by per-metric size:
  - 100 metrics × 8,192 values each = 6.4MB total payload ✅
  - Each metric delta = 64KB (8,192 × 8 bytes), fits in uint16
- **Compressed Payloads:** Even better - deltas compress the encoded data, not the offsets
- **Practical Scenario:** 1,000 metrics × 100 data points each:
  - Timestamp payload: 800KB (per-metric delta = 800 bytes)
  - Value payload: 800KB (per-metric delta = 800 bytes)
  - Total addressing range extended by 12.5× compared to absolute offsets

### Other Design Constraints
-   **Payload Alignment:** All major payloads are aligned to an 8-byte memory boundary by adding padding where necessary. This prevents potential unaligned memory access penalties on certain CPU architectures.
-   **Random Access Trade-offs:**
    -   **Values (`Raw`):** True **O(1)** random access.
    -   **Values (`Gorilla`) / Timestamps:** Require an O(N) scan from the start of the metric's data. For fast random access with these encodings, the data would need to be further broken into smaller, indexed sub-chunks.

## Example: 150 Metrics × 10 Points

**Note:** This example benefits from delta encoding for both TimestampOffset and ValueOffset. Each metric requires only ~80 bytes for timestamps and ~80 bytes for values, so deltas easily fit within uint16 range.

### Size Breakdown
| Component | Size | Details |
|-----------|------|---------|
| Header | 32 B | Fixed |
| Index | 2,400 B | 150 × 16 bytes |
| Timestamps | ~300 B | Delta + Zstd compressed |
| Values | 12,000 B | Raw float64 |
| **Total** | **14,732 B** | ~14.4 KB |

## Encoding Selection Guide

### Timestamps
- **Delta + S2**: Best for regular intervals (99% use cases)
- **Delta + Zstd**: Best for maximum compression rate
- **Raw + None**: Only for random access requirements

## Usage Guidelines

### When to Use Different Encodings

**Timestamps:**
- **Delta + S2:** Regular interval metrics (recommended for 99% of cases)
- **Delta + Zstd:** When the data size matters
- **Raw + None:** When random timestamp access is required

**Values:**
- **Raw + None:** Frequently accessed data, random patterns, maximum performance
- **Gorilla + None:** Slowly changing metrics (temperature, voltage, etc.)
- **Raw + Zstd:** Cold storage with infrequent access

### Blob Size Recommendations

**With Delta Encoding (current implementation for both offsets):**
- **Optimal Size:** 4KB - 256KB per blob for best performance
- **Both Payloads:** Effectively unlimited total size (limited by per-metric delta ≤65,535 bytes)
- **Practical Recommendations:**
  - For 100-500 metrics: Target 32KB - 512KB total blob size
  - For 1000+ metrics: Can scale to several MB with appropriate per-metric sizing
  - Keep individual metric data < 32KB for optimal delta efficiency

**Per-Metric Constraints:**
- Each metric's timestamp delta must fit in uint16 (≤65,535 bytes)
- Each metric's value delta must fit in uint16 (≤65,535 bytes)
- With raw encoding (8 bytes/point): Max 8,191 data points per metric
- With delta/compressed encoding: Typically supports 10,000+ data points per metric## Implementation Considerations

### Offset Delta Encoding (TimestampOffset & ValueOffset)

Both `TimestampOffset` and `ValueOffset` fields in `IndexEntry` use delta encoding to maximize the effective range of the uint16 type.

**Encoding Process (in NumericEncoder):**
```go
// In EndMetric() method:
tsOffsetDelta := e.tsOffset - e.lastTsOffset    // Calculate delta from previous
valOffsetDelta := e.valOffset - e.lastValOffset // Calculate delta from previous
entry.TimestampOffset = uint16(tsOffsetDelta)   // Store delta, not absolute
entry.ValueOffset = uint16(valOffsetDelta)      // Store delta, not absolute
e.lastTsOffset = e.tsOffset                     // Update for next metric
e.lastValOffset = e.valOffset                   // Update for next metric
```

**Decoding Process (in NumericDecoder):**
```go
// In Decode() method:
var lastTsOffset uint16
var lastValOffset uint16
for i := 0; i < d.metricCount; i++ {
    entry.Parse(indexData[start:end], d.engine)

    // Convert deltas to absolute offsets by accumulation
    entry.TimestampOffset += lastTsOffset
    entry.ValueOffset += lastValOffset
    blob.indexEntryMap[entry.MetricID] = entry

    // Update for next iteration
    lastTsOffset = entry.TimestampOffset
    lastValOffset = entry.ValueOffset
}
```
**Why Delta Encoding:**
- **Smaller values**: Deltas between consecutive metric offsets are typically much smaller than absolute offsets
- **Extended range**: For example, with 100-byte deltas, you can address 6.5MB of data vs 64KB with absolute offsets
- **No overhead**: Same 16-bit storage, just different interpretation
- **Sequential benefit**: Works naturally with sequential metric processing
- **Applies to both payloads**: Both timestamp and value payloads benefit equally from extended addressing

**Example Comparison:**
```
Scenario: 1000 metrics, each with 50 timestamps (400 bytes in raw encoding)

Absolute offsets approach:
  Metric 1:    offset = 0
  Metric 2:    offset = 400
  Metric 1000: offset = 399,600  ❌ Exceeds uint16 (65,535)

Delta encoding approach:
  Metric 1:    delta = 0 (first metric)
  Metric 2:    delta = 400
  Metric 1000: delta = 400 ✅ All deltas fit in uint16

  Decoder reconstructs: offset[1000] = 400 × 999 = 399,600
```

**Implementation Notes:**
- First metric always stores 0 for both offsets (absolute offsets from payload starts)
- Encoder maintains `lastTsOffset` and `lastValOffset` state across `EndMetric()` calls
- Decoder accumulates deltas sequentially to reconstruct absolute offsets for both types
- Absolute offsets enable direct payload access without recalculation
- Delta encoding is transparent to users of the NumericBlob API

### Memory Alignment
- All payload sections aligned to 8-byte boundaries
- Prevents unaligned access penalties on modern CPUs
- Add padding bytes between sections as needed


### Single-Pass Encoding
The fixed-size header and index enable efficient single-pass encoding:

1. Pre-allocate header with known offsets
2. Build fixed-size index (can calculate offsets ahead of time)
3. Sequentially append encoded payloads
4. Update header with final offsets
5. Calculate and write checksum

### External Name Management

If application doesn't have metric ID with unsigned 64-bit integer, it needs to pass the metric name string and hash into unsigned 64-bit integer.
Since only hashes are stored, applications must maintain hash→name mappings:

```go
type MetricRegistry struct {
    hashToName map[uint64]string
    nameToHash map[string]uint64
    mu         sync.RWMutex
}

func (r *MetricRegistry) RegisterMetric(name string) uint64 {
    hash := HashMetricName(name)
    r.mu.Lock()
    r.hashToName[hash] = name
    r.nameToHash[name] = hash
    r.mu.Unlock()
    return hash
}

func (r *MetricRegistry) GetName(hash uint64) (string, bool) {
    r.mu.RLock()
    name, exists := r.hashToName[hash]
    r.mu.RUnlock()
    return name, exists
}
```

## Trade-offs and Limitations

### Advantages
- **Excellent compression:** 40-60% size reduction typical
- **Fast lookups:** O(1) hash map lookup for metric access (insertion-order storage required by delta encoding)
- **Flexible encoding:** Choose optimal strategy per use case
- **Memory efficient:** Minimal overhead, cache-friendly structures
- **Extended addressing:** Delta encoding allows uint16 offsets to address much larger payloads effectively
- **Platform independent:** Well-defined binary format

### Limitations
- **External names:** Requires application-level hash→name mapping
- **Sequential access:** Compressed/encoded data requires sequential decoding
- **Per-metric size constraint:** Each metric's data delta must fit in uint16 (≤65,535 bytes for both timestamps and values)
- **Sequential metric ordering:** Delta encoding requires metrics to be processed in index order during decoding
- **No schema evolution:** Format changes require version bumps

## Conclusion

The Mebo format provides an optimal balance of space efficiency, lookup performance, and encoding flexibility for time-series metric data. The design is particularly well-suited for scenarios with many metrics and relatively few points per metric, achieving significant compression while maintaining fast access patterns.

Key innovations include:
- **Hash-based identification:** Eliminates metric name storage overhead with collision-free 64-bit xxHash64 hashes
- **Dual delta offset encoding:** Both TimestampOffset AND ValueOffset use delta encoding, extending the effective addressing range of uint16 offsets for both payloads
- **Columnar storage:** Separates timestamps and values for optimal compression and access patterns
- **Flexible encoding:** Per-blob configurable strategies (Raw, Delta, Gorilla) with optional compression

This makes Mebo suitable for both small, focused datasets and large-scale monitoring scenarios with thousands of metrics and millions of data points.

Combined with O(1) hash map lookups and memory-efficient fixed-size structures, Mebo delivers both high performance and compact representation for time-series workloads.
