package blob

import (
	"time"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/hash"
)

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
type blobBase struct {
	engine        endian.EndianEngine
	startTime     time.Time
	tsEncType     format.EncodingType
	sameByteOrder bool
}

// indexMaps holds metric ID and name mappings for a blob.
// Generic over the index entry type (NumericIndexEntry or TextIndexEntry).
type indexMaps[T any] struct {
	byID   map[uint64]T // metricID → IndexEntry
	byName map[string]T // metricName → IndexEntry (nil if no collisions occurred)
}

// countGetter is an interface for accessing the Count field from index entries.
// Both NumericIndexEntry and TextIndexEntry implement this via their Count field.
type countGetter interface {
	GetCount() uint32
}

// StartTime returns the start time of the blob.
// This method is embedded by NumericBlob and TextBlob to satisfy BlobReader interface.
func (b blobBase) StartTime() time.Time {
	return b.startTime
}

// Engine returns the endian engine for byte order operations.
// Internal helper for decoder selection.
func (b blobBase) Engine() endian.EndianEngine {
	return b.engine
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

// MetricCount returns the number of unique metrics in the blob.
// If metric names are available (collision occurred), returns len(byName),
// otherwise returns len(byID).
func (m indexMaps[T]) MetricCount() int {
	if m.byName != nil {
		return len(m.byName)
	}

	return len(m.byID)
}

// HasMetricID checks if the given metric ID exists in the blob.
func (m indexMaps[T]) HasMetricID(metricID uint64) bool {
	_, ok := m.byID[metricID]
	return ok
}

// HasMetricName checks if the given metric name exists in the blob.
// This method looks up by hashed ID if byName is nil(no collisions occurred),
// then looks up by name if byName exists(collision case).
//
// Returns false if the blob doesn't have metric names (byName is nil).
func (m indexMaps[T]) HasMetricName(metricName string) bool {
	// If byName map doesn't exist(the most common case), look up by hashed ID
	if m.byName == nil {
		_, ok := m.byID[hash.ID(metricName)]
		return ok
	}

	_, ok := m.byName[metricName]

	return ok
}

// MetricIDs returns a slice of all metric IDs in the blob.
// The slice is newly allocated to prevent external modification.
func (m indexMaps[T]) MetricIDs() []uint64 {
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
// Returns (entry, true) if found, or (zero-value, false) if not found.
func (m indexMaps[T]) GetByID(metricID uint64) (T, bool) {
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
	metricID := hash.ID(metricName)
	entry, ok := m.byID[metricID]

	return entry, ok
}

// Len returns the number of data points for the given metric ID.
// Returns 0 if the metric ID doesn't exist.
//
// This method requires T to implement countGetter interface (have GetCount() method).
func (m indexMaps[T]) Len(metricID uint64) int {
	entry, ok := m.GetByID(metricID)
	if !ok {
		return 0
	}

	// Use type assertion to access Count field
	// Both NumericIndexEntry and TextIndexEntry have Count field
	if typed, ok := any(entry).(countGetter); ok {
		return int(typed.GetCount())
	}

	return 0
}

// LenByName returns the number of data points for the given metric name.
// Returns 0 if the metric name doesn't exist.
func (m indexMaps[T]) LenByName(metricName string) int {
	entry, ok := m.GetByName(metricName)
	if !ok {
		return 0
	}

	// Use type assertion to access Count field
	if typed, ok := any(entry).(countGetter); ok {
		return int(typed.GetCount())
	}

	return 0
}
