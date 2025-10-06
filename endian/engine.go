// Package endian provides byte order utilities for binary encoding and decoding.
//
// This package extends Go's standard encoding/binary package by combining
// ByteOrder and AppendByteOrder interfaces into a unified EndianEngine interface.
// This enables cleaner API design and improved performance for binary data operations.
//
// # Basic Usage
//
// Most users should use GetLittleEndianEngine() as it's the standard for mebo:
//
//	import "github.com/arloliu/mebo/endian"
//
//	engine := endian.GetLittleEndianEngine()
//	encoder := encoding.NewNumericRawEncoder(engine)
//
// For interoperability with big-endian systems:
//
//	engine := endian.GetBigEndianEngine()
//	encoder := encoding.NewNumericRawEncoder(engine)
//
// # Performance
//
// Using EndianEngine (which includes AppendByteOrder) provides approximately 30%
// better performance for appending operations compared to ByteOrder alone:
//
//	// Using EndianEngine (recommended)
//	buf = engine.AppendUint64(buf, value)  // ~30% faster
//
//	// Using ByteOrder only
//	tmp := make([]byte, 8)
//	engine.PutUint64(tmp, value)
//	buf = append(buf, tmp...)  // Slower, extra allocation
//
// # Thread Safety
//
// All functions and methods in this package are safe for concurrent use.
// The returned EndianEngine instances are immutable and stateless.
package endian

import (
	"encoding/binary"
	"unsafe"
)

// EndianEngine combines ByteOrder and AppendByteOrder interfaces from encoding/binary
// into a single interface for convenient byte order operations.
//
// This interface is satisfied by binary.LittleEndian and binary.BigEndian from
// the standard library, making it fully compatible with existing Go code while
// providing access to both read/write and append operations.
type EndianEngine interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

// CheckEndianness uses a fixed integer value to determine the host's byte order.
func CheckEndianness() binary.ByteOrder {
	// 0x0100 is 256. For a little-endian system, the LSB (0x00) is first.
	// For a big-endian system, the MSB (0x01) is first.
	var i uint16 = 0x0100

	// Create a byte slice pointing to the memory address of 'i'.
	// We only need the first byte.
	b := (*[2]byte)(unsafe.Pointer(&i))

	// Check the first byte at the lowest memory address
	if b[0] == 0x01 {
		return binary.BigEndian
	}

	return binary.LittleEndian
}

func IsNativeLittleEndian() bool {
	return CheckEndianness() == binary.LittleEndian
}

func IsNativeBigEndian() bool {
	return CheckEndianness() == binary.BigEndian
}

func CompareNativeEndian(engine EndianEngine) bool {
	return engine == CheckEndianness()
}

// GetLittleEndianEngine returns the little-endian engine.
func GetLittleEndianEngine() EndianEngine {
	return binary.LittleEndian
}

// GetBigEndianEngine returns the big-endian engine.
func GetBigEndianEngine() EndianEngine {
	return binary.BigEndian
}
