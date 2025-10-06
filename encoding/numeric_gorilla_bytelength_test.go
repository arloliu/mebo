package encoding

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNumericGorillaDecoder_ByteLength tests the ByteLength method
func TestNumericGorillaDecoder_ByteLength(t *testing.T) {
	encoder := NewNumericGorillaEncoder()

	// Create test data with known values
	testValues := []float64{1.0, 2.0, 3.0, 4.0, 5.0}

	for _, val := range testValues {
		encoder.Write(val)
	}

	data := encoder.Bytes()

	// Test ByteLength
	decoder := NewNumericGorillaDecoder()
	byteLen := decoder.ByteLength(data, len(testValues))

	t.Logf("Encoded %d values into %d bytes", len(testValues), len(data))
	t.Logf("ByteLength returned: %d bytes", byteLen)

	require.Greater(t, byteLen, 0, "ByteLength should return non-zero")
	require.LessOrEqual(t, byteLen, len(data), "ByteLength should not exceed actual data length")

	// Verify that decoding exactly byteLen bytes gives us all the values
	limitedData := data[:byteLen]
	decoded := make([]float64, 0, len(testValues))
	for val := range decoder.All(limitedData, len(testValues)) {
		decoded = append(decoded, val)
	}

	require.Equal(t, testValues, decoded, "Should decode all values from limited data")

	// Now test that ByteLength works correctly for multiple metrics
	// Encode two metrics
	encoder1 := NewNumericGorillaEncoder()
	encoder2 := NewNumericGorillaEncoder()

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

	t.Logf("Metric 1 encoded into %d bytes", len(data1))
	t.Logf("Metric 2 encoded into %d bytes", len(data2))

	// Concatenate the two metrics
	combinedData := append([]byte(nil), data1...)
	combinedData = append(combinedData, data2...)

	// ByteLength for metric 1 should return len(data1)
	byteLen1 := decoder.ByteLength(combinedData, len(metric1Values))
	t.Logf("ByteLength for metric 1: %d (expected %d)", byteLen1, len(data1))

	require.Equal(t, len(data1), byteLen1, "ByteLength should match encoded length for first metric")

	// Decode metric 1 from combined data
	decoded1 := make([]float64, 0, len(metric1Values))
	for val := range decoder.All(combinedData[:byteLen1], len(metric1Values)) {
		decoded1 = append(decoded1, val)
	}

	require.Equal(t, metric1Values, decoded1, "Should decode metric 1 correctly")

	// Decode metric 2 from combined data
	metric2Start := byteLen1
	byteLen2 := decoder.ByteLength(combinedData[metric2Start:], len(metric2Values))
	t.Logf("ByteLength for metric 2: %d (expected %d)", byteLen2, len(data2))

	decoded2 := make([]float64, 0, len(metric2Values))
	for val := range decoder.All(combinedData[metric2Start:metric2Start+byteLen2], len(metric2Values)) {
		decoded2 = append(decoded2, val)
	}

	require.Equal(t, metric2Values, decoded2, "Should decode metric 2 correctly")
}
