#include "textflag.h"

// Lane iota [0,1,2,3,4,5,6,7] as int64, used to build the per-lane bit
// positions (iota*width) from which the per-lane shifts are derived.
DATA alpDecIota8<>+0(SB)/8, $0
DATA alpDecIota8<>+8(SB)/8, $1
DATA alpDecIota8<>+16(SB)/8, $2
DATA alpDecIota8<>+24(SB)/8, $3
DATA alpDecIota8<>+32(SB)/8, $4
DATA alpDecIota8<>+40(SB)/8, $5
DATA alpDecIota8<>+48(SB)/8, $6
DATA alpDecIota8<>+56(SB)/8, $7
GLOBL alpDecIota8<>(SB), RODATA|NOPTR, $64

// func alpFusedDecodeAVX512Asm(codes *byte, groups int, width int, mn int64,
//     pf, ie float64, dst *float64, idx *[64]byte)
//
// Decodes exactly `groups` groups of 8 ALP-main codes (groups*8 values) from
// the LSB-first, byte-aligned code stream into dst as
//     float64(int64(code)+mn) * pf * ie
// bit-identical to the scalar generated kernels. Group g of 8 values occupies
// exactly `width` bytes starting at byte g*width; lane j (value 8g+j) starts at
// bit j*width within that window, so the per-lane byte offsets (j*width)>>3 and
// bit shifts (j*width)&7 are constant across every group.
//
// Per group, one masked byte load reads the group's own `width` bytes (the
// byte mask is the width mask: its low `width` bits are set; the other bytes
// are zeroed, and their faults suppressed). One VPERMB then gives each lane the 8 bytes starting at its byte
// offset (idx, built by the Go wrapper), so a variable right shift and the
// width mask extract the code. This needs every lane's code to fit in the qword
// at its byte offset, (j*width)&7 + width <= 64, which holds for every width
// except 59 and 61..63; the Go wrapper declines those.
//
// Register map:
//   SI  = current group base pointer (codes + g*width; advanced +width/group)
//   DI  = dst pointer                (advanced +64/group)
//   CX  = group counter
//   DX  = width                      (kept for the SI advance)
//   AX  = scratch (mask/broadcast building)
//   Z0  = lane iota [0..7]  Z13/Z14/Z16 = setup scratch (free after the prologue)
//   Z3  = per-lane right shifts (bit)
//   Z5  = width mask broadcast    Z6 = mn broadcast
//   Z7  = pf bits broadcast       Z8 = ie bits broadcast
//   Z9  = loaded group bytes      Z11/Z12 = arithmetic scratch
//   Z20 = VPERMB byte index table
//   K3  = byte mask of the group's `width` bytes
TEXT ·alpFusedDecodeAVX512Asm(SB), NOSPLIT, $0-64
	MOVQ codes+0(FP), SI
	MOVQ width+16(FP), DX

	// shift[j] = (iota[j] * width) & 7  (AVX-512DQ VPMULLQ).
	VMOVDQU64    alpDecIota8<>(SB), Z0
	VPBROADCASTQ DX, Z13
	VPMULLQ      Z13, Z0, Z14
	MOVQ         $7, AX
	VPBROADCASTQ AX, Z16
	VPANDQ       Z16, Z14, Z3

	// mask = (1<<width)-1, or all-ones when width==64.
	MOVQ $-1, AX
	CMPQ DX, $64
	JGE  maskDone
	MOVQ $1, AX
	MOVQ DX, CX                      // CL = width (1..63)
	SHLQ CL, AX
	DECQ AX

maskDone:
	VPBROADCASTQ AX, Z5
	KMOVQ        AX, K3              // the low `width` bytes of a group [AVX512BW]

	// Broadcast mn (int64) and the raw float64 bit patterns of pf, ie.
	MOVQ         mn+24(FP), AX
	VPBROADCASTQ AX, Z6
	MOVQ         pf+32(FP), AX
	VPBROADCASTQ AX, Z7
	MOVQ         ie+40(FP), AX
	VPBROADCASTQ AX, Z8

	MOVQ     idx+56(FP), AX
	VMOVDQU8 (AX), Z20

	MOVQ  dst+48(FP), DI
	MOVQ  groups+8(FP), CX
	TESTQ CX, CX
	JEQ   done

loop:
	VMOVDQU8.Z (SI), K3, Z9          // the group's width bytes        [AVX512BW]
	VPERMB     Z9, Z20, Z11          // lane j <- bytes off_j..off_j+7 [AVX512VBMI]
	VPSRLVQ    Z3, Z11, Z11          // >> shift
	VPANDQ     Z5, Z11, Z11          // & width mask
	VPADDQ     Z6, Z11, Z11          // + mn  (int64, wrapping)
	VCVTQQ2PD  Z11, Z12              // float64(int64(code)+mn)   [AVX512DQ]
	VMULPD     Z7, Z12, Z12          // * pf
	VMULPD     Z8, Z12, Z12          // * ie   (two multiplies, never FMA)
	VMOVUPD    Z12, (DI)

	ADDQ $64, DI
	ADDQ DX, SI                      // base += width
	DECQ CX
	JNZ  loop

done:
	VZEROUPPER
	RET
