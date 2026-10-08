package blob

import (
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
	// Timestamps: Raw, or a sequence pre-decoded at open because two or more metrics of the blob share it.
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
// Copying a handle copies slice headers over the same bytes, so a copy serves the same reads.
//
// TimestampAt and ValueAt read the point directly or replay the column up to it,
// as the encodings and the metric's shared-timestamp group decide, not the encoder options alone;
// TimestampAccess and ValueAccess report it.
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
// It decodes each blob's column once on the stack, as NumericBlob.ForEach does.
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
// It decodes each blob's value column once on the stack, as NumericBlob.ForEachValues does.
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
// A blob's pre-decoded shared group is yielded as it is, as NumericBlob.ForEachTimestamps does;
// any other column is decoded once on the stack.
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
	// The pre-decoded group first; a group shorter than count falls through to the payload, as timestampAtFromEntry does.
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
// Tags are variable-length strings walked from the column start, so TagAt is sequential on every tagged blob.
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

	return ienc.NewTagDecoder(p.engine).At(p.tagBytes, i, count)
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

// forEachPoints is forEachFromEntry over the part's own ranges: nothing when a required range was invalid.
func (p *numericMetricPart) forEachPoints(yield func(int, NumericDataPoint) bool) {
	if p.count == 0 || !p.tsOK || !p.valOK || (p.HasTag() && !p.tagOK) {
		return
	}
	NumericBlob{blobBase: p.blobBase}.forEachDataPoint(p.tsBytes, p.valBytes, p.tagBytes, p.count, yield)
}

// forEachValues is forEachValuesFromEntry over the part's own range, indexed from the part's base.
// It returns the index after the last value, or -1 if yield stopped the walk.
func (p *numericMetricPart) forEachValues(yield func(int, float64) bool) int {
	if p.count == 0 || !p.valOK {
		return p.base
	}

	return NumericBlob{blobBase: p.blobBase}.forEachValuesBytes(p.valBytes, p.count, p.base, yield)
}

// forEachTimestamps is forEachTimestampsFromEntry over the part's own range, indexed from the part's base:
// the pre-decoded group when there is one, the column otherwise.
// It returns the index after the last timestamp, or -1 if yield stopped the walk.
func (p *numericMetricPart) forEachTimestamps(yield func(int, int64) bool) int {
	if p.count == 0 {
		return p.base
	}
	if p.shared != nil {
		for i, ts := range p.shared {
			if !yield(p.base+i, ts) {
				return -1
			}
		}

		return p.base + len(p.shared)
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
		if p.shared != nil {
			return AccessDirect
		}

		return AccessSequential
	default:
		return AccessUnsupported
	}
}

// valueAccess classifies the part's value axis from its encoding; every ALP scheme is direct.
func (p *numericMetricPart) valueAccess() AccessClass {
	switch p.valEncType { //nolint: exhaustive
	case format.TypeRaw, format.TypeALP, format.TypeALPRLE:
		return AccessDirect
	case format.TypeGorilla, format.TypeChimp:
		return AccessSequential
	default:
		return AccessUnsupported
	}
}
