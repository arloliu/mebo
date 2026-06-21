package blob

// Callback-style (push) iteration over a NumericBlobSet. These mirror the
// All / AllTimestamps / AllValues set iterators but take the yield callback as a
// plain parameter, delegating to each blob's ForEach* method.
//
// They are strictly faster than the range-over-func set iterators: the set
// iterator must heap-allocate its returned closure and forces the caller's loop
// body to the heap, and it wraps each blob's All* (itself a heap closure) in an
// outer closure. The ForEach* forms remove the outer closure and reach the
// per-blob static decode loops, so they compound the per-blob stack-state
// speedup with the removal of the set-level closure: ~21-24% faster and ~94%
// fewer allocations on a multi-blob scan (see docs/perf/FOREACH_CALLBACK_API.md).
//
// Index semantics match the set's All: the index passed to yield is the global,
// 0-based, continuous position across all blobs (not the per-blob index).
//
// Each method returns true if the metric exists in at least one blob (including
// when iteration was stopped early by yield), false if it is absent from every
// blob or yield is nil.

// forEachAcrossBlobs drives a push callback over every blob in chronological
// order, remapping each blob's local index to a continuous global index. It is
// the shared engine for all six exported ForEach* set methods.
//
// perBlob is the blob-level ForEach* method, supplied as a method expression
// (e.g. NumericBlob.ForEachValues) so each call site stays a one-liner; key is
// the metric ID (uint64) or name (string) forwarded to it. Only the unavoidable
// per-call adapter closure allocates — the generic form keeps the same
// allocation count and wall time as a hand-written method.
func forEachAcrossBlobs[K, T any](
	blobs []NumericBlob,
	key K,
	yield func(int, T) bool,
	perBlob func(b NumericBlob, key K, adapter func(int, T) bool) bool,
) bool {
	if yield == nil {
		return false
	}

	var (
		found       bool
		globalIndex int
		stopped     bool
	)

	adapter := func(_ int, v T) bool {
		if !yield(globalIndex, v) {
			stopped = true
			return false
		}
		globalIndex++

		return true
	}

	for i := range blobs {
		if perBlob(blobs[i], key, adapter) {
			found = true
		}
		if stopped {
			break
		}
	}

	return found
}

// ForEach calls yield for each data point of the given metric ID across all
// blobs in chronological order, stopping early if yield returns false. It is the
// callback equivalent of All.
//
// Returns false if the metric is absent from every blob, or if yield is nil.
func (s NumericBlobSet) ForEach(metricID uint64, yield func(idx int, dp NumericDataPoint) bool) bool {
	return forEachAcrossBlobs(s.blobs, metricID, yield, NumericBlob.ForEach)
}

// ForEachByName calls yield for each data point of the given metric name across
// all blobs in chronological order, stopping early if yield returns false.
//
// See ForEach for semantics and performance characteristics.
//
// Returns false if the metric is absent from every blob, or if yield is nil.
func (s NumericBlobSet) ForEachByName(metricName string, yield func(idx int, dp NumericDataPoint) bool) bool {
	return forEachAcrossBlobs(s.blobs, metricName, yield, NumericBlob.ForEachByName)
}

// ForEachValues calls yield for each value of the given metric ID across all
// blobs in chronological order, stopping early if yield returns false. It is the
// callback equivalent of AllValues.
//
// Returns false if the metric is absent from every blob, or if yield is nil.
func (s NumericBlobSet) ForEachValues(metricID uint64, yield func(idx int, val float64) bool) bool {
	return forEachAcrossBlobs(s.blobs, metricID, yield, NumericBlob.ForEachValues)
}

// ForEachValuesByName calls yield for each value of the given metric name across
// all blobs in chronological order, stopping early if yield returns false.
//
// See ForEachValues for semantics and performance characteristics.
//
// Returns false if the metric is absent from every blob, or if yield is nil.
func (s NumericBlobSet) ForEachValuesByName(metricName string, yield func(idx int, val float64) bool) bool {
	return forEachAcrossBlobs(s.blobs, metricName, yield, NumericBlob.ForEachValuesByName)
}

// ForEachTimestamps calls yield for each timestamp of the given metric ID across
// all blobs in chronological order, stopping early if yield returns false. It is
// the callback equivalent of AllTimestamps.
//
// Returns false if the metric is absent from every blob, or if yield is nil.
func (s NumericBlobSet) ForEachTimestamps(metricID uint64, yield func(idx int, ts int64) bool) bool {
	return forEachAcrossBlobs(s.blobs, metricID, yield, NumericBlob.ForEachTimestamps)
}

// ForEachTimestampsByName calls yield for each timestamp of the given metric
// name across all blobs in chronological order, stopping early if yield returns
// false.
//
// See ForEachTimestamps for semantics and performance characteristics.
//
// Returns false if the metric is absent from every blob, or if yield is nil.
func (s NumericBlobSet) ForEachTimestampsByName(metricName string, yield func(idx int, ts int64) bool) bool {
	return forEachAcrossBlobs(s.blobs, metricName, yield, NumericBlob.ForEachTimestampsByName)
}
