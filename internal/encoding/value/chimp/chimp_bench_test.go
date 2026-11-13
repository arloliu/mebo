package chimp

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"

	"github.com/arloliu/mebo/internal/encoding/value/gorilla"
)

const (
	chimpMixSetColumns = 100     // columns per set; every operation processes one set
	chimpMixFreshMin   = 960_000 // distinct points in the fresh data, beyond what a branch predictor memorizes
)

type chimpBenchDataset struct {
	name    string
	values  []float64
	encoded []byte
	indexes []int
}

type gorillaBenchDataset struct {
	name    string
	values  []float64
	encoded []byte
	indexes []int
}

// Real-world inspired datasets with small value variety typical of monitoring/IoT metrics.
type chimpMixSet struct {
	cols    [][]float64
	encoded [][]byte
}

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

var gorillaBenchDatasets = []gorillaBenchDataset{
	buildGorillaDataset("cpu_util_150", generateSmallJitterMetric(150, 42.0, 0.003, 0.001)),
	buildGorillaDataset("mem_usage_500", generateDriftingMetric(500, 67.2, 0.0006, 0.002)),
	buildGorillaDataset("temperature_200", generateSmallJitterMetric(200, 23.4, 0.0005, 0.0002)),
	buildGorillaDataset("net_throughput_300", generateSmallJitterMetric(300, 950.0, 0.005, 0.002)),
	buildGorillaDataset("latency_1000", generateStickyMetric(1000, 12.5, 0.04, 0.15)),
	buildGorillaDataset("disk_iops_500", generateIntegerLikeMetric(500, 4200.0, 50.0, 0.1)),
	buildGorillaDataset("battery_volt_100", generateSmallJitterMetric(100, 3.72, 0.0003, 0.0001)),
	buildGorillaDataset("request_rate_800", generateStepDriftMetric(800, 1500.0, 10.0, 0.3)),
}

var benchmarkFloatSink float64

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

func buildGorillaDataset(name string, values []float64) gorillaBenchDataset {
	encoder := gorilla.NewNumericGorillaEncoder()
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

// BenchmarkChimpVsGorilla_Encode compares encoding speed between Chimp and Gorilla.
func BenchmarkChimpVsGorilla_Encode(b *testing.B) {
	for _, dataset := range chimpBenchDatasets {
		b.Run(dataset.name, func(b *testing.B) {
			values := dataset.values

			b.Run("Gorilla", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for b.Loop() {
					enc := gorilla.NewNumericGorillaEncoder()
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
				decoder := gorilla.NewNumericGorillaDecoder()
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
				decoder := gorilla.NewNumericGorillaDecoder()
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

	panic("dataset not found: " + name)
}

func (d gorillaBenchDataset) getName() string { return d.name }
func (d chimpBenchDataset) getName() string   { return d.name }

func BenchmarkNumericChimpDecoder_DecodeAll(b *testing.B) {
	for _, dataset := range chimpBenchDatasets {
		b.Run(dataset.name, func(b *testing.B) {
			decoder := NewNumericChimpDecoder()
			data := dataset.encoded
			count := len(dataset.values)
			dst := make([]float64, count)

			b.ReportAllocs()
			if count > 0 {
				b.SetBytes(int64(count * 8))
			}
			b.ResetTimer()

			for b.Loop() {
				produced := decoder.DecodeAll(data, count, dst)
				if produced != count {
					b.Fatalf("expected %d values, got %d", count, produced)
				}
			}
		})
	}
}

func BenchmarkNumericChimpDecoder_AllVsDecodeAll(b *testing.B) {
	for _, dataset := range chimpBenchDatasets {
		b.Run(dataset.name, func(b *testing.B) {
			decoder := NewNumericChimpDecoder()
			data := dataset.encoded
			count := len(dataset.values)

			b.Run("All_Iterator", func(b *testing.B) {
				b.ReportAllocs()
				var sum float64
				for b.Loop() {
					for value := range decoder.All(data, count) {
						sum += value
					}
				}
				benchmarkFloatSink = sum
			})

			b.Run("DecodeAll_Slice", func(b *testing.B) {
				dst := make([]float64, count)
				b.ReportAllocs()
				for b.Loop() {
					decoder.DecodeAll(data, count, dst)
				}
				benchmarkFloatSink = dst[count-1]
			})
		})
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
		if trigger > 1.0-changeProb*2 {
			delta := math.Sin(float64(i)*2.3+0.7) * changeScale
			current = base + delta
		}
		values[i] = current
	}

	return values
}

func generateIntegerLikeMetric(count int, base, rangeVal, driftRate float64) []float64 {
	values := make([]float64, count)
	for i := range values {
		drift := float64(i) * driftRate
		variation := math.Round(math.Sin(float64(i)*0.19) * rangeVal)
		values[i] = math.Round(base + drift + variation)
	}

	return values
}

func generateStepDriftMetric(count int, base, stepSize, stepProb float64) []float64 {
	values := make([]float64, count)
	level := base
	for i := range values {
		trigger := math.Sin(float64(i)*1.3 + 2.7)
		if trigger > 1.0-stepProb*2 {
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

	maxProbe := min(length, 32)
	pattern := make([]int, 0, maxProbe)
	pattern = append(pattern, 0)
	if length > 1 {
		pattern = append(pattern, length-1)
	}
	if length > 2 {
		middle := length / 2
		if middle != 0 && middle != length-1 {
			pattern = append(pattern, middle)
		}
	}

	step := maxInt(1, length/maxProbe)
	for index := step; index < length-1 && len(pattern) < cap(pattern); index += step {
		if !slices.Contains(pattern, index) {
			pattern = append(pattern, index)
		}
	}

	return pattern
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}

	return right
}

func generateJitteredValues(count int, baseValue, deltaPercent float64, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	values := make([]float64, count)
	currentValue := baseValue
	for i := range count {
		deltaRange := currentValue * deltaPercent
		delta := (rng.Float64()*2 - 1) * deltaRange
		currentValue += delta
		values[i] = currentValue
	}

	return values
}

// generateChimpMixColumn builds one column of a monitoring-style mix:
// 35% two-decimal gauge, 20% integer counter, 18% sparse and 27% full-precision gauge (about 3.7 bytes per point in Chimp).
func generateChimpMixColumn(slot, n int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	cur := 100.0 + float64(slot)*10.0
	col := make([]float64, n)

	for j := range col {
		switch {
		case slot < 35: // two-decimal gauge, repeats the previous value 30% of the time
			if j > 0 && rng.Float64() < 0.3 {
				col[j] = col[j-1]
				continue
			}
			cur += cur * (rng.Float64()*2 - 1) * 0.005
			col[j] = math.Round(cur*100) / 100
		case slot < 55: // integer counter
			cur += math.Floor(rng.Float64()*10) + 1
			col[j] = cur
		case slot < 73: // sparse: constant with a 5% chance of a small step
			if rng.Float64() < 0.05 {
				cur += rng.Float64()*2 - 1
			}
			col[j] = math.Round(cur*100) / 100
		default: // full-precision gauge
			cur += cur * (rng.Float64()*2 - 1) * 0.005
			col[j] = cur
		}
	}

	return col
}

// buildChimpMixSets returns sets of 100 columns, each column from its own seed.
func buildChimpMixSets(sets, n int) []chimpMixSet {
	out := make([]chimpMixSet, sets)
	enc := NewNumericChimpEncoder()
	defer enc.Finish()

	for s := range out {
		out[s] = chimpMixSet{
			cols:    make([][]float64, chimpMixSetColumns),
			encoded: make([][]byte, chimpMixSetColumns),
		}
		for c := range chimpMixSetColumns {
			col := generateChimpMixColumn(c, n, int64(s*chimpMixSetColumns+c))
			enc.WriteSlice(col)
			out[s].cols[c] = col
			out[s].encoded[c] = slices.Clone(enc.Bytes())
			enc.Reset()
			enc.buf.B = enc.buf.B[:0]
		}
	}

	return out
}

// BenchmarkChimpMix measures Chimp on a realistic value mix at two data sizes.
// "repeated" loops over one set of 100 columns, small enough for a branch predictor to learn its flag sequence.
// "fresh" cycles through at least 960,000 distinct points, too many to learn; its working set is also larger.
// Both process 100 columns per operation, so their ns/col figures compare directly,
// but the gap between them is a workload difference: attribute it to branch or cache misses only with hardware counters.
func BenchmarkChimpMix(b *testing.B) {
	for _, size := range []string{"repeated", "fresh"} {
		for _, n := range []int{30, 150} {
			b.Run(fmt.Sprintf("%s/n%d", size, n), func(b *testing.B) {
				sets := 1
				if size == "fresh" {
					sets = (chimpMixFreshMin + chimpMixSetColumns*n - 1) / (chimpMixSetColumns * n)
				}
				data := buildChimpMixSets(sets, n)

				// run cycles through the sets, one set per operation.
				run := func(b *testing.B, op func(set *chimpMixSet)) {
					b.ReportAllocs()
					b.ResetTimer()

					next := 0
					for b.Loop() {
						op(&data[next])
						if next++; next == len(data) {
							next = 0
						}
					}

					b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/chimpMixSetColumns, "ns/col")
				}

				enc := NewNumericChimpEncoder()
				defer enc.Finish()
				enc.buf.Grow(chimpMixSetColumns*n*10 + 64)
				dec := NewNumericChimpDecoder()
				dst := make([]float64, n)
				var sum float64

				b.Run("WriteSlice", func(b *testing.B) {
					run(b, func(set *chimpMixSet) {
						for _, col := range set.cols {
							enc.WriteSlice(col)
							enc.flushBits()
							enc.Reset()
						}
						enc.buf.B = enc.buf.B[:0]
					})
				})
				b.Run("Write", func(b *testing.B) {
					run(b, func(set *chimpMixSet) {
						for _, col := range set.cols {
							for _, v := range col {
								enc.Write(v)
							}
							enc.flushBits()
							enc.Reset()
						}
						enc.buf.B = enc.buf.B[:0]
					})
				})
				b.Run("DecodeAll", func(b *testing.B) {
					run(b, func(set *chimpMixSet) {
						for _, data := range set.encoded {
							dec.DecodeAll(data, n, dst)
							sum += dst[n-1]
						}
					})
				})
				b.Run("All", func(b *testing.B) {
					run(b, func(set *chimpMixSet) {
						for _, data := range set.encoded {
							for v := range dec.All(data, n) {
								sum += v
							}
						}
					})
				})
				b.Run("AtLast", func(b *testing.B) {
					run(b, func(set *chimpMixSet) {
						for _, data := range set.encoded {
							v, _ := dec.At(data, n-1, n)
							sum += v
						}
					})
				})

				benchmarkFloatSink = sum
			})
		}
	}
}
