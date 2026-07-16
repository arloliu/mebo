//go:build race

package pool

// raceEnabled is true when the test binary is built with -race.
//
// Go's sync.Pool.Put intentionally drops ~25% of items at random when the
// race detector is enabled (see runtime/sync/pool.go: the race.Enabled +
// runtime_randn(4) == 0 branch), a deliberate behavior to help expose races
// around pooled objects. That makes any test asserting a sync.Pool-backed
// warm-path is allocation-free inherently flaky under -race, independent of
// whether the production code is correct.
const raceEnabled = true
