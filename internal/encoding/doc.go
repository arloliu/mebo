// Package encoding preserves the internal codec API used by blob.
//
// It is a compatibility facade: blob continues to import this package while
// concrete codecs live in codec-owned packages. The facade contains type
// aliases and forwarding functions only; it does not own encoded layouts,
// codec state, or decode loops.
//
// # Layout
//
// Timestamp codecs live under internal/encoding/timestamp:
//
//   - raw stores fixed-width int64 timestamps.
//   - delta stores delta-of-delta timestamps with varints.
//   - deltapacked stores delta-of-delta timestamps with Group Varint packing.
//   - simple8b and bp128 are retained experimental codecs. They are not
//     registered format types and blob cannot select them.
//
// Value codecs live under internal/encoding/value:
//
//   - raw stores fixed-width float64 values.
//   - gorilla and chimp provide XOR-compressed values.
//   - alp provides adaptive lossless floating-point compression and is a
//     registered value encoding.
//
// Metadata codecs for tags, variable-length strings, and metric names live in
// internal/encoding/metadata. Cross-column sequential iteration lives in
// internal/encoding/fused; it combines concrete timestamp, value, and metadata
// cursors without changing their encoded formats. Shared low-level helpers live
// under internal/encoding/internal. Research-only codec experiments live in
// internal/encoding/research.
//
// # For External Users
//
// This package is internal and must not be imported outside this module. Use
// the public interfaces in github.com/arloliu/mebo/encoding, the blob package,
// or the root mebo package instead.
//
// Custom encoders implement encoding.ColumnarEncoder[T] in their own package;
// they do not need access to these internal implementations.
package encoding
