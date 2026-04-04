package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
)

type sharedTimestampBenchmarkScenario struct {
	name               string
	metricCount        int
	pointsPerMetric    int
	compressionEnabled bool
}

type mixedSharedTimestampBenchmarkScenario struct {
	name               string
	metricCount        int
	pointsPerMetric    int
	sharedGroupCount   int
	uniqueMetricCount  int
	compressionEnabled bool
}

// BenchmarkSharedTimestamps_EncodedSize reports end-to-end blob size deltas
// between the default V1 encoding and opt-in V2 shared timestamp encoding.
func BenchmarkSharedTimestamps_EncodedSize(b *testing.B) {
	scenarios := []sharedTimestampBenchmarkScenario{
		{name: "150metrics_10points_DefaultCompression", metricCount: 150, pointsPerMetric: 10, compressionEnabled: true},
		{name: "150metrics_10points_NoCompression", metricCount: 150, pointsPerMetric: 10, compressionEnabled: false},
		{name: "150metrics_100points_DefaultCompression", metricCount: 150, pointsPerMetric: 100, compressionEnabled: true},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			v1Data := createSharedTimestampBenchmarkData(b, sc, false)
			v2Data := createSharedTimestampBenchmarkData(b, sc, true)

			savedBytes := len(v1Data) - len(v2Data)
			savedPct := 0.0
			if len(v1Data) > 0 {
				savedPct = float64(savedBytes) * 100 / float64(len(v1Data))
			}

			b.ResetTimer()
			for b.Loop() {
				_ = savedBytes
			}

			b.ReportMetric(float64(len(v1Data)), "v1-bytes")
			b.ReportMetric(float64(len(v2Data)), "v2-bytes")
			b.ReportMetric(float64(savedBytes), "saved-bytes")
			b.ReportMetric(savedPct, "saved-pct")
		})
	}
}

// BenchmarkSharedTimestamps_Encode benchmarks the encoder on repeated-timestamp
// workloads with and without the shared timestamp optimization enabled.
func BenchmarkSharedTimestamps_Encode(b *testing.B) {
	scenarios := []sharedTimestampBenchmarkScenario{
		{name: "150metrics_10points_DefaultCompression", metricCount: 150, pointsPerMetric: 10, compressionEnabled: true},
		{name: "150metrics_10points_NoCompression", metricCount: 150, pointsPerMetric: 10, compressionEnabled: false},
		{name: "150metrics_100points_DefaultCompression", metricCount: 150, pointsPerMetric: 100, compressionEnabled: true},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			for _, sharedEnabled := range []bool{false, true} {
				modeName := "V1_Default"
				if sharedEnabled {
					modeName = "V2_SharedTimestamps"
				}

				b.Run(modeName, func(b *testing.B) {
					sample := createSharedTimestampBenchmarkData(b, sc, sharedEnabled)
					b.ReportAllocs()
					b.SetBytes(int64(len(sample)))
					b.ResetTimer()

					for b.Loop() {
						_ = createSharedTimestampBenchmarkData(b, sc, sharedEnabled)
					}

					b.ReportMetric(float64(len(sample)), "blob-bytes")
				})
			}
		})
	}
}

// BenchmarkSharedTimestamps_Mixed_EncodedSize reports blob size deltas for a
// workload where some metrics share timestamps in groups and others remain unique.
func BenchmarkSharedTimestamps_Mixed_EncodedSize(b *testing.B) {
	scenarios := []mixedSharedTimestampBenchmarkScenario{
		{name: "150metrics_10points_3groups_30unique_DefaultCompression", metricCount: 150, pointsPerMetric: 10, sharedGroupCount: 3, uniqueMetricCount: 30, compressionEnabled: true},
		{name: "150metrics_10points_3groups_30unique_NoCompression", metricCount: 150, pointsPerMetric: 10, sharedGroupCount: 3, uniqueMetricCount: 30, compressionEnabled: false},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			v1Data := createMixedSharedTimestampBenchmarkData(b, sc, false)
			v2Data := createMixedSharedTimestampBenchmarkData(b, sc, true)

			savedBytes := len(v1Data) - len(v2Data)
			savedPct := 0.0
			if len(v1Data) > 0 {
				savedPct = float64(savedBytes) * 100 / float64(len(v1Data))
			}

			b.ResetTimer()
			for b.Loop() {
				_ = savedBytes
			}

			b.ReportMetric(float64(len(v1Data)), "v1-bytes")
			b.ReportMetric(float64(len(v2Data)), "v2-bytes")
			b.ReportMetric(float64(savedBytes), "saved-bytes")
			b.ReportMetric(savedPct, "saved-pct")
		})
	}
}

// BenchmarkSharedTimestamps_Mixed_Encode benchmarks encoding on mixed
// shared/unique timestamp workloads.
func BenchmarkSharedTimestamps_Mixed_Encode(b *testing.B) {
	scenarios := []mixedSharedTimestampBenchmarkScenario{
		{name: "150metrics_10points_3groups_30unique_DefaultCompression", metricCount: 150, pointsPerMetric: 10, sharedGroupCount: 3, uniqueMetricCount: 30, compressionEnabled: true},
		{name: "150metrics_10points_3groups_30unique_NoCompression", metricCount: 150, pointsPerMetric: 10, sharedGroupCount: 3, uniqueMetricCount: 30, compressionEnabled: false},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			for _, sharedEnabled := range []bool{false, true} {
				modeName := "V1_Default"
				if sharedEnabled {
					modeName = "V2_SharedTimestamps"
				}

				b.Run(modeName, func(b *testing.B) {
					sample := createMixedSharedTimestampBenchmarkData(b, sc, sharedEnabled)
					b.ReportAllocs()
					b.SetBytes(int64(len(sample)))
					b.ResetTimer()

					for b.Loop() {
						_ = createMixedSharedTimestampBenchmarkData(b, sc, sharedEnabled)
					}

					b.ReportMetric(float64(len(sample)), "blob-bytes")
				})
			}
		})
	}
}

func createSharedTimestampBenchmarkData(tb testing.TB, sc sharedTimestampBenchmarkScenario, sharedEnabled bool) []byte {
	tb.Helper()

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	options := makeSharedTimestampBenchmarkOptions(sc.compressionEnabled, sharedEnabled)

	encoder, err := NewNumericEncoder(startTime, options...)
	if err != nil {
		tb.Fatalf("failed to create encoder: %v", err)
	}

	for metricIdx := range sc.metricCount {
		err = encoder.StartMetricID(uint64(metricIdx+1), sc.pointsPerMetric)
		if err != nil {
			tb.Fatalf("failed to start metric %d: %v", metricIdx, err)
		}

		for pointIdx := range sc.pointsPerMetric {
			ts := startTime.Add(time.Duration(pointIdx) * time.Second).UnixMicro()
			value := float64(metricIdx*1000 + pointIdx)
			err = encoder.AddDataPoint(ts, value, "")
			if err != nil {
				tb.Fatalf("failed to add point %d for metric %d: %v", pointIdx, metricIdx, err)
			}
		}

		err = encoder.EndMetric()
		if err != nil {
			tb.Fatalf("failed to end metric %d: %v", metricIdx, err)
		}
	}

	data, err := encoder.Finish()
	if err != nil {
		tb.Fatalf("failed to finish encoding: %v", err)
	}

	return data
}

func createMixedSharedTimestampBenchmarkData(tb testing.TB, sc mixedSharedTimestampBenchmarkScenario, sharedEnabled bool) []byte {
	tb.Helper()

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	options := makeSharedTimestampBenchmarkOptions(sc.compressionEnabled, sharedEnabled)

	encoder, err := NewNumericEncoder(startTime, options...)
	if err != nil {
		tb.Fatalf("failed to create encoder: %v", err)
	}

	sharedMetricCount := sc.metricCount - sc.uniqueMetricCount
	if sharedMetricCount < 0 {
		tb.Fatalf("invalid mixed scenario: shared metric count cannot be negative")
	}
	if sharedMetricCount > 0 && sc.sharedGroupCount <= 0 {
		tb.Fatalf("invalid mixed scenario: sharedGroupCount must be > 0 when shared metrics exist")
	}

	for metricIdx := range sc.metricCount {
		err = encoder.StartMetricID(uint64(metricIdx+1), sc.pointsPerMetric)
		if err != nil {
			tb.Fatalf("failed to start metric %d: %v", metricIdx, err)
		}

		for pointIdx := range sc.pointsPerMetric {
			var ts int64
			if metricIdx < sharedMetricCount {
				groupIdx := metricIdx % sc.sharedGroupCount
				ts = startTime.Add(time.Duration(groupIdx*1000+pointIdx) * time.Second).UnixMicro()
			} else {
				uniqueIdx := metricIdx - sharedMetricCount
				ts = startTime.Add(time.Duration(10000+uniqueIdx*1000+pointIdx) * time.Second).UnixMicro()
			}

			value := float64(metricIdx*1000 + pointIdx)
			err = encoder.AddDataPoint(ts, value, "")
			if err != nil {
				tb.Fatalf("failed to add point %d for metric %d: %v", pointIdx, metricIdx, err)
			}
		}

		err = encoder.EndMetric()
		if err != nil {
			tb.Fatalf("failed to end metric %d: %v", metricIdx, err)
		}
	}

	data, err := encoder.Finish()
	if err != nil {
		tb.Fatalf("failed to finish encoding: %v", err)
	}

	return data
}

func makeSharedTimestampBenchmarkOptions(compressionEnabled, sharedEnabled bool) []NumericEncoderOption {
	options := []NumericEncoderOption{
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla),
	}
	if !compressionEnabled {
		options = append(options,
			WithTimestampCompression(format.CompressionNone),
			WithValueCompression(format.CompressionNone),
		)
	}
	if sharedEnabled {
		options = append(options, WithSharedTimestamps())
	}

	return options
}

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
	for m := range metricCount {
		points := make([]DataPoint, pointsPerMetric)
		for p := range pointsPerMetric {
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

			for m := range metricCount {
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

			for m := range metricCount {
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
	for m := range metricCount {
		_ = encoderDisabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := range pointsPerMetric {
			_ = encoderDisabled.AddDataPoint(int64(p*1000), float64(p)+0.5, "")
		}
		_ = encoderDisabled.EndMetric()
	}
	dataDisabled, _ := encoderDisabled.Finish()

	// Prepare data with tags enabled
	encoderEnabled, _ := NewNumericEncoder(startTime, WithTagsEnabled(true))
	for m := range metricCount {
		_ = encoderEnabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := range pointsPerMetric {
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
	for m := range metricCount {
		_ = encoderDisabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := range pointsPerMetric {
			_ = encoderDisabled.AddDataPoint(int64(p*1000), float64(p)+0.5, "")
		}
		_ = encoderDisabled.EndMetric()
	}
	dataDisabled, _ := encoderDisabled.Finish()
	decoderDisabled, _ := NewNumericDecoder(dataDisabled)
	blobDisabled, _ := decoderDisabled.Decode()

	// Prepare blob with tags enabled
	encoderEnabled, _ := NewNumericEncoder(startTime, WithTagsEnabled(true))
	for m := range metricCount {
		_ = encoderEnabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := range pointsPerMetric {
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

			for m := range metricCount {
				_ = encoder.StartMetricID(uint64(m+1), pointsPerMetric)
				for p := range pointsPerMetric {
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

			for m := range metricCount {
				_ = encoder.StartMetricID(uint64(m+1), pointsPerMetric)
				for p := range pointsPerMetric {
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
	for m := range metricCount {
		_ = encoderDisabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := range pointsPerMetric {
			_ = encoderDisabled.AddDataPoint(int64(p*1000), float64(p)+0.5, "")
		}
		_ = encoderDisabled.EndMetric()
	}
	dataDisabled, _ := encoderDisabled.Finish()

	// Measure with tags enabled (empty tags)
	encoderEnabled, _ := NewNumericEncoder(startTime, WithTagsEnabled(true))
	for m := range metricCount {
		_ = encoderEnabled.StartMetricID(uint64(m+1), pointsPerMetric)
		for p := range pointsPerMetric {
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

// ==============================================================================
// V1 vs V2 Layout Encode Benchmarks
// ==============================================================================

// BenchmarkV2Layout_Encode compares encoding performance between V1, V2, and V2+SharedTS.
// V2 has extra sorting overhead at Finish() time.
func BenchmarkV2Layout_Encode(b *testing.B) {
	sizes := []struct {
		name    string
		metrics int
		points  int
	}{
		{"10metrics_10points", 10, 10},
		{"150metrics_10points", 150, 10},
		{"500metrics_10points", 500, 10},
		{"150metrics_100points", 150, 100},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

			for _, cfg := range layoutConfigs() {
				b.Run(cfg.label, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						opts := append([]NumericEncoderOption{
							WithTimestampEncoding(format.TypeDelta),
							WithValueEncoding(format.TypeGorilla),
						}, cfg.opts...)
						enc, _ := NewNumericEncoder(startTime, opts...)
						for i := sz.metrics; i >= 1; i-- {
							_ = enc.StartMetricID(uint64(i), sz.points)
							for j := range sz.points {
								ts := startTime.Add(time.Duration(j) * time.Second).UnixMicro()
								_ = enc.AddDataPoint(ts, float64(i*1000+j), "")
							}
							_ = enc.EndMetric()
						}
						_, _ = enc.Finish()
					}
				})
			}
		})
	}
}
