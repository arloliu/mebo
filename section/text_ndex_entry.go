package section

import (
	"bytes"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
)

// TextIndexEntry records information about a single metric in the text value blob index section.
// It is a fixed size of 16 bytes and uses absolute offsets (not delta encoding like float values).
//
// Absolute Offset Encoding:
//   - Each metric stores its actual byte offset in the data section
//   - Also stores the size of its data block
//   - Enables O(1) random access without reconstructing deltas
//   - Simpler decoder - direct array index, no accumulation needed
//
// Example with 3 metrics:
//
//	Metric 1: 150 bytes → Offset=0, Size=150
//	Metric 2: 200 bytes → Offset=150, Size=200
//	Metric 3: 100 bytes → Offset=350, Size=100
//	Direct access: data[entry.Offset : entry.Offset+entry.Size]
type TextIndexEntry struct {
	// MetricID is the unsigned 64-bit metric id or the xxHash64 hash of the metric name string.
	MetricID uint64 // 8 bytes, offset 0-7

	// Count is the number of data points for this metric.
	Count uint16 // 2 bytes, offset 8-9

	// Reserved for future use, must be set to 0.
	Reserved1 uint16 // 2 bytes, offset 10-11

	// Offset is the absolute byte offset from the start of the data section.
	// Points to where this metric's data begins in the decompressed data section.
	Offset uint32 // 4 bytes, offset 12-15

	// Size is the size in bytes of this metric's data in the decompressed data section.
	Size uint32 // Calculated by decoder from offset differences
}

// WriteTo writes the index entry to a buffer using the specified endian engine.
// Returns the number of bytes written (always 16).
func (e *TextIndexEntry) WriteTo(buf *bytes.Buffer, engine endian.EndianEngine) (int, error) {
	b := make([]byte, TextIndexEntrySize)
	if err := e.WriteToSlice(b, engine); err != nil {
		return 0, err
	}

	return buf.Write(b)
}

// WriteToSlice writes the index entry to a byte slice using the specified endian engine.
// The slice must be at least 16 bytes long.
func (e *TextIndexEntry) WriteToSlice(b []byte, engine endian.EndianEngine) error {
	if len(b) < TextIndexEntrySize {
		return errs.ErrInvalidIndexEntrySize
	}

	engine.PutUint64(b[0:8], e.MetricID)
	engine.PutUint16(b[8:10], e.Count)
	engine.PutUint16(b[10:12], e.Reserved1)
	engine.PutUint32(b[12:16], e.Offset)

	// Note: Size is not stored in the 16-byte index entry to save space.
	// The decoder calculates it from offset differences:
	//   Size[i] = Offset[i+1] - Offset[i]
	// For the last metric:
	//   Size[last] = DataSize - Offset[last]
	// Where DataSize comes from the TextHeader.

	return nil
}

// ParseTextIndexEntry parses a text value index entry from a byte slice.
// The size field must be calculated separately by the caller from offset differences.
func ParseTextIndexEntry(data []byte, engine endian.EndianEngine) (TextIndexEntry, error) {
	if len(data) < TextIndexEntrySize {
		return TextIndexEntry{}, errs.ErrInvalidIndexEntrySize
	}

	return TextIndexEntry{
		MetricID:  engine.Uint64(data[0:8]),
		Count:     engine.Uint16(data[8:10]),
		Reserved1: engine.Uint16(data[10:12]),
		Offset:    engine.Uint32(data[12:16]),
		Size:      0, // Calculated by decoder from offset differences
	}, nil
}

// GetCount returns the count of data points for this metric.
// This method is used by the generic indexMaps type for type-safe access.
func (e TextIndexEntry) GetCount() uint32 {
	return uint32(e.Count)
}
