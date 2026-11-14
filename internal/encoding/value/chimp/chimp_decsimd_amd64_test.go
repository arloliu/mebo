//go:build linux || darwin

package chimp

import (
	"math"
	"math/rand"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// chimpGroupGuardedPayloads maps eight readable pages, each followed by a PROT_NONE page,
// so a payload that ends at its page's end has no readable byte after it.
func chimpGroupGuardedPayloads(t *testing.T) (pages [8][]byte) {
	t.Helper()
	page := syscall.Getpagesize()
	mem, err := syscall.Mmap(-1, 0, 16*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := syscall.Munmap(mem); err != nil {
			t.Errorf("munmap: %v", err)
		}
	})
	for i := range pages {
		require.NoError(t, syscall.Mprotect(mem[(2*i+1)*page:(2*i+2)*page], syscall.PROT_NONE))
		// The slice's capacity runs on into the guard page, as a payload's capacity may run past its length.
		pages[i] = mem[2*i*page : (2*i+1)*page]
	}

	return pages
}

// TestChimpGroup_GuardPage ends every lane's payload immediately before a PROT_NONE page.
// Lanes with 0, 7, 8 and 15 bytes of payload after the column must not enter the kernel, the lanes with exactly 16 must,
// and nothing may fault or differ from DecodeAll.
// Every lane asks for more values than its column holds, so the kernel walks the shortest lane to its last bit;
// each lane takes its turn as the shortest, with a column whose values end on its last bit,
// where the kernel's window load starts at the first byte after the column and needs all 16 bytes.
func TestChimpGroup_GuardPage(t *testing.T) {
	chimpGroupRequireKernel(t)
	pages := chimpGroupGuardedPayloads(t)
	tails := [8]int{16, 0, 16, 7, 16, 8, 16, 15}
	const short, long, extra = 30, 38, 8

	// endsOnLastBit returns a dense column of n values that DecodeAll, asked for more, walks to its very last bit.
	endsOnLastBit := func(lane, n int) []byte {
		for seed := int64(1); ; seed++ {
			column := chimpGroupEncode(generateChimpMixColumn(chimpGroupDenseSlot+lane, n, seed))
			trace := chimpGroupTrace(column, math.MaxInt32)
			if chimpGroupWide(column, math.MaxInt32) == 0 && trace[len(trace)-1].bitPos == len(column)*8 {
				return column
			}
		}
	}

	rng := rand.New(rand.NewSource(53))
	for shortest := range 8 {
		var g ColumnGroup
		var want [8][]float64
		var produced [8]int
		kernelSteps := math.MaxInt
		for i := range 8 {
			n := long
			if i == shortest {
				n = short
			}
			column := endsOnLastBit(i, n)
			payload := pages[i][len(pages[i])-len(column)-tails[i]:]
			_, _ = rng.Read(payload)
			copy(payload, column)

			g.Payload[i], g.Off[i], g.Len[i], g.Count[i] = payload, 0, len(column), n+extra
			g.Dst[i] = make([]float64, n+extra+1)
			chimpGroupFill(g.Dst[i])
			want[i] = make([]float64, n+extra+1)
			chimpGroupFill(want[i])
			produced[i] = NumericChimpDecoder{}.DecodeAll(column, n+extra, want[i])
			require.GreaterOrEqual(t, produced[i], n)
			require.Less(t, produced[i], n+extra, "lane %d: the column holds what it is asked for", i)
			if tails[i] == chimpGroupReadAhead {
				kernelSteps = min(kernelSteps, produced[i]-1)
			}
		}

		g.Decode(8)
		for i := range 8 {
			require.Equal(t, produced[i], g.Produced[i], "shortest %d: lane %d", shortest, i)
			require.True(t, chimpGroupSameBits(want[i], g.Dst[i]), "shortest %d: lane %d differs from DecodeAll", shortest, i)
		}
		// Only the four lanes with 16 bytes enter, and the kernel stops where the first of them ends.
		require.Equal(t, 4, g.Columns, "shortest %d: columns in the kernel", shortest)
		require.Equal(t, kernelSteps, g.Steps, "shortest %d: kernel steps", shortest)
		require.Equal(t, 1, g.Calls, "shortest %d: kernel calls", shortest)
	}
}

// TestChimpGroupDecodeAVX512_GODEBUG runs child processes with CPU features masked off.
// The kernel needs AVX-512 DQ and BW, so it must lose eligibility without either.
func TestChimpGroupDecodeAVX512_GODEBUG(t *testing.T) {
	if child := os.Getenv("MEBO_CHIMP_GROUP_CHILD"); child != "" {
		require.False(t, GroupDecodeEnabled(), "group decode kernel enabled without %s", child)

		return
	}
	chimpGroupRequireKernel(t)
	for child, godebug := range map[string]string{"DQ": "cpu.avx512dq=off", "BW": "cpu.avx512bw=off"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestChimpGroupDecodeAVX512_GODEBUG$", "-test.count=1")
		cmd.Env = append(os.Environ(), "MEBO_CHIMP_GROUP_CHILD="+child, "GODEBUG="+godebug)
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "child process with GODEBUG=%s:\n%s", godebug, out)
	}
}
