# Text Blob Implementation Plan

## Date: October 4, 2025
## Status: Ready for Discussion

---

## 🎯 Executive Summary

This document outlines the implementation plan for **Text Value Blob**, incorporating all the improvements and lessons learned from the Numeric Blob implementation.

**Key Improvements to Apply:**
1. ✅ **Exclusive Identifier Modes** (ID vs Name)
2. ✅ **Collision Detection & Handling**
3. ✅ **Optional Tag Support** (performance optimization)
4. ✅ **Metric Name-Based Access** (with collision handling)
5. ✅ **Iterator-Based API** (Go 1.23+ `iter.Seq`)
6. ✅ **BlobSet for Multi-Blob Queries**
7. ✅ **Header Immutability Pattern**
8. ✅ **Lazy Resource Allocation**

---

## 📋 Design Updates from Original

### **1. Header Structure** (Updated)

```go
// section/text_header.go
type TextHeader struct {
    Flag        TextFlag  // 4 bytes
    StartTime   int64     // 8 bytes - blob-level timestamp for sorting
    IndexOffset uint32    // 4 bytes - offset to index section
    DataOffset  uint32    // 4 bytes - offset to data section
    DataSize    uint32    // 4 bytes - compressed data size (0 if uncompressed)
    MetricCount uint32    // 4 bytes
    Checksum    uint32    // 4 bytes - CRC32
}
```

**Changes from Original:**
- ✅ **No change needed** - current design is solid

---

### **2. Flag Structure** (Updated)

```go
// section/text_flag.go
type TextFlag struct {
    Options           uint16  // Bits: [15:4]=magic(0xEB10), [3]=hasMetricNames, [2]=hasTag, [1]=endian, [0]=checksum
    TimestampEncoding uint8   // TypeRaw, TypeDelta
    DataCompression   uint8   // None, Zstd, S2, LZ4
}
```

**Changes from Original:**
- ✅ **Added `hasMetricNames` bit** (bit 3) - tracks collision detection
- ✅ **Added `hasTag` bit** (bit 2) - optional tag support
- ✅ **Reserved bits reduced** - now using bits [3:2] for features

**Rationale:**
- Matches NumericFlag design for consistency
- Enables collision handling like numeric blob
- Supports optional tags for performance optimization

---

### **3. Index Entry Structure** (Updated)

```go
// section/text_index_entry.go
type TextIndexEntry struct {
    MetricID  uint64  // 8 bytes - xxHash64 hash
    Count     uint16  // 2 bytes - number of data points
    Reserved1 uint16  // 2 bytes - for future use
    Offset    uint32  // 4 bytes - absolute offset in data section
    // Size is CALCULATED (not stored): Size[i] = Offset[i+1] - Offset[i]
}
```

**Serialization (16 bytes on disk):**
```
[MetricID:8][Count:2][Reserved:2][Offset:4]
```

**Changes from Original:**
- ✅ **No structural changes** - design is optimal
- ✅ **Absolute offsets** remain the best choice for variable-length text data
- ✅ **Size calculation** keeps it space-efficient

---

### **4. Encoder Architecture** (NEW - Major Update)

```go
// blob/text_encoder.go

// textIdentifierMode defines how metrics are identified (same as numeric blob)
type textIdentifierMode uint8

const (
    modeUndefined textIdentifierMode = iota
    modeUserID    // User provides IDs, no collision handling
    modeNameManaged // Mebo manages IDs, collision detection enabled
)

type TextEncoder struct {
    *TextEncoderConfig

    // Data encoders
    dataBuffer    *bytes.Buffer        // Buffer for all data points
    tsEncoder     encoding.ColumnarEncoder[int64]   // For timestamp encoding
    pointEncoder  *textDataPointEncoder // Custom encoder for data points

    // State tracking
    curMetricID   uint64
    claimed       int

    // Metric tracking (per-metric data)
    indexEntries  []section.TextIndexEntry
    dataOffsets   []uint32  // Track offset of each metric's data

    // Collision detection - mode-specific (like numeric blob)
    collisionTracker *collision.Tracker    // Name mode only
    usedIDs          map[uint64]struct{}   // ID mode only
    identifierMode   textIdentifierMode

    // Header immutability
    hasCollision  bool  // Pending collision flag

    // Compression codecs
    dataCodec     compress.Codec

    // Configuration
    config        *TextEncoderConfig
    header        *section.TextHeader
}

// textDataPointEncoder handles encoding of individual data points
type textDataPointEncoder struct {
    buf     *bytes.Buffer
    engine  endian.EndianEngine
}
```

**Key Features:**
1. ✅ **Dual Identifier Modes** - ID vs Name (exclusive)
2. ✅ **Lazy Resource Allocation** - collision tracker only when needed
3. ✅ **Buffered Encoding** - accumulate data, then write header
4. ✅ **Header Immutability** - clone pattern in Finish()
5. ✅ **Efficient State Tracking** - minimal memory overhead

---

### **5. Encoder API** (NEW)

```go
// Core Encoder Methods (matching numeric blob pattern)

func NewTextEncoder(blobTs time.Time, opts ...TextEncoderOption) (*TextEncoder, error)

// Exclusive identifier modes (cannot mix)
func (e *TextEncoder) StartMetricID(metricID uint64, numOfDataPoints int) error
func (e *TextEncoder) StartMetricName(metricName string, numOfDataPoints int) error

// Add data points
func (e *TextEncoder) AddDataPoint(timestamp int64, value string, tag string) error

// Complete current metric
func (e *TextEncoder) EndMetric() error

// Finalize blob
func (e *TextEncoder) Finish() ([]byte, error)

// Utility
func (e *TextEncoder) MetricCount() int
func (e *TextEncoder) DataPointCount() int
```

**Behavior:**
- `StartMetricID()` → **ID Mode**: no collision handling, user provides IDs
- `StartMetricName()` → **Name Mode**: collision detection, metric names stored if collision
- Cannot mix modes (returns `ErrMixedIdentifierMode`)

---

### **6. Data Point Encoding** (Detailed)

```go
// Single data point encoding (per metric)

type TextDataPoint struct {
    Timestamp int64   // Raw: 8 bytes, Delta: varint
    Value     string  // uint8 length (max 256) + UTF-8 bytes
    Tag       string  // uint8 length (max 256) + UTF-8 bytes (0 if no tag)
}

// Encoding layout:
// [Timestamp][ValueLen:uint8][Value:bytes][TagLen:uint8][Tag:bytes]

func (e *textDataPointEncoder) Encode(dp TextDataPoint, hasTags bool, useDelta bool, prevTs int64) error {
    // Validate string lengths (max 256 chars)
    if len(dp.Value) > 256 {
        return fmt.Errorf("value length exceeds 256 characters: %d", len(dp.Value))
    }
    if hasTags && len(dp.Tag) > 256 {
        return fmt.Errorf("tag length exceeds 256 characters: %d", len(dp.Tag))
    }

    // Encode timestamp
    if useDelta {
        delta := dp.Timestamp - prevTs
        e.encodeVarint(delta)  // 1-2 bytes for regular intervals
    } else {
        e.engine.PutUint64(dp.Timestamp)  // 8 bytes
    }

    // Encode value (uint8 length prefix)
    e.buf.WriteByte(uint8(len(dp.Value)))  // 1 byte length (max 256)
    e.buf.Write([]byte(dp.Value))

    // Encode tag (if enabled)
    if hasTags {
        e.buf.WriteByte(uint8(len(dp.Tag)))  // 0 = empty tag (1 byte)
        if len(dp.Tag) > 0 {
            e.buf.Write([]byte(dp.Tag))
        }
    }
}
```

**Space Efficiency:**
- **All strings**: 1 byte length prefix (uint8, max 256 chars)
- **Empty tags** (when enabled): 1 byte (length=0)
- **Delta timestamps**: 1-2 bytes for regular intervals
- **Example**: 10-char string + empty tag = 1 + 10 + 1 = **12 bytes**
- **Hard limit**: 256 characters for both values and tags

---

### **7. Decoder Architecture** (NEW)

```go
// blob/text_decoder.go

type TextDecoder struct {
    engine        endian.EndianEngine
    data          []byte
    header        *section.TextHeader
    indexEntries  []section.TextIndexEntry
    metricNameMap map[string]section.TextIndexEntry  // Only if HasMetricNames
    dataPayload   []byte

    // Codecs
    dataCodec     compress.Codec
}

func NewTextDecoder(data []byte) (*TextDecoder, error)
func (d *TextDecoder) Decode() (*TextBlob, error)
func (d *TextDecoder) DecodeHeader() (*section.TextHeader, error)
```

**Decoding Phases:**
1. Parse header (32 bytes)
2. Parse index section (N × 16 bytes)
3. Decompress data section (if compressed)
4. Parse metric names payload (if HasMetricNames)
5. Build lookup maps

---

### **8. Blob API** (NEW - Iterator-Based)

```go
// blob/text_blob.go

type TextBlob struct {
    engine        endian.EndianEngine
    startTime     time.Time
    indexEntryMap map[uint64]section.TextIndexEntry
    metricNameMap map[string]section.TextIndexEntry  // Only if collision
    dataPayload   []byte
    flag          section.TextFlag
}

// TextDataPoint mirrors NumericDataPoint
type TextDataPoint struct {
    Ts   int64   // Timestamp
    Val  string  // Text value
    Tag  string  // Optional tag
}

// Iterator-based access (matching numeric blob)
func (b *TextBlob) All(metricID uint64) iter.Seq2[int, TextDataPoint]
func (b *TextBlob) AllTimestamps(metricID uint64) iter.Seq[int64]
func (b *TextBlob) AllValues(metricID uint64) iter.Seq[string]
func (b *TextBlob) AllTags(metricID uint64) iter.Seq[string]

// Name-based access (if collision occurred)
func (b *TextBlob) AllByName(metricName string) iter.Seq2[int, TextDataPoint]
func (b *TextBlob) AllValuesByName(metricName string) iter.Seq[string]
func (b *TextBlob) AllTimestampsByName(metricName string) iter.Seq[int64]
func (b *TextBlob) AllTagsByName(metricName string) iter.Seq[string]

// Metadata
func (b *TextBlob) StartTime() time.Time
func (b *TextBlob) Len(metricID uint64) int
func (b *TextBlob) LenByName(metricName string) int

// Random access (O(1) offset + O(N) scan to target index)
func (b *TextBlob) ValueAt(metricID uint64, index int) (string, bool)
func (b *TextBlob) TimestampAt(metricID uint64, index int) (int64, bool)
func (b *TextBlob) TagAt(metricID uint64, index int) (string, bool)
```

**Key Differences from Numeric Blob:**
- ✅ Values are `string` instead of `float64`
- ✅ Random access is **O(N) scan** (not O(1)) due to variable-length encoding
- ✅ Iteration is still **O(N) linear** (same performance)
- ✅ API signature matches numeric blob for consistency

---

### **9. BlobSet API** (NEW)

```go
// blob/text_blob_set.go

type TextBlobSet struct {
    blobs []TextBlob
}

func NewTextBlobSet(blobs []TextBlob) (*TextBlobSet, error)

// Iterator-based access across all blobs (same as numeric blob)
func (s *TextBlobSet) All(metricID uint64) iter.Seq2[int, TextDataPoint]
func (s *TextBlobSet) AllTimestamps(metricID uint64) iter.Seq[int64]
func (s *TextBlobSet) AllValues(metricID uint64) iter.Seq[string]
func (s *TextBlobSet) AllTags(metricID uint64) iter.Seq[string]

// Random access across all blobs
func (s *TextBlobSet) ValueAt(metricID uint64, index int) (string, bool)
func (s *TextBlobSet) TimestampAt(metricID uint64, index int) (int64, bool)
func (s *TextBlobSet) TagAt(metricID uint64, index int) (string, bool)

// Metadata
func (s *TextBlobSet) Len() int
func (s *TextBlobSet) TimeRange() (start, end time.Time)
```

**Behavior:**
- Blobs sorted by `StartTime` (ascending)
- Seamless iteration across time windows
- Sparse data handling (skip blobs without metric)

---

### **10. Encoder Options** (NEW)

```go
// blob/text_encoder_options.go

type TextEncoderOption = options.Option[*TextEncoderConfig]

// Endianness
func WithLittleEndian() TextEncoderOption
func WithBigEndian() TextEncoderOption

// Encoding
func WithTimestampEncoding(enc format.EncodingType) TextEncoderOption  // TypeRaw, TypeDelta (default: TypeDelta)

// Compression
func WithDataCompression(comp format.CompressionType) TextEncoderOption  // None, Zstd, S2, LZ4 (default: Zstd)

// Features
func WithTagsEnabled() TextEncoderOption  // Enable per-point tags (default: disabled)

// Checksum
func WithChecksum() TextEncoderOption  // Enable CRC32 checksum (default: enabled)

// NOTE: The following options from NumericEncoder do NOT apply to TextEncoder:
// - WithValueEncoding() - Text values are always stored as uint8-length-prefixed strings
// - WithTimestampCompression() - Only data section compression is available (WithDataCompression)
// - WithValueCompression() - Use WithDataCompression() instead (compresses entire data section)
```

**Recommended Defaults:**
- **Endianness**: Little-endian (most common)
- **Timestamp Encoding**: Delta (space-efficient, user can override with `WithTimestampEncoding()`)
- **Data Compression**: Zstd (best compression, user can override with `WithDataCompression()`)
- **Tags**: Disabled (enable only if needed with `WithTagsEnabled()`)
- **Checksum**: Enabled (data integrity)

**Important Notes:**
- `WithValueEncoding()` - **Does NOT apply to text blob** (only numeric blob)
- `WithTimestampCompression()` - **Does NOT apply to text blob** (only numeric blob)
- `WithValueCompression()` - Controls text blob data section compression
- **String length limits**: 256 characters for values and tags

---

## 🔄 Implementation Phases

### **Phase 1: Core Structures** ✅ (Partially Done)

**Files to Create/Update:**
- [x] `section/text_flag.go` - Update with `hasMetricNames`, `hasTag` bits
- [x] `section/text_header.go` - Already exists
- [x] `section/text_index_entry.go` - Already exists

**Tasks:**
1. Update `TextFlag` with new option bits
2. Add flag methods: `HasMetricNames()`, `SetHasMetricNames()`, `HasTag()`, `SetHasTag()`
3. Add validation for flag bits
4. Unit tests for flag operations

**Estimated Time:** 2-3 hours

---

### **Phase 2: Data Point Encoding** 🆕

**Files to Create:**
- `blob/text_data_point_encoder.go` - Internal data point encoder

**Implementation:**
```go
type textDataPointEncoder struct {
    buf    *bytes.Buffer
    engine endian.EndianEngine
}

func (e *textDataPointEncoder) encodeVarint(val int64) error
func (e *textDataPointEncoder) encodeString(s string) error  // uint8 length, max 256 chars
func (e *textDataPointEncoder) encodeDataPoint(ts int64, val string, tag string, hasTags bool, useDelta bool, prevTs int64) error
func (e *textDataPointEncoder) reset()
func (e *textDataPointEncoder) bytes() []byte
```

**Tasks:**
1. Implement varint encoding (for timestamps only)
2. Implement string encoding (uint8 length + bytes, max 256 chars)
3. **Add string length validation** (return error if > 256 chars)
4. Implement data point encoding (timestamp + value + tag)
5. Handle delta vs raw timestamps
6. Handle optional tags
7. Unit tests for all encoding paths
8. **Unit tests for length limit validation**

**Estimated Time:** 4-5 hours

---

### **Phase 3: TextEncoder** 🆕 (Critical)

**Files to Create:**
- `blob/text_encoder.go` - Main encoder
- `blob/text_encoder_config.go` - Configuration
- `blob/text_encoder_options.go` - Functional options

**Implementation:**
```go
// Core encoder with dual identifier modes
type TextEncoder struct {
    // ... (see detailed structure above)
}

func NewTextEncoder(blobTs time.Time, opts ...TextEncoderOption) (*TextEncoder, error)
func (e *TextEncoder) StartMetricID(metricID uint64, numOfDataPoints int) error
func (e *TextEncoder) StartMetricName(metricName string, numOfDataPoints int) error
func (e *TextEncoder) AddDataPoint(timestamp int64, value string, tag string) error
func (e *TextEncoder) EndMetric() error
func (e *TextEncoder) Finish() ([]byte, error)
func (e *TextEncoder) cloneHeader() *section.TextHeader
```

**Key Patterns to Apply:**
1. ✅ **Exclusive Modes** - ID vs Name (cannot mix)
2. ✅ **Lazy Allocation** - collision tracker only in Name mode
3. ✅ **Header Immutability** - `cloneHeader()` in `Finish()`
4. ✅ **Efficient State** - encoderState pattern
5. ✅ **Collision Handling** - auto-enable metric names on collision

**Tasks:**
1. Implement `NewTextEncoder()` with option parsing
2. Implement `StartMetricID()` (ID mode)
3. Implement `StartMetricName()` (Name mode with collision detection)
4. Implement `AddDataPoint()` with validation
5. Implement `EndMetric()` with offset tracking
6. Implement `Finish()` with buffered write strategy:
   - Compress data section if needed
   - Calculate all offsets
   - Build metric names payload if collision
   - Clone header and apply pending changes
   - Write: header → index → data → metric names
7. Add mode validation (prevent mixing)
8. Add comprehensive unit tests

**Estimated Time:** 12-15 hours

---

### **Phase 4: TextDecoder** 🆕

**Files to Create:**
- `blob/text_decoder.go` - Main decoder
- `blob/text_data_point_decoder.go` - Internal data point decoder

**Implementation:**
```go
type TextDecoder struct {
    engine        endian.EndianEngine
    data          []byte
    header        *section.TextHeader
    indexEntries  []section.TextIndexEntry
    dataPayload   []byte
    dataCodec     compress.Codec
}

func NewTextDecoder(data []byte) (*TextDecoder, error)
func (d *TextDecoder) Decode() (*TextBlob, error)
func (d *TextDecoder) DecodeHeader() (*section.TextHeader, error)
```

**Data Point Decoder:**
```go
type textDataPointDecoder struct {
    buf       []byte
    engine    endian.EndianEngine
    pos       int
    hasTags   bool
    useDelta  bool
    prevTs    int64
}

func (d *textDataPointDecoder) decodeVarint() (int64, error)
func (d *textDataPointDecoder) decodeString() (string, error)  // Reads uint8 length, max 256 chars
func (d *textDataPointDecoder) decodeDataPoint() (TextDataPoint, error)
```

**Tasks:**
1. Implement header parsing with validation
2. Implement index section parsing
3. Implement data decompression
4. Implement metric names payload parsing
5. Implement varint decoding (for timestamps)
6. Implement string decoding (uint8 length, max 256 chars)
7. **Add string length validation** (error if length > 256)
8. Implement data point decoding
9. Build index maps (ID → entry, name → entry)
10. Unit tests for all decoding paths
11. **Unit tests for length limit validation**

**Estimated Time:** 10-12 hours

---

### **Phase 5: TextBlob** 🆕 (Iterator API)

**Files to Create:**
- `blob/text_blob.go` - Read-only blob with iterators

**Implementation:**
```go
type TextBlob struct {
    // ... (see detailed structure above)
}

// Iterator methods (primary API)
func (b *TextBlob) All(metricID uint64) iter.Seq2[int, TextDataPoint]
func (b *TextBlob) AllTimestamps(metricID uint64) iter.Seq[int64]
func (b *TextBlob) AllValues(metricID uint64) iter.Seq[string]
func (b *TextBlob) AllTags(metricID uint64) iter.Seq[string]

// Name-based methods (if collision)
func (b *TextBlob) AllByName(metricName string) iter.Seq2[int, TextDataPoint]
func (b *TextBlob) AllValuesByName(metricName string) iter.Seq[string]
func (b *TextBlob) AllTimestampsByName(metricName string) iter.Seq[int64]
func (b *TextBlob) AllTagsByName(metricName string) iter.Seq[string]

// Random access (O(N) scan)
func (b *TextBlob) ValueAt(metricID uint64, index int) (string, bool)
func (b *TextBlob) TimestampAt(metricID uint64, index int) (int64, bool)
func (b *TextBlob) TagAt(metricID uint64, index int) (string, bool)

// Metadata
func (b *TextBlob) Len(metricID uint64) int
func (b *TextBlob) LenByName(metricName string) int
func (b *TextBlob) HasMetricNames() bool
func (b *TextBlob) StartTime() time.Time
```

**Tasks:**
1. Implement all iterator methods
2. Implement name-based lookup (with collision handling)
3. Implement random access (with O(N) scan warning)
4. Implement metadata methods
5. Optimize hot paths (use defer sparingly, inline-friendly code)
6. Comprehensive unit tests

**Estimated Time:** 10-12 hours

---

### **Phase 6: TextBlobSet** 🆕

**Files to Create:**
- `blob/text_blob_set.go` - Multi-blob operations

**Implementation:**
```go
type TextBlobSet struct {
    blobs []TextBlob
}

func NewTextBlobSet(blobs []TextBlob) (*TextBlobSet, error)

// Iterator methods across all blobs
func (s *TextBlobSet) All(metricID uint64) iter.Seq2[int, TextDataPoint]
func (s *TextBlobSet) AllTimestamps(metricID uint64) iter.Seq[int64]
func (s *TextBlobSet) AllValues(metricID uint64) iter.Seq[string]
func (s *TextBlobSet) AllTags(metricID uint64) iter.Seq[string]

// Random access across all blobs
func (s *TextBlobSet) ValueAt(metricID uint64, index int) (string, bool)
func (s *TextBlobSet) TimestampAt(metricID uint64, index int) (int64, bool)
func (s *TextBlobSet) TagAt(metricID uint64, index int) (string, bool)

// Metadata
func (s *TextBlobSet) Len() int
func (s *TextBlobSet) TimeRange() (start, end time.Time)
```

**Tasks:**
1. Implement blob sorting by start time
2. Implement seamless iteration across blobs
3. Implement random access with blob offset calculation
4. Handle sparse data (skip blobs without metric)
5. Unit tests for multi-blob scenarios

**Estimated Time:** 6-8 hours

---

### **Phase 7: Comprehensive Testing** 🧪

**Files to Create:**
- `blob/text_encoder_test.go` - Encoder unit tests
- `blob/text_encoder_collision_test.go` - Collision detection tests
- `blob/text_encoder_mode_test.go` - Mode exclusivity tests
- `blob/text_encoder_tags_test.go` - Optional tags tests
- `blob/text_decoder_test.go` - Decoder unit tests
- `blob/text_blob_test.go` - Blob API tests
- `blob/text_blob_set_test.go` - BlobSet tests
- `blob/text_encoder_bench_test.go` - Benchmarks

**Test Categories:**
1. **Encoder Tests:**
   - ID mode (no collision tracking)
   - Name mode (with collision detection)
   - Mode mixing prevention
   - Tag enable/disable
   - Empty strings
   - **Strings at 256 char limit (boundary test)**
   - **Strings exceeding 256 chars (error test)**
   - Empty tags
   - **Tags at 256 char limit (boundary test)**
   - **Tags exceeding 256 chars (error test)**
   - Delta vs raw timestamps
   - Compression variants (Zstd, S2, LZ4, None)
   - Error paths

2. **Collision Tests:**
   - Detect real collisions
   - Prevent duplicates
   - Metric names payload generation
   - ByName methods work correctly

3. **Decoder Tests:**
   - All encoding variants
   - All compression variants
   - Metric names payload
   - Error handling
   - Partial data

4. **Blob Tests:**
   - Iterator correctness
   - Name-based access
   - Random access
   - Empty metrics
   - Single-point metrics

5. **BlobSet Tests:**
   - Multi-blob iteration
   - Sparse data
   - Time ordering
   - Random access

6. **Benchmarks:**
   - Encode performance
   - Decode performance
   - Iterator performance
   - Memory allocations

**Estimated Time:** 15-20 hours

---

### **Phase 8: Documentation** 📚

**Files to Create:**
- `docs/TEXT_BLOB_USAGE.md` - Usage guide
- `examples/text_blob_demo/main.go` - Demo application

**Documentation Sections:**
1. Quick start
2. Encoder usage (ID mode vs Name mode)
3. Decoder usage
4. Iterator patterns
5. BlobSet usage
6. Collision handling
7. Performance tips
8. Migration from numeric blob

**Estimated Time:** 4-6 hours

---

## 📊 Comparison: Numeric vs Text Blob

| Aspect | Numeric Blob | Text Blob |
|--------|--------------|-----------|
| **Storage Model** | Columnar (separate arrays) | Row-based (inline points) |
| **Magic Number** | 0xEA10 | 0xEB10 |
| **Value Type** | float64 (8 bytes) | string (uint8 length + bytes, max 256 chars) |
| **Value Encoding** | Raw, Gorilla, Delta | N/A (strings are uint8-length-prefixed) |
| **Timestamp Encoding** | Raw, Delta | Raw, Delta (default: Delta) |
| **Index Offsets** | Delta (space efficient) | Absolute (simplicity) |
| **Random Access** | O(1) math | O(N) scan |
| **Iteration** | O(N) linear | O(N) linear |
| **Compression** | Per-column (timestamp/value separate) | Single data section (default: Zstd) |
| **Tag Length Limit** | 256 characters | 256 characters |
| **Value Length Limit** | N/A (fixed float64) | 256 characters |
| **Best For** | Numeric metrics | Text/log metrics |

---

## 🎯 Implementation Priorities

### **P0 - Critical Path** (Must Have)
1. ✅ Update `TextFlag` with new bits
2. ✅ Implement `textDataPointEncoder`
3. ✅ Implement `TextEncoder` (core functionality)
4. ✅ Implement `TextDecoder`
5. ✅ Implement `TextBlob` (iterator API)
6. ✅ Basic unit tests

### **P1 - Important** (Should Have)
1. ✅ Implement `TextBlobSet`
2. ✅ Collision detection tests
3. ✅ Mode exclusivity tests
4. ✅ Optional tags tests
5. ✅ Compression variants

### **P2 - Nice to Have** (Can Defer)
1. 📋 Benchmarks vs other formats
2. 📋 Example applications
3. 📋 Migration guide
4. 📋 Performance profiling

---

## 🚀 Success Criteria

### **Functional Requirements**
- ✅ Encoder supports both ID and Name modes (exclusive)
- ✅ Collision detection works correctly
- ✅ Optional tags can be enabled/disabled
- ✅ Metric names payload generated on collision
- ✅ All compression types work (None, Zstd, S2, LZ4)
- ✅ Iterator API matches numeric blob
- ✅ BlobSet seamlessly iterates across blobs
- ✅ ByName methods work with collision handling

### **Performance Requirements**
- ✅ Encoding: < 100 ns per data point (no compression)
- ✅ Decoding: < 50 ns per data point (iterator)
- ✅ Memory: < 100 bytes overhead per metric
- ✅ Compression: 2-5x space savings with Zstd

### **Quality Requirements**
- ✅ 100% test coverage for core logic
- ✅ Zero linting issues
- ✅ All benchmarks pass
- ✅ Documentation complete
- ✅ Examples compile and run

---

## ✅ Design Decisions - APPROVED

All design decisions have been reviewed and approved. Implementation can proceed with these specifications:

### **Decision 1: Tag Storage Strategy** ✅ APPROVED
- **Chosen:** Per-point tags with optional disable via `WithTagsEnabled()`
- **Applies To:** Both NumericEncoder and TextEncoder
- **Tag Length Limit:** 256 characters (enforced)
- **Performance:** ~30 ns faster per data point when disabled
- **Rationale:** Maximum flexibility, matches numeric blob pattern

### **Decision 2: Default Timestamp Encoding** ✅ APPROVED
- **Chosen:** Delta encoding (default)
- **User Override:** `WithTimestampEncoding(format.TypeRaw)` for raw timestamps
- **Space Savings:** 1-2 bytes (delta) vs 8 bytes (raw)
- **Rationale:** Space-efficient for time series, standard use case
- **Note:** `WithTimestampCompression()` does NOT apply to text blob

### **Decision 3: Default Data Compression** ✅ APPROVED
- **Chosen:** Zstd compression (default)
- **User Override:** `WithDataCompression(comp)` to change compression type
- **Options:** None, Zstd, S2, LZ4
- **Space Savings:** 2-5x with Zstd compression
- **Rationale:** Best compression ratio, typical use case is storage
- **Note:** `WithValueEncoding()` does NOT apply to text blob (numeric only)
- **Note:** `WithValueCompression()` is the correct option for text blob compression

### **Decision 4: Random Access Performance** ✅ APPROVED
- **Chosen:** O(N) scan (accept current implementation)
- **Future Enhancement:** Offset cache deferred to next development cycle
- **Rationale:** Simpler implementation, iteration is primary use case
- **Trade-off:** Can optimize later without breaking API

### **Decision 5: String Length Limits** ✅ APPROVED
- **Chosen:** Hard limit of 256 characters for ALL strings
- **Applies To:**
  - Text blob values (max 256 chars)
  - Text blob tags (max 256 chars)
  - Numeric blob tags (max 256 chars)
- **Length Prefix:** uint8 (1 byte) instead of varint (1-9 bytes)
- **Validation:** Encoder returns error if length exceeds 256
- **Space Savings:** Consistent 1-byte length prefix
- **Rationale:** Reasonable limit for metric data, prevents abuse, simpler encoding

---

## 🎯 Estimated Total Time

| Phase | Time | Priority |
|-------|------|----------|
| Phase 1: Core Structures | 2-3 hours | P0 |
| Phase 2: Data Point Encoding | 4-5 hours | P0 |
| Phase 3: TextEncoder | 12-15 hours | P0 |
| Phase 4: TextDecoder | 10-12 hours | P0 |
| Phase 5: TextBlob | 10-12 hours | P0 |
| Phase 6: TextBlobSet | 6-8 hours | P1 |
| Phase 7: Comprehensive Testing | 15-20 hours | P1 |
| Phase 8: Documentation | 4-6 hours | P2 |
| **Total** | **63-81 hours** | |

**Estimated Calendar Time:** 2-3 weeks (with parallel tasks)

---

## ✅ Review & Sign-Off

### **Design Decisions**
- ✅ Apply all numeric blob improvements
- ✅ Exclusive identifier modes (ID vs Name)
- ✅ Collision detection with auto-handling
- ✅ Optional tags for performance
- ✅ Iterator-based API (Go 1.23+)
- ✅ Header immutability pattern
- ✅ Lazy resource allocation
- ✅ BlobSet for multi-blob queries

### **Ready for Implementation?**
- [ ] Architecture approved
- [ ] API design approved
- [ ] Performance targets agreed
- [ ] Test strategy approved
- [ ] Timeline acceptable

---

**Next Steps:**
1. **Review this plan** - Discuss any concerns or changes
2. **Start Phase 1** - Update flag structure
3. **Iterate** - Build incrementally, test frequently
4. **Review** - Code review after each phase

**END OF IMPLEMENTATION PLAN**
