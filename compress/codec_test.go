package compress

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/arloliu/mebo/format"
	"github.com/stretchr/testify/require"
)

// MockCompressor implements the Compressor interface for testing purposes.
type MockCompressor struct {
	compressionType format.CompressionType
	compressionFunc func([]byte) ([]byte, error)
	resetFunc       func()
}

// NewMockCompressor creates a new mock compressor with the specified type.
func NewMockCompressor(compressionType format.CompressionType) *MockCompressor {
	return &MockCompressor{
		compressionType: compressionType,
		compressionFunc: func(data []byte) ([]byte, error) {
			// Simple mock: just return the input data (no actual compression)
			return data, nil
		},
		resetFunc: func() {
			// Mock reset does nothing
		},
	}
}

func (m *MockCompressor) Type() format.CompressionType {
	return m.compressionType
}

func (m *MockCompressor) Compress(data []byte) ([]byte, error) {
	return m.compressionFunc(data)
}

func (m *MockCompressor) CompressTo(data []byte, writer io.Writer) (int, error) {
	compressed, err := m.Compress(data)
	if err != nil {
		return 0, err
	}

	return writer.Write(compressed)
}

func (m *MockCompressor) EstimateCompressedSize(inputSize int) int {
	switch m.compressionType {
	case format.CompressionNone:
		return inputSize
	case format.CompressionLZ4, format.CompressionS2:
		return int(float64(inputSize) * 0.75) // Conservative estimate
	case format.CompressionZstd:
		return int(float64(inputSize) * 0.50) // Conservative estimate
	default:
		return inputSize
	}
}

func (m *MockCompressor) Reset() {
	m.resetFunc()
}

// MockDecompressor implements the Decompressor interface for testing purposes.
type MockDecompressor struct {
	compressionType   format.CompressionType
	decompressionFunc func([]byte) ([]byte, error)
	resetFunc         func()
}

// NewMockDecompressor creates a new mock decompressor with the specified type.
func NewMockDecompressor(compressionType format.CompressionType) *MockDecompressor {
	return &MockDecompressor{
		compressionType: compressionType,
		decompressionFunc: func(data []byte) ([]byte, error) {
			// Simple mock: just return the input data (no actual decompression)
			return data, nil
		},
		resetFunc: func() {
			// Mock reset does nothing
		},
	}
}

func (m *MockDecompressor) Type() format.CompressionType {
	return m.compressionType
}

func (m *MockDecompressor) Decompress(data []byte) ([]byte, error) {
	return m.decompressionFunc(data)
}

func (m *MockDecompressor) DecompressTo(data []byte, writer io.Writer) (int, error) {
	decompressed, err := m.Decompress(data)
	if err != nil {
		return 0, err
	}

	return writer.Write(decompressed)
}

func (m *MockDecompressor) EstimateDecompressedSize(compressedData []byte) int {
	// Mock implementation: assume 2x expansion ratio for compressed data
	switch m.compressionType {
	case format.CompressionNone:
		return len(compressedData)
	case format.CompressionLZ4, format.CompressionS2, format.CompressionZstd:
		return len(compressedData) * 2
	default:
		return len(compressedData) * 2
	}
}

func (m *MockDecompressor) Reset() {
	m.resetFunc()
}

// MockCodec implements the Codec interface.
type MockCodec struct {
	compressionType format.CompressionType
	compressor      *MockCompressor
	decompressor    *MockDecompressor
}

// NewMockCodec creates a new mock codec that implements both compression and decompression.
func NewMockCodec(compressionType format.CompressionType) *MockCodec {
	return &MockCodec{
		compressionType: compressionType,
		compressor:      NewMockCompressor(compressionType),
		decompressor:    NewMockDecompressor(compressionType),
	}
}

// Compressor interface methods
func (c *MockCodec) Type() format.CompressionType {
	return c.compressionType
}

func (c *MockCodec) Compress(data []byte) ([]byte, error) {
	return c.compressor.Compress(data)
}

func (c *MockCodec) CompressTo(data []byte, writer io.Writer) (int, error) {
	return c.compressor.CompressTo(data, writer)
}

func (c *MockCodec) EstimateCompressedSize(inputSize int) int {
	return c.compressor.EstimateCompressedSize(inputSize)
}

func (c *MockCodec) Reset() {
	c.compressor.Reset()
	c.decompressor.Reset()
}

// Decompressor interface methods
func (c *MockCodec) Decompress(data []byte) ([]byte, error) {
	return c.decompressor.Decompress(data)
}

func (c *MockCodec) DecompressTo(data []byte, writer io.Writer) (int, error) {
	return c.decompressor.DecompressTo(data, writer)
}

func (c *MockCodec) EstimateDecompressedSize(compressedData []byte) int {
	return c.decompressor.EstimateDecompressedSize(compressedData)
}

// Test CompressionType String() method
func TestCompressionType_String(t *testing.T) {
	tests := []struct {
		name     string
		cType    format.CompressionType
		expected string
	}{
		{
			name:     "none compression",
			cType:    format.CompressionNone,
			expected: "None",
		},
		{
			name:     "zstd compression",
			cType:    format.CompressionZstd,
			expected: "Zstd",
		},
		{
			name:     "s2 compression",
			cType:    format.CompressionS2,
			expected: "S2",
		},
		{
			name:     "lz4 compression",
			cType:    format.CompressionLZ4,
			expected: "LZ4",
		},
		{
			name:     "unknown compression",
			cType:    format.CompressionType(0xFF),
			expected: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.cType.String()
			require.Equal(t, tt.expected, result)
		})
	}
}

// Test Compressor interface implementation
func TestCompressor_Interface(t *testing.T) {
	testData := []byte("test time-series data for compression")

	compressor := NewMockCompressor(format.CompressionZstd)

	// Test Type method
	require.Equal(t, format.CompressionZstd, compressor.Type())

	// Test Compress method
	compressed, err := compressor.Compress(testData)
	require.NoError(t, err)
	require.Equal(t, testData, compressed) // Mock returns same data

	// Test EstimateCompressedSize method
	estimatedSize := compressor.EstimateCompressedSize(len(testData))
	expectedSize := int(float64(len(testData)) * 0.50) // Zstd estimate
	require.Equal(t, expectedSize, estimatedSize)

	// Test Reset method (should not panic)
	require.NotPanics(t, func() {
		compressor.Reset()
	})
}

// Test Decompressor interface implementation
func TestDecompressor_Interface(t *testing.T) {
	testData := []byte("compressed time-series data for decompression")

	decompressor := NewMockDecompressor(format.CompressionS2)

	// Test Type method
	require.Equal(t, format.CompressionS2, decompressor.Type())

	// Test Decompress method
	decompressed, err := decompressor.Decompress(testData)
	require.NoError(t, err)
	require.Equal(t, testData, decompressed) // Mock returns same data

	// Test EstimateDecompressedSize method
	estimatedSize := decompressor.EstimateDecompressedSize(testData)
	expectedSize := len(testData) * 2 // Mock assumes 2x expansion
	require.Equal(t, expectedSize, estimatedSize)

	// Test Reset method (should not panic)
	require.NotPanics(t, func() {
		decompressor.Reset()
	})
}

// Test Codec interface implementation
func TestCodec_Interface(t *testing.T) {
	testData := []byte("time-series payload data for codec testing")

	codec := NewMockCodec(format.CompressionLZ4)

	// Test that codec implements both interfaces
	require.Implements(t, (*Compressor)(nil), codec)
	require.Implements(t, (*Decompressor)(nil), codec)
	require.Implements(t, (*Codec)(nil), codec)

	// Test Type method (should be consistent for both interfaces)
	require.Equal(t, format.CompressionLZ4, codec.Type())

	// Test round-trip compression/decompression
	compressed, err := codec.Compress(testData)
	require.NoError(t, err)

	decompressed, err := codec.Decompress(compressed)
	require.NoError(t, err)
	require.Equal(t, testData, decompressed)
}

// Test CompressionStats calculation methods
func TestCompressionStats_Calculations(t *testing.T) {
	tests := []struct {
		name            string
		stats           CompressionStats
		expectedRatio   float64
		expectedSavings float64
	}{
		{
			name: "good compression",
			stats: CompressionStats{
				Algorithm:      format.CompressionZstd,
				OriginalSize:   1000,
				CompressedSize: 300,
			},
			expectedRatio:   0.3,
			expectedSavings: 70.0,
		},
		{
			name: "no compression benefit",
			stats: CompressionStats{
				Algorithm:      format.CompressionNone,
				OriginalSize:   500,
				CompressedSize: 500,
			},
			expectedRatio:   1.0,
			expectedSavings: 0.0,
		},
		{
			name: "compression overhead",
			stats: CompressionStats{
				Algorithm:      format.CompressionS2,
				OriginalSize:   100,
				CompressedSize: 120,
			},
			expectedRatio:   1.2,
			expectedSavings: -20.0,
		},
		{
			name: "zero original size",
			stats: CompressionStats{
				Algorithm:      format.CompressionLZ4,
				OriginalSize:   0,
				CompressedSize: 100,
			},
			expectedRatio:   0.0,
			expectedSavings: 100.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratio := tt.stats.CompressionRatio()
			require.InDelta(t, tt.expectedRatio, ratio, 0.001)

			savings := tt.stats.SpaceSavings()
			require.InDelta(t, tt.expectedSavings, savings, 0.001)
		})
	}
}

// Test interface usage patterns for mebo time-series scenarios
func TestMeboUsagePatterns(t *testing.T) {
	// Simulate typical mebo time-series payload sizes
	payloadSizes := []int{
		1024,  // 1KB - small metric payload
		8192,  // 8KB - medium metric payload
		32768, // 32KB - large metric payload
		65536, // 64KB - maximum expected payload
	}

	compressionTypes := []format.CompressionType{
		format.CompressionNone,
		format.CompressionLZ4,
		format.CompressionS2,
		format.CompressionZstd,
	}

	for _, payloadSize := range payloadSizes {
		for _, cType := range compressionTypes {
			t.Run(testName(payloadSize, cType), func(t *testing.T) {
				// Create test payload simulating encoded time-series data
				payload := make([]byte, payloadSize)
				for i := range payload {
					payload[i] = byte(i % 256) // Simple pattern
				}

				// Test compressor
				compressor := NewMockCompressor(cType)
				estimatedSize := compressor.EstimateCompressedSize(payloadSize)

				// Validate size estimates are reasonable
				switch cType {
				case format.CompressionNone:
					require.Equal(t, payloadSize, estimatedSize)
				case format.CompressionLZ4, format.CompressionS2:
					require.LessOrEqual(t, estimatedSize, payloadSize)
					require.GreaterOrEqual(t, estimatedSize, payloadSize/2) // Conservative estimate
				case format.CompressionZstd:
					require.LessOrEqual(t, estimatedSize, payloadSize)
					require.GreaterOrEqual(t, estimatedSize, payloadSize/4) // More aggressive estimate
				}

				// Test compression
				compressed, err := compressor.Compress(payload)
				require.NoError(t, err)
				require.NotNil(t, compressed)

				// Test decompressor
				decompressor := NewMockDecompressor(cType)
				decompressed, err := decompressor.Decompress(compressed)
				require.NoError(t, err)
				require.Equal(t, payload, decompressed)
			})
		}
	}
}

func testName(payloadSize int, cType format.CompressionType) string {
	return fmt.Sprintf("payload_%dKB_compression_%s", payloadSize/1024, cType.String())
}

func TestNoOpCompressor_EmptyData(t *testing.T) {
	compressor := NewNoOpCompressor()

	// Test compress nil data
	compressed, err := compressor.Compress(nil)
	require.NoError(t, err)
	require.Nil(t, compressed)

	// Test compress empty slice
	empty := []byte{}
	compressed, err = compressor.Compress(empty)
	require.NoError(t, err)
	require.Equal(t, empty, compressed)

	// Test decompress nil data
	decompressed, err := compressor.Decompress(nil)
	require.NoError(t, err)
	require.Nil(t, decompressed)

	// Test decompress empty slice
	decompressed, err = compressor.Decompress(empty)
	require.NoError(t, err)
	require.Equal(t, empty, decompressed)
}

func TestNoOpCompressor_RoundTrip(t *testing.T) {
	compressor := NewNoOpCompressor()

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "small text data",
			data: []byte("hello world"),
		},
		{
			name: "binary data",
			data: []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD},
		},
		{
			name: "repeated pattern",
			data: []byte("abcabcabcabcabc"),
		},
		{
			name: "large payload",
			data: make([]byte, 64*1024), // 64KB of zeros
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Compress
			compressed, err := compressor.Compress(tt.data)
			require.NoError(t, err)
			require.Equal(t, tt.data, compressed) // Should be identical (no compression)
			if len(tt.data) > 0 {
				require.Same(t, &tt.data[0], &compressed[0]) // Should be the same slice (no copy)
			}

			// Decompress
			decompressed, err := compressor.Decompress(compressed)
			require.NoError(t, err)
			require.Equal(t, tt.data, decompressed) // Should match original
			if len(compressed) > 0 {
				require.Same(t, &compressed[0], &decompressed[0]) // Should be the same slice (no copy)
			}
		})
	}
}

func TestNoOpCompressor_InterfaceCompliance(t *testing.T) {
	compressor := NewNoOpCompressor()

	// Test Compressor interface
	var _ Compressor = compressor

	// Test Decompressor interface
	var _ Decompressor = compressor

	// Test Codec interface
	var _ Codec = compressor
}

// getAllCodecs returns all available codec implementations for testing
func getAllCodecs() map[string]Codec {
	return map[string]Codec{
		"NoOp": NewNoOpCompressor(),
		"LZ4":  NewLZ4Compressor(),
		"S2":   NewS2Compressor(),
		"Zstd": NewZstdCompressor(),
	}
}

// TestAllCodecs_EmptyData tests that all codecs handle empty data correctly
func TestAllCodecs_EmptyData(t *testing.T) {
	codecs := getAllCodecs()

	for name, codec := range codecs {
		t.Run(name, func(t *testing.T) {
			// Test compression of nil data
			compressed, err := codec.Compress(nil)
			require.NoError(t, err)
			require.Nil(t, compressed, "Compressing nil should return nil")

			// Test decompression of nil data
			decompressed, err := codec.Decompress(nil)
			require.NoError(t, err)
			require.Nil(t, decompressed, "Decompressing nil should return nil")

			// Test compression of empty slice
			empty := []byte{}
			compressed, err = codec.Compress(empty)
			require.NoError(t, err)

			// Test decompression of empty slice
			decompressed, err = codec.Decompress(compressed)
			require.NoError(t, err)
			require.Empty(t, decompressed, "Decompressing empty should return empty")
		})
	}
}

// TestAllCodecs_RoundTrip tests compression and decompression round-trip for all codecs
func TestAllCodecs_RoundTrip(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
	}{
		{
			name: "small_text",
			data: []byte("Hello, World!"),
		},
		{
			name: "repeated_pattern",
			data: bytes.Repeat([]byte("ABCD"), 100),
		},
		{
			name: "binary_data",
			data: []byte{0x00, 0x01, 0x02, 0x03, 0xFF, 0xFE, 0xFD, 0xFC},
		},
		{
			name: "single_byte",
			data: []byte{0x42},
		},
		{
			name: "medium_payload",
			data: bytes.Repeat([]byte("Time series data with timestamp 1234567890 and value 3.14159"), 256), // ~16KB
		},
		{
			name: "large_payload",
			data: bytes.Repeat([]byte("Time series data with timestamp 1234567890 and value 3.14159"), 1024), // ~64KB
		},
		{
			name: "pseudo_random",
			data: func() []byte {
				// Create pseudo-random data that is semi-compressible
				data := make([]byte, 4096)
				for i := range data {
					if i%100 < 50 {
						data[i] = byte(i % 256)
					} else {
						data[i] = byte((i*7 + i*i) % 256)
					}
				}

				return data
			}(),
		},
		{
			name: "highly_compressible",
			data: make([]byte, 1024*1024), // 1MB of zeros
		},
	}

	codecs := getAllCodecs()

	for codecName, codec := range codecs {
		t.Run(codecName, func(t *testing.T) {
			for _, tc := range testCases {
				t.Run(tc.name, func(t *testing.T) {
					// Compress
					compressed, err := codec.Compress(tc.data)
					require.NoError(t, err)
					require.NotNil(t, compressed)

					// Log compression stats
					ratio := float64(len(compressed)) / float64(len(tc.data)) * 100
					t.Logf("Original: %d bytes, Compressed: %d bytes, Ratio: %.2f%%",
						len(tc.data), len(compressed), ratio)

					// Decompress
					decompressed, err := codec.Decompress(compressed)
					require.NoError(t, err)
					require.Equal(t, tc.data, decompressed, "Decompressed data must match original")

					// Verify data integrity
					require.Equal(t, len(tc.data), len(decompressed), "Length must match")
					if len(tc.data) > 0 {
						require.True(t, bytes.Equal(tc.data, decompressed), "Byte-by-byte comparison must match")
					}
				})
			}
		})
	}
}

// TestAllCodecs_InvalidData tests that all codecs handle invalid compressed data appropriately
func TestAllCodecs_InvalidData(t *testing.T) {
	invalidInputs := []struct {
		name string
		data []byte
	}{
		{
			name: "random_bytes",
			data: []byte{0xFF, 0xFF, 0xFF, 0xFF},
		},
		{
			name: "text_as_compressed",
			data: []byte("this is not compressed data"),
		},
		{
			name: "corrupted_header",
			data: []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07},
		},
	}

	codecs := getAllCodecs()

	for codecName, codec := range codecs {
		t.Run(codecName, func(t *testing.T) {
			// NoOp codec doesn't validate data, so skip invalid data tests
			if codecName == "NoOp" {
				t.Skip("NoOp codec doesn't validate data")
				return
			}

			for _, input := range invalidInputs {
				t.Run(input.name, func(t *testing.T) {
					_, err := codec.Decompress(input.data)
					require.Error(t, err, "Should return error for invalid compressed data")
				})
			}
		})
	}
}

// TestAllCodecs_ConcurrentUsage tests that all codecs are safe for concurrent use
func TestAllCodecs_ConcurrentUsage(t *testing.T) {
	const numGoroutines = 20
	testData := []byte("Concurrent compression test data with some content to compress")

	codecs := getAllCodecs()

	for codecName, codec := range codecs {
		t.Run(codecName, func(t *testing.T) {
			// Test concurrent compression
			t.Run("concurrent_compress", func(t *testing.T) {
				done := make(chan error, numGoroutines)

				for range numGoroutines {
					go func() {
						compressed, err := codec.Compress(testData)
						if err != nil {
							done <- err
							return
						}
						if compressed == nil {
							done <- fmt.Errorf("compressed result is nil")
							return
						}
						done <- nil
					}()
				}

				for range numGoroutines {
					err := <-done
					require.NoError(t, err)
				}
			})

			// Test concurrent decompression
			t.Run("concurrent_decompress", func(t *testing.T) {
				// First compress the data
				compressed, err := codec.Compress(testData)
				require.NoError(t, err)

				done := make(chan error, numGoroutines)

				for range numGoroutines {
					go func() {
						decompressed, err := codec.Decompress(compressed)
						if err != nil {
							done <- err
							return
						}
						if !bytes.Equal(testData, decompressed) {
							done <- fmt.Errorf("decompressed data mismatch")
							return
						}
						done <- nil
					}()
				}

				for range numGoroutines {
					err := <-done
					require.NoError(t, err)
				}
			})

			// Test concurrent compress and decompress
			t.Run("concurrent_mixed", func(t *testing.T) {
				done := make(chan error, numGoroutines*2)

				// Half compress, half decompress
				compressed, err := codec.Compress(testData)
				require.NoError(t, err)

				for i := 0; i < numGoroutines; i++ {
					// Compress
					go func() {
						_, err := codec.Compress(testData)
						done <- err
					}()

					// Decompress
					go func() {
						decompressed, err := codec.Decompress(compressed)
						if err != nil {
							done <- err
							return
						}
						if !bytes.Equal(testData, decompressed) {
							done <- fmt.Errorf("data mismatch")
							return
						}
						done <- nil
					}()
				}

				for range numGoroutines * 2 {
					err := <-done
					require.NoError(t, err)
				}
			})
		})
	}
}

// TestAllCodecs_InterfaceCompliance verifies that all codecs implement the Codec interface
func TestAllCodecs_InterfaceCompliance(t *testing.T) {
	codecs := getAllCodecs()

	for name, codec := range codecs {
		t.Run(name, func(t *testing.T) {
			// Verify codec implements Codec interface
			var _ Codec = codec
			require.NotNil(t, codec)
		})
	}
}

// TestAllCodecs_LargeExpansionRatio tests codecs with highly compressible data
func TestAllCodecs_LargeExpansionRatio(t *testing.T) {
	// Create highly compressible data (1MB of zeros)
	original := make([]byte, 1024*1024)

	codecs := getAllCodecs()

	for codecName, codec := range codecs {
		t.Run(codecName, func(t *testing.T) {
			// Compress
			compressed, err := codec.Compress(original)
			require.NoError(t, err)
			require.NotNil(t, compressed)

			// Log compression results
			ratio := float64(len(compressed)) / float64(len(original)) * 100
			t.Logf("Compressed %d bytes to %d bytes (%.4f%% of original)",
				len(original), len(compressed), ratio)

			// NoOp should have no compression
			if codecName == "NoOp" {
				require.Equal(t, len(original), len(compressed))
			} else {
				// Real compressors should achieve significant compression on zeros
				require.Less(t, len(compressed), len(original)/10,
					"Should compress to less than 10% of original for highly compressible data")
			}

			// Decompress and verify
			decompressed, err := codec.Decompress(compressed)
			require.NoError(t, err)
			require.Equal(t, original, decompressed)
		})
	}
}

// TestAllCodecs_ProgressiveDataSizes tests various data sizes from tiny to large
func TestAllCodecs_ProgressiveDataSizes(t *testing.T) {
	sizes := []int{
		1,       // 1 byte
		10,      // 10 bytes
		100,     // 100 bytes
		1024,    // 1 KB
		4096,    // 4 KB
		16384,   // 16 KB
		65536,   // 64 KB
		262144,  // 256 KB
		1048576, // 1 MB
	}

	codecs := getAllCodecs()

	for codecName, codec := range codecs {
		t.Run(codecName, func(t *testing.T) {
			for _, size := range sizes {
				t.Run(fmt.Sprintf("%d_bytes", size), func(t *testing.T) {
					// Create test data with pattern
					data := make([]byte, size)
					for i := range data {
						data[i] = byte(i % 256)
					}

					// Compress
					compressed, err := codec.Compress(data)
					require.NoError(t, err)

					// Decompress
					decompressed, err := codec.Decompress(compressed)
					require.NoError(t, err)
					require.Equal(t, data, decompressed)
				})
			}
		})
	}
}
