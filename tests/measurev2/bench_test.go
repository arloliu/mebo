package main

import (
	"testing"

	"github.com/arloliu/mebo/format"
)

// BenchmarkBlobEncodeMixes measures whole-blob encode on the four mixed profiles at 100 metrics × 150 points,
// with shared DeltaPacked timestamps, for the Chimp, ALP and ALP-RLE value codecs.
// It is the benchmark behind the ALP encode speed gates in docs/specs/alp-simd-ef-search-design.md;
// run it with GODEBUG=cpu.avx512dq=off for the scalar ALP paths.
func BenchmarkBlobEncodeMixes(b *testing.B) {
	for _, prof := range []string{"mix_monitoring", "mix_integer", "mix_fullprec", "mix_sensor"} {
		p, ok := findProfile(prof)
		if !ok {
			b.Fatalf("profile %s not found", prof)
		}
		cfg := DataConfig{NumMetrics: 100, PointsPerMetric: 150, ValueJitterPct: 2, TSJitterPct: 5, Seed: 42, Profile: prof}
		data := GenerateProfile(p, cfg)
		pts := float64(cfg.NumMetrics * cfg.PointsPerMetric)
		for _, v := range []struct {
			name string
			enc  format.EncodingType
		}{{"chimp", format.TypeChimp}, {"alp", format.TypeALP}, {"alprle", format.TypeALPRLE}} {
			combo := EncodingCombo{TSEncoding: format.TypeDeltaPacked, ValEncoding: v.enc, SharedTS: true}
			b.Run(prof+"/"+v.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := encodeBlob(combo, data); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/pts, "ns/pt")
			})
		}
	}
}
