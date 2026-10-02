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
// A numeric blob is laid out as one contiguous byte slice.
// Sections follow each other back to back, with no padding between them:
//
//	┌─────────────────────────────────────────────────────────┐
//	│ Header (32 bytes, fixed)                                │
//	├─────────────────────────────────────────────────────────┤
//	│ Metric Names Payload (optional, Options bit 2)          │
//	│  - u16 count, then u16 length + bytes per name          │
//	├─────────────────────────────────────────────────────────┤
//	│ Index (N × 16 bytes, or N × 32 bytes for 0xEA30)        │
//	├─────────────────────────────────────────────────────────┤
//	│ Shared Timestamp Table (optional, Options bit 3, V2)    │
//	├─────────────────────────────────────────────────────────┤
//	│ Timestamp Payload (encoded, then compressed as a unit)  │
//	├─────────────────────────────────────────────────────────┤
//	│ Value Payload (encoded, then compressed as a unit)      │
//	├─────────────────────────────────────────────────────────┤
//	│ Tag Payload (optional, Options bit 0, always zstd)      │
//	└─────────────────────────────────────────────────────────┘
//
// The index starts right after the names payload (byte 32 when there is none).
// Decoders derive that position from the names payload length;
// they do not read the header's IndexOffset field.
//
// # Header Format
//
// NumericHeader (32 bytes):
//
//	Bytes  | Field                    | Type   | Description
//	-------|--------------------------|--------|----------------------------------
//	0-3    | Flag                     | 4 × u8 | Options (u16), encoding, compression
//	4-11   | StartTime                | int64  | Unix timestamp in microseconds
//	12-15  | MetricCount              | uint32 | Number of metrics in blob
//	16-19  | IndexOffset              | uint32 | Byte offset to index section
//	20-23  | TimestampPayloadOffset   | uint32 | Byte offset to timestamp data
//	24-27  | ValuePayloadOffset       | uint32 | Byte offset to value data
//	28-31  | TagPayloadOffset         | uint32 | Byte offset to tag data
//
// TextHeader (32 bytes) shares the Flag, StartTime, MetricCount and IndexOffset
// positions, followed by DataOffset (20-23), the uncompressed DataSize (24-27)
// and 4 reserved bytes (28-31).
//
// # Flag Format
//
// Flags are packed into 4 bytes (32 bits):
//
//	Bytes 0-1 (Options, 16 bits, always little-endian):
//	  Bit 0: Tag support (0=disabled, 1=enabled)
//	  Bit 1: Endianness of every other field (0=little-endian, 1=big-endian)
//	  Bit 2: Metric names payload (0=not present, 1=present)
//	  Bit 3: Numeric: shared timestamp table present (V2 only); text: reserved, must be 0
//	  Bits 4-15: Magic number:
//	    0xEA10  numeric V1
//	    0xEA20  numeric V2, compact (16-byte) index entries
//	    0xEA30  numeric V2, extended (32-byte) index entries
//	    0xEB10  text V1
//
//	Byte 2 (EncodingType, 8 bits):
//	  Bits 0-3: Timestamp encoding (0x1=Raw, 0x2=Delta, 0x5=DeltaPacked)
//	  Bits 4-7: Value encoding (0x1=Raw, 0x3=Gorilla, 0x4=Chimp, 0x6=ALP)
//
//	Byte 3 (CompressionType, 8 bits):
//	  Bits 0-3: Timestamp compression (0x1=None, 0x2=Zstd, 0x3=S2, 0x4=LZ4)
//	  Bits 4-7: Value compression (0x1=None, 0x2=Zstd, 0x3=S2, 0x4=LZ4)
//
// The tag payload is not covered by byte 3: it is always zstd-compressed.
// Text blobs use byte 2 for the timestamp encoding and byte 3 for the data
// section's compression.
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
// NumericIndexEntry, compact (16 bytes, magics 0xEA10 and 0xEA20):
//
//	Bytes  | Field           | Type   | Description
//	-------|-----------------|--------|----------------------------------
//	0-7    | MetricID        | uint64 | xxHash64 of metric name
//	8-9    | Count           | uint16 | Number of data points (max 65535)
//	10-11  | TimestampOffset | uint16 | Delta offset from previous metric
//	12-13  | ValueOffset     | uint16 | Delta offset from previous metric
//	14-15  | TagOffset       | uint16 | Delta offset from previous metric
//
// NumericIndexEntry, extended (32 bytes, magic 0xEA30):
//
//	Bytes  | Field           | Type   | Description
//	-------|-----------------|--------|----------------------------------
//	0-7    | MetricID        | uint64 | xxHash64 of metric name
//	8-11   | Count           | uint32 | Number of data points
//	12-15  | TimestampOffset | uint32 | Delta offset from previous metric
//	16-19  | ValueOffset     | uint32 | Delta offset from previous metric
//	20-23  | TagOffset       | uint32 | Delta offset from previous metric
//	24-31  | Reserved        | 8 × u8 | Must be zero
//
// V1 entries keep insertion order; V2 entries are sorted by MetricID.
//
// Note: In memory, Count and offset fields are stored as 'int' to avoid type conversions.
// The decoder reconstructs absolute offsets from delta offsets.
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
// Offsets point into the decompressed payload sections.
// The last metric's range ends at the decompressed section's length.
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
//	HeaderSize               = 32               // Fixed header size
//	NumericIndexEntrySize    = 16               // Compact index entry size
//	NumericExtIndexEntrySize = 32               // Extended index entry size
//	TextIndexEntrySize       = 16               // Fixed index entry size
//	IndexOffsetOffset        = 32               // Index starts after header (no names payload)
//	NumericMaxOffset         = math.MaxUint16   // Max compact offset delta (65535)
//
// Magic numbers for format identification:
//
//	MagicNumericV1Opt    = 0xEA10  // Numeric blob format V1
//	MagicNumericV2Opt    = 0xEA20  // Numeric blob format V2, compact index
//	MagicNumericV2ExtOpt = 0xEA30  // Numeric blob format V2, extended index
//	MagicTextV1Opt       = 0xEB10  // Text blob format V1
//
// Encoding and compression nibble values come from the format package
// (format.TypeRaw, format.TypeDelta, format.CompressionZstd, ...).
//
// # Byte Order (Endianness)
//
// The Options field (bytes 0-1) is always little-endian, so a reader can find
// the endianness bit before choosing a byte order.
// Every other multi-byte header, index, names-payload and shared-table field
// uses the byte order selected by bit 1:
//   - Bit 1 = 0: Little-endian (default, native on x86/x64/ARM)
//   - Bit 1 = 1: Big-endian (network byte order)
//
// Codec payloads follow their own rules: raw timestamps and values follow bit 1,
// while varints, the DeltaPacked group payload, the ALP bit-packed codes and the
// Gorilla/Chimp bit streams have a fixed byte order.
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
