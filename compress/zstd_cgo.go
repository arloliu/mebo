//go:build nobuild

package compress

import (
	"github.com/valyala/gozstd"
)

// Compress compresses the input data using Zstandard compression.
func (c ZstdCompressor) Compress(data []byte) ([]byte, error) {
	return gozstd.CompressLevel(nil, data, 3), nil
}

func (c ZstdCompressor) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	return gozstd.Decompress(nil, data)
}
