// Package section defines the low-level binary structures and constants for mebo blob format.
//
// This package provides the foundational types and constants that define the physical layout
// of mebo blobs. It handles binary serialization/deserialization of headers, flags, and index
// entries, ensuring consistent byte-level representation across platforms.
//
// # Overview
//
// The section package defines three main categories of types:
//
//  1. Headers: Fixed-size blob metadata (NumericHeader, TextHeader)
//  2. Flags: Packed bitfields for encoding/compression configuration (NumericFlag, TextFlag)
//  3. Index Entries: Fixed-size metric descriptors (NumericIndexEntry, TextIndexEntry)
//
// These types form the structural foundation of mebo's binary format, providing:
//   - Fixed-size layouts for O(1) random access
//   - Efficient binary serialization with minimal overhead
//   - Platform-independent byte representation
//   - Bitfield packing for compact storage
//
// # Blob Structure
//
// A mebo blob consists of fixed-size sections followed by variable-size payloads:
//
//	┌─────────────────────────────────────────────────────────┐
//	│ Header (32 bytes, fixed)                                │
//	│  - Flag (4 bytes): encoding/compression/options         │
//	│  - MetricCount (4 bytes)                                │
//	│  - StartTime (8 bytes)                                  │
//	│  - Offsets (12 bytes): index, timestamp, value, tag     │
//	├─────────────────────────────────────────────────────────┤
//	│ Metric Names Payload (variable, optional)              │
//	│  - Only present when collision detected                 │
//	│  - Length-prefixed strings                              │
//	├─────────────────────────────────────────────────────────┤
//	│ Index (N × 16 bytes, fixed per entry)                  │
//	│  - One entry per metric                                 │
//	│  - MetricID, offsets, count                             │
//	├─────────────────────────────────────────────────────────┤
//	│ Padding (0-7 bytes, for 8-byte alignment)              │
//	├─────────────────────────────────────────────────────────┤
//	│ Timestamp Payload (variable)                            │
//	│  - Encoded + compressed timestamps                      │
//	├─────────────────────────────────────────────────────────┤
//	│ Padding (0-7 bytes, for 8-byte alignment)              │
//	├─────────────────────────────────────────────────────────┤
//	│ Value Payload (variable)                                │
//	│  - Encoded + compressed values                          │
//	├─────────────────────────────────────────────────────────┤
//	│ Tag Payload (variable, optional)                        │
//	│  - Only present if tags enabled                         │
//	│  - Encoded + compressed tags                            │
//	└─────────────────────────────────────────────────────────┘
//
// # Header Format
//
// NumericHeader (32 bytes):
//
//	Bytes  | Field                    | Type   | Description
//	-------|--------------------------|--------|----------------------------------
//	0-3    | Flag                     | uint32 | Encoding, compression, options
//	4-7    | MetricCount              | uint32 | Number of metrics in blob
//	8-15   | StartTime                | int64  | Unix timestamp in microseconds
//	16-19  | IndexOffset              | uint32 | Byte offset to index section
//	20-23  | TimestampPayloadOffset   | uint32 | Byte offset to timestamp data
//	24-27  | ValuePayloadOffset       | uint32 | Byte offset to value data
//	28-31  | TagPayloadOffset         | uint32 | Byte offset to tag data
//
// TextHeader (32 bytes):
//
//	Same layout as NumericHeader but with text-specific magic number.
//
// # Flag Format
//
// Flags are packed into 4 bytes (32 bits):
//
//	Byte 0-1 (Options, 16 bits):
//	  Bit 0: Tag support (0=disabled, 1=enabled)
//	  Bit 1: Endianness (0=little-endian, 1=big-endian)
//	  Bit 2: Metric names payload (0=not present, 1=present)
//	  Bit 3: Reserved (must be 0)
//	  Bits 4-15: Magic number (0xEA10 for numeric, 0xEB10 for text)
//
//	Byte 2 (EncodingType, 8 bits):
//	  Bits 0-3: Timestamp encoding (0x1=Raw, 0x2=Delta)
//	  Bits 4-7: Value encoding (0x1=Raw, 0x2=Gorilla for numeric)
//
//	Byte 3 (CompressionType, 8 bits):
//	  Bits 0-3: Timestamp compression (0x1=None, 0x2=Zstd, 0x3=S2, 0x4=LZ4)
//	  Bits 4-7: Value compression (0x1=None, 0x2=Zstd, 0x3=S2, 0x4=LZ4)
//
// Example flag decoding:
//
//	flag := section.NewNumericFlag()
//	flag.SetTimestampEncoding(format.TypeDelta)
//	flag.SetValueEncoding(format.TypeGorilla)
//	flag.SetTimestampCompression(format.CompressionZstd)
//	flag.SetTagsEnabled(true)
//
//	// Check flags
//	if flag.HasTags() {
//	    // Handle tags
//	}
//	tsEnc := flag.GetTimestampEncoding()  // format.TypeDelta
//
// # Index Entry Format
//
// NumericIndexEntry (16 bytes):
//
//	Bytes  | Field           | Type   | Description
//	-------|-----------------|--------|----------------------------------
//	0-7    | MetricID        | uint64 | xxHash64 of metric name
//	8-9    | Count           | uint16 | Number of data points (max 65535)
//	10-11  | TimestampOffset | uint16 | Delta offset from previous metric
//	12-13  | ValueOffset     | uint16 | Delta offset from previous metric
//	14-15  | TagOffset       | uint16 | Delta offset from previous metric
//
// Note: In memory, Count and offset fields are stored as 'int' to avoid type conversions.
// On disk, they're stored as 'uint16' to save space. The decoder reconstructs absolute
// offsets from delta offsets.
//
// TextIndexEntry (16 bytes):
//
//	Same layout as NumericIndexEntry but with Offset/Size instead of separate offsets:
//
//	Bytes  | Field      | Type   | Description
//	-------|------------|--------|----------------------------------
//	0-7    | MetricID   | uint64 | xxHash64 of metric name
//	8-9    | Count      | uint16 | Number of data points
//	10-11  | Reserved1  | uint16 | Reserved for future use
//	12-15  | Offset     | uint32 | Absolute byte offset in data section
//
// # Delta Offset Encoding
//
// Numeric blobs use delta offsets to save space. Instead of storing absolute offsets
// (which can exceed 65535), we store the difference between consecutive offsets:
//
//	Metric 1: TimestampOffset = 0        (absolute: 0)
//	Metric 2: TimestampOffset = 100      (absolute: 0 + 100 = 100)
//	Metric 3: TimestampOffset = 50       (absolute: 100 + 50 = 150)
//
// Benefits:
//   - Most deltas fit in uint16 (0-65535)
//   - Supports blobs with >65KB payloads
//   - Minimal space overhead (2 bytes per offset)
//
// Decoder reconstruction:
//
//	absoluteOffset[0] = deltaOffset[0]  // First is absolute
//	for i := 1; i < metricCount; i++ {
//	    absoluteOffset[i] = absoluteOffset[i-1] + deltaOffset[i]
//	}
//
// # Constants
//
// The package defines important constants:
//
//	HeaderSize            = 32              // Fixed header size
//	NumericIndexEntrySize = 16              // Fixed index entry size
//	TextIndexEntrySize    = 16              // Fixed index entry size
//	IndexOffsetOffset     = 32              // Index starts after header
//	NumericMaxOffset      = math.MaxUint16  // Max offset value (65535)
//
// Magic numbers for format identification:
//
//	MagicFloatV1Opt = 0xEA10  // Numeric blob format v1
//	MagicTextV1Opt  = 0xEB10  // Text blob format v1
//
// Encoding type constants:
//
//	TimestampEncodingNRaw = 0x01  // Raw timestamp encoding
//	TimestampTypeDelta    = 0x02  // Delta timestamp encoding
//	ValueTypeRaw          = 0x10  // Raw value encoding
//	ValueTypeGorilla      = 0x20  // Gorilla value encoding
//
// Compression type constants:
//
//	TimestampCompressionNone = 0x01  // No timestamp compression
//	TimestampCompressionZstd = 0x02  // Zstd timestamp compression
//	TimestampCompressionS2   = 0x03  // S2 timestamp compression
//	TimestampCompressionLZ4  = 0x04  // LZ4 timestamp compression
//	(Similar for value compression with <<4 shift)
//
// # Byte Order (Endianness)
//
// All multi-byte numeric values use the byte order specified in the flag:
//   - Bit 1 = 0: Little-endian (default, native on x86/x64/ARM)
//   - Bit 1 = 1: Big-endian (network byte order)
//
// The endian package provides engine implementations for each:
//
//	if flag.IsBigEndian() {
//	    engine = endian.GetBigEndianEngine()
//	} else {
//	    engine = endian.GetLittleEndianEngine()
//	}
//
// For maximum performance, use little-endian on x86/x64/ARM systems to avoid
// byte-swapping overhead.
//
// # Alignment
//
// Payload sections are aligned to 8-byte boundaries for optimal CPU cache performance:
//   - Header: Always 32 bytes (8-byte aligned)
//   - Index: Padded to next 8-byte boundary after last entry
//   - Timestamp payload: Padded to 8-byte boundary
//   - Value payload: Padded to 8-byte boundary
//   - Tag payload: No padding required (last section)
//
// Padding bytes are filled with zeros.
//
// # Thread Safety
//
// All types in this package are immutable value types and are safe for concurrent use.
// Flag manipulation methods create new instances rather than modifying in place.
//
// # Performance Considerations
//
// Fixed-Size Advantage:
//
// All header and index structures use fixed sizes, enabling:
//   - O(1) index lookups via offset calculation
//   - Single-pass encoding without backtracking
//   - Memory-mapped file support
//   - Zero-copy deserialization
//
// Cache Efficiency:
//
// 16-byte index entries fit perfectly in cache lines (64 bytes = 4 entries).
//
// Binary Layout:
//
// Structs use explicit field ordering to avoid padding and ensure consistent
// cross-platform representation.
//
// # Usage Examples
//
// Creating a header:
//
//	header := section.NewNumericHeader(time.Now())
//	header.MetricCount = 100
//	header.TimestampPayloadOffset = 1632
//	header.Flag.SetTimestampEncoding(format.TypeDelta)
//
// Serializing to bytes:
//
//	buf := make([]byte, section.HeaderSize)
//	err := header.WriteToSlice(buf, endian.GetLittleEndianEngine())
//
// Parsing from bytes:
//
//	header := &section.NumericHeader{}
//	err := header.Parse(data)
//
// Working with flags:
//
//	flag := section.NewNumericFlag()
//	flag.SetTagsEnabled(true)
//	flag.SetTimestampCompression(format.CompressionZstd)
//	if flag.HasTags() {
//	    // Handle tags
//	}
//
// **Creating index entries**:
//
//	entry := section.NumericIndexEntry{
//	    MetricID:        12345,
//	    Count:           100,
//	    TimestampOffset: 0,    // First metric: absolute offset
//	    ValueOffset:     800,  // First metric: absolute offset
//	    TagOffset:       0,    // No tags
//	}
//
// # Integration with Other Packages
//
// The section package is used by:
//   - **blob**: High-level encoder/decoder implementation
//   - **encoding**: Low-level encoding algorithms
//   - **endian**: Byte order handling
//
// Most users should interact with the blob package instead of using section directly.
// Use this package only when you need fine-grained control over binary format details
// or are implementing custom blob formats.
package section
