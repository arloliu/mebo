package encoding

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDeltaPackedDecodeTable_ControlByteExhaustive verifies that the precomputed
// shuffle metadata is consistent with the scalar width-lookup for every possible
// control byte.
func TestDeltaPackedDecodeTable_ControlByteExhaustive(t *testing.T) {
	for cb := range 256 {
		meta := &deltaPackedDecodeTable[cb]

		// Cross-check lengths against the tag decode rule.
		var expectedTotal uint8
		for lane := range groupSize {
			tag := (uint8(cb) >> (uint(lane) * 2)) & 0x03
			expectedWidth := uint8(groupVarintLengths[tag])

			require.Equal(t, expectedWidth, meta.lengths[lane],
				"cb=0x%02X lane=%d: wrong length", cb, lane)
			require.Equal(t, expectedTotal, meta.offsets[lane],
				"cb=0x%02X lane=%d: wrong offset", cb, lane)
			expectedTotal += expectedWidth
		}

		require.Equal(t, expectedTotal, meta.totalBytes,
			"cb=0x%02X: wrong totalBytes", cb)

		// Cross-check flat tables match struct table.
		require.Equal(t, meta.totalBytes, deltaPackedDecodeTotalBytes[cb],
			"cb=0x%02X: flat totalBytes mismatch", cb)

		require.Equal(t, meta.shuffle[:], deltaPackedDecodeShuffles[cb*32:(cb+1)*32],
			"cb=0x%02X: flat shuffle mismatch", cb)

		// Verify shuffle mask: for each lane, non-0x80 entries must point to valid
		// payload bytes, and the source indices must match the expected offsets.
		for lane := range groupSize {
			dstBase := lane * 8
			width := int(meta.lengths[lane])
			offset := int(meta.offsets[lane])

			for b := range 8 {
				maskByte := meta.shuffle[dstBase+b]
				if b < width {
					require.Equal(t, uint8(offset+b), maskByte,
						"cb=0x%02X lane=%d byte=%d: wrong shuffle src index", cb, lane, b)
				} else {
					require.Equal(t, uint8(0x80), maskByte,
						"cb=0x%02X lane=%d byte=%d: expected 0x80 sentinel", cb, lane, b)
				}
			}
		}
	}
}

// TestDeltaPackedDecodeBackends_Parity runs each backend against the scalar reference
// on a variety of encoded streams and verifies they produce identical output.
func TestDeltaPackedDecodeBackends_Parity(t *testing.T) {
	datasets := []struct {
		name string
		ts   []int64
	}{
		{"regular_1s_100", generateSequentialFromBase(100, 1_000_000)},
		{"regular_1s_1000", generateSequentialFromBase(1000, 1_000_000)},
		{"jitter_small_200", simdTestJitterSmall(200)},
		{"jumps_large_500", simdTestJumpsLarge(500)},
		{"all_1byte_tags", simdTestAll1ByteTags(256)},
		{"all_8byte_tags", simdTestAll8ByteTags(64)},
		{"mixed_widths_300", simdTestMixedWidths(300)},
		// Boundary sizes
		{"size_3", generateSequentialFromBase(3, 1_000_000)},
		{"size_5", generateSequentialFromBase(5, 1_000_000)},
		{"size_6", generateSequentialFromBase(6, 1_000_000)},
		{"size_7", generateSequentialFromBase(7, 1_000_000)},
		{"size_9", generateSequentialFromBase(9, 1_000_000)},
		{"size_10", generateSequentialFromBase(10, 1_000_000)},
	}

	for _, ds := range datasets {
		t.Run(ds.name, func(t *testing.T) {
			enc := NewTimestampDeltaPackedEncoder()
			enc.WriteSlice(ds.ts)
			encoded := make([]byte, len(enc.Bytes()))
			copy(encoded, enc.Bytes())
			count := enc.Len()
			enc.Finish()

			// Reference: scalar
			dec := NewTimestampDeltaPackedDecoder()
			restoreScalar := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
			want := make([]int64, count)
			n := dec.DecodeAll(encoded, count, want)
			restoreScalar()
			require.Equal(t, count, n, "scalar DecodeAll produced wrong count")
			require.Equal(t, ds.ts, want[:n])

			// Each backend
			for _, backend := range allDeltaPackedDecodeBackends {
				if !deltaPackedDecodeBackendSupported(backend) {
					continue
				}

				t.Run(deltaPackedDecodeBackendName(backend), func(t *testing.T) {
					restore := setDeltaPackedDecodeBackendForTest(backend)
					defer restore()

					got := make([]int64, count)
					n2 := dec.DecodeAll(encoded, count, got)
					require.Equal(t, n, n2,
						"backend %s: DecodeAll count mismatch", deltaPackedDecodeBackendName(backend))
					require.Equal(t, want[:n], got[:n2],
						"backend %s: value mismatch", deltaPackedDecodeBackendName(backend))
				})
			}
		})
	}
}

// TestDeltaPackedDecodeBackends_AllIterator verifies the All() iterator produces
// the same sequence as DecodeAll for each backend.
func TestDeltaPackedDecodeBackends_AllIterator(t *testing.T) {
	ts := generateSequentialFromBase(300, 1_000_000)
	enc := NewTimestampDeltaPackedEncoder()
	enc.WriteSlice(ts)
	encoded := make([]byte, len(enc.Bytes()))
	copy(encoded, enc.Bytes())
	count := enc.Len()
	enc.Finish()

	dec := NewTimestampDeltaPackedDecoder()
	restoreScalar := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
	want := make([]int64, count)
	n := dec.DecodeAll(encoded, count, want)
	restoreScalar()
	require.Equal(t, count, n)

	for _, backend := range allDeltaPackedDecodeBackends {
		if !deltaPackedDecodeBackendSupported(backend) {
			continue
		}

		t.Run(deltaPackedDecodeBackendName(backend), func(t *testing.T) {
			restore := setDeltaPackedDecodeBackendForTest(backend)
			defer restore()

			got := make([]int64, 0, count)
			for ts2 := range dec.All(encoded, count) {
				got = append(got, ts2)
			}

			require.Equal(t, want[:n], got)
		})
	}
}

// TestDeltaPackedDecodeBackends_ExhaustiveControlBytes verifies each backend correctly
// decodes all 256 possible control bytes when used in a real encoded stream.
func TestDeltaPackedDecodeBackends_ExhaustiveControlBytes(t *testing.T) {
	for cb := range 256 {
		meta := &deltaPackedDecodeTable[cb]

		// Build the minimum timestamp sequence that would produce this control byte
		// for the second packed group, after a padding first group with control byte 0x00.
		// We need 4 delta-of-delta zigzag values whose encoded widths match the tags.
		ts := simdTestForControlByte(meta)

		enc := NewTimestampDeltaPackedEncoder()
		enc.WriteSlice(ts)
		encoded := make([]byte, len(enc.Bytes()))
		copy(encoded, enc.Bytes())
		count := enc.Len()
		enc.Finish()

		actualControlByte, ok := packedControlByteAt(encoded, 1)
		require.True(t, ok, "cb=0x%02X: failed to locate target packed control byte", cb)
		require.Equal(t, uint8(cb), actualControlByte,
			"cb=0x%02X: helper generated unexpected control byte 0x%02X", cb, actualControlByte)

		dec := NewTimestampDeltaPackedDecoder()
		restoreScalar := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
		want := make([]int64, count)
		n := dec.DecodeAll(encoded, count, want)
		restoreScalar()
		require.Equal(t, count, n, "cb=0x%02X: scalar decoded wrong count", cb)

		for _, backend := range allDeltaPackedDecodeBackends {
			if !deltaPackedDecodeBackendSupported(backend) || backend == deltaPackedDecodeBackendScalar {
				continue
			}

			t.Run(fmt.Sprintf("cb=0x%02X/%s", cb, deltaPackedDecodeBackendName(backend)), func(t *testing.T) {
				restore := setDeltaPackedDecodeBackendForTest(backend)
				defer restore()

				got := make([]int64, count)
				n2 := dec.DecodeAll(encoded, count, got)
				require.Equal(t, n, n2, "DecodeAll count mismatch")
				require.Equal(t, want[:n], got[:n2], "value mismatch")
			})
		}
	}
}

func TestDeltaPackedDecodeBackends_TruncatedInput(t *testing.T) {
	if !deltaPackedDecodeBackendSupported(deltaPackedDecodeBackendAsmAVX2) {
		t.Skip("AsmAVX2 backend not supported")
	}

	ts := simdTestAll1ByteTags(90)
	enc := NewTimestampDeltaPackedEncoder()
	enc.WriteSlice(ts)
	encoded := append([]byte(nil), enc.Bytes()...)
	count := enc.Len()
	enc.Finish()

	require.Greater(t, len(encoded), 2)
	truncated := append([]byte(nil), encoded[:len(encoded)-1]...)

	dec := NewTimestampDeltaPackedDecoder()

	restoreScalar := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
	want := make([]int64, count)
	wantCount := dec.DecodeAll(truncated, count, want)
	restoreScalar()

	restoreAVX2 := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendAsmAVX2)
	got := make([]int64, count)
	gotCount := dec.DecodeAll(truncated, count, got)
	restoreAVX2()

	require.Equal(t, wantCount, gotCount)
	require.Equal(t, want[:wantCount], got[:gotCount])

	restoreScalar = setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
	wantIter := collectInt64Seq(dec.All(truncated, count))
	restoreScalar()

	restoreAVX2 = setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendAsmAVX2)
	gotIter := collectInt64Seq(dec.All(truncated, count))
	restoreAVX2()

	require.Equal(t, wantIter, gotIter)
}

func TestDeltaPackedDecodeBackends_SIMDThresholdBoundaries(t *testing.T) {
	if !deltaPackedDecodeBackendSupported(deltaPackedDecodeBackendAsmAVX2) {
		t.Skip("AsmAVX2 backend not supported")
	}

	testCases := []struct {
		name  string
		count int
	}{
		{name: "below_threshold", count: 65},
		{name: "at_threshold", count: 66},
	}

	dec := NewTimestampDeltaPackedDecoder()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ts := simdTestMixedWidths(tc.count)
			enc := NewTimestampDeltaPackedEncoder()
			enc.WriteSlice(ts)
			encoded := append([]byte(nil), enc.Bytes()...)
			count := enc.Len()
			enc.Finish()

			restoreScalar := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendScalar)
			want := make([]int64, count)
			wantCount := dec.DecodeAll(encoded, count, want)
			wantIter := collectInt64Seq(dec.All(encoded, count))
			restoreScalar()

			restoreAVX2 := setDeltaPackedDecodeBackendForTest(deltaPackedDecodeBackendAsmAVX2)
			got := make([]int64, count)
			gotCount := dec.DecodeAll(encoded, count, got)
			gotIter := collectInt64Seq(dec.All(encoded, count))
			restoreAVX2()

			require.Equal(t, wantCount, gotCount)
			require.Equal(t, want[:wantCount], got[:gotCount])
			require.Equal(t, wantIter, gotIter)
		})
	}
}

// --- helpers ---

// simdTestJitterSmall generates timestamps with small random jitter (DoD ≈ ±100 µs).
func simdTestJitterSmall(n int) []int64 {
	ts := make([]int64, n)
	base := int64(1_000_000_000_000) // 1s epoch start
	delta := int64(1_000_000)        // 1s base interval

	for i := range n {
		jitter := int64((i*37)%201 - 100) // deterministic ±100 µs jitter
		delta += jitter
		if delta < 1 {
			delta = 1
		}
		if i == 0 {
			ts[i] = base
		} else {
			ts[i] = ts[i-1] + delta
		}
	}

	return ts
}

// simdTestJumpsLarge generates timestamps with large gaps (DoD fits in 4 or 8 bytes).
func simdTestJumpsLarge(n int) []int64 {
	ts := make([]int64, n)
	base := int64(1_000_000_000_000)

	for i := range n {
		if i == 0 {
			ts[i] = base
			continue
		}
		gap := int64(1_000_000) + int64((i*100_000)%1_000_000_000) // varies 1s..1ks
		ts[i] = ts[i-1] + gap
	}

	return ts
}

// simdTestAll1ByteTags generates timestamps where all DoD values fit in 1 byte (control byte 0x00).
func simdTestAll1ByteTags(n int) []int64 {
	ts := make([]int64, n)
	base := int64(1_000_000_000_000)

	for i := range n {
		ts[i] = base + int64(i)*1_000_000 // constant 1s delta → DoD = 0 → zigzag = 0 → 1 byte
	}

	return ts
}

// simdTestAll8ByteTags generates timestamps where all DoD values require 8 bytes (control byte 0xFF).
func simdTestAll8ByteTags(n int) []int64 {
	ts := make([]int64, n)
	base := int64(1_000_000_000_000)
	const wideDeltaOfDelta = int64(1 << 33)

	ts[0] = base
	if n == 1 {
		return ts
	}

	cur := base
	delta := int64(1_000_000)

	for i := 1; i < n; i++ {
		delta += wideDeltaOfDelta
		cur += delta
		ts[i] = cur
	}

	return ts
}

// simdTestMixedWidths generates timestamps with a mix of DoD widths.
func simdTestMixedWidths(n int) []int64 {
	ts := make([]int64, n)
	base := int64(1_000_000_000_000)
	cur := base

	for i := range n {
		var delta int64
		switch i % 4 {
		case 0:
			delta = 1_000_000 // 1s → small DoD
		case 1:
			delta = 1_000_000 + int64(i)*300 // growing slowly → 2-byte DoD
		case 2:
			delta = int64(65536) * int64(i%128) // 4-byte DoD
		case 3:
			delta = int64(1<<33) + int64(i)*7 // 8-byte DoD
		default:
			panic("unreachable mixed-width case")
		}

		if delta <= 0 {
			delta = 1_000
		}

		cur += delta
		ts[i] = cur
	}

	return ts
}

// simdTestForControlByte builds a timestamp sequence whose second packed group
// uses the given control byte. The sequence contains two header timestamps,
// one padding group with control byte 0x00, and then the 4-value target group.
func simdTestForControlByte(meta *deltaPackedDecodeMeta) []int64 {
	// We need timestamps[2..5] to produce the target control byte.
	// Strategy: pick zigzag values whose widths match meta.lengths[].
	// Use the minimum value that triggers each width:
	//   width=1: zigzag = 0   (value 0)
	//   width=2: zigzag = 256 (first 2-byte value)
	//   width=4: zigzag = 65536
	//   width=8: zigzag = 4294967296
	minZigzagForWidth := [9]uint64{
		0, 0, 256, 0, 65536, 0, 0, 0, 4294967296,
	}

	// Build 6 timestamps: first 2 are header, then one padding group (all 1-byte),
	// then the 4 target values.
	ts := make([]int64, 2+groupSize+groupSize)
	ts[0] = 1_000_000_000_000 // arbitrary start time
	ts[1] = ts[0] + 1_000_000 // first delta = 1s

	// Padding group: constant delta to keep DoD=0 (zigzag=0, all 1-byte, cb=0x00)
	prevTS := ts[1]
	prevDelta := ts[1] - ts[0]

	for i := range groupSize {
		prevTS += prevDelta // DoD = 0 → zigzag = 0
		ts[2+i] = prevTS
	}

	// Target group: use zigzag values that match meta.lengths
	for lane := range groupSize {
		width := meta.lengths[lane]
		zz := minZigzagForWidth[width]
		expectedTag := encodeTag(zz)
		// Convert zigzag back to deltaOfDelta signed value
		var dod int64
		if zz&1 == 0 {
			dod = int64(zz >> 1)
		} else {
			dod = ^int64(zz >> 1)
		}

		newDelta := prevDelta + dod
		if newDelta == 0 {
			newDelta = 1 // avoid zero delta in target group slot
		}
		// Recalculate dod to match newDelta
		dod = newDelta - prevDelta
		zz2 := uint64((dod << 1) ^ (dod >> 63))

		if encodeTag(zz2) != expectedTag {
			panic("generated zigzag width mismatch")
		}

		prevTS += newDelta
		prevDelta = newDelta
		ts[2+groupSize+lane] = prevTS
	}

	return ts
}

func collectInt64Seq(seq func(func(int64) bool)) []int64 {
	values := make([]int64, 0)
	for value := range seq {
		values = append(values, value)
	}

	return values
}

func packedControlByteAt(data []byte, groupIndex int) (byte, bool) {
	_, offset, ok := decodeVarint64(data, 0)
	if !ok {
		return 0, false
	}

	_, offset, ok = decodeVarint64(data, offset)
	if !ok {
		return 0, false
	}

	for range groupIndex {
		if offset >= len(data) {
			return 0, false
		}

		meta := &deltaPackedDecodeTable[data[offset]]
		offset += 1 + int(meta.totalBytes)
	}

	if offset >= len(data) {
		return 0, false
	}

	return data[offset], true
}
