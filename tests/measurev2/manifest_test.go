package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManifestCellCounts(t *testing.T) {
	for _, tc := range []struct {
		cells string
		want  int
	}{
		{cellsReport, 420},
		{cellsWide, 780},
		{cellsFull, 2400},
	} {
		ids := manifestCells(reportProfiles, tc.cells)
		require.Len(t, ids, tc.want, tc.cells)
		require.Len(t, uniq(ids), tc.want, "%s: duplicate cell ids", tc.cells)
	}
}

func TestReportProfilesManifest(t *testing.T) {
	require.Len(t, reportProfiles, 16)
	require.Equal(t, mainProfile, reportProfiles[0], "the main data set runs first")
	require.NotContains(t, reportProfiles, "regular_scrape_60s")
	require.NotContains(t, reportProfiles, "bursty_scrape")
	for _, name := range reportProfiles {
		_, ok := findProfile(name)
		require.True(t, ok, name)
	}

	all := manifestProfiles()
	require.Len(t, all, len(Profiles()), "all is the generator catalog")
	require.Equal(t, reportProfiles, all[:len(reportProfiles)])
}

// TestReportCellsCoverGenerateReport checks the cells generate_report.py reads:
// every operation of every combo on the main data set,
// and on every other report profile encode, iterate and ValueAt of the five Shared DeltaPacked combos and Delta + Gorilla.
func TestReportCellsCoverGenerateReport(t *testing.T) {
	ids := manifestCells(reportProfiles, cellsReport)
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}

	for _, c := range measuredCombos() {
		for _, op := range allOperations {
			require.True(t, set[cellID(mainProfile, c.Label, op)], "main %s/%s", c.Label, op)
		}
	}
	for _, p := range reportProfiles[1:] {
		for _, v := range []string{"raw", "gorilla", "chimp", "alp", "alprle"} {
			for _, op := range []operation{opEncode, opIterSeq, opValueAt} {
				require.True(t, set[cellID(p, "shared-deltapacked-"+v, op)], "%s shared-deltapacked-%s/%s", p, v, op)
			}
			require.False(t, set[cellID(p, "shared-deltapacked-"+v, opDecode)])
			require.False(t, set[cellID(p, "shared-deltapacked-"+v, opTimestampAt)])
		}
		for _, op := range []operation{opEncode, opIterSeq, opValueAt} {
			require.True(t, set[cellID(p, "delta-gorilla", op)], "%s delta-gorilla/%s", p, op)
		}
	}
}

func TestWideCellsAddIterate(t *testing.T) {
	report := manifestCells([]string{"counter"}, cellsReport)
	wide := manifestCells([]string{"counter"}, cellsWide)
	require.Len(t, report, 18)
	require.Len(t, wide, 42)
	for _, id := range wide {
		if !slices.Contains(report, id) {
			require.True(t, strings.HasSuffix(id, "/iter_seq"), id)
		}
	}
}

func TestParseProfileList(t *testing.T) {
	got, err := parseProfileList("report")
	require.NoError(t, err)
	require.Equal(t, reportProfiles, got)

	got, err = parseProfileList("all")
	require.NoError(t, err)
	require.Len(t, got, 18)

	got, err = parseProfileList("legacy_random_walk")
	require.NoError(t, err)
	require.Equal(t, []string{"legacy_random_walk"}, got)

	got, err = parseProfileList("bursty_scrape,counter,mix_monitoring")
	require.NoError(t, err)
	require.Equal(t, []string{"mix_monitoring", "counter", "bursty_scrape"}, got, "manifest order")

	for _, bad := range []string{"", "counter,counter", "counter,nope", "counter,,mix_sensor", "Report"} {
		_, err := parseProfileList(bad)
		require.Error(t, err, bad)
	}
}

func TestCellsDigestIsOrderFree(t *testing.T) {
	ids := manifestCells(reportProfiles, cellsReport)
	rev := slices.Clone(ids)
	slices.Reverse(rev)
	require.Equal(t, cellsDigest(ids), cellsDigest(rev))
	require.NotEqual(t, cellsDigest(ids), cellsDigest(ids[1:]))
}

func uniq(ids []string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)

	return slices.Compact(out)
}
