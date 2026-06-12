package encoding

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
