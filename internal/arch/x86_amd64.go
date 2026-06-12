//go:build amd64

package arch

import "golang.org/x/sys/cpu"

// X86HasAVX2 reports whether the current x86 CPU supports AVX2 instructions.
func X86HasAVX2() bool {
	return cpu.X86.HasAVX2
}

// X86HasAVX512 reports whether the current x86 CPU supports AVX-512 instructions.
func X86HasAVX512() bool {
	return cpu.X86.HasAVX512
}

// X86HasAVX512VBMI reports whether the current x86 CPU supports the AVX-512
// baseline plus the VBMI extension (VPERMB byte-granular cross-lane permute).
func X86HasAVX512VBMI() bool {
	return cpu.X86.HasAVX512 && cpu.X86.HasAVX512BW && cpu.X86.HasAVX512VBMI
}
