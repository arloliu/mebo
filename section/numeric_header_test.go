package section

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/stretchr/testify/require"
)

func TestNewNumericHeader(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	header := NewNumericHeader(startTime)

	require.NotNil(t, header)
	require.Equal(t, startTime.UnixMicro(), header.StartTime)
	require.Equal(t, uint32(IndexOffsetOffset), header.IndexOffset)
	require.Equal(t, uint32(0), header.MetricCount)
	require.Equal(t, uint32(0), header.TimestampPayloadOffset)
	require.True(t, header.Flag.IsValidMagicNumber())
	require.True(t, header.Flag.IsLittleEndian())
}

func TestNumericHeader_Parse(t *testing.T) {
	t.Run("Valid header", func(t *testing.T) {
		// Create a valid header
		startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		original := NewNumericHeader(startTime)
		original.MetricCount = 10
		original.TimestampPayloadOffset = 100
		original.ValuePayloadOffset = 200
		original.TagPayloadOffset = 300

		// Serialize to bytes
		data := original.Bytes()

		// Parse back
		parsed := &NumericHeader{}
		err := parsed.Parse(data)

		require.NoError(t, err)
		require.Equal(t, original.StartTime, parsed.StartTime)
		require.Equal(t, original.MetricCount, parsed.MetricCount)
		require.Equal(t, original.IndexOffset, parsed.IndexOffset)
		require.Equal(t, original.TimestampPayloadOffset, parsed.TimestampPayloadOffset)
		require.Equal(t, original.ValuePayloadOffset, parsed.ValuePayloadOffset)
		require.Equal(t, original.TagPayloadOffset, parsed.TagPayloadOffset)
	})

	t.Run("Invalid size", func(t *testing.T) {
		header := &NumericHeader{}
		err := header.Parse([]byte{1, 2, 3}) // Too short

		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidHeaderSize)
	})

	t.Run("Invalid magic number", func(t *testing.T) {
		data := make([]byte, HeaderSize)
		// Set invalid magic number (not 0xEA10)
		data[0] = 0x00
		data[1] = 0x00

		header := &NumericHeader{}
		err := header.Parse(data)

		require.Error(t, err)
	})
}

func TestNumericHeader_Bytes(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	header := NewNumericHeader(startTime)
	header.MetricCount = 42
	header.TimestampPayloadOffset = 1000
	header.ValuePayloadOffset = 2000
	header.TagPayloadOffset = 3000

	data := header.Bytes()

	require.Len(t, data, HeaderSize)

	// Verify we can parse it back
	parsed := &NumericHeader{}
	err := parsed.Parse(data)
	require.NoError(t, err)
	require.Equal(t, header.StartTime, parsed.StartTime)
	require.Equal(t, header.MetricCount, parsed.MetricCount)
}

func TestNumericHeader_StartTimeAsTime(t *testing.T) {
	expectedTime := time.Date(2024, 6, 15, 12, 30, 45, 123456000, time.UTC)
	header := NewNumericHeader(expectedTime)

	result := header.StartTimeAsTime()

	require.Equal(t, expectedTime.Unix(), result.Unix())
	require.Equal(t, expectedTime.UnixMicro(), result.UnixMicro())
}

func TestNumericHeader_Endianness(t *testing.T) {
	t.Run("Little endian", func(t *testing.T) {
		header := NewNumericHeader(time.Now())
		header.Flag.WithLittleEndian()

		engine := header.Flag.GetEndianEngine()
		require.Equal(t, endian.GetLittleEndianEngine(), engine)
	})

	t.Run("Big endian", func(t *testing.T) {
		header := NewNumericHeader(time.Now())
		header.Flag.WithBigEndian()

		engine := header.Flag.GetEndianEngine()
		require.Equal(t, endian.GetBigEndianEngine(), engine)
	})
}

func TestParseNumericHeader(t *testing.T) {
	t.Run("Valid header", func(t *testing.T) {
		// Create and serialize a valid header
		original := NewNumericHeader(time.Now())
		original.MetricCount = 5
		data := original.Bytes()

		// Parse using the function
		parsed, err := ParseNumericHeader(data)

		require.NoError(t, err)
		require.Equal(t, original.StartTime, parsed.StartTime)
		require.Equal(t, original.MetricCount, parsed.MetricCount)
	})

	t.Run("Too short", func(t *testing.T) {
		data := make([]byte, HeaderSize-1)

		_, err := ParseNumericHeader(data)

		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidHeaderSize)
	})

	t.Run("Extra data ignored", func(t *testing.T) {
		// Create valid header with extra bytes
		original := NewNumericHeader(time.Now())
		data := append(original.Bytes(), []byte{1, 2, 3, 4, 5}...)

		parsed, err := ParseNumericHeader(data)

		require.NoError(t, err)
		require.Equal(t, original.StartTime, parsed.StartTime)
	})
}

func TestIsNumericBlob(t *testing.T) {
	t.Run("Valid numeric blob", func(t *testing.T) {
		header := NewNumericHeader(time.Now())
		data := header.Bytes()

		result := IsNumericBlob(data)

		require.True(t, result)
	})

	t.Run("Text blob (wrong magic)", func(t *testing.T) {
		// Create data with text blob magic number (0xEB10)
		data := make([]byte, HeaderSize)
		data[0] = 0x10
		data[1] = 0xEB

		result := IsNumericBlob(data)

		require.False(t, result)
	})

	t.Run("Invalid magic number", func(t *testing.T) {
		data := make([]byte, HeaderSize)
		data[0] = 0xFF
		data[1] = 0xFF

		result := IsNumericBlob(data)

		require.False(t, result)
	})

	t.Run("Too short", func(t *testing.T) {
		data := make([]byte, HeaderSize-1)

		result := IsNumericBlob(data)

		require.False(t, result)
	})

	t.Run("Empty data", func(t *testing.T) {
		result := IsNumericBlob([]byte{})

		require.False(t, result)
	})

	t.Run("Nil data", func(t *testing.T) {
		result := IsNumericBlob(nil)

		require.False(t, result)
	})

	t.Run("With extra data", func(t *testing.T) {
		header := NewNumericHeader(time.Now())
		data := append(header.Bytes(), []byte{1, 2, 3, 4, 5}...)

		result := IsNumericBlob(data)

		require.True(t, result)
	})
}

func TestNumericHeader_RoundTrip(t *testing.T) {
	// Create header with all fields set
	startTime := time.Date(2024, 12, 25, 10, 30, 45, 0, time.UTC)
	original := NewNumericHeader(startTime)
	original.MetricCount = 100
	original.IndexOffset = 32
	original.TimestampPayloadOffset = 1000
	original.ValuePayloadOffset = 2000
	original.TagPayloadOffset = 3000
	original.Flag.WithTag()
	original.Flag.SetHasMetricNames(true)
	original.Flag.WithBigEndian()

	// Serialize
	data := original.Bytes()

	// Parse back
	parsed, err := ParseNumericHeader(data)
	require.NoError(t, err)

	// Verify all fields
	require.Equal(t, original.StartTime, parsed.StartTime)
	require.Equal(t, original.MetricCount, parsed.MetricCount)
	require.Equal(t, original.IndexOffset, parsed.IndexOffset)
	require.Equal(t, original.TimestampPayloadOffset, parsed.TimestampPayloadOffset)
	require.Equal(t, original.ValuePayloadOffset, parsed.ValuePayloadOffset)
	require.Equal(t, original.TagPayloadOffset, parsed.TagPayloadOffset)
	require.Equal(t, original.Flag.HasTag(), parsed.Flag.HasTag())
	require.Equal(t, original.Flag.HasMetricNames(), parsed.Flag.HasMetricNames())
	require.Equal(t, original.Flag.IsBigEndian(), parsed.Flag.IsBigEndian())
}
