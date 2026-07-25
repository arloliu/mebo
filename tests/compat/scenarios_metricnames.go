//go:build metricnames

package main

import (
	"fmt"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
)

// This file registers compat scenarios that exercise the v1.10.0 "metric
// names storage" feature (WithMetricNames / WithoutMetricNames / strip /
// out-of-order insertion). It is gated behind the "metricnames" build tag —
// mirroring the "v2" tag in scenarios_v2.go — because it references public
// symbols (blob.WithMetricNames, blob.WithoutMetricNames,
// blob.StripMetricNames) that do not exist before v1.10.0. Without the tag
// (e.g. building this source tree against the v1.9.0 module), this file is
// excluded and scenarios_metricnames_stub.go's empty init keeps the binary
// buildable.
//
// These scenarios all produce ordinary, non-adversarial blobs meant to prove
// the "must decode" side of the v1.9.0<->v1.10.0 compat matrix: a v1.9.0
// reader must be able to decode every one of them, because the wire-format
// concepts involved (names payload, MetricNamesMask flag, V1/V2/V2Ext
// layouts) all predate v1.10.0 — v1.10.0 only adds new *public* ways to
// reach them (forced-on names, opt-out, strip) plus decode-time hardening.
// Adversarial "must reject" fixtures live in mncorrupt_metricnames.go.

func init() {
	allScenarios = append(allScenarios, metricNamesScenarios()...)
}

func metricNamesScenarios() []Scenario {
	return []Scenario{
		{ID: "mn-num-v1-names", BlobType: BlobTypeNumeric, Format: FormatV1, encode: encodeMnNumV1Names},
		{ID: "mn-num-v2-names", BlobType: BlobTypeNumeric, Format: FormatV2, encode: encodeMnNumV2Names},
		{ID: "mn-num-v2ext-names", BlobType: BlobTypeNumeric, Format: FormatV2Ext, encode: encodeMnNumV2ExtNames},
		{ID: "mn-txt-names", BlobType: BlobTypeText, Format: FormatV1, encode: encodeMnTxtNames},
		{ID: "mn-txt-no-names", BlobType: BlobTypeText, Format: FormatV1, encode: encodeMnTxtNoNames},
		{ID: "mn-num-v2-outoforder", BlobType: BlobTypeNumeric, Format: FormatV2, encode: encodeMnNumV2OutOfOrder},
		{ID: "mn-num-v1-stripped", BlobType: BlobTypeNumeric, Format: FormatV1, encode: encodeMnNumV1Stripped},
		{ID: "mn-num-v2-stripped", BlobType: BlobTypeNumeric, Format: FormatV2, encode: encodeMnNumV2Stripped},
		{ID: "mn-num-v1-collision", BlobType: BlobTypeNumeric, Format: FormatV1, encode: encodeMnNumV1Collision},
		{ID: "mn-num-v2-collision", BlobType: BlobTypeNumeric, Format: FormatV2, encode: encodeMnNumV2Collision},
		{ID: "mn-txt-collision", BlobType: BlobTypeText, Format: FormatV1, encode: encodeMnTxtCollision},
		{ID: "blobset-v1-mn-collision", BlobType: BlobTypeSet, Format: FormatV1, encode: encodeMnBlobSetCollision},
	}
}

// ---------------------------------------------------------------------------
// Numeric: names forced on via WithMetricNames() (name mode only).
// ---------------------------------------------------------------------------

func encodeMnNumV1Names(startTime time.Time) ([]byte, *Manifest, error) {
	return encodeMnNumeric(startTime, "mn-num-v1-names", FormatV1, nil, 5, 8, false)
}

func encodeMnNumV2Names(startTime time.Time) ([]byte, *Manifest, error) {
	return encodeMnNumeric(startTime, "mn-num-v2-names", FormatV2, []blob.NumericEncoderOption{blob.WithBlobLayoutV2()}, 5, 8, false)
}

func encodeMnNumV2ExtNames(startTime time.Time) ([]byte, *Manifest, error) {
	return encodeMnNumeric(startTime, "mn-num-v2ext-names", FormatV2Ext, []blob.NumericEncoderOption{blob.WithSharedTimestamps()}, 6, 12, true)
}

// encodeMnNumeric builds a names-bearing numeric blob (WithMetricNames
// forced on, name mode) and its manifest.
func encodeMnNumeric(
	startTime time.Time,
	id string,
	fv FormatVersion,
	extraOpts []blob.NumericEncoderOption,
	numMetrics int,
	numPoints int,
	tagsEnabled bool,
) ([]byte, *Manifest, error) {
	opts := append([]blob.NumericEncoderOption{
		blob.WithMetricNames(),
		blob.WithLittleEndian(),
		blob.WithTagsEnabled(tagsEnabled),
	}, extraOpts...)

	enc, err := blob.NewNumericEncoder(startTime, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("new encoder %s: %w", id, err)
	}

	manifest := &Manifest{
		ScenarioID:       id,
		BlobType:         BlobTypeNumeric,
		Format:           fv,
		UseMetricID:      false,
		Metrics:          make([]ManifestMetric, 0, numMetrics),
		WantNamesPayload: true, // WithMetricNames() is always set above
	}

	stepUs := int64(45_000_000)
	for m := range numMetrics {
		name := fmt.Sprintf("mn.metric.%02d", m)
		ts := generateTimestamps(baseTimestampUs, stepUs, numPoints, false)
		vals := generateValues(m+500, numPoints)
		tags := generateTags(m, numPoints, tagsEnabled)

		if err := enc.StartMetricName(name, numPoints); err != nil {
			return nil, nil, fmt.Errorf("start metric name %s: %w", name, err)
		}
		if err := enc.AddDataPoints(ts, vals, tags); err != nil {
			return nil, nil, fmt.Errorf("add data points %s: %w", name, err)
		}
		if err := enc.EndMetric(); err != nil {
			return nil, nil, fmt.Errorf("end metric %s: %w", name, err)
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
			MetricName: name,
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
// Text: default (names on) and WithoutMetricNames() opt-out.
// ---------------------------------------------------------------------------

func encodeMnTxtNames(startTime time.Time) ([]byte, *Manifest, error) {
	return encodeMnText(startTime, "mn-txt-names", nil, 4, 6, false, true)
}

func encodeMnTxtNoNames(startTime time.Time) ([]byte, *Manifest, error) {
	return encodeMnText(startTime, "mn-txt-no-names", []blob.TextEncoderOption{blob.WithoutMetricNames()}, 4, 6, true, false)
}

func encodeMnText(
	startTime time.Time,
	id string,
	extraOpts []blob.TextEncoderOption,
	numMetrics int,
	numPoints int,
	tagsEnabled bool,
	wantNames bool,
) ([]byte, *Manifest, error) {
	opts := append([]blob.TextEncoderOption{
		blob.WithTextTimestampEncoding(format.TypeDelta),
		blob.WithTextDataCompression(format.CompressionZstd),
		blob.WithTextTagsEnabled(tagsEnabled),
		blob.WithTextLittleEndian(),
	}, extraOpts...)

	enc, err := blob.NewTextEncoder(startTime, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("new text encoder %s: %w", id, err)
	}

	manifest := &Manifest{
		ScenarioID:       id,
		BlobType:         BlobTypeText,
		Format:           FormatV1,
		UseMetricID:      false,
		Metrics:          make([]ManifestMetric, 0, numMetrics),
		WantNamesPayload: wantNames,
	}

	stepUs := int64(20_000_000)
	for m := range numMetrics {
		name := fmt.Sprintf("mn.text.metric.%02d", m)
		ts := generateTimestamps(baseTimestampUs, stepUs, numPoints, false)
		tags := generateTags(m, numPoints, tagsEnabled)

		if err := enc.StartMetricName(name, numPoints); err != nil {
			return nil, nil, fmt.Errorf("start metric name %s: %w", name, err)
		}

		dps := make([]ManifestDataPoint, numPoints)
		for i := range numPoints {
			textVal := fmt.Sprintf("mn_state_%02d_%d", i, m)
			tag := tags[i]
			if err := enc.AddDataPoint(ts[i], textVal, tag); err != nil {
				return nil, nil, fmt.Errorf("add data point %d/%d: %w", m, i, err)
			}
			dps[i] = ManifestDataPoint{Timestamp: ts[i], ValueBits: float64ToBits(0)}
			if tagsEnabled {
				dps[i].Tag = textVal + "|" + tag
			} else {
				dps[i].Tag = textVal
			}
		}

		if err := enc.EndMetric(); err != nil {
			return nil, nil, fmt.Errorf("end metric %s: %w", name, err)
		}

		manifest.Metrics = append(manifest.Metrics, ManifestMetric{
			MetricName: name,
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
// V2 out-of-order insertion (verifies the encoder keeps names locked to the
// right data even after it permutes the index into MetricID order).
// ---------------------------------------------------------------------------

// encodeMnNumV2OutOfOrder inserts names in an order that does not match their
// final hash-sorted index order, forcing the encoder's sort+permute path
// (the encoder always sorts V2 index entries by MetricID) to keep every name
// aligned with its own data. No collision needed — this reproduces a
// name/index misalignment bug directly from the public API.
func encodeMnNumV2OutOfOrder(startTime time.Time) ([]byte, *Manifest, error) {
	id := "mn-num-v2-outoforder"
	enc, err := blob.NewNumericEncoder(startTime, blob.WithBlobLayoutV2(), blob.WithMetricNames(), blob.WithLittleEndian())
	if err != nil {
		return nil, nil, fmt.Errorf("new encoder %s: %w", id, err)
	}

	names := []string{
		"zzz.mn.outoforder",
		"aaa.mn.outoforder",
		"mmm.mn.outoforder",
		"bbb.mn.outoforder",
		"qqq.mn.outoforder",
	}
	manifest := &Manifest{
		ScenarioID:       id,
		BlobType:         BlobTypeNumeric,
		Format:           FormatV2,
		UseMetricID:      false,
		Metrics:          make([]ManifestMetric, 0, len(names)),
		WantNamesPayload: true, // WithMetricNames() is set above
	}

	for i, name := range names {
		ts := baseTimestampUs + int64(i)*1_000_000
		val := float64(i) + 0.25

		if err := enc.StartMetricName(name, 1); err != nil {
			return nil, nil, fmt.Errorf("start metric name %s: %w", name, err)
		}
		if err := enc.AddDataPoint(ts, val, ""); err != nil {
			return nil, nil, fmt.Errorf("add data point %s: %w", name, err)
		}
		if err := enc.EndMetric(); err != nil {
			return nil, nil, fmt.Errorf("end metric %s: %w", name, err)
		}

		manifest.Metrics = append(manifest.Metrics, ManifestMetric{
			MetricName: name,
			DataPoints: []ManifestDataPoint{{Timestamp: ts, ValueBits: float64ToBits(val)}},
		})
	}

	data, err := enc.Finish()
	if err != nil {
		return nil, nil, fmt.Errorf("finish %s: %w", id, err)
	}

	return data, manifest, nil
}

// ---------------------------------------------------------------------------
// Stripped output — must decode as an ordinary no-names blob.
// ---------------------------------------------------------------------------

func encodeMnNumV1Stripped(startTime time.Time) ([]byte, *Manifest, error) {
	return encodeMnStripped(startTime, "mn-num-v1-stripped", FormatV1, nil)
}

func encodeMnNumV2Stripped(startTime time.Time) ([]byte, *Manifest, error) {
	return encodeMnStripped(startTime, "mn-num-v2-stripped", FormatV2, []blob.NumericEncoderOption{blob.WithBlobLayoutV2()})
}

func encodeMnStripped(startTime time.Time, id string, fv FormatVersion, extraOpts []blob.NumericEncoderOption) ([]byte, *Manifest, error) {
	data, manifest, err := encodeMnNumeric(startTime, id, fv, extraOpts, 5, 8, false)
	if err != nil {
		return nil, nil, err
	}

	out, stripped, err := blob.StripMetricNames(nil, data)
	if err != nil {
		return nil, nil, fmt.Errorf("strip %s: %w", id, err)
	}
	if !stripped {
		return nil, nil, fmt.Errorf("strip %s: expected stripped=true (no collision present), got false", id)
	}

	// Post-strip, ByName resolution falls back to hash membership. With
	// no collision present that is behaviourally identical to exact-name
	// membership, so the existing by-name verify path still applies as-is.
	// Stripping is the whole point of this scenario, so — unlike the
	// pre-strip manifest encodeMnNumeric returned — the payload is expected
	// to be gone.
	manifest.WantNamesPayload = false

	return out, manifest, nil
}

// ---------------------------------------------------------------------------
// Real (non-adversarial) hash collision: two distinct metric names that
// genuinely hash to the same MetricID (mnNameA/mnNameB, declared in
// mncorrupt_metricnames.go — same package, same build tag), encoded through
// the ordinary public API with no byte patching. Unlike the adversarial
// fixtures in mncorrupt_metricnames.go (hostile byte-patched constructions
// built specifically to be REJECTED), these must decode successfully and
// resolve each name to its own, distinct data.
//
// Collision handling itself pre-dates v1.10.0 — the encoder has always
// forced the names payload on once a collision is detected — but collapsing
// two colliding metrics into one has been a real, previously-shipped defect
// on more than one read path (see the "Fixed" section of CHANGELOG.md for
// this release: materialized blobs used to do exactly this). These
// scenarios, combined with verify.go's Materialize()/MaterializeMetric(ByName)
// checks, exercise both the raw and materialized resolution paths against a
// genuine collision instead of only the byte-patched adversarial shape.
func encodeMnNumV1Collision(startTime time.Time) ([]byte, *Manifest, error) {
	return encodeMnNumericCollision(startTime, "mn-num-v1-collision", FormatV1, nil)
}

func encodeMnNumV2Collision(startTime time.Time) ([]byte, *Manifest, error) {
	return encodeMnNumericCollision(startTime, "mn-num-v2-collision", FormatV2, []blob.NumericEncoderOption{blob.WithBlobLayoutV2()})
}

func encodeMnNumericCollision(startTime time.Time, id string, fv FormatVersion, extraOpts []blob.NumericEncoderOption) ([]byte, *Manifest, error) {
	opts := append([]blob.NumericEncoderOption{
		blob.WithLittleEndian(),
	}, extraOpts...)

	enc, err := blob.NewNumericEncoder(startTime, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("new encoder %s: %w", id, err)
	}

	manifest := &Manifest{
		ScenarioID:       id,
		BlobType:         BlobTypeNumeric,
		Format:           fv,
		UseMetricID:      false,
		Metrics:          make([]ManifestMetric, 0, 2),
		VerifyBorrowed:   true,
		HasRealCollision: true,
		WantNamesPayload: true, // a real collision always forces names on
	}

	const numPoints = 6
	stepUs := int64(60_000_000)
	for i, name := range []string{mnNameA, mnNameB} {
		ts := generateTimestamps(baseTimestampUs, stepUs, numPoints, false)
		vals := generateValues(700+i, numPoints)

		if err := enc.StartMetricName(name, numPoints); err != nil {
			return nil, nil, fmt.Errorf("start metric name %s: %w", name, err)
		}
		if err := enc.AddDataPoints(ts, vals, nil); err != nil {
			return nil, nil, fmt.Errorf("add data points %s: %w", name, err)
		}
		if err := enc.EndMetric(); err != nil {
			return nil, nil, fmt.Errorf("end metric %s: %w", name, err)
		}

		dps := make([]ManifestDataPoint, numPoints)
		for j := range numPoints {
			dps[j] = ManifestDataPoint{Timestamp: ts[j], ValueBits: float64ToBits(vals[j])}
		}
		manifest.Metrics = append(manifest.Metrics, ManifestMetric{
			MetricName: name,
			DataPoints: dps,
		})
	}

	data, err := enc.Finish()
	if err != nil {
		return nil, nil, fmt.Errorf("finish %s: %w", id, err)
	}

	return data, manifest, nil
}

// encodeMnBlobSetCollision builds a 2-numeric + 1-text BlobSet (the same
// packMultiBlob shape as blobset-v1-mixed in scenarios.go) where the two
// numeric member blobs carry a genuine CROSS-MEMBER hash collision:
// mnNameA lives in member 0, mnNameB — which hashes to the same MetricID —
// lives in member 1. This exercises collision resolution at the blob-set
// boundary specifically (each member's own within-blob resolution is
// already covered by mn-num-*-collision above); VerifyBlobSet locates each
// member's metric by name (m.UseMetricID is false here) and checks both the
// raw and Materialize() paths per member.
func encodeMnBlobSetCollision(startTime time.Time) ([]byte, *Manifest, error) {
	id := "blobset-v1-mn-collision"

	nb0Data, nb0Manifest, err := encodeMnBlobSetCollisionMember(startTime, id+"-num0", mnNameA, "blobset.mn.extra.0")
	if err != nil {
		return nil, nil, fmt.Errorf("blobset numeric0: %w", err)
	}
	nb1Data, nb1Manifest, err := encodeMnBlobSetCollisionMember(startTime, id+"-num1", mnNameB, "blobset.mn.extra.1")
	if err != nil {
		return nil, nil, fmt.Errorf("blobset numeric1: %w", err)
	}

	tb0Data, tb0Manifest, err := encodeMnText(startTime, id+"-txt0", nil, 2, 4, false, true)
	if err != nil {
		return nil, nil, fmt.Errorf("blobset text: %w", err)
	}

	manifest := &Manifest{
		ScenarioID:  id,
		BlobType:    BlobTypeSet,
		Format:      FormatV1,
		UseMetricID: false,
		Metrics:     append(append(nb0Manifest.Metrics, nb1Manifest.Metrics...), tb0Manifest.Metrics...),
		BlobFiles:   []string{id + "-num0.blob", id + "-num1.blob", id + "-txt0.blob"},
		// Deliberately NOT HasRealCollision: no single member blob has an
		// internal collision (each carries only one of the two colliding
		// names), so per-member Materialize() in VerifyBlobSet's main loop
		// is genuinely unaffected on every version — confirmed empirically,
		// don't weaken that check by reusing HasRealCollision here too. The
		// collision only exists across members, and only for mnNameA/mnNameB
		// specifically — the two "extra" names and the text metrics are
		// ordinary and unaffected — so tolerance for it lives per-metric on
		// ManifestMetric.IsCrossMemberCollision (set on those two entries
		// above), not scenario-wide here.
		WantNamesPayload: true, // every member (2 numeric WithMetricNames(), 1 text default-on) carries names
	}

	packed := packMultiBlob(nb0Data, nb1Data, tb0Data)

	return packed, manifest, nil
}

// encodeMnBlobSetCollisionMember encodes one WithMetricNames() numeric blob
// carrying two metrics: collidingName (one half of a cross-member collision
// pair) and a second, ordinary, non-colliding name.
func encodeMnBlobSetCollisionMember(startTime time.Time, id, collidingName, extraName string) ([]byte, *Manifest, error) {
	enc, err := blob.NewNumericEncoder(startTime, blob.WithMetricNames(), blob.WithLittleEndian())
	if err != nil {
		return nil, nil, fmt.Errorf("new encoder %s: %w", id, err)
	}

	manifest := &Manifest{
		ScenarioID:  id,
		BlobType:    BlobTypeNumeric,
		Format:      FormatV1,
		UseMetricID: false,
		Metrics:     make([]ManifestMetric, 0, 2),
	}

	const numPoints = 5
	stepUs := int64(60_000_000)
	for i, name := range []string{collidingName, extraName} {
		ts := generateTimestamps(baseTimestampUs, stepUs, numPoints, false)
		vals := generateValues(800+i, numPoints)

		if err := enc.StartMetricName(name, numPoints); err != nil {
			return nil, nil, fmt.Errorf("start metric name %s: %w", name, err)
		}
		if err := enc.AddDataPoints(ts, vals, nil); err != nil {
			return nil, nil, fmt.Errorf("add data points %s: %w", name, err)
		}
		if err := enc.EndMetric(); err != nil {
			return nil, nil, fmt.Errorf("end metric %s: %w", name, err)
		}

		dps := make([]ManifestDataPoint, numPoints)
		for j := range numPoints {
			dps[j] = ManifestDataPoint{Timestamp: ts[j], ValueBits: float64ToBits(vals[j])}
		}
		manifest.Metrics = append(manifest.Metrics, ManifestMetric{
			MetricName: name,
			DataPoints: dps,
			// Only the first name (collidingName) is the actual collision
			// half; extraName is an ordinary, unaffected metric sharing
			// this member blob and must not have BlobSet-level materialize
			// checks tolerated on its account.
			IsCrossMemberCollision: i == 0,
		})
	}

	data, err := enc.Finish()
	if err != nil {
		return nil, nil, fmt.Errorf("finish %s: %w", id, err)
	}

	return data, manifest, nil
}

// encodeMnTxtCollision is the text-encoder counterpart of
// encodeMnNumericCollision. The text encoder stores names unconditionally by
// default, so this exercises collision resolution through that always-on
// path rather than the numeric encoder's collision-triggered one.
func encodeMnTxtCollision(startTime time.Time) ([]byte, *Manifest, error) {
	id := "mn-txt-collision"
	enc, err := blob.NewTextEncoder(startTime,
		blob.WithTextTimestampEncoding(format.TypeDelta),
		blob.WithTextDataCompression(format.CompressionZstd),
		blob.WithTextLittleEndian(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("new text encoder %s: %w", id, err)
	}

	manifest := &Manifest{
		ScenarioID:       id,
		BlobType:         BlobTypeText,
		Format:           FormatV1,
		UseMetricID:      false,
		Metrics:          make([]ManifestMetric, 0, 2),
		VerifyBorrowed:   true,
		HasRealCollision: true,
		WantNamesPayload: true, // text stores names by default; collision reinforces it
	}

	const numPoints = 4
	stepUs := int64(20_000_000)
	for i, name := range []string{mnNameA, mnNameB} {
		ts := generateTimestamps(baseTimestampUs, stepUs, numPoints, false)

		if err := enc.StartMetricName(name, numPoints); err != nil {
			return nil, nil, fmt.Errorf("start metric name %s: %w", name, err)
		}

		dps := make([]ManifestDataPoint, numPoints)
		for j := range numPoints {
			textVal := fmt.Sprintf("mn_collision_%02d_%d", j, i)
			if err := enc.AddDataPoint(ts[j], textVal, ""); err != nil {
				return nil, nil, fmt.Errorf("add data point %d/%d: %w", i, j, err)
			}
			dps[j] = ManifestDataPoint{Timestamp: ts[j], ValueBits: float64ToBits(0), Tag: textVal}
		}

		if err := enc.EndMetric(); err != nil {
			return nil, nil, fmt.Errorf("end metric %s: %w", name, err)
		}

		manifest.Metrics = append(manifest.Metrics, ManifestMetric{
			MetricName: name,
			DataPoints: dps,
		})
	}

	data, err := enc.Finish()
	if err != nil {
		return nil, nil, fmt.Errorf("finish %s: %w", id, err)
	}

	return data, manifest, nil
}
