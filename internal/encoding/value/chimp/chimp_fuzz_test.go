package chimp

import (
	"encoding/binary"
	"math"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

// chimpGroupFuzzHeaderSize is the fixed prefix FuzzChimpGroupDecode reads its lanes from:
// one byte for the number of lanes, then four bytes per lane.
// The rest of the input holds the lanes' bytes, one lane after the other.
const chimpGroupFuzzHeaderSize = 1 + 8*4

// chimpGroupFuzzSeed builds a FuzzChimpGroupDecode input from up to eight columns,
// the count asked of each and the number of zero bytes that follow each in its buffer.
func chimpGroupFuzzSeed(cols [][]byte, counts, tails []int) []byte {
	var header [chimpGroupFuzzHeaderSize]byte
	header[0] = byte(len(cols) - 1)
	for i, col := range cols {
		lane := header[1+i*4:]
		binary.LittleEndian.PutUint16(lane, uint16(len(col)))
		lane[2], lane[3] = byte(counts[i]), byte(tails[i])
	}
	in := header[:]
	for i, col := range cols {
		in = append(in, col...)
		in = append(in, make([]byte, tails[i])...)
	}

	return in
}

// chimpGroupFuzzSeeds returns valid groups of eight columns of 30 to 40 values, each with read-ahead:
// columns the kernel decodes whole, columns of differing counts whose last lanes the scalar loop finishes,
// and a group in which one value is too wide for the kernel.
func chimpGroupFuzzSeeds() [][]byte {
	rng := rand.New(rand.NewSource(61))
	seed := func(slot int, differ bool, edit func(lane int, col []float64)) []byte {
		cols := make([][]byte, 8)
		counts := make([]int, 8)
		tails := make([]int, 8)
		for i := range cols {
			counts[i] = 35
			if differ {
				counts[i] = 30 + rng.Intn(11)
			}
			col := generateChimpMixColumn(slot, counts[i], int64(slot*8+i))
			if edit != nil {
				edit(i, col)
			}
			cols[i] = chimpGroupEncode(col)
			tails[i] = chimpGroupReadAhead + rng.Intn(16)
		}

		return chimpGroupFuzzSeed(cols, counts, tails)
	}

	return [][]byte{
		seed(chimpGroupDenseSlot, false, nil), // full-precision gauge, equal counts
		seed(chimpGroupDenseSlot+5, true, nil),
		seed(0, true, nil),  // two-decimal gauge that holds its value
		seed(40, true, nil), // integer counter
		seed(chimpGroupDenseSlot+9, false, func(lane int, col []float64) {
			if lane == 3 {
				col[20] = math.Float64frombits(rng.Uint64() | 1<<63)
			}
		}),
	}
}

// chimpGroupFuzzCheck cuts in into up to eight lanes, decodes them as one ColumnGroup
// and compares every lane with DecodeAll: the count produced and the whole destination.
// Each lane header gives the column's length, the count asked for and 0 to 31 bytes that follow the column;
// column and following bytes are taken from the input and placed in a buffer of their own that ends with them,
// so lanes with and without the kernel's read-ahead both occur.
// It returns nil when the input is shorter than the header.
func chimpGroupFuzzCheck(tb testing.TB, in []byte) *ColumnGroup {
	tb.Helper()
	if len(in) < chimpGroupFuzzHeaderSize {
		return nil
	}
	n := 1 + int(in[0]%8)
	body := in[chimpGroupFuzzHeaderSize:]

	g := new(ColumnGroup)
	for i := range n {
		lane := in[1+i*4:]
		length := min(int(binary.LittleEndian.Uint16(lane))%1024, len(body))
		tail := min(int(lane[3]%32), len(body)-length)
		buf := make([]byte, length+tail)
		copy(buf, body)
		body = body[length+tail:]

		g.Payload[i], g.Off[i], g.Len[i], g.Count[i] = buf, 0, length, int(lane[2])
		g.Dst[i] = make([]float64, g.Count[i]+i%3)
		chimpGroupFill(g.Dst[i])
	}
	g.Decode(n)

	for i := range n {
		want := make([]float64, len(g.Dst[i]))
		chimpGroupFill(want)
		produced := NumericChimpDecoder{}.DecodeAll(g.Payload[i][:g.Len[i]], g.Count[i], want)
		if g.Produced[i] != produced {
			tb.Fatalf("lane %d: produced %d, DecodeAll %d", i, g.Produced[i], produced)
		}
		if !chimpGroupSameBits(g.Dst[i], want) {
			tb.Fatalf("lane %d: destination differs from DecodeAll (count %d, produced %d)", i, g.Count[i], produced)
		}
	}

	return g
}

// FuzzChimpGroupDecode feeds ColumnGroup.Decode arbitrary columns, counts and read-ahead,
// and requires what NumericChimpDecoder.DecodeAll gives for each column on its own.
func FuzzChimpGroupDecode(f *testing.F) {
	for _, seed := range chimpGroupFuzzSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		chimpGroupFuzzCheck(t, in)
	})
}

// TestChimpGroup_FuzzSeedsEngage requires that every seed of FuzzChimpGroupDecode runs the kernel,
// and that at least one of them leaves a lane for the scalar loop to finish,
// so the fuzzer starts from inputs that reach both.
func TestChimpGroup_FuzzSeedsEngage(t *testing.T) {
	chimpGroupRequireKernel(t)
	handedOff := 0
	for k, seed := range chimpGroupFuzzSeeds() {
		g := chimpGroupFuzzCheck(t, seed)
		require.NotNil(t, g, "seed %d", k)
		require.Equal(t, 8, g.Columns, "seed %d: columns in the kernel", k)
		require.Positive(t, g.Steps, "seed %d: kernel steps", k)
		for i := range 8 {
			// The lane entered the kernel, has every value, and got fewer of them from the kernel than it holds after the first.
			if g.Produced[i] == g.Count[i] && g.Count[i]-1 > g.Steps {
				handedOff++
			}
		}
	}
	require.Positive(t, handedOff, "no seed hands a lane to the scalar loop")
}
