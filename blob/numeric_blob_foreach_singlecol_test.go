package blob

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
)

// TestNumericBlob_ForEachValues_MatchesAll verifies ForEachValues yields exactly
// the same (index, value) sequence as AllValues across every encoding
// combination, with and without tags.
func TestNumericBlob_ForEachValues_MatchesAll(t *testing.T) {
	tsEncodings := []format.EncodingType{format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked}
	valEncodings := []format.EncodingType{format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP}

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
	valEncodings := []format.EncodingType{format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP}

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
