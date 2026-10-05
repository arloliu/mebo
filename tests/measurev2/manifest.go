package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// mainProfile is the data set behind the report's main tables and main.json;
// it times every operation of every combo in every -cells mode.
const mainProfile = DefaultProfile

// -cells modes.
const (
	cellsFull   = "full"
	cellsReport = "report"
	cellsWide   = "wide"
)

// Operations timed per combo, in forward run order.
const (
	opEncode operation = iota
	opDecode
	opIterSeq
	opValueAt
	opTimestampAt
)

var (
	// reportProfiles is the frozen list of data sets the performance report measures, in run order:
	// the main data set first, then the 15 profiles of the profile tables.
	// `-profiles report` measures exactly these;
	// the update-performance-report skill and its template point here.
	reportProfiles = []string{
		"mix_monitoring", "mix_sensor", "mix_integer", "mix_fullprec",
		"decimal_gauge_2dp", "decimal_gauge_4dp", "counter", "sparse_constant", "worst_case",
		"cal_2dp_hold30", "cal_2dp_hold50", "cal_2dp_hold70", "cal_2dp_step0.005",
		"cal_1dp_step0.03", "cal_1dp_step0.01", "legacy_random_walk",
	}

	// reportProfileCombos are the combos timed on a data set other than the main one with `-cells report`:
	// the production-like timestamp layout with every value codec, and the library default.
	reportProfileCombos = []string{
		"shared-deltapacked-raw", "shared-deltapacked-gorilla", "shared-deltapacked-chimp",
		"shared-deltapacked-alp", "shared-deltapacked-alprle", "delta-gorilla",
	}

	// reportProfileOps are the operations timed on reportProfileCombos.
	reportProfileOps = []operation{opEncode, opIterSeq, opValueAt}

	// allOperations lists every operation in forward run order.
	allOperations = []operation{opEncode, opDecode, opIterSeq, opValueAt, opTimestampAt}
)

// operation is one timed operation of a combo; its String is the JSON key of the operation object.
type operation int

// manifestProfiles returns every catalog profile in manifest order:
// the report profiles in their run order, then the rest of the catalog in catalog order.
func manifestProfiles() []string {
	names := slices.Clone(reportProfiles)
	for _, p := range Profiles() {
		if !slices.Contains(names, p.Name) {
			names = append(names, p.Name)
		}
	}

	return names
}

// parseProfileList resolves a -profiles value ("report", "all" or a comma list) to profile names in manifest order.
// Duplicate, unknown and empty names are errors.
func parseProfileList(s string) ([]string, error) {
	switch s {
	case "report":
		return slices.Clone(reportProfiles), nil
	case "all":
		return manifestProfiles(), nil
	}

	seen := make(map[string]bool)
	for name := range strings.SplitSeq(s, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, errors.New("-profiles: empty profile name")
		}
		if _, ok := findProfile(name); !ok {
			return nil, fmt.Errorf("-profiles: unknown profile %q; available: %s", name, profileNames())
		}
		if seen[name] {
			return nil, fmt.Errorf("-profiles: duplicate profile %q", name)
		}
		seen[name] = true
	}

	names := make([]string, 0, len(seen))
	for _, name := range manifestProfiles() {
		if seen[name] {
			names = append(names, name)
		}
	}

	return names, nil
}

// validCells reports whether s is a -cells mode.
func validCells(s string) bool {
	return s == cellsFull || s == cellsReport || s == cellsWide
}

// measuredCombos returns every combo measured on a data set, in forward run order:
// the per-metric timestamp combos, then the shared-timestamp combos.
func measuredCombos() []EncodingCombo {
	return append(AllCombos(), SharedTSCombos()...)
}

// timedOps returns the operations timed for one combo of one data set under a -cells mode, in forward run order.
// The main data set times every operation of every combo in every mode.
func timedOps(profile, cells, combo string) []operation {
	if profile == mainProfile || cells == cellsFull {
		return allOperations
	}
	if slices.Contains(reportProfileCombos, combo) {
		return reportProfileOps
	}
	if cells == cellsWide {
		return []operation{opIterSeq}
	}

	return nil
}

// cellID names one timed cell: <profile>/<combo label>/<operation>.
func cellID(profile, combo string, op operation) string {
	return profile + "/" + combo + "/" + op.String()
}

// manifestCells lists the cell ids timed for profiles under a -cells mode, in forward run order.
func manifestCells(profiles []string, cells string) []string {
	ids := make([]string, 0, len(profiles)*len(allOperations)*30)
	for _, p := range profiles {
		for _, c := range measuredCombos() {
			for _, op := range timedOps(p, cells, c.Label) {
				ids = append(ids, cellID(p, c.Label, op))
			}
		}
	}

	return ids
}

// cellsDigest returns the SHA-256 of the sorted cell ids, one per line.
func cellsDigest(ids []string) string {
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))

	return hex.EncodeToString(sum[:])
}

// String returns the operation's JSON key, which is also the last part of its cell id.
func (o operation) String() string {
	switch o {
	case opEncode:
		return "encode"
	case opDecode:
		return "decode"
	case opIterSeq:
		return "iter_seq"
	case opValueAt:
		return "random_value_at"
	case opTimestampAt:
		return "random_timestamp_at"
	}

	return fmt.Sprintf("operation(%d)", int(o))
}
