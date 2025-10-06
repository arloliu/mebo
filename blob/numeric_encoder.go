package blob

import (
	"fmt"
	"math"
	"time"

	"github.com/arloliu/mebo/encoding"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/collision"
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
	hasCollision bool // Set when hash collision detected, applied to cloned header in Finish()
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

// NewNumericEncoder creates a new NumericEncoder with the given start time.
//
// The encoder will grow dynamically as metrics are added, up to MaxMetricCount (65536).
//
// Parameters:
//   - blobTs: Timestamp for the entire blob, used as sorting key for all blobs in the same series
//   - opts: Optional encoding configuration (endianness, compression, encoding types, etc.)
//
// Returns:
//   - *NumericEncoder: New encoder instance ready for metric encoding
//   - error: Configuration error if invalid options provided
func NewNumericEncoder(blobTs time.Time, opts ...NumericEncoderOption) (*NumericEncoder, error) {
	// Create base configuration
	config := NewNumericEncoderConfig(blobTs)

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
	switch enc {
	case format.TypeRaw:
		encoder.valEncoder = encoding.NewNumericRawEncoder(encoder.engine)
	case format.TypeGorilla:
		encoder.valEncoder = encoding.NewNumericGorillaEncoder()
	case format.TypeDelta:
		return nil, fmt.Errorf("value encoding %s not supported yet", enc.String())
	default:
		return nil, fmt.Errorf("invalid value encoding: %s", enc.String())
	}

	enc = encoder.header.Flag.TimestampEncoding()
	switch enc { //nolint: exhaustive
	case format.TypeRaw:
		encoder.tsEncoder = encoding.NewTimestampRawEncoder(encoder.engine)
	case format.TypeDelta:
		encoder.tsEncoder = encoding.NewTimestampDeltaEncoder()
	default:
		return nil, fmt.Errorf("invalid timestamp encoding: %s", enc.String())
	}

	encoder.tagEncoder = encoding.NewTagEncoder(encoder.engine)

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

	if numOfDataPoints <= 0 || numOfDataPoints > math.MaxUint16 {
		return errs.ErrInvalidNumOfDataPoints
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

	if numOfDataPoints <= 0 || numOfDataPoints > math.MaxUint16 {
		return errs.ErrInvalidNumOfDataPoints
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

	// For Gorilla encoding, we need to flush any pending bits BEFORE calculating lengths
	// This ensures the length includes all flushed data
	// For other encodings, this is a no-op as Bytes() just returns the buffer
	if e.header.Flag.ValueEncoding() == format.TypeGorilla {
		_ = e.valEncoder.Bytes() // Flush pending bits
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

	// Set actual metric count in cloned header now that encoding is complete
	finalHeader.MetricCount = uint32(len(e.indexEntries)) //nolint: gosec

	// Compress timestamp and value payloads
	tsPayload, err := e.tsCodec.Compress(e.tsEncoder.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to compress timestamp payload: %w", err)
	}
	valPayload, err := e.valCodec.Compress(e.valEncoder.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to compress value payload: %w", err)
	}

	// Only compress tag payload if tag support is enabled
	var tagPayload []byte
	if finalHeader.Flag.HasTag() {
		tagPayload, err = e.tagCodec.Compress(e.tagEncoder.Bytes())
		if err != nil {
			return nil, fmt.Errorf("failed to compress tag payload: %w", err)
		}
	}

	// Encode metric names payload if collision was detected
	// In ID mode, collisionTracker is nil, so we skip this entirely
	var metricNamesPayload []byte
	if e.collisionTracker != nil && finalHeader.Flag.HasMetricNames() {
		metricNamesPayload, err = encoding.EncodeMetricNames(e.collisionTracker.GetMetricNames(), e.engine)
		if err != nil {
			return nil, fmt.Errorf("failed to encode metric names: %w", err)
		}
		// Update IndexOffset to account for metric names payload (positioned after header)
		finalHeader.IndexOffset = uint32(section.HeaderSize + len(metricNamesPayload)) //nolint: gosec
	}

	// Calculate TimestampPayloadOffset based on actual index entries count
	indexEntriesSize := section.NumericIndexEntrySize * len(e.indexEntries)
	finalHeader.TimestampPayloadOffset = finalHeader.IndexOffset + uint32(indexEntriesSize) //nolint: gosec

	// Set value payload offset in header, it records the value payload offset after the timestamp payload.
	// The size of timestamp payload is the compressed size.
	finalHeader.ValuePayloadOffset = finalHeader.TimestampPayloadOffset + uint32(len(tsPayload)) //nolint: gosec

	// Set tag payload offset in header, it records the tag payload offset after the value payload.
	// The size of value payload is the compressed size.
	finalHeader.TagPayloadOffset = finalHeader.ValuePayloadOffset + uint32(len(valPayload)) //nolint: gosec

	// Pre-calculate exact size (reuse indexEntriesSize from above)
	blobSize := section.HeaderSize + len(metricNamesPayload) + indexEntriesSize + len(tsPayload) + len(valPayload) + len(tagPayload)

	// Get pooled buffer for building the final blob
	buf := pool.GetBlobBuffer()
	defer pool.PutBlobBuffer(buf)

	buf.Reset()
	buf.ExtendOrGrow(blobSize)
	blob := buf.Bytes()
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

	// Copy timestamp payload
	offset += copy(blob[offset:], tsPayload)

	// Copy value payload
	offset += copy(blob[offset:], valPayload)

	// Copy tag payload
	copy(blob[offset:], tagPayload)

	return blob, nil
}

// AddDataPoint adds a single timestamp-value pair to the current started metric being encoded.
//
// This method is exclusive with AddDataPoints. Use AddDataPoints for bulk additions
// for better performance.
//
// Parameters:
//   - timestamp: Timestamp in microseconds since Unix epoch
//   - value: Float64 metric value
//   - tag: Optional tag string (ignored if tag support not enabled)
//
// Returns:
//   - error: ErrTooManyDataPoints if adding would exceed claimed data point count
func (e *NumericEncoder) AddDataPoint(timestamp int64, value float64, tag string) error {
	if e.tsEncoder.Len()-e.ts.length >= e.claimed {
		return errs.ErrTooManyDataPoints
	}

	e.tsEncoder.Write(timestamp)
	e.valEncoder.Write(value)
	// Only encode tags if tag support is enabled
	if e.header.Flag.HasTag() {
		e.tagEncoder.Write(tag)
	}

	return nil
}

// AddDataPoints adds multiple timestamp-value pairs to the current started metric being encoded.
//
// This method is more efficient than calling AddDataPoint multiple times. The tags parameter is
// optional, but if provided its length must match timestamps and values. This method is exclusive
// with AddDataPoint.
//
// Parameters:
//   - timestamps: Slice of timestamps in microseconds since Unix epoch
//   - values: Slice of float64 metric values (must match timestamps length)
//   - tags: Optional slice of tag strings (if provided, must match timestamps length)
//
// Returns:
//   - error: Length mismatch error if timestamps/values/tags lengths don't match,
//     or ErrTooManyDataPoints if adding would exceed claimed data point count
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
	} else {
		// If no tags provided, write empty strings for each data point
		emptyTags := make([]string, tsLen)
		e.tagEncoder.WriteSlice(emptyTags)
	}

	return nil
}
