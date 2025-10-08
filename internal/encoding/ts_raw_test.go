package encoding

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

// === TimestampRawEncoder Tests ===

func TestTimestampRawEncoder_NewEncoder(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)

	require.NotNil(t, encoder)
	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
	require.Empty(t, encoder.Bytes())
}

func TestTimestampRawEncoder_Write_SingleTimestamp(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamp := int64(1672531200000000) // 2023-01-01 00:00:00 UTC in microseconds

	encoder.Write(timestamp)

	require.Equal(t, 1, encoder.Len())
	require.Equal(t, 8, encoder.Size())
	require.Len(t, encoder.Bytes(), 8)

	// Verify decoding works
	decoder := NewTimestampRawDecoder(engine)
	decoded := make([]int64, 0, 1)
	for ts := range decoder.All(encoder.Bytes(), 1) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, 1)
	require.Equal(t, timestamp, decoded[0])
}

func TestTimestampRawEncoder_Write_MultipleTimestamps(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{
		1672531200000000, // 2023-01-01 00:00:00 UTC
		1672531201000000, // +1 second
		1672531202000000, // +1 second
		1672531205000000, // +3 seconds
	}

	for _, ts := range timestamps {
		encoder.Write(ts)
	}

	require.Equal(t, len(timestamps), encoder.Len())
	require.Equal(t, len(timestamps)*8, encoder.Size())

	// Verify decoding works
	decoder := NewTimestampRawDecoder(engine)
	decoded := make([]int64, 0, len(timestamps))
	for ts := range decoder.All(encoder.Bytes(), len(timestamps)) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, len(timestamps))
	for i, original := range timestamps {
		require.Equal(t, original, decoded[i])
	}
}

func TestTimestampRawEncoder_WriteSlice_EmptySlice(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	encoder.WriteSlice([]int64{})

	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
	require.Empty(t, encoder.Bytes())
}

func TestTimestampRawEncoder_WriteSlice_SingleTimestamp(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{1672531200000000}

	encoder.WriteSlice(timestamps)

	require.Equal(t, 1, encoder.Len())
	require.Equal(t, 8, encoder.Size())

	// Verify decoding
	decoder := NewTimestampRawDecoder(engine)
	decoded := make([]int64, 0, 1)
	for ts := range decoder.All(encoder.Bytes(), 1) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, 1)
	require.Equal(t, timestamps[0], decoded[0])
}

func TestTimestampRawEncoder_WriteSlice_MultipleTimestamps(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{
		1672531200000000, // Base timestamp
		1672531200100000, // +100ms
		1672531200150000, // +50ms
		1672531200300000, // +150ms
		1672531205000000, // +4.7s
	}

	encoder.WriteSlice(timestamps)

	require.Equal(t, len(timestamps), encoder.Len())
	require.Equal(t, len(timestamps)*8, encoder.Size())

	// Verify decoding
	decoder := NewTimestampRawDecoder(engine)
	decoded := make([]int64, 0, len(timestamps))
	for ts := range decoder.All(encoder.Bytes(), len(timestamps)) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, len(timestamps))
	for i, original := range timestamps {
		require.Equal(t, original, decoded[i])
	}
}

func TestTimestampRawEncoder_WriteSlice_NegativeTimestamps(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{
		-1000000000, // Negative timestamp
		0,           // Zero
		1000000000,  // Positive timestamp
	}

	encoder.WriteSlice(timestamps)

	require.Equal(t, len(timestamps), encoder.Len())

	// Verify decoding handles negative timestamps correctly
	decoder := NewTimestampRawDecoder(engine)
	decoded := make([]int64, 0, len(timestamps))
	for ts := range decoder.All(encoder.Bytes(), len(timestamps)) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, len(timestamps))
	for i, original := range timestamps {
		require.Equal(t, original, decoded[i])
	}
}

func TestTimestampRawEncoder_Reset(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{1672531200000000, 1672531201000000}

	// Write some data
	encoder.WriteSlice(timestamps)
	require.Equal(t, 2, encoder.Len())
	require.Equal(t, 16, encoder.Size())

	// Reset should keep buffer data
	encoder.Reset()
	require.Equal(t, 2, encoder.Len())   // Len unchanged after Reset
	require.Equal(t, 16, encoder.Size()) // Size unchanged after Reset
	require.NotEmpty(t, encoder.Bytes()) // Bytes unchanged after Reset
}

func TestTimestampRawEncoder_Finish(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{1672531200000000, 1672531201000000}

	// Write some data
	encoder.WriteSlice(timestamps)
	require.Equal(t, 2, encoder.Len())
	require.Greater(t, encoder.Size(), 0)

	// Get data BEFORE Finish
	data := encoder.Bytes()
	require.NotEmpty(t, data)

	// Finish should return buffer to pool and make encoder unusable
	encoder.Finish()
	require.Equal(t, 0, encoder.Len()) // Len() doesn't access buffer, so it's safe

	// Attempting to access buffer-dependent methods after Finish should panic
	require.Panics(t, func() { encoder.Size() })
	require.Panics(t, func() { encoder.Bytes() })
	require.Panics(t, func() { encoder.Write(1672531202000000) })
	require.Panics(t, func() { encoder.WriteSlice([]int64{1672531202000000}) })
}

func TestTimestampRawEncoder_MixedWriteAndWriteSlice(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)

	// Write single timestamp
	encoder.Write(1672531200000000)

	// Write slice
	encoder.WriteSlice([]int64{1672531201000000, 1672531202000000})

	// Write another single
	encoder.Write(1672531203000000)

	require.Equal(t, 4, encoder.Len())
	require.Equal(t, 32, encoder.Size())

	// Verify all timestamps
	decoder := NewTimestampRawDecoder(engine)
	decoded := make([]int64, 0, 4)
	for ts := range decoder.All(encoder.Bytes(), 4) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, 4)
	require.Equal(t, int64(1672531200000000), decoded[0])
	require.Equal(t, int64(1672531201000000), decoded[1])
	require.Equal(t, int64(1672531202000000), decoded[2])
	require.Equal(t, int64(1672531203000000), decoded[3])
}

func TestTimestampRawEncoder_EdgeCaseValues(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	testCases := []struct {
		name      string
		timestamp int64
	}{
		{"Zero", 0},
		{"Negative", -1672531200000000},
		{"Maximum int64", 9223372036854775807},
		{"Minimum int64", -9223372036854775808},
		{"Unix epoch", 0},
		{"Year 2000", 946684800000000},
		{"Year 2038 (32-bit limit)", 2147483647000000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewTimestampRawEncoder(engine)
			enc.Write(tc.timestamp)

			decoder := NewTimestampRawDecoder(engine)
			var decoded []int64
			for ts := range decoder.All(enc.Bytes(), 1) {
				decoded = append(decoded, ts)
			}

			require.Len(t, decoded, 1)
			require.Equal(t, tc.timestamp, decoded[0])
		})
	}
}

// === TimestampRawDecoder Tests ===

func TestTimestampRawDecoder_All_EmptyData(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTimestampRawDecoder(engine)

	decoded := make([]int64, 0)
	for ts := range decoder.All([]byte{}, 0) {
		decoded = append(decoded, ts)
	}

	require.Empty(t, decoded)
}

func TestTimestampRawDecoder_All_InvalidDataLength(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTimestampRawDecoder(engine)

	// Data length not multiple of 8
	invalidData := []byte{1, 2, 3, 4, 5}

	decoded := make([]int64, 0, 1)
	for ts := range decoder.All(invalidData, 1) {
		decoded = append(decoded, ts)
	}

	require.Empty(t, decoded)
}

func TestTimestampRawDecoder_All_EarlyTermination(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{
		1672531200000000,
		1672531201000000,
		1672531202000000,
		1672531203000000,
	}
	encoder.WriteSlice(timestamps)

	decoder := NewTimestampRawDecoder(engine)
	decoded := make([]int64, 0, len(timestamps))
	count := 0
	for ts := range decoder.All(encoder.Bytes(), len(timestamps)) {
		decoded = append(decoded, ts)
		count++
		if count >= 2 {
			break // Early termination
		}
	}

	require.Len(t, decoded, 2)
	require.Equal(t, timestamps[0], decoded[0])
	require.Equal(t, timestamps[1], decoded[1])
}

func TestTimestampRawDecoder_At_BasicAccess(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{
		1672531200000000,
		1672531201000000,
		1672531202000000,
		1672531203000000,
	}
	encoder.WriteSlice(timestamps)

	decoder := NewTimestampRawDecoder(engine)

	// Test each index
	for i, expected := range timestamps {
		ts, ok := decoder.At(encoder.Bytes(), i, len(timestamps))
		require.True(t, ok)
		require.Equal(t, expected, ts)
	}
}

func TestTimestampRawDecoder_At_InvalidIndices(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{1672531200000000, 1672531201000000}
	encoder.WriteSlice(timestamps)

	decoder := NewTimestampRawDecoder(engine)
	data := encoder.Bytes()

	testCases := []struct {
		name  string
		index int
	}{
		{"Negative index", -1},
		{"Index beyond data", 2},
		{"Large index", 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := decoder.At(data, tc.index, 2)
			require.False(t, ok)
		})
	}
}

func TestTimestampRawDecoder_At_EmptyData(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTimestampRawDecoder(engine)

	_, ok := decoder.At([]byte{}, 0, 0)
	require.False(t, ok)
}

// === TimestampRawUnsafeDecoder Tests ===

func TestTimestampRawUnsafeDecoder_All(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{
		1672531200000000,
		1672531201000000,
		1672531202000000,
	}
	encoder.WriteSlice(timestamps)

	decoder := NewTimestampRawUnsafeDecoder(engine)
	decoded := make([]int64, 0, len(timestamps))
	for ts := range decoder.All(encoder.Bytes(), len(timestamps)) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, len(timestamps))
	for i, original := range timestamps {
		require.Equal(t, original, decoded[i])
	}
}

func TestTimestampRawUnsafeDecoder_All_EmptyData(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTimestampRawUnsafeDecoder(engine)

	decoded := make([]int64, 0)
	for ts := range decoder.All([]byte{}, 0) {
		decoded = append(decoded, ts)
	}

	require.Empty(t, decoded)
}

func TestTimestampRawUnsafeDecoder_All_InvalidDataLength(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTimestampRawUnsafeDecoder(engine)

	// Data length not multiple of 8
	invalidData := []byte{1, 2, 3, 4, 5}

	decoded := make([]int64, 0, 1)
	for ts := range decoder.All(invalidData, 1) {
		decoded = append(decoded, ts)
	}

	require.Empty(t, decoded)
}

func TestTimestampRawUnsafeDecoder_At(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{
		1672531200000000,
		1672531201000000,
		1672531202000000,
	}
	encoder.WriteSlice(timestamps)

	decoder := NewTimestampRawUnsafeDecoder(engine)

	// Test each index
	for i, expected := range timestamps {
		ts, ok := decoder.At(encoder.Bytes(), i, len(timestamps))
		require.True(t, ok)
		require.Equal(t, expected, ts)
	}
}

func TestTimestampRawUnsafeDecoder_At_InvalidIndices(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{1672531200000000, 1672531201000000}
	encoder.WriteSlice(timestamps)

	decoder := NewTimestampRawUnsafeDecoder(engine)
	data := encoder.Bytes()

	testCases := []struct {
		name  string
		index int
	}{
		{"Negative index", -1},
		{"Index beyond data", 2},
		{"Large index", 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := decoder.At(data, tc.index, 2)
			require.False(t, ok)
		})
	}
}

// === Round-Trip Tests ===

func TestTimestampRaw_RoundTrip_LargeDataset(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTimestampRawEncoder(engine)

	// Generate 1000 timestamps
	now := time.Now().UnixMicro()
	timestamps := make([]int64, 1000)
	for i := range timestamps {
		timestamps[i] = now + int64(i)*1000000 // 1-second intervals
	}

	encoder.WriteSlice(timestamps)
	require.Equal(t, 1000, encoder.Len())
	require.Equal(t, 8000, encoder.Size())

	// Decode with safe decoder
	decoder := NewTimestampRawDecoder(engine)
	decoded := make([]int64, 0, len(timestamps))
	for ts := range decoder.All(encoder.Bytes(), len(timestamps)) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, len(timestamps))
	for i, original := range timestamps {
		require.Equal(t, original, decoded[i])
	}

	// Decode with unsafe decoder
	unsafeDecoder := NewTimestampRawUnsafeDecoder(engine)
	decodedUnsafe := make([]int64, 0, len(timestamps))
	for ts := range unsafeDecoder.All(encoder.Bytes(), len(timestamps)) {
		decodedUnsafe = append(decodedUnsafe, ts)
	}

	require.Len(t, decodedUnsafe, len(timestamps))
	for i, original := range timestamps {
		require.Equal(t, original, decodedUnsafe[i])
	}
}

func TestTimestampRaw_RoundTrip_BigEndian(t *testing.T) {
	engine := endian.GetBigEndianEngine()
	encoder := NewTimestampRawEncoder(engine)
	timestamps := []int64{
		1672531200000000,
		1672531201000000,
		1672531202000000,
	}

	encoder.WriteSlice(timestamps)

	// Decode with matching big-endian decoder
	decoder := NewTimestampRawDecoder(engine)
	decoded := make([]int64, 0, len(timestamps))
	for ts := range decoder.All(encoder.Bytes(), len(timestamps)) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, len(timestamps))
	for i, original := range timestamps {
		require.Equal(t, original, decoded[i])
	}
}

func TestTimestampRaw_EncodingSize(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Test predictable size: 8 bytes per timestamp
	for i := 1; i <= 100; i++ {
		enc := NewTimestampRawEncoder(engine)
		timestamps := make([]int64, i)
		for j := range timestamps {
			timestamps[j] = int64(j) * 1000000
		}
		enc.WriteSlice(timestamps)

		require.Equal(t, i, enc.Len())
		require.Equal(t, i*8, enc.Size())
	}
}
