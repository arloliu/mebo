package fused

import (
	"iter"

	"github.com/arloliu/mebo/internal/encoding/metadata"
	tsdelta "github.com/arloliu/mebo/internal/encoding/timestamp/delta"
	"github.com/arloliu/mebo/internal/encoding/timestamp/deltapacked"
	"github.com/arloliu/mebo/internal/encoding/value/chimp"
	"github.com/arloliu/mebo/internal/encoding/value/gorilla"
)

// deltaState keeps the root fused loops compatible with the codec-owned
// incremental Delta decoder.
type deltaState struct {
	state tsdelta.DeltaTsState
	curTS int64
}

func newDeltaState(data []byte) (deltaState, bool) {
	state, ok := tsdelta.NewDeltaTsState(data)
	if !ok {
		return deltaState{}, false
	}

	return deltaState{state: state, curTS: state.Ts()}, true
}

// FusedDeltaGorillaAll returns an iterator that decodes delta-of-delta timestamps and
// Gorilla-compressed values in a single fused loop, avoiding iter.Pull overhead.
//
// This is the fused equivalent of synchronizing TimestampDeltaDecoder.All() and
// NumericGorillaDecoder.All() via iter.Pull, but with all state inlined into
// a single loop iteration. Eliminates coroutine creation and context-switch overhead.
//
// Parameters:
//   - tsData: Delta-of-delta encoded timestamp bytes
//   - valData: Gorilla XOR compressed value bytes
//   - count: Number of data points to decode
//
// Returns:
//   - iter.Seq2[int64, float64]: Iterator yielding (timestamp, value) pairs
func FusedDeltaGorillaAll(tsData, valData []byte, count int) iter.Seq2[int64, float64] {
	return func(yield func(int64, float64) bool) {
		FusedDeltaGorillaEach(tsData, valData, count, func(_ int, ts int64, val float64) bool {
			return yield(ts, val)
		})
	}
}

// FusedDeltaGorillaTagAll returns an iterator that decodes delta-of-delta timestamps,
// Gorilla-compressed values, and varint-prefixed tags in a single fused loop.
//
// Parameters:
//   - tsData: Delta-of-delta encoded timestamp bytes
//   - valData: Gorilla XOR compressed value bytes
//   - tagData: Varint length-prefixed tag bytes
//   - count: Number of data points to decode
//
// Returns:
//   - iter.Seq2[int64, float64]: first return is timestamp, second is value
//
// The tag is provided via a callback to avoid allocating a 3-tuple struct per iteration.
// Use FusedDeltaGorillaTagAllWith for the full (ts, val, tag) iteration.
func FusedDeltaGorillaTagAll(tsData, valData, tagData []byte, count int, tagYield func(int, int64, float64, string) bool) {
	if count == 0 || len(tsData) == 0 || len(valData) == 0 {
		return
	}

	// Initialize timestamp delta-of-delta state.
	ds, tsOk := newDeltaState(tsData)
	if !tsOk {
		return
	}

	gc, valOk := gorilla.NewGorillaCursor(valData)
	if !valOk {
		return
	}
	val := gc.First()

	// Initialize tag state
	tagCursor := metadata.NewTagCursor(tagData)
	tag, tagOk := tagCursor.Next()
	if !tagOk {
		return
	}

	// Yield first data point
	if !tagYield(0, ds.curTS, val, tag) {
		return
	}

	// Decode remaining data points
	for i := 1; i < count; i++ {
		if !decodeDeltaTimestamp(&ds, tsData) {
			return
		}

		val, valOk = gc.Next()
		if !valOk {
			return
		}

		tag, tagOk = tagCursor.Next()
		if !tagOk {
			return
		}

		if !tagYield(i, ds.curTS, val, tag) {
			return
		}
	}
}

// FusedDeltaRawTagAll decodes delta-of-delta timestamps with tag iteration in a single
// fused loop, returning index for raw value At() lookup. Eliminates iter.Pull for the
// delta+raw+tag path.
//
// Parameters:
//   - tsData: Delta-of-delta encoded timestamp bytes
//   - tagData: Varint length-prefixed tag bytes
//   - count: Number of data points to decode
//   - yield: Callback receiving (index, timestamp, tag)
func FusedDeltaTagAll(tsData, tagData []byte, count int, yield func(int, int64, string) bool) {
	if count == 0 || len(tsData) == 0 {
		return
	}

	// Initialize timestamp delta-of-delta state.
	ds, tsOk := newDeltaState(tsData)
	if !tsOk {
		return
	}

	// Initialize tag state
	tagCursor := metadata.NewTagCursor(tagData)
	tag, tagOk := tagCursor.Next()
	if !tagOk {
		return
	}

	// Yield first data point
	if !yield(0, ds.curTS, tag) {
		return
	}

	// Decode remaining data points
	for i := 1; i < count; i++ {
		if !decodeDeltaTimestamp(&ds, tsData) {
			return
		}

		tag, tagOk = tagCursor.Next()
		if !tagOk {
			return
		}

		if !yield(i, ds.curTS, tag) {
			return
		}
	}
}

// FusedGorillaTagAll decodes Gorilla-compressed values and tags in a single fused loop.
// Timestamps are not decoded here (caller uses At() for raw timestamps).
//
// Parameters:
//   - valData: Gorilla XOR compressed value bytes
//   - tagData: Varint length-prefixed tag bytes
//   - count: Number of data points to decode
//   - yield: Callback receiving (index, value, tag)
func FusedGorillaTagAll(valData, tagData []byte, count int, yield func(int, float64, string) bool) {
	if count == 0 || len(valData) == 0 {
		return
	}

	gc, valOk := gorilla.NewGorillaCursor(valData)
	if !valOk {
		return
	}
	val := gc.First()

	// Initialize tag state
	tagCursor := metadata.NewTagCursor(tagData)
	tag, tagOk := tagCursor.Next()
	if !tagOk {
		return
	}

	// Yield first data point
	if !yield(0, val, tag) {
		return
	}

	// Decode remaining data points
	for i := 1; i < count; i++ {
		val, valOk = gc.Next()
		if !valOk {
			return
		}

		tag, tagOk = tagCursor.Next()
		if !tagOk {
			return
		}

		if !yield(i, val, tag) {
			return
		}
	}
}

func decodeDeltaTimestamp(ds *deltaState, data []byte) bool {
	if !ds.state.Next(data) {
		return false
	}
	ds.curTS = ds.state.Ts()

	return true
}

// Chimp and Gorilla cursors return decoded values directly so fused callbacks
// do not carry wrapper state or count bookkeeping in their hot loops.

// FusedDeltaChimpAll returns an iterator that decodes delta-of-delta timestamps and
// Chimp-compressed values in a single fused loop, avoiding iter.Pull overhead.
//
// This is the fused equivalent of synchronizing TimestampDeltaDecoder.All() and
// NumericChimpDecoder.All() via iter.Pull, but with all state inlined into
// a single loop iteration.
//
// Parameters:
//   - tsData: Delta-of-delta encoded timestamp bytes
//   - valData: Chimp XOR compressed value bytes
//   - count: Number of data points to decode
//
// Returns:
//   - iter.Seq2[int64, float64]: Iterator yielding (timestamp, value) pairs
func FusedDeltaChimpAll(tsData, valData []byte, count int) iter.Seq2[int64, float64] {
	return func(yield func(int64, float64) bool) {
		FusedDeltaChimpEach(tsData, valData, count, func(_ int, ts int64, val float64) bool {
			return yield(ts, val)
		})
	}
}

// FusedDeltaChimpTagAll decodes delta-of-delta timestamps, Chimp-compressed values,
// and varint-prefixed tags in a single fused loop.
//
// Parameters:
//   - tsData: Delta-of-delta encoded timestamp bytes
//   - valData: Chimp XOR compressed value bytes
//   - tagData: Varint length-prefixed tag bytes
//   - count: Number of data points to decode
//   - tagYield: Callback receiving (index, timestamp, value, tag)
func FusedDeltaChimpTagAll(tsData, valData, tagData []byte, count int, tagYield func(int, int64, float64, string) bool) {
	if count == 0 || len(tsData) == 0 || len(valData) == 0 {
		return
	}

	// Initialize timestamp delta-of-delta state.
	ds, tsOk := newDeltaState(tsData)
	if !tsOk {
		return
	}

	cc, valOk := chimp.NewChimpCursor(valData)
	if !valOk {
		return
	}
	val := cc.First()

	// Initialize tag state
	tagCursor := metadata.NewTagCursor(tagData)
	tag, tagOk := tagCursor.Next()
	if !tagOk {
		return
	}

	// Yield first data point
	if !tagYield(0, ds.curTS, val, tag) {
		return
	}

	// Decode remaining data points
	for i := 1; i < count; i++ {
		if !decodeDeltaTimestamp(&ds, tsData) {
			return
		}

		val, valOk = cc.Next()
		if !valOk {
			return
		}

		tag, tagOk = tagCursor.Next()
		if !tagOk {
			return
		}

		if !tagYield(i, ds.curTS, val, tag) {
			return
		}
	}
}

// FusedChimpTagAll decodes Chimp-compressed values and tags in a single fused loop.
// Timestamps are not decoded here (caller uses At() for raw timestamps).
//
// Parameters:
//   - valData: Chimp XOR compressed value bytes
//   - tagData: Varint length-prefixed tag bytes
//   - count: Number of data points to decode
//   - yield: Callback receiving (index, value, tag)
func FusedChimpTagAll(valData, tagData []byte, count int, yield func(int, float64, string) bool) {
	if count == 0 || len(valData) == 0 {
		return
	}

	cc, valOk := chimp.NewChimpCursor(valData)
	if !valOk {
		return
	}
	val := cc.First()

	// Initialize tag state
	tagCursor := metadata.NewTagCursor(tagData)
	tag, tagOk := tagCursor.Next()
	if !tagOk {
		return
	}

	// Yield first data point
	if !yield(0, val, tag) {
		return
	}

	// Decode remaining data points
	for i := 1; i < count; i++ {
		val, valOk = cc.Next()
		if !valOk {
			return
		}

		tag, tagOk = tagCursor.Next()
		if !tagOk {
			return
		}

		if !yield(i, val, tag) {
			return
		}
	}
}

// FusedDeltaPackedGorillaAll returns an iterator that decodes Group Varint packed
// delta-of-delta timestamps and Gorilla-compressed values in a single fused loop,
// avoiding iter.Pull overhead.
//
// Parameters:
//   - tsData: Group Varint packed delta-of-delta encoded timestamp bytes
//   - valData: Gorilla XOR compressed value bytes
//   - count: Number of data points to decode
//
// Returns:
//   - iter.Seq2[int64, float64]: Iterator yielding (timestamp, value) pairs
func FusedDeltaPackedGorillaAll(tsData, valData []byte, count int) iter.Seq2[int64, float64] {
	return func(yield func(int64, float64) bool) {
		FusedDeltaPackedGorillaEach(tsData, valData, count, func(_ int, ts int64, val float64) bool {
			return yield(ts, val)
		})
	}
}

// FusedDeltaPackedGorillaTagAll decodes Group Varint packed delta-of-delta timestamps,
// Gorilla-compressed values, and varint-prefixed tags in a single fused loop.
//
// Parameters:
//   - tsData: Group Varint packed delta-of-delta encoded timestamp bytes
//   - valData: Gorilla XOR compressed value bytes
//   - tagData: Varint length-prefixed tag bytes
//   - count: Number of data points to decode
//   - tagYield: Callback receiving (index, timestamp, value, tag); return false to stop
func FusedDeltaPackedGorillaTagAll(tsData, valData, tagData []byte, count int, tagYield func(int, int64, float64, string) bool) {
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

	tagCursor := metadata.NewTagCursor(tagData)
	tag, tagOk := tagCursor.Next()
	if !tagOk {
		return
	}

	if !tagYield(0, dps.Ts(), val, tag) {
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

		tag, tagOk = tagCursor.Next()
		if !tagOk || !tagYield(i, dps.Ts(), val, tag) {
			return
		}
	}
}

// FusedDeltaPackedChimpAll returns an iterator that decodes Group Varint packed
// delta-of-delta timestamps and Chimp-compressed values in a single fused loop.
//
// Parameters:
//   - tsData: Group Varint packed delta-of-delta encoded timestamp bytes
//   - valData: Chimp XOR compressed value bytes
//   - count: Number of data points to decode
//
// Returns:
//   - iter.Seq2[int64, float64]: Iterator yielding (timestamp, value) pairs
func FusedDeltaPackedChimpAll(tsData, valData []byte, count int) iter.Seq2[int64, float64] {
	return func(yield func(int64, float64) bool) {
		FusedDeltaPackedChimpEach(tsData, valData, count, func(_ int, ts int64, val float64) bool {
			return yield(ts, val)
		})
	}
}

// FusedDeltaPackedChimpTagAll decodes Group Varint packed delta-of-delta timestamps,
// Chimp-compressed values, and varint-prefixed tags in a single fused loop.
//
// Parameters:
//   - tsData: Group Varint packed delta-of-delta encoded timestamp bytes
//   - valData: Chimp XOR compressed value bytes
//   - tagData: Varint length-prefixed tag bytes
//   - count: Number of data points to decode
//   - tagYield: Callback receiving (index, timestamp, value, tag); return false to stop
func FusedDeltaPackedChimpTagAll(tsData, valData, tagData []byte, count int, tagYield func(int, int64, float64, string) bool) {
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

	tagCursor := metadata.NewTagCursor(tagData)
	tag, tagOk := tagCursor.Next()
	if !tagOk {
		return
	}

	if !tagYield(0, dps.Ts(), val, tag) {
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

		tag, tagOk = tagCursor.Next()
		if !tagOk || !tagYield(i, dps.Ts(), val, tag) {
			return
		}
	}
}

// FusedDeltaPackedTagAll decodes Group Varint packed delta-of-delta timestamps
// and varint-prefixed tags in a single fused loop. Values are not decoded here;
// the caller uses At() for raw values.
//
// Parameters:
//   - tsData: Group Varint packed delta-of-delta encoded timestamp bytes
//   - tagData: Varint length-prefixed tag bytes
//   - count: Number of data points to decode
//   - yield: Callback receiving (index, timestamp, tag); return false to stop
func FusedDeltaPackedTagAll(tsData, tagData []byte, count int, yield func(int, int64, string) bool) {
	if count == 0 || len(tsData) == 0 {
		return
	}

	dps, tsOk := deltapacked.NewDeltaPackedTsState(tsData)
	if !tsOk {
		return
	}

	tagCursor := metadata.NewTagCursor(tagData)
	tag, tagOk := tagCursor.Next()
	if !tagOk {
		return
	}

	if !yield(0, dps.Ts(), tag) {
		return
	}

	for i := 1; i < count; i++ {
		if !dps.Next(count - i) {
			return
		}

		tag, tagOk = tagCursor.Next()
		if !tagOk || !yield(i, dps.Ts(), tag) {
			return
		}
	}
}
