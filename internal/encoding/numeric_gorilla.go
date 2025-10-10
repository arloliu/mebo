package encoding

import (
	"encoding/binary"
	"iter"
	"math"
	"math/bits"

	"github.com/arloliu/mebo/encoding"
	"github.com/arloliu/mebo/internal/pool"
)

const (
	gorillaSmallSequenceThreshold = 64
)

// NumericGorillaEncoder implements Facebook's Gorilla compression algorithm for float64 time-series values.
//
// The algorithm uses XOR-based compression with leading/trailing zero optimization:
//  1. Store the first value uncompressed (64 bits)
//  2. For subsequent values:
//     - XOR with previous value
//     - If XOR is 0 (value unchanged): store 1 bit (0)
//     - If XOR is non-zero:
//     a. Store control bit (1)
//     b. Calculate leading/trailing zeros
//     c. If same as previous block: store 1 bit (0) + meaningful bits
//     d. If different block: store 1 bit (1) + 5 bits (leading) + 6 bits (length) + meaningful bits
//
// This achieves excellent compression for typical time-series data where consecutive values
// are similar, resulting in many leading and trailing zeros in the XOR result.
//
// See https://www.vldb.org/pvldb/vol8/p1816-teller.pdf for algorithm details.
type NumericGorillaEncoder struct {
	// Hot path fields (frequently accessed, keep together for cache locality)
	bitBuf        uint64 // Bit buffer for accumulating bits before writing to byte buffer
	prevValue     uint64 // Previous value (as uint64 bits)
	bitCount      int    // Number of valid bits in bitBuf
	count         int    // Number of values encoded
	prevLeading   int    // Leading zeros in previous XOR
	prevTrailing  int    // Trailing zeros in previous XOR
	prevBlockSize int    // Cached block size: 64 - prevLeading - prevTrailing (performance optimization)
	firstValue    bool   // True if this is the first value

	// Offset: 64, cold path field, place one cache line away to improve cache locality
	buf *pool.ByteBuffer // Byte buffer for storing encoded data
}

var _ encoding.ColumnarEncoder[float64] = (*NumericGorillaEncoder)(nil)

// NewNumericGorillaEncoder creates a new Gorilla encoder for float64 values.
//
// The encoder uses a bit-level buffer to achieve optimal compression by storing
// only the meaningful bits of each value based on XOR compression.
//
// Memory efficiency:
//   - First value: 64 bits (uncompressed)
//   - Unchanged values: 1 bit
//   - Same block: 2 bits + meaningful bits (typically 12-20 bits)
//   - Different block: 2 + 5 + 6 + meaningful bits (typically 15-30 bits)
//
// This results in significant space savings for typical time-series data where
// consecutive values are similar.
//
// Returns:
//   - *NumericGorillaEncoder: A new encoder instance ready for float64 encoding
func NewNumericGorillaEncoder() *NumericGorillaEncoder {
	return &NumericGorillaEncoder{
		buf:        pool.GetBlobBuffer(),
		firstValue: true,
	}
}

// Write encodes a single float64 value using Gorilla compression.
//
// The encoding process:
//  1. First value: store as-is (64 bits)
//  2. Subsequent values: XOR with previous, compress using leading/trailing zero optimization
//
// This method uses bit-level operations for maximum compression efficiency.
// The compressed bits are accumulated in an internal bit buffer and flushed
// to the byte buffer when it fills up.
//
// Parameters:
//   - val: The float64 value to encode
func (e *NumericGorillaEncoder) Write(val float64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write values after Finish()")
	}

	e.count++
	valBits := math.Float64bits(val)

	if e.firstValue {
		e.firstValue = false
		e.prevValue = valBits
		// Write first value uncompressed
		e.writeBits(valBits, 64)

		return
	}

	e.writeValue(valBits)
}

// WriteSlice encodes a slice of float64 values using Gorilla compression.
//
// This method processes values sequentially, applying the same compression
// algorithm as Write but optimized for bulk operations.
//
// Parameters:
//   - values: Slice of float64 values to encode
func (e *NumericGorillaEncoder) WriteSlice(values []float64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write values after Finish()")
	}

	if len(values) == 0 {
		return
	}

	// Process first value
	if e.firstValue {
		e.count++
		valBits := math.Float64bits(values[0])
		e.firstValue = false
		e.prevValue = valBits
		e.writeBits(valBits, 64)
		values = values[1:]
	}

	// Batch process: detect runs of identical values
	i := 0
	for i < len(values) {
		valBits := math.Float64bits(values[i])

		// Look ahead for identical values
		j := i + 1
		for j < len(values) && math.Float64bits(values[j]) == valBits {
			j++
		}

		runLength := j - i
		if runLength > 1 && valBits == e.prevValue {
			// Optimize: write multiple 0 bits at once for unchanged values
			e.writeMultipleZeroBits(runLength)
			e.count += runLength
			i = j
		} else {
			// Normal encoding for single value or changed value
			e.count++
			e.writeValue(valBits)
			i++
		}
	}
}

func (e *NumericGorillaEncoder) writeMultipleZeroBits(count int) {
	// Write multiple 0 bits efficiently
	for count > 0 {
		bitsToWrite := count
		if bitsToWrite > 64 {
			bitsToWrite = 64
		}
		e.writeBits(0, bitsToWrite)
		count -= bitsToWrite
	}
}

// Bytes returns the encoded byte slice containing all compressed values.
//
// The returned slice is valid until the next call to Write, WriteSlice, Reset, or Finish.
// The caller must not modify the returned slice as it references the internal buffer.
//
// This method automatically flushes any pending bits in the bit buffer to ensure
// all encoded data is included. However, calling Bytes() multiple times without
// writing new data will not cause additional flushes (the bits are already flushed).
//
// Returns:
//   - []byte: Gorilla-compressed byte slice (empty if no values written since last Reset)
func (e *NumericGorillaEncoder) Bytes() []byte {
	if e.buf == nil {
		panic("encoder already finished - cannot access bytes after Finish()")
	}

	// Flush pending bits to ensure we return complete data
	// Note: flushBits() has a guard to prevent flushing when bitCount == 0
	if e.bitCount > 0 {
		e.flushBits()
	}

	return e.buf.Bytes()
}

// Len returns the number of encoded float64 values.
//
// Returns:
//   - int: Number of float64 values written since last Finish
func (e *NumericGorillaEncoder) Len() int {
	return e.count
}

// Size returns the size in bytes of the encoded data.
//
// Note: This returns the size of data that has been flushed to the byte buffer.
// Pending bits in the bit buffer are not included. Use Finish() to ensure all
// bits are written before checking the final size.
//
// Returns:
//   - int: Total bytes written to internal buffer since last Finish
func (e *NumericGorillaEncoder) Size() int {
	if e.buf == nil {
		panic("encoder already finished - cannot access size after Finish()")
	}

	return e.buf.Len()
}

// Reset clears the encoder state for reuse while retaining accumulated data.
//
// This allows encoding multiple sequences of values into the same buffer
// without losing previously encoded data.
func (e *NumericGorillaEncoder) Reset() {
	e.bitBuf = 0
	e.bitCount = 0
	e.prevValue = 0
	e.prevLeading = 0
	e.prevTrailing = 0
	e.prevBlockSize = 0
	e.firstValue = true
}

// Finish finalizes the encoding process and returns the buffer to the pool.
//
// This method:
//  1. Returns the byte buffer to the pool
//  2. Sets the buffer to nil, making the encoder unusable
//
// IMPORTANT: This encoder becomes single-use after calling Finish().
// Any subsequent calls to Write(), WriteSlice(), Bytes(), or Size() will panic.
// Create a new encoder if you need to encode more data.
//
// The caller should retrieve the encoded data using Bytes() BEFORE calling Finish(),
// as the buffer will be returned to the pool and the encoder will become unusable.
func (e *NumericGorillaEncoder) Finish() {
	if e.buf == nil {
		return // Already finished
	}

	// Return buffer to pool
	pool.PutBlobBuffer(e.buf)
	e.buf = nil
}

// writeValue encodes a value using XOR compression with leading/trailing zero optimization.
//
// Algorithm:
//  1. XOR current value with previous value
//  2. If XOR is 0: write control bit 0 (value unchanged)
//  3. If XOR is non-zero:
//     a. Write control bit 1
//     b. Calculate leading and trailing zeros
//     c. If block matches previous: write control bit 0 + meaningful bits
//     d. If block different: write control bit 1 + 5-bit leading + 6-bit length + meaningful bits
func (e *NumericGorillaEncoder) writeValue(valBits uint64) {
	xor := valBits ^ e.prevValue
	e.prevValue = valBits

	if xor == 0 {
		// Fast path: Value unchanged - inline the critical operation
		// This eliminates function call overhead for the most common case in constant data
		e.bitBuf = (e.bitBuf << 1) // No OR needed (bit is 0)
		e.bitCount++
		if e.bitCount == 64 {
			e.flushBits()
		}

		return
	}

	// Value changed: write control bit 1
	e.writeBit(1)

	// Calculate leading and trailing zeros
	leading := bits.LeadingZeros64(xor)
	trailing := bits.TrailingZeros64(xor)

	// Gorilla format limitation: leading zeros are encoded in 5 bits (0-31)
	// If we have more than 31 leading zeros, we need to adjust to stay within bounds
	if leading > 31 {
		// Clamp leading to 31 and adjust trailing to maintain the same meaningful bits
		adjustment := leading - 31
		leading = 31
		trailing -= adjustment
		if trailing < 0 {
			trailing = 0
		}
	}

	// Check if we can reuse the previous block
	// Note: count > 2 because count was already incremented, so:
	//   - count == 1: first value (stored uncompressed, not here)
	//   - count == 2: second value (first XOR, no previous block to reuse)
	//   - count > 2: can potentially reuse previous block
	// We also need prevBlockSize > 0 to ensure we have valid previous block info
	if e.count > 2 && e.prevBlockSize > 0 && leading >= e.prevLeading && trailing >= e.prevTrailing {
		// Same block: write control bit 0 + meaningful bits
		e.writeBit(0)
		// Use cached prevBlockSize instead of recomputing (performance optimization)
		e.writeBits(xor>>e.prevTrailing, e.prevBlockSize)
	} else {
		// Different block: write control bit 1 + block info + meaningful bits
		blockSize := 64 - leading - trailing
		e.writeBit(1)

		// 5 bits for leading zeros (0-31)
		e.write5Bits(uint64(leading)) //nolint:gosec // G115: leading is always 0-31, safe conversion
		// 6 bits for block size (1-64, encoded as 0-63)
		e.write6Bits(uint64(blockSize - 1)) //nolint:gosec // G115: blockSize-1 is always 0-63, safe conversion
		// meaningful bits
		e.writeBits(xor>>trailing, blockSize)

		e.prevLeading = leading
		e.prevTrailing = trailing
		e.prevBlockSize = blockSize // Cache block size for next iteration
	}
}

// writeBit writes a single bit to the bit buffer.
//
// This is a performance-critical function used by writeValue.
// It accumulates bits in a 64-bit buffer and flushes to the byte buffer
// when full (every 8 bits).
//
// The bit is stored in the most significant position and shifted left
// as more bits are added, ensuring correct byte-order when flushed.
func (e *NumericGorillaEncoder) writeBit(bit uint64) {
	e.bitBuf = (e.bitBuf << 1) | bit
	e.bitCount++

	if e.bitCount == 64 {
		e.flushBits()
	}
}

// writeBits writes multiple bits to the bit buffer.
//
// This is a performance-critical function used extensively in compression.
// It efficiently handles writing 1-64 bits at once, automatically flushing
// the bit buffer to the byte buffer when necessary.
//
// Parameters:
//   - value: the bits to write (only the least significant 'numBits' are used)
//   - numBits: number of bits to write (1-64)
func (e *NumericGorillaEncoder) writeBits(value uint64, numBits int) {
	if numBits == 0 {
		return
	}

	// Mask value to only include the specified number of bits
	if numBits < 64 {
		value &= (1 << numBits) - 1
	}

	// Calculate how many bits fit in current buffer
	available := 64 - e.bitCount

	if numBits <= available {
		// All bits fit in current buffer
		e.bitBuf = (e.bitBuf << numBits) | value
		e.bitCount += numBits

		if e.bitCount == 64 {
			e.flushBits()
		}
	} else {
		// Split across buffer boundary
		// Write high bits that fit in current buffer
		highBits := numBits - available
		e.bitBuf = (e.bitBuf << available) | (value >> highBits)
		e.bitCount = 64
		e.flushBits()

		// Write remaining low bits to new buffer
		e.bitBuf = value & ((1 << highBits) - 1)
		e.bitCount = highBits
	}
}

// write5Bits writes exactly 5 bits, handling buffer boundary splits.
func (e *NumericGorillaEncoder) write5Bits(value uint64) {
	value &= 0x1F // Mask to 5 bits
	available := 64 - e.bitCount
	if available >= 5 {
		// Fast path: fits in current buffer
		e.bitBuf = (e.bitBuf << 5) | value
		e.bitCount += 5
		if e.bitCount >= 64 {
			e.flushBits()
		}
	} else {
		// Slow path: split across boundary
		highBits := 5 - available
		e.bitBuf = (e.bitBuf << available) | (value >> highBits)
		e.bitCount = 64
		e.flushBits()

		e.bitBuf = value & ((1 << highBits) - 1)
		e.bitCount = highBits
	}
}

// write6Bits writes exactly 6 bits, handling buffer boundary splits.
func (e *NumericGorillaEncoder) write6Bits(value uint64) {
	value &= 0x3F // Mask to 6 bits
	available := 64 - e.bitCount
	if available >= 6 {
		// Fast path: fits in current buffer
		e.bitBuf = (e.bitBuf << 6) | value
		e.bitCount += 6
		if e.bitCount >= 64 {
			e.flushBits()
		}
	} else {
		// Slow path: split across boundary
		highBits := 6 - available
		e.bitBuf = (e.bitBuf << available) | (value >> highBits)
		e.bitCount = 64
		e.flushBits()

		e.bitBuf = value & ((1 << highBits) - 1)
		e.bitCount = highBits
	}
}

// flushBits writes the current bit buffer to the byte buffer.
//
// This converts accumulated bits into bytes and appends them to the byte buffer.
// The bit buffer is organized as big-endian (most significant bits first).
func (e *NumericGorillaEncoder) flushBits() {
	if e.bitCount == 0 {
		return
	}

	// Calculate how many bytes we need
	numBytes := (e.bitCount + 7) / 8

	// Ensure buffer has capacity
	e.buf.Grow(numBytes)

	// Shift bits to align to byte boundary (left-align)
	alignedBits := e.bitBuf << (64 - e.bitCount)

	// Write bytes in big-endian order (most significant byte first)
	startLen := e.buf.Len()
	e.buf.ExtendOrGrow(numBytes)

	bs := e.buf.Slice(startLen, startLen+numBytes)

	// Fast path: use binary.BigEndian for 8-byte writes
	if numBytes == 8 {
		binary.BigEndian.PutUint64(bs, alignedBits)
	} else {
		// Slow path: write partial bytes
		for i := range numBytes {
			shift := 56 - (i * 8)
			bs[i] = byte(alignedBits >> shift)
		}
	}

	// Clear bit buffer
	e.bitBuf = 0
	e.bitCount = 0
}

// NumericGorillaDecoder decodes float64 values compressed with the Gorilla algorithm.
//
// The decoder reads the bit-compressed data and reconstructs the original float64 values
// by reversing the XOR compression with leading/trailing zero optimization.
//
// This decoder is stateless and can be used concurrently for different data streams.
type NumericGorillaDecoder struct{}

var _ encoding.ColumnarDecoder[float64] = NumericGorillaDecoder{}

// NewNumericGorillaDecoder creates a new Gorilla decoder for float64 values.
//
// The decoder is stateless and returned by value for optimal performance:
//   - Zero heap allocations (stack-only, no GC pressure)
//   - Small struct fits in CPU registers
//   - Can be used concurrently for different data streams
//
// Returns:
//   - NumericGorillaDecoder: A new decoder instance (stateless, can be reused)
func NewNumericGorillaDecoder() NumericGorillaDecoder {
	return NumericGorillaDecoder{}
}

// gorillaBlockState caches block metadata to support Gorilla decoder reuse logic.
//
// It tracks the leading/trailing zero counts and block size from the previous
// block, allowing subsequent values to reuse the same bit window without
// re-reading header metadata from the stream.
type gorillaBlockState struct {
	trailing  int
	blockSize int
	valid     bool
}

// next reads the block metadata for a changed Gorilla value.
// It returns the block parameters and updates the cached state when a new block
// definition is encountered.
func (s *gorillaBlockState) next(br *bitReader) (trailing int, blockSize int, ok bool) {
	blockControlBit, ok := br.readBit()
	if !ok {
		return 0, 0, false
	}

	if blockControlBit == 0 {
		if !s.valid {
			return 0, 0, false
		}

		return s.trailing, s.blockSize, true
	}

	leading, ok := br.read5Bits()
	if !ok {
		return 0, 0, false
	}

	blockSize, ok = br.read6Bits()
	if !ok {
		return 0, 0, false
	}
	blockSize++
	if blockSize < 1 || blockSize > 64 {
		return 0, 0, false
	}

	trailing = 64 - leading - blockSize
	if trailing < 0 || trailing > 64 {
		return 0, 0, false
	}

	s.trailing = trailing
	s.blockSize = blockSize
	s.valid = true

	return trailing, blockSize, true
}

// All decodes all float64 values from the Gorilla-compressed byte slice.
//
// The decoder reads the first value uncompressed, then decodes subsequent values
// using XOR decompression with leading/trailing zero optimization.
//
// Parameters:
//   - data: byte slice containing Gorilla-compressed float64 values
//   - count: expected number of values to decode
//
// Returns:
//   - iter.Seq[float64]: Iterator yielding decoded float64 values
//
// If the data is malformed or insufficient, the iterator may yield fewer values.
func (d NumericGorillaDecoder) All(data []byte, count int) iter.Seq[float64] {
	return func(yield func(float64) bool) {
		if len(data) == 0 || count == 0 {
			return
		}

		if len(data) >= 64 {
			_ = data[63]
		}

		br := newBitReader(data)

		// Read first value (uncompressed)
		firstBits, ok := br.readBits(64)
		if !ok {
			return
		}
		prevValue := firstBits
		prevFloat := math.Float64frombits(prevValue)
		if !yield(prevFloat) {
			return
		}

		if count == 1 {
			return
		}

		remaining := count - 1
		if remaining <= gorillaSmallSequenceThreshold {
			d.decodeAllSmall(br, prevValue, prevFloat, remaining, yield)
			return
		}

		d.decodeAllLarge(br, prevValue, prevFloat, remaining, yield)
	}
}

func (NumericGorillaDecoder) decodeAllSmall(br *bitReader, prevValue uint64, prevFloat float64, remaining int, yield func(float64) bool) {
	trailing := 0
	blockSize := 0
	blockValid := false

	for remaining > 0 {
		controlBit, ok := br.readBit()
		if !ok {
			return
		}

		if controlBit == 0 {
			if !yield(prevFloat) {
				return
			}
			remaining--

			continue
		}

		reuseBit, ok := br.readBit()
		if !ok {
			return
		}

		var trailingBits, blockSizeBits int
		if reuseBit == 0 {
			if !blockValid {
				return
			}
			trailingBits = trailing
			blockSizeBits = blockSize
		} else {
			leading, ok := br.read5Bits()
			if !ok {
				return
			}
			sizeBits, ok := br.read6Bits()
			if !ok {
				return
			}
			blockSizeBits = sizeBits + 1
			if blockSizeBits < 1 || blockSizeBits > 64 {
				return
			}
			trailingBits = 64 - leading - blockSizeBits
			if trailingBits < 0 || trailingBits > 64 {
				return
			}

			trailing = trailingBits
			blockSize = blockSizeBits
			blockValid = true
		}

		meaningful, ok := br.readBits(blockSizeBits)
		if !ok {
			return
		}

		shift := uint64(trailingBits) // #nosec G115 -- trailingBits constrained to [0,64]
		prevValue ^= meaningful << shift
		prevFloat = math.Float64frombits(prevValue)
		if !yield(prevFloat) {
			return
		}
		remaining--
	}
}

func (NumericGorillaDecoder) decodeAllLarge(br *bitReader, prevValue uint64, prevFloat float64, remaining int, yield func(float64) bool) {
	if remaining <= 0 {
		return
	}

	state := gorillaBlockState{}
	produced := 0

	for produced < remaining {
		controlBit, ok := br.readBit()
		if !ok {
			return
		}

		if controlBit == 0 {
			if !yield(prevFloat) {
				return
			}
			produced++

			for produced < remaining {
				controlBit, ok = br.readBit()
				if !ok {
					return
				}
				if controlBit != 0 {
					break
				}

				if !yield(prevFloat) {
					return
				}
				produced++
			}

			if produced >= remaining {
				return
			}
		}

		trailing, blockSize, ok := state.next(br)
		if !ok {
			return
		}

		meaningfulBits, ok := br.readBits(blockSize)
		if !ok {
			return
		}

		shift := uint64(trailing) // #nosec G115 -- trailing validated by gorillaBlockState
		prevValue ^= meaningfulBits << shift
		prevFloat = math.Float64frombits(prevValue)
		if !yield(prevFloat) {
			return
		}
		produced++
	}
}

func (NumericGorillaDecoder) decodeAtSmall(br *bitReader, prevValue uint64, target int) (float64, bool) {
	trailing := 0
	blockSize := 0
	blockValid := false
	prevFloat := math.Float64frombits(prevValue)

	for current := 1; current <= target; {
		controlBit, ok := br.readBit()
		if !ok {
			return 0, false
		}

		if controlBit == 0 {
			if current == target {
				return prevFloat, true
			}
			current++

			continue
		}

		reuseBit, ok := br.readBit()
		if !ok {
			return 0, false
		}

		var trailingBits, blockSizeBits int
		if reuseBit == 0 {
			if !blockValid {
				return 0, false
			}
			trailingBits = trailing
			blockSizeBits = blockSize
		} else {
			leading, ok := br.read5Bits()
			if !ok {
				return 0, false
			}
			sizeBits, ok := br.read6Bits()
			if !ok {
				return 0, false
			}
			blockSizeBits = sizeBits + 1
			if blockSizeBits < 1 || blockSizeBits > 64 {
				return 0, false
			}
			trailingBits = 64 - leading - blockSizeBits
			if trailingBits < 0 || trailingBits > 64 {
				return 0, false
			}

			trailing = trailingBits
			blockSize = blockSizeBits
			blockValid = true
		}

		meaningful, ok := br.readBits(blockSizeBits)
		if !ok {
			return 0, false
		}

		shift := uint64(trailingBits) // #nosec G115 -- trailingBits constrained to [0,64]
		prevValue ^= meaningful << shift
		prevFloat = math.Float64frombits(prevValue)
		if current == target {
			return prevFloat, true
		}
		current++
	}

	return 0, false
}

// At retrieves the float64 value at the specified index from the Gorilla-compressed data.
//
// This method decodes values sequentially up to the requested index.
// For random access to multiple indices, consider using All() and caching results.
//
// Parameters:
//   - data: byte slice containing Gorilla-compressed float64 values
//   - index: zero-based index of the value to retrieve
//   - count: total number of values encoded in the data
//
// Returns:
//   - The decoded float64 value and true if successful
//   - Zero value and false if index is out of bounds or data is malformed
func (d NumericGorillaDecoder) At(data []byte, index int, count int) (float64, bool) {
	if len(data) == 0 || index < 0 || index >= count {
		return 0, false
	}

	br := newBitReader(data)

	// Read first value (uncompressed)
	firstBits, ok := br.readBits(64)
	if !ok {
		return 0, false
	}

	prevValue := firstBits
	prevFloat := math.Float64frombits(prevValue)
	if index == 0 {
		return prevFloat, true
	}
	remaining := index
	if remaining <= gorillaSmallSequenceThreshold {
		return d.decodeAtSmall(br, prevValue, remaining)
	}

	state := gorillaBlockState{}

	for current := 1; current <= index; {
		controlBit, ok := br.readBit()
		if !ok {
			return 0, false
		}

		if controlBit == 0 {
			if current == index {
				return prevFloat, true
			}
			current++

			for current <= index {
				controlBit, ok = br.readBit()
				if !ok {
					return 0, false
				}
				if controlBit != 0 {
					break
				}
				if current == index {
					return prevFloat, true
				}
				current++
			}

			if controlBit == 0 {
				return 0, false
			}
		}

		trailing, blockSize, ok := state.next(br)
		if !ok {
			return 0, false
		}

		meaningfulBits, ok := br.readBits(blockSize)
		if !ok {
			return 0, false
		}

		shift := uint64(trailing) // #nosec G115 -- trailing validated by gorillaBlockState
		prevValue ^= meaningfulBits << shift
		prevFloat = math.Float64frombits(prevValue)
		if current == index {
			return prevFloat, true
		}
		current++
	}

	return 0, false
}

// ByteLength calculates the number of bytes consumed by count Gorilla-encoded float64 values.
//
// This function is used to determine the exact byte boundary for a single metric's
// Gorilla-encoded values when multiple metrics are stored consecutively in a payload.
//
// Parameters:
//   - data: byte slice containing Gorilla-compressed float64 values
//   - count: number of values to scan
//
// Returns:
//   - Number of bytes consumed by count values, or 0 if data is malformed
func (d NumericGorillaDecoder) ByteLength(data []byte, count int) int {
	if len(data) == 0 || count <= 0 {
		return 0
	}

	br := newBitReader(data)

	// Read first value (uncompressed 64 bits = 8 bytes)
	if _, ok := br.readBits(64); !ok {
		return 0
	}

	if count == 1 {
		// For first value, we consumed exactly 8 bytes
		return 8
	}

	state := gorillaBlockState{}

	// Decode remaining values to track bit consumption
	for i := 1; i < count; i++ {
		controlBit, ok := br.readBit()
		if !ok {
			return 0
		}

		if controlBit == 0 {
			// Value unchanged - just 1 bit

			continue
		}

		// Value changed - read block info
		_, blockSize, ok := state.next(br)
		if !ok {
			return 0
		}

		// Read meaningful bits
		if _, ok := br.readBits(blockSize); !ok {
			return 0
		}
	}

	// Calculate total bytes consumed
	// We need: (br.bytePos * 8 - br.bitCount) / 8 rounded up
	// The br.bytePos tells us how many bytes we've consumed from the stream
	totalBits := br.bytePos*8 - br.bitCount
	totalBytes := (totalBits + 7) / 8 // Round up to next byte

	return totalBytes
}

// bitReader provides efficient bit-level reading from a byte slice.
//
// This is a performance-critical component used by the Gorilla decoder.
// It maintains a buffer of bits and efficiently reads them as needed.
type bitReader struct {
	data     []byte // Source data
	bytePos  int    // Current byte position
	bitBuf   uint64 // Buffer holding current bits
	bitCount int    // Number of valid bits in buffer
}

// newBitReader creates a new bit reader for the given data.
func newBitReader(data []byte) *bitReader {
	return &bitReader{
		data: data,
	}
}

// readBit reads a single bit from the stream.
//
// Returns:
//   - The bit value (0 or 1) and true if successful
//   - Zero and false if no more data is available
func (br *bitReader) readBit() (uint64, bool) {
	if br.bitCount == 0 {
		if !br.fillBuffer() {
			return 0, false
		}
	}

	// Extract most significant bit (already 0 or 1, no mask needed)
	bit := br.bitBuf >> 63
	br.bitBuf <<= 1
	br.bitCount--

	return bit, true
}

// read5Bits reads exactly 5 bits with fast path.
// This specialized version is optimized for reading leading zeros field (5 bits)
// by eliminating the loop overhead of the generic readBits() function.
//
// Fast path: If buffer has ≥5 bits, extract directly without loop.
// Slow path: Fall back to readBits(5) if buffer needs refilling.
//
// Returns:
//   - The 5-bit value (0-31) as int and true if successful
//   - Zero and false if insufficient data is available
//
// Performance: 2-3% decoding improvement vs readBits(5).
func (br *bitReader) read5Bits() (int, bool) {
	if br.bitCount >= 5 {
		// Fast path: extract 5 bits directly
		br.bitCount -= 5
		// Extract from MSB
		val := int((br.bitBuf >> 59) & 0x1F) //nolint: gosec
		br.bitBuf <<= 5

		return val, true
	}

	// Slow path: need to fill buffer
	val, ok := br.readBits(5)

	return int(val), ok //nolint: gosec
}

// read6Bits reads exactly 6 bits with fast path.
//
// This specialized version is optimized for reading block size field (6 bits)
// by eliminating the loop overhead of the generic readBits() function.
//
// Fast path: If buffer has ≥6 bits, extract directly without loop.
// Slow path: Fall back to readBits(6) if buffer needs refilling.
//
// Returns:
//   - The 6-bit value (0-63) as int and true if successful
//   - Zero and false if insufficient data is available
func (br *bitReader) read6Bits() (int, bool) {
	if br.bitCount >= 6 {
		// Fast path: extract 6 bits directly
		br.bitCount -= 6
		// Extract from MSB
		val := int((br.bitBuf >> 58) & 0x3F) //nolint: gosec
		br.bitBuf <<= 6

		return val, true
	}

	// Slow path: need to fill buffer
	val, ok := br.readBits(6)

	return int(val), ok //nolint: gosec
}

// readBits reads multiple bits from the stream.
//
// Parameters:
//   - numBits: number of bits to read (1-64)
//
// Returns:
//   - The bits as a uint64 (right-aligned) and true if successful
//   - Zero and false if insufficient data is available
//
// Performance-critical: optimized for reading common bit counts (5, 6, 64).
func (br *bitReader) readBits(numBits int) (uint64, bool) {
	if numBits == 0 {
		return 0, true
	}

	if numBits <= br.bitCount {
		shift := 64 - numBits
		result := br.bitBuf >> shift
		br.bitBuf <<= numBits
		br.bitCount -= numBits

		return result, true
	}

	var result uint64
	firstRead := true

	for numBits > 0 {
		if br.bitCount == 0 {
			if !br.fillBuffer() {
				return 0, false
			}
		}

		// Determine how many bits we can read from current buffer
		bitsToRead := numBits
		if bitsToRead > br.bitCount {
			bitsToRead = br.bitCount
		}

		// Extract bits from most significant position
		shift := 64 - bitsToRead
		shiftedBits := br.bitBuf >> shift

		// Accumulate result
		if firstRead {
			result = shiftedBits
			firstRead = false
		} else {
			result = (result << bitsToRead) | shiftedBits
		}

		// Update buffer
		br.bitBuf <<= bitsToRead
		br.bitCount -= bitsToRead
		numBits -= bitsToRead
	}

	return result, true
}

// fillBuffer refills the bit buffer from the byte stream.
//
// Reads up to 8 bytes and fills the 64-bit buffer for efficient bit extraction.
// The bits are left-aligned in the buffer for consistent extraction.
// This method is called automatically when the bit buffer is empty.
//
// Returns true if buffer was filled successfully, false if no more data.
//
// Performance optimization:
//   - Fast path: uses binary.BigEndian.Uint64 for 8-byte reads (compiler intrinsic)
//   - Slow path: byte-by-byte for partial reads
func (br *bitReader) fillBuffer() bool {
	if br.bytePos >= len(br.data) {
		return false
	}

	// Read up to 8 bytes to fill the buffer
	bytesAvailable := len(br.data) - br.bytePos
	bytesToRead := 8
	if bytesToRead > bytesAvailable {
		bytesToRead = bytesAvailable
	}

	// Fast path: read full 8 bytes using binary.BigEndian
	if bytesToRead == 8 {
		br.bitBuf = binary.BigEndian.Uint64(br.data[br.bytePos : br.bytePos+8])
		br.bytePos += 8
		br.bitCount = 64

		return true
	}

	// Slow path: read partial bytes
	br.bitBuf = 0
	for i := 0; i < bytesToRead; i++ {
		br.bitBuf = (br.bitBuf << 8) | uint64(br.data[br.bytePos])
		br.bytePos++
	}

	// Left-align the bits if we read less than 8 bytes
	// This ensures consistent bit extraction from the MSB
	br.bitBuf <<= (8 - bytesToRead) * 8
	br.bitCount = bytesToRead * 8

	return true
}
