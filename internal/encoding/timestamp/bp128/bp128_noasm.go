//go:build !amd64

package bp128

// On non-amd64 builds the block kernels are always the scalar reference.

func bp128PackBlock(out []uint64, block []uint64, w int) []uint64 {
	return bp128PackBlockScalar(out, block, w)
}

func bp128UnpackBlock(dst []uint64, packed []uint64, w int) int {
	return bp128UnpackBlockScalar(dst, packed, w)
}
