//go:build !goexperiment.simd || !amd64

package encoding

func deltaOfDeltaIntoArchSIMDAVX2(dst []int64, src []int64, prevTS int64, prevDelta int64) (lastTS int64, lastDelta int64) {
	return deltaOfDeltaIntoScalar(dst, src, prevTS, prevDelta)
}

func deltaOfDeltaIntoArchSIMDAVX512(dst []int64, src []int64, prevTS int64, prevDelta int64) (lastTS int64, lastDelta int64) {
	return deltaOfDeltaIntoScalar(dst, src, prevTS, prevDelta)
}
