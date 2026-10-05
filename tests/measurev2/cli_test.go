package main

import (
	"flag"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func mustParse(t *testing.T, args ...string) *options {
	t.Helper()
	o, err := parseOptions(args, io.Discard)
	require.NoError(t, err)

	return o
}

func TestParseOptionsDefaultsAreLegacy(t *testing.T) {
	o := mustParse(t)
	require.Nil(t, o.profiles)
	require.Equal(t, DefaultProfile, o.profile)
	require.Equal(t, cellsFull, o.cells)
	require.Equal(t, "1s", o.benchtime)
	require.Equal(t, orderForward, o.order)
	require.Equal(t, -1, o.layout)
	require.Equal(t, -1, o.round)
	require.Zero(t, o.rounds)

	o = mustParse(t, "-profile", "")
	require.Equal(t, "legacy_random_walk", o.profile, "an empty -profile selects legacy_random_walk, as before")
}

func TestParseOptionsProfiles(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	o := mustParse(t, "-profiles", "report", "-outdir", out, "-cells", "report", "-benchtime", "50ms",
		"-order", "reverse", "-layout", "2", "-round", "3", "-rounds", "4", "-source", "s", "-tools", "x", "-run-id", "r1")
	require.Equal(t, reportProfiles, o.profiles)
	require.Equal(t, "50ms", o.benchtime)
	require.Equal(t, "r1", o.runID)

	o = mustParse(t, "-profiles", "counter", "-outdir", out)
	require.NotEmpty(t, o.runID, "a -profiles run always has a run id")
	require.NotEqual(t, o.runID, mustParse(t, "-profiles", "counter", "-outdir", out).runID)

	o = mustParse(t, "-profiles", "counter", "-outdir", out, "-benchtime", "0.05s")
	require.Equal(t, "50ms", o.benchtime, "normalized")
}

func TestParseOptionsConflicts(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	for _, args := range [][]string{
		{"-profiles", "counter", "-profile", "counter", "-outdir", out},
		{"-profiles", "counter", "-output", "x.json", "-outdir", out},
		{"-profiles", "counter"},
		{"-profiles", "counter,counter", "-outdir", out},
		{"-profiles", "nope", "-outdir", out},
		{"-profiles", "", "-outdir", out},
		{"-outdir", out},
		{"-sizes-only"},
		{"-order", "reverse"},
		{"-run-id", "x"},
		{"-source", "x"},
		{"-tools", "x"},
		{"-layout", "0"},
		{"-round", "1"},
		{"-rounds", "4"},
		{"-cells", "report"},
		{"-cells", "wide"},
		{"-profiles", "counter", "-outdir", out, "-cells", "some"},
		{"-benchtime", "5ms"},
		{"-benchtime", "11s"},
		{"-benchtime", "100x"},
		{"-benchtime", "fast"},
		{"-profiles", "counter", "-outdir", out, "-order", "sideways"},
		{"-profiles", "counter", "-outdir", out, "-rounds", "3"},
		{"-profiles", "counter", "-outdir", out, "-rounds", "2", "-round", "3"},
		{"-profiles", "counter", "-outdir", out, "-round", "1"},
		{"-profiles", "counter", "-outdir", out, "-rounds", "4", "-round", "0"},
		{"-profiles", "counter", "-outdir", out, "-layout", "-2"},
		{"-profile", "nope"},
		{"-metrics", "0"},
		{"-points", "-1"},
		{"extra"},
		{"-nosuchflag"},
	} {
		_, err := parseOptions(args, io.Discard)
		require.Error(t, err, "%v", args)
	}
}

func TestParseOptionsHelp(t *testing.T) {
	_, err := parseOptions([]string{"-help"}, io.Discard)
	require.ErrorIs(t, err, flag.ErrHelp)
}

// TestBenchtimeReachesTestingFlag checks that -benchtime sets -test.benchtime, which testing.Init registered.
// Whether testing.Benchmark then aims at it is checked by validate.sh, which times real cells.
func TestBenchtimeReachesTestingFlag(t *testing.T) {
	testing.Init()
	f := flag.Lookup("test.benchtime")
	require.NotNil(t, f)
	old := f.Value.String()
	t.Cleanup(func() { _ = flag.Set("test.benchtime", old) })

	o := mustParse(t, "-benchtime", "50ms")
	require.NoError(t, applyBenchtime(o.benchtime))
	require.Equal(t, "50ms", flag.Lookup("test.benchtime").Value.String())
}
