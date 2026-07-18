package simple8b

import (
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/internal/encoding/timestamp/delta"
	"github.com/arloliu/mebo/internal/encoding/timestamp/deltapacked"
	"github.com/stretchr/testify/require"
)

type lcg struct{ s uint64 }

func newLCG(seed uint64) *lcg { return &lcg{s: seed*2862933555777941757 + 3037000493} }

func (l *lcg) f64() float64 {
	l.s = l.s*6364136223846793005 + 1442695040888963407

	return float64(l.s>>11) / float64(1<<53)
}

func genTimestamps(n int, jitterPct float64, seed int64) []int64 {
	const step = 1_000_000
	out := make([]int64, n)
	cur := int64(1_700_000_000_000_000)
	r := newLCG(uint64(seed))
	for i := range out {
		jitter := int64((r.f64()*2 - 1) * jitterPct / 100 * step)
		cur += step + jitter
		out[i] = cur
	}

	return out
}

func mboDeltaSize(timestamps []int64) int {
	encoder := delta.NewTimestampDeltaEncoder()
	encoder.WriteSlice(timestamps)
	size := len(encoder.Bytes())
	encoder.Finish()

	return size
}

func mboDeltaPackedSize(timestamps []int64) int {
	encoder := deltapacked.NewTimestampDeltaPackedEncoder()
	encoder.WriteSlice(timestamps)
	size := len(encoder.Bytes())
	encoder.Finish()

	return size
}

func s8bDecodeAll(data []byte, count int, eng endian.EndianEngine) []int64 {
	dec := NewTimestampSimple8bDecoder(eng)
	out := make([]int64, 0, count)
	for ts := range dec.All(data, count) {
		out = append(out, ts)
	}

	return out
}

func s8bEncodeSlice(ts []int64, eng endian.EndianEngine) []byte {
	enc := NewTimestampSimple8bEncoder(eng)
	enc.WriteSlice(ts)
	data := append([]byte(nil), enc.Bytes()...)
	enc.Finish()

	return data
}

func TestTimestampSimple8b_RoundTrip_Jittered(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	ts := genTimestamps(200, 0.1, 1) // reuse PoC helper

	enc := NewTimestampSimple8bEncoder(eng)
	enc.WriteSlice(ts)
	data := append([]byte(nil), enc.Bytes()...)
	require.Equal(t, len(ts), enc.Len())
	enc.Finish()

	got := s8bDecodeAll(data, len(ts), eng)
	require.Equal(t, ts, got)
}

func TestTimestampSimple8b_RoundTrip_Shapes(t *testing.T) {
	shapes := []struct {
		name string
		ts   []int64
	}{
		{"empty", []int64{}},
		{"single", []int64{1_700_000_000_000_000}},
		{"two", []int64{1_700_000_000_000_000, 1_700_000_001_000_000}},
		{"three", []int64{1_700_000_000_000_000, 1_700_000_001_000_000, 1_700_000_002_000_000}},
		{"clean_1s_200", genTimestamps(200, 0, 1)},
		{"jitter_0.1pct_200", genTimestamps(200, 0.1, 1)},
		{"jitter_0.5pct_200", genTimestamps(200, 0.5, 3)},
		{"jitter_2pct_500", genTimestamps(500, 2.0, 5)},
		{"jitter_0.1pct_1000", genTimestamps(1000, 0.1, 7)},
		{"large_gaps", []int64{0, 1_000_000, 2_000_000, 1_000_000_000_000, 1_000_001_000_000}},
	}

	for _, eng := range []endian.EndianEngine{endian.GetLittleEndianEngine(), endian.GetBigEndianEngine()} {
		for _, s := range shapes {
			data := s8bEncodeSlice(s.ts, eng)
			got := s8bDecodeAll(data, len(s.ts), eng)
			if len(s.ts) == 0 {
				require.Empty(t, got, "%s", s.name)
				continue
			}
			require.Equal(t, s.ts, got, "%s", s.name)

			// At() must agree with All() for every index.
			dec := NewTimestampSimple8bDecoder(eng)
			for i := range s.ts {
				v, ok := dec.At(data, i, len(s.ts))
				require.True(t, ok, "%s At(%d)", s.name, i)
				require.Equal(t, s.ts[i], v, "%s At(%d)", s.name, i)
			}
			_, ok := dec.At(data, len(s.ts), len(s.ts)) // out of range
			require.False(t, ok, "%s At(oob)", s.name)
		}
	}
}

func TestTimestampSimple8b_WriteVsWriteSlice(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	ts := genTimestamps(137, 0.3, 11)

	sliceData := s8bEncodeSlice(ts, eng)

	enc := NewTimestampSimple8bEncoder(eng)
	for _, v := range ts {
		enc.Write(v)
	}
	writeData := append([]byte(nil), enc.Bytes()...)
	enc.Finish()

	require.Equal(t, sliceData, writeData, "Write and WriteSlice must produce identical bytes")
}

func TestTimestampSimple8b_MultiSegmentReset(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	segA := genTimestamps(200, 0.1, 1)
	segB := genTimestamps(50, 0.5, 2)

	enc := NewTimestampSimple8bEncoder(eng)
	enc.WriteSlice(segA)
	_ = enc.Bytes() // flush segment A
	lenA := enc.Size()
	enc.Reset()
	enc.WriteSlice(segB)
	full := append([]byte(nil), enc.Bytes()...)
	require.Equal(t, len(segA)+len(segB), enc.Len(), "count is cumulative across segments")
	enc.Finish()

	gotA := s8bDecodeAll(full[:lenA], len(segA), eng)
	gotB := s8bDecodeAll(full[lenA:], len(segB), eng)
	require.Equal(t, segA, gotA)
	require.Equal(t, segB, gotB)
}

func TestTimestampSimple8b_OverflowExceptions(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	// dod at index 2 = (1<<60)-1, whose zigzag needs 61 bits -> exception path.
	ts := []int64{0, 1, 1 + (int64(1) << 60), 2 + (int64(1) << 60)}

	// Full round-trip must remain bit-exact despite exceptions.
	data := s8bEncodeSlice(ts, eng)
	got := s8bDecodeAll(data, len(ts), eng)
	require.Equal(t, ts, got)

	// White-box: the nExc field (bytes 8..10, after the 8-byte firstTS) must be
	// non-zero, proving the >60-bit dod actually took the exception path.
	require.GreaterOrEqual(t, len(data), 10)
	nExc := eng.Uint16(data[8:10])
	require.Positive(t, nExc, "expected the >60-bit dod to be recorded as an exception")
}

func s8bProdSize(ts []int64, eng endian.EndianEngine) int {
	return len(s8bEncodeSlice(ts, eng))
}

// TestTimestampSimple8b_Ratio proves Simple8b's compression ratio against the
// in-tree Delta (delta-of-delta + varint) and DeltaPacked (group varint) codecs
// using the PRODUCTION encoders on identical data.
func TestTimestampSimple8b_Ratio(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	cases := []struct {
		name      string
		ts        []int64
		wantBeatD bool // expect Simple8b <= Delta (the low-jitter win)
	}{
		{"clean_1s_200", genTimestamps(200, 0, 1), true},
		{"jitter_0.1pct_200", genTimestamps(200, 0.1, 1), true},
		{"jitter_0.1pct_1000", genTimestamps(1000, 0.1, 7), true},
		{"jitter_0.5pct_200", genTimestamps(200, 0.5, 3), false},
		{"jitter_2pct_500", genTimestamps(500, 2.0, 5), false},
	}

	t.Logf("%-20s %6s | %11s %12s %11s | %s", "dataset", "n", "Delta(dod)", "DeltaPacked", "Simple8b", "S8b vs Delta")
	t.Logf("%s", "---------------------------------------------------------------------------------------")
	for _, c := range cases {
		n := len(c.ts)
		d := float64(mboDeltaSize(c.ts)) / float64(n)
		dp := float64(mboDeltaPackedSize(c.ts)) / float64(n)
		s := float64(s8bProdSize(c.ts, eng)) / float64(n)
		t.Logf("%-20s %6d | %10.3fB %11.3fB %10.3fB | %+.1f%%",
			c.name, n, d, dp, s, 100*(s-d)/d)
		if c.wantBeatD {
			require.LessOrEqualf(t, s, d, "%s: Simple8b (%.3fB) should beat Delta (%.3fB) on low jitter", c.name, s, d)
		}
	}
}

// ---- no-regress speed gate: Simple8b vs Delta on realistic low-jitter data ----

func benchTS() []int64 { return genTimestamps(200, 0.1, 1) }

func deltaEncode(ts []int64) []byte {
	enc := delta.NewTimestampDeltaEncoder()
	enc.WriteSlice(ts)
	data := append([]byte(nil), enc.Bytes()...)
	enc.Finish()

	return data
}

func BenchmarkS8bVsDelta_Encode(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	ts := benchTS()

	b.Run("Delta", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			enc := delta.NewTimestampDeltaEncoder()
			enc.WriteSlice(ts)
			_ = enc.Bytes()
			enc.Finish()
		}
	})
	b.Run("Simple8b", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			enc := NewTimestampSimple8bEncoder(eng)
			enc.WriteSlice(ts)
			_ = enc.Bytes()
			enc.Finish()
		}
	})
}

// BenchmarkS8bVsDelta_EncodeBlob reflects mebo's real usage: one encoder reused
// across many metrics (Reset between, Finish at end), which amortizes per-segment
// scratch allocation — the steady-state encode cost.
func BenchmarkS8bVsDelta_EncodeBlob(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	const metrics = 200
	cols := make([][]int64, metrics)
	for i := range cols {
		cols[i] = genTimestamps(200, 0.1, int64(i+1))
	}

	b.Run("Delta", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			enc := delta.NewTimestampDeltaEncoder()
			for _, c := range cols {
				enc.WriteSlice(c)
				_ = enc.Bytes()
				enc.Reset()
			}
			enc.Finish()
		}
	})
	b.Run("Simple8b", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			enc := NewTimestampSimple8bEncoder(eng)
			for _, c := range cols {
				enc.WriteSlice(c)
				_ = enc.Bytes()
				enc.Reset()
			}
			enc.Finish()
		}
	})
}

func BenchmarkS8bVsDelta_Iterate(b *testing.B) {
	eng := endian.GetLittleEndianEngine()
	ts := benchTS()
	deltaData := deltaEncode(ts)
	s8bData := s8bEncodeSlice(ts, eng)
	n := len(ts)

	b.Run("Delta", func(b *testing.B) {
		dec := delta.NewTimestampDeltaDecoder()
		b.ReportAllocs()
		var sink int64
		for b.Loop() {
			for v := range dec.All(deltaData, n) {
				sink += v
			}
		}
		_ = sink
	})
	b.Run("Simple8b", func(b *testing.B) {
		dec := NewTimestampSimple8bDecoder(eng)
		b.ReportAllocs()
		var sink int64
		for b.Loop() {
			for v := range dec.All(s8bData, n) {
				sink += v
			}
		}
		_ = sink
	})
}
