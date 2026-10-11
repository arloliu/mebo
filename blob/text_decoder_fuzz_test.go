package blob

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/section"
)

// textFuzzSeeds returns valid text blobs covering both timestamp encodings,
// every data compression, tags, both byte orders and blobs without names.
func textFuzzSeeds(tb testing.TB) [][]byte {
	tb.Helper()

	compressions := []format.CompressionType{
		format.CompressionNone, format.CompressionZstd, format.CompressionS2, format.CompressionLZ4,
	}

	var optSets [][]TextEncoderOption
	for _, tsEnc := range []format.EncodingType{format.TypeRaw, format.TypeDelta} {
		for _, comp := range compressions {
			base := []TextEncoderOption{WithTextTimestampEncoding(tsEnc), WithTextDataCompression(comp)}
			optSets = append(optSets,
				base,
				append(append([]TextEncoderOption(nil), base...), WithTextTagsEnabled(true), WithTextBigEndian()),
				append(append([]TextEncoderOption(nil), base...), WithoutMetricNames()),
			)
		}
	}

	seeds := make([][]byte, 0, len(optSets))
	for _, opts := range optSets {
		enc, err := NewTextEncoder(fuzzSeedStart, opts...)
		require.NoError(tb, err)

		for m := range 3 {
			points := 20 + m*5
			require.NoError(tb, enc.StartMetricName(fmt.Sprintf("fuzz.text.%d", m), points))
			for i := range points {
				ts := fuzzSeedStart.Add(time.Duration(i*10+i%3) * time.Second).UnixMicro()
				require.NoError(tb, enc.AddDataPoint(ts, fmt.Sprintf("value-%d", i%4), fmt.Sprintf("t%d", i%2)))
			}
			require.NoError(tb, enc.EndMetric())
		}

		data, err := enc.Finish()
		require.NoError(tb, err)
		seeds = append(seeds, data)
	}

	return seeds
}

// FuzzTextDecoder decodes arbitrary bytes as a text blob and drives every
// read path over the result, with the same no-panic, bounds and
// decoder-agreement assertions as FuzzNumericDecoder.
func FuzzTextDecoder(f *testing.F) {
	for _, seed := range textFuzzSeeds(f) {
		f.Add(seed)
	}
	f.Add([]byte{})
	f.Add(make([]byte, section.HeaderSize))

	f.Fuzz(func(t *testing.T, data []byte) {
		copied, copiedErr := decodeTextFuzz(NewTextDecoder, data)
		borrowed, borrowedErr := decodeTextFuzz(NewTextDecoderBorrowed, data)
		require.Equal(t, copiedErr == nil, borrowedErr == nil, "copying and borrowing decoders disagree")

		if copiedErr == nil {
			require.Equal(t, copied.MetricIDs(), borrowed.MetricIDs())
			require.Equal(t, copied.MetricNames(), borrowed.MetricNames())
			exerciseTextFuzz(t, copied)
			exerciseTextFuzz(t, borrowed)
		}

		// Verify opens the blob itself, so it must reject whatever the decoder rejects.
		if verifyErr := Verify(data); copiedErr != nil && section.IsTextBlob(data) {
			require.Error(t, verifyErr, "Verify accepted a blob the decoder rejects")
		}

		if guarded, ok := fuzzGuardedCopy(t, data); ok {
			if blob, err := decodeTextFuzz(NewTextDecoderBorrowed, guarded); err == nil {
				exerciseTextFuzz(t, blob)
			}
			_ = Verify(guarded)
		}

		if set, err := DecodeBlobSet(data, data); err == nil {
			exerciseBlobSetFuzz(t, set)
		}
	})
}

// decodeTextFuzz runs one decoder constructor over data.
func decodeTextFuzz(newDecoder func([]byte) (*TextDecoder, error), data []byte) (TextBlob, error) {
	dec, err := newDecoder(data)
	if err != nil {
		return TextBlob{}, err
	}

	return dec.Decode()
}

// exerciseTextFuzz reads every metric of blob through each access path.
func exerciseTextFuzz(t *testing.T, blob TextBlob) {
	t.Helper()

	for _, id := range blob.MetricIDs() {
		n := blob.Len(id)
		requireFuzzIterationBounded(t, n, countSeq2(blob.All(id)))
		requireFuzzIterationBounded(t, n, countSeq(blob.AllTimestamps(id)))
		requireFuzzIterationBounded(t, n, countSeq(blob.AllValues(id)))
		requireFuzzIterationBounded(t, n, countSeq(blob.AllTags(id)))

		for _, i := range []int{0, n / 2, n - 1} {
			blob.ValueAt(id, i)
			blob.TimestampAt(id, i)
			blob.TagAt(id, i)
		}
		for _, i := range []int{-1, n} {
			_, ok := blob.ValueAt(id, i)
			require.False(t, ok, "ValueAt(%d) outside length %d", i, n)
			_, ok = blob.TimestampAt(id, i)
			require.False(t, ok, "TimestampAt(%d) outside length %d", i, n)
			_, ok = blob.TagAt(id, i)
			require.False(t, ok, "TagAt(%d) outside length %d", i, n)
		}

		blob.MaterializeMetric(id)
	}

	for _, name := range blob.MetricNames() {
		countSeq2(blob.AllByName(name))
	}

	blob.Materialize()
}
