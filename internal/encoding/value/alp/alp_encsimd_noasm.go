//go:build !amd64

package alp

// alpMainStatsSIMD has no vectorized implementation off amd64; it is always the
// scalar reference. Kept as a separate build-tagged shim (rather than calling
// alpMainStatsScalar directly from alpMainStats) so the amd64 dispatch — which
// owns the AVX-512 kernel and its runtime gate — is the only place that varies
// by architecture.
func alpMainStatsSIMD(values []float64, ee, ff int, dst []uint64, excPos []uint32) (alpMainCand, []uint32) {
	return alpMainStatsScalar(values, ee, ff, dst, excPos)
}

// alpBestEFSIMD has no vectorized implementation off amd64: ok is always false, and the caller runs the scalar search.
func alpBestEFSIMD(_ []float64, _, _ int) (bestE, bestF int, ok bool) {
	return 0, 0, false
}
