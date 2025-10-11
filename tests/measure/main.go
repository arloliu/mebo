package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	// Define CLI flags
	numMetrics := flag.Int("metrics", 200, "Number of metrics to generate (for simulated data)")
	maxPoints := flag.Int("max-points", 200, "Maximum points per metric to test")
	valueJitter := flag.Float64("value-jitter", 5.0, "Value jitter percentage (e.g., 5.0 = 5%) (for simulated data)")
	tsJitter := flag.Float64("ts-jitter", 2.0, "Timestamp jitter percentage (e.g., 2.0 = 2%) (for simulated data)")
	inputFile := flag.String("input-file", "", "Input JSON file with real-world data (column-based or row-based format)")
	timeUnit := flag.String("time-unit", "us", "Time unit for input data: s, ms, us, ns (default: us)")
	outputFile := flag.String("output", "", "Optional CSV output file")
	verbose := flag.Bool("verbose", false, "Enable verbose output")

	flag.Parse()

	// Validate time unit
	tu, err := ValidateTimeUnit(*timeUnit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print header
	fmt.Println("=== Mebo Compression Analysis: Delta+Gorilla (No Compression) ===")
	fmt.Println()

	var testData *TestData
	var config Config

	// Load data from input file or generate simulated data
	if *inputFile != "" {
		// Load real-world data from file
		if *verbose {
			fmt.Printf("Loading data from %s...\n", *inputFile)
		}

		loadedData, err := LoadInputData(*inputFile, tu)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading input file: %v\n", err)
			os.Exit(1)
		}

		testData = loadedData

		// Update config from loaded data
		config = Config{
			NumMetrics:      len(loadedData.MetricIDs),
			MaxPoints:       loadedData.Config.MaxPoints,
			ValueJitter:     0,
			TimestampJitter: 0,
			Seed:            0,
			DataSource:      *inputFile,
			TimeUnit:        tu,
		}

		// Update testData.Config
		testData.Config = config

		if *verbose {
			fmt.Printf("Loaded %d metrics with up to %d points each\n",
				config.NumMetrics, config.MaxPoints)
		}
	} else {
		// Generate simulated data
		// Validate inputs for simulated data
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

		// Create configuration
		config = Config{
			NumMetrics:      *numMetrics,
			MaxPoints:       *maxPoints,
			ValueJitter:     *valueJitter,
			TimestampJitter: *tsJitter,
			Seed:            42, // Fixed seed for reproducibility
			DataSource:      "simulated",
			TimeUnit:        TimeUnitMicrosecond,
		}

		// Generate test data once
		if *verbose {
			fmt.Println("Generating test data...")
		}
		testData = GenerateTestData(config)
		if *verbose {
			fmt.Printf("Generated %d metrics with %d points each\n",
				len(testData.MetricIDs), config.MaxPoints)
		}
	}

	// Print configuration
	PrintConfig(config)

	// Calculate test point counts based on max points
	validPoints := CalculateTestPoints(config.MaxPoints)

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
