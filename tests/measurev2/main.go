package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"
)

// logf writes a diagnostic line to stderr, ignoring write errors.
func logf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...)
}

func main() {
	// CLI flags
	numMetrics := flag.Int("metrics", 200, "Number of metrics to generate")
	pointsPerMetric := flag.Int("points", 200, "Points per metric for matrix benchmarks")
	valueJitter := flag.Float64("value-jitter", 0.5, "Value jitter percentage (e.g., 0.5 = ±0.5% random walk per point; semiconductor sensors: 0.01-0.5%)")
	tsJitter := flag.Float64("ts-jitter", 0.1, "Timestamp jitter percentage (e.g., 0.1 = ±0.1% of interval; industrial protocols: <0.1%)")
	outputFile := flag.String("output", "", "Output JSON file path (default: stdout)")
	pretty := flag.Bool("pretty", false, "Pretty-print JSON output")
	verbose := flag.Bool("verbose", false, "Print progress to stderr")
	profileName := flag.String("profile", "", "Realistic data profile (empty = legacy full-precision random walk). Available: "+profileNames())

	flag.Parse()

	config := DataConfig{
		NumMetrics:      *numMetrics,
		PointsPerMetric: *pointsPerMetric,
		ValueJitterPct:  *valueJitter,
		TSJitterPct:     *tsJitter,
		Seed:            42,
		Profile:         *profileName,
	}

	// Validate
	if config.NumMetrics <= 0 {
		logf("Error: -metrics must be positive\n")
		os.Exit(1)
	}

	if config.PointsPerMetric <= 0 {
		logf("Error: -points must be positive\n")
		os.Exit(1)
	}

	// Generate test data
	if *verbose {
		src := "legacy full-precision random walk"
		if config.Profile != "" {
			src = "profile " + config.Profile
		}
		logf("Generating test data (%s): %d metrics × %d points...\n", src, config.NumMetrics, config.PointsPerMetric)
	}

	var data, sharedData *TestData
	if config.Profile != "" {
		p, ok := findProfile(config.Profile)
		if !ok {
			logf("Error: unknown -profile %q; available: %s\n", config.Profile, profileNames())
			os.Exit(1)
		}
		data = GenerateProfile(p, config)
		sharedData = data.shareTimestamps()
	} else {
		data = GenerateTestData(config)
		sharedData = GenerateSharedTimestampData(config)
	}

	combos := AllCombos()
	sharedCombos := SharedTSCombos()
	totalPoints := config.NumMetrics * config.PointsPerMetric

	// Build metadata
	metadata := ReportMetadata{
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		NumCPU:    runtime.NumCPU(),
		Timestamp: time.Now(),
		Data:      config,
	}

	// First, get the raw-raw baseline size for ratio calculations.
	rawRawSize, err := rawRawBaseline(combos, data)
	if err != nil {
		logf("Error measuring raw-raw baseline: %v\n", err)
		os.Exit(1)
	}

	// Phase 1: Matrix benchmarks (regular + shared-TS).
	if *verbose {
		logf("\n=== Matrix Benchmarks (%d combos + %d shared-TS combos) ===\n", len(combos), len(sharedCombos))
	}

	matrixResults := make([]MatrixResult, 0, len(combos)+len(sharedCombos))

	regularMatrix, err := runMatrixBench(combos, data, rawRawSize, totalPoints, *verbose)
	if err != nil {
		logf("Error in matrix benchmark: %v\n", err)
		os.Exit(1)
	}
	matrixResults = append(matrixResults, regularMatrix...)

	sharedMatrix, err := runMatrixBench(sharedCombos, sharedData, rawRawSize, totalPoints, *verbose)
	if err != nil {
		logf("Error in shared-TS matrix benchmark: %v\n", err)
		os.Exit(1)
	}
	matrixResults = append(matrixResults, sharedMatrix...)

	// Phase 2: Scaling analysis (regular + shared-TS).
	if *verbose {
		logf("\n=== Scaling Analysis (%d combos + %d shared-TS combos) ===\n", len(combos), len(sharedCombos))
	}

	scalingResults := make([]ScalingResult, 0, len(combos)+len(sharedCombos))

	regularScaling, err := runScalingBench(combos, data, *verbose)
	if err != nil {
		logf("Error in scaling: %v\n", err)
		os.Exit(1)
	}
	scalingResults = append(scalingResults, regularScaling...)

	sharedScaling, err := runScalingBench(sharedCombos, sharedData, *verbose)
	if err != nil {
		logf("Error in shared-TS scaling: %v\n", err)
		os.Exit(1)
	}
	scalingResults = append(scalingResults, sharedScaling...)

	// Build and emit the report.
	report := FullReport{
		Metadata: metadata,
		Matrix:   matrixResults,
		Scaling:  scalingResults,
	}

	if err := writeReport(report, *outputFile, *pretty, *verbose); err != nil {
		logf("Error writing report: %v\n", err)
		os.Exit(1)
	}

	if *verbose {
		logf("\nBenchmark complete! ✅\n")
	}
}

// rawRawBaseline returns the encoded size of the raw-raw combo, used as the
// denominator for the compression-ratio columns.
func rawRawBaseline(combos []EncodingCombo, data *TestData) (int, error) {
	for _, combo := range combos {
		if combo.Label == "raw-raw" {
			return measureEncodedSize(combo, data)
		}
	}

	return 0, errors.New("raw-raw combo not found in matrix")
}

// runMatrixBench benchmarks every combo against data at the fixed matrix size,
// returning one MatrixResult per combo.
func runMatrixBench(combos []EncodingCombo, data *TestData, rawRawSize, totalPoints int, verbose bool) ([]MatrixResult, error) {
	results := make([]MatrixResult, 0, len(combos))

	for i, combo := range combos {
		if verbose {
			logf("  [%d/%d] Benchmarking %s...\n", i+1, len(combos), combo.Label)
		}

		encodedSize, err := measureEncodedSize(combo, data)
		if err != nil {
			return nil, fmt.Errorf("encoding %s: %w", combo.Label, err)
		}

		bpp := float64(encodedSize) / float64(totalPoints)
		vsRaw := float64(rawRawSize) / float64(encodedSize)
		savings := (1.0 - float64(encodedSize)/float64(rawRawSize)) * 100.0

		if verbose {
			logf("    encode...")
		}

		encMetrics := benchEncode(combo, data)

		if verbose {
			logf(" decode...")
		}

		decMetrics, err := benchDecode(combo, data)
		if err != nil {
			return nil, fmt.Errorf("decoding %s: %w", combo.Label, err)
		}

		if verbose {
			logf(" iterate...")
		}

		iterMetrics, err := benchIterSeq(combo, data)
		if err != nil {
			return nil, fmt.Errorf("iterating %s: %w", combo.Label, err)
		}

		if verbose {
			logf(" done (%.1f bytes/point)\n", bpp)
		}

		results = append(results, MatrixResult{
			Label:           combo.Label,
			TSEncoding:      combo.TSEncoding.String(),
			ValEncoding:     combo.ValEncoding.String(),
			NumMetrics:      data.Config.NumMetrics,
			PointsPerMetric: data.Config.PointsPerMetric,
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

	return results, nil
}

// runScalingBench runs the scaling analysis for every combo against data.
func runScalingBench(combos []EncodingCombo, data *TestData, verbose bool) ([]ScalingResult, error) {
	results := make([]ScalingResult, 0, len(combos))

	for i, combo := range combos {
		if verbose {
			logf("  [%d/%d] Scaling %s...\n", i+1, len(combos), combo.Label)
		}

		scalingResult, err := runScaling(combo, data)
		if err != nil {
			return nil, fmt.Errorf("scaling %s: %w", combo.Label, err)
		}

		results = append(results, scalingResult)
	}

	return results, nil
}

// writeReport serializes report as JSON and writes it to outputFile, or to
// stdout when outputFile is empty.
func writeReport(report FullReport, outputFile string, pretty, verbose bool) error {
	var (
		jsonData []byte
		err      error
	)

	if pretty {
		jsonData, err = json.MarshalIndent(report, "", "  ")
	} else {
		jsonData, err = json.Marshal(report)
	}

	if err != nil {
		return fmt.Errorf("serializing JSON: %w", err)
	}

	if outputFile == "" {
		fmt.Println(string(jsonData))

		return nil
	}

	if err := os.WriteFile(outputFile, jsonData, 0o600); err != nil {
		return fmt.Errorf("writing output file: %w", err)
	}

	if verbose {
		logf("\nResults written to %s\n", outputFile)
	}

	return nil
}
