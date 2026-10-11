package blob

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/compress"
	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/pool"
)

// verifyTestValues returns n values that every value codec encodes in more than a few bytes.
func verifyTestValues(n int, seed float64) []float64 {
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = math.Round((seed+math.Sin(float64(i)*0.7)*10+float64(i%7))*100) / 100
	}

	return vals
}

func verifyTestTimestamps(n int) []int64 {
	ts := make([]int64, n)
	for i := range ts {
		ts[i] = 1_700_000_000_000_000 + int64(i)*1_000_000 + int64(i%3)*17
	}

	return ts
}

func encodeVerifyNumeric(t *testing.T, tagged bool, opts ...NumericEncoderOption) []byte {
	t.Helper()

	opts = append(opts, WithTagsEnabled(tagged))
	encoder, err := NewNumericEncoder(time.Unix(1_700_000_000, 0), opts...)
	require.NoError(t, err)

	const points = 100
	ts := verifyTestTimestamps(points)
	for m := range 3 {
		var tags []string
		if tagged {
			tags = make([]string, points)
			for i := range tags {
				tags[i] = fmt.Sprintf("wafer%d", i/10)
			}
		}
		require.NoError(t, encoder.StartMetricName(fmt.Sprintf("metric.%d", m), points))
		require.NoError(t, encoder.AddDataPoints(ts, verifyTestValues(points, float64(m)*3), tags))
		require.NoError(t, encoder.EndMetric())
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	return data
}

func encodeVerifyText(t *testing.T, tagged bool, opts ...TextEncoderOption) []byte {
	t.Helper()

	opts = append(opts, WithTextTagsEnabled(tagged))
	encoder, err := NewTextEncoder(time.Unix(1_700_000_000, 0), opts...)
	require.NoError(t, err)

	const points = 20
	ts := verifyTestTimestamps(points)
	for m := range 3 {
		require.NoError(t, encoder.StartMetricName(fmt.Sprintf("state.%d", m), points))
		for i := range points {
			tag := ""
			if tagged {
				tag = fmt.Sprintf("wafer%d", i/5)
			}
			require.NoError(t, encoder.AddDataPoint(ts[i], fmt.Sprintf("step-%d", i%4), tag))
		}
		require.NoError(t, encoder.EndMetric())
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	return data
}

func TestVerify_ValidNumericBlobs(t *testing.T) {
	tsEncodings := []format.EncodingType{format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked}
	valEncodings := []format.EncodingType{format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE}

	for _, tsEnc := range tsEncodings {
		for _, valEnc := range valEncodings {
			for _, tagged := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/tags=%t", tsEnc, valEnc, tagged)
				t.Run(name, func(t *testing.T) {
					data := encodeVerifyNumeric(t, tagged, WithTimestampEncoding(tsEnc), WithValueEncoding(valEnc))
					require.NoError(t, Verify(data))
				})
			}
		}
	}

	layouts := []struct {
		name string
		opts []NumericEncoderOption
	}{
		{name: "shared timestamps", opts: []NumericEncoderOption{WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked)}},
		{name: "big endian", opts: []NumericEncoderOption{WithBigEndian(), WithBlobLayoutV2()}},
		{name: "metric names", opts: []NumericEncoderOption{WithMetricNames()}},
		{name: "zstd", opts: []NumericEncoderOption{
			WithTimestampCompression(format.CompressionZstd), WithValueCompression(format.CompressionZstd),
		}},
		{name: "s2 and lz4", opts: []NumericEncoderOption{
			WithTimestampCompression(format.CompressionS2), WithValueCompression(format.CompressionLZ4),
		}},
	}
	for _, tt := range layouts {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, Verify(encodeVerifyNumeric(t, true, tt.opts...)))
		})
	}
}

func TestVerify_ValidTextBlobs(t *testing.T) {
	for _, tsEnc := range []format.EncodingType{format.TypeRaw, format.TypeDelta} {
		for _, comp := range []format.CompressionType{format.CompressionNone, format.CompressionZstd} {
			for _, tagged := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/tags=%t", tsEnc, comp, tagged)
				t.Run(name, func(t *testing.T) {
					data := encodeVerifyText(t, tagged, WithTextTimestampEncoding(tsEnc), WithTextDataCompression(comp))
					require.NoError(t, Verify(data))
				})
			}
		}
	}
}

// TestVerify_TruncatedNumericColumn cuts bytes off the end of the last metric's value column.
// A Gorilla or Chimp blob still opens, because the open-time checks only bound each column's length from below,
// but the column no longer decodes all of its points; ALP columns are fully checked when the blob opens.
func TestVerify_TruncatedNumericColumn(t *testing.T) {
	tests := []struct {
		enc   format.EncodingType
		opens bool
	}{
		{enc: format.TypeGorilla, opens: true},
		{enc: format.TypeChimp, opens: true},
		{enc: format.TypeALP},
	}

	for _, tt := range tests {
		t.Run(tt.enc.String(), func(t *testing.T) {
			data := encodeVerifyNumeric(t, false, WithValueEncoding(tt.enc), WithValueCompression(format.CompressionNone))
			require.NoError(t, Verify(data))

			// Without tags the value payload ends the blob; move the empty tag payload with it.
			const cut = 20
			engine := endian.GetLittleEndianEngine()
			truncated := data[:len(data)-cut]
			engine.PutUint32(truncated[28:32], engine.Uint32(truncated[28:32])-cut)

			decoder, err := NewNumericDecoder(truncated)
			require.NoError(t, err)
			_, openErr := decoder.Decode()
			err = Verify(truncated)

			if !tt.opens {
				require.Error(t, openErr)
				require.ErrorIs(t, err, errs.ErrInvalidALPColumn)

				return
			}

			require.NoError(t, openErr, "the truncated column must still pass the open-time checks")
			require.ErrorIs(t, err, errs.ErrInvalidNumOfDataPoints)
			require.ErrorContains(t, err, "value column")
		})
	}
}

func TestVerify_TextRecords(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// resize changes the uncompressed data section's length and records it in the header's DataSize.
	resize := func(data []byte, delta int) []byte {
		out := append([]byte(nil), data...)
		if delta < 0 {
			out = out[:len(out)+delta]
		} else {
			out = append(out, make([]byte, delta)...)
		}
		engine.PutUint32(out[24:28], uint32(int(engine.Uint32(out[24:28]))+delta))

		return out
	}

	data := encodeVerifyText(t, true, WithTextDataCompression(format.CompressionNone))
	require.NoError(t, Verify(data))

	t.Run("truncated records", func(t *testing.T) {
		err := Verify(resize(data, -3))
		require.ErrorIs(t, err, errs.ErrInvalidNumOfDataPoints)
	})

	t.Run("trailing bytes", func(t *testing.T) {
		err := Verify(resize(data, 2))
		require.ErrorIs(t, err, errs.ErrInvalidNumOfDataPoints)
		require.ErrorContains(t, err, "records end at byte")
	})
}

func TestVerify_NotABlob(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not a blob"), make([]byte, 64)} {
		require.ErrorIs(t, Verify(data), errs.ErrInvalidMagicNumber)
	}
}

func TestVerify_DecoderErrorPassesThrough(t *testing.T) {
	data := encodeVerifyNumeric(t, false)
	data[2] = 0xFF // no encoding is 0xF on either axis

	require.ErrorIs(t, Verify(data), errs.ErrInvalidHeaderFlags)
}

// TestVerify_TextZeroCountEntry zeroes a text metric's point count and leaves its records in place.
// The decoder accepts a zero count, but the metric's data is then unaccounted for.
func TestVerify_TextZeroCountEntry(t *testing.T) {
	data := encodeVerifyText(t, false, WithTextDataCompression(format.CompressionNone))
	require.NoError(t, Verify(data))

	engine := endian.GetLittleEndianEngine()
	countAt := int(engine.Uint32(data[16:20])) + 8 // first index entry's Count field
	engine.PutUint16(data[countAt:countAt+2], 0)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	_, err = decoder.Decode()
	require.NoError(t, err, "the decoder accepts a zero count")

	err = Verify(data)
	require.ErrorIs(t, err, errs.ErrInvalidNumOfDataPoints)
	require.ErrorContains(t, err, "records end at byte 0")
}

// TestVerify_TruncatedTimestampColumn removes bytes from the end of the timestamp payload,
// which shortens the last timestamp column.
// With shared timestamps every metric references one column, and the blob no longer opens.
func TestVerify_TruncatedTimestampColumn(t *testing.T) {
	tests := []struct {
		name  string
		opts  []NumericEncoderOption
		opens bool
	}{
		{name: "Delta", opts: []NumericEncoderOption{WithTimestampEncoding(format.TypeDelta)}, opens: true},
		{name: "DeltaPacked", opts: []NumericEncoderOption{WithTimestampEncoding(format.TypeDeltaPacked)}, opens: true},
		{name: "shared DeltaPacked", opts: []NumericEncoderOption{
			WithTimestampEncoding(format.TypeDeltaPacked), WithSharedTimestamps(),
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := encodeVerifyNumeric(t, false, tt.opts...)
			require.NoError(t, Verify(data))

			const cut = 4
			engine := endian.GetLittleEndianEngine()
			valOffset := engine.Uint32(data[24:28])
			truncated := append(append([]byte(nil), data[:valOffset-cut]...), data[valOffset:]...)
			engine.PutUint32(truncated[24:28], valOffset-cut)
			engine.PutUint32(truncated[28:32], engine.Uint32(truncated[28:32])-cut)

			decoder, err := NewNumericDecoder(truncated)
			require.NoError(t, err)
			_, openErr := decoder.Decode()
			err = Verify(truncated)

			if !tt.opens {
				require.Error(t, openErr)
				require.Error(t, err)

				return
			}

			require.NoError(t, openErr, "the truncated column must still pass the open-time checks")
			require.ErrorIs(t, err, errs.ErrInvalidNumOfDataPoints)
			require.ErrorContains(t, err, "timestamp column")
		})
	}
}

// TestVerify_TruncatedTagColumn shortens the last metric's tags inside the zstd-compressed tag payload,
// so the payload still decompresses but its last column ends early.
func TestVerify_TruncatedTagColumn(t *testing.T) {
	data := encodeVerifyNumeric(t, true)
	require.NoError(t, Verify(data))

	codec, err := compress.GetCodec(format.CompressionZstd)
	require.NoError(t, err)

	tagOffset := endian.GetLittleEndianEngine().Uint32(data[28:32])
	plain, err := codec.Decompress(data[tagOffset:])
	require.NoError(t, err)

	const cut = 20
	recompressed, err := codec.Compress(plain[:len(plain)-cut])
	require.NoError(t, err)
	truncated := append(append([]byte(nil), data[:tagOffset]...), recompressed...)

	decoder, err := NewNumericDecoder(truncated)
	require.NoError(t, err)
	_, err = decoder.Decode()
	require.NoError(t, err, "the truncated tag column must still pass the open-time checks")

	err = Verify(truncated)
	require.ErrorIs(t, err, errs.ErrInvalidNumOfDataPoints)
	require.ErrorContains(t, err, "tag column")
}

// TestVerify_LargeALPColumns covers both ALP value paths:
// columns up to the decode pool's cap decode into a pooled buffer, longer ones stream through the codec.
func TestVerify_LargeALPColumns(t *testing.T) {
	for _, valEnc := range []format.EncodingType{format.TypeALP, format.TypeALPRLE} {
		for _, points := range []int{pool.MaxPooledDecodeFloat64s, pool.MaxPooledDecodeFloat64s + 1} {
			t.Run(fmt.Sprintf("%s/%d", valEnc, points), func(t *testing.T) {
				encoder, err := NewNumericEncoder(time.Unix(1_700_000_000, 0), WithValueEncoding(valEnc), WithBlobLayoutV2())
				require.NoError(t, err)
				require.NoError(t, encoder.StartMetricID(1, points))
				require.NoError(t, encoder.AddDataPoints(verifyTestTimestamps(points), verifyTestValues(points, 1), nil))
				require.NoError(t, encoder.EndMetric())
				data, err := encoder.Finish()
				require.NoError(t, err)

				require.NoError(t, Verify(data))
			})
		}
	}
}
