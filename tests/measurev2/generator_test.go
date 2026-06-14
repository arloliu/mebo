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
