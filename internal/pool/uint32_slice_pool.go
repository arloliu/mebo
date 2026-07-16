package pool

import "sync"

// uint32SlicePool backs GetUint32Slice. It is dedicated to sparse counting
// tables (currently the ALP-RD left-part histogram in internal/encoding): a
// caller indexes a small, data-dependent subset of a large table, then resets
// exactly those entries before releasing. See GetUint32Slice for the invariant
// callers MUST uphold.
var uint32SlicePool = sync.Pool{
	New: func() any { return &[]uint32{} },
}

// GetUint32Slice retrieves and resizes a uint32 slice from the pool for use as a
// sparse counting table, and returns a cleanup function that returns it.
//
// The returned slice has length exactly size. Unlike GetInt64Slice/
// GetFloat64Slice (whose callers overwrite every entry densely), this pool is
// specialised for SPARSE accumulation and maintains an all-zero-at-rest
// invariant: the backing array is fully zero when handed out, and callers MUST
// leave it fully zero when they release it.
//
// Concretely, a caller may increment/write only a data-dependent subset of
// indices, but MUST zero exactly those indices (typically via a touched-index
// list) before calling cleanup. A fresh slice is zeroed by make; every reuse
// then stays zero by induction because the previous holder cleared everything
// it wrote. This is what lets the counting table be cleared in O(distinct)
// rather than O(size) per column — the whole point of pooling a 64Ki-entry
// table across short-lived encoders.
//
// A caller that writes an index without recording it for reset would poison
// the pool for the next holder, so this pool must not be used for dense or
// unclearable workloads.
//
// Parameters:
//   - size: The desired length of the slice
//
// Returns:
//   - []uint32: A slice with length equal to size, all entries zero
//   - func(): Cleanup function that must be called (typically with defer) to
//     return the slice to the pool. The caller must have already re-zeroed
//     every entry it wrote.
func GetUint32Slice(size int) ([]uint32, func()) {
	ptr, _ := uint32SlicePool.Get().(*[]uint32)
	slice := (*ptr)[:0]

	if cap(slice) < size {
		slice = make([]uint32, size)
		*ptr = slice
	} else {
		slice = slice[:size]
		*ptr = slice
	}

	return slice, func() { uint32SlicePool.Put(ptr) }
}
