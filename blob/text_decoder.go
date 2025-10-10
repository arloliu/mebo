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
}

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
			startTimeMicros: d.header.StartTime, // Direct int64 assignment (optimized)
			sameByteOrder:   endian.CompareNativeEndian(d.engine),
			tsEncType:       d.header.Flag.GetTimestampEncoding(),
			endianType: func() uint8 {
				if d.header.Flag.IsBigEndian() {
					return 1
				}

				return 0
			}(), // 0=little, 1=big
			flags: flags, // Packed flags (optimized)
		},
	}

	// Validate payload offsets
	dataOffset := int(d.header.DataOffset)
	if len(d.data) < dataOffset {
		return blob, fmt.Errorf("data offset %d exceeds data length %d", dataOffset, len(d.data))
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

	// Step 3: Build index entry map
	blob.index.byID = make(map[uint64]section.TextIndexEntry, d.metricCount)
	for _, entry := range indexEntries {
		blob.index.byID[entry.MetricID] = entry
	}

	// Step 4: Verify and populate metric name map (if metric names present)
	if len(metricNames) > 0 {
		if err := ienc.VerifyMetricNamesHashes(metricNames, metricIDs, hash.ID); err != nil {
			return blob, fmt.Errorf("metric name verification failed: %w", err)
		}

		// Populate metric name map for ByName lookups
		// metricNames[i] corresponds to indexEntries[i] (consistent ordering)
		blob.index.byName = make(map[string]section.TextIndexEntry, d.metricCount)
		for i, name := range metricNames {
			blob.index.byName[name] = indexEntries[i]
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

	metricNames, bytesRead, err := ienc.DecodeMetricNames(d.data[section.HeaderSize:], d.engine)
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
	expectedIndexSize := d.metricCount * section.TextIndexEntrySize
	endOffset := startOffset + expectedIndexSize

	if len(d.data) < endOffset {
		return nil, nil, fmt.Errorf("insufficient data for index entries: need %d bytes, have %d",
			expectedIndexSize, len(d.data)-startOffset)
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
			// Last metric: size = total decompressed size - offset
			entry.Size = d.header.DataSize - entry.Offset
		}
		// Not implemented yet - need to read next entry's offset first
		// Will be calculated after all entries are parsed

		indexEntries[i] = entry
		metricIDs[i] = entry.MetricID
	}

	// Calculate sizes for all entries except the last one
	for i := 0; i < d.metricCount-1; i++ {
		indexEntries[i].Size = indexEntries[i+1].Offset - indexEntries[i].Offset
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
		return nil, fmt.Errorf("decompressed data size mismatch: expected %d, got %d",
			d.header.DataSize, len(decompressedData))
	}

	return decompressedData, nil
}
