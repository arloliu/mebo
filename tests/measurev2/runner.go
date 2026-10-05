package main

import (
	"errors"
	"fmt"
	"testing"
)

// errRefusedTiming is returned by refusingRunner, which -sizes-only installs.
var errRefusedTiming = errors.New("timing runner called in -sizes-only mode")

// timingRunner times one cell's operation body; every timing in measurev2 goes through it.
// The caller prepares the body's fixtures before calling run, so preparation is never timed.
type timingRunner interface {
	run(cell string, body func() error) (RawOp, error)
}

// benchmarkRunner times a body with testing.Benchmark and b.Loop at the current -test.benchtime.
type benchmarkRunner struct{}

// refusingRunner fails every call; -sizes-only installs it so a timing call cannot slip in.
type refusingRunner struct{}

// toRawOp converts a testing.BenchmarkResult to a raw operation object.
// A result with no iterations is an error, never a division by one.
func toRawOp(r testing.BenchmarkResult) (RawOp, error) {
	if r.N <= 0 {
		return RawOp{}, fmt.Errorf("benchmark ran %d iterations", r.N)
	}

	return RawOp{
		NsPerOp:     float64(r.T.Nanoseconds()) / float64(r.N),
		BytesPerOp:  r.AllocedBytesPerOp(),
		AllocsPerOp: r.AllocsPerOp(),
		N:           int64(r.N),
		TNs:         r.T.Nanoseconds(),
		MemAllocs:   r.MemAllocs,
		MemBytes:    r.MemBytes,
	}, nil
}

// legacyMetrics converts a raw operation object to the legacy schema's three fields.
func legacyMetrics(op *RawOp) BenchMetrics {
	return BenchMetrics{NsPerOp: op.NsPerOp, BytesPerOp: op.BytesPerOp, AllocsPerOp: op.AllocsPerOp}
}

// run calls testing.Benchmark, which runs runtime.GC before the benchmark function, as it always has.
// A body error stops the benchmark and is returned with the cell id.
func (benchmarkRunner) run(cell string, body func() error) (RawOp, error) {
	var bodyErr error
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			if err := body(); err != nil {
				bodyErr = err
				b.FailNow()
			}
		}
	})
	if bodyErr != nil {
		return RawOp{}, fmt.Errorf("%s: %w", cell, bodyErr)
	}

	op, err := toRawOp(result)
	if err != nil {
		return RawOp{}, fmt.Errorf("%s: %w", cell, err)
	}

	return op, nil
}

// run refuses to time anything.
func (refusingRunner) run(cell string, _ func() error) (RawOp, error) {
	return RawOp{}, fmt.Errorf("%s: %w", cell, errRefusedTiming)
}
