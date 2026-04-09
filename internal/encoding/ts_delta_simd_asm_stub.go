//go:build !amd64

package encoding

func asmAVX2Enabled() bool {
	return false
}

func asmAVX512Enabled() bool {
	return false
}

func deltaOfDeltaIntoASMAVX2(dst []int64, src []int64, prevTS int64, prevDelta int64) (lastTS int64, lastDelta int64) {
	return deltaOfDeltaIntoScalar(dst, src, prevTS, prevDelta)
}

func deltaOfDeltaIntoASMAVX512(dst []int64, src []int64, prevTS int64, prevDelta int64) (lastTS int64, lastDelta int64) {
	return deltaOfDeltaIntoScalar(dst, src, prevTS, prevDelta)
}
