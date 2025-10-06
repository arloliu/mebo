package hash

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestID(t *testing.T) {
	tests := []struct {
		name string
		data string
		id   uint64
	}{
		{"empty string", "", 0xef46db3751d8e999},
		{"short string", "test", 0x4fdcca5ddb678139},
		{"long string", "this is a longer test string to hash", 0x69275f7f7ee59dbd},
		{"another string", "another test string", 0x212a22f593810bec},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.id, ID(tt.data))
		})
	}
}

func randString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, n)
	seededRand := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := range b {
		// random index
		b[i] = letters[seededRand.Intn(len(letters))]
	}

	return string(b)
}

func BenchmarkID(b *testing.B) {
	randStr := randString(20)
	b.ResetTimer()
	for b.Loop() {
		ID(randStr)
	}
}
