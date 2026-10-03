package research

// PoC: empirical entropy ceiling for value residuals.
//
// Question: before writing a new value codec, how many bits per value could a
// transform plus an entropy coder save over the best in-tree codec (ALP,
// Gorilla, Chimp) on the tests/measurev2 profile catalog?
//
// Cost model (bits per column):
//   - Baselines (gorilla, chimp, alp) are the production encoders' byte counts.
//   - "FOR" streams cost header + n·width, the bit-packing ALP does today.
//   - "adapt" streams are realizable: each zigzag residual r is split into a
//     bin (bits.Len64(r) plus the next k bits below the MSB), coded with an
//     adaptive Krichevsky–Trofimov estimator, and the remaining low bits are
//     stored raw (the binning Pcodec uses). The estimator starts empty, so the
//     model's learning cost is included; an arithmetic coder lands within a
//     few bits of this per column.
//   - "blkFOR" is FOR with a fresh 64-bit min and 6-bit width per block of 32, 64 or 128 values
//     (best per column): no serial chain, O(1) random access kept.
//   - "s8"/"s16" are strided deltas x[i]-x[i-L] (independent lanes, as a transposed layout codes them);
//     the first L values are FOR-packed as lane bases.
//   - "static" codes the same bins with one static model pooled over every column of a profile:
//     a shared trained table whose cost is ignored, so it is an optimistic bound.
//     Order-0 entropy of raw residual symbols is NOT reported:
//     with wide residuals nearly every symbol is unique and it collapses toward log2(n).
//   - "ceiling" picks the cheapest realizable stream per column (+3 selector
//     bits); "save%" compares it to the best production codec.
//
// Generator artifact: gauge steps are uniform ±0.5% and counter steps uniform 1..10,
// so residuals are near-uniform over their range
// and entropy coding ties bit-packing by construction.
// Real metrics have peaked residuals; entropy coding conclusions need real data.
// Columns are independent random walks,
// so cross-series correlation is not measurable here either.
//
// Run: go test ./internal/encoding/research -run 'POCEntropyCeiling' -v

import (
	"fmt"
	"math"
	"math/bits"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/encoding/value/alp"
	"github.com/arloliu/mebo/internal/encoding/value/chimp"
)

const (
	entHeaderBits = 120 // same header alpCompress charges
	entFlushBits  = 16  // arithmetic coder termination
	entExcBits    = 64 + 16
	entSelector   = 3 // per-column transform choice
	entMaxBinK    = 3
)

// entMethod indexes the per-column cost table.
type entMethod int

const (
	entGorilla entMethod = iota
	entChimp
	entALP
	entALPModel
	entDeltaFOR
	entDeltaAdapt
	entDDFOR
	entDDAdapt
	entFbitsAdapt
	entXORAdapt
	entRDAdapt
	entBlockFOR
	entStride8FOR
	entStride8Adapt
	entStride16FOR
	entStride16Adapt
	entNumMethods
)

var entMethodNames = [entNumMethods]string{
	"gorilla", "chimp", "alp", "alpModel",
	"ΔFOR", "Δadapt", "ΔΔFOR", "ΔΔadapt", "fbΔadapt", "xorAdapt", "rdAdapt",
	"blkFOR", "s8FOR", "s8adapt", "s16FOR", "s16adapt",
}

// entRealizable lists the methods a real codec could implement; the ceiling is
// the per-column minimum over them.
var entRealizable = []entMethod{
	entDeltaFOR, entDeltaAdapt, entDDFOR, entDDAdapt, entFbitsAdapt, entXORAdapt, entRDAdapt,
	entBlockFOR, entStride8FOR, entStride8Adapt, entStride16FOR, entStride16Adapt,
}

func entZigzag(v int64) uint64 {
	return uint64((v << 1) ^ (v >> 63))
}

func entDiff(xs []int64) []int64 {
	if len(xs) < 2 {
		return nil
	}
	out := make([]int64, len(xs)-1)
	for i := 1; i < len(xs); i++ {
		out[i-1] = xs[i] - xs[i-1]
	}

	return out
}

// entFORBits is the bit-packing cost of xs after subtracting its minimum.
func entFORBits(xs []int64) float64 {
	if len(xs) == 0 {
		return 0
	}
	mn, mx := slices.Min(xs), slices.Max(xs)

	return float64(len(xs) * bits.Len64(uint64(mx-mn)))
}

// entKT is an adaptive Krichevsky–Trofimov estimator over a fixed alphabet.
type entKT struct {
	counts map[uint64]float64
	total  float64
	alpha  float64
	bits   float64
}

func newEntKT(alphabet int) *entKT {
	return &entKT{counts: make(map[uint64]float64), alpha: float64(alphabet)}
}

func (m *entKT) code(sym uint64) {
	c := m.counts[sym]
	m.bits -= math.Log2((c + 0.5) / (m.total + m.alpha/2))
	m.counts[sym] = c + 1
	m.total++
}

// entBinnedBitsK costs xs under bin width k: KT-coded bins plus raw low bits.
func entBinnedBitsK(xs []int64, k int) float64 {
	maxL := 0
	for _, x := range xs {
		maxL = max(maxL, bits.Len64(entZigzag(x)))
	}
	alphabet := 0
	for l := 0; l <= maxL; l++ {
		alphabet += 1 << min(k, max(l-1, 0))
	}
	m := newEntKT(alphabet)
	var raw float64
	for _, x := range xs {
		r := entZigzag(x)
		l := bits.Len64(r)
		extra := min(k, max(l-1, 0))
		low := max(l-1-extra, 0)
		prefix := uint64(0)
		if extra > 0 {
			prefix = (r >> uint(low)) & (1<<uint(extra) - 1)
		}
		m.code(uint64(l)<<8 | prefix)
		raw += float64(low)
	}

	return m.bits + raw + 6 // maxL
}

// entBinnedBits picks the best bin width; 2 bits signal the choice.
func entBinnedBits(xs []int64) float64 {
	best := math.Inf(1)
	for k := 0; k <= entMaxBinK; k++ {
		best = min(best, entBinnedBitsK(xs, k))
	}

	return best + 2 + entFlushBits
}

// entXORAdaptBits costs the Gorilla XOR stream with KT-coded leading-zero and
// meaningful-length symbols; the meaningful bits minus their two implied 1s are raw.
func entXORAdaptBits(values []float64) float64 {
	lzm, lenm := newEntKT(65), newEntKT(64)
	var raw float64
	prev := math.Float64bits(values[0])
	for _, v := range values[1:] {
		p := math.Float64bits(v)
		x := p ^ prev
		prev = p
		if x == 0 {
			lzm.code(64)

			continue
		}
		lz, tz := bits.LeadingZeros64(x), bits.TrailingZeros64(x)
		mlen := 64 - lz - tz
		lzm.code(uint64(lz))
		lenm.code(uint64(mlen - 1))
		raw += float64(max(mlen-2, 0))
	}

	return 64 + lzm.bits + lenm.bits + raw + entFlushBits
}

// entRDAdaptBits costs ALP-RD at its best cut with KT-coded dictionary codes
// and an escape symbol in place of positioned exceptions.
func entRDAdaptBits(values []float64) float64 {
	rd := alpRDCompress(values)
	rbw := uint(rd.rbw)
	freq := make(map[uint64]int, len(values))
	for _, v := range values {
		freq[math.Float64bits(v)>>rbw]++
	}
	lefts := make([]uint64, 0, len(freq))
	for l := range freq {
		lefts = append(lefts, l)
	}
	slices.SortFunc(lefts, func(a, b uint64) int { return freq[b] - freq[a] })
	dict := make(map[uint64]uint64, alpRDDictSize)
	for i, l := range lefts[:min(alpRDDictSize, len(lefts))] {
		dict[l] = uint64(i)
	}
	m := newEntKT(len(dict) + 1)
	var excBits float64
	for _, v := range values {
		code, ok := dict[math.Float64bits(v)>>rbw]
		if !ok {
			code = uint64(len(dict))
			excBits += float64(rd.leftBits)
		}
		m.code(code)
	}

	return 30 + float64(len(dict)*rd.leftBits) + m.bits +
		float64(len(values)*rd.rbw) + excBits + entFlushBits
}

// entBlockFORBits is FOR with a fresh min and width per block, best block size per column.
func entBlockFORBits(xs []int64) float64 {
	best := math.Inf(1)
	for _, b := range []int{32, 64, 128} {
		var total float64
		for i := 0; i < len(xs); i += b {
			total += 64 + 6 + entFORBits(xs[i:min(i+b, len(xs))])
		}
		best = min(best, total)
	}

	return best + 2
}

// entStrideBits codes lane bases (first L values, FOR-packed) plus strided deltas x[i]-x[i-L].
func entStrideBits(xs []int64, lanes int, cost func([]int64) float64) float64 {
	if len(xs) <= lanes {
		return 64 + 6 + entFORBits(xs)
	}
	d := make([]int64, len(xs)-lanes)
	for i := lanes; i < len(xs); i++ {
		d[i-lanes] = xs[i] - xs[i-lanes]
	}

	return 64 + 6 + entFORBits(xs[:lanes]) + cost(d)
}

// entColumn holds one column's costs plus the residual stream fed to the static model.
type entColumn struct {
	cost    [entNumMethods]float64
	residFB []int64 // float-bit deltas, always present
	residIn []int64 // ALP-integer deltas, nil when ALP main does not fit
}

func entMeasureColumn(values []float64, eng endian.EndianEngine) entColumn {
	var col entColumn
	n := len(values)
	col.cost[entGorilla] = float64(8 * gorillaSize(values))
	col.cost[entChimp] = float64(8 * chimpSize(values))
	col.cost[entALP] = float64(8 * len(encodeALPColumn(values, eng)))

	res := alpCompress(values)
	col.cost[entALPModel] = float64(res.totalBits)

	pats := make([]int64, n)
	for i, v := range values {
		pats[i] = int64(math.Float64bits(v))
	}
	fbDelta := entDiff(pats)
	col.cost[entFbitsAdapt] = 64 + entBinnedBits(fbDelta)
	col.cost[entXORAdapt] = entXORAdaptBits(values)
	col.residFB = fbDelta

	col.cost[entRDAdapt] = entRDAdaptBits(values)

	ints := make([]int64, n)
	var prev int64
	for i, v := range values {
		d, exc := alpEncodeOne(v, res.e, res.f)
		if exc {
			d = prev
		}
		ints[i] = d
		prev = d
	}
	excCost := float64(res.nExc * entExcBits)
	d1 := entDiff(ints)
	d2 := entDiff(d1)
	col.cost[entDeltaFOR] = entHeaderBits + 64 + entFORBits(d1) + excCost
	col.cost[entDeltaAdapt] = entHeaderBits + 64 + entBinnedBits(d1) + excCost
	col.cost[entDDFOR] = entHeaderBits + 128 + entFORBits(d2) + excCost
	col.cost[entDDAdapt] = entHeaderBits + 128 + entBinnedBits(d2) + excCost
	col.cost[entBlockFOR] = entHeaderBits + entBlockFORBits(ints) + excCost
	col.cost[entStride8FOR] = entHeaderBits + entStrideBits(ints, 8, entFORBits) + excCost
	col.cost[entStride8Adapt] = entHeaderBits + entStrideBits(ints, 8, entBinnedBits) + excCost
	col.cost[entStride16FOR] = entHeaderBits + entStrideBits(ints, 16, entFORBits) + excCost
	col.cost[entStride16Adapt] = entHeaderBits + entStrideBits(ints, 16, entBinnedBits) + excCost
	if res.nExc*2 <= n {
		col.residIn = d1
	}

	return col
}

// entStaticBinnedBits costs xs under one static model of its bin frequencies (table cost ignored).
func entStaticBinnedBits(xs []int64, k int) float64 {
	counts := make(map[uint64]int)
	var raw float64
	for _, x := range xs {
		r := entZigzag(x)
		l := bits.Len64(r)
		extra := min(k, max(l-1, 0))
		low := max(l-1-extra, 0)
		prefix := uint64(0)
		if extra > 0 {
			prefix = (r >> uint(low)) & (1<<uint(extra) - 1)
		}
		counts[uint64(l)<<8|prefix]++
		raw += float64(low)
	}
	n := float64(len(xs))
	h := raw
	for _, c := range counts {
		h -= float64(c) * math.Log2(float64(c)/n)
	}

	return h
}

// entRow aggregates one (profile, n) cell.
type entRow struct {
	profile   string
	n, cols   int
	sum       [entNumMethods]float64
	ceiling   float64
	static    float64
	bestShare map[entMethod]int
}

func entMeasureProfile(p alpv2Profile, nCols, nPts int, seed int64, eng endian.EndianEngine) entRow {
	row := entRow{profile: p.name, n: nPts, cols: nCols, bestShare: make(map[entMethod]int)}
	poolIn := make([]int64, 0, nCols*nPts)
	poolFB := make([]int64, 0, nCols*nPts)
	allIn := true
	for _, values := range alpv2GenColumns(p, nCols, nPts, seed) {
		col := entMeasureColumn(values, eng)
		for m := range entNumMethods {
			row.sum[m] += col.cost[m]
		}
		best, bestM := math.Inf(1), entMethod(-1)
		for _, m := range entRealizable {
			if col.cost[m] < best {
				best, bestM = col.cost[m], m
			}
		}
		row.ceiling += best + entSelector
		row.bestShare[bestM]++
		allIn = allIn && col.residIn != nil
		poolIn = append(poolIn, col.residIn...)
		poolFB = append(poolFB, col.residFB...)
	}
	// Pool one residual space only: ALP-integer deltas when every column fits ALP main,
	// float-bit deltas otherwise.
	pool, head := poolFB, 64
	if allIn {
		pool, head = poolIn, entHeaderBits+64
	}
	row.static = math.Inf(1)
	for k := 0; k <= entMaxBinK; k++ {
		row.static = min(row.static, entStaticBinnedBits(pool, k))
	}
	row.static += float64(nCols * head)

	return row
}

func entBPP(bitsTotal float64, points int) string {
	if math.IsInf(bitsTotal, 1) {
		return "n/a"
	}

	return fmt.Sprintf("%.2f", bitsTotal/8/float64(points))
}

func entLogRow(t *testing.T, r entRow) {
	t.Helper()
	pts := r.n * r.cols
	best := min(r.sum[entGorilla], r.sum[entChimp], r.sum[entALP])
	cells := make([]string, 0, entNumMethods+4)
	for m := range entNumMethods {
		cells = append(cells, fmt.Sprintf("%9s", entBPP(r.sum[m], pts)))
	}
	cells = append(cells,
		fmt.Sprintf("%9s", entBPP(r.static, pts)),
		fmt.Sprintf("%9s", entBPP(best, pts)),
		fmt.Sprintf("%9s", entBPP(r.ceiling, pts)),
		fmt.Sprintf("%+8.1f%%", 100*(r.ceiling-best)/best))
	share := make([]string, 0, len(r.bestShare))
	for _, m := range entRealizable {
		if c := r.bestShare[m]; c > 0 {
			share = append(share, fmt.Sprintf("%s=%d", entMethodNames[m], c))
		}
	}
	t.Logf("%-19s %5d %4d |%s | %s", r.profile, r.n, r.cols, strings.Join(cells, ""), strings.Join(share, " "))
}

// TestPOCEntropyCeiling reports bytes/point per method, per profile and column length.
// Decision rule (stated before the numbers):
// a realizable ceiling at least 15% below the best production codec
// on the decimal profiles at n=200 justifies a residual-coding spec;
// otherwise drop the direction until real data shows peaked residuals.
func TestPOCEntropyCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("research PoC; run without -short")
	}
	eng := endian.GetLittleEndianEngine()
	shapes := []struct{ nPts, nCols int }{{150, 200}, {200, 200}, {1024, 40}, {8500, 10}}

	header := make([]string, 0, entNumMethods+4)
	for m := range entNumMethods {
		header = append(header, fmt.Sprintf("%9s", entMethodNames[m]))
	}
	header = append(header, fmt.Sprintf("%9s", "static"), fmt.Sprintf("%9s", "best"),
		fmt.Sprintf("%9s", "ceiling"), fmt.Sprintf("%9s", "save%"))
	t.Logf("bytes/point; save%% = ceiling vs best of gorilla/chimp/alp (negative = smaller)")
	t.Logf("%-19s %5s %4s |%s | ceiling picks", "profile", "n", "cols", strings.Join(header, ""))

	for _, sh := range shapes {
		for i, p := range alpv2Profiles() {
			seed := int64(20261003) + int64(i)*1009 + int64(sh.nPts)
			entLogRow(t, entMeasureProfile(p, sh.nCols, sh.nPts, seed, eng))
		}
	}
}

// entAnchorKs are the anchor spacings measured for delta-coded ALP integers.
var entAnchorKs = []int{16, 32, 64, 128}

// entALPInts returns ALP integers under the column's best (e, f), with exceptions filled by the previous value.
func entALPInts(values []float64) ([]int64, alpResult) {
	res := alpCompress(values)
	ints := make([]int64, len(values))
	var prev int64
	for i, v := range values {
		d, exc := alpEncodeOne(v, res.e, res.f)
		if exc {
			d = prev
		}
		ints[i] = d
		prev = d
	}

	return ints, res
}

// entAnchoredDeltaBits costs delta-coded ALP integers with an absolute anchor every k values,
// so ValueAt sums at most k-1 deltas.
// Anchors are FOR-packed against the column minimum (64-bit min + 6-bit width);
// deltas share one FOR width whose min and width live in the ALP header.
func entAnchoredDeltaBits(ints []int64, k int) float64 {
	nAnch := (len(ints) + k - 1) / k
	deltas := make([]int64, 0, len(ints)-nAnch)
	for i := 1; i < len(ints); i++ {
		if i%k != 0 {
			deltas = append(deltas, ints[i]-ints[i-1])
		}
	}
	wInt := bits.Len64(uint64(slices.Max(ints) - slices.Min(ints)))

	return 64 + 6 + float64(nAnch*wInt) + entFORBits(deltas)
}

// entRuns splits values into run values and run lengths.
func entRuns(values []float64) ([]float64, []int64) {
	vals := make([]float64, 0, len(values))
	lens := make([]int64, 0, len(values))
	for i, v := range values {
		if i > 0 && math.Float64bits(v) == math.Float64bits(vals[len(vals)-1]) {
			lens[len(lens)-1]++

			continue
		}
		vals = append(vals, v)
		lens = append(lens, 1)
	}

	return vals, lens
}

// entRLEBits is RLE in front of a value coder: run values through code,
// run lengths minus one bit-packed (16-bit run count + 6-bit width).
func entRLEBits(values []float64, code func([]float64) float64) float64 {
	vals, lens := entRuns(values)
	w := bits.Len64(uint64(slices.Max(lens) - 1))

	return 16 + 6 + code(vals) + float64(len(lens)*w)
}

// entDictALPBits is a dictionary of distinct values (ALP-coded) plus bit-packed codes.
func entDictALPBits(values []float64) float64 {
	seen := make(map[uint64]struct{}, len(values))
	dict := make([]float64, 0, len(values))
	for _, v := range values {
		b := math.Float64bits(v)
		if _, ok := seen[b]; !ok {
			seen[b] = struct{}{}
			dict = append(dict, v)
		}
	}

	return 16 + float64(alpCompress(dict).totalBits) + float64(len(values)*alpCodeBitsOf(len(dict)))
}

func alpCodeBitsOf(n int) int {
	if n <= 1 {
		return 0
	}

	return bits.Len64(uint64(n - 1))
}

func entALPModelBits(values []float64) float64 {
	return float64(alpCompress(values).totalBits)
}

func entDeltaFORBits(values []float64) float64 {
	ints, res := entALPInts(values)

	return entHeaderBits + 64 + entFORBits(entDiff(ints)) + float64(res.nExc*entExcBits)
}

// entAnchorLabels names the columns of TestPOCAnchorAndLWC after the three baselines.
func entAnchorLabels() []string {
	labels := []string{"alpModel", "ΔFOR"}
	for _, k := range entAnchorKs {
		labels = append(labels, fmt.Sprintf("Δk%d", k))
	}

	return append(labels, "RLE+ALP", "Dict+ALP", "LWC+ALP", "RLE+ΔFOR")
}

// entAnchorLWCColumn returns one column's costs in entAnchorLabels order.
func entAnchorLWCColumn(values []float64) []float64 {
	ints, res := entALPInts(values)
	exc := float64(res.nExc * entExcBits)
	alpModel := float64(res.totalBits)
	out := []float64{alpModel, entDeltaFORBits(values)}
	for _, k := range entAnchorKs {
		out = append(out, entHeaderBits+entAnchoredDeltaBits(ints, k)+exc)
	}
	rle := entRLEBits(values, entALPModelBits)
	dict := entDictALPBits(values)
	lwc := min(alpModel, rle, dict) + 2

	return append(out, rle, dict, lwc, entRLEBits(values, entDeltaFORBits))
}

// TestPOCAnchorAndLWC measures two follow-ups to the entropy ceiling at mebo's real blob size (~150 points):
// what ValueAt anchors cost a delta-coded ALP column,
// and what the ALP paper's LWC+ALP (RLE or dictionary in front of ALP) saves.
// Decision rule (stated before the numbers):
// delta coding is worth a spec only if the anchored variant with k <= 32
// still saves at least 15% against the best production codec on decimal_gauge_2dp at n=150.
func TestPOCAnchorAndLWC(t *testing.T) {
	if testing.Short() {
		t.Skip("research PoC; run without -short")
	}
	eng := endian.GetLittleEndianEngine()
	shapes := []struct{ nPts, nCols int }{{150, 200}, {200, 200}, {1024, 40}}
	labels := entAnchorLabels()

	head := make([]string, 0, len(labels)+2)
	for _, l := range labels {
		head = append(head, fmt.Sprintf("%9s", l))
	}
	t.Logf("bytes/point; %% columns compare against best of gorilla/chimp/alp (negative = smaller)")
	t.Logf("%-19s %5s |%9s%s | %8s %8s %8s", "profile", "n", "best", strings.Join(head, ""), "Δk32%", "LWC%", "RLEΔ%")

	for _, sh := range shapes {
		for i, p := range alpv2Profiles() {
			seed := int64(20261003) + int64(i)*1009 + int64(sh.nPts)
			var best float64
			sums := make([]float64, len(labels))
			for _, values := range alpv2GenColumns(p, sh.nCols, sh.nPts, seed) {
				best += 8 * float64(min(gorillaSize(values), chimpSize(values), len(encodeALPColumn(values, eng))))
				for j, c := range entAnchorLWCColumn(values) {
					sums[j] += c
				}
			}
			pts := sh.nPts * sh.nCols
			cells := make([]string, 0, len(labels))
			for _, s := range sums {
				cells = append(cells, fmt.Sprintf("%9s", entBPP(s, pts)))
			}
			pct := func(label string) string {
				j := slices.Index(labels, label)

				return fmt.Sprintf("%+7.1f%%", 100*(sums[j]-best)/best)
			}
			t.Logf("%-19s %5d |%9s%s | %8s %8s %8s", p.name, sh.nPts, entBPP(best, pts), strings.Join(cells, ""),
				pct("Δk32"), pct("LWC+ALP"), pct("RLE+ΔFOR"))
		}
	}
}

// rleCalibrated generates nCols gauges that hold the previous value with probability hold
// and otherwise take a uniform ±stepPct% step, quantized to dec decimals.
// The shapes below were calibrated so production's encoder settings give Chimp ≈ 2–4.5 B/point,
// bracketing the ~3.3 B/point production reports.
func rleCalibrated(nCols, nPts int, hold, stepPct float64, dec int, seed int64) [][]float64 {
	rng := rand.New(rand.NewSource(seed))
	scale := math.Pow(10, float64(dec))
	cols := make([][]float64, nCols)
	for c := range cols {
		col := make([]float64, nPts)
		cur := 100.0 + float64(c)*10.0
		for j := range col {
			if j == 0 || rng.Float64() >= hold {
				cur += cur * (rng.Float64()*2 - 1) * stepPct / 100
			}
			col[j] = math.Round(cur*scale) / scale
		}
		cols[c] = col
	}

	return cols
}

// rleShape names one data shape for TestPOCRLEALPVariants.
type rleShape struct {
	name string
	gen  func(nCols, nPts int, seed int64) [][]float64
}

func rleShapes() []rleShape {
	shapes := make([]rleShape, 0, 12)
	for _, p := range alpv2Profiles() {
		shapes = append(shapes, rleShape{p.name, func(nCols, nPts int, seed int64) [][]float64 {
			return alpv2GenColumns(p, nCols, nPts, seed)
		}})
	}
	cal := []struct {
		name          string
		hold, stepPct float64
		dec           int
	}{
		{"cal_2dp_hold30", 0.3, 0.5, 2},
		{"cal_2dp_hold50", 0.5, 0.5, 2},
		{"cal_2dp_hold70", 0.7, 0.5, 2},
		{"cal_2dp_step0.005", 0, 0.005, 2},
		{"cal_1dp_step0.03", 0, 0.03, 1},
		{"cal_1dp_step0.01", 0, 0.01, 1},
	}
	for _, c := range cal {
		shapes = append(shapes, rleShape{c.name, func(nCols, nPts int, seed int64) [][]float64 {
			return rleCalibrated(nCols, nPts, c.hold, c.stepPct, c.dec, seed)
		}})
	}

	return shapes
}

// TestPOCRLEALPVariants compares two ways to put RLE in front of the production ALP encoder at n=150:
// run lengths bit-packed (smaller when runs are long, ValueAt needs a search),
// or a run-start bitmap of one bit per point (ValueAt stays O(1) via rank/popcount).
// All ALP costs use the real production encoder; chimp and gorilla are production byte counts.
// Decision rule (stated before the numbers):
// RLE+ALP earns an encoding byte only if, with per-column selection,
// it is never worse than plain ALP by more than its 2-bit selector
// and it beats Chimp on every calibrated production-like shape.
func TestPOCRLEALPVariants(t *testing.T) {
	if testing.Short() {
		t.Skip("research PoC; run without -short")
	}
	const nPts, nCols = 150, 100
	eng := endian.GetLittleEndianEngine()
	alpBits := func(v []float64) float64 { return float64(8 * len(encodeALPColumn(v, eng))) }

	t.Logf("values only, bytes/point, n=%d, %d columns", nPts, nCols)
	t.Logf("%-19s %7s %7s %7s | %7s %7s %7s %7s | %6s %8s", "shape", "chimp", "gorilla", "alp",
		"rleLen", "rleBmp", "lwcLen", "lwcBmp", "runs/n", "bmp-len")
	for i, sh := range rleShapes() {
		var chimpB, gorB, alpB, lenB, bmpB, lwcLen, lwcBmp float64
		var runs int
		for _, v := range sh.gen(nCols, nPts, int64(20261004)+int64(i)*1009) {
			a := alpBits(v)
			vals, lens := entRuns(v)
			runs += len(vals)
			w := bits.Len64(uint64(slices.Max(lens) - 1))
			rv := alpBits(vals)
			l := 16 + 6 + rv + float64(len(lens)*w)
			b := 16 + rv + float64(len(v))
			chimpB += float64(8 * chimpSize(v))
			gorB += float64(8 * gorillaSize(v))
			alpB += a
			lenB += l
			bmpB += b
			lwcLen += min(a, l) + 2
			lwcBmp += min(a, b) + 2
		}
		pts := float64(nPts * nCols)
		bpp := func(x float64) float64 { return x / 8 / pts }
		t.Logf("%-19s %7.2f %7.2f %7.2f | %7.2f %7.2f %7.2f %7.2f | %6.2f %+8.2f", sh.name,
			bpp(chimpB), bpp(gorB), bpp(alpB), bpp(lenB), bpp(bmpB), bpp(lwcLen), bpp(lwcBmp),
			float64(runs)/pts, bpp(bmpB-lenB))
	}
}

// rleProtoColumn is one column prepared both ways: plain ALP and the scheme-3 runs layout
// (a run-start bitmap plus an ALP column of run values).
type rleProtoColumn struct {
	n, nRuns int
	plain    []byte
	nested   []byte
	bitmap   []uint64
}

func rleProtoColumns(cols [][]float64, eng endian.EndianEngine) []rleProtoColumn {
	out := make([]rleProtoColumn, 0, len(cols))
	for _, v := range cols {
		vals, _ := entRuns(v)
		bm := make([]uint64, (len(v)+63)/64)
		for i := range v {
			if i == 0 || math.Float64bits(v[i]) != math.Float64bits(v[i-1]) {
				bm[i>>6] |= 1 << uint(i&63)
			}
		}
		out = append(out, rleProtoColumn{
			n: len(v), nRuns: len(vals),
			plain: encodeALPColumn(v, eng), nested: encodeALPColumn(vals, eng), bitmap: bm,
		})
	}

	return out
}

// rleExpand writes runs[rank(i)-1] to dst[i] without branches.
func rleExpand(bm []uint64, runs, dst []float64) {
	idx := -1
	for i := range dst {
		idx += int(bm[i>>6] >> uint(i&63) & 1)
		dst[i] = runs[idx]
	}
}

// rleRank returns the number of set bits in bm[0..i].
func rleRank(bm []uint64, i int) int {
	r := 0
	for _, w := range bm[:i>>6] {
		r += bits.OnesCount64(w)
	}

	return r + bits.OnesCount64(bm[i>>6]<<uint(63-i&63))
}

// BenchmarkPOCRLEDecode pre-checks the spec's speed gates for the runs layout at 150 points
// on the hold-50% calibrated shape (about half the points repeat).
// Gates: DecodeAll ≤ 1.2× plain, ValueAt ≤ 2× plain; Chimp DecodeAll is the production reference.
// Each op touches all 100 columns; ns/pt is per decoded point (or per lookup for At).
func BenchmarkPOCRLEDecode(b *testing.B) {
	const nPts, nCols = 150, 100
	eng := endian.GetLittleEndianEngine()
	dec := alp.NewNumericALPDecoder(eng)
	cols := rleProtoColumns(rleCalibrated(nCols, nPts, 0.5, 0.5, 2, 7), eng)
	dst := make([]float64, nPts)
	scratch := make([]float64, nPts)
	idx := make([]int, nCols)
	rng := rand.New(rand.NewSource(11))
	for i := range idx {
		idx[i] = rng.Intn(nPts)
	}

	b.Run("DecodeAll/plain", func(b *testing.B) {
		for b.Loop() {
			for _, c := range cols {
				dec.DecodeAll(c.plain, c.n, dst)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*nPts*nCols), "ns/pt")
	})
	b.Run("DecodeAll/runs", func(b *testing.B) {
		for b.Loop() {
			for _, c := range cols {
				dec.DecodeAll(c.nested, c.nRuns, scratch)
				rleExpandWords(c.bitmap, scratch, dst[:c.n])
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*nPts*nCols), "ns/pt")
	})
	b.Run("DecodeAll/chimp", func(b *testing.B) {
		cd := chimp.NewNumericChimpDecoder()
		raw := rleCalibrated(nCols, nPts, 0.5, 0.5, 2, 7)
		bufs := make([][]byte, len(raw))
		for i, v := range raw {
			enc := chimp.NewNumericChimpEncoder()
			enc.WriteSlice(v)
			bufs[i] = append([]byte(nil), enc.Bytes()...)
			enc.Finish()
		}
		for b.Loop() {
			for _, buf := range bufs {
				cd.DecodeAll(buf, nPts, dst)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*nPts*nCols), "ns/pt")
	})
	b.Run("At/plain", func(b *testing.B) {
		var sink float64
		for b.Loop() {
			for j, c := range cols {
				v, _ := dec.At(c.plain, idx[j], c.n)
				sink += v
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*nCols), "ns/lookup")
		_ = sink
	})
	b.Run("At/runs", func(b *testing.B) {
		var sink float64
		for b.Loop() {
			for j, c := range cols {
				v, _ := dec.At(c.nested, rleRank(c.bitmap, idx[j])-1, c.nRuns)
				sink += v
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*nCols), "ns/lookup")
		_ = sink
	})
}

// BenchmarkPOCRLEEncodeRunFree pre-checks the encode gate (≤ 1.1× plain) on 2-decimal gauges with almost no repeats.
// The runs encoder always encodes the plain column and counts runs.
// It encodes the run values only when rleShouldTry says the repeats could pay for the runs overhead.
func BenchmarkPOCRLEEncodeRunFree(b *testing.B) {
	const nPts, nCols = 150, 100
	eng := endian.GetLittleEndianEngine()
	cols := generateALPColumns(nCols, nPts, 2, 7)

	b.Run("plain", func(b *testing.B) {
		for b.Loop() {
			for _, v := range cols {
				_ = encodeALPColumn(v, eng)
			}
		}
	})
	b.Run("runs", func(b *testing.B) {
		for b.Loop() {
			for _, v := range cols {
				plain := encodeALPColumn(v, eng)
				runs := 1
				for i := 1; i < len(v); i++ {
					if math.Float64bits(v[i]) != math.Float64bits(v[i-1]) {
						runs++
					}
				}
				if runs < len(v) && rleShouldTry(v, plain) {
					vals, _ := entRuns(v)
					_ = encodeALPColumn(vals, eng)
				}
			}
		}
	})
}

// rleExpandWords is rleExpand processed one bitmap word at a time with bounds checks hoisted.
// Like ALP's DecodeAll, it writes min(points, len(dst)) values and stops at the destination's end.
func rleExpandWords(bm []uint64, runs, dst []float64) {
	idx := -1
	for w, word := range bm {
		base := w * 64
		if base >= len(dst) {
			return
		}
		end := min(base+64, len(dst))
		out := dst[base:end]
		for j := range out {
			idx += int(word & 1)
			word >>= 1
			out[j] = runs[idx]
		}
	}
}

// rleExpandFill walks run starts with TrailingZeros and fills each run's span,
// stopping at the destination's end.
func rleExpandFill(bm []uint64, runs, dst []float64) {
	r := 0
	prev := 0
	for w, word := range bm {
		for word != 0 {
			start := w*64 + bits.TrailingZeros64(word)
			word &= word - 1
			if start >= len(dst) {
				break
			}
			if start > 0 {
				v := runs[r-1]
				for k := prev; k < start; k++ {
					dst[k] = v
				}
			}
			prev = start
			r++
		}
	}
	if r == 0 {
		return
	}
	v := runs[r-1]
	for k := prev; k < len(dst); k++ {
		dst[k] = v
	}
}

// BenchmarkPOCRLEExpand isolates the expansion step and tries two faster shapes.
func BenchmarkPOCRLEExpand(b *testing.B) {
	const nPts, nCols = 150, 100
	eng := endian.GetLittleEndianEngine()
	dec := alp.NewNumericALPDecoder(eng)
	for _, hold := range []float64{0.3, 0.5, 0.7} {
		cols := rleProtoColumns(rleCalibrated(nCols, nPts, hold, 0.5, 2, 7), eng)
		runs := make([][]float64, len(cols))
		for i, c := range cols {
			runs[i] = make([]float64, c.nRuns)
			dec.DecodeAll(c.nested, c.nRuns, runs[i])
		}
		dst := make([]float64, nPts)
		want := make([]float64, nPts)
		for name, f := range map[string]func(bm []uint64, runs, dst []float64){
			"branchless": rleExpand, "words": rleExpandWords, "fill": rleExpandFill,
		} {
			for i, c := range cols {
				rleExpand(c.bitmap, runs[i], want)
				f(c.bitmap, runs[i], dst)
				if !slices.Equal(want, dst) {
					b.Fatalf("%s: expansion mismatch on column %d", name, i)
				}
			}
			b.Run(fmt.Sprintf("hold%.0f/%s", hold*100, name), func(b *testing.B) {
				for b.Loop() {
					for i, c := range cols {
						f(c.bitmap, runs[i], dst)
					}
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*nPts*nCols), "ns/pt")
			})
		}
	}
}

// rlePlainPointCost returns, for a plain ALP column of count values (little-endian),
// the bits each point costs on its own: packed bits plus its exception entry, if any.
// Removing a repeated point from the column saves at most this much.
func rlePlainPointCost(col []byte, count int) []int {
	costs := make([]int, count)
	le := endian.GetLittleEndianEngine()
	switch col[0] {
	case 0: // main: [e][f][width][nExc:4][min:8] codes, exceptions (pos:4, value:8)
		width := int(col[3])
		nExc := int(le.Uint32(col[4:8]))
		excStart := 16 + (count*width+7)/8
		for i := range costs {
			costs[i] = width
		}
		for k := range nExc {
			costs[le.Uint32(col[excStart+12*k:])] += 96
		}
	case 1: // rd: [rbw][codeBits][nDict][nExc:4] dict, lefts, rights, exceptions (pos:4, left:2)
		rbw, codeBits, nDict := int(col[1]), int(col[2]), int(col[3])
		nExc := int(le.Uint32(col[4:8]))
		excStart := 8 + 2*nDict + (count*codeBits+7)/8 + (count*rbw+7)/8
		for i := range costs {
			costs[i] = rbw + codeBits
		}
		for k := range nExc {
			costs[le.Uint32(col[excStart+6*k:])] += 48
		}
	default: // raw
		for i := range costs {
			costs[i] = 64
		}
	}

	return costs
}

// rleRunsColumnBytes is the exact scheme-3 size: scheme byte, uint32 nRuns, the byte-rounded bitmap, then the nested column.
func rleRunsColumnBytes(count int, nested []byte) int {
	return 1 + 4 + (count+7)/8 + len(nested)
}

// rleShouldTry is the encoder's pruning rule: try the runs layout only when the
// removed repeats could save more than the runs overhead (5 bytes + bitmap).
func rleShouldTry(v []float64, plain []byte) bool {
	costs := rlePlainPointCost(plain, len(v))
	saved := 0
	for i := 1; i < len(v); i++ {
		if math.Float64bits(v[i]) == math.Float64bits(v[i-1]) {
			saved += costs[i]
		}
	}

	return saved > 8*(5+(len(v)+7)/8)
}

// TestPOCRLEALPExact recomputes the ratio gate with the exact scheme-3 byte layout,
// comparing "always try the runs layout" with the pruning rule.
// The pruned column must never be larger than plain ALP by construction;
// the gap between pruned and always-try is the heuristic's ratio loss.
func TestPOCRLEALPExact(t *testing.T) {
	if testing.Short() {
		t.Skip("research PoC; run without -short")
	}
	const nPts, nCols = 150, 100
	eng := endian.GetLittleEndianEngine()

	// The review's counterexample: repeated exceptions cost far more than the average point.
	ce := make([]float64, 0, 150)
	for i := range 146 {
		ce = append(ce, float64(1+i%2))
	}
	ce = append(ce, math.Inf(1), math.Inf(1), math.Inf(1), math.Inf(1))
	cePlain := encodeALPColumn(ce, eng)
	ceVals, _ := entRuns(ce)
	ceRuns := rleRunsColumnBytes(len(ce), encodeALPColumn(ceVals, eng))
	t.Logf("counterexample: plain %dB, runs %dB, pruning tries runs: %v", len(cePlain), ceRuns, rleShouldTry(ce, cePlain))
	if ceRuns >= len(cePlain) || !rleShouldTry(ce, cePlain) {
		t.Errorf("counterexample: want runs smaller than plain and tried, got plain %dB runs %dB", len(cePlain), ceRuns)
	}

	t.Logf("values only, bytes/point, n=%d, %d columns", nPts, nCols)
	t.Logf("%-19s %7s %7s | %7s %7s %7s | %7s %6s", "shape", "chimp", "alp", "always", "pruned", "loss%", "vsChimp", "tried")
	for i, sh := range rleShapes() {
		var chimpB, alpB, always, pruned float64
		tried := 0
		for _, v := range sh.gen(nCols, nPts, int64(20261004)+int64(i)*1009) {
			plain := encodeALPColumn(v, eng)
			vals, _ := entRuns(v)
			best := len(plain)
			if len(vals) < len(v) {
				best = min(best, rleRunsColumnBytes(len(v), encodeALPColumn(vals, eng)))
			}
			chosen := len(plain)
			if rleShouldTry(v, plain) {
				tried++
				chosen = best
			}
			if chosen > len(plain) {
				t.Errorf("%s: column larger than plain ALP (%dB > %dB)", sh.name, chosen, len(plain))
			}
			chimpB += float64(chimpSize(v))
			alpB += float64(len(plain))
			always += float64(best)
			pruned += float64(chosen)
		}
		pts := float64(nPts * nCols)
		t.Logf("%-19s %7.2f %7.2f | %7.2f %7.2f %+6.2f%% | %+6.1f%% %3d/%d", sh.name,
			chimpB/pts, alpB/pts, always/pts, pruned/pts, 100*(pruned-always)/always, 100*(pruned-chimpB)/chimpB, tried, nCols)
		// Gate: calibrated production-like shapes beat Chimp; pruning costs at most 1% against always trying.
		if strings.HasPrefix(sh.name, "cal_") && pruned >= chimpB {
			t.Errorf("%s: ALP-RLE %.2f B/pt does not beat Chimp %.2f", sh.name, pruned/pts, chimpB/pts)
		}
		if pruned > always*1.01 {
			t.Errorf("%s: pruning loss %.2f%% exceeds 1%%", sh.name, 100*(pruned-always)/always)
		}
	}
}

// TestPOCRLEExpandShortDst checks every expansion against the per-point reference
// for empty, short (ending inside and at a bitmap word) and exact destinations.
func TestPOCRLEExpandShortDst(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	dec := alp.NewNumericALPDecoder(eng)
	for _, c := range rleProtoColumns(rleCalibrated(4, 150, 0.5, 0.5, 2, 9), eng) {
		runs := make([]float64, c.nRuns)
		dec.DecodeAll(c.nested, c.nRuns, runs)
		full := make([]float64, c.n)
		rleExpand(c.bitmap, runs, full)
		for _, n := range []int{0, 1, 63, 64, 65, 127, 128, 149, 150} {
			for name, f := range map[string]func(bm []uint64, runs, dst []float64){
				"branchless": rleExpand, "words": rleExpandWords, "fill": rleExpandFill,
			} {
				dst := make([]float64, n)
				f(c.bitmap, runs, dst)
				if !slices.Equal(dst, full[:n]) {
					t.Fatalf("%s: len(dst)=%d: got %v, want %v", name, n, dst, full[:n])
				}
			}
		}
	}
}
