package encoding

import (
	"fmt"
	"testing"

	"github.com/arloliu/mebo/endian"
)

// bp128BenchData builds a realistic timestamp series. bursty injects periodic
// gaps/restarts (big delta-of-delta spikes) — BP128's worst case for ratio.
func bp128BenchData(n int, bursty bool) []int64 {
	ts := genTimestamps(n, 0.1, int64(n)*31+1)
	if bursty {
		for i := 100; i < len(ts); i += 137 {
			ts[i] += 5_000_000
		}
		for i := 1; i < len(ts); i++ {
			if ts[i] <= ts[i-1] {
				ts[i] = ts[i-1] + 1
			}
		}
	}

	return ts
}

// BenchmarkBP128VsDelta compares the full BP128 pipeline against the production
// Delta codec on encode, decode, and ratio, at the pinned gate sizes over clean
// and bursty profiles. BP128 uses whichever block kernel is active (scalar today,
// AVX-512 asm once wired); Delta runs its default (SIMD-asm) backend.
func BenchmarkBP128VsDelta(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	sizes := []int{32, 127, 256, 257, 1000, 40192}
	profiles := []struct {
		name   string
		bursty bool
	}{{"clean", false}, {"bursty", true}}

	for _, prof := range profiles {
		for _, n := range sizes {
			ts := bp128BenchData(n, prof.bursty)

			var enc bp128Codec
			bpData := enc.encode(nil, ts, eng)
			de := NewTimestampDeltaEncoder()
			de.WriteSlice(ts)
			deltaData := append([]byte(nil), de.Bytes()...)
			de.Finish()
			bpBPP := float64(len(bpData)) / float64(n)
			dBPP := float64(len(deltaData)) / float64(n)

			b.Run(fmt.Sprintf("%s/n%d/Encode/BP128", prof.name, n), func(b *testing.B) {
				var c bp128Codec
				buf := make([]byte, 0, len(bpData))
				b.ReportAllocs()
				for b.Loop() {
					buf = c.encode(buf[:0], ts, eng)
				}
				b.ReportMetric(bpBPP, "B/point")
			})
			b.Run(fmt.Sprintf("%s/n%d/Encode/Delta", prof.name, n), func(b *testing.B) {
				dEnc := NewTimestampDeltaEncoder()
				b.ReportAllocs()
				for b.Loop() {
					dEnc.Reset()
					dEnc.WriteSlice(ts)
					_ = dEnc.Bytes()
				}
				b.ReportMetric(dBPP, "B/point")
				dEnc.Finish()
			})
			b.Run(fmt.Sprintf("%s/n%d/Decode/BP128", prof.name, n), func(b *testing.B) {
				var c bp128Codec
				dst := make([]int64, n)
				b.ReportAllocs()
				for b.Loop() {
					c.decodeInto(dst, bpData, eng)
				}
			})
			b.Run(fmt.Sprintf("%s/n%d/Decode/Delta", prof.name, n), func(b *testing.B) {
				dec := NewTimestampDeltaDecoder()
				dst := make([]int64, n)
				b.ReportAllocs()
				for b.Loop() {
					dec.DecodeAll(deltaData, n, dst)
				}
			})
		}
	}
}
