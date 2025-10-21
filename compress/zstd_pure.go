//go:build !cgo

package compress

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// zstdDecoderPool pools zstd decoders for reuse to eliminate allocation overhead.
// The klauspost/compress/zstd library is explicitly designed for decoder reuse:
// "The decoder has been designed to operate without allocations after a warmup.
// This means that you should store the decoder for best performance."
var zstdDecoderPool = sync.Pool{
	New: func() any {
		decoder, err := zstd.NewReader(nil,
			zstd.WithDecoderConcurrency(1), // Single-threaded for predictable performance
			zstd.WithDecoderLowmem(false),  // Use more memory for better performance
		)
		if err != nil {
			// This should never happen with valid options
			panic(fmt.Sprintf("failed to create zstd decoder for pool: %v", err))
		}
		return decoder
	},
}

// zstdEncoderPool pools zstd encoders for reuse to eliminate allocation overhead.
var zstdEncoderPool = sync.Pool{
	New: func() any {
		encoder, err := zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.SpeedDefault),
			zstd.WithEncoderCRC(false), // Disable CRC for performance
		)
		if err != nil {
			// This should never happen with valid options
			panic(fmt.Sprintf("failed to create zstd encoder for pool: %v", err))
		}
		return encoder
	},
}

// Compress compresses the input data using Zstandard compression.
// Uses a pooled encoder for better performance (eliminates allocation overhead).
func (c ZstdCompressor) Compress(data []byte) ([]byte, error) {
	// Get encoder from pool (reuses "warmed up" encoder)
	encoder := zstdEncoderPool.Get().(*zstd.Encoder)
	defer zstdEncoderPool.Put(encoder)

	// EncodeAll is stateless - safe to use with pooled encoder
	compressed := encoder.EncodeAll(data, nil)

	return compressed, nil
}

// Decompress decompresses Zstd-compressed data.
// Uses a pooled decoder for better performance (eliminates allocation overhead).
//
// This method validates the input data format and returns an error if the
// data is corrupted or was not compressed with Zstd.
func (c ZstdCompressor) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	// Get decoder from pool (reuses "warmed up" decoder)
	decoder := zstdDecoderPool.Get().(*zstd.Decoder)
	defer zstdDecoderPool.Put(decoder)

	// DecodeAll is stateless - safe to use with pooled decoder
	// Even if this call fails, the decoder can be reused for next call
	decompressed, err := decoder.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd decompression failed: %w", err)
	}

	return decompressed, nil
}
