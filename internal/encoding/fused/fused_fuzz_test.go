package fused

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/internal/encoding/metadata"
	"github.com/arloliu/mebo/internal/encoding/timestamp/delta"
	"github.com/arloliu/mebo/internal/encoding/timestamp/deltapacked"
	"github.com/arloliu/mebo/internal/encoding/value/chimp"
	"github.com/arloliu/mebo/internal/encoding/value/gorilla"
	"github.com/arloliu/mebo/internal/pool"
)

// fuzzStreams holds one encoded column per stream kind the fused decoders read.
type fuzzStreams struct {
	tsDelta, tsPacked, gorillaVals, chimpVals, tags []byte
}

// encodeFuzzStreams encodes n points into every stream kind: timestamps with
// irregular jumps, so DeltaPacked groups take several byte widths, and
// values that mix repeats with noise.
func encodeFuzzStreams(n int) fuzzStreams {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro()
	jumps := []int64{1, 300, 70_000, 20_000_000, 5_000_000_000}
	timestamps := make([]int64, n)
	values := make([]float64, n)
	tags := make([]string, n)
	for i := range n {
		timestamps[i] = start + int64(i)*1_000_000 + jumps[i%len(jumps)]
		values[i] = float64(i/4) + math.Sin(float64(i))*float64(i%3)
		tags[i] = []string{"", "a", "host-1"}[i%3]
	}

	tsDelta := delta.NewTimestampDeltaEncoder()
	tsDelta.WriteSlice(timestamps)
	tsPacked := deltapacked.NewTimestampDeltaPackedEncoder()
	tsPacked.WriteSlice(timestamps)
	gorillaEnc := gorilla.NewNumericGorillaEncoder()
	gorillaEnc.WriteSlice(values)
	chimpEnc := chimp.NewNumericChimpEncoder()
	chimpEnc.WriteSlice(values)
	tagEnc := metadata.NewTagEncoder(nil)
	tagEnc.WriteSlice(tags)

	streams := fuzzStreams{
		tsDelta:     append([]byte(nil), tsDelta.Bytes()...),
		tsPacked:    append([]byte(nil), tsPacked.Bytes()...),
		gorillaVals: append([]byte(nil), gorillaEnc.Bytes()...),
		chimpVals:   append([]byte(nil), chimpEnc.Bytes()...),
		tags:        append([]byte(nil), tagEnc.Bytes()...),
	}
	tsDelta.Finish()
	tsPacked.Finish()
	gorillaEnc.Finish()
	chimpEnc.Finish()
	tagEnc.Finish()

	return streams
}

// fusedPoint is one decoded point, compared bit for bit so NaNs compare equal.
type fusedPoint struct {
	ts  int64
	val uint64
}

// FuzzFusedDecoders feeds arbitrary stream bytes and point counts straight to
// every fused decoder, below the blob validation that the blob fuzz targets
// cannot get past.
// Counts run past pool.MaxPooledDecodeInt64s, so the per-point fallback that
// long DeltaPacked columns take is fuzzed too.
//
// It asserts that nothing panics, that each decoder yields at most count
// points with consecutive indexes, that a stopped walk stays stopped, and that
// the bulk and per-point DeltaPacked loops agree on every point both produce.
func FuzzFusedDecoders(f *testing.F) {
	for _, n := range []int{1, 9, 150, pool.MaxPooledDecodeInt64s + 1} {
		s := encodeFuzzStreams(n)
		for _, count := range []int{n, n + 20, pool.MaxPooledDecodeInt64s + 100} {
			f.Add(s.tsDelta, s.tsPacked, s.gorillaVals, s.chimpVals, s.tags, uint16(count))
		}
	}

	f.Fuzz(func(t *testing.T, tsDelta, tsPacked, gorillaVals, chimpVals, tags []byte, count16 uint16) {
		count := int(count16)
		for _, stopAfter := range []int{-1, 3} {
			fuzzTwoStreamDecoders(t, tsDelta, tsPacked, gorillaVals, chimpVals, count, stopAfter)
			fuzzTagDecoders(t, tsDelta, tsPacked, gorillaVals, chimpVals, tags, count, stopAfter)
			fuzzSingleColumnDecoders(t, tsDelta, tsPacked, gorillaVals, chimpVals, count, stopAfter)
		}

		// The per-point loops leave the empty cases to their callers, as the Each forms do.
		if count > 0 && count <= pool.MaxPooledDecodeInt64s && len(tsPacked) > 0 {
			if len(gorillaVals) > 0 {
				requireFusedPrefixAgree(t,
					collectFused(t, count, -1, func(y func(int, int64, float64) bool) { FusedDeltaPackedGorillaEach(tsPacked, gorillaVals, count, y) }),
					collectFused(t, count, -1, func(y func(int, int64, float64) bool) { fusedDeltaPackedGorillaStep(tsPacked, gorillaVals, count, y) }))
			}
			if len(chimpVals) > 0 {
				requireFusedPrefixAgree(t,
					collectFused(t, count, -1, func(y func(int, int64, float64) bool) { FusedDeltaPackedChimpEach(tsPacked, chimpVals, count, y) }),
					collectFused(t, count, -1, func(y func(int, int64, float64) bool) { fusedDeltaPackedChimpStep(tsPacked, chimpVals, count, y) }))
			}
		}
	})
}

// fuzzTwoStreamDecoders runs the timestamp-and-value Each decoders, and the
// per-point DeltaPacked loops directly, which the Each forms use only past
// the pooled-buffer length.
func fuzzTwoStreamDecoders(t *testing.T, tsDelta, tsPacked, gorillaVals, chimpVals []byte, count, stopAfter int) {
	t.Helper()

	collectFused(t, count, stopAfter, func(y func(int, int64, float64) bool) { FusedDeltaGorillaEach(tsDelta, gorillaVals, count, y) })
	collectFused(t, count, stopAfter, func(y func(int, int64, float64) bool) { FusedDeltaChimpEach(tsDelta, chimpVals, count, y) })
	collectFused(t, count, stopAfter, func(y func(int, int64, float64) bool) { FusedDeltaPackedGorillaEach(tsPacked, gorillaVals, count, y) })
	collectFused(t, count, stopAfter, func(y func(int, int64, float64) bool) { FusedDeltaPackedChimpEach(tsPacked, chimpVals, count, y) })
	if count > 0 && len(tsPacked) > 0 && len(gorillaVals) > 0 {
		collectFused(t, count, stopAfter, func(y func(int, int64, float64) bool) { fusedDeltaPackedGorillaStep(tsPacked, gorillaVals, count, y) })
	}
	if count > 0 && len(tsPacked) > 0 && len(chimpVals) > 0 {
		collectFused(t, count, stopAfter, func(y func(int, int64, float64) bool) { fusedDeltaPackedChimpStep(tsPacked, chimpVals, count, y) })
	}
}

// fuzzTagDecoders runs every decoder that walks a tag column beside other streams.
func fuzzTagDecoders(t *testing.T, tsDelta, tsPacked, gorillaVals, chimpVals, tags []byte, count, stopAfter int) {
	t.Helper()

	withTag := func(decode func(func(int, int64, float64, string) bool)) {
		collectFused(t, count, stopAfter, func(y func(int, int64, float64) bool) {
			decode(func(i int, ts int64, val float64, _ string) bool { return y(i, ts, val) })
		})
	}
	withTag(func(y func(int, int64, float64, string) bool) {
		FusedDeltaGorillaTagAll(tsDelta, gorillaVals, tags, count, y)
	})
	withTag(func(y func(int, int64, float64, string) bool) {
		FusedDeltaChimpTagAll(tsDelta, chimpVals, tags, count, y)
	})
	withTag(func(y func(int, int64, float64, string) bool) {
		FusedDeltaPackedGorillaTagAll(tsPacked, gorillaVals, tags, count, y)
	})
	withTag(func(y func(int, int64, float64, string) bool) {
		FusedDeltaPackedChimpTagAll(tsPacked, chimpVals, tags, count, y)
	})

	collectFused(t, count, stopAfter, func(y func(int, int64, float64) bool) {
		FusedDeltaTagAll(tsDelta, tags, count, func(i int, ts int64, _ string) bool { return y(i, ts, 0) })
	})
	collectFused(t, count, stopAfter, func(y func(int, int64, float64) bool) {
		FusedDeltaPackedTagAll(tsPacked, tags, count, func(i int, ts int64, _ string) bool { return y(i, ts, 0) })
	})
	collectFused(t, count, stopAfter, func(y func(int, int64, float64) bool) {
		FusedGorillaTagAll(gorillaVals, tags, count, func(i int, val float64, _ string) bool { return y(i, 0, val) })
	})
	collectFused(t, count, stopAfter, func(y func(int, int64, float64) bool) {
		FusedChimpTagAll(chimpVals, tags, count, func(i int, val float64, _ string) bool { return y(i, 0, val) })
	})
}

// fuzzSingleColumnDecoders runs the one-column decoders that index from a
// base, checking the index they return.
func fuzzSingleColumnDecoders(t *testing.T, tsDelta, tsPacked, gorillaVals, chimpVals []byte, count, stopAfter int) {
	t.Helper()

	const base = 7
	run := func(decode func(func(int) bool) int) {
		yielded := 0
		next := decode(func(i int) bool {
			if i != base+yielded {
				t.Fatalf("index %d, want %d", i, base+yielded)
			}
			yielded++
			if yielded > count {
				t.Fatalf("yielded more than count=%d points", count)
			}

			return yielded != stopAfter
		})
		if yielded == stopAfter {
			require.Equal(t, -1, next, "a stopped walk returns -1")
		} else {
			require.Equal(t, base+yielded, next, "next index after the last point")
		}
	}
	run(func(y func(int) bool) int {
		return FusedDeltaEach(tsDelta, count, base, func(i int, _ int64) bool { return y(i) })
	})
	run(func(y func(int) bool) int {
		return FusedDeltaPackedEach(tsPacked, count, base, func(i int, _ int64) bool { return y(i) })
	})
	run(func(y func(int) bool) int {
		return FusedGorillaEach(gorillaVals, count, base, func(i int, _ float64) bool { return y(i) })
	})
	run(func(y func(int) bool) int {
		return FusedChimpEach(chimpVals, count, base, func(i int, _ float64) bool { return y(i) })
	})
}

// collectFused runs one decoder, asserting indexes run 0, 1, 2, ..., that it
// yields at most count points, and that it makes no call after a stop.
// stopAfter < 0 never stops.
func collectFused(t *testing.T, count, stopAfter int, decode func(func(int, int64, float64) bool)) []fusedPoint {
	t.Helper()

	var points []fusedPoint
	stopped := false
	// Plain comparisons: require costs too much per point on long columns.
	decode(func(i int, ts int64, val float64) bool {
		switch {
		case stopped:
			t.Fatal("yield called after it returned false")
		case i != len(points):
			t.Fatalf("index %d, want %d", i, len(points))
		case len(points) == count:
			t.Fatalf("yielded more than count=%d points", count)
		default:
		}
		points = append(points, fusedPoint{ts: ts, val: math.Float64bits(val)})
		if len(points) == stopAfter {
			stopped = true
			return false
		}

		return true
	})

	return points
}

// requireFusedPrefixAgree fails unless the shorter sequence is a prefix of
// the longer: two loops over the same bytes may stop at different points on
// corrupt input, but never decode a point differently.
func requireFusedPrefixAgree(t *testing.T, a, b []fusedPoint) {
	t.Helper()

	n := min(len(a), len(b))
	require.True(t, slices.Equal(a[:n], b[:n]), "bulk and per-point DeltaPacked loops decode differently")
}
