//go:build !goexperiment.simd || !amd64

package arch

// X86ArchSIMDHasAVX2 reports whether the Go experimental SIMD API's AVX2
// support is available. Always returns false when GOEXPERIMENT=simd is not
// active or on non-amd64 platforms.
func X86ArchSIMDHasAVX2() bool {
	return false
}

// X86ArchSIMDHasAVX512 reports whether the Go experimental SIMD API's AVX-512
// support is available. Always returns false when GOEXPERIMENT=simd is not
// active or on non-amd64 platforms.
func X86ArchSIMDHasAVX512() bool {
	return false
}
