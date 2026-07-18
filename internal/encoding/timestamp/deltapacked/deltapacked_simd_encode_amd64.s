#include "textflag.h"

// func encodeDeltaPackedGroupsASMAVX2(
//     dst []byte,    FP+0..23
//     src []int64,   FP+24..47
//     nGroups int,   FP+48
// ) int              FP+56
//
// Register allocation:
//   DI  = output pointer (advances per group)
//   SI  = source pointer (advances by 32 per group)
//   R8  = groups remaining counter
//   R9  = &tagSpreadTable
//   R10 = &groupVarintWidthsU8
//   R13 = initial output pointer (for computing bytes written)
//   Y13 = broadcast 0 (constant)
//   Y14 = broadcast 0xFF (constant)
//   Y15 = broadcast 0xFFFF (constant)
//   Y0  = loaded DoDs / zigzag values
//   Y1-Y5 = scratch for classification
//   AX,BX,CX = scratch for mask extraction and control byte
//   R11 = scratch for value packing
TEXT ·encodeDeltaPackedGroupsASMAVX2(SB), NOSPLIT, $32-64
	MOVQ dst_base+0(FP), DI
	MOVQ src_base+24(FP), SI
	MOVQ nGroups+48(FP), R8

	TESTQ R8, R8
	JLE   doneZero

	// Save initial output pointer for computing bytes written
	MOVQ DI, R13

	// Load table addresses
	LEAQ ·tagSpreadTable(SB), R9
	LEAQ ·groupVarintWidthsU8(SB), R10

	// Initialize SIMD constants
	VPXOR Y13, Y13, Y13

	MOVQ    $0xFF, AX
	VMOVQ   AX, X14
	VPBROADCASTQ X14, Y14

	MOVQ    $0xFFFF, AX
	VMOVQ   AX, X15
	VPBROADCASTQ X15, Y15

groupLoop:
	// === Load 4 delta-of-delta values ===
	VMOVDQU (SI), Y0

	// === Zigzag encode ===
	// zigzag = (dod << 1) ^ sign_extend(dod)
	// sign_extend = (0 > dod) ? -1 : 0
	VPCMPGTQ Y0, Y13, Y2    // Y2 = (0 > dod) per lane → sign mask
	VPSLLQ   $1, Y0, Y1     // Y1 = dod << 1
	VPXOR    Y2, Y1, Y0     // Y0 = zigzag = (dod << 1) ^ sign_mask

	// === Tag classification (unsigned-safe) ===
	// Check if value exceeds 4 bytes: high 32 bits non-zero
	VPSRLQ   $32, Y0, Y1    // Y1 = zigzag >> 32
	VPCMPGTQ Y13, Y1, Y5    // Y5 = (Y1 > 0) → exceeds 4 bytes

	// Signed comparison for smaller thresholds (correct when high 32 = 0)
	VPCMPGTQ Y14, Y0, Y3    // Y3 = (zigzag > 0xFF) signed
	VPCMPGTQ Y15, Y0, Y4    // Y4 = (zigzag > 0xFFFF) signed

	// Fix signed comparison for large values (high 32 bits set → sign bit issue)
	VPOR Y5, Y3, Y3         // if exceeds 4 bytes, also exceeds 0xFF
	VPOR Y5, Y4, Y4         // if exceeds 4 bytes, also exceeds 0xFFFF

	// === Build control byte ===
	// Extract 4-bit masks from comparison results
	VMOVMSKPD Y3, AX        // AX = 4 bits: which lanes > 0xFF
	VMOVMSKPD Y4, BX        // BX = 4 bits: which lanes > 0xFFFF
	VMOVMSKPD Y5, CX        // CX = 4 bits: which lanes > 4 bytes

	// Spread 4-bit masks to 2-bit-per-lane control byte positions and sum
	// tagSpreadTable[mask] places bit i at position 2*i
	MOVBQZX (R9)(AX*1), AX  // spread exceeds_1byte
	MOVBQZX (R9)(BX*1), BX  // spread exceeds_2byte
	MOVBQZX (R9)(CX*1), CX  // spread exceeds_4byte
	ADDL    BX, AX
	ADDL    CX, AX           // AX = control byte

	// === Store zigzag values to stack for scalar extraction ===
	VMOVDQU Y0, 0(SP)

	// === Pack to output ===
	// Write control byte
	MOVB AX, (DI)
	ADDQ $1, DI

	// Lane 0: tag = cb & 0x03
	MOVQ    0(SP), R11
	MOVQ    R11, (DI)        // write 8 bytes (LE, overwrite is OK)
	MOVL    AX, BX
	ANDL    $3, BX
	MOVBQZX (R10)(BX*1), BX
	ADDQ    BX, DI

	// Lane 1: tag = (cb >> 2) & 0x03
	MOVQ    8(SP), R11
	MOVQ    R11, (DI)
	MOVL    AX, BX
	SHRL    $2, BX
	ANDL    $3, BX
	MOVBQZX (R10)(BX*1), BX
	ADDQ    BX, DI

	// Lane 2: tag = (cb >> 4) & 0x03
	MOVQ    16(SP), R11
	MOVQ    R11, (DI)
	MOVL    AX, BX
	SHRL    $4, BX
	ANDL    $3, BX
	MOVBQZX (R10)(BX*1), BX
	ADDQ    BX, DI

	// Lane 3: tag = (cb >> 6) & 0x03
	MOVQ    24(SP), R11
	MOVQ    R11, (DI)
	MOVL    AX, BX
	SHRL    $6, BX
	ANDL    $3, BX
	MOVBQZX (R10)(BX*1), BX
	ADDQ    BX, DI

	// === Advance source and loop ===
	ADDQ $32, SI
	SUBQ $1, R8
	JG   groupLoop

	// === Return bytes written ===
	MOVQ DI, AX
	SUBQ R13, AX
	MOVQ AX, ret+56(FP)
	VZEROUPPER
	RET

doneZero:
	MOVQ $0, ret+56(FP)
	RET
