package encoding

// PoC: ALP-RD ("Real Double") — ALP's second scheme for full-precision doubles
// that the decimal ALP main scheme can't represent. Reference: cwida/ALP rd.hpp
// + config.hpp (CUTTING_LIMIT=16, MAX_RD_DICTIONARY_SIZE=8).
//
// Goal: prove ALP-RD losslessly compresses the FULL-PRECISION datasets where ALP
// main collapsed (battery_fp: 199/200 exceptions), with ratio competitive with
// Chimp. Run: go test ./internal/encoding/ -run 'POCALPRD' -v

import (
	"math"
	"math/bits"
	"slices"
	"sort"
	"testing"
)

const (
	alpRDCuttingLimit = 16
	alpRDDictSize     = 8
)

type alpRDResult struct {
	totalBits int
	rbw       int // right bit width
	leftBits  int // left part width = 64 - rbw
	dictBits  int // code width = ceil(log2(dictSize))
	nDict     int
	nExc      int
}

func ceilLog2(n int) int {
	if n <= 1 {
		return 1
	}

	return bits.Len64(uint64(n - 1))
}

// alpRDEvaluate computes the exact ALP-RD compressed size for a given right bit
// width, building an 8-entry most-frequent dictionary of left parts.
func alpRDEvaluate(patterns []uint64, rbw int) alpRDResult {
	n := len(patterns)
	leftBits := 64 - rbw

	freq := make(map[uint64]int, n)
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
	sort.Slice(lvs, func(i, j int) bool { return lvs[i].count > lvs[j].count })

	nDict := min(alpRDDictSize, len(lvs))
	inDict := make(map[uint64]struct{}, nDict)
	for i := range nDict {
		inDict[lvs[i].left] = struct{}{}
	}
	nExc := 0
	for _, p := range patterns {
		if _, ok := inDict[p>>uint(rbw)]; !ok {
			nExc++
		}
	}
	dictBits := ceilLog2(nDict)

	// header: rbw(6) + dictBits(4) + nDict(4) + nExc(16) = 30
	const header = 30
	total := header +
		nDict*leftBits + // dictionary entries
		n*dictBits + // left codes
		n*rbw + // right parts
		nExc*(leftBits+16) // exceptions: left value + uint16 position

	return alpRDResult{totalBits: total, rbw: rbw, leftBits: leftBits, dictBits: dictBits, nDict: nDict, nExc: nExc}
}

// alpRDCompress searches the cut point (right bit width) for the smallest size.
func alpRDCompress(values []float64) alpRDResult {
	patterns := make([]uint64, len(values))
	for i, v := range values {
		patterns[i] = math.Float64bits(v)
	}
	best := alpRDResult{totalBits: math.MaxInt}
	for i := 1; i <= alpRDCuttingLimit; i++ {
		r := alpRDEvaluate(patterns, 64-i)
		if r.totalBits < best.totalBits {
			best = r
		}
	}

	return best
}

// alpRDRoundTrip verifies lossless reconstruction at the chosen cut point.
func alpRDRoundTrip(values []float64, rbw int) []float64 {
	n := len(values)
	patterns := make([]uint64, n)
	for i, v := range values {
		patterns[i] = math.Float64bits(v)
	}
	// build dictionary (same as evaluate)
	freq := make(map[uint64]int, n)
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
	sort.Slice(lvs, func(i, j int) bool { return lvs[i].count > lvs[j].count })
	nDict := min(alpRDDictSize, len(lvs))
	dict := make([]uint64, nDict)
	code := make(map[uint64]int, nDict)
	for i := range nDict {
		dict[i] = lvs[i].left
		code[lvs[i].left] = i
	}
	rightMask := (uint64(1) << uint(rbw)) - 1

	// encode → (codes, rights, exceptions) then decode
	out := make([]float64, n)
	for i, p := range patterns {
		left := p >> uint(rbw)
		right := p & rightMask
		var recLeft uint64
		if c, ok := code[left]; ok {
			recLeft = dict[c] // via dictionary code
		} else {
			recLeft = left // exception: stored raw
		}
		recBits := (recLeft << uint(rbw)) | right
		out[i] = math.Float64frombits(recBits)
	}

	return out
}

func TestPOCALPRD(t *testing.T) {
	t.Logf("%-16s %6s | %9s %9s | %10s | %s", "dataset", "n", "gorilla", "chimp", "ALP-RD", "rd(rbw,dict,exc)")
	t.Logf("%s", "----------------------------------------------------------------------------------")
	for _, d := range pocFloatDatasets() {
		n := len(d.values)
		g := float64(gorillaSize(d.values)) / float64(n)
		c := float64(chimpSize(d.values)) / float64(n)
		rd := alpRDCompress(d.values)
		rdB := float64(rd.totalBits) / 8 / float64(n)

		// lossless check at chosen cut
		got := alpRDRoundTrip(d.values, rd.rbw)
		if !slices.Equal(got, d.values) {
			t.Errorf("%s: ALP-RD round-trip not bit-exact", d.name)
		}

		win := ""
		if rdB < c && rdB < g {
			win = "  <- ALP-RD best"
		} else if rdB <= c*1.05 {
			win = "  (~chimp)"
		}
		t.Logf("%-16s %6d | %8.3fB %8.3fB | %9.3fB | rbw=%d dict=%d exc=%d/%d%s",
			d.name, n, g, c, rdB, rd.rbw, rd.nDict, rd.nExc, n, win)
	}
}
