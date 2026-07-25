package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/arloliu/mebo/section"
)

// corruptScenario describes a single adversarial blob to generate.
type corruptScenario struct {
	id       string
	generate func(outdir string) error
}

// corruptionScenarios returns all adversarial scenarios derived from a
// "seed" numeric blob in indir that is a valid V1 encode. Most scenarios
// must be decoded with an error (and must NOT panic); a scenario may instead
// opt into the weaker "must not panic" (Graceful) contract when its
// corruption targets unchecksummed payload bytes that a decoder can
// legitimately accept as structurally valid-but-wrong — see
// corrupt-flipped-bits below for the one fixture that does this today.
func corruptionScenarios(indir string) []corruptScenario {
	seed := "num-v1-defaults"
	return []corruptScenario{
		{
			id: "corrupt-zero-bytes",
			generate: func(outdir string) error {
				return writeCorruptBlob(outdir, "corrupt-zero-bytes", []byte{})
			},
		},
		{
			id: "corrupt-one-byte",
			generate: func(outdir string) error {
				return writeCorruptBlob(outdir, "corrupt-one-byte", []byte{0xFF})
			},
		},
		{
			id: "corrupt-truncated-header",
			generate: func(outdir string) error {
				data, err := readBlobFile(indir, seed)
				if err != nil {
					return err
				}
				if len(data) > 16 {
					data = data[:16] // truncate mid-header
				}
				return writeCorruptBlob(outdir, "corrupt-truncated-header", data)
			},
		},
		{
			id: "corrupt-truncated-index",
			generate: func(outdir string) error {
				data, err := readBlobFile(indir, seed)
				if err != nil {
					return err
				}
				// Header is 32 bytes; truncate 4 bytes into the index section.
				cutAt := 32 + 4
				if len(data) > cutAt {
					data = data[:cutAt]
				}
				return writeCorruptBlob(outdir, "corrupt-truncated-index", data)
			},
		},
		{
			id: "corrupt-truncated-payload",
			generate: func(outdir string) error {
				data, err := readBlobFile(indir, seed)
				if err != nil {
					return err
				}
				// Keep header + full index, truncate halfway into payload.
				cut := len(data) / 2
				if cut < 48 {
					cut = 48
				}
				data = data[:cut]
				return writeCorruptBlob(outdir, "corrupt-truncated-payload", data)
			},
		},
		{
			id: "corrupt-bad-magic",
			generate: func(outdir string) error {
				data, err := readBlobFile(indir, seed)
				if err != nil {
					return err
				}
				if len(data) < 4 {
					return fmt.Errorf("seed blob too small")
				}
				// Overwrite magic bytes (bytes 0-1 of the uint16 flags field) with
				// garbage that doesn't match any known magic number.
				corrupted := make([]byte, len(data))
				copy(corrupted, data)
				corrupted[0] = 0xDE
				corrupted[1] = 0xAD
				return writeCorruptBlob(outdir, "corrupt-bad-magic", corrupted)
			},
		},
		{
			id: "corrupt-flipped-bits",
			generate: func(outdir string) error {
				data, err := readBlobFile(indir, seed)
				if err != nil {
					return err
				}
				// Carry the seed's own manifest (real MetricID/name +
				// data-point list) into the corrupted fixture so that
				// VerifyNumericBlob's per-metric loop actually calls
				// All/AllByName, TimestampAt, and ValueAt over every metric
				// and every point — i.e. genuinely traverses the corrupted
				// Delta/Gorilla payload — instead of skipping verification
				// entirely on a nil Metrics list. Value/timestamp mismatches
				// this produces are expected (the payload bytes are flipped)
				// and are harmless: the Graceful row (see runDecode in
				// main.go) only fails on a panic/read failure, never on
				// VerifyResult field mismatches.
				seedManifest, err := readManifest(indir, seed)
				if err != nil {
					return fmt.Errorf("read seed manifest: %w", err)
				}
				corrupted := make([]byte, len(data))
				copy(corrupted, data)

				// Flip bytes in the timestamp/value/tag payload area (after
				// header+index). Derive the real payload start from the
				// seed's own header (TimestampPayloadOffset) instead of a
				// hand-computed "header + N index entries" guess, which goes
				// stale the moment the seed's metric count or index-entry
				// size (V1 vs V2/V2Ext) changes.
				hdr, err := section.ParseNumericHeader(data)
				if err != nil {
					return fmt.Errorf("parse seed header: %w", err)
				}
				payloadStart := int(hdr.TimestampPayloadOffset)
				if payloadStart >= len(corrupted) {
					payloadStart = len(corrupted) / 2
				}
				for i := payloadStart; i < len(corrupted) && i < payloadStart+32; i++ {
					corrupted[i] ^= 0xFF
				}

				// NOT a rejection fixture. Timestamp/value/tag payload bytes
				// are raw Delta/Gorilla-encoded data with no length-prefix
				// or checksum protecting them, so flipped bits decode as
				// structurally valid-but-wrong values rather than tripping
				// any validation — "must reject" is not an honest contract
				// here. Verified empirically: fuzzing every possible 32-byte
				// (and tail-truncated) window across this seed's entire
				// ts+val payload — every offset from TimestampPayloadOffset
				// through the end of the blob — produced 0 decode errors and
				// 0 panics across all of them; only silently wrong decoded
				// values. The honest contract this fixture can make is
				// "decoding corrupted payload bytes must not panic, hang, or
				// read out of bounds" — see Manifest.Graceful and its
				// handling in main.go's runDecode.
				return writeCorruptBlobGracefulTraversing(outdir, "corrupt-flipped-bits", corrupted, seedManifest.Metrics, seedManifest.UseMetricID)
			},
		},
		{
			id: "corrupt-all-zeroes",
			generate: func(outdir string) error {
				return writeCorruptBlob(outdir, "corrupt-all-zeroes", make([]byte, 64))
			},
		},
		{
			id: "corrupt-all-ff",
			generate: func(outdir string) error {
				buf := make([]byte, 64)
				for i := range buf {
					buf[i] = 0xFF
				}
				return writeCorruptBlob(outdir, "corrupt-all-ff", buf)
			},
		},
		{
			id: "corrupt-oversized-metric-count",
			generate: func(outdir string) error {
				data, err := readBlobFile(indir, seed)
				if err != nil {
					return err
				}
				corrupted := make([]byte, len(data))
				copy(corrupted, data)

				// MetricCount is a uint32 at header byte offset 12-15 (NOT
				// bytes 4-5 — those land inside StartTime and corrupting
				// them there never invalidated anything; see
				// section/numeric_header.go's field layout comments). Parse
				// the seed's own header to read back the endian engine it
				// was actually written with (this harness defaults to
				// little-endian, but a seed could be big-endian — e.g.
				// num-v1-big-endian — so confirm rather than assume) and
				// write the corrupted value through that engine.
				hdr, err := section.ParseNumericHeader(corrupted)
				if err != nil {
					return fmt.Errorf("parse seed header: %w", err)
				}
				engine := hdr.Flag.GetEndianEngine()

				// 0xFFFFFFFF declares far more metrics than the seed's index
				// section could possibly hold. This is NOT caught by
				// NumericHeader.Parse's own "MetricCount > maxSafeUint32"
				// check (section/numeric_header.go:80): on 64-bit platforms
				// maxSafeUint32 == math.MaxUint32, so a uint32 field can
				// never exceed it — that guard only matters on 32-bit
				// builds. The real, deterministic validation this trips is
				// downstream in NumericDecoder.parseIndexEntries
				// (blob/numeric_decoder.go): it computes
				// indexOffset+entrySize*metricCount and rejects with
				// ErrInvalidIndexEntrySize once that exceeds the blob's
				// actual length — verified empirically against this exact
				// seed shape.
				engine.PutUint32(corrupted[12:16], 0xFFFFFFFF)

				return writeCorruptBlobExpect(outdir, "corrupt-oversized-metric-count", corrupted, "ErrInvalidIndexEntrySize")
			},
		},
		{
			id: "corrupt-random-noise",
			generate: func(outdir string) error {
				// Deterministic "random" bytes (no crypto/rand needed).
				buf := make([]byte, 128)
				for i := range buf {
					buf[i] = byte((i*37 + 13) & 0xFF)
				}
				return writeCorruptBlob(outdir, "corrupt-random-noise", buf)
			},
		},
	}
}

// writeCorruptBlob persists corrupt blob bytes to outdir with a generic
// "must reject with some error" manifest (no specific sentinel asserted).
// Unlike writeBlobFile, it also writes a minimal manifest so that the reject
// subcommand can discover the scenario via directory scanning.
func writeCorruptBlob(outdir, id string, data []byte) error {
	return writeCorruptBlobManifest(outdir, id, data, "", false, nil, false)
}

// writeCorruptBlobExpect is like writeCorruptBlob but additionally declares
// the specific sentinel error (a key into main.go's namedSentinels registry)
// that decode must fail with, checked via errors.Is — for corruption whose
// resulting failure path is deterministic and known, rather than "any
// error".
func writeCorruptBlobExpect(outdir, id string, data []byte, expectErrIs string) error {
	return writeCorruptBlobManifest(outdir, id, data, expectErrIs, false, nil, false)
}

// writeCorruptBlobGraceful is like writeCorruptBlob but marks the fixture
// Graceful (see Manifest.Graceful in testdata.go): decode may succeed or
// fail, and only a panic fails the row. Used for corruption that targets
// unchecksummed payload bytes, where "must reject" is not a well-defined
// contract.
//
// It carries no Metrics, so VerifyNumericBlob's per-metric verification loop
// never runs — appropriate only when the corruption is severe enough (e.g.
// header/structural corruption) that decode is expected to fail before any
// metric accessor could be reached. A Graceful fixture whose blob decodes
// successfully and is expected to be walked metric-by-metric must use
// writeCorruptBlobGracefulTraversing instead — otherwise its no-panic claim
// never actually exercises accessor traversal.
func writeCorruptBlobGraceful(outdir, id string, data []byte) error {
	return writeCorruptBlobManifest(outdir, id, data, "", true, nil, false)
}

// writeCorruptBlobGracefulTraversing is like writeCorruptBlobGraceful but
// additionally carries a real metric/data-point manifest (typically copied
// from the uncorrupted seed blob's own manifest) so that VerifyNumericBlob
// actually invokes All/AllByName, TimestampAt, and ValueAt over every
// decoded metric and point — i.e. genuinely traverses the corrupted payload
// bytes rather than merely opening the blob's header/index. Field mismatches
// this produces (the payload bytes are, by construction, wrong) are expected
// and do not fail the row: main.go's runDecode ignores VerifyResult field
// errors for Graceful rows and fails only on a panic or read failure.
func writeCorruptBlobGracefulTraversing(outdir, id string, data []byte, metrics []ManifestMetric, useMetricID bool) error {
	return writeCorruptBlobManifest(outdir, id, data, "", true, metrics, useMetricID)
}

// writeCorruptBlobManifest is the shared implementation behind
// writeCorruptBlob / writeCorruptBlobExpect / writeCorruptBlobGraceful /
// writeCorruptBlobGracefulTraversing.
func writeCorruptBlobManifest(outdir, id string, data []byte, expectErrIs string, graceful bool, metrics []ManifestMetric, useMetricID bool) error {
	if err := os.MkdirAll(outdir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", outdir, err)
	}

	blobPath := filepath.Join(outdir, id+".blob")
	if err := os.WriteFile(blobPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", blobPath, err)
	}

	// Write a manifest so the reject subcommand knows which blob type to try.
	m := &Manifest{
		ScenarioID:  id,
		BlobType:    BlobTypeNumeric, // all corruption tests use the numeric decoder
		Format:      FormatV1,
		UseMetricID: useMetricID,
		Metrics:     metrics,
		ExpectErrIs: expectErrIs,
		Graceful:    graceful,
	}
	return writeManifest(outdir, m)
}
