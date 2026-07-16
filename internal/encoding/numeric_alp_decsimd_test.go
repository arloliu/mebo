//go:build amd64

package encoding

import (
	"encoding/binary"
	"math"
	"math/rand"
	"testing"

	"github.com/arloliu/mebo/internal/arch"
)

// ---------------------------------------------------------------------------
// AVX-512 fused ALP-main decode kernel — differential test.
//
// The kernel (alpFusedDecodeAVX512 -> alpFusedDecodeAVX512Asm) must reproduce
// the scalar decode of decodeMainInto bit-for-bit (math.Float64bits equality)
// for the whole-group prefix it decodes; the caller finishes the remainder and
// the <8-value tail in Go. This test calls the kernel function DIRECTLY under
// the arch gate (so it exercises the real asm on AVX-512DQ hardware whether or
// not the dispatch is wired) and reconstructs the exact caller contract: kernel
// prefix + scalar tail, compared to a pure scalar reference over all N values.
//
// Coverage: every width 1..64 (including the {59,61,62,63} lane-straddle widths
// the two-gather funnel must handle) x counts spanning full groups and odd
// tails (n%8 != 0) x adversarial FOR minimums (incl. MinInt64/MaxInt64 wrap)
// x pf/ie scale combos x random codes (masked to width, so max-width all-ones
// values are exercised). A tight-buffer variant witnesses the wrapper's gather
// over-read bound is memory-safe (no fault, still bit-equal).
// ---------------------------------------------------------------------------

// alpDecodeTailScalar decodes values [start,n) exactly as decodeMainInto's
// scalar remainder loop does, so the test reproduces the real caller contract
// (kernel prefix + Go tail) rather than short-circuiting the seam.
func alpDecodeTailScalar(codes []byte, start, n, width int, mn int64, pf, ie float64, dst []float64) {
	mask := maskW(width)
	clen := len(codes)
	bitpos := start * width
	for i := start; i < n; i++ {
		bp := bitpos >> 3
		sh := bitpos & 7
		var code uint64
		if bp+8 <= clen && sh+width <= 64 {
			code = (binary.LittleEndian.Uint64(codes[bp:bp+8]) >> sh) & mask
		} else {
			code = alpReadBitsSlow(codes, bitpos, width, mask)
		}
		dst[i] = float64(int64(code)+mn) * pf * ie
		bitpos += width
	}
}

func TestALPFusedDecodeAVX512_Differential(t *testing.T) {
	if !arch.X86HasAVX512DQ() {
		t.Skip("AVX-512DQ not available on this CPU; kernel path cannot be exercised")
	}

	rng := rand.New(rand.NewSource(20260716))
	counts := []int{8, 9, 15, 16, 17, 24, 31, 32, 64, 255, 256, 1000, 1024}
	mns := []int64{0, 42, -1234567, math.MinInt64, math.MaxInt64, math.MinInt64 / 3}
	scales := []struct{ pf, ie float64 }{
		{1, 1},
		{alpPow10[2], alpInvPow10[0]},  // 2dp-like
		{alpPow10[7], alpInvPow10[4]},  // full-precision-like
		{alpPow10[18], alpInvPow10[0]}, // extreme magnitude
	}

	const poison = 0xDEADBEEFCAFEBABE

	for w := 1; w <= 64; w++ {
		mask := maskW(w)
		// Over-read slack the asm gather needs beyond the exact ceil(n*w/8)
		// packed bytes: hi qword of the last lane plus its 8-byte load.
		pad := ((7 * w) >> 3) + 16 + 8
		for _, n := range counts {
			codes := make([]uint64, n)
			for i := range codes {
				codes[i] = rng.Uint64() & mask
			}
			packed := alpPackBits(nil, codes, w) // exactly ceil(n*w/8) bytes

			// (a) padded buffer: over-read region is real, noise-filled memory,
			// so the kernel covers the maximum number of groups.
			padded := make([]byte, len(packed)+pad)
			copy(padded, packed)
			for i := len(packed); i < len(padded); i++ {
				padded[i] = byte(rng.Intn(256))
			}

			for _, mn := range mns {
				for _, s := range scales {
					for variant, buf := range [][]byte{padded, packed} {
						got := make([]float64, n)
						want := make([]float64, n)
						for i := range got {
							got[i] = math.Float64frombits(poison)
						}

						nk := alpFusedDecodeAVX512(buf, n, w, mn, s.pf, s.ie, got)
						if nk%8 != 0 || nk < 0 || nk > n {
							t.Fatalf("w=%d n=%d variant=%d: bad decoded count %d", w, n, variant, nk)
						}
						// Finish the tail exactly as the caller would.
						alpDecodeTailScalar(buf, nk, n, w, mn, s.pf, s.ie, got)

						// Pure scalar reference over all N. alpDecodeCodesScalarRef is
						// itself pinned bit-equal to the generated kernels (alpFusedUnpack)
						// by the pre-existing TestALPKernel_Differential, so this asm-vs-oracle
						// comparison transitively proves asm-vs-generated-kernels equivalence.
						alpDecodeCodesScalarRef(buf, n, w, mn, s.pf, s.ie, want)

						for i := 0; i < n; i++ {
							if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
								t.Fatalf("w=%d n=%d mn=%d pf=%g ie=%g variant=%d nk=%d idx=%d: got %#016x want %#016x",
									w, n, mn, s.pf, s.ie, variant, nk, i,
									math.Float64bits(got[i]), math.Float64bits(want[i]))
							}
						}
					}
				}
			}
		}
	}
}

// TestALPFusedDecodeAVX512_Guards pins the wrapper's decline paths: sub-group
// counts, out-of-range widths, and buffers too short for even one group's
// gather must return 0 (caller decodes everything scalar) and never fault.
func TestALPFusedDecodeAVX512_Guards(t *testing.T) {
	if !arch.X86HasAVX512DQ() {
		t.Skip("AVX-512DQ not available on this CPU")
	}
	dst := make([]float64, 64)
	big := make([]byte, 4096)

	if got := alpFusedDecodeAVX512(big, 7, 8, 0, 1, 1, dst); got != 0 {
		t.Fatalf("n<8 should decline, got %d", got)
	}
	if got := alpFusedDecodeAVX512(big, 64, 0, 0, 1, 1, dst); got != 0 {
		t.Fatalf("width 0 should decline, got %d", got)
	}
	if got := alpFusedDecodeAVX512(big, 64, 65, 0, 1, 1, dst); got != 0 {
		t.Fatalf("width>64 should decline, got %d", got)
	}
	// Buffer too short for a single width-64 group's two-gather reach.
	short := make([]byte, 8)
	if got := alpFusedDecodeAVX512(short, 8, 64, 0, 1, 1, dst); got != 0 {
		t.Fatalf("short buffer should decline, got %d", got)
	}
}
