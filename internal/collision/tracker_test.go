package collision

import (
	"testing"

	"github.com/arloliu/mebo/errs"
	"github.com/stretchr/testify/require"
)

// trackMetric replays the old one-shot TrackMetric behaviour (probe, then
// commit unless the probe found a duplicate name) on top of the Probe/Commit
// split that NumericEncoder/TextEncoder now call directly. It exists purely so
// these tests can express "track this (name, hash) pair" concisely; production
// code never calls it — see blob/numeric_encoder.go's AddMetric /
// blob/text_encoder.go's equivalent for the real call sites.
func trackMetric(tracker *Tracker, name string, hash uint64) error {
	dupName, _, err := tracker.Probe(name, hash)
	if err != nil {
		return err
	}
	if dupName {
		return errs.ErrMetricAlreadyStarted
	}

	tracker.Commit(name, hash)

	return nil
}

func TestNewTracker(t *testing.T) {
	tracker := NewTracker()

	require.NotNil(t, tracker)
	require.Equal(t, 0, tracker.Count())
	require.False(t, tracker.HasCollision())
	require.Empty(t, tracker.GetMetricNames())
}

func TestTracker_ProbeCommit_Success(t *testing.T) {
	tracker := NewTracker()

	// Track first metric
	err := trackMetric(tracker, "cpu.usage", 0x1234567890abcdef)
	require.NoError(t, err)
	require.Equal(t, 1, tracker.Count())
	require.False(t, tracker.HasCollision())
	require.Equal(t, []string{"cpu.usage"}, tracker.GetMetricNames())

	// Track second metric
	err = trackMetric(tracker, "mem.usage", 0xfedcba0987654321)
	require.NoError(t, err)
	require.Equal(t, 2, tracker.Count())
	require.False(t, tracker.HasCollision())
	require.Equal(t, []string{"cpu.usage", "mem.usage"}, tracker.GetMetricNames())
}

func TestTracker_Probe_EmptyName(t *testing.T) {
	tracker := NewTracker()

	dupName, prospectiveCollision, err := tracker.Probe("", 0x1234567890abcdef)

	require.ErrorIs(t, err, errs.ErrInvalidMetricName)
	require.False(t, dupName)
	require.False(t, prospectiveCollision)
	require.Equal(t, 0, tracker.Count())
	require.False(t, tracker.HasCollision())
}

func TestTracker_ProbeCommit_Collision(t *testing.T) {
	tracker := NewTracker()

	// Track first metric
	err := trackMetric(tracker, "cpu.usage", 0x1234567890abcdef)
	require.NoError(t, err)
	require.False(t, tracker.HasCollision())

	// Probe reports the prospective collision before any mutation happens.
	dupName, prospectiveCollision, err := tracker.Probe("cpu.idle", 0x1234567890abcdef)
	require.NoError(t, err)
	require.False(t, dupName)
	require.True(t, prospectiveCollision)
	require.False(t, tracker.HasCollision()) // Probe never mutates.

	// Track second metric with same hash but different name.
	// This should NOT return error - collision is handled automatically.
	err = trackMetric(tracker, "cpu.idle", 0x1234567890abcdef)
	require.NoError(t, err)
	require.True(t, tracker.HasCollision())
	require.Equal(t, 2, tracker.Count()) // Both metrics tracked
	require.Equal(t, []string{"cpu.usage", "cpu.idle"}, tracker.GetMetricNames())
}

func TestTracker_ProbeCommit_Duplicate(t *testing.T) {
	tracker := NewTracker()

	// Track first metric
	err := trackMetric(tracker, "cpu.usage", 0x1234567890abcdef)
	require.NoError(t, err)

	// Probe the same metric again (same name, same hash) - reported as a
	// duplicate without mutating the tracker.
	dupName, prospectiveCollision, err := tracker.Probe("cpu.usage", 0x1234567890abcdef)
	require.NoError(t, err)
	require.True(t, dupName)
	require.False(t, prospectiveCollision)

	// The one-shot helper surfaces that as ErrMetricAlreadyStarted, same as
	// the production Probe-then-reject call sites.
	err = trackMetric(tracker, "cpu.usage", 0x1234567890abcdef)
	require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)
	require.False(t, tracker.HasCollision()) // Not a collision, just duplicate
	require.Equal(t, 1, tracker.Count())     // Only tracked once
}

func TestTracker_GetMetricNames_PreservesOrder(t *testing.T) {
	tracker := NewTracker()

	metrics := []struct {
		name string
		hash uint64
	}{
		{"cpu.usage", 0x0001},
		{"mem.usage", 0x0002},
		{"disk.usage", 0x0003},
		{"net.usage", 0x0004},
	}

	for _, m := range metrics {
		err := trackMetric(tracker, m.name, m.hash)
		require.NoError(t, err)
	}

	names := tracker.GetMetricNames()
	require.Equal(t, 4, len(names))
	require.Equal(t, "cpu.usage", names[0])
	require.Equal(t, "mem.usage", names[1])
	require.Equal(t, "disk.usage", names[2])
	require.Equal(t, "net.usage", names[3])
}

func TestTracker_Reset(t *testing.T) {
	tracker := NewTracker()

	// Track some metrics
	_ = trackMetric(tracker, "cpu.usage", 0x1234567890abcdef)
	_ = trackMetric(tracker, "mem.usage", 0xfedcba0987654321)
	require.Equal(t, 2, tracker.Count())

	// Reset
	tracker.Reset()

	require.Equal(t, 0, tracker.Count())
	require.False(t, tracker.HasCollision())
	require.Empty(t, tracker.GetMetricNames())

	// Should be able to track new metrics after reset
	err := trackMetric(tracker, "disk.usage", 0x1111111111111111)
	require.NoError(t, err)
	require.Equal(t, 1, tracker.Count())
	require.Equal(t, []string{"disk.usage"}, tracker.GetMetricNames())
}

func TestTracker_Reset_PreservesCapacity(t *testing.T) {
	tracker := NewTracker()

	// Track many metrics to allocate capacity
	for i := range 100 {
		_ = trackMetric(tracker, "metric", uint64(i))
	}

	initialCap := cap(tracker.metricNamesList)

	// Reset should preserve capacity
	tracker.Reset()

	require.Equal(t, 0, len(tracker.metricNamesList))
	require.GreaterOrEqual(t, cap(tracker.metricNamesList), initialCap)
}

func TestTracker_HasCollision_AfterCollision(t *testing.T) {
	tracker := NewTracker()

	// Track first metric
	_ = trackMetric(tracker, "cpu.usage", 0x1234567890abcdef)
	require.False(t, tracker.HasCollision())

	// Trigger collision
	_ = trackMetric(tracker, "cpu.idle", 0x1234567890abcdef)
	require.True(t, tracker.HasCollision())

	// Collision flag persists
	_ = trackMetric(tracker, "mem.usage", 0xfedcba0987654321)
	require.True(t, tracker.HasCollision())
}

func TestTracker_MultipleCollisions(t *testing.T) {
	tracker := NewTracker()

	// Track first metric
	err := trackMetric(tracker, "metric1", 0x0001)
	require.NoError(t, err)

	// First collision - should not return error
	err = trackMetric(tracker, "metric2", 0x0001)
	require.NoError(t, err)
	require.True(t, tracker.HasCollision())

	// Second collision (different hash) - should not return error
	err = trackMetric(tracker, "metric3", 0x0002)
	require.NoError(t, err)
	err = trackMetric(tracker, "metric4", 0x0002)
	require.NoError(t, err)
	require.True(t, tracker.HasCollision())

	// Should have all 4 metrics tracked
	require.Equal(t, 4, tracker.Count())
}
