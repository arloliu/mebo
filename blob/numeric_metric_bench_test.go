package blob

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
)

const (
	// consumerSetOneGroup gives every metric the same timestamps, which the encoder stores once.
	consumerSetOneGroup consumerSetSharing = iota
	// consumerSetPairs shifts each pair of metrics by its ordinal in microseconds: one group per pair.
	consumerSetPairs
	// consumerSetDistinct shifts every metric by its ordinal in microseconds: no two columns are alike.
	consumerSetDistinct
)

// consumerSetSharing is how the metrics of one blob of a consumer set relate their timestamp columns.
type consumerSetSharing uint8

// consumerSet describes a set of blobs encoded with the consumer's options:
// little endian, the V2 layout, shared DeltaPacked timestamps, no compression and no stored metric names.
// Every blob holds the same metrics, one point per second, and starts where the blob before it ends.
type consumerSet struct {
	blobs, metrics, points int
	valEnc                 format.EncodingType
	tags                   bool
	sharing                consumerSetSharing
	// byName starts the metrics by name, which hashes the name into the ID and stores no name.
	// Otherwise they are started by ID.
	byName bool
}

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

// BenchmarkNumericMetric_LookupDirect measures the lookups that never replay a column, 100 metrics × 150 points:
// Raw timestamps and values read from the payload, a materialized Delta and Chimp handle read from its slices,
// and shared DeltaPacked timestamps read from the pre-decoded group (with Raw values).
// Each iteration visits index (m*37) % 150 of every metric; ns/lookup is one ValueAt and TimestampAt pair
// in the first sub-benchmark and one At call in the second.
func BenchmarkNumericMetric_LookupDirect(b *testing.B) {
	const metrics, points = 100, 150
	cases := []struct {
		name        string
		opts        []NumericEncoderOption
		materialize bool
	}{
		{"raw-raw", []NumericEncoderOption{WithTimestampEncoding(format.TypeRaw), WithValueEncoding(format.TypeRaw)}, false},
		{"materialized-delta-chimp", []NumericEncoderOption{
			WithTimestampEncoding(format.TypeDelta), WithValueEncoding(format.TypeChimp),
		}, true},
		{"shared-deltapacked-raw", []NumericEncoderOption{
			WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeRaw),
		}, false},
	}
	for _, c := range cases {
		blob := handleTestBlob(b, handleTestMetrics(metrics, points, handleTestIdentical), false, c.opts...)
		handles := make([]NumericMetric, metrics)
		for m := range handles {
			h, ok := blob.Metric(uint64(m + 1))
			require.True(b, ok)
			if c.materialize {
				h.Materialize()
			}
			require.Equal(b, AccessDirect, h.TimestampAccess())
			require.Equal(b, AccessDirect, h.ValueAccess())
			handles[m] = h
		}

		b.Run(c.name+"/ValueAt+TimestampAt", func(b *testing.B) {
			var sink float64
			var sinkTS int64
			b.ReportAllocs()
			for b.Loop() {
				for m := range metrics {
					idx := (m * 37) % points
					h := &handles[m]
					v, _ := h.ValueAt(idx)
					ts, _ := h.TimestampAt(idx)
					sink += v
					sinkTS += ts
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*metrics), "ns/lookup")
			if sink == -1 || sinkTS == -1 {
				b.Fatal("unreachable")
			}
		})

		b.Run(c.name+"/At", func(b *testing.B) {
			var sink float64
			b.ReportAllocs()
			for b.Loop() {
				for m := range metrics {
					dp, _ := handles[m].At((m * 37) % points)
					sink += dp.Val + float64(dp.Ts)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*metrics), "ns/lookup")
			if sink == -1 {
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

// BenchmarkNumericMetric_SearchTimestamp measures a timestamp search over 2880 blobs of 30 points,
// the blob and point counts of a day at one point per second in 30-second blobs, with Chimp values:
// on a sequential timestamp axis of each codec, and on the group DeltaPacked blobs pre-decode when metrics share it.
// It times the handle's SearchTimestamp alone, with a ValueAt at the index it returns, and after Materialize,
// against a scan from the first point and a bisection over TimestampAt, which replays a blob's column on every probe,
// and the TimestampsSorted check a caller runs once before searching.
func BenchmarkNumericMetric_SearchTimestamp(b *testing.B) {
	const members, points = 2880, 30
	parts := make([][]int64, members)
	for k := range parts {
		parts[k], _, _ = handleTestSeries(points, int64(k)*points*15_000_000, 0)
	}
	first, last := parts[0][0], parts[members-1][points-1]
	targets := make([]int64, 1024)
	state := uint64(1)
	for i := range targets {
		state = state*6364136223846793005 + 1442695040888963407
		targets[i] = first + int64(state>>1)%(last-first)
	}

	for _, tt := range []struct {
		name  string
		opts  []NumericEncoderOption
		class AccessClass
	}{
		{"Delta", []NumericEncoderOption{WithTimestampEncoding(format.TypeDelta)}, AccessSequential},
		{"DeltaPacked", []NumericEncoderOption{WithTimestampEncoding(format.TypeDeltaPacked)}, AccessSequential},
		{"DeltaPacked-shared", []NumericEncoderOption{WithTimestampEncoding(format.TypeDeltaPacked), WithSharedTimestamps()}, AccessDirect},
	} {
		h := searchTestHandle(b, parts, append(tt.opts, WithValueEncoding(format.TypeChimp))...)
		require.Equal(b, tt.class, h.TimestampAccess())
		materialized := h
		materialized.Materialize()

		b.Run(tt.name+"/SearchTimestamp+ValueAt", func(b *testing.B) {
			var sink float64
			var n int
			b.ReportAllocs()
			for b.Loop() {
				index, _ := h.SearchTimestamp(targets[n&1023])
				v, _ := h.ValueAt(index)
				sink += v
				n++
			}
			if sink == -1 {
				b.Fatal("unreachable")
			}
		})

		b.Run(tt.name+"/SearchTimestamp", func(b *testing.B) {
			var sink, n int
			b.ReportAllocs()
			for b.Loop() {
				index, _ := h.SearchTimestamp(targets[n&1023])
				sink += index
				n++
			}
			if sink == -1 {
				b.Fatal("unreachable")
			}
		})

		b.Run(tt.name+"/SearchTimestamp-materialized", func(b *testing.B) {
			var sink, n int
			b.ReportAllocs()
			for b.Loop() {
				index, _ := materialized.SearchTimestamp(targets[n&1023])
				sink += index
				n++
			}
			if sink == -1 {
				b.Fatal("unreachable")
			}
		})

		b.Run(tt.name+"/TimestampsSorted", func(b *testing.B) {
			var sink int
			b.ReportAllocs()
			for b.Loop() {
				if h.TimestampsSorted() {
					sink++
				}
			}
			if sink == 0 {
				b.Fatal("the fixture is sorted")
			}
		})

		b.Run(tt.name+"/scan", func(b *testing.B) {
			var sink, n int
			b.ReportAllocs()
			for b.Loop() {
				target := targets[n&1023]
				h.ForEachTimestamps(func(i int, ts int64) bool {
					if ts >= target {
						sink += i

						return false
					}

					return true
				})
				n++
			}
			if sink == -1 {
				b.Fatal("unreachable")
			}
		})

		b.Run(tt.name+"/bisect-TimestampAt", func(b *testing.B) {
			var sink, n int
			b.ReportAllocs()
			for b.Loop() {
				target := targets[n&1023]
				lo, hi := 0, h.Len()
				for lo < hi {
					mid := (lo + hi) / 2
					if ts, _ := h.TimestampAt(mid); ts < target {
						lo = mid + 1
					} else {
						hi = mid
					}
				}
				sink += lo
				n++
			}
			if sink == -1 {
				b.Fatal("unreachable")
			}
		})
	}
}

// searchPassSet builds about a day of one point per second,
// with DeltaPacked timestamps two metrics share and Chimp values: the shape the search was written for.
// The regular set has 2880 blobs of 30 points.
// The irregular one has blobs of 27 to 33 points, one blob in 64 holding a single point,
// and three gaps of several hours, so neither an index nor a time maps evenly onto the blobs.
func searchPassSet(tb testing.TB, irregular bool) BlobSet {
	tb.Helper()
	const members = 2880
	parts := make([][]int64, members)
	next := handleTestStart
	state := uint32(7)
	for k := range parts {
		points := 30
		if irregular {
			state = state*1664525 + 1013904223
			points = 27 + int(state>>24)%7
			if k%64 == 63 {
				points = 1
			}
			if k == 400 || k == 1500 || k == 2500 {
				next += 5 * 3_600_000_000
			}
		}
		parts[k] = make([]int64, points)
		for i := range parts[k] {
			parts[k][i] = next
			next += 1_000_000
		}
	}

	return searchTestSet(tb, parts,
		WithTimestampEncoding(format.TypeDeltaPacked), WithSharedTimestamps(), WithValueEncoding(format.TypeChimp))
}

// BenchmarkNumericMetric_SearchPass measures one pass of nearest-point lookups over that day:
// one target per point, 300 ms after it, in increasing order,
// and per target the search, the timestamps on either side of the index it returns, and the value of the nearer one.
// It reports the time per lookup on the handle as resolved, on the handle after Materialize,
// and on the flattened MaterializedNumericMetric with slices.BinarySearch,
// which is what a caller writes without the search.
func BenchmarkNumericMetric_SearchPass(b *testing.B) {
	for _, shape := range []struct {
		name      string
		irregular bool
	}{{"regular", false}, {"irregular", true}} {
		bs := searchPassSet(b, shape.irregular)
		h, ok := bs.NumericMetric(1)
		require.True(b, ok)
		require.True(b, h.TimestampsSorted())
		n := h.Len()
		materialized := h
		materialized.Materialize()
		flat, ok := bs.MaterializeNumericMetric(1)
		require.True(b, ok)
		targets := make([]int64, n)
		for i, ts := range flat.Timestamps {
			targets[i] = ts + 300_000
		}

		pass := func(b *testing.B, h *NumericMetric) {
			b.Helper()
			var sink float64
			b.ReportAllocs()
			for b.Loop() {
				for _, target := range targets {
					i, _ := h.SearchTimestamp(target)
					nearest := i - 1
					if i < n {
						below, _ := h.TimestampAt(i - 1)
						at, _ := h.TimestampAt(i)
						if at-target < target-below {
							nearest = i
						}
					}
					v, _ := h.ValueAt(nearest)
					sink += v
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/lookup")
			if sink == -1 {
				b.Fatal("unreachable")
			}
		}
		b.Run(shape.name+"/NumericMetric", func(b *testing.B) { pass(b, &h) })
		b.Run(shape.name+"/NumericMetric-materialized", func(b *testing.B) { pass(b, &materialized) })
		b.Run(shape.name+"/flat-BinarySearch", func(b *testing.B) {
			var sink float64
			b.ReportAllocs()
			for b.Loop() {
				for _, target := range targets {
					i, _ := slices.BinarySearch(flat.Timestamps, target)
					nearest := i - 1
					if i < n && flat.Timestamps[i]-target < target-flat.Timestamps[i-1] {
						nearest = i
					}
					sink += flat.Values[nearest]
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/lookup")
			if sink == -1 {
				b.Fatal("unreachable")
			}
		})
	}
}

// BenchmarkNumericMetric_LargeSet measures what a caller pays before its first lookup on 2880 blobs of 200 metrics:
// decoding the set, resolving one handle by name, a different metric on every iteration,
// so each resolution reads index entries the one before it did not,
// and that resolution followed by the TimestampsSorted check and by Materialize.
func BenchmarkNumericMetric_LargeSet(b *testing.B) {
	const members, metrics, points = 2880, 200, 30
	names := make([]string, metrics)
	for m := range names {
		names[m] = "svid." + strconv.Itoa(m)
	}
	inputs := make([][]byte, members)
	for k := range inputs {
		start := handleTestStart + int64(k*points)*1_000_000
		enc, err := NewNumericEncoder(time.UnixMicro(start), WithLittleEndian(), WithMetricNames(), WithSharedTimestamps(),
			WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeChimp),
			WithTimestampCompression(format.CompressionNone), WithValueCompression(format.CompressionNone))
		require.NoError(b, err)
		for m, name := range names {
			require.NoError(b, enc.StartMetricName(name, points))
			for i := range points {
				require.NoError(b, enc.AddDataPoint(start+int64(i)*1_000_000, float64(m)+float64(i%7)*0.25, ""))
			}
			require.NoError(b, enc.EndMetric())
		}
		data, err := enc.Finish()
		require.NoError(b, err)
		inputs[k] = data
	}
	bs, err := DecodeBlobSet(inputs...)
	require.NoError(b, err)

	b.Run("DecodeBlobSet", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := DecodeBlobSet(inputs...); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("NumericMetricByName", func(b *testing.B) {
		var sink, k int
		b.ReportAllocs()
		for b.Loop() {
			h, _ := bs.NumericMetricByName(names[k%metrics])
			sink += h.Len()
			k++
		}
		if sink == 0 {
			b.Fatal("no handle resolved")
		}
	})

	b.Run("NumericMetricByName+Materialize", func(b *testing.B) {
		var sink, k int
		b.ReportAllocs()
		for b.Loop() {
			h, _ := bs.NumericMetricByName(names[k%metrics])
			h.Materialize()
			sink += h.Len()
			k++
		}
		if sink == 0 {
			b.Fatal("no handle resolved")
		}
	})

	b.Run("NumericMetricByName+TimestampsSorted", func(b *testing.B) {
		var sink, k int
		b.ReportAllocs()
		for b.Loop() {
			h, _ := bs.NumericMetricByName(names[k%metrics])
			if h.TimestampsSorted() {
				sink++
			}
			k++
		}
		if sink == 0 {
			b.Fatal("the fixture is sorted")
		}
	})
}

// BenchmarkNumericMetric_WindowWalk measures reading the values of a 600-point window of a day in 2880 blobs,
// with the window 10%, 50% and 90% of the way through the metric:
// ForEachValuesRange, and ForEachValues skipping to the window, which is the walk a caller had before it.
func BenchmarkNumericMetric_WindowWalk(b *testing.B) {
	bs := searchPassSet(b, false)
	h, ok := bs.NumericMetric(1)
	require.True(b, ok)
	n := h.Len()
	const window = 600
	for _, at := range []int{10, 50, 90} {
		start := n * at / 100
		end := start + window
		b.Run(strconv.Itoa(at)+"pct/ForEachValuesRange", func(b *testing.B) {
			var sink float64
			b.ReportAllocs()
			for b.Loop() {
				h.ForEachValuesRange(start, end, func(_ int, v float64) bool {
					sink += v

					return true
				})
			}
			if sink == -1 {
				b.Fatal("unreachable")
			}
		})
		b.Run(strconv.Itoa(at)+"pct/ForEachValues-skip", func(b *testing.B) {
			var sink float64
			b.ReportAllocs()
			for b.Loop() {
				h.ForEachValues(func(i int, v float64) bool {
					if i < start {
						return true
					}
					if i >= end {
						return false
					}
					sink += v

					return true
				})
			}
			if sink == -1 {
				b.Fatal("unreachable")
			}
		})
	}
}

// consumerSetName is the name of metric m of a consumer set started by name.
func consumerSetName(m int) string {
	return "svid." + strconv.Itoa(m)
}

// consumerSetID is the ID of metric m of a consumer set started by ID.
// The IDs are scattered like hashes, so the index order differs from the order the metrics were added in.
func consumerSetID(m int) uint64 {
	return uint64(m+1) * 0x9E3779B97F4A7C15
}

// consumerSetInputs encodes the blobs of c.
// The values are two-decimal numbers that change on about half the points,
// since a constant column would hide the cost of the value codec.
// It decodes the first blob and fails unless it has the layout, the tags, the absence of names
// and the number of shared timestamp groups that c asks for.
func consumerSetInputs(tb testing.TB, c consumerSet) [][]byte {
	tb.Helper()
	opts := []NumericEncoderOption{
		WithLittleEndian(), WithBlobLayoutV2(), WithSharedTimestamps(),
		WithTimestampEncoding(format.TypeDeltaPacked), WithTimestampCompression(format.CompressionNone),
		WithValueEncoding(c.valEnc), WithValueCompression(format.CompressionNone), WithTagsEnabled(c.tags),
	}
	ts := make([]int64, c.points)
	vals := make([]float64, c.points)
	// The tags are two bytes long: the tag payload is always zstd-compressed,
	// and longer tags would turn the rows with tags into a measurement of its decompression.
	var tags []string
	if c.tags {
		tags = make([]string, c.points)
		for i := range tags {
			tags[i] = "s" + strconv.Itoa(i%4)
		}
	}
	// Each metric walks in hundredths, which keeps its values exact two-decimal numbers.
	cents := make([]int64, c.metrics)
	for m := range cents {
		cents[m] = 10_000 + int64(m)*100
	}
	state := uint64(17)
	inputs := make([][]byte, c.blobs)
	for k := range inputs {
		start := handleTestStart + int64(k*c.points)*1_000_000
		enc, err := NewNumericEncoder(time.UnixMicro(start), opts...)
		require.NoError(tb, err)
		for m := range c.metrics {
			first := start + c.shift(m)
			for i := range ts {
				state = state*6364136223846793005 + 1442695040888963407
				if state>>63 == 0 {
					cents[m] += int64(state>>40)%201 - 100
				}
				ts[i] = first + int64(i)*1_000_000
				vals[i] = float64(cents[m]) / 100
			}
			if c.byName {
				require.NoError(tb, enc.StartMetricName(consumerSetName(m), c.points))
			} else {
				require.NoError(tb, enc.StartMetricID(consumerSetID(m), c.points))
			}
			require.NoError(tb, enc.AddDataPoints(ts, vals, tags))
			require.NoError(tb, enc.EndMetric())
		}
		inputs[k], err = enc.Finish()
		require.NoError(tb, err)
	}

	dec, err := NewNumericDecoder(inputs[0])
	require.NoError(tb, err)
	blob, err := dec.Decode()
	require.NoError(tb, err)
	require.True(tb, blob.IsV2Layout())
	require.False(tb, blob.HasMetricNames())
	require.Equal(tb, c.tags, blob.HasTag())
	groups := 0
	if blob.sharedTs != nil {
		groups = len(blob.sharedTs.groups)
	}
	require.Equal(tb, c.groups(), groups, "shared timestamp groups")

	return inputs
}

// BenchmarkNumericMetric_SetDecode measures DecodeBlobSet over blobs of 30 points encoded the way the consumer does,
// with the metrics started by name and no names stored, and reports the time per blob.
// The rows of 200 metrics decode 2880 blobs, a day at one point per second:
// one timestamp group for every metric with Chimp values, with tags beside them, and with ALP-RLE values;
// 100 groups of two metrics;
// and no two metrics alike, which leaves the blob without a shared group although the encoder looked for one.
// The rows of 255, 256, 257 and 1000 metrics in one group decode 288 blobs:
// the time is reported per blob, and a tenth of the blobs keeps the largest fixture as quick to build as the others.
// Each row builds its fixture inside its own sub-benchmark, so a filtered run builds only the fixtures it measures.
func BenchmarkNumericMetric_SetDecode(b *testing.B) {
	for _, tt := range []struct {
		name           string
		blobs, metrics int
		valEnc         format.EncodingType
		tags           bool
		sharing        consumerSetSharing
	}{
		{"Chimp", 2880, 200, format.TypeChimp, false, consumerSetOneGroup},
		{"Chimp-tags", 2880, 200, format.TypeChimp, true, consumerSetOneGroup},
		{"ALPRLE", 2880, 200, format.TypeALPRLE, false, consumerSetOneGroup},
		{"Chimp-100groups", 2880, 200, format.TypeChimp, false, consumerSetPairs},
		{"Chimp-distinct", 2880, 200, format.TypeChimp, false, consumerSetDistinct},
		{"Chimp-255metrics", 288, 255, format.TypeChimp, false, consumerSetOneGroup},
		{"Chimp-256metrics", 288, 256, format.TypeChimp, false, consumerSetOneGroup},
		{"Chimp-257metrics", 288, 257, format.TypeChimp, false, consumerSetOneGroup},
		{"Chimp-1000metrics", 288, 1000, format.TypeChimp, false, consumerSetOneGroup},
	} {
		b.Run(tt.name, func(b *testing.B) {
			inputs := consumerSetInputs(b, consumerSet{
				blobs: tt.blobs, metrics: tt.metrics, points: 30,
				valEnc: tt.valEnc, tags: tt.tags, sharing: tt.sharing, byName: true,
			})
			b.ReportAllocs()
			for b.Loop() {
				if _, err := DecodeBlobSet(inputs...); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(tt.blobs), "ns/blob")
		})
	}
}

// BenchmarkNumericMetric_SetForEach measures a walk over the points of one metric of a consumer set of 86,400 points,
// through the handle's ForEach, with the handle resolved once, and through ForEach on every blob in turn.
// The set holds 8 metrics started by ID, in 8640, 2880 or 576 blobs of 10, 30 or 150 points,
// with Chimp or ALP-RLE values, with the timestamps shared by every metric or distinct for each, and with or without tags.
// The full rows walk to the end.
// The first rows stop at the first point: the handle's walk ends there,
// and the walk over the blobs stops at the first point of each blob, since a blob's ForEach reports no stop.
// ns/point is the time per callback: per point in the full rows, per walk on the handle and per blob otherwise.
// Each fixture is built inside its own sub-benchmark, so a filtered run builds only the fixtures it measures.
func BenchmarkNumericMetric_SetForEach(b *testing.B) {
	const metrics, total = 8, 86_400
	id := consumerSetID(3)
	type row struct {
		name string
		set  consumerSet
	}
	var rows []row
	for _, enc := range []row{
		{"Chimp", consumerSet{valEnc: format.TypeChimp}},
		{"ALPRLE", consumerSet{valEnc: format.TypeALPRLE}},
	} {
		for _, points := range []int{10, 30, 150} {
			for _, sharing := range []row{
				{"shared", consumerSet{sharing: consumerSetOneGroup}},
				{"distinct", consumerSet{sharing: consumerSetDistinct}},
			} {
				for _, tags := range []row{{"no-tags", consumerSet{}}, {"with-tags", consumerSet{tags: true}}} {
					rows = append(rows, row{
						enc.name + "/" + strconv.Itoa(points) + "pts/" + sharing.name + "/" + tags.name,
						consumerSet{
							blobs: total / points, metrics: metrics, points: points,
							valEnc: enc.set.valEnc, tags: tags.set.tags, sharing: sharing.set.sharing,
						},
					})
				}
			}
		}
	}

	for _, r := range rows {
		b.Run(r.name, func(b *testing.B) {
			bs, err := DecodeBlobSet(consumerSetInputs(b, r.set)...)
			require.NoError(b, err)
			h, ok := bs.NumericMetric(id)
			require.True(b, ok)
			require.Equal(b, total, h.Len())
			blobs := bs.NumericBlobs()
			require.Len(b, blobs, r.set.blobs)

			for _, walk := range []struct {
				name                    string
				more                    bool
				handleCalls, blobsCalls int
			}{{"full", true, total, total}, {"first", false, 1, len(blobs)}} {
				more := walk.more
				b.Run("handle/"+walk.name, func(b *testing.B) {
					var sink int64
					b.ReportAllocs()
					for b.Loop() {
						h.ForEach(func(_ int, dp NumericDataPoint) bool {
							sink += dp.Ts + int64(dp.Val) + int64(len(dp.Tag))

							return more
						})
					}
					b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(walk.handleCalls), "ns/point")
					if sink == -1 {
						b.Fatal("unreachable")
					}
				})
				b.Run("blobs/"+walk.name, func(b *testing.B) {
					var sink int64
					b.ReportAllocs()
					for b.Loop() {
						for i := range blobs {
							blobs[i].ForEach(id, func(_ int, dp NumericDataPoint) bool {
								sink += dp.Ts + int64(dp.Val) + int64(len(dp.Tag))

								return more
							})
						}
					}
					b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(walk.blobsCalls), "ns/point")
					if sink == -1 {
						b.Fatal("unreachable")
					}
				})
			}
		})
	}
}

// BenchmarkNumericMetric_SetResolveUnnamed measures NumericMetricByName on a consumer set that stores no names:
// 120, 960 and 2880 blobs of 200 metrics started by name, 30 points each, in one timestamp group.
// The resolve rows time the resolution alone; both kinds of row report the time per blob.
// The window600 rows time what a windowed reader does with the handle:
// the resolution, two SearchTimestamp calls for the ends of a window in the middle of the metric,
// and a ForEachValuesRange over its 600 points.
// Every iteration resolves a different metric, so each resolution reads index entries the one before it did not.
// Each fixture is built inside its own sub-benchmark, so a filtered run builds only the fixtures it measures.
func BenchmarkNumericMetric_SetResolveUnnamed(b *testing.B) {
	const metrics, points, window = 200, 30, 600
	names := make([]string, metrics)
	for m := range names {
		names[m] = consumerSetName(m)
	}
	for _, blobs := range []int{120, 960, 2880} {
		b.Run(strconv.Itoa(blobs)+"blobs", func(b *testing.B) {
			bs, err := DecodeBlobSet(consumerSetInputs(b, consumerSet{
				blobs: blobs, metrics: metrics, points: points, valEnc: format.TypeChimp, byName: true,
			})...)
			require.NoError(b, err)
			h, ok := bs.NumericMetricByName(names[0])
			require.True(b, ok)
			n := h.Len()
			require.Equal(b, blobs*points, n)
			// Every metric has the same timestamps, so one pair of targets bounds the same window in each.
			from, _ := h.TimestampAt(n / 2)
			to, _ := h.TimestampAt(n/2 + window)
			lo, _ := h.SearchTimestamp(from)
			hi, _ := h.SearchTimestamp(to)
			require.Equal(b, window, hi-lo)

			b.Run("resolve", func(b *testing.B) {
				var sink, k int
				b.ReportAllocs()
				for b.Loop() {
					h, _ := bs.NumericMetricByName(names[k%metrics])
					sink += h.Len()
					k++
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(blobs), "ns/blob")
				if sink == 0 {
					b.Fatal("no handle resolved")
				}
			})

			b.Run("window600", func(b *testing.B) {
				var sink float64
				var k int
				b.ReportAllocs()
				for b.Loop() {
					h, _ := bs.NumericMetricByName(names[k%metrics])
					lo, _ := h.SearchTimestamp(from)
					hi, _ := h.SearchTimestamp(to)
					h.ForEachValuesRange(lo, hi, func(_ int, v float64) bool {
						sink += v

						return true
					})
					k++
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(blobs), "ns/blob")
				if sink == 0 {
					b.Fatal("no value read")
				}
			})
		})
	}
}

// shift is the offset in microseconds of the timestamps of metric m, which decides the columns it shares.
func (c consumerSet) shift(m int) int64 {
	if c.sharing == consumerSetPairs {
		return int64(m / 2)
	}
	if c.sharing == consumerSetDistinct {
		return int64(m)
	}

	return 0
}

// groups is the number of shared timestamp groups a decoded blob of the set holds.
func (c consumerSet) groups() int {
	if c.sharing == consumerSetPairs {
		return c.metrics / 2
	}
	if c.sharing == consumerSetDistinct {
		return 0
	}

	return 1
}
