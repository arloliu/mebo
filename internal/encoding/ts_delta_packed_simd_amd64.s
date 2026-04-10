#include "textflag.h"

// deltaPackedDecodeMeta struct layout (size=41):
//   +0: totalBytes uint8
//   +1: lengths [4]uint8
//   +5: offsets [4]uint8
//   +9: shuffle [32]byte  (unused by this function)
#define META_TOTAL_BYTES  0
#define META_LENGTHS      1
#define META_OFFSETS      5
#define META_SIZE        41

// func decodeDeltaPackedASMAVX2BulkGroups(
//     dst []int64,                FP+0..23
//     data []byte,                FP+24..47
//     nGroups int,                FP+48
//     table *[256]decodeMeta,     FP+56
//     totalBytesTable *[256]uint8,FP+64  (unused: totalBytes is in struct)
//     prevTS int64,               FP+72
//     prevDelta int64,            FP+80
// ) (consumed int, produced int, lastTS int64, lastDelta int64)
//      FP+88       FP+96          FP+104       FP+112
TEXT ·decodeDeltaPackedASMAVX2BulkGroups(SB), NOSPLIT, $32-120
	MOVQ dst_base+0(FP), DI
	MOVQ data_base+24(FP), SI
	MOVQ data_len+32(FP), DX
	MOVQ nGroups+48(FP), R8
	MOVQ table+56(FP), R9
	// totalBytesTable+64 unused
	MOVQ prevTS+72(FP), R11
	MOVQ prevDelta+80(FP), R12
	MOVQ SI, R13
	XORQ R14, R14

	TESTQ R8, R8
	JLE done

groupLoop:
	// Bounds: need 1 control byte + at least 4 payload bytes minimum.
	MOVQ SI, AX
	SUBQ R13, AX
	ADDQ $5, AX
	CMPQ AX, DX
	JGT done

	// cb = *SI; entry = table + cb*41
	MOVBQZX (SI), CX
	MOVQ CX, AX
	IMULQ $META_SIZE, AX
	LEAQ (R9)(AX*1), R10

	// totalBytes bounds check: SI + 1 + totalBytes <= data end
	MOVBQZX META_TOTAL_BYTES(R10), BX
	MOVQ SI, AX
	ADDQ $1, AX
	ADDQ BX, AX
	SUBQ R13, AX
	CMPQ AX, DX
	JGT done

	// Vector path: payload start must have a safe 32-byte readable window.
	MOVQ SI, AX
	SUBQ R13, AX
	ADDQ $33, AX
	CMPQ AX, DX
	JGT scalarGroup

	VMOVDQU 1(SI), Y0
	VPERM2I128 $0x00, Y0, Y0, Y1
	VPERM2I128 $0x11, Y0, Y0, Y2
	SHLQ $5, CX
	LEAQ ·deltaPackedDecodeShufflesLoDup(SB), AX
	VMOVDQU (AX)(CX*1), Y3
	LEAQ ·deltaPackedDecodeShufflesHiDup(SB), AX
	VMOVDQU (AX)(CX*1), Y4
	VPSHUFB Y3, Y1, Y5
	VPSHUFB Y4, Y2, Y6
	VPOR Y6, Y5, Y5

	// Zigzag decode: decoded = (v >> 1) ^ -(v & 1)
	// AVX2 lacks VPSRAQ (64-bit arithmetic right shift). We emulate
	// -(v & 1) by shifting bit 0 to the sign position and using
	// VPCMPGTQ against zero to broadcast it across 64 bits.
	VPSRLQ   $1, Y5, Y0      // Y0 = v >> 1 (logical right shift)
	VPSLLQ   $63, Y5, Y1     // Y1 = v << 63 (bit 0 at sign position)
	VPXOR    Y6, Y6, Y6      // Y6 = 0
	VPCMPGTQ Y1, Y6, Y5      // Y5 = (0 > (v<<63)) = -1 if bit0=1, else 0
	VPXOR    Y0, Y5, Y5      // Y5 = (v >> 1) ^ -(v & 1) = decoded

	VMOVDQU Y5, 0(SP)

	MOVQ 0(SP), AX
	ADDQ AX, R12
	ADDQ R12, R11
	MOVQ R11, (DI)

	MOVQ 8(SP), AX
	ADDQ AX, R12
	ADDQ R12, R11
	MOVQ R11, 8(DI)

	MOVQ 16(SP), AX
	ADDQ AX, R12
	ADDQ R12, R11
	MOVQ R11, 16(DI)

	MOVQ 24(SP), AX
	ADDQ AX, R12
	ADDQ R12, R11
	MOVQ R11, 24(DI)

	MOVBQZX META_TOTAL_BYTES(R10), AX
	ADDQ $1, SI
	ADDQ AX, SI
	ADDQ $32, DI
	ADDQ $4, R14
	SUBQ $1, R8
	JG groupLoop
	JMP done

scalarGroup:
	LEAQ 1(SI), BP

	MOVBQZX META_OFFSETS+0(R10), AX
	MOVBQZX META_LENGTHS+0(R10), BX
	ADDQ BP, AX
	CMPQ BX, $1
	JE l0_1
	CMPQ BX, $2
	JE l0_2
	CMPQ BX, $4
	JE l0_4
	MOVQ (AX), R15
	JMP l0_done
l0_1:
	MOVBQZX (AX), R15
	JMP l0_done
l0_2:
	MOVWQZX (AX), R15
	JMP l0_done
l0_4:
	MOVLQZX (AX), R15
l0_done:
	MOVQ R15, AX
	SHRQ $1, AX
	ANDQ $1, R15
	NEGQ R15
	XORQ R15, AX
	ADDQ AX, R12
	ADDQ R12, R11
	MOVQ R11, (DI)

	MOVBQZX META_OFFSETS+1(R10), AX
	MOVBQZX META_LENGTHS+1(R10), BX
	ADDQ BP, AX
	CMPQ BX, $1
	JE l1_1
	CMPQ BX, $2
	JE l1_2
	CMPQ BX, $4
	JE l1_4
	MOVQ (AX), R15
	JMP l1_done
l1_1:
	MOVBQZX (AX), R15
	JMP l1_done
l1_2:
	MOVWQZX (AX), R15
	JMP l1_done
l1_4:
	MOVLQZX (AX), R15
l1_done:
	MOVQ R15, AX
	SHRQ $1, AX
	ANDQ $1, R15
	NEGQ R15
	XORQ R15, AX
	ADDQ AX, R12
	ADDQ R12, R11
	MOVQ R11, 8(DI)

	MOVBQZX META_OFFSETS+2(R10), AX
	MOVBQZX META_LENGTHS+2(R10), BX
	ADDQ BP, AX
	CMPQ BX, $1
	JE l2_1
	CMPQ BX, $2
	JE l2_2
	CMPQ BX, $4
	JE l2_4
	MOVQ (AX), R15
	JMP l2_done
l2_1:
	MOVBQZX (AX), R15
	JMP l2_done
l2_2:
	MOVWQZX (AX), R15
	JMP l2_done
l2_4:
	MOVLQZX (AX), R15
l2_done:
	MOVQ R15, AX
	SHRQ $1, AX
	ANDQ $1, R15
	NEGQ R15
	XORQ R15, AX
	ADDQ AX, R12
	ADDQ R12, R11
	MOVQ R11, 16(DI)

	MOVBQZX META_OFFSETS+3(R10), AX
	MOVBQZX META_LENGTHS+3(R10), BX
	ADDQ BP, AX
	CMPQ BX, $1
	JE l3_1
	CMPQ BX, $2
	JE l3_2
	CMPQ BX, $4
	JE l3_4
	MOVQ (AX), R15
	JMP l3_done
l3_1:
	MOVBQZX (AX), R15
	JMP l3_done
l3_2:
	MOVWQZX (AX), R15
	JMP l3_done
l3_4:
	MOVLQZX (AX), R15
l3_done:
	MOVQ R15, AX
	SHRQ $1, AX
	ANDQ $1, R15
	NEGQ R15
	XORQ R15, AX
	ADDQ AX, R12
	ADDQ R12, R11
	MOVQ R11, 24(DI)

	MOVBQZX META_TOTAL_BYTES(R10), AX
	ADDQ $1, SI
	ADDQ AX, SI
	ADDQ $32, DI
	ADDQ $4, R14
	SUBQ $1, R8
	JG groupLoop

done:
	MOVQ SI, AX
	SUBQ R13, AX
	MOVQ AX, consumed+88(FP)
	MOVQ R14, produced+96(FP)
	MOVQ R11, lastTS+104(FP)
	MOVQ R12, lastDelta+112(FP)
	VZEROUPPER
	RET
