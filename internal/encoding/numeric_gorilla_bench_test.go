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
		buildGorillaDataset("steady_10", generateLinearValues(10, 20.5, 0.05)),
		buildGorillaDataset("seasonal_150", generateSeasonalValues(150)),
		buildGorillaDataset("repeated_runs_1000", generateRepeatedRunValues(1000)),
		buildGorillaDataset("alternating_256", generateAlternatingValues(256, 48.0, 0.75)),
		buildGorillaDataset("alternating_bursts_512", generateAlternatingBurstValues(512)),
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

func generateLinearValues(count int, start float64, delta float64) []float64 {
	values := make([]float64, count)
	current := start
	for i := range values {
		values[i] = current
		current += delta
	}

	return values
}

func generateSeasonalValues(count int) []float64 {
	values := make([]float64, count)

	for i := range values {
		trend := float64(i) * 0.01
		seasonal := math.Sin(float64(i)*0.05) * 2.5
		microCycle := math.Sin(float64(i)*0.0035) * 0.3
		values[i] = 50.0 + trend + seasonal + microCycle
	}

	return values
}

func generateRepeatedRunValues(count int) []float64 {
	values := make([]float64, count)

	base := 70.0
	i := 0
	for i < count {
		runLength := minInt(3+(i/40)%6, count-i)
		value := base + math.Sin(float64(i)*0.12)*1.8

		for j := 0; j < runLength; j++ {
			values[i+j] = value
		}

		base += 0.02
		i += runLength
	}

	return values
}

func generateAlternatingValues(count int, base float64, delta float64) []float64 {
	values := make([]float64, count)

	trend := 0.0
	for i := range values {
		if i%2 == 0 {
			values[i] = base + delta + trend
		} else {
			values[i] = base - delta + trend
		}

		// introduce a periodic larger change every 16 points to force new blocks
		if i%16 == 15 {
			trend += delta * 0.25
		} else {
			trend += delta * 0.01
		}
	}

	return values
}

func generateAlternatingBurstValues(count int) []float64 {
	values := make([]float64, count)

	base := 55.0
	delta := 1.5
	index := 0

	for index < count {
		burstLen := minInt(32, count-index)
		for i := 0; i < burstLen && index < count; i++ {
			if i%2 == 0 {
				values[index] = base + delta + math.Sin(float64(index)*0.04)*0.2
			} else {
				values[index] = base - delta + math.Cos(float64(index)*0.02)*0.15
			}
			index++
		}

		steadyLen := minInt(48, count-index)
		if steadyLen == 0 {
			break
		}

		steadyValue := base + math.Sin(float64(index)*0.015)*0.4
		for i := 0; i < steadyLen && index < count; i++ {
			values[index] = steadyValue + float64(i)*0.03
			index++
		}

		base += 0.35
		delta *= 0.97
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

func minInt(a int, b int) int {
	if a < b {
		return a
	}

	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}

	return b
}
