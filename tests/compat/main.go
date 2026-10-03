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
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arloliu/mebo/errs"
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
	case "mncorrupt":
		if err := mnCorruptImpl(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "mncorrupt: %v\n", err)
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
  compat encode    --outdir <dir>           Encode all scenarios to <dir>/
  compat decode    --indir  <dir>           Decode & verify all scenarios in <dir>/
  compat reject    --indir  <dir>           Assert that blobs in <dir>/ fail to decode (no panic)
  compat corrupt   --indir  <dir> --outdir  <outdir>  Generate corrupted blobs from <indir>/
  compat mncorrupt --outdir <dir>           Generate metric-names adversarial fixtures
                                             (requires -tags metricnames; see mncorrupt_metricnames.go)
`)
}

// mnCorruptImpl is set by exactly one of mncorrupt_metricnames.go (build tag
// "metricnames") or mncorrupt_stub.go (build tag "!metricnames") via init().
// This indirection lets main.go dispatch the "mncorrupt" subcommand without
// itself depending on which variant was compiled in.
var mnCorruptImpl func(args []string) error

// namedSentinels maps a Manifest.ExpectErrIs key to the concrete sentinel
// error a "must reject" fixture's decode is required to satisfy via
// errors.Is.
//
// Entries that exist identically in every supported module version (e.g.
// errs.ErrInvalidIndexEntrySize, a generic structural-decode error present
// since well before v1.9.0) are registered directly below — main.go is
// unconditionally compiled, so referencing such a symbol is safe against
// every version this harness builds against.
//
// Entries that only exist in a v1.10.0+ module (e.g. ErrDuplicateMetricName,
// ErrUnsortedIndex) CANNOT be registered here: main.go itself must stay
// buildable against v1.9.0 (see mnCorruptImpl above for why the same
// indirection is needed there), so those are instead populated by
// mncorrupt_metricnames.go's init() (build tag "metricnames"), which never
// compiles into a v1.9.0 build. Building without the metricnames tag (e.g.
// against v1.9.0) leaves those specific keys unregistered; a manifest that
// declares an ExpectErrIs key with no registered sentinel is a hard FAIL
// (see runDecode) rather than a silently-skipped check, since v1.9.0
// binaries never process fixtures that set such an ExpectErrIs in the first
// place.
var namedSentinels = map[string]error{
	"ErrInvalidIndexEntrySize": errs.ErrInvalidIndexEntrySize,
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
		switch {
		case expectError && s.graceful:
			fmt.Printf("  graceful %-38s ", s.id)
		case expectError:
			fmt.Printf("  reject %-40s ", s.id)
		default:
			fmt.Printf("  verify %-40s ", s.id)
		}

		result, decodeErr := decodeAndVerify(*indir, s.id, s.blobType)
		switch {
		case expectError && s.graceful:
			// Graceful-handling row (see Manifest.Graceful in testdata.go):
			// decode may succeed OR fail — both are acceptable outcomes for
			// corrupted payload bytes with no checksum backing them. The
			// only thing this row rejects is a panic, which
			// decodeAndVerify's recover already converts into decodeErr
			// (it never surfaces via result.DecodeErr). An operational
			// error (missing/unreadable fixture) also fails the row here —
			// it means the harness never got to run the decoder, so it
			// proves nothing about graceful handling.
			if decodeErr != nil {
				if !reportGracefulDecodeErr(s, decodeErr) {
					failed = append(failed, s.id)
				}
			} else {
				outcome := "decoded without error"
				if result != nil && result.DecodeErr != nil {
					outcome = fmt.Sprintf("decode returned error: %v", result.DecodeErr)
				}
				fmt.Printf("OK (no panic; %s)\n", outcome)
			}
		case expectError:
			// A row only counts as "rejected" when the *decoder itself*
			// returned an error — i.e. result.DecodeErr, populated by
			// VerifyNumericBlob/VerifyTextBlob/VerifyBlobSet only from
			// NewXDecoder/dec.Decode()/unpackMultiBlob/DecodeBlobSet.
			// decodeErr (the outer return from decodeAndVerify) must NEVER
			// satisfy rejection on its own, because it conflates two failure
			// shapes that are not genuine rejections:
			//
			//   - a recovered decoder panic: the robustness matrix's
			//     contract is "must reject OR must not panic", never "a
			//     panic counts as rejection" — a panic must always FAIL
			//     this row (see run_compat.sh's robustness section);
			//   - an operational error (missing/unreadable manifest or blob
			//     file): it means the harness never got to invoke the
			//     decoder at all, not that the blob was rejected.
			//
			// A merely-non-OK VerifyResult (e.g. a manifest with no
			// verifiable metrics, which every corruption fixture here uses)
			// also must NOT count as rejection on its own: that would pass
			// vacuously even when decoding fully succeeded.
			switch {
			case decodeErr != nil:
				var pe *panicError
				if errors.As(decodeErr, &pe) {
					fmt.Printf("FAIL (decoder panicked — violates the no-panic contract): %v\n", decodeErr)
				} else {
					fmt.Printf("FAIL (harness error, not a genuine rejection — fixture/manifest unreadable): %v\n", decodeErr)
				}
				failed = append(failed, s.id)
			case result == nil || result.DecodeErr == nil:
				fmt.Printf("FAIL (expected decode error, but decoding succeeded)\n")
				failed = append(failed, s.id)
			case s.expectErrIs == "":
				// No specific sentinel required for this fixture — any
				// genuine decoder-returned error satisfies it.
				fmt.Printf("OK (decode returned error as expected: %v)\n", result.DecodeErr)
			default:
				sentinel, ok := namedSentinels[s.expectErrIs]
				switch {
				case !ok:
					fmt.Printf("FAIL (manifest expects sentinel %q, but this binary has no such sentinel registered — built without the required capability tag?)\n", s.expectErrIs)
					failed = append(failed, s.id)
				case !errors.Is(result.DecodeErr, sentinel):
					fmt.Printf("FAIL (expected error %s, got: %v)\n", s.expectErrIs, result.DecodeErr)
					failed = append(failed, s.id)
				default:
					fmt.Printf("OK (decode returned %s as expected)\n", s.expectErrIs)
				}
			}
		default:
			if decodeErr != nil {
				fmt.Printf("FAIL (decode error): %v\n", decodeErr)
				failed = append(failed, s.id)
			} else if result == nil || !result.OK() {
				errMsgs := "nil result"
				if result != nil {
					errMsgs = strings.Join(result.Errors, "; ")
				}
				fmt.Printf("FAIL: %s\n", errMsgs)
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

// reportGracefulDecodeErr prints the outcome of a Graceful row whose decode
// returned a harness-level error, and reports whether the row still passes.
// Only a known, frozen v1.8.0 panic on a fixture marked
// PanicWithoutALPValidation passes; see verify.go's alpOpenValidation.
func reportGracefulDecodeErr(s scenarioMeta, decodeErr error) bool {
	var pe *panicError
	switch {
	case errors.As(decodeErr, &pe) && s.panicWithoutALPValidation && !alpOpenValidation:
		fmt.Printf("KNOWN (decoder panicked; this release predates open-time ALP validation, added in v1.9.0): %v\n", decodeErr)

		return true
	case errors.As(decodeErr, &pe):
		fmt.Printf("FAIL (decoder panicked): %v\n", decodeErr)
	default:
		fmt.Printf("FAIL (harness error — fixture could not be read): %v\n", decodeErr)
	}

	return false
}

type scenarioMeta struct {
	id       string
	blobType BlobType
	// expectErrIs is copied from the manifest's ExpectErrIs field (see
	// testdata.go). Empty for most scenarios; set for "must reject"
	// fixtures whose corruption is known to trip one specific,
	// deterministic validation path.
	expectErrIs string
	// graceful is copied from the manifest's Graceful field (see
	// testdata.go). True only for "must not crash" fixtures where neither
	// decode success nor decode failure is asserted — only the absence of
	// a panic.
	graceful bool
	// panicWithoutALPValidation is copied from the manifest's
	// PanicWithoutALPValidation field (see testdata.go).
	panicWithoutALPValidation bool
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
			result = append(result, scenarioMeta{id: id, blobType: m.BlobType, expectErrIs: m.ExpectErrIs, graceful: m.Graceful, panicWithoutALPValidation: m.PanicWithoutALPValidation})
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
		result = append(result, scenarioMeta{id: id, blobType: m.BlobType, expectErrIs: m.ExpectErrIs, graceful: m.Graceful, panicWithoutALPValidation: m.PanicWithoutALPValidation})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

// panicError wraps a value recovered from a decoder panic inside
// decodeAndVerify. It is a distinct type — rather than a plain
// fmt.Errorf-wrapped error — specifically so runDecode's generic rejection
// branch can tell "the decoder panicked" apart from every other error that
// also flows through decodeAndVerify's err return (in particular, an
// operational error from a missing/unreadable manifest or blob file). Both
// are failures, but only a genuine decoder-returned error may satisfy a
// rejection row; a panic must always FAIL it outright, never count as a
// successful "reject".
type panicError struct {
	val any
}

func (e *panicError) Error() string {
	return fmt.Sprintf("PANIC in decoder: %v", e.val)
}

// decodeAndVerify is wrapped in a recover to catch any panic from the decoder
// (which would be a regression). On panic it returns (nil, non-nil
// *panicError) wrapping the recovered value, so the caller can distinguish
// "decoder panicked" (err is a *panicError) from "harness could not even run
// the decoder" (err is a plain wrapped error from readManifest/readBlobFile)
// from both "decoded and verified cleanly" (nil, nil) and "decoded but
// VerifyResult carries errors" (non-nil result, nil error).
func decodeAndVerify(indir, scenarioID string, blobType BlobType) (res *VerifyResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &panicError{val: r}
			res = nil
		}
	}()

	m, err := readManifest(indir, scenarioID)
	if err != nil {
		return nil, fmt.Errorf("harness error (manifest unreadable): %w", err)
	}

	data, err := readBlobFile(indir, scenarioID)
	if err != nil {
		return nil, fmt.Errorf("harness error (blob unreadable): %w", err)
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
