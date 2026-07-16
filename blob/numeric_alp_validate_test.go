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
		column := buildRD(4, 2, 9, 0, 0, 0, 0, 0) // nDict = 9 > ALPRDMaxDictSize (8)
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
		rbw, codeBits, nDict, count := 4, 4, 2, 8
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
		rbw, codeBits, nDict, count := 4, 255, 2, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		column := buildRD(rbw, codeBits, nDict, 0, nDict*2, leftLen, rightLen, 0)
		entry := alpValidateTestEntry(9, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("dict region truncated", func(t *testing.T) {
		rbw, codeBits, nDict, count := 4, 2, 2, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		column := buildRD(rbw, codeBits, nDict, 0, nDict*2-1, leftLen, rightLen, 0) // dict short by 1
		entry := alpValidateTestEntry(3, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("left codes region truncated", func(t *testing.T) {
		rbw, codeBits, nDict, count := 4, 2, 2, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		column := buildRD(rbw, codeBits, nDict, 0, nDict*2, leftLen-1, rightLen, 0) // left short by 1
		entry := alpValidateTestEntry(4, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("right codes region truncated", func(t *testing.T) {
		rbw, codeBits, nDict, count := 4, 2, 2, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		column := buildRD(rbw, codeBits, nDict, 0, nDict*2, leftLen, rightLen-1, 0) // right short by 1, left intact
		entry := alpValidateTestEntry(7, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("exceptions region truncated", func(t *testing.T) {
		rbw, codeBits, nDict, nExc, count := 4, 2, 2, 1, 8
		leftLen := (count*codeBits + 7) / 8
		rightLen := (count*rbw + 7) / 8
		excLen := nExc*6 - 1 // one byte short
		column := buildRD(rbw, codeBits, nDict, nExc, nDict*2, leftLen, rightLen, excLen)
		entry := alpValidateTestEntry(5, count, len(column))
		err := validateALPColumns(column, []section.NumericIndexEntry{entry}, engine)
		require.Error(t, err)
		require.ErrorIs(t, err, errs.ErrInvalidALPColumn)
	})

	t.Run("exactly minimal valid rd column", func(t *testing.T) {
		rbw, codeBits, nDict, nExc, count := 4, 2, 2, 1, 8
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
		rbw, codeBits, nDict, nExc, count := 4, 3, 2, 1, 8
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
