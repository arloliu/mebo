//go:build !amd64

package encoding

func encodeDeltaPackedGroupsSIMD(dst []byte, dods []int64, nGroups int) int {
	return encodeDeltaPackedGroupsScalar(dst, dods, nGroups)
}

func hasDeltaPackedEncodeSIMD() bool {
	return false
}
