package regression

import (
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/options"
)

// AnalyzeConfig holds configuration for analysis re-encoding parameters.
type AnalyzeConfig struct {
	TimestampEncoding    format.EncodingType
	ValueEncoding        format.EncodingType
	TimestampCompression format.CompressionType
	ValueCompression     format.CompressionType
}

// defaultAnalyzeConfig returns default config (Delta ts + Gorilla values, no compression).
func defaultAnalyzeConfig() AnalyzeConfig {
	return AnalyzeConfig{
		TimestampEncoding:    format.TypeDelta,
		ValueEncoding:        format.TypeGorilla,
		TimestampCompression: format.CompressionNone,
		ValueCompression:     format.CompressionNone,
	}
}

// AnalyzeOption is a functional option for AnalyzeConfig.
type AnalyzeOption = options.Option[*AnalyzeConfig]

// WithTimestampEncoding sets timestamp encoding.
func WithTimestampEncoding(enc format.EncodingType) AnalyzeOption {
	return options.NoError(func(cfg *AnalyzeConfig) {
		cfg.TimestampEncoding = enc
	})
}

// WithValueEncoding sets value encoding.
func WithValueEncoding(enc format.EncodingType) AnalyzeOption {
	return options.NoError(func(cfg *AnalyzeConfig) {
		cfg.ValueEncoding = enc
	})
}

// WithTimestampCompression sets timestamp compression.
func WithTimestampCompression(comp format.CompressionType) AnalyzeOption {
	return options.NoError(func(cfg *AnalyzeConfig) {
		cfg.TimestampCompression = comp
	})
}

// WithValueCompression sets value compression.
func WithValueCompression(comp format.CompressionType) AnalyzeOption {
	return options.NoError(func(cfg *AnalyzeConfig) {
		cfg.ValueCompression = comp
	})
}
