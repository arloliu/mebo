package encoding

import (
	"math"
	"math/rand"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

func TestNumericChimpEncoder_SingleValue(t *testing.T) {
	encoder := NewNumericChimpEncoder()

	encoder.Write(42.0)

	require.Equal(t, 1, encoder.Len())
	require.Greater(t, encoder.Size(), 0)

	data := encoder.Bytes()
	require.Greater(t, len(data), 0)

	encoder.Finish()

	require.Equal(t, 1, encoder.Len())
	require.Panics(t, func() { encoder.Size() })
	require.Panics(t, func() { encoder.Bytes() })
	require.Panics(t, func() { encoder.Write(1.0) })
	require.Panics(t, func() { encoder.WriteSlice([]float64{1.0}) })
}

func TestNumericChimpEncoder_UnchangedValues(t *testing.T) {
	encoder := NewNumericChimpEncoder()

	// First value: 64 bits
	// Subsequent unchanged values: 2 bits each (flag 00)
	encoder.Write(100.0)
	encoder.Write(100.0)
	encoder.Write(100.0)
	encoder.Write(100.0)

	require.Equal(t, 4, encoder.Len())

	data := encoder.Bytes()
	// First value: 64 bits (8 bytes), next 3 values: 2 bits each (6 bits)
	// Total: 70 bits = 9 bytes
	require.LessOrEqual(t, len(data), 9, "Compressed size should be small for unchanged values")

	encoder.Finish()
}

func TestNumericChimpEncoder_SimilarValues(t *testing.T) {
	encoder := NewNumericChimpEncoder()

	values := []float64{100.0, 100.1, 100.2, 100.3, 100.4}
	for _, v := range values {
		encoder.Write(v)
	}

	require.Equal(t, 5, encoder.Len())

	data := encoder.Bytes()
	require.Less(t, len(data), 40, "Similar values should compress well")

	encoder.Finish()
}

func TestNumericChimpEncoder_WriteSlice(t *testing.T) {
	encoder := NewNumericChimpEncoder()

	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	encoder.WriteSlice(values)

	require.Equal(t, 5, encoder.Len())

	encoder.Finish()
	require.Equal(t, 5, encoder.Len())
}

func TestNumericChimpEncoder_EmptySlice(t *testing.T) {
	encoder := NewNumericChimpEncoder()

	encoder.WriteSlice([]float64{})

	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
}

func TestNumericChimpEncoder_Reset(t *testing.T) {
	encoder := NewNumericChimpEncoder()

	encoder.Write(1.0)
	encoder.Write(2.0)

	initialSize := encoder.Size()
	require.Greater(t, initialSize, 0)

	encoder.Reset()

	require.Equal(t, initialSize, encoder.Size())

	encoder.Write(3.0)
	encoder.Write(4.0)

	require.Greater(t, encoder.Size(), initialSize)
}

func TestNumericChimpEncoder_SpecialValues(t *testing.T) {
	encoder := NewNumericChimpEncoder()

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

func TestNumericChimpDecoder_SingleValue(t *testing.T) {
	encoder := NewNumericChimpEncoder()
	encoder.Write(42.0)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericChimpDecoder()
	values := make([]float64, 0)
	for v := range decoder.All(data, 1) {
		values = append(values, v)
	}

	require.Equal(t, []float64{42.0}, values)
}

func TestNumericChimpDecoder_UnchangedValues(t *testing.T) {
	encoder := NewNumericChimpEncoder()
	expected := []float64{100.0, 100.0, 100.0, 100.0}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericChimpDecoder()
	values := make([]float64, 0)
	for v := range decoder.All(data, len(expected)) {
		values = append(values, v)
	}

	require.Equal(t, expected, values)
}

func TestNumericChimpDecoder_SimilarValues(t *testing.T) {
	encoder := NewNumericChimpEncoder()
	expected := []float64{100.0, 100.1, 100.2, 100.3, 100.4}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericChimpDecoder()
	values := make([]float64, 0)
	for v := range decoder.All(data, len(expected)) {
		values = append(values, v)
	}

	require.Equal(t, expected, values)
}

func TestNumericChimpDecoder_VaryingValues(t *testing.T) {
	encoder := NewNumericChimpEncoder()
	expected := []float64{1.0, 10.0, 100.0, 1000.0, 10000.0, 0.1, 0.01}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericChimpDecoder()
	values := make([]float64, 0)
	for v := range decoder.All(data, len(expected)) {
		values = append(values, v)
	}

	require.Equal(t, expected, values)
}

func TestNumericChimpDecoder_SpecialValues(t *testing.T) {
	encoder := NewNumericChimpEncoder()
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

	decoder := NewNumericChimpDecoder()
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

func TestNumericChimpDecoder_NaN(t *testing.T) {
	encoder := NewNumericChimpEncoder()
	encoder.Write(1.0)
	encoder.Write(math.NaN())
	encoder.Write(2.0)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericChimpDecoder()
	values := make([]float64, 0)
	for v := range decoder.All(data, 3) {
		values = append(values, v)
	}

	require.Equal(t, 3, len(values))
	require.Equal(t, 1.0, values[0])
	require.True(t, math.IsNaN(values[1]))
	require.Equal(t, 2.0, values[2])
}

func TestNumericChimpDecoder_At(t *testing.T) {
	encoder := NewNumericChimpEncoder()
	expected := make([]float64, 300)
	for i := range expected {
		expected[i] = float64(i + 1)
	}

	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericChimpDecoder()

	for i, expectedVal := range expected {
		val, ok := decoder.At(data, i, len(expected))
		require.True(t, ok, "Should successfully decode at index %d", i)
		require.Equal(t, expectedVal, val, "Value at index %d", i)
	}

	val, ok := decoder.At(data, -1, len(expected))
	require.False(t, ok)
	require.Zero(t, val)

	val, ok = decoder.At(data, len(expected), len(expected))
	require.False(t, ok)
	require.Zero(t, val)
}

func TestNumericChimpDecoder_At_FirstValue(t *testing.T) {
	encoder := NewNumericChimpEncoder()
	encoder.Write(42.0)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericChimpDecoder()
	val, ok := decoder.At(data, 0, 1)

	require.True(t, ok)
	require.Equal(t, 42.0, val)
}

func TestNumericChimpDecoder_EmptyData(t *testing.T) {
	decoder := NewNumericChimpDecoder()

	values := make([]float64, 0)
	for v := range decoder.All([]byte{}, 0) {
		values = append(values, v)
	}
	require.Empty(t, values)

	val, ok := decoder.At([]byte{}, 0, 0)
	require.False(t, ok)
	require.Zero(t, val)
}

func TestNumericChimpRoundTrip_LargeDataset(t *testing.T) {
	encoder := NewNumericChimpEncoder()

	expected := make([]float64, 1000)
	base := 100.0
	for i := range expected {
		expected[i] = base + float64(i)*0.1 + math.Sin(float64(i)*0.1)*5.0
	}

	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	rawSize := len(expected) * 8
	compressedSize := len(data)
	compressionRatio := float64(rawSize) / float64(compressedSize)
	t.Logf("Compression ratio: %.2fx (raw: %d bytes, compressed: %d bytes)",
		compressionRatio, rawSize, compressedSize)

	decoder := NewNumericChimpDecoder()
	values := make([]float64, 0, len(expected))
	for v := range decoder.All(data, len(expected)) {
		values = append(values, v)
	}

	require.Equal(t, len(expected), len(values))
	for i := range expected {
		require.Equal(t, expected[i], values[i], "Mismatch at index %d", i)
	}
}

func TestNumericChimpRoundTrip_RandomAccess(t *testing.T) {
	encoder := NewNumericChimpEncoder()
	expected := []float64{10.0, 20.0, 30.0, 40.0, 50.0, 60.0, 70.0, 80.0, 90.0, 100.0}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericChimpDecoder()

	testIndices := []int{0, 3, 5, 7, 9}
	for _, idx := range testIndices {
		val, ok := decoder.At(data, idx, len(expected))
		require.True(t, ok, "Should decode at index %d", idx)
		require.Equal(t, expected[idx], val, "Value at index %d", idx)
	}
}

func TestNumericChimpDecoder_At_JitteredData(t *testing.T) {
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
			values := generateJitteredValues(tc.numValues, tc.baseValue, tc.deltaPercent, tc.randomSeed)

			encoder := NewNumericChimpEncoder()
			for _, val := range values {
				encoder.Write(val)
			}
			data := encoder.Bytes()
			encoder.Finish()
			t.Logf("Encoded %d values into %d bytes", len(values), len(data))

			decoder := NewNumericChimpDecoder()
			rng := rand.New(rand.NewSource(tc.randomSeed))

			numTests := 100
			for range numTests {
				index := rng.Intn(tc.numValues + 100)

				val, ok := decoder.At(data, index, len(values))

				if index >= len(values) {
					require.False(t, ok, "Index %d should be out of bounds", index)
				} else {
					require.True(t, ok, "At() failed for index %d", index)
					require.Equal(t, values[index], val,
						"At(index=%d) returned wrong value", index)
				}
			}

			t.Run("sequential_access", func(t *testing.T) {
				for i := range values {
					val, ok := decoder.At(data, i, len(values))
					require.True(t, ok, "At(%d) failed", i)
					require.Equal(t, values[i], val, "At(%d) returned wrong value", i)
				}
			})
		})
	}
}

func TestNumericChimpDecoder_ByteLength(t *testing.T) {
	encoder := NewNumericChimpEncoder()

	testValues := []float64{1.0, 2.0, 3.0, 4.0, 5.0}

	for _, val := range testValues {
		encoder.Write(val)
	}

	data := encoder.Bytes()

	decoder := NewNumericChimpDecoder()
	byteLen := decoder.ByteLength(data, len(testValues))

	t.Logf("Encoded %d values into %d bytes", len(testValues), len(data))
	t.Logf("ByteLength returned: %d bytes", byteLen)

	require.Greater(t, byteLen, 0, "ByteLength should return non-zero")
	require.LessOrEqual(t, byteLen, len(data), "ByteLength should not exceed actual data length")

	limitedData := data[:byteLen]
	decoded := make([]float64, 0, len(testValues))
	for val := range decoder.All(limitedData, len(testValues)) {
		decoded = append(decoded, val)
	}

	require.Equal(t, testValues, decoded, "Should decode all values from limited data")
}

func TestNumericChimpDecoder_ByteLength_MultiMetric(t *testing.T) {
	encoder1 := NewNumericChimpEncoder()
	encoder2 := NewNumericChimpEncoder()

	metric1Values := []float64{10.0, 11.0, 12.0}
	metric2Values := []float64{20.0, 21.0, 22.0}

	for _, val := range metric1Values {
		encoder1.Write(val)
	}
	for _, val := range metric2Values {
		encoder2.Write(val)
	}

	data1 := encoder1.Bytes()
	data2 := encoder2.Bytes()

	combinedData := append([]byte(nil), data1...)
	combinedData = append(combinedData, data2...)

	decoder := NewNumericChimpDecoder()

	byteLen1 := decoder.ByteLength(combinedData, len(metric1Values))
	require.Equal(t, len(data1), byteLen1, "ByteLength should match encoded length for first metric")

	decoded1 := make([]float64, 0, len(metric1Values))
	for val := range decoder.All(combinedData[:byteLen1], len(metric1Values)) {
		decoded1 = append(decoded1, val)
	}
	require.Equal(t, metric1Values, decoded1)

	metric2Start := byteLen1
	byteLen2 := decoder.ByteLength(combinedData[metric2Start:], len(metric2Values))

	decoded2 := make([]float64, 0, len(metric2Values))
	for val := range decoder.All(combinedData[metric2Start:metric2Start+byteLen2], len(metric2Values)) {
		decoded2 = append(decoded2, val)
	}
	require.Equal(t, metric2Values, decoded2)
}

func TestNumericChimpEncoder_AllFlagPaths(t *testing.T) {
	encoder := NewNumericChimpEncoder()

	// Carefully chosen values to exercise all 4 Chimp encoding flags
	values := []float64{
		1.0,                                      // First: stored raw (64 bits)
		1.0,                                      // Flag 00: unchanged
		2.0,                                      // Flag 11 or 01: changed, new leading
		2.0,                                      // Flag 00: unchanged
		2.5,                                      // Flag 10 or 11: similar XOR pattern
		100.0,                                    // Flag 11: large change, new leading
		100.000001,                               // Flag 01 or 10: tiny change (many trailing zeros)
		math.Float64frombits(0x3FF0000000000001), // Specific bit pattern
	}

	encoder.WriteSlice(values)
	data := encoder.Bytes()
	encoder.Finish()

	decoder := NewNumericChimpDecoder()
	decoded := make([]float64, 0, len(values))
	for v := range decoder.All(data, len(values)) {
		decoded = append(decoded, v)
	}

	require.Equal(t, len(values), len(decoded))
	for i := range values {
		require.Equal(t, values[i], decoded[i], "Mismatch at index %d", i)
	}
}

func TestNumericChimpVsGorilla_CompressionRatio(t *testing.T) {
	// Compare Chimp vs Gorilla compression on realistic data
	testCases := []struct {
		name   string
		values []float64
	}{
		{
			name: "constant",
			values: func() []float64 {
				v := make([]float64, 100)
				for i := range v {
					v[i] = 42.0
				}

				return v
			}(),
		},
		{
			name: "slowly_increasing",
			values: func() []float64 {
				v := make([]float64, 100)
				for i := range v {
					v[i] = 100.0 + float64(i)*0.01
				}

				return v
			}(),
		},
		{
			name: "sinusoidal",
			values: func() []float64 {
				v := make([]float64, 100)
				for i := range v {
					v[i] = 100.0 + math.Sin(float64(i)*0.1)*5.0
				}

				return v
			}(),
		},
		{
			name:   "jittered",
			values: generateJitteredValues(100, 100.0, 0.02, 42),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode with Gorilla
			gorillaEnc := NewNumericGorillaEncoder()
			gorillaEnc.WriteSlice(tc.values)
			gorillaData := gorillaEnc.Bytes()
			gorillaEnc.Finish()

			// Encode with Chimp
			chimpEnc := NewNumericChimpEncoder()
			chimpEnc.WriteSlice(tc.values)
			chimpData := chimpEnc.Bytes()
			chimpEnc.Finish()

			rawSize := len(tc.values) * 8
			t.Logf("Raw: %d bytes, Gorilla: %d bytes (%.2fx), Chimp: %d bytes (%.2fx), Chimp vs Gorilla: %.1f%%",
				rawSize,
				len(gorillaData), float64(rawSize)/float64(len(gorillaData)),
				len(chimpData), float64(rawSize)/float64(len(chimpData)),
				(1.0-float64(len(chimpData))/float64(len(gorillaData)))*100)

			// Verify Chimp decodes correctly
			decoder := NewNumericChimpDecoder()
			decoded := make([]float64, 0, len(tc.values))
			for v := range decoder.All(chimpData, len(tc.values)) {
				decoded = append(decoded, v)
			}
			require.Equal(t, len(tc.values), len(decoded))
			for i := range tc.values {
				if math.IsNaN(tc.values[i]) {
					require.True(t, math.IsNaN(decoded[i]))
				} else {
					require.Equal(t, tc.values[i], decoded[i], "Chimp mismatch at index %d", i)
				}
			}
		})
	}
}

func TestChimpGorillaRaw_DecodedValueEquivalence(t *testing.T) {
	// Verify that all three encoders produce identical decoded values for the same input.
	// This is a correctness test: the compression format differs, but the decoded output must match.
	testCases := []struct {
		name   string
		values []float64
	}{
		{
			name: "slowly_increasing",
			values: func() []float64 {
				v := make([]float64, 100)
				for i := range v {
					v[i] = 100.0 + float64(i)*0.01
				}

				return v
			}(),
		},
		{
			name: "sinusoidal",
			values: func() []float64 {
				v := make([]float64, 100)
				for i := range v {
					v[i] = 100.0 + math.Sin(float64(i)*0.1)*5.0
				}

				return v
			}(),
		},
		{
			name:   "jittered",
			values: generateJitteredValues(100, 100.0, 0.02, 42),
		},
		{
			name:   "special_values",
			values: []float64{0, math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64, math.Inf(1), math.Inf(-1)},
		},
		{
			name:   "single_value",
			values: []float64{3.14},
		},
		{
			name: "constant",
			values: func() []float64 {
				v := make([]float64, 50)
				for i := range v {
					v[i] = 42.0
				}

				return v
			}(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode with all three encoders
			rawEnc := NewNumericRawEncoder(endian.GetLittleEndianEngine())
			rawEnc.WriteSlice(tc.values)
			rawData := append([]byte(nil), rawEnc.Bytes()...)
			rawEnc.Finish()

			gorillaEnc := NewNumericGorillaEncoder()
			gorillaEnc.WriteSlice(tc.values)
			gorillaData := append([]byte(nil), gorillaEnc.Bytes()...)
			gorillaEnc.Finish()

			chimpEnc := NewNumericChimpEncoder()
			chimpEnc.WriteSlice(tc.values)
			chimpData := append([]byte(nil), chimpEnc.Bytes()...)
			chimpEnc.Finish()

			// Decode all three
			rawDec := NewNumericRawDecoder(endian.GetLittleEndianEngine())
			gorillaDec := NewNumericGorillaDecoder()
			chimpDec := NewNumericChimpDecoder()

			rawDecoded := make([]float64, 0, len(tc.values))
			for v := range rawDec.All(rawData, len(tc.values)) {
				rawDecoded = append(rawDecoded, v)
			}

			gorillaDecoded := make([]float64, 0, len(tc.values))
			for v := range gorillaDec.All(gorillaData, len(tc.values)) {
				gorillaDecoded = append(gorillaDecoded, v)
			}

			chimpDecoded := make([]float64, 0, len(tc.values))
			for v := range chimpDec.All(chimpData, len(tc.values)) {
				chimpDecoded = append(chimpDecoded, v)
			}

			// Assert all three produce identical results
			require.Equal(t, len(tc.values), len(rawDecoded), "raw count mismatch")
			require.Equal(t, len(tc.values), len(gorillaDecoded), "gorilla count mismatch")
			require.Equal(t, len(tc.values), len(chimpDecoded), "chimp count mismatch")

			for i := range tc.values {
				if math.IsNaN(tc.values[i]) {
					require.True(t, math.IsNaN(rawDecoded[i]), "raw NaN mismatch at %d", i)
					require.True(t, math.IsNaN(gorillaDecoded[i]), "gorilla NaN mismatch at %d", i)
					require.True(t, math.IsNaN(chimpDecoded[i]), "chimp NaN mismatch at %d", i)
				} else {
					require.Equal(t, rawDecoded[i], gorillaDecoded[i], "raw vs gorilla mismatch at index %d", i)
					require.Equal(t, rawDecoded[i], chimpDecoded[i], "raw vs chimp mismatch at index %d", i)
				}
			}

			// Also verify At() random access equivalence
			for i := range tc.values {
				rawVal, rawOk := rawDec.At(rawData, i, len(tc.values))
				gorillaVal, gorillaOk := gorillaDec.At(gorillaData, i, len(tc.values))
				chimpVal, chimpOk := chimpDec.At(chimpData, i, len(tc.values))

				require.True(t, rawOk, "raw At(%d) failed", i)
				require.True(t, gorillaOk, "gorilla At(%d) failed", i)
				require.True(t, chimpOk, "chimp At(%d) failed", i)

				if math.IsNaN(tc.values[i]) {
					require.True(t, math.IsNaN(rawVal))
					require.True(t, math.IsNaN(gorillaVal))
					require.True(t, math.IsNaN(chimpVal))
				} else {
					require.Equal(t, rawVal, gorillaVal, "At() raw vs gorilla mismatch at %d", i)
					require.Equal(t, rawVal, chimpVal, "At() raw vs chimp mismatch at %d", i)
				}
			}
		})
	}
}
