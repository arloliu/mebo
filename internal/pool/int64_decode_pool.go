package pool

import "sync"

// MaxPooledDecodeInt64s is the largest slice length the int64 decode pool hands out from, and keeps.
// It matches MaxPooledDecodeFloat64s, so a timestamp column and a value column of the same metric pool alike.
const MaxPooledDecodeInt64s = MaxPooledDecodeFloat64s

// decodeInt64Pool backs GetDecodeInt64Slice and PutDecodeInt64Slice.
// It is separate from int64SlicePool, which the encoder uses for its bounded batch buffers,
// so a reader's decode buffer can never end up held by an encoder, and the reverse.
var decodeInt64Pool = sync.Pool{
	New: func() any { return &[]int64{} },
}

// GetDecodeInt64Slice returns a *[]int64 of length size for bulk-decoding one column.
//
// It has the shape of GetDecodeFloat64Slice: the pointer itself rather than a cleanup closure,
// so a warm Get/Put pair does not allocate.
// The contents are stale: callers must overwrite the entries they read.
// Callers keep size within MaxPooledDecodeInt64s; a larger slice works,
// but PutDecodeInt64Slice will not keep it.
//
// Parameters:
//   - size: The desired length of the slice
//
// Returns:
//   - *[]int64: Pointer to a slice with length equal to size
func GetDecodeInt64Slice(size int) *[]int64 {
	ptr, _ := decodeInt64Pool.Get().(*[]int64)
	if cap(*ptr) < size {
		*ptr = make([]int64, size)
	} else {
		*ptr = (*ptr)[:size]
	}

	return ptr
}

// PutDecodeInt64Slice returns ptr (obtained from GetDecodeInt64Slice) to the pool.
// A nil ptr, or one whose capacity exceeds MaxPooledDecodeInt64s, is dropped,
// so the pool never retains more than MaxPooledDecodeInt64s int64s per buffer.
//
// Parameters:
//   - ptr: The pointer returned by GetDecodeInt64Slice
func PutDecodeInt64Slice(ptr *[]int64) {
	if ptr == nil || cap(*ptr) > MaxPooledDecodeInt64s {
		return
	}

	decodeInt64Pool.Put(ptr)
}
