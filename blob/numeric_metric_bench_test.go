package blob

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
)

// BenchmarkNumericMetric_Lookup measures one ValueAt plus one TimestampAt per metric over the gate fixture
// (100 metrics × 150 points, shared DeltaPacked timestamps, index (m*37) % 150),
// through the NumericBlob accessors and through handles resolved once.
func BenchmarkNumericMetric_Lookup(b *testing.B) {
	cols := alpRLEGateColumns(0.5, 1)
	for _, c := range alpRLEGateEncodings {
		blob := alpRLEGateBlob(b, cols, c.enc)
		handles := make([]NumericMetric, alpRLEGateMetrics)
		for m := range handles {
			h, ok := blob.Metric(uint64(m + 1))
			require.True(b, ok)
			handles[m] = h
		}

		b.Run(c.name+"/NumericBlob", func(b *testing.B) {
			var sink float64
			var sinkTS int64
			b.ReportAllocs()
			for b.Loop() {
				for m := range alpRLEGateMetrics {
					idx := (m * 37) % alpRLEGatePoints
					v, _ := blob.ValueAt(uint64(m+1), idx)
					ts, _ := blob.TimestampAt(uint64(m+1), idx)
					sink += v
					sinkTS += ts
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*alpRLEGateMetrics), "ns/lookup")
			if sink == -1 || sinkTS == -1 {
				b.Fatal("unreachable")
			}
		})

		b.Run(c.name+"/NumericMetric", func(b *testing.B) {
			var sink float64
			var sinkTS int64
			b.ReportAllocs()
			for b.Loop() {
				for m := range alpRLEGateMetrics {
					idx := (m * 37) % alpRLEGatePoints
					h := &handles[m]
					v, _ := h.ValueAt(idx)
					ts, _ := h.TimestampAt(idx)
					sink += v
					sinkTS += ts
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*alpRLEGateMetrics), "ns/lookup")
			if sink == -1 || sinkTS == -1 {
				b.Fatal("unreachable")
			}
		})
	}
}

// BenchmarkNumericMetric_Resolve measures one resolution per iteration, by ID on the gate fixture
// and by name on the same shape with retained names.
func BenchmarkNumericMetric_Resolve(b *testing.B) {
	byID := alpRLEGateBlob(b, alpRLEGateColumns(0.5, 1), format.TypeALP)
	byName := handleTestBlob(b, handleTestMetrics(alpRLEGateMetrics, alpRLEGatePoints, handleTestIdentical), true,
		WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeALP))

	b.Run("Metric", func(b *testing.B) {
		var sink int
		b.ReportAllocs()
		for b.Loop() {
			h, _ := byID.Metric(50)
			sink += h.Len()
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})

	b.Run("MetricByName", func(b *testing.B) {
		var sink int
		b.ReportAllocs()
		for b.Loop() {
			h, _ := byName.MetricByName("metric.50")
			sink += h.Len()
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})
}

// handleBenchSet builds the set gate fixture (S): four blobs of the production shape with retained names,
// each at a later start time with later timestamps, so one metric spans 600 points.
func handleBenchSet(tb testing.TB) BlobSet {
	tb.Helper()

	return handleBenchSetWith(tb, format.TypeALP, false)
}

// handleBenchSetWith builds the set gate fixture with the given value encoding,
// and with tags on, "host=server1" on every point, when tagged is set:
// Chimp with tags is the consumer fixture (C).
func handleBenchSetWith(tb testing.TB, valEnc format.EncodingType, tagged bool) BlobSet {
	tb.Helper()
	cols := alpRLEGateColumns(0.5, 1)
	blobs := make([]NumericBlob, 0, 4)
	for k := range 4 {
		ms := make([]handleTestMetric, len(cols))
		for m, col := range cols {
			ts := make([]int64, len(col))
			tags := make([]string, len(col))
			for i := range ts {
				ts[i] = handleTestStart + int64(k*alpRLEGatePoints+i)*15_000_000
				tags[i] = "host=server1"
			}
			ms[m] = handleTestMetric{name: "metric." + strconv.Itoa(m+1), ts: ts, vals: col, tags: tags}
		}
		start := handleTestStart + int64(k)*3_600_000_000
		blobs = append(blobs, handleTestBlobAt(tb, start, ms, true,
			WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(valEnc), WithTagsEnabled(tagged)))
	}

	return NewBlobSet(blobs, nil)
}

// BenchmarkNumericMetric_SetLookup measures one ValueAt plus one TimestampAt at index 599 of a 600-point metric over the set gate fixture,
// through the BlobSet ByName accessors and through a handle resolved once.
func BenchmarkNumericMetric_SetLookup(b *testing.B) {
	bs := handleBenchSet(b)
	const name = "metric.50"
	h, ok := bs.NumericMetricByName(name)
	require.True(b, ok)
	require.Equal(b, 4*alpRLEGatePoints, h.Len())
	const idx = 4*alpRLEGatePoints - 1

	b.Run("BlobSet", func(b *testing.B) {
		var sink float64
		var sinkTS int64
		b.ReportAllocs()
		for b.Loop() {
			v, _ := bs.NumericValueAtByName(name, idx)
			ts, _ := bs.TimestampAtByName(name, idx)
			sink += v
			sinkTS += ts
		}
		if sink == -1 || sinkTS == -1 {
			b.Fatal("unreachable")
		}
	})

	b.Run("NumericMetric", func(b *testing.B) {
		var sink float64
		var sinkTS int64
		b.ReportAllocs()
		for b.Loop() {
			v, _ := h.ValueAt(idx)
			ts, _ := h.TimestampAt(idx)
			sink += v
			sinkTS += ts
		}
		if sink == -1 || sinkTS == -1 {
			b.Fatal("unreachable")
		}
	})
}

// BenchmarkNumericMetric_SetForEachTimestamps measures one pass over the 600 timestamps of a set metric:
// the consumer's blob-by-blob loop, the set's AllTimestampsByName iterator, and the handle's ForEachTimestamps.
func BenchmarkNumericMetric_SetForEachTimestamps(b *testing.B) {
	bs := handleBenchSet(b)
	const name = "metric.50"
	h, ok := bs.NumericMetricByName(name)
	require.True(b, ok)
	blobs := bs.NumericBlobs()

	b.Run("blob-by-blob", func(b *testing.B) {
		var sink int64
		b.ReportAllocs()
		for b.Loop() {
			base := 0
			for i := range blobs {
				if !blobs[i].HasMetricName(name) {
					continue
				}
				blobs[i].ForEachTimestampsByName(name, func(_ int, ts int64) bool {
					sink += ts

					return true
				})
				base += blobs[i].LenByName(name)
			}
			sink += int64(base)
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})

	b.Run("AllTimestampsByName", func(b *testing.B) {
		var sink int64
		b.ReportAllocs()
		for b.Loop() {
			for _, ts := range bs.AllTimestampsByName(name) {
				sink += ts
			}
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})

	b.Run("NumericMetric", func(b *testing.B) {
		var sink int64
		b.ReportAllocs()
		for b.Loop() {
			h.ForEachTimestamps(func(_ int, ts int64) bool {
				sink += ts

				return true
			})
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})
}

// BenchmarkNumericMetric_SetResolve measures one set resolution per iteration, by name and by ID,
// for a metric four members hold.
func BenchmarkNumericMetric_SetResolve(b *testing.B) {
	bs := handleBenchSet(b)
	const name = "metric.50"
	id := hash.ID(name)

	b.Run("NumericMetricByName", func(b *testing.B) {
		var sink int
		b.ReportAllocs()
		for b.Loop() {
			h, _ := bs.NumericMetricByName(name)
			sink += h.Len()
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})

	b.Run("NumericMetric", func(b *testing.B) {
		var sink int
		b.ReportAllocs()
		for b.Loop() {
			h, _ := bs.NumericMetric(id)
			sink += h.Len()
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})
}

// BenchmarkNumericMetric_ConsumerMaterialize measures materializing one 600-point metric of the consumer fixture
// (Chimp values, tags), resolved fresh in every iteration,
// through MaterializeNumericMetricByName and through a handle and its Materialize.
func BenchmarkNumericMetric_ConsumerMaterialize(b *testing.B) {
	bs := handleBenchSetWith(b, format.TypeChimp, true)
	const name = "metric.50"

	b.Run("MaterializeNumericMetricByName", func(b *testing.B) {
		var sink int
		b.ReportAllocs()
		for b.Loop() {
			m, _ := bs.MaterializeNumericMetricByName(name)
			sink += len(m.Values)
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})

	b.Run("NumericMetric", func(b *testing.B) {
		var sink int
		b.ReportAllocs()
		for b.Loop() {
			h, _ := bs.NumericMetricByName(name)
			h.Materialize()
			sink += h.Len()
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})
}

// BenchmarkNumericMetric_ConsumerAt measures one point lookup at index 599 of the consumer fixture's metric:
// At on a materialized handle, and the three accessors of MaterializedNumericMetric as the reference.
func BenchmarkNumericMetric_ConsumerAt(b *testing.B) {
	bs := handleBenchSetWith(b, format.TypeChimp, true)
	const name = "metric.50"
	const idx = 4*alpRLEGatePoints - 1
	h, ok := bs.NumericMetricByName(name)
	require.True(b, ok)
	h.Materialize()
	require.Equal(b, AccessDirect, h.ValueAccess())
	m, ok := bs.MaterializeNumericMetricByName(name)
	require.True(b, ok)

	b.Run("MaterializedNumericMetric", func(b *testing.B) {
		var sink int64
		b.ReportAllocs()
		for b.Loop() {
			ts, _ := m.TimestampAt(idx)
			v, _ := m.ValueAt(idx)
			tag, _ := m.TagAt(idx)
			sink += ts + int64(v) + int64(len(tag))
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})

	b.Run("NumericMetric", func(b *testing.B) {
		var sink int64
		b.ReportAllocs()
		for b.Loop() {
			dp, _ := h.At(idx)
			sink += dp.Ts + int64(dp.Val) + int64(len(dp.Tag))
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})
}

// BenchmarkNumericMetric_ConsumerForEach measures one ForEach pass over the 600 points of the consumer fixture's metric:
// the consumer's blob-by-blob loop, a range over a MaterializedNumericMetric's slices as the reference,
// and ForEach on a materialized handle.
func BenchmarkNumericMetric_ConsumerForEach(b *testing.B) {
	bs := handleBenchSetWith(b, format.TypeChimp, true)
	const name = "metric.50"
	h, ok := bs.NumericMetricByName(name)
	require.True(b, ok)
	h.Materialize()
	m, ok := bs.MaterializeNumericMetricByName(name)
	require.True(b, ok)
	blobs := bs.NumericBlobs()

	b.Run("blob-by-blob", func(b *testing.B) {
		var sink int64
		b.ReportAllocs()
		for b.Loop() {
			for i := range blobs {
				blobs[i].ForEachByName(name, func(_ int, dp NumericDataPoint) bool {
					sink += dp.Ts + int64(len(dp.Tag))

					return true
				})
			}
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})

	b.Run("MaterializedNumericMetric", func(b *testing.B) {
		var sink int64
		b.ReportAllocs()
		for b.Loop() {
			for i, ts := range m.Timestamps {
				sink += ts + int64(m.Values[i]) + int64(len(m.Tags[i]))
			}
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})

	b.Run("NumericMetric", func(b *testing.B) {
		var sink int64
		b.ReportAllocs()
		for b.Loop() {
			h.ForEach(func(_ int, dp NumericDataPoint) bool {
				sink += dp.Ts + int64(dp.Val) + int64(len(dp.Tag))

				return true
			})
		}
		if sink == -1 {
			b.Fatal("unreachable")
		}
	})
}
