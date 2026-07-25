package blob

import (
	"bytes"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/section"
)

// baseNamesV2 builds a valid 2-metric names-bearing V2 numeric blob for mutation.
func baseNamesV2(t *testing.T) []byte {
	t.Helper()

	return encodeNumericAt(t, []NumericEncoderOption{WithBlobLayoutV2(), WithMetricNames()}, []numericMetricSpec{
		{name: "metric.alpha", points: 2, value: 1.0},
		{name: "metric.bravo", points: 3, value: 2.0},
	})
}

// mutateNumericHeader returns a copy of src with its header re-serialized after fn.
func mutateNumericHeader(t *testing.T, src []byte, fn func(*section.NumericHeader)) []byte {
	t.Helper()
	out := append([]byte(nil), src...)
	hdr, err := section.ParseNumericHeader(src)
	require.NoError(t, err)
	fn(&hdr)
	copy(out[:section.HeaderSize], hdr.Bytes())

	return out
}

// ==============================================================================
// Malformed inputs: no panic, an error, and dst unchanged.
// ==============================================================================

func TestStrip_Malformed(t *testing.T) {
	base := baseNamesV2(t)
	engine := endian.GetLittleEndianEngine()

	// helper: length of the encoded names payload in base.
	baseHdr, err := section.ParseNumericHeader(base)
	require.NoError(t, err)
	baseNamesSize := int(baseHdr.IndexOffset) - section.HeaderSize
	require.Positive(t, baseNamesSize)

	cases := []struct {
		name    string
		build   func() []byte
		wantErr error
	}{
		{
			name:    "unknown_magic",
			build:   func() []byte { b := append([]byte(nil), base...); b[1] = 0x00; return b },
			wantErr: errs.ErrInvalidMagicNumber,
		},
		{
			name:    "too_short",
			build:   func() []byte { return base[:section.HeaderSize-1] },
			wantErr: errs.ErrInvalidHeaderSize,
		},
		{
			name: "index_offset_32",
			build: func() []byte {
				return mutateNumericHeader(t, base, func(h *section.NumericHeader) { h.IndexOffset = 32 })
			},
			wantErr: errs.ErrMetricNamesExtentMismatch,
		},
		{
			name: "index_offset_33",
			build: func() []byte {
				return mutateNumericHeader(t, base, func(h *section.NumericHeader) { h.IndexOffset = 33 })
			},
			wantErr: errs.ErrMetricNamesExtentMismatch,
		},
		{
			name: "index_offset_beyond_len",
			build: func() []byte {
				return mutateNumericHeader(t, base, func(h *section.NumericHeader) { h.IndexOffset = uint32(len(base) + 100) })
			},
			wantErr: errs.ErrInvalidIndexOffsets,
		},
		{
			name: "index_offset_too_small_stale",
			build: func() []byte {
				// A stale (too-small) IndexOffset shifts where entry IDs are read,
				// so the aligned re-hash fails first — still detected, no panic.
				return mutateNumericHeader(t, base, func(h *section.NumericHeader) { h.IndexOffset -= 4 })
			},
			wantErr: errs.ErrHashMismatch,
		},
		{
			name: "offset_chain_violation",
			build: func() []byte {
				// TimestampPayloadOffset < IndexOffset.
				return mutateNumericHeader(t, base, func(h *section.NumericHeader) { h.TimestampPayloadOffset = h.IndexOffset - 1 })
			},
			wantErr: errs.ErrInvalidIndexOffsets,
		},
		{
			name: "oversized_metric_count",
			build: func() []byte {
				return mutateNumericHeader(t, base, func(h *section.NumericHeader) { h.MetricCount = 70000 })
			},
			wantErr: errs.ErrInvalidMetricCount,
		},
		{
			name: "hash_mismatch",
			build: func() []byte {
				b := append([]byte(nil), base...)
				// Flip a byte inside the first name (payload starts at 32+2).
				b[section.HeaderSize+4] ^= 0xFF

				return b
			},
			wantErr: errs.ErrHashMismatch,
		},
		{
			name: "name_length_prefix_overflow",
			build: func() []byte {
				b := append([]byte(nil), base...)
				// Corrupt the first name's length prefix to run past the extent.
				engine.PutUint16(b[section.HeaderSize+2:section.HeaderSize+4], 0xFFFF)

				return b
			},
			wantErr: errs.ErrMetricNamesExtentMismatch,
		},
		{
			name:    "duplicate_names",
			build:   func() []byte { return spliceCollisionNames(t, []string{cnA, cnA}) },
			wantErr: errs.ErrDuplicateMetricName,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.build()
			assertStripError(t, data, tc.wantErr)
		})
	}
}

// TestStrip_ZeroMetricNamesBlob covers the zero-metric names-bearing edge case
// shapes: an empty blob with a names payload still present as a 2-byte extent
// must still strip correctly.
func TestStrip_ZeroMetricNamesBlob(t *testing.T) {
	buildZero := func(indexOffset uint32, namesPayload []byte) []byte {
		h := section.NewNumericHeader(stripFixedStart)
		h.Flag.SetHasMetricNames(true)
		h.MetricCount = 0
		h.IndexOffset = indexOffset
		h.TimestampPayloadOffset = indexOffset
		h.ValuePayloadOffset = indexOffset
		h.TagPayloadOffset = indexOffset

		return append(h.Bytes(), namesPayload...)
	}

	t.Run("extent_exactly_2_succeeds", func(t *testing.T) {
		// count=0, extent is exactly the 2-byte count field.
		data := buildZero(34, []byte{0x00, 0x00})
		orig := append([]byte(nil), data...)

		out, stripped, err := StripMetricNames(nil, data)
		require.NoError(t, err)
		require.True(t, stripped, "zero-metric names blob with 2-byte extent strips successfully")
		require.Len(t, out, section.HeaderSize, "only the header remains")
		require.Equal(t, orig, data, "src unchanged")

		outHdr, err := section.ParseNumericHeader(out)
		require.NoError(t, err)
		require.False(t, outHdr.Flag.HasMetricNames())
		require.Equal(t, uint32(section.HeaderSize), outHdr.IndexOffset)
	})

	t.Run("extent_not_2_mismatch", func(t *testing.T) {
		// count=0 but 4-byte extent -> consumed(2) != extent(4).
		data := buildZero(36, []byte{0x00, 0x00, 0x00, 0x00})
		assertStripError(t, data, errs.ErrMetricNamesExtentMismatch)
	})
}

// assertStripError asserts both strip forms return wantErr, do not panic, leave
// src/buf unchanged, and never write into a non-empty dst prefix. It also pins
// the error-path contract: on error, the returned slice is dst/buf itself
// (same backing array), not nil and not a copy — byte-atomic, and safe
// for the idiomatic `dst, _, err = StripMetricNames(dst, src)` call shape.
func assertStripError(t *testing.T, data []byte, wantErr error) {
	t.Helper()
	orig := append([]byte(nil), data...)

	dstPrefix := []byte("SENTINEL")
	dstCopy := append([]byte(nil), dstPrefix...)
	out, stripped, err := StripMetricNames(dstPrefix, data)
	require.ErrorIs(t, err, wantErr)
	require.False(t, stripped)
	require.True(t, unsafe.SliceData(out) == unsafe.SliceData(dstPrefix), "out must be the same backing array as dst on error, not a copy")
	require.Equal(t, dstCopy, out, "out must equal dst's original content on error")
	require.Equal(t, dstCopy, dstPrefix, "dst must be unchanged on error")
	require.Equal(t, orig, data, "src must be unchanged on error")

	buf := append([]byte(nil), data...)
	outIP, strippedIP, err := StripMetricNamesInPlace(buf)
	require.ErrorIs(t, err, wantErr)
	require.False(t, strippedIP)
	require.True(t, unsafe.SliceData(outIP) == unsafe.SliceData(buf), "outIP must be the same backing array as buf on error, not a copy")
	require.Equal(t, orig, outIP, "outIP must equal buf's original content on error")
	require.Equal(t, orig, buf, "buf must be unchanged on error")
}

// spliceCollisionNames rebuilds a collision blob (IDs [H,H]) with a replacement
// names payload, mirroring the duplicate-name fixture construction used
// elsewhere in this package.
func spliceCollisionNames(t *testing.T, names []string) []byte {
	t.Helper()
	engine := endian.GetLittleEndianEngine()
	data := encodeCollisionNumeric(t)
	hdr, err := section.ParseNumericHeader(data)
	require.NoError(t, err)

	_, oldLen, err := ienc.DecodeMetricNames(data[section.HeaderSize:], engine)
	require.NoError(t, err)

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

// ==============================================================================
// Pass-through classes: strip succeeds AND output still fails decode
// identically to the input (fields strip's validation boundary does not
// re-check).
// ==============================================================================

func TestStrip_PassThrough_ReservedBytes(t *testing.T) {
	// V2Ext names blob (32-byte entries with reserved bytes at 24-31). The large
	// metric must sort first (so its 65536-byte length becomes the next entry's
	// offset delta and forces V2Ext) — "metric.aaaaaaaa" hashes below the other.
	metrics := []numericMetricSpec{
		{name: "metric.aaaaaaaa", points: 8192, value: 1.0},
		{name: "metric.bbbbbbbb", points: 5, value: 2.0},
	}
	opts := append([]NumericEncoderOption{WithBlobLayoutV2(), WithMetricNames()}, withRawCodecs()...)
	with := encodeNumericAt(t, opts, metrics)

	hdr, err := section.ParseNumericHeader(with)
	require.NoError(t, err)
	require.True(t, hdr.Flag.IsV2Ext())

	// Corrupt a reserved byte in the first extended index entry.
	idxOff := int(hdr.IndexOffset)
	corrupt := append([]byte(nil), with...)
	corrupt[idxOff+24] = 0x01 // reserved region [24:32]

	// The input already fails decode.
	assertDecodeErr(t, corrupt, errs.ErrInvalidReservedBytes)

	// Strip succeeds (reserved bytes are pass-through, not read by strip).
	out, stripped, err := StripMetricNames(nil, corrupt)
	require.NoError(t, err)
	require.True(t, stripped)

	// The stripped output fails decode with the SAME error.
	assertDecodeErr(t, out, errs.ErrInvalidReservedBytes)
}

func TestStrip_PassThrough_InvalidOffsetDelta(t *testing.T) {
	with := encodeNumericAt(t, []NumericEncoderOption{WithBlobLayoutV2(), WithMetricNames()}, []numericMetricSpec{
		{name: "metric.alpha", points: 2, value: 1.0},
		{name: "metric.bravo", points: 3, value: 2.0},
	})
	hdr, err := section.ParseNumericHeader(with)
	require.NoError(t, err)

	engine := hdr.Flag.GetEndianEngine()
	idxOff := int(hdr.IndexOffset)
	// Corrupt entry 1's TimestampOffset delta (bytes [10:12] of a compact entry)
	// so the reconstructed absolute offset runs past the timestamp payload — a
	// per-entry delta, which is outside strip's validation boundary and so is
	// copied through unchecked.
	corrupt := append([]byte(nil), with...)
	engine.PutUint16(corrupt[idxOff+section.NumericIndexEntrySize+10:idxOff+section.NumericIndexEntrySize+12], 0xFFFF)

	assertDecodeErr(t, corrupt, errs.ErrInvalidIndexOffsets)

	out, stripped, err := StripMetricNames(nil, corrupt)
	require.NoError(t, err)
	require.True(t, stripped)

	assertDecodeErr(t, out, errs.ErrInvalidIndexOffsets)
}

func TestStrip_PassThrough_MalformedSharedTable(t *testing.T) {
	metrics := []numericMetricSpec{
		{name: "metric.alpha", points: 2, value: 1.0},
		{name: "metric.bravo", points: 2, value: 2.0},
	}
	with := encodeNumericAt(t, []NumericEncoderOption{WithBlobLayoutV2(), WithSharedTimestamps(), WithMetricNames()}, metrics)

	hdr, err := section.ParseNumericHeader(with)
	require.NoError(t, err)
	require.True(t, hdr.Flag.HasSharedTimestamps())

	engine := hdr.Flag.GetEndianEngine()
	idxEnd := int(hdr.IndexOffset) + int(hdr.MetricCount)*hdr.Flag.IndexEntrySize()
	// Shared table layout: [groupCount][canonicalIdx]... Corrupt canonicalIdx to
	// exceed the metric count.
	corrupt := append([]byte(nil), with...)
	engine.PutUint16(corrupt[idxEnd+2:idxEnd+4], 0xFFFF)

	assertDecodeErr(t, corrupt, errs.ErrInvalidSharedTimestampTable)

	out, stripped, err := StripMetricNames(nil, corrupt)
	require.NoError(t, err)
	require.True(t, stripped)

	assertDecodeErr(t, out, errs.ErrInvalidSharedTimestampTable)
}

// assertDecodeErr asserts a numeric blob fails to decode with wantErr.
func assertDecodeErr(t *testing.T, data []byte, wantErr error) {
	t.Helper()
	dec, err := NewNumericDecoder(data)
	if err != nil {
		require.ErrorIs(t, err, wantErr)

		return
	}
	_, err = dec.Decode()
	require.ErrorIs(t, err, wantErr)
}

// ==============================================================================
// Aliasing battery: dst and src sharing a backing array must never become a
// silent in-place mutation of src.
// ==============================================================================

func TestStrip_Aliasing(t *testing.T) {
	// Expected stripped bytes (via a clean non-aliasing call).
	base := baseNamesV2(t)
	expected, stripped, err := StripMetricNames(nil, append([]byte(nil), base...))
	require.NoError(t, err)
	require.True(t, stripped)

	t.Run("nil_dst", func(t *testing.T) {
		src := append([]byte(nil), base...)
		out, ok, err := StripMetricNames(nil, src)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, expected, out)
		require.Equal(t, base, src, "src unchanged")
	})

	t.Run("non_empty_prefix_dst", func(t *testing.T) {
		src := append([]byte(nil), base...)
		out, ok, err := StripMetricNames([]byte("HDR"), src)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, append([]byte("HDR"), expected...), out)
		require.Equal(t, base, src, "src unchanged")
	})

	t.Run("dst_is_src_prefix_empty", func(t *testing.T) {
		// dst == src[:0] must NOT become an in-place mutation of src.
		src := append([]byte(nil), base...)
		out, ok, err := StripMetricNames(src[:0], src)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, expected, out)
		require.Equal(t, base, src, "src bytes must be preserved (not mutated in place)")
	})

	t.Run("exact_alias", func(t *testing.T) {
		// dst and src are literally the same slice (same pointer, len, cap), so
		// dst's "existing bytes" are the entire pre-strip blob. The documented
		// contract (out = dst ++ names-free blob) therefore requires dst's full
		// content as a prefix, NOT just the stripped bytes on their own — a
		// prior version of this assertion (`out == expected`) enshrined the
		// dropped-prefix bug, which silently discarded dst's contents whenever
		// dst aliased src. See blob/strip_metric_names.go's Aliasing doc.
		src := append([]byte(nil), base...)
		out, ok, err := StripMetricNames(src, src)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, append(append([]byte(nil), base...), expected...), out)
		require.Equal(t, base, src, "src unchanged")
	})

	t.Run("partial_overlap_interior", func(t *testing.T) {
		src := append([]byte(nil), base...)
		out, ok, err := StripMetricNames(src[5:5], src)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, expected, out)
		require.Equal(t, base, src, "src unchanged")
	})

	t.Run("spare_capacity_reuse_no_alias", func(t *testing.T) {
		src := append([]byte(nil), base...)
		dst := make([]byte, 0, len(base)) // separate backing, has spare cap
		out, ok, err := StripMetricNames(dst, src)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, expected, out)
		require.Equal(t, base, src, "src unchanged")
	})

	t.Run("refusal_alias_preserves_src", func(t *testing.T) {
		coll := encodeCollisionNumeric(t)
		collCopy := append([]byte(nil), coll...)
		out, ok, err := StripMetricNames(coll[:0], coll)
		require.NoError(t, err)
		require.False(t, ok, "collision refused")
		require.Equal(t, collCopy, out, "refused output equals input")
		require.Equal(t, collCopy, coll, "src preserved on aliased refusal")
	})
}

// ==============================================================================
// Every subtest above uses an empty-or-fresh dst, so none can observe a
// dropped prefix. aliasConfigs supplies a matrix of (dst, src) pairs
// that share a backing array, most with a NON-EMPTY dst, exercised below
// across all three outcomes: strip succeeds, refusal, and error. The critical
// missing direction is arena_prefix_before_src: dst's backing array starts
// *before* src (an arena prefix), which is the exact shape that reproduced
// the dropped-prefix bug — a non-aliasing dst with the same content keeps
// the prefix, an aliasing one silently dropped it.
// ==============================================================================

// aliasKV pairs a named aliasing configuration with its (dst, src, wantPrefix)
// builder. wantPrefix is the bytes dst is expected to contribute as an output
// prefix (nil where dst carries no bytes).
type aliasKV struct {
	name  string
	build func(payload []byte) (dst, src, wantPrefix []byte)
}

// aliasConfigs returns the aliasing matrix described above. prefixBytes is a
// fixed, recognizable non-empty prefix used wherever a config needs one.
func aliasConfigs() []aliasKV {
	prefixBytes := []byte("PREFIX0123456789")

	return []aliasKV{
		{
			name: "nil_dst",
			build: func(payload []byte) (dst, src, wantPrefix []byte) {
				return nil, append([]byte(nil), payload...), nil
			},
		},
		{
			name: "non_empty_prefix_dst_non_aliasing",
			build: func(payload []byte) (dst, src, wantPrefix []byte) {
				d := append([]byte(nil), prefixBytes...)

				return d, append([]byte(nil), payload...), append([]byte(nil), prefixBytes...)
			},
		},
		{
			name: "dst_is_src_prefix_empty",
			build: func(payload []byte) (dst, src, wantPrefix []byte) {
				s := append([]byte(nil), payload...)

				return s[:0], s, nil
			},
		},
		{
			name: "exact_alias",
			build: func(payload []byte) (dst, src, wantPrefix []byte) {
				s := append([]byte(nil), payload...)

				return s, s, append([]byte(nil), payload...)
			},
		},
		{
			// The dropped-prefix repro: dst's backing array starts BEFORE src (an
			// arena prefix). dst and src do not share logical bytes, but slicesOverlap
			// must still catch it via shared capacity, and the fix must preserve
			// dst's content as the output prefix.
			name: "arena_prefix_before_src",
			build: func(payload []byte) (dst, src, wantPrefix []byte) {
				arena := append(append([]byte(nil), prefixBytes...), payload...)

				return arena[:len(prefixBytes)], arena[len(prefixBytes):], append([]byte(nil), prefixBytes...)
			},
		},
		{
			// The other partial-overlap direction (dst inside src), but with a
			// NON-EMPTY dst that shares actual logical bytes with src (unlike
			// TestStrip_Aliasing's partial_overlap_interior, which uses src[5:5]).
			name: "partial_overlap_interior_nonempty",
			build: func(payload []byte) (dst, src, wantPrefix []byte) {
				s := append([]byte(nil), payload...)
				const n = 10

				return s[:n], s, append([]byte(nil), s[:n]...)
			},
		},
		{
			// dst has spare capacity reaching only partway into src's region
			// (not the whole tail), via a 3-index slice — a smaller-footprint
			// variant of arena_prefix_before_src.
			name: "spare_capacity_reuse_aliased",
			build: func(payload []byte) (dst, src, wantPrefix []byte) {
				arena := append(append([]byte(nil), prefixBytes...), payload...)
				d := arena[: len(prefixBytes) : len(prefixBytes)+5]

				return d, arena[len(prefixBytes):], append([]byte(nil), prefixBytes...)
			},
		},
	}
}

// TestStrip_Aliasing_StripPath_PrefixPreserved runs the aliasConfigs matrix
// over a clean names-bearing blob (strip succeeds) and asserts out == wantPrefix ++
// names-free-blob in every configuration.
func TestStrip_Aliasing_StripPath_PrefixPreserved(t *testing.T) {
	payload := baseNamesV2(t)
	expected, ok, err := StripMetricNames(nil, append([]byte(nil), payload...))
	require.NoError(t, err)
	require.True(t, ok)

	for _, cfg := range aliasConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			dst, src, wantPrefix := cfg.build(payload)
			srcOrig := append([]byte(nil), src...)

			out, stripped, err := StripMetricNames(dst, src)
			require.NoError(t, err)
			require.True(t, stripped)
			require.Equal(t, append(append([]byte(nil), wantPrefix...), expected...), out)
			require.Equal(t, srcOrig, src, "src must be unchanged")
		})
	}
}

// TestStrip_Aliasing_RefusalPath_PrefixPreserved runs the aliasConfigs matrix
// over a real-collision blob (refusal: stripped=false, err=nil) and asserts out ==
// wantPrefix ++ src in every configuration.
func TestStrip_Aliasing_RefusalPath_PrefixPreserved(t *testing.T) {
	payload := encodeCollisionNumeric(t)

	for _, cfg := range aliasConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			dst, src, wantPrefix := cfg.build(payload)
			srcOrig := append([]byte(nil), src...)

			out, stripped, err := StripMetricNames(dst, src)
			require.NoError(t, err)
			require.False(t, stripped, "collision must refuse")
			require.Equal(t, append(append([]byte(nil), wantPrefix...), srcOrig...), out)
			require.Equal(t, srcOrig, src, "src must be unchanged")
		})
	}
}

// TestStrip_Aliasing_ErrorPath_PrefixPreserved runs the aliasConfigs matrix
// over a malformed (hash-mismatch) blob and asserts every aliasing configuration
// still produces the documented error-path contract: out == dst (same backing
// array, unchanged), the enumerated error, and neither dst nor src touched.
// validateStrip always errors before dst is ever read or written, so this
// holds structurally, but it was never pinned for an ALIASED, non-empty dst.
func TestStrip_Aliasing_ErrorPath_PrefixPreserved(t *testing.T) {
	payload := append([]byte(nil), baseNamesV2(t)...)
	payload[section.HeaderSize+4] ^= 0xFF // corrupt first name's bytes -> ErrHashMismatch

	for _, cfg := range aliasConfigs() {
		t.Run(cfg.name, func(t *testing.T) {
			dst, src, _ := cfg.build(payload)
			srcOrig := append([]byte(nil), src...)
			dstOrig := append([]byte(nil), dst...)

			out, stripped, err := StripMetricNames(dst, src)
			require.ErrorIs(t, err, errs.ErrHashMismatch)
			require.False(t, stripped)
			// The error path returns dst unchanged (not nil) — same backing
			// array, so the idiomatic dst, _, err = StripMetricNames(dst, src)
			// never silently discards the caller's buffer.
			require.True(t, unsafe.SliceData(out) == unsafe.SliceData(dst), "out must be the same backing array as dst on error, not a copy")
			require.True(t, bytes.Equal(dstOrig, out), "out must equal dst's original content on error")
			require.Equal(t, srcOrig, src, "src must be unchanged on error")
			// bytes.Equal (not require.Equal) so a nil vs. non-nil-but-empty dst
			// (e.g. dst == src[:0]) doesn't register as a spurious mismatch —
			// only actual content changes should fail this assertion.
			require.True(t, bytes.Equal(dstOrig, dst), "dst must be unchanged on error")
		})
	}
}

func TestStrip_InPlace_Reslice(t *testing.T) {
	base := baseNamesV2(t)
	expected, _, err := StripMetricNames(nil, append([]byte(nil), base...))
	require.NoError(t, err)

	buf := append([]byte(nil), base...)
	out, stripped, err := StripMetricNamesInPlace(buf)
	require.NoError(t, err)
	require.True(t, stripped)
	require.Equal(t, expected, out)
	require.Len(t, out, len(expected))
	requireDecodes(t, out)
}

// ==============================================================================
// validateStripText had zero negative-path coverage. This table mirrors
// TestStrip_Malformed but drives every one of validateStripText's own error
// branches — ParseTextHeader failure, the names-extent lower bound, the
// IndexOffset/DataOffset chain (both directions), the metric-count ceiling,
// the index-entry-fit bound, and the two checks it delegates to
// (verifyMetricNamesExtent, scanIndexForCollision) — via TEXT blobs
// specifically, since the function hand-duplicates the numeric checks.
// ==============================================================================

// baseNamesText builds a valid 2-metric names-bearing text blob for mutation.
func baseNamesText(t *testing.T) []byte {
	t.Helper()

	return encodeTextAt(t, nil, []textMetricSpec{
		{name: "metric.alpha", values: []string{"a", "b"}},
		{name: "metric.bravo", values: []string{"c", "d", "e"}},
	})
}

// mutateTextHeader returns a copy of src with its header re-serialized after fn.
func mutateTextHeader(t *testing.T, src []byte, fn func(*section.TextHeader)) []byte {
	t.Helper()
	out := append([]byte(nil), src...)
	hdr, err := section.ParseTextHeader(src)
	require.NoError(t, err)
	fn(&hdr)
	copy(out[:section.HeaderSize], hdr.Bytes())

	return out
}

// encodeCollisionText encodes a 2-metric text blob for the colliding pair A,B.
func encodeCollisionText(t *testing.T) []byte {
	t.Helper()
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

	return data
}

// spliceCollisionNamesText is spliceCollisionNames for a text collision blob
// (IDs [H,H]), used to force a duplicate-name error through validateStripText.
func spliceCollisionNamesText(t *testing.T, names []string) []byte {
	t.Helper()
	engine := endian.GetLittleEndianEngine()
	data := encodeCollisionText(t)
	hdr, err := section.ParseTextHeader(data)
	require.NoError(t, err)

	_, oldLen, err := ienc.DecodeMetricNames(data[section.HeaderSize:], engine)
	require.NoError(t, err)

	newNames, err := ienc.EncodeMetricNames(names, engine)
	require.NoError(t, err)

	delta := len(newNames) - oldLen
	hdr.IndexOffset = uint32(int(hdr.IndexOffset) + delta)
	hdr.DataOffset = uint32(int(hdr.DataOffset) + delta)

	out := make([]byte, 0, len(data)+delta)
	out = append(out, hdr.Bytes()...)
	out = append(out, newNames...)
	out = append(out, data[section.HeaderSize+oldLen:]...)

	return out
}

func TestStripText_Malformed(t *testing.T) {
	base := baseNamesText(t)
	engine := endian.GetLittleEndianEngine()

	baseHdr, err := section.ParseTextHeader(base)
	require.NoError(t, err)
	baseNamesSize := int(baseHdr.IndexOffset) - section.HeaderSize
	require.Positive(t, baseNamesSize)

	cases := []struct {
		name    string
		build   func() []byte
		wantErr error
	}{
		{
			// b[1] clears the masked magic bits (0xFFF0) entirely, so this is
			// actually caught by validateStrip's OWN magic-word switch before
			// ever dispatching to validateStripText — same outer branch the
			// numeric unknown_magic case exercises. Kept for documentation of
			// that behavior; invalid_timestamp_encoding below is what actually
			// drives ParseTextHeader's own error return.
			name:    "unknown_magic",
			build:   func() []byte { b := append([]byte(nil), base...); b[1] = 0x00; return b },
			wantErr: errs.ErrInvalidMagicNumber,
		},
		{
			// A valid magic word (so validateStrip's dispatcher routes here)
			// with an invalid TimestampEncoding byte: this is the case that
			// genuinely exercises ParseTextHeader's own error return inside
			// validateStripText, since the magic check itself can never fail by
			// the time control reaches here (the outer dispatcher already
			// verified the identical masked bits).
			name: "invalid_timestamp_encoding",
			build: func() []byte {
				b := append([]byte(nil), base...)
				b[2] = 0xFF

				return b
			},
			wantErr: errs.ErrInvalidHeaderFlags,
		},
		{
			name: "index_offset_32",
			build: func() []byte {
				return mutateTextHeader(t, base, func(h *section.TextHeader) { h.IndexOffset = 32 })
			},
			wantErr: errs.ErrMetricNamesExtentMismatch,
		},
		{
			name: "index_offset_33",
			build: func() []byte {
				return mutateTextHeader(t, base, func(h *section.TextHeader) { h.IndexOffset = 33 })
			},
			wantErr: errs.ErrMetricNamesExtentMismatch,
		},
		{
			name: "index_offset_beyond_dataoffset",
			build: func() []byte {
				// IndexOffset > DataOffset.
				return mutateTextHeader(t, base, func(h *section.TextHeader) { h.IndexOffset = h.DataOffset + 1 })
			},
			wantErr: errs.ErrInvalidIndexOffsets,
		},
		{
			name: "data_offset_beyond_len",
			build: func() []byte {
				return mutateTextHeader(t, base, func(h *section.TextHeader) { h.DataOffset = uint32(len(base) + 100) })
			},
			wantErr: errs.ErrInvalidIndexOffsets,
		},
		{
			name: "oversized_metric_count",
			build: func() []byte {
				return mutateTextHeader(t, base, func(h *section.TextHeader) { h.MetricCount = 70000 })
			},
			wantErr: errs.ErrInvalidMetricCount,
		},
		{
			name: "index_does_not_fit_before_data",
			build: func() []byte {
				// Shrink the IndexOffset->DataOffset gap below MetricCount*entrySize
				// while keeping IndexOffset <= DataOffset <= len(src).
				return mutateTextHeader(t, base, func(h *section.TextHeader) { h.DataOffset = h.IndexOffset + 1 })
			},
			wantErr: errs.ErrInvalidIndexEntrySize,
		},
		{
			name: "hash_mismatch",
			build: func() []byte {
				b := append([]byte(nil), base...)
				// Flip a byte inside the first name (payload starts at 32+2).
				b[section.HeaderSize+4] ^= 0xFF

				return b
			},
			wantErr: errs.ErrHashMismatch,
		},
		{
			name: "name_length_prefix_overflow",
			build: func() []byte {
				b := append([]byte(nil), base...)
				// Corrupt the first name's length prefix to run past the extent.
				engine.PutUint16(b[section.HeaderSize+2:section.HeaderSize+4], 0xFFFF)

				return b
			},
			wantErr: errs.ErrMetricNamesExtentMismatch,
		},
		{
			name:    "duplicate_names",
			build:   func() []byte { return spliceCollisionNamesText(t, []string{cnA, cnA}) },
			wantErr: errs.ErrDuplicateMetricName,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.build()
			assertStripError(t, data, tc.wantErr)
		})
	}
}

// ==============================================================================
// Additional malformed-input cases and the V2/V2Ext duplicate-name path that
// were previously untested. Each of these hits a distinct branch from the
// existing TestStrip_Malformed table (see the branch comment on each case).
// ==============================================================================

// TestStrip_NamesDeclaredCountMismatch corrupts the names extent's OWN
// declared count field (the first 2 bytes of the payload, independent of the
// header's MetricCount) so verifyMetricNamesExtent's `declared != count` check
// fires — previously 0-covered (every other malformed case mutates the header
// instead).
func TestStrip_NamesDeclaredCountMismatch(t *testing.T) {
	base := baseNamesV2(t)
	engine := endian.GetLittleEndianEngine()

	b := append([]byte(nil), base...)
	engine.PutUint16(b[section.HeaderSize:section.HeaderSize+2], 3) // header.MetricCount stays 2
	assertStripError(t, b, errs.ErrMetricNamesExtentMismatch)
}

// TestStrip_NameLengthPrefixTruncated shrinks IndexOffset so the extent holds
// only the 2-byte count field plus 1 stray byte — not enough for even the
// FIRST name's 2-byte length prefix (`pos+2 > len(extent)`, checked before any
// name bytes are read or any hash computed). This has to land on the first
// name: shrinking IndexOffset also shifts where readEntryMetricID looks up
// entry IDs (it shares the same offset), so truncating a LATER name's prefix
// instead would misalign entry 0's ID lookup and surface ErrHashMismatch
// first — that failure mode is already covered by index_offset_too_small_stale
// (in TestStrip_Malformed). This case is distinct from name_length_prefix_overflow
// (in TestStrip_Malformed), which truncates the name *bytes* after a valid
// prefix, and from index_offset_33 (also in TestStrip_Malformed), which is
// caught by the OUTER `indexOff < HeaderSize+2` bound before
// verifyMetricNamesExtent's inner walk ever runs.
func TestStrip_NameLengthPrefixTruncated(t *testing.T) {
	base := baseNamesV2(t)
	hdr, err := section.ParseNumericHeader(base)
	require.NoError(t, err)

	newIndexOffset := section.HeaderSize + 3
	require.Less(t, newIndexOffset, int(hdr.IndexOffset), "fixture sanity: must shrink the extent")
	require.GreaterOrEqual(t, newIndexOffset, section.HeaderSize+2, "fixture sanity: must clear the outer bound check")

	data := mutateNumericHeader(t, base, func(h *section.NumericHeader) {
		h.IndexOffset = uint32(newIndexOffset)
	})
	assertStripError(t, data, errs.ErrMetricNamesExtentMismatch)
}

// TestStrip_IndexEntriesDoNotFitBeforeFirstPayload shrinks the
// IndexOffset->TimestampPayloadOffset gap below MetricCount*entrySize while
// keeping the offset chain itself valid (IndexOffset <= TS <= Val <= Tag <=
// len(src)) — previously-uncovered branch, distinct from offset_chain_violation
// (in TestStrip_Malformed) which breaks the chain ordering instead.
func TestStrip_IndexEntriesDoNotFitBeforeFirstPayload(t *testing.T) {
	base := baseNamesV2(t)
	data := mutateNumericHeader(t, base, func(h *section.NumericHeader) {
		gap := h.IndexOffset + 1
		h.TimestampPayloadOffset = gap
		h.ValuePayloadOffset = gap
		h.TagPayloadOffset = gap
	})
	assertStripError(t, data, errs.ErrInvalidIndexEntrySize)
}

// TestStrip_OffsetOrderingViolations covers the offset-chain violations other
// than tsOff < indexOff (already covered by offset_chain_violation in
// TestStrip_Malformed): valOff < tsOff, tagOff < valOff, and tagOff > len(src).
func TestStrip_OffsetOrderingViolations(t *testing.T) {
	base := baseNamesV2(t)

	cases := []struct {
		name  string
		build func() []byte
	}{
		{
			name: "value_before_timestamp",
			build: func() []byte {
				return mutateNumericHeader(t, base, func(h *section.NumericHeader) {
					h.ValuePayloadOffset = h.TimestampPayloadOffset - 1
				})
			},
		},
		{
			name: "tag_before_value",
			build: func() []byte {
				return mutateNumericHeader(t, base, func(h *section.NumericHeader) {
					h.TagPayloadOffset = h.ValuePayloadOffset - 1
				})
			},
		},
		{
			name: "tag_beyond_len",
			build: func() []byte {
				return mutateNumericHeader(t, base, func(h *section.NumericHeader) {
					h.TagPayloadOffset = uint32(len(base) + 100)
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.build()
			assertStripError(t, data, errs.ErrInvalidIndexOffsets)
		})
	}
}

// spliceCollisionNamesV2 mirrors spliceCollisionNames but rebuilds a V2-layout
// collision blob, so the replacement names exercise the sorted-index scan
// (scanV2 / analyzeRunAdjacent) duplicate-name path instead of scanGeneral —
// the only pre-existing duplicate-names test used a V1 (unsorted) blob.
func spliceCollisionNamesV2(t *testing.T, names []string) []byte {
	t.Helper()
	engine := endian.GetLittleEndianEngine()
	data := encodeCollisionNumeric(t, WithBlobLayoutV2())
	hdr, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	require.True(t, hdr.Flag.IsV2(), "fixture sanity: must be V2 layout")

	_, oldLen, err := ienc.DecodeMetricNames(data[section.HeaderSize:], engine)
	require.NoError(t, err)

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

// TestStrip_DuplicateNames_V2 pins the guarantee for the sorted (V2)
// duplicate-name path: two adjacent same-ID, same-name entries must produce
// ErrDuplicateMetricName via scanV2 -> analyzeRunAdjacent, not a silent refusal.
func TestStrip_DuplicateNames_V2(t *testing.T) {
	data := spliceCollisionNamesV2(t, []string{cnA, cnA})
	assertStripError(t, data, errs.ErrDuplicateMetricName)
}

// extraMetricName hashes above collisiontest.CollisionID (verified offline
// against xxhash.Sum64String), so in an ID-ascending walk it always sorts
// AFTER the cnA/cnB collision pair — for V2 that's on-disk order, for V1/text
// (scanGeneral) it's the internally-sorted ordinal order.
const extraMetricName = "metric.zzz.after.0000"

// encodeCollisionNumericWithExtra builds a 3-metric numeric blob under opts:
// the colliding pair (cnA, cnB) inserted first, plus a distinct metric (also
// inserted last) whose ID sorts after both. For V2 (opts = WithBlobLayoutV2())
// this also fixes on-disk order, since V2's index is ID-sorted; for V1 (opts =
// nil) on-disk order is simply insertion order, which already places the
// collision pair before the extra metric.
func encodeCollisionNumericWithExtra(t *testing.T, opts ...NumericEncoderOption) []byte {
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

	require.NoError(t, enc.StartMetricName(extraMetricName, 1))
	require.NoError(t, enc.AddDataPoint(start.UnixMicro(), 9.0, ""))
	require.NoError(t, enc.EndMetric())

	data, err := enc.Finish()
	require.NoError(t, err)

	return data
}

// spliceCollisionNamesMidRun rebuilds encodeCollisionNumericWithExtra(opts...)
// with the collision pair's names replaced (forcing a duplicate) while leaving
// the third (non-colliding, higher-ID) metric's name untouched. With a
// distinct-ID entry after the duplicate-ID run, the run closes via the
// scanning loop's MID-LOOP branch (`cur != prev` / next ID differs, inside the
// loop) rather than only the final-run branch that a 2-entry collision fixture
// exercises — for V2 that's scanV2, for V1/text it's scanGeneral.
func spliceCollisionNamesMidRun(t *testing.T, names []string, opts ...NumericEncoderOption) []byte {
	t.Helper()
	require.Len(t, names, 2, "names must replace exactly the 2-entry collision pair")

	engine := endian.GetLittleEndianEngine()
	data := encodeCollisionNumericWithExtra(t, opts...)
	hdr, err := section.ParseNumericHeader(data)
	require.NoError(t, err)
	require.EqualValues(t, 3, hdr.MetricCount, "fixture sanity")

	orig, oldLen, err := ienc.DecodeMetricNames(data[section.HeaderSize:], engine)
	require.NoError(t, err)
	require.Len(t, orig, 3, "fixture sanity")

	// Confirm the collision pair sits at on-disk positions 0,1 (both ID cnH)
	// and the extra metric at position 2 (distinct ID) — true for V2 because
	// the index is ID-sorted, and true for V1 because insertion order already
	// placed them that way. The mid-loop closure these fixtures exist to
	// exercise depends on this layout (scanGeneral re-sorts internally by ID,
	// but that only reorders WITHIN scanGeneral — the splice below still needs
	// to know which on-disk positions to rewrite).
	idxOff := int(hdr.IndexOffset)
	entrySize := hdr.Flag.IndexEntrySize()
	id0 := engine.Uint64(data[idxOff : idxOff+8])
	id1 := engine.Uint64(data[idxOff+entrySize : idxOff+entrySize+8])
	id2 := engine.Uint64(data[idxOff+2*entrySize : idxOff+2*entrySize+8])
	require.Equal(t, cnH, id0, "fixture sanity: entry 0 must be the collision ID")
	require.Equal(t, cnH, id1, "fixture sanity: entry 1 must be the collision ID")
	require.NotEqual(t, cnH, id2, "fixture sanity: entry 2 must sort after the collision run")

	newNames := append(append([]string(nil), names...), orig[2])
	newPayload, err := ienc.EncodeMetricNames(newNames, engine)
	require.NoError(t, err)

	delta := len(newPayload) - oldLen
	hdr.IndexOffset = uint32(int(hdr.IndexOffset) + delta)
	hdr.TimestampPayloadOffset = uint32(int(hdr.TimestampPayloadOffset) + delta)
	hdr.ValuePayloadOffset = uint32(int(hdr.ValuePayloadOffset) + delta)
	hdr.TagPayloadOffset = uint32(int(hdr.TagPayloadOffset) + delta)

	out := make([]byte, 0, len(data)+delta)
	out = append(out, hdr.Bytes()...)
	out = append(out, newPayload...)
	out = append(out, data[section.HeaderSize+oldLen:]...)

	return out
}

// TestStrip_DuplicateNames_V2_MidRun complements TestStrip_DuplicateNames_V2:
// with a third, higher-ID metric following the collision pair, scanV2 closes
// the duplicate-ID run mid-loop rather than only at the final-run branch.
func TestStrip_DuplicateNames_V2_MidRun(t *testing.T) {
	data := spliceCollisionNamesMidRun(t, []string{cnA, cnA}, WithBlobLayoutV2())
	assertStripError(t, data, errs.ErrDuplicateMetricName)
}

// TestStrip_Refuse_V2_MidRun exercises scanV2's mid-loop "refuse, not
// duplicate" branch: a genuine collision (distinct names, same ID) whose run
// closes mid-loop because a distinct-ID entry follows it — unlike
// TestStrip_Refuse_Collision_Numeric/V2, where the collision pair is the
// entire blob and the run can only close via the final-run branch.
func TestStrip_Refuse_V2_MidRun(t *testing.T) {
	data := encodeCollisionNumericWithExtra(t, WithBlobLayoutV2())
	assertRefused(t, data)
}

// TestStrip_DuplicateNames_V1_MidRun is TestStrip_DuplicateNames_V2_MidRun's
// V1 (scanGeneral) counterpart: scanGeneral internally sorts ordinals by ID,
// so the collision pair's run closes mid-loop here too, exercising the same
// `dupErr != nil` branch scanV2's mid-run case does, on the unsorted-index path.
func TestStrip_DuplicateNames_V1_MidRun(t *testing.T) {
	data := spliceCollisionNamesMidRun(t, []string{cnA, cnA}) // V1: no opts
	assertStripError(t, data, errs.ErrDuplicateMetricName)
}
