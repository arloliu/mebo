package section

import (
	"bytes"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
)

// NumericIndexEntry records information about a single metric in the float value blob index section.
// It is a fixed size of 16 bytes and uses delta offset encoding for space efficiency.
//
// Delta Offset Encoding:
//   - First metric: Stores absolute offsets from payload starts (typically 0)
//   - Subsequent metrics: Stores delta = (current_offset - previous_offset)
//   - Benefits: Smaller delta values fit better in uint16 range
//   - Decoding: Absolute offsets reconstructed by accumulating deltas
//
// Example with 3 metrics (raw encoding, 8 bytes per timestamp/value):
//
//	Metric 1: 5 points → TimestampOffset=0, ValueOffset=0
//	Metric 2: 3 points → TimestampOffset=40 (delta: 5×8), ValueOffset=40
//	Metric 3: 7 points → TimestampOffset=24 (delta: 3×8), ValueOffset=24
//	Decoded absolute offsets: Timestamps=[0,40,64], Values=[0,40,64]
type NumericIndexEntry struct {
	// MetricID is the unsigned 64-bit metric id or the xxHash64 hash of the metric name string.
	//
	// Offset: 0, Size: 8 bytes
	MetricID uint64

	// TimestampOffset stores the delta offset (in bytes) from the previous metric's timestamp offset.
	// First metric: absolute offset from timestamp payload start (typically 0)
	// Subsequent metrics: delta = (current_offset - previous_offset)
	// Decoder reconstructs: absolute_offset[i] = absolute_offset[i-1] + delta[i]
	//
	// Offset: 10, Size: 2 bytes (store as uint16 on disk)
	//
	// NOTE: After decoding, this field contains the ABSOLUTE offset (not delta).
	// The absolute offset can exceed 65535 bytes, so we use int (not uint16) in memory.
	// On disk, deltas are stored as uint16 (2 bytes) to save space.
	TimestampOffset int

	// TimestampLength is the total byte length of the encoded timestamps for this metric.
	//
	// This field is not stored on disk and is only used in memory for slicing and dicing.
	// It can be computed as TimestampLength = Count * timestamp_size.
	TimestampLength int

	// ValueOffset stores the delta offset (in bytes) from the previous metric's value offset.
	// First metric: absolute offset from value payload start (typically 0)
	// Subsequent metrics: delta = (current_offset - previous_offset)
	// Decoder reconstructs: absolute_offset[i] = absolute_offset[i-1] + delta[i]
	//
	// Offset: 12, Size: 2 bytes (store as uint16 on disk)
	//
	// NOTE: After decoding, this field contains the ABSOLUTE offset (not delta).
	// The absolute offset can exceed 65535 bytes, so we use int (not uint16) in memory.
	// On disk, deltas are stored as uint16 (2 bytes) to save space.
	ValueOffset int

	// ValueLength is the total byte length of the encoded values for this metric.
	//
	// This field is not stored on disk and is only used in memory for slicing and dicing.
	// It can be computed as ValueLength = Count * value_size.
	ValueLength int

	// TagOffset stores the delta offset (in bytes) from the previous metric's tag offset.
	// First metric: absolute offset from tag payload start (typically 0)
	// Subsequent metrics: delta = (current_offset - previous_offset)
	// Decoder reconstructs: absolute_offset[i] = absolute_offset[i-1] + delta[i]
	//
	// Offset: 14, Size: 2 bytes (store as uint16 on disk)
	//
	// NOTE: After decoding, this field contains the ABSOLUTE offset (not delta).
	// The absolute offset can exceed 65535 bytes, so we use int (not uint16) in memory.
	// On disk, deltas are stored as uint16 (2 bytes) to save space.
	TagOffset int

	// TagLength is the total byte length of the encoded tags for this metric.
	// This field is not stored on disk and is only used in memory for slicing and dicing.
	// It can be computed as TagLength = Count * tag_size.
	TagLength int

	// Count is the number of data points (timestamps/values) for this metric.
	//
	// Compact (V1/V2): Offset 8, Size 2 bytes (uint16 on disk, max 65535)
	// Extended (V2Ext): Offset 8, Size 4 bytes (uint32 on disk, max 4294967295)
	//
	// NOTE: In memory, we use int to avoid frequent type conversions during processing.
	Count int
}

// Bytes returns the index entry as a byte slice using the specified endian engine.
//
// This method uses stack allocation for better performance. It can only be used during
// encoding when offsets fit in uint16 range. After decoding, offsets may exceed uint16
// range and should not be written back using this method.
//
// Parameters:
//   - engine: Endian engine for byte order
//
// Returns:
//   - []byte: 16-byte index entry with all fields encoded
func (e *NumericIndexEntry) Bytes(engine endian.EndianEngine) []byte {
	var b [NumericIndexEntrySize]byte // stack allocation, it's faster than heap allocation
	engine.PutUint64(b[0:8], e.MetricID)
	engine.PutUint16(b[8:10], uint16(e.Count))            //nolint: gosec
	engine.PutUint16(b[10:12], uint16(e.TimestampOffset)) //nolint: gosec
	engine.PutUint16(b[12:14], uint16(e.ValueOffset))     //nolint: gosec
	engine.PutUint16(b[14:16], uint16(e.TagOffset))       //nolint: gosec

	return b[:]
}

// WriteTo writes the index entry to a buffer using the specified endian engine.
//
// Parameters:
//   - buf: Buffer to write to (will grow if needed)
//   - engine: Endian engine for byte order
func (e *NumericIndexEntry) WriteTo(buf *bytes.Buffer, engine endian.EndianEngine) {
	buf.Grow(NumericIndexEntrySize)

	start := buf.Len()
	var b [NumericIndexEntrySize]byte
	buf.Write(b[:])

	// Write directly to the allocated space
	data := buf.Bytes()[start : start+NumericIndexEntrySize]
	engine.PutUint64(data[0:8], e.MetricID)
	engine.PutUint16(data[8:10], uint16(e.Count))            //nolint: gosec
	engine.PutUint16(data[10:12], uint16(e.TimestampOffset)) //nolint: gosec
	engine.PutUint16(data[12:14], uint16(e.ValueOffset))     //nolint: gosec
	engine.PutUint16(data[14:16], uint16(e.TagOffset))       //nolint: gosec
}

// WriteToSlice writes to a pre-allocated slice and returns the next position.
//
// This is the most efficient method when writing multiple entries sequentially.
//
// Parameters:
//   - data: Pre-allocated byte slice (must have space for 16 bytes at offset)
//   - offset: Starting position in data slice
//   - engine: Endian engine for byte order
//
// Returns:
//   - int: Next write position (offset + 16)
func (e *NumericIndexEntry) WriteToSlice(data []byte, offset int, engine endian.EndianEngine) int {
	engine.PutUint64(data[offset:offset+8], e.MetricID)
	engine.PutUint16(data[offset+8:offset+10], uint16(e.Count))            //nolint: gosec
	engine.PutUint16(data[offset+10:offset+12], uint16(e.TimestampOffset)) //nolint: gosec
	engine.PutUint16(data[offset+12:offset+14], uint16(e.ValueOffset))     //nolint: gosec
	engine.PutUint16(data[offset+14:offset+16], uint16(e.TagOffset))       //nolint: gosec

	return offset + NumericIndexEntrySize
}

// Bytes32 returns the index entry as a 32-byte slice using extended format.
//
// Layout (32 bytes, cache-line aligned — 2 entries per 64B L1 cache line):
//   - [0:8]   MetricID   (uint64)
//   - [8:12]  Count      (uint32)
//   - [12:16] TsOffset   (uint32)
//   - [16:20] ValOffset  (uint32)
//   - [20:24] TagOffset  (uint32)
//   - [24:32] Reserved   (8 bytes, zero)
//
// Parameters:
//   - engine: Endian engine for byte order
//
// Returns:
//   - []byte: 32-byte index entry with uint32 count/offsets and 8-byte reserved tail
func (e *NumericIndexEntry) Bytes32(engine endian.EndianEngine) []byte {
	var b [NumericExtIndexEntrySize]byte
	engine.PutUint64(b[0:8], e.MetricID)
	engine.PutUint32(b[8:12], uint32(e.Count))            //nolint: gosec
	engine.PutUint32(b[12:16], uint32(e.TimestampOffset)) //nolint: gosec
	engine.PutUint32(b[16:20], uint32(e.ValueOffset))     //nolint: gosec
	engine.PutUint32(b[20:24], uint32(e.TagOffset))       //nolint: gosec
	// b[24:32] reserved (zero-initialized)

	return b[:]
}

// WriteToSlice32 writes a 32-byte extended index entry to a pre-allocated slice
// and returns the next position.
//
// Layout: MetricID(8) + Count(4) + TsOffset(4) + ValOffset(4) + TagOffset(4) + Reserved(8) = 32
//
// Parameters:
//   - data: Pre-allocated byte slice (must have space for 32 bytes at offset)
//   - offset: Starting position in data slice
//   - engine: Endian engine for byte order
//
// Returns:
//   - int: Next write position (offset + 32)
func (e *NumericIndexEntry) WriteToSlice32(data []byte, offset int, engine endian.EndianEngine) int {
	engine.PutUint64(data[offset:offset+8], e.MetricID)
	engine.PutUint32(data[offset+8:offset+12], uint32(e.Count))            //nolint: gosec
	engine.PutUint32(data[offset+12:offset+16], uint32(e.TimestampOffset)) //nolint: gosec
	engine.PutUint32(data[offset+16:offset+20], uint32(e.ValueOffset))     //nolint: gosec
	engine.PutUint32(data[offset+20:offset+24], uint32(e.TagOffset))       //nolint: gosec
	// Explicitly zero reserved bytes to enforce "must be 0" contract
	clear(data[offset+24 : offset+32])

	return offset + NumericExtIndexEntrySize
}

// NewNumericIndexEntry creates a new NumericIndexEntry with the specified metric ID and count.
//
// Offsets are initialized to zero and should be set by the encoder.
//
// Parameters:
//   - metricID: Unique 64-bit metric identifier
//   - count: Number of data points for this metric
//
// Returns:
//   - NumericIndexEntry: New index entry with zero offsets
func NewNumericIndexEntry(metricID uint64, count int) NumericIndexEntry {
	return NumericIndexEntry{
		MetricID:        metricID,
		Count:           count,
		TimestampOffset: 0,
		ValueOffset:     0,
		TagOffset:       0,
	}
}

// ParseNumericIndexEntry parses a NumericIndexEntry from a byte slice.
//
// Parameters:
//   - data: Byte slice containing index entry (must be at least 16 bytes)
//   - engine: Endian engine for byte order
//
// Returns:
//   - NumericIndexEntry: Parsed index entry
//   - error: ErrInvalidIndexEntrySize if data is too short
func ParseNumericIndexEntry(data []byte, engine endian.EndianEngine) (NumericIndexEntry, error) {
	if len(data) < NumericIndexEntrySize {
		return NumericIndexEntry{}, errs.ErrInvalidIndexEntrySize
	}

	return NumericIndexEntry{
		MetricID:        engine.Uint64(data[0:8]),
		Count:           int(engine.Uint16(data[8:10])),
		TimestampOffset: int(engine.Uint16(data[10:12])),
		ValueOffset:     int(engine.Uint16(data[12:14])),
		TagOffset:       int(engine.Uint16(data[14:16])),
	}, nil
}

// ParseNumericIndexEntryExt parses a 32-byte extended NumericIndexEntry from a byte slice.
//
// Layout: MetricID(8) + Count(uint32, 4) + TsOffset(4) + ValOffset(4) + TagOffset(4) + Reserved(8) = 32
//
// Parameters:
//   - data: Byte slice containing index entry (must be at least 32 bytes)
//   - engine: Endian engine for byte order
//
// Returns:
//   - NumericIndexEntry: Parsed index entry with uint32-range count and offsets
//   - error: ErrInvalidIndexEntrySize if data is too short
func ParseNumericIndexEntryExt(data []byte, engine endian.EndianEngine) (NumericIndexEntry, error) {
	if len(data) < NumericExtIndexEntrySize {
		return NumericIndexEntry{}, errs.ErrInvalidIndexEntrySize
	}

	// Validate reserved bytes (24-32) are zero per the wire format contract
	for _, b := range data[24:32] {
		if b != 0 {
			return NumericIndexEntry{}, errs.ErrInvalidReservedBytes
		}
	}

	count := engine.Uint32(data[8:12])
	tsOff := engine.Uint32(data[12:16])
	valOff := engine.Uint32(data[16:20])
	tagOff := engine.Uint32(data[20:24])

	// Guard against silent int overflow on 32-bit platforms where int is 32 bits.
	// On 64-bit this check is optimized away by the compiler (maxSafeUint32 == MaxUint32).
	if count > maxSafeUint32 || tsOff > maxSafeUint32 || valOff > maxSafeUint32 || tagOff > maxSafeUint32 {
		return NumericIndexEntry{}, errs.ErrIndexEntryOverflow
	}

	return NumericIndexEntry{
		MetricID:        engine.Uint64(data[0:8]),
		Count:           int(count),
		TimestampOffset: int(tsOff),
		ValueOffset:     int(valOff),
		TagOffset:       int(tagOff),
	}, nil
}

// GetMetricID returns the metric ID for this entry.
//
// This method is used by the generic indexMaps type for type-safe access.
//
// Returns:
//   - uint64: The 64-bit metric identifier
func (e NumericIndexEntry) GetMetricID() uint64 {
	return e.MetricID
}

// GetCount returns the count of data points for this metric.
//
// This method is used by the generic indexMaps type for type-safe access.
//
// Returns:
//   - uint32: Number of data points (converted from int)
func (e NumericIndexEntry) GetCount() uint32 {
	return uint32(e.Count) //nolint: gosec
}
