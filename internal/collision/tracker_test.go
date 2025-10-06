package collision

import (
	"testing"

	"github.com/arloliu/mebo/errs"
	"github.com/stretchr/testify/require"
)

func TestNewTracker(t *testing.T) {
	tracker := NewTracker()

	require.NotNil(t, tracker)
	require.Equal(t, 0, tracker.Count())
	require.False(t, tracker.HasCollision())
	require.Empty(t, tracker.GetMetricNames())
}

func TestTracker_TrackMetric_Success(t *testing.T) {
	tracker := NewTracker()

	// Track first metric
	err := tracker.TrackMetric("cpu.usage", 0x1234567890abcdef)
	require.NoError(t, err)
	require.Equal(t, 1, tracker.Count())
	require.False(t, tracker.HasCollision())
	require.Equal(t, []string{"cpu.usage"}, tracker.GetMetricNames())

	// Track second metric
	err = tracker.TrackMetric("mem.usage", 0xfedcba0987654321)
	require.NoError(t, err)
	require.Equal(t, 2, tracker.Count())
	require.False(t, tracker.HasCollision())
	require.Equal(t, []string{"cpu.usage", "mem.usage"}, tracker.GetMetricNames())
}

func TestTracker_TrackMetric_EmptyName(t *testing.T) {
	tracker := NewTracker()

	err := tracker.TrackMetric("", 0x1234567890abcdef)

	require.ErrorIs(t, err, errs.ErrInvalidMetricName)
	require.Equal(t, 0, tracker.Count())
	require.False(t, tracker.HasCollision())
}

func TestTracker_TrackMetric_Collision(t *testing.T) {
	tracker := NewTracker()

	// Track first metric
	err := tracker.TrackMetric("cpu.usage", 0x1234567890abcdef)
	require.NoError(t, err)
	require.False(t, tracker.HasCollision())

	// Track second metric with same hash but different name
	// This should NOT return error - collision is handled automatically
	err = tracker.TrackMetric("cpu.idle", 0x1234567890abcdef)
	require.NoError(t, err)
	require.True(t, tracker.HasCollision())
	require.Equal(t, 2, tracker.Count()) // Both metrics tracked
	require.Equal(t, []string{"cpu.usage", "cpu.idle"}, tracker.GetMetricNames())
}

func TestTracker_TrackMetric_Duplicate(t *testing.T) {
	tracker := NewTracker()

	// Track first metric
	err := tracker.TrackMetric("cpu.usage", 0x1234567890abcdef)
	require.NoError(t, err)

	// Track same metric again (same name, same hash)
	err = tracker.TrackMetric("cpu.usage", 0x1234567890abcdef)
	require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)
	require.False(t, tracker.HasCollision()) // Not a collision, just duplicate
	require.Equal(t, 1, tracker.Count())     // Only tracked once
}

func TestTracker_TrackMetricID_Success(t *testing.T) {
	tracker := NewTracker()

	// Track first metric ID
	err := tracker.TrackMetricID(0x1111111111111111)
	require.NoError(t, err)

	// Track second metric ID
	err = tracker.TrackMetricID(0x2222222222222222)
	require.NoError(t, err)
}

func TestTracker_TrackMetricID_Collision(t *testing.T) {
	tracker := NewTracker()

	// Track first metric ID
	err := tracker.TrackMetricID(0x1234567890abcdef)
	require.NoError(t, err)

	// Try to track same metric ID again - should fail
	err = tracker.TrackMetricID(0x1234567890abcdef)
	require.ErrorIs(t, err, errs.ErrHashCollision)
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
		err := tracker.TrackMetric(m.name, m.hash)
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
	_ = tracker.TrackMetric("cpu.usage", 0x1234567890abcdef)
	_ = tracker.TrackMetric("mem.usage", 0xfedcba0987654321)
	require.Equal(t, 2, tracker.Count())

	// Reset
	tracker.Reset()

	require.Equal(t, 0, tracker.Count())
	require.False(t, tracker.HasCollision())
	require.Empty(t, tracker.GetMetricNames())

	// Should be able to track new metrics after reset
	err := tracker.TrackMetric("disk.usage", 0x1111111111111111)
	require.NoError(t, err)
	require.Equal(t, 1, tracker.Count())
	require.Equal(t, []string{"disk.usage"}, tracker.GetMetricNames())
}

func TestTracker_Reset_PreservesCapacity(t *testing.T) {
	tracker := NewTracker()

	// Track many metrics to allocate capacity
	for i := 0; i < 100; i++ {
		_ = tracker.TrackMetric("metric", uint64(i))
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
	_ = tracker.TrackMetric("cpu.usage", 0x1234567890abcdef)
	require.False(t, tracker.HasCollision())

	// Trigger collision
	_ = tracker.TrackMetric("cpu.idle", 0x1234567890abcdef)
	require.True(t, tracker.HasCollision())

	// Collision flag persists
	_ = tracker.TrackMetric("mem.usage", 0xfedcba0987654321)
	require.True(t, tracker.HasCollision())
}

func TestTracker_MultipleCollisions(t *testing.T) {
	tracker := NewTracker()

	// Track first metric
	err := tracker.TrackMetric("metric1", 0x0001)
	require.NoError(t, err)

	// First collision - should not return error
	err = tracker.TrackMetric("metric2", 0x0001)
	require.NoError(t, err)
	require.True(t, tracker.HasCollision())

	// Second collision (different hash) - should not return error
	err = tracker.TrackMetric("metric3", 0x0002)
	require.NoError(t, err)
	err = tracker.TrackMetric("metric4", 0x0002)
	require.NoError(t, err)
	require.True(t, tracker.HasCollision())

	// Should have all 4 metrics tracked
	require.Equal(t, 4, tracker.Count())
}
