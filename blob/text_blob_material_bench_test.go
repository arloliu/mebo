package blob

import (
	"fmt"
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
)

// BenchmarkTextMaterialize benchmarks the full text blob materialization process
// for different encoding scenarios and blob sizes.
func BenchmarkTextMaterialize(b *testing.B) {
	testCases := []struct {
		name       string
		tsEnc      format.EncodingType
		metrics    int
		pointsEach int
		withTags   bool
	}{
		// Small blob scenarios
		{"Small_Raw_NoTags", format.TypeRaw, 10, 100, false},
		{"Small_Raw_WithTags", format.TypeRaw, 10, 100, true},
		{"Small_Delta_NoTags", format.TypeDelta, 10, 100, false},
		{"Small_Delta_WithTags", format.TypeDelta, 10, 100, true},

		// Medium blob scenarios (realistic production size)
		{"Medium_Raw_NoTags", format.TypeRaw, 50, 1000, false},
		{"Medium_Raw_WithTags", format.TypeRaw, 50, 1000, true},
		{"Medium_Delta_NoTags", format.TypeDelta, 50, 1000, false},
		{"Medium_Delta_WithTags", format.TypeDelta, 50, 1000, true},

		// Large blob scenarios
		{"Large_Raw_NoTags", format.TypeRaw, 100, 5000, false},
		{"Large_Raw_WithTags", format.TypeRaw, 100, 5000, true},
		{"Large_Delta_NoTags", format.TypeDelta, 100, 5000, false},
		{"Large_Delta_WithTags", format.TypeDelta, 100, 5000, true},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Create test blob
			blob := createTextBenchmarkBlob(tc.metrics, tc.pointsEach, tc.tsEnc, tc.withTags)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_ = blob.Materialize()
			}
		})
	}
}

// BenchmarkTextMaterializeMetric benchmarks single metric materialization
func BenchmarkTextMaterializeMetric(b *testing.B) {
	testCases := []struct {
		name     string
		tsEnc    format.EncodingType
		points   int
		withTags bool
	}{
		{"Small_Raw_NoTags", format.TypeRaw, 100, false},
		{"Small_Raw_WithTags", format.TypeRaw, 100, true},
		{"Small_Delta_NoTags", format.TypeDelta, 100, false},
		{"Small_Delta_WithTags", format.TypeDelta, 100, true},

		{"Medium_Raw_NoTags", format.TypeRaw, 1000, false},
		{"Medium_Raw_WithTags", format.TypeRaw, 1000, true},
		{"Medium_Delta_NoTags", format.TypeDelta, 1000, false},
		{"Medium_Delta_WithTags", format.TypeDelta, 1000, true},

		{"Large_Raw_NoTags", format.TypeRaw, 5000, false},
		{"Large_Raw_WithTags", format.TypeRaw, 5000, true},
		{"Large_Delta_NoTags", format.TypeDelta, 5000, false},
		{"Large_Delta_WithTags", format.TypeDelta, 5000, true},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Create test blob with one metric
			blob := createTextBenchmarkBlob(1, tc.points, tc.tsEnc, tc.withTags)
			metricID := blob.MetricIDs()[0]

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, _ = blob.MaterializeMetric(metricID)
			}
		})
	}
}

// createTextBenchmarkBlob creates a text blob for benchmarking with specified characteristics
func createTextBenchmarkBlob(metrics, pointsEach int, tsEnc format.EncodingType, withTags bool) TextBlob {
	baseTime := time.Unix(1000000, 0).UTC()

	var opts []TextEncoderOption
	if tsEnc != format.TypeRaw {
		opts = append(opts, WithTextTimestampEncoding(tsEnc))
	}
	if withTags {
		opts = append(opts, WithTextTagsEnabled(true))
	}

	encoder, err := NewTextEncoder(baseTime, opts...)
	if err != nil {
		panic(err)
	}

	for m := 0; m < metrics; m++ {
		metricID := uint64(1000 + m)

		if err := encoder.StartMetricID(metricID, pointsEach); err != nil {
			panic(err)
		}

		for p := 0; p < pointsEach; p++ {
			ts := baseTime.Add(time.Duration(p) * time.Second).UnixMicro()
			val := fmt.Sprintf("value_%d", p)
			tag := ""
			if withTags {
				tag = "tag_value"
			}

			if err := encoder.AddDataPoint(ts, val, tag); err != nil {
				panic(err)
			}
		}

		if err := encoder.EndMetric(); err != nil {
			panic(err)
		}
	}

	data, err := encoder.Finish()
	if err != nil {
		panic(err)
	}

	decoder, err := NewTextDecoder(data)
	if err != nil {
		panic(err)
	}

	blob, err := decoder.Decode()
	if err != nil {
		panic(err)
	}

	return blob
}
