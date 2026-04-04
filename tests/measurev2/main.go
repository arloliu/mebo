package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"
)

func main() {
	// CLI flags
	numMetrics := flag.Int("metrics", 200, "Number of metrics to generate")
	pointsPerMetric := flag.Int("points", 200, "Points per metric for matrix benchmarks")
	valueJitter := flag.Float64("value-jitter", 0.5, "Value jitter percentage (e.g., 0.5 = ±0.5% random walk per point; semiconductor sensors: 0.01-0.5%)")
	tsJitter := flag.Float64("ts-jitter", 0.1, "Timestamp jitter percentage (e.g., 0.1 = ±0.1% of interval; industrial protocols: <0.1%)")
	outputFile := flag.String("output", "", "Output JSON file path (default: stdout)")
	pretty := flag.Bool("pretty", false, "Pretty-print JSON output")
	verbose := flag.Bool("verbose", false, "Print progress to stderr")

	flag.Parse()

	config := DataConfig{
		NumMetrics:      *numMetrics,
		PointsPerMetric: *pointsPerMetric,
		ValueJitterPct:  *valueJitter,
		TSJitterPct:     *tsJitter,
		Seed:            42,
	}

	// Validate
	if config.NumMetrics <= 0 {
		fmt.Fprintf(os.Stderr, "Error: -metrics must be positive\n")
		os.Exit(1)
	}

	if config.PointsPerMetric <= 0 {
		fmt.Fprintf(os.Stderr, "Error: -points must be positive\n")
		os.Exit(1)
	}

	// Generate test data
	if *verbose {
		fmt.Fprintf(os.Stderr, "Generating test data: %d metrics × %d points...\n", config.NumMetrics, config.PointsPerMetric)
	}

	data := GenerateTestData(config)
	sharedData := GenerateSharedTimestampData(config)
	combos := AllCombos()
	sharedCombos := SharedTSCombos()

	// Build metadata
	metadata := ReportMetadata{
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		NumCPU:    runtime.NumCPU(),
		Timestamp: time.Now(),
		Data:      config,
	}

	// Phase 1: Matrix benchmarks
	if *verbose {
		fmt.Fprintf(os.Stderr, "\n=== Matrix Benchmarks (%d combos + %d shared-TS combos) ===\n", len(combos), len(sharedCombos))
	}

	// First, get the raw-raw baseline size for ratio calculations
	var rawRawSize int
	for _, combo := range combos {
		if combo.Label == "raw-raw" {
			size, err := measureEncodedSize(combo, data)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error measuring raw-raw baseline: %v\n", err)
				os.Exit(1)
			}
			rawRawSize = size

			break
		}
	}

	matrixResults := make([]MatrixResult, 0, len(combos))

	for i, combo := range combos {
		if *verbose {
			fmt.Fprintf(os.Stderr, "  [%d/%d] Benchmarking %s...\n", i+1, len(combos), combo.Label)
		}

		// Measure encoded size
		encodedSize, err := measureEncodedSize(combo, data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding %s: %v\n", combo.Label, err)
			os.Exit(1)
		}

		totalPoints := config.NumMetrics * config.PointsPerMetric
		bpp := float64(encodedSize) / float64(totalPoints)
		vsRaw := float64(rawRawSize) / float64(encodedSize)
		savings := (1.0 - float64(encodedSize)/float64(rawRawSize)) * 100.0

		// Run benchmarks
		if *verbose {
			fmt.Fprintf(os.Stderr, "    encode...")
		}

		encMetrics := benchEncode(combo, data)

		if *verbose {
			fmt.Fprintf(os.Stderr, " decode...")
		}

		decMetrics, err := benchDecode(combo, data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError decoding %s: %v\n", combo.Label, err)
			os.Exit(1)
		}

		if *verbose {
			fmt.Fprintf(os.Stderr, " iterate...")
		}

		iterMetrics, err := benchIterSeq(combo, data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError iterating %s: %v\n", combo.Label, err)
			os.Exit(1)
		}

		if *verbose {
			fmt.Fprintf(os.Stderr, " done (%.1f bytes/point)\n", bpp)
		}

		matrixResults = append(matrixResults, MatrixResult{
			Label:           combo.Label,
			TSEncoding:      combo.TSEncoding.String(),
			ValEncoding:     combo.ValEncoding.String(),
			NumMetrics:      config.NumMetrics,
			PointsPerMetric: config.PointsPerMetric,
			TotalPoints:     totalPoints,
			EncodedBytes:    encodedSize,
			BytesPerPoint:   bpp,
			VsRawRatio:      vsRaw,
			SpaceSavingsPct: savings,
			Encode:          encMetrics,
			Decode:          decMetrics,
			IterSeq:         iterMetrics,
		})
	}

	// Phase 1b: Shared-timestamp matrix benchmarks
	for i, combo := range sharedCombos {
		if *verbose {
			fmt.Fprintf(os.Stderr, "  [%d/%d] Benchmarking %s...\n", i+1, len(sharedCombos), combo.Label)
		}

		encodedSize, err := measureEncodedSize(combo, sharedData)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding %s: %v\n", combo.Label, err)
			os.Exit(1)
		}

		totalPoints := config.NumMetrics * config.PointsPerMetric
		bpp := float64(encodedSize) / float64(totalPoints)
		vsRaw := float64(rawRawSize) / float64(encodedSize)
		savings := (1.0 - float64(encodedSize)/float64(rawRawSize)) * 100.0

		if *verbose {
			fmt.Fprintf(os.Stderr, "    encode...")
		}

		encMetrics := benchEncode(combo, sharedData)

		if *verbose {
			fmt.Fprintf(os.Stderr, " decode...")
		}

		decMetrics, err := benchDecode(combo, sharedData)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError decoding %s: %v\n", combo.Label, err)
			os.Exit(1)
		}

		if *verbose {
			fmt.Fprintf(os.Stderr, " iterate...")
		}

		iterMetrics, err := benchIterSeq(combo, sharedData)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError iterating %s: %v\n", combo.Label, err)
			os.Exit(1)
		}

		if *verbose {
			fmt.Fprintf(os.Stderr, " done (%.1f bytes/point)\n", bpp)
		}

		matrixResults = append(matrixResults, MatrixResult{
			Label:           combo.Label,
			TSEncoding:      combo.TSEncoding.String(),
			ValEncoding:     combo.ValEncoding.String(),
			NumMetrics:      config.NumMetrics,
			PointsPerMetric: config.PointsPerMetric,
			TotalPoints:     totalPoints,
			EncodedBytes:    encodedSize,
			BytesPerPoint:   bpp,
			VsRawRatio:      vsRaw,
			SpaceSavingsPct: savings,
			Encode:          encMetrics,
			Decode:          decMetrics,
			IterSeq:         iterMetrics,
		})
	}

	// Phase 2: Scaling analysis
	if *verbose {
		fmt.Fprintf(os.Stderr, "\n=== Scaling Analysis (%d combos + %d shared-TS combos) ===\n", len(combos), len(sharedCombos))
	}

	scalingResults := make([]ScalingResult, 0, len(combos))
	for i, combo := range combos {
		if *verbose {
			fmt.Fprintf(os.Stderr, "  [%d/%d] Scaling %s...\n", i+1, len(combos), combo.Label)
		}

		scalingResult, err := runScaling(combo, data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error in scaling %s: %v\n", combo.Label, err)
			os.Exit(1)
		}

		scalingResults = append(scalingResults, scalingResult)
	}

	// Phase 2b: Shared-TS scaling analysis
	for i, combo := range sharedCombos {
		if *verbose {
			fmt.Fprintf(os.Stderr, "  [%d/%d] Scaling %s...\n", i+1, len(sharedCombos), combo.Label)
		}

		scalingResult, err := runScaling(combo, sharedData)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error in scaling %s: %v\n", combo.Label, err)
			os.Exit(1)
		}

		scalingResults = append(scalingResults, scalingResult)
	}

	// Build full report
	report := FullReport{
		Metadata: metadata,
		Matrix:   matrixResults,
		Scaling:  scalingResults,
	}

	// Serialize JSON
	var jsonData []byte
	var jsonErr error

	if *pretty {
		jsonData, jsonErr = json.MarshalIndent(report, "", "  ")
	} else {
		jsonData, jsonErr = json.Marshal(report)
	}

	if jsonErr != nil {
		fmt.Fprintf(os.Stderr, "Error serializing JSON: %v\n", jsonErr)
		os.Exit(1)
	}

	// Output
	if *outputFile != "" {
		if err := os.WriteFile(*outputFile, jsonData, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output file: %v\n", err)
			os.Exit(1)
		}

		if *verbose {
			fmt.Fprintf(os.Stderr, "\nResults written to %s\n", *outputFile)
		}
	} else {
		fmt.Println(string(jsonData))
	}

	if *verbose {
		fmt.Fprintf(os.Stderr, "\nBenchmark complete! ✅\n")
	}
}
