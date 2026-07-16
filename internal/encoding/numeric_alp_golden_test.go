package encoding

import (
	"math"
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/require"
)

// TestNumericALP_GoldenBytes pins NumericALPEncoder's output — for four
// representative datasets, encoded with both endian engines — to recorded
// xxhash.Sum64 constants. This is the safety net for the ALP performance
// work: every Phase 1-2 optimization to numeric_alp.go must keep the
// encoder's bytes identical, and this test fails the instant a single
// output byte changes.
//
// To regenerate the constants after an INTENTIONAL wire-format change, run
//
//	go test ./internal/encoding/ -run TestNumericALP_GoldenBytes -v
//
// read the "hash: 0x..." lines from the log output, and paste the new
// values in below.
//
// 2026-07: the fast-round optimization (magic-number round with a
// legacy math.Round fallback for |scaled| >= 2^51) was adopted in
// alpEncodeDigit/alpBestEF and the encoder's output was verified
// byte-identical on this corpus — the constants below did NOT need
// regeneration. The byte-identity contract is now enforced empirically by
// numeric_alp_crossver_test.go (lossless, digit-divergence, and size-parity
// tests, gate-run at MEBO_ALP_VERIFY_N=10000000) rather than by policy.
const (
	alpGoldenPoints = 1000
	alpGoldenSeed   = 42

	alpGoldenDecimal2dpLE      uint64 = 0xc642985c2b608526
	alpGoldenDecimal2dpBE      uint64 = 0xa1b2f948018ababa
	alpGoldenFullPrecisionLE   uint64 = 0x7a1535f788abf63e
	alpGoldenFullPrecisionBE   uint64 = 0x012f9aeaafd7889e
	alpGoldenMixedExceptionsLE uint64 = 0x6ce54b0b6aeb51c0
	alpGoldenMixedExceptionsBE uint64 = 0x00a6c863bdfd2940
	alpGoldenConstantLE        uint64 = 0x9569360489db2778
	alpGoldenConstantBE        uint64 = 0xf24ee91274dbe21e
)

// alpGoldenHash encodes values with the given engine and returns the
// xxhash.Sum64 of the resulting bytes.
func alpGoldenHash(values []float64, eng endian.EndianEngine) uint64 {
	enc := NewNumericALPEncoder(eng)
	enc.WriteSlice(values)
	h := xxhash.Sum64(enc.Bytes())
	enc.Finish()

	return h
}

func TestNumericALP_GoldenBytes(t *testing.T) {
	decimal2dp := genALPColumns(1, alpGoldenPoints, 2, alpGoldenSeed)[0]
	fullPrecision := genALPColumns(1, alpGoldenPoints, -1, alpGoldenSeed)[0]

	// Same 2dp random walk, but every 97th value (1-indexed) is replaced by a
	// full-precision irrational constant that cannot round-trip through ALP
	// main's (e,f) digit encoding — forcing the exception sidecar path.
	mixedExceptions := append([]float64(nil), decimal2dp...)
	for i := range mixedExceptions {
		if (i+1)%97 == 0 {
			mixedExceptions[i] = math.Pi * 1e17
		}
	}

	// A constant column drives FOR min == max, i.e. bit width 0 — the
	// degenerate alpPackBits early-return path.
	constant := make([]float64, alpGoldenPoints)
	for i := range constant {
		constant[i] = 123.45
	}

	cases := []struct {
		name   string
		values []float64
		wantLE uint64
		wantBE uint64
	}{
		{"decimal2dp", decimal2dp, alpGoldenDecimal2dpLE, alpGoldenDecimal2dpBE},
		{"fullPrecision", fullPrecision, alpGoldenFullPrecisionLE, alpGoldenFullPrecisionBE},
		{"mixedExceptions", mixedExceptions, alpGoldenMixedExceptionsLE, alpGoldenMixedExceptionsBE},
		{"constant", constant, alpGoldenConstantLE, alpGoldenConstantBE},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLE := alpGoldenHash(tc.values, endian.GetLittleEndianEngine())
			gotBE := alpGoldenHash(tc.values, endian.GetBigEndianEngine())
			t.Logf("%s little-endian hash: 0x%016x", tc.name, gotLE)
			t.Logf("%s big-endian hash: 0x%016x", tc.name, gotBE)
			require.Equalf(t, tc.wantLE, gotLE,
				"%s: little-endian ALP output changed (got hash 0x%016x)", tc.name, gotLE)
			require.Equalf(t, tc.wantBE, gotBE,
				"%s: big-endian ALP output changed (got hash 0x%016x)", tc.name, gotBE)
		})
	}
}
