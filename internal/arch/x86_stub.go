//go:build !amd64

package arch

// X86HasAVX2 reports whether the current x86 CPU supports AVX2 instructions.
// Always returns false on non-amd64 platforms.
func X86HasAVX2() bool {
	return false
}

// X86HasAVX512 reports whether the current x86 CPU supports AVX-512 instructions.
// Always returns false on non-amd64 platforms.
func X86HasAVX512() bool {
	return false
}
