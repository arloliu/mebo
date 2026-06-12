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

// decodeDeltaPackedASMAVX512BulkPairs is the inner AVX-512 decode loop.
// It decodes pairs of full Group Varint groups (8 values per iteration) from
// data into dst using zeroing-masked VPERMB, vectorized zigzag, and two
// 8-wide prefix sums. It exits early when fewer than 2 groups remain, when
// the 64-byte payload load window would cross the end of data, or when a
// pair's combined payload exceeds the window; callers handle the remainder.
//
// table must point to deltaPackedDecodeTotalBytes[0] and validMasks to
// deltaPackedDecodeValidMasks[0].
//
//nolint:revive // assembly ABI returns progress as a tuple for low overhead
//go:noescape
func decodeDeltaPackedASMAVX512BulkPairs(
	dst []int64,
	data []byte,
	nGroups int,
	totalBytesTable *[256]uint8,
	validMasks *[256]uint32,
	prevTS int64,
	prevDelta int64,
) (consumed int, produced int, lastTS int64, lastDelta int64)

func decodeDeltaPackedASMAVX512(
	dst []int64,
	data []byte,
	groupCount int,
	prevTS int64,
	prevDelta int64,
) (deltaPackedDecodeProgress, bool) {
	nGroups := min(groupCount/groupSize, len(dst)/groupSize)
	if nGroups < 2 || len(data) < deltaPackedDecodeSIMDSafeLoadWindowAVX512+2 {
		return decodeDeltaPackedASMAVX2(dst, data, groupCount, prevTS, prevDelta)
	}

	var result deltaPackedDecodeProgress

	result.consumed, result.produced, result.lastTS, result.lastDelta = decodeDeltaPackedASMAVX512BulkPairs(
		dst, data,
		nGroups,
		&deltaPackedDecodeTotalBytes,
		&deltaPackedDecodeValidMasks,
		prevTS, prevDelta,
	)

	// Remainder: an odd trailing group, a pair whose combined payload exceeded
	// the 64-byte window, or groups near the end of data without a safe load
	// window. The AVX2 path (with its internal per-group scalar fallback)
	// finishes them.
	if rest := nGroups - result.produced/groupSize; rest > 0 {
		tail, ok := decodeDeltaPackedASMAVX2(
			dst[result.produced:],
			data[result.consumed:],
			rest*groupSize,
			result.lastTS,
			result.lastDelta,
		)
		if !ok {
			return result, result.produced > 0
		}

		result.consumed += tail.consumed
		result.produced += tail.produced
		result.lastTS = tail.lastTS
		result.lastDelta = tail.lastDelta
	}

	return result, true
}
