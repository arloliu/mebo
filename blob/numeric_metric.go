package blob

import (
	"slices"
	"strings"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/format"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/section"
)

// AccessClass says how a NumericMetric serves a random access on one axis.
//
// The class names the mechanism, not a complexity bound:
// AccessDirect means the lookup never walks the column's points,
// AccessSequential means it replays the column from its start up to the index,
// and AccessUnsupported means the axis cannot be read and every lookup on it returns false.
type AccessClass uint8

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
func numericMetricAcrossBlobs(blobs []NumericBlob, r setEntryResolver) (h NumericMetric, ok bool) {
	first := -1
	members := 0
	var firstEntry *section.NumericIndexEntry
	for i := range blobs {
		entry := r.entry(&blobs[i])
		if entry == nil {
			continue
		}
		if first < 0 {
			first, firstEntry = i, entry
		}
		members++
	}
	if members == 0 {
		return NumericMetric{}, false
	}
	fillNumericMetricPart(&h.first, &blobs[first], firstEntry, 0)
	if members == 1 {
		return h, true
	}

	h.rest = make([]numericMetricPart, members-1)
	base := h.first.count
	k := 0
	for i := first + 1; i < len(blobs); i++ {
		entry := r.entry(&blobs[i])
		if entry == nil {
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
// Resolution allocates nothing when one member holds the metric and once when several do.
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
// Resolution allocates nothing when one member holds the metric and once when several do.
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
	// A materialized part serves the point from its slices after one placement.
	if p, i := h.locate(index); i >= 0 && i < len(p.values) && (i < len(p.tags) || !p.HasTag()) {
		if ts, ok := p.timestampFromSlices(i); ok {
			dp := NumericDataPoint{Ts: ts, Val: p.values[i]}
			if i < len(p.tags) {
				dp.Tag = p.tags[i]
			}

			return dp, true
		}
	}

	ts, ok := h.TimestampAt(index)
	if !ok {
		return NumericDataPoint{}, false
	}
	val, ok := h.ValueAt(index)
	if !ok {
		return NumericDataPoint{}, false
	}
	tag, ok := h.TagAt(index)
	if !ok {
		return NumericDataPoint{}, false
	}

	return NumericDataPoint{Ts: ts, Val: val, Tag: tag}, true
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
	p, i := h.locate(index)
	count := p.count
	if i < 0 || i >= count {
		return 0, false
	}

	// The column Materialize decoded first; a short decode falls through to the payload for the rest.
	if i < len(p.values) {
		return p.values[i], true
	}

	// valueAtFromEntry over the part's own range.
	switch p.valEncType { //nolint: exhaustive
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
	p, i := h.locate(index)
	count := p.count
	if i < 0 || i >= count {
		return 0, false
	}
	// The column Materialize decoded, then the pre-decoded group;
	// either one shorter than count falls through to the payload, as timestampAtFromEntry does for a short group.
	if i < len(p.timestamps) {
		return p.timestamps[i], true
	}
	if i < len(p.shared) {
		return p.shared[i], true
	}

	switch p.tsEncType { //nolint: exhaustive
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
	default:
		return 0, false
	}
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
	p, i := h.locate(index)
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

// locate returns the part holding index and the index local to that part.
// A negative index, or one past the last part, lands on the first part with an index its bounds check rejects.
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
	switch p.tsEncType { //nolint: exhaustive
	case format.TypeRaw:
		return AccessDirect
	case format.TypeDelta, format.TypeDeltaPacked:
		if p.shared != nil || (p.timestamps != nil && len(p.timestamps) == p.count) {
			return AccessDirect
		}

		return AccessSequential
	default:
		return AccessUnsupported
	}
}

// valueAccess classifies the part's value axis from its encoding and what Materialize decoded; every ALP scheme is direct.
func (p *numericMetricPart) valueAccess() AccessClass {
	switch p.valEncType { //nolint: exhaustive
	case format.TypeRaw, format.TypeALP, format.TypeALPRLE:
		return AccessDirect
	case format.TypeGorilla, format.TypeChimp:
		if p.values != nil && len(p.values) == p.count {
			return AccessDirect
		}

		return AccessSequential
	default:
		return AccessUnsupported
	}
}

// timestampFromSlices reads the timestamp at local index i from the decoded column or the pre-decoded group,
// and returns false when neither holds it.
func (p *numericMetricPart) timestampFromSlices(i int) (int64, bool) {
	if i < len(p.timestamps) {
		return p.timestamps[i], true
	}
	if i < len(p.shared) {
		return p.shared[i], true
	}

	return 0, false
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
