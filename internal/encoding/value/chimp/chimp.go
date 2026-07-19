// Package chimp implements Chimp compression for float64 value streams.
package chimp

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
	bitBuf             uint64 // Pending bits, MSB-aligned (bit 63 is the next bit to be written)
	prevValue          uint64 // Previous value (as uint64 bits)
	bitCount           int    // Number of valid bits in bitBuf (0-63)
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

	// Worst case per value: 11-bit header + 64 significant bits → two 8-byte spills.
	e.buf.Grow(16)

	if e.firstValue {
		e.firstValue = false
		e.prevValue = valBits
		e.appendBits(valBits, 64)

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

	// Pre-grow once for the whole slice: worst case ~75 bits ≈ 10 bytes per value.
	e.buf.Grow(len(values)*10 + 16)

	if e.firstValue {
		e.count++
		valBits := math.Float64bits(values[0])
		e.firstValue = false
		e.prevValue = valBits
		e.appendBits(valBits, 64)
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
		// Flag 00: value unchanged. bitBuf is MSB-aligned, so only the count
		// advances; the two flag bits are already zero.
		total := e.bitCount + 2
		if total >= 64 {
			e.buf.B = binary.BigEndian.AppendUint64(e.buf.B, e.bitBuf)
			e.bitBuf = 0
			e.bitCount = total - 64
		} else {
			e.bitCount = total
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

		significantBitsField := uint64(significantBits)

		// Write: 01 flag + 3-bit leading bucket + 6-bit significant bits count + significant bits
		e.appendBits((1<<9)|(chimpLeadingRepresentation[leadingZeros]<<6)|(significantBitsField&0x3F), 11) // flag 01 + 3-bit leading + 6-bit sigBits
		e.appendBits(xor>>uint(trailingZeros), significantBits)

		// Reset stored leading to force new-leading on next change
		e.storedLeadingZeros = 65
	} else if leadingRounded == e.storedLeadingZeros {
		// Flag 10: reuse previous leading zeros
		significantBits := 64 - leadingRounded
		e.appendBits(2, 2) // flag 10
		e.appendBits(xor, significantBits)
	} else {
		// Flag 11: new leading zeros
		e.storedLeadingZeros = leadingRounded
		significantBits := 64 - leadingRounded

		e.appendBits((3<<3)|chimpLeadingRepresentation[leadingZeros], 5) // flag 11 + 3-bit leading
		e.appendBits(xor, significantBits)
	}
}

// appendBits writes the low numBits bits of value to the stream, MSB-first.
//
// Same hot-path primitive as NumericGorillaEncoder.appendBits: OR-merge into
// the MSB-aligned accumulator, spill exactly one 8-byte big-endian word when
// it fills. Callers pre-grow the buffer, so the append never reallocates.
//
// Parameters:
//   - value: the bits to write (only the least significant numBits are used)
//   - numBits: number of bits to write (1-64)
func (e *NumericChimpEncoder) appendBits(value uint64, numBits int) {
	m := value << (64 - uint(numBits))
	e.bitBuf |= m >> uint(e.bitCount)

	total := e.bitCount + numBits
	if total >= 64 {
		e.buf.B = binary.BigEndian.AppendUint64(e.buf.B, e.bitBuf)
		spill := 64 - e.bitCount
		e.bitBuf = m << uint(spill)
		e.bitCount = total - 64
	} else {
		e.bitCount = total
	}
}

// flushBits writes the remaining partial bits (< 64) to the byte buffer,
// zero-padding the final byte. The bit buffer is MSB-aligned, so bytes are
// emitted most-significant first.
func (e *NumericChimpEncoder) flushBits() {
	if e.bitCount == 0 {
		return
	}

	numBytes := (e.bitCount + 7) / 8

	startLen := e.buf.Len()
	e.buf.ExtendOrGrow(numBytes)
	bs := e.buf.Slice(startLen, startLen+numBytes)

	for i := range numBytes {
		bs[i] = byte(e.bitBuf >> (56 - i*8)) //nolint:gosec // top numBytes bytes of the accumulator
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
func (d NumericChimpDecoder) All(data []byte, count int) iter.Seq[float64] { //nolint:cyclop // windowed XOR decode has four inherent flag branches
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

		storedLeading := 65 // Sentinel: no valid previous leading

		for produced < count {
			if bitPos+2 > totalBits {
				return
			}

			w := bitstream.PeekBits64(data, bitPos)

			switch w >> 62 {
			case 0: // Value unchanged: batch the whole run of 00 flag pairs in the window
				pairs := bits.LeadingZeros64(w) / 2
				run := min(pairs, count-produced, (totalBits-bitPos)/2)
				for range run {
					if !yield(prevFloat) {
						return
					}
				}
				produced += run
				bitPos += run * 2
				storedLeading = 65

			case 1: // Trailing-zero optimized
				leading := chimpLeadingDecode[(w>>59)&0x07]

				sigBits := int(w>>53) & 0x3F
				if sigBits == 0 {
					sigBits = 64
				}

				trailingZeros := 64 - leading - sigBits
				if trailingZeros < 0 || bitPos+11+sigBits > totalBits {
					return
				}

				var meaningful uint64
				if sigBits <= 53 {
					meaningful = (w << 11) >> (64 - uint(sigBits))
				} else {
					meaningful = bitstream.PeekBits64(data, bitPos+11) >> (64 - uint(sigBits))
				}
				bitPos += 11 + sigBits

				prevValue ^= meaningful << uint(trailingZeros)
				prevFloat = math.Float64frombits(prevValue)
				if !yield(prevFloat) {
					return
				}
				produced++
				storedLeading = 65

			case 2: // Reuse previous leading
				if storedLeading > 64 {
					return
				}

				sigBits := 64 - storedLeading
				if bitPos+2+sigBits > totalBits {
					return
				}

				var meaningful uint64
				if sigBits <= 62 {
					meaningful = (w << 2) >> (64 - uint(sigBits))
				} else {
					meaningful = bitstream.PeekBits64(data, bitPos+2) >> (64 - uint(sigBits))
				}
				bitPos += 2 + sigBits

				prevValue ^= meaningful
				prevFloat = math.Float64frombits(prevValue)
				if !yield(prevFloat) {
					return
				}
				produced++

			default: // case 3: New leading
				leading := chimpLeadingDecode[(w>>59)&0x07]
				storedLeading = leading
				sigBits := 64 - leading

				if bitPos+5+sigBits > totalBits {
					return
				}

				var meaningful uint64
				if sigBits <= 59 {
					meaningful = (w << 5) >> (64 - uint(sigBits))
				} else {
					meaningful = bitstream.PeekBits64(data, bitPos+5) >> (64 - uint(sigBits))
				}
				bitPos += 5 + sigBits

				prevValue ^= meaningful
				prevFloat = math.Float64frombits(prevValue)
				if !yield(prevFloat) {
					return
				}
				produced++
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

	totalBits := len(data) * 8
	if totalBits < 64 {
		return 0
	}

	prevValue := bitstream.PeekBits64(data, 0)
	prevFloat := math.Float64frombits(prevValue)
	dst[0] = prevFloat
	bitPos := 64
	produced := 1
	_ = dst[count-1] // bounds-check elimination

	storedLeading := 65

	for produced < count {
		if bitPos+2 > totalBits {
			return produced
		}

		w := bitstream.PeekBits64(data, bitPos)

		switch w >> 62 {
		case 0: // Value unchanged: batch the whole run of 00 flag pairs in the window
			pairs := bits.LeadingZeros64(w) / 2
			run := min(pairs, count-produced, (totalBits-bitPos)/2)
			for range run {
				dst[produced] = prevFloat
				produced++
			}
			bitPos += run * 2
			storedLeading = 65

		case 1: // Trailing-zero optimized
			leading := chimpLeadingDecode[(w>>59)&0x07]

			sigBits := int(w>>53) & 0x3F
			if sigBits == 0 {
				sigBits = 64
			}

			trailingZeros := 64 - leading - sigBits
			if trailingZeros < 0 || bitPos+11+sigBits > totalBits {
				return produced
			}

			var meaningful uint64
			if sigBits <= 53 {
				meaningful = (w << 11) >> (64 - uint(sigBits))
			} else {
				meaningful = bitstream.PeekBits64(data, bitPos+11) >> (64 - uint(sigBits))
			}
			bitPos += 11 + sigBits

			prevValue ^= meaningful << uint(trailingZeros)
			prevFloat = math.Float64frombits(prevValue)
			dst[produced] = prevFloat //nolint:gosec // produced advances only while it remains within dst.
			produced++
			storedLeading = 65

		case 2: // Reuse previous leading
			if storedLeading > 64 {
				return produced
			}

			sigBits := 64 - storedLeading
			if bitPos+2+sigBits > totalBits {
				return produced
			}

			var meaningful uint64
			if sigBits <= 62 {
				meaningful = (w << 2) >> (64 - uint(sigBits))
			} else {
				meaningful = bitstream.PeekBits64(data, bitPos+2) >> (64 - uint(sigBits))
			}
			bitPos += 2 + sigBits

			prevValue ^= meaningful
			prevFloat = math.Float64frombits(prevValue)
			dst[produced] = prevFloat //nolint:gosec // produced advances only while it remains within dst.
			produced++

		default: // case 3: New leading
			leading := chimpLeadingDecode[(w>>59)&0x07]
			storedLeading = leading
			sigBits := 64 - leading

			if bitPos+5+sigBits > totalBits {
				return produced
			}

			var meaningful uint64
			if sigBits <= 59 {
				meaningful = (w << 5) >> (64 - uint(sigBits))
			} else {
				meaningful = bitstream.PeekBits64(data, bitPos+5) >> (64 - uint(sigBits))
			}
			bitPos += 5 + sigBits

			prevValue ^= meaningful
			prevFloat = math.Float64frombits(prevValue)
			dst[produced] = prevFloat //nolint:gosec // produced advances only while it remains within dst.
			produced++
		}
	}

	return produced
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

	reader := bitstream.NewReader(data)

	// Read first value (uncompressed)
	firstBits, ok := reader.ReadBits(64)
	if !ok {
		return 0, false
	}

	prevValue := firstBits
	if index == 0 {
		return math.Float64frombits(prevValue), true
	}

	storedLeading := 65

	for current := 1; current <= index; current++ {
		if !chimpDecodeNext(reader, &prevValue, &storedLeading) {
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

	reader := bitstream.NewReader(data)

	if _, ok := reader.ReadBits(64); !ok {
		return 0
	}

	if count == 1 {
		return 8
	}

	storedLeading := 65

	for i := 1; i < count; i++ {
		flag, ok := reader.Read2Bits()
		if !ok {
			return 0
		}

		switch flag {
		case 0: // Unchanged
			storedLeading = 65

		case 1: // Trailing-zero optimized
			header, ok := readChimpTrailingHeader(reader)
			if !ok {
				return 0
			}

			leading := chimpLeadingDecode[header>>6]

			sigBits := int(header & 0x3F)
			if sigBits == 0 {
				sigBits = 64
			}

			if _, ok := reader.ReadBits(sigBits); !ok {
				return 0
			}

			_ = leading
			storedLeading = 65

		case 2: // Reuse leading
			if storedLeading > 64 {
				return 0
			}

			sigBits := 64 - storedLeading
			if _, ok := reader.ReadBits(sigBits); !ok {
				return 0
			}

		case 3: // New leading
			leadingBucket, ok := reader.Read3Bits()
			if !ok {
				return 0
			}

			leading := chimpLeadingDecode[leadingBucket]
			storedLeading = leading
			sigBits := 64 - leading

			if _, ok := reader.ReadBits(sigBits); !ok {
				return 0
			}

		default:
			return 0
		}
	}

	return reader.ConsumedBytes()
}

// chimpState holds mutable state for Chimp XOR value decoding.
type chimpState struct {
	data          []byte
	bitPos        int
	totalBits     int
	prevValue     uint64
	prevFloat     float64
	storedLeading int
	zeroRun       int
}

func newChimpState(valData []byte) (chimpState, bool) {
	if len(valData) < 8 {
		return chimpState{}, false
	}

	prev := binary.BigEndian.Uint64(valData)

	return chimpState{
		data:          valData,
		bitPos:        64,
		totalBits:     len(valData) * 8,
		prevValue:     prev,
		prevFloat:     math.Float64frombits(prev),
		storedLeading: 65,
	}, true
}

func decodeChimpValue(cs *chimpState) bool {
	if cs.zeroRun > 0 {
		cs.zeroRun--

		return true
	}

	if cs.bitPos+2 > cs.totalBits {
		return false
	}

	w := bitstream.PeekBits64(cs.data, cs.bitPos)
	switch w >> 62 {
	case 0:
		run := min(bits.LeadingZeros64(w)/2, (cs.totalBits-cs.bitPos)/2)
		cs.bitPos += run * 2
		cs.zeroRun = run - 1
		cs.storedLeading = 65
	case 1:
		leading := chimpLeadingDecode[(w>>59)&0x07]
		sigBits := int(w>>53) & 0x3F
		if sigBits == 0 {
			sigBits = 64
		}
		trailingZeros := 64 - leading - sigBits
		if trailingZeros < 0 || cs.bitPos+11+sigBits > cs.totalBits {
			return false
		}

		var meaningful uint64
		if sigBits <= 53 {
			meaningful = (w << 11) >> (64 - uint(sigBits))
		} else {
			meaningful = bitstream.PeekBits64(cs.data, cs.bitPos+11) >> (64 - uint(sigBits))
		}
		cs.bitPos += 11 + sigBits
		cs.prevValue ^= meaningful << uint(trailingZeros)
		cs.prevFloat = math.Float64frombits(cs.prevValue)
		cs.storedLeading = 65
	case 2:
		if cs.storedLeading > 64 {
			return false
		}
		sigBits := 64 - cs.storedLeading
		if cs.bitPos+2+sigBits > cs.totalBits {
			return false
		}

		var meaningful uint64
		if sigBits <= 62 {
			meaningful = (w << 2) >> (64 - uint(sigBits))
		} else {
			meaningful = bitstream.PeekBits64(cs.data, cs.bitPos+2) >> (64 - uint(sigBits))
		}
		cs.bitPos += 2 + sigBits
		cs.prevValue ^= meaningful
		cs.prevFloat = math.Float64frombits(cs.prevValue)
	default:
		leading := chimpLeadingDecode[(w>>59)&0x07]
		cs.storedLeading = leading
		sigBits := 64 - leading
		if cs.bitPos+5+sigBits > cs.totalBits {
			return false
		}

		var meaningful uint64
		if sigBits <= 59 {
			meaningful = (w << 5) >> (64 - uint(sigBits))
		} else {
			meaningful = bitstream.PeekBits64(cs.data, cs.bitPos+5) >> (64 - uint(sigBits))
		}
		cs.bitPos += 5 + sigBits
		cs.prevValue ^= meaningful
		cs.prevFloat = math.Float64frombits(cs.prevValue)
	}

	return true
}

// ChimpCursor incrementally decodes a Chimp XOR stream without a count cap.
// The zero value is not usable; construct with NewChimpCursor.
type ChimpCursor struct {
	state chimpState
}

// ChimpValState incrementally decodes a Chimp-compressed value stream.
// The zero value is not usable; construct with NewChimpValState.
type ChimpValState struct {
	state     chimpState
	remaining int
}

// NewChimpCursor initializes an uncapped cursor from data.
func NewChimpCursor(data []byte) (ChimpCursor, bool) {
	state, ok := newChimpState(data)

	return ChimpCursor{state: state}, ok
}

// NewChimpValState initializes the state from the value payload, consuming
// the uncompressed first value (available via Val immediately). Returns false
// if the payload is too short.
func NewChimpValState(valData []byte) (ChimpValState, bool) {
	state, ok := newChimpState(valData)

	return ChimpValState{state: state, remaining: math.MaxInt}, ok
}

// First returns the first value consumed while constructing the cursor.
func (c ChimpCursor) First() float64 {
	return c.state.prevFloat
}

// Next decodes and returns the next value.
func (c *ChimpCursor) Next() (float64, bool) {
	if !decodeChimpValue(&c.state) {
		return 0, false
	}

	return c.state.prevFloat, true
}

// SetCount constrains the state to exactly count values total, including the
// first value already consumed by NewChimpValState.
func (s *ChimpValState) SetCount(count int) {
	if count > 1 {
		s.remaining = count - 1
	} else {
		s.remaining = 0
	}
}

// Next decodes the next value. It returns false when the stream is exhausted,
// corrupted, or the SetCount limit has been reached.
func (s *ChimpValState) Next() bool {
	if s.remaining <= 0 || !decodeChimpValue(&s.state) {
		return false
	}
	s.remaining--

	return true
}

// Val returns the most recently decoded value.
func (s *ChimpValState) Val() float64 {
	return s.state.prevFloat
}

// chimpDecodeNext reads a 2-bit flag and decodes the next Chimp-compressed value,
// updating prevValue and storedLeading in place.
// Returns true on success, false on read failure or invalid data.
func chimpDecodeNext(reader *bitstream.Reader, prevValue *uint64, storedLeading *int) bool {
	flag, ok := reader.Read2Bits()
	if !ok {
		return false
	}

	switch flag {
	case 0: // Value unchanged
		*storedLeading = 65

	case 1: // Trailing-zero optimized
		header, ok := readChimpTrailingHeader(reader)
		if !ok {
			return false
		}

		leading := chimpLeadingDecode[header>>6]

		sigBits := int(header & 0x3F)
		if sigBits == 0 {
			sigBits = 64
		}

		meaningful, ok := reader.ReadBits(sigBits)
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

		meaningful, ok := reader.ReadBits(sigBits)
		if !ok {
			return false
		}

		*prevValue ^= meaningful

	case 3: // New leading
		leadingBucket, ok := reader.Read3Bits()
		if !ok {
			return false
		}

		leading := chimpLeadingDecode[leadingBucket]
		*storedLeading = leading
		sigBits := 64 - leading

		meaningful, ok := reader.ReadBits(sigBits)
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
func readChimpTrailingHeader(reader *bitstream.Reader) (uint64, bool) {
	return reader.ReadBits(9)
}
