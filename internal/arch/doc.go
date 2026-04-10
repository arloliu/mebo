// Package arch provides CPU architecture and SIMD capability detection.
//
// It offers two detection APIs:
//
//   - X86HasAVX2 / X86HasAVX512: stable detection via golang.org/x/sys/cpu,
//     used to gate hand-written assembly paths.
//   - X86ArchSIMDHasAVX2 / X86ArchSIMDHasAVX512: detection via the
//     experimental Go SIMD API (simd/archsimd), used to gate the
//     GOEXPERIMENT=simd pure-Go SIMD paths.
package arch
