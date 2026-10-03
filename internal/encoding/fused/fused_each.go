package fused

import (
	tsdelta "github.com/arloliu/mebo/internal/encoding/timestamp/delta"
	"github.com/arloliu/mebo/internal/encoding/timestamp/deltapacked"
	"github.com/arloliu/mebo/internal/encoding/value/chimp"
	"github.com/arloliu/mebo/internal/encoding/value/gorilla"
)

// Callback-style fused decoders ("Each" variants). These mirror the Seq2/Seq
// iterator forms but take the yield callback as a plain parameter, which keeps
// the caller's adapter closure and loop state on the stack: escape analysis
// proves a direct callback parameter does not escape, whereas range-over-func
// over a returned closure forces the loop body and its captures to the heap.
// The blob iteration hot paths build NumericDataPoint values inside these
// callbacks, so this is what keeps All() at ~1 allocation per metric.
//
// The Seq2-returning Fused*All forms in fused.go delegate to these so each
// decode loop exists exactly once.

// FusedDeltaGorillaEach decodes delta-of-delta timestamps and Gorilla-compressed
// values in a single fused loop, invoking yield with (index, timestamp, value)
// for each data point. Stops early if yield returns false.
func FusedDeltaGorillaEach(tsData, valData []byte, count int, yield func(int, int64, float64) bool) {
	if count == 0 || len(tsData) == 0 || len(valData) == 0 {
		return
	}

	ds, tsOk := tsdelta.NewDeltaTsState(tsData)
	if !tsOk {
		return
	}

	gc, valOk := gorilla.NewGorillaCursor(valData)
	if !valOk {
		return
	}
	val := gc.First()

	if !yield(0, ds.Ts(), val) {
		return
	}

	for i := 1; i < count; i++ {
		if !ds.NextShort(tsData) && !ds.NextLong(tsData) {
			return
		}

		val, valOk = gc.Next()
		if !valOk {
			return
		}

		if !yield(i, ds.Ts(), val) {
			return
		}
	}
}

// FusedDeltaChimpEach decodes delta-of-delta timestamps and Chimp-compressed
// values in a single fused loop, invoking yield with (index, timestamp, value)
// for each data point. Stops early if yield returns false.
func FusedDeltaChimpEach(tsData, valData []byte, count int, yield func(int, int64, float64) bool) {
	if count == 0 || len(tsData) == 0 || len(valData) == 0 {
		return
	}

	ds, tsOk := tsdelta.NewDeltaTsState(tsData)
	if !tsOk {
		return
	}

	cc, valOk := chimp.NewChimpCursor(valData)
	if !valOk {
		return
	}
	val := cc.First()

	if !yield(0, ds.Ts(), val) {
		return
	}

	for i := 1; i < count; i++ {
		if !ds.NextShort(tsData) && !ds.NextLong(tsData) {
			return
		}

		val, valOk = cc.Next()
		if !valOk {
			return
		}

		if !yield(i, ds.Ts(), val) {
			return
		}
	}
}

// FusedDeltaPackedGorillaEach decodes Group Varint packed delta-of-delta
// timestamps and Gorilla-compressed values in a single fused loop, invoking
// yield with (index, timestamp, value) for each data point. Stops early if
// yield returns false.
func FusedDeltaPackedGorillaEach(tsData, valData []byte, count int, yield func(int, int64, float64) bool) {
	if count == 0 || len(tsData) == 0 || len(valData) == 0 {
		return
	}

	gc, valOk := gorilla.NewGorillaCursor(valData)
	if !valOk {
		return
	}
	val := gc.First()

	dps, tsOk := deltapacked.NewDeltaPackedTsState(tsData)
	if !tsOk {
		return
	}

	if !yield(0, dps.Ts(), val) {
		return
	}

	for i := 1; i < count; i++ {
		if !dps.Next(count - i) {
			return
		}
		val, valOk = gc.Next()
		if !valOk {
			return
		}

		if !yield(i, dps.Ts(), val) {
			return
		}
	}
}

// FusedDeltaPackedChimpEach decodes Group Varint packed delta-of-delta
// timestamps and Chimp-compressed values in a single fused loop, invoking
// yield with (index, timestamp, value) for each data point. Stops early if
// yield returns false.
func FusedDeltaPackedChimpEach(tsData, valData []byte, count int, yield func(int, int64, float64) bool) {
	if count == 0 || len(tsData) == 0 || len(valData) == 0 {
		return
	}

	cc, valOk := chimp.NewChimpCursor(valData)
	if !valOk {
		return
	}
	val := cc.First()

	dps, tsOk := deltapacked.NewDeltaPackedTsState(tsData)
	if !tsOk {
		return
	}

	if !yield(0, dps.Ts(), val) {
		return
	}

	for i := 1; i < count; i++ {
		if !dps.Next(count - i) {
			return
		}
		val, valOk = cc.Next()
		if !valOk {
			return
		}

		if !yield(i, dps.Ts(), val) {
			return
		}
	}
}

// FusedDeltaEach decodes delta-of-delta timestamps, invoking yield with
// (index, timestamp) for each data point, where indexes start at base. Values
// are not decoded here (caller uses At() for raw values).
//
// Returns the index after the last yielded timestamp, or -1 if yield returned
// false. A caller iterating several streams passes the result as the next
// base to keep indexes continuous.
func FusedDeltaEach(tsData []byte, count, base int, yield func(int, int64) bool) int {
	if count == 0 || len(tsData) == 0 {
		return base
	}

	ds, tsOk := tsdelta.NewDeltaTsState(tsData)
	if !tsOk {
		return base
	}

	if !yield(base, ds.Ts()) {
		return -1
	}

	for i := 1; i < count; i++ {
		if !ds.NextShort(tsData) && !ds.NextLong(tsData) {
			return base + i
		}

		if !yield(base+i, ds.Ts()) {
			return -1
		}
	}

	return base + count
}

// FusedGorillaEach decodes Gorilla-compressed values, invoking yield with
// (index, value) for each data point, where indexes start at base. Timestamps
// are not decoded here (caller uses At() for raw timestamps).
//
// Returns the index after the last yielded value, or -1 if yield returned
// false.
func FusedGorillaEach(valData []byte, count, base int, yield func(int, float64) bool) int {
	if count == 0 || len(valData) == 0 {
		return base
	}

	gc, valOk := gorilla.NewGorillaCursor(valData)
	if !valOk {
		return base
	}
	val := gc.First()

	if !yield(base, val) {
		return -1
	}

	for i := 1; i < count; i++ {
		val, valOk = gc.Next()
		if !valOk {
			return base + i
		}

		if !yield(base+i, val) {
			return -1
		}
	}

	return base + count
}

// FusedChimpEach decodes Chimp-compressed values, invoking yield with
// (index, value) for each data point, where indexes start at base. Timestamps
// are not decoded here (caller uses At() for raw timestamps).
//
// Returns the index after the last yielded value, or -1 if yield returned
// false.
func FusedChimpEach(valData []byte, count, base int, yield func(int, float64) bool) int {
	if count == 0 || len(valData) == 0 {
		return base
	}

	cc, valOk := chimp.NewChimpCursor(valData)
	if !valOk {
		return base
	}
	val := cc.First()

	if !yield(base, val) {
		return -1
	}

	for i := 1; i < count; i++ {
		val, valOk = cc.Next()
		if !valOk {
			return base + i
		}

		if !yield(base+i, val) {
			return -1
		}
	}

	return base + count
}

// FusedDeltaPackedEach decodes Group Varint packed delta-of-delta timestamps,
// invoking yield with (index, timestamp) for each data point, where indexes
// start at base. It is the timestamp-only counterpart of
// FusedDeltaPackedGorillaEach (same packed decode state machine, no value
// stream).
//
// Returns the index after the last yielded timestamp, or -1 if yield returned
// false.
//
// Like the other Each variants this must stay a static package-level function:
// running the same loop inside a heap-allocated range-over-func closure body
// keeps the DeltaPacked cursor on the heap and measures slower (see
// docs/perf/iterate_closure_optimization.md).
func FusedDeltaPackedEach(tsData []byte, count, base int, yield func(int, int64) bool) int {
	if count == 0 || len(tsData) == 0 {
		return base
	}

	dps, tsOk := deltapacked.NewDeltaPackedTsState(tsData)
	if !tsOk {
		return base
	}

	if !yield(base, dps.Ts()) {
		return -1
	}

	for i := 1; i < count; i++ {
		if !dps.Next(count - i) {
			return base + i
		}

		if !yield(base+i, dps.Ts()) {
			return -1
		}
	}

	return base + count
}
