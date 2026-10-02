package blob

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
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
// Ordinal-keyed representation: `sorted` holds EVERY index entry in index order
// and is the single source of truth for cardinality and enumeration:
//   - V1/text: `sorted` is insertion order; `byID` maps a MetricID to its FIRST
//     ordinal (first-wins) so GetByID resolves a collided ID to the first entry.
//     `sortedIDs` is nil.
//   - V2: `sorted` is MetricID-sorted (this order is enforced at decode time);
//     `sortedIDs` is the parallel MetricID slice for binary search (leftmost =
//     first). `byID` is nil.
//
// A within-blob collision (two distinct names sharing one MetricID) keeps both
// entries in `sorted`, so MetricCount/MetricIDs count both entries separately
// and the collided ID resolves to the first entry in index order.
//
// Name lookups:
//   - `names` (parallel to `sorted`) is retained whenever the blob carries a
//     names payload; nil otherwise.
//   - `byName` (name → ordinal) is built ONLY when a collision is present. On a
//     no-collision names-bearing blob it stays nil, and ByName membership is
//     answered by hashing the query, locating the candidate entry, and then
//     string-comparing against the retained stored name to preserve exact
//     membership (this rejects a query that only hash-collides with a stored
//     name without actually matching it). This laziness also lets a blob-set
//     cheaply detect collisions via `byName != nil` instead of rescanning.
type indexMaps[T indexEntry] struct {
	byID      map[uint64]int // V1/text: MetricID → first ordinal into `sorted`; nil for V2
	byName    map[string]int // metricName → ordinal into `sorted`; built ONLY on collision; nil otherwise
	sorted    []T            // ALL entries in index order (V1/text insertion; V2 MetricID-sorted)
	sortedIDs []uint64       // V2: parallel MetricID slice for binary search; nil for V1/text
	names     []string       // ordered metric names parallel to `sorted`; nil if no names payload
	// namesBorrowed is true when `names` alias the decoder's input buffer instead
	// of owning their bytes (a zero-copy borrowed decode). It never affects
	// raw-blob reads (immutable-while-live is the borrowed contract); it only
	// tells the materialization paths to DEEP-clone names so derived objects are
	// always owning and the borrowed-lifetime rule never propagates.
	namesBorrowed bool
}

// hasDuplicateID reports whether two entries share a MetricID (a within-blob
// collision). Zero-alloc on both layouts.
func (m indexMaps[T]) hasDuplicateID() bool {
	if m.sortedIDs != nil {
		for i := 1; i < len(m.sortedIDs); i++ {
			if m.sortedIDs[i] == m.sortedIDs[i-1] {
				return true
			}
		}

		return false
	}

	// V1/text: byID is first-wins, so a duplicate exists iff distinct IDs < entries.
	return len(m.byID) < len(m.sorted)
}

// finalizeNames attaches the ordered names payload to the index, then — only
// when a within-blob collision is present — rejects duplicate names and builds
// the byName ordinal map. On a no-collision blob no string map is allocated, so
// this adds no allocation to the common decode path.
//
// borrowed records whether names alias the decoder input (a zero-copy borrowed
// decode); it is stored so materialization can deep-clone. Must be called after
// sorted/byID/sortedIDs are populated. names must be parallel to sorted
// (names[i] is the name of entry sorted[i]).
func (m *indexMaps[T]) finalizeNames(names []string, borrowed bool) error {
	m.names = names
	m.namesBorrowed = borrowed

	if !m.hasDuplicateID() {
		// No collision: byName stays nil. A duplicate name is impossible here
		// because equal names hash to equal IDs, which would be a duplicate ID.
		return nil
	}

	// Collision present. Build name → ordinal; a repeated name (necessarily
	// within a collided ID group, since hash.ID is deterministic) is rejected.
	byName := make(map[string]int, len(names))
	for i, name := range names {
		if _, dup := byName[name]; dup {
			return errs.ErrDuplicateMetricName
		}
		byName[name] = i
	}
	m.byName = byName

	return nil
}

// cloneNamesOwned returns an owning copy of names. When the source names alias a
// borrowed backing array (a zero-copy borrowed decode), each string is
// deep-copied via strings.Clone so the result never references the borrowed
// array; otherwise a
// shallow slices.Clone suffices because the strings already own their bytes and
// are immutable. Used by the materialization paths so materialized objects are
// always owning regardless of how the source blob was decoded.
func cloneNamesOwned(names []string, borrowed bool) []string {
	if names == nil {
		return nil
	}
	if !borrowed {
		return slices.Clone(names)
	}

	out := make([]string, len(names))
	for i, s := range names {
		out[i] = strings.Clone(s)
	}

	return out
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

// TimestampEncoding reports whether timestamps are raw: it returns format.TypeRaw
// for raw timestamps and format.TypeDelta for every delta-of-delta encoding,
// including format.TypeDeltaPacked. Use TimestampEncodingType for the exact
// encoding stored in the header.
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

// getOrdinal returns the ordinal (index into `sorted`) of the FIRST entry with
// the given MetricID, or (-1, false) if absent. On V2 this is the leftmost
// binary-search result; on V1/text it is the first-wins byID map.
func (m indexMaps[T]) getOrdinal(metricID uint64) (int, bool) {
	if m.sortedIDs != nil {
		i, found := slices.BinarySearch(m.sortedIDs, metricID)
		if !found {
			return -1, false
		}

		return i, true
	}

	ord, ok := m.byID[metricID]

	return ord, ok
}

// MetricCount returns the number of metrics in the blob, counting one per index
// entry — a within-blob collision (two names, one ID) counts as two.
func (m indexMaps[T]) MetricCount() int {
	return len(m.sorted)
}

// HasMetricID checks if the given metric ID exists in the blob.
func (m indexMaps[T]) HasMetricID(metricID uint64) bool {
	_, ok := m.getOrdinal(metricID)

	return ok
}

// HasMetricName checks if the given metric name exists in the blob.
//
// Behavior:
//   - byName present (collision): direct name lookup.
//   - names retained, no collision: hash the query, locate the candidate entry,
//     and string-compare against the retained stored name so a query that only
//     hash-collides with a stored name is correctly rejected.
//   - no names payload: hash the query to an ID and check existence.
func (m indexMaps[T]) HasMetricName(metricName string) bool {
	if m.byName != nil {
		_, ok := m.byName[metricName]

		return ok
	}

	if m.names != nil {
		ord, ok := m.getOrdinal(hash.ID(metricName))

		return ok && m.names[ord] == metricName
	}

	return m.HasMetricID(hash.ID(metricName))
}

// MetricIDs returns a slice of all metric IDs in the blob, one per index entry
// in index order. A collided ID appears once per colliding entry.
// The slice is newly allocated to prevent external modification.
func (m indexMaps[T]) MetricIDs() []uint64 {
	ids := make([]uint64, len(m.sorted))
	for i := range m.sorted {
		ids[i] = m.sorted[i].GetMetricID()
	}

	return ids
}

// MetricNames returns a slice of all metric names in the blob in index order.
// Returns an empty slice if the blob has no metric names payload.
// The slice is newly allocated to prevent external modification.
func (m indexMaps[T]) MetricNames() []string {
	if m.names == nil {
		return []string{}
	}

	return slices.Clone(m.names)
}

// GetByID returns the index entry for the given metric ID, resolving a collided
// ID to the FIRST entry in index order.
// Returns (entry, true) if found, or (zero-value, false) if not found.
func (m indexMaps[T]) GetByID(metricID uint64) (T, bool) {
	ord, ok := m.getOrdinal(metricID)
	if !ok {
		var zero T

		return zero, false
	}

	return m.sorted[ord], true
}

// GetByName returns the index entry for the given metric name.
//
// Behavior mirrors HasMetricName: byName map on collision; hash + string-compare
// on a retained-names no-collision blob; hash + ID lookup when the blob carries
// no names payload.
//
// Returns (entry, true) if found, or (zero-value, false) if not found.
func (m indexMaps[T]) GetByName(metricName string) (T, bool) {
	if m.byName != nil {
		ord, ok := m.byName[metricName]
		if !ok {
			var zero T

			return zero, false
		}

		return m.sorted[ord], true
	}

	if m.names != nil {
		ord, ok := m.getOrdinal(hash.ID(metricName))
		if ok && m.names[ord] == metricName {
			return m.sorted[ord], true
		}

		var zero T

		return zero, false
	}

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
	for _, e := range m.sorted {
		if !fn(e) {
			return
		}
	}
}

// At returns the entry at position i for direct indexed access (index order).
// Panics if index is out of range.
func (m indexMaps[T]) At(i int) T {
	return m.sorted[i]
}

// IsEmpty returns whether the index contains no entries.
func (m indexMaps[T]) IsEmpty() bool {
	return len(m.sorted) == 0
}
