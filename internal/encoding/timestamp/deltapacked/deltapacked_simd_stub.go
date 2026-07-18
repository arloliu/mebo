//go:build !amd64

package deltapacked

func decodeDeltaPackedASMAVX2(
	dst []int64,
	data []byte,
	groupCount int,
	prevTS int64,
	prevDelta int64,
) (deltaPackedDecodeProgress, bool) {
	return decodeDeltaPackedScalarBulk(dst, data, groupCount, prevTS, prevDelta)
}

func decodeDeltaPackedASMAVX512(
	dst []int64,
	data []byte,
	groupCount int,
	prevTS int64,
	prevDelta int64,
) (deltaPackedDecodeProgress, bool) {
	return decodeDeltaPackedScalarBulk(dst, data, groupCount, prevTS, prevDelta)
}
