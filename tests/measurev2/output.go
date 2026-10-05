package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"
)

// Paths inside -outdir.
const (
	partialDir   = ".partial"
	profilesDir  = "profiles"
	mainJSONName = "main.json"
)

// runProfiles runs a -profiles invocation: create -outdir (failing if it exists, before anything else),
// measure every data set, write the version-1 files under -outdir/.partial/, then rename them into place.
// After an error nothing is renamed.
func runProfiles(o *options, runner timingRunner) error {
	if err := os.Mkdir(o.outdir, 0o750); err != nil { //nolint:gosec // -outdir is the caller's own output path
		return fmt.Errorf("creating -outdir: %w", err)
	}

	start := time.Now()
	plan := o.plan(runner)
	results, err := measureAll(plan)
	if err != nil {
		return err
	}
	end := time.Now()

	inv := Invocation{Layout: o.layout, Round: o.round, Order: o.order, Start: start, End: end, Timestamp: start}
	files, err := encodeReports(o, results, collectCommon(o, manifestCells(o.profiles, o.cells)), inv)
	if err != nil {
		return err
	}

	return publish(o.outdir, files, o.verbose)
}

// encodeReports builds one version-1 file per data set, keyed by its path relative to -outdir.
// main.json, written when the main data set was measured, holds the same bytes as its profile file.
func encodeReports(o *options, results map[string]*datasetResult, common Common, inv Invocation) (map[string][]byte, error) {
	method := methodBenchmark
	if o.sizesOnly {
		method = methodSizesOnly
	}

	files := make(map[string][]byte, len(o.profiles)+1)
	for _, name := range o.profiles {
		res, ok := results[name]
		if !ok {
			return nil, fmt.Errorf("%s: not measured", name)
		}
		spec := res.spec
		report := Report[RawOp]{
			FormatVersion: formatVersion,
			Method:        method,
			RunID:         o.runID,
			Common:        common,
			Invocation:    &inv,
			RawRawBytes:   res.rawRaw,
			Metadata: ReportMetadata{
				GoVersion:   runtime.Version(),
				OS:          runtime.GOOS,
				Arch:        runtime.GOARCH,
				NumCPU:      runtime.NumCPU(),
				Timestamp:   inv.Timestamp,
				Data:        res.config,
				ProfileSpec: &spec,
			},
			Matrix:  res.rows,
			Scaling: res.scaling,
		}
		data, err := marshalJSON(report, o.pretty)
		if err != nil {
			return nil, err
		}
		files[filepath.Join(profilesDir, "matrix_"+name+".json")] = data
		if name == mainProfile {
			files[mainJSONName] = data
		}
	}

	return files, nil
}

// publish writes files under outdir/.partial/ and then renames them into outdir:
// the profiles directory first, then main.json.
// Every path is resolved inside outdir through an os.Root.
func publish(outdir string, files map[string][]byte, verbose bool) error {
	root, err := os.OpenRoot(outdir)
	if err != nil {
		return fmt.Errorf("opening -outdir: %w", err)
	}
	defer func() { _ = root.Close() }()

	if err := root.MkdirAll(filepath.Join(partialDir, profilesDir), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", partialDir, err)
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		if err := root.WriteFile(filepath.Join(partialDir, p), files[p], 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", p, err)
		}
	}

	if err := root.Rename(filepath.Join(partialDir, profilesDir), profilesDir); err != nil {
		return fmt.Errorf("publishing %s: %w", profilesDir, err)
	}
	if _, ok := files[mainJSONName]; ok {
		if err := root.Rename(filepath.Join(partialDir, mainJSONName), mainJSONName); err != nil {
			return fmt.Errorf("publishing %s: %w", mainJSONName, err)
		}
	}
	if err := root.Remove(partialDir); err != nil {
		return fmt.Errorf("removing %s: %w", partialDir, err)
	}

	if verbose {
		logf("\nResults written to %s (%d files)\n", outdir, len(files))
	}

	return nil
}

// writeReport serializes report as JSON and writes it to outputFile, or to
// stdout when outputFile is empty.
func writeReport(report FullReport, outputFile string, pretty, verbose bool) error {
	jsonData, err := marshalJSON(report, pretty)
	if err != nil {
		return err
	}

	if outputFile == "" {
		fmt.Println(string(jsonData))

		return nil
	}

	if err := os.WriteFile(outputFile, jsonData, 0o600); err != nil { //nolint:gosec // -output is the caller's own output path
		return fmt.Errorf("writing output file: %w", err)
	}

	if verbose {
		logf("\nResults written to %s\n", outputFile)
	}

	return nil
}

// marshalJSON serializes v, indented when pretty is set.
func marshalJSON(v any, pretty bool) ([]byte, error) {
	var (
		data []byte
		err  error
	)
	if pretty {
		data, err = json.MarshalIndent(v, "", "  ")
	} else {
		data, err = json.Marshal(v)
	}
	if err != nil {
		return nil, fmt.Errorf("serializing JSON: %w", err)
	}

	return data, nil
}
