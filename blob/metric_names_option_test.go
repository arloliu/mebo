package blob

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/section"
)

// ==============================================================================
// WithMetricNames() forces the names payload on (numeric).
// ==============================================================================

// numericMetricSpec describes one metric to add to a numeric encoder in a test.
type numericMetricSpec struct {
	name   string
	points int
	value  float64
	tag    string
}

// buildNumeric encodes the given metrics (in the given order, by name) with opts,
// returning the finished blob.
func buildNumeric(t *testing.T, opts []NumericEncoderOption, metrics []numericMetricSpec) []byte {
	t.Helper()
	start := time.Now()
	enc, err := NewNumericEncoder(start, opts...)
	require.NoError(t, err)

	for _, m := range metrics {
		require.NoError(t, enc.StartMetricName(m.name, m.points))
		for i := range m.points {
			ts := start.UnixMicro() + int64(i)*1_000_000
			require.NoError(t, enc.AddDataPoint(ts, m.value+float64(i), m.tag))
		}
		require.NoError(t, enc.EndMetric())
	}

	data, err := enc.Finish()
	require.NoError(t, err)

	return data
}

// TestWithMetricNames_V1 covers option on/off, sorted/unsorted insertion, and
// tags on/off for the default (V1) layout.
func TestWithMetricNames_V1(t *testing.T) {
	sorted := []numericMetricSpec{
		{name: "aaa.metric", points: 2, value: 1.0},
		{name: "bbb.metric", points: 3, value: 2.0},
		{name: "ccc.metric", points: 1, value: 3.0},
	}
	unsorted := []numericMetricSpec{
		{name: "zzz.metric", points: 2, value: 1.0},
		{name: "aaa.metric", points: 3, value: 2.0},
		{name: "mmm.metric", points: 1, value: 3.0},
	}

	for _, tagsEnabled := range []bool{false, true} {
		for orderName, metrics := range map[string][]numericMetricSpec{"sorted": sorted, "unsorted": unsorted} {
			t.Run(fmt.Sprintf("tags=%v/%s", tagsEnabled, orderName), func(t *testing.T) {
				specs := metrics
				if tagsEnabled {
					specs = append([]numericMetricSpec(nil), metrics...)
					for i := range specs {
						specs[i].tag = "host=server1"
					}
				}

				baseOpts := []NumericEncoderOption{WithTagsEnabled(tagsEnabled)}
				withOpts := append(append([]NumericEncoderOption(nil), baseOpts...), WithMetricNames())

				without := buildNumeric(t, baseOpts, specs)
				with := buildNumeric(t, withOpts, specs)

				decWithout, err := NewNumericDecoder(without)
				require.NoError(t, err)
				bWithout, err := decWithout.Decode()
				require.NoError(t, err)
				require.False(t, bWithout.HasMetricNames(), "no collision, no option -> no names payload")

				decWith, err := NewNumericDecoder(with)
				require.NoError(t, err)
				bWith, err := decWith.Decode()
				require.NoError(t, err)
				require.True(t, bWith.HasMetricNames(), "WithMetricNames() must force the names payload on")
				for _, m := range specs {
					require.True(t, bWith.HasMetricName(m.name), "name %q must be retrievable", m.name)
				}

				// Size delta: the names-bearing blob must be larger by
				// exactly the encoded names payload size.
				names := make([]string, len(specs))
				for i, m := range specs {
					names[i] = m.name
				}
				namesPayload, err := ienc.EncodeMetricNames(names, decWith.header.Flag.GetEndianEngine())
				require.NoError(t, err)
				require.Equal(t, len(without)+len(namesPayload), len(with),
					"blob size delta must equal exactly the encoded names payload size")
			})
		}
	}
}

// TestWithMetricNames_V2 covers the V2 layout with out-of-order insertion,
// which forces the encoder's internal MetricID sort.
func TestWithMetricNames_V2(t *testing.T) {
	metrics := []numericMetricSpec{
		{name: "zzz.metric", points: 2, value: 1.0},
		{name: "aaa.metric", points: 3, value: 2.0},
		{name: "mmm.metric", points: 1, value: 3.0},
	}

	without := buildNumeric(t, []NumericEncoderOption{WithBlobLayoutV2()}, metrics)
	with := buildNumeric(t, []NumericEncoderOption{WithBlobLayoutV2(), WithMetricNames()}, metrics)

	decWithout, err := NewNumericDecoder(without)
	require.NoError(t, err)
	bWithout, err := decWithout.Decode()
	require.NoError(t, err)
	require.False(t, bWithout.HasMetricNames())

	decWith, err := NewNumericDecoder(with)
	require.NoError(t, err)
	bWith, err := decWith.Decode()
	require.NoError(t, err)
	require.True(t, bWith.HasMetricNames())
	require.Less(t, len(without), len(with), "names-bearing blob must be larger")

	// Every name resolves to its own payload after the MetricID sort permutation.
	for i, m := range metrics {
		var vals []float64
		for _, dp := range bWith.AllByName(m.name) {
			vals = append(vals, dp.Val)
		}
		require.Len(t, vals, m.points)
		require.Equal(t, m.value, vals[0], "metric %d (%q) must resolve to its own payload", i, m.name)
	}
}

// TestWithMetricNames_V2Ext forces the extended (32-byte) V2 index format via
// a large offset delta, and verifies the option still works under that format.
func TestWithMetricNames_V2Ext(t *testing.T) {
	// 8192 raw float64 points = 65536 bytes > NumericMaxOffset (65535), forcing V2Ext.
	metrics := []numericMetricSpec{
		{name: "small.metric", points: 5, value: 1.0},
		{name: "large.metric", points: 8192, value: 2.0},
	}
	opts := []NumericEncoderOption{
		WithBlobLayoutV2(),
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeRaw),
	}

	without := buildNumeric(t, opts, metrics)
	with := buildNumeric(t, append(append([]NumericEncoderOption(nil), opts...), WithMetricNames()), metrics)

	hdr, err := section.ParseNumericHeader(without)
	require.NoError(t, err)
	require.True(t, hdr.Flag.IsV2Ext(), "test fixture must actually trigger V2Ext")

	decWithout, err := NewNumericDecoder(without)
	require.NoError(t, err)
	bWithout, err := decWithout.Decode()
	require.NoError(t, err)
	require.False(t, bWithout.HasMetricNames())

	decWith, err := NewNumericDecoder(with)
	require.NoError(t, err)
	bWith, err := decWith.Decode()
	require.NoError(t, err)
	require.True(t, bWith.HasMetricNames())
	require.True(t, bWith.HasMetricName("small.metric"))
	require.True(t, bWith.HasMetricName("large.metric"))
}

// TestWithMetricNames_SharedTimestamps covers WithSharedTimestamps() combined
// with WithMetricNames().
func TestWithMetricNames_SharedTimestamps(t *testing.T) {
	start := time.Now()
	build := func(t *testing.T, opts ...NumericEncoderOption) []byte {
		t.Helper()
		enc, err := NewNumericEncoder(start, opts...)
		require.NoError(t, err)

		names := []string{"metric.a", "metric.b", "metric.c"}
		for i, nm := range names {
			require.NoError(t, enc.StartMetricName(nm, 2))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro(), float64(i), ""))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro()+1_000_000, float64(i)+0.5, ""))
			require.NoError(t, enc.EndMetric())
		}
		data, err := enc.Finish()
		require.NoError(t, err)

		return data
	}

	without := build(t, WithSharedTimestamps())
	with := build(t, WithSharedTimestamps(), WithMetricNames())

	decWithout, err := NewNumericDecoder(without)
	require.NoError(t, err)
	bWithout, err := decWithout.Decode()
	require.NoError(t, err)
	require.False(t, bWithout.HasMetricNames())

	decWith, err := NewNumericDecoder(with)
	require.NoError(t, err)
	bWith, err := decWith.Decode()
	require.NoError(t, err)
	require.True(t, bWith.HasMetricNames())
	require.True(t, bWith.HasMetricName("metric.a"))
	require.True(t, bWith.HasMetricName("metric.b"))
	require.True(t, bWith.HasMetricName("metric.c"))
}

// TestWithMetricNames_CollisionStillWorks verifies a real collision still
// forces names on even when WithMetricNames() was NOT set — the option and the
// unconditional collision path are independent.
func TestWithMetricNames_CollisionStillWorks(t *testing.T) {
	data := encodeCollisionNumeric(t) // no WithMetricNames()
	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)
	require.True(t, b.HasMetricNames())
}

// ==============================================================================
// StartMetricID fails fast with ErrMetricNamesUnavailable.
// ==============================================================================

func TestStartMetricID_RejectedWithMetricNames(t *testing.T) {
	t.Run("fresh encoder", func(t *testing.T) {
		enc, err := NewNumericEncoder(time.Now(), WithMetricNames())
		require.NoError(t, err)

		err = enc.StartMetricID(42, 1)
		require.ErrorIs(t, err, errs.ErrMetricNamesUnavailable)
	})

	t.Run("without the option, ID mode works normally", func(t *testing.T) {
		enc, err := NewNumericEncoder(time.Now())
		require.NoError(t, err)

		require.NoError(t, enc.StartMetricID(42, 1))
	})
}

// TestStartMetricID_ErrorPrecedence asserts ErrMetricNamesUnavailable is
// checked BEFORE any other StartMetricID validation, mode lock, or tracker
// mutation — it must win over checks that would otherwise fire first.
func TestStartMetricID_ErrorPrecedence(t *testing.T) {
	t.Run("wins over ErrInvalidMetricID", func(t *testing.T) {
		enc, err := NewNumericEncoder(time.Now(), WithMetricNames())
		require.NoError(t, err)

		// metricID == 0 would normally be ErrInvalidMetricID.
		err = enc.StartMetricID(0, 1)
		require.ErrorIs(t, err, errs.ErrMetricNamesUnavailable)
		require.NotErrorIs(t, err, errs.ErrInvalidMetricID)
	})

	t.Run("wins over ErrInvalidNumOfDataPoints", func(t *testing.T) {
		enc, err := NewNumericEncoder(time.Now(), WithMetricNames())
		require.NoError(t, err)

		err = enc.StartMetricID(42, -1)
		require.ErrorIs(t, err, errs.ErrMetricNamesUnavailable)
	})

	t.Run("wins over ErrMixedIdentifierMode after Name mode is locked", func(t *testing.T) {
		start := time.Now()
		enc, err := NewNumericEncoder(start, WithMetricNames())
		require.NoError(t, err)

		// Successfully use Name mode first (WithMetricNames is compatible with it).
		require.NoError(t, enc.StartMetricName("metric.one", 1))
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1.0, ""))
		require.NoError(t, enc.EndMetric())

		// Now try StartMetricID: without the option this would be
		// ErrMixedIdentifierMode; with the option, ErrMetricNamesUnavailable
		// takes precedence.
		err = enc.StartMetricID(99, 1)
		require.ErrorIs(t, err, errs.ErrMetricNamesUnavailable)

		// Confirm this really is a precedence override, not a coincidence:
		// the same sequence WITHOUT the option produces ErrMixedIdentifierMode.
		enc2, err := NewNumericEncoder(start)
		require.NoError(t, err)
		require.NoError(t, enc2.StartMetricName("metric.one", 1))
		require.NoError(t, enc2.AddDataPoint(start.UnixMicro(), 1.0, ""))
		require.NoError(t, enc2.EndMetric())
		err = enc2.StartMetricID(99, 1)
		require.ErrorIs(t, err, errs.ErrMixedIdentifierMode)
	})

	t.Run("leaves encoder state unchanged on rejection", func(t *testing.T) {
		start := time.Now()
		enc, err := NewNumericEncoder(start, WithMetricNames())
		require.NoError(t, err)
		require.NoError(t, enc.StartMetricName("metric.one", 1))
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1.0, ""))
		require.NoError(t, enc.EndMetric())

		countBefore := enc.MetricCount()
		err = enc.StartMetricID(99, 1)
		require.ErrorIs(t, err, errs.ErrMetricNamesUnavailable)
		require.Equal(t, countBefore, enc.MetricCount(), "rejection must not mutate index entries")
		require.Equal(t, uint64(0), enc.curMetricID, "rejection must not set curMetricID")

		// Encoder remains usable in Name mode afterwards.
		require.NoError(t, enc.StartMetricName("metric.two", 1))
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 2.0, ""))
		require.NoError(t, enc.EndMetric())
		data, err := enc.Finish()
		require.NoError(t, err)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)
		require.Equal(t, 2, b.MetricCount())
	})
}

// ==============================================================================
// The collision tracker's seen-name set rejects a repeated name, exercised
// under WithMetricNames() (no collision).
// ==============================================================================

func TestWithMetricNames_DuplicateNameRejected(t *testing.T) {
	start := time.Now()
	enc, err := NewNumericEncoder(start, WithMetricNames())
	require.NoError(t, err)

	require.NoError(t, enc.StartMetricName("metric.one", 1))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1.0, ""))
	require.NoError(t, enc.EndMetric())

	// Repeating the same name with no collision involved must still be
	// rejected by the seen-name set inside the tracker's Probe.
	err = enc.StartMetricName("metric.one", 1)
	require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)
}

// ==============================================================================
// TextEncoderConfig.omitMetricNames / WithoutMetricNames().
// ==============================================================================

func buildText(t *testing.T, opts []TextEncoderOption, names []string) []byte {
	t.Helper()
	start := time.Now()
	enc, err := NewTextEncoder(start, opts...)
	require.NoError(t, err)

	for i, nm := range names {
		require.NoError(t, enc.StartMetricName(nm, 1))
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), fmt.Sprintf("v%d", i), ""))
		require.NoError(t, enc.EndMetric())
	}

	data, err := enc.Finish()
	require.NoError(t, err)

	return data
}

func TestTextWithoutMetricNames_DefaultStoresNames(t *testing.T) {
	data := buildText(t, nil, []string{"metric.a", "metric.b"})
	dec, err := NewTextDecoder(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)
	require.True(t, b.HasMetricNames(), "text defaults to storing names")
	require.True(t, b.HasMetricName("metric.a"))
	require.True(t, b.HasMetricName("metric.b"))
}

func TestTextWithoutMetricNames_OptsOut(t *testing.T) {
	names := []string{"metric.a", "metric.b", "metric.c"}
	with := buildText(t, nil, names)
	without := buildText(t, []TextEncoderOption{WithoutMetricNames()}, names)

	decWith, err := NewTextDecoder(with)
	require.NoError(t, err)
	bWith, err := decWith.Decode()
	require.NoError(t, err)
	require.True(t, bWith.HasMetricNames())

	decWithout, err := NewTextDecoder(without)
	require.NoError(t, err)
	bWithout, err := decWithout.Decode()
	require.NoError(t, err)
	require.False(t, bWithout.HasMetricNames())

	// Size delta: opted-out blob is smaller by exactly the names payload size.
	namesPayload, err := ienc.EncodeMetricNames(names, decWith.header.GetEndianEngine())
	require.NoError(t, err)
	require.Equal(t, len(without)+len(namesPayload), len(with),
		"blob size delta must equal exactly the encoded names payload size")
}

func TestTextWithoutMetricNames_CollisionForcesNamesOn(t *testing.T) {
	start := time.Now()
	enc, err := NewTextEncoder(start, WithoutMetricNames())
	require.NoError(t, err)

	require.NoError(t, enc.StartMetricName(cnA, 1))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro(), "a1", ""))
	require.NoError(t, enc.EndMetric())
	require.NoError(t, enc.StartMetricName(cnB, 1)) // collision with A
	require.NoError(t, enc.AddDataPoint(start.UnixMicro(), "b1", ""))
	require.NoError(t, enc.EndMetric())

	data, err := enc.Finish()
	require.NoError(t, err)

	dec, err := NewTextDecoder(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)
	require.True(t, b.HasMetricNames(), "a collision must force names on despite WithoutMetricNames()")
	require.True(t, b.HasMetricName(cnA))
	require.True(t, b.HasMetricName(cnB))
}

// TestTextWithoutMetricNames_Boundary mirrors TestMetricCountBoundary for
// the text encoder: with WithoutMetricNames(), the encoder reaches MaxMetricCount
// (no names required); with the default (names always required), it caps at
// MaxMetricNamesCount and rejects the 65536th entry atomically.
func TestTextWithoutMetricNames_Boundary(t *testing.T) {
	start := time.Now()

	t.Run("WithoutMetricNames reaches MaxMetricCount", func(t *testing.T) {
		enc, err := NewTextEncoder(start, WithoutMetricNames())
		require.NoError(t, err)
		for i := 0; i < MaxMetricCount; i++ {
			require.NoError(t, enc.StartMetricName(fmt.Sprintf("metric.%d", i), 1))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro(), "v", ""))
			require.NoError(t, enc.EndMetric())
		}
		require.Equal(t, MaxMetricCount, enc.MetricCount())

		err = enc.StartMetricName("metric.overflow", 1)
		require.ErrorIs(t, err, errs.ErrMetricCountExceeded)
	})

	t.Run("default names-required caps at MaxMetricNamesCount and recovers", func(t *testing.T) {
		enc, err := NewTextEncoder(start)
		require.NoError(t, err)
		for i := 0; i < MaxMetricNamesCount; i++ {
			require.NoError(t, enc.StartMetricName(fmt.Sprintf("filler.%d", i), 1))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro(), "v", ""))
			require.NoError(t, enc.EndMetric())
		}
		require.Equal(t, MaxMetricNamesCount, enc.MetricCount())

		err = enc.StartMetricName("one.too.many", 1)
		require.ErrorIs(t, err, errs.ErrTooManyMetricNames)
		require.Equal(t, MaxMetricNamesCount, enc.MetricCount(), "count unchanged after rejection")

		// State is recoverable: another rejection then a successful finish.
		err = enc.StartMetricName("still.too.many", 1)
		require.ErrorIs(t, err, errs.ErrTooManyMetricNames)

		data, err := enc.Finish()
		require.NoError(t, err)
		dec, err := NewTextDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)
		require.Equal(t, MaxMetricNamesCount, b.MetricCount())
	})
}
