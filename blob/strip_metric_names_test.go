package blob

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/section"
)

// stripFixedStart is a fixed encode start time so a names-bearing blob and its
// names-free twin differ ONLY by the names payload (identical StartTime), which
// is what makes byte-for-byte equivalence assertable.
var stripFixedStart = time.UnixMicro(1_700_000_000_000_000)

// encodeNumericAt encodes the given metrics at a fixed start time with opts.
func encodeNumericAt(t testing.TB, opts []NumericEncoderOption, metrics []numericMetricSpec) []byte {
	t.Helper()
	enc, err := NewNumericEncoder(stripFixedStart, opts...)
	require.NoError(t, err)

	for _, m := range metrics {
		require.NoError(t, enc.StartMetricName(m.name, m.points))
		for i := range m.points {
			ts := stripFixedStart.UnixMicro() + int64(i)*1_000_000
			require.NoError(t, enc.AddDataPoint(ts, m.value+float64(i), m.tag))
		}
		require.NoError(t, enc.EndMetric())
	}

	data, err := enc.Finish()
	require.NoError(t, err)

	return data
}

// textMetricSpec describes one text metric.
type textMetricSpec struct {
	name   string
	values []string
	tag    string
}

// encodeTextAt encodes text metrics at a fixed start time with opts.
func encodeTextAt(t testing.TB, opts []TextEncoderOption, metrics []textMetricSpec) []byte {
	t.Helper()
	enc, err := NewTextEncoder(stripFixedStart, opts...)
	require.NoError(t, err)

	for _, m := range metrics {
		require.NoError(t, enc.StartMetricName(m.name, len(m.values)))
		for i, v := range m.values {
			ts := stripFixedStart.UnixMicro() + int64(i)*1_000_000
			require.NoError(t, enc.AddDataPoint(ts, v, m.tag))
		}
		require.NoError(t, enc.EndMetric())
	}

	data, err := enc.Finish()
	require.NoError(t, err)

	return data
}

// ==============================================================================
// Byte-for-byte equivalence vs encode-without-names.
// ==============================================================================

func TestStrip_ByteEquivalence_Numeric(t *testing.T) {
	sorted := []numericMetricSpec{
		{name: "aaa.metric", points: 2, value: 1.0},
		{name: "bbb.metric", points: 3, value: 2.0},
		{name: "ccc.metric", points: 1, value: 3.0},
	}
	unsorted := []numericMetricSpec{
		{name: "zzz.metric", points: 2, value: 1.0},
		{name: "aaa.metric", points: 3, value: 2.0},
		{name: "mmm.metric", points: 1, value: 3.0},
	}
	single := []numericMetricSpec{{name: "solo.metric", points: 4, value: 7.0}}

	type variant struct {
		name    string
		opts    []NumericEncoderOption
		metrics []numericMetricSpec
	}

	variants := []variant{
		{"V1", nil, sorted},
		{"V1_unsorted", nil, unsorted},
		{"V1_tags", []NumericEncoderOption{WithTagsEnabled(true)}, sorted},
		{"V1_bigendian", []NumericEncoderOption{WithBigEndian()}, sorted},
		{"V2", []NumericEncoderOption{WithBlobLayoutV2()}, unsorted},
		{"V2_tags", []NumericEncoderOption{WithBlobLayoutV2(), WithTagsEnabled(true)}, unsorted},
		{"V2_bigendian", []NumericEncoderOption{WithBlobLayoutV2(), WithBigEndian()}, unsorted},
		{"V2_shared_ts", []NumericEncoderOption{WithBlobLayoutV2(), WithSharedTimestamps()}, sorted},
		{"single", nil, single},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			without := encodeNumericAt(t, v.opts, v.metrics)
			with := encodeNumericAt(t, append(append([]NumericEncoderOption(nil), v.opts...), WithMetricNames()), v.metrics)

			hdr, err := section.ParseNumericHeader(with)
			require.NoError(t, err)
			require.True(t, hdr.Flag.HasMetricNames(), "with-blob must carry names")

			assertStripEquivalent(t, with, without)
		})
	}
}

func TestStrip_ByteEquivalence_Numeric_V2Ext(t *testing.T) {
	// 8192 raw float64 points = 65536 bytes > NumericMaxOffset, forcing V2Ext.
	metrics := []numericMetricSpec{
		{name: "small.metric", points: 5, value: 1.0},
		{name: "large.metric", points: 8192, value: 2.0},
	}
	opts := append([]NumericEncoderOption{WithBlobLayoutV2()}, withRawCodecs()...)

	without := encodeNumericAt(t, opts, metrics)
	with := encodeNumericAt(t, append(append([]NumericEncoderOption(nil), opts...), WithMetricNames()), metrics)

	hdr, err := section.ParseNumericHeader(without)
	require.NoError(t, err)
	require.True(t, hdr.Flag.IsV2Ext(), "fixture must trigger V2Ext")

	assertStripEquivalent(t, with, without)
}

func TestStrip_ByteEquivalence_Numeric_65535(t *testing.T) {
	metrics := make([]numericMetricSpec, 65535)
	for i := range metrics {
		metrics[i] = numericMetricSpec{name: fmt.Sprintf("metric.%05d", i), points: 1, value: float64(i)}
	}

	without := encodeNumericAt(t, nil, metrics)
	with := encodeNumericAt(t, []NumericEncoderOption{WithMetricNames()}, metrics)

	assertStripEquivalent(t, with, without)
}

func TestStrip_ByteEquivalence_Text(t *testing.T) {
	metrics := []textMetricSpec{
		{name: "zzz.text", values: []string{"a", "bb"}},
		{name: "aaa.text", values: []string{"ccc"}},
		{name: "mmm.text", values: []string{"d", "e", "f"}},
	}

	variants := []struct {
		name string
		opts []TextEncoderOption
	}{
		{"default", nil},
		{"tags", []TextEncoderOption{WithTextTagsEnabled(true)}},
		{"bigendian", []TextEncoderOption{WithTextBigEndian()}},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			specs := metrics
			// "without" opts out of the unconditional names payload.
			withoutOpts := append(append([]TextEncoderOption(nil), v.opts...), WithoutMetricNames())
			without := encodeTextAt(t, withoutOpts, specs)
			with := encodeTextAt(t, v.opts, specs)

			hdr, err := section.ParseTextHeader(with)
			require.NoError(t, err)
			require.True(t, hdr.Flag.HasMetricNames(), "text default must carry names")
			hdrWithout, err := section.ParseTextHeader(without)
			require.NoError(t, err)
			require.False(t, hdrWithout.Flag.HasMetricNames(), "WithoutMetricNames must drop names")

			assertStripEquivalent(t, with, without)
		})
	}
}

func TestStrip_ByteEquivalence_Text_Single(t *testing.T) {
	metrics := []textMetricSpec{{name: "solo.text", values: []string{"x", "y", "z"}}}
	without := encodeTextAt(t, []TextEncoderOption{WithoutMetricNames()}, metrics)
	with := encodeTextAt(t, nil, metrics)
	assertStripEquivalent(t, with, without)
}

// assertStripEquivalent asserts strip(with) == without for both the append and
// in-place forms, that stripped==true, and that the stripped output decodes.
func assertStripEquivalent(t *testing.T, with, without []byte) {
	t.Helper()

	withCopy := append([]byte(nil), with...)

	// Append form.
	out, stripped, err := StripMetricNames(nil, with)
	require.NoError(t, err)
	require.True(t, stripped, "names must be strippable")
	require.Equal(t, without, out, "append strip must equal encode-without-names")
	require.Equal(t, withCopy, with, "src must be unmodified by append form")

	// In-place form (operates on a fresh copy since it mutates).
	buf := append([]byte(nil), with...)
	outIP, strippedIP, err := StripMetricNamesInPlace(buf)
	require.NoError(t, err)
	require.True(t, strippedIP)
	require.Equal(t, without, outIP, "in-place strip must equal encode-without-names")

	// The stripped blob must still decode.
	requireDecodes(t, out)
}

// requireDecodes asserts a blob decodes through the appropriate codec.
func requireDecodes(t *testing.T, data []byte) {
	t.Helper()
	if section.IsNumericBlob(data) {
		dec, err := NewNumericDecoder(data)
		require.NoError(t, err)
		_, err = dec.Decode()
		require.NoError(t, err)

		return
	}
	dec, err := NewTextDecoder(data)
	require.NoError(t, err)
	_, err = dec.Decode()
	require.NoError(t, err)
}

// withRawCodecs returns raw timestamp+value encoding options (forces predictable
// fixed-size payloads, used to trigger V2Ext via large offsets).
func withRawCodecs() []NumericEncoderOption {
	return []NumericEncoderOption{
		WithTimestampEncoding(format.TypeRaw),
		WithValueEncoding(format.TypeRaw),
	}
}

// ==============================================================================
// Only the names flag and absolute offsets change.
// ==============================================================================

func TestStrip_HeaderFields_Numeric(t *testing.T) {
	metrics := []numericMetricSpec{
		{name: "aaa.metric", points: 2, value: 1.0},
		{name: "bbb.metric", points: 3, value: 2.0},
	}
	with := encodeNumericAt(t, []NumericEncoderOption{WithBlobLayoutV2(), WithTagsEnabled(true), WithMetricNames()}, metrics)

	inHdr, err := section.ParseNumericHeader(with)
	require.NoError(t, err)
	namesSize := int(inHdr.IndexOffset) - section.HeaderSize
	require.Positive(t, namesSize)

	out, stripped, err := StripMetricNames(nil, with)
	require.NoError(t, err)
	require.True(t, stripped)

	outHdr, err := section.ParseNumericHeader(out)
	require.NoError(t, err)

	// Names flag cleared.
	require.False(t, outHdr.Flag.HasMetricNames())
	// Absolute offsets reduced by exactly namesSize.
	require.Equal(t, uint32(section.HeaderSize), outHdr.IndexOffset)
	require.Equal(t, inHdr.TimestampPayloadOffset-uint32(namesSize), outHdr.TimestampPayloadOffset)
	require.Equal(t, inHdr.ValuePayloadOffset-uint32(namesSize), outHdr.ValuePayloadOffset)
	require.Equal(t, inHdr.TagPayloadOffset-uint32(namesSize), outHdr.TagPayloadOffset)
	// Everything else unchanged.
	require.Equal(t, inHdr.StartTime, outHdr.StartTime)
	require.Equal(t, inHdr.MetricCount, outHdr.MetricCount)
	require.Equal(t, inHdr.Flag.EncodingType, outHdr.Flag.EncodingType)
	require.Equal(t, inHdr.Flag.CompressionType, outHdr.Flag.CompressionType)
	require.Equal(t, inHdr.Flag.HasTag(), outHdr.Flag.HasTag())
	require.Equal(t, inHdr.Flag.GetMagicNumber(), outHdr.Flag.GetMagicNumber())
}

func TestStrip_HeaderFields_Text(t *testing.T) {
	metrics := []textMetricSpec{
		{name: "aaa.text", values: []string{"a", "b"}},
		{name: "bbb.text", values: []string{"c"}},
	}
	with := encodeTextAt(t, nil, metrics)

	inHdr, err := section.ParseTextHeader(with)
	require.NoError(t, err)
	namesSize := int(inHdr.IndexOffset) - section.HeaderSize
	require.Positive(t, namesSize)

	out, stripped, err := StripMetricNames(nil, with)
	require.NoError(t, err)
	require.True(t, stripped)

	outHdr, err := section.ParseTextHeader(out)
	require.NoError(t, err)

	require.False(t, outHdr.Flag.HasMetricNames())
	require.Equal(t, uint32(section.HeaderSize), outHdr.IndexOffset)
	require.Equal(t, inHdr.DataOffset-uint32(namesSize), outHdr.DataOffset)
	// DataSize (uncompressed size) is NOT an offset and must be untouched.
	require.Equal(t, inHdr.DataSize, outHdr.DataSize)
	require.Equal(t, inHdr.StartTime, outHdr.StartTime)
	require.Equal(t, inHdr.MetricCount, outHdr.MetricCount)
}

// ==============================================================================
// Refusal on real collisions and duplicate IDs; output byte-identical.
// ==============================================================================

func TestStrip_Refuse_Collision_Numeric(t *testing.T) {
	layouts := map[string][]NumericEncoderOption{
		"V1": nil,
		"V2": {WithBlobLayoutV2()},
	}
	for name, opts := range layouts {
		t.Run(name, func(t *testing.T) {
			data := encodeCollisionNumeric(t, opts...)
			assertRefused(t, data)
		})
	}
}

func TestStrip_Refuse_Collision_Text(t *testing.T) {
	enc, err := NewTextEncoder(stripFixedStart)
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricName(cnA, 1))
	require.NoError(t, enc.AddDataPoint(stripFixedStart.UnixMicro(), "a", ""))
	require.NoError(t, enc.EndMetric())
	require.NoError(t, enc.StartMetricName(cnB, 1))
	require.NoError(t, enc.AddDataPoint(stripFixedStart.UnixMicro(), "b", ""))
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)

	assertRefused(t, data)
}

// TestStrip_Refuse_NonAdjacentCollision builds a V1 blob whose two colliding
// entries are NOT adjacent in index order (A, X, B), exercising the sorted-scan
// duplicate detection.
func TestStrip_Refuse_NonAdjacentCollision(t *testing.T) {
	enc, err := NewNumericEncoder(stripFixedStart) // V1, insertion order preserved
	require.NoError(t, err)
	require.NoError(t, enc.StartMetricName(cnA, 1))
	require.NoError(t, enc.AddDataPoint(stripFixedStart.UnixMicro(), 1, ""))
	require.NoError(t, enc.EndMetric())
	require.NoError(t, enc.StartMetricName("separator.metric", 1))
	require.NoError(t, enc.AddDataPoint(stripFixedStart.UnixMicro(), 2, ""))
	require.NoError(t, enc.EndMetric())
	require.NoError(t, enc.StartMetricName(cnB, 1))
	require.NoError(t, enc.AddDataPoint(stripFixedStart.UnixMicro(), 3, ""))
	require.NoError(t, enc.EndMetric())
	data, err := enc.Finish()
	require.NoError(t, err)

	// Confirm the two cnH entries are non-adjacent in index order.
	hdr, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	eng := hdr.Flag.GetEndianEngine()
	idxOff := int(hdr.IndexOffset)
	id0 := eng.Uint64(data[idxOff : idxOff+8])
	id1 := eng.Uint64(data[idxOff+16 : idxOff+24])
	id2 := eng.Uint64(data[idxOff+32 : idxOff+40])
	require.Equal(t, cnH, id0)
	require.NotEqual(t, cnH, id1)
	require.Equal(t, cnH, id2)

	assertRefused(t, data)
}

// assertRefused asserts strip refuses (stripped=false, no error) and leaves the
// blob byte-identical, for both forms.
func assertRefused(t *testing.T, data []byte) {
	t.Helper()
	orig := append([]byte(nil), data...)

	out, stripped, err := StripMetricNames(nil, data)
	require.NoError(t, err)
	require.False(t, stripped, "load-bearing names must not be stripped")
	require.Equal(t, orig, out, "refused output must equal input")
	require.Equal(t, orig, data, "src must be unchanged")

	buf := append([]byte(nil), data...)
	outIP, strippedIP, err := StripMetricNamesInPlace(buf)
	require.NoError(t, err)
	require.False(t, strippedIP)
	require.Equal(t, orig, outIP)
	require.Equal(t, orig, buf, "in-place refusal must not modify buf")
}

// ==============================================================================
// ErrUnsortedIndex for descending V2/V2Ext (± shared timestamps).
// ==============================================================================

func TestStrip_UnsortedIndex_V2(t *testing.T) {
	cases := []struct {
		name     string
		opts     []NumericEncoderOption
		metrics  []numericMetricSpec
		entryLen int
		v2ext    bool
	}{
		{
			name:     "V2_compact",
			opts:     []NumericEncoderOption{WithBlobLayoutV2()},
			metrics:  []numericMetricSpec{{name: "metric.aaaaaaaa", points: 2, value: 1}, {name: "metric.bbbbbbbb", points: 2, value: 2}},
			entryLen: section.NumericIndexEntrySize,
		},
		{
			name:     "V2_shared_ts",
			opts:     []NumericEncoderOption{WithBlobLayoutV2(), WithSharedTimestamps()},
			metrics:  []numericMetricSpec{{name: "metric.aaaaaaaa", points: 2, value: 1}, {name: "metric.bbbbbbbb", points: 2, value: 2}},
			entryLen: section.NumericIndexEntrySize,
		},
		{
			name:     "V2Ext",
			opts:     append([]NumericEncoderOption{WithBlobLayoutV2()}, withRawCodecs()...),
			metrics:  []numericMetricSpec{{name: "metric.aaaaaaaa", points: 8192, value: 1}, {name: "metric.bbbbbbbb", points: 5, value: 2}},
			entryLen: section.NumericExtIndexEntrySize,
			v2ext:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			with := encodeNumericAt(t, append(append([]NumericEncoderOption(nil), tc.opts...), WithMetricNames()), tc.metrics)

			hdr, err := section.ParseNumericHeader(with)
			require.NoError(t, err)
			require.Equal(t, tc.v2ext, hdr.Flag.IsV2Ext())

			// Sanity: the sorted original decodes and looks up before we corrupt order.
			dec, err := NewNumericDecoder(with)
			require.NoError(t, err)
			b, err := dec.Decode()
			require.NoError(t, err)
			require.True(t, b.HasMetricName("metric.aaaaaaaa"))
			require.True(t, b.HasMetricName("metric.bbbbbbbb"))

			desc := makeDescendingV2(t, with, tc.entryLen)

			descOrig := append([]byte(nil), desc...)
			_, stripped, err := StripMetricNames(nil, desc)
			require.ErrorIs(t, err, errs.ErrUnsortedIndex)
			require.False(t, stripped)
			require.Equal(t, descOrig, desc, "src unchanged on error")

			buf := append([]byte(nil), desc...)
			_, strippedIP, err := StripMetricNamesInPlace(buf)
			require.ErrorIs(t, err, errs.ErrUnsortedIndex)
			require.False(t, strippedIP)
			require.Equal(t, descOrig, buf, "buf unchanged on error")
		})
	}
}

// makeDescendingV2 swaps the first two index entries and their two aligned
// (equal-length) names in a 2-metric V2 names-bearing blob, producing a
// descending-by-ID index whose name↔ID hashes still align.
func makeDescendingV2(t *testing.T, src []byte, entrySize int) []byte {
	t.Helper()
	out := append([]byte(nil), src...)
	hdr, err := section.ParseNumericHeader(src)
	require.NoError(t, err)
	eng := hdr.Flag.GetEndianEngine()
	idxOff := int(hdr.IndexOffset)

	// Swap the two fixed-size index entries.
	e0 := out[idxOff : idxOff+entrySize]
	e1 := out[idxOff+entrySize : idxOff+2*entrySize]
	tmp := append([]byte(nil), e0...)
	copy(e0, e1)
	copy(e1, tmp)

	// Swap the two equal-length names in the payload: [count][l0][n0][l1][n1].
	p := section.HeaderSize + 2
	l0 := int(eng.Uint16(out[p : p+2]))
	n0 := p + 2
	p = n0 + l0
	l1 := int(eng.Uint16(out[p : p+2]))
	n1 := p + 2
	require.Equal(t, l0, l1, "test fixture requires equal-length names")
	tmpn := append([]byte(nil), out[n0:n0+l0]...)
	copy(out[n0:n0+l0], out[n1:n1+l1])
	copy(out[n1:n1+l1], tmpn)

	return out
}

// ==============================================================================
// Negative membership: store only A, pin the post-strip false-positive.
// ==============================================================================

func TestStrip_NegativeMembership(t *testing.T) {
	// Store only A (no collision). With names retained, a query for B (which
	// hash-collides with A) is correctly rejected. After strip, it false-positives.
	with := encodeNumericAt(t, []NumericEncoderOption{WithMetricNames()}, []numericMetricSpec{
		{name: cnA, points: 2, value: 1.0},
	})

	// Before strip: names retained -> exact membership.
	decBefore, err := NewNumericDecoder(with)
	require.NoError(t, err)
	bBefore, err := decBefore.Decode()
	require.NoError(t, err)
	require.True(t, bBefore.HasMetricName(cnA), "stored name resolves")
	require.False(t, bBefore.HasMetricName(cnB), "colliding absent name rejected before strip")

	out, stripped, err := StripMetricNames(nil, with)
	require.NoError(t, err)
	require.True(t, stripped)

	// After strip: no names payload -> answers by hash, so B false-positives.
	decAfter, err := NewNumericDecoder(out)
	require.NoError(t, err)
	bAfter, err := decAfter.Decode()
	require.NoError(t, err)
	require.True(t, bAfter.HasMetricName(cnA), "stored name still resolves")
	require.True(t, bAfter.HasMetricName(cnB), "documented false-positive: B collides with A's hash")
	require.Empty(t, bAfter.MetricNames(), "enumeration is lost after strip")
}
