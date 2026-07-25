package blob

import "github.com/arloliu/mebo/section"

// newNumericTestIndex builds a V1-style (first-wins) ordinal index from the given
// entries in index order. Used by white-box tests that construct blobs directly.
func newNumericTestIndex(entries ...section.NumericIndexEntry) indexMaps[section.NumericIndexEntry] {
	m := indexMaps[section.NumericIndexEntry]{sorted: entries}
	m.byID = make(map[uint64]int, len(entries))
	for i := range entries {
		if _, ok := m.byID[entries[i].MetricID]; !ok {
			m.byID[entries[i].MetricID] = i
		}
	}

	return m
}
