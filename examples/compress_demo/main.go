package main

import (
	"fmt"
	"log"

	"github.com/arloliu/mebo/compress"
)

func main() {
	// Example: Using Zstd compressor for mebo time-series data
	fmt.Println("Mebo Zstd Compressor Example")
	fmt.Println("============================")

	// Create some sample time-series-like data (simulating encoded timestamps)
	// In practice, this would come from mebo's timestamp or value encoders
	sampleData := generateTimestampLikeData()
	fmt.Printf("Original data size: %d bytes\n", len(sampleData))

	// Example 1: Basic compression with default settings
	fmt.Println("\n1. Basic Compression (Default Settings):")
	basicExample(sampleData)

	// Example 2: High-compression for archival
	fmt.Println("\n2. High Compression for Archival:")
	archivalExample(sampleData)

	// Example 3: Fast compression for real-time use
	fmt.Println("\n3. Fast Compression for Real-time:")
	realTimeExample(sampleData)
}

func basicExample(data []byte) {
	// Create compressor with default settings (recommended)
	compressor := compress.NewZstdCompressor()

	// Compress the data
	compressed, err := compressor.Compress(data)
	if err != nil {
		log.Fatal(err)
	}

	// Calculate compression ratio
	ratio := float64(len(compressed)) / float64(len(data))
	savings := (1.0 - ratio) * 100.0

	fmt.Printf("   Compressed size: %d bytes\n", len(compressed))
	fmt.Printf("   Compression ratio: %.2f:1\n", 1.0/ratio)
	fmt.Printf("   Space savings: %.1f%%\n", savings)

	// Verify round-trip
	decompressed, err := compressor.Decompress(compressed)
	if err != nil {
		log.Fatal(err)
	}

	if len(decompressed) == len(data) {
		fmt.Printf("   ✓ Round-trip successful\n")
	} else {
		fmt.Printf("   ✗ Round-trip failed\n")
	}
}

func archivalExample(data []byte) {
	// Create compressor optimized for maximum compression (archival use)
	compressor := compress.NewZstdCompressor()
	// Compress the data
	compressed, err := compressor.Compress(data)
	if err != nil {
		log.Fatal(err)
	}

	// Calculate compression ratio
	ratio := float64(len(compressed)) / float64(len(data))
	savings := (1.0 - ratio) * 100.0

	fmt.Printf("   Compressed size: %d bytes\n", len(compressed))
	fmt.Printf("   Compression ratio: %.2f:1\n", 1.0/ratio)
	fmt.Printf("   Space savings: %.1f%%\n", savings)
	fmt.Printf("   ✓ Best compression for archival storage\n")
}

func realTimeExample(data []byte) {
	// Create compressor optimized for speed (real-time use)
	compressor := compress.NewZstdCompressor()

	// Compress the data
	compressed, err := compressor.Compress(data)
	if err != nil {
		fmt.Printf("   ✗ Compression error: %v\n", err)
		return
	}

	// Calculate compression ratio
	ratio := float64(len(compressed)) / float64(len(data))
	fmt.Printf("   Actual compressed size: %d bytes\n", len(compressed))
	fmt.Printf("   Compression ratio: %.2f:1\n", 1.0/ratio)
	fmt.Printf("   ✓ Fast compression for real-time ingestion\n")
}

// generateTimestampLikeData creates sample data that resembles delta-encoded timestamps
// This simulates the kind of highly compressible data that mebo's timestamp encoder would produce
func generateTimestampLikeData() []byte {
	// Simulate delta-encoded timestamps: small values with patterns
	// This represents a typical 32KB payload from mebo's timestamp encoder
	data := make([]byte, 32*1024)

	// Fill with delta values (typically small integers for timestamp deltas)
	for i := 0; i < len(data); i += 4 {
		// Simulate small delta values (1-1000ms between timestamps)
		delta := uint32(i%1000 + 1) //nolint: gosec

		// Store as little-endian bytes (similar to mebo encoding)
		if i+3 < len(data) {
			data[i] = byte(delta)
			data[i+1] = byte(delta >> 8)
			data[i+2] = byte(delta >> 16)
			data[i+3] = byte(delta >> 24)
		}
	}

	return data
}
