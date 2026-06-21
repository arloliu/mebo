package blob

import (
	"github.com/arloliu/mebo/format"
	ienc "github.com/arloliu/mebo/internal/encoding"
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

// forEachDataPoint invokes the combo-specific iteration body directly with
// yield. It mirrors the dispatch order of allDataPoints (keep the two in
// sync). The allDataPoints* variants are inlinable, so the iterator closure
// they return is constructed and invoked in this frame and never escapes —
// this is what makes ForEach allocation-free where All cannot be.
func (b NumericBlob) forEachDataPoint(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	// ALP values: materialize ts+values and zip (avoids generic iter.Pull overhead).
	if b.ValueEncoding() == format.TypeALP {
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
// measures ~20% slower (see docs/perf/ITERATE_CLOSURE_OPTIMIZATION.md).
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
		if !ts.Next(tsBytes) {
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
		if !ts.Next(tsBytes) {
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
// heap, while ForEachValues dispatches straight to a static decode loop that
// keeps the callback and decoder cursor on the stack — allocation-free per
// call. For the stateful value codecs (Gorilla/Chimp) it is also faster because
// the XOR decode state stays in registers instead of a heap closure.
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

	b.forEachValuesFromEntry(entry, yield)

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

	b.forEachValuesFromEntry(entry, yield)

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

	b.forEachTimestampsFromEntry(entry, yield)

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

	b.forEachTimestampsFromEntry(entry, yield)

	return true
}

// forEachValuesFromEntry slices the value payload for the entry and dispatches
// to the encoding-specific static decode loop. It mirrors decodeValues; keep
// the two in sync.
func (b NumericBlob) forEachValuesFromEntry(entry section.NumericIndexEntry, yield func(int, float64) bool) {
	if entry.Count == 0 {
		return
	}

	valBytes, ok := safeSlice(b.valPayload, entry.ValueOffset, entry.ValueLength)
	if !ok {
		return
	}

	switch b.ValueEncoding() { //nolint:exhaustive // default branch drains the remaining codecs
	case format.TypeGorilla:
		ienc.FusedGorillaEach(valBytes, entry.Count, yield)
	case format.TypeChimp:
		ienc.FusedChimpEach(valBytes, entry.Count, yield)
	case format.TypeRaw:
		ienc.RawValuesEach(valBytes, entry.Count, b.Engine(), b.sameByteOrder, yield)
	default:
		// ALP (and any future codec without a static Each) drains the
		// slice-decode iterator. For a single column this matches AllValues
		// exactly — no iter.Pull — so there is no regression; it just does not
		// get the stack-state speedup.
		i := 0
		for v := range b.decodeValues(valBytes, entry.Count) {
			if !yield(i, v) {
				return
			}
			i++
		}
	}
}

// forEachTimestampsFromEntry slices the timestamp payload for the entry and
// dispatches to the encoding-specific static decode loop. It mirrors
// allTimestampsFromEntry (including the shared-TS cache fast path); keep them in
// sync.
func (b NumericBlob) forEachTimestampsFromEntry(entry section.NumericIndexEntry, yield func(int, int64) bool) {
	if entry.Count == 0 {
		return
	}

	// Fast path: yield cached pre-decoded shared timestamps.
	if cached, ok := b.sharedTsCache[entry.TimestampOffset]; ok {
		for i, ts := range cached {
			if !yield(i, ts) {
				return
			}
		}

		return
	}

	tsBytes, ok := safeSlice(b.tsPayload, entry.TimestampOffset, entry.TimestampLength)
	if !ok {
		return
	}

	switch b.tsEncType { //nolint:exhaustive // default branch drains the remaining codecs
	case format.TypeDelta:
		ienc.FusedDeltaEach(tsBytes, entry.Count, yield)
	case format.TypeDeltaPacked:
		ienc.FusedDeltaPackedEach(tsBytes, entry.Count, yield)
	case format.TypeRaw:
		ienc.RawTimestampsEach(tsBytes, entry.Count, b.Engine(), b.sameByteOrder, yield)
	default:
		i := 0
		for ts := range b.decodeTimestamps(tsBytes, entry.Count) {
			if !yield(i, ts) {
				return
			}
			i++
		}
	}
}
