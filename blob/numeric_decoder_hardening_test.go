package blob

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/section"
)

// encodeHardeningBlob encodes two metrics (IDs 1 and 2, three points each) with
// the given options and no compression, so index entries can be patched in place.
func encodeHardeningBlob(t *testing.T, opts ...NumericEncoderOption) []byte {
	t.Helper()

	enc, err := NewNumericEncoder(time.Unix(1_700_000_000, 0).UTC(), opts...)
	require.NoError(t, err)
	for id := uint64(1); id <= 2; id++ {
		require.NoError(t, enc.StartMetricID(id, 3))
		for i := range 3 {
			require.NoError(t, enc.AddDataPoint(1_700_000_000_000_000+int64(i)*1_000_000, float64(id)*10+float64(i), "tag"))
		}
		require.NoError(t, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(t, err)

	return data
}

// patchCompactCount overwrites the Count of compact index entry i (no names payload).
func patchCompactCount(data []byte, i int, count uint16) []byte {
	out := append([]byte(nil), data...)
	off := section.HeaderSize + i*section.NumericIndexEntrySize + 8
	endian.GetLittleEndianEngine().PutUint16(out[off:off+2], count)

	return out
}

func decodeHardening(t *testing.T, data []byte) (NumericBlob, error) {
	t.Helper()

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)

	var blob NumericBlob
	require.NotPanics(t, func() { blob, err = decoder.Decode() })

	return blob, err
}

// TestNumericDecoder_RejectsCountBeyondPayload pins that Decode rejects an index
// entry whose Count cannot fit its encoded payload, instead of letting read
// paths trust it (reading a neighbor metric, fabricating points, or sizing
// allocations from it).
func TestNumericDecoder_RejectsCountBeyondPayload(t *testing.T) {
	cases := []struct {
		name  string
		opts  []NumericEncoderOption
		count uint16
	}{
		{"raw/raw one extra point", []NumericEncoderOption{WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw)}, 4},
		{"delta/gorilla count above ts bytes", []NumericEncoderOption{WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeGorilla)}, 60000},
		{"delta/alp count above ts bytes", []NumericEncoderOption{WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeALP)}, 60000},
		{"tags fewer bytes than points", []NumericEncoderOption{WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw), WithTagsEnabled(true)}, 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := encodeHardeningBlob(t, tc.opts...)
			_, err := decodeHardening(t, data)
			require.NoError(t, err, "unpatched blob must decode")

			_, err = decodeHardening(t, patchCompactCount(data, 0, tc.count))
			require.ErrorIs(t, err, errs.ErrInvalidNumOfDataPoints)
		})
	}
}

// TestNumericDecoder_RejectsSharedTimestampCountMismatch pins that a shared
// timestamp member whose Count differs from its canonical entry is rejected:
// it would otherwise read the canonical's timestamps against its own values.
func TestNumericDecoder_RejectsSharedTimestampCountMismatch(t *testing.T) {
	data := encodeHardeningBlob(t, WithBlobLayoutV2(), WithSharedTimestamps(),
		WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeGorilla))

	blob, err := decodeHardening(t, data)
	require.NoError(t, err)
	require.True(t, blob.IsV2Layout())

	_, err = decodeHardening(t, patchCompactCount(data, 1, 2))
	require.ErrorIs(t, err, errs.ErrInvalidSharedTimestampTable)
}

// TestNumericBlobSet_MaterializeKeepsColumnsAligned pins that when a member
// decodes fewer timestamps or values than its Count (a corrupt but Count-valid
// entry), set materialization trims that member's rows so every later point
// keeps its own timestamp, value and tag.
func TestNumericBlobSet_MaterializeKeepsColumnsAligned(t *testing.T) {
	opts := []NumericEncoderOption{WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeChimp)}
	first := encodeHardeningBlob(t, opts...)
	// Five points claimed for three encoded; still within the timestamp bytes.
	corrupt, err := decodeHardening(t, patchCompactCount(first, 0, 5))
	require.NoError(t, err)

	enc, err := NewNumericEncoder(time.Unix(1_700_003_600, 0).UTC(), opts...)
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricID(1, 2))
	require.NoError(t, enc.AddDataPoint(5000, 50, ""))
	require.NoError(t, enc.AddDataPoint(5010, 51, ""))
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)
	second, err := decodeHardening(t, data)
	require.NoError(t, err)

	set, err := NewNumericBlobSet([]NumericBlob{corrupt, second})
	require.NoError(t, err)

	for name, metric := range map[string]MaterializedNumericMetric{
		"MaterializeMetric": func() MaterializedNumericMetric { m, _ := set.MaterializeMetric(1); return m }(),
	} {
		require.Lenf(t, metric.Values, len(metric.Timestamps), "%s column lengths", name)
		last := len(metric.Timestamps) - 1
		require.Equalf(t, int64(5010), metric.Timestamps[last], "%s last timestamp", name)
		require.Equalf(t, 51.0, metric.Values[last], "%s last value", name)
	}

	mat := set.Materialize()
	n := mat.DataPointCount(1)
	ts, ok := mat.TimestampAt(1, n-1)
	require.True(t, ok)
	val, ok := mat.ValueAt(1, n-1)
	require.True(t, ok)
	require.Equal(t, int64(5010), ts)
	require.Equal(t, 51.0, val)
}

// TestNumericBlob_AllYieldsOnlyCompleteRows pins that the materializing All path
// stops at the shorter of the decoded timestamp and value columns instead of
// yielding zero-filled timestamps for a truncated stream.
func TestNumericBlob_AllYieldsOnlyCompleteRows(t *testing.T) {
	b := NumericBlob{blobBase: blobBase{tsEncType: format.TypeDelta, valEncType: format.TypeALP}}

	tsBytes := []byte{0x0a, 0x80} // first timestamp 10, then a truncated varint
	engine := endian.GetLittleEndianEngine()
	valBytes := []byte{2} // ALP raw scheme: two little-endian float64 values
	valBytes = engine.AppendUint64(valBytes, math.Float64bits(5))
	valBytes = engine.AppendUint64(valBytes, math.Float64bits(7))

	var got []NumericDataPoint
	for _, dp := range b.allDataPointsMaterialized(tsBytes, valBytes, nil, 2) {
		got = append(got, dp)
	}
	require.Equal(t, []NumericDataPoint{{Ts: 10, Val: 5}}, got)
}

// TestNumericDecoder_RejectsPayloadOverlappingIndex pins that a header whose
// payload offsets point into the header or index is rejected.
func TestNumericDecoder_RejectsPayloadOverlappingIndex(t *testing.T) {
	enc, err := NewNumericEncoder(time.Unix(0, 0).UTC(),
		WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw))
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricID(1, 1))
	require.NoError(t, enc.AddDataPoint(1000, 1.5, ""))
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)

	// Drop the real payloads and point the sections into the header itself:
	// every length and count check still holds, only the layout is impossible.
	engine := endian.GetLittleEndianEngine()
	corrupt := append([]byte(nil), data[:section.HeaderSize+section.NumericIndexEntrySize]...)
	engine.PutUint32(corrupt[20:24], 0)
	engine.PutUint32(corrupt[24:28], 8)
	engine.PutUint32(corrupt[28:32], 16)

	_, err = decodeHardening(t, corrupt)
	require.ErrorIs(t, err, errs.ErrInvalidTimestampPayloadOffset)
}

// TestTextDecoder_RejectsDataOverlappingIndex is the text counterpart.
func TestTextDecoder_RejectsDataOverlappingIndex(t *testing.T) {
	enc, err := NewTextEncoder(time.Unix(0, 0).UTC(), WithTextDataCompression(format.CompressionNone), WithoutMetricNames())
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricID(1, 1))
	require.NoError(t, enc.AddDataPoint(0, "v", ""))
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)

	endian.GetLittleEndianEngine().PutUint32(data[20:24], section.HeaderSize) // data offset inside the index
	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	var decodeErr error
	require.NotPanics(t, func() { _, decodeErr = decoder.Decode() })
	require.Error(t, decodeErr)
}
