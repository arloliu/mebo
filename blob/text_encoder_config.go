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

// TextEncoderConfig handles common text encoder configuration and state management.
//
// This struct follows the composition over inheritance principle, allowing
// concrete encoders to focus on their specific encoding logic while reusing
// common configuration and state management.
type TextEncoderConfig struct {
	header       *section.TextHeader
	indexEntries []section.TextIndexEntry
	dataCodec    compress.Codec
	engine       endian.EndianEngine
}

// NewTextEncoderConfig creates a new TextEncoderConfig with the given start time.
// The encoder will grow dynamically as metrics are added, up to MaxMetricCount.
func NewTextEncoderConfig(startTime time.Time) *TextEncoderConfig {
	// Start with 0 metric count - will grow dynamically
	header, _ := section.NewTextHeader(startTime, 0)

	config := &TextEncoderConfig{
		header:       header,
		indexEntries: make([]section.TextIndexEntry, 0, initialIndexCapacity),
		engine:       header.GetEndianEngine(),
	}

	return config
}

// Configuration setter methods - these handle all the common encoder options

// setTimestampEncoding sets the timestamp encoding type.
func (c *TextEncoderConfig) setTimestampEncoding(enc format.EncodingType) error {
	switch enc { //nolint: exhaustive
	case format.TypeRaw, format.TypeDelta:
		c.header.Flag.SetTimestampEncoding(enc)
		return nil
	default:
		return fmt.Errorf("invalid timestamp encoding: %v", enc)
	}
}

// setDataCompression sets the data compression type.
func (c *TextEncoderConfig) setDataCompression(comp format.CompressionType) error {
	switch comp {
	case format.CompressionNone, format.CompressionZstd, format.CompressionS2, format.CompressionLZ4:
		c.header.Flag.SetDataCompression(comp)
		return nil
	default:
		return fmt.Errorf("invalid data compression: %v", comp)
	}
}

// setEndianess sets the endianness option.
func (c *TextEncoderConfig) setEndianess(endiness endianness) {
	if endiness == bigEndianOpt {
		c.header.Flag.WithBigEndian()
	} else {
		// Default to little-endian
		c.header.Flag.WithLittleEndian()
	}

	// Update the engine after changing endianness
	c.engine = c.header.GetEndianEngine()
}

// setTagsEnabled enables or disables tag support.
func (c *TextEncoderConfig) setTagsEnabled(enabled bool) {
	if enabled {
		c.header.Flag.WithTag()
	} else {
		c.header.Flag.WithoutTag()
	}
}

// Common helper methods that can be used by concrete encoders

// TextHeader returns the header for this encoder configuration.
func (c *TextEncoderConfig) TextHeader() *section.TextHeader {
	return c.header
}

// MetricCount returns the current number of metrics added to the encoder.
func (c *TextEncoderConfig) MetricCount() int {
	return len(c.indexEntries)
}

// DataCodec returns the data compression codec.
func (c *TextEncoderConfig) DataCodec() compress.Codec {
	return c.dataCodec
}

// setCodecs initializes the compression codecs based on the header configuration.
func (c *TextEncoderConfig) setCodecs(header section.TextHeader) error {
	var err error

	// Initialize data codec
	dataCompType := header.Flag.GetDataCompression()
	c.dataCodec, err = compress.CreateCodec(dataCompType, "data")
	if err != nil {
		return fmt.Errorf("failed to create data codec: %w", err)
	}

	return nil
}

// addEntryIndex adds a new entry index for a completed metric.
// Uses amortized growth strategy to minimize allocations:
// - 2x growth up to 256 entries (aggressive for small blobs)
// - 1.25x growth beyond 256 (conservative for large blobs)
func (c *TextEncoderConfig) addEntryIndex(entry section.TextIndexEntry) {
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
		newEntries := make([]section.TextIndexEntry, len(c.indexEntries), newCap)
		copy(newEntries, c.indexEntries)
		c.indexEntries = newEntries
	}

	c.indexEntries = append(c.indexEntries, entry)
}

// TextEncoderOption is a functional option for configuring TextEncoder.
type TextEncoderOption = options.Option[*TextEncoderConfig]

// WithTextTimestampEncoding configures the timestamp encoding type.
// Valid values are format.TypeRaw and format.TypeDelta.
// Default is format.TypeDelta for better compression.
func WithTextTimestampEncoding(enc format.EncodingType) TextEncoderOption {
	return options.NoError(func(cfg *TextEncoderConfig) {
		_ = cfg.setTimestampEncoding(enc)
	})
}

// WithTextDataCompression configures compression for text data section.
// Available compression types: format.CompressionZstd, format.CompressionS2,
// format.CompressionLZ4, format.CompressionNone.
// Default is format.CompressionZstd.
func WithTextDataCompression(codec format.CompressionType) TextEncoderOption {
	return options.NoError(func(cfg *TextEncoderConfig) {
		_ = cfg.setDataCompression(codec)
	})
}

// WithTextTagsEnabled enables per-point tags when set to true.
// Tags are stored as text strings with a maximum length of 255 characters.
// Default is false.
func WithTextTagsEnabled(enabled bool) TextEncoderOption {
	return options.NoError(func(cfg *TextEncoderConfig) {
		cfg.setTagsEnabled(enabled)
	})
}

// WithTextLittleEndian sets the encoder to use little-endian byte order.
// This is the default endianness for most modern systems.
func WithTextLittleEndian() TextEncoderOption {
	return options.NoError(func(c *TextEncoderConfig) {
		c.setEndianess(littleEndianOpt)
	})
}

// WithTextBigEndian sets the encoder to use big-endian byte order.
// Use this for compatibility with big-endian systems.
func WithTextBigEndian() TextEncoderOption {
	return options.NoError(func(c *TextEncoderConfig) {
		c.setEndianess(bigEndianOpt)
	})
}
