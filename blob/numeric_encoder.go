package blob

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/cespare/xxhash/v2"

	"github.com/arloliu/mebo/encoding"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/collision"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/internal/options"
	"github.com/arloliu/mebo/internal/pool"
	"github.com/arloliu/mebo/section"
)

// metricIdentifierMode defines how metrics are identified in the encoder.
// Once the first metric is added, the mode is locked for the entire encoder lifecycle.
type metricIdentifierMode uint8

const (
	// modeUndefined indicates no metrics have been added yet, mode not determined.
	modeUndefined metricIdentifierMode = iota

	// modeUserID indicates user provides metric IDs via StartMetricID().
	// In this mode:
	// - User is responsible for providing unique IDs
	// - No collision handling (duplicate IDs return errors)
	// - No metric names tracking (performance optimization)
	// - No metric names payload in blob
	modeUserID

	// modeNameManaged indicates mebo manages metric IDs via StartMetricName().
	// In this mode:
	// - Metric names are hashed to IDs automatically
	// - Collision detection and handling enabled
	// - Metric names payload included if collision detected
	// - Full collision tracker allocated
	modeNameManaged

	// maxCachedSliceSize is the maximum size of cached slices for AddFromRows operations.
	// This prevents excessive memory usage when processing very large metrics by batching
	// the data transformation in chunks. The value is chosen to balance performance and
	// memory efficiency:
	// - 512 × 8 bytes = 4096 bytes for int64/float64 slices (one memory page)
	// - 512 × 16 bytes ≈ 8192 bytes for string slices (typical string header size)
	// Metrics with more than 512 data points will be processed in multiple batches.
	maxCachedSliceSize = 512
)

// NumericEncoder encodes float values into the binary blob format.
//
// It supports various encoding and compression schemes for both timestamps and values,
// as well as different endianness options.
//
// Note: The NumericEncoder is NOT thread-safe. Each encoder instance should be used by a single goroutine at a time.
//
// Note: The NumericEncoder is NOT reusable. After calling Finish, a new encoder must be created for further encoding.
type NumericEncoder struct {
	*NumericEncoderConfig

	valEncoder encoding.ColumnarEncoder[float64]
	tsEncoder  encoding.ColumnarEncoder[int64]
	tagEncoder encoding.ColumnarEncoder[string]

	curMetricID uint64 // current metric ID being encoded
	claimed     int    // number of data points claimed for the current metric

	// Encoder state tracking: groups related fields for better cache locality.
	// Each encoderState (24 bytes) keeps related fields together (lastOffset, offset, length),
	// ensuring they're loaded in a single cache line access.
	// The three states are sequential (72 bytes total), making them prefetcher-friendly when
	// accessed in sequence during EndMetric()'s hot path.
	ts  encoderState // timestamp encoder state (24 bytes)
	val encoderState // value encoder state (24 bytes)
	tag encoderState // tag encoder state (24 bytes)

	// Collision detection - mode-specific optimization:
	// - ID mode (modeUserID): Only usedIDs is allocated for simple duplicate detection
	// - Name mode (modeNameManaged): Only collisionTracker is allocated for full collision handling
	// This avoids unnecessary memory allocation and improves performance in ID mode.
	collisionTracker *collision.Tracker  // Tracks metric names (Name mode only)
	usedIDs          map[uint64]struct{} // Tracks IDs for duplicates (ID mode only)

	// Mode tracking - determines identifier strategy (ID vs Name)
	identifierMode metricIdentifierMode // Locked after first StartMetric call

	// Header immutability - track pending changes to apply in Finish()
	// This keeps the original header immutable for future stateless encoder pattern
	hasCollision    bool // Set when hash collision detected, applied to cloned header in Finish()
	hasNonEmptyTags bool // Set when any non-empty tag is written, used to optimize empty-tag-only blobs

	// Reusable slices for AddFromRows - cached across multiple metrics to reduce pool overhead
	// These slices are:
	// - Allocated lazily on first AddFromRows call
	// - Reused across multiple metrics (grown if needed)
	// - Returned to pool only once in Finish()
	cachedTimestamps []int64
	cachedValues     []float64
	cachedTags       []string
	// Cleanup functions for returning slices to pool
	cleanupTS  func()
	cleanupVal func()
	cleanupTag func()
}

// tsGroup represents a group of metrics sharing identical timestamp sequences.
type tsGroup struct {
	canonicalIdx int   // index in indexEntries of the canonical metric
	sharedIdxs   []int // indices of metrics sharing the canonical's timestamps
}

// encoderState tracks offset and length state for a single encoder (timestamp, value, or tag).
// This struct is small enough to be a cache-friendly struct (24 bytes) that can be inlined by the Go compiler.
type encoderState struct {
	lastOffset int // offset of last metric end (for delta calculation in index entries)
	offset     int // current offset in uncompressed data (byte position where current metric starts)
	length     int // total count of items encoded so far (accumulated across all metrics)
}

// delta returns the offset delta from the last metric.
// This is used to calculate index entry offset deltas.
func (s *encoderState) delta() int {
	return s.offset - s.lastOffset
}

// updateLast updates lastOffset to current offset after ending a metric.
func (s *encoderState) updateLast() {
	s.lastOffset = s.offset
}

// update updates the state with new offset and length values.
func (s *encoderState) update(offset int, length int) {
	s.offset = offset
	s.length = length
}

// cloneHeader creates a shallow copy of the encoder's header for immutability.
// This allows Finish() to compute final header fields without mutating the original.
// The 32-byte copy cost is negligible compared to compression/encoding work.
func (e *NumericEncoder) cloneHeader() *section.NumericHeader {
	cloned := *e.header // Shallow copy (32 bytes)
	return &cloned
}

// MaxDataPoints returns the maximum number of data points a single metric can hold
// safely without overflowing the 16-bit offset index. This limit depends on the
// uncompressed byte size of the chosen encoding combinations.
func (e *NumericEncoder) MaxDataPoints() int {
	tsEnc := e.header.Flag.TimestampEncoding()
	tsBytes := 9   // Default safe worst-case for delta varints
	switch tsEnc { //nolint:exhaustive // other enum values use the default fallback
	case format.TypeRaw:
		tsBytes = 8
	case format.TypeDeltaPacked:
		tsBytes = 5 // Max 17 bytes per 4 values -> ~4.25
	default:
		// tsBytes already initialized to 9
	}

	valEnc := e.header.Flag.ValueEncoding()
	valBytes := 9 // Gorilla/Chimp worst case is ~65-69 bits (~9 bytes)
	if valEnc == format.TypeRaw {
		valBytes = 8
	}

	maxBytes := max(tsBytes, valBytes)

	return section.NumericMaxOffset / maxBytes
}

// NewNumericEncoder creates a new NumericEncoder with the given start time.
//
// The encoder will grow dynamically as metrics are added, up to MaxMetricCount (65536).
//
// Parameters:
//   - blobTS: Timestamp for the entire blob, used as sorting key for all blobs in the same series
//   - opts: Optional encoding configuration (endianness, compression, encoding types, etc.)
//
// Returns:
//   - *NumericEncoder: New encoder instance ready for metric encoding
//   - error: Configuration error if invalid options provided
func NewNumericEncoder(blobTS time.Time, opts ...NumericEncoderOption) (*NumericEncoder, error) {
	// Create base configuration
	config := NewNumericEncoderConfig(blobTS)

	encoder := &NumericEncoder{
		NumericEncoderConfig: config,
		identifierMode:       modeUndefined,
		collisionTracker:     nil, // lazy creation
		usedIDs:              nil, // lazy creation
	}

	// Apply options to base config
	if err := options.Apply(config, opts...); err != nil {
		return nil, err
	}

	enc := encoder.header.Flag.ValueEncoding()
	switch enc { //nolint:exhaustive // TypeDeltaPacked is timestamp-only encoding
	case format.TypeRaw:
		encoder.valEncoder = ienc.NewNumericRawEncoder(encoder.engine)
	case format.TypeGorilla:
		encoder.valEncoder = ienc.NewNumericGorillaEncoder()
	case format.TypeChimp:
		encoder.valEncoder = ienc.NewNumericChimpEncoder()
	case format.TypeDelta:
		return nil, fmt.Errorf("%w: value encoding %s not supported yet", errs.ErrUnsupportedEncoding, enc.String())
	default:
		return nil, fmt.Errorf("%w: invalid value encoding %s", errs.ErrUnsupportedEncoding, enc.String())
	}

	enc = encoder.header.Flag.TimestampEncoding()
	switch enc { //nolint: exhaustive
	case format.TypeRaw:
		encoder.tsEncoder = ienc.NewTimestampRawEncoder(encoder.engine)
	case format.TypeDelta:
		encoder.tsEncoder = ienc.NewTimestampDeltaEncoder()
	case format.TypeDeltaPacked:
		encoder.tsEncoder = ienc.NewTimestampDeltaPackedEncoder()
	default:
		return nil, fmt.Errorf("%w: invalid timestamp encoding %s", errs.ErrUnsupportedEncoding, enc.String())
	}

	encoder.tagEncoder = ienc.NewTagEncoder(encoder.engine)

	if err := encoder.setCodecs(*encoder.header); err != nil {
		return nil, err
	}

	return encoder, nil
}

// StartMetricID begins encoding a new metric with the specified unique identifier and number of data points.
//
// The metricID should be a unique unsigned 64-bit integer. If the application does not have
// a predefined metric ID, it can use the hash.ID function to hash the metric name string.
//
// This method is exclusive with StartMetricName. Once StartMetricID is called, all subsequent
// metrics must also use StartMetricID. Attempting to mix with StartMetricName will return
// ErrMixedIdentifierMode.
//
// In ID mode, the encoder uses optimized duplicate detection (no collision tracker overhead).
//
// Parameters:
//   - metricID: Unique 64-bit metric identifier (must be non-zero)
//   - numOfDataPoints: Expected number of data points (1-65535)
//
// Returns:
//   - error: ErrMetricAlreadyStarted, ErrMixedIdentifierMode, ErrInvalidMetricID,
//     ErrInvalidNumOfDataPoints, ErrMetricCountExceeded, or ErrHashCollision on duplicate ID
func (e *NumericEncoder) StartMetricID(metricID uint64, numOfDataPoints int) error {
	if e.curMetricID != 0 {
		return fmt.Errorf("%w: metric ID %d is already started", errs.ErrMetricAlreadyStarted, e.curMetricID)
	}

	// Check mode exclusivity - cannot mix ID mode with Name mode
	if e.identifierMode == modeNameManaged {
		return fmt.Errorf("%w: cannot use StartMetricID after StartMetricName", errs.ErrMixedIdentifierMode)
	}

	// Set mode on first use
	if e.identifierMode == modeUndefined {
		e.identifierMode = modeUserID
	}

	if metricID == 0 {
		return errs.ErrInvalidMetricID
	}

	maxPoints := e.MaxDataPoints()
	if numOfDataPoints <= 0 || numOfDataPoints > maxPoints {
		return fmt.Errorf("%w: max %d for current encoding", errs.ErrInvalidNumOfDataPoints, maxPoints)
	}

	if len(e.indexEntries) >= MaxMetricCount {
		return fmt.Errorf("%w: metric count exceeded: max %d", errs.ErrMetricCountExceeded, MaxMetricCount)
	}

	// In ID mode, use simple map for duplicate detection, is much lighter than collision tracker
	if e.usedIDs == nil {
		e.usedIDs = make(map[uint64]struct{})
	}

	if _, exists := e.usedIDs[metricID]; exists {
		return fmt.Errorf("%w: metric ID 0x%016x already used", errs.ErrHashCollision, metricID)
	}
	e.usedIDs[metricID] = struct{}{}

	return e.startMetric(metricID, numOfDataPoints)
}

// startMetric is the internal method that actually starts a metric.
// It does NOT do collision checking - caller is responsible for that.
func (e *NumericEncoder) startMetric(metricID uint64, numOfDataPoints int) error {
	// Capture current encoder state
	e.ts.update(e.tsEncoder.Size(), e.tsEncoder.Len())
	e.val.update(e.valEncoder.Size(), e.valEncoder.Len())
	e.tag.update(e.tagEncoder.Size(), e.tagEncoder.Len())

	// Set current metric state
	e.curMetricID = metricID
	e.claimed = numOfDataPoints

	return nil
}

// StartMetricName begins encoding a new metric with the specified name and number of data points.
//
// The metric name string will be hashed to an unsigned 64-bit integer using xxHash64.
// This method performs collision detection by tracking all metric names added to the blob.
// If a hash collision is detected (different name, same hash), it automatically enables
// the metric names payload to handle the collision. This is NOT an error - mebo can
// handle collisions when metric names are available.
//
// If the application already has a unique metric ID, it should use StartMetricID instead
// to avoid the overhead of hashing and collision detection.
//
// This method is exclusive with StartMetricID. Once StartMetricID is called, all subsequent
// metrics must also use StartMetricName. Attempting to mix with StartMetricID will return
// ErrMixedIdentifierMode.
//
// Parameters:
//   - metricName: Metric name string (must be non-empty)
//   - numOfDataPoints: Expected number of data points (1-65535)
//
// Returns:
//   - error: ErrMetricAlreadyStarted, ErrMixedIdentifierMode, ErrInvalidMetricName,
//     ErrInvalidNumOfDataPoints, or ErrMetricCountExceeded
func (e *NumericEncoder) StartMetricName(metricName string, numOfDataPoints int) error {
	if e.curMetricID != 0 {
		return fmt.Errorf("%w: metric ID %d is already started", errs.ErrMetricAlreadyStarted, e.curMetricID)
	}

	// Check mode exclusivity - cannot mix Name mode with ID mode
	if e.identifierMode == modeUserID {
		return fmt.Errorf("%w: cannot use StartMetricName after StartMetricID", errs.ErrMixedIdentifierMode)
	}

	// Set mode on first use and create collision tracker (LAZY)
	if e.identifierMode == modeUndefined {
		e.identifierMode = modeNameManaged
		e.collisionTracker = collision.NewTracker()
	}

	maxPoints := e.MaxDataPoints()
	if numOfDataPoints <= 0 || numOfDataPoints > maxPoints {
		return fmt.Errorf("%w: max %d for current encoding", errs.ErrInvalidNumOfDataPoints, maxPoints)
	}

	if len(e.indexEntries) >= MaxMetricCount {
		return fmt.Errorf("%w: metric count exceeded: max %d", errs.ErrMetricCountExceeded, MaxMetricCount)
	}

	metricID := hash.ID(metricName)

	// Track metric and detect collisions using collision tracker
	err := e.collisionTracker.TrackMetric(metricName, metricID)
	if err != nil {
		// Only return error for duplicates and invalid names
		// Collisions are handled automatically
		return err
	}

	// If collision was detected, mark flag for later application in Finish()
	// This keeps the original header immutable
	if e.collisionTracker.HasCollision() {
		e.hasCollision = true
	}

	return e.startMetric(metricID, numOfDataPoints)
}

// EndMetric completes the encoding of the current metric and prepares the encoder for the next metric.
//
// This method should be called after all data points have been added via AddDataPoint or AddDataPoints.
// It validates data point counts, creates the index entry, and resets encoder state for the next metric.
//
// Returns:
//   - error: ErrNoMetricStarted, ErrNoDataPointsAdded, ErrDataPointCountMismatch,
//     or ErrOffsetOutOfRange if offset deltas exceed uint16 range
func (e *NumericEncoder) EndMetric() error {
	if e.curMetricID == 0 {
		return errs.ErrNoMetricStarted
	}

	// For bit-packed encodings (Gorilla, Chimp), we need to flush any pending bits
	// BEFORE calculating lengths. This ensures the length includes all flushed data.
	// For other encodings, this is a no-op as Bytes() just returns the buffer.
	valEnc := e.header.Flag.ValueEncoding()
	if valEnc == format.TypeGorilla || valEnc == format.TypeChimp {
		_ = e.valEncoder.Bytes() // Flush pending bits
	}

	// For Group Varint timestamp encoding (DeltaPacked), we need to flush any pending
	// partial group BEFORE calculating lengths. Without this, pending values would not
	// be included in Size() and would be lost on Reset().
	tsEnc := e.header.Flag.TimestampEncoding()
	if tsEnc == format.TypeDeltaPacked {
		_ = e.tsEncoder.Bytes() // Flush pending group
	}

	// Calculate lengths and byte size of newly added data points since the last metric was ended.
	// These are used for validation and index entry creation.
	tsEncLen := e.tsEncoder.Len()
	tsEncSize := e.tsEncoder.Size()
	valEncLen := e.valEncoder.Len()
	valEncSize := e.valEncoder.Size()

	// Only track tag lengths/offsets if tag support is enabled
	var tagEncLen, tagEncSize int
	if e.header.Flag.HasTag() {
		tagEncLen = e.tagEncoder.Len()
		tagEncSize = e.tagEncoder.Size()
	}

	// Calculate current metric's data point count
	curTsLen := tsEncLen - e.ts.length
	curValLen := valEncLen - e.val.length
	curTagLen := tagEncLen - e.tag.length

	if err := e.validateMetricData(curTsLen, curValLen, curTagLen); err != nil {
		return err
	}

	// Calculate offset deltas from last metric - uses encoderState.delta()
	tsOffsetDelta := e.ts.delta()
	valOffsetDelta := e.val.delta()
	tagOffsetDelta := e.tag.delta()

	// Validate offset deltas are within uint16 range BEFORE creating index entry
	if tsOffsetDelta > section.NumericMaxOffset ||
		valOffsetDelta > section.NumericMaxOffset ||
		tagOffsetDelta > section.NumericMaxOffset {
		return fmt.Errorf("%w: timestamp_delta=%d, value_delta=%d, tag_delta=%d (max=%d)",
			errs.ErrOffsetOutOfRange, tsOffsetDelta, valOffsetDelta, tagOffsetDelta, section.NumericMaxOffset)
	}

	// Create index entry and store validated offset deltas
	// The deltas are guaranteed to be <= NumericMaxOffset (65535) by validation above
	entry := section.NewNumericIndexEntry(e.curMetricID, uint16(curTsLen)) //nolint: gosec
	entry.TimestampOffset = tsOffsetDelta
	entry.ValueOffset = valOffsetDelta
	// Only set tag offset if tag support is enabled
	if e.header.Flag.HasTag() {
		entry.TagOffset = tagOffsetDelta
	}
	e.addEntryIndex(entry)

	// Update last offsets for next metric - uses encoderState.updateLast()
	e.ts.updateLast()
	e.val.updateLast()
	e.tag.updateLast()

	// Update accumulated state for next metric
	e.ts.update(tsEncSize, tsEncLen)
	e.val.update(valEncSize, valEncLen)
	e.tag.update(tagEncSize, tagEncLen)

	// Reset encoder internal states for next metric
	e.tsEncoder.Reset()
	e.valEncoder.Reset()
	e.tagEncoder.Reset()

	// Reset current metric state
	e.curMetricID = 0
	e.claimed = 0

	return nil
}

func (e *NumericEncoder) validateMetricData(curTsLen int, curValLen int, curTagLen int) error {
	// Ensure at least one data point was added
	if curTsLen == 0 || curValLen == 0 {
		return errs.ErrNoDataPointsAdded
	}

	// Ensure timestamp and value counts match
	if curTsLen != curValLen {
		return fmt.Errorf("%w: %d timestamps, %d values", errs.ErrDataPointCountMismatch, curTsLen, curValLen)
	}

	// Validate that exactly the claimed number of data points were added
	if curTsLen != e.claimed {
		return fmt.Errorf("%w: claimed %d, got %d", errs.ErrDataPointCountMismatch, e.claimed, curTsLen)
	}

	// Tag count must match data point count (tags can be empty strings) - only check if tags are enabled
	if e.header.Flag.HasTag() && curTagLen != e.claimed {
		return fmt.Errorf("%w: claimed %d, got %d tags", errs.ErrDataPointCountMismatch, e.claimed, curTagLen)
	}

	return nil
}

// Finish finalizes the encoding process and returns the complete byte slice representing all encoded metrics.
//
// This method compresses all payloads, builds the header with final offsets, assembles the index entries,
// and produces the complete binary blob. After calling Finish, the encoder cannot be reused and a new
// encoder must be created for further encoding.
//
// Returns:
//   - []byte: Complete encoded blob with header, index entries, and compressed payloads
//   - error: ErrMetricNotEnded if a metric was started but not ended, ErrNoMetricsAdded if no metrics
//     were added, or compression errors
func (e *NumericEncoder) Finish() ([]byte, error) {
	// Return cached slices to pool before any returns (including error paths)
	defer e.releasePooledSlices()

	// Finish encoders regardless of error to release resources
	defer e.tsEncoder.Finish()
	defer e.valEncoder.Finish()
	defer e.tagEncoder.Finish()

	if e.curMetricID != 0 {
		return nil, errs.ErrMetricNotEnded
	}

	// Validate at least one metric was added
	if len(e.indexEntries) == 0 {
		return nil, errs.ErrNoMetricsAdded
	}

	// Clone header to keep original immutable (preparation for stateless encoder pattern)
	// All computed fields will be set on the clone
	finalHeader := e.cloneHeader()

	// Apply pending collision flag if set
	if e.hasCollision {
		finalHeader.Flag.SetHasMetricNames(true)
	}

	// Dynamically disable tag support if no non-empty tags were written
	// This optimization saves space and decoding time when all tags are empty
	if finalHeader.Flag.HasTag() && !e.hasNonEmptyTags {
		finalHeader.Flag.WithoutTag()
		// Zero out stale TagOffset deltas stored in index entries so that the decoder's
		// non-decreasing offset validation does not reject the blob. These deltas were
		// accumulated during EndMetric() but now reference a tag payload that won't exist.
		for i := range e.indexEntries {
			e.indexEntries[i].TagOffset = 0
		}
	}

	// Set actual metric count in cloned header now that encoding is complete
	finalHeader.MetricCount = uint32(len(e.indexEntries)) //nolint: gosec

	// Set V2 magic number if layout version is V2
	if e.layoutVersion >= 2 {
		finalHeader.Flag.Options = (finalHeader.Flag.Options &^ section.MagicNumberMask) | section.MagicNumericV2Opt
	}

	// Sort index entries by MetricID for V2 layout.
	// This enables cache-friendly iteration and binary search lookups in the decoder.
	// Index entries store delta offsets referencing payload data in insertion order,
	// so we must also reorder all payload data to match sorted order.
	rawTsBytes := e.tsEncoder.Bytes()
	rawValBytes := e.valEncoder.Bytes()
	var rawTagBytes []byte
	if finalHeader.Flag.HasTag() {
		rawTagBytes = e.tagEncoder.Bytes()
	}

	if e.layoutVersion >= 2 && !e.sortedByMetricID {
		rawTsBytes, rawValBytes, rawTagBytes = e.sortEntriesByMetricID(rawTsBytes, rawValBytes, rawTagBytes)
	}

	// Detect shared timestamp groups and build dedup payload if opt-in enabled.
	var sharedTable section.SharedTimestampTable
	var sharedTableSize int

	rawTsBytes, sharedTable, sharedTableSize = e.maybeBuildSharedTimestampPayload(rawTsBytes)
	if sharedTableSize > 0 {
		// Set shared timestamps flag bit
		finalHeader.Flag.SetHasSharedTimestamps(true)
	}

	// Compress timestamp and value payloads
	tsPayload, err := e.tsCodec.Compress(rawTsBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to compress timestamp payload: %w", err)
	}

	valPayload, err := e.valCodec.Compress(rawValBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to compress value payload: %w", err)
	}

	// Only compress tag payload if tag support is enabled
	var tagPayload []byte
	if finalHeader.Flag.HasTag() {
		tagPayload, err = e.tagCodec.Compress(rawTagBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to compress tag payload: %w", err)
		}
	}

	// Encode metric names payload if collision was detected
	// In ID mode, collisionTracker is nil, so we skip this entirely
	var metricNamesPayload []byte
	if e.collisionTracker != nil && finalHeader.Flag.HasMetricNames() {
		metricNamesPayload, err = ienc.EncodeMetricNames(e.collisionTracker.GetMetricNames(), e.engine)
		if err != nil {
			return nil, fmt.Errorf("failed to encode metric names: %w", err)
		}
		// Update IndexOffset to account for metric names payload (positioned after header)
		finalHeader.IndexOffset = uint32(section.HeaderSize + len(metricNamesPayload)) //nolint: gosec
	}

	// Calculate TimestampPayloadOffset based on actual index entries count and shared table
	indexEntriesSize := section.NumericIndexEntrySize * len(e.indexEntries)
	finalHeader.TimestampPayloadOffset = finalHeader.IndexOffset + uint32(indexEntriesSize) + uint32(sharedTableSize) //nolint: gosec

	// Set value payload offset in header, it records the value payload offset after the timestamp payload.
	// The size of timestamp payload is the compressed size.
	finalHeader.ValuePayloadOffset = finalHeader.TimestampPayloadOffset + uint32(len(tsPayload)) //nolint: gosec

	// Set tag payload offset in header, it records the tag payload offset after the value payload.
	// The size of value payload is the compressed size.
	finalHeader.TagPayloadOffset = finalHeader.ValuePayloadOffset + uint32(len(valPayload)) //nolint: gosec

	// Pre-calculate exact size (reuse indexEntriesSize from above)
	blobSize := section.HeaderSize + len(metricNamesPayload) + indexEntriesSize + sharedTableSize + len(tsPayload) + len(valPayload) + len(tagPayload)

	// Allocate exact-size buffer for the final blob
	// No need for pooled buffer since we return this directly to caller
	blob := make([]byte, blobSize)
	offset := 0

	// Copy cloned header with all computed fields
	offset += copy(blob[offset:], finalHeader.Bytes())

	// Copy metric names payload (if present)
	if finalHeader.Flag.HasMetricNames() {
		offset += copy(blob[offset:], metricNamesPayload)
	}

	// Write index entries
	for i, entry := range e.indexEntries {
		entryOffset := offset + i*section.NumericIndexEntrySize
		entry.WriteToSlice(blob, entryOffset, e.engine)
	}
	offset += indexEntriesSize

	// Write shared timestamp table (if V2 with sharing)
	if sharedTableSize > 0 {
		offset = sharedTable.WriteToSlice(blob, offset, e.engine)
	}

	// Copy timestamp payload
	offset += copy(blob[offset:], tsPayload)

	// Copy value payload
	offset += copy(blob[offset:], valPayload)

	// Copy tag payload
	copy(blob[offset:], tagPayload)

	return blob, nil
}

// releasePooledSlices returns cached slices to their respective pools.
func (e *NumericEncoder) releasePooledSlices() {
	if e.cleanupTS != nil {
		e.cleanupTS()
		e.cleanupTS = nil
	}

	if e.cleanupVal != nil {
		e.cleanupVal()
		e.cleanupVal = nil
	}

	if e.cleanupTag != nil {
		e.cleanupTag()
		e.cleanupTag = nil
	}
}

// maybeBuildSharedTimestampPayload deduplicates shared timestamp sequences when enabled.
// It returns original bytes unchanged when no sharing is detected.
func (e *NumericEncoder) maybeBuildSharedTimestampPayload(rawTsBytes []byte) ([]byte, section.SharedTimestampTable, int) {
	if !e.sharedTimestamps {
		return rawTsBytes, section.SharedTimestampTable{}, 0
	}

	sharedGroups := e.detectSharedTimestamps(rawTsBytes)
	if len(sharedGroups) == 0 {
		return rawTsBytes, section.SharedTimestampTable{}, 0
	}

	dedupTsBytes, sharedTable := e.buildDedupTsPayload(rawTsBytes, sharedGroups)

	return dedupTsBytes, sharedTable, sharedTable.Size()
}

// sortEntriesByMetricID sorts index entries by MetricID for V2 layout and reorders
// all payload data to match. Returns reordered ts, val, and tag byte slices.
//
// Since index entries store delta offsets based on insertion order, we must:
//  1. Convert deltas to absolute offsets and compute segment lengths
//  2. Sort entries by MetricID
//  3. Reassemble payload bytes in sorted order
//  4. Recompute sequential delta offsets
func (e *NumericEncoder) sortEntriesByMetricID(rawTs, rawVal, rawTag []byte) (sortedTs, sortedVal, sortedTag []byte) {
	n := len(e.indexEntries)
	if n <= 1 {
		return rawTs, rawVal, rawTag
	}

	// Step 1: Convert deltas to absolute offsets
	type absEntry struct {
		tsOff, valOff, tagOff int
		tsLen, valLen, tagLen int
	}

	abs := make([]absEntry, n)
	var tsAcc, valAcc, tagAcc int
	for i := range n {
		tsAcc += e.indexEntries[i].TimestampOffset
		valAcc += e.indexEntries[i].ValueOffset
		tagAcc += e.indexEntries[i].TagOffset
		abs[i].tsOff = tsAcc
		abs[i].valOff = valAcc
		abs[i].tagOff = tagAcc
	}

	// Compute lengths from consecutive absolute offsets
	for i := range n {
		if i < n-1 {
			abs[i].tsLen = abs[i+1].tsOff - abs[i].tsOff
			abs[i].valLen = abs[i+1].valOff - abs[i].valOff
			abs[i].tagLen = abs[i+1].tagOff - abs[i].tagOff
		} else {
			abs[i].tsLen = len(rawTs) - abs[i].tsOff
			abs[i].valLen = len(rawVal) - abs[i].valOff
			if len(rawTag) > 0 {
				abs[i].tagLen = len(rawTag) - abs[i].tagOff
			}
		}
	}

	// Step 2: Sort entries (and abs offsets) together by MetricID
	// Build a permutation index, sort it, then apply
	perm := make([]int, n)
	for i := range n {
		perm[i] = i
	}
	slices.SortFunc(perm, func(a, b int) int {
		return cmp.Compare(e.indexEntries[a].MetricID, e.indexEntries[b].MetricID)
	})

	// Apply permutation to entries and abs
	sortedEntries := make([]section.NumericIndexEntry, n)
	sortedAbs := make([]absEntry, n)
	for i, orig := range perm {
		sortedEntries[i] = e.indexEntries[orig]
		sortedAbs[i] = abs[orig]
	}

	// Step 3: Reassemble payload bytes in sorted order
	newTs := make([]byte, len(rawTs))
	newVal := make([]byte, len(rawVal))
	var newTag []byte
	if len(rawTag) > 0 {
		newTag = make([]byte, len(rawTag))
	}

	var tsPos, valPos, tagPos int
	for i := range n {
		copy(newTs[tsPos:], rawTs[sortedAbs[i].tsOff:sortedAbs[i].tsOff+sortedAbs[i].tsLen])
		copy(newVal[valPos:], rawVal[sortedAbs[i].valOff:sortedAbs[i].valOff+sortedAbs[i].valLen])
		if len(rawTag) > 0 {
			copy(newTag[tagPos:], rawTag[sortedAbs[i].tagOff:sortedAbs[i].tagOff+sortedAbs[i].tagLen])
		}

		tsPos += sortedAbs[i].tsLen
		valPos += sortedAbs[i].valLen
		tagPos += sortedAbs[i].tagLen
	}

	// Step 4: Compute new sequential deltas.
	// Data is now contiguous in sorted order, so:
	//   entry[0].delta = 0 (start of payload)
	//   entry[i].delta = entry[i-1].length (each segment follows the previous)
	for i := range n {
		if i == 0 {
			sortedEntries[i].TimestampOffset = 0
			sortedEntries[i].ValueOffset = 0
			sortedEntries[i].TagOffset = 0
		} else {
			sortedEntries[i].TimestampOffset = sortedAbs[i-1].tsLen
			sortedEntries[i].ValueOffset = sortedAbs[i-1].valLen
			sortedEntries[i].TagOffset = sortedAbs[i-1].tagLen
		}
	}

	copy(e.indexEntries, sortedEntries)

	return newTs, newVal, newTag
}

// detectSharedTimestamps scans all metrics' encoded timestamp byte ranges and groups
// metrics with identical timestamp sequences. Returns nil if no sharing is detected.
//
// Parameters:
//   - allTsBytes: Complete encoded timestamp payload from tsEncoder.Bytes()
//
// Returns:
//   - []tsGroup: Groups with at least one shared metric, or nil if no sharing
func (e *NumericEncoder) detectSharedTimestamps(allTsBytes []byte) []tsGroup {
	metricCount := len(e.indexEntries)
	if metricCount < 2 {
		return nil
	}

	// Compute absolute offsets from delta offsets in index entries
	absOffsets := make([]int, metricCount)
	acc := 0

	for i := range metricCount {
		acc += e.indexEntries[i].TimestampOffset
		absOffsets[i] = acc
	}

	// Compute lengths from consecutive offsets
	tsLengths := make([]int, metricCount)
	for i := range metricCount {
		if i < metricCount-1 {
			tsLengths[i] = absOffsets[i+1] - absOffsets[i]
		} else {
			tsLengths[i] = len(allTsBytes) - absOffsets[i]
		}
	}

	// Group by (hash, length) with bytes.Equal verification
	type hashKey struct {
		hash   uint64
		length int
	}

	seen := make(map[hashKey][]int) // value = candidate indices into groups slice
	groups := make([]tsGroup, 0, metricCount)

	for i := range metricCount {
		start := absOffsets[i]
		end := start + tsLengths[i]
		tsSlice := allTsBytes[start:end]

		h := xxhash.Sum64(tsSlice)
		key := hashKey{hash: h, length: tsLengths[i]}

		if candidateGroupIdxs, exists := seen[key]; exists {
			for _, gIdx := range candidateGroupIdxs {
				canon := groups[gIdx]
				canonStart := absOffsets[canon.canonicalIdx]
				canonEnd := canonStart + tsLengths[canon.canonicalIdx]

				if bytes.Equal(tsSlice, allTsBytes[canonStart:canonEnd]) {
					groups[gIdx].sharedIdxs = append(groups[gIdx].sharedIdxs, i)

					goto nextMetric
				}
			}
		}

		// New unique group
		seen[key] = append(seen[key], len(groups))
		groups = append(groups, tsGroup{canonicalIdx: i})

	nextMetric:
	}

	// Filter to only groups with actual sharing
	var shared []tsGroup
	for i := range groups {
		if len(groups[i].sharedIdxs) > 0 {
			shared = append(shared, groups[i])
		}
	}

	return shared
}

// buildDedupTsPayload builds a deduplicated timestamp payload and updates index entries'
// TimestampOffset deltas. Only canonical metrics' timestamp bytes are included; shared
// metrics' data is removed.
//
// Parameters:
//   - allTsBytes: Original encoded timestamp payload
//   - groups: Shared timestamp groups from detectSharedTimestamps
//
// Returns:
//   - []byte: Deduplicated timestamp payload
//   - section.SharedTimestampTable: Table mapping shared metrics to their canonical metrics
func (e *NumericEncoder) buildDedupTsPayload(allTsBytes []byte, groups []tsGroup) ([]byte, section.SharedTimestampTable) {
	metricCount := len(e.indexEntries)

	// Build absolute offsets and lengths from current delta encoding
	absOffsets := make([]int, metricCount)
	acc := 0

	for i := range metricCount {
		acc += e.indexEntries[i].TimestampOffset
		absOffsets[i] = acc
	}

	tsLengths := make([]int, metricCount)
	for i := range metricCount {
		if i < metricCount-1 {
			tsLengths[i] = absOffsets[i+1] - absOffsets[i]
		} else {
			tsLengths[i] = len(allTsBytes) - absOffsets[i]
		}
	}

	// Mark shared metrics
	isShared := make([]bool, metricCount)
	for _, g := range groups {
		for _, idx := range g.sharedIdxs {
			isShared[idx] = true
		}
	}

	// Estimate deduplicated payload size
	dedupSize := 0
	for i := range metricCount {
		if !isShared[i] {
			dedupSize += tsLengths[i]
		}
	}

	// Build deduplicated tsPayload: only non-shared metrics contribute data
	newTsPayload := make([]byte, 0, dedupSize)
	newAbsOffsets := make([]int, metricCount)
	writePos := 0

	for i := range metricCount {
		newAbsOffsets[i] = writePos

		if !isShared[i] {
			start := absOffsets[i]
			end := start + tsLengths[i]
			newTsPayload = append(newTsPayload, allTsBytes[start:end]...)
			writePos += tsLengths[i]
		}
	}

	// Recompute delta offsets from new absolute offsets
	lastOffset := 0
	for i := range metricCount {
		delta := newAbsOffsets[i] - lastOffset
		e.indexEntries[i].TimestampOffset = delta
		lastOffset = newAbsOffsets[i]
	}

	// Build SharedTimestampTable
	table := section.SharedTimestampTable{
		Groups: make([]section.SharedTimestampGroup, len(groups)),
	}

	for i, g := range groups {
		table.Groups[i] = section.SharedTimestampGroup{
			CanonicalIndex: g.canonicalIdx,
			SharedIndices:  g.sharedIdxs,
		}
	}

	return newTsPayload, table
}

// AddDataPoint adds a single data point to the current started metric being encoded.
//
// This method is exclusive with AddDataPoints. Use AddDataPoints for bulk additions
// for better performance.
//
// Parameters:
//   - timestamp: Caller-defined timestamp value (e.g. microseconds since Unix epoch).
//     The unit must be consistent across all data points in the blob.
//   - value: Float64 metric value.
//   - tag: Optional tag string (ignored if tag support is not enabled).
//
// Returns:
//   - error: ErrTooManyDataPoints if adding would exceed claimed data point count.
func (e *NumericEncoder) AddDataPoint(timestamp int64, value float64, tag string) error {
	if e.tsEncoder.Len()-e.ts.length >= e.claimed {
		return errs.ErrTooManyDataPoints
	}

	e.tsEncoder.Write(timestamp)
	e.valEncoder.Write(value)
	// Only encode tags if tag support is enabled
	if e.header.Flag.HasTag() {
		e.tagEncoder.Write(tag)
		// Track if any non-empty tag is written (branchless check for hot path)
		if tag != "" {
			e.hasNonEmptyTags = true
		}
	}

	return nil
}

// AddDataPoints adds multiple data points to the current started metric being encoded.
//
// This method is more efficient than calling AddDataPoint multiple times. The tags parameter
// is optional, but if provided its length must match timestamps and values.
//
// Parameters:
//   - timestamps: Slice of caller-defined timestamp values. The unit must be consistent
//     across all data points in the blob (e.g. microseconds since Unix epoch).
//   - values: Slice of float64 metric values (must have the same length as timestamps).
//   - tags: Optional slice of tag strings (if non-empty, must have the same length as timestamps).
//
// Returns:
//   - error: Length mismatch error if timestamps/values/tags lengths don't match,
//     or ErrTooManyDataPoints if adding would exceed the claimed data point count.
func (e *NumericEncoder) AddDataPoints(timestamps []int64, values []float64, tags []string) error {
	tsLen := len(timestamps)
	valLen := len(values)
	tagLen := len(tags)

	if tsLen == 0 {
		return nil // No-op for empty input
	}
	if tsLen != valLen {
		return fmt.Errorf("mismatched lengths: %d timestamps, %d values", tsLen, valLen)
	}
	if tagLen > 0 && tagLen != tsLen {
		return fmt.Errorf("mismatched lengths: %d timestamps, %d tags", tsLen, tagLen)
	}

	curCount := e.tsEncoder.Len() - e.ts.length // current data points count equal to: total added - previously added
	if curCount+tsLen > e.claimed {
		return errs.ErrTooManyDataPoints
	}

	e.tsEncoder.WriteSlice(timestamps)
	e.valEncoder.WriteSlice(values)
	if tagLen > 0 {
		e.tagEncoder.WriteSlice(tags)
		// Track if any non-empty tag exists in the slice
		for _, tag := range tags {
			if tag != "" {
				e.hasNonEmptyTags = true
				break // Early exit once we find one non-empty tag
			}
		}
	} else {
		// If no tags provided, write empty strings for each data point
		emptyTags := make([]string, tsLen)
		e.tagEncoder.WriteSlice(emptyTags)
		// No need to set hasNonEmptyTags - all are empty
	}

	return nil
}

// getTimestamps returns a slice for timestamps with at least the requested capacity.
// It lazily allocates from pool on first call, then reuses and grows the same slice
// across multiple AddFromRows calls within the same encoder lifecycle.
// The returned slice is capped at maxCachedSliceSize to prevent excessive memory usage.
func (e *NumericEncoder) getTimestamps(capacity int) []int64 {
	// Cap capacity at maximum to prevent excessive memory usage
	if capacity > maxCachedSliceSize {
		capacity = maxCachedSliceSize
	}

	if e.cachedTimestamps == nil {
		// First call - get from pool
		e.cachedTimestamps, e.cleanupTS = pool.GetInt64Slice(capacity)
		return e.cachedTimestamps
	}

	// Reuse existing slice - grow if needed
	if cap(e.cachedTimestamps) < capacity {
		// Return old slice to pool and get a larger one
		if e.cleanupTS != nil {
			e.cleanupTS()
		}
		e.cachedTimestamps, e.cleanupTS = pool.GetInt64Slice(capacity)
	} else {
		// Reuse existing capacity - just reslice to requested length
		e.cachedTimestamps = e.cachedTimestamps[:capacity]
	}

	return e.cachedTimestamps
}

// getValues returns a slice for float64 values with at least the requested capacity.
// It lazily allocates from pool on first call, then reuses and grows the same slice
// across multiple AddFromRows calls within the same encoder lifecycle.
// The returned slice is capped at maxCachedSliceSize to prevent excessive memory usage.
func (e *NumericEncoder) getValues(capacity int) []float64 {
	// Cap capacity at maximum to prevent excessive memory usage
	if capacity > maxCachedSliceSize {
		capacity = maxCachedSliceSize
	}

	if e.cachedValues == nil {
		// First call - get from pool
		e.cachedValues, e.cleanupVal = pool.GetFloat64Slice(capacity)
		return e.cachedValues
	}

	// Reuse existing slice - grow if needed
	if cap(e.cachedValues) < capacity {
		// Return old slice to pool and get a larger one
		if e.cleanupVal != nil {
			e.cleanupVal()
		}
		e.cachedValues, e.cleanupVal = pool.GetFloat64Slice(capacity)
	} else {
		// Reuse existing capacity - just reslice to requested length
		e.cachedValues = e.cachedValues[:capacity]
	}

	return e.cachedValues
}

// getTags returns a slice for tag strings with at least the requested capacity.
// It lazily allocates from pool on first call, then reuses and grows the same slice
// across multiple AddFromRows calls within the same encoder lifecycle.
// The returned slice is capped at maxCachedSliceSize to prevent excessive memory usage.
func (e *NumericEncoder) getTags(capacity int) []string {
	// Cap capacity at maximum to prevent excessive memory usage
	if capacity > maxCachedSliceSize {
		capacity = maxCachedSliceSize
	}

	if e.cachedTags == nil {
		// First call - get from pool
		e.cachedTags, e.cleanupTag = pool.GetStringSlice(capacity)
		return e.cachedTags
	}

	// Reuse existing slice - grow if needed
	if cap(e.cachedTags) < capacity {
		// Return old slice to pool and get a larger one
		if e.cleanupTag != nil {
			e.cleanupTag()
		}
		e.cachedTags, e.cleanupTag = pool.GetStringSlice(capacity)
	} else {
		// Reuse existing capacity - just reslice to requested length
		e.cachedTags = e.cachedTags[:capacity]
	}

	return e.cachedTags
}

// AddFromRows transforms and adds row-based data points to the current started metric.
//
// This high-performance helper eliminates manual loop overhead by pre-allocating columnar
// buffers and extracting all data in a single pass. It provides 3-5x better performance
// compared to manually calling AddDataPoint in a loop.
//
// For metrics with more than 512 data points, the function automatically processes the data
// in batches to prevent excessive memory usage. This chunking mechanism ensures consistent
// memory footprint regardless of metric size.
//
// The slices are cached within the encoder and reused across multiple AddFromRows calls,
// significantly reducing pool allocation overhead when encoding multiple metrics in a single blob.
//
// The extract function is called once for each row to extract the timestamp, value, and tag.
// For metrics without tags, return an empty string for the tag parameter.
//
// Type Parameters:
//   - T: The row-based data type (any type - structs, pointers, maps, interfaces, etc.)
//
// Parameters:
//   - encoder: The NumericEncoder to add data points to (must have a metric already started)
//   - rows: Slice of row-based data points to add
//   - extract: Function to extract (timestamp, value, tag) from each row
//
// Returns:
//   - error: Returns error if AddDataPoints fails (e.g., too many data points, length mismatches)
//
// Example:
//
//	type Measurement struct {
//	    Timestamp time.Time
//	    Value     float64
//	    Tag       string
//	}
//
//	measurements := []Measurement{
//	    {Timestamp: time.Now(), Value: 42.5, Tag: "host=server1"},
//	    {Timestamp: time.Now(), Value: 43.2, Tag: "host=server1"},
//	}
//	encoder, _ := NewNumericEncoder(time.Now())
//	encoder.StartMetricName("cpu.usage", len(measurements))
//	err := AddFromRows(encoder, measurements, func(m Measurement) (int64, float64, string) {
//	    return m.Timestamp.UnixMicro(), m.Value, m.Tag
//	})
//	encoder.EndMetric()
func AddFromRows[T any](
	encoder *NumericEncoder,
	rows []T,
	extract func(T) (timestamp int64, value float64, tag string),
) error {
	if len(rows) == 0 {
		return nil
	}

	// Process in batches to prevent excessive memory usage
	// Maximum batch size is maxCachedSliceSize (512 by default)
	for offset := 0; offset < len(rows); offset += maxCachedSliceSize {
		// Calculate batch size (may be smaller for the last batch)
		batchSize := min(len(rows)-offset, maxCachedSliceSize)

		// Get cached slices (sized for this batch)
		timestamps := encoder.getTimestamps(batchSize)
		values := encoder.getValues(batchSize)
		tags := encoder.getTags(batchSize)

		// Extract data for current batch
		batch := rows[offset : offset+batchSize]
		for i, row := range batch {
			timestamps[i], values[i], tags[i] = extract(row)
		}

		// Add batch to encoder
		if err := encoder.AddDataPoints(timestamps, values, tags); err != nil {
			return err
		}
	}

	return nil
}

// AddFromRowsNoTag transforms and adds row-based data points without tags to the current started metric.
//
// This helper is optimized for metrics that don't use tags. It avoids allocating the tag slice
// entirely, providing 5-7x better performance compared to manual loops for tag-less metrics.
// The encoder will handle empty tags internally if tag support is enabled.
//
// For metrics with more than 512 data points, the function automatically processes the data
// in batches to prevent excessive memory usage. This chunking mechanism ensures consistent
// memory footprint regardless of metric size.
//
// The slices are cached within the encoder and reused across multiple AddFromRowsNoTag calls,
// significantly reducing pool allocation overhead when encoding multiple metrics in a single blob.
//
// Type Parameters:
//   - T: The row-based data type (any type - structs, pointers, maps, interfaces, etc.)
//
// Parameters:
//   - encoder: The NumericEncoder to add data points to (must have a metric already started)
//   - rows: Slice of row-based data points to add
//   - extract: Function to extract (timestamp, value) from each row
//
// Returns:
//   - error: Returns error if AddDataPoints fails (e.g., too many data points, length mismatches)
//
// Example:
//
//	type DataPoint struct {
//	    TS  int64
//	    Val float64
//	}
//
//	points := []DataPoint{
//	    {TS: 1609459200000000, Val: 100.5},
//	    {TS: 1609459201000000, Val: 101.2},
//	}
//	encoder, _ := NewNumericEncoder(time.Now())
//	encoder.StartMetricID(hash.ID("metric1"), len(points))
//	err := AddFromRowsNoTag(encoder, points, func(p DataPoint) (int64, float64) {
//	    return p.TS, p.Val
//	})
//	encoder.EndMetric()
func AddFromRowsNoTag[T any](
	encoder *NumericEncoder,
	rows []T,
	extract func(T) (timestamp int64, value float64),
) error {
	if len(rows) == 0 {
		return nil
	}

	// Process in batches to prevent excessive memory usage
	// Maximum batch size is maxCachedSliceSize (512 by default)
	for offset := 0; offset < len(rows); offset += maxCachedSliceSize {
		// Calculate batch size (may be smaller for the last batch)
		batchSize := min(len(rows)-offset, maxCachedSliceSize)

		// Get cached slices (sized for this batch)
		timestamps := encoder.getTimestamps(batchSize)
		values := encoder.getValues(batchSize)

		// Extract data for current batch (no tag extraction)
		batch := rows[offset : offset+batchSize]
		for i, row := range batch {
			timestamps[i], values[i] = extract(row)
		}

		// Add batch to encoder (nil for tags - encoder handles as empty tags)
		if err := encoder.AddDataPoints(timestamps, values, nil); err != nil {
			return err
		}
	}

	return nil
}
