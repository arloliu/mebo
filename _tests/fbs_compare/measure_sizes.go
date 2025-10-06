package main

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
)

type Result struct {
	numMetrics    int
	numPoints     int
	totalPoints   int
	blobSize      int
	bytesPerPoint float64
	savings       float64
	ratio         float64 // metrics:points ratio
}

func main() {
	fmt.Println("=== Comprehensive Mebo Size Analysis: delta-none-gorilla-none ===")
	fmt.Println()

	// Test with different combinations
	metricCounts := []int{10, 100, 200, 400}
	pointSizes := []int{1, 5, 10, 100, 250}

	var results []Result
	var baselineResults []Result

	fmt.Println("=== Running Tests for All Combinations ===")
	fmt.Println()

	for _, numMetrics := range metricCounts {
		for _, numPoints := range pointSizes {
			totalPoints := numMetrics * numPoints

			// Test delta-none-gorilla-none
			testData := generateTestData(numMetrics, numPoints)
			blobSize := measureBlobSize(testData, format.TypeDelta, format.CompressionNone,
				format.TypeGorilla, format.CompressionNone)

			bytesPerPoint := float64(blobSize) / float64(totalPoints)
			baselineBytes := totalPoints * 16
			savings := (1.0 - (float64(blobSize) / float64(baselineBytes))) * 100.0
			ratio := float64(numMetrics) / float64(numPoints)

			results = append(results, Result{
				numMetrics:    numMetrics,
				numPoints:     numPoints,
				totalPoints:   totalPoints,
				blobSize:      blobSize,
				bytesPerPoint: bytesPerPoint,
				savings:       savings,
				ratio:         ratio,
			})

			// Also measure baseline (raw-none-raw-none) for reference
			baselineSize := measureBlobSize(testData, format.TypeRaw, format.CompressionNone,
				format.TypeRaw, format.CompressionNone)
			baselineBPP := float64(baselineSize) / float64(totalPoints)

			baselineResults = append(baselineResults, Result{
				numMetrics:    numMetrics,
				numPoints:     numPoints,
				totalPoints:   totalPoints,
				blobSize:      baselineSize,
				bytesPerPoint: baselineBPP,
				savings:       0.0,
				ratio:         ratio,
			})
		}
	}

	// Print results in table format
	fmt.Println("=== Delta+Gorilla Results (Production Default) ===")
	fmt.Println()
	fmt.Printf("%-10s | %-10s | %-12s | %-12s | %-12s | %-10s | %-10s\n",
		"Metrics", "Points", "Total Pts", "Blob Size", "Bytes/Point", "Savings", "M:P Ratio")
	fmt.Println(strings.Repeat("-", 100))

	for _, r := range results {
		fmt.Printf("%-10d | %-10d | %-12d | %-12d | %-12.2f | %-10.1f%% | %-10.2f\n",
			r.numMetrics, r.numPoints, r.totalPoints, r.blobSize, r.bytesPerPoint, r.savings, r.ratio)
	}

	fmt.Println()
	fmt.Println("=== Baseline Raw+Raw Results ===")
	fmt.Println()
	fmt.Printf("%-10s | %-10s | %-12s | %-12s | %-12s\n",
		"Metrics", "Points", "Total Pts", "Blob Size", "Bytes/Point")
	fmt.Println(strings.Repeat("-", 70))

	for _, r := range baselineResults {
		fmt.Printf("%-10d | %-10d | %-12d | %-12d | %-12.2f\n",
			r.numMetrics, r.numPoints, r.totalPoints, r.blobSize, r.bytesPerPoint)
	}

	// Analysis
	fmt.Println()
	fmt.Println("=== ANALYSIS ===")
	fmt.Println()

	analyzeResults(results)
}

func measureBlobSize(testData TestData, tsEnc format.EncodingType, tsComp format.CompressionType,
	valEnc format.EncodingType, valComp format.CompressionType,
) int {
	encoder, err := blob.NewNumericEncoder(
		testData.startTime,
		blob.WithTimestampEncoding(tsEnc),
		blob.WithTimestampCompression(tsComp),
		blob.WithValueEncoding(valEnc),
		blob.WithValueCompression(valComp),
	)
	if err != nil {
		panic(err)
	}

	numMetrics := len(testData.metricIDs)
	numPoints := len(testData.timestamps) / numMetrics

	for i := 0; i < numMetrics; i++ {
		metricID := testData.metricIDs[i]
		encoder.StartMetricID(metricID, numPoints)

		for j := 0; j < numPoints; j++ {
			idx := i*numPoints + j
			encoder.AddDataPoint(testData.timestamps[idx], testData.values[idx], "")
		}

		encoder.EndMetric()
	}

	b, err := encoder.Finish()
	if err != nil {
		panic(err)
	}

	return len(b)
}

func analyzeResults(results []Result) {
	// Group by metrics count
	metricGroups := make(map[int][]Result)
	for _, r := range results {
		metricGroups[r.numMetrics] = append(metricGroups[r.numMetrics], r)
	}

	// Group by points count
	pointGroups := make(map[int][]Result)
	for _, r := range results {
		pointGroups[r.numPoints] = append(pointGroups[r.numPoints], r)
	}

	fmt.Println("1. Impact of Points per Metric (holding metrics constant)")
	fmt.Println()

	for metrics := range metricGroups {
		results := metricGroups[metrics]
		if len(results) < 2 {
			continue
		}

		fmt.Printf("   With %d metrics:\n", metrics)
		for i := 0; i < len(results)-1; i++ {
			from := results[i]
			to := results[i+1]
			improvement := ((from.bytesPerPoint - to.bytesPerPoint) / from.bytesPerPoint) * 100.0
			fmt.Printf("     %d → %d points: %.2f → %.2f bytes/point (%.1f%% improvement)\n",
				from.numPoints, to.numPoints, from.bytesPerPoint, to.bytesPerPoint, improvement)
		}
		fmt.Println()
	}

	fmt.Println("2. Impact of Metric Count (holding points constant)")
	fmt.Println()

	for points := range pointGroups {
		results := pointGroups[points]
		if len(results) < 2 {
			continue
		}

		fmt.Printf("   With %d points per metric:\n", points)
		for i := 0; i < len(results)-1; i++ {
			from := results[i]
			to := results[i+1]
			change := ((to.bytesPerPoint - from.bytesPerPoint) / from.bytesPerPoint) * 100.0
			fmt.Printf("     %d → %d metrics: %.2f → %.2f bytes/point (%.1f%% change)\n",
				from.numMetrics, to.numMetrics, from.bytesPerPoint, to.bytesPerPoint, change)
		}
		fmt.Println()
	}

	fmt.Println("3. Best and Worst Configurations")
	fmt.Println()

	// Find best and worst
	best := results[0]
	worst := results[0]
	for _, r := range results {
		if r.bytesPerPoint < best.bytesPerPoint {
			best = r
		}
		if r.bytesPerPoint > worst.bytesPerPoint {
			worst = r
		}
	}

	fmt.Printf("   Best:  %d metrics × %d points = %.2f bytes/point (%.1f%% savings, ratio %.2f:1)\n",
		best.numMetrics, best.numPoints, best.bytesPerPoint, best.savings, best.ratio)
	fmt.Printf("   Worst: %d metrics × %d points = %.2f bytes/point (%.1f%% savings, ratio %.2f:1)\n",
		worst.numMetrics, worst.numPoints, worst.bytesPerPoint, worst.savings, worst.ratio)
	fmt.Println()

	fmt.Println("4. Metrics-to-Points Ratio Analysis")
	fmt.Println()

	// Group by ratio ranges
	veryHighRatio := []Result{} // ratio > 10
	highRatio := []Result{}     // ratio 1-10
	lowRatio := []Result{}      // ratio < 1

	for _, r := range results {
		if r.ratio > 10 {
			veryHighRatio = append(veryHighRatio, r)
		} else if r.ratio >= 1 {
			highRatio = append(highRatio, r)
		} else {
			lowRatio = append(lowRatio, r)
		}
	}

	avgBPP := func(results []Result) float64 {
		sum := 0.0
		for _, r := range results {
			sum += r.bytesPerPoint
		}
		return sum / float64(len(results))
	}

	if len(veryHighRatio) > 0 {
		fmt.Printf("   Very High Ratio (>10:1): Avg %.2f bytes/point ❌ Poor\n", avgBPP(veryHighRatio))
	}
	if len(highRatio) > 0 {
		fmt.Printf("   High Ratio (1-10:1):     Avg %.2f bytes/point ⚠️  Moderate\n", avgBPP(highRatio))
	}
	if len(lowRatio) > 0 {
		fmt.Printf("   Low Ratio (<1:1):        Avg %.2f bytes/point ✅ Good\n", avgBPP(lowRatio))
	}
	fmt.Println()

	fmt.Println("5. Key Recommendations")
	fmt.Println()
	fmt.Println("   ✅ Use at least 10 points per metric")
	fmt.Println("   ✅ Optimal: 100-250 points per metric")
	fmt.Println("   ✅ Keep metrics-to-points ratio below 1:1")
	fmt.Println("   ❌ Avoid: Many metrics with few points (high ratio)")
	fmt.Println("   📊 Best efficiency at 9.2-9.8 bytes/point (~39-42% savings)")
}

type TestData struct {
	metricIDs  []uint64
	timestamps []int64
	values     []float64
	startTime  time.Time
}

func generateTestData(numMetrics, numPoints int) TestData {
	totalPoints := numMetrics * numPoints
	rng := rand.New(rand.NewSource(42))

	data := TestData{
		metricIDs:  make([]uint64, numMetrics),
		timestamps: make([]int64, totalPoints),
		values:     make([]float64, totalPoints),
		startTime:  time.Unix(1700000000, 0),
	}

	// Generate metric IDs
	for i := 0; i < numMetrics; i++ {
		name := fmt.Sprintf("metric.%d", i+1000)
		data.metricIDs[i] = hash.ID(name)
	}

	// Generate timestamps and values
	baseInterval := time.Second
	jitterPercent := 0.05
	baseValue := 100.0
	deltaPercent := 0.02

	for i := 0; i < numMetrics; i++ {
		currentTime := data.startTime
		currentValue := baseValue + float64(i)*10.0

		for j := 0; j < numPoints; j++ {
			idx := i*numPoints + j

			// Timestamp with jitter (microseconds)
			jitter := time.Duration(float64(baseInterval) * (rng.Float64()*2.0 - 1.0) * jitterPercent)
			currentTime = currentTime.Add(baseInterval + jitter)
			data.timestamps[idx] = currentTime.UnixMicro()

			// Value with small delta
			delta := currentValue * deltaPercent * (rng.Float64()*2.0 - 1.0)
			currentValue += delta
			data.values[idx] = currentValue
		}
	}

	return data
}
