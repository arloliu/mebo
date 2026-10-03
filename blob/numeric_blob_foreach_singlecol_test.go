package blob

import (
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/pool"
)

// TestNumericBlob_ForEachValues_MatchesAll verifies ForEachValues yields exactly
// the same (index, value) sequence as AllValues across every encoding
// combination, with and without tags.
func TestNumericBlob_ForEachValues_MatchesAll(t *testing.T) {
	tsEncodings := []format.EncodingType{format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked}
	valEncodings := []format.EncodingType{format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE}

	for _, tsEnc := range tsEncodings {
		for _, valEnc := range valEncodings {
			for _, withTags := range []bool{false, true} {
				name := fmt.Sprintf("%v_%v_tags=%v", tsEnc, valEnc, withTags)
				t.Run(name, func(t *testing.T) {
					blob, metricIDs := buildForEachTestBlob(t, tsEnc, valEnc, withTags)

					for _, id := range metricIDs {
						var want []float64
						var wantIdx int
						for v := range blob.AllValues(id) {
							want = append(want, v)
						}
						require.NotEmpty(t, want)

						var got []float64
						var gotIdx []int
						found := blob.ForEachValues(id, func(i int, v float64) bool {
							gotIdx = append(gotIdx, i)
							got = append(got, v)

							return true
						})

						require.True(t, found)
						require.Equal(t, want, got)
						// Index must be a dense 0..n-1 sequence.
						for i := range gotIdx {
							require.Equal(t, wantIdx, gotIdx[i])
							wantIdx++
						}
					}
				})
			}
		}
	}
}

// TestNumericBlob_ForEachTimestamps_MatchesAll verifies ForEachTimestamps yields
// exactly the same (index, timestamp) sequence as AllTimestamps across every
// encoding combination, with and without tags. It also exercises the shared
// timestamp path via the encoder default.
func TestNumericBlob_ForEachTimestamps_MatchesAll(t *testing.T) {
	tsEncodings := []format.EncodingType{format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked}
	valEncodings := []format.EncodingType{format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE}

	for _, tsEnc := range tsEncodings {
		for _, valEnc := range valEncodings {
			for _, withTags := range []bool{false, true} {
				name := fmt.Sprintf("%v_%v_tags=%v", tsEnc, valEnc, withTags)
				t.Run(name, func(t *testing.T) {
					blob, metricIDs := buildForEachTestBlob(t, tsEnc, valEnc, withTags)

					for _, id := range metricIDs {
						var want []int64
						for ts := range blob.AllTimestamps(id) {
							want = append(want, ts)
						}
						require.NotEmpty(t, want)

						var got []int64
						var gotIdx int
						found := blob.ForEachTimestamps(id, func(i int, ts int64) bool {
							require.Equal(t, gotIdx, i)
							gotIdx++
							got = append(got, ts)

							return true
						})

						require.True(t, found)
						require.Equal(t, want, got)
					}
				})
			}
		}
	}
}

// TestNumericBlob_ForEachTimestamps_SharedCache verifies the shared-TS cache fast
// path yields the same data as AllTimestamps.
func TestNumericBlob_ForEachTimestamps_SharedCache(t *testing.T) {
	startTime := time.Unix(1700000000, 0).UTC()
	encoder, err := NewNumericEncoder(startTime, WithTimestampEncoding(format.TypeRaw))
	require.NoError(t, err)

	const numMetrics = 4
	const points = 30
	sharedTs := make([]int64, points)
	ts := startTime.UnixMicro()
	for i := range points {
		ts += int64(time.Second / time.Microsecond)
		sharedTs[i] = ts
	}

	metricIDs := make([]uint64, numMetrics)
	for m := range numMetrics {
		metricIDs[m] = uint64(500 + m)
		require.NoError(t, encoder.StartMetricID(metricIDs[m], points))
		for i := range points {
			require.NoError(t, encoder.AddDataPoint(sharedTs[i], float64(m)+float64(i)*0.25, ""))
		}
		require.NoError(t, encoder.EndMetric())
	}
	data, err := encoder.Finish()
	require.NoError(t, err)
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	for _, id := range metricIDs {
		var want []int64
		for tsv := range blob.AllTimestamps(id) {
			want = append(want, tsv)
		}

		var got []int64
		found := blob.ForEachTimestamps(id, func(_ int, tsv int64) bool {
			got = append(got, tsv)

			return true
		})
		require.True(t, found)
		require.Equal(t, want, got)
	}
}

func TestNumericBlob_ForEachValues_EarlyStop(t *testing.T) {
	blob, metricIDs := buildForEachTestBlob(t, format.TypeDelta, format.TypeGorilla, false)

	var got []float64
	found := blob.ForEachValues(metricIDs[0], func(i int, v float64) bool {
		got = append(got, v)

		return i < 9 // stop after 10 values
	})
	require.True(t, found)
	require.Len(t, got, 10)

	want := make([]float64, 0, 10)
	for v := range blob.AllValues(metricIDs[0]) {
		want = append(want, v)
		if len(want) == 10 {
			break
		}
	}
	require.Equal(t, want, got)
}

func TestNumericBlob_ForEachTimestamps_EarlyStop(t *testing.T) {
	blob, metricIDs := buildForEachTestBlob(t, format.TypeDeltaPacked, format.TypeRaw, false)

	var got []int64
	found := blob.ForEachTimestamps(metricIDs[0], func(i int, ts int64) bool {
		got = append(got, ts)

		return i < 4 // stop after 5 timestamps
	})
	require.True(t, found)
	require.Len(t, got, 5)

	want := make([]int64, 0, 5)
	for ts := range blob.AllTimestamps(metricIDs[0]) {
		want = append(want, ts)
		if len(want) == 5 {
			break
		}
	}
	require.Equal(t, want, got)
}

func TestNumericBlob_ForEachSingleColumn_NotFound(t *testing.T) {
	blob, _ := buildForEachTestBlob(t, format.TypeDelta, format.TypeGorilla, false)

	calledV := false
	require.False(t, blob.ForEachValues(99999, func(int, float64) bool {
		calledV = true

		return true
	}))
	require.False(t, calledV)

	calledT := false
	require.False(t, blob.ForEachTimestamps(99999, func(int, int64) bool {
		calledT = true

		return true
	}))
	require.False(t, calledT)
}

func TestNumericBlob_ForEachSingleColumn_NilYield(t *testing.T) {
	blob, metricIDs := buildForEachTestBlob(t, format.TypeDelta, format.TypeGorilla, false)

	require.False(t, blob.ForEachValues(metricIDs[0], nil))
	require.False(t, blob.ForEachTimestamps(metricIDs[0], nil))
	require.False(t, blob.ForEachValuesByName("x", nil))
	require.False(t, blob.ForEachTimestampsByName("x", nil))
}

func TestNumericBlob_ForEachSingleColumn_ByName(t *testing.T) {
	startTime := time.Unix(1700000000, 0).UTC()
	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)

	require.NoError(t, encoder.StartMetricName("cpu.usage", 3))
	base := startTime.UnixMicro()
	for i := range 3 {
		require.NoError(t, encoder.AddDataPoint(base+int64(i)*1000000, float64(i)+0.5, ""))
	}
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	wantV := make([]float64, 0, 3)
	for v := range blob.AllValuesByName("cpu.usage") {
		wantV = append(wantV, v)
	}
	var gotV []float64
	require.True(t, blob.ForEachValuesByName("cpu.usage", func(_ int, v float64) bool {
		gotV = append(gotV, v)

		return true
	}))
	require.Equal(t, wantV, gotV)

	wantT := make([]int64, 0, 3)
	for ts := range blob.AllTimestampsByName("cpu.usage") {
		wantT = append(wantT, ts)
	}
	var gotT []int64
	require.True(t, blob.ForEachTimestampsByName("cpu.usage", func(_ int, ts int64) bool {
		gotT = append(gotT, ts)

		return true
	}))
	require.Equal(t, wantT, gotT)

	require.False(t, blob.ForEachValuesByName("no.such.metric", func(int, float64) bool { return true }))
	require.False(t, blob.ForEachTimestampsByName("no.such.metric", func(int, int64) bool { return true }))
}

// buildALPFamilyBlob encodes alpRLETestMetrics (runs columns, NaN and −0 runs, a single point)
// under valEnc with shared DeltaPacked timestamps and the given start time.
func buildALPFamilyBlob(t *testing.T, valEnc format.EncodingType, start time.Time, opts ...NumericEncoderOption) NumericBlob {
	t.Helper()
	opts = append([]NumericEncoderOption{
		WithValueEncoding(valEnc), WithTimestampEncoding(format.TypeDeltaPacked), WithSharedTimestamps(),
	}, opts...)
	enc, err := NewNumericEncoder(start, opts...)
	require.NoError(t, err)
	for _, m := range alpRLETestMetrics() {
		require.NoError(t, enc.StartMetricID(m.id, len(m.values)))
		for i, v := range m.values {
			require.NoError(t, enc.AddDataPoint(start.Add(time.Duration(i)*time.Second).UnixMicro(), v, ""))
		}
		require.NoError(t, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(t, err)
	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)

	return blob
}

// TestNumericBlob_ForEachValues_ALPBulkDecode pins the bulk-decode path ALP and ALP-RLE take in ForEachValues
// against AllValues, which still drains the codec iterator and so is an independent oracle.
// It covers runs columns, NaN payloads and −0 (compared bitwise), every early-stop position class,
// a nested ForEachValues call from inside yield, and both byte orders.
func TestNumericBlob_ForEachValues_ALPBulkDecode(t *testing.T) {
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for _, valEnc := range []format.EncodingType{format.TypeALP, format.TypeALPRLE} {
		for _, bigEndian := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v_bigEndian=%v", valEnc, bigEndian), func(t *testing.T) {
				var opts []NumericEncoderOption
				if bigEndian {
					opts = append(opts, WithBigEndian())
				}
				blob := buildALPFamilyBlob(t, valEnc, start, opts...)
				metrics := alpRLETestMetrics()

				for _, m := range metrics {
					var want []float64
					for v := range blob.AllValues(m.id) {
						want = append(want, v)
					}
					require.Equal(t, alpRLETestBits(m.values), alpRLETestBits(want), "oracle sanity")

					var got []float64
					require.True(t, blob.ForEachValues(m.id, func(i int, v float64) bool {
						require.Equal(t, len(got), i)
						got = append(got, v)

						return true
					}))
					require.Equalf(t, alpRLETestBits(want), alpRLETestBits(got), "metric %d", m.id)

					// Stop after the first, a word-boundary and the last value.
					for _, stop := range []int{0, 63, 64, len(want) - 1} {
						if stop < 0 || stop >= len(want) {
							continue
						}
						var prefix []float64
						require.True(t, blob.ForEachValues(m.id, func(i int, v float64) bool {
							prefix = append(prefix, v)

							return i < stop
						}), "early stop still reports the metric as found")
						require.Equalf(t, alpRLETestBits(want[:stop+1]), alpRLETestBits(prefix), "metric %d stop %d", m.id, stop)
					}
				}

				// A nested call from inside yield gets its own pooled buffer,
				// and must not disturb the outer iteration's remaining values.
				outer, inner := metrics[0], metrics[4]
				var outerGot []float64
				nestedOK := true
				blob.ForEachValues(outer.id, func(i int, v float64) bool {
					outerGot = append(outerGot, v)
					if i%10 != 0 {
						return true
					}
					var got []float64
					blob.ForEachValues(inner.id, func(_ int, v float64) bool {
						got = append(got, v)

						return true
					})
					nestedOK = nestedOK && assert.ObjectsAreEqual(alpRLETestBits(inner.values), alpRLETestBits(got))

					return true
				})
				require.True(t, nestedOK, "nested ForEachValues must see the inner metric's values")
				require.Equal(t, alpRLETestBits(outer.values), alpRLETestBits(outerGot), "nested calls must not change the outer values")

				// A panicking yield must leave later calls correct.
				require.Panics(t, func() {
					blob.ForEachValues(outer.id, func(i int, _ float64) bool {
						if i == 3 {
							panic("yield failed")
						}

						return true
					})
				})
				var after []float64
				blob.ForEachValues(outer.id, func(_ int, v float64) bool {
					after = append(after, v)

					return true
				})
				require.Equal(t, alpRLETestBits(outer.values), alpRLETestBits(after), "values after a panicking yield")
			})
		}
	}
}

// TestNumericBlobSet_ForEachValues_ALPBulkDecode checks that the bulk-decode path keeps NumericBlobSet's
// global indexes continuous across member blobs and stops the whole set when yield stops mid-blob.
func TestNumericBlobSet_ForEachValues_ALPBulkDecode(t *testing.T) {
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for _, valEnc := range []format.EncodingType{format.TypeALP, format.TypeALPRLE} {
		t.Run(valEnc.String(), func(t *testing.T) {
			blobs := make([]NumericBlob, 3)
			for i := range blobs {
				blobs[i] = buildALPFamilyBlob(t, valEnc, start.Add(time.Duration(i)*time.Hour))
			}
			set, err := NewNumericBlobSet(blobs)
			require.NoError(t, err)

			id := alpRLETestMetrics()[0].id
			var want []float64
			for v := range set.AllValues(id) {
				want = append(want, v)
			}
			require.Len(t, want, 3*len(alpRLETestMetrics()[0].values))

			var got []float64
			require.True(t, set.ForEachValues(id, func(i int, v float64) bool {
				require.Equal(t, len(got), i, "global index must be continuous across blobs")
				got = append(got, v)

				return true
			}))
			require.Equal(t, alpRLETestBits(want), alpRLETestBits(got))

			stop := len(want)/2 + 7 // inside the second blob
			var prefix []float64
			set.ForEachValues(id, func(i int, v float64) bool {
				prefix = append(prefix, v)

				return i < stop
			})
			require.Equal(t, alpRLETestBits(want[:stop+1]), alpRLETestBits(prefix), "yield stopping mid-blob stops the set")
		})
	}
}

// TestNumericBlob_ForEachValues_ALPDoesNotAllocate pins that the bulk-decode path is allocation-free once the pool is warm,
// for a single blob and across a set.
func TestNumericBlob_ForEachValues_ALPDoesNotAllocate(t *testing.T) {
	if raceEnabled {
		t.Skip("sync.Pool intentionally drops Puts under the race detector; the zero-alloc invariant only holds without -race")
	}
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	var sink float64
	yield := func(_ int, v float64) bool { sink += v; return true }
	for _, valEnc := range []format.EncodingType{format.TypeALP, format.TypeALPRLE} {
		blob := buildALPFamilyBlob(t, valEnc, start)
		set, err := NewNumericBlobSet([]NumericBlob{blob, buildALPFamilyBlob(t, valEnc, start.Add(time.Hour))})
		require.NoError(t, err)
		for _, m := range alpRLETestMetrics() {
			blob.ForEachValues(m.id, yield) // warm the pool for this size
			require.Zerof(t, testing.AllocsPerRun(100, func() { blob.ForEachValues(m.id, yield) }), "%v blob metric %d", valEnc, m.id)
			require.Zerof(t, testing.AllocsPerRun(100, func() { set.ForEachValues(m.id, yield) }), "%v set metric %d", valEnc, m.id)
		}
	}
	require.NotZero(t, sink)
}

// TestNumericBlob_ForEachValues_ALPConcurrent runs the pooled bulk-decode path from many goroutines on one blob,
// so the race detector (make test runs with -race) checks that pooled buffers are never shared between calls.
func TestNumericBlob_ForEachValues_ALPConcurrent(t *testing.T) {
	blob := buildALPFamilyBlob(t, format.TypeALPRLE, time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC))
	metrics := alpRLETestMetrics()
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := range 8 {
		wg.Go(func() {
			for r := range 50 {
				m := metrics[(g+r)%len(metrics)]
				got := make([]float64, 0, len(m.values))
				blob.ForEachValues(m.id, func(_ int, v float64) bool {
					got = append(got, v)

					return true
				})
				if !assert.ObjectsAreEqual(alpRLETestBits(m.values), alpRLETestBits(got)) {
					errs <- fmt.Sprintf("goroutine %d round %d metric %d: values differ", g, r, m.id)

					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// TestNumericBlob_ForEachValues_ALPLongColumns checks both sides of the bulk-decode cap:
// a column of pool.MaxPooledDecodeFloat64s points takes the pooled bulk path and does not allocate,
// a longer one streams through the iterator, and both yield the same values as AllValues.
func TestNumericBlob_ForEachValues_ALPLongColumns(t *testing.T) {
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for _, valEnc := range []format.EncodingType{format.TypeALP, format.TypeALPRLE} {
		t.Run(valEnc.String(), func(t *testing.T) {
			enc, err := NewNumericEncoder(start, WithValueEncoding(valEnc), WithTimestampEncoding(format.TypeDelta), WithBlobLayoutV2())
			require.NoError(t, err)
			sizes := map[uint64]int{1: pool.MaxPooledDecodeFloat64s, 2: pool.MaxPooledDecodeFloat64s + 1}
			values := map[uint64][]float64{}
			for id := uint64(1); id <= 2; id++ {
				n := sizes[id]
				values[id] = alpRunsHoldForTest(n, int64(id))
				require.NoError(t, enc.StartMetricID(id, n))
				for i, v := range values[id] {
					require.NoError(t, enc.AddDataPoint(start.Add(time.Duration(i)*time.Second).UnixMicro(), v, ""))
				}
				require.NoError(t, enc.EndMetric())
			}
			data, err := enc.Finish()
			require.NoError(t, err)
			dec, err := NewNumericDecoder(data)
			require.NoError(t, err)
			blob, err := dec.Decode()
			require.NoError(t, err)

			var sink float64
			yield := func(_ int, v float64) bool { sink += v; return true }
			for id := uint64(1); id <= 2; id++ {
				var got []float64
				require.True(t, blob.ForEachValues(id, func(i int, v float64) bool {
					require.Equal(t, len(got), i)
					got = append(got, v)

					return true
				}))
				require.Equalf(t, alpRLETestBits(values[id]), alpRLETestBits(got), "metric of %d points", sizes[id])

				var prefix []float64
				require.True(t, blob.ForEachValues(id, func(i int, v float64) bool {
					prefix = append(prefix, v)

					return i < 99
				}))
				require.Equalf(t, alpRLETestBits(values[id][:100]), alpRLETestBits(prefix), "early stop, metric of %d points", sizes[id])
			}
			if !raceEnabled {
				blob.ForEachValues(1, yield)
				require.Zero(t, testing.AllocsPerRun(20, func() { blob.ForEachValues(1, yield) }), "column at the cap takes the pooled path")
				require.NotZero(t, sink)
			}
			// Above the cap, the iterator path allocates its closure but never a column-sized buffer.
			const calls = 20
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range calls {
				blob.ForEachValues(2, yield)
			}
			runtime.ReadMemStats(&after)
			perCall := (after.TotalAlloc - before.TotalAlloc) / calls
			require.Lessf(t, perCall, uint64(8*pool.MaxPooledDecodeFloat64s/4),
				"column above the cap allocated %d bytes per call; it must stream, not decode into a buffer", perCall)
		})
	}
}

// alpRunsHoldForTest is a 2-decimal gauge of n points where about half the points repeat the previous one.
func alpRunsHoldForTest(n int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	cur := 100.0
	for i := range out {
		if i > 0 && rng.Float64() < 0.5 {
			out[i] = out[i-1]
			continue
		}
		cur += cur * (rng.Float64()*2 - 1) * 0.005
		out[i] = math.Round(cur*100) / 100
	}

	return out
}
