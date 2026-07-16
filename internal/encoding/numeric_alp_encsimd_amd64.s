#include "textflag.h"

// func alpMainStatsAVX512(values *float64, nBlock int, factors *[4]float64,
//     dst *uint64, blockMask *uint64, minOut *int64, maxOut *int64) uint64
//
// factors holds {pe, iff, pf, ie}. Processes nBlock 8-lane blocks. Register map:
//   SI  = values ptr        (advanced +64/block)
//   DI  = dst ptr           (advanced +64/block)
//   BX  = blockMask ptr     (advanced +8/block)
//   CX  = block counter
//   R12 = anyBad accumulator (OR of every block's exc|guard byte)
//   Z16..Z19 = broadcast pe, iff, pf, ie
//   Z20 = fast-round magic (0x1.8p52), Z21 = 2^51 threshold, Z22 = abs mask
//   Z23 = min accumulator (init MaxInt64), Z24 = max accumulator (init MinInt64)
//   Z0..Z5 = per-block scratch, K1..K3 = per-block masks
TEXT ·alpMainStatsAVX512(SB), NOSPLIT, $0-64
	MOVQ values+0(FP), SI
	MOVQ nBlock+8(FP), CX
	MOVQ dst+24(FP), DI
	MOVQ blockMask+32(FP), BX

	// Broadcast the four multiply factors {pe, iff, pf, ie} (bit patterns, so
	// VPBROADCASTQ works on the raw float64 bits).
	MOVQ         factors+16(FP), DX
	MOVQ         0(DX), AX
	VPBROADCASTQ AX, Z16
	MOVQ         8(DX), AX
	VPBROADCASTQ AX, Z17
	MOVQ         16(DX), AX
	VPBROADCASTQ AX, Z18
	MOVQ         24(DX), AX
	VPBROADCASTQ AX, Z19

	// magic = 0x1.8p52 = 2^52 + 2^51 (bits 0x4338000000000000).
	MOVQ         $0x4338000000000000, AX
	VPBROADCASTQ AX, Z20
	// threshold = 2^51 (bits 0x4320000000000000).
	MOVQ         $0x4320000000000000, AX
	VPBROADCASTQ AX, Z21
	// abs mask = clear sign bit = 0x7FFFFFFFFFFFFFFF (also MaxInt64 for min).
	MOVQ         $0x7FFFFFFFFFFFFFFF, AX
	VPBROADCASTQ AX, Z22
	VPBROADCASTQ AX, Z23
	// max accumulator init = MinInt64 = 0x8000000000000000.
	MOVQ         $1, AX
	SHLQ         $63, AX
	VPBROADCASTQ AX, Z24

	XORQ  R12, R12
	TESTQ CX, CX
	JEQ   done

loop:
	VMOVUPD (SI), Z0             // Z0 = 8 input values
	ADDQ    $64, SI

	VMULPD Z16, Z0, Z1           // Z1 = v * pe
	VMULPD Z17, Z1, Z1           // Z1 = scaled = (v*pe) * iff
	VANDPD Z22, Z1, Z2           // Z2 = |scaled|

	VCMPPD $0x11, Z21, Z2, K1    // K1 = (|scaled| < 2^51)  [LT_OQ: NaN -> 0]

	VADDPD     Z20, Z1, Z3       // Z3 = scaled + magic
	VSUBPD     Z20, Z3, Z3       // Z3 = fastRound(scaled)
	VCVTTPD2QQ Z3, Z4            // Z4 = int64 truncate(round)   [AVX512DQ]

	VCVTQQ2PD Z4, Z5            // Z5 = float64(digit)          [AVX512DQ]
	VMULPD    Z18, Z5, Z5        // Z5 = digit * pf
	VMULPD    Z19, Z5, Z5        // Z5 = verify-back = (digit*pf) * ie
	VPCMPEQQ  Z0, Z5, K2         // K2 = (verify-back bits == v bits)

	KANDW K1, K2, K3            // K3 = good = inDomain & verify

	VMOVDQU64 Z4, K3, (DI)       // store good-lane digits only
	ADDQ      $64, DI
	VPMINSQ   Z4, Z23, K3, Z23   // min accumulate over good lanes
	VPMAXSQ   Z4, Z24, K3, Z24   // max accumulate over good lanes

	// Build the block mask word from K1 (inDomain) and K2 (verify).
	KMOVW K1, AX                // AX = inDomain bits (low 8)
	KMOVW K2, DX                // DX = verify bits

	MOVL DX, R8                 // exc = inDomain & ~verify
	NOTL R8
	ANDL AX, R8
	ANDL $0xFF, R8

	MOVL AX, R9                 // guard = ~inDomain (8 lanes)
	NOTL R9
	ANDL $0xFF, R9

	MOVL R9, R10                // maskword = (guard << 8) | exc
	SHLL $8, R10
	ORL  R8, R10
	MOVQ R10, (BX)
	ADDQ $8, BX

	MOVL R8, R11               // anyBad |= exc | guard
	ORL  R9, R11
	ORQ  R11, R12

	DECQ CX
	JNZ  loop

done:
	MOVQ      minOut+40(FP), AX
	VMOVDQU64 Z23, (AX)
	MOVQ      maxOut+48(FP), AX
	VMOVDQU64 Z24, (AX)
	MOVQ      R12, ret+56(FP)
	VZEROUPPER
	RET
