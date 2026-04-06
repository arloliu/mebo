package main

import (
	"fmt"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
)

// Scenario describes one encode/decode test case.
type Scenario struct {
	ID       string
	BlobType BlobType
	Format   FormatVersion
	// encode produces blob bytes and the matching manifest.
	encode func(startTime time.Time) ([]byte, *Manifest, error)
}

// allScenarios holds all scenarios registered for this binary.
// V2-specific scenarios are appended by init() in scenarios_v2.go (build
// tag "v2") or not at all via scenarios_v2_stub.go.
var allScenarios []Scenario

func init() {
	allScenarios = append(allScenarios, v1NumericScenarios()...)
	allScenarios = append(allScenarios, v1TextScenarios()...)
	allScenarios = append(allScenarios, v1BlobSetScenarios()...)
}

// baseStartTime is a fixed reference time used by all scenarios so that
// regenerated blobs are byte-for-byte identical across runs.
var baseStartTime = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// baseTimestampUs is the Unix microsecond timestamp for baseStartTime.
var baseTimestampUs = baseStartTime.UnixMicro()

// ---------------------------------------------------------------------------
// V1 Numeric scenarios
// ---------------------------------------------------------------------------

func v1NumericScenarios() []Scenario {
	type numericCase struct {
		id          string
		tsEnc       format.EncodingType
		valEnc      format.EncodingType
		tsComp      format.CompressionType
		valComp     format.CompressionType
		tagsEnabled bool
		bigEndian   bool
		useMetricID bool // true → StartMetricID, false → StartMetricName
		numMetrics  int
		numPoints   int
		jitter      bool
	}

	cases := []numericCase{
		{id: "num-v1-defaults", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, useMetricID: true, numMetrics: 5, numPoints: 10},
		{id: "num-v1-raw-raw", tsEnc: format.TypeRaw, valEnc: format.TypeRaw, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, useMetricID: true, numMetrics: 3, numPoints: 8},
		{id: "num-v1-tagged", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: true, useMetricID: true, numMetrics: 3, numPoints: 5},
		{id: "num-v1-zstd", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionZstd, valComp: format.CompressionZstd, tagsEnabled: false, useMetricID: true, numMetrics: 5, numPoints: 10},
		{id: "num-v1-s2", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionS2, valComp: format.CompressionS2, tagsEnabled: false, useMetricID: true, numMetrics: 5, numPoints: 10},
		{id: "num-v1-lz4", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionLZ4, valComp: format.CompressionLZ4, tagsEnabled: false, useMetricID: true, numMetrics: 5, numPoints: 10},
		{id: "num-v1-single-point", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, useMetricID: true, numMetrics: 1, numPoints: 1},
		{id: "num-v1-multi-metric", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, useMetricID: true, numMetrics: 50, numPoints: 5},
		{id: "num-v1-by-name", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, useMetricID: false, numMetrics: 5, numPoints: 10},
		{id: "num-v1-big-endian", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, bigEndian: true, useMetricID: true, numMetrics: 3, numPoints: 5},
		{id: "num-v1-jitter-ts", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, useMetricID: true, numMetrics: 5, numPoints: 15, jitter: true},
		{id: "num-v1-tagged-by-name", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: true, useMetricID: false, numMetrics: 3, numPoints: 5},
		{id: "num-v1-raw-zstd", tsEnc: format.TypeRaw, valEnc: format.TypeRaw, tsComp: format.CompressionZstd, valComp: format.CompressionZstd, tagsEnabled: false, useMetricID: true, numMetrics: 3, numPoints: 8},
	}

	scenarios := make([]Scenario, 0, len(cases))
	for _, c := range cases {
		c := c // capture
		scenarios = append(scenarios, Scenario{
			ID:       c.id,
			BlobType: BlobTypeNumeric,
			Format:   FormatV1,
			encode: func(startTime time.Time) ([]byte, *Manifest, error) {
				return encodeNumericV1(startTime, c.id, c.tsEnc, c.valEnc, c.tsComp, c.valComp, c.tagsEnabled, c.bigEndian, c.useMetricID, c.numMetrics, c.numPoints, c.jitter)
			},
		})
	}
	return scenarios
}

func encodeNumericV1(
	startTime time.Time,
	id string,
	tsEnc format.EncodingType,
	valEnc format.EncodingType,
	tsComp format.CompressionType,
	valComp format.CompressionType,
	tagsEnabled bool,
	bigEndian bool,
	useMetricID bool,
	numMetrics int,
	numPoints int,
	jitter bool,
) ([]byte, *Manifest, error) {
	return encodeNumericV1WithIDBase(startTime, id, tsEnc, valEnc, tsComp, valComp,
		tagsEnabled, bigEndian, useMetricID, numMetrics, numPoints, jitter, 1000)
}

func encodeNumericV1WithIDBase(
	startTime time.Time,
	id string,
	tsEnc format.EncodingType,
	valEnc format.EncodingType,
	tsComp format.CompressionType,
	valComp format.CompressionType,
	tagsEnabled bool,
	bigEndian bool,
	useMetricID bool,
	numMetrics int,
	numPoints int,
	jitter bool,
	metricIDBase uint64,
) ([]byte, *Manifest, error) {
	opts := []blob.NumericEncoderOption{
		blob.WithTimestampEncoding(tsEnc),
		blob.WithValueEncoding(valEnc),
		blob.WithTimestampCompression(tsComp),
		blob.WithValueCompression(valComp),
		blob.WithTagsEnabled(tagsEnabled),
	}
	if bigEndian {
		opts = append(opts, blob.WithBigEndian())
	} else {
		opts = append(opts, blob.WithLittleEndian())
	}

	enc, err := blob.NewNumericEncoder(startTime, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("new encoder %s: %w", id, err)
	}

	manifest := &Manifest{
		ScenarioID:  id,
		BlobType:    BlobTypeNumeric,
		Format:      FormatV1,
		UseMetricID: useMetricID,
		Metrics:     make([]ManifestMetric, 0, numMetrics),
	}

	stepUs := int64(60_000_000) // 60 seconds in μs
	for m := range numMetrics {
		metricID := metricIDBase + uint64(m*7)
		metricName := fmt.Sprintf("metric.%02d", m)
		ts := generateTimestamps(baseTimestampUs, stepUs, numPoints, jitter)
		vals := generateValues(m, numPoints)
		tags := generateTags(m, numPoints, tagsEnabled)

		if useMetricID {
			if err := enc.StartMetricID(metricID, numPoints); err != nil {
				return nil, nil, fmt.Errorf("start metric id %d: %w", metricID, err)
			}
		} else {
			metricID = 0 // not used; will be read back by name
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

// ---------------------------------------------------------------------------
// V1 Text scenarios
// ---------------------------------------------------------------------------

func v1TextScenarios() []Scenario {
	type textCase struct {
		id          string
		tsEnc       format.EncodingType
		dataComp    format.CompressionType
		tagsEnabled bool
		useMetricID bool
		numMetrics  int
		numPoints   int
	}

	cases := []textCase{
		{id: "txt-v1-defaults", tsEnc: format.TypeDelta, dataComp: format.CompressionZstd, tagsEnabled: false, useMetricID: true, numMetrics: 3, numPoints: 5},
		{id: "txt-v1-tagged", tsEnc: format.TypeDelta, dataComp: format.CompressionZstd, tagsEnabled: true, useMetricID: true, numMetrics: 3, numPoints: 5},
		{id: "txt-v1-no-compress", tsEnc: format.TypeDelta, dataComp: format.CompressionNone, tagsEnabled: false, useMetricID: true, numMetrics: 3, numPoints: 5},
		{id: "txt-v1-by-name", tsEnc: format.TypeDelta, dataComp: format.CompressionZstd, tagsEnabled: false, useMetricID: false, numMetrics: 3, numPoints: 5},
		{id: "txt-v1-raw-ts", tsEnc: format.TypeRaw, dataComp: format.CompressionNone, tagsEnabled: false, useMetricID: true, numMetrics: 2, numPoints: 4},
		{id: "txt-v1-tagged-by-name", tsEnc: format.TypeDelta, dataComp: format.CompressionZstd, tagsEnabled: true, useMetricID: false, numMetrics: 2, numPoints: 4},
	}

	scenarios := make([]Scenario, 0, len(cases))
	for _, c := range cases {
		c := c
		scenarios = append(scenarios, Scenario{
			ID:       c.id,
			BlobType: BlobTypeText,
			Format:   FormatV1,
			encode: func(startTime time.Time) ([]byte, *Manifest, error) {
				return encodeTextV1(startTime, c.id, c.tsEnc, c.dataComp, c.tagsEnabled, c.useMetricID, c.numMetrics, c.numPoints)
			},
		})
	}
	return scenarios
}

func encodeTextV1(
	startTime time.Time,
	id string,
	tsEnc format.EncodingType,
	dataComp format.CompressionType,
	tagsEnabled bool,
	useMetricID bool,
	numMetrics int,
	numPoints int,
) ([]byte, *Manifest, error) {
	opts := []blob.TextEncoderOption{
		blob.WithTextTimestampEncoding(tsEnc),
		blob.WithTextDataCompression(dataComp),
		blob.WithTextTagsEnabled(tagsEnabled),
		blob.WithTextLittleEndian(),
	}

	enc, err := blob.NewTextEncoder(startTime, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("new text encoder %s: %w", id, err)
	}

	manifest := &Manifest{
		ScenarioID:  id,
		BlobType:    BlobTypeText,
		Format:      FormatV1,
		UseMetricID: useMetricID,
		Metrics:     make([]ManifestMetric, 0, numMetrics),
	}

	stepUs := int64(30_000_000) // 30 seconds in μs
	for m := range numMetrics {
		metricID := uint64(2000 + m*13)
		metricName := fmt.Sprintf("text.metric.%02d", m)
		ts := generateTimestamps(baseTimestampUs, stepUs, numPoints, false)
		tags := generateTags(m, numPoints, tagsEnabled)

		if useMetricID {
			if err := enc.StartMetricID(metricID, numPoints); err != nil {
				return nil, nil, fmt.Errorf("start metric id %d: %w", metricID, err)
			}
		} else {
			metricID = 0
			if err := enc.StartMetricName(metricName, numPoints); err != nil {
				return nil, nil, fmt.Errorf("start metric name %s: %w", metricName, err)
			}
		}

		dps := make([]ManifestDataPoint, numPoints)
		for i := range numPoints {
			textVal := fmt.Sprintf("state_%02d_metric%d", i, m)
			tag := tags[i]
			if err := enc.AddDataPoint(ts[i], textVal, tag); err != nil {
				return nil, nil, fmt.Errorf("add data point %d/%d: %w", m, i, err)
			}
			dps[i] = ManifestDataPoint{
				Timestamp: ts[i],
				ValueBits: float64ToBits(0), // text: store value in Tag field repurposed as TextValue
				Tag:       tag,
			}
			// We store text values separately: reuse Tag for text since manifests
			// don't have a dedicated text field. Actually use a proper field below.
			dps[i].Tag = tag
			// Store text value in Tag field is ambiguous. Let's encode the text
			// value into ValueBits as a length-prefixed approach is too complex.
			// Instead: we'll embed text in the Tag field with a separator when
			// tagsEnabled=false, and use a separate marker otherwise.
			// Simplest correct approach: store text value in Tag field when tags disabled.
			// When both text value and real tag exist, use "textval|tag" encoding.
			if tagsEnabled {
				dps[i].Tag = textVal + "|" + tag
			} else {
				dps[i].Tag = textVal
			}
		}

		if err := enc.EndMetric(); err != nil {
			return nil, nil, fmt.Errorf("end metric %d: %w", m, err)
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

// ---------------------------------------------------------------------------
// V1 BlobSet scenarios
// ---------------------------------------------------------------------------

func v1BlobSetScenarios() []Scenario {
	return []Scenario{
		{
			ID:       "blobset-v1-mixed",
			BlobType: BlobTypeSet,
			Format:   FormatV1,
			encode:   encodeBlobSetV1Mixed,
		},
	}
}

// encodeBlobSetV1Mixed encodes 2 numeric blobs + 1 text blob into a BlobSet.
// The blob bytes are written as 3 separate files; the manifest lists them.
func encodeBlobSetV1Mixed(startTime time.Time) ([]byte, *Manifest, error) {
	// Build numeric blob 0 (metric IDs: 1000, 1007, 1014)
	nb0Data, nb0Manifest, err := encodeNumericV1WithIDBase(startTime, "blobset-v1-mixed-num0",
		format.TypeDelta, format.TypeGorilla,
		format.CompressionNone, format.CompressionNone,
		false, false, true, 3, 5, false, 1000)
	if err != nil {
		return nil, nil, fmt.Errorf("blobset numeric0: %w", err)
	}

	// Build numeric blob 1 (metric IDs: 5000, 5007 — non-overlapping with blob 0)
	nb1Data, nb1Manifest, err := encodeNumericV1WithIDBase(startTime, "blobset-v1-mixed-num1",
		format.TypeDelta, format.TypeGorilla,
		format.CompressionZstd, format.CompressionNone,
		false, false, true, 2, 8, false, 5000)
	if err != nil {
		return nil, nil, fmt.Errorf("blobset numeric1: %w", err)
	}

	// Build text blob
	tb0Data, tb0Manifest, err := encodeTextV1(startTime, "blobset-v1-mixed-txt0",
		format.TypeDelta, format.CompressionZstd,
		false, true, 2, 4)
	if err != nil {
		return nil, nil, fmt.Errorf("blobset text: %w", err)
	}

	// The BlobSet manifest references sub-manifests; for simplicity we store all
	// metrics in one flat manifest (resolver in verify.go handles by blobType).
	manifest := &Manifest{
		ScenarioID:  "blobset-v1-mixed",
		BlobType:    BlobTypeSet,
		Format:      FormatV1,
		UseMetricID: true,
		Metrics:     append(append(nb0Manifest.Metrics, nb1Manifest.Metrics...), tb0Manifest.Metrics...),
		BlobFiles: []string{
			"blobset-v1-mixed-num0.blob",
			"blobset-v1-mixed-num1.blob",
			"blobset-v1-mixed-txt0.blob",
		},
	}

	// Return the three blobs as concatenated storage is not applicable here.
	// The caller (encode subcommand) must handle BlobTypeSet specially by writing
	// each component blob separately.  We return just nb0Data as the "primary"
	// blob; the others are side-channel stored.
	// Better: pack as a length-prefixed multi-blob payload.
	packed := packMultiBlob(nb0Data, nb1Data, tb0Data)
	return packed, manifest, nil
}

// packMultiBlob creates a trivial length-prefixed container for multiple blobs.
// Format: 4-byte count, then for each blob: 4-byte length + bytes.
func packMultiBlob(blobs ...[]byte) []byte {
	totalLen := 4
	for _, b := range blobs {
		totalLen += 4 + len(b)
	}
	buf := make([]byte, 0, totalLen)
	buf = appendUint32LE(buf, uint32(len(blobs)))
	for _, b := range blobs {
		buf = appendUint32LE(buf, uint32(len(b)))
		buf = append(buf, b...)
	}
	return buf
}

// unpackMultiBlob parses the container written by packMultiBlob.
func unpackMultiBlob(data []byte) ([][]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("multi-blob too short")
	}
	count := readUint32LE(data, 0)
	pos := 4
	blobs := make([][]byte, 0, count)
	for range count {
		if pos+4 > len(data) {
			return nil, fmt.Errorf("multi-blob truncated at count entry")
		}
		length := readUint32LE(data, pos)
		pos += 4
		if pos+int(length) > len(data) {
			return nil, fmt.Errorf("multi-blob truncated at blob data")
		}
		blobs = append(blobs, data[pos:pos+int(length)])
		pos += int(length)
	}
	return blobs, nil
}

func appendUint32LE(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func readUint32LE(b []byte, off int) uint32 {
	return uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
}
