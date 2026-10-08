package blob

import "github.com/arloliu/mebo/section"

// Callback-style (push) iteration over a NumericBlobSet. These mirror the
// All / AllTimestamps / AllValues set iterators but take the yield callback as a
// plain parameter, reaching each blob's ForEach* decode loops directly.
//
// They are strictly faster than the range-over-func set iterators: the set
// iterator must heap-allocate its returned closure and forces the caller's loop
// body to the heap, and it wraps each blob's All* (itself a heap closure) in an
// outer closure. The ForEach* forms remove the outer closure and reach the
// per-blob static decode loops, so they compound the per-blob stack-state
// speedup with the removal of the set-level closure: ~21-24% faster and ~94%
// fewer allocations on a multi-blob scan (see docs/perf/foreach_callback_api.md).
// ForEachValues and ForEachTimestamps go further: their decode loops take the
// starting global index, so the user's yield is called directly with no
// adapter closure in between and nothing allocates.
//
// Index semantics match the set's All: the index passed to yield is the global,
// 0-based, continuous position across all blobs (not the per-blob index).
//
// Each method returns true if the metric exists in at least one blob (including
// when iteration was stopped early by yield), false if it is absent from every
// blob or yield is nil.

// setEntryResolver picks, for one member blob, the index entry a set-level
// ForEach* call must read. It applies the set's logical identity exactly like
// All*, ValueAt and MetricLen: a collided ID resolves to its first colliding
// name, and a name-keyed lookup skips stripped members that belong to another
// colliding name. Without a collision it reduces to the member's own lookup.
type setEntryResolver struct {
	metricName   string
	target       string
	metricID     uint64
	byName       bool
	collided     bool
	skipStripped bool
}

func (s NumericBlobSet) resolverByID(metricID uint64) setEntryResolver {
	target, collided := s.identity.resolveID(metricID)

	return setEntryResolver{metricID: metricID, target: target, collided: collided}
}

func (s NumericBlobSet) resolverByName(metricName string) setEntryResolver {
	return setEntryResolver{
		metricName:   metricName,
		byName:       true,
		skipStripped: s.identity.excludesStripped(metricName),
	}
}

// entry returns the member's index entry for the resolver's metric by pointer,
// or nil when the member lacks it.
// The pointer must not be retained, as entryFor and entryForName require.
func (r setEntryResolver) entry(blob *NumericBlob) *section.NumericIndexEntry {
	if r.byName {
		return blob.index.entryForName(r.metricName, r.skipStripped)
	}

	return blob.index.entryFor(r.metricID, r.target, r.collided)
}

func (r setEntryResolver) resolve(blob *NumericBlob) (section.NumericIndexEntry, bool) {
	if entry := r.entry(blob); entry != nil {
		return *entry, true
	}

	return section.NumericIndexEntry{}, false
}

// forEachAcrossBlobs drives a push callback over every blob in chronological order,
// remapping each blob's local index to a continuous global index.
// It is the engine for ForEach and ForEachByName, whose data-point loops index from zero;
// ForEachValues and ForEachTimestamps use the column helpers below.
//
// resolver selects each member's entry under the set's logical identity.
// The helpers call the blob-level loops by name:
// a call through a func value is dynamic, escape analysis then treats yield as leaking,
// and the adapter and every caller's capturing callback are moved to the heap.
func forEachAcrossBlobs(blobs []NumericBlob, resolver setEntryResolver, yield func(int, NumericDataPoint) bool) bool {
	if yield == nil {
		return false
	}

	var (
		found       bool
		globalIndex int
		stopped     bool
	)

	adapter := func(_ int, dp NumericDataPoint) bool {
		if !yield(globalIndex, dp) {
			stopped = true
			return false
		}
		globalIndex++

		return true
	}

	for i := range blobs {
		entry, ok := resolver.resolve(&blobs[i])
		if !ok {
			continue
		}
		found = true
		blobs[i].forEachFromEntry(entry, adapter)
		if stopped {
			break
		}
	}

	return found
}

// forEachValuesAcrossBlobs is the adapter-free engine for value iteration:
// forEachValuesFromEntry starts its indexes at base and returns the next base, or -1 once yield stops,
// so the user's yield is called directly with continuous global indexes.
func forEachValuesAcrossBlobs(blobs []NumericBlob, resolver setEntryResolver, yield func(int, float64) bool) bool {
	if yield == nil {
		return false
	}

	found := false
	next := 0
	for i := range blobs {
		entry, ok := resolver.resolve(&blobs[i])
		if !ok {
			continue
		}
		found = true
		next = blobs[i].forEachValuesFromEntry(entry, next, yield)
		if next < 0 {
			break
		}
	}

	return found
}

// forEachTimestampsAcrossBlobs is forEachValuesAcrossBlobs for the timestamp column.
func forEachTimestampsAcrossBlobs(blobs []NumericBlob, resolver setEntryResolver, yield func(int, int64) bool) bool {
	if yield == nil {
		return false
	}

	found := false
	next := 0
	for i := range blobs {
		entry, ok := resolver.resolve(&blobs[i])
		if !ok {
			continue
		}
		found = true
		next = blobs[i].forEachTimestampsFromEntry(entry, next, yield)
		if next < 0 {
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
	return forEachAcrossBlobs(s.blobs, s.resolverByID(metricID), yield)
}

// ForEachByName calls yield for each data point of the given metric name across
// all blobs in chronological order, stopping early if yield returns false.
//
// See ForEach for semantics and performance characteristics.
//
// Returns false if the metric is absent from every blob, or if yield is nil.
func (s NumericBlobSet) ForEachByName(metricName string, yield func(idx int, dp NumericDataPoint) bool) bool {
	return forEachAcrossBlobs(s.blobs, s.resolverByName(metricName), yield)
}

// ForEachValues calls yield for each value of the given metric ID across all
// blobs in chronological order, stopping early if yield returns false. It is the
// callback equivalent of AllValues.
//
// Returns false if the metric is absent from every blob, or if yield is nil.
func (s NumericBlobSet) ForEachValues(metricID uint64, yield func(idx int, val float64) bool) bool {
	return forEachValuesAcrossBlobs(s.blobs, s.resolverByID(metricID), yield)
}

// ForEachValuesByName calls yield for each value of the given metric name across
// all blobs in chronological order, stopping early if yield returns false.
//
// See ForEachValues for semantics and performance characteristics.
//
// Returns false if the metric is absent from every blob, or if yield is nil.
func (s NumericBlobSet) ForEachValuesByName(metricName string, yield func(idx int, val float64) bool) bool {
	return forEachValuesAcrossBlobs(s.blobs, s.resolverByName(metricName), yield)
}

// ForEachTimestamps calls yield for each timestamp of the given metric ID across
// all blobs in chronological order, stopping early if yield returns false. It is
// the callback equivalent of AllTimestamps.
//
// Returns false if the metric is absent from every blob, or if yield is nil.
func (s NumericBlobSet) ForEachTimestamps(metricID uint64, yield func(idx int, ts int64) bool) bool {
	return forEachTimestampsAcrossBlobs(s.blobs, s.resolverByID(metricID), yield)
}

// ForEachTimestampsByName calls yield for each timestamp of the given metric
// name across all blobs in chronological order, stopping early if yield returns
// false.
//
// See ForEachTimestamps for semantics and performance characteristics.
//
// Returns false if the metric is absent from every blob, or if yield is nil.
func (s NumericBlobSet) ForEachTimestampsByName(metricName string, yield func(idx int, ts int64) bool) bool {
	return forEachTimestampsAcrossBlobs(s.blobs, s.resolverByName(metricName), yield)
}
