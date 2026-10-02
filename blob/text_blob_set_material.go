package blob

import (
	"slices"

	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/section"
)

// materializedTextMetricSet holds the materialized data for a single metric across all blobs.
type materializedTextMetricSet struct {
	timestamps []int64  // Flattened timestamps across all blobs
	values     []string // Flattened text values across all blobs
	tags       []string // Flattened tags across all blobs (empty if no tags)
}

// MaterializedTextBlobSet represents a TextBlobSet with all data pre-decoded and
// stored in continuous memory arrays for O(1) random access.
//
// Instead of iterating through compressed blobs to find a specific data point,
// materialization pre-decodes ALL metrics from ALL blobs into flat arrays indexed by
// metric ID and data point index. This trades memory for dramatically faster random access.
//
// Memory layout per metric:
//   - timestamps: []int64 (8 bytes × total points across all blobs)
//   - values: []string (pointer + len per value)
//   - tags: []string (pointer + len per tag, if tags enabled)
//
// Data point ordering:
// Data points are stored chronologically across all blobs. If metric M exists in
// blobs B0, B1, B2, the materialized arrays will contain:
//
//	[B0_point0, B0_point1, ..., B0_pointN, B1_point0, ..., B2_pointN]
//
// This preserves time-ordering while enabling O(1) access to any point:
//
//	ValueAt(metricID, index) → Direct array lookup, ~5ns
//
// vs Sequential iteration (non-materialized):
//   - Decode blob headers
//   - Binary search for metric
//   - Decode compressed data
//   - Iterate to target index
//   - Result: ~1000-10000ns depending on encoding
//
// Performance:
//   - Materialization cost: ~100μs per metric per blob (one-time)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~24 bytes per data point (timestamp + 2 string pointers + overhead)
//
// Use this when:
//   - You need random access to many metrics across the entire time range
//   - You will access each metric multiple times
//   - Memory is available (~24 bytes per data point)
//
// Skip this when:
//   - You only need sequential iteration (use TextBlobSet.All())
//   - Memory is constrained
//   - You're only accessing a few data points
func (s *TextBlobSet) Materialize() MaterializedTextBlobSet {
	if len(s.blobs) == 0 {
		return MaterializedTextBlobSet{
			byName: make(map[string]int),
			byID:   make(map[uint64]int),
		}
	}

	// Step 1: Build the logical-identity plan (canonical order, name/id keyed,
	// grouping entries by logical metric across blobs). The materialized path
	// decodes everything, so it builds the plan unconditionally, unlike the lazy
	// on-collision-only build used elsewhere. slotOf routes a member entry to its
	// logical metric.
	plan := buildLogicalPlan(func(i int) *indexMaps[section.TextIndexEntry] { return &s.blobs[i].index }, len(s.blobs))
	slotOf := func(blob *TextBlob, ord int) int {
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
			capacities[slotOf(blob, ord)] += int(blob.index.sorted[ord].Count)
		}
	}

	// Step 3: Pre-allocate per-slot slices with exact capacity. Deep-clone names
	// off any borrowed member backing so the materialized set is fully owning and
	// never aliases a borrowed decode buffer.
	ownNames, ownByName := ownSetNames(plan.names, plan.byName,
		func(i int) *indexMaps[section.TextIndexEntry] { return &s.blobs[i].index }, len(s.blobs))
	material := MaterializedTextBlobSet{
		metrics: make([]materializedTextMetricSet, len(plan.ids)),
		ids:     plan.ids,
		names:   ownNames,
		byName:  ownByName,
		byID:    plan.byID,
	}
	for slot := range material.metrics {
		material.metrics[slot] = materializedTextMetricSet{
			timestamps: make([]int64, 0, capacities[slot]),
			values:     make([]string, 0, capacities[slot]),
		}
		if hasTags {
			material.metrics[slot].tags = make([]string, 0, capacities[slot])
		}
	}

	// Step 4: Iterate through blobs in chronological order, appending data per slot.
	s.materializeBlobData(&material, slotOf, hasTags)

	return material
}

// MaterializeMetric decodes a single metric by ID from all blobs in the set and returns
// a MaterializedTextMetric for O(1) random access without needing to pass metric ID on each call.
//
// Unlike Materialize() which materializes all metrics, this method materializes only
// one metric, reducing memory usage when you only need specific metrics.
//
// Parameters:
//   - metricID: The metric ID to materialize
//
// Returns:
//   - MaterializedTextMetric: The materialized metric with direct access methods
//   - bool: false if the metric is not found in any blob
//
// Performance:
//   - Materialization cost: ~100μs (one-time, for one metric across all blobs)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~24 bytes per data point × total data points for this metric
//
// Example:
//
//	blobSet, _ := NewTextBlobSet(blobs)
//	metric, ok := blobSet.MaterializeMetric(metricID)
//	if ok {
//	    val, _ := metric.ValueAt(150)      // O(1) access, no metric ID needed
//	    ts, _ := metric.TimestampAt(250)   // O(1) access
//	}
func (s *TextBlobSet) MaterializeMetric(metricID uint64) (MaterializedTextMetric, bool) {
	// A collided ID resolves to the first colliding name's logical metric: gate
	// members so a cross-member A/H + B/H pair never concatenates into A+B.
	targetName, collided := s.identity.resolveID(metricID)
	resolve := func(blob *TextBlob) (section.TextIndexEntry, bool) {
		return blob.index.resolveEntry(metricID, targetName, collided)
	}

	return s.materializeMetricCore(metricID, resolve)
}

// materializeMetricCore decodes and concatenates a single logical metric across all
// members, selecting each member's contributing entry via resolve.
func (s *TextBlobSet) materializeMetricCore(metricID uint64, resolve func(blob *TextBlob) (section.TextIndexEntry, bool)) (MaterializedTextMetric, bool) {
	// Step 1: Check if metric exists in any blob and calculate total capacity
	capacity := 0
	for i := range s.blobs {
		blob := &s.blobs[i]
		if entry, ok := resolve(blob); ok {
			capacity += int(entry.Count)
		}
	}

	// If metric not found in any blob, return false
	if capacity == 0 {
		return MaterializedTextMetric{}, false
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
	values := make([]string, 0, capacity)
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

		// Decode and append timestamps
		for ts := range blob.allTimestampsFromEntry(entry) {
			timestamps = append(timestamps, ts)
		}

		// Decode and append values
		valsBefore := len(values)
		for val := range blob.allValuesFromEntry(entry) {
			values = append(values, val)
		}
		valsProduced := len(values) - valsBefore

		// Decode and append tags if present
		if hasTags && blob.HasTag() {
			for tag := range blob.allTagsFromEntry(entry) {
				tags = append(tags, tag)
			}
		} else if hasTags {
			// This blob doesn't have tags, but other blobs do
			// Fill with empty strings to maintain index alignment
			for range valsProduced {
				tags = append(tags, "")
			}
		}
	}

	return MaterializedTextMetric{
		MetricID:   metricID,
		Timestamps: timestamps,
		Values:     values,
		Tags:       tags,
	}, true
}

// MaterializeMetricByName decodes a single metric by name from all blobs in the set and returns
// a MaterializedTextMetric for O(1) random access without needing to pass metric name on each call.
//
// Unlike Materialize() which materializes all metrics, this method materializes only
// one metric, reducing memory usage when you only need specific metrics.
//
// Parameters:
//   - metricName: The metric name to materialize
//
// Returns:
//   - MaterializedTextMetric: The materialized metric with direct access methods
//   - bool: false if the metric is not found in any blob
//
// Performance:
//   - Materialization cost: ~100μs (one-time, for one metric across all blobs)
//   - Random access: ~5ns (O(1), direct array indexing)
//   - Memory: ~24 bytes per data point × total data points for this metric
//
// Example:
//
//	blobSet, _ := NewTextBlobSet(blobs)
//	metric, ok := blobSet.MaterializeMetricByName("log.message")
//	if ok {
//	    val, _ := metric.ValueAt(150)      // O(1) access, no metric name needed
//	    ts, _ := metric.TimestampAt(250)   // O(1) access
//	}
func (s *TextBlobSet) MaterializeMetricByName(metricName string) (MaterializedTextMetric, bool) {
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
		return MaterializedTextMetric{}, false
	}

	// Step 2: Gather THIS name's logical set metric — never re-resolving by ID,
	// which would collapse a collided name onto the first colliding name's series. Named
	// members match the name exactly; a stripped member attaches only when the name is
	// the first colliding name for this id.
	skipStripped := s.identity.excludesStripped(metricName)
	resolve := func(blob *TextBlob) (section.TextIndexEntry, bool) {
		return blob.index.resolveEntryByName(metricName, skipStripped)
	}

	return s.materializeMetricCore(metricID, resolve)
}

// materializeBlobData appends data from all blobs to the per-slot materialized metric
// sets, routing each member entry to its logical metric via slotOf.
func (s *TextBlobSet) materializeBlobData(material *MaterializedTextBlobSet, slotOf func(blob *TextBlob, ord int) int, hasTags bool) {
	for i := range s.blobs {
		blob := &s.blobs[i]

		for ord := range blob.index.sorted {
			entry := blob.index.sorted[ord]
			slot := slotOf(blob, ord)
			metricSet := material.metrics[slot]

			// Decode and append timestamps
			for ts := range blob.allTimestampsFromEntry(entry) {
				metricSet.timestamps = append(metricSet.timestamps, ts)
			}

			// Decode and append values
			valsBefore := len(metricSet.values)
			for val := range blob.allValuesFromEntry(entry) {
				metricSet.values = append(metricSet.values, val)
			}
			valsProduced := len(metricSet.values) - valsBefore

			// Decode and append tags if present
			if hasTags && blob.HasTag() {
				for tag := range blob.allTagsFromEntry(entry) {
					metricSet.tags = append(metricSet.tags, tag)
				}
			} else if hasTags {
				// This blob doesn't have tags, but other blobs do
				// Fill with empty strings to maintain index alignment
				for range valsProduced {
					metricSet.tags = append(metricSet.tags, "")
				}
			}

			material.metrics[slot] = metricSet
		}
	}
}

// MaterializedTextBlobSet provides O(1) random access to text metrics across multiple
// blobs. It keys its logical metrics by set identity: by name when the set
// is names-bearing, by MetricID otherwise. metrics holds one entry per logical metric
// in canonical order; ids/names are parallel (names[k] is "" for an id-only metric).
// byName resolves a name exactly; byID resolves an ID to its FIRST logical metric (a
// collided ID → the first colliding name's series).
type MaterializedTextBlobSet struct {
	metrics []materializedTextMetricSet
	ids     []uint64
	names   []string
	byName  map[string]int
	byID    map[uint64]int
}

// ValueAt returns the text value at the specified index for the given metric ID.
// Index is 0-based and spans all blobs chronologically.
//
// Returns ("", false) if the metric ID doesn't exist or index is out of bounds.
func (m MaterializedTextBlobSet) metricByID(metricID uint64) (materializedTextMetricSet, bool) {
	slot, ok := m.byID[metricID]
	if !ok {
		return materializedTextMetricSet{}, false
	}

	return m.metrics[slot], true
}

// slotByName resolves a metric name to its logical slot. A named slot matches
// exactly. Otherwise the name can only refer to data from names-free members,
// whose slots are keyed by ID alone, so the query is hashed and accepted when it
// lands on an id-only slot — the same hash fallback the raw set and a
// single-blob Materialize() use when no names payload exists.
func (m MaterializedTextBlobSet) slotByName(metricName string) (int, bool) {
	if slot, ok := m.byName[metricName]; ok {
		return slot, true
	}

	slot, ok := m.byID[hash.ID(metricName)]
	if !ok || m.names[slot] != "" {
		return -1, false
	}

	return slot, true
}

func (m MaterializedTextBlobSet) metricByName(metricName string) (materializedTextMetricSet, bool) {
	slot, ok := m.slotByName(metricName)
	if !ok {
		return materializedTextMetricSet{}, false
	}

	return m.metrics[slot], true
}

func (m MaterializedTextBlobSet) ValueAt(metricID uint64, index int) (string, bool) {
	metricSet, ok := m.metricByID(metricID)
	if !ok {
		return "", false
	}

	if index < 0 || index >= len(metricSet.values) {
		return "", false
	}

	return metricSet.values[index], true
}

// TimestampAt returns the timestamp at the specified index for the given metric ID.
// Index is 0-based and spans all blobs chronologically.
//
// Returns (0, false) if the metric ID doesn't exist or index is out of bounds.
func (m MaterializedTextBlobSet) TimestampAt(metricID uint64, index int) (int64, bool) {
	metricSet, ok := m.metricByID(metricID)
	if !ok {
		return 0, false
	}

	if index < 0 || index >= len(metricSet.timestamps) {
		return 0, false
	}

	return metricSet.timestamps[index], true
}

// TagAt returns the tag at the specified index for the given metric ID.
// Index is 0-based and spans all blobs chronologically.
//
// Returns ("", true) if tags are not enabled but the metric and index are valid.
// Returns ("", false) if the metric ID doesn't exist or index is out of bounds.
func (m MaterializedTextBlobSet) TagAt(metricID uint64, index int) (string, bool) {
	metricSet, ok := m.metricByID(metricID)
	if !ok {
		return "", false
	}

	if len(metricSet.tags) == 0 {
		return "", index >= 0 && index < len(metricSet.timestamps)
	}

	if index < 0 || index >= len(metricSet.tags) {
		return "", false
	}

	return metricSet.tags[index], true
}

// ValueAtByName returns the text value at the specified index for the given metric name.
// This is a convenience wrapper around ValueAt() that looks up the metric ID by name.
//
// Returns ("", false) if the metric name doesn't exist or index is out of bounds.
func (m MaterializedTextBlobSet) ValueAtByName(metricName string, index int) (string, bool) {
	metricSet, ok := m.metricByName(metricName)
	if !ok {
		return "", false
	}

	if index < 0 || index >= len(metricSet.values) {
		return "", false
	}

	return metricSet.values[index], true
}

// TimestampAtByName returns the timestamp at the specified index for the given metric name.
// This is a convenience wrapper around TimestampAt() that looks up the metric ID by name.
//
// Returns (0, false) if the metric name doesn't exist or index is out of bounds.
func (m MaterializedTextBlobSet) TimestampAtByName(metricName string, index int) (int64, bool) {
	metricSet, ok := m.metricByName(metricName)
	if !ok {
		return 0, false
	}

	if index < 0 || index >= len(metricSet.timestamps) {
		return 0, false
	}

	return metricSet.timestamps[index], true
}

// TagAtByName returns the tag at the specified index for the given metric name.
// This is a convenience wrapper around TagAt() that looks up the metric ID by name.
//
// Returns ("", false) if the metric name doesn't exist or index is out of bounds.
func (m MaterializedTextBlobSet) TagAtByName(metricName string, index int) (string, bool) {
	metricSet, ok := m.metricByName(metricName)
	if !ok {
		return "", false
	}

	if len(metricSet.tags) == 0 {
		return "", index >= 0 && index < len(metricSet.timestamps)
	}

	if index < 0 || index >= len(metricSet.tags) {
		return "", false
	}

	return metricSet.tags[index], true
}

// MetricCount returns the number of distinct logical metrics under the set
// identity (a collided ID with two names counts as two).
func (m MaterializedTextBlobSet) MetricCount() int {
	return len(m.metrics)
}

// DataPointCount returns the total number of data points for the given metric ID
// across all blobs. A collided ID resolves to the first colliding name's logical
// metric.
//
// Returns 0 if the metric ID doesn't exist.
func (m MaterializedTextBlobSet) DataPointCount(metricID uint64) int {
	metricSet, ok := m.metricByID(metricID)
	if !ok {
		return 0
	}

	return len(metricSet.values)
}

// DataPointCountByName returns the total number of data points for the given metric
// name across all blobs. Returns 0 if the metric name doesn't exist.
func (m MaterializedTextBlobSet) DataPointCountByName(metricName string) int {
	metricSet, ok := m.metricByName(metricName)
	if !ok {
		return 0
	}

	return len(metricSet.values)
}

// HasMetricID returns true if the given metric ID exists in the materialized data.
func (m MaterializedTextBlobSet) HasMetricID(metricID uint64) bool {
	_, ok := m.byID[metricID]
	return ok
}

// HasMetricName returns true if the given metric name exists in the materialized data.
// Names-free members are matched by the name's hash, like the raw set.
func (m MaterializedTextBlobSet) HasMetricName(metricName string) bool {
	_, ok := m.slotByName(metricName)
	return ok
}

// MetricIDs returns the MetricID of each logical metric in canonical order.
// A collided ID appears once per colliding name.
func (m MaterializedTextBlobSet) MetricIDs() []uint64 {
	if len(m.ids) == 0 {
		return nil
	}

	return slices.Clone(m.ids)
}

// MetricNames returns the distinct logical metric names in canonical order.
// Returns nil if no metric names are available (blobs were created with StartMetricID).
func (m MaterializedTextBlobSet) MetricNames() []string {
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
