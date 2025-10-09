package pool

import "sync"

// Slice pools for efficient reuse of typed slices.
// These pools help reduce allocations when transforming row-based data to columnar format.
var (
	int64SlicePool = sync.Pool{
		New: func() any { return &[]int64{} },
	}
	float64SlicePool = sync.Pool{
		New: func() any { return &[]float64{} },
	}
	stringSlicePool = sync.Pool{
		New: func() any { return &[]string{} },
	}
)

// GetInt64Slice retrieves and resizes an int64 slice from the pool.
//
// The returned slice will have the exact length specified by the size parameter.
// If the pooled slice has insufficient capacity, a new slice will be allocated.
// The caller must call the returned cleanup function to return the slice to the pool.
//
// Parameters:
//   - size: The desired length of the slice
//
// Returns:
//   - []int64: A slice with length equal to size
//   - func(): Cleanup function that must be called (typically with defer) to return the slice to the pool
//
// Example:
//
//	timestamps, cleanup := pool.GetInt64Slice(1000)
//	defer cleanup()
//	// Use timestamps slice...
func GetInt64Slice(size int) ([]int64, func()) {
	ptr, _ := int64SlicePool.Get().(*[]int64)
	slice := (*ptr)[:0]

	if cap(slice) < size {
		slice = make([]int64, size)
		*ptr = slice
	} else {
		slice = slice[:size]
		*ptr = slice
	}

	return slice, func() { int64SlicePool.Put(ptr) }
}

// GetFloat64Slice retrieves and resizes a float64 slice from the pool.
//
// The returned slice will have the exact length specified by the size parameter.
// If the pooled slice has insufficient capacity, a new slice will be allocated.
// The caller must call the returned cleanup function to return the slice to the pool.
//
// Parameters:
//   - size: The desired length of the slice
//
// Returns:
//   - []float64: A slice with length equal to size
//   - func(): Cleanup function that must be called (typically with defer) to return the slice to the pool
//
// Example:
//
//	values, cleanup := pool.GetFloat64Slice(1000)
//	defer cleanup()
//	// Use values slice...
func GetFloat64Slice(size int) ([]float64, func()) {
	ptr, _ := float64SlicePool.Get().(*[]float64)
	slice := (*ptr)[:0]

	if cap(slice) < size {
		slice = make([]float64, size)
		*ptr = slice
	} else {
		slice = slice[:size]
		*ptr = slice
	}

	return slice, func() { float64SlicePool.Put(ptr) }
}

// GetStringSlice retrieves and resizes a string slice from the pool.
//
// The returned slice will have the exact length specified by the size parameter.
// If the pooled slice has insufficient capacity, a new slice will be allocated.
// The caller must call the returned cleanup function to return the slice to the pool.
//
// Parameters:
//   - size: The desired length of the slice
//
// Returns:
//   - []string: A slice with length equal to size
//   - func(): Cleanup function that must be called (typically with defer) to return the slice to the pool
//
// Example:
//
//	tags, cleanup := pool.GetStringSlice(1000)
//	defer cleanup()
//	// Use tags slice...
func GetStringSlice(size int) ([]string, func()) {
	ptr, _ := stringSlicePool.Get().(*[]string)
	slice := (*ptr)[:0]

	if cap(slice) < size {
		slice = make([]string, size)
		*ptr = slice
	} else {
		slice = slice[:size]
		*ptr = slice
	}

	return slice, func() { stringSlicePool.Put(ptr) }
}
