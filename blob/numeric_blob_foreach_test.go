package blob

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
)

// buildForEachTestBlob encodes 3 metrics × 50 points with the given encodings
// and tag mode, returning the decoded blob and metric IDs.
func buildForEachTestBlob(t *testing.T, tsEnc, valEnc format.EncodingType, withTags bool) (NumericBlob, []uint64) {
	t.Helper()

	startTime := time.Unix(1700000000, 0).UTC()
	opts := []NumericEncoderOption{
		WithTimestampEncoding(tsEnc),
		WithValueEncoding(valEnc),
	}
	if withTags {
		opts = append(opts, WithTagsEnabled(true))
	}

	encoder, err := NewNumericEncoder(startTime, opts...)
	require.NoError(t, err)

	const numMetrics = 3
	const points = 50

	metricIDs := make([]uint64, numMetrics)
	for m := range numMetrics {
		metricIDs[m] = uint64(1000 + m*7)
		require.NoError(t, encoder.StartMetricID(metricIDs[m], points))

		ts := startTime.UnixMicro()
		val := 100.0 * float64(m+1)
		for i := range points {
			ts += int64(time.Second/time.Microsecond) + int64(i%7)
			val *= 1.001
			tag := ""
			if withTags {
				tag = fmt.Sprintf("tag-%d-%d", m, i)
			}
			require.NoError(t, encoder.AddDataPoint(ts, val, tag))
		}

		require.NoError(t, encoder.EndMetric())
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	return blob, metricIDs
}

// TestNumericBlob_ForEach_MatchesAll verifies ForEach yields exactly the same
// (index, data point) sequence as All across every encoding combination, with
// and without tags.
func TestNumericBlob_ForEach_MatchesAll(t *testing.T) {
	tsEncodings := []format.EncodingType{format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked}
	valEncodings := []format.EncodingType{format.TypeRaw, format.TypeGorilla, format.TypeChimp}

	for _, tsEnc := range tsEncodings {
		for _, valEnc := range valEncodings {
			for _, withTags := range []bool{false, true} {
				name := fmt.Sprintf("%v_%v_tags=%v", tsEnc, valEnc, withTags)
				t.Run(name, func(t *testing.T) {
					blob, metricIDs := buildForEachTestBlob(t, tsEnc, valEnc, withTags)

					for _, id := range metricIDs {
						var wantIdx []int
						var want []NumericDataPoint
						for i, dp := range blob.All(id) {
							wantIdx = append(wantIdx, i)
							want = append(want, dp)
						}
						require.NotEmpty(t, want)

						var gotIdx []int
						var got []NumericDataPoint
						found := blob.ForEach(id, func(i int, dp NumericDataPoint) bool {
							gotIdx = append(gotIdx, i)
							got = append(got, dp)

							return true
						})

						require.True(t, found)
						require.Equal(t, wantIdx, gotIdx)
						require.Equal(t, want, got)
					}
				})
			}
		}
	}
}

func TestNumericBlob_ForEach_EarlyStop(t *testing.T) {
	blob, metricIDs := buildForEachTestBlob(t, format.TypeDelta, format.TypeGorilla, false)

	var got []NumericDataPoint
	found := blob.ForEach(metricIDs[0], func(i int, dp NumericDataPoint) bool {
		got = append(got, dp)

		return i < 9 // stop after 10 points
	})

	require.True(t, found, "early stop must still report the metric as found")
	require.Len(t, got, 10)

	// The early prefix matches All.
	want := make([]NumericDataPoint, 0, 10)
	for _, dp := range blob.All(metricIDs[0]) {
		want = append(want, dp)
		if len(want) == 10 {
			break
		}
	}
	require.Equal(t, want, got)
}

func TestNumericBlob_ForEach_NotFound(t *testing.T) {
	blob, _ := buildForEachTestBlob(t, format.TypeDelta, format.TypeGorilla, false)

	called := false
	found := blob.ForEach(99999, func(int, NumericDataPoint) bool {
		called = true

		return true
	})

	require.False(t, found)
	require.False(t, called)
}

func TestNumericBlob_ForEachByName(t *testing.T) {
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

	want := make([]NumericDataPoint, 0, 3)
	for _, dp := range blob.AllByName("cpu.usage") {
		want = append(want, dp)
	}
	require.Len(t, want, 3)

	var got []NumericDataPoint
	found := blob.ForEachByName("cpu.usage", func(_ int, dp NumericDataPoint) bool {
		got = append(got, dp)

		return true
	})
	require.True(t, found)
	require.Equal(t, want, got)

	require.False(t, blob.ForEachByName("no.such.metric", func(int, NumericDataPoint) bool {
		return true
	}))
}
