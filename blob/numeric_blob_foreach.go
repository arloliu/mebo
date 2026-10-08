package blob

import (
	"iter"

	"github.com/arloliu/mebo/encoding"
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
// Prefer it in hot read paths:
// All must return a heap-allocated iterator and makes the caller's range loop body escape to the heap,
// while ForEach's static call chain keeps the callback and all decoder state on the stack.
// A call does not allocate, with two exceptions:
// ALP and ALP-RLE values decode both columns into two new slices before the first callback,
// and on a blob with tags every point's tag is a string copied out of the payload.
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

// forEachDataPoint calls the static loop of the blob's encoding pair with yield.
// It mirrors the dispatch order of allDataPoints, whose iterators call the same loops; keep the two in sync.
// Every loop is called by name and calls yield from a non-escaping literal at most,
// so escape analysis keeps a caller's capturing callback on the stack, where All must allocate an iterator.
func (b NumericBlob) forEachDataPoint(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	valEnc := b.ValueEncoding()
	// ALP and ALP-RLE values: decode both columns and zip them (avoids the generic iter.Pull overhead).
	if valEnc == format.TypeALP || valEnc == format.TypeALPRLE {
		b.forEachPointsDecoded(tsBytes, valBytes, tagBytes, count, yield)
		return
	}

	switch b.tsEncType { //nolint: exhaustive
	case format.TypeRaw:
		switch valEnc { //nolint: exhaustive
		case format.TypeRaw:
			b.forEachPointsRaw(tsBytes, valBytes, tagBytes, count, yield)
			return
		case format.TypeGorilla, format.TypeChimp:
			b.forEachPointsRawXOR(tsBytes, valBytes, tagBytes, count, yield)
			return
		default:
		}
	case format.TypeDelta, format.TypeDeltaPacked:
		if valEnc == format.TypeRaw {
			b.forEachPointsDeltaRaw(tsBytes, valBytes, tagBytes, count, yield)
			return
		}
		if valEnc == format.TypeGorilla || valEnc == format.TypeChimp {
			b.forEachPointsDeltaXOR(tsBytes, valBytes, tagBytes, count, yield)
			return
		}
	default:
	}

	b.forEachPointsGeneric(tsBytes, valBytes, tagBytes, count, yield)
}

// forEachPointsRaw dispatches Raw timestamps with Raw values on the byte order and the tag flag,
// so the loop reads through concrete decoders.
func (b NumericBlob) forEachPointsRaw(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	engine := b.Engine()
	switch {
	case b.sameByteOrder && !b.HasTag():
		forEachRaw(ienc.NewTimestampRawUnsafeDecoder(engine), ienc.NewNumericRawUnsafeDecoder(engine), tsBytes, valBytes, count, yield)
	case b.sameByteOrder:
		forEachRawTagged(ienc.NewTimestampRawUnsafeDecoder(engine), ienc.NewNumericRawUnsafeDecoder(engine), tsBytes, valBytes, tagBytes, count, yield)
	case !b.HasTag():
		forEachRaw(ienc.NewTimestampRawDecoder(engine), ienc.NewNumericRawDecoder(engine), tsBytes, valBytes, count, yield)
	default:
		forEachRawTagged(ienc.NewTimestampRawDecoder(engine), ienc.NewNumericRawDecoder(engine), tsBytes, valBytes, tagBytes, count, yield)
	}
}

// forEachPointsRawXOR dispatches Raw timestamps with Gorilla or Chimp values.
func (b NumericBlob) forEachPointsRawXOR(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	engine := b.Engine()
	if b.sameByteOrder {
		forEachRawXOR(ienc.NewTimestampRawUnsafeDecoder(engine), b.ValueEncoding(), b.HasTag(), tsBytes, valBytes, tagBytes, count, yield)
		return
	}
	forEachRawXOR(ienc.NewTimestampRawDecoder(engine), b.ValueEncoding(), b.HasTag(), tsBytes, valBytes, tagBytes, count, yield)
}

// forEachRawXOR picks the Gorilla or Chimp loop for Raw timestamps read through tsDec.
func forEachRawXOR[T encoding.ColumnarDecoder[int64]](
	tsDec T, valEnc format.EncodingType, tagged bool, tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool,
) {
	switch {
	case valEnc == format.TypeGorilla && !tagged:
		forEachRawGorilla(tsDec, tsBytes, valBytes, count, yield)
	case valEnc == format.TypeGorilla:
		forEachRawGorillaTagged(tsDec, tsBytes, valBytes, tagBytes, count, yield)
	case !tagged:
		forEachRawChimp(tsDec, tsBytes, valBytes, count, yield)
	default:
		forEachRawChimpTagged(tsDec, tsBytes, valBytes, tagBytes, count, yield)
	}
}

// forEachPointsDeltaRaw dispatches Delta or DeltaPacked timestamps with Raw values.
func (b NumericBlob) forEachPointsDeltaRaw(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	engine := b.Engine()
	if b.sameByteOrder {
		forEachDeltaRawWith(ienc.NewNumericRawUnsafeDecoder(engine), b.tsEncType, b.HasTag(), tsBytes, valBytes, tagBytes, count, yield)
		return
	}
	forEachDeltaRawWith(ienc.NewNumericRawDecoder(engine), b.tsEncType, b.HasTag(), tsBytes, valBytes, tagBytes, count, yield)
}

// forEachDeltaRawWith picks the Delta or DeltaPacked loop for Raw values read through valDec.
func forEachDeltaRawWith[V encoding.ColumnarDecoder[float64]](
	valDec V, tsEnc format.EncodingType, tagged bool, tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool,
) {
	switch {
	case tsEnc == format.TypeDelta && !tagged:
		forEachDeltaRaw(valDec, tsBytes, valBytes, count, yield)
	case tsEnc == format.TypeDelta:
		forEachDeltaRawTagged(valDec, tsBytes, valBytes, tagBytes, count, yield)
	case !tagged:
		forEachDeltaPackedRaw(valDec, tsBytes, valBytes, count, yield)
	default:
		forEachDeltaPackedRawTagged(valDec, tsBytes, valBytes, tagBytes, count, yield)
	}
}

// forEachPointsDeltaXOR dispatches Delta or DeltaPacked timestamps with Gorilla or Chimp values to the fused loops.
func (b NumericBlob) forEachPointsDeltaXOR(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	gorilla := b.ValueEncoding() == format.TypeGorilla
	if b.tsEncType == format.TypeDelta {
		switch {
		case gorilla && !b.HasTag():
			forEachDeltaGorilla(tsBytes, valBytes, count, yield)
		case gorilla:
			forEachDeltaGorillaTagged(tsBytes, valBytes, tagBytes, count, yield)
		case !b.HasTag():
			forEachDeltaChimp(tsBytes, valBytes, count, yield)
		default:
			forEachDeltaChimpTagged(tsBytes, valBytes, tagBytes, count, yield)
		}

		return
	}

	switch {
	case gorilla && !b.HasTag():
		forEachDeltaPackedGorilla(tsBytes, valBytes, count, yield)
	case gorilla:
		forEachDeltaPackedGorillaTagged(tsBytes, valBytes, tagBytes, count, yield)
	case !b.HasTag():
		forEachDeltaPackedChimp(tsBytes, valBytes, count, yield)
	default:
		forEachDeltaPackedChimpTagged(tsBytes, valBytes, tagBytes, count, yield)
	}
}

// forEachPointsDecoded decodes both columns and zips them, as allDataPointsMaterialized does.
func (b NumericBlob) forEachPointsDecoded(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	ts, vals := b.decodePointColumns(tsBytes, valBytes, count)
	if !b.HasTag() {
		forEachDecoded(ts, vals, yield)
		return
	}
	forEachDecodedTagged(ts, vals, tagBytes, yield)
}

// forEachPointsGeneric pulls the three column iterators in step, as allDataPointsGeneric does.
func (b NumericBlob) forEachPointsGeneric(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	tsIter := b.decodeTimestamps(tsBytes, count)
	valIter := b.decodeValues(valBytes, count)
	if !b.HasTag() {
		forEachPulled(tsIter, valIter, yield)
		return
	}
	forEachPulledTagged(tsIter, valIter, b.decodeTags(tagBytes, count), yield)
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

// The loops below are the bodies of the allDataPoints* iterators, shared with forEachDataPoint.
// They are package-level functions called by name, and they call yield directly or from a literal
// that the fused decoders do not retain, so escape analysis keeps yield, and a caller's capturing callback, on the stack.
// The decoder type parameters let forEachDataPoint pass concrete decoders, which are not boxed,
// while the All* iterators pass the interface values they capture.

// forEachRaw reads Raw timestamps and Raw values by index.
func forEachRaw[T encoding.ColumnarDecoder[int64], V encoding.ColumnarDecoder[float64]](
	tsDec T, valDec V, tsBytes, valBytes []byte, count int, yield func(int, NumericDataPoint) bool,
) {
	for i := range count {
		ts, _ := tsDec.At(tsBytes, i, count)
		val, _ := valDec.At(valBytes, i, count)
		if !yield(i, NumericDataPoint{Ts: ts, Val: val}) {
			return
		}
	}
}

// forEachRawTagged walks the tag column once and reads Raw timestamps and Raw values by index
// (TagAt would rescan the column from its start for every index).
func forEachRawTagged[T encoding.ColumnarDecoder[int64], V encoding.ColumnarDecoder[float64]](
	tsDec T, valDec V, tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool,
) {
	var tags ienc.TagDecoder
	tags.Each(tagBytes, count, 0, func(i int, tag string) bool {
		ts, _ := tsDec.At(tsBytes, i, count)
		val, _ := valDec.At(valBytes, i, count)

		return yield(i, NumericDataPoint{Ts: ts, Val: val, Tag: tag})
	})
}

// forEachRawGorilla runs the Gorilla loop and reads Raw timestamps by index.
func forEachRawGorilla[T encoding.ColumnarDecoder[int64]](tsDec T, tsBytes, valBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	ienc.FusedGorillaEach(valBytes, count, 0, func(i int, val float64) bool {
		ts, _ := tsDec.At(tsBytes, i, count)

		return yield(i, NumericDataPoint{Ts: ts, Val: val})
	})
}

// forEachRawGorillaTagged runs the fused Gorilla and tag loop and reads Raw timestamps by index.
func forEachRawGorillaTagged[T encoding.ColumnarDecoder[int64]](
	tsDec T, tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool,
) {
	ienc.FusedGorillaTagAll(valBytes, tagBytes, count, func(i int, val float64, tag string) bool {
		ts, _ := tsDec.At(tsBytes, i, count)

		return yield(i, NumericDataPoint{Ts: ts, Val: val, Tag: tag})
	})
}

// forEachRawChimp runs the Chimp loop and reads Raw timestamps by index.
func forEachRawChimp[T encoding.ColumnarDecoder[int64]](tsDec T, tsBytes, valBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	ienc.FusedChimpEach(valBytes, count, 0, func(i int, val float64) bool {
		ts, _ := tsDec.At(tsBytes, i, count)

		return yield(i, NumericDataPoint{Ts: ts, Val: val})
	})
}

// forEachRawChimpTagged runs the fused Chimp and tag loop and reads Raw timestamps by index.
func forEachRawChimpTagged[T encoding.ColumnarDecoder[int64]](
	tsDec T, tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool,
) {
	ienc.FusedChimpTagAll(valBytes, tagBytes, count, func(i int, val float64, tag string) bool {
		ts, _ := tsDec.At(tsBytes, i, count)

		return yield(i, NumericDataPoint{Ts: ts, Val: val, Tag: tag})
	})
}

// forEachDeltaRaw runs the Delta loop and reads Raw values by index.
func forEachDeltaRaw[V encoding.ColumnarDecoder[float64]](valDec V, tsBytes, valBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	ienc.FusedDeltaEach(tsBytes, count, 0, func(i int, ts int64) bool {
		val, _ := valDec.At(valBytes, i, count)

		return yield(i, NumericDataPoint{Ts: ts, Val: val})
	})
}

// forEachDeltaRawTagged runs the fused Delta and tag loop and reads Raw values by index.
func forEachDeltaRawTagged[V encoding.ColumnarDecoder[float64]](
	valDec V, tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool,
) {
	ienc.FusedDeltaTagAll(tsBytes, tagBytes, count, func(i int, ts int64, tag string) bool {
		val, _ := valDec.At(valBytes, i, count)

		return yield(i, NumericDataPoint{Ts: ts, Val: val, Tag: tag})
	})
}

// forEachDeltaPackedRaw runs the DeltaPacked loop and reads Raw values by index.
func forEachDeltaPackedRaw[V encoding.ColumnarDecoder[float64]](
	valDec V, tsBytes, valBytes []byte, count int, yield func(int, NumericDataPoint) bool,
) {
	ienc.FusedDeltaPackedEach(tsBytes, count, 0, func(i int, ts int64) bool {
		val, _ := valDec.At(valBytes, i, count)

		return yield(i, NumericDataPoint{Ts: ts, Val: val})
	})
}

// forEachDeltaPackedRawTagged runs the fused DeltaPacked and tag loop and reads Raw values by index.
func forEachDeltaPackedRawTagged[V encoding.ColumnarDecoder[float64]](
	valDec V, tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool,
) {
	ienc.FusedDeltaPackedTagAll(tsBytes, tagBytes, count, func(i int, ts int64, tag string) bool {
		val, _ := valDec.At(valBytes, i, count)

		return yield(i, NumericDataPoint{Ts: ts, Val: val, Tag: tag})
	})
}

// forEachDeltaGorillaTagged runs the fused Delta, Gorilla and tag loop.
func forEachDeltaGorillaTagged(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	ienc.FusedDeltaGorillaTagAll(tsBytes, valBytes, tagBytes, count, func(i int, ts int64, val float64, tag string) bool {
		return yield(i, NumericDataPoint{Ts: ts, Val: val, Tag: tag})
	})
}

// forEachDeltaChimpTagged runs the fused Delta, Chimp and tag loop.
func forEachDeltaChimpTagged(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	ienc.FusedDeltaChimpTagAll(tsBytes, valBytes, tagBytes, count, func(i int, ts int64, val float64, tag string) bool {
		return yield(i, NumericDataPoint{Ts: ts, Val: val, Tag: tag})
	})
}

// forEachDeltaPackedGorilla runs the fused DeltaPacked and Gorilla loop.
func forEachDeltaPackedGorilla(tsBytes, valBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	ienc.FusedDeltaPackedGorillaEach(tsBytes, valBytes, count, func(i int, ts int64, val float64) bool {
		return yield(i, NumericDataPoint{Ts: ts, Val: val})
	})
}

// forEachDeltaPackedGorillaTagged runs the fused DeltaPacked, Gorilla and tag loop.
func forEachDeltaPackedGorillaTagged(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	ienc.FusedDeltaPackedGorillaTagAll(tsBytes, valBytes, tagBytes, count, func(i int, ts int64, val float64, tag string) bool {
		return yield(i, NumericDataPoint{Ts: ts, Val: val, Tag: tag})
	})
}

// forEachDeltaPackedChimp runs the fused DeltaPacked and Chimp loop.
func forEachDeltaPackedChimp(tsBytes, valBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	ienc.FusedDeltaPackedChimpEach(tsBytes, valBytes, count, func(i int, ts int64, val float64) bool {
		return yield(i, NumericDataPoint{Ts: ts, Val: val})
	})
}

// forEachDeltaPackedChimpTagged runs the fused DeltaPacked, Chimp and tag loop.
func forEachDeltaPackedChimpTagged(tsBytes, valBytes, tagBytes []byte, count int, yield func(int, NumericDataPoint) bool) {
	ienc.FusedDeltaPackedChimpTagAll(tsBytes, valBytes, tagBytes, count, func(i int, ts int64, val float64, tag string) bool {
		return yield(i, NumericDataPoint{Ts: ts, Val: val, Tag: tag})
	})
}

// forEachDecoded zips decoded timestamp and value columns of equal length.
func forEachDecoded(ts []int64, vals []float64, yield func(int, NumericDataPoint) bool) {
	for i := range ts {
		if !yield(i, NumericDataPoint{Ts: ts[i], Val: vals[i]}) {
			return
		}
	}
}

// forEachDecodedTagged zips decoded timestamp and value columns of equal length with the tag column;
// a tag column that ends early ends the walk, so only complete rows are yielded.
func forEachDecodedTagged(ts []int64, vals []float64, tagBytes []byte, yield func(int, NumericDataPoint) bool) {
	var tags ienc.TagDecoder
	tags.Each(tagBytes, len(ts), 0, func(i int, tag string) bool {
		return yield(i, NumericDataPoint{Ts: ts[i], Val: vals[i], Tag: tag})
	})
}

// forEachPulled pulls the timestamp and value iterators in step until either ends.
func forEachPulled(tsIter iter.Seq[int64], valIter iter.Seq[float64], yield func(int, NumericDataPoint) bool) {
	tsNext, tsStop := iter.Pull(tsIter)
	valNext, valStop := iter.Pull(valIter)
	defer tsStop()
	defer valStop()

	for i := 0; ; i++ {
		ts, tsOk := tsNext()
		val, valOk := valNext()
		if !tsOk || !valOk || !yield(i, NumericDataPoint{Ts: ts, Val: val}) {
			return
		}
	}
}

// forEachPulledTagged pulls the timestamp, value and tag iterators in step until any ends.
func forEachPulledTagged(tsIter iter.Seq[int64], valIter iter.Seq[float64], tagIter iter.Seq[string], yield func(int, NumericDataPoint) bool) {
	tsNext, tsStop := iter.Pull(tsIter)
	valNext, valStop := iter.Pull(valIter)
	tagNext, tagStop := iter.Pull(tagIter)
	defer tsStop()
	defer valStop()
	defer tagStop()

	for i := 0; ; i++ {
		ts, tsOk := tsNext()
		val, valOk := valNext()
		tag, tagOk := tagNext()
		if !tsOk || !valOk || !tagOk || !yield(i, NumericDataPoint{Ts: ts, Val: val, Tag: tag}) {
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
