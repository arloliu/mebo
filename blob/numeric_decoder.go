package blob

import (
	"fmt"

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
}

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

	// Validate payload offsets
	tsOffset := int(d.header.TimestampPayloadOffset)
	if len(d.data) < tsOffset {
		return blob, errs.ErrInvalidTimestampPayloadOffset
	}

	valOffset := int(d.header.ValuePayloadOffset)
	if len(d.data) < valOffset {
		return blob, errs.ErrInvalidValuePayloadOffset
	}

	tagOffset := int(d.header.TagPayloadOffset)
	if len(d.data) < tagOffset {
		return blob, errs.ErrInvalidTagPayloadOffset
	}

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

	// Step 3.4: If values are ALP-encoded, validate every column's structure
	// once here (blob open), not on the decode hot path. An unknown scheme
	// byte (>= 3) would otherwise decode silently as an empty/zero column
	// through All/DecodeAll/At and the ForEach materialize path alike — every
	// one of those falls through an unlabeled default: case — which is
	// indistinguishable from data loss. See the alpScheme* doc comment in
	// internal/encoding/numeric_alp.go for why this set is closed. A column
	// whose body is shorter than its header-declared layout (or whose header
	// fields are out of range) would otherwise panic deep in the decode paths
	// on out-of-range slicing/indexing — validated here too, so both classes
	// of corruption are caught at blob open instead of on the decode hot path.
	if blob.valEncType == format.TypeALP {
		if err := validateALPColumns(blob.valPayload, indexEntries, d.engine); err != nil {
			return blob, err
		}
	}

	// Step 3.5: If shared timestamps flag is set, parse and apply shared timestamp table
	if d.header.Flag.HasSharedTimestamps() {
		indexEnd := indexOffset + d.metricCount*d.header.Flag.IndexEntrySize()
		sharedTableEnd := int(d.header.TimestampPayloadOffset)

		if sharedTableEnd <= indexEnd {
			return blob, fmt.Errorf("%w: shared timestamps flag set but table missing", errs.ErrInvalidSharedTimestampTable)
		}

		sharedTableData := d.data[indexEnd:sharedTableEnd]
		if err := section.ApplySharedTimestampTable(sharedTableData, d.engine, d.metricCount, indexEntries); err != nil {
			return blob, fmt.Errorf("failed to parse shared timestamp table: %w", err)
		}

		// Build sharedTsCache: pre-decode timestamps for offsets used by multiple metrics.
		// After ApplySharedTimestampTable, shared metrics have identical TimestampOffset values.
		d.buildSharedTsCache(&blob, indexEntries)
	}

	// Step 4: Build index — V2 uses sorted slice, V1 uses map
	d.buildIndex(&blob, indexEntries, metricIDs)

	// Step 5: Verify and populate metric name map (if metric names present)
	if len(metricNames) > 0 {
		if err := ienc.VerifyMetricNamesHashes(metricNames, metricIDs, hash.ID); err != nil {
			return blob, fmt.Errorf("metric name verification failed: %w", err)
		}

		// Populate metric name map for ByName lookups
		// metricNames[i] corresponds to indexEntries[i] (consistent ordering)
		blob.index.byName = make(map[string]section.NumericIndexEntry, d.metricCount)
		for i, name := range metricNames {
			blob.index.byName[name] = indexEntries[i]
		}
	}

	return blob, nil
}

// buildIndex populates the blob's index from parsed index entries.
// V2 uses sorted slice with parallel sortedIDs; V1 uses map.
func (d *NumericDecoder) buildIndex(blob *NumericBlob, indexEntries []section.NumericIndexEntry, metricIDs []uint64) {
	if d.header.Flag.IsV2() {
		// V2: entries are already sorted by MetricID from the encoder.
		// Assign directly — no copy needed since parseIndexEntries returns a dedicated slice.
		blob.index.sorted = indexEntries

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

	// V1: map-based index for O(1) amortized lookups
	blob.index.byID = make(map[uint64]section.NumericIndexEntry, d.metricCount)
	for _, entry := range indexEntries {
		blob.index.byID[entry.MetricID] = entry
	}
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

// parseMetricNames decodes the metric names payload if present.
// Returns the metric names slice and the byte offset where the index section starts.
func (d *NumericDecoder) parseMetricNames() ([]string, int, error) {
	if !d.header.Flag.HasMetricNames() {
		return nil, section.HeaderSize, nil
	}

	metricNames, bytesRead, err := ienc.DecodeMetricNames(d.data[section.HeaderSize:], d.engine)
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
	indexSize := entrySize * d.metricCount
	if len(d.data) < indexOffset+indexSize {
		return nil, nil, errs.ErrInvalidIndexEntrySize
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

// validateALPColumns checks that every ALP-encoded value column begins with
// a known scheme byte (0=main, 1=RD, 2=raw; see internal/encoding/
// numeric_alp.go's ALPMaxSchemeByte) and that the column's body is at least
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
func validateALPColumns(valPayload []byte, indexEntries []section.NumericIndexEntry, engine endian.EndianEngine) error {
	for i := range indexEntries {
		entry := &indexEntries[i]
		if entry.ValueLength == 0 {
			continue
		}

		column := valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength]
		scheme := column[0]
		if scheme > ienc.ALPMaxSchemeByte {
			return fmt.Errorf("%w: metric ID %d has ALP scheme byte %d, want 0 (main), 1 (rd), or 2 (raw)",
				errs.ErrInvalidALPScheme, entry.MetricID, scheme)
		}

		body := column[1:]
		count := entry.Count

		// Scheme byte values below mirror the unexported alpSchemeMain (0),
		// alpSchemeRD (1), alpSchemeRaw (2) constants in
		// internal/encoding/numeric_alp.go — already range-checked against
		// ALPMaxSchemeByte above, so this switch is exhaustive.
		switch scheme {
		case 0: // alpSchemeMain
			const mainHeaderSize = 15
			if len(body) < mainHeaderSize {
				return fmt.Errorf("%w: metric ID %d has ALP main column body of %d bytes, want at least %d (fixed header)",
					errs.ErrInvalidALPColumn, entry.MetricID, len(body), mainHeaderSize)
			}

			width := int(body[2])
			nExc := int(engine.Uint32(body[3:7]))
			want := mainHeaderSize + (count*width+7)/8 + nExc*12
			if len(body) < want {
				return fmt.Errorf("%w: metric ID %d has ALP main column body of %d bytes, want at least %d (width=%d, nExc=%d, count=%d)",
					errs.ErrInvalidALPColumn, entry.MetricID, len(body), want, width, nExc, count)
			}
		case 1: // alpSchemeRD
			const rdHeaderSize = 7
			if len(body) < rdHeaderSize {
				return fmt.Errorf("%w: metric ID %d has ALP rd column body of %d bytes, want at least %d (fixed header)",
					errs.ErrInvalidALPColumn, entry.MetricID, len(body), rdHeaderSize)
			}

			rbw := int(body[0])
			codeBits := int(body[1])
			nDict := int(body[2])
			nExc := int(engine.Uint32(body[3:7]))
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

			want := rdHeaderSize + nDict*2 + (count*codeBits+7)/8 + (count*rbw+7)/8 + nExc*6
			if len(body) < want {
				return fmt.Errorf("%w: metric ID %d has ALP rd column body of %d bytes, want at least %d (rbw=%d, codeBits=%d, nDict=%d, nExc=%d, count=%d)",
					errs.ErrInvalidALPColumn, entry.MetricID, len(body), want, rbw, codeBits, nDict, nExc, count)
			}
		case 2: // alpSchemeRaw
			want := 1 + count*8
			if len(column) < want {
				return fmt.Errorf("%w: metric ID %d has ALP raw column of %d bytes, want at least %d (count=%d)",
					errs.ErrInvalidALPColumn, entry.MetricID, len(column), want, count)
			}
		default:
			// Unreachable: scheme was already range-checked against
			// ALPMaxSchemeByte above, so it is always 0, 1, or 2 here.
		}
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

// buildSharedTsCache pre-decodes timestamps for offsets shared by multiple metrics.
// This avoids redundant decoding when iterating timestamps across many metrics
// that share the same underlying timestamp data.
func (d *NumericDecoder) buildSharedTsCache(blob *NumericBlob, indexEntries []section.NumericIndexEntry) {
	// Count how many metrics reference each TimestampOffset
	refCount := make(map[int]int, d.metricCount)
	for i := range indexEntries[:d.metricCount] {
		refCount[indexEntries[i].TimestampOffset]++
	}

	// Pre-decode only offsets used by more than one metric
	cache := make(map[int][]int64)
	for i := range indexEntries[:d.metricCount] {
		entry := &indexEntries[i]
		if refCount[entry.TimestampOffset] <= 1 {
			continue
		}
		if _, exists := cache[entry.TimestampOffset]; exists {
			continue
		}

		tsBytes := blob.tsPayload[entry.TimestampOffset : entry.TimestampOffset+entry.TimestampLength]
		decoded := make([]int64, entry.Count)
		produced := blob.decodeTimestampsSlice(tsBytes, entry.Count, decoded)
		cache[entry.TimestampOffset] = decoded[:produced]
	}

	if len(cache) > 0 {
		blob.sharedTsCache = cache
	}
}
