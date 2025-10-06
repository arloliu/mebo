package encoding

import (
	"fmt"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/pool"
)

// MaxTextLength is the maximum length for text strings (values and tags).
// This limit ensures compatibility with uint8 length prefix encoding.
// Since uint8 can represent 0-255, the maximum string length is 255 bytes.
const MaxTextLength = 255

// VarStringEncoder encodes variable-length strings with uint8 length prefix.
//
// Each string is encoded as:
//   - 1 byte: length (0-255)
//   - N bytes: string data (UTF-8)
//
// The encoder enforces a hard limit of 255 characters per string.
// Strings exceeding this limit will trigger an error.
//
// Additionally provides WriteVarint for encoding signed integers as varints,
// which is useful for delta timestamp encoding.
//
// Note: The VarStringEncoder is NOT a ColumnarEncoder.
type VarStringEncoder struct {
	buf    *pool.ByteBuffer
	engine endian.EndianEngine
	count  int
}

// NewVarStringEncoder creates a new variable-length string encoder using the specified endian engine.
//
// The encoder uses a pooled byte buffer with amortized growth strategy for
// optimal performance when encoding multiple strings.
//
// Parameters:
//   - engine: Endian engine for byte order (typically little-endian)
//
// Returns:
//   - *VarStringEncoder: A new encoder instance ready for variable-length string encoding
func NewVarStringEncoder(engine endian.EndianEngine) *VarStringEncoder {
	return &VarStringEncoder{
		engine: engine,
		buf:    pool.GetBlobBuffer(),
	}
}

// Write encodes a single text string with uint8 length prefix.
//
// The string is validated to ensure it doesn't exceed MaxTextLength (255 characters).
// Returns an error if the string is too long.
//
// Encoding format:
//   - 1 byte: length as uint8 (0-255)
//   - N bytes: UTF-8 string data
//
// Buffer growth strategy:
//   - Pre-grows buffer to accommodate length byte + string data
//   - Minimizes reallocations during encoding
//
// Parameters:
//   - text: String to encode (must not exceed 255 characters)
//
// Returns:
//   - error: nil if successful, error if string exceeds MaxTextLength
func (e *VarStringEncoder) Write(text string) error {
	if len(text) > MaxTextLength {
		return fmt.Errorf("text length %d exceeds maximum %d", len(text), MaxTextLength)
	}

	e.count++

	// Pre-grow buffer for length byte + string data
	e.buf.Grow(1 + len(text))

	// Write length as uint8
	length := uint8(len(text)) //nolint:gosec
	e.buf.MustWrite([]byte{length})

	// Write string data
	e.buf.MustWrite([]byte(text))

	return nil
}

// WriteSlice encodes a slice of text strings with buffer pre-allocation.
//
// All strings are validated to ensure none exceed MaxTextLength (255 characters).
// Returns an error if any string is too long.
//
// Buffer growth strategy:
//   - Pre-allocates total space needed for all strings
//   - Single buffer growth operation for the entire slice
//   - Minimizes memory allocations and copying
//
// Parameters:
//   - texts: Slice of strings to encode (each must not exceed 255 characters)
//
// Returns:
//   - error: nil if successful, error if any string exceeds MaxTextLength
func (e *VarStringEncoder) WriteSlice(texts []string) error {
	// Validate all strings first
	totalSize := 0
	for _, text := range texts {
		if len(text) > MaxTextLength {
			return fmt.Errorf("text length %d exceeds maximum %d", len(text), MaxTextLength)
		}
		totalSize += 1 + len(text) // length byte + string data
	}

	// Pre-allocate buffer space
	e.buf.Grow(totalSize)

	// Encode all strings
	for _, text := range texts {
		length := uint8(len(text)) //nolint:gosec
		e.buf.MustWrite([]byte{length})
		e.buf.MustWrite([]byte(text))
		e.count++
	}

	return nil
}

// WriteVarint encodes an int64 value as a variable-length integer.
//
// This method is used for encoding timestamps in delta format.
// It uses zigzag encoding for signed integers to improve compression
// of negative values.
//
// Parameters:
//   - val: The int64 value to encode as varint
func (e *VarStringEncoder) WriteVarint(val int64) {
	// Zigzag encoding: converts signed to unsigned
	// -1 becomes 1, -2 becomes 3, 0 stays 0, 1 becomes 2, etc.
	uval := uint64(val<<1) ^ uint64(val>>63) //nolint:gosec

	// Encode as varint
	for uval >= 0x80 {
		e.buf.MustWrite([]byte{byte(uval) | 0x80})
		uval >>= 7
	}
	e.buf.MustWrite([]byte{byte(uval)})
}

// Bytes returns the encoded data as a byte slice.
//
// The returned slice shares the underlying buffer with the encoder.
// Do not modify the returned slice.
//
// Returns:
//   - []byte: Encoded byte slice containing all written strings
func (e *VarStringEncoder) Bytes() []byte {
	return e.buf.Bytes()
}

// Len returns the number of strings encoded.
//
// Returns:
//   - int: Number of strings written since last Reset
func (e *VarStringEncoder) Len() int {
	return e.count
}

// Size returns the total size of encoded data in bytes.
//
// Returns:
//   - int: Total bytes written to internal buffer since last Reset
func (e *VarStringEncoder) Size() int {
	return e.buf.Len()
}

// Reset clears the encoder state and returns the buffer to the pool.
//
// After calling Reset, the encoder should not be used again.
func (e *VarStringEncoder) Reset() {
	if e.buf != nil {
		pool.PutBlobBuffer(e.buf)
		e.buf = nil
	}
	e.count = 0
}
