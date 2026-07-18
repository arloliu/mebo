//go:build amd64

package alp

import "github.com/arloliu/mebo/internal/arch"

// alpFusedDecodeAVX512Asm is the hand-written AVX-512DQ fused unpack+decode
// kernel backing ALP-main bulk decode. It processes exactly `groups` full
// 8-value groups (groups*8 values), gathering each lane's covering qword(s),
// funnel-extracting the width-bit code, adding the FOR minimum, and scaling by
// pf then ie — the exact scalar decode, bit-for-bit. The Go wrapper below owns
// the runtime gate and the gather over-read bound.
//
//go:noescape
func alpFusedDecodeAVX512Asm(codes *byte, groups int, width int, mn int64, pf, ie float64, dst *float64)

// alpFusedDecodeAVX512 decodes as many whole 8-value groups of an ALP-main
// column as the AVX-512 kernel can safely cover and returns the number of
// values written (a multiple of 8); the caller finishes the remainder and the
// <8-value tail in Go, exactly as with the generated scalar kernels. It returns
// 0 (kernel declined; caller falls back entirely) when the CPU lacks AVX-512DQ,
// when width is out of the 1..64 kernel range, when there is not a full group,
// or when even the first group's gather would over-read past codes.
//
// Over-read bound: the last processed group g reads a hi qword at
// base + ((7*width)>>3) + 8, an 8-byte load, so it touches up to
// g*width + ((7*width)>>3) + 16 bytes (exclusive) of codes. groups is capped so
// that stays within len(codes), mirroring decodeMainInto's slack bound for the
// scalar kernels (just with the larger two-gather reach).
//
// The kernel is currently PARKED (unwired): decodeMainInto still dispatches to
// the generated scalar kernels. On Ryzen 9 9950X3D (AVX-512DQ), the focused
// BenchmarkALPDecodeKernelWidths (count=5 medians) measured this two-gather
// kernel at 0.74x-1.07x the generated scalar kernels across widths 8..64
// (geomean 0.87x — i.e. ~13% SLOWER on average): the VPGATHERQQ pair per
// 8-value group costs more than the fully-unrolled constant-shift scalar
// loads, even amortized over 8 lanes. That is far below the 1.5x wire gate set
// for this kernel (2026-07-16), so the dispatch is left unchanged (BP128-style
// park). The .s + these stubs stay in-tree, differentially validated bit-equal
// against the scalar path for all widths 1..64
// (TestALPFusedDecodeAVX512_Differential), so the decision can be revisited on
// hardware with faster qword gathers — or a gather-free shuffle-based unpack —
// without re-deriving the kernel.
func alpFusedDecodeAVX512(codes []byte, n int, width int, mn int64, pf, ie float64, dst []float64) int {
	if width < 1 || width > 64 || n < 8 || !arch.X86HasAVX512DQ() {
		return 0
	}

	clen := len(codes)
	hiExtra := ((7 * width) >> 3) + 16
	if clen < hiExtra {
		return 0
	}

	groups := n >> 3
	if g := (clen-hiExtra)/width + 1; g < groups {
		groups = g
	}
	if groups <= 0 {
		return 0
	}

	alpFusedDecodeAVX512Asm(&codes[0], groups, width, mn, pf, ie, &dst[0])

	return groups << 3
}
