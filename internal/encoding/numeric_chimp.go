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
	// chimpTrailingThreshold is the minimum number of trailing zeros to trigger
	// the trailing-zero-optimized encoding path. Values with more trailing zeros
	// than this threshold store only the significant bits, achieving better compression.
	chimpTrailingThreshold = 6
)

// chimpLeadingRepresentation maps actual leading zero counts (0-63) to 3-bit bucket indices (0-7).
// This enables encoding leading zeros in just 3 bits instead of Gorilla's 5 bits.
//
// Bucket mapping: 0-7→0, 8-11→1, 12-15→2, 16-17→3, 18-19→4, 20-21→5, 22-23→6, 24-63→7
var chimpLeadingRepresentation = [64]uint64{
	0, 0, 0, 0, 0, 0, 0, 0, // 0-7 → bucket 0
	1, 1, 1, 1, // 8-11 → bucket 1
	2, 2, 2, 2, // 12-15 → bucket 2
	3, 3, // 16-17 → bucket 3
	4, 4, // 18-19 → bucket 4
	5, 5, // 20-21 → bucket 5
	6, 6, // 22-23 → bucket 6
	7, 7, 7, 7, 7, 7, 7, 7, // 24-31 → bucket 7
	7, 7, 7, 7, 7, 7, 7, 7, // 32-39 → bucket 7
	7, 7, 7, 7, 7, 7, 7, 7, // 40-47 → bucket 7
	7, 7, 7, 7, 7, 7, 7, 7, // 48-55 → bucket 7
	7, 7, 7, 7, 7, 7, 7, 7, // 56-63 → bucket 7
}

// chimpLeadingRound maps actual leading zero counts to the rounded (minimum) value for each bucket.
// During encoding, leading zeros are rounded down to the bucket boundary to enable 3-bit storage.
// The decoder uses chimpLeadingDecode to recover this rounded value.
var chimpLeadingRound = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0, // 0-7 → round to 0
	8, 8, 8, 8, // 8-11 → round to 8
	12, 12, 12, 12, // 12-15 → round to 12
	16, 16, // 16-17 → round to 16
	18, 18, // 18-19 → round to 18
	20, 20, // 20-21 → round to 20
	22, 22, // 22-23 → round to 22
	24, 24, 24, 24, 24, 24, 24, 24, // 24-31 → round to 24
	24, 24, 24, 24, 24, 24, 24, 24, // 32-39 → round to 24
	24, 24, 24, 24, 24, 24, 24, 24, // 40-47 → round to 24
	24, 24, 24, 24, 24, 24, 24, 24, // 48-55 → round to 24
	24, 24, 24, 24, 24, 24, 24, 24, // 56-63 → round to 24
}

// chimpLeadingDecode maps 3-bit bucket indices back to the actual (rounded) leading zero count.
var chimpLeadingDecode = [8]int{0, 8, 12, 16, 18, 20, 22, 24}

// NumericChimpEncoder implements the Chimp compression algorithm for float64 time-series values.
//
// Chimp improves upon Facebook's Gorilla algorithm (2015) with three key optimizations:
//  1. Leading-zero bucketing: Uses 3-bit encoding (8 buckets) instead of Gorilla's 5-bit raw encoding,
//     saving 2 bits per "new block" header.
//  2. Trailing-zero optimization: When trailing zeros exceed a threshold (6), strips them and stores
//     only the significant bits, saving trailing zero bits per value.
//  3. Refined control flow: Uses a 2-bit flag scheme for better prefix coding of common cases.
//
// Encoding format for each value after the first (which is stored uncompressed):
//   - Flag 00 (2 bits): Value unchanged (XOR == 0)
//   - Flag 01 (2 bits): Trailing zeros > threshold: 3-bit leading + 6-bit sigBits + significant bits
//   - Flag 10 (2 bits): Reuse previous leading zeros: (64 - leading) bits of XOR
//   - Flag 11 (2 bits): New leading zeros: 3-bit leading + (64 - leading) bits of XOR
//
// See Liakos et al., "Chimp: Efficient Lossless Floating Point Compression for Time Series Databases",
// PVLDB 15(11), 2022.
type NumericChimpEncoder struct {
	// Hot path fields (frequently accessed, keep together for cache locality)
	bitBuf             uint64 // Bit buffer for accumulating bits before writing to byte buffer
	prevValue          uint64 // Previous value (as uint64 bits)
	bitCount           int    // Number of valid bits in bitBuf
	count              int    // Number of values encoded
	storedLeadingZeros int    // Leading zeros from previous XOR (rounded to bucket boundary)
	firstValue         bool   // True if this is the first value

	// Offset: 64, cold path field
	buf *pool.ByteBuffer // Byte buffer for storing encoded data
}

var _ encoding.ColumnarEncoder[float64] = (*NumericChimpEncoder)(nil)

// NewNumericChimpEncoder creates a new Chimp encoder for float64 values.
//
// The encoder uses leading-zero bucketing and trailing-zero optimization to achieve
// 15-30% better compression than Gorilla for typical time-series data.
//
// Returns:
//   - *NumericChimpEncoder: A new encoder instance ready for float64 encoding
func NewNumericChimpEncoder() *NumericChimpEncoder {
	return &NumericChimpEncoder{
		buf:                pool.GetBlobBuffer(),
		firstValue:         true,
		storedLeadingZeros: 65, // Invalid sentinel: forces "new leading" on first changed value
	}
}

// Write encodes a single float64 value using Chimp compression.
//
// Parameters:
//   - val: The float64 value to encode
func (e *NumericChimpEncoder) Write(val float64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write values after Finish()")
	}

	e.count++
	valBits := math.Float64bits(val)

	if e.firstValue {
		e.firstValue = false
		e.prevValue = valBits
		e.writeBits(valBits, 64)

		return
	}

	e.writeValue(valBits)
}

// WriteSlice encodes a slice of float64 values using Chimp compression.
//
// Parameters:
//   - values: Slice of float64 values to encode
func (e *NumericChimpEncoder) WriteSlice(values []float64) {
	if e.buf == nil {
		panic("encoder already finished - cannot write values after Finish()")
	}

	if len(values) == 0 {
		return
	}

	if e.firstValue {
		e.count++
		valBits := math.Float64bits(values[0])
		e.firstValue = false
		e.prevValue = valBits
		e.writeBits(valBits, 64)
		values = values[1:]
	}

	for _, v := range values {
		e.count++
		e.writeValue(math.Float64bits(v))
	}
}

// Bytes returns the encoded byte slice containing all compressed values.
//
// Returns:
//   - []byte: Chimp-compressed byte slice (empty if no values written since last Reset)
func (e *NumericChimpEncoder) Bytes() []byte {
	if e.buf == nil {
		panic("encoder already finished - cannot access bytes after Finish()")
	}

	if e.bitCount > 0 {
		e.flushBits()
	}

	return e.buf.Bytes()
}

// Len returns the number of encoded float64 values.
//
// Returns:
//   - int: Number of float64 values written since last Finish
func (e *NumericChimpEncoder) Len() int {
	return e.count
}

// Size returns the size in bytes of the encoded data.
//
// Returns:
//   - int: Total bytes written to internal buffer since last Finish
func (e *NumericChimpEncoder) Size() int {
	if e.buf == nil {
		panic("encoder already finished - cannot access size after Finish()")
	}

	return e.buf.Len()
}

// Reset clears the encoder state for reuse while retaining accumulated data.
func (e *NumericChimpEncoder) Reset() {
	e.bitBuf = 0
	e.bitCount = 0
	e.count = 0
	e.prevValue = 0
	e.storedLeadingZeros = 65
	e.firstValue = true
}

// Finish finalizes the encoding process and returns the buffer to the pool.
//
// After calling Finish(), the encoder is no longer usable.
func (e *NumericChimpEncoder) Finish() {
	if e.buf == nil {
		return
	}

	pool.PutBlobBuffer(e.buf)
	e.buf = nil
}

// writeValue encodes a value using Chimp's XOR compression with leading-zero bucketing
// and trailing-zero optimization.
//
// Chimp encoding flags:
//   - 00: Value unchanged (XOR == 0)
//   - 01: Trailing zeros > threshold → 3-bit leading bucket + 6-bit sigBits + significant bits
//   - 10: Reuse previous leading zeros → (64 - leading) bits of full XOR
//   - 11: New leading zeros → 3-bit leading bucket + (64 - leading) bits of full XOR
func (e *NumericChimpEncoder) writeValue(valBits uint64) {
	xor := valBits ^ e.prevValue
	e.prevValue = valBits

	if xor == 0 {
		// Flag 00: value unchanged — inline the 2-bit write to avoid function call overhead
		available := 64 - e.bitCount
		if available >= 2 {
			e.bitBuf <<= 2
			e.bitCount += 2
			if e.bitCount == 64 {
				e.flushBits()
			}
		} else {
			// Split: 1 bit fits in current buffer, 1 bit carries over
			e.bitBuf <<= 1
			e.bitCount = 64
			e.flushBits()
			e.bitBuf = 0
			e.bitCount = 1
		}

		// Reset stored leading zeros so next changed value forces "new leading" path
		e.storedLeadingZeros = 65

		return
	}

	leadingZeros := bits.LeadingZeros64(xor)
	trailingZeros := bits.TrailingZeros64(xor)

	// Round leading zeros to bucket boundary for 3-bit encoding
	leadingRounded := chimpLeadingRound[leadingZeros]

	if trailingZeros > chimpTrailingThreshold {
		// Flag 01: trailing-zero optimized path
		significantBits := 64 - leadingRounded - trailingZeros
		if significantBits <= 0 {
			significantBits = 1
		}

		significantBitsField := uint64(significantBits) //nolint:gosec // significantBits is clamped to 1..64 and masked below

		// Write: 01 flag + 3-bit leading bucket + 6-bit significant bits count + significant bits
		e.writeBits((1<<9)|(chimpLeadingRepresentation[leadingZeros]<<6)|(significantBitsField&0x3F), 11) // flag 01 + 3-bit leading + 6-bit sigBits
		e.writeBits(xor>>trailingZeros, significantBits)

		// Reset stored leading to force new-leading on next change
		e.storedLeadingZeros = 65
	} else if leadingRounded == e.storedLeadingZeros {
		// Flag 10: reuse previous leading zeros
		significantBits := 64 - leadingRounded
		e.writeBits(2, 2) // flag 10
		e.writeBits(xor, significantBits)
	} else {
		// Flag 11: new leading zeros
		e.storedLeadingZeros = leadingRounded
		significantBits := 64 - leadingRounded

		e.writeBits((3<<3)|chimpLeadingRepresentation[leadingZeros], 5) // flag 11 + 3-bit leading
		e.writeBits(xor, significantBits)
	}
}

// writeBits writes multiple bits to the bit buffer.
func (e *NumericChimpEncoder) writeBits(value uint64, numBits int) {
	if numBits == 0 {
		return
	}

	if numBits < 64 {
		value &= (1 << numBits) - 1
	}

	available := 64 - e.bitCount

	if numBits <= available {
		e.bitBuf = (e.bitBuf << numBits) | value
		e.bitCount += numBits

		if e.bitCount == 64 {
			e.flushBits()
		}
	} else {
		highBits := numBits - available
		e.bitBuf = (e.bitBuf << available) | (value >> highBits)
		e.bitCount = 64
		e.flushBits()

		e.bitBuf = value & ((1 << highBits) - 1)
		e.bitCount = highBits
	}
}

// flushBits writes the current bit buffer to the byte buffer.
func (e *NumericChimpEncoder) flushBits() {
	if e.bitCount == 0 {
		return
	}

	numBytes := (e.bitCount + 7) / 8

	alignedBits := e.bitBuf << (64 - e.bitCount)

	startLen := e.buf.Len()
	e.buf.ExtendOrGrow(numBytes)
	bs := e.buf.Slice(startLen, startLen+numBytes)

	if numBytes == 8 {
		binary.BigEndian.PutUint64(bs, alignedBits)
	} else {
		for i := range numBytes {
			shift := 56 - (i * 8)
			bs[i] = byte(alignedBits >> shift)
		}
	}

	e.bitBuf = 0
	e.bitCount = 0
}

// NumericChimpDecoder decodes float64 values compressed with the Chimp algorithm.
//
// The decoder reads the Chimp-compressed data and reconstructs the original float64 values
// by reversing the XOR compression with leading-zero bucketing and trailing-zero optimization.
//
// This decoder is stateless and can be used concurrently for different data streams.
type NumericChimpDecoder struct{}

var _ encoding.ColumnarDecoder[float64] = NumericChimpDecoder{}

// NewNumericChimpDecoder creates a new Chimp decoder for float64 values.
//
// Returns:
//   - NumericChimpDecoder: A new decoder instance (stateless, can be reused)
func NewNumericChimpDecoder() NumericChimpDecoder {
	return NumericChimpDecoder{}
}

// All decodes all float64 values from the Chimp-compressed byte slice.
//
// Parameters:
//   - data: byte slice containing Chimp-compressed float64 values
//   - count: expected number of values to decode
//
// Returns:
//   - iter.Seq[float64]: Iterator yielding decoded float64 values
func (d NumericChimpDecoder) All(data []byte, count int) iter.Seq[float64] {
	return func(yield func(float64) bool) {
		if len(data) == 0 || count == 0 {
			return
		}

		br := bitReader{data: data}

		// Read first value (uncompressed)
		firstBits, ok := br.readBits(64)
		if !ok {
			return
		}

		prevValue := firstBits
		if !yield(math.Float64frombits(prevValue)) {
			return
		}

		storedLeading := 65 // Sentinel: no valid previous leading

		for i := 1; i < count; i++ {
			if !chimpDecodeNext(&br, &prevValue, &storedLeading) {
				return
			}

			if !yield(math.Float64frombits(prevValue)) {
				return
			}
		}
	}
}

// DecodeAll decodes all values from the encoded data directly into the destination slice.
//
// This method is optimized for bulk decoding when the caller needs all values in a slice,
// avoiding the per-element yield overhead of the All() iterator.
//
// Parameters:
//   - data: byte slice containing Chimp-compressed float64 values
//   - count: total number of values encoded in the data
//   - dst: Pre-allocated destination slice (must have len >= count)
//
// Returns:
//   - int: Number of values successfully decoded into dst
func (d NumericChimpDecoder) DecodeAll(data []byte, count int, dst []float64) int {
	if len(data) == 0 || count == 0 || len(dst) < count {
		return 0
	}

	br := bitReader{data: data}

	firstBits, ok := br.readBits(64)
	if !ok {
		return 0
	}

	prevValue := firstBits
	dst[0] = math.Float64frombits(prevValue)
	_ = dst[count-1] // bounds-check elimination

	storedLeading := 65

	for i := 1; i < count; i++ {
		flag, ok := br.read2Bits()
		if !ok {
			return i
		}

		switch flag {
		case 0: // Value unchanged
			dst[i] = math.Float64frombits(prevValue)
			storedLeading = 65

			// Batch: count consecutive unchanged values (00 flag pairs)
			remaining := count - i - 1
			if remaining >= 4 {
				zeros := chimpCountUnchangedRun(&br, remaining)
				prevFloat := math.Float64frombits(prevValue)
				for j := range zeros {
					dst[i+1+j] = prevFloat
				}
				i += zeros
			}

		case 1: // Trailing-zero optimized
			header, ok := readChimpTrailingHeader(&br)
			if !ok {
				return i
			}

			leading := chimpLeadingDecode[header>>6]

			sigBits := int(header & 0x3F) //nolint:gosec // G115: masked to 6 bits, bounded 0..63
			if sigBits == 0 {
				sigBits = 64
			}

			meaningful, ok := br.readBits(sigBits)
			if !ok {
				return i
			}

			trailingZeros := 64 - leading - sigBits
			if trailingZeros < 0 {
				return i
			}

			prevValue ^= meaningful << trailingZeros
			dst[i] = math.Float64frombits(prevValue)
			storedLeading = 65

		case 2: // Reuse previous leading
			if storedLeading > 64 {
				return i
			}

			sigBits := 64 - storedLeading

			meaningful, ok := br.readBits(sigBits)
			if !ok {
				return i
			}

			prevValue ^= meaningful
			dst[i] = math.Float64frombits(prevValue)

		case 3: // New leading
			leadingBucket, ok := br.read3Bits()
			if !ok {
				return i
			}

			leading := chimpLeadingDecode[leadingBucket]
			storedLeading = leading
			sigBits := 64 - leading

			meaningful, ok := br.readBits(sigBits)
			if !ok {
				return i
			}

			prevValue ^= meaningful
			dst[i] = math.Float64frombits(prevValue)

		default:
			return i
		}
	}

	return count
}

// chimpCountUnchangedRun counts consecutive Chimp "unchanged" flags (00 bit pairs)
// from the current position in the bit reader. Each pair represents one unchanged value.
// Follows the same consume-after-check pattern as bitReader.countLeadingZeroBits.
func chimpCountUnchangedRun(br *bitReader, maxPairs int) int {
	count := 0

	for count < maxPairs {
		if br.bitCount == 0 {
			if !br.fillBuffer() {
				return count
			}
		}

		leadingZeros := min(bits.LeadingZeros64(br.bitBuf), br.bitCount)

		// Complete pairs we can extract from consecutive zeros
		pairs := min(leadingZeros/2, maxPairs-count)
		bitsToConsume := pairs * 2

		// Found a 1-bit within the buffer: consume pairs and stop
		if leadingZeros < br.bitCount {
			if bitsToConsume > 0 {
				br.bitBuf <<= bitsToConsume
				br.bitCount -= bitsToConsume
				count += pairs
			}

			break
		}

		// All buffer bits were zeros: consume and refill on next iteration.
		// Only consume complete pairs — an odd trailing zero stays for the
		// next read2Bits call since it might be part of a non-00 flag.
		if bitsToConsume > 0 {
			br.bitBuf <<= bitsToConsume
			br.bitCount -= bitsToConsume
			count += pairs
		}

		// Odd zero left over (leadingZeros was odd) — can't form a pair
		if leadingZeros%2 != 0 {
			break
		}
	}

	return count
}

// At retrieves the float64 value at the specified index from the Chimp-compressed data.
//
// Parameters:
//   - data: byte slice containing Chimp-compressed float64 values
//   - index: zero-based index of the value to retrieve
//   - count: total number of values encoded in the data
//
// Returns:
//   - The decoded float64 value and true if successful
//   - Zero value and false if index is out of bounds or data is malformed
func (d NumericChimpDecoder) At(data []byte, index int, count int) (float64, bool) {
	if len(data) == 0 || index < 0 || index >= count {
		return 0, false
	}

	br := bitReader{data: data}

	// Read first value (uncompressed)
	firstBits, ok := br.readBits(64)
	if !ok {
		return 0, false
	}

	prevValue := firstBits
	if index == 0 {
		return math.Float64frombits(prevValue), true
	}

	storedLeading := 65

	for current := 1; current <= index; current++ {
		if !chimpDecodeNext(&br, &prevValue, &storedLeading) {
			return 0, false
		}

		if current == index {
			return math.Float64frombits(prevValue), true
		}
	}

	return 0, false
}

// ByteLength calculates the number of bytes consumed by count Chimp-encoded float64 values.
//
// Parameters:
//   - data: byte slice containing Chimp-compressed float64 values
//   - count: number of values to scan
//
// Returns:
//   - Number of bytes consumed by count values, or 0 if data is malformed
func (d NumericChimpDecoder) ByteLength(data []byte, count int) int {
	if len(data) == 0 || count <= 0 {
		return 0
	}

	br := bitReader{data: data}

	if _, ok := br.readBits(64); !ok {
		return 0
	}

	if count == 1 {
		return 8
	}

	storedLeading := 65

	for i := 1; i < count; i++ {
		flag, ok := br.read2Bits()
		if !ok {
			return 0
		}

		switch flag {
		case 0: // Unchanged
			storedLeading = 65

		case 1: // Trailing-zero optimized
			header, ok := readChimpTrailingHeader(&br)
			if !ok {
				return 0
			}

			leading := chimpLeadingDecode[header>>6]

			sigBits := int(header & 0x3F) //nolint:gosec // header is masked to 6 bits, so the conversion is bounded to 0..63
			if sigBits == 0 {
				sigBits = 64
			}

			if _, ok := br.readBits(sigBits); !ok {
				return 0
			}

			_ = leading
			storedLeading = 65

		case 2: // Reuse leading
			if storedLeading > 64 {
				return 0
			}

			sigBits := 64 - storedLeading
			if _, ok := br.readBits(sigBits); !ok {
				return 0
			}

		case 3: // New leading
			leadingBucket, ok := br.read3Bits()
			if !ok {
				return 0
			}

			leading := chimpLeadingDecode[leadingBucket]
			storedLeading = leading
			sigBits := 64 - leading

			if _, ok := br.readBits(sigBits); !ok {
				return 0
			}

		default:
			return 0
		}
	}

	totalBits := br.bytePos*8 - br.bitCount
	totalBytes := (totalBits + 7) / 8

	return totalBytes
}

// chimpDecodeNext reads a 2-bit flag and decodes the next Chimp-compressed value,
// updating prevValue and storedLeading in place.
// Returns true on success, false on read failure or invalid data.
func chimpDecodeNext(br *bitReader, prevValue *uint64, storedLeading *int) bool {
	flag, ok := br.read2Bits()
	if !ok {
		return false
	}

	switch flag {
	case 0: // Value unchanged
		*storedLeading = 65

	case 1: // Trailing-zero optimized
		header, ok := readChimpTrailingHeader(br)
		if !ok {
			return false
		}

		leading := chimpLeadingDecode[header>>6]

		sigBits := int(header & 0x3F) //nolint:gosec // header is masked to 6 bits, so the conversion is bounded to 0..63
		if sigBits == 0 {
			sigBits = 64
		}

		meaningful, ok := br.readBits(sigBits)
		if !ok {
			return false
		}

		trailingZeros := 64 - leading - sigBits
		if trailingZeros < 0 {
			return false
		}

		*prevValue ^= meaningful << trailingZeros
		*storedLeading = 65

	case 2: // Reuse previous leading
		if *storedLeading > 64 {
			return false
		}

		sigBits := 64 - *storedLeading

		meaningful, ok := br.readBits(sigBits)
		if !ok {
			return false
		}

		*prevValue ^= meaningful

	case 3: // New leading
		leadingBucket, ok := br.read3Bits()
		if !ok {
			return false
		}

		leading := chimpLeadingDecode[leadingBucket]
		*storedLeading = leading
		sigBits := 64 - leading

		meaningful, ok := br.readBits(sigBits)
		if !ok {
			return false
		}

		*prevValue ^= meaningful

	default:
		return false
	}

	return true
}

// readChimpTrailingHeader reads the 9-bit header used by Chimp's trailing-zero path.
// The returned value packs the 3-bit leading bucket in the high bits and the 6-bit
// significant-bit count in the low bits.
func readChimpTrailingHeader(br *bitReader) (uint64, bool) {
	if br.bitCount >= 9 {
		br.bitCount -= 9
		header := (br.bitBuf >> 55) & 0x1FF
		br.bitBuf <<= 9

		return header, true
	}

	return br.readBits(9)
}
