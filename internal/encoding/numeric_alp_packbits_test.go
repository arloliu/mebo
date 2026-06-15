package encoding

import (
	"math"
	"math/bits"
	"math/rand"
	"testing"

	"github.com/arloliu/mebo/endian"
)

// ---- reference implementations of the pre-optimization algorithms ----
// These pin the BEHAVIOR the optimized versions must preserve byte-for-byte.

// alpPackBitsRef is the original naive bit-by-bit packer.
func alpPackBitsRef(dst []byte, codes []uint64, width int) []byte {
	if width == 0 {
		return dst
	}
	start := len(dst)
	nbytes := (len(codes)*width + 7) / 8
	dst = append(dst, make([]byte, nbytes)...)
	bitpos := 0
	for _, c := range codes {
		for b := range width {
			if c&(uint64(1)<<uint(b)) != 0 {
				dst[start+(bitpos>>3)] |= 1 << uint(bitpos&7)
			}
			bitpos++
		}
	}

	return dst
}

// alpBestEFRef is the original exhaustive (non-pruned) (e,f) search.
func alpBestEFRef(values []float64, stride int) (bestE, bestF int) {
	best := math.MaxFloat64
	for e := 0; e <= alpMaxExponent; e++ {
		for f := 0; f <= e; f++ {
			var nExc, cnt int
			mn := int64(math.MaxInt64)
			mx := int64(math.MinInt64)
			for i := 0; i < len(values); i += stride {
				cnt++
				v := values[i]
				r := math.Round(v * alpPow10[e] * alpInvPow10[f])
				if math.Abs(r) >= 9.2e18 {
					nExc++

					continue
				}
				d := int64(r)
				if float64(d)*alpPow10[f]*alpInvPow10[e] != v {
					nExc++

					continue
				}
				if d < mn {
					mn = d
				}
				if d > mx {
					mx = d
				}
			}
			width := 0
			if nExc < cnt && mx >= mn {
				width = bits.Len64(uint64(mx - mn))
			}
			est := float64(cnt*width + nExc*96)
			if est < best {
				best = est
				bestE, bestF = e, f
			}
		}
	}

	return bestE, bestF
}

// TestAlpPackBits_Differential fuzzes the optimized word-at-a-time packer against
// the naive bit-by-bit reference across random widths, lengths, and code values.
func TestAlpPackBits_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for range 20000 {
		width := rng.Intn(65) // 0..64
		n := rng.Intn(40)
		codes := make([]uint64, n)
		for i := range codes {
			codes[i] = rng.Uint64()
		}
		// Random non-empty prefix so packing starts at a non-zero offset too.
		prefix := make([]byte, rng.Intn(5))
		for i := range prefix {
			prefix[i] = byte(rng.Intn(256))
		}
		got := alpPackBits(append([]byte(nil), prefix...), codes, width)
		want := alpPackBitsRef(append([]byte(nil), prefix...), codes, width)
		if string(got) != string(want) {
			t.Fatalf("alpPackBits mismatch: width=%d n=%d\n got=%x\nwant=%x", width, n, got, want)
		}
	}
}

// TestAlpBestEF_Differential fuzzes the pruned (e,f) search against the exhaustive
// reference across several value distributions; selection must be identical.
func TestAlpBestEF_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	gens := []func() float64{
		func() float64 { return math.Round(rng.Float64()*1000) / 100 }, // 2dp
		func() float64 { return math.Round(rng.Float64()*1e6) / 1e4 },  // 4dp
		func() float64 { return float64(rng.Intn(1 << 20)) },           // integers
		func() float64 { return rng.NormFloat64() * 1e6 },              // full precision
		func() float64 { return rng.NormFloat64() * 1e-9 },             // tiny
		func() float64 { return math.Round(rng.Float64() * 10) },       // 0dp
	}
	for range 4000 {
		n := 1 + rng.Intn(300)
		col := make([]float64, n)
		g := gens[rng.Intn(len(gens))]
		for i := range col {
			col[i] = g()
		}
		// occasionally inject a constant or sign mix
		if rng.Intn(4) == 0 {
			for i := range col {
				col[i] = math.Copysign(col[i], float64(1-2*(i&1)))
			}
		}
		stride := alpSampleStride(n)
		ge, gf := alpBestEF(col, stride)
		we, wf := alpBestEFRef(col, stride)
		if ge != we || gf != wf {
			t.Fatalf("alpBestEF mismatch n=%d: got (%d,%d) want (%d,%d)", n, ge, gf, we, wf)
		}
	}
}

// ---- encode benchmarks (regression guards for the optimization) ----

// genALPColumns mimics measurev2's gauge generator: ±0.5% random walk from 100,
// quantized to `decimals` places (decimals<0 = full precision).
func genALPColumns(nCols, nPts, decimals int, seed int64) [][]float64 { //nolint:unparam // nCols kept explicit to mirror measurev2's 100-column profile
	rng := rand.New(rand.NewSource(seed))
	scale := math.Pow(10, float64(decimals))
	cols := make([][]float64, nCols)
	for c := range cols {
		col := make([]float64, nPts)
		cur := 100.0 + float64(c)*10.0
		for j := range col {
			cur += cur * (rng.Float64()*2.0 - 1.0) * 0.005
			if decimals >= 0 {
				col[j] = math.Round(cur*scale) / scale
			} else {
				col[j] = cur
			}
		}
		cols[c] = col
	}

	return cols
}

func benchALPEncodeColumns(b *testing.B, cols [][]float64) {
	b.Helper()
	eng := endian.GetLittleEndianEngine()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, col := range cols {
			enc := NewNumericALPEncoder(eng)
			enc.WriteSlice(col)
			_ = enc.Bytes()
			enc.Finish()
		}
	}
}

func BenchmarkALPEncode_Decimal2dp(b *testing.B) {
	benchALPEncodeColumns(b, genALPColumns(100, 1000, 2, 42)) // ALP-main fast path
}

func BenchmarkALPEncode_FullPrecision(b *testing.B) {
	benchALPEncodeColumns(b, genALPColumns(100, 1000, -1, 42)) // ALP-RD path
}
