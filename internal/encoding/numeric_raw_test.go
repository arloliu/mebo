package encoding

import (
	"math"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

// === NumericRawEncoder Tests ===

func TestNumericRawEncoder_NewEncoder(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)

	require.NotNil(t, encoder)
	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
	require.Empty(t, encoder.Bytes())
}

func TestNumericRawEncoder_Write_SingleValue(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	value := 3.14159

	encoder.Write(value)

	require.Equal(t, 1, encoder.Len())
	require.Equal(t, 8, encoder.Size())
	require.Len(t, encoder.Bytes(), 8)

	// Verify decoding works
	decoder := NewNumericRawDecoder(engine)
	decoded := make([]float64, 0, 1)
	for val := range decoder.All(encoder.Bytes(), 1) {
		decoded = append(decoded, val)
	}

	require.Len(t, decoded, 1)
	require.Equal(t, value, decoded[0])
}

func TestNumericRawEncoder_Write_MultipleValues(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{3.14159, 2.71828, 1.41421, 1.73205}

	for _, val := range values {
		encoder.Write(val)
	}

	require.Equal(t, len(values), encoder.Len())
	require.Equal(t, len(values)*8, encoder.Size())

	// Verify decoding works
	decoder := NewNumericRawDecoder(engine)
	decoded := make([]float64, 0, len(values))
	for val := range decoder.All(encoder.Bytes(), len(values)) {
		decoded = append(decoded, val)
	}

	require.Len(t, decoded, len(values))
	for i, original := range values {
		require.Equal(t, original, decoded[i])
	}
}

func TestNumericRawEncoder_WriteSlice_EmptySlice(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	encoder.WriteSlice([]float64{})

	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
	require.Empty(t, encoder.Bytes())
}

func TestNumericRawEncoder_WriteSlice_SingleValue(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{3.14159}

	encoder.WriteSlice(values)

	require.Equal(t, 1, encoder.Len())
	require.Equal(t, 8, encoder.Size())

	// Verify decoding
	decoder := NewNumericRawDecoder(engine)
	decoded := make([]float64, 0, 1)
	for val := range decoder.All(encoder.Bytes(), 1) {
		decoded = append(decoded, val)
	}

	require.Len(t, decoded, 1)
	require.Equal(t, values[0], decoded[0])
}

func TestNumericRawEncoder_PoolReuse(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Test that Finish returns buffer to pool and subsequent New gets from pool
	encoder1 := NewNumericRawEncoder(engine)
	encoder1.WriteSlice([]float64{1.0, 2.0, 3.0})
	require.Equal(t, 3, encoder1.Len())

	// Finish should return buffer to pool
	encoder1.Finish()
	require.Equal(t, 0, encoder1.Len())

	// New encoder should potentially reuse buffer from pool
	encoder2 := NewNumericRawEncoder(engine)
	encoder2.WriteSlice([]float64{4.0, 5.0, 6.0})
	require.Equal(t, 3, encoder2.Len())
	require.Equal(t, 24, encoder2.Size())

	// Verify values are correct
	decoder := NewNumericRawDecoder(engine)
	decoded := make([]float64, 0, 3)
	for val := range decoder.All(encoder2.Bytes(), 3) {
		decoded = append(decoded, val)
	}
	require.Equal(t, []float64{4.0, 5.0, 6.0}, decoded)

	encoder2.Finish()
}

func TestNumericRawEncoder_ZeroAllocations(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}

	// After initial pool Get (1 alloc), subsequent operations should be zero-alloc
	encoder.WriteSlice(values)

	// Measure allocations for subsequent operations
	allocsBefore := testing.AllocsPerRun(1000, func() {
		encoder.Reset()
		encoder.WriteSlice(values)
		_ = encoder.Bytes()
	})

	require.Equal(t, float64(0), allocsBefore, "WriteSlice should have zero allocations after initial buffer")

	encoder.Finish()
}

func TestNumericRawEncoder_WriteSlice_MultipleValues(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{
		3.14159,
		2.71828,
		1.41421,
		1.73205,
		0.57721,
	}

	encoder.WriteSlice(values)

	require.Equal(t, len(values), encoder.Len())
	require.Equal(t, len(values)*8, encoder.Size())

	// Verify decoding
	decoder := NewNumericRawDecoder(engine)
	decoded := make([]float64, 0, len(values))
	for val := range decoder.All(encoder.Bytes(), len(values)) {
		decoded = append(decoded, val)
	}

	require.Len(t, decoded, len(values))
	for i, original := range values {
		require.Equal(t, original, decoded[i])
	}
}

func TestNumericRawEncoder_Reset(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{3.14159, 2.71828}

	// Write some data
	encoder.WriteSlice(values)
	require.Equal(t, 2, encoder.Len())
	require.Equal(t, 16, encoder.Size())

	// Reset should keep buffer data
	encoder.Reset()
	require.Equal(t, 2, encoder.Len())   // Len unchanged after Reset
	require.Equal(t, 16, encoder.Size()) // Size unchanged after Reset
	require.NotEmpty(t, encoder.Bytes()) // Bytes unchanged after Reset
}

func TestNumericRawEncoder_Finish(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{3.14159, 2.71828}

	// Write some data
	encoder.WriteSlice(values)
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
	require.Panics(t, func() { encoder.Write(1.0) })
	require.Panics(t, func() { encoder.WriteSlice([]float64{1.0}) })
}

func TestNumericRawEncoder_MixedWriteAndWriteSlice(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)

	// Write single value
	encoder.Write(3.14159)

	// Write slice
	encoder.WriteSlice([]float64{2.71828, 1.41421})

	// Write another single
	encoder.Write(1.73205)

	require.Equal(t, 4, encoder.Len())
	require.Equal(t, 32, encoder.Size())

	// Verify all values
	decoder := NewNumericRawDecoder(engine)
	decoded := make([]float64, 0, 4)
	for val := range decoder.All(encoder.Bytes(), 4) {
		decoded = append(decoded, val)
	}

	require.Len(t, decoded, 4)
	require.Equal(t, 3.14159, decoded[0])
	require.Equal(t, 2.71828, decoded[1])
	require.Equal(t, 1.41421, decoded[2])
	require.Equal(t, 1.73205, decoded[3])
}

func TestNumericRawEncoder_SpecialValues(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	testCases := []struct {
		name  string
		value float64
	}{
		{"Zero", 0.0},
		{"Positive infinity", math.Inf(1)},
		{"Negative infinity", math.Inf(-1)},
		{"NaN", math.NaN()},
		{"Very small positive", 1e-300},
		{"Very small negative", -1e-300},
		{"Very large positive", 1e300},
		{"Very large negative", -1e300},
		{"Smallest positive", math.SmallestNonzeroFloat64},
		{"Maximum float64", math.MaxFloat64},
		{"Minimum (most negative)", -math.MaxFloat64},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoder := NewNumericRawEncoder(engine)
			encoder.Write(tc.value)

			decoder := NewNumericRawDecoder(engine)
			var decoded []float64
			for val := range decoder.All(encoder.Bytes(), 1) {
				decoded = append(decoded, val)
			}

			require.Len(t, decoded, 1)
			if math.IsNaN(tc.value) {
				require.True(t, math.IsNaN(decoded[0]))
			} else {
				require.Equal(t, tc.value, decoded[0])
			}
		})
	}
}

func TestNumericRawEncoder_PrecisionPreservation(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)

	// Test that precision is preserved exactly
	precisionTests := []float64{
		math.Pi,
		math.E,
		math.Sqrt2,
		1.0 / 3.0,
		0.1 + 0.2, // Classic floating point issue
		1.234567890123456,
		9.87654321098765,
	}

	encoder.WriteSlice(precisionTests)

	decoder := NewNumericRawDecoder(engine)
	decoded := make([]float64, 0, len(precisionTests))
	for val := range decoder.All(encoder.Bytes(), len(precisionTests)) {
		decoded = append(decoded, val)
	}

	require.Len(t, decoded, len(precisionTests))
	for i, original := range precisionTests {
		require.Equal(t, original, decoded[i])
		// Verify bit-exact equality
		require.Equal(t, math.Float64bits(original), math.Float64bits(decoded[i]))
	}
}

// === NumericRawDecoder Tests ===

func TestNumericRawDecoder_All_EmptyData(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewNumericRawDecoder(engine)

	decoded := make([]float64, 0)
	for val := range decoder.All([]byte{}, 0) {
		decoded = append(decoded, val)
	}

	require.Empty(t, decoded)
}

func TestNumericRawDecoder_All_InvalidDataLength(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewNumericRawDecoder(engine)

	// Data length not multiple of 8
	invalidData := []byte{1, 2, 3, 4, 5}

	decoded := make([]float64, 0, 1)
	for val := range decoder.All(invalidData, 1) {
		decoded = append(decoded, val)
	}

	require.Empty(t, decoded)
}

func TestNumericRawDecoder_All_EarlyTermination(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{3.14159, 2.71828, 1.41421, 1.73205}
	encoder.WriteSlice(values)

	decoder := NewNumericRawDecoder(engine)
	decoded := make([]float64, 0, len(values))
	count := 0
	for val := range decoder.All(encoder.Bytes(), len(values)) {
		decoded = append(decoded, val)
		count++
		if count >= 2 {
			break // Early termination
		}
	}

	require.Len(t, decoded, 2)
	require.Equal(t, values[0], decoded[0])
	require.Equal(t, values[1], decoded[1])
}

func TestNumericRawDecoder_At_BasicAccess(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{3.14159, 2.71828, 1.41421, 1.73205}
	encoder.WriteSlice(values)

	decoder := NewNumericRawDecoder(engine)

	// Test each index
	for i, expected := range values {
		val, ok := decoder.At(encoder.Bytes(), i, len(values))
		require.True(t, ok)
		require.Equal(t, expected, val)
	}
}

func TestNumericRawDecoder_At_InvalidIndices(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{3.14159, 2.71828}
	encoder.WriteSlice(values)

	decoder := NewNumericRawDecoder(engine)
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
			_, ok := decoder.At(data, tc.index, len(values))
			require.False(t, ok)
		})
	}
}

func TestNumericRawDecoder_At_EmptyData(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewNumericRawDecoder(engine)

	_, ok := decoder.At([]byte{}, 0, 0)
	require.False(t, ok)
}

// === NumericRawUnsafeDecoder Tests ===

func TestNumericRawUnsafeDecoder_All(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{3.14159, 2.71828, 1.41421}
	encoder.WriteSlice(values)

	decoder := NewNumericRawUnsafeDecoder(engine)
	decoded := make([]float64, 0, len(values))
	for val := range decoder.All(encoder.Bytes(), len(values)) {
		decoded = append(decoded, val)
	}

	require.Len(t, decoded, len(values))
	for i, original := range values {
		require.Equal(t, original, decoded[i])
	}
}

func TestNumericRawUnsafeDecoder_All_EmptyData(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewNumericRawUnsafeDecoder(engine)

	decoded := make([]float64, 0)
	for val := range decoder.All([]byte{}, 0) {
		decoded = append(decoded, val)
	}

	require.Empty(t, decoded)
}

func TestNumericRawUnsafeDecoder_All_InvalidDataLength(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewNumericRawUnsafeDecoder(engine)

	// Data length not multiple of 8
	invalidData := []byte{1, 2, 3, 4, 5}

	decoded := make([]float64, 0, 1)
	for val := range decoder.All(invalidData, 1) {
		decoded = append(decoded, val)
	}

	require.Empty(t, decoded)
}

func TestNumericRawUnsafeDecoder_At(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{3.14159, 2.71828, 1.41421}
	encoder.WriteSlice(values)

	decoder := NewNumericRawUnsafeDecoder(engine)

	// Test each index
	for i, expected := range values {
		val, ok := decoder.At(encoder.Bytes(), i, len(values))
		require.True(t, ok)
		require.Equal(t, expected, val)
	}
}

func TestNumericRawUnsafeDecoder_At_InvalidIndices(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{3.14159, 2.71828}
	encoder.WriteSlice(values)

	decoder := NewNumericRawUnsafeDecoder(engine)
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
			_, ok := decoder.At(data, tc.index, len(values))
			require.False(t, ok)
		})
	}
}

// === Round-Trip Tests ===

func TestNumericRaw_RoundTrip_LargeDataset(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)

	// Generate 1000 values
	values := make([]float64, 1000)
	for i := range values {
		values[i] = float64(i) * 0.1
	}

	encoder.WriteSlice(values)
	require.Equal(t, 1000, encoder.Len())
	require.Equal(t, 8000, encoder.Size())

	// Decode with safe decoder
	decoder := NewNumericRawDecoder(engine)
	decoded := make([]float64, 0, len(values))
	for val := range decoder.All(encoder.Bytes(), len(values)) {
		decoded = append(decoded, val)
	}

	require.Len(t, decoded, len(values))
	for i, original := range values {
		require.Equal(t, original, decoded[i])
	}

	// Decode with unsafe decoder
	unsafeDecoder := NewNumericRawUnsafeDecoder(engine)
	decodedUnsafe := make([]float64, 0, len(values))
	for val := range unsafeDecoder.All(encoder.Bytes(), len(values)) {
		decodedUnsafe = append(decodedUnsafe, val)
	}

	require.Len(t, decodedUnsafe, len(values))
	for i, original := range values {
		require.Equal(t, original, decodedUnsafe[i])
	}
}

func TestNumericRaw_RoundTrip_BigEndian(t *testing.T) {
	engine := endian.GetBigEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := []float64{3.14159, 2.71828, 1.41421}

	encoder.WriteSlice(values)

	// Decode with matching big-endian decoder
	decoder := NewNumericRawDecoder(engine)
	decoded := make([]float64, 0, len(values))
	for val := range decoder.All(encoder.Bytes(), len(values)) {
		decoded = append(decoded, val)
	}

	require.Len(t, decoded, len(values))
	for i, original := range values {
		require.Equal(t, original, decoded[i])
	}
}

func TestNumericRaw_EncodingSize(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Test predictable size: 8 bytes per float
	for i := 1; i <= 100; i++ {
		enc := NewNumericRawEncoder(engine)
		values := make([]float64, i)
		for j := range values {
			values[j] = float64(j) * 0.1
		}
		enc.WriteSlice(values)

		require.Equal(t, i, enc.Len())
		require.Equal(t, i*8, enc.Size())
	}
}

func TestNumericRaw_RoundTrip_RandomAccess(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)
	values := make([]float64, 100)
	for i := range values {
		values[i] = float64(i) * math.Pi
	}

	encoder.WriteSlice(values)

	// Test random access with both decoders
	decoder := NewNumericRawDecoder(engine)
	unsafeDecoder := NewNumericRawUnsafeDecoder(engine)
	data := encoder.Bytes()

	// Test accessing various indices
	indices := []int{0, 1, 10, 50, 99}
	for _, idx := range indices {
		// Safe decoder
		val, ok := decoder.At(data, idx, len(values))
		require.True(t, ok)
		require.Equal(t, values[idx], val)

		// Unsafe decoder
		valUnsafe, ok := unsafeDecoder.At(data, idx, len(values))
		require.True(t, ok)
		require.Equal(t, values[idx], valUnsafe)
	}
}

func TestNumericRaw_DecoderComparison(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewNumericRawEncoder(engine)

	// Generate diverse test data
	values := []float64{
		0.0,
		1.0,
		-1.0,
		math.Pi,
		math.E,
		math.Inf(1),
		math.Inf(-1),
		math.NaN(),
		1e-300,
		1e300,
	}

	encoder.WriteSlice(values)
	data := encoder.Bytes()

	// Decode with both decoders and compare
	safeDecoder := NewNumericRawDecoder(engine)
	unsafeDecoder := NewNumericRawUnsafeDecoder(engine)

	safeResults := make([]float64, 0, len(values))
	for val := range safeDecoder.All(data, len(values)) {
		safeResults = append(safeResults, val)
	}

	unsafeResults := make([]float64, 0, len(values))
	for val := range unsafeDecoder.All(data, len(values)) {
		unsafeResults = append(unsafeResults, val)
	}

	require.Equal(t, len(safeResults), len(unsafeResults))
	for i := range values {
		if math.IsNaN(values[i]) {
			require.True(t, math.IsNaN(safeResults[i]))
			require.True(t, math.IsNaN(unsafeResults[i]))
		} else {
			require.Equal(t, safeResults[i], unsafeResults[i])
		}
	}
}
