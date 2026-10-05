package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
)

// logf writes a diagnostic line to stderr, ignoring write errors.
func logf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...)
}

func main() {
	// testing.Init registers -test.benchtime, which -benchtime sets before any benchmark runs.
	testing.Init()

	opts, err := parseOptions(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		logf("Error: %v\n", err)
		os.Exit(1)
	}
	if err := applyBenchtime(opts.benchtime); err != nil {
		logf("Error: %v\n", err)
		os.Exit(1)
	}

	if opts.profiles != nil {
		err = runProfiles(opts, benchmarkRunner{})
	} else {
		err = runLegacy(opts, benchmarkRunner{})
	}
	if err != nil {
		logf("Error: %v\n", err)
		os.Exit(1)
	}

	if opts.verbose {
		logf("\nBenchmark complete! ✅\n")
	}
}

// runLegacy measures one -profile data set with every cell timed and writes the legacy schema,
// as measurev2 did before -profiles existed.
func runLegacy(o *options, runner timingRunner) error {
	profile, ok := findProfile(o.profile)
	if !ok {
		return fmt.Errorf("unknown -profile %q", o.profile)
	}
	metadata := ReportMetadata{
		GoVersion:   runtime.Version(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		NumCPU:      runtime.NumCPU(),
		Timestamp:   time.Now(),
		Data:        o.dataConfig(o.profile),
		ProfileSpec: &profile,
	}

	res, err := measureDataset(o.profile, o.plan(runner))
	if err != nil {
		return err
	}
	report, err := legacyReport(res, metadata)
	if err != nil {
		return err
	}

	return writeReport(report, o.output, o.pretty, o.verbose)
}
