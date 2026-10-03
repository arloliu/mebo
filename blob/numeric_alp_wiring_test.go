package blob

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
)

// TestNumericBlob_ALP_DispatchParity is the safety net for ALP value-codec
// wiring: it builds an ALP-encoded blob and asserts that all four read paths
// return identical values. Any missed dispatch site (valueAt random access,
// the decodeValues iterator that backs All/AllValues/ForEach, or the
// decodeValuesSlice materialize path) silently returns zeros and is caught
// here.
func TestNumericBlob_ALP_DispatchParity(t *testing.T) {
	blob, metrics := createTestBlob(t, format.TypeRaw, format.TypeALP)

	// Sanity: the blob actually recorded ALP as the value encoding.
	require.Equal(t, format.TypeALP, blob.ValueEncoding(), "blob value encoding must be ALP")

	// Materialize the whole blob once for the blob-level random-access path.
	material := blob.Materialize()

	for _, m := range metrics {
		expected := m.values

		// Path 1: public value iterator (AllValues).
		var iterVals []float64
		for v := range blob.AllValues(m.id) {
			iterVals = append(iterVals, v)
		}
		require.Equalf(t, expected, iterVals, "metric %d: AllValues iterator mismatch", m.id)

		// Path 2: random access via ValueAt for every index.
		atVals := make([]float64, len(expected))
		for i := range expected {
			v, ok := blob.ValueAt(m.id, i)
			require.Truef(t, ok, "metric %d: ValueAt(%d) not ok", m.id, i)
			atVals[i] = v
		}
		require.Equalf(t, expected, atVals, "metric %d: ValueAt mismatch", m.id)

		// Path 3a: materialization via the whole-blob Materialize().
		matVals := make([]float64, len(expected))
		for i := range expected {
			v, ok := material.ValueAt(m.id, i)
			require.Truef(t, ok, "metric %d: Materialize().ValueAt(%d) not ok", m.id, i)
			matVals[i] = v
		}
		require.Equalf(t, expected, matVals, "metric %d: Materialize() mismatch", m.id)

		// Path 3b: materialization via the single-metric MaterializeMetric().
		metric, ok := blob.MaterializeMetric(m.id)
		require.Truef(t, ok, "metric %d: MaterializeMetric not ok", m.id)
		require.Equalf(t, expected, metric.Values, "metric %d: MaterializeMetric mismatch", m.id)

		// Path 4: ForEach callback.
		var feVals []float64
		ok = blob.ForEach(m.id, func(idx int, dp NumericDataPoint) bool {
			require.Equalf(t, len(feVals), idx, "metric %d: ForEach index out of order", m.id)
			feVals = append(feVals, dp.Val)

			return true
		})
		require.Truef(t, ok, "metric %d: ForEach not ok", m.id)
		require.Equalf(t, expected, feVals, "metric %d: ForEach mismatch", m.id)
	}
}

// alpRLETestMetric is one metric of an ALP-RLE wiring blob and the scheme byte its column must use.
type alpRLETestMetric struct {
	id     uint64
	values []float64
	scheme byte
}

// alpRLETestMetrics returns columns that take the runs layout (scheme 3) next to ones that stay plain ALP.
func alpRLETestMetrics() []alpRLETestMetric {
	rng := rand.New(rand.NewSource(20261004))
	hold := func(n int, p float64) []float64 {
		out := make([]float64, n)
		cur := 100.0
		for i := range out {
			if i > 0 && rng.Float64() < p {
				out[i] = out[i-1]
				continue
			}
			cur += cur * (rng.Float64()*2 - 1) * 0.005
			out[i] = math.Round(cur*100) / 100
		}

		return out
	}
	repeat := func(n int, vals ...float64) []float64 {
		out := make([]float64, 0, n*len(vals))
		for _, v := range vals {
			for range n {
				out = append(out, v)
			}
		}

		return out
	}
	runFree := hold(150, 0)
	withExc := hold(150, 0.6)
	for i := 0; i < len(withExc); i += 37 {
		withExc[i] = math.Pi * 1e17
	}

	return []alpRLETestMetric{
		{id: 1, values: hold(150, 0.5), scheme: 3},
		{id: 2, values: runFree, scheme: 0},
		{id: 3, values: withExc, scheme: 3},
		{id: 4, values: repeat(20, 0, math.Copysign(0, -1), math.NaN(), 1.5, math.Inf(-1)), scheme: 3},
		{id: 5, values: hold(1000, 0.7), scheme: 3},
		{id: 6, values: []float64{42.5}, scheme: 2}, // a single point is never a runs column; raw beats main here
	}
}

// alpRLETestBits returns values as bit patterns, so comparisons hold for NaN and −0.0.
func alpRLETestBits(values []float64) []uint64 {
	out := make([]uint64, len(values))
	for i, v := range values {
		out[i] = math.Float64bits(v)
	}

	return out
}

// TestNumericBlob_ALPRLE_DispatchParity is the ALP-RLE counterpart of TestNumericBlob_ALP_DispatchParity:
// every read path must return the encoded values bitwise, across layouts and byte orders,
// and the blob must really contain both runs (scheme 3) and plain columns.
func TestNumericBlob_ALPRLE_DispatchParity(t *testing.T) {
	layouts := []struct {
		name string
		opts []NumericEncoderOption
	}{
		{"v1-le-raw-ts", []NumericEncoderOption{WithTimestampEncoding(format.TypeRaw)}},
		{"v1-be-delta-zstd", []NumericEncoderOption{
			WithBigEndian(), WithTimestampEncoding(format.TypeDelta), WithValueCompression(format.CompressionZstd),
		}},
		{"v2-shared-deltapacked", []NumericEncoderOption{
			WithBlobLayoutV2(), WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked),
		}},
		{"v2-be-shared-tags", []NumericEncoderOption{
			WithBigEndian(), WithBlobLayoutV2(), WithSharedTimestamps(), WithTagsEnabled(true),
		}},
	}
	metrics := alpRLETestMetrics()
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)

	for _, layout := range layouts {
		t.Run(layout.name, func(t *testing.T) {
			opts := append([]NumericEncoderOption{WithValueEncoding(format.TypeALPRLE)}, layout.opts...)
			enc, err := NewNumericEncoder(start, opts...)
			require.NoError(t, err)
			for _, m := range metrics {
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
			require.Equal(t, format.TypeALPRLE, blob.ValueEncoding())
			material := blob.Materialize()

			for _, m := range metrics {
				want := alpRLETestBits(m.values)

				entry, ok := blob.index.GetByID(m.id)
				require.True(t, ok)
				require.Equalf(t, m.scheme, blob.valPayload[entry.ValueOffset], "metric %d: column scheme byte", m.id)

				var got []float64
				for v := range blob.AllValues(m.id) {
					got = append(got, v)
				}
				require.Equalf(t, want, alpRLETestBits(got), "metric %d: AllValues", m.id)

				got = got[:0]
				for idx, dp := range blob.All(m.id) {
					require.Equal(t, len(got), idx)
					got = append(got, dp.Val)
				}
				require.Equalf(t, want, alpRLETestBits(got), "metric %d: All", m.id)

				got = got[:0]
				for i := range m.values {
					v, ok := blob.ValueAt(m.id, i)
					require.Truef(t, ok, "metric %d: ValueAt(%d)", m.id, i)
					got = append(got, v)
				}
				require.Equalf(t, want, alpRLETestBits(got), "metric %d: ValueAt", m.id)
				_, ok = blob.ValueAt(m.id, len(m.values))
				require.False(t, ok, "ValueAt past the end")

				got = got[:0]
				for i := range m.values {
					v, ok := material.ValueAt(m.id, i)
					require.Truef(t, ok, "metric %d: Materialize().ValueAt(%d)", m.id, i)
					got = append(got, v)
				}
				require.Equalf(t, want, alpRLETestBits(got), "metric %d: Materialize", m.id)

				metric, ok := blob.MaterializeMetric(m.id)
				require.True(t, ok)
				require.Equalf(t, want, alpRLETestBits(metric.Values), "metric %d: MaterializeMetric", m.id)

				got = got[:0]
				require.True(t, blob.ForEach(m.id, func(idx int, dp NumericDataPoint) bool {
					require.Equal(t, len(got), idx)
					got = append(got, dp.Val)

					return true
				}))
				require.Equalf(t, want, alpRLETestBits(got), "metric %d: ForEach", m.id)

				got = got[:0]
				require.True(t, blob.ForEachValues(m.id, func(idx int, v float64) bool {
					require.Equal(t, len(got), idx)
					got = append(got, v)

					return true
				}))
				require.Equalf(t, want, alpRLETestBits(got), "metric %d: ForEachValues", m.id)
			}
		})
	}
}
