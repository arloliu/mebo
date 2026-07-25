package blob

import (
	"bytes"
	"slices"
	"unsafe"

	"github.com/cespare/xxhash/v2"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/internal/pool"
	"github.com/arloliu/mebo/section"
)

// StripMetricNames returns a copy of src, appended to dst, with the metric-names
// payload removed — when, and only when, the names are not load-bearing.
//
// The metric-names payload is an optional section between the header and the
// index that stores the original name string for every metric (see
// WithMetricNames / the text encoder's default). It exists to distinguish two
// distinct names that hash to the same 64-bit MetricID (a collision). When no
// collision is present the payload is pure redundancy: every stored name is
// still recoverable from its hash, so it can be dropped, shrinking the blob by
// exactly the payload size. StripMetricNames performs that drop.
//
// Aliasing: if dst and src share a backing array (for example
// StripMetricNames(src[:0], src)), a fresh buffer is allocated and seeded with
// dst's existing bytes before src's contribution is appended, so the call is
// never a silent in-place mutation of src and dst's existing prefix is never
// dropped. Use StripMetricNamesInPlace for the deliberate in-place form.
//
// # Validation boundary
//
// A successful strip (stripped == true) means "the names were removed safely",
// NOT "src is a valid blob". Strip validates exactly what is required to
// guarantee that (a) removing names cannot make an invalid blob look valid, and
// (b) a valid blob stays valid. It checks: header size, magic, flags,
// encoding/compression validity; that every absolute section offset is ordered
// and within len(src); that the index fits before the first payload; the names
// extent, count, and per-name length bounds; that every stored name hashes to
// its entry's MetricID; and duplicate names, duplicate IDs, and V2/V2Ext index
// order. Everything else — per-entry offset deltas, V2Ext reserved bytes, the
// shared-timestamp table, payload contents — is copied byte-for-byte and still
// fails at decode exactly as it did before stripping. err therefore means
// "malformed within that boundary", never a whole-blob validity verdict.
//
// # Membership cost
//
// After stripping, the blob has ordinary no-names semantics, which loses two
// things relative to a names-bearing blob:
//
//  1. Enumeration: MetricNames() returns an empty slice — names cannot be
//     recovered from hashes. Consumers that enumerate need a side dictionary.
//  2. Exact negative membership: GetByName, HasMetricName, and every *ByName
//     iterator still resolve every stored name, but they now answer by hash.ID
//     rather than exact string match. A query string that hash-collides with a
//     stored name will false-positive (resolve the stored metric). This is
//     intrinsic — the names are gone — and is exactly how every no-collision
//     blob already answers ByName. Callers needing collision-proof membership
//     must keep the names (do not strip) or use ID-based access.
//
// Parameters:
//   - dst: destination buffer the result is appended to. May be nil. If dst
//     shares a backing array with src, a fresh buffer is allocated instead of
//     appending in place (see Aliasing above); dst's own bytes are still
//     copied in first so the dst ++ result contract holds either way.
//   - src: the blob to strip. Never modified, on any outcome.
//
// Returns:
//   - stripped == true:  out is a fresh blob (dst ++ names-free blob) that is
//     byte-for-byte identical to what the encoder would have produced without
//     the names payload. src is not modified.
//   - stripped == false, err == nil:  the names are load-bearing (a real
//     collision — two distinct names share one MetricID) or already absent. The
//     blob is returned unchanged (dst ++ src) and src is not modified. This is a
//     normal outcome, not an error: keep using the blob as-is.
//   - err != nil:  src is malformed within strip's enumerated validation
//     boundary (see above). out is dst, returned unchanged — nothing is
//     appended, dst's existing bytes are untouched, and src is untouched. This
//     keeps `dst, _, err = StripMetricNames(dst, src)` safe: a failed strip
//     never discards the caller's buffer.
func StripMetricNames(dst, src []byte) (out []byte, stripped bool, err error) {
	info, doStrip, err := validateStrip(src)
	if err != nil {
		return dst, false, err
	}

	if !doStrip {
		// No-op or refusal: hand back dst ++ src unchanged. When dst aliases src
		// we must allocate a fresh buffer rather than append into src's backing
		// array, but dst's existing bytes are still the required prefix.
		if slicesOverlap(dst, src) {
			cp := make([]byte, 0, len(dst)+len(src))
			cp = append(cp, dst...)
			cp = append(cp, src...)

			return cp, false, nil
		}

		return append(dst, src...), false, nil
	}

	newHeader := info.rewriteHeader()
	tail := src[info.indexOffset:]
	need := len(newHeader) + len(tail)

	// Allocate a fresh buffer when dst aliases src so we never clobber src
	// while reading its tail. dst's existing bytes are copied in first so the
	// documented dst ++ blob contract holds even on the aliased path.
	if slicesOverlap(dst, src) {
		out = make([]byte, 0, len(dst)+need)
		out = append(out, dst...)
	} else {
		out = dst
	}
	out = append(out, newHeader...)
	out = append(out, tail...)

	return out, true, nil
}

// StripMetricNamesInPlace removes the metric-names payload from buf in place,
// reslicing buf down and returning the shortened blob.
//
// It takes EXCLUSIVE OWNERSHIP of buf: on a successful strip the bytes of buf
// are overwritten (the tail is shifted up over the removed payload and the
// header is rewritten), so the caller must not retain or reuse the original buf
// contents afterwards. Use StripMetricNames when src must stay intact.
//
// # Validation boundary
//
// A successful strip (stripped == true) means "the names were removed safely",
// NOT "buf is a valid blob" — the same enumerated boundary StripMetricNames
// checks (see its documentation for the full list: header size, magic, flags,
// and encoding/compression validity; ordered, in-bounds section offsets; the
// index fitting before the first payload; the names extent, count, and
// per-name length bounds; every stored name hashing to its entry's MetricID;
// and duplicate names, duplicate IDs, and V2/V2Ext index order). Everything
// else is copied byte-for-byte and still fails at decode exactly as it did
// before stripping.
//
// # Membership cost
//
// After stripping, buf has ordinary no-names semantics, which loses two things
// relative to a names-bearing blob: (1) enumeration — MetricNames() returns an
// empty slice, so names can no longer be recovered from hashes; and (2) exact
// negative membership — GetByName, HasMetricName, and every *ByName iterator
// still resolve every stored name, but now by hash.ID rather than exact string
// match, so a query string that hash-collides with a stored name will
// false-positive (resolve the stored metric). This is intrinsic (the names are
// gone) and matches ordinary no-collision-blob behaviour. Callers needing
// collision-proof membership must keep the names (do not strip) or use
// ID-based access.
//
// Parameters:
//   - buf: the blob to strip in place. Ownership transfers to the callee: on a
//     successful strip its bytes are overwritten, and only the returned,
//     resliced value remains valid to use afterwards.
//
// Returns:
//   - stripped == true:  out is buf resliced to the names-free blob, byte-for-byte
//     what the encoder would have produced without names.
//   - stripped == false, err == nil:  names are load-bearing (collision) or
//     absent; buf is returned unchanged.
//   - err != nil:  buf is malformed within strip's enumerated validation
//     boundary (see above). out is buf, returned unchanged — its bytes are not
//     modified and nothing is resliced away.
func StripMetricNamesInPlace(buf []byte) (out []byte, stripped bool, err error) {
	info, doStrip, err := validateStrip(buf)
	if err != nil {
		return buf, false, err
	}

	if !doStrip {
		return buf, false, nil
	}

	newHeader := info.rewriteHeader()
	tailLen := len(buf) - info.indexOffset

	// Compact the tail up over the removed names payload. copy uses memmove, and
	// the destination (HeaderSize) is strictly below the source (indexOffset),
	// so the forward move is safe. The header region [0:HeaderSize) is disjoint
	// from the tail destination [HeaderSize:...), so header rewrite order is free.
	copy(buf[section.HeaderSize:], buf[info.indexOffset:])
	copy(buf[:section.HeaderSize], newHeader)

	return buf[:section.HeaderSize+tailLen], true, nil
}

// stripInfo carries the validated header and offsets needed to emit a stripped
// blob. Header values are held inline (not via interface) so validateStrip stays
// allocation-free on the success path.
type stripInfo struct {
	numHeader   section.NumericHeader
	txtHeader   section.TextHeader
	isText      bool
	indexOffset int // absolute offset of the index section (tail start)
	namesSize   int // bytes to remove: indexOffset - HeaderSize
}

// rewriteHeader returns a freshly serialized 32-byte header with the names flag
// cleared and every absolute offset reduced by the removed names size. The
// byte order is selected from the flag, so big-endian blobs stay correct.
func (info stripInfo) rewriteHeader() []byte {
	ns := uint32(info.namesSize) //nolint:gosec // namesSize == indexOffset-HeaderSize, bounded by len(src) <= maxInt.

	if info.isText {
		h := info.txtHeader
		h.Flag.SetHasMetricNames(false)
		h.IndexOffset -= ns
		h.DataOffset -= ns
		// DataSize is an uncompressed-size field, not an absolute offset: untouched.
		return h.Bytes()
	}

	h := info.numHeader
	h.Flag.SetHasMetricNames(false)
	h.IndexOffset -= ns
	h.TimestampPayloadOffset -= ns
	h.ValuePayloadOffset -= ns
	h.TagPayloadOffset -= ns

	return h.Bytes()
}

// validateStrip performs strip algorithm steps 1-7 with zero allocations
// on the success path (pooled scratch is reused warm). It never mutates src.
//
// Returns:
//   - doStrip == true:  info describes how to emit; the blob is safe to strip.
//   - doStrip == false, err == nil:  no-op (no names flag) or refusal (real
//     collision — names are load-bearing).
//   - err != nil:  src is malformed within the enumerated boundary.
func validateStrip(src []byte) (info stripInfo, doStrip bool, err error) {
	// Step 1: header must be present.
	if len(src) < section.HeaderSize {
		return info, false, errs.ErrInvalidHeaderSize
	}

	// Step 2: dispatch on the magic number. The options/magic word is always
	// little-endian; every other header field uses the flag's endian engine.
	magic := (uint16(src[0]) | uint16(src[1])<<8) & section.MagicNumberMask

	switch magic {
	case section.MagicNumericV1Opt, section.MagicNumericV2Opt, section.MagicNumericV2ExtOpt:
		return validateStripNumeric(src)
	case section.MagicTextV1Opt:
		return validateStripText(src)
	default:
		return info, false, errs.ErrInvalidMagicNumber
	}
}

// validateStripNumeric handles steps 3-7 for numeric blobs.
func validateStripNumeric(src []byte) (info stripInfo, doStrip bool, err error) {
	// Step 3: parse + validate magic, encoding, compression.
	header, err := section.ParseNumericHeader(src)
	if err != nil {
		return info, false, err
	}

	// Step 4: no names flag -> nothing to strip.
	if !header.Flag.HasMetricNames() {
		return info, false, nil
	}

	engine := header.Flag.GetEndianEngine()
	srcLen := uint64(len(src))

	// Step 5: checked offset ordering and counts (all comparisons in uint64 to
	// avoid any int conversion overflow on 32-bit platforms).
	indexOff := uint64(header.IndexOffset)
	tsOff := uint64(header.TimestampPayloadOffset)
	valOff := uint64(header.ValuePayloadOffset)
	tagOff := uint64(header.TagPayloadOffset)

	// HeaderSize+2 <= IndexOffset: the names extent must hold at least the count.
	if indexOff < section.HeaderSize+2 {
		return info, false, errs.ErrMetricNamesExtentMismatch
	}
	// IndexOffset <= TS <= Val <= Tag <= len(src).
	if indexOff > tsOff || tsOff > valOff || valOff > tagOff || tagOff > srcLen {
		return info, false, errs.ErrInvalidIndexOffsets
	}

	metricCount := uint64(header.MetricCount)
	if metricCount > MaxMetricNamesCount {
		return info, false, errs.ErrInvalidMetricCount
	}

	entrySize := header.Flag.IndexEntrySize()
	// MetricCount * entrySize must fit between IndexOffset and the first payload.
	if metricCount*uint64(entrySize) > tsOff-indexOff { //nolint:gosec // entrySize is 16 or 32.
		return info, false, errs.ErrInvalidIndexEntrySize
	}

	indexOffset := int(indexOff) //nolint:gosec // indexOff validated <= len(src) <= maxInt.
	count := int(metricCount)

	// Step 6: verify the names payload (extent, count, per-name bounds, hashes).
	spansPtr, err := verifyMetricNamesExtent(src, indexOffset, entrySize, count, engine)
	if err != nil {
		return info, false, err
	}
	if spansPtr != nil {
		defer pool.PutUint64Slice(spansPtr)
	}

	// Step 7: collision / order scan.
	doStrip, err = scanIndexForCollision(src, indexOffset, entrySize, count, header.Flag.IsV2(), engine, derefSpans(spansPtr))
	if err != nil {
		return info, false, err
	}
	if !doStrip {
		return info, false, nil
	}

	info = stripInfo{
		numHeader:   header,
		isText:      false,
		indexOffset: indexOffset,
		namesSize:   indexOffset - section.HeaderSize,
	}

	return info, true, nil
}

// validateStripText handles steps 3-7 for text blobs.
func validateStripText(src []byte) (info stripInfo, doStrip bool, err error) {
	header, err := section.ParseTextHeader(src)
	if err != nil {
		return info, false, err
	}

	if !header.Flag.HasMetricNames() {
		return info, false, nil
	}

	engine := header.GetEndianEngine()
	srcLen := uint64(len(src))

	indexOff := uint64(header.IndexOffset)
	dataOff := uint64(header.DataOffset)

	if indexOff < section.HeaderSize+2 {
		return info, false, errs.ErrMetricNamesExtentMismatch
	}
	// IndexOffset <= DataOffset <= len(src).
	if indexOff > dataOff || dataOff > srcLen {
		return info, false, errs.ErrInvalidIndexOffsets
	}

	metricCount := uint64(header.MetricCount)
	if metricCount > MaxMetricNamesCount {
		return info, false, errs.ErrInvalidMetricCount
	}

	const entrySize = section.TextIndexEntrySize
	if metricCount*uint64(entrySize) > dataOff-indexOff {
		return info, false, errs.ErrInvalidIndexEntrySize
	}

	indexOffset := int(indexOff) //nolint:gosec // indexOff validated <= len(src) <= maxInt.
	count := int(metricCount)

	spansPtr, err := verifyMetricNamesExtent(src, indexOffset, entrySize, count, engine)
	if err != nil {
		return info, false, err
	}
	if spansPtr != nil {
		defer pool.PutUint64Slice(spansPtr)
	}

	// Text index is never ID-sorted: use the general (V1) duplicate scan.
	doStrip, err = scanIndexForCollision(src, indexOffset, entrySize, count, false, engine, derefSpans(spansPtr))
	if err != nil {
		return info, false, err
	}
	if !doStrip {
		return info, false, nil
	}

	info = stripInfo{
		txtHeader:   header,
		isText:      true,
		indexOffset: indexOffset,
		namesSize:   indexOffset - section.HeaderSize,
	}

	return info, true, nil
}

// derefSpans returns the slice behind a pooled *[]uint64, or nil.
func derefSpans(ptr *[]uint64) []uint64 {
	if ptr == nil {
		return nil
	}

	return *ptr
}

// verifyMetricNamesExtent walks the names payload (src[HeaderSize:indexOffset])
// zero-copy (step 6): it checks the count matches, every length prefix stays
// inside the declared extent, the consumed length equals the extent exactly, and
// every name hashes to its aligned index entry's MetricID.
//
// It returns a pooled *[]uint64 of packed name spans (start<<32 | len), one per
// entry, in index order, for the duplicate-name check in step 7. The caller owns
// the returned pointer and must return it via pool.PutUint64Slice. When
// count == 0 the returned pointer is nil. On error the pool slice is returned
// before returning.
func verifyMetricNamesExtent(src []byte, indexOffset, entrySize, count int, engine endian.EndianEngine) (*[]uint64, error) {
	extent := src[section.HeaderSize:indexOffset]

	// len(extent) >= 2 (the count field). Guaranteed by the step-5 HeaderSize+2
	// check, but re-assert defensively.
	if len(extent) < 2 {
		return nil, errs.ErrMetricNamesExtentMismatch
	}

	declared := int(engine.Uint16(extent[0:2]))
	if declared != count {
		return nil, errs.ErrMetricNamesExtentMismatch
	}

	var spansPtr *[]uint64
	var spans []uint64
	if count > 0 {
		spansPtr = pool.GetUint64Slice(count)
		spans = *spansPtr
	}

	pos := 2 // byte cursor within extent
	for i := 0; i < count; i++ {
		// Length prefix must fit.
		if pos+2 > len(extent) {
			pool.PutUint64Slice(spansPtr)
			return nil, errs.ErrMetricNamesExtentMismatch
		}
		nameLen := int(engine.Uint16(extent[pos : pos+2]))
		pos += 2

		// Name bytes must fit.
		if pos+nameLen > len(extent) {
			pool.PutUint64Slice(spansPtr)
			return nil, errs.ErrMetricNamesExtentMismatch
		}
		nameBytes := extent[pos : pos+nameLen]

		// Re-hash against the aligned entry ID. xxhash.Sum64 over the bytes is
		// identical to hash.ID(string(name)) but avoids the string allocation,
		// keeping this walk zero-alloc.
		entryID := readEntryMetricID(src, indexOffset, entrySize, i, engine)
		if xxhash.Sum64(nameBytes) != entryID {
			pool.PutUint64Slice(spansPtr)
			return nil, errs.ErrHashMismatch
		}

		// Record the name span for the duplicate-name check (step 7). start is
		// the absolute offset of the name bytes in src.
		start := section.HeaderSize + pos
		spans[i] = uint64(start)<<32 | uint64(nameLen) //nolint:gosec // start<=len(src)<=maxInt, nameLen<=65535.

		pos += nameLen
	}

	// consumed == IndexOffset - HeaderSize.
	if pos != len(extent) {
		pool.PutUint64Slice(spansPtr)
		return nil, errs.ErrMetricNamesExtentMismatch
	}

	return spansPtr, nil
}

// readEntryMetricID reads the MetricID (offset 0, 8 bytes) of index entry i.
func readEntryMetricID(src []byte, indexOffset, entrySize, i int, engine endian.EndianEngine) uint64 {
	off := indexOffset + i*entrySize

	return engine.Uint64(src[off : off+8])
}

// scanIndexForCollision performs step 7: it detects duplicate MetricIDs and,
// within each duplicate-ID group, distinguishes a real collision (two distinct
// names sharing an ID -> refuse, doStrip=false) from a duplicate name (the same
// name twice -> ErrDuplicateMetricName). For V2/V2Ext it additionally enforces
// non-descending index order (a descending pair -> ErrUnsortedIndex).
//
// spans holds the packed name span of every entry (nil when count == 0). It is
// only consulted inside a duplicate-ID group, so the no-collision path never
// touches it.
func scanIndexForCollision(
	src []byte,
	indexOffset, entrySize, count int,
	isV2 bool,
	engine endian.EndianEngine,
	spans []uint64,
) (doStrip bool, err error) {
	if count <= 1 {
		return true, nil
	}

	if isV2 {
		return scanV2(src, indexOffset, entrySize, count, engine, spans)
	}

	return scanGeneral(src, indexOffset, entrySize, count, engine, spans)
}

// scanV2 is the sorted-index single pass for V2/V2Ext. Entries are ID-sorted
// (required for GetByID's binary search to work), so equal IDs are adjacent runs.
func scanV2(src []byte, indexOffset, entrySize, count int, engine endian.EndianEngine, spans []uint64) (bool, error) {
	refuse := false
	runStart := 0
	prev := readEntryMetricID(src, indexOffset, entrySize, 0, engine)

	for i := 1; i < count; i++ {
		cur := readEntryMetricID(src, indexOffset, entrySize, i, engine)
		if cur < prev {
			return false, errs.ErrUnsortedIndex
		}
		if cur != prev {
			// Close the run [runStart, i).
			if i-runStart > 1 {
				collided, dupErr := analyzeRunAdjacent(src, spans, runStart, i)
				if dupErr != nil {
					return false, dupErr
				}
				refuse = refuse || collided
			}
			runStart = i
		}
		prev = cur
	}
	// Close the final run.
	if count-runStart > 1 {
		collided, dupErr := analyzeRunAdjacent(src, spans, runStart, count)
		if dupErr != nil {
			return false, dupErr
		}
		refuse = refuse || collided
	}

	return !refuse, nil
}

// scanGeneral is the unsorted-index path for V1 and text. It sorts a pooled
// slice of ordinals by MetricID so equal IDs become adjacent, then analyses each
// duplicate-ID run.
func scanGeneral(src []byte, indexOffset, entrySize, count int, engine endian.EndianEngine, spans []uint64) (bool, error) {
	ordPtr := pool.GetUint64Slice(count)
	defer pool.PutUint64Slice(ordPtr)
	ord := *ordPtr
	for i := 0; i < count; i++ {
		ord[i] = uint64(i)
	}

	idOf := func(ordinal uint64) uint64 {
		return readEntryMetricID(src, indexOffset, entrySize, int(ordinal), engine) //nolint:gosec // ordinal < count <= 65535.
	}

	// Sort ordinals by ID so equal IDs become adjacent. Within a run we do an
	// all-pairs name compare, so intra-run order does not matter.
	sortUint64ByID(ord, idOf)

	refuse := false
	runStart := 0
	for i := 1; i < count; i++ {
		if idOf(ord[i]) != idOf(ord[i-1]) {
			if i-runStart > 1 {
				collided, dupErr := analyzeRunOrdinals(src, spans, ord[runStart:i])
				if dupErr != nil {
					return false, dupErr
				}
				refuse = refuse || collided
			}
			runStart = i
		}
	}
	if count-runStart > 1 {
		collided, dupErr := analyzeRunOrdinals(src, spans, ord[runStart:count])
		if dupErr != nil {
			return false, dupErr
		}
		refuse = refuse || collided
	}

	return !refuse, nil
}

// analyzeRunAdjacent examines a duplicate-ID run over contiguous entry ordinals
// [lo, hi) (V2/V2Ext). It returns collided=true if the names are distinct (a
// real collision), or ErrDuplicateMetricName if any two names are byte-equal.
func analyzeRunAdjacent(src []byte, spans []uint64, lo, hi int) (bool, error) {
	for a := lo; a < hi; a++ {
		na := spanBytes(src, spans[a])
		for b := a + 1; b < hi; b++ {
			if bytes.Equal(na, spanBytes(src, spans[b])) {
				return false, errs.ErrDuplicateMetricName
			}
		}
	}

	return true, nil
}

// analyzeRunOrdinals is analyzeRunAdjacent over an explicit list of ordinals
// (V1/text, where a run's members are not contiguous in index order).
func analyzeRunOrdinals(src []byte, spans []uint64, ords []uint64) (bool, error) {
	for a := 0; a < len(ords); a++ {
		na := spanBytes(src, spans[ords[a]])
		for b := a + 1; b < len(ords); b++ {
			if bytes.Equal(na, spanBytes(src, spans[ords[b]])) {
				return false, errs.ErrDuplicateMetricName
			}
		}
	}

	return true, nil
}

// spanBytes resolves a packed name span (start<<32 | len) back to its bytes in src.
func spanBytes(src []byte, span uint64) []byte {
	start := int(span >> 32)
	length := int(span & 0xFFFFFFFF)

	return src[start : start+length]
}

// sortUint64ByID sorts ordinals in place by their MetricID (ascending). The
// slice length equals the metric count (<= 65535) and this path only runs for
// V1/text blobs. slices.SortFunc is in-place pdqsort — O(n log n) and
// allocation-free (no heap pivot buffer), so it preserves the strip validation
// path's zero-allocation guarantee while avoiding the O(n^2) blow-up an
// insertion sort suffers at the 65535-metric boundary.
func sortUint64ByID(ord []uint64, idOf func(uint64) uint64) {
	slices.SortFunc(ord, func(a, b uint64) int {
		ia, ib := idOf(a), idOf(b)
		switch {
		case ia < ib:
			return -1
		case ia > ib:
			return 1
		default:
			return 0
		}
	})
}

// slicesOverlap reports whether a and b share any bytes of their backing arrays
// (comparing full capacity, so spare-capacity aliasing is caught). Used by the
// append form to detect dst/src aliasing.
func slicesOverlap(a, b []byte) bool {
	if cap(a) == 0 || cap(b) == 0 {
		return false
	}
	a = a[:cap(a)]
	b = b[:cap(b)]
	aStart := uintptr(unsafe.Pointer(&a[0]))
	aEnd := aStart + uintptr(len(a))
	bStart := uintptr(unsafe.Pointer(&b[0]))
	bEnd := bStart + uintptr(len(b))

	return aStart < bEnd && bStart < aEnd
}
