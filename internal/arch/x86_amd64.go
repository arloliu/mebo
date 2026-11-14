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

// X86HasAVX512DQ reports whether the current x86 CPU supports the AVX-512
// Foundation plus the Doubleword-and-Quadword (DQ) extension. DQ is what
// provides the packed float64<->int64 conversions (VCVTTPD2QQ / VCVTQQ2PD)
// the ALP encode verify kernel needs; Foundation covers the multiplies,
// masked stores, and packed-qword min/max. Real CPUs that report DQ always
// report F, but both are checked so the gate never enables the kernel on a
// core missing either half.
func X86HasAVX512DQ() bool {
	return cpu.X86.HasAVX512F && cpu.X86.HasAVX512DQ
}

// X86HasAVX512BW reports whether the current x86 CPU supports the AVX-512 Foundation plus the Byte-and-Word (BW) extension.
// BW is what provides the 512-bit byte shuffle (VPSHUFB) the Chimp group decode kernel reverses its window bytes with.
// Real CPUs that report BW always report F,
// but both are checked so the gate never enables the kernel on a core missing either half.
func X86HasAVX512BW() bool {
	return cpu.X86.HasAVX512F && cpu.X86.HasAVX512BW
}

// X86HasPOPCNT reports whether the current x86 CPU supports the POPCNT instruction.
func X86HasPOPCNT() bool {
	return cpu.X86.HasPOPCNT
}

// X86HasAVX512VBMI reports whether the current x86 CPU supports the AVX-512
// baseline plus the VBMI extension (VPERMB byte-granular cross-lane permute).
func X86HasAVX512VBMI() bool {
	return cpu.X86.HasAVX512 && cpu.X86.HasAVX512BW && cpu.X86.HasAVX512VBMI
}
