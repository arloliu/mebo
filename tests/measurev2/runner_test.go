package main

import (
	"errors"
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestToRawOp(t *testing.T) {
	tests := []struct {
		name string
		in   testing.BenchmarkResult
		want RawOp
	}{
		{
			name: "fractional ns/op",
			in:   testing.BenchmarkResult{N: 3, T: 1000 * time.Nanosecond, MemAllocs: 9, MemBytes: 300},
			want: RawOp{NsPerOp: 1000.0 / 3, BytesPerOp: 100, AllocsPerOp: 3, N: 3, TNs: 1000, MemAllocs: 9, MemBytes: 300},
		},
		{
			name: "measured zero allocations",
			in:   testing.BenchmarkResult{N: 149, T: 50 * time.Millisecond},
			want: RawOp{NsPerOp: 50e6 / 149.0, N: 149, TNs: 50_000_000},
		},
		{
			name: "allocation total just above an integer per op",
			in:   testing.BenchmarkResult{N: 100, T: time.Millisecond, MemAllocs: 10801, MemBytes: 1_850_001},
			want: RawOp{NsPerOp: 10_000, BytesPerOp: 18500, AllocsPerOp: 108, N: 100, TNs: 1_000_000, MemAllocs: 10801, MemBytes: 1_850_001},
		},
		{
			name: "allocation total just below an integer per op",
			in:   testing.BenchmarkResult{N: 100, T: time.Millisecond, MemAllocs: 10799, MemBytes: 1_849_999},
			want: RawOp{NsPerOp: 10_000, BytesPerOp: 18499, AllocsPerOp: 107, N: 100, TNs: 1_000_000, MemAllocs: 10799, MemBytes: 1_849_999},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toRawOp(tt.in)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, float64(got.TNs)/float64(got.N), got.NsPerOp)
		})
	}
}

func TestToRawOpZeroIterationsIsAnError(t *testing.T) {
	_, err := toRawOp(testing.BenchmarkResult{N: 0, T: time.Second})
	require.Error(t, err)
}

func TestRefusingRunner(t *testing.T) {
	_, err := refusingRunner{}.run("mix_monitoring/raw-raw/encode", func() error { return nil })
	require.ErrorIs(t, err, errRefusedTiming)
}

// TestBenchmarkRunnerWarmUp checks that the body runs warmUpCalls times before timing
// and that neither those calls' allocations nor their count reach the result.
func TestBenchmarkRunnerWarmUp(t *testing.T) {
	require.Equal(t, 2, warmUpCalls, "one call leaves role-swapped pool buffers; the spec measured two")
	setBenchtime(t, "10ms")
	calls := 0
	var sink []byte
	body := func() error {
		calls++
		if calls <= warmUpCalls {
			sink = make([]byte, 1<<20) // only the untimed calls allocate
		}

		return nil
	}

	op, err := benchmarkRunner{}.run("counter/raw-raw/encode", body)
	require.NoError(t, err)
	require.NotNil(t, sink)
	require.Equal(t, int64(calls-warmUpCalls), op.N, "timed iterations exclude the warm-up calls")
	require.Less(t, op.MemBytes, uint64(1<<20), "warm-up allocations are excluded")
}

func TestBenchmarkRunnerWarmUpError(t *testing.T) {
	setBenchtime(t, "10ms")
	errBody := errors.New("broken body")
	calls := 0
	_, err := benchmarkRunner{}.run("counter/raw-raw/encode", func() error {
		calls++
		return errBody
	})
	require.ErrorIs(t, err, errBody)
	require.ErrorContains(t, err, "counter/raw-raw/encode")
	require.Equal(t, 1, calls, "the first failing warm-up call stops the benchmark")
}

// setBenchtime sets -test.benchtime for one test and restores it afterwards.
func setBenchtime(t *testing.T, benchtime string) {
	t.Helper()
	testing.Init()
	f := flag.Lookup("test.benchtime")
	require.NotNil(t, f)
	old := f.Value.String()
	t.Cleanup(func() { _ = flag.Set("test.benchtime", old) })
	require.NoError(t, applyBenchtime(benchtime))
}

func TestLegacyMetrics(t *testing.T) {
	op := RawOp{NsPerOp: 1.5, BytesPerOp: 7, AllocsPerOp: 0, N: 2, TNs: 3}
	require.Equal(t, BenchMetrics{NsPerOp: 1.5, BytesPerOp: 7, AllocsPerOp: 0}, legacyMetrics(&op))
}
