package encoding

import (
	"testing"

	"github.com/arloliu/mebo/endian"
)

// Decode regression guards for the streaming bit reader (alpReadBitsFast). They
// cover both schemes via genALPColumns: 2dp decimals exercise ALP-main, full
// precision exercises ALP-RD (two bit-streams per value). Decode is zero-alloc.

func benchALPDecodeIterate(b *testing.B, cols [][]float64) {
	b.Helper()
	eng := endian.GetLittleEndianEngine()
	blobs := make([][]byte, len(cols))
	counts := make([]int, len(cols))
	for i, col := range cols {
		enc := NewNumericALPEncoder(eng)
		enc.WriteSlice(col)
		blobs[i] = append([]byte(nil), enc.Bytes()...)
		counts[i] = len(col)
		enc.Finish()
	}
	dec := NewNumericALPDecoder(eng)
	b.ReportAllocs()
	b.ResetTimer()
	var sink float64
	for b.Loop() {
		for i, blob := range blobs {
			for v := range dec.All(blob, counts[i]) {
				sink += v
			}
		}
	}
	if sink == -1 {
		b.Fatal("unreachable")
	}
}

func benchALPDecodeAll(b *testing.B, cols [][]float64) {
	b.Helper()
	eng := endian.GetLittleEndianEngine()
	blobs := make([][]byte, len(cols))
	counts := make([]int, len(cols))
	maxLen := 0
	for i, col := range cols {
		enc := NewNumericALPEncoder(eng)
		enc.WriteSlice(col)
		blobs[i] = append([]byte(nil), enc.Bytes()...)
		counts[i] = len(col)
		if len(col) > maxLen {
			maxLen = len(col)
		}
		enc.Finish()
	}
	dst := make([]float64, maxLen)
	dec := NewNumericALPDecoder(eng)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for i, blob := range blobs {
			dec.DecodeAll(blob, counts[i], dst)
		}
	}
}

func BenchmarkALPDecodeIterate_Decimal2dp(b *testing.B) {
	benchALPDecodeIterate(b, genALPColumns(100, 1000, 2, 42)) // ALP-main path
}

func BenchmarkALPDecodeIterate_FullPrecision(b *testing.B) {
	benchALPDecodeIterate(b, genALPColumns(100, 1000, -1, 42)) // ALP-RD path
}

func BenchmarkALPDecodeAll_Decimal2dp(b *testing.B) {
	benchALPDecodeAll(b, genALPColumns(100, 1000, 2, 42))
}

func BenchmarkALPDecodeAll_FullPrecision(b *testing.B) {
	benchALPDecodeAll(b, genALPColumns(100, 1000, -1, 42))
}
