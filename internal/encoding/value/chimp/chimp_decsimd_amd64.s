#include "textflag.h"
#include "go_asm.h"

// Byte order reversal of every qword, as a VPSHUFB control: the stream is big-endian.
DATA chimpGroupBswap<>+0(SB)/8, $0x0001020304050607
DATA chimpGroupBswap<>+8(SB)/8, $0x08090A0B0C0D0E0F
DATA chimpGroupBswap<>+16(SB)/8, $0x0001020304050607
DATA chimpGroupBswap<>+24(SB)/8, $0x08090A0B0C0D0E0F
DATA chimpGroupBswap<>+32(SB)/8, $0x0001020304050607
DATA chimpGroupBswap<>+40(SB)/8, $0x08090A0B0C0D0E0F
DATA chimpGroupBswap<>+48(SB)/8, $0x0001020304050607
DATA chimpGroupBswap<>+56(SB)/8, $0x08090A0B0C0D0E0F
GLOBL chimpGroupBswap<>(SB), RODATA|NOPTR, $64

// func chimpGroupDecodeAVX512Asm(st *chimpGroupState, steps int, out *uint64) int
//
// Decodes up to `steps` values from each of eight Chimp columns at once, one column per 64-bit lane,
// and returns the number of steps it completed. Row r of out receives the eight values of step r.
// The columns may sit in eight different buffers: every lane has its own base pointer in st,
// and its bit position and end are counted from that base.
//
// One step, for all lanes: load the 64-bit window at the lane's bit position
// (two gathered qwords at base + pos>>3, shifted by pos&7, so 16 bytes are read per lane),
// take the 2-bit flag, the 3-bit leading bucket and the 6-bit significant-bit count from its top,
// pick the header and payload widths by flag, XOR the payload into the previous value, store the row,
// advance the position and update the stored leading count.
//
//   flag 00  value unchanged                      header 2,  payload 0
//   flag 01  bucket, count, payload, zero tail    header 11, payload count (0 meaning 64)
//   flag 10  payload under the stored leading     header 2,  payload 64 - stored
//   flag 11  bucket, payload                      header 5,  payload 64 - leading
//
// The loop leaves before committing a step that any lane cannot take:
// a header and payload wider than the 64-bit window, a flag 10 with no stored leading,
// a negative count of trailing zeros, or a next position past the lane's end.
// It does not decide what such a value is; the caller's scalar loop does.
// On return st.pos, st.prev and st.stored hold the state after the completed steps,
// which is the state of NumericChimpDecoder.DecodeAll's loop after the same number of values.
// st.base and st.end are not written.
//
// Memory safety. The gathers have no base register: their indices are the lanes' absolute addresses.
// Those addresses exist only in Z31 (the bases) and Z4 (base + byte offset) while this function runs,
// and nothing derived from one is written to st, to out, or returned.
// The collector sees the eight pointers in st.base, which the caller keeps reachable, and never moves heap objects.
// This function is a NOSPLIT leaf without a CALL and without a frame, so it has no stack-growth check,
// and the runtime does not preempt assembly asynchronously
// (isAsyncSafePoint in runtime/preempt.go, read for Go 1.25 and Go 1.26; read it again for any later release).
// So no safe point occurs between loading the bases and the last gather, and no stack can move meanwhile.
// The caller keeps 16 readable bytes of the lane's own buffer after every column's end.
//
// Needs AVX-512 F, BW (VPSHUFB on 512 bits) and DQ (VPMOVQ2M).
//
// Register map:
//   BX  = st                 DI = out row pointer (advanced 64 per step)
//   CX  = steps left         DX = steps asked     SI = 0, the gathers' base register
//   Z0  = pos (bits from the lane's base)         Z1 = prev      Z2 = stored leading
//   Z3  = end (bits)         Z31 = base addresses
//   Z4  = address of the lane's current byte      Z5 = bit offset inside that byte
//   Z6  = window, then the payload                Z7/Z8 = window scratch
//   Z9  = flag               Z10 = leading of the bucket         Z11 = 6-bit count
//   Z12 = header width       Z13 = 64 - leading   Z14 = payload width   Z15 = 64 - stored
//   Z27 = trailing zeros     Z28 = header + payload              Z29 = next position   Z30 = scratch
//   Z16 = 7   Z17 = 64   Z18 = byte reversal   Z19 = leading table   Z20 = header width table
//   Z21 = 0xFF   Z22 = 0x38   Z23 = 63   Z24 = 1   Z25 = 65   Z26 = 2
//   K3  = flags 01 and 11    K4 = flags 10 and 11   K5 = flag 01   K6/K7 = reasons to leave
TEXT ·chimpGroupDecodeAVX512Asm(SB), NOSPLIT, $0-32
	MOVQ st+0(FP), BX
	MOVQ steps+8(FP), CX
	MOVQ out+16(FP), DI
	MOVQ CX, DX
	XORQ SI, SI

	VMOVDQU64 chimpGroupState_base(BX), Z31
	VMOVDQU64 chimpGroupState_pos(BX), Z0
	VMOVDQU64 chimpGroupState_end(BX), Z3
	VMOVDQU64 chimpGroupState_prev(BX), Z1
	VMOVDQU64 chimpGroupState_stored(BX), Z2

	MOVQ         $7, AX
	VPBROADCASTQ AX, Z16
	MOVQ         $64, AX
	VPBROADCASTQ AX, Z17
	VMOVDQU64    chimpGroupBswap<>(SB), Z18
	MOVQ         $0x18161412100C0800, AX // leading zeros of buckets 0..7, one per byte
	VPBROADCASTQ AX, Z19
	MOVQ         $0x05020B02, AX         // header widths of flags 0..3, one per byte
	VPBROADCASTQ AX, Z20
	MOVQ         $0xFF, AX
	VPBROADCASTQ AX, Z21
	MOVQ         $0x38, AX
	VPBROADCASTQ AX, Z22
	MOVQ         $63, AX
	VPBROADCASTQ AX, Z23
	MOVQ         $1, AX
	VPBROADCASTQ AX, Z24
	MOVQ         $65, AX
	VPBROADCASTQ AX, Z25
	MOVQ         $2, AX
	VPBROADCASTQ AX, Z26

	TESTQ CX, CX
	JEQ   done

loop:
	// 64-bit window of every lane at its bit position.
	VPSRLQ     $3, Z0, Z4
	VPADDQ     Z31, Z4, Z4                // address of the lane's current byte
	VPANDQ     Z16, Z0, Z5                // bit offset inside the byte
	KXNORW     K1, K1, K1
	VPGATHERQQ (SI)(Z4*1), K1, Z6         // SI is zero
	KXNORW     K2, K2, K2
	VPGATHERQQ 8(SI)(Z4*1), K2, Z7
	VPSHUFB    Z18, Z6, Z6
	VPSHUFB    Z18, Z7, Z7
	VPSLLVQ    Z5, Z6, Z6
	VPSUBQ     Z5, Z17, Z8
	VPSRLVQ    Z8, Z7, Z7                 // a count of 64 gives 0
	VPORQ      Z7, Z6, Z6                 // the window

	// Header fields.
	VPSRLQ   $62, Z6, Z9                  // flag
	VPSRLQ   $56, Z6, Z10
	VPANDQ   Z22, Z10, Z10                // bucket * 8
	VPSRLVQ  Z10, Z19, Z10
	VPANDQ   Z21, Z10, Z10                // leading of the bucket
	VPSRLQ   $53, Z6, Z11
	VPSUBQ   Z24, Z11, Z11
	VPANDQ   Z23, Z11, Z11
	VPADDQ   Z24, Z11, Z11                // 6-bit count, 0 meaning 64
	VPSLLQ   $3, Z9, Z12
	VPSRLVQ  Z12, Z20, Z12
	VPANDQ   Z21, Z12, Z12                // header width
	VPTESTMQ Z24, Z9, K3                  // flags 01 and 11
	VPTESTMQ Z26, Z9, K4                  // flags 10 and 11

	// Payload width, by flag: 0, the 6-bit count, 64 - stored, 64 - leading.
	VPSUBQ      Z10, Z17, Z13             // 64 - leading
	VPBLENDMQ   Z13, Z11, K4, Z14
	VPSUBQ      Z2, Z17, Z15              // 64 - stored
	VMOVDQA64.Z Z15, K4, Z15
	VPBLENDMQ   Z14, Z15, K3, Z14         // payload width

	// Trailing zeros, flag 01 only.
	VPSUBQ      Z11, Z13, Z27
	KANDNW      K3, K4, K5
	VMOVDQA64.Z Z27, K5, Z27

	VPADDQ Z12, Z14, Z28                  // header + payload
	VPADDQ Z28, Z0, Z29                   // next position

	// Leave before committing a step any lane cannot take.
	VPCMPUQ  $6, Z17, Z28, K6             // wider than the window
	VPCMPUQ  $6, Z17, Z14, K7             // flag 10 with no stored leading
	KORW     K6, K7, K6
	VPMOVQ2M Z27, K7                      // negative trailing count
	KORW     K6, K7, K6
	VPCMPUQ  $6, Z3, Z29, K7              // past the lane's end
	KORW     K6, K7, K6
	KORTESTW K6, K6
	JNZ      done

	VPSLLVQ   Z12, Z6, Z6
	VPSUBQ    Z14, Z17, Z30
	VPSRLVQ   Z30, Z6, Z6                 // a count of 64 gives 0: the unchanged flag
	VPSLLVQ   Z27, Z6, Z6
	VPXORQ    Z6, Z1, Z1
	VMOVDQU64 Z1, (DI)
	ADDQ      $64, DI

	VMOVDQA64 Z10, K3, Z2                 // flag 11 takes the bucket's leading
	VPBLENDMQ Z2, Z25, K4, Z2             // flags 00 and 01 drop it
	VMOVDQA64 Z29, Z0

	DECQ CX
	JNZ  loop

done:
	VMOVDQU64 Z0, chimpGroupState_pos(BX)
	VMOVDQU64 Z1, chimpGroupState_prev(BX)
	VMOVDQU64 Z2, chimpGroupState_stored(BX)
	SUBQ      CX, DX
	VZEROUPPER
	MOVQ DX, ret+24(FP)
	RET
