//go:build !race

package blob

// raceEnabled is false in non-race test builds; alloc assertions run.
const raceEnabled = false
