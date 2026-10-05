package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"time"
)

// -benchtime bounds and -order values.
const (
	minBenchtime = 10 * time.Millisecond
	maxBenchtime = 10 * time.Second
	orderForward = "forward"
	orderReverse = "reverse"
)

// profilesOnlyFlags are the flags that only make sense with -profiles; without it they are errors.
var profilesOnlyFlags = []string{"outdir", "sizes-only", "order", "run-id", "source", "tools", "layout", "round", "rounds"}

// options holds the parsed and validated command line.
type options struct {
	numMetrics      int
	pointsPerMetric int
	valueJitter     float64
	tsJitter        float64
	output          string
	pretty          bool
	verbose         bool
	profile         string

	// The version-1 flags; profiles is nil without -profiles.
	profilesFlag string
	profiles     []string
	outdir       string
	cells        string
	benchtime    string
	order        string
	sizesOnly    bool
	runID        string
	source       string
	tools        string
	layout       int
	round        int
	rounds       int
}

// parseOptions parses and validates args on a private flag set, so it never touches flag.CommandLine,
// where testing.Init registers -test.benchtime.
// It returns flag.ErrHelp for -help.
func parseOptions(args []string, errOut io.Writer) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet("measurev2", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.IntVar(&o.numMetrics, "metrics", 100, "Number of metrics to generate")
	fs.IntVar(&o.pointsPerMetric, "points", 150, "Points per metric for matrix benchmarks")
	fs.Float64Var(&o.valueJitter, "value-jitter", 0.5, "legacy_random_walk only: value jitter percentage (0.5 = ±0.5% random walk per point)")
	fs.Float64Var(&o.tsJitter, "ts-jitter", 0.1, "legacy_random_walk only: timestamp jitter percentage (0.1 = ±0.1% of the 1 s interval)")
	fs.StringVar(&o.output, "output", "", "Output JSON file path (default: stdout); an error with -profiles")
	fs.BoolVar(&o.pretty, "pretty", false, "Pretty-print JSON output")
	fs.BoolVar(&o.verbose, "verbose", false, "Print progress to stderr")
	fs.StringVar(&o.profile, "profile", DefaultProfile, "Data profile (empty = legacy_random_walk); an error with -profiles. Available: "+profileNames())
	fs.StringVar(&o.profilesFlag, "profiles", "", "Measure several data sets in one process: report, all, or a comma list of profile names")
	fs.StringVar(&o.outdir, "outdir", "", "With -profiles: output directory, which must not exist")
	fs.StringVar(&o.cells, "cells", cellsFull, "Timed cells: full, or with -profiles report or wide")
	fs.StringVar(&o.benchtime, "benchtime", "1s", "Target time of each testing.Benchmark call (10ms-10s)")
	fs.StringVar(&o.order, "order", orderForward, "With -profiles: forward, or reverse to run data sets, combos and operations backwards")
	fs.BoolVar(&o.sizesOnly, "sizes-only", false, "With -profiles: measure sizes and scaling only, never timing anything")
	fs.StringVar(&o.runID, "run-id", "", "With -profiles: run id recorded in the JSON (default: a new random id)")
	fs.StringVar(&o.source, "source", "", "With -profiles: source provenance recorded in the JSON (set by layouts.sh)")
	fs.StringVar(&o.tools, "tools", "", "With -profiles: tool provenance recorded in the JSON (set by layouts.sh)")
	fs.IntVar(&o.layout, "layout", -1, "With -profiles: code layout number recorded in the JSON (set by layouts.sh)")
	fs.IntVar(&o.round, "round", -1, "With -profiles: round number recorded in the JSON, at most -rounds (set by layouts.sh)")
	fs.IntVar(&o.rounds, "rounds", 0, "With -profiles: the run's round count, 0, 2 or 4 (0 = standalone)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if err := o.validate(set); err != nil {
		return nil, err
	}

	return o, nil
}

// applyBenchtime sets -test.benchtime, which testing.Benchmark reads; testing.Init must have run first.
func applyBenchtime(benchtime string) error {
	if flag.Lookup("test.benchtime") == nil {
		return errors.New("testing.Init has not registered -test.benchtime")
	}

	return flag.Set("test.benchtime", benchtime)
}

// validate checks every flag and the conflicts between them, and fills the derived fields.
func (o *options) validate(set map[string]bool) error {
	if o.numMetrics <= 0 {
		return errors.New("-metrics must be positive")
	}
	if o.pointsPerMetric <= 0 {
		return errors.New("-points must be positive")
	}
	if err := o.validateTiming(); err != nil {
		return err
	}
	if set["profiles"] {
		return o.validateProfiles(set)
	}

	for _, name := range profilesOnlyFlags {
		if set[name] {
			return fmt.Errorf("-%s requires -profiles", name)
		}
	}
	if o.cells != cellsFull {
		return fmt.Errorf("-cells %s requires -profiles", o.cells)
	}
	if o.profile == "" {
		o.profile = "legacy_random_walk"
	}
	if _, ok := findProfile(o.profile); !ok {
		return fmt.Errorf("unknown -profile %q; available: %s", o.profile, profileNames())
	}

	return nil
}

// validateTiming checks -cells, -benchtime, -order and the round flags, and normalizes -benchtime.
func (o *options) validateTiming() error {
	if !validCells(o.cells) {
		return fmt.Errorf("-cells %q: want full, report or wide", o.cells)
	}

	d, err := time.ParseDuration(o.benchtime)
	if err != nil {
		return fmt.Errorf("-benchtime %q: want a duration such as 50ms: %w", o.benchtime, err)
	}
	if d < minBenchtime || d > maxBenchtime {
		return fmt.Errorf("-benchtime %s: want %s to %s", d, minBenchtime, maxBenchtime)
	}
	o.benchtime = d.String()

	if o.order != orderForward && o.order != orderReverse {
		return fmt.Errorf("-order %q: want forward or reverse", o.order)
	}
	if !slices.Contains([]int{0, 2, 4}, o.rounds) {
		return fmt.Errorf("-rounds %d: want 0, 2 or 4", o.rounds)
	}
	if o.round > o.rounds {
		return fmt.Errorf("-round %d exceeds -rounds %d", o.round, o.rounds)
	}
	if o.round != -1 && o.round < 1 {
		return fmt.Errorf("-round %d: want -1 or 1 to -rounds", o.round)
	}
	if o.layout < -1 {
		return fmt.Errorf("-layout %d: want -1 or a layout number", o.layout)
	}

	return nil
}

// validateProfiles checks the flags of a -profiles run and generates a run id when none is given.
func (o *options) validateProfiles(set map[string]bool) error {
	if set["profile"] {
		return errors.New("-profile and -profiles are mutually exclusive")
	}
	if set["output"] {
		return errors.New("-output and -profiles are mutually exclusive; use -outdir")
	}
	if o.outdir == "" {
		return errors.New("-profiles requires -outdir")
	}

	var err error
	if o.profiles, err = parseProfileList(o.profilesFlag); err != nil {
		return err
	}
	if o.runID == "" {
		o.runID = rand.Text()
	}

	return nil
}

// dataConfig returns the data configuration of one data set.
func (o *options) dataConfig(profile string) DataConfig {
	return DataConfig{
		NumMetrics:      o.numMetrics,
		PointsPerMetric: o.pointsPerMetric,
		ValueJitterPct:  o.valueJitter,
		TSJitterPct:     o.tsJitter,
		Seed:            42,
		Profile:         profile,
	}
}

// plan returns the measurement plan for these options; profiles is nil for a legacy run of -profile.
func (o *options) plan(runner timingRunner) *measurePlan {
	profiles := o.profiles
	if profiles == nil {
		profiles = []string{o.profile}
	}
	if o.sizesOnly {
		runner = refusingRunner{}
	}

	return &measurePlan{
		profiles:  profiles,
		cells:     o.cells,
		reverse:   o.order == orderReverse,
		sizesOnly: o.sizesOnly,
		base:      o.dataConfig(""),
		runner:    runner,
		verbose:   o.verbose,
	}
}
