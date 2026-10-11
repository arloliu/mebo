package blob

import (
	"fmt"
	"math"
	"runtime/debug"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/section"
)

// fuzzSeedStart is the fixed blob start time of every fuzz seed, so the
// seed corpus is byte-identical from run to run.
var fuzzSeedStart = time.Unix(1_700_000_000, 0)

// numericFuzzSeedSpec describes one seed blob: its encoder options and how
// its metrics' timestamps and values are shaped.
type numericFuzzSeedSpec struct {
	opts       []NumericEncoderOption
	metrics    int
	points     int
	sameTS     bool                   // every metric uses the same timestamps (shared-timestamp groups)
	timestamps func(m, i int) int64   // nil: regular 15 s cadence with jitter
	values     func(m, i int) float64 // nil: decimal values that repeat in runs
}

// numericFuzzSeeds returns valid numeric blobs covering every timestamp and
// value encoding pair, the layout and payload options, the ALP bit widths
// and the DeltaPacked group widths the decoders branch on.
func numericFuzzSeeds(tb testing.TB) [][]byte {
	tb.Helper()

	tsEncodings := []format.EncodingType{format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked}
	valEncodings := []format.EncodingType{format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE}
	precise := func(m, i int) float64 { return math.Sin(float64(i)*0.37+float64(m)) * 1e6 * float64(1+i%11) }

	var specs []numericFuzzSeedSpec
	for _, tsEnc := range tsEncodings {
		for _, valEnc := range valEncodings {
			encodings := []NumericEncoderOption{WithTimestampEncoding(tsEnc), WithValueEncoding(valEnc)}
			with := func(extra ...NumericEncoderOption) []NumericEncoderOption {
				return append(append([]NumericEncoderOption(nil), encodings...), extra...)
			}
			specs = append(specs,
				numericFuzzSeedSpec{opts: with(), metrics: 3, points: 64},
				numericFuzzSeedSpec{opts: with(WithTagsEnabled(true)), metrics: 3, points: 64},
				numericFuzzSeedSpec{opts: with(WithMetricNames()), metrics: 3, points: 64},
				numericFuzzSeedSpec{opts: with(WithSharedTimestamps(), WithBlobLayoutV2(), WithTagsEnabled(true)), metrics: 3, points: 80, sameTS: true},
				numericFuzzSeedSpec{opts: with(WithBlobLayoutV2(), WithBigEndian()), metrics: 3, points: 64},
				numericFuzzSeedSpec{opts: with(WithValueCompression(format.CompressionLZ4), WithTimestampCompression(format.CompressionS2)), metrics: 3, points: 64},
				numericFuzzSeedSpec{opts: with(WithMetricNames(), WithBlobLayoutV2()), metrics: 3, points: 64, values: precise},
			)
		}
	}

	// ALP columns at every bit width the main scheme packs: metric m holds
	// distinct integers spread evenly over a range 2^w wide, so its
	// frame-of-reference codes span exactly w bits and no dictionary scheme
	// encodes them smaller.
	// Ranges wider than 2^62 are centred on zero to stay inside int64.
	const widthPoints = 64
	for _, valEnc := range []format.EncodingType{format.TypeALP, format.TypeALPRLE} {
		for first := 1; first <= 64; first += 16 {
			specs = append(specs, numericFuzzSeedSpec{
				opts:    []NumericEncoderOption{WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(valEnc)},
				metrics: 16,
				points:  widthPoints,
				sameTS:  true,
				values: func(m, i int) float64 {
					width := first + m
					top := math.Ldexp(1, width)
					low := 0.0
					if width > 62 {
						low = -top / 2
					}
					if i == widthPoints-1 {
						return math.Nextafter(low+top, low)
					}

					return low + math.Floor(top*float64(i)/widthPoints)
				},
			})
		}
	}

	// DeltaPacked groups of every byte width: delta-of-deltas that alternate
	// between small and multi-byte jumps, long enough for the SIMD kernels.
	jumps := []int64{1, 300, 70_000, 20_000_000, 5_000_000_000}
	specs = append(specs, numericFuzzSeedSpec{
		opts:    []NumericEncoderOption{WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeGorilla)},
		metrics: 3,
		points:  150,
		timestamps: func(m, i int) int64 {
			return fuzzSeedStart.UnixMicro() + int64(i)*1_000_000 + jumps[(i+m)%len(jumps)]
		},
	})

	seeds := make([][]byte, 0, len(specs))
	for _, spec := range specs {
		seeds = append(seeds, encodeNumericFuzzSeed(tb, spec))
	}

	return seeds
}

// encodeNumericFuzzSeed encodes one seed blob from its spec.
func encodeNumericFuzzSeed(tb testing.TB, spec numericFuzzSeedSpec) []byte {
	tb.Helper()

	enc, err := NewNumericEncoder(fuzzSeedStart, spec.opts...)
	require.NoError(tb, err)

	for m := range spec.metrics {
		points := spec.points
		if !spec.sameTS {
			points += m * 7
		}
		require.NoError(tb, enc.StartMetricName(fmt.Sprintf("fuzz.metric.%d", m), points))
		for i := range points {
			ts := fuzzSeedStart.Add(time.Duration(i*15+i%5) * time.Second).UnixMicro()
			switch {
			case spec.timestamps != nil:
				ts = spec.timestamps(m, i)
			case spec.sameTS:
				ts = fuzzSeedStart.Add(time.Duration(i*10) * time.Second).UnixMicro()
			default:
				// Keep the jittered 15 s cadence.
			}
			val := float64((i/3)*25+m) / 100
			if spec.values != nil {
				val = spec.values(m, i)
			}
			require.NoError(tb, enc.AddDataPoint(ts, val, fmt.Sprintf("t%d", i%3)))
		}
		require.NoError(tb, enc.EndMetric())
	}

	data, err := enc.Finish()
	require.NoError(tb, err)

	return data
}

// FuzzNumericDecoder decodes arbitrary bytes as a numeric blob and drives
// every read path over the result: iteration, ForEach, random access at
// and past the bounds, metric handles, materialization and blob sets.
//
// It asserts that no input panics, that lookups outside a metric's length
// report false, and that the copying and borrowing decoders agree.
// On Linux, inputs up to a page long are also decoded in place from memory
// that ends at an inaccessible page,
// so a decoder (assembly kernels included) that reads past the end of the
// whole input faults the run.
// Reads that cross from one column into the next stay inside the input and
// are not caught this way.
func FuzzNumericDecoder(f *testing.F) {
	for _, seed := range numericFuzzSeeds(f) {
		f.Add(seed)
	}
	f.Add([]byte{})
	f.Add(make([]byte, section.HeaderSize))

	f.Fuzz(func(t *testing.T, data []byte) {
		copied, copiedErr := decodeNumericFuzz(NewNumericDecoder, data)
		borrowed, borrowedErr := decodeNumericFuzz(NewNumericDecoderBorrowed, data)
		require.Equal(t, copiedErr == nil, borrowedErr == nil, "copying and borrowing decoders disagree")

		if copiedErr == nil {
			require.Equal(t, copied.MetricIDs(), borrowed.MetricIDs())
			require.Equal(t, copied.MetricNames(), borrowed.MetricNames())
			exerciseNumericFuzz(t, copied)
			exerciseNumericFuzz(t, borrowed)
		}

		// Verify opens the blob itself, so it must reject whatever the decoder rejects.
		if verifyErr := Verify(data); copiedErr != nil && section.IsNumericBlob(data) {
			require.Error(t, verifyErr, "Verify accepted a blob the decoder rejects")
		}

		if guarded, ok := fuzzGuardedCopy(t, data); ok {
			if blob, err := decodeNumericFuzz(NewNumericDecoderBorrowed, guarded); err == nil {
				exerciseNumericFuzz(t, blob)
			}
			_ = Verify(guarded)
		}

		if set, err := DecodeBlobSet(data, data); err == nil {
			exerciseBlobSetFuzz(t, set)
		}
	})
}

// decodeNumericFuzz runs one decoder constructor over data.
func decodeNumericFuzz(newDecoder func([]byte) (*NumericDecoder, error), data []byte) (NumericBlob, error) {
	dec, err := newDecoder(data)
	if err != nil {
		return NumericBlob{}, err
	}

	return dec.Decode()
}

// fuzzGuardedCopy copies data to the end of a readable page that is followed
// by an inaccessible one, and turns a fault on that page into a panic for
// the rest of the test.
// It reports false when guard pages are unavailable or data does not fit in a page.
func fuzzGuardedCopy(t *testing.T, data []byte) ([]byte, bool) {
	t.Helper()

	if len(data) == 0 || len(data) > 4096 {
		return nil, false
	}

	mem, ok := guardedBytes(t, len(data))
	if !ok {
		return nil, false
	}
	guarded := mem[:len(data):len(data)]
	copy(guarded, data)

	prev := debug.SetPanicOnFault(true)
	t.Cleanup(func() { debug.SetPanicOnFault(prev) })

	return guarded, true
}

// exerciseNumericFuzz reads every metric of blob through each access path.
func exerciseNumericFuzz(t *testing.T, blob NumericBlob) {
	t.Helper()

	for _, id := range blob.MetricIDs() {
		n := blob.Len(id)
		requireFuzzIterationBounded(t, n, countSeq2(blob.All(id)))
		requireFuzzIterationBounded(t, n, countSeq(blob.AllTimestamps(id)))
		requireFuzzIterationBounded(t, n, countSeq(blob.AllValues(id)))
		requireFuzzIterationBounded(t, n, countSeq(blob.AllTags(id)))

		blob.ForEach(id, func(int, NumericDataPoint) bool { return true })
		blob.ForEachTimestamps(id, func(int, int64) bool { return true })
		blob.ForEachValues(id, func(int, float64) bool { return true })

		for _, i := range []int{0, n / 2, n - 1} {
			blob.ValueAt(id, i)
			blob.TimestampAt(id, i)
			blob.TagAt(id, i)
		}
		for _, i := range []int{-1, n} {
			_, ok := blob.ValueAt(id, i)
			require.False(t, ok, "ValueAt(%d) outside length %d", i, n)
			_, ok = blob.TimestampAt(id, i)
			require.False(t, ok, "TimestampAt(%d) outside length %d", i, n)
			_, ok = blob.TagAt(id, i)
			require.False(t, ok, "TagAt(%d) outside length %d", i, n)
		}

		if h, ok := blob.Metric(id); ok {
			exerciseNumericMetricFuzz(t, &h)
		}
		blob.MaterializeMetric(id)
	}

	for _, name := range blob.MetricNames() {
		countSeq2(blob.AllByName(name))
		blob.ValueAtByName(name, 0)
	}

	blob.Materialize()
}

// exerciseNumericMetricFuzz reads a metric handle point by point, before and
// after materializing it.
func exerciseNumericMetricFuzz(t *testing.T, h *NumericMetric) {
	t.Helper()

	n := h.Len()
	for range 2 {
		// Sample indices: At replays sequential columns from the start, so
		// reading every index would make the run quadratic in the column length.
		for _, i := range []int{0, 1, n / 2, n - 2, n - 1} {
			h.At(i)
		}
		_, ok := h.At(n)
		require.False(t, ok, "At(%d) outside length %d", n, n)
		h.ForEach(func(int, NumericDataPoint) bool { return true })
		h.Materialize()
	}
}

// exerciseBlobSetFuzz reads every metric of a decoded blob set.
func exerciseBlobSetFuzz(t *testing.T, set BlobSet) {
	t.Helper()

	for _, nb := range set.NumericBlobs() {
		for _, id := range nb.MetricIDs() {
			n := set.MetricLen(id)
			countSeq2(set.AllNumerics(id))
			countSeq2(set.AllTimestamps(id))
			countSeq2(set.AllTags(id))
			set.NumericValueAt(id, n-1)
			_, ok := set.NumericValueAt(id, n)
			require.False(t, ok, "NumericValueAt(%d) outside length %d", n, n)
			if h, ok := set.NumericMetric(id); ok {
				exerciseNumericMetricFuzz(t, &h)
			}
		}
	}
	for _, tb := range set.TextBlobs() {
		for _, id := range tb.MetricIDs() {
			n := set.MetricLen(id)
			countSeq2(set.AllTexts(id))
			set.TextValueAt(id, n-1)
			_, ok := set.TextValueAt(id, n)
			require.False(t, ok, "TextValueAt(%d) outside length %d", n, n)
		}
	}

	set.MaterializeNumeric()
	set.MaterializeText()
}

// requireFuzzIterationBounded fails when an iteration yields more points
// than the metric's length.
func requireFuzzIterationBounded(t *testing.T, length, yielded int) {
	t.Helper()
	require.LessOrEqual(t, yielded, length, "iteration yielded more points than Len")
}

// countSeq drains seq and returns how many elements it yielded.
func countSeq[V any](seq func(yield func(V) bool)) int {
	n := 0
	for range seq {
		n++
	}

	return n
}

// countSeq2 drains seq and returns how many pairs it yielded.
func countSeq2[K, V any](seq func(yield func(K, V) bool)) int {
	n := 0
	for range seq {
		n++
	}

	return n
}
