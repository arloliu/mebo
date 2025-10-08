package encoding

import (
	"fmt"
	"strings"
	"testing"

	"github.com/arloliu/mebo/compress"
	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/format"
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

// Compression benchmark tests to measure the actual compression improvement
// of the uint8 grouped layout vs the old varint interleaved layout.

// BenchmarkTagCompression_Zstd_ShortTags measures compression with short tags (typical monitoring)
func BenchmarkTagCompression_Zstd_ShortTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionZstd, generateShortTags(150))
}

// BenchmarkTagCompression_Zstd_MediumTags measures compression with medium tags
func BenchmarkTagCompression_Zstd_MediumTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionZstd, generateMediumTags(150))
}

// BenchmarkTagCompression_Zstd_LongTags measures compression with long tags (128-255 chars)
func BenchmarkTagCompression_Zstd_LongTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionZstd, generateLongTags(150))
}

// BenchmarkTagCompression_Zstd_MixedTags measures compression with mixed length tags
func BenchmarkTagCompression_Zstd_MixedTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionZstd, generateMixedTags(150))
}

// BenchmarkTagCompression_LZ4_ShortTags measures LZ4 compression with short tags
func BenchmarkTagCompression_LZ4_ShortTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionLZ4, generateShortTags(150))
}

// BenchmarkTagCompression_LZ4_MediumTags measures LZ4 compression with medium tags
func BenchmarkTagCompression_LZ4_MediumTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionLZ4, generateMediumTags(150))
}

// BenchmarkTagCompression_LZ4_LongTags measures LZ4 compression with long tags
func BenchmarkTagCompression_LZ4_LongTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionLZ4, generateLongTags(150))
}

// BenchmarkTagCompression_LZ4_MixedTags measures LZ4 compression with mixed tags
func BenchmarkTagCompression_LZ4_MixedTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionLZ4, generateMixedTags(150))
}

// BenchmarkTagCompression_S2_ShortTags measures S2 compression with short tags
func BenchmarkTagCompression_S2_ShortTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionS2, generateShortTags(150))
}

// BenchmarkTagCompression_S2_MediumTags measures S2 compression with medium tags
func BenchmarkTagCompression_S2_MediumTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionS2, generateMediumTags(150))
}

// BenchmarkTagCompression_S2_LongTags measures S2 compression with long tags
func BenchmarkTagCompression_S2_LongTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionS2, generateLongTags(150))
}

// BenchmarkTagCompression_S2_MixedTags measures S2 compression with mixed tags
func BenchmarkTagCompression_S2_MixedTags(b *testing.B) {
	benchmarkTagCompression(b, format.CompressionS2, generateMixedTags(150))
}

// benchmarkTagCompression is the core compression benchmark function
func benchmarkTagCompression(b *testing.B, codecType format.CompressionType, tags []string) {
	engine := endian.GetLittleEndianEngine()
	encoder := NewTagEncoder(engine)

	// Encode tags
	encoder.WriteSlice(tags)
	uncompressed := encoder.Bytes()

	// Get codec
	codec, err := compress.CreateCodec(codecType, "tag")
	if err != nil {
		b.Fatalf("Failed to get codec: %v", err)
	}

	// Compress
	compressed, err := codec.Compress(uncompressed)
	if err != nil {
		b.Fatalf("Failed to compress: %v", err)
	}

	// Calculate and report compression ratio
	uncompressedSize := len(uncompressed)
	compressedSize := len(compressed)
	ratio := float64(uncompressedSize) / float64(compressedSize)
	savings := 100.0 * (1.0 - float64(compressedSize)/float64(uncompressedSize))

	b.ReportMetric(float64(uncompressedSize), "uncompressed_bytes")
	b.ReportMetric(float64(compressedSize), "compressed_bytes")
	b.ReportMetric(ratio, "compression_ratio")
	b.ReportMetric(savings, "space_savings_%")

	// Benchmark compression performance
	b.ResetTimer()
	for b.Loop() {
		_, _ = codec.Compress(uncompressed)
	}
}

// TestTagCompressionRatio_Report generates a detailed compression report
// This is a test (not benchmark) that outputs a comprehensive comparison table
func TestTagCompressionRatio_Report(t *testing.T) {
	engine := endian.GetLittleEndianEngine()

	testCases := []struct {
		name     string
		tags     []string
		tagCount int
	}{
		{"Short tags (10-20 chars)", generateShortTags(150), 150},
		{"Medium tags (30-50 chars)", generateMediumTags(150), 150},
		{"Long tags (150-200 chars)", generateLongTags(150), 150},
		{"Mixed tags", generateMixedTags(150), 150},
		{"Real monitoring tags", generateRealisticMonitoringTags(150), 150},
	}

	codecs := []format.CompressionType{
		format.CompressionZstd,
		format.CompressionLZ4,
		format.CompressionS2,
	}

	fmt.Println("\n" + strings.Repeat("=", 100))
	fmt.Println("TAG COMPRESSION ANALYSIS REPORT")
	fmt.Println("Layout: [len1:uint8][len2:uint8]...[tag1][tag2]...")
	fmt.Println(strings.Repeat("=", 100))

	for _, tc := range testCases {
		encoder := NewTagEncoder(engine)
		encoder.WriteSlice(tc.tags)
		uncompressed := encoder.Bytes()

		fmt.Printf("\n%-40s (Count: %d)\n", tc.name, tc.tagCount)
		fmt.Printf("  Uncompressed size: %d bytes\n", len(uncompressed))
		fmt.Println("  " + strings.Repeat("-", 80))
		fmt.Printf("  %-15s %12s %15s %18s\n", "Codec", "Compressed", "Ratio", "Space Savings")
		fmt.Println("  " + strings.Repeat("-", 80))

		for _, codecType := range codecs {
			codec, err := compress.CreateCodec(codecType, "tag")
			if err != nil {
				t.Fatalf("Failed to get codec: %v", err)
			}

			compressed, err := codec.Compress(uncompressed)
			if err != nil {
				t.Fatalf("Failed to compress: %v", err)
			}

			ratio := float64(len(uncompressed)) / float64(len(compressed))
			savings := 100.0 * (1.0 - float64(len(compressed))/float64(len(uncompressed)))

			fmt.Printf("  %-15s %10d B %15.2fx %16.1f%%\n",
				codecType, len(compressed), ratio, savings)
		}
	}

	fmt.Println("\n" + strings.Repeat("=", 100))
}

// Helper functions to generate test data

func generateShortTags(count int) []string { //nolint:unparam
	tags := make([]string, count)
	prefixes := []string{"ok", "error", "warn", "info", "debug"}

	for i := range count {
		tags[i] = fmt.Sprintf("%s_%d", prefixes[i%len(prefixes)], i)
	}

	return tags
}

func generateMediumTags(count int) []string { //nolint:unparam
	tags := make([]string, count)

	for i := range count {
		tags[i] = fmt.Sprintf("service=api,region=us-west-%d,env=prod,version=v1.2.%d",
			i%5, i%10)
	}

	return tags
}

func generateLongTags(count int) []string { //nolint:unparam
	tags := make([]string, count)
	base := "host=server.example.com,datacenter=us-west-2a,cluster=prod-k8s,namespace=monitoring,pod=metrics-collector,container=exporter,app=prometheus,version=2.45.0,env=production"

	for i := range count {
		tags[i] = fmt.Sprintf("%s,instance=%d", base, i)
	}

	return tags
}

func generateMixedTags(count int) []string { //nolint:unparam
	tags := make([]string, count)

	for i := range count {
		switch i % 4 {
		case 0:
			tags[i] = "ok"
		case 1:
			tags[i] = "service=api,region=us-west"
		case 2:
			tags[i] = "host=server.example.com,datacenter=us-west-2a,env=production"
		case 3:
			tags[i] = string(make([]byte, 150)) // Long tag
			for j := range 150 {
				tags[i] = tags[i][:j] + "x" + tags[i][j+1:]
			}
		}
	}

	return tags
}

func generateRealisticMonitoringTags(count int) []string {
	tags := make([]string, count)

	services := []string{"api", "web", "worker", "cache", "db"}
	regions := []string{"us-west-1", "us-east-1", "eu-west-1"}
	envs := []string{"prod", "staging", "dev"}

	for i := range count {
		tags[i] = fmt.Sprintf("service=%s,region=%s,env=%s,host=host-%d",
			services[i%len(services)],
			regions[i%len(regions)],
			envs[i%len(envs)],
			i)
	}

	return tags
}
