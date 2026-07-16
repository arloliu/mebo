//go:build !amd64

package encoding

// alpFusedDecodeAVX512 has no vectorized implementation off amd64; it always
// declines (returns 0) so the caller decodes the whole column with the generated
// scalar kernels + scalar tail. Kept as a separate build-tagged shim so the
// amd64 wrapper — which owns the AVX-512 kernel and its runtime gate — is the
// only place that varies by architecture, mirroring numeric_alp_encsimd_noasm.go.
func alpFusedDecodeAVX512(codes []byte, n int, width int, mn int64, pf, ie float64, dst []float64) int {
	return 0
}
