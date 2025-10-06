package compress

import "github.com/klauspost/compress/s2"

type S2Compressor struct{}

var _ Codec = (*S2Compressor)(nil)

// NewS2Compressor creates a new S2 compressor with the specified options.
func NewS2Compressor() S2Compressor {
	return S2Compressor{}
}

// Compress compresses the input data using S2 compression.
func (c S2Compressor) Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	return s2.Encode(nil, data), nil
}

// Decompress decompresses the input data using S2 decompression.
func (c S2Compressor) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	return s2.Decode(nil, data)
}
