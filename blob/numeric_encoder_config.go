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
	header       *section.NumericHeader
	indexEntries []section.NumericIndexEntry
	tsCodec      compress.Codec
	valCodec     compress.Codec
	tagCodec     compress.Codec
	engine       endian.EndianEngine
}

// NewNumericEncoderConfig creates a new NumericEncoderConfig with the given start time.
// The encoder will grow dynamically as metrics are added, up to MaxMetricCount.
func NewNumericEncoderConfig(startTime time.Time) *NumericEncoderConfig {
	header := section.NewNumericHeader(startTime)

	config := &NumericEncoderConfig{
		header:       header,
		indexEntries: make([]section.NumericIndexEntry, 0, initialIndexCapacity),
		engine:       header.Flag.GetEndianEngine(),
	}

	return config
}

// Configuration setter methods - these handle all the common encoder options

// setTimestampEncoding sets the timestamp encoding type.
func (c *NumericEncoderConfig) setTimestampEncoding(enc format.EncodingType) error {
	switch enc {
	case format.TypeRaw, format.TypeDelta:
		c.header.Flag.SetTimestampEncoding(enc)
		return nil
	case format.TypeGorilla:
		return fmt.Errorf("gorilla encoding is not supported for timestamps")
	default:
		return fmt.Errorf("invalid timestamp encoding: %v", enc)
	}
}

// setValueEncoding sets the value encoding type.
func (c *NumericEncoderConfig) setValueEncoding(enc format.EncodingType) error {
	switch enc { //nolint: exhaustive
	case format.TypeRaw, format.TypeGorilla:
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
	switch endiness {
	case littleEndianOpt:
		c.header.Flag.WithLittleEndian()
	case bigEndianOpt:
		c.header.Flag.WithBigEndian()
	default:
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
// It is the default option.
func WithLittleEndian() NumericEncoderOption {
	return options.NoError(func(c *NumericEncoderConfig) {
		c.setEndianess(littleEndianOpt)
	})
}

// WithBigEndian sets the encoder to use big-endian byte order.
// It rarely needs to be used unless interoperability with big-endian systems is required.
func WithBigEndian() NumericEncoderOption {
	return options.NoError(func(c *NumericEncoderConfig) {
		c.setEndianess(bigEndianOpt)
	})
}

// WithTimestampEncoding sets the timestamp encoding type for the encoder.
func WithTimestampEncoding(enc format.EncodingType) NumericEncoderOption {
	return options.New(func(c *NumericEncoderConfig) error {
		return c.setTimestampEncoding(enc)
	})
}

// WithValueEncoding sets the value encoding type for the encoder.
func WithValueEncoding(enc format.EncodingType) NumericEncoderOption {
	return options.New(func(c *NumericEncoderConfig) error {
		return c.setValueEncoding(enc)
	})
}

// WithTimestampCompression sets the timestamp compression type for the encoder.
func WithTimestampCompression(comp format.CompressionType) NumericEncoderOption {
	return options.New(func(c *NumericEncoderConfig) error {
		return c.setTimestampCompression(comp)
	})
}

// WithValueCompression sets the value compression type for the encoder.
func WithValueCompression(comp format.CompressionType) NumericEncoderOption {
	return options.New(func(c *NumericEncoderConfig) error {
		return c.setValueCompression(comp)
	})
}

// WithTagsEnabled enables per-point tags when set to true.
// Tags are stored as text strings with a maximum length of 255 characters.
func WithTagsEnabled(enabled bool) NumericEncoderOption {
	return options.NoError(func(cfg *NumericEncoderConfig) {
		cfg.setTagsEnabled(enabled)
	})
}
