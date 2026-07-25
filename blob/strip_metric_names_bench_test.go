package blob

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// benchStripBlob builds a 200-metric names-bearing V2 blob (no collision), the
// reference shape used to benchmark strip's cost.
func benchStripBlob(tb testing.TB) []byte {
	tb.Helper()
	metrics := make([]numericMetricSpec, 200)
	for i := range metrics {
		metrics[i] = numericMetricSpec{name: fmt.Sprintf("service.metric.name.%04d", i), points: 10, value: float64(i)}
	}

	return encodeNumericAt(tb, []NumericEncoderOption{WithBlobLayoutV2(), WithMetricNames()}, metrics)
}

// TestStrip_Validate_ZeroAllocs pins the "walk + re-hash + collision scan"
// validation pass to zero allocations. The pooled span scratch is
// reused warm, so a naive map[string] duplicate check — which this asserts
// against — would regress the headline strip figure. Skipped under -race, where
// sync.Pool drops Puts.
func TestStrip_Validate_ZeroAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("sync.Pool drops Puts under -race; the zero-alloc invariant only holds without -race")
	}

	src := benchStripBlob(t)

	allocs := testing.AllocsPerRun(200, func() {
		_, doStrip, err := validateStrip(src)
		if err != nil || !doStrip {
			t.Fatalf("validateStrip unexpected: doStrip=%v err=%v", doStrip, err)
		}
	})

	require.Zero(t, allocs, "strip validation (walk + re-hash + scan) must be zero-alloc")
}

// TestStrip_Validate_ZeroAllocs_CollidedRun pins the same zero-allocation
// claim as TestStrip_Validate_ZeroAllocs above, but for the path that test
// never reaches: analyzeRunAdjacent, the duplicate-ID-run all-pairs name
// compare that only runs when the sorted V2 index actually contains a
// duplicate MetricID. benchStripBlob's 200 metrics all have distinct IDs, so
// scanV2 never finds a run longer than 1 and analyzeRunAdjacent is never
// called — the "0 allocations" claim is about that scan, so a fixture that
// never enters it leaves the claim unpinned.
//
// encodeCollisionNumeric (blob/metric_names_collision_test.go) encodes the
// real xxHash64-colliding pair cnA/cnB, forcing a genuine 2-entry duplicate-ID
// run in the V2 index. Strip legitimately refuses to strip a real collision
// (doStrip=false, err=nil, names stay load-bearing to disambiguate at decode
// time) — that refusal is only reached after scanV2 -> analyzeRunAdjacent has
// already walked and compared the two names, which is exactly the allocation
// behaviour under test.
func TestStrip_Validate_ZeroAllocs_CollidedRun(t *testing.T) {
	if raceEnabled {
		t.Skip("sync.Pool drops Puts under -race; the zero-alloc invariant only holds without -race")
	}

	src := encodeCollisionNumeric(t, WithBlobLayoutV2())

	allocs := testing.AllocsPerRun(200, func() {
		_, doStrip, err := validateStrip(src)
		if err != nil || doStrip {
			t.Fatalf("validateStrip unexpected: doStrip=%v err=%v (a real collision must be refused, not stripped)", doStrip, err)
		}
	})

	require.Zero(t, allocs, "strip validation over a duplicate-ID run (analyzeRunAdjacent) must be zero-alloc")
}

// BenchmarkStripValidate measures the validation-only pass (steps 1-7).
func BenchmarkStripValidate(b *testing.B) {
	src := benchStripBlob(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _, _ = validateStrip(src)
	}
}

// BenchmarkStripAppend measures the append form into a reused destination.
func BenchmarkStripAppend(b *testing.B) {
	src := benchStripBlob(b)
	dst := make([]byte, 0, len(src))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		out, _, _ := StripMetricNames(dst[:0], src)
		_ = out
	}
}

// BenchmarkStripInPlace measures the in-place form (refill excluded from timing).
func BenchmarkStripInPlace(b *testing.B) {
	src := benchStripBlob(b)
	buf := make([]byte, len(src))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		copy(buf, src)
		work := buf[:len(src)]
		b.StartTimer()
		_, _, _ = StripMetricNamesInPlace(work)
	}
}

// BenchmarkStripVsDecodeReencode measures the alternative strip replaces:
// decoding the names-bearing blob and re-encoding it without names.
func BenchmarkStripVsDecodeReencode(b *testing.B) {
	src := benchStripBlob(b)

	b.Run("strip_append", func(b *testing.B) {
		dst := make([]byte, 0, len(src))
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_, _, _ = StripMetricNames(dst[:0], src)
		}
	})

	b.Run("decode_reencode", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			dec, err := NewNumericDecoder(src)
			if err != nil {
				b.Fatal(err)
			}
			nb, err := dec.Decode()
			if err != nil {
				b.Fatal(err)
			}
			enc, err := NewNumericEncoder(nb.StartTime(), WithBlobLayoutV2())
			if err != nil {
				b.Fatal(err)
			}
			for _, id := range nb.MetricIDs() {
				n := nb.Len(id)
				_ = enc.StartMetricID(id, n)
				for _, dp := range nb.All(id) {
					_ = enc.AddDataPoint(dp.Ts, dp.Val, "")
				}
				_ = enc.EndMetric()
			}
			_, _ = enc.Finish()
		}
	})
}
