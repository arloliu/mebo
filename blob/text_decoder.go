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

// TextDecoder decodes the encoded text blob data and reconstructs a TextBlob.
//
// The decoder handles:
//   - Header parsing with validation
//   - Metric names payload (when present)
//   - Index entries with offset calculations
//   - Data section decompression
//   - Metric name hash verification
//
// Note: The TextDecoder is NOT thread-safe. Each decoder instance should be used by a single goroutine at a time.
//
// Note: The TextDecoder is NOT reusable. After calling Decode, a new decoder must be created for further decoding.
type TextDecoder struct {
	data        []byte
	metricCount int
	engine      endian.EndianEngine
	header      *section.TextHeader
	// borrowNames selects the zero-copy metric-names decode. When true the
	// decoded blob's names alias `data`; the default (false) copies them.
	borrowNames bool
}

// Compile-time assertions that pin the existing constructor function types.
// Code stores these constructors in typed function variables (mebo.go), which
// API_STABILITY.md forbids breaking. If a future change alters either signature,
// these fail to compile and force an explicit, documented decision.
var (
	_ func([]byte) (*TextDecoder, error) = NewTextDecoder
	_ func([]byte) (*TextDecoder, error) = NewTextDecoderBorrowed
)

// NewTextDecoder creates a new TextDecoder for the given encoded data.
//
// The decoder validates the header and prepares for decoding but does not decompress
// the data section until Decode() is called.
//
// Parameters:
//   - data: Encoded blob byte slice (must contain valid header)
//
// Returns:
//   - *TextDecoder: New decoder instance ready for decoding
//   - error: Header parsing error or invalid data format
func NewTextDecoder(data []byte) (*TextDecoder, error) {
	decoder := &TextDecoder{
		data: data,
	}

	if err := decoder.parseHeader(); err != nil {
		return nil, err
	}

	return decoder, nil
}

// NewTextDecoderBorrowed creates a TextDecoder that decodes metric names with
// ZERO COPY: the resulting blob's names alias the input `data` buffer instead of
// owning independent copies. This removes the per-name string allocations
// that otherwise dominate the names-bearing decode cost.
//
// Lifetime rule: the backing array of `data` MUST NOT be mutated or reused while
// the decoded blob (or anything derived directly from its names) is live. Doing so
// corrupts the blob's metric names. Use NewTextDecoder for the owning (copying)
// behaviour when the caller cannot guarantee that.
//
// Materialising a blob decoded this way CLONES the names, so materialized objects
// are always owning and the borrowed-lifetime rule never propagates past the blob
// itself.
//
// Everything else is identical to NewTextDecoder.
//
// Parameters:
//   - data: Encoded blob byte slice (must contain valid header). Its backing
//     array is borrowed by the returned decoder's metric names — see the
//     lifetime rule above.
//
// Returns:
//   - *TextDecoder: New decoder instance whose metric names alias data, ready
//     for decoding
//   - error: Header parsing error or invalid data format
func NewTextDecoderBorrowed(data []byte) (*TextDecoder, error) {
	decoder := &TextDecoder{
		data:        data,
		borrowNames: true,
	}

	if err := decoder.parseHeader(); err != nil {
		return nil, err
	}

	return decoder, nil
}

// Decode decodes the encoded data into a TextBlob.
//
// This method decompresses the data section, parses index entries, and reconstructs the blob
// structure. If metric names are present, it verifies name hashes and builds the name index.
//
// Returns:
//   - TextBlob: Decoded blob with data payload and index maps
//   - error: Payload offset validation errors, decompression errors, index parsing errors,
//     or metric name verification failures
func (d *TextDecoder) Decode() (TextBlob, error) {
	// Pack flags into single uint16 for size optimization
	var flags uint16
	if d.header.Flag.IsBigEndian() {
		flags |= section.FlagEndianLittleEndian // 1=big endian
	}
	if d.header.Flag.GetTimestampEncoding() == format.TypeRaw {
		flags |= section.FlagTsEncRaw
	}
	if d.header.Flag.HasTag() {
		flags |= section.FlagTagEnabled
	}
	if d.header.Flag.HasMetricNames() {
		flags |= section.FlagMetricNames
	}

	blob := TextBlob{
		blobBase: blobBase{
			tsEncType:     d.header.Flag.GetTimestampEncoding(),
			flags:         flags, // Packed flags (optimized)
			formatVersion: blobFormatV1,
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
	dataOffset := int(d.header.DataOffset)
	if len(d.data) < dataOffset {
		return blob, fmt.Errorf("%w: data offset %d exceeds data length %d", errs.ErrInvalidTimestampPayloadOffset, dataOffset, len(d.data))
	}

	// Step 1: Parse metric names (if present)
	metricNames, indexOffset, err := d.parseMetricNames()
	if err != nil {
		return blob, err
	}

	// Step 2: Parse index entries
	indexEntries, metricIDs, err := d.parseIndexEntries(indexOffset)
	if err != nil {
		return blob, err
	}

	// Step 3: Build index entry representation. Text is always V1-style (wire
	// order is NOT MetricID-sorted), so use a first-wins MetricID→ordinal map
	// over the ordered entries.
	blob.index.sorted = indexEntries
	byID := make(map[uint64]int, d.metricCount)
	for i := range indexEntries {
		id := indexEntries[i].MetricID
		if _, exists := byID[id]; !exists {
			byID[id] = i
		}
	}
	blob.index.byID = byID

	// Step 4: Verify names, reject a blob that stores the same name twice, and
	// finalize the name representation (retain ordered names; build byName
	// only on collision).
	if len(metricNames) > 0 {
		if err := ienc.VerifyMetricNamesHashes(metricNames, metricIDs, hash.ID); err != nil {
			return blob, fmt.Errorf("metric name verification failed: %w", err)
		}

		// metricNames[i] corresponds to indexEntries[i] (consistent ordering).
		if err := blob.index.finalizeNames(metricNames, d.borrowNames); err != nil {
			return blob, err
		}
	}

	// Step 5: Decompress data payload
	dataPayload, err := d.decompressData(dataOffset)
	if err != nil {
		return blob, err
	}

	blob.dataPayload = dataPayload

	return blob, nil
}

// parseHeader parses the header section of the encoded data.
func (d *TextDecoder) parseHeader() error {
	if len(d.data) < section.HeaderSize {
		return errs.ErrInvalidHeaderSize
	}

	var header section.TextHeader
	if err := header.Parse(d.data[:section.HeaderSize]); err != nil {
		return err
	}

	d.engine = header.GetEndianEngine()
	d.metricCount = int(header.MetricCount)
	d.header = &header

	return nil
}

// parseMetricNames decodes the metric names payload if present.
// Returns the metric names slice and the byte offset where the index section starts.
func (d *TextDecoder) parseMetricNames() ([]string, int, error) {
	if !d.header.Flag.HasMetricNames() {
		return nil, section.HeaderSize, nil
	}

	decodeNames := ienc.DecodeMetricNames
	if d.borrowNames {
		decodeNames = ienc.DecodeMetricNamesBorrowed
	}

	metricNames, bytesRead, err := decodeNames(d.data[section.HeaderSize:], d.engine)
	if err != nil {
		return nil, 0, err
	}

	if len(metricNames) != d.metricCount {
		return nil, 0, fmt.Errorf("%w: expected %d metric names, got %d",
			errs.ErrInvalidMetricNamesCount, d.metricCount, len(metricNames))
	}

	indexOffset := section.HeaderSize + bytesRead

	return metricNames, indexOffset, nil
}

// parseIndexEntries parses the index section starting at the given offset.
// Returns the index entries and metric IDs in the same order.
func (d *TextDecoder) parseIndexEntries(startOffset int) ([]section.TextIndexEntry, []uint64, error) {
	// Compare counts before multiplying: on 32-bit platforms a crafted
	// MetricCount times the entry size can wrap.
	if startOffset > len(d.data) || d.metricCount > (len(d.data)-startOffset)/section.TextIndexEntrySize {
		return nil, nil, fmt.Errorf("%w: %d entries do not fit %d index bytes",
			errs.ErrInvalidIndexEntrySize, d.metricCount, len(d.data)-min(startOffset, len(d.data)))
	}

	expectedIndexSize := d.metricCount * section.TextIndexEntrySize
	endOffset := startOffset + expectedIndexSize

	// The data section must start at or after the end of the index.
	if endOffset > int(d.header.DataOffset) {
		return nil, nil, fmt.Errorf("%w: data section at %d overlaps the index ending at %d",
			errs.ErrInvalidTimestampPayloadOffset, d.header.DataOffset, endOffset)
	}

	if len(d.data) < endOffset {
		return nil, nil, fmt.Errorf("%w: need %d bytes, have %d",
			errs.ErrInvalidIndexEntrySize, expectedIndexSize, len(d.data)-startOffset)
	}

	indexEntries := make([]section.TextIndexEntry, d.metricCount)
	metricIDs := make([]uint64, d.metricCount)

	for i := 0; i < d.metricCount; i++ {
		offset := startOffset + i*section.TextIndexEntrySize
		entry, err := section.ParseTextIndexEntry(d.data[offset:offset+section.TextIndexEntrySize], d.engine)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse index entry %d: %w", i, err)
		}

		// Calculate size from offset differences
		// For last entry, use total data size from header
		if i == d.metricCount-1 {
			// Validate last entry offset doesn't exceed data size (prevents uint32 underflow)
			if entry.Offset > d.header.DataSize {
				return nil, nil, fmt.Errorf("%w: last entry offset %d exceeds data size %d",
					errs.ErrInvalidIndexOffsets, entry.Offset, d.header.DataSize)
			}

			entry.Size = d.header.DataSize - entry.Offset
		}

		indexEntries[i] = entry
		metricIDs[i] = entry.MetricID
	}

	// Calculate sizes for all entries except the last one
	for i := 0; i < d.metricCount-1; i++ {
		// Validate monotonic offsets (prevents uint32 underflow in size calculation)
		if indexEntries[i+1].Offset < indexEntries[i].Offset {
			return nil, nil, fmt.Errorf("%w: entry %d offset %d > entry %d offset %d",
				errs.ErrInvalidIndexOffsets, i, indexEntries[i].Offset, i+1, indexEntries[i+1].Offset)
		}

		indexEntries[i].Size = indexEntries[i+1].Offset - indexEntries[i].Offset
	}

	// Every point takes at least its timestamp (one varint byte, or nine raw
	// bytes) and a value-length byte, plus a tag-length byte with tags. A larger
	// Count can only come from a corrupt index and would size allocations.
	minPointSize := uint64(2)
	if d.header.Flag.GetTimestampEncoding() == format.TypeRaw {
		minPointSize = 10
	}
	if d.header.Flag.HasTag() {
		minPointSize++
	}
	for i := range indexEntries {
		if uint64(indexEntries[i].Count)*minPointSize > uint64(indexEntries[i].Size) {
			return nil, nil, fmt.Errorf("%w: entry %d has count %d but %d data bytes",
				errs.ErrInvalidNumOfDataPoints, i, indexEntries[i].Count, indexEntries[i].Size)
		}
	}

	return indexEntries, metricIDs, nil
}

// decompressData decompresses the data payload if compression is enabled.
func (d *TextDecoder) decompressData(dataOffset int) ([]byte, error) {
	compressionType := d.header.Flag.GetDataCompression()

	// If no compression, return the raw data section
	if compressionType == 0 {
		return d.data[dataOffset:], nil
	}

	// Get compressed data
	compressedData := d.data[dataOffset:]

	// Create codec and decompress
	codec, err := compress.CreateCodec(compressionType, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create decompression codec: %w", err)
	}

	decompressedData, err := codec.Decompress(compressedData)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress data: %w", err)
	}

	// Verify decompressed size matches header
	// DataSize stores the uncompressed (decompressed) size
	if uint32(len(decompressedData)) != d.header.DataSize { //nolint:gosec
		return nil, fmt.Errorf("%w: expected %d, got %d",
			errs.ErrDataSizeMismatch, d.header.DataSize, len(decompressedData))
	}

	return decompressedData, nil
}
