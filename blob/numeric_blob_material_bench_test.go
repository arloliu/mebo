package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
)

// BenchmarkMaterialize benchmarks the full blob materialization process
// for different encoding combinations and blob sizes.
func BenchmarkMaterialize(b *testing.B) {
	testCases := []struct {
		name       string
		tsEnc      format.EncodingType
		valEnc     format.EncodingType
		metrics    int
		pointsEach int
		withTags   bool
	}{
		// Small blob scenarios
		{"Small_Raw-Raw_NoTags", format.TypeRaw, format.TypeRaw, 10, 100, false},
		{"Small_Raw-Raw_WithTags", format.TypeRaw, format.TypeRaw, 10, 100, true},
		{"Small_Delta-Gorilla_NoTags", format.TypeDelta, format.TypeGorilla, 10, 100, false},
		{"Small_Delta-Gorilla_WithTags", format.TypeDelta, format.TypeGorilla, 10, 100, true},

		// Medium blob scenarios (realistic production size)
		{"Medium_Raw-Raw_NoTags", format.TypeRaw, format.TypeRaw, 50, 1000, false},
		{"Medium_Raw-Raw_WithTags", format.TypeRaw, format.TypeRaw, 50, 1000, true},
		{"Medium_Delta-Gorilla_NoTags", format.TypeDelta, format.TypeGorilla, 50, 1000, false},
		{"Medium_Delta-Gorilla_WithTags", format.TypeDelta, format.TypeGorilla, 50, 1000, true},

		// Large blob scenarios
		{"Large_Raw-Raw_NoTags", format.TypeRaw, format.TypeRaw, 100, 5000, false},
		{"Large_Raw-Raw_WithTags", format.TypeRaw, format.TypeRaw, 100, 5000, true},
		{"Large_Delta-Gorilla_NoTags", format.TypeDelta, format.TypeGorilla, 100, 5000, false},
		{"Large_Delta-Gorilla_WithTags", format.TypeDelta, format.TypeGorilla, 100, 5000, true},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Create test blob
			blob := createBenchmarkBlob(tc.metrics, tc.pointsEach, tc.tsEnc, tc.valEnc, tc.withTags)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_ = blob.Materialize()
			}
		})
	}
}

// BenchmarkMaterializeMetric benchmarks single metric materialization
func BenchmarkMaterializeMetric(b *testing.B) {
	testCases := []struct {
		name     string
		tsEnc    format.EncodingType
		valEnc   format.EncodingType
		points   int
		withTags bool
	}{
		{"Small_Raw-Raw_NoTags", format.TypeRaw, format.TypeRaw, 100, false},
		{"Small_Raw-Raw_WithTags", format.TypeRaw, format.TypeRaw, 100, true},
		{"Small_Delta-Gorilla_NoTags", format.TypeDelta, format.TypeGorilla, 100, false},
		{"Small_Delta-Gorilla_WithTags", format.TypeDelta, format.TypeGorilla, 100, true},

		{"Medium_Raw-Raw_NoTags", format.TypeRaw, format.TypeRaw, 1000, false},
		{"Medium_Raw-Raw_WithTags", format.TypeRaw, format.TypeRaw, 1000, true},
		{"Medium_Delta-Gorilla_NoTags", format.TypeDelta, format.TypeGorilla, 1000, false},
		{"Medium_Delta-Gorilla_WithTags", format.TypeDelta, format.TypeGorilla, 1000, true},

		{"Large_Raw-Raw_NoTags", format.TypeRaw, format.TypeRaw, 5000, false},
		{"Large_Raw-Raw_WithTags", format.TypeRaw, format.TypeRaw, 5000, true},
		{"Large_Delta-Gorilla_NoTags", format.TypeDelta, format.TypeGorilla, 5000, false},
		{"Large_Delta-Gorilla_WithTags", format.TypeDelta, format.TypeGorilla, 5000, true},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Create test blob with one metric
			blob := createBenchmarkBlob(1, tc.points, tc.tsEnc, tc.valEnc, tc.withTags)
			metricID := blob.MetricIDs()[0]

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, _ = blob.MaterializeMetric(metricID)
			}
		})
	}
}

// createBenchmarkBlob creates a blob for benchmarking with specified characteristics
func createBenchmarkBlob(metrics, pointsEach int, tsEnc, valEnc format.EncodingType, withTags bool) NumericBlob {
	baseTime := time.Unix(1000000, 0).UTC()

	var opts []NumericEncoderOption
	if tsEnc != format.TypeRaw {
		opts = append(opts, WithTimestampEncoding(tsEnc))
	}
	if valEnc != format.TypeRaw {
		opts = append(opts, WithValueEncoding(valEnc))
	}
	if withTags {
		opts = append(opts, WithTagsEnabled(true))
	}

	encoder, err := NewNumericEncoder(baseTime, opts...)
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
			val := float64(100 + p)
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

	decoder, err := NewNumericDecoder(data)
	if err != nil {
		panic(err)
	}

	blob, err := decoder.Decode()
	if err != nil {
		panic(err)
	}

	return blob
}
