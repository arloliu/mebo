//go:build alp

package main

import (
	"fmt"
	"math"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/section"
)

// ALP (format.TypeALP, value encoding 0x6) first shipped in v1.8.0, so its
// scenarios live behind the "alp" build tag, which run_compat.sh applies to
// any ref >= v1.8.0. Every scenario ID starts with "alp-": run_compat.sh
// routes that prefix into its own bucket, so an OLD binary that predates ALP
// is asked to reject these blobs gracefully instead of decoding them, and
// the "num-v1-"/"num-v2-" buckets never pick them up.
func init() {
	allScenarios = append(allScenarios, alpNumericScenarios()...)
	extraCorruptionScenarios = append(extraCorruptionScenarios, alpCorruptionScenarios)
}

// alpCorruptionSeed holds main, RD and raw columns side by side, so payload
// corruption lands in every ALP scheme's decoder.
const alpCorruptionSeed = "alp-v2-mixed"

// alpCorruptionScenarios damages the ALP value payload of alpCorruptionSeed.
// Both fixtures are Graceful: the contract checked across versions is "no
// panic, hang or out-of-bounds read". v1.8.0 has no open-time ALP validation
// (validateALPColumns arrived in v1.9.0): it panics on the flipped-values
// fixture, which is tolerated only for binaries built without the
// "alpvalidate" tag (see verify.go's alpOpenValidation). A stricter "must
// reject" contract would not hold for v1.8.0 either; the current decoder's
// strict rejection is covered by blob/numeric_alp_scheme_test.go and
// blob/numeric_alp_validate_test.go.
func alpCorruptionScenarios(indir string) []corruptScenario {
	// damage copies the seed, applies mutate to it given the value payload
	// offset, and writes a Graceful fixture that traverses every metric.
	// knownV180Panic marks the fixture as one v1.8.0 is known to panic on.
	damage := func(outdir, id string, knownV180Panic bool, mutate func(data []byte, valStart int) []byte) error {
		data, err := readBlobFile(indir, alpCorruptionSeed)
		if err != nil {
			return err
		}
		seedManifest, err := readManifest(indir, alpCorruptionSeed)
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
		corrupted := mutate(append([]byte(nil), data...), valStart)

		if err := writeCorruptBlobGracefulTraversing(outdir, id, corrupted, seedManifest.Metrics, seedManifest.UseMetricID); err != nil {
			return err
		}
		if !knownV180Panic {
			return nil
		}
		m, err := readManifest(outdir, id)
		if err != nil {
			return fmt.Errorf("reread fixture manifest: %w", err)
		}
		m.PanicWithoutALPValidation = true

		return writeManifest(outdir, m)
	}

	return []corruptScenario{
		{
			// Every 5th byte of the value payload inverted: hits scheme bytes,
			// headers, packed codes and exception lists across all columns.
			id: "alp-corrupt-flipped-values",
			generate: func(outdir string) error {
				// v1.8.0 panics here (slice bounds out of range in the ALP decoder).
				return damage(outdir, "alp-corrupt-flipped-values", true, func(data []byte, valStart int) []byte {
					for i := valStart; i < len(data); i += 5 {
						data[i] ^= 0xFF
					}

					return data
				})
			},
		},
		{
			// Value payload cut in half: column headers promise more bytes than remain.
			id: "alp-corrupt-truncated-values",
			generate: func(outdir string) error {
				return damage(outdir, "alp-corrupt-truncated-values", false, func(data []byte, valStart int) []byte {
					return data[:valStart+(len(data)-valStart)/2]
				})
			},
		},
	}
}

// alpValueGen returns the values of metric m, n points long.
type alpValueGen func(m, n int) []float64

// alpLCG is a tiny deterministic generator, so blobs are byte-identical across
// runs and across the binaries built against different mebo versions.
type alpLCG uint64

func (s *alpLCG) next() uint64 {
	*s = *s*6364136223846793005 + 1442695040888963407

	return uint64(*s)
}

// unit returns a value in [-1, 1).
func (s *alpLCG) unit() float64 {
	return float64(s.next()>>11)/float64(1<<52) - 1
}

// alpSeed derives a per-metric generator seed.
func alpSeed(m int, mul, add uint64) alpLCG {
	if m < 0 {
		m = 0
	}

	return alpLCG(uint64(m)*mul + add)
}

// alpDecimals is a 2-decimal random walk: ALP's main scheme with no exceptions.
func alpDecimals(m, n int) []float64 {
	rng := alpSeed(m, 7919, 1)
	vals := make([]float64, n)
	cur := 100.0 + float64(m)*10
	for i := range vals {
		cur += cur * rng.unit() * 0.005
		vals[i] = math.Round(cur*100) / 100
	}

	return vals
}

// alpDecimalsWithExceptions is a 2-decimal walk where every 7th point is a
// full-precision value, exercising main-scheme exception patching.
func alpDecimalsWithExceptions(m, n int) []float64 {
	vals := alpDecimals(m, n)
	for i := 3; i < n; i += 7 {
		vals[i] += math.Pi / 1e3
	}

	return vals
}

// alpFullPrecision is an unrounded random walk, which ALP stores with its
// real-doubles (RD) scheme.
func alpFullPrecision(m, n int) []float64 {
	rng := alpSeed(m, 104729, 3)
	vals := make([]float64, n)
	cur := 100.0 + float64(m)*10
	for i := range vals {
		cur += cur * rng.unit() * 0.005
		vals[i] = cur
	}

	return vals
}

// alpFullPrecisionWithOutliers is alpFullPrecision with every 11th point scaled
// by one of 12 powers of ten, so its left bits fall outside RD's 8-entry
// dictionary and exercise RD exception patching.
func alpFullPrecisionWithOutliers(m, n int) []float64 {
	vals := alpFullPrecision(m, n)
	for i := 5; i < n; i += 11 {
		vals[i] *= math.Pow(10, float64(3+(i/11)%12))
	}

	return vals
}

// alpExtendedIndex puts a raw (8 bytes/point) column first, so with enough
// points the next metric's value offset exceeds uint16 and a V2 blob switches
// to the extended (32-byte) index.
func alpExtendedIndex(m, n int) []float64 {
	if m == 0 {
		return alpRandomBits(m, n)
	}

	return alpDecimals(m, n)
}

// alpRandomBits spreads values over the whole float64 range, so neither the
// main nor the RD scheme pays off and ALP falls back to raw.
// NaN patterns are skipped here; alpSpecials covers them separately.
func alpRandomBits(m, n int) []float64 {
	rng := alpSeed(m, 15485863, 5)
	vals := make([]float64, n)
	for i := range vals {
		v := math.Float64frombits(rng.next())
		for math.IsNaN(v) {
			v = math.Float64frombits(rng.next())
		}
		vals[i] = v
	}

	return vals
}

// alpSpecials cycles through values that must survive bit-exactly:
// signed zeros, infinities, a NaN with a payload, subnormals and extremes,
// mixed into an otherwise decimal column.
func alpSpecials(m, n int) []float64 {
	specials := []float64{
		math.Copysign(0, -1), 0, math.Inf(1), math.Inf(-1),
		math.Float64frombits(0x7ff8_0000_0000_beef), // quiet NaN with payload
		math.SmallestNonzeroFloat64, -math.MaxFloat64, math.MaxFloat64,
	}
	vals := alpDecimals(m, n)
	for i := range vals {
		if i%3 == 0 {
			vals[i] = specials[(i/3+m)%len(specials)]
		}
	}

	return vals
}

// alpConstant repeats one decimal value, which packs at width 0.
func alpConstant(m, n int) []float64 {
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = 42.5 + float64(m)
	}

	return vals
}

// alpMixed gives each metric of one blob a different shape, so a single blob
// holds main, RD and raw columns side by side.
func alpMixed(m, n int) []float64 {
	gens := []alpValueGen{alpDecimals, alpFullPrecision, alpRandomBits, alpConstant, alpDecimalsWithExceptions, alpSpecials}

	return gens[m%len(gens)](m, n)
}

// alpCase is one ALP scenario: its value shape, encoder options and size.
type alpCase struct {
	id          string
	gen         alpValueGen
	tsEnc       format.EncodingType
	valComp     format.CompressionType
	layout      FormatVersion
	bigEndian   bool
	tagsEnabled bool
	useMetricID bool
	numMetrics  int
	numPoints   int
}

func alpNumericScenarios() []Scenario {
	none := format.CompressionNone
	cases := []alpCase{
		{id: "alp-v1-decimal", gen: alpDecimals, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 5, numPoints: 150},
		{id: "alp-v1-exceptions", gen: alpDecimalsWithExceptions, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 4, numPoints: 150},
		{id: "alp-v1-full-precision", gen: alpFullPrecision, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 4, numPoints: 150},
		{id: "alp-v1-rd-exceptions", gen: alpFullPrecisionWithOutliers, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 4, numPoints: 150},
		{id: "alp-v1-random-bits", gen: alpRandomBits, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 3, numPoints: 40},
		{id: "alp-v1-specials", gen: alpSpecials, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 3, numPoints: 48},
		{id: "alp-v1-constant", gen: alpConstant, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 3, numPoints: 20},
		{id: "alp-v1-single-point", gen: alpDecimals, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 2, numPoints: 1},
		{id: "alp-v1-big-endian", gen: alpMixed, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, bigEndian: true, useMetricID: true, numMetrics: 6, numPoints: 64},
		{id: "alp-v1-by-name", gen: alpMixed, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: false, numMetrics: 6, numPoints: 30},
		{id: "alp-v1-tagged", gen: alpDecimals, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, tagsEnabled: true, useMetricID: true, numMetrics: 3, numPoints: 12},
		{id: "alp-v1-zstd", gen: alpMixed, tsEnc: format.TypeDelta, valComp: format.CompressionZstd, layout: FormatV1, useMetricID: true, numMetrics: 6, numPoints: 64},
		{id: "alp-v1-long-column", gen: alpMixed, tsEnc: format.TypeDelta, valComp: none, layout: FormatV1, useMetricID: true, numMetrics: 3, numPoints: 1500},
		{id: "alp-v2-mixed", gen: alpMixed, tsEnc: format.TypeDeltaPacked, valComp: none, layout: FormatV2, useMetricID: true, numMetrics: 12, numPoints: 150},
		{id: "alp-v2-big-endian", gen: alpMixed, tsEnc: format.TypeDelta, valComp: none, layout: FormatV2, bigEndian: true, useMetricID: true, numMetrics: 6, numPoints: 64},
		// The production configuration: shared DeltaPacked timestamps, no compression, no tags.
		// Small columns keep the compact 16-byte index.
		{id: "alp-v2-shared", gen: alpMixed, tsEnc: format.TypeDeltaPacked, valComp: none, layout: FormatV2Ext, useMetricID: true, numMetrics: 12, numPoints: 150},
		// A 72 KB raw column pushes the next value offset past uint16: extended 32-byte index.
		{id: "alp-v2ext-extended-index", gen: alpExtendedIndex, tsEnc: format.TypeDeltaPacked, valComp: none, layout: FormatV2Ext, useMetricID: true, numMetrics: 3, numPoints: 9000},
	}

	scenarios := make([]Scenario, 0, len(cases))
	for _, c := range cases {
		scenarios = append(scenarios, Scenario{
			ID:       c.id,
			BlobType: BlobTypeNumeric,
			Format:   c.layout,
			encode: func(startTime time.Time) ([]byte, *Manifest, error) {
				opts := []blob.NumericEncoderOption{
					blob.WithTimestampEncoding(c.tsEnc),
					blob.WithValueEncoding(format.TypeALP),
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
				opts = append(opts, layoutOpts[c.layout]...)

				return encodeNumericALP(startTime, c, opts)
			},
		})
	}

	return scenarios
}

func encodeNumericALP(startTime time.Time, c alpCase, opts []blob.NumericEncoderOption) ([]byte, *Manifest, error) {
	id, numMetrics, numPoints, useMetricID := c.id, c.numMetrics, c.numPoints, c.useMetricID
	enc, err := blob.NewNumericEncoder(startTime, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("new alp encoder %s: %w", id, err)
	}

	manifest := &Manifest{
		ScenarioID:     id,
		BlobType:       BlobTypeNumeric,
		Format:         c.layout,
		UseMetricID:    useMetricID,
		Metrics:        make([]ManifestMetric, 0, numMetrics),
		VerifyBorrowed: true,
	}

	stepUs := int64(15_000_000) // 15 seconds in μs
	for m := range numMetrics {
		metricID := uint64(6000 + m*13)
		metricName := fmt.Sprintf("alp.metric.%02d", m)
		ts := generateTimestamps(baseTimestampUs, stepUs, numPoints, true)
		vals := c.gen(m, numPoints)
		tags := generateTags(m, numPoints, c.tagsEnabled)

		if useMetricID {
			if err := enc.StartMetricID(metricID, numPoints); err != nil {
				return nil, nil, fmt.Errorf("start metric id %d: %w", metricID, err)
			}
		} else {
			metricID = 0 // read back by name
			if err := enc.StartMetricName(metricName, numPoints); err != nil {
				return nil, nil, fmt.Errorf("start metric name %s: %w", metricName, err)
			}
		}
		if err := enc.AddDataPoints(ts, vals, tags); err != nil {
			return nil, nil, fmt.Errorf("add data points metric %d: %w", m, err)
		}
		if err := enc.EndMetric(); err != nil {
			return nil, nil, fmt.Errorf("end metric %d: %w", m, err)
		}

		dps := make([]ManifestDataPoint, numPoints)
		for i := range numPoints {
			dps[i] = ManifestDataPoint{
				Timestamp: ts[i],
				ValueBits: float64ToBits(vals[i]),
				Tag:       tags[i],
			}
		}
		manifest.Metrics = append(manifest.Metrics, ManifestMetric{
			MetricID:   metricID,
			MetricName: metricName,
			DataPoints: dps,
		})
	}

	data, err := enc.Finish()
	if err != nil {
		return nil, nil, fmt.Errorf("finish %s: %w", id, err)
	}

	return data, manifest, nil
}
