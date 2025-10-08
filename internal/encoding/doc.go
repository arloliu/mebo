// Package encoding provides internal implementations of columnar encoding strategies.package encoding

// This package contains the concrete implementations of timestamp, numeric, and text
// encoders/decoders used by the mebo time-series format. These implementations are
// internal to mebo and are not part of the public API.
//
// # For External Users
//
// This package is internal and should not be imported by external code. Use the
// public interfaces defined in github.com/arloliu/mebo/encoding instead:
//
//   - encoding.ColumnarEncoder[T] - Generic encoding interface
//   - encoding.ColumnarDecoder[T] - Generic decoding interface
//
// For most use cases, use the high-level blob package or the convenience functions
// in the root mebo package.
//
// # For Custom Encoders
//
// To implement custom encoders, implement the encoding.ColumnarEncoder[T] interface
// in your own package. You don't need access to these internal implementations.
//
// Example:
//
//	package myencoder
//
//	import "github.com/arloliu/mebo/encoding"
//
//	type MyEncoder struct {
//	    // Your implementation
//	}
//
//	// Implement encoding.ColumnarEncoder[T] interface
//	func (e *MyEncoder) Write(data T) { /* ... */ }
//	func (e *MyEncoder) WriteSlice(data []T) { /* ... */ }
//	func (e *MyEncoder) Bytes() []byte { /* ... */ }
//	func (e *MyEncoder) Len() int { /* ... */ }
//	func (e *MyEncoder) Size() int { /* ... */ }
//	func (e *MyEncoder) Reset() { /* ... */ }
//	func (e *MyEncoder) Finish() { /* ... */ }
//
// # Implementation Overview
//
// This package provides the following encoding implementations:
//
// Timestamp Encoders/Decoders:
//   - TimestampRawEncoder/Decoder - Uncompressed 64-bit timestamps
//   - TimestampDeltaEncoder/Decoder - Delta-of-delta compression
//
// Numeric Value Encoders/Decoders:
//   - NumericRawEncoder/Decoder - Uncompressed 64-bit floats
//   - NumericGorillaEncoder/Decoder - Facebook's Gorilla compression
//
// Text/Tag Encoders/Decoders:
//   - VarStringEncoder/Decoder - Variable-length string encoding
//   - TagEncoder/Decoder - Tag metadata encoding
//   - MetricNames encoding/decoding - Hash-based metric name storage
//
// # Architecture Notes
//
// All encoders implement the encoding.ColumnarEncoder[T] interface, which provides:
//   - Generic type parameter T for type safety
//   - Consistent API across all encoding strategies
//   - Zero-allocation iteration using iter.Seq
//   - Support for both sequential and random access (where applicable)
//
// For detailed documentation of each encoding strategy, see the individual files.
package encoding
