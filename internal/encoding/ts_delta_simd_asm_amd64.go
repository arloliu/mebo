//go:build amd64

package encoding

import "golang.org/x/sys/cpu"

func asmAVX2Enabled() bool {
	return cpu.X86.HasAVX2
}

func asmAVX512Enabled() bool {
	return cpu.X86.HasAVX512
}

//go:noescape
func deltaOfDeltaIntoASMAVX2Bulk(dst []int64, src []int64, count int)

//go:noescape
func deltaOfDeltaIntoASMAVX512(dst []int64, src []int64, prevTS int64, prevDelta int64) (lastTS int64, lastDelta int64)

func deltaOfDeltaIntoASMAVX2(dst []int64, src []int64, prevTS int64, prevDelta int64) (lastTS int64, lastDelta int64) {
	if len(src) < 6 {
		return deltaOfDeltaIntoScalar(dst, src, prevTS, prevDelta)
	}

	deltaOfDeltaIntoScalar(dst[:2], src[:2], prevTS, prevDelta)

	bulkCount := (len(src) - 2) &^ 3
	if bulkCount > 0 {
		deltaOfDeltaIntoASMAVX2Bulk(dst[2:2+bulkCount], src[2:2+bulkCount], bulkCount)
	}

	tailStart := 2 + bulkCount
	if tailStart < len(src) {
		tailPrevTS := src[tailStart-1]
		tailPrevDelta := src[tailStart-1] - src[tailStart-2]

		return deltaOfDeltaIntoScalar(dst[tailStart:], src[tailStart:], tailPrevTS, tailPrevDelta)
	}

	return src[len(src)-1], src[len(src)-1] - src[len(src)-2]
}
