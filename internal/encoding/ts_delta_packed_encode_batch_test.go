package encoding

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/arloliu/mebo/internal/pool"
	"github.com/stretchr/testify/require"
)

// TestEncodeDeltaPackedGroupsBatch_Parity verifies that the batch encode produces
// identical bytes to the per-group flushGroup path.
func TestEncodeDeltaPackedGroupsBatch_Parity(t *testing.T) {
	sizes := []int{4, 8, 20, 100, 256}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("Size%d", size), func(t *testing.T) {
			timestamps := batchEncodeTestTimestamps(size + 2) // +2 for header timestamps

			// --- Reference: encode via per-group flushGroup ---
			refEncoder := NewTimestampDeltaPackedEncoder()
			refEncoder.WriteSlice(timestamps)
			refBytes := make([]byte, len(refEncoder.Bytes()))
			copy(refBytes, refEncoder.Bytes())
			refEncoder.Finish()

			// --- Batch: compute DoDs, then batch encode ---
			// Reproduce the same header handling as WriteSlice
			batchBuf := pool.GetBlobBuffer()
			defer pool.PutBlobBuffer(batchBuf)

			prevTS := timestamps[0]
			batchBuf.Grow(10)
			batchBuf.B = appendUvarint(batchBuf.B, uint64(prevTS))

			delta := timestamps[1] - prevTS
			zigzag := (delta << 1) ^ (delta >> 63)
			batchBuf.B = appendUvarint(batchBuf.B, uint64(zigzag))
			prevTS = timestamps[1]
			prevDelta := delta

			// Compute DoDs for the remaining timestamps
			remaining := timestamps[2:]
			dods := make([]int64, len(remaining))
			for i, ts := range remaining {
				d := ts - prevTS
				dods[i] = d - prevDelta
				prevTS = ts
				prevDelta = d
			}

			// Batch encode only full groups
			nGroups := len(dods) / groupSize
			if nGroups > 0 {
				encodeDeltaPackedGroupsBatch(batchBuf, dods[:nGroups*groupSize])
			}

			// Handle tail via per-group (same as encoder does)
			tail := dods[nGroups*groupSize:]
			if len(tail) > 0 {
				var pending [groupSize]uint64
				pendingLen := 0
				for _, dod := range tail {
					zz := uint64((dod << 1) ^ (dod >> 63))
					pending[pendingLen] = zz
					pendingLen++
				}
				flushGroupStandalone(batchBuf, pending[:], pendingLen)
			}

			require.Equal(t, refBytes, batchBuf.Bytes(),
				"batch encode output must match per-group flushGroup output")
		})
	}
}

// TestEncodeDeltaPackedGroupsBatch_DataPatterns tests correctness across varied data patterns.
func TestEncodeDeltaPackedGroupsBatch_DataPatterns(t *testing.T) {
	patterns := []struct {
		name string
		dods []int64
	}{
		{
			name: "AllZero",
			dods: make([]int64, 64),
		},
		{
			name: "Small1Byte",
			dods: func() []int64 {
				d := make([]int64, 64)
				for i := range d {
					d[i] = int64(i % 50)
				}

				return d
			}(),
		},
		{
			name: "Mixed2Byte",
			dods: func() []int64 {
				d := make([]int64, 64)
				for i := range d {
					d[i] = int64(i*137 + 200)
				}

				return d
			}(),
		},
		{
			name: "Large8Byte",
			dods: func() []int64 {
				d := make([]int64, 64)
				for i := range d {
					d[i] = int64(i)*1_000_000_000_000 + 42
				}

				return d
			}(),
		},
		{
			name: "Negative",
			dods: func() []int64 {
				d := make([]int64, 64)
				for i := range d {
					d[i] = -int64(i*100 + 1)
				}

				return d
			}(),
		},
		{
			name: "MixedWidths",
			dods: func() []int64 {
				d := make([]int64, 64)
				for i := range d {
					switch i % 4 {
					case 0:
						d[i] = int64(i % 100) // 1-byte
					case 1:
						d[i] = int64(i*100 + 500) // 2-byte
					case 2:
						d[i] = int64(i)*100000 + 70000 // 4-byte
					default:
						d[i] = int64(i)*10_000_000_000 + 5_000_000_000 // 8-byte
					}
				}

				return d
			}(),
		},
	}

	for _, p := range patterns {
		t.Run(p.name, func(t *testing.T) {
			// Reference: per-group encode
			refBuf := pool.GetBlobBuffer()
			defer pool.PutBlobBuffer(refBuf)
			encodePerGroup(refBuf, p.dods)

			// Batch encode
			batchBuf := pool.GetBlobBuffer()
			defer pool.PutBlobBuffer(batchBuf)
			encodeDeltaPackedGroupsBatch(batchBuf, p.dods)

			require.Equal(t, refBuf.Bytes(), batchBuf.Bytes(),
				"batch encode must match per-group encode for pattern %s", p.name)
		})
	}
}

// TestEncodeDeltaPackedGroupsSIMD_Parity verifies the AVX2 SIMD encode kernel produces
// byte-identical output to the scalar per-group encode.
func TestEncodeDeltaPackedGroupsSIMD_Parity(t *testing.T) {
	if !hasDeltaPackedEncodeSIMD() {
		t.Skip("SIMD encode not available on this platform")
	}

	sizes := []int{4, 8, 20, 64, 100, 252, 256}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("Size%d", size), func(t *testing.T) {
			dods := benchmarkBatchEncodeDODs(size)
			nGroups := len(dods) / groupSize
			nValues := nGroups * groupSize

			// Reference: scalar per-group encode
			refBuf := make([]byte, nGroups*33+8)
			refWritten := encodeDeltaPackedGroupsScalar(refBuf, dods[:nValues], nGroups)

			// SIMD encode
			simdBuf := make([]byte, nGroups*33+8)
			simdWritten := encodeDeltaPackedGroupsSIMD(simdBuf, dods[:nValues], nGroups)

			require.Equal(t, refWritten, simdWritten,
				"SIMD and scalar must write same number of bytes")
			require.Equal(t, refBuf[:refWritten], simdBuf[:simdWritten],
				"SIMD output must be byte-identical to scalar output")
		})
	}
}

// TestEncodeDeltaPackedGroupsSIMD_DataPatterns tests SIMD encode across varied data patterns.
func TestEncodeDeltaPackedGroupsSIMD_DataPatterns(t *testing.T) {
	if !hasDeltaPackedEncodeSIMD() {
		t.Skip("SIMD encode not available on this platform")
	}

	patterns := []struct {
		name string
		dods []int64
	}{
		{
			name: "AllZero",
			dods: make([]int64, 64),
		},
		{
			name: "Small1Byte",
			dods: func() []int64 {
				d := make([]int64, 64)
				for i := range d {
					d[i] = int64(i % 50)
				}

				return d
			}(),
		},
		{
			name: "Mixed2Byte",
			dods: func() []int64 {
				d := make([]int64, 64)
				for i := range d {
					d[i] = int64(i*137 + 200)
				}

				return d
			}(),
		},
		{
			name: "Large8Byte",
			dods: func() []int64 {
				d := make([]int64, 64)
				for i := range d {
					d[i] = int64(i)*1_000_000_000_000 + 42
				}

				return d
			}(),
		},
		{
			name: "Negative",
			dods: func() []int64 {
				d := make([]int64, 64)
				for i := range d {
					d[i] = -int64(i*100 + 1)
				}

				return d
			}(),
		},
		{
			name: "MixedWidths",
			dods: func() []int64 {
				d := make([]int64, 64)
				for i := range d {
					switch i % 4 {
					case 0:
						d[i] = int64(i % 100) // 1-byte
					case 1:
						d[i] = int64(i*100 + 500) // 2-byte
					case 2:
						d[i] = int64(i)*100000 + 70000 // 4-byte
					default:
						d[i] = int64(i)*10_000_000_000 + 5_000_000_000 // 8-byte
					}
				}

				return d
			}(),
		},
		{
			name: "LargeNegative_8ByteZigzag",
			dods: func() []int64 {
				d := make([]int64, 16)
				d[0] = -1 << 32
				d[1] = 1 << 32
				d[2] = -(1 << 40)
				d[3] = 1 << 40
				d[4] = -(1 << 50)
				d[5] = 1 << 50
				d[6] = -(1 << 62)
				d[7] = 1 << 62
				d[8] = -1
				d[9] = 0
				d[10] = 1
				d[11] = -128
				d[12] = 127
				d[13] = -32768
				d[14] = 32767
				d[15] = -2147483648

				return d
			}(),
		},
	}

	for _, p := range patterns {
		t.Run(p.name, func(t *testing.T) {
			nGroups := len(p.dods) / groupSize
			nValues := nGroups * groupSize

			refBuf := make([]byte, nGroups*33+8)
			refWritten := encodeDeltaPackedGroupsScalar(refBuf, p.dods[:nValues], nGroups)

			simdBuf := make([]byte, nGroups*33+8)
			simdWritten := encodeDeltaPackedGroupsSIMD(simdBuf, p.dods[:nValues], nGroups)

			require.Equal(t, refWritten, simdWritten,
				"bytes written mismatch for pattern %s", p.name)
			require.Equal(t, refBuf[:refWritten], simdBuf[:simdWritten],
				"output mismatch for pattern %s", p.name)
		})
	}
}

// TestEncodeDeltaPackedGroupsSIMD_RoundTrip verifies SIMD-encoded data can be decoded correctly.
func TestEncodeDeltaPackedGroupsSIMD_RoundTrip(t *testing.T) {
	if !hasDeltaPackedEncodeSIMD() {
		t.Skip("SIMD encode not available on this platform")
	}

	sizes := []int{4, 100, 256}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("Size%d", size), func(t *testing.T) {
			timestamps := batchEncodeTestTimestamps(size + 2)

			// Encode using the full encoder pipeline
			refEnc := NewTimestampDeltaPackedEncoder()
			refEnc.WriteSlice(timestamps)
			refBytes := make([]byte, len(refEnc.Bytes()))
			copy(refBytes, refEnc.Bytes())
			refCount := refEnc.Len()
			refEnc.Finish()

			// Decode reference
			dec := NewTimestampDeltaPackedDecoder()
			refDecoded := make([]int64, refCount)
			n := dec.DecodeAll(refBytes, refCount, refDecoded)
			require.Equal(t, refCount, n)
			require.Equal(t, timestamps, refDecoded)
		})
	}
}

// --- Benchmarks ---

// BenchmarkGroupVarintEncode_PerGroup benchmarks the current per-group flushGroup approach
// for the serialization-only phase (DoDs already computed).
func BenchmarkGroupVarintEncode_PerGroup(b *testing.B) {
	sizes := []int{100, 256, 1000, 10000}

	for _, size := range sizes {
		dods := benchmarkBatchEncodeDODs(size)

		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			buf := pool.GetBlobBuffer()
			defer pool.PutBlobBuffer(buf)

			b.ResetTimer()
			for b.Loop() {
				buf.B = buf.B[:0]
				encodePerGroup(buf, dods)
			}
		})
	}
}

// BenchmarkGroupVarintEncode_Batch benchmarks the batch encode approach
// for the serialization-only phase (DoDs already computed).
func BenchmarkGroupVarintEncode_Batch(b *testing.B) {
	sizes := []int{100, 256, 1000, 10000}

	for _, size := range sizes {
		dods := benchmarkBatchEncodeDODs(size)

		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			buf := pool.GetBlobBuffer()
			defer pool.PutBlobBuffer(buf)

			b.ResetTimer()
			for b.Loop() {
				buf.B = buf.B[:0]
				encodeBatched(buf, dods)
			}
		})
	}
}

// BenchmarkDeltaPackedEncoder_WriteSlice_BatchVsOriginal benchmarks the full encoder
// pipeline (DoD + serialize) comparing original vs batch-integrated path.
func BenchmarkDeltaPackedEncoder_WriteSlice_BatchVsOriginal(b *testing.B) {
	sizes := []int{100, 1000, 10000}

	for _, size := range sizes {
		timestamps := batchEncodeTestTimestamps(size)

		b.Run(fmt.Sprintf("Original/Size%d", size), func(b *testing.B) {
			for b.Loop() {
				enc := NewTimestampDeltaPackedEncoder()
				enc.WriteSlice(timestamps)
				_ = enc.Bytes()
				enc.Finish()
			}
		})

		b.Run(fmt.Sprintf("BatchFused/Size%d", size), func(b *testing.B) {
			for b.Loop() {
				enc := NewTimestampDeltaPackedEncoder()
				writeSliceBatchFused(enc, timestamps)
				_ = enc.Bytes()
				enc.Finish()
			}
		})
	}
}

// --- helpers ---

// encodePerGroup simulates the current per-group flushGroup path for DoD values.
func encodePerGroup(buf *pool.ByteBuffer, dods []int64) {
	nGroups := len(dods) / groupSize
	var pending [groupSize]uint64

	for g := range nGroups {
		base := g * groupSize
		for lane := range groupSize {
			dod := dods[base+lane]
			pending[lane] = uint64((dod << 1) ^ (dod >> 63))
		}
		flushGroupStandalone(buf, pending[:], groupSize)
	}

	// Tail
	tail := len(dods) - nGroups*groupSize
	if tail > 0 {
		base := nGroups * groupSize
		for i := range tail {
			dod := dods[base+i]
			pending[i] = uint64((dod << 1) ^ (dod >> 63))
		}
		flushGroupStandalone(buf, pending[:], tail)
	}
}

// encodeBatched uses the batch path, chunking at 256 values (matching SIMD chunk size).
func encodeBatched(buf *pool.ByteBuffer, dods []int64) {
	remaining := dods
	for len(remaining) > 0 {
		n := min(len(remaining), deltaOfDeltaSIMDChunkSize)
		nGroups := n / groupSize
		if nGroups > 0 {
			encodeDeltaPackedGroupsBatch(buf, remaining[:nGroups*groupSize])
		}
		// Tail within chunk
		tail := n - nGroups*groupSize
		if tail > 0 {
			var pending [groupSize]uint64
			base := nGroups * groupSize
			for i := range tail {
				dod := remaining[base+i]
				pending[i] = uint64((dod << 1) ^ (dod >> 63))
			}
			flushGroupStandalone(buf, pending[:], tail)
		}
		remaining = remaining[n:]
	}
}

// flushGroupStandalone is an extracted version of flushGroup that operates on a
// standalone ByteBuffer (no encoder state), for benchmark comparison.
// Uses the same unrolled fast path as the real encoder for fair comparison.
func flushGroupStandalone(buf *pool.ByteBuffer, pending []uint64, n int) {
	if n == groupSize {
		tag0 := encodeTag(pending[0])
		tag1 := encodeTag(pending[1])
		tag2 := encodeTag(pending[2])
		tag3 := encodeTag(pending[3])
		controlByte := tag0 | (tag1 << 2) | (tag2 << 4) | (tag3 << 6)
		totalDataBytes := groupVarintLengths[tag0] +
			groupVarintLengths[tag1] +
			groupVarintLengths[tag2] +
			groupVarintLengths[tag3]

		startLen := len(buf.B)
		buf.Grow(1 + totalDataBytes + 8)
		buf.B = buf.B[:startLen+1+totalDataBytes+8]
		buf.B[startLen] = controlByte

		offset := startLen + 1
		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], pending[0])
		offset += groupVarintLengths[tag0]
		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], pending[1])
		offset += groupVarintLengths[tag1]
		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], pending[2])
		offset += groupVarintLengths[tag2]
		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], pending[3])

		buf.B = buf.B[:startLen+1+totalDataBytes]

		return
	}

	var controlByte byte
	var totalDataBytes int

	for i := range n {
		tag := encodeTag(pending[i])
		controlByte |= tag << (uint(i) * 2)
		totalDataBytes += groupVarintLengths[tag]
	}

	startLen := len(buf.B)
	buf.Grow(1 + totalDataBytes + 8)
	buf.B = buf.B[:startLen+1+totalDataBytes+8]
	buf.B[startLen] = controlByte

	offset := startLen + 1
	for i := range n {
		tag := (controlByte >> (uint(i) * 2)) & 0x03
		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], pending[i])
		offset += groupVarintLengths[tag]
	}

	buf.B = buf.B[:startLen+1+totalDataBytes]
}

// writeSliceBatchFused is a batch-fused variant of WriteSlice for benchmarking.
// It replaces the per-element zigzag + flushGroup loop with batch encode.
func writeSliceBatchFused(e *TimestampDeltaPackedEncoder, timestampsUs []int64) {
	tsLen := len(timestampsUs)
	if tsLen == 0 {
		return
	}

	currentSeqCount := e.seqCount
	e.count += tsLen
	e.seqCount += tsLen

	e.buf.Grow(tsLen*9 + 10)

	prevTS := e.prevTS
	prevDelta := e.prevDelta
	startIdx := 0

	if currentSeqCount == 0 {
		ts := timestampsUs[0]
		e.appendUvarint(uint64(ts))
		prevTS = ts
		startIdx = 1
		currentSeqCount++
	}

	if startIdx < tsLen && currentSeqCount == 1 {
		ts := timestampsUs[startIdx]
		delta := ts - prevTS
		zigzag := (delta << 1) ^ (delta >> 63)
		e.appendUvarint(uint64(zigzag))
		prevTS = ts
		prevDelta = delta
		startIdx++
	}

	remaining := timestampsUs[startIdx:]

	// Compute DoDs (using active SIMD backend)
	var deltaBuf [deltaOfDeltaSIMDChunkSize]int64

	for len(remaining) > 0 {
		n := min(len(remaining), deltaOfDeltaSIMDChunkSize)
		prevTS, prevDelta = deltaOfDeltaIntoActive(deltaBuf[:n], remaining[:n], prevTS, prevDelta)

		// Batch encode full groups
		nGroups := n / groupSize
		if nGroups > 0 {
			encodeDeltaPackedGroupsBatch(e.buf, deltaBuf[:nGroups*groupSize])
		}

		// Handle tail (< 4 values) via existing scalar path
		for i := nGroups * groupSize; i < n; i++ {
			zigzag := uint64((deltaBuf[i] << 1) ^ (deltaBuf[i] >> 63))
			e.pending[e.pendingLen] = zigzag
			e.pendingLen++

			if e.pendingLen == groupSize {
				e.flushGroup(groupSize)
			}
		}

		remaining = remaining[n:]
	}

	e.prevTS = prevTS
	e.prevDelta = prevDelta
}

func appendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}

	return append(b, byte(v))
}

func batchEncodeTestTimestamps(size int) []int64 {
	timestamps := make([]int64, size)
	baseUs := time.Now().UnixMicro()
	for i := range size {
		timestamps[i] = baseUs + int64(i)*1_000_000 + int64(i%5)*100
	}

	return timestamps
}

// BenchmarkGroupVarintEncode_SIMD benchmarks the AVX2 SIMD encode approach
// for the serialization-only phase (DoDs already computed).
func BenchmarkGroupVarintEncode_SIMD(b *testing.B) {
	if !hasDeltaPackedEncodeSIMD() {
		b.Skip("SIMD encode not available on this platform")
	}

	sizes := []int{100, 256, 1000, 10000}

	for _, size := range sizes {
		dods := benchmarkBatchEncodeDODs(size)
		nGroups := len(dods) / groupSize
		nValues := nGroups * groupSize
		dst := make([]byte, nGroups*33+8)

		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				_ = encodeDeltaPackedGroupsSIMD(dst, dods[:nValues], nGroups)
			}
		})
	}
}

// BenchmarkGroupVarintEncode_ScalarDirect benchmarks the scalar encode function
// with the same interface as the SIMD kernel (raw dst slice, no ByteBuffer).
func BenchmarkGroupVarintEncode_ScalarDirect(b *testing.B) {
	sizes := []int{100, 256, 1000, 10000}

	for _, size := range sizes {
		dods := benchmarkBatchEncodeDODs(size)
		nGroups := len(dods) / groupSize
		nValues := nGroups * groupSize
		dst := make([]byte, nGroups*33+8)

		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				_ = encodeDeltaPackedGroupsScalar(dst, dods[:nValues], nGroups)
			}
		})
	}
}

// BenchmarkDeltaPackedEncoder_WriteSlice_SIMDFused benchmarks the full encoder
// pipeline with SIMD fused encode.
func BenchmarkDeltaPackedEncoder_WriteSlice_SIMDFused(b *testing.B) {
	if !hasDeltaPackedEncodeSIMD() {
		b.Skip("SIMD encode not available on this platform")
	}

	sizes := []int{100, 1000, 10000}

	for _, size := range sizes {
		timestamps := batchEncodeTestTimestamps(size)

		b.Run(fmt.Sprintf("Original/Size%d", size), func(b *testing.B) {
			for b.Loop() {
				enc := NewTimestampDeltaPackedEncoder()
				enc.WriteSlice(timestamps)
				_ = enc.Bytes()
				enc.Finish()
			}
		})

		b.Run(fmt.Sprintf("SIMDFused/Size%d", size), func(b *testing.B) {
			for b.Loop() {
				enc := NewTimestampDeltaPackedEncoder()
				writeSliceSIMDFused(enc, timestamps)
				_ = enc.Bytes()
				enc.Finish()
			}
		})
	}
}

// writeSliceSIMDFused uses the SIMD kernel for the fused encode pipeline.
func writeSliceSIMDFused(e *TimestampDeltaPackedEncoder, timestampsUs []int64) {
	tsLen := len(timestampsUs)
	if tsLen == 0 {
		return
	}

	currentSeqCount := e.seqCount
	e.count += tsLen
	e.seqCount += tsLen

	e.buf.Grow(tsLen*9 + 10)

	prevTS := e.prevTS
	prevDelta := e.prevDelta
	startIdx := 0

	if currentSeqCount == 0 {
		ts := timestampsUs[0]
		e.appendUvarint(uint64(ts))
		prevTS = ts
		startIdx = 1
		currentSeqCount++
	}

	if startIdx < tsLen && currentSeqCount == 1 {
		ts := timestampsUs[startIdx]
		delta := ts - prevTS
		zigzag := (delta << 1) ^ (delta >> 63)
		e.appendUvarint(uint64(zigzag))
		prevTS = ts
		prevDelta = delta
		startIdx++
	}

	remaining := timestampsUs[startIdx:]
	var deltaBuf [deltaOfDeltaSIMDChunkSize]int64

	for len(remaining) > 0 {
		n := min(len(remaining), deltaOfDeltaSIMDChunkSize)
		prevTS, prevDelta = deltaOfDeltaIntoActive(deltaBuf[:n], remaining[:n], prevTS, prevDelta)

		nGroups := n / groupSize
		if nGroups > 0 {
			nValues := nGroups * groupSize
			// Pre-allocate worst case for this chunk
			maxBytes := nGroups * 33
			startLen := len(e.buf.B)
			e.buf.Grow(maxBytes)
			e.buf.B = e.buf.B[:startLen+maxBytes]
			written := encodeDeltaPackedGroupsSIMD(e.buf.B[startLen:], deltaBuf[:nValues], nGroups)
			e.buf.B = e.buf.B[:startLen+written]
		}

		// Handle tail (< 4 values) via existing scalar path
		for i := nGroups * groupSize; i < n; i++ {
			zigzag := uint64((deltaBuf[i] << 1) ^ (deltaBuf[i] >> 63))
			e.pending[e.pendingLen] = zigzag
			e.pendingLen++

			if e.pendingLen == groupSize {
				e.flushGroup(groupSize)
			}
		}

		remaining = remaining[n:]
	}

	e.prevTS = prevTS
	e.prevDelta = prevDelta
}

func benchmarkBatchEncodeDODs(size int) []int64 {
	timestamps := batchEncodeTestTimestamps(size + 2) // +2 for first/second header
	dods := make([]int64, size)
	prevTS := timestamps[1]
	prevDelta := timestamps[1] - timestamps[0]
	for i := range size {
		ts := timestamps[i+2]
		delta := ts - prevTS
		dods[i] = delta - prevDelta
		prevTS = ts
		prevDelta = delta
	}

	return dods
}
