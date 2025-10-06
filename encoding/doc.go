// Package encoding provides low-level encoding and decoding algorithms for mebo time-series data.
//
// This package implements the columnar encoding strategies that power mebo's space-efficient
// binary format. It provides specialized encoders and decoders for timestamps, numeric values,
// text values, and tags.
//
// # Overview
//
// Mebo uses columnar storage where timestamps, values, and tags are encoded separately using
// algorithms optimized for their specific characteristics:
//
// Timestamps - Regular intervals, highly compressible:
//   - Raw encoding: No compression, 8 bytes per timestamp
//   - Delta encoding: Delta-of-delta with zigzag+varint, 1-5 bytes per timestamp
//
// Numeric Values - Slowly-changing floats, high redundancy:
//   - Raw encoding: No compression, 8 bytes per value
//   - Gorilla encoding: XOR-based compression, 1-8 bytes per value
//
// Text Values - Variable-length strings:
//   - Length-prefixed encoding with varint lengths
//
// Tags - Optional metadata strings:
//   - Length-prefixed encoding with optional compression
//   - Stored in a separate payload section
//   - Compressed by default using Zstd.
//
// # Architecture
//
// The package is organized around the ColumnarEncoder and ColumnarDecoder interfaces:
//
//	type ColumnarEncoder[T comparable] interface {
//	    Write(data T)           // Encode single value
//	    WriteSlice(data []T)    // Encode multiple values (more efficient)
//	    Bytes() []byte          // Get encoded data
//	    Len() int               // Number of values encoded
//	    Size() int              // Size in bytes
//	    Reset()                 // Clear state but keep buffer
//	    Finish()                // Finalize and release resources
//	}
//
//	type ColumnarDecoder[T comparable] interface {
//	    All(data []byte, count int) iter.Seq[T]  // Sequential iteration
//	    At(data []byte, count, index int) (T, bool)  // Random access (if supported)
//	}
//
// # Timestamp Encoding
//
// TimestampRawEncoder/Decoder - Uncompressed timestamps:
//
//	encoder := encoding.NewTimestampRawEncoder()
//	encoder.Write(1700000000000000)  // Unix microseconds
//	encoder.Write(1700000001000000)
//	data := encoder.Bytes()  // 16 bytes (2 × 8 bytes)
//
// Use when:
//   - Random access is required
//   - Timestamps are irregular with large variations
//   - Compression adds no benefit
//
// TimestampDeltaEncoder/Decoder - Delta-of-delta compression:
//
//	encoder := encoding.NewTimestampDeltaEncoder()
//	encoder.Write(1700000000000000)  // First: full value (5-9 bytes)
//	encoder.Write(1700000001000000)  // Second: delta (1-5 bytes)
//	encoder.Write(1700000002000000)  // Third: delta-of-delta (1 byte if regular)
//	data := encoder.Bytes()  // ~10 bytes for 3 timestamps
//
// Compression characteristics:
//   - Regular intervals (1s, 1min): ~1 byte per timestamp (87% savings)
//   - Semi-regular (±5% jitter): ~1-2 bytes per timestamp (75-87% savings)
//   - Irregular: 3-5 bytes per timestamp (38-63% savings)
//
// Use when:
//   - Timestamps have regular or semi-regular intervals
//   - Storage space is critical
//   - Sequential access is the primary pattern
//
// # Numeric Value Encoding
//
// NumericRawEncoder/Decoder - Uncompressed float64 values:
//
//	encoder := encoding.NewNumericRawEncoder()
//	encoder.Write(42.5)
//	encoder.Write(43.7)
//	data := encoder.Bytes()  // 16 bytes (2 × 8 bytes)
//
// Use when:
//   - Values change dramatically between points
//   - Random access is required
//   - Compression provides no benefit
//
// NumericGorillaEncoder/Decoder - Facebook's Gorilla compression:
//
//	encoder := encoding.NewNumericGorillaEncoder()
//	encoder.Write(42.5)      // First: full value (64 bits)
//	encoder.Write(42.5)      // Unchanged: 1 bit
//	encoder.Write(42.501)    // Similar: 2-20 bits typical
//	data := encoder.Bytes()  // ~10 bytes for 3 values
//
// Compression characteristics:
//   - Unchanged values: 1 bit (99.98% savings)
//   - Slowly changing: 12-20 bits per value (70-85% savings)
//   - Rapidly changing: 30-64 bits per value (6-50% savings)
//
// Algorithm:
//  1. XOR current value with previous value
//  2. If XOR = 0: store 1 control bit (0)
//  3. If XOR ≠ 0:
//     - Store control bit (1)
//     - Count leading and trailing zeros in XOR
//     - If same block as previous: store 1 bit (0) + meaningful bits
//     - If different block: store 1 bit (1) + 5 bits (leading) + 6 bits (length) + meaningful bits
//
// Use when:
//   - Values change slowly (typical metrics: CPU, memory, temperature)
//   - Consecutive values are similar
//   - Storage efficiency is important
//
// # Text Value Encoding
//
// Text values are stored as length-prefixed strings with varint lengths:
//
//	encoder := encoding.NewVarStringEncoder()
//	encoder.Write("OK")      // 1 byte (length) + 2 bytes (data)
//	encoder.Write("FAILED")  // 1 byte (length) + 6 bytes (data)
//	data := encoder.Bytes()  // 10 bytes total
//
// The varint length encoding uses 1-5 bytes:
//   - 0-127: 1 byte
//   - 128-16383: 2 bytes
//   - 16384-2097151: 3 bytes
//   - And so on...
//
// Most strings are under 128 characters, so length overhead is minimal.
//
// # Tag Encoding
//
// Tags use the same length-prefixed encoding as text values but are stored
// in a separate payload section. Tags can be:
//   - Empty strings (encoded as length 0)
//   - Short identifiers (1 byte length + data)
//   - Full key=value pairs (1-2 byte length + data)
//
// Example:
//
//	encoder := encoding.NewTagEncoder(endian.GetLittleEndianEngine())
//	encoder.Write("host=server1")  // Common pattern
//	encoder.Write("")              // Empty tag
//	encoder.Write("env=prod")
//	data := encoder.Bytes()
//
// # Performance Characteristics
//
// Encoding Performance (operations per second):
//   - TimestampRaw: ~50M ops/sec (~20 ns/op)
//   - TimestampDelta: ~25M ops/sec (~40 ns/op)
//   - NumericRaw: ~50M ops/sec (~20 ns/op)
//   - NumericGorilla: ~25M ops/sec (~40 ns/op)
//   - VarString: ~20M ops/sec (~50 ns/op, depends on length)
//
// Decoding Performance (sequential):
//   - TimestampRaw: ~100M ops/sec (~10 ns/op)
//   - TimestampDelta: ~40M ops/sec (~25 ns/op)
//   - NumericRaw: ~100M ops/sec (~10 ns/op)
//   - NumericGorilla: ~50M ops/sec (~20 ns/op)
//   - VarString: ~30M ops/sec (~33 ns/op)
//
// Random Access Performance:
//   - Raw encodings: O(1), ~10 ns per access
//   - Delta encodings: O(n), must decode from start
//   - Gorilla encoding: O(n), must decode from start
//
// # Memory Usage
//
// Encoders use internal buffer pools to minimize allocations:
//   - Buffer pool provides 4KB-64KB buffers
//   - Buffers are reused across encoder instances
//   - Automatic growth for large payloads
//
// Decoders have minimal memory overhead:
//   - No allocations for sequential iteration (uses iter.Seq)
//   - Small temporary buffers for random access
//
// # Thread Safety
//
// Encoders: Not thread-safe. Use one encoder per goroutine.
//
// Decoders: Thread-safe for concurrent reads from different goroutines.
//
// Buffer Pool: Thread-safe with internal synchronization.
//
// # Choosing Encodings
//
// For Timestamps:
//   - Regular intervals (monitoring, metrics): Delta encoding (87% savings)
//   - Irregular events: Raw encoding (no compression overhead)
//   - Need random access: Raw encoding
//
// For Numeric Values:
//   - Slowly changing (CPU, memory, temperature): Gorilla (70-85% savings)
//   - Rapidly changing (network packets, counters): Raw encoding
//   - Need random access: Raw encoding
//
// For Text Values:
//   - Always use varint length-prefixed encoding
//   - Add compression at the blob level if strings are repetitive
//
// For Tags:
//   - Enable only when needed (adds 8-16 bytes per point)
//   - Use short tag values to minimize overhead
//   - The tag payload are compressed by default using Zstd.
//
// # Advanced Features
//
// Bit-Level Encoding:
//
// NumericGorillaEncoder uses bit-level operations for maximum
// compression. It maintains a 64-bit buffer and flushes complete bytes to the output:
//
//	bitBuf: [████████ ████████ ████████ ████░░░░] (28 bits filled)
//	         ↓ flush when ≥8 bits available
//	output:  [████████] [████████] [████████]
//
// Varint Encoding:
//
// Timestamps and string lengths use Protocol Buffers-style varint
// encoding where the MSB indicates continuation:
//
//	Value 0-127:     0xxxxxxx                    (1 byte)
//	Value 128-16383: 1xxxxxxx 0xxxxxxx           (2 bytes)
//	Value 16384+:    1xxxxxxx 1xxxxxxx 0xxxxxxx  (3+ bytes)
//
// Zigzag Encoding:
//
// Signed delta values use zigzag encoding to efficiently represent
// both positive and negative values:
//
//	Positive: 0 → 0, 1 → 2, 2 → 4, 3 → 6
//	Negative: -1 → 1, -2 → 3, -3 → 5
//
// # Examples
//
// See the encoding package tests for detailed usage examples:
//   - numeric_gorilla_test.go: Gorilla compression examples
//   - ts_delta_test.go: Delta-of-delta encoding examples
//   - varstring_test.go: String encoding examples
//
// For high-level usage, see the blob package which uses these encoders internally.
package encoding
