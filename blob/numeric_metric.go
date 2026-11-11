package blob

import (
	"math"
	"slices"
	"strings"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/format"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// AccessClass says how a NumericMetric serves a random access on one axis.
//
// The class names the mechanism, not a complexity bound:
// AccessDirect means the lookup never walks the column's points,
// AccessSequential means it replays the column from its start up to the index,
// and AccessUnsupported means the axis cannot be read and every lookup on it returns false.
type AccessClass uint8

const (
	// locateScanParts is the number of parts after the first up to which an index is placed by a scan, not a bisection.
	locateScanParts = 8
	// locateStrides is the number of doubling strides locateBisect takes from its estimate before it bisects.
	locateStrides = 5
)

// The classes are ordered from best to worst, so the worst of several is their maximum.
const (
	// AccessDirect reads the point without walking the column.
	// Timestamps: Raw, a sequence pre-decoded at open because two or more metrics of the blob share it,
	// or a sequence NumericMetric.Materialize decoded.
	// Values: Raw, ALP and ALP-RLE, a bit unpack plus a search of the column's exceptions,
	// and for the runs layout a popcount of one word per 64 points.
	AccessDirect AccessClass = iota
	// AccessSequential replays the column from its start up to the index:
	// about 1 ns per point for Delta and DeltaPacked timestamps, and 4 ns per point for Gorilla and Chimp values.
	AccessSequential
	// AccessUnsupported marks an axis whose encoding has no point accessor; every lookup on it returns false.
	AccessUnsupported
)

// NumericMetric is a handle on one metric, of a NumericBlob or across the numeric members of a BlobSet,
// resolved once and then read by index.
//
// NumericBlob.Metric and MetricByName, and BlobSet.NumericMetric and NumericMetricByName,
// resolve the metric's index entries, its payload ranges and its pre-decoded shared timestamps once,
// so Len, ValueAt, TimestampAt, TagAt and At do no name hashing, index search, slicing or decoder construction per call,
// and allocate nothing.
// On a set the index runs over the members in start-time order, as the BlobSet accessors' does.
//
// The handle aliases the blobs' bytes, exactly as a NumericBlob does, and is valid for as long as they are:
// every backing buffer must stay unmodified, including under the lifetime rule of NewNumericDecoderBorrowed.
//
// The zero value is a handle on nothing: Len returns 0, every lookup returns false,
// and TimestampAccess and ValueAccess return AccessUnsupported.
//
// A handle is safe for concurrent reads, like the NumericBlob.
// Materialize is the one write: it must not run concurrently with any other method on the same handle.
// Copying a handle copies slice headers over the same bytes, so a copy serves the same reads.
// A copy made before Materialize is independent of it and may materialize on its own;
// copies made after it share the decoded slices, which are never written again.
//
// TimestampAt and ValueAt read the point directly or replay the column up to it,
// as the encodings and the metric's shared-timestamp group decide, not the encoder options alone;
// TimestampAccess and ValueAccess report it.
// Materialize decodes the columns a lookup would replay, and the tags, into slices the handle owns,
// after which every lookup is direct.
// On an encoding the accessors cannot read, every lookup on that axis returns false and the class is AccessUnsupported.
//
// Methods take a pointer receiver; the type is still used by value, as bytes.Buffer is:
//
//	h, ok := blob.Metric(metricID)
//	if ok {
//	    v, _ := h.ValueAt(i)
//	}
type NumericMetric struct {
	first numericMetricPart
	rest  []numericMetricPart
}

// numericMetricPart is the metric's slice of one blob: what the point accessors need, copied at resolution.
// It holds no pointer into the blob's index, which entryByID and entryByName forbid retaining.
// The embedded blobBase carries the encodings, the flags and the byte order the decode paths dispatch on,
// so the iteration helpers run on a NumericBlob built from it and the part's own slices.
type numericMetricPart struct {
	blobBase
	count    int                 // points in this part
	base     int                 // points before this part in the handle
	shared   []int64             // the pre-decoded timestamps of the metric's shared group, or nil when it has none
	tsBytes  []byte              // the metric's own timestamp range, nil when the entry's range is invalid
	valBytes []byte              // the metric's own value range, nil when the entry's range is invalid
	tagBytes []byte              // the metric's own tag range, nil without tags or when the range is invalid
	engine   endian.EndianEngine // the blob's byte order, resolved once for the point accessors
	tsOK     bool                // the entry's timestamp range lay inside the payload
	valOK    bool                // the entry's value range lay inside the payload
	tagOK    bool                // the entry's tag range lay inside the payload (false without tags)
	// sharedSorted records that the decoder found the whole shared group non-decreasing when it opened the blob,
	// which every metric of the group can then rely on without reading the group again.
	sharedSorted bool

	// Owned by the handle and filled by Materialize, nil until then; never written again once set.
	// Each holds what its decoder produced, count elements unless the stream ended early.
	timestamps []int64   // the decoded timestamp column, for a sequential timestamp axis
	values     []float64 // the decoded value column, for a sequential value axis
	tags       []string  // the decoded tag column, for a blob with tags
}

// numericMetricArena holds the arrays one Materialize call carves its parts' decoded columns from:
// one per axis, and one string holding every tag column the call decodes, back to back.
// Materialize sizes it from the parts that need decoding, fills the tag string, then hands each part its share in order.
type numericMetricArena struct {
	timestamps []int64
	values     []float64
	tags       []string
	tagColumns string
	tagBuilder strings.Builder

	numTimestamps int
	numValues     int
	numTags       int
	numTagBytes   int

	// Whether some part decodes the axis, even an empty one: a decoded column is non-nil, which marks it done.
	decodesTimestamps bool
	decodesValues     bool
	decodesTags       bool
}

// fillNumericMetricPart copies what the point accessors need for entry of b into p, a part whose points start at base.
// It writes in place and takes the blob by pointer so that resolution copies neither the blob nor the part.
// A range that falls outside its payload leaves the slice nil, so every lookup on that axis returns false,
// which is what the NumericBlob accessors answer on the same entry.
func fillNumericMetricPart(p *numericMetricPart, b *NumericBlob, entry *section.NumericIndexEntry, base int) {
	p.blobBase = b.blobBase
	p.count = entry.Count
	p.base = base
	p.shared = b.sharedTs.lookup(entry.TimestampOffset)
	p.sharedSorted = p.shared != nil && b.sharedTs.sortedAt(entry.TimestampOffset)
	p.engine = b.Engine()
	p.tsBytes, p.tsOK = safeSlice(b.tsPayload, entry.TimestampOffset, entry.TimestampLength)
	p.valBytes, p.valOK = safeSlice(b.valPayload, entry.ValueOffset, entry.ValueLength)
	if b.HasTag() {
		p.tagBytes, p.tagOK = safeSlice(b.tagPayload, entry.TagOffset, entry.TagLength)
	}
}

// numericMetricAcrossBlobs resolves the metric in every member in order:
// the first contributing member fills first, the others fill rest, allocated once after counting them,
// each with the points before it as its base.
//
// A member that holds the metric without points is left out: it adds nothing to the index,
// and it has no first timestamp for SearchTimestamp to place it by.
// When no member has points, the first that holds the metric stands for it and the handle is empty.
// The encoder refuses a metric without points, so only a blob another writer made has one.
func numericMetricAcrossBlobs(blobs []NumericBlob, r setEntryResolver) (h NumericMetric, ok bool) {
	// The members of a set usually hold the same metrics in the same order,
	// so each member is first asked for the entry at the ordinal the member before it had.
	id := r.metricID
	if r.byName {
		id = hash.ID(r.metricName)
	}
	first := -1
	members := 0
	ord, firstOrd := 0, 0
	var firstEntry *section.NumericIndexEntry
	for i := range blobs {
		var entry *section.NumericIndexEntry
		entry, ord = r.entryNear(&blobs[i], id, ord)
		if entry == nil {
			continue
		}
		if entry.Count == 0 {
			if firstEntry == nil {
				first, firstEntry = i, entry
			}

			continue
		}
		if members == 0 {
			first, firstEntry, firstOrd = i, entry, ord
		}
		members++
	}
	if firstEntry == nil {
		return NumericMetric{}, false
	}
	fillNumericMetricPart(&h.first, &blobs[first], firstEntry, 0)
	if members <= 1 {
		return h, true
	}

	h.rest = make([]numericMetricPart, members-1)
	base := h.first.count
	k := 0
	ord = firstOrd
	for i := first + 1; i < len(blobs); i++ {
		var entry *section.NumericIndexEntry
		entry, ord = r.entryNear(&blobs[i], id, ord)
		if entry == nil || entry.Count == 0 {
			continue
		}
		fillNumericMetricPart(&h.rest[k], &blobs[i], entry, base)
		base += entry.Count
		k++
	}

	return h, true
}

// forEachZipped yields the points of decoded columns of equal length; tags is nil on a blob without tags.
func forEachZipped(ts []int64, vals []float64, tags []string, yield func(int, NumericDataPoint) bool) {
	for i, val := range vals {
		dp := NumericDataPoint{Ts: ts[i], Val: val}
		if tags != nil {
			dp.Tag = tags[i]
		}
		if !yield(i, dp) {
			return
		}
	}
}

// carve returns the next n elements of *buf with their capacity cut to n, so a part never reaches the next one's,
// and advances *buf past them.
func carve[T any](buf *[]T, n int) []T {
	s := (*buf)[:n:n]
	*buf = (*buf)[n:]

	return s
}

// Metric returns a handle on the metric with the given ID.
//
// Resolution follows ValueAt: an ID that two metric names collided on resolves to the first entry in index order.
// The handle aliases the blob's bytes and allocates nothing; see NumericMetric for its lifetime.
//
// Parameters:
//   - metricID: The metric ID to resolve.
//
// Returns:
//   - NumericMetric: The handle, or the zero value when the metric does not exist.
//   - bool: false if the metric ID does not exist in this blob.
//
// Example:
//
//	h, ok := blob.Metric(metricID)
//	if ok {
//	    for i := range h.Len() {
//	        v, _ := h.ValueAt(i)
//	        fmt.Println(v)
//	    }
//	}
func (b NumericBlob) Metric(metricID uint64) (h NumericMetric, ok bool) {
	entry := b.index.entryByID(metricID)
	if entry == nil {
		return NumericMetric{}, false
	}
	fillNumericMetricPart(&h.first, &b, entry, 0)

	return h, true
}

// MetricByName returns a handle on the metric with the given name.
//
// Resolution follows ValueAtByName: the by-name map on a collision,
// a hash plus a string compare when the blob retains metric names,
// and a hash plus an ID lookup when it does not.
// The handle aliases the blob's bytes and allocates nothing; see NumericMetric for its lifetime.
//
// Parameters:
//   - metricName: The metric name to resolve.
//
// Returns:
//   - NumericMetric: The handle, or the zero value when the metric does not exist.
//   - bool: false if the metric name does not exist in this blob.
func (b NumericBlob) MetricByName(metricName string) (h NumericMetric, ok bool) {
	entry := b.index.entryByName(metricName)
	if entry == nil {
		return NumericMetric{}, false
	}
	fillNumericMetricPart(&h.first, &b, entry, 0)

	return h, true
}

// NumericMetric returns a handle on the metric with the given ID across the numeric members of the set.
//
// The members contribute in start-time order,
// so the handle's index runs over the whole set, as NumericValueAt's does.
// Resolution follows NumericValueAt: the set's logical identity decides which member entry a collided ID names.
// Text members are not consulted, so a metric that only text members hold has no handle.
// The handle aliases the members' bytes; see NumericMetric for its lifetime.
// A member that holds the metric without points is left out of the handle.
// Resolution allocates nothing when one member has points of the metric and once when several do.
//
// Parameters:
//   - metricID: The metric ID to resolve.
//
// Returns:
//   - NumericMetric: The handle, or the zero value when no numeric member holds the metric.
//   - bool: false if no numeric member holds the metric.
func (bs BlobSet) NumericMetric(metricID uint64) (NumericMetric, bool) {
	target, collided := bs.numericIdentity.resolveID(metricID)

	return numericMetricAcrossBlobs(bs.numericBlobs, setEntryResolver{metricID: metricID, target: target, collided: collided})
}

// NumericMetricByName returns a handle on the metric with the given name across the numeric members of the set.
//
// The members contribute in start-time order,
// so the handle's index runs over the whole set, as NumericValueAtByName's does.
// Resolution follows NumericValueAtByName: a member that stripped its names is skipped when the name collided in the set.
// Text members are not consulted, so a name that only text members hold has no handle,
// where TimestampAtByName and MetricLenByName fall back to them.
// The handle aliases the members' bytes; see NumericMetric for its lifetime.
// A member that holds the metric without points is left out of the handle.
// Resolution allocates nothing when one member has points of the metric and once when several do.
//
// Parameters:
//   - metricName: The metric name to resolve.
//
// Returns:
//   - NumericMetric: The handle, or the zero value when no numeric member holds the metric.
//   - bool: false if no numeric member holds the metric.
//
// Example:
//
//	h, ok := set.NumericMetricByName("cpu.usage")
//	if ok {
//	    h.ForEachTimestamps(func(i int, ts int64) bool {
//	        fmt.Println(i, ts)
//	        return true
//	    })
//	}
func (bs BlobSet) NumericMetricByName(metricName string) (NumericMetric, bool) {
	r := setEntryResolver{metricName: metricName, byName: true, skipStripped: bs.numericIdentity.excludesStripped(metricName)}

	return numericMetricAcrossBlobs(bs.numericBlobs, r)
}

// Len returns the number of data points of the metric, 0 for the zero value.
func (h *NumericMetric) Len() int {
	if n := len(h.rest); n > 0 {
		last := &h.rest[n-1]

		return last.base + last.count
	}

	return h.first.count
}

// Duration returns the last timestamp of the metric minus its first, in the unit the encoder was given.
//
// It is the numeric branch of BlobSet.MetricDurationByName: 0 when the metric is empty,
// when either end cannot be read, or when the last timestamp is not after the first, so it is never negative.
// It reads the two ends through TimestampAt, so it walks a sequential column, and caches nothing.
func (h *NumericMetric) Duration() int64 {
	n := h.Len()
	if n == 0 {
		return 0
	}
	first, ok := h.TimestampAt(0)
	if !ok {
		return 0
	}
	last, ok := h.TimestampAt(n - 1)
	if !ok || last <= first {
		return 0
	}

	return last - first
}

// ForEach calls yield for every data point of the metric in index order, with the index At accepts.
//
// The index is the handle's own, each blob's points starting at the count of the points before it,
// not a running count of the points yielded.
// It decodes each blob's column once on the stack, as NumericBlob.ForEach does,
// or reads the slices Materialize decoded; decoded tags are not copied again.
// It returns nothing: the metric's existence was settled when the handle was made, and the zero value yields nothing.
//
// Parameters:
//   - yield: Called with (index, point); return false to stop the walk.
//     A nil yield walks nothing.
func (h *NumericMetric) ForEach(yield func(index int, dp NumericDataPoint) bool) {
	if yield == nil {
		return
	}
	if len(h.rest) == 0 {
		h.first.forEachPoints(yield)

		return
	}

	var (
		base    int
		stopped bool
	)
	adapter := func(i int, dp NumericDataPoint) bool {
		if !yield(base+i, dp) {
			stopped = true

			return false
		}

		return true
	}
	h.first.forEachPoints(adapter)
	for k := range h.rest {
		if stopped {
			return
		}
		base = h.rest[k].base
		h.rest[k].forEachPoints(adapter)
	}
}

// ForEachValues calls yield for every value of the metric in index order, with the index At accepts.
//
// It decodes each blob's value column once on the stack, as NumericBlob.ForEachValues does,
// or ranges over the values Materialize decoded.
// It returns nothing, and the zero value yields nothing.
//
// Parameters:
//   - yield: Called with (index, value); return false to stop the walk.
//     A nil yield walks nothing.
func (h *NumericMetric) ForEachValues(yield func(index int, value float64) bool) {
	if yield == nil {
		return
	}
	if h.first.forEachValues(yield) < 0 {
		return
	}
	for k := range h.rest {
		if h.rest[k].forEachValues(yield) < 0 {
			return
		}
	}
}

// ForEachTimestamps calls yield for every timestamp of the metric in index order, with the index At accepts.
//
// A blob's pre-decoded shared group, or the timestamps Materialize decoded, are yielded as they are,
// as NumericBlob.ForEachTimestamps yields the group; any other column is decoded once on the stack.
// It returns nothing, and the zero value yields nothing.
//
// Parameters:
//   - yield: Called with (index, timestamp); return false to stop the walk.
//     A nil yield walks nothing.
func (h *NumericMetric) ForEachTimestamps(yield func(index int, ts int64) bool) {
	if yield == nil {
		return
	}
	if h.first.forEachTimestamps(yield) < 0 {
		return
	}
	for k := range h.rest {
		if h.rest[k].forEachTimestamps(yield) < 0 {
			return
		}
	}
}

// ForEachRange calls yield for the data points whose index lies in [start, end), in index order,
// with the index At accepts: ForEach over a window of the metric.
//
// The range is clamped to the metric: a negative start reads from the first point, an end past Len() to the last,
// and a range left empty yields nothing.
// The walk begins at the blob that holds start and ends at the one that holds end-1,
// so the blobs before the window are not decoded; inside the first blob the points before start are decoded and skipped,
// which costs at most one blob's points.
// On a handle whose timestamps SearchTimestamp can search, the points of a time window [begin, end] are:
//
//	lo, _ := h.SearchTimestamp(begin)
//	hi := h.Len()
//	if end < math.MaxInt64 {
//	    hi, _ = h.SearchTimestamp(end + 1)
//	}
//	h.ForEachRange(lo, hi, yield)
//
// Parameters:
//   - start: The index of the first point to yield.
//   - end: The index after the last point to yield.
//   - yield: Called with (index, point); return false to stop the walk.
//     A nil yield walks nothing.
func (h *NumericMetric) ForEachRange(start, end int, yield func(index int, dp NumericDataPoint) bool) {
	start, end = max(start, 0), min(end, h.Len())
	if yield == nil || start >= end {
		return
	}

	var (
		base    int
		stopped bool
	)
	adapter := func(i int, dp NumericDataPoint) bool {
		index := base + i
		if index < start {
			return true
		}
		if index >= end || !yield(index, dp) {
			stopped = true

			return false
		}

		return true
	}
	for k := h.partOrdinal(start); !stopped && k <= len(h.rest); k++ {
		p := h.part(k)
		if p.base >= end {
			return
		}
		base = p.base
		p.forEachPoints(adapter)
	}
}

// ForEachValuesRange calls yield for the values whose index lies in [start, end), in index order:
// ForEachValues over a window of the metric, with the clamping and the cost of ForEachRange.
//
// Parameters:
//   - start: The index of the first value to yield.
//   - end: The index after the last value to yield.
//   - yield: Called with (index, value); return false to stop the walk.
//     A nil yield walks nothing.
func (h *NumericMetric) ForEachValuesRange(start, end int, yield func(index int, value float64) bool) {
	start, end = max(start, 0), min(end, h.Len())
	if yield == nil || start >= end {
		return
	}

	adapter := func(index int, value float64) bool {
		if index < start {
			return true
		}

		return index < end && yield(index, value)
	}
	for k := h.partOrdinal(start); k <= len(h.rest); k++ {
		p := h.part(k)
		if p.base >= end || p.forEachValues(adapter) < 0 {
			return
		}
	}
}

// ForEachTimestampsRange calls yield for the timestamps whose index lies in [start, end), in index order:
// ForEachTimestamps over a window of the metric, with the clamping and the cost of ForEachRange.
//
// Parameters:
//   - start: The index of the first timestamp to yield.
//   - end: The index after the last timestamp to yield.
//   - yield: Called with (index, timestamp); return false to stop the walk.
//     A nil yield walks nothing.
func (h *NumericMetric) ForEachTimestampsRange(start, end int, yield func(index int, ts int64) bool) {
	start, end = max(start, 0), min(end, h.Len())
	if yield == nil || start >= end {
		return
	}

	adapter := func(index int, ts int64) bool {
		if index < start {
			return true
		}

		return index < end && yield(index, ts)
	}
	for k := h.partOrdinal(start); k <= len(h.rest); k++ {
		p := h.part(k)
		if p.base >= end || p.forEachTimestamps(adapter) < 0 {
			return
		}
	}
}

// At returns the data point at the given index: its timestamp, its value and its tag.
//
// It agrees with TimestampAt, ValueAt and TagAt at the same index and succeeds only when all three do.
//
// Parameters:
//   - index: The 0-based index of the point.
//
// Returns:
//   - NumericDataPoint: The point, with an empty tag on a blob without tags.
//   - bool: false if the index is out of bounds or an axis cannot be read.
func (h *NumericMetric) At(index int) (NumericDataPoint, bool) {
	// A materialized part serves the point from its slices after one placement;
	// otherwise the part reads each axis from that same placement.
	var (
		p *numericMetricPart
		i int
	)
	if len(h.rest) > locateScanParts {
		p, i = h.locateBisect(index)
	} else {
		p, i = h.locate(index)
	}
	if i >= 0 && i < len(p.values) && (i < len(p.tags) || !p.HasTag()) {
		if ts, ok := p.timestampFromSlices(i); ok {
			dp := NumericDataPoint{Ts: ts, Val: p.values[i]}
			if i < len(p.tags) {
				dp.Tag = p.tags[i]
			}

			return dp, true
		}
	}

	return p.at(i)
}

// ValueAt returns the value at the given index.
//
// Its cost follows ValueAccess: direct for Raw, ALP and ALP-RLE values,
// a replay of the column up to the index for Gorilla and Chimp.
//
// Parameters:
//   - index: The 0-based index of the point.
//
// Returns:
//   - float64: The value.
//   - bool: false if the index is out of bounds or the value encoding cannot be read.
func (h *NumericMetric) ValueAt(index int) (float64, bool) {
	// The first part's decoded column needs no placement.
	if uint(index) < uint(len(h.first.values)) {
		return h.first.values[index], true
	}

	// numericMetricPart.valueAt, written out: calling it measurably slowed the ALP lookups.
	var (
		p *numericMetricPart
		i int
	)
	if len(h.rest) > locateScanParts {
		p, i = h.locateBisect(index)
	} else {
		p, i = h.locate(index)
	}
	count := p.count
	if i < 0 || i >= count {
		return 0, false
	}
	if i < len(p.values) {
		return p.values[i], true
	}

	switch p.valEncType {
	case format.TypeRaw:
		if p.sameByteOrder {
			return ienc.NewNumericRawUnsafeDecoder(p.engine).At(p.valBytes, i, count)
		}

		return ienc.NewNumericRawDecoder(p.engine).At(p.valBytes, i, count)
	case format.TypeGorilla:
		return ienc.NewNumericGorillaDecoder().At(p.valBytes, i, count)
	case format.TypeChimp:
		return ienc.NewNumericChimpDecoder().At(p.valBytes, i, count)
	case format.TypeALP, format.TypeALPRLE:
		return ienc.NewNumericALPDecoder(p.engine).At(p.valBytes, i, count)
	case format.TypeDelta, format.TypeDeltaPacked:
		// Timestamp-only encodings: header validation rejects them as value encodings.
		fallthrough
	default:
		return 0, false
	}
}

// TimestampAt returns the timestamp at the given index, in the unit the encoder was given.
//
// Its cost follows TimestampAccess: direct for Raw timestamps and for a sequence the blob pre-decoded at open,
// a replay of the column up to the index for a Delta or DeltaPacked sequence of the metric's own.
//
// Parameters:
//   - index: The 0-based index of the point.
//
// Returns:
//   - int64: The timestamp.
//   - bool: false if the index is out of bounds or the timestamp encoding cannot be read.
func (h *NumericMetric) TimestampAt(index int) (int64, bool) {
	// The first part's decoded column or pre-decoded group needs no placement.
	if uint(index) < uint(len(h.first.timestamps)) {
		return h.first.timestamps[index], true
	}
	if uint(index) < uint(min(len(h.first.shared), h.first.count)) {
		return h.first.shared[index], true
	}

	// numericMetricPart.timestampAt, written out, as ValueAt does.
	var (
		p *numericMetricPart
		i int
	)
	if len(h.rest) > locateScanParts {
		p, i = h.locateBisect(index)
	} else {
		p, i = h.locate(index)
	}
	count := p.count
	if i < 0 || i >= count {
		return 0, false
	}
	if ts, ok := p.timestampFromSlices(i); ok {
		return ts, true
	}

	switch p.tsEncType {
	case format.TypeRaw:
		if p.sameByteOrder {
			return ienc.NewTimestampRawUnsafeDecoder(p.engine).At(p.tsBytes, i, count)
		}

		return ienc.NewTimestampRawDecoder(p.engine).At(p.tsBytes, i, count)
	case format.TypeDelta:
		return ienc.NewTimestampDeltaDecoder().At(p.tsBytes, i, count)
	case format.TypeDeltaPacked:
		var decoder ienc.TimestampDeltaPackedDecoder

		return decoder.At(p.tsBytes, i, count)
	case format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE:
		// Value-only encodings: header validation rejects them as timestamp encodings.
		fallthrough
	default:
		return 0, false
	}
}

// SearchTimestamp returns the first index whose timestamp is not below ts, in the unit the encoder was given,
// and whether the timestamp there equals ts: the contract of slices.BinarySearch.
//
// When every timestamp is below ts it returns (Len(), false).
// The points of a window [begin, end] are the indices from SearchTimestamp(begin) up to,
// but not including, SearchTimestamp(end+1), or up to Len() when end is math.MaxInt64.
// The point nearest ts is the one at the returned index or the one before it.
//
// It bisects the blobs the handle spans on their first timestamps, which every encoding reads without a replay,
// then searches one blob: a bisection when that blob's timestamps are Raw, pre-decoded or materialized,
// one forward pass over its column otherwise.
// The cost is therefore a bisection of the blobs plus a search of one blob's points, wherever ts falls in the handle,
// and no Materialize is needed first.
// It allocates nothing.
//
// The timestamps must be non-decreasing over the whole handle; the result is unspecified otherwise,
// as with slices.BinarySearch on unsorted input.
// The members of a set are concatenated in start-time order, not merged,
// so the precondition fails when a member's first timestamp is below the last timestamp of the member before it,
// as it is between overlapping members.
// TimestampsSorted reports whether it holds.
//
// Parameters:
//   - ts: The timestamp to look for.
//
// Returns:
//   - index: The first index whose timestamp is not below ts, or Len() when there is none.
//   - found: true if the timestamp at index equals ts.
//     A timestamp the search needs that cannot be read also answers (Len(), false).
func (h *NumericMetric) SearchTimestamp(ts int64) (index int, found bool) {
	k, ok := h.partsStartingBelow(ts)
	if !ok {
		return h.Len(), false
	}
	if k == 0 {
		// ts is at or before the first point.
		first, ok := h.first.timestampAt(0)
		if !ok {
			return h.Len(), false
		}

		return 0, first == ts
	}

	// The first timestamp not below ts is in part k-1, or is the first point of part k when that part ends below ts.
	p := h.part(k - 1)
	i, at := p.lowerBound(ts)
	if i == 0 {
		return h.Len(), false
	}
	if i == p.count {
		if k == 1+len(h.rest) {
			return h.Len(), false
		}
		var ok bool
		if at, ok = h.part(k).timestampAt(0); !ok {
			return h.Len(), false
		}
	}

	return p.base + i, at == ts
}

// TimestampsSorted reports whether the handle's timestamps are non-decreasing over its whole index,
// which is what SearchTimestamp requires.
//
// It reads each timestamp at most once and stops at the first one below its predecessor:
// a compare per point on a column that is pre-decoded or materialized, a replay of the column otherwise,
// so its cost follows TimestampAccess.
// It allocates nothing and caches nothing; a caller that searches a handle many times checks it once.
// It reports true for a handle without points, and false when a blob's column does not decode to its last point.
func (h *NumericMetric) TimestampsSorted() bool {
	last, ok := h.first.sortedFrom(math.MinInt64)
	for k := 0; ok && k < len(h.rest); k++ {
		last, ok = h.rest[k].sortedFrom(last)
	}

	return ok
}

// TagAt returns the tag at the given index.
//
// Tags are variable-length strings walked from the column start,
// so TagAt is sequential on every tagged blob until Materialize decodes them.
// On a blob without tags it returns ("", true) for every valid index, as NumericBlob.TagAt does.
//
// Parameters:
//   - index: The 0-based index of the point.
//
// Returns:
//   - string: The tag, empty on a blob without tags.
//   - bool: false if the index is out of bounds or the tag column cannot be read.
func (h *NumericMetric) TagAt(index int) (string, bool) {
	// The first part's decoded tags need no placement.
	if uint(index) < uint(len(h.first.tags)) {
		return h.first.tags[index], true
	}

	var (
		p *numericMetricPart
		i int
	)
	if len(h.rest) > locateScanParts {
		p, i = h.locateBisect(index)
	} else {
		p, i = h.locate(index)
	}

	return p.tagAt(i)
}

// Materialize decodes every axis the handle reads by replaying its column into slices the handle owns,
// so that every later lookup is direct.
//
// Per blob it decodes the timestamps when they are sequential (Delta or DeltaPacked without a complete shared group),
// the values when they are sequential (Gorilla or Chimp), and the tags whenever the blob has them;
// axes that are already direct are left alone,
// so on a handle whose axes are all direct and whose blobs have no tags it allocates nothing.
// The decoded columns of all blobs share one array per axis, and the tags one string copy of every tag column,
// so a call makes at most four allocations regardless of how many blobs the handle spans,
// plus one copy of the parts after the first when one of them decodes (see below).
// An axis becomes direct only when its decode produced every point;
// a stream that ends early keeps what was decoded, the axis keeps its class,
// and lookups past the decoded points read the payload as before.
// Afterwards TimestampAccess and ValueAccess report the outcome,
// and At, ValueAt, TimestampAt, TagAt and the ForEach forms read the decoded slices.
//
// Materialize is idempotent: a second call decodes nothing and allocates nothing.
// It is a write: it must not run concurrently with any other method on the same handle,
// and the caller serializes it.
// A copy made before it stays as it was and may materialize on its own;
// copies made after it share the decoded slices, which are never written again.
// The slices are the handle's own, but the handle still aliases the blobs' bytes for its other reads.
func (h *NumericMetric) Materialize() {
	restDecode := h.restNeedsDecode()
	if !restDecode && !h.first.needsDecode() {
		return
	}
	if restDecode {
		// The parts after the first live in an array that copies of the handle share: write to a copy of it,
		// so a copy made before this call keeps its parts as they were.
		h.rest = slices.Clone(h.rest)
	}

	// Three passes over the same parts in the same order, so the last one carves the arena in step with the first.
	// The loops call by name: a part pointer handed to a func value would leak the handle to the heap.
	parts := 1
	if restDecode {
		parts += len(h.rest)
	}
	var arena numericMetricArena
	for k := range parts {
		arena.reserve(h.part(k))
	}
	arena.allocate()
	for k := range parts {
		arena.appendTagColumn(h.part(k))
	}
	arena.sealTagColumns()
	for k := range parts {
		h.part(k).materialize(&arena)
	}
}

// TimestampAccess reports how TimestampAt reads a point: the worst class over the blobs the handle spans.
//
// It is for callers that must stay allocation-free and want to decide for themselves how to read the metric.
func (h *NumericMetric) TimestampAccess() AccessClass {
	class := h.first.timestampAccess()
	for k := range h.rest {
		class = max(class, h.rest[k].timestampAccess())
	}

	return class
}

// ValueAccess reports how ValueAt reads a point: the worst class over the blobs the handle spans.
//
// It is for callers that must stay allocation-free and want to decide for themselves how to read the metric.
func (h *NumericMetric) ValueAccess() AccessClass {
	class := h.first.valueAccess()
	for k := range h.rest {
		class = max(class, h.rest[k].valueAccess())
	}

	return class
}

// String returns the name of the class: "Direct", "Sequential" or "Unsupported".
func (c AccessClass) String() string {
	switch c {
	case AccessDirect:
		return "Direct"
	case AccessSequential:
		return "Sequential"
	case AccessUnsupported:
		return "Unsupported"
	default:
		return "Unknown"
	}
}

// part returns the k-th part: the first, then the parts after it.
func (h *NumericMetric) part(k int) *numericMetricPart {
	if k == 0 {
		return &h.first
	}

	return &h.rest[k-1]
}

// partOrdinal returns the ordinal part takes of the part holding index, which the caller keeps within the handle:
// the last part that starts at or before index, as locateBisect chooses it.
// It runs once per range walk, so it is a plain bisection.
func (h *NumericMetric) partOrdinal(index int) int {
	if index < h.first.count {
		return 0
	}
	// k becomes the number of parts after the first that start at or before index.
	k, hi := 0, len(h.rest)
	for k < hi {
		mid := (k + hi) / 2
		if h.rest[mid].base <= index {
			k = mid + 1
		} else {
			hi = mid
		}
	}

	return k
}

// partsStartingBelow returns the number of parts whose first timestamp is below ts,
// and false when a first timestamp it needs cannot be read.
//
// It is a branching bisection, not the branch-free one countBelow runs over a blob's points:
// over thousands of parts the processor gains more from running ahead on a predicted compare,
// which targets that advance in order make right nearly every time, than it loses on a miss.
func (h *NumericMetric) partsStartingBelow(ts int64) (int, bool) {
	// The loop calls part by name: a part pointer handed to a func value would leak the handle to the heap.
	k, hi := 0, 1+len(h.rest)
	for k < hi {
		mid := (k + hi) / 2
		// The decoded column or the pre-decoded group is read in line; a probe through timestampAt is mostly the call.
		p := h.part(mid)
		first, ok := p.timestampFromSlices(0)
		if !ok || p.count == 0 {
			if first, ok = p.timestampAt(0); !ok {
				return 0, false
			}
		}
		if first < ts {
			k = mid + 1
		} else {
			hi = mid
		}
	}

	return k, true
}

// locate returns the part holding index and the index local to that part.
// A negative index, or one past the last part, lands on the first part with an index its bounds check rejects.
// It scans the parts, which suits the few blobs of a small set and is small enough to inline;
// the accessors call locateBisect instead when the handle has more than locateScanParts parts after the first.
// Each writes that choice out, because one function holding both would not inline.
func (h *NumericMetric) locate(index int) (*numericMetricPart, int) {
	if index < h.first.count {
		return &h.first, index
	}

	return h.locateRest(index)
}

// locateRest scans the parts after the first; they are contiguous and in order, so the first part
// whose local index is in range is the one.
func (h *NumericMetric) locateRest(index int) (*numericMetricPart, int) {
	for k := range h.rest {
		p := &h.rest[k]
		if local := index - p.base; local < p.count {
			return p, local
		}
	}

	return &h.first, -1
}

// locateBisect is locate for a handle on a set of many blobs, where a scan would walk up to all of them on every lookup.
// An index past the last part lands on that part with a local index its bounds check rejects.
//
// It first estimates the part from the index, as if every part held the same number of points,
// and steps outward from the estimate in doubling strides, at most locateStrides of them, before it bisects what is left.
// Blobs of similar sizes, which is what a writer on a timer produces, are placed in two or three reads of a base;
// the worst case, sizes that put the estimate far off,
// costs the estimate's read and locateStrides more than a plain bisection.
func (h *NumericMetric) locateBisect(index int) (*numericMetricPart, int) {
	if index < h.first.count {
		return &h.first, index
	}
	n := len(h.rest)
	if n == 0 {
		return &h.first, index
	}
	last := &h.rest[n-1]
	total := last.base + last.count
	if index >= total {
		return last, index - last.base
	}

	// lo becomes the last part that starts at or before index;
	// an empty part sharing its base with the part after it never is.
	// Invariant: rest[lo].base <= index, and rest[hi].base > index when hi < n.
	lo, hi := 0, n
	// A product that overflows gives a guess that is wrong, or out of range and replaced:
	// the steps below are correct from any part.
	guess := int(uint64(index) * uint64(n) / uint64(total)) //nolint:gosec // 0 <= index < total
	if uint(guess) >= uint(n) {
		guess = 0
	}
	if h.rest[guess].base <= index {
		lo = guess
		for stride, left := 1, locateStrides; left > 0 && lo+stride < hi; stride, left = stride<<1, left-1 {
			if h.rest[lo+stride].base > index {
				hi = lo + stride

				break
			}
			lo += stride
		}
	} else {
		hi = guess
		for stride, left := 1, locateStrides; left > 0 && hi-stride > lo; stride, left = stride<<1, left-1 {
			if h.rest[hi-stride].base <= index {
				lo = hi - stride

				break
			}
			hi -= stride
		}
	}
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if h.rest[mid].base <= index {
			lo = mid
		} else {
			hi = mid
		}
	}
	p := &h.rest[lo]

	return p, index - p.base
}

// restNeedsDecode reports whether any part after the first has an axis Materialize would decode.
func (h *NumericMetric) restNeedsDecode() bool {
	for k := range h.rest {
		if h.rest[k].needsDecode() {
			return true
		}
	}

	return false
}

// forEachPoints is forEachFromEntry over the part's own ranges: nothing when a required range was invalid.
// After a full Materialize it zips the decoded slices, and decoded tags are never decoded again.
func (p *numericMetricPart) forEachPoints(yield func(int, NumericDataPoint) bool) {
	if p.count == 0 || !p.tsOK || !p.valOK || (p.HasTag() && !p.tagOK) {
		return
	}
	tags := p.tags
	if len(tags) != p.count {
		tags = nil
	}
	if p.HasTag() && tags == nil {
		NumericBlob{blobBase: p.blobBase}.forEachDataPoint(p.tsBytes, p.valBytes, p.tagBytes, p.count, yield)
		return
	}
	if ts := p.timestampColumn(); ts != nil && len(p.values) == p.count {
		forEachZipped(ts, p.values, tags, yield)
		return
	}
	if tags == nil {
		NumericBlob{blobBase: p.blobBase}.forEachDataPoint(p.tsBytes, p.valBytes, nil, p.count, yield)
		return
	}

	// Decoded tags with columns that are not: walk the columns as a tagless blob and attach the tags.
	untagged := p.blobBase
	untagged.flags &^= section.FlagTagEnabled
	NumericBlob{blobBase: untagged}.forEachDataPoint(p.tsBytes, p.valBytes, nil, p.count, func(i int, dp NumericDataPoint) bool {
		if i >= len(tags) {
			return false
		}
		dp.Tag = tags[i]

		return yield(i, dp)
	})
}

// forEachValues is forEachValuesFromEntry over the part's own range, indexed from the part's base.
// It returns the index after the last value, or -1 if yield stopped the walk.
func (p *numericMetricPart) forEachValues(yield func(int, float64) bool) int {
	if p.count == 0 || !p.valOK {
		return p.base
	}
	if len(p.values) == p.count {
		for i, v := range p.values {
			if !yield(p.base+i, v) {
				return -1
			}
		}

		return p.base + p.count
	}

	return NumericBlob{blobBase: p.blobBase}.forEachValuesBytes(p.valBytes, p.count, p.base, yield)
}

// forEachTimestamps is forEachTimestampsFromEntry over the part's own range, indexed from the part's base:
// the pre-decoded group when there is one, even a short one, as NumericBlob.ForEachTimestamps yields it,
// then the column Materialize decoded in full, the column otherwise.
// It returns the index after the last timestamp, or -1 if yield stopped the walk.
func (p *numericMetricPart) forEachTimestamps(yield func(int, int64) bool) int {
	if p.count == 0 {
		return p.base
	}
	group := p.shared
	if group == nil && len(p.timestamps) == p.count {
		group = p.timestamps
	}
	if group != nil {
		for i, ts := range group {
			if !yield(p.base+i, ts) {
				return -1
			}
		}

		return p.base + len(group)
	}
	if !p.tsOK {
		return p.base
	}

	return NumericBlob{blobBase: p.blobBase}.forEachTimestampsBytes(p.tsBytes, p.count, p.base, yield)
}

// timestampAccess classifies the part's timestamp axis from its encoding and its group.
func (p *numericMetricPart) timestampAccess() AccessClass {
	switch p.tsEncType {
	case format.TypeRaw:
		return AccessDirect
	case format.TypeDelta, format.TypeDeltaPacked:
		if p.shared != nil || (p.timestamps != nil && len(p.timestamps) == p.count) {
			return AccessDirect
		}

		return AccessSequential
	case format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE:
		// Value-only encodings: header validation rejects them as timestamp encodings.
		fallthrough
	default:
		return AccessUnsupported
	}
}

// valueAccess classifies the part's value axis from its encoding and what Materialize decoded; every ALP scheme is direct.
func (p *numericMetricPart) valueAccess() AccessClass {
	switch p.valEncType {
	case format.TypeRaw, format.TypeALP, format.TypeALPRLE:
		return AccessDirect
	case format.TypeGorilla, format.TypeChimp:
		if p.values != nil && len(p.values) == p.count {
			return AccessDirect
		}

		return AccessSequential
	case format.TypeDelta, format.TypeDeltaPacked:
		// Timestamp-only encodings: header validation rejects them as value encodings.
		fallthrough
	default:
		return AccessUnsupported
	}
}

// at is At on the part's local index i past the materialized fast path: each axis reads as its accessor does.
func (p *numericMetricPart) at(i int) (NumericDataPoint, bool) {
	ts, ok := p.timestampAt(i)
	if !ok {
		return NumericDataPoint{}, false
	}
	val, ok := p.valueAt(i)
	if !ok {
		return NumericDataPoint{}, false
	}
	tag, ok := p.tagAt(i)
	if !ok {
		return NumericDataPoint{}, false
	}

	return NumericDataPoint{Ts: ts, Val: val, Tag: tag}, true
}

// timestampFromSlices reads the timestamp at local index i from the decoded column or the pre-decoded group,
// and reports false when neither covers it. The caller keeps i within the part.
func (p *numericMetricPart) timestampFromSlices(i int) (int64, bool) {
	if i < len(p.timestamps) {
		return p.timestamps[i], true
	}
	if i < len(p.shared) {
		return p.shared[i], true
	}

	return 0, false
}

// timestampAt is TimestampAt on the part's local index i, for At's fallback (TimestampAt writes it out):
// the column Materialize decoded, then the pre-decoded group,
// either one shorter than count falling through to the payload, as timestampAtFromEntry does for a short group.
func (p *numericMetricPart) timestampAt(i int) (int64, bool) {
	count := p.count
	if i < 0 || i >= count {
		return 0, false
	}
	if ts, ok := p.timestampFromSlices(i); ok {
		return ts, true
	}

	switch p.tsEncType {
	case format.TypeRaw:
		if p.sameByteOrder {
			return ienc.NewTimestampRawUnsafeDecoder(p.engine).At(p.tsBytes, i, count)
		}

		return ienc.NewTimestampRawDecoder(p.engine).At(p.tsBytes, i, count)
	case format.TypeDelta:
		return ienc.NewTimestampDeltaDecoder().At(p.tsBytes, i, count)
	case format.TypeDeltaPacked:
		var decoder ienc.TimestampDeltaPackedDecoder

		return decoder.At(p.tsBytes, i, count)
	case format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE:
		// Value-only encodings: header validation rejects them as timestamp encodings.
		fallthrough
	default:
		return 0, false
	}
}

// sortedFrom reports whether the part's timestamps are non-decreasing and none is below prev,
// the last timestamp before the part, and returns the part's last timestamp, or prev for a part without points.
// A column that cannot be read to its end is not sorted.
func (p *numericMetricPart) sortedFrom(prev int64) (int64, bool) {
	if p.count == 0 {
		return prev, true
	}
	if column := p.timestampColumn(); column != nil {
		// A group the decoder found in order needs its two ends compared, not its points.
		if p.sharedSorted && len(p.timestamps) != p.count {
			return column[len(column)-1], column[0] >= prev
		}
		for _, ts := range column {
			if ts < prev {
				return prev, false
			}
			prev = ts
		}

		return prev, true
	}

	sorted := true
	end := NumericBlob{blobBase: p.blobBase}.forEachTimestampsBytes(p.tsBytes, p.count, 0, func(_ int, ts int64) bool {
		if ts < prev {
			sorted = false

			return false
		}
		prev = ts

		return true
	})

	return prev, sorted && end == p.count
}

// lowerBound returns the first local index i whose timestamp is not below ts, with that timestamp in at.
// The caller has read the part's first timestamp and found it below ts, so i >= 1;
// i == count means every timestamp is below ts, and at is then unset.
// i == 0 reports that a timestamp it needs cannot be read, a column that ends early included.
func (p *numericMetricPart) lowerBound(ts int64) (i int, at int64) {
	// Bisect only where every probe is a direct read: a shared group shorter than count still classes as direct,
	// but its tail replays the payload on each probe, so it takes the pass.
	if column := p.timestampColumn(); column != nil {
		return lowerBoundInColumn(column, ts)
	}
	if p.tsEncType == format.TypeRaw {
		// Invariant: the timestamp at i-1 is below ts; at is the timestamp at hi when hi < count.
		i = 1
		hi := p.count
		for i < hi {
			mid := (i + hi) / 2
			v, ok := p.timestampAt(mid)
			if !ok {
				return 0, 0
			}
			if v < ts {
				i = mid + 1
			} else {
				hi, at = mid, v
			}
		}

		return i, at
	}

	return p.lowerBoundByReplay(ts)
}

// lowerBoundInColumn is lowerBound on a decoded column whose first timestamp is below ts.
func lowerBoundInColumn(column []int64, ts int64) (i int, at int64) {
	i = 1 + countBelow(column[1:], ts)
	if i < len(column) {
		at = column[i]
	}

	return i, at
}

// countBelow returns the number of elements of a non-decreasing column that are below ts.
//
// The loop runs a number of steps that depends on the column's length alone,
// and each step adds half the remaining range under a mask made from the compare, not on a branch.
// Targets that advance one point at a time took a different path through a branching bisection on every call,
// and over the few dozen points of a blob the processor mispredicted most of them.
// Only the last compare, of one element, branches.
func countBelow(column []int64, ts int64) int {
	// The answer lies in the n elements from i.
	i := 0
	n := len(column)
	for n > 1 {
		half := n >> 1
		below := 0
		if column[i+half-1] < ts {
			below = 1
		}
		i += half & -below
		n -= half
	}
	if n == 1 && column[i] < ts {
		i++
	}

	return i
}

// lowerBoundByReplay is lowerBound by one pass over the payload that stops at the first timestamp not below ts.
// A bisection would replay the column from its start on every probe.
// It is a function of its own because its callback captures the results,
// which keeps them in memory for as long as the function runs.
func (p *numericMetricPart) lowerBoundByReplay(ts int64) (i int, at int64) {
	found := false
	end := NumericBlob{blobBase: p.blobBase}.forEachTimestampsBytes(p.tsBytes, p.count, 0, func(j int, v int64) bool {
		if v >= ts {
			i, at, found = j, v, true

			return false
		}

		return true
	})
	if found {
		return i, at
	}
	if end != p.count {
		return 0, 0
	}

	return p.count, 0
}

// valueAt is ValueAt on the part's local index i, for At's fallback (ValueAt writes it out): the column Materialize decoded,
// a short decode falling through to the payload for the rest, as valueAtFromEntry does over the part's own range.
func (p *numericMetricPart) valueAt(i int) (float64, bool) {
	count := p.count
	if i < 0 || i >= count {
		return 0, false
	}
	if i < len(p.values) {
		return p.values[i], true
	}

	switch p.valEncType {
	case format.TypeRaw:
		if p.sameByteOrder {
			return ienc.NewNumericRawUnsafeDecoder(p.engine).At(p.valBytes, i, count)
		}

		return ienc.NewNumericRawDecoder(p.engine).At(p.valBytes, i, count)
	case format.TypeGorilla:
		return ienc.NewNumericGorillaDecoder().At(p.valBytes, i, count)
	case format.TypeChimp:
		return ienc.NewNumericChimpDecoder().At(p.valBytes, i, count)
	case format.TypeALP, format.TypeALPRLE:
		return ienc.NewNumericALPDecoder(p.engine).At(p.valBytes, i, count)
	case format.TypeDelta, format.TypeDeltaPacked:
		// Timestamp-only encodings: header validation rejects them as value encodings.
		fallthrough
	default:
		return 0, false
	}
}

// tagAt is TagAt on the part's local index i: empty on a blob without tags,
// the decoded tags first, then a walk of the tag column.
func (p *numericMetricPart) tagAt(i int) (string, bool) {
	count := p.count
	if i < 0 || i >= count {
		return "", false
	}
	if !p.HasTag() {
		return "", true
	}
	if i < len(p.tags) {
		return p.tags[i], true
	}

	return ienc.NewTagDecoder(p.engine).At(p.tagBytes, i, count)
}

// timestampColumn returns the part's timestamps as a slice of count elements, decoded or pre-decoded, or nil.
func (p *numericMetricPart) timestampColumn() []int64 {
	if len(p.timestamps) == p.count {
		return p.timestamps
	}
	if len(p.shared) == p.count {
		return p.shared
	}

	return nil
}

// needsDecode reports whether Materialize has an axis of the part to decode:
// a sequential timestamp or value axis, a timestamp axis whose shared group is short, or a tag column,
// readable and not decoded yet.
func (p *numericMetricPart) needsDecode() bool {
	return p.timestampsNeedDecode() || p.valuesNeedDecode() || p.tagsNeedDecode()
}

func (p *numericMetricPart) timestampsNeedDecode() bool {
	// A shared group shorter than count (malformed) leaves the rest to the payload, so it is decoded as well.
	return (p.tsEncType == format.TypeDelta || p.tsEncType == format.TypeDeltaPacked) &&
		(p.shared == nil || len(p.shared) < p.count) && p.timestamps == nil && p.tsOK
}

func (p *numericMetricPart) valuesNeedDecode() bool {
	return (p.valEncType == format.TypeGorilla || p.valEncType == format.TypeChimp) && p.values == nil && p.valOK
}

func (p *numericMetricPart) tagsNeedDecode() bool {
	return p.HasTag() && p.tags == nil && p.tagOK
}

// materialize decodes the part's axes that need it, each into a slice carved from the arena
// and cut to the points its decoder produced.
func (p *numericMetricPart) materialize(a *numericMetricArena) {
	b := NumericBlob{blobBase: p.blobBase}
	if p.timestampsNeedDecode() {
		ts := carve(&a.timestamps, p.count)
		p.timestamps = ts[:b.decodeTimestampsSlice(p.tsBytes, p.count, ts)]
	}
	if p.valuesNeedDecode() {
		vals := carve(&a.values, p.count)
		p.values = vals[:b.decodeValuesSlice(p.valBytes, p.count, vals)]
	}
	if p.tagsNeedDecode() {
		tags := carve(&a.tags, p.count)
		column := a.tagColumns[:len(p.tagBytes)]
		a.tagColumns = a.tagColumns[len(p.tagBytes):]
		var decoder ienc.TagDecoder
		p.tags = tags[:decoder.DecodeStringInto(column, tags)]
	}
}

// reserve counts the points and tag bytes p will carve, by the predicates materialize tests.
func (a *numericMetricArena) reserve(p *numericMetricPart) {
	if p.timestampsNeedDecode() {
		a.numTimestamps += p.count
		a.decodesTimestamps = true
	}
	if p.valuesNeedDecode() {
		a.numValues += p.count
		a.decodesValues = true
	}
	if p.tagsNeedDecode() {
		a.numTags += p.count
		a.numTagBytes += len(p.tagBytes)
		a.decodesTags = true
	}
}

// allocate makes one array per axis that some part decodes.
func (a *numericMetricArena) allocate() {
	if a.decodesTimestamps {
		a.timestamps = make([]int64, a.numTimestamps)
	}
	if a.decodesValues {
		a.values = make([]float64, a.numValues)
	}
	if a.decodesTags {
		a.tags = make([]string, a.numTags)
	}
	a.tagBuilder.Grow(a.numTagBytes)
}

// appendTagColumn appends p's tag column to the tag string when p decodes its tags.
func (a *numericMetricArena) appendTagColumn(p *numericMetricPart) {
	if p.tagsNeedDecode() {
		a.tagBuilder.Write(p.tagBytes)
	}
}

// sealTagColumns turns the appended tag columns into the string the parts slice their tags from.
func (a *numericMetricArena) sealTagColumns() {
	a.tagColumns = a.tagBuilder.String()
}
