package blob

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/format"
	ienc "github.com/arloliu/mebo/internal/encoding"
)

// setMaterialFreshMin is the number of distinct points a timed row walks before it sees a point again,
// more than a branch predictor can memorize.
const setMaterialFreshMin = 960_000

// The kinds of value column the set fixtures generate.
const (
	setKindGauge     setValueKind = iota // two-decimal gauge that repeats its previous value 30% of the time
	setKindCounter                       // integer counter that grows by 1 to 10 per point
	setKindNearConst                     // two-decimal value with a 5% chance per point of a step under 1
	setKindFullPrec                      // full-precision gauge
	setKindConstant                      // one value repeated
)

// setValueKind is the shape of a generated value column.
type setValueKind int

// setFixture describes a set of blobs in the shape the production consumer writes:
// little endian, V2 layout, shared DeltaPacked timestamps, no compression,
// metrics started by name without stored names, one point a second.
type setFixture struct {
	id        int // the fixture's number within its row: every column's seed and start depend on it
	blobs     int
	metrics   int
	minPoints int // each blob draws one count in [minPoints, maxPoints] for all its metrics
	maxPoints int
	encoding  func(blob int) format.EncodingType
	tags      bool // two-byte tags
	kind      func(metric int) setValueKind
	// blobKind, when set, gives the kind of metric m in blob k instead of kind, for metrics that change kind with the blob.
	blobKind func(blob, metric int) setValueKind
}

// setMaterialRow is the work of one benchmark row: operation p materializes metric names[p/len(sets)] of set sets[p%len(sets)],
// so the row visits every pair of set and metric once in len(sets)*len(names) operations before it repeats.
type setMaterialRow struct {
	sets  []BlobSet
	names []string
}

// setMaterialBenchRow names a row of BenchmarkNumericBlobSet_MaterializeMetricByName and builds its work.
type setMaterialBenchRow struct {
	name  string
	build func(b *testing.B) setMaterialRow
}

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

// createSparseEncodedBlobs creates blobs where each blob covers only a subset of the total
// metrics (metricsPerBlob out of totalMetrics), cycling through the full set across blobs.
// This simulates the common scenario where metrics are not uniformly present in every blob.
func createSparseEncodedBlobs(
	numBlobs int,
	totalMetrics int,
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

		startMetric := (blobIdx * metricsPerBlob) % totalMetrics
		for j := range metricsPerBlob {
			m := (startMetric + j) % totalMetrics
			metricName := "metric_" + string(rune('A'+m%26)) + "_" + string(rune('0'+m/26))

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

// BenchmarkNumericBlobSetMaterialize_Sparse measures materialization performance when
// different blobs cover different metric subsets (sparse coverage scenario).
func BenchmarkNumericBlobSetMaterialize_Sparse(b *testing.B) {
	benchmarks := []struct {
		name            string
		numBlobs        int
		totalMetrics    int
		metricsPerBlob  int
		pointsPerMetric int
		tsEncoding      format.EncodingType
		valEncoding     format.EncodingType
	}{
		{"20Blobs_150Total_10PerBlob_Delta", 20, 150, 10, 100, format.TypeDelta, format.TypeGorilla},
		{"20Blobs_150Total_30PerBlob_Delta", 20, 150, 30, 100, format.TypeDelta, format.TypeGorilla},
		{"10Blobs_50Total_10PerBlob_Delta", 10, 50, 10, 100, format.TypeDelta, format.TypeGorilla},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			blobs, err := createSparseEncodedBlobs(
				bm.numBlobs,
				bm.totalMetrics,
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

// BenchmarkNumericBlobSet_MaterializeMetricByName measures BlobSet.MaterializeNumericMetricByName
// on sets in the production consumer's shape, one metric of one set per operation.
// Every row walks at least 960,000 distinct points before it repeats one, so the timing is not that of data
// a branch predictor has learned, and it builds its fixtures inside its own sub-benchmark,
// so a filtered run builds only the fixtures it measures.
// It reports ns/point, the time per materialized point, and kernel-columns/column,
// the share of the row's value columns that the Chimp group decode takes into its kernel.
// The share is counted in a pass of its own once the timed loop has ended and the timer has stopped,
// so every revision of this benchmark starts its timed loop from the same state.
// The consumer row fails when the kernel can run and takes no column, so it cannot time an idle kernel unnoticed.
func BenchmarkNumericBlobSet_MaterializeMetricByName(b *testing.B) {
	whole := func(opts ...func(*setFixture)) func(*testing.B) setMaterialRow {
		return func(b *testing.B) setMaterialRow { return wholeSetRow(b, consumerSetFixture(opts...)) }
	}
	kindOnly := func(kind setValueKind) func(*setFixture) {
		return func(f *setFixture) { f.kind = func(int) setValueKind { return kind } }
	}
	fixedPoints := func(n int) func(*setFixture) {
		return func(f *setFixture) { f.minPoints, f.maxPoints = n, n }
	}
	// quietWhere makes every metric constant in the blobs where quiet holds and a gauge in the others,
	// so a metric's columns are too sparse for the kernel in those blobs and dense enough between them.
	quietWhere := func(quiet func(blob int) bool) func(*setFixture) {
		return func(f *setFixture) {
			f.blobKind = func(k, _ int) setValueKind {
				if quiet(k) {
					return setKindConstant
				}

				return setKindGauge
			}
		}
	}
	alpRLEWhere := func(blobs int, alpRLE func(blob int) bool) func(*setFixture) {
		return func(f *setFixture) {
			f.blobs = blobs
			f.encoding = func(k int) format.EncodingType {
				if alpRLE(k) {
					return format.TypeALPRLE
				}

				return format.TypeChimp
			}
		}
	}
	rows := []setMaterialBenchRow{
		{"consumer", whole()},
		{"gauge", whole(kindOnly(setKindGauge))},
		{"counter", whole(kindOnly(setKindCounter))},
		{"fullprec", whole(kindOnly(setKindFullPrec))},
		{"nearconst", whole(kindOnly(setKindNearConst))},
		{"constant", whole(kindOnly(setKindConstant))},
		{"lastcolumn", lastColumnRow},
		{"equalcounts", whole(fixedPoints(35))},
		{"points=10", whole(fixedPoints(10))},
		{"points=150", whole(fixedPoints(150))},
	}
	for _, k := range []int{1, 2, 3, 4, 7, 8, 9, 16, 64} {
		rows = append(rows, setMaterialBenchRow{"blobs=" + strconv.Itoa(k), func(b *testing.B) setMaterialRow { return cutSetRow(b, k) }})
	}
	rows = append(rows,
		setMaterialBenchRow{"mixed=alternate", whole(alpRLEWhere(1000, func(k int) bool { return k%2 == 1 }))},
		setMaterialBenchRow{"mixed=1of8", whole(alpRLEWhere(1000, func(k int) bool { return k%8 == 7 }))},
		setMaterialBenchRow{"mixed=7of8", whole(alpRLEWhere(2400, func(k int) bool { return k%8 != 0 }))},
		setMaterialBenchRow{"alprle", whole(alpRLEWhere(1000, func(int) bool { return true }))},
		setMaterialBenchRow{"tags", whole(func(f *setFixture) { f.tags = true })},
		setMaterialBenchRow{"quiet=start", whole(quietWhere(func(k int) bool { return k < 16 }))},
		setMaterialBenchRow{"quiet=spells", whole(quietWhere(func(k int) bool { return k%250 < 16 }))},
	)

	for _, row := range rows {
		b.Run(row.name, func(b *testing.B) {
			work := row.build(b)
			work.check(b)
			period := len(work.sets) * len(work.names)

			b.ReportAllocs()
			p, points := 0, 0
			for b.Loop() {
				s, m := work.pair(p)
				metric, ok := work.sets[s].MaterializeNumericMetricByName(work.names[m])
				if !ok {
					b.Fatalf("operation %d: metric %s not found", p, work.names[m])
				}
				points += len(metric.Values)
				if p++; p == period {
					p = 0
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(points), "ns/point")

			// b.Loop stopped the timer when it returned false: this pass adds no time and no allocation to the row.
			kernelShare := work.kernelColumnShare(b)
			if row.name == "consumer" && ienc.ChimpGroupDecodeEnabled() && kernelShare == 0 {
				b.Fatal("the group decode kernel can run on this machine and took no column")
			}
			b.ReportMetric(kernelShare, "kernel-columns/column")
		})
	}
}

// setMixKind returns the kind of metric m in the monitoring mix:
// out of every 100 metrics, 35 gauges, 20 counters, 18 near-constant and 27 full-precision gauges.
func setMixKind(m int) setValueKind {
	switch slot := m % 100; {
	case slot < 35:
		return setKindGauge
	case slot < 55:
		return setKindCounter
	case slot < 73:
		return setKindNearConst
	default:
		return setKindFullPrec
	}
}

// setValueColumn generates n values of the given kind from start, drawing from a generator seeded with seed.
// It follows the value kinds of the chimp package's benchmark mix,
// except that the caller chooses the start, so no two columns need to begin alike.
func setValueColumn(kind setValueKind, n int, start float64, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	cur := start
	col := make([]float64, n)
	for j := range col {
		switch kind {
		case setKindGauge:
			if j > 0 && rng.Float64() < 0.3 {
				col[j] = col[j-1]

				continue
			}
			cur += cur * (rng.Float64()*2 - 1) * 0.005
			col[j] = math.Round(cur*100) / 100
		case setKindCounter:
			cur += math.Floor(rng.Float64()*10) + 1
			col[j] = cur
		case setKindNearConst:
			if rng.Float64() < 0.05 {
				cur += rng.Float64()*2 - 1
			}
			col[j] = math.Round(cur*100) / 100
		case setKindFullPrec:
			cur += cur * (rng.Float64()*2 - 1) * 0.005
			col[j] = cur
		case setKindConstant:
			col[j] = cur
		default:
			panic(fmt.Sprintf("unknown value kind %d", kind))
		}
	}

	return col
}

// setFixtureName is the name of metric m of a fixture.
func setFixtureName(m int) string {
	return fmt.Sprintf("set.metric.%03d", m)
}

// setFixtureColumn returns the values of metric m in blob k of fixture f, n points long.
// Each column has a seed and a start of its own, so no two columns of a row hold the same values.
// Counters start at distinct integers, and near-constant columns at even integers,
// so a step under 1 cannot turn one column's first value into another's;
// every other kind starts at a distinct multiple of 0.01.
func setFixtureColumn(f setFixture, k, m, n int) []float64 {
	u := (f.id*f.blobs+k)*f.metrics + m
	kind := f.kind(m)
	if f.blobKind != nil {
		kind = f.blobKind(k, m)
	}
	start := 100 + float64(u)/100
	switch kind {
	case setKindCounter:
		start = 100 + float64(u)
	case setKindNearConst:
		start = 100 + 2*float64(u)
	case setKindGauge, setKindFullPrec, setKindConstant:
	default:
		panic(fmt.Sprintf("unknown value kind %d", kind))
	}

	return setValueColumn(kind, n, start, int64(u)*7919+1)
}

// buildSetFixture encodes the blobs of f and decodes them with DecodeBlobSet.
// It fails unless the first and last members have the layout, the value encoding and no names payload,
// and hand their first and last metric a shared timestamp group of Count timestamps.
func buildSetFixture(tb testing.TB, f setFixture) BlobSet {
	tb.Helper()
	counts := rand.New(rand.NewSource(int64(f.id) + 1))
	base := time.Unix(1_700_000_000, 0).UTC()
	inputs := make([][]byte, f.blobs)
	for k := range inputs {
		n := f.minPoints + counts.Intn(f.maxPoints-f.minPoints+1)
		start := base.Add(time.Duration(k) * time.Minute)
		enc, err := NewNumericEncoder(start,
			WithLittleEndian(), WithBlobLayoutV2(), WithSharedTimestamps(),
			WithTimestampEncoding(format.TypeDeltaPacked), WithTimestampCompression(format.CompressionNone),
			WithValueEncoding(f.encoding(k)), WithValueCompression(format.CompressionNone), WithTagsEnabled(f.tags))
		require.NoError(tb, err)
		ts := make([]int64, n)
		var tags []string
		if f.tags {
			tags = make([]string, n)
		}
		for j := range ts {
			ts[j] = start.UnixMicro() + int64(j)*1_000_000
			if f.tags {
				tags[j] = string([]byte{'a' + byte(j%26), '0' + byte(k%10)})
			}
		}
		for m := range f.metrics {
			require.NoError(tb, enc.StartMetricName(setFixtureName(m), n))
			require.NoError(tb, enc.AddDataPoints(ts, setFixtureColumn(f, k, m, n), tags))
			require.NoError(tb, enc.EndMetric())
		}
		inputs[k], err = enc.Finish()
		require.NoError(tb, err)
	}

	set, err := DecodeBlobSet(inputs...)
	require.NoError(tb, err)
	members := set.NumericBlobs()
	require.Len(tb, members, f.blobs)
	for _, k := range []int{0, f.blobs - 1} {
		member := &members[k]
		require.True(tb, member.IsV2Layout(), "member %d: layout", k)
		require.True(tb, member.IsLittleEndian(), "member %d: byte order", k)
		require.Equal(tb, f.encoding(k), member.ValueEncoding(), "member %d: value encoding", k)
		require.False(tb, member.HasMetricNames(), "member %d: names payload", k)
		for _, m := range []int{0, f.metrics - 1} {
			entry, ok := member.index.GetByName(setFixtureName(m))
			require.True(tb, ok, "member %d: metric %d", k, m)
			require.Len(tb, member.sharedTs.lookup(entry.TimestampOffset), entry.Count,
				"member %d: shared timestamps of metric %d", k, m)
		}
	}

	return set
}

// shuffledSetNames returns the names of metrics 0 to n-1 in a fixed shuffled order.
func shuffledSetNames(n int) []string {
	names := make([]string, n)
	for i, m := range rand.New(rand.NewSource(int64(n))).Perm(n) {
		names[i] = setFixtureName(m)
	}

	return names
}

// consumerSetFixture is 1,000 Chimp blobs of 100 metrics in the mix, 30 to 40 points each,
// changed by the options given.
func consumerSetFixture(opts ...func(*setFixture)) setFixture {
	f := setFixture{
		blobs: 1000, metrics: 100, minPoints: 30, maxPoints: 40,
		encoding: func(int) format.EncodingType { return format.TypeChimp },
		kind:     setMixKind,
	}
	for _, opt := range opts {
		opt(&f)
	}

	return f
}

// wholeSetRow materializes each of the fixture's metrics from one set holding all of its blobs.
func wholeSetRow(b *testing.B, f setFixture) setMaterialRow {
	b.Helper()

	return setMaterialRow{sets: []BlobSet{buildSetFixture(b, f)}, names: shuffledSetNames(f.metrics)}
}

// lastColumnRow builds 30 fixtures of 1,000 Chimp blobs with four full-precision metrics
// and materializes, from each, the metric whose column ends every value payload.
func lastColumnRow(b *testing.B) setMaterialRow {
	b.Helper()
	row := setMaterialRow{sets: make([]BlobSet, 30)}
	for id := range row.sets {
		row.sets[id] = buildSetFixture(b, consumerSetFixture(func(f *setFixture) {
			f.id, f.metrics = id, 4
			f.kind = func(int) setValueKind { return setKindFullPrec }
		}))
	}
	first := &row.sets[0].numericBlobs[0]
	for m := range 4 {
		entry, ok := first.index.GetByName(setFixtureName(m))
		require.True(b, ok)
		if entry.ValueOffset+entry.ValueLength == len(first.valPayload) {
			row.names = []string{setFixtureName(m)}
		}
	}
	require.Len(b, row.names, 1, "a metric ends the first value payload")
	for s := range row.sets {
		for i := range row.sets[s].numericBlobs {
			member := &row.sets[s].numericBlobs[i]
			entry, ok := member.index.GetByName(row.names[0])
			require.True(b, ok)
			require.Equal(b, len(member.valPayload), entry.ValueOffset+entry.ValueLength,
				"set %d, member %d: the metric ends the value payload", s, i)
			require.GreaterOrEqual(b, entry.Count, 2, "set %d, member %d: points of the last column", s, i)
		}
	}

	return row
}

// cutSetRow cuts the consumer fixture into sets of k consecutive blobs, as many as fit.
func cutSetRow(b *testing.B, k int) setMaterialRow {
	b.Helper()
	members := buildSetFixture(b, consumerSetFixture()).NumericBlobs()
	row := setMaterialRow{sets: make([]BlobSet, len(members)/k), names: shuffledSetNames(100)}
	for s := range row.sets {
		row.sets[s] = NewBlobSet(members[s*k:(s+1)*k], nil)
	}

	return row
}

// pair returns the indexes of the set and the metric of operation p, for p below the row's period.
func (r *setMaterialRow) pair(p int) (set, metric int) {
	return p % len(r.sets), p / len(r.sets)
}

// check walks one period of the row and fails unless it visits every pair of set and metric exactly once,
// walks at least setMaterialFreshMin points (Chimp points when the row has Chimp members),
// and no two visited columns have the same encoded bytes.
func (r *setMaterialRow) check(b *testing.B) {
	b.Helper()
	period := len(r.sets) * len(r.names)
	visits := make([]int, period)
	columns := make(map[string]struct{})
	points, chimpPoints := 0, 0
	for p := range period {
		s, m := r.pair(p)
		visits[m*len(r.sets)+s]++
		name := r.names[m]
		for i := range r.sets[s].numericBlobs {
			member := &r.sets[s].numericBlobs[i]
			entry, ok := member.index.GetByName(name)
			if !ok {
				continue
			}
			points += entry.Count
			if member.ValueEncoding() == format.TypeChimp {
				chimpPoints += entry.Count
			}
			column := string(member.valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength])
			_, dup := columns[column]
			require.False(b, dup, "set %d, member %d: %s holds the same value bytes as another column of the row", s, i, name)
			columns[column] = struct{}{}
		}
	}
	for pair, n := range visits {
		require.Equal(b, 1, n, "visits of set %d, metric %d", pair%len(r.sets), pair/len(r.sets))
	}
	if chimpPoints > 0 {
		require.GreaterOrEqual(b, chimpPoints, setMaterialFreshMin, "Chimp points walked in one period")
	} else {
		require.GreaterOrEqual(b, points, setMaterialFreshMin, "points walked in one period")
	}
	b.Logf("%d sets, %d metrics, %d distinct columns, %d points, %d Chimp points",
		len(r.sets), len(r.names), len(columns), points, chimpPoints)
}

// kernelColumnShare materializes one period of the row and returns the share of the value columns it visits
// that the Chimp group decode took into its kernel, as chimpGroupObserve reports them.
// It is 0 where the kernel cannot run, and for a row whose columns never enter it.
func (r *setMaterialRow) kernelColumnShare(b *testing.B) float64 {
	b.Helper()
	kernelColumns, columns := 0, 0
	chimpGroupObserve = func(_, _, taken, _, _ int) { kernelColumns += taken }
	defer func() { chimpGroupObserve = nil }()
	for p := range len(r.sets) * len(r.names) {
		s, m := r.pair(p)
		_, ok := r.sets[s].MaterializeNumericMetricByName(r.names[m])
		require.True(b, ok, "set %d: metric %s", s, r.names[m])
		for i := range r.sets[s].numericBlobs {
			if _, ok := r.sets[s].numericBlobs[i].index.GetByName(r.names[m]); ok {
				columns++
			}
		}
	}

	return float64(kernelColumns) / float64(columns)
}
