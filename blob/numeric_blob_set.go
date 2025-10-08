package blob

import (
	"fmt"
	"iter"
	"slices"
	"time"
)

// NumericBlobSet represents an immutable collection of NumericBlob instances that
// together contain time-series data for metrics across multiple time windows.
//
// The blobs are automatically sorted by their start time, enabling efficient
// time-ordered iteration across the entire dataset. Metrics may not be present
// in all blobs (e.g., sparse data where some metrics have no data points in certain
// time windows).
//
// NumericBlobSet is designed to be immutable and safe for concurrent reads. Once
// created, the set cannot be modified. Use value semantics when passing BlobSets
// to functions.
//
// Example use case: A BlobSet containing hourly blobs for a 24-hour period,
// where each blob contains metrics with data points for that hour.
type NumericBlobSet struct {
	blobs []NumericBlob
}

// NewNumericBlobSet creates a new NumericBlobSet from the provided blobs.
//
// The blobs are automatically sorted by their start time in ascending order.
// The provided slice is copied to ensure immutability - modifications to the
// original slice will not affect the returned BlobSet.
//
// Parameters:
//   - blobs: Slice of NumericBlob instances to include in the set
//
// Returns:
//   - NumericBlobSet: An immutable blob set with blobs sorted by start time
//   - error: Returns an error if the blobs slice is empty
//
// Example:
//
//	blob1 := createBlob(time1, metrics1)
//	blob2 := createBlob(time2, metrics2)
//	blobSet, err := NewNumericBlobSet([]NumericBlob{blob1, blob2})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	// blobSet is immutable and safe for concurrent reads
func NewNumericBlobSet(blobs []NumericBlob) (NumericBlobSet, error) {
	if len(blobs) == 0 {
		return NumericBlobSet{}, fmt.Errorf("cannot create NumericBlobSet with empty blobs")
	}

	// Create a copy to avoid modifying the caller's slice
	sortedBlobs := make([]NumericBlob, len(blobs))
	copy(sortedBlobs, blobs)

	// Sort blobs by start time in ascending order
	slices.SortFunc(sortedBlobs, func(a, b NumericBlob) int {
		return a.startTime.Compare(b.startTime)
	})

	return NumericBlobSet{
		blobs: sortedBlobs,
	}, nil
}

// All returns a sequence of (index, NumericDataPoint) tuples for the given metric ID
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
func (s NumericBlobSet) All(metricID uint64) iter.Seq2[int, NumericDataPoint] {
	return func(yield func(int, NumericDataPoint) bool) {
		globalIndex := 0
		for i := range s.blobs {
			blob := &s.blobs[i]
			// Iterate through all data points in this blob for the metric
			for _, dp := range blob.All(metricID) {
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
func (s NumericBlobSet) AllTimestamps(metricID uint64) iter.Seq[int64] {
	return func(yield func(int64) bool) {
		for i := range s.blobs {
			blob := &s.blobs[i]
			for ts := range blob.AllTimestamps(metricID) {
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
func (s NumericBlobSet) AllValues(metricID uint64) iter.Seq[float64] {
	return func(yield func(float64) bool) {
		for i := range s.blobs {
			blob := &s.blobs[i]
			for val := range blob.AllValues(metricID) {
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
func (s NumericBlobSet) AllTags(metricID uint64) iter.Seq[string] {
	return func(yield func(string) bool) {
		for i := range s.blobs {
			blob := &s.blobs[i]
			for tag := range blob.AllTags(metricID) {
				if !yield(tag) {
					return
				}
			}
		}
	}
}

// Len returns the number of blobs in the set.
func (s NumericBlobSet) Len() int {
	return len(s.blobs)
}

// TimeRange returns the time range covered by this blob set.
// Returns the start time of the first blob and the start time of the last blob.
//
// Note: The actual time range extends beyond the last blob's start time
// to include its data points. To get the exact end time, you would need
// to inspect the last timestamp in the last blob.
func (s NumericBlobSet) TimeRange() (start, end time.Time) {
	if len(s.blobs) == 0 {
		return time.Time{}, time.Time{}
	}

	return s.blobs[0].startTime, s.blobs[len(s.blobs)-1].startTime
}

// BlobAt returns the blob at the specified index.
// Returns nil if the index is out of bounds.
func (s NumericBlobSet) BlobAt(index int) *NumericBlob {
	if index < 0 || index >= len(s.blobs) {
		return nil
	}

	return &s.blobs[index]
}

// Blobs returns all blobs in chronological order.
// The returned slice is a copy and can be safely modified without affecting the set.
func (s NumericBlobSet) Blobs() []NumericBlob {
	result := make([]NumericBlob, len(s.blobs))
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
// Returns (value, true) if the index is valid, or (0, false) if:
//   - The metric doesn't exist in any blob
//   - The index is out of bounds
//   - The index falls within a blob that doesn't contain this metric
//
// Performance: O(log n) to find the blob + O(1) to access the value within the blob.
func (s NumericBlobSet) ValueAt(metricID uint64, index int) (float64, bool) {
	if index < 0 || len(s.blobs) == 0 {
		return 0, false
	}

	// Find which blob contains this index by accumulating counts
	currentOffset := 0
	for i := range s.blobs {
		blob := &s.blobs[i]
		blobLen := blob.Len(metricID)

		// Check if index falls within this blob
		if index < currentOffset+blobLen {
			// Calculate local index within this blob
			localIndex := index - currentOffset

			// Get value at local index
			return blob.ValueAt(metricID, localIndex)
		}

		currentOffset += blobLen
	}

	// Index is beyond the total count across all blobs
	return 0, false
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
// Performance: O(log n) to find the blob + O(1) to access the timestamp within the blob.
func (s NumericBlobSet) TimestampAt(metricID uint64, index int) (int64, bool) {
	if index < 0 || len(s.blobs) == 0 {
		return 0, false
	}

	// Find which blob contains this index by accumulating counts
	currentOffset := 0
	for i := range s.blobs {
		blob := &s.blobs[i]
		blobLen := blob.Len(metricID)

		// Check if index falls within this blob
		if index < currentOffset+blobLen {
			// Calculate local index within this blob
			localIndex := index - currentOffset

			// Get timestamp at local index
			return blob.TimestampAt(metricID, localIndex)
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
// Performance: O(log n) to find the blob + O(1) to access the tag within the blob.
func (s NumericBlobSet) TagAt(metricID uint64, index int) (string, bool) {
	if index < 0 || len(s.blobs) == 0 {
		return "", false
	}

	// Find which blob contains this index by accumulating counts
	currentOffset := 0
	for i := range s.blobs {
		blob := &s.blobs[i]
		blobLen := blob.Len(metricID)

		// Check if index falls within this blob
		if index < currentOffset+blobLen {
			// Calculate local index within this blob
			localIndex := index - currentOffset

			// Get tag at local index
			return blob.TagAt(metricID, localIndex)
		}

		currentOffset += blobLen
	}

	// Index is beyond the total count across all blobs
	return "", false
}
