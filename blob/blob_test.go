package blob

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// codecPackagePrefix prefixes the codec packages behind the internal/encoding facade.
const codecPackagePrefix = "github.com/arloliu/mebo/internal/encoding/"

// importPaths returns the sorted import paths of a Go file that start with
// codecPackagePrefix, keeping only blank imports when blankOnly is set.
func importPaths(t *testing.T, path string, blankOnly bool) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	require.NoError(t, err)

	var paths []string
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		require.NoError(t, err)
		if !strings.HasPrefix(importPath, codecPackagePrefix) {
			continue
		}
		if blankOnly && (spec.Name == nil || spec.Name.Name != "_") {
			continue
		}
		paths = append(paths, importPath)
	}
	slices.Sort(paths)

	return paths
}

// TestCodecBlankImportsMatchFacade pins that blob blank-imports every codec
// package the facade aliases, so a codec added to the facade cannot silently
// lose method inlining inside blob.
func TestCodecBlankImportsMatchFacade(t *testing.T) {
	facade := importPaths(t, "../internal/encoding/facade.go", false)
	require.NotEmpty(t, facade)
	require.Equal(t, facade, importPaths(t, "blob.go", true))
}

// TestHotPathCodecMethodsInline pins that the per-point codec methods reached
// through facade aliases inline into blob's decode and encode loops, and that
// the codecs' iterator constructors do not.
// It builds this package with -gcflags=-m and checks the compiler's report.
func TestHotPathCodecMethodsInline(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}

	// -gcflags=all= resets compiler flags for every package, overriding any set
	// through GOFLAGS (environment or go env -w), so inherited flags cannot
	// disable inlining in a dependency; -gcflags=-m then applies to blob only.
	cmd := exec.Command(goTool, "build", "-gcflags=all=", "-gcflags=-m", "-o", os.DevNull, ".")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	report := packageDiagnostics(string(out), "github.com/arloliu/mebo/blob")
	require.NotEmpty(t, report, "no compiler diagnostics for blob")
	for _, method := range []string{
		"delta.(*DeltaTsState).NextShort",
		"delta.(*DeltaTsState).Ts",
		"gorilla.(*GorillaValState).Next",
		"gorilla.(*GorillaValState).Val",
		"chimp.(*ChimpValState).Next",
		"chimp.(*ChimpValState).Val",
		"metadata.(*VarStringEncoder).WriteVarint",
	} {
		require.Containsf(t, report, "inlining call to "+method, "%s is not inlined into blob", method)
	}

	// Iterator constructors must stay out of line: an inlined copy of their
	// closure would lose every per-element inline.
	for _, method := range []string{
		"delta.TimestampDeltaDecoder.All",
		"deltapacked.TimestampDeltaPackedDecoder.All",
		"raw.TimestampRawDecoder.All",
		"raw.TimestampRawUnsafeDecoder.All",
		"gorilla.NumericGorillaDecoder.All",
		"chimp.NumericChimpDecoder.All",
		"alp.NumericALPDecoder.All",
		"raw.NumericRawDecoder.All",
		"raw.NumericRawUnsafeDecoder.All",
		"metadata.TagDecoder.All",
	} {
		require.NotContainsf(t, report, "inlining call to "+method, "%s is inlined into blob", method)
	}
}

// packageDiagnostics returns the compiler diagnostics that go build prints
// under the "# <importPath>" header, so matches cannot come from another package.
func packageDiagnostics(output, importPath string) string {
	var b strings.Builder
	inPackage := false
	for line := range strings.Lines(output) {
		if header, ok := strings.CutPrefix(line, "# "); ok {
			inPackage = strings.TrimSpace(header) == importPath
			continue
		}
		if inPackage {
			b.WriteString(line)
		}
	}

	return b.String()
}

// TestIndexMapsEntryLookups pins the pointer lookups on each index layout:
// they return the element of sorted that the lookup rules select, or nil,
// and the by-value lookups return a copy of that element.
func TestIndexMapsEntryLookups(t *testing.T) {
	entry := func(id uint64, count int) section.NumericIndexEntry {
		return section.NumericIndexEntry{MetricID: id, Count: count}
	}
	cpuID := hash.ID("cpu")

	v1 := newNumericTestIndex(entry(30, 1), entry(10, 2), entry(cpuID, 3))
	v2 := indexMaps[section.NumericIndexEntry]{
		sorted:    []section.NumericIndexEntry{entry(10, 1), entry(20, 2), entry(20, 3), entry(30, 4)},
		sortedIDs: []uint64{10, 20, 20, 30},
	}
	collided := newNumericTestIndex(entry(cnH, 1), entry(cnH, 2))
	collided.names = []string{cnA, cnB}
	collided.byName = map[string]int{cnA: 0, cnB: 1}
	named := newNumericTestIndex(entry(cpuID, 1), entry(cnH, 2))
	named.names = []string{"cpu", cnA}

	const absent = -1
	tests := []struct {
		name    string
		index   *indexMaps[section.NumericIndexEntry]
		id      uint64 // looked up when byName is empty
		byName  string
		wantOrd int
	}{
		{name: "V1 by ID", index: &v1, id: 10, wantOrd: 1},
		{name: "V1 absent ID", index: &v1, id: 99, wantOrd: absent},
		{name: "V1 without names resolves a name by its hash", index: &v1, byName: "cpu", wantOrd: 2},
		{name: "V1 without names, absent name", index: &v1, byName: "mem", wantOrd: absent},
		{name: "V2 by ID", index: &v2, id: 30, wantOrd: 3},
		{name: "V2 duplicate ID resolves to the first entry", index: &v2, id: 20, wantOrd: 1},
		{name: "V2 absent ID", index: &v2, id: 25, wantOrd: absent},
		{name: "collided ID resolves to the first entry", index: &collided, id: cnH, wantOrd: 0},
		{name: "collided first name", index: &collided, byName: cnA, wantOrd: 0},
		{name: "collided second name", index: &collided, byName: cnB, wantOrd: 1},
		{name: "collided index, absent name", index: &collided, byName: "cpu", wantOrd: absent},
		{name: "retained name", index: &named, byName: cnA, wantOrd: 1},
		{name: "retained names reject a hash-only match", index: &named, byName: cnB, wantOrd: absent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				got    *section.NumericIndexEntry
				copied section.NumericIndexEntry
				ok     bool
			)
			if tt.byName != "" {
				got = tt.index.entryByName(tt.byName)
				copied, ok = tt.index.GetByName(tt.byName)
			} else {
				got = tt.index.entryByID(tt.id)
				copied, ok = tt.index.GetByID(tt.id)
			}

			if tt.wantOrd == absent {
				require.Nil(t, got)
				require.False(t, ok)
				require.Zero(t, copied)

				return
			}

			require.Same(t, &tt.index.sorted[tt.wantOrd], got)
			require.True(t, ok)
			require.Equal(t, tt.index.sorted[tt.wantOrd], copied)
		})
	}
}
