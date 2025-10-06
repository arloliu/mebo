package encoding

import (
	"encoding/binary"
	"iter"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/pool"
)

// TagEncoder provides variable-length encoding of string tags.
// Each tag is encoded as: [length:uvarint][bytes:UTF-8]
//
// This encoder is optimized for performance with:
// - ByteBuffer pool for efficient memory reuse
// - Direct byte slice operations
// - Efficient uvarint encoding via encoding/binary
type TagEncoder struct {
	buf    *pool.ByteBuffer
	count  int
	engine endian.EndianEngine
}

var _ ColumnarEncoder[string] = (*TagEncoder)(nil)

// NewTagEncoder creates a new tag encoder.
// The engine parameter is kept for interface compatibility but not used
// since tag encoding is endian-neutral (length-prefixed bytes).
//
// Parameters:
//   - engine: Endian engine (currently unused but kept for interface compatibility)
//
// Returns:
//   - *TagEncoder: A new encoder instance ready for tag encoding
func NewTagEncoder(engine endian.EndianEngine) *TagEncoder {
	return &TagEncoder{
		engine: engine,
		buf:    pool.GetBlobBuffer(),
	}
}

// Bytes returns the encoded byte slice.
// The returned slice is valid until the next call to Write, WriteSlice, or Reset.
// The caller should not modify the returned slice.
//
// The Reset() method does not clear the internal buffer, allowing it to be reused for a new sequence of timestamps
// until the end of the encoding process.
//
// Returns:
//   - []byte: Encoded byte slice containing all written tags
func (e *TagEncoder) Bytes() []byte {
	return e.buf.Bytes()
}

// Len returns the number of encoded tags.
//
// The Reset() method does not clear the internal buffer, allowing it to be reused for a new sequence of timestamps
// until the end of the encoding process.
//
// Returns:
//   - int: Number of tags written since last Finish
func (e *TagEncoder) Len() int {
	return e.count
}

// Size returns the size in bytes of encoded tags.
// It represents the number of bytes that were written to the internal buffer.
//
// The Reset() method does not clear the internal buffer, allowing it to be reused for a new sequence of timestamps
// until the end of the encoding process.
//
// Returns:
//   - int: Total bytes written to internal buffer since last Finish
func (e *TagEncoder) Size() int {
	return e.buf.Len()
}

// Reset clears the internal encoder state but keeps the accumulated data in the internal buffer,
// allowing it to be reused for a new sequence of tags until the end of the encoding process.
//
// The Len(), Size() and Bytes() remain unchanged, the caller will retrieve the accumulated data
// information using Len(), Size() and Bytes().
func (e *TagEncoder) Reset() {
	e.count = 0
}

// Finish finalizes the encoding process.
//
// This method clears the internal buffer and resets the encoder state, preparing it for a new encoding session.
// After calling Finish, the encoder behaves as if it was newly created.
//
// The Len(), Size() and Bytes() will return zero values after calling Finish.
// The caller can continue to retrieve the accumulated data information using Len(), Size() and Bytes()
// until Finish() is called.
func (e *TagEncoder) Finish() {
	pool.PutBlobBuffer(e.buf)
	e.buf = pool.GetBlobBuffer()
	e.count = 0
}

// Write encodes a single string tag in variable-length format.
// Format: [length:uvarint][bytes:UTF-8]
//
// This method is optimized for appending a single tag.
// For bulk writes, use WriteSlice for better performance.
//
// Parameters:
//   - tag: The string tag to encode
func (e *TagEncoder) Write(tag string) {
	// Fast path for empty tags: just write a zero-length varint
	if len(tag) == 0 {
		e.buf.MustWrite([]byte{0})
		e.count++
		return
	}

	// Calculate space needed: varint length + string bytes
	// Use fast inline varint length calculation
	tagLen := len(tag)
	varintBytes := varintLen(uint64(tagLen))

	// Grow buffer and get current position
	requiredBytes := varintBytes + tagLen
	oldLen := e.buf.Len()
	e.buf.ExtendOrGrow(requiredBytes)
	buf := e.buf.Bytes()

	// Encode length and tag
	binary.PutUvarint(buf[oldLen:], uint64(tagLen))
	copy(buf[oldLen+varintBytes:], tag)

	e.count++
}

// WriteSlice encodes a slice of string tags in variable-length format.
// Format: [length:uvarint][bytes:UTF-8] for each tag
//
// This method is optimized for bulk writes. For single writes, use Write for better performance.
//
// Parameters:
//   - tags: Slice of string tags to encode
func (e *TagEncoder) WriteSlice(tags []string) {
	if len(tags) == 0 {
		return
	}

	// Pre-calculate total size needed to minimize allocations
	totalSize := 0
	for i := range tags {
		tagLen := len(tags[i])
		// Use fast inline varint length calculation
		totalSize += varintLen(uint64(tagLen)) + tagLen
	}

	// Grow buffer once for all tags
	oldLen := e.buf.Len()
	e.buf.ExtendOrGrow(totalSize)
	buf := e.buf.Bytes()

	// Write all tags
	offset := oldLen
	for i := range tags {
		tag := tags[i]
		tagLen := len(tag)

		n := binary.PutUvarint(buf[offset:], uint64(tagLen))
		offset += n

		if tagLen > 0 {
			copy(buf[offset:], tag)
			offset += tagLen
		}
	}

	e.count += len(tags)
}

type TagDecoder struct {
	engine endian.EndianEngine
}

var _ ColumnarDecoder[string] = TagDecoder{}

// NewTagDecoder creates a new tag decoder.
// The engine parameter is kept for interface compatibility but not used
// since tag encoding is endian-neutral (length-prefixed bytes).
//
// Parameters:
//   - engine: Endian engine (currently unused but kept for interface compatibility)
//
// Returns:
//   - TagDecoder: A new decoder instance (stateless, can be reused)
func NewTagDecoder(engine endian.EndianEngine) TagDecoder {
	return TagDecoder{
		engine: engine,
	}
}

// All returns an iterator that yields all decoded items from the provided encoded data.
//
// Parameters:
//   - data: Encoded byte slice from TagEncoder.Bytes()
//   - count: Expected number of tags to decode
//
// Returns:
//   - iter.Seq[string]: Iterator yielding decoded string tags
func (d TagDecoder) All(data []byte, count int) iter.Seq[string] {
	return func(yield func(string) bool) {
		offset := 0
		for range count {
			tagLen, n, ok := decodeTagAt(data, offset)
			if !ok {
				return
			}

			// Read tag bytes
			offset += n
			tag := string(data[offset : offset+tagLen])
			offset += tagLen

			if !yield(tag) {
				return
			}
		}
	}
}

// At retrieves the tag at the specified index from the encoded data.
// The index is zero-based, so index 0 retrieves the first tag.
//
// If the index is out of bounds, the second return value will be false.
//
// Parameters:
//   - data: Encoded byte slice from TagEncoder.Bytes()
//   - index: Zero-based index of the tag to retrieve
//   - count: Total number of tags in the encoded data
//
// Returns:
//   - string: The tag at the specified index
//   - bool: true if the index exists and was successfully decoded, false otherwise
func (d TagDecoder) At(data []byte, index int, count int) (string, bool) {
	if index < 0 || index >= count {
		return "", false
	}

	offset := 0
	for i := 0; i <= index; i++ {
		tagLen, n, ok := decodeTagAt(data, offset)
		if !ok {
			return "", false
		}

		offset += n

		if i == index {
			tag := string(data[offset : offset+tagLen])
			return tag, true
		}

		offset += tagLen
	}

	return "", false
}

// decodeTagAt decodes tag metadata at the given offset.
// Returns the tag length in bytes, the varint size, and whether the operation succeeded.
// This helper eliminates code duplication between All() and At() methods.
func decodeTagAt(data []byte, offset int) (tagLen int, varintSize int, ok bool) {
	if offset >= len(data) {
		return 0, 0, false
	}

	// Read varint length
	tagLenU64, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return 0, 0, false
	}

	// Check for integer overflow before conversion and bounds check
	if tagLenU64 > uint64(^uint(0)>>1) || offset+n+int(tagLenU64) > len(data) {
		return 0, 0, false
	}

	return int(tagLenU64), n, true
}

// varintLen returns the number of bytes required to encode a uvarint.
// This is a fast inline calculation without allocating a temporary buffer.
// Benchmarked to be significantly faster than binary.PutUvarint(make([]byte, 10), n).
func varintLen(n uint64) int {
	if n < 1<<7 {
		return 1
	}
	if n < 1<<14 {
		return 2
	}
	if n < 1<<21 {
		return 3
	}
	if n < 1<<28 {
		return 4
	}
	if n < 1<<35 {
		return 5
	}
	if n < 1<<42 {
		return 6
	}
	if n < 1<<49 {
		return 7
	}
	if n < 1<<56 {
		return 8
	}
	if n < 1<<63 {
		return 9
	}

	return 10
}
