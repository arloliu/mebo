package blob

import (
	"cmp"
	"iter"
	"slices"
	"time"

	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/section"
)

// TextBlobSet represents an immutable collection of TextBlob instances that
// together contain time-series data for metrics across multiple time windows.
//
// The blobs are automatically sorted by their start time, enabling efficient
// time-ordered iteration across the entire dataset. Metrics may not be present
// in all blobs (e.g., sparse data where some metrics have no data points in certain
// time windows).
//
// TextBlobSet is designed to be immutable and safe for concurrent reads. Once
// created, the set cannot be modified. Use value semantics when passing BlobSets
// to functions.
//
// Example use case: A BlobSet containing hourly blobs for a 24-hour period,
// where each blob contains metrics with data points for that hour.
type TextBlobSet struct {
	blobs []TextBlob
	// identity is the lazily-built logical-identity table that groups member blob
	// entries by their logical metric name/id across the set. It is nil unless a
	// collision was observed at construction; a nil identity keeps every
	// ID-keyed accessor on today's zero-alloc direct per-member iteration.
	identity *setLogicalIdentity
}

// NewTextBlobSet creates a new TextBlobSet from the provided blobs.
//
// The blobs are automatically sorted by their start time in ascending order.
// The provided slice is copied to ensure immutability - modifications to the
// original slice will not affect the returned BlobSet.
//
// Parameters:
//   - blobs: Slice of TextBlob instances to include in the set
//
// Returns:
//   - TextBlobSet: An immutable blob set with blobs sorted by start time
//   - error: Returns an error if the blobs slice is empty
//
// Example:
//
//	blob1 := createBlob(time1, metrics1)
//	blob2 := createBlob(time2, metrics2)
//	blobSet, err := NewTextBlobSet([]TextBlob{blob1, blob2})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	// blobSet is immutable and safe for concurrent reads
func NewTextBlobSet(blobs []TextBlob) (TextBlobSet, error) {
	if len(blobs) == 0 {
		return TextBlobSet{}, errs.ErrEmptyBlobSet
	}

	// Create a copy to avoid modifying the caller's slice
	sortedBlobs := make([]TextBlob, len(blobs))
	copy(sortedBlobs, blobs)

	// Sort blobs by start time in ascending order. A STABLE sort keeps the caller's
	// slice order for equal-StartTime members, which the canonical logical-metric
	// ordering (grouping entries by logical metric name/id across blobs) relies on
	// to break equal-StartTime ties deterministically.
	slices.SortStableFunc(sortedBlobs, func(a, b TextBlob) int {
		return cmp.Compare(a.startTimeMicros, b.startTimeMicros)
	})

	return TextBlobSet{
		blobs:    sortedBlobs,
		identity: newSetLogicalIdentity(func(i int) *indexMaps[section.TextIndexEntry] { return &sortedBlobs[i].index }, len(sortedBlobs)),
	}, nil
}

// All returns a sequence of (index, TextDataPoint) tuples for the given metric ID
// across all blobs in the set, in chronological order.
//
// The iterator will seamlessly traverse all blobs, yielding data points with their
// global index. If a metric is not present in some blobs, those blobs are
// automatically skipped.
//
// The index is 0-based and continuous across all blobs. For example, if blob 0
// has 10 points and blob 1 has 5 points, indices will be 0-14.
//
// Performance: Single iteration through all blobs with minimal overhead.
func (s TextBlobSet) All(metricID uint64) iter.Seq2[int, TextDataPoint] {
	targetName, collided := s.identity.resolveID(metricID)

	return func(yield func(int, TextDataPoint) bool) {
		globalIndex := 0
		for i := range s.blobs {
			blob := &s.blobs[i]
			entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
			if !ok {
				continue
			}
			// Iterate through all data points in this blob for the metric
			for _, dp := range blob.allFromEntry(entry) {
				if !yield(globalIndex, dp) {
					return
				}
				globalIndex++
			}
		}
	}
}

// AllByName returns a sequence of (index, TextDataPoint) tuples for the given metric name
// across all blobs in the set, in chronological order.
//
// Returns an empty iterator if any blob doesn't support metric names or the metric
// name doesn't exist.
//
// The index is 0-based and continuous across all blobs.
func (s TextBlobSet) AllByName(metricName string) iter.Seq2[int, TextDataPoint] {
	skipStripped := s.identity.excludesStripped(metricName)

	return func(yield func(int, TextDataPoint) bool) {
		globalIndex := 0
		for i := range s.blobs {
			blob := &s.blobs[i]
			entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
			if !ok {
				continue
			}
			// Iterate through all data points in this blob for the metric
			for _, dp := range blob.allFromEntry(entry) {
				if !yield(globalIndex, dp) {
					return
				}
				globalIndex++
			}
		}
	}
}

// AllTimestamps returns a sequence of timestamps for the given metric ID
// across all blobs in the set, in chronological order.
//
// The iterator will seamlessly traverse all blobs, yielding timestamps in
// time order. If a metric is not present in some blobs, those blobs are
// automatically skipped.
func (s TextBlobSet) AllTimestamps(metricID uint64) iter.Seq[int64] {
	targetName, collided := s.identity.resolveID(metricID)

	return func(yield func(int64) bool) {
		for i := range s.blobs {
			blob := &s.blobs[i]
			entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
			if !ok {
				continue
			}
			for ts := range blob.allTimestampsFromEntry(entry) {
				if !yield(ts) {
					return
				}
			}
		}
	}
}

// AllTimestampsByName returns a sequence of timestamps for the given metric name
// across all blobs in the set, in chronological order.
//
// Returns an empty iterator if any blob doesn't support metric names or the metric
// name doesn't exist.
func (s TextBlobSet) AllTimestampsByName(metricName string) iter.Seq[int64] {
	skipStripped := s.identity.excludesStripped(metricName)

	return func(yield func(int64) bool) {
		for i := range s.blobs {
			blob := &s.blobs[i]
			entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
			if !ok {
				continue
			}
			for ts := range blob.allTimestampsFromEntry(entry) {
				if !yield(ts) {
					return
				}
			}
		}
	}
}

// AllValues returns a sequence of values for the given metric ID
// across all blobs in the set, in chronological order.
//
// The iterator will seamlessly traverse all blobs, yielding values in
// time order. If a metric is not present in some blobs, those blobs are
// automatically skipped.
func (s TextBlobSet) AllValues(metricID uint64) iter.Seq[string] {
	targetName, collided := s.identity.resolveID(metricID)

	return func(yield func(string) bool) {
		for i := range s.blobs {
			blob := &s.blobs[i]
			entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
			if !ok {
				continue
			}
			for val := range blob.allValuesFromEntry(entry) {
				if !yield(val) {
					return
				}
			}
		}
	}
}

// AllValuesByName returns a sequence of values for the given metric name
// across all blobs in the set, in chronological order.
//
// Returns an empty iterator if any blob doesn't support metric names or the metric
// name doesn't exist.
func (s TextBlobSet) AllValuesByName(metricName string) iter.Seq[string] {
	skipStripped := s.identity.excludesStripped(metricName)

	return func(yield func(string) bool) {
		for i := range s.blobs {
			blob := &s.blobs[i]
			entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
			if !ok {
				continue
			}
			for val := range blob.allValuesFromEntry(entry) {
				if !yield(val) {
					return
				}
			}
		}
	}
}

// AllTags returns a sequence of tags for the given metric ID
// across all blobs in the set, in chronological order.
//
// The iterator will seamlessly traverse all blobs, yielding tags in
// time order. If a metric is not present in some blobs, those blobs are
// automatically skipped. Tags can be empty strings.
//
// When any member carries tags, a member without tags yields one empty tag
// per data point, so the sequence stays aligned with the data points.
// A set where no member carries tags yields nothing.
func (s TextBlobSet) AllTags(metricID uint64) iter.Seq[string] {
	targetName, collided := s.identity.resolveID(metricID)

	padTags := anyHasTag(s.blobs)

	return func(yield func(string) bool) {
		for i := range s.blobs {
			blob := &s.blobs[i]
			entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
			if !ok {
				continue
			}
			if !blob.HasTag() {
				// Tags disabled or optimized away. When other members carry
				// tags, pad with one empty tag per point to keep the sequence
				// aligned with the data points; otherwise yield nothing.
				if padTags && !yieldEmptyTags(int(entry.Count), yield) {
					return
				}

				continue
			}
			for tag := range blob.allTagsFromEntry(entry) {
				if !yield(tag) {
					return
				}
			}
		}
	}
}

// AllTagsByName returns a sequence of tags for the given metric name
// across all blobs in the set, in chronological order.
//
// Returns an empty iterator if any blob doesn't support metric names or the metric
// name doesn't exist.
func (s TextBlobSet) AllTagsByName(metricName string) iter.Seq[string] {
	skipStripped := s.identity.excludesStripped(metricName)

	padTags := anyHasTag(s.blobs)

	return func(yield func(string) bool) {
		for i := range s.blobs {
			blob := &s.blobs[i]
			entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
			if !ok {
				continue
			}
			if !blob.HasTag() {
				// Tags disabled or optimized away. When other members carry
				// tags, pad with one empty tag per point to keep the sequence
				// aligned with the data points; otherwise yield nothing.
				if padTags && !yieldEmptyTags(int(entry.Count), yield) {
					return
				}

				continue
			}
			for tag := range blob.allTagsFromEntry(entry) {
				if !yield(tag) {
					return
				}
			}
		}
	}
}

// Len returns the number of blobs in the set.
func (s TextBlobSet) Len() int {
	return len(s.blobs)
}

// TimeRange returns the time range covered by this blob set.
// Returns the start time of the first blob and the start time of the last blob.
//
// Note: The actual time range extends beyond the last blob's start time
// to include its data points. To get the exact end time, you would need
// to inspect the last timestamp in the last blob.
func (s TextBlobSet) TimeRange() (start, end time.Time) {
	if len(s.blobs) == 0 {
		return time.Time{}, time.Time{}
	}

	return s.blobs[0].StartTime(), s.blobs[len(s.blobs)-1].StartTime()
}

// BlobAt returns a pointer to the blob at the specified index in chronological order.
//
// Parameters:
//   - index: Zero-based index into the sorted blob slice.
//
// Returns:
//   - *TextBlob: Pointer to the blob, or nil if the index is out of bounds.
func (s TextBlobSet) BlobAt(index int) *TextBlob {
	if index < 0 || index >= len(s.blobs) {
		return nil
	}

	return &s.blobs[index]
}

// Blobs returns all blobs in chronological order.
//
// The returned slice is a copy and can be safely modified without affecting the set.
//
// Returns:
//   - []TextBlob: A copy of the internal blob slice sorted by start time.
func (s TextBlobSet) Blobs() []TextBlob {
	result := make([]TextBlob, len(s.blobs))
	copy(result, s.blobs)

	return result
}

// ValueAt returns the value at the specified global index across all blobs for the given metric.
// The index is 0-based and spans across all blobs in chronological order.
//
// For example, if blob 0 has 10 points and blob 1 has 5 points:
//   - Index 0-9 refer to blob 0
//   - Index 10-14 refer to blob 1
//
// Returns (value, true) if the index is valid, or ("", false) if:
//   - The metric doesn't exist in any blob
//   - The index is out of bounds
//   - The index falls within a blob that doesn't contain this metric
//
// Performance: O(blobs) to find the blob, plus O(local index) within that blob,
// because text data points are decoded sequentially.
func (s TextBlobSet) ValueAt(metricID uint64, index int) (string, bool) {
	if index < 0 || len(s.blobs) == 0 {
		return "", false
	}

	targetName, collided := s.identity.resolveID(metricID)

	// Find which blob contains this index by accumulating counts
	currentOffset := 0
	for i := range s.blobs {
		blob := &s.blobs[i]
		entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
		if !ok {
			continue
		}
		blobLen := int(entry.Count)

		// Check if index falls within this blob
		if index < currentOffset+blobLen {
			// Calculate local index within this blob
			localIndex := index - currentOffset

			// Get value at local index
			return blob.valueAtFromEntry(entry, localIndex)
		}

		currentOffset += blobLen
	}

	// Index is beyond the total count across all blobs
	return "", false
}

// TimestampAt returns the timestamp at the specified global index across all blobs for the given metric.
// The index is 0-based and spans across all blobs in chronological order.
//
// For example, if blob 0 has 10 points and blob 1 has 5 points:
//   - Index 0-9 refer to blob 0
//   - Index 10-14 refer to blob 1
//
// Returns (timestamp, true) if the index is valid, or (0, false) if:
//   - The metric doesn't exist in any blob
//   - The index is out of bounds
//   - The index falls within a blob that doesn't contain this metric
//
// Performance: O(blobs) to find the blob, plus O(local index) within that blob,
// because text data points are decoded sequentially.
func (s TextBlobSet) TimestampAt(metricID uint64, index int) (int64, bool) {
	if index < 0 || len(s.blobs) == 0 {
		return 0, false
	}

	targetName, collided := s.identity.resolveID(metricID)

	// Find which blob contains this index by accumulating counts
	currentOffset := 0
	for i := range s.blobs {
		blob := &s.blobs[i]
		entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
		if !ok {
			continue
		}
		blobLen := int(entry.Count)

		// Check if index falls within this blob
		if index < currentOffset+blobLen {
			// Calculate local index within this blob
			localIndex := index - currentOffset

			// Get timestamp at local index
			return blob.timestampAtFromEntry(entry, localIndex)
		}

		currentOffset += blobLen
	}

	// Index is beyond the total count across all blobs
	return 0, false
}

// TagAt returns the tag at the specified global index across all blobs for the given metric.
// The index is 0-based and spans across all blobs in chronological order.
//
// For example, if blob 0 has 10 points and blob 1 has 5 points:
//   - Index 0-9 refer to blob 0
//   - Index 10-14 refer to blob 1
//
// Returns (tag, true) if the index is valid, or ("", false) if:
//   - The metric doesn't exist in any blob
//   - The index is out of bounds
//   - The index falls within a blob that doesn't contain this metric
//
// Performance: O(blobs) to find the blob, plus O(local index) within that blob,
// because text data points are decoded sequentially.
func (s TextBlobSet) TagAt(metricID uint64, index int) (string, bool) {
	if index < 0 || len(s.blobs) == 0 {
		return "", false
	}

	targetName, collided := s.identity.resolveID(metricID)

	// Find which blob contains this index by accumulating counts
	currentOffset := 0
	for i := range s.blobs {
		blob := &s.blobs[i]
		entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
		if !ok {
			continue
		}
		blobLen := int(entry.Count)

		// Check if index falls within this blob
		if index < currentOffset+blobLen {
			// Calculate local index within this blob
			localIndex := index - currentOffset

			// Tags disabled or optimized away: the point exists but carries no tag
			if !blob.HasTag() {
				return "", true
			}

			// Get tag at local index
			return blob.tagAtFromEntry(entry, localIndex)
		}

		currentOffset += blobLen
	}

	// Index is beyond the total count across all blobs
	return "", false
}

// MetricLen returns the total number of data points for the given metric ID across all blobs.
//
// This method sums up the data point counts from all blobs that contain the metric.
//
// Parameters:
//   - metricID: The metric ID to query
//
// Returns:
//   - int: Total number of data points, or 0 if the metric doesn't exist in any blob
//
// Example:
//
//	blobSet, _ := NewTextBlobSet(blobs)
//	totalPoints := blobSet.MetricLen(metricID)
//	fmt.Printf("Metric has %d data points across all blobs\n", totalPoints)
func (s TextBlobSet) MetricLen(metricID uint64) int {
	targetName, collided := s.identity.resolveID(metricID)

	totalLen := 0
	for i := range s.blobs {
		if entry, ok := s.blobs[i].index.resolveEntry(metricID, targetName, collided); ok {
			totalLen += int(entry.Count)
		}
	}

	return totalLen
}

// MetricLenByName returns the total number of data points for the given metric name across all blobs.
//
// This method sums up the data point counts from all blobs that contain the metric.
//
// Parameters:
//   - metricName: The metric name to query
//
// Returns:
//   - int: Total number of data points, or 0 if the metric doesn't exist in any blob
//
// Example:
//
//	blobSet, _ := NewTextBlobSet(blobs)
//	totalPoints := blobSet.MetricLenByName("log.message")
//	fmt.Printf("Metric has %d data points across all blobs\n", totalPoints)
func (s TextBlobSet) MetricLenByName(metricName string) int {
	skipStripped := s.identity.excludesStripped(metricName)

	totalLen := 0
	for i := range s.blobs {
		if entry, ok := s.blobs[i].index.resolveEntryByName(metricName, skipStripped); ok {
			totalLen += int(entry.Count)
		}
	}

	return totalLen
}

// MetricDuration calculates the time span for the given metric ID across all blobs.
//
// The duration is calculated as the difference between the first timestamp in the
// first blob containing this metric and the last timestamp in the last blob containing
// this metric. Only blobs that contain the metric are considered.
//
// Parameters:
//   - metricID: The metric ID to query
//
// Returns:
//   - int64: Duration in timestamp units (e.g., microseconds if timestamps are in microseconds),
//     or 0 if the metric doesn't exist or has fewer than 2 data points
//
// Example:
//
//	duration := blobSet.MetricDuration(metricID)
//	fmt.Printf("Metric spans %d timestamp units\n", duration)
func (s TextBlobSet) MetricDuration(metricID uint64) int64 {
	// A collided ID resolves to the first colliding name's logical metric; name-keyed
	// duration walks exactly that metric's members. targetName IS the first colliding
	// name, so a stripped member correctly contributes and needs no filter.
	if targetName, collided := s.identity.resolveID(metricID); collided {
		return calculateDurationByName(s.blobs, targetName, nil)
	}

	return calculateDuration(s.blobs, metricID)
}

// MetricCount returns the number of distinct logical set metrics, grouped by
// identity: the metric name when the set is names-bearing, else the MetricID. A
// collided ID that carries two distinct names counts as two logical metrics; the same
// name across members counts once (the merge is preserved).
func (s TextBlobSet) MetricCount() int {
	plan := buildLogicalPlan(func(i int) *indexMaps[section.TextIndexEntry] { return &s.blobs[i].index }, len(s.blobs))

	return len(plan.ids)
}

// MetricIDs returns the MetricID of each logical set metric in canonical order.
// A collided ID appears once per colliding name; the returned slice is newly
// allocated.
func (s TextBlobSet) MetricIDs() []uint64 {
	plan := buildLogicalPlan(func(i int) *indexMaps[section.TextIndexEntry] { return &s.blobs[i].index }, len(s.blobs))

	return plan.ids
}

// MetricNames returns the distinct logical metric names in canonical order,
// deduplicated. Returns nil when the set carries no metric names.
func (s TextBlobSet) MetricNames() []string {
	plan := buildLogicalPlan(func(i int) *indexMaps[section.TextIndexEntry] { return &s.blobs[i].index }, len(s.blobs))

	return plan.metricNames()
}

// HasMetricID reports whether the given metric ID is present in any member blob.
func (s TextBlobSet) HasMetricID(metricID uint64) bool {
	for i := range s.blobs {
		if s.blobs[i].HasMetricID(metricID) {
			return true
		}
	}

	return false
}

// MetricDurationByName calculates the time span for the given metric name across all blobs.
//
// The duration is calculated as the difference between the first timestamp in the
// first blob containing this metric and the last timestamp in the last blob containing
// this metric. Only blobs that contain the metric are considered.
//
// Parameters:
//   - metricName: The metric name to query
//
// Returns:
//   - int64: Duration in timestamp units (e.g., microseconds if timestamps are in microseconds),
//     or 0 if the metric doesn't exist or has fewer than 2 data points
//
// Example:
//
//	duration := blobSet.MetricDurationByName("log.message")
//	fmt.Printf("Metric spans %d timestamp units\n", duration)
func (s TextBlobSet) MetricDurationByName(metricName string) int64 {
	if s.identity.excludesStripped(metricName) {
		// metricName is not the first colliding name for its ID, so the stripped members
		// that merely hash-match it belong to the other logical metric.
		return calculateDurationByName(s.blobs, metricName, func(i int) bool {
			return s.blobs[i].index.names != nil
		})
	}

	return calculateDurationByName(s.blobs, metricName, nil)
}
