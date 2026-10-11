package alp

import (
	"encoding/binary"
	"math"
	"slices"
	"testing"

	"github.com/arloliu/mebo/endian"
)

// alpFuzzHeaderSize is the fixed prefix FuzzALPDecode reads its column parameters from;
// the rest of the input becomes code bytes, exception values and trailing slack.
const alpFuzzHeaderSize = 16

// alpFuzzParams are the column parameters at the head of a FuzzALPDecode input.
type alpFuzzParams struct {
	e, f, width byte
	mn          int64
	count       uint16
	dstDelta    int8 // dst length is count+dstDelta%9
	nExc        byte // exceptions: nExc%8, at most one per 8 payload bytes left after the codes
	trail       byte // bytes of trailing slack: trail%64
}

// alpFuzzSeed builds a FuzzALPDecode input from column parameters and payload bytes.
func alpFuzzSeed(p alpFuzzParams, payload []byte) []byte {
	b := make([]byte, alpFuzzHeaderSize, alpFuzzHeaderSize+len(payload))
	b[0], b[1], b[2] = p.e, p.f, p.width
	binary.LittleEndian.PutUint64(b[3:11], uint64(p.mn))
	binary.LittleEndian.PutUint16(b[11:13], p.count)
	b[13], b[14], b[15] = byte(p.dstDelta), p.nExc, p.trail

	return append(b, payload...)
}

// alpFuzzColumn is an ALP-main column built by alpFuzzMainColumn,
// with its record count and the dst length to decode it into.
type alpFuzzColumn struct {
	data          []byte
	count, dstLen int
}

// alpFuzzMainColumn turns a fuzz input into an ALP-main column that passes the blob's column validation:
// e and f within the power tables, width up to 64, a code region of exactly ceil(count*width/8) bytes,
// and up to 7 exceptions at strictly ascending positions below count.
// Code bytes are arbitrary, and so are up to 63 bytes of trailing slack after the exceptions.
// The dst length lies within 8 of count, so DecodeAll writes min(count, dstLen) values.
// It reports false when the input is too short for a header and one value.
func alpFuzzMainColumn(in []byte) (alpFuzzColumn, bool) {
	if len(in) < alpFuzzHeaderSize {
		return alpFuzzColumn{}, false
	}
	e, f := in[0]%(alpMaxExponent+1), in[1]%(alpMaxExponent+1)
	width := in[2] % 65
	mn := binary.LittleEndian.Uint64(in[3:11])
	count := int(binary.LittleEndian.Uint16(in[11:13]) % 4096)
	dstDelta := int(int8(in[13]) % 9)
	nExc := int(in[14] % 8)
	trail := int(in[15] % 64)
	rest := in[alpFuzzHeaderSize:]

	// Shrink count until its codes fit in the input.
	w := int(width)
	if w > 0 && count*w > len(rest)*8 {
		count = len(rest) * 8 / w
	}
	if count == 0 {
		return alpFuzzColumn{}, false
	}
	codeLen := (count*w + 7) / 8
	codes, rest := rest[:codeLen], rest[codeLen:]
	nExc = min(nExc, count, len(rest)/8)

	// Exception positions spread across the column, strictly ascending and below count.
	pos := make([]uint32, 0, nExc)
	for k := range nExc {
		pos = append(pos, uint32(int(rest[k*8])*count/256))
	}
	slices.Sort(pos)
	pos = slices.Compact(pos)

	data := make([]byte, 0, 16+codeLen+12*len(pos)+trail)
	data = append(data, alpSchemeMain, e, f, width)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(pos)))
	data = binary.LittleEndian.AppendUint64(data, mn)
	data = append(data, codes...)
	for k, p := range pos {
		data = binary.LittleEndian.AppendUint32(data, p)
		data = append(data, rest[k*8:k*8+8]...)
	}
	rest = rest[nExc*8:]
	data = append(data, rest[:min(trail, len(rest))]...)

	return alpFuzzColumn{data: data, count: count, dstLen: max(count+dstDelta, 0)}, true
}

// FuzzALPDecode decodes ALP-main columns built from arbitrary bytes (see alpFuzzMainColumn)
// with DecodeAll, which runs the AVX-512 decode kernel where the CPU supports it.
// The result must match, bit for bit, both DecodeAll with the kernel switched off
// and the per-value All iterator, which share no unpack code with the kernel.
func FuzzALPDecode(f *testing.F) {
	pat := make([]byte, 4096)
	for i := range pat {
		pat[i] = byte(i*131 + i>>3)
	}
	for _, w := range []byte{0, 1, 7, 8, 12, 17, 32, 52, 58, 59, 60, 61, 63, 64} {
		f.Add(alpFuzzSeed(alpFuzzParams{e: 2, width: w, mn: -98765, count: 150}, pat))
		f.Add(alpFuzzSeed(alpFuzzParams{e: 14, f: 12, width: w, mn: math.MinInt64, count: 1024, dstDelta: -5, nExc: 5, trail: 40}, pat))
	}
	f.Add(alpFuzzSeed(alpFuzzParams{width: 16, mn: math.MaxInt64, count: 7, nExc: 1}, pat[:64]))
	f.Add(alpFuzzSeed(alpFuzzParams{e: 18, f: 18, width: 64, count: 8, dstDelta: -8}, pat[:64]))

	dec := NewNumericALPDecoder(endian.GetLittleEndianEngine())
	kernel := alpDecAVX512
	f.Fuzz(func(t *testing.T, data []byte) {
		col, ok := alpFuzzMainColumn(data)
		if !ok {
			return
		}
		column, count, dstLen := col.data, col.count, col.dstLen
		want := min(count, dstLen)

		got := make([]float64, dstLen)
		if n := dec.DecodeAll(column, count, got); n != want {
			t.Fatalf("DecodeAll returned %d, want %d", n, want)
		}

		scalar := make([]float64, dstLen)
		alpDecAVX512 = false
		n := dec.DecodeAll(column, count, scalar)
		alpDecAVX512 = kernel
		if n != want {
			t.Fatalf("scalar DecodeAll returned %d, want %d", n, want)
		}

		iter := make([]float64, 0, count)
		for v := range dec.All(column, count) {
			iter = append(iter, v)
		}
		if len(iter) != count {
			t.Fatalf("All yielded %d values, want %d", len(iter), count)
		}

		for i := range want {
			g := math.Float64bits(got[i])
			if s := math.Float64bits(scalar[i]); g != s {
				t.Fatalf("value %d: kernel %#016x, scalar %#016x", i, g, s)
			}
			if a := math.Float64bits(iter[i]); g != a {
				t.Fatalf("value %d: DecodeAll %#016x, All %#016x", i, g, a)
			}
		}
	})
}
