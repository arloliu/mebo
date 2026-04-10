package encoding

import (
	"encoding/binary"

	"github.com/arloliu/mebo/internal/pool"
)

const (
	// batchEncodeMaxGroups is the maximum number of groups (4 values each) that can be
	// processed in a single batch call. Matches deltaOfDeltaSIMDChunkSize / groupSize.
	batchEncodeMaxGroups = deltaOfDeltaSIMDChunkSize / groupSize // 64

	// deltaPackedEncodeSIMDMinLen is the minimum number of delta-of-delta values
	// required before the SIMD encode path is used. Below this threshold, the scalar
	// per-group flushGroup path is faster due to SIMD setup overhead.
	//
	// Benchmarked crossover: SIMD-fused beats scalar at ~20 values in the full pipeline
	// (encoder allocation + header + DoD + serialize). 32 gives a safety margin.
	deltaPackedEncodeSIMDMinLen = 32
)

// shouldUseDeltaPackedEncodeSIMD reports whether the SIMD encode path should be used
// for the given number of remaining values.
func shouldUseDeltaPackedEncodeSIMD(count int) bool {
	return hasDeltaPackedEncodeSIMD() && count >= deltaPackedEncodeSIMDMinLen
}

// encodeDeltaPackedGroupsBatch encodes pre-computed delta-of-delta int64 values into
// Group Varint format in a single batch, eliminating per-group Grow() overhead.
//
// This is the fused pipeline: zigzag → tag-classify → control-byte → pack, batched
// across all groups with a single buffer pre-allocation.
//
// Parameters:
//   - buf: output byte buffer (data is appended)
//   - dods: pre-computed delta-of-delta values (len must be a multiple of groupSize)
//
// The caller must ensure len(dods) is a multiple of groupSize and <= deltaOfDeltaSIMDChunkSize.
func encodeDeltaPackedGroupsBatch(buf *pool.ByteBuffer, dods []int64) {
	nValues := len(dods)
	nGroups := nValues / groupSize
	if nGroups == 0 {
		return
	}

	nValues = nGroups * groupSize

	// --- Phase 1: Batch zigzag encode ---
	var zigzags [deltaOfDeltaSIMDChunkSize]uint64
	for i := range nValues {
		dod := dods[i]
		zigzags[i] = uint64((dod << 1) ^ (dod >> 63)) //nolint:gosec
	}

	// --- Phase 2: Batch tag classify + control bytes + total size ---
	var controlBytes [batchEncodeMaxGroups]byte
	totalPayload := 0

	for g := range nGroups {
		base := g * groupSize
		tag0 := encodeTag(zigzags[base])
		tag1 := encodeTag(zigzags[base+1])
		tag2 := encodeTag(zigzags[base+2])
		tag3 := encodeTag(zigzags[base+3])
		controlBytes[g] = tag0 | (tag1 << 2) | (tag2 << 4) | (tag3 << 6)
		totalPayload += groupVarintLengths[tag0] +
			groupVarintLengths[tag1] +
			groupVarintLengths[tag2] +
			groupVarintLengths[tag3]
	}

	// --- Phase 3: Single pre-allocation for entire batch ---
	// nGroups control bytes + totalPayload data bytes + 8 bytes slack for last PutUint64
	startLen := len(buf.B)
	needed := nGroups + totalPayload + 8
	buf.Grow(needed)
	buf.B = buf.B[:startLen+needed]

	// --- Phase 4: Pack all groups ---
	offset := startLen
	for g := range nGroups {
		base := g * groupSize
		cb := controlBytes[g]
		buf.B[offset] = cb
		offset++

		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], zigzags[base])
		offset += groupVarintLengths[(cb>>0)&0x03]

		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], zigzags[base+1])
		offset += groupVarintLengths[(cb>>2)&0x03]

		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], zigzags[base+2])
		offset += groupVarintLengths[(cb>>4)&0x03]

		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], zigzags[base+3])
		offset += groupVarintLengths[(cb>>6)&0x03]
	}

	// --- Phase 5: Trim to actual size (remove slack) ---
	buf.B = buf.B[:offset]
}

// encodeDeltaPackedGroupsScalar is the scalar fallback for encodeDeltaPackedGroupsSIMD.
// It encodes delta-of-delta values into Group Varint format using pure Go.
//
// Parameters:
//   - dst: output buffer, must have capacity >= nGroups * 33 bytes
//   - dods: delta-of-delta int64 values, len must be >= nGroups * 4
//   - nGroups: number of 4-value groups to encode
//
// Returns: bytes written to dst
func encodeDeltaPackedGroupsScalar(dst []byte, dods []int64, nGroups int) int {
	offset := 0

	for g := range nGroups {
		base := g * groupSize

		// Zigzag encode
		zz0 := uint64((dods[base] << 1) ^ (dods[base] >> 63))     //nolint:gosec
		zz1 := uint64((dods[base+1] << 1) ^ (dods[base+1] >> 63)) //nolint:gosec
		zz2 := uint64((dods[base+2] << 1) ^ (dods[base+2] >> 63)) //nolint:gosec
		zz3 := uint64((dods[base+3] << 1) ^ (dods[base+3] >> 63)) //nolint:gosec

		// Tag classify
		tag0 := encodeTag(zz0)
		tag1 := encodeTag(zz1)
		tag2 := encodeTag(zz2)
		tag3 := encodeTag(zz3)

		// Control byte
		cb := tag0 | (tag1 << 2) | (tag2 << 4) | (tag3 << 6)
		dst[offset] = cb
		offset++

		// Pack values (branchless 8-byte write, advance by actual width)
		binary.LittleEndian.PutUint64(dst[offset:offset+8], zz0)
		offset += groupVarintLengths[tag0]
		binary.LittleEndian.PutUint64(dst[offset:offset+8], zz1)
		offset += groupVarintLengths[tag1]
		binary.LittleEndian.PutUint64(dst[offset:offset+8], zz2)
		offset += groupVarintLengths[tag2]
		binary.LittleEndian.PutUint64(dst[offset:offset+8], zz3)
		offset += groupVarintLengths[tag3]
	}

	return offset
}
