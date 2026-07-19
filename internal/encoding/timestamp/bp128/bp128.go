// Package bp128 implements the retained BP128 timestamp experiment.
package bp128

import (
	"math/bits"

	"github.com/arloliu/mebo/endian"
)

// BP128: a SIMD-friendly fixed-width Frame-of-Reference + bit-pack codec for
// int64 timestamps, applied over the zigzag(delta-of-delta) stream.
//
// Layout is the "vertical 8-lane" arrangement (FastLanes / SIMD-BP128 style):
// a block is 256 values split across 8 lanes of 32 values each, where lane j
// holds value block[p*8+j]. All 8 lanes share the same bit width and bit
// position, so the 64-bit-boundary carry is a single uniform shift across the
// whole 512-bit register — that is what makes the AVX-512 kernel branch-light.
//
// This file is the SCALAR reference: pure Go, works in every build, and is the
// bit-exact oracle the AVX-512 asm kernels are differentially tested against.
//
// Spike on-disk pipeline (NOT the final wired on-disk format): the first
// timestamp and first delta are stored explicitly (Frame-of-Reference base, so
// the large first delta never inflates a block), then the zigzag(delta-of-delta)
// stream is packed in 256-value blocks with per-block PFOR exceptions. See
// bp128Codec.encode for the byte layout.
const (
	bp128Lanes   = 8
	bp128PerLane = 32
	bp128Block   = bp128Lanes * bp128PerLane // 256 values per block
)

func bp128ZigZag(value int64) uint64 { return uint64((value << 1) ^ (value >> 63)) } //nolint:gosec

func bp128UnZigZag(value uint64) int64 { return int64(value>>1) ^ -int64(value&1) }

// bp128WordsPerBlock returns the number of packed 64-bit words a block of the
// given width occupies (0 when w == 0).
func bp128WordsPerBlock(w int) int {
	if w == 0 {
		return 0
	}

	return bp128Lanes * ((bp128PerLane*w + 63) / 64)
}

// bp128PackBlockScalar packs one 256-value block at fixed width w (0..64) into
// out using the vertical 8-lane layout, appending whole groups of 8 words.
func bp128PackBlockScalar(out []uint64, block []uint64, w int) []uint64 {
	if w == 0 {
		return out
	}

	wU := uint(w)
	var acc [bp128Lanes]uint64
	var bitpos uint
	for p := range bp128PerLane {
		base := p * bp128Lanes
		for j := range bp128Lanes {
			acc[j] |= block[base+j] << bitpos
		}
		nb := bitpos + wU
		if nb >= 64 {
			out = append(out, acc[:]...)
			if nb > 64 {
				sh := 64 - bitpos
				for j := range bp128Lanes {
					acc[j] = block[base+j] >> sh
				}
			} else {
				acc = [bp128Lanes]uint64{}
			}
			bitpos = nb - 64
		} else {
			bitpos = nb
		}
	}
	if bitpos > 0 {
		out = append(out, acc[:]...)
	}

	return out
}

// bp128UnpackBlockScalar reverses bp128PackBlockScalar into dst[0:256], reading
// from packed. Returns the number of words consumed.
func bp128UnpackBlockScalar(dst []uint64, packed []uint64, w int) int {
	if w == 0 {
		for i := range bp128Block {
			dst[i] = 0
		}

		return 0
	}

	wU := uint(w)
	var mask uint64
	if w >= 64 {
		mask = ^uint64(0)
	} else {
		mask = (uint64(1) << wU) - 1
	}

	var cur [bp128Lanes]uint64
	in := 0
	copy(cur[:], packed[in:in+bp128Lanes])
	in += bp128Lanes
	var bitpos uint
	for p := range bp128PerLane {
		base := p * bp128Lanes
		nb := bitpos + wU
		switch {
		case nb < 64:
			for j := range bp128Lanes {
				dst[base+j] = (cur[j] >> bitpos) & mask
			}
			bitpos = nb
		case nb == 64:
			for j := range bp128Lanes {
				dst[base+j] = (cur[j] >> bitpos) & mask
			}
			if p != bp128PerLane-1 {
				copy(cur[:], packed[in:in+bp128Lanes])
				in += bp128Lanes
			}
			bitpos = 0
		default:
			var next [bp128Lanes]uint64
			copy(next[:], packed[in:in+bp128Lanes])
			in += bp128Lanes
			hiSh := 64 - bitpos
			for j := range bp128Lanes {
				dst[base+j] = ((cur[j] >> bitpos) | (next[j] << hiSh)) & mask
			}
			cur = next
			bitpos = nb - 64
		}
	}

	return in
}

// bp128Codec holds reusable scratch so steady-state encode/decode are
// allocation-free (mirrors how the production encoder/decoder would own scratch).
// The block kernels are reached through bp128PackBlock / bp128UnpackBlock, which
// dispatch to the AVX-512 asm when available and to the scalar reference
// otherwise — so the whole pipeline transparently uses the fastest kernel.
type bp128Codec struct {
	dods  []uint64
	words []uint64
}

func (c *bp128Codec) ensureDods(n int) []uint64 {
	if cap(c.dods) < n {
		c.dods = make([]uint64, n)
	}
	c.dods = c.dods[:n]

	return c.dods
}

// bp128ExcEntrySize is the on-stream size of one PFOR exception: position (1) +
// the full zigzag(dod) value (8).
const bp128ExcEntrySize = 1 + 8

// bp128ChooseWidth picks the pack width that minimises bytes for one block,
// given a histogram of value bit-lengths (hist[k] = #values needing k bits).
// Values wider than the chosen width become PFOR exceptions. Returns the width
// and the resulting exception count.
func bp128ChooseWidth(hist *[65]int) (width, nExc int) {
	const maxCost = int(^uint(0) >> 1)
	bestW, bestExc, bestCost := 64, 0, maxCost
	for cand := 0; cand <= 64; cand++ {
		exc := 0
		for k := cand + 1; k <= 64; k++ {
			exc += hist[k]
		}
		cost := bp128WordsPerBlock(cand)*8 + exc*bp128ExcEntrySize
		if cost < bestCost {
			bestCost, bestW, bestExc = cost, cand, exc
		}
	}

	return bestW, bestExc
}

// encode appends the spike pipeline format for ts to dst and returns it:
//
//	[firstTS:8] [firstDelta zigzag:8]?            (firstDelta present when n>=2)
//	per block (n>=3):
//	  [width:1][nExc:1] [exceptions: nExc×(pos:1, zigzagDod:8)] [packed words...]
//
// The first delta is stored explicitly (FOR base) so it never inflates block 0,
// and each block uses PFOR: rare outliers are stored as exceptions while the
// rest pack at a small width.
func (c *bp128Codec) encode(dst []byte, ts []int64, eng endian.EndianEngine) []byte {
	n := len(ts)
	if n == 0 {
		return dst
	}

	dst = eng.AppendUint64(dst, uint64(ts[0])) //nolint:gosec // bit-preserving
	if n == 1 {
		return dst
	}
	firstDelta := ts[1] - ts[0]
	dst = eng.AppendUint64(dst, bp128ZigZag(firstDelta))
	if n == 2 {
		return dst
	}

	nDod := n - 2
	nBlocks := (nDod + bp128Block - 1) / bp128Block
	need := nBlocks * bp128Block
	dods := c.ensureDods(need)
	prevDelta := firstDelta
	for i := 2; i < n; i++ {
		delta := ts[i] - ts[i-1]
		dods[i-2] = bp128ZigZag(delta - prevDelta)
		prevDelta = delta
	}
	for i := nDod; i < need; i++ { // zero the padded tail of the last block
		dods[i] = 0
	}

	for b := range nBlocks {
		block := dods[b*bp128Block : (b+1)*bp128Block]
		var hist [65]int
		for _, v := range block {
			hist[bits.Len64(v)]++
		}
		w, nExc := bp128ChooseWidth(&hist)
		dst = append(dst, byte(w), byte(nExc)) //nolint:gosec // Width and exception count are bounded by a 128-value block.

		var mask uint64
		if w >= 64 {
			mask = ^uint64(0)
		} else {
			mask = (uint64(1) << uint(w)) - 1
		}
		var mblk [bp128Block]uint64
		for i, v := range block {
			if bits.Len64(v) > w {
				dst = append(dst, byte(i))
				dst = eng.AppendUint64(dst, v)
			}
			mblk[i] = v & mask
		}
		c.words = bp128PackBlock(c.words[:0], mblk[:], w)
		for _, word := range c.words {
			dst = eng.AppendUint64(dst, word)
		}
	}

	return dst
}

// decodeInto decodes len(dst) timestamps from data into dst (allocation-free in
// steady state once scratch is warm).
func (c *bp128Codec) decodeInto(dst []int64, data []byte, eng endian.EndianEngine) {
	count := len(dst)
	if count == 0 {
		return
	}

	off := 0
	ts := int64(eng.Uint64(data[off:])) //nolint:gosec // bit-preserving
	off += 8
	dst[0] = ts
	if count == 1 {
		return
	}
	delta := bp128UnZigZag(eng.Uint64(data[off:]))
	off += 8
	ts += delta
	dst[1] = ts
	if count == 2 {
		return
	}

	nDod := count - 2
	nBlocks := (nDod + bp128Block - 1) / bp128Block
	dods := c.ensureDods(nBlocks * bp128Block)
	for b := range nBlocks {
		w := int(data[off])
		nExc := int(data[off+1])
		off += 2
		blk := dods[b*bp128Block : (b+1)*bp128Block]

		excAt := off
		off += nExc * bp128ExcEntrySize

		nWords := bp128WordsPerBlock(w)
		if cap(c.words) < nWords {
			c.words = make([]uint64, nWords)
		}
		words := c.words[:nWords]
		for k := range nWords {
			words[k] = eng.Uint64(data[off:])
			off += 8
		}
		bp128UnpackBlock(blk, words, w)

		for range nExc { // patch the outliers back over their masked slots
			pos := int(data[excAt])
			blk[pos] = eng.Uint64(data[excAt+1:])
			excAt += bp128ExcEntrySize
		}
	}

	for i := range nDod {
		delta += bp128UnZigZag(dods[i])
		ts += delta
		dst[i+2] = ts
	}
}

// bp128Encode is an allocating convenience wrapper around bp128Codec.encode.
func bp128Encode(ts []int64, eng endian.EndianEngine) []byte {
	var c bp128Codec

	return c.encode(nil, ts, eng)
}

// bp128Decode is an allocating convenience wrapper around bp128Codec.decodeInto.
func bp128Decode(data []byte, count int, eng endian.EndianEngine) []int64 {
	if count == 0 {
		return nil
	}
	out := make([]int64, count)
	var c bp128Codec
	c.decodeInto(out, data, eng)

	return out
}
