//go:build amd64

package encoding

import (
	"math/rand"
	"strconv"
	"testing"
)

// BenchmarkALPDecodeKernelWidths isolates the AVX-512 decode kernel's wire/park
// decision: for a spread of code widths it times the fused kernel against the
// generated scalar kernel (numeric_alp_kernels_gen.go) that decodeMainInto
// currently dispatches to, over the same N=1024 whole-group column. The
// per-width asm/scalar ratio (and its geomean) is what the 1.5x wire gate is
// measured against — the high-level BenchmarkALPDecode* benchmarks only cover
// the one or two widths a corpus happens to produce.
func BenchmarkALPDecodeKernelWidths(b *testing.B) {
	const n = 1024
	widths := []int{8, 12, 16, 20, 24, 32, 40, 48, 56, 64}
	rng := rand.New(rand.NewSource(424242))
	mn := int64(-98765)
	pf, ie := alpPow10[2], alpInvPow10[0]

	for _, w := range widths {
		mask := maskW(w)
		codes := make([]uint64, n)
		for i := range codes {
			codes[i] = rng.Uint64() & mask
		}
		packed := alpPackBits(nil, codes, w)
		buf := make([]byte, len(packed)+((7*w)>>3)+24)
		copy(buf, packed)
		dst := make([]float64, n)
		kern := alpFusedUnpack[w]

		ws := strconv.Itoa(w)
		b.Run("w="+ws+"/scalar", func(b *testing.B) {
			for b.Loop() {
				kern(buf, n, mn, pf, ie, dst)
			}
		})
		b.Run("w="+ws+"/asm", func(b *testing.B) {
			for b.Loop() {
				alpFusedDecodeAVX512(buf, n, w, mn, pf, ie, dst)
			}
		})
	}
}
