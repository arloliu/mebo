package blob

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
	material := MaterializedTextBlobSet{
		data:  make(map[uint64]materializedTextMetricSet),
		names: make(map[string]uint64),
	}

	if len(s.blobs) == 0 {
		return material
	}

	// Step 1: Identify all unique metric IDs across all blobs
	metricIDs := make(map[uint64]bool)
	for i := range s.blobs {
		for metricID := range s.blobs[i].index.byID {
			metricIDs[metricID] = true
		}
	}

	// Step 2: Calculate total capacity needed for each metric
	capacities := make(map[uint64]int)
	for metricID := range metricIDs {
		totalCount := 0
		for i := range s.blobs {
			if entry, ok := s.blobs[i].index.GetByID(metricID); ok {
				totalCount += int(entry.Count)
			}
		}
		capacities[metricID] = totalCount
	}

	// Step 3: Check if any blob has tags enabled
	hasTags := false
	for i := range s.blobs {
		if s.blobs[i].flag.HasTag() {
			hasTags = true
			break
		}
	}

	// Step 4: Pre-allocate slices for each metric
	for metricID, capacity := range capacities {
		metricSet := materializedTextMetricSet{
			timestamps: make([]int64, 0, capacity),
			values:     make([]string, 0, capacity),
		}
		if hasTags {
			metricSet.tags = make([]string, 0, capacity)
		}
		material.data[metricID] = metricSet
	}

	// Step 5: Iterate through blobs in chronological order, appending data
	s.materializeBlobData(&material, metricIDs, hasTags)

	// Step 6: Build metric name mapping if available
	for i := range s.blobs {
		blob := &s.blobs[i]
		// Check if blob has metric names (byName map is populated)
		if blob.index.byName != nil {
			// Iterate through the byName map to build the name→ID mapping
			for name, entry := range blob.index.byName {
				material.names[name] = entry.MetricID
			}
		}
	}

	return material
}

// materializeBlobData appends data from all blobs to the materialized metric sets.
// This helper method is extracted to reduce cyclomatic complexity of Materialize.
func (s *TextBlobSet) materializeBlobData(material *MaterializedTextBlobSet, metricIDs map[uint64]bool, hasTags bool) {
	for i := range s.blobs {
		blob := &s.blobs[i]

		// For each metric in this blob
		for metricID := range metricIDs {
			entry, ok := blob.index.GetByID(metricID)
			if !ok {
				continue // This metric doesn't exist in this blob
			}

			metricSet := material.data[metricID]

			// Decode and append timestamps
			for ts := range blob.allTimestampsFromEntry(entry) {
				metricSet.timestamps = append(metricSet.timestamps, ts)
			}

			// Decode and append values
			for val := range blob.allValuesFromEntry(entry) {
				metricSet.values = append(metricSet.values, val)
			}

			// Decode and append tags if present
			if hasTags && blob.flag.HasTag() {
				for tag := range blob.allTagsFromEntry(entry) {
					metricSet.tags = append(metricSet.tags, tag)
				}
			} else if hasTags {
				// This blob doesn't have tags, but other blobs do
				// Fill with empty strings to maintain index alignment
				for range int(entry.Count) {
					metricSet.tags = append(metricSet.tags, "")
				}
			}

			material.data[metricID] = metricSet
		}
	}
}

// MaterializedTextBlobSet provides O(1) random access to text metrics across multiple blobs.
type MaterializedTextBlobSet struct {
	data  map[uint64]materializedTextMetricSet // metricID → metric data
	names map[string]uint64                    // metricName → metricID (if names available)
}

// ValueAt returns the text value at the specified index for the given metric ID.
// Index is 0-based and spans all blobs chronologically.
//
// Returns ("", false) if the metric ID doesn't exist or index is out of bounds.
func (m MaterializedTextBlobSet) ValueAt(metricID uint64, index int) (string, bool) {
	metricSet, ok := m.data[metricID]
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
	metricSet, ok := m.data[metricID]
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
// Returns ("", false) if tags are not enabled for this metric.
// Returns ("", false) if the metric ID doesn't exist or index is out of bounds.
func (m MaterializedTextBlobSet) TagAt(metricID uint64, index int) (string, bool) {
	metricSet, ok := m.data[metricID]
	if !ok {
		return "", false
	}

	if len(metricSet.tags) == 0 {
		return "", false // Tags not enabled
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
	metricID, ok := m.names[metricName]
	if !ok {
		return "", false
	}

	return m.ValueAt(metricID, index)
}

// TimestampAtByName returns the timestamp at the specified index for the given metric name.
// This is a convenience wrapper around TimestampAt() that looks up the metric ID by name.
//
// Returns (0, false) if the metric name doesn't exist or index is out of bounds.
func (m MaterializedTextBlobSet) TimestampAtByName(metricName string, index int) (int64, bool) {
	metricID, ok := m.names[metricName]
	if !ok {
		return 0, false
	}

	return m.TimestampAt(metricID, index)
}

// TagAtByName returns the tag at the specified index for the given metric name.
// This is a convenience wrapper around TagAt() that looks up the metric ID by name.
//
// Returns ("", false) if the metric name doesn't exist or index is out of bounds.
func (m MaterializedTextBlobSet) TagAtByName(metricName string, index int) (string, bool) {
	metricID, ok := m.names[metricName]
	if !ok {
		return "", false
	}

	return m.TagAt(metricID, index)
}

// MetricCount returns the total number of unique metrics across all blobs.
func (m MaterializedTextBlobSet) MetricCount() int {
	return len(m.data)
}

// DataPointCount returns the total number of data points for the given metric ID
// across all blobs.
//
// Returns 0 if the metric ID doesn't exist.
func (m MaterializedTextBlobSet) DataPointCount(metricID uint64) int {
	metricSet, ok := m.data[metricID]
	if !ok {
		return 0
	}

	return len(metricSet.values)
}

// HasMetricID returns true if the given metric ID exists in the materialized data.
func (m MaterializedTextBlobSet) HasMetricID(metricID uint64) bool {
	_, ok := m.data[metricID]
	return ok
}

// MetricIDs returns a slice of all metric IDs in the materialized data.
// The order is non-deterministic (map iteration order).
func (m MaterializedTextBlobSet) MetricIDs() []uint64 {
	if len(m.data) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(m.data))
	for id := range m.data {
		ids = append(ids, id)
	}

	return ids
}

// MetricNames returns a slice of all metric names in the materialized data.
// Returns nil if no metric names are available (blobs were created with StartMetricID).
// The order is non-deterministic (map iteration order).
func (m MaterializedTextBlobSet) MetricNames() []string {
	if len(m.names) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.names))
	for name := range m.names {
		names = append(names, name)
	}

	return names
}
