package blob

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/errs"
)

// ==============================================================================
// StartMetricName must validate name length in the preflight, atomically,
// before any state mutation or blob-encoding work — for both encoders, and
// regardless of whether the WithMetricNames()/WithoutMetricNames() option means
// names will actually end up in the payload.
//
// Before the fix, only internal/encoding/metadata.EncodeMetricNames (reached
// from Finish) rejected an oversized name, so:
//   - StartMetricName(oversized) returned nil (accepted)
//   - The whole blob was encoded before Finish finally rejected it
//   - Under text WithoutMetricNames(), the encode step is skipped entirely, so
//     the oversized name was silently accepted with no error at all.
// ==============================================================================

// oversizedMetricName is one byte longer than MaxMetricNameLength, so it must
// be rejected. boundaryMetricName is exactly MaxMetricNameLength bytes, so it
// must be accepted (off-by-one check on the new preflight bound).
var (
	oversizedMetricName = strings.Repeat("a", MaxMetricNameLength+1)
	boundaryMetricName  = strings.Repeat("a", MaxMetricNameLength)
)

// TestStartMetricName_OversizedName_Numeric covers the numeric encoder with and
// without WithMetricNames(). Both must reject at StartMetricName, not at Finish.
func TestStartMetricName_OversizedName_Numeric(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []NumericEncoderOption
	}{
		{"with WithMetricNames", []NumericEncoderOption{WithMetricNames()}},
		{"without WithMetricNames (default)", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			enc, err := NewNumericEncoder(start, tc.opts...)
			require.NoError(t, err)

			err = enc.StartMetricName(oversizedMetricName, 1)
			require.ErrorIs(t, err, errs.ErrInvalidMetricName,
				"oversized name must be rejected at StartMetricName, before any blob is encoded")

			// Encoder state must be completely unchanged: no entry was added,
			// no metric is in progress.
			require.Equal(t, 0, enc.MetricCount(), "rejection must not add an index entry")
			require.Equal(t, uint64(0), enc.curMetricID, "rejection must not start a metric")

			// The encoder remains usable: a different metric can still be
			// started, ended, and finished after the rejection. (Note: this
			// is a fresh, never-finished encoder — reuse after Finish is a
			// separate, pre-existing "encoder already finished" concern and
			// is not what this test is about.)
			require.NoError(t, enc.StartMetricName("metric.ok", 1))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1.0, ""))
			require.NoError(t, enc.EndMetric())

			data, err := enc.Finish()
			require.NoError(t, err)

			dec, err := NewNumericDecoder(data)
			require.NoError(t, err)
			b, err := dec.Decode()
			require.NoError(t, err)
			require.Equal(t, 1, b.MetricCount())
			require.True(t, b.HasMetricName("metric.ok"))
		})
	}
}

// TestStartMetricName_OversizedName_Text covers the text encoder with and
// without WithoutMetricNames(). Both must reject at StartMetricName; before the
// fix, the WithoutMetricNames() case silently accepted the oversized name
// because it never reaches the encode step at all.
func TestStartMetricName_OversizedName_Text(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []TextEncoderOption
	}{
		{"without WithoutMetricNames (default, names stored)", nil},
		{"with WithoutMetricNames", []TextEncoderOption{WithoutMetricNames()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			enc, err := NewTextEncoder(start, tc.opts...)
			require.NoError(t, err)

			err = enc.StartMetricName(oversizedMetricName, 1)
			require.ErrorIs(t, err, errs.ErrInvalidMetricName,
				"oversized name must be rejected at StartMetricName regardless of WithoutMetricNames()")

			require.Equal(t, 0, enc.MetricCount(), "rejection must not add an index entry")
			require.Equal(t, uint64(0), enc.curMetricID, "rejection must not start a metric")

			// Encoder remains usable afterwards.
			require.NoError(t, enc.StartMetricName("metric.ok", 1))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro(), "v", ""))
			require.NoError(t, enc.EndMetric())

			data, err := enc.Finish()
			require.NoError(t, err)

			dec, err := NewTextDecoder(data)
			require.NoError(t, err)
			b, err := dec.Decode()
			require.NoError(t, err)
			require.Equal(t, 1, b.MetricCount())
			require.True(t, b.HasMetricName("metric.ok"))
		})
	}
}

// TestStartMetricName_NameLengthBoundary asserts the off-by-one edge: exactly
// MaxMetricNameLength bytes is accepted (not just oversized names rejected),
// for both encoders.
func TestStartMetricName_NameLengthBoundary(t *testing.T) {
	t.Run("numeric", func(t *testing.T) {
		start := time.Now()
		enc, err := NewNumericEncoder(start, WithMetricNames())
		require.NoError(t, err)

		require.NoError(t, enc.StartMetricName(boundaryMetricName, 1),
			"a name of exactly MaxMetricNameLength bytes must be accepted")
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1.0, ""))
		require.NoError(t, enc.EndMetric())

		data, err := enc.Finish()
		require.NoError(t, err)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)
		require.True(t, b.HasMetricName(boundaryMetricName))
	})

	t.Run("text", func(t *testing.T) {
		start := time.Now()
		enc, err := NewTextEncoder(start)
		require.NoError(t, err)

		require.NoError(t, enc.StartMetricName(boundaryMetricName, 1),
			"a name of exactly MaxMetricNameLength bytes must be accepted")
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), "v", ""))
		require.NoError(t, enc.EndMetric())

		data, err := enc.Finish()
		require.NoError(t, err)
		dec, err := NewTextDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)
		require.True(t, b.HasMetricName(boundaryMetricName))
	})
}
