//go:build goexperiment.simd && amd64

package encoding

// Prototype (measurement only): a SIMD fixed-width FOR+bitpack codec (BP128-style)
// using the vertical 8-lane layout, where all lanes share width+bitpos so the
// 64-bit-boundary carry is a uniform vector shift (FastLanes/bp128 approach).
//
// Goal: does SIMD pack/unpack beat Simple8b's scalar pack (encode) and stay
// competitive on ratio? Built only under GOEXPERIMENT=simd. Run:
//   GOEXPERIMENT=simd go test ./internal/encoding/ -run 'BP128Proto' -v
//   GOEXPERIMENT=simd go test ./internal/encoding/ -run x -bench 'BP128Proto' -benchmem

import (
	"math/bits"
	"simd/archsimd"
	"testing"

	"github.com/stretchr/testify/require"
)

const bpLanes = 8
const bpPerLane = 32
const bpBlock = bpLanes * bpPerLane // 256 values per block

// bpPackBlock packs one 256-value block at fixed width w (vertical 8-lane layout).
// Appends groups of 8 words to out. Block laid out so lane j holds block[p*8+j].
func bpPackBlock(out []uint64, block []uint64, w int) []uint64 {
	var tmp [8]uint64
	acc := archsimd.BroadcastUint64x8(0)
	bitpos := 0
	for p := 0; p < bpPerLane; p++ {
		v := archsimd.LoadUint64x8Slice(block[p*8:])
		acc = acc.Or(v.ShiftAllLeft(uint64(bitpos))) //nolint:gosec
		nb := bitpos + w
		if nb >= 64 {
			acc.Store(&tmp)
			out = append(out, tmp[:]...)
			if nb > 64 {
				acc = v.ShiftAllRight(uint64(64 - bitpos)) //nolint:gosec
			} else {
				acc = archsimd.BroadcastUint64x8(0)
			}
			bitpos = nb - 64
		} else {
			bitpos = nb
		}
	}
	if bitpos > 0 {
		acc.Store(&tmp)
		out = append(out, tmp[:]...)
	}

	return out
}

// bpUnpackBlock reverses bpPackBlock into dst[0:256], reading from packed.
// Returns the number of words consumed.
func bpUnpackBlock(dst []uint64, packed []uint64, w int) int {
	if w == 0 {
		for i := range dst[:bpBlock] {
			dst[i] = 0
		}

		return 0
	}
	mask := archsimd.BroadcastUint64x8((uint64(1) << uint(w)) - 1) //nolint:gosec
	in := 0
	cur := archsimd.LoadUint64x8Slice(packed[in:])
	in += 8
	bitpos := 0
	for p := 0; p < bpPerLane; p++ {
		nb := bitpos + w
		var v archsimd.Uint64x8
		if nb < 64 {
			v = cur.ShiftAllRight(uint64(bitpos)).And(mask) //nolint:gosec
			bitpos = nb
		} else if nb == 64 {
			v = cur.ShiftAllRight(uint64(bitpos)).And(mask) //nolint:gosec
			if p != bpPerLane-1 {
				cur = archsimd.LoadUint64x8Slice(packed[in:])
				in += 8
			}
			bitpos = 0
		} else {
			next := archsimd.LoadUint64x8Slice(packed[in:])
			in += 8
			lo := cur.ShiftAllRight(uint64(bitpos))            //nolint:gosec
			hi := next.ShiftAllLeft(uint64(64 - bitpos))       //nolint:gosec
			v = lo.Or(hi).And(mask)
			cur = next
			bitpos = nb - 64
		}
		v.StoreSlice(dst[p*8:])
	}

	return in
}

// bpEncode encodes vals (length multiple of bpBlock) as per-block fixed-width.
func bpEncode(vals []uint64) (packed []uint64, widths []uint8) {
	for b := 0; b < len(vals); b += bpBlock {
		block := vals[b : b+bpBlock]
		w := 0
		for _, v := range block {
			if bl := bits.Len64(v); bl > w {
				w = bl
			}
		}
		widths = append(widths, uint8(w)) //nolint:gosec
		if w > 0 {
			packed = bpPackBlock(packed, block, w)
		}
	}

	return packed, widths
}

func bpDecode(packed []uint64, widths []uint8, n int) []uint64 {
	out := make([]uint64, n)
	in := 0
	for bi, w := range widths {
		used := bpUnpackBlock(out[bi*bpBlock:], packed[in:], int(w))
		in += used
	}

	return out
}

// makeDods builds a multiple-of-256 zigzag(dod) stream from ONE continuous
// timestamp series (no per-metric boundary spikes — those would be stored
// separately as first-deltas in a real codec).
func makeDods(blocks int, jitterPct float64) []uint64 {
	n := blocks * bpBlock
	ts := genTimestamps(n+1, jitterPct, 1)
	out := make([]uint64, 0, n)
	var prevDelta int64
	prevTS := ts[0]
	for i := 1; i < len(ts); i++ {
		delta := ts[i] - prevTS
		out = append(out, s8bZigZag(delta-prevDelta))
		prevTS = ts[i]
		prevDelta = delta
	}

	return out[:n]
}

func TestBP128Proto_RoundTripAndRatio(t *testing.T) {
	for _, jit := range []float64{0, 0.1, 0.5, 2.0} {
		vals := makeDods(40, jit) // 40*256 = 10240 values
		packed, widths := bpEncode(vals)
		got := bpDecode(packed, widths, len(vals))
		require.Equal(t, vals, got, "jitter=%.1f round-trip", jit)

		bytesPerVal := float64(len(packed)*8+len(widths)) / float64(len(vals))
		t.Logf("jitter=%.1f%%  BP128 = %.3f B/dod  (avg width %.1f bits)",
			jit, bytesPerVal, avgWidth(widths))
	}
}

func avgWidth(w []uint8) float64 {
	s := 0
	for _, x := range w {
		s += int(x)
	}

	return float64(s) / float64(len(w))
}

func BenchmarkBP128Proto_PackUnpack(b *testing.B) {
	vals := makeDods(157, 0.1) // ~40k values, realistic low-jitter
	packed, widths := bpEncode(vals)
	dst := make([]uint64, len(vals))

	b.Run("Pack_SIMD", func(b *testing.B) {
		b.ReportAllocs()
		out := make([]uint64, 0, len(packed))
		for b.Loop() {
			out = out[:0]
			for bi := 0; bi < len(vals); bi += bpBlock {
				block := vals[bi : bi+bpBlock]
				w := int(widths[bi/bpBlock])
				if w > 0 {
					out = bpPackBlock(out, block, w)
				}
			}
		}
		_ = out
	})

	b.Run("Unpack_SIMD", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			in := 0
			for bi, w := range widths {
				in += bpUnpackBlock(dst[bi*bpBlock:], packed[in:], int(w))
			}
		}
	})
}

// ---- full end-to-end pipeline (honest comparison incl. dod + prefix-sum) ----

func bpFullEncode(ts []int64, scratch []uint64) []uint64 {
	// dod + zigzag into scratch (length len(ts)-1), padded up to a block multiple.
	scratch = scratch[:0]
	var prevDelta int64
	prevTS := ts[0]
	for i := 1; i < len(ts); i++ {
		delta := ts[i] - prevTS
		scratch = append(scratch, s8bZigZag(delta-prevDelta))
		prevTS = ts[i]
		prevDelta = delta
	}
	for len(scratch)%bpBlock != 0 {
		scratch = append(scratch, 0)
	}
	packed, _ := bpEncode(scratch)

	return packed
}

func bpFullDecode(packed []uint64, widths []uint8, firstTS int64, n int, dst []uint64) int64 {
	dods := bpDecode(packed, widths, n)
	ts := firstTS
	var delta int64
	var sink int64
	for i := 0; i < n; i++ {
		delta += s8bUnZigZag(dods[i])
		ts += delta
		sink += ts
	}

	return sink
}

func BenchmarkBP128Proto_FullVsDelta(b *testing.B) {
	const n = 40192 // multiple of 256
	ts := genTimestamps(n+1, 0.1, 1)
	scratch := make([]uint64, 0, n+bpBlock)

	// pre-encode for decode bench
	dods := makeDods(n/bpBlock, 0.1)
	packed, widths := bpEncode(dods)
	dst := make([]uint64, len(dods))

	b.Run("BP128_FullEncode", func(b *testing.B) {
		b.ReportAllocs()
		var out []uint64
		for b.Loop() {
			out = bpFullEncode(ts, scratch)
		}
		_ = out
	})
	b.Run("Delta_FullEncode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			enc := NewTimestampDeltaEncoder()
			enc.WriteSlice(ts)
			_ = enc.Bytes()
			enc.Finish()
		}
	})
	b.Run("BP128_FullDecode", func(b *testing.B) {
		b.ReportAllocs()
		var s int64
		for b.Loop() {
			s += bpFullDecode(packed, widths, ts[0], len(dods), dst)
		}
		_ = s
	})
	b.Run("Delta_FullDecode", func(b *testing.B) {
		deltaEnc := NewTimestampDeltaEncoder()
		deltaEnc.WriteSlice(ts)
		data := append([]byte(nil), deltaEnc.Bytes()...)
		deltaEnc.Finish()
		dec := NewTimestampDeltaDecoder()
		b.ReportAllocs()
		var s int64
		for b.Loop() {
			for v := range dec.All(data, len(ts)) {
				s += v
			}
		}
		_ = s
	})
}

// bpEncodeInto packs into reused buffers (0-alloc steady state, like production).
func bpEncodeInto(packed []uint64, widths []uint8, vals []uint64) ([]uint64, []uint8) {
	packed = packed[:0]
	widths = widths[:0]
	for b := 0; b < len(vals); b += bpBlock {
		block := vals[b : b+bpBlock]
		w := 0
		for _, v := range block {
			if bl := bits.Len64(v); bl > w {
				w = bl
			}
		}
		widths = append(widths, uint8(w))
		if w > 0 {
			packed = bpPackBlock(packed, block, w)
		}
	}

	return packed, widths
}

func BenchmarkBP128Proto_EncodePooled(b *testing.B) {
	const n = 40192
	ts := genTimestamps(n+1, 0.1, 1)
	scratch := make([]uint64, 0, n+bpBlock)
	packed := make([]uint64, 0, n)
	widths := make([]uint8, 0, n/bpBlock+1)

	b.Run("BP128_Encode_Pooled", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			scratch = scratch[:0]
			var prevDelta int64
			prevTS := ts[0]
			for i := 1; i < len(ts); i++ {
				delta := ts[i] - prevTS
				scratch = append(scratch, s8bZigZag(delta-prevDelta))
				prevTS = ts[i]
				prevDelta = delta
			}
			for len(scratch)%bpBlock != 0 {
				scratch = append(scratch, 0)
			}
			packed, widths = bpEncodeInto(packed, widths, scratch)
		}
		_ = packed
		_ = widths
	})
}

func BenchmarkBP128Proto_EncodeSIMDDod(b *testing.B) {
	const n = 40192
	ts := genTimestamps(n+1, 0.1, 1)
	zz := make([]uint64, 0, n+bpBlock)
	packed := make([]uint64, 0, n)
	widths := make([]uint8, 0, n/bpBlock+1)
	var deltaBuf [256]int64

	b.Run("BP128_Encode_SIMDdod", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			remaining := ts[1:]
			prevTS, prevDelta := ts[0], int64(0)
			zz = zz[:0]
			for len(remaining) > 0 {
				c := min(len(remaining), 256)
				prevTS, prevDelta = deltaOfDeltaIntoActive(deltaBuf[:c], remaining[:c], prevTS, prevDelta)
				for _, d := range deltaBuf[:c] {
					zz = append(zz, s8bZigZag(d))
				}
				remaining = remaining[c:]
			}
			for len(zz)%bpBlock != 0 {
				zz = append(zz, 0)
			}
			packed, widths = bpEncodeInto(packed, widths, zz)
		}
		_ = packed
		_ = widths
	})
}
