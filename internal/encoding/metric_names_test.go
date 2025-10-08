package encoding

import (
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeMetricNames(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	names := []string{
		"cpu.usage",
		"memory.total",
		"disk.io.read",
		"network.bytes.sent",
	}

	// Encode
	encoded, err := EncodeMetricNames(names, engine)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	// Decode
	decoded, bytesRead, err := DecodeMetricNames(encoded, engine)
	require.NoError(t, err)
	require.Equal(t, len(encoded), bytesRead)
	require.Equal(t, names, decoded)
}

func TestEncodeMetricNamesEmptyList(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	names := []string{}

	encoded, err := EncodeMetricNames(names, engine)
	require.NoError(t, err)
	require.Len(t, encoded, 2) // Just count field

	decoded, bytesRead, err := DecodeMetricNames(encoded, engine)
	require.NoError(t, err)
	require.Equal(t, 2, bytesRead)
	require.Empty(t, decoded)
}

func TestEncodeMetricNamesTooMany(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Create more than uint16 max names
	names := make([]string, 65536)
	for i := range names {
		names[i] = "metric"
	}

	_, err := EncodeMetricNames(names, engine)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidMetricNamesCount)
}

func TestEncodeMetricNamesTooLong(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Create name longer than uint16 max
	longName := make([]byte, 65536)
	for i := range longName {
		longName[i] = 'a'
	}

	names := []string{string(longName)}

	_, err := EncodeMetricNames(names, engine)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidMetricName)
}

func TestDecodeMetricNamesTruncatedCount(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Only 1 byte instead of 2 for count
	data := []byte{0x05}

	_, _, err := DecodeMetricNames(data, engine)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidMetricNamesPayload)
}

func TestDecodeMetricNamesTruncatedLength(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Count=1 but missing length field
	data := []byte{0x01, 0x00} // count=1

	_, _, err := DecodeMetricNames(data, engine)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidMetricNamesPayload)
}

func TestDecodeMetricNamesTruncatedName(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Count=1, len=10, but only 5 bytes of name
	data := []byte{
		0x01, 0x00, // count=1
		0x0A, 0x00, // len=10
		'h', 'e', 'l', 'l', 'o', // only 5 bytes
	}

	_, _, err := DecodeMetricNames(data, engine)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidMetricNamesPayload)
}

func TestEncodeDecodeMetricNamesWithUnicode(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	names := []string{
		"cpu使用率",
		"メモリ合計",
		"disque.io.読取",
		"네트워크.바이트.송신",
	}

	encoded, err := EncodeMetricNames(names, engine)
	require.NoError(t, err)

	decoded, _, err := DecodeMetricNames(encoded, engine)
	require.NoError(t, err)
	require.Equal(t, names, decoded)
}

func TestVerifyMetricNamesHashesSuccess(t *testing.T) {
	names := []string{"metric1", "metric2", "metric3"}
	hashFunc := func(s string) uint64 {
		// Simple hash for testing
		return uint64(len(s))
	}

	metricIDs := []uint64{7, 7, 7} // All have length 7

	err := VerifyMetricNamesHashes(names, metricIDs, hashFunc)
	require.NoError(t, err)
}

func TestVerifyMetricNamesHashesMismatch(t *testing.T) {
	names := []string{"metric1", "metric2", "metric3"}
	hashFunc := func(s string) uint64 {
		return uint64(len(s))
	}

	metricIDs := []uint64{7, 8, 7} // Middle one is wrong

	err := VerifyMetricNamesHashes(names, metricIDs, hashFunc)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrHashMismatch)
	require.Contains(t, err.Error(), "metric2")
}

func TestVerifyMetricNamesHashesCountMismatch(t *testing.T) {
	names := []string{"metric1", "metric2"}
	hashFunc := func(s string) uint64 {
		return uint64(len(s))
	}

	metricIDs := []uint64{7, 7, 7} // 3 IDs but only 2 names

	err := VerifyMetricNamesHashes(names, metricIDs, hashFunc)
	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrInvalidMetricNamesCount)
}

func TestEncodeDecodeMetricNamesLargeList(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Test with 1000 metrics
	names := make([]string, 1000)
	for i := range names {
		names[i] = "metric." + string(rune('a'+i%26))
	}

	encoded, err := EncodeMetricNames(names, engine)
	require.NoError(t, err)

	decoded, bytesRead, err := DecodeMetricNames(encoded, engine)
	require.NoError(t, err)
	require.Equal(t, len(encoded), bytesRead)
	require.Equal(t, names, decoded)
}

func TestEncodeDecodeMetricNamesEmptyStrings(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Test with empty metric names (edge case)
	names := []string{"", "", "valid.name", ""}

	encoded, err := EncodeMetricNames(names, engine)
	require.NoError(t, err)

	decoded, _, err := DecodeMetricNames(encoded, engine)
	require.NoError(t, err)
	require.Equal(t, names, decoded)
}

func TestEncodeDecodeMetricNamesBigEndian(t *testing.T) {
	engine := endian.GetBigEndianEngine()

	names := []string{"metric.one", "metric.two", "metric.three"}

	encoded, err := EncodeMetricNames(names, engine)
	require.NoError(t, err)

	decoded, _, err := DecodeMetricNames(encoded, engine)
	require.NoError(t, err)
	require.Equal(t, names, decoded)
}

func TestEncodeDecodeMetricNamesMaxUint16Count(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Test with max uint16 count (65535)
	names := make([]string, 65535)
	for i := range names {
		names[i] = "m"
	}

	encoded, err := EncodeMetricNames(names, engine)
	require.NoError(t, err)

	decoded, bytesRead, err := DecodeMetricNames(encoded, engine)
	require.NoError(t, err)
	require.Equal(t, len(encoded), bytesRead)
	require.Equal(t, len(names), len(decoded))
}
