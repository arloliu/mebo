//go:build race

package blob

// raceEnabled is true under -race; the runtime adds bookkeeping allocations that
// make zero-alloc assertions flaky, so they are skipped in that build.
const raceEnabled = true
