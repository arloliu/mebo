package blob

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
)

// ==============================================================================
// Benchmark: Pure Numeric - Same Metrics Across Blobs (Best Case)
// ==============================================================================

func BenchmarkBlobSet_SameMetrics_Sequential(b *testing.B) {
	benchmarks := []struct {
		name            string
		numBlobs        int
		metricsPerBlob  int
		pointsPerMetric int
	}{
		// Small scale
		{"2Blobs_10Metrics_100Points", 2, 10, 100},
		{"2Blobs_100Metrics_100Points", 2, 100, 100},
		{"2Blobs_200Metrics_100Points", 2, 200, 100},
		{"2Blobs_10Metrics_1000Points", 2, 10, 1000},
		{"2Blobs_100Metrics_1000Points", 2, 100, 1000},

		// Medium scale
		{"5Blobs_10Metrics_100Points", 5, 10, 100},
		{"5Blobs_100Metrics_100Points", 5, 100, 100},
		{"5Blobs_200Metrics_100Points", 5, 200, 100},
		{"5Blobs_10Metrics_1000Points", 5, 10, 1000},
		{"5Blobs_100Metrics_1000Points", 5, 100, 1000},

		// Large scale
		{"10Blobs_10Metrics_100Points", 10, 10, 100},
		{"10Blobs_100Metrics_100Points", 10, 100, 100},
		{"10Blobs_200Metrics_100Points", 10, 200, 100},
		{"10Blobs_10Metrics_1000Points", 10, 10, 1000},
		{"10Blobs_100Metrics_1000Points", 10, 100, 1000},

		// Extra large scale
		{"20Blobs_10Metrics_100Points", 20, 10, 100},
		{"20Blobs_100Metrics_100Points", 20, 100, 100},
		{"20Blobs_200Metrics_100Points", 20, 200, 100},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			// Create blobs with same metrics across all blobs
			blobs, err := createBlobsWithSameMetrics(
				bm.numBlobs,
				bm.metricsPerBlob,
				bm.pointsPerMetric,
			)
			if err != nil {
				b.Fatalf("Failed to create blobs: %v", err)
			}

			set := NewBlobSet(blobs, nil)

			// Get first metric ID for testing
			metricIDs := blobs[0].MetricIDs()
			if len(metricIDs) == 0 {
				b.Fatal("No metrics found")
			}
			testMetricID := metricIDs[0]

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				count := 0
				for _, point := range set.AllNumerics(testMetricID) {
					count++
					_ = point.Val
				}
				if count == 0 {
					b.Fatal("No points iterated")
				}
			}
		})
	}
}

func BenchmarkBlobSet_SameMetrics_RandomAccess(b *testing.B) {
	benchmarks := []struct {
		name            string
		numBlobs        int
		metricsPerBlob  int
		pointsPerMetric int
	}{
		{"2Blobs_10Metrics_100Points", 2, 10, 100},
		{"5Blobs_100Metrics_100Points", 5, 100, 100},
		{"10Blobs_200Metrics_100Points", 10, 200, 100},
		{"20Blobs_100Metrics_1000Points", 20, 100, 1000},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			blobs, err := createBlobsWithSameMetrics(
				bm.numBlobs,
				bm.metricsPerBlob,
				bm.pointsPerMetric,
			)
			if err != nil {
				b.Fatalf("Failed to create blobs: %v", err)
			}

			set := NewBlobSet(blobs, nil)

			metricIDs := blobs[0].MetricIDs()
			if len(metricIDs) == 0 {
				b.Fatal("No metrics found")
			}
			testMetricID := metricIDs[0]

			// Calculate total points across all blobs
			totalPoints := bm.numBlobs * bm.pointsPerMetric

			// Test access at different positions across blobs
			indices := []int{0, totalPoints / 2, totalPoints - 1}

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				for _, idx := range indices {
					point, ok := set.NumericAt(testMetricID, idx)
					if !ok {
						b.Fatalf("Failed to access index %d (total points: %d)", idx, totalPoints)
					}
					_ = point.Val
				}
			}
		})
	}
}

// ==============================================================================
// Benchmark: Pure Numeric - Different Metrics Per Blob (Worst Case)
// ==============================================================================

func BenchmarkBlobSet_UniqueMetrics_Sequential(b *testing.B) {
	benchmarks := []struct {
		name            string
		numBlobs        int
		metricsPerBlob  int
		pointsPerMetric int
	}{
		{"2Blobs_10Metrics_100Points", 2, 10, 100},
		{"5Blobs_100Metrics_100Points", 5, 100, 100},
		{"10Blobs_200Metrics_100Points", 10, 200, 100},
		{"20Blobs_100Metrics_1000Points", 20, 100, 1000},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			// Create blobs with unique metrics per blob
			blobs, err := createBlobsWithUniqueMetrics(
				bm.numBlobs,
				bm.metricsPerBlob,
				bm.pointsPerMetric,
			)
			if err != nil {
				b.Fatalf("Failed to create blobs: %v", err)
			}

			set := NewBlobSet(blobs, nil)

			// Get first metric ID from first blob
			metricIDs := blobs[0].MetricIDs()
			if len(metricIDs) == 0 {
				b.Fatal("No metrics found")
			}
			testMetricID := metricIDs[0]

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				count := 0
				for _, point := range set.AllNumerics(testMetricID) {
					count++
					_ = point.Val
				}
				if count == 0 {
					b.Fatal("No points iterated")
				}
			}
		})
	}
}

func BenchmarkBlobSet_UniqueMetrics_RandomAccess(b *testing.B) {
	benchmarks := []struct {
		name            string
		numBlobs        int
		metricsPerBlob  int
		pointsPerMetric int
	}{
		{"2Blobs_10Metrics_100Points", 2, 10, 100},
		{"5Blobs_100Metrics_100Points", 5, 100, 100},
		{"10Blobs_200Metrics_100Points", 10, 200, 100},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			blobs, err := createBlobsWithUniqueMetrics(
				bm.numBlobs,
				bm.metricsPerBlob,
				bm.pointsPerMetric,
			)
			if err != nil {
				b.Fatalf("Failed to create blobs: %v", err)
			}

			set := NewBlobSet(blobs, nil)

			metricIDs := blobs[0].MetricIDs()
			if len(metricIDs) == 0 {
				b.Fatal("No metrics found")
			}
			testMetricID := metricIDs[0]

			// For unique metrics, each metric only appears in one blob
			// So we only have pointsPerMetric points per metric
			totalPoints := bm.pointsPerMetric
			indices := []int{0, totalPoints / 2, totalPoints - 1}

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				for _, idx := range indices {
					point, ok := set.NumericAt(testMetricID, idx)
					if !ok {
						b.Fatalf("Failed to access index %d (total points: %d)", idx, totalPoints)
					}
					_ = point.Val
				}
			}
		})
	}
}

// ==============================================================================
// Benchmark: Pure Numeric - Overlapping Metrics (Realistic Case)
// ==============================================================================

func BenchmarkBlobSet_OverlappingMetrics_Sequential(b *testing.B) {
	benchmarks := []struct {
		name            string
		numBlobs        int
		metricsPerBlob  int
		pointsPerMetric int
		overlapPercent  float64
	}{
		{"5Blobs_100Metrics_50%Overlap", 5, 100, 100, 0.5},
		{"10Blobs_200Metrics_50%Overlap", 10, 200, 100, 0.5},
		{"10Blobs_100Metrics_75%Overlap", 10, 100, 100, 0.75},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			blobs, err := createBlobsWithOverlap(
				bm.numBlobs,
				bm.metricsPerBlob,
				bm.pointsPerMetric,
				bm.overlapPercent,
			)
			if err != nil {
				b.Fatalf("Failed to create blobs: %v", err)
			}

			set := NewBlobSet(blobs, nil)

			// Get a metric that exists in multiple blobs
			metricIDs := blobs[0].MetricIDs()
			if len(metricIDs) == 0 {
				b.Fatal("No metrics found")
			}
			testMetricID := metricIDs[0]

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				count := 0
				for _, point := range set.AllNumerics(testMetricID) {
					count++
					_ = point.Val
				}
			}
		})
	}
}

// ==============================================================================
// Benchmark: Mixed Numeric/Text Blobs
// ==============================================================================

func BenchmarkBlobSet_Mixed90_10_TypeSpecific(b *testing.B) {
	benchmarks := []struct {
		name       string
		numBlobs   int
		numNumeric int
		numText    int
		points     int
	}{
		{"5Blobs_90Numeric_10Text_100Points", 5, 90, 10, 100},
		{"10Blobs_90Numeric_10Text_100Points", 10, 90, 10, 100},
		{"10Blobs_180Numeric_20Text_100Points", 10, 180, 20, 100},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name+"_Numeric", func(b *testing.B) {
			numericBlobs, textBlobs, err := createMixedBlobs(
				bm.numBlobs,
				bm.numNumeric,
				bm.numText,
				bm.points,
			)
			if err != nil {
				b.Fatalf("Failed to create blobs: %v", err)
			}

			set := NewBlobSet(numericBlobs, textBlobs)

			metricIDs := numericBlobs[0].MetricIDs()
			if len(metricIDs) == 0 {
				b.Fatal("No metrics found")
			}
			testMetricID := metricIDs[0]

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				count := 0
				for _, point := range set.AllNumerics(testMetricID) {
					count++
					_ = point.Val
				}
			}
		})

		b.Run(bm.name+"_Text", func(b *testing.B) {
			numericBlobs, textBlobs, err := createMixedBlobs(
				bm.numBlobs,
				bm.numNumeric,
				bm.numText,
				bm.points,
			)
			if err != nil {
				b.Fatalf("Failed to create blobs: %v", err)
			}

			set := NewBlobSet(numericBlobs, textBlobs)

			metricIDs := textBlobs[0].MetricIDs()
			if len(metricIDs) == 0 {
				b.Fatal("No metrics found")
			}
			testMetricID := metricIDs[0]

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				count := 0
				for _, point := range set.AllTexts(testMetricID) {
					count++
					_ = point.Val
				}
			}
		})
	}
}

func BenchmarkBlobSet_Mixed95_5_TypeSpecific(b *testing.B) {
	benchmarks := []struct {
		name       string
		numBlobs   int
		numNumeric int
		numText    int
		points     int
	}{
		{"10Blobs_95Numeric_5Text_100Points", 10, 95, 5, 100},
		{"10Blobs_190Numeric_10Text_100Points", 10, 190, 10, 100},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name+"_Numeric", func(b *testing.B) {
			numericBlobs, textBlobs, err := createMixedBlobs(
				bm.numBlobs,
				bm.numNumeric,
				bm.numText,
				bm.points,
			)
			if err != nil {
				b.Fatalf("Failed to create blobs: %v", err)
			}

			set := NewBlobSet(numericBlobs, textBlobs)

			metricIDs := numericBlobs[0].MetricIDs()
			if len(metricIDs) == 0 {
				b.Fatal("No metrics found")
			}
			testMetricID := metricIDs[0]

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				count := 0
				for _, point := range set.AllNumerics(testMetricID) {
					count++
					_ = point.Val
				}
			}
		})
	}
}

// ==============================================================================
// Benchmark: Sequential Iteration Operations
// ==============================================================================

func BenchmarkBlobSet_Iteration_FullScan(b *testing.B) {
	const (
		numBlobs        = 10
		metricsPerBlob  = 100
		pointsPerMetric = 100
	)

	blobs, err := createBlobsWithSameMetrics(numBlobs, metricsPerBlob, pointsPerMetric)
	if err != nil {
		b.Fatalf("Failed to create blobs: %v", err)
	}

	set := NewBlobSet(blobs, nil)

	metricIDs := blobs[0].MetricIDs()
	if len(metricIDs) == 0 {
		b.Fatal("No metrics found")
	}
	testMetricID := metricIDs[0]

	b.Run("AllNumerics", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			count := 0
			for _, point := range set.AllNumerics(testMetricID) {
				count++
				_ = point.Val
			}
		}
	})

	b.Run("AllNumericValues", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			count := 0
			for _, val := range set.AllNumericValues(testMetricID) {
				count++
				_ = val
			}
		}
	})

	b.Run("AllTimestamps", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			count := 0
			for _, ts := range set.AllTimestamps(testMetricID) {
				count++
				_ = ts
			}
		}
	})
}

func BenchmarkBlobSet_Iteration_EarlyTermination(b *testing.B) {
	const (
		numBlobs        = 10
		metricsPerBlob  = 100
		pointsPerMetric = 1000
	)

	blobs, err := createBlobsWithSameMetrics(numBlobs, metricsPerBlob, pointsPerMetric)
	if err != nil {
		b.Fatalf("Failed to create blobs: %v", err)
	}

	set := NewBlobSet(blobs, nil)

	metricIDs := blobs[0].MetricIDs()
	if len(metricIDs) == 0 {
		b.Fatal("No metrics found")
	}
	testMetricID := metricIDs[0]

	terminationPoints := []struct {
		name  string
		limit int
	}{
		{"First10", 10},
		{"First100", 100},
		{"First1000", 1000},
	}

	for _, tp := range terminationPoints {
		b.Run(tp.name, func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				count := 0
				for _, point := range set.AllNumerics(testMetricID) {
					count++
					_ = point.Val
					if count >= tp.limit {
						break
					}
				}
			}
		})
	}
}

// ==============================================================================
// Benchmark: Random Access Patterns
// ==============================================================================

func BenchmarkBlobSet_Access_Sequential(b *testing.B) {
	const (
		numBlobs        = 10
		metricsPerBlob  = 100
		pointsPerMetric = 1000
	)

	blobs, err := createBlobsWithSameMetrics(numBlobs, metricsPerBlob, pointsPerMetric)
	if err != nil {
		b.Fatalf("Failed to create blobs: %v", err)
	}

	set := NewBlobSet(blobs, nil)

	metricIDs := blobs[0].MetricIDs()
	if len(metricIDs) == 0 {
		b.Fatal("No metrics found")
	}
	testMetricID := metricIDs[0]

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Access first 100 indices sequentially
		for i := range 100 {
			point, ok := set.NumericAt(testMetricID, i)
			if !ok {
				b.Fatalf("Failed to access index %d", i)
			}
			_ = point.Val
		}
	}
}

func BenchmarkBlobSet_Access_Random(b *testing.B) {
	const (
		numBlobs        = 10
		metricsPerBlob  = 100
		pointsPerMetric = 1000
	)

	blobs, err := createBlobsWithSameMetrics(numBlobs, metricsPerBlob, pointsPerMetric)
	if err != nil {
		b.Fatalf("Failed to create blobs: %v", err)
	}

	set := NewBlobSet(blobs, nil)

	metricIDs := blobs[0].MetricIDs()
	if len(metricIDs) == 0 {
		b.Fatal("No metrics found")
	}
	testMetricID := metricIDs[0]

	// Pre-generate random indices
	indices := make([]int, 100)
	for i := range indices {
		indices[i] = rand.IntN(pointsPerMetric)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		for _, idx := range indices {
			point, ok := set.NumericAt(testMetricID, idx)
			if !ok {
				b.Fatalf("Failed to access index %d", idx)
			}
			_ = point.Val
		}
	}
}

func BenchmarkBlobSet_Access_Strided(b *testing.B) {
	const (
		numBlobs        = 10
		metricsPerBlob  = 100
		pointsPerMetric = 1000
	)

	blobs, err := createBlobsWithSameMetrics(numBlobs, metricsPerBlob, pointsPerMetric)
	if err != nil {
		b.Fatalf("Failed to create blobs: %v", err)
	}

	set := NewBlobSet(blobs, nil)

	metricIDs := blobs[0].MetricIDs()
	if len(metricIDs) == 0 {
		b.Fatal("No metrics found")
	}
	testMetricID := metricIDs[0]

	strides := []struct {
		name   string
		stride int
	}{
		{"Stride10", 10},
		{"Stride50", 50},
		{"Stride100", 100},
	}

	for _, st := range strides {
		b.Run(st.name, func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				for i := 0; i < pointsPerMetric; i += st.stride {
					point, ok := set.NumericAt(testMetricID, i)
					if !ok {
						b.Fatalf("Failed to access index %d", i)
					}
					_ = point.Val
				}
			}
		})
	}
}

// ==============================================================================
// Benchmark: Lookup Performance - ByID vs ByName
// ==============================================================================

func BenchmarkBlobSet_Lookup_ByID_vs_ByName(b *testing.B) {
	const (
		numBlobs        = 10
		metricsPerBlob  = 100
		pointsPerMetric = 100
	)

	// Create blobs using metric names so we can test both ID and name lookups
	blobs := make([]NumericBlob, numBlobs)
	baseTime := time.Now()
	testMetricName := generateMetricName("shared", 0)
	testMetricID := hash.ID(testMetricName)

	for blobIdx := range numBlobs {
		startTime := baseTime.Add(time.Duration(blobIdx) * time.Hour)
		encoder, err := NewNumericEncoder(
			startTime,
			WithTimestampEncoding(format.TypeDelta),
			WithValueEncoding(format.TypeGorilla),
			WithTagsEnabled(true),
		)
		if err != nil {
			b.Fatalf("Failed to create encoder: %v", err)
		}

		for m := range metricsPerBlob {
			metricName := generateMetricName("shared", m)

			if err := encoder.StartMetricName(metricName, pointsPerMetric); err != nil {
				b.Fatalf("Failed to start metric: %v", err)
			}

			for i := range pointsPerMetric {
				ts := int64(blobIdx*1000000 + i*1000)
				val := float64(i) + float64(m)*100
				if err := encoder.AddDataPoint(ts, val, ""); err != nil {
					b.Fatalf("Failed to add data point: %v", err)
				}
			}

			if err := encoder.EndMetric(); err != nil {
				b.Fatalf("Failed to end metric: %v", err)
			}
		}

		blobBytes, err := encoder.Finish()
		if err != nil {
			b.Fatalf("Failed to finish: %v", err)
		}

		decoder, err := NewNumericDecoder(blobBytes)
		if err != nil {
			b.Fatalf("Failed to create decoder: %v", err)
		}

		blob, err := decoder.Decode()
		if err != nil {
			b.Fatalf("Failed to decode: %v", err)
		}

		blobs[blobIdx] = blob
	}

	set := NewBlobSet(blobs, nil)

	b.Run("ByID", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			count := 0
			for _, point := range set.AllNumerics(testMetricID) {
				count++
				_ = point.Val
			}
		}
	})

	b.Run("ByName", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			count := 0
			for _, point := range set.AllNumericsByName(testMetricName) {
				count++
				_ = point.Val
			}
		}
	})
}

// ==============================================================================
// Benchmark: Global Indexing Overhead
// ==============================================================================

func BenchmarkBlobSet_GlobalIndexing_Overhead(b *testing.B) {
	const (
		metricsPerBlob  = 100
		pointsPerMetric = 100
	)

	blobCounts := []int{1, 2, 5, 10, 20}

	for _, numBlobs := range blobCounts {
		b.Run("Blobs_"+string(rune('0'+numBlobs)), func(b *testing.B) {
			blobs, err := createBlobsWithSameMetrics(numBlobs, metricsPerBlob, pointsPerMetric)
			if err != nil {
				b.Fatalf("Failed to create blobs: %v", err)
			}

			set := NewBlobSet(blobs, nil)

			metricIDs := blobs[0].MetricIDs()
			if len(metricIDs) == 0 {
				b.Fatal("No metrics found")
			}
			testMetricID := metricIDs[0]

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				// Access middle point to test global indexing
				point, ok := set.NumericAt(testMetricID, pointsPerMetric/2)
				if !ok {
					b.Fatal("Failed to access point")
				}
				_ = point.Val
			}
		})
	}
}

// ==============================================================================
// Benchmark: Realistic Workload Simulations
// ==============================================================================

func BenchmarkBlobSet_RealWorld_Dashboard(b *testing.B) {
	// Simulate dashboard: 10 metrics × 100 points across 5 blobs
	const (
		numBlobs        = 5
		numMetrics      = 10
		pointsPerMetric = 100
	)

	blobs, err := createBlobsWithSameMetrics(numBlobs, numMetrics, pointsPerMetric)
	if err != nil {
		b.Fatalf("Failed to create blobs: %v", err)
	}

	set := NewBlobSet(blobs, nil)

	metricIDs := blobs[0].MetricIDs()
	if len(metricIDs) < numMetrics {
		b.Fatal("Not enough metrics")
	}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Query 10 different metrics
		for i := range numMetrics {
			metricID := metricIDs[i]
			count := 0
			for _, point := range set.AllNumerics(metricID) {
				count++
				_ = point.Val
			}
		}
	}
}

func BenchmarkBlobSet_RealWorld_Alert(b *testing.B) {
	// Simulate alert: Check single metric across all blobs
	const (
		numBlobs        = 20
		metricsPerBlob  = 100
		pointsPerMetric = 100
	)

	blobs, err := createBlobsWithSameMetrics(numBlobs, metricsPerBlob, pointsPerMetric)
	if err != nil {
		b.Fatalf("Failed to create blobs: %v", err)
	}

	set := NewBlobSet(blobs, nil)

	metricIDs := blobs[0].MetricIDs()
	if len(metricIDs) == 0 {
		b.Fatal("No metrics found")
	}
	testMetricID := metricIDs[0]

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Check if any value exceeds threshold
		threshold := 500.0
		found := false
		for _, val := range set.AllNumericValues(testMetricID) {
			if val > threshold {
				found = true
				break
			}
		}
		_ = found
	}
}

func BenchmarkBlobSet_RealWorld_Aggregation(b *testing.B) {
	// Simulate aggregation: Sum all values across blobs
	const (
		numBlobs        = 10
		metricsPerBlob  = 100
		pointsPerMetric = 100
	)

	blobs, err := createBlobsWithSameMetrics(numBlobs, metricsPerBlob, pointsPerMetric)
	if err != nil {
		b.Fatalf("Failed to create blobs: %v", err)
	}

	set := NewBlobSet(blobs, nil)

	metricIDs := blobs[0].MetricIDs()
	if len(metricIDs) == 0 {
		b.Fatal("No metrics found")
	}
	testMetricID := metricIDs[0]

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		sum := 0.0
		count := 0
		for _, val := range set.AllNumericValues(testMetricID) {
			sum += val
			count++
		}
		_ = sum / float64(count) // Average
	}
}

// ==============================================================================
// Helper Functions
// ==============================================================================

// generateMetricName creates a unique metric name using a prefix and index
func generateMetricName(prefix string, index int) string {
	// Use sprintf to ensure unique names without hash collisions
	return prefix + "_metric_" + string(rune('0'+(index/1000)%10)) +
		string(rune('0'+(index/100)%10)) +
		string(rune('0'+(index/10)%10)) +
		string(rune('0'+index%10))
}

// createBlobsWithSameMetrics creates blobs where all blobs contain the same metrics
func createBlobsWithSameMetrics(numBlobs, metricsPerBlob, pointsPerMetric int) ([]NumericBlob, error) {
	blobs := make([]NumericBlob, numBlobs)
	baseTime := time.Now()

	// Pre-generate metric IDs to ensure consistency across blobs
	metricIDs := make([]uint64, metricsPerBlob)
	for m := range metricsPerBlob {
		metricName := generateMetricName("shared", m)
		metricIDs[m] = hash.ID(metricName)
	}

	for blobIdx := range numBlobs {
		startTime := baseTime.Add(time.Duration(blobIdx) * time.Hour)
		encoder, err := NewNumericEncoder(
			startTime,
			WithTimestampEncoding(format.TypeDelta),
			WithValueEncoding(format.TypeGorilla),
			WithTagsEnabled(true),
		)
		if err != nil {
			return nil, err
		}

		for m, metricID := range metricIDs {
			if err := encoder.StartMetricID(metricID, pointsPerMetric); err != nil {
				return nil, err
			}

			for i := range pointsPerMetric {
				ts := int64(blobIdx*1000000 + i*1000)
				val := float64(i) + float64(m)*100
				if err := encoder.AddDataPoint(ts, val, ""); err != nil {
					return nil, err
				}
			}

			if err := encoder.EndMetric(); err != nil {
				return nil, err
			}
		}

		blobBytes, err := encoder.Finish()
		if err != nil {
			return nil, err
		}

		decoder, err := NewNumericDecoder(blobBytes)
		if err != nil {
			return nil, err
		}

		blob, err := decoder.Decode()
		if err != nil {
			return nil, err
		}

		blobs[blobIdx] = blob
	}

	return blobs, nil
}

// createBlobsWithUniqueMetrics creates blobs where each blob has unique metrics
func createBlobsWithUniqueMetrics(numBlobs, metricsPerBlob, pointsPerMetric int) ([]NumericBlob, error) {
	blobs := make([]NumericBlob, numBlobs)
	baseTime := time.Now()

	for blobIdx := range numBlobs {
		startTime := baseTime.Add(time.Duration(blobIdx) * time.Hour)
		encoder, err := NewNumericEncoder(
			startTime,
			WithTimestampEncoding(format.TypeDelta),
			WithValueEncoding(format.TypeGorilla),
			WithTagsEnabled(true),
		)
		if err != nil {
			return nil, err
		}

		for m := range metricsPerBlob {
			// Create unique metric names per blob using blob index
			metricName := generateMetricName("blob"+string(rune('0'+blobIdx)), m)
			metricID := hash.ID(metricName)

			if err := encoder.StartMetricID(metricID, pointsPerMetric); err != nil {
				return nil, err
			}

			for i := range pointsPerMetric {
				ts := int64(blobIdx*1000000 + i*1000)
				val := float64(i) + float64(m)*100 + float64(blobIdx)*10000
				if err := encoder.AddDataPoint(ts, val, ""); err != nil {
					return nil, err
				}
			}

			if err := encoder.EndMetric(); err != nil {
				return nil, err
			}
		}

		blobBytes, err := encoder.Finish()
		if err != nil {
			return nil, err
		}

		decoder, err := NewNumericDecoder(blobBytes)
		if err != nil {
			return nil, err
		}

		blob, err := decoder.Decode()
		if err != nil {
			return nil, err
		}

		blobs[blobIdx] = blob
	}

	return blobs, nil
}

// createBlobsWithOverlap creates blobs with specified percentage of overlapping metrics
func createBlobsWithOverlap(numBlobs, metricsPerBlob, pointsPerMetric int, overlapPercent float64) ([]NumericBlob, error) {
	blobs := make([]NumericBlob, numBlobs)
	baseTime := time.Now()

	// Calculate shared and unique metric counts
	sharedCount := int(float64(metricsPerBlob) * overlapPercent)
	uniqueCount := metricsPerBlob - sharedCount

	// Pre-generate shared metric IDs
	sharedMetricIDs := make([]uint64, sharedCount)
	for m := range sharedCount {
		metricName := generateMetricName("shared", m)
		sharedMetricIDs[m] = hash.ID(metricName)
	}

	for blobIdx := range numBlobs {
		startTime := baseTime.Add(time.Duration(blobIdx) * time.Hour)
		encoder, err := NewNumericEncoder(
			startTime,
			WithTimestampEncoding(format.TypeDelta),
			WithValueEncoding(format.TypeGorilla),
			WithTagsEnabled(true),
		)
		if err != nil {
			return nil, err
		}

		// Add shared metrics
		for m, metricID := range sharedMetricIDs {
			if err := encoder.StartMetricID(metricID, pointsPerMetric); err != nil {
				return nil, err
			}

			for i := range pointsPerMetric {
				ts := int64(blobIdx*1000000 + i*1000)
				val := float64(i) + float64(m)*100
				if err := encoder.AddDataPoint(ts, val, ""); err != nil {
					return nil, err
				}
			}

			if err := encoder.EndMetric(); err != nil {
				return nil, err
			}
		}

		// Add unique metrics
		for m := range uniqueCount {
			metricName := generateMetricName("blob"+string(rune('0'+blobIdx))+"_unique", m)
			metricID := hash.ID(metricName)

			if err := encoder.StartMetricID(metricID, pointsPerMetric); err != nil {
				return nil, err
			}

			for i := range pointsPerMetric {
				ts := int64(blobIdx*1000000 + i*1000)
				val := float64(i) + float64(m)*100 + float64(blobIdx)*10000
				if err := encoder.AddDataPoint(ts, val, ""); err != nil {
					return nil, err
				}
			}

			if err := encoder.EndMetric(); err != nil {
				return nil, err
			}
		}

		blobBytes, err := encoder.Finish()
		if err != nil {
			return nil, err
		}

		decoder, err := NewNumericDecoder(blobBytes)
		if err != nil {
			return nil, err
		}

		blob, err := decoder.Decode()
		if err != nil {
			return nil, err
		}

		blobs[blobIdx] = blob
	}

	return blobs, nil
}

// createMixedBlobs creates both numeric and text blobs for mixed workload testing
func createMixedBlobs(numBlobs, numericMetricsPerBlob, textMetricsPerBlob, pointsPerMetric int) ([]NumericBlob, []TextBlob, error) {
	baseTime := time.Now()

	// Pre-generate numeric metric IDs
	numericMetricIDs := make([]uint64, numericMetricsPerBlob)
	for m := range numericMetricsPerBlob {
		metricName := generateMetricName("numeric", m)
		numericMetricIDs[m] = hash.ID(metricName)
	}

	// Pre-generate text metric IDs
	textMetricIDs := make([]uint64, textMetricsPerBlob)
	for m := range textMetricsPerBlob {
		metricName := generateMetricName("text", m)
		textMetricIDs[m] = hash.ID(metricName)
	}

	// Create numeric blobs
	numericBlobs, err := createNumericBlobsWithIDs(numBlobs, numericMetricIDs, pointsPerMetric, baseTime)
	if err != nil {
		return nil, nil, err
	}

	// Create text blobs
	textBlobs, err := createTextBlobsWithIDs(numBlobs, textMetricIDs, pointsPerMetric, baseTime)
	if err != nil {
		return nil, nil, err
	}

	return numericBlobs, textBlobs, nil
}

// createNumericBlobsWithIDs creates numeric blobs with pre-generated metric IDs
func createNumericBlobsWithIDs(numBlobs int, metricIDs []uint64, pointsPerMetric int, baseTime time.Time) ([]NumericBlob, error) {
	blobs := make([]NumericBlob, numBlobs)

	for blobIdx := range numBlobs {
		startTime := baseTime.Add(time.Duration(blobIdx) * time.Hour)
		encoder, err := NewNumericEncoder(
			startTime,
			WithTimestampEncoding(format.TypeDelta),
			WithValueEncoding(format.TypeGorilla),
			WithTagsEnabled(true),
		)
		if err != nil {
			return nil, err
		}

		for m, metricID := range metricIDs {
			if err := encoder.StartMetricID(metricID, pointsPerMetric); err != nil {
				return nil, err
			}

			for i := range pointsPerMetric {
				ts := int64(blobIdx*1000000 + i*1000)
				val := float64(i) + float64(m)*100
				if err := encoder.AddDataPoint(ts, val, ""); err != nil {
					return nil, err
				}
			}

			if err := encoder.EndMetric(); err != nil {
				return nil, err
			}
		}

		blobBytes, err := encoder.Finish()
		if err != nil {
			return nil, err
		}

		decoder, err := NewNumericDecoder(blobBytes)
		if err != nil {
			return nil, err
		}

		blob, err := decoder.Decode()
		if err != nil {
			return nil, err
		}

		blobs[blobIdx] = blob
	}

	return blobs, nil
}

// createTextBlobsWithIDs creates text blobs with pre-generated metric IDs
func createTextBlobsWithIDs(numBlobs int, metricIDs []uint64, pointsPerMetric int, baseTime time.Time) ([]TextBlob, error) {
	blobs := make([]TextBlob, numBlobs)

	for blobIdx := range numBlobs {
		startTime := baseTime.Add(time.Duration(blobIdx) * time.Hour)
		encoder, err := NewTextEncoder(startTime, WithTextTagsEnabled(true))
		if err != nil {
			return nil, err
		}

		for _, metricID := range metricIDs {
			if err := encoder.StartMetricID(metricID, pointsPerMetric); err != nil {
				return nil, err
			}

			for i := range pointsPerMetric {
				ts := int64(blobIdx*1000000 + i*1000)
				val := "value_" + generateMetricName("", i)
				if err := encoder.AddDataPoint(ts, val, ""); err != nil {
					return nil, err
				}
			}

			if err := encoder.EndMetric(); err != nil {
				return nil, err
			}
		}

		blobBytes, err := encoder.Finish()
		if err != nil {
			return nil, err
		}

		decoder, err := NewTextDecoder(blobBytes)
		if err != nil {
			return nil, err
		}

		blob, err := decoder.Decode()
		if err != nil {
			return nil, err
		}

		blobs[blobIdx] = blob
	}

	return blobs, nil
}
