package blob

import (
	"iter"
	"slices"

	"github.com/arloliu/mebo/section"
)

// BlobSetIterator provides sequential iteration through data points across multiple blobs.
// All iteration methods return global indices that span across all blobs in the set.
type BlobSetIterator interface {
	// AllNumerics iterates through all numeric data points for the given metric ID.
	// Returns global index and data point for each iteration.
	AllNumerics(metricID uint64) iter.Seq2[int, NumericDataPoint]

	// AllNumericsByName iterates through all numeric data points for the given metric name.
	// Returns global index and data point for each iteration.
	AllNumericsByName(metricName string) iter.Seq2[int, NumericDataPoint]

	// AllTexts iterates through all text data points for the given metric ID.
	// Returns global index and data point for each iteration.
	AllTexts(metricID uint64) iter.Seq2[int, TextDataPoint]

	// AllTextsByName iterates through all text data points for the given metric name.
	// Returns global index and data point for each iteration.
	AllTextsByName(metricName string) iter.Seq2[int, TextDataPoint]

	// AllNumericValues iterates through all numeric values for the given metric ID.
	// Returns global index and value for each iteration.
	AllNumericValues(metricID uint64) iter.Seq2[int, float64]

	// AllNumericValuesByName iterates through all numeric values for the given metric name.
	// Returns global index and value for each iteration.
	AllNumericValuesByName(metricName string) iter.Seq2[int, float64]

	// AllTextValues iterates through all text values for the given metric ID.
	// Returns global index and value for each iteration.
	AllTextValues(metricID uint64) iter.Seq2[int, string]

	// AllTextValuesByName iterates through all text values for the given metric name.
	// Returns global index and value for each iteration.
	AllTextValuesByName(metricName string) iter.Seq2[int, string]

	// AllTimestamps iterates through all timestamps for the given metric ID.
	// Works for both numeric and text blobs since timestamps are common.
	// Returns global index and timestamp for each iteration.
	AllTimestamps(metricID uint64) iter.Seq2[int, int64]

	// AllTimestampsByName iterates through all timestamps for the given metric name.
	// Works for both numeric and text blobs since timestamps are common.
	// Returns global index and timestamp for each iteration.
	AllTimestampsByName(metricName string) iter.Seq2[int, int64]

	// AllTags iterates through all tags for the given metric ID.
	// Works for both numeric and text blobs since tags are common.
	// Returns global index and tag string for each iteration.
	AllTags(metricID uint64) iter.Seq2[int, string]

	// AllTagsByName iterates through all tags for the given metric name.
	// Works for both numeric and text blobs since tags are common.
	// Returns global index and tag string for each iteration.
	AllTagsByName(metricName string) iter.Seq2[int, string]
}

// BlobSetIndexer provides random access to data points by global index across multiple blobs.
// All indexing methods use global indices that span across all blobs in the set.
type BlobSetIndexer interface {
	// TimestampAt returns the timestamp at the given global index for the metric ID.
	// Returns false if the metric ID doesn't exist or index is out of range.
	TimestampAt(metricID uint64, index int) (int64, bool)

	// TimestampAtByName returns the timestamp at the given global index for the metric name.
	// Returns false if the metric name doesn't exist or index is out of range.
	TimestampAtByName(metricName string, index int) (int64, bool)

	// NumericValueAt returns the numeric value at the given global index for the metric ID.
	// Returns false if the metric ID doesn't exist, is not numeric, or index is out of range.
	NumericValueAt(metricID uint64, index int) (float64, bool)

	// NumericValueAtByName returns the numeric value at the given global index for the metric name.
	// Returns false if the metric name doesn't exist, is not numeric, or index is out of range.
	NumericValueAtByName(metricName string, index int) (float64, bool)

	// TextValueAt returns the text value at the given global index for the metric ID.
	// Returns false if the metric ID doesn't exist, is not text, or index is out of range.
	TextValueAt(metricID uint64, index int) (string, bool)

	// TextValueAtByName returns the text value at the given global index for the metric name.
	// Returns false if the metric name doesn't exist, is not text, or index is out of range.
	TextValueAtByName(metricName string, index int) (string, bool)

	// TagAt returns the tag string at the given global index for the metric ID.
	// Returns false if the metric ID doesn't exist or index is out of range.
	TagAt(metricID uint64, index int) (string, bool)

	// TagAtByName returns the tag string at the given global index for the metric name.
	// Returns false if the metric name doesn't exist or index is out of range.
	TagAtByName(metricName string, index int) (string, bool)

	// NumericAt returns the complete data point at the given global index for the metric ID.
	// Returns false if the metric ID doesn't exist, is not numeric, or index is out of range.
	NumericAt(metricID uint64, index int) (NumericDataPoint, bool)

	// NumericAtByName returns the complete data point at the given global index for the metric name.
	// Returns false if the metric name doesn't exist, is not numeric, or index is out of range.
	NumericAtByName(metricName string, index int) (NumericDataPoint, bool)

	// TextAt returns the complete data point at the given global index for the metric ID.
	// Returns false if the metric ID doesn't exist, is not text, or index is out of range.
	TextAt(metricID uint64, index int) (TextDataPoint, bool)

	// TextAtByName returns the complete data point at the given global index for the metric name.
	// Returns false if the metric name doesn't exist, is not text, or index is out of range.
	TextAtByName(metricName string, index int) (TextDataPoint, bool)
}

// BlobSet represents a collection of blobs (both numeric and text) sorted by start time.
// It provides unified access to data points across multiple blobs with global indexing.
//
// Performance: Numeric and text blobs are stored separately for optimal performance:
//   - Type-specific queries avoid type assertions and skip irrelevant blobs
//   - Better CPU cache locality with similar data together
//   - Generic queries check numeric first (95% of typical workloads)
type BlobSet struct {
	numericBlobs []NumericBlob // Sorted by StartTime
	textBlobs    []TextBlob    // Sorted by StartTime
}

var (
	_ BlobSetIterator = BlobSet{}
	_ BlobSetIndexer  = BlobSet{}
)

// NewBlobSet creates a new BlobSet from numeric and text blobs.
// Blobs are sorted by start time within each type for deterministic iteration order.
//
// Parameters:
//   - numericBlobs: List of numeric blobs to include in the set
//   - textBlobs: List of text blobs to include in the set
//
// Returns:
//   - BlobSet: Constructed BlobSet with parsed blobs
func NewBlobSet(numericBlobs []NumericBlob, textBlobs []TextBlob) BlobSet {
	// Sort numeric blobs by start time (optimized: compare microseconds directly)
	sortedNumeric := make([]NumericBlob, len(numericBlobs))
	copy(sortedNumeric, numericBlobs)
	slices.SortFunc(sortedNumeric, func(a, b NumericBlob) int {
		return int(a.startTimeMicros - b.startTimeMicros)
	})

	// Sort text blobs by start time (optimized: compare microseconds directly)
	sortedText := make([]TextBlob, len(textBlobs))
	copy(sortedText, textBlobs)
	slices.SortFunc(sortedText, func(a, b TextBlob) int {
		return int(a.startTimeMicros - b.startTimeMicros)
	})

	return BlobSet{
		numericBlobs: sortedNumeric,
		textBlobs:    sortedText,
	}
}

// DecodeBlobSet creates a new BlobSet from a list of encoded byte slices.
// Each byte slice is parsed to determine if it's a numeric or text blob.
//
// Parameters:
//   - blobs: List of byte slices representing encoded blobs
//
// Returns:
//   - BlobSet: Constructed BlobSet with parsed blobs
//   - error: Parsing or decoding error
func DecodeBlobSet(blobs ...[]byte) (BlobSet, error) {
	numericBlobs := make([]NumericBlob, 0, len(blobs)/2)
	textBlobs := make([]TextBlob, 0, len(blobs)/2)
	for _, blob := range blobs {
		if section.IsNumericBlob(blob) {
			decoder, err := NewNumericDecoder(blob)
			if err != nil {
				return BlobSet{}, err
			}

			nb, err := decoder.Decode()
			if err != nil {
				return BlobSet{}, err
			}

			numericBlobs = append(numericBlobs, nb)
		} else if section.IsTextBlob(blob) {
			decoder, err := NewTextDecoder(blob)
			if err != nil {
				return BlobSet{}, err
			}

			tb, err := decoder.Decode()
			if err != nil {
				return BlobSet{}, err
			}

			textBlobs = append(textBlobs, tb)
		}
	}

	return NewBlobSet(numericBlobs, textBlobs), nil
}

func (bs BlobSet) AllNumerics(metricID uint64) iter.Seq2[int, NumericDataPoint] {
	return func(yield func(int, NumericDataPoint) bool) {
		index := 0
		for _, blob := range bs.numericBlobs {
			if blob.HasMetricID(metricID) {
				for _, dp := range blob.All(metricID) {
					if !yield(index, dp) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) AllNumericsByName(metricName string) iter.Seq2[int, NumericDataPoint] {
	return func(yield func(int, NumericDataPoint) bool) {
		index := 0
		for _, blob := range bs.numericBlobs {
			if blob.HasMetricName(metricName) {
				for _, dp := range blob.AllByName(metricName) {
					if !yield(index, dp) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) AllTexts(metricID uint64) iter.Seq2[int, TextDataPoint] {
	return func(yield func(int, TextDataPoint) bool) {
		index := 0
		for _, blob := range bs.textBlobs {
			if blob.HasMetricID(metricID) {
				for _, dp := range blob.All(metricID) {
					if !yield(index, dp) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) AllTextsByName(metricName string) iter.Seq2[int, TextDataPoint] {
	return func(yield func(int, TextDataPoint) bool) {
		index := 0
		for _, blob := range bs.textBlobs {
			if blob.HasMetricName(metricName) {
				for _, dp := range blob.AllByName(metricName) {
					if !yield(index, dp) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) AllNumericValues(metricID uint64) iter.Seq2[int, float64] {
	return func(yield func(int, float64) bool) {
		index := 0
		for _, blob := range bs.numericBlobs {
			if blob.HasMetricID(metricID) {
				for val := range blob.AllValues(metricID) {
					if !yield(index, val) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) AllNumericValuesByName(metricName string) iter.Seq2[int, float64] {
	return func(yield func(int, float64) bool) {
		index := 0
		for _, blob := range bs.numericBlobs {
			if blob.HasMetricName(metricName) {
				for val := range blob.AllValuesByName(metricName) {
					if !yield(index, val) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) AllTextValues(metricID uint64) iter.Seq2[int, string] {
	return func(yield func(int, string) bool) {
		index := 0
		for _, blob := range bs.textBlobs {
			if blob.HasMetricID(metricID) {
				for val := range blob.AllValues(metricID) {
					if !yield(index, val) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) AllTextValuesByName(metricName string) iter.Seq2[int, string] {
	return func(yield func(int, string) bool) {
		index := 0
		for _, blob := range bs.textBlobs {
			if blob.HasMetricName(metricName) {
				for val := range blob.AllValuesByName(metricName) {
					if !yield(index, val) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) AllTimestamps(metricID uint64) iter.Seq2[int, int64] {
	return func(yield func(int, int64) bool) {
		index := 0
		foundInNumeric := false

		for _, blob := range bs.numericBlobs {
			if blob.HasMetricID(metricID) {
				foundInNumeric = true
				for ts := range blob.AllTimestamps(metricID) {
					if !yield(index, ts) {
						return
					}
					index++
				}
			}
		}

		if foundInNumeric {
			return
		}

		for _, blob := range bs.textBlobs {
			if blob.HasMetricID(metricID) {
				for ts := range blob.AllTimestamps(metricID) {
					if !yield(index, ts) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) AllTimestampsByName(metricName string) iter.Seq2[int, int64] {
	return func(yield func(int, int64) bool) {
		index := 0
		foundInNumeric := false

		for _, blob := range bs.numericBlobs {
			if blob.HasMetricName(metricName) {
				foundInNumeric = true
				for ts := range blob.AllTimestampsByName(metricName) {
					if !yield(index, ts) {
						return
					}
					index++
				}
			}
		}

		if foundInNumeric {
			return
		}

		for _, blob := range bs.textBlobs {
			if blob.HasMetricName(metricName) {
				for ts := range blob.AllTimestampsByName(metricName) {
					if !yield(index, ts) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) AllTags(metricID uint64) iter.Seq2[int, string] {
	return func(yield func(int, string) bool) {
		index := 0
		foundInNumeric := false

		for _, blob := range bs.numericBlobs {
			if blob.HasMetricID(metricID) {
				foundInNumeric = true
				for tag := range blob.AllTags(metricID) {
					if !yield(index, tag) {
						return
					}
					index++
				}
			}
		}

		if foundInNumeric {
			return
		}

		for _, blob := range bs.textBlobs {
			if blob.HasMetricID(metricID) {
				for tag := range blob.AllTags(metricID) {
					if !yield(index, tag) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) AllTagsByName(metricName string) iter.Seq2[int, string] {
	return func(yield func(int, string) bool) {
		index := 0
		foundInNumeric := false

		for _, blob := range bs.numericBlobs {
			if blob.HasMetricName(metricName) {
				foundInNumeric = true
				for tag := range blob.AllTagsByName(metricName) {
					if !yield(index, tag) {
						return
					}
					index++
				}
			}
		}

		if foundInNumeric {
			return
		}

		for _, blob := range bs.textBlobs {
			if blob.HasMetricName(metricName) {
				for tag := range blob.AllTagsByName(metricName) {
					if !yield(index, tag) {
						return
					}
					index++
				}
			}
		}
	}
}

func (bs BlobSet) TimestampAt(metricID uint64, index int) (int64, bool) {
	if index < 0 {
		return 0, false
	}

	curIdx := 0

	for _, blob := range bs.numericBlobs {
		if !blob.HasMetricID(metricID) {
			continue
		}
		length := blob.Len(metricID)
		if curIdx+length > index {
			return blob.TimestampAt(metricID, index-curIdx)
		}
		curIdx += length
	}

	curIdx = 0 // Reset for text blobs
	for _, blob := range bs.textBlobs {
		if !blob.HasMetricID(metricID) {
			continue
		}
		length := blob.Len(metricID)
		if curIdx+length > index {
			return blob.TimestampAt(metricID, index-curIdx)
		}
		curIdx += length
	}

	return 0, false
}

func (bs BlobSet) TimestampAtByName(metricName string, index int) (int64, bool) {
	if index < 0 {
		return 0, false
	}

	curIdx := 0

	for _, blob := range bs.numericBlobs {
		if !blob.HasMetricName(metricName) {
			continue
		}
		length := blob.LenByName(metricName)
		if curIdx+length > index {
			return blob.TimestampAtByName(metricName, index-curIdx)
		}
		curIdx += length
	}

	curIdx = 0 // Reset for text blobs
	for _, blob := range bs.textBlobs {
		if !blob.HasMetricName(metricName) {
			continue
		}
		length := blob.LenByName(metricName)
		if curIdx+length > index {
			return blob.TimestampAtByName(metricName, index-curIdx)
		}
		curIdx += length
	}

	return 0, false
}

func (bs BlobSet) TagAt(metricID uint64, index int) (string, bool) {
	if index < 0 {
		return "", false
	}

	curIdx := 0

	// Try numeric blobs first (95% case)
	for _, blob := range bs.numericBlobs {
		if !blob.HasMetricID(metricID) {
			continue
		}
		length := blob.Len(metricID)
		if curIdx+length > index {
			return blob.TagAt(metricID, index-curIdx)
		}
		curIdx += length
	}

	curIdx = 0 // Reset for text blobs
	for _, blob := range bs.textBlobs {
		if !blob.HasMetricID(metricID) {
			continue
		}
		length := blob.Len(metricID)
		if curIdx+length > index {
			return blob.TagAt(metricID, index-curIdx)
		}
		curIdx += length
	}

	return "", false
}

func (bs BlobSet) TagAtByName(metricName string, index int) (string, bool) {
	if index < 0 {
		return "", false
	}

	curIdx := 0

	// Try numeric blobs first (95% case)
	for _, blob := range bs.numericBlobs {
		if !blob.HasMetricName(metricName) {
			continue
		}
		length := blob.LenByName(metricName)
		if curIdx+length > index {
			return blob.TagAtByName(metricName, index-curIdx)
		}
		curIdx += length
	}

	curIdx = 0 // Reset for text blobs
	for _, blob := range bs.textBlobs {
		if !blob.HasMetricName(metricName) {
			continue
		}
		length := blob.LenByName(metricName)
		if curIdx+length > index {
			return blob.TagAtByName(metricName, index-curIdx)
		}
		curIdx += length
	}

	return "", false
}

func (bs BlobSet) NumericValueAt(metricID uint64, index int) (float64, bool) {
	if index < 0 {
		return 0, false
	}

	curIdx := 0
	for _, blob := range bs.numericBlobs {
		if !blob.HasMetricID(metricID) {
			continue
		}
		length := blob.Len(metricID)
		if curIdx+length > index {
			return blob.ValueAt(metricID, index-curIdx)
		}
		curIdx += length
	}

	return 0, false
}

func (bs BlobSet) NumericValueAtByName(metricName string, index int) (float64, bool) {
	if index < 0 {
		return 0, false
	}

	curIdx := 0
	for _, blob := range bs.numericBlobs {
		if !blob.HasMetricName(metricName) {
			continue
		}
		length := blob.LenByName(metricName)
		if curIdx+length > index {
			return blob.ValueAtByName(metricName, index-curIdx)
		}
		curIdx += length
	}

	return 0, false
}

func (bs BlobSet) TextValueAt(metricID uint64, index int) (string, bool) {
	if index < 0 {
		return "", false
	}

	curIdx := 0
	for _, blob := range bs.textBlobs {
		if !blob.HasMetricID(metricID) {
			continue
		}
		length := blob.Len(metricID)
		if curIdx+length > index {
			return blob.ValueAt(metricID, index-curIdx)
		}
		curIdx += length
	}

	return "", false
}

func (bs BlobSet) TextValueAtByName(metricName string, index int) (string, bool) {
	if index < 0 {
		return "", false
	}

	curIdx := 0
	for _, blob := range bs.textBlobs {
		if !blob.HasMetricName(metricName) {
			continue
		}
		length := blob.LenByName(metricName)
		if curIdx+length > index {
			return blob.ValueAtByName(metricName, index-curIdx)
		}
		curIdx += length
	}

	return "", false
}

func (bs BlobSet) NumericAt(metricID uint64, index int) (NumericDataPoint, bool) {
	if index < 0 {
		return NumericDataPoint{}, false
	}

	curIdx := 0
	for _, blob := range bs.numericBlobs {
		if !blob.HasMetricID(metricID) {
			continue
		}
		length := blob.Len(metricID)
		if curIdx+length > index {
			localIndex := index - curIdx
			ts, tsOk := blob.TimestampAt(metricID, localIndex)
			val, valOk := blob.ValueAt(metricID, localIndex)
			tag, tagOk := blob.TagAt(metricID, localIndex)
			if tsOk && valOk && tagOk {
				return NumericDataPoint{Ts: ts, Val: val, Tag: tag}, true
			}

			return NumericDataPoint{}, false
		}
		curIdx += length
	}

	return NumericDataPoint{}, false
}

func (bs BlobSet) NumericAtByName(metricName string, index int) (NumericDataPoint, bool) {
	if index < 0 {
		return NumericDataPoint{}, false
	}

	curIdx := 0
	for _, blob := range bs.numericBlobs {
		if !blob.HasMetricName(metricName) {
			continue
		}
		length := blob.LenByName(metricName)
		if curIdx+length > index {
			localIndex := index - curIdx
			ts, tsOk := blob.TimestampAtByName(metricName, localIndex)
			val, valOk := blob.ValueAtByName(metricName, localIndex)
			tag, tagOk := blob.TagAtByName(metricName, localIndex)
			if tsOk && valOk && tagOk {
				return NumericDataPoint{Ts: ts, Val: val, Tag: tag}, true
			}

			return NumericDataPoint{}, false
		}
		curIdx += length
	}

	return NumericDataPoint{}, false
}

func (bs BlobSet) TextAt(metricID uint64, index int) (TextDataPoint, bool) {
	if index < 0 {
		return TextDataPoint{}, false
	}

	curIdx := 0
	for _, blob := range bs.textBlobs {
		if !blob.HasMetricID(metricID) {
			continue
		}
		length := blob.Len(metricID)
		if curIdx+length > index {
			localIndex := index - curIdx
			ts, tsOk := blob.TimestampAt(metricID, localIndex)
			val, valOk := blob.ValueAt(metricID, localIndex)
			tag, tagOk := blob.TagAt(metricID, localIndex)
			if tsOk && valOk && tagOk {
				return TextDataPoint{Ts: ts, Val: val, Tag: tag}, true
			}

			return TextDataPoint{}, false
		}
		curIdx += length
	}

	return TextDataPoint{}, false
}

func (bs BlobSet) TextAtByName(metricName string, index int) (TextDataPoint, bool) {
	if index < 0 {
		return TextDataPoint{}, false
	}

	curIdx := 0
	for _, blob := range bs.textBlobs {
		if !blob.HasMetricName(metricName) {
			continue
		}
		length := blob.LenByName(metricName)
		if curIdx+length > index {
			localIndex := index - curIdx
			ts, tsOk := blob.TimestampAtByName(metricName, localIndex)
			val, valOk := blob.ValueAtByName(metricName, localIndex)
			tag, tagOk := blob.TagAtByName(metricName, localIndex)
			if tsOk && valOk && tagOk {
				return TextDataPoint{Ts: ts, Val: val, Tag: tag}, true
			}

			return TextDataPoint{}, false
		}
		curIdx += length
	}

	return TextDataPoint{}, false
}

// MaterializeNumeric materializes all numeric blobs in this BlobSet into a
// MaterializedNumericBlobSet for O(1) random access across all numeric metrics.
//
// This is a thin wrapper that delegates to NumericBlobSet.Materialize().
// If the BlobSet contains no numeric blobs, returns an empty materialized set.
//
// Performance:
//   - Materialization cost: ~100μs per metric per blob (one-time)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~16 bytes per data point × total numeric data points
//
// Use this when:
//   - You need random access to numeric metrics across the entire time range
//   - You will access each metric multiple times
//   - Memory is available (~16 bytes per data point)
//
// Example:
//
//	blobSet := NewBlobSet(numericBlobs, textBlobs)
//	matNumeric := blobSet.MaterializeNumeric()
//	val, ok := matNumeric.ValueAt(metricID, 150)  // O(1) access
func (bs BlobSet) MaterializeNumeric() MaterializedNumericBlobSet {
	if len(bs.numericBlobs) == 0 {
		return MaterializedNumericBlobSet{
			data:  make(map[uint64]materializedNumericMetricSet),
			names: make(map[string]uint64),
		}
	}

	// Create NumericBlobSet and delegate to its Materialize()
	numericSet := &NumericBlobSet{blobs: bs.numericBlobs}

	return numericSet.Materialize()
}

// MaterializeText materializes all text blobs in this BlobSet into a
// MaterializedTextBlobSet for O(1) random access across all text metrics.
//
// This is a thin wrapper that delegates to TextBlobSet.Materialize().
// If the BlobSet contains no text blobs, returns an empty materialized set.
//
// Performance:
//   - Materialization cost: ~100μs per metric per blob (one-time)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~24 bytes per data point × total text data points
//
// Use this when:
//   - You need random access to text metrics across the entire time range
//   - You will access each metric multiple times
//   - Memory is available (~24 bytes per data point)
//
// Example:
//
//	blobSet := NewBlobSet(numericBlobs, textBlobs)
//	matText := blobSet.MaterializeText()
//	val, ok := matText.ValueAt(metricID, 150)  // O(1) access
func (bs BlobSet) MaterializeText() MaterializedTextBlobSet {
	if len(bs.textBlobs) == 0 {
		return MaterializedTextBlobSet{
			data:  make(map[uint64]materializedTextMetricSet),
			names: make(map[string]uint64),
		}
	}

	// Create TextBlobSet and delegate to its Materialize()
	textSet := &TextBlobSet{blobs: bs.textBlobs}

	return textSet.Materialize()
}

// MaterializeNumericMetric materializes a single numeric metric by ID from all numeric blobs
// in this BlobSet for O(1) random access without needing to pass metric ID on each call.
//
// This is a thin wrapper that delegates to NumericBlobSet.MaterializeMetric().
//
// Parameters:
//   - metricID: The metric ID to materialize
//
// Returns:
//   - MaterializedNumericMetric: The materialized metric with direct access methods
//   - bool: false if the metric is not found in any numeric blob
//
// Performance:
//   - Materialization cost: ~100μs (one-time, for one metric across all blobs)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~16 bytes per data point × total data points for this metric
//
// Example:
//
//	blobSet := NewBlobSet(numericBlobs, textBlobs)
//	metric, ok := blobSet.MaterializeNumericMetric(metricID)
//	if ok {
//	    val, _ := metric.ValueAt(150)  // O(1) access, no metric ID needed
//	}
func (bs BlobSet) MaterializeNumericMetric(metricID uint64) (MaterializedNumericMetric, bool) {
	if len(bs.numericBlobs) == 0 {
		return MaterializedNumericMetric{}, false
	}

	// Create NumericBlobSet and delegate to its MaterializeMetric()
	numericSet := &NumericBlobSet{blobs: bs.numericBlobs}

	return numericSet.MaterializeMetric(metricID)
}

// MaterializeNumericMetricByName materializes a single numeric metric by name from all numeric blobs
// in this BlobSet for O(1) random access without needing to pass metric name on each call.
//
// This is a thin wrapper that delegates to NumericBlobSet.MaterializeMetricByName().
//
// Parameters:
//   - metricName: The metric name to materialize
//
// Returns:
//   - MaterializedNumericMetric: The materialized metric with direct access methods
//   - bool: false if the metric is not found in any numeric blob
//
// Performance:
//   - Materialization cost: ~100μs (one-time, for one metric across all blobs)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~16 bytes per data point × total data points for this metric
//
// Example:
//
//	blobSet := NewBlobSet(numericBlobs, textBlobs)
//	metric, ok := blobSet.MaterializeNumericMetricByName("cpu.usage")
//	if ok {
//	    val, _ := metric.ValueAt(150)  // O(1) access, no metric name needed
//	}
func (bs BlobSet) MaterializeNumericMetricByName(metricName string) (MaterializedNumericMetric, bool) {
	if len(bs.numericBlobs) == 0 {
		return MaterializedNumericMetric{}, false
	}

	// Create NumericBlobSet and delegate to its MaterializeMetricByName()
	numericSet := &NumericBlobSet{blobs: bs.numericBlobs}

	return numericSet.MaterializeMetricByName(metricName)
}

// MaterializeTextMetric materializes a single text metric by ID from all text blobs
// in this BlobSet for O(1) random access without needing to pass metric ID on each call.
//
// This is a thin wrapper that delegates to TextBlobSet.MaterializeMetric().
//
// Parameters:
//   - metricID: The metric ID to materialize
//
// Returns:
//   - MaterializedTextMetric: The materialized metric with direct access methods
//   - bool: false if the metric is not found in any text blob
//
// Performance:
//   - Materialization cost: ~100μs (one-time, for one metric across all blobs)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~24 bytes per data point × total data points for this metric
//
// Example:
//
//	blobSet := NewBlobSet(numericBlobs, textBlobs)
//	metric, ok := blobSet.MaterializeTextMetric(metricID)
//	if ok {
//	    val, _ := metric.ValueAt(150)  // O(1) access, no metric ID needed
//	}
func (bs BlobSet) MaterializeTextMetric(metricID uint64) (MaterializedTextMetric, bool) {
	if len(bs.textBlobs) == 0 {
		return MaterializedTextMetric{}, false
	}

	// Create TextBlobSet and delegate to its MaterializeMetric()
	textSet := &TextBlobSet{blobs: bs.textBlobs}

	return textSet.MaterializeMetric(metricID)
}

// MaterializeTextMetricByName materializes a single text metric by name from all text blobs
// in this BlobSet for O(1) random access without needing to pass metric name on each call.
//
// This is a thin wrapper that delegates to TextBlobSet.MaterializeMetricByName().
//
// Parameters:
//   - metricName: The metric name to materialize
//
// Returns:
//   - MaterializedTextMetric: The materialized metric with direct access methods
//   - bool: false if the metric is not found in any text blob
//
// Performance:
//   - Materialization cost: ~100μs (one-time, for one metric across all blobs)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~24 bytes per data point × total data points for this metric
//
// Example:
//
//	blobSet := NewBlobSet(numericBlobs, textBlobs)
//	metric, ok := blobSet.MaterializeTextMetricByName("log.message")
//	if ok {
//	    val, _ := metric.ValueAt(150)  // O(1) access, no metric name needed
//	}
func (bs BlobSet) MaterializeTextMetricByName(metricName string) (MaterializedTextMetric, bool) {
	if len(bs.textBlobs) == 0 {
		return MaterializedTextMetric{}, false
	}

	// Create TextBlobSet and delegate to its MaterializeMetricByName()
	textSet := &TextBlobSet{blobs: bs.textBlobs}

	return textSet.MaterializeMetricByName(metricName)
}

// NumericBlobs returns the numeric blobs in this BlobSet.
// The blobs are sorted by start time.
func (bs BlobSet) NumericBlobs() []NumericBlob {
	return bs.numericBlobs
}

// TextBlobs returns the text blobs in this BlobSet.
// The blobs are sorted by start time.
func (bs BlobSet) TextBlobs() []TextBlob {
	return bs.textBlobs
}
