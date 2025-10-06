package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
)

// BenchmarkNumericBlobSetMaterialize measures the overhead of materializing a BlobSet
func BenchmarkNumericBlobSetMaterialize(b *testing.B) {
	benchmarks := []struct {
		name            string
		numBlobs        int
		metricsPerBlob  int
		pointsPerMetric int
		tsEncoding      format.EncodingType
		valEncoding     format.EncodingType
	}{
		{"10Blobs_10Metrics_100Points_Raw", 10, 10, 100, format.TypeRaw, format.TypeRaw},
		{"10Blobs_10Metrics_100Points_Delta", 10, 10, 100, format.TypeDelta, format.TypeGorilla},
		{"10Blobs_50Metrics_100Points_Raw", 10, 50, 100, format.TypeRaw, format.TypeRaw},
		{"10Blobs_50Metrics_100Points_Delta", 10, 50, 100, format.TypeDelta, format.TypeGorilla},
		{"20Blobs_150Metrics_100Points_Delta", 20, 150, 100, format.TypeDelta, format.TypeGorilla},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			// Create test BlobSet once
			blobs, err := createEncodedBlobs(
				bm.numBlobs,
				bm.metricsPerBlob,
				bm.pointsPerMetric,
				bm.tsEncoding,
				bm.valEncoding,
			)
			if err != nil {
				b.Fatalf("Failed to create blobs: %v", err)
			}

			set, err := NewNumericBlobSet(blobs)
			if err != nil {
				b.Fatalf("Failed to create BlobSet: %v", err)
			}

			b.ResetTimer()
			for b.Loop() {
				mat := set.Materialize()
				_ = mat
			}
		})
	}
}

// BenchmarkMaterializedVsSequential compares random access performance
func BenchmarkMaterializedVsSequential(b *testing.B) {
	const (
		numBlobs        = 10
		metricsPerBlob  = 20
		pointsPerMetric = 100
	)

	// Create test BlobSet with Delta encoding (worst case for sequential access)
	blobs, err := createEncodedBlobs(
		numBlobs,
		metricsPerBlob,
		pointsPerMetric,
		format.TypeDelta,
		format.TypeGorilla,
	)
	if err != nil {
		b.Fatalf("Failed to create blobs: %v", err)
	}

	set, err := NewNumericBlobSet(blobs)
	if err != nil {
		b.Fatalf("Failed to create BlobSet: %v", err)
	}

	// Get first metric ID for testing
	var testMetricID uint64
	mat := set.Materialize()
	metricIDs := mat.MetricIDs()
	if len(metricIDs) > 0 {
		testMetricID = metricIDs[0]
	}

	b.Run("Sequential", func(b *testing.B) {
		b.ResetTimer()
		for b.Loop() {
			// Simulate random access by iterating to find metric each time
			for idx, point := range set.All(testMetricID) {
				if idx == 50 {
					_ = point.Val // Access middle point
					break
				}
			}
		}
	})

	b.Run("Materialized", func(b *testing.B) {
		mat := set.Materialize()

		b.ResetTimer()
		for b.Loop() {
			_, _ = mat.ValueAt(testMetricID, 50)
		}
	})
}

// BenchmarkRandomAccessPattern simulates realistic workload
func BenchmarkRandomAccessPattern(b *testing.B) {
	const (
		numBlobs        = 10
		metricsPerBlob  = 20
		pointsPerMetric = 100
	)

	// Create test BlobSet
	blobs, err := createEncodedBlobs(
		numBlobs,
		metricsPerBlob,
		pointsPerMetric,
		format.TypeDelta,
		format.TypeGorilla,
	)
	if err != nil {
		b.Fatalf("Failed to create blobs: %v", err)
	}

	set, err := NewNumericBlobSet(blobs)
	if err != nil {
		b.Fatalf("Failed to create BlobSet: %v", err)
	}

	mat := set.Materialize()

	// Get all metric IDs
	metricIDs := mat.MetricIDs()
	if len(metricIDs) == 0 {
		b.Fatal("No metrics found")
	}

	// Test accessing multiple points across multiple metrics
	b.ResetTimer()
	for b.Loop() {
		// Access 3 different metrics at 3 different time points each
		for i := range 3 {
			metricID := metricIDs[i%len(metricIDs)]
			for j := range 3 {
				idx := j * 30 // Access at different positions
				_, _ = mat.ValueAt(metricID, idx)
				_, _ = mat.TimestampAt(metricID, idx)
			}
		}
	}
}

// BenchmarkByNameVsByID compares lookup performance
func BenchmarkByNameVsByID(b *testing.B) {
	const (
		numBlobs        = 5
		metricsPerBlob  = 10
		pointsPerMetric = 100
	)

	// Create test BlobSet
	blobs, err := createEncodedBlobs(
		numBlobs,
		metricsPerBlob,
		pointsPerMetric,
		format.TypeDelta,
		format.TypeGorilla,
	)
	if err != nil {
		b.Fatalf("Failed to create blobs: %v", err)
	}

	set, err := NewNumericBlobSet(blobs)
	if err != nil {
		b.Fatalf("Failed to create BlobSet: %v", err)
	}

	mat := set.Materialize()

	// Get first metric for testing
	metricIDs := mat.MetricIDs()
	if len(metricIDs) == 0 {
		b.Fatal("No metrics found")
	}
	testMetricID := metricIDs[0]

	metricNames := mat.MetricNames()
	if len(metricNames) == 0 {
		b.Skip("No metric names found - skipping ByName benchmark")
	}
	testMetricName := metricNames[0]

	b.Run("ByID", func(b *testing.B) {
		b.ResetTimer()
		for b.Loop() {
			_, _ = mat.ValueAt(testMetricID, 50)
		}
	})

	b.Run("ByName", func(b *testing.B) {
		b.ResetTimer()
		for b.Loop() {
			_, _ = mat.ValueAtByName(testMetricName, 50)
		}
	})
}

// Helper function to create encoded blobs for benchmarking
func createEncodedBlobs(
	numBlobs int,
	metricsPerBlob int,
	pointsPerMetric int,
	tsEncoding format.EncodingType,
	valEncoding format.EncodingType,
) ([]NumericBlob, error) {
	blobs := make([]NumericBlob, numBlobs)
	baseTime := time.Now()

	for blobIdx := range numBlobs {
		startTime := baseTime.Add(time.Duration(blobIdx) * time.Hour)
		encoder, err := NewNumericEncoder(
			startTime,
			WithTimestampEncoding(tsEncoding),
			WithValueEncoding(valEncoding),
		)
		if err != nil {
			return nil, err
		}

		for m := range metricsPerBlob {
			metricName := "metric_" + string(rune('A'+m))

			if err := encoder.StartMetricName(metricName, pointsPerMetric); err != nil {
				return nil, err
			}

			for i := range pointsPerMetric {
				ts := int64(blobIdx*1000000 + i*1000)
				val := float64(i) + float64(m)*100 + float64(blobIdx)*10000
				if err := encoder.AddDataPoint(ts, val, ""); err != nil {
					return nil, err
				}
			}

			if err := encoder.EndMetric(); err != nil {
				return nil, err
			}
		}

		blobBytes, err := encoder.Finish()
		if err != nil {
			return nil, err
		}

		decoder, err := NewNumericDecoder(blobBytes)
		if err != nil {
			return nil, err
		}

		blob, err := decoder.Decode()
		if err != nil {
			return nil, err
		}

		blobs[blobIdx] = blob
	}

	return blobs, nil
}
