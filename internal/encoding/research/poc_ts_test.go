package research

// PoC: Simple8b for int64 microsecond timestamps.
//
// Reference: Anh & Moffat, SPE 2010; pure-Go lineage in jwilder/encoding.
// mebo's in-tree `Delta` codec already does delta-of-delta + zigzag + LEB128
// varint, and `DeltaPacked` does delta + group-varint. The open question is
// whether bit-packing (Simple8b) over the delta-of-delta stream beats varint's
// whole-byte granularity on mebo-shaped timestamps. This settles it on ratio.
//
// Run: go test ./internal/encoding/research -run 'POCTimestamp' -v

import (
	"testing"

	"github.com/arloliu/mebo/internal/encoding/timestamp/delta"
	"github.com/arloliu/mebo/internal/encoding/timestamp/deltapacked"
)

var s8bItems = [16]int{240, 120, 60, 30, 20, 15, 12, 10, 8, 7, 6, 5, 4, 3, 2, 1}
var s8bBits = [16]int{0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 15, 20, 30, 60}

// simple8bEncode packs unsigned values (each must fit in 60 bits) into 64-bit
// words: 4-bit selector + up to 60 data bits. Greedy: at each position use the
// selector that packs the most values at a uniform bit width. Lossless.
func simple8bEncode(in []uint64) ([]uint64, bool) {
	var out []uint64
	i, n := 0, len(in)
	for i < n {
		packed := false
		for sel := range 16 {
			cnt := s8bItems[sel]
			bitw := s8bBits[sel]
			if i+cnt > n {
				continue
			}
			fit := true
			if bitw == 0 {
				for j := range cnt {
					if in[i+j] != 0 {
						fit = false
						break
					}
				}
			} else {
				maxv := uint64(1)<<uint(bitw) - 1
				for j := range cnt {
					if in[i+j] > maxv {
						fit = false
						break
					}
				}
			}
			if !fit {
				continue
			}
			word := uint64(sel) << 60
			if bitw > 0 {
				for j := range cnt {
					word |= in[i+j] << uint(j*bitw)
				}
			}
			out = append(out, word)
			i += cnt
			packed = true

			break
		}
		if !packed {
			return nil, false
		}
	}

	return out, true
}

func simple8bDecode(in []uint64, n int) []uint64 {
	out := make([]uint64, 0, n)
	for _, word := range in {
		sel := int(word >> 60)
		cnt := s8bItems[sel]
		bitw := s8bBits[sel]
		if bitw == 0 {
			for range cnt {
				out = append(out, 0)
			}

			continue
		}
		mask := uint64(1)<<uint(bitw) - 1
		for j := range cnt {
			out = append(out, (word>>uint(j*bitw))&mask)
		}
	}

	return out[:n]
}

func zigzag64(v int64) uint64 { return uint64((v << 1) ^ (v >> 63)) }

// pocS8bTimestampBytes: first_ts(8) + simple8b( zigzag(delta-of-delta) stream ).
func pocS8bTimestampBytes(ts []int64) (int, bool) {
	n := len(ts)
	if n == 0 {
		return 0, true
	}
	zz := make([]uint64, 0, n-1)
	var prevDelta int64
	for i := 1; i < n; i++ {
		delta := ts[i] - ts[i-1]
		dod := delta - prevDelta // for i==1, prevDelta=0 so dod=delta
		zz = append(zz, zigzag64(dod))
		prevDelta = delta
	}
	words, ok := simple8bEncode(zz)
	if !ok {
		return 0, false
	}
	// verify lossless
	dec := simple8bDecode(words, len(zz))
	for i := range zz {
		if dec[i] != zz[i] {
			return 0, false
		}
	}

	return 8 + len(words)*8, true
}

func mboDeltaSize(ts []int64) int {
	enc := delta.NewTimestampDeltaEncoder()
	enc.WriteSlice(ts)
	n := len(enc.Bytes())
	enc.Finish()

	return n
}

func mboDeltaPackedSize(ts []int64) int {
	enc := deltapacked.NewTimestampDeltaPackedEncoder()
	enc.WriteSlice(ts)
	n := len(enc.Bytes())
	enc.Finish()

	return n
}

// genTimestamps: base + step(=1e6 us) with ±jitterPct jitter, monotonic.
func genTimestamps(n int, jitterPct float64, seed int64) []int64 {
	const step = 1_000_000
	out := make([]int64, n)
	cur := int64(1_700_000_000_000_000)
	r := newLCG(uint64(seed))
	for i := range out {
		j := int64((r.f64()*2 - 1) * jitterPct / 100 * step)
		cur += step + j
		out[i] = cur
	}

	return out
}

// tiny deterministic RNG to avoid importing math/rand twice across PoC files.
type lcg struct{ s uint64 }

func newLCG(seed uint64) *lcg { return &lcg{s: seed*2862933555777941757 + 3037000493} }
func (l *lcg) f64() float64 {
	l.s = l.s*6364136223846793005 + 1442695040888963407
	return float64(l.s>>11) / float64(1<<53)
}

func TestPOCTimestampRatio(t *testing.T) {
	type tc struct {
		name string
		ts   []int64
	}
	cases := []tc{
		{"clean_1s_200", genTimestamps(200, 0, 1)},
		{"jitter_0.1pct_200", genTimestamps(200, 0.1, 1)},
		{"jitter_0.5pct_200", genTimestamps(200, 0.5, 1)},
		{"jitter_2pct_200", genTimestamps(200, 2.0, 1)},
		{"jitter_0.1pct_1000", genTimestamps(1000, 0.1, 7)},
	}

	t.Logf("%-20s %6s | %10s %12s %10s | %s", "dataset", "n", "Delta(dod)", "DeltaPacked", "Simple8b", "winner")
	t.Logf("%s", "----------------------------------------------------------------------------------")
	for _, c := range cases {
		n := len(c.ts)
		d := float64(mboDeltaSize(c.ts)) / float64(n)
		dp := float64(mboDeltaPackedSize(c.ts)) / float64(n)
		s8b, ok := pocS8bTimestampBytes(c.ts)
		if !ok {
			t.Fatalf("%s: simple8b failed (value > 60 bits)", c.name)
		}
		s := float64(s8b) / float64(n)
		win := "Delta(dod)"
		best := d
		if dp < best {
			best, win = dp, "DeltaPacked"
		}
		if s < best {
			win = "Simple8b"
		}
		t.Logf("%-20s %6d | %9.3fB %11.3fB %9.3fB | %s", c.name, n, d, dp, s, win)
	}
}
