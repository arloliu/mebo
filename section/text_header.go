package section

import (
	"time"
	"unsafe"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
)

// TextHeader represents the fixed-size header section for text blobs.
// It is 32 bytes and contains metadata about the blob structure.
//
// Layout differences from FloatValueHeader:
//   - Uses magic number 0xEB10 (vs 0xEA10 for float)
//   - DataOffset field instead of separate timestamp/value offsets
//   - Single data section (not separate timestamp/value sections)
//   - Uses TextFlag (simpler than NumericFlag)
type TextHeader struct {
	// Flag is a packed field for various flags and magic number (0xEB10).
	Flag TextFlag // 4 bytes, offset 0-3

	Reserved [4]byte // Reserved for future use, must be zero, offset 28-31

	// StartTime is the start time of the metric, unix timestamp in microseconds.
	StartTime int64 // 8 bytes, offset 4-11
	// IndexOffset is the byte offset to the start of the metric index section.
	IndexOffset uint32 // 4 bytes, offset 12-15
	// DataOffset is the byte offset to the start of the data section.
	DataOffset uint32 // 4 bytes, offset 16-19
	// DataSize is the uncompressed size of the data section in bytes.
	// Used for verification and calculating the last metric's Size field.
	DataSize uint32 // 4 bytes, offset 20-23
	// MetricCount is the number of unique metrics stored in the blob, max to 65535.
	MetricCount uint32 // 4 bytes, offset 24-27
}

// NewTextHeader creates a new TextHeader with the given start time and metric count.
func NewTextHeader(startTime time.Time, metricCount int) (*TextHeader, error) {
	if metricCount < 0 || metricCount > 65535 {
		return nil, errs.ErrInvalidMetricCount
	}

	return &TextHeader{
		StartTime:   startTime.UnixMicro(),
		Flag:        NewTextFlag(),
		IndexOffset: IndexOffsetOffset,
		DataOffset:  uint32(IndexOffsetOffset + metricCount*TextIndexEntrySize), //nolint: gosec
		MetricCount: uint32(metricCount),
	}, nil
}

// Parse parses the header from a byte slice.
// It returns an error if the data is not exactly 32 bytes or if the flags are invalid.
func (h *TextHeader) Parse(data []byte) error {
	if len(data) != HeaderSize {
		return errs.ErrInvalidHeaderSize
	}

	// Parse options first to determine endianness (always little-endian for Options field itself)
	h.Flag.Options = uint16(data[0]) | (uint16(data[1]) << 8)
	h.Flag.TimestampEncoding = data[2]
	h.Flag.DataCompression = data[3]

	engine := h.GetEndianEngine()

	// Use unsafe pointer conversion to interpret bytes as signed int64
	startTimeUint := engine.Uint64(data[4:12])
	h.StartTime = *(*int64)(unsafe.Pointer(&startTimeUint))

	h.MetricCount = engine.Uint32(data[12:16])
	h.IndexOffset = engine.Uint32(data[16:20])
	h.DataOffset = engine.Uint32(data[20:24])
	h.DataSize = engine.Uint32(data[24:28])
	copy(h.Reserved[:], data[28:32])

	if !h.IsValidFlags() {
		return errs.ErrInvalidHeaderFlags
	}

	return nil
}

// Bytes serializes the TextHeader into a byte slice.
func (h *TextHeader) Bytes() []byte {
	b := make([]byte, HeaderSize)

	engine := h.GetEndianEngine()

	engine.PutUint16(b[0:2], h.Flag.Options)
	b[2] = h.Flag.TimestampEncoding
	b[3] = h.Flag.DataCompression
	// Use bitwise conversion to avoid overflow warning
	engine.PutUint64(b[4:12], *(*uint64)(unsafe.Pointer(&h.StartTime)))
	engine.PutUint32(b[12:16], h.MetricCount)
	engine.PutUint32(b[16:20], h.IndexOffset)
	engine.PutUint32(b[20:24], h.DataOffset)
	engine.PutUint32(b[24:28], h.DataSize)
	copy(b[28:32], h.Reserved[:])

	return b
}

// StartTimeAsTime returns the start time as a time.Time object.
func (h *TextHeader) StartTimeAsTime() time.Time {
	return time.UnixMicro(h.StartTime)
}

// GetEndianEngine returns the appropriate endian engine based on the header flags.
func (h *TextHeader) GetEndianEngine() endian.EndianEngine {
	if h.Flag.IsBigEndian() {
		return endian.GetBigEndianEngine()
	}

	return endian.GetLittleEndianEngine()
}

// IsValidFlags checks if the header flags are valid for text value blob.
func (h *TextHeader) IsValidFlags() bool {
	if err := h.Flag.Validate(); err != nil {
		return false
	}

	return true
}
