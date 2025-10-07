package section

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/stretchr/testify/require"
)

func TestNewTextHeader(t *testing.T) {
	t.Run("Valid metric count", func(t *testing.T) {
		startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		metricCount := 100

		header, err := NewTextHeader(startTime, metricCount)

		require.NoError(t, err)
		require.NotNil(t, header)
		require.Equal(t, startTime.UnixMicro(), header.StartTime)
		require.Equal(t, uint32(metricCount), header.MetricCount)
		require.Equal(t, uint32(IndexOffsetOffset), header.IndexOffset)
		require.True(t, header.Flag.IsLittleEndian())
	})

	t.Run("Metric count at boundary (65535)", func(t *testing.T) {
		startTime := time.Now()
		metricCount := 65535

		header, err := NewTextHeader(startTime, metricCount)

		require.NoError(t, err)
		require.Equal(t, uint32(65535), header.MetricCount)
	})

	t.Run("Negative metric count", func(t *testing.T) {
		startTime := time.Now()

		header, err := NewTextHeader(startTime, -1)

		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidMetricCount)
		require.Nil(t, header)
	})

	t.Run("Metric count exceeds max (65536)", func(t *testing.T) {
		startTime := time.Now()

		header, err := NewTextHeader(startTime, 65536)

		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidMetricCount)
		require.Nil(t, header)
	})

	t.Run("Zero metric count", func(t *testing.T) {
		startTime := time.Now()

		header, err := NewTextHeader(startTime, 0)

		require.NoError(t, err)
		require.Equal(t, uint32(0), header.MetricCount)
	})
}

func TestTextHeader_Parse(t *testing.T) {
	t.Run("Valid header", func(t *testing.T) {
		// Create a valid header
		startTime := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
		original, err := NewTextHeader(startTime, 50)
		require.NoError(t, err)
		original.DataOffset = 500
		original.DataSize = 10000

		// Serialize to bytes
		data := original.Bytes()

		// Parse back
		parsed := &TextHeader{}
		err = parsed.Parse(data)

		require.NoError(t, err)
		require.Equal(t, original.StartTime, parsed.StartTime)
		require.Equal(t, original.MetricCount, parsed.MetricCount)
		require.Equal(t, original.IndexOffset, parsed.IndexOffset)
		require.Equal(t, original.DataOffset, parsed.DataOffset)
		require.Equal(t, original.DataSize, parsed.DataSize)
	})

	t.Run("Invalid size", func(t *testing.T) {
		header := &TextHeader{}
		err := header.Parse([]byte{1, 2, 3}) // Too short

		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidHeaderSize)
	})

	t.Run("Invalid flags", func(t *testing.T) {
		data := make([]byte, HeaderSize)
		// Set invalid magic number (not 0xEB10)
		data[0] = 0x00
		data[1] = 0x00

		header := &TextHeader{}
		err := header.Parse(data)

		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidMagicNumber)
	})
}

func TestTextHeader_Bytes(t *testing.T) {
	startTime := time.Date(2024, 3, 15, 8, 45, 30, 0, time.UTC)
	header, err := NewTextHeader(startTime, 25)
	require.NoError(t, err)

	header.DataOffset = 1500
	header.DataSize = 50000

	data := header.Bytes()

	require.Len(t, data, HeaderSize)

	// Verify we can parse it back
	parsed := &TextHeader{}
	err = parsed.Parse(data)
	require.NoError(t, err)
	require.Equal(t, header.StartTime, parsed.StartTime)
	require.Equal(t, header.MetricCount, parsed.MetricCount)
	require.Equal(t, header.DataOffset, parsed.DataOffset)
	require.Equal(t, header.DataSize, parsed.DataSize)
}

func TestTextHeader_StartTimeAsTime(t *testing.T) {
	expectedTime := time.Date(2024, 11, 20, 15, 22, 10, 987654000, time.UTC)
	header, err := NewTextHeader(expectedTime, 10)
	require.NoError(t, err)

	result := header.StartTimeAsTime()

	require.Equal(t, expectedTime.Unix(), result.Unix())
	require.Equal(t, expectedTime.UnixMicro(), result.UnixMicro())
}

func TestTextHeader_GetEndianEngine(t *testing.T) {
	t.Run("Little endian", func(t *testing.T) {
		header, err := NewTextHeader(time.Now(), 10)
		require.NoError(t, err)
		header.Flag.WithLittleEndian()

		engine := header.GetEndianEngine()

		require.Equal(t, endian.GetLittleEndianEngine(), engine)
	})

	t.Run("Big endian", func(t *testing.T) {
		header, err := NewTextHeader(time.Now(), 10)
		require.NoError(t, err)
		header.Flag.WithBigEndian()

		engine := header.GetEndianEngine()

		require.Equal(t, endian.GetBigEndianEngine(), engine)
	})
}

func TestTextHeader_IsValidFlags(t *testing.T) {
	t.Run("Valid flags", func(t *testing.T) {
		header, err := NewTextHeader(time.Now(), 10)
		require.NoError(t, err)

		result := header.IsValidFlags()

		require.True(t, result)
	})

	t.Run("Invalid flags", func(t *testing.T) {
		header := &TextHeader{}
		// Don't set any flags - should be invalid
		header.Flag.Options = 0x0000

		result := header.IsValidFlags()

		require.False(t, result)
	})
}

func TestParseTextHeader(t *testing.T) {
	t.Run("Valid header", func(t *testing.T) {
		// Create and serialize a valid header
		original, err := NewTextHeader(time.Now(), 15)
		require.NoError(t, err)
		original.DataOffset = 800
		data := original.Bytes()

		// Parse using the function
		parsed, err := ParseTextHeader(data)

		require.NoError(t, err)
		require.Equal(t, original.StartTime, parsed.StartTime)
		require.Equal(t, original.MetricCount, parsed.MetricCount)
		require.Equal(t, original.DataOffset, parsed.DataOffset)
	})

	t.Run("Too short", func(t *testing.T) {
		data := make([]byte, HeaderSize-1)

		_, err := ParseTextHeader(data)

		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidHeaderSize)
	})

	t.Run("Extra data ignored", func(t *testing.T) {
		// Create valid header with extra bytes
		original, err := NewTextHeader(time.Now(), 5)
		require.NoError(t, err)
		data := append(original.Bytes(), []byte{1, 2, 3, 4, 5}...)

		parsed, err := ParseTextHeader(data)

		require.NoError(t, err)
		require.Equal(t, original.StartTime, parsed.StartTime)
	})

	t.Run("Invalid flags", func(t *testing.T) {
		data := make([]byte, HeaderSize)
		// Set invalid magic number
		data[0] = 0xFF
		data[1] = 0xFF

		_, err := ParseTextHeader(data)

		require.Error(t, err)
	})
}

func TestIsTextBlob(t *testing.T) {
	t.Run("Valid text blob", func(t *testing.T) {
		header, err := NewTextHeader(time.Now(), 10)
		require.NoError(t, err)
		data := header.Bytes()

		result := IsTextBlob(data)

		require.True(t, result)
	})

	t.Run("Numeric blob (wrong magic)", func(t *testing.T) {
		// Create data with numeric blob magic number (0xEA10)
		data := make([]byte, HeaderSize)
		data[0] = 0x10
		data[1] = 0xEA

		result := IsTextBlob(data)

		require.False(t, result)
	})

	t.Run("Invalid magic number", func(t *testing.T) {
		data := make([]byte, HeaderSize)
		data[0] = 0x00
		data[1] = 0x00

		result := IsTextBlob(data)

		require.False(t, result)
	})

	t.Run("Too short", func(t *testing.T) {
		data := make([]byte, HeaderSize-1)

		result := IsTextBlob(data)

		require.False(t, result)
	})

	t.Run("Empty data", func(t *testing.T) {
		result := IsTextBlob([]byte{})

		require.False(t, result)
	})

	t.Run("Nil data", func(t *testing.T) {
		result := IsTextBlob(nil)

		require.False(t, result)
	})

	t.Run("With extra data", func(t *testing.T) {
		header, err := NewTextHeader(time.Now(), 20)
		require.NoError(t, err)
		data := append(header.Bytes(), []byte{1, 2, 3, 4, 5}...)

		result := IsTextBlob(data)

		require.True(t, result)
	})
}

func TestTextHeader_RoundTrip(t *testing.T) {
	// Create header with all fields set
	startTime := time.Date(2024, 8, 10, 14, 20, 55, 0, time.UTC)
	original, err := NewTextHeader(startTime, 200)
	require.NoError(t, err)

	original.IndexOffset = 32
	original.DataOffset = 5000
	original.DataSize = 100000
	original.Flag.WithTag()
	original.Flag.SetHasMetricNames(true)
	original.Flag.WithBigEndian()

	// Serialize
	data := original.Bytes()

	// Parse back
	parsed, err := ParseTextHeader(data)
	require.NoError(t, err)

	// Verify all fields
	require.Equal(t, original.StartTime, parsed.StartTime)
	require.Equal(t, original.MetricCount, parsed.MetricCount)
	require.Equal(t, original.IndexOffset, parsed.IndexOffset)
	require.Equal(t, original.DataOffset, parsed.DataOffset)
	require.Equal(t, original.DataSize, parsed.DataSize)
	require.Equal(t, original.Flag.HasTag(), parsed.Flag.HasTag())
	require.Equal(t, original.Flag.HasMetricNames(), parsed.Flag.HasMetricNames())
	require.Equal(t, original.Flag.IsBigEndian(), parsed.Flag.IsBigEndian())
}

func TestIsNumericBlob_vs_IsTextBlob(t *testing.T) {
	t.Run("Numeric blob detection", func(t *testing.T) {
		numericHeader := NewNumericHeader(time.Now())
		data := numericHeader.Bytes()

		require.True(t, IsNumericBlob(data))
		require.False(t, IsTextBlob(data))
	})

	t.Run("Text blob detection", func(t *testing.T) {
		textHeader, err := NewTextHeader(time.Now(), 10)
		require.NoError(t, err)
		data := textHeader.Bytes()

		require.False(t, IsNumericBlob(data))
		require.True(t, IsTextBlob(data))
	})
}
