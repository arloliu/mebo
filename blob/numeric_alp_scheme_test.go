package blob

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/section"
)

// TestNumericDecoder_ALPUnknownScheme_Errors proves that a corrupted ALP
// column — one whose payload starts with an unknown scheme byte (>= 3) —
// is caught once at blob open (Decode()) and surfaced as a descriptive
// error, instead of silently decoding as an empty/zero column through every
// read path (see numeric_alp_wiring_test.go's DispatchParity test for the
// happy-path mirror of those same four paths).
func TestNumericDecoder_ALPUnknownScheme_Errors(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeALP),
		// CompressionNone keeps the on-disk value payload byte-identical to
		// what the ALP decoders read, so the scheme byte lives at a
		// predictable offset for this test to corrupt.
		WithValueCompression(format.CompressionNone))
	require.NoError(t, err)

	values := []float64{1.5, 2.25, 3.125, 4.0, 5.75}
	require.NoError(t, encoder.StartMetricID(42, len(values)))
	for i, v := range values {
		ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, v, ""))
	}
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Locate the value payload: with a single metric and no compression, the
	// first byte of the value payload is that metric's ALP scheme byte
	// (ValueOffset 0 for the first column).
	header, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	schemeOff := int(header.ValuePayloadOffset)
	require.Lessf(t, data[schemeOff], byte(3), "sanity: encoder must emit a known scheme byte (0, 1, or 2)")

	corrupted := append([]byte(nil), data...)
	corrupted[schemeOff] = 3 // first unknown scheme byte

	decoder, err := NewNumericDecoder(corrupted)
	require.NoError(t, err, "header/offsets are still well-formed; only the ALP payload is corrupted")

	_, err = decoder.Decode()
	require.Errorf(t, err, "unknown ALP scheme byte must be reported as an error, not silently decoded")
	require.ErrorIsf(t, err, errs.ErrInvalidALPScheme, "got %v", err)
}

// TestNumericDecoder_ALPUnknownScheme_MultiMetric proves the validation
// catches a corrupted non-first column too (not just column 0), and that a
// valid blob with multiple ALP columns still decodes cleanly.
func TestNumericDecoder_ALPUnknownScheme_MultiMetric(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeALP),
		WithValueCompression(format.CompressionNone))
	require.NoError(t, err)

	metrics := []struct {
		id     uint64
		values []float64
	}{
		{id: 1, values: []float64{1.5, 2.5, 3.5}},
		{id: 2, values: []float64{10.25, 20.75}},
	}

	for _, m := range metrics {
		require.NoError(t, encoder.StartMetricID(m.id, len(m.values)))
		for i, v := range m.values {
			ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
			require.NoError(t, encoder.AddDataPoint(ts, v, ""))
		}
		require.NoError(t, encoder.EndMetric())
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Valid blob: both ALP columns decode without error.
	decoder, err := NewNumericDecoder(append([]byte(nil), data...))
	require.NoError(t, err)
	_, err = decoder.Decode()
	require.NoError(t, err, "unmodified blob with valid ALP columns must decode cleanly")

	// Corrupt the SECOND metric's scheme byte. First column's ValueOffset is
	// 0; the second column starts wherever the first column's encoded bytes
	// end, so parse the index to find it precisely.
	header, err := section.ParseNumericHeader(data)
	require.NoError(t, err)

	metricCount := int(header.MetricCount)
	require.Equal(t, 2, metricCount)

	entrySize := header.Flag.IndexEntrySize()
	indexOffset := section.HeaderSize
	engine := header.Flag.GetEndianEngine()
	entry2, err := section.ParseNumericIndexEntry(
		data[indexOffset+entrySize:indexOffset+2*entrySize], engine)
	require.NoError(t, err)

	// entry2.ValueOffset is a delta from the first entry's (absolute) offset,
	// which is always 0, so it's already absolute here.
	secondColOff := int(header.ValuePayloadOffset) + entry2.ValueOffset
	require.Lessf(t, data[secondColOff], byte(3), "sanity: second column's scheme byte must be known")

	corrupted := append([]byte(nil), data...)
	corrupted[secondColOff] = 200 // arbitrary unknown scheme byte

	decoder2, err := NewNumericDecoder(corrupted)
	require.NoError(t, err)
	_, err = decoder2.Decode()
	require.Errorf(t, err, "corrupting the second column's scheme byte must also be caught")
	require.ErrorIsf(t, err, errs.ErrInvalidALPScheme, "got %v", err)
}

// TestNumericDecoder_ALPCorruptNExc_Errors proves that an ALP main column
// whose scheme byte is intact but whose 4-byte nExc header field has been
// corrupted to a huge value is also caught once at blob open (Decode()),
// instead of panicking deep inside decodeMainInto's exception-patch loop
// (out-of-range slicing of the exception sidecar, sized by the lying nExc).
// This is the structural-corruption counterpart to
// TestNumericDecoder_ALPUnknownScheme_Errors above, which only covers the
// leading scheme byte.
func TestNumericDecoder_ALPCorruptNExc_Errors(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoder, err := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeALP),
		// CompressionNone keeps the on-disk value payload byte-identical to
		// what the ALP decoders read, so the column's byte layout lives at a
		// predictable offset for this test to corrupt.
		WithValueCompression(format.CompressionNone))
	require.NoError(t, err)

	// 2-decimal-place values pick the ALP main scheme (decimal decomposition
	// beats both ALP-RD and raw for this kind of data).
	values := []float64{12.34, 56.78, 90.12, 34.56, 78.90, 11.11, 22.22, 33.33}
	require.NoError(t, encoder.StartMetricID(42, len(values)))
	for i, v := range values {
		ts := startTime.Add(time.Duration(i) * time.Second).UnixMicro()
		require.NoError(t, encoder.AddDataPoint(ts, v, ""))
	}
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)

	// Unmodified blob still decodes cleanly.
	decoder, err := NewNumericDecoder(append([]byte(nil), data...))
	require.NoError(t, err)
	_, err = decoder.Decode()
	require.NoError(t, err, "unmodified blob with a valid ALP main column must decode cleanly")

	header, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	schemeOff := int(header.ValuePayloadOffset)
	require.Equalf(t, byte(0), data[schemeOff], "sanity: encoder must pick ALP main (scheme 0) for 2dp decimal data")

	// Main column layout: [scheme:1][e:1][f:1][width:1][nExc:4][min:8]...
	// nExc lives at schemeOff+4 .. schemeOff+8 (4-byte, engine-endian).
	corrupted := append([]byte(nil), data...)
	for i := schemeOff + 4; i < schemeOff+8; i++ {
		corrupted[i] = 0xFF
	}

	decoder2, err := NewNumericDecoder(corrupted)
	require.NoError(t, err, "header/offsets are still well-formed; only the ALP column body is corrupted")

	_, err = decoder2.Decode()
	require.Errorf(t, err, "a corrupt nExc field must be reported as an error, not panic")
	require.ErrorIsf(t, err, errs.ErrInvalidALPColumn, "got %v", err)
}

// alpRLESchemeTestBlob encodes one hold-50% metric as uncompressed ALP-RLE
// and returns the blob and the offset of its column, which must use the runs layout.
func alpRLESchemeTestBlob(t *testing.T) ([]byte, int) {
	t.Helper()
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	encoder, err := NewNumericEncoder(start,
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeALPRLE),
		WithValueCompression(format.CompressionNone))
	require.NoError(t, err)

	values := alpRLETestMetrics()[0].values
	require.NoError(t, encoder.StartMetricID(42, len(values)))
	for i, v := range values {
		require.NoError(t, encoder.AddDataPoint(start.Add(time.Duration(i)*time.Second).UnixMicro(), v, ""))
	}
	require.NoError(t, encoder.EndMetric())
	data, err := encoder.Finish()
	require.NoError(t, err)

	header, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	off := int(header.ValuePayloadOffset)
	require.Equal(t, byte(3), data[off], "sanity: the column must use the runs layout")

	decoder, err := NewNumericDecoder(append([]byte(nil), data...))
	require.NoError(t, err)
	_, err = decoder.Decode()
	require.NoError(t, err, "unmodified ALP-RLE blob must decode cleanly")

	return data, off
}

// TestNumericDecoder_ALPRLEScheme_Errors pins the open-time scheme gate for both ALP encoding types:
// a runs column is rejected under TypeALP, an unknown scheme byte is rejected under TypeALPRLE,
// and a corrupt runs column or nested column is reported instead of panicking.
func TestNumericDecoder_ALPRLEScheme_Errors(t *testing.T) {
	data, off := alpRLESchemeTestBlob(t)
	decode := func(blob []byte) error {
		decoder, err := NewNumericDecoder(blob)
		require.NoError(t, err, "header and offsets stay well-formed")
		_, err = decoder.Decode()

		return err
	}

	t.Run("runs column under TypeALP", func(t *testing.T) {
		header, err := section.ParseNumericHeader(data)
		require.NoError(t, err)
		header.Flag.SetValueEncoding(format.TypeALP)
		corrupted := append([]byte(nil), data...)
		copy(corrupted, header.Bytes())
		require.ErrorIs(t, decode(corrupted), errs.ErrInvalidALPScheme)
	})

	t.Run("scheme 4 under TypeALPRLE", func(t *testing.T) {
		corrupted := append([]byte(nil), data...)
		corrupted[off] = 4
		require.ErrorIs(t, decode(corrupted), errs.ErrInvalidALPScheme)
	})

	t.Run("bitmap without a run at point 0", func(t *testing.T) {
		corrupted := append([]byte(nil), data...)
		corrupted[off+5] &^= 1
		require.ErrorIs(t, decode(corrupted), errs.ErrInvalidALPColumn)
	})

	t.Run("nested exception count beyond the column", func(t *testing.T) {
		// [3][nRuns:4][bitmap: 150 points = 19 bytes][nested scheme][e][f][width][nExc:4]...
		nestedOff := off + 5 + 19
		require.Equal(t, byte(0), data[nestedOff], "sanity: the nested column must be ALP main")
		corrupted := append([]byte(nil), data...)
		for i := nestedOff + 4; i < nestedOff+8; i++ {
			corrupted[i] = 0xFF
		}
		require.ErrorIs(t, decode(corrupted), errs.ErrInvalidALPColumn)
	})
}

// TestNumericDecoder_ALPRLECorruptPayload_NoPanic mutates random value-payload bytes of an ALP-RLE blob
// that holds runs columns of every nested scheme.
// Each mutation must either fail at open or decode through every read path without panicking:
// open-time validation is the only guard in front of the trusted-column decoders.
func TestNumericDecoder_ALPRLECorruptPayload_NoPanic(t *testing.T) {
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	encoder, err := NewNumericEncoder(start,
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeALPRLE),
		WithValueCompression(format.CompressionNone))
	require.NoError(t, err)
	metrics := alpRLETestMetrics()
	for _, m := range metrics {
		require.NoError(t, encoder.StartMetricID(m.id, len(m.values)))
		for i, v := range m.values {
			require.NoError(t, encoder.AddDataPoint(start.Add(time.Duration(i)*time.Second).UnixMicro(), v, ""))
		}
		require.NoError(t, encoder.EndMetric())
	}
	data, err := encoder.Finish()
	require.NoError(t, err)
	header, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	valStart, valEnd := int(header.ValuePayloadOffset), int(header.TagPayloadOffset)
	if valEnd <= valStart {
		valEnd = len(data)
	}

	var sink float64
	traverse := func(blob NumericBlob) {
		for _, m := range metrics {
			for v := range blob.AllValues(m.id) {
				sink += v
			}
			for _, dp := range blob.All(m.id) {
				sink += dp.Val
			}
			for i := range len(m.values) + 1 {
				_, _ = blob.ValueAt(m.id, i)
			}
			blob.ForEach(m.id, func(int, NumericDataPoint) bool { return true })
			blob.ForEachValues(m.id, func(int, float64) bool { return true })
			_, _ = blob.MaterializeMetric(m.id)
		}
		_ = blob.Materialize()
	}

	rng := rand.New(rand.NewSource(7))
	iterations := 3000
	if testing.Short() {
		iterations = 300
	}
	opened := 0
	for it := range iterations {
		corrupted := append([]byte(nil), data...)
		for range 1 + it%4 {
			pos := valStart + rng.Intn(valEnd-valStart)
			corrupted[pos] ^= byte(1 + rng.Intn(255))
		}
		decoder, err := NewNumericDecoder(corrupted)
		if err != nil {
			continue
		}
		blob, err := decoder.Decode()
		if err != nil {
			continue
		}
		opened++
		traverse(blob)
	}
	t.Logf("%d of %d corrupted blobs passed open-time validation and were traversed (checksum %g)", opened, iterations, sink)
}
