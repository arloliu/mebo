package encoding

import (
	"encoding/binary"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

func TestTagEncoder_Write_EmptyTag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	encoder.Write("")

	require.Equal(t, 1, encoder.Len())
	require.Equal(t, 1, encoder.Size())
	require.Equal(t, []byte{0}, encoder.Bytes())
}

func TestTagEncoder_Write_ShortTag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	encoder.Write("ok")

	require.Equal(t, 1, encoder.Len())
	require.Equal(t, 3, encoder.Size()) // 1 byte varint(2) + 2 bytes "ok"

	expected := []byte{2, 'o', 'k'}
	require.Equal(t, expected, encoder.Bytes())
}

func TestTagEncoder_Write_UTF8Tag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	tag := "你好" // 6 bytes in UTF-8
	encoder.Write(tag)

	require.Equal(t, 1, encoder.Len())
	require.Equal(t, 7, encoder.Size()) // 1 byte varint(6) + 6 bytes UTF-8

	// Verify encoding: length + UTF-8 bytes
	data := encoder.Bytes()
	length, n := binary.Uvarint(data)
	require.Equal(t, uint64(6), length)
	require.Equal(t, 1, n)
	require.Equal(t, tag, string(data[n:]))
}

func TestTagEncoder_Write_LongTag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	// Create a 200-byte tag (requires 2-byte varint)
	tag := string(make([]byte, 200))
	for i := range []byte(tag) {
		tag = tag[:i] + "a" + tag[i+1:]
	}

	encoder.Write(tag)

	require.Equal(t, 1, encoder.Len())
	require.Equal(t, 202, encoder.Size()) // 2 bytes varint(200) + 200 bytes

	data := encoder.Bytes()
	length, n := binary.Uvarint(data)
	require.Equal(t, uint64(200), length)
	require.Equal(t, 2, n) // 200 requires 2 bytes varint
	require.Equal(t, tag, string(data[n:]))
}

func TestTagEncoder_Write_MultipleTags(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	tags := []string{"tag1", "tag2", ""}
	for _, tag := range tags {
		encoder.Write(tag)
	}

	require.Equal(t, 3, encoder.Len())

	// Verify all tags can be decoded
	data := encoder.Bytes()
	offset := 0
	for _, expectedTag := range tags {
		length, n := binary.Uvarint(data[offset:])
		offset += n
		actualTag := string(data[offset : offset+int(length)])
		require.Equal(t, expectedTag, actualTag)
		offset += int(length)
	}
}

func TestTagEncoder_WriteSlice_Empty(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	encoder.WriteSlice([]string{})

	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
}

func TestTagEncoder_WriteSlice_SingleTag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	encoder.WriteSlice([]string{"test"})

	require.Equal(t, 1, encoder.Len())
	require.Equal(t, 5, encoder.Size()) // 1 byte varint(4) + 4 bytes "test"

	data := encoder.Bytes()
	length, n := binary.Uvarint(data)
	require.Equal(t, uint64(4), length)
	require.Equal(t, "test", string(data[n:]))
}

func TestTagEncoder_WriteSlice_MultipleTags(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	tags := []string{"tag1", "tag2", "tag3", ""}
	encoder.WriteSlice(tags)

	require.Equal(t, 4, encoder.Len())

	// Verify all tags
	data := encoder.Bytes()
	offset := 0
	for _, expectedTag := range tags {
		length, n := binary.Uvarint(data[offset:])
		offset += n
		actualTag := string(data[offset : offset+int(length)])
		require.Equal(t, expectedTag, actualTag)
		offset += int(length)
	}
}

func TestTagEncoder_WriteSlice_MixedLengths(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	// Mix of short and long tags
	longTag := string(make([]byte, 150))
	for i := range []byte(longTag) {
		longTag = longTag[:i] + "x" + longTag[i+1:]
	}

	tags := []string{"", "a", "hello", longTag}
	encoder.WriteSlice(tags)

	require.Equal(t, 4, encoder.Len())

	// Verify decoding
	data := encoder.Bytes()
	offset := 0
	for _, expectedTag := range tags {
		length, n := binary.Uvarint(data[offset:])
		offset += n
		actualTag := string(data[offset : offset+int(length)])
		require.Equal(t, expectedTag, actualTag)
		offset += int(length)
	}
}

func TestTagEncoder_Reset(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	encoder.Write("tag1")
	encoder.Write("tag2")

	oldBytes := encoder.Bytes()
	oldSize := encoder.Size()
	require.Equal(t, 2, encoder.Len())

	encoder.Reset()

	// After reset, bytes and size remain the same, but count is reset
	require.Equal(t, 0, encoder.Len())
	require.Equal(t, oldSize, encoder.Size())
	require.Equal(t, oldBytes, encoder.Bytes())
}

func TestTagEncoder_Finish(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	encoder.Write("tag1")
	encoder.Write("tag2")

	encoder.Finish()

	// After finish, everything is cleared
	require.Equal(t, 0, encoder.Len())
	require.Equal(t, 0, encoder.Size())
	require.Equal(t, []byte{}, encoder.Bytes())
}

func TestTagEncoder_WriteAfterReset(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	// First round
	encoder.Write("tag1")
	encoder.Reset()

	// Second round - data accumulates
	encoder.Write("tag2")

	require.Equal(t, 1, encoder.Len())

	// Both tags should be in buffer
	data := encoder.Bytes()
	offset := 0

	// First tag
	length, n := binary.Uvarint(data[offset:])
	require.Equal(t, uint64(4), length)
	offset += n
	require.Equal(t, "tag1", string(data[offset:offset+int(length)]))
	offset += int(length)

	// Second tag
	length, n = binary.Uvarint(data[offset:])
	require.Equal(t, uint64(4), length)
	offset += n
	require.Equal(t, "tag2", string(data[offset:offset+int(length)]))
}

func TestTagEncoder_WriteAfterFinish(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	// First session
	encoder.Write("tag1")
	encoder.Finish()

	// Second session - starts fresh
	encoder.Write("tag2")

	require.Equal(t, 1, encoder.Len())

	// Only second tag should be in buffer
	data := encoder.Bytes()
	length, n := binary.Uvarint(data)
	require.Equal(t, uint64(4), length)
	require.Equal(t, "tag2", string(data[n:]))
}

func TestTagEncoder_KeyValueTags(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	tags := []string{
		"severity=high",
		"user_id=12345",
		"region=us-west",
		"",
	}

	encoder.WriteSlice(tags)

	require.Equal(t, 4, encoder.Len())

	// Verify all key=value tags
	data := encoder.Bytes()
	offset := 0
	for _, expectedTag := range tags {
		length, n := binary.Uvarint(data[offset:])
		offset += n
		actualTag := string(data[offset : offset+int(length)])
		require.Equal(t, expectedTag, actualTag)
		offset += int(length)
	}
}

func TestTagEncoder_BufferGrowth(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	// Write many tags to test buffer growth
	for i := 0; i < 100; i++ {
		encoder.Write("test_tag_value")
	}

	require.Equal(t, 100, encoder.Len())

	// Verify first and last tags
	data := encoder.Bytes()

	// First tag
	length, n := binary.Uvarint(data)
	require.Equal(t, uint64(14), length)
	require.Equal(t, "test_tag_value", string(data[n:n+int(length)]))

	// Navigate to last tag
	offset := 0
	for i := 0; i < 99; i++ {
		length, n := binary.Uvarint(data[offset:])
		offset += n + int(length)
	}

	// Last tag
	length, n = binary.Uvarint(data[offset:])
	require.Equal(t, uint64(14), length)
	require.Equal(t, "test_tag_value", string(data[offset+n:offset+n+int(length)]))
}

func TestTagDecoder_All_EmptyData(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTagDecoder(engine)

	tags := make([]string, 0)
	for tag := range decoder.All([]byte{}, 0) {
		tags = append(tags, tag)
	}

	require.Empty(t, tags)
}

func TestTagDecoder_All_SingleTag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	encoder.Write("test")
	data := encoder.Bytes()

	tags := make([]string, 0, 1)
	for tag := range decoder.All(data, 1) {
		tags = append(tags, tag)
	}

	require.Len(t, tags, 1)
	require.Equal(t, "test", tags[0])
}

func TestTagDecoder_All_MultipleTags(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	expected := []string{"app", "service=api", "region=us-west", "env=prod"}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()

	tags := make([]string, 0, len(expected))
	for tag := range decoder.All(data, len(expected)) {
		tags = append(tags, tag)
	}

	require.Equal(t, expected, tags)
}

func TestTagDecoder_All_EmptyTags(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	expected := []string{"", "tag", "", "another", ""}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()

	tags := make([]string, 0, len(expected))
	for tag := range decoder.All(data, len(expected)) {
		tags = append(tags, tag)
	}

	require.Equal(t, expected, tags)
}

func TestTagDecoder_All_UTF8Tags(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	expected := []string{"你好", "世界", "🚀", "emoji=✅"}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()

	tags := make([]string, 0, len(expected))
	for tag := range decoder.All(data, len(expected)) {
		tags = append(tags, tag)
	}

	require.Equal(t, expected, tags)
}

func TestTagDecoder_All_LongTags(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	// Create tags with lengths that require multi-byte varints
	longTag := string(make([]byte, 200))       // Requires 2-byte varint
	veryLongTag := string(make([]byte, 20000)) // Requires 3-byte varint

	expected := []string{"short", longTag, "medium", veryLongTag}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()

	tags := make([]string, 0, len(expected))
	for tag := range decoder.All(data, len(expected)) {
		tags = append(tags, tag)
	}

	require.Len(t, tags, len(expected))
	require.Equal(t, "short", tags[0])
	require.Equal(t, len(longTag), len(tags[1]))
	require.Equal(t, "medium", tags[2])
	require.Equal(t, len(veryLongTag), len(tags[3]))
}

func TestTagDecoder_All_EarlyTermination(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	expected := []string{"first", "second", "third", "fourth"}
	encoder.WriteSlice(expected)
	data := encoder.Bytes()

	// Break after 2 tags
	tags := make([]string, 0, 2)
	count := 0
	for tag := range decoder.All(data, len(expected)) {
		tags = append(tags, tag)
		count++
		if count == 2 {
			break
		}
	}

	require.Len(t, tags, 2)
	require.Equal(t, "first", tags[0])
	require.Equal(t, "second", tags[1])
}

func TestTagDecoder_All_InvalidData_TruncatedLength(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTagDecoder(engine)

	// Invalid varint (incomplete)
	invalidData := []byte{0xFF, 0xFF}

	tags := make([]string, 0)
	for tag := range decoder.All(invalidData, 5) {
		tags = append(tags, tag)
	}

	// Should return empty or stop early
	require.True(t, len(tags) <= 5)
}

func TestTagDecoder_All_InvalidData_TruncatedTag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTagDecoder(engine)

	// Valid varint (10) but not enough data
	invalidData := []byte{10, 'a', 'b', 'c'} // Says 10 bytes but only has 3

	tags := make([]string, 0)
	for tag := range decoder.All(invalidData, 1) {
		tags = append(tags, tag)
	}

	// Should stop without yielding the truncated tag
	require.Empty(t, tags)
}

func TestTagDecoder_All_CountMismatch(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	encoder.WriteSlice([]string{"tag1", "tag2"})
	data := encoder.Bytes()

	// Request more than available
	tags := make([]string, 0, 10)
	for tag := range decoder.All(data, 10) {
		tags = append(tags, tag)
	}

	// Should only return available tags
	require.Len(t, tags, 2)
}

// === TagDecoder.At Tests ===

func TestTagDecoder_At_EmptyData(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTagDecoder(engine)

	tag, ok := decoder.At([]byte{}, 0, 0)
	require.False(t, ok)
	require.Empty(t, tag)
}

func TestTagDecoder_At_FirstTag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	encoder.WriteSlice([]string{"first", "second", "third"})
	data := encoder.Bytes()

	tag, ok := decoder.At(data, 0, 3)
	require.True(t, ok)
	require.Equal(t, "first", tag)
}

func TestTagDecoder_At_MiddleTag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	encoder.WriteSlice([]string{"first", "second", "third"})
	data := encoder.Bytes()

	tag, ok := decoder.At(data, 1, 3)
	require.True(t, ok)
	require.Equal(t, "second", tag)
}

func TestTagDecoder_At_LastTag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	encoder.WriteSlice([]string{"first", "second", "third"})
	data := encoder.Bytes()

	tag, ok := decoder.At(data, 2, 3)
	require.True(t, ok)
	require.Equal(t, "third", tag)
}

func TestTagDecoder_At_OutOfBounds(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	encoder.WriteSlice([]string{"first", "second"})
	data := encoder.Bytes()

	tag, ok := decoder.At(data, 10, 2)
	require.False(t, ok)
	require.Empty(t, tag)
}

func TestTagDecoder_At_NegativeIndex(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	encoder.WriteSlice([]string{"first", "second"})
	data := encoder.Bytes()

	tag, ok := decoder.At(data, -1, 2)
	require.False(t, ok)
	require.Empty(t, tag)
}

func TestTagDecoder_At_EmptyTag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	encoder.WriteSlice([]string{"first", "", "third"})
	data := encoder.Bytes()

	tag, ok := decoder.At(data, 1, 3)
	require.True(t, ok)
	require.Equal(t, "", tag)
}

func TestTagDecoder_At_UTF8Tag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	encoder.WriteSlice([]string{"app", "你好世界", "service"})
	data := encoder.Bytes()

	tag, ok := decoder.At(data, 1, 3)
	require.True(t, ok)
	require.Equal(t, "你好世界", tag)
}

func TestTagDecoder_At_LongTag(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	longTag := string(make([]byte, 1000))
	encoder.WriteSlice([]string{"short", longTag, "another"})
	data := encoder.Bytes()

	tag, ok := decoder.At(data, 1, 3)
	require.True(t, ok)
	require.Equal(t, len(longTag), len(tag))
}

func TestTagDecoder_At_InvalidData(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTagDecoder(engine)

	// Invalid varint
	invalidData := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}

	tag, ok := decoder.At(invalidData, 0, 1)
	require.False(t, ok)
	require.Empty(t, tag)
}

func TestTagDecoder_At_TruncatedData(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTagDecoder(engine)

	// Valid varint but not enough data
	truncatedData := []byte{10, 'a', 'b', 'c'}

	tag, ok := decoder.At(truncatedData, 0, 1)
	require.False(t, ok)
	require.Empty(t, tag)
}

// === Round-trip Tests ===

func TestTagDecoder_RoundTrip_Various(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	decoder := NewTagDecoder(engine)

	testCases := [][]string{
		{},
		{"single"},
		{"", ""},
		{"a", "b", "c"},
		{"app=myapp", "service=api", "region=us-west-1", "env=production"},
		{"你好", "世界", "🚀"},
		{string(make([]byte, 500))},
	}

	for _, tc := range testCases {
		encoder := NewTagEncoder(engine) // Create new encoder for each test case
		encoder.WriteSlice(tc)
		data := encoder.Bytes()

		// Test All()
		tags := make([]string, 0, len(tc))
		for tag := range decoder.All(data, len(tc)) {
			tags = append(tags, tag)
		}
		require.Equal(t, tc, tags, "All() failed")

		// Test At() for each index
		for i, expected := range tc {
			tag, ok := decoder.At(data, i, len(tc))
			require.True(t, ok, "At(%d) returned false", i)
			require.Equal(t, expected, tag, "At(%d) mismatch", i)
		}
	}
}

func TestTagDecoder_RoundTrip_LargeDataset(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	// Create large dataset
	expected := make([]string, 1000)
	for i := range expected {
		expected[i] = "tag_value_" + string(rune('0'+i%10))
	}

	encoder.WriteSlice(expected)
	data := encoder.Bytes()

	// Test All()
	tags := make([]string, 0, len(expected))
	for tag := range decoder.All(data, len(expected)) {
		tags = append(tags, tag)
	}
	require.Equal(t, expected, tags)

	// Test At() for random indices
	testIndices := []int{0, 1, 100, 500, 999}
	for _, idx := range testIndices {
		tag, ok := decoder.At(data, idx, len(expected))
		require.True(t, ok)
		require.Equal(t, expected[idx], tag)
	}
}
