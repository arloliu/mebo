package blob

import (
	"cmp"
	"fmt"
	"math"
	"slices"

	"github.com/arloliu/mebo/compress"
	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// NumericDecoder decodes the encoded numeric blob data and reconstructs a NumericBlob.
//
// Note: The NumericDecoder is NOT thread-safe. Each decoder instance should be used by a single goroutine at a time.
//
// Note: The NumericDecoder is NOT reusable. After calling Decode, a new decoder must be created for further decoding.
type NumericDecoder struct {
	data        []byte
	metricCount int
	engine      endian.EndianEngine
	header      *section.NumericHeader
	// borrowNames selects the zero-copy metric-names decode. When true the
	// decoded blob's names alias `data`; the default (false) copies them.
	borrowNames bool
}

// Compile-time assertions that pin the existing constructor function types.
// Code stores these constructors in typed function variables (mebo.go:191-220),
// which API_STABILITY.md forbids breaking; adding options to the existing
// signatures would change the function type. If a future change alters either
// signature, these fail to compile and force an explicit, documented decision.
var (
	_ func([]byte) (*NumericDecoder, error) = NewNumericDecoder
	_ func([]byte) (*NumericDecoder, error) = NewNumericDecoderBorrowed
)

// NewNumericDecoder creates a new NumericDecoder for the given encoded data.
//
// The decoder validates the header and prepares for decoding but does not decompress
// payloads until Decode() is called.
//
// Parameters:
//   - data: Encoded blob byte slice (must contain valid header)
//
// Returns:
//   - *NumericDecoder: New decoder instance ready for decoding
//   - error: Header parsing error or invalid data format
func NewNumericDecoder(data []byte) (*NumericDecoder, error) {
	decoder := &NumericDecoder{
		data: data,
	}

	if err := decoder.parseHeader(); err != nil {
		return nil, err
	}

	if err := decoder.parsePayloads(); err != nil {
		return nil, err
	}

	return decoder, nil
}

// NewNumericDecoderBorrowed creates a NumericDecoder that decodes metric names
// with ZERO COPY: the resulting blob's names alias the input `data` buffer
// instead of owning independent copies. This removes the ~200 name-string
// allocations per decode that otherwise dominate the names-bearing decode cost.
//
// Lifetime rule: the backing array of `data` MUST NOT be mutated or reused while
// the decoded blob (or anything derived directly from its names) is live. Doing
// so corrupts the blob's metric names. Use NewNumericDecoder for the owning
// (copying) behaviour when the caller cannot guarantee that.
//
// Materialising a blob decoded this way CLONES the names (Materialize /
// MaterializeMetric* and set materialization), so materialized objects are always
// owning and the borrowed-lifetime rule never propagates past the blob itself.
//
// Everything else is identical to NewNumericDecoder.
//
// Parameters:
//   - data: Encoded blob byte slice (must contain valid header). Its backing
//     array is borrowed by the returned decoder's metric names — see the
//     lifetime rule above.
//
// Returns:
//   - *NumericDecoder: New decoder instance whose metric names alias data,
//     ready for decoding
//   - error: Header parsing error or invalid data format
func NewNumericDecoderBorrowed(data []byte) (*NumericDecoder, error) {
	decoder := &NumericDecoder{
		data:        data,
		borrowNames: true,
	}

	if err := decoder.parseHeader(); err != nil {
		return nil, err
	}

	if err := decoder.parsePayloads(); err != nil {
		return nil, err
	}

	return decoder, nil
}

// Decode decodes the encoded data into a NumericBlob.
//
// This method decompresses all payloads, parses index entries, and reconstructs the blob
// structure. If metric names are present, it verifies name hashes and builds the name index.
//
// Returns:
//   - NumericBlob: Decoded blob with timestamp/value/tag payloads and index maps
//   - error: Payload offset validation errors, decompression errors, index parsing errors,
//     or metric name verification failures
func (d *NumericDecoder) Decode() (NumericBlob, error) {
	// Pack flags into single uint16 for size optimization
	var flags uint16
	if d.header.Flag.IsBigEndian() {
		flags |= section.FlagEndianLittleEndian // 1=big endian
	}
	if d.header.Flag.TimestampEncoding() == format.TypeRaw {
		flags |= section.FlagTsEncRaw
	}
	if d.header.Flag.HasTag() {
		flags |= section.FlagTagEnabled
	}
	if d.header.Flag.HasMetricNames() {
		flags |= section.FlagMetricNames
	}

	blob := NumericBlob{
		blobBase: blobBase{
			tsEncType:  d.header.Flag.TimestampEncoding(),
			valEncType: d.header.Flag.ValueEncoding(),
			flags:      flags, // Packed flags (optimized)
			formatVersion: func() uint8 {
				if d.header.Flag.IsV2() {
					return blobFormatV2
				}

				return blobFormatV1
			}(),
			sameByteOrder: endian.CompareNativeEndian(d.engine),
			endianType: func() uint8 {
				if d.header.Flag.IsBigEndian() {
					return 1
				}

				return 0
			}(), // 0=little, 1=big
			startTimeMicros: d.header.StartTime, // Direct int64 assignment (optimized)
		},
	}

	if err := d.validatePayloadOffsets(); err != nil {
		return blob, err
	}

	tsOffset := int(d.header.TimestampPayloadOffset)
	valOffset := int(d.header.ValuePayloadOffset)
	tagOffset := int(d.header.TagPayloadOffset)

	// Step 1: Parse metric names (if present)
	metricNames, indexOffset, err := d.parseMetricNames()
	if err != nil {
		return blob, err
	}

	// Step 2: Decompress payloads (do this before parsing index entries)
	payloads, err := d.decompressPayloads(tsOffset, valOffset, tagOffset)
	if err != nil {
		return blob, err
	}

	blob.tsPayload = payloads.tsPayload
	blob.valPayload = payloads.valPayload
	blob.tagPayload = payloads.tagPayload

	// Step 3: Parse index entries (now we know decompressed payload sizes)
	// For V2 without metric names, skip metricIDs allocation (it would be unused).
	// For V2 with metric names, metricIDs is reused directly as sortedIDs.
	needMetricIDs := len(metricNames) > 0
	indexEntries, metricIDs, err := d.parseIndexEntries(indexOffset, len(blob.tsPayload), len(blob.valPayload), len(blob.tagPayload), needMetricIDs)
	if err != nil {
		return blob, err
	}

	// Step 3.5: If shared timestamps flag is set, parse and apply shared timestamp table
	hasShared := d.header.Flag.HasSharedTimestamps()
	if hasShared {
		if err := d.applySharedTimestamps(indexOffset, indexEntries); err != nil {
			return blob, err
		}
	}

	// Step 3.6: Every read path and the shared-timestamp cache size their work
	// from each entry's Count, so prove it fits the entry's payload first.
	if err := validateEntryCounts(indexEntries, blob.tsEncType, blob.valEncType, d.header.Flag.HasTag()); err != nil {
		return blob, err
	}

	// Step 3.7: If values are ALP-encoded, validate every column's structure
	// once here (blob open), not on the decode hot path. An unknown scheme
	// byte (>= 3) would otherwise decode silently as an empty/zero column
	// through All/DecodeAll/At and the ForEach materialize path alike — every
	// one of those falls through an unlabeled default: case — which is
	// indistinguishable from data loss. See the alpScheme* doc comment in
	// internal/encoding/value/alp/alp.go for why this set is closed. A column
	// whose body is shorter than its header-declared layout (or whose header
	// fields are out of range) would otherwise panic deep in the decode paths
	// on out-of-range slicing/indexing — validated here too, so both classes
	// of corruption are caught at blob open instead of on the decode hot path.
	// ALP-RLE columns may also use the runs layout (scheme 3); ALP columns may not.
	if blob.valEncType == format.TypeALP || blob.valEncType == format.TypeALPRLE {
		if err := validateALPColumns(blob.valPayload, indexEntries, d.engine, blob.valEncType == format.TypeALPRLE); err != nil {
			return blob, err
		}
	}

	if hasShared {
		// Pre-decode the timestamps of every offset used by more than one metric.
		// After ApplySharedTimestampTable, shared metrics have identical TimestampOffset values.
		d.buildSharedTimestamps(&blob, indexEntries)
	}

	// Step 4: Build index — V2 uses sorted slice, V1 uses first-wins ordinal map
	d.buildIndex(&blob, indexEntries, metricIDs)

	// Step 5: Verify names, reject a blob that stores the same name twice, and
	// finalize the index name representation (retain ordered names; build
	// byName only on collision).
	if len(metricNames) > 0 {
		if err := ienc.VerifyMetricNamesHashes(metricNames, metricIDs, hash.ID); err != nil {
			return blob, fmt.Errorf("metric name verification failed: %w", err)
		}

		// metricNames[i] corresponds to indexEntries[i] (consistent ordering).
		if err := blob.index.finalizeNames(metricNames, d.borrowNames); err != nil {
			return blob, err
		}
	}

	return blob, nil
}

// buildIndex populates the blob's index from parsed index entries.
// V2 uses the sorted slice with parallel sortedIDs; V1 uses a first-wins
// MetricID→ordinal map. `sorted` (index order) is always populated.
func (d *NumericDecoder) buildIndex(blob *NumericBlob, indexEntries []section.NumericIndexEntry, metricIDs []uint64) {
	blob.index.sorted = indexEntries

	if d.header.Flag.IsV2() {
		if metricIDs != nil {
			// Reuse metricIDs from parseIndexEntries as sortedIDs (same data, same order)
			blob.index.sortedIDs = metricIDs
		} else {
			// Build sortedIDs when metricIDs wasn't allocated (V2 without metric names)
			blob.index.sortedIDs = make([]uint64, len(indexEntries))
			for i := range indexEntries {
				blob.index.sortedIDs[i] = indexEntries[i].MetricID
			}
		}

		return
	}

	// V1: first-wins MetricID→ordinal map so a collided ID resolves to its
	// first entry in index order.
	byID := make(map[uint64]int, d.metricCount)
	for i := range indexEntries {
		id := indexEntries[i].MetricID
		if _, exists := byID[id]; !exists {
			byID[id] = i
		}
	}
	blob.index.byID = byID
}

// parseHeader parses the header section of the encoded data.
func (d *NumericDecoder) parseHeader() error {
	header, err := section.ParseNumericHeader(d.data)
	if err != nil {
		return err
	}

	d.engine = header.Flag.GetEndianEngine()
	d.metricCount = int(header.MetricCount)
	d.header = &header

	return nil
}

// parsePayloads extracts the timestamp and value payloads from the encoded data.
func (d *NumericDecoder) parsePayloads() error {
	headerSize := section.HeaderSize
	if len(d.data) < headerSize {
		return errs.ErrInvalidHeaderSize
	}

	return nil
}

// applySharedTimestamps parses the shared timestamp table that sits between the
// index and the timestamp payload, and points every member entry at its
// canonical entry's timestamps.
func (d *NumericDecoder) applySharedTimestamps(indexOffset int, indexEntries []section.NumericIndexEntry) error {
	indexEnd := indexOffset + d.metricCount*d.header.Flag.IndexEntrySize()
	sharedTableEnd := int(d.header.TimestampPayloadOffset)

	if sharedTableEnd <= indexEnd {
		return fmt.Errorf("%w: shared timestamps flag set but table missing", errs.ErrInvalidSharedTimestampTable)
	}

	sharedTableData := d.data[indexEnd:sharedTableEnd]
	if err := section.ApplySharedTimestampTable(sharedTableData, d.engine, d.metricCount, indexEntries); err != nil {
		return fmt.Errorf("failed to parse shared timestamp table: %w", err)
	}

	return nil
}

// validateEntryCounts checks that each entry's Count fits its payload ranges.
// Every timestamp encoding spends at least one byte per point (raw exactly
// eight), raw values exactly eight, and each tag at least its one-byte length,
// so a larger Count can only come from a corrupt or crafted index. Value
// codecs that can spend less than a byte per point (Gorilla, Chimp, ALP,
// ALP-RLE) are bounded through the timestamps instead; ALP and ALP-RLE still
// need a non-empty column.
func validateEntryCounts(entries []section.NumericIndexEntry, tsEnc, valEnc format.EncodingType, hasTag bool) error {
	for i := range entries {
		entry := &entries[i]
		count := entry.Count
		if count == 0 {
			continue
		}

		tsOK := count <= entry.TimestampLength
		if tsEnc == format.TypeRaw {
			tsOK = entry.TimestampLength/8 == count && entry.TimestampLength%8 == 0
		}

		valOK := entry.ValueLength > 0
		if valEnc == format.TypeRaw {
			valOK = entry.ValueLength/8 == count && entry.ValueLength%8 == 0
		}

		tagOK := !hasTag || count <= entry.TagLength

		if !tsOK || !valOK || !tagOK {
			return fmt.Errorf("%w: metric ID %d has count %d but %d timestamp, %d value and %d tag bytes",
				errs.ErrInvalidNumOfDataPoints, entry.MetricID, count, entry.TimestampLength, entry.ValueLength, entry.TagLength)
		}
	}

	return nil
}

// validatePayloadOffsets checks that the header's timestamp, value and tag
// payload offsets each lie within the blob and are in layout order; inverted
// offsets would slice backwards when the sections are cut apart.
func (d *NumericDecoder) validatePayloadOffsets() error {
	tsOffset := int(d.header.TimestampPayloadOffset)
	if len(d.data) < tsOffset {
		return errs.ErrInvalidTimestampPayloadOffset
	}

	valOffset := int(d.header.ValuePayloadOffset)
	if len(d.data) < valOffset || tsOffset > valOffset {
		return errs.ErrInvalidValuePayloadOffset
	}

	tagOffset := int(d.header.TagPayloadOffset)
	if len(d.data) < tagOffset || valOffset > tagOffset {
		return errs.ErrInvalidTagPayloadOffset
	}

	return nil
}

// parseMetricNames decodes the metric names payload if present.
// Returns the metric names slice and the byte offset where the index section starts.
func (d *NumericDecoder) parseMetricNames() ([]string, int, error) {
	if !d.header.Flag.HasMetricNames() {
		return nil, section.HeaderSize, nil
	}

	decodeNames := ienc.DecodeMetricNames
	if d.borrowNames {
		decodeNames = ienc.DecodeMetricNamesBorrowed
	}

	metricNames, bytesRead, err := decodeNames(d.data[section.HeaderSize:], d.engine)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to decode metric names: %w", err)
	}

	// Verify metric names count matches header
	if len(metricNames) != d.metricCount {
		return nil, 0, fmt.Errorf("%w: expected %d names, got %d",
			errs.ErrInvalidMetricNamesCount, d.metricCount, len(metricNames))
	}

	indexOffset := section.HeaderSize + bytesRead

	return metricNames, indexOffset, nil
}

// parseIndexEntries parses the index section and populates the index entry map.
// Returns the parsed index entries in order and the metric IDs for verification.
// Uses the provided decompressed payload sizes to calculate entry lengths correctly.
// When needMetricIDs is false, the metricIDs slice is not allocated (returns nil).
func (d *NumericDecoder) parseIndexEntries(
	indexOffset, tsPayloadSize, valPayloadSize, tagPayloadSize int,
	needMetricIDs bool,
) ([]section.NumericIndexEntry, []uint64, error) {
	entrySize := d.header.Flag.IndexEntrySize()
	// Compare counts before multiplying: on 32-bit platforms a crafted
	// MetricCount times the entry size can wrap.
	if indexOffset > len(d.data) || d.metricCount > (len(d.data)-indexOffset)/entrySize {
		return nil, nil, errs.ErrInvalidIndexEntrySize
	}
	indexSize := entrySize * d.metricCount

	// The timestamp payload (or the shared timestamp table before it) must
	// start at or after the end of the index, never inside the header or index.
	if indexOffset+indexSize > int(d.header.TimestampPayloadOffset) {
		return nil, nil, fmt.Errorf("%w: timestamp payload at %d overlaps the index ending at %d",
			errs.ErrInvalidTimestampPayloadOffset, d.header.TimestampPayloadOffset, indexOffset+indexSize)
	}

	indexData := d.data[indexOffset : indexOffset+indexSize]
	// Use int for accumulated offsets to prevent uint16 overflow
	// Index entries store deltas as uint16/uint32, but absolute offsets can exceed those ranges
	var lastTsOffset int
	var lastValOffset int
	var lastTagOffset int

	// Pre-allocate slices with exact size for better performance
	// Direct indexing eliminates bounds checking on each append operation
	indexEntries := make([]section.NumericIndexEntry, d.metricCount)
	var metricIDs []uint64
	if needMetricIDs {
		metricIDs = make([]uint64, d.metricCount)
	}

	// Select parser based on entry size (compact 16B vs extended 32B)
	parseEntry := section.ParseNumericIndexEntry
	if entrySize == section.NumericExtIndexEntrySize {
		parseEntry = section.ParseNumericIndexEntryExt
	}

	// V2/V2Ext lookup relies on MetricIDs being non-descending. Validate that at
	// decode open (before buildIndex selects a lookup representation).
	// Strictly-decreasing (id < prev) is rejected; equal adjacent IDs (id == prev)
	// are a legitimate collision and accepted.
	validateV2Order := d.header.Flag.IsV2()

	var err error
	for i := 0; i < d.metricCount; i++ {
		start := i * entrySize
		end := start + entrySize

		indexEntries[i], err = parseEntry(indexData[start:end], d.engine)
		if err != nil {
			return nil, nil, err
		}

		curEntry := &indexEntries[i]

		// Convert delta offsets to absolute offsets
		// Accumulate in int to prevent uint16 overflow, entry now has int fields
		lastTsOffset += curEntry.TimestampOffset
		lastValOffset += curEntry.ValueOffset
		lastTagOffset += curEntry.TagOffset

		curEntry.TimestampOffset = lastTsOffset
		curEntry.ValueOffset = lastValOffset
		curEntry.TagOffset = lastTagOffset

		if needMetricIDs {
			metricIDs[i] = curEntry.MetricID
		}

		// Calculate entry lengths for validation later
		if i > 0 {
			prevEntry := &indexEntries[i-1]

			// Reject strictly-decreasing MetricID order on V2/V2Ext: an unsorted
			// index would make the binary-search lookup silently miss entries.
			if validateV2Order && curEntry.MetricID < prevEntry.MetricID {
				return nil, nil, errs.ErrUnsortedIndex
			}

			// Validate offsets are non-decreasing
			if lastTsOffset < prevEntry.TimestampOffset ||
				lastValOffset < prevEntry.ValueOffset ||
				lastTagOffset < prevEntry.TagOffset {
				return nil, nil, errs.ErrInvalidIndexOffsets
			}

			prevEntry.TimestampLength = lastTsOffset - prevEntry.TimestampOffset
			prevEntry.ValueLength = lastValOffset - prevEntry.ValueOffset
			prevEntry.TagLength = lastTagOffset - prevEntry.TagOffset
		}
	}

	// Calculate the last entry's lengths using decompressed payload sizes
	if d.metricCount > 0 {
		lastEntry := &indexEntries[d.metricCount-1]

		// Validate last entry offsets don't exceed payload sizes
		if lastEntry.TimestampOffset > tsPayloadSize ||
			lastEntry.ValueOffset > valPayloadSize ||
			lastEntry.TagOffset > tagPayloadSize {
			return nil, nil, errs.ErrInvalidIndexOffsets
		}

		lastEntry.TimestampLength = tsPayloadSize - lastEntry.TimestampOffset
		lastEntry.ValueLength = valPayloadSize - lastEntry.ValueOffset
		lastEntry.TagLength = tagPayloadSize - lastEntry.TagOffset
	}

	// Final validation: ensure offsets are non-negative and lengths are valid
	if lastTsOffset < 0 || lastValOffset < 0 || lastTagOffset < 0 {
		return nil, nil, errs.ErrInvalidIndexOffsets
	}

	return indexEntries, metricIDs, nil
}

// validateALPExceptionPositions checks that an ALP column's exception sidecar
// lists strictly ascending positions below count. The encoder always writes
// them that way; the decode paths patch, iterate and binary-search the
// sidecar under that assumption, so unsorted or duplicate positions would make
// AllValues, DecodeAll and ValueAt disagree. stride is the bytes per exception.
func validateALPExceptionPositions(exc []byte, nExc, stride, count int, engine endian.EndianEngine) error {
	var prev uint32
	for k := range nExc {
		pos := engine.Uint32(exc[k*stride : k*stride+4])
		if uint64(pos) >= uint64(count) || (k > 0 && pos <= prev) { //nolint:gosec // count is non-negative
			return fmt.Errorf("exception %d has position %d, want ascending positions below %d", k, pos, count)
		}
		prev = pos
	}

	return nil
}

// maxALPMainWidth is the widest packed code an ALP main column can hold:
// codes are FOR-adjusted uint64 values.
const maxALPMainWidth = 64

// maxALPColumnBits caps a column's packed bit count so every bit position and
// section offset the ALP decoders compute in int stays representable,
// including the +7 that rounds a bit count up to bytes. It only
// binds on 32-bit platforms; it is a variable so tests can emulate them.
var maxALPColumnBits = uint64(math.MaxInt)

// validateALPColumns checks that every ALP-encoded value column begins with
// a known scheme byte (0=main, 1=RD, 2=raw; see internal/encoding/value/alp's
// ALPMaxSchemeByte), plus 3=runs when allowRuns is set (TypeALPRLE blobs only),
// and that the column's body is at least
// as long as its own header-declared layout requires. It runs once per
// column at blob open — this is the earliest seam that both sees the
// decompressed column payload and can return an error — rather than inside
// All/DecodeAll/At, whose signatures stay error-free.
//
// A column with a corrupt or future/unknown scheme byte would otherwise
// decode as an empty/zero column with no indication anything went wrong. A
// column whose declared nExc/nDict/width/codeBits/rbw fields describe a
// layout longer than the actual column body would otherwise panic deep in
// decodeMainInto/decodeRDInto on out-of-range slicing or indexing. For the
// RD scheme specifically, codeBits is also bounded to at most 3 (see the
// codeBits check below): nDict alone only bounds the number of live dict
// entries, not the *width* of the packed codes, and decodeRDInto/allRD/atRD
// index the fixed 8-entry dict array directly with an unpacked
// codeBits-wide code (dict[alpReadBitsFast(...)]) — bounding codeBits to 3
// caps every possible unpacked code at 7, which is always in range for that
// array regardless of nDict. Together, this check makes every downstream
// slice/index in those decode paths provably in-bounds by construction. It
// uses >= (minimum required length), not ==, since its job is
// bounds-safety, not pinning the encoder's exact output size.
//
// Header fields are range-checked as well.
// A main column's exponent and factor index the power-of-ten tables,
// so both must be at most ALPMaxExponent, and its width is at most 64.
// An RD column's right width must be within ALPRDMinRightBits..ALPRDMaxRightBits.
func validateALPColumns(valPayload []byte, indexEntries []section.NumericIndexEntry, engine endian.EndianEngine, allowRuns bool) error {
	for i := range indexEntries {
		entry := &indexEntries[i]
		if entry.ValueLength == 0 {
			continue
		}

		column := valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength]
		if allowRuns && column[0] == ienc.ALPRLEMaxSchemeByte {
			if err := validateALPRunsColumn(entry, column[1:], engine); err != nil {
				return err
			}

			continue
		}

		if column[0] > ienc.ALPMaxSchemeByte {
			want := "0 (main), 1 (rd), or 2 (raw)"
			if allowRuns {
				want = "0 (main), 1 (rd), 2 (raw), or 3 (runs)"
			}

			return fmt.Errorf("%w: metric ID %d has ALP scheme byte %d, want %s",
				errs.ErrInvalidALPScheme, entry.MetricID, column[0], want)
		}

		if err := validateALPPlainColumn(entry, column, engine); err != nil {
			return err
		}
	}

	return nil
}

// validateALPPlainColumn checks one ALP column whose scheme byte is 0 (main), 1 (rd) or 2 (raw).
// The caller has already range-checked the scheme byte against ALPMaxSchemeByte.
func validateALPPlainColumn(entry *section.NumericIndexEntry, column []byte, engine endian.EndianEngine) error {
	body := column[1:]
	count := entry.Count

	// Scheme byte values below mirror the unexported alpSchemeMain (0),
	// alpSchemeRD (1), alpSchemeRaw (2) constants in
	// internal/encoding/value/alp/alp.go — already range-checked against
	// ALPMaxSchemeByte by the caller, so this switch is exhaustive.
	switch column[0] {
	case 0: // alpSchemeMain
		return validateALPMainColumn(entry, body, engine)
	case 1: // alpSchemeRD
		return validateALPRDColumn(entry, body, engine)
	case 2: // alpSchemeRaw
		want := 1 + uint64(count)*8 //nolint:gosec // count is non-negative
		if uint64(len(column)) < want {
			return fmt.Errorf("%w: metric ID %d has ALP raw column of %d bytes, want at least %d (count=%d)",
				errs.ErrInvalidALPColumn, entry.MetricID, len(column), want, count)
		}
	default:
		// Unreachable: the caller range-checks the scheme byte against
		// ALPMaxSchemeByte, so it is always 0, 1, or 2 here.
	}

	return nil
}

// validateALPRunsColumn checks one ALP-RLE runs column body (after scheme byte 3).
// The codec checks the runs envelope (header, bitmap, nested scheme byte);
// the nested column of run values then goes through every plain-column check with count = nRuns,
// including the maxALPColumnBits bound the nested decoders rely on.
func validateALPRunsColumn(entry *section.NumericIndexEntry, body []byte, engine endian.EndianEngine) error {
	nRuns, nested, err := ienc.ValidateALPRunsColumn(body, entry.Count, engine)
	if err != nil {
		return fmt.Errorf("metric ID %d: %w", entry.MetricID, err)
	}

	nestedEntry := *entry
	nestedEntry.Count = nRuns
	if err := validateALPPlainColumn(&nestedEntry, nested, engine); err != nil {
		return fmt.Errorf("nested run values of %d runs: %w", nRuns, err)
	}

	return nil
}

// validateALPMainColumn checks one ALP main-scheme column body (after the
// scheme byte) for header ranges, length and exception positions.
func validateALPMainColumn(entry *section.NumericIndexEntry, body []byte, engine endian.EndianEngine) error {
	count := entry.Count
	const mainHeaderSize = 15
	if len(body) < mainHeaderSize {
		return fmt.Errorf("%w: metric ID %d has ALP main column body of %d bytes, want at least %d (fixed header)",
			errs.ErrInvalidALPColumn, entry.MetricID, len(body), mainHeaderSize)
	}

	// e and f index the 19-entry power-of-ten tables in every
	// decode path; an out-of-range byte would panic there.
	exp, factor := int(body[0]), int(body[1])
	if exp > ienc.ALPMaxExponent || factor > ienc.ALPMaxExponent {
		return fmt.Errorf("%w: metric ID %d has ALP main column exponent %d / factor %d, want at most %d",
			errs.ErrInvalidALPColumn, entry.MetricID, exp, factor, ienc.ALPMaxExponent)
	}

	width := int(body[2])
	if width > maxALPMainWidth {
		return fmt.Errorf("%w: metric ID %d has ALP main column width %d, want at most %d",
			errs.ErrInvalidALPColumn, entry.MetricID, width, maxALPMainWidth)
	}

	// Keep nExc unsigned until the length check bounds it: int(uint32)
	// is negative on 32-bit platforms for values >= 1<<31.
	nExc := uint64(engine.Uint32(body[3:7]))
	if uint64(count)*uint64(width)+7 > maxALPColumnBits { //nolint:gosec // count and width are non-negative
		return fmt.Errorf("%w: metric ID %d has ALP main column of %d × %d bits, too large for this platform",
			errs.ErrInvalidALPColumn, entry.MetricID, count, width)
	}

	// uint64 arithmetic: count*width and nExc*12 can wrap int on 32-bit.
	want := uint64(mainHeaderSize) + (uint64(count)*uint64(width)+7)/8 + nExc*12 //nolint:gosec // count and width are non-negative
	if uint64(len(body)) < want {
		return fmt.Errorf("%w: metric ID %d has ALP main column body of %d bytes, want at least %d (width=%d, nExc=%d, count=%d)",
			errs.ErrInvalidALPColumn, entry.MetricID, len(body), want, width, nExc, count)
	}

	excStart := mainHeaderSize + (count*width+7)/8
	if err := validateALPExceptionPositions(body[excStart:], int(nExc), 12, count, engine); err != nil { //nolint:gosec // nExc*12 <= len(body)
		return fmt.Errorf("%w: metric ID %d: %w", errs.ErrInvalidALPColumn, entry.MetricID, err)
	}

	return nil
}

// validateALPRDColumn checks one ALP-RD column body (after the scheme byte) for
// header ranges, length and exception positions.
func validateALPRDColumn(entry *section.NumericIndexEntry, body []byte, engine endian.EndianEngine) error {
	count := entry.Count
	const rdHeaderSize = 7
	if len(body) < rdHeaderSize {
		return fmt.Errorf("%w: metric ID %d has ALP rd column body of %d bytes, want at least %d (fixed header)",
			errs.ErrInvalidALPColumn, entry.MetricID, len(body), rdHeaderSize)
	}

	rbw := int(body[0])
	codeBits := int(body[1])
	nDict := int(body[2])
	nExc := uint64(engine.Uint32(body[3:7])) // unsigned until bounded; see validateALPMainColumn
	if rbw < ienc.ALPRDMinRightBits || rbw > ienc.ALPRDMaxRightBits {
		return fmt.Errorf("%w: metric ID %d has ALP rd column right width %d, want %d..%d",
			errs.ErrInvalidALPColumn, entry.MetricID, rbw, ienc.ALPRDMinRightBits, ienc.ALPRDMaxRightBits)
	}

	if nDict > ienc.ALPRDMaxDictSize {
		return fmt.Errorf("%w: metric ID %d has ALP rd column nDict %d, want at most %d",
			errs.ErrInvalidALPColumn, entry.MetricID, nDict, ienc.ALPRDMaxDictSize)
	}

	// codeBits must fit the fixed 8-entry dict array the decode paths
	// index into. alpCodeBits(nDict) = bits.Len64(nDict-1) for a
	// valid encoder output, which for nDict <= ALPRDMaxDictSize (8)
	// tops out at bits.Len64(8-1) = bits.Len64(7) = 3 — so 3 is the
	// largest codeBits an encoder can ever emit. Reject anything
	// larger: decodeRDInto/allRD/atRD unpack a codeBits-wide code and
	// index dict[code] with no other bound, so codeBits > 3 lets a
	// corrupt code exceed the array and panic with index out of
	// range. Deliberately compare against the literal 3 rather than
	// checking `1<<codeBits > ienc.ALPRDMaxDictSize`: codeBits is an
	// attacker-controlled byte (0-255), and Go's shift operator
	// yields 0 for shift counts >= 64, so that form would silently
	// pass validation for a corrupt codeBits like 64. (Codes that are
	// < 1<<codeBits but >= nDict read a zero-valued dict entry —
	// garbage output, not a panic — and need no separate check.)
	const maxRDCodeBits = 3
	if codeBits > maxRDCodeBits {
		return fmt.Errorf("%w: metric ID %d has ALP rd column codeBits %d, want at most %d",
			errs.ErrInvalidALPColumn, entry.MetricID, codeBits, maxRDCodeBits)
	}

	if uint64(count)*uint64(max(codeBits, rbw))+7 > maxALPColumnBits { //nolint:gosec // count and widths are non-negative
		return fmt.Errorf("%w: metric ID %d has ALP rd column of %d × %d bits, too large for this platform",
			errs.ErrInvalidALPColumn, entry.MetricID, count, max(codeBits, rbw))
	}

	want := uint64(rdHeaderSize) + uint64(nDict)*2 + (uint64(count)*uint64(codeBits)+7)/8 + //nolint:gosec // all fields are non-negative
		(uint64(count)*uint64(rbw)+7)/8 + nExc*6 //nolint:gosec // all fields are non-negative
	if uint64(len(body)) < want {
		return fmt.Errorf("%w: metric ID %d has ALP rd column body of %d bytes, want at least %d (rbw=%d, codeBits=%d, nDict=%d, nExc=%d, count=%d)",
			errs.ErrInvalidALPColumn, entry.MetricID, len(body), want, rbw, codeBits, nDict, nExc, count)
	}

	excStart := rdHeaderSize + nDict*2 + (count*codeBits+7)/8 + (count*rbw+7)/8
	if err := validateALPExceptionPositions(body[excStart:], int(nExc), 6, count, engine); err != nil { //nolint:gosec // nExc*6 <= len(body)
		return fmt.Errorf("%w: metric ID %d: %w", errs.ErrInvalidALPColumn, entry.MetricID, err)
	}

	return nil
}

// decodedPayloads holds the decompressed payload data.
type decodedPayloads struct {
	tsPayload  []byte
	valPayload []byte
	tagPayload []byte
}

// decompressPayloads decompresses timestamp, value, and tag payloads.
func (d *NumericDecoder) decompressPayloads(tsOffset, valOffset, tagOffset int) (decodedPayloads, error) {
	// Get built-in codecs based on header settings
	tsCodec, err := compress.GetCodec(d.header.Flag.TimestampCompression())
	if err != nil {
		return decodedPayloads{}, fmt.Errorf("unsupported timestamp compression: %w", err)
	}

	valCodec, err := compress.GetCodec(d.header.Flag.ValueCompression())
	if err != nil {
		return decodedPayloads{}, fmt.Errorf("unsupported value compression: %w", err)
	}

	// Decompress timestamp and value payloads
	tsPayload, err := tsCodec.Decompress(d.data[tsOffset:valOffset])
	if err != nil {
		return decodedPayloads{}, fmt.Errorf("failed to decompress timestamp payload: %w", err)
	}

	valPayload, err := valCodec.Decompress(d.data[valOffset:tagOffset])
	if err != nil {
		return decodedPayloads{}, fmt.Errorf("failed to decompress value payload: %w", err)
	}

	// Decompress tag payload only if tag support is enabled
	var tagPayload []byte
	if d.header.Flag.HasTag() {
		tagCodec, err := compress.GetCodec(format.CompressionZstd)
		if err != nil {
			return decodedPayloads{}, fmt.Errorf("unsupported tag compression: %w", err)
		}

		tagPayload, err = tagCodec.Decompress(d.data[tagOffset:])
		if err != nil {
			return decodedPayloads{}, fmt.Errorf("failed to decompress tag payload: %w", err)
		}
	}

	return decodedPayloads{
		tsPayload:  tsPayload,
		valPayload: valPayload,
		tagPayload: tagPayload,
	}, nil
}

// buildSharedTimestamps fills the blob's shared-timestamp groups, sorted by offset,
// with the pre-decoded timestamps of every offset that several metrics share.
// This avoids redundant decoding when iterating timestamps across many metrics
// that share the same underlying timestamp data.
func (d *NumericDecoder) buildSharedTimestamps(blob *NumericBlob, indexEntries []section.NumericIndexEntry) {
	// Count how many metrics reference each TimestampOffset
	refCount := make(map[int]int, d.metricCount)
	for i := range indexEntries[:d.metricCount] {
		refCount[indexEntries[i].TimestampOffset]++
	}

	// Pre-decode only offsets used by more than one metric, once each
	var groups []sharedTimestampGroup
	seen := make(map[int]struct{})
	for i := range indexEntries[:d.metricCount] {
		entry := &indexEntries[i]
		if refCount[entry.TimestampOffset] <= 1 {
			continue
		}
		if _, exists := seen[entry.TimestampOffset]; exists {
			continue
		}
		seen[entry.TimestampOffset] = struct{}{}

		tsBytes := blob.tsPayload[entry.TimestampOffset : entry.TimestampOffset+entry.TimestampLength]
		decoded := make([]int64, entry.Count)
		produced := blob.decodeTimestampsSlice(tsBytes, entry.Count, decoded)
		groups = append(groups, sharedTimestampGroup{offset: entry.TimestampOffset, ts: decoded[:produced]})
	}

	if len(groups) > 0 {
		slices.SortFunc(groups, func(a, b sharedTimestampGroup) int { return cmp.Compare(a.offset, b.offset) })
		blob.sharedTs = &sharedTimestamps{first: groups[0], groups: groups}
	}
}
