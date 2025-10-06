package encoding

import (
	"math"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNumericGorillaEncoder_SingleValue(t *testing.T) {
	encoder := NewNumericGorillaEncoder()

	encoder.Write(42.0)

	require.Equal(t, 1, encoder.Len())
	require.Greater(t, encoder.Size(), 0)

	encoder.Finish()
	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
}

func TestNumericGorillaEncoder_UnchangedValues(t *testing.T) {
	encoder := NewNumericGorillaEncoder()

	// First value: 64 bits
	// Subsequent unchanged values: 1 bit each
	encoder.Write(100.0)
	encoder.Write(100.0)
	encoder.Write(100.0)
	encoder.Write(100.0)

	require.Equal(t, 4, encoder.Len())

	// Flush pending bits
	encoder.Finish()

	// First value takes 64 bits (8 bytes)
	// Next 3 values take 1 bit each (3 bits total)
	// Total: 64 + 3 = 67 bits = 9 bytes (rounded up)
	data := encoder.Bytes()
	require.LessOrEqual(t, len(data), 9, "Compressed size should be small for unchanged values")
}

func TestNumericGorillaEncoder_SimilarValues(t *testing.T) {
	encoder := NewNumericGorillaEncoder()

	// Similar values should compress well
	values := []float64{100.0, 100.1, 100.2, 100.3, 100.4}
	for _, v := range values {
		encoder.Write(v)
	}

	require.Equal(t, 5, encoder.Len())

	// Flush pending bits
	encoder.Finish()

	data := encoder.Bytes()
	// Compressed size should be much less than raw (5 * 8 = 40 bytes)
	require.Less(t, len(data), 40, "Similar values should compress well")
}

func TestNumericGorillaEncoder_WriteSlice(t *testing.T) {
	encoder := NewNumericGorillaEncoder()

	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	encoder.WriteSlice(values)

	require.Equal(t, 5, encoder.Len())

	encoder.Finish()
	require.Equal(t, 0, encoder.Len())
}

func TestNumericGorillaEncoder_EmptySlice(t *testing.T) {
	encoder := NewNumericGorillaEncoder()

	encoder.WriteSlice([]float64{})

	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
}

func TestNumericGorillaEncoder_Reset(t *testing.T) {
	encoder := NewNumericGorillaEncoder()

	encoder.Write(1.0)
	encoder.Write(2.0)

	initialSize := encoder.Size()
	require.Greater(t, initialSize, 0)

	encoder.Reset()

	// After reset, accumulated data is retained
	require.Equal(t, initialSize, encoder.Size())

	// Can write more values
	encoder.Write(3.0)
	encoder.Write(4.0)

	require.Greater(t, encoder.Size(), initialSize)
}

func TestNumericGorillaEncoder_SpecialValues(t *testing.T) {
	encoder := NewNumericGorillaEncoder()

	// Test special float64 values
	specialValues := []float64{
		0.0,
		math.Copysign(0, -1), // -0.0
		1.0,
		-1.0,
		math.MaxFloat64,
		math.SmallestNonzeroFloat64,
		math.Inf(1),
		math.Inf(-1),
		math.NaN(),
	}

	for _, v := range specialValues {
		encoder.Write(v)
	}

	require.Equal(t, len(specialValues), encoder.Len())
	encoder.Finish()
}

func TestNumericGorillaDecoder_SingleValue(t *testing.T) {
	encoder := NewNumericGorillaEncoder()
	encoder.Write(42.0)
	data := encoder.Bytes() // Get bytes before Finish()
	encoder.Finish()

	decoder := NewNumericGorillaDecoder()
	values := make([]float64, 0)
	for v := range decoder.All(data, 1) {
		values = append(values, v)
	}

	require.Equal(t, []float64{42.0}, values)
}

func TestNumericGorillaDecoder_UnchangedValues(t *testing.T) {
	encoder := NewNumericGorillaEncoder()
	expected := []float64{100.0, 100.0, 100.0, 100.0}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericGorillaDecoder()
	values := make([]float64, 0)
	for v := range decoder.All(data, len(expected)) {
		values = append(values, v)
	}

	require.Equal(t, expected, values)
}

func TestNumericGorillaDecoder_SimilarValues(t *testing.T) {
	encoder := NewNumericGorillaEncoder()
	expected := []float64{100.0, 100.1, 100.2, 100.3, 100.4}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericGorillaDecoder()
	values := make([]float64, 0)
	for v := range decoder.All(data, len(expected)) {
		values = append(values, v)
	}

	require.Equal(t, expected, values)
}

func TestNumericGorillaDecoder_VaryingValues(t *testing.T) {
	encoder := NewNumericGorillaEncoder()
	expected := []float64{1.0, 10.0, 100.0, 1000.0, 10000.0, 0.1, 0.01}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericGorillaDecoder()
	values := make([]float64, 0)
	for v := range decoder.All(data, len(expected)) {
		values = append(values, v)
	}

	require.Equal(t, expected, values)
}

func TestNumericGorillaDecoder_SpecialValues(t *testing.T) {
	encoder := NewNumericGorillaEncoder()
	expected := []float64{
		0.0,
		math.Copysign(0, -1), // -0.0
		1.0,
		-1.0,
		math.MaxFloat64,
		math.SmallestNonzeroFloat64,
		math.Inf(1),
		math.Inf(-1),
	}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericGorillaDecoder()
	values := make([]float64, 0)
	for v := range decoder.All(data, len(expected)) {
		values = append(values, v)
	}

	require.Equal(t, len(expected), len(values))
	for i := range expected {
		if math.IsInf(expected[i], 0) {
			require.True(t, math.IsInf(values[i], 0))
			require.Equal(t, math.Signbit(expected[i]), math.Signbit(values[i]))
		} else {
			require.Equal(t, expected[i], values[i])
		}
	}
}

func TestNumericGorillaDecoder_NaN(t *testing.T) {
	encoder := NewNumericGorillaEncoder()
	encoder.Write(1.0)
	encoder.Write(math.NaN())
	encoder.Write(2.0)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericGorillaDecoder()
	values := make([]float64, 0)
	for v := range decoder.All(data, 3) {
		values = append(values, v)
	}

	require.Equal(t, 3, len(values))
	require.Equal(t, 1.0, values[0])
	require.True(t, math.IsNaN(values[1]))
	require.Equal(t, 2.0, values[2])
}

func TestNumericGorillaDecoder_At(t *testing.T) {
	encoder := NewNumericGorillaEncoder()
	expected := make([]float64, 300)
	for i := range expected {
		expected[i] = float64(i + 1)
	}

	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericGorillaDecoder()

	// Test each valid index
	for i, expectedVal := range expected {
		val, ok := decoder.At(data, i, len(expected))
		require.True(t, ok, "Should successfully decode at index %d", i)
		require.Equal(t, expectedVal, val, "Value at index %d", i)
	}

	// Test negative index
	val, ok := decoder.At(data, -1, len(expected))
	require.False(t, ok, "Should return false for negative index")
	require.Zero(t, val)

	// Test out-of-bounds positive index
	val, ok = decoder.At(data, len(expected), len(expected))
	require.False(t, ok, "Should return false for index == count")
	require.Zero(t, val)

	val, ok = decoder.At(data, len(expected)+1, len(expected))
	require.False(t, ok, "Should return false for index > count")
	require.Zero(t, val)
}

func TestNumericGorillaDecoder_At_FirstValue(t *testing.T) {
	encoder := NewNumericGorillaEncoder()
	encoder.Write(42.0)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericGorillaDecoder()
	val, ok := decoder.At(data, 0, 1)

	require.True(t, ok)
	require.Equal(t, 42.0, val)
}

func TestNumericGorillaDecoder_EmptyData(t *testing.T) {
	decoder := NewNumericGorillaDecoder()

	values := make([]float64, 0)
	for v := range decoder.All([]byte{}, 0) {
		values = append(values, v)
	}
	require.Empty(t, values)

	val, ok := decoder.At([]byte{}, 0, 0)
	require.False(t, ok)
	require.Zero(t, val)
}

func TestNumericGorillaDecoder_InsufficientData(t *testing.T) {
	encoder := NewNumericGorillaEncoder()
	encoder.Write(1.0)
	encoder.Write(2.0)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericGorillaDecoder()

	// Request more values than encoded
	values := make([]float64, 0)
	for v := range decoder.All(data, 10) {
		values = append(values, v)
	}

	// Should only get the values that were encoded
	require.LessOrEqual(t, len(values), 2)
}

func TestNumericGorillaRoundTrip_LargeDataset(t *testing.T) {
	encoder := NewNumericGorillaEncoder()

	// Generate a realistic time-series dataset
	expected := make([]float64, 1000)
	base := 100.0
	for i := range expected {
		// Simulate slowly changing time-series data
		expected[i] = base + float64(i)*0.1 + math.Sin(float64(i)*0.1)*5.0
	}

	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	// Check compression ratio
	rawSize := len(expected) * 8
	compressedSize := len(data)
	compressionRatio := float64(rawSize) / float64(compressedSize)
	t.Logf("Compression ratio: %.2fx (raw: %d bytes, compressed: %d bytes)",
		compressionRatio, rawSize, compressedSize)

	// Decode and verify
	decoder := NewNumericGorillaDecoder()
	values := make([]float64, 0, len(expected))
	for v := range decoder.All(data, len(expected)) {
		values = append(values, v)
	}

	require.Equal(t, len(expected), len(values))
	for i := range expected {
		require.Equal(t, expected[i], values[i], "Mismatch at index %d", i)
	}
}

func TestNumericGorillaRoundTrip_RandomAccess(t *testing.T) {
	encoder := NewNumericGorillaEncoder()
	expected := []float64{10.0, 20.0, 30.0, 40.0, 50.0, 60.0, 70.0, 80.0, 90.0, 100.0}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericGorillaDecoder()

	// Test random access at various indices
	testIndices := []int{0, 3, 5, 7, 9}
	for _, idx := range testIndices {
		val, ok := decoder.At(data, idx, len(expected))
		require.True(t, ok, "Should decode at index %d", idx)
		require.Equal(t, expected[idx], val, "Value at index %d", idx)
	}
}

func TestNumericGorillaEncoder_MultipleResets(t *testing.T) {
	encoder := NewNumericGorillaEncoder()

	// First batch
	encoder.Write(1.0)
	encoder.Write(2.0)
	size1 := encoder.Size()

	encoder.Reset()

	// Second batch
	encoder.Write(3.0)
	encoder.Write(4.0)
	size2 := encoder.Size()

	// Size should have grown
	require.Greater(t, size2, size1)

	encoder.Finish()
}

func TestNumericGorillaBitOperations(t *testing.T) {
	encoder := NewNumericGorillaEncoder()

	// Test values that will exercise different bit patterns
	values := []float64{
		1.0,                                      // Simple value
		1.0,                                      // Unchanged (1 bit)
		2.0,                                      // Changed (XOR compression)
		2.0,                                      // Unchanged again
		1.5,                                      // Similar value
		100.0,                                    // Large change
		100.000001,                               // Tiny change (tests leading zeros > 31)
		math.Float64frombits(0x3FF0000000000001), // Specific bit pattern
	}

	encoder.WriteSlice(values)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericGorillaDecoder()
	decoded := make([]float64, 0, len(values))
	for v := range decoder.All(data, len(values)) {
		decoded = append(decoded, v)
	}

	require.Equal(t, len(values), len(decoded))
	for i := range values {
		require.Equal(t, values[i], decoded[i], "Mismatch at index %d", i)
	}
}

func TestBitReader_ReadBit(t *testing.T) {
	// Create test data: 10101010 11001100 (0xAACC)
	data := []byte{0xAA, 0xCC}
	br := newBitReader(data)

	expected := []uint64{1, 0, 1, 0, 1, 0, 1, 0, 1, 1, 0, 0, 1, 1, 0, 0}
	for i, exp := range expected {
		bit, ok := br.readBit()
		require.True(t, ok, "Should read bit %d", i)
		require.Equal(t, exp, bit, "Bit %d should be %d", i, exp)
	}
}

func TestBitReader_ReadBits(t *testing.T) {
	// Create test data
	data := []byte{0xFF, 0x00, 0xAA}
	br := newBitReader(data)

	// Read 8 bits (should be 0xFF)
	val, ok := br.readBits(8)
	require.True(t, ok)
	require.Equal(t, uint64(0xFF), val)

	// Read 8 bits (should be 0x00)
	val, ok = br.readBits(8)
	require.True(t, ok)
	require.Equal(t, uint64(0x00), val)

	// Read 4 bits (should be 0xA from 0xAA)
	val, ok = br.readBits(4)
	require.True(t, ok)
	require.Equal(t, uint64(0xA), val)
}

func TestBitReader_ReadBeyondEnd(t *testing.T) {
	data := []byte{0xFF}
	br := newBitReader(data)

	// Read all 8 bits
	_, ok := br.readBits(8)
	require.True(t, ok)

	// Try to read more
	_, ok = br.readBit()
	require.False(t, ok, "Should return false when reading beyond end")
}

func TestNumericGorillaEncoder_CompressionQuality(t *testing.T) {
	testCases := []struct {
		name               string
		values             []float64
		maxCompressionSize int // Maximum expected size in bytes
	}{
		{
			name:               "Constant values",
			values:             []float64{100.0, 100.0, 100.0, 100.0, 100.0, 100.0, 100.0, 100.0, 100.0, 100.0},
			maxCompressionSize: 10, // First value (8 bytes) + 9 bits for unchanged values
		},
		{
			name: "Slowly increasing",
			values: func() []float64 {
				vals := make([]float64, 50)
				for i := range vals {
					vals[i] = 100.0 + float64(i)*0.01
				}

				return vals
			}(),
			maxCompressionSize: 320, // Should provide modest compression (actual: ~308 bytes)
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoder := NewNumericGorillaEncoder()
			encoder.WriteSlice(tc.values)
			data := encoder.Bytes()
			encoder.Finish()

			rawSize := len(tc.values) * 8
			compressedSize := len(data)
			compressionRatio := float64(rawSize) / float64(compressedSize)

			t.Logf("Raw: %d bytes, Compressed: %d bytes, Ratio: %.2fx",
				rawSize, compressedSize, compressionRatio)

			require.LessOrEqual(t, compressedSize, tc.maxCompressionSize,
				"Compressed size should be <= %d bytes for %s", tc.maxCompressionSize, tc.name)
		})
	}
}

// TestNumericGorillaDecoder_At_JitteredData tests the At() method with realistic jittered data
// that simulates the benchmark scenario (2% delta, 100-400 values).
// This test reproduces the bug where trailing calculation can become negative.
func TestNumericGorillaDecoder_At_JitteredData(t *testing.T) {
	// Generate jittered test data similar to benchmark
	testCases := []struct {
		name         string
		numValues    int
		baseValue    float64
		deltaPercent float64
		randomSeed   int64
	}{
		{
			name:         "100_values_5pct_jitter",
			numValues:    100,
			baseValue:    100.0,
			deltaPercent: 0.05,
			randomSeed:   42,
		},
		{
			name:         "200_values_2pct_jitter",
			numValues:    200,
			baseValue:    100.0,
			deltaPercent: 0.02,
			randomSeed:   42,
		},
		{
			name:         "400_values_2pct_jitter",
			numValues:    400,
			baseValue:    100.0,
			deltaPercent: 0.02,
			randomSeed:   42,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Generate jittered values
			values := generateJitteredValues(tc.numValues, tc.baseValue, tc.deltaPercent, tc.randomSeed)

			// Encode
			encoder := NewNumericGorillaEncoder()
			for _, val := range values {
				encoder.Write(val)
			}
			data := encoder.Bytes()
			encoder.Finish()
			t.Logf("Encoded %d values into %d bytes", len(values), len(data))

			// Test random access with decoder
			decoder := NewNumericGorillaDecoder()
			rng := rand.New(rand.NewSource(tc.randomSeed))

			// Test random indices (including out of bounds to simulate benchmark)
			numTests := 100
			for i := 0; i < numTests; i++ {
				// Random index from 0 to numValues+100 (to include out of bounds)
				index := rng.Intn(tc.numValues + 100)

				val, ok := decoder.At(data, index, len(values))

				if index >= len(values) {
					// Out of bounds - should return false
					require.False(t, ok, "Index %d should be out of bounds for %d values", index, len(values))
				} else {
					// Valid index - should succeed
					if !ok {
						t.Errorf("At() failed for index %d (total %d values)", index, len(values))
						t.Errorf("Expected value: %.6f", values[index])

						// Debug: try to decode sequentially to see where it fails
						debugSequentialDecode(t, data, len(values), index)

						t.FailNow()
					}

					expectedValue := values[index]
					require.Equal(t, expectedValue, val,
						"At(index=%d) returned wrong value. Got %.6f, expected %.6f",
						index, val, expectedValue)
				}
			}

			// Also test sequential access to all indices
			t.Run("sequential_access", func(t *testing.T) {
				for i := 0; i < len(values); i++ {
					val, ok := decoder.At(data, i, len(values))
					if !ok {
						t.Errorf("At(%d) failed for sequential access", i)
						debugSequentialDecode(t, data, len(values), i)
						t.FailNow()
					}
					require.Equal(t, values[i], val, "At(%d) returned wrong value", i)
				}
			})
		})
	}
}

// TestNumericGorillaDecoder_At_NegativeTrailing specifically tests for the negative trailing bug
func TestNumericGorillaDecoder_At_NegativeTrailing(t *testing.T) {
	// This test specifically tries to reproduce the case where:
	// trailing = 64 - leading - blockSize becomes negative

	// Generate data that's likely to trigger the issue
	encoder := NewNumericGorillaEncoder()

	// Use realistic values with small deltas that cause edge cases in XOR compression
	baseValue := 100.0
	values := make([]float64, 400)
	rng := rand.New(rand.NewSource(42))

	currentValue := baseValue
	for i := 0; i < len(values); i++ {
		deltaRange := currentValue * 0.02
		delta := (rng.Float64()*2 - 1) * deltaRange
		currentValue += delta
		values[i] = currentValue
		encoder.Write(currentValue)
	}

	data := encoder.Bytes()
	decoder := NewNumericGorillaDecoder()

	t.Logf("Testing %d values, encoded into %d bytes", len(values), len(data))

	// Test every single index
	for i := 0; i < len(values); i++ {
		val, ok := decoder.At(data, i, len(values))
		if !ok {
			t.Errorf("At(%d) failed - likely hit negative trailing bug", i)

			// Try to get more debug info
			debugDecodeWithLogging(t, data, len(values), i)

			t.FailNow()
		}

		if val != values[i] {
			t.Errorf("At(%d) = %.6f, expected %.6f", i, val, values[i])
		}
	}

	// Also verify using All() iterator
	t.Run("verify_with_All", func(t *testing.T) {
		i := 0
		for val := range decoder.All(data, len(values)) {
			require.Equal(t, values[i], val, "All() at index %d returned wrong value", i)
			i++
		}
		require.Equal(t, len(values), i, "All() did not yield all values")
	})
}

// generateJitteredValues generates float64 values with random deltas (simulating real metrics)
func generateJitteredValues(count int, baseValue float64, deltaPercent float64, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	values := make([]float64, count)

	currentValue := baseValue
	for i := 0; i < count; i++ {
		// Add small random delta
		deltaRange := currentValue * deltaPercent
		delta := (rng.Float64()*2 - 1) * deltaRange // -delta% to +delta%
		currentValue += delta
		values[i] = currentValue
	}

	return values
}

// debugSequentialDecode helps debug At() failures by decoding sequentially
func debugSequentialDecode(t *testing.T, data []byte, count int, failedIndex int) {
	t.Helper()

	br := newBitReader(data)

	// Read first value
	firstBits, ok := br.readBits(64)
	if !ok {
		t.Logf("DEBUG: Failed to read first value")
		return
	}

	t.Logf("DEBUG: First value decoded successfully")

	prevValue := firstBits
	var prevLeading, prevTrailing int

	// Decode up to failed index
	for i := 1; i <= failedIndex && i < count; i++ {
		controlBit, ok := br.readBit()
		if !ok {
			t.Logf("DEBUG: Failed to read control bit at i=%d", i)
			return
		}

		if controlBit == 0 {
			t.Logf("DEBUG: i=%d, value unchanged", i)
			continue
		}

		blockControlBit, ok := br.readBit()
		if !ok {
			t.Logf("DEBUG: Failed to read block control bit at i=%d", i)
			return
		}

		var leading, blockSize int
		if blockControlBit == 0 {
			leading = prevLeading
			blockSize = 64 - prevLeading - prevTrailing
			t.Logf("DEBUG: i=%d, reuse block: leading=%d, blockSize=%d, trailing=%d",
				i, leading, blockSize, prevTrailing)
		} else {
			leading, ok = br.read5Bits()
			if !ok {
				t.Logf("DEBUG: Failed to read leading bits at i=%d", i)
				return
			}

			blockSize, ok = br.read6Bits()
			if !ok {
				t.Logf("DEBUG: Failed to read block size at i=%d", i)
				return
			}
			blockSize++

			trailing := 64 - leading - blockSize
			t.Logf("DEBUG: i=%d, new block: leading=%d, blockSize=%d, trailing=%d",
				i, leading, blockSize, trailing)

			if trailing < 0 {
				t.Errorf("DEBUG: FOUND BUG! i=%d has negative trailing: leading=%d, blockSize=%d, trailing=%d",
					i, leading, blockSize, trailing)
				return
			}

			prevLeading = leading
			prevTrailing = trailing
		}

		meaningfulBits, ok := br.readBits(blockSize)
		if !ok {
			t.Logf("DEBUG: Failed to read meaningful bits at i=%d", i)
			return
		}

		trailing := 64 - leading - blockSize
		if trailing < 0 {
			t.Errorf("DEBUG: FOUND BUG! i=%d: calculated trailing=%d (leading=%d, blockSize=%d)",
				i, trailing, leading, blockSize)
		}

		xor := meaningfulBits << trailing
		prevValue ^= xor
	}
}

// debugDecodeWithLogging decodes with detailed logging to find the bug
func debugDecodeWithLogging(t *testing.T, data []byte, _ int, targetIndex int) {
	t.Helper()

	t.Logf("=== Debugging decode up to index %d ===", targetIndex)

	br := newBitReader(data)

	// Read first value
	firstBits, ok := br.readBits(64)
	if !ok {
		t.Logf("Failed to read first value")
		return
	}
	t.Logf("Index 0: first value read successfully")

	if targetIndex == 0 {
		return
	}

	prevValue := firstBits
	var prevLeading, prevTrailing int

	for i := 1; i <= targetIndex; i++ {
		controlBit, ok := br.readBit()
		if !ok {
			t.Logf("Index %d: failed to read control bit", i)
			return
		}

		if controlBit == 0 {
			t.Logf("Index %d: value unchanged", i)
			if i == targetIndex {
				return
			}

			continue
		}

		blockControlBit, ok := br.readBit()
		if !ok {
			t.Logf("Index %d: failed to read block control bit", i)
			return
		}

		var leading, blockSize int
		if blockControlBit == 0 {
			leading = prevLeading
			blockSize = 64 - prevLeading - prevTrailing
			t.Logf("Index %d: reuse block (leading=%d, blockSize=%d, trailing=%d)",
				i, leading, blockSize, prevTrailing)
		} else {
			leading, ok = br.read5Bits()
			if !ok {
				t.Logf("Index %d: failed to read leading (5 bits)", i)
				return
			}

			blockSize, ok = br.read6Bits()
			if !ok {
				t.Logf("Index %d: failed to read blockSize (6 bits)", i)
				return
			}
			blockSize++ // Decode from 0-63 to 1-64

			t.Logf("Index %d: new block (leading=%d, blockSize=%d)", i, leading, blockSize)

			// Calculate trailing BEFORE updating prevTrailing
			trailing := 64 - leading - blockSize
			t.Logf("Index %d: calculated trailing = 64 - %d - %d = %d", i, leading, blockSize, trailing)

			if trailing < 0 || trailing > 64 {
				t.Errorf("Index %d: INVALID trailing=%d (leading=%d, blockSize=%d)",
					i, trailing, leading, blockSize)
				t.Errorf("This causes the bug!")

				return
			}

			prevLeading = leading
			prevTrailing = trailing
		}

		meaningfulBits, ok := br.readBits(blockSize)
		if !ok {
			t.Logf("Index %d: failed to read meaningful bits (count=%d)", i, blockSize)
			return
		}

		trailing := 64 - leading - blockSize
		xor := meaningfulBits << trailing
		prevValue ^= xor

		if i == targetIndex {
			t.Logf("Index %d: successfully decoded", i)
			return
		}
	}
}
