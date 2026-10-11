//go:build amd64

package alp

import "unsafe"

// alpFusedDecodeAVX512Asm is the hand-written AVX-512 fused unpack+decode kernel backing ALP-main bulk decode
// (needs AVX-512 DQ, BW and VBMI).
// It processes exactly `groups` full 8-value groups (groups*8 values):
// a masked load of each group's `width` bytes,
// a VPERMB that moves each lane's covering qword into place,
// then shift, width mask, the FOR minimum, and the pf then ie scaling,
// the exact scalar decode, bit-for-bit.
// The Go wrapper below owns the runtime gate, the width check, and the group bound.
//
//go:noescape
func alpFusedDecodeAVX512Asm(codes *byte, groups int, width int, mn int64, pf, ie float64, dst *float64, idx *[64]byte)

// alpDecPageSize is the smallest page size on amd64:
// memory is mapped in whole pages of at least this size, aligned to it.
const alpDecPageSize = 4096

// alpDecPermIdx[w] is the kernel's VPERMB index table for width w:
// byte k of lane j selects group byte (j*w)>>3 + k, so lane j receives the qword starting at its code.
// Entries are only built for widths alpDecPermFits accepts, where every index stays below 64.
var alpDecPermIdx = alpBuildDecPermIdx()

func alpBuildDecPermIdx() *[65][64]byte {
	var t [65][64]byte
	for w := 1; w <= 64; w++ {
		if !alpDecPermFits(w) {
			continue
		}
		for j := range 8 {
			off := (j * w) >> 3
			for k := range 8 {
				t[w][j*8+k] = byte(off + k) //nolint:gosec // off+k <= 63 for every width alpDecPermFits accepts
			}
		}
	}

	return &t
}

// alpDecPermFits reports whether every lane's width-bit code lies within the qword at the lane's byte offset,
// (j*width)&7 + width <= 64 for lanes 0..7.
// The kernel extracts one qword per lane, so it handles exactly these widths: 1..58, 60 and 64.
func alpDecPermFits(width int) bool {
	for j := range 8 {
		if (j*width)&7+width > 64 {
			return false
		}
	}

	return true
}

// alpFusedDecodeAVX512 decodes as many whole 8-value groups of an ALP-main column as the AVX-512 kernel can cover,
// and returns the number of values written (a multiple of 8);
// the caller finishes the remainder and the <8-value tail in Go.
// It returns 0 (kernel declined; the caller falls back to the generated scalar kernels)
// when alpDecAVX512 is off, when width is outside what the kernel handles (see alpDecPermFits),
// or when there is not a full group in n or in codes.
//
// Bounds: each group's masked load reads exactly its own width bytes,
// so groups is capped at len(codes)/width and no loaded byte lies past codes;
// it is capped at len(dst)/8 too, so the kernel's stores stay inside dst.
// The load still spans 64 bytes, and its masked-off bytes past codes are only fault-suppressed.
// Suppressing a fault costs a microcode assist (about 60 ns per load on Zen 5),
// so groups is also capped to keep every 64-byte span within the 4 KiB page holding the last byte of codes,
// which is mapped; the masked loads then never touch an unmapped page.
// Codes ending less than 64 bytes before a page boundary leave their last few groups to the scalar loop.
func alpFusedDecodeAVX512(codes []byte, n int, width int, mn int64, pf, ie float64, dst []float64) int {
	if !alpDecAVX512 || width < 1 || width > 64 || n < 8 || !alpDecPermFits(width) {
		return 0
	}

	groups := min(min(n, len(dst))>>3, len(codes)/width)
	if groups <= 0 {
		return 0
	}

	// Group g spans [base+g*width, base+g*width+64).
	base := uintptr(unsafe.Pointer(unsafe.SliceData(codes)))
	pageEnd := ((base + uintptr(len(codes)) - 1) | (alpDecPageSize - 1)) + 1
	if room := int(pageEnd-base) - 64; room < (groups-1)*width {
		if room < 0 {
			return 0
		}
		groups = room/width + 1
	}

	//nolint:gosec // groups > 0 means len(codes) >= width >= 1 and len(dst) >= 8
	alpFusedDecodeAVX512Asm(&codes[0], groups, width, mn, pf, ie, &dst[0], &alpDecPermIdx[width])

	return groups << 3
}
