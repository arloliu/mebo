package blob

import (
	"testing"
	"time"

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
