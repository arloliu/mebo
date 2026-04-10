//go:build amd64

package encoding

// decodeDeltaPackedASMAVX2BulkGroups is the inner AVX2 decode loop.
// It decodes nGroups full Group Varint groups from data into dst.
// table must point to deltaPackedDecodeTable[0].
// totalBytesTable must point to deltaPackedDecodeTotalBytes[0].
// Returns (consumed bytes, produced values, lastTS, lastDelta).
//
//nolint:revive // assembly ABI returns progress as a tuple for low overhead
//go:noescape
func decodeDeltaPackedASMAVX2BulkGroups(
	dst []int64,
	data []byte,
	nGroups int,
	table *[256]deltaPackedDecodeMeta,
	totalBytesTable *[256]uint8,
	prevTS int64,
	prevDelta int64,
) (consumed int, produced int, lastTS int64, lastDelta int64)

func decodeDeltaPackedASMAVX2(
	dst []int64,
	data []byte,
	groupCount int,
	prevTS int64,
	prevDelta int64,
) (deltaPackedDecodeProgress, bool) {
	if len(data) < 5 || groupCount < groupSize {
		return decodeDeltaPackedScalarBulk(dst, data, groupCount, prevTS, prevDelta)
	}

	nGroups := min(groupCount/groupSize, len(dst)/groupSize)
	if nGroups <= 0 {
		return decodeDeltaPackedScalarBulk(dst, data, groupCount, prevTS, prevDelta)
	}

	var result deltaPackedDecodeProgress

	result.consumed, result.produced, result.lastTS, result.lastDelta = decodeDeltaPackedASMAVX2BulkGroups(
		dst, data,
		nGroups,
		&deltaPackedDecodeTable,
		&deltaPackedDecodeTotalBytes,
		prevTS, prevDelta,
	)

	return result, true
}
