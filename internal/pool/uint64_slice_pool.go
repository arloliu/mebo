package pool

import "sync"

// uint64SlicePool backs GetUint64Slice/PutUint64Slice: pooled []uint64
// scratch for bulk-decode combine passes (currently ALP-RD's decodeRDInto in
// internal/encoding, which unpacks left dictionary codes and right parts into
// two of these buffers per call).
//
// This pool deliberately does NOT follow the GetInt64Slice/GetFloat64Slice/
// GetStringSlice shape (return the slice plus a cleanup closure): a closure
// returned from a function escapes to the heap by construction (it outlives
// the stack frame that created it), so that shape costs one allocation per
// Get even when the underlying array is warm and reused from the pool. That
// is invisible for those pools' callers (a handful of Gets per column
// encode), but decodeRDInto's bulk path is called on the hot decode loop,
// where a zero-allocation bar applies per call. Handing back the
// *[]uint64 pointer itself and pairing it with a plain PutUint64Slice
// function (mirroring byte_buffer_pool.go's Get/Put-on-a-pointer shape, not
// slice_pool.go's Get-plus-closure shape) keeps a warm Get+Put pair truly
// allocation-free — see internal/pool/uint64_slice_pool_test.go.
var uint64SlicePool = sync.Pool{
	New: func() any { return &[]uint64{} },
}

// GetUint64Slice retrieves a *[]uint64 from the pool, resized to length size
// (its backing array is grown via make if the pooled one is too small). The
// caller must return it via PutUint64Slice(ptr) (typically via defer).
//
// Parameters:
//   - size: The desired length of the slice
//
// Returns:
//   - *[]uint64: pointer to a slice with length equal to size
//
// Example:
//
//	ptr := pool.GetUint64Slice(1000)
//	defer pool.PutUint64Slice(ptr)
//	codes := *ptr
//	// Use codes...
func GetUint64Slice(size int) *[]uint64 {
	ptr, _ := uint64SlicePool.Get().(*[]uint64)
	slice := (*ptr)[:0]

	if cap(slice) < size {
		slice = make([]uint64, size)
	} else {
		slice = slice[:size]
	}
	*ptr = slice

	return ptr
}

// PutUint64Slice returns ptr (obtained from GetUint64Slice) to the pool.
func PutUint64Slice(ptr *[]uint64) {
	if ptr == nil {
		return
	}

	uint64SlicePool.Put(ptr)
}
