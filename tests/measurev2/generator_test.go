package main

import "testing"

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
