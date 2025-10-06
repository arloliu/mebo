package section

import (
	"time"
	"unsafe"

	"github.com/arloliu/mebo/errs"
)

// Numeric
// Header represents the fixed-size header section at the start of the metric blob.
type NumericHeader struct {
	// StartTime is the start time of the metric. the unix timestamp in microseconds.
	StartTime int64 // byte offset 4-11
	// MetricCount is the number of unique metrics stored in the blob, max to 65535.
	MetricCount uint32 // byte offset 12-15
	// IndexOffset is the byte offset to the start of the metric index section.
	IndexOffset uint32 // byte offset 16-19
	// TimestampPayloadOffset is the byte offset to the start of the timestamp payload section.
	// It records the offset after the index section.
	TimestampPayloadOffset uint32 // byte offset 20-23
	// ValuePayloadOffset is the byte offset to the start of the value payload section.
	// It records the offset after the encoded and compressed (if any) timestamp payload section.
	ValuePayloadOffset uint32 // byte offset 24-27
	// TagPayloadOffset is the byte offset to the start of the tag payload section.
	// It records the offset after the encoded and compressed (if any) value payload section.
	TagPayloadOffset uint32 // byte offset 28-31

	// Flag is a packed field for various flags and magic number.
	Flag NumericFlag // byte offset 0-3
}

// NewNumeric
// Header creates a new Numeric
// Header with the given start time.
// The metric count and payload offsets will be set when the encoder finishes.
func NewNumericHeader(startTime time.Time) *NumericHeader {
	return &NumericHeader{
		StartTime:              startTime.UnixMicro(),
		Flag:                   NewNumericFlag(),
		IndexOffset:            IndexOffsetOffset,
		TimestampPayloadOffset: 0, // Will be calculated in Finish()
		MetricCount:            0, // Will be set in Finish()
	}
}

// Parse parses the header from a byte slice.
//
// Parameters:
//   - data: Byte slice containing header (must be exactly 32 bytes)
//
// Returns:
//   - error: ErrInvalidHeaderSize if data is not 32 bytes, or flag validation errors
func (h *NumericHeader) Parse(data []byte) error {
	if len(data) != HeaderSize {
		return errs.ErrInvalidHeaderSize
	}

	// Parse options first to determine endianness (always little-endian for Options field itself)
	h.Flag.Options = uint16(data[0]) | (uint16(data[1]) << 8)
	h.Flag.EncodingType = data[2]
	h.Flag.CompressionType = data[3]

	engine := h.Flag.GetEndianEngine()

	// Use unsafe pointer conversion to interpret bytes as signed int64
	startTimeUint := engine.Uint64(data[4:12])
	h.StartTime = *(*int64)(unsafe.Pointer(&startTimeUint))

	h.MetricCount = engine.Uint32(data[12:16])
	h.IndexOffset = engine.Uint32(data[16:20])
	h.TimestampPayloadOffset = engine.Uint32(data[20:24])
	h.ValuePayloadOffset = engine.Uint32(data[24:28])
	h.TagPayloadOffset = engine.Uint32(data[28:32])

	return h.Flag.Validate()
}

// Bytes serializes the Numeric
// Header into a byte slice.
func (h *NumericHeader) Bytes() []byte {
	b := make([]byte, HeaderSize)

	engine := h.Flag.GetEndianEngine()

	engine.PutUint16(b[0:2], h.Flag.Options)
	b[2] = h.Flag.EncodingType
	b[3] = h.Flag.CompressionType
	// Use bitwise conversion to avoid overflow warning - timestamps are stored as-is in binary
	engine.PutUint64(b[4:12], *(*uint64)(unsafe.Pointer(&h.StartTime)))
	engine.PutUint32(b[12:16], h.MetricCount)
	engine.PutUint32(b[16:20], h.IndexOffset)
	engine.PutUint32(b[20:24], h.TimestampPayloadOffset)
	engine.PutUint32(b[24:28], h.ValuePayloadOffset)
	engine.PutUint32(b[28:32], h.TagPayloadOffset)

	return b
}

// StartTimeAsTime returns the start time as a time.Time object.
//
// Returns:
//   - time.Time: Start time converted from microseconds since Unix epoch
func (h *NumericHeader) StartTimeAsTime() time.Time {
	return time.UnixMicro(h.StartTime)
}

// ParseNumericHeader parses a NumericHeader from a byte slice.
//
// Parameters:
//   - data: Byte slice containing header (must be at least 32 bytes)
//
// Returns:
//   - NumericHeader: Parsed header struct
//   - error: ErrInvalidHeaderSize or flag validation errors
func ParseNumericHeader(data []byte) (NumericHeader, error) {
	if len(data) < HeaderSize {
		return NumericHeader{}, errs.ErrInvalidHeaderSize
	}

	h := NumericHeader{}
	if err := h.Parse(data[:HeaderSize]); err != nil {
		return NumericHeader{}, err
	}

	return h, nil
}
