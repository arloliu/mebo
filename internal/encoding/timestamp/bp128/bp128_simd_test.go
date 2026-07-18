//go:build amd64

package bp128

import (
	"strconv"
	"testing"

	"github.com/arloliu/mebo/internal/arch"
	"github.com/stretchr/testify/require"
)

func TestBP128AVX512_DifferentialAllWidths(t *testing.T) {
	if !arch.X86HasAVX512() {
		t.Skip("AVX-512 unavailable")
	}

	seeds := []uint64{
		1,
		0x9e3779b97f4a7c15,
		0xd1b54a32d192ed03,
		0x94d049bb133111eb,
	}
	for w := 0; w <= 64; w++ {
		t.Run("w="+strconv.Itoa(w), func(t *testing.T) {
			for _, seed := range seeds {
				block := makeWidthBlock(w, seed)
				wantPacked := bp128PackBlockScalar(make([]uint64, 0, bp128WordsPerBlock(w)), block, w)

				scratchLen := max(1, bp128WordsPerBlock(w))
				gotPackedScratch := make([]uint64, scratchLen)
				gotWords := bp128PackBlockAVX512(&gotPackedScratch[0], &block[0], w)
				gotPacked := gotPackedScratch[:gotWords]

				require.Equalf(t, bp128WordsPerBlock(w), gotWords, "w=%d seed=%d word count", w, seed)
				require.Equalf(t, wantPacked, gotPacked, "w=%d seed=%d packed words", w, seed)

				wantDst := make([]uint64, bp128Block)
				wantUsed := bp128UnpackBlockScalar(wantDst, wantPacked, w)

				srcWords := wantPacked
				if len(srcWords) == 0 {
					srcWords = []uint64{0}
				}
				gotDst := make([]uint64, bp128Block)
				bp128UnpackBlockAVX512(&gotDst[0], &srcWords[0], w)

				require.Equalf(t, bp128WordsPerBlock(w), wantUsed, "w=%d seed=%d scalar consumed words", w, seed)
				require.Equalf(t, wantDst, gotDst, "w=%d seed=%d unpacked words", w, seed)
				require.Equalf(t, block, gotDst, "w=%d seed=%d round trip", w, seed)
			}
		})
	}
}
