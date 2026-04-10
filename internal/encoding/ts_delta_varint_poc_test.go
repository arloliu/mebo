package encoding

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/arloliu/mebo/internal/pool"
	"github.com/stretchr/testify/require"
)

// TestVarintInlinedParity verifies the inlined varint loop produces
// byte-identical output to the current appendUnsigned loop.
func TestVarintInlinedParity(t *testing.T) {
	sizes := []int{4, 10, 50, 100, 256, 1000}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("Size%d", size), func(t *testing.T) {
			dods := varintPOCDods(size)

			// Reference: current appendUnsigned path
			refBuf := pool.GetBlobBuffer()
			defer pool.PutBlobBuffer(refBuf)

			refBuf.Grow(len(dods) * binary.MaxVarintLen64)
			for _, dod := range dods {
				zigzag := uint64((dod << 1) ^ (dod >> 63))
				varintPOCAppendUnsigned(refBuf, zigzag)
			}

			// Inlined: pre-Grow + append loop
			inlBuf := pool.GetBlobBuffer()
			defer pool.PutBlobBuffer(inlBuf)

			varintPOCWriteBatch(inlBuf, dods)

			require.Equal(t, refBuf.Bytes(), inlBuf.Bytes(),
				"inlined loop must produce identical output to appendUnsigned loop")
		})
	}
}

// BenchmarkVarintEncode_AppendUnsigned benchmarks the current per-value
// appendUnsigned path (simulated standalone, no encoder struct).
func BenchmarkVarintEncode_AppendUnsigned(b *testing.B) {
	sizes := []int{100, 256, 1000, 10000}

	for _, size := range sizes {
		dods := varintPOCDods(size)

		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			buf := pool.GetBlobBuffer()
			defer pool.PutBlobBuffer(buf)

			buf.Grow(len(dods) * binary.MaxVarintLen64)

			b.ResetTimer()
			for b.Loop() {
				buf.B = buf.B[:0]
				for _, dod := range dods {
					zigzag := uint64((dod << 1) ^ (dod >> 63))
					varintPOCAppendUnsigned(buf, zigzag)
				}
			}
		})
	}
}

// BenchmarkVarintEncode_Inlined benchmarks the proposed inlined loop
// with pre-Grow + direct append.
func BenchmarkVarintEncode_Inlined(b *testing.B) {
	sizes := []int{100, 256, 1000, 10000}

	for _, size := range sizes {
		dods := varintPOCDods(size)

		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			buf := pool.GetBlobBuffer()
			defer pool.PutBlobBuffer(buf)

			b.ResetTimer()
			for b.Loop() {
				buf.B = buf.B[:0]
				varintPOCWriteBatch(buf, dods)
			}
		})
	}
}

// BenchmarkDeltaEncoder_WriteSlice_InlinedPOC benchmarks the full encoder
// pipeline to see end-to-end impact.
func BenchmarkDeltaEncoder_WriteSlice_InlinedPOC(b *testing.B) {
	sizes := []int{10, 50, 100, 1000, 10000}

	for _, size := range sizes {
		timestamps := varintPOCTimestamps(size)

		b.Run(fmt.Sprintf("Original/Size%d", size), func(b *testing.B) {
			for b.Loop() {
				enc := NewTimestampDeltaEncoder()
				enc.WriteSlice(timestamps)
				_ = enc.Bytes()
				enc.Finish()
			}
		})

		b.Run(fmt.Sprintf("Inlined/Size%d", size), func(b *testing.B) {
			for b.Loop() {
				enc := NewTimestampDeltaEncoder()
				varintPOCWriteSliceInlined(enc, timestamps)
				_ = enc.Bytes()
				enc.Finish()
			}
		})
	}
}

// --- POC helpers (standalone, no production code modified) ---

// varintPOCAppendUnsigned mirrors the current appendUnsigned + appendSingleByte chain.
func varintPOCAppendUnsigned(buf *pool.ByteBuffer, value uint64) {
	if value <= 0x7F {
		idx := len(buf.B)
		buf.ExtendOrGrow(1)
		buf.B[idx] = byte(value)

		return
	}

	buf.Grow(binary.MaxVarintLen64)
	buf.B = binary.AppendUvarint(buf.B, value)
}

// varintPOCWriteBatch is the proposed inlined loop: pre-Grow + direct append.
func varintPOCWriteBatch(buf *pool.ByteBuffer, dods []int64) {
	buf.Grow(len(dods) * binary.MaxVarintLen64)

	for _, dod := range dods {
		zigzag := uint64((dod << 1) ^ (dod >> 63))
		if zigzag <= 0x7F {
			buf.B = append(buf.B, byte(zigzag))
		} else {
			buf.B = binary.AppendUvarint(buf.B, zigzag)
		}
	}
}

// varintPOCWriteSliceInlined simulates the full WriteSlice with the inlined approach.
func varintPOCWriteSliceInlined(e *TimestampDeltaEncoder, timestampsUs []int64) {
	tsLen := len(timestampsUs)
	if tsLen == 0 {
		return
	}

	currentSeqCount := e.seqCount
	e.count += tsLen
	e.seqCount += tsLen
	e.reserveFor(tsLen)

	prevTS := e.prevTS
	prevDelta := e.prevDelta
	startIdx := 0

	if currentSeqCount == 0 {
		ts := timestampsUs[0]
		e.appendUnsigned(uint64(ts))
		prevTS = ts
		startIdx = 1
		currentSeqCount++
	}

	if startIdx < tsLen && currentSeqCount == 1 {
		ts := timestampsUs[startIdx]
		delta := ts - prevTS
		zigzag := (delta << 1) ^ (delta >> 63)
		e.appendUnsigned(uint64(zigzag))
		prevTS = ts
		prevDelta = delta
		startIdx++
	}

	remaining := timestampsUs[startIdx:]
	if shouldUseDeltaOfDeltaSIMD(len(remaining)) {
		var deltaBuf [deltaOfDeltaSIMDChunkSize]int64

		for len(remaining) > 0 {
			n := min(len(remaining), deltaOfDeltaSIMDChunkSize)
			prevTS, prevDelta = deltaOfDeltaIntoActive(deltaBuf[:n], remaining[:n], prevTS, prevDelta)

			// Inlined varint batch (the proposed optimization)
			e.buf.Grow(n * binary.MaxVarintLen64)
			for _, dod := range deltaBuf[:n] {
				zigzag := uint64((dod << 1) ^ (dod >> 63))
				if zigzag <= 0x7F {
					e.buf.B = append(e.buf.B, byte(zigzag))
				} else {
					e.buf.B = binary.AppendUvarint(e.buf.B, zigzag)
				}
			}

			remaining = remaining[n:]
		}
	} else {
		// Inlined scalar path
		e.buf.Grow(len(remaining) * binary.MaxVarintLen64)
		for _, ts := range remaining {
			dod := nextDeltaOfDelta(ts, &prevTS, &prevDelta)
			zigzag := uint64((dod << 1) ^ (dod >> 63))
			if zigzag <= 0x7F {
				e.buf.B = append(e.buf.B, byte(zigzag))
			} else {
				e.buf.B = binary.AppendUvarint(e.buf.B, zigzag)
			}
		}
	}

	e.prevTS = prevTS
	e.prevDelta = prevDelta
}

func varintPOCTimestamps(size int) []int64 {
	timestamps := make([]int64, size)
	baseUs := time.Now().UnixMicro()

	for i := range size {
		timestamps[i] = baseUs + int64(i)*1_000_000 + int64(i%5)*100
	}

	return timestamps
}

func varintPOCDods(size int) []int64 {
	timestamps := varintPOCTimestamps(size + 2)
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
