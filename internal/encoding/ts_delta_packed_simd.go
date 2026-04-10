package encoding

import "github.com/arloliu/mebo/internal/arch"

const (
	deltaPackedDecodeSIMDMinLenAVX2     = 64
	deltaPackedDecodeSIMDScratchSize    = 256
	deltaPackedDecodeSIMDSafeLoadWindow = 32 // minimum safe readable bytes for a VPSHUFB load
)

// deltaPackedDecodeBackend selects the decode implementation for Group Varint packed timestamps.
type deltaPackedDecodeBackend uint8

const (
	deltaPackedDecodeBackendScalar deltaPackedDecodeBackend = iota
	deltaPackedDecodeBackendAsmAVX2
)

// deltaPackedDecodeMeta holds precomputed metadata for one Group Varint control byte.
// Built once at package init time via initDeltaPackedDecodeTable.
type deltaPackedDecodeMeta struct {
	// totalBytes is the sum of byte widths for the four values encoded by this control byte.
	totalBytes uint8
	// lengths[i] is the byte width of value i (1, 2, 4, or 8).
	lengths [groupSize]uint8
	// offsets[i] is the byte offset of value i within the payload (after the control byte).
	offsets [groupSize]uint8
	// shuffle is the 32-byte VPSHUFB mask that expands the group payload into four
	// zero-extended little-endian 64-bit lanes laid out as two 128-bit halves:
	//   low 16 bytes  → lane 0 (bytes 0-7) and lane 1 (bytes 8-15)
	//   high 16 bytes → lane 2 (bytes 0-7) and lane 3 (bytes 8-15)
	// Unused payload positions are filled with 0x80 (VPSHUFB zeroing sentinel).
	shuffle [32]byte
}

// deltaPackedDecodeTable holds the 256-entry metadata table, indexed by control byte.
var deltaPackedDecodeTable [256]deltaPackedDecodeMeta

// deltaPackedDecodeShuffles is a flat 256×32 byte array of VPSHUFB masks, indexed by
// control byte. Stride is exactly 32 bytes, so the ASM can address entry[cb] as
// table + cb*32 without a multiply (shift by 5 is sufficient).
var deltaPackedDecodeShuffles [256 * 32]byte

// deltaPackedDecodeShufflesLoDup and deltaPackedDecodeShufflesHiDup are flat
// 256×32 byte arrays of VPSHUFB masks for duplicated 128-bit halves. For each
// control byte, the low mask extracts bytes sourced from payload offsets 0..15
// after duplicating that 16-byte half into both 128-bit lanes; the high mask does
// the same for payload offsets 16..31 after subtracting 16.
var (
	deltaPackedDecodeShufflesLoDup [256 * 32]byte
	deltaPackedDecodeShufflesHiDup [256 * 32]byte
)

// deltaPackedDecodeTotalBytes holds the total payload byte count for each control byte.
// Stride is 1 byte, value range 4-32.
var deltaPackedDecodeTotalBytes [256]uint8

func init() {
	initDeltaPackedDecodeTable()
}

func initDeltaPackedDecodeTable() {
	for cb := range 256 {
		var meta deltaPackedDecodeMeta

		// Compute per-lane widths and offsets from the 2-bit tags in the control byte.
		var cur uint8
		for lane := range groupSize {
			tag := (uint8(cb) >> (uint(lane) * 2)) & 0x03 //nolint:gosec // lane is 0-3
			width := uint8(groupVarintLengths[tag])       //nolint:gosec // tag is 0-3, values are 1,2,4,8
			meta.lengths[lane] = width
			meta.offsets[lane] = cur
			cur += width
		}
		meta.totalBytes = cur

		// Build shuffle mask: for each lane, position the payload bytes at the correct
		// destination slots. Unused byte slots within a lane (b >= width) get 0x80
		// (VPSHUFB zeroing sentinel). The layout is: lane 0 at bytes 0-7, lane 1 at
		// bytes 8-15, lane 2 at bytes 16-23, lane 3 at bytes 24-31.
		// NOTE: Phase 1 ASM uses scalar offsets/lengths directly and does not issue
		// VPSHUFB, but the table is fully populated for correctness and Phase 2 use.
		for i := range 32 {
			meta.shuffle[i] = 0x80
		}
		for lane := range groupSize {
			dstBase := lane * 8
			offset := int(meta.offsets[lane])
			width := int(meta.lengths[lane])
			for b := range 8 {
				if b < width {
					meta.shuffle[dstBase+b] = uint8(offset + b) //nolint:gosec // offset+b < 32
				}
			}
		}

		// Store into main table and fill flat tables used by assembly.
		deltaPackedDecodeTable[cb] = meta
		deltaPackedDecodeTotalBytes[cb] = meta.totalBytes
		copy(deltaPackedDecodeShuffles[cb*32:(cb+1)*32], meta.shuffle[:])

		for i := range 32 {
			deltaPackedDecodeShufflesLoDup[cb*32+i] = 0x80
			deltaPackedDecodeShufflesHiDup[cb*32+i] = 0x80
		}

		for lane := range groupSize {
			dstBase := lane * 8
			offset := int(meta.offsets[lane])
			width := int(meta.lengths[lane])

			for b := range width {
				src := offset + b
				if src < 16 {
					deltaPackedDecodeShufflesLoDup[cb*32+dstBase+b] = uint8(src) //nolint:gosec // src is 0..15 here

					continue
				}

				deltaPackedDecodeShufflesHiDup[cb*32+dstBase+b] = uint8(src - 16) //nolint:gosec // src is 16..31 here
			}
		}
	}
}

var activeDeltaPackedDecodeBackend deltaPackedDecodeBackend = deltaPackedDecodeBackendScalar

type deltaPackedDecodeProgress struct {
	consumed  int
	produced  int
	lastTS    int64
	lastDelta int64
}

func init() {
	setActiveDeltaPackedDecodeBackend(defaultDeltaPackedDecodeBackend())
}

func defaultDeltaPackedDecodeBackend() deltaPackedDecodeBackend {
	if arch.X86HasAVX2() {
		return deltaPackedDecodeBackendAsmAVX2
	}

	return deltaPackedDecodeBackendScalar
}

func setActiveDeltaPackedDecodeBackend(backend deltaPackedDecodeBackend) {
	if backend == deltaPackedDecodeBackendAsmAVX2 && arch.X86HasAVX2() {
		activeDeltaPackedDecodeBackend = deltaPackedDecodeBackendAsmAVX2

		return
	}

	activeDeltaPackedDecodeBackend = deltaPackedDecodeBackendScalar
}

func setDeltaPackedDecodeBackendForTest(backend deltaPackedDecodeBackend) func() {
	prev := activeDeltaPackedDecodeBackend
	setActiveDeltaPackedDecodeBackend(backend)

	return func() {
		activeDeltaPackedDecodeBackend = prev
	}
}

func deltaPackedDecodeBackendName(backend deltaPackedDecodeBackend) string {
	switch backend {
	case deltaPackedDecodeBackendScalar:
		return "Scalar"
	case deltaPackedDecodeBackendAsmAVX2:
		return "AsmAVX2"
	default:
		return "Unknown"
	}
}

func deltaPackedDecodeBackendSupported(backend deltaPackedDecodeBackend) bool {
	switch backend {
	case deltaPackedDecodeBackendScalar:
		return true
	case deltaPackedDecodeBackendAsmAVX2:
		return arch.X86HasAVX2()
	default:
		return false
	}
}

var allDeltaPackedDecodeBackends = [...]deltaPackedDecodeBackend{
	deltaPackedDecodeBackendScalar,
	deltaPackedDecodeBackendAsmAVX2,
}

func shouldUseDeltaPackedDecodeSIMD(remainingValues int) bool {
	if activeDeltaPackedDecodeBackend == deltaPackedDecodeBackendAsmAVX2 {
		return remainingValues >= deltaPackedDecodeSIMDMinLenAVX2
	}

	return false
}

// decodeDeltaPackedIntoActive decodes as many full Group Varint groups as possible
// from data into dst, using the active backend.
//
// Parameters:
//   - dst: destination slice (len must accommodate groupCount values)
//   - data: encoded byte slice starting at the first control byte to decode
//   - groupCount: maximum number of full 4-value groups to decode
//   - prevTS: carry timestamp from the previous decoded value
//   - prevDelta: carry delta from the previous decoded value
//
// Returns:
//   - consumed: bytes consumed from data
//   - produced: number of timestamps written to dst
//   - lastTS: carry timestamp after the last decoded value
//   - lastDelta: carry delta after the last decoded value
//   - ok: false if data was malformed or insufficient
func decodeDeltaPackedIntoActive(
	dst []int64,
	data []byte,
	groupCount int,
	prevTS int64,
	prevDelta int64,
) (deltaPackedDecodeProgress, bool) {
	if activeDeltaPackedDecodeBackend == deltaPackedDecodeBackendAsmAVX2 {
		return decodeDeltaPackedASMAVX2(dst, data, groupCount, prevTS, prevDelta)
	}

	return decodeDeltaPackedScalarBulk(dst, data, groupCount, prevTS, prevDelta)
}

// decodeDeltaPackedScalarBulk is the scalar bulk helper used as:
//   - the reference implementation for SIMD parity tests
//   - the backend when SIMD is not available
//
// It decodes full Group Varint groups (4 values each) from data into dst.
func decodeDeltaPackedScalarBulk(
	dst []int64,
	data []byte,
	groupCount int,
	prevTS int64,
	prevDelta int64,
) (deltaPackedDecodeProgress, bool) {
	maxValues := groupCount &^ (groupSize - 1) // round down to multiple of groupSize
	if maxValues <= 0 {
		return deltaPackedDecodeProgress{lastTS: prevTS, lastDelta: prevDelta}, true
	}

	result := deltaPackedDecodeProgress{lastTS: prevTS, lastDelta: prevDelta}
	offset := 0

	for result.produced+groupSize <= maxValues && offset < len(data) {
		meta := &deltaPackedDecodeTable[data[offset]]
		offset++

		payloadEnd := offset + int(meta.totalBytes)
		if payloadEnd > len(data) {
			return result, false
		}

		for lane := range groupSize {
			payloadOff := offset + int(meta.offsets[lane])
			width := meta.lengths[lane]

			var zz uint64
			switch width {
			case 1:
				zz = uint64(data[payloadOff])
			case 2:
				zz = uint64(data[payloadOff]) | uint64(data[payloadOff+1])<<8
			case 4:
				zz = uint64(data[payloadOff]) |
					uint64(data[payloadOff+1])<<8 |
					uint64(data[payloadOff+2])<<16 |
					uint64(data[payloadOff+3])<<24
			case 8:
				zz = uint64(data[payloadOff]) |
					uint64(data[payloadOff+1])<<8 |
					uint64(data[payloadOff+2])<<16 |
					uint64(data[payloadOff+3])<<24 |
					uint64(data[payloadOff+4])<<32 |
					uint64(data[payloadOff+5])<<40 |
					uint64(data[payloadOff+6])<<48 |
					uint64(data[payloadOff+7])<<56
			default:
				return result, false
			}

			deltaOfDelta := decodeZigZag64(zz)
			result.lastDelta += deltaOfDelta
			result.lastTS += result.lastDelta
			dst[result.produced] = result.lastTS
			result.produced++
		}

		offset = payloadEnd
		result.consumed = offset
	}

	return result, true
}
