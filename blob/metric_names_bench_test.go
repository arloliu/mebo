package blob

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// ==============================================================================
// Permanent metric-names benchmark suite.
//
// Pins three benchmark claims:
//   (a) default vs *Borrowed decode side by side — the borrowed path drops the
//       ~N name-string copies per decode, so it wins big while the default path
//       only wins modestly.
//   (b) GetByName / HasMetricName query cost on a names-bearing NO-collision blob:
//       the hash + binary-search/map + strcmp name-lookup path — reported with
//       ns/op and allocs to pin the "negligible" claim.
//   (c) blob-set construction + raw-set ID-keyed accessors (ValueAt / MetricLen /
//       All*) with names present but NO collision — asserting the lazily-built
//       identity table is not built and the accessors stay zero-alloc.
//
// Fixed corpora / shapes / metadata below make the numbers reproducible.
// ==============================================================================

// benchNameCorpus returns n deterministic, realistically-shaped metric names.
func benchNameCorpus(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("service.subsystem.metric.name.instance.%05d", i)
	}

	return names
}

// benchNamesNumericBlob builds an n-metric names-bearing V2 numeric blob (no
// collision), the reference decode shape. points is the per-metric point count.
func benchNamesNumericBlob(tb testing.TB, n, points int) []byte {
	tb.Helper()
	specs := make([]numericMetricSpec, n)
	for i, name := range benchNameCorpus(n) {
		specs[i] = numericMetricSpec{name: name, points: points, value: float64(i)}
	}

	return encodeNumericAt(tb, []NumericEncoderOption{WithBlobLayoutV2(), WithMetricNames()}, specs)
}

// benchNamesTextBlob builds an n-metric names-bearing text blob (no collision).
func benchNamesTextBlob(tb testing.TB, n, points int) []byte {
	tb.Helper()
	specs := make([]textMetricSpec, n)
	for i, name := range benchNameCorpus(n) {
		vals := make([]string, points)
		for j := range vals {
			vals[j] = fmt.Sprintf("v%d", j)
		}
		specs[i] = textMetricSpec{name: name, values: vals}
	}

	return encodeTextAt(tb, nil, specs)
}

// benchBlobShapes is the fixed set of (metric-count, points) shapes benchmarked.
var benchBlobShapes = []struct {
	name    string
	metrics int
	points  int
}{
	{name: "10m_100p", metrics: 10, points: 100},
	{name: "200m_10p", metrics: 200, points: 10},
}

// logBenchEnv records toolchain/CPU metadata (visible with `go test -v`) so the
// committed numbers are attributable to an environment.
func logBenchEnv(b *testing.B) {
	b.Helper()
	b.Logf("toolchain=%s GOOS=%s GOARCH=%s NumCPU=%d GOMAXPROCS=%d",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0))
}

// BenchmarkMetricNames_Decode measures default (owning) vs borrowed decode,
// side by side, for both numeric and text blobs across the fixed shapes. The
// gap is the name-copy cost the borrowed path eliminates.
func BenchmarkMetricNames_Decode(b *testing.B) {
	logBenchEnv(b)

	for _, shape := range benchBlobShapes {
		numData := benchNamesNumericBlob(b, shape.metrics, shape.points)
		txtData := benchNamesTextBlob(b, shape.metrics, shape.points)

		b.Run("numeric/"+shape.name+"/owning", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				dec, err := NewNumericDecoder(numData)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := dec.Decode(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("numeric/"+shape.name+"/borrowed", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				dec, err := NewNumericDecoderBorrowed(numData)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := dec.Decode(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("text/"+shape.name+"/owning", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				dec, err := NewTextDecoder(txtData)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := dec.Decode(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("text/"+shape.name+"/borrowed", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				dec, err := NewTextDecoderBorrowed(txtData)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := dec.Decode(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMetricNames_Query measures GetByName / HasMetricName query cost on a
// names-bearing NO-collision blob (the hash + binary-search + strcmp name-lookup
// path). Reports ns/op and allocs to pin the "negligible" claim.
func BenchmarkMetricNames_Query(b *testing.B) {
	data := benchNamesNumericBlob(b, 200, 10)
	dec, err := NewNumericDecoder(data)
	require.NoError(b, err)
	blob, err := dec.Decode()
	require.NoError(b, err)
	require.Nil(b, blob.index.byName, "fixture must be a no-collision retained-names blob")

	names := benchNameCorpus(200)
	hit := names[123] // a stored name (found path)
	miss := "no.such.metric.name.absent"

	b.Run("HasMetricName/hit", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if !blob.HasMetricName(hit) {
				b.Fatal("expected hit")
			}
		}
	})
	b.Run("HasMetricName/miss", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if blob.HasMetricName(miss) {
				b.Fatal("expected miss")
			}
		}
	})
	b.Run("GetByName/hit", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, ok := blob.index.GetByName(hit); !ok {
				b.Fatal("expected hit")
			}
		}
	})
}

// BenchmarkMetricNames_AccessByIDVsByName measures All()/ValueAt() by
// MetricID vs the ByName equivalents on a names-bearing NO-collision blob.
// The two paths resolve to the same index entry and share the identical
// per-point decode work afterward (allFromEntry) — only entry resolution
// differs (GetByID vs GetByName's extra hash + string-compare) — so the
// delta here is a fixed per-call cost, not something that scales with point
// count or iteration length.
func BenchmarkMetricNames_AccessByIDVsByName(b *testing.B) {
	logBenchEnv(b)

	for _, shape := range benchBlobShapes {
		data := benchNamesNumericBlob(b, shape.metrics, shape.points)
		dec, err := NewNumericDecoder(data)
		require.NoError(b, err)
		blob, err := dec.Decode()
		require.NoError(b, err)
		require.Nil(b, blob.index.byName, "fixture must be a no-collision retained-names blob")

		names := benchNameCorpus(shape.metrics)
		name := names[shape.metrics/2]
		entry, ok := blob.index.GetByName(name)
		require.True(b, ok)
		metricID := entry.MetricID

		b.Run(shape.name+"/All/byID", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				var sum float64
				for _, dp := range blob.All(metricID) {
					sum += dp.Val
				}
				_ = sum
			}
		})
		b.Run(shape.name+"/All/byName", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				var sum float64
				for _, dp := range blob.AllByName(name) {
					sum += dp.Val
				}
				_ = sum
			}
		})
		b.Run(shape.name+"/ValueAt/byID", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, _ = blob.ValueAt(metricID, shape.points/2)
			}
		})
		b.Run(shape.name+"/ValueAt/byName", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, _ = blob.ValueAtByName(name, shape.points/2)
			}
		})
	}
}

// TestMetricNames_Query_ZeroAlloc converts the "negligible" allocation claim
// BenchmarkMetricNames_Query only reports into an enforced assertion:
// GetByName and HasMetricName (hit and miss) on a names-bearing NO-collision
// blob must not allocate. A benchmark can regress this without failing CI;
// this test cannot. Skipped under -race (sync.Pool bookkeeping perturbs
// allocs, mirroring the other alloc assertions in this package).
func TestMetricNames_Query_ZeroAlloc(t *testing.T) {
	if raceEnabled {
		t.Skip("alloc assertions are unstable under -race")
	}

	data := benchNamesNumericBlob(t, 200, 10)
	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)
	require.Nil(t, blob.index.byName, "fixture must be a no-collision retained-names blob")

	names := benchNameCorpus(200)
	hit := names[123] // a stored name (found path)
	miss := "no.such.metric.name.absent"

	hitAllocs := testing.AllocsPerRun(200, func() {
		if !blob.HasMetricName(hit) {
			t.Fatal("expected hit")
		}
	})
	require.Zero(t, hitAllocs, "HasMetricName hit on a no-collision blob must be zero-alloc")

	missAllocs := testing.AllocsPerRun(200, func() {
		if blob.HasMetricName(miss) {
			t.Fatal("expected miss")
		}
	})
	require.Zero(t, missAllocs, "HasMetricName miss on a no-collision blob must be zero-alloc")

	getByNameAllocs := testing.AllocsPerRun(200, func() {
		if _, ok := blob.index.GetByName(hit); !ok {
			t.Fatal("expected hit")
		}
	})
	require.Zero(t, getByNameAllocs, "GetByName hit on a no-collision blob must be zero-alloc")
}

// benchNoCollisionNumericSet builds a 3-member names-bearing set sharing the same
// metric names across members (a cross-window merge, NOT a collision), so the
// lazy identity-table build is probed but no identity table is built.
func benchNoCollisionNumericSet(tb testing.TB, n int) NumericBlobSet {
	tb.Helper()
	names := benchNameCorpus(n)
	blobs := make([]NumericBlob, 3)
	for m := range blobs {
		specs := make([]numericMetricSpec, n)
		for i, name := range names {
			specs[i] = numericMetricSpec{name: name, points: 10, value: float64(i + m)}
		}
		data := encodeNumericAt(tb, []NumericEncoderOption{WithBlobLayoutV2(), WithMetricNames()}, specs)
		dec, err := NewNumericDecoder(data)
		require.NoError(tb, err)
		blob, err := dec.Decode()
		require.NoError(tb, err)
		blobs[m] = blob
	}
	set, err := NewNumericBlobSet(blobs)
	require.NoError(tb, err)

	return set
}

// BenchmarkMetricNames_SetConstruction measures blob-set construction with
// names present but no collision. Construction must not build an identity table.
func BenchmarkMetricNames_SetConstruction(b *testing.B) {
	names := benchNameCorpus(200)
	blobs := make([]NumericBlob, 3)
	for m := range blobs {
		specs := make([]numericMetricSpec, 200)
		for i, name := range names {
			specs[i] = numericMetricSpec{name: name, points: 10, value: float64(i + m)}
		}
		data := encodeNumericAt(b, []NumericEncoderOption{WithBlobLayoutV2(), WithMetricNames()}, specs)
		dec, err := NewNumericDecoder(data)
		require.NoError(b, err)
		blob, err := dec.Decode()
		require.NoError(b, err)
		blobs[m] = blob
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		set, err := NewNumericBlobSet(blobs)
		if err != nil {
			b.Fatal(err)
		}
		if set.identity != nil {
			b.Fatal("no-collision set must not build an identity table")
		}
	}
}

// BenchmarkMetricNames_SetAccessors measures raw-set ID-keyed accessors on a
// no-collision names-bearing set (direct per-member path, no identity table).
func BenchmarkMetricNames_SetAccessors(b *testing.B) {
	set := benchNoCollisionNumericSet(b, 200)
	require.Nil(b, set.identity, "fixture set must have no identity table")
	ids := set.MetricIDs()
	id := ids[100]

	b.Run("ValueAt", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_, _ = set.ValueAt(id, 5)
		}
	})
	b.Run("MetricLen", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_ = set.MetricLen(id)
		}
	})
	b.Run("AllValues", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			var sum float64
			for v := range set.AllValues(id) {
				sum += v
			}
			_ = sum
		}
	})
}

// TestSetAccessors_ZeroAlloc pins the claim that on a no-collision
// names-bearing set the identity table is nil and the raw ID-keyed accessors add
// no allocations. Skipped under -race (sync.Pool bookkeeping perturbs allocs).
func TestSetAccessors_ZeroAlloc(t *testing.T) {
	if raceEnabled {
		t.Skip("alloc assertions are unstable under -race")
	}

	set := benchNoCollisionNumericSet(t, 100)
	require.Nil(t, set.identity, "no-collision set must not build an identity table")
	id := set.MetricIDs()[50]

	valAllocs := testing.AllocsPerRun(200, func() {
		_, _ = set.ValueAt(id, 3)
	})
	require.Zero(t, valAllocs, "ValueAt on a no-collision set must be zero-alloc")

	lenAllocs := testing.AllocsPerRun(200, func() {
		_ = set.MetricLen(id)
	})
	require.Zero(t, lenAllocs, "MetricLen on a no-collision set must be zero-alloc")
}

// ==============================================================================
// Encode/decode/materialize overhead of turning names on, isolated from the
// owning-vs-borrowed and size numbers above. Three further claims:
//   (d) encode cost of WithMetricNames() (numeric) / the text default vs
//       WithoutMetricNames() — the marginal cost on top of Name-mode encoding,
//       which already computes hash.ID(name) either way.
//   (e) decode cost of a names-bearing blob vs a names-free one, same (owning)
//       decoder — isolates "the cost of names" from the owning/borrowed choice
//       BenchmarkMetricNames_Decode measures.
//   (f) Materialize() cost, names-bearing vs names-free.
// ==============================================================================

// encodeNumericNamed encodes n metrics of points points each, either storing
// names (WithMetricNames()) or not (plain Name-mode default), same shape as
// benchNamesNumericBlob.
func encodeNumericNamed(tb testing.TB, names []string, points int, withNames bool) []byte {
	tb.Helper()
	opts := []NumericEncoderOption{WithBlobLayoutV2()}
	if withNames {
		opts = append(opts, WithMetricNames())
	}
	specs := make([]numericMetricSpec, len(names))
	for i, name := range names {
		specs[i] = numericMetricSpec{name: name, points: points, value: float64(i)}
	}

	return encodeNumericAt(tb, opts, specs)
}

// encodeTextNamed encodes len(names) text metrics of points points each,
// either storing names (the text default) or not (WithoutMetricNames()).
func encodeTextNamed(tb testing.TB, names []string, points int, withNames bool) []byte {
	tb.Helper()
	var opts []TextEncoderOption
	if !withNames {
		opts = append(opts, WithoutMetricNames())
	}
	specs := make([]textMetricSpec, len(names))
	for i, name := range names {
		vals := make([]string, points)
		for j := range vals {
			vals[j] = fmt.Sprintf("v%d", j)
		}
		specs[i] = textMetricSpec{name: name, values: vals}
	}

	return encodeTextAt(tb, opts, specs)
}

// BenchmarkMetricNames_EncodeOverhead measures the encode-side marginal cost
// of WithMetricNames() (numeric) / the text default vs WithoutMetricNames().
func BenchmarkMetricNames_EncodeOverhead(b *testing.B) {
	logBenchEnv(b)

	for _, shape := range benchBlobShapes {
		names := benchNameCorpus(shape.metrics)

		b.Run("numeric/"+shape.name+"/no_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = encodeNumericNamed(b, names, shape.points, false)
			}
		})
		b.Run("numeric/"+shape.name+"/with_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = encodeNumericNamed(b, names, shape.points, true)
			}
		})
		b.Run("text/"+shape.name+"/no_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = encodeTextNamed(b, names, shape.points, false)
			}
		})
		b.Run("text/"+shape.name+"/with_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = encodeTextNamed(b, names, shape.points, true)
			}
		})
	}
}

// BenchmarkMetricNames_DecodeVsNoNames measures the decode-time cost of
// carrying names, same (owning) decoder, names-bearing vs names-free —
// isolating the names cost from BenchmarkMetricNames_Decode's owning-vs-
// borrowed comparison.
func BenchmarkMetricNames_DecodeVsNoNames(b *testing.B) {
	logBenchEnv(b)

	for _, shape := range benchBlobShapes {
		names := benchNameCorpus(shape.metrics)
		numNoNames := encodeNumericNamed(b, names, shape.points, false)
		numWithNames := encodeNumericNamed(b, names, shape.points, true)
		txtNoNames := encodeTextNamed(b, names, shape.points, false)
		txtWithNames := encodeTextNamed(b, names, shape.points, true)

		b.Run("numeric/"+shape.name+"/no_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				dec, err := NewNumericDecoder(numNoNames)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := dec.Decode(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("numeric/"+shape.name+"/with_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				dec, err := NewNumericDecoder(numWithNames)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := dec.Decode(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("text/"+shape.name+"/no_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				dec, err := NewTextDecoder(txtNoNames)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := dec.Decode(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("text/"+shape.name+"/with_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				dec, err := NewTextDecoder(txtWithNames)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := dec.Decode(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMetricNames_Materialize measures Materialize() cost, names-bearing
// vs names-free, for both blob types.
func BenchmarkMetricNames_Materialize(b *testing.B) {
	logBenchEnv(b)

	for _, shape := range benchBlobShapes {
		names := benchNameCorpus(shape.metrics)

		numNoNamesData := encodeNumericNamed(b, names, shape.points, false)
		numDec, err := NewNumericDecoder(numNoNamesData)
		require.NoError(b, err)
		numNoNames, err := numDec.Decode()
		require.NoError(b, err)

		numWithNamesData := encodeNumericNamed(b, names, shape.points, true)
		numDec2, err := NewNumericDecoder(numWithNamesData)
		require.NoError(b, err)
		numWithNames, err := numDec2.Decode()
		require.NoError(b, err)

		txtNoNamesData := encodeTextNamed(b, names, shape.points, false)
		txtDec, err := NewTextDecoder(txtNoNamesData)
		require.NoError(b, err)
		txtNoNames, err := txtDec.Decode()
		require.NoError(b, err)

		txtWithNamesData := encodeTextNamed(b, names, shape.points, true)
		txtDec2, err := NewTextDecoder(txtWithNamesData)
		require.NoError(b, err)
		txtWithNames, err := txtDec2.Decode()
		require.NoError(b, err)

		b.Run("numeric/"+shape.name+"/no_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = numNoNames.Materialize()
			}
		})
		b.Run("numeric/"+shape.name+"/with_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = numWithNames.Materialize()
			}
		})
		b.Run("text/"+shape.name+"/no_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = txtNoNames.Materialize()
			}
		})
		b.Run("text/"+shape.name+"/with_names", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = txtWithNames.Materialize()
			}
		})
	}
}
