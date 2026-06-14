package blob

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
)

// TestNumericBlob_ALP_DispatchParity is the safety net for ALP value-codec
// wiring: it builds an ALP-encoded blob and asserts that all four read paths
// return identical values. Any missed dispatch site (valueAt random access,
// the decodeValues iterator that backs All/AllValues/ForEach, or the
// decodeValuesSlice materialize path) silently returns zeros and is caught
// here.
func TestNumericBlob_ALP_DispatchParity(t *testing.T) {
	blob, metrics := createTestBlob(t, format.TypeRaw, format.TypeALP)

	// Sanity: the blob actually recorded ALP as the value encoding.
	require.Equal(t, format.TypeALP, blob.ValueEncoding(), "blob value encoding must be ALP")

	// Materialize the whole blob once for the blob-level random-access path.
	material := blob.Materialize()

	for _, m := range metrics {
		expected := m.values

		// Path 1: public value iterator (AllValues).
		var iterVals []float64
		for v := range blob.AllValues(m.id) {
			iterVals = append(iterVals, v)
		}
		require.Equalf(t, expected, iterVals, "metric %d: AllValues iterator mismatch", m.id)

		// Path 2: random access via ValueAt for every index.
		atVals := make([]float64, len(expected))
		for i := range expected {
			v, ok := blob.ValueAt(m.id, i)
			require.Truef(t, ok, "metric %d: ValueAt(%d) not ok", m.id, i)
			atVals[i] = v
		}
		require.Equalf(t, expected, atVals, "metric %d: ValueAt mismatch", m.id)

		// Path 3a: materialization via the whole-blob Materialize().
		matVals := make([]float64, len(expected))
		for i := range expected {
			v, ok := material.ValueAt(m.id, i)
			require.Truef(t, ok, "metric %d: Materialize().ValueAt(%d) not ok", m.id, i)
			matVals[i] = v
		}
		require.Equalf(t, expected, matVals, "metric %d: Materialize() mismatch", m.id)

		// Path 3b: materialization via the single-metric MaterializeMetric().
		metric, ok := blob.MaterializeMetric(m.id)
		require.Truef(t, ok, "metric %d: MaterializeMetric not ok", m.id)
		require.Equalf(t, expected, metric.Values, "metric %d: MaterializeMetric mismatch", m.id)

		// Path 4: ForEach callback.
		var feVals []float64
		ok = blob.ForEach(m.id, func(idx int, dp NumericDataPoint) bool {
			require.Equalf(t, len(feVals), idx, "metric %d: ForEach index out of order", m.id)
			feVals = append(feVals, dp.Val)

			return true
		})
		require.Truef(t, ok, "metric %d: ForEach not ok", m.id)
		require.Equalf(t, expected, feVals, "metric %d: ForEach mismatch", m.id)
	}
}
