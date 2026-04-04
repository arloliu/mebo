package encoding

import (
	"math"
	"slices"
	"testing"
)

type gorillaBenchDataset struct {
	name    string
	values  []float64
	encoded []byte
	indexes []int
}

var (
	gorillaBenchDatasets = []gorillaBenchDataset{
		buildGorillaDataset("cpu_util_150", generateSmallJitterMetric(150, 42.0, 0.003, 0.001)),
		buildGorillaDataset("mem_usage_500", generateDriftingMetric(500, 67.2, 0.0006, 0.002)),
		buildGorillaDataset("temperature_200", generateSmallJitterMetric(200, 23.4, 0.0005, 0.0002)),
		buildGorillaDataset("net_throughput_300", generateSmallJitterMetric(300, 950.0, 0.005, 0.002)),
		buildGorillaDataset("latency_1000", generateStickyMetric(1000, 12.5, 0.04, 0.15)),
		buildGorillaDataset("disk_iops_500", generateIntegerLikeMetric(500, 4200.0, 50.0, 0.1)),
		buildGorillaDataset("battery_volt_100", generateSmallJitterMetric(100, 3.72, 0.0003, 0.0001)),
		buildGorillaDataset("request_rate_800", generateStepDriftMetric(800, 1500.0, 10.0, 0.3)),
	}
	benchmarkFloatSink float64
)

func BenchmarkNumericGorillaDecoderAll(b *testing.B) {
	for _, dataset := range gorillaBenchDatasets {
		b.Run(dataset.name, func(b *testing.B) {
			decoder := NewNumericGorillaDecoder()
			data := dataset.encoded
			expected := len(dataset.values)

			b.ReportAllocs()
			b.ResetTimer()

			var sum float64
			for b.Loop() {
				for value := range decoder.All(data, expected) {
					sum += value
				}
			}

			benchmarkFloatSink = sum
		})
	}
}

func BenchmarkNumericGorillaDecoderAt(b *testing.B) {
	for _, dataset := range gorillaBenchDatasets {
		if len(dataset.indexes) == 0 {
			continue
		}

		b.Run(dataset.name, func(b *testing.B) {
			decoder := NewNumericGorillaDecoder()
			data := dataset.encoded
			count := len(dataset.values)
			indexes := dataset.indexes

			b.ReportAllocs()
			b.ResetTimer()

			var sum float64
			for b.Loop() {
				for _, idx := range indexes {
					value, ok := decoder.At(data, idx, count)
					if !ok {
						b.Fatalf("failed to decode index %d", idx)
					}
					sum += value
				}
			}

			benchmarkFloatSink = sum
		})
	}
}

func buildGorillaDataset(name string, values []float64) gorillaBenchDataset {
	encoder := NewNumericGorillaEncoder()
	encoder.WriteSlice(values)
	encoded := slices.Clone(encoder.Bytes())
	encoder.Finish()

	return gorillaBenchDataset{
		name:    name,
		values:  slices.Clone(values),
		encoded: encoded,
		indexes: makeAccessPattern(len(values)),
	}
}

func generateSmallJitterMetric(count int, base, driftRate, noiseRate float64) []float64 {
	values := make([]float64, count)
	for i := range values {
		drift := math.Sin(float64(i)*0.07) * base * driftRate
		noise := math.Cos(float64(i)*0.23) * base * noiseRate
		values[i] = base + drift + noise
	}

	return values
}

func generateDriftingMetric(count int, base, slope, noiseRate float64) []float64 {
	values := make([]float64, count)
	for i := range values {
		trend := float64(i) * slope
		noise := math.Sin(float64(i)*0.13) * base * noiseRate
		values[i] = base + trend + noise
	}

	return values
}

func generateStickyMetric(count int, base, changeProb, changeScale float64) []float64 {
	values := make([]float64, count)
	current := base

	for i := range values {
		trigger := math.Sin(float64(i)*1.7 + 3.1)
		if trigger > (1.0 - changeProb*2) {
			delta := math.Sin(float64(i)*2.3+0.7) * changeScale
			current = base + delta
		}

		values[i] = current
	}

	return values
}

func generateIntegerLikeMetric(count int, base, rangeVal float64, driftRate float64) []float64 {
	values := make([]float64, count)
	for i := range values {
		drift := float64(i) * driftRate
		variation := math.Round(math.Sin(float64(i)*0.19) * rangeVal)
		values[i] = math.Round(base + drift + variation)
	}

	return values
}

func generateStepDriftMetric(count int, base, stepSize float64, stepProb float64) []float64 {
	values := make([]float64, count)
	level := base

	for i := range values {
		trigger := math.Sin(float64(i)*1.3 + 2.7)
		if trigger > (1.0 - stepProb*2) {
			step := math.Sin(float64(i)*0.9+1.1) * stepSize
			level = base + step
		}

		noise := math.Sin(float64(i)*0.31) * stepSize * 0.05
		values[i] = level + noise
	}

	return values
}

func makeAccessPattern(length int) []int {
	if length <= 0 {
		return nil
	}

	maxProbe := length
	if maxProbe > 32 {
		maxProbe = 32
	}

	pattern := make([]int, 0, maxProbe)
	pattern = append(pattern, 0)

	if length > 1 {
		pattern = append(pattern, length-1)
	}

	if length > 2 {
		mid := length / 2
		if mid != 0 && mid != length-1 {
			pattern = append(pattern, mid)
		}
	}

	step := maxInt(1, length/maxProbe)
	for i := step; i < length-1 && len(pattern) < cap(pattern); i += step {
		if i == length-1 {
			break
		}

		if !containsIndex(pattern, i) {
			pattern = append(pattern, i)
		}
	}

	return pattern
}

func containsIndex(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}

	return b
}
