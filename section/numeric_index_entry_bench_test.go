package section

import (
	"bytes"
	"testing"

	"github.com/arloliu/mebo/endian"
)

// Benchmark writing multiple entries (realistic scenario)
func BenchmarkNumericIndexEntry_Bytes(b *testing.B) {
	entries := make([]NumericIndexEntry, 150)
	for i := range entries {
		entries[i] = NewNumericIndexEntry(uint64(i+1000), 10)
		entries[i].TimestampOffset = i * 80
		entries[i].ValueOffset = i * 80
	}
	engine := endian.GetLittleEndianEngine()
	buf := &bytes.Buffer{}
	buf.Grow(NumericIndexEntrySize * 150)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		for i := range entries {
			data := entries[i].Bytes(engine)
			buf.Write(data)
		}
		buf.Reset()
	}
}

func BenchmarkNumericIndexEntry_WriteTo(b *testing.B) {
	entries := make([]NumericIndexEntry, 150)
	for i := range entries {
		entries[i] = NewNumericIndexEntry(uint64(i+1000), 10)
		entries[i].TimestampOffset = i * 80
		entries[i].ValueOffset = i * 80
	}
	engine := endian.GetLittleEndianEngine()
	buf := &bytes.Buffer{}
	buf.Grow(NumericIndexEntrySize * 150)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		for i := range entries {
			entries[i].WriteTo(buf, engine)
		}
		buf.Reset()
	}
}

func BenchmarkNumericIndexEntry_WriteToSlice(b *testing.B) {
	entries := make([]NumericIndexEntry, 150)
	for i := range entries {
		entries[i] = NewNumericIndexEntry(uint64(i+1000), 10)
		entries[i].TimestampOffset = i * 80
		entries[i].ValueOffset = i * 80
	}
	engine := endian.GetLittleEndianEngine()
	data := make([]byte, NumericIndexEntrySize*150) // Pre-allocate exact size
	buf := &bytes.Buffer{}
	buf.Grow(NumericIndexEntrySize * 150)

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		offset := 0
		for i := range entries {
			offset = entries[i].WriteToSlice(data, offset, engine)
		}
		buf.Write(data)
		buf.Reset()
		// No reset needed, we overwrite the same slice
	}
}
