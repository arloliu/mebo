package fbscompare

import (
	"testing"
)

// TestTimestampPattern examines the actual timestamp values
func TestTimestampPattern(t *testing.T) {
	cfg := DefaultTestDataConfig(200, 250)
	testData := GenerateTestData(cfg)

	// Look at first metric's timestamps
	metric := testData[0]

	t.Logf("First 20 timestamps of first metric:")
	for i := 0; i < 20 && i < len(metric.Timestamps); i++ {
		ts := metric.Timestamps[i]
		t.Logf("  [%2d] %20d  (0x%016x)", i, ts, ts)
	}

	// Check deltas
	t.Logf("\nDeltas between consecutive timestamps:")
	for i := 1; i < 20 && i < len(metric.Timestamps); i++ {
		delta := metric.Timestamps[i] - metric.Timestamps[i-1]
		t.Logf("  [%2d] %15d  (0x%012x)", i, delta, delta)
	}

	// Check how many bytes change
	t.Logf("\nByte-level comparison of first 5 timestamps:")
	for i := 0; i < 5 && i < len(metric.Timestamps); i++ {
		ts := metric.Timestamps[i]
		bytes := make([]byte, 8)
		for b := 0; b < 8; b++ {
			bytes[b] = byte(ts >> (b * 8))
		}
		t.Logf("  [%d] % x", i, bytes)
	}

	// Check last 5 timestamps too
	t.Logf("\nByte-level comparison of LAST 5 timestamps:")
	startIdx := len(metric.Timestamps) - 5
	for i := startIdx; i < len(metric.Timestamps); i++ {
		ts := metric.Timestamps[i]
		bytes := make([]byte, 8)
		for b := 0; b < 8; b++ {
			bytes[b] = byte(ts >> (b * 8))
		}
		t.Logf("  [%d] % x", i, bytes)
	}

	// Count how many timestamps share the same last 4 bytes
	lastFourBytesMap := make(map[uint32]int)
	for _, ts := range metric.Timestamps {
		lastFour := uint32(ts >> 32) // Get upper 4 bytes
		lastFourBytesMap[lastFour]++
	}
	t.Logf("\nNumber of unique upper-4-byte patterns: %d (out of %d timestamps)", len(lastFourBytesMap), len(metric.Timestamps))
	if len(lastFourBytesMap) < 10 {
		t.Logf("  Upper-4-byte values and counts:")
		for val, count := range lastFourBytesMap {
			t.Logf("    0x%08x: %d times", val, count)
		}
	}
}
