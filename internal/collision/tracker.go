package collision

import (
	"github.com/arloliu/mebo/errs"
)

// Tracker tracks metric names and detects hash collisions during encoding.
// It maintains a map of hash-to-name mappings and an ordered list of names
// for payload encoding when collisions are detected.
type Tracker struct {
	metricNames     map[uint64]string // Hash → name mapping for collision detection
	metricNamesList []string          // Ordered list for payload encoding
	hasCollision    bool              // Whether a collision has been detected
}

// NewTracker creates a new collision tracker.
func NewTracker() *Tracker {
	return &Tracker{
		metricNames:     make(map[uint64]string),
		metricNamesList: make([]string, 0),
		hasCollision:    false,
	}
}

// TrackMetricID tracks a metric ID and checks for collisions.
// This is used when user provides hash directly (StartMetricID).
// Returns error if the hash was already used - this indicates a collision
// that CANNOT be handled automatically since we don't have metric names.
func (t *Tracker) TrackMetricID(hash uint64) error {
	// Check if this hash was already used
	if _, exists := t.metricNames[hash]; exists {
		// Collision detected - cannot handle without metric name
		return errs.ErrHashCollision
	}

	// Track the hash with empty name (we don't have the name)
	t.metricNames[hash] = ""

	return nil
}

// TrackMetric tracks a metric name with its hash.
// This is used when user provides name (StartMetricName).
// Returns error if:
// - The metric name is empty (ErrInvalidMetricName)
// - The same metric name is added twice (ErrMetricAlreadyStarted)
//
// Note: Hash collisions (different names, same hash) are NOT errors here.
// Instead, the collision flag is set and metric names will be stored in the blob.
func (t *Tracker) TrackMetric(name string, hash uint64) error {
	if name == "" {
		return errs.ErrInvalidMetricName
	}

	// Check for collision: different name, same hash
	if existingName, exists := t.metricNames[hash]; exists {
		if existingName != name {
			// Hash collision detected - set flag but don't return error
			// We can handle this by storing metric names in the blob
			t.hasCollision = true
		}
		if existingName == name {
			// Same name, same hash - duplicate metric
			return errs.ErrMetricAlreadyStarted
		}
	}

	// Track the metric
	t.metricNames[hash] = name
	t.metricNamesList = append(t.metricNamesList, name)

	return nil
}

// HasCollision returns true if a collision has been detected.
func (t *Tracker) HasCollision() bool {
	return t.hasCollision
}

// GetMetricNames returns the ordered list of metric names.
// The order matches the order in which TrackMetric was called.
func (t *Tracker) GetMetricNames() []string {
	return t.metricNamesList
}

// Count returns the number of tracked metrics.
func (t *Tracker) Count() int {
	return len(t.metricNamesList)
}

// Reset clears all tracked metrics and collision state.
// This allows reusing the tracker for encoding a new blob.
func (t *Tracker) Reset() {
	// Clear maps but preserve capacity to avoid allocations
	for k := range t.metricNames {
		delete(t.metricNames, k)
	}
	t.metricNamesList = t.metricNamesList[:0]
	t.hasCollision = false
}
