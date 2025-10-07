package blob

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
//	val, ok := material.ValueAt(metricID, 500)  // O(1), ~5ns
//	ts, ok := material.TimestampAt(metricID, 500)
type MaterializedNumericBlob struct {
	data  map[uint64]materializedNumericMetric
	names map[string]uint64 // metricName → metricID (if available)
}

type materializedNumericMetric struct {
	timestamps []int64
	values     []float64
	tags       []string
}

// Materialize decodes all metrics in the blob and returns a MaterializedNumericBlob
// that supports O(1) random access to all data points.
//
// Performance:
//   - Materialization cost: ~100μs per metric (one-time)
//   - Random access: ~5ns (O(1), array indexing)
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
	material := MaterializedNumericBlob{
		data:  make(map[uint64]materializedNumericMetric, b.MetricCount()),
		names: make(map[string]uint64),
	}

	// Decode all metrics
	for metricID, entry := range b.index.byID {
		// Pre-allocate slices with known capacity
		count := entry.Count
		timestamps := make([]int64, 0, count)
		values := make([]float64, 0, count)
		var tags []string
		if b.flag.HasTag() {
			tags = make([]string, 0, count)
		}

		for ts := range b.allTimestampsFromEntry(entry) {
			timestamps = append(timestamps, ts)
		}

		for val := range b.allValuesFromEntry(entry) {
			values = append(values, val)
		}

		if b.flag.HasTag() {
			for tag := range b.allTagsFromEntry(entry) {
				tags = append(tags, tag)
			}
		}

		material.data[metricID] = materializedNumericMetric{
			timestamps: timestamps,
			values:     values,
			tags:       tags,
		}
	}

	// Copy metric name mappings (if available)
	if b.index.byName != nil {
		for name, entry := range b.index.byName {
			material.names[name] = entry.MetricID
		}
	}

	return material
}

// ValueAt returns the value at the specified index for the given metric ID.
// Returns (0, false) if the metric ID is not found or index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericBlob) ValueAt(metricID uint64, index int) (float64, bool) {
	metric, ok := m.data[metricID]
	if !ok {
		return 0, false
	}

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
	metric, ok := m.data[metricID]
	if !ok {
		return 0, false
	}

	if index < 0 || index >= len(metric.timestamps) {
		return 0, false
	}

	return metric.timestamps[index], true
}

// TagAt returns the tag at the specified index for the given metric ID.
// Returns ("", false) if the metric ID is not found or index is out of bounds.
// Returns empty string if tags are not enabled for this blob.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericBlob) TagAt(metricID uint64, index int) (string, bool) {
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

// ValueAtByName returns the value at the specified index by metric name.
// Returns (0, false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedNumericBlob) ValueAtByName(metricName string, index int) (float64, bool) {
	metricID, ok := m.names[metricName]
	if !ok {
		return 0, false
	}

	return m.ValueAt(metricID, index)
}

// TimestampAtByName returns the timestamp at the specified index by metric name.
// Returns (0, false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedNumericBlob) TimestampAtByName(metricName string, index int) (int64, bool) {
	metricID, ok := m.names[metricName]
	if !ok {
		return 0, false
	}

	return m.TimestampAt(metricID, index)
}

// TagAtByName returns the tag at the specified index by metric name.
// Returns ("", false) if the metric name is not found or index is out of bounds.
//
// This is an O(1) operation after the name→ID lookup.
func (m MaterializedNumericBlob) TagAtByName(metricName string, index int) (string, bool) {
	metricID, ok := m.names[metricName]
	if !ok {
		return "", false
	}

	return m.TagAt(metricID, index)
}

// DataPointCount returns the number of data points for the given metric ID.
// Returns 0 if the metric ID is not found.
func (m MaterializedNumericBlob) DataPointCount(metricID uint64) int {
	metric, ok := m.data[metricID]
	if !ok {
		return 0
	}

	return len(metric.values)
}

// DataPointCountByName returns the number of data points for the given metric name.
// Returns 0 if the metric name is not found.
func (m MaterializedNumericBlob) DataPointCountByName(metricName string) int {
	metricID, ok := m.names[metricName]
	if !ok {
		return 0
	}

	return m.DataPointCount(metricID)
}

// MetricCount returns the number of metrics in the materialized blob.
func (m MaterializedNumericBlob) MetricCount() int {
	return len(m.data)
}

// HasMetricID checks if the materialized blob contains the given metric ID.
func (m MaterializedNumericBlob) HasMetricID(metricID uint64) bool {
	_, ok := m.data[metricID]
	return ok
}

// HasMetricName checks if the materialized blob contains the given metric name.
// Returns false if metric names are not available in the blob.
func (m MaterializedNumericBlob) HasMetricName(metricName string) bool {
	_, ok := m.names[metricName]
	return ok
}

// MetricIDs returns a slice of all metric IDs in the materialized blob.
// The order is not guaranteed.
func (m MaterializedNumericBlob) MetricIDs() []uint64 {
	ids := make([]uint64, 0, len(m.data))
	for id := range m.data {
		ids = append(ids, id)
	}

	return ids
}

// MetricNames returns a slice of all metric names in the materialized blob.
// Returns empty slice if no metric names are available.
// The order is not guaranteed.
func (m MaterializedNumericBlob) MetricNames() []string {
	if len(m.names) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.names))
	for name := range m.names {
		names = append(names, name)
	}

	return names
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
//   - Materialization cost: ~100μs (one-time)
//   - Random access: ~5ns (O(1), array indexing)
//   - Memory: ~16 bytes per data point
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
func (b NumericBlob) MaterializeMetric(metricID uint64) (MaterializedNumericMetric, bool) {
	entry, ok := b.index.GetByID(metricID)
	if !ok {
		return MaterializedNumericMetric{}, false
	}

	// Pre-allocate slices with known capacity
	count := entry.Count
	timestamps := make([]int64, 0, count)
	values := make([]float64, 0, count)
	var tags []string
	if b.flag.HasTag() {
		tags = make([]string, 0, count)
	}

	// Decode timestamps
	for ts := range b.allTimestampsFromEntry(entry) {
		timestamps = append(timestamps, ts)
	}

	// Decode values
	for val := range b.allValuesFromEntry(entry) {
		values = append(values, val)
	}

	// Decode tags (if enabled)
	if b.flag.HasTag() {
		for tag := range b.allTagsFromEntry(entry) {
			tags = append(tags, tag)
		}
	}

	return MaterializedNumericMetric{
		MetricID:   metricID,
		Timestamps: timestamps,
		Values:     values,
		Tags:       tags,
	}, true
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

	return b.MaterializeMetric(entry.MetricID)
}

// ValueAt returns the value at the specified index.
// Returns (0, false) if index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericMetric) ValueAt(index int) (float64, bool) {
	if index < 0 || index >= len(m.Values) {
		return 0, false
	}

	return m.Values[index], true
}

// TimestampAt returns the timestamp at the specified index.
// Returns (0, false) if index is out of bounds.
//
// This is an O(1) operation (~5ns).
func (m MaterializedNumericMetric) TimestampAt(index int) (int64, bool) {
	if index < 0 || index >= len(m.Timestamps) {
		return 0, false
	}

	return m.Timestamps[index], true
}

// TagAt returns the tag at the specified index.
// Returns ("", false) if index is out of bounds.
// Returns empty string if tags are not enabled.
//
// This is an O(1) operation (~5ns).
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
