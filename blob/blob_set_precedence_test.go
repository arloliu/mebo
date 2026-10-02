package blob

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/internal/collisiontest"
)

func encodeSetTestNumeric(t *testing.T, start time.Time, name string, vals ...float64) []byte {
	t.Helper()

	enc, err := NewNumericEncoder(start, WithMetricNames(), WithTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricName(name, len(vals)))
	for i, v := range vals {
		require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(i)*1_000_000, v, "n"))
	}
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)

	return data
}

func decodeSetTestNumeric(t *testing.T, data []byte) NumericBlob {
	t.Helper()

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	return blob
}

func buildSetTestText(t *testing.T, start time.Time, name string, vals ...string) TextBlob {
	t.Helper()

	enc, err := NewTextEncoder(start, WithTextTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricName(name, len(vals)))
	for i, v := range vals {
		require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(i)*1_000_000, v, "t"+v))
	}
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)

	decoder, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	return blob
}

// TestBlobSet_NumericPrecedenceForRandomAccess pins that, when a metric exists in
// both numeric and text members, the random-access and duration accessors use
// the numeric members only, like MetricLen, AllTimestamps and AllTags: an index
// past the numeric points is out of range rather than a text point.
func TestBlobSet_NumericPrecedenceForRandomAccess(t *testing.T) {
	const name = "shared.metric"
	base := time.Unix(1_700_000_000, 0).UTC()

	numeric := decodeSetTestNumeric(t, encodeSetTestNumeric(t, base, name, 1, 2))
	id := numeric.MetricIDs()[0]
	text1 := buildSetTestText(t, base, name, "a", "b", "c", "d", "e")
	text2 := buildSetTestText(t, base.Add(time.Hour), name, "f")

	bs := NewBlobSet([]NumericBlob{numeric}, []TextBlob{text1, text2})
	require.Equal(t, 2, bs.MetricLen(id))

	for i := range 7 {
		wantOK := i < 2

		_, ok := bs.TimestampAt(id, i)
		require.Equalf(t, wantOK, ok, "TimestampAt(%d)", i)
		_, ok = bs.TimestampAtByName(name, i)
		require.Equalf(t, wantOK, ok, "TimestampAtByName(%d)", i)
		_, ok = bs.TagAt(id, i)
		require.Equalf(t, wantOK, ok, "TagAt(%d)", i)
		_, ok = bs.TagAtByName(name, i)
		require.Equalf(t, wantOK, ok, "TagAtByName(%d)", i)
	}

	// A single numeric point has zero duration; the text members must not be
	// consulted once the metric is found in a numeric member.
	single := decodeSetTestNumeric(t, encodeSetTestNumeric(t, base, name, 7))
	bs = NewBlobSet([]NumericBlob{single}, []TextBlob{text1})
	require.Equal(t, 1, bs.MetricLen(id))
	require.Equal(t, int64(0), bs.MetricDuration(id))
	require.Equal(t, int64(0), bs.MetricDurationByName(name))
}

// TestNumericBlobSet_ForEachHonorsSetIdentity pins that the ForEach* set methods
// resolve a collided ID and stripped members exactly like All*: a collided ID
// yields only the first colliding name's series, and a stripped member attaches
// to that first name only.
func TestNumericBlobSet_ForEachHonorsSetIdentity(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()

	memberA := decodeSetTestNumeric(t, encodeSetTestNumeric(t, base, collisiontest.NameA, 1, 2))
	memberB := decodeSetTestNumeric(t, encodeSetTestNumeric(t, base.Add(time.Hour), collisiontest.NameB, 70, 71))
	stripped, ok, err := StripMetricNames(nil, encodeSetTestNumeric(t, base.Add(2*time.Hour), collisiontest.NameA, 9))
	require.NoError(t, err)
	require.True(t, ok)
	memberS := decodeSetTestNumeric(t, stripped)

	set, err := NewNumericBlobSet([]NumericBlob{memberA, memberB, memberS})
	require.NoError(t, err)

	values := func(seq func(func(float64) bool)) []float64 {
		var out []float64
		for v := range seq {
			out = append(out, v)
		}

		return out
	}
	viaForEach := func(run func(func(int, float64) bool) bool) []float64 {
		var out []float64
		run(func(_ int, v float64) bool { out = append(out, v); return true })

		return out
	}

	id := collisiontest.CollisionID
	want := values(set.AllValues(id))
	require.Equal(t, []float64{1, 2, 9}, want)

	require.Equal(t, want, viaForEach(func(y func(int, float64) bool) bool {
		return set.ForEachValues(id, y)
	}), "ForEachValues")
	require.Equal(t, want, viaForEach(func(y func(int, float64) bool) bool {
		return set.ForEach(id, func(i int, dp NumericDataPoint) bool { return y(i, dp.Val) })
	}), "ForEach")

	var tsCount int
	set.ForEachTimestamps(id, func(int, int64) bool { tsCount++; return true })
	require.Equal(t, set.MetricLen(id), tsCount, "ForEachTimestamps")

	for _, name := range []string{collisiontest.NameA, collisiontest.NameB} {
		metric, found := set.MaterializeMetricByName(name)
		require.Truef(t, found, "MaterializeMetricByName(%s)", name)
		wantByName := metric.Values
		require.Equalf(t, wantByName, viaForEach(func(y func(int, float64) bool) bool {
			return set.ForEachValuesByName(name, y)
		}), "ForEachValuesByName(%s)", name)
		require.Equalf(t, wantByName, viaForEach(func(y func(int, float64) bool) bool {
			return set.ForEachByName(name, func(i int, dp NumericDataPoint) bool { return y(i, dp.Val) })
		}), "ForEachByName(%s)", name)

		var n int
		set.ForEachTimestampsByName(name, func(int, int64) bool { n++; return true })
		require.Equalf(t, set.MetricLenByName(name), n, "ForEachTimestampsByName(%s)", name)
	}
}
