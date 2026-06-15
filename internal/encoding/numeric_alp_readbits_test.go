package encoding

import (
	"math/rand"
	"testing"
)

// TestAlpReadBitsFast_Differential fuzzes the unaligned word-at-a-time reader
// against the naive bit-by-bit reference across every width (0..64), random bit
// offsets, and buffer tails (so the <8-byte fallback and the width-near-64 fixup
// are both exercised). The two must agree bit-for-bit.
func TestAlpReadBitsFast_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for range 200000 {
		width := rng.Intn(65) // 0..64
		// Buffer large enough to hold the read, plus a random short tail sometimes.
		nbytes := (64+width)/8 + 1 + rng.Intn(6)
		src := make([]byte, nbytes)
		for i := range src {
			src[i] = byte(rng.Intn(256))
		}
		// bitpos anywhere the width-bit field still fits in the buffer.
		maxBit := nbytes*8 - width
		if maxBit <= 0 {
			continue
		}
		bitpos := rng.Intn(maxBit)

		mask := ^uint64(0)
		if width < 64 {
			mask = (uint64(1) << uint(width)) - 1
		}
		got := alpReadBitsFast(src, bitpos, width, mask)
		want := alpReadBitsAt(src, bitpos, width)
		if got != want {
			t.Fatalf("mismatch: width=%d bitpos=%d got=%#x want=%#x", width, bitpos, got, want)
		}
	}
}
