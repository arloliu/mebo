package section

import (
	"fmt"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
)

// SharedTimestampGroup represents a group of metrics that share the same timestamp sequence.
// The canonical metric owns the timestamp data; shared metrics reference it.
type SharedTimestampGroup struct {
	// CanonicalIndex is the 0-based index of the metric that owns the timestamp data.
	CanonicalIndex int

	// SharedIndices contains the 0-based indices of metrics that share the canonical's timestamps.
	SharedIndices []int
}

// SharedTimestampTable holds all shared timestamp groups in a V2 blob.
// It is serialized between the index entries and the timestamp payload.
type SharedTimestampTable struct {
	// Groups contains each shared timestamp group.
	Groups []SharedTimestampGroup
}

// Size returns the serialized byte size of the shared timestamp table.
//
// Format: GroupCount(2B) + for each group: CanonicalIndex(2B) + MemberCount(2B) + Members(2B × N)
//
// Returns:
//   - int: Total byte size of the serialized table
func (t *SharedTimestampTable) Size() int {
	size := 2 // GroupCount

	for i := range t.Groups {
		size += 4 + 2*len(t.Groups[i].SharedIndices) // CanonicalIndex(2) + MemberCount(2) + Members(2×N)
	}

	return size
}

// WriteToSlice serializes the shared timestamp table into the given byte slice.
//
// Parameters:
//   - data: Pre-allocated byte slice (must have space for Size() bytes at offset)
//   - offset: Starting position in data slice
//   - engine: Endian engine for byte order
//
// Returns:
//   - int: Next write position after the table
func (t *SharedTimestampTable) WriteToSlice(data []byte, offset int, engine endian.EndianEngine) int {
	engine.PutUint16(data[offset:offset+2], uint16(len(t.Groups))) //nolint: gosec
	offset += 2

	for i := range t.Groups {
		g := &t.Groups[i]
		engine.PutUint16(data[offset:offset+2], uint16(g.CanonicalIndex)) //nolint: gosec
		offset += 2

		engine.PutUint16(data[offset:offset+2], uint16(len(g.SharedIndices))) //nolint: gosec
		offset += 2

		for _, idx := range g.SharedIndices {
			engine.PutUint16(data[offset:offset+2], uint16(idx)) //nolint: gosec
			offset += 2
		}
	}

	return offset
}

// ParseSharedTimestampTable parses a SharedTimestampTable from a byte slice.
//
// Parameters:
//   - data: Byte slice containing the serialized table
//   - engine: Endian engine for byte order
//   - metricCount: Total number of metrics in the blob (for validation)
//
// Returns:
//   - SharedTimestampTable: Parsed table
//   - error: Parse or validation errors
func ParseSharedTimestampTable(data []byte, engine endian.EndianEngine, metricCount int) (SharedTimestampTable, error) {
	if len(data) < 2 {
		return SharedTimestampTable{}, fmt.Errorf("%w: shared timestamp table too short", errs.ErrInvalidHeaderFlags)
	}

	groupCount := int(engine.Uint16(data[0:2]))
	if groupCount == 0 {
		return SharedTimestampTable{}, fmt.Errorf("%w: shared timestamp table cannot be empty", errs.ErrInvalidSharedTimestampTable)
	}

	// Pre-scan to count total shared members for single flat allocation.
	totalMembers, err := prescanSharedTimestampTable(data, groupCount, engine)
	if err != nil {
		return SharedTimestampTable{}, err
	}

	offset := 2

	groups := make([]SharedTimestampGroup, groupCount)
	allSharedIndices := make([]int, totalMembers)

	// Use flat byte slice instead of maps for O(1) validation with minimal allocations.
	// Values: 0=unused, 1=canonical, 2=shared
	status := make([]byte, metricCount)
	memberPos := 0

	for i := range groupCount {
		canonicalIdx := int(engine.Uint16(data[offset : offset+2]))
		offset += 2

		memberCount := int(engine.Uint16(data[offset : offset+2]))
		offset += 2

		if canonicalIdx >= metricCount {
			return SharedTimestampTable{}, fmt.Errorf("%w: canonical index %d exceeds metric count %d", errs.ErrInvalidHeaderFlags, canonicalIdx, metricCount)
		}
		if status[canonicalIdx] == 1 {
			return SharedTimestampTable{}, fmt.Errorf("%w: canonical index %d appears in multiple groups", errs.ErrInvalidSharedTimestampTable, canonicalIdx)
		}
		if status[canonicalIdx] == 2 {
			return SharedTimestampTable{}, fmt.Errorf("%w: canonical index %d is already used as a shared index", errs.ErrInvalidSharedTimestampTable, canonicalIdx)
		}

		sharedIndices := allSharedIndices[memberPos : memberPos+memberCount]
		memberPos += memberCount

		for j := range memberCount {
			idx := int(engine.Uint16(data[offset : offset+2]))
			if idx >= metricCount {
				return SharedTimestampTable{}, fmt.Errorf("%w: shared index %d exceeds metric count %d", errs.ErrInvalidHeaderFlags, idx, metricCount)
			}
			if idx == canonicalIdx {
				return SharedTimestampTable{}, fmt.Errorf("%w: canonical index %d cannot share with itself", errs.ErrInvalidSharedTimestampTable, canonicalIdx)
			}
			if status[idx] != 0 {
				return SharedTimestampTable{}, fmt.Errorf("%w: shared index %d conflicts with existing assignment", errs.ErrInvalidSharedTimestampTable, idx)
			}

			sharedIndices[j] = idx
			status[idx] = 2
			offset += 2
		}

		status[canonicalIdx] = 1

		groups[i] = SharedTimestampGroup{
			CanonicalIndex: canonicalIdx,
			SharedIndices:  sharedIndices,
		}
	}

	if offset != len(data) {
		return SharedTimestampTable{}, fmt.Errorf("%w: shared timestamp table has %d trailing bytes", errs.ErrInvalidSharedTimestampTable, len(data)-offset)
	}

	return SharedTimestampTable{Groups: groups}, nil
}

// ApplySharedTimestampTable parses and applies shared timestamp mappings directly
// to index entries without materializing an intermediate table structure.
//
// Parameters:
//   - data: Byte slice containing the serialized shared timestamp table
//   - engine: Endian engine for byte order
//   - metricCount: Total number of metrics in the blob
//   - indexEntries: Index entries to mutate with canonical timestamp offsets/lengths
//
// Returns:
//   - error: Parse or validation errors
func ApplySharedTimestampTable(data []byte, engine endian.EndianEngine, metricCount int, indexEntries []NumericIndexEntry) error {
	if len(indexEntries) < metricCount {
		return fmt.Errorf("%w: index entries shorter than metric count", errs.ErrInvalidIndexEntrySize)
	}
	if len(data) < 2 {
		return fmt.Errorf("%w: shared timestamp table too short", errs.ErrInvalidHeaderFlags)
	}

	groupCount := int(engine.Uint16(data[0:2]))
	if groupCount == 0 {
		return fmt.Errorf("%w: shared timestamp table cannot be empty", errs.ErrInvalidSharedTimestampTable)
	}

	offset := 2
	status := make([]byte, metricCount) // 0=unused, 1=canonical, 2=shared

	for i := range groupCount {
		if offset+4 > len(data) {
			return fmt.Errorf("%w: shared timestamp table truncated at group %d", errs.ErrInvalidHeaderFlags, i)
		}

		canonicalIdx := int(engine.Uint16(data[offset : offset+2]))
		offset += 2

		memberCount := int(engine.Uint16(data[offset : offset+2]))
		offset += 2

		if offset+2*memberCount > len(data) {
			return fmt.Errorf("%w: shared timestamp table truncated at group %d members", errs.ErrInvalidHeaderFlags, i)
		}
		if canonicalIdx >= metricCount {
			return fmt.Errorf("%w: canonical index %d exceeds metric count %d", errs.ErrInvalidHeaderFlags, canonicalIdx, metricCount)
		}
		if status[canonicalIdx] == 1 {
			return fmt.Errorf("%w: canonical index %d appears in multiple groups", errs.ErrInvalidSharedTimestampTable, canonicalIdx)
		}
		if status[canonicalIdx] == 2 {
			return fmt.Errorf("%w: canonical index %d is already used as a shared index", errs.ErrInvalidSharedTimestampTable, canonicalIdx)
		}

		canonTsOffset := indexEntries[canonicalIdx].TimestampOffset
		canonTsLength := indexEntries[canonicalIdx].TimestampLength

		for range memberCount {
			idx := int(engine.Uint16(data[offset : offset+2]))
			if idx >= metricCount {
				return fmt.Errorf("%w: shared index %d exceeds metric count %d", errs.ErrInvalidHeaderFlags, idx, metricCount)
			}
			if idx == canonicalIdx {
				return fmt.Errorf("%w: canonical index %d cannot share with itself", errs.ErrInvalidSharedTimestampTable, canonicalIdx)
			}
			if status[idx] != 0 {
				return fmt.Errorf("%w: shared index %d conflicts with existing assignment", errs.ErrInvalidSharedTimestampTable, idx)
			}

			indexEntries[idx].TimestampOffset = canonTsOffset
			indexEntries[idx].TimestampLength = canonTsLength
			status[idx] = 2
			offset += 2
		}

		status[canonicalIdx] = 1
	}

	if offset != len(data) {
		return fmt.Errorf("%w: shared timestamp table has %d trailing bytes", errs.ErrInvalidSharedTimestampTable, len(data)-offset)
	}

	return nil
}

// prescanSharedTimestampTable validates structural integrity and counts total shared members.
func prescanSharedTimestampTable(data []byte, groupCount int, engine endian.EndianEngine) (int, error) {
	offset := 2
	totalMembers := 0

	for i := range groupCount {
		if offset+4 > len(data) {
			return 0, fmt.Errorf("%w: shared timestamp table truncated at group %d", errs.ErrInvalidHeaderFlags, i)
		}

		offset += 2 // skip canonical index
		memberCount := int(engine.Uint16(data[offset : offset+2]))
		offset += 2
		totalMembers += memberCount

		if offset+2*memberCount > len(data) {
			return 0, fmt.Errorf("%w: shared timestamp table truncated at group %d members", errs.ErrInvalidHeaderFlags, i)
		}

		offset += 2 * memberCount
	}

	return totalMembers, nil
}
