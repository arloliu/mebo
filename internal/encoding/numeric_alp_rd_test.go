package encoding

import (
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---- reference implementations of the pre-optimization ALP-RD planner ----
//
// alpRDBuildDictRef and alpRDBestCutRef are verbatim copies of the shipped
// map+sort implementations of alpRDBuildDict/alpRDBestCut (the ones that
// produced the frozen TestNumericALP_GoldenBytes constants for the
// fullPrecision dataset). They pin the BEHAVIOR the map-free rewrite must
// preserve byte-for-byte: identical dictionary contents AND order, identical
// chosen rbw, identical estimated totalBits.
//
// The rewrite is byte-identical because the dictionary comparator
// (count DESC, then left ASC) is a TOTAL order with no ties — left values are
// unique map keys — so selecting the top-8 by that comparator yields the exact
// same 8 entries in the exact same order regardless of the order the distinct
// lefts are visited in. The differential tests below deliberately include
// count-tie inputs (multiple lefts sharing a count) to prove the left-ASC
// tie-break survives the switch from sort.Slice to insertion selection.

// alpRDBuildDictRef is the shipped map+sort dictionary builder.
func alpRDBuildDictRef(patterns []uint64, rbw int) (dict []uint64, codeOf map[uint64]int) {
	freq := make(map[uint64]int, len(patterns))
	for _, p := range patterns {
		freq[p>>uint(rbw)]++
	}
	type lv struct {
		left  uint64
		count int
	}
	lvs := make([]lv, 0, len(freq))
	for l, c := range freq {
		lvs = append(lvs, lv{l, c})
	}
	sort.Slice(lvs, func(i, j int) bool {
		if lvs[i].count != lvs[j].count {
			return lvs[i].count > lvs[j].count
		}

		return lvs[i].left < lvs[j].left
	})
	nDict := min(alpRDMaxDictSize, len(lvs))
	dict = make([]uint64, nDict)
	codeOf = make(map[uint64]int, nDict)
	for i := range nDict {
		dict[i] = lvs[i].left
		codeOf[lvs[i].left] = i
	}

	return dict, codeOf
}

// alpRDBestCutRef is the shipped map+sort best-cut search.
func alpRDBestCutRef(patterns []uint64) (rbw, totalBits int) {
	best := math.MaxInt
	n := len(patterns)
	freq := make(map[uint64]int, n)
	type lv struct {
		left  uint64
		count int
	}
	var lvs []lv
	var top [alpRDMaxDictSize]uint64
	for i := 1; i <= alpRDCutLimit; i++ {
		r := 64 - i
		clear(freq)
		for _, p := range patterns {
			freq[p>>r]++
		}
		lvs = lvs[:0]
		for l, c := range freq {
			lvs = append(lvs, lv{l, c})
		}
		sort.Slice(lvs, func(a, b int) bool {
			if lvs[a].count != lvs[b].count {
				return lvs[a].count > lvs[b].count
			}

			return lvs[a].left < lvs[b].left
		})
		nDict := min(alpRDMaxDictSize, len(lvs))
		for k := range nDict {
			top[k] = lvs[k].left
		}
		ex := 0
		for _, p := range patterns {
			l := p >> r
			found := false
			for k := range nDict {
				if top[k] == l {
					found = true

					break
				}
			}
			if !found {
				ex++
			}
		}
		codeBits := alpCodeBits(nDict)
		total := 8 + 8 + 8 + 32 + nDict*16 + n*codeBits + n*r + ex*(32+16)
		if total < best {
			best = total
			rbw, totalBits = r, total
		}
	}

	return rbw, totalBits
}

// ---- pattern-set generators (shared by the differential tests) ----

// alpRDRandPatterns returns n fully random 64-bit patterns.
func alpRDRandPatterns(rng *rand.Rand, n int) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		out[i] = rng.Uint64()
	}

	return out
}

// alpRDTemplatePatterns returns n patterns whose top 16 bits are drawn from a
// pool of nLefts distinct templates and whose low 48 bits are random. Repeating
// templates creates count ties (exact ties at cut width 16 / rbw 48) and lets
// the caller dial distinct-left cardinality independent of n.
func alpRDTemplatePatterns(rng *rand.Rand, n, nLefts int) []uint64 {
	tops := make([]uint64, nLefts)
	seen := make(map[uint64]bool, nLefts)
	for i := range tops {
		for {
			t := uint64(rng.Intn(1 << 16))
			if !seen[t] {
				seen[t] = true
				tops[i] = t

				break
			}
		}
	}
	out := make([]uint64, n)
	for i := range out {
		out[i] = (tops[rng.Intn(nLefts)] << 48) | (rng.Uint64() >> 16)
	}

	return out
}

// alpRDExactTiePatterns returns nLefts distinct 16-bit-top templates, each
// repeated exactly countEach times (low 48 bits random). At rbw 48 every left
// has the identical count — maximal tie stress for the left-ASC tie-break.
func alpRDExactTiePatterns(rng *rand.Rand, nLefts, countEach int) []uint64 {
	tops := make([]uint64, nLefts)
	seen := make(map[uint64]bool, nLefts)
	for i := range tops {
		for {
			t := uint64(rng.Intn(1 << 16))
			if !seen[t] {
				seen[t] = true
				tops[i] = t

				break
			}
		}
	}
	out := make([]uint64, 0, nLefts*countEach)
	for _, t := range tops {
		for range countEach {
			out = append(out, (t<<48)|(rng.Uint64()>>16))
		}
	}
	// Shuffle so first-seen order differs from value order — proves the
	// selection is insensitive to visitation order.
	rng.Shuffle(len(out), func(a, b int) { out[a], out[b] = out[b], out[a] })

	return out
}

// ---- known-vector sanity: pins the Ref builder against hand-computed dicts ----
//
// This runs GREEN against the shipped code before the map-free swap. It proves
// the Ref capture and my understanding of the (count DESC, left ASC) total
// order are correct, so the differential comparison below is grounded in an
// independent oracle rather than being circular.
func TestAlpRDBuildDictRef_KnownVectors(t *testing.T) {
	const rbw = 60                                       // left = p>>60, i.e. the top 4 bits (values 0..15)
	mk := func(left uint64) uint64 { return left << 60 } // low bits irrelevant to the dict

	tests := []struct {
		name     string
		patterns []uint64
		wantDict []uint64
	}{
		{
			// left 2 ×3, left 5 ×3 (tie, count 3), left 1 ×1.
			// count DESC: {2,5} then 1; left ASC breaks the 3-way tie: 2 before 5.
			name:     "count_tie_left_asc",
			patterns: []uint64{mk(5), mk(2), mk(5), mk(1), mk(2), mk(5), mk(2)},
			wantDict: []uint64{2, 5, 1},
		},
		{
			// single distinct left (nDict == 1 -> codeBits == 1 downstream).
			name:     "single_left",
			patterns: []uint64{mk(7), mk(7), mk(7), mk(7), mk(7)},
			wantDict: []uint64{7},
		},
		{
			// 9 distinct lefts (0..8), all count 1: overflow by one, keep the 8
			// smallest by the left-ASC tie-break, drop 8.
			name:     "overflow_drop_largest",
			patterns: []uint64{mk(8), mk(0), mk(7), mk(1), mk(6), mk(2), mk(5), mk(3), mk(4)},
			wantDict: []uint64{0, 1, 2, 3, 4, 5, 6, 7},
		},
		{
			// full-8 tie set displaced by a higher-count late entry: left 9 has
			// count 3, so it takes code 0 and the largest count-1 left (8) drops.
			name:     "high_count_displaces_full_dict",
			patterns: []uint64{mk(1), mk(2), mk(3), mk(4), mk(5), mk(6), mk(7), mk(8), mk(9), mk(9), mk(9)},
			wantDict: []uint64{9, 1, 2, 3, 4, 5, 6, 7},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dict, codeOf := alpRDBuildDictRef(tc.patterns, rbw)
			require.Equalf(t, tc.wantDict, dict, "%s: dict contents/order", tc.name)
			require.Lenf(t, codeOf, len(tc.wantDict), "%s: codeOf size", tc.name)
			for i, l := range tc.wantDict {
				require.Equalf(t, i, codeOf[l], "%s: codeOf[%d]", tc.name, l)
			}
		})
	}
}

// alpRDBuildDictNew drives the map-free alpRDBuildDict with fresh scratch and
// returns the dict slice + the reconstructed codeOf + the reported coverage, so
// it can be compared against the map-returning Ref directly.
func alpRDBuildDictNew(patterns []uint64, rbw int) (dict []uint64, codeOf map[uint64]int, covered int) {
	var arr [alpRDMaxDictSize]uint64
	var lefts []uint64
	var cnts []int32
	nDict, cov := alpRDBuildDict(patterns, rbw, &lefts, &cnts, &arr)
	dict = append([]uint64(nil), arr[:nDict]...)
	codeOf = make(map[uint64]int, nDict)
	for i, l := range dict {
		codeOf[l] = i
	}

	return dict, codeOf, cov
}

// alpRDCoveredRef independently counts how many patterns' left parts fall in the
// reference dictionary — the oracle for alpRDBuildDict's covered return (and, via
// len(patterns)-covered, for the exception total the size estimate depends on).
func alpRDCoveredRef(patterns []uint64, rbw int, codeOf map[uint64]int) int {
	covered := 0
	for _, p := range patterns {
		if _, ok := codeOf[p>>uint(rbw)]; ok {
			covered++
		}
	}

	return covered
}

// TestAlpRDBuildDict_Differential proves the map-free dictionary builder is
// byte-identical to the shipped map+sort builder across seeded random pattern
// sets — every cut width, varied distinct-left cardinality (1, 2, ~8, 9, ~20,
// 200+), and deliberate count ties. Dict contents AND order must match, and the
// reconstructed codeOf must agree, or a downstream encoded byte would differ.
func TestAlpRDBuildDict_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(202))
	// A spread of column sizes that (across the 16 cut widths) exercises the
	// full cardinality ladder, including >8 distinct (overflow) and 200+.
	sizes := []int{1, 2, 3, 8, 9, 16, 20, 50, 200, 400}
	kinds := 4
	for _, n := range sizes {
		for range 300 {
			var patterns []uint64
			switch rng.Intn(kinds) {
			case 0:
				patterns = alpRDRandPatterns(rng, n)
			case 1:
				patterns = alpRDTemplatePatterns(rng, n, 1+rng.Intn(12))
			case 2:
				// Force exact count ties; total size ~= n.
				each := 1 + rng.Intn(6)
				patterns = alpRDExactTiePatterns(rng, max(1, n/each), each)
			default:
				// Low-cardinality: few templates so many cut widths yield ≤8
				// distinct, exercising the nDict < 8 path.
				patterns = alpRDTemplatePatterns(rng, n, 1+rng.Intn(3))
			}
			for rbw := 64 - alpRDCutLimit; rbw <= 63; rbw++ {
				gotDict, gotCodeOf, gotCovered := alpRDBuildDictNew(patterns, rbw)
				wantDict, wantCodeOf := alpRDBuildDictRef(patterns, rbw)
				require.Equalf(t, wantDict, gotDict, "buildDict dict n=%d rbw=%d", n, rbw)
				require.Equalf(t, wantCodeOf, gotCodeOf, "buildDict codeOf n=%d rbw=%d", n, rbw)
				require.Equalf(t, alpRDCoveredRef(patterns, rbw, wantCodeOf), gotCovered,
					"buildDict covered n=%d rbw=%d", n, rbw)
			}
		}
	}
}

// TestAlpRDBestCut_Differential proves the map-free best-cut search returns an
// identical (rbw, totalBits) to the shipped map+sort search. Inputs are capped
// at 60 patterns — alpRDBestCut only ever runs on the strided sample, which is
// bounded at 63 entries (TestAlpRDSampleBound); its fixed [64] scratch arrays
// must not be fed more. Ties and mixed cardinalities are included.
func TestAlpRDBestCut_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(303))
	for range 20000 {
		n := 1 + rng.Intn(60)
		var patterns []uint64
		switch rng.Intn(4) {
		case 0:
			patterns = alpRDRandPatterns(rng, n)
		case 1:
			patterns = alpRDTemplatePatterns(rng, n, 1+rng.Intn(10))
		case 2:
			each := 1 + rng.Intn(5)
			nl := max(1, min(n, 12))
			patterns = alpRDExactTiePatterns(rng, nl, each)
			if len(patterns) > 60 {
				patterns = patterns[:60]
			}
		default:
			patterns = alpRDTemplatePatterns(rng, n, 1+rng.Intn(3))
		}
		gotRbw, gotBits := alpRDBestCut(patterns)
		wantRbw, wantBits := alpRDBestCutRef(patterns)
		require.Equalf(t, wantRbw, gotRbw, "bestCut rbw (n=%d)", len(patterns))
		require.Equalf(t, wantBits, gotBits, "bestCut totalBits (n=%d)", len(patterns))
	}
}

// TestAlpRDSampleBound pins the invariant the fixed [64] sample/scratch arrays
// depend on: the strided sample alpRDPlan/alpRDBestCut operate on never exceeds
// 63 entries (so ≤63 distinct lefts). It checks every n in 1..200 plus a spread
// of large n. If alpSampleStride ever changes such that the sample can reach 64,
// this fails before any [64] array can overflow in production.
func TestAlpRDSampleBound(t *testing.T) {
	check := func(n int) {
		stride := alpSampleStride(n)
		count := 0
		for i := 0; i < n; i += stride {
			count++
		}
		require.LessOrEqualf(t, count, 64, "sample count for n=%d (stride=%d) must fit [64]", n, stride)
		// Tighter documented bound: at most 63.
		require.LessOrEqualf(t, count, 63, "sample count for n=%d (stride=%d) must be ≤63", n, stride)
	}
	for n := 1; n <= 200; n++ {
		check(n)
	}
	for _, n := range []int{201, 255, 256, 1000, 1023, 1024, 4095, 65535, 65536, 1 << 20} {
		check(n)
	}
}
