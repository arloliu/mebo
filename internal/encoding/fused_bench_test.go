package encoding

// Codec-level benchmarks for the fused decode hot paths, using the measurev2
// data shape (200 points, ±0.5% value random walk, ±0.1% timestamp jitter).
// The Each (callback) forms are what the blob iteration hot paths consume;
// the Seq2 All forms delegate to them.

import (
	"math/rand"
	"testing"
	"time"
)

// genFusedBenchStream encodes one measurev2-shaped metric stream.
func genFusedBenchStream(points int, seed int64) (tsData, valData []byte) {
	rng := rand.New(rand.NewSource(seed))
	start := time.Unix(1700000000, 0).UTC()

	intervalUs := int64(time.Second / time.Microsecond)
	ts := start.UnixMicro()
	val := 50.0 + rng.Float64()*50.0
	timestamps := make([]int64, 0, points)
	values := make([]float64, 0, points)
	for range points {
		jitter := int64(float64(intervalUs) * 0.001 * (rng.Float64()*2 - 1))
		ts += intervalUs + jitter
		val *= 1 + 0.005*(rng.Float64()*2-1)
		timestamps = append(timestamps, ts)
		values = append(values, val)
	}

	tsEncoder := NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData = append([]byte(nil), tsEncoder.Bytes()...)
	tsEncoder.Finish()

	valEncoder := NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData = append([]byte(nil), valEncoder.Bytes()...)
	valEncoder.Finish()

	return tsData, valData
}

func BenchmarkFusedDeltaGorillaEach(b *testing.B) {
	tsData, valData := genFusedBenchStream(200, 42)

	b.ReportAllocs()
	for b.Loop() {
		var sink int64
		var vsink float64
		FusedDeltaGorillaEach(tsData, valData, 200, func(_ int, ts int64, val float64) bool {
			sink += ts
			vsink += val

			return true
		})
		if sink == 0 && vsink == 0 {
			b.Fatal("no data")
		}
	}
}

func BenchmarkFusedDeltaGorillaAll(b *testing.B) {
	tsData, valData := genFusedBenchStream(200, 42)

	b.ReportAllocs()
	for b.Loop() {
		var sink int64
		var vsink float64
		for ts, val := range FusedDeltaGorillaAll(tsData, valData, 200) {
			sink += ts
			vsink += val
		}
		if sink == 0 && vsink == 0 {
			b.Fatal("no data")
		}
	}
}
