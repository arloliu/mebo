package encoding

import "iter"

type ColumnarEncoder[T comparable] interface {
	// Bytes returns the encoded byte slice.
	// The returned slice is valid until the next call to Write, WriteSlice, or Reset.
	// The caller should not modify the returned slice.
	//
	// The Reset() method does not clear the internal buffer, allowing it to be reused for a new sequence of timestamps
	// until the end of the encoding process.
	Bytes() []byte

	// Len returns the number of encoded timestamps.
	//
	// The Reset() method does not clear the internal buffer, allowing it to be reused for a new sequence of timestamps
	// until the end of the encoding process.
	Len() int

	// Size returns the size in bytes of encoded timestamps.
	// It represents the number of bytes that were written to the internal buffer.
	//
	// The Reset() method does not clear the internal buffer, allowing it to be reused for a new sequence of timestamps
	// until the end of the encoding process.
	Size() int

	// Reset clears the internal encoder state but keeps the accumulated data in the internal buffer,
	// allowing it to be reused for a new sequence of timestamps until the end of the encoding process.
	//
	// The Len(), Size() and Bytes() remain unchanged, the caller will retrieve the accumulated data
	// information using Len(), Size() and Bytes().
	Reset()

	// Finish finalizes the encoding process and returns buffer resources to the pool.
	//
	// After calling Finish(), the encoder is no longer usable. Any subsequent calls to
	// Write(), WriteSlice(), Bytes(), Len(), or Size() will result in a panic due to nil buffer.
	//
	// To encode more data, create a new encoder instance.
	//
	// This method must be called when the encoding session is complete to ensure buffer
	// resources are properly returned to the pool for reuse by other encoders. Use defer
	// to ensure it's called even in error paths:
	//
	//	encoder := NewTimestampRawEncoder(engine)
	//	defer encoder.Finish()  // Ensure buffer is returned to pool
	//
	//	encoder.Write(timestamp1)
	//	data := encoder.Bytes()  // Get data before Finish
	//	// Finish() called automatically via defer
	Finish()

	// Write a single value.
	//
	// This method is optimized for appending a single value.
	// For bulk writes, use WriteSlice for better performance.
	Write(data T)

	// WriteSlice encodes a slice of values.
	//
	// This method is optimized for bulk writes. For single writes, use Write for better performance.
	WriteSlice(values []T)
}

type ColumnarDecoder[T comparable] interface {
	// All returns a iterator that yields all decoded items from the provided encoded data.
	//
	// The data should be the byte slice payload produced by a corresponding encoder.
	// The count parameter specifies the expected number of values to decode.
	//
	// The method returns an iterator that yields each item in sequence.
	// The iterator will yield exactly 'count' values if the data is valid.
	//
	// If the data is malformed or does not contain enough values, the iterator
	// may yield fewer values. The caller should handle this case appropriately.
	All(data []byte, count int) iter.Seq[T]

	// At retrieves the value at the specified index from the encoded data.
	//
	// The data should be the byte slice payload produced by a corresponding encoder.
	// The index is zero-based, so index 0 retrieves the first value.
	// The count parameter specifies the total number of values encoded in the data,
	// enabling proper bounds checking.
	//
	// If the index is out of bounds (index < 0 or index >= count), the second return
	// value will be false.
	At(data []byte, index int, count int) (T, bool)
}
