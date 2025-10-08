package blob

import (
	"fmt"
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
)

// BenchmarkTextEncoder_DeltaEncoding benchmarks delta encoding performance
// with different data sizes.
func BenchmarkTextEncoder_DeltaEncoding(b *testing.B) {
	blobTS := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	sizes := []struct {
		name  string
		count int
	}{
		{"10pts", 10},
		{"100pts", 100},
		{"1000pts", 1000},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			// Generate test data
			timestamps := make([]int64, size.count)
			values := make([]string, size.count)
			for i := range size.count {
				timestamps[i] = blobTS.Add(time.Duration(i) * time.Second).UnixMicro()
				values[i] = fmt.Sprintf("value%d", i)
			}

			metricID := hash.ID("test.metric")

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				encoder, _ := NewTextEncoder(blobTS,
					WithTextTimestampEncoding(format.TypeDelta),
					WithTextDataCompression(format.CompressionNone))

				_ = encoder.StartMetricID(metricID, size.count)

				for i := range size.count {
					_ = encoder.AddDataPoint(timestamps[i], values[i], "")
				}

				_ = encoder.EndMetric()
				_, _ = encoder.Finish()
			}
		})
	}
}
