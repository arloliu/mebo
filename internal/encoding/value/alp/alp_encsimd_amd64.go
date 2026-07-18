//go:build amd64

package alp

import (
	"math"
	"math/bits"

	"github.com/arloliu/mebo/internal/arch"
	"github.com/arloliu/mebo/internal/pool"
)

// alpMainStatsAVX512 is the hand-written AVX-512DQ verify kernel backing the
// ALP-main encode pass. It processes exactly nBlock 8-lane blocks (the first
// nBlock*8 values); the Go caller finishes the <8-value tail scalar.
//
// Per lane it computes scaled = v*pe*iff (two separate multiplies, never
// fused), a domain guard |scaled| < 2^51, the magic-number fast round, the
// int64 digit, and the bit-exact verify-back digit*pf*ie == v. Multiply order
// and the round magic match alpEncodeDigit's fast path exactly, so every
// in-domain lane's result is bit-identical to the scalar path.
//
// Outputs, per block g, into blockMask[g]: bits 0..7 = the exception mask
// (in-domain lanes whose verify-back failed) and bits 8..15 = the guard-fail
// mask (lanes with |scaled| >= 2^51, or NaN/Inf, where the vectorized fast
// round is invalid and the scalar legacy fallback must decide). Good lanes
// (in-domain AND verify-back matched) have their int64 digit masked-stored into
// dst and folded into the per-lane min/max accumulators returned via minOut and
// maxOut (each an 8-int64 vector the caller reduces). The return value is the
// OR of every block's (exception|guard) byte: zero means every processed lane
// was good, so the caller can skip the mask scan entirely.
//
//go:noescape
func alpMainStatsAVX512(values *float64, nBlock int, factors *[4]float64,
	dst *uint64, blockMask *uint64, minOut *int64, maxOut *int64) uint64

// alpMainStatsSIMD dispatches to the AVX-512 verify kernel when the CPU
// supports AVX-512DQ and there is at least one full 8-lane block, then rescues
// the rare guard-fail lanes and finishes the tail in Go so its output is
// byte-identical to alpMainStatsScalar. Off the fast path (no AVX-512DQ, or
// n < 8) it is exactly the scalar loop.
//
// Rescue/tail scalar work reuses alpEncodeDigit, the same primitive
// alpMainStatsScalar uses, so digits, exception positions (ascending), min/max,
// and the derived width are identical to the scalar reference for every input.
func alpMainStatsSIMD(values []float64, ee, ff int, dst []uint64, excPos []uint32) (alpMainCand, []uint32) {
	n := len(values)
	if n < 8 || !arch.X86HasAVX512DQ() {
		return alpMainStatsScalar(values, ee, ff, dst, excPos)
	}

	nBlock := n >> 3
	blkVals := nBlock << 3

	// Multiply factors {pe, iff, pf, ie}, precomputed once (bit-identical to
	// alpEncodeDigit's alpPow10[ee]*alpInvPow10[ff] scaling and
	// alpPow10[ff]*alpInvPow10[ee] verify-back): the kernel broadcasts each into
	// a ZMM. Binding to locals does not reorder anything — FP is not
	// associative, and the two-multiply order is preserved in the asm. Passed as
	// a stack array (the //go:noescape kernel does not retain the pointer).
	factors := [4]float64{alpPow10[ee], alpInvPow10[ff], alpPow10[ff], alpInvPow10[ee]}

	// Pooled per-block mask scratch (one uint64 per block). Pooled — not a
	// per-call make — so the zero-exception fast path stays allocation-free
	// even though the kernel writes every block's mask word unconditionally;
	// the sync.Pool is reused across short-lived encoders (see
	// internal/pool/uint64_slice_pool.go). defer Put so a panic mid-pass cannot
	// leak the buffer.
	maskPtr := pool.GetUint64Slice(nBlock)
	defer pool.PutUint64Slice(maskPtr)
	blockMask := *maskPtr

	// Per-lane min/max accumulators; the kernel writes the raw 8-lane vectors
	// here and Go reduces them. Stack-resident (the //go:noescape kernel does
	// not retain the pointers), so no heap allocation.
	var minOut, maxOut [8]int64

	anyBad := alpMainStatsAVX512(&values[0], nBlock, &factors,
		&dst[0], &blockMask[0], &minOut[0], &maxOut[0])

	// Reduce the kernel's good-lane min/max. Lanes that never saw a good value
	// hold the sentinels (MaxInt64 / MinInt64) the kernel initialized them to,
	// so they never corrupt the reduction.
	mn := int64(math.MaxInt64)
	mx := int64(math.MinInt64)
	for i := 0; i < 8; i++ {
		if minOut[i] < mn {
			mn = minOut[i]
		}
		if maxOut[i] > mx {
			mx = maxOut[i]
		}
	}

	// Rescue bad lanes (rare on real data). Walking blocks then lanes in
	// ascending order keeps excPos ascending — the invariant the decoder's
	// binary search relies on. Every exception in the column comes from a bad
	// lane (good lanes verify by construction), so this single ascending walk
	// appends them in the same order the scalar loop would.
	if anyBad != 0 {
		for g := 0; g < nBlock; g++ {
			m := blockMask[g]
			if m == 0 {
				continue
			}
			base := g << 3
			if (m>>8)&0xFF != 0 {
				// Dirty block (contains a guard-fail lane): the vectorized fast
				// round was invalid for at least one lane, so re-derive all 8
				// lanes scalar. The kernel's good-lane stores/min-max for this
				// block are idempotently reproduced (same digits, same min/max),
				// and its guard-fail lanes — which it left untouched — are
				// resolved here to either a digit or an exception.
				for j := 0; j < 8; j++ {
					i := base + j
					d, good := alpEncodeDigit(values[i], ee, ff)
					if !good {
						excPos = append(excPos, uint32(i)) //nolint:gosec
						continue
					}
					dst[i] = uint64(d) //nolint:gosec
					if d < mn {
						mn = d
					}
					if d > mx {
						mx = d
					}
				}

				continue
			}
			// Clean block with in-domain verify-fail exceptions: the kernel
			// already stored/min-maxed the good lanes, so only the exception
			// positions remain to append (ascending lane order).
			exc := byte(m & 0xFF)
			b := uint32(base) //nolint:gosec
			if exc == 0xFF {
				// All-exception block (the full-precision column shape): one
				// bulk append instead of eight, avoiding per-element grow checks.
				excPos = append(excPos, b, b+1, b+2, b+3, b+4, b+5, b+6, b+7)
			} else {
				for exc != 0 {
					j := bits.TrailingZeros8(exc)
					excPos = append(excPos, b+uint32(j)) //nolint:gosec
					exc &= exc - 1
				}
			}
		}
	}

	// Tail: the <8-value remainder the kernel did not process, scalar.
	for i := blkVals; i < n; i++ {
		d, good := alpEncodeDigit(values[i], ee, ff)
		if !good {
			excPos = append(excPos, uint32(i)) //nolint:gosec
			continue
		}
		dst[i] = uint64(d) //nolint:gosec
		if d < mn {
			mn = d
		}
		if d > mx {
			mx = d
		}
	}

	nExc := len(excPos)
	if nExc == n {
		return alpMainCand{nExc: nExc, ok: false}, excPos
	}
	width := 0
	if mx >= mn {
		width = bits.Len64(uint64(mx - mn)) //nolint:gosec
	}

	return alpMainCand{mn: mn, width: width, nExc: nExc, ok: true}, excPos
}
