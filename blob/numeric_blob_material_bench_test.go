package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
)

// ==============================================================================
// Materialization Overhead Benchmarks
// ==============================================================================

// BenchmarkMaterialize measures the cost of materializing a blob
func BenchmarkMaterialize(b *testing.B) {
	// Create test blob: 150 metrics × 1000 points
	startTime := time.Now()
	encoder, _ := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla))

	for metricID := range 150 {
		_ = encoder.StartMetricID(uint64(metricID), 1000)
		for i := range 1000 {
			_ = encoder.AddDataPoint(int64(i*1000), float64(i), "")
		}
		_ = encoder.EndMetric()
	}

	blobBytes, _ := encoder.Finish()
	decoder, _ := NewNumericDecoder(blobBytes)
	blob, _ := decoder.Decode()

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		_ = blob.Materialize()
	}
}

// BenchmarkMaterializeMetric measures the cost of materializing a single metric
func BenchmarkMaterializeMetric(b *testing.B) {
	// Create test blob with 1 metric, 1000 points
	startTime := time.Now()
	encoder, _ := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla))

	metricID := uint64(12345)
	_ = encoder.StartMetricID(metricID, 1000)
	for i := range 1000 {
		_ = encoder.AddDataPoint(int64(i*1000), float64(i), "")
	}
	_ = encoder.EndMetric()

	blobBytes, _ := encoder.Finish()
	decoder, _ := NewNumericDecoder(blobBytes)
	blob, _ := decoder.Decode()

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		_, _ = blob.MaterializeMetric(metricID)
	}
}

// ==============================================================================
// Random Access Performance - Materialized vs Non-Materialized
// ==============================================================================

// BenchmarkValueAt_Sequential_NonMaterialized measures sequential access cost without materialization
func BenchmarkValueAt_Sequential_NonMaterialized(b *testing.B) {
	// Create test blob
	startTime := time.Now()
	encoder, _ := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla))

	metricID := uint64(12345)
	_ = encoder.StartMetricID(metricID, 1000)
	for i := range 1000 {
		_ = encoder.AddDataPoint(int64(i*1000), float64(i), "")
	}
	_ = encoder.EndMetric()

	blobBytes, _ := encoder.Finish()
	decoder, _ := NewNumericDecoder(blobBytes)
	blob, _ := decoder.Decode()

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Access all 1000 points sequentially using iterator
		for v := range blob.AllValues(metricID) {
			_ = v
		}
	}
}

// BenchmarkValueAt_Sequential_Materialized measures sequential access cost with materialization
func BenchmarkValueAt_Sequential_Materialized(b *testing.B) {
	// Create and materialize test blob
	startTime := time.Now()
	encoder, _ := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla))

	metricID := uint64(12345)
	_ = encoder.StartMetricID(metricID, 1000)
	for i := range 1000 {
		_ = encoder.AddDataPoint(int64(i*1000), float64(i), "")
	}
	_ = encoder.EndMetric()

	blobBytes, _ := encoder.Finish()
	decoder, _ := NewNumericDecoder(blobBytes)
	blob, _ := decoder.Decode()
	material := blob.Materialize()

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Access all 1000 points sequentially via materialized access
		for i := range 1000 {
			_, _ = material.ValueAt(metricID, i)
		}
	}
}

// BenchmarkValueAt_Random_NonMaterialized measures random access cost without materialization
func BenchmarkValueAt_Random_NonMaterialized(b *testing.B) {
	// Create test blob
	startTime := time.Now()
	encoder, _ := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeRaw), // Raw supports O(1) random access
		WithValueEncoding(format.TypeRaw))

	metricID := uint64(12345)
	_ = encoder.StartMetricID(metricID, 1000)
	for i := range 1000 {
		_ = encoder.AddDataPoint(int64(i*1000), float64(i), "")
	}
	_ = encoder.EndMetric()

	blobBytes, _ := encoder.Finish()
	decoder, _ := NewNumericDecoder(blobBytes)
	blob, _ := decoder.Decode()

	// Random access pattern
	indices := []int{0, 500, 999, 250, 750, 100, 900, 50, 450, 850}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		for _, idx := range indices {
			_, _ = blob.ValueAt(metricID, idx)
		}
	}
}

// BenchmarkValueAt_Random_Materialized measures random access cost with materialization
func BenchmarkValueAt_Random_Materialized(b *testing.B) {
	// Create and materialize test blob
	startTime := time.Now()
	encoder, _ := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeRaw), // Raw for fair comparison
		WithValueEncoding(format.TypeRaw))

	metricID := uint64(12345)
	_ = encoder.StartMetricID(metricID, 1000)
	for i := range 1000 {
		_ = encoder.AddDataPoint(int64(i*1000), float64(i), "")
	}
	_ = encoder.EndMetric()

	blobBytes, _ := encoder.Finish()
	decoder, _ := NewNumericDecoder(blobBytes)
	blob, _ := decoder.Decode()
	material := blob.Materialize()

	// Random access pattern
	indices := []int{0, 500, 999, 250, 750, 100, 900, 50, 450, 850}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		for _, idx := range indices {
			_, _ = material.ValueAt(metricID, idx)
		}
	}
}

// BenchmarkValueAt_Random_Delta_NonMaterialized measures Delta encoding random access (worst case - O(N))
func BenchmarkValueAt_Random_Delta_NonMaterialized(b *testing.B) {
	// Create test blob with Delta encoding
	startTime := time.Now()
	encoder, _ := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla))

	metricID := uint64(12345)
	_ = encoder.StartMetricID(metricID, 1000)
	for i := range 1000 {
		_ = encoder.AddDataPoint(int64(i*1000), float64(i), "")
	}
	_ = encoder.EndMetric()

	blobBytes, _ := encoder.Finish()
	decoder, _ := NewNumericDecoder(blobBytes)
	blob, _ := decoder.Decode()

	// Random access pattern (Delta encoding doesn't support random access, falls back to iteration)
	indices := []int{500, 999} // Only test a few to avoid slow benchmarks

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		for _, idx := range indices {
			// This will iterate from start to idx (O(N) for each access)
			count := 0
			for val := range blob.AllValues(metricID) {
				if count == idx {
					_ = val
					break
				}
				count++
			}
		}
	}
}

// BenchmarkValueAt_Random_Delta_Materialized measures Delta encoding random access with materialization
func BenchmarkValueAt_Random_Delta_Materialized(b *testing.B) {
	// Create and materialize test blob with Delta encoding
	startTime := time.Now()
	encoder, _ := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla))

	metricID := uint64(12345)
	_ = encoder.StartMetricID(metricID, 1000)
	for i := range 1000 {
		_ = encoder.AddDataPoint(int64(i*1000), float64(i), "")
	}
	_ = encoder.EndMetric()

	blobBytes, _ := encoder.Finish()
	decoder, _ := NewNumericDecoder(blobBytes)
	blob, _ := decoder.Decode()
	material := blob.Materialize()

	// Random access pattern
	indices := []int{500, 999, 250, 750, 100, 900, 50, 450, 850}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		for _, idx := range indices {
			_, _ = material.ValueAt(metricID, idx)
		}
	}
}

// ==============================================================================
// MaterializedMetric Benchmarks
// ==============================================================================

// BenchmarkMaterializedMetric_ValueAt measures O(1) access performance
func BenchmarkMaterializedMetric_ValueAt(b *testing.B) {
	// Create and materialize a single metric
	startTime := time.Now()
	encoder, _ := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla))

	metricID := uint64(12345)
	_ = encoder.StartMetricID(metricID, 1000)
	for i := range 1000 {
		_ = encoder.AddDataPoint(int64(i*1000), float64(i), "")
	}
	_ = encoder.EndMetric()

	blobBytes, _ := encoder.Finish()
	decoder, _ := NewNumericDecoder(blobBytes)
	blob, _ := decoder.Decode()
	metric, _ := blob.MaterializeMetric(metricID)

	// Random indices
	indices := []int{0, 500, 999, 250, 750}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		for _, idx := range indices {
			_, _ = metric.ValueAt(idx)
		}
	}
}

// ==============================================================================
// Multi-Metric Benchmarks
// ==============================================================================

// BenchmarkMaterialize_150Metrics measures realistic workload (150 metrics × 1000 points)
func BenchmarkMaterialize_150Metrics(b *testing.B) {
	// Create realistic blob: 150 metrics × 1000 points ≈ 2.4MB
	startTime := time.Now()
	encoder, _ := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla))

	for metricID := range 150 {
		_ = encoder.StartMetricID(uint64(metricID+100), 1000)
		for i := range 1000 {
			_ = encoder.AddDataPoint(int64(i*1000), float64(i), "")
		}
		_ = encoder.EndMetric()
	}

	blobBytes, _ := encoder.Finish()
	decoder, _ := NewNumericDecoder(blobBytes)
	blob, _ := decoder.Decode()

	b.ReportMetric(float64(len(blobBytes))/1024/1024, "MB")
	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		material := blob.Materialize()
		// Access a few random metrics to ensure materialization completes
		_, _ = material.ValueAt(100, 500)
		_, _ = material.ValueAt(150, 500)
		_, _ = material.ValueAt(200, 500)
	}
}

// BenchmarkRandomAccess_150Metrics_Materialized measures random metric access after materialization
func BenchmarkRandomAccess_150Metrics_Materialized(b *testing.B) {
	// Create and materialize realistic blob
	startTime := time.Now()
	encoder, _ := NewNumericEncoder(startTime,
		WithTimestampEncoding(format.TypeDelta),
		WithValueEncoding(format.TypeGorilla))

	for metricID := range 150 {
		_ = encoder.StartMetricID(uint64(metricID+100), 1000)
		for i := range 1000 {
			_ = encoder.AddDataPoint(int64(i*1000), float64(i), "")
		}
		_ = encoder.EndMetric()
	}

	blobBytes, _ := encoder.Finish()
	decoder, _ := NewNumericDecoder(blobBytes)
	blob, _ := decoder.Decode()
	material := blob.Materialize()

	// Access pattern: random metrics, random indices
	metricIDs := []uint64{100, 125, 150, 175, 200, 225, 249}
	indices := []int{0, 500, 999, 250, 750}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		for _, metricID := range metricIDs {
			for _, idx := range indices {
				_, _ = material.ValueAt(metricID, idx)
			}
		}
	}
}
