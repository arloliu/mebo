#include "textflag.h"

// Lane iota [0,1,2,3,4,5,6,7] as int64, used to build the per-lane bit
// positions (iota*width) from which the byte offsets and shifts are derived.
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
//     pf, ie float64, dst *float64)
//
// Decodes exactly `groups` groups of 8 ALP-main codes (groups*8 values) from
// the LSB-first, byte-aligned code stream into dst as
//     float64(int64(code)+mn) * pf * ie
// bit-identical to the scalar generated kernels. Group g of 8 values occupies
// exactly `width` bytes starting at byte g*width; lane j (value 8g+j) starts at
// bit j*width within that window, so the per-lane byte offsets (j*width)>>3 and
// bit shifts (j*width)&7 are constant across every group and are computed once
// below. The Go wrapper (alp_decsimd_amd64.go) bounds `groups` so the
// gather over-read stays inside `codes`.
//
// Per group each lane's w-bit code is assembled with a funnel: a qword gathered
// at the lane's byte offset (lo) plus a qword gathered 8 bytes further (hi),
// combined as (lo >> shift) | (hi << (64-shift)) then masked to width bits. For
// non-straddling lanes hi's contribution lands above bit width and is masked
// away; for the few widths whose lanes straddle a 64-bit boundary (59,61,62,63)
// hi supplies the code's top bits. shift==0 lanes get hi<<64 == 0 (AVX-512
// logical shift counts >= 64 yield 0), so the same funnel is correct for all
// widths 1..64.
//
// Register map:
//   SI  = current group base pointer (codes + g*width; advanced +width/group)
//   DI  = dst pointer                (advanced +64/group)
//   CX  = group counter
//   DX  = width                      (kept for the SI advance)
//   AX  = scratch (mask/broadcast building)
//   Z0  = lane iota [0..7] (loaded from alpDecIota8 rodata; consumed by the
//         VPMULLQ prologue that derives bitpos, then dead)
//   Z1  = per-lane lo gather byte offsets  (VSIB index)
//   Z2  = per-lane hi gather byte offsets  (lo + 8)
//   Z3  = per-lane right shifts   (bit)
//   Z4  = per-lane left  shifts   (64 - shift)
//   Z5  = width mask broadcast    Z6 = mn broadcast
//   Z7  = pf bits broadcast       Z8 = ie bits broadcast
//   Z9  = lo gather dst   Z10 = hi gather dst   Z11/Z12 = arithmetic scratch
//   Z13..Z17 = setup scratch (free after the prologue)
//   K1/K2    = gather masks (reset all-ones before every gather)
TEXT ·alpFusedDecodeAVX512Asm(SB), NOSPLIT, $0-56
	MOVQ codes+0(FP), SI
	MOVQ width+16(FP), DX

	// bitpos[j] = iota[j] * width  (AVX-512DQ VPMULLQ).
	VMOVDQU64    alpDecIota8<>(SB), Z0
	VPBROADCASTQ DX, Z13
	VPMULLQ      Z13, Z0, Z14        // Z14 = bitpos

	// lo byte offsets = bitpos >> 3; hi byte offsets = lo + 8.
	VPSRLQ       $3, Z14, Z1
	MOVQ         $8, AX
	VPBROADCASTQ AX, Z15
	VPADDQ       Z15, Z1, Z2

	// shift = bitpos & 7; leftShift = 64 - shift.
	MOVQ         $7, AX
	VPBROADCASTQ AX, Z16
	VPANDQ       Z16, Z14, Z3
	MOVQ         $64, AX
	VPBROADCASTQ AX, Z17
	VPSUBQ       Z3, Z17, Z4         // Z4 = 64 - shift

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

	// Broadcast mn (int64) and the raw float64 bit patterns of pf, ie.
	MOVQ         mn+24(FP), AX
	VPBROADCASTQ AX, Z6
	MOVQ         pf+32(FP), AX
	VPBROADCASTQ AX, Z7
	MOVQ         ie+40(FP), AX
	VPBROADCASTQ AX, Z8

	MOVQ  dst+48(FP), DI
	MOVQ  groups+8(FP), CX
	TESTQ CX, CX
	JEQ   done

loop:
	KXNORW     K1, K1, K1
	VPGATHERQQ (SI)(Z1*1), K1, Z9    // lo qwords
	KXNORW     K2, K2, K2
	VPGATHERQQ (SI)(Z2*1), K2, Z10   // hi qwords

	VPSRLVQ   Z3, Z9, Z11            // lo >> shift
	VPSLLVQ   Z4, Z10, Z12           // hi << (64-shift)
	VPORQ     Z12, Z11, Z11          // funnel-combined
	VPANDQ    Z5, Z11, Z11           // & width mask
	VPADDQ    Z6, Z11, Z11           // + mn  (int64, wrapping)
	VCVTQQ2PD Z11, Z12               // float64(int64(code)+mn)   [AVX512DQ]
	VMULPD    Z7, Z12, Z12           // * pf
	VMULPD    Z8, Z12, Z12           // * ie   (two multiplies, never FMA)
	VMOVUPD   Z12, (DI)

	ADDQ $64, DI
	ADDQ DX, SI                      // base += width
	DECQ CX
	JNZ  loop

done:
	VZEROUPPER
	RET
