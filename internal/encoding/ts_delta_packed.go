package encoding

import (
	"encoding/binary"
	"iter"

	"github.com/arloliu/mebo/encoding"
	"github.com/arloliu/mebo/internal/pool"
)

// Group Varint control byte layout:
// Each control byte describes 4 values, using 2 bits per value.
// The 2 bits encode the byte length of the zigzag-encoded value:
//
//	00 = 1 byte  (values 0-255)
//	01 = 2 bytes (values 256-65535)
//	10 = 4 bytes (values 65536-4294967295)
//	11 = 8 bytes (values > 4294967295)
//
// Control byte bit layout: [val3:2][val2:2][val1:2][val0:2]
// The data bytes for all 4 values immediately follow the control byte.
const (
	groupSize = 4 // Number of values packed per control byte
)

// groupVarintLengths maps 2-bit tag to byte length.
var groupVarintLengths = [4]int{1, 2, 4, 8}

// TimestampDeltaPackedEncoder implements TimestampEncoder using delta-of-delta encoding
// with Group Varint packing for the delta-of-delta values.
//
// Compared to TimestampDeltaEncoder which uses LEB128 varint per value, this encoder
// packs every 4 delta-of-delta values with a single control byte, eliminating per-byte
// continuation-bit branches during decoding and enabling byte-aligned bulk reads.
//
// Encoding format:
//  1. First timestamp: Full varint-encoded value (same as TimestampDeltaEncoder)
//  2. Second timestamp: Zigzag + varint encoded delta (same as TimestampDeltaEncoder)
//  3. Remaining timestamps: Grouped into blocks of 4 delta-of-deltas, each block has:
//     - 1 control byte (2 bits per value encoding byte-length)
//     - N data bytes (1/2/4/8 bytes per value, little-endian)
//  4. Tail values (< 4 remaining): Packed with a partial control byte
//
// Space characteristics vs LEB128 varint:
//   - Regular intervals (DoD=0): 1.25 bytes/value (1 control byte per 4 values + 1 data byte each)
//     vs 1 byte/value for LEB128. Slight overhead at small N.
//   - Semi-regular intervals: Comparable or better depending on value distribution
//   - The win is in decode throughput, not encode size
type TimestampDeltaPackedEncoder struct {
	prevTS    int64
	prevDelta int64
	buf       *pool.ByteBuffer
	count     int
	seqCount  int
	// Accumulator for grouping delta-of-deltas before writing
	pending    [groupSize]uint64
	pendingLen int
}

var _ encoding.ColumnarEncoder[int64] = (*TimestampDeltaPackedEncoder)(nil)

// NewTimestampDeltaPackedEncoder creates a new delta-of-delta timestamp encoder
// with Group Varint packing for improved decode throughput.
//
// Returns:
//   - *TimestampDeltaPackedEncoder: A new encoder instance ready for timestamp encoding
func NewTimestampDeltaPackedEncoder() *TimestampDeltaPackedEncoder {
	return &TimestampDeltaPackedEncoder{
		buf: pool.GetBlobBuffer(),
	}
}

// Write encodes a single timestamp using delta-of-delta with Group Varint packing.
//
// Parameters:
//   - timestampUs: Timestamp in microseconds since Unix epoch
func (e *TimestampDeltaPackedEncoder) Write(timestampUs int64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write after Finish()")
	}

	e.count++
	e.seqCount++

	if e.seqCount == 1 {
		e.appendUvarint(uint64(timestampUs)) //nolint:gosec
		e.prevTS = timestampUs
		return
	}

	delta := timestampUs - e.prevTS

	if e.seqCount == 2 {
		zigzag := (delta << 1) ^ (delta >> 63)
		e.appendUvarint(uint64(zigzag)) //nolint:gosec
		e.prevTS = timestampUs
		e.prevDelta = delta

		return
	}

	// Delta-of-delta: accumulate into group
	deltaOfDelta := delta - e.prevDelta
	zigzag := uint64((deltaOfDelta << 1) ^ (deltaOfDelta >> 63)) //nolint:gosec

	e.pending[e.pendingLen] = zigzag
	e.pendingLen++

	if e.pendingLen == groupSize {
		e.flushGroup(groupSize)
	}

	e.prevTS = timestampUs
	e.prevDelta = delta
}

// WriteSlice encodes a slice of timestamps using delta-of-delta with Group Varint packing.
//
// Parameters:
//   - timestampsUs: Slice of timestamps in microseconds since Unix epoch
func (e *TimestampDeltaPackedEncoder) WriteSlice(timestampsUs []int64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write after Finish()")
	}

	tsLen := len(timestampsUs)
	if tsLen == 0 {
		return
	}

	currentSeqCount := e.seqCount
	e.count += tsLen
	e.seqCount += tsLen

	// Conservative pre-allocation: control bytes + max 8 bytes per value + header
	e.buf.Grow(tsLen*9 + binary.MaxVarintLen64)

	prevTS := e.prevTS
	prevDelta := e.prevDelta
	startIdx := 0

	// First timestamp: full varint
	if currentSeqCount == 0 {
		ts := timestampsUs[0]
		e.appendUvarint(uint64(ts)) //nolint:gosec
		prevTS = ts
		startIdx = 1
		currentSeqCount++
	}

	// Second timestamp: zigzag+varint delta
	if startIdx < tsLen && currentSeqCount == 1 {
		ts := timestampsUs[startIdx]
		delta := ts - prevTS
		zigzag := (delta << 1) ^ (delta >> 63)
		e.appendUvarint(uint64(zigzag)) //nolint:gosec
		prevTS = ts
		prevDelta = delta
		startIdx++
	}

	// Remaining timestamps: group varint packed delta-of-deltas
	for _, ts := range timestampsUs[startIdx:] {
		delta := ts - prevTS
		deltaOfDelta := delta - prevDelta
		zigzag := uint64((deltaOfDelta << 1) ^ (deltaOfDelta >> 63)) //nolint:gosec

		e.pending[e.pendingLen] = zigzag
		e.pendingLen++

		if e.pendingLen == groupSize {
			e.flushGroup(groupSize)
		}

		prevTS = ts
		prevDelta = delta
	}

	e.prevTS = prevTS
	e.prevDelta = prevDelta
}

// Bytes returns the encoded byte slice. Any pending partial group is flushed first.
//
// Returns:
//   - []byte: Encoded byte slice
func (e *TimestampDeltaPackedEncoder) Bytes() []byte {
	if e.buf == nil {
		panic("encoder already finished - cannot access bytes after Finish()")
	}

	// Flush any partial group
	if e.pendingLen > 0 {
		e.flushGroup(e.pendingLen)
	}

	return e.buf.Bytes()
}

// Len returns the number of encoded timestamps.
//
// Returns:
//   - int: Number of timestamps written since last Finish
func (e *TimestampDeltaPackedEncoder) Len() int {
	return e.count
}

// Size returns the size in bytes of encoded timestamps.
//
// Returns:
//   - int: Total bytes written to internal buffer since last Finish
func (e *TimestampDeltaPackedEncoder) Size() int {
	if e.buf == nil {
		panic("encoder already finished - cannot access size after Finish()")
	}

	return e.buf.Len()
}

// Reset clears the encoder state, allowing reuse for a new sequence.
func (e *TimestampDeltaPackedEncoder) Reset() {
	e.prevTS = 0
	e.prevDelta = 0
	e.seqCount = 0
	e.pendingLen = 0
}

// Finish finalizes the encoding and returns buffer resources to the pool.
func (e *TimestampDeltaPackedEncoder) Finish() {
	if e.buf != nil {
		pool.PutBlobBuffer(e.buf)
		e.buf = nil
	}
	e.prevTS = 0
	e.prevDelta = 0
	e.count = 0
	e.seqCount = 0
	e.pendingLen = 0
}

// flushGroup writes n pending values as a Group Varint block.
func (e *TimestampDeltaPackedEncoder) flushGroup(n int) {
	if n == groupSize {
		tag0 := encodeTag(e.pending[0])
		tag1 := encodeTag(e.pending[1])
		tag2 := encodeTag(e.pending[2])
		tag3 := encodeTag(e.pending[3])
		controlByte := tag0 | (tag1 << 2) | (tag2 << 4) | (tag3 << 6)
		totalDataBytes := groupVarintLengths[tag0] +
			groupVarintLengths[tag1] +
			groupVarintLengths[tag2] +
			groupVarintLengths[tag3]

		startLen := len(e.buf.B)
		e.buf.Grow(1 + totalDataBytes + 8)
		e.buf.B = e.buf.B[:startLen+1+totalDataBytes+8]
		e.buf.B[startLen] = controlByte

		offset := startLen + 1
		binary.LittleEndian.PutUint64(e.buf.B[offset:offset+8], e.pending[0])
		offset += groupVarintLengths[tag0]
		binary.LittleEndian.PutUint64(e.buf.B[offset:offset+8], e.pending[1])
		offset += groupVarintLengths[tag1]
		binary.LittleEndian.PutUint64(e.buf.B[offset:offset+8], e.pending[2])
		offset += groupVarintLengths[tag2]
		binary.LittleEndian.PutUint64(e.buf.B[offset:offset+8], e.pending[3])
		offset += groupVarintLengths[tag3]

		e.buf.B = e.buf.B[:offset]
		e.pendingLen = 0

		return
	}

	var controlByte byte
	var totalDataBytes int

	// Build control byte and calculate total data size
	for i := range n {
		tag := encodeTag(e.pending[i])
		controlByte |= tag << (uint(i) * 2) //nolint:gosec // i is bounded by groupSize (0-3)
		totalDataBytes += groupVarintLengths[tag]
	}

	// Allocate: 1 control byte + data bytes + 8 bytes slack for branchless writes.
	// The branchless write always writes 8 bytes per value (PutUint64), but only
	// advances offset by the actual tag length. The extra bytes are harmlessly
	// overwritten by the next value. The slack ensures the last value's 8-byte
	// write doesn't go out of bounds.
	startLen := len(e.buf.B)
	e.buf.Grow(1 + totalDataBytes + 8)
	e.buf.B = e.buf.B[:startLen+1+totalDataBytes+8]
	e.buf.B[startLen] = controlByte

	// Branchless write: always PutUint64, advance by actual byte length per tag.
	offset := startLen + 1
	for i := range n {
		tag := (controlByte >> (uint(i) * 2)) & 0x03 //nolint:gosec // i is bounded by groupSize (0-3)
		binary.LittleEndian.PutUint64(e.buf.B[offset:offset+8], e.pending[i])
		offset += groupVarintLengths[tag]
	}

	// Trim buffer back to actual data length (remove slack)
	e.buf.B = e.buf.B[:startLen+1+totalDataBytes]

	e.pendingLen = 0
}

// appendUvarint appends a uvarint to the buffer with a fast path for single-byte values.
func (e *TimestampDeltaPackedEncoder) appendUvarint(value uint64) {
	if value <= 0x7F {
		idx := len(e.buf.B)
		e.buf.ExtendOrGrow(1)
		e.buf.B[idx] = byte(value)

		return
	}

	e.buf.Grow(binary.MaxVarintLen64)
	e.buf.B = binary.AppendUvarint(e.buf.B, value)
}

// encodeTag returns the 2-bit tag for a zigzag-encoded value based on its magnitude.
// Branchless: uses bit manipulation to determine byte-width category.
func encodeTag(v uint64) byte {
	if v <= 0xFF {
		return 0
	}

	if v <= 0xFFFF {
		return 1
	}

	if v <= 0xFFFFFFFF {
		return 2
	}

	return 3
}

// TimestampDeltaPackedDecoder provides high-performance decoding of Group Varint packed
// delta-of-delta timestamps.
type TimestampDeltaPackedDecoder struct{}

var _ encoding.ColumnarDecoder[int64] = TimestampDeltaPackedDecoder{}

// NewTimestampDeltaPackedDecoder creates a new Group Varint delta-of-delta timestamp decoder.
//
// Returns:
//   - TimestampDeltaPackedDecoder: A new decoder instance (stateless, can be reused)
func NewTimestampDeltaPackedDecoder() TimestampDeltaPackedDecoder {
	return TimestampDeltaPackedDecoder{}
}

// All returns an iterator that yields all timestamps from Group Varint packed data.
//
// The decoder reads one control byte per group of 4 values, then uses typed
// fixed-width reads (Uint16, Uint32, Uint64) to decode each value without
// the per-byte continuation-bit loop of LEB128 varint.
//
// Parameters:
//   - data: Encoded byte slice from TimestampDeltaPackedEncoder.Bytes()
//   - count: Expected number of timestamps
//
// Returns:
//   - iter.Seq[int64]: Iterator yielding decoded timestamps
func (d TimestampDeltaPackedDecoder) All(data []byte, count int) iter.Seq[int64] { //nolint:cyclop // inherent complexity of Group Varint decoding
	return func(yield func(int64) bool) {
		if len(data) == 0 || count <= 0 {
			return
		}

		// Decode first timestamp (full varint)
		first, offset, ok := decodeVarint64(data, 0)
		if !ok {
			return
		}

		curTS := int64(first) //nolint:gosec
		if !yield(curTS) {
			return
		}

		if count == 1 {
			return
		}

		// Decode second timestamp (zigzag + varint delta)
		zigzag, offset, ok := decodeVarint64(data, offset)
		if !ok {
			return
		}

		delta := decodeZigZag64(zigzag)
		curTS += delta
		if !yield(curTS) {
			return
		}

		// Remaining timestamps: Group Varint packed delta-of-deltas
		prevDelta := delta
		remaining := count - 2

		// Fast path: full groups of 4
		for remaining >= groupSize && offset < len(data) {
			cb := data[offset]
			offset++

			for i := range groupSize {
				tag := (cb >> (uint(i) * 2)) & 0x03 //nolint:gosec // i is bounded by groupSize (0-3)
				byteLen := groupVarintLengths[tag]

				if offset+byteLen > len(data) {
					return
				}

				var zz uint64
				switch tag {
				case 0:
					zz = uint64(data[offset])
				case 1:
					zz = uint64(binary.LittleEndian.Uint16(data[offset:]))
				case 2:
					zz = uint64(binary.LittleEndian.Uint32(data[offset:]))
				case 3:
					zz = binary.LittleEndian.Uint64(data[offset:])
				default:
					return
				}
				offset += byteLen

				deltaOfDelta := decodeZigZag64(zz)
				prevDelta += deltaOfDelta
				curTS += prevDelta

				if !yield(curTS) {
					return
				}
			}

			remaining -= groupSize
		}

		// Tail: partial group (< 4 values)
		if remaining > 0 && offset < len(data) {
			cb := data[offset]
			offset++

			for i := range remaining {
				tag := (cb >> (uint(i) * 2)) & 0x03 //nolint:gosec // i is bounded by groupSize (0-3)
				byteLen := groupVarintLengths[tag]

				if offset+byteLen > len(data) {
					return
				}

				var zz uint64
				switch tag {
				case 0:
					zz = uint64(data[offset])
				case 1:
					zz = uint64(binary.LittleEndian.Uint16(data[offset:]))
				case 2:
					zz = uint64(binary.LittleEndian.Uint32(data[offset:]))
				case 3:
					zz = binary.LittleEndian.Uint64(data[offset:])
				default:
					return
				}
				offset += byteLen

				deltaOfDelta := decodeZigZag64(zz)
				prevDelta += deltaOfDelta
				curTS += prevDelta

				if !yield(curTS) {
					return
				}
			}
		}
	}
}

// At returns the timestamp at the specified index.
//
// Parameters:
//   - data: Encoded byte slice from TimestampDeltaPackedEncoder.Bytes()
//   - index: Zero-based index of the timestamp to retrieve
//   - count: Total number of timestamps in the encoded data
//
// Returns:
//   - int64: The timestamp at the specified index
//   - bool: true if successfully decoded, false otherwise
func (d TimestampDeltaPackedDecoder) At(data []byte, index int, count int) (int64, bool) { //nolint:cyclop // inherent complexity of Group Varint decoding
	if index < 0 || index >= count || len(data) == 0 {
		return 0, false
	}

	// Decode first timestamp
	first, offset, ok := decodeVarint64(data, 0)
	if !ok {
		return 0, false
	}

	curTS := int64(first) //nolint:gosec
	if index == 0 {
		return curTS, true
	}

	// Decode second timestamp
	zigzag, offset, ok := decodeVarint64(data, offset)
	if !ok {
		return 0, false
	}

	delta := decodeZigZag64(zigzag)
	curTS += delta
	if index == 1 {
		return curTS, true
	}

	// Decode through Group Varint blocks until target index
	prevDelta := delta
	produced := 2
	target := index + 1

	// Process full groups that fit entirely within the target range
	for produced+groupSize <= target && offset < len(data) {
		cb := data[offset]
		offset++

		for i := range groupSize {
			tag := (cb >> (uint(i) * 2)) & 0x03 //nolint:gosec // i is bounded by groupSize (0-3)
			byteLen := groupVarintLengths[tag]
			if offset+byteLen > len(data) {
				return 0, false
			}

			var zz uint64
			switch tag {
			case 0:
				zz = uint64(data[offset])
			case 1:
				zz = uint64(binary.LittleEndian.Uint16(data[offset:]))
			case 2:
				zz = uint64(binary.LittleEndian.Uint32(data[offset:]))
			case 3:
				zz = binary.LittleEndian.Uint64(data[offset:])
			default:
				return 0, false
			}
			offset += byteLen

			deltaOfDelta := decodeZigZag64(zz)
			prevDelta += deltaOfDelta
			curTS += prevDelta
		}

		produced += groupSize
	}

	// Partial group: decode remaining values up to target
	if produced < target && offset < len(data) {
		cb := data[offset]
		offset++

		need := target - produced
		for i := range need {
			tag := (cb >> (uint(i) * 2)) & 0x03 //nolint:gosec // i is bounded by groupSize (0-3)
			byteLen := groupVarintLengths[tag]
			if offset+byteLen > len(data) {
				return 0, false
			}

			var zz uint64
			switch tag {
			case 0:
				zz = uint64(data[offset])
			case 1:
				zz = uint64(binary.LittleEndian.Uint16(data[offset:]))
			case 2:
				zz = uint64(binary.LittleEndian.Uint32(data[offset:]))
			case 3:
				zz = binary.LittleEndian.Uint64(data[offset:])
			default:
				return 0, false
			}
			offset += byteLen

			deltaOfDelta := decodeZigZag64(zz)
			prevDelta += deltaOfDelta
			curTS += prevDelta
			produced++
		}
	}

	if produced == target {
		return curTS, true
	}

	return 0, false
}
