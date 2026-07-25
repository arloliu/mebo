package regression

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/internal/collisiontest"
	"github.com/stretchr/testify/require"
)

// buildBlob encodes one numeric blob with the given (name → point count) metrics.
func buildBlob(t *testing.T, start time.Time, metrics map[string]int, opts ...blob.NumericEncoderOption) blob.NumericBlob {
	t.Helper()
	enc, err := blob.NewNumericEncoder(start, opts...)
	require.NoError(t, err)
	for name, n := range metrics {
		require.NoError(t, enc.StartMetricName(name, n))
		for i := 0; i < n; i++ {
			require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(i)*1_000_000, float64(i), ""))
		}
		require.NoError(t, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(t, err)
	dec, err := blob.NewNumericDecoder(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)

	return b
}

func TestRegression_CollisionGuard(t *testing.T) {
	start := time.Now()
	nA, nB := collisiontest.NameA, collisiontest.NameB

	t.Run("within-blob collision rejected by all four entry points", func(t *testing.T) {
		b := buildBlob(t, start, map[string]int{nA: 50, nB: 50})
		blobs := []blob.NumericBlob{b}

		_, err := Analyze(blobs)
		require.ErrorIs(t, err, errs.ErrCollisionNotSupported)
		_, err = AnalyzeWithOptions(blobs)
		require.ErrorIs(t, err, errs.ErrCollisionNotSupported)
		_, err = AnalyzeEach(blobs)
		require.ErrorIs(t, err, errs.ErrCollisionNotSupported)
		_, err = AnalyzeEachWithOptions(blobs)
		require.ErrorIs(t, err, errs.ErrCollisionNotSupported)
	})

	t.Run("cross-member collision without names is intrinsically indistinguishable", func(t *testing.T) {
		// Without blob.WithMetricNames(), a single-metric blob doesn't store its
		// own name, so two members that each hash to H are structurally
		// identical to the aggregator: it cannot tell "same metric repeated"
		// from "different metric, same hash". Accepted (merged) is the only
		// sound behavior when names aren't available; the next two subtests
		// cover the names-bearing case, which IS detectable via
		// blob.WithMetricNames() (exists on this branch, blob/numeric_encoder_config.go)
		// and must be rejected by the aggregating entry points.
		b1 := buildBlob(t, start, map[string]int{nA: 200})
		b2 := buildBlob(t, start.Add(time.Hour), map[string]int{nB: 200})
		blobs := []blob.NumericBlob{b1, b2}

		_, err := Analyze(blobs)
		require.NotErrorIs(t, err, errs.ErrCollisionNotSupported,
			"names-free cross-member collision is intrinsically indistinguishable")
		_, err = AnalyzeEach(blobs)
		require.NotErrorIs(t, err, errs.ErrCollisionNotSupported)
	})

	t.Run("cross-member collision with names rejected by aggregating entry points", func(t *testing.T) {
		// Each single-metric member stores its own name via blob.WithMetricNames(),
		// so checkNoCollision can see that ID H is bound to two different names
		// (nA in b1, nB in b2). Analyze/AnalyzeWithOptions pass the whole slice
		// through the guard in a single chunkAndMeasureWithConfig call and must
		// reject it.
		b1 := buildBlob(t, start, map[string]int{nA: 200}, blob.WithMetricNames())
		b2 := buildBlob(t, start.Add(time.Hour), map[string]int{nB: 200}, blob.WithMetricNames())
		blobs := []blob.NumericBlob{b1, b2}

		_, err := Analyze(blobs)
		require.ErrorIs(t, err, errs.ErrCollisionNotSupported)
		_, err = AnalyzeWithOptions(blobs)
		require.ErrorIs(t, err, errs.ErrCollisionNotSupported)
	})

	t.Run("cross-member collision with names accepted by per-blob entry points", func(t *testing.T) {
		// AnalyzeEach/AnalyzeEachWithOptions route each blob through the guard
		// ALONE (see the chunkAndMeasureWithConfig call sites in analyzer.go),
		// so the cross-member id→name pairing is never assembled in a single
		// checkNoCollision call and the pair must be accepted, producing one
		// result per blob.
		b1 := buildBlob(t, start, map[string]int{nA: 200}, blob.WithMetricNames())
		b2 := buildBlob(t, start.Add(time.Hour), map[string]int{nB: 200}, blob.WithMetricNames())
		blobs := []blob.NumericBlob{b1, b2}

		results, err := AnalyzeEach(blobs)
		require.NoError(t, err)
		require.Len(t, results, 2)
		for i, res := range results {
			require.NotNil(t, res, "result %d", i)
			require.NotNil(t, res.BestFit, "result %d BestFit", i)
		}

		results, err = AnalyzeEachWithOptions(blobs)
		require.NoError(t, err)
		require.Len(t, results, 2)
		for i, res := range results {
			require.NotNil(t, res, "result %d", i)
			require.NotNil(t, res.BestFit, "result %d BestFit", i)
		}
	})

	t.Run("same metric repeated across windows still accepted", func(t *testing.T) {
		b1 := buildBlob(t, start, map[string]int{"cpu.usage": 200, "mem.usage": 200})
		b2 := buildBlob(t, start.Add(time.Hour), map[string]int{"cpu.usage": 200, "mem.usage": 200})
		blobs := []blob.NumericBlob{b1, b2}

		_, err := Analyze(blobs)
		require.NoError(t, err)
		_, err = AnalyzeWithOptions(blobs)
		require.NoError(t, err)
		_, err = AnalyzeEach(blobs)
		require.NoError(t, err)
		_, err = AnalyzeEachWithOptions(blobs)
		require.NoError(t, err)
	})

	t.Run("same metric repeated across windows still accepted with names payload present", func(t *testing.T) {
		// This is the boundary a too-broad guard would break: with
		// blob.WithMetricNames() forcing the names payload on, checkNoCollision's
		// cross-member branch runs for every entry point below. It must
		// distinguish "same ID, same name" (ordinary repeat — accept) from
		// "same ID, different name" (collision — reject, covered above).
		b1 := buildBlob(t, start, map[string]int{"cpu.usage": 200}, blob.WithMetricNames())
		b2 := buildBlob(t, start.Add(time.Hour), map[string]int{"cpu.usage": 200}, blob.WithMetricNames())
		blobs := []blob.NumericBlob{b1, b2}

		_, err := Analyze(blobs)
		require.NoError(t, err)
		_, err = AnalyzeWithOptions(blobs)
		require.NoError(t, err)
		_, err = AnalyzeEach(blobs)
		require.NoError(t, err)
		_, err = AnalyzeEachWithOptions(blobs)
		require.NoError(t, err)
	})

	t.Run("non-collided set unaffected", func(t *testing.T) {
		b := buildBlob(t, start, map[string]int{"a.metric": 200, "b.metric": 200})
		_, err := Analyze([]blob.NumericBlob{b})
		require.NoError(t, err)
	})
}
