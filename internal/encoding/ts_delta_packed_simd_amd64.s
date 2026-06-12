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

// func decodeDeltaPackedASMAVX512BulkPairs(
//     dst []int64,                 FP+0..23
//     data []byte,                 FP+24..47
//     nGroups int,                 FP+48
//     totalBytesTable *[256]uint8, FP+56
//     validMasks *[256]uint32,     FP+64
//     prevTS int64,                FP+72
//     prevDelta int64,             FP+80
// ) (consumed int, produced int, lastTS int64, lastDelta int64)
//      FP+88       FP+96          FP+104       FP+112
//
// Decodes 2 Group Varint groups (8 values) per iteration:
//   - one 64-byte ZMM load covers both payloads (and the second control byte)
//   - zeroing-masked VPERMB expands both groups to 8 little-endian uint64 lanes
//     (VPERMB has no 0x80 sentinel, so invalid lanes are zeroed via K1)
//   - vectorized zigzag using VPSRAQ (64-bit arithmetic shift, AVX-512 only)
//   - two 8-wide prefix sums (VALIGNQ+VPADDQ, 3 steps each) produce deltas
//     then timestamps; carries stay broadcast in Z8/Z9 across iterations
//
// Exits to the caller when fewer than 2 groups remain, the 64-byte load
// window would cross the end of data, or a pair's combined payload exceeds
// 63 bytes (both groups near-maximal width; the AVX2/scalar path finishes).
//
// Requires AVX-512 F+BW+VBMI (gated by arch.X86HasAVX512VBMI).
TEXT ·decodeDeltaPackedASMAVX512BulkPairs(SB), NOSPLIT, $0-120
	MOVQ dst_base+0(FP), DI
	MOVQ data_base+24(FP), SI
	MOVQ data_len+32(FP), DX
	MOVQ nGroups+48(FP), R8
	MOVQ totalBytesTable+56(FP), R9
	MOVQ validMasks+64(FP), R10
	MOVQ SI, R13
	XORQ R14, R14

	// Z8 = broadcast(prevTS), Z9 = broadcast(prevDelta)
	MOVQ prevTS+72(FP), AX
	VPBROADCASTQ AX, Z8
	MOVQ prevDelta+80(FP), AX
	VPBROADCASTQ AX, Z9

	// Z10 = 0, Z11 = broadcast(7) for lane-7 extraction
	VPXORQ Z10, Z10, Z10
	MOVQ $7, AX
	VPBROADCASTQ AX, Z11

	// K2 selects the high 32 bytes (group 1 lane indices)
	MOVQ $0xFFFFFFFF00000000, AX
	KMOVQ AX, K2

	// BP = shuffle table base (loop invariant)
	LEAQ ·deltaPackedDecodeShuffles(SB), BP

pairLoop512:
	CMPQ R8, $2
	JLT  done512

	// Window check: SI-base + 1 + 64 <= dataLen
	MOVQ SI, AX
	SUBQ R13, AX
	ADDQ $65, AX
	CMPQ AX, DX
	JGT  done512

	// cb0 / tb0
	MOVBQZX (SI), CX
	MOVBQZX (R9)(CX*1), BX

	// cb1 at SI + 1 + tb0 (inside the checked window) / tb1
	LEAQ 1(SI)(BX*1), AX
	MOVBQZX (AX), R11
	MOVBQZX (R9)(R11*1), R12

	// Pair payload must fit the 64-byte window: tb0 + tb1 <= 63
	LEAQ (BX)(R12*1), AX
	CMPQ AX, $63
	JGT  done512

	// Z0 = 64-byte payload window starting at payload0
	VMOVDQU64 1(SI), Z0

	// Z3 = combined VPERMB index vector:
	//   low 32B  = shuffles[cb0]
	//   high 32B = shuffles[cb1] + (tb0 + 1)   (cb1 byte sits between payloads)
	MOVQ CX, AX
	SHLQ $5, AX
	VMOVDQU (BP)(AX*1), Y3
	MOVQ R11, AX
	SHLQ $5, AX
	VMOVDQU (BP)(AX*1), Y4
	VINSERTI64X4 $1, Y4, Z3, Z3
	LEAQ 1(BX), AX
	VPBROADCASTB AX, Z5
	VPADDB Z5, Z3, K2, Z3

	// K1 = valid-byte mask: valid[cb0] | valid[cb1]<<32
	MOVLQZX (R10)(CX*4), AX
	MOVLQZX (R10)(R11*4), R15
	SHLQ $32, R15
	ORQ  R15, AX
	KMOVQ AX, K1

	// Z1 = 8 zero-extended little-endian uint64 zigzag values
	VPERMB.Z Z0, Z3, K1, Z1

	// Zigzag decode: dod = (v >> 1) ^ -(v & 1)
	VPSRLQ $1, Z1, Z5
	VPSLLQ $63, Z1, Z6
	VPSRAQ $63, Z6, Z6
	VPXORQ Z6, Z5, Z5

	// Prefix sum #1: deltas = inclusive_prefix(dod) + broadcast(prevDelta)
	VALIGNQ $7, Z10, Z5, Z6
	VPADDQ Z6, Z5, Z5
	VALIGNQ $6, Z10, Z5, Z6
	VPADDQ Z6, Z5, Z5
	VALIGNQ $4, Z10, Z5, Z6
	VPADDQ Z6, Z5, Z5
	VPADDQ Z9, Z5, Z12

	// Prefix sum #2: ts = inclusive_prefix(deltas) + broadcast(prevTS)
	VALIGNQ $7, Z10, Z12, Z6
	VPADDQ Z6, Z12, Z7
	VALIGNQ $6, Z10, Z7, Z6
	VPADDQ Z6, Z7, Z7
	VALIGNQ $4, Z10, Z7, Z6
	VPADDQ Z6, Z7, Z7
	VPADDQ Z8, Z7, Z13

	VMOVDQU64 Z13, (DI)

	// Carries: broadcast lane 7 of deltas/timestamps for the next iteration
	VPERMQ Z12, Z11, Z9
	VPERMQ Z13, Z11, Z8

	// Advance: 2 control bytes + both payloads; 8 values written
	LEAQ 2(BX)(R12*1), AX
	ADDQ AX, SI
	ADDQ $64, DI
	ADDQ $8, R14
	SUBQ $2, R8
	JMP  pairLoop512

done512:
	MOVQ SI, AX
	SUBQ R13, AX
	MOVQ AX, consumed+88(FP)
	MOVQ R14, produced+96(FP)
	MOVQ X8, AX
	MOVQ AX, lastTS+104(FP)
	MOVQ X9, AX
	MOVQ AX, lastDelta+112(FP)
	VZEROUPPER
	RET
