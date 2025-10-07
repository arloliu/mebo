package main

import (
	"fmt"
	"math"
	"os"
	"strings"
)

// PrintConfig prints the configuration summary.
func PrintConfig(config Config) {
	fmt.Println("Configuration:")
	fmt.Printf("  Metrics:           %d\n", config.NumMetrics)
	fmt.Printf("  Max Points:        %d\n", config.MaxPoints)
	fmt.Printf("  Value Jitter:      %.1f%%\n", config.ValueJitter)
	fmt.Printf("  Timestamp Jitter:  %.1f%%\n", config.TimestampJitter)
	fmt.Printf("  Encoding:          Delta + Gorilla\n")
	fmt.Printf("  Compression:       None\n")
	fmt.Println()
}

// PrintResults prints the measurement results in a formatted table.
func PrintResults(results []MeasurementResult) {
	fmt.Println("=== Measurement Results ===")
	fmt.Println()

	// Print header
	fmt.Printf("%-13s | %-11s | %-12s | %-13s | %-13s | %-10s\n",
		"Points/Metric", "Total Pts", "Blob Size", "Bytes/Point", "Compression", "Savings")
	fmt.Println(strings.Repeat("-", 90))

	// Print each result
	for _, r := range results {
		fmt.Printf("%-13d | %-11s | %-12s | %-13.2f | %-13s | %-10s\n",
			r.PointsPerMetric,
			formatNumber(r.TotalPoints),
			formatNumber(r.BlobSize),
			r.BytesPerPoint,
			fmt.Sprintf("%.2fx", r.CompressionRatio),
			fmt.Sprintf("%.1f%%", r.SavingsPercent))
	}
	fmt.Println()
}

// PrintAnalysis prints the statistical analysis and formula fit.
func PrintAnalysis(analysis AnalysisResult) {
	fmt.Println("=== Statistical Analysis ===")
	fmt.Println()

	stats := analysis.Statistics
	fmt.Printf("  Minimum bytes/point:  %.2f (at %d points/metric)\n",
		stats.MinBytesPerPoint, findPointsAtValue(analysis.Measurements, stats.MinBytesPerPoint))
	fmt.Printf("  Maximum bytes/point:  %.2f (at %d points/metric)\n",
		stats.MaxBytesPerPoint, findPointsAtValue(analysis.Measurements, stats.MaxBytesPerPoint))
	fmt.Printf("  Average bytes/point:  %.2f\n", stats.AvgBytesPerPoint)
	fmt.Printf("  Median bytes/point:   %.2f\n", stats.MedianBytesPerPoint)
	fmt.Printf("  Standard deviation:   %.2f\n", stats.StdDeviation)
	fmt.Println()

	fmt.Println("=== Formula Fit ===")
	fmt.Println()

	best := analysis.BestFit
	fmt.Printf("  Best fit: %s\n", strings.Title(best.Type))
	fmt.Printf("  Formula:  %s\n", best.Formula)
	fmt.Printf("  R²:       %.4f (%s)\n", best.RSquared, classifyRSquared(best.RSquared))
	fmt.Printf("  RMSE:     %.2f bytes/point\n", best.RMSE)
	fmt.Println()

	// Show prediction accuracy
	fmt.Println("  Prediction accuracy:")
	showPredictions(analysis.Measurements, best)
	fmt.Println()

	// Show all fits for comparison
	if len(analysis.AllFits) > 1 {
		fmt.Println("  All formula fits:")
		for _, fit := range analysis.AllFits {
			fmt.Printf("    %-12s  R²=%.4f  RMSE=%.2f\n", fit.Type+":", fit.RSquared, fit.RMSE)
		}
		fmt.Println()
	}
}

// PrintRecommendations prints usage recommendations based on analysis.
func PrintRecommendations(analysis AnalysisResult) {
	fmt.Println("=== Recommendations ===")
	fmt.Println()

	stats := analysis.Statistics

	// Find optimal ranges
	var minAcceptable, optimalStart, optimalEnd int
	for _, r := range analysis.Measurements {
		if r.BytesPerPoint < 11.0 && minAcceptable == 0 {
			minAcceptable = r.PointsPerMetric
		}
		if r.BytesPerPoint < 9.8 && optimalStart == 0 {
			optimalStart = r.PointsPerMetric
		}
		if r.BytesPerPoint < 9.3 {
			optimalEnd = r.PointsPerMetric
		}
	}

	if minAcceptable > 0 {
		fmt.Printf("  ✅ Use at least %d points per metric (bytes/point < 11)\n", minAcceptable)
	}
	if optimalStart > 0 && optimalEnd > optimalStart {
		fmt.Printf("  ✅ Optimal: %d-%d points per metric (bytes/point: %.1f-%.1f)\n",
			optimalStart, optimalEnd, stats.MinBytesPerPoint, 9.8)
	}
	if len(analysis.Measurements) > 3 {
		lastIdx := len(analysis.Measurements) - 1
		improvement := analysis.Measurements[lastIdx-1].BytesPerPoint - analysis.Measurements[lastIdx].BytesPerPoint
		if improvement < 0.1 {
			fmt.Printf("  ✅ Diminishing returns after %d points per metric\n",
				analysis.Measurements[lastIdx-1].PointsPerMetric)
		}
	}
	fmt.Println("  ⚠️  Avoid: 1-5 points per metric (bytes/point > 14)")
	fmt.Printf("  📊 Formula: %s\n", analysis.BestFit.Formula)
	fmt.Println()
}

// ExportCSV exports results to a CSV file.
func ExportCSV(filename string, results []MeasurementResult) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Write header
	_, err = file.WriteString("points_per_metric,num_metrics,total_points,blob_size,bytes_per_point,compression_ratio,savings_percent\n")
	if err != nil {
		return err
	}

	// Write data rows
	for _, r := range results {
		_, err = file.WriteString(fmt.Sprintf("%d,%d,%d,%d,%.2f,%.2f,%.1f\n",
			r.PointsPerMetric,
			r.NumMetrics,
			r.TotalPoints,
			r.BlobSize,
			r.BytesPerPoint,
			r.CompressionRatio,
			r.SavingsPercent))
		if err != nil {
			return err
		}
	}

	return nil
}

// Helper functions

func formatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	// Add commas
	var result []rune
	for i, digit := range reverse(s) {
		if i > 0 && i%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, digit)
	}
	return reverse(string(result))
}

func reverse(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func findPointsAtValue(results []MeasurementResult, targetValue float64) int {
	for _, r := range results {
		if r.BytesPerPoint == targetValue {
			return r.PointsPerMetric
		}
	}
	return 0
}

func classifyRSquared(r2 float64) string {
	if r2 >= 0.98 {
		return "excellent fit"
	} else if r2 >= 0.95 {
		return "very good fit"
	} else if r2 >= 0.90 {
		return "good fit"
	} else if r2 >= 0.80 {
		return "moderate fit"
	}
	return "poor fit"
}

func showPredictions(results []MeasurementResult, fit FormulaFit) {
	// Show predictions for a few representative points
	indices := []int{0, len(results) / 3, len(results) * 2 / 3, len(results) - 1}

	for _, idx := range indices {
		if idx >= len(results) {
			continue
		}
		r := results[idx]
		ppm := float64(r.PointsPerMetric)
		actual := r.BytesPerPoint

		var predicted float64
		switch fit.Type {
		case "logarithmic":
			predicted = fit.Coefficients[0] + fit.Coefficients[1]*math.Log(ppm)
		case "power":
			predicted = fit.Coefficients[0] * math.Pow(ppm, fit.Coefficients[1])
		case "hyperbolic":
			predicted = fit.Coefficients[0] + fit.Coefficients[1]/ppm
		}

		errorPct := math.Abs((predicted - actual) / actual * 100.0)
		fmt.Printf("    %3d points:  Predicted %.2f, Actual %.2f (%.1f%% error)\n",
			r.PointsPerMetric, predicted, actual, errorPct)
	}
}
