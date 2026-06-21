package blob

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/internal/hash"
)

// buildForEachTestSet builds a 3-blob set for metric "test.metric" with point
// counts 3/5/2 (global indices 0..9), blobs in ascending start-time order.
func buildForEachTestSet(t *testing.T) (NumericBlobSet, string, uint64) {
	t.Helper()

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	name := "test.metric"

	mk := func(base time.Time, vals []float64) NumericBlob {
		ts := make([]int64, len(vals))
		for i := range vals {
			ts[i] = base.Add(time.Duration(i) * time.Minute).UnixMicro()
		}

		return createBlobWithTimestamp(t, base, name, ts, vals)
	}

	blob1 := mk(start, []float64{10, 11, 12})
	blob2 := mk(start.Add(time.Hour), []float64{20, 21, 22, 23, 24})
	blob3 := mk(start.Add(2*time.Hour), []float64{30, 31})

	set, err := NewNumericBlobSet([]NumericBlob{blob1, blob2, blob3})
	require.NoError(t, err)

	return set, name, hash.ID(name)
}

func TestNumericBlobSet_ForEach_MatchesAll(t *testing.T) {
	set, _, id := buildForEachTestSet(t)

	wantIdx := make([]int, 0, 10)
	want := make([]NumericDataPoint, 0, 10)
	for i, dp := range set.All(id) {
		wantIdx = append(wantIdx, i)
		want = append(want, dp)
	}
	require.Len(t, want, 10)

	var gotIdx []int
	var got []NumericDataPoint
	found := set.ForEach(id, func(i int, dp NumericDataPoint) bool {
		gotIdx = append(gotIdx, i)
		got = append(got, dp)

		return true
	})
	require.True(t, found)
	require.Equal(t, wantIdx, gotIdx) // continuous global index 0..9
	require.Equal(t, want, got)
}

func TestNumericBlobSet_ForEachValues_MatchesAll(t *testing.T) {
	set, _, id := buildForEachTestSet(t)

	want := make([]float64, 0, 10)
	for v := range set.AllValues(id) {
		want = append(want, v)
	}
	require.Len(t, want, 10)

	var got []float64
	var idx int
	found := set.ForEachValues(id, func(i int, v float64) bool {
		require.Equal(t, idx, i)
		idx++
		got = append(got, v)

		return true
	})
	require.True(t, found)
	require.Equal(t, want, got)
}

func TestNumericBlobSet_ForEachTimestamps_MatchesAll(t *testing.T) {
	set, _, id := buildForEachTestSet(t)

	want := make([]int64, 0, 10)
	for ts := range set.AllTimestamps(id) {
		want = append(want, ts)
	}
	require.Len(t, want, 10)

	var got []int64
	var idx int
	found := set.ForEachTimestamps(id, func(i int, ts int64) bool {
		require.Equal(t, idx, i)
		idx++
		got = append(got, ts)

		return true
	})
	require.True(t, found)
	require.Equal(t, want, got)
}

func TestNumericBlobSet_ForEachByName_MatchesByID(t *testing.T) {
	set, name, id := buildForEachTestSet(t)

	// Data points
	want := make([]NumericDataPoint, 0, 10)
	for _, dp := range set.All(id) {
		want = append(want, dp)
	}
	var got []NumericDataPoint
	require.True(t, set.ForEachByName(name, func(_ int, dp NumericDataPoint) bool {
		got = append(got, dp)

		return true
	}))
	require.Equal(t, want, got)

	// Values
	wantV := make([]float64, 0, 10)
	for v := range set.AllValues(id) {
		wantV = append(wantV, v)
	}
	var gotV []float64
	require.True(t, set.ForEachValuesByName(name, func(_ int, v float64) bool {
		gotV = append(gotV, v)

		return true
	}))
	require.Equal(t, wantV, gotV)

	// Timestamps
	wantT := make([]int64, 0, 10)
	for ts := range set.AllTimestamps(id) {
		wantT = append(wantT, ts)
	}
	var gotT []int64
	require.True(t, set.ForEachTimestampsByName(name, func(_ int, ts int64) bool {
		gotT = append(gotT, ts)

		return true
	}))
	require.Equal(t, wantT, gotT)
}

// TestNumericBlobSet_ForEach_EarlyStop stops mid-set (after 4 of 10 points,
// which crosses from blob 0 into blob 1) and verifies the prefix matches All.
func TestNumericBlobSet_ForEach_EarlyStop(t *testing.T) {
	set, _, id := buildForEachTestSet(t)

	var got []NumericDataPoint
	found := set.ForEach(id, func(i int, dp NumericDataPoint) bool {
		got = append(got, dp)

		return i < 3 // stop after global index 3 (4 points)
	})
	require.True(t, found)
	require.Len(t, got, 4)

	want := make([]NumericDataPoint, 0, 4)
	for _, dp := range set.All(id) {
		want = append(want, dp)
		if len(want) == 4 {
			break
		}
	}
	require.Equal(t, want, got)

	// Values early stop crossing the same boundary.
	var gotV []float64
	require.True(t, set.ForEachValues(id, func(i int, v float64) bool {
		gotV = append(gotV, v)

		return i < 3
	}))
	require.Equal(t, []float64{10, 11, 12, 20}, gotV)
}

func TestNumericBlobSet_ForEach_NotFound(t *testing.T) {
	set, _, _ := buildForEachTestSet(t)

	calls := 0
	require.False(t, set.ForEach(99999, func(int, NumericDataPoint) bool { calls++; return true }))
	require.False(t, set.ForEachValues(99999, func(int, float64) bool { calls++; return true }))
	require.False(t, set.ForEachTimestamps(99999, func(int, int64) bool { calls++; return true }))
	require.False(t, set.ForEachByName("nope", func(int, NumericDataPoint) bool { calls++; return true }))
	require.False(t, set.ForEachValuesByName("nope", func(int, float64) bool { calls++; return true }))
	require.False(t, set.ForEachTimestampsByName("nope", func(int, int64) bool { calls++; return true }))
	require.Zero(t, calls)
}

func TestNumericBlobSet_ForEach_NilYield(t *testing.T) {
	set, name, id := buildForEachTestSet(t)

	require.False(t, set.ForEach(id, nil))
	require.False(t, set.ForEachValues(id, nil))
	require.False(t, set.ForEachTimestamps(id, nil))
	require.False(t, set.ForEachByName(name, nil))
	require.False(t, set.ForEachValuesByName(name, nil))
	require.False(t, set.ForEachTimestampsByName(name, nil))
}

// TestNumericBlobSet_ForEach_Sparse covers a metric present in only some blobs:
// the global index must stay continuous across the blob that lacks it, and the
// result must match All.
func TestNumericBlobSet_ForEach_Sparse(t *testing.T) {
	start := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	name := "sparse.metric"
	other := "other.metric"
	id := hash.ID(name)

	mk := func(base time.Time, metric string, vals []float64) NumericBlob {
		ts := make([]int64, len(vals))
		for i := range vals {
			ts[i] = base.Add(time.Duration(i) * time.Minute).UnixMicro()
		}

		return createBlobWithTimestamp(t, base, metric, ts, vals)
	}

	// Blob 0 has the metric, blob 1 does NOT (only "other"), blob 2 has it again.
	blob0 := mk(start, name, []float64{1, 2})
	blob1 := mk(start.Add(time.Hour), other, []float64{99, 99, 99})
	blob2 := mk(start.Add(2*time.Hour), name, []float64{3, 4, 5})

	set, err := NewNumericBlobSet([]NumericBlob{blob0, blob1, blob2})
	require.NoError(t, err)

	want := make([]NumericDataPoint, 0, 5)
	for _, dp := range set.All(id) {
		want = append(want, dp)
	}

	var got []NumericDataPoint
	var idx int
	found := set.ForEach(id, func(i int, dp NumericDataPoint) bool {
		require.Equal(t, idx, i)
		idx++
		got = append(got, dp)

		return true
	})
	require.True(t, found)
	require.Equal(t, want, got)
	require.Len(t, got, 5) // 2 + 0 + 3, indices 0..4

	// Values: global index must stay continuous across the gap blob.
	wantV := make([]float64, 0, 5)
	for v := range set.AllValues(id) {
		wantV = append(wantV, v)
	}
	var gotV []float64
	var idxV int
	require.True(t, set.ForEachValues(id, func(i int, v float64) bool {
		require.Equal(t, idxV, i)
		idxV++
		gotV = append(gotV, v)

		return true
	}))
	require.Equal(t, wantV, gotV)
	require.Equal(t, []float64{1, 2, 3, 4, 5}, gotV)

	// Timestamps.
	wantT := make([]int64, 0, 5)
	for tsv := range set.AllTimestamps(id) {
		wantT = append(wantT, tsv)
	}
	var gotT []int64
	var idxT int
	require.True(t, set.ForEachTimestamps(id, func(i int, tsv int64) bool {
		require.Equal(t, idxT, i)
		idxT++
		gotT = append(gotT, tsv)

		return true
	}))
	require.Equal(t, wantT, gotT)

	// ByName variants resolve to the same hashed ID and must match.
	var gotVByName []float64
	require.True(t, set.ForEachValuesByName(name, func(_ int, v float64) bool {
		gotVByName = append(gotVByName, v)

		return true
	}))
	require.Equal(t, wantV, gotVByName)

	var gotTByName []int64
	require.True(t, set.ForEachTimestampsByName(name, func(_ int, tsv int64) bool {
		gotTByName = append(gotTByName, tsv)

		return true
	}))
	require.Equal(t, wantT, gotTByName)

	var gotDPByName []NumericDataPoint
	require.True(t, set.ForEachByName(name, func(_ int, dp NumericDataPoint) bool {
		gotDPByName = append(gotDPByName, dp)

		return true
	}))
	require.Equal(t, want, gotDPByName)
}
