package blob

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
)

// ==============================================================================
// Helper Functions for Benchmarks
// ==============================================================================

func createAtBenchBlob(tb testing.TB, startTime time.Time, metricName string, timestamps []int64, values []float64) NumericBlob {
	tb.Helper()

	metricID := hash.ID(metricName)

	// Create encoder
	encoder, err := NewNumericEncoder(startTime)
	if err != nil {
		tb.Fatalf("Failed to create encoder: %v", err)
	}

	// Encode data
	err = encoder.StartMetricID(metricID, len(timestamps))
	if err != nil {
		tb.Fatalf("Failed to start metric: %v", err)
	}

	for i, ts := range timestamps {
		err = encoder.AddDataPoint(ts, values[i], "")
		if err != nil {
			tb.Fatalf("Failed to write data: %v", err)
		}
	}

	err = encoder.EndMetric()
	if err != nil {
		tb.Fatalf("Failed to end metric: %v", err)
	}

	data, err := encoder.Finish()
	if err != nil {
		tb.Fatalf("Failed to finish: %v", err)
	}

	// Decode
	decoder, err := NewNumericDecoder(data)
	if err != nil {
		tb.Fatalf("Failed to create decoder: %v", err)
	}

	blob, err := decoder.Decode()
	if err != nil {
		tb.Fatalf("Failed to decode: %v", err)
	}

	return blob
}

// ==============================================================================
// Benchmarks
// ==============================================================================

func BenchmarkNumericBlob_ValueAt(b *testing.B) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Create blob with 100 data points
	timestamps := make([]int64, 100)
	values := make([]float64, 100)
	for i := range 100 {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Minute).UnixMicro()
		values[i] = float64(i)
	}

	blob := createAtBenchBlob(b, startTime, metricName, timestamps, values)

	b.Run("FirstIndex", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blob.ValueAt(metricID, 0)
		}
	})

	b.Run("MiddleIndex", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blob.ValueAt(metricID, 50)
		}
	})

	b.Run("LastIndex", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blob.ValueAt(metricID, 99)
		}
	})
}

func BenchmarkNumericBlob_TimestampAt(b *testing.B) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric2"
	metricID := hash.ID(metricName)

	// Create blob with 100 data points
	timestamps := make([]int64, 100)
	values := make([]float64, 100)
	for i := range 100 {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Minute).UnixMicro()
		values[i] = float64(i)
	}

	blob := createAtBenchBlob(b, startTime, metricName, timestamps, values)

	b.Run("FirstIndex", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blob.TimestampAt(metricID, 0)
		}
	})

	b.Run("MiddleIndex", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blob.TimestampAt(metricID, 50)
		}
	})

	b.Run("LastIndex", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blob.TimestampAt(metricID, 99)
		}
	})
}

func BenchmarkNumericBlobSet_ValueAt(b *testing.B) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Create 50 blobs with 10 points each = 500 total points
	blobs := make([]NumericBlob, 50)
	for i := range 50 {
		blobStartTime := startTime.Add(time.Duration(i) * time.Hour)
		timestamps := make([]int64, 10)
		values := make([]float64, 10)
		for j := range 10 {
			timestamps[j] = blobStartTime.Add(time.Duration(j) * time.Minute).UnixMicro()
			values[j] = float64(i*10 + j)
		}
		blobs[i] = createAtBenchBlob(b, blobStartTime, metricName, timestamps, values)
	}

	blobSet, err := NewNumericBlobSet(blobs)
	if err != nil {
		b.Fatalf("Failed to create blob set: %v", err)
	}

	b.Run("FirstBlob_FirstIndex", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blobSet.ValueAt(metricID, 0)
		}
	})

	b.Run("MiddleBlob_MiddleIndex", func(b *testing.B) {
		// Index 250 = blob 25, local index 0
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blobSet.ValueAt(metricID, 250)
		}
	})

	b.Run("LastBlob_LastIndex", func(b *testing.B) {
		// Index 499 = blob 49, local index 9
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blobSet.ValueAt(metricID, 499)
		}
	})

	b.Run("BlobBoundary", func(b *testing.B) {
		// Index 9 = last of first blob
		// Index 10 = first of second blob
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blobSet.ValueAt(metricID, 9)
			_, _ = blobSet.ValueAt(metricID, 10)
		}
	})
}

func BenchmarkNumericBlobSet_TimestampAt(b *testing.B) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Create 50 blobs with 10 points each = 500 total points
	blobs := make([]NumericBlob, 50)
	for i := range 50 {
		blobStartTime := startTime.Add(time.Duration(i) * time.Hour)
		timestamps := make([]int64, 10)
		values := make([]float64, 10)
		for j := range 10 {
			timestamps[j] = blobStartTime.Add(time.Duration(j) * time.Minute).UnixMicro()
			values[j] = float64(i*10 + j)
		}
		blobs[i] = createAtBenchBlob(b, blobStartTime, metricName, timestamps, values)
	}

	blobSet, err := NewNumericBlobSet(blobs)
	if err != nil {
		b.Fatalf("Failed to create blob set: %v", err)
	}

	b.Run("FirstBlob_FirstIndex", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blobSet.TimestampAt(metricID, 0)
		}
	})

	b.Run("MiddleBlob_MiddleIndex", func(b *testing.B) {
		// Index 250 = blob 25, local index 0
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blobSet.TimestampAt(metricID, 250)
		}
	})

	b.Run("LastBlob_LastIndex", func(b *testing.B) {
		// Index 499 = blob 49, local index 9
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blobSet.TimestampAt(metricID, 499)
		}
	})

	b.Run("BlobBoundary", func(b *testing.B) {
		// Index 9 = last of first blob
		// Index 10 = first of second blob
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = blobSet.TimestampAt(metricID, 9)
			_, _ = blobSet.TimestampAt(metricID, 10)
		}
	})
}

func BenchmarkValueAt_vs_Iteration(b *testing.B) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Create 50 blobs with 10 points each
	blobs := make([]NumericBlob, 50)
	for i := range 50 {
		blobStartTime := startTime.Add(time.Duration(i) * time.Hour)
		timestamps := make([]int64, 10)
		values := make([]float64, 10)
		for j := range 10 {
			timestamps[j] = blobStartTime.Add(time.Duration(j) * time.Minute).UnixMicro()
			values[j] = float64(i*10 + j)
		}
		blobs[i] = createAtBenchBlob(b, blobStartTime, metricName, timestamps, values)
	}

	blobSet, _ := NewNumericBlobSet(blobs)

	b.Run("ValueAt_10_RandomAccess", func(b *testing.B) {
		indices := []int{5, 100, 200, 300, 400, 50, 150, 250, 350, 450}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			for _, idx := range indices {
				_, _ = blobSet.ValueAt(metricID, idx)
			}
		}
	})

	b.Run("Iteration_All", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			count := 0
			for range blobSet.AllValues(metricID) {
				count++
			}
		}
	})

	b.Run("Iteration_First10", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			count := 0
			for range blobSet.AllValues(metricID) {
				count++
				if count >= 10 {
					break
				}
			}
		}
	})
}

func BenchmarkNumericBlob_All(b *testing.B) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Create test data with 100 points
	timestamps := make([]int64, 100)
	values := make([]float64, 100)
	for i := range 100 {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Minute).UnixMicro()
		values[i] = float64(i) * 1.5
	}

	// Encode data
	encoder, _ := NewNumericEncoder(startTime)
	_ = encoder.StartMetricID(metricID, len(timestamps))
	for i := range timestamps {
		_ = encoder.AddDataPoint(timestamps[i], values[i], "")
	}
	_ = encoder.EndMetric()
	data, _ := encoder.Finish()

	// Decode once
	decoder, _ := NewNumericDecoder(data)
	blob, _ := decoder.Decode()

	b.Run("All", func(b *testing.B) {
		for b.Loop() {
			for ts, val := range blob.All(metricID) {
				_ = ts  // Consume timestamp
				_ = val // Consume value
			}
		}
	})

	b.Run("AllTimestamps+AllValues", func(b *testing.B) {
		for b.Loop() {
			for ts := range blob.AllTimestamps(metricID) {
				_ = ts // Consume timestamp
			}
			for val := range blob.AllValues(metricID) {
				_ = val // Consume value
			}
		}
	})
}

// BenchmarkNumericBlob_All_EncodingCombinations benchmarks all encoding combinations
// to verify performance improvements from optimized paths.
func BenchmarkNumericBlob_All_EncodingCombinations(b *testing.B) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	metricName := "test.metric"
	metricID := hash.ID(metricName)

	// Test different data sizes
	sizes := []struct {
		name  string
		count int
	}{
		{"10pts", 10},
		{"100pts", 100},
		{"1000pts", 1000},
	}

	// Test all encoding combinations
	encodings := []struct {
		name   string
		tsEnc  NumericEncoderOption
		valEnc NumericEncoderOption
	}{
		{
			name:   "RawRaw",
			tsEnc:  WithTimestampEncoding(format.TypeRaw),
			valEnc: WithValueEncoding(format.TypeRaw),
		},
		{
			name:   "RawGorilla",
			tsEnc:  WithTimestampEncoding(format.TypeRaw),
			valEnc: WithValueEncoding(format.TypeGorilla),
		},
		{
			name:   "DeltaRaw",
			tsEnc:  WithTimestampEncoding(format.TypeDelta),
			valEnc: WithValueEncoding(format.TypeRaw),
		},
		{
			name:   "DeltaGorilla",
			tsEnc:  WithTimestampEncoding(format.TypeDelta),
			valEnc: WithValueEncoding(format.TypeGorilla),
		},
	}

	for _, enc := range encodings {
		for _, size := range sizes {
			b.Run(enc.name+"/"+size.name, func(b *testing.B) {
				// Generate test data
				timestamps := make([]int64, size.count)
				values := make([]float64, size.count)
				baseTime := startTime.UnixMicro()
				for i := range size.count {
					timestamps[i] = baseTime + int64(i)*60*1000000 // 1 minute intervals
					values[i] = 100.0 + float64(i)*0.1             // Slowly increasing values (good for Gorilla)
				}

				// Create encoder with specific encoding
				encoder, err := NewNumericEncoder(startTime, enc.tsEnc, enc.valEnc)
				if err != nil {
					b.Fatalf("Failed to create encoder: %v", err)
				}

				// Encode data
				err = encoder.StartMetricID(metricID, len(timestamps))
				if err != nil {
					b.Fatalf("Failed to start metric: %v", err)
				}

				for i := range timestamps {
					err = encoder.AddDataPoint(timestamps[i], values[i], "")
					if err != nil {
						b.Fatalf("Failed to add data point: %v", err)
					}
				}

				err = encoder.EndMetric()
				if err != nil {
					b.Fatalf("Failed to end metric: %v", err)
				}

				data, err := encoder.Finish()
				if err != nil {
					b.Fatalf("Failed to finish: %v", err)
				}

				// Decode
				decoder, err := NewNumericDecoder(data)
				if err != nil {
					b.Fatalf("Failed to create decoder: %v", err)
				}

				blob, err := decoder.Decode()
				if err != nil {
					b.Fatalf("Failed to decode: %v", err)
				}

				// Benchmark All() iteration
				b.ReportAllocs()
				b.ResetTimer()

				for b.Loop() {
					count := 0
					for _, dp := range blob.All(metricID) {
						_ = dp.Ts
						_ = dp.Val
						count++
					}
					if count != size.count {
						b.Fatalf("wrong count: got %d, want %d", count, size.count)
					}
				}
			})
		}
	}
}

// ==============================================================================
// V1 vs V2 vs V2+SharedTimestamps Layout Benchmarks
// ==============================================================================

// benchBlob holds a labeled decoded blob for config-driven benchmarks.
type benchBlob struct {
	label string
	blob  NumericBlob
}

// benchBlobSet holds a labeled blob set for config-driven benchmarks.
type benchBlobSet struct {
	label string
	set   NumericBlobSet
}

// benchRawBlobs holds labeled raw encoded blobs for config-driven benchmarks.
type benchRawBlobs struct {
	label string
	raw   [][]byte
}

// layoutConfigs returns the three layout configurations to benchmark.
func layoutConfigs() []struct {
	label string
	opts  []NumericEncoderOption
} {
	return []struct {
		label string
		opts  []NumericEncoderOption
	}{
		{"V1_Default", nil},
		{"V2_Sorted", []NumericEncoderOption{WithBlobLayoutV2()}},
		{"V2_SharedTS", []NumericEncoderOption{WithSharedTimestamps()}},
	}
}

// createLayoutBenchBlobs creates V1, V2, and V2+SharedTimestamps decoded blobs.
// MetricIDs are inserted in reverse order to stress the V2 sorting path
// and create worst-case map iteration patterns for V1.
func createLayoutBenchBlobs(tb testing.TB, metricCount, pointsPerMetric int) []benchBlob {
	tb.Helper()

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	configs := layoutConfigs()
	result := make([]benchBlob, len(configs))

	for ci, cfg := range configs {
		opts := append([]NumericEncoderOption{
			WithTimestampEncoding(format.TypeDelta),
			WithValueEncoding(format.TypeGorilla),
		}, cfg.opts...)

		enc, err := NewNumericEncoder(startTime, opts...)
		if err != nil {
			tb.Fatalf("%s: failed to create encoder: %v", cfg.label, err)
		}

		// Insert metrics in reverse ID order to exercise sort path
		for i := metricCount; i >= 1; i-- {
			if err = enc.StartMetricID(uint64(i), pointsPerMetric); err != nil {
				tb.Fatalf("%s: StartMetricID(%d): %v", cfg.label, i, err)
			}
			for j := range pointsPerMetric {
				ts := startTime.Add(time.Duration(j) * time.Second).UnixMicro()
				if err = enc.AddDataPoint(ts, float64(i*1000+j), ""); err != nil {
					tb.Fatalf("%s: AddDataPoint: %v", cfg.label, err)
				}
			}
			if err = enc.EndMetric(); err != nil {
				tb.Fatalf("%s: EndMetric: %v", cfg.label, err)
			}
		}

		data, err := enc.Finish()
		if err != nil {
			tb.Fatalf("%s: Finish: %v", cfg.label, err)
		}

		dec, err := NewNumericDecoder(data)
		if err != nil {
			tb.Fatalf("%s: NewNumericDecoder: %v", cfg.label, err)
		}

		blob, err := dec.Decode()
		if err != nil {
			tb.Fatalf("%s: Decode: %v", cfg.label, err)
		}

		result[ci] = benchBlob{label: cfg.label, blob: blob}
	}

	return result
}

// BenchmarkV2Layout_HasMetricID compares binary search (V2) vs map lookup (V1).
func BenchmarkV2Layout_HasMetricID(b *testing.B) {
	sizes := []struct {
		name    string
		metrics int
		points  int
	}{
		{"10metrics_10points", 10, 10},
		{"150metrics_10points", 150, 10},
		{"500metrics_10points", 500, 10},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			blobs := createLayoutBenchBlobs(b, sz.metrics, sz.points)

			// Build a shuffled lookup sequence to avoid branch predictor bias
			rng := rand.New(rand.NewPCG(42, 0))
			lookupIDs := make([]uint64, sz.metrics*2) // 50% hit, 50% miss
			for i := range sz.metrics {
				lookupIDs[i] = uint64(i + 1)                                        // hit
				lookupIDs[sz.metrics+i] = hash.ID("nonexistent_" + string(rune(i))) // miss
			}
			rng.Shuffle(len(lookupIDs), func(i, j int) {
				lookupIDs[i], lookupIDs[j] = lookupIDs[j], lookupIDs[i]
			})

			for _, nb := range blobs {
				b.Run(nb.label, func(b *testing.B) {
					blob := nb.blob
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						for _, id := range lookupIDs {
							_ = blob.HasMetricID(id)
						}
					}
				})
			}
		})
	}
}

// BenchmarkV2Layout_GetByID compares V1 map vs V2 binary search for entry retrieval.
// Uses Len() which delegates to indexMaps.GetByID internally.
func BenchmarkV2Layout_GetByID(b *testing.B) {
	const metricCount = 150
	const pointsPerMetric = 10

	blobs := createLayoutBenchBlobs(b, metricCount, pointsPerMetric)

	// Build lookup sequence: known hits only
	ids := make([]uint64, metricCount)
	for i := range metricCount {
		ids[i] = uint64(i + 1)
	}
	rng := rand.New(rand.NewPCG(99, 0))
	rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })

	for _, nb := range blobs {
		b.Run(nb.label, func(b *testing.B) {
			blob := nb.blob
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for _, id := range ids {
					_ = blob.Len(id)
				}
			}
		})
	}
}

// BenchmarkV2Layout_AllIteration compares sequential iteration of all data points.
func BenchmarkV2Layout_AllIteration(b *testing.B) {
	sizes := []struct {
		name    string
		metrics int
		points  int
	}{
		{"150metrics_10points", 150, 10},
		{"150metrics_100points", 150, 100},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			blobs := createLayoutBenchBlobs(b, sz.metrics, sz.points)
			ids := blobs[0].blob.MetricIDs()

			for _, nb := range blobs {
				b.Run(nb.label, func(b *testing.B) {
					blob := nb.blob
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						total := 0
						for _, id := range ids {
							for _, dp := range blob.All(id) {
								_ = dp.Val
								total++
							}
						}
						if total != sz.metrics*sz.points {
							b.Fatalf("got %d points, want %d", total, sz.metrics*sz.points)
						}
					}
				})
			}
		})
	}
}

// BenchmarkV2Layout_Materialize compares materialization on V1 vs V2 blobs.
func BenchmarkV2Layout_Materialize(b *testing.B) {
	sizes := []struct {
		name    string
		metrics int
		points  int
	}{
		{"150metrics_10points", 150, 10},
		{"150metrics_100points", 150, 100},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			blobs := createLayoutBenchBlobs(b, sz.metrics, sz.points)

			for _, nb := range blobs {
				b.Run(nb.label, func(b *testing.B) {
					blob := nb.blob
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						_ = blob.Materialize()
					}
				})
			}
		})
	}
}

// BenchmarkV2Layout_NumericBlobSet_AllMetrics simulates the typical BlobSet workload:
// M pre-decoded blobs, iterate N metrics across all blobs.
// Total cost = N_metrics × M_blobs × (lookup + iterate_points).
func BenchmarkV2Layout_NumericBlobSet_AllMetrics(b *testing.B) {
	sizes := []struct {
		name    string
		blobs   int
		metrics int
		points  int
	}{
		{"5blobs_150metrics_10points", 5, 150, 10},
		{"10blobs_150metrics_10points", 10, 150, 10},
		{"5blobs_150metrics_100points", 5, 150, 100},
		{"10blobs_500metrics_10points", 10, 500, 10},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			sets := createLayoutBlobSets(b, sz.blobs, sz.metrics, sz.points)
			// Use sorted IDs so all configs iterate in the same order
			ids := make([]uint64, sz.metrics)
			for i := range sz.metrics {
				ids[i] = uint64(i + 1)
			}
			expectedTotal := sz.blobs * sz.metrics * sz.points

			for _, ns := range sets {
				b.Run(ns.label, func(b *testing.B) {
					set := ns.set
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						total := 0
						for _, id := range ids {
							for _, dp := range set.All(id) {
								_ = dp.Val
								total++
							}
						}
						if total != expectedTotal {
							b.Fatalf("got %d points, want %d", total, expectedTotal)
						}
					}
				})
			}
		})
	}
}

// BenchmarkV2Layout_NumericBlobSet_ValueAt tests random-access pattern across BlobSet.
func BenchmarkV2Layout_NumericBlobSet_ValueAt(b *testing.B) {
	const (
		numBlobs   = 10
		numMetrics = 150
		numPoints  = 10
	)

	sets := createLayoutBlobSets(b, numBlobs, numMetrics, numPoints)

	// Pre-build random access pattern: pick 50 random (metricID, globalIndex) pairs
	rng := rand.New(rand.NewPCG(77, 0))
	type query struct {
		metricID uint64
		index    int
	}
	queries := make([]query, 50)
	totalPerMetric := numBlobs * numPoints
	for i := range queries {
		queries[i] = query{
			metricID: uint64(rng.IntN(numMetrics) + 1),
			index:    rng.IntN(totalPerMetric),
		}
	}

	for _, ns := range sets {
		b.Run(ns.label, func(b *testing.B) {
			set := ns.set
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for _, q := range queries {
					_, _ = set.ValueAt(q.metricID, q.index)
				}
			}
		})
	}
}

// createLayoutBlobSets creates NumericBlobSets with V1, V2, and V2+SharedTS blobs.
func createLayoutBlobSets(tb testing.TB, numBlobs, numMetrics, pointsPerMetric int) []benchBlobSet {
	tb.Helper()

	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	configs := layoutConfigs()
	result := make([]benchBlobSet, len(configs))

	for ci, cfg := range configs {
		blobs := make([]NumericBlob, numBlobs)

		for bi := range numBlobs {
			startTime := baseTime.Add(time.Duration(bi) * time.Hour)
			opts := append([]NumericEncoderOption{
				WithTimestampEncoding(format.TypeDelta),
				WithValueEncoding(format.TypeGorilla),
			}, cfg.opts...)

			enc, err := NewNumericEncoder(startTime, opts...)
			if err != nil {
				tb.Fatalf("%s: encoder: %v", cfg.label, err)
			}

			// Insert in reverse order to exercise V2 sorting
			for m := numMetrics; m >= 1; m-- {
				if err = enc.StartMetricID(uint64(m), pointsPerMetric); err != nil {
					tb.Fatal(err)
				}
				for p := range pointsPerMetric {
					ts := startTime.Add(time.Duration(p) * time.Second).UnixMicro()
					if err = enc.AddDataPoint(ts, float64(m*1000+p), ""); err != nil {
						tb.Fatal(err)
					}
				}
				if err = enc.EndMetric(); err != nil {
					tb.Fatal(err)
				}
			}

			data, err := enc.Finish()
			if err != nil {
				tb.Fatal(err)
			}

			dec, err := NewNumericDecoder(data)
			if err != nil {
				tb.Fatal(err)
			}

			blobs[bi], err = dec.Decode()
			if err != nil {
				tb.Fatal(err)
			}
		}

		set, err := NewNumericBlobSet(blobs)
		if err != nil {
			tb.Fatal(err)
		}
		result[ci] = benchBlobSet{label: cfg.label, set: set}
	}

	return result
}

// BenchmarkV2Layout_EndToEnd simulates the typical production pipeline:
// decode raw bytes from storage → create BlobSet → iterate 50% of metrics once.
func BenchmarkV2Layout_EndToEnd(b *testing.B) {
	sizes := []struct {
		name    string
		blobs   int
		metrics int
		points  int
	}{
		{"5blobs_150metrics_10points", 5, 150, 10},
		{"10blobs_150metrics_10points", 10, 150, 10},
		{"5blobs_150metrics_100points", 5, 150, 100},
		{"10blobs_500metrics_10points", 10, 500, 10},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			// Pre-encode raw blob data (simulates what Cassandra returns)
			allRaw := encodeLayoutRawBlobs(b, sz.blobs, sz.metrics, sz.points)

			// Query 50% of metrics (every other ID)
			queryIDs := make([]uint64, 0, sz.metrics/2)
			for i := 1; i <= sz.metrics; i += 2 {
				queryIDs = append(queryIDs, uint64(i))
			}
			expectedPerMetric := sz.blobs * sz.points

			for _, nr := range allRaw {
				b.Run(nr.label, func(b *testing.B) {
					raw := nr.raw
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						// Step 1: Decode all blobs
						blobs := make([]NumericBlob, len(raw))
						for i, r := range raw {
							dec, err := NewNumericDecoder(r)
							if err != nil {
								b.Fatal(err)
							}
							blobs[i], err = dec.Decode()
							if err != nil {
								b.Fatal(err)
							}
						}

						// Step 2: Create BlobSet
						set, err := NewNumericBlobSet(blobs)
						if err != nil {
							b.Fatal(err)
						}

						// Step 3: Iterate 50% of metrics
						total := 0
						for _, id := range queryIDs {
							for _, dp := range set.All(id) {
								_ = dp.Val
								total++
							}
						}
						if total != len(queryIDs)*expectedPerMetric {
							b.Fatalf("got %d points, want %d", total, len(queryIDs)*expectedPerMetric)
						}
					}
				})
			}
		})
	}
}

// encodeLayoutRawBlobs creates pre-encoded blob bytes for V1, V2, and V2+SharedTS.
// This simulates the raw data fetched from Cassandra.
func encodeLayoutRawBlobs(tb testing.TB, numBlobs, numMetrics, pointsPerMetric int) []benchRawBlobs {
	tb.Helper()

	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	configs := layoutConfigs()
	result := make([]benchRawBlobs, len(configs))

	for ci, cfg := range configs {
		raw := make([][]byte, numBlobs)

		for bi := range numBlobs {
			startTime := baseTime.Add(time.Duration(bi) * time.Hour)
			opts := append([]NumericEncoderOption{
				WithTimestampEncoding(format.TypeDelta),
				WithValueEncoding(format.TypeGorilla),
			}, cfg.opts...)

			enc, err := NewNumericEncoder(startTime, opts...)
			if err != nil {
				tb.Fatal(err)
			}

			// Reverse insertion order to exercise V2 sort path
			for m := numMetrics; m >= 1; m-- {
				if err = enc.StartMetricID(uint64(m), pointsPerMetric); err != nil {
					tb.Fatal(err)
				}
				for p := range pointsPerMetric {
					ts := startTime.Add(time.Duration(p) * time.Second).UnixMicro()
					if err = enc.AddDataPoint(ts, float64(m*1000+p), ""); err != nil {
						tb.Fatal(err)
					}
				}
				if err = enc.EndMetric(); err != nil {
					tb.Fatal(err)
				}
			}

			raw[bi], err = enc.Finish()
			if err != nil {
				tb.Fatal(err)
			}
		}

		result[ci] = benchRawBlobs{label: cfg.label, raw: raw}
	}

	return result
}
