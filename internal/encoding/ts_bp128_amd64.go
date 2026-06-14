//go:build amd64

package encoding

import "github.com/arloliu/mebo/internal/arch"

func bp128PackBlock(out []uint64, block []uint64, w int) []uint64 {
	if w == 0 || !arch.X86HasAVX512() {
		return bp128PackBlockScalar(out, block, w)
	}

	nWords := bp128WordsPerBlock(w)
	baseLen := len(out)
	need := baseLen + nWords
	if cap(out) < need {
		next := make([]uint64, baseLen, need)
		copy(next, out)
		out = next
	}
	out = out[:need]
	gotWords := bp128PackBlockAVX512(&out[baseLen], &block[0], w)

	return out[:baseLen+gotWords]
}

func bp128UnpackBlock(dst []uint64, packed []uint64, w int) int {
	if w == 0 || !arch.X86HasAVX512() {
		return bp128UnpackBlockScalar(dst, packed, w)
	}

	bp128UnpackBlockAVX512(&dst[0], &packed[0], w)

	return bp128WordsPerBlock(w)
}
