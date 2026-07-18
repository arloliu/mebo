package deltadelta

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntoActiveBackendsMatchScalarOutput(t *testing.T) {
	for _, test := range []struct {
		name   string
		srcLen int
	}{
		{name: "source_length_10", srcLen: 10},
		{name: "source_length_11", srcLen: 11},
		{name: "source_length_12", srcLen: 12},
		{name: "source_length_13", srcLen: 13},
	} {
		t.Run(test.name, func(t *testing.T) {
			timestamps := makeDeltaOfDeltaTestTimestamps(test.srcLen + 2)
			src := timestamps[2:]
			prevTS := timestamps[1]
			prevDelta := timestamps[1] - timestamps[0]

			scalar := make([]int64, len(src))
			scalarLastTS, scalarLastDelta := deltaOfDeltaIntoScalar(scalar, src, prevTS, prevDelta)

			for _, backend := range deltaOfDeltaBackends {
				if backend == deltaOfDeltaBackendScalar || !deltaOfDeltaBackendSupported(backend) {
					continue
				}

				got := make([]int64, len(src))
				lastTS, lastDelta := deltaOfDeltaKernelForBackend(backend)(got, src, prevTS, prevDelta)

				require.Equalf(t, scalar, got, "%s backend output mismatch", deltaOfDeltaBackendName(backend))
				require.Equalf(t, scalarLastTS, lastTS, "%s backend last timestamp mismatch", deltaOfDeltaBackendName(backend))
				require.Equalf(t, scalarLastDelta, lastDelta, "%s backend last delta mismatch", deltaOfDeltaBackendName(backend))
			}
		})
	}
}

func TestIntoActiveUsesSelectedBackend(t *testing.T) {
	timestamps := []int64{1_000_000, 2_000_000, 3_000_100, 4_000_050}
	src := timestamps[2:]
	prevTS := timestamps[1]
	prevDelta := timestamps[1] - timestamps[0]

	for _, backend := range deltaOfDeltaBackends {
		if !deltaOfDeltaBackendSupported(backend) {
			continue
		}

		t.Run(deltaOfDeltaBackendName(backend), func(t *testing.T) {
			restore := setDeltaOfDeltaBackendForTest(backend)
			defer restore()

			got := make([]int64, len(src))
			lastTS, lastDelta := IntoActive(got, src, prevTS, prevDelta)

			require.Equal(t, []int64{100, -150}, got)
			require.Equal(t, timestamps[len(timestamps)-1], lastTS)
			require.Equal(t, int64(999_950), lastDelta)
		})
	}
}

func TestDeltaOfDeltaIntoScalar(t *testing.T) {
	tests := []struct {
		name           string
		prevTS         int64
		prevDelta      int64
		input          []int64
		expected       []int64
		expectedLastTS int64
		expectedDelta  int64
	}{
		{
			name:           "regular intervals stay zero",
			prevTS:         2_000_000,
			prevDelta:      1_000_000,
			input:          []int64{3_000_000, 4_000_000, 5_000_000},
			expected:       []int64{0, 0, 0},
			expectedLastTS: 5_000_000,
			expectedDelta:  1_000_000,
		},
		{
			name:           "jitter produces positive and negative swings",
			prevTS:         2_000_000,
			prevDelta:      1_000_000,
			input:          []int64{3_000_100, 4_000_050, 5_000_300},
			expected:       []int64{100, -150, 300},
			expectedLastTS: 5_000_300,
			expectedDelta:  1_000_250,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := make([]int64, len(test.input))
			lastTS, lastDelta := deltaOfDeltaIntoScalar(got, test.input, test.prevTS, test.prevDelta)

			require.Equal(t, test.expected, got)
			require.Equal(t, test.expectedLastTS, lastTS)
			require.Equal(t, test.expectedDelta, lastDelta)
		})
	}
}

func TestShouldUseHonorsBackendThreshold(t *testing.T) {
	restore := setDeltaOfDeltaBackendForTest(deltaOfDeltaBackendScalar)
	require.False(t, ShouldUse(ChunkSize))
	restore()

	for _, backend := range deltaOfDeltaBackends {
		if backend == deltaOfDeltaBackendScalar || !deltaOfDeltaBackendSupported(backend) {
			continue
		}

		t.Run(deltaOfDeltaBackendName(backend), func(t *testing.T) {
			restore := setDeltaOfDeltaBackendForTest(backend)
			defer restore()

			threshold := deltaOfDeltaSIMDMinLenForBackend(backend)
			require.False(t, ShouldUse(threshold-1))
			require.True(t, ShouldUse(threshold))
		})
	}
}

func makeDeltaOfDeltaTestTimestamps(count int) []int64 {
	timestamps := make([]int64, count)
	timestamps[0] = 1_000_000

	deltas := []int64{1_000_000, 1_000_100, 999_950, 1_000_250, 999_900, 1_000_400, 999_700}
	for i := 1; i < len(timestamps); i++ {
		timestamps[i] = timestamps[i-1] + deltas[(i-1)%len(deltas)]
	}

	return timestamps
}
