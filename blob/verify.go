package blob

import (
	"fmt"

	"github.com/arloliu/mebo/errs"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/section"
)

// Verify decodes a numeric or text blob and checks that every metric's data decodes completely.
//
// Decoding a blob validates its structure, its header, index and payload extents, but not its contents:
// a truncated or corrupted column can pass those checks and then end early when it is iterated.
// Verify walks every metric to the end, which costs a full decode of the blob,
// so it suits checking blobs from an untrusted source or diagnosing a bad one, not every read.
//
// A numeric metric passes when its timestamp, value and, with tags enabled, tag columns
// each decode to the metric's point count.
// A text metric passes when its records decode to the point count and use up the metric's data exactly.
//
// Verify does not detect a change that leaves a valid encoding,
// such as a flipped bit inside a value, which only a checksum can catch.
//
// Parameters:
//   - data: The encoded blob, as returned by a numeric or text encoder's Finish
//
// Returns:
//   - error: nil if the blob decodes completely;
//     the decoder's error if it fails to open;
//     errs.ErrInvalidMagicNumber if data is neither a numeric nor a text blob;
//     otherwise an error wrapping errs.ErrInvalidNumOfDataPoints that names the first incomplete metric and column
func Verify(data []byte) error {
	switch {
	case section.IsNumericBlob(data):
		decoder, err := NewNumericDecoder(data)
		if err != nil {
			return err
		}

		b, err := decoder.Decode()
		if err != nil {
			return err
		}

		return b.verify()
	case section.IsTextBlob(data):
		decoder, err := NewTextDecoder(data)
		if err != nil {
			return err
		}

		b, err := decoder.Decode()
		if err != nil {
			return err
		}

		return b.verify()
	default:
		return fmt.Errorf("%w: neither a numeric nor a text blob", errs.ErrInvalidMagicNumber)
	}
}

// verify checks that every metric's columns decode to the metric's point count.
// It drives the same static decode loops as ForEach, with a callback that keeps going.
func (b NumericBlob) verify() error {
	entries := b.index.sorted
	for i := range entries {
		entry := entries[i]
		if entry.Count == 0 {
			continue
		}

		n := b.forEachTimestampsFromEntry(entry, 0, func(int, int64) bool { return true })
		if n != entry.Count {
			return errIncompleteColumn(entry.MetricID, "timestamp", n, entry.Count)
		}

		n = b.forEachValuesFromEntry(entry, 0, func(int, float64) bool { return true })
		if n != entry.Count {
			return errIncompleteColumn(entry.MetricID, "value", n, entry.Count)
		}

		if !b.HasTag() {
			continue
		}

		n = 0
		if tagBytes, ok := safeSlice(b.tagPayload, entry.TagOffset, entry.TagLength); ok {
			n = ienc.NewTagDecoder(b.Engine()).Each(tagBytes, entry.Count, 0, func(int, string) bool { return true })
		}
		if n != entry.Count {
			return errIncompleteColumn(entry.MetricID, "tag", n, entry.Count)
		}
	}

	return nil
}

// verify checks that every metric's records decode to the metric's point count
// and end exactly where the metric's data ends.
func (b TextBlob) verify() error {
	entries := b.index.sorted
	for i := range entries {
		entry := &entries[i]
		count := int(entry.Count)

		// A metric with no points must have no data either, so zero counts are walked too.
		data, ok := safeSlice(b.dataPayload, int(entry.Offset), int(entry.Size))
		if !ok {
			return errIncompleteColumn(entry.MetricID, "record", 0, count)
		}

		n, used, err := b.walkRecords(data, count)
		if err != nil {
			return fmt.Errorf("%w: metric ID %d record %d: %w", errs.ErrInvalidNumOfDataPoints, entry.MetricID, n, err)
		}
		if n != count {
			return errIncompleteColumn(entry.MetricID, "record", n, count)
		}
		if used != len(data) {
			return fmt.Errorf("%w: metric ID %d records end at byte %d of %d",
				errs.ErrInvalidNumOfDataPoints, entry.MetricID, used, len(data))
		}
	}

	return nil
}

// walkRecords steps over up to count records the way decodeDataPoints reads them,
// without copying values or tags.
// It returns the number of complete records, the bytes they use,
// and the timestamp decode error that stopped the walk, if any.
func (b TextBlob) walkRecords(data []byte, count int) (records, used int, err error) {
	offset := 0
	lastTs := b.startTimeMicros
	for i := range count {
		_, n, err := b.decodeTimestampAt(data, offset, &lastTs)
		if err != nil {
			return i, offset, err
		}
		offset += n

		lengths := 1
		if b.HasTag() {
			lengths = 2
		}
		if offset+lengths > len(data) {
			return i, offset, nil
		}

		size := int(data[offset])
		if b.HasTag() {
			size += int(data[offset+1])
		}
		offset += lengths

		if offset+size > len(data) {
			return i, offset, nil
		}
		offset += size
	}

	return count, offset, nil
}

// errIncompleteColumn reports a column that decoded fewer points than its metric declares.
func errIncompleteColumn(metricID uint64, column string, decoded, count int) error {
	return fmt.Errorf("%w: metric ID %d %s column decoded %d of %d points",
		errs.ErrInvalidNumOfDataPoints, metricID, column, decoded, count)
}
