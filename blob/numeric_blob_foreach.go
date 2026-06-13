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
