package compress

import (
	"errors"
	"sync"

	"github.com/pierrec/lz4/v4"
)

// lz4CompressorPool pools lz4.Compressor instances for reuse.
// The lz4.Compressor maintains internal state that benefits from reuse.
var lz4CompressorPool = sync.Pool{
	New: func() any {
		return &lz4.Compressor{}
	},
}

type LZ4Compressor struct{}

var _ Codec = (*LZ4Compressor)(nil)

// NewLZ4Compressor creates a new LZ4 compressor.
//
// Returns:
//   - LZ4Compressor: New LZ4 compressor instance
func NewLZ4Compressor() LZ4Compressor {
	return LZ4Compressor{}
}

// Compress compresses the input data using LZ4 compression.
//
// Uses a pooled lz4.Compressor for better performance.
//
// Parameters:
//   - data: Input data to compress
//
// Returns:
//   - []byte: Compressed data (nil if input is empty)
//   - error: Compression error if any
func (c LZ4Compressor) Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	dstSize := lz4.CompressBlockBound(len(data))
	dst := make([]byte, dstSize)

	// Get compressor from pool
	lc, _ := lz4CompressorPool.Get().(*lz4.Compressor)
	defer lz4CompressorPool.Put(lc)

	n, err := lc.CompressBlock(data, dst)
	if err != nil {
		return nil, err
	}

	return dst[:n], nil
}

// Decompress decompresses the input data using LZ4 decompression.
//
// This method uses an adaptive buffer sizing strategy to handle cases where
// the decompressed size is unknown:
//  1. Start with a buffer 4x the compressed size (common expansion ratio)
//  2. On ErrInvalidSourceShortBuffer, double the buffer size (up to maxSize)
//  3. Return error if buffer exceeds reasonable limits (prevents memory exhaustion)
//
// Parameters:
//   - data: Compressed data to decompress
//
// Returns:
//   - []byte: Decompressed data (nil if input is empty)
//   - error: ErrInvalidSourceShortBuffer if buffer exceeded 128MB limit, or other decompression errors
func (c LZ4Compressor) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	bufSize := len(data) * 4
	const maxSize = 128 * 1024 * 1024 // 128MB safety limit

	for bufSize <= maxSize {
		buf := make([]byte, bufSize)
		n, err := lz4.UncompressBlock(data, buf)
		if err != nil {
			if errors.Is(err, lz4.ErrInvalidSourceShortBuffer) && bufSize < maxSize {
				bufSize *= 2 // Double buffer size and retry
				continue
			}

			return nil, err
		}

		return buf[:n], nil
	}

	// Buffer exceeded maxSize - likely corrupted data or unreasonable compression ratio
	return nil, lz4.ErrInvalidSourceShortBuffer
}
