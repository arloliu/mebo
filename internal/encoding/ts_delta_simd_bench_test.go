package encoding

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkDeltaOfDeltaBackends(b *testing.B) {
	sizes := []int{30, 100, 200, 1000, 10000}

	for _, size := range sizes {
		ts := benchmarkDeltaOfDeltaTimestamps(size)
		prevTS := ts[1]
		prevDelta := ts[1] - ts[0]
		src := ts[2:]

		for _, backend := range deltaOfDeltaBackends {
			b.Run(fmt.Sprintf("%s/Size%d", deltaOfDeltaBackendName(backend), size), func(b *testing.B) {
				if !deltaOfDeltaBackendSupported(backend) {
					b.Skip("backend not supported in this build or on this CPU")
				}

				out := make([]int64, len(src))
				restore := setDeltaOfDeltaBackendForTest(backend)
				defer restore()

				kernel := deltaOfDeltaKernelForBackend(backend)
				b.ResetTimer()

				for b.Loop() {
					_, _ = kernel(out, src, prevTS, prevDelta)
				}
			})
		}
	}
}

func BenchmarkTimestampDeltaEncoder_WriteSlice_Backends(b *testing.B) {
	benchmarkTimestampDeltaWriteSliceBackends(b, false)
}

func BenchmarkTimestampDeltaPackedEncoder_WriteSlice_Backends(b *testing.B) {
	benchmarkTimestampDeltaWriteSliceBackends(b, true)
}

func benchmarkTimestampDeltaWriteSliceBackends(b *testing.B, packed bool) {
	timestamps := benchmarkDeltaOfDeltaTimestamps(10_000)

	for _, backend := range deltaOfDeltaBackends {
		b.Run(deltaOfDeltaBackendName(backend), func(b *testing.B) {
			if !deltaOfDeltaBackendSupported(backend) {
				b.Skip("backend not supported in this build or on this CPU")
			}

			restore := setDeltaOfDeltaBackendForTest(backend)
			defer restore()

			b.ResetTimer()
			for b.Loop() {
				if packed {
					encoder := NewTimestampDeltaPackedEncoder()
					encoder.WriteSlice(timestamps)
					_ = encoder.Bytes()
					encoder.Finish()

					continue
				}

				encoder := NewTimestampDeltaEncoder()
				encoder.WriteSlice(timestamps)
				_ = encoder.Bytes()
				encoder.Finish()
			}
		})
	}
}

func benchmarkDeltaOfDeltaTimestamps(size int) []int64 {
	timestamps := make([]int64, size)
	base := time.Now().UnixMicro()
	for i := range timestamps {
		timestamps[i] = base + int64(i)*1_000_000 + int64(i%5)*100
	}

	return timestamps
}
