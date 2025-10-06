package encoding

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

// === TimestampDeltaEncoder Tests ===

func TestTimestampDeltaEncoder_NewEncoder(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()

	require.NotNil(t, encoder)
	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
	require.Empty(t, encoder.Bytes())
}

func TestTimestampDeltaEncoder_Write_SingleTimestamp(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
	timestamp := int64(1672531200000000) // 2023-01-01 00:00:00 UTC in microseconds

	encoder.Write(timestamp)

	require.Equal(t, 1, encoder.Len())
	require.Greater(t, encoder.Size(), 0)
	require.NotEmpty(t, encoder.Bytes())

	// Verify decoding works
	decoder := NewTimestampDeltaDecoder()

	decoded := make([]int64, 0, 1)
	for ts := range decoder.All(encoder.Bytes(), 1) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, 1)
	require.Equal(t, timestamp, decoded[0])
}

func TestTimestampDeltaEncoder_Write_MultipleTimestamps(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
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
	require.Greater(t, encoder.Size(), 0)

	// Verify decoding works
	decoder := NewTimestampDeltaDecoder()
	decoded := make([]int64, 0, len(timestamps))
	for ts := range decoder.All(encoder.Bytes(), len(timestamps)) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, len(timestamps))
	for i, original := range timestamps {
		require.Equal(t, original, decoded[i])
	}
}

func TestTimestampDeltaEncoder_WriteSlice_EmptySlice(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice([]int64{})

	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
	require.Empty(t, encoder.Bytes())
}

func TestTimestampDeltaEncoder_WriteSlice_SingleTimestamp(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
	timestamps := []int64{1672531200000000}

	encoder.WriteSlice(timestamps)

	require.Equal(t, 1, encoder.Len())
	require.Greater(t, encoder.Size(), 0)

	// Verify decoding
	decoder := NewTimestampDeltaDecoder()
	decoded := make([]int64, 0, 1)
	for ts := range decoder.All(encoder.Bytes(), 1) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, 1)
	require.Equal(t, timestamps[0], decoded[0])
}

func TestTimestampDeltaEncoder_WriteSlice_MultipleTimestamps(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
	timestamps := []int64{
		1672531200000000, // Base timestamp
		1672531200100000, // +100ms
		1672531200150000, // +50ms
		1672531200300000, // +150ms
		1672531205000000, // +4.7s (large delta)
	}

	encoder.WriteSlice(timestamps)

	require.Equal(t, len(timestamps), encoder.Len())
	require.Greater(t, encoder.Size(), 0)

	// Verify decoding
	decoder := NewTimestampDeltaDecoder()
	decoded := make([]int64, 0, len(timestamps))
	for ts := range decoder.All(encoder.Bytes(), len(timestamps)) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, len(timestamps))
	for i, original := range timestamps {
		require.Equal(t, original, decoded[i])
	}
}

func TestTimestampDeltaEncoder_WriteSlice_NegativeDeltas(t *testing.T) {
	now := time.Now().UnixMicro()
	encoder := NewTimestampDeltaEncoder()
	timestamps := []int64{
		now,
		now + 1000000,  // +1 second
		now - 5000000,  // -5 seconds (negative delta)
		now + 10000000, // +10 seconds
		now - 2000000,  // -2 seconds (negative delta)
	}

	encoder.WriteSlice(timestamps)

	require.Equal(t, len(timestamps), encoder.Len())

	// Verify decoding handles negative deltas correctly
	decoder := NewTimestampDeltaDecoder()
	decoded := make([]int64, 0, len(timestamps))
	for ts := range decoder.All(encoder.Bytes(), len(timestamps)) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, len(timestamps))
	for i, original := range timestamps {
		require.Equal(t, original, decoded[i])
	}
}

func TestTimestampDeltaEncoder_WriteSlice_LargeDeltas(t *testing.T) {
	now := time.Now().UnixMicro()
	encoder := NewTimestampDeltaEncoder()
	timestamps := []int64{
		now,
		now + 86400000000,  // +1 day
		now + 172800000000, // +2 days
		now - 86400000000,  // -1 day (large negative delta)
		now + 604800000000, // +1 week
	}

	encoder.WriteSlice(timestamps)

	require.Equal(t, len(timestamps), encoder.Len())

	// Verify decoding handles large deltas
	decoder := NewTimestampDeltaDecoder()
	decoded := make([]int64, 0, len(timestamps))
	for ts := range decoder.All(encoder.Bytes(), len(timestamps)) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, len(timestamps))
	for i, original := range timestamps {
		require.Equal(t, original, decoded[i])
	}
}

func TestTimestampDeltaEncoder_Reset(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
	timestamps := []int64{1672531200000000, 1672531201000000}

	// Write some data
	encoder.WriteSlice(timestamps)
	require.Equal(t, 2, encoder.Len())
	require.Greater(t, encoder.Size(), 0)

	// Reset should clear state but not buffer
	encoder.Reset()
	require.Equal(t, 2, encoder.Len())    // Len unchanged after Reset
	require.Greater(t, encoder.Size(), 0) // Size unchanged after Reset
	require.NotEmpty(t, encoder.Bytes())  // Bytes unchanged after Reset

	// Write new data should start fresh delta chain
	newTimestamps := []int64{1672531300000000, 1672531301000000} // Different base time
	encoder.WriteSlice(newTimestamps)
	require.Equal(t, 4, encoder.Len()) // 2 + 2 timestamps

	// The data should contain both sequences
	data := encoder.Bytes()
	require.Greater(t, len(data), 0)
}

func TestTimestampDeltaEncoder_Finish(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
	timestamps := []int64{1672531200000000, 1672531201000000}

	// Write some data
	encoder.WriteSlice(timestamps)
	require.Equal(t, 2, encoder.Len())
	require.Greater(t, encoder.Size(), 0)

	// Finish should clear everything
	encoder.Finish()
	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
	require.Empty(t, encoder.Bytes())

	// Should be able to write new data after finish
	newTimestamps := []int64{1672531300000000}
	encoder.WriteSlice(newTimestamps)
	require.Equal(t, 1, encoder.Len())
	require.Greater(t, encoder.Size(), 0)
}

func TestTimestampDeltaEncoder_MixedWriteAndWriteSlice(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()

	// Start with WriteSlice
	slice1 := []int64{1672531200000000, 1672531201000000}
	encoder.WriteSlice(slice1)

	// Add individual timestamps
	encoder.Write(1672531202000000)
	encoder.Write(1672531203000000)

	// Add another slice
	slice2 := []int64{1672531204000000, 1672531205000000}
	encoder.WriteSlice(slice2)

	totalExpected := len(slice1) + 2 + len(slice2)
	require.Equal(t, totalExpected, encoder.Len())

	// Verify all timestamps decode correctly in sequence
	decoder := NewTimestampDeltaDecoder()
	decoded := make([]int64, 0, totalExpected)
	for ts := range decoder.All(encoder.Bytes(), totalExpected) {
		decoded = append(decoded, ts)
	}

	require.Len(t, decoded, totalExpected)

	// Verify the sequence is correct
	expectedSequence := []int64{
		1672531200000000, 1672531201000000, // slice1
		1672531202000000, 1672531203000000, // individual writes
		1672531204000000, 1672531205000000, // slice2
	}

	for i, expected := range expectedSequence {
		require.Equal(t, expected, decoded[i])
	}
}

func TestTimestampDeltaEncoder_CompressionEfficiency(t *testing.T) {
	// Test that delta encoding is more efficient than raw encoding for sequential data
	encoder := NewTimestampDeltaEncoder()

	// Generate 100 timestamps with 1-second intervals (highly compressible)
	start := time.Now().UnixMicro()
	timestamps := make([]int64, 100)
	for i := range timestamps {
		timestamps[i] = start + int64(i)*1000000 // 1-second intervals
	}

	encoder.WriteSlice(timestamps)

	deltaSize := encoder.Size()
	rawSize := len(timestamps) * 8 // 8 bytes per int64

	require.Less(t, deltaSize, rawSize, "Delta encoding should be more efficient than raw for sequential data")

	// For 1-second intervals, compression should be significant
	compressionRatio := float64(deltaSize) / float64(rawSize)
	require.Less(t, compressionRatio, 0.5, "Expected at least 50% compression for regular intervals")
}

// === TimestampDeltaDecoder Tests ===

func TestTimestampDeltaDecoder_All(t *testing.T) {
	// Test with a sequence of timestamps
	originalTimestamps := []int64{
		1672531200000000, // 2023-01-01 00:00:00 UTC in microseconds
		1672531200100000, // +100ms
		1672531200150000, // +50ms
		1672531200300000, // +150ms
		1672531205000000, // +4.7s (large delta)
	}

	// Encode the timestamps
	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(originalTimestamps)
	encodedData := encoder.Bytes()

	// Decode using the new decoder
	decoder := NewTimestampDeltaDecoder()
	decodedTimestamps := make([]int64, 0, len(originalTimestamps))
	for timestamp := range decoder.All(encodedData, len(originalTimestamps)) {
		decodedTimestamps = append(decodedTimestamps, timestamp)
	}

	// Verify the decoded timestamps match the originals
	require.Len(t, decodedTimestamps, len(originalTimestamps))
	for i, original := range originalTimestamps {
		require.Equal(t, original, decodedTimestamps[i], "Timestamp mismatch at index %d", i)
	}
}

func TestTimestampDeltaDecoder_EmptyData(t *testing.T) {
	decoder := NewTimestampDeltaDecoder()
	timestamps := make([]int64, 0)
	for timestamp := range decoder.All([]byte{}, 0) {
		timestamps = append(timestamps, timestamp)
	}
	require.Empty(t, timestamps)
}

func TestTimestampDeltaDecoder_EarlyTermination(t *testing.T) {
	// Test early termination by breaking after first timestamp
	originalTimestamps := []int64{
		1672531200000000,
		1672531200100000,
		1672531200150000,
	}

	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(originalTimestamps)
	encodedData := encoder.Bytes()

	decoder := NewTimestampDeltaDecoder()
	decodedTimestamps := make([]int64, 0, len(originalTimestamps))
	count := 0
	for timestamp := range decoder.All(encodedData, len(originalTimestamps)) {
		decodedTimestamps = append(decodedTimestamps, timestamp)
		count++
		if count == 1 { // Break after first timestamp
			break
		}
	}

	require.Len(t, decodedTimestamps, 1)
	require.Equal(t, originalTimestamps[0], decodedTimestamps[0])
}

func TestTimestampDeltaDecoder_LargeDeltas(t *testing.T) {
	// Test with large positive and negative deltas
	now := time.Now().UnixMicro()
	originalTimestamps := []int64{
		now,
		now + 86400000000,  // +1 day
		now - 3600000000,   // -1 hour (negative delta)
		now + 604800000000, // +1 week
	}

	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(originalTimestamps)
	encodedData := encoder.Bytes()

	decoder := NewTimestampDeltaDecoder()
	decodedTimestamps := make([]int64, 0, len(originalTimestamps))
	for timestamp := range decoder.All(encodedData, len(originalTimestamps)) {
		decodedTimestamps = append(decodedTimestamps, timestamp)
	}

	require.Len(t, decodedTimestamps, len(originalTimestamps))
	for i, original := range originalTimestamps {
		require.Equal(t, original, decodedTimestamps[i], "Timestamp mismatch at index %d", i)
	}
}

func TestTimestampDeltaDecoder_InvalidData(t *testing.T) {
	decoder := NewTimestampDeltaDecoder()

	// Test with invalid varint data
	invalidData := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF} // Invalid varint

	timestamps := make([]int64, 0, 5)
	for timestamp := range decoder.All(invalidData, 5) {
		timestamps = append(timestamps, timestamp)
	}

	// Should handle invalid data gracefully (may decode some valid data or none)
	require.True(t, len(timestamps) <= 5, "Should not decode more than expected count")
}

func TestTimestampDeltaDecoder_CountMismatch(t *testing.T) {
	// Test when actual data has fewer timestamps than expected count
	originalTimestamps := []int64{1672531200000000, 1672531201000000}

	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(originalTimestamps)
	encodedData := encoder.Bytes()

	decoder := NewTimestampDeltaDecoder()

	// Request more timestamps than available
	timestamps := make([]int64, 0, 10)
	for timestamp := range decoder.All(encodedData, 10) { // Expecting 10, but only 2 available
		timestamps = append(timestamps, timestamp)
	}

	// Should only return available timestamps
	require.Len(t, timestamps, len(originalTimestamps))
}

// === Round-trip Tests ===

func TestTimestampDeltaRoundTrip_EdgeCases(t *testing.T) {
	testCases := []struct {
		name       string
		timestamps []int64
	}{
		{
			name:       "Single timestamp",
			timestamps: []int64{1672531200000000},
		},
		{
			name:       "Zero timestamp",
			timestamps: []int64{0},
		},
		{
			name:       "Negative timestamp",
			timestamps: []int64{-1000000},
		},
		{
			name:       "Maximum int64",
			timestamps: []int64{9223372036854775807},
		},
		{
			name:       "Minimum int64",
			timestamps: []int64{-9223372036854775808},
		},
		{
			name:       "Identical timestamps",
			timestamps: []int64{1672531200000000, 1672531200000000, 1672531200000000},
		},
		{
			name:       "Decreasing sequence",
			timestamps: []int64{1672531205000000, 1672531204000000, 1672531203000000},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoder := NewTimestampDeltaEncoder()
			encoder.WriteSlice(tc.timestamps)
			encoded := encoder.Bytes()

			decoder := NewTimestampDeltaDecoder()
			var decoded []int64
			for ts := range decoder.All(encoded, len(tc.timestamps)) {
				decoded = append(decoded, ts)
			}

			require.Len(t, decoded, len(tc.timestamps))
			for i, original := range tc.timestamps {
				require.Equal(t, original, decoded[i])
			}
		})
	}
}

// === TimestampDeltaDecoder At Method Tests ===

func TestTimestampDeltaDecoder_At_BasicAccess(t *testing.T) {
	// Test basic random access functionality
	originalTimestamps := []int64{
		1672531200000000, // Index 0: 2023-01-01 00:00:00 UTC
		1672531201000000, // Index 1: +1 second
		1672531202000000, // Index 2: +1 second
		1672531205000000, // Index 3: +3 seconds
		1672531210000000, // Index 4: +5 seconds
	}

	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(originalTimestamps)
	encodedData := encoder.Bytes()

	decoder := NewTimestampDeltaDecoder()

	// Test accessing each index
	for i, expectedTs := range originalTimestamps {
		timestamp, ok := decoder.At(encodedData, i, len(originalTimestamps))
		require.True(t, ok, "Should find timestamp at index %d", i)
		require.Equal(t, expectedTs, timestamp, "Timestamp mismatch at index %d", i)
	}
}

func TestTimestampDeltaDecoder_At_InvalidIndices(t *testing.T) {
	originalTimestamps := []int64{
		1672531200000000,
		1672531201000000,
		1672531202000000,
	}

	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(originalTimestamps)
	encodedData := encoder.Bytes()

	decoder := NewTimestampDeltaDecoder()

	testCases := []struct {
		name  string
		index int
	}{
		{"Negative index", -1},
		{"Negative large index", -100},
		{"Beyond end index", 3},
		{"Large beyond end index", 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			timestamp, ok := decoder.At(encodedData, tc.index, 3)
			require.False(t, ok, "Should not find timestamp at invalid index %d", tc.index)
			require.Equal(t, int64(0), timestamp, "Should return zero time for invalid index %d", tc.index)
		})
	}
}

func TestTimestampDeltaDecoder_At_EmptyData(t *testing.T) {
	decoder := NewTimestampDeltaDecoder()

	testCases := []struct {
		name  string
		data  []byte
		index int
	}{
		{"Empty slice", []byte{}, 0},
		{"Empty slice negative index", []byte{}, -1},
		{"Nil slice", nil, 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			timestamp, ok := decoder.At(tc.data, tc.index, 0)
			require.False(t, ok, "Should not find timestamp in empty data")
			require.Equal(t, int64(0), timestamp, "Should return zero time for empty data")
		})
	}
}

func TestTimestampDeltaDecoder_At_SingleTimestamp(t *testing.T) {
	timestamp := int64(1672531200000000)

	encoder := NewTimestampDeltaEncoder()
	encoder.Write(timestamp)
	encodedData := encoder.Bytes()

	decoder := NewTimestampDeltaDecoder()

	// Test accessing index 0 (should succeed)
	ts, ok := decoder.At(encodedData, 0, 1)
	require.True(t, ok, "Should find timestamp at index 0")
	require.Equal(t, timestamp, ts)

	// Test accessing index 1 (should fail)
	ts, ok = decoder.At(encodedData, 1, 1)
	require.False(t, ok, "Should not find timestamp at index 1")
	require.Equal(t, int64(0), ts)
}

func TestTimestampDeltaDecoder_At_WithNegativeDeltas(t *testing.T) {
	now := time.Now().UnixMicro()
	originalTimestamps := []int64{
		now,           // Index 0: base time
		now + 1000000, // Index 1: +1 second
		now - 2000000, // Index 2: -2 seconds (negative delta)
		now + 5000000, // Index 3: +5 seconds
		now - 1000000, // Index 4: -1 second (negative delta)
	}

	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(originalTimestamps)
	encodedData := encoder.Bytes()

	decoder := NewTimestampDeltaDecoder()

	// Test accessing each index with negative deltas
	for i, expectedTs := range originalTimestamps {
		timestamp, ok := decoder.At(encodedData, i, len(originalTimestamps))
		require.True(t, ok, "Should find timestamp at index %d", i)
		require.Equal(t, expectedTs, timestamp, "Timestamp mismatch at index %d", i)
	}
}

func TestTimestampDeltaDecoder_At_WithLargeDeltas(t *testing.T) {
	now := time.Now().UnixMicro()
	originalTimestamps := []int64{
		now,
		now + 86400000000,  // +1 day
		now - 3600000000,   // -1 hour (large negative delta)
		now + 604800000000, // +1 week
	}

	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(originalTimestamps)
	encodedData := encoder.Bytes()

	decoder := NewTimestampDeltaDecoder()

	// Test accessing each index with large deltas
	for i, expectedTs := range originalTimestamps {
		timestamp, ok := decoder.At(encodedData, i, len(originalTimestamps))
		require.True(t, ok, "Should find timestamp at index %d", i)
		require.Equal(t, expectedTs, timestamp, "Timestamp mismatch at index %d", i)
	}
}

func TestTimestampDeltaDecoder_At_InvalidData(t *testing.T) {
	decoder := NewTimestampDeltaDecoder()

	testCases := []struct {
		name string
		data []byte
	}{
		{"Invalid varint", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
		{"Truncated data", []byte{0x80}},      // Incomplete varint
		{"Partial delta", []byte{0x01, 0x80}}, // Valid first timestamp, incomplete delta
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			timestamp, ok := decoder.At(tc.data, 0, 1)
			// For completely invalid data, should return false
			// For partially valid data, might return the first timestamp
			if !ok {
				require.Equal(t, int64(0), timestamp, "Should return zero time for invalid data")
			}

			// Accessing beyond first timestamp should fail for all invalid data cases
			timestamp2, ok2 := decoder.At(tc.data, 1, 2)
			require.False(t, ok2, "Should not find second timestamp in invalid data")
			require.Equal(t, int64(0), timestamp2, "Should return zero time for invalid data")
		})
	}
}

func TestTimestampDeltaDecoder_At_EdgeCaseValues(t *testing.T) {
	testCases := []struct {
		name       string
		timestamps []int64
	}{
		{
			name:       "Zero timestamp",
			timestamps: []int64{0},
		},
		{
			name:       "Maximum int64",
			timestamps: []int64{9223372036854775807},
		},
		{
			name:       "Minimum int64",
			timestamps: []int64{-9223372036854775808},
		},
		{
			name:       "Identical timestamps",
			timestamps: []int64{1672531200000000, 1672531200000000, 1672531200000000},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoder := NewTimestampDeltaEncoder()
			encoder.WriteSlice(tc.timestamps)
			encodedData := encoder.Bytes()

			decoder := NewTimestampDeltaDecoder()

			for i, expectedTs := range tc.timestamps {
				timestamp, ok := decoder.At(encodedData, i, len(tc.timestamps))
				require.True(t, ok, "Should find timestamp at index %d", i)
				require.Equal(t, expectedTs, timestamp, "Timestamp mismatch at index %d", i)
			}
		})
	}
}

func TestTimestampDeltaDecoder_At_ComparisonWithAll(t *testing.T) {
	// Test that At() returns the same results as All() for various scenarios
	originalTimestamps := []int64{
		1672531200000000,
		1672531200100000,
		1672531200150000,
		1672531200300000,
		1672531205000000,
	}

	encoder := NewTimestampDeltaEncoder()
	encoder.WriteSlice(originalTimestamps)
	encodedData := encoder.Bytes()

	decoder := NewTimestampDeltaDecoder()

	// Get all timestamps using All() method
	allTimestamps := make([]int64, 0, len(originalTimestamps))
	for ts := range decoder.All(encodedData, len(originalTimestamps)) {
		allTimestamps = append(allTimestamps, ts)
	}

	// Compare with At() method results
	require.Len(t, allTimestamps, len(originalTimestamps))

	for i, expectedFromAll := range allTimestamps {
		timestampFromAt, ok := decoder.At(encodedData, i, len(originalTimestamps))
		require.True(t, ok, "At() should find timestamp at index %d", i)
		require.Equal(t, expectedFromAll, timestampFromAt, "At() and All() should return same timestamp at index %d", i)
	}
}

// === Encoder Reuse Tests ===
// These tests verify that encoders can be safely reused after Finish()

// TestTimestampDeltaEncoder_ReuseAfterFinish verifies encoder can be reused after Finish()
func TestTimestampDeltaEncoder_ReuseAfterFinish(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
	decoder := NewTimestampDeltaDecoder()

	// First encoding session
	firstTimestamps := []int64{1000000, 2000000, 3000000}
	encoder.WriteSlice(firstTimestamps)
	require.Equal(t, 3, encoder.Len())
	firstData := make([]byte, len(encoder.Bytes()))
	copy(firstData, encoder.Bytes())

	// Verify first encoding
	decoded := make([]int64, 0, 3)
	for ts := range decoder.All(firstData, 3) {
		decoded = append(decoded, ts)
	}
	require.Equal(t, firstTimestamps, decoded)

	// Finish to prepare for reuse
	encoder.Finish()
	require.Equal(t, 0, encoder.Len())
	require.Empty(t, encoder.Bytes())

	// Second encoding session - should work correctly
	secondTimestamps := []int64{4000000, 5000000, 6000000, 7000000}
	encoder.WriteSlice(secondTimestamps)
	require.Equal(t, 4, encoder.Len())
	secondData := encoder.Bytes()

	// Verify second encoding is independent and correct
	decoded = decoded[:0]
	for ts := range decoder.All(secondData, 4) {
		decoded = append(decoded, ts)
	}
	require.Equal(t, secondTimestamps, decoded)

	// Third session - verify continued reuse
	encoder.Finish()
	thirdTimestamps := []int64{8000000}
	encoder.Write(thirdTimestamps[0])
	require.Equal(t, 1, encoder.Len())
	thirdData := encoder.Bytes()

	// Verify third encoding
	decoded = decoded[:0]
	for ts := range decoder.All(thirdData, 1) {
		decoded = append(decoded, ts)
	}
	require.Equal(t, thirdTimestamps, decoded)
}

// TestTimestampDeltaEncoder_MultipleReuseCycles tests many consecutive reuse cycles
func TestTimestampDeltaEncoder_MultipleReuseCycles(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
	decoder := NewTimestampDeltaDecoder()

	const numCycles = 10

	for cycle := range numCycles {
		// Encode timestamps with sequential deltas
		base := int64(cycle) * 1000000
		timestamps := []int64{base, base + 100000}
		encoder.WriteSlice(timestamps)

		// Verify encoding
		require.Equal(t, 2, encoder.Len())
		data := encoder.Bytes()
		require.NotEmpty(t, data)

		decoded := make([]int64, 0, 2)
		for ts := range decoder.All(data, 2) {
			decoded = append(decoded, ts)
		}
		require.Equal(t, timestamps, decoded, "Cycle %d failed", cycle)

		// Prepare for next cycle
		encoder.Finish()
		require.Equal(t, 0, encoder.Len())
		require.Empty(t, encoder.Bytes())
	}
}

// TestTimestampDeltaEncoder_ReuseWithMixedOperations tests reuse with Write and WriteSlice
func TestTimestampDeltaEncoder_ReuseWithMixedOperations(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
	decoder := NewTimestampDeltaDecoder()

	// First session: WriteSlice
	encoder.WriteSlice([]int64{1000000, 2000000})
	require.Equal(t, 2, encoder.Len())
	encoder.Finish()

	// Second session: Write
	encoder.Write(3000000)
	encoder.Write(4000000)
	require.Equal(t, 2, encoder.Len())
	data := encoder.Bytes()

	decoded := make([]int64, 0, 2)
	for ts := range decoder.All(data, 2) {
		decoded = append(decoded, ts)
	}
	require.Equal(t, []int64{3000000, 4000000}, decoded)
	encoder.Finish()

	// Third session: Mixed
	encoder.Write(5000000)
	encoder.WriteSlice([]int64{6000000, 7000000})
	require.Equal(t, 3, encoder.Len())
	data = encoder.Bytes()

	decoded = decoded[:0]
	for ts := range decoder.All(data, 3) {
		decoded = append(decoded, ts)
	}
	require.Equal(t, []int64{5000000, 6000000, 7000000}, decoded)
}

// TestTimestampDeltaEncoder_ReuseStateReset verifies state is properly reset after Finish()
func TestTimestampDeltaEncoder_ReuseStateReset(t *testing.T) {
	encoder := NewTimestampDeltaEncoder()
	decoder := NewTimestampDeltaDecoder()

	// First session with specific timestamp sequence
	encoder.Write(1000000)
	encoder.Write(1001000) // Delta: 1000
	encoder.Write(1002000) // Delta: 1000
	firstData := make([]byte, len(encoder.Bytes()))
	copy(firstData, encoder.Bytes())

	// Verify first encoding uses deltas
	decoded := make([]int64, 0, 3)
	for ts := range decoder.All(firstData, 3) {
		decoded = append(decoded, ts)
	}
	require.Equal(t, []int64{1000000, 1001000, 1002000}, decoded)

	// Finish and start new session with different base
	encoder.Finish()

	// Second session should NOT use previous timestamp as reference
	encoder.Write(5000000) // Should encode as absolute, not delta from 1002000
	encoder.Write(5001000) // Should be delta from 5000000
	secondData := encoder.Bytes()

	// Verify second encoding starts fresh
	decoded = decoded[:0]
	for ts := range decoder.All(secondData, 2) {
		decoded = append(decoded, ts)
	}
	require.Equal(t, []int64{5000000, 5001000}, decoded)
}

// === Size Efficiency Tests ===

// TestTimestampEncodingSize compares the size efficiency of delta-of-delta vs raw encoding
// across different data patterns and sizes.
//
// This test measures actual bytes used, not performance, to demonstrate space savings
// of the delta-of-delta encoding implementation.
func TestTimestampEncodingSize(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	testCases := []struct {
		name     string
		numTS    int
		interval int64 // microseconds
		jitter   float64
	}{
		{"10pts_1s_regular", 10, 1_000_000, 0.0},
		{"10pts_1s_jitter5pct", 10, 1_000_000, 0.05},
		{"100pts_1s_regular", 100, 1_000_000, 0.0},
		{"100pts_1s_jitter5pct", 100, 1_000_000, 0.05},
		{"100pts_1min_regular", 100, 60_000_000, 0.0},
		{"250pts_1s_regular", 250, 1_000_000, 0.0},
		{"250pts_1s_jitter5pct", 250, 1_000_000, 0.05},
		{"1000pts_1s_regular", 1000, 1_000_000, 0.0},
		{"1000pts_1s_jitter5pct", 1000, 1_000_000, 0.05},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Generate timestamps
			timestamps := generateTimestampsWithJitter(tc.numTS, tc.interval, tc.jitter)

			// Encode with delta-of-delta
			deltaEncoder := NewTimestampDeltaEncoder()
			deltaEncoder.WriteSlice(timestamps)
			deltaSize := deltaEncoder.Size()

			// Encode with raw
			rawEncoder := NewTimestampRawEncoder(engine)
			rawEncoder.WriteSlice(timestamps)
			rawSize := rawEncoder.Size()

			// Calculate metrics
			rawExpected := tc.numTS * 8
			deltaBytes := float64(deltaSize) / float64(tc.numTS)
			rawBytes := float64(rawSize) / float64(tc.numTS)
			savingsVsRaw := (1.0 - float64(deltaSize)/float64(rawSize)) * 100
			compressionRatio := float64(deltaSize) / float64(rawSize)

			t.Logf("Results for %s:", tc.name)
			t.Logf("  Raw encoding:          %6d bytes (%.2f bytes/ts)", rawSize, rawBytes)
			t.Logf("  Delta-of-delta:        %6d bytes (%.2f bytes/ts)", deltaSize, deltaBytes)
			t.Logf("  Space savings:         %.1f%%", savingsVsRaw)
			t.Logf("  Compression ratio:     %.3f", compressionRatio)

			// Verify raw encoding size
			if rawSize != rawExpected {
				t.Errorf("Raw size mismatch: got %d, expected %d", rawSize, rawExpected)
			}

			// Verify delta-of-delta achieves compression
			if tc.jitter == 0.0 && deltaSize >= rawSize {
				t.Errorf("Delta-of-delta should compress regular intervals, but got %d >= %d bytes", deltaSize, rawSize)
			}
		})
	}
}

// TestTimestampEncodingSizeIrregular tests delta-of-delta encoding with irregular intervals
func TestTimestampEncodingSizeIrregular(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	testCases := []struct {
		name  string
		numTS int
	}{
		{"100pts_irregular", 100},
		{"250pts_irregular", 250},
		{"1000pts_irregular", 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Generate irregular timestamps
			timestamps := generateIrregularTimestamps(tc.numTS)

			// Encode with delta-of-delta
			deltaEncoder := NewTimestampDeltaEncoder()
			deltaEncoder.WriteSlice(timestamps)
			deltaSize := deltaEncoder.Size()

			// Encode with raw
			rawEncoder := NewTimestampRawEncoder(engine)
			rawEncoder.WriteSlice(timestamps)
			rawSize := rawEncoder.Size()

			// Calculate metrics
			deltaBytes := float64(deltaSize) / float64(tc.numTS)
			rawBytes := float64(rawSize) / float64(tc.numTS)
			savingsVsRaw := (1.0 - float64(deltaSize)/float64(rawSize)) * 100
			compressionRatio := float64(deltaSize) / float64(rawSize)

			t.Logf("Results for %s:", tc.name)
			t.Logf("  Raw encoding:          %6d bytes (%.2f bytes/ts)", rawSize, rawBytes)
			t.Logf("  Delta-of-delta:        %6d bytes (%.2f bytes/ts)", deltaSize, deltaBytes)
			t.Logf("  Space savings:         %.1f%%", savingsVsRaw)
			t.Logf("  Compression ratio:     %.3f", compressionRatio)

			// For irregular data, delta-of-delta should still provide some compression
			// but not as much as regular intervals
			if deltaSize > rawSize {
				t.Logf("  Note: Delta-of-delta is larger than raw for irregular data (expected for some patterns)")
			}
		})
	}
}

// TestTimestampEncodingSizeComparison generates a comprehensive comparison table
func TestTimestampEncodingSizeComparison(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	scenarios := []struct {
		name        string
		description string
		generator   func(int) []int64
	}{
		{
			"Regular_1s",
			"1-second intervals (perfect regularity)",
			func(n int) []int64 { return generateTimestampsWithJitter(n, 1_000_000, 0.0) },
		},
		{
			"Regular_1min",
			"1-minute intervals (perfect regularity)",
			func(n int) []int64 { return generateTimestampsWithJitter(n, 60_000_000, 0.0) },
		},
		{
			"Jitter_5pct",
			"1-second intervals with ±5% jitter",
			func(n int) []int64 { return generateTimestampsWithJitter(n, 1_000_000, 0.05) },
		},
		{
			"Jitter_10pct",
			"1-second intervals with ±10% jitter",
			func(n int) []int64 { return generateTimestampsWithJitter(n, 1_000_000, 0.10) },
		},
		{
			"Irregular",
			"Completely irregular intervals",
			generateIrregularTimestamps,
		},
		{
			"Accelerating",
			"Accelerating intervals (increasing deltas)",
			generateAcceleratingTimestamps,
		},
	}

	sizes := []int{100, 250, 1000}

	t.Log("\n╔═══════════════════════════════════════════════════════════════════════════════╗")
	t.Log("║           Delta-of-Delta vs Raw Encoding Size Comparison                     ║")
	t.Log("╚═══════════════════════════════════════════════════════════════════════════════╝")

	for _, size := range sizes {
		t.Logf("\n━━━ %d Timestamps ━━━", size)
		t.Log("")
		t.Log("┌──────────────────────┬────────────┬────────────┬──────────┬──────────┬─────────────┐")
		t.Log("│ Scenario             │ Raw (bytes)│ Delta (B)  │ Δ B/ts   │ Raw B/ts │ Savings (%) │")
		t.Log("├──────────────────────┼────────────┼────────────┼──────────┼──────────┼─────────────┤")

		for _, scenario := range scenarios {
			timestamps := scenario.generator(size)

			// Raw encoding
			rawEncoder := NewTimestampRawEncoder(engine)
			rawEncoder.WriteSlice(timestamps)
			rawSize := rawEncoder.Size()

			// Delta-of-delta encoding
			deltaEncoder := NewTimestampDeltaEncoder()
			deltaEncoder.WriteSlice(timestamps)
			deltaSize := deltaEncoder.Size()

			// Metrics
			deltaBytes := float64(deltaSize) / float64(size)
			rawBytes := float64(rawSize) / float64(size)
			savings := (1.0 - float64(deltaSize)/float64(rawSize)) * 100

			t.Logf("│ %-20s │ %10d │ %10d │ %8.2f │ %8.2f │ %10.1f%% │",
				scenario.name, rawSize, deltaSize, deltaBytes, rawBytes, savings)
		}

		t.Log("└──────────────────────┴────────────┴────────────┴──────────┴──────────┴─────────────┘")
	}

	t.Log("")
	t.Log("Legend:")
	t.Log("  Raw:     Fixed 8 bytes per timestamp (baseline)")
	t.Log("  Delta:   Delta-of-delta + zigzag + varint encoding")
	t.Log("  Δ B/ts:  Bytes per timestamp for delta-of-delta")
	t.Log("  Savings: Space saved compared to raw encoding")
}
