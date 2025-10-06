package encoding

import (
	"fmt"
	"iter"
	"unsafe"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/pool"
)

type TimestampRawEncoder struct {
	buf    *pool.ByteBuffer
	count  int
	engine endian.EndianEngine
}

var _ ColumnarEncoder[int64] = (*TimestampRawEncoder)(nil)

// NewTimestampRawEncoder creates a new raw timestamp encoder using the specified endian engine.
//
// Raw encoding stores each timestamp as a fixed 64-bit integer representing microseconds
// since Unix epoch. This provides:
//   - Fixed 8 bytes per timestamp storage
//   - Fast encoding/decoding with no computational overhead
//   - Random access to any timestamp without decoding others
//   - Predictable memory usage (8 × count bytes)
//
// The encoder uses the specified endian engine for byte order consistency across
// the mebo binary format. Typically used with little-endian format.
//
// This encoding is optimal when:
//   - Timestamps are not sequential (delta encoding wouldn't help)
//   - Random access to timestamps is required
//   - Encoding/decoding speed is more important than storage size
//   - Memory usage predictability is important
//
// Parameters:
//   - engine: Endian engine for byte order (typically little-endian)
//
// Returns:
//   - *TimestampRawEncoder: A new encoder instance ready for timestamp encoding
//
// Example:
//
//	encoder := NewTimestampRawEncoder(endian.GetLittleEndianEngine())
//	encoder.Write(time.Now().UnixMicro())  // Single timestamp
//	timestamps := []int64{time.Now().UnixMicro(), time.Now().Add(time.Hour).UnixMicro()}
//	encoder.WriteSlice(timestamps)  // Bulk timestamps
//	data := encoder.Bytes()  // 8 bytes × (1 + 2) = 24 bytes
func NewTimestampRawEncoder(engine endian.EndianEngine) *TimestampRawEncoder {
	return &TimestampRawEncoder{
		engine: engine,
		buf:    pool.GetBlobBuffer(),
	}
}

// Write encodes a single timestamp as a 64-bit microsecond value since Unix epoch.
//
// The timestamp is provided as microseconds since Unix epoch (equivalent to time.Time.UnixMicro())
// and stored as a fixed 8-byte integer in the byte order specified by the endian engine.
//
// This method provides:
//   - Fixed 8-byte storage per timestamp
//   - Fast encoding with minimal CPU overhead
//   - Microsecond precision (1µs resolution)
//   - Consistent with mebo's time-series precision requirements
//   - Amortized O(1) buffer growth for repeated calls
//
// Buffer growth strategy:
//   - Pre-grows buffer when within 8 bytes of capacity
//   - Small buffers (≤4KB): grow by 512 bytes (64 timestamps)
//   - Large buffers (>4KB): grow by 25% of current capacity
//   - Minimizes reallocation frequency for repeated Write calls
//
// For encoding multiple timestamps at once, use WriteSlice for optimal performance.
//
// The encoded bytes are appended to the internal buffer and can be retrieved
// using the Bytes method.
func (e *TimestampRawEncoder) Write(timestampUs int64) {
	e.count++

	// Amortized growth: pre-grow buffer if near capacity
	// This prevents frequent reallocations when Write is called repeatedly
	e.buf.Grow(8)

	e.writeInt64(timestampUs)
}

// WriteSlice encodes a slice of timestamps as 64-bit microsecond values with buffer optimization.
//
// Each timestamp is provided as microseconds since Unix epoch (equivalent to time.Time.UnixMicro())
// and stored as a fixed 8-byte integer. The method pre-grows the internal buffer to minimize
// allocations during bulk format.
//
// Buffer growth strategy:
//   - Pre-allocates len(timestampsMicros) × 8 bytes
//   - Single buffer growth operation for the entire slice
//   - Minimizes memory allocations and copying
//
// This method provides:
//   - Fixed 8-byte storage per timestamp
//   - Optimal bulk encoding performance
//   - Predictable memory usage (8 × len(timestampsMicros) bytes)
//   - Microsecond precision for all timestamps
//
// For encoding single timestamps, use Write for simpler operation.
//
// The encoded bytes are appended to the internal buffer and can be retrieved
// using the Bytes method.
//
// Parameters:
//   - timestampsUs: Slice of timestamps in microseconds since Unix epoch
func (e *TimestampRawEncoder) WriteSlice(timestampsUs []int64) {
	tsLen := len(timestampsUs)
	e.count += tsLen

	if tsLen == 0 {
		return
	}

	// Pre-allocate space for all timestamps (8 bytes each)
	e.buf.Grow(tsLen * 8)

	// Extend buffer length once for all timestamps
	startIdx := e.buf.Len()
	e.buf.ExtendOrGrow(tsLen * 8)
	buf := e.buf.Bytes()

	// Write each timestamp directly using PutUint64 on the buffer slice
	for i, ts := range timestampsUs {
		offset := startIdx + i*8
		e.engine.PutUint64(buf[offset:offset+8], uint64(ts)) //nolint:gosec
	}
}

// Bytes returns the encoded byte slice containing all written timestamps.
//
// Each timestamp occupies exactly 8 bytes in the output, encoded as microseconds
// since Unix epoch in the byte order specified by the endian engine during construction.
//
// The returned slice is valid until the next call to Write, WriteSlice, or Reset.
// The caller must not modify the returned slice as it references the internal buffer.
//
// Output format:
//   - Each timestamp: 8 bytes (int64 as uint64)
//   - Total size: 8 × number_of_timestamps bytes
//   - Byte order: As specified by endian engine
//
// Returns:
//   - []byte: Encoded byte slice (empty if no timestamps written since last Reset)
func (e *TimestampRawEncoder) Bytes() []byte {
	return e.buf.Bytes()
}

// Len returns the number of encoded timestamps.
//
// Returns:
//   - int: Number of timestamps written since last Finish
func (e *TimestampRawEncoder) Len() int {
	return e.count
}

// Size returns the size in bytes of encoded timestamps.
//
// It represents the number of bytes that were written to the internal buffer.
//
// Returns:
//   - int: Total bytes written to internal buffer since last Finish
func (e *TimestampRawEncoder) Size() int {
	return e.buf.Len()
}

// Reset clears the encoder state, allowing it to be reused for a new sequence of timestamps.
//
// Due to the raw encoding strategy, Reset is implemented as a no-op to retain
// the accumulated data in the internal buffer. This allows the encoder to be reused
// for additional timestamps without losing previously encoded data.
//
// The length and size remain unchanged after calling Reset.
// The caller can continue to retrieve the accumulated data using Bytes(), Len(), and Size().
func (e *TimestampRawEncoder) Reset() {
	// No-Op: Keep existing data in buffer
}

// Finish finalizes the encoding process.
//
// This method clears the internal buffer and resets the encoder state, preparing it for a new encoding session.
// After calling Finish, the encoder behaves as if it was newly created.
//
// The Len(), Size() and Bytes() will return zero values after calling Finish.
// The caller can continue to retrieve the accumulated data information using Len(), Size() and Bytes()
// until Finish() is called.
func (e *TimestampRawEncoder) Finish() {
	pool.PutBlobBuffer(e.buf)
	e.buf = pool.GetBlobBuffer()
	e.count = 0
}

// writeInt64 encodes a single int64 timestamp (microseconds) into the buffer.
//
// This helper method converts the int64 to uint64 and uses the endian engine's
// PutUint64 method to write the 8-byte representation directly to the buffer.
//
// The method assumes the buffer has sufficient capacity (caller must ensure this).
// When called from Write(), the buffer is pre-grown to avoid reallocations.
// When called from WriteSlice(), the buffer is pre-allocated for all timestamps.
//
// The int64 to uint64 conversion is safe for timestamp values as Unix microsecond
// timestamps are always positive values that fit within the positive range of int64.
func (e *TimestampRawEncoder) writeInt64(timestamp int64) {
	bufLen := e.buf.Len()
	bs := e.buf.Bytes()[bufLen : bufLen+8]
	e.engine.PutUint64(bs, uint64(timestamp)) //nolint:gosec
	e.buf.SetLength(bufLen + 8)
}

type TimestampRawDecoder struct {
	engine endian.EndianEngine
}

var _ ColumnarDecoder[int64] = TimestampRawDecoder{}

// NewTimestampRawDecoder creates a new raw timestamp decoder using the specified endian engine.
//
// The decoder uses the specified endian engine for byte order consistency across
// the mebo binary format. Typically used with little-endian format.
//
// The decoder is stateless and can be reused across multiple decoding operations.
// Each call to All() operates independently on the provided data.
//
// Parameters:
//   - engine: Endian engine for byte order (must match encoder's engine)
//
// Returns:
//   - TimestampRawDecoder: A new decoder instance (stateless, can be reused)
func NewTimestampRawDecoder(engine endian.EndianEngine) TimestampRawDecoder {
	return TimestampRawDecoder{engine: engine}
}

// All returns a channel that yields all decoded timestamps from the provided data.
//
// The data should be the byte slice payload produced by a corresponding TimestampEncoder.
// The count parameter specifies the expected number of timestamps to decode.
//
// The method returns an iterator that yields each decoded timestamp in sequence.
// The iterator will yield exactly 'count' timestamps if the data is valid.
//
// If the data is malformed or does not contain enough timestamps, the iterator
// may yield fewer timestamps. The caller should handle this case appropriately.
//
// Parameters:
//   - data: Encoded byte slice from TimestampRawEncoder.Bytes()
//   - count: Expected number of timestamps to decode
//
// Returns:
//   - iter.Seq[int64]: Iterator yielding decoded timestamps (microseconds since Unix epoch)
func (d TimestampRawDecoder) All(data []byte, count int) iter.Seq[int64] {
	return func(yield func(int64) bool) {
		if len(data) == 0 || count == 0 {
			return
		}

		dataLen := len(data)
		if dataLen%8 != 0 {
			return
		}

		for i := range count {
			start := i * 8
			if start+8 > dataLen {
				break
			}

			ts := int64(d.engine.Uint64(data[start : start+8])) //nolint: gosec

			if !yield(ts) {
				break
			}
		}
	}
}

// At retrieves the timestamp at the specified index from the encoded data.
//
// The data should be the byte slice payload produced by a corresponding TimestampEncoder.
// The index is zero-based, so index 0 retrieves the first timestamp.
//
// If the index is out of bounds (negative or >= count), the method returns false.
// If the data is malformed or does not contain enough timestamps, it may return false.
//
// Parameters:
//   - data: Encoded byte slice from TimestampRawEncoder.Bytes()
//   - index: Zero-based index of the timestamp to retrieve
//   - count: Total number of timestamps in the encoded data
//
// Returns:
//   - int64: The timestamp at the specified index (microseconds since Unix epoch)
//   - bool: true if the index exists and was successfully decoded, false otherwise
func (d TimestampRawDecoder) At(data []byte, index int, count int) (int64, bool) {
	if len(data) == 0 || index < 0 || index >= count {
		return 0, false
	}

	start := index * 8
	if start+8 > len(data) {
		return 0, false
	}

	ts := int64(d.engine.Uint64(data[start : start+8])) //nolint: gosec

	return ts, true
}

// TimestampRawDecoder is an optimized decoder for raw timestamps using unsafe memory operations.
//
// This decoder uses unsafe memory operations to map the input byte slice directly to an int64 slice,
// avoiding intermediate allocations and copies. It is significantly faster than the safe decoder,
// especially for large datasets.
//
// Caution: This decoder assumes that the input byte slice has the correct alignment and length.
// The caller must ensure that the input length is a multiple of 8 bytes, as each int64 value occupies exactly 8 bytes.
// Using this decoder with improperly aligned or sized data may lead to undefined behavior.
type TimestampRawUnsafeDecoder struct{}

var _ ColumnarDecoder[int64] = TimestampRawUnsafeDecoder{}

// NewTimestampRawUnsafeDecoder creates a new raw timestamp decoder.
//
// The decoder is stateless and can be reused across multiple decoding operations.
// Each call to All() operates independently on the provided data.
//
// Parameters:
//   - engine: Endian engine (currently unused but kept for interface compatibility)
//
// Returns:
//   - TimestampRawUnsafeDecoder: A new unsafe decoder instance (stateless, can be reused)
func NewTimestampRawUnsafeDecoder(engine endian.EndianEngine) TimestampRawUnsafeDecoder {
	return TimestampRawUnsafeDecoder{}
}

// All decodes all timestamps from the given byte slice using unsafe memory operations.
//
// It returns a sequence of int64 timestamps decoded from the input byte slice.
// The data must be a multiple of 8 bytes, as each int64 timestamp occupies exactly 8 bytes.
//
// Parameters:
//   - data: Encoded byte slice from TimestampRawEncoder.Bytes() (must be multiple of 8 bytes)
//   - count: Expected number of timestamps to decode
//
// Returns:
//   - iter.Seq[int64]: Iterator yielding decoded timestamps (microseconds since Unix epoch)
func (d TimestampRawUnsafeDecoder) All(data []byte, count int) iter.Seq[int64] {
	return func(yield func(int64) bool) {
		if len(data) < count*8 || count == 0 {
			return
		}

		timestamps, err := unsafeDecodeInt64Slice(data)
		if err != nil {
			return
		}

		for i, ts := range timestamps {
			if i >= count {
				break
			}

			if !yield(ts) {
				break
			}
		}
	}
}

// At retrieves the timestamp at the specified index from the encoded data.
//
// The data should be the byte slice payload produced by a TimestampRawEncoder.
// The index is zero-based, so index 0 retrieves the first timestamp.
//
// If the index is out of bounds (negative or >= count), the method returns false.
// If the data is malformed or does not contain enough timestamps, it may return false.
//
// Parameters:
//   - data: Encoded byte slice from TimestampRawEncoder.Bytes() (must be multiple of 8 bytes)
//   - index: Zero-based index of the timestamp to retrieve
//   - count: Total number of timestamps in the encoded data
//
// Returns:
//   - int64: The timestamp at the specified index (microseconds since Unix epoch)
//   - bool: true if the index exists and was successfully decoded, false otherwise
func (d TimestampRawUnsafeDecoder) At(data []byte, index int, count int) (int64, bool) {
	if len(data) == 0 || index < 0 || index >= count {
		return 0, false
	}

	timestamps, err := unsafeDecodeInt64Slice(data)
	if err != nil {
		return 0, false
	}

	if index >= len(timestamps) {
		return 0, false
	}

	return timestamps[index], true
}

func unsafeDecodeInt64Slice(data []byte) ([]int64, error) {
	if len(data)%8 != 0 {
		return nil, fmt.Errorf("byte slice length (%d) is not a multiple of 8", len(data))
	}

	// Zero-copy conversion using unsafe.Slice
	// Cast the byte slice pointer to *int64 and create a slice from it
	ptr := (*int64)(unsafe.Pointer(&data[0]))

	return unsafe.Slice(ptr, len(data)/8), nil
}
