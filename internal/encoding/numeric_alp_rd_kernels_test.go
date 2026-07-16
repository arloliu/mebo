package encoding

import (
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/stretchr/testify/require"
)

// ---- frozen reference (the pre-bulk-unpack decodeRDInto this task must preserve) ----

// refDecodeRDInto is a frozen byte-for-byte copy of decodeRDInto as it stood
// before the two-stream bulk unpack was wired in: two windowed per-value
// reads (alpReadBitsFast) for the left code and the right part, straight into
// dst, then the exception patch loop. TestALPDecodeRDInto_BulkUnpack pins the
// bulk-unpack decoder against this reference.
func refDecodeRDInto(eng endian.EndianEngine, data []byte, count int, dst []float64) int {
	rbw := int(data[0])
	codeBits := int(data[1])
	nDict := int(data[2])
	nExc := int(eng.Uint32(data[3:7]))
	off := 7
	var dict [alpRDMaxDictSize]uint64
	for i := 0; i < nDict; i++ {
		dict[i] = uint64(eng.Uint16(data[off : off+2]))
		off += 2
	}
	leftCodes := data[off:]
	rights := data[off+(count*codeBits+7)/8:]
	exc := data[off+(count*codeBits+7)/8+(count*rbw+7)/8:]

	rightMask := ^uint64(0)
	if rbw < 64 {
		rightMask = (uint64(1) << rbw) - 1
	}
	codeMask := ^uint64(0)
	if codeBits < 64 {
		codeMask = (uint64(1) << codeBits) - 1
	}

	n := count
	if n > len(dst) {
		n = len(dst)
	}

	for i := 0; i < n; i++ {
		right := alpReadBitsFast(rights, i*rbw, rbw, rightMask)
		left := dict[alpReadBitsFast(leftCodes, i*codeBits, codeBits, codeMask)]
		dst[i] = math.Float64frombits((left << rbw) | right)
	}
	for k := 0; k < nExc; k++ {
		p := int(eng.Uint32(exc[k*6 : k*6+4]))
		if p < n {
			left := uint64(eng.Uint16(exc[k*6+4 : k*6+6]))
			right := alpReadBitsFast(rights, p*rbw, rbw, rightMask)
			dst[p] = math.Float64frombits((left << rbw) | right)
		}
	}

	return n
}

// alpUnpackBitsScalarRef extracts n w-bit codes LSB-first from codes into dst
// using the same windowed-read building block the RD scalar path uses
// (alpReadBitsFast/alpReadBitsSlow) — the oracle alpUnpackBits[w] must match
// bit-for-bit.
func alpUnpackBitsScalarRef(codes []byte, n, w int, dst []uint64) {
	mask := maskW(w)
	for i := 0; i < n; i++ {
		dst[i] = alpReadBitsFast(codes, i*w, w, mask)
	}
}

// buildALPRD assembles a synthetic ALP-RD body in the exact layout
// decodeRDInto parses (header + dict + LSB-first packed left codes + LSB-first
// packed rights + 6-byte exception records), letting the tests sweep every
// (rbw, codeBits, count, exception) shape directly rather than relying on the
// encoder's data-driven parameter choice. It returns the data[1:] form (no
// scheme byte). nDict is taken as len(dict); exception positions must be <
// count.
func buildALPRD(eng endian.EndianEngine, rbw, codeBits int, dict, leftCodes, rights []uint64, excPos []int, excLeft []uint64) []byte {
	nDict := len(dict)
	var hdr [7]byte
	hdr[0] = byte(rbw)
	hdr[1] = byte(codeBits)
	hdr[2] = byte(nDict)
	eng.PutUint32(hdr[3:7], uint32(len(excPos)))
	data := append([]byte(nil), hdr[:]...)
	for i := 0; i < nDict; i++ {
		var b [2]byte
		eng.PutUint16(b[:], uint16(dict[i]))
		data = append(data, b[:]...)
	}
	data = alpPackBits(data, leftCodes, codeBits)
	data = alpPackBits(data, rights, rbw)
	for i := range excPos {
		var rec [6]byte
		eng.PutUint32(rec[0:4], uint32(excPos[i]))
		eng.PutUint16(rec[4:6], uint16(excLeft[i]))
		data = append(data, rec[:]...)
	}

	return data
}

// TestALPUnpackBitsKernel_Differential drives every non-nil width kernel in
// alpUnpackBits directly and checks it reproduces the scalar windowed-read
// extraction bit-for-bit. Each case packs exactly ceil(n*w/8) code bytes plus
// the minimal over-read slack (8*ceil(w/8)-w bytes of random noise) — the
// over-read boundary witness: an off-by-one in slack would panic at the last
// group's array-pointer conversion, and any dependence on the noise bytes
// would fail the value check (mirrors TestALPKernel_Differential in
// numeric_alp_kernels_test.go for the fused ALP-main kernels).
func TestALPUnpackBitsKernel_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(20260716))
	counts := []int{8, 16, 1000, 1024}
	for w := 1; w <= 64; w++ {
		kern := alpUnpackBits[w]
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
			got := make([]uint64, n)
			want := make([]uint64, n)
			kern(buf, n, got)
			alpUnpackBitsScalarRef(buf, n, w, want)
			for i := 0; i < n; i++ {
				if got[i] != want[i] {
					t.Fatalf("unpack kernel w=%d n=%d idx=%d: got %#x want %#x", w, n, i, got[i], want[i])
				}
			}
		}
	}
}

// TestALPDecodeRDInto_BulkUnpack pins the kernel-dispatched decodeRDInto
// against the frozen scalar reference across every reachable (rbw, codeBits)
// combination (rbw 48..63, codeBits 1..3 — see numeric_alp.go's alpRDCutLimit
// and alpRDMaxDictSize), a count set spanning the count<8 kernel-skip guard,
// multiples and non-multiples of 8 (tail handling), exceptions present/
// absent, and full/partial dst (clamping + exception p<n guard). The two must
// agree bit-for-bit.
func TestALPDecodeRDInto_BulkUnpack(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	dec := NewNumericALPDecoder(eng)
	rng := rand.New(rand.NewSource(424242))

	counts := []int{1, 7, 8, 16, 1000, 1003, 1024}

	for rbw := 48; rbw <= 63; rbw++ {
		rMask := maskW(rbw)
		for codeBits := 1; codeBits <= 3; codeBits++ {
			cMask := maskW(codeBits)
			nDict := int(cMask) + 1
			if nDict > alpRDMaxDictSize {
				nDict = alpRDMaxDictSize
			}
			dict := make([]uint64, nDict)
			for i := range dict {
				dict[i] = rng.Uint64()
			}
			for _, count := range counts {
				leftCodes := make([]uint64, count)
				rights := make([]uint64, count)
				for i := range leftCodes {
					leftCodes[i] = rng.Uint64() & cMask
					rights[i] = rng.Uint64() & rMask
				}
				for _, withExc := range []bool{false, true} {
					var excPos []int
					var excLeft []uint64
					if withExc {
						seen := map[int]bool{}
						add := func(p int) {
							if p >= 0 && p < count && !seen[p] {
								seen[p] = true
								excPos = append(excPos, p)
							}
						}
						add(0)
						if count > 2 {
							add(count / 2)
						}
						if count > 1 {
							add(count - 1)
						}
						sort.Ints(excPos)
						for range excPos {
							excLeft = append(excLeft, rng.Uint64()&0xFFFF)
						}
					}
					data := buildALPRD(eng, rbw, codeBits, dict, leftCodes, rights, excPos, excLeft)
					for _, dstLen := range []int{count, count - 3} {
						if dstLen <= 0 {
							continue
						}
						got := make([]float64, dstLen)
						want := make([]float64, dstLen)
						ng := dec.decodeRDInto(data, count, got)
						nw := refDecodeRDInto(eng, data, count, want)
						if ng != nw {
							t.Fatalf("rbw=%d codeBits=%d count=%d dstLen=%d: n mismatch got %d want %d",
								rbw, codeBits, count, dstLen, ng, nw)
						}
						for i := 0; i < dstLen; i++ {
							if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
								t.Fatalf("rbw=%d codeBits=%d count=%d withExc=%v dstLen=%d idx=%d: got %#016x want %#016x",
									rbw, codeBits, count, withExc, dstLen, i,
									math.Float64bits(got[i]), math.Float64bits(want[i]))
							}
						}
					}
				}
			}
		}
	}
}

// TestALPDecodeRDInto_WholePipeline drives the full encode -> DecodeAll path
// (not the synthetic buildALPRD harness) for genuinely RD-routed full-
// precision columns at sizes that cross the pooled-scratch group boundary
// multiple times, and checks DecodeAll agrees with All() bit-for-bit.
func TestALPDecodeRDInto_WholePipeline(t *testing.T) {
	eng := endian.GetLittleEndianEngine()
	dec := NewNumericALPDecoder(eng)
	for _, n := range []int{8, 9, 50, 300, 1000, 2000, 4096} {
		values := genALPColumns(1, n, -1, 12345)[0]
		data := alpEncodeSlice(values, eng)
		require.Equalf(t, alpSchemeRD, data[0], "n=%d: expected RD scheme", n)

		want := alpDecodeAll(data, len(values), eng)
		got := make([]float64, len(values))
		ng := dec.DecodeAll(data, len(values), got)
		require.Equalf(t, len(values), ng, "n=%d: DecodeAll count", n)
		for i := range want {
			require.Equalf(t, math.Float64bits(want[i]), math.Float64bits(got[i]), "n=%d idx=%d", n, i)
		}
	}
}
