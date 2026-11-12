package blob

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
	ienc "github.com/arloliu/mebo/internal/encoding"
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
		name   string
		tagged bool
		opts   []NumericEncoderOption
	}{
		{"v1-le-raw-ts", false, []NumericEncoderOption{WithTimestampEncoding(format.TypeRaw)}},
		{"v1-be-delta-zstd", false, []NumericEncoderOption{
			WithBigEndian(), WithTimestampEncoding(format.TypeDelta), WithValueCompression(format.CompressionZstd),
		}},
		{"v2-shared-deltapacked", false, []NumericEncoderOption{
			WithBlobLayoutV2(), WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked),
		}},
		{"v2-be-shared-tags", true, []NumericEncoderOption{
			WithBigEndian(), WithBlobLayoutV2(), WithSharedTimestamps(), WithTagsEnabled(true),
		}},
	}
	tagOf := func(tagged bool, id uint64, i int) string {
		if !tagged {
			return ""
		}

		return fmt.Sprintf("m%d-p%d", id, i)
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
					require.NoError(t, enc.AddDataPoint(start.Add(time.Duration(i)*time.Second).UnixMicro(), v, tagOf(layout.tagged, m.id, i)))
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
					require.Equal(t, tagOf(layout.tagged, m.id, idx), dp.Tag)
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

// alpConstantMetricID is the one metric of every blob the constant-column test encodes.
const alpConstantMetricID = 7

// alpConstantFixture is a value column the ALP encoder stores with zero-bit codes:
// a main column, or under ALP-RLE a runs column that nests one.
type alpConstantFixture struct {
	name       string
	encoding   format.EncodingType
	values     []float64
	runs       bool // the column takes the runs layout, and its nested column has the zero-bit codes
	exceptions int  // values the zero-width column keeps in its exception sidecar
}

// alpConstantFixtures returns a constant column and a constant column with outliers no decimal scaling can hold,
// at several point counts and for both ALP encodings, plus an ALP-RLE column that takes the runs layout.
func alpConstantFixtures() []alpConstantFixture {
	outliers := []float64{math.Float64frombits(0x7ff8000000000abc), math.Pi * 1e17, math.Inf(1), math.Copysign(0, -1)}
	fixtures := make([]alpConstantFixture, 0, 17)
	for _, enc := range []format.EncodingType{format.TypeALP, format.TypeALPRLE} {
		for _, n := range []int{8, 30, 150, 8193} {
			constant := make([]float64, n)
			for i := range constant {
				constant[i] = 123.45
			}
			mixed := slices.Clone(constant)
			for k, p := range []int{0, n / 3, n / 2, n - 1} {
				mixed[p] = outliers[k]
			}
			fixtures = append(fixtures,
				alpConstantFixture{fmt.Sprintf("%s/constant/points=%d", enc, n), enc, constant, false, 0},
				alpConstantFixture{fmt.Sprintf("%s/outliers/points=%d", enc, n), enc, mixed, false, len(outliers)},
			)
		}
	}

	// A level that alternates with the outliers, each held for a few points:
	// the runs layout wins, and its nested column of run values is a constant with exceptions.
	held := make([]float64, 0, 139)
	for k := range 21 {
		v, n := 42.5, 9
		if k%2 == 1 {
			v, n = outliers[k/2%len(outliers)], 4
		}
		for range n {
			held = append(held, v)
		}
	}

	return append(fixtures, alpConstantFixture{"ALPRLE/runs", format.TypeALPRLE, held, true, 10})
}

// alpConstantBlob encodes f's values as one metric that starts at start with a point per second,
// and fails unless the stored value column has zero-bit codes and the fixture's exception count,
// so a fixture the encoder stores another way fails instead of passing for nothing.
func alpConstantBlob(t *testing.T, f alpConstantFixture, start time.Time) NumericBlob {
	t.Helper()
	enc, err := NewNumericEncoder(start, WithValueEncoding(f.encoding))
	require.NoError(t, err)
	// The default layout caps the points of a metric below the longest fixture, which takes layout 2 instead.
	if len(f.values) > enc.MaxDataPoints() {
		enc, err = NewNumericEncoder(start, WithValueEncoding(f.encoding), WithBlobLayoutV2())
		require.NoError(t, err)
	}
	require.NoError(t, enc.StartMetricID(alpConstantMetricID, len(f.values)))
	for i, v := range f.values {
		require.NoError(t, enc.AddDataPoint(start.Add(time.Duration(i)*time.Second).UnixMicro(), v, ""))
	}
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)
	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)

	// A main column is its scheme byte, exponent, factor and width byte, then its exception count;
	// a runs column wraps the main column that holds its run values.
	entry, ok := blob.index.GetByID(alpConstantMetricID)
	require.True(t, ok)
	col := blob.valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength]
	if f.runs {
		require.Equal(t, ienc.ALPRLEMaxSchemeByte, col[0], "scheme byte of a runs column")
		_, col, err = ienc.ValidateALPRunsColumn(col[1:], len(f.values), blob.Engine())
		require.NoError(t, err)
	}
	require.Equal(t, byte(0), col[0], "scheme byte of a main column")
	require.Equal(t, byte(0), col[3], "width byte")
	require.Equal(t, uint32(f.exceptions), blob.Engine().Uint32(col[4:8]), "exception count")

	return blob
}

// TestNumericBlob_ALP_ConstantColumn_ReadPaths reads zero-width ALP columns through the blob's read paths,
// which all decode the column in bulk except ForEachValues past 8192 points.
// ALP is lossless, so every path must return the encoder's input bit for bit, NaN payload and signed zero included,
// and the point walks must return the timestamps beside the values.
// Each fixture is two blobs a day apart: the per-blob paths read one after the other, the set reads both as one metric.
func TestNumericBlob_ALP_ConstantColumn_ReadPaths(t *testing.T) {
	// Every read returns the values of all blobs in order, and the timestamps when its path yields them.
	// A walk stops at an index that is out of order, which shortens what it returns.
	reads := []struct {
		name string
		read func(t *testing.T, blobs []NumericBlob) (vals []float64, ts []int64)
	}{
		{"NumericBlob.ForEach", func(t *testing.T, blobs []NumericBlob) (vals []float64, ts []int64) {
			for _, b := range blobs {
				base := len(vals)
				require.True(t, b.ForEach(alpConstantMetricID, func(i int, dp NumericDataPoint) bool {
					vals, ts = append(vals, dp.Val), append(ts, dp.Ts)

					return i == len(vals)-base-1
				}))
			}

			return vals, ts
		}},
		{"NumericMetric.ForEach", func(t *testing.T, blobs []NumericBlob) (vals []float64, ts []int64) {
			for _, b := range blobs {
				base := len(vals)
				h, ok := b.Metric(alpConstantMetricID)
				require.True(t, ok)
				h.ForEach(func(i int, dp NumericDataPoint) bool {
					vals, ts = append(vals, dp.Val), append(ts, dp.Ts)

					return i == len(vals)-base-1
				})
			}

			return vals, ts
		}},
		{"NumericBlob.ForEachValues", func(t *testing.T, blobs []NumericBlob) (vals []float64, ts []int64) {
			for _, b := range blobs {
				base := len(vals)
				require.True(t, b.ForEachValues(alpConstantMetricID, func(i int, v float64) bool {
					vals = append(vals, v)

					return i == len(vals)-base-1
				}))
			}

			return vals, nil
		}},
		{"NumericBlob.MaterializeMetric", func(t *testing.T, blobs []NumericBlob) (vals []float64, ts []int64) {
			for _, b := range blobs {
				m, ok := b.MaterializeMetric(alpConstantMetricID)
				require.True(t, ok)
				vals, ts = append(vals, m.Values...), append(ts, m.Timestamps...)
			}

			return vals, ts
		}},
		{"BlobSet.MaterializeNumericMetric", func(t *testing.T, blobs []NumericBlob) (vals []float64, ts []int64) {
			m, ok := NewBlobSet(blobs, nil).MaterializeNumericMetric(alpConstantMetricID)
			require.True(t, ok)

			return m.Values, m.Timestamps
		}},
	}

	starts := []time.Time{time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)}
	for _, f := range alpConstantFixtures() {
		t.Run(f.name, func(t *testing.T) {
			blobs := make([]NumericBlob, 0, len(starts))
			wantVals := make([]float64, 0, len(starts)*len(f.values))
			wantTS := make([]int64, 0, len(starts)*len(f.values))
			for _, start := range starts {
				blobs = append(blobs, alpConstantBlob(t, f, start))
				wantVals = append(wantVals, f.values...)
				for i := range f.values {
					wantTS = append(wantTS, start.Add(time.Duration(i)*time.Second).UnixMicro())
				}
			}

			// A path that fails does not stop the others, and names its first wrong value instead of printing the column.
			want := alpRLETestBits(wantVals)
			for _, r := range reads {
				vals, ts := r.read(t, blobs)
				got := alpRLETestBits(vals)
				if !assert.Equalf(t, len(want), len(got), "%s: value count", r.name) {
					continue
				}
				for i := range want {
					if got[i] != want[i] {
						assert.Failf(t, "value bits differ", "%s: index %d: got %#016x, want %#016x", r.name, i, got[i], want[i])

						break
					}
				}
				if ts != nil {
					assert.Equalf(t, wantTS, ts, "%s: timestamps", r.name)
				}
			}
		})
	}
}
