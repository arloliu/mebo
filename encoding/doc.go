// Package encoding defines the generic columnar encoder and decoder interfaces used by mebo.
//
// This package exports only two interfaces: ColumnarEncoder and ColumnarDecoder.
// Mebo's concrete codecs implement them,
// but those implementations live under internal/ and cannot be imported by other modules.
// There is no way to plug a custom codec into the blob encoders:
// the blob package builds its codecs from the encoding types the caller configures.
//
// Most users should not need this package.
// Use the blob package (or the top-level mebo package) to encode and decode data,
// and select codecs with options such as blob.WithTimestampEncoding and blob.WithValueEncoding.
// Nothing selects an encoding automatically;
// a blob always uses exactly the encodings it was configured with.
//
// # Interfaces
//
// The interfaces, abridged (see their declarations for the full contracts):
//
//	type ColumnarEncoder[T comparable] interface {
//	    Write(data T)           // Encode a single value
//	    WriteSlice(values []T)  // Encode multiple values (more efficient)
//	    Bytes() []byte          // Get encoded data
//	    Len() int               // Number of values encoded
//	    Size() int              // Size in bytes
//	    Reset()                 // Clear state but keep buffer
//	    Finish()                // Finalize and release resources
//	}
//
//	type ColumnarDecoder[T comparable] interface {
//	    All(data []byte, count int) iter.Seq[T]       // Sequential iteration
//	    At(data []byte, index int, count int) (T, bool) // Random access
//	}
//
// # Built-in Encodings
//
// Mebo stores timestamps, values, and tags in separate columns,
// each with a codec suited to its data.
// The format package defines the encoding type constants.
//
// Timestamps (int64), numeric blobs:
//   - Raw (format.TypeRaw): 8 bytes per timestamp, O(1) random access.
//   - Delta (format.TypeDelta): delta-of-delta values stored as zigzag varints.
//     Regular intervals take about 1 byte per timestamp; the worst case is 10 bytes.
//     Random access is O(index).
//   - DeltaPacked (format.TypeDeltaPacked): delta-of-delta values packed with Group Varint,
//     one control byte per group of 4 values.
//     Regular intervals take about 1.25 bytes per timestamp;
//     the worst case is 33 bytes per group of 4 (about 8.25 bytes per timestamp).
//     Its advantage over Delta is decode throughput, not size.
//     Random access is O(index).
//
// Timestamps, text blobs:
//   - Raw or Delta only.
//     Text Delta stores the plain delta from the previous timestamp
//     (the first point uses the blob start time) as a zigzag varint.
//
// Numeric values (float64):
//   - Raw (format.TypeRaw): 8 bytes per value, O(1) random access.
//   - Gorilla (format.TypeGorilla): XOR-based compression (VLDB 2015).
//     Random access is O(index).
//   - Chimp (format.TypeChimp): improved XOR-based compression (VLDB 2022).
//     Random access is O(index).
//   - ALP (format.TypeALP): Adaptive Lossless floating-Point for decimal-quantized data (SIGMOD 2024).
//     Random access is O(1) plus O(log k) over the column's k exceptions.
//   - ALP-RLE (format.TypeALPRLE): ALP with a run-length front end for columns where many points repeat the previous value.
//     Each uncompressed column is never larger than under ALP;
//     with value compression the compressed payload is not guaranteed to shrink.
//     Random access adds a popcount over the column's run-start bitmap.
//
// Text values and tags:
//   - Text blob values and tags are length-prefixed with one byte each, so each is at most 255 bytes.
//   - Numeric blob tags are length-prefixed with a uvarint
//     and stored in a separate payload that is always Zstd-compressed;
//     the payload is omitted when every tag is empty.
//
// # Thread Safety
//
// Encoders are not thread-safe; use one encoder per goroutine.
//
// Blob-level decoders (blob.NumericDecoder, blob.TextDecoder) are not thread-safe and not reusable.
// Create a new decoder for each decode operation.
// The decoded blobs are immutable and safe for concurrent reads.
package encoding
