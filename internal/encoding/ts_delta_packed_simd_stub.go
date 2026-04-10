//go:build !amd64

package encoding

func decodeDeltaPackedASMAVX2(
	dst []int64,
	data []byte,
	groupCount int,
	prevTS int64,
	prevDelta int64,
) (deltaPackedDecodeProgress, bool) {
	return decodeDeltaPackedScalarBulk(dst, data, groupCount, prevTS, prevDelta)
}
