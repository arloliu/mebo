package encoding

import "math"

// Exported incremental decode states for the fused hot loops.
//
// These let the blob package run the per-point decode loop in a static
// function and construct its public data-point struct inline, so the
// consumer's yield is the only indirect call per element. The methods are
// thin wrappers around the unexported state machines in fused.go; they inline
// to direct calls of the underlying decode functions.
//
// Use these only from static functions: running the same loop inside a
// heap-allocated closure body measured ~20% slower than the Fused*Each
// callback forms (see docs/perf/iterate_closure_optimization.md).
//
// The optional count cap (SetCount) lives here in the Next() wrappers rather
// than in the unexported decode primitives, so the bulk fused loops that call
// those primitives directly (FusedDelta*Each, Fused*TagAll) stay free of any
// per-value bookkeeping. Those loops are already bounded by their own
// `for i < count` range, so only the Next()-drained wrappers need the cap.

// DeltaTsState incrementally decodes a delta-of-delta timestamp stream.
// The zero value is not usable; construct with NewDeltaTsState.
type DeltaTsState struct {
	ds deltaState
}

// NewDeltaTsState initializes the state from the payload, consuming the first
// timestamp (available via Ts immediately). Returns false if the payload does
// not contain a valid first varint.
func NewDeltaTsState(data []byte) (DeltaTsState, bool) {
	first, offset, ok := decodeVarint64(data, 0)
	if !ok {
		return DeltaTsState{}, false
	}

	return DeltaTsState{ds: deltaState{
		curTS:    int64(first), //nolint:gosec
		offset:   offset,
		seqCount: 1,
	}}, true
}

// Next decodes the next timestamp from data. Returns false when the stream is
// exhausted or corrupted.
func (s *DeltaTsState) Next(data []byte) bool {
	return decodeDeltaTimestamp(&s.ds, data)
}

// Ts returns the most recently decoded timestamp.
func (s *DeltaTsState) Ts() int64 {
	return s.ds.curTS
}

// GorillaValState incrementally decodes a Gorilla XOR compressed value stream.
// The zero value is not usable; construct with NewGorillaValState.
type GorillaValState struct {
	gs        gorillaState
	remaining int // values still decodable via Next(); math.MaxInt when unconstrained
}

// NewGorillaValState initializes the state from the value payload, consuming
// the uncompressed first value (available via Val immediately). Returns false
// if the payload is too short.
func NewGorillaValState(valData []byte) (GorillaValState, bool) {
	gs, ok := newGorillaState(valData)

	return GorillaValState{gs: gs, remaining: math.MaxInt}, ok
}

// SetCount constrains the state to exactly count values total (including the
// first value already consumed by NewGorillaValState). Call this when iterating
// via Next() without an outer count-bounded loop; without it, trailing padding
// zeros in the final byte of a Gorilla stream can be misread as extra unchanged
// values.
func (s *GorillaValState) SetCount(count int) {
	if count > 1 {
		s.remaining = count - 1
	} else {
		s.remaining = 0
	}
}

// Next decodes the next value. Returns false when the stream is exhausted,
// corrupted, or the SetCount limit has been reached.
func (s *GorillaValState) Next() bool {
	if s.remaining <= 0 {
		return false
	}
	if !decodeGorillaValue(&s.gs) {
		return false
	}
	s.remaining--

	return true
}

// Val returns the most recently decoded value.
func (s *GorillaValState) Val() float64 {
	return s.gs.prevFloat
}

// ChimpValState incrementally decodes a Chimp compressed value stream.
// The zero value is not usable; construct with NewChimpValState.
type ChimpValState struct {
	cs        chimpState
	remaining int // values still decodable via Next(); math.MaxInt when unconstrained
}

// NewChimpValState initializes the state from the value payload, consuming
// the uncompressed first value (available via Val immediately). Returns false
// if the payload is too short.
func NewChimpValState(valData []byte) (ChimpValState, bool) {
	cs, ok := newChimpState(valData)

	return ChimpValState{cs: cs, remaining: math.MaxInt}, ok
}

// SetCount constrains the state to exactly count values total (including the
// first value already consumed by NewChimpValState). Call this when iterating
// via Next() without an outer count-bounded loop; without it, trailing padding
// zeros in the final byte of a Chimp stream can be misread as extra unchanged
// values.
func (s *ChimpValState) SetCount(count int) {
	if count > 1 {
		s.remaining = count - 1
	} else {
		s.remaining = 0
	}
}

// Next decodes the next value. Returns false when the stream is exhausted,
// corrupted, or the SetCount limit has been reached.
func (s *ChimpValState) Next() bool {
	if s.remaining <= 0 {
		return false
	}
	if !decodeChimpValue(&s.cs) {
		return false
	}
	s.remaining--

	return true
}

// Val returns the most recently decoded value.
func (s *ChimpValState) Val() float64 {
	return s.cs.prevFloat
}
