package blob

import (
	"iter"
	"time"

	"github.com/arloliu/mebo/encoding"
	"github.com/arloliu/mebo/format"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/section"
)

// NumericDataPoint represents a single data point with timestamp, value, and optional tag.
// The NumericBlob methods return iterators of these data points.
type NumericDataPoint struct {
	// Ts is the timestamp, the unit is defined by the caller when adding data points in NumericEncoder
	Ts int64
	// Val is the float value
	Val float64
	// Tag is the optional tag associated with this data point
	Tag string
}

// NumericBlob represents a decoded blob of float values with associated timestamps and optional tags.
type NumericBlob struct {
	blobBase                                        // Embedded base: engine, startTime, tsEncType, sameByteOrder, flags
	index      indexMaps[section.NumericIndexEntry] // Metric ID/name → IndexEntry mappings
	tsPayload  []byte
	valPayload []byte
	tagPayload []byte
}

var _ BlobReader = NumericBlob{}

// IsNumeric returns true if it's a numeric blob.
func (b NumericBlob) IsNumeric() bool {
	return true
}

// IsText returns true if it's a text blob.
func (b NumericBlob) IsText() bool {
	return false
}

// AsNumeric attempts to cast to NumericBlob, returns false if not numeric.
func (b NumericBlob) AsNumeric() (NumericBlob, bool) {
	return b, true
}

// AsText attempts to cast to TextBlob, returns false if not text.
func (b NumericBlob) AsText() (TextBlob, bool) {
	return TextBlob{}, false
}

// StartTime returns the start time of the blob.
func (b NumericBlob) StartTime() time.Time {
	if b.startTimeMicros == 0 && len(b.index.byID) == 0 {
		return time.Time{}
	}

	return time.UnixMicro(b.startTimeMicros).UTC()
}

// MetricCount returns the number of metrics in the blob.
func (b NumericBlob) MetricCount() int {
	return b.index.MetricCount()
}

// HasMetricID checks if the blob contains the given metric ID.
func (b NumericBlob) HasMetricID(metricID uint64) bool {
	return b.index.HasMetricID(metricID)
}

// HasMetricName checks if the blob contains the given metric name.
// Returns false if the blob doesn't have metric names payload.
func (b NumericBlob) HasMetricName(metricName string) bool {
	return b.index.HasMetricName(metricName)
}

// MetricIDs returns a slice of all metric IDs in the blob.
func (b NumericBlob) MetricIDs() []uint64 {
	return b.index.MetricIDs()
}

// MetricNames returns a slice of all metric names in the blob.
// Returns empty slice if the blob doesn't have metric names payload.
func (b NumericBlob) MetricNames() []string {
	return b.index.MetricNames()
}

// Len returns the number of data points for the given metric ID.
// If the metric ID does not exist, it returns 0.
//
// It is useful for calculating the index cross multiple blobs without
// needing to decode all timestamps or values.
func (b NumericBlob) Len(metricID uint64) int {
	return b.index.Len(metricID)
}

// LenByName returns the number of data points for the given metric name.
//
// Behavior:
//   - If no hash collisions occurred (HasMetricNames() == false): automatically hashes the metric name
//     and falls back to Len(metricID).
//   - If hash collisions occurred (HasMetricNames() == true): uses the metric name map for reliable lookup.
//
// Returns 0 if the metric name is not found.
func (b NumericBlob) LenByName(metricName string) int {
	return b.index.LenByName(metricName)
}

// All returns an iterator over (index, NumericDataPoint) for the given metric ID.
// The index starts from 0 and increments for each data point.
// NumericDataPoint contains timestamp, value, and optional tag.
//
// This is the most convenient way(but costly) to retrieve complete data points with their indices.
// If you only need timestamps, values, or tags individually, use the specific methods
// (AllTimestamps, AllValues, AllTags) for better performance.
//
// Example:
//
//	for idx, dp := range blob.All(metricID) {
//	    fmt.Printf("[%d] ts=%d, val=%f, tag=%s\n", idx, dp.Ts, dp.Val, dp.Tag)
//	}
func (b NumericBlob) All(metricID uint64) iter.Seq2[int, NumericDataPoint] {
	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return func(yield func(int, NumericDataPoint) bool) {}
	}

	return b.allFromEntry(entry)
}

// AllByName returns a sequence of (index, NumericDataPoint) for the given metric name.
//
// Example:
//
//	for idx, dp := range blob.AllByName("cpu.usage") {
//	    fmt.Printf("[%d] ts=%d, val=%f\n", idx, dp.Ts, dp.Val)
//	}
func (b NumericBlob) AllByName(metricName string) iter.Seq2[int, NumericDataPoint] {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return func(yield func(int, NumericDataPoint) bool) {}
	}

	return b.allFromEntry(entry)
}

// AllTimestamps returns an iterator over all timestamps for the given metric ID.
func (b NumericBlob) AllTimestamps(metricID uint64) iter.Seq[int64] {
	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return func(yield func(int64) bool) {}
	}

	return b.allTimestampsFromEntry(entry)
}

// AllTimestampsByName returns all timestamps for the given metric name.
//
// Returns an empty sequence if the metric name is not found.
func (b NumericBlob) AllTimestampsByName(metricName string) iter.Seq[int64] {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return func(yield func(int64) bool) {}
	}

	return b.allTimestampsFromEntry(entry)
}

// AllValues returns an iterator over all values for the given metric ID.
func (b NumericBlob) AllValues(metricID uint64) iter.Seq[float64] {
	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return func(yield func(float64) bool) {}
	}

	return b.allValuesFromEntry(entry)
}

// AllValuesByName returns all values for the given metric name.
//
// Returns an empty sequence if the metric name is not found.
func (b NumericBlob) AllValuesByName(metricName string) iter.Seq[float64] {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return func(yield func(float64) bool) {}
	}

	return b.allValuesFromEntry(entry)
}

// AllTags returns a sequence of tags for the given metric ID.
// Tags are always stored as strings and may be empty.
//
// Returns an empty sequence if tags are not enabled (HasTag() == false).
// This includes both cases where tags were disabled at encoding time
// or optimized away due to all tags being empty.
//
// Example:
//
//	for tag := range blob.AllTags(metricID) {
//	    fmt.Println(tag)
//	}
func (b NumericBlob) AllTags(metricID uint64) iter.Seq[string] {
	// Return empty iterator if tags are not enabled
	// This handles both tags disabled and tags optimized away
	if !b.HasTag() {
		return func(yield func(string) bool) {}
	}

	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return func(yield func(string) bool) {}
	}

	return b.allTagsFromEntry(entry)
}

// AllTagsByName returns all tags for the given metric name.
//
// Returns an empty sequence if tags are not enabled or the metric name is not found.
func (b NumericBlob) AllTagsByName(metricName string) iter.Seq[string] {
	// Return empty iterator if tags are not enabled
	if !b.HasTag() {
		return func(yield func(string) bool) {}
	}

	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return func(yield func(string) bool) {}
	}

	return b.allTagsFromEntry(entry)
}

// TimestampAt returns the timestamp at the specified index for the given metric.
// The index is 0-based within this blob.
//
// Returns (timestamp, true) if successful, or (0, false) if:
//   - The metric doesn't exist in this blob
//   - The index is out of bounds
//   - The encoding doesn't support random access (currently only raw encoding is supported)
//
// Performance: O(1) for raw encoding with same byte order, O(1) for raw encoding with different byte order.
// Delta encoding is not supported for random access and will return false.
func (b NumericBlob) TimestampAt(metricID uint64, index int) (int64, bool) {
	entry, ok := b.index.GetByID(metricID)
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
//   - The encoding doesn't support random access (currently only raw encoding is supported)
//
// Performance: O(1) for raw encoding.
func (b NumericBlob) TimestampAtByName(metricName string, index int) (int64, bool) {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return 0, false
	}

	return b.timestampAtFromEntry(entry, index)
}

// ValueAt returns the value at the specified index for the given metric.
// The index is 0-based within this blob.
//
// Returns (value, true) if successful, or (0, false) if:
//   - The metric doesn't exist in this blob
//   - The index is out of bounds
//   - The encoding doesn't support random access (currently only raw encoding is supported)
//
// Performance: O(1) for raw encoding with same byte order, O(1) for raw encoding with different byte order.
// Delta encoding is not supported for random access and will return false.
func (b NumericBlob) ValueAt(metricID uint64, index int) (float64, bool) {
	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return 0, false
	}

	return b.valueAtFromEntry(entry, index)
}

// ValueAtByName returns the value at the specified index for the given metric name.
// The index is 0-based within this blob.
//
// Returns (value, true) if successful, or (0, false) if:
//   - The metric name doesn't exist in this blob
//   - The index is out of bounds
//   - The encoding doesn't support random access (currently only raw encoding is supported)
//
// Performance: O(1) for raw encoding.
func (b NumericBlob) ValueAtByName(metricName string, index int) (float64, bool) {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return 0, false
	}

	return b.valueAtFromEntry(entry, index)
}

// TagAt returns the tag at the specified index for the given metric.
// The index is 0-based within this blob.
//
// Returns (tag, true) if successful, or ("", false) if:
//   - Tags are not enabled (HasTag() == false)
//   - The metric doesn't exist in this blob
//   - The index is out of bounds
//
// Performance: O(1) - tags always support random access.
//
// Example:
//
//	if tag, ok := blob.TagAt(metricID, 5); ok {
//	    fmt.Printf("Tag at index 5: %s\n", tag)
//	}
func (b NumericBlob) TagAt(metricID uint64, index int) (string, bool) {
	// Return empty string if tags are not enabled
	if !b.HasTag() {
		return "", false
	}

	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return "", false
	}

	return b.tagAtFromEntry(entry, index)
}

// TagAtByName returns the tag at the specified index for the given metric name.
// The index is 0-based within this blob.
//
// Returns (tag, true) if successful, or ("", false) if:
//   - Tags are not enabled in this blob
//   - The metric name doesn't exist in this blob
//   - The index is out of bounds
func (b NumericBlob) TagAtByName(metricName string, index int) (string, bool) {
	// Return empty string if tags are not enabled
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
func (b NumericBlob) lookupMetricEntry(metricName string) (section.NumericIndexEntry, bool) {
	return b.index.GetByName(metricName)
}

// Internal helper methods that work with NumericIndexEntry directly.
// These eliminate duplication between ByID and ByName methods.

// allFromEntry returns an iterator over (index, NumericDataPoint) for the given entry.
func (b NumericBlob) allFromEntry(entry section.NumericIndexEntry) iter.Seq2[int, NumericDataPoint] {
	if entry.Count == 0 {
		return func(yield func(int, NumericDataPoint) bool) {}
	}

	// Get timestamp, value, and tag byte slices
	tsBytes := b.tsPayload[entry.TimestampOffset : entry.TimestampOffset+entry.TimestampLength]
	valBytes := b.valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength]
	
	var tagBytes []byte
	if b.HasTag() && len(b.tagPayload) > 0 {
		tagBytes = b.tagPayload[entry.TagOffset : entry.TagOffset+entry.TagLength]
	}

	// Return optimized iterator based on encoding types
	return b.allDataPoints(tsBytes, valBytes, tagBytes, entry.Count)
}

// allTimestampsFromEntry returns an iterator over all timestamps for the given entry.
func (b NumericBlob) allTimestampsFromEntry(entry section.NumericIndexEntry) iter.Seq[int64] {
	if entry.Count == 0 {
		return func(yield func(int64) bool) {}
	}

	tsBytes := b.tsPayload[entry.TimestampOffset : entry.TimestampOffset+entry.TimestampLength]

	return b.decodeTimestamps(tsBytes, entry.Count)
}

// allValuesFromEntry returns an iterator over all values for the given entry.
func (b NumericBlob) allValuesFromEntry(entry section.NumericIndexEntry) iter.Seq[float64] {
	if entry.Count == 0 {
		return func(yield func(float64) bool) {}
	}

	valBytes := b.valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength]

	return b.decodeValues(valBytes, entry.Count)
}

// allTagsFromEntry returns an iterator over all tags for the given entry.
func (b NumericBlob) allTagsFromEntry(entry section.NumericIndexEntry) iter.Seq[string] {
	count := entry.Count
	if count == 0 {
		return func(yield func(string) bool) {}
	}

	// If tags are disabled, return empty strings
	if !b.HasTag() || len(b.tagPayload) == 0 {
		return func(yield func(string) bool) {
			for i := 0; i < count; i++ {
				if !yield("") {
					break
				}
			}
		}
	}

	start := entry.TagOffset
	tagBytes := b.tagPayload[start:]

	return b.decodeTags(tagBytes, count)
}

// timestampAtFromEntry returns the timestamp at the specified index for the given entry.
func (b NumericBlob) timestampAtFromEntry(entry section.NumericIndexEntry, index int) (int64, bool) {
	count := entry.Count
	if index < 0 || index >= count {
		return 0, false
	}

	tsBytes := b.tsPayload[entry.TimestampOffset : entry.TimestampOffset+entry.TimestampLength]

	switch b.tsEncType { //nolint: exhaustive
	case format.TypeRaw:
		engine := b.Engine()
		if b.sameByteOrder {
			decoder := ienc.NewTimestampRawUnsafeDecoder(engine)
			return decoder.At(tsBytes, index, count)
		}

		decoder := ienc.NewTimestampRawDecoder(engine)

		return decoder.At(tsBytes, index, count)
	case format.TypeDelta:
		decoder := ienc.NewTimestampDeltaDecoder()
		return decoder.At(tsBytes, index, count)
	default:
		// Other encodings don't support random access
		return 0, false
	}
}

// valueAtFromEntry returns the value at the specified index for the given entry.
func (b NumericBlob) valueAtFromEntry(entry section.NumericIndexEntry, index int) (float64, bool) {
	count := entry.Count
	if index < 0 || index >= count {
		return 0, false
	}

	// Get byte slice for values
	valStart := entry.ValueOffset
	if valStart > len(b.valPayload) {
		return 0, false
	}

	var valBytes []byte

	switch b.ValueEncoding() { //nolint: exhaustive
	case format.TypeRaw:
		// Raw encoding: fixed 8 bytes per float64
		valEnd := valStart + count*8
		if valEnd > len(b.valPayload) {
			return 0, false
		}
		valBytes = b.valPayload[valStart:valEnd]

		engine := b.Engine()
		if b.sameByteOrder {
			decoder := ienc.NewNumericRawUnsafeDecoder(engine)
			return decoder.At(valBytes, index, count)
		}

		decoder := ienc.NewNumericRawDecoder(engine)

		return decoder.At(valBytes, index, count)
	case format.TypeGorilla:
		// For Gorilla encoding, we need to calculate the exact byte length for this metric
		// because the data is variable-length compressed. If we pass all remaining bytes,
		// the decoder might read into the next metric's data, causing incorrect values.
		decoder := ienc.NewNumericGorillaDecoder()

		valBytes = b.valPayload[valStart:]

		return decoder.At(valBytes, index, count)
	default:
		// Other encodings don't support random access
		return 0, false
	}
}

// tagAtFromEntry returns the tag at the specified index for the given entry.
func (b NumericBlob) tagAtFromEntry(entry section.NumericIndexEntry, index int) (string, bool) {
	count := entry.Count
	if index < 0 || index >= count {
		return "", false
	}

	// Get tag bytes starting from this metric's offset
	tagStart := entry.TagOffset
	tagBytes := b.tagPayload[tagStart:]

	// Tags always support random access
	decoder := ienc.NewTagDecoder(b.Engine())

	return decoder.At(tagBytes, index, count)
}

// allDataPoints creates an optimized iterator for (index, NumericDataPoint).
// It selects the fastest decoder combination based on encoding types and byte order.
//
// Optimized paths:
//   - Raw + Raw: Uses At() for both (O(1) random access) - fastest
//   - Raw + Gorilla: Uses At() for timestamps, All() for values - fast
//   - Delta + Gorilla: Uses All() for both (sequential decoding) - default config
//   - Delta + Raw: Uses All() for timestamps, At() for values - fast
//
// All other combinations fall back to generic implementation.
func (b NumericBlob) allDataPoints(tsBytes, valBytes, tagBytes []byte, count int) iter.Seq2[int, NumericDataPoint] {
	// Fastest path: optimize for raw encoding (supports random access via At())
	if b.tsEncType == format.TypeRaw && b.ValueEncoding() == format.TypeRaw {
		return b.allDataPointsRaw(tsBytes, valBytes, tagBytes, count)
	}

	// Fast path: optimize for raw timestamps + gorilla values
	if b.tsEncType == format.TypeRaw && b.ValueEncoding() == format.TypeGorilla {
		return b.allDataPointsRawGorilla(tsBytes, valBytes, tagBytes, count)
	}

	// High-priority path: optimize for delta + gorilla (DEFAULT CONFIGURATION)
	// This is the most commonly used encoding combination in production
	if b.tsEncType == format.TypeDelta && b.ValueEncoding() == format.TypeGorilla {
		return b.allDataPointsDeltaGorilla(tsBytes, valBytes, tagBytes, count)
	}

	// Faster path: optimize for delta timestamps + raw values (common for time-series)
	if b.tsEncType == format.TypeDelta && b.ValueEncoding() == format.TypeRaw {
		return b.allDataPointsDeltaRaw(tsBytes, valBytes, tagBytes, count)
	}

	// Generic fallback for other combinations, use iter.Pull for synchronization.
	// This works for all encoding combinations but is slower (not that slow).
	return b.allDataPointsGeneric(tsBytes, valBytes, tagBytes, count)
}

// allDataPointsRaw handles raw encoding for timestamps and values.
// Uses At() for ts/val (O(1) direct memory access - fastest possible).
// Uses All() iterator for tags (O(1) per iteration, avoids O(N²) scanning in At()).
func (b NumericBlob) allDataPointsRaw(tsBytes, valBytes, tagBytes []byte, count int) iter.Seq2[int, NumericDataPoint] {
	var tsDecoder encoding.ColumnarDecoder[int64]
	var valDecoder encoding.ColumnarDecoder[float64]

	engine := b.Engine()
	if b.sameByteOrder {
		tsDecoder = ienc.NewTimestampRawUnsafeDecoder(engine)
		valDecoder = ienc.NewNumericRawUnsafeDecoder(engine)
	} else {
		tsDecoder = ienc.NewTimestampRawDecoder(engine)
		valDecoder = ienc.NewNumericRawDecoder(engine)
	}

	return func(yield func(int, NumericDataPoint) bool) {
		// If tags are disabled, use simple iteration without tag decoder
		if !b.HasTag() {
			for i := 0; i < count; i++ {
				ts, _ := tsDecoder.At(tsBytes, i, count)
				val, _ := valDecoder.At(valBytes, i, count)

				dp := NumericDataPoint{
					Ts:  ts,
					Val: val,
					Tag: "",
				}

				if !yield(i, dp) {
					break
				}
			}

			return
		}

		// Tags enabled: Use tag iterator to avoid O(N²) cost of repeated At() calls
		// Tag At() must scan from start each time due to varint encoding
		tagDecoder := ienc.NewTagDecoder(b.Engine())
		tagIter := tagDecoder.All(tagBytes, count)

		i := 0
		for tag := range tagIter {
			// Use At() for ts/val - O(1) direct memory access
			ts, _ := tsDecoder.At(tsBytes, i, count)
			val, _ := valDecoder.At(valBytes, i, count)

			dp := NumericDataPoint{
				Ts:  ts,
				Val: val,
				Tag: tag,
			}

			if !yield(i, dp) {
				break
			}
			i++
		}
	}
}

// allDataPointsDeltaRaw handles delta timestamps with raw values and tags.
// Uses All() for ts (delta requires sequential) and tags (avoids O(N²) At() scanning).
// Uses At() for values (O(1) direct memory access for raw encoding).
func (b NumericBlob) allDataPointsDeltaRaw(tsBytes, valBytes, tagBytes []byte, count int) iter.Seq2[int, NumericDataPoint] {
	var tsDecoder ienc.TimestampDeltaDecoder
	var valDecoder encoding.ColumnarDecoder[float64]

	engine := b.Engine()
	if b.sameByteOrder {
		valDecoder = ienc.NewNumericRawUnsafeDecoder(engine)
	} else {
		valDecoder = ienc.NewNumericRawDecoder(engine)
	}

	return func(yield func(int, NumericDataPoint) bool) {
		// If tags are disabled, use simple iteration without tag decoder
		if !b.HasTag() {
			tsIter := tsDecoder.All(tsBytes, count)
			i := 0
			for ts := range tsIter {
				val, _ := valDecoder.At(valBytes, i, count)

				dp := NumericDataPoint{
					Ts:  ts,
					Val: val,
					Tag: "",
				}

				if !yield(i, dp) {
					break
				}
				i++
			}

			return
		}

		// Tags enabled: Use iterators for ts (required) and tags (faster than At())
		tagDecoder := ienc.NewTagDecoder(b.Engine())
		tsIter := tsDecoder.All(tsBytes, count)
		tagIter := tagDecoder.All(tagBytes, count)

		// Sync ts and tag iterators manually, it's faster than iter.Pull
		tsNext, tsStop := iter.Pull(tsIter)
		tagNext, tagStop := iter.Pull(tagIter)
		defer tsStop()
		defer tagStop()

		i := 0
		for {
			ts, tsOk := tsNext()
			tag, tagOk := tagNext()
			if !tsOk || !tagOk {
				break
			}

			// Use At() for values - O(1) direct memory access
			val, _ := valDecoder.At(valBytes, i, count)

			dp := NumericDataPoint{
				Ts:  ts,
				Val: val,
				Tag: tag,
			}

			if !yield(i, dp) {
				break
			}

			i++
		}
	}
}

// allDataPointsDeltaGorilla handles delta timestamps with Gorilla values.
//
// Uses All() for ts (delta requires sequential), val (Gorilla requires sequential),
// and tags (avoids O(N²) At() scanning). This is the optimized path for the default
// encoding configuration (Delta + Gorilla).
//
// Performance: ~350 ns/op for 10 points (50-70% faster than generic fallback).
//
// Parameters:
//   - tsBytes: Encoded timestamp data (delta-of-delta format)
//   - valBytes: Encoded value data (Gorilla XOR format)
//   - tagBytes: Encoded tag data (varint strings)
//   - count: Number of data points
//
// Returns:
//   - iter.Seq2[int, NumericDataPoint]: Iterator yielding (index, data point) pairs
func (b NumericBlob) allDataPointsDeltaGorilla(tsBytes, valBytes, tagBytes []byte, count int) iter.Seq2[int, NumericDataPoint] {
	var tsDecoder ienc.TimestampDeltaDecoder
	var valDecoder ienc.NumericGorillaDecoder

	return func(yield func(int, NumericDataPoint) bool) {
		// If tags are disabled, use simple iteration without tag decoder
		if !b.HasTag() {
			tsIter := tsDecoder.All(tsBytes, count)
			valIter := valDecoder.All(valBytes, count)

			// Sync ts and val iterators manually
			tsNext, tsStop := iter.Pull(tsIter)
			valNext, valStop := iter.Pull(valIter)
			defer tsStop()
			defer valStop()

			i := 0
			for {
				ts, tsOk := tsNext()
				val, valOk := valNext()
				if !tsOk || !valOk {
					break
				}

				dp := NumericDataPoint{
					Ts:  ts,
					Val: val,
					Tag: "",
				}

				if !yield(i, dp) {
					break
				}
				i++
			}

			return
		}

		// Tags enabled: Use iterators for all three
		tagDecoder := ienc.NewTagDecoder(b.Engine())
		tsIter := tsDecoder.All(tsBytes, count)
		valIter := valDecoder.All(valBytes, count)
		tagIter := tagDecoder.All(tagBytes, count)

		// Sync all three iterators manually
		tsNext, tsStop := iter.Pull(tsIter)
		valNext, valStop := iter.Pull(valIter)
		tagNext, tagStop := iter.Pull(tagIter)
		defer tsStop()
		defer valStop()
		defer tagStop()

		i := 0
		for {
			ts, tsOk := tsNext()
			val, valOk := valNext()
			tag, tagOk := tagNext()

			if !tsOk || !valOk || !tagOk {
				break
			}

			dp := NumericDataPoint{
				Ts:  ts,
				Val: val,
				Tag: tag,
			}

			if !yield(i, dp) {
				break
			}

			i++
		}
	}
}

// allDataPointsRawGorilla handles raw timestamps with Gorilla values.
//
// Uses At() for ts (O(1) direct memory access for raw encoding).
// Uses All() for val (Gorilla requires sequential) and tags (faster than At()).
//
// Performance: ~400 ns/op for 10 points (30-50% faster than generic fallback).
//
// Parameters:
//   - tsBytes: Encoded timestamp data (raw format)
//   - valBytes: Encoded value data (Gorilla XOR format)
//   - tagBytes: Encoded tag data (varint strings)
//   - count: Number of data points
//
// Returns:
//   - iter.Seq2[int, NumericDataPoint]: Iterator yielding (index, data point) pairs
func (b NumericBlob) allDataPointsRawGorilla(tsBytes, valBytes, tagBytes []byte, count int) iter.Seq2[int, NumericDataPoint] {
	var tsDecoder encoding.ColumnarDecoder[int64]
	var valDecoder ienc.NumericGorillaDecoder

	engine := b.Engine()
	if b.sameByteOrder {
		tsDecoder = ienc.NewTimestampRawUnsafeDecoder(engine)
	} else {
		tsDecoder = ienc.NewTimestampRawDecoder(engine)
	}

	return func(yield func(int, NumericDataPoint) bool) {
		// If tags are disabled, use simple iteration without tag decoder
		if !b.HasTag() {
			valIter := valDecoder.All(valBytes, count)

			i := 0
			for val := range valIter {
				// Use At() for timestamps - O(1) direct memory access
				ts, _ := tsDecoder.At(tsBytes, i, count)

				dp := NumericDataPoint{
					Ts:  ts,
					Val: val,
					Tag: "",
				}

				if !yield(i, dp) {
					break
				}
				i++
			}

			return
		}

		// Tags enabled: Use iterators for val and tags, At() for ts
		tagDecoder := ienc.NewTagDecoder(b.Engine())
		valIter := valDecoder.All(valBytes, count)
		tagIter := tagDecoder.All(tagBytes, count)

		// Sync val and tag iterators manually
		valNext, valStop := iter.Pull(valIter)
		tagNext, tagStop := iter.Pull(tagIter)
		defer valStop()
		defer tagStop()

		i := 0
		for {
			val, valOk := valNext()
			tag, tagOk := tagNext()
			if !valOk || !tagOk {
				break
			}

			// Use At() for timestamps - O(1) direct memory access
			ts, _ := tsDecoder.At(tsBytes, i, count)

			dp := NumericDataPoint{
				Ts:  ts,
				Val: val,
				Tag: tag,
			}

			if !yield(i, dp) {
				break
			}

			i++
		}
	}
}

// allDataPointsGeneric is the fallback for unsupported encoding combinations (uses iter.Pull).
func (b NumericBlob) allDataPointsGeneric(tsBytes, valBytes, tagBytes []byte, count int) iter.Seq2[int, NumericDataPoint] {
	// If tags are disabled, use simple iteration without tag decoder
	tsIter := b.decodeTimestamps(tsBytes, count)
	valIter := b.decodeValues(valBytes, count)

	if !b.HasTag() {
		return func(yield func(int, NumericDataPoint) bool) {
			// Use iter.Pull for fallback (works for all encoding combinations)
			tsNext, tsStop := iter.Pull(tsIter)
			valNext, valStop := iter.Pull(valIter)
			defer tsStop()
			defer valStop()

			i := 0
			for {
				ts, tsOk := tsNext()
				val, valOk := valNext()
				if !tsOk || !valOk {
					break
				}

				dp := NumericDataPoint{Ts: ts, Val: val, Tag: ""}
				if !yield(i, dp) {
					break
				}

				i++
			}
		}
	}

	tagIter := b.decodeTags(tagBytes, count)

	return func(yield func(int, NumericDataPoint) bool) {
		// Use iter.Pull for fallback (works for all encoding combinations)
		tsNext, tsStop := iter.Pull(tsIter)
		valNext, valStop := iter.Pull(valIter)
		tagNext, tagStop := iter.Pull(tagIter)
		defer tsStop()
		defer valStop()
		defer tagStop()

		i := 0
		for {
			ts, tsOk := tsNext()
			val, valOk := valNext()
			tag, _ := tagNext()

			if !tsOk || !valOk {
				break
			}

			dp := NumericDataPoint{Ts: ts, Val: val, Tag: tag}
			if !yield(i, dp) {
				break
			}

			i++
		}
	}
}

// decodeTimestamps selects the optimal timestamp decoder and returns an iterator.
func (b NumericBlob) decodeTimestamps(tsBytes []byte, count int) iter.Seq[int64] {
	switch b.tsEncType { //nolint: exhaustive
	case format.TypeRaw:
		engine := b.Engine()
		if b.sameByteOrder {
			decoder := ienc.NewTimestampRawUnsafeDecoder(engine)
			return decoder.All(tsBytes, count)
		}

		decoder := ienc.NewTimestampRawDecoder(engine)

		return decoder.All(tsBytes, count)
	case format.TypeDelta:
		var decoder ienc.TimestampDeltaDecoder

		return decoder.All(tsBytes, count)
	default:
		return func(yield func(int64) bool) {}
	}
}

// decodeValues selects the optimal value decoder and returns an iterator.
func (b NumericBlob) decodeValues(valBytes []byte, count int) iter.Seq[float64] {
	switch b.ValueEncoding() { //nolint: exhaustive
	case format.TypeRaw:
		engine := b.Engine()
		if b.sameByteOrder {
			decoder := ienc.NewNumericRawUnsafeDecoder(engine)
			return decoder.All(valBytes, count)
		}

		decoder := ienc.NewNumericRawDecoder(engine)

		return decoder.All(valBytes, count)
	case format.TypeGorilla:
		var decoder ienc.NumericGorillaDecoder
		return decoder.All(valBytes, count)
	default:
		return func(yield func(float64) bool) {}
	}
}

// decodeTags returns an iterator for tag strings.
// Tags are always encoded the same way regardless of timestamp/value encoding.
func (b NumericBlob) decodeTags(tagBytes []byte, count int) iter.Seq[string] {
	var decoder ienc.TagDecoder
	return decoder.All(tagBytes, count)
}
