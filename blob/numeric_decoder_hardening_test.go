package blob

import (
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
