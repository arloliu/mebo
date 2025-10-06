package section

import (
	"bytes"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

func TestNumericIndexEntry_WriteTo(t *testing.T) {
	ie := NewNumericIndexEntry(0x0FEDCBA987654321, 99)
	ie.TimestampOffset = 5000
	ie.ValueOffset = 6000
	engine := endian.GetBigEndianEngine()

	buf := &bytes.Buffer{}
	ie.WriteTo(buf, engine)

	// Should produce same result as Bytes() method
	expected := ie.Bytes(engine)
	require.Equal(t, expected, buf.Bytes())
}

// test WriteToSlice
func TestNumericIndexEntry_WriteToSlice(t *testing.T) {
	ie := NewNumericIndexEntry(0x1122334455667788, 7)
	ie.TimestampOffset = 1234
	ie.ValueOffset = 5678
	engine := endian.GetLittleEndianEngine()

	buf := make([]byte, 0, NumericIndexEntrySize)
	n := ie.WriteToSlice(buf, 0, engine)

	// Should produce same result as Bytes() method
	expected := ie.Bytes(engine)
	require.Equal(t, expected, buf[:n])
}

func TestNumericIndexEntry_WriteToMethods_Consistency(t *testing.T) {
	testCases := []struct {
		name         string
		metricID     uint64
		count        uint16
		timestampOff int
		valueOff     int
		engine       endian.EndianEngine
	}{
		{
			name:         "little-endian basic",
			metricID:     0x123456789ABCDEF0,
			count:        42,
			timestampOff: 1000,
			valueOff:     2000,
			engine:       endian.GetLittleEndianEngine(),
		},
		{
			name:         "big-endian max values",
			metricID:     0xFEDCBA9876543210,
			count:        65535,
			timestampOff: 65535,
			valueOff:     32768,
			engine:       endian.GetBigEndianEngine(),
		},
		{
			name:         "little-endian edge case",
			metricID:     0x1122334455667788,
			count:        7,
			timestampOff: 1234,
			valueOff:     5678,
			engine:       endian.GetLittleEndianEngine(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ie := NewNumericIndexEntry(tc.metricID, tc.count)
			ie.TimestampOffset = tc.timestampOff
			ie.ValueOffset = tc.valueOff

			// All methods should produce identical results
			expected := ie.Bytes(tc.engine)

			// Test WriteTo
			buf2 := &bytes.Buffer{}
			ie.WriteTo(buf2, tc.engine)
			require.Equal(t, expected, buf2.Bytes(), "WriteTo should match Bytes()")

			// Test WriteToSlice
			buf3 := make([]byte, NumericIndexEntrySize)
			nextOffset := ie.WriteToSlice(buf3, 0, tc.engine)
			require.Equal(t, NumericIndexEntrySize, nextOffset)
			require.Equal(t, expected, buf3, "WriteToSlice should match Bytes()")
		})
	}
}

func TestNumericIndexEntry_WriteTo_BufferBehavior(t *testing.T) {
	ie1 := NewNumericIndexEntry(1111, 10)
	ie1.TimestampOffset = 100
	ie1.ValueOffset = 200

	ie2 := NewNumericIndexEntry(2222, 20)
	ie2.TimestampOffset = 300
	ie2.ValueOffset = 400

	engine := endian.GetLittleEndianEngine()

	t.Run("empty buffer", func(t *testing.T) {
		buf := &bytes.Buffer{}
		ie1.WriteTo(buf, engine)

		require.Equal(t, NumericIndexEntrySize, buf.Len())

		// Verify roundtrip parsing
		parsed, err := ParseNumericIndexEntry(buf.Bytes(), engine)
		require.NoError(t, err)
		require.Equal(t, ie1.MetricID, parsed.MetricID)
		require.Equal(t, ie1.Count, parsed.Count)
		require.Equal(t, ie1.TimestampOffset, parsed.TimestampOffset)
		require.Equal(t, ie1.ValueOffset, parsed.ValueOffset)
	})

	t.Run("append to existing data", func(t *testing.T) {
		buf := &bytes.Buffer{}
		buf.WriteString("prefix") // Add existing data

		ie1.WriteTo(buf, engine)
		ie2.WriteTo(buf, engine)

		data := buf.Bytes()
		require.Equal(t, 6+NumericIndexEntrySize*2, len(data)) // prefix + 2 entries
		require.Equal(t, "prefix", string(data[:6]))

		parsed1, err := ParseNumericIndexEntry(data[6:6+NumericIndexEntrySize], engine)
		require.NoError(t, err)

		parsed2, err := ParseNumericIndexEntry(data[6+NumericIndexEntrySize:6+NumericIndexEntrySize*2], engine)
		require.NoError(t, err)

		require.Equal(t, ie1.MetricID, parsed1.MetricID)
		require.Equal(t, ie2.MetricID, parsed2.MetricID)
	})
}

func TestNumericIndexEntry_WriteToSlice_OffsetHandling(t *testing.T) {
	entries := []NumericIndexEntry{
		NewNumericIndexEntry(1111, 10),
		NewNumericIndexEntry(2222, 20),
		NewNumericIndexEntry(3333, 30),
	}

	// Set some test values
	for i := range entries {
		entries[i].TimestampOffset = (i + 1) * 100
		entries[i].ValueOffset = (i + 1) * 200
	}

	engine := endian.GetLittleEndianEngine()
	buf := make([]byte, NumericIndexEntrySize*len(entries))

	// Write all entries sequentially
	offset := 0
	for i := range entries {
		offset = entries[i].WriteToSlice(buf, offset, engine)
	}

	require.Equal(t, len(buf), offset, "Final offset should equal buffer length")

	// Verify each entry can be parsed back correctly
	for i := range entries {
		start := i * NumericIndexEntrySize
		end := start + NumericIndexEntrySize

		parsed, err := ParseNumericIndexEntry(buf[start:end], engine)
		require.NoError(t, err)

		require.Equal(t, entries[i].MetricID, parsed.MetricID)
		require.Equal(t, entries[i].Count, parsed.Count)
		require.Equal(t, entries[i].TimestampOffset, parsed.TimestampOffset)
		require.Equal(t, entries[i].ValueOffset, parsed.ValueOffset)
	}
}
