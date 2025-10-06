package encoding

import (
	"testing"

	"github.com/arloliu/mebo/endian"
)

func BenchmarkTagEncoder_Write_Empty(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	b.ResetTimer()
	for b.Loop() {
		encoder.Write("")
	}
}

func BenchmarkTagEncoder_Write_Short(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	tag := "ok"

	b.ResetTimer()
	for b.Loop() {
		encoder.Write(tag)
	}
}

func BenchmarkTagEncoder_Write_Medium(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	tag := "severity=high,user_id=12345"

	b.ResetTimer()
	for b.Loop() {
		encoder.Write(tag)
	}
}

func BenchmarkTagEncoder_Write_Long(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	// 200 bytes tag (requires 2-byte varint)
	tag := string(make([]byte, 200))
	for i := range []byte(tag) {
		tag = tag[:i] + "x" + tag[i+1:]
	}

	b.ResetTimer()
	for b.Loop() {
		encoder.Write(tag)
	}
}

func BenchmarkTagEncoder_Write_UTF8(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	tag := "用户错误：无效的输入" // Mixed UTF-8

	b.ResetTimer()
	for b.Loop() {
		encoder.Write(tag)
	}
}

func BenchmarkTagEncoder_WriteSlice_10Tags(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	tags := []string{
		"severity=low",
		"severity=medium",
		"severity=high",
		"user_id=123",
		"user_id=456",
		"region=us-west",
		"region=us-east",
		"",
		"status=ok",
		"status=error",
	}

	b.ResetTimer()
	for b.Loop() {
		encoder.WriteSlice(tags)
	}
}

func BenchmarkTagEncoder_WriteSlice_100Tags(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	tags := make([]string, 100)
	for i := range tags {
		if i%10 == 0 {
			tags[i] = "" // 10% empty
		} else if i%5 == 0 {
			tags[i] = "short"
		} else {
			tags[i] = "user_id=12345,region=us-west"
		}
	}

	b.ResetTimer()
	for b.Loop() {
		encoder.WriteSlice(tags)
	}
}

func BenchmarkTagEncoder_WriteSlice_MixedSizes(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	longTag := string(make([]byte, 150))
	for i := range []byte(longTag) {
		longTag = longTag[:i] + "x" + longTag[i+1:]
	}

	tags := []string{
		"",
		"a",
		"hello",
		"severity=high",
		longTag,
		"user_id=12345,region=us-west,host=server1",
	}

	b.ResetTimer()
	for b.Loop() {
		encoder.WriteSlice(tags)
	}
}

func BenchmarkTagEncoder_ResetAndReuse(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	b.ResetTimer()
	for b.Loop() {
		encoder.Write("test_tag")
		encoder.Reset()
	}
}

func BenchmarkTagEncoder_FullCycle(b *testing.B) {
	engine := endian.GetLittleEndianEngine()

	tags := []string{
		"severity=high",
		"user_id=12345",
		"region=us-west",
		"host=server1",
		"",
	}

	b.ResetTimer()
	for b.Loop() {
		encoder := NewTagEncoder(engine)
		encoder.WriteSlice(tags)
		_ = encoder.Bytes()
		_ = encoder.Len()
		_ = encoder.Size()
		encoder.Finish()
	}
}

// Benchmark comparison: Write vs WriteSlice for multiple tags
func BenchmarkTagEncoder_Write_10Times(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	tags := []string{
		"tag1", "tag2", "tag3", "tag4", "tag5",
		"tag6", "tag7", "tag8", "tag9", "tag10",
	}

	b.ResetTimer()
	for b.Loop() {
		for _, tag := range tags {
			encoder.Write(tag)
		}
	}
}

func BenchmarkTagEncoder_WriteSlice_10Tags_Compare(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	tags := []string{
		"tag1", "tag2", "tag3", "tag4", "tag5",
		"tag6", "tag7", "tag8", "tag9", "tag10",
	}

	b.ResetTimer()
	for b.Loop() {
		encoder.WriteSlice(tags)
	}
}

// Memory allocation benchmarks
func BenchmarkTagEncoder_Allocations_SingleWrite(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	tag := "severity=high"

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		encoder := NewTagEncoder(engine)
		encoder.Write(tag)
	}
}

func BenchmarkTagEncoder_Allocations_BulkWrite(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	tags := []string{
		"tag1", "tag2", "tag3", "tag4", "tag5",
		"tag6", "tag7", "tag8", "tag9", "tag10",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		encoder := NewTagEncoder(engine)
		encoder.WriteSlice(tags)
	}
}

// === TagDecoder Benchmarks ===

func BenchmarkTagDecoder_All_SingleTag(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	encoder.Write("test_tag")
	data := encoder.Bytes()

	b.ResetTimer()
	for b.Loop() {
		for v := range decoder.All(data, 1) {
			_ = v
		}
	}
}

func BenchmarkTagDecoder_All_10Tags(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := []string{
		"tag1", "tag2", "tag3", "tag4", "tag5",
		"tag6", "tag7", "tag8", "tag9", "tag10",
	}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ResetTimer()
	for b.Loop() {
		for tag := range decoder.All(data, len(tags)) {
			_ = tag // Consume tag
		}
	}
}

func BenchmarkTagDecoder_All_100Tags(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := make([]string, 100)
	for i := range tags {
		tags[i] = "tag_value_" + string(rune('0'+i%10))
	}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ResetTimer()
	for b.Loop() {
		for tag := range decoder.All(data, len(tags)) {
			_ = tag // Consume tag
		}
	}
}

func BenchmarkTagDecoder_All_EmptyTags(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := make([]string, 10)
	// All empty strings
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ResetTimer()
	for b.Loop() {
		for tag := range decoder.All(data, len(tags)) {
			_ = tag // Consume tag
		}
	}
}

func BenchmarkTagDecoder_All_MixedSizes(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := []string{
		"",
		"short",
		"medium_length_tag_value",
		string(make([]byte, 200)),
		"app=myapp",
		"",
		"service=api",
		string(make([]byte, 1000)),
	}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ResetTimer()
	for b.Loop() {
		for tag := range decoder.All(data, len(tags)) {
			_ = tag // Consume tag
		}
	}
}

func BenchmarkTagDecoder_All_UTF8Tags(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := []string{"你好", "世界", "emoji=🚀", "测试标签"}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ResetTimer()
	for b.Loop() {
		for tag := range decoder.All(data, len(tags)) {
			_ = tag // Consume tag
		}
	}
}

func BenchmarkTagDecoder_All_EarlyExit(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := make([]string, 100)
	for i := range tags {
		tags[i] = "tag_value"
	}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ResetTimer()
	for b.Loop() {
		count := 0
		for tag := range decoder.All(data, len(tags)) {
			_ = tag // Consume tag
			count++
			if count == 10 {
				break
			}
		}
	}
}

func BenchmarkTagDecoder_At_FirstTag(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := []string{"first", "second", "third", "fourth", "fifth"}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ResetTimer()
	for b.Loop() {
		_, _ = decoder.At(data, 0, len(tags))
	}
}

func BenchmarkTagDecoder_At_MiddleTag(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := make([]string, 100)
	for i := range tags {
		tags[i] = "tag_value"
	}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ResetTimer()
	for b.Loop() {
		_, _ = decoder.At(data, 50, len(tags))
	}
}

func BenchmarkTagDecoder_At_LastTag(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := make([]string, 100)
	for i := range tags {
		tags[i] = "tag_value"
	}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ResetTimer()
	for b.Loop() {
		_, _ = decoder.At(data, 99, len(tags))
	}
}

func BenchmarkTagDecoder_At_OutOfBounds(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := []string{"first", "second", "third"}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ResetTimer()
	for b.Loop() {
		_, _ = decoder.At(data, 100, len(tags))
	}
}

func BenchmarkTagDecoder_At_RandomAccess(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := make([]string, 1000)
	for i := range tags {
		tags[i] = "tag_value_" + string(rune('0'+i%10))
	}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	indices := []int{0, 100, 250, 500, 750, 999}

	b.ResetTimer()
	for b.Loop() {
		for _, idx := range indices {
			_, _ = decoder.At(data, idx, len(tags))
		}
	}
}

// === Round-trip Benchmarks ===

func BenchmarkTagCodec_RoundTrip_10Tags(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := []string{
		"app=myapp", "service=api", "region=us-west",
		"env=prod", "version=1.2.3", "host=server01",
		"datacenter=dc1", "team=backend", "project=main",
		"severity=high",
	}

	b.ResetTimer()
	for b.Loop() {
		encoder.Reset()
		encoder.WriteSlice(tags)
		data := encoder.Bytes()

		for tag := range decoder.All(data, len(tags)) {
			_ = tag // Consume tag
		}
	}
}

func BenchmarkTagCodec_RoundTrip_100Tags(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := make([]string, 100)
	for i := range tags {
		tags[i] = "tag_key=tag_value"
	}

	b.ResetTimer()
	for b.Loop() {
		encoder.Reset()
		encoder.WriteSlice(tags)
		data := encoder.Bytes()

		for tag := range decoder.All(data, len(tags)) {
			_ = tag // Consume tag
		}
	}
}

// === Allocation Benchmarks ===

func BenchmarkTagDecoder_Allocations_All(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := []string{"tag1", "tag2", "tag3", "tag4", "tag5"}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for tag := range decoder.All(data, len(tags)) {
			_ = tag // Consume tag
		}
	}
}

func BenchmarkTagDecoder_Allocations_At(b *testing.B) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)
	decoder := NewTagDecoder(engine)

	tags := []string{"tag1", "tag2", "tag3", "tag4", "tag5"}
	encoder.WriteSlice(tags)
	data := encoder.Bytes()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = decoder.At(data, 2, len(tags))
	}
}
