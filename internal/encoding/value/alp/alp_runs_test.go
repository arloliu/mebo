package alp

import (
	"math"
	"math/rand"
	"testing"

	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
)

// alpRunsHold returns a 2-decimal gauge of n points where each point repeats the previous one with probability hold.
func alpRunsHold(n int, hold float64, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	cur := 100.0
	for i := range out {
		if i > 0 && rng.Float64() < hold {
			out[i] = out[i-1]
			continue
		}
		cur += cur * (rng.Float64()*2 - 1) * 0.005
		out[i] = math.Round(cur*100) / 100
	}

	return out
}

// alpRunsRepeat expands values so that values[k] appears lens[k%len(lens)] times.
func alpRunsRepeat(values []float64, lens ...int) []float64 {
	out := make([]float64, 0, len(values)*4)
	for k, v := range values {
		for range lens[k%len(lens)] {
			out = append(out, v)
		}
	}

	return out
}

// alpRunsOneRepeat returns a run-free 2-decimal gauge of n points whose last point repeats the one before.
func alpRunsOneRepeat(n int, seed int64) []float64 {
	out := genALPColumns(1, n, 2, seed)[0]
	out[n-1] = out[n-2]

	return out
}

func alpRunsEncode(values []float64, eng endian.EndianEngine, runs bool) []byte {
	enc := NewNumericALPEncoder(eng)
	if runs {
		enc = NewNumericALPRLEEncoder(eng)
	}
	enc.WriteSlice(values)
	out := append([]byte(nil), enc.Bytes()...)
	enc.Finish()

	return out
}

// alpRunsBits converts values to bit patterns so comparisons are bitwise (−0.0, NaN payloads).
func alpRunsBits(values []float64) []uint64 {
	out := make([]uint64, len(values))
	for i, v := range values {
		out[i] = math.Float64bits(v)
	}

	return out
}

// alpRunsCheckDecode asserts that All, DecodeAll and At all reproduce values bitwise.
func alpRunsCheckDecode(t *testing.T, col []byte, values []float64, eng endian.EndianEngine) {
	t.Helper()
	dec := NewNumericALPDecoder(eng)
	want := alpRunsBits(values)
	n := len(values)

	got := make([]float64, 0, n)
	for v := range dec.All(col, n) {
		got = append(got, v)
	}
	require.Equal(t, want, alpRunsBits(got), "All")

	dst := make([]float64, n)
	require.Equal(t, n, dec.DecodeAll(col, n, dst))
	require.Equal(t, want, alpRunsBits(dst), "DecodeAll")

	for i := range n {
		v, ok := dec.At(col, i, n)
		require.Truef(t, ok, "At(%d)", i)
		require.Equalf(t, want[i], math.Float64bits(v), "At(%d)", i)
	}
	_, ok := dec.At(col, n, n)
	require.False(t, ok, "At past the end")
	_, ok = dec.At(col, -1, n)
	require.False(t, ok, "At(-1)")
}

func alpRunsCases() []struct {
	name   string
	values []float64
	runs   bool // the runs layout must win
} {
	negZero := math.Copysign(0, -1)
	nan1 := math.Float64frombits(0x7ff8000000000001)
	nan2 := math.Float64frombits(0x7ff8000000000002)
	fullPrec := genALPColumns(1, 40, -1, 7)[0]
	withExc := genALPColumns(1, 40, 2, 8)[0]
	withExc[5], withExc[20], withExc[33] = math.Pi, math.Inf(1), math.E

	return []struct {
		name   string
		values []float64
		runs   bool
	}{
		{"hold50", alpRunsHold(150, 0.5, 1), true},
		{"hold70_1000pts", alpRunsHold(1000, 0.7, 2), true},
		{"step_levels", alpRunsRepeat([]float64{100.25, 3.5, 77.75, 3.5}, 40, 35), true},
		{"all_equal", alpRunsRepeat([]float64{42.5}, 150), false}, // plain ALP packs a constant at width 0
		{"signed_zeros", alpRunsRepeat([]float64{0, negZero, 0, negZero, 1.5}, 9, 7), true},
		{"nan_payloads", alpRunsRepeat([]float64{nan1, nan2, nan1, 3.25}, 8, 5), true},
		{"nested_exceptions", alpRunsRepeat(withExc, 6, 9), true},
		{"nested_rd", alpRunsRepeat(fullPrec, 5, 3), true},
		{"nested_raw", alpRunsRepeat([]float64{math.Pi, math.Inf(-1), math.E, nan1, math.Sqrt2}, 12), true},
		{"run_free", genALPColumns(1, 150, 2, 3)[0], false},
		{"one_repeat", alpRunsOneRepeat(150, 4), false},
		{"single_point", []float64{1.5}, false},
		{"two_equal_points", []float64{1.5, 1.5}, false},
	}
}

func TestNumericALPRuns_RoundTrip(t *testing.T) {
	engines := []struct {
		name string
		eng  endian.EndianEngine
	}{
		{"LE", endian.GetLittleEndianEngine()},
		{"BE", endian.GetBigEndianEngine()},
	}
	for _, e := range engines {
		for _, tc := range alpRunsCases() {
			t.Run(e.name+"/"+tc.name, func(t *testing.T) {
				col := alpRunsEncode(tc.values, e.eng, true)
				plain := alpRunsEncode(tc.values, e.eng, false)
				if tc.runs {
					require.Equal(t, alpSchemeRuns, col[0], "runs layout must win")
					require.Less(t, len(col), len(plain))
					_, nested, err := ValidateRunsColumn(col[1:], len(tc.values), e.eng)
					require.NoError(t, err)
					require.LessOrEqual(t, nested[0], ALPMaxSchemeByte)
				} else {
					require.Equal(t, plain, col, "plain must win byte-identically")
				}
				alpRunsCheckDecode(t, col, tc.values, e.eng)
			})
		}
	}
}

// TestNumericALPRuns_NestedSchemes pins which nested scheme each case exercises,
// so a change in ALP's scheme choice cannot silently drop coverage.
func TestNumericALPRuns_NestedSchemes(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	want := map[string]byte{
		"nested_exceptions": alpSchemeMain,
		"nested_rd":         alpSchemeRD,
		"nested_raw":        alpSchemeRaw,
	}
	for _, tc := range alpRunsCases() {
		scheme, ok := want[tc.name]
		if !ok {
			continue
		}
		col := alpRunsEncode(tc.values, eng, true)
		require.Equal(t, alpSchemeRuns, col[0], tc.name)
		_, nested, err := ValidateRunsColumn(col[1:], len(tc.values), eng)
		require.NoError(t, err)
		require.Equal(t, scheme, nested[0], tc.name)
		if scheme == alpSchemeMain {
			require.NotZero(t, eng.Uint32(nested[4:8]), "%s: nested column must carry exceptions", tc.name)
		}
	}
}

// TestNumericALPRuns_NeverLarger checks the superset guarantee on random columns:
// the runs encoder is never larger than plain ALP, and is byte-identical whenever it keeps the plain column.
func TestNumericALPRuns_NeverLarger(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	rng := rand.New(rand.NewSource(11))
	for i := range 300 {
		n := 1 + rng.Intn(300)
		values := alpRunsHold(n, rng.Float64(), int64(i))
		if i%3 == 0 {
			for k := range values {
				if rng.Intn(20) == 0 {
					values[k] = math.Pi * float64(k)
				}
			}
		}
		col := alpRunsEncode(values, eng, true)
		plain := alpRunsEncode(values, eng, false)
		require.NotEqual(t, alpSchemeRuns, plain[0], "the plain encoder must never write the runs layout")
		require.LessOrEqual(t, len(col), len(plain))
		if col[0] != alpSchemeRuns {
			require.Equal(t, plain, col)
		}
		alpRunsCheckDecode(t, col, values, eng)
	}
}

// TestNumericALPRuns_MultiColumn checks that a runs column replacing a plain one
// in the middle of the buffer leaves the neighboring columns intact.
func TestNumericALPRuns_MultiColumn(t *testing.T) {
	eng := endian.GetBigEndianEngine()
	cols := [][]float64{
		genALPColumns(1, 100, 2, 5)[0],
		alpRunsHold(150, 0.6, 6),
		alpRunsRepeat([]float64{7.5}, 64),
		genALPColumns(1, 70, -1, 8)[0],
	}

	enc := NewNumericALPRLEEncoder(eng)
	offsets := make([]int, 0, len(cols)+1)
	offsets = append(offsets, 0)
	for _, c := range cols {
		enc.WriteSlice(c)
		offsets = append(offsets, len(enc.Bytes()))
		enc.Reset()
	}
	buf := append([]byte(nil), enc.Bytes()...)
	enc.Finish()

	for k, c := range cols {
		col := buf[offsets[k]:offsets[k+1]]
		require.Equal(t, alpRunsEncode(c, eng, true), col, "column %d", k)
		alpRunsCheckDecode(t, col, c, eng)
	}
}

// TestNumericALPRuns_PruningCounterexample covers the case an average-cost rule missed:
// repeated exceptions cost far more than the average point, so the runs layout must be tried and win.
func TestNumericALPRuns_PruningCounterexample(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	values := make([]float64, 0, 150)
	for i := range 146 {
		values = append(values, float64(1+i%2))
	}
	values = append(values, math.Inf(1), math.Inf(1), math.Inf(1), math.Inf(1))

	plain := alpRunsEncode(values, eng, false)
	require.True(t, alpRunsWorthTrying(values, plain, eng))
	col := alpRunsEncode(values, eng, true)
	require.Equal(t, alpSchemeRuns, col[0])
	require.Less(t, len(col), len(plain))
	alpRunsCheckDecode(t, col, values, eng)
}

func TestNumericALPRuns_WorthTrying(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	tests := []struct {
		name   string
		values []float64
		want   bool
	}{
		{"no_repeats", genALPColumns(1, 150, 2, 1)[0], false},
		{"one_cheap_repeat", alpRunsOneRepeat(150, 1), false},
		{"half_repeats", alpRunsHold(150, 0.5, 1), true},
		{"raw_repeats", alpRunsRepeat([]float64{math.Pi, math.E, math.Sqrt2, math.Inf(1)}, 2), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plain := alpRunsEncode(tt.values, eng, false)
			require.Equal(t, tt.want, alpRunsWorthTrying(tt.values, plain, eng))
		})
	}
}

// TestNumericALPRuns_DecodeAllDst checks the min(count, len(dst)) contract on a runs column:
// empty, short (ending inside and at a bitmap word or byte), exact and oversized destinations.
func TestNumericALPRuns_DecodeAllDst(t *testing.T) {
	for _, eng := range []endian.EndianEngine{endian.GetLittleEndianEngine(), endian.GetBigEndianEngine()} {
		for _, values := range [][]float64{alpRunsHold(150, 0.5, 9), alpRunsRepeat([]float64{1, math.Pi, 2}, 7, 60)} {
			col := alpRunsEncode(values, eng, true)
			require.Equal(t, alpSchemeRuns, col[0])
			count := len(values)
			want := alpRunsBits(values)
			dec := NewNumericALPDecoder(eng)
			for _, n := range []int{0, 1, 2, 7, 8, 9, 63, 64, 65, 127, 128, 129, count - 1, count, count + 1, count + 64} {
				if n < 0 {
					continue
				}
				const sentinel = -12345.0
				dst := make([]float64, n)
				for i := range dst {
					dst[i] = sentinel
				}
				wrote := dec.DecodeAll(col, count, dst)
				m := min(n, count)
				require.Equalf(t, m, wrote, "len(dst)=%d", n)
				require.Equalf(t, want[:m], alpRunsBits(dst[:m]), "len(dst)=%d", n)
				for i := m; i < n; i++ {
					require.Equalf(t, sentinel, dst[i], "len(dst)=%d: dst[%d] past count was written", n, i)
				}
			}
			require.Zero(t, dec.DecodeAll(col, 0, make([]float64, 4)))
		}
	}
}

// TestNumericALPRuns_AllEarlyStop checks that breaking out of All mid-run and mid-column stops cleanly.
func TestNumericALPRuns_AllEarlyStop(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	values := alpRunsRepeat([]float64{1.5, 2.5, 3.5}, 10)
	col := alpRunsEncode(values, eng, true)
	require.Equal(t, alpSchemeRuns, col[0])
	dec := NewNumericALPDecoder(eng)
	for _, stop := range []int{1, 5, 10, 11, 29} {
		got := 0
		for v := range dec.All(col, len(values)) {
			require.Equal(t, values[got], v)
			got++
			if got == stop {
				break
			}
		}
		require.Equal(t, stop, got)
	}
}

func TestNumericALPRuns_DecodeAllNoAlloc(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	values := alpRunsHold(150, 0.5, 3)
	col := alpRunsEncode(values, eng, true)
	require.Equal(t, alpSchemeRuns, col[0])
	dec := NewNumericALPDecoder(eng)
	dst := make([]float64, len(values))
	allocs := testing.AllocsPerRun(100, func() {
		dec.DecodeAll(col, len(values), dst)
		_, _ = dec.At(col, 149, len(values))
	})
	require.Zero(t, allocs)
}

func TestNumericALPRuns_BitmapLen(t *testing.T) {
	for count := range 100 {
		require.Equalf(t, (count+7)/8, alpRunsBitmapLen(count), "count=%d", count)
	}
	// (count+7)/8 wraps for these on every platform; the bitmap length must not.
	for _, count := range []int{math.MaxInt - 8, math.MaxInt - 7, math.MaxInt - 1, math.MaxInt} {
		got := alpRunsBitmapLen(count)
		require.Equalf(t, count/8+min(count%8, 1), got, "count=%d", count)
		require.Positive(t, got)
	}
	require.Equal(t, math.MaxInt32/8+1, alpRunsBitmapLen(math.MaxInt32), "largest count a 32-bit blob can declare")
}

func TestNumericALPRuns_Rank(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, n := range []int{1, 7, 8, 9, 63, 64, 65, 150, 1000} {
		bm := make([]byte, (n+7)/8)
		want := make([]int, n)
		r := 0
		for i := range n {
			if i == 0 || rng.Intn(3) == 0 {
				bm[i>>3] |= 1 << uint(i&7)
				r++
			}
			want[i] = r
		}
		for i := range n {
			require.Equalf(t, want[i], alpRunsRank(bm, i), "n=%d i=%d", n, i)
		}
	}
}

// TestValidateRunsColumn covers every runs-envelope rule; each case corrupts one field of a valid column.
func TestValidateRunsColumn(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	// 12 points in runs of 3, 4, 2 and 3: runs start at points 0, 3, 7 and 9.
	// The bitmap is 2 bytes with 4 padding bits; the column is built by hand, independently of the encoder.
	const count = 12
	runVals := []float64{1.5, 2.5, 3.5, 4.5}
	body := eng.AppendUint32(nil, uint32(len(runVals)))
	body = append(body, 0b1000_1001, 0b0000_0010)
	body = append(body, alpRunsEncode(runVals, eng, false)...)
	alpRunsCheckDecode(t, append([]byte{alpSchemeRuns}, body...), alpRunsRepeat(runVals, 3, 4, 2, 3), eng)

	nRuns, nested, err := ValidateRunsColumn(body, count, eng)
	require.NoError(t, err)
	require.Equal(t, 4, nRuns)
	require.Equal(t, body[4+2:], nested)

	mutate := func(f func(b []byte) []byte) []byte {
		return f(append([]byte(nil), body...))
	}
	setRuns := func(n uint32) []byte {
		return mutate(func(b []byte) []byte { eng.PutUint32(b[:4], n); return b })
	}

	tests := []struct {
		name  string
		body  []byte
		count int
	}{
		{"empty body", nil, count},
		{"short header", body[:3], count},
		{"zero runs", setRuns(0), count},
		{"more runs than points", setRuns(count + 1), count},
		{"huge run count", setRuns(math.MaxUint32), count},
		{"zero count", body, 0},
		{"negative count", body, -1},
		{"truncated bitmap", body[:5], count},
		{"missing nested scheme byte", body[:6], count},
		{"bit 0 clear", mutate(func(b []byte) []byte { b[4] &^= 1; return b }), count},
		{"padding bit set", mutate(func(b []byte) []byte { b[5] |= 0x80; return b }), count},
		{"popcount above nRuns", mutate(func(b []byte) []byte { b[4] |= 0x02; return b }), count},
		{"popcount below nRuns", setRuns(3), count},
		{"nested scheme 3", mutate(func(b []byte) []byte { b[6] = alpSchemeRuns; return b }), count},
		{"nested scheme 255", mutate(func(b []byte) []byte { b[6] = 0xff; return b }), count},
		{"count past the body", body, 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, _, err := ValidateRunsColumn(tt.body, tt.count, eng)
				require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
			})
		})
	}
}

// TestNumericALPRuns_GoldenBytes pins the runs encoder's output, both byte orders, to xxhash constants
// (see TestNumericALP_GoldenBytes for how to regenerate them after an intentional format change).
// The existing plain-ALP goldens must stay unchanged: the runs encoder is a separate constructor.
func TestNumericALPRuns_GoldenBytes(t *testing.T) {
	hold50 := alpRunsHold(alpGoldenPoints, 0.5, alpGoldenSeed)
	steps := alpRunsRepeat(genALPColumns(1, 40, 2, alpGoldenSeed)[0], 25)
	withExceptions := append([]float64(nil), hold50...)
	for i := range withExceptions {
		if (i/10)%9 == 0 {
			withExceptions[i] = math.Pi * 1e17
		}
	}

	_, nested, err := ValidateRunsColumn(alpRunsEncode(withExceptions, endian.GetLittleEndianEngine(), true)[1:], alpGoldenPoints, endian.GetLittleEndianEngine())
	require.NoError(t, err)
	require.Equal(t, alpSchemeMain, nested[0], "withExceptions must nest a main column")
	require.NotZero(t, endian.GetLittleEndianEngine().Uint32(nested[4:8]), "withExceptions must nest exceptions")

	cases := []struct {
		name   string
		values []float64
		wantLE uint64
		wantBE uint64
	}{
		{"hold50", hold50, 0xc93c5a534fa3fb19, 0xa8f2381f94bb1779},
		{"steps", steps, 0x4440c96e6fd21ce8, 0x1b43fe9f65fd3379},
		{"withExceptions", withExceptions, 0xa55aa94f175b6112, 0xff4bef00f81ad9a4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			le := alpRunsEncode(tc.values, endian.GetLittleEndianEngine(), true)
			be := alpRunsEncode(tc.values, endian.GetBigEndianEngine(), true)
			require.Equal(t, alpSchemeRuns, le[0])
			require.Equal(t, alpSchemeRuns, be[0])
			alpRunsCheckDecode(t, le, tc.values, endian.GetLittleEndianEngine())
			alpRunsCheckDecode(t, be, tc.values, endian.GetBigEndianEngine())
			gotLE, gotBE := xxhash.Sum64(le), xxhash.Sum64(be)
			t.Logf("%s little-endian hash: 0x%016x", tc.name, gotLE)
			t.Logf("%s big-endian hash: 0x%016x", tc.name, gotBE)
			require.Equalf(t, tc.wantLE, gotLE, "%s: little-endian runs output changed", tc.name)
			require.Equalf(t, tc.wantBE, gotBE, "%s: big-endian runs output changed", tc.name)
		})
	}
}

// alpEFTestColumns returns columns of many shapes and lengths for the (e,f) search tests:
// decimals at several magnitudes and precisions, integers, full precision, special values,
// random bit patterns, constants, and the run values of hold columns (the nested encoder's real input).
func alpEFTestColumns(rng *rand.Rand) [][]float64 {
	var cols [][]float64
	for _, n := range []int{1, 2, 3, 7, 31, 32, 33, 64, 75, 150, 151, 1000, 2017} {
		for dec := -1; dec <= 6; dec++ {
			col := make([]float64, n)
			scale := math.Pow(10, float64(dec))
			cur := math.Pow(10, float64(rng.Intn(10)-3)) * (rng.Float64() + 0.5)
			if rng.Intn(2) == 0 {
				cur = -cur
			}
			for i := range col {
				cur += cur * (rng.Float64()*2 - 1) * 0.01
				col[i] = cur
				if dec >= 0 {
					col[i] = math.Round(cur*scale) / scale
				}
			}
			cols = append(cols, col)
		}
		specials := []float64{math.Copysign(0, -1), 0, math.Inf(1), math.NaN(), math.SmallestNonzeroFloat64, math.MaxFloat64, 1e300}
		mixed := genALPColumns(1, n, 2, rng.Int63())[0]
		for i := range mixed {
			if rng.Intn(5) == 0 {
				mixed[i] = specials[rng.Intn(len(specials))]
			}
		}
		bitsCol := make([]float64, n)
		for i := range bitsCol {
			bitsCol[i] = math.Float64frombits(rng.Uint64())
		}
		ints := make([]float64, n)
		for i := range ints {
			ints[i] = float64(rng.Int63n(1 << 40))
		}
		constant := make([]float64, n)
		for i := range constant {
			constant[i] = 123.45
		}
		runVals, _ := alpRunValues(alpRunsHold(n, 0.5, rng.Int63()))
		cols = append(cols, mixed, bitsCol, ints, constant, runVals)
	}

	return cols
}

// alpRunValues returns the distinct consecutive values of col (one per run) and the run count.
func alpRunValues(col []float64) ([]float64, int) {
	out := make([]float64, 0, len(col))
	for i, v := range col {
		if i == 0 || math.Float64bits(v) != math.Float64bits(col[i-1]) {
			out = append(out, v)
		}
	}

	return out, len(out)
}

// alpEFSample is the strided sample alpBestEF searches over.
func alpEFSample(values []float64, stride int) []float64 {
	var sample []float64
	for i := 0; i < len(values); i += stride {
		sample = append(sample, values[i])
	}

	return sample
}

// TestAlpEFEstimate_MirrorsBestEF is the differential test for the duplicated (e,f) estimator:
// alpBestEF's choice must be the first (e,f), in its search order, that minimizes alpEFEstimate,
// so the seeded search used for nested run values ranks candidates exactly like the plain search.
// It also checks that a pruned estimate is never one that could have beaten the bound.
func TestAlpEFEstimate_MirrorsBestEF(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	for c, values := range alpEFTestColumns(rng) {
		stride := alpSampleStride(len(values))
		sample := alpEFSample(values, stride)
		fullCnt := (len(values) + stride - 1) / stride

		minEst, firstE, firstF := math.MaxFloat64, -1, -1
		for e := 0; e <= alpMaxExponent; e++ {
			for f := 0; f <= e; f++ {
				est, ok := alpEFEstimate(sample, e, f, fullCnt, math.MaxFloat64)
				require.Truef(t, ok, "column %d: unbounded estimate must not prune", c)
				if est < minEst {
					minEst, firstE, firstF = est, e, f
				}
				// Pruning against a bound is only allowed when the full estimate reaches that bound.
				bound := est * (0.5 + rng.Float64())
				if _, ok := alpEFEstimate(sample, e, f, fullCnt, bound); !ok {
					require.GreaterOrEqualf(t, est, bound, "column %d (e=%d,f=%d): pruned below the bound", c, e, f)
				}
			}
		}
		e0, f0 := alpBestEF(values, stride)
		require.Equalf(t, [2]int{firstE, firstF}, [2]int{e0, f0}, "column %d (n=%d): alpBestEF and alpEFEstimate disagree", c, len(values))
	}
}

// TestAlpBestEFSeeded_FindsMinimum checks the seeded search for every seed:
// it returns a minimum-estimate (e,f), the seed itself when the seed is a minimum,
// and otherwise the first minimum in search order, which is what alpBestEF returns.
func TestAlpBestEFSeeded_FindsMinimum(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for c, values := range alpEFTestColumns(rng) {
		if c%3 != 0 {
			continue // every seed below is checked, so a third of the columns keeps the test fast
		}
		stride := alpSampleStride(len(values))
		sample := alpEFSample(values, stride)
		fullCnt := (len(values) + stride - 1) / stride
		est := func(e, f int) float64 {
			v, ok := alpEFEstimate(sample, e, f, fullCnt, math.MaxFloat64)
			require.True(t, ok)

			return v
		}
		e0, f0 := alpBestEF(values, stride)
		minEst := est(e0, f0)
		for se := 0; se <= alpMaxExponent; se++ {
			for sf := 0; sf <= se; sf++ {
				e1, f1 := alpBestEFSeeded(values, stride, se, sf)
				require.Equalf(t, minEst, est(e1, f1), "column %d seed (%d,%d): not a minimum", c, se, sf)
				switch {
				case est(se, sf) == minEst:
					require.Equalf(t, [2]int{se, sf}, [2]int{e1, f1}, "column %d: a minimal seed must win ties", c)
				default:
					require.Equalf(t, [2]int{e0, f0}, [2]int{e1, f1}, "column %d seed (%d,%d): must match the unseeded search", c, se, sf)
				}
			}
		}
	}
}
