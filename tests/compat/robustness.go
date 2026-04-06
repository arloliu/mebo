package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// corruptScenario describes a single adversarial blob to generate.
type corruptScenario struct {
	id       string
	generate func(outdir string) error
}

// corruptionScenarios returns all adversarial scenarios that must be decoded
// with an error (and must NOT panic).  They are derived from a "seed" numeric
// blob in indir that is a valid V1 encode.
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
				corrupted := make([]byte, len(data))
				copy(corrupted, data)
				// Flip bytes in the payload area (after header+index) to
				// create an invalid compressed/encoded payload.
				payloadStart := 32 + 16*5 // header + 5 index entries (rough)
				if payloadStart >= len(corrupted) {
					payloadStart = len(corrupted) / 2
				}
				for i := payloadStart; i < len(corrupted) && i < payloadStart+32; i++ {
					corrupted[i] ^= 0xFF
				}
				return writeCorruptBlob(outdir, "corrupt-flipped-bits", corrupted)
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
				// The metric count is stored in bytes 4-5 (uint16 LE) of the header.
				// Set it to 65535 to force the decoder to read way more index entries
				// than exist in the blob.
				if len(corrupted) >= 6 {
					corrupted[4] = 0xFF
					corrupted[5] = 0xFF
				}
				return writeCorruptBlob(outdir, "corrupt-oversized-metric-count", corrupted)
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

// writeCorruptBlob persists corrupt blob bytes to outdir.
// Unlike writeBlobFile, it also writes a minimal manifest so that the reject
// subcommand can discover the scenario via directory scanning.
func writeCorruptBlob(outdir, id string, data []byte) error {
	if err := os.MkdirAll(outdir, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", outdir, err)
	}

	blobPath := filepath.Join(outdir, id+".blob")
	if err := os.WriteFile(blobPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", blobPath, err)
	}

	// Write a manifest so the reject subcommand knows which blob type to try.
	m := &Manifest{
		ScenarioID: id,
		BlobType:   BlobTypeNumeric, // all corruption tests use the numeric decoder
		Format:     FormatV1,
		Metrics:    nil,
	}
	return writeManifest(outdir, m)
}
