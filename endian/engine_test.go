package endian

import (
	"encoding/binary"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func TestCheckEndianness(t *testing.T) {
	require := require.New(t)

	result := CheckEndianness()

	// Verify the result matches the actual system endianness
	var testValue uint16 = 0x0102
	testBytes := (*[2]byte)(unsafe.Pointer(&testValue))

	switch testBytes[0] {
	case 0x01:
		// Big-endian system
		require.Equal(binary.BigEndian, result, "CheckEndianness() should return BigEndian")
	case 0x02:
		// Little-endian system
		require.Equal(binary.LittleEndian, result, "CheckEndianness() should return LittleEndian")
	default:
		require.Failf("Unexpected byte value", "got: %v", testBytes[0])
	}
}

func TestCheckEndiannessConsistency(t *testing.T) {
	// Run multiple times to ensure consistency
	first := CheckEndianness()
	for i := range 100 {
		result := CheckEndianness()
		if result != first {
			t.Errorf("CheckEndianness() returned inconsistent results: first=%v, iteration %d=%v", first, i, result)
		}
	}
}

func TestCheckEndiannessReturnType(t *testing.T) {
	result := CheckEndianness()

	// Verify it returns one of the two valid ByteOrder implementations
	switch result {
	case binary.BigEndian, binary.LittleEndian:
		// Valid result
	default:
		t.Errorf("CheckEndianness() returned unexpected ByteOrder: %v", result)
	}
}

func TestIsNativeLittleEndian(t *testing.T) {
	result := IsNativeLittleEndian()
	expected := CheckEndianness() == binary.LittleEndian
	require.Equal(t, expected, result)

	// Should be consistent across multiple calls
	for range 10 {
		require.Equal(t, result, IsNativeLittleEndian())
	}
}

func TestIsNativeBigEndian(t *testing.T) {
	result := IsNativeBigEndian()
	expected := CheckEndianness() == binary.BigEndian
	require.Equal(t, expected, result)

	// Should be consistent across multiple calls
	for range 10 {
		require.Equal(t, result, IsNativeBigEndian())
	}
}

func TestIsNativeEndiannessInverse(t *testing.T) {
	// IsNativeLittleEndian and IsNativeBigEndian should be inverses
	littleEndian := IsNativeLittleEndian()
	bigEndian := IsNativeBigEndian()

	require.NotEqual(t, littleEndian, bigEndian, "IsNativeLittleEndian and IsNativeBigEndian should return opposite values")
	require.True(t, littleEndian || bigEndian, "At least one endianness check should be true")
}

func TestCompareNativeEndian(t *testing.T) {
	if IsNativeLittleEndian() {
		require.True(t, CompareNativeEndian(GetLittleEndianEngine()))
		require.False(t, CompareNativeEndian(GetBigEndianEngine()))
	} else {
		require.False(t, CompareNativeEndian(GetLittleEndianEngine()))
		require.True(t, CompareNativeEndian(GetBigEndianEngine()))
	}
}

func TestGetLittleEndianEngine(t *testing.T) {
	engine := GetLittleEndianEngine()

	// Should implement EndianEngine interface
	require.Implements(t, (*EndianEngine)(nil), engine)

	// Should be binary.LittleEndian
	require.Equal(t, binary.LittleEndian, engine)

	// Test actual endian behavior
	var testValue uint16 = 0x0102
	bytes := make([]byte, 2)
	engine.PutUint16(bytes, testValue)
	// Little endian should put LSB first
	require.Equal(t, byte(0x02), bytes[0], "Little endian should put LSB first")
	require.Equal(t, byte(0x01), bytes[1], "Little endian should put MSB second")

	// Test reading back
	readValue := engine.Uint16(bytes)
	require.Equal(t, testValue, readValue)
}

func TestGetBigEndianEngine(t *testing.T) {
	engine := GetBigEndianEngine()

	// Should implement EndianEngine interface
	require.Implements(t, (*EndianEngine)(nil), engine)

	// Should be binary.BigEndian
	require.Equal(t, binary.BigEndian, engine)

	// Test actual endian behavior
	var testValue uint16 = 0x0102
	bytes := make([]byte, 2)
	engine.PutUint16(bytes, testValue)
	// Big endian should put MSB first
	require.Equal(t, byte(0x01), bytes[0], "Big endian should put MSB first")
	require.Equal(t, byte(0x02), bytes[1], "Big endian should put LSB second")

	// Test reading back
	readValue := engine.Uint16(bytes)
	require.Equal(t, testValue, readValue)
}

func TestEndianEngines(t *testing.T) {
	// Test that both engines work correctly with different data types
	littleEngine := GetLittleEndianEngine()
	bigEngine := GetBigEndianEngine()

	// Test uint32
	var testUint32 uint32 = 0x01020304
	littleBytes := make([]byte, 4)
	bigBytes := make([]byte, 4)

	littleEngine.PutUint32(littleBytes, testUint32)
	bigEngine.PutUint32(bigBytes, testUint32)

	// Bytes should be different (unless on a weird architecture)
	require.NotEqual(t, littleBytes, bigBytes, "Little and big endian byte representations should differ")

	// But should read back to same value
	require.Equal(t, testUint32, littleEngine.Uint32(littleBytes))
	require.Equal(t, testUint32, bigEngine.Uint32(bigBytes))

	// Test uint64
	var testUint64 uint64 = 0x0102030405060708
	littleBytes64 := make([]byte, 8)
	bigBytes64 := make([]byte, 8)

	littleEngine.PutUint64(littleBytes64, testUint64)
	bigEngine.PutUint64(bigBytes64, testUint64)

	require.NotEqual(t, littleBytes64, bigBytes64)
	require.Equal(t, testUint64, littleEngine.Uint64(littleBytes64))
	require.Equal(t, testUint64, bigEngine.Uint64(bigBytes64))
}
