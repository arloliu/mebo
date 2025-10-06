package compress

import "github.com/klauspost/compress/s2"

type S2Compressor struct{}

var _ Codec = (*S2Compressor)(nil)

// NewS2Compressor creates a new S2 compressor.
//
// Returns:
//   - S2Compressor: New S2 compressor instance
func NewS2Compressor() S2Compressor {
	return S2Compressor{}
}

// Compress compresses the input data using S2 compression.
//
// Parameters:
//   - data: Input data to compress
//
// Returns:
//   - []byte: Compressed data (nil if input is empty)
//   - error: Always nil (S2 compression doesn't return errors)
func (c S2Compressor) Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	return s2.Encode(nil, data), nil
}

// Decompress decompresses the input data using S2 decompression.
//
// Parameters:
//   - data: Compressed data to decompress
//
// Returns:
//   - []byte: Decompressed data (nil if input is empty)
//   - error: Decompression error if data is corrupted
func (c S2Compressor) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	return s2.Decode(nil, data)
}
