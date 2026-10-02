package blob

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/section"
)

// alpValidateTestEntry builds a single-column NumericIndexEntry describing a
// column that starts at offset 0 in the (single-entry) valPayload passed to
// validateALPColumns in these tests.
func alpValidateTestEntry(metricID uint64, count, valueLength int) section.NumericIndexEntry {
	return section.NumericIndexEntry{
		MetricID:    metricID,
		ValueOffset: 0,
		ValueLength: valueLength,
		Count:       count,
	}
}

// TestValidateALPColumns_Main drives validateALPColumns directly with
// hand-built ALP-main column payloads (scheme byte 0), covering every
// length-validation branch documented in the function's doc comment.
func TestValidateALPColumns_Main(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// buildMain constructs a scheme-0 column: [scheme:1][e:1][f:1][width:1]
	// [nExc:4][min:8] + codesLen bytes (codes region) + excLen bytes
	// (exceptions region). Region contents are zero-filled; only the
	// declared width/nExc header fields and the overall byte length matter
	// to validateALPColumns.
	buildMain := func(width, nExc, codesLen, excLen int) []byte {
		body := make([]byte, 15+codesLen+excLen)
		body[2] = byte(width)
		engine.PutUint32(body[3:7], uint32(nExc))

		return append([]byte{0}, body...)
	}

	t.Run("body shorter than 15 bytes", func(t *testing.T) {
		column := append([]byte{0}, make([]byte, 5)...) // body len 5 < 15
		entry := alpValidateTestEntry(1, 8, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("codes region truncated", func(t *testing.T) {
		width, nExc, count := 4, 0, 8
		wantCodesLen := (count*width + 7) / 8 // 4 bytes
		column := buildMain(width, nExc, wantCodesLen-1, 0)
		entry := alpValidateTestEntry(2, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("exceptions truncated", func(t *testing.T) {
		width, nExc, count := 4, 1, 8
		codesLen := (count*width + 7) / 8
		excLen := nExc*12 - 1 // one byte short of the declared nExc
		column := buildMain(width, nExc, codesLen, excLen)
		entry := alpValidateTestEntry(3, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("exponent or factor out of range", func(t *testing.T) {
		width, nExc, count := 4, 0, 8
		codesLen := (count*width + 7) / 8
		for _, ef := range [][2]int{{19, 0}, {0, 19}, {255, 255}} {
			column := buildMain(width, nExc, codesLen, 0)
			column[1], column[2] = byte(ef[0]), byte(ef[1])
			entry := alpValidateTestEntry(8, count, len(column))
			err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
			require.ErrorIsf(t, err, errs.ErrInvalidALPColumn, "e=%d f=%d", ef[0], ef[1])
		}
	})

	t.Run("exponent and factor at max boundary (18) are not falsely rejected", func(t *testing.T) {
		width, nExc, count := 4, 0, 8
		codesLen := (count*width + 7) / 8
		column := buildMain(width, nExc, codesLen, 0)
		column[1], column[2] = 18, 18
		entry := alpValidateTestEntry(9, count, len(column))
		require.NoError(t, validateALPColumns(column, []section.NumericIndexEntry{entry}, engine))
	})

	t.Run("width exceeds 64", func(t *testing.T) {
		width, nExc, count := 65, 0, 8
		codesLen := (count*width + 7) / 8
		column := buildMain(width, nExc, codesLen, 0)
		entry := alpValidateTestEntry(10, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("exactly minimal valid main column", func(t *testing.T) {
		width, nExc, count := 4, 1, 8
		codesLen := (count*width + 7) / 8
		excLen := nExc * 12
		column := buildMain(width, nExc, codesLen, excLen)
		entry := alpValidateTestEntry(4, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.NoError(t, err)
	})
}

// TestValidateALPColumns_RD drives validateALPColumns directly with
// hand-built ALP-RD column payloads (scheme byte 1).
func TestValidateALPColumns_RD(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// buildRD constructs a scheme-1 column: [scheme:1][rbw:1][codeBits:1]
	// [nDict:1][nExc:4] + dictLen + leftLen + rightLen + excLen bytes.
	// Region contents are zero-filled; only the declared header fields and
	// overall byte length matter to validateALPColumns.
	buildRD := func(rbw, codeBits, nDict, nExc, dictLen, leftLen, rightLen, excLen int) []byte {
		body := make([]byte, 7+dictLen+leftLen+rightLen+excLen)
		body[0] = byte(rbw)
		body[1] = byte(codeBits)
		body[2] = byte(nDict)
		engine.PutUint32(body[3:7], uint32(nExc))

		return append([]byte{1}, body...)
	}

	t.Run("body shorter than 7 bytes", func(t *testing.T) {
		column := append([]byte{1}, make([]byte, 3)...) // body len 3 < 7
		entry := alpValidateTestEntry(1, 8, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("nDict exceeds max", func(t *testing.T) {
		column := buildRD(48, 2, 9, 0, 0, 0, 0, 0) // nDict = 9 > ALPRDMaxDictSize (8)
		entry := alpValidateTestEntry(2, 8, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("codeBits exceeds max (4)", func(t *testing.T) {
		// nDict is within bounds (<=8) and every region is sized exactly to
		// codeBits=4, so this column would satisfy every length check in
		// validateALPColumns — it must be rejected specifically by the
		// codeBits bound, not by a truncation check, since decodeRDInto's
		// dict is a fixed [8]uint64 array indexed by a codeBits-wide
		// unpacked code and codeBits=4 allows codes up to 15.
		rbw, codeBits, nDict, count := 48, 4, 2, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		column := buildRD(rbw, codeBits, nDict, 0, nDict*2, leftLen, rightLen, 0)
		entry := alpValidateTestEntry(8, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("codeBits = 255 does not bypass the check via shift overflow", func(t *testing.T) {
		// codeBits is stored as a single byte, so a corrupt column can set it
		// to 255. Region lengths are sized to match codeBits=255 (255 bytes
		// of "left codes"), so a length-based implementation of the codeBits
		// bound (comparing 1<<codeBits against ALPRDMaxDictSize) would wrap
		// around to 0 for a shift count >= 64 and wrongly accept this
		// column. The direct `codeBits > 3` comparison must reject it.
		rbw, codeBits, nDict, count := 48, 255, 2, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		column := buildRD(rbw, codeBits, nDict, 0, nDict*2, leftLen, rightLen, 0)
		entry := alpValidateTestEntry(9, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("dict region truncated", func(t *testing.T) {
		rbw, codeBits, nDict, count := 48, 2, 2, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		column := buildRD(rbw, codeBits, nDict, 0, nDict*2-1, leftLen, rightLen, 0) // dict short by 1
		entry := alpValidateTestEntry(3, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("left codes region truncated", func(t *testing.T) {
		rbw, codeBits, nDict, count := 48, 2, 2, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		column := buildRD(rbw, codeBits, nDict, 0, nDict*2, leftLen-1, rightLen, 0) // left short by 1
		entry := alpValidateTestEntry(4, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("right codes region truncated", func(t *testing.T) {
		rbw, codeBits, nDict, count := 48, 2, 2, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		column := buildRD(rbw, codeBits, nDict, 0, nDict*2, leftLen, rightLen-1, 0) // right short by 1, left intact
		entry := alpValidateTestEntry(7, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("exceptions region truncated", func(t *testing.T) {
		rbw, codeBits, nDict, nExc, count := 48, 2, 2, 1, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		excLen := nExc*6 - 1 // one byte short
		column := buildRD(rbw, codeBits, nDict, nExc, nDict*2, leftLen, rightLen, excLen)
		entry := alpValidateTestEntry(5, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("rbw outside the encodable range", func(t *testing.T) {
		// Dictionary entries are 2 bytes, so the left part is at most 16 bits
		// and the right part (rbw) is 48..63 bits for every valid column.
		for _, rbw := range []int{0, 4, 47, 64, 255} {
			codeBits, nDict, count := 2, 2, 8
			leftLen := (count*codeBits + 7) / 8
			rightLen := (count*rbw + 7) / 8
			column := buildRD(rbw, codeBits, nDict, 0, nDict*2, leftLen, rightLen, 0)
			entry := alpValidateTestEntry(11, count, len(column))
			err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
			require.ErrorIsf(t, err, errs.ErrInvalidALPColumn, "rbw=%d", rbw)
		}
	})

	t.Run("exactly minimal valid rd column", func(t *testing.T) {
		rbw, codeBits, nDict, nExc, count := 48, 2, 2, 1, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		excLen := nExc * 6
		column := buildRD(rbw, codeBits, nDict, nExc, nDict*2, leftLen, rightLen, excLen)
		entry := alpValidateTestEntry(6, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.NoError(t, err)
	})

	t.Run("codeBits at max boundary (3) is not falsely rejected", func(t *testing.T) {
		// codeBits=3 is the largest value a valid encoder can ever emit
		// (alpCodeBits(nDict) for nDict <= ALPRDMaxDictSize tops out at
		// bits.Len64(7) = 3), so the codeBits bound must accept it.
		rbw, codeBits, nDict, nExc, count := 48, 3, 2, 1, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		excLen := nExc * 6
		column := buildRD(rbw, codeBits, nDict, nExc, nDict*2, leftLen, rightLen, excLen)
		entry := alpValidateTestEntry(10, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.NoError(t, err)
	})
}

// TestValidateALPColumns_Raw drives validateALPColumns directly with
// hand-built ALP-raw column payloads (scheme byte 2).
func TestValidateALPColumns_Raw(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	t.Run("body shorter than count*8 bytes", func(t *testing.T) {
		count := 5
		column := append([]byte{2}, make([]byte, count*8-1)...) // one byte short
		entry := alpValidateTestEntry(1, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("exactly minimal valid raw column", func(t *testing.T) {
		count := 5
		column := append([]byte{2}, make([]byte, count*8)...)
		entry := alpValidateTestEntry(2, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.NoError(t, err)
	})
}

// TestValidateALPColumns_ColumnTooLargeForPlatform pins that a column whose
// packed bit count cannot be addressed with the platform int is rejected; the
// limit is lowered here to emulate a 32-bit platform.
func TestValidateALPColumns_ColumnTooLargeForPlatform(t *testing.T) {
	engine := endian.GetLittleEndianEngine()
	saved := maxALPColumnBits
	maxALPColumnBits = 1 << 10
	t.Cleanup(func() { maxALPColumnBits = saved })

	const count, width = 64, 32 // 2048 bits > 1024
	body := make([]byte, 15+(count*width+7)/8)
	body[2] = width
	column := append([]byte{0}, body...)
	entry := alpValidateTestEntry(1, count, len(column))
	require.ErrorIs(t, validateALPColumns(column, []section.NumericIndexEntry{entry}, engine), errs.ErrInvalidALPColumn)

	const small = 16 // 512 bits fits
	body = make([]byte, 15+(small*width+7)/8)
	body[2] = width
	column = append([]byte{0}, body...)
	entry = alpValidateTestEntry(2, small, len(column))
	require.NoError(t, validateALPColumns(column, []section.NumericIndexEntry{entry}, engine))

	// A bit count within 7 of the limit would overflow int
	// when rounded up to bytes, so it is rejected too.
	const nearCount, nearWidth = 34, 30 // 1020 bits; 1020+7 > 1024
	body = make([]byte, 15+(nearCount*nearWidth+7)/8)
	body[2] = nearWidth
	column = append([]byte{0}, body...)
	entry = alpValidateTestEntry(3, nearCount, len(column))
	require.ErrorIs(t, validateALPColumns(column, []section.NumericIndexEntry{entry}, engine), errs.ErrInvalidALPColumn,
		"main column")

	const rdCount, rdWidth = 20, 51 // 1020 bits; codeBits 0, nDict 0
	body = make([]byte, 7+(rdCount*rdWidth+7)/8)
	body[0] = rdWidth
	column = append([]byte{1}, body...)
	entry = alpValidateTestEntry(4, rdCount, len(column))
	require.ErrorIs(t, validateALPColumns(column, []section.NumericIndexEntry{entry}, engine), errs.ErrInvalidALPColumn,
		"rd column")
}

// TestValidateALPColumns_ExceptionCountAboveInt32 pins that an exception count
// of 2^31 or more is rejected by the length check.
// On 32-bit platforms such a count is negative as an int,
// so it must stay unsigned until the length check bounds it.
func TestValidateALPColumns_ExceptionCountAboveInt32(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	// Complete one-point columns: main is the 15-byte header with width 0;
	// RD is its 7-byte header plus 7 bytes of 51-bit right parts.
	// With nExc read as a negative int, 0xFFFFFFFF would shrink the required
	// length below these bodies and pass.
	mainColumn := func(nExc uint32) []byte {
		body := make([]byte, 15)
		engine.PutUint32(body[3:7], nExc)

		return append([]byte{0}, body...)
	}
	rdColumn := func(nExc uint32) []byte {
		body := make([]byte, 7+(51+7)/8)
		body[0] = 51 // right width
		engine.PutUint32(body[3:7], nExc)

		return append([]byte{1}, body...)
	}

	for name, column := range map[string]func(uint32) []byte{"main": mainColumn, "rd": rdColumn} {
		valid := column(0)
		entry := alpValidateTestEntry(1, 1, len(valid))
		require.NoErrorf(t, validateALPColumns(valid, []section.NumericIndexEntry{entry}, engine),
			"%s column without exceptions", name)

		for _, nExc := range []uint32{0x80000000, 0xFFFFFFFF} {
			corrupt := column(nExc)
			require.ErrorIsf(t, validateALPColumns(corrupt, []section.NumericIndexEntry{entry}, engine),
				errs.ErrInvalidALPColumn, "%s column, nExc=%#x", name, nExc)
		}
	}
}

// TestValidateALPColumns_ExceptionPositions pins that exception positions must be
// strictly ascending and below the point count, for main and RD columns.
func TestValidateALPColumns_ExceptionPositions(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	mainColumn := func(positions ...uint32) []byte {
		body := make([]byte, 15)
		engine.PutUint32(body[3:7], uint32(len(positions)))
		for _, p := range positions {
			body = engine.AppendUint32(body, p)
			body = engine.AppendUint64(body, 0)
		}

		return append([]byte{0}, body...) // width 0: no code bytes for count points
	}
	rdColumn := func(positions ...uint32) []byte {
		const points, rbw, codeBits, nDict = 4, 48, 1, 2
		body := make([]byte, 7+nDict*2+(points*codeBits+7)/8+(points*rbw+7)/8)
		body[0], body[1], body[2] = rbw, codeBits, nDict
		engine.PutUint32(body[3:7], uint32(len(positions)))
		for _, p := range positions {
			body = engine.AppendUint32(body, p)
			body = engine.AppendUint16(body, 0)
		}

		return append([]byte{1}, body...)
	}

	for name, build := range map[string]func(...uint32) []byte{"main": mainColumn, "rd": rdColumn} {
		t.Run(name, func(t *testing.T) {
			check := func(positions ...uint32) error {
				column := build(positions...)
				entry := alpValidateTestEntry(1, 4, len(column))

				return validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
			}

			require.NoError(t, check(0, 2, 3))
			require.ErrorIs(t, check(1, 0), errs.ErrInvalidALPColumn, "descending")
			require.ErrorIs(t, check(2, 2), errs.ErrInvalidALPColumn, "duplicate")
			require.ErrorIs(t, check(4), errs.ErrInvalidALPColumn, "position == count")
		})
	}
}
