//go:build amd64

package encoding

import "github.com/arloliu/mebo/internal/arch"

// tagSpreadTable maps a 4-bit mask to an 8-bit value with bits spread to even positions.
// Used to build Group Varint control bytes from SIMD threshold comparison results.
//
// tagSpreadTable[0b_dcba] = (a << 0) | (b << 2) | (c << 4) | (d << 6)
//
// This converts per-lane "exceeds threshold" bitmasks into the control byte layout
// where each lane occupies a 2-bit field.
//
//nolint:unused // referenced by assembly in ts_delta_packed_simd_encode_amd64.s
var tagSpreadTable = [16]byte{
	0x00, 0x01, 0x04, 0x05,
	0x10, 0x11, 0x14, 0x15,
	0x40, 0x41, 0x44, 0x45,
	0x50, 0x51, 0x54, 0x55,
}

// groupVarintWidthsU8 maps 2-bit tag to byte width (uint8) for assembly use.
// Identical to groupVarintLengths but typed as [4]byte for direct assembly MOVBQZX.
//
//nolint:unused // referenced by assembly in ts_delta_packed_simd_encode_amd64.s
var groupVarintWidthsU8 = [4]byte{1, 2, 4, 8}

// encodeDeltaPackedGroupsASMAVX2 encodes pre-computed delta-of-delta int64 values into
// Group Varint format using AVX2 SIMD for zigzag encoding and tag classification.
//
// Parameters:
//   - dst: output buffer, must have capacity >= nGroups * 33 bytes
//   - src: delta-of-delta int64 values, len must be >= nGroups * 4
//   - nGroups: number of 4-value groups to encode
//
// Returns: bytes written to dst
//
//go:noescape
func encodeDeltaPackedGroupsASMAVX2(dst []byte, src []int64, nGroups int) int

var activeDeltaPackedEncodeUseSIMD = arch.X86HasAVX2()

// encodeDeltaPackedGroupsSIMD dispatches to the SIMD encode kernel when available.
func encodeDeltaPackedGroupsSIMD(dst []byte, dods []int64, nGroups int) int {
	if activeDeltaPackedEncodeUseSIMD {
		return encodeDeltaPackedGroupsASMAVX2(dst, dods, nGroups)
	}

	return encodeDeltaPackedGroupsScalar(dst, dods, nGroups)
}

// hasDeltaPackedEncodeSIMD reports whether SIMD-accelerated encoding is available.
func hasDeltaPackedEncodeSIMD() bool {
	return activeDeltaPackedEncodeUseSIMD
}
