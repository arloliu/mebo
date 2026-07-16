//go:build !race

package pool

// raceEnabled is false in non-race test builds. See race_enabled_test.go for
// why this flag exists.
const raceEnabled = false
