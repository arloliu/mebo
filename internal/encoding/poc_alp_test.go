package encoding

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
//   go test ./internal/encoding/ -run 'POCALP' -v
//   go test ./internal/encoding/ -run x -bench 'POCALP' -benchmem

import (
	"math"
	"math/bits"
	"math/rand"
	"slices"
	"testing"
)

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
	enc := NewNumericGorillaEncoder()
	enc.WriteSlice(values)
	n := len(enc.Bytes())
	enc.Finish()

	return n
}

func chimpSize(values []float64) int {
	enc := NewNumericChimpEncoder()
	enc.WriteSlice(values)
	n := len(enc.Bytes())
	enc.Finish()

	return n
}

// quantize rounds values to d decimal places (models real sensors that report
// fixed-precision readings stored as float64, e.g. 45.2, 2048.5).
func quantize(values []float64, d int) []float64 {
	out := make([]float64, len(values))
	scale := math.Pow(10, float64(d))
	for i, v := range values {
		out[i] = math.Round(v*scale) / scale
	}

	return out
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
		{"cpu_util_fp", generateSmallJitterMetric(200, 42.0, 0.003, 0.001)},
		{"mem_drift_fp", generateDriftingMetric(500, 67.2, 0.0006, 0.002)},
		{"temp_fp", generateSmallJitterMetric(200, 23.4, 0.0005, 0.0002)},
		{"battery_fp", generateSmallJitterMetric(200, 3.72, 0.0003, 0.0001)},
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
		{"cpu_util", generateSmallJitterMetric(200, 42.0, 0.003, 0.001)},
		{"temp", generateSmallJitterMetric(200, 23.4, 0.0005, 0.0002)},
		{"net_tput", generateSmallJitterMetric(300, 950.0, 0.005, 0.002)},
		{"battery", generateSmallJitterMetric(200, 3.72, 0.0003, 0.0001)},
	} {
		ds = append(ds, pocFloatDataset{base.name + "_q1", quantize(base.values, 1)})
		ds = append(ds, pocFloatDataset{base.name + "_q2", quantize(base.values, 2)})
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

func BenchmarkPOCALP_Encode(b *testing.B) {
	vals := quantize(generateSmallJitterMetric(200, 42.0, 0.003, 0.001), 2)
	b.ReportAllocs()
	for b.Loop() {
		_ = alpEncodeBytes(vals)
	}
}

func BenchmarkPOCALP_Decode(b *testing.B) {
	vals := quantize(generateSmallJitterMetric(200, 42.0, 0.003, 0.001), 2)
	buf := alpEncodeBytes(vals)
	b.ReportAllocs()
	for b.Loop() {
		_ = alpDecodeBytes(buf, len(vals))
	}
}
