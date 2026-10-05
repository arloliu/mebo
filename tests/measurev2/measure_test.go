package main

import (
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"weak"

	"github.com/stretchr/testify/require"
)

// smallConfig keeps the race-enabled `make test` run fast; the cell structure does not depend on the data size.
var smallConfig = DataConfig{NumMetrics: 4, PointsPerMetric: 12, ValueJitterPct: 0.5, TSJitterPct: 0.1, Seed: 42}

// recordingRunner stands in for testing.Benchmark: it calls each body once, untimed,
// records the order of preparations and runner calls, and optionally checks the checksum each body wrote.
type recordingRunner struct {
	t           *testing.T
	log         []string
	calls       []string
	inBody      bool
	checkBodies bool
	datasets    []string
	data        map[string][2]*TestData
	fixtures    map[string]*opFixtures
	result      RawOp
	failOn      string
	warmUps     int
}

// bodyRunner calls each body once and keeps no reference to anything.
type bodyRunner struct{}

func newRecordingRunner(t *testing.T) *recordingRunner {
	t.Helper()

	return &recordingRunner{
		t:        t,
		data:     make(map[string][2]*TestData),
		fixtures: make(map[string]*opFixtures),
		result:   RawOp{NsPerOp: 1000, BytesPerOp: 0, AllocsPerOp: 0, N: 1, TNs: 1000},
	}
}

// seedPlusOneIndices draws one lookup index per metric from Seed + 1, independently of randomAccessPattern.
func seedPlusOneIndices(d *TestData) []int {
	ppm := d.Config.PointsPerMetric
	indices := make([]int, len(d.MetricIDs))
	rng := rand.New(rand.NewSource(d.Config.Seed + 1))
	for i := range indices {
		indices[i] = rng.Intn(ppm)
	}

	return indices
}

// comboByLabel returns the measured combo with the given label.
func comboByLabel(t *testing.T, label string) EncodingCombo {
	t.Helper()
	for _, c := range measuredCombos() {
		if c.Label == label {
			return c
		}
	}
	t.Fatalf("no combo %s", label)

	return EncodingCombo{}
}

func testPlan(r *recordingRunner, profiles []string, cells string, reverse bool) *measurePlan {
	return &measurePlan{
		profiles: profiles,
		cells:    cells,
		reverse:  reverse,
		base:     smallConfig,
		runner:   r,
		trace:    r.trace,
	}
}

// requirePreparedBeforeRun checks that every runner call directly follows its own cell's preparation.
func requirePreparedBeforeRun(t *testing.T, log []string) {
	t.Helper()
	require.Zero(t, len(log)%2)
	for i := 0; i < len(log); i += 2 {
		cell := strings.TrimPrefix(log[i], "prepare ")
		require.Equal(t, "prepare "+cell, log[i])
		require.Equal(t, "run "+cell, log[i+1])
	}
}

func TestRunOrderForwardAndReverse(t *testing.T) {
	fwd := newRecordingRunner(t)
	fwd.checkBodies = true
	_, err := measureAll(testPlan(fwd, reportProfiles, cellsReport, false))
	require.NoError(t, err)

	want := manifestCells(reportProfiles, cellsReport)
	require.Equal(t, want, fwd.calls)
	require.Len(t, uniq(fwd.calls), 420, "each cell once")
	require.Equal(t, reportProfiles, fwd.datasets)
	requirePreparedBeforeRun(t, fwd.log)

	require.Equal(t, 1, fwd.warmUps, "one warm-up before the first timed cell")

	rev := newRecordingRunner(t)
	rev.checkBodies = true
	_, err = measureAll(testPlan(rev, reportProfiles, cellsReport, true))
	require.NoError(t, err)

	reversed := slices.Clone(want)
	slices.Reverse(reversed)
	require.Equal(t, reversed, rev.calls, "reverse runs data sets, combos and operations backwards")
	revProfiles := slices.Clone(reportProfiles)
	slices.Reverse(revProfiles)
	require.Equal(t, revProfiles, rev.datasets)
	requirePreparedBeforeRun(t, rev.log)
}

func TestFullCellsBodiesKeepTheirWork(t *testing.T) {
	// legacy_random_walk keeps its own shared-timestamp generator; mix_monitoring copies the first metric's timestamps.
	r := newRecordingRunner(t)
	r.checkBodies = true
	results, err := measureAll(testPlan(r, []string{"mix_monitoring", "legacy_random_walk"}, cellsFull, false))
	require.NoError(t, err)
	require.Len(t, r.calls, 300)
	for _, res := range results {
		for i := range res.rows {
			for _, op := range allOperations {
				require.NotNil(t, *res.rows[i].op(op), "%s %s", res.rows[i].Label, op)
			}
		}
	}
}

func TestMainProfileMeasuredOnce(t *testing.T) {
	r := newRecordingRunner(t)
	_, err := measureAll(testPlan(r, reportProfiles, cellsWide, false))
	require.NoError(t, err)
	require.Len(t, r.calls, 780)

	n := 0
	for _, c := range r.calls {
		if strings.HasPrefix(c, mainProfile+"/") {
			n++
		}
	}
	require.Equal(t, 150, n)
	require.Equal(t, 1, strings.Count(strings.Join(r.datasets, ","), mainProfile))
}

func TestRandomAccessPatternUsesSeedPlusOne(t *testing.T) {
	cfg := DataConfig{NumMetrics: 50, PointsPerMetric: 150, Seed: 42}
	data := &TestData{MetricIDs: make([]uint64, cfg.NumMetrics), Config: cfg}
	rng := rand.New(rand.NewSource(43))
	want := make([]int, cfg.NumMetrics)
	for i := range want {
		want[i] = rng.Intn(cfg.PointsPerMetric)
	}
	require.Equal(t, want, randomAccessPattern(data))
}

func TestBodiesKeepTheirChecks(t *testing.T) {
	p, _ := findProfile("counter")
	data, _ := generateDatasets(p, smallConfig)
	combo := comboByLabel(t, "delta-gorilla")

	_, body, err := prepareOp(opDecode, combo, data)
	require.NoError(t, err)
	require.NoError(t, body())
	fewer := *data
	fewer.MetricIDs = data.MetricIDs[:2]
	require.ErrorContains(t, decodeBody(&opFixtures{blob: mustEncode(t, combo, data)}, &fewer)(), "metric count mismatch")

	for _, op := range []operation{opValueAt, opTimestampAt} {
		fx, body, err := prepareOp(op, combo, data)
		require.NoError(t, err)
		require.NoError(t, body())
		fx.indices[1] = smallConfig.PointsPerMetric + 3
		require.Error(t, body(), "%s: a failed lookup is an error", op)
	}
}

func mustEncode(t *testing.T, combo EncodingCombo, data *TestData) []byte {
	t.Helper()
	b, err := encodeBlob(combo, data)
	require.NoError(t, err)

	return b
}

// TestSizesOnlyParity checks that -sizes-only, with the refusing runner installed,
// produces the same sizes and scaling series as the matrix path, and times nothing.
func TestSizesOnlyParity(t *testing.T) {
	profiles := []string{"mix_monitoring", "legacy_random_walk", "cal_2dp_hold50"}
	warmUps := 0
	sizesPlan := &measurePlan{profiles: profiles, cells: cellsFull, sizesOnly: true, base: smallConfig, runner: refusingRunner{},
		trace: func(ev traceEvent) {
			if ev.kind == traceWarmUp {
				warmUps++
			}
		}}
	sizes, err := measureAll(sizesPlan)
	require.NoError(t, err)

	require.Zero(t, warmUps, "-sizes-only times nothing, so it does not warm up")
	r := newRecordingRunner(t)
	matrix, err := measureAll(testPlan(r, profiles, cellsFull, false))
	require.NoError(t, err)

	for _, name := range profiles {
		s, m := sizes[name], matrix[name]
		require.Equal(t, m.rawRaw, s.rawRaw, name)
		require.Equal(t, m.scaling, s.scaling, name)
		require.Len(t, s.rows, 30)
		for i := range s.rows {
			require.Equal(t, m.rows[i].MatrixSizes, s.rows[i].MatrixSizes, name)
			for _, op := range allOperations {
				require.Nil(t, *s.rows[i].op(op), "%s %s %s timed in -sizes-only", name, s.rows[i].Label, op)
			}
		}
	}
}

func TestLegacyReportNeedsEveryOperation(t *testing.T) {
	r := newRecordingRunner(t)
	results, err := measureAll(testPlan(r, []string{"counter"}, cellsReport, false))
	require.NoError(t, err)
	_, err = legacyReport(results["counter"], ReportMetadata{})
	require.Error(t, err)

	results, err = measureAll(testPlan(newRecordingRunner(t), []string{"counter"}, cellsFull, false))
	require.NoError(t, err)
	report, err := legacyReport(results["counter"], ReportMetadata{})
	require.NoError(t, err)
	require.Len(t, report.Matrix, 30)
	require.Len(t, report.Scaling, 30)
}

func TestScalingPointCounts(t *testing.T) {
	for _, tc := range []struct {
		max  int
		want []int
	}{
		{1, []int{1}},
		{3, []int{1, 2, 3}},
		{7, []int{1, 2, 5, 7}},
		{12, []int{1, 2, 5, 10}},
		{150, []int{1, 2, 5, 10, 20, 50, 100, 150}},
		{175, []int{1, 2, 5, 10, 20, 50, 100, 150}},
		{1300, []int{1, 2, 5, 10, 20, 50, 100, 150, 200, 500, 1000, 1300}},
	} {
		require.Equal(t, tc.want, scalingPointCounts(tc.max), "max %d", tc.max)
	}

	p, _ := findProfile("counter")
	cfg := smallConfig
	cfg.PointsPerMetric = 7
	data, _ := generateDatasets(p, cfg)
	res, err := runScaling(comboByLabel(t, "delta-chimp"), data)
	require.NoError(t, err)
	require.Len(t, res.Series, 4)
	require.Equal(t, 7, res.Series[3].PointsPerMetric)
}

func TestNonLegacySharedTimestampsCopyTheFirstMetric(t *testing.T) {
	p, _ := findProfile("mix_sensor")
	cfg := DataConfig{NumMetrics: 20, PointsPerMetric: 150, ValueJitterPct: 0.5, TSJitterPct: 0.1, Seed: 42}
	data, shared := generateDatasets(p, cfg)
	require.Equal(t, data.shareTimestamps().Timestamps, shared.Timestamps)
	require.Equal(t, data.Values, shared.Values)
	require.Equal(t, "a25a30fac86a39dd5f626e34", hashTestData(shared))
}

func TestRunProfilesSingletonWritesNoMainJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	o := mustParse(t, "-profiles", "legacy_random_walk", "-outdir", dir, "-metrics", "4", "-points", "12")
	r := newRecordingRunner(t)
	require.NoError(t, runProfiles(o, r))

	require.Len(t, r.calls, 150)
	for _, c := range r.calls {
		require.True(t, strings.HasPrefix(c, "legacy_random_walk/"), c)
	}
	require.Equal(t, []string{"profiles"}, dirNames(t, dir))
	require.Equal(t, []string{"matrix_legacy_random_walk.json"}, dirNames(t, filepath.Join(dir, "profiles")))
}

func TestRunProfilesMainAlias(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	o := mustParse(t, "-profiles", "counter,mix_monitoring", "-cells", "report", "-outdir", dir, "-metrics", "4", "-points", "12")
	r := newRecordingRunner(t)
	require.NoError(t, runProfiles(o, r))
	require.Len(t, r.calls, 150+18)

	require.Equal(t, []string{"main.json", "profiles"}, dirNames(t, dir))
	mainJSON, err := os.ReadFile(filepath.Join(dir, "main.json"))
	require.NoError(t, err)
	alias, err := os.ReadFile(filepath.Join(dir, "profiles", "matrix_mix_monitoring.json"))
	require.NoError(t, err)
	require.Equal(t, alias, mainJSON, "main.json and its profile file come from the same measurements")
}

func TestRunProfilesExistingOutdir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep"), []byte("x"), 0o600))
	o := mustParse(t, "-profiles", "counter", "-outdir", dir)
	r := newRecordingRunner(t)
	require.Error(t, runProfiles(o, r))
	require.Empty(t, r.calls, "nothing measured")
	require.Equal(t, []string{"keep"}, dirNames(t, dir), "nothing written")
}

func TestRunProfilesFailurePublishesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	o := mustParse(t, "-profiles", "mix_monitoring,counter", "-cells", "report", "-outdir", dir, "-metrics", "4", "-points", "12")
	r := newRecordingRunner(t)
	r.failOn = "counter/delta-gorilla/iter_seq"
	require.ErrorContains(t, runProfiles(o, r), "injected failure")
	require.Empty(t, dirNames(t, dir))
}

// TestDatasetLifecycle checks that a data set's data and fixtures are unreachable once the next data set starts.
// It forces garbage collections, so it runs outside `make test` (validate.sh runs it without -short).
func TestDatasetLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("GC lifecycle check; run without -short (validate.sh)")
	}

	var (
		data     []weak.Pointer[TestData]
		fixtures []weak.Pointer[opFixtures]
		checked  int
	)
	trace := func(ev traceEvent) {
		switch ev.kind {
		case traceDataset:
			if data != nil {
				runtime.GC() //nolint:revive // the check needs collections
				runtime.GC() //nolint:revive // a second cycle frees what the first one finalized
				for _, w := range data {
					require.Nil(t, w.Value(), "previous data set still reachable when %s starts", ev.profile)
				}
				for _, w := range fixtures {
					require.Nil(t, w.Value(), "previous fixtures still reachable when %s starts", ev.profile)
				}
				checked++
			}
			data = []weak.Pointer[TestData]{weak.Make(ev.data), weak.Make(ev.shared)}
			fixtures = nil
		case tracePrepare:
			fixtures = append(fixtures, weak.Make(ev.fixtures))
		case traceWarmUp:
		default:
			t.Errorf("unknown trace event %s", ev.kind)
		}
	}
	plan := &measurePlan{profiles: reportProfiles[:4], cells: cellsReport, base: smallConfig, runner: bodyRunner{}, trace: trace}
	_, err := measureAll(plan)
	require.NoError(t, err)
	require.Equal(t, 3, checked)
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names
}

func (r *recordingRunner) trace(ev traceEvent) {
	if r.inBody {
		r.t.Errorf("trace %s %s inside a timed body", ev.kind, ev.cell)
	}
	switch ev.kind {
	case traceDataset:
		r.datasets = append(r.datasets, ev.profile)
		r.data[ev.profile] = [2]*TestData{ev.data, ev.shared}
	case tracePrepare:
		r.log = append(r.log, "prepare "+ev.cell)
		r.fixtures[ev.cell] = ev.fixtures
	case traceWarmUp:
		r.warmUps++
		if len(r.datasets) > 0 || len(r.calls) > 0 {
			r.t.Errorf("warm-up after the first data set or timed cell")
		}
	default:
		r.t.Errorf("unknown trace event %s", ev.kind)
	}
}

func (r *recordingRunner) run(cell string, body func() error) (RawOp, error) {
	if r.inBody {
		r.t.Errorf("runner called for %s inside a timed body", cell)
	}
	r.log = append(r.log, "run "+cell)
	r.calls = append(r.calls, cell)
	if cell == r.failOn {
		return RawOp{}, errors.New("injected failure")
	}

	if r.checkBodies {
		r.checkPrepared(cell)
	}

	r.inBody = true
	err := body()
	r.inBody = false
	if err != nil {
		return RawOp{}, err
	}
	if r.checkBodies {
		r.checkChecksum(cell)
	}

	return r.result, nil
}

// checkPrepared checks, at runner entry and before the body runs, that the cell's preparation is complete and no more:
// encode prepares nothing, decode only the encoded blob,
// iterate and the lookups the decoded blob, and the lookups also their Seed + 1 indices.
func (r *recordingRunner) checkPrepared(cell string) {
	parts := strings.Split(cell, "/")
	profile, label, opName := parts[0], parts[1], parts[2]
	d := r.data[profile][0]
	if strings.HasPrefix(label, "shared-") {
		d = r.data[profile][1]
	}
	fx := r.fixtures[cell]
	require.NotNil(r.t, fx, cell)

	if opName == "encode" {
		require.Nil(r.t, fx.blob, "%s: encode builds its blob inside the body", cell)
		require.Zero(r.t, fx.decoded.MetricCount(), cell)
		require.Nil(r.t, fx.indices, cell)

		return
	}
	require.Equal(r.t, mustEncode(r.t, comboByLabel(r.t, label), d), fx.blob, "%s: blob encoded before timing", cell)
	if opName == "decode" {
		require.Zero(r.t, fx.decoded.MetricCount(), "%s: decode must not be pre-decoded", cell)
		require.Nil(r.t, fx.indices, cell)

		return
	}
	require.Equal(r.t, len(d.MetricIDs), fx.decoded.MetricCount(), "%s: blob decoded before timing", cell)
	if opName == "iter_seq" {
		require.Nil(r.t, fx.indices, cell)

		return
	}
	require.Equal(r.t, seedPlusOneIndices(d), fx.indices, "%s: lookup indices prepared before timing", cell)
}

// checkChecksum compares what a body computed with the same quantity computed directly from the data,
// with lookup indices drawn independently from Seed + 1.
func (r *recordingRunner) checkChecksum(cell string) {
	parts := strings.Split(cell, "/")
	profile, label, opName := parts[0], parts[1], parts[2]
	ds := r.data[profile]
	d := ds[0]
	if strings.HasPrefix(label, "shared-") {
		d = ds[1]
	}
	fx := r.fixtures[cell]
	ppm := d.Config.PointsPerMetric
	indices := seedPlusOneIndices(d)

	switch opName {
	case "encode":
		blobData, err := encodeBlob(comboByLabel(r.t, label), d)
		require.NoError(r.t, err)
		require.InDelta(r.t, float64(len(blobData)), fx.checksum, 0, cell)
	case "decode":
		require.InDelta(r.t, float64(len(d.MetricIDs)), fx.checksum, 0, cell)
	case "iter_seq":
		want := 0.0
		for _, v := range d.Values {
			want += v
		}
		require.InDelta(r.t, want, fx.checksum, 0, "%s: iteration must visit every point", cell)
	case "random_value_at":
		want := 0.0
		for i, idx := range indices {
			want += d.Values[i*ppm+idx]
		}
		require.InDelta(r.t, want, fx.checksum, 0, "%s: one lookup per metric at the Seed+1 indices", cell)
	case "random_timestamp_at":
		var want int64
		for i, idx := range indices {
			want += d.Timestamps[i*ppm+idx]
		}
		require.Equal(r.t, want, fx.tsChecksum, "%s: one lookup per metric at the Seed+1 indices", cell)
	default:
		r.t.Fatalf("unknown operation in %s", cell)
	}
}

func (bodyRunner) run(_ string, body func() error) (RawOp, error) {
	return RawOp{NsPerOp: 1, N: 1, TNs: 1}, body()
}
