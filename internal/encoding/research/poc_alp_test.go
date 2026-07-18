package research

import (
	"math"
	"math/bits"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/encoding/value/alp"
	"github.com/arloliu/mebo/internal/encoding/value/chimp"
	"github.com/arloliu/mebo/internal/encoding/value/gorilla"
)

func encodeALPColumn(values []float64, eng endian.EndianEngine) []byte {
	encoder := alp.NewNumericALPEncoder(eng)
	encoder.WriteSlice(values)
	data := append([]byte(nil), encoder.Bytes()...)
	encoder.Finish()

	return data
}

func generateALPColumns(nCols, nPts, decimals int, seed int64) [][]float64 {
	rng := rand.New(rand.NewSource(seed))
	scale := math.Pow(10, float64(decimals))
	columns := make([][]float64, nCols)
	for i := range columns {
		column := make([]float64, nPts)
		current := 100.0 + float64(i)*10.0
		for j := range column {
			current += current * (rng.Float64()*2.0 - 1.0) * 0.005
			if decimals >= 0 {
				column[j] = math.Round(current*scale) / scale
			} else {
				column[j] = current
			}
		}
		columns[i] = column
	}

	return columns
}

// PoC: ALP (Adaptive Lossless floating-Point) value compression.
//
// Reference: Afroozeh, Kuffó, Boncz — "ALP: Adaptive Lossless floating-Point
// Compression", SIGMOD 2024 (cwida/ALP, MIT). This is a SMALLEST-PoC of the ALP
// *main* scheme only (decimal decomposition + Frame-of-Reference + bit-packing +
// exception patching). It exists to settle the one question the literature could
// NOT answer: ALP's actual compression RATIO on mebo-shaped float64 data vs the
// in-tree Gorilla and Chimp codecs.
//
// NOTE ON SPEED: ALP's headline "1-2 orders of magnitude faster" comes from SIMD
// auto-vectorization of dependency-free scalar C++. This Go PoC is plain scalar
// bit-twiddling and does NOT attempt to reproduce that; the encode/decode timing
// below is a scalar sanity check only. The ratio numbers, however, are exact.
//
// Run:
//   go test ./internal/encoding/research -run 'POCALP' -v
//   go test ./internal/encoding/research -run x -bench 'POCALP' -benchmem

var alpEXP = [...]float64{
	1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
	1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18,
}

var alpFRAC = [...]float64{
	1e0, 1e-1, 1e-2, 1e-3, 1e-4, 1e-5, 1e-6, 1e-7, 1e-8, 1e-9,
	1e-10, 1e-11, 1e-12, 1e-13, 1e-14, 1e-15, 1e-16, 1e-17, 1e-18,
}

const alpMaxExp = 18

// alpEncodeOne computes i = round(v * 10^e / 10^f) and reports whether v is an
// exception (does not round-trip bit-exactly under the (e,f) pair).
func alpEncodeOne(v float64, e, f int) (int64, bool) {
	scaled := v * alpEXP[e] * alpFRAC[f]
	r := math.Round(scaled)
	if math.Abs(r) >= 9.2e18 { // int64 overflow guard
		return 0, true
	}
	i := int64(r)
	// decode: v' = i * 10^f / 10^e
	dec := float64(i) * alpEXP[f] * alpFRAC[e]
	if dec != v {
		return 0, true
	}

	return i, false
}

// alpAnalyze searches (e,f) with f<=e minimizing estimated compressed bits.
func alpAnalyze(values []float64) (bestE, bestF int) {
	n := len(values)
	best := math.MaxFloat64

	for e := 0; e <= alpMaxExp; e++ {
		for f := 0; f <= e; f++ {
			var nExc int
			mn := int64(math.MaxInt64)
			mx := int64(math.MinInt64)
			for _, v := range values {
				enc, exc := alpEncodeOne(v, e, f)
				if exc {
					nExc++
					continue
				}
				if enc < mn {
					mn = enc
				}
				if enc > mx {
					mx = enc
				}
			}
			width := 0
			if nExc < n && mx >= mn {
				width = bits.Len64(uint64(mx - mn))
			}
			estBits := n*width + nExc*(64+16) + 96 // +header
			if float64(estBits) < best {
				best = float64(estBits)
				bestE, bestF = e, f
			}
		}
	}

	return bestE, bestF
}

// alpResult holds an exact compressed-size accounting plus metadata.
type alpResult struct {
	totalBits int
	e, f      int
	width     int
	nExc      int
}

// alpCompress returns the EXACT compressed bit count under the best (e,f).
func alpCompress(values []float64) alpResult {
	n := len(values)
	e, f := alpAnalyze(values)

	mn := int64(math.MaxInt64)
	mx := int64(math.MinInt64)
	var nExc int
	for _, v := range values {
		i, exc := alpEncodeOne(v, e, f)
		if exc {
			nExc++
			continue
		}
		if i < mn {
			mn = i
		}
		if i > mx {
			mx = i
		}
	}
	width := 0
	if nExc < n && mx >= mn {
		width = bits.Len64(uint64(mx - mn))
	}

	// header: e(8) f(8) width(8) nExc(16) n(16) min(64) = 120 bits
	const header = 120
	dataBits := n * width       // full vector bit-packed (exceptions get filler)
	excBits := nExc * (64 + 16) // original float64 + uint16 position
	total := header + dataBits + excBits

	return alpResult{totalBits: total, e: e, f: f, width: width, nExc: nExc}
}

func gorillaSize(values []float64) int {
	enc := gorilla.NewNumericGorillaEncoder()
	enc.WriteSlice(values)
	n := len(enc.Bytes())
	enc.Finish()

	return n
}

func chimpSize(values []float64) int {
	enc := chimp.NewNumericChimpEncoder()
	enc.WriteSlice(values)
	n := len(enc.Bytes())
	enc.Finish()

	return n
}

// alpResearchQuantize rounds values to d decimal places (models real sensors that report
// fixed-precision readings stored as float64, e.g. 45.2, 2048.5).
func alpResearchQuantize(values []float64, d int) []float64 {
	out := make([]float64, len(values))
	scale := math.Pow(10, float64(d))
	for i, v := range values {
		out[i] = math.Round(v*scale) / scale
	}

	return out
}

func alpResearchGenerateSmallJitterMetric(count int, base, driftRate, noiseRate float64) []float64 {
	values := make([]float64, count)
	for i := range values {
		drift := math.Sin(float64(i)*0.07) * base * driftRate
		noise := math.Cos(float64(i)*0.23) * base * noiseRate
		values[i] = base + drift + noise
	}

	return values
}

func generateDriftingMetric(count int, base, slope, noiseRate float64) []float64 {
	values := make([]float64, count)
	for i := range values {
		trend := float64(i) * slope
		noise := math.Sin(float64(i)*0.13) * base * noiseRate
		values[i] = base + trend + noise
	}

	return values
}

func generateIntegerLikeMetric(count int, base, rangeVal, driftRate float64) []float64 {
	values := make([]float64, count)
	for i := range values {
		drift := float64(i) * driftRate
		variation := math.Round(math.Sin(float64(i)*0.19) * rangeVal)
		values[i] = math.Round(base + drift + variation)
	}

	return values
}

// randomWalk reproduces mebo's measurev2 generator shape: ±jitterPct random walk,
// producing full-precision (non-decimal) doubles — ALP's worst case.
func randomWalk(n int, base, jitterPct float64, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	cur := base
	for i := range out {
		cur += cur * (rng.Float64()*2 - 1) * jitterPct / 100
		out[i] = cur
	}

	return out
}

type pocFloatDataset struct {
	name   string
	values []float64
}

func pocFloatDatasets() []pocFloatDataset {
	ds := []pocFloatDataset{
		// --- full-precision realistic (sin/cos noise -> non-decimal doubles) ---
		{"cpu_util_fp", alpResearchGenerateSmallJitterMetric(200, 42.0, 0.003, 0.001)},
		{"mem_drift_fp", generateDriftingMetric(500, 67.2, 0.0006, 0.002)},
		{"temp_fp", alpResearchGenerateSmallJitterMetric(200, 23.4, 0.0005, 0.0002)},
		{"battery_fp", alpResearchGenerateSmallJitterMetric(200, 3.72, 0.0003, 0.0001)},
		// --- integer-like (already rounded) ---
		{"disk_iops_int", generateIntegerLikeMetric(500, 4200, 50, 0.1)},
		// --- random-walk (mebo measurev2 default shape) ---
		{"randwalk_fp", randomWalk(200, 100, 0.5, 42)},
		// --- real JSON sample values (clean 1-decimal sensor data) ---
		{"real_cpu", []float64{45.2, 46.1, 45.8, 47.3, 46.5, 48.2, 47.9, 46.7, 45.5, 46.3}},
		{"real_mem", []float64{2048.5, 2050.1, 2047.8, 2051.2, 2049.6, 2052.3, 2050.8, 2048.1, 2046.9, 2049.4}},
		{"real_latency", []float64{10.5, 11.2, 10.8, 12.1, 11.5}},
	}

	// quantized variants: real sensors usually report 1-2 decimals
	for _, base := range []pocFloatDataset{
		{"cpu_util", alpResearchGenerateSmallJitterMetric(200, 42.0, 0.003, 0.001)},
		{"temp", alpResearchGenerateSmallJitterMetric(200, 23.4, 0.0005, 0.0002)},
		{"net_tput", alpResearchGenerateSmallJitterMetric(300, 950.0, 0.005, 0.002)},
		{"battery", alpResearchGenerateSmallJitterMetric(200, 3.72, 0.0003, 0.0001)},
	} {
		ds = append(ds, pocFloatDataset{base.name + "_q1", alpResearchQuantize(base.values, 1)})
		ds = append(ds, pocFloatDataset{base.name + "_q2", alpResearchQuantize(base.values, 2)})
	}

	return ds
}

func TestPOCALPRatio(t *testing.T) {
	t.Logf("%-22s %6s | %10s %10s %10s | %s",
		"dataset", "n", "gorilla", "chimp", "ALP", "ALP(e,f,w,exc)")
	t.Logf("%s", "-------------------------------------------------------------------------------------")

	for _, d := range pocFloatDatasets() {
		n := len(d.values)
		g := float64(gorillaSize(d.values)) / float64(n)
		c := float64(chimpSize(d.values)) / float64(n)
		r := alpCompress(d.values)
		a := float64(r.totalBits) / 8 / float64(n)

		win := ""
		switch {
		case a < c && a < g:
			win = "  <- ALP best"
		case c < a && c < g:
			win = "  (chimp best)"
		case g < a && g < c:
			win = "  (gorilla best)"
		default:
		}

		t.Logf("%-22s %6d | %9.3fB %9.3fB %9.3fB | e=%d f=%d w=%d exc=%d/%d%s",
			d.name, n, g, c, a, r.e, r.f, r.width, r.nExc, n, win)
	}
}

// ---- scalar speed sanity check (NOT an ALP-SIMD measurement) ----

// alpEncodeBytes produces a real lossless byte stream so we can verify round-trip
// and get a scalar timing. Layout: [e f width][n:u16][nExc:u16][min:i64]
// [packed width-bit FOR codes][nExc*(pos:u16 + val:f64)].
func alpEncodeBytes(values []float64) []byte {
	n := len(values)
	e, f := alpAnalyze(values)
	codes := make([]uint64, n)
	exc := make([]bool, n)
	mn := int64(math.MaxInt64)
	for idx, v := range values {
		i, isExc := alpEncodeOne(v, e, f)
		if isExc {
			exc[idx] = true
			continue
		}
		if i < mn {
			mn = i
		}
		codes[idx] = uint64(i)
	}
	mx := int64(math.MinInt64)
	for idx, v := range values {
		if exc[idx] {
			continue
		}
		d := int64(codes[idx]) - mn
		codes[idx] = uint64(d)
		if int64(codes[idx]) > mx {
			mx = int64(codes[idx])
		}
		_ = v
	}
	width := 0
	if mx >= 0 {
		width = bits.Len64(uint64(mx))
	}

	out := make([]byte, 0, 16+(n*width+7)/8+len(values)*10)
	out = append(out, byte(e), byte(f), byte(width))
	out = append(out, byte(n), byte(n>>8))
	nExc := 0
	for _, b := range exc {
		if b {
			nExc++
		}
	}
	out = append(out, byte(nExc), byte(nExc>>8))
	um := uint64(mn)
	for i := range 8 {
		out = append(out, byte(um>>(8*i)))
	}
	// bit-pack codes (LSB-first)
	packed := make([]byte, (n*width+7)/8)
	bitpos := 0
	if width > 0 {
		for _, code := range codes {
			for b := range width {
				if code&(1<<uint(b)) != 0 {
					packed[bitpos>>3] |= 1 << uint(bitpos&7)
				}
				bitpos++
			}
		}
	}
	out = append(out, packed...)
	for idx, isExc := range exc {
		if !isExc {
			continue
		}
		out = append(out, byte(idx), byte(idx>>8))
		ub := math.Float64bits(values[idx])
		for i := range 8 {
			out = append(out, byte(ub>>(8*i)))
		}
	}

	return out
}

func alpDecodeBytes(buf []byte, n int) []float64 {
	e := int(buf[0])
	f := int(buf[1])
	width := int(buf[2])
	nExc := int(buf[5]) | int(buf[6])<<8
	var mn int64
	um := uint64(0)
	for i := range 8 {
		um |= uint64(buf[7+i]) << (8 * i)
	}
	mn = int64(um)
	off := 15
	out := make([]float64, n)
	bitpos := 0
	for i := range n {
		var code uint64
		for b := range width {
			byteIdx := off + (bitpos >> 3)
			if buf[byteIdx]&(1<<uint(bitpos&7)) != 0 {
				code |= 1 << uint(b)
			}
			bitpos++
		}
		val := int64(code) + mn
		out[i] = float64(val) * alpEXP[f] * alpFRAC[e]
	}
	off += (n*width + 7) / 8
	for range nExc {
		pos := int(buf[off]) | int(buf[off+1])<<8
		off += 2
		ub := uint64(0)
		for j := range 8 {
			ub |= uint64(buf[off+j]) << (8 * j)
		}
		off += 8
		out[pos] = math.Float64frombits(ub)
	}

	return out
}

func TestPOCALPLossless(t *testing.T) {
	for _, d := range pocFloatDatasets() {
		buf := alpEncodeBytes(d.values)
		got := alpDecodeBytes(buf, len(d.values))
		if !slices.Equal(got, d.values) {
			t.Errorf("%s: ALP round-trip not bit-exact", d.name)
		}
	}
}

// PoC: ALP-RD ("Real Double") — ALP's second scheme for full-precision doubles
// that the decimal ALP main scheme can't represent. Reference: cwida/ALP rd.hpp
// + config.hpp (CUTTING_LIMIT=16, MAX_RD_DICTIONARY_SIZE=8).
//
// Goal: prove ALP-RD losslessly compresses the FULL-PRECISION datasets where ALP
// main collapsed (battery_fp: 199/200 exceptions), with ratio competitive with
// Chimp. Run: go test ./internal/encoding/value/alp -run 'POCALPRD' -v

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

// PoC: ALP-v2 per-vector compression ratio measurement.
//
// Background: today's ALP (TypeALP = 0x6) picks ONE (e,f) exponent pair and
// ONE scheme (main/RD/raw) for an entire column. The open design question:
// would splitting each column into independent 1024-value VECTORS, each with
// its own (e,f)/scheme choice and its own header, win enough compression
// ratio on real mixed data to justify a new wire format (encoding type 0x7)?
// The decision rule (2026-07-16): pursue a vectored format only if per-vector
// wins by >= 5% aggregate on real mixed data. This file answers that with a
// number; it does NOT wire anything — no format change, no production code
// touched.
//
// ---- cost model ----
//
// Whole-column ACTUAL: encodeALPColumn(column, eng) — the exact same production
// NumericALPEncoder path TestNumericALP_Ratio (alp_test.go) uses, i.e.
// the real encoder's byte count for the column as ONE unit.
//
// Per-vector ESTIMATE: split the column into ceil(n/1024) vectors of at most
// 1024 values, then run encodeALPColumn on EACH vector slice INDEPENDENTLY and
// sum the byte counts. This is deliberately NOT a hand-derived bit formula:
// calling the real encoder per vector exercises the actual alpBestEF search,
// the actual main/RD/raw selection (encodeColumn), and the actual bit packer,
// once per vector instead of once per column — so "estimate" here means
// "what today's format produces if invoked once per 1024-value vector", and
// every vector's header is the real [scheme][e][f][width][nExc][min] (main) or
// [scheme][rbw][codeBits][nDict][nExc][dict] (RD) or [scheme][raw floats] (raw)
// layout, not an approximation of it. Vectors that would choose RD/raw are
// therefore costed with the exact RD/raw bytes the real encoder produces for
// that vector slice.
//
// Known bias, stated deliberately:
// this reuses today's COLUMN-level field widths unmodified as the per-vector
// header (nExc: 32-bit, exception pos: 32-bit, min: 64-bit) rather than
// right-sizing them for a real ALP-v2 header capped at 1024 values (nExc/pos
// would fit in ~10-11 bits there). This keeps the per-value/per-exception cost
// model identical on both sides (96 bits per exception: 32-bit pos + 64-bit
// value) — but it does mean the per-vector
// ESTIMATE is somewhat PESSIMISTIC relative to a hand-tuned v2 header: if
// per-vector still wins by >=5% under this header cost, a real v2 header would
// likely win by more; if it falls short, a tighter header could still close
// some of the gap and would need a follow-up measurement before concluding
// the question is closed on a knife's edge.
//
// No extra cross-vector framing is added: the number of vectors per column is
// implicit from the column's record count (already known from the index) and a
// fixed 1024 vector size, so a real ALP-v2 column needs no extra byte for vector
// count beyond what's summed here.
//
// ---- datasets ----
//
// tests/measurev2 is a separate Go module (its own go.mod) that imports mebo's
// root module, so internal/encoding/value/alp cannot import it back (would be a module
// cycle) — its loaders are not importable. Instead this mirrors its realistic
// profile catalog (tests/measurev2/types.go Profiles(), generator.go
// GenerateProfile) locally, VALUE-generation only (timestamps aren't ALP's
// domain). Two of the profile's ValueKinds ("gauge", already covered by this
// package's existing generateALPColumns helper in alp_test.go, and
// the "counter"/"sparse" kinds added below) reproduce GenerateProfile's exact
// per-point formulas. Note that regular_scrape_60s and bursty_scrape use the
// SAME value-generation formula and decimals as decimal_gauge_2dp in the real
// corpus too — IntervalMs and BurstyGaps only perturb timestamps, never Values.
// Their reported byte counts still differ row-to-row here because each profile
// below is seeded independently (distinct random-walk realizations over 8500
// steps diverge substantially, not because the underlying value distribution
// differs); they are kept as separate rows for 1:1 traceability against the
// corpus's named profiles.
//
// Columns are generated at 8500 points each (8 full 1024-vectors + one 308-value
// partial tail vector) so the comparison actually exercises multiple vectors per
// column, including the partial-tail case a real vectored format would hit at
// every column boundary.
//
// This test only measures and logs; it PASSES regardless of which side wins —
// the >=5% decision is made by a human reading the table, not by this harness.
//
// Run:
//
//	go test ./internal/encoding/value/alp -run 'POCALPv2' -v

// alpv2VectorSize is the per-vector split point this PoC evaluates (the
// candidate vector size for a vectored ALP format).
const alpv2VectorSize = 1024

// alpv2Profile names a realistic value-generation shape mirrored from
// tests/measurev2/types.go's Profiles() catalog (decimals/valueKind only —
// IntervalMs/BurstyGaps affect timestamps, which are outside ALP's domain).
type alpv2Profile struct {
	name     string
	decimals int
	kind     string // "gauge" | "counter" | "sparse"
}

// alpv2Profiles mirrors tests/measurev2/types.go Profiles() verbatim (names,
// decimals, value kinds).
func alpv2Profiles() []alpv2Profile {
	return []alpv2Profile{
		{"decimal_gauge_2dp", 2, "gauge"},
		{"decimal_gauge_4dp", 4, "gauge"},
		{"counter", 0, "counter"},
		{"sparse_constant", 2, "sparse"},
		{"regular_scrape_60s", 2, "gauge"},
		{"bursty_scrape", 2, "gauge"},
		{"worst_case", -1, "gauge"},
	}
}

// alpv2GenCounterColumns mirrors tests/measurev2 GenerateProfile's "counter"
// ValueKind: a monotonic integer-valued random walk (step 1..10 per point).
// Values are integer-valued regardless of the profile's decimals field,
// matching production exactly (the counter branch there never calls alpResearchQuantize).
func alpv2GenCounterColumns(nCols, nPts int, seed int64) [][]float64 {
	rng := rand.New(rand.NewSource(seed))
	cols := make([][]float64, nCols)
	for c := range cols {
		col := make([]float64, nPts)
		cur := 100.0 + float64(c)*10.0
		for j := range col {
			step := math.Floor(rng.Float64()*10) + 1
			cur += step
			col[j] = cur
		}
		cols[c] = col
	}

	return cols
}

// alpv2GenSparseColumns mirrors tests/measurev2 GenerateProfile's "sparse"
// ValueKind: long runs of an identical quantized value, punctuated by a rare
// (5% per point) small step.
func alpv2GenSparseColumns(nCols, nPts, decimals int, seed int64) [][]float64 {
	rng := rand.New(rand.NewSource(seed))
	scale := math.Pow(10, float64(decimals))
	cols := make([][]float64, nCols)
	for c := range cols {
		col := make([]float64, nPts)
		cur := 100.0 + float64(c)*10.0
		for j := range col {
			if rng.Float64() < 0.05 {
				cur += (rng.Float64()*2.0 - 1.0) * 1.0
			}
			col[j] = math.Round(cur*scale) / scale
		}
		cols[c] = col
	}

	return cols
}

// alpv2GenColumns generates nCols independent columns of nPts values each for
// profile p: "gauge" reuses this package's existing generateALPColumns helper
// (alp_test.go), which already reproduces GenerateProfile's
// gauge random walk; "counter"/"sparse" use the two generators above.
func alpv2GenColumns(p alpv2Profile, nCols, nPts int, seed int64) [][]float64 {
	switch p.kind {
	case "counter":
		return alpv2GenCounterColumns(nCols, nPts, seed)
	case "sparse":
		return alpv2GenSparseColumns(nCols, nPts, p.decimals, seed)
	default: // "gauge"
		return generateALPColumns(nCols, nPts, p.decimals, seed)
	}
}

// alpv2SchemeCounts tallies how many vectors of a column picked each ALP
// scheme, for the report's "vectors main/rd/raw" column.
type alpv2SchemeCounts struct {
	main, rd, raw int
}

// alpv2EstimateColumnBytes splits values into ceil(n/1024) vectors and runs
// the real production encoder (encodeALPColumn) independently on each vector
// slice, summing the resulting byte counts — see the file-level comment for
// why this IS the estimate rather than an approximation of it.
func alpv2EstimateColumnBytes(values []float64, eng endian.EndianEngine) (int, alpv2SchemeCounts) {
	var total int
	var sc alpv2SchemeCounts
	for i := 0; i < len(values); i += alpv2VectorSize {
		end := i + alpv2VectorSize
		if end > len(values) {
			end = len(values)
		}
		data := encodeALPColumn(values[i:end], eng)
		total += len(data)
		switch data[0] {
		case 0:
			sc.main++
		case 1:
			sc.rd++
		case 2:
			sc.raw++
		default:
		}
	}

	return total, sc
}

// TestPOCALPv2VectorRatio measures whole-column ACTUAL (today's format) vs
// per-vector ESTIMATE (a hypothetical vectored ALP-v2),
// aggregated over the realistic profile catalog mirrored from tests/measurev2.
// See the file-level comment for the cost model and its documented biases.
func TestPOCALPv2VectorRatio(t *testing.T) {
	const (
		numCols  = 10
		numPts   = 8500 // 8 full 1024-vectors + one 308-value partial tail
		seedBase = int64(20260716)
	)
	eng := endian.GetLittleEndianEngine()

	t.Logf("%-20s %5s %6s | %13s %13s | %8s | %s",
		"profile", "cols", "n/col", "actual(B)", "estimate(B)", "delta%", "vectors main/rd/raw")
	t.Logf("%s", strings.Repeat("-", 96))

	var aggActual, aggEstimate int64

	for i, p := range alpv2Profiles() {
		cols := alpv2GenColumns(p, numCols, numPts, seedBase+int64(i)*1009)

		var actual, estimate int64
		var sc alpv2SchemeCounts
		for _, col := range cols {
			actual += int64(len(encodeALPColumn(col, eng)))
			e, s := alpv2EstimateColumnBytes(col, eng)
			estimate += int64(e)
			sc.main += s.main
			sc.rd += s.rd
			sc.raw += s.raw
		}

		delta := 100 * (float64(estimate) - float64(actual)) / float64(actual)
		t.Logf("%-20s %5d %6d | %12dB %12dB | %+7.2f%% | %d/%d/%d",
			p.name, numCols, numPts, actual, estimate, delta, sc.main, sc.rd, sc.raw)

		aggActual += actual
		aggEstimate += estimate
	}

	aggDelta := 100 * (float64(aggEstimate) - float64(aggActual)) / float64(aggActual)
	t.Logf("%s", strings.Repeat("-", 96))
	t.Logf("AGGREGATE actual=%dB estimate=%dB delta=%+.2f%% (negative = per-vector ESTIMATE smaller = per-vector wins)",
		aggActual, aggEstimate, aggDelta)
	t.Logf("Decision rule: pursue a vectored format only if per-vector wins by >= 5%% aggregate (i.e. delta <= -5.00%%). Observed: %+.2f%%.",
		aggDelta)
}
func BenchmarkPOCALP_Encode(b *testing.B) {
	vals := alpResearchQuantize(alpResearchGenerateSmallJitterMetric(200, 42.0, 0.003, 0.001), 2)
	b.ReportAllocs()
	for b.Loop() {
		_ = alpEncodeBytes(vals)
	}
}
func BenchmarkPOCALP_Decode(b *testing.B) {
	vals := alpResearchQuantize(alpResearchGenerateSmallJitterMetric(200, 42.0, 0.003, 0.001), 2)
	buf := alpEncodeBytes(vals)
	b.ReportAllocs()
	for b.Loop() {
		_ = alpDecodeBytes(buf, len(vals))
	}
}
func BenchmarkALPVsChimp(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	vals := alpResearchQuantize(alpResearchGenerateSmallJitterMetric(200, 42.0, 0.003, 0.001), 2) // decimal

	b.Run("ALP_Encode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			enc := alp.NewNumericALPEncoder(eng)
			enc.WriteSlice(vals)
			_ = enc.Bytes()
			enc.Finish()
		}
	})
	b.Run("Chimp_Encode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			enc := chimp.NewNumericChimpEncoder()
			enc.WriteSlice(vals)
			_ = enc.Bytes()
			enc.Finish()
		}
	})

	alpData := encodeALPColumn(vals, eng)
	chimpEnc := chimp.NewNumericChimpEncoder()
	chimpEnc.WriteSlice(vals)
	chimpData := append([]byte(nil), chimpEnc.Bytes()...)
	chimpEnc.Finish()

	b.Run("ALP_Decode", func(b *testing.B) {
		dec := alp.NewNumericALPDecoder(eng)
		b.ReportAllocs()
		var s float64
		for b.Loop() {
			for v := range dec.All(alpData, len(vals)) {
				s += v
			}
		}
		_ = s
	})
	b.Run("Chimp_Decode", func(b *testing.B) {
		dec := chimp.NewNumericChimpDecoder()
		b.ReportAllocs()
		var s float64
		for b.Loop() {
			for v := range dec.All(chimpData, len(vals)) {
				s += v
			}
		}
		_ = s
	})
}
