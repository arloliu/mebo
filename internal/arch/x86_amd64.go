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
