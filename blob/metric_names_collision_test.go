package blob

import (
	"testing"
	"time"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/collisiontest"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/section"
	"github.com/stretchr/testify/require"
)

// nameA/nameB collide under xxHash64 (see internal/collisiontest).
const (
	cnA = collisiontest.NameA
	cnB = collisiontest.NameB
	cnH = collisiontest.CollisionID
)

// encodeCollisionNumeric encodes a 2-metric numeric blob for the colliding pair
// A,B (inserted in that order), each with distinct constant values so payloads
// are distinguishable. opts allow layout selection.
func encodeCollisionNumeric(t *testing.T, opts ...NumericEncoderOption) []byte {
	t.Helper()
	start := time.Now()
	enc, err := NewNumericEncoder(start, opts...)
	require.NoError(t, err)

	require.NoError(t, enc.StartMetricName(cnA, 2))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1.0, ""))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro()+1_000_000, 1.5, ""))
	require.NoError(t, enc.EndMetric())

	require.NoError(t, enc.StartMetricName(cnB, 3))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 2.0, ""))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro()+1_000_000, 2.5, ""))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro()+2_000_000, 2.7, ""))
	require.NoError(t, enc.EndMetric())

	data, err := enc.Finish()
	require.NoError(t, err)

	return data
}

// encodeCollisionNumericV2Ext encodes a real-collision pair (A,B) that forces
// the V2Ext (32-byte index) layout. V2Ext has no dedicated encoder option:
// NumericEncoder.selectIndexFormat (numeric_encoder.go) auto-upgrades from
// compact V2 to extended whenever any entry's delta offset exceeds
// section.NumericMaxOffset (65535) or its Count exceeds math.MaxUint16. This
// mirrors the proven technique in TestWithMetricNames_V2Ext
// (metric_names_option_test.go): raw timestamp/value encoding (8 bytes/point,
// no compression) plus a large first entry.
//
// A is inserted before B. Both hash to the same MetricID (cnH), and the V2
// sort is a stable sort keyed on MetricID alone (sortEntriesByMetricID), so
// equal-ID ties preserve insertion order: A always lands at index 0 and B at
// index 1. B's stored delta offset is therefore exactly A's payload byte
// length, so making A large enough (aPoints below) forces V2Ext for the whole
// blob while leaving B's own values unchanged from the V1/V2 fixture
// ([2.0, 2.5, 2.7]).
func encodeCollisionNumericV2Ext(t *testing.T) (data []byte, aVals []float64) {
	t.Helper()
	start := time.Now()
	enc, err := NewNumericEncoder(start,
		WithBlobLayoutV2(),
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeRaw),
	)
	require.NoError(t, err)

	// 8192 raw float64 points = 65536 bytes > NumericMaxOffset (65535), so B's
	// delta offset (A's payload length) forces the extended index format.
	const aPoints = 8192
	aVals = make([]float64, aPoints)
	require.NoError(t, enc.StartMetricName(cnA, aPoints))
	for i := 0; i < aPoints; i++ {
		aVals[i] = 1000.0 + float64(i)
		require.NoError(t, enc.AddDataPoint(start.UnixMicro()+int64(i)*1_000_000, aVals[i], ""))
	}
	require.NoError(t, enc.EndMetric())

	require.NoError(t, enc.StartMetricName(cnB, 3))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 2.0, ""))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro()+1_000_000, 2.5, ""))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro()+2_000_000, 2.7, ""))
	require.NoError(t, enc.EndMetric())

	data, err = enc.Finish()
	require.NoError(t, err)

	hdr, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	require.True(t, hdr.Flag.IsV2Ext(), "fixture must actually trigger V2Ext")

	return data, aVals
}

// Exercises a real collision pair through the numeric decoder, across every
// index layout the format supports: V1, compact V2, and extended V2Ext.
func TestCollisionPair_Numeric(t *testing.T) {
	type fixture struct {
		data  []byte
		aVals []float64
		bVals []float64
	}

	fixtures := map[string]func(t *testing.T) fixture{
		"V1": func(t *testing.T) fixture {
			return fixture{
				data:  encodeCollisionNumeric(t),
				aVals: []float64{1.0, 1.5},
				bVals: []float64{2.0, 2.5, 2.7},
			}
		},
		"V2": func(t *testing.T) fixture {
			return fixture{
				data:  encodeCollisionNumeric(t, WithBlobLayoutV2()),
				aVals: []float64{1.0, 1.5},
				bVals: []float64{2.0, 2.5, 2.7},
			}
		},
		"V2Ext": func(t *testing.T) fixture {
			data, aVals := encodeCollisionNumericV2Ext(t)

			return fixture{
				data:  data,
				aVals: aVals,
				bVals: []float64{2.0, 2.5, 2.7},
			}
		},
	}

	for name, build := range fixtures {
		t.Run(name, func(t *testing.T) {
			fx := build(t)

			dec, err := NewNumericDecoder(fx.data)
			require.NoError(t, err)
			b, err := dec.Decode()
			require.NoError(t, err)

			// Collision detected: byName built, both names retained.
			require.NotNil(t, b.index.byName, "collision must build byName")
			require.ElementsMatch(t, []string{cnA, cnB}, b.MetricNames())

			// Every ID-keyed surface counts both entries.
			require.Equal(t, 2, b.MetricCount())
			require.Equal(t, []uint64{cnH, cnH}, b.MetricIDs())
			require.True(t, b.HasMetricID(cnH))

			// GetByID resolves to the FIRST entry (A, inserted first).
			require.Equal(t, len(fx.aVals), b.Len(cnH), "first entry (A) point count")

			// Selective ByName paths return each metric's OWN payload.
			var aVals, bVals []float64
			for _, dp := range b.AllByName(cnA) {
				aVals = append(aVals, dp.Val)
			}
			for _, dp := range b.AllByName(cnB) {
				bVals = append(bVals, dp.Val)
			}
			require.Equal(t, fx.aVals, aVals)
			require.Equal(t, fx.bVals, bVals)
			require.True(t, b.HasMetricName(cnA))
			require.True(t, b.HasMetricName(cnB))
			require.Equal(t, len(fx.aVals), b.LenByName(cnA))
			require.Equal(t, len(fx.bVals), b.LenByName(cnB))

			// Materialized single-blob: both entries are kept, and ValueAtByName
			// resolves each colliding name to its own distinct payload.
			mat := b.Materialize()
			require.Equal(t, 2, mat.MetricCount())
			require.Equal(t, []uint64{cnH, cnH}, mat.MetricIDs())
			v, ok := mat.ValueAtByName(cnA, 0)
			require.True(t, ok)
			require.Equal(t, fx.aVals[0], v)
			v, ok = mat.ValueAtByName(cnB, len(fx.bVals)-1)
			require.True(t, ok)
			require.Equal(t, fx.bVals[len(fx.bVals)-1], v)

			// MaterializeMetricByName returns the resolved entry, not a re-lookup.
			mmA, ok := b.MaterializeMetricByName(cnA)
			require.True(t, ok)
			require.Equal(t, fx.aVals, mmA.Values)
			mmB, ok := b.MaterializeMetricByName(cnB)
			require.True(t, ok)
			require.Equal(t, fx.bVals, mmB.Values)
		})
	}
}

// Exercises a real collision pair through the text decoder.
func TestCollisionPair_Text(t *testing.T) {
	start := time.Now()
	enc, err := NewTextEncoder(start)
	require.NoError(t, err)

	require.NoError(t, enc.StartMetricName(cnA, 2))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro(), "a1", ""))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro()+1_000_000, "a2", ""))
	require.NoError(t, enc.EndMetric())
	require.NoError(t, enc.StartMetricName(cnB, 1))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro(), "b1", ""))
	require.NoError(t, enc.EndMetric())

	data, err := enc.Finish()
	require.NoError(t, err)

	dec, err := NewTextDecoder(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)

	require.NotNil(t, b.index.byName)
	require.Equal(t, 2, b.MetricCount())
	require.Equal(t, []uint64{cnH, cnH}, b.MetricIDs())
	require.ElementsMatch(t, []string{cnA, cnB}, b.MetricNames())

	var aVals, bVals []string
	for _, dp := range b.AllByName(cnA) {
		aVals = append(aVals, dp.Val)
	}
	for _, dp := range b.AllByName(cnB) {
		bVals = append(bVals, dp.Val)
	}
	require.Equal(t, []string{"a1", "a2"}, aVals)
	require.Equal(t, []string{"b1"}, bVals)
}

// Verifies V2 out-of-order insertion keeps names aligned with their entries
// after the encoder's MetricID sort permutation (regression coverage for a
// previously fixed desync bug). Uses the collision pair plus extra metrics
// inserted out of ID order.
func TestV2_OutOfOrder_NamesAligned(t *testing.T) {
	start := time.Now()
	enc, err := NewNumericEncoder(start, WithBlobLayoutV2())
	require.NoError(t, err)

	// Insert three metrics whose hashes are NOT in ascending order, plus the
	// colliding pair, so the encoder must sort and permute names.
	names := []string{"zzz.metric", cnA, "aaa.metric", cnB, "mmm.metric"}
	for i, nm := range names {
		require.NoError(t, enc.StartMetricName(nm, 1))
		require.NoError(t, enc.AddDataPoint(start.UnixMicro(), float64(i), ""))
		require.NoError(t, enc.EndMetric())
	}
	data, err := enc.Finish()
	require.NoError(t, err)

	dec, err := NewNumericDecoder(data)
	require.NoError(t, err)
	b, err := dec.Decode()
	require.NoError(t, err)

	// All names present and each resolves to its own value (name↔entry aligned).
	for i, nm := range names {
		vals := make([]float64, 0, 1)
		for _, dp := range b.AllByName(nm) {
			vals = append(vals, dp.Val)
		}
		require.Equal(t, []float64{float64(i)}, vals, "name %q must resolve to its own payload", nm)
	}
	require.Equal(t, 5, b.MetricCount())
}

// Verifies (A,H),(B,H),(A,H) is rejected with ErrMetricAlreadyStarted: after a
// collision, repeating an earlier name is still rejected.
func TestEncoder_RepeatedNameAfterCollision(t *testing.T) {
	start := time.Now()
	enc, err := NewNumericEncoder(start)
	require.NoError(t, err)

	require.NoError(t, enc.StartMetricName(cnA, 1))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 1, ""))
	require.NoError(t, enc.EndMetric())

	require.NoError(t, enc.StartMetricName(cnB, 1)) // collision, accepted
	require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 2, ""))
	require.NoError(t, enc.EndMetric())

	// Repeat A after the collision — the seen-name set must still reject it.
	err = enc.StartMetricName(cnA, 1)
	require.ErrorIs(t, err, errs.ErrMetricAlreadyStarted)
}

// Exercises the decoder-half scenario: a blob containing the same name twice is
// rejected at decode with ErrDuplicateMetricName. The fixture is built by
// splicing a duplicate-name payload into a valid collision blob (both names
// hash to the shared entry ID, so hash verification passes and only the
// duplicate-name check fails).
func TestDecoder_DuplicateName(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	build := func(t *testing.T, names []string) []byte {
		t.Helper()
		// Start from a valid collision blob (IDs [H,H], names [A,B]).
		data := encodeCollisionNumeric(t)
		hdr, err := section.ParseNumericHeader(data)
		require.NoError(t, err)

		oldNames, oldLen, err := ienc.DecodeMetricNames(data[section.HeaderSize:], engine)
		require.NoError(t, err)
		require.Len(t, oldNames, 2)

		newNames, err := ienc.EncodeMetricNames(names, engine)
		require.NoError(t, err)

		delta := len(newNames) - oldLen
		hdr.IndexOffset = uint32(int(hdr.IndexOffset) + delta)
		hdr.TimestampPayloadOffset = uint32(int(hdr.TimestampPayloadOffset) + delta)
		hdr.ValuePayloadOffset = uint32(int(hdr.ValuePayloadOffset) + delta)
		hdr.TagPayloadOffset = uint32(int(hdr.TagPayloadOffset) + delta)

		out := make([]byte, 0, len(data)+delta)
		out = append(out, hdr.Bytes()...)
		out = append(out, newNames...)
		out = append(out, data[section.HeaderSize+oldLen:]...)

		return out
	}

	t.Run("A_A repeat", func(t *testing.T) {
		data := build(t, []string{cnA, cnA})
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		_, err = dec.Decode()
		require.ErrorIs(t, err, errs.ErrDuplicateMetricName)
	})

	t.Run("B_B repeat", func(t *testing.T) {
		data := build(t, []string{cnB, cnB})
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		_, err = dec.Decode()
		require.ErrorIs(t, err, errs.ErrDuplicateMetricName)
	})

	t.Run("A_B valid still decodes", func(t *testing.T) {
		data := build(t, []string{cnA, cnB})
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		b, err := dec.Decode()
		require.NoError(t, err)
		require.Equal(t, 2, b.MetricCount())
	})
}
