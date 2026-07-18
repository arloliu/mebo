// Package gorilla implements XOR-compressed float64 values.
package gorilla

import (
	"encoding/binary"
	"iter"
	"math"
	"math/bits"

	"github.com/arloliu/mebo/encoding"
	"github.com/arloliu/mebo/internal/encoding/internal/bitstream"
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
	bitBuf        uint64 // Pending bits, MSB-aligned (bit 63 is the next bit to be written)
	prevValue     uint64 // Previous value (as uint64 bits)
	bitCount      int    // Number of valid bits in bitBuf (0-63)
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

	// Worst case per value: 13-bit header + 64 meaningful bits → two 8-byte spills.
	e.buf.Grow(16)

	if e.firstValue {
		e.firstValue = false
		e.prevValue = valBits
		// Write first value uncompressed
		e.appendBits(valBits, 64)

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

	// Pre-grow once for the whole slice: worst case ~77 bits ≈ 10 bytes per value.
	e.buf.Grow(len(values)*10 + 16)

	// Process first value
	if e.firstValue {
		e.count++
		valBits := math.Float64bits(values[0])
		e.firstValue = false
		e.prevValue = valBits
		e.appendBits(valBits, 64)
		values = values[1:]
	}

	for _, val := range values {
		e.count++
		e.writeValue(math.Float64bits(val))
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
	e.count = 0
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
		// Value unchanged: a single 0 bit. bitBuf is MSB-aligned, so only the
		// count advances; the bit itself is already zero.
		e.bitCount++
		if e.bitCount == 64 {
			e.buf.B = binary.BigEndian.AppendUint64(e.buf.B, e.bitBuf)
			e.bitBuf = 0
			e.bitCount = 0
		}

		return
	}

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
		// Same block: control bit 1, reuse bit 0, then meaningful bits
		e.appendBits(0b10, 2)
		e.appendBits(xor>>uint(e.prevTrailing), e.prevBlockSize) //nolint:gosec // G115: bit widths/counts bounded to 0..64
	} else {
		// Different block: control bit 1, new-block bit 1, 5-bit leading,
		// 6-bit (blockSize-1), then meaningful bits
		blockSize := 64 - leading - trailing
		header := uint64(0b11)<<11 | uint64(leading)<<6 | uint64(blockSize-1) //nolint:gosec // G115: leading is 0-31, blockSize-1 is 0-63
		e.appendBits(header, 13)
		e.appendBits(xor>>uint(trailing), blockSize) //nolint:gosec // G115: bit widths/counts bounded to 0..64

		e.prevLeading = leading
		e.prevTrailing = trailing
		e.prevBlockSize = blockSize // Cache block size for next iteration
	}
}

// appendBits writes the low numBits bits of value to the stream, MSB-first.
//
// This is the single hot-path bit append primitive: it OR-merges the bits into
// the MSB-aligned accumulator and spills exactly one 8-byte big-endian word to
// the byte buffer when the accumulator fills. The caller must ensure buffer
// capacity (Write/WriteSlice pre-grow), so the append never reallocates.
//
// Parameters:
//   - value: the bits to write (only the least significant numBits are used)
//   - numBits: number of bits to write (1-64)
func (e *NumericGorillaEncoder) appendBits(value uint64, numBits int) {
	m := value << (64 - uint(numBits)) //nolint:gosec // G115: numBits bounded to 1..64; MSB-align (numBits==64 → shift 0)
	e.bitBuf |= m >> uint(e.bitCount)  //nolint:gosec // G115: bit widths/counts bounded to 0..64

	total := e.bitCount + numBits
	if total >= 64 {
		e.buf.B = binary.BigEndian.AppendUint64(e.buf.B, e.bitBuf)
		spill := 64 - e.bitCount
		e.bitBuf = m << uint(spill) //nolint:gosec // G115: spill bounded to 1..64; spill==64 → 0 (Go defines shift ≥ width as 0)
		e.bitCount = total - 64
	} else {
		e.bitCount = total
	}
}

// flushBits writes the remaining partial bits (< 64) to the byte buffer,
// zero-padding the final byte. The bit buffer is MSB-aligned, so bytes are
// emitted most-significant first.
func (e *NumericGorillaEncoder) flushBits() {
	if e.bitCount == 0 {
		return
	}

	numBytes := (e.bitCount + 7) / 8

	startLen := e.buf.Len()
	e.buf.ExtendOrGrow(numBytes)
	bs := e.buf.Slice(startLen, startLen+numBytes)

	for i := range numBytes {
		bs[i] = byte(e.bitBuf >> (56 - i*8)) // top numBytes bytes of the accumulator
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
func (s *gorillaBlockState) next(reader *bitstream.Reader) (trailing int, blockSize int, ok bool) {
	blockControlBit, ok := reader.ReadBit()
	if !ok {
		return 0, 0, false
	}

	if blockControlBit == 0 {
		if !s.valid {
			return 0, 0, false
		}

		return s.trailing, s.blockSize, true
	}

	leading, ok := reader.Read5Bits()
	if !ok {
		return 0, 0, false
	}

	blockSize, ok = reader.Read6Bits()
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

		totalBits := len(data) * 8
		if totalBits < 64 {
			return
		}

		prevValue := bitstream.PeekBits64(data, 0)
		prevFloat := math.Float64frombits(prevValue)
		if !yield(prevFloat) {
			return
		}

		bitPos := 64
		produced := 1

		trailing := 0
		blockSize := 0
		blockValid := false

		for produced < count {
			if bitPos >= totalBits {
				return
			}

			w := bitstream.PeekBits64(data, bitPos)

			if w>>63 == 0 {
				// Run of unchanged values: count leading zero control bits in the window.
				zeros := bits.LeadingZeros64(w)
				run := min(zeros, count-produced, totalBits-bitPos)
				for range run {
					if !yield(prevFloat) {
						return
					}
				}
				produced += run
				bitPos += run

				continue
			}

			// Changed value: control bit 1 already seen in the window.
			var consumed int
			if (w>>62)&1 == 0 {
				// Reuse previous block window.
				if !blockValid {
					return
				}
				consumed = 2
			} else {
				leading := int(w>>57) & 0x1F    //nolint:gosec // G115: bit widths/counts bounded to 0..64
				blockSize = int(w>>51)&0x3F + 1 //nolint:gosec // G115: bit widths/counts bounded to 0..64
				trailing = 64 - leading - blockSize
				if trailing < 0 || trailing > 64 {
					return
				}
				blockValid = true
				consumed = 13
			}

			if bitPos+consumed+blockSize > totalBits {
				return
			}

			var meaningful uint64
			if blockSize <= 64-consumed {
				meaningful = (w << uint(consumed)) >> (64 - uint(blockSize)) //nolint:gosec // G115: bit widths/counts bounded to 0..64
			} else {
				meaningful = bitstream.PeekBits64(data, bitPos+consumed) >> (64 - uint(blockSize)) //nolint:gosec // G115: bit widths/counts bounded to 0..64
			}
			bitPos += consumed + blockSize

			prevValue ^= meaningful << uint(trailing) //nolint:gosec // G115: bit widths/counts bounded to 0..64
			prevFloat = math.Float64frombits(prevValue)
			if !yield(prevFloat) {
				return
			}
			produced++
		}
	}
}

func (NumericGorillaDecoder) decodeAtSmall(reader *bitstream.Reader, prevValue uint64, target int) (float64, bool) {
	trailing := 0
	blockSize := 0
	blockValid := false
	prevFloat := math.Float64frombits(prevValue)

	for current := 1; current <= target; {
		controlBit, ok := reader.ReadBit()
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

		reuseBit, ok := reader.ReadBit()
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
			leading, ok := reader.Read5Bits()
			if !ok {
				return 0, false
			}
			sizeBits, ok := reader.Read6Bits()
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

		meaningful, ok := reader.ReadBits(blockSizeBits)
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

// DecodeAll decodes all values from the encoded data directly into the destination slice.
//
// This method is optimized for bulk decoding when the caller needs all values in a slice,
// avoiding the per-element yield overhead of the All() iterator.
//
// Parameters:
//   - data: byte slice containing Gorilla-compressed float64 values
//   - count: total number of values encoded in the data
//   - dst: Pre-allocated destination slice (must have len >= count)
//
// Returns:
//   - int: Number of values successfully decoded into dst
func (d NumericGorillaDecoder) DecodeAll(data []byte, count int, dst []float64) int {
	if len(data) == 0 || count == 0 || len(dst) < count {
		return 0
	}

	totalBits := len(data) * 8
	if totalBits < 64 {
		return 0
	}

	prevValue := bitstream.PeekBits64(data, 0)
	prevFloat := math.Float64frombits(prevValue)
	dst[0] = prevFloat
	bitPos := 64
	produced := 1

	trailing := 0
	blockSize := 0
	blockValid := false

	for produced < count {
		if bitPos >= totalBits {
			return produced
		}

		w := bitstream.PeekBits64(data, bitPos)

		if w>>63 == 0 {
			// Run of unchanged values: count leading zero control bits in the window.
			zeros := bits.LeadingZeros64(w)
			run := min(zeros, count-produced, totalBits-bitPos)
			for range run {
				dst[produced] = prevFloat
				produced++
			}
			bitPos += run

			continue
		}

		// Changed value: control bit 1 already seen in the window.
		var consumed int
		if (w>>62)&1 == 0 {
			// Reuse previous block window.
			if !blockValid {
				return produced
			}
			consumed = 2
		} else {
			leading := int(w>>57) & 0x1F    //nolint:gosec // G115: bit widths/counts bounded to 0..64
			blockSize = int(w>>51)&0x3F + 1 //nolint:gosec // G115: bit widths/counts bounded to 0..64
			trailing = 64 - leading - blockSize
			if trailing < 0 || trailing > 64 {
				return produced
			}
			blockValid = true
			consumed = 13
		}

		if bitPos+consumed+blockSize > totalBits {
			return produced
		}

		var meaningful uint64
		if blockSize <= 64-consumed {
			meaningful = (w << uint(consumed)) >> (64 - uint(blockSize)) //nolint:gosec // G115: bit widths/counts bounded to 0..64
		} else {
			meaningful = bitstream.PeekBits64(data, bitPos+consumed) >> (64 - uint(blockSize)) //nolint:gosec // G115: bit widths/counts bounded to 0..64
		}
		bitPos += consumed + blockSize

		prevValue ^= meaningful << uint(trailing) //nolint:gosec // G115: bit widths/counts bounded to 0..64
		prevFloat = math.Float64frombits(prevValue)
		dst[produced] = prevFloat
		produced++
	}

	return produced
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

	reader := bitstream.NewReader(data)

	// Read first value (uncompressed)
	firstBits, ok := reader.ReadBits(64)
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
		return d.decodeAtSmall(reader, prevValue, remaining)
	}

	state := gorillaBlockState{}

	for current := 1; current <= index; {
		controlBit, ok := reader.ReadBit()
		if !ok {
			return 0, false
		}

		if controlBit == 0 {
			if current == index {
				return prevFloat, true
			}
			current++

			for current <= index {
				controlBit, ok = reader.ReadBit()
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

		trailing, blockSize, ok := state.next(reader)
		if !ok {
			return 0, false
		}

		meaningfulBits, ok := reader.ReadBits(blockSize)
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

	reader := bitstream.NewReader(data)

	// Read first value (uncompressed 64 bits = 8 bytes)
	if _, ok := reader.ReadBits(64); !ok {
		return 0
	}

	if count == 1 {
		// For first value, we consumed exactly 8 bytes
		return 8
	}

	state := gorillaBlockState{}

	// Decode remaining values to track bit consumption
	for i := 1; i < count; i++ {
		controlBit, ok := reader.ReadBit()
		if !ok {
			return 0
		}

		if controlBit == 0 {
			// Value unchanged - just 1 bit

			continue
		}

		// Value changed - read block info
		_, blockSize, ok := state.next(reader)
		if !ok {
			return 0
		}

		// Read meaningful bits
		if _, ok := reader.ReadBits(blockSize); !ok {
			return 0
		}
	}

	return reader.ConsumedBytes()
}

type gorillaState struct {
	data       []byte
	bitPos     int
	totalBits  int
	prevValue  uint64
	prevFloat  float64
	trailing   int
	blockSize  int
	zeroRun    int
	blockValid bool
}

func newGorillaState(data []byte) (gorillaState, bool) {
	if len(data) < 8 {
		return gorillaState{}, false
	}

	previous := binary.BigEndian.Uint64(data)

	return gorillaState{
		data:      data,
		bitPos:    64,
		totalBits: len(data) * 8,
		prevValue: previous,
		prevFloat: math.Float64frombits(previous),
	}, true
}

func decodeGorillaValue(state *gorillaState) bool {
	if state.zeroRun > 0 {
		state.zeroRun--

		return true
	}

	if state.bitPos >= state.totalBits {
		return false
	}

	window := bitstream.PeekBits64(state.data, state.bitPos)
	if window>>63 == 0 {
		run := min(bits.LeadingZeros64(window), state.totalBits-state.bitPos)
		state.bitPos += run
		state.zeroRun = run - 1

		return true
	}

	consumed := 2
	if (window>>62)&1 == 0 {
		if !state.blockValid {
			return false
		}
	} else {
		leading := int(window>>57) & 0x1F          //nolint:gosec
		state.blockSize = int(window>>51)&0x3F + 1 //nolint:gosec
		state.trailing = 64 - leading - state.blockSize
		if state.trailing < 0 || state.trailing > 64 {
			return false
		}
		state.blockValid = true
		consumed = 13
	}

	if state.bitPos+consumed+state.blockSize > state.totalBits {
		return false
	}

	var meaningful uint64
	if state.blockSize <= 64-consumed {
		meaningful = (window << uint(consumed)) >> (64 - uint(state.blockSize)) //nolint:gosec
	} else {
		meaningful = bitstream.PeekBits64(state.data, state.bitPos+consumed) >> (64 - uint(state.blockSize)) //nolint:gosec
	}
	state.bitPos += consumed + state.blockSize
	state.prevValue ^= meaningful << uint(state.trailing)
	state.prevFloat = math.Float64frombits(state.prevValue)

	return true
}

// GorillaCursor incrementally decodes a Gorilla XOR stream without a count cap.
// The zero value is not usable; construct with NewGorillaCursor.
type GorillaCursor struct {
	state gorillaState
}

// GorillaValState incrementally decodes a Gorilla XOR compressed value stream.
// The zero value is not usable; construct with NewGorillaValState.
type GorillaValState struct {
	state     gorillaState
	remaining int
}

// NewGorillaCursor initializes an uncapped cursor from data.
func NewGorillaCursor(data []byte) (GorillaCursor, bool) {
	state, ok := newGorillaState(data)

	return GorillaCursor{state: state}, ok
}

// NewGorillaValState initializes the state from data, consuming the first
// value, which is immediately available through Val.
func NewGorillaValState(data []byte) (GorillaValState, bool) {
	state, ok := newGorillaState(data)

	return GorillaValState{state: state, remaining: math.MaxInt}, ok
}

// First returns the first value consumed while constructing the cursor.
func (c GorillaCursor) First() float64 {
	return c.state.prevFloat
}

// Next decodes and returns the next value.
func (c *GorillaCursor) Next() (float64, bool) {
	if !decodeGorillaValue(&c.state) {
		return 0, false
	}

	return c.state.prevFloat, true
}

// SetCount constrains state to count values total, including the first value.
func (s *GorillaValState) SetCount(count int) {
	if count > 1 {
		s.remaining = count - 1

		return
	}

	s.remaining = 0
}

// Next decodes the next value.
func (s *GorillaValState) Next() bool {
	if s.remaining <= 0 || !decodeGorillaValue(&s.state) {
		return false
	}
	s.remaining--

	return true
}

// Val returns the most recently decoded value.
func (s *GorillaValState) Val() float64 {
	return s.state.prevFloat
}
