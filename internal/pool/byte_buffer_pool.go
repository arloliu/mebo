package pool

import (
	"io"
	"sync"
)

// BlobBufferDefaultSize is the default size of the ByteBuffer obtained from the pool.
const (
	BlobBufferDefaultSize     = 1024 * 16       // 16KiB
	BlobBufferMaxThreshold    = 1024 * 128      // 128KiB
	BlobSetBufferDefaultSize  = 1024 * 1024     // 1MiB
	BlobSetBufferMaxThreshold = 1024 * 1024 * 8 // 8MiB
)

type ByteBuffer struct {
	// B is the underlying byte slice.
	B []byte
}

// NewByteBuffer creates a new ByteBuffer with the specified default size.
func NewByteBuffer(defaultSize int) *ByteBuffer {
	return &ByteBuffer{
		B: make([]byte, 0, defaultSize),
	}
}

// Bytes() returns the underlying byte slice.
func (bb *ByteBuffer) Bytes() []byte {
	return bb.B
}

// Reset resets the buffer to be empty, but retains the allocated memory for reuse.
func (bb *ByteBuffer) Reset() {
	bb.B = bb.B[:0]
}

// Len returns the length of the buffer.
func (bb *ByteBuffer) Len() int {
	return len(bb.B)
}

// Cap returns the capacity of the buffer.
func (bb *ByteBuffer) Cap() int {
	return cap(bb.B)
}

// MustWrite writes data to the buffer, growing it if necessary.
func (bb *ByteBuffer) MustWrite(data []byte) {
	bb.B = append(bb.B, data...)
}

// Slice returns a slice of the buffer from start to end.
// Panics if the indices are out of bounds.
func (bb *ByteBuffer) Slice(start, end int) []byte {
	if start < 0 || end < start || end > cap(bb.B) {
		panic("Slice: invalid indices")
	}

	return bb.B[start:end]
}

// SetLength sets the length of the buffer to n.
// Panics if n is negative or greater than the capacity.
func (bb *ByteBuffer) SetLength(n int) {
	if n < 0 || n > cap(bb.B) {
		panic("SetLength: invalid length")
	}
	bb.B = bb.B[:n]
}

// Extend extends the buffer by n bytes if there is sufficient capacity.
func (bb *ByteBuffer) Extend(n int) bool {
	curLen := len(bb.B)
	if cap(bb.B)-curLen < n {
		return false
	}

	bb.B = bb.B[:curLen+n]

	return true
}

// ExtendOrGrow extends the buffer by n bytes, growing it if necessary.
func (bb *ByteBuffer) ExtendOrGrow(n int) {
	if bb.Extend(n) {
		return
	}

	start := len(bb.B)
	bb.Grow(n)
	bb.B = bb.B[:start+n]
}

// Grow grows the buffer to ensure it can hold requiredBytes more bytes without reallocating.
// If the buffer has sufficient capacity, Grow does nothing.
//
// The growth strategy is as follows:
//   - For small buffers (<32KB), grow by BlobBufferDefaultSize to minimize reallocations.
//   - For larger buffers, grow by 25% of current capacity to balance memory usage and reallocation cost.
func (bb *ByteBuffer) Grow(requiredBytes int) {
	available := cap(bb.B) - len(bb.B)
	if available >= requiredBytes {
		return // Sufficient capacity
	}

	// Calculate growth size based on current buffer size
	growBy := BlobBufferDefaultSize
	if cap(bb.B) > 4*BlobBufferDefaultSize {
		// For larger buffers, grow by 25% to balance memory and reallocation cost
		growBy = cap(bb.B) / 4
	}

	// Ensure we grow enough for at least the required bytes
	if growBy < requiredBytes {
		growBy = requiredBytes
	}

	// Allocate new buffer with increased capacity
	newBuf := make([]byte, len(bb.B), len(bb.B)+growBy)
	copy(newBuf, bb.B)
	bb.B = newBuf
}

// Write appends the contents of data to the buffer, growing it as needed.
func (bb *ByteBuffer) Write(data []byte) (int, error) {
	bb.B = append(bb.B, data...)
	return len(data), nil
}

// WriteTo writes the contents of the buffer to w.
func (bb *ByteBuffer) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(bb.B)
	return int64(n), err
}

// ByteBufferPool is a pool of ByteBuffers to minimize allocations.
//
// It uses sync.Pool internally to manage the buffers.
// The pool can be configured with a maximum size threshold to avoid retaining
// overly large buffers that could lead to memory bloat.
type ByteBufferPool struct {
	pool         sync.Pool
	maxThreshold int // Optional maximum size threshold for buffers
}

// NewByteBufferPool creates a new ByteBufferPool with buffers of the specified default size.
func NewByteBufferPool(defaultSize int, maxThreshold int) *ByteBufferPool {
	return &ByteBufferPool{
		pool: sync.Pool{
			New: func() any {
				return NewByteBuffer(defaultSize)
			},
		},
		maxThreshold: maxThreshold,
	}
}

// Get retrieves a ByteBuffer from the pool.
func (bbp *ByteBufferPool) Get() *ByteBuffer {
	bb, _ := bbp.pool.Get().(*ByteBuffer)
	return bb
}

// Put returns a ByteBuffer to the pool for reuse.
func (bbp *ByteBufferPool) Put(bb *ByteBuffer) {
	if bb == nil {
		return
	}

	if bbp.maxThreshold > 0 && cap(bb.B) > bbp.maxThreshold {
		// Discard overly large buffers to prevent memory bloat
		return
	}

	bb.Reset()
	bbp.pool.Put(bb)
}

var (
	blobDefaultPool    = NewByteBufferPool(BlobBufferDefaultSize, BlobBufferMaxThreshold)
	blobSetDefaultPool = NewByteBufferPool(BlobSetBufferDefaultSize, BlobSetBufferMaxThreshold)
)

// GetBlobBuffer retrieves a ByteBuffer from the default blob pool.
func GetBlobBuffer() *ByteBuffer {
	return blobDefaultPool.Get()
}

// PutBlobBuffer returns a ByteBuffer to the default blob pool.
func PutBlobBuffer(bb *ByteBuffer) {
	blobDefaultPool.Put(bb)
}

// GetBlobSetBuffer retrieves a ByteBuffer from the default blob set pool.
func GetBlobSetBuffer() *ByteBuffer {
	return blobSetDefaultPool.Get()
}

// PutBlobSetBuffer returns a ByteBuffer to the default blob set pool.
func PutBlobSetBuffer(bb *ByteBuffer) {
	blobSetDefaultPool.Put(bb)
}
