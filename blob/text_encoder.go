package blob

import (
	"fmt"
	"math"
	"time"

	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/collision"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/internal/options"
	"github.com/arloliu/mebo/internal/pool"
	"github.com/arloliu/mebo/section"
)

// TextEncoder encodes text values into the binary blob format.
//
// Unlike NumericEncoder which uses columnar storage (separate timestamp and value sections),
// TextEncoder uses row-based storage where each data point is encoded as:
//   - Timestamp (varint delta or int64 raw)
//   - Value (uint8 length + string, max 255 chars)
//   - Tag (uint8 length + string, max 255 chars, optional)
//
// The entire data section is compressed as a single unit after encoding.
//
// Note: The TextEncoder is NOT thread-safe. Each encoder instance should be used by a single goroutine at a time.
//
// Note: The TextEncoder is NOT reusable. After calling Finish, a new encoder must be created for further encoding.
type TextEncoder struct {
	*TextEncoderConfig

	dataEncoder *ienc.VarStringEncoder // Encodes all data points (timestamps + values + tags)

	curMetricID uint64 // current metric ID being encoded
	claimed     int    // number of data points claimed for the current metric
	added       int    // number of data points added for the current metric

	// Delta encoding state - tracks last timestamp for efficient delta calculation
	lastTimestamp int64 // Last encoded timestamp (reset to 0 in EndMetric for each new metric)

	// Data encoder state tracking
	dataState encoderState // data encoder state (24 bytes)

	// Collision detection - mode-specific optimization
	collisionTracker *collision.Tracker  // Tracks metric names (Name mode only)
	usedIDs          map[uint64]struct{} // Tracks IDs for duplicates (ID mode only)

	// Mode tracking - determines identifier strategy (ID vs Name)
	identifierMode metricIdentifierMode // Locked after first StartMetric call

	// Header immutability - track pending changes to apply in Finish()
	hasCollision bool // Set when hash collision detected, applied to cloned header in Finish()

	// Pooled buffer for building data points
	buf *pool.ByteBuffer
}

// NewTextEncoder creates a new TextEncoder with the given start time.
//
// The encoder will grow dynamically as metrics are added, up to MaxMetricCount (65536).
//
// Parameters:
//   - blobTS: Timestamp for the entire blob, used as sorting key for all blobs in the same series
//   - opts: Optional encoding configuration (endianness, compression, timestamp encoding, tag support, etc.)
//
// Returns:
//   - *TextEncoder: New encoder instance ready for metric encoding
//   - error: Configuration error if invalid options provided
func NewTextEncoder(blobTS time.Time, opts ...TextEncoderOption) (*TextEncoder, error) {
	// Create base configuration
	config := NewTextEncoderConfig(blobTS)

	encoder := &TextEncoder{
		TextEncoderConfig: config,
		identifierMode:    modeUndefined,
		collisionTracker:  nil, // lazy creation
		usedIDs:           nil, // lazy creation
		buf:               pool.GetBlobBuffer(),
	}

	// Apply options to base config
	if err := options.Apply(config, opts...); err != nil {
		return nil, err
	}

	// Initialize data encoder
	encoder.dataEncoder = ienc.NewVarStringEncoder(encoder.engine)

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
func (e *TextEncoder) StartMetricID(metricID uint64, numOfDataPoints int) error {
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

	// In ID mode, use simple map for duplicate detection
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
func (e *TextEncoder) startMetric(metricID uint64, numOfDataPoints int) error {
	// Capture current encoder state
	e.dataState.update(e.dataEncoder.Size(), e.dataEncoder.Len())

	// Set current metric state
	e.curMetricID = metricID
	e.claimed = numOfDataPoints
	e.added = 0
	e.lastTimestamp = 0 // Initialize for delta encoding

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
// This method is exclusive with StartMetricID. Once StartMetricName is called, all subsequent
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
func (e *TextEncoder) StartMetricName(metricName string, numOfDataPoints int) error {
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
	if e.collisionTracker.HasCollision() {
		e.hasCollision = true
	}

	return e.startMetric(metricID, numOfDataPoints)
}

// AddDataPoint adds a single data point to the current metric.
//
// The value string is limited to 255 characters. The tag string is optional and limited
// to 255 characters. If tags are disabled (default), the tag parameter is ignored.
// If tags are enabled, the tag is encoded even if empty string.
//
// Parameters:
//   - timestamp: Timestamp in microseconds since Unix epoch
//   - value: Text value string (max 255 characters)
//   - tag: Optional tag string (max 255 characters, ignored if tag support disabled)
//
// Returns:
//   - error: ErrNoMetricStarted, value/tag length validation errors, or ErrTooManyDataPoints
//     if adding would exceed claimed data point count
func (e *TextEncoder) AddDataPoint(timestamp int64, value string, tag string) error {
	if e.curMetricID == 0 {
		return errs.ErrNoMetricStarted
	}

	if e.added >= e.claimed {
		return fmt.Errorf("%w: claimed %d points, trying to add %d", errs.ErrTooManyDataPoints, e.claimed, e.added+1)
	}

	// Encode timestamp based on encoding type
	e.buf.Reset()
	tsEncoding := e.header.Flag.GetTimestampEncoding()
	switch tsEncoding {
	case format.TypeRaw:
		// Raw encoding: write int64 directly using endianness
		e.buf.ExtendOrGrow(8)
		bufBytes := e.buf.Bytes()
		offset := len(bufBytes) - 8
		e.engine.PutUint64(bufBytes[offset:], uint64(timestamp)) //nolint:gosec

	case format.TypeDelta:
		// Delta encoding: write varint delta from previous timestamp
		// First data point: delta from blob start time
		// Subsequent data points: delta from previous timestamp
		var baseTs int64
		if e.added == 0 {
			// First data point: calculate delta from blob start time
			baseTs = e.header.StartTime
		} else {
			// Subsequent data points: calculate delta from previous timestamp
			baseTs = e.lastTimestamp
		}

		delta := timestamp - baseTs
		e.dataEncoder.WriteVarint(delta)

		// Track timestamp for next delta calculation
		e.lastTimestamp = timestamp

	case format.TypeGorilla:
		return fmt.Errorf("timestamp encoding %v not supported yet", format.TypeGorilla)

	default:
		return fmt.Errorf("invalid timestamp encoding: %v", tsEncoding)
	}

	// Write timestamp from buffer to data encoder
	if e.buf.Len() > 0 {
		if err := e.dataEncoder.Write(string(e.buf.Bytes())); err != nil {
			return fmt.Errorf("failed to write timestamp: %w", err)
		}
	}

	// Validate lengths before encoding
	if len(value) > ienc.MaxTextLength {
		return fmt.Errorf("value length %d exceeds maximum %d", len(value), ienc.MaxTextLength)
	}
	if e.header.Flag.HasTag() && len(tag) > ienc.MaxTextLength {
		return fmt.Errorf("tag length %d exceeds maximum %d", len(tag), ienc.MaxTextLength)
	}

	// NEW LAYOUT: Group length bytes together before data
	// Write [LEN_V][LEN_T] (if tags enabled), then [VAL][TAG]
	// This improves cache locality during random access operations

	// Write all length bytes together
	e.buf.Reset()
	e.buf.MustWrite([]byte{byte(len(value))})
	if e.header.Flag.HasTag() {
		e.buf.MustWrite([]byte{byte(len(tag))})
	}
	e.dataEncoder.WriteRaw(e.buf.Bytes())

	// Write all data together
	e.dataEncoder.WriteRaw([]byte(value))
	if e.header.Flag.HasTag() {
		e.dataEncoder.WriteRaw([]byte(tag))
	}

	e.added++

	return nil
}

// EndMetric completes the encoding of the current metric and prepares the encoder
// for the next metric. This method should be called after all data points have been added.
func (e *TextEncoder) EndMetric() error {
	if e.curMetricID == 0 {
		return errs.ErrNoMetricStarted
	}

	if e.added == 0 {
		return errs.ErrNoDataPointsAdded
	}

	if e.added != e.claimed {
		return fmt.Errorf("%w: claimed %d points, added %d", errs.ErrDataPointCountMismatch, e.claimed, e.added)
	}

	// Create index entry
	dataOffset := e.dataState.offset
	dataLength := e.dataEncoder.Size() - dataOffset

	//nolint:gosec
	entry := section.TextIndexEntry{
		MetricID: e.curMetricID,
		Count:    uint16(e.added),
		Offset:   uint32(dataOffset),
		Size:     uint32(dataLength),
	}

	// Add entry to index
	e.addEntryIndex(entry)

	// Update state for next metric
	e.dataState.updateLast()

	// Reset current metric state
	e.curMetricID = 0
	e.claimed = 0
	e.added = 0
	e.lastTimestamp = 0 // Reset for next metric's delta encoding

	return nil
}

// Finish completes the encoding and returns the final blob as a byte slice.
// After calling Finish, the encoder cannot be reused.
func (e *TextEncoder) Finish() ([]byte, error) {
	// Return buffers to pool even on error paths
	defer func() {
		if e.buf != nil {
			pool.PutBlobBuffer(e.buf)
			e.buf = nil
		}
		if e.dataEncoder != nil {
			e.dataEncoder.Reset()
		}
	}()

	// Check state
	if e.curMetricID != 0 {
		return nil, errs.ErrMetricNotEnded
	}

	if len(e.indexEntries) == 0 {
		return nil, errs.ErrNoMetricsAdded
	}

	// Clone header for immutability
	header := e.cloneHeader()

	// Update header with final counts and offsets
	header.MetricCount = uint32(len(e.indexEntries)) //nolint:gosec

	// Compress data if needed
	dataBytes := e.dataEncoder.Bytes()
	var compressedData []byte
	var err error

	// DataSize always stores the uncompressed size for verification and Size calculation
	header.DataSize = uint32(len(dataBytes)) //nolint:gosec

	if e.dataCodec != nil {
		compressedData, err = e.dataCodec.Compress(dataBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to compress data: %w", err)
		}
	} else {
		compressedData = dataBytes
	}

	// Encode metric names payload if in Name mode (before calculating offsets)
	// This allows ByName() lookups and handles hash collisions
	var namesPayload []byte
	if e.identifierMode == modeNameManaged && e.collisionTracker != nil {
		var err error
		namesPayload, err = ienc.EncodeMetricNames(e.collisionTracker.GetMetricNames(), e.engine)
		if err != nil {
			return nil, fmt.Errorf("failed to encode metric names: %w", err)
		}
		header.Flag.SetHasMetricNames(true)
	}

	// Calculate offsets (metric names come before index)
	namesSize := uint32(len(namesPayload)) //nolint:gosec
	indexSize := len(e.indexEntries) * section.TextIndexEntrySize
	header.IndexOffset = section.IndexOffsetOffset + namesSize
	header.DataOffset = header.IndexOffset + uint32(indexSize) //nolint:gosec

	// Pre-calculate exact blob size
	headerSize := section.HeaderSize
	indexEntriesSize := len(e.indexEntries) * section.TextIndexEntrySize
	blobSize := headerSize + len(namesPayload) + indexEntriesSize + len(compressedData)

	// Allocate exact-size buffer for the final blob
	// No need for pooled buffer since we return this directly to caller
	blob := make([]byte, blobSize)
	offset := 0

	// Write header
	offset += copy(blob[offset:], header.Bytes())

	// Write metric names payload (if exists)
	if len(namesPayload) > 0 {
		offset += copy(blob[offset:], namesPayload)
	}

	// Write index entries
	for i := range e.indexEntries {
		entryOffset := offset + i*section.TextIndexEntrySize
		if err := e.indexEntries[i].WriteToSlice(blob[entryOffset:], e.engine); err != nil {
			return nil, fmt.Errorf("failed to write index entry: %w", err)
		}
	}
	offset += indexEntriesSize

	// Write compressed data
	copy(blob[offset:], compressedData)

	return blob, nil
}

// cloneHeader creates a shallow copy of the encoder's header for immutability.
func (e *TextEncoder) cloneHeader() *section.TextHeader {
	cloned := *e.header
	return &cloned
}
