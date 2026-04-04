package blob

import (
	"fmt"
	"time"

	"github.com/arloliu/mebo/compress"
	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/options"
	"github.com/arloliu/mebo/section"
)

// MaxMetricCount is the maximum number of metrics allowed in a single numeric blob.
const MaxMetricCount = 65536

// Index entry capacity growth strategy constants for performance optimization.
const (
	// initialIndexCapacity is the initial capacity for index entries slice.
	// Small enough to avoid waste for small blobs, large enough to avoid early reallocations.
	initialIndexCapacity = 16

	// indexGrowthThreshold is the size threshold where we switch from 2x to 1.25x growth.
	// Below this, we use aggressive 2x doubling; above, we use conservative 1.25x growth.
	indexGrowthThreshold = 256
)

// NumericEncoderConfig handles common numeric encoder configuration and state management.
//
// This struct follows the composition over inheritance principle, allowing
// concrete encoders to focus on their specific encoding logic while reusing
// common configuration and state management.
type NumericEncoderConfig struct {
	header           *section.NumericHeader
	indexEntries     []section.NumericIndexEntry
	tsCodec          compress.Codec
	valCodec         compress.Codec
	tagCodec         compress.Codec
	engine           endian.EndianEngine
	layoutVersion    uint8  // 0=default(v1), 2=v2
	sharedTimestamps bool   // opt-in for shared timestamp detection (implies v2)
	sortedByMetricID bool   // tracks whether metrics were inserted in ascending MetricID order
	lastMetricID     uint64 // last MetricID added (for sorted tracking)
}

// NewNumericEncoderConfig creates a new NumericEncoderConfig with the given start time.
//
// The encoder will grow dynamically as metrics are added, up to MaxMetricCount.
// Index entry capacity starts at initialIndexCapacity and grows using an amortized
// strategy to minimize allocations across small and large blobs alike.
//
// Parameters:
//   - startTime: The reference start time recorded in the blob header.
//
// Returns:
//   - *NumericEncoderConfig: A new encoder configuration ready for use.
func NewNumericEncoderConfig(startTime time.Time) *NumericEncoderConfig {
	header := section.NewNumericHeader(startTime)

	config := &NumericEncoderConfig{
		header:           header,
		indexEntries:     make([]section.NumericIndexEntry, 0, initialIndexCapacity),
		engine:           header.Flag.GetEndianEngine(),
		sortedByMetricID: true, // optimistic: assume ascending insertion order
	}

	return config
}

// Configuration setter methods - these handle all the common encoder options

// setTimestampEncoding sets the timestamp encoding type.
func (c *NumericEncoderConfig) setTimestampEncoding(enc format.EncodingType) error {
	switch enc {
	case format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked:
		c.header.Flag.SetTimestampEncoding(enc)
		return nil
	case format.TypeGorilla, format.TypeChimp:
		return fmt.Errorf("%v encoding is not supported for timestamps", enc)
	default:
		return fmt.Errorf("invalid timestamp encoding: %v", enc)
	}
}

// setValueEncoding sets the value encoding type.
func (c *NumericEncoderConfig) setValueEncoding(enc format.EncodingType) error {
	switch enc { //nolint: exhaustive
	case format.TypeRaw, format.TypeGorilla, format.TypeChimp:
		c.header.Flag.SetValueEncoding(enc)
		return nil
	default:
		return fmt.Errorf("invalid value encoding: %v", enc)
	}
}

// setTimestampCompression sets the timestamp compression type.
func (c *NumericEncoderConfig) setTimestampCompression(comp format.CompressionType) error {
	switch comp {
	case format.CompressionNone, format.CompressionZstd, format.CompressionS2, format.CompressionLZ4:
		c.header.Flag.SetTimestampCompression(comp)
		return nil
	default:
		return fmt.Errorf("invalid timestamp compression: %v", comp)
	}
}

// setValueCompression sets the value compression type.
func (c *NumericEncoderConfig) setValueCompression(comp format.CompressionType) error {
	switch comp {
	case format.CompressionNone, format.CompressionZstd, format.CompressionS2, format.CompressionLZ4:
		c.header.Flag.SetValueCompression(comp)
		return nil
	default:
		return fmt.Errorf("invalid value compression: %v", comp)
	}
}

// setEndianess sets the endianness option.
func (c *NumericEncoderConfig) setEndianess(endiness endianness) {
	if endiness == bigEndianOpt {
		c.header.Flag.WithBigEndian()
	} else {
		// Default to little-endian
		c.header.Flag.WithLittleEndian()
	}

	// Update the engine after changing endianness
	c.engine = c.header.Flag.GetEndianEngine()
}

// setTagsEnabled enables or disables tag support.
func (c *NumericEncoderConfig) setTagsEnabled(enabled bool) {
	if enabled {
		c.header.Flag.WithTag()
	} else {
		c.header.Flag.WithoutTag()
	}
}

// Common helper methods that can be used by concrete encoders

// NumericHeader returns the header for this encoder configuration.
func (c *NumericEncoderConfig) NumericHeader() *section.NumericHeader {
	return c.header
}

// MetricCount returns the current number of metrics added to the encoder.
func (c *NumericEncoderConfig) MetricCount() int {
	return len(c.indexEntries)
}

// TimestampCodec returns the timestamp compression codec.
func (c *NumericEncoderConfig) TimestampCodec() compress.Codec {
	return c.tsCodec
}

// ValueCodec returns the value compression codec.
func (c *NumericEncoderConfig) ValueCodec() compress.Codec {
	return c.valCodec
}

// addEntryIndex adds a new entry index for a completed metric.
// Uses amortized growth strategy to minimize allocations:
// - 2x growth up to 256 entries (aggressive for small blobs)
// - 1.25x growth beyond 256 (conservative for large blobs)
func (c *NumericEncoderConfig) addEntryIndex(entry section.NumericIndexEntry) {
	// Track whether entries remain in ascending MetricID order
	if c.sortedByMetricID && entry.MetricID <= c.lastMetricID && len(c.indexEntries) > 0 {
		c.sortedByMetricID = false
	}
	c.lastMetricID = entry.MetricID

	// Check if we need to grow the slice capacity
	if len(c.indexEntries) == cap(c.indexEntries) {
		// Calculate new capacity using amortized growth
		oldCap := cap(c.indexEntries)
		var newCap int
		if oldCap < indexGrowthThreshold {
			// Aggressive 2x growth for small slices
			newCap = oldCap * 2
		} else {
			// Conservative 1.25x growth for large slices
			newCap = oldCap + oldCap/4
		}

		// Ensure we don't exceed MaxMetricCount
		if newCap > MaxMetricCount {
			newCap = MaxMetricCount
		}

		// Manually grow the slice to avoid append's internal reallocation
		newEntries := make([]section.NumericIndexEntry, len(c.indexEntries), newCap)
		copy(newEntries, c.indexEntries)
		c.indexEntries = newEntries
	}

	c.indexEntries = append(c.indexEntries, entry)
}

// setCodecs sets the compression codecs.
func (c *NumericEncoderConfig) setCodecs(header section.NumericHeader) error {
	// Create compressors based on header settings
	tsCodec, err := compress.CreateCodec(header.Flag.TimestampCompression(), "timestamps")
	if err != nil {
		return err
	}

	valCodec, err := compress.CreateCodec(header.Flag.ValueCompression(), "values")
	if err != nil {
		return err
	}

	// tag payload is calways compressed with zstd
	tagCodec, err := compress.CreateCodec(format.CompressionZstd, "tags")
	if err != nil {
		return err
	}

	c.tsCodec = tsCodec
	c.valCodec = valCodec
	c.tagCodec = tagCodec

	return nil
}

// endianness represents the byte order configuration option.
type endianness uint8

const (
	littleEndianOpt endianness = iota
	bigEndianOpt    endianness = iota
)

// NumericEncoderOption represents a functional option for configuring the NumericEncoderConfig.
// This is a type alias for the generic Option interface specialized for NumericEncoderConfig.
type NumericEncoderOption = options.Option[*NumericEncoderConfig]

// WithLittleEndian sets the encoder to use little-endian byte order.
//
// It is the default byte order. Use WithBigEndian only when interoperability
// with big-endian systems is required.
//
// Returns:
//   - NumericEncoderOption: An option that configures little-endian byte order.
func WithLittleEndian() NumericEncoderOption {
	return options.NoError(func(c *NumericEncoderConfig) {
		c.setEndianess(littleEndianOpt)
	})
}

// WithBigEndian sets the encoder to use big-endian byte order.
//
// This option is rarely needed. Prefer WithLittleEndian (the default) unless
// interoperability with a big-endian system is explicitly required.
//
// Returns:
//   - NumericEncoderOption: An option that configures big-endian byte order.
func WithBigEndian() NumericEncoderOption {
	return options.NoError(func(c *NumericEncoderConfig) {
		c.setEndianess(bigEndianOpt)
	})
}

// WithTimestampEncoding sets the timestamp encoding type for the encoder.
//
// Valid encoding types:
//   - format.TypeRaw: No encoding; timestamps stored as raw 64-bit values.
//   - format.TypeDelta: Delta-of-delta encoding; stores differences between consecutive timestamps as varints, ideal for regular intervals.
//   - format.TypeDeltaPacked: Delta-of-delta encoding with Group Varint packing; better compression for irregular intervals.
//
// The default encoding is format.TypeDelta.
//
// Parameters:
//   - enc: The encoding type to use for timestamps.
//
// Returns:
//   - NumericEncoderOption: An option that sets the timestamp encoding, or an error if the encoding type is unsupported.
func WithTimestampEncoding(enc format.EncodingType) NumericEncoderOption {
	return options.New(func(c *NumericEncoderConfig) error {
		return c.setTimestampEncoding(enc)
	})
}

// WithValueEncoding sets the value encoding type for the encoder.
//
// Valid encoding types:
//   - format.TypeRaw: No encoding; values stored as raw 64-bit IEEE 754 floats.
//   - format.TypeGorilla: Facebook Gorilla XOR encoding; excellent compression for slowly changing float values.
//   - format.TypeChimp: Chimp encoding; improved variant of Gorilla with better compression for noisy or volatile values.
//
// The default encoding is format.TypeGorilla.
//
// Parameters:
//   - enc: The encoding type to use for metric values.
//
// Returns:
//   - NumericEncoderOption: An option that sets the value encoding, or an error if the encoding type is unsupported.
func WithValueEncoding(enc format.EncodingType) NumericEncoderOption {
	return options.New(func(c *NumericEncoderConfig) error {
		return c.setValueEncoding(enc)
	})
}

// WithTimestampCompression sets the timestamp compression type for the encoder.
//
// Valid compression types:
//   - format.CompressionNone: No compression; timestamps stored as-is after encoding.
//   - format.CompressionZstd: Zstandard compression; best ratio, higher CPU cost.
//   - format.CompressionS2: S2 compression; good ratio with lower CPU cost than Zstd.
//   - format.CompressionLZ4: LZ4 compression; fastest decompression, moderate ratio.
//
// The default is format.CompressionNone. Compression is applied on top of the
// selected timestamp encoding (see WithTimestampEncoding).
//
// Parameters:
//   - comp: The compression type to apply to the encoded timestamp stream.
//
// Returns:
//   - NumericEncoderOption: An option that sets the timestamp compression, or an error if the compression type is unsupported.
func WithTimestampCompression(comp format.CompressionType) NumericEncoderOption {
	return options.New(func(c *NumericEncoderConfig) error {
		return c.setTimestampCompression(comp)
	})
}

// WithValueCompression sets the value compression type for the encoder.
//
// Valid compression types:
//   - format.CompressionNone: No compression; values stored as-is after encoding.
//   - format.CompressionZstd: Zstandard compression; best ratio, higher CPU cost.
//   - format.CompressionS2: S2 compression; good ratio with lower CPU cost than Zstd.
//   - format.CompressionLZ4: LZ4 compression; fastest decompression, moderate ratio.
//
// The default is format.CompressionNone. Compression is applied on top of the
// selected value encoding (see WithValueEncoding).
//
// Parameters:
//   - comp: The compression type to apply to the encoded value stream.
//
// Returns:
//   - NumericEncoderOption: An option that sets the value compression, or an error if the compression type is unsupported.
func WithValueCompression(comp format.CompressionType) NumericEncoderOption {
	return options.New(func(c *NumericEncoderConfig) error {
		return c.setValueCompression(comp)
	})
}

// WithTagsEnabled enables or disables per-point tag storage.
//
// When enabled, each data point may carry an associated text tag of up to
// 255 characters. Tags are stored in a separate compressed payload and do
// not affect timestamp or value encoding.
//
// Parameters:
//   - enabled: Set to true to enable tag storage, false to disable it.
//
// Returns:
//   - NumericEncoderOption: An option that enables or disables per-point tags.
func WithTagsEnabled(enabled bool) NumericEncoderOption {
	return options.NoError(func(cfg *NumericEncoderConfig) {
		cfg.setTagsEnabled(enabled)
	})
}

// WithBlobLayoutV2 sets the blob container format to V2.
//
// V2 layout uses a different magic number (MagicNumericV2Opt) and supports
// optional features like shared timestamp tables. Using V2 layout alone does
// not enable shared timestamps — use WithSharedTimestamps() for that.
//
// IMPORTANT: All consumers must be upgraded to a mebo version that supports V2
// decoding before enabling this on producers. The decoder accepts both V1 and V2
// formats, so upgrade consumers first, then enable this option on producers.
func WithBlobLayoutV2() NumericEncoderOption {
	return options.NoError(func(cfg *NumericEncoderConfig) {
		cfg.layoutVersion = 2
	})
}

// WithSharedTimestamps enables shared timestamp detection and encoding.
//
// When enabled, the encoder detects metrics with identical timestamp sequences
// and stores the timestamps only once, reducing blob size significantly when
// many metrics share the same collection timestamps.
//
// This option implies V2 layout (WithBlobLayoutV2). The shared timestamp table
// flag bit is set in the header only when actual sharing is detected.
//
// IMPORTANT: All consumers must be upgraded to a mebo version that supports V2
// decoding before enabling this on producers. The decoder accepts both V1 and V2
// formats, so upgrade consumers first, then enable this option on producers.
func WithSharedTimestamps() NumericEncoderOption {
	return options.NoError(func(cfg *NumericEncoderConfig) {
		cfg.sharedTimestamps = true
		cfg.layoutVersion = 2
	})
}
