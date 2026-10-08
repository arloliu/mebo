package blob

import (
	"github.com/arloliu/mebo/format"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/internal/pool"
	"github.com/arloliu/mebo/section"
)

// ForEach calls yield for each data point of the given metric ID in insertion
// order, stopping early if yield returns false. The index passed to yield
// starts at 0 and increments for each data point.
//
// ForEach is the callback (push) equivalent of All and yields identical data.
// Prefer it in hot read paths: All must return a heap-allocated iterator and
// makes the caller's range loop body escape to the heap, while ForEach's
// static call chain keeps the callback and all decoder state on the stack —
// zero allocations per call on the optimized encoding combinations.
//
// Parameters:
//   - metricID: The metric ID to iterate over.
//   - yield: Callback receiving (0-based index, data point); return false to
//     stop iteration early.
//
// Returns:
//   - bool: false if the metric ID does not exist in the blob, true otherwise
//     (including when iteration was stopped early by yield).
//
// Example:
//
//	blob.ForEach(metricID, func(idx int, dp NumericDataPoint) bool {
//	    fmt.Printf("[%d] ts=%d, val=%f\n", idx, dp.Ts, dp.Val)
//	    return true
//	})
func (b NumericBlob) ForEach(metricID uint64, yield func(idx int, dp NumericDataPoint) bool) bool {
	if yield == nil {
		return false
	}

	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return false
	}

	b.forEachFromEntry(entry, yield)

	return true
}

// ForEachByName calls yield for each data point of the given metric name in
// insertion order, stopping early if yield returns false.
//
// See ForEach for semantics and performance characteristics.
//
// Returns:
//   - bool: false if the metric name does not exist in the blob, true
//     otherwise (including when iteration was stopped early by yield).
func (b NumericBlob) ForEachByName(metricName string, yield func(idx int, dp NumericDataPoint) bool) bool {
	if yield == nil {
		return false
	}

	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return false
	}

	b.forEachFromEntry(entry, yield)

	return true
}

// forEachFromEntry slices the payloads for the entry and dispatches to the
// encoding-specific iteration body.
func (b NumericBlob) forEachFromEntry(entry section.NumericIndexEntry, yield func(int, NumericDataPoint) bool) {
	if entry.Count == 0 {
		return
	}

	// Guard against corrupt/crafted index entries whose offsets fall outside the
	// payloads; return silently rather than panicking on the slice. Shares the
	// overflow-safe bounds helpers with the All/random-access paths.
	tsBytes, tsOk := safeSlice(b.tsPayload, entry.TimestampOffset, entry.TimestampLength)
	valBytes, valOk := safeSlice(b.valPayload, entry.ValueOffset, entry.ValueLength)
	if !tsOk || !valOk {
		return
	}

	var tagBytes []byte
	if b.HasTag() && len(b.tagPayload) > 0 {
		var tagOk bool
		tagBytes, tagOk = safeSlice(b.tagPayload, entry.TagOffset, entry.TagLength)
		if !tagOk {
			return
		}
	}

	b.forEachDataPoint(tsBytes, valBytes, tagBytes, entry.Count, yield)
}

// forEachDataPoint invokes the combo-specific iteration body directly with yield.
// It mirrors the dispatch order of allDataPoints; keep the two in sync.
// The iterator closures the allDataPoints* variants return are invoked in this frame,
// so no iterator is allocated here, where All must allocate one.
// The variants themselves are not inlinable,
// so the call through the returned closure is dynamic and escape analysis treats yield as leaking:
// a caller's capturing callback is moved to the heap on this path,
// unlike ForEachValues and ForEachTimestamps, whose loops call yield by name.
func (b NumericBlob) forEachDataPoint(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	// ALP and ALP-RLE values: materialize ts+values and zip (avoids generic iter.Pull overhead).
	if enc := b.ValueEncoding(); enc == format.TypeALP || enc == format.TypeALPRLE {
		b.allDataPointsMaterialized(tsBytes, valBytes, tagBytes, count)(yield)
		return
	}

	if b.tsEncType == format.TypeRaw && b.ValueEncoding() == format.TypeRaw {
		b.allDataPointsRaw(tsBytes, valBytes, tagBytes, count)(yield)
		return
	}

	if b.tsEncType == format.TypeRaw && b.ValueEncoding() == format.TypeGorilla {
		b.allDataPointsRawGorilla(tsBytes, valBytes, tagBytes, count)(yield)
		return
	}

	if b.tsEncType == format.TypeRaw && b.ValueEncoding() == format.TypeChimp {
		b.allDataPointsRawChimp(tsBytes, valBytes, tagBytes, count)(yield)
		return
	}

	if b.tsEncType == format.TypeDelta && b.ValueEncoding() == format.TypeGorilla {
		if !b.HasTag() {
			forEachDeltaGorilla(tsBytes, valBytes, count, yield)
			return
		}
		b.allDataPointsDeltaGorilla(tsBytes, valBytes, tagBytes, count)(yield)

		return
	}

	if b.tsEncType == format.TypeDelta && b.ValueEncoding() == format.TypeChimp {
		if !b.HasTag() {
			forEachDeltaChimp(tsBytes, valBytes, count, yield)
			return
		}
		b.allDataPointsDeltaChimp(tsBytes, valBytes, tagBytes, count)(yield)

		return
	}

	if b.tsEncType == format.TypeDelta && b.ValueEncoding() == format.TypeRaw {
		b.allDataPointsDeltaRaw(tsBytes, valBytes, tagBytes, count)(yield)
		return
	}

	if b.tsEncType == format.TypeDeltaPacked {
		switch b.ValueEncoding() { //nolint: exhaustive
		case format.TypeGorilla:
			b.allDataPointsDeltaPackedGorilla(tsBytes, valBytes, tagBytes, count)(yield)
			return
		case format.TypeChimp:
			b.allDataPointsDeltaPackedChimp(tsBytes, valBytes, tagBytes, count)(yield)
			return
		case format.TypeRaw:
			b.allDataPointsDeltaPackedRaw(tsBytes, valBytes, tagBytes, count)(yield)
			return
		}
	}

	b.allDataPointsGeneric(tsBytes, valBytes, tagBytes, count)(yield)
}

// forEachDeltaGorilla runs the fused delta+gorilla decode loop inline so the
// user's yield is the only indirect call per element. This must stay a static
// package-level function: the same loop inside a heap-allocated closure body
// measures ~20% slower (see docs/perf/iterate_closure_optimization.md).
func forEachDeltaGorilla(tsBytes, valBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	if count == 0 || len(tsBytes) == 0 || len(valBytes) == 0 {
		return
	}

	ts, tsOk := ienc.NewDeltaTsState(tsBytes)
	if !tsOk {
		return
	}

	val, valOk := ienc.NewGorillaValState(valBytes)
	if !valOk {
		return
	}

	if !yield(0, NumericDataPoint{Ts: ts.Ts(), Val: val.Val()}) {
		return
	}

	for i := 1; i < count; i++ {
		if !ts.NextShort(tsBytes) && !ts.NextLong(tsBytes) {
			return
		}

		if !val.Next() {
			return
		}

		if !yield(i, NumericDataPoint{Ts: ts.Ts(), Val: val.Val()}) {
			return
		}
	}
}

// forEachDeltaChimp runs the fused delta+chimp decode loop inline so the
// user's yield is the only indirect call per element. Like forEachDeltaGorilla,
// this must stay a static package-level function.
func forEachDeltaChimp(tsBytes, valBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	if count == 0 || len(tsBytes) == 0 || len(valBytes) == 0 {
		return
	}

	ts, tsOk := ienc.NewDeltaTsState(tsBytes)
	if !tsOk {
		return
	}

	val, valOk := ienc.NewChimpValState(valBytes)
	if !valOk {
		return
	}

	if !yield(0, NumericDataPoint{Ts: ts.Ts(), Val: val.Val()}) {
		return
	}

	for i := 1; i < count; i++ {
		if !ts.NextShort(tsBytes) && !ts.NextLong(tsBytes) {
			return
		}

		if !val.Next() {
			return
		}

		if !yield(i, NumericDataPoint{Ts: ts.Ts(), Val: val.Val()}) {
			return
		}
	}
}

// ForEachValues calls yield for each value of the given metric ID in insertion
// order, stopping early if yield returns false. The index passed to yield
// starts at 0 and increments for each value.
//
// ForEachValues is the callback (push) equivalent of AllValues and yields
// identical data. Prefer it in hot read paths: AllValues must return a
// heap-allocated iterator and makes the caller's range loop body escape to the
// heap, while ForEachValues avoids that iterator.
// For Raw, Gorilla and Chimp values it dispatches straight to a static decode loop
// that keeps the callback and decoder cursor on the stack, so a call does not allocate;
// for Gorilla and Chimp it is also faster because the XOR decode state stays in registers.
// ALP and ALP-RLE columns of up to 8192 points (pool.MaxPooledDecodeFloat64s) are bulk-decoded
// into a pooled buffer first: a full traversal is faster than per-value decoding,
// and a call does not allocate when the pool holds a large enough buffer.
// The whole column is decoded before the first callback, even if yield stops early.
// Longer ALP and ALP-RLE columns stream through the codec iterator instead,
// which allocates the iterator but keeps memory independent of the column length.
//
// Parameters:
//   - metricID: The metric ID to iterate over.
//   - yield: Callback receiving (0-based index, value); return false to stop
//     iteration early.
//
// Returns:
//   - bool: false if the metric ID does not exist in the blob, true otherwise
//     (including when iteration was stopped early by yield).
func (b NumericBlob) ForEachValues(metricID uint64, yield func(idx int, val float64) bool) bool {
	if yield == nil {
		return false
	}

	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return false
	}

	b.forEachValuesFromEntry(entry, 0, yield)

	return true
}

// ForEachValuesByName calls yield for each value of the given metric name in
// insertion order, stopping early if yield returns false.
//
// See ForEachValues for semantics and performance characteristics.
//
// Returns:
//   - bool: false if the metric name does not exist in the blob, true otherwise
//     (including when iteration was stopped early by yield).
func (b NumericBlob) ForEachValuesByName(metricName string, yield func(idx int, val float64) bool) bool {
	if yield == nil {
		return false
	}

	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return false
	}

	b.forEachValuesFromEntry(entry, 0, yield)

	return true
}

// ForEachTimestamps calls yield for each timestamp of the given metric ID in
// insertion order, stopping early if yield returns false. The index passed to
// yield starts at 0 and increments for each timestamp.
//
// ForEachTimestamps is the callback (push) equivalent of AllTimestamps and
// yields identical data. See ForEachValues for the performance rationale; the
// timestamp codecs (Delta / DeltaPacked) get the same stack-state speedup as
// the XOR value codecs. The shared-timestamp cache fast path is honored.
//
// Returns:
//   - bool: false if the metric ID does not exist in the blob, true otherwise
//     (including when iteration was stopped early by yield).
func (b NumericBlob) ForEachTimestamps(metricID uint64, yield func(idx int, ts int64) bool) bool {
	if yield == nil {
		return false
	}

	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return false
	}

	b.forEachTimestampsFromEntry(entry, 0, yield)

	return true
}

// ForEachTimestampsByName calls yield for each timestamp of the given metric
// name in insertion order, stopping early if yield returns false.
//
// See ForEachTimestamps for semantics and performance characteristics.
//
// Returns:
//   - bool: false if the metric name does not exist in the blob, true otherwise
//     (including when iteration was stopped early by yield).
func (b NumericBlob) ForEachTimestampsByName(metricName string, yield func(idx int, ts int64) bool) bool {
	if yield == nil {
		return false
	}

	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return false
	}

	b.forEachTimestampsFromEntry(entry, 0, yield)

	return true
}

// forEachValuesFromEntry slices the value payload for the entry and dispatches
// to the encoding-specific static decode loop. It mirrors decodeValues; keep
// the two in sync.
//
// Indexes passed to yield start at base. Returns the index after the last
// yielded value, or -1 if yield returned false, so a set can chain members
// with continuous indexes and no adapter closure.
func (b NumericBlob) forEachValuesFromEntry(entry section.NumericIndexEntry, base int, yield func(int, float64) bool) int {
	if entry.Count == 0 {
		return base
	}

	valBytes, ok := safeSlice(b.valPayload, entry.ValueOffset, entry.ValueLength)
	if !ok {
		return base
	}

	return b.forEachValuesBytes(valBytes, entry.Count, base, yield)
}

// forEachValuesBytes is the dispatch half of forEachValuesFromEntry,
// over a column already cut from the payload,
// so a caller that already holds the column, such as a NumericMetric part,
// shares the same decode loops.
// Indexes and the result follow forEachValuesFromEntry.
func (b NumericBlob) forEachValuesBytes(valBytes []byte, count, base int, yield func(int, float64) bool) int {
	switch b.ValueEncoding() { //nolint:exhaustive // an encoding without an Each loop yields nothing
	case format.TypeGorilla:
		return ienc.FusedGorillaEach(valBytes, count, base, yield)
	case format.TypeChimp:
		return ienc.FusedChimpEach(valBytes, count, base, yield)
	case format.TypeRaw:
		return ienc.RawValuesEach(valBytes, count, base, b.Engine(), b.sameByteOrder, yield)
	case format.TypeALP, format.TypeALPRLE:
		if count <= pool.MaxPooledDecodeFloat64s {
			return b.forEachALPValues(valBytes, count, base, yield)
		}

		// Past the pool's cap the column streams through the codec's own Each loop, which calls yield directly.
		return ienc.NewNumericALPDecoder(b.Engine()).Each(valBytes, count, base, yield)
	default:
		// An encoding without a static Each loop yields nothing, as decodeValues does for it;
		// see forEachTimestampsBytes for why its iterator is not drained here.
		return base
	}
}

// forEachTimestampsFromEntry slices the timestamp payload for the entry and
// dispatches to the encoding-specific static decode loop. It mirrors
// allTimestampsFromEntry (including the shared-TS cache fast path); keep them in
// sync.
//
// Indexes and the result follow forEachValuesFromEntry.
func (b NumericBlob) forEachTimestampsFromEntry(entry section.NumericIndexEntry, base int, yield func(int, int64) bool) int {
	if entry.Count == 0 {
		return base
	}

	// Fast path: yield cached pre-decoded shared timestamps.
	if cached := b.sharedTs.lookup(entry.TimestampOffset); cached != nil {
		for i, ts := range cached {
			if !yield(base+i, ts) {
				return -1
			}
		}

		return base + len(cached)
	}

	tsBytes, ok := safeSlice(b.tsPayload, entry.TimestampOffset, entry.TimestampLength)
	if !ok {
		return base
	}

	return b.forEachTimestampsBytes(tsBytes, entry.Count, base, yield)
}

// forEachTimestampsBytes is the dispatch half of forEachTimestampsFromEntry,
// over a column already cut from the payload and not served by a group,
// so a caller that already holds the column, such as a NumericMetric part,
// shares the same decode loops.
// Indexes and the result follow forEachValuesFromEntry.
func (b NumericBlob) forEachTimestampsBytes(tsBytes []byte, count, base int, yield func(int, int64) bool) int {
	switch b.tsEncType { //nolint:exhaustive // an encoding without an Each loop yields nothing
	case format.TypeDelta:
		return ienc.FusedDeltaEach(tsBytes, count, base, yield)
	case format.TypeDeltaPacked:
		return ienc.FusedDeltaPackedEach(tsBytes, count, base, yield)
	case format.TypeRaw:
		return ienc.RawTimestampsEach(tsBytes, count, base, b.Engine(), b.sameByteOrder, yield)
	default:
		// An encoding without a static Each loop yields nothing, as decodeTimestamps does for it.
		// Draining its iterator here with a range-over-func body would capture yield in a closure
		// and make escape analysis move every caller's callback to the heap, on every path of ForEachTimestamps.
		return base
	}
}

// forEachALPValues bulk-decodes an ALP or ALP-RLE column into a pooled buffer and yields from it.
// ALP has no stateful per-point decoder, so one DecodeAll beats draining the codec's All iterator,
// and a pooled buffer avoids allocating per call.
// The caller keeps count within pool.MaxPooledDecodeFloat64s, so the pool's retained memory stays bounded;
// the pool is separate from the encoder's scratch pool.
// It yields only the values DecodeAll produced, the same rows the iterator yields for a validated column,
// and returns like forEachValuesFromEntry: the index after the last value, or -1 if yield stopped.
// yield receives values, never the buffer, and the deferred Put runs only after the last callback returns,
// so a yield that re-enters ForEachValues or panics never sees its buffer reused.
func (b NumericBlob) forEachALPValues(valBytes []byte, count, base int, yield func(int, float64) bool) int {
	ptr := pool.GetDecodeFloat64Slice(count)
	defer pool.PutDecodeFloat64Slice(ptr)

	buf := *ptr
	n := b.decodeValuesSlice(valBytes, count, buf)
	for i, v := range buf[:n] {
		if !yield(base+i, v) {
			return -1
		}
	}

	return base + n
}
