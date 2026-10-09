package blob

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// handleTestStart is the start time of every handle test blob, in microseconds.
const handleTestStart = int64(1_700_000_000_000_000)

// handleTestShape says how the timestamp sequences of a handle test blob relate to each other.
type handleTestShape uint8

const (
	// handleTestIdentical gives every metric the same timestamps.
	handleTestIdentical handleTestShape = iota
	// handleTestUniqueLast shifts the last metric's timestamps by one microsecond.
	handleTestUniqueLast
	// handleTestAllUnique shifts every metric's timestamps by its ordinal in microseconds.
	handleTestAllUnique
)

// handleTestMetric is one metric of a handle test blob.
type handleTestMetric struct {
	id   uint64
	name string
	ts   []int64
	vals []float64
	tags []string
}

// handleTestSeries returns n timestamps 15 s apart shifted by offset microseconds,
// n 2-decimal values that repeat the previous one every third point,
// and n tags that cycle over four hosts.
func handleTestSeries(n int, offset int64, seed float64) ([]int64, []float64, []string) {
	ts := make([]int64, n)
	vals := make([]float64, n)
	tags := make([]string, n)
	cur := 100.0 + seed
	for i := range n {
		ts[i] = handleTestStart + int64(i)*15_000_000 + offset
		if i%3 != 2 {
			cur = math.Round((cur+cur*0.004)*100) / 100
		}
		vals[i] = cur
		tags[i] = "host=" + strconv.Itoa(i%4)
	}

	return ts, vals, tags
}

// handleTestMetrics builds n metrics of points each, with the timestamp relation shape describes.
func handleTestMetrics(n, points int, shape handleTestShape) []handleTestMetric {
	metrics := make([]handleTestMetric, n)
	for m := range metrics {
		var offset int64
		if shape == handleTestAllUnique {
			offset = int64(m)
		}
		if shape == handleTestUniqueLast && m == n-1 {
			offset = 1
		}
		ts, vals, tags := handleTestSeries(points, offset, float64(m)*10)
		metrics[m] = handleTestMetric{id: uint64(m + 1), name: "metric." + strconv.Itoa(m+1), ts: ts, vals: vals, tags: tags}
	}

	return metrics
}

// handleTestBlob encodes metrics little-endian and uncompressed unless opts say otherwise, and decodes the result.
// With names, the metrics are started by name and the blob retains the names; otherwise they are started by ID.
// Tags are passed to the encoder every time and written only when it has tags enabled.
func handleTestBlob(tb testing.TB, metrics []handleTestMetric, names bool, opts ...NumericEncoderOption) NumericBlob {
	tb.Helper()

	return handleTestBlobAt(tb, handleTestStart, metrics, names, opts...)
}

// handleTestBlobAt is handleTestBlob with the blob's start time, which orders the members of a set.
func handleTestBlobAt(tb testing.TB, start int64, metrics []handleTestMetric, names bool, opts ...NumericEncoderOption) NumericBlob {
	tb.Helper()
	base := []NumericEncoderOption{
		WithLittleEndian(),
		WithTimestampCompression(format.CompressionNone),
		WithValueCompression(format.CompressionNone),
	}
	if names {
		base = append(base, WithMetricNames())
	}
	enc, err := NewNumericEncoder(time.UnixMicro(start), append(base, opts...)...)
	require.NoError(tb, err)
	for _, m := range metrics {
		if names {
			require.NoError(tb, enc.StartMetricName(m.name, len(m.ts)))
		} else {
			require.NoError(tb, enc.StartMetricID(m.id, len(m.ts)))
		}
		require.NoError(tb, enc.AddDataPoints(m.ts, m.vals, m.tags))
		require.NoError(tb, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(tb, err)
	dec, err := NewNumericDecoder(data)
	require.NoError(tb, err)
	blob, err := dec.Decode()
	require.NoError(tb, err)

	return blob
}

// handleTestID is the metric ID the blob stores for m: its own ID, or the hash of its name when names were used.
func handleTestID(m handleTestMetric, names bool) uint64 {
	if names {
		return hash.ID(m.name)
	}

	return m.id
}

// requireHandleAgrees checks every handle accessor against the NumericBlob accessor it replaces,
// at every index from -1 to Len inclusive, and the two access classes.
func requireHandleAgrees(t *testing.T, h *NumericMetric, blob NumericBlob, id uint64, wantTs, wantVal AccessClass) {
	t.Helper()
	entry := blob.index.entryByID(id)
	require.NotNil(t, entry)
	n := blob.Len(id)
	require.Equal(t, n, h.Len(), "Len")
	require.Equal(t, wantTs, h.TimestampAccess(), "TimestampAccess")
	require.Equal(t, wantVal, h.ValueAccess(), "ValueAccess")
	for i := -1; i <= n; i++ {
		wantV, wantVOk := blob.ValueAt(id, i)
		gotV, gotVOk := h.ValueAt(i)
		require.Equal(t, wantVOk, gotVOk, "ValueAt(%d) ok", i)
		require.Equal(t, math.Float64bits(wantV), math.Float64bits(gotV), "ValueAt(%d)", i)

		wantTS, wantTSOk := blob.TimestampAt(id, i)
		gotTS, gotTSOk := h.TimestampAt(i)
		require.Equal(t, wantTSOk, gotTSOk, "TimestampAt(%d) ok", i)
		require.Equal(t, wantTS, gotTS, "TimestampAt(%d)", i)

		wantTag, wantTagOk := blob.TagAt(id, i)
		gotTag, gotTagOk := h.TagAt(i)
		require.Equal(t, wantTagOk, gotTagOk, "TagAt(%d) ok", i)
		require.Equal(t, wantTag, gotTag, "TagAt(%d)", i)

		wantDP, wantDPOk := numericPointFromEntry(blob, entry, i)
		gotDP, gotDPOk := h.At(i)
		require.Equal(t, wantDPOk, gotDPOk, "At(%d) ok", i)
		require.Equal(t, wantDP.Ts, gotDP.Ts, "At(%d).Ts", i)
		require.Equal(t, math.Float64bits(wantDP.Val), math.Float64bits(gotDP.Val), "At(%d).Val", i)
		require.Equal(t, wantDP.Tag, gotDP.Tag, "At(%d).Tag", i)
	}
}

// requireMaterializedHandleAgrees materializes a copy of h and checks it as requireHandleAgrees does,
// with both axes direct, and that its three ForEach forms yield what h's do;
// h, copied before Materialize, keeps its classes and its answers.
func requireMaterializedHandleAgrees(t *testing.T, h *NumericMetric, blob NumericBlob, id uint64, wantTs, wantVal AccessClass) {
	t.Helper()
	hm := *h
	hm.Materialize()
	requireHandleAgrees(t, &hm, blob, id, AccessDirect, AccessDirect)
	require.Equal(t, collectHandle(h), collectHandle(&hm), "iteration before and after Materialize")
	requireHandleAgrees(t, h, blob, id, wantTs, wantVal)
}

// TestNumericMetric_TimestampAccess pins every row of the spec's TimestampAt decision table:
// the class follows the encoding and the metric's membership in a pre-decoded group, not the encoder options.
func TestNumericMetric_TimestampAccess(t *testing.T) {
	const metrics, points = 4, 30
	tests := []struct {
		name       string
		tsEnc      format.EncodingType
		shared     bool
		shape      handleTestShape
		want       AccessClass // metrics 1..metrics-1
		wantLast   AccessClass // the last metric
		wantGroups bool        // the blob pre-decoded at least one group at open
	}{
		{"raw timestamps", format.TypeRaw, false, handleTestIdentical, AccessDirect, AccessDirect, false},
		{"raw timestamps shared", format.TypeRaw, true, handleTestIdentical, AccessDirect, AccessDirect, true},
		{"delta without the shared option", format.TypeDelta, false, handleTestIdentical, AccessSequential, AccessSequential, false},
		{"deltapacked without the shared option", format.TypeDeltaPacked, false, handleTestIdentical, AccessSequential, AccessSequential, false},
		{"deltapacked shared option but nothing shared", format.TypeDeltaPacked, true, handleTestAllUnique, AccessSequential, AccessSequential, false},
		{"delta shared", format.TypeDelta, true, handleTestIdentical, AccessDirect, AccessDirect, true},
		{"deltapacked shared", format.TypeDeltaPacked, true, handleTestIdentical, AccessDirect, AccessDirect, true},
		{"delta shared with one unique sequence", format.TypeDelta, true, handleTestUniqueLast, AccessDirect, AccessSequential, true},
		{"deltapacked shared with one unique sequence", format.TypeDeltaPacked, true, handleTestUniqueLast, AccessDirect, AccessSequential, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []NumericEncoderOption{WithTimestampEncoding(tt.tsEnc), WithValueEncoding(format.TypeALP)}
			if tt.shared {
				opts = append(opts, WithSharedTimestamps())
			}
			ms := handleTestMetrics(metrics, points, tt.shape)
			blob := handleTestBlob(t, ms, false, opts...)
			require.Equal(t, tt.wantGroups, blob.sharedTs != nil, "pre-decoded groups")
			if !tt.shared {
				require.False(t, blob.IsV2Layout(), "a blob without the shared option stays V1")
			}
			for k, m := range ms {
				want := tt.want
				if k == len(ms)-1 {
					want = tt.wantLast
				}
				h, ok := blob.Metric(m.id)
				require.True(t, ok)
				requireHandleAgrees(t, &h, blob, m.id, want, AccessDirect)
			}
		})
	}
}

// TestNumericMetric_ValueAccess pins the ValueAt decision table for the blob-wide value encodings.
func TestNumericMetric_ValueAccess(t *testing.T) {
	tests := []struct {
		name string
		enc  format.EncodingType
		want AccessClass
	}{
		{"raw", format.TypeRaw, AccessDirect},
		{"alp", format.TypeALP, AccessDirect},
		{"gorilla", format.TypeGorilla, AccessSequential},
		{"chimp", format.TypeChimp, AccessSequential},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := handleTestMetrics(3, 40, handleTestIdentical)
			blob := handleTestBlob(t, ms, false,
				WithTimestampEncoding(format.TypeDeltaPacked), WithSharedTimestamps(), WithValueEncoding(tt.enc))
			for _, m := range ms {
				if tt.enc == format.TypeALP {
					entry := blob.index.entryByID(m.id)
					require.NotNil(t, entry)
					require.Equalf(t, byte(0), blob.valPayload[entry.ValueOffset], "metric %d: 2-decimal values take the main scheme", m.id)
				}
				h, ok := blob.Metric(m.id)
				require.True(t, ok)
				requireHandleAgrees(t, &h, blob, m.id, AccessDirect, tt.want)
			}
		})
	}
}

// TestNumericMetric_ValueAccess_ALPRD pins the RD scheme (1), which ALP picks for values its decimal scheme cannot fit.
func TestNumericMetric_ValueAccess_ALPRD(t *testing.T) {
	const points = 64
	ts, _, tags := handleTestSeries(points, 0, 0)
	vals := make([]float64, points)
	for i := range vals {
		vals[i] = math.Sqrt(float64(i+2)) * 1e3
	}
	ms := []handleTestMetric{{id: 1, name: "metric.1", ts: ts, vals: vals, tags: tags}}
	blob := handleTestBlob(t, ms, false, WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeALP))
	entry := blob.index.entryByID(1)
	require.NotNil(t, entry)
	require.Equal(t, byte(1), blob.valPayload[entry.ValueOffset], "column scheme byte")
	h, ok := blob.Metric(1)
	require.True(t, ok)
	requireHandleAgrees(t, &h, blob, 1, AccessDirect, AccessDirect)
	for i, want := range vals {
		got, ok := h.ValueAt(i)
		require.True(t, ok)
		require.Equal(t, math.Float64bits(want), math.Float64bits(got), "ValueAt(%d)", i)
	}
}

// TestNumericMetric_ValueAccess_ALPRLEMixedLayouts pins that every ALP-RLE column is direct,
// whether it took the runs layout (scheme 3) or stayed plain (schemes 0 and 2).
func TestNumericMetric_ValueAccess_ALPRLEMixedLayouts(t *testing.T) {
	metrics := alpRLETestMetrics()
	enc, err := NewNumericEncoder(time.UnixMicro(handleTestStart),
		WithLittleEndian(), WithValueEncoding(format.TypeALPRLE), WithValueCompression(format.CompressionNone),
		WithTimestampEncoding(format.TypeRaw), WithTimestampCompression(format.CompressionNone))
	require.NoError(t, err)
	for _, m := range metrics {
		require.NoError(t, enc.StartMetricID(m.id, len(m.values)))
		for i, v := range m.values {
			require.NoError(t, enc.AddDataPoint(handleTestStart+int64(i)*1_000_000, v, ""))
		}
		require.NoError(t, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(t, err)
	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)

	schemes := map[byte]bool{}
	for _, m := range metrics {
		entry := blob.index.entryByID(m.id)
		require.NotNil(t, entry)
		require.Equalf(t, m.scheme, blob.valPayload[entry.ValueOffset], "metric %d: column scheme byte", m.id)
		schemes[m.scheme] = true
		h, ok := blob.Metric(m.id)
		require.True(t, ok)
		requireHandleAgrees(t, &h, blob, m.id, AccessDirect, AccessDirect)
	}
	require.True(t, schemes[3] && schemes[0], "the fixture mixes runs and plain columns")
}

// TestNumericMetric_UnsupportedEncodings pins the unsupported rows of both tables.
// The blob's header names an encoding the accessors cannot read.
func TestNumericMetric_UnsupportedEncodings(t *testing.T) {
	ms := handleTestMetrics(1, 8, handleTestIdentical)
	base := handleTestBlob(t, ms, false, WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw))

	t.Run("timestamps", func(t *testing.T) {
		blob := base
		blob.tsEncType = format.TypeGorilla
		h, ok := blob.Metric(1)
		require.True(t, ok, "the metric exists even when its timestamps cannot be read")
		require.Equal(t, AccessUnsupported, h.TimestampAccess())
		require.Equal(t, AccessDirect, h.ValueAccess())
		requireHandleAgrees(t, &h, blob, 1, AccessUnsupported, AccessDirect)
		h.Materialize()
		requireHandleAgrees(t, &h, blob, 1, AccessUnsupported, AccessDirect)
		_, ok = h.TimestampAt(0)
		require.False(t, ok)
		_, ok = h.At(0)
		require.False(t, ok, "At needs every axis")
		_, ok = h.ValueAt(0)
		require.True(t, ok, "the value axis is unaffected")
	})

	t.Run("values", func(t *testing.T) {
		blob := base
		blob.valEncType = format.TypeDelta
		h, ok := blob.Metric(1)
		require.True(t, ok)
		require.Equal(t, AccessDirect, h.TimestampAccess())
		require.Equal(t, AccessUnsupported, h.ValueAccess())
		requireHandleAgrees(t, &h, blob, 1, AccessDirect, AccessUnsupported)
		h.Materialize()
		requireHandleAgrees(t, &h, blob, 1, AccessDirect, AccessUnsupported)
		_, ok = h.ValueAt(0)
		require.False(t, ok)
		_, ok = h.At(0)
		require.False(t, ok)
		_, ok = h.TimestampAt(0)
		require.True(t, ok)
	})
}

// TestNumericMetric_TagAt pins TagAt on a tagless blob, ("", true) for every valid index,
// and on a tagged one, where it agrees with NumericBlob.TagAt.
func TestNumericMetric_TagAt(t *testing.T) {
	for _, tagged := range []bool{false, true} {
		t.Run(fmt.Sprintf("tagged=%t", tagged), func(t *testing.T) {
			ms := handleTestMetrics(2, 12, handleTestIdentical)
			blob := handleTestBlob(t, ms, false, WithTagsEnabled(tagged))
			require.Equal(t, tagged, blob.HasTag())
			h, ok := blob.Metric(2)
			require.True(t, ok)
			requireHandleAgrees(t, &h, blob, 2, AccessSequential, AccessSequential)
			for i := range h.Len() {
				tag, ok := h.TagAt(i)
				require.True(t, ok)
				if tagged {
					require.Equal(t, ms[1].tags[i], tag)
				} else {
					require.Empty(t, tag)
				}
				dp, ok := h.At(i)
				require.True(t, ok)
				require.Equal(t, tag, dp.Tag)
			}
			_, ok = h.TagAt(h.Len())
			require.False(t, ok)
			_, ok = h.TagAt(-1)
			require.False(t, ok)
		})
	}
}

// TestNumericMetric_AgreesWithNumericBlob is the blob half of the accessor-parity gate:
// every encoding pair, with and without shared timestamps, tags, big-endian bytes and retained names,
// on a blob whose last metric has a unique timestamp sequence.
func TestNumericMetric_AgreesWithNumericBlob(t *testing.T) {
	tsEncs := []format.EncodingType{format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked}
	valEncs := []format.EncodingType{format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE}
	flags := []bool{false, true}
	for _, tsEnc := range tsEncs {
		for _, valEnc := range valEncs {
			for _, shared := range flags {
				for _, tagged := range flags {
					for _, big := range flags {
						for _, names := range flags {
							name := fmt.Sprintf("%s/%s/shared=%t/tags=%t/big=%t/names=%t", tsEnc, valEnc, shared, tagged, big, names)
							t.Run(name, func(t *testing.T) {
								testNumericMetricParity(t, tsEnc, valEnc, shared, tagged, big, names)
							})
						}
					}
				}
			}
		}
	}
}

func testNumericMetricParity(t *testing.T, tsEnc, valEnc format.EncodingType, shared, tagged, big, names bool) {
	t.Helper()
	opts := []NumericEncoderOption{WithTimestampEncoding(tsEnc), WithValueEncoding(valEnc), WithTagsEnabled(tagged)}
	if shared {
		opts = append(opts, WithSharedTimestamps())
	}
	if big {
		opts = append(opts, WithBigEndian())
	}
	ms := handleTestMetrics(3, 20, handleTestUniqueLast)
	blob := handleTestBlob(t, ms, names, opts...)
	require.Equal(t, big, blob.IsBigEndian())

	wantVal := AccessDirect
	if valEnc == format.TypeGorilla || valEnc == format.TypeChimp {
		wantVal = AccessSequential
	}
	for k, m := range ms {
		wantTs := AccessSequential
		if tsEnc == format.TypeRaw || (shared && k < len(ms)-1) {
			wantTs = AccessDirect
		}
		id := handleTestID(m, names)
		h, ok := blob.Metric(id)
		require.True(t, ok, "Metric")
		requireHandleAgrees(t, &h, blob, id, wantTs, wantVal)
		requireMaterializedHandleAgrees(t, &h, blob, id, wantTs, wantVal)

		hn, ok := blob.MetricByName(m.name)
		if names {
			require.True(t, ok, "MetricByName with retained names")
			requireHandleAgrees(t, &hn, blob, id, wantTs, wantVal)
		} else {
			require.False(t, ok, "MetricByName hashes the name, which is not the ID the blob stores")
			require.Equal(t, NumericMetric{}, hn)
		}
	}
	_, ok := blob.Metric(999_999)
	require.False(t, ok)
	_, ok = blob.MetricByName("absent")
	require.False(t, ok)
}

// TestNumericMetric_ByNameCollision pins the by-name resolution on a blob whose two names share one ID:
// each name resolves to its own column, and the ID resolves to the first entry, as the blob accessors do.
func TestNumericMetric_ByNameCollision(t *testing.T) {
	dec, err := NewNumericDecoder(encodeCollisionNumeric(t))
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)

	hA, ok := blob.MetricByName(cnA)
	require.True(t, ok)
	hB, ok := blob.MetricByName(cnB)
	require.True(t, ok)
	require.Equal(t, 2, hA.Len())
	require.Equal(t, 3, hB.Len())
	vA, ok := hA.ValueAt(0)
	require.True(t, ok)
	require.InDelta(t, 1.0, vA, 0)
	vB, ok := hB.ValueAt(0)
	require.True(t, ok)
	require.InDelta(t, 2.0, vB, 0)

	hID, ok := blob.Metric(cnH)
	require.True(t, ok)
	require.Equal(t, blob.Len(cnH), hID.Len())
	want, ok := blob.ValueAt(cnH, 0)
	require.True(t, ok)
	got, ok := hID.ValueAt(0)
	require.True(t, ok)
	require.InDelta(t, want, got, 0)
}

// TestNumericMetric_ZeroValue pins the documented zero value: nothing to read, nothing supported.
func TestNumericMetric_ZeroValue(t *testing.T) {
	var h NumericMetric
	require.Equal(t, 0, h.Len())
	require.Equal(t, int64(0), h.Duration())
	require.Equal(t, AccessUnsupported, h.TimestampAccess())
	require.Equal(t, AccessUnsupported, h.ValueAccess())
	h.ForEach(func(int, NumericDataPoint) bool { t.Fatal("ForEach on the zero value"); return false })
	h.ForEachValues(func(int, float64) bool { t.Fatal("ForEachValues on the zero value"); return false })
	h.ForEachTimestamps(func(int, int64) bool { t.Fatal("ForEachTimestamps on the zero value"); return false })
	h.ForEach(nil)
	h.ForEachValues(nil)
	h.ForEachTimestamps(nil)
	h.Materialize()
	require.Equal(t, NumericMetric{}, h, "Materialize on the zero value does nothing")
	for _, i := range []int{-1, 0, 1} {
		_, ok := h.ValueAt(i)
		require.False(t, ok, "ValueAt(%d)", i)
		_, ok = h.TimestampAt(i)
		require.False(t, ok, "TimestampAt(%d)", i)
		_, ok = h.TagAt(i)
		require.False(t, ok, "TagAt(%d)", i)
		_, ok = h.At(i)
		require.False(t, ok, "At(%d)", i)
	}
}

// TestNumericMetric_CopyAnswersAlike pins that a copied handle serves the same reads.
func TestNumericMetric_CopyAnswersAlike(t *testing.T) {
	ms := handleTestMetrics(2, 16, handleTestIdentical)
	blob := handleTestBlob(t, ms, false, WithTagsEnabled(true))
	h, ok := blob.Metric(1)
	require.True(t, ok)
	h2 := h
	for i := range h.Len() {
		want, ok := h.At(i)
		require.True(t, ok)
		got, ok := h2.At(i)
		require.True(t, ok)
		require.Equal(t, want, got)
	}
}

// TestNumericMetric_CorruptIndexEntry pins a handle on an entry whose ranges fall outside the payloads:
// it exists, reports its count, and fails every read cleanly, as the NumericBlob accessors do.
func TestNumericMetric_CorruptIndexEntry(t *testing.T) {
	const metricID = uint64(12345)
	entries := []struct {
		name  string
		entry section.NumericIndexEntry
	}{
		{"offset far beyond payload", section.NumericIndexEntry{
			MetricID: metricID, Count: 4,
			TimestampOffset: 1 << 20, TimestampLength: 32,
			ValueOffset: 1 << 20, ValueLength: 32,
			TagOffset: 1 << 20, TagLength: 16,
		}},
		{"length overflows payload", section.NumericIndexEntry{
			MetricID: metricID, Count: 4,
			TimestampOffset: 0, TimestampLength: math.MaxInt,
			ValueOffset: 0, ValueLength: math.MaxInt,
			TagOffset: 1 << 20, TagLength: 16,
		}},
	}
	for _, tt := range entries {
		t.Run(tt.name, func(t *testing.T) {
			blob := NumericBlob{
				blobBase: blobBase{
					tsEncType:  format.TypeRaw,
					valEncType: format.TypeRaw,
					flags:      section.FlagTagEnabled,
				},
				index:      newNumericTestIndex(tt.entry),
				tsPayload:  make([]byte, 32),
				valPayload: make([]byte, 32),
				tagPayload: make([]byte, 32),
			}
			h, ok := blob.Metric(metricID)
			require.True(t, ok)
			require.Equal(t, 4, h.Len())
			requireHandleAgrees(t, &h, blob, metricID, AccessDirect, AccessDirect)
			h.Materialize()
			requireHandleAgrees(t, &h, blob, metricID, AccessDirect, AccessDirect)
			for i := range 4 {
				_, ok := h.ValueAt(i)
				require.False(t, ok)
				_, ok = h.TimestampAt(i)
				require.False(t, ok)
				_, ok = h.TagAt(i)
				require.False(t, ok)
			}
		})
	}
}

// TestNumericMetric_EmptyMetric pins an entry with no points: Len 0, every read false,
// and the classes still follow the encodings until Materialize, which decodes nothing and leaves both axes direct.
func TestNumericMetric_EmptyMetric(t *testing.T) {
	const metricID = uint64(7)
	newBlob := func(tsEnc format.EncodingType) NumericBlob {
		return NumericBlob{
			blobBase: blobBase{tsEncType: tsEnc, valEncType: format.TypeChimp},
			index:    newNumericTestIndex(section.NumericIndexEntry{MetricID: metricID}),
		}
	}
	for _, tt := range []struct {
		tsEnc format.EncodingType
		want  AccessClass
	}{
		{format.TypeRaw, AccessDirect},
		{format.TypeDelta, AccessSequential},
	} {
		blob := newBlob(tt.tsEnc)
		h, ok := blob.Metric(metricID)
		require.True(t, ok)
		require.Equal(t, 0, h.Len())
		requireHandleAgrees(t, &h, blob, metricID, tt.want, AccessSequential)
		h.Materialize()
		requireHandleAgrees(t, &h, blob, metricID, AccessDirect, AccessDirect)
	}
}

// TestNumericMetric_PartsAfterFirst pins index placement over the first part and the rest,
// the layout the set constructors fill: each part answers its own range with its own class,
// and the handle reports the worst class.
func TestNumericMetric_PartsAfterFirst(t *testing.T) {
	msA := handleTestMetrics(2, 10, handleTestIdentical)
	msB := handleTestMetrics(2, 7, handleTestAllUnique)
	for i := range msB[1].ts {
		msB[1].ts[i] += 10 * 15_000_000
	}
	blobA := handleTestBlob(t, msA, false, WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeALP), WithTagsEnabled(true))
	blobB := handleTestBlob(t, msB, false, WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeChimp))
	entryA := blobA.index.entryByID(2)
	entryB := blobB.index.entryByID(2)
	require.NotNil(t, entryA)
	require.NotNil(t, entryB)

	h := NumericMetric{rest: make([]numericMetricPart, 1)}
	fillNumericMetricPart(&h.first, &blobA, entryA, 0)
	fillNumericMetricPart(&h.rest[0], &blobB, entryB, entryA.Count)
	require.Equal(t, 17, h.Len())
	require.Equal(t, AccessSequential, h.TimestampAccess(), "worst of Raw and Delta")
	require.Equal(t, AccessSequential, h.ValueAccess(), "worst of ALP and Chimp")

	for i := -1; i <= 17; i++ {
		got, ok := h.At(i)
		var (
			want   NumericDataPoint
			wantOk bool
		)
		switch {
		case i < 0 || i >= 17:
		case i < 10:
			want, wantOk = numericPointFromEntry(blobA, entryA, i)
		default:
			want, wantOk = numericPointFromEntry(blobB, entryB, i-10)
		}
		require.Equal(t, wantOk, ok, "At(%d) ok", i)
		require.Equal(t, want, got, "At(%d)", i)
	}
}

// TestNumericMetric_ShortSharedGroupFallsThrough pins the malformed-group rule:
// a pre-decoded group shorter than the metric's count serves the indices it has,
// and the rest decode from the metric's own payload, as NumericBlob.TimestampAt does on the same blob;
// the class stays direct because the group exists.
func TestNumericMetric_ShortSharedGroupFallsThrough(t *testing.T) {
	const points = 12
	ms := handleTestMetrics(3, points, handleTestIdentical)
	blob := handleTestBlob(t, ms, false,
		WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeALP))
	require.NotNil(t, blob.sharedTs)
	full := blob.sharedTs.first.ts
	require.Len(t, full, points)

	short := blob
	group := sharedTimestampGroup{offset: blob.sharedTs.first.offset, ts: full[:5]}
	short.sharedTs = &sharedTimestamps{first: group, groups: []sharedTimestampGroup{group}}

	h, ok := short.Metric(2)
	require.True(t, ok)
	requireHandleAgrees(t, &h, short, 2, AccessDirect, AccessDirect)
	for i := range points {
		got, ok := h.TimestampAt(i)
		require.True(t, ok)
		require.Equal(t, ms[1].ts[i], got, "TimestampAt(%d) through the group or the payload", i)
	}

	// Iteration follows NumericBlob.ForEachTimestamps, which yields the group and nothing after it.
	var want, got []int64
	short.ForEachTimestamps(2, func(_ int, ts int64) bool { want = append(want, ts); return true })
	h.ForEachTimestamps(func(_ int, ts int64) bool { got = append(got, ts); return true })
	require.Len(t, want, 5)
	require.Equal(t, want, got)

	// Materialize decodes the payload behind a short group, so every lookup is direct;
	// iteration still yields the group, as NumericBlob.ForEachTimestamps does.
	walk := collectHandle(&h)
	h.Materialize()
	require.Len(t, h.first.timestamps, points, "a short group is decoded from the payload")
	requireHandleAgrees(t, &h, short, 2, AccessDirect, AccessDirect)
	require.Equal(t, walk, collectHandle(&h), "iteration before and after Materialize")
}

// TestNumericMetric_EmptyFirstPart pins placement when the inline part holds no points
// and the first part after it starts at base 0.
func TestNumericMetric_EmptyFirstPart(t *testing.T) {
	empty := NumericBlob{
		blobBase: blobBase{tsEncType: format.TypeRaw, valEncType: format.TypeRaw},
		index:    newNumericTestIndex(section.NumericIndexEntry{MetricID: 1}),
	}
	emptyEntry := empty.index.entryByID(1)
	require.NotNil(t, emptyEntry)
	ms := handleTestMetrics(1, 9, handleTestIdentical)
	blob := handleTestBlob(t, ms, false, WithTagsEnabled(true))
	entry := blob.index.entryByID(1)
	require.NotNil(t, entry)

	h := NumericMetric{rest: make([]numericMetricPart, 1)}
	fillNumericMetricPart(&h.first, &empty, emptyEntry, 0)
	fillNumericMetricPart(&h.rest[0], &blob, entry, 0)
	require.Equal(t, 9, h.Len())
	require.Equal(t, AccessSequential, h.TimestampAccess(), "worst of Raw and the default Delta")
	require.Equal(t, AccessSequential, h.ValueAccess(), "worst of Raw and the default Gorilla")
	for _, i := range []int{-1, 0, 1, 8, 9} {
		want, wantOk := numericPointFromEntry(blob, entry, i)
		got, ok := h.At(i)
		require.Equal(t, wantOk, ok, "At(%d) ok", i)
		require.Equal(t, want, got, "At(%d)", i)
	}
}

// TestNumericMetric_ResolveAllocatesNothing pins the resolution gate for the blob form: no allocation.
func TestNumericMetric_ResolveAllocatesNothing(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not stable under the race detector")
	}
	ms := handleTestMetrics(50, 20, handleTestIdentical)
	byID := handleTestBlob(t, ms, false, WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeALP))
	byName := handleTestBlob(t, ms, true, WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeALP))

	var sink int
	allocs := testing.AllocsPerRun(100, func() {
		h, _ := byID.Metric(25)
		sink += h.Len()
	})
	require.Zero(t, allocs, "Metric")
	allocs = testing.AllocsPerRun(100, func() {
		h, _ := byName.MetricByName("metric.25")
		sink += h.Len()
	})
	require.Zero(t, allocs, "MetricByName")
	require.Positive(t, sink)
}

// TestAccessClass_String pins the names, including the one for a value outside the three classes.
func TestAccessClass_String(t *testing.T) {
	tests := []struct {
		class AccessClass
		want  string
	}{
		{AccessDirect, "Direct"},
		{AccessSequential, "Sequential"},
		{AccessUnsupported, "Unsupported"},
		{AccessClass(9), "Unknown"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, tt.class.String())
	}
}

// handleTestSetMember describes one numeric member of a handle test set: which metrics it holds and its own options.
type handleTestSetMember struct {
	names []string
	opts  []NumericEncoderOption
}

// handleTestSetNames are the metric names of a handle test set:
// "all" is in every member, "some" in the second and fourth, "one" in the third only,
// and "both" in the first numeric member and in the text member, which also holds "text.only".
var handleTestSetNames = []string{"all", "some", "one", "both"}

// handleTestSetMembers returns the four numeric members of the standard handle test set.
func handleTestSetMembers() []handleTestSetMember {
	return []handleTestSetMember{
		{names: []string{"all", "both"}},
		{names: []string{"all", "some"}},
		{names: []string{"all", "one"}},
		{names: []string{"all", "some"}},
	}
}

// handleTestSetExpected is what the set holds for one metric, concatenated over the members in start-time order.
type handleTestSetExpected struct {
	ts   []int64
	vals []float64
	tags []string
}

// handleTestSet builds a BlobSet of the given numeric members, each at a later start time with later timestamps,
// plus one text member holding "text.only" and "both".
// With names, the numeric members retain metric names; otherwise metrics are stored by the IDs handleTestSetID gives.
// It returns the set and the expected series per metric name.
func handleTestSet(tb testing.TB, members []handleTestSetMember, names bool, common ...NumericEncoderOption) (BlobSet, map[string]handleTestSetExpected) {
	tb.Helper()
	const points = 20
	expected := map[string]handleTestSetExpected{}
	numeric := make([]NumericBlob, 0, len(members))
	for k, member := range members {
		start := handleTestStart + int64(k)*3_600_000_000
		ms := make([]handleTestMetric, 0, len(member.names))
		for _, name := range member.names {
			ts, vals, tags := handleTestSeries(points, int64(k)*points*15_000_000, float64(handleTestSetID(name))*10)
			ms = append(ms, handleTestMetric{id: handleTestSetID(name), name: name, ts: ts, vals: vals, tags: tags})
			e := expected[name]
			e.ts = append(e.ts, ts...)
			e.vals = append(e.vals, vals...)
			e.tags = append(e.tags, tags...)
			expected[name] = e
		}
		opts := append(append([]NumericEncoderOption{}, common...), member.opts...)
		numeric = append(numeric, handleTestBlobAt(tb, start, ms, names, opts...))
	}

	enc, err := NewTextEncoder(time.UnixMicro(handleTestStart))
	require.NoError(tb, err)
	for _, name := range []string{"text.only", "both"} {
		require.NoError(tb, enc.StartMetricName(name, 3))
		for i := range 3 {
			require.NoError(tb, enc.AddDataPoint(handleTestStart+int64(i)*1_000_000, name+strconv.Itoa(i), ""))
		}
		require.NoError(tb, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(tb, err)
	dec, err := NewTextDecoder(data)
	require.NoError(tb, err)
	text, err := dec.Decode()
	require.NoError(tb, err)

	return NewBlobSet(numeric, []TextBlob{text}), expected
}

// handleTestSetID is the metric ID a handle test set stores for name when it does not retain names.
func handleTestSetID(name string) uint64 {
	for i, n := range handleTestSetNames {
		if n == name {
			return uint64(i + 1)
		}
	}

	return 0
}

// handleWalk is what the three ForEach forms of a handle yielded, with the indices ForEach reported.
type handleWalk struct {
	idx    []int
	points []NumericDataPoint
	vals   []float64
	ts     []int64
}

// collectHandle drains the three ForEach forms of h.
func collectHandle(h *NumericMetric) handleWalk {
	var w handleWalk
	h.ForEach(func(i int, dp NumericDataPoint) bool {
		w.idx = append(w.idx, i)
		w.points = append(w.points, dp)

		return true
	})
	h.ForEachValues(func(_ int, v float64) bool { w.vals = append(w.vals, v); return true })
	h.ForEachTimestamps(func(_ int, t int64) bool { w.ts = append(w.ts, t); return true })

	return w
}

// requireSetHandleAgrees checks every handle accessor against the BlobSet accessor it replaces, by ID and,
// when byName is set, by name, at every index from -1 to Len inclusive, and the two access classes.
func requireSetHandleAgrees(t *testing.T, h *NumericMetric, bs BlobSet, id uint64, name string, byName bool, wantTs, wantVal AccessClass) {
	t.Helper()
	n := bs.MetricLen(id)
	require.Equal(t, n, h.Len(), "Len")
	require.Equal(t, bs.MetricDuration(id), h.Duration(), "Duration")
	require.Equal(t, wantTs, h.TimestampAccess(), "TimestampAccess")
	require.Equal(t, wantVal, h.ValueAccess(), "ValueAccess")
	if byName {
		requireSetHandleAgreesByName(t, h, bs, name)
	}
	for i := -1; i <= n; i++ {
		wantV, wantVOk := bs.NumericValueAt(id, i)
		gotV, gotVOk := h.ValueAt(i)
		require.Equal(t, wantVOk, gotVOk, "ValueAt(%d) ok", i)
		require.Equal(t, math.Float64bits(wantV), math.Float64bits(gotV), "ValueAt(%d)", i)
		wantTS, wantTSOk := bs.TimestampAt(id, i)
		gotTS, gotTSOk := h.TimestampAt(i)
		require.Equal(t, wantTSOk, gotTSOk, "TimestampAt(%d) ok", i)
		require.Equal(t, wantTS, gotTS, "TimestampAt(%d)", i)
		wantTag, wantTagOk := bs.TagAt(id, i)
		gotTag, gotTagOk := h.TagAt(i)
		require.Equal(t, wantTagOk, gotTagOk, "TagAt(%d) ok", i)
		require.Equal(t, wantTag, gotTag, "TagAt(%d)", i)
		wantDP, wantDPOk := bs.NumericAt(id, i)
		gotDP, gotDPOk := h.At(i)
		require.Equal(t, wantDPOk, gotDPOk, "At(%d) ok", i)
		require.Equal(t, wantDP, gotDP, "At(%d)", i)
	}

	w := collectHandle(h)
	require.Len(t, w.idx, n, "ForEach count")
	for k := 1; k < len(w.idx); k++ {
		require.Equal(t, w.idx[k-1]+1, w.idx[k], "ForEach indices are contiguous")
	}
	if n > 0 {
		require.Equal(t, 0, w.idx[0], "ForEach starts at 0")
	}
	var wantPoints []NumericDataPoint
	for _, dp := range bs.AllNumerics(id) {
		wantPoints = append(wantPoints, dp)
	}
	require.Equal(t, wantPoints, w.points, "ForEach against AllNumerics")
	var wantVals []float64
	for _, v := range bs.AllNumericValues(id) {
		wantVals = append(wantVals, v)
	}
	require.Equal(t, wantVals, w.vals, "ForEachValues against AllNumericValues")
	var wantTimestamps []int64
	for _, v := range bs.AllTimestamps(id) {
		wantTimestamps = append(wantTimestamps, v)
	}
	require.Equal(t, wantTimestamps, w.ts, "ForEachTimestamps against AllTimestamps")
	for i := range n {
		dp, ok := h.At(i)
		require.True(t, ok)
		require.Equal(t, dp, w.points[i], "ForEach index %d names the point At returns", i)
	}

	// The set-level callback API the handle replaces, indices included.
	set, err := NewNumericBlobSet(bs.NumericBlobs())
	require.NoError(t, err)
	var setIdx []int
	var setPoints []NumericDataPoint
	set.ForEach(id, func(i int, dp NumericDataPoint) bool {
		setIdx = append(setIdx, i)
		setPoints = append(setPoints, dp)

		return true
	})
	require.Equal(t, w.idx, setIdx, "ForEach indices against NumericBlobSet.ForEach")
	require.Equal(t, w.points, setPoints, "ForEach against NumericBlobSet.ForEach")
	var setVals []float64
	set.ForEachValues(id, func(_ int, v float64) bool { setVals = append(setVals, v); return true })
	require.Equal(t, w.vals, setVals, "ForEachValues against NumericBlobSet.ForEachValues")
	var setTs []int64
	set.ForEachTimestamps(id, func(_ int, v int64) bool { setTs = append(setTs, v); return true })
	require.Equal(t, w.ts, setTs, "ForEachTimestamps against NumericBlobSet.ForEachTimestamps")
}

// requireMaterializedSetHandleAgrees materializes a copy of h and checks it as requireSetHandleAgrees does,
// with both axes direct; h, copied before Materialize, keeps its classes and its answers.
func requireMaterializedSetHandleAgrees(t *testing.T, h *NumericMetric, bs BlobSet, id uint64, name string, byName bool, wantTs, wantVal AccessClass) {
	t.Helper()
	hm := *h
	hm.Materialize()
	requireSetHandleAgrees(t, &hm, bs, id, name, byName, AccessDirect, AccessDirect)
	require.Equal(t, collectHandle(h), collectHandle(&hm), "iteration before and after Materialize")
	requireSetHandleAgrees(t, h, bs, id, name, byName, wantTs, wantVal)
}

// requireSetHandleAgreesByName checks every handle accessor against the BlobSet ByName accessor it replaces,
// at every index from -1 to Len inclusive, and the three ForEach forms against NumericBlobSet's ByName forms.
func requireSetHandleAgreesByName(t *testing.T, h *NumericMetric, bs BlobSet, name string) {
	t.Helper()
	n := bs.MetricLenByName(name)
	require.Equal(t, n, h.Len(), "MetricLenByName")
	require.Equal(t, bs.MetricDurationByName(name), h.Duration(), "MetricDurationByName")
	for i := -1; i <= n; i++ {
		gotV, gotVOk := h.ValueAt(i)
		v, ok := bs.NumericValueAtByName(name, i)
		require.Equal(t, gotVOk, ok, "NumericValueAtByName(%d) ok", i)
		require.Equal(t, math.Float64bits(gotV), math.Float64bits(v), "NumericValueAtByName(%d)", i)
		gotTS, gotTSOk := h.TimestampAt(i)
		ts, ok := bs.TimestampAtByName(name, i)
		require.Equal(t, gotTSOk, ok, "TimestampAtByName(%d) ok", i)
		require.Equal(t, gotTS, ts, "TimestampAtByName(%d)", i)
		gotTag, gotTagOk := h.TagAt(i)
		tag, ok := bs.TagAtByName(name, i)
		require.Equal(t, gotTagOk, ok, "TagAtByName(%d) ok", i)
		require.Equal(t, gotTag, tag, "TagAtByName(%d)", i)
		gotDP, gotDPOk := h.At(i)
		dp, ok := bs.NumericAtByName(name, i)
		require.Equal(t, gotDPOk, ok, "NumericAtByName(%d) ok", i)
		require.Equal(t, gotDP, dp, "NumericAtByName(%d)", i)
	}

	w := collectHandle(h)
	set, err := NewNumericBlobSet(bs.NumericBlobs())
	require.NoError(t, err)
	var setIdx []int
	var setPoints []NumericDataPoint
	set.ForEachByName(name, func(i int, dp NumericDataPoint) bool {
		setIdx = append(setIdx, i)
		setPoints = append(setPoints, dp)

		return true
	})
	require.Equal(t, w.idx, setIdx, "ForEach indices against NumericBlobSet.ForEachByName")
	require.Equal(t, w.points, setPoints, "ForEach against NumericBlobSet.ForEachByName")
	var setVals []float64
	set.ForEachValuesByName(name, func(_ int, v float64) bool { setVals = append(setVals, v); return true })
	require.Equal(t, w.vals, setVals, "ForEachValues against NumericBlobSet.ForEachValuesByName")
	var setTs []int64
	set.ForEachTimestampsByName(name, func(_ int, v int64) bool { setTs = append(setTs, v); return true })
	require.Equal(t, w.ts, setTs, "ForEachTimestamps against NumericBlobSet.ForEachTimestampsByName")
	var wantPoints []NumericDataPoint
	for _, dp := range bs.AllNumericsByName(name) {
		wantPoints = append(wantPoints, dp)
	}
	require.Equal(t, wantPoints, w.points, "ForEach against AllNumericsByName")
}

// TestNumericMetric_SetAgreesWithBlobSet is the set half of the accessor-parity gate,
// over members that differ in which metrics they hold, with retained names and without, on three encoding layouts.
func TestNumericMetric_SetAgreesWithBlobSet(t *testing.T) {
	layouts := []struct {
		name    string
		opts    []NumericEncoderOption
		wantTs  AccessClass
		wantVal AccessClass
	}{
		{"raw-ts/alp", []NumericEncoderOption{WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeALP)}, AccessDirect, AccessDirect},
		{"shared-deltapacked/chimp/tags", []NumericEncoderOption{
			WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeChimp), WithTagsEnabled(true),
		}, AccessDirect, AccessSequential},
		{"delta/gorilla/big-endian", []NumericEncoderOption{
			WithBigEndian(), WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeGorilla),
		}, AccessSequential, AccessSequential},
	}
	for _, layout := range layouts {
		for _, names := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/names=%t", layout.name, names), func(t *testing.T) {
				bs, expected := handleTestSet(t, handleTestSetMembers(), names, layout.opts...)
				for _, name := range handleTestSetNames {
					id := handleTestSetID(name)
					if names {
						id = hash.ID(name)
					}
					h, ok := bs.NumericMetric(id)
					require.True(t, ok, "NumericMetric(%s)", name)
					require.Equal(t, expected[name].ts, collectTs(&h), "metric %s timestamps", name)
					require.Equal(t, expected[name].vals, collectVals(&h), "metric %s values", name)
					requireSetHandleAgrees(t, &h, bs, id, name, names, layout.wantTs, layout.wantVal)
					requireMaterializedSetHandleAgrees(t, &h, bs, id, name, names, layout.wantTs, layout.wantVal)

					hn, ok := bs.NumericMetricByName(name)
					if names {
						require.True(t, ok, "NumericMetricByName(%s)", name)
						requireSetHandleAgrees(t, &hn, bs, id, name, true, layout.wantTs, layout.wantVal)
					} else {
						require.False(t, ok, "without retained names a name resolves by hash, which is not the stored ID")
					}
				}

				// A name that only text members hold: the handle says no, where TimestampAtByName falls back to text.
				_, ok := bs.NumericMetricByName("text.only")
				require.False(t, ok)
				_, ok = bs.NumericMetric(hash.ID("text.only"))
				require.False(t, ok)
				_, ok = bs.TimestampAtByName("text.only", 0)
				require.True(t, ok, "the generic accessor reads the text member")
				_, ok = bs.NumericMetricByName("absent")
				require.False(t, ok)
				_, ok = bs.NumericMetric(999_999)
				require.False(t, ok)
			})
		}
	}
}

func collectTs(h *NumericMetric) []int64 {
	return collectHandle(h).ts
}

func collectVals(h *NumericMetric) []float64 {
	return collectHandle(h).vals
}

// TestNumericMetric_SetMixedMembers pins a set whose members differ per part: one big-endian member,
// one with Raw timestamps among shared DeltaPacked ones,
// so the handle reports the worst class and each part reads with its own byte order and encoding.
func TestNumericMetric_SetMixedMembers(t *testing.T) {
	members := handleTestSetMembers()
	members[1].opts = []NumericEncoderOption{WithBigEndian()}
	members[2].opts = []NumericEncoderOption{WithTimestampEncoding(format.TypeRaw)}
	members[3].opts = []NumericEncoderOption{WithValueEncoding(format.TypeChimp)}
	bs, expected := handleTestSet(t, members, true,
		WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeALP), WithTagsEnabled(true))
	blobs := bs.NumericBlobs()
	require.True(t, blobs[1].IsBigEndian(), "the second member is big-endian, a part after the first")
	require.Equal(t, format.TypeRaw, blobs[2].TimestampEncoding())
	require.Equal(t, format.TypeChimp, blobs[3].ValueEncoding())

	h, ok := bs.NumericMetricByName("all")
	require.True(t, ok)
	require.Equal(t, 80, h.Len())
	require.Equal(t, expected["all"].ts, collectTs(&h))
	requireSetHandleAgrees(t, &h, bs, hash.ID("all"), "all", true, AccessDirect, AccessSequential)
	requireMaterializedSetHandleAgrees(t, &h, bs, hash.ID("all"), "all", true, AccessDirect, AccessSequential)

	one, ok := bs.NumericMetricByName("one")
	require.True(t, ok)
	require.Equal(t, 20, one.Len())
	requireSetHandleAgrees(t, &one, bs, hash.ID("one"), "one", true, AccessDirect, AccessDirect)

	some, ok := bs.NumericMetricByName("some")
	require.True(t, ok)
	require.Equal(t, 40, some.Len())
	requireSetHandleAgrees(t, &some, bs, hash.ID("some"), "some", true, AccessDirect, AccessSequential)
	requireMaterializedSetHandleAgrees(t, &some, bs, hash.ID("some"), "some", true, AccessDirect, AccessSequential)
}

// TestNumericMetric_SetForEachStops pins the callback-stop semantics across parts:
// a callback that returns false inside a later part is not called again, on all three forms.
func TestNumericMetric_SetForEachStops(t *testing.T) {
	bs, _ := handleTestSet(t, handleTestSetMembers(), true, WithTagsEnabled(true))
	h, ok := bs.NumericMetricByName("all")
	require.True(t, ok)
	require.Equal(t, 80, h.Len())
	const stopAt = 45 // inside the third part

	var seen []int
	h.ForEach(func(i int, _ NumericDataPoint) bool { seen = append(seen, i); return i < stopAt })
	require.Len(t, seen, stopAt+1)
	require.Equal(t, stopAt, seen[stopAt])

	seen = seen[:0]
	h.ForEachValues(func(i int, _ float64) bool { seen = append(seen, i); return i < stopAt })
	require.Len(t, seen, stopAt+1)

	seen = seen[:0]
	h.ForEachTimestamps(func(i int, _ int64) bool { seen = append(seen, i); return i < stopAt })
	require.Len(t, seen, stopAt+1)

	// Stopping on the very first point calls nothing else.
	calls := 0
	h.ForEach(func(int, NumericDataPoint) bool { calls++; return false })
	require.Equal(t, 1, calls)
}

// TestNumericMetric_Duration pins Duration: last minus first across parts, 0 for one point,
// 0 when the last timestamp is not after the first, and the BlobSet contract on every set metric.
func TestNumericMetric_Duration(t *testing.T) {
	bs, expected := handleTestSet(t, handleTestSetMembers(), true)
	for _, name := range handleTestSetNames {
		h, ok := bs.NumericMetricByName(name)
		require.True(t, ok)
		ts := expected[name].ts
		require.Equal(t, ts[len(ts)-1]-ts[0], h.Duration(), name)
		require.Equal(t, bs.MetricDurationByName(name), h.Duration(), name)
	}

	one := handleTestBlob(t, []handleTestMetric{{id: 1, ts: []int64{5}, vals: []float64{1}, tags: []string{""}}}, false)
	h, ok := one.Metric(1)
	require.True(t, ok)
	require.Equal(t, int64(0), h.Duration(), "one point")

	descending := handleTestBlob(t, []handleTestMetric{{id: 1, ts: []int64{30, 20, 10}, vals: []float64{1, 2, 3}, tags: []string{"", "", ""}}}, false,
		WithTimestampEncoding(format.TypeRaw))
	h, ok = descending.Metric(1)
	require.True(t, ok)
	require.Equal(t, int64(0), h.Duration(), "never negative")
	single := NewBlobSet([]NumericBlob{descending}, nil)
	require.Equal(t, int64(0), single.MetricDuration(1))
	require.Equal(t, single.MetricDuration(1), h.Duration())
}

// TestNumericMetric_SetCollision pins the set's logical identity: two names on one ID across a collided member
// and a stripped member, resolved as NumericValueAt and NumericValueAtByName resolve them.
func TestNumericMetric_SetCollision(t *testing.T) {
	dec, err := NewNumericDecoder(encodeCollisionNumeric(t))
	require.NoError(t, err)
	collision, err := dec.Decode()
	require.NoError(t, err)
	stripped := encodeStrippedNumeric(t, time.Now().Truncate(time.Hour).Add(time.Hour), 9.0, 9.5)
	bs := NewBlobSet([]NumericBlob{collision, stripped}, nil)

	hA, ok := bs.NumericMetricByName(cnA)
	require.True(t, ok)
	require.Equal(t, []float64{1.0, 1.5, 9.0, 9.5}, collectVals(&hA), "the first colliding name absorbs the stripped member")
	hB, ok := bs.NumericMetricByName(cnB)
	require.True(t, ok)
	require.Equal(t, []float64{2.0, 2.5, 2.7}, collectVals(&hB), "the second colliding name keeps only its own entry")
	hID, ok := bs.NumericMetric(cnH)
	require.True(t, ok)
	require.Equal(t, []float64{1.0, 1.5, 9.0, 9.5}, collectVals(&hID), "the ID resolves to the first name")

	requireSetHandleAgreesByName(t, &hA, bs, cnA)
	requireSetHandleAgreesByName(t, &hB, bs, cnB)
	requireSetHandleAgrees(t, &hID, bs, cnH, cnA, false, AccessSequential, AccessSequential)
}

// TestNumericMetric_SetEmpty pins the set constructors on an empty set.
func TestNumericMetric_SetEmpty(t *testing.T) {
	bs := NewBlobSet(nil, nil)
	h, ok := bs.NumericMetric(1)
	require.False(t, ok)
	require.Equal(t, NumericMetric{}, h)
	h, ok = bs.NumericMetricByName("all")
	require.False(t, ok)
	require.Equal(t, NumericMetric{}, h)
}

// TestNumericMetric_SetResolveAllocations pins the resolution gate for the set form:
// no allocation when one member contributes, exactly one, for the parts after the first, when several do.
func TestNumericMetric_SetResolveAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not stable under the race detector")
	}
	bs, _ := handleTestSet(t, handleTestSetMembers(), true,
		WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeALP))
	var sink int
	allocs := testing.AllocsPerRun(100, func() {
		h, _ := bs.NumericMetricByName("one")
		sink += h.Len()
	})
	require.Zero(t, allocs, "one contributing member")
	allocs = testing.AllocsPerRun(100, func() {
		h, _ := bs.NumericMetricByName("all")
		sink += h.Len()
	})
	require.InDelta(t, 1, allocs, 0, "four contributing members: one allocation for the parts after the first")
	allocs = testing.AllocsPerRun(100, func() {
		h, _ := bs.NumericMetric(hash.ID("some"))
		sink += h.Len()
	})
	require.InDelta(t, 1, allocs, 0, "two contributing members by ID")
	require.Positive(t, sink)
}

// TestNumericMetric_ForEachCapturingCallbacksStayOnStack pins the set handle's loops:
// ForEach, ForEachValues and ForEachTimestamps keep a call-site callback literal that captures a local on the stack.
// It runs on a direct layout (shared group ranged directly, ALP through the pooled bulk decode)
// and on a sequential one (fused Delta and Gorilla loops).
func TestNumericMetric_ForEachCapturingCallbacksStayOnStack(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not stable under the race detector")
	}
	layouts := []struct {
		name   string
		opts   []NumericEncoderOption
		tagged bool // ForEach copies one tag string per point
	}{
		{"shared-deltapacked/alp", []NumericEncoderOption{
			WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeALP),
		}, false},
		{"delta/gorilla/tags", []NumericEncoderOption{
			WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeGorilla), WithTagsEnabled(true),
		}, true},
	}
	for _, layout := range layouts {
		t.Run(layout.name, func(t *testing.T) {
			bs, _ := handleTestSet(t, handleTestSetMembers(), true, layout.opts...)
			h, ok := bs.NumericMetricByName("all")
			require.True(t, ok)
			require.Len(t, h.rest, 3)
			var sinkTS int64
			var sinkV float64
			allocs := testing.AllocsPerRun(50, func() {
				var sum int64
				h.ForEachTimestamps(func(_ int, ts int64) bool { sum += ts; return true })
				sinkTS += sum
			})
			require.Zero(t, allocs, "ForEachTimestamps")
			allocs = testing.AllocsPerRun(50, func() {
				var sum float64
				h.ForEachValues(func(_ int, v float64) bool { sum += v; return true })
				sinkV += sum
			})
			require.Zero(t, allocs, "ForEachValues")
			allocs = testing.AllocsPerRun(50, func() {
				var sum float64
				h.ForEach(func(_ int, dp NumericDataPoint) bool { sum += dp.Val; return true })
				sinkV += sum
			})
			want := 0.0
			if layout.tagged {
				want = float64(h.Len())
			}
			require.InDelta(t, want, allocs, 0, "ForEach")
			require.NotZero(t, sinkTS)
			require.NotZero(t, sinkV)
		})
	}
}

// materializeTestSet is the consumer's layout over the standard handle test set:
// shared DeltaPacked timestamps, Chimp values and tags, so values and tags need decoding and timestamps do not.
func materializeTestSet(t *testing.T) BlobSet {
	t.Helper()
	bs, _ := handleTestSet(t, handleTestSetMembers(), true,
		WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeChimp), WithTagsEnabled(true))

	return bs
}

// TestNumericMetric_MaterializeCopies pins the copy contract:
// a copy made before Materialize is independent, keeps its classes and owns no decoded slices,
// even for the parts after the first, which a copy shares with the original until Materialize replaces them;
// a copy made after shares the decoded slices.
func TestNumericMetric_MaterializeCopies(t *testing.T) {
	bs := materializeTestSet(t)
	h, ok := bs.NumericMetricByName("all")
	require.True(t, ok)
	require.Len(t, h.rest, 3)
	before := h
	h.Materialize()
	require.Equal(t, AccessDirect, h.ValueAccess())
	require.Equal(t, AccessSequential, before.ValueAccess(), "the copy made before is not materialized")
	require.Nil(t, before.first.values)
	require.Nil(t, before.first.tags)
	for k := range before.rest {
		require.Nil(t, before.rest[k].values, "part %d of the copy made before", k+1)
		require.Nil(t, before.rest[k].tags, "part %d of the copy made before", k+1)
		require.Len(t, h.rest[k].values, h.rest[k].count, "part %d of the materialized handle", k+1)
		require.Len(t, h.rest[k].tags, h.rest[k].count, "part %d of the materialized handle", k+1)
	}

	after := h
	require.Same(t, &h.rest[0], &after.rest[0], "a copy made after shares the parts")
	require.Same(t, &h.first.values[0], &after.first.values[0], "and the decoded slices")
	after.Materialize()
	require.Same(t, &h.rest[0], &after.rest[0], "materializing a materialized copy writes nothing")

	before.Materialize()
	require.NotSame(t, &h.rest[0], &before.rest[0], "the copy made before materializes on its own")
	require.Equal(t, collectHandle(&h), collectHandle(&before))
}

// TestNumericMetric_MaterializeIdempotent pins that a second Materialize decodes nothing and allocates nothing.
func TestNumericMetric_MaterializeIdempotent(t *testing.T) {
	bs := materializeTestSet(t)
	h, ok := bs.NumericMetricByName("all")
	require.True(t, ok)
	h.Materialize()
	values, tags := &h.rest[2].values[0], &h.rest[2].tags[0]
	h.Materialize()
	require.Same(t, values, &h.rest[2].values[0])
	require.Same(t, tags, &h.rest[2].tags[0])
	if raceEnabled {
		return
	}
	require.Zero(t, testing.AllocsPerRun(50, h.Materialize))
}

// TestNumericMetric_MaterializeDirectAllocatesNothing pins gate 4:
// Materialize on a handle whose axes are all direct and whose blobs have no tags allocates nothing,
// on the blob form and on a set form with parts after the first.
func TestNumericMetric_MaterializeDirectAllocatesNothing(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not stable under the race detector")
	}
	layouts := []struct {
		name string
		opts []NumericEncoderOption
	}{
		{"shared-deltapacked/alp", []NumericEncoderOption{
			WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeALP),
		}},
		{"raw/raw", []NumericEncoderOption{WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw)}},
	}
	for _, layout := range layouts {
		t.Run(layout.name, func(t *testing.T) {
			blob := handleTestBlob(t, handleTestMetrics(3, 20, handleTestIdentical), false, layout.opts...)
			bh, ok := blob.Metric(1)
			require.True(t, ok)
			bs, _ := handleTestSet(t, handleTestSetMembers(), true, layout.opts...)
			sh, ok := bs.NumericMetricByName("all")
			require.True(t, ok)
			require.Len(t, sh.rest, 3)
			for name, h := range map[string]NumericMetric{"blob": bh, "set": sh} {
				require.Equal(t, AccessDirect, h.TimestampAccess(), name)
				require.Equal(t, AccessDirect, h.ValueAccess(), name)
				allocs := testing.AllocsPerRun(50, func() {
					c := h
					c.Materialize()
				})
				require.Zero(t, allocs, name)
			}
		})
	}
}

// TestNumericMetric_MaterializeTagsCopyOncePerColumn pins the tag cost of materialization:
// a tag column decodes into one string copy shared by its tags, so the allocations do not grow with the points.
// Raw timestamps and values are direct, so the handle allocates the tag slice and the column string only;
// MaterializeMetric adds its timestamp and value slices.
func TestNumericMetric_MaterializeTagsCopyOncePerColumn(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not stable under the race detector")
	}
	for _, points := range []int{20, 200} {
		ms := handleTestMetrics(1, points, handleTestIdentical)
		blob := handleTestBlob(t, ms, false,
			WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw), WithTagsEnabled(true))
		h, ok := blob.Metric(1)
		require.True(t, ok)
		allocs := testing.AllocsPerRun(50, func() {
			c := h
			c.Materialize()
		})
		require.InDeltaf(t, 2, allocs, 0, "handle, %d points", points)
		allocs = testing.AllocsPerRun(50, func() {
			_, _ = blob.MaterializeMetric(1)
		})
		require.InDeltaf(t, 4, allocs, 0, "MaterializeMetric, %d points", points)

		c := h
		c.Materialize()
		m, ok := blob.MaterializeMetric(1)
		require.True(t, ok)
		require.Equal(t, ms[0].tags, c.first.tags)
		require.Equal(t, ms[0].tags, m.Tags)
	}
}

// TestNumericMetric_MaterializeShortDecode pins the promotion rule on a stream that decodes short:
// the owned slice keeps what the decoder produced, the axis keeps its class,
// and every accessor and ForEach form answers as it did before Materialize.
func TestNumericMetric_MaterializeShortDecode(t *testing.T) {
	ms := handleTestMetrics(1, 40, handleTestIdentical)
	blob := handleTestBlob(t, ms, false,
		WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeGorilla), WithTagsEnabled(true))
	h, ok := blob.Metric(1)
	require.True(t, ok)
	// Cut every column of the part in half, as a corrupt entry whose ranges still lie inside the payloads would.
	h.first.tsBytes = h.first.tsBytes[:len(h.first.tsBytes)/2]
	h.first.valBytes = h.first.valBytes[:len(h.first.valBytes)/2]
	h.first.tagBytes = h.first.tagBytes[:len(h.first.tagBytes)/2]

	before := h
	h.Materialize()
	require.Equal(t, AccessSequential, h.TimestampAccess(), "a short decode does not promote the axis")
	require.Equal(t, AccessSequential, h.ValueAccess())
	require.NotEmpty(t, h.first.timestamps)
	require.Less(t, len(h.first.timestamps), h.first.count)
	require.NotEmpty(t, h.first.values)
	require.Less(t, len(h.first.values), h.first.count)
	require.NotEmpty(t, h.first.tags)
	require.Less(t, len(h.first.tags), h.first.count)
	for i := -1; i <= h.Len(); i++ {
		wantTS, wantTSOk := before.TimestampAt(i)
		gotTS, gotTSOk := h.TimestampAt(i)
		require.Equal(t, wantTSOk, gotTSOk, "TimestampAt(%d) ok", i)
		require.Equal(t, wantTS, gotTS, "TimestampAt(%d)", i)
		wantV, wantVOk := before.ValueAt(i)
		gotV, gotVOk := h.ValueAt(i)
		require.Equal(t, wantVOk, gotVOk, "ValueAt(%d) ok", i)
		require.Equal(t, math.Float64bits(wantV), math.Float64bits(gotV), "ValueAt(%d)", i)
		wantTag, wantTagOk := before.TagAt(i)
		gotTag, gotTagOk := h.TagAt(i)
		require.Equal(t, wantTagOk, gotTagOk, "TagAt(%d) ok", i)
		require.Equal(t, wantTag, gotTag, "TagAt(%d)", i)
	}
	require.Equal(t, collectHandle(&before), collectHandle(&h))
	allocs := testing.AllocsPerRun(10, h.Materialize)
	if !raceEnabled {
		require.Zero(t, allocs, "a short decode is not retried")
	}
}

// TestNumericMetric_MaterializeArenaMixesShortAndCompleteParts pins the shared arrays Materialize carves:
// with a part in the middle whose columns decode short, every part keeps its own share,
// capped at its point count, and every accessor and ForEach form answers as it did before Materialize.
func TestNumericMetric_MaterializeArenaMixesShortAndCompleteParts(t *testing.T) {
	bs, _ := handleTestSet(t, handleTestSetMembers(), true,
		WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeGorilla), WithTagsEnabled(true))
	h, ok := bs.NumericMetricByName("all")
	require.True(t, ok)
	require.Len(t, h.rest, 3)
	short := &h.rest[1]
	short.tsBytes = short.tsBytes[:len(short.tsBytes)/2]
	short.valBytes = short.valBytes[:len(short.valBytes)/2]
	short.tagBytes = short.tagBytes[:len(short.tagBytes)/2]

	before := h
	before.rest = slices.Clone(h.rest)
	h.Materialize()
	for k := range 1 + len(h.rest) {
		p := h.part(k)
		require.Equal(t, p.count, cap(p.timestamps), "part %d timestamps", k)
		require.Equal(t, p.count, cap(p.values), "part %d values", k)
		require.Equal(t, p.count, cap(p.tags), "part %d tags", k)
		if k == 2 {
			require.Less(t, len(p.timestamps), p.count, "the short part's timestamps")
			require.Less(t, len(p.values), p.count, "the short part's values")
			require.Less(t, len(p.tags), p.count, "the short part's tags")
		} else {
			require.Len(t, p.timestamps, p.count, "part %d timestamps", k)
			require.Len(t, p.values, p.count, "part %d values", k)
			require.Len(t, p.tags, p.count, "part %d tags", k)
		}
	}
	for i := -1; i <= h.Len(); i++ {
		wantDP, wantOk := before.At(i)
		gotDP, gotOk := h.At(i)
		require.Equal(t, wantOk, gotOk, "At(%d) ok", i)
		require.Equal(t, wantDP, gotDP, "At(%d)", i)
	}
	require.Equal(t, collectHandle(&before), collectHandle(&h))
}

// TestNumericMetric_MaterializedForEachReadsOwnedSlices pins the iteration after Materialize:
// on the consumer's layout ForEach zips the owned slices and copies no tag,
// and on ALP values with tags it reuses the owned tags and decodes the columns into pooled buffers, allocating nothing.
func TestNumericMetric_MaterializedForEachReadsOwnedSlices(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not stable under the race detector")
	}
	alp, _ := handleTestSet(t, handleTestSetMembers(), true,
		WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeALP), WithTagsEnabled(true))
	for _, tt := range []struct {
		name string
		bs   BlobSet
		want float64
	}{
		{"shared-deltapacked/chimp/tags", materializeTestSet(t), 0},
		{"shared-deltapacked/alp/tags", alp, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, ok := tt.bs.NumericMetricByName("all")
			require.True(t, ok)
			h.Materialize()
			var sink int
			allocs := testing.AllocsPerRun(50, func() {
				var n int
				h.ForEach(func(_ int, dp NumericDataPoint) bool { n += len(dp.Tag); return true })
				sink += n
			})
			require.InDelta(t, tt.want, allocs, 0, "ForEach")
			allocs = testing.AllocsPerRun(50, func() {
				var sum float64
				h.ForEachValues(func(_ int, v float64) bool { sum += v; return true })
				sink += int(sum)
			})
			require.Zero(t, allocs, "ForEachValues")
			require.Positive(t, sink)
		})
	}
}

// TestNumericMetric_MaterializeAllocations pins the allocation side of gate 2 on the consumer's layout:
// resolving a handle and materializing it allocates no more than MaterializeNumericMetricByName on the same metric.
func TestNumericMetric_MaterializeAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not stable under the race detector")
	}
	bs := materializeTestSet(t)
	handle := testing.AllocsPerRun(20, func() {
		h, _ := bs.NumericMetricByName("all")
		h.Materialize()
	})
	materialized := testing.AllocsPerRun(20, func() {
		_, _ = bs.MaterializeNumericMetricByName("all")
	})
	require.LessOrEqual(t, handle, materialized)
	// One for rest at resolution, one to copy it before writing,
	// and for all parts together one value array, one tag array and one string holding every tag column.
	require.InDelta(t, 2+3, handle, 0)

	// Each part's share of the arrays ends where its points do, so no part can reach the next one's.
	h, ok := bs.NumericMetricByName("all")
	require.True(t, ok)
	h.Materialize()
	for k := range h.rest {
		p := &h.rest[k]
		require.Equal(t, p.count, cap(p.values), "part %d values", k)
		require.Equal(t, p.count, cap(p.tags), "part %d tags", k)
	}
}
