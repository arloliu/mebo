package encoding

import (
	"fmt"
	"slices"
	"testing"
)

type chimpBenchDataset struct {
	name    string
	values  []float64
	encoded []byte
	indexes []int
}

// Real-world inspired datasets with small value variety typical of monitoring/IoT metrics.
var chimpBenchDatasets = []chimpBenchDataset{
	// CPU utilization: hovers around 42%, tiny jitter ±0.1-0.3%
	buildChimpDataset("cpu_util_150", generateSmallJitterMetric(150, 42.0, 0.003, 0.001)),
	// Memory usage: slowly climbs from 67.2% to ~67.5%, sub-percent noise
	buildChimpDataset("mem_usage_500", generateDriftingMetric(500, 67.2, 0.0006, 0.002)),
	// Temperature sensor: 23.4°C ± 0.05°C, very stable
	buildChimpDataset("temperature_200", generateSmallJitterMetric(200, 23.4, 0.0005, 0.0002)),
	// Network throughput (Mbps): ~950 with occasional ±5 Mbps fluctuation
	buildChimpDataset("net_throughput_300", generateSmallJitterMetric(300, 950.0, 0.005, 0.002)),
	// Latency (ms): ~12.5ms with rare spikes to ~13ms, mostly identical readings
	buildChimpDataset("latency_1000", generateStickyMetric(1000, 12.5, 0.04, 0.15)),
	// Disk IOPS: ~4200, integer-like values with small drift
	buildChimpDataset("disk_iops_500", generateIntegerLikeMetric(500, 4200.0, 50.0, 0.1)),
	// Battery voltage: 3.72V, extremely stable, ±0.001V
	buildChimpDataset("battery_volt_100", generateSmallJitterMetric(100, 3.72, 0.0003, 0.0001)),
	// Request rate (req/s): ~1500, with realistic small drift and occasional plateau
	buildChimpDataset("request_rate_800", generateStepDriftMetric(800, 1500.0, 10.0, 0.3)),
}

func buildChimpDataset(name string, values []float64) chimpBenchDataset {
	encoder := NewNumericChimpEncoder()
	encoder.WriteSlice(values)
	encoded := slices.Clone(encoder.Bytes())
	encoder.Finish()

	return chimpBenchDataset{
		name:    name,
		values:  slices.Clone(values),
		encoded: encoded,
		indexes: makeAccessPattern(len(values)),
	}
}

// BenchmarkChimpVsGorilla_Encode compares encoding speed between Chimp and Gorilla.
func BenchmarkChimpVsGorilla_Encode(b *testing.B) {
	for _, dataset := range chimpBenchDatasets {
		b.Run(dataset.name, func(b *testing.B) {
			values := dataset.values

			b.Run("Gorilla", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for b.Loop() {
					enc := NewNumericGorillaEncoder()
					enc.WriteSlice(values)
					_ = enc.Bytes()
					enc.Finish()
				}
			})

			b.Run("Chimp", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for b.Loop() {
					enc := NewNumericChimpEncoder()
					enc.WriteSlice(values)
					_ = enc.Bytes()
					enc.Finish()
				}
			})
		})
	}
}

// BenchmarkChimpVsGorilla_DecodeAll compares sequential decode (All iterator) speed.
func BenchmarkChimpVsGorilla_DecodeAll(b *testing.B) {
	for _, dataset := range chimpBenchDatasets {
		b.Run(dataset.name, func(b *testing.B) {
			gorillaDS := gorillaBenchDatasets[findDatasetIndex(gorillaBenchDatasets, dataset.name)]
			count := len(dataset.values)

			b.Run("Gorilla", func(b *testing.B) {
				decoder := NewNumericGorillaDecoder()
				data := gorillaDS.encoded

				b.ReportAllocs()
				b.ResetTimer()

				var sum float64
				for b.Loop() {
					for value := range decoder.All(data, count) {
						sum += value
					}
				}

				benchmarkFloatSink = sum
			})

			b.Run("Chimp", func(b *testing.B) {
				decoder := NewNumericChimpDecoder()
				data := dataset.encoded

				b.ReportAllocs()
				b.ResetTimer()

				var sum float64
				for b.Loop() {
					for value := range decoder.All(data, count) {
						sum += value
					}
				}

				benchmarkFloatSink = sum
			})
		})
	}
}

// BenchmarkChimpVsGorilla_DecodeAt compares random access (At) speed.
func BenchmarkChimpVsGorilla_DecodeAt(b *testing.B) {
	for _, dataset := range chimpBenchDatasets {
		if len(dataset.indexes) == 0 {
			continue
		}

		b.Run(dataset.name, func(b *testing.B) {
			gorillaDS := gorillaBenchDatasets[findDatasetIndex(gorillaBenchDatasets, dataset.name)]
			count := len(dataset.values)
			indexes := dataset.indexes

			b.Run("Gorilla", func(b *testing.B) {
				decoder := NewNumericGorillaDecoder()
				data := gorillaDS.encoded

				b.ReportAllocs()
				b.ResetTimer()

				var sum float64
				for b.Loop() {
					for _, idx := range indexes {
						value, _ := decoder.At(data, idx, count)
						sum += value
					}
				}

				benchmarkFloatSink = sum
			})

			b.Run("Chimp", func(b *testing.B) {
				decoder := NewNumericChimpDecoder()
				data := dataset.encoded

				b.ReportAllocs()
				b.ResetTimer()

				var sum float64
				for b.Loop() {
					for _, idx := range indexes {
						value, _ := decoder.At(data, idx, count)
						sum += value
					}
				}

				benchmarkFloatSink = sum
			})
		})
	}
}

// BenchmarkChimpVsGorilla_EncodedSize reports encoded size comparison (not a speed benchmark).
func BenchmarkChimpVsGorilla_EncodedSize(b *testing.B) {
	for _, dataset := range chimpBenchDatasets {
		b.Run(dataset.name, func(b *testing.B) {
			gorillaDS := gorillaBenchDatasets[findDatasetIndex(gorillaBenchDatasets, dataset.name)]

			rawSize := len(dataset.values) * 8
			gorillaSize := len(gorillaDS.encoded)
			chimpSize := len(dataset.encoded)

			b.ResetTimer()
			for b.Loop() {
				// no-op: we just report metrics
			}

			b.ReportMetric(float64(rawSize), "raw-bytes")
			b.ReportMetric(float64(gorillaSize), "gorilla-bytes")
			b.ReportMetric(float64(chimpSize), "chimp-bytes")
			b.ReportMetric(float64(gorillaSize-chimpSize), "saved-bytes")

			savingPct := 0.0
			if gorillaSize > 0 {
				savingPct = float64(gorillaSize-chimpSize) * 100 / float64(gorillaSize)
			}

			b.ReportMetric(savingPct, "chimp-saving-%")
		})
	}
}

func findDatasetIndex[T interface{ getName() string }](datasets []T, name string) int {
	for i, ds := range datasets {
		if ds.getName() == name {
			return i
		}
	}

	panic(fmt.Sprintf("dataset %q not found", name))
}

func (d gorillaBenchDataset) getName() string { return d.name }
func (d chimpBenchDataset) getName() string   { return d.name }
