package alp

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
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

// BenchmarkALPDecodeKernelWidths isolates the AVX-512 decode kernel's wire/park
// decision: for a spread of code widths it times the fused kernel against the
// generated scalar kernel (numeric_alp_kernels_gen.go) that decodeMainInto
// currently dispatches to, over the same N=1024 whole-group column. The
// per-width asm/scalar ratio (and its geomean) is what the 1.5x wire gate is
// measured against — the high-level BenchmarkALPDecode* benchmarks only cover
// the one or two widths a corpus happens to produce.
func BenchmarkALPDecodeKernelWidths(b *testing.B) {
	const n = 1024
	widths := []int{8, 12, 16, 20, 24, 32, 40, 48, 56, 64}
	rng := rand.New(rand.NewSource(424242))
	mn := int64(-98765)
	pf, ie := alpPow10[2], alpInvPow10[0]

	for _, w := range widths {
		mask := maskW(w)
		codes := make([]uint64, n)
		for i := range codes {
			codes[i] = rng.Uint64() & mask
		}
		packed := alpPackBits(nil, codes, w)
		buf := make([]byte, len(packed)+((7*w)>>3)+24)
		copy(buf, packed)
		dst := make([]float64, n)
		kern := alpFusedUnpack[w]

		ws := strconv.Itoa(w)
		b.Run("w="+ws+"/scalar", func(b *testing.B) {
			for b.Loop() {
				kern(buf, n, mn, pf, ie, dst)
			}
		})
		b.Run("w="+ws+"/asm", func(b *testing.B) {
			for b.Loop() {
				alpFusedDecodeAVX512(buf, n, w, mn, pf, ie, dst)
			}
		})
	}
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

// BenchmarkALPEncode_MixedExceptions isolates the ALP-main exception-sidecar
// path: same decimal2dp shape as BenchmarkALPEncode_Decimal2dp, but every 97th
// value (matching TestNumericALP_GoldenBytes' "mixedExceptions" case) is
// replaced by a full-precision constant that can't round-trip through
// ALP-main's (e,f) digit encoding. Every column here still selects ALP main
// (the exception rate is far too low to tip the size estimate toward RD/raw)
// but goes through encodeMain's exception sidecar rather than encodeMainFast
// — the single-digit-pass codepath for ALP-main exception columns, which is
// not exercised by any other benchmark in this file.
func BenchmarkALPEncode_MixedExceptions(b *testing.B) {
	cols := genALPColumns(100, 1000, 2, 42)
	for _, col := range cols {
		for i := range col {
			if (i+1)%97 == 0 {
				col[i] = math.Pi * 1e17
			}
		}
	}
	benchALPEncodeColumns(b, cols)
}

// BenchmarkALPEncodeMatrix measures 100-column encode workloads across point
// counts and decimal, full-precision, and mixed-exception distributions.
func BenchmarkALPEncodeMatrix(b *testing.B) {
	precisions := []struct {
		name     string
		decimals int
		mixed    bool
	}{
		{name: "0", decimals: 0},
		{name: "2", decimals: 2},
		{name: "4", decimals: 4},
		{name: "6", decimals: 6},
		{name: "full", decimals: -1},
		{name: "mixed", decimals: 2, mixed: true},
	}

	for _, precision := range precisions {
		for _, pointCount := range []int{10, 50, 100, 200, 1000} {
			columns := genALPColumns(100, pointCount, precision.decimals, 42)
			if precision.mixed {
				addALPMixedExceptions(columns)
			}

			name := fmt.Sprintf("Precision/%s/Points/%d", precision.name, pointCount)
			b.Run(name, func(b *testing.B) {
				benchALPEncodeColumns(b, columns)
			})
		}
	}
}

// BenchmarkALPAt measures single-index random access on a 10k-point 2dp
// column (ALP main, width >= 7): an O(1) windowed bit read (alpReadBitsFast)
// plus an O(log k) binary search over the exception sidecar (k = exceptions
// in the column). See At/atMain in alp.go for the current code path.
func BenchmarkALPAt(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	vals := genALPColumns(1, 10000, 2, 123)[0]
	data := alpEncodeSlice(vals, eng)
	dec := NewNumericALPDecoder(eng)

	rng := rand.New(rand.NewSource(1))
	idx := make([]int, 4096)
	for i := range idx {
		idx[i] = rng.Intn(len(vals))
	}

	b.ReportAllocs()
	var s float64
	var ok bool
	for i := 0; b.Loop(); i++ {
		v, o := dec.At(data, idx[i%len(idx)], len(vals))
		s += v
		ok = o
	}
	_ = s
	_ = ok
}

func addALPMixedExceptions(columns [][]float64) {
	flatIndex := 0
	for _, column := range columns {
		for i := range column {
			flatIndex++
			// Preserve about 1% exceptions across the flattened workload; for
			// short columns, only some columns therefore contain an exception.
			if flatIndex%97 == 0 {
				column[i] = math.Pi * 1e17
			}
		}
	}
}
