//go:build linux || darwin

package alp

import (
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/endian"
)

// alpDecGuardedCodes returns a page-aligned mapping whose page 1 is PROT_NONE,
// so a slice ending at page boundary 1 has no readable byte after it.
func alpDecGuardedCodes(tb testing.TB) (mem []byte, page int) {
	tb.Helper()
	page = syscall.Getpagesize()
	mem, err := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	require.NoError(tb, err)
	tb.Cleanup(func() {
		if err := syscall.Munmap(mem); err != nil {
			tb.Errorf("munmap: %v", err)
		}
	})
	require.NoError(tb, syscall.Mprotect(mem[page:], syscall.PROT_NONE))

	return mem, page
}

// TestALPFusedDecodeAVX512_GuardPage ends the packed codes immediately before a PROT_NONE page,
// for every width and for counts covering whole groups and every tail.
// The kernel must not fault or change the result,
// and must decode every whole group whose 64-byte load span stays off that page, and no other.
// DecodeAll on a column ending at the same place, where the generated kernels take the groups the kernel left,
// must match DecodeAll with the kernel switched off.
func TestALPFusedDecodeAVX512_GuardPage(t *testing.T) {
	alpRequireAVX512(t, alpDecAVX512, "the AVX-512 decode kernel")
	mem, page := alpDecGuardedCodes(t)
	dec := NewNumericALPDecoder(endian.GetLittleEndianEngine())

	rng := rand.New(rand.NewSource(0x9A6E))
	var mn int64 = -7654321
	pf, ie := alpPow10[3], alpInvPow10[1]
	for w := 1; w <= 64; w++ {
		mask := maskW(w)
		for _, n := range []int{8, 9, 15, 16, 23, 64, 150, 151, 255} {
			codes := make([]uint64, n)
			for i := range codes {
				codes[i] = rng.Uint64() & mask
			}
			packed := alpPackBits(nil, codes, w)
			buf := mem[page-len(packed) : page]
			copy(buf, packed)

			got := make([]float64, n)
			want := make([]float64, n)
			nk := alpFusedDecodeAVX512(buf, n, w, mn, pf, ie, got)
			if g := nk / 8; g > 0 {
				require.LessOrEqualf(t, (g-1)*w+64, len(buf), "w=%d n=%d: group %d's load span reaches the guard page", w, n, g-1)
			}
			if alpDecPermFits(w) && nk < n&^7 {
				require.Greaterf(t, (nk/8)*w+64, len(buf), "w=%d n=%d: group %d left to the scalar loop", w, n, nk/8)
			}
			alpDecodeTailScalar(buf, nk, n, w, mn, pf, ie, got)
			alpDecodeCodesScalarRef(buf, n, w, mn, pf, ie, want)
			for i := range n {
				if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
					t.Fatalf("w=%d n=%d idx=%d: got %#016x want %#016x",
						w, n, i, math.Float64bits(got[i]), math.Float64bits(want[i]))
				}
			}

			// The same codes as an exception-free ALP-main column (e=3, f=1).
			column := []byte{alpSchemeMain, 3, 1, byte(w), 0, 0, 0, 0}
			column = binary.LittleEndian.AppendUint64(column, uint64(mn))
			column = append(column, packed...)
			buf = mem[page-len(column) : page]
			copy(buf, column)
			require.Equal(t, n, dec.DecodeAll(buf, n, got))
			alpDecAVX512 = false
			require.Equal(t, n, dec.DecodeAll(buf, n, want))
			alpDecAVX512 = true
			for i := range n {
				if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
					t.Fatalf("DecodeAll w=%d n=%d idx=%d: got %#016x want %#016x",
						w, n, i, math.Float64bits(got[i]), math.Float64bits(want[i]))
				}
			}
		}
	}
}

// TestALPFusedDecodeAVX512_GODEBUG runs child processes with CPU features masked off.
// With VBMI off the decode kernel must lose eligibility, while the encode kernels, which only need DQ, keep it;
// with DQ off, the documented switch, every AVX-512 kernel must lose it.
func TestALPFusedDecodeAVX512_GODEBUG(t *testing.T) {
	switch os.Getenv("MEBO_ALP_VBMI_CHILD") {
	case "vbmi":
		require.False(t, alpDecAVX512, "decode kernel enabled without VBMI")
		require.True(t, alpEncAVX512, "encode kernels lost eligibility")

		return
	case "dq":
		require.False(t, alpDecAVX512, "decode kernel enabled without DQ")
		require.False(t, alpEncAVX512, "encode kernels enabled without DQ")

		return
	}
	alpRequireAVX512(t, alpDecAVX512, "the AVX-512 decode kernel")
	for child, godebug := range map[string]string{"vbmi": "cpu.avx512vbmi=off", "dq": "cpu.avx512dq=off"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestALPFusedDecodeAVX512_GODEBUG$", "-test.count=1")
		cmd.Env = append(os.Environ(), "MEBO_ALP_VBMI_CHILD="+child, "GODEBUG="+godebug)
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "child process with GODEBUG=%s:\n%s", godebug, out)
	}
}

// BenchmarkALPDecodeAll_PageEnd decodes a 150-point ALP-main column
// that ends mid-page and one that ends right before a PROT_NONE page,
// where the AVX-512 kernel leaves the groups whose load spans would reach that page to the generated kernels.
func BenchmarkALPDecodeAll_PageEnd(b *testing.B) {
	mem, page := alpDecGuardedCodes(b)
	dec := NewNumericALPDecoder(endian.GetLittleEndianEngine())
	dst := make([]float64, 150)
	for dp := 1; dp <= 3; dp++ {
		// The first exception-free column, so its codes end the column.
		var column []byte
		for seed := int64(1); column == nil; seed++ {
			enc := NewNumericALPEncoder(endian.GetLittleEndianEngine())
			enc.WriteSlice(genALPColumns(1, 150, dp, seed)[0])
			if c := enc.Bytes(); c[0] == alpSchemeMain && binary.LittleEndian.Uint32(c[4:8]) == 0 {
				column = append([]byte(nil), c...)
			}
			enc.Finish()
		}
		for _, c := range []struct {
			name string
			end  int
		}{{"mid-page", page / 2}, {"page-end", page}} {
			buf := mem[c.end-len(column) : c.end]
			copy(buf, column)
			b.Run("dp="+strconv.Itoa(dp)+"/"+c.name, func(b *testing.B) {
				for b.Loop() {
					dec.DecodeAll(buf, 150, dst)
				}
			})
		}
	}
}
