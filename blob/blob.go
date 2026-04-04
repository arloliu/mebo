package blob

import (
	"slices"
	"sync"
	"time"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// Global engine cache to avoid interface overhead
var (
	littleEndianEngine endian.EndianEngine
	bigEndianEngine    endian.EndianEngine
	engineOnce         sync.Once
)

func initEngines() {
	littleEndianEngine = endian.GetLittleEndianEngine()
	bigEndianEngine = endian.GetBigEndianEngine()
}

// BlobReader represents common interface for both NumericBlob and TextBlob.
// It is a type-erased interface for accessing blob metadata and data.
//
// This interface allows users to work with blobs without knowing their concrete type.
type BlobReader interface {
	// IsNumeric returns true if it's a numeric blob.
	IsNumeric() bool

	// IsText returns true if it's a text blob.
	IsText() bool

	// AsNumeric attempts to cast to NumericBlob, returns false if not numeric.
	AsNumeric() (NumericBlob, bool)

	// AsText attempts to cast to TextBlob, returns false if not text.
	AsText() (TextBlob, bool)

	// StartTime returns the start time of the blob.
	StartTime() time.Time

	// MetricCount returns the number of unique metrics in the blob.
	MetricCount() int

	// HasMetricID returns true if the blob has the given metric ID.
	HasMetricID(metricID uint64) bool

	// HasMetricName returns true if the blob has the given metric name.
	HasMetricName(metricName string) bool

	// MetricIDs returns a slice of all metric IDs in the blob.
	// The returned slice is cloned to prevent external modification.
	MetricIDs() []uint64

	// MetricNames returns a slice of all metric names in the blob.
	// The returned slice is cloned to prevent external modification.
	MetricNames() []string

	// Len returns the number of data points for the given metric ID.
	// Returns 0 if the metric ID doesn't exist.
	Len(metricID uint64) int

	// LenByName returns the number of data points for the given metric name.
	// Returns 0 if the metric name doesn't exist or the blob has no metric names.
	LenByName(metricName string) int
}

// blobBase contains common fields and methods shared by NumericBlob and TextBlob.
// This is an internal type for code reuse and is not exposed in the public API.
//
// Layout version (formatVersion) controls container structure:
//   - V1: map-based index, insertion-order metric storage
//   - V2: sorted-slice index (binary search), MetricID-sorted storage, optional shared timestamps
//
// Encoding type (tsEncType, valEncType) controls data compression algorithms
// independently of layout version. Codecs (Raw, Delta, Gorilla, Chimp) are orthogonal
// to the container layout and can be freely combined with any layout version.
type blobBase struct {
	tsEncType       format.EncodingType // Timestamp encoding type (hot: decoder selection)
	valEncType      format.EncodingType // Value encoding type (hot: decoder selection)
	flags           uint16              // Packed flags: endian, tsEnc, valEnc, tag, etc. (hot: feature checks)
	formatVersion   uint8               // 1=v1 layout, 2=v2 layout (shared timestamp table)
	sameByteOrder   bool                // Whether the blob uses the same byte order as the system (hot: decoder optimization)
	endianType      uint8               // 0=little, 1=big (warm: Engine() only)
	startTimeMicros int64               // Unix timestamp in microseconds (warm: metadata queries)
}

const (
	blobFormatV1 uint8 = 1
	blobFormatV2 uint8 = 2
)

// indexEntry is a type constraint for index entry types used in indexMaps.
// Both NumericIndexEntry and TextIndexEntry implement this interface.
type indexEntry interface {
	GetMetricID() uint64
	GetCount() uint32
}

// indexMaps holds metric ID and name mappings for a blob.
// Generic over the index entry type (NumericIndexEntry or TextIndexEntry).
//
// Supports two storage strategies:
//   - V1 (map): byID map for O(1) amortized lookups (default)
//   - V2 (sorted): sorted slice for cache-friendly iteration and binary search lookups
//
// V2 maintains a parallel sortedIDs []uint64 slice for fast binary search.
// This avoids the 64-byte cache line stride of []NumericIndexEntry and eliminates
// interface dispatch overhead from the generic GetMetricID() call.
type indexMaps[T indexEntry] struct {
	byID      map[uint64]T // V1: primary lookup; V2: nil
	byName    map[string]T // metricName → IndexEntry (nil if no collisions occurred)
	sorted    []T          // V2: primary lookup (sorted by MetricID); V1: nil
	sortedIDs []uint64     // V2: parallel MetricID slice for binary search; V1: nil
}

// StartTime returns the start time of the blob.
// This method is embedded by NumericBlob and TextBlob to satisfy BlobReader interface.
func (b blobBase) StartTime() time.Time {
	if b.startTimeMicros == 0 && b.tsEncType == 0 && b.flags == 0 && b.endianType == 0 {
		return time.Time{}
	}

	return time.UnixMicro(b.startTimeMicros).UTC()
}

// Engine returns the endian engine for byte order operations.
// Internal helper for decoder selection.
func (b blobBase) Engine() endian.EndianEngine {
	engineOnce.Do(initEngines)
	if b.endianType == 0 {
		return littleEndianEngine
	}

	return bigEndianEngine
}

// TimestampEncodingType returns the timestamp encoding type.
// Internal helper for decoder selection.
func (b blobBase) TimestampEncodingType() format.EncodingType {
	return b.tsEncType
}

// SameByteOrder returns whether the blob uses the same byte order as the system.
// Internal helper for optimization selection (raw unsafe decoder vs safe decoder).
func (b blobBase) SameByteOrder() bool {
	return b.sameByteOrder
}

// Flag accessor methods for packed flags field

// IsLittleEndian returns whether the data is little-endian.
func (b blobBase) IsLittleEndian() bool {
	return (b.flags & section.FlagEndianLittleEndian) == 0
}

// IsBigEndian returns whether the data is big-endian.
func (b blobBase) IsBigEndian() bool {
	return (b.flags & section.FlagEndianLittleEndian) != 0
}

// TimestampEncoding returns the timestamp encoding type from packed flags.
func (b blobBase) TimestampEncoding() format.EncodingType {
	if (b.flags & section.FlagTsEncRaw) != 0 {
		return format.TypeRaw
	}

	return format.TypeDelta
}

// ValueEncoding returns the value encoding type.
func (b blobBase) ValueEncoding() format.EncodingType {
	return b.valEncType
}

// IsV2Layout returns whether blob uses the V2 on-wire layout.
// V2 layout controls container structure (sorted index, optional shared timestamps)
// but does NOT affect encoder/decoder algorithm selection — codecs are orthogonal.
func (b blobBase) IsV2Layout() bool {
	return b.formatVersion == blobFormatV2
}

// HasTag returns whether tag support is enabled.
func (b blobBase) HasTag() bool {
	return (b.flags & section.FlagTagEnabled) != 0
}

// HasMetricNames returns whether metric names payload is enabled.
func (b blobBase) HasMetricNames() bool {
	return (b.flags & section.FlagMetricNames) != 0
}

// MetricCount returns the number of unique metrics in the blob.
// If metric names are available (collision occurred), returns len(byName),
// otherwise returns len(byID) or len(sorted).
func (m indexMaps[T]) MetricCount() int {
	if m.byName != nil {
		return len(m.byName)
	}

	if m.sorted != nil {
		return len(m.sorted)
	}

	return len(m.byID)
}

// HasMetricID checks if the given metric ID exists in the blob.
func (m indexMaps[T]) HasMetricID(metricID uint64) bool {
	if m.sortedIDs != nil {
		_, found := slices.BinarySearch(m.sortedIDs, metricID)

		return found
	}

	_, ok := m.byID[metricID]

	return ok
}

// HasMetricName checks if the given metric name exists in the blob.
// This method looks up by hashed ID if byName is nil(no collisions occurred),
// then looks up by name if byName exists(collision case).
//
// Returns false if the blob doesn't have metric names (byName is nil).
func (m indexMaps[T]) HasMetricName(metricName string) bool {
	// If byName map exists, use it (collision case)
	if m.byName != nil {
		_, ok := m.byName[metricName]
		return ok
	}

	// Hash fallback: safe when byName is nil (no collisions)
	return m.HasMetricID(hash.ID(metricName))
}

// MetricIDs returns a slice of all metric IDs in the blob.
// The slice is newly allocated to prevent external modification.
// V2 sorted index returns IDs in deterministic sorted order.
func (m indexMaps[T]) MetricIDs() []uint64 {
	if m.sortedIDs != nil {
		return slices.Clone(m.sortedIDs)
	}

	ids := make([]uint64, 0, len(m.byID))
	for id := range m.byID {
		ids = append(ids, id)
	}

	return ids
}

// MetricNames returns a slice of all metric names in the blob.
// Returns an empty slice if the blob doesn't have metric names (byName is nil).
// The slice is newly allocated to prevent external modification.
func (m indexMaps[T]) MetricNames() []string {
	if m.byName == nil {
		return []string{}
	}
	names := make([]string, 0, len(m.byName))
	for name := range m.byName {
		names = append(names, name)
	}

	return names
}

// GetByID returns the index entry for the given metric ID.
// Uses binary search on V2 sorted index, map lookup on V1.
// Returns (entry, true) if found, or (zero-value, false) if not found.
func (m indexMaps[T]) GetByID(metricID uint64) (T, bool) {
	if m.sortedIDs != nil {
		i, found := slices.BinarySearch(m.sortedIDs, metricID)
		if found {
			return m.sorted[i], true
		}

		var zero T

		return zero, false
	}

	entry, ok := m.byID[metricID]

	return entry, ok
}

// GetByName returns the index entry for the given metric name.
//
// Behavior:
//   - If byName map exists: performs direct lookup (handles collisions correctly)
//   - If byName is nil: hashes the name and looks up by ID (works when no collisions)
//
// This design leverages the collision detection mechanism:
//   - When collision occurs → byName is populated → direct lookup is used
//   - When no collision → byName is nil → hash fallback is safe
//
// Returns (entry, true) if found, or (zero-value, false) if not found.
func (m indexMaps[T]) GetByName(metricName string) (T, bool) {
	// If metric name map exists, use it (collision case or StartMetricName used)
	if m.byName != nil {
		entry, ok := m.byName[metricName]
		return entry, ok
	}

	// Hash fallback: safe when byName is nil (no collisions)
	return m.GetByID(hash.ID(metricName))
}

// Len returns the number of data points for the given metric ID.
// Returns 0 if the metric ID doesn't exist.
func (m indexMaps[T]) Len(metricID uint64) int {
	entry, ok := m.GetByID(metricID)
	if !ok {
		return 0
	}

	return int(entry.GetCount())
}

// LenByName returns the number of data points for the given metric name.
// Returns 0 if the metric name doesn't exist.
func (m indexMaps[T]) LenByName(metricName string) int {
	entry, ok := m.GetByName(metricName)
	if !ok {
		return 0
	}

	return int(entry.GetCount())
}

// ForEach iterates over all entries, calling fn for each one.
// V2 sorted index iterates in deterministic MetricID order with contiguous memory access.
// V1 map iterates in arbitrary order.
// Return false from fn to stop iteration.
func (m indexMaps[T]) ForEach(fn func(T) bool) {
	if m.sorted != nil {
		for _, e := range m.sorted {
			if !fn(e) {
				return
			}
		}

		return
	}

	for _, e := range m.byID {
		if !fn(e) {
			return
		}
	}
}

// At returns the entry at position i for direct indexed access.
// Only valid for V2 sorted index. Panics if index is out of range.
func (m indexMaps[T]) At(i int) T {
	return m.sorted[i]
}

// IsEmpty returns whether the index contains no entries.
func (m indexMaps[T]) IsEmpty() bool {
	if m.sorted != nil {
		return len(m.sorted) == 0
	}

	return len(m.byID) == 0
}
