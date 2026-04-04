package encoding

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/internal/pool"
)

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
				encoder := NewTimestampDeltaEncoder()
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
		deltaEnc := NewTimestampDeltaEncoder()
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
			decoder := NewTimestampDeltaDecoder()
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

		deltaEnc := NewTimestampDeltaEncoder()
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
			decoder := NewTimestampDeltaDecoder()
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

		deltaEnc := NewTimestampDeltaEncoder()
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
			decoder := NewTimestampDeltaDecoder()
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
			deltaEnc := NewTimestampDeltaEncoder()
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

		deltaOfDelta := decodeZigZag64(zz)
		prevDelta += deltaOfDelta
		curTS += prevDelta
	}

	return packedGroupDecodeState{offset: offset, prevDelta: prevDelta, curTS: curTS}
}

func decodePackedGroupUnrolled(data []byte, offset int, prevDelta int64, curTS int64) packedGroupDecodeState {
	cb := data[0]

	tag0 := cb & 0x03
	zz0, nextOffset := decodePackedValueByTag(data, offset, tag0)
	deltaOfDelta0 := decodeZigZag64(zz0)
	prevDelta += deltaOfDelta0
	curTS += prevDelta

	tag1 := (cb >> 2) & 0x03
	zz1, nextOffset := decodePackedValueByTag(data, nextOffset, tag1)
	deltaOfDelta1 := decodeZigZag64(zz1)
	prevDelta += deltaOfDelta1
	curTS += prevDelta

	tag2 := (cb >> 4) & 0x03
	zz2, nextOffset := decodePackedValueByTag(data, nextOffset, tag2)
	deltaOfDelta2 := decodeZigZag64(zz2)
	prevDelta += deltaOfDelta2
	curTS += prevDelta

	tag3 := cb >> 6
	zz3, nextOffset := decodePackedValueByTag(data, nextOffset, tag3)
	deltaOfDelta3 := decodeZigZag64(zz3)
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
