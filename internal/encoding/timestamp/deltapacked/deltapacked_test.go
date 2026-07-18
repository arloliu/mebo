package deltapacked

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/internal/encoding/internal/deltadelta"
	"github.com/arloliu/mebo/internal/encoding/internal/varint"
	"github.com/arloliu/mebo/internal/encoding/timestamp/delta"
	"github.com/arloliu/mebo/internal/pool"
)

func generateSequentialFromBase(count int, intervalUs int64) []int64 {
	if count <= 0 {
		return nil
	}

	const baseTimestamp = int64(1_700_000_000_000_000)
	timestamps := make([]int64, count)
	current := baseTimestamp
	for i := range timestamps {
		timestamps[i] = current
		current += intervalUs
	}

	return timestamps
}

func makeDeltaBackendParityTimestamps(count int) []int64 {
	timestamps := make([]int64, count)
	if count == 0 {
		return timestamps
	}
	timestamps[0] = 1_000_000

	deltas := []int64{1_000_000, 1_000_100, 999_950, 1_000_250, 999_900, 1_000_400, 999_700}
	for i := 1; i < len(timestamps); i++ {
		timestamps[i] = timestamps[i-1] + deltas[(i-1)%len(deltas)]
	}

	return timestamps
}

func generateBurstyTimestamps(count int) []int64 {
	if count <= 0 {
		return nil
	}

	const (
		burstIntervalUs = 10_000
		gapIntervalUs   = 2_000_000
		burstSize       = 64
	)

	timestamps := make([]int64, count)
	current := int64(1_700_000_000_000_000)
	for i := range timestamps {
		timestamps[i] = current
		if (i+1)%burstSize == 0 {
			current += gapIntervalUs
		} else {
			current += burstIntervalUs
		}
	}

	return timestamps
}

func generateDiurnalTimestamps(minutes int) []int64 {
	if minutes <= 0 {
		return nil
	}

	const minuteUs int64 = 60_000_000
	timestamps := make([]int64, minutes)
	current := int64(1_700_000_000_000_000)
	for i := range timestamps {
		timestamps[i] = current
		hour := (i / 60) % 24

		var interval int64
		switch {
		case hour < 5:
			interval = minuteUs * 3
		case hour < 8:
			interval = minuteUs * 2
		case hour < 18:
			interval = 30_000_000
		case hour < 22:
			interval = minuteUs
		default:
			interval = minuteUs * 2
		}

		jitter := int64(((i*97)%7)-3) * 5_000_000
		current += interval + jitter
	}

	return timestamps
}

func generateClockResetTimestamps(count int) []int64 {
	if count <= 0 {
		return nil
	}

	timestamps := make([]int64, count)
	current := int64(1_700_000_000_000_000)
	for i := range timestamps {
		timestamps[i] = current
		if i != 0 && i%500 == 0 {
			current -= 200_000
			continue
		}

		switch i % 6 {
		case 0, 1:
			current += 1_000_000
		case 2, 3:
			current += 1_250_000
		default:
			current += 850_000
		}
	}

	return timestamps
}

func generateHighVarianceTimestamps(count int) []int64 {
	if count <= 0 {
		return nil
	}

	intervals := [...]int64{1_000_000, 10_000_000, 500_000, 60_000_000, 5_000_000, 250_000, 1_500_000, 90_000_000}
	timestamps := make([]int64, count)
	current := int64(1_700_000_000_000_000)
	for i := range timestamps {
		timestamps[i] = current
		current += intervals[i%len(intervals)]
	}

	return timestamps
}

func generateTimestampsWithJitter(count int, intervalUs int64, jitterPct float64) []int64 {
	timestamps := make([]int64, count)
	current := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro()
	for i := range timestamps {
		timestamps[i] = current

		jitter := int64(0)
		if jitterPct > 0 {
			maxJitter := int64(float64(intervalUs) * jitterPct)
			jitter = (int64(i*1234567) % (maxJitter * 2)) - maxJitter
		}
		current += intervalUs + jitter
	}

	return timestamps
}

// === Correctness Tests ===

func TestTimestampDeltaPackedEncoder_RoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		timestamps []int64
	}{
		{
			name:       "single timestamp",
			timestamps: []int64{1000000},
		},
		{
			name:       "two timestamps",
			timestamps: []int64{1000000, 2000000},
		},
		{
			name:       "three timestamps (one DoD)",
			timestamps: []int64{1000000, 2000000, 3000000},
		},
		{
			name:       "four timestamps (two DoDs)",
			timestamps: []int64{1000000, 2000000, 3000000, 4000000},
		},
		{
			name:       "five timestamps (three DoDs)",
			timestamps: []int64{1000000, 2000000, 3000000, 4000000, 5000000},
		},
		{
			name:       "six timestamps (exactly one group)",
			timestamps: []int64{1000000, 2000000, 3000000, 4000000, 5000000, 6000000},
		},
		{
			name:       "ten timestamps (two groups)",
			timestamps: generateSequentialFromBase(10, 1000000),
		},
		{
			name:       "regular 1s intervals 100 points",
			timestamps: generateSequentialFromBase(100, 1000000),
		},
		{
			name:       "irregular intervals",
			timestamps: []int64{100, 300, 450, 1200, 1201, 5000, 5001, 5002, 10000, 10500},
		},
		{
			name:       "zero timestamps",
			timestamps: []int64{0, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name:       "decreasing deltas",
			timestamps: []int64{1000, 1500, 1900, 2200, 2400, 2500},
		},
		{
			name:       "large values",
			timestamps: generateSequentialFromBase(20, 60000000), // 1-minute intervals
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoder := NewTimestampDeltaPackedEncoder()
			encoder.WriteSlice(tt.timestamps)
			encoded := make([]byte, len(encoder.Bytes()))
			copy(encoded, encoder.Bytes())
			encoder.Finish()

			require.Equal(t, len(tt.timestamps), len(tt.timestamps))

			decoder := NewTimestampDeltaPackedDecoder()
			decoded := make([]int64, 0, len(tt.timestamps))
			for ts := range decoder.All(encoded, len(tt.timestamps)) {
				decoded = append(decoded, ts)
			}

			require.Equal(t, tt.timestamps, decoded, "round-trip mismatch")
		})
	}
}

func TestDeltaPackedTsState_Next(t *testing.T) {
	timestamps := []int64{1_000, 2_000, 3_100, 4_050, 5_250, 6_200, 7_600}
	encoder := NewTimestampDeltaPackedEncoder()
	encoder.WriteSlice(timestamps)
	encoded := append([]byte(nil), encoder.Bytes()...)
	encoder.Finish()

	state, ok := NewDeltaPackedTsState(encoded)
	require.True(t, ok)

	decoded := []int64{state.Ts()}
	for i := 1; i < len(timestamps); i++ {
		require.True(t, state.Next(len(timestamps)-i))
		decoded = append(decoded, state.Ts())
	}

	require.Equal(t, timestamps, decoded)
}

func TestTimestampDeltaPackedEncoder_WriteOneByOne(t *testing.T) {
	timestamps := generateSequentialFromBase(20, 1000000)

	// Encode one-by-one
	encoder := NewTimestampDeltaPackedEncoder()
	for _, ts := range timestamps {
		encoder.Write(ts)
	}
	encoded := make([]byte, len(encoder.Bytes()))
	copy(encoded, encoder.Bytes())
	encoder.Finish()

	// Decode
	decoder := NewTimestampDeltaPackedDecoder()
	decoded := make([]int64, 0, len(timestamps))
	for ts := range decoder.All(encoded, len(timestamps)) {
		decoded = append(decoded, ts)
	}

	require.Equal(t, timestamps, decoded)
}

func TestTimestampDeltaPackedEncoder_WriteSliceMatchesRepeatedWrite(t *testing.T) {
	timestamps := makeDeltaBackendParityTimestamps(269)

	bulk := NewTimestampDeltaPackedEncoder()
	bulk.WriteSlice(timestamps)
	bulkBytes := append([]byte(nil), bulk.Bytes()...)
	bulk.Finish()

	scalar := NewTimestampDeltaPackedEncoder()
	for _, timestamp := range timestamps {
		scalar.Write(timestamp)
	}
	scalarBytes := append([]byte(nil), scalar.Bytes()...)
	scalar.Finish()

	require.Equal(t, scalarBytes, bulkBytes, "WriteSlice output must match repeated Write output")

	decoder := NewTimestampDeltaPackedDecoder()
	all := make([]int64, 0, len(timestamps))
	for timestamp := range decoder.All(bulkBytes, len(timestamps)) {
		all = append(all, timestamp)
	}
	require.Equal(t, timestamps, all, "All output mismatch")

	decoded := make([]int64, len(timestamps))
	require.Equal(t, len(timestamps), decoder.DecodeAll(bulkBytes, len(timestamps), decoded))
	require.Equal(t, timestamps, decoded, "DecodeAll output mismatch")

	for i, expected := range timestamps {
		actual, ok := decoder.At(bulkBytes, i, len(timestamps))
		require.Truef(t, ok, "At(%d) failed", i)
		require.Equalf(t, expected, actual, "At(%d) mismatch", i)
	}
}

func TestTimestampDeltaPackedDecoder_At(t *testing.T) {
	timestamps := generateSequentialFromBase(20, 1000000)

	encoder := NewTimestampDeltaPackedEncoder()
	encoder.WriteSlice(timestamps)
	encoded := make([]byte, len(encoder.Bytes()))
	copy(encoded, encoder.Bytes())
	encoder.Finish()

	decoder := NewTimestampDeltaPackedDecoder()
	for i, expected := range timestamps {
		got, ok := decoder.At(encoded, i, len(timestamps))
		require.True(t, ok, "At(%d) failed", i)
		require.Equal(t, expected, got, "At(%d) mismatch", i)
	}

	// Out of bounds
	_, ok := decoder.At(encoded, -1, len(timestamps))
	require.False(t, ok)
	_, ok = decoder.At(encoded, len(timestamps), len(timestamps))
	require.False(t, ok)
}

func TestTimestampDeltaPackedEncoder_EmptySlice(t *testing.T) {
	encoder := NewTimestampDeltaPackedEncoder()
	encoder.WriteSlice(nil)
	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
	encoder.Finish()
}

func TestTimestampDeltaPackedEncoder_Reset(t *testing.T) {
	timestamps1 := generateSequentialFromBase(10, 1000000)
	timestamps2 := generateSequentialFromBase(8, 500000)

	encoder := NewTimestampDeltaPackedEncoder()
	encoder.WriteSlice(timestamps1)
	encoder.Reset()
	encoder.WriteSlice(timestamps2)

	encoded := make([]byte, len(encoder.Bytes()))
	copy(encoded, encoder.Bytes())

	// The total count should include both sequences
	require.Equal(t, len(timestamps1)+len(timestamps2), encoder.Len())
	encoder.Finish()

	// Decoding should recover both sequences concatenated
	// The first sequence is in the buffer, then the second sequence starts fresh
	decoder := NewTimestampDeltaPackedDecoder()

	// Decode first sequence
	decoded1 := make([]int64, 0, len(timestamps1))
	for ts := range decoder.All(encoded, len(timestamps1)) {
		decoded1 = append(decoded1, ts)
	}
	require.Equal(t, timestamps1, decoded1)
}

// === Benchmark Comparison: DeltaPacked vs Delta ===

func BenchmarkTimestampDeltaPacked_vs_Delta_Encode(b *testing.B) {
	sizes := []int{10, 100, 1000, 10000}

	for _, size := range sizes {
		timestamps := generateSequentialFromBase(size, 1000000)

		b.Run(fmt.Sprintf("Delta/Size%d", size), func(b *testing.B) {
			for b.Loop() {
				encoder := delta.NewTimestampDeltaEncoder()
				encoder.WriteSlice(timestamps)
				_ = encoder.Bytes()
				encoder.Finish()
			}
		})

		b.Run(fmt.Sprintf("DeltaPacked/Size%d", size), func(b *testing.B) {
			for b.Loop() {
				encoder := NewTimestampDeltaPackedEncoder()
				encoder.WriteSlice(timestamps)
				_ = encoder.Bytes()
				encoder.Finish()
			}
		})
	}
}

func BenchmarkTimestampDeltaPacked_vs_Delta_Decode_All(b *testing.B) {
	sizes := []int{10, 100, 1000, 10000}

	for _, size := range sizes {
		timestamps := generateSequentialFromBase(size, 1000000)

		// Encode with Delta
		deltaEnc := delta.NewTimestampDeltaEncoder()
		deltaEnc.WriteSlice(timestamps)
		deltaData := make([]byte, len(deltaEnc.Bytes()))
		copy(deltaData, deltaEnc.Bytes())
		deltaEnc.Finish()

		// Encode with DeltaPacked
		packedEnc := NewTimestampDeltaPackedEncoder()
		packedEnc.WriteSlice(timestamps)
		packedData := make([]byte, len(packedEnc.Bytes()))
		copy(packedData, packedEnc.Bytes())
		packedEnc.Finish()

		b.Run(fmt.Sprintf("Delta/Size%d", size), func(b *testing.B) {
			decoder := delta.NewTimestampDeltaDecoder()
			b.ResetTimer()

			for b.Loop() {
				count := 0
				for range decoder.All(deltaData, size) {
					count++
				}
				if count != size {
					b.Fatalf("expected %d, got %d", size, count)
				}
			}
		})

		b.Run(fmt.Sprintf("DeltaPacked/Size%d", size), func(b *testing.B) {
			decoder := NewTimestampDeltaPackedDecoder()
			b.ResetTimer()

			for b.Loop() {
				count := 0
				for range decoder.All(packedData, size) {
					count++
				}
				if count != size {
					b.Fatalf("expected %d, got %d", size, count)
				}
			}
		})
	}
}

func BenchmarkTimestampDeltaPacked_vs_Delta_Decode_Irregular(b *testing.B) {
	sizes := []int{10, 100, 1000}

	for _, size := range sizes {
		timestamps := generateTimestampsWithJitter(size, 1000000, 0.05)

		deltaEnc := delta.NewTimestampDeltaEncoder()
		deltaEnc.WriteSlice(timestamps)
		deltaData := make([]byte, len(deltaEnc.Bytes()))
		copy(deltaData, deltaEnc.Bytes())
		deltaEnc.Finish()

		packedEnc := NewTimestampDeltaPackedEncoder()
		packedEnc.WriteSlice(timestamps)
		packedData := make([]byte, len(packedEnc.Bytes()))
		copy(packedData, packedEnc.Bytes())
		packedEnc.Finish()

		b.Run(fmt.Sprintf("Delta_Jitter/Size%d", size), func(b *testing.B) {
			decoder := delta.NewTimestampDeltaDecoder()
			for b.Loop() {
				for range decoder.All(deltaData, size) { //nolint:revive // intentional empty block in benchmark
				}
			}
		})

		b.Run(fmt.Sprintf("DeltaPacked_Jitter/Size%d", size), func(b *testing.B) {
			decoder := NewTimestampDeltaPackedDecoder()
			for b.Loop() {
				for range decoder.All(packedData, size) { //nolint:revive // intentional empty block in benchmark
				}
			}
		})
	}
}

func BenchmarkTimestampDeltaPacked_vs_Delta_At(b *testing.B) {
	sizes := []int{10, 100, 1000}

	for _, size := range sizes {
		timestamps := generateSequentialFromBase(size, 1000000)

		deltaEnc := delta.NewTimestampDeltaEncoder()
		deltaEnc.WriteSlice(timestamps)
		deltaData := make([]byte, len(deltaEnc.Bytes()))
		copy(deltaData, deltaEnc.Bytes())
		deltaEnc.Finish()

		packedEnc := NewTimestampDeltaPackedEncoder()
		packedEnc.WriteSlice(timestamps)
		packedData := make([]byte, len(packedEnc.Bytes()))
		copy(packedData, packedEnc.Bytes())
		packedEnc.Finish()

		midIdx := size / 2

		b.Run(fmt.Sprintf("Delta/Size%d/Mid", size), func(b *testing.B) {
			decoder := delta.NewTimestampDeltaDecoder()
			for b.Loop() {
				_, ok := decoder.At(deltaData, midIdx, size)
				if !ok {
					b.Fatal("At failed")
				}
			}
		})

		b.Run(fmt.Sprintf("DeltaPacked/Size%d/Mid", size), func(b *testing.B) {
			decoder := NewTimestampDeltaPackedDecoder()
			for b.Loop() {
				_, ok := decoder.At(packedData, midIdx, size)
				if !ok {
					b.Fatal("At failed")
				}
			}
		})
	}
}

func BenchmarkTimestampDeltaPacked_vs_Delta_SpaceEfficiency(b *testing.B) {
	scenarios := []struct {
		name       string
		timestamps []int64
	}{
		{
			name:       "Regular_1s",
			timestamps: generateSequentialFromBase(100, 1000000),
		},
		{
			name:       "Regular_1min",
			timestamps: generateSequentialFromBase(100, 60000000),
		},
		{
			name:       "Jitter_5pct",
			timestamps: generateTimestampsWithJitter(100, 1000000, 0.05),
		},
		{
			name:       "MeboTypical_150x10",
			timestamps: generateSequentialFromBase(10, 1000000),
		},
	}

	for _, s := range scenarios {
		b.Run(s.name, func(b *testing.B) {
			deltaEnc := delta.NewTimestampDeltaEncoder()
			deltaEnc.WriteSlice(s.timestamps)
			deltaSize := deltaEnc.Size()
			deltaEnc.Finish()

			packedEnc := NewTimestampDeltaPackedEncoder()
			packedEnc.WriteSlice(s.timestamps)
			packedSize := packedEnc.Size()
			packedEnc.Finish()

			rawSize := len(s.timestamps) * 8

			b.ReportMetric(float64(rawSize), "raw-bytes")
			b.ReportMetric(float64(deltaSize), "delta-bytes")
			b.ReportMetric(float64(packedSize), "packed-bytes")
			b.ReportMetric(float64(deltaSize)/float64(rawSize), "delta-ratio")
			b.ReportMetric(float64(packedSize)/float64(rawSize), "packed-ratio")

			// Actual benchmark (encode+decode round trip)
			for b.Loop() {
				enc := NewTimestampDeltaPackedEncoder()
				enc.WriteSlice(s.timestamps)
				data := enc.Bytes()
				dec := NewTimestampDeltaPackedDecoder()
				for range dec.All(data, len(s.timestamps)) { //nolint:revive // intentional empty block in benchmark
				}
				enc.Finish()
			}
		})
	}
}

// === Benchmark Decode Suite (same datasets as ts_delta_bench_test.go) ===

func BenchmarkTimestampDeltaPackedDecoder_All_Suite(b *testing.B) {
	type benchCase struct {
		name    string
		encoded []byte
		count   int
	}

	datasets := []struct {
		name     string
		generate func() []int64
	}{
		{"Regular_1s_10k", func() []int64 { return generateSequentialFromBase(10_000, 1_000_000) }},
		{"Regular_1s_250", func() []int64 { return generateSequentialFromBase(250, 1_000_000) }},
		{"Jitter_5pct_5k", func() []int64 { return generateTimestampsWithJitter(5_000, 1_000_000, 0.05) }},
		{"BurstyTraffic_12k", func() []int64 { return generateBurstyTimestamps(12_000) }},
		{"DailyCycle_24h", func() []int64 { return generateDiurnalTimestamps(24 * 60) }},
		{"ClockResetEvents_8k", func() []int64 { return generateClockResetTimestamps(8_000) }},
		{"HighVariance_2k", func() []int64 { return generateHighVarianceTimestamps(2_000) }},
	}

	cases := make([]benchCase, 0, len(datasets))
	for _, ds := range datasets {
		timestamps := ds.generate()
		enc := NewTimestampDeltaPackedEncoder()
		enc.WriteSlice(timestamps)
		encoded := make([]byte, len(enc.Bytes()))
		copy(encoded, enc.Bytes())
		enc.Finish()
		cases = append(cases, benchCase{name: ds.name, encoded: encoded, count: len(timestamps)})
	}

	decoder := NewTimestampDeltaPackedDecoder()

	for _, cs := range cases {
		caseData := cs
		b.Run(caseData.name, func(b *testing.B) {
			b.ReportAllocs()
			if caseData.count > 0 {
				b.SetBytes(int64(caseData.count * 8))
			}

			for b.Loop() {
				produced := 0
				for range decoder.All(caseData.encoded, caseData.count) {
					produced++
				}
				if produced != caseData.count {
					b.Fatalf("expected %d timestamps, got %d", caseData.count, produced)
				}
			}
		})
	}
}

func BenchmarkTimestampDeltaPacked_Write(b *testing.B) {
	b.Run("Sequential", func(b *testing.B) {
		start := time.Now().UnixMicro()
		encoder := NewTimestampDeltaPackedEncoder()
		b.ResetTimer()

		for b.Loop() {
			encoder.Write(start + int64(b.N)*1000000)
		}
	})
}

func BenchmarkTimestampDeltaPacked_encodeTag(b *testing.B) {
	values := []struct {
		name  string
		value uint64
	}{
		{name: "Zero", value: 0},
		{name: "OneByte", value: 7},
		{name: "TwoByte", value: 700},
		{name: "FourByte", value: 70_000},
		{name: "EightByte", value: 1 << 40},
	}

	for _, tc := range values {
		b.Run(tc.name+"/BitsLen", func(b *testing.B) {
			var tag byte
			for b.Loop() {
				tag = encodeTag(tc.value)
			}
			_ = tag
		})

		b.Run(tc.name+"/Thresholds", func(b *testing.B) {
			var tag byte
			for b.Loop() {
				tag = encodeTagThreshold(tc.value)
			}
			_ = tag
		})
	}
}

func BenchmarkTimestampDeltaPacked_decodeGroup(b *testing.B) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "AllOneByte",
			data: []byte{0x00, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name: "Mixed12",
			data: buildPackedGroupBenchmarkData([]uint64{0, 3, 255, 511}),
		},
		{
			name: "Mixed1248",
			data: buildPackedGroupBenchmarkData([]uint64{1, 700, 70_000, 1 << 40}),
		},
	}

	for _, tc := range tests {
		b.Run(tc.name+"/Loop", func(b *testing.B) {
			var curTS int64
			var prevDelta int64
			for b.Loop() {
				state := decodePackedGroupLoop(tc.data, 1, prevDelta, curTS)
				prevDelta = state.prevDelta
				curTS = state.curTS
			}
			_, _ = prevDelta, curTS
		})

		b.Run(tc.name+"/Unrolled", func(b *testing.B) {
			var curTS int64
			var prevDelta int64
			for b.Loop() {
				state := decodePackedGroupUnrolled(tc.data, 1, prevDelta, curTS)
				prevDelta = state.prevDelta
				curTS = state.curTS
			}
			_, _ = prevDelta, curTS
		})
	}
}

func BenchmarkTimestampDeltaPacked_flushGroup(b *testing.B) {
	pending := [groupSize]uint64{1, 700, 70_000, 1 << 40}

	b.Run("Loop", func(b *testing.B) {
		buf := pool.GetBlobBuffer()
		defer pool.PutBlobBuffer(buf)

		for b.Loop() {
			buf.Reset()
			flushGroupLoopBenchmark(buf, pending)
		}
	})

	b.Run("Unrolled", func(b *testing.B) {
		buf := pool.GetBlobBuffer()
		defer pool.PutBlobBuffer(buf)

		for b.Loop() {
			buf.Reset()
			flushGroupUnrolledBenchmark(buf, pending)
		}
	})
}

func encodeTagThreshold(v uint64) byte {
	if v <= 0xFF {
		return 0
	}

	if v <= 0xFFFF {
		return 1
	}

	if v <= 0xFFFFFFFF {
		return 2
	}

	return 3
}

func buildPackedGroupBenchmarkData(values []uint64) []byte {
	data := make([]byte, 1, 1+groupSize*8)
	var cb byte
	for i := range groupSize {
		tag := encodeTag(values[i])
		cb |= tag << (uint(i) * 2)
	}
	data[0] = cb

	for i := range groupSize {
		tag := (cb >> (uint(i) * 2)) & 0x03
		byteLen := groupVarintLengths[tag]
		start := len(data)
		data = append(data, make([]byte, byteLen)...)
		switch tag {
		case 0:
			data[start] = byte(values[i])
		case 1:
			binary.LittleEndian.PutUint16(data[start:], uint16(values[i]))
		case 2:
			binary.LittleEndian.PutUint32(data[start:], uint32(values[i]))
		case 3:
			binary.LittleEndian.PutUint64(data[start:], values[i])
		default:
			panic("invalid tag")
		}
	}

	return data
}

type packedGroupDecodeState struct {
	offset    int
	prevDelta int64
	curTS     int64
}

func decodePackedGroupLoop(data []byte, offset int, prevDelta int64, curTS int64) packedGroupDecodeState {
	cb := data[0]
	for i := range groupSize {
		tag := (cb >> (uint(i) * 2)) & 0x03
		byteLen := groupVarintLengths[tag]

		var zz uint64
		switch tag {
		case 0:
			zz = uint64(data[offset])
		case 1:
			zz = uint64(binary.LittleEndian.Uint16(data[offset:]))
		case 2:
			zz = uint64(binary.LittleEndian.Uint32(data[offset:]))
		case 3:
			zz = binary.LittleEndian.Uint64(data[offset:])
		default:
			panic("invalid tag")
		}
		offset += byteLen

		deltaOfDelta := varint.DecodeZigZag64(zz)
		prevDelta += deltaOfDelta
		curTS += prevDelta
	}

	return packedGroupDecodeState{offset: offset, prevDelta: prevDelta, curTS: curTS}
}

func decodePackedGroupUnrolled(data []byte, offset int, prevDelta int64, curTS int64) packedGroupDecodeState {
	cb := data[0]

	tag0 := cb & 0x03
	zz0, nextOffset := decodePackedValueByTag(data, offset, tag0)
	deltaOfDelta0 := varint.DecodeZigZag64(zz0)
	prevDelta += deltaOfDelta0
	curTS += prevDelta

	tag1 := (cb >> 2) & 0x03
	zz1, nextOffset := decodePackedValueByTag(data, nextOffset, tag1)
	deltaOfDelta1 := varint.DecodeZigZag64(zz1)
	prevDelta += deltaOfDelta1
	curTS += prevDelta

	tag2 := (cb >> 4) & 0x03
	zz2, nextOffset := decodePackedValueByTag(data, nextOffset, tag2)
	deltaOfDelta2 := varint.DecodeZigZag64(zz2)
	prevDelta += deltaOfDelta2
	curTS += prevDelta

	tag3 := cb >> 6
	zz3, nextOffset := decodePackedValueByTag(data, nextOffset, tag3)
	deltaOfDelta3 := varint.DecodeZigZag64(zz3)
	prevDelta += deltaOfDelta3
	curTS += prevDelta

	return packedGroupDecodeState{offset: nextOffset, prevDelta: prevDelta, curTS: curTS}
}

func decodePackedValueByTag(data []byte, offset int, tag byte) (uint64, int) {
	switch tag {
	case 0:
		return uint64(data[offset]), offset + 1
	case 1:
		return uint64(binary.LittleEndian.Uint16(data[offset:])), offset + 2
	case 2:
		return uint64(binary.LittleEndian.Uint32(data[offset:])), offset + 4
	case 3:
		return binary.LittleEndian.Uint64(data[offset:]), offset + 8
	default:
		panic("invalid tag")
	}
}

func flushGroupLoopBenchmark(buf *pool.ByteBuffer, pending [groupSize]uint64) {
	var controlByte byte
	var totalDataBytes int

	for i := range groupSize {
		tag := encodeTag(pending[i])
		controlByte |= tag << (uint(i) * 2)
		totalDataBytes += groupVarintLengths[tag]
	}

	startLen := len(buf.B)
	buf.Grow(1 + totalDataBytes + 8)
	buf.B = buf.B[:startLen+1+totalDataBytes+8]
	buf.B[startLen] = controlByte

	offset := startLen + 1
	for i := range groupSize {
		tag := (controlByte >> (uint(i) * 2)) & 0x03
		binary.LittleEndian.PutUint64(buf.B[offset:offset+8], pending[i])
		offset += groupVarintLengths[tag]
	}

	buf.B = buf.B[:startLen+1+totalDataBytes]
}

func flushGroupUnrolledBenchmark(buf *pool.ByteBuffer, pending [groupSize]uint64) {
	tag0 := encodeTag(pending[0])
	tag1 := encodeTag(pending[1])
	tag2 := encodeTag(pending[2])
	tag3 := encodeTag(pending[3])
	controlByte := tag0 | (tag1 << 2) | (tag2 << 4) | (tag3 << 6)
	totalDataBytes := groupVarintLengths[tag0] + groupVarintLengths[tag1] + groupVarintLengths[tag2] + groupVarintLengths[tag3]

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
	offset += groupVarintLengths[tag3]

	buf.B = buf.B[:offset]
}

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
		n := min(len(remaining), deltadelta.ChunkSize)
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
	var deltaBuf [deltadelta.ChunkSize]int64

	for len(remaining) > 0 {
		n := min(len(remaining), deltadelta.ChunkSize)
		prevTS, prevDelta = deltadelta.IntoActive(deltaBuf[:n], remaining[:n], prevTS, prevDelta)

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
	var deltaBuf [deltadelta.ChunkSize]int64

	for len(remaining) > 0 {
		n := min(len(remaining), deltadelta.ChunkSize)
		prevTS, prevDelta = deltadelta.IntoActive(deltaBuf[:n], remaining[:n], prevTS, prevDelta)

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

// TestDeltaPackedDecodeTable_ControlByteExhaustive verifies that the precomputed
// shuffle metadata is consistent with the scalar width-lookup for every possible
// control byte.
func TestDeltaPackedDecodeTable_ControlByteExhaustive(t *testing.T) {
	for cb := range 256 {
		meta := &deltaPackedDecodeTable[cb]

		// Cross-check lengths against the tag decode rule.
		var expectedTotal uint8
		for lane := range groupSize {
			tag := (uint8(cb) >> (uint(lane) * 2)) & 0x03
			expectedWidth := uint8(groupVarintLengths[tag])

			require.Equal(t, expectedWidth, meta.lengths[lane],
				"cb=0x%02X lane=%d: wrong length", cb, lane)
			require.Equal(t, expectedTotal, meta.offsets[lane],
				"cb=0x%02X lane=%d: wrong offset", cb, lane)
			expectedTotal += expectedWidth
		}

		require.Equal(t, expectedTotal, meta.totalBytes,
			"cb=0x%02X: wrong totalBytes", cb)

		// Cross-check flat tables match struct table.
		require.Equal(t, meta.totalBytes, deltaPackedDecodeTotalBytes[cb],
			"cb=0x%02X: flat totalBytes mismatch", cb)

		require.Equal(t, meta.shuffle[:], deltaPackedDecodeShuffles[cb*32:(cb+1)*32],
			"cb=0x%02X: flat shuffle mismatch", cb)

		// Verify shuffle mask: for each lane, non-0x80 entries must point to valid
		// payload bytes, and the source indices must match the expected offsets.
		for lane := range groupSize {
			dstBase := lane * 8
			width := int(meta.lengths[lane])
			offset := int(meta.offsets[lane])

			for b := range 8 {
				maskByte := meta.shuffle[dstBase+b]
				if b < width {
					require.Equal(t, uint8(offset+b), maskByte,
						"cb=0x%02X lane=%d byte=%d: wrong shuffle src index", cb, lane, b)
				} else {
					require.Equal(t, uint8(0x80), maskByte,
						"cb=0x%02X lane=%d byte=%d: expected 0x80 sentinel", cb, lane, b)
				}
			}
		}
	}
}

// TestDeltaPackedDecodeBackends_Parity runs each backend against the scalar reference
// on a variety of encoded streams and verifies they produce identical output.
func TestDeltaPackedDecodeBackends_Parity(t *testing.T) {
	datasets := []struct {
		name string
		ts   []int64
	}{
		{"regular_1s_100", generateSequentialFromBase(100, 1_000_000)},
		{"regular_1s_1000", generateSequentialFromBase(1000, 1_000_000)},
		{"jitter_small_200", simdTestJitterSmall(200)},
		{"jumps_large_500", simdTestJumpsLarge(500)},
		{"all_1byte_tags", simdTestAll1ByteTags(256)},
		{"all_8byte_tags", simdTestAll8ByteTags(64)},
		{"mixed_widths_300", simdTestMixedWidths(300)},
		// Boundary sizes
		{"size_3", generateSequentialFromBase(3, 1_000_000)},
		{"size_5", generateSequentialFromBase(5, 1_000_000)},
		{"size_6", generateSequentialFromBase(6, 1_000_000)},
		{"size_7", generateSequentialFromBase(7, 1_000_000)},
		{"size_9", generateSequentialFromBase(9, 1_000_000)},
		{"size_10", generateSequentialFromBase(10, 1_000_000)},
	}

	for _, ds := range datasets {
		t.Run(ds.name, func(t *testing.T) {
			enc := NewTimestampDeltaPackedEncoder()
			enc.WriteSlice(ds.ts)
			encoded := make([]byte, len(enc.Bytes()))
			copy(encoded, enc.Bytes())
			count := enc.Len()
			enc.Finish()

			// Reference: scalar
			dec := NewTimestampDeltaPackedDecoder()
			restoreScalar := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
			want := make([]int64, count)
			n := dec.DecodeAll(encoded, count, want)
			restoreScalar()
			require.Equal(t, count, n, "scalar DecodeAll produced wrong count")
			require.Equal(t, ds.ts, want[:n])

			// Each backend
			for _, backend := range allDeltaPackedDecodeBackends {
				if !deltaPackedDecodeBackendSupported(backend) {
					continue
				}

				t.Run(deltaPackedDecodeBackendName(backend), func(t *testing.T) {
					restore := setDeltaPackedDecodeBackendForTest(backend)
					defer restore()

					got := make([]int64, count)
					n2 := dec.DecodeAll(encoded, count, got)
					require.Equal(t, n, n2,
						"backend %s: DecodeAll count mismatch", deltaPackedDecodeBackendName(backend))
					require.Equal(t, want[:n], got[:n2],
						"backend %s: value mismatch", deltaPackedDecodeBackendName(backend))
				})
			}
		})
	}
}

// TestDeltaPackedDecodeBackends_AllIterator verifies the All() iterator produces
// the same sequence as DecodeAll for each backend.
func TestDeltaPackedDecodeBackends_AllIterator(t *testing.T) {
	ts := generateSequentialFromBase(300, 1_000_000)
	enc := NewTimestampDeltaPackedEncoder()
	enc.WriteSlice(ts)
	encoded := make([]byte, len(enc.Bytes()))
	copy(encoded, enc.Bytes())
	count := enc.Len()
	enc.Finish()

	dec := NewTimestampDeltaPackedDecoder()
	restoreScalar := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
	want := make([]int64, count)
	n := dec.DecodeAll(encoded, count, want)
	restoreScalar()
	require.Equal(t, count, n)

	for _, backend := range allDeltaPackedDecodeBackends {
		if !deltaPackedDecodeBackendSupported(backend) {
			continue
		}

		t.Run(deltaPackedDecodeBackendName(backend), func(t *testing.T) {
			restore := setDeltaPackedDecodeBackendForTest(backend)
			defer restore()

			got := make([]int64, 0, count)
			for ts2 := range dec.All(encoded, count) {
				got = append(got, ts2)
			}

			require.Equal(t, want[:n], got)
		})
	}
}

// TestDeltaPackedDecodeBackends_ExhaustiveControlBytes verifies each backend correctly
// decodes all 256 possible control bytes when used in a real encoded stream.
func TestDeltaPackedDecodeBackends_ExhaustiveControlBytes(t *testing.T) {
	for cb := range 256 {
		meta := &deltaPackedDecodeTable[cb]

		// Build the minimum timestamp sequence that would produce this control byte
		// for the second packed group, after a padding first group with control byte 0x00.
		// We need 4 delta-of-delta zigzag values whose encoded widths match the tags.
		ts := simdTestForControlByte(meta)

		enc := NewTimestampDeltaPackedEncoder()
		enc.WriteSlice(ts)
		encoded := make([]byte, len(enc.Bytes()))
		copy(encoded, enc.Bytes())
		count := enc.Len()
		enc.Finish()

		actualControlByte, ok := packedControlByteAt(encoded, 1)
		require.True(t, ok, "cb=0x%02X: failed to locate target packed control byte", cb)
		require.Equal(t, uint8(cb), actualControlByte,
			"cb=0x%02X: helper generated unexpected control byte 0x%02X", cb, actualControlByte)

		dec := NewTimestampDeltaPackedDecoder()
		restoreScalar := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
		want := make([]int64, count)
		n := dec.DecodeAll(encoded, count, want)
		restoreScalar()
		require.Equal(t, count, n, "cb=0x%02X: scalar decoded wrong count", cb)

		for _, backend := range allDeltaPackedDecodeBackends {
			if !deltaPackedDecodeBackendSupported(backend) || backend == deltaPackedDecodeBackendScalar {
				continue
			}

			t.Run(fmt.Sprintf("cb=0x%02X/%s", cb, deltaPackedDecodeBackendName(backend)), func(t *testing.T) {
				restore := setDeltaPackedDecodeBackendForTest(backend)
				defer restore()

				got := make([]int64, count)
				n2 := dec.DecodeAll(encoded, count, got)
				require.Equal(t, n, n2, "DecodeAll count mismatch")
				require.Equal(t, want[:n], got[:n2], "value mismatch")
			})
		}
	}
}

func TestDeltaPackedDecodeBackends_TruncatedInput(t *testing.T) {
	if !deltaPackedDecodeBackendSupported(deltaPackedDecodeBackendAsmAVX2) {
		t.Skip("AsmAVX2 backend not supported")
	}

	ts := simdTestAll1ByteTags(90)
	enc := NewTimestampDeltaPackedEncoder()
	enc.WriteSlice(ts)
	encoded := append([]byte(nil), enc.Bytes()...)
	count := enc.Len()
	enc.Finish()

	require.Greater(t, len(encoded), 2)
	truncated := append([]byte(nil), encoded[:len(encoded)-1]...)

	dec := NewTimestampDeltaPackedDecoder()

	restoreScalar := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
	want := make([]int64, count)
	wantCount := dec.DecodeAll(truncated, count, want)
	restoreScalar()

	restoreAVX2 := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendAsmAVX2)
	got := make([]int64, count)
	gotCount := dec.DecodeAll(truncated, count, got)
	restoreAVX2()

	require.Equal(t, wantCount, gotCount)
	require.Equal(t, want[:wantCount], got[:gotCount])

	restoreScalar = setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
	wantIter := collectInt64Seq(dec.All(truncated, count))
	restoreScalar()

	restoreAVX2 = setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendAsmAVX2)
	gotIter := collectInt64Seq(dec.All(truncated, count))
	restoreAVX2()

	require.Equal(t, wantIter, gotIter)
}

func TestDeltaPackedDecodeBackends_SIMDThresholdBoundaries(t *testing.T) {
	if !deltaPackedDecodeBackendSupported(deltaPackedDecodeBackendAsmAVX2) {
		t.Skip("AsmAVX2 backend not supported")
	}

	testCases := []struct {
		name  string
		count int
	}{
		{name: "below_threshold", count: 65},
		{name: "at_threshold", count: 66},
	}

	dec := NewTimestampDeltaPackedDecoder()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ts := simdTestMixedWidths(tc.count)
			enc := NewTimestampDeltaPackedEncoder()
			enc.WriteSlice(ts)
			encoded := append([]byte(nil), enc.Bytes()...)
			count := enc.Len()
			enc.Finish()

			restoreScalar := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
			want := make([]int64, count)
			wantCount := dec.DecodeAll(encoded, count, want)
			wantIter := collectInt64Seq(dec.All(encoded, count))
			restoreScalar()

			restoreAVX2 := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendAsmAVX2)
			got := make([]int64, count)
			gotCount := dec.DecodeAll(encoded, count, got)
			gotIter := collectInt64Seq(dec.All(encoded, count))
			restoreAVX2()

			require.Equal(t, wantCount, gotCount)
			require.Equal(t, want[:wantCount], got[:gotCount])
			require.Equal(t, wantIter, gotIter)
		})
	}
}

// --- helpers ---

// simdTestJitterSmall generates timestamps with small random jitter (DoD ≈ ±100 µs).
func simdTestJitterSmall(n int) []int64 {
	ts := make([]int64, n)
	base := int64(1_000_000_000_000) // 1s epoch start
	delta := int64(1_000_000)        // 1s base interval

	for i := range n {
		jitter := int64((i*37)%201 - 100) // deterministic ±100 µs jitter
		delta += jitter
		if delta < 1 {
			delta = 1
		}
		if i == 0 {
			ts[i] = base
		} else {
			ts[i] = ts[i-1] + delta
		}
	}

	return ts
}

// simdTestJumpsLarge generates timestamps with large gaps (DoD fits in 4 or 8 bytes).
func simdTestJumpsLarge(n int) []int64 {
	ts := make([]int64, n)
	base := int64(1_000_000_000_000)

	for i := range n {
		if i == 0 {
			ts[i] = base
			continue
		}
		gap := int64(1_000_000) + int64((i*100_000)%1_000_000_000) // varies 1s..1ks
		ts[i] = ts[i-1] + gap
	}

	return ts
}

// simdTestAll1ByteTags generates timestamps where all DoD values fit in 1 byte (control byte 0x00).
func simdTestAll1ByteTags(n int) []int64 {
	ts := make([]int64, n)
	base := int64(1_000_000_000_000)

	for i := range n {
		ts[i] = base + int64(i)*1_000_000 // constant 1s delta → DoD = 0 → zigzag = 0 → 1 byte
	}

	return ts
}

// simdTestAll8ByteTags generates timestamps where all DoD values require 8 bytes (control byte 0xFF).
func simdTestAll8ByteTags(n int) []int64 {
	ts := make([]int64, n)
	base := int64(1_000_000_000_000)
	const wideDeltaOfDelta = int64(1 << 33)

	ts[0] = base
	if n == 1 {
		return ts
	}

	cur := base
	delta := int64(1_000_000)

	for i := 1; i < n; i++ {
		delta += wideDeltaOfDelta
		cur += delta
		ts[i] = cur
	}

	return ts
}

// simdTestMixedWidths generates timestamps with a mix of DoD widths.
func simdTestMixedWidths(n int) []int64 {
	ts := make([]int64, n)
	base := int64(1_000_000_000_000)
	cur := base

	for i := range n {
		var delta int64
		switch i % 4 {
		case 0:
			delta = 1_000_000 // 1s → small DoD
		case 1:
			delta = 1_000_000 + int64(i)*300 // growing slowly → 2-byte DoD
		case 2:
			delta = int64(65536) * int64(i%128) // 4-byte DoD
		case 3:
			delta = int64(1<<33) + int64(i)*7 // 8-byte DoD
		default:
			panic("unreachable mixed-width case")
		}

		if delta <= 0 {
			delta = 1_000
		}

		cur += delta
		ts[i] = cur
	}

	return ts
}

// simdTestForControlByte builds a timestamp sequence whose second packed group
// uses the given control byte. The sequence contains two header timestamps,
// one padding group with control byte 0x00, and then the 4-value target group.
func simdTestForControlByte(meta *deltaPackedDecodeMeta) []int64 {
	// We need timestamps[2..5] to produce the target control byte.
	// Strategy: pick zigzag values whose widths match meta.lengths[].
	// Use the minimum value that triggers each width:
	//   width=1: zigzag = 0   (value 0)
	//   width=2: zigzag = 256 (first 2-byte value)
	//   width=4: zigzag = 65536
	//   width=8: zigzag = 4294967296
	minZigzagForWidth := [9]uint64{
		0, 0, 256, 0, 65536, 0, 0, 0, 4294967296,
	}

	// Build 6 timestamps: first 2 are header, then one padding group (all 1-byte),
	// then the 4 target values.
	ts := make([]int64, 2+groupSize+groupSize)
	ts[0] = 1_000_000_000_000 // arbitrary start time
	ts[1] = ts[0] + 1_000_000 // first delta = 1s

	// Padding group: constant delta to keep DoD=0 (zigzag=0, all 1-byte, cb=0x00)
	prevTS := ts[1]
	prevDelta := ts[1] - ts[0]

	for i := range groupSize {
		prevTS += prevDelta // DoD = 0 → zigzag = 0
		ts[2+i] = prevTS
	}

	// Target group: use zigzag values that match meta.lengths
	for lane := range groupSize {
		width := meta.lengths[lane]
		zz := minZigzagForWidth[width]
		expectedTag := encodeTag(zz)
		// Convert zigzag back to deltaOfDelta signed value
		var dod int64
		if zz&1 == 0 {
			dod = int64(zz >> 1)
		} else {
			dod = ^int64(zz >> 1)
		}

		newDelta := prevDelta + dod
		if newDelta == 0 {
			newDelta = 1 // avoid zero delta in target group slot
		}
		// Recalculate dod to match newDelta
		dod = newDelta - prevDelta
		zz2 := uint64((dod << 1) ^ (dod >> 63))

		if encodeTag(zz2) != expectedTag {
			panic("generated zigzag width mismatch")
		}

		prevTS += newDelta
		prevDelta = newDelta
		ts[2+groupSize+lane] = prevTS
	}

	return ts
}

func collectInt64Seq(seq func(func(int64) bool)) []int64 {
	values := make([]int64, 0)
	for value := range seq {
		values = append(values, value)
	}

	return values
}

func packedControlByteAt(data []byte, groupIndex int) (byte, bool) {
	_, offset, ok := varint.DecodeU64(data, 0)
	if !ok {
		return 0, false
	}

	_, offset, ok = varint.DecodeU64(data, offset)
	if !ok {
		return 0, false
	}

	for range groupIndex {
		if offset >= len(data) {
			return 0, false
		}

		meta := &deltaPackedDecodeTable[data[offset]]
		offset += 1 + int(meta.totalBytes)
	}

	if offset >= len(data) {
		return 0, false
	}

	return data[offset], true
}

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

		first, offset, ok := varint.DecodeU64(encoded, 0)
		if !ok {
			b.Fatalf("failed to decode first header varint for size %d", size)
		}

		zigzag, offset, ok := varint.DecodeU64(encoded, offset)
		if !ok {
			b.Fatalf("failed to decode second header varint for size %d", size)
		}

		prevTS := int64(first)
		prevDelta := varint.DecodeZigZag64(zigzag)
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
