package blob

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
	"github.com/stretchr/testify/require"
)

// TestMetricNames_IndexOrderDeterministic verifies MetricNames() is index-order
// deterministic and stays parallel to MetricIDs() (hash(name[i]) == id[i]) across
// raw blobs, materialized blobs, and repeated decodes, for every numeric format
// and text.
func TestMetricNames_IndexOrderDeterministic(t *testing.T) {
	assertParallel := func(t *testing.T, names []string, ids []uint64) {
		t.Helper()
		require.Len(t, names, len(ids))
		for i := range names {
			require.Equal(t, ids[i], hash.ID(names[i]),
				"MetricNames[%d]=%q must hash to MetricIDs[%d]=%#x (index-order parallel)", i, names[i], i, ids[i])
		}
	}

	t.Run("numeric", func(t *testing.T) {
		for _, fc := range numericFormatCases() {
			t.Run(fc.name, func(t *testing.T) {
				specs := append([]numericMetricSpec{
					{name: "zzz.metric", points: 2, value: 1},
					{name: "aaa.metric", points: 3, value: 2},
					{name: "mmm.metric", points: 1, value: 3},
				}, fc.extra...)
				data := buildNamesNumeric(t, fc.opts, specs)

				dec, err := NewNumericDecoder(data)
				require.NoError(t, err)
				b, err := dec.Decode()
				require.NoError(t, err)

				names := b.MetricNames()
				assertParallel(t, names, b.MetricIDs())

				// Deterministic across a second decode.
				dec2, err := NewNumericDecoder(data)
				require.NoError(t, err)
				b2, err := dec2.Decode()
				require.NoError(t, err)
				require.Equal(t, names, b2.MetricNames())

				// Materialized blob keeps the same index order.
				require.Equal(t, names, b.Materialize().MetricNames())
			})
		}
	})

	t.Run("text", func(t *testing.T) {
		data := buildNamesText(t, []textMetricSpec{
			{name: "zzz.metric", values: []string{"a"}},
			{name: "aaa.metric", values: []string{"b", "c"}},
			{name: "mmm.metric", values: []string{"d"}},
		})
		dec, err := NewTextDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)

		names := b.MetricNames()
		assertParallel(t, names, b.MetricIDs())
		require.Equal(t, names, b.Materialize().MetricNames())
	})
}

// ==============================================================================
// Borrowed-names decode (strings alias the decoder input instead of being
// copied) plus exact negative membership across raw AND materialized surfaces
// (*Blob / Materialized*Blob / BlobSet / Materialized*BlobSet), all formats.
// ==============================================================================

// buildNamesNumeric encodes a names-bearing (WithMetricNames) numeric blob from
// the given specs under opts. Returns a freshly-allocated buffer the caller may
// mutate (to exercise the borrowed-alias contract).
func buildNamesNumeric(t *testing.T, opts []NumericEncoderOption, specs []numericMetricSpec) []byte {
	t.Helper()
	o := append([]NumericEncoderOption{WithMetricNames()}, opts...)

	return slices.Clone(buildNumeric(t, o, specs))
}

// buildNamesText encodes a names-bearing text blob (names are on by default for
// text) from the given specs. Returns a freshly-allocated, mutable buffer.
func buildNamesText(t *testing.T, specs []textMetricSpec) []byte {
	t.Helper()
	start := time.Now()
	enc, err := NewTextEncoder(start)
	require.NoError(t, err)
	for _, m := range specs {
		require.NoError(t, enc.StartMetricName(m.name, len(m.values)))
		for i, v := range m.values {
			ts := start.UnixMicro() + int64(i)*1_000_000
			require.NoError(t, enc.AddDataPoint(ts, v, m.tag))
		}
		require.NoError(t, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(t, err)

	return slices.Clone(data)
}

// numericFormatCases returns the numeric encoder-option sets and per-case filler
// metrics that exercise every on-wire format the retained-names path runs on.
func numericFormatCases() []struct {
	name  string
	opts  []NumericEncoderOption
	extra []numericMetricSpec
	check func(t *testing.T, data []byte)
} {
	return []struct {
		name  string
		opts  []NumericEncoderOption
		extra []numericMetricSpec
		check func(t *testing.T, data []byte)
	}{
		{
			name: "V1",
			opts: nil,
		},
		{
			name: "V2",
			opts: []NumericEncoderOption{WithBlobLayoutV2()},
			check: func(t *testing.T, data []byte) {
				hdr, err := section.ParseNumericHeader(data)
				require.NoError(t, err)
				require.True(t, hdr.Flag.IsV2())
			},
		},
		{
			name: "V2Ext",
			opts: []NumericEncoderOption{
				WithBlobLayoutV2(),
				WithTimestampEncoding(format.TypeRaw),
				WithValueEncoding(format.TypeRaw),
			},
			// Two 8192-point raw metrics push the last entry's cumulative offset
			// past NumericMaxOffset regardless of hash sort order, forcing V2Ext.
			extra: []numericMetricSpec{
				{name: "big.filler.metric.a", points: 8192, value: 9.0},
				{name: "big.filler.metric.b", points: 8192, value: 8.0},
			},
			check: func(t *testing.T, data []byte) {
				hdr, err := section.ParseNumericHeader(data)
				require.NoError(t, err)
				require.True(t, hdr.Flag.IsV2Ext(), "fixture must trigger V2Ext")
			},
		},
	}
}

// TestNegativeMembership_Numeric proves the retained-names hash+strcmp
// name-lookup path rejects an absent query that only HASH-collides with a
// stored name, on a no-collision names-bearing blob — across raw, materialized,
// set, and materialized-set surfaces, for every numeric format.
func TestNegativeMembership_Numeric(t *testing.T) {
	for _, fc := range numericFormatCases() {
		t.Run(fc.name, func(t *testing.T) {
			// Store NameA (plus fillers) but NOT NameB. NameB hash-collides with
			// NameA, so a hash-only lookup would false-positive.
			specs := []numericMetricSpec{
				{name: "svc.one", points: 3, value: 1.0},
				{name: cnA, points: 4, value: 2.0},
				{name: "svc.two", points: 3, value: 3.0},
			}
			specs = append(specs, fc.extra...)
			data := buildNamesNumeric(t, fc.opts, specs)
			if fc.check != nil {
				fc.check(t, data)
			}

			dec, err := NewNumericDecoder(data)
			require.NoError(t, err)
			b, err := dec.Decode()
			require.NoError(t, err)

			// No collision within the blob → byName stays nil (retained-names path).
			require.Nil(t, b.index.byName, "no-collision blob must not build byName")
			require.NotNil(t, b.index.names, "names must be retained")

			// Raw blob: stored name resolves, colliding-absent name does not.
			require.True(t, b.HasMetricName(cnA))
			require.False(t, b.HasMetricName(cnB), "absent colliding query must be not-found (raw)")
			require.Equal(t, 4, b.LenByName(cnA))
			require.Equal(t, 0, b.LenByName(cnB))

			// Materialized blob.
			mat := b.Materialize()
			require.True(t, mat.HasMetricName(cnA))
			require.False(t, mat.HasMetricName(cnB), "absent colliding query must be not-found (materialized)")

			// Blob set (membership routes through member HasMetricName/LenByName).
			set, err := NewNumericBlobSet([]NumericBlob{b})
			require.NoError(t, err)
			require.Positive(t, set.MetricLenByName(cnA))
			require.Equal(t, 0, set.MetricLenByName(cnB), "absent colliding query must be not-found (set)")

			// Materialized blob set.
			matSet := set.Materialize()
			require.True(t, matSet.HasMetricName(cnA))
			require.False(t, matSet.HasMetricName(cnB), "absent colliding query must be not-found (materialized set)")
		})
	}
}

// TestNegativeMembership_Text mirrors the numeric negative-membership coverage
// for the text blob/set surfaces (text stores names by default).
func TestNegativeMembership_Text(t *testing.T) {
	specs := []textMetricSpec{
		{name: "svc.one", values: []string{"a", "b"}},
		{name: cnA, values: []string{"x", "y", "z"}},
	}
	data := buildNamesText(t, specs)

	dec, err := NewTextDecoder(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)

	require.Nil(t, b.index.byName)
	require.NotNil(t, b.index.names)

	require.True(t, b.HasMetricName(cnA))
	require.False(t, b.HasMetricName(cnB), "absent colliding query must be not-found (raw text)")
	require.Equal(t, 3, b.LenByName(cnA))
	require.Equal(t, 0, b.LenByName(cnB))

	mat := b.Materialize()
	require.True(t, mat.HasMetricName(cnA))
	require.False(t, mat.HasMetricName(cnB))

	set, err := NewTextBlobSet([]TextBlob{b})
	require.NoError(t, err)
	require.Positive(t, set.MetricLenByName(cnA))
	require.Equal(t, 0, set.MetricLenByName(cnB))

	matSet := set.Materialize()
	require.True(t, matSet.HasMetricName(cnA))
	require.False(t, matSet.HasMetricName(cnB))
}

// TestBorrowed_Aliases proves the borrowed constructor keeps zero-copy names
// that alias the input buffer: mutating the backing array changes what the
// borrowed blob reports through MetricNames.
func TestBorrowed_Aliases_Numeric(t *testing.T) {
	const target = "cpu.usage.percent.borrowed"
	data := buildNamesNumeric(t, []NumericEncoderOption{WithBlobLayoutV2()}, []numericMetricSpec{
		{name: target, points: 3, value: 1.0},
		{name: "svc.other", points: 2, value: 2.0},
	})

	dec, err := NewNumericDecoderBorrowed(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)
	require.True(t, b.index.namesBorrowed, "borrowed decode must flag names as borrowed")
	require.Contains(t, b.MetricNames(), target)

	// Mutate the name's first byte in the backing buffer.
	idx := bytes.Index(data, []byte(target))
	require.GreaterOrEqual(t, idx, 0)
	data[idx] = 'X'
	mutated := "X" + target[1:]

	names := b.MetricNames()
	require.Contains(t, names, mutated, "borrowed blob must observe backing-array mutation")
	require.NotContains(t, names, target)
}

func TestBorrowed_Aliases_Text(t *testing.T) {
	const target = "text.metric.borrowed.alias"
	data := buildNamesText(t, []textMetricSpec{
		{name: target, values: []string{"a", "b"}},
		{name: "text.other", values: []string{"c"}},
	})

	dec, err := NewTextDecoderBorrowed(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)
	require.True(t, b.index.namesBorrowed)
	require.Contains(t, b.MetricNames(), target)

	idx := bytes.Index(data, []byte(target))
	require.GreaterOrEqual(t, idx, 0)
	data[idx] = 'Z'
	mutated := "Z" + target[1:]

	require.Contains(t, b.MetricNames(), mutated, "borrowed text blob must observe backing mutation")
}

// TestOwning_DoesNotAlias proves the default (owning) constructors copy names:
// mutating or fully reusing the backing buffer after decode leaves the blob intact.
func TestOwning_DoesNotAlias_Numeric(t *testing.T) {
	const target = "cpu.usage.percent.owning"
	data := buildNamesNumeric(t, []NumericEncoderOption{WithBlobLayoutV2()}, []numericMetricSpec{
		{name: target, points: 3, value: 1.0},
		{name: "svc.other", points: 2, value: 2.0},
	})

	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)
	require.False(t, b.index.namesBorrowed)

	ids := b.MetricIDs()

	// Mutate the name bytes, then fully zero the buffer (reuse-after-decode).
	idx := bytes.Index(data, []byte(target))
	require.GreaterOrEqual(t, idx, 0)
	data[idx] = 'X'
	for i := range data {
		data[i] = 0
	}

	require.Contains(t, b.MetricNames(), target, "owning blob must not observe backing mutation/reuse")
	require.True(t, b.HasMetricName(target))
	// Data still intact (payloads were copied/decompressed at Decode time).
	require.Equal(t, ids, b.MetricIDs())
	require.Equal(t, 3, b.LenByName(target))
}

func TestOwning_DoesNotAlias_Text(t *testing.T) {
	const target = "text.metric.owning"
	data := buildNamesText(t, []textMetricSpec{
		{name: target, values: []string{"a", "b"}},
	})

	dec, err := NewTextDecoder(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)
	require.False(t, b.index.namesBorrowed)

	for i := range data {
		data[i] = 0
	}
	require.Contains(t, b.MetricNames(), target)
	require.True(t, b.HasMetricName(target))
}

// TestMaterializeFromBorrowed_Clones proves that materialising a borrowed blob
// deep-clones names: a subsequent backing-array mutation leaves the materialized
// copy untouched while the borrowed raw blob still aliases (and observes it).
func TestMaterializeFromBorrowed_Clones_Numeric(t *testing.T) {
	const target = "cpu.usage.percent.mat"
	data := buildNamesNumeric(t, []NumericEncoderOption{WithBlobLayoutV2()}, []numericMetricSpec{
		{name: target, points: 3, value: 1.0},
		{name: "svc.other", points: 2, value: 2.0},
	})

	dec, err := NewNumericDecoderBorrowed(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)

	// Materialize (single blob + set) BEFORE mutation — these must clone.
	mat := b.Materialize()
	set, err := NewNumericBlobSet([]NumericBlob{b})
	require.NoError(t, err)
	matSet := set.Materialize()

	idx := bytes.Index(data, []byte(target))
	require.GreaterOrEqual(t, idx, 0)
	data[idx] = 'X'
	mutated := "X" + target[1:]

	// Raw borrowed blob observes the mutation …
	require.Contains(t, b.MetricNames(), mutated)
	// … but every materialized surface kept its own clone.
	require.Contains(t, mat.MetricNames(), target, "materialized blob must clone borrowed names")
	require.NotContains(t, mat.MetricNames(), mutated)
	require.True(t, mat.HasMetricName(target))
	require.Contains(t, matSet.MetricNames(), target, "materialized set must clone borrowed names")
	require.NotContains(t, matSet.MetricNames(), mutated)
	require.True(t, matSet.HasMetricName(target))
}

func TestMaterializeFromBorrowed_Clones_Text(t *testing.T) {
	const target = "text.metric.mat.clone"
	data := buildNamesText(t, []textMetricSpec{
		{name: target, values: []string{"a", "b"}},
		{name: "text.other", values: []string{"c"}},
	})

	dec, err := NewTextDecoderBorrowed(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)

	mat := b.Materialize()
	set, err := NewTextBlobSet([]TextBlob{b})
	require.NoError(t, err)
	matSet := set.Materialize()

	idx := bytes.Index(data, []byte(target))
	require.GreaterOrEqual(t, idx, 0)
	data[idx] = 'Z'
	mutated := "Z" + target[1:]

	require.Contains(t, b.MetricNames(), mutated)
	require.Contains(t, mat.MetricNames(), target)
	require.True(t, mat.HasMetricName(target))
	require.Contains(t, matSet.MetricNames(), target)
	require.True(t, matSet.HasMetricName(target))
}

// TestInPlaceStrip_WithLiveAliases exercises an in-place strip while a decoded
// blob shares the buffer. The owning blob (independent copy) survives the strip;
// a fresh decode of the stripped output has no names but identical data.
func TestInPlaceStrip_WithLiveAliases(t *testing.T) {
	const target = "svc.strip.alias"
	orig := buildNamesNumeric(t, []NumericEncoderOption{WithBlobLayoutV2()}, []numericMetricSpec{
		{name: target, points: 3, value: 5.0},
		{name: "svc.other", points: 2, value: 6.0},
	})

	buf := slices.Clone(orig)

	// Owning decode holds an independent copy of names + decoded payloads.
	decOwn, err := NewNumericDecoder(buf)
	require.NoError(t, err)
	bOwn, err := decOwn.Decode()
	require.NoError(t, err)
	wantIDs := bOwn.MetricIDs()
	wantNames := bOwn.MetricNames()

	// Strip in place — mutates buf (any borrowed alias over buf is now invalid).
	out, stripped, err := StripMetricNamesInPlace(buf)
	require.NoError(t, err)
	require.True(t, stripped)

	// Owning blob is unaffected by the in-place mutation.
	require.Equal(t, wantIDs, bOwn.MetricIDs())
	require.ElementsMatch(t, wantNames, bOwn.MetricNames())
	require.True(t, bOwn.HasMetricName(target))

	// Fresh decode of the stripped bytes: no names, identical ID-keyed data.
	dec2, err := NewNumericDecoder(out)
	require.NoError(t, err)
	b2, err := dec2.Decode()
	require.NoError(t, err)
	require.False(t, b2.HasMetricNames())
	require.Empty(t, b2.MetricNames())
	require.ElementsMatch(t, wantIDs, b2.MetricIDs())
	for _, id := range wantIDs {
		require.Equal(t, bOwn.Len(id), b2.Len(id))
	}
}

// TestBorrowed_Collision confirms the borrowed path still handles a real
// collision correctly: byName is built (from borrowed strings) and both names
// resolve to their own entries; materialising clones them.
func TestBorrowed_Collision_Numeric(t *testing.T) {
	data := slices.Clone(encodeCollisionNumeric(t, WithBlobLayoutV2()))

	dec, err := NewNumericDecoderBorrowed(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)
	require.NotNil(t, b.index.byName, "collision must build byName even on borrowed decode")
	require.ElementsMatch(t, []string{cnA, cnB}, b.MetricNames())

	mat := b.Materialize()
	require.True(t, mat.HasMetricName(cnA))
	require.True(t, mat.HasMetricName(cnB))

	// Mutate NameA's bytes in the buffer; the materialized clone is unaffected.
	idx := bytes.Index(data, []byte(cnA))
	require.GreaterOrEqual(t, idx, 0)
	data[idx] = 'q'
	require.True(t, mat.HasMetricName(cnA), "materialized clone unaffected by backing mutation")
	require.True(t, mat.HasMetricName(cnB))
}
