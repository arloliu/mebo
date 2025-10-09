package blob

import (
	"fmt"
	"time"

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
	blob := NumericBlob{
		blobBase: blobBase{
			engine:        d.engine,
			startTime:     time.UnixMicro(d.header.StartTime).UTC(),
			sameByteOrder: endian.CompareNativeEndian(d.engine),
			tsEncType:     d.header.Flag.TimestampEncoding(),
		},
		flag:       d.header.Flag,
		valEncType: d.header.Flag.ValueEncoding(),
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
	indexEntries, metricIDs, err := d.parseIndexEntries(indexOffset, len(blob.tsPayload), len(blob.valPayload), len(blob.tagPayload))
	if err != nil {
		return blob, err
	}

	// Step 4: Build index entry map
	blob.index.byID = make(map[uint64]section.NumericIndexEntry, d.metricCount)
	for _, entry := range indexEntries {
		blob.index.byID[entry.MetricID] = entry
	}

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
func (d *NumericDecoder) parseIndexEntries(indexOffset, tsPayloadSize, valPayloadSize, tagPayloadSize int) ([]section.NumericIndexEntry, []uint64, error) {
	indexSize := section.NumericIndexEntrySize * d.metricCount
	if len(d.data) < indexOffset+indexSize {
		return nil, nil, errs.ErrInvalidIndexEntrySize
	}

	indexData := d.data[indexOffset : indexOffset+indexSize]
	// Use int for accumulated offsets to prevent uint16 overflow
	// Index entries store deltas as uint16, but absolute offsets can exceed 65535
	var lastTsOffset int
	var lastValOffset int
	var lastTagOffset int

	// Pre-allocate slices with exact size for better performance
	// Direct indexing eliminates bounds checking on each append operation
	indexEntries := make([]section.NumericIndexEntry, d.metricCount)
	metricIDs := make([]uint64, d.metricCount)

	var err error
	for i := 0; i < d.metricCount; i++ {
		start := i * section.NumericIndexEntrySize
		end := start + section.NumericIndexEntrySize

		indexEntries[i], err = section.ParseNumericIndexEntry(indexData[start:end], d.engine)
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

		metricIDs[i] = curEntry.MetricID

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
