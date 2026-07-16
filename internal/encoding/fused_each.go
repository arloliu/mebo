package encoding

import (
	"math"

	"github.com/arloliu/mebo/endian"
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

	tsFirst, tsOffset, tsOk := decodeVarint64(tsData, 0)
	if !tsOk {
		return
	}

	ds := deltaState{
		curTS:    int64(tsFirst), //nolint:gosec
		offset:   tsOffset,
		seqCount: 1,
	}

	gs, valOk := newGorillaState(valData)
	if !valOk {
		return
	}

	if !yield(0, ds.curTS, gs.prevFloat) {
		return
	}

	for i := 1; i < count; i++ {
		if !decodeDeltaTimestamp(&ds, tsData) {
			return
		}

		if !decodeGorillaValue(&gs) {
			return
		}

		if !yield(i, ds.curTS, gs.prevFloat) {
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

	tsFirst, tsOffset, tsOk := decodeVarint64(tsData, 0)
	if !tsOk {
		return
	}

	ds := deltaState{
		curTS:    int64(tsFirst), //nolint:gosec
		offset:   tsOffset,
		seqCount: 1,
	}

	cs, valOk := newChimpState(valData)
	if !valOk {
		return
	}

	if !yield(0, ds.curTS, cs.prevFloat) {
		return
	}

	for i := 1; i < count; i++ {
		if !decodeDeltaTimestamp(&ds, tsData) {
			return
		}

		if !decodeChimpValue(&cs) {
			return
		}

		if !yield(i, ds.curTS, cs.prevFloat) {
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

	gs, valOk := newGorillaState(valData)
	if !valOk {
		return
	}

	// First timestamp (full varint)
	first, offset, tsOk := decodeVarint64(tsData, 0)
	if !tsOk {
		return
	}

	var dps deltaPackedState
	dps.curTS = int64(first) //nolint:gosec
	dps.offset = offset

	if !yield(0, dps.curTS, gs.prevFloat) {
		return
	}

	if count == 1 {
		return
	}

	// Second timestamp
	zigzag, offset, tsOk := decodeVarint64(tsData, dps.offset)
	if !tsOk {
		return
	}

	delta := decodeZigZag64(zigzag)
	dps.curTS += delta
	dps.prevDelta = delta
	dps.offset = offset

	if !decodeGorillaValue(&gs) {
		return
	}

	if !yield(1, dps.curTS, gs.prevFloat) {
		return
	}

	// Remaining: Group Varint packed delta-of-deltas
	idx := 2
	remaining := count - 2
	dps.groupLen = groupSize

	for remaining > 0 {
		if remaining < groupSize {
			dps.groupLen = remaining
		}

		for range dps.groupLen {
			if !decodeDeltaPackedTimestamp(&dps, tsData) {
				return
			}

			if !decodeGorillaValue(&gs) {
				return
			}

			if !yield(idx, dps.curTS, gs.prevFloat) {
				return
			}
			idx++
		}

		remaining -= dps.groupLen
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

	cs, valOk := newChimpState(valData)
	if !valOk {
		return
	}

	// First timestamp (full varint)
	first, offset, tsOk := decodeVarint64(tsData, 0)
	if !tsOk {
		return
	}

	var dps deltaPackedState
	dps.curTS = int64(first) //nolint:gosec
	dps.offset = offset

	if !yield(0, dps.curTS, cs.prevFloat) {
		return
	}

	if count == 1 {
		return
	}

	// Second timestamp
	zigzag, offset, tsOk := decodeVarint64(tsData, dps.offset)
	if !tsOk {
		return
	}

	delta := decodeZigZag64(zigzag)
	dps.curTS += delta
	dps.prevDelta = delta
	dps.offset = offset

	if !decodeChimpValue(&cs) {
		return
	}

	if !yield(1, dps.curTS, cs.prevFloat) {
		return
	}

	// Remaining: Group Varint packed delta-of-deltas
	idx := 2
	remaining := count - 2
	dps.groupLen = groupSize

	for remaining > 0 {
		if remaining < groupSize {
			dps.groupLen = remaining
		}

		for range dps.groupLen {
			if !decodeDeltaPackedTimestamp(&dps, tsData) {
				return
			}

			if !decodeChimpValue(&cs) {
				return
			}

			if !yield(idx, dps.curTS, cs.prevFloat) {
				return
			}
			idx++
		}

		remaining -= dps.groupLen
	}
}

// FusedDeltaEach decodes delta-of-delta timestamps, invoking yield with
// (index, timestamp) for each data point. Values are not decoded here (caller
// uses At() for raw values). Stops early if yield returns false.
func FusedDeltaEach(tsData []byte, count int, yield func(int, int64) bool) {
	if count == 0 || len(tsData) == 0 {
		return
	}

	tsFirst, tsOffset, tsOk := decodeVarint64(tsData, 0)
	if !tsOk {
		return
	}

	ds := deltaState{
		curTS:    int64(tsFirst), //nolint:gosec
		offset:   tsOffset,
		seqCount: 1,
	}

	if !yield(0, ds.curTS) {
		return
	}

	for i := 1; i < count; i++ {
		if !decodeDeltaTimestamp(&ds, tsData) {
			return
		}

		if !yield(i, ds.curTS) {
			return
		}
	}
}

// FusedGorillaEach decodes Gorilla-compressed values, invoking yield with
// (index, value) for each data point. Timestamps are not decoded here (caller
// uses At() for raw timestamps). Stops early if yield returns false.
func FusedGorillaEach(valData []byte, count int, yield func(int, float64) bool) {
	if count == 0 || len(valData) == 0 {
		return
	}

	gs, valOk := newGorillaState(valData)
	if !valOk {
		return
	}

	if !yield(0, gs.prevFloat) {
		return
	}

	for i := 1; i < count; i++ {
		if !decodeGorillaValue(&gs) {
			return
		}

		if !yield(i, gs.prevFloat) {
			return
		}
	}
}

// FusedChimpEach decodes Chimp-compressed values, invoking yield with
// (index, value) for each data point. Timestamps are not decoded here (caller
// uses At() for raw timestamps). Stops early if yield returns false.
func FusedChimpEach(valData []byte, count int, yield func(int, float64) bool) {
	if count == 0 || len(valData) == 0 {
		return
	}

	cs, valOk := newChimpState(valData)
	if !valOk {
		return
	}

	if !yield(0, cs.prevFloat) {
		return
	}

	for i := 1; i < count; i++ {
		if !decodeChimpValue(&cs) {
			return
		}

		if !yield(i, cs.prevFloat) {
			return
		}
	}
}

// FusedDeltaPackedEach decodes Group Varint packed delta-of-delta timestamps,
// invoking yield with (index, timestamp) for each data point. It is the
// timestamp-only counterpart of FusedDeltaPackedGorillaEach (same packed decode
// state machine, no value stream). Stops early if yield returns false.
//
// Like the other Each variants this must stay a static package-level function:
// running the same loop inside a heap-allocated range-over-func closure body
// keeps the deltaPackedState cursor on the heap and measures slower (see
// docs/perf/iterate_closure_optimization.md).
func FusedDeltaPackedEach(tsData []byte, count int, yield func(int, int64) bool) {
	if count == 0 || len(tsData) == 0 {
		return
	}

	// First timestamp (full varint).
	first, offset, tsOk := decodeVarint64(tsData, 0)
	if !tsOk {
		return
	}

	var dps deltaPackedState
	dps.curTS = int64(first) //nolint:gosec
	dps.offset = offset

	if !yield(0, dps.curTS) {
		return
	}

	if count == 1 {
		return
	}

	// Second timestamp (zigzag delta).
	zigzag, offset, tsOk := decodeVarint64(tsData, dps.offset)
	if !tsOk {
		return
	}

	delta := decodeZigZag64(zigzag)
	dps.curTS += delta
	dps.prevDelta = delta
	dps.offset = offset

	if !yield(1, dps.curTS) {
		return
	}

	// Remaining: Group Varint packed delta-of-deltas.
	idx := 2
	remaining := count - 2
	dps.groupLen = groupSize

	for remaining > 0 {
		if remaining < groupSize {
			dps.groupLen = remaining
		}

		for range dps.groupLen {
			if !decodeDeltaPackedTimestamp(&dps, tsData) {
				return
			}

			if !yield(idx, dps.curTS) {
				return
			}
			idx++
		}

		remaining -= dps.groupLen
	}
}

// RawValuesEach decodes raw (uncompressed) float64 values, invoking yield with
// (index, value) for each data point. When nativeByteOrder is true it reuses
// the zero-copy unsafe reinterpret of the payload; otherwise it reads each value
// through the endian engine. Stops early if yield returns false.
//
// Raw decode has no stateful bit cursor, so unlike the XOR/delta variants the
// stack-vs-heap distinction does not change throughput here — the value of the
// static form is purely that it allocates nothing per call (no returned
// iterator closure, no escaping range-over-func body).
func RawValuesEach(data []byte, count int, engine endian.EndianEngine, nativeByteOrder bool, yield func(int, float64) bool) {
	if count == 0 || len(data) < count*8 {
		return
	}

	if nativeByteOrder {
		floats, err := unsafeDecodeFloat64Slice(data[:count*8])
		if err == nil && floats != nil {
			for i, v := range floats {
				if !yield(i, v) {
					return
				}
			}

			return
		}
		// Fall through to the safe path if the unsafe reinterpret failed.
	}

	for i := range count {
		start := i * 8
		bits := engine.Uint64(data[start : start+8])
		if !yield(i, math.Float64frombits(bits)) {
			return
		}
	}
}

// RawTimestampsEach decodes raw (uncompressed) int64 timestamps, invoking yield
// with (index, timestamp) for each data point. It mirrors RawValuesEach for the
// timestamp column. Stops early if yield returns false.
func RawTimestampsEach(data []byte, count int, engine endian.EndianEngine, nativeByteOrder bool, yield func(int, int64) bool) {
	if count == 0 || len(data) < count*8 {
		return
	}

	// Match TimestampRawDecoder.All, which rejects non-8-aligned payloads
	// outright (the raw value decoder does not, so this guard is timestamp-only).
	// Keeps ForEachTimestamps byte-identical to AllTimestamps on malformed entries.
	if len(data)%8 != 0 {
		return
	}

	if nativeByteOrder {
		ts, err := unsafeDecodeInt64Slice(data[:count*8])
		if err == nil && ts != nil {
			for i, v := range ts {
				if !yield(i, v) {
					return
				}
			}

			return
		}
		// Fall through to the safe path if the unsafe reinterpret failed.
	}

	for i := range count {
		start := i * 8
		ts := int64(engine.Uint64(data[start : start+8])) //nolint:gosec
		if !yield(i, ts) {
			return
		}
	}
}
