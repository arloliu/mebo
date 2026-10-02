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

// lz4MaxExpansion and lz4ExpansionSlack bound how large a decompressed LZ4
// block can be relative to its input.
const (
	lz4MaxExpansion   = 255
	lz4ExpansionSlack = 64
)

// Decompress decompresses the input data using LZ4 decompression.
//
// This method uses an adaptive buffer sizing strategy to handle cases where
// the decompressed size is unknown:
//  1. Start with a buffer 4x the compressed size (common expansion ratio)
//  2. On ErrInvalidSourceShortBuffer, double the buffer size, up to the smaller
//     of 255x the input (the most an LZ4 block can expand) and 128MB
//  3. Return error if buffer exceeds reasonable limits (prevents memory exhaustion)
//
// Parameters:
//   - data: Compressed data to decompress
//
// Returns:
//   - []byte: Decompressed data (nil if input is empty)
//   - error: ErrInvalidSourceShortBuffer if the output would exceed those limits
//     or the input is corrupt, or other decompression errors
func (c LZ4Compressor) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	// LZ4 blocks cannot expand more than ~255x (a match length costs one byte
	// per 255 output bytes), so cap the retry budget there as well as at the
	// global limit. lz4 reports corrupt input and a short buffer with the same
	// error, so without this cap corrupt input doubled the buffer up to the
	// global limit. Sizes are computed in uint64 so they cannot wrap on 32-bit.
	limit := min(uint64(maxDecompressSize), uint64(len(data))*lz4MaxExpansion+lz4ExpansionSlack)
	bufSize := min(uint64(len(data))*4, limit)

	for {
		buf := make([]byte, bufSize)
		n, err := lz4.UncompressBlock(data, buf)
		if err == nil {
			return buf[:n], nil
		}
		if !errors.Is(err, lz4.ErrInvalidSourceShortBuffer) || bufSize >= limit {
			// Corrupt data, or output beyond what LZ4 or the global limit allows.
			return nil, err
		}
		bufSize = min(bufSize*2, limit) // Double buffer size and retry
	}
}
