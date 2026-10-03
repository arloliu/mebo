package pool

import "sync"

// MaxPooledDecodeFloat64s is the largest slice length the decode pool hands out from, and keeps.
// 8192 float64s is 64 KiB: several times a typical metric column, small enough that idle pooled buffers stay cheap.
// Callers decoding longer columns should use a streaming path instead of asking for a buffer.
const MaxPooledDecodeFloat64s = 8192

// decodeFloat64Pool backs GetDecodeFloat64Slice and PutDecodeFloat64Slice.
// It is separate from float64SlicePool, which the encoder uses for its bounded batch buffers,
// so a reader's decode buffer can never end up held by an encoder, and the reverse.
var decodeFloat64Pool = sync.Pool{
	New: func() any { return &[]float64{} },
}

// GetDecodeFloat64Slice returns a *[]float64 of length size for bulk-decoding one column.
//
// Like GetUint64Slice it returns the pointer itself rather than a cleanup closure,
// which would escape to the heap on every call, so a warm Get/Put pair does not allocate.
// The contents are stale: callers must overwrite the entries they read.
// Callers keep size within MaxPooledDecodeFloat64s; a larger slice works,
// but PutDecodeFloat64Slice will not keep it.
//
// Parameters:
//   - size: The desired length of the slice
//
// Returns:
//   - *[]float64: Pointer to a slice with length equal to size
func GetDecodeFloat64Slice(size int) *[]float64 {
	ptr, _ := decodeFloat64Pool.Get().(*[]float64)
	if cap(*ptr) < size {
		*ptr = make([]float64, size)
	} else {
		*ptr = (*ptr)[:size]
	}

	return ptr
}

// PutDecodeFloat64Slice returns ptr (obtained from GetDecodeFloat64Slice) to the pool.
// A nil ptr, or one whose capacity exceeds MaxPooledDecodeFloat64s, is dropped,
// so the pool never retains more than MaxPooledDecodeFloat64s float64s per buffer.
//
// Parameters:
//   - ptr: The pointer returned by GetDecodeFloat64Slice
func PutDecodeFloat64Slice(ptr *[]float64) {
	if ptr == nil || cap(*ptr) > MaxPooledDecodeFloat64s {
		return
	}

	decodeFloat64Pool.Put(ptr)
}
