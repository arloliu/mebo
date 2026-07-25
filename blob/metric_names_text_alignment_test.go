package blob

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ==============================================================================
// TextEncoder must source its metric-names payload from an entry-parallel
// e.metricNames slice (mirroring NumericEncoder's equivalent invariant), not
// from the collision tracker's StartMetricName-insertion-order list. This is
// a pure refactor of *where* the names come from — the emitted bytes must
// not change.
//
// Text never reorders entries today, so the two lists always hold identical
// content in identical order — there is no public-API input that drives them
// apart (see the note above
// TestTextEncoder_NamesPayloadSourcedFromEntryParallelSlice). One test below
// pins that agreement directly; another manufactures divergence via
// encoder-internal state so sourcing from the wrong list is observable.
// ==============================================================================

// textAlignmentScenario drives a TextEncoder through a sequence of named
// metrics under the given options, stopping right before Finish so the test
// can inspect encoder-internal state.
type textAlignmentScenario struct {
	name  string
	opts  []TextEncoderOption
	names []string // metric names, in insertion order
	tags  bool     // whether to write a non-empty tag per point
}

var textAlignmentScenarios = []textAlignmentScenario{
	{
		name:  "default (names on), no collision",
		opts:  nil,
		names: []string{"cpu.usage", "memory.usage", "disk.usage"},
	},
	{
		name:  "WithoutMetricNames, no collision",
		opts:  []TextEncoderOption{WithoutMetricNames()},
		names: []string{"cpu.usage", "memory.usage", "disk.usage"},
	},
	{
		name:  "WithoutMetricNames, collision forces names on",
		opts:  []TextEncoderOption{WithoutMetricNames()},
		names: []string{cnA, cnB}, // collide under xxHash64
	},
	{
		name:  "default (names on), collision, tags enabled",
		opts:  []TextEncoderOption{WithTextTagsEnabled(true)},
		names: []string{cnA, "unrelated.metric", cnB},
		tags:  true,
	},
	{
		name:  "WithoutMetricNames, tags enabled, no collision",
		opts:  []TextEncoderOption{WithoutMetricNames(), WithTextTagsEnabled(true)},
		names: []string{"a.metric", "b.metric", "c.metric"},
		tags:  true,
	},
}

// buildTextAlignment drives sc through StartMetricName/AddDataPoint/EndMetric
// for every name and returns the still-open encoder (Finish not yet called).
// start is threaded in explicitly (rather than captured via time.Now() here)
// so two builds of the same scenario are byte-comparable.
func buildTextAlignment(t *testing.T, start time.Time, sc textAlignmentScenario) *TextEncoder {
	t.Helper()
	enc, err := NewTextEncoder(start, sc.opts...)
	require.NoError(t, err)

	for i, nm := range sc.names {
		require.NoError(t, enc.StartMetricName(nm, 1))
		tag := ""
		if sc.tags {
			tag = "host=server1"
		}
		require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(i), "v", tag))
		require.NoError(t, enc.EndMetric())
	}

	return enc
}

// TestTextEncoder_MetricNamesAlignedWithEntries asserts e.metricNames grows in
// lockstep with e.indexEntries — same length, same order as insertion —
// across every scenario, mirroring the numeric encoder's invariant.
func TestTextEncoder_MetricNamesAlignedWithEntries(t *testing.T) {
	start := time.Now()
	for _, sc := range textAlignmentScenarios {
		t.Run(sc.name, func(t *testing.T) {
			enc := buildTextAlignment(t, start, sc)

			require.Equal(t, len(sc.names), len(enc.metricNames), "metricNames must have one entry per completed metric")
			require.Equal(t, len(enc.indexEntries), len(enc.metricNames), "metricNames must stay parallel to indexEntries")
			require.Equal(t, sc.names, enc.metricNames, "metricNames must preserve insertion order")
			require.Empty(t, enc.curMetricName, "curMetricName must be cleared once the metric is ended")

			// The encoder must still finish and decode normally.
			data, err := enc.Finish()
			require.NoError(t, err)
			dec, err := NewTextDecoder(data)
			require.NoError(t, err)
			b, err := dec.Decode()
			require.NoError(t, err)
			require.Equal(t, len(sc.names), b.MetricCount())
		})
	}
}

// Replacement note: the two tests that previously lived here —
// TestTextEncoder_NamesPayloadSourceEquivalence
// and TestTextEncoder_ByteIdenticalAcrossRefactor — compared
// enc.collisionTracker.GetMetricNames() against enc.metricNames (or built a
// blob from one in place of the other) and asserted the results matched.
// That comparison cannot fail: through the public API the text encoder never
// reorders entries and never lets a metric start without also ending it
// (StartMetricName is blocked by curMetricID != 0 while a metric is open, and
// Finish refuses with ErrMetricNotEnded if one is left open), so
// collisionTracker.Commit (in StartMetricName) and the append to
// e.metricNames (in EndMetric) always fire exactly once each, in the same
// order, for every completed metric. The two lists are therefore always
// identical in content and order for any input reachable through the public
// API today — TestTextEncoder_MetricNamesAlignedWithEntries above already
// pins that agreement. A test comparing two values that are provably always
// equal cannot fail if Finish regressed to reading the wrong one; it was
// unpinned in exactly the way the surrounding comment claimed to guard
// against.
//
// Replaced with TestTextEncoder_NamesPayloadSourcedFromEntryParallelSlice
// below: a white-box test that manufactures divergence directly (mutating the
// tracker's own backing slice, which GetMetricNames exposes without copying)
// so the two sources are observably different, then asserts Finish's output
// tracks e.metricNames and not the corrupted tracker.

// TestTextEncoder_NamesPayloadSourcedFromEntryParallelSlice is a white-box
// regression test asserting that Finish must build the names payload from
// e.metricNames (the entry-parallel list), not from the collision tracker's
// insertion-order list (internal/collision.Tracker.metricNamesList).
//
// The two lists cannot be driven apart through the public API (see the note
// above), so this test reaches into encoder-internal state to force a
// divergence: collision.Tracker.GetMetricNames returns the tracker's own live
// slice, not a copy, so writing through the returned slice mutates the
// tracker in place while leaving e.metricNames — appended independently in
// EndMetric, a separate backing array — untouched. If Finish is sourcing
// correctly, the corruption is invisible in the output; if Finish ever
// regressed to reading the tracker, the corrupted name would appear in the
// decoded blob instead of the real one.
func TestTextEncoder_NamesPayloadSourcedFromEntryParallelSlice(t *testing.T) {
	start := time.Now()
	for _, sc := range textAlignmentScenarios {
		t.Run(sc.name, func(t *testing.T) {
			enc := buildTextAlignment(t, start, sc)
			require.NotNil(t, enc.collisionTracker, "scenario must exercise Name mode")

			// Scenarios that both opt out of names (WithoutMetricNames) and
			// have no collision never write a names payload at all (Finish's
			// `!e.omitMetricNames || e.hasCollision` gate is false), so
			// corrupting the tracker would be unobservable — skip those, they
			// prove nothing about sourcing.
			if enc.omitMetricNames && !enc.hasCollision {
				t.Skip("scenario writes no names payload (WithoutMetricNames, no collision); sourcing is unobservable")
			}

			trackerNames := enc.collisionTracker.GetMetricNames()
			require.Equal(t, sc.names, trackerNames, "precondition: tracker and entry-parallel lists start identical")
			require.Equal(t, sc.names, enc.metricNames, "precondition: entry-parallel list matches insertion order")

			// Corrupt the tracker's list in place. enc.metricNames must be
			// unaffected (independent backing array) — that gap is the
			// divergence the rest of the test relies on.
			const corrupted = "corrupted.name.not.sourced.from.tracker"
			trackerNames[0] = corrupted
			require.NotEqual(t, corrupted, enc.metricNames[0], "sanity: corrupting the tracker must not alias enc.metricNames")

			data, err := enc.Finish()
			require.NoError(t, err)

			dec, err := NewTextDecoder(data)
			require.NoError(t, err)
			b, err := dec.Decode()
			require.NoError(t, err)

			require.Equal(t, sc.names, b.MetricNames(),
				"decoded names must match the entry-parallel source (enc.metricNames), not the corrupted tracker list")
		})
	}
}
