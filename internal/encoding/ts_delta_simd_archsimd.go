//go:build goexperiment.simd && amd64

package encoding

import "simd/archsimd"

func archSIMDAVX2Enabled() bool {
	return archsimd.X86.AVX2()
}

func archSIMDAVX512Enabled() bool {
	return archsimd.X86.AVX512()
}

func deltaOfDeltaIntoArchSIMDAVX2(dst []int64, src []int64, prevTS int64, prevDelta int64) (lastTS int64, lastDelta int64) {
	if len(src) < 6 {
		return deltaOfDeltaIntoScalar(dst, src, prevTS, prevDelta)
	}

	prevTS, prevDelta = deltaOfDeltaIntoScalar(dst[:2], src[:2], prevTS, prevDelta)

	i := 2
	for ; i <= len(src)-4; i += 4 {
		vi := archsimd.LoadInt64x4Slice(src[i : i+4])
		vim1 := archsimd.LoadInt64x4Slice(src[i-1 : i+3])
		vim2 := archsimd.LoadInt64x4Slice(src[i-2 : i+2])

		vim1Twice := vim1.Add(vim1)
		dd := vi.Sub(vim1Twice).Add(vim2)
		dd.Store((*[4]int64)(dst[i : i+4]))
	}

	if i < len(src) {
		tailPrevTS := src[i-1]
		tailPrevDelta := src[i-1] - src[i-2]

		return deltaOfDeltaIntoScalar(dst[i:], src[i:], tailPrevTS, tailPrevDelta)
	}

	return src[len(src)-1], src[len(src)-1] - src[len(src)-2]
}

func deltaOfDeltaIntoArchSIMDAVX512(dst []int64, src []int64, prevTS int64, prevDelta int64) (lastTS int64, lastDelta int64) {
	if len(src) < 10 {
		return deltaOfDeltaIntoScalar(dst, src, prevTS, prevDelta)
	}

	prevTS, prevDelta = deltaOfDeltaIntoScalar(dst[:2], src[:2], prevTS, prevDelta)

	i := 2
	for ; i <= len(src)-8; i += 8 {
		vi := archsimd.LoadInt64x8Slice(src[i : i+8])
		vim1 := archsimd.LoadInt64x8Slice(src[i-1 : i+7])
		vim2 := archsimd.LoadInt64x8Slice(src[i-2 : i+6])

		vim1Twice := vim1.Add(vim1)
		dd := vi.Sub(vim1Twice).Add(vim2)
		dd.Store((*[8]int64)(dst[i : i+8]))
	}

	if i < len(src) {
		tailPrevTS := src[i-1]
		tailPrevDelta := src[i-1] - src[i-2]

		return deltaOfDeltaIntoScalar(dst[i:], src[i:], tailPrevTS, tailPrevDelta)
	}

	return src[len(src)-1], src[len(src)-1] - src[len(src)-2]
}
