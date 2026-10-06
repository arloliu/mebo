package chimp

import (
	"math"
	"math/bits"
	"math/rand"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/encoding/value/gorilla"
	valraw "github.com/arloliu/mebo/internal/encoding/value/raw"
	"github.com/arloliu/mebo/internal/pool"
	"github.com/stretchr/testify/require"
)

// chimpReferenceEncoder is a bit-at-a-time Chimp encoder that the reference-parity tests
// compare the production encoder against, byte for byte.
type chimpReferenceEncoder struct {
	data               []byte
	bitOffset          int
	prevValue          uint64
	count              int
	storedLeadingZeros int
}

func TestChimpCodecContract(t *testing.T) {
	values := []float64{1.5, 1.5, 2.75, 3.25}
	encoder := NewNumericChimpEncoder()
	t.Cleanup(encoder.Finish)
	encoder.WriteSlice(values)

	decoder := NewNumericChimpDecoder()
	decoded := make([]float64, len(values))
	require.Equal(t, len(values), decoder.DecodeAll(encoder.Bytes(), len(values), decoded))
	require.Equal(t, values, decoded)

	at, ok := decoder.At(encoder.Bytes(), 2, len(values))
	require.True(t, ok)
	require.Equal(t, values[2], at)
	require.Equal(t, len(encoder.Bytes()), decoder.ByteLength(encoder.Bytes(), len(values)))
	require.Zero(t, decoder.DecodeAll([]byte{1}, len(values), decoded))

	state, ok := NewChimpValState(encoder.Bytes())
	require.True(t, ok)
	state.SetCount(len(values))
	for i := 1; i < len(values); i++ {
		require.True(t, state.Next())
		require.Equal(t, values[i], state.Val())
	}
	require.False(t, state.Next())

	cursor, ok := NewChimpCursor(encoder.Bytes())
	require.True(t, ok)
	require.Equal(t, values[0], cursor.First())
	for i := 1; i < len(values); i++ {
		value, nextOK := cursor.Next()
		require.True(t, nextOK)
		require.Equal(t, values[i], value)
	}
	_, ok = NewChimpCursor([]byte{0})
	require.False(t, ok)
}

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

// === DecodeAll Tests ===

func TestNumericChimpDecoder_DecodeAll_MatchesAll(t *testing.T) {
	testCases := []struct {
		name   string
		values []float64
	}{
		{
			name:   "single_value",
			values: []float64{42.0},
		},
		{
			name:   "two_values",
			values: []float64{1.0, 2.0},
		},
		{
			name:   "unchanged_exactly_4",
			values: []float64{100.0, 100.0, 100.0, 100.0},
		},
		{
			name:   "unchanged_3_no_batch",
			values: []float64{100.0, 100.0, 100.0},
		},
		{
			name: "unchanged_long_run",
			values: func() []float64 {
				v := make([]float64, 50)
				for i := range v {
					v[i] = 42.0
				}

				return v
			}(),
		},
		{
			name:   "unchanged_then_change",
			values: []float64{1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 2.0, 2.0, 2.0, 2.0, 2.0, 2.0},
		},
		{
			name:   "varying_values",
			values: []float64{1.0, 10.0, 100.0, 1000.0, 10000.0, 0.1, 0.01},
		},
		{
			name:   "similar_values",
			values: []float64{100.0, 100.1, 100.2, 100.3, 100.4, 100.5},
		},
		{
			name: "special_values",
			values: []float64{
				0.0,
				math.Copysign(0, -1),
				1.0,
				-1.0,
				math.MaxFloat64,
				math.SmallestNonzeroFloat64,
				math.Inf(1),
				math.Inf(-1),
			},
		},
		{
			name: "sinusoidal_1000",
			values: func() []float64 {
				v := make([]float64, 1000)
				for i := range v {
					v[i] = 100.0 + float64(i)*0.1 + math.Sin(float64(i)*0.1)*5.0
				}

				return v
			}(),
		},
		{
			name: "unchanged_100_cross_buffer",
			values: func() []float64 {
				// 100 identical values forces chimpCountUnchangedRun to cross
				// the 64-bit buffer boundary (32 pairs = 64 bits per refill)
				v := make([]float64, 100)
				for i := range v {
					v[i] = 99.9
				}

				return v
			}(),
		},
		{
			name: "unchanged_200_then_change",
			values: func() []float64 {
				v := make([]float64, 202)
				for i := range 200 {
					v[i] = 42.0
				}
				v[200] = 99.0
				v[201] = 42.0

				return v
			}(),
		},
		{
			name:   "sticky_latency",
			values: generateStickyMetric(200, 12.5, 0.04, 0.15),
		},
		{
			name:   "jittered",
			values: generateJitteredValues(300, 100.0, 0.02, 42),
		},
	}

	decoder := NewNumericChimpDecoder()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoder := NewNumericChimpEncoder()
			encoder.WriteSlice(tc.values)
			data := append([]byte(nil), encoder.Bytes()...)
			encoder.Finish()

			count := len(tc.values)

			// Reference: decode via All() iterator
			allResult := make([]float64, 0, count)
			for v := range decoder.All(data, count) {
				allResult = append(allResult, v)
			}
			require.Equal(t, count, len(allResult), "All() count mismatch")

			// DecodeAll: decode into pre-allocated slice
			dst := make([]float64, count)
			produced := decoder.DecodeAll(data, count, dst)
			require.Equal(t, count, produced, "DecodeAll produced wrong count")

			for i := range count {
				if math.IsNaN(allResult[i]) {
					require.True(t, math.IsNaN(dst[i]), "DecodeAll NaN mismatch at %d", i)
				} else {
					require.Equal(t, allResult[i], dst[i], "DecodeAll mismatch at %d", i)
				}
			}
		})
	}
}

func TestNumericChimpDecoder_DecodeAll_EdgeCases(t *testing.T) {
	decoder := NewNumericChimpDecoder()

	// Empty/nil data
	require.Equal(t, 0, decoder.DecodeAll(nil, 0, nil))
	require.Equal(t, 0, decoder.DecodeAll([]byte{}, 0, nil))

	// dst too small
	dst := make([]float64, 1)
	require.Equal(t, 0, decoder.DecodeAll([]byte{0x01}, 5, dst))
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
			gorillaEnc := gorilla.NewNumericGorillaEncoder()
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
			rawEnc := valraw.NewNumericRawEncoder(endian.GetLittleEndianEngine())
			rawEnc.WriteSlice(tc.values)
			rawData := append([]byte(nil), rawEnc.Bytes()...)
			rawEnc.Finish()

			gorillaEnc := gorilla.NewNumericGorillaEncoder()
			gorillaEnc.WriteSlice(tc.values)
			gorillaData := append([]byte(nil), gorillaEnc.Bytes()...)
			gorillaEnc.Finish()

			chimpEnc := NewNumericChimpEncoder()
			chimpEnc.WriteSlice(tc.values)
			chimpData := append([]byte(nil), chimpEnc.Bytes()...)
			chimpEnc.Finish()

			// Decode all three
			rawDec := valraw.NewNumericRawDecoder(endian.GetLittleEndianEngine())
			gorillaDec := gorilla.NewNumericGorillaDecoder()
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

func TestNumericChimpEncoder_ReferenceParity(t *testing.T) {
	type writeOperation struct {
		values []float64
		bulk   bool
	}

	unchanged := make([]float64, 34)
	for i := range unchanged {
		unchanged[i] = 100.0
	}

	rng := rand.New(rand.NewSource(0x5eed))
	randomValues := make([]float64, 257)
	for i := range randomValues {
		randomValues[i] = math.Float64frombits(rng.Uint64())
	}

	// Windows: new leading (11), reused leading (10), trailing-zero (01), unchanged (00), then a sign flip (01).
	windowValues := []float64{
		math.Float64frombits(0x3ff0000000000000),
		math.Float64frombits(0x3ff0000000000001),
		math.Float64frombits(0x3ff0000000000003),
		math.Float64frombits(0x3ff0001000000003),
		math.Float64frombits(0x3ff0001000000003),
		math.Float64frombits(0xbff0001000000003),
	}
	specialValues := []float64{
		0,
		math.Copysign(0, -1),
		math.Inf(1),
		math.Inf(-1),
		math.Float64frombits(0x7ff8000000000001),
		math.Float64frombits(0xfff8000000000042),
		math.MaxFloat64,
		math.SmallestNonzeroFloat64,
	}
	bucketSwitch := chimpBucketSwitchValues(4096)
	maxSpill := chimpMaxSpillValues(4096)

	tests := []struct {
		name    string
		metrics [][]writeOperation
	}{
		{name: "unchanged count 32", metrics: [][]writeOperation{{{values: unchanged[:32], bulk: true}}}},
		{name: "unchanged count 33", metrics: [][]writeOperation{{{values: unchanged[:33], bulk: true}}}},
		{name: "unchanged count 34", metrics: [][]writeOperation{{{values: unchanged}}}},
		{
			name: "all flag windows",
			metrics: [][]writeOperation{{
				{values: windowValues[:2], bulk: true},
				{values: windowValues[2:4]},
				{values: windowValues[4:], bulk: true},
			}},
		},
		{
			name: "mixed scalar and bulk writes",
			metrics: [][]writeOperation{{
				{values: []float64{1.25}},
				{values: []float64{1.25, 1.5, 1.75}, bulk: true},
				{values: []float64{-4.5}},
				{values: nil, bulk: true},
				{values: []float64{-4.5, 1024.125}, bulk: true},
			}},
		},
		{name: "special float bit patterns", metrics: [][]writeOperation{{{values: specialValues, bulk: true}}}},
		{name: "single value", metrics: [][]writeOperation{{{values: randomValues[:1]}}}},
		{name: "deterministic random sequence", metrics: [][]writeOperation{{{values: randomValues, bulk: true}}}},
		{name: "leading bucket switches bulk", metrics: [][]writeOperation{{{values: bucketSwitch, bulk: true}}}},
		{name: "leading bucket switches scalar", metrics: [][]writeOperation{{{values: bucketSwitch[:1024]}}}},
		{name: "max spill density bulk", metrics: [][]writeOperation{{{values: maxSpill, bulk: true}}}},
		{name: "max spill density scalar", metrics: [][]writeOperation{{{values: maxSpill[:1024]}}}},
		{
			name: "metrics separated by Bytes and Reset",
			metrics: [][]writeOperation{
				{{values: randomValues[:5], bulk: true}},
				{{values: unchanged[:7]}, {values: windowValues, bulk: true}},
				{{values: maxSpill[:33]}, {values: specialValues, bulk: true}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoder := NewNumericChimpEncoder()
			t.Cleanup(encoder.Finish)
			reference := chimpReferenceEncoder{storedLeadingZeros: 65}
			decoder := NewNumericChimpDecoder()

			for _, metric := range tc.metrics {
				start := len(reference.data)
				valueCount := 0
				for _, operation := range metric {
					valueCount += len(operation.values)
				}
				expected := make([]float64, 0, valueCount)
				for _, operation := range metric {
					expected = append(expected, operation.values...)
					if operation.bulk {
						encoder.WriteSlice(operation.values)
						reference.writeSlice(operation.values)
						continue
					}

					for _, value := range operation.values {
						encoder.Write(value)
						reference.write(value)
					}
				}

				got := append([]byte(nil), encoder.Bytes()...)
				require.Equal(t, reference.bytes(), got)
				require.Equal(t, len(expected), encoder.Len())

				decoded := make([]float64, len(expected))
				require.Equal(t, len(expected), decoder.DecodeAll(got[start:], len(expected), decoded))
				for i := range expected {
					require.Equal(t, math.Float64bits(expected[i]), math.Float64bits(decoded[i]), "value %d", i)
				}

				encoder.Reset()
				reference.reset()
			}
		})
	}
}

// TestNumericChimpEncoder_TightCapacity holds the capacity invariant with no spare room:
// a bulk write into an empty buffer reserves exactly len(values)*10+16 bytes,
// and each scalar write starts with exactly the 16 bytes Write reserves.
// Spilling past the reservation would panic on the bounds check.
func TestNumericChimpEncoder_TightCapacity(t *testing.T) {
	tests := []struct {
		name     string
		values   []float64
		oddBits  int // record size of values 1, 3, 5, ...
		evenBits int // record size of values 2, 4, 6, ...
	}{
		{name: "leading bucket switches", values: chimpBucketSwitchValues(4096), oddBits: 69, evenBits: 61},
		{name: "max spill density", values: chimpMaxSpillValues(4096), oddBits: 68, evenBits: 69},
	}

	for _, tc := range tests {
		wantBits := 64
		for i := 1; i < len(tc.values); i++ {
			if i%2 == 1 {
				wantBits += tc.oddBits
			} else {
				wantBits += tc.evenBits
			}
		}

		t.Run(tc.name+"/bulk", func(t *testing.T) {
			encoder := NewNumericChimpEncoder()
			t.Cleanup(encoder.Finish)
			encoder.buf = pool.NewByteBuffer(0)
			reference := chimpReferenceEncoder{storedLeadingZeros: 65}

			encoder.WriteSlice(tc.values)
			reference.writeSlice(tc.values)
			reserved := len(tc.values)*10 + 16
			require.Equal(t, reserved, encoder.buf.Cap(), "WriteSlice reserves exactly len*10+16 bytes")

			got := encoder.Bytes()
			require.Equal(t, reserved, encoder.buf.Cap(), "no growth while spilling or flushing")
			require.Len(t, got, (wantBits+7)/8, "record sizes")
			require.Equal(t, reference.bytes(), got)
		})

		t.Run(tc.name+"/scalar", func(t *testing.T) {
			encoder := NewNumericChimpEncoder()
			t.Cleanup(encoder.Finish)
			reference := chimpReferenceEncoder{storedLeadingZeros: 65}
			values := tc.values[:1024]

			twoSpills := 0
			for _, value := range values {
				tightenChimpBuffer(encoder, 16)
				before := encoder.buf.Len()
				encoder.Write(value)
				reference.write(value)
				if encoder.buf.Len()-before == 16 {
					twoSpills++
				}
			}

			require.Positive(t, twoSpills, "some values spill twice into exactly 16 bytes")
			require.Equal(t, reference.bytes(), encoder.Bytes())
		})
	}
}

// tightenChimpBuffer copies the encoder's buffer into one with exactly avail bytes of spare capacity.
func tightenChimpBuffer(e *NumericChimpEncoder, avail int) {
	b := make([]byte, len(e.buf.B), len(e.buf.B)+avail)
	copy(b, e.buf.B)
	e.buf.B = b
}

// chimpBucketSwitchValues returns n values whose XORs alternate between leading-zero buckets 0 and 8
// with no trailing zeros, so every value after the first takes the new-leading branch: 69- and 61-bit records.
func chimpBucketSwitchValues(n int) []float64 {
	rng := rand.New(rand.NewSource(0xc417))
	values := make([]float64, n)
	prev := rng.Uint64()
	for i := range values {
		values[i] = math.Float64frombits(prev)
		if i%2 == 0 {
			prev ^= rng.Uint64() | 1<<63 | 1
		} else {
			prev ^= rng.Uint64()&0x00FFFFFFFFFFFFFF | 1<<55 | 1
		}
	}

	return values
}

// chimpMaxSpillValues returns n values whose XORs alternate between (leading 0, trailing 7) and (leading 0, trailing 0):
// a 68-bit trailing-zero record, which forgets the stored leading count, then a 69-bit new-leading record.
// That is Chimp's densest attainable stream.
func chimpMaxSpillValues(n int) []float64 {
	rng := rand.New(rand.NewSource(0xc418))
	values := make([]float64, n)
	prev := rng.Uint64()
	for i := range values {
		values[i] = math.Float64frombits(prev)
		if i%2 == 0 {
			prev ^= rng.Uint64()&^0xFF | 1<<63 | 1<<7
		} else {
			prev ^= rng.Uint64() | 1<<63 | 1
		}
	}

	return values
}

func (e *chimpReferenceEncoder) writeSlice(values []float64) {
	for _, value := range values {
		e.write(value)
	}
}

func (e *chimpReferenceEncoder) write(value float64) {
	e.count++
	valueBits := math.Float64bits(value)
	if e.count == 1 {
		e.prevValue = valueBits
		e.appendBits(valueBits, 64)

		return
	}

	xor := valueBits ^ e.prevValue
	e.prevValue = valueBits
	if xor == 0 {
		e.appendBits(0b00, 2)
		e.storedLeadingZeros = 65

		return
	}

	leading := bits.LeadingZeros64(xor)
	trailing := bits.TrailingZeros64(xor)
	rounded := chimpLeadingRound[leading]
	bucket := chimpLeadingRepresentation[leading]
	switch {
	case trailing > chimpTrailingThreshold:
		significant := max(64-rounded-trailing, 1)
		e.appendBits(0b01, 2)
		e.appendBits(bucket, 3)
		e.appendBits(uint64(significant)&0x3F, 6)
		e.appendBits(xor>>uint(trailing), significant)
		e.storedLeadingZeros = 65
	case rounded == e.storedLeadingZeros:
		e.appendBits(0b10, 2)
		e.appendBits(xor, 64-rounded)
	default:
		e.storedLeadingZeros = rounded
		e.appendBits(0b11, 2)
		e.appendBits(bucket, 3)
		e.appendBits(xor, 64-rounded)
	}
}

func (e *chimpReferenceEncoder) appendBits(value uint64, numBits int) {
	for bitIndex := numBits - 1; bitIndex >= 0; bitIndex-- {
		if e.bitOffset == 0 {
			e.data = append(e.data, 0)
		}

		bit := byte(value>>uint(bitIndex)) & 1
		e.data[len(e.data)-1] |= bit << uint(7-e.bitOffset)
		e.bitOffset = (e.bitOffset + 1) & 7
	}
}

// bytes returns the stream so far; like the encoder's Bytes, it pads the last byte, so the next bit starts a new one.
func (e *chimpReferenceEncoder) bytes() []byte {
	e.bitOffset = 0

	return append([]byte(nil), e.data...)
}

// reset starts a new metric's stream after the bytes written so far, as the encoder's Reset does.
func (e *chimpReferenceEncoder) reset() {
	e.count = 0
	e.prevValue = 0
	e.storedLeadingZeros = 65
}
