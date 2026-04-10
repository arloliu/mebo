package encoding

import (
	"fmt"
	"testing"
	"time"
)

// BenchmarkTimestampDeltaPackedDecoder_DecodeAll_Backends benchmarks DecodeAll
// for each available backend across a range of input sizes.
func BenchmarkTimestampDeltaPackedDecoder_DecodeAll_Backends(b *testing.B) {
	sizes := []int{30, 100, 200, 1000, 10000}
	dec := NewTimestampDeltaPackedDecoder()

	for _, size := range sizes {
		ts := benchmarkPackedDecodeTimestamps(size)

		enc := NewTimestampDeltaPackedEncoder()
		enc.WriteSlice(ts)
		encoded := make([]byte, len(enc.Bytes()))
		copy(encoded, enc.Bytes())
		count := enc.Len()
		enc.Finish()

		dst := make([]int64, count)

		for _, backend := range allDeltaPackedDecodeBackends {
			b.Run(fmt.Sprintf("%s/Size%d", deltaPackedDecodeBackendName(backend), size), func(b *testing.B) {
				if !deltaPackedDecodeBackendSupported(backend) {
					b.Skip("backend not supported")
				}

				restore := setDeltaPackedDecodeBackendForTest(backend)
				defer restore()

				b.ResetTimer()
				for b.Loop() {
					_ = dec.DecodeAll(encoded, count, dst)
				}
			})
		}
	}
}

// BenchmarkTimestampDeltaPackedDecoder_All_Backends benchmarks the All() iterator
// for each available backend.
func BenchmarkTimestampDeltaPackedDecoder_All_Backends(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	dec := NewTimestampDeltaPackedDecoder()

	for _, size := range sizes {
		ts := benchmarkPackedDecodeTimestamps(size)

		enc := NewTimestampDeltaPackedEncoder()
		enc.WriteSlice(ts)
		encoded := make([]byte, len(enc.Bytes()))
		copy(encoded, enc.Bytes())
		count := enc.Len()
		enc.Finish()

		for _, backend := range allDeltaPackedDecodeBackends {
			b.Run(fmt.Sprintf("%s/Size%d", deltaPackedDecodeBackendName(backend), size), func(b *testing.B) {
				if !deltaPackedDecodeBackendSupported(backend) {
					b.Skip("backend not supported")
				}

				restore := setDeltaPackedDecodeBackendForTest(backend)
				defer restore()

				b.ResetTimer()
				for b.Loop() {
					for range dec.All(encoded, count) { //nolint:revive // intentionally drain iterator in benchmark
					}
				}
			})
		}
	}
}

// BenchmarkTimestampDeltaPackedDecodeScalarBulk microbenchmarks the scalar bulk helper
// in isolation (no encoder overhead, no header/tail).
func BenchmarkTimestampDeltaPackedDecodeScalarBulk(b *testing.B) {
	sizes := []int{100, 1000, 10000}

	for _, size := range sizes {
		ts := benchmarkPackedDecodeTimestamps(size)
		enc := NewTimestampDeltaPackedEncoder()
		enc.WriteSlice(ts)
		encoded := make([]byte, len(enc.Bytes()))
		copy(encoded, enc.Bytes())
		count := enc.Len()
		enc.Finish()

		first, offset, ok := decodeVarint64(encoded, 0)
		if !ok {
			b.Fatalf("failed to decode first header varint for size %d", size)
		}

		zigzag, offset, ok := decodeVarint64(encoded, offset)
		if !ok {
			b.Fatalf("failed to decode second header varint for size %d", size)
		}

		prevTS := int64(first)
		prevDelta := decodeZigZag64(zigzag)
		prevTS += prevDelta

		bulkData := encoded[offset:]
		bulkCount := max(count-2, 0)
		dst := make([]int64, bulkCount)

		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			for b.Loop() {
				_, _ = decodeDeltaPackedScalarBulk(dst, bulkData, bulkCount, prevTS, prevDelta)
			}
		})
	}
}

func benchmarkPackedDecodeTimestamps(size int) []int64 {
	timestamps := make([]int64, size)
	base := time.Now().UnixMicro()

	for i := range size {
		timestamps[i] = base + int64(i)*1_000_000 + int64(i%5)*100
	}

	return timestamps
}
