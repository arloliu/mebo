package collision

import (
	"github.com/arloliu/mebo/errs"
)

// Tracker tracks metric names and detects hash collisions during encoding.
// It maintains a map of hash-to-name mappings and an ordered list of names
// for payload encoding when collisions are detected.
type Tracker struct {
	metricNames     map[uint64]string   // Hash → name mapping for collision detection
	seenNames       map[string]struct{} // Set of every name seen, so an already-seen name is rejected as a duplicate
	metricNamesList []string            // Ordered list for payload encoding
	hasCollision    bool                // Whether a collision has been detected
}

// NewTracker creates a new collision tracker.
func NewTracker() *Tracker {
	return &Tracker{
		metricNames:     make(map[uint64]string),
		seenNames:       make(map[string]struct{}),
		metricNamesList: make([]string, 0),
		hasCollision:    false,
	}
}

// Probe performs a read-only check of adding (name, hash) WITHOUT mutating the
// tracker. It is the non-mutating half of a probe-then-commit split: probing
// first lets a failed add be reported without leaving the tracker half-mutated.
//
// Returns:
//   - dupName: true if this exact name was already tracked (would be a duplicate).
//   - prospectiveCollision: true if a DIFFERENT name already maps to this hash,
//     so committing this name would newly require a metric-names payload.
//   - err: ErrInvalidMetricName if the name is empty.
//
// The tracker is left completely unchanged; Commit must be called to record the
// metric once the caller has decided to proceed.
func (t *Tracker) Probe(name string, hash uint64) (dupName bool, prospectiveCollision bool, err error) {
	if name == "" {
		return false, false, errs.ErrInvalidMetricName
	}

	if _, seen := t.seenNames[name]; seen {
		return true, false, nil
	}

	if existingName, exists := t.metricNames[hash]; exists && existingName != name {
		// A different name already owns this hash — a real collision would occur.
		prospectiveCollision = true
	}

	return false, prospectiveCollision, nil
}

// Commit records (name, hash) into the tracker, updating the collision flag,
// the hash→name map, the seen-name set, and the ordered payload list. It is the
// mutating half of the probe-then-commit split and must only be called after
// Probe reports the name is not a duplicate.
func (t *Tracker) Commit(name string, hash uint64) {
	if existingName, exists := t.metricNames[hash]; exists && existingName != name {
		t.hasCollision = true
	}

	t.metricNames[hash] = name
	t.seenNames[name] = struct{}{}
	t.metricNamesList = append(t.metricNamesList, name)
}

// HasCollision returns true if a collision has been detected.
func (t *Tracker) HasCollision() bool {
	return t.hasCollision
}

// GetMetricNames returns the ordered list of metric names.
// The order matches the order in which Commit was called.
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
	for k := range t.seenNames {
		delete(t.seenNames, k)
	}
	t.metricNamesList = t.metricNamesList[:0]
	t.hasCollision = false
}
