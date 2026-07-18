// Package bitstream provides allocation-free bit readers for XOR value codecs.
package bitstream

import "encoding/binary"

// Reader reads most-significant-bit-first values from a byte slice.
type Reader struct {
	data     []byte
	bytePos  int
	bitBuf   uint64
	bitCount int
}

// NewReader creates a Reader for data.
func NewReader(data []byte) *Reader {
	return &Reader{data: data}
}

// PeekBits64 returns up to 64 bits starting at absolute bit position bitPos,
// MSB-aligned. Bits beyond the end of data read as zero.
func PeekBits64(data []byte, bitPos int) uint64 {
	i := bitPos >> 3
	s := uint(bitPos & 7) //nolint:gosec // G115: bit widths/counts bounded to 0..64

	if i+9 <= len(data) {
		return binary.BigEndian.Uint64(data[i:])<<s | uint64(data[i+8])>>(8-s)
	}

	var tmp [9]byte
	if i < len(data) {
		copy(tmp[:], data[i:])
	}

	return binary.BigEndian.Uint64(tmp[:8])<<s | uint64(tmp[8])>>(8-s)
}

// ReadBit reads one bit from the stream.
func (reader *Reader) ReadBit() (uint64, bool) {
	if reader.bitCount == 0 {
		if !reader.fillBuffer() {
			return 0, false
		}
	}

	bit := reader.bitBuf >> 63
	reader.bitBuf <<= 1
	reader.bitCount--

	return bit, true
}

// Read2Bits reads exactly two bits from the stream.
func (reader *Reader) Read2Bits() (uint64, bool) {
	if reader.bitCount >= 2 {
		reader.bitCount -= 2
		value := (reader.bitBuf >> 62) & 0x03
		reader.bitBuf <<= 2

		return value, true
	}

	return reader.ReadBits(2)
}

// Read3Bits reads exactly three bits from the stream.
func (reader *Reader) Read3Bits() (uint64, bool) {
	if reader.bitCount >= 3 {
		reader.bitCount -= 3
		value := (reader.bitBuf >> 61) & 0x07
		reader.bitBuf <<= 3

		return value, true
	}

	return reader.ReadBits(3)
}

// Read5Bits reads exactly five bits from the stream.
func (reader *Reader) Read5Bits() (int, bool) {
	if reader.bitCount >= 5 {
		reader.bitCount -= 5
		value := int((reader.bitBuf >> 59) & 0x1F) //nolint:gosec // bit width is bounded to 5
		reader.bitBuf <<= 5

		return value, true
	}

	value, ok := reader.ReadBits(5)

	return int(value), ok //nolint:gosec // bit width is bounded to 5
}

// Read6Bits reads exactly six bits from the stream.
func (reader *Reader) Read6Bits() (int, bool) {
	if reader.bitCount >= 6 {
		reader.bitCount -= 6
		value := int((reader.bitBuf >> 58) & 0x3F) //nolint:gosec // bit width is bounded to 6
		reader.bitBuf <<= 6

		return value, true
	}

	value, ok := reader.ReadBits(6)

	return int(value), ok //nolint:gosec // bit width is bounded to 6
}

// ReadBits reads numBits bits from the stream. It returns false when the
// stream does not contain enough bits.
func (reader *Reader) ReadBits(numBits int) (uint64, bool) {
	if numBits == 0 {
		return 0, true
	}

	if numBits <= reader.bitCount {
		shift := 64 - numBits
		result := reader.bitBuf >> shift
		reader.bitBuf <<= numBits
		reader.bitCount -= numBits

		return result, true
	}

	var result uint64
	firstRead := true

	for numBits > 0 {
		if reader.bitCount == 0 {
			if !reader.fillBuffer() {
				return 0, false
			}
		}

		bitsToRead := min(numBits, reader.bitCount)
		shift := 64 - bitsToRead
		shiftedBits := reader.bitBuf >> shift

		if firstRead {
			result = shiftedBits
			firstRead = false
		} else {
			result = (result << bitsToRead) | shiftedBits
		}

		reader.bitBuf <<= bitsToRead
		reader.bitCount -= bitsToRead
		numBits -= bitsToRead
	}

	return result, true
}

// ConsumedBytes returns the number of complete or partial bytes read so far.
func (reader *Reader) ConsumedBytes() int {
	consumedBits := reader.bytePos*8 - reader.bitCount

	return (consumedBits + 7) / 8
}

func (reader *Reader) fillBuffer() bool {
	if reader.bytePos >= len(reader.data) {
		return false
	}

	bytesAvailable := len(reader.data) - reader.bytePos
	bytesToRead := min(8, bytesAvailable)
	if bytesToRead == 8 {
		reader.bitBuf = binary.BigEndian.Uint64(reader.data[reader.bytePos : reader.bytePos+8])
		reader.bytePos += 8
		reader.bitCount = 64

		return true
	}

	reader.bitBuf = 0
	for range bytesToRead {
		reader.bitBuf = (reader.bitBuf << 8) | uint64(reader.data[reader.bytePos])
		reader.bytePos++
	}

	reader.bitBuf <<= (8 - bytesToRead) * 8
	reader.bitCount = bytesToRead * 8

	return true
}
