package section

import (
	"testing"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/stretchr/testify/require"
)

func TestParseSharedTimestampTableRejectsTrailingBytes(t *testing.T) {
	table := SharedTimestampTable{
		Groups: []SharedTimestampGroup{{
			CanonicalIndex: 0,
			SharedIndices:  []int{1},
		}},
	}

	data := make([]byte, table.Size()+1)
	engine := endian.GetLittleEndianEngine()
	offset := table.WriteToSlice(data, 0, engine)
	data[offset] = 0xAB

	_, err := ParseSharedTimestampTable(data, engine, 2)
	require.ErrorIs(t, err, errs.ErrInvalidSharedTimestampTable)
}

func TestParseSharedTimestampTableRejectsDuplicateSharedIndex(t *testing.T) {
	table := SharedTimestampTable{
		Groups: []SharedTimestampGroup{{
			CanonicalIndex: 0,
			SharedIndices:  []int{1, 1},
		}},
	}

	data := make([]byte, table.Size())
	engine := endian.GetLittleEndianEngine()
	table.WriteToSlice(data, 0, engine)

	_, err := ParseSharedTimestampTable(data, engine, 3)
	require.ErrorIs(t, err, errs.ErrInvalidSharedTimestampTable)
}

func TestParseSharedTimestampTableRejectsSelfReference(t *testing.T) {
	table := SharedTimestampTable{
		Groups: []SharedTimestampGroup{{
			CanonicalIndex: 1,
			SharedIndices:  []int{1},
		}},
	}

	data := make([]byte, table.Size())
	engine := endian.GetLittleEndianEngine()
	table.WriteToSlice(data, 0, engine)

	_, err := ParseSharedTimestampTable(data, engine, 3)
	require.ErrorIs(t, err, errs.ErrInvalidSharedTimestampTable)
}

func TestParseSharedTimestampTableRejectsSharedIndexAcrossGroups(t *testing.T) {
	table := SharedTimestampTable{
		Groups: []SharedTimestampGroup{
			{
				CanonicalIndex: 0,
				SharedIndices:  []int{2},
			},
			{
				CanonicalIndex: 1,
				SharedIndices:  []int{2},
			},
		},
	}

	data := make([]byte, table.Size())
	engine := endian.GetLittleEndianEngine()
	table.WriteToSlice(data, 0, engine)

	_, err := ParseSharedTimestampTable(data, engine, 4)
	require.ErrorIs(t, err, errs.ErrInvalidSharedTimestampTable)
}

func TestParseSharedTimestampTableRejectsCanonicalUsedAsShared(t *testing.T) {
	table := SharedTimestampTable{
		Groups: []SharedTimestampGroup{
			{
				CanonicalIndex: 0,
				SharedIndices:  []int{1},
			},
			{
				CanonicalIndex: 1,
				SharedIndices:  []int{2},
			},
		},
	}

	data := make([]byte, table.Size())
	engine := endian.GetLittleEndianEngine()
	table.WriteToSlice(data, 0, engine)

	_, err := ParseSharedTimestampTable(data, engine, 4)
	require.ErrorIs(t, err, errs.ErrInvalidSharedTimestampTable)
}

func BenchmarkParseSharedTimestampTable(b *testing.B) {
	scenarios := []struct {
		name        string
		groupCount  int
		membersEach int
		metricCount int
	}{
		{"1group_149members_150metrics", 1, 149, 150},
		{"3groups_40members_150metrics", 3, 40, 150},
		{"10groups_10members_150metrics", 10, 10, 150},
	}

	engine := endian.GetLittleEndianEngine()

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			groups := make([]SharedTimestampGroup, sc.groupCount)
			idx := 0
			for g := range sc.groupCount {
				canonical := idx
				idx++
				shared := make([]int, sc.membersEach)
				for m := range sc.membersEach {
					shared[m] = idx
					idx++
				}
				groups[g] = SharedTimestampGroup{
					CanonicalIndex: canonical,
					SharedIndices:  shared,
				}
			}

			table := SharedTimestampTable{Groups: groups}
			data := make([]byte, table.Size())
			table.WriteToSlice(data, 0, engine)

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_, err := ParseSharedTimestampTable(data, engine, sc.metricCount)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkApplySharedTimestampTable(b *testing.B) {
	scenarios := []struct {
		name        string
		groupCount  int
		membersEach int
		metricCount int
	}{
		{"1group_149members_150metrics", 1, 149, 150},
		{"3groups_40members_150metrics", 3, 40, 150},
		{"10groups_10members_150metrics", 10, 10, 150},
	}

	engine := endian.GetLittleEndianEngine()

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			table := makeSharedTimestampBenchmarkTable(sc.groupCount, sc.membersEach)
			data := make([]byte, table.Size())
			table.WriteToSlice(data, 0, engine)

			seedEntries := makeSharedTimestampBenchmarkEntries(sc.metricCount)
			workEntries := make([]NumericIndexEntry, len(seedEntries))

			b.Run("DirectApply", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for b.Loop() {
					copy(workEntries, seedEntries)
					if err := ApplySharedTimestampTable(data, engine, sc.metricCount, workEntries); err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("ParseThenApply", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for b.Loop() {
					copy(workEntries, seedEntries)

					parsed, err := ParseSharedTimestampTable(data, engine, sc.metricCount)
					if err != nil {
						b.Fatal(err)
					}

					for _, group := range parsed.Groups {
						canonTsOffset := workEntries[group.CanonicalIndex].TimestampOffset
						canonTsLength := workEntries[group.CanonicalIndex].TimestampLength
						for _, sharedIdx := range group.SharedIndices {
							workEntries[sharedIdx].TimestampOffset = canonTsOffset
							workEntries[sharedIdx].TimestampLength = canonTsLength
						}
					}
				}
			})
		})
	}
}

func makeSharedTimestampBenchmarkTable(groupCount, membersEach int) SharedTimestampTable {
	groups := make([]SharedTimestampGroup, groupCount)
	idx := 0

	for g := range groupCount {
		canonical := idx
		idx++

		shared := make([]int, membersEach)
		for m := range membersEach {
			shared[m] = idx
			idx++
		}

		groups[g] = SharedTimestampGroup{
			CanonicalIndex: canonical,
			SharedIndices:  shared,
		}
	}

	return SharedTimestampTable{Groups: groups}
}

func makeSharedTimestampBenchmarkEntries(metricCount int) []NumericIndexEntry {
	entries := make([]NumericIndexEntry, metricCount)

	for i := range metricCount {
		entries[i] = NumericIndexEntry{
			MetricID:        uint64(i + 1),
			Count:           10,
			TimestampOffset: i * 80,
			TimestampLength: 80,
			ValueOffset:     i * 80,
			ValueLength:     80,
			TagOffset:       0,
			TagLength:       0,
		}
	}

	return entries
}
