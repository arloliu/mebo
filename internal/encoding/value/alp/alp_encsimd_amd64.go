//go:build amd64

package alp

import (
	"math"
	"math/bits"

	"github.com/arloliu/mebo/internal/arch"
	"github.com/arloliu/mebo/internal/pool"
)

// alpMainStatsMinN is the shortest column the stats kernel handles:
// below it the kernel's fixed cost (about 15 ns for the pooled mask buffer) loses to the scalar loop
// (measured 2026-10-04: scalar 16.6 vs kernel 15.3 ns at n = 7, and 4.2 vs 16.1 ns at n = 1).
const alpMainStatsMinN = 8

// alpEFSearchMinSamples is the smallest strided sample the search kernel handles:
// a one-value sample is faster scalar (measured 2026-10-04: 335–384 ns scalar vs 451–473 ns kernel),
// while from two values on the kernel wins (456–460 vs 498–595 ns, and 980–1005 vs 3330–7340 ns at 150 points).
const alpEFSearchMinSamples = 2

// alpHasPOPCNT gates the search kernel, which counts good lanes with POPCNT.
var alpHasPOPCNT = arch.X86HasPOPCNT()

// The search kernel hardcodes the candidate grid: e runs from 0 to 18, there are 190 candidates,
// and a candidate's rank (index + 1) fits in 9 bits.
// These fail to compile if alpMaxExponent or alpEFCandidates change without the kernel.
const (
	_ = uint(alpMaxExponent - 18)
	_ = uint(18 - alpMaxExponent)
	_ = uint(alpEFCandidates - 190)
	_ = uint(190 - alpEFCandidates)
)

// alpEFSearchAVX512 evaluates every (e, f) candidate on sample[0:ns] and returns the index of the winner:
// the first strictly smaller estimate in candidate order, with candidate seed (or none, for -1) winning every tie.
// Lanes ns..ceil(ns/8)*8-1 of sample must be NaN.
// ns must be in 1..63: the kernel returns -1 for any other ns, before touching memory,
// so a caller that indexes alpEFPairs with the result panics instead of reading past the sample.
// See alp_encsimd_amd64.s for the per-lane rules that make it select exactly as alpBestEF and alpBestEFSeeded do.
//
//go:noescape
func alpEFSearchAVX512(sample *[64]float64, ns int, factors *[alpEFCandidates][4]float64, seed int) int

// alpMainStatsAVX512 is the AVX-512DQ kernel behind the ALP-main encode pass:
// alpEncodeDigit for all n values (n >= 1),
// including the lanes where |scaled| >= 2^51 and the n mod 8 tail, so no lane needs a scalar rescue.
// See alp_encsimd_amd64.s for the per-lane rules that keep it bit-identical to alpEncodeDigit.
//
// Good digits are stored to dst, and exception slots are left untouched.
// blockMask[g] receives block g's exception lanes in its low 8 bits;
// the result is the OR of all block masks, so zero means no exception.
// mnmx receives {min, max} over the good digits ({MaxInt64, MinInt64} if none).
//
//go:noescape
func alpMainStatsAVX512(values *float64, n int, factors *[4]float64,
	dst *uint64, blockMask *uint64, mnmx *[2]int64) uint64

// alpMainStatsSIMD runs the AVX-512 kernel when alpEncAVX512 is set and the column has at least alpMainStatsMinN values,
// and is alpMainStatsScalar otherwise.
// Both produce identical digits, exception positions (ascending), min, width and nExc for every input.
func alpMainStatsSIMD(values []float64, ee, ff int, dst []uint64, excPos []uint32) (alpMainCand, []uint32) {
	if len(values) < alpMainStatsMinN || !alpEncAVX512 {
		return alpMainStatsScalar(values, ee, ff, dst, excPos)
	}

	return alpMainStatsKernel(values, ee, ff, dst, excPos)
}

// alpMainStatsKernel is alpMainStatsSIMD's kernel path for len(values) >= 1 and len(dst) >= len(values),
// callable directly so tests reach it regardless of the dispatch rule.
func alpMainStatsKernel(values []float64, ee, ff int, dst []uint64, excPos []uint32) (alpMainCand, []uint32) {
	n := len(values)
	nBlock := (n + 7) >> 3
	// The kernel writes dst[0:n] through a raw pointer: check the bound here, as the scalar loop would.
	_ = dst[n-1]

	// {pe, iff, pf, ie}: the same table values alpEncodeDigit multiplies by, in the same order.
	// A stack array: the //go:noescape kernel does not retain the pointer.
	factors := [4]float64{alpPow10[ee], alpInvPow10[ff], alpPow10[ff], alpInvPow10[ee]}

	// One mask word per block, pooled so the call stays allocation-free;
	// defer, so a panic cannot leak the buffer.
	maskPtr := pool.GetUint64Slice(nBlock)
	defer pool.PutUint64Slice(maskPtr)
	blockMask := *maskPtr

	var mnmx [2]int64
	anyExc := alpMainStatsAVX512(&values[0], n, &factors, &dst[0], &blockMask[0], &mnmx)

	// Walking blocks, then lanes, in ascending order keeps excPos ascending,
	// the invariant the decoder's binary search relies on.
	if anyExc != 0 {
		for g, m := range blockMask {
			b := uint32(g << 3)
			if m == 0xFF {
				// All-exception block (the full-precision column shape): one bulk append.
				excPos = append(excPos, b, b+1, b+2, b+3, b+4, b+5, b+6, b+7)
				continue
			}
			for m != 0 {
				excPos = append(excPos, b+uint32(bits.TrailingZeros64(m))) //nolint:gosec // lane index < 8
				m &= m - 1
			}
		}
	}

	nExc := len(excPos)
	if nExc == n {
		return alpMainCand{nExc: nExc, ok: false}, excPos
	}
	mn, mx := mnmx[0], mnmx[1]
	width := 0
	if mx >= mn {
		width = bits.Len64(uint64(mx - mn)) //nolint:gosec // the range is taken modulo 2^64, as in alpMainStatsScalar
	}

	return alpMainCand{mn: mn, width: width, nExc: nExc, ok: true}, excPos
}

// alpBestEFSIMD runs the (e, f) search with the AVX-512 kernel.
// seed is the candidate index e(e+1)/2 + f that wins ties, or -1 for the plain search.
// ok is false when the kernel is switched off or unsupported, and the caller runs the scalar search instead.
func alpBestEFSIMD(values []float64, stride, seed int) (bestE, bestF int, ok bool) {
	if !alpEncAVX512 || !alpHasPOPCNT {
		return 0, 0, false
	}
	ns := (len(values) + stride - 1) / stride
	if ns < alpEFSearchMinSamples || ns > 63 {
		return 0, 0, false
	}
	// The strided sample, NaN-padded to whole vectors.
	// A stack array: the //go:noescape kernel does not retain it, and it is passed directly, never through a func value.
	var sbuf [64]float64
	for i, k := 0, 0; k < ns; i, k = i+stride, k+1 {
		sbuf[k] = values[i]
	}
	for k := ns; k < (ns+7)&^7; k++ {
		sbuf[k] = math.NaN()
	}
	c := alpEFSearchAVX512(&sbuf, ns, &alpEFFactors, seed)

	return int(alpEFPairs[c][0]), int(alpEFPairs[c][1]), true
}
