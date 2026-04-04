package encoding

import (
	"encoding/binary"
	"iter"
	"math"
)

// deltaState holds mutable state for delta-of-delta timestamp decoding.
type deltaState struct {
	curTS     int64
	delta     int64
	prevDelta int64
	offset    int
	seqCount  int
}

// gorillaState holds mutable state for Gorilla XOR value decoding.
type gorillaState struct {
	br         *bitReader
	prevValue  uint64
	prevFloat  float64
	trailing   int
	blockSize  int
	blockValid bool
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
		if count == 0 || len(tsData) == 0 || len(valData) == 0 {
			return
		}

		// Initialize timestamp delta-of-delta state
		tsFirst, tsOffset, tsOk := decodeVarint64(tsData, 0)
		if !tsOk {
			return
		}

		ds := deltaState{
			curTS:    int64(tsFirst), //nolint:gosec
			offset:   tsOffset,
			seqCount: 1,
		}

		// Initialize Gorilla value state
		br := newBitReader(valData)
		firstBits, valOk := br.readBits(64)
		if !valOk {
			return
		}

		gs := gorillaState{
			br:        br,
			prevValue: firstBits,
			prevFloat: math.Float64frombits(firstBits),
		}

		// Yield first data point
		if !yield(ds.curTS, gs.prevFloat) {
			return
		}

		// Decode remaining data points
		for i := 1; i < count; i++ {
			if !decodeDeltaTimestamp(&ds, tsData) {
				return
			}

			if !decodeGorillaValue(&gs) {
				return
			}

			if !yield(ds.curTS, gs.prevFloat) {
				return
			}
		}
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

	// Initialize timestamp delta-of-delta state
	tsFirst, tsOffset, tsOk := decodeVarint64(tsData, 0)
	if !tsOk {
		return
	}

	ds := deltaState{
		curTS:    int64(tsFirst), //nolint:gosec
		offset:   tsOffset,
		seqCount: 1,
	}

	// Initialize Gorilla value state
	br := newBitReader(valData)
	firstBits, valOk := br.readBits(64)
	if !valOk {
		return
	}

	gs := gorillaState{
		br:        br,
		prevValue: firstBits,
		prevFloat: math.Float64frombits(firstBits),
	}

	// Initialize tag state
	tagOffset := 0
	tag, tagOffset, tagOk := decodeNextTag(tagData, tagOffset)
	if !tagOk {
		return
	}

	// Yield first data point
	if !tagYield(0, ds.curTS, gs.prevFloat, tag) {
		return
	}

	// Decode remaining data points
	for i := 1; i < count; i++ {
		if !decodeDeltaTimestamp(&ds, tsData) {
			return
		}

		if !decodeGorillaValue(&gs) {
			return
		}

		tag, tagOffset, tagOk = decodeNextTag(tagData, tagOffset)
		if !tagOk {
			return
		}

		if !tagYield(i, ds.curTS, gs.prevFloat, tag) {
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

	// Initialize timestamp delta-of-delta state
	tsFirst, tsOffset, tsOk := decodeVarint64(tsData, 0)
	if !tsOk {
		return
	}

	ds := deltaState{
		curTS:    int64(tsFirst), //nolint:gosec
		offset:   tsOffset,
		seqCount: 1,
	}

	// Initialize tag state
	tagOffset := 0
	tag, tagOffset, tagOk := decodeNextTag(tagData, tagOffset)
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

		tag, tagOffset, tagOk = decodeNextTag(tagData, tagOffset)
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

	// Initialize Gorilla value state
	br := newBitReader(valData)
	firstBits, valOk := br.readBits(64)
	if !valOk {
		return
	}

	gs := gorillaState{
		br:        br,
		prevValue: firstBits,
		prevFloat: math.Float64frombits(firstBits),
	}

	// Initialize tag state
	tagOffset := 0
	tag, tagOffset, tagOk := decodeNextTag(tagData, tagOffset)
	if !tagOk {
		return
	}

	// Yield first data point
	if !yield(0, gs.prevFloat, tag) {
		return
	}

	// Decode remaining data points
	for i := 1; i < count; i++ {
		if !decodeGorillaValue(&gs) {
			return
		}

		tag, tagOffset, tagOk = decodeNextTag(tagData, tagOffset)
		if !tagOk {
			return
		}

		if !yield(i, gs.prevFloat, tag) {
			return
		}
	}
}

// decodeDeltaTimestamp decodes a single timestamp from a delta-of-delta stream,
// updating the state in place.
//
// Parameters:
//   - ds: Mutable delta decoder state
//   - data: Encoded timestamp bytes
//
// Returns true if decoding succeeded.
func decodeDeltaTimestamp(ds *deltaState, data []byte) bool {
	zigzag, newOffset, ok := decodeVarint64(data, ds.offset)
	if !ok {
		return false
	}

	decoded := decodeZigZag64(zigzag)

	if ds.seqCount == 1 {
		// Second timestamp: decoded is the delta
		ds.delta = decoded
		ds.curTS += ds.delta
		ds.prevDelta = ds.delta
	} else {
		// Third+ timestamp: decoded is delta-of-delta
		ds.prevDelta += decoded
		ds.curTS += ds.prevDelta
	}

	ds.offset = newOffset
	ds.seqCount++

	return true
}

// decodeGorillaValue decodes a single value from a Gorilla XOR compressed stream,
// updating the state in place.
//
// Parameters:
//   - gs: Mutable Gorilla decoder state
//
// Returns true if decoding succeeded.
func decodeGorillaValue(gs *gorillaState) bool {
	controlBit, ok := gs.br.readBit()
	if !ok {
		return false
	}

	if controlBit == 0 {
		// Value unchanged
		return true
	}

	// Value changed - decode it
	reuseBit, ok := gs.br.readBit()
	if !ok {
		return false
	}

	var trailingBits, blockSizeBits int
	if reuseBit == 0 {
		if !gs.blockValid {
			return false
		}

		trailingBits = gs.trailing
		blockSizeBits = gs.blockSize
	} else {
		leading, ok := gs.br.read5Bits()
		if !ok {
			return false
		}

		sizeBits, ok := gs.br.read6Bits()
		if !ok {
			return false
		}

		blockSizeBits = sizeBits + 1
		if blockSizeBits < 1 || blockSizeBits > 64 {
			return false
		}

		trailingBits = 64 - leading - blockSizeBits
		if trailingBits < 0 || trailingBits > 64 {
			return false
		}

		gs.trailing = trailingBits
		gs.blockSize = blockSizeBits
		gs.blockValid = true
	}

	meaningful, ok := gs.br.readBits(blockSizeBits)
	if !ok {
		return false
	}

	shift := uint64(trailingBits) // #nosec G115 -- trailingBits constrained to [0,64]
	gs.prevValue ^= meaningful << shift
	gs.prevFloat = math.Float64frombits(gs.prevValue)

	return true
}

// chimpState holds mutable state for Chimp XOR value decoding.
type chimpState struct {
	br            bitReader
	prevValue     uint64
	prevFloat     float64
	storedLeading int
}

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
		if count == 0 || len(tsData) == 0 || len(valData) == 0 {
			return
		}

		// Initialize timestamp delta-of-delta state
		tsFirst, tsOffset, tsOk := decodeVarint64(tsData, 0)
		if !tsOk {
			return
		}

		ds := deltaState{
			curTS:    int64(tsFirst), //nolint:gosec
			offset:   tsOffset,
			seqCount: 1,
		}

		// Initialize Chimp value state
		var br bitReader
		br.data = valData
		firstBits, valOk := br.readBits(64)
		if !valOk {
			return
		}

		cs := chimpState{
			br:            br,
			prevValue:     firstBits,
			prevFloat:     math.Float64frombits(firstBits),
			storedLeading: 65,
		}

		// Yield first data point
		if !yield(ds.curTS, cs.prevFloat) {
			return
		}

		// Decode remaining data points
		for i := 1; i < count; i++ {
			if !decodeDeltaTimestamp(&ds, tsData) {
				return
			}

			if !decodeChimpValue(&cs) {
				return
			}

			if !yield(ds.curTS, cs.prevFloat) {
				return
			}
		}
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

	// Initialize timestamp delta-of-delta state
	tsFirst, tsOffset, tsOk := decodeVarint64(tsData, 0)
	if !tsOk {
		return
	}

	ds := deltaState{
		curTS:    int64(tsFirst), //nolint:gosec
		offset:   tsOffset,
		seqCount: 1,
	}

	// Initialize Chimp value state
	var br bitReader
	br.data = valData
	firstBits, valOk := br.readBits(64)
	if !valOk {
		return
	}

	cs := chimpState{
		br:            br,
		prevValue:     firstBits,
		prevFloat:     math.Float64frombits(firstBits),
		storedLeading: 65,
	}

	// Initialize tag state
	tagOffset := 0
	tag, tagOffset, tagOk := decodeNextTag(tagData, tagOffset)
	if !tagOk {
		return
	}

	// Yield first data point
	if !tagYield(0, ds.curTS, cs.prevFloat, tag) {
		return
	}

	// Decode remaining data points
	for i := 1; i < count; i++ {
		if !decodeDeltaTimestamp(&ds, tsData) {
			return
		}

		if !decodeChimpValue(&cs) {
			return
		}

		tag, tagOffset, tagOk = decodeNextTag(tagData, tagOffset)
		if !tagOk {
			return
		}

		if !tagYield(i, ds.curTS, cs.prevFloat, tag) {
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

	// Initialize Chimp value state
	var br bitReader
	br.data = valData
	firstBits, valOk := br.readBits(64)
	if !valOk {
		return
	}

	cs := chimpState{
		br:            br,
		prevValue:     firstBits,
		prevFloat:     math.Float64frombits(firstBits),
		storedLeading: 65,
	}

	// Initialize tag state
	tagOffset := 0
	tag, tagOffset, tagOk := decodeNextTag(tagData, tagOffset)
	if !tagOk {
		return
	}

	// Yield first data point
	if !yield(0, cs.prevFloat, tag) {
		return
	}

	// Decode remaining data points
	for i := 1; i < count; i++ {
		if !decodeChimpValue(&cs) {
			return
		}

		tag, tagOffset, tagOk = decodeNextTag(tagData, tagOffset)
		if !tagOk {
			return
		}

		if !yield(i, cs.prevFloat, tag) {
			return
		}
	}
}

// decodeChimpValue decodes a single value from a Chimp XOR compressed stream,
// updating the state in place.
//
// Parameters:
//   - cs: Mutable Chimp decoder state
//
// Returns true if decoding succeeded.
func decodeChimpValue(cs *chimpState) bool {
	flag, ok := cs.br.read2Bits()
	if !ok {
		return false
	}

	switch flag {
	case 0: // Value unchanged
		cs.storedLeading = 65

	case 1: // Trailing-zero optimized
		leadingBucket, ok := cs.br.read3Bits()
		if !ok {
			return false
		}

		leading := chimpLeadingDecode[leadingBucket]

		sigBitsCount, ok := cs.br.read6Bits()
		if !ok {
			return false
		}

		sigBits := sigBitsCount
		if sigBits == 0 {
			sigBits = 64
		}

		meaningful, ok := cs.br.readBits(sigBits)
		if !ok {
			return false
		}

		trailingZeros := 64 - leading - sigBits
		if trailingZeros < 0 {
			return false
		}

		cs.prevValue ^= meaningful << trailingZeros
		cs.prevFloat = math.Float64frombits(cs.prevValue)
		cs.storedLeading = 65

	case 2: // Reuse previous leading
		if cs.storedLeading > 64 {
			return false
		}

		sigBits := 64 - cs.storedLeading

		meaningful, ok := cs.br.readBits(sigBits)
		if !ok {
			return false
		}

		cs.prevValue ^= meaningful
		cs.prevFloat = math.Float64frombits(cs.prevValue)

	case 3: // New leading
		leadingBucket, ok := cs.br.read3Bits()
		if !ok {
			return false
		}

		leading := chimpLeadingDecode[leadingBucket]
		cs.storedLeading = leading
		sigBits := 64 - leading

		meaningful, ok := cs.br.readBits(sigBits)
		if !ok {
			return false
		}

		cs.prevValue ^= meaningful
		cs.prevFloat = math.Float64frombits(cs.prevValue)

	default:
		return false
	}

	return true
}

// decodeNextTag decodes the next varint-prefixed string tag from the data.
//
// Parameters:
//   - data: Tag payload bytes
//   - offset: Current byte offset
//
// Returns (tag string, new offset, ok).
func decodeNextTag(data []byte, offset int) (string, int, bool) {
	if offset >= len(data) {
		return "", offset, false
	}

	tagLenU64, n := binary.Uvarint(data[offset:])
	if n <= 0 {
		return "", offset, false
	}

	if tagLenU64 > uint64(len(data)) {
		return "", offset, false
	}

	tagLen := int(tagLenU64) //nolint:gosec // overflow checked above
	start := offset + n
	end := start + tagLen

	if end > len(data) {
		return "", offset, false
	}

	if tagLen == 0 {
		return "", end, true
	}

	return string(data[start:end]), end, true
}
