package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"slices"
	"testing"
)

func TestProfilesGenerateValidData(t *testing.T) {
	cfg := DataConfig{NumMetrics: 8, PointsPerMetric: 300, Seed: 42}
	for _, p := range Profiles() {
		d := GenerateProfile(p, cfg)
		if len(d.Values) != cfg.NumMetrics*cfg.PointsPerMetric {
			t.Fatalf("%s: wrong value count", p.Name)
		}
		// timestamps must be strictly increasing per metric
		// (spot-check first metric)
		for j := 1; j < cfg.PointsPerMetric; j++ {
			if d.Timestamps[j] <= d.Timestamps[j-1] {
				t.Fatalf("%s: timestamps not strictly increasing at index %d: %d <= %d",
					p.Name, j, d.Timestamps[j], d.Timestamps[j-1])
			}
		}
	}
}

func TestFindProfile(t *testing.T) {
	if _, ok := findProfile("decimal_gauge_2dp"); !ok {
		t.Fatal("decimal_gauge_2dp should resolve")
	}
	if _, ok := findProfile("nope"); ok {
		t.Fatal("unknown profile must not resolve")
	}
}

func TestShareTimestamps(t *testing.T) {
	cfg := DataConfig{NumMetrics: 4, PointsPerMetric: 100, Seed: 42}
	p, _ := findProfile("decimal_gauge_2dp")
	shared := GenerateProfile(p, cfg).shareTimestamps()
	ppm := cfg.PointsPerMetric
	for m := 1; m < cfg.NumMetrics; m++ {
		for j := range ppm {
			if shared.Timestamps[m*ppm+j] != shared.Timestamps[j] {
				t.Fatalf("metric %d point %d ts not shared with metric 0", m, j)
			}
		}
	}
}

// TestProfileHoldRepeats checks that a Hold profile repeats about that share of points.
func TestProfileHoldRepeats(t *testing.T) {
	cfg := DataConfig{NumMetrics: 20, PointsPerMetric: 150, Seed: 42}
	for _, tc := range []struct {
		name     string
		min, max float64
	}{
		{"cal_2dp_hold30", 0.25, 0.40},
		{"cal_2dp_hold50", 0.45, 0.60},
		{"cal_2dp_hold70", 0.65, 0.80},
	} {
		p, ok := findProfile(tc.name)
		if !ok {
			t.Fatalf("%s should resolve", tc.name)
		}
		d := GenerateProfile(p, cfg)
		repeats, pairs := 0, 0
		for m := range cfg.NumMetrics {
			for j := 1; j < cfg.PointsPerMetric; j++ {
				pairs++
				if d.Values[m*cfg.PointsPerMetric+j] == d.Values[m*cfg.PointsPerMetric+j-1] {
					repeats++
				}
			}
		}
		share := float64(repeats) / float64(pairs)
		if share < tc.min || share > tc.max {
			t.Fatalf("%s: %.2f of points repeat, want %.2f..%.2f", tc.name, share, tc.min, tc.max)
		}
	}
}

// TestProfilesByteIdentical pins every profile's data, so published numbers measured on them stay reproducible.
// The hashes cover metric IDs, timestamps and value bits at 20 metrics × 150 points, seed 42.
// A mixed profile's hash also pins its parts, shares and timestamp model.
func TestProfilesByteIdentical(t *testing.T) {
	cfg := DataConfig{NumMetrics: 20, PointsPerMetric: 150, ValueJitterPct: 0.5, TSJitterPct: 0.1, Seed: 42}
	want := map[string]string{
		"decimal_gauge_2dp":  "4908652da2ce602578ceaa66",
		"decimal_gauge_4dp":  "2164847fe984d46b1b67b99c",
		"counter":            "6ac1c50203f94672bfa1ce6f",
		"sparse_constant":    "199e63e155e3326c7eff215a",
		"regular_scrape_60s": "4db6051287f2dea8a659144a",
		"bursty_scrape":      "48aa4f054efed8c76f3c1815",
		"worst_case":         "083fbc45e1dc3bae1ee243eb",
		"cal_2dp_hold30":     "ca80c9b2a257dfcc15eab1fc",
		"cal_2dp_hold50":     "61f2d6083b37023551eec657",
		"cal_2dp_hold70":     "7eda1bcb72c127c7f800e18a",
		"cal_2dp_step0.005":  "1c5276d94bb9ed43b1269782",
		"cal_1dp_step0.03":   "31716a6017113ec056948ef2",
		"cal_1dp_step0.01":   "fa2a034747478b860d8537ec",
		"legacy_random_walk": "fdf99108362bcc31c627adeb",
		"mix_monitoring":     "d836762cb0018267a61df045",
		"mix_sensor":         "3f95519c6dccad37dbc07bfc",
		"mix_integer":        "3f95c4f0e0b19b126e3009c1",
		"mix_fullprec":       "2facdd0213bf78b73084ec7c",
	}
	if len(want) != len(Profiles()) {
		t.Fatalf("%d pinned hashes for %d profiles", len(want), len(Profiles()))
	}
	for name, h := range want {
		p, ok := findProfile(name)
		if !ok {
			t.Fatalf("%s should resolve", name)
		}
		if got := hashTestData(GenerateProfile(p, cfg)); got != h {
			t.Errorf("%s: data hash %s, want %s", name, got, h)
		}
	}
	legacy, _ := findProfile("legacy_random_walk")
	_, shared := generateDatasets(legacy, cfg)
	if got := hashTestData(shared); got != "3a7c5eb0545926ecf6f89286" {
		t.Errorf("legacy shared timestamps: data hash %s", got)
	}
}

func hashTestData(d *TestData) string {
	buf := make([]byte, 0, 8*(len(d.MetricIDs)+len(d.Timestamps)+len(d.Values)))
	for _, id := range d.MetricIDs {
		buf = binary.LittleEndian.AppendUint64(buf, id)
	}
	for _, ts := range d.Timestamps {
		buf = binary.LittleEndian.AppendUint64(buf, uint64(ts))
	}
	for _, v := range d.Values {
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(v))
	}
	sum := sha256.Sum256(buf)

	return hex.EncodeToString(sum[:12])
}

// TestMixProfiles checks the mixed blobs' shape: every metric present once, unique IDs,
// parts in their shares, and aligned timestamps that are on the grid except for a few 2-10 ms misses.
func TestMixProfiles(t *testing.T) {
	cfg := DataConfig{NumMetrics: 100, PointsPerMetric: 150, Seed: 42}
	for _, name := range []string{"mix_monitoring", "mix_sensor", "mix_integer", "mix_fullprec"} {
		p, ok := findProfile(name)
		if !ok {
			t.Fatalf("%s should resolve", name)
		}
		d := GenerateProfile(p, cfg)
		total := cfg.NumMetrics * cfg.PointsPerMetric
		if len(d.MetricIDs) != cfg.NumMetrics || len(d.Values) != total || len(d.Timestamps) != total {
			t.Fatalf("%s: %d IDs, %d values, %d timestamps", name, len(d.MetricIDs), len(d.Values), len(d.Timestamps))
		}

		seen := make(map[uint64]bool, cfg.NumMetrics)
		for _, id := range d.MetricIDs {
			if seen[id] {
				t.Fatalf("%s: duplicate metric ID %d", name, id)
			}
			seen[id] = true
		}

		// Full-precision parts are the only ones with values that need more than 2 decimals.
		fullShare := 0.0
		for _, part := range p.Parts {
			if part.Profile.Decimals < 0 {
				fullShare += part.Share
			}
		}
		full := 0
		for m := range cfg.NumMetrics {
			v := d.Values[m*cfg.PointsPerMetric+1]
			if math.Abs(v*100-math.Round(v*100)) > 1e-6 {
				full++
			}
		}
		if math.Abs(fullShare*float64(cfg.NumMetrics)-float64(full)) > 1 {
			t.Errorf("%s: %d full-precision metrics, want about %.0f", name, full, fullShare*float64(cfg.NumMetrics))
		}

		start := d.StartTime.UnixMicro()
		interval := p.IntervalMs * 1000
		off := 0
		for i, ts := range d.Timestamps {
			j := int64(i%cfg.PointsPerMetric) + 1
			delta := ts - (start + j*interval)
			if delta == 0 {
				continue
			}
			off++
			if a := abs64(delta); a < int64(p.TSJitterMinMs*1000) || a > int64(p.TSJitterMaxMs*1000) {
				t.Fatalf("%s: timestamp %d is %d µs off the grid, want %.0f..%.0f ms", name, i, delta, p.TSJitterMinMs, p.TSJitterMaxMs)
			}
		}
		if share := float64(off) / float64(len(d.Timestamps)); math.Abs(share-p.TSJitterShare) > 0.01 {
			t.Errorf("%s: %.3f of timestamps off the grid, want about %.2f", name, share, p.TSJitterShare)
		}
	}
}

// TestMixPartCounts pins how many metrics each part of a mixed profile gets at the documented 100 metrics,
// and the rounding rule: each part rounds its share, and the last part takes the rest.
func TestMixPartCounts(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		want []int
	}{
		{"mix_monitoring", 100, []int{35, 20, 18, 27}},
		{"mix_sensor", 100, []int{50, 20, 11, 19}},
		{"mix_integer", 100, []int{30, 21, 35, 14}},
		{"mix_fullprec", 100, []int{20, 10, 5, 40, 25}},
		{"mix_monitoring", 7, []int{2, 1, 1, 3}},
	} {
		p, ok := findProfile(tc.name)
		if !ok {
			t.Fatalf("%s should resolve", tc.name)
		}
		if got := mixPartCounts(p.Parts, tc.n); !slices.Equal(got, tc.want) {
			t.Errorf("%s at %d metrics: part counts %v, want %v", tc.name, tc.n, got, tc.want)
		}
	}

	// Rounded shares that overrun the total are clipped, and the last part takes what is left.
	parts := []ProfilePart{{Share: 0.6}, {Share: 0.6}, {Share: 0.1}}
	if got, want := mixPartCounts(parts, 10), []int{6, 4, 0}; !slices.Equal(got, want) {
		t.Errorf("overrunning shares: part counts %v, want %v", got, want)
	}
	parts = []ProfilePart{{Share: 0.5}, {Share: 0.5}}
	if got, want := mixPartCounts(parts, 3), []int{2, 1}; !slices.Equal(got, want) {
		t.Errorf("half-way rounding: part counts %v, want %v", got, want)
	}
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}

	return x
}

// TestMixCalibration pins the documented calibration:
// about 3.8 B/point for Chimp at 100 metrics × 150 points with shared DeltaPacked timestamps and no compression.
func TestMixCalibration(t *testing.T) {
	cfg := DataConfig{NumMetrics: 100, PointsPerMetric: 150, Seed: 42}
	combo := sharedComboByLabel(t, "shared-deltapacked-chimp")
	for _, name := range []string{"mix_monitoring", "mix_sensor", "mix_integer", "mix_fullprec"} {
		p, _ := findProfile(name)
		_, shared := generateDatasets(p, cfg)
		n, err := measureEncodedSize(combo, shared)
		if err != nil {
			t.Fatal(err)
		}
		if bpp := float64(n) / float64(cfg.NumMetrics*cfg.PointsPerMetric); math.Abs(bpp-3.8) > 0.15 {
			t.Errorf("%s: Chimp %.3f B/point, want 3.8 ± 0.15", name, bpp)
		}
	}
}

func sharedComboByLabel(t *testing.T, label string) EncodingCombo {
	t.Helper()
	for _, c := range SharedTSCombos() {
		if c.Label == label {
			return c
		}
	}
	t.Fatalf("no combo %s", label)

	return EncodingCombo{}
}
