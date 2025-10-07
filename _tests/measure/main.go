package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	// Define CLI flags
	numMetrics := flag.Int("metrics", 200, "Number of metrics to generate")
	maxPoints := flag.Int("max-points", 200, "Maximum points per metric to test")
	valueJitter := flag.Float64("value-jitter", 5.0, "Value jitter percentage (e.g., 5.0 = 5%)")
	tsJitter := flag.Float64("ts-jitter", 2.0, "Timestamp jitter percentage (e.g., 2.0 = 2%)")
	outputFile := flag.String("output", "", "Optional CSV output file")
	verbose := flag.Bool("verbose", false, "Enable verbose output")

	flag.Parse()

	// Validate inputs
	if *numMetrics <= 0 {
		fmt.Fprintf(os.Stderr, "Error: -metrics must be positive\n")
		os.Exit(1)
	}
	if *maxPoints <= 0 {
		fmt.Fprintf(os.Stderr, "Error: -max-points must be positive\n")
		os.Exit(1)
	}
	if *valueJitter < 0 {
		fmt.Fprintf(os.Stderr, "Error: -value-jitter cannot be negative\n")
		os.Exit(1)
	}
	if *tsJitter < 0 {
		fmt.Fprintf(os.Stderr, "Error: -ts-jitter cannot be negative\n")
		os.Exit(1)
	}

	// Print header
	fmt.Println("=== Mebo Compression Analysis: Delta+Gorilla (No Compression) ===")
	fmt.Println()

	// Create configuration
	config := Config{
		NumMetrics:      *numMetrics,
		MaxPoints:       *maxPoints,
		ValueJitter:     *valueJitter,
		TimestampJitter: *tsJitter,
		Seed:            42, // Fixed seed for reproducibility
	}

	// Print configuration
	PrintConfig(config)

	// Generate test data once
	if *verbose {
		fmt.Println("Generating test data...")
	}
	testData := GenerateTestData(config)
	if *verbose {
		fmt.Printf("Generated %d metrics with %d points each\n\n",
			len(testData.MetricIDs), config.MaxPoints)
	}

	// Define test point counts
	testPoints := []int{1, 2, 5, 10, 20, 50, 100, 150, 200}

	// Filter out test points greater than maxPoints
	var validPoints []int
	for _, pts := range testPoints {
		if pts <= *maxPoints {
			validPoints = append(validPoints, pts)
		}
	}

	// Run measurements
	fmt.Println("Running measurements...")
	var results []MeasurementResult

	for i, pts := range validPoints {
		if *verbose {
			fmt.Printf("  [%d/%d] Testing %d points per metric...\n",
				i+1, len(validPoints), pts)
		}

		result, err := MeasureDeltaGorilla(*testData, pts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error measuring %d points: %v\n", pts, err)
			os.Exit(1)
		}

		results = append(results, result)
	}

	if *verbose {
		fmt.Println("Measurements complete!\n")
	}
	fmt.Println()

	// Analyze results
	if *verbose {
		fmt.Println("Analyzing results...")
	}
	analysis := Analyze(results)
	if *verbose {
		fmt.Println("Analysis complete!\n")
	}

	// Print results
	PrintResults(results)
	PrintAnalysis(analysis)
	PrintRecommendations(analysis)

	// Export CSV if requested
	if *outputFile != "" {
		if *verbose {
			fmt.Printf("Exporting results to %s...\n", *outputFile)
		}
		err := ExportCSV(*outputFile, results)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error exporting CSV: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Results exported to: %s\n", *outputFile)
	}

	fmt.Println("Measurement complete! ✅")
}
