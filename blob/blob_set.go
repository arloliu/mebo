package blob

import (
	"cmp"
	"fmt"
	"iter"
	"slices"

	"github.com/arloliu/mebo/errs"
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
	// numericIdentity/textIdentity are the lazily-built logical-identity tables that
	// group this set's metrics by logical name/id, resolved PER concrete type (a
	// numeric H and a text H are independent). nil unless a collision was observed
	// for that type.
	numericIdentity *setLogicalIdentity
	textIdentity    *setLogicalIdentity
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
	// Sort numeric blobs by start time. A STABLE sort keeps caller order for
	// equal-StartTime members, honouring the canonical tie-break for logical
	// identity ordering.
	sortedNumeric := make([]NumericBlob, len(numericBlobs))
	copy(sortedNumeric, numericBlobs)
	slices.SortStableFunc(sortedNumeric, func(a, b NumericBlob) int {
		return cmp.Compare(a.startTimeMicros, b.startTimeMicros)
	})

	// Sort text blobs by start time (stable, same rationale).
	sortedText := make([]TextBlob, len(textBlobs))
	copy(sortedText, textBlobs)
	slices.SortStableFunc(sortedText, func(a, b TextBlob) int {
		return cmp.Compare(a.startTimeMicros, b.startTimeMicros)
	})

	return BlobSet{
		numericBlobs:    sortedNumeric,
		textBlobs:       sortedText,
		numericIdentity: newSetLogicalIdentity(func(i int) *indexMaps[section.NumericIndexEntry] { return &sortedNumeric[i].index }, len(sortedNumeric)),
		textIdentity:    newSetLogicalIdentity(func(i int) *indexMaps[section.TextIndexEntry] { return &sortedText[i].index }, len(sortedText)),
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
	for i, blob := range blobs {
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
		} else {
			return BlobSet{}, fmt.Errorf("%w: input %d is neither a numeric nor a text blob", errs.ErrInvalidMagicNumber, i)
		}
	}

	return NewBlobSet(numericBlobs, textBlobs), nil
}

func (bs BlobSet) AllNumerics(metricID uint64) iter.Seq2[int, NumericDataPoint] {
	targetName, collided := bs.numericIdentity.resolveID(metricID)

	return func(yield func(int, NumericDataPoint) bool) {
		index := 0
		for _, blob := range bs.numericBlobs {
			entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
			if !ok {
				continue
			}
			for _, dp := range blob.allFromEntry(entry) {
				if !yield(index, dp) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) AllNumericsByName(metricName string) iter.Seq2[int, NumericDataPoint] {
	skipStripped := bs.numericIdentity.excludesStripped(metricName)

	return func(yield func(int, NumericDataPoint) bool) {
		index := 0
		for _, blob := range bs.numericBlobs {
			entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
			if !ok {
				continue
			}
			for _, dp := range blob.allFromEntry(entry) {
				if !yield(index, dp) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) AllTexts(metricID uint64) iter.Seq2[int, TextDataPoint] {
	targetName, collided := bs.textIdentity.resolveID(metricID)

	return func(yield func(int, TextDataPoint) bool) {
		index := 0
		for _, blob := range bs.textBlobs {
			entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
			if !ok {
				continue
			}
			for _, dp := range blob.allFromEntry(entry) {
				if !yield(index, dp) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) AllTextsByName(metricName string) iter.Seq2[int, TextDataPoint] {
	skipStripped := bs.textIdentity.excludesStripped(metricName)

	return func(yield func(int, TextDataPoint) bool) {
		index := 0
		for _, blob := range bs.textBlobs {
			entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
			if !ok {
				continue
			}
			for _, dp := range blob.allFromEntry(entry) {
				if !yield(index, dp) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) AllNumericValues(metricID uint64) iter.Seq2[int, float64] {
	targetName, collided := bs.numericIdentity.resolveID(metricID)

	return func(yield func(int, float64) bool) {
		index := 0
		for _, blob := range bs.numericBlobs {
			entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
			if !ok {
				continue
			}
			for val := range blob.allValuesFromEntry(entry) {
				if !yield(index, val) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) AllNumericValuesByName(metricName string) iter.Seq2[int, float64] {
	skipStripped := bs.numericIdentity.excludesStripped(metricName)

	return func(yield func(int, float64) bool) {
		index := 0
		for _, blob := range bs.numericBlobs {
			entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
			if !ok {
				continue
			}
			for val := range blob.allValuesFromEntry(entry) {
				if !yield(index, val) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) AllTextValues(metricID uint64) iter.Seq2[int, string] {
	targetName, collided := bs.textIdentity.resolveID(metricID)

	return func(yield func(int, string) bool) {
		index := 0
		for _, blob := range bs.textBlobs {
			entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
			if !ok {
				continue
			}
			for val := range blob.allValuesFromEntry(entry) {
				if !yield(index, val) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) AllTextValuesByName(metricName string) iter.Seq2[int, string] {
	skipStripped := bs.textIdentity.excludesStripped(metricName)

	return func(yield func(int, string) bool) {
		index := 0
		for _, blob := range bs.textBlobs {
			entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
			if !ok {
				continue
			}
			for val := range blob.allValuesFromEntry(entry) {
				if !yield(index, val) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) AllTimestamps(metricID uint64) iter.Seq2[int, int64] {
	numTarget, numCollided := bs.numericIdentity.resolveID(metricID)
	txtTarget, txtCollided := bs.textIdentity.resolveID(metricID)

	return func(yield func(int, int64) bool) {
		index := 0
		foundInNumeric := false

		for _, blob := range bs.numericBlobs {
			entry, ok := blob.index.resolveEntry(metricID, numTarget, numCollided)
			if !ok {
				continue
			}
			foundInNumeric = true
			for ts := range blob.allTimestampsFromEntry(entry) {
				if !yield(index, ts) {
					return
				}
				index++
			}
		}

		if foundInNumeric {
			return
		}

		for _, blob := range bs.textBlobs {
			entry, ok := blob.index.resolveEntry(metricID, txtTarget, txtCollided)
			if !ok {
				continue
			}
			for ts := range blob.allTimestampsFromEntry(entry) {
				if !yield(index, ts) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) AllTimestampsByName(metricName string) iter.Seq2[int, int64] {
	numSkipStripped := bs.numericIdentity.excludesStripped(metricName)
	txtSkipStripped := bs.textIdentity.excludesStripped(metricName)

	return func(yield func(int, int64) bool) {
		index := 0
		foundInNumeric := false

		for _, blob := range bs.numericBlobs {
			entry, ok := blob.index.resolveEntryByName(metricName, numSkipStripped)
			if !ok {
				continue
			}
			foundInNumeric = true
			for ts := range blob.allTimestampsFromEntry(entry) {
				if !yield(index, ts) {
					return
				}
				index++
			}
		}

		if foundInNumeric {
			return
		}

		for _, blob := range bs.textBlobs {
			entry, ok := blob.index.resolveEntryByName(metricName, txtSkipStripped)
			if !ok {
				continue
			}
			for ts := range blob.allTimestampsFromEntry(entry) {
				if !yield(index, ts) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) AllTags(metricID uint64) iter.Seq2[int, string] {
	numPad := anyHasTag(bs.numericBlobs)
	txtPad := anyHasTag(bs.textBlobs)
	numTarget, numCollided := bs.numericIdentity.resolveID(metricID)
	txtTarget, txtCollided := bs.textIdentity.resolveID(metricID)

	return func(yield func(int, string) bool) {
		index := 0
		foundInNumeric := false

		for _, blob := range bs.numericBlobs {
			entry, ok := blob.index.resolveEntry(metricID, numTarget, numCollided)
			if !ok {
				continue
			}
			foundInNumeric = true
			if !blob.HasTag() {
				// Tags disabled or optimized away. When other numeric members
				// carry tags, pad with one empty tag per point so indexes stay
				// aligned with TimestampAt and TagAt; otherwise yield nothing.
				if numPad {
					for range entry.Count {
						if !yield(index, "") {
							return
						}
						index++
					}
				}

				continue
			}
			for tag := range blob.allTagsFromEntry(entry) {
				if !yield(index, tag) {
					return
				}
				index++
			}
		}

		if foundInNumeric {
			return
		}

		for _, blob := range bs.textBlobs {
			entry, ok := blob.index.resolveEntry(metricID, txtTarget, txtCollided)
			if !ok {
				continue
			}
			if !blob.HasTag() {
				if txtPad {
					for range int(entry.Count) {
						if !yield(index, "") {
							return
						}
						index++
					}
				}

				continue
			}
			for tag := range blob.allTagsFromEntry(entry) {
				if !yield(index, tag) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) AllTagsByName(metricName string) iter.Seq2[int, string] {
	numPad := anyHasTag(bs.numericBlobs)
	txtPad := anyHasTag(bs.textBlobs)
	numSkipStripped := bs.numericIdentity.excludesStripped(metricName)
	txtSkipStripped := bs.textIdentity.excludesStripped(metricName)

	return func(yield func(int, string) bool) {
		index := 0
		foundInNumeric := false

		for _, blob := range bs.numericBlobs {
			entry, ok := blob.index.resolveEntryByName(metricName, numSkipStripped)
			if !ok {
				continue
			}
			foundInNumeric = true
			if !blob.HasTag() {
				// Tags disabled or optimized away. When other numeric members
				// carry tags, pad with one empty tag per point so indexes stay
				// aligned with TimestampAt and TagAt; otherwise yield nothing.
				if numPad {
					for range entry.Count {
						if !yield(index, "") {
							return
						}
						index++
					}
				}

				continue
			}
			for tag := range blob.allTagsFromEntry(entry) {
				if !yield(index, tag) {
					return
				}
				index++
			}
		}

		if foundInNumeric {
			return
		}

		for _, blob := range bs.textBlobs {
			entry, ok := blob.index.resolveEntryByName(metricName, txtSkipStripped)
			if !ok {
				continue
			}
			if !blob.HasTag() {
				if txtPad {
					for range int(entry.Count) {
						if !yield(index, "") {
							return
						}
						index++
					}
				}

				continue
			}
			for tag := range blob.allTagsFromEntry(entry) {
				if !yield(index, tag) {
					return
				}
				index++
			}
		}
	}
}

func (bs BlobSet) TimestampAt(metricID uint64, index int) (int64, bool) {
	if index < 0 {
		return 0, false
	}

	numTarget, numCollided := bs.numericIdentity.resolveID(metricID)
	txtTarget, txtCollided := bs.textIdentity.resolveID(metricID)

	curIdx := 0
	foundInNumeric := false

	for _, blob := range bs.numericBlobs {
		entry, ok := blob.index.resolveEntry(metricID, numTarget, numCollided)
		if !ok {
			continue
		}
		foundInNumeric = true
		length := entry.Count
		if curIdx+length > index {
			return blob.timestampAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	// Like MetricLen and AllTimestamps, a metric found in numeric members is
	// served by them alone; text members only answer numeric-absent metrics.
	if foundInNumeric {
		return 0, false
	}

	for _, blob := range bs.textBlobs {
		entry, ok := blob.index.resolveEntry(metricID, txtTarget, txtCollided)
		if !ok {
			continue
		}
		length := int(entry.Count)
		if curIdx+length > index {
			return blob.timestampAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	return 0, false
}

func (bs BlobSet) TimestampAtByName(metricName string, index int) (int64, bool) {
	if index < 0 {
		return 0, false
	}

	numSkipStripped := bs.numericIdentity.excludesStripped(metricName)
	txtSkipStripped := bs.textIdentity.excludesStripped(metricName)

	curIdx := 0
	foundInNumeric := false

	for _, blob := range bs.numericBlobs {
		entry, ok := blob.index.resolveEntryByName(metricName, numSkipStripped)
		if !ok {
			continue
		}
		foundInNumeric = true
		length := entry.Count
		if curIdx+length > index {
			return blob.timestampAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	// Like MetricLen and AllTimestamps, a metric found in numeric members is
	// served by them alone; text members only answer numeric-absent metrics.
	if foundInNumeric {
		return 0, false
	}

	for _, blob := range bs.textBlobs {
		entry, ok := blob.index.resolveEntryByName(metricName, txtSkipStripped)
		if !ok {
			continue
		}
		length := int(entry.Count)
		if curIdx+length > index {
			return blob.timestampAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	return 0, false
}

func (bs BlobSet) TagAt(metricID uint64, index int) (string, bool) {
	if index < 0 {
		return "", false
	}

	numTarget, numCollided := bs.numericIdentity.resolveID(metricID)
	txtTarget, txtCollided := bs.textIdentity.resolveID(metricID)

	curIdx := 0
	foundInNumeric := false

	// Try numeric blobs first (95% case)
	for _, blob := range bs.numericBlobs {
		entry, ok := blob.index.resolveEntry(metricID, numTarget, numCollided)
		if !ok {
			continue
		}
		foundInNumeric = true
		length := entry.Count
		if curIdx+length > index {
			if !blob.HasTag() {
				return "", true
			}

			return blob.tagAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	// Like MetricLen and AllTimestamps, a metric found in numeric members is
	// served by them alone; text members only answer numeric-absent metrics.
	if foundInNumeric {
		return "", false
	}

	for _, blob := range bs.textBlobs {
		entry, ok := blob.index.resolveEntry(metricID, txtTarget, txtCollided)
		if !ok {
			continue
		}
		length := int(entry.Count)
		if curIdx+length > index {
			if !blob.HasTag() {
				return "", true
			}

			return blob.tagAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	return "", false
}

func (bs BlobSet) TagAtByName(metricName string, index int) (string, bool) {
	if index < 0 {
		return "", false
	}

	numSkipStripped := bs.numericIdentity.excludesStripped(metricName)
	txtSkipStripped := bs.textIdentity.excludesStripped(metricName)

	curIdx := 0
	foundInNumeric := false

	// Try numeric blobs first (95% case)
	for _, blob := range bs.numericBlobs {
		entry, ok := blob.index.resolveEntryByName(metricName, numSkipStripped)
		if !ok {
			continue
		}
		foundInNumeric = true
		length := entry.Count
		if curIdx+length > index {
			if !blob.HasTag() {
				return "", true
			}

			return blob.tagAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	// Like MetricLen and AllTimestamps, a metric found in numeric members is
	// served by them alone; text members only answer numeric-absent metrics.
	if foundInNumeric {
		return "", false
	}

	for _, blob := range bs.textBlobs {
		entry, ok := blob.index.resolveEntryByName(metricName, txtSkipStripped)
		if !ok {
			continue
		}
		length := int(entry.Count)
		if curIdx+length > index {
			if !blob.HasTag() {
				return "", true
			}

			return blob.tagAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	return "", false
}

func (bs BlobSet) NumericValueAt(metricID uint64, index int) (float64, bool) {
	if index < 0 {
		return 0, false
	}

	targetName, collided := bs.numericIdentity.resolveID(metricID)

	curIdx := 0
	for _, blob := range bs.numericBlobs {
		entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
		if !ok {
			continue
		}
		length := entry.Count
		if curIdx+length > index {
			return blob.valueAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	return 0, false
}

func (bs BlobSet) NumericValueAtByName(metricName string, index int) (float64, bool) {
	if index < 0 {
		return 0, false
	}

	skipStripped := bs.numericIdentity.excludesStripped(metricName)

	curIdx := 0
	for _, blob := range bs.numericBlobs {
		entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
		if !ok {
			continue
		}
		length := entry.Count
		if curIdx+length > index {
			return blob.valueAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	return 0, false
}

func (bs BlobSet) TextValueAt(metricID uint64, index int) (string, bool) {
	if index < 0 {
		return "", false
	}

	targetName, collided := bs.textIdentity.resolveID(metricID)

	curIdx := 0
	for _, blob := range bs.textBlobs {
		entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
		if !ok {
			continue
		}
		length := int(entry.Count)
		if curIdx+length > index {
			return blob.valueAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	return "", false
}

func (bs BlobSet) TextValueAtByName(metricName string, index int) (string, bool) {
	if index < 0 {
		return "", false
	}

	skipStripped := bs.textIdentity.excludesStripped(metricName)

	curIdx := 0
	for _, blob := range bs.textBlobs {
		entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
		if !ok {
			continue
		}
		length := int(entry.Count)
		if curIdx+length > index {
			return blob.valueAtFromEntry(entry, index-curIdx)
		}
		curIdx += length
	}

	return "", false
}

func (bs BlobSet) NumericAt(metricID uint64, index int) (NumericDataPoint, bool) {
	if index < 0 {
		return NumericDataPoint{}, false
	}

	targetName, collided := bs.numericIdentity.resolveID(metricID)

	curIdx := 0
	for _, blob := range bs.numericBlobs {
		entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
		if !ok {
			continue
		}
		length := entry.Count
		if curIdx+length > index {
			return numericPointFromEntry(blob, entry, index-curIdx)
		}
		curIdx += length
	}

	return NumericDataPoint{}, false
}

func (bs BlobSet) NumericAtByName(metricName string, index int) (NumericDataPoint, bool) {
	if index < 0 {
		return NumericDataPoint{}, false
	}

	skipStripped := bs.numericIdentity.excludesStripped(metricName)

	curIdx := 0
	for _, blob := range bs.numericBlobs {
		entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
		if !ok {
			continue
		}
		length := entry.Count
		if curIdx+length > index {
			return numericPointFromEntry(blob, entry, index-curIdx)
		}
		curIdx += length
	}

	return NumericDataPoint{}, false
}

func (bs BlobSet) TextAt(metricID uint64, index int) (TextDataPoint, bool) {
	if index < 0 {
		return TextDataPoint{}, false
	}

	targetName, collided := bs.textIdentity.resolveID(metricID)

	curIdx := 0
	for _, blob := range bs.textBlobs {
		entry, ok := blob.index.resolveEntry(metricID, targetName, collided)
		if !ok {
			continue
		}
		length := int(entry.Count)
		if curIdx+length > index {
			return textPointFromEntry(blob, entry, index-curIdx)
		}
		curIdx += length
	}

	return TextDataPoint{}, false
}

func (bs BlobSet) TextAtByName(metricName string, index int) (TextDataPoint, bool) {
	if index < 0 {
		return TextDataPoint{}, false
	}

	skipStripped := bs.textIdentity.excludesStripped(metricName)

	curIdx := 0
	for _, blob := range bs.textBlobs {
		entry, ok := blob.index.resolveEntryByName(metricName, skipStripped)
		if !ok {
			continue
		}
		length := int(entry.Count)
		if curIdx+length > index {
			return textPointFromEntry(blob, entry, index-curIdx)
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
			byName: make(map[string]int),
			byID:   make(map[uint64]int),
		}
	}

	// Create NumericBlobSet and delegate to its Materialize()
	numericSet := &NumericBlobSet{blobs: bs.numericBlobs, identity: bs.numericIdentity}

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
			byName: make(map[string]int),
			byID:   make(map[uint64]int),
		}
	}

	// Create TextBlobSet and delegate to its Materialize()
	textSet := &TextBlobSet{blobs: bs.textBlobs, identity: bs.textIdentity}

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
	numericSet := &NumericBlobSet{blobs: bs.numericBlobs, identity: bs.numericIdentity}

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
	numericSet := &NumericBlobSet{blobs: bs.numericBlobs, identity: bs.numericIdentity}

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
	textSet := &TextBlobSet{blobs: bs.textBlobs, identity: bs.textIdentity}

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
	textSet := &TextBlobSet{blobs: bs.textBlobs, identity: bs.textIdentity}

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

// MetricLen returns the total number of data points for the given metric ID across all blobs.
//
// This method searches both numeric and text blobs, checking numeric blobs first.
// Once the metric is found in a blob type, it only sums up counts from that type.
//
// Parameters:
//   - metricID: The metric ID to query
//
// Returns:
//   - int: Total number of data points, or 0 if the metric doesn't exist in any blob
//
// Example:
//
//	blobSet := NewBlobSet(numericBlobs, textBlobs)
//	totalPoints := blobSet.MetricLen(metricID)
//	fmt.Printf("Metric has %d data points across all blobs\n", totalPoints)
func (bs BlobSet) MetricLen(metricID uint64) int {
	numTarget, numCollided := bs.numericIdentity.resolveID(metricID)
	txtTarget, txtCollided := bs.textIdentity.resolveID(metricID)

	totalLen := 0

	for i := range bs.numericBlobs {
		if entry, ok := bs.numericBlobs[i].index.resolveEntry(metricID, numTarget, numCollided); ok {
			totalLen += entry.Count
		}
	}

	if totalLen > 0 {
		return totalLen
	}

	for i := range bs.textBlobs {
		if entry, ok := bs.textBlobs[i].index.resolveEntry(metricID, txtTarget, txtCollided); ok {
			totalLen += int(entry.Count)
		}
	}

	return totalLen
}

// MetricLenByName returns the total number of data points for the given metric name across all blobs.
//
// This method searches both numeric and text blobs, checking numeric blobs first.
// Once the metric is found in a blob type, it only sums up counts from that type.
//
// Parameters:
//   - metricName: The metric name to query
//
// Returns:
//   - int: Total number of data points, or 0 if the metric doesn't exist in any blob
//
// Example:
//
//	blobSet := NewBlobSet(numericBlobs, textBlobs)
//	totalPoints := blobSet.MetricLenByName("cpu.usage")
//	fmt.Printf("Metric has %d data points across all blobs\n", totalPoints)
func (bs BlobSet) MetricLenByName(metricName string) int {
	numSkipStripped := bs.numericIdentity.excludesStripped(metricName)
	txtSkipStripped := bs.textIdentity.excludesStripped(metricName)

	totalLen := 0

	for i := range bs.numericBlobs {
		if entry, ok := bs.numericBlobs[i].index.resolveEntryByName(metricName, numSkipStripped); ok {
			totalLen += entry.Count
		}
	}

	if totalLen > 0 {
		return totalLen
	}

	for i := range bs.textBlobs {
		if entry, ok := bs.textBlobs[i].index.resolveEntryByName(metricName, txtSkipStripped); ok {
			totalLen += int(entry.Count)
		}
	}

	return totalLen
}

// IsNumericMetric checks if the given metric ID exists in any numeric blob.
//
// Parameters:
//   - metricID: The metric ID to check
//
// Returns:
//   - bool: true if the metric exists in at least one numeric blob, false otherwise
//
// Example:
//
//	if blobSet.IsNumericMetric(metricID) {
//	    // Process as numeric metric
//	}
func (bs BlobSet) IsNumericMetric(metricID uint64) bool {
	for i := range bs.numericBlobs {
		if bs.numericBlobs[i].HasMetricID(metricID) {
			return true
		}
	}

	return false
}

// IsNumericMetricByName checks if the given metric name exists in any numeric blob.
//
// Parameters:
//   - metricName: The metric name to check
//
// Returns:
//   - bool: true if the metric exists in at least one numeric blob, false otherwise
//
// Example:
//
//	if blobSet.IsNumericMetricByName("cpu.usage") {
//	    // Process as numeric metric
//	}
func (bs BlobSet) IsNumericMetricByName(metricName string) bool {
	for i := range bs.numericBlobs {
		if bs.numericBlobs[i].HasMetricName(metricName) {
			return true
		}
	}

	return false
}

// IsTextMetric checks if the given metric ID exists in any text blob.
//
// Parameters:
//   - metricID: The metric ID to check
//
// Returns:
//   - bool: true if the metric exists in at least one text blob, false otherwise
//
// Example:
//
//	if blobSet.IsTextMetric(metricID) {
//	    // Process as text metric
//	}
func (bs BlobSet) IsTextMetric(metricID uint64) bool {
	for i := range bs.textBlobs {
		if bs.textBlobs[i].HasMetricID(metricID) {
			return true
		}
	}

	return false
}

// IsTextMetricByName checks if the given metric name exists in any text blob.
//
// Parameters:
//   - metricName: The metric name to check
//
// Returns:
//   - bool: true if the metric exists in at least one text blob, false otherwise
//
// Example:
//
//	if blobSet.IsTextMetricByName("log.message") {
//	    // Process as text metric
//	}
func (bs BlobSet) IsTextMetricByName(metricName string) bool {
	for i := range bs.textBlobs {
		if bs.textBlobs[i].HasMetricName(metricName) {
			return true
		}
	}

	return false
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
func (bs BlobSet) MetricDuration(metricID uint64) int64 {
	// A collided ID resolves per concrete type to the first colliding name's logical
	// metric; name-keyed duration walks exactly that metric's members.
	// targetName IS the first colliding name, so a stripped member correctly
	// contributes and needs no filter.
	// A metric found in numeric members is served by them alone, even when its duration is 0.
	var duration int64
	var found bool
	if numTarget, numCollided := bs.numericIdentity.resolveID(metricID); numCollided {
		duration, found = calculateDurationByName(bs.numericBlobs, numTarget, nil)
	} else {
		duration, found = calculateDuration(bs.numericBlobs, metricID)
	}
	if found {
		return duration
	}

	if txtTarget, txtCollided := bs.textIdentity.resolveID(metricID); txtCollided {
		duration, _ = calculateDurationByName(bs.textBlobs, txtTarget, nil)
	} else {
		duration, _ = calculateDuration(bs.textBlobs, metricID)
	}

	return duration
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
//	duration := blobSet.MetricDurationByName("cpu.usage")
//	fmt.Printf("Metric spans %d timestamp units\n", duration)
func (bs BlobSet) MetricDurationByName(metricName string) int64 {
	// A stripped member's ID hash-matches every colliding name; when metricName is not
	// the first colliding name, those members belong to the other logical metric.
	var numFilter func(i int) bool
	if bs.numericIdentity.excludesStripped(metricName) {
		numFilter = func(i int) bool { return bs.numericBlobs[i].index.names != nil }
	}
	// A metric found in numeric members is served by them alone, even when its duration is 0.
	if duration, found := calculateDurationByName(bs.numericBlobs, metricName, numFilter); found {
		return duration
	}

	var txtFilter func(i int) bool
	if bs.textIdentity.excludesStripped(metricName) {
		txtFilter = func(i int) bool { return bs.textBlobs[i].index.names != nil }
	}
	duration, _ := calculateDurationByName(bs.textBlobs, metricName, txtFilter)

	return duration
}

// numericPointFromEntry / textPointFromEntry assemble a full data point from a member's
// resolved index entry. They mirror the ByID/ByName point accessors they replaced,
// including TagAt's contract that a tagless blob still reports a valid empty tag for an
// in-range index.
func numericPointFromEntry(blob NumericBlob, entry section.NumericIndexEntry, index int) (NumericDataPoint, bool) {
	ts, tsOk := blob.timestampAtFromEntry(entry, index)
	val, valOk := blob.valueAtFromEntry(entry, index)
	tag, tagOk := "", index >= 0 && index < entry.Count
	if tagOk && blob.HasTag() {
		tag, tagOk = blob.tagAtFromEntry(entry, index)
	}
	if tsOk && valOk && tagOk {
		return NumericDataPoint{Ts: ts, Val: val, Tag: tag}, true
	}

	return NumericDataPoint{}, false
}

func textPointFromEntry(blob TextBlob, entry section.TextIndexEntry, index int) (TextDataPoint, bool) {
	ts, tsOk := blob.timestampAtFromEntry(entry, index)
	val, valOk := blob.valueAtFromEntry(entry, index)
	tag, tagOk := "", index >= 0 && index < int(entry.Count)
	if tagOk && blob.HasTag() {
		tag, tagOk = blob.tagAtFromEntry(entry, index)
	}
	if tsOk && valOk && tagOk {
		return TextDataPoint{Ts: ts, Val: val, Tag: tag}, true
	}

	return TextDataPoint{}, false
}

// blobAccessor defines the interface for accessing blob metadata and timestamps.
// This interface enables generic duration calculation without performance overhead.
type blobAccessor[T any] interface {
	HasMetricID(metricID uint64) bool
	HasMetricName(metricName string) bool
	Len(metricID uint64) int
	LenByName(metricName string) int
	TimestampAt(metricID uint64, index int) (int64, bool)
	TimestampAtByName(metricName string, index int) (int64, bool)
}

// calculateDuration is a generic helper that calculates metric duration across a slice of blobs.
// It also reports whether any blob holds the metric, so callers need no separate lookup pass.
// This function is inlined by the compiler for zero overhead compared to duplicated code.
//
// Performance: Optimized bi-directional search - finds first from start, last from end.
// Average case: O(n/2) when metric is in middle blobs. Best case: O(2) when in first and last.
func calculateDuration[T blobAccessor[T]](blobs []T, metricID uint64) (int64, bool) {
	if len(blobs) == 0 {
		return 0, false
	}

	// Find first blob containing the metric (forward search)
	firstIdx := -1
	for i := range blobs {
		if blobs[i].HasMetricID(metricID) {
			firstIdx = i
			break // Stop as soon as we find the first
		}
	}

	if firstIdx == -1 {
		return 0, false // Metric not found
	}

	// Find last blob containing the metric (reverse search)
	lastIdx := firstIdx // Default to first if it's the only one
	for i := len(blobs) - 1; i > firstIdx; i-- {
		if blobs[i].HasMetricID(metricID) {
			lastIdx = i
			break // Stop as soon as we find the last
		}
	}

	// Get first timestamp from first blob
	firstTimestamp, ok := blobs[firstIdx].TimestampAt(metricID, 0)
	if !ok {
		return 0, true
	}

	// Get last timestamp from last blob
	lastBlobLen := blobs[lastIdx].Len(metricID)
	if lastBlobLen == 0 {
		return 0, true
	}

	lastTimestamp, ok := blobs[lastIdx].TimestampAt(metricID, lastBlobLen-1)
	if !ok {
		return 0, true
	}

	if lastTimestamp > firstTimestamp {
		return lastTimestamp - firstTimestamp, true
	}

	return 0, true
}

// calculateDurationByName is a generic helper that calculates metric duration by name across a slice of blobs.
// It also reports whether any contributing blob holds the metric.
// This function is inlined by the compiler for zero overhead compared to duplicated code.
//
// Performance: Optimized bi-directional search - finds first from start, last from end.
// Average case: O(n/2) when metric is in middle blobs. Best case: O(2) when in first and last.
func calculateDurationByName[T blobAccessor[T]](blobs []T, metricName string, contributes func(i int) bool) (int64, bool) {
	if len(blobs) == 0 {
		return 0, false
	}

	// Find first blob containing the metric (forward search)
	firstIdx := -1
	for i := range blobs {
		if contributes != nil && !contributes(i) {
			continue
		}
		if blobs[i].HasMetricName(metricName) {
			firstIdx = i
			break // Stop as soon as we find the first
		}
	}

	if firstIdx == -1 {
		return 0, false // Metric not found
	}

	// Find last blob containing the metric (reverse search)
	lastIdx := firstIdx // Default to first if it's the only one
	for i := len(blobs) - 1; i > firstIdx; i-- {
		if contributes != nil && !contributes(i) {
			continue
		}
		if blobs[i].HasMetricName(metricName) {
			lastIdx = i
			break // Stop as soon as we find the last
		}
	}

	// Get first timestamp from first blob
	firstTimestamp, ok := blobs[firstIdx].TimestampAtByName(metricName, 0)
	if !ok {
		return 0, true
	}

	// Get last timestamp from last blob
	lastBlobLen := blobs[lastIdx].LenByName(metricName)
	if lastBlobLen == 0 {
		return 0, true
	}

	lastTimestamp, ok := blobs[lastIdx].TimestampAtByName(metricName, lastBlobLen-1)
	if !ok {
		return 0, true
	}

	if lastTimestamp > firstTimestamp {
		return lastTimestamp - firstTimestamp, true
	}

	return 0, true
}
