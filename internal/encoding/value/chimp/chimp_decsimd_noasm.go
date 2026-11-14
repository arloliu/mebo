//go:build !amd64

package chimp

// chimpGroupKernel has no implementation off amd64, where chimpGroupAVX512 is false and nothing calls it.
// It completes no step, which leaves every value to the scalar loop.
func chimpGroupKernel(*chimpGroupState, int, *uint64) int {
	return 0
}
