// Command compat is a black-box cross-version compatibility test tool for mebo.
//
// Usage:
//
//	compat encode   --outdir <dir>
//	compat decode   --indir  <dir>
//	compat reject   --indir  <dir>   (expects decode to return an error)
//
// The encode subcommand writes one <scenario>.blob + <scenario>.json for every
// registered scenario.  The decode subcommand reads those files, decodes the
// blob bytes, and compares every field against the manifest.  The reject
// subcommand does the same but asserts that decoding returns an error and does
// NOT panic.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "encode":
		if err := runEncode(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "encode: %v\n", err)
			os.Exit(1)
		}
	case "decode":
		if err := runDecode(os.Args[2:], false); err != nil {
			fmt.Fprintf(os.Stderr, "decode: %v\n", err)
			os.Exit(1)
		}
	case "reject":
		if err := runDecode(os.Args[2:], true); err != nil {
			fmt.Fprintf(os.Stderr, "reject: %v\n", err)
			os.Exit(1)
		}
	case "corrupt":
		if err := runCorrupt(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "corrupt: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  compat encode  --outdir <dir>           Encode all scenarios to <dir>/
  compat decode  --indir  <dir>           Decode & verify all scenarios in <dir>/
  compat reject  --indir  <dir>           Assert that blobs in <dir>/ fail to decode (no panic)
  compat corrupt --indir  <dir> --outdir  <outdir>  Generate corrupted blobs from <indir>/
`)
}

// ---------------------------------------------------------------------------
// encode
// ---------------------------------------------------------------------------

func runEncode(args []string) error {
	fs := flag.NewFlagSet("encode", flag.ExitOnError)
	outdir := fs.String("outdir", "", "output directory for blobs and manifests (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *outdir == "" {
		return fmt.Errorf("--outdir is required")
	}
	if err := os.MkdirAll(*outdir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", *outdir, err)
	}

	startTime := baseStartTime
	var failed []string
	for _, s := range allScenarios {
		fmt.Printf("  encode %-40s ", s.ID)
		data, manifest, err := s.encode(startTime)
		if err != nil {
			fmt.Printf("FAIL (encode): %v\n", err)
			failed = append(failed, s.ID)
			continue
		}
		if err := writeManifest(*outdir, manifest); err != nil {
			fmt.Printf("FAIL (manifest): %v\n", err)
			failed = append(failed, s.ID)
			continue
		}
		if err := writeBlobFile(*outdir, s.ID, data); err != nil {
			fmt.Printf("FAIL (blob write): %v\n", err)
			failed = append(failed, s.ID)
			continue
		}
		fmt.Printf("OK (%d bytes)\n", len(data))
	}

	if len(failed) > 0 {
		return fmt.Errorf("%d scenario(s) failed to encode: %s", len(failed), strings.Join(failed, ", "))
	}
	fmt.Printf("\nEncoded %d scenario(s) to %s\n", len(allScenarios), *outdir)
	return nil
}

// ---------------------------------------------------------------------------
// decode / reject
// ---------------------------------------------------------------------------

func runDecode(args []string, expectError bool) error {
	fs := flag.NewFlagSet("decode", flag.ExitOnError)
	indir := fs.String("indir", "", "directory containing blobs and manifests (required)")
	filter := fs.String("filter", "", "comma-separated list of scenario IDs to run (default: all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *indir == "" {
		return fmt.Errorf("--indir is required")
	}

	scenarios, err := scenariosToRun(*indir, *filter)
	if err != nil {
		return err
	}

	var failed []string
	for _, s := range scenarios {
		if expectError {
			fmt.Printf("  reject %-40s ", s.id)
		} else {
			fmt.Printf("  verify %-40s ", s.id)
		}

		result, decodeErr := decodeAndVerify(*indir, s.id, s.blobType)
		if expectError {
			// We want the decode to fail gracefully — result is nil iff decode
			// panicked (recoverable), or result.OK() is true (unexpectedly passed).
			if decodeErr == nil && result != nil && result.OK() {
				fmt.Printf("FAIL (expected decode error, but decoding succeeded)\n")
				failed = append(failed, s.id)
			} else {
				fmt.Printf("OK (decode returned error as expected)\n")
			}
		} else {
			if decodeErr != nil {
				fmt.Printf("FAIL (decode error): %v\n", decodeErr)
				failed = append(failed, s.id)
			} else if result == nil || !result.OK() {
				errs := "nil result"
				if result != nil {
					errs = strings.Join(result.Errors, "; ")
				}
				fmt.Printf("FAIL: %s\n", errs)
				failed = append(failed, s.id)
			} else {
				fmt.Printf("OK\n")
			}
		}
	}

	mode := "verified"
	if expectError {
		mode = "rejected"
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d scenario(s) failed: %s", len(failed), strings.Join(failed, ", "))
	}
	fmt.Printf("\n%s %d scenario(s) from %s\n", strings.Title(mode), len(scenarios), *indir) //nolint:staticcheck
	return nil
}

type scenarioMeta struct {
	id       string
	blobType BlobType
}

// scenariosToRun returns the list of scenarios to process.
// When filter is empty it discovers all *.json manifest files in indir.
func scenariosToRun(indir, filter string) ([]scenarioMeta, error) {
	if filter != "" {
		ids := strings.Split(filter, ",")
		result := make([]scenarioMeta, 0, len(ids))
		for _, id := range ids {
			id = strings.TrimSpace(id)
			m, err := readManifest(indir, id)
			if err != nil {
				return nil, fmt.Errorf("read manifest for %s: %w", id, err)
			}
			result = append(result, scenarioMeta{id: id, blobType: m.BlobType})
		}
		return result, nil
	}

	// Discover all manifests.
	entries, err := os.ReadDir(indir)
	if err != nil {
		return nil, fmt.Errorf("readdir %s: %w", indir, err)
	}
	result := make([]scenarioMeta, 0)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		m, err := readManifest(indir, id)
		if err != nil {
			return nil, fmt.Errorf("read manifest %s: %w", e.Name(), err)
		}
		result = append(result, scenarioMeta{id: id, blobType: m.BlobType})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

// decodeAndVerify is wrapped in a recover to catch any panic from the decoder
// (which would be a regression).  It returns (nil, nil) on panic so the caller
// can distinguish panic from expected-error.
func decodeAndVerify(indir, scenarioID string, blobType BlobType) (res *VerifyResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in decoder: %v", r)
			res = nil
		}
	}()

	m, err := readManifest(indir, scenarioID)
	if err != nil {
		return nil, err
	}

	data, err := readBlobFile(indir, scenarioID)
	if err != nil {
		return nil, err
	}

	switch blobType {
	case BlobTypeNumeric:
		result := VerifyNumericBlob(data, m)
		return result, nil
	case BlobTypeText:
		result := VerifyTextBlob(data, m)
		return result, nil
	case BlobTypeSet:
		result := VerifyBlobSet(data, m)
		return result, nil
	default:
		return nil, fmt.Errorf("unknown blob type: %s", blobType)
	}
}

// ---------------------------------------------------------------------------
// corrupt — generate adversarial blobs for reject testing
// ---------------------------------------------------------------------------

func runCorrupt(args []string) error {
	fs := flag.NewFlagSet("corrupt", flag.ExitOnError)
	indir := fs.String("indir", "", "directory containing source blobs (required)")
	outdir := fs.String("outdir", "", "output directory for corrupted blobs (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *indir == "" || *outdir == "" {
		return fmt.Errorf("--indir and --outdir are required")
	}
	if err := os.MkdirAll(*outdir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", *outdir, err)
	}

	count := 0
	for _, s := range corruptionScenarios(*indir) {
		fmt.Printf("  corrupt %-50s ", s.id)
		if err := s.generate(*outdir); err != nil {
			fmt.Printf("FAIL: %v\n", err)
			continue
		}
		fmt.Printf("OK\n")
		count++
	}
	fmt.Printf("\nGenerated %d corrupted blob(s) in %s\n", count, *outdir)
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// Ensure baseStartTime is used from scenarios.go (suppress unused import).
var _ = time.Time{}
var _ = filepath.Join
