package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// BlobType identifies whether a scenario encodes numeric or text data.
type BlobType string

const (
	BlobTypeNumeric BlobType = "numeric"
	BlobTypeText    BlobType = "text"
	BlobTypeSet     BlobType = "blobset"
)

// FormatVersion records which layout was used when encoding.
type FormatVersion string

const (
	FormatV1    FormatVersion = "v1"
	FormatV2    FormatVersion = "v2"
	FormatV2Ext FormatVersion = "v2ext"
)

// ManifestDataPoint holds a single time-series observation.
// Float64 values are stored as their IEEE-754 uint64 bit pattern to survive
// JSON round-trip without precision loss.
type ManifestDataPoint struct {
	Timestamp int64  `json:"ts"`
	ValueBits uint64 `json:"val_bits"` // math.Float64bits(value)
	Tag       string `json:"tag"`
}

// ManifestMetric holds all data points for one metric in the manifest.
type ManifestMetric struct {
	MetricID   uint64              `json:"metric_id"`
	MetricName string              `json:"metric_name"` // empty when not used
	DataPoints []ManifestDataPoint `json:"data_points"`
	// IsCrossMemberCollision marks this metric as one half of a genuine
	// cross-member hash collision within a BlobTypeSet scenario (the other
	// half is a different ManifestMetric, in a different member blob,
	// sharing this one's MetricID). Unlike Manifest.HasRealCollision/
	// WantNamesPayload, which apply uniformly to every metric in a
	// scenario, this is per-metric: a blobset scenario can mix genuinely
	// colliding names with ordinary, unaffected ones (as
	// blobset-v1-mn-collision does), and only the colliding pair should
	// have BlobSet-level materialize checks tolerated on a pre-v1.10.0
	// binary — see verifyBlobSetMetricByNameAtSetLevel in verify.go, the
	// only place this is read.
	IsCrossMemberCollision bool `json:"is_cross_member_collision,omitempty"`
}

// Manifest is the golden record that the encode phase writes alongside each
// blob file.  The decode phase reads both and checks that every decoded value
// matches exactly.
type Manifest struct {
	ScenarioID  string           `json:"scenario_id"`
	BlobType    BlobType         `json:"blob_type"`
	Format      FormatVersion    `json:"format"`
	UseMetricID bool             `json:"use_metric_id"` // true → StartMetricID, false → StartMetricName
	Metrics     []ManifestMetric `json:"metrics"`
	// For blobset scenarios: multiple blob files that together form the set.
	BlobFiles []string `json:"blob_files,omitempty"`
	// ExpectErrIs names the specific sentinel error (a key into main.go's
	// namedSentinels registry, e.g. "ErrDuplicateMetricName") that a "must
	// reject" fixture's decode is required to fail with, checked via
	// errors.Is. Empty for most "must reject" fixtures (corruption/robustness),
	// which only require that decode fail at all with no specific sentinel
	// asserted, but a fixture may opt in when its corruption is known to
	// trip one specific, deterministic validation path (e.g.
	// corrupt-oversized-metric-count → ErrInvalidIndexEntrySize, or the
	// adversarial metric-names fixtures built by compat mncorrupt). Only
	// meaningful when the "reject" subcommand is used and Graceful is
	// false; ignored by "decode".
	ExpectErrIs string `json:"expect_err_is,omitempty"`
	// Graceful marks a fixture whose real contract is "the decoder must not
	// panic, hang, or read out of bounds" rather than "the decoder must
	// reject this blob". It exists for corruption fixtures that flip bits
	// in a codec's payload region (timestamps/values/tags): those bytes
	// carry no length-prefix or checksum, so a decoder may legitimately
	// either decode them as structurally valid-but-wrong values or fail
	// with a genuine decode error — both are acceptable outcomes for such a
	// fixture, and asserting "must reject" would be dishonest (see
	// corrupt-flipped-bits in robustness.go for the empirical evidence).
	// Only meaningful when the "reject" subcommand is used, and mutually
	// exclusive with ExpectErrIs in practice — a Graceful fixture has no
	// single expected sentinel to assert since it may not error at all.
	// Ignored by "decode".
	Graceful bool `json:"graceful,omitempty"`
	// VerifyBorrowed additionally decodes this scenario via
	// NewNumericDecoderBorrowed/NewTextDecoderBorrowed (the zero-copy,
	// alias-the-input decode path) and verifies it identically to the
	// default owning decode. These constructors don't exist before
	// v1.10.0, so the check is a no-op (not a failure) on a binary built
	// without the "metricnames" tag — see verify.go's
	// verifyNumericBorrowedImpl/verifyTextBorrowedImpl. Only meaningful for
	// BlobTypeNumeric/BlobTypeText scenarios used with "decode" (not
	// "reject").
	VerifyBorrowed bool `json:"verify_borrowed,omitempty"`
	// HasRealCollision marks a scenario whose blob genuinely contains two
	// distinct metrics sharing one hashed MetricID (as opposed to a
	// mncorrupt_metricnames.go adversarial fixture, which is hostile and
	// must be rejected). Materialize() collapsed such metrics into one
	// until the fix that shipped alongside v1.10.0, so a binary built
	// against an older module still has that gap — see
	// verify.go's materializeCollisionSafe, which this flag gates.
	HasRealCollision bool `json:"has_real_collision,omitempty"`
	// WantNamesPayload asserts nb.HasMetricNames()/tb.HasMetricNames() is
	// true, unconditionally on every version — independent of, and stricter
	// than, the OLD-tolerance branches gated by materializeByNameFallbackFixed
	// and materializeCollisionSafe. Those tolerances exist specifically to
	// accept a name resolving via hash-only fallback on a blob that
	// legitimately has no names payload; without this separate assertion,
	// the same tolerance could just as easily mask a REAL regression that
	// silently drops the names payload from a scenario that is supposed to
	// have one (e.g. an encoder bug under WithMetricNames()), since a
	// dropped-payload blob and an intentionally-names-free blob look
	// identical to that tolerance check. Set true only on scenarios that
	// explicitly force names on (WithMetricNames(), a real collision, or
	// text's names-on-by-default); left false (no assertion) elsewhere.
	WantNamesPayload bool `json:"want_names_payload,omitempty"`
}

// bitsToFloat64 converts stored bit pattern back to float64.
func bitsToFloat64(bits uint64) float64 {
	return math.Float64frombits(bits)
}

// float64ToBits converts a float64 to its IEEE-754 bit pattern.
func float64ToBits(v float64) uint64 {
	return math.Float64bits(v)
}

// writeManifest serialises m to <dir>/<scenarioID>.json.
func writeManifest(dir string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest %s: %w", m.ScenarioID, err)
	}
	path := filepath.Join(dir, m.ScenarioID+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write manifest %s: %w", path, err)
	}
	return nil
}

// readManifest reads and deserialises the manifest at <dir>/<scenarioID>.json.
func readManifest(dir, scenarioID string) (*Manifest, error) {
	path := filepath.Join(dir, scenarioID+".json")
	data, err := os.ReadFile(path) //nolint:gosec // path is built from trusted CLI args
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest %s: %w", path, err)
	}
	return &m, nil
}

// writeBlobFile writes raw blob bytes to <dir>/<scenarioID>.blob.
func writeBlobFile(dir, scenarioID string, data []byte) error {
	path := filepath.Join(dir, scenarioID+".blob")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write blob %s: %w", path, err)
	}
	return nil
}

// readBlobFile reads raw blob bytes from <dir>/<scenarioID>.blob.
func readBlobFile(dir, scenarioID string) ([]byte, error) {
	path := filepath.Join(dir, scenarioID+".blob")
	data, err := os.ReadFile(path) //nolint:gosec // path is built from trusted CLI args
	if err != nil {
		return nil, fmt.Errorf("read blob %s: %w", path, err)
	}
	return data, nil
}

// generateTimestamps creates n evenly-spaced timestamps (μs) starting at
// baseUs with the given step.  A fixed seed-based jitter can be added for
// irregular series.
func generateTimestamps(baseUs int64, stepUs int64, n int, jitter bool) []int64 {
	ts := make([]int64, n)
	for i := range n {
		t := baseUs + int64(i)*stepUs
		if jitter {
			// Simple deterministic jitter: ±(i%5)*1000 μs
			t += int64(i%5-2) * 1000
		}
		ts[i] = t
	}
	return ts
}

// generateValues creates n float64 values with a deterministic pattern.
// seed is used to differentiate across metrics.
func generateValues(seed int, n int) []float64 {
	vals := make([]float64, n)
	for i := range n {
		// Use a simple formula to get varied but reproducible values.
		vals[i] = float64(seed*100+i) + 0.5
	}
	return vals
}

// generateTags creates n tag strings, empty if tagsEnabled is false.
func generateTags(seed int, n int, tagsEnabled bool) []string {
	tags := make([]string, n)
	if !tagsEnabled {
		return tags
	}
	for i := range n {
		tags[i] = fmt.Sprintf("host=server%02d", (seed+i)%10)
	}
	return tags
}
