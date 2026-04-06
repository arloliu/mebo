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
