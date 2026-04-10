//go:build goexperiment.simd && amd64

package arch

import "simd/archsimd"

// X86ArchSIMDHasAVX2 reports whether the Go experimental SIMD API's AVX2
// support is available on the current x86 CPU.
func X86ArchSIMDHasAVX2() bool {
	return archsimd.X86.AVX2()
}

// X86ArchSIMDHasAVX512 reports whether the Go experimental SIMD API's AVX-512
// support is available on the current x86 CPU.
func X86ArchSIMDHasAVX512() bool {
	return archsimd.X86.AVX512()
}
