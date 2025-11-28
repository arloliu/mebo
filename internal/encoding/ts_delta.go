package encoding

import (
	"encoding/binary"
	"iter"

	"github.com/arloliu/mebo/encoding"

	"github.com/arloliu/mebo/internal/pool"
)

// TimestampDeltaEncoder implements TimestampEncoder using delta-of-delta encoding with zigzag and varint compression.
//
// This encoder provides exceptional space savings for time-series data by:
//   - Storing the first timestamp as a full varint-encoded value
//   - Storing the second timestamp as a delta from the first
//   - Storing subsequent timestamps as delta-of-delta (difference between consecutive deltas)
//   - Using zigzag encoding to efficiently handle negative values
//   - Using varint encoding to minimize bytes for small values
//   - Using fixed buffer allocation estimates for optimal performance
//
// Typical compression characteristics:
//   - First timestamp: 5-9 bytes (varint-encoded microseconds)
//   - Second timestamp delta: 1-5 bytes (typical interval)
//   - Regular intervals: 1 byte per timestamp (delta-of-delta = 0)
//   - Semi-regular intervals: 1-2 bytes per timestamp (small delta-of-deltas)
//   - Irregular intervals: 3-5 bytes per timestamp (larger delta-of-deltas)
//   - Overall compression: 60-87% space savings vs raw encoding for regular data
//
// This encoding is optimal for:
//   - Regular interval time-series (monitoring, IoT sensors, metrics)
//   - Semi-regular intervals with small jitter (realistic time-series)
//   - Storage-constrained environments
//   - Network transmission of timestamp data
//
// Trade-offs compared to simple delta encoding:
//   - Pros: 60-75% better compression for regular intervals, no worse for irregular data
//   - Cons: Sequential decoding required, ~10-20% more CPU overhead
//
// Internal state:
//   - prevTS: Previous timestamp for delta calculation
//   - prevDelta: Previous delta for delta-of-delta calculation
//   - buf: Output buffer accumulating encoded data
//   - count: Number of timestamps encoded
type TimestampDeltaEncoder struct {
	prevTS    int64
	prevDelta int64
	buf       *pool.ByteBuffer
	count     int
	seqCount  int
}

var _ encoding.ColumnarEncoder[int64] = (*TimestampDeltaEncoder)(nil)

// NewTimestampDeltaEncoder creates a new delta-of-delta compressed timestamp encoder.
//
// Delta-of-delta encoding provides exceptional space savings for time-series data by
// storing the differences between consecutive deltas. The encoding process:
//
//  1. First timestamp: Stored as full varint-encoded microseconds (5-9 bytes)
//  2. Second timestamp: Stored as zigzag + varint encoded delta (1-9 bytes)
//  3. Remaining timestamps: Stored as zigzag + varint encoded delta-of-deltas (1-9 bytes)
//
// Compression characteristics:
//   - Regular intervals (1s, 1min): ~1 byte per timestamp (delta-of-delta = 0)
//   - Semi-regular intervals (±5% jitter): ~1-2 bytes per timestamp
//   - Irregular intervals: 3-5 bytes per timestamp (same as simple delta)
//   - Typical space savings: 60-87% vs raw encoding for regular data
//
// This encoding is optimal when:
//   - Timestamps have regular or semi-regular intervals (monitoring, metrics, IoT)
//   - Storage space or bandwidth is critical
//   - Compression ratio is more important than random access
//   - Time-series data has predictable patterns
//
// Encoding algorithm details:
//   - Delta-of-delta: current_delta - previous_delta
//   - Zigzag encoding: Maps signed values to unsigned efficiently
//   - Positive value v -> 2*v
//   - Negative value v -> 2*|v|-1
//   - Varint encoding: Uses 1-9 bytes based on magnitude
//   - Values 0-127: 1 byte (typical for regular intervals)
//   - Values 128-16383: 2 bytes
//   - Larger values: Up to 9 bytes
//
// Returns:
//   - *TimestampDeltaEncoder: A new encoder instance ready for timestamp encoding
//
// Example:
//
//	encoder := NewTimestampDeltaEncoder()
//	// Sequential timestamps with 1-second intervals
//	now := time.Now().UnixMicro()
//	timestamps := []int64{now, now + 1000000, now + 2000000} // 1-second intervals in microseconds
//	encoder.WriteSlice(timestamps)
//	data := encoder.Bytes()  // ~8 bytes total vs 24 bytes raw (67% savings)
func NewTimestampDeltaEncoder() *TimestampDeltaEncoder {
	return &TimestampDeltaEncoder{
		buf: pool.GetBlobBuffer(),
	}
}

// Write encodes a single timestamp using delta-of-delta compression with zigzag and varint format.
//
// Encoding strategy based on position:
//   - First timestamp: Full varint-encoded microseconds (5-9 bytes)
//   - Second timestamp: Delta from first, zigzag + varint encoded (1-9 bytes)
//   - Subsequent timestamps: Delta-of-delta, zigzag + varint encoded (1-9 bytes)
//
// The implementation uses branchless logic where possible to improve CPU pipeline efficiency
// and enable better compiler optimizations for inlining.
//
// Example compression for regular 1-second intervals:
//   - First timestamp: ~6 bytes
//   - Second timestamp: ~3 bytes (delta = 1000000μs)
//   - Each subsequent: ~1 byte (delta-of-delta = 0)
//   - Total for 10 timestamps: ~13 bytes vs 80 bytes raw (84% savings)
//
// Panics if Finish() has been called (nil buffer).
//
// Parameters:
//   - timestampUs: Timestamp in microseconds since Unix epoch
func (e *TimestampDeltaEncoder) Write(timestampUs int64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write after Finish()")
	}

	e.count++
	e.seqCount++

	if e.seqCount == 1 {
		// First timestamp: write full value (no zigzag, just varint)
		e.appendUnsigned(uint64(timestampUs)) //nolint:gosec
		e.prevTS = timestampUs

		return
	}

	// Calculate delta for all subsequent timestamps
	delta := timestampUs - e.prevTS

	var valToEncode int64
	if e.seqCount == 2 {
		// Second timestamp: encode delta
		valToEncode = delta
		e.prevDelta = delta
	} else {
		// Third+ timestamp: encode delta-of-delta
		valToEncode = delta - e.prevDelta
		e.prevDelta = delta
	}

	// Zigzag encode (efficient signed-to-unsigned mapping)
	zigzag := (valToEncode << 1) ^ (valToEncode >> 63)

	// Write varint with inline fast paths
	e.appendUnsigned(uint64(zigzag)) //nolint:gosec

	e.prevTS = timestampUs
}

// WriteSlice encodes a slice of timestamps using optimized delta-of-delta compression.
//
// This method provides the most efficient encoding by processing all timestamps
// in a single operation with optimal buffer management:
//
// Encoding process:
//  1. Pre-allocates buffer: ~6 bytes for first + ~3 bytes for second + ~1.5 bytes average per delta-of-delta
//  2. First timestamp: Full varint encoding (establishes baseline)
//  3. Second timestamp: Delta + zigzag + varint encoding
//  4. Remaining timestamps: Delta-of-delta + zigzag + varint encoding
//
// Buffer management:
//   - Estimated size: 6 + 3 + (len(timestampsUs)-2) * 1.5 bytes (optimistic for regular intervals)
//   - Single buffer growth operation (no repeated allocations)
//   - Uses local temp buffer to avoid allocations during varint encoding
//
// Example compression for typical time-series data:
//   - 10 timestamps at 1-second intervals: ~11 bytes vs 80 bytes raw (86% savings)
//   - 100 timestamps at 1-second intervals: ~109 bytes vs 800 bytes raw (86% savings)
//
// The encoded bytes are appended to the internal buffer and can be retrieved
// using the Bytes method.
//
// Panics if Finish() has been called (nil buffer).
//
// Parameters:
//   - timestampsUs: Slice of timestamps in microseconds since Unix epoch
func (e *TimestampDeltaEncoder) WriteSlice(timestampsUs []int64) {
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
	e.reserveFor(tsLen)

	prevTS := e.prevTS
	prevDelta := e.prevDelta
	startIdx := 0

	// Handle first timestamp if this is initial write
	if currentSeqCount == 0 {
		ts := timestampsUs[0]
		e.appendUnsigned(uint64(ts)) //nolint:gosec
		prevTS = ts
		startIdx = 1
		currentSeqCount++
	}

	// Handle second timestamp (first delta) if we have it
	if startIdx < tsLen && currentSeqCount == 1 {
		ts := timestampsUs[startIdx]
		delta := ts - prevTS
		zigzag := (delta << 1) ^ (delta >> 63)
		e.appendUnsigned(uint64(zigzag)) //nolint:gosec
		prevTS = ts
		prevDelta = delta
		startIdx++
	}

	// Encode remaining timestamps as delta-of-deltas
	for _, ts := range timestampsUs[startIdx:] {
		delta := ts - prevTS
		deltaOfDelta := delta - prevDelta
		// Zigzag encoding
		zigzag := (deltaOfDelta << 1) ^ (deltaOfDelta >> 63)
		// Varint encoding
		e.appendUnsigned(uint64(zigzag)) //nolint:gosec
		prevTS = ts
		prevDelta = delta
	}

	// Update encoder state
	e.prevTS = prevTS
	e.prevDelta = prevDelta
}

func (e *TimestampDeltaEncoder) appendUnsigned(value uint64) {
	if value <= 0x7F {
		e.appendSingleByte(byte(value))
		return
	}

	const maxLen = binary.MaxVarintLen64
	e.buf.Grow(maxLen)
	e.buf.B = binary.AppendUvarint(e.buf.B, value)
}

func (e *TimestampDeltaEncoder) appendSingleByte(b byte) {
	idx := len(e.buf.B)
	e.buf.ExtendOrGrow(1)
	e.buf.B[idx] = b
}

// reserveFor pre-allocates buffer space using fixed estimates for optimal performance.
//
// The fixed approach provides predictable performance without the overhead of
// dynamic observation and floating-point arithmetic during encoding.
func (e *TimestampDeltaEncoder) reserveFor(count int) {
	if count <= 0 || e.buf == nil {
		return
	}

	// Use fixed estimates based on typical time-series patterns:
	// - First timestamp: ~6 bytes (varint-encoded microseconds)
	// - Second timestamp: ~3 bytes (delta)
	// - Regular intervals: ~1.5 bytes average (delta-of-delta)
	// - Conservative estimate: 3 bytes per timestamp
	perEntry := 3
	if perEntry < 1 {
		perEntry = 1
	}

	// Conservative upper bound for irregular data
	const maxPerEntry = 5
	if maxPerEntry > perEntry {
		perEntry = maxPerEntry
	}

	reserve := perEntry*count + binary.MaxVarintLen64
	e.buf.Grow(reserve)
}

// Bytes returns the delta-of-delta compressed encoded byte slice containing all written timestamps.
//
// Output format (sequential binary data):
//  1. First timestamp: Varint-encoded microseconds since Unix epoch (5-9 bytes)
//  2. Second timestamp: Zigzag + varint encoded delta (1-9 bytes)
//  3. Subsequent timestamps: Zigzag + varint encoded delta-of-deltas (1-9 bytes each)
//
// The returned slice is valid until the next call to Write, WriteSlice, or Reset.
// The caller must not modify the returned slice as it references the internal buffer.
//
// Decoding requirements:
//   - Must decode sequentially from start to maintain delta-of-delta chain
//   - Cannot randomly access individual timestamps
//   - First value is full timestamp, second is delta, rest are delta-of-deltas
//
// Size characteristics:
//   - Regular intervals: 1 byte per timestamp (after first two)
//   - Semi-regular intervals: 1-2 bytes per timestamp
//   - Irregular intervals: 3-5 bytes per timestamp
//   - Minimum size: 5 bytes (single timestamp)
//   - Maximum size: 9 bytes per timestamp (for extreme variations)
//
// Panics if Finish() has been called (nil buffer).
//
// Returns:
//   - []byte: Encoded byte slice (empty if no timestamps written since last Reset)
func (e *TimestampDeltaEncoder) Bytes() []byte {
	if e.buf == nil {
		panic("encoder already finished - cannot access bytes after Finish()")
	}

	return e.buf.Bytes()
}

// Len returns the number of encoded timestamps.
//
// Returns:
//   - int: Number of timestamps written since last Finish
func (e *TimestampDeltaEncoder) Len() int {
	return e.count
}

// Size returns the size in bytes of encoded timestamps.
//
// It represents the number of bytes that were written to the internal buffer.
//
// Panics if Finish() has been called (nil buffer).
//
// Returns:
//   - int: Total bytes written to internal buffer since last Finish
func (e *TimestampDeltaEncoder) Size() int {
	if e.buf == nil {
		panic("encoder already finished - cannot access size after Finish()")
	}

	return e.buf.Len()
}

// Reset clears the delta-of-delta encoder state, allowing it to be reused for a new sequence of timestamps.
//
// This method is essential for reusing the encoder across multiple independent
// timestamp sequences, particularly in high-throughput time-series processing
// where encoder instances are pooled and reused.
//
// The length and size remain unchanged after calling Reset.
// The caller can continue to retrieve the accumulated data using Bytes(), Len(), and Size().
func (e *TimestampDeltaEncoder) Reset() {
	e.prevTS = 0
	e.prevDelta = 0
	e.seqCount = 0
}

// Finish finalizes the encoding process and returns buffer resources to the pool.
//
// After calling Finish(), the encoder is no longer usable. Any subsequent calls to
// Write(), WriteSlice(), Bytes(), or Size() will panic due to nil buffer.
//
// To encode more data, create a new encoder instance.
//
// This method must be called when the encoding session is complete to ensure buffer
// resources are properly returned to the pool for reuse by other encoders.
func (e *TimestampDeltaEncoder) Finish() {
	if e.buf != nil {
		pool.PutBlobBuffer(e.buf)
		e.buf = nil
	}
	e.prevTS = 0
	e.prevDelta = 0
	e.count = 0
	e.seqCount = 0
}

// TimestampDeltaDecoder provides high-performance decoding of delta-of-delta compressed timestamps.
//
// This decoder efficiently processes timestamps encoded by TimestampDeltaEncoder using:
//   - Direct byte slice access (no bytes.Reader overhead)
//   - Optimized binary.Uvarint operations
//   - Sequential iteration with minimal allocations
//   - Iterator pattern for memory-efficient processing
//
// The decoder expects data in the format produced by TimestampDeltaEncoder:
//  1. First timestamp: Varint-encoded microseconds since Unix epoch
//  2. Second timestamp: Zigzag + varint encoded delta
//  3. Subsequent timestamps: Zigzag + varint encoded delta-of-deltas
//
// Usage patterns:
//   - Full iteration: Process all timestamps in sequence
//   - Partial iteration: Break early when conditions are met
//   - Memory efficient: No intermediate slice allocations
//   - High throughput: Optimized for time-series data processing
type TimestampDeltaDecoder struct{}

var _ encoding.ColumnarDecoder[int64] = TimestampDeltaDecoder{}

// NewTimestampDeltaDecoder creates a new high-performance delta-of-delta timestamp decoder.
//
// The decoder is stateless and can be reused across multiple decoding operations.
// Each call to All() operates independently on the provided data.
//
// Returns:
//   - TimestampDeltaDecoder: A new decoder instance (stateless, can be reused)
func NewTimestampDeltaDecoder() TimestampDeltaDecoder {
	return TimestampDeltaDecoder{}
}

// All returns an iterator that yields all timestamps from the delta-of-delta encoded data.
//
// This method provides zero-allocation iteration over timestamps using Go's
// iter.Seq pattern. The iterator processes data sequentially, decoding each
// timestamp represented in int64 on-demand without creating intermediate slices.
//
// Decoding algorithm:
//  1. Decode first timestamp as full varint-encoded microseconds
//  2. Decode second timestamp as delta (zigzag + varint)
//  3. For remaining timestamps:
//     - Decode zigzag + varint encoded delta-of-delta
//     - Apply zigzag decoding: signed = (unsigned >> 1) ^ -(signed(unsigned & 1))
//     - Reconstruct delta: current_delta = previous_delta + delta_of_delta
//     - Add delta to previous timestamp
//
// Error handling:
//   - Invalid varint encoding: Iterator stops early
//   - Insufficient data: Iterator stops at actual data end
//   - Count mismatch: Iterator continues until data exhausted
//
// Parameters:
//   - data: Delta-of-delta encoded byte slice from TimestampDeltaEncoder.Bytes()
//   - count: Expected number of timestamps (used for optimization and validation)
//
// Returns:
//   - iter.Seq[int64]: Iterator yielding decoded timestamps (microseconds since Unix epoch)
//
// Example:
//
//	decoder := NewTimestampDeltaDecoder()
//	for ts := range decoder.All(encodedData, expectedCount) {
//	    // Process each timestamp
//	    fmt.Printf("Timestamp: %v\n", time.UnixMicro(ts)) // assume timestamp is unix microseconds
//	    // Can break early if needed
//	    if someCondition {
//	        break
//	    }
//	}
func (d TimestampDeltaDecoder) All(data []byte, count int) iter.Seq[int64] {
	return func(yield func(int64) bool) {
		if len(data) == 0 || count <= 0 {
			return
		}

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

		zigzag, offset, ok := decodeVarint64(data, offset)
		if !ok {
			return
		}

		delta := decodeZigZag64(zigzag)
		curTS += delta
		if !yield(curTS) {
			return
		}

		prevDelta := delta
		for produced := 2; produced < count; produced++ {
			deltaZigzag, nextOffset, ok := decodeVarint64(data, offset)
			if !ok {
				return
			}
			offset = nextOffset

			deltaOfDelta := decodeZigZag64(deltaZigzag)
			prevDelta += deltaOfDelta
			curTS += prevDelta

			if !yield(curTS) {
				return
			}
		}
	}
}

// At returns the timestamp(as int64) at the specified index in the delta-of-delta encoded data.
//
// This method provides efficient random access to timestamps by decoding only
// up to the target index, avoiding full iteration when possible.
//
// Parameters:
//   - data: Delta-of-delta encoded byte slice from TimestampDeltaEncoder.Bytes()
//   - index: Zero-based index of the timestamp to retrieve
//
// Returns:
//   - int64: The timestamp integer at the specified index, the unit of timestamp is user-defined
//   - bool: true if the index exists and was successfully decoded, false otherwise
//
// Error conditions:
//   - Negative index: Returns zero time and false
//   - Index beyond available data: Returns zero time and false
//   - Invalid varint encoding: Returns zero time and false
//   - Empty data: Returns zero time and false
//
// Example:
//
//	decoder := NewTimestampDeltaDecoder()
//	timestamp, ok := decoder.At(encodedData, 5)
//	if ok {
//	    fmt.Printf("Timestamp at index 5: %v\n", timestamp)
//	} else {
//	    fmt.Println("Index 5 not found or invalid data")
//	}
//
// Note: For sequential access of multiple timestamps, use All() iterator which
// is more efficient than multiple At() calls.
func (d TimestampDeltaDecoder) At(data []byte, index int, count int) (int64, bool) {
	if index < 0 || index >= count || len(data) == 0 {
		return 0, false
	}

	first, offset, ok := decodeVarint64(data, 0)
	if !ok {
		return 0, false
	}

	curTS := int64(first) //nolint:gosec
	if index == 0 {
		return curTS, true
	}

	zigzag, offset, ok := decodeVarint64(data, offset)
	if !ok {
		return 0, false
	}

	delta := decodeZigZag64(zigzag)
	curTS += delta
	if index == 1 {
		return curTS, true
	}

	prevDelta := delta

	for i := 2; i <= index; i++ {
		deltaZigzag, nextOffset, ok := decodeVarint64(data, offset)
		if !ok {
			return 0, false
		}
		offset = nextOffset

		deltaOfDelta := decodeZigZag64(deltaZigzag)
		prevDelta += deltaOfDelta
		curTS += prevDelta
	}

	return curTS, true
}

// decodeVarint64 decodes a uint64 varint from data starting at offset.
//
// This function handles provides a fast path for single-byte varints
// and falls back to binary.Uvarint for larger values.
//
// Parameters:
//   - data: Byte slice containing the varint-encoded data
//   - offset: Starting index within data to decode from
//
// Returns:
//   - uint64: The decoded unsigned integer value
//   - int: The new offset after reading the varint
//   - bool: true if decoding was successful, false otherwise

func decodeVarint64(data []byte, offset int) (uint64, int, bool) {
	if offset >= len(data) {
		return 0, offset, false
	}

	cur := offset
	b0 := data[cur]
	cur++
	if b0 < 0x80 {
		return uint64(b0), cur, true
	}

	if cur >= len(data) {
		return 0, offset, false
	}

	b1 := data[cur]
	cur++
	value := uint64(b0&0x7f) | uint64(b1&0x7f)<<7
	if b1 < 0x80 {
		return value, cur, true
	}

	shift := uint(14)
	for i := 2; i < binary.MaxVarintLen64; i++ {
		if cur >= len(data) {
			return 0, offset, false
		}

		b := data[cur]
		cur++
		value |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return value, cur, true
		}
		shift += 7
	}

	return 0, offset, false
}

// decodeZigZag64 reverses zigzag encoding using branchless bit operations.
func decodeZigZag64(value uint64) int64 {
	return int64((value >> 1) ^ -(value & 1)) //nolint:gosec
}
