package blob

import (
	"fmt"
	"iter"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// Blob-set logical identity under collisions: metrics are grouped by logical
// name/id across a set, and a collided id resolves to the first colliding name.
//
// The colliding pair cnA/cnB (both hash to cnH) drives every scenario. Helpers build
// single-metric member blobs so a set can host A/H and B/H in DIFFERENT members (the
// cross-member collision the frankenseries bug lived in).

// encodeNamedNumeric builds a names-bearing single-metric numeric blob (WithMetricNames
// forces the names payload even without a within-blob collision). Values are distinct so
// the resolved series is identifiable.
func encodeNamedNumeric(t *testing.T, start time.Time, name string, vals ...float64) NumericBlob {
	t.Helper()
	enc, err := NewNumericEncoder(start, WithMetricNames())
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricName(name, len(vals)))
	for i, v := range vals {
		require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(i)*1_000_000, v, ""))
	}
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)

	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)

	return blob
}

// encodeStrippedNumeric builds a names-free single-metric numeric blob keyed by the
// colliding ID cnH (index.names == nil), standing in for a stripped member of a collided
// set — the only shape these identity scenarios need.
func encodeStrippedNumeric(t *testing.T, start time.Time, vals ...float64) NumericBlob {
	t.Helper()
	enc, err := NewNumericEncoder(start)
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricID(cnH, len(vals)))
	for i, v := range vals {
		require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(i)*1_000_000, v, ""))
	}
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)

	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)

	return blob
}

func encodeNamedText(t *testing.T, start time.Time, name string, vals ...string) TextBlob {
	t.Helper()
	enc, err := NewTextEncoder(start)
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricName(name, len(vals)))
	for i, v := range vals {
		require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(i)*1_000_000, v, ""))
	}
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)

	dec, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)

	return blob
}

// TestBlobSet_NoCollision_ZeroAlloc confirms a names-bearing set with NO collision
// keeps the identity table nil and its ID-keyed accessors stay allocation-free
// (today's direct per-member iteration). The probe pays only for the cheap
// collision check.
func TestBlobSet_NoCollision_ZeroAlloc(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation assertions are unreliable under -race")
	}

	base := time.Now().Truncate(time.Hour)
	// Distinct (non-colliding) names across two windows, names retained.
	blob1 := encodeNamedNumeric(t, base, "metric.one", 1.0, 2.0)
	blob2 := encodeNamedNumeric(t, base.Add(time.Hour), "metric.one", 3.0)
	set, err := NewNumericBlobSet([]NumericBlob{blob1, blob2})
	require.NoError(t, err)
	require.Nil(t, set.identity, "no-collision names-bearing set must not build the identity table")

	id := set.MetricIDs()[0]
	allocs := testing.AllocsPerRun(200, func() {
		_ = set.MetricLen(id)
		_, _ = set.ValueAt(id, 0)
		_, _ = set.TimestampAt(id, 0)
	})
	require.Zero(t, allocs, "no-collision ID-keyed set access must be zero-alloc")
}

func collectNumericValues(seq func(func(float64) bool)) []float64 {
	var out []float64
	for v := range seq {
		out = append(out, v)
	}

	return out
}

// TestBlobSet_CrossMemberCollision_Numeric is the core cross-member collision
// scenario: A/H and B/H live in DIFFERENT members, both names retained. The set
// has two logical metrics, and every ID-taking surface resolves H to the FIRST
// colliding name (A, the earlier member) — never an A+B frankenseries.
func TestBlobSet_CrossMemberCollision_Numeric(t *testing.T) {
	base := time.Now().Truncate(time.Hour)
	blobA := encodeNamedNumeric(t, base, cnA, 1.0, 1.5)                     // A/H, earlier
	blobB := encodeNamedNumeric(t, base.Add(time.Hour), cnB, 2.0, 2.5, 2.7) // B/H, later

	set, err := NewNumericBlobSet([]NumericBlob{blobB, blobA}) // caller order reversed
	require.NoError(t, err)

	// Identity built (collision observed); canonical first = A (earlier StartTime).
	require.NotNil(t, set.identity, "cross-member collision must build the identity table")

	// Two set metrics under logical identity (A and B are distinct set metrics).
	require.Equal(t, 2, set.MetricCount())
	require.Equal(t, []uint64{cnH, cnH}, set.MetricIDs())
	require.ElementsMatch(t, []string{cnA, cnB}, set.MetricNames())
	require.True(t, set.HasMetricID(cnH))

	// Canonical first (A) is the FIRST name in MetricNames order.
	require.Equal(t, cnA, set.MetricNames()[0], "A appears first in canonical order")

	aVals := []float64{1.0, 1.5}

	// Every ID-taking raw surface resolves H → A (never A+B = 5 points).
	require.Equal(t, len(aVals), set.MetricLen(cnH))
	require.Equal(t, aVals, collectNumericValues(set.AllValues(cnH)))

	var allDP []float64
	for _, dp := range set.All(cnH) {
		allDP = append(allDP, dp.Val)
	}
	require.Equal(t, aVals, allDP)

	for i, want := range aVals {
		v, ok := set.ValueAt(cnH, i)
		require.True(t, ok)
		require.Equal(t, want, v)
	}
	_, ok := set.ValueAt(cnH, len(aVals)) // no B data bleeds past A's length
	require.False(t, ok)

	ts0, ok := set.TimestampAt(cnH, 0)
	require.True(t, ok)
	require.Equal(t, base.UnixMicro(), ts0)

	// Selective MaterializeMetric(H) → A only.
	mmH, ok := set.MaterializeMetric(cnH)
	require.True(t, ok)
	require.Equal(t, aVals, mmH.Values)

	// MaterializeMetricByName resolves each name's OWN series (no re-resolve by ID).
	mmA, ok := set.MaterializeMetricByName(cnA)
	require.True(t, ok)
	require.Equal(t, aVals, mmA.Values)
	mmB, ok := set.MaterializeMetricByName(cnB)
	require.True(t, ok)
	require.Equal(t, []float64{2.0, 2.5, 2.7}, mmB.Values)

	// Materialized set: same identity on every ID/name surface.
	mat := set.Materialize()
	require.Equal(t, 2, mat.MetricCount())
	require.Equal(t, []uint64{cnH, cnH}, mat.MetricIDs())
	require.ElementsMatch(t, []string{cnA, cnB}, mat.MetricNames())
	require.True(t, mat.HasMetricID(cnH))
	require.Equal(t, len(aVals), mat.DataPointCount(cnH))
	v, ok := mat.ValueAt(cnH, 0) // ID → first colliding name A
	require.True(t, ok)
	require.Equal(t, 1.0, v)
	_, ok = mat.ValueAt(cnH, len(aVals))
	require.False(t, ok)
	v, ok = mat.ValueAtByName(cnA, 1)
	require.True(t, ok)
	require.Equal(t, 1.5, v)
	v, ok = mat.ValueAtByName(cnB, 2)
	require.True(t, ok)
	require.Equal(t, 2.7, v)
}

// TestBlobSet_CanonicalOrder pins the total order: StartTime first, then caller slice
// order for equal StartTime, then index order within a member.
func TestBlobSet_CanonicalOrder(t *testing.T) {
	base := time.Now().Truncate(time.Hour)

	t.Run("StartTime wins over slice order", func(t *testing.T) {
		// A is LATER, B is EARLIER; pass [A, B]. Chronological sort puts B first → B is
		// the canonical first colliding name for H.
		blobA := encodeNamedNumeric(t, base.Add(time.Hour), cnA, 1.0)
		blobB := encodeNamedNumeric(t, base, cnB, 2.0, 2.5)
		set, err := NewNumericBlobSet([]NumericBlob{blobA, blobB})
		require.NoError(t, err)
		require.Equal(t, cnB, set.MetricNames()[0])
		require.Equal(t, 2, set.MetricLen(cnH)) // B has 2 points
		v, ok := set.ValueAt(cnH, 0)
		require.True(t, ok)
		require.Equal(t, 2.0, v)
	})

	t.Run("equal StartTime broken by slice order", func(t *testing.T) {
		mk := func(name string, v float64) NumericBlob { return encodeNamedNumeric(t, base, name, v) }
		// [A, B] at the same StartTime → stable sort keeps A first.
		setAB, err := NewNumericBlobSet([]NumericBlob{mk(cnA, 1.0), mk(cnB, 2.0)})
		require.NoError(t, err)
		require.Equal(t, cnA, setAB.MetricNames()[0])
		v, ok := setAB.ValueAt(cnH, 0)
		require.True(t, ok)
		require.Equal(t, 1.0, v)

		// [B, A] at the same StartTime → B first.
		setBA, err := NewNumericBlobSet([]NumericBlob{mk(cnB, 2.0), mk(cnA, 1.0)})
		require.NoError(t, err)
		require.Equal(t, cnB, setBA.MetricNames()[0])
		v, ok = setBA.ValueAt(cnH, 0)
		require.True(t, ok)
		require.Equal(t, 2.0, v)
	})

	t.Run("same-member collision broken by index order", func(t *testing.T) {
		// encodeCollisionNumeric inserts A then B in ONE blob → index order makes A first.
		data := encodeCollisionNumeric(t)
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		blob, err := dec.Decode()
		require.NoError(t, err)
		set, err := NewNumericBlobSet([]NumericBlob{blob})
		require.NoError(t, err)
		require.Equal(t, cnA, set.MetricNames()[0])
		require.Equal(t, []float64{1.0, 1.5}, collectNumericValues(set.AllValues(cnH)))
		require.Equal(t, 2, set.MetricCount())
	})
}

// TestBlobSet_SameNameMerge confirms the same name across members stays ONE merged series
// (today's cross-window merge preserved — no collision, identity stays nil).
func TestBlobSet_SameNameMerge(t *testing.T) {
	base := time.Now().Truncate(time.Hour)
	blob1 := encodeNamedNumeric(t, base, cnA, 1.0, 1.5)
	blob2 := encodeNamedNumeric(t, base.Add(time.Hour), cnA, 2.0, 2.5, 2.7)
	set, err := NewNumericBlobSet([]NumericBlob{blob1, blob2})
	require.NoError(t, err)

	require.Nil(t, set.identity, "same name across members is not a collision")
	require.Equal(t, 1, set.MetricCount())
	require.Equal(t, []uint64{cnH}, set.MetricIDs())
	require.Equal(t, []string{cnA}, set.MetricNames())
	require.Equal(t, 5, set.MetricLen(cnH)) // merged across both windows
	require.Equal(t, []float64{1.0, 1.5, 2.0, 2.5, 2.7}, collectNumericValues(set.AllValues(cnH)))

	mat := set.Materialize()
	require.Equal(t, 1, mat.MetricCount())
	require.Equal(t, 5, mat.DataPointCount(cnH))
}

// TestBlobSet_SparseMembership: a metric present in only some members still resolves and
// merges correctly, with distinct colliding names kept apart.
func TestBlobSet_SparseMembership(t *testing.T) {
	base := time.Now().Truncate(time.Hour)
	blobA1 := encodeNamedNumeric(t, base, cnA, 1.0)
	blobB := encodeNamedNumeric(t, base.Add(time.Hour), cnB, 2.0, 2.5)
	blobA2 := encodeNamedNumeric(t, base.Add(2*time.Hour), cnA, 1.7, 1.9)
	set, err := NewNumericBlobSet([]NumericBlob{blobA1, blobB, blobA2})
	require.NoError(t, err)

	require.Equal(t, 2, set.MetricCount())
	// A merges across its two members (sparse: absent from the middle one); B stays alone.
	require.Equal(t, []float64{1.0, 1.7, 1.9}, collectNumericValues(set.AllValues(cnH)))
	mmB, ok := set.MaterializeMetricByName(cnB)
	require.True(t, ok)
	require.Equal(t, []float64{2.0, 2.5}, mmB.Values)
}

// TestBlobSet_MixedStripped: a stripped member sharing H attaches to the FIRST colliding
// name; the second colliding name never absorbs the stripped data.
func TestBlobSet_MixedStripped(t *testing.T) {
	base := time.Now().Truncate(time.Hour)
	// blob1 is a within-member collision (A/H then B/H); blob2 is stripped H.
	data := encodeCollisionNumeric(t)
	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	collision, err := dec.Decode()
	require.NoError(t, err)
	stripped := encodeStrippedNumeric(t, base.Add(time.Hour), 9.0, 9.5)

	set, err := NewNumericBlobSet([]NumericBlob{collision, stripped})
	require.NoError(t, err)
	require.NotNil(t, set.identity)

	// ID access → first name A, and the stripped member's H attaches to A.
	require.Equal(t, cnA, set.MetricNames()[0])
	require.Equal(t, []float64{1.0, 1.5, 9.0, 9.5}, collectNumericValues(set.AllValues(cnH)))

	// Second colliding name B keeps ONLY its own entry — stripped data does not attach.
	mmB, ok := set.MaterializeMetricByName(cnB)
	require.True(t, ok)
	require.Equal(t, []float64{2.0, 2.5, 2.7}, mmB.Values)

	// First colliding name A absorbs the stripped member.
	mmA, ok := set.MaterializeMetricByName(cnA)
	require.True(t, ok)
	require.Equal(t, []float64{1.0, 1.5, 9.0, 9.5}, mmA.Values)
}

// TestBlobSet_LogicalIdentity_Text mirrors the cross-member scenario for TextBlobSet
// (text stores names by default, so the set is names-bearing without an explicit
// option).
func TestBlobSet_LogicalIdentity_Text(t *testing.T) {
	base := time.Now().Truncate(time.Hour)
	blobA := encodeNamedText(t, base, cnA, "a0", "a1")
	blobB := encodeNamedText(t, base.Add(time.Hour), cnB, "b0", "b1", "b2")
	set, err := NewTextBlobSet([]TextBlob{blobB, blobA})
	require.NoError(t, err)
	require.NotNil(t, set.identity)

	require.Equal(t, 2, set.MetricCount())
	require.Equal(t, []uint64{cnH, cnH}, set.MetricIDs())
	require.Equal(t, cnA, set.MetricNames()[0])

	var vals []string
	for v := range set.AllValues(cnH) {
		vals = append(vals, v)
	}
	require.Equal(t, []string{"a0", "a1"}, vals) // A only, no B frankenseries
	require.Equal(t, 2, set.MetricLen(cnH))

	mat := set.Materialize()
	require.Equal(t, 2, mat.MetricCount())
	v, ok := mat.ValueAt(cnH, 0)
	require.True(t, ok)
	require.Equal(t, "a0", v)
	v, ok = mat.ValueAtByName(cnB, 2)
	require.True(t, ok)
	require.Equal(t, "b2", v)
	require.Equal(t, 3, mat.DataPointCountByName(cnB))
}

// TestBlobSet_UnifiedPerType: a numeric A/H and a text B/H are independent logical
// metrics, each resolved within its own concrete type (identity scoping stays
// separate per numeric/text type even though both call into the unified BlobSet
// API).
func TestBlobSet_UnifiedPerType(t *testing.T) {
	base := time.Now().Truncate(time.Hour)
	numA := encodeNamedNumeric(t, base, cnA, 1.0, 1.5)
	txtB := encodeNamedText(t, base, cnB, "b0", "b1", "b2")

	bs := NewBlobSet([]NumericBlob{numA}, []TextBlob{txtB})

	// Numeric H resolves to A (numeric side); text H resolves to B (text side).
	v, ok := bs.NumericValueAt(cnH, 0)
	require.True(t, ok)
	require.Equal(t, 1.0, v)
	tv, ok := bs.TextValueAt(cnH, 2)
	require.True(t, ok)
	require.Equal(t, "b2", tv)

	// Name-keyed access stays exact per type.
	v, ok = bs.NumericValueAtByName(cnA, 1)
	require.True(t, ok)
	require.Equal(t, 1.5, v)
	tv, ok = bs.TextValueAtByName(cnB, 0)
	require.True(t, ok)
	require.Equal(t, "b0", tv)

	require.Equal(t, 2, bs.MetricLen(cnH)) // numeric-first precedence: A's length
}

// TestBlobSet_UnifiedPerType_Collision drives a genuine within-type collision on each
// side and asserts the unified generic accessors still resolve per type.
func TestBlobSet_UnifiedPerType_Collision(t *testing.T) {
	base := time.Now().Truncate(time.Hour)
	// Numeric within-member collision A/H,B/H.
	numData := encodeCollisionNumeric(t)
	ndec, err := NewNumericDecoder(numData)
	require.NoError(t, err)
	numColl, err := ndec.Decode()
	require.NoError(t, err)

	// Text cross-member collision A/H (earlier) + B/H (later).
	txtA := encodeNamedText(t, base, cnA, "ta0")
	txtB := encodeNamedText(t, base.Add(time.Hour), cnB, "tb0", "tb1")

	bs := NewBlobSet([]NumericBlob{numColl}, []TextBlob{txtB, txtA})
	require.NotNil(t, bs.numericIdentity)
	require.NotNil(t, bs.textIdentity)

	// Numeric H → first numeric name A (values 1.0,1.5).
	v, ok := bs.NumericValueAt(cnH, 0)
	require.True(t, ok)
	require.Equal(t, 1.0, v)
	require.Equal(t, 2, bs.MetricLen(cnH))

	// Text H → first text name A (canonical: earlier StartTime).
	tv, ok := bs.TextValueAt(cnH, 0)
	require.True(t, ok)
	require.Equal(t, "ta0", tv)
	_, ok = bs.TextValueAt(cnH, 1) // A has one point; B does not bleed in
	require.False(t, ok)
}

// --- collided-set resolution fixtures ---------------------------------------
//
// The scenarios below place the SECOND colliding name first inside a member, and
// mix stripped members into a collided set, so both directions of the resolution
// rule are exercised: an ID-keyed access must not drop a member that carries the
// resolved name at a non-first ordinal, and a name-keyed access must not attach a
// stripped member to a name that is not the first colliding name for its ID.

// numericSeries / textSeries describe one metric inside a multi-metric fixture blob.
type numericSeries struct {
	name string
	vals []float64
}

type textSeries struct {
	name string
	vals []string
}

func numericTag(v float64) string { return fmt.Sprintf("tag-%g", v) }

func textTag(v string) string { return "tag-" + v }

// encodeNumericSeries builds a names-bearing numeric blob holding every series in the
// given INDEX order, so a fixture can place a colliding name at a non-first ordinal.
// Tags are enabled so the tag surfaces are assertable.
func encodeNumericSeries(t *testing.T, start time.Time, opts []NumericEncoderOption, series ...numericSeries) NumericBlob {
	t.Helper()
	all := append([]NumericEncoderOption{WithMetricNames(), WithTagsEnabled(true)}, opts...)
	enc, err := NewNumericEncoder(start, all...)
	require.NoError(t, err)
	for _, s := range series {
		require.NoError(t, enc.StartMetricName(s.name, len(s.vals)))
		for i, v := range s.vals {
			require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(i)*1_000_000, v, numericTag(v)))
		}
		require.NoError(t, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(t, err)

	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)

	return blob
}

// encodeTextSeries is encodeNumericSeries' text twin.
func encodeTextSeries(t *testing.T, start time.Time, series ...textSeries) TextBlob {
	t.Helper()
	enc, err := NewTextEncoder(start, WithTextTagsEnabled(true))
	require.NoError(t, err)
	for _, s := range series {
		require.NoError(t, enc.StartMetricName(s.name, len(s.vals)))
		for i, v := range s.vals {
			require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(i)*1_000_000, v, textTag(v)))
		}
		require.NoError(t, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(t, err)

	dec, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)

	return blob
}

// encodeStrippedText is encodeStrippedNumeric's text twin, likewise keyed by cnH.
func encodeStrippedText(t *testing.T, start time.Time, vals ...string) TextBlob {
	t.Helper()
	enc, err := NewTextEncoder(start, WithoutMetricNames(), WithTextTagsEnabled(true))
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricID(cnH, len(vals)))
	for i, v := range vals {
		require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(i)*1_000_000, v, textTag(v)))
	}
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)

	dec, err := NewTextDecoder(data)
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)

	return blob
}

// encodeNamesNumeric builds a names-bearing numeric blob carrying exactly the given metric
// names, two points each. Fixtures use it to control both a member's width and which ids it
// declares — the two things the cross-member collision probe branches on.
func encodeNamesNumeric(t *testing.T, start time.Time, names []string) NumericBlob {
	t.Helper()
	enc, err := NewNumericEncoder(start, WithMetricNames())
	require.NoError(t, err)
	for i, name := range names {
		require.NoError(t, enc.StartMetricName(name, 2))
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), float64(i), ""))
		require.NoError(t, enc.AddDataPoint(start.UnixMicro()+1_000_000, float64(i), ""))
		require.NoError(t, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(t, err)

	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := dec.Decode()
	require.NoError(t, err)

	return blob
}

// wideNames returns n distinct metric names under prefix; distinct prefixes yield disjoint
// name sets, and none of them collides with cnA/cnB.
func wideNames(prefix string, n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("%s.metric.name.%05d", prefix, i)
	}

	return names
}

// encodeWideNamedNumeric builds an n-metric names-bearing numeric blob with distinct,
// non-colliding names — wide enough that any per-entry scratch structure built during
// set construction shows up in an allocation count.
func encodeWideNamedNumeric(t *testing.T, start time.Time, n int) NumericBlob {
	t.Helper()

	return encodeNamesNumeric(t, start, wideNames("wide", n))
}

func collectSeq[T any](seq iter.Seq[T]) []T {
	var out []T
	for v := range seq {
		out = append(out, v)
	}

	return out
}

func collectSeq2[K, V any](seq iter.Seq2[K, V]) []V {
	var out []V
	for _, v := range seq {
		out = append(out, v)
	}

	return out
}

// TestBlobSet_CollidedID_ResolvesEntryByName_Numeric pins the ID-keyed resolution rule
// when the resolved name sits at a NON-first ordinal in a member: member2 holds the
// colliding pair with B inserted BEFORE A, so its first entry for H is B's. The logical
// metric H resolves to (A's data in member1) ++ (A's data in member2) — member2 must
// neither be dropped nor read through its B entry.
//
// It also pins the MetricDuration/MetricLen agreement invariant: the two resolve through
// different code paths and must describe the same series.
func TestBlobSet_CollidedID_ResolvesEntryByName_Numeric(t *testing.T) {
	layouts := map[string][]NumericEncoderOption{
		"V1": nil,
		"V2": {WithBlobLayoutV2()},
	}

	for layout, opts := range layouts {
		t.Run(layout, func(t *testing.T) {
			base := time.Now().Truncate(time.Hour)
			later := base.Add(time.Hour)
			member1 := encodeNumericSeries(t, base, opts, numericSeries{cnA, []float64{1.0, 1.5}})
			member2 := encodeNumericSeries(t, later, opts,
				numericSeries{cnB, []float64{70, 71}},
				numericSeries{cnA, []float64{80, 81}},
			)

			set, err := NewNumericBlobSet([]NumericBlob{member1, member2})
			require.NoError(t, err)
			require.NotNil(t, set.identity, "cross-member collision must build the identity table")
			require.Equal(t, cnA, set.MetricNames()[0], "A is the first colliding name")
			require.Equal(t, []uint64{cnH, cnH}, set.MetricIDs())
			require.True(t, set.HasMetricID(cnH))

			wantVals := []float64{1.0, 1.5, 80, 81}
			wantTs := []int64{
				base.UnixMicro(), base.UnixMicro() + 1_000_000,
				later.UnixMicro(), later.UnixMicro() + 1_000_000,
			}
			wantTags := []string{numericTag(1.0), numericTag(1.5), numericTag(80), numericTag(81)}

			require.Equal(t, len(wantVals), set.MetricLen(cnH))
			require.Equal(t, wantVals, collectSeq(set.AllValues(cnH)))
			require.Equal(t, wantTs, collectSeq(set.AllTimestamps(cnH)))
			require.Equal(t, wantTags, collectSeq(set.AllTags(cnH)))

			var dpVals []float64
			for _, dp := range set.All(cnH) {
				dpVals = append(dpVals, dp.Val)
			}
			require.Equal(t, wantVals, dpVals)

			for i := range wantVals {
				v, ok := set.ValueAt(cnH, i)
				require.True(t, ok, "ValueAt(%d)", i)
				require.Equal(t, wantVals[i], v)

				ts, ok := set.TimestampAt(cnH, i)
				require.True(t, ok, "TimestampAt(%d)", i)
				require.Equal(t, wantTs[i], ts)

				tag, ok := set.TagAt(cnH, i)
				require.True(t, ok, "TagAt(%d)", i)
				require.Equal(t, wantTags[i], tag)
			}
			_, ok := set.ValueAt(cnH, len(wantVals)) // B's data never bleeds past A's length
			require.False(t, ok)

			// MetricDuration and MetricLen resolve through different paths; they must agree
			// on which windows the logical metric spans.
			require.Equal(t, wantTs[len(wantTs)-1]-wantTs[0], set.MetricDuration(cnH))
			require.Equal(t, len(collectSeq(set.AllTimestamps(cnH))), set.MetricLen(cnH),
				"MetricLen must count exactly the timestamps AllTimestamps yields")

			// Selective and whole-set materialization agree with the raw surfaces.
			mmH, ok := set.MaterializeMetric(cnH)
			require.True(t, ok)
			require.Equal(t, wantVals, mmH.Values)
			require.Equal(t, wantTs, mmH.Timestamps)

			mmA, ok := set.MaterializeMetricByName(cnA)
			require.True(t, ok)
			require.Equal(t, wantVals, mmA.Values)
			mmB, ok := set.MaterializeMetricByName(cnB)
			require.True(t, ok)
			require.Equal(t, []float64{70, 71}, mmB.Values)

			mat := set.Materialize()
			require.Equal(t, 2, mat.MetricCount())
			require.True(t, mat.HasMetricID(cnH))
			require.Equal(t, len(wantVals), mat.DataPointCount(cnH))
			require.Equal(t, set.MetricLen(cnH), mat.DataPointCount(cnH))
			require.Equal(t, 2, mat.DataPointCountByName(cnB))
			for i := range wantVals {
				v, ok := mat.ValueAt(cnH, i)
				require.True(t, ok)
				require.Equal(t, wantVals[i], v)
				ts, ok := mat.TimestampAt(cnH, i)
				require.True(t, ok)
				require.Equal(t, wantTs[i], ts)
				tag, ok := mat.TagAt(cnH, i)
				require.True(t, ok)
				require.Equal(t, wantTags[i], tag)
			}

			// Name-keyed raw surfaces agree with the ID-keyed ones for the first name.
			require.Equal(t, len(wantVals), set.MetricLenByName(cnA))
			require.Equal(t, set.MetricDuration(cnH), set.MetricDurationByName(cnA))
			require.Equal(t, 2, set.MetricLenByName(cnB))
		})
	}
}

// TestBlobSet_CollidedID_ResolvesEntryByName_Text is the text twin of the numeric
// non-first-ordinal scenario.
func TestBlobSet_CollidedID_ResolvesEntryByName_Text(t *testing.T) {
	base := time.Now().Truncate(time.Hour)
	later := base.Add(time.Hour)
	member1 := encodeTextSeries(t, base, textSeries{cnA, []string{"a0", "a1"}})
	member2 := encodeTextSeries(t, later,
		textSeries{cnB, []string{"b0", "b1"}},
		textSeries{cnA, []string{"a2", "a3"}},
	)

	set, err := NewTextBlobSet([]TextBlob{member1, member2})
	require.NoError(t, err)
	require.NotNil(t, set.identity)
	require.Equal(t, cnA, set.MetricNames()[0])
	require.True(t, set.HasMetricID(cnH))

	wantVals := []string{"a0", "a1", "a2", "a3"}
	wantTs := []int64{
		base.UnixMicro(), base.UnixMicro() + 1_000_000,
		later.UnixMicro(), later.UnixMicro() + 1_000_000,
	}
	wantTags := []string{textTag("a0"), textTag("a1"), textTag("a2"), textTag("a3")}

	require.Equal(t, len(wantVals), set.MetricLen(cnH))
	require.Equal(t, wantVals, collectSeq(set.AllValues(cnH)))
	require.Equal(t, wantTs, collectSeq(set.AllTimestamps(cnH)))
	require.Equal(t, wantTags, collectSeq(set.AllTags(cnH)))
	require.Equal(t, wantVals, collectTextValues(set.All(cnH)), "All must yield the same series")

	for i := range wantVals {
		v, ok := set.ValueAt(cnH, i)
		require.True(t, ok)
		require.Equal(t, wantVals[i], v)
		ts, ok := set.TimestampAt(cnH, i)
		require.True(t, ok)
		require.Equal(t, wantTs[i], ts)
		tag, ok := set.TagAt(cnH, i)
		require.True(t, ok)
		require.Equal(t, wantTags[i], tag)
	}

	require.Equal(t, wantTs[len(wantTs)-1]-wantTs[0], set.MetricDuration(cnH))
	require.Equal(t, len(collectSeq(set.AllTimestamps(cnH))), set.MetricLen(cnH))

	mmH, ok := set.MaterializeMetric(cnH)
	require.True(t, ok)
	require.Equal(t, wantVals, mmH.Values)

	mat := set.Materialize()
	require.Equal(t, set.MetricLen(cnH), mat.DataPointCount(cnH))
	require.Equal(t, 2, mat.DataPointCountByName(cnB))
}

// collectTextValues / collectNumericDataPointValues project a data-point sequence onto
// its values, which is what the identity assertions compare.
func collectTextValues(seq iter.Seq2[int, TextDataPoint]) []string {
	var out []string
	for _, dp := range seq {
		out = append(out, dp.Val)
	}

	return out
}

func collectNumericDataPointValues(seq iter.Seq2[int, NumericDataPoint]) []float64 {
	var out []float64
	for _, dp := range seq {
		out = append(out, dp.Val)
	}

	return out
}

// TestBlobSet_CollidedID_ResolvesEntryByName_Unified drives the same non-first-ordinal
// scenario through the unified BlobSet, per concrete type.
func TestBlobSet_CollidedID_ResolvesEntryByName_Unified(t *testing.T) {
	base := time.Now().Truncate(time.Hour)
	later := base.Add(time.Hour)

	num1 := encodeNumericSeries(t, base, nil, numericSeries{cnA, []float64{1.0, 1.5}})
	num2 := encodeNumericSeries(t, later, nil,
		numericSeries{cnB, []float64{70, 71}},
		numericSeries{cnA, []float64{80, 81}},
	)
	txt1 := encodeTextSeries(t, base, textSeries{cnA, []string{"a0", "a1"}})
	txt2 := encodeTextSeries(t, later,
		textSeries{cnB, []string{"b0", "b1"}},
		textSeries{cnA, []string{"a2", "a3"}},
	)

	bs := NewBlobSet([]NumericBlob{num1, num2}, []TextBlob{txt1, txt2})
	require.NotNil(t, bs.numericIdentity)
	require.NotNil(t, bs.textIdentity)

	wantNum := []float64{1.0, 1.5, 80, 81}
	wantTxt := []string{"a0", "a1", "a2", "a3"}
	wantTs := []int64{
		base.UnixMicro(), base.UnixMicro() + 1_000_000,
		later.UnixMicro(), later.UnixMicro() + 1_000_000,
	}

	require.Equal(t, len(wantNum), bs.MetricLen(cnH))
	require.Equal(t, wantNum, collectSeq2(bs.AllNumericValues(cnH)))
	require.Equal(t, wantTxt, collectSeq2(bs.AllTextValues(cnH)))
	require.Equal(t, wantTs, collectSeq2(bs.AllTimestamps(cnH)), "numeric-first precedence")
	require.Equal(t, []string{numericTag(1.0), numericTag(1.5), numericTag(80), numericTag(81)},
		collectSeq2(bs.AllTags(cnH)))
	require.Equal(t, wantNum, collectNumericDataPointValues(bs.AllNumerics(cnH)))
	require.Equal(t, wantTxt, collectTextValues(bs.AllTexts(cnH)))

	for i := range wantNum {
		v, ok := bs.NumericValueAt(cnH, i)
		require.True(t, ok)
		require.Equal(t, wantNum[i], v)
		tv, ok := bs.TextValueAt(cnH, i)
		require.True(t, ok)
		require.Equal(t, wantTxt[i], tv)
		ts, ok := bs.TimestampAt(cnH, i)
		require.True(t, ok)
		require.Equal(t, wantTs[i], ts)
		tag, ok := bs.TagAt(cnH, i)
		require.True(t, ok)
		require.Equal(t, numericTag(wantNum[i]), tag)
		dp, ok := bs.NumericAt(cnH, i)
		require.True(t, ok)
		require.Equal(t, wantNum[i], dp.Val)
		tdp, ok := bs.TextAt(cnH, i)
		require.True(t, ok)
		require.Equal(t, wantTxt[i], tdp.Val)
	}

	require.Equal(t, wantTs[len(wantTs)-1]-wantTs[0], bs.MetricDuration(cnH))

	mmH, ok := bs.MaterializeNumericMetric(cnH)
	require.True(t, ok)
	require.Equal(t, wantNum, mmH.Values)
	tmH, ok := bs.MaterializeTextMetric(cnH)
	require.True(t, ok)
	require.Equal(t, wantTxt, tmH.Values)
}

// TestBlobSet_StrippedMember_AttachesToFirstNameOnly is the name-keyed dual: a stripped
// member's data hash-matches BOTH colliding names, so without consulting the set
// identity it is counted for each of them. It belongs to the FIRST colliding name only.
func TestBlobSet_StrippedMember_AttachesToFirstNameOnly(t *testing.T) {
	base := time.Now().Truncate(time.Hour)
	later := base.Add(time.Hour)

	t.Run("numeric", func(t *testing.T) {
		collision := encodeNumericSeries(t, base, nil,
			numericSeries{cnA, []float64{1.0, 1.5}},
			numericSeries{cnB, []float64{2.0, 2.5, 2.7}},
		)
		stripped := encodeStrippedNumeric(t, later, 9.0, 9.5)

		set, err := NewNumericBlobSet([]NumericBlob{collision, stripped})
		require.NoError(t, err)
		require.NotNil(t, set.identity)

		mat := set.Materialize()
		require.Equal(t, 4, mat.DataPointCountByName(cnA), "A absorbs the stripped member")
		require.Equal(t, 3, mat.DataPointCountByName(cnB), "B keeps only its own entry")

		// Name-keyed raw surfaces must agree with the materialized set.
		require.Equal(t, mat.DataPointCountByName(cnA), set.MetricLenByName(cnA))
		require.Equal(t, mat.DataPointCountByName(cnB), set.MetricLenByName(cnB))

		// B spans only its own window; only A reaches into the stripped member's window.
		require.Equal(t, int64(2_000_000), set.MetricDurationByName(cnB))
		require.Equal(t, int64(3_601_000_000), set.MetricDurationByName(cnA))

		// ID-keyed access resolves to A, so it matches A's name-keyed answers.
		require.Equal(t, set.MetricLenByName(cnA), set.MetricLen(cnH))
		require.Equal(t, set.MetricDurationByName(cnA), set.MetricDuration(cnH))
	})

	t.Run("text", func(t *testing.T) {
		collision := encodeTextSeries(t, base,
			textSeries{cnA, []string{"a0", "a1"}},
			textSeries{cnB, []string{"b0", "b1", "b2"}},
		)
		stripped := encodeStrippedText(t, later, "s0", "s1")

		set, err := NewTextBlobSet([]TextBlob{collision, stripped})
		require.NoError(t, err)
		require.NotNil(t, set.identity)

		mat := set.Materialize()
		require.Equal(t, 4, mat.DataPointCountByName(cnA))
		require.Equal(t, 3, mat.DataPointCountByName(cnB))

		require.Equal(t, mat.DataPointCountByName(cnA), set.MetricLenByName(cnA))
		require.Equal(t, mat.DataPointCountByName(cnB), set.MetricLenByName(cnB))
		require.Equal(t, int64(2_000_000), set.MetricDurationByName(cnB))
		require.Equal(t, int64(3_601_000_000), set.MetricDurationByName(cnA))

		// The name-keyed iterators must not replay the stripped member under B either.
		require.Equal(t, []string{"a0", "a1", "s0", "s1"}, collectSeq(set.AllValuesByName(cnA)))
		require.Equal(t, []string{"b0", "b1", "b2"}, collectSeq(set.AllValuesByName(cnB)))
		require.Equal(t, []string{"b0", "b1", "b2"}, collectTextValues(set.AllByName(cnB)))
		require.Len(t, collectSeq(set.AllTimestampsByName(cnB)), 3)
		require.Len(t, collectSeq(set.AllTagsByName(cnB)), 3)
	})

	t.Run("unified", func(t *testing.T) {
		numColl := encodeNumericSeries(t, base, nil,
			numericSeries{cnA, []float64{1.0, 1.5}},
			numericSeries{cnB, []float64{2.0, 2.5, 2.7}},
		)
		numStripped := encodeStrippedNumeric(t, later, 9.0, 9.5)
		bs := NewBlobSet([]NumericBlob{numColl, numStripped}, nil)
		require.NotNil(t, bs.numericIdentity)

		mat := bs.MaterializeNumeric()
		require.Equal(t, mat.DataPointCountByName(cnA), bs.MetricLenByName(cnA))
		require.Equal(t, mat.DataPointCountByName(cnB), bs.MetricLenByName(cnB))
		require.Equal(t, int64(2_000_000), bs.MetricDurationByName(cnB))

		require.Equal(t, []float64{2.0, 2.5, 2.7}, collectSeq2(bs.AllNumericValuesByName(cnB)))
		require.Len(t, collectSeq2(bs.AllTimestampsByName(cnB)), 3)
		require.Len(t, collectSeq2(bs.AllTagsByName(cnB)), 3)
		_, ok := bs.NumericValueAtByName(cnB, 3)
		require.False(t, ok, "the stripped member must not extend B")
	})
}

// TestBlobSet_Construction_AllocContract pins what set construction costs. A set with no
// names payload and a set whose members are not both names-bearing never scan entries; a
// names-bearing multi-member set pays one bounded scratch map for the separated
// cross-member collision probe.
func TestBlobSet_Construction_AllocContract(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation assertions are unreliable under -race")
	}

	base := time.Now().Truncate(time.Hour)

	t.Run("names-free members", func(t *testing.T) {
		b1 := encodeStrippedNumeric(t, base, 1.0, 2.0)
		b2 := encodeStrippedNumeric(t, base.Add(time.Hour), 3.0)
		blobs := []NumericBlob{b1, b2}
		allocs := testing.AllocsPerRun(100, func() {
			set, err := NewNumericBlobSet(blobs)
			if err != nil || set.identity != nil {
				t.Fatal("unexpected set state")
			}
		})
		require.Equal(t, float64(1), allocs, "names-free construction is the member copy only")
	})

	t.Run("single names-bearing member", func(t *testing.T) {
		named := encodeNamedNumeric(t, base, "metric.one", 1.0, 2.0)
		stripped := encodeStrippedNumeric(t, base.Add(time.Hour), 3.0)
		blobs := []NumericBlob{named, stripped}
		allocs := testing.AllocsPerRun(100, func() {
			set, err := NewNumericBlobSet(blobs)
			if err != nil || set.identity != nil {
				t.Fatal("unexpected set state")
			}
		})
		require.Equal(t, float64(1), allocs,
			"a separated cross-member collision needs two names-bearing members; one cannot collide")
	})

	t.Run("two names-bearing members", func(t *testing.T) {
		blobs := []NumericBlob{
			encodeWideNamedNumeric(t, base, 200),
			encodeWideNamedNumeric(t, base.Add(time.Hour), 200),
		}
		allocs := testing.AllocsPerRun(50, func() {
			set, err := NewNumericBlobSet(blobs)
			if err != nil || set.identity != nil {
				t.Fatal("unexpected set state")
			}
		})
		require.Equal(t, float64(1), allocs,
			"two names-bearing members probe each other's index directly — member copy only")
	})

	constructionAllocs := func(blobs []NumericBlob) float64 {
		return testing.AllocsPerRun(50, func() {
			set, err := NewNumericBlobSet(blobs)
			if err != nil || set.identity != nil {
				t.Fatal("unexpected set state")
			}
		})
	}

	// Every member carries the same metrics across time windows — the common multi-window
	// shape. The widest member is a superset of the others, so the probe settles every
	// binding against its index and needs no scratch at all.
	sameSchema := func(n int) []NumericBlob {
		blobs := make([]NumericBlob, n)
		for i := range blobs {
			blobs[i] = encodeWideNamedNumeric(t, base.Add(time.Duration(i)*time.Hour), 200)
		}

		return blobs
	}

	// Members that BARELY overlap: a small shared core plus 200 names of their own. The id
	// UNION — everything a first-seen-name scratch map would have to hold — therefore grows
	// with member count, which is exactly what a pre-size taken from the widest single
	// member cannot cover.
	barelyOverlapping := func(n int) []NumericBlob {
		blobs := make([]NumericBlob, n)
		for i := range blobs {
			names := append(wideNames("shared", 8), wideNames(fmt.Sprintf("own%02d", i), 200)...)
			blobs[i] = encodeNamesNumeric(t, base.Add(time.Duration(i)*time.Hour), names)
		}

		return blobs
	}

	t.Run("three or more names-bearing members, same schema", func(t *testing.T) {
		three := constructionAllocs(sameSchema(3))
		require.Equal(t, float64(1), three,
			"when the widest member declares every id, the probe resolves them in its index — member copy only")
		require.Equal(t, three, constructionAllocs(sameSchema(16)),
			"probe allocation must not scale with member count")
	})

	t.Run("three or more names-bearing members, barely overlapping", func(t *testing.T) {
		three := constructionAllocs(barelyOverlapping(3))
		require.Equal(t, float64(2), three,
			"member copy + ONE exactly-sized scratch for the bindings the widest member does not declare")
		require.Equal(t, three, constructionAllocs(barelyOverlapping(8)),
			"probe allocation must not scale with member count when the id union does")
		require.Equal(t, three, constructionAllocs(barelyOverlapping(16)),
			"probe allocation must not scale with member count when the id union does")
	})
}

// TestBlobSet_SeparatedCollision_ThreeOrMoreMembers pins the three-plus-member probe. A
// separated collision — one id bound to two names in two different members, neither
// internally collided — must be found whether or not the member the probe uses as its
// reference index (the widest one) declares the colliding id at all. Missing one silently
// reinstates the A++B frankenseries.
func TestBlobSet_SeparatedCollision_ThreeOrMoreMembers(t *testing.T) {
	base := time.Now().Truncate(time.Hour)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Hour) }

	// A resolves alone under H (it is first in canonical order) and B keeps its own series;
	// a probe that missed the collision would report H as A ++ B.
	requireSeparated := func(t *testing.T, set NumericBlobSet) {
		t.Helper()
		require.NotNil(t, set.identity, "a separated cross-member collision must build the identity table")
		require.Equal(t, 2, set.MetricLen(cnH), "H must resolve to A alone, never A ++ B")
		mmA, ok := set.MaterializeMetricByName(cnA)
		require.True(t, ok)
		require.Len(t, mmA.Values, 2)
		mmB, ok := set.MaterializeMetricByName(cnB)
		require.True(t, ok)
		require.Len(t, mmB.Values, 2)
	}

	t.Run("colliding id declared by the widest member", func(t *testing.T) {
		set, err := NewNumericBlobSet([]NumericBlob{
			encodeNamesNumeric(t, at(0), append(wideNames("wide", 20), cnA)),
			encodeNamesNumeric(t, at(1), []string{"solo.one"}),
			encodeNamesNumeric(t, at(2), []string{cnB}),
		})
		require.NoError(t, err)
		requireSeparated(t, set)
	})

	t.Run("colliding id absent from the widest member", func(t *testing.T) {
		// The widest member declares neither colliding name, so the reference-index pass
		// cannot see the disagreement: it exists only among ids that member lacks.
		set, err := NewNumericBlobSet([]NumericBlob{
			encodeNamesNumeric(t, at(0), wideNames("wide", 20)),
			encodeNamesNumeric(t, at(1), []string{"solo.one", cnA}),
			encodeNamesNumeric(t, at(2), []string{"solo.two", cnB}),
		})
		require.NoError(t, err)
		requireSeparated(t, set)
	})

	t.Run("colliding id absent from the widest member, non-adjacent members", func(t *testing.T) {
		// The two colliding members are separated by unrelated members, so a probe that
		// only compared neighbours would miss them.
		set, err := NewNumericBlobSet([]NumericBlob{
			encodeNamesNumeric(t, at(0), wideNames("wide", 20)),
			encodeNamesNumeric(t, at(1), []string{"solo.one", cnA}),
			encodeNamesNumeric(t, at(2), wideNames("mid", 5)),
			encodeNamesNumeric(t, at(3), wideNames("late", 5)),
			encodeNamesNumeric(t, at(4), []string{"solo.two", cnB}),
		})
		require.NoError(t, err)
		requireSeparated(t, set)
	})

	t.Run("text members, colliding id absent from the widest member", func(t *testing.T) {
		// Text members index ids through a hash map instead of a sorted id slice, so the
		// probe's reference lookups take the V1/text branch of getOrdinal.
		series := func(names ...string) []textSeries {
			out := make([]textSeries, len(names))
			for i, name := range names {
				out[i] = textSeries{name: name, vals: []string{"v0", "v1"}}
			}

			return out
		}
		set, err := NewTextBlobSet([]TextBlob{
			encodeTextSeries(t, at(0), series(wideNames("wide", 20)...)...),
			encodeTextSeries(t, at(1), series("solo.one", cnA)...),
			encodeTextSeries(t, at(2), series("solo.two", cnB)...),
		})
		require.NoError(t, err)
		require.NotNil(t, set.identity, "a separated cross-member collision must build the identity table")
		require.Equal(t, 2, set.MetricLen(cnH), "H must resolve to A alone, never A ++ B")
		require.Equal(t, []string{"v0", "v1"}, collectSeq(set.AllValues(cnH)))
	})

	t.Run("barely overlapping members without a colliding pair", func(t *testing.T) {
		// Same shape as the fixtures above minus the colliding pair: the probe must not
		// report a collision just because members declare ids the widest one does not.
		blobs := make([]NumericBlob, 5)
		for i := range blobs {
			names := append(wideNames("shared", 4), wideNames(fmt.Sprintf("own%02d", i), 6)...)
			blobs[i] = encodeNamesNumeric(t, at(i), names)
		}
		set, err := NewNumericBlobSet(blobs)
		require.NoError(t, err)
		require.Nil(t, set.identity, "disjoint names that never collide are not a collision")
		require.Equal(t, 4+6*5, set.MetricCount())
	})

	t.Run("same name in every member is a merge, not a collision", func(t *testing.T) {
		blobs := make([]NumericBlob, 4)
		for i := range blobs {
			blobs[i] = encodeNamesNumeric(t, at(i), append(wideNames("wide", 3), cnA))
		}
		set, err := NewNumericBlobSet(blobs)
		require.NoError(t, err)
		require.Nil(t, set.identity, "one id bound to ONE name across members is today's merge")
		require.Equal(t, 8, set.MetricLen(cnH), "A merges across all four windows")
	})
}

// TestIndexMapsSetEntryLookups pins entryFor and entryForName, the pointer forms of
// resolveEntry and resolveEntryByName, for each kind of set member.
func TestIndexMapsSetEntryLookups(t *testing.T) {
	entry := func(id uint64, count int) section.NumericIndexEntry {
		return section.NumericIndexEntry{MetricID: id, Count: count}
	}

	// An internally collided member, a names-bearing member that binds cnB only,
	// and a stripped member (no names payload).
	collided := newNumericTestIndex(entry(cnH, 1), entry(cnH, 2))
	collided.names = []string{cnA, cnB}
	collided.byName = map[string]int{cnA: 0, cnB: 1}
	named := newNumericTestIndex(entry(cnH, 1))
	named.names = []string{cnB}
	stripped := newNumericTestIndex(entry(cnH, 1))

	const absent = -1
	tests := []struct {
		name         string
		index        *indexMaps[section.NumericIndexEntry]
		byName       bool // entryForName(target, skipStripped) instead of entryFor(id, target, collided)
		id           uint64
		target       string
		collided     bool
		skipStripped bool
		wantOrd      int
	}{
		{name: "non-collided id uses the id lookup", index: &collided, id: cnH, wantOrd: 0},
		{name: "collided id resolves by name inside a collided member", index: &collided, id: cnH, target: cnB, collided: true, wantOrd: 1},
		{name: "collided id excludes a member that binds the other name", index: &named, id: cnH, target: cnA, collided: true, wantOrd: absent},
		{name: "collided id attaches a stripped member by hash", index: &stripped, id: cnH, target: cnA, collided: true, wantOrd: 0},
		{name: "absent id", index: &stripped, id: 42, wantOrd: absent},
		{name: "names-bearing member matches its name", index: &named, byName: true, target: cnB, wantOrd: 0},
		{name: "names-bearing member ignores skipStripped", index: &named, byName: true, target: cnB, skipStripped: true, wantOrd: 0},
		{name: "names-bearing member rejects a hash-only match", index: &named, byName: true, target: cnA, wantOrd: absent},
		{name: "stripped member matches by hash", index: &stripped, byName: true, target: cnA, wantOrd: 0},
		{name: "stripped member is skipped on request", index: &stripped, byName: true, target: cnA, skipStripped: true, wantOrd: absent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				got    *section.NumericIndexEntry
				copied section.NumericIndexEntry
				ok     bool
			)
			if tt.byName {
				got = tt.index.entryForName(tt.target, tt.skipStripped)
				copied, ok = tt.index.resolveEntryByName(tt.target, tt.skipStripped)
			} else {
				got = tt.index.entryFor(tt.id, tt.target, tt.collided)
				copied, ok = tt.index.resolveEntry(tt.id, tt.target, tt.collided)
			}

			if tt.wantOrd == absent {
				require.Nil(t, got)
				require.False(t, ok)
				require.Zero(t, copied)

				return
			}

			require.Same(t, &tt.index.sorted[tt.wantOrd], got)
			require.True(t, ok)
			require.Equal(t, tt.index.sorted[tt.wantOrd], copied)
		})
	}
}

// TestPointAccessorsMatchIterators pins that the point accessors, which look an entry up by pointer,
// read the same entry as the iterators, which look it up by value, on decoded blobs and sets:
// V1 and V2 indexes, a collided ID, a retained name that only hash-matches, and stripped members.
func TestPointAccessorsMatchIterators(t *testing.T) {
	start := time.Unix(1700000000, 0)
	hour := func(n int) time.Time { return start.Add(time.Duration(n) * time.Hour) }
	numericPoint := func(ts int64, val float64, tag string) NumericDataPoint {
		return NumericDataPoint{Ts: ts, Val: val, Tag: tag}
	}
	textPoint := func(ts int64, val string, tag string) TextDataPoint {
		return TextDataPoint{Ts: ts, Val: val, Tag: tag}
	}

	// cnB sits before cnA, so the collided ID resolves to cnB's entry and cnA to a later one.
	collided := encodeNumericSeries(t, hour(0), nil,
		numericSeries{name: cnB, vals: []float64{1, 2, 3}}, numericSeries{name: cnA, vals: []float64{4, 5}})
	shared := encodeNumericSeries(t, hour(1), []NumericEncoderOption{WithSharedTimestamps()},
		numericSeries{name: "cpu", vals: []float64{6, 7, 8}}, numericSeries{name: "mem", vals: []float64{9, 10, 11}})
	namedA := encodeNamedNumeric(t, hour(2), cnA, 12, 13)
	stripped := encodeStrippedNumeric(t, hour(3), 14, 15, 16)
	require.True(t, shared.IsV2Layout())
	require.False(t, collided.IsV2Layout())

	textCollided := encodeTextSeries(t, hour(0),
		textSeries{name: cnB, vals: []string{"a", "b"}}, textSeries{name: cnA, vals: []string{"c"}})
	textNamedA := encodeNamedText(t, hour(1), cnA, "d", "e")
	textStripped := encodeStrippedText(t, hour(2), "f")

	ids := []uint64{cnH, hash.ID("cpu"), hash.ID("mem"), 42}
	names := []string{cnA, cnB, "cpu", "mem", "absent"}

	numericBlobs := map[string]NumericBlob{"collided": collided, "shared V2": shared, "named": namedA, "stripped": stripped}
	for label, b := range numericBlobs {
		t.Run("numeric blob/"+label, func(t *testing.T) {
			total := 0
			for _, id := range ids {
				total += requirePointsMatch(t, b.All(id), pointAt(t,
					func(i int) (int64, bool) { return b.TimestampAt(id, i) },
					func(i int) (float64, bool) { return b.ValueAt(id, i) },
					func(i int) (string, bool) { return b.TagAt(id, i) }, numericPoint))
			}
			for _, name := range names {
				total += requirePointsMatch(t, b.AllByName(name), pointAt(t,
					func(i int) (int64, bool) { return b.TimestampAtByName(name, i) },
					func(i int) (float64, bool) { return b.ValueAtByName(name, i) },
					func(i int) (string, bool) { return b.TagAtByName(name, i) }, numericPoint))
			}
			require.Positive(t, total)
		})
	}

	// The fixtures' own shape: first entry by ID, exact entry by name, no hash-only match.
	require.Equal(t, 3, requirePointsMatch(t, collided.All(cnH), func(i int) (NumericDataPoint, bool) {
		ts, _ := collided.TimestampAt(cnH, i)
		val, ok := collided.ValueAt(cnH, i)
		tag, _ := collided.TagAt(cnH, i)

		return numericPoint(ts, val, tag), ok
	}))
	_, ok := collided.ValueAtByName(cnA, 1)
	require.True(t, ok)
	_, ok = collided.ValueAtByName(cnA, 2)
	require.False(t, ok)
	_, ok = namedA.ValueAtByName(cnB, 0)
	require.False(t, ok, "cnB only hash-matches the retained name cnA")

	textBlobs := map[string]TextBlob{"collided": textCollided, "named": textNamedA, "stripped": textStripped}
	for label, b := range textBlobs {
		t.Run("text blob/"+label, func(t *testing.T) {
			total := 0
			for _, id := range ids {
				total += requirePointsMatch(t, b.All(id), pointAt(t,
					func(i int) (int64, bool) { return b.TimestampAt(id, i) },
					func(i int) (string, bool) { return b.ValueAt(id, i) },
					func(i int) (string, bool) { return b.TagAt(id, i) }, textPoint))
			}
			for _, name := range names {
				total += requirePointsMatch(t, b.AllByName(name), pointAt(t,
					func(i int) (int64, bool) { return b.TimestampAtByName(name, i) },
					func(i int) (string, bool) { return b.ValueAtByName(name, i) },
					func(i int) (string, bool) { return b.TagAtByName(name, i) }, textPoint))
			}
			require.Positive(t, total)
		})
	}

	t.Run("numeric blob set", func(t *testing.T) {
		set, err := NewNumericBlobSet([]NumericBlob{collided, shared, namedA, stripped})
		require.NoError(t, err)
		total := 0
		for _, id := range ids {
			total += requirePointsMatch(t, set.All(id), pointAt(t,
				func(i int) (int64, bool) { return set.TimestampAt(id, i) },
				func(i int) (float64, bool) { return set.ValueAt(id, i) },
				func(i int) (string, bool) { return set.TagAt(id, i) }, numericPoint))
		}
		require.Positive(t, total)
	})

	t.Run("text blob set", func(t *testing.T) {
		set, err := NewTextBlobSet([]TextBlob{textCollided, textNamedA, textStripped})
		require.NoError(t, err)
		total := 0
		for _, id := range ids {
			total += requirePointsMatch(t, set.All(id), pointAt(t,
				func(i int) (int64, bool) { return set.TimestampAt(id, i) },
				func(i int) (string, bool) { return set.ValueAt(id, i) },
				func(i int) (string, bool) { return set.TagAt(id, i) }, textPoint))
		}
		require.Positive(t, total)
	})

	t.Run("blob set", func(t *testing.T) {
		// Numeric members alone serve a metric they hold, so the text side is checked on a text-only set.
		numericSet := NewBlobSet([]NumericBlob{collided, shared, namedA, stripped}, []TextBlob{textCollided, textNamedA, textStripped})
		textSet := NewBlobSet(nil, []TextBlob{textCollided, textNamedA, textStripped})
		total := 0
		for _, id := range ids {
			total += requirePointsMatch(t, numericSet.AllNumerics(id), func(i int) (NumericDataPoint, bool) { return numericSet.NumericAt(id, i) })
			total += requirePointsMatch(t, numericSet.AllNumerics(id), pointAt(t,
				func(i int) (int64, bool) { return numericSet.TimestampAt(id, i) },
				func(i int) (float64, bool) { return numericSet.NumericValueAt(id, i) },
				func(i int) (string, bool) { return numericSet.TagAt(id, i) }, numericPoint))
			total += requirePointsMatch(t, numericSet.AllTexts(id), func(i int) (TextDataPoint, bool) { return numericSet.TextAt(id, i) })
			total += requirePointsMatch(t, textSet.AllTexts(id), pointAt(t,
				func(i int) (int64, bool) { return textSet.TimestampAt(id, i) },
				func(i int) (string, bool) { return textSet.TextValueAt(id, i) },
				func(i int) (string, bool) { return textSet.TagAt(id, i) }, textPoint))
		}
		for _, name := range names {
			total += requirePointsMatch(t, numericSet.AllNumericsByName(name), func(i int) (NumericDataPoint, bool) { return numericSet.NumericAtByName(name, i) })
			total += requirePointsMatch(t, numericSet.AllNumericsByName(name), pointAt(t,
				func(i int) (int64, bool) { return numericSet.TimestampAtByName(name, i) },
				func(i int) (float64, bool) { return numericSet.NumericValueAtByName(name, i) },
				func(i int) (string, bool) { return numericSet.TagAtByName(name, i) }, numericPoint))
			total += requirePointsMatch(t, numericSet.AllTextsByName(name), func(i int) (TextDataPoint, bool) { return numericSet.TextAtByName(name, i) })
			total += requirePointsMatch(t, textSet.AllTextsByName(name), pointAt(t,
				func(i int) (int64, bool) { return textSet.TimestampAtByName(name, i) },
				func(i int) (string, bool) { return textSet.TextValueAtByName(name, i) },
				func(i int) (string, bool) { return textSet.TagAtByName(name, i) }, textPoint))
		}
		require.Positive(t, total)
	})
}

// requirePointsMatch checks that a point accessor returns, index by index,
// what the iterator over the same metric yields,
// and reports absence one past the end and at a negative index.
// It returns the number of points.
func requirePointsMatch[P comparable](t *testing.T, all iter.Seq2[int, P], at func(int) (P, bool)) int {
	t.Helper()
	n := 0
	for i, want := range all {
		got, ok := at(i)
		require.True(t, ok, "index %d", i)
		require.Equal(t, want, got, "index %d", i)
		n++
	}
	_, ok := at(n)
	require.False(t, ok, "index %d is past the end", n)
	_, ok = at(-1)
	require.False(t, ok, "a negative index")

	return n
}

// pointAt assembles a point accessor from three single-field accessors, which must agree on presence.
func pointAt[V, P any](
	t *testing.T,
	tsAt func(int) (int64, bool), valAt func(int) (V, bool), tagAt func(int) (string, bool),
	build func(ts int64, val V, tag string) P,
) func(int) (P, bool) {
	t.Helper()

	return func(i int) (P, bool) {
		ts, tsOk := tsAt(i)
		val, valOk := valAt(i)
		tag, tagOk := tagAt(i)
		require.Equal(t, tsOk, valOk, "index %d", i)
		require.Equal(t, tsOk, tagOk, "index %d", i)

		return build(ts, val, tag), tsOk
	}
}
