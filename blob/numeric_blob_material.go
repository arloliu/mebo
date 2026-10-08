package blob

import (
	"slices"

	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// MaterializedNumericBlob provides O(1) random access to all data points.
// Created by calling NumericBlob.Materialize().
//
// Safe for concurrent read access after creation.
// All data is decoded and stored in memory, providing constant-time access
// at the cost of memory (~16 bytes per data point).
//
// Use when:
//   - You need random access to many metrics
//   - You will access each metric multiple times
//   - Memory is available for pre-decoded data
//
// Example:
//
//	material := blob.Materialize()
//	val, ok := material.ValueAt(metricID, 500)  // O(1), slice indexing
//	ts, ok := material.TimestampAt(metricID, 500)
//
// MaterializedNumericBlob uses an ordinal-keyed representation: metrics are stored
// in index order, a within-blob collision keeps both entries (each index entry
// counts individually, even when two names share one MetricID), and a collided
// MetricID resolves to the first entry in index order. Name lookups mirror the
// raw blob: the byName lookup table is built only when a collision is actually
// observed; otherwise membership is answered by hashing the name and comparing
// against the retained name string.
type MaterializedNumericBlob struct {
	metrics []materializedNumericMetric // index order, one per entry
	ids     []uint64                    // parallel MetricID per metric
	byID    map[uint64]int              // first-wins MetricID → ordinal
	byName  map[string]int              // metricName → ordinal; built ONLY on collision
	names   []string                    // ordered names parallel to metrics; nil if no names payload
}

type materializedNumericMetric struct {
	timestamps []int64
	values     []float64
	tags       []string
}

// ordinalByID returns the ordinal of the first entry with the given MetricID.
func (m MaterializedNumericBlob) ordinalByID(metricID uint64) (int, bool) {
	ord, ok := m.byID[metricID]

	return ord, ok
}

// ordinalByName resolves a metric name to its ordinal, mirroring
// indexMaps[T].HasMetricName's three cases: byName present (collision) is a
// direct lookup; names retained with no collision hashes the query and
// string-compares against the retained name for exact membership; with no
// names payload at all, membership can only be decided by hash, so it falls
// back to byID.
func (m MaterializedNumericBlob) ordinalByName(metricName string) (int, bool) {
	if m.byName != nil {
		ord, ok := m.byName[metricName]

		return ord, ok
	}

	if m.names != nil {
		ord, ok := m.byID[hash.ID(metricName)]
		if ok && m.names[ord] == metricName {
			return ord, true
		}

		return -1, false
	}

	return m.ordinalByID(hash.ID(metricName))
}

// Materialize decodes all metrics in the blob and returns a MaterializedNumericBlob
// that supports O(1) random access to all data points.
//
// Performance:
//   - Materialization cost: about 2–5 ns per point without tags (ALP to Chimp values),
//     plus one string copy per point with tags
//     (measured 2026-10 on 150-point metrics with shared DeltaPacked timestamps, uncompressed, little-endian)
//   - Random access: about 1 ns per accessor (O(1), slice indexing)
//   - Memory: ~16 bytes per data point
//
// Use this when:
//   - You need random access to many metrics
//   - You will access each metric multiple times
//   - Memory is available (~16 bytes per data point)
//
// For single-metric access, consider MaterializeMetric() for lower memory overhead.
//
// Example:
//
//	material := blob.Materialize()
//	// Access any data point in O(1) time
//	val, ok := material.ValueAt(metricID, 500)
//	ts, ok := material.TimestampAt(metricID, 500)
//	tag, ok := material.TagAt(metricID, 500)
func (b NumericBlob) Materialize() MaterializedNumericBlob {
	n := b.MetricCount()
	material := MaterializedNumericBlob{
		metrics: make([]materializedNumericMetric, 0, n),
		ids:     make([]uint64, 0, n),
	}

	// Decode all metrics in index order (one materialized metric per entry, so a
	// within-blob collision keeps both entries instead of merging them).
	b.index.ForEach(func(entry section.NumericIndexEntry) bool {
		m := b.materializeEntry(entry)
		material.metrics = append(material.metrics, materializedNumericMetric{
			timestamps: m.Timestamps,
			values:     m.Values,
			tags:       m.Tags,
		})
		material.ids = append(material.ids, entry.MetricID)

		return true
	})

	// First-wins MetricID → ordinal.
	material.byID = make(map[uint64]int, len(material.ids))
	for i, id := range material.ids {
		if _, ok := material.byID[id]; !ok {
			material.byID[id] = i
		}
	}

	// Retain ordered names (cloned for ownership; deep-cloned when the source blob
	// borrowed its names so the materialized copy never aliases the borrowed
	// backing) and build the name lookup from them. byName is built only when a
	// collision was observed, mirroring the raw blob's lazy behavior.
	if b.index.names != nil {
		material.names = cloneNamesOwned(b.index.names, b.index.namesBorrowed)
		if b.index.byName != nil {
			material.byName = make(map[string]int, len(material.names))
			for i, name := range material.names {
				material.byName[name] = i
			}
		}
	}

	return material
}

// ValueAt returns the value at the specified index for the given metric ID.
// Returns (0, false) if the metric ID is not found or index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericBlob) ValueAt(metricID uint64, index int) (float64, bool) {
	ord, ok := m.ordinalByID(metricID)
	if !ok {
		return 0, false
	}
	metric := m.metrics[ord]

	if index < 0 || index >= len(metric.values) {
		return 0, false
	}

	return metric.values[index], true
}

// TimestampAt returns the timestamp at the specified index for the given metric ID.
// Returns (0, false) if the metric ID is not found or index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericBlob) TimestampAt(metricID uint64, index int) (int64, bool) {
	ord, ok := m.ordinalByID(metricID)
	if !ok {
		return 0, false
	}
	metric := m.metrics[ord]

	if index < 0 || index >= len(metric.timestamps) {
		return 0, false
	}

	return metric.timestamps[index], true
}

// TagAt returns the tag at the specified index for the given metric ID.
// Returns ("", false) if the metric ID is not found or index is out of bounds.
// Returns ("", true) if tags are not enabled but the metric and index are valid.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericBlob) TagAt(metricID uint64, index int) (string, bool) {
	ord, ok := m.ordinalByID(metricID)
	if !ok {
		return "", false
	}
	metric := m.metrics[ord]

	// If tags weren't enabled, return empty string
	if len(metric.tags) == 0 {
		return "", index >= 0 && index < len(metric.values)
	}

	if index < 0 || index >= len(metric.tags) {
		return "", false
	}

	return metric.tags[index], true
}

// ValueAtByName returns the value at the specified index by metric name.
// Returns (0, false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedNumericBlob) ValueAtByName(metricName string, index int) (float64, bool) {
	ord, ok := m.ordinalByName(metricName)
	if !ok {
		return 0, false
	}
	metric := m.metrics[ord]

	if index < 0 || index >= len(metric.values) {
		return 0, false
	}

	return metric.values[index], true
}

// TimestampAtByName returns the timestamp at the specified index by metric name.
// Returns (0, false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedNumericBlob) TimestampAtByName(metricName string, index int) (int64, bool) {
	ord, ok := m.ordinalByName(metricName)
	if !ok {
		return 0, false
	}
	metric := m.metrics[ord]

	if index < 0 || index >= len(metric.timestamps) {
		return 0, false
	}

	return metric.timestamps[index], true
}

// TagAtByName returns the tag at the specified index by metric name.
// Returns ("", false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedNumericBlob) TagAtByName(metricName string, index int) (string, bool) {
	ord, ok := m.ordinalByName(metricName)
	if !ok {
		return "", false
	}
	metric := m.metrics[ord]

	if len(metric.tags) == 0 {
		return "", index >= 0 && index < len(metric.values)
	}

	if index < 0 || index >= len(metric.tags) {
		return "", false
	}

	return metric.tags[index], true
}

// DataPointCount returns the number of data points for the given metric ID.
// Returns 0 if the metric ID is not found.
func (m MaterializedNumericBlob) DataPointCount(metricID uint64) int {
	ord, ok := m.ordinalByID(metricID)
	if !ok {
		return 0
	}

	return len(m.metrics[ord].values)
}

// DataPointCountByName returns the number of data points for the given metric name.
// Returns 0 if the metric name is not found.
func (m MaterializedNumericBlob) DataPointCountByName(metricName string) int {
	ord, ok := m.ordinalByName(metricName)
	if !ok {
		return 0
	}

	return len(m.metrics[ord].values)
}

// MetricCount returns the number of metrics in the materialized blob (one per
// index entry — a within-blob collision counts as two, since each name/ID pair
// is counted individually).
func (m MaterializedNumericBlob) MetricCount() int {
	return len(m.metrics)
}

// HasMetricID checks if the materialized blob contains the given metric ID.
func (m MaterializedNumericBlob) HasMetricID(metricID uint64) bool {
	_, ok := m.ordinalByID(metricID)
	return ok
}

// HasMetricName checks if the materialized blob contains the given metric name.
// Mirrors the raw blob's three-case resolution (see indexMaps[T].HasMetricName):
// a built byName map (collision) is a direct lookup; retained names with no
// collision hash the query and string-compare against the retained name for
// exact membership; with no names payload at all, membership is decided by
// hash alone.
func (m MaterializedNumericBlob) HasMetricName(metricName string) bool {
	_, ok := m.ordinalByName(metricName)
	return ok
}

// MetricIDs returns a slice of all metric IDs in the materialized blob, one per
// entry in index order (a collided ID appears once per colliding entry, since
// entries are counted individually rather than deduplicated by ID).
func (m MaterializedNumericBlob) MetricIDs() []uint64 {
	return slices.Clone(m.ids)
}

// MetricNames returns a slice of all metric names in the materialized blob in
// index order. Returns nil if no metric names are available.
func (m MaterializedNumericBlob) MetricNames() []string {
	if len(m.names) == 0 {
		return nil
	}

	return slices.Clone(m.names)
}

// MaterializedNumericMetric represents a single materialized numeric metric with O(1) random access.
// Created by calling NumericBlob.MaterializeMetric().
//
// All data is decoded and stored in memory, providing constant-time access.
//
// Example:
//
//	metric, ok := blob.MaterializeMetric(metricID)
//	if ok {
//	    val, _ := metric.ValueAt(500)  // O(1)
//	    ts, _ := metric.TimestampAt(500)
//	}
type MaterializedNumericMetric struct {
	MetricID   uint64
	Timestamps []int64
	Values     []float64
	Tags       []string // Empty if tags not enabled
}

// MaterializeMetric decodes a single metric for O(1) random access.
//
// Performance:
//   - Materialization cost: about 0.4 µs for a 150-point metric with ALP values and shared timestamps,
//     about 2–5 ns per point without tags (ALP to Chimp values), plus one string copy per point with tags
//     (measured 2026-10)
//   - Random access: about 1 ns per accessor (O(1), slice indexing)
//   - Memory: ~16 bytes per data point
//
// Use this when:
//   - You only need to access one or few metrics
//   - You want fine-grained control over memory usage
//   - You want to materialize metrics on demand
//   - The copy must outlive the blob; otherwise NumericBlob.Metric and its Materialize serve the same lookups
//
// For accessing many metrics, consider Materialize() instead for one-time decode overhead.
//
// Example:
//
//	metric, ok := blob.MaterializeMetric(metricID)
//	if !ok {
//	    // Metric not found
//	    return
//	}
//	val, _ := metric.ValueAt(500)  // O(1), slice indexing
//	ts, _ := metric.TimestampAt(500)
func (b NumericBlob) MaterializeMetric(metricID uint64) (MaterializedNumericMetric, bool) {
	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return MaterializedNumericMetric{}, false
	}

	return b.materializeEntry(entry), true
}

// materializeEntry decodes a single index entry into a MaterializedNumericMetric.
// It works directly from the resolved entry so callers that already resolved the
// correct entry by name (a collision) do not re-resolve by ID, which could pick
// the wrong colliding entry.
func (b NumericBlob) materializeEntry(entry section.NumericIndexEntry) MaterializedNumericMetric {
	// Pre-allocate slices with exact size for direct indexing (no append overhead)
	count := entry.Count
	timestamps := make([]int64, count)
	values := make([]float64, count)

	// Fast path: use cached shared timestamps if available
	if cached := b.sharedTs.lookup(entry.TimestampOffset); cached != nil {
		timestamps = timestamps[:copy(timestamps, cached)]
	} else {
		tsBytes := b.tsPayload[entry.TimestampOffset : entry.TimestampOffset+entry.TimestampLength]
		tsProduced := b.decodeTimestampsSlice(tsBytes, count, timestamps)
		timestamps = timestamps[:tsProduced]
	}

	valBytes := b.valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength]
	valProduced := b.decodeValuesSlice(valBytes, count, values)
	values = values[:valProduced]

	var tags []string
	if b.HasTag() {
		tags = make([]string, count)
		idx := 0
		for tag := range b.allTagsFromEntry(entry) {
			tags[idx] = tag
			idx++
		}

		tags = tags[:idx]
	}

	// Keep only complete rows when a corrupt stream decoded short, so
	// TimestampAt, ValueAt and TagAt agree on every index.
	timestamps, values, tags = alignMemberRows(timestamps, values, tags, b.HasTag())

	return MaterializedNumericMetric{
		MetricID:   entry.MetricID,
		Timestamps: timestamps,
		Values:     values,
		Tags:       tags,
	}
}

// MaterializeMetricByName decodes a single metric by name for O(1) random access.
// Returns (MaterializedNumericMetric{}, false) if the metric name is not found.
//
// Example:
//
//	metric, ok := blob.MaterializeMetricByName("cpu.usage")
//	if ok {
//	    val, _ := metric.ValueAt(500)
//	}
func (b NumericBlob) MaterializeMetricByName(metricName string) (MaterializedNumericMetric, bool) {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return MaterializedNumericMetric{}, false
	}

	// Materialize the exact entry GetByName resolved, NOT a re-lookup by ID (which
	// would resolve the first colliding entry, discarding the name resolution).
	// materializeEntry decodes directly from the resolved entry.
	return b.materializeEntry(entry), true
}

// ValueAt returns the value at the specified index.
// Returns (0, false) if index is out of bounds.
//
// This is an O(1) operation (about 1 ns, slice indexing).
func (m MaterializedNumericMetric) ValueAt(index int) (float64, bool) {
	if index < 0 || index >= len(m.Values) {
		return 0, false
	}

	return m.Values[index], true
}

// TimestampAt returns the timestamp at the specified index.
// Returns (0, false) if index is out of bounds.
//
// This is an O(1) operation (about 1 ns, slice indexing).
func (m MaterializedNumericMetric) TimestampAt(index int) (int64, bool) {
	if index < 0 || index >= len(m.Timestamps) {
		return 0, false
	}

	return m.Timestamps[index], true
}

// TagAt returns the tag at the specified index.
// Returns ("", false) if index is out of bounds.
// Returns ("", true) if tags are not enabled but the index is valid.
//
// This is an O(1) operation (about 1 ns, slice indexing).
func (m MaterializedNumericMetric) TagAt(index int) (string, bool) {
	// If tags weren't enabled, return empty string
	if len(m.Tags) == 0 {
		return "", index >= 0 && index < len(m.Values)
	}

	if index < 0 || index >= len(m.Tags) {
		return "", false
	}

	return m.Tags[index], true
}

// Len returns the number of data points in the materialized metric.
func (m MaterializedNumericMetric) Len() int {
	return len(m.Values)
}
