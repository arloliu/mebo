package encoding

import (
	"strings"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

func TestVarStringEncoder_Write(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewVarStringEncoder(engine)
	defer encoder.Reset()

	// Test empty string
	err := encoder.Write("")
	require.NoError(t, err)
	require.Equal(t, 1, encoder.Len())
	require.Equal(t, 1, encoder.Size()) // 1 byte for length (0)

	// Test short string (create new encoder)
	encoder2 := NewVarStringEncoder(engine)
	defer encoder2.Reset()
	err = encoder2.Write("hello")
	require.NoError(t, err)
	require.Equal(t, 1, encoder2.Len())
	require.Equal(t, 6, encoder2.Size()) // 1 byte length + 5 bytes data

	// Verify encoding
	bytes := encoder2.Bytes()
	require.Equal(t, byte(5), bytes[0])          // Length
	require.Equal(t, "hello", string(bytes[1:])) // Data
}

func TestVarStringEncoder_Write_MaxLength(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewVarStringEncoder(engine)
	defer encoder.Reset()

	// Test maximum length string (255 chars)
	maxStr := strings.Repeat("a", MaxTextLength)
	err := encoder.Write(maxStr)
	require.NoError(t, err)
	require.Equal(t, 1, encoder.Len())
	require.Equal(t, 256, encoder.Size()) // 1 byte length + 255 bytes data

	// Verify encoding
	bytes := encoder.Bytes()
	require.Equal(t, byte(MaxTextLength), bytes[0])
	require.Equal(t, maxStr, string(bytes[1:]))
}

func TestVarStringEncoder_Write_ExceedsMaxLength(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewVarStringEncoder(engine)
	defer encoder.Reset()

	// Test string exceeding maximum length
	tooLong := strings.Repeat("a", MaxTextLength+1)
	err := encoder.Write(tooLong)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
	require.Equal(t, 0, encoder.Len()) // Should not increment count on error
}

func TestVarStringEncoder_WriteSlice(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewVarStringEncoder(engine)
	defer encoder.Reset()

	texts := []string{"hello", "world", "test"}
	err := encoder.WriteSlice(texts)
	require.NoError(t, err)
	require.Equal(t, 3, encoder.Len())

	// Expected size: (1+5) + (1+5) + (1+4) = 17 bytes
	require.Equal(t, 17, encoder.Size())

	// Verify encoding
	bytes := encoder.Bytes()
	offset := 0

	// First string: "hello"
	require.Equal(t, byte(5), bytes[offset])
	require.Equal(t, "hello", string(bytes[offset+1:offset+6]))
	offset += 6

	// Second string: "world"
	require.Equal(t, byte(5), bytes[offset])
	require.Equal(t, "world", string(bytes[offset+1:offset+6]))
	offset += 6

	// Third string: "test"
	require.Equal(t, byte(4), bytes[offset])
	require.Equal(t, "test", string(bytes[offset+1:offset+5]))
}

func TestVarStringEncoder_WriteSlice_WithInvalidString(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewVarStringEncoder(engine)
	defer encoder.Reset()

	// Include one string that exceeds max length
	texts := []string{
		"hello",
		strings.Repeat("a", MaxTextLength+1), // Too long
		"world",
	}

	err := encoder.WriteSlice(texts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
	require.Equal(t, 0, encoder.Len()) // Should not encode anything on error
}

func TestVarStringEncoder_WriteSlice_Empty(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewVarStringEncoder(engine)
	defer encoder.Reset()

	err := encoder.WriteSlice([]string{})
	require.NoError(t, err)
	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
}

func TestVarStringEncoder_WriteVarint(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Test positive values
	testCases := []struct {
		name     string
		value    int64
		expected []byte
	}{
		{"zero", 0, []byte{0x00}},
		{"small positive", 1, []byte{0x02}},          // Zigzag: 1 -> 2
		{"small negative", -1, []byte{0x01}},         // Zigzag: -1 -> 1
		{"medium positive", 127, []byte{0xFE, 0x01}}, // Zigzag: 127 -> 254
		{"medium negative", -64, []byte{0x7F}},       // Zigzag: -64 -> 127
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoder := NewVarStringEncoder(engine)
			defer encoder.Reset()

			encoder.WriteVarint(tc.value)

			bytes := encoder.Bytes()
			require.Equal(t, tc.expected, bytes, "varint encoding mismatch")
		})
	}
}

func TestVarStringEncoder_MultipleWrites(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewVarStringEncoder(engine)
	defer encoder.Reset()

	// Write multiple strings
	err := encoder.Write("first")
	require.NoError(t, err)

	err = encoder.Write("second")
	require.NoError(t, err)

	err = encoder.Write("third")
	require.NoError(t, err)

	require.Equal(t, 3, encoder.Len())

	// Expected size: (1+5) + (1+6) + (1+5) = 19 bytes
	require.Equal(t, 19, encoder.Size())

	// Verify all strings are encoded correctly
	bytes := encoder.Bytes()
	offset := 0

	// First: "first"
	require.Equal(t, byte(5), bytes[offset])
	require.Equal(t, "first", string(bytes[offset+1:offset+6]))
	offset += 6

	// Second: "second"
	require.Equal(t, byte(6), bytes[offset])
	require.Equal(t, "second", string(bytes[offset+1:offset+7]))
	offset += 7

	// Third: "third"
	require.Equal(t, byte(5), bytes[offset])
	require.Equal(t, "third", string(bytes[offset+1:offset+6]))
}

func TestVarStringEncoder_UTF8(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Test UTF-8 strings
	utf8Strings := []string{
		"Hello, 世界",
		"Привет",
		"🚀",
		"emoji test 😀👍",
	}

	for _, str := range utf8Strings {
		encoder := NewVarStringEncoder(engine)
		err := encoder.Write(str)
		require.NoError(t, err)

		bytes := encoder.Bytes()
		length := bytes[0]
		decoded := string(bytes[1:])

		require.Equal(t, len(str), int(length))
		require.Equal(t, str, decoded)

		encoder.Reset()
	}
}

func TestVarStringEncoder_BoundaryCase_255Chars(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewVarStringEncoder(engine)
	defer encoder.Reset()

	// Test exactly 255 characters (max valid length)
	str255 := strings.Repeat("x", 255)
	err := encoder.Write(str255)
	require.NoError(t, err)

	bytes := encoder.Bytes()
	require.Equal(t, byte(255), bytes[0])
	require.Equal(t, 256, len(bytes)) // 1 + 255
}

func TestVarStringEncoder_BoundaryCase_256Chars(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewVarStringEncoder(engine)
	defer encoder.Reset()

	// Test 256 characters (should fail - exceeds max)
	str256 := strings.Repeat("x", 256)
	err := encoder.Write(str256)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
}
