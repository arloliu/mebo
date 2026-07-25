package blob

import (
	"slices"

	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// MaterializedTextBlob provides O(1) random access to all text data points.
// Created by calling TextBlob.Materialize().
//
// Safe for concurrent read access after creation.
// All data is decoded and stored in memory, providing constant-time access
// at the cost of memory (~32 bytes per data point for text data).
//
// Use when:
//   - You need random access to many metrics
//   - You will access each metric multiple times
//   - Memory is available for pre-decoded data
//
// Example:
//
//	material := blob.Materialize()
//	val, ok := material.ValueAt(metricID, 500)  // O(1), ~5ns
//	ts, ok := material.TimestampAt(metricID, 500)
//
// MaterializedTextBlob uses an ordinal-keyed representation: metrics are stored
// in index order, a within-blob collision keeps both entries, and a collided ID
// resolves to the first entry. Name lookups mirror the raw blob: the byName map
// is built only when a collision is actually observed, otherwise membership is
// answered by hash + retained-name string-compare.
type MaterializedTextBlob struct {
	metrics []materializedTextMetric // index order, one per entry
	ids     []uint64                 // parallel MetricID per metric
	byID    map[uint64]int           // first-wins MetricID → ordinal
	byName  map[string]int           // metricName → ordinal; built ONLY on collision
	names   []string                 // ordered names parallel to metrics; nil if no names payload
}

type materializedTextMetric struct {
	timestamps []int64
	values     []string
	tags       []string
}

// ordinalByID returns the ordinal of the first entry with the given MetricID.
func (m MaterializedTextBlob) ordinalByID(metricID uint64) (int, bool) {
	ord, ok := m.byID[metricID]

	return ord, ok
}

// ordinalByName resolves a metric name to its ordinal, mirroring
// indexMaps[T].HasMetricName's three cases: byName present (collision) is a
// direct lookup; names retained with no collision hashes the query and
// string-compares against the retained name for exact membership; with no
// names payload at all, membership can only be decided by hash, so it falls
// back to byID.
func (m MaterializedTextBlob) ordinalByName(metricName string) (int, bool) {
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

// Materialize decodes all metrics in the blob and returns a MaterializedTextBlob
// that supports O(1) random access to all data points.
//
// Performance:
//   - Materialization cost: ~100μs per metric (one-time)
//   - Random access: ~5ns (O(1), array indexing)
//   - Memory: ~32 bytes per data point (varies with string length)
//
// Use this when:
//   - You need random access to many metrics
//   - You will access each metric multiple times
//   - Memory is available (~32 bytes per data point)
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
func (b TextBlob) Materialize() MaterializedTextBlob {
	n := b.MetricCount()
	material := MaterializedTextBlob{
		metrics: make([]materializedTextMetric, 0, n),
		ids:     make([]uint64, 0, n),
	}

	// Decode all metrics in index order (one materialized metric per entry, so a
	// within-blob collision keeps both entries rather than merging them).
	b.index.ForEach(func(entry section.TextIndexEntry) bool {
		material.metrics = append(material.metrics, b.materializeMetricData(entry))
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
	// backing) and build the name lookup from them. The byName map is built only
	// when a collision was observed, mirroring the raw blob.
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

// materializeMetricData decodes a single entry's timestamps/values/tags.
func (b TextBlob) materializeMetricData(entry section.TextIndexEntry) materializedTextMetric {
	count := int(entry.Count)
	timestamps := make([]int64, count)
	values := make([]string, count)
	var tags []string
	if b.HasTag() {
		tags = make([]string, count)
	}

	idx := 0
	for ts := range b.allTimestampsFromEntry(entry) {
		timestamps[idx] = ts
		idx++
	}

	idx = 0
	for val := range b.allValuesFromEntry(entry) {
		values[idx] = val
		idx++
	}

	if b.HasTag() {
		idx = 0
		for tag := range b.allTagsFromEntry(entry) {
			tags[idx] = tag
			idx++
		}
	}

	return materializedTextMetric{
		timestamps: timestamps,
		values:     values,
		tags:       tags,
	}
}

// ValueAt returns the text value at the specified index for the given metric ID.
// Returns ("", false) if the metric ID is not found or index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedTextBlob) ValueAt(metricID uint64, index int) (string, bool) {
	ord, ok := m.ordinalByID(metricID)
	if !ok {
		return "", false
	}
	metric := m.metrics[ord]

	if index < 0 || index >= len(metric.values) {
		return "", false
	}

	return metric.values[index], true
}

// TimestampAt returns the timestamp at the specified index for the given metric ID.
// Returns (0, false) if the metric ID is not found or index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedTextBlob) TimestampAt(metricID uint64, index int) (int64, bool) {
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
func (m MaterializedTextBlob) TagAt(metricID uint64, index int) (string, bool) {
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

// ValueAtByName returns the text value at the specified index by metric name.
// Returns ("", false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedTextBlob) ValueAtByName(metricName string, index int) (string, bool) {
	ord, ok := m.ordinalByName(metricName)
	if !ok {
		return "", false
	}
	metric := m.metrics[ord]

	if index < 0 || index >= len(metric.values) {
		return "", false
	}

	return metric.values[index], true
}

// TimestampAtByName returns the timestamp at the specified index by metric name.
// Returns (0, false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedTextBlob) TimestampAtByName(metricName string, index int) (int64, bool) {
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
func (m MaterializedTextBlob) TagAtByName(metricName string, index int) (string, bool) {
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
func (m MaterializedTextBlob) DataPointCount(metricID uint64) int {
	ord, ok := m.ordinalByID(metricID)
	if !ok {
		return 0
	}

	return len(m.metrics[ord].values)
}

// DataPointCountByName returns the number of data points for the given metric name.
// Returns 0 if the metric name is not found.
func (m MaterializedTextBlob) DataPointCountByName(metricName string) int {
	ord, ok := m.ordinalByName(metricName)
	if !ok {
		return 0
	}

	return len(m.metrics[ord].values)
}

// MetricCount returns the number of metrics in the materialized blob (one per
// index entry — a within-blob collision counts as two, since each colliding
// name keeps its own entry).
func (m MaterializedTextBlob) MetricCount() int {
	return len(m.metrics)
}

// HasMetricID checks if the materialized blob contains the given metric ID.
func (m MaterializedTextBlob) HasMetricID(metricID uint64) bool {
	_, ok := m.ordinalByID(metricID)
	return ok
}

// HasMetricName checks if the materialized blob contains the given metric name.
// Mirrors the raw blob's three-case resolution (see indexMaps[T].HasMetricName):
// a built byName map (collision) is a direct lookup; retained names with no
// collision hash the query and string-compare against the retained name for
// exact membership; with no names payload at all, membership is decided by
// hash alone.
func (m MaterializedTextBlob) HasMetricName(metricName string) bool {
	_, ok := m.ordinalByName(metricName)
	return ok
}

// MetricIDs returns a slice of all metric IDs in the materialized blob, one per
// entry in index order (a collided ID appears once per colliding entry, since
// each colliding name keeps its own entry).
func (m MaterializedTextBlob) MetricIDs() []uint64 {
	return slices.Clone(m.ids)
}

// MetricNames returns a slice of all metric names in the materialized blob in
// index order. Returns nil if no metric names are available.
func (m MaterializedTextBlob) MetricNames() []string {
	if len(m.names) == 0 {
		return nil
	}

	return slices.Clone(m.names)
}

// MaterializedTextMetric represents a single materialized text metric with O(1) random access.
// Created by calling TextBlob.MaterializeMetric().
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
type MaterializedTextMetric struct {
	MetricID   uint64
	Timestamps []int64
	Values     []string
	Tags       []string // Empty if tags not enabled
}

// MaterializeMetric decodes a single metric for O(1) random access.
//
// Performance:
//   - Materialization cost: ~100μs (one-time)
//   - Random access: ~5ns (O(1), array indexing)
//   - Memory: ~32 bytes per data point (varies with string length)
//
// Use this when:
//   - You only need to access one or few metrics
//   - You want fine-grained control over memory usage
//   - You want to materialize metrics on demand
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
//	val, _ := metric.ValueAt(500)  // O(1), ~5ns
//	ts, _ := metric.TimestampAt(500)
func (b TextBlob) MaterializeMetric(metricID uint64) (MaterializedTextMetric, bool) {
	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return MaterializedTextMetric{}, false
	}

	return b.materializeEntry(entry), true
}

// materializeEntry decodes a single index entry into a MaterializedTextMetric.
// It works directly from the resolved entry so a name-resolved collision does
// not re-resolve by ID, which could otherwise pick the wrong collision member.
func (b TextBlob) materializeEntry(entry section.TextIndexEntry) MaterializedTextMetric {
	data := b.materializeMetricData(entry)

	return MaterializedTextMetric{
		MetricID:   entry.MetricID,
		Timestamps: data.timestamps,
		Values:     data.values,
		Tags:       data.tags,
	}
}

// MaterializeMetricByName decodes a single metric by name for O(1) random access.
// Returns (MaterializedTextMetric{}, false) if the metric name is not found.
//
// Example:
//
//	metric, ok := blob.MaterializeMetricByName("status")
//	if ok {
//	    val, _ := metric.ValueAt(500)
//	}
func (b TextBlob) MaterializeMetricByName(metricName string) (MaterializedTextMetric, bool) {
	entry, ok := b.lookupMetricEntry(metricName)
	if !ok {
		return MaterializedTextMetric{}, false
	}

	// Materialize the exact entry GetByName resolved, rather than re-resolving by
	// ID, which could pick the wrong collision member.
	return b.materializeEntry(entry), true
}

// ValueAt returns the text value at the specified index.
// Returns ("", false) if index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedTextMetric) ValueAt(index int) (string, bool) {
	if index < 0 || index >= len(m.Values) {
		return "", false
	}

	return m.Values[index], true
}

// TimestampAt returns the timestamp at the specified index.
// Returns (0, false) if index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedTextMetric) TimestampAt(index int) (int64, bool) {
	if index < 0 || index >= len(m.Timestamps) {
		return 0, false
	}

	return m.Timestamps[index], true
}

// TagAt returns the tag at the specified index.
// Returns ("", false) if index is out of bounds.
// Returns ("", true) if tags are not enabled but the index is valid.
//
// This is an O(1) operation (~5ns).
func (m MaterializedTextMetric) TagAt(index int) (string, bool) {
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
func (m MaterializedTextMetric) Len() int {
	return len(m.Values)
}
