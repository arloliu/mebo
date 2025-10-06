package encoding

import (
	"fmt"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
)

// EncodeMetricNames encodes a list of metric names into a length-prefixed binary format.
// Format: [Count: uint16] [Len1: uint16][Name1: UTF-8] [Len2: uint16][Name2: UTF-8] ...
//
// This function is used by both NumericEncoder and TextEncoder to create the metric names payload
// when collision detection is enabled.
//
// Parameters:
//   - names: The ordered list of metric names to encode
//   - engine: The endian engine to use for encoding length fields
//
// Returns:
//   - []byte: The encoded metric names payload
//   - error: An error if encoding fails (e.g., name too long, count exceeds uint16)
func EncodeMetricNames(names []string, engine endian.EndianEngine) ([]byte, error) {
	if len(names) > 65535 {
		return nil, fmt.Errorf("%w: metric count %d exceeds maximum 65535", errs.ErrInvalidMetricNamesCount, len(names))
	}

	// Calculate total size: 2 bytes for count + (2 bytes + name length) for each name
	totalSize := 2
	for _, name := range names {
		nameLen := len(name)
		if nameLen > 65535 {
			return nil, fmt.Errorf("%w: metric name '%s' exceeds maximum length 65535 bytes", errs.ErrInvalidMetricName, name)
		}
		totalSize += 2 + nameLen // Length prefix + string bytes
	}

	buf := make([]byte, totalSize)
	offset := 0

	// Write count
	engine.PutUint16(buf[offset:], uint16(len(names))) //nolint: gosec
	offset += 2

	// Write each name with length prefix
	for _, name := range names {
		nameBytes := []byte(name)
		nameLen := len(nameBytes)

		engine.PutUint16(buf[offset:], uint16(nameLen)) //nolint: gosec
		offset += 2

		copy(buf[offset:], nameBytes)
		offset += nameLen
	}

	return buf, nil
}

// DecodeMetricNames decodes a length-prefixed metric names payload.
// Format: [Count: uint16] [Len1: uint16][Name1: UTF-8] [Len2: uint16][Name2: UTF-8] ...
//
// This function is used by both NumericDecoder and TextDecoder to parse the metric names payload
// when the HasMetricNames flag is set.
//
// Parameters:
//   - data: The raw byte slice containing the metric names payload (starting from the count field)
//   - engine: The endian engine to use for decoding length fields
//
// Returns:
//   - []string: The decoded list of metric names (in order)
//   - int: The total number of bytes consumed (offset after last name)
//   - error: An error if decoding fails (e.g., truncated data, invalid length)
func DecodeMetricNames(data []byte, engine endian.EndianEngine) ([]string, int, error) {
	offset := 0

	// Read count
	if len(data) < offset+2 {
		return nil, 0, fmt.Errorf("%w: cannot read metric names count (need 2 bytes, have %d)", errs.ErrInvalidMetricNamesPayload, len(data))
	}

	count := engine.Uint16(data[offset:])
	offset += 2

	// Pre-allocate slice for names
	names := make([]string, count)

	// Read each name
	for i := 0; i < int(count); i++ {
		// Read name length
		if len(data) < offset+2 {
			return nil, 0, fmt.Errorf("%w: cannot read length for metric name %d (need 2 bytes at offset %d, have %d total)",
				errs.ErrInvalidMetricNamesPayload, i, offset, len(data))
		}

		nameLen := engine.Uint16(data[offset:])
		offset += 2

		// Read name bytes
		if len(data) < offset+int(nameLen) {
			return nil, 0, fmt.Errorf("%w: cannot read metric name %d (need %d bytes at offset %d, have %d total)",
				errs.ErrInvalidMetricNamesPayload, i, nameLen, offset, len(data))
		}

		// Convert bytes to string (creates a copy)
		names[i] = string(data[offset : offset+int(nameLen)])
		offset += int(nameLen)
	}

	return names, offset, nil
}

// VerifyMetricNamesHashes verifies that the provided metric names hash to the expected metric IDs.
// This is used by decoders to ensure data integrity when the metric names payload is present.
//
// The names slice and metricIDs slice must have the same length and be in corresponding order:
// hash(names[i]) must equal metricIDs[i].
//
// Parameters:
//   - names: The decoded metric names (in order)
//   - metricIDs: The metric IDs from index entries (in same order as names)
//   - hashFunc: A function that computes the hash for a metric name (e.g., hash.ID)
//
// Returns:
//   - error: An error if any hash mismatch is detected, nil if all hashes match
func VerifyMetricNamesHashes(names []string, metricIDs []uint64, hashFunc func(string) uint64) error {
	if len(names) != len(metricIDs) {
		return fmt.Errorf("%w: metric names count %d does not match metric IDs count %d",
			errs.ErrInvalidMetricNamesCount, len(names), len(metricIDs))
	}

	for i, name := range names {
		expectedHash := hashFunc(name)
		actualHash := metricIDs[i]

		if expectedHash != actualHash {
			return fmt.Errorf("%w: metric name '%s' at index %d: expected hash 0x%016x, got 0x%016x",
				errs.ErrHashMismatch, name, i, expectedHash, actualHash)
		}
	}

	return nil
}
