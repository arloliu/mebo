package blob

import (
	"errors"
	"fmt"
	"iter"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/section"
)

// TextDataPoint represents a single data point with timestamp, text value, and optional tag.
// The TextBlob methods return iterators of these data points.
type TextDataPoint struct {
	// Ts is the timestamp, the unit is defined by the caller when adding data points in TextEncoder
	Ts int64
	// Val is the text value (max 255 characters)
	Val string
	// Tag is the optional tag associated with this data point (max 255 characters)
	Tag string
}

// TextBlob represents a decoded blob of text values with associated timestamps and optional tags.
type TextBlob struct {
	blobBase                                      // Embedded base: engine, startTime, tsEncType, sameByteOrder, flags
	index       indexMaps[section.TextIndexEntry] // Metric ID/name → IndexEntry mappings
	dataPayload []byte                            // Single decompressed data section (row-based)
	// flag is now packed into blobBase.flags (optimized)
}

var _ BlobReader = TextBlob{}

// IsNumeric returns true if it's a numeric blob.
func (b TextBlob) IsNumeric() bool {
	return false
}

// IsText returns true if it's a text blob.
func (b TextBlob) IsText() bool {
	return true
}

// AsNumeric attempts to cast to NumericBlob, returns false if not numeric.
func (b TextBlob) AsNumeric() (NumericBlob, bool) {
	return NumericBlob{}, false
}

// AsText attempts to cast to TextBlob, returns false if not text.
func (b TextBlob) AsText() (TextBlob, bool) {
	return b, true
}

// MetricCount returns the number of metrics in the blob.
func (b TextBlob) MetricCount() int {
	return b.index.MetricCount()
}

// HasMetricID checks if the blob contains the given metric ID.
func (b TextBlob) HasMetricID(metricID uint64) bool {
	return b.index.HasMetricID(metricID)
}

// HasMetricName checks if the blob contains the given metric name.
// Returns false if the blob doesn't have metric names payload.
func (b TextBlob) HasMetricName(metricName string) bool {
	return b.index.HasMetricName(metricName)
}

// MetricIDs returns a slice of all metric IDs in the blob.
func (b TextBlob) MetricIDs() []uint64 {
	return b.index.MetricIDs()
}

// MetricNames returns a slice of all metric names in the blob.
// Returns empty slice if the blob doesn't have metric names payload.
func (b TextBlob) MetricNames() []string {
	return b.index.MetricNames()
}

// All returns an iterator over all data points for the given metric ID.
// Returns an empty iterator if the metric ID doesn't exist.
//
// Example:
//
//	for i, dp := range blob.All(metricID) {
//	    fmt.Printf("Point %d: ts=%d, val=%s, tag=%s\n", i, dp.Ts, dp.Val, dp.Tag)
//	}
func (b TextBlob) All(metricID uint64) iter.Seq2[int, TextDataPoint] {
	entry, ok := b.index.byID[metricID]
	if !ok {
		return func(yield func(int, TextDataPoint) bool) {}
	}

	return b.allFromEntry(entry)
}

// AllByName returns an iterator over all data points for the given metric name.
// Returns an empty iterator if the metric name doesn't exist or the blob has no metric names.
//
// Example:
//
//	for i, dp := range blob.AllByName("cpu.usage") {
//	    fmt.Printf("Point %d: ts=%d, val=%s\n", i, dp.Ts, dp.Val)
//	}
func (b TextBlob) AllByName(metricName string) iter.Seq2[int, TextDataPoint] {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return func(yield func(int, TextDataPoint) bool) {}
	}

	return b.allFromEntry(entry)
}

// AllTimestamps returns an iterator over all timestamps for the given metric ID.
// Returns an empty iterator if the metric ID doesn't exist.
func (b TextBlob) AllTimestamps(metricID uint64) iter.Seq[int64] {
	entry, ok := b.index.byID[metricID]
	if !ok {
		return func(yield func(int64) bool) {}
	}

	return b.allTimestampsFromEntry(entry)
}

// AllTimestampsByName returns an iterator over all timestamps for the given metric name.
// Returns an empty iterator if the metric name doesn't exist or the blob has no metric names.
func (b TextBlob) AllTimestampsByName(metricName string) iter.Seq[int64] {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return func(yield func(int64) bool) {}
	}

	return b.allTimestampsFromEntry(entry)
}

// AllValues returns an iterator over all text values for the given metric ID.
// Returns an empty iterator if the metric ID doesn't exist.
func (b TextBlob) AllValues(metricID uint64) iter.Seq[string] {
	entry, ok := b.index.byID[metricID]
	if !ok {
		return func(yield func(string) bool) {}
	}

	return b.allValuesFromEntry(entry)
}

// AllValuesByName returns an iterator over all text values for the given metric name.
// Returns an empty iterator if the metric name doesn't exist or the blob has no metric names.
func (b TextBlob) AllValuesByName(metricName string) iter.Seq[string] {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return func(yield func(string) bool) {}
	}

	return b.allValuesFromEntry(entry)
}

// AllTags returns an iterator over all tags for the given metric ID.
// Returns an empty iterator if tags are not enabled or the metric ID doesn't exist.
func (b TextBlob) AllTags(metricID uint64) iter.Seq[string] {
	if !b.HasTag() {
		return func(yield func(string) bool) {}
	}

	entry, ok := b.index.byID[metricID]
	if !ok {
		return func(yield func(string) bool) {}
	}

	return b.allTagsFromEntry(entry)
}

// AllTagsByName returns an iterator over all tags for the given metric name.
// Returns an empty iterator if tags are not enabled, the metric name doesn't exist,
// or the blob has no metric names.
func (b TextBlob) AllTagsByName(metricName string) iter.Seq[string] {
	if !b.HasTag() {
		return func(yield func(string) bool) {}
	}

	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return func(yield func(string) bool) {}
	}

	return b.allTagsFromEntry(entry)
}

// Len returns the number of data points for the given metric ID.
// Returns 0 if the metric ID doesn't exist.
func (b TextBlob) Len(metricID uint64) int {
	return b.index.Len(metricID)
}

// LenByName returns the number of data points for the given metric name.
// Returns 0 if the metric name doesn't exist.
func (b TextBlob) LenByName(metricName string) int {
	return b.index.LenByName(metricName)
}

// ValueAt returns the value at the specified index for the given metric ID.
// The index is 0-based within this blob.
//
// Returns (value, true) if successful, or ("", false) if:
//   - The metric doesn't exist in this blob
//   - The index is out of bounds
//
// Performance: O(n) where n is the index, as we need to skip through row-based data.
// For frequent random access, consider using iterators instead.
func (b TextBlob) ValueAt(metricID uint64, index int) (string, bool) {
	entry, ok := b.index.byID[metricID]
	if !ok {
		return "", false
	}

	return b.valueAtFromEntry(entry, index)
}

// ValueAtByName returns the value at the specified index for the given metric name.
// The index is 0-based within this blob.
//
// Returns (value, true) if successful, or ("", false) if:
//   - The metric name doesn't exist in this blob
//   - The index is out of bounds
//
// Performance: O(n) where n is the index, as we need to skip through row-based data.
// For frequent random access, consider using iterators instead.
func (b TextBlob) ValueAtByName(metricName string, index int) (string, bool) {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return "", false
	}

	return b.valueAtFromEntry(entry, index)
}

// TimestampAt returns the timestamp at the specified index for the given metric ID.
// The index is 0-based within this blob.
//
// Returns (timestamp, true) if successful, or (0, false) if:
//   - The metric doesn't exist in this blob
//   - The index is out of bounds
//
// Performance: O(n) where n is the index, as we need to skip through row-based data.
// For frequent random access, consider using iterators instead.
func (b TextBlob) TimestampAt(metricID uint64, index int) (int64, bool) {
	entry, ok := b.index.byID[metricID]
	if !ok {
		return 0, false
	}

	return b.timestampAtFromEntry(entry, index)
}

// TimestampAtByName returns the timestamp at the specified index for the given metric name.
// The index is 0-based within this blob.
//
// Returns (timestamp, true) if successful, or (0, false) if:
//   - The metric name doesn't exist in this blob
//   - The index is out of bounds
//
// Performance: O(n) where n is the index, as we need to skip through row-based data.
// For frequent random access, consider using iterators instead.
func (b TextBlob) TimestampAtByName(metricName string, index int) (int64, bool) {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return 0, false
	}

	return b.timestampAtFromEntry(entry, index)
}

// TagAt returns the tag at the specified index for the given metric ID.
// The index is 0-based within this blob.
//
// Returns (tag, true) if successful, or ("", false) if:
//   - The metric doesn't exist in this blob
//   - The index is out of bounds
//   - Tags are not enabled for this blob
//
// Performance: O(n) where n is the index, as we need to skip through row-based data.
// For frequent random access, consider using iterators instead.
func (b TextBlob) TagAt(metricID uint64, index int) (string, bool) {
	if !b.HasTag() {
		return "", false
	}

	entry, ok := b.index.byID[metricID]
	if !ok {
		return "", false
	}

	return b.tagAtFromEntry(entry, index)
}

// TagAtByName returns the tag at the specified index for the given metric name.
// The index is 0-based within this blob.
//
// Returns (tag, true) if successful, or ("", false) if:
//   - The metric name doesn't exist in this blob
//   - The index is out of bounds
//   - Tags are not enabled for this blob
//
// Performance: O(n) where n is the index, as we need to skip through row-based data.
// For frequent random access, consider using iterators instead.
func (b TextBlob) TagAtByName(metricName string, index int) (string, bool) {
	if !b.HasTag() {
		return "", false
	}

	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return "", false
	}

	return b.tagAtFromEntry(entry, index)
}

// lookupMetricEntry returns the index entry for a metric name.
// Returns (entry, true) if found, or (zero-value, false) if not found.
func (b TextBlob) lookupMetricEntry(metricName string) (section.TextIndexEntry, bool) {
	return b.index.GetByName(metricName)
}

// Internal helper methods that work with TextIndexEntry directly.
// These eliminate duplication between ByID and ByName methods.

// allFromEntry returns an iterator over (index, TextDataPoint) for the given entry.
func (b TextBlob) allFromEntry(entry section.TextIndexEntry) iter.Seq2[int, TextDataPoint] {
	count := int(entry.Count)
	if count == 0 {
		return func(yield func(int, TextDataPoint) bool) {}
	}

	offset := int(entry.Offset)
	size := int(entry.Size)
	dataBytes := b.dataPayload[offset : offset+size]

	return b.decodeDataPoints(dataBytes, count)
}

// allTimestampsFromEntry returns an iterator over all timestamps for the given entry.
func (b TextBlob) allTimestampsFromEntry(entry section.TextIndexEntry) iter.Seq[int64] {
	count := int(entry.Count)
	if count == 0 {
		return func(yield func(int64) bool) {}
	}

	offset := int(entry.Offset)
	size := int(entry.Size)
	dataBytes := b.dataPayload[offset : offset+size]

	return b.decodeTimestamps(dataBytes, count)
}

// allValuesFromEntry returns an iterator over all values for the given entry.
func (b TextBlob) allValuesFromEntry(entry section.TextIndexEntry) iter.Seq[string] {
	count := int(entry.Count)
	if count == 0 {
		return func(yield func(string) bool) {}
	}

	offset := int(entry.Offset)
	size := int(entry.Size)
	dataBytes := b.dataPayload[offset : offset+size]

	return b.decodeValues(dataBytes, count)
}

// allTagsFromEntry returns an iterator over all tags for the given entry.
func (b TextBlob) allTagsFromEntry(entry section.TextIndexEntry) iter.Seq[string] {
	count := int(entry.Count)
	if count == 0 {
		return func(yield func(string) bool) {}
	}

	offset := int(entry.Offset)
	size := int(entry.Size)
	dataBytes := b.dataPayload[offset : offset+size]

	return b.decodeTags(dataBytes, count)
}

// valueAtFromEntry returns the value at the specified index for the given entry.
func (b TextBlob) valueAtFromEntry(entry section.TextIndexEntry, index int) (string, bool) {
	count := int(entry.Count)
	if index < 0 || index >= count {
		return "", false
	}

	offset := int(entry.Offset)
	size := int(entry.Size)
	dataBytes := b.dataPayload[offset : offset+size]

	// Skip to the target index
	currentOffset := 0
	lastTs := b.startTimeMicros
	hasTags := b.HasTag()

	for i := range count {
		// Decode and skip timestamp
		_, n, err := b.decodeTimestampAt(dataBytes, currentOffset, &lastTs)
		if err != nil {
			return "", false
		}
		currentOffset += n

		// NEW LAYOUT: Read grouped length bytes first
		// Layout is now [LEN_V][LEN_T][VAL][TAG] instead of [LEN_V][VAL][LEN_T][TAG]
		if currentOffset >= len(dataBytes) {
			return "", false
		}
		lenV := int(dataBytes[currentOffset])
		currentOffset++

		lenT := 0
		if hasTags {
			if currentOffset >= len(dataBytes) {
				return "", false
			}
			lenT = int(dataBytes[currentOffset])
			currentOffset++
		}

		// If this is our target index, read and return the value
		if i == index {
			if currentOffset+lenV > len(dataBytes) {
				return "", false
			}
			val := string(dataBytes[currentOffset : currentOffset+lenV])

			return val, true
		}

		// Skip both value and tag data
		currentOffset += lenV + lenT
	}

	return "", false
}

// timestampAtFromEntry returns the timestamp at the specified index for the given entry.
func (b TextBlob) timestampAtFromEntry(entry section.TextIndexEntry, index int) (int64, bool) {
	count := int(entry.Count)
	if index < 0 || index >= count {
		return 0, false
	}

	offset := int(entry.Offset)
	size := int(entry.Size)
	dataBytes := b.dataPayload[offset : offset+size]

	// Skip to the target index
	currentOffset := 0
	lastTs := b.startTimeMicros
	hasTags := b.HasTag()

	for i := range count {
		// Decode timestamp
		ts, n, err := b.decodeTimestampAt(dataBytes, currentOffset, &lastTs)
		if err != nil {
			return 0, false
		}
		currentOffset += n

		// If this is our target index, return the timestamp
		if i == index {
			return ts, true
		}

		// NEW LAYOUT: Read grouped length bytes
		if currentOffset >= len(dataBytes) {
			return 0, false
		}
		lenV := int(dataBytes[currentOffset])
		currentOffset++

		lenT := 0
		if hasTags {
			if currentOffset >= len(dataBytes) {
				return 0, false
			}
			lenT = int(dataBytes[currentOffset])
			currentOffset++
		}

		// Skip both value and tag data
		currentOffset += lenV + lenT
	}

	return 0, false
}

// tagAtFromEntry returns the tag at the specified index for the given entry.
func (b TextBlob) tagAtFromEntry(entry section.TextIndexEntry, index int) (string, bool) {
	count := int(entry.Count)
	if index < 0 || index >= count {
		return "", false
	}

	offset := int(entry.Offset)
	size := int(entry.Size)
	dataBytes := b.dataPayload[offset : offset+size]

	// Skip to the target index
	currentOffset := 0
	lastTs := b.startTimeMicros

	for i := range count {
		// Skip timestamp
		_, n, err := b.decodeTimestampAt(dataBytes, currentOffset, &lastTs)
		if err != nil {
			return "", false
		}
		currentOffset += n

		// NEW LAYOUT: Read grouped length bytes
		if currentOffset+1 >= len(dataBytes) {
			return "", false
		}
		lenV := int(dataBytes[currentOffset])
		lenT := int(dataBytes[currentOffset+1])
		currentOffset += 2

		// Skip value data
		currentOffset += lenV

		// If this is our target index, read and return the tag
		if i == index {
			if currentOffset+lenT > len(dataBytes) {
				return "", false
			}
			tag := string(dataBytes[currentOffset : currentOffset+lenT])

			return tag, true
		}

		// Skip tag data
		currentOffset += lenT
	}

	return "", false
}

// decodeDataPoints decodes a data section and returns an iterator over all data points.
// The data section contains interleaved timestamps, values, and optional tags.
func (b TextBlob) decodeDataPoints(dataBytes []byte, count int) iter.Seq2[int, TextDataPoint] {
	return func(yield func(int, TextDataPoint) bool) {
		offset := 0
		// Initialize lastTs to blob start time for delta encoding
		// For raw encoding, this value is not used
		lastTs := b.startTimeMicros

		for i := range count {
			// Decode timestamp
			ts, n, err := b.decodeTimestampAt(dataBytes, offset, &lastTs)
			if err != nil {
				return // Stop iteration on error
			}
			offset += n

			// NEW LAYOUT: Read grouped length bytes first
			if offset >= len(dataBytes) {
				return
			}
			lenV := int(dataBytes[offset])
			offset++

			lenT := 0
			if b.HasTag() {
				if offset >= len(dataBytes) {
					return
				}
				lenT = int(dataBytes[offset])
				offset++
			}

			// Read value
			if offset+lenV > len(dataBytes) {
				return
			}
			val := string(dataBytes[offset : offset+lenV])
			offset += lenV

			// Read tag if enabled
			var tag string
			if b.HasTag() {
				if offset+lenT > len(dataBytes) {
					return
				}
				tag = string(dataBytes[offset : offset+lenT])
				offset += lenT
			}

			if !yield(i, TextDataPoint{Ts: ts, Val: val, Tag: tag}) {
				return
			}
		}
	}
}

// decodeTimestamps decodes only timestamps from the data section.
func (b TextBlob) decodeTimestamps(dataBytes []byte, count int) iter.Seq[int64] {
	return func(yield func(int64) bool) {
		offset := 0
		// Initialize lastTs to blob start time for delta encoding
		lastTs := b.startTimeMicros

		for range count {
			// Decode timestamp
			ts, n, err := b.decodeTimestampAt(dataBytes, offset, &lastTs)
			if err != nil {
				return
			}
			offset += n

			// NEW LAYOUT: Read grouped length bytes
			if offset >= len(dataBytes) {
				return
			}
			lenV := int(dataBytes[offset])
			offset++

			lenT := 0
			if b.HasTag() {
				if offset >= len(dataBytes) {
					return
				}
				lenT = int(dataBytes[offset])
				offset++
			}

			// Skip both value and tag data
			offset += lenV + lenT

			if !yield(ts) {
				return
			}
		}
	}
}

// decodeValues decodes only values from the data section.
func (b TextBlob) decodeValues(dataBytes []byte, count int) iter.Seq[string] {
	return func(yield func(string) bool) {
		offset := 0
		// Initialize lastTs to blob start time for delta encoding
		lastTs := b.startTimeMicros

		for range count {
			// Skip timestamp
			_, n, err := b.decodeTimestampAt(dataBytes, offset, &lastTs)
			if err != nil {
				return
			}
			offset += n

			// NEW LAYOUT: Read grouped length bytes
			if offset >= len(dataBytes) {
				return
			}
			lenV := int(dataBytes[offset])
			offset++

			lenT := 0
			if b.HasTag() {
				if offset >= len(dataBytes) {
					return
				}
				lenT = int(dataBytes[offset])
				offset++
			}

			// Read value
			if offset+lenV > len(dataBytes) {
				return
			}
			val := string(dataBytes[offset : offset+lenV])
			offset += lenV

			// Skip tag data
			offset += lenT

			if !yield(val) {
				return
			}
		}
	}
}

// decodeTags decodes only tags from the data section.
func (b TextBlob) decodeTags(dataBytes []byte, count int) iter.Seq[string] {
	return func(yield func(string) bool) {
		offset := 0
		// Initialize lastTs to blob start time for delta encoding
		lastTs := b.startTimeMicros

		for range count {
			// Skip timestamp
			_, n, err := b.decodeTimestampAt(dataBytes, offset, &lastTs)
			if err != nil {
				return
			}
			offset += n

			// NEW LAYOUT: Read grouped length bytes
			if offset+1 >= len(dataBytes) {
				return
			}
			lenV := int(dataBytes[offset])
			lenT := int(dataBytes[offset+1])
			offset += 2

			// Skip value data
			offset += lenV

			// Read tag
			if offset+lenT > len(dataBytes) {
				return
			}
			tag := string(dataBytes[offset : offset+lenT])
			offset += lenT

			if !yield(tag) {
				return
			}
		}
	}
}

// decodeTimestampAt decodes a single timestamp at the given offset.
// Returns the timestamp, bytes consumed, and any error.
// Updates lastTs for delta encoding.
func (b TextBlob) decodeTimestampAt(data []byte, offset int, lastTs *int64) (int64, int, error) {
	switch b.tsEncType { //nolint: exhaustive
	case format.TypeDelta:
		// Delta encoding: read varint delta from previous timestamp
		// First data point: delta from blob start time
		// Subsequent data points: delta from previous timestamp
		delta, n := decodeVarint(data[offset:])

		var ts int64
		if *lastTs == 0 {
			// First data point: add delta to blob start time
			ts = b.startTimeMicros + delta
		} else {
			// Subsequent data points: add delta to previous timestamp
			ts = *lastTs + delta
		}
		*lastTs = ts

		return ts, n, nil

	case format.TypeRaw:
		// Raw encoding: timestamps are written as length-prefixed strings containing 8 bytes
		// First read the length prefix
		if len(data[offset:]) < 1 {
			return 0, 0, errors.New("insufficient data for timestamp length prefix")
		}
		length := int(data[offset])
		offset++

		// Read the timestamp bytes
		if len(data[offset:]) < length {
			return 0, 0, errors.New("insufficient data for raw timestamp")
		}
		if length != 8 {
			return 0, 0, fmt.Errorf("invalid timestamp length: expected 8, got %d", length)
		}

		ts := int64(b.Engine().Uint64(data[offset : offset+8])) //nolint:gosec
		*lastTs = ts

		return ts, 1 + length, nil // 1 byte for length prefix + 8 bytes for timestamp

	default:
		return 0, 0, fmt.Errorf("unsupported timestamp encoding: %v", b.tsEncType)
	}
}

// decodeVarint decodes a varint from the byte slice and returns the value and bytes consumed.
func decodeVarint(data []byte) (int64, int) {
	var uval uint64
	var shift uint
	var n int

	for {
		if n >= len(data) {
			return 0, 0
		}

		b := data[n]
		n++

		uval |= uint64(b&0x7f) << shift
		if b < 0x80 {
			break
		}
		shift += 7
	}

	// Zigzag decoding: converts unsigned back to signed
	val := int64(uval>>1) ^ -int64(uval&1) //nolint:gosec

	return val, n
}
