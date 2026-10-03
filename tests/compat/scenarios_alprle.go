//go:build alp && alprle

package main

import (
	"fmt"
	"math"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/section"
)

// ALP-RLE (format.TypeALPRLE, value encoding 0x7) adds a run-length front end to ALP.
// Its scenarios live behind the "alprle" build tag, which run_compat.sh applies to every ref that has the type.
// Every scenario ID starts with "alprle-": run_compat.sh routes that prefix into its own bucket,
// so an OLD binary that predates ALP-RLE is asked to reject these blobs gracefully instead of decoding them.
// The shapes reuse scenarios_alp.go's helpers (both tags are set together), so each blob holds runs columns
// (scheme 3, nesting main, RD and raw run values) next to plain ALP columns.
func init() {
	allScenarios = append(allScenarios, alprleNumericScenarios()...)
	extraCorruptionScenarios = append(extraCorruptionScenarios, alprleCorruptionScenarios)
}

// alprleCorruptionSeed holds runs columns of every nested scheme next to plain columns.
const alprleCorruptionSeed = "alprle-v2-mixed"

// alprleCorruptionScenarios damages the value payload of alprleCorruptionSeed.
// Both fixtures are Graceful: no panic, hang or out-of-bounds read.
// Readers without ALP-RLE reject the blob at its header,
// so only readers that open it exercise the runs-column validation.
func alprleCorruptionScenarios(indir string) []corruptScenario {
	damage := func(outdir, id string, mutate func(data []byte, valStart int) []byte) error {
		data, err := readBlobFile(indir, alprleCorruptionSeed)
		if err != nil {
			return err
		}
		seedManifest, err := readManifest(indir, alprleCorruptionSeed)
		if err != nil {
			return fmt.Errorf("read seed manifest: %w", err)
		}
		hdr, err := section.ParseNumericHeader(data)
		if err != nil {
			return fmt.Errorf("parse seed header: %w", err)
		}
		valStart := int(hdr.ValuePayloadOffset)
		if valStart >= len(data) {
			return fmt.Errorf("seed value payload offset %d beyond blob length %d", valStart, len(data))
		}

		return writeCorruptBlobGracefulTraversing(outdir, id, mutate(append([]byte(nil), data...), valStart),
			seedManifest.Metrics, seedManifest.UseMetricID)
	}

	return []corruptScenario{
		{
			// Every 5th byte of the value payload inverted: hits run counts, bitmaps and nested columns.
			id: "alprle-corrupt-flipped-values",
			generate: func(outdir string) error {
				return damage(outdir, "alprle-corrupt-flipped-values", func(data []byte, valStart int) []byte {
					for i := valStart; i < len(data); i += 5 {
						data[i] ^= 0xFF
					}

					return data
				})
			},
		},
		{
			// Value payload cut in half: run counts and bitmaps promise more bytes than remain.
			id: "alprle-corrupt-truncated-values",
			generate: func(outdir string) error {
				return damage(outdir, "alprle-corrupt-truncated-values", func(data []byte, valStart int) []byte {
					return data[:valStart+(len(data)-valStart)/2]
				})
			},
		},
	}
}

// alprleHold is a 2-decimal walk where about half the points repeat the previous value.
func alprleHold(m, n int) []float64 {
	rng := alpSeed(m, 7907, 11)
	vals := make([]float64, n)
	cur := 100.0 + float64(m)*10
	for i := range vals {
		if i > 0 && rng.unit() < 0 {
			vals[i] = vals[i-1]
			continue
		}
		cur += cur * rng.unit() * 0.005
		vals[i] = math.Round(cur*100) / 100
	}

	return vals
}

// alprleRepeat stretches the first values of gen into runs of length run, n points in total.
func alprleRepeat(gen alpValueGen, run int) alpValueGen {
	return func(m, n int) []float64 {
		src := gen(m, (n+run-1)/run)
		vals := make([]float64, n)
		for i := range vals {
			vals[i] = src[i/run]
		}

		return vals
	}
}

// alprleExceptionRuns is alprleHold with runs of a full-precision value,
// so the nested main column carries exceptions that index runs, not points.
func alprleExceptionRuns(m, n int) []float64 {
	vals := alprleHold(m, n)
	for i := 10; i < n; i += 25 {
		for k := i; k < min(i+4, n); k++ {
			vals[k] = math.Pi * float64(i)
		}
	}

	return vals
}

// alprleMixed gives each metric a different shape, so one blob holds runs columns
// nesting main, RD and raw run values, signed zeros and NaN payloads,
// next to plain columns (a run-free walk and a constant) that stay scheme 0.
func alprleMixed(m, n int) []float64 {
	gens := []alpValueGen{
		alprleHold,
		alprleRepeat(alpDecimals, 25),     // long steps
		alprleRepeat(alpSpecials, 5),      // runs of −0, +0, ±Inf, NaN payload, subnormals
		alprleExceptionRuns,               // nested main with exceptions
		alprleRepeat(alpFullPrecision, 3), // nested RD
		alprleRepeat(alpRandomBits, 4),    // nested raw
		alpDecimals,                       // run-free: stays plain
		alpConstant,                       // width 0: plain beats runs
	}

	return gens[m%len(gens)](m, n)
}

func alprleNumericScenarios() []Scenario {
	none := format.CompressionNone
	cases := []alpCase{
		{id: "alprle-v1-hold", gen: alprleHold, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 5, numPoints: 150},
		{id: "alprle-v1-mixed", gen: alprleMixed, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 8, numPoints: 150},
		{id: "alprle-v1-big-endian", gen: alprleMixed, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, bigEndian: true, useMetricID: true, numMetrics: 8, numPoints: 64},
		{id: "alprle-v1-by-name", gen: alprleMixed, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: false, numMetrics: 8, numPoints: 30},
		{id: "alprle-v1-tagged", gen: alprleHold, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, tagsEnabled: true, useMetricID: true, numMetrics: 3, numPoints: 40},
		{id: "alprle-v1-zstd", gen: alprleMixed, tsEnc: format.TypeDelta, valComp: format.CompressionZstd, layout: FormatV1, useMetricID: true, numMetrics: 8, numPoints: 64},
		{id: "alprle-v1-single-point", gen: alpDecimals, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 2, numPoints: 1},
		{id: "alprle-v1-long-column", gen: alprleMixed, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 8, numPoints: 1500},
		{id: "alprle-v2-mixed", gen: alprleMixed, tsEnc: format.TypeDeltaPacked, valComp: none, layout: FormatV2, useMetricID: true, numMetrics: 16, numPoints: 150},
		{id: "alprle-v2-big-endian", gen: alprleMixed, tsEnc: format.TypeDelta, valComp: none, layout: FormatV2, bigEndian: true, useMetricID: true, numMetrics: 8, numPoints: 64},
		// The production configuration: shared DeltaPacked timestamps, no compression, no tags.
		{id: "alprle-v2-shared", gen: alprleMixed, tsEnc: format.TypeDeltaPacked, valComp: none, layout: FormatV2Ext, useMetricID: true, numMetrics: 16, numPoints: 150},
		// A raw first column pushes the next value offset past uint16: extended 32-byte index.
		{id: "alprle-v2ext-extended-index", gen: alpExtendedIndex, tsEnc: format.TypeDeltaPacked, valComp: none, layout: FormatV2Ext, useMetricID: true, numMetrics: 3, numPoints: 9000},
	}

	scenarios := make([]Scenario, 0, len(cases))
	for _, c := range cases {
		scenarios = append(scenarios, Scenario{
			ID:       c.id,
			BlobType: BlobTypeNumeric,
			Format:   c.layout,
			encode: func(startTime time.Time) ([]byte, *Manifest, error) {
				data, manifest, err := encodeNumericALP(startTime, c, alprleOptions(c, format.TypeALPRLE))
				if err != nil {
					return nil, nil, err
				}
				if !alprleExpectsRuns(c) {
					return data, manifest, nil
				}

				// A shape with repeats must take the runs layout somewhere,
				// or this scenario would only re-test plain ALP under a new type byte.
				plain, _, err := encodeNumericALP(startTime, c, alprleOptions(c, format.TypeALP))
				if err != nil {
					return nil, nil, err
				}
				if len(data) >= len(plain) {
					return nil, nil, fmt.Errorf("%s: ALP-RLE blob (%d bytes) is not smaller than ALP (%d bytes); no runs column was written",
						c.id, len(data), len(plain))
				}

				return data, manifest, nil
			},
		})
	}

	return scenarios
}

// alprleExpectsRuns reports whether a case's shape has enough repeats for at least one runs column.
func alprleExpectsRuns(c alpCase) bool {
	return c.numPoints > 1 && c.valComp == format.CompressionNone && c.id != "alprle-v2ext-extended-index"
}

// alprleOptions returns the encoder options for case c under value encoding valEnc.
func alprleOptions(c alpCase, valEnc format.EncodingType) []blob.NumericEncoderOption {
	opts := []blob.NumericEncoderOption{
		blob.WithTimestampEncoding(c.tsEnc),
		blob.WithValueEncoding(valEnc),
		blob.WithTimestampCompression(format.CompressionNone),
		blob.WithValueCompression(c.valComp),
		blob.WithTagsEnabled(c.tagsEnabled),
	}
	if c.bigEndian {
		opts = append(opts, blob.WithBigEndian())
	} else {
		opts = append(opts, blob.WithLittleEndian())
	}
	layoutOpts := map[FormatVersion][]blob.NumericEncoderOption{
		FormatV1:    nil, // the default layout
		FormatV2:    {blob.WithBlobLayoutV2()},
		FormatV2Ext: {blob.WithSharedTimestamps()},
	}

	return append(opts, layoutOpts[c.layout]...)
}
