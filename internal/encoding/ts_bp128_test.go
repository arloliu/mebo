package encoding

import (
	"math/bits"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

// makeWidthBlock builds one 256-value block whose values each fit in exactly w
// bits (includes 0 and the max value (1<<w)-1 so boundary bits are exercised).
func makeWidthBlock(w int, seed uint64) []uint64 {
	block := make([]uint64, bp128Block)
	if w == 0 {
		return block // all zeros
	}
	var mask uint64
	if w >= 64 {
		mask = ^uint64(0)
	} else {
		mask = (uint64(1) << uint(w)) - 1
	}
	r := newLCG(seed)
	for i := range block {
		switch i {
		case 0:
			block[i] = 0
		case 1:
			block[i] = mask // max value at this width
		default:
			block[i] = r.s & mask
			r.f64() // advance
		}
	}

	return block
}

// TestBP128Scalar_RoundTripAllWidths is the core correctness contract: every
// fixed width 0..64 must pack then unpack bit-exactly. This is the oracle the
// AVX-512 asm kernels will be diffed against.
func TestBP128Scalar_RoundTripAllWidths(t *testing.T) {
	for w := 0; w <= 64; w++ {
		block := makeWidthBlock(w, uint64(w)+1)
		packed := bp128PackBlockScalar(nil, block, w)

		dst := make([]uint64, bp128Block)
		used := bp128UnpackBlockScalar(dst, packed, w)

		require.Equalf(t, len(packed), used, "w=%d: unpack must consume exactly the packed words", w)
		require.Equalf(t, block, dst, "w=%d: scalar pack/unpack must round-trip bit-exact", w)
	}
}

// TestBP128Scalar_WordCount locks the per-block word count formula so the asm
// and decoder agree on stream geometry: 8*ceil(32*w/64) words, 0 when w==0.
func TestBP128Scalar_WordCount(t *testing.T) {
	for w := 0; w <= 64; w++ {
		block := makeWidthBlock(w, 7)
		packed := bp128PackBlockScalar(nil, block, w)
		want := 0
		if w > 0 {
			wordsPerLane := (bp128PerLane*w + 63) / 64
			want = bp128Lanes * wordsPerLane
		}
		require.Equalf(t, want, len(packed), "w=%d word count", w)
	}
}

// TestBP128Scalar_PipelineRoundTrip proves the full timestamp pipeline
// (dod+zigzag -> pack / unpack -> prefix-sum) is lossless on realistic shapes
// at the pinned gate sizes, both endians, clean and bursty.
func TestBP128Scalar_PipelineRoundTrip(t *testing.T) {
	sizes := []int{0, 1, 2, 32, 127, 256, 257, 1000, 4096}
	for _, eng := range []endian.EndianEngine{endian.GetLittleEndianEngine(), endian.GetBigEndianEngine()} {
		for _, n := range sizes {
			for _, jit := range []float64{0, 0.1, 2.0} {
				var ts []int64
				if n > 0 {
					ts = genTimestamps(n, jit, int64(n*7+1))
				}
				data := bp128Encode(nil, ts, eng)
				got := bp128Decode(data, n, eng)
				require.Equalf(t, ts, got, "n=%d jitter=%.1f endian=%s round-trip", n, jit, eng.String())
			}
		}
	}
}

// bp128EncodeNoPFOR mirrors the production pipeline (first-delta extraction +
// per-block fixed width) but never uses exceptions: each block is packed at the
// width of its widest value. It is the baseline PFOR must beat.
func bp128EncodeNoPFOR(dst []byte, ts []int64, eng endian.EndianEngine) []byte {
	n := len(ts)
	if n == 0 {
		return dst
	}
	dst = eng.AppendUint64(dst, uint64(ts[0]))
	if n == 1 {
		return dst
	}
	firstDelta := ts[1] - ts[0]
	dst = eng.AppendUint64(dst, s8bZigZag(firstDelta))
	if n == 2 {
		return dst
	}

	nDod := n - 2
	nBlocks := (nDod + bp128Block - 1) / bp128Block
	dods := make([]uint64, nBlocks*bp128Block)
	prevDelta := firstDelta
	for i := 2; i < n; i++ {
		delta := ts[i] - ts[i-1]
		dods[i-2] = s8bZigZag(delta - prevDelta)
		prevDelta = delta
	}

	var words []uint64
	for b := range nBlocks {
		block := dods[b*bp128Block : (b+1)*bp128Block]
		w := 0
		for _, v := range block {
			if bl := bits.Len64(v); bl > w {
				w = bl
			}
		}
		dst = append(dst, byte(w), 0) // width, nExc=0
		words = bp128PackBlock(words[:0], block, w)
		for _, word := range words {
			dst = eng.AppendUint64(dst, word)
		}
	}

	return dst
}

// TestBP128_PFOR proves Patched-FOR: a block of small values with a few large
// outliers must round-trip, and must encode far smaller than if the outliers
// forced the whole block to their width (i.e. exceptions are actually used).
func TestBP128_PFOR(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	// 1000 timestamps at a steady ~1s cadence (small dods) with rare 5s gaps.
	ts := make([]int64, 1000)
	cur := int64(1_700_000_000_000_000)
	for i := range ts {
		step := int64(1_000_000)
		if i%173 == 0 && i > 0 {
			step += 5_000_000 // outlier dod
		}
		cur += step
		ts[i] = cur
	}
	data := bp128Encode(nil, ts, eng)
	require.Equal(t, ts, bp128Decode(data, len(ts), eng))

	// Force every block to the widest value (no exceptions) and confirm PFOR is
	// meaningfully smaller — proves outliers are patched, not absorbed into width.
	naive := bp128EncodeNoPFOR(nil, ts, eng)
	require.Lessf(t, len(data), len(naive)*7/10,
		"PFOR (%d B) must be >=30%% smaller than no-exception packing (%d B)", len(data), len(naive))
}

// TestBP128Scalar_BurstyGaps stresses the bursty path: large dod spikes from
// gaps/restarts must still round-trip (this is the ratio-on-bursty gate input).
func TestBP128Scalar_BurstyGaps(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	ts := genTimestamps(1000, 0.1, 99)
	// inject gaps/restarts -> big delta-of-delta spikes
	for i := 100; i < len(ts); i += 137 {
		ts[i] += 5_000_000 // a missed-scrape gap
	}
	// keep monotonic
	for i := 1; i < len(ts); i++ {
		if ts[i] <= ts[i-1] {
			ts[i] = ts[i-1] + 1
		}
	}
	data := bp128Encode(nil, ts, eng)
	got := bp128Decode(data, len(ts), eng)
	require.Equal(t, ts, got)
}
