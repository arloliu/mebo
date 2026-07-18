package varint

import "encoding/binary"

// DecodeU64 decodes an unsigned LEB128 value from data starting at offset.
//
// It returns the decoded value, the offset immediately after it, and true on
// success. On truncated or unterminated input, it returns the original offset
// and false. It does not allocate.
func DecodeU64(data []byte, offset int) (uint64, int, bool) {
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

// DecodeZigZag64 decodes a zigzag-encoded int64 value without branches.
func DecodeZigZag64(value uint64) int64 {
	return int64((value >> 1) ^ -(value & 1)) //nolint:gosec
}
