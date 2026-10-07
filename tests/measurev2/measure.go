package main

import (
	"errors"
	"fmt"
	"slices"
)

// Trace event kinds, emitted to measurePlan.trace for tests.
const (
	traceWarmUp  = "warmup"
	traceDataset = "dataset"
	tracePrepare = "prepare"
)

// measurePlan describes what one invocation measures and how it times it.
type measurePlan struct {
	profiles  []string // requested data sets, in manifest order
	cells     string
	reverse   bool
	sizesOnly bool
	base      DataConfig // the data configuration shared by every data set; Profile is set per data set
	runner    timingRunner
	trace     func(traceEvent)
	verbose   bool
}

// traceEvent reports a step of the measurement to tests: a data set generated, or a cell's fixtures prepared.
type traceEvent struct {
	kind     string
	profile  string
	cell     string
	data     *TestData
	shared   *TestData
	fixtures *opFixtures
}

// datasetResult holds one data set's measurements; it keeps no reference to the generated data.
type datasetResult struct {
	config  DataConfig
	spec    Profile
	rawRaw  int
	rows    []MatrixRow[RawOp] // in measuredCombos order, whatever the run order
	scaling []ScalingResult
}

// measureAll measures every requested data set in run order, one at a time,
// so a data set's data and fixtures are unreachable before the next data set is generated.
// Unless nothing is timed, it first warms the process up (see warmUp).
func measureAll(plan *measurePlan) (map[string]*datasetResult, error) {
	results := make(map[string]*datasetResult, len(plan.profiles))
	if !plan.sizesOnly {
		if err := warmUp(plan); err != nil {
			return nil, fmt.Errorf("warm-up: %w", err)
		}
	}
	for _, name := range runOrder(plan, plan.profiles) {
		res, err := measureDataset(name, plan)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		results[name] = res
	}

	return results, nil
}

// measureDataset generates one data set and measures it: the raw-raw baseline,
// then per combo in run order its sizes followed by its timed cells, then the scaling series of every combo.
func measureDataset(name string, plan *measurePlan) (*datasetResult, error) {
	spec, ok := findProfile(name)
	if !ok {
		return nil, fmt.Errorf("unknown profile %q", name)
	}
	cfg := plan.base
	cfg.Profile = name

	if plan.verbose {
		logf("Generating test data (profile %s): %d metrics × %d points...\n", name, cfg.NumMetrics, cfg.PointsPerMetric)
	}
	data, shared := generateDatasets(spec, cfg)
	plan.emit(traceEvent{kind: traceDataset, profile: name, data: data, shared: shared})

	combos := measuredCombos()
	rawRaw, err := rawRawBaseline(combos, data)
	if err != nil {
		return nil, fmt.Errorf("measuring raw-raw baseline: %w", err)
	}

	rows := make([]MatrixRow[RawOp], len(combos))
	order := runOrder(plan, comboIndices(len(combos)))
	for k, i := range order {
		c := combos[i]
		d := data
		if c.SharedTS {
			d = shared
		}
		if rows[i].MatrixSizes, err = measureSizes(c, d, rawRaw); err != nil {
			return nil, fmt.Errorf("encoding %s: %w", c.Label, err)
		}
		if plan.verbose {
			logf("  [%d/%d] %s: %.3f bytes/point\n", k+1, len(order), c.Label, rows[i].BytesPerPoint)
		}
		if err := plan.timeCombo(name, c, d, &rows[i]); err != nil {
			return nil, err
		}
	}

	scaling, err := scaleCombos(combos, data, shared)
	if err != nil {
		return nil, err
	}

	return &datasetResult{config: cfg, spec: spec, rawRaw: rawRaw, rows: rows, scaling: scaling}, nil
}

// warmUp encodes every combo of the main data set once, untimed, before the first timed cell.
// A fresh process spends its first ~100 ms of allocation-heavy work in a runtime GC state
// in which encodes overlap GC marking less often than in steady state;
// without the warm-up, the first data set a process measured encoded up to 16% faster than the same cells later on
// (measured while Gorilla and Chimp spills still took write barriers, which made them the most GC-sensitive encodes).
func warmUp(plan *measurePlan) error {
	spec, ok := findProfile(mainProfile)
	if !ok {
		return fmt.Errorf("unknown profile %q", mainProfile)
	}
	cfg := plan.base
	cfg.Profile = mainProfile
	data, shared := generateDatasets(spec, cfg)
	for _, c := range measuredCombos() {
		d := data
		if c.SharedTS {
			d = shared
		}
		if _, err := encodeBlob(c, d); err != nil {
			return fmt.Errorf("encoding %s: %w", c.Label, err)
		}
	}
	plan.emit(traceEvent{kind: traceWarmUp, profile: mainProfile})

	return nil
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

// measureSizes encodes one combo and derives its size fields against the raw-raw baseline.
func measureSizes(combo EncodingCombo, data *TestData, rawRawSize int) (MatrixSizes, error) {
	encodedSize, err := measureEncodedSize(combo, data)
	if err != nil {
		return MatrixSizes{}, err
	}
	totalPoints := data.Config.NumMetrics * data.Config.PointsPerMetric

	return MatrixSizes{
		Label:           combo.Label,
		TSEncoding:      combo.TSEncoding.String(),
		ValEncoding:     combo.ValEncoding.String(),
		NumMetrics:      data.Config.NumMetrics,
		PointsPerMetric: data.Config.PointsPerMetric,
		TotalPoints:     totalPoints,
		EncodedBytes:    encodedSize,
		BytesPerPoint:   float64(encodedSize) / float64(totalPoints),
		VsRawRatio:      float64(rawRawSize) / float64(encodedSize),
		SpaceSavingsPct: (1.0 - float64(encodedSize)/float64(rawRawSize)) * 100.0,
	}, nil
}

// scaleCombos runs the scaling analysis for every combo, per-metric timestamp combos on data and shared ones on shared.
func scaleCombos(combos []EncodingCombo, data, shared *TestData) ([]ScalingResult, error) {
	results := make([]ScalingResult, 0, len(combos))
	for _, c := range combos {
		d := data
		if c.SharedTS {
			d = shared
		}
		res, err := runScaling(c, d)
		if err != nil {
			return nil, fmt.Errorf("scaling %s: %w", c.Label, err)
		}
		results = append(results, res)
	}

	return results, nil
}

// comboIndices returns 0..n-1.
func comboIndices(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}

	return idx
}

// legacyReport converts a fully timed data set to the legacy schema.
func legacyReport(res *datasetResult, meta ReportMetadata) (FullReport, error) {
	matrix := make([]MatrixResult, 0, len(res.rows))
	for i := range res.rows {
		r := &res.rows[i]
		if r.Encode == nil || r.Decode == nil || r.IterSeq == nil || r.RandomValueAt == nil || r.RandomTimestampAt == nil {
			return FullReport{}, fmt.Errorf("%s: the legacy schema needs every operation timed", r.Label)
		}
		matrix = append(matrix, MatrixResult{
			MatrixSizes:       r.MatrixSizes,
			Encode:            legacyMetrics(r.Encode),
			Decode:            legacyMetrics(r.Decode),
			IterSeq:           legacyMetrics(r.IterSeq),
			RandomValueAt:     legacyMetrics(r.RandomValueAt),
			RandomTimestampAt: legacyMetrics(r.RandomTimestampAt),
		})
	}

	return FullReport{Metadata: meta, Matrix: matrix, Scaling: res.scaling}, nil
}

// runOrder returns items in run order: as given for -order forward, reversed for -order reverse.
func runOrder[T any](p *measurePlan, items []T) []T {
	if !p.reverse {
		return items
	}
	out := slices.Clone(items)
	slices.Reverse(out)

	return out
}

// emit passes a trace event to the plan's trace hook, if any.
func (p *measurePlan) emit(ev traceEvent) {
	if p.trace != nil {
		p.trace(ev)
	}
}

// timeCombo times the cells of one combo that the plan's -cells mode selects, preparing each just before its runner call.
// It times nothing with -sizes-only.
func (p *measurePlan) timeCombo(profile string, c EncodingCombo, d *TestData, row *MatrixRow[RawOp]) error {
	if p.sizesOnly {
		return nil
	}
	for _, op := range runOrder(p, timedOps(profile, p.cells, c.Label)) {
		id := cellID(profile, c.Label, op)
		fx, body, err := prepareOp(op, c, d)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		p.emit(traceEvent{kind: tracePrepare, profile: profile, cell: id, fixtures: fx})
		if p.verbose {
			// One line per timed cell, so a stamped stderr gives every cell's start (cellperf.py analyze).
			logf("    cell %s\n", id)
		}

		res, err := p.runner.run(id, body)
		if err != nil {
			return err
		}
		*row.op(op) = &res
	}

	return nil
}
