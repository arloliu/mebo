//go:build linux || darwin

package alp

import (
	"math"
	"math/rand"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/internal/arch"
)

// TestALPMainStatsKernel_Direct compares the kernel with alpMainStatsScalar for every n from 1 to 17
// (every tail residue, below the dispatch threshold too) and all 190 (e, f),
// with canaries on both sides of dst.
func TestALPMainStatsKernel_Direct(t *testing.T) {
	alpRequireAVX512(t, arch.X86HasAVX512DQ(), "AVX-512DQ")
	const canary = 0xDEADBEEFCAFEF00D
	rng := rand.New(rand.NewSource(0x51A75))
	for n := 1; n <= 17; n++ {
		for ci, col := range alpKernelTestColumns(rng, n) {
			for e := 0; e <= alpMaxExponent; e++ {
				for f := 0; f <= e; f++ {
					want := make([]uint64, n+16)
					got := make([]uint64, n+16)
					for i := range want {
						want[i], got[i] = canary, canary
					}
					wc, wexc := alpMainStatsScalar(col, e, f, want[8:8+n], nil)
					gc, gexc := alpMainStatsKernel(col, e, f, got[8:8+n], nil)
					require.Equalf(t, wc, gc, "n=%d col=%d (e,f)=(%d,%d): cand", n, ci, e, f)
					require.Equalf(t, wexc, gexc, "n=%d col=%d (e,f)=(%d,%d): excPos", n, ci, e, f)
					require.Equalf(t, want, got, "n=%d col=%d (e,f)=(%d,%d): dst and canaries", n, ci, e, f)
				}
			}
		}
	}
}

// TestALPMainStatsKernel_Boundaries compares the kernel with alpMainStatsScalar on the complete boundary
// and special-value collections for all 190 (e, f), with canaries on both sides of dst.
func TestALPMainStatsKernel_Boundaries(t *testing.T) {
	alpRequireAVX512(t, arch.X86HasAVX512DQ(), "AVX-512DQ")
	const canary = 0x0123456789ABCDEF
	for name, col := range map[string][]float64{"boundaries": alpIdentBoundaries(), "specials": alpIdentSpecials()} {
		n := len(col)
		for e := 0; e <= alpMaxExponent; e++ {
			for f := 0; f <= e; f++ {
				want := make([]uint64, n+16)
				got := make([]uint64, n+16)
				for i := range want {
					want[i], got[i] = canary, canary
				}
				wc, wexc := alpMainStatsScalar(col, e, f, want[8:8+n], nil)
				gc, gexc := alpMainStatsKernel(col, e, f, got[8:8+n], nil)
				require.Equalf(t, wc, gc, "%s (e,f)=(%d,%d): cand", name, e, f)
				require.Equalf(t, wexc, gexc, "%s (e,f)=(%d,%d): excPos", name, e, f)
				require.Equalf(t, want, got, "%s (e,f)=(%d,%d): dst and canaries", name, e, f)
			}
		}
	}
}

// TestALPMainStatsKernel_BlockMasks calls the kernel itself.
// blockMask must hold exactly the exception lanes, no lane at or past n may be set in the masks or the returned OR,
// and the words on either side of the mask buffer must stay untouched.
func TestALPMainStatsKernel_BlockMasks(t *testing.T) {
	alpRequireAVX512(t, arch.X86HasAVX512DQ(), "AVX-512DQ")
	const canary = 0xA5A5A5A5A5A5A5A5
	rng := rand.New(rand.NewSource(0xB10C))
	for n := 1; n <= 40; n++ {
		for _, col := range alpKernelTestColumns(rng, n) {
			for _, ef := range [][2]int{{0, 0}, {2, 0}, {14, 12}, {18, 0}, {18, 18}} {
				e, f := ef[0], ef[1]
				nBlock := (n + 7) >> 3
				buf := make([]uint64, nBlock+4) // two canary words on each side
				for i := range buf {
					buf[i] = canary
				}
				masks := buf[2 : 2+nBlock]
				dst := make([]uint64, n)
				factors := [4]float64{alpPow10[e], alpInvPow10[f], alpPow10[f], alpInvPow10[e]}
				var mnmx [2]int64
				or := alpMainStatsAVX512(&col[0], n, &factors, &dst[0], &masks[0], &mnmx)

				var wantOr uint64
				for g := range nBlock {
					var want uint64
					for j := range 8 {
						i := g*8 + j
						if i >= n {
							break
						}
						if _, good := alpEncodeDigit(col[i], e, f); !good {
							want |= 1 << j
						}
					}
					require.Equalf(t, want, masks[g], "n=%d (e,f)=(%d,%d) block %d", n, e, f, g)
					wantOr |= want
				}
				require.Equalf(t, wantOr, or, "n=%d (e,f)=(%d,%d): returned OR", n, e, f)
				require.Equalf(t, []uint64{canary, canary}, buf[:2], "n=%d: words before the first block", n)
				require.Equalf(t, []uint64{canary, canary}, buf[2+nBlock:], "n=%d: words after the last block", n)
			}
		}
	}
}

// TestALPMainStatsKernel_GuardPage places the last value, and the last digit slot,
// immediately before a PROT_NONE page for every n from 1 to 130 (whole blocks and every tail):
// the masked tail load and store must not touch it.
func TestALPMainStatsKernel_GuardPage(t *testing.T) {
	alpRequireAVX512(t, arch.X86HasAVX512DQ(), "AVX-512DQ")
	page := syscall.Getpagesize()
	mem, err := syscall.Mmap(-1, 0, 4*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := syscall.Munmap(mem); err != nil {
			t.Errorf("munmap: %v", err)
		}
	})
	// Pages 1 and 3 are guards; values end at page 1, digits end at page 3.
	require.NoError(t, syscall.Mprotect(mem[page:2*page], syscall.PROT_NONE))
	require.NoError(t, syscall.Mprotect(mem[3*page:], syscall.PROT_NONE))

	rng := rand.New(rand.NewSource(0x6A4D))
	for n := 1; n <= 130; n++ {
		valBytes := mem[page-8*n : page]
		dstBytes := mem[3*page-8*n : 3*page]
		values := unsafe.Slice((*float64)(unsafe.Pointer(&valBytes[0])), n)
		dst := unsafe.Slice((*uint64)(unsafe.Pointer(&dstBytes[0])), n)
		for _, col := range alpKernelTestColumns(rng, n) {
			copy(values, col)
			for _, ef := range [][2]int{{0, 0}, {2, 0}, {14, 12}} {
				want := make([]uint64, n)
				copy(want, dst)
				wc, wexc := alpMainStatsScalar(values, ef[0], ef[1], want, nil)
				gc, gexc := alpMainStatsKernel(values, ef[0], ef[1], dst, nil)
				require.Equalf(t, wc, gc, "n=%d (e,f)=%v: cand", n, ef)
				require.Equalf(t, wexc, gexc, "n=%d (e,f)=%v: excPos", n, ef)
				require.Equalf(t, want, append([]uint64(nil), dst...), "n=%d (e,f)=%v: dst", n, ef)
			}
		}
	}
}

// TestALPMainStatsKernel_ShortDst checks that a dst shorter than values panics before the kernel runs,
// instead of letting the kernel write past it.
func TestALPMainStatsKernel_ShortDst(t *testing.T) {
	alpRequireAVX512(t, arch.X86HasAVX512DQ(), "AVX-512DQ")
	values := make([]float64, 16)
	buf := make([]uint64, 16)
	require.Panics(t, func() { alpMainStatsKernel(values, 2, 0, buf[:15:15], nil) })
	require.Equal(t, make([]uint64, 16), buf, "nothing written")
}

// TestALPEFSearchKernel_RejectsBadNS calls the search kernel with sample sizes outside 1..63:
// it must return -1 without reading the sample or writing its frame.
func TestALPEFSearchKernel_RejectsBadNS(t *testing.T) {
	alpRequireAVX512(t, alpEFKernelRuns(), "the AVX-512 search kernel")
	var sample [64]float64
	for _, ns := range []int{math.MinInt64, -1, 0, 64, 65, 1 << 40, math.MaxInt64} {
		require.Equalf(t, -1, alpEFSearchAVX512(&sample, ns, &alpEFFactors, -1), "ns=%d", ns)
	}
}

// TestALPEFSearchKernel_Poison calls the search kernel with the lanes past the last vector poisoned:
// they must not change the result, for the plain search and for every seed.
// (Poisoning detects observable contamination; the kernel cannot read past its 512-byte array anyway.)
func TestALPEFSearchKernel_Poison(t *testing.T) {
	alpRequireAVX512(t, alpEFKernelRuns(), "the AVX-512 search kernel")
	rng := rand.New(rand.NewSource(0x9015))
	poison := []float64{0, 1, 123.45, math.Inf(1), 1 << 51, 9.2e18}
	for ns := 1; ns <= 63; ns++ {
		col := alpIdentGauge(2, 0, 0.005).column(rng, ns, 100)
		var clean, dirty [64]float64
		copy(clean[:], col)
		copy(dirty[:], col)
		for k := ns; k < (ns+7)&^7; k++ {
			clean[k], dirty[k] = math.NaN(), math.NaN()
		}
		for k := (ns + 7) &^ 7; k < 64; k++ {
			clean[k] = math.NaN()
			dirty[k] = poison[rng.Intn(len(poison))]
		}
		for seed := -1; seed < alpEFCandidates; seed++ {
			want := alpEFSearchAVX512(&clean, ns, &alpEFFactors, seed)
			got := alpEFSearchAVX512(&dirty, ns, &alpEFFactors, seed)
			require.Equalf(t, want, got, "ns=%d seed=%d", ns, seed)
		}
	}
}

// TestALPEFSearchKernel_RequiresPOPCNT runs a child process with POPCNT masked off:
// the search must decline the kernel there, while the stats kernel, which does not need POPCNT, stays eligible.
func TestALPEFSearchKernel_RequiresPOPCNT(t *testing.T) {
	if os.Getenv("MEBO_ALP_POPCNT_CHILD") == "1" {
		_, _, ok := alpBestEFSIMD([]float64{1.5, 2.5, 3.5}, 1, -1)
		require.False(t, ok, "search kernel ran without POPCNT")
		require.True(t, alpEncAVX512, "stats kernel lost eligibility")

		return
	}
	alpRequireAVX512(t, alpEFKernelRuns(), "the AVX-512 search kernel")
	cmd := exec.Command(os.Args[0], "-test.run=^TestALPEFSearchKernel_RequiresPOPCNT$", "-test.count=1")
	cmd.Env = append(os.Environ(), "MEBO_ALP_POPCNT_CHILD=1", "GODEBUG=cpu.popcnt=off")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "child process:\n%s", out)
}

// alpKernelTestColumns returns short columns for the direct kernel tests:
// boundary values, special values, decimals, all-exception columns,
// and columns whose only good value sits in the last valid lane.
func alpKernelTestColumns(rng *rand.Rand, n int) [][]float64 {
	specials := alpIdentSpecials()
	bnd := alpIdentBoundaries()
	pick := func(src []float64) []float64 {
		c := make([]float64, n)
		for i := range c {
			c[i] = src[rng.Intn(len(src))]
		}

		return c
	}
	decimals := alpIdentGauge(2, 0, 0.005).column(rng, n, 100)
	allExc := make([]float64, n)
	for i := range allExc {
		allExc[i] = math.Float64frombits(0x7ff8000000000000 | rng.Uint64()>>13) // NaN payloads
	}
	lastGood := append([]float64(nil), allExc...)
	lastGood[n-1] = 12.5

	return [][]float64{pick(specials), pick(bnd), decimals, allExc, lastGood}
}
