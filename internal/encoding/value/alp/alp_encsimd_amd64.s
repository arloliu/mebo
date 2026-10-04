#include "textflag.h"

// EFEVAL folds one evaluated candidate into the running minimum key at ret.
// In: AX = min digit, R8 = max digit, good = its good-lane count, DI = candidate index c,
// R12 = ns. Clobbers AX, R8.
//   width = (good > 0 && max != min) ? BSR(max - min) + 1 : 0   (= bits.Len64(uint64(max-min)))
//   est   = ns*width + (ns - good)*96                            (= alpBestEF's estimate)
//   key   = est<<9 | rank, rank = 0 for the seed, else c+1
// BSR leaves its destination undefined for a zero source and sets ZF, so the
// zero range is handled with a CMOV; BSR is baseline x86-64, unlike LZCNT.
#define EFEVAL(good) \
	SUBQ    AX, R8 \
	MOVQ    $-1, AX \
	BSRQ    R8, R8 \
	CMOVQEQ AX, R8 \
	INCQ    R8 \
	XORQ    AX, AX \
	TESTQ   good, good \
	CMOVQEQ AX, R8 \
	MOVQ    R8, AX \
	IMULQ   R12, AX \
	MOVQ    R12, R8 \
	SUBQ    good, R8 \
	IMULQ   $96, R8 \
	ADDQ    R8, AX \
	SHLQ    $9, AX \
	LEAQ    1(DI), R8 \
	CMPQ    DI, seed+24(FP) \
	JNE     2(PC) \
	XORQ    R8, R8 \
	ORQ     R8, AX \
	CMPQ    AX, ret+32(FP) \
	JAE     2(PC) \
	MOVQ    AX, ret+32(FP)

// func alpMainStatsAVX512(values *float64, n int, factors *[4]float64,
//     dst *uint64, blockMask *uint64, mnmx *[2]int64) uint64
//
// alpEncodeDigit for every one of the n values, eight lanes per block, including
// the lanes the old kernel left to a scalar rescue (|scaled| >= 2^51) and the
// n mod 8 tail, which is loaded and stored under a lane mask (masked loads do not
// fault on masked-off lanes). factors holds {pe, iff, pf, ie}.
//
// Per lane, with x = (v*pe)*iff (two separate multiplies, never fused):
//   - r = RNE(x) (VRNDSCALEPD imm 0x08), which for |x| < 2^51 equals the
//     scalar magic-number round;
//   - for 2^51 <= |x| < 2^52, r = x + copysign(0.5, x) instead: doubles there are
//     multiples of 0.5, the add is exact, and the truncating convert below
//     finishes math.Round's half-away-from-zero;
//   - for |x| >= 2^52, x is integral (or infinite) and r = RNE(x) = x;
//   - d = VCVTTPD2QQ(r); inRange = |x| < 9.2e18 (LT_OQ, so false for NaN and
//     ±Inf), the scalar path's |round(x)| >= 9.2e18 rule;
//   - verify: (float64(d)*pf)*ie compared bitwise with v, as alpEncodeDigit
//     does, so -0.0 (which converts to digit 0 and back to +0.0) stays an
//     exception.
// good = valid & inRange & verified. Good digits are masked-stored to dst and
// folded into signed min/max; blockMask[g] gets block g's exception lanes
// (valid & ~good) in its low 8 bits. Returns the OR of all block masks.
// mnmx receives {min, max} over good digits, or {MaxInt64, MinInt64} when
// no lane is good.
//
// Register map:
//   SI = values ptr, DI = dst ptr, BX = blockMask ptr (advanced per block)
//   R8 = blocks left, R10 = tail lane mask, R12 = OR of block masks
//   Z16..Z19 = broadcast pe, iff, pf, ie
//   Z21 = 2^51, Z22 = abs mask, Z25 = 2^52, Z26 = 9.2e18, Z27 = sign mask, Z28 = 0.5
//   Z8 = min accumulator, Z9 = max accumulator
//   Z0..Z7 = per-block scratch; K7 = valid lanes, K1..K6 = per-block masks
TEXT ·alpMainStatsAVX512(SB), NOSPLIT, $0-56
	MOVQ values+0(FP), SI
	MOVQ n+8(FP), AX
	MOVQ dst+24(FP), DI
	MOVQ blockMask+32(FP), BX

	// R8 = ceil(n/8) blocks; R10 = valid-lane mask of the last block.
	MOVQ AX, R8
	ADDQ $7, R8
	SHRQ $3, R8
	MOVQ AX, CX
	ANDQ $7, CX
	MOVL $0xFF, R10
	JZ   masks
	MOVL $1, R10
	SHLL CX, R10
	DECL R10

masks:
	MOVQ         factors+16(FP), DX
	VBROADCASTSD 0(DX), Z16
	VBROADCASTSD 8(DX), Z17
	VBROADCASTSD 16(DX), Z18
	VBROADCASTSD 24(DX), Z19

	MOVQ         $0x4320000000000000, AX // 2^51
	VPBROADCASTQ AX, Z21
	MOVQ         $0x7FFFFFFFFFFFFFFF, AX // abs mask, and MaxInt64 for the min accumulator
	VPBROADCASTQ AX, Z22
	VPBROADCASTQ AX, Z8
	MOVQ         $0x4330000000000000, AX // 2^52
	VPBROADCASTQ AX, Z25
	MOVQ         $0x43dfeb3dd0676600, AX // 9.2e18
	VPBROADCASTQ AX, Z26
	MOVQ         $1, AX
	SHLQ         $63, AX                 // sign mask, and MinInt64 for the max accumulator
	VPBROADCASTQ AX, Z27
	VPBROADCASTQ AX, Z9
	MOVQ         $0x3FE0000000000000, AX // 0.5
	VPBROADCASTQ AX, Z28

	XORQ  R12, R12
	TESTQ R8, R8
	JEQ   done

loop:
	// K7 = valid lanes: all eight, or the tail mask on the last block.
	MOVL    $0xFF, R9
	CMPQ    R8, $1
	CMOVLEQ R10, R9
	KMOVB   R9, K7

	VMOVUPD.Z   (SI), K7, Z0       // Z0 = v (masked-off lanes zero, never loaded)
	VMULPD      Z16, Z0, Z1        // Z1 = v * pe
	VMULPD      Z17, Z1, Z1        // Z1 = x = (v*pe) * iff
	VANDPD      Z22, Z1, Z2        // Z2 = |x|
	VCMPPD      $0x11, Z21, Z2, K1 // K1 = |x| < 2^51
	VCMPPD      $0x11, Z25, Z2, K2 // K2 = |x| < 2^52
	VCMPPD      $0x11, Z26, Z2, K3 // K3 = inRange = |x| < 9.2e18
	VRNDSCALEPD $0x08, Z1, Z3      // Z3 = RNE(x)
	KANDNB      K2, K1, K5         // K5 = 2^51 <= |x| < 2^52
	VANDPD      Z27, Z1, Z4
	VORPD       Z28, Z4, Z4        // Z4 = copysign(0.5, x)
	VADDPD      Z4, Z1, K5, Z3     // Z3 = x ± 0.5 on K5 lanes
	VCVTTPD2QQ  Z3, Z6             // Z6 = d
	VCVTQQ2PD   Z6, Z7             // Z7 = float64(d)
	VMULPD      Z18, Z7, Z7
	VMULPD      Z19, Z7, Z7        // Z7 = (float64(d)*pf) * ie
	VPCMPEQQ    Z0, Z7, K4         // K4 = verified (bitwise)
	KANDB       K4, K3, K4
	KANDB       K4, K7, K4         // K4 = good
	VMOVDQU64   Z6, K4, (DI)       // store good digits only
	VPMINSQ     Z6, Z8, K4, Z8
	VPMAXSQ     Z6, Z9, K4, Z9
	KANDNB      K7, K4, K6         // K6 = valid & ~good
	KMOVB       K6, AX
	MOVQ        AX, (BX)
	ORQ         AX, R12

	ADDQ $64, SI
	ADDQ $64, DI
	ADDQ $8, BX
	DECQ R8
	JNZ  loop

done:
	// Horizontal min/max: 512 -> 256 -> 128 -> 64 bits.
	VSHUFI64X2 $0x4E, Z8, Z8, Z10
	VPMINSQ    Z10, Z8, Z8
	VSHUFI64X2 $0xB1, Z8, Z8, Z10
	VPMINSQ    Z10, Z8, Z8
	VPSHUFD    $0x4E, Z8, Z10
	VPMINSQ    Z10, Z8, Z8
	VSHUFI64X2 $0x4E, Z9, Z9, Z11
	VPMAXSQ    Z11, Z9, Z9
	VSHUFI64X2 $0xB1, Z9, Z9, Z11
	VPMAXSQ    Z11, Z9, Z9
	VPSHUFD    $0x4E, Z9, Z11
	VPMAXSQ    Z11, Z9, Z9
	MOVQ       mnmx+40(FP), DX
	VMOVQ      X8, 0(DX)
	VMOVQ      X9, 8(DX)
	MOVQ       R12, ret+48(FP)
	VZEROUPPER
	RET

// func alpEFSearchAVX512(sample *[64]float64, ns int, factors *[alpEFCandidates][4]float64, seed int) int
//
// Evaluates every (e, f) candidate on sample[0:ns] and returns the index of the
// candidate with the smallest key est<<9 | rank (see EFEVAL): the first
// strictly smaller estimate in candidate order, with the seed (a candidate
// index, or -1) winning every tie. That is alpBestEF's and alpBestEFSeeded's
// selection without their pruning, which only ever skips candidates that
// cannot win.
//
// The caller fills lanes ns..ceil(ns/8)*8-1 with NaN, which is never a good
// lane, so no valid-lane mask is needed. Candidates are ordered by e, then f;
// factors[c] = {10^e, 10^-f, 10^f, 10^-e}, and the kernel reads 10^e from the
// first candidate of each e group.
//
// Per e, ve = v*10^e is computed once into the 512-byte frame. Per candidate
// and vector, with x = ve*10^-f (together the scalar (v*pe)*iff):
//   - r = RNE(x) (VRNDSCALEPD imm 0x08), the magic-number round for |x| < 2^51;
//   - fast path, when no lane of the vector pair has |x| >= 2^51 (GE_OQ, false
//     for NaN): good = ((r*10^f)*10^-e == v) under EQ_OQ, the estimator's FP
//     compare (-0.0 verifies, NaN never does);
//   - slow path otherwise: big lanes take r = trunc(RZ(x + copysign(0.5, x))),
//     which is math.Round for 2^51 <= |x| < 2^52 (exact add, half away from
//     zero) and x itself above 2^52 (integral; the inexact sum rounds back
//     toward zero), and good also needs |x| < 9.2e18 (LT_OQ, false for NaN and
//     ±Inf), the scalar |round(x)| >= 9.2e18 rule.
// Good lanes accumulate r as doubles into min/max (from +Inf/-Inf) and are
// counted with KMOVB+POPCNT. After the last vector the extrema are reduced and
// converted to int64, exactly when good > 0; with no good lane the converted
// sentinels are discarded by EFEVAL.
//
// Candidates of one e run in pairs (f, f+1) sharing the ve and v loads; an odd
// one out runs alone.
//
// Register map:
//   R13 = sample, R12 = ns, R11 = vectors, DX = factors[c], DI = c, BX = e,
//   R14 = candidates left in this e group, R15 = frame (ve), SI/R8/CX = loop
//   Z21 = 2^51, Z22 = abs mask, Z24 = sign mask, Z26 = 9.2e18, Z28 = 0.5,
//   Z30 = +Inf, Z31 = -Inf, Z12 = 10^e
//   candidate A: factors Z17..Z19, min Z8, max Z9, good R9; scratch Z1..Z5 Z7; K1 K3 K4
//   candidate B: factors Z13..Z15, min Z10, max Z11, good R10; scratch Z16 Z20 Z23 Z25 Z27 Z29; K2 K5 K6
TEXT ·alpEFSearchAVX512(SB), NOSPLIT, $512-40
	MOVQ sample+0(FP), R13
	MOVQ ns+8(FP), R12
	MOVQ factors+16(FP), DX

	// An ns outside 1..63 would overrun the sample and the 512-byte frame: return -1 instead.
	CMPQ R12, $1
	JLT  efbadns
	CMPQ R12, $63
	JGT  efbadns

	XORQ DI, DI
	LEAQ 0(SP), R15
	MOVQ $0x7FFFFFFFFFFFFFFF, AX
	MOVQ AX, ret+32(FP)

	MOVQ R12, R11
	ADDQ $7, R11
	SHRQ $3, R11

	MOVQ         $0x4320000000000000, AX // 2^51
	VPBROADCASTQ AX, Z21
	MOVQ         $0x7FFFFFFFFFFFFFFF, AX // abs mask
	VPBROADCASTQ AX, Z22
	MOVQ         $0x43dfeb3dd0676600, AX // 9.2e18
	VPBROADCASTQ AX, Z26
	MOVQ         $0x8000000000000000, AX // sign mask
	VPBROADCASTQ AX, Z24
	MOVQ         $0x3FE0000000000000, AX // 0.5
	VPBROADCASTQ AX, Z28
	MOVQ         $0x7FF0000000000000, AX // +Inf
	VPBROADCASTQ AX, Z30
	MOVQ         $0xFFF0000000000000, AX // -Inf
	VPBROADCASTQ AX, Z31

	XORQ BX, BX

efloop:
	// ve = v * 10^e for every vector, shared by this e's candidates.
	VBROADCASTSD 0(DX), Z12
	MOVQ         R13, SI
	MOVQ         R15, R8
	MOVQ         R11, CX

efve:
	VMULPD  (SI), Z12, Z0
	VMOVUPD Z0, (R8)
	ADDQ    $64, SI
	ADDQ    $64, R8
	DECQ    CX
	JNZ     efve

	LEAQ 1(BX), R14 // e+1 candidates: f = 0..e

efpair:
	CMPQ R14, $2
	JLT  efsingle

	VBROADCASTSD 8(DX), Z17
	VBROADCASTSD 16(DX), Z18
	VBROADCASTSD 24(DX), Z19
	VBROADCASTSD 40(DX), Z13
	VBROADCASTSD 48(DX), Z14
	VBROADCASTSD 56(DX), Z15
	VMOVAPD      Z30, Z8
	VMOVAPD      Z31, Z9
	VMOVAPD      Z30, Z10
	VMOVAPD      Z31, Z11
	XORQ         R9, R9
	XORQ         R10, R10
	MOVQ         R13, SI
	MOVQ         R15, R8
	MOVQ         R11, CX

efpvec:
	VMOVUPD     (R8), Z0           // ve
	VMOVUPD     (SI), Z6           // v
	VMULPD      Z17, Z0, Z1        // xA
	VMULPD      Z13, Z0, Z16       // xB
	VANDPD      Z22, Z1, Z2
	VANDPD      Z22, Z16, Z20
	VCMPPD      $0x1D, Z21, Z2, K1 // K1 = |xA| >= 2^51
	VCMPPD      $0x1D, Z21, Z20, K2
	VRNDSCALEPD $0x08, Z1, Z3      // rA = RNE(xA)
	VRNDSCALEPD $0x08, Z16, Z23
	KORTESTB    K1, K2
	JNZ         efpbig
	VMULPD      Z18, Z3, Z7
	VMULPD      Z14, Z23, Z25
	VMULPD      Z19, Z7, Z7
	VMULPD      Z15, Z25, Z25
	VCMPPD      $0x00, Z6, Z7, K4  // goodA = back == v (EQ_OQ)
	VCMPPD      $0x00, Z6, Z25, K6
	JMP         efpacc

efpbig:
	VCMPPD        $0x11, Z26, Z2, K3 // inRange = |x| < 9.2e18
	VCMPPD        $0x11, Z26, Z20, K5
	VANDPD        Z24, Z1, Z4
	VANDPD        Z24, Z16, Z27
	VORPD         Z28, Z4, Z4        // copysign(0.5, x)
	VORPD         Z28, Z27, Z27
	VADDPD.RZ_SAE Z4, Z1, Z5
	VADDPD.RZ_SAE Z27, Z16, Z29
	VRNDSCALEPD   $0x0B, Z5, K1, Z3  // big lanes: trunc
	VRNDSCALEPD   $0x0B, Z29, K2, Z23
	VMULPD        Z18, Z3, Z7
	VMULPD        Z14, Z23, Z25
	VMULPD        Z19, Z7, Z7
	VMULPD        Z15, Z25, Z25
	VCMPPD        $0x00, Z6, Z7, K4
	VCMPPD        $0x00, Z6, Z25, K6
	KANDB         K4, K3, K4
	KANDB         K6, K5, K6

efpacc:
	VMINPD  Z3, Z8, K4, Z8
	VMAXPD  Z3, Z9, K4, Z9
	VMINPD  Z23, Z10, K6, Z10
	VMAXPD  Z23, Z11, K6, Z11
	KMOVB   K4, AX
	POPCNTL AX, AX
	ADDQ    AX, R9
	KMOVB   K6, AX
	POPCNTL AX, AX
	ADDQ    AX, R10
	ADDQ    $64, SI
	ADDQ    $64, R8
	DECQ    CX
	JNZ     efpvec

	// Paired reduction: element 0 ends up with A's extremum, element 4 with B's.
	VSHUFF64X2    $0x44, Z10, Z8, Z12
	VSHUFF64X2    $0xEE, Z10, Z8, Z8
	VMINPD        Z12, Z8, Z8
	VSHUFF64X2    $0xB1, Z8, Z8, Z12
	VMINPD        Z12, Z8, Z8
	VPERMILPD     $0x55, Z8, Z12
	VMINPD        Z12, Z8, Z8
	VSHUFF64X2    $0x44, Z11, Z9, Z12
	VSHUFF64X2    $0xEE, Z11, Z9, Z9
	VMAXPD        Z12, Z9, Z9
	VSHUFF64X2    $0xB1, Z9, Z9, Z12
	VMAXPD        Z12, Z9, Z9
	VPERMILPD     $0x55, Z9, Z12
	VMAXPD        Z12, Z9, Z9
	VCVTTSD2SIQ   X8, AX
	VCVTTSD2SIQ   X9, R8
	EFEVAL(R9)
	INCQ          DI
	VEXTRACTF64X2 $2, Z8, X12
	VCVTTSD2SIQ   X12, AX
	VEXTRACTF64X2 $2, Z9, X12
	VCVTTSD2SIQ   X12, R8
	EFEVAL(R10)
	INCQ          DI
	ADDQ          $64, DX
	SUBQ          $2, R14
	JMP           efpair

efsingle:
	TESTQ R14, R14
	JEQ   efnext

	VBROADCASTSD 8(DX), Z17
	VBROADCASTSD 16(DX), Z18
	VBROADCASTSD 24(DX), Z19
	VMOVAPD      Z30, Z8
	VMOVAPD      Z31, Z9
	XORQ         R9, R9
	MOVQ         R13, SI
	MOVQ         R15, R8
	MOVQ         R11, CX

efsvec:
	VMULPD      (R8), Z17, Z1
	VANDPD      Z22, Z1, Z2
	VCMPPD      $0x1D, Z21, Z2, K1
	VRNDSCALEPD $0x08, Z1, Z3
	KORTESTB    K1, K1
	JNZ         efsbig
	VMULPD      Z18, Z3, Z7
	VMULPD      Z19, Z7, Z7
	VCMPPD      $0x00, (SI), Z7, K4
	JMP         efsacc

efsbig:
	VCMPPD        $0x11, Z26, Z2, K3
	VANDPD        Z24, Z1, Z4
	VORPD         Z28, Z4, Z4
	VADDPD.RZ_SAE Z4, Z1, Z5
	VRNDSCALEPD   $0x0B, Z5, K1, Z3
	VMULPD        Z18, Z3, Z7
	VMULPD        Z19, Z7, Z7
	VCMPPD        $0x00, (SI), Z7, K4
	KANDB         K4, K3, K4

efsacc:
	VMINPD  Z3, Z8, K4, Z8
	VMAXPD  Z3, Z9, K4, Z9
	KMOVB   K4, AX
	POPCNTL AX, AX
	ADDQ    AX, R9
	ADDQ    $64, SI
	ADDQ    $64, R8
	DECQ    CX
	JNZ     efsvec

	VSHUFF64X2  $0x4E, Z8, Z8, Z12
	VMINPD      Z12, Z8, Z8
	VSHUFF64X2  $0xB1, Z8, Z8, Z12
	VMINPD      Z12, Z8, Z8
	VPERMILPD   $0x55, Z8, Z12
	VMINPD      Z12, Z8, Z8
	VSHUFF64X2  $0x4E, Z9, Z9, Z12
	VMAXPD      Z12, Z9, Z9
	VSHUFF64X2  $0xB1, Z9, Z9, Z12
	VMAXPD      Z12, Z9, Z9
	VPERMILPD   $0x55, Z9, Z12
	VMAXPD      Z12, Z9, Z9
	VCVTTSD2SIQ X8, AX
	VCVTTSD2SIQ X9, R8
	EFEVAL(R9)
	INCQ        DI
	ADDQ        $32, DX

efnext:
	INCQ BX
	CMPQ BX, $19
	JNE  efloop

	// Decode the winning key: rank 0 is the seed, rank r > 0 is candidate r-1.
	MOVQ    ret+32(FP), AX
	ANDQ    $511, AX
	MOVQ    seed+24(FP), CX
	DECQ    AX
	CMPQ    AX, $-1
	CMOVQEQ CX, AX
	MOVQ    AX, ret+32(FP)
	VZEROUPPER
	RET

efbadns:
	MOVQ $-1, ret+32(FP)
	RET
