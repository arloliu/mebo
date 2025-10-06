package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
)

// Benchmark encoding with tags disabled vs enabled to show performance improvement
func BenchmarkNumericEncoder_TagsDisabled_vs_Enabled(b *testing.B) {
	testCases := []struct {
		name            string
		metricCount     int
		pointsPerMetric int
	}{
		{"100metrics_10points", 100, 10},
		{"150metrics_10points", 150, 10},
		{"100metrics_100points", 100, 100},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Benchmark with tags disabled
			b.Run("TagsDisabled", func(b *testing.B) {
				b.ReportAllocs()
				startTime := time.Now()

				for b.Loop() {
					encoder, _ := NewNumericEncoder(startTime)

					for m := 0; m < tc.metricCount; m++ {
						_ = encoder.StartMetricID(uint64(m+1), tc.pointsPerMetric)
						for p := 0; p < tc.pointsPerMetric; p++ {
							_ = encoder.AddDataPoint(int64(p*1000), float64(p)+0.5, "")
						}
						_ = encoder.EndMetric()
					}

					_, _ = encoder.Finish()
				}
			})

			// Benchmark with tags enabled
			b.Run("TagsEnabled", func(b *testing.B) {
				b.ReportAllocs()
				startTime := time.Now()

				for b.Loop() {
					encoder, _ := NewNumericEncoder(startTime, WithTagsEnabled(true))

					for m := 0; m < tc.metricCount; m++ {
						_ = encoder.StartMetricID(uint64(m+1), tc.pointsPerMetric)
						for p := 0; p < tc.pointsPerMetric; p++ {
							_ = encoder.AddDataPoint(int64(p*1000), float64(p)+0.5, "")
						}
						_ = encoder.EndMetric()
					}

					_, _ = encoder.Finish()
				}
			})
		})
	}
}

// Benchmark decoding with tags disabled vs enabled
func BenchmarkNumericDecoder_TagsDisabled_vs_Enabled(b *testing.B) {
	metricCount := 150
	pointsPerMetric := 10
	startTime := time.Now()

	// Prepare data with tags disabled
	encoderDisabled, _ := NewNumericEncoder(startTime)
	for m := 0; m < metricCount; m++ {
		_ = encoderDisabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := 0; p < pointsPerMetric; p++ {
			_ = encoderDisabled.AddDataPoint(int64(p*1000), float64(p)+0.5, "")
		}
		_ = encoderDisabled.EndMetric()
	}
	dataDisabled, _ := encoderDisabled.Finish()

	// Prepare data with tags enabled
	encoderEnabled, _ := NewNumericEncoder(startTime, WithTagsEnabled(true))
	for m := 0; m < metricCount; m++ {
		_ = encoderEnabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := 0; p < pointsPerMetric; p++ {
			_ = encoderEnabled.AddDataPoint(int64(p*1000), float64(p)+0.5, "tag")
		}
		_ = encoderEnabled.EndMetric()
	}
	dataEnabled, _ := encoderEnabled.Finish()

	b.Run("TagsDisabled", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			decoder, _ := NewNumericDecoder(dataDisabled)
			_, _ = decoder.Decode()
		}
	})

	b.Run("TagsEnabled", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			decoder, _ := NewNumericDecoder(dataEnabled)
			_, _ = decoder.Decode()
		}
	})
}

// Benchmark All() iteration with tags disabled vs enabled
func BenchmarkNumericBlob_All_TagsDisabled_vs_Enabled(b *testing.B) {
	metricCount := 150
	pointsPerMetric := 10
	startTime := time.Now()

	// Prepare blob with tags disabled
	encoderDisabled, _ := NewNumericEncoder(startTime)
	for m := 0; m < metricCount; m++ {
		_ = encoderDisabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := 0; p < pointsPerMetric; p++ {
			_ = encoderDisabled.AddDataPoint(int64(p*1000), float64(p)+0.5, "")
		}
		_ = encoderDisabled.EndMetric()
	}
	dataDisabled, _ := encoderDisabled.Finish()
	decoderDisabled, _ := NewNumericDecoder(dataDisabled)
	blobDisabled, _ := decoderDisabled.Decode()

	// Prepare blob with tags enabled
	encoderEnabled, _ := NewNumericEncoder(startTime, WithTagsEnabled(true))
	for m := 0; m < metricCount; m++ {
		_ = encoderEnabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := 0; p < pointsPerMetric; p++ {
			_ = encoderEnabled.AddDataPoint(int64(p*1000), float64(p)+0.5, "tag")
		}
		_ = encoderEnabled.EndMetric()
	}
	dataEnabled, _ := encoderEnabled.Finish()
	decoderEnabled, _ := NewNumericDecoder(dataEnabled)
	blobEnabled, _ := decoderEnabled.Decode()

	b.Run("TagsDisabled", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, dp := range blobDisabled.All(1) {
				_ = dp
			}
		}
	})

	b.Run("TagsEnabled", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, dp := range blobEnabled.All(1) {
				_ = dp
			}
		}
	})
}

// Benchmark with delta encoding to show performance improvement
func BenchmarkNumericEncoder_DeltaEncoding_TagsDisabled_vs_Enabled(b *testing.B) {
	metricCount := 150
	pointsPerMetric := 10
	startTime := time.Now()

	b.Run("TagsDisabled", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			encoder, _ := NewNumericEncoder(startTime, WithTimestampEncoding(format.TypeDelta))

			for m := 0; m < metricCount; m++ {
				_ = encoder.StartMetricID(uint64(m+1), pointsPerMetric)
				for p := 0; p < pointsPerMetric; p++ {
					_ = encoder.AddDataPoint(int64(p*1000), float64(p)+0.5, "")
				}
				_ = encoder.EndMetric()
			}

			_, _ = encoder.Finish()
		}
	})

	b.Run("TagsEnabled", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			encoder, _ := NewNumericEncoder(startTime, WithTimestampEncoding(format.TypeDelta), WithTagsEnabled(true))

			for m := 0; m < metricCount; m++ {
				_ = encoder.StartMetricID(uint64(m+1), pointsPerMetric)
				for p := 0; p < pointsPerMetric; p++ {
					_ = encoder.AddDataPoint(int64(p*1000), float64(p)+0.5, "tag")
				}
				_ = encoder.EndMetric()
			}

			_, _ = encoder.Finish()
		}
	})
}

// Benchmark blob size comparison
func BenchmarkBlobSize_TagsDisabled_vs_Enabled(b *testing.B) {
	metricCount := 150
	pointsPerMetric := 10
	startTime := time.Now()

	// Measure with tags disabled
	encoderDisabled, _ := NewNumericEncoder(startTime)
	for m := 0; m < metricCount; m++ {
		_ = encoderDisabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := 0; p < pointsPerMetric; p++ {
			_ = encoderDisabled.AddDataPoint(int64(p*1000), float64(p)+0.5, "")
		}
		_ = encoderDisabled.EndMetric()
	}
	dataDisabled, _ := encoderDisabled.Finish()

	// Measure with tags enabled (empty tags)
	encoderEnabled, _ := NewNumericEncoder(startTime, WithTagsEnabled(true))
	for m := 0; m < metricCount; m++ {
		_ = encoderEnabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := 0; p < pointsPerMetric; p++ {
			_ = encoderEnabled.AddDataPoint(int64(p*1000), float64(p)+0.5, "")
		}
		_ = encoderEnabled.EndMetric()
	}
	dataEnabled, _ := encoderEnabled.Finish()

	b.Logf("Blob size with tags disabled: %d bytes", len(dataDisabled))
	b.Logf("Blob size with tags enabled (empty): %d bytes", len(dataEnabled))
	b.Logf("Space savings: %d bytes (%.1f%% reduction)", len(dataEnabled)-len(dataDisabled),
		float64(len(dataEnabled)-len(dataDisabled))/float64(len(dataEnabled))*100)
}
