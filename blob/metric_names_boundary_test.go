package blob

import (
	"fmt"
	"testing"
	"time"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/section"
	"github.com/stretchr/testify/require"
)

// patchEntryMetricID overwrites the MetricID (first 8 bytes) of index entry i in
// a V2 no-names blob (IndexOffset == HeaderSize, 16-byte compact entries).
func patchEntryMetricID(t *testing.T, data []byte, entrySize, i int, id uint64) {
	t.Helper()
	hdr, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	off := int(hdr.IndexOffset) + i*entrySize
	endian.GetLittleEndianEngine().PutUint64(data[off:off+8], id)
}

// makeV2Ext builds a V2Ext (32-byte index) numeric blob in ID mode (no
// collision) for the given distinct MetricIDs. V2Ext has no dedicated
// encoder option — it is auto-selected the same way encodeCollisionNumericV2Ext
// (metric_names_collision_test.go) triggers it: raw ts/value encoding (8
// bytes/point) plus a large first entry, so a later entry's delta offset
// exceeds section.NumericMaxOffset (65535).
func makeV2Ext(t *testing.T, ids []uint64) []byte {
	t.Helper()
	require.GreaterOrEqual(t, len(ids), 2, "need >=2 entries to observe a delta offset")

	start := time.Now()
	enc, err := NewNumericEncoder(start,
		WithBlobLayoutV2(),
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeRaw),
	)
	require.NoError(t, err)

	// 8192 raw float64 points = 65536 bytes > NumericMaxOffset (65535).
	const bigPoints = 8192
	for i, id := range ids {
		points := 1
		if i == 0 {
			points = bigPoints
		}
		require.NoError(t, enc.StartMetricID(id, points))
		for j := 0; j < points; j++ {
			require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(j)*1_000_000, float64(j), ""))
		}
		require.NoError(t, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(t, err)

	hdr, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	require.True(t, hdr.Flag.IsV2Ext(), "fixture must actually trigger V2Ext")

	return data
}

// makeV2SharedTS builds a compact V2 numeric blob in ID mode with
// WithSharedTimestamps() for the given distinct MetricIDs, giving every entry
// the identical timestamp sequence so the shared-timestamp table activates.
func makeV2SharedTS(t *testing.T, ids []uint64) []byte {
	t.Helper()
	start := time.Now()
	enc, err := NewNumericEncoder(start, WithSharedTimestamps())
	require.NoError(t, err)

	ts := []int64{start.UnixMicro(), start.UnixMicro() + 1_000_000}
	for _, id := range ids {
		require.NoError(t, enc.StartMetricID(id, len(ts)))
		for _, tsv := range ts {
			require.NoError(t, enc.AddDataPoint(tsv, 1.0, ""))
		}
		require.NoError(t, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(t, err)

	hdr, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	require.True(t, hdr.Flag.HasSharedTimestamps(), "fixture must actually trigger timestamp sharing")

	return data
}

// encodeCollisionNumericSharedTS encodes a real-collision pair (A,B) under
// WithSharedTimestamps(), giving both entries the identical timestamp
// sequence so the shared-timestamp table actually activates — the
// "adjacent [H,H], shared ts" collision case for V2Ext.
func encodeCollisionNumericSharedTS(t *testing.T) []byte {
	t.Helper()
	start := time.Now()
	enc, err := NewNumericEncoder(start, WithSharedTimestamps())
	require.NoError(t, err)

	ts := []int64{start.UnixMicro(), start.UnixMicro() + 1_000_000}

	require.NoError(t, enc.StartMetricName(cnA, len(ts)))
	require.NoError(t, enc.AddDataPoint(ts[0], 1.0, ""))
	require.NoError(t, enc.AddDataPoint(ts[1], 1.5, ""))
	require.NoError(t, enc.EndMetric())

	require.NoError(t, enc.StartMetricName(cnB, len(ts)))
	require.NoError(t, enc.AddDataPoint(ts[0], 2.0, ""))
	require.NoError(t, enc.AddDataPoint(ts[1], 2.5, ""))
	require.NoError(t, enc.EndMetric())

	data, err := enc.Finish()
	require.NoError(t, err)

	hdr, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	require.True(t, hdr.Flag.HasSharedTimestamps(), "fixture must actually trigger timestamp sharing")

	return data
}

// Rejects a V2 index whose entries are not sorted by MetricID; entries with
// equal, adjacent IDs (as produced by a real hash collision) are still accepted.
func TestUnsortedV2_Rejected(t *testing.T) {
	makeV2 := func(t *testing.T, ids []uint64) []byte {
		t.Helper()
		start := time.Now()
		enc, err := NewNumericEncoder(start, WithBlobLayoutV2())
		require.NoError(t, err)
		for _, id := range ids {
			require.NoError(t, enc.StartMetricID(id, 1))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1, ""))
			require.NoError(t, enc.EndMetric())
		}
		data, err := enc.Finish()
		require.NoError(t, err)

		return data
	}

	t.Run("descending pair", func(t *testing.T) {
		data := makeV2(t, []uint64{100, 200}) // encoder emits sorted [100,200]
		entrySize := section.NumericIndexEntrySize
		// Patch to descending [300,200]: entry0=300 > entry1=200.
		patchEntryMetricID(t, data, entrySize, 0, 300)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		_, err = dec.Decode()
		require.ErrorIs(t, err, errs.ErrUnsortedIndex)
	})

	t.Run("equal IDs separated by another", func(t *testing.T) {
		data := makeV2(t, []uint64{10, 20, 30})
		entrySize := section.NumericIndexEntrySize
		// Patch to [50, 20, 50]: entry2=50 vs entry1=20 -> 50>20 ok, but
		// entry1=20 < entry0=50 -> unsorted.
		patchEntryMetricID(t, data, entrySize, 0, 50)
		patchEntryMetricID(t, data, entrySize, 2, 50)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		_, err = dec.Decode()
		require.ErrorIs(t, err, errs.ErrUnsortedIndex)
	})

	t.Run("adjacent equal IDs accepted", func(t *testing.T) {
		// The collision pair produces a producer-valid [H,H]-adjacent V2 blob.
		data := encodeCollisionNumeric(t, WithBlobLayoutV2())
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)
		require.Equal(t, 2, b.MetricCount())
		require.True(t, b.HasMetricName(cnA))
		require.True(t, b.HasMetricName(cnB))
	})

	// validateV2Order (numeric_decoder.go) is gated on Flag.IsV2(), which is
	// true for BOTH compact V2 (0xEA20) and extended V2Ext (0xEA30) — see
	// section/numeric_flag.go. The four sub-tests below repeat the descending
	// vs. adjacent-equal distinction above for V2Ext and for the
	// shared-timestamp variant of V2, so that dimension isn't exercised only
	// through the 16-byte compact layout.

	t.Run("V2Ext descending pair", func(t *testing.T) {
		data := makeV2Ext(t, []uint64{100, 200})
		entrySize := section.NumericExtIndexEntrySize
		// Patch to descending [300,200]: entry0=300 > entry1=200.
		patchEntryMetricID(t, data, entrySize, 0, 300)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		_, err = dec.Decode()
		require.ErrorIs(t, err, errs.ErrUnsortedIndex)
	})

	t.Run("V2Ext adjacent equal IDs accepted", func(t *testing.T) {
		// The collision pair produces a producer-valid [H,H]-adjacent V2Ext blob.
		data, _ := encodeCollisionNumericV2Ext(t)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)
		require.Equal(t, 2, b.MetricCount())
		require.True(t, b.HasMetricName(cnA))
		require.True(t, b.HasMetricName(cnB))
	})

	t.Run("shared-timestamp descending pair", func(t *testing.T) {
		data := makeV2SharedTS(t, []uint64{100, 200})
		entrySize := section.NumericIndexEntrySize
		// Patch to descending [300,200]: entry0=300 > entry1=200.
		patchEntryMetricID(t, data, entrySize, 0, 300)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		_, err = dec.Decode()
		require.ErrorIs(t, err, errs.ErrUnsortedIndex)
	})

	t.Run("shared-timestamp adjacent equal IDs accepted", func(t *testing.T) {
		// The collision pair produces a producer-valid [H,H]-adjacent, shared-ts V2 blob.
		data := encodeCollisionNumericSharedTS(t)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)
		require.Equal(t, 2, b.MetricCount())
		require.True(t, b.HasMetricName(cnA))
		require.True(t, b.HasMetricName(cnB))
	})
}

// Exercises the 65536th-entry boundary, where the metric-count ceiling starts
// rejecting further inserts.
func TestMetricCountBoundary(t *testing.T) {
	start := time.Now()

	t.Run("ID mode reaches 65536", func(t *testing.T) {
		enc, err := NewNumericEncoder(start)
		require.NoError(t, err)
		for i := 1; i <= MaxMetricCount; i++ {
			require.NoError(t, enc.StartMetricID(uint64(i), 1))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1, ""))
			require.NoError(t, enc.EndMetric())
		}
		// 65537th rejected.
		err = enc.StartMetricID(uint64(MaxMetricCount+1), 1)
		require.ErrorIs(t, err, errs.ErrMetricCountExceeded)
	})

	t.Run("name mode no collision reaches 65536", func(t *testing.T) {
		enc, err := NewNumericEncoder(start)
		require.NoError(t, err)
		for i := 0; i < MaxMetricCount; i++ {
			require.NoError(t, enc.StartMetricName(fmt.Sprintf("metric.%d", i), 1))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1, ""))
			require.NoError(t, enc.EndMetric())
		}
		require.Equal(t, MaxMetricCount, enc.MetricCount())
		// 65537th rejected by the overall ceiling.
		err = enc.StartMetricName("metric.overflow", 1)
		require.ErrorIs(t, err, errs.ErrMetricCountExceeded)
	})

	t.Run("names required caps at 65535 and recovers", func(t *testing.T) {
		enc, err := NewNumericEncoder(start)
		require.NoError(t, err)
		// Force a collision first so names become required.
		require.NoError(t, enc.StartMetricName(cnA, 1))
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1, ""))
		require.NoError(t, enc.EndMetric())
		require.NoError(t, enc.StartMetricName(cnB, 1))
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 2, ""))
		require.NoError(t, enc.EndMetric())

		// Fill up to exactly MaxMetricNamesCount (65535) entries.
		for i := enc.MetricCount(); i < MaxMetricNamesCount; i++ {
			require.NoError(t, enc.StartMetricName(fmt.Sprintf("filler.%d", i), 1))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1, ""))
			require.NoError(t, enc.EndMetric())
		}
		require.Equal(t, MaxMetricNamesCount, enc.MetricCount())

		// The 65536th entry is rejected BEFORE any mutation.
		err = enc.StartMetricName("one.too.many", 1)
		require.ErrorIs(t, err, errs.ErrTooManyMetricNames)
		require.Equal(t, MaxMetricNamesCount, enc.MetricCount(), "count unchanged after rejection")

		// State is recoverable: another rejection then a successful finish.
		err = enc.StartMetricName("still.too.many", 1)
		require.ErrorIs(t, err, errs.ErrTooManyMetricNames)

		data, err := enc.Finish()
		require.NoError(t, err)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)
		require.Equal(t, MaxMetricNamesCount, b.MetricCount())
	})

	// The two sub-tests above trigger namesRequired via a PRIOR collision
	// (hasCollision already latched before the boundary is reached). Under
	// that trigger, a non-transactional implementation would pass every
	// assertion above too: hasCollision is already true so re-latching it is
	// a no-op, MetricCount() is len(indexEntries) and untouched by a stray
	// tracker mutation, and the names payload is sourced from e.metricNames,
	// not the tracker, so a stray Commit would be invisible. These two
	// sub-tests instead trigger namesRequired via each of the OTHER two
	// disjuncts — prospectiveCollision alone, and storeMetricNames alone —
	// with no prior collision, so they can actually distinguish an atomic
	// preflight from a bug that commits before checking the names ceiling.

	t.Run("prospective collision alone triggers the names ceiling", func(t *testing.T) {
		enc, err := NewNumericEncoder(start)
		require.NoError(t, err)

		// Fill to exactly MaxMetricNamesCount (65535) entries with cnA among
		// them and NO collision: cnB (which collides with cnA) is never
		// added in this loop, so hasCollision stays false throughout.
		require.NoError(t, enc.StartMetricName(cnA, 1))
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1, ""))
		require.NoError(t, enc.EndMetric())
		for i := enc.MetricCount(); i < MaxMetricNamesCount; i++ {
			require.NoError(t, enc.StartMetricName(fmt.Sprintf("filler.%d", i), 1))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1, ""))
			require.NoError(t, enc.EndMetric())
		}
		require.Equal(t, MaxMetricNamesCount, enc.MetricCount())
		require.False(t, enc.hasCollision, "no real collision has happened yet")
		trackerCountBefore := enc.collisionTracker.Count()
		namesBefore := append([]string(nil), enc.collisionTracker.GetMetricNames()...)

		// cnB collides with the already-stored cnA. At exactly 65535
		// entries, namesRequired must come SOLELY from prospectiveCollision
		// (storeMetricNames is false, hasCollision is false) — this is the
		// case a namesRequired calculation that forgets prospectiveCollision
		// would get wrong, accepting the entry and pushing the encoder past
		// the names-payload ceiling.
		err = enc.StartMetricName(cnB, 1)
		require.ErrorIs(t, err, errs.ErrTooManyMetricNames)

		// Rejection must be fully atomic: no index entry, no tracker
		// mutation (Commit not called), no collision latched, no metric
		// started.
		require.Equal(t, MaxMetricNamesCount, enc.MetricCount(), "count unchanged after rejection")
		require.False(t, enc.hasCollision, "rejection must not latch a collision")
		require.Equal(t, uint64(0), enc.curMetricID, "rejection must not start a metric")
		require.Equal(t, trackerCountBefore, enc.collisionTracker.Count(), "tracker must not record the rejected name")
		require.Equal(t, namesBefore, enc.collisionTracker.GetMetricNames(), "tracker's ordered names must be unchanged")
		require.Equal(t, MaxMetricNamesCount, len(enc.metricNames), "entry-parallel names list must be unchanged")

		// Operationally recoverable: a non-colliding metric does NOT require
		// names (namesRequired stays false for it), so it is accepted under
		// the overall MaxMetricCount ceiling even though the names ceiling
		// was just hit.
		require.NoError(t, enc.StartMetricName("metric.recoverable", 1))
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 2, ""))
		require.NoError(t, enc.EndMetric())
		require.Equal(t, MaxMetricNamesCount+1, enc.MetricCount())

		data, err := enc.Finish()
		require.NoError(t, err)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)
		require.Equal(t, MaxMetricNamesCount+1, b.MetricCount())
		require.False(t, b.HasMetricNames(), "no collision and no WithMetricNames() -> no names payload")
	})

	t.Run("WithMetricNames alone triggers the names ceiling", func(t *testing.T) {
		enc, err := NewNumericEncoder(start, WithMetricNames())
		require.NoError(t, err)

		// Fill to exactly MaxMetricNamesCount (65535) entries, all distinct,
		// non-colliding names. namesRequired is true for every one of them
		// solely because of WithMetricNames() (storeMetricNames), never
		// because of hasCollision or prospectiveCollision.
		for i := range MaxMetricNamesCount {
			require.NoError(t, enc.StartMetricName(fmt.Sprintf("wmn.filler.%d", i), 1))
			require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1, ""))
			require.NoError(t, enc.EndMetric())
		}
		require.Equal(t, MaxMetricNamesCount, enc.MetricCount())
		require.False(t, enc.hasCollision, "no collision anywhere in this sub-test")
		trackerCountBefore := enc.collisionTracker.Count()

		err = enc.StartMetricName("wmn.overflow", 1)
		require.ErrorIs(t, err, errs.ErrTooManyMetricNames)

		require.Equal(t, MaxMetricNamesCount, enc.MetricCount(), "count unchanged after rejection")
		require.False(t, enc.hasCollision, "rejection must not latch a collision")
		require.Equal(t, uint64(0), enc.curMetricID, "rejection must not start a metric")
		require.Equal(t, trackerCountBefore, enc.collisionTracker.Count(), "tracker must not record the rejected name")
		require.Equal(t, MaxMetricNamesCount, len(enc.metricNames), "entry-parallel names list must be unchanged")

		// Recoverable, but under WithMetricNames() EVERY metric requires
		// names, so a further insert stays capped at MaxMetricNamesCount
		// too (unlike the prospective-collision sub-test above).
		err = enc.StartMetricName("wmn.still.overflow", 1)
		require.ErrorIs(t, err, errs.ErrTooManyMetricNames)

		data, err := enc.Finish()
		require.NoError(t, err)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)
		require.Equal(t, MaxMetricNamesCount, b.MetricCount())
		require.True(t, b.HasMetricNames(), "WithMetricNames() must force the names payload on")
	})
}
