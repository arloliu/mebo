package encoding

import (
	"encoding/binary"
	"math"
	"math/rand"
	"testing"

	"github.com/arloliu/mebo/endian"
)

// ---- frozen references (the pre-kernel scalar decode that must be preserved) ----

// alpDecodeCodesScalarRef is the exact per-value scalar decode of decodeMainInto's
// bulk loop (windowed 8-byte load + shift/mask, alpReadBitsSlow tail fallback,
// then float64(int64(code)+mn)*pf*ie). It is the value-level oracle the fused
// kernels must reproduce bit-for-bit.
func alpDecodeCodesScalarRef(codes []byte, count, width int, mn int64, pf, ie float64, dst []float64) {
	mask := ^uint64(0)
	if width < 64 {
		mask = (uint64(1) << uint(width)) - 1
	}
	clen := len(codes)
	bitpos := 0
	for i := 0; i < count; i++ {
		bp := bitpos >> 3
		sh := bitpos & 7
		var code uint64
		if bp+8 <= clen && sh+width <= 64 {
			code = (binary.LittleEndian.Uint64(codes[bp:bp+8]) >> sh) & mask
		} else {
			code = alpReadBitsSlow(codes, bitpos, width, mask)
		}
		dst[i] = float64(int64(code)+mn) * pf * ie
		bitpos += width
	}
}

// refDecodeMainInto is a frozen byte-for-byte copy of decodeMainInto as it stood
// before kernel dispatch was wired in (the scalar loop + post-pass exception
// patch). TestALPDecodeMainInto_Kernels pins the wired decoder against it.
func refDecodeMainInto(eng endian.EndianEngine, data []byte, count int, dst []float64) int {
	ee := int(data[0])
	ff := int(data[1])
	width := int(data[2])
	nExc := int(eng.Uint32(data[3:7]))
	mn := int64(eng.Uint64(data[7:15]))
	codes := data[15:]
	exc := data[15+(count*width+7)/8:]

	mask := ^uint64(0)
	if width < 64 {
		mask = (uint64(1) << uint(width)) - 1
	}
	clen := len(codes)

	n := count
	if n > len(dst) {
		n = len(dst)
	}
	bitpos := 0
	for i := 0; i < n; i++ {
		bp := bitpos >> 3
		sh := bitpos & 7
		var code uint64
		if bp+8 <= clen && sh+width <= 64 {
			code = (binary.LittleEndian.Uint64(codes[bp:bp+8]) >> sh) & mask
		} else {
			code = alpReadBitsSlow(codes, bitpos, width, mask)
		}
		dst[i] = float64(int64(code)+mn) * alpPow10[ff] * alpInvPow10[ee]
		bitpos += width
	}
	for k := 0; k < nExc; k++ {
		p := int(eng.Uint32(exc[k*12 : k*12+4]))
		if p < n {
			dst[p] = math.Float64frombits(eng.Uint64(exc[k*12+4 : k*12+12]))
		}
	}

	return n
}

// buildALPMain assembles a synthetic ALP-main body in the exact layout
// decodeMainInto parses (header + LSB-first packed codes + 12-byte exception
// records), letting the tests sweep every width/count/exception shape directly
// rather than relying on the encoder's data-driven width choice. It returns the
// data[1:] form (no scheme byte). Exception positions must be < len(codes); the
// code stored at those positions is irrelevant (the patch overwrites them).
func buildALPMain(eng endian.EndianEngine, ee, ff, width int, mn int64, codes []uint64, excPos []int, excVal []float64) []byte {
	var hdr [15]byte
	hdr[0] = byte(ee)
	hdr[1] = byte(ff)
	hdr[2] = byte(width)
	eng.PutUint32(hdr[3:7], uint32(len(excPos)))
	eng.PutUint64(hdr[7:15], uint64(mn))
	data := append([]byte(nil), hdr[:]...)
	data = alpPackBits(data, codes, width)
	for i := range excPos {
		var rec [12]byte
		eng.PutUint32(rec[0:4], uint32(excPos[i]))
		eng.PutUint64(rec[4:12], math.Float64bits(excVal[i]))
		data = append(data, rec[:]...)
	}

	return data
}

func maskW(width int) uint64 {
	if width >= 64 {
		return ^uint64(0)
	}

	return (uint64(1) << uint(width)) - 1
}

// TestALPKernel_Differential drives every non-nil width kernel directly and
// checks it reproduces the scalar decode bit-for-bit. Each case packs exactly
// ceil(n*w/8) code bytes plus the minimal over-read slack (8*ceil(w/8)-w bytes
// of random noise) — so the last group's array-pointer conversion lands exactly
// at the end of the slice. If the slack math were off by one, the conversion
// would panic here (this is the over-read boundary witness), and if the kernel
// depended on the noise bytes the value comparison would fail.
func TestALPKernel_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(20260715))
	counts := []int{8, 16, 1000, 1024}
	mns := []int64{-1234567, 42, 0}
	scales := []struct{ pf, ie float64 }{
		{1, 1},
		{alpPow10[2], alpInvPow10[0]}, // 2dp-like
		{alpPow10[7], alpInvPow10[4]},
	}
	for w := 1; w <= 64; w++ {
		kern := alpFusedUnpack[w]
		if kern == nil {
			continue
		}
		mask := maskW(w)
		nwords := (w + 7) / 8
		slack := nwords*8 - w
		for _, n := range counts {
			codes := make([]uint64, n)
			for i := range codes {
				codes[i] = rng.Uint64() & mask
			}
			packed := alpPackBits(nil, codes, w) // exactly ceil(n*w/8) bytes
			buf := make([]byte, len(packed)+slack)
			copy(buf, packed)
			for i := len(packed); i < len(buf); i++ {
				buf[i] = byte(rng.Intn(256)) // noise in the over-read region
			}
			for _, mn := range mns {
				for _, s := range scales {
					got := make([]float64, n)
					want := make([]float64, n)
					kern(buf, n, mn, s.pf, s.ie, got)
					alpDecodeCodesScalarRef(buf, n, w, mn, s.pf, s.ie, want)
					for i := 0; i < n; i++ {
						if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
							t.Fatalf("kernel w=%d n=%d mn=%d pf=%g ie=%g idx=%d: got %#016x want %#016x",
								w, n, mn, s.pf, s.ie, i, math.Float64bits(got[i]), math.Float64bits(want[i]))
						}
					}
				}
			}
		}
	}
}

// TestALPDecodeMainInto_Kernels pins the kernel-dispatched decodeMainInto against
// the frozen scalar reference across every width, a count set spanning multiples
// and non-multiples of 8 (tail handling) and the count<8 kernel-skip guard,
// positive/negative min, exceptions present/absent, and full/partial dst
// (clamping + exception p<n guard). The two must agree bit-for-bit.
func TestALPDecodeMainInto_Kernels(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	dec := NewNumericALPDecoder(eng)
	rng := rand.New(rand.NewSource(987654321))

	counts := []int{1, 3, 7, 8, 16, 1000, 1003, 1024}
	mns := []int64{-98765, 0, 31337}
	efs := []struct{ ee, ff int }{{0, 0}, {2, 0}, {7, 3}}

	for w := 1; w <= 64; w++ {
		mask := maskW(w)
		for _, count := range counts {
			codes := make([]uint64, count)
			for i := range codes {
				codes[i] = rng.Uint64() & mask
			}
			// exception shapes: none, and a spread including first/last.
			excShapes := [][]int{nil}
			if count >= 1 {
				spread := []int{0}
				if count > 2 {
					spread = append(spread, count/2)
				}
				if count > 1 {
					spread = append(spread, count-1)
				}
				excShapes = append(excShapes, spread)
			}
			for _, mn := range mns {
				ef := efs[(w+count)%len(efs)] // rotate e/f so all get exercised
				for _, excPos := range excShapes {
					excVal := make([]float64, len(excPos))
					for i := range excVal {
						excVal[i] = rng.NormFloat64() * math.Pi * 1e9
					}
					data := buildALPMain(eng, ef.ee, ef.ff, w, mn, codes, excPos, excVal)
					for _, dstLen := range []int{count, count - 3} {
						if dstLen <= 0 {
							continue
						}
						got := make([]float64, dstLen)
						want := make([]float64, dstLen)
						ng := dec.decodeMainInto(data, count, got)
						nw := refDecodeMainInto(eng, data, count, want)
						if ng != nw {
							t.Fatalf("w=%d count=%d dstLen=%d: n mismatch got %d want %d", w, count, dstLen, ng, nw)
						}
						for i := 0; i < dstLen; i++ {
							if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
								t.Fatalf("w=%d count=%d mn=%d ef=%v nExc=%d dstLen=%d idx=%d: got %#016x want %#016x",
									w, count, mn, ef, len(excPos), dstLen, i,
									math.Float64bits(got[i]), math.Float64bits(want[i]))
							}
						}
					}
				}
			}
		}
	}
}
