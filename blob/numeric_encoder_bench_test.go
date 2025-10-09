package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
)

// Benchmark AddFromRows with large metrics (testing batching mechanism)
func BenchmarkAddFromRows_LargeMetrics(b *testing.B) {
	type DataPoint struct {
		Timestamp int64
		Value     float64
		Tag       string
	}

	testCases := []struct {
		name   string
		size   int
		hasTag bool
	}{
		{"500points_WithTags", 500, true},
		{"512points_WithTags", 512, true},
		{"1000points_WithTags", 1000, true},
		{"2000points_WithTags", 2000, true},
		{"500points_NoTags", 500, false},
		{"1000points_NoTags", 1000, false},
		{"2000points_NoTags", 2000, false},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Create test data once
			data := make([]DataPoint, tc.size)
			for i := 0; i < tc.size; i++ {
				data[i] = DataPoint{
					Timestamp: int64(i * 1000),
					Value:     float64(i) + 0.5,
					Tag:       "host=server1",
				}
			}

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				var encoder *NumericEncoder
				var err error
				if tc.hasTag {
					encoder, _ = NewNumericEncoder(time.Now(), WithTagsEnabled(true))
				} else {
					encoder, _ = NewNumericEncoder(time.Now())
				}

				_ = encoder.StartMetricID(1, tc.size)

				if tc.hasTag {
					err = AddFromRows(encoder, data, func(dp DataPoint) (int64, float64, string) {
						return dp.Timestamp, dp.Value, dp.Tag
					})
				} else {
					err = AddFromRowsNoTag(encoder, data, func(dp DataPoint) (int64, float64) {
						return dp.Timestamp, dp.Value
					})
				}
				if err != nil {
					b.Fatal(err)
				}

				_ = encoder.EndMetric()
				_, _ = encoder.Finish()
			}
		})
	}
}

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
							_ = encoder.AddDataPoint(int64(p*1000), float64(p)+0.5, "host=server1")
						}
						_ = encoder.EndMetric()
					}

					_, _ = encoder.Finish()
				}
			})
		})
	}
}

// Benchmark AddFromRows with multiple metrics in same blob to demonstrate slice caching benefit
func BenchmarkAddFromRows_MultipleMetrics(b *testing.B) {
	type DataPoint struct {
		Timestamp int64
		Value     float64
		Tag       string
	}

	testCases := []struct {
		name            string
		metricCount     int
		pointsPerMetric int
	}{
		{"10metrics_100points", 10, 100},
		{"50metrics_100points", 50, 100},
		{"100metrics_100points", 100, 100},
		{"150metrics_10points", 150, 10},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Create test data once
			allMetrics := make([][]DataPoint, tc.metricCount)
			for m := 0; m < tc.metricCount; m++ {
				points := make([]DataPoint, tc.pointsPerMetric)
				for p := 0; p < tc.pointsPerMetric; p++ {
					points[p] = DataPoint{
						Timestamp: int64(p * 1000),
						Value:     float64(m*1000 + p),
						Tag:       "host=server1",
					}
				}
				allMetrics[m] = points
			}

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				encoder, _ := NewNumericEncoder(time.Now(), WithTagsEnabled(true))

				// Encode all metrics using AddFromRows
				// This demonstrates the benefit of slice caching across multiple metrics
				for m := 0; m < tc.metricCount; m++ {
					_ = encoder.StartMetricID(uint64(m+1), tc.pointsPerMetric)
					_ = AddFromRows(encoder, allMetrics[m], func(dp DataPoint) (int64, float64, string) {
						return dp.Timestamp, dp.Value, dp.Tag
					})
					_ = encoder.EndMetric()
				}

				_, _ = encoder.Finish()
			}
		})
	}
}

// Benchmark comparison: AddFromRows vs manual loop for multiple metrics
func BenchmarkAddFromRows_vs_ManualLoop_MultipleMetrics(b *testing.B) {
	type DataPoint struct {
		Timestamp int64
		Value     float64
		Tag       string
	}

	metricCount := 100
	pointsPerMetric := 100

	// Create test data once
	allMetrics := make([][]DataPoint, metricCount)
	for m := 0; m < metricCount; m++ {
		points := make([]DataPoint, pointsPerMetric)
		for p := 0; p < pointsPerMetric; p++ {
			points[p] = DataPoint{
				Timestamp: int64(p * 1000),
				Value:     float64(m*1000 + p),
				Tag:       "host=server1",
			}
		}
		allMetrics[m] = points
	}

	b.Run("AddFromRows", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			encoder, _ := NewNumericEncoder(time.Now(), WithTagsEnabled(true))

			for m := 0; m < metricCount; m++ {
				_ = encoder.StartMetricID(uint64(m+1), pointsPerMetric)
				_ = AddFromRows(encoder, allMetrics[m], func(dp DataPoint) (int64, float64, string) {
					return dp.Timestamp, dp.Value, dp.Tag
				})
				_ = encoder.EndMetric()
			}

			_, _ = encoder.Finish()
		}
	})

	b.Run("ManualLoop", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			encoder, _ := NewNumericEncoder(time.Now(), WithTagsEnabled(true))

			for m := 0; m < metricCount; m++ {
				_ = encoder.StartMetricID(uint64(m+1), pointsPerMetric)
				for _, dp := range allMetrics[m] {
					_ = encoder.AddDataPoint(dp.Timestamp, dp.Value, dp.Tag)
				}
				_ = encoder.EndMetric()
			}

			_, _ = encoder.Finish()
		}
	})
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

// ============================================================================
// AddFromRows Benchmarks (consolidated from numeric_encoder_rows_bench_test.go)
// ============================================================================

// Benchmark data types
type benchMeasurement struct {
	Timestamp time.Time
	Value     float64
	Tag       string
}

type benchDataPoint struct {
	TS  int64
	Val float64
}

// ==============================================================================
// AddFromRows vs Manual Loop Benchmarks (With Tags)
// ==============================================================================

func BenchmarkAddFromRows_10Points(b *testing.B) {
	rows := generateMeasurements(10)
	benchmarkAddFromRows(b, rows)
}

func BenchmarkManualLoop_10Points(b *testing.B) {
	rows := generateMeasurements(10)
	benchmarkManualLoop(b, rows)
}

func BenchmarkAddFromRows_100Points(b *testing.B) {
	rows := generateMeasurements(100)
	benchmarkAddFromRows(b, rows)
}

func BenchmarkManualLoop_100Points(b *testing.B) {
	rows := generateMeasurements(100)
	benchmarkManualLoop(b, rows)
}

func BenchmarkAddFromRows_1000Points(b *testing.B) {
	rows := generateMeasurements(1000)
	benchmarkAddFromRows(b, rows)
}

func BenchmarkManualLoop_1000Points(b *testing.B) {
	rows := generateMeasurements(1000)
	benchmarkManualLoop(b, rows)
}

// ==============================================================================
// AddFromRowsNoTag vs Manual Loop Benchmarks (Without Tags)
// ==============================================================================

func BenchmarkAddFromRowsNoTag_10Points(b *testing.B) {
	rows := generateDataPoints(10)
	benchmarkAddFromRowsNoTag(b, rows)
}

func BenchmarkManualLoopNoTag_10Points(b *testing.B) {
	rows := generateDataPoints(10)
	benchmarkManualLoopNoTag(b, rows)
}

func BenchmarkAddFromRowsNoTag_100Points(b *testing.B) {
	rows := generateDataPoints(100)
	benchmarkAddFromRowsNoTag(b, rows)
}

func BenchmarkManualLoopNoTag_100Points(b *testing.B) {
	rows := generateDataPoints(100)
	benchmarkManualLoopNoTag(b, rows)
}

func BenchmarkAddFromRowsNoTag_1000Points(b *testing.B) {
	rows := generateDataPoints(1000)
	benchmarkAddFromRowsNoTag(b, rows)
}

func BenchmarkManualLoopNoTag_1000Points(b *testing.B) {
	rows := generateDataPoints(1000)
	benchmarkManualLoopNoTag(b, rows)
}

// ==============================================================================
// Helper Functions
// ==============================================================================

func generateMeasurements(count int) []benchMeasurement {
	rows := make([]benchMeasurement, count)
	baseTS := time.Now()
	for i := range count {
		rows[i] = benchMeasurement{
			Timestamp: baseTS.Add(time.Duration(i) * time.Second),
			Value:     float64(i) * 1.5,
			Tag:       "host=server1",
		}
	}

	return rows
}

func generateDataPoints(count int) []benchDataPoint {
	rows := make([]benchDataPoint, count)
	baseTS := time.Now().UnixMicro()
	for i := range count {
		rows[i] = benchDataPoint{
			TS:  baseTS + int64(i)*1000000,
			Val: float64(i) * 1.5,
		}
	}

	return rows
}

// ==============================================================================
// Benchmark Implementations - With Tags
// ==============================================================================

func benchmarkAddFromRows(b *testing.B, rows []benchMeasurement) {
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		encoder, _ := NewNumericEncoder(time.Now())
		_ = encoder.StartMetricName("test.metric", len(rows))

		_ = AddFromRows(encoder, rows, func(m benchMeasurement) (int64, float64, string) {
			return m.Timestamp.UnixMicro(), m.Value, m.Tag
		})

		_ = encoder.EndMetric()
		_, _ = encoder.Finish()
	}
}

func benchmarkManualLoop(b *testing.B, rows []benchMeasurement) {
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		encoder, _ := NewNumericEncoder(time.Now())
		_ = encoder.StartMetricName("test.metric", len(rows))

		for _, row := range rows {
			_ = encoder.AddDataPoint(row.Timestamp.UnixMicro(), row.Value, row.Tag)
		}

		_ = encoder.EndMetric()
		_, _ = encoder.Finish()
	}
}

// ==============================================================================
// Benchmark Implementations - Without Tags
// ==============================================================================

func benchmarkAddFromRowsNoTag(b *testing.B, rows []benchDataPoint) {
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		encoder, _ := NewNumericEncoder(time.Now())
		_ = encoder.StartMetricName("test.metric", len(rows))

		_ = AddFromRowsNoTag(encoder, rows, func(p benchDataPoint) (int64, float64) {
			return p.TS, p.Val
		})

		_ = encoder.EndMetric()
		_, _ = encoder.Finish()
	}
}

func benchmarkManualLoopNoTag(b *testing.B, rows []benchDataPoint) {
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		encoder, _ := NewNumericEncoder(time.Now())
		_ = encoder.StartMetricName("test.metric", len(rows))

		for _, row := range rows {
			_ = encoder.AddDataPoint(row.TS, row.Val, "")
		}

		_ = encoder.EndMetric()
		_, _ = encoder.Finish()
	}
}
