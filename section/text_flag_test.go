package section

import (
	"testing"

	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/stretchr/testify/require"
)

func TestNewTextFlag(t *testing.T) {
	flag := NewTextFlag()

	// Default values
	require.False(t, flag.HasTag())
	require.False(t, flag.HasMetricNames())
	require.True(t, flag.IsLittleEndian())
	require.False(t, flag.IsBigEndian())
	require.Equal(t, format.TypeRaw, flag.GetTimestampEncoding())
	require.Equal(t, format.CompressionZstd, flag.GetDataCompression())
}

func TestTextFlag_TagSupport(t *testing.T) {
	flag := NewTextFlag()

	// Initially no tag support
	require.False(t, flag.HasTag())

	// Enable tag support
	flag.WithTag()
	require.True(t, flag.HasTag())

	// Disable tag support
	flag.WithoutTag()
	require.False(t, flag.HasTag())
}

func TestTextFlag_MetricNames(t *testing.T) {
	flag := NewTextFlag()

	// Initially no metric names
	require.False(t, flag.HasMetricNames())

	// Enable metric names
	flag.SetHasMetricNames(true)
	require.True(t, flag.HasMetricNames())

	// Disable metric names
	flag.SetHasMetricNames(false)
	require.False(t, flag.HasMetricNames())
}

func TestTextFlag_Endianness(t *testing.T) {
	flag := NewTextFlag()

	// Default is little endian
	require.True(t, flag.IsLittleEndian())
	require.False(t, flag.IsBigEndian())

	// Switch to big endian
	flag.WithBigEndian()
	require.False(t, flag.IsLittleEndian())
	require.True(t, flag.IsBigEndian())

	// Switch back to little endian
	flag.WithLittleEndian()
	require.True(t, flag.IsLittleEndian())
	require.False(t, flag.IsBigEndian())
}

func TestTextFlag_TimestampEncoding(t *testing.T) {
	flag := NewTextFlag()

	// Default is raw
	require.Equal(t, format.TypeRaw, flag.GetTimestampEncoding())

	// Set to delta
	flag.SetTimestampEncoding(format.TypeDelta)
	require.Equal(t, format.TypeDelta, flag.GetTimestampEncoding())

	// Set back to raw
	flag.SetTimestampEncoding(format.TypeRaw)
	require.Equal(t, format.TypeRaw, flag.GetTimestampEncoding())
}

func TestTextFlag_DataCompression(t *testing.T) {
	flag := NewTextFlag()

	// Default is Zstd
	require.Equal(t, format.CompressionZstd, flag.GetDataCompression())

	// Test all compression types
	compressions := []format.CompressionType{
		format.CompressionNone,
		format.CompressionZstd,
		format.CompressionS2,
		format.CompressionLZ4,
	}

	for _, comp := range compressions {
		flag.SetDataCompression(comp)
		require.Equal(t, comp, flag.GetDataCompression())
	}
}

func TestTextFlag_Validate_Success(t *testing.T) {
	flag := NewTextFlag()

	// Valid flag should pass validation
	err := flag.Validate()
	require.NoError(t, err)

	// Enable various options and revalidate
	flag.WithTag()
	flag.SetHasMetricNames(true)
	flag.WithBigEndian()
	flag.SetTimestampEncoding(format.TypeDelta)
	flag.SetDataCompression(format.CompressionS2)

	err = flag.Validate()
	require.NoError(t, err)
}

func TestTextFlag_Validate_ReservedBits(t *testing.T) {
	flag := NewTextFlag()

	// Set reserved bits (should fail validation)
	flag.Options |= ReservedBitsMask
	err := flag.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidHeaderFlags)
}

func TestTextFlag_Validate_MagicNumber(t *testing.T) {
	flag := NewTextFlag()

	// Corrupt magic number
	flag.Options &^= MagicNumberMask // Clear magic bits
	flag.Options |= 0xAB00           // Set wrong magic

	err := flag.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidHeaderFlags)
}

func TestTextFlag_Validate_TimestampEncoding(t *testing.T) {
	flag := NewTextFlag()

	// Invalid timestamp encoding
	flag.TimestampEncoding = 0xFF

	err := flag.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidHeaderFlags)
}

func TestTextFlag_Validate_DataCompression(t *testing.T) {
	flag := NewTextFlag()

	// Invalid data compression
	flag.DataCompression = 0xFF

	err := flag.Validate()
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidHeaderFlags)
}

func TestTextFlag_CombinedOptions(t *testing.T) {
	flag := NewTextFlag()

	// Enable multiple options
	flag.WithTag()
	flag.SetHasMetricNames(true)
	flag.WithBigEndian()
	flag.SetTimestampEncoding(format.TypeDelta)
	flag.SetDataCompression(format.CompressionLZ4)

	// Verify all options are set correctly
	require.True(t, flag.HasTag())
	require.True(t, flag.HasMetricNames())
	require.True(t, flag.IsBigEndian())
	require.False(t, flag.IsLittleEndian())
	require.Equal(t, format.TypeDelta, flag.GetTimestampEncoding())
	require.Equal(t, format.CompressionLZ4, flag.GetDataCompression())

	// Validation should pass
	err := flag.Validate()
	require.NoError(t, err)
}

func TestTextFlag_BitMasksDoNotOverlap(t *testing.T) {
	// Verify that bit masks don't overlap
	masks := []uint16{
		TagMask,
		EndiannessMask,
		MetricNamesMask,
		ReservedBitsMask,
	}

	for i := range masks {
		for j := i + 1; j < len(masks); j++ {
			overlap := masks[i] & masks[j]
			require.Equal(t, uint16(0), overlap,
				"masks should not overlap: 0x%04X & 0x%04X = 0x%04X",
				masks[i], masks[j], overlap)
		}
	}
}

func TestTextFlag_MagicNumberPreserved(t *testing.T) {
	flag := NewTextFlag()

	// Manipulate various flags
	flag.WithTag()
	flag.SetHasMetricNames(true)
	flag.WithBigEndian()

	// Magic number should remain unchanged
	magic := flag.GetMagicNumber()
	require.Equal(t, uint16(MagicTextV1Opt), magic)
}
