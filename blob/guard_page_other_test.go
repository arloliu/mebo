//go:build !linux

package blob

import "testing"

// guardedBytes is only implemented on linux; callers skip elsewhere.
func guardedBytes(t *testing.T, _ int) ([]byte, bool) {
	t.Helper()

	return nil, false
}
