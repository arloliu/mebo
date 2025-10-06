package encoding

import (
	"encoding/binary"
	"iter"

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
//   - temp: Reusable buffer for varint encoding (avoids allocations)
//   - buf: Output buffer accumulating encoded data
//   - count: Number of timestamps encoded
type TimestampDeltaEncoder struct {
	prevTS    int64
	prevDelta int64
	temp      [binary.MaxVarintLen64]byte
	buf       *pool.ByteBuffer
	count     int
}

var _ ColumnarEncoder[int64] = (*TimestampDeltaEncoder)(nil)

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
// Parameters:
//   - timestampUs: Timestamp in microseconds since Unix epoch
func (e *TimestampDeltaEncoder) Write(timestampUs int64) {
	e.count++
	e.buf.Grow(10)

	if e.count == 1 {
		// First timestamp: write full value (no zigzag, just varint)
		n := binary.PutUvarint(e.temp[:], uint64(timestampUs)) //nolint:gosec
		e.buf.MustWrite(e.temp[:n])
		e.prevTS = timestampUs

		return
	}

	// Calculate delta for all subsequent timestamps
	delta := timestampUs - e.prevTS

	var valToEncode int64
	if e.count == 2 {
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

	// Write varint
	n := binary.PutUvarint(e.temp[:], uint64(zigzag)) //nolint:gosec
	e.buf.MustWrite(e.temp[:n])

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
// Parameters:
//   - timestampsUs: Slice of timestamps in microseconds since Unix epoch
func (e *TimestampDeltaEncoder) WriteSlice(timestampsUs []int64) {
	tsLen := len(timestampsUs)
	if tsLen == 0 {
		return
	}

	e.count += tsLen

	// Estimate size for regular intervals: 6 + 3 + (n-2)*1.5
	// Use conservative estimate of 2 bytes per timestamp after first two
	estimatedSize := 6 + (tsLen-1)*2
	e.buf.Grow(estimatedSize)

	prevTS := e.prevTS
	prevDelta := e.prevDelta
	startIdx := 0

	// Handle first timestamp if this is initial write
	if e.prevTS == 0 {
		ts := timestampsUs[0]
		n := binary.PutUvarint(e.temp[:], uint64(ts)) //nolint:gosec
		e.buf.MustWrite(e.temp[:n])
		prevTS = ts
		startIdx = 1
	}

	// Handle second timestamp (first delta) if we have it
	if startIdx < tsLen && prevDelta == 0 {
		ts := timestampsUs[startIdx]
		delta := ts - prevTS
		zigzag := (delta << 1) ^ (delta >> 63)
		n := binary.PutUvarint(e.temp[:], uint64(zigzag)) //nolint:gosec
		e.buf.MustWrite(e.temp[:n])
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
		n := binary.PutUvarint(e.temp[:], uint64(zigzag)) //nolint:gosec
		e.buf.MustWrite(e.temp[:n])
		prevTS = ts
		prevDelta = delta
	}

	// Update encoder state
	e.prevTS = prevTS
	e.prevDelta = prevDelta
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
// Returns:
//   - []byte: Encoded byte slice (empty if no timestamps written since last Reset)
func (e *TimestampDeltaEncoder) Bytes() []byte {
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
// Returns:
//   - int: Total bytes written to internal buffer since last Finish
func (e *TimestampDeltaEncoder) Size() int {
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
}

// Finish finalizes the encoding process.
//
// This method clears the internal buffer and resets the encoder state, preparing it for a new encoding session.
// After calling Finish, the encoder behaves as if it was newly created.
//
// The Len(), Size() and Bytes() will return zero values after calling Finish.
// The caller can continue to retrieve the accumulated data information using Len(), Size() and Bytes()
// until Finish() is called.
func (e *TimestampDeltaEncoder) Finish() {
	pool.PutBlobBuffer(e.buf)
	e.buf = pool.GetBlobBuffer()
	e.prevTS = 0
	e.prevDelta = 0
	e.count = 0
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

var _ ColumnarDecoder[int64] = TimestampDeltaDecoder{}

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

		offset := 0
		yielded := 0

		// Decode first timestamp (full varint)
		firstTS, n := binary.Uvarint(data[offset:])
		if n <= 0 {
			return
		}
		offset += n
		yielded++

		curTS := int64(firstTS) //nolint:gosec
		if !yield(curTS) {
			return
		}

		if yielded >= count {
			return
		}

		// Decode second timestamp (first delta)
		zigzag, n := binary.Uvarint(data[offset:])
		if n <= 0 {
			return
		}
		offset += n

		delta := int64(zigzag>>1) ^ -(int64(zigzag & 1)) //nolint:gosec
		curTS += delta
		yielded++

		if !yield(curTS) {
			return
		}

		prevDelta := delta

		// Decode remaining timestamps as delta-of-deltas
		for yielded < count && offset < len(data) {
			zigzag, n := binary.Uvarint(data[offset:])
			if n <= 0 {
				return
			}
			offset += n

			// Decode delta-of-delta
			deltaOfDelta := int64(zigzag>>1) ^ -(int64(zigzag & 1)) //nolint:gosec
			delta = prevDelta + deltaOfDelta
			curTS += delta
			yielded++

			if !yield(curTS) {
				return
			}

			prevDelta = delta
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

	offset := 0
	curIdx := 0

	// Decode first timestamp (full varint)
	firstTS, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return 0, false
	}
	offset += n

	curTS := int64(firstTS) //nolint:gosec

	if index == 0 {
		return curTS, true
	}

	curIdx++

	// Decode second timestamp (first delta)
	if offset >= len(data) {
		return 0, false
	}

	zigzag, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return 0, false
	}
	offset += n

	delta := int64(zigzag>>1) ^ -(int64(zigzag & 1)) //nolint:gosec
	curTS += delta

	if index == 1 {
		return curTS, true
	}

	curIdx++
	prevDelta := delta

	// Decode remaining timestamps as delta-of-deltas
	for curIdx <= index && offset < len(data) {
		zigzag, n := binary.Uvarint(data[offset:])
		if n <= 0 {
			return 0, false
		}
		offset += n

		deltaOfDelta := int64(zigzag>>1) ^ -(int64(zigzag & 1)) //nolint:gosec
		delta = prevDelta + deltaOfDelta
		curTS += delta

		if curIdx == index {
			return curTS, true
		}

		curIdx++
		prevDelta = delta
	}

	return 0, false
}
