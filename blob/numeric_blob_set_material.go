package blob

import (
	"slices"

	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// MaterializedNumericBlobSet provides O(1) random access to all data points across all blobs.
// Created by calling NumericBlobSet.Materialize().
//
// All data from all blobs is decoded and flattened into continuous arrays,
// providing constant-time access at the cost of memory (~16 bytes per data point).
//
// Safe for concurrent read access after creation.
//
// Use when:
//   - You need random access to many metrics across multiple time windows
//   - You will access each metric multiple times
//   - Memory is available for pre-decoded data
//
// Example:
//
//	blobSet, _ := NewNumericBlobSet(blobs)
//	material := blobSet.Materialize()
//	val, ok := material.ValueAt(metricID, 1500)  // O(1), ~5ns (could be in any blob)
//	ts, ok := material.TimestampAt(metricID, 2500)
//
// MaterializedNumericBlobSet keys its logical metrics by the set's logical identity: by
// name when the set is names-bearing, by MetricID otherwise. metrics holds one entry
// per logical metric in canonical order; ids/names are parallel (names[k] is
// "" for an id-only metric). byName resolves a name exactly; byID resolves an ID to its
// FIRST logical metric (a collided ID → the first colliding name's series).
type MaterializedNumericBlobSet struct {
	metrics []materializedNumericMetricSet
	ids     []uint64
	names   []string
	byName  map[string]int
	byID    map[uint64]int
}

type materializedNumericMetricSet struct {
	timestamps []int64   // All timestamps from all blobs, concatenated
	values     []float64 // All values from all blobs, concatenated
	tags       []string  // All tags from all blobs, concatenated (empty if tags disabled)
}

// Materialize decodes all metrics from all blobs in the set and returns a
// MaterializedNumericBlobSet that supports O(1) random access.
//
// Performance:
//   - Materialization cost: ~100μs per metric per blob (one-time)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~16 bytes per data point × total data points across all blobs
//
// Use this when:
//   - You need random access to many metrics across the entire time range
//   - You will access each metric multiple times
//   - Memory is available (~16 bytes per data point)
//
// Example:
//
//	blobSet, _ := NewNumericBlobSet(blobs)
//	material := blobSet.Materialize()
//	// Access any data point across all blobs in O(1) time
//	val, ok := material.ValueAt(metricID, 1500)  // Could be in blob 2
//	ts, ok := material.TimestampAt(metricID, 2500) // Could be in blob 3
func (s *NumericBlobSet) Materialize() MaterializedNumericBlobSet {
	if len(s.blobs) == 0 {
		return MaterializedNumericBlobSet{
			byName: make(map[string]int),
			byID:   make(map[uint64]int),
		}
	}

	// Step 1: Build the logical-identity plan (canonical order, name/id keyed), which
	// groups member blobs' entries by logical metric name/id. The materialized path
	// decodes everything, so it builds the plan unconditionally rather than lazily
	// deferring it until a collision is observed. slotOf routes a member entry to its
	// logical metric.
	plan := buildLogicalPlan(func(i int) *indexMaps[section.NumericIndexEntry] { return &s.blobs[i].index }, len(s.blobs))
	slotOf := func(blob *NumericBlob, ord int) int {
		if blob.index.names != nil {
			return plan.byName[blob.index.names[ord]]
		}

		return plan.byID[blob.index.sorted[ord].MetricID]
	}

	// Step 2: Per-slot capacity + tag detection.
	capacities := make([]int, len(plan.ids))
	hasTags := false
	for i := range s.blobs {
		blob := &s.blobs[i]
		if !hasTags && blob.HasTag() {
			hasTags = true
		}
		for ord := range blob.index.sorted {
			capacities[slotOf(blob, ord)] += blob.index.sorted[ord].Count
		}
	}

	// Step 3: Pre-allocate per-slot slices with exact capacity. Deep-clone names
	// off any borrowed member backing so the materialized set is fully owning and
	// never aliases a member blob's borrowed string data.
	ownNames, ownByName := ownSetNames(plan.names, plan.byName,
		func(i int) *indexMaps[section.NumericIndexEntry] { return &s.blobs[i].index }, len(s.blobs))
	material := MaterializedNumericBlobSet{
		metrics: make([]materializedNumericMetricSet, len(plan.ids)),
		ids:     plan.ids,
		names:   ownNames,
		byName:  ownByName,
		byID:    plan.byID,
	}
	for slot := range material.metrics {
		material.metrics[slot] = materializedNumericMetricSet{
			timestamps: make([]int64, 0, capacities[slot]),
			values:     make([]float64, 0, capacities[slot]),
		}
		if hasTags {
			material.metrics[slot].tags = make([]string, 0, capacities[slot])
		}
	}

	// Step 4: Iterate through blobs in chronological order, appending data per slot.
	for i := range s.blobs {
		blob := &s.blobs[i]

		for ord := range blob.index.sorted {
			entry := blob.index.sorted[ord]
			slot := slotOf(blob, ord)
			metricSet := material.metrics[slot]
			count := entry.Count

			// Decode timestamps: extend slice and decode directly into tail
			tsOff := len(metricSet.timestamps)
			if cached, ok := blob.sharedTsCache[entry.TimestampOffset]; ok {
				metricSet.timestamps = append(metricSet.timestamps, cached...)
			} else {
				tsBytes := blob.tsPayload[entry.TimestampOffset : entry.TimestampOffset+entry.TimestampLength]
				metricSet.timestamps = metricSet.timestamps[:tsOff+count]
				tsProduced := blob.decodeTimestampsSlice(tsBytes, count, metricSet.timestamps[tsOff:])
				metricSet.timestamps = metricSet.timestamps[:tsOff+tsProduced]
			}

			// Decode values: extend slice and decode directly into tail
			valBytes := blob.valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength]
			valOff := len(metricSet.values)
			metricSet.values = metricSet.values[:valOff+count]
			valProduced := blob.decodeValuesSlice(valBytes, count, metricSet.values[valOff:])
			metricSet.values = metricSet.values[:valOff+valProduced]

			// Decode and append tags (if enabled)
			if hasTags && blob.HasTag() {
				for tag := range blob.allTagsFromEntry(entry) {
					metricSet.tags = append(metricSet.tags, tag)
				}
			} else if hasTags {
				// This blob doesn't have tags, but other blobs do
				// Fill with empty strings to maintain index alignment
				for range valProduced {
					metricSet.tags = append(metricSet.tags, "")
				}
			}

			metricSet.timestamps, metricSet.values, metricSet.tags = alignMemberRows(
				metricSet.timestamps, metricSet.values, metricSet.tags, hasTags)
			material.metrics[slot] = metricSet
		}
	}

	return material
}

// MaterializeMetric decodes a single metric by ID from all blobs in the set and returns
// a MaterializedNumericMetric for O(1) random access without needing to pass metric ID on each call.
//
// Unlike Materialize() which materializes all metrics, this method materializes only
// one metric, reducing memory usage when you only need specific metrics.
//
// Parameters:
//   - metricID: The metric ID to materialize
//
// Returns:
//   - MaterializedNumericMetric: The materialized metric with direct access methods
//   - bool: false if the metric is not found in any blob
//
// Performance:
//   - Materialization cost: ~100μs (one-time, for one metric across all blobs)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~16 bytes per data point × total data points for this metric
//
// Example:
//
//	blobSet, _ := NewNumericBlobSet(blobs)
//	metric, ok := blobSet.MaterializeMetric(metricID)
//	if ok {
//	    val, _ := metric.ValueAt(150)      // O(1) access, no metric ID needed
//	    ts, _ := metric.TimestampAt(250)   // O(1) access
//	}
func (s *NumericBlobSet) MaterializeMetric(metricID uint64) (MaterializedNumericMetric, bool) {
	// A collided ID resolves to the first colliding name's logical metric:
	// gate members so a cross-member A/H + B/H pair never concatenates into A+B.
	targetName, collided := s.identity.resolveID(metricID)
	resolve := func(blob *NumericBlob) (section.NumericIndexEntry, bool) {
		return blob.index.resolveEntry(metricID, targetName, collided)
	}

	return s.materializeMetricCore(metricID, resolve)
}

// materializeMetricCore decodes and concatenates a single logical metric across all
// members, selecting each member's contributing entry via resolve. resolve returns the
// member's index entry and whether the member contributes to this logical metric.
func (s *NumericBlobSet) materializeMetricCore(metricID uint64, resolve func(blob *NumericBlob) (section.NumericIndexEntry, bool)) (MaterializedNumericMetric, bool) {
	// Step 1: Check if metric exists in any blob and calculate total capacity
	capacity := 0
	for i := range s.blobs {
		blob := &s.blobs[i]
		if entry, ok := resolve(blob); ok {
			capacity += entry.Count
		}
	}

	// If metric not found in any blob, return false
	if capacity == 0 {
		return MaterializedNumericMetric{}, false
	}

	// Step 2: Check if any blob has tags enabled
	hasTags := false
	for i := range s.blobs {
		if s.blobs[i].HasTag() {
			hasTags = true
			break
		}
	}

	// Step 3: Pre-allocate slices with exact capacity
	timestamps := make([]int64, 0, capacity)
	values := make([]float64, 0, capacity)
	var tags []string
	if hasTags {
		tags = make([]string, 0, capacity)
	}

	// Step 4: Iterate through blobs in chronological order, appending data
	for i := range s.blobs {
		blob := &s.blobs[i]
		entry, ok := resolve(blob)
		if !ok {
			continue // This metric doesn't contribute to this logical metric
		}

		count := entry.Count

		// Decode timestamps: extend slice and decode directly into tail
		tsOff := len(timestamps)
		if cached, ok := blob.sharedTsCache[entry.TimestampOffset]; ok {
			timestamps = append(timestamps, cached...)
		} else {
			tsBytes := blob.tsPayload[entry.TimestampOffset : entry.TimestampOffset+entry.TimestampLength]
			timestamps = timestamps[:tsOff+count]
			tsProduced := blob.decodeTimestampsSlice(tsBytes, count, timestamps[tsOff:])
			timestamps = timestamps[:tsOff+tsProduced]
		}

		// Decode values: extend slice and decode directly into tail
		valBytes := blob.valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength]
		valOff := len(values)
		values = values[:valOff+count]
		valProduced := blob.decodeValuesSlice(valBytes, count, values[valOff:])
		values = values[:valOff+valProduced]

		// Decode and append tags (if enabled)
		if hasTags && blob.HasTag() {
			for tag := range blob.allTagsFromEntry(entry) {
				tags = append(tags, tag)
			}
		} else if hasTags {
			// This blob doesn't have tags, but other blobs do
			// Fill with empty strings to maintain index alignment
			for range valProduced {
				tags = append(tags, "")
			}
		}

		timestamps, values, tags = alignMemberRows(timestamps, values, tags, hasTags)
	}

	return MaterializedNumericMetric{
		MetricID:   metricID,
		Timestamps: timestamps,
		Values:     values,
		Tags:       tags,
	}, true
}

// MaterializeMetricByName decodes a single metric by name from all blobs in the set and returns
// a MaterializedNumericMetric for O(1) random access without needing to pass metric name on each call.
//
// Unlike Materialize() which materializes all metrics, this method materializes only
// one metric, reducing memory usage when you only need specific metrics.
//
// Parameters:
//   - metricName: The metric name to materialize
//
// Returns:
//   - MaterializedNumericMetric: The materialized metric with direct access methods
//   - bool: false if the metric is not found in any blob
//
// Performance:
//   - Materialization cost: ~100μs (one-time, for one metric across all blobs)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~16 bytes per data point × total data points for this metric
//
// Example:
//
//	blobSet, _ := NewNumericBlobSet(blobs)
//	metric, ok := blobSet.MaterializeMetricByName("cpu.usage")
//	if ok {
//	    val, _ := metric.ValueAt(150)      // O(1) access, no metric name needed
//	    ts, _ := metric.TimestampAt(250)   // O(1) access
//	}
func (s *NumericBlobSet) MaterializeMetricByName(metricName string) (MaterializedNumericMetric, bool) {
	// Step 1: Find the metric ID from the first blob that has this name.
	var metricID uint64
	found := false
	for i := range s.blobs {
		blob := &s.blobs[i]
		if entry, ok := blob.index.GetByName(metricName); ok {
			metricID = entry.MetricID
			found = true
			break
		}
	}

	if !found {
		return MaterializedNumericMetric{}, false
	}

	// Step 2: Gather THIS name's logical set metric — never re-resolving by ID,
	// which would collapse a collided name onto the first colliding name's series. Named
	// members match the name exactly; a stripped member attaches only when the name is
	// the first colliding name for this id.
	skipStripped := s.identity.excludesStripped(metricName)
	resolve := func(blob *NumericBlob) (section.NumericIndexEntry, bool) {
		return blob.index.resolveEntryByName(metricName, skipStripped)
	}

	return s.materializeMetricCore(metricID, resolve)
}

// ValueAt returns the value at the specified global index for the given metric ID.
// Returns (0, false) if the metric ID is not found or index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericBlobSet) metricByID(metricID uint64) (materializedNumericMetricSet, bool) {
	slot, ok := m.byID[metricID]
	if !ok {
		return materializedNumericMetricSet{}, false
	}

	return m.metrics[slot], true
}

// slotByName resolves a metric name to its logical slot. A named slot matches
// exactly. Otherwise the name can only refer to data from names-free members,
// whose slots are keyed by ID alone, so the query is hashed and accepted when it
// lands on an id-only slot — the same hash fallback the raw set and a
// single-blob Materialize() use when no names payload exists.
func (m MaterializedNumericBlobSet) slotByName(metricName string) (int, bool) {
	if slot, ok := m.byName[metricName]; ok {
		return slot, true
	}

	slot, ok := m.byID[hash.ID(metricName)]
	if !ok || m.names[slot] != "" {
		return -1, false
	}

	return slot, true
}

func (m MaterializedNumericBlobSet) metricByName(metricName string) (materializedNumericMetricSet, bool) {
	slot, ok := m.slotByName(metricName)
	if !ok {
		return materializedNumericMetricSet{}, false
	}

	return m.metrics[slot], true
}

func (m MaterializedNumericBlobSet) ValueAt(metricID uint64, index int) (float64, bool) {
	metric, ok := m.metricByID(metricID)
	if !ok {
		return 0, false
	}

	if index < 0 || index >= len(metric.values) {
		return 0, false
	}

	return metric.values[index], true
}

// TimestampAt returns the timestamp at the specified global index for the given metric ID.
// Returns (0, false) if the metric ID is not found or index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericBlobSet) TimestampAt(metricID uint64, index int) (int64, bool) {
	metric, ok := m.metricByID(metricID)
	if !ok {
		return 0, false
	}

	if index < 0 || index >= len(metric.timestamps) {
		return 0, false
	}

	return metric.timestamps[index], true
}

// TagAt returns the tag at the specified global index for the given metric ID.
// Returns ("", false) if the metric ID is not found or index is out of bounds.
// Returns ("", true) if tags are not enabled but the metric and index are valid.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericBlobSet) TagAt(metricID uint64, index int) (string, bool) {
	metric, ok := m.metricByID(metricID)
	if !ok {
		return "", false
	}

	// If tags weren't enabled, return empty string
	if len(metric.tags) == 0 {
		return "", index >= 0 && index < len(metric.values)
	}

	if index < 0 || index >= len(metric.tags) {
		return "", false
	}

	return metric.tags[index], true
}

// ValueAtByName returns the value at the specified global index by metric name.
// Returns (0, false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedNumericBlobSet) ValueAtByName(metricName string, index int) (float64, bool) {
	metric, ok := m.metricByName(metricName)
	if !ok {
		return 0, false
	}

	if index < 0 || index >= len(metric.values) {
		return 0, false
	}

	return metric.values[index], true
}

// TimestampAtByName returns the timestamp at the specified global index by metric name.
// Returns (0, false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedNumericBlobSet) TimestampAtByName(metricName string, index int) (int64, bool) {
	metric, ok := m.metricByName(metricName)
	if !ok {
		return 0, false
	}

	if index < 0 || index >= len(metric.timestamps) {
		return 0, false
	}

	return metric.timestamps[index], true
}

// TagAtByName returns the tag at the specified global index by metric name.
// Returns ("", false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedNumericBlobSet) TagAtByName(metricName string, index int) (string, bool) {
	metric, ok := m.metricByName(metricName)
	if !ok {
		return "", false
	}

	if len(metric.tags) == 0 {
		return "", index >= 0 && index < len(metric.values)
	}

	if index < 0 || index >= len(metric.tags) {
		return "", false
	}

	return metric.tags[index], true
}

// DataPointCount returns the number of data points for the given metric ID across all blobs.
// A collided ID resolves to the first colliding name's logical metric.
// Returns 0 if the metric ID is not found.
func (m MaterializedNumericBlobSet) DataPointCount(metricID uint64) int {
	metric, ok := m.metricByID(metricID)
	if !ok {
		return 0
	}

	return len(metric.values)
}

// DataPointCountByName returns the number of data points for the given metric name across all blobs.
// Returns 0 if the metric name is not found.
func (m MaterializedNumericBlobSet) DataPointCountByName(metricName string) int {
	metric, ok := m.metricByName(metricName)
	if !ok {
		return 0
	}

	return len(metric.values)
}

// MetricCount returns the number of distinct logical metrics under the set's
// logical identity (a collided ID with two names counts as two).
func (m MaterializedNumericBlobSet) MetricCount() int {
	return len(m.metrics)
}

// HasMetricID checks if the materialized blob set contains the given metric ID.
func (m MaterializedNumericBlobSet) HasMetricID(metricID uint64) bool {
	_, ok := m.byID[metricID]
	return ok
}

// HasMetricName checks if the materialized blob set contains the given metric name.
// Names-free members are matched by the name's hash, like the raw set.
func (m MaterializedNumericBlobSet) HasMetricName(metricName string) bool {
	_, ok := m.slotByName(metricName)
	return ok
}

// MetricIDs returns the MetricID of each logical metric in canonical order.
// A collided ID appears once per colliding name.
func (m MaterializedNumericBlobSet) MetricIDs() []uint64 {
	if len(m.ids) == 0 {
		return nil
	}

	return slices.Clone(m.ids)
}

// MetricNames returns the distinct logical metric names in canonical order.
// Returns nil if no metric names are available.
func (m MaterializedNumericBlobSet) MetricNames() []string {
	if len(m.byName) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.byName))
	for k := range m.names {
		if m.names[k] != "" {
			names = append(names, m.names[k])
		}
	}

	return names
}

// alignMemberRows trims the rows one member just appended so the timestamp,
// value and tag columns stay the same length. Columns are aligned before each
// member, so a member that decoded fewer timestamps, values or tags than the
// others from a corrupt stream loses only its own unmatched tail instead of
// shifting every later member's points. Callers pad tagless members with ""
// before aligning, so a short tag column always means missing tags.
func alignMemberRows[V any](timestamps []int64, values []V, tags []string, hasTags bool) ([]int64, []V, []string) {
	n := min(len(timestamps), len(values))
	if hasTags {
		n = min(n, len(tags))
		tags = tags[:n]
	}

	return timestamps[:n], values[:n], tags
}
