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
