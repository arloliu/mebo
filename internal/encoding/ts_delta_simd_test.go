package encoding

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNextDeltaOfDelta(t *testing.T) {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prevTS := tt.prevTS
			prevDelta := tt.prevDelta
			got := make([]int64, 0, len(tt.input))

			for _, ts := range tt.input {
				got = append(got, nextDeltaOfDelta(ts, &prevTS, &prevDelta))
			}

			require.Equal(t, tt.expected, got)
			require.Equal(t, tt.expectedLastTS, prevTS)
			require.Equal(t, tt.expectedDelta, prevDelta)
		})
	}
}

func TestDeltaOfDeltaBackendsMatch(t *testing.T) {
	timestamps := []int64{
		1_000_000,
		2_000_000,
		3_000_100,
		4_000_050,
		5_000_300,
		6_000_350,
		7_000_400,
		8_000_200,
	}

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
}

func TestTimestampDeltaEncoderBackendsMatch(t *testing.T) {
	timestamps := benchmarkDeltaOfDeltaTimestamps(256)
	scalarRestore := setDeltaOfDeltaBackendForTest(deltaOfDeltaBackendScalar)
	scalarEncoder := NewTimestampDeltaEncoder()
	scalarEncoder.WriteSlice(timestamps)
	scalarBytes := append([]byte(nil), scalarEncoder.Bytes()...)
	scalarEncoder.Finish()
	scalarRestore()

	for _, backend := range deltaOfDeltaBackends {
		if backend == deltaOfDeltaBackendScalar || !deltaOfDeltaBackendSupported(backend) {
			continue
		}

		restore := setDeltaOfDeltaBackendForTest(backend)
		encoder := NewTimestampDeltaEncoder()
		encoder.WriteSlice(timestamps)
		gotBytes := append([]byte(nil), encoder.Bytes()...)
		encoder.Finish()
		restore()

		require.Equalf(t, scalarBytes, gotBytes, "%s backend encoded bytes mismatch", deltaOfDeltaBackendName(backend))
	}
}

func TestTimestampDeltaPackedEncoderBackendsMatch(t *testing.T) {
	timestamps := benchmarkDeltaOfDeltaTimestamps(256)
	scalarRestore := setDeltaOfDeltaBackendForTest(deltaOfDeltaBackendScalar)
	scalarEncoder := NewTimestampDeltaPackedEncoder()
	scalarEncoder.WriteSlice(timestamps)
	scalarBytes := append([]byte(nil), scalarEncoder.Bytes()...)
	scalarEncoder.Finish()
	scalarRestore()

	for _, backend := range deltaOfDeltaBackends {
		if backend == deltaOfDeltaBackendScalar || !deltaOfDeltaBackendSupported(backend) {
			continue
		}

		restore := setDeltaOfDeltaBackendForTest(backend)
		encoder := NewTimestampDeltaPackedEncoder()
		encoder.WriteSlice(timestamps)
		gotBytes := append([]byte(nil), encoder.Bytes()...)
		encoder.Finish()
		restore()

		require.Equalf(t, scalarBytes, gotBytes, "%s backend encoded bytes mismatch", deltaOfDeltaBackendName(backend))
	}
}
