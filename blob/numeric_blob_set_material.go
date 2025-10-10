package blob

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
type MaterializedNumericBlobSet struct {
	data  map[uint64]materializedNumericMetricSet
	names map[string]uint64 // metricName → metricID (if available)
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
	// Step 1: Identify all unique metric IDs across all blobs
	metricIDs := make(map[uint64]bool)
	for i := range s.blobs {
		blob := &s.blobs[i]
		for metricID := range blob.index.byID {
			metricIDs[metricID] = true
		}
	}

	// Step 2: Calculate total capacity for each metric
	capacities := make(map[uint64]int)
	for metricID := range metricIDs {
		total := 0
		for i := range s.blobs {
			blob := &s.blobs[i]
			if entry, ok := blob.index.GetByID(metricID); ok {
				total += entry.Count
			}
		}
		capacities[metricID] = total
	}

	// Step 3: Check if any blob has tags enabled
	hasTags := false
	for i := range s.blobs {
		if s.blobs[i].HasTag() {
			hasTags = true
			break
		}
	}

	// Step 4: Pre-allocate slices for each metric with exact capacity
	material := MaterializedNumericBlobSet{
		data:  make(map[uint64]materializedNumericMetricSet, len(metricIDs)),
		names: make(map[string]uint64),
	}

	for metricID, capacity := range capacities {
		metricSet := materializedNumericMetricSet{
			timestamps: make([]int64, 0, capacity),
			values:     make([]float64, 0, capacity),
		}
		if hasTags {
			metricSet.tags = make([]string, 0, capacity)
		}
		material.data[metricID] = metricSet
	}

	// Step 5: Iterate through blobs in chronological order, appending data
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

			// Decode and append tags (if enabled)
			if hasTags && blob.HasTag() {
				for tag := range blob.allTagsFromEntry(entry) {
					metricSet.tags = append(metricSet.tags, tag)
				}
			}

			material.data[metricID] = metricSet
		}
	}

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
	// Step 1: Check if metric exists in any blob and calculate total capacity
	capacity := 0
	for i := range s.blobs {
		blob := &s.blobs[i]
		if entry, ok := blob.index.GetByID(metricID); ok {
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
		entry, ok := blob.index.GetByID(metricID)
		if !ok {
			continue // This metric doesn't exist in this blob
		}

		// Decode and append timestamps
		for ts := range blob.allTimestampsFromEntry(entry) {
			timestamps = append(timestamps, ts)
		}

		// Decode and append values
		for val := range blob.allValuesFromEntry(entry) {
			values = append(values, val)
		}

		// Decode and append tags (if enabled)
		if hasTags && blob.HasTag() {
			for tag := range blob.allTagsFromEntry(entry) {
				tags = append(tags, tag)
			}
		}
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
	// Step 1: Find the metric ID from the first blob that has this name
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

	// Step 2: Delegate to MaterializeMetric
	return s.MaterializeMetric(metricID)
}

// ValueAt returns the value at the specified global index for the given metric ID.
// Returns (0, false) if the metric ID is not found or index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericBlobSet) ValueAt(metricID uint64, index int) (float64, bool) {
	metric, ok := m.data[metricID]
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
	metric, ok := m.data[metricID]
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
// Returns empty string if tags are not enabled for this blob set.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericBlobSet) TagAt(metricID uint64, index int) (string, bool) {
	metric, ok := m.data[metricID]
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
	metricID, ok := m.names[metricName]
	if !ok {
		return 0, false
	}

	return m.ValueAt(metricID, index)
}

// TimestampAtByName returns the timestamp at the specified global index by metric name.
// Returns (0, false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedNumericBlobSet) TimestampAtByName(metricName string, index int) (int64, bool) {
	metricID, ok := m.names[metricName]
	if !ok {
		return 0, false
	}

	return m.TimestampAt(metricID, index)
}

// TagAtByName returns the tag at the specified global index by metric name.
// Returns ("", false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedNumericBlobSet) TagAtByName(metricName string, index int) (string, bool) {
	metricID, ok := m.names[metricName]
	if !ok {
		return "", false
	}

	return m.TagAt(metricID, index)
}

// DataPointCount returns the number of data points for the given metric ID across all blobs.
// Returns 0 if the metric ID is not found.
func (m MaterializedNumericBlobSet) DataPointCount(metricID uint64) int {
	metric, ok := m.data[metricID]
	if !ok {
		return 0
	}

	return len(metric.values)
}

// DataPointCountByName returns the number of data points for the given metric name across all blobs.
// Returns 0 if the metric name is not found.
func (m MaterializedNumericBlobSet) DataPointCountByName(metricName string) int {
	metricID, ok := m.names[metricName]
	if !ok {
		return 0
	}

	return m.DataPointCount(metricID)
}

// MetricCount returns the number of unique metrics in the materialized blob set.
func (m MaterializedNumericBlobSet) MetricCount() int {
	return len(m.data)
}

// HasMetricID checks if the materialized blob set contains the given metric ID.
func (m MaterializedNumericBlobSet) HasMetricID(metricID uint64) bool {
	_, ok := m.data[metricID]
	return ok
}

// HasMetricName checks if the materialized blob set contains the given metric name.
// Returns false if metric names are not available in any blob.
func (m MaterializedNumericBlobSet) HasMetricName(metricName string) bool {
	_, ok := m.names[metricName]
	return ok
}

// MetricIDs returns a slice of all metric IDs in the materialized blob set.
// The order is not guaranteed.
func (m MaterializedNumericBlobSet) MetricIDs() []uint64 {
	ids := make([]uint64, 0, len(m.data))
	for id := range m.data {
		ids = append(ids, id)
	}

	return ids
}

// MetricNames returns a slice of all metric names in the materialized blob set.
// Returns empty slice if no metric names are available.
// The order is not guaranteed.
func (m MaterializedNumericBlobSet) MetricNames() []string {
	if len(m.names) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.names))
	for name := range m.names {
		names = append(names, name)
	}

	return names
}
