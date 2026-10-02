//go:build linux

package blob

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// guardedBytes returns a slice whose first readable bytes are writable and
// whose remaining length lies on an inaccessible page, so any read past
// readable faults. Paired with debug.SetPanicOnFault, it lets a test observe
// how far a decoder reads. readable must not exceed the page size.
func guardedBytes(t *testing.T, readable int) ([]byte, bool) {
	t.Helper()

	page := syscall.Getpagesize()
	require.LessOrEqual(t, readable, page)

	mem, err := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := syscall.Munmap(mem); err != nil {
			t.Errorf("munmap guard pages: %v", err)
		}
	})
	require.NoError(t, syscall.Mprotect(mem[page:], syscall.PROT_NONE))

	return mem[page-readable:], true
}
