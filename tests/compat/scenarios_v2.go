//go:build v2

package main

import (
	"fmt"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
)

func init() {
	allScenarios = append(allScenarios, v2NumericScenarios()...)
}

func v2NumericScenarios() []Scenario {
	type v2Case struct {
		id             string
		tsEnc          format.EncodingType
		valEnc         format.EncodingType
		tsComp         format.CompressionType
		valComp        format.CompressionType
		tagsEnabled    bool
		sharedTS       bool
		numMetrics     int
		numPoints      int
		jitter         bool
		expectedFormat FormatVersion
	}

	cases := []v2Case{
		{id: "num-v2-compact", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, sharedTS: false, numMetrics: 5, numPoints: 10, expectedFormat: FormatV2},
		{id: "num-v2-chimp", tsEnc: format.TypeDelta, valEnc: format.TypeChimp, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, sharedTS: false, numMetrics: 5, numPoints: 10, expectedFormat: FormatV2},
		{id: "num-v2-packed-delta", tsEnc: format.TypeDeltaPacked, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, sharedTS: false, numMetrics: 5, numPoints: 10, expectedFormat: FormatV2},
		{id: "num-v2-shared-ts", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, sharedTS: true, numMetrics: 5, numPoints: 10, expectedFormat: FormatV2},
		{id: "num-v2-shared-chimp", tsEnc: format.TypeDelta, valEnc: format.TypeChimp, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, sharedTS: true, numMetrics: 5, numPoints: 10, expectedFormat: FormatV2},
		{id: "num-v2-tagged", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: true, sharedTS: false, numMetrics: 3, numPoints: 5, expectedFormat: FormatV2},
		{id: "num-v2-zstd", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionZstd, valComp: format.CompressionZstd, tagsEnabled: false, sharedTS: false, numMetrics: 5, numPoints: 10, expectedFormat: FormatV2},
		{id: "num-v2-packed-shared", tsEnc: format.TypeDeltaPacked, valEnc: format.TypeChimp, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, sharedTS: true, numMetrics: 8, numPoints: 12, expectedFormat: FormatV2},
		{id: "num-v2-raw-raw", tsEnc: format.TypeRaw, valEnc: format.TypeRaw, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, sharedTS: false, numMetrics: 3, numPoints: 5, expectedFormat: FormatV2},
		{id: "num-v2-jitter-shared", tsEnc: format.TypeDelta, valEnc: format.TypeGorilla, tsComp: format.CompressionNone, valComp: format.CompressionNone, tagsEnabled: false, sharedTS: true, numMetrics: 5, numPoints: 15, jitter: true, expectedFormat: FormatV2},
	}

	scenarios := make([]Scenario, 0, len(cases))
	for _, c := range cases {
		c := c
		scenarios = append(scenarios, Scenario{
			ID:       c.id,
			BlobType: BlobTypeNumeric,
			Format:   c.expectedFormat,
			encode: func(startTime time.Time) ([]byte, *Manifest, error) {
				return encodeNumericV2(startTime, c.id, c.tsEnc, c.valEnc, c.tsComp, c.valComp, c.tagsEnabled, c.sharedTS, c.numMetrics, c.numPoints, c.jitter, c.expectedFormat)
			},
		})
	}
	return scenarios
}

func encodeNumericV2(
	startTime time.Time,
	id string,
	tsEnc format.EncodingType,
	valEnc format.EncodingType,
	tsComp format.CompressionType,
	valComp format.CompressionType,
	tagsEnabled bool,
	sharedTS bool,
	numMetrics int,
	numPoints int,
	jitter bool,
	expectedFormat FormatVersion,
) ([]byte, *Manifest, error) {
	opts := []blob.NumericEncoderOption{
		blob.WithTimestampEncoding(tsEnc),
		blob.WithValueEncoding(valEnc),
		blob.WithTimestampCompression(tsComp),
		blob.WithValueCompression(valComp),
		blob.WithTagsEnabled(tagsEnabled),
		blob.WithLittleEndian(),
	}
	if sharedTS {
		opts = append(opts, blob.WithSharedTimestamps())
	} else {
		opts = append(opts, blob.WithBlobLayoutV2())
	}

	enc, err := blob.NewNumericEncoder(startTime, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("new v2 encoder %s: %w", id, err)
	}

	manifest := &Manifest{
		ScenarioID:  id,
		BlobType:    BlobTypeNumeric,
		Format:      expectedFormat,
		UseMetricID: true,
		Metrics:     make([]ManifestMetric, 0, numMetrics),
	}

	stepUs := int64(60_000_000) // 60 seconds in μs
	for m := range numMetrics {
		metricID := uint64(3000 + m*11)
		ts := generateTimestamps(baseTimestampUs, stepUs, numPoints, jitter)
		vals := generateValues(m+100, numPoints)
		tags := generateTags(m, numPoints, tagsEnabled)

		if err := enc.StartMetricID(metricID, numPoints); err != nil {
			return nil, nil, fmt.Errorf("start metric id %d: %w", metricID, err)
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
			MetricName: fmt.Sprintf("metric.%02d", m),
			DataPoints: dps,
		})
	}

	data, err := enc.Finish()
	if err != nil {
		return nil, nil, fmt.Errorf("finish %s: %w", id, err)
	}
	return data, manifest, nil
}
