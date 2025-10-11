package fbscompare

import (
	"fmt"
	"testing"

	"github.com/arloliu/mebo/format"
)

// TestBlobSizes measures actual blob sizes for different encoding/compression combinations
// Using three-part naming: timestamp_encoding-timestamp_compression-value_compression
// This isolates the contribution of each dimension to overall compression
func TestBlobSizes(t *testing.T) {
	numMetrics := 200
	pointSizes := []int{10, 100, 250}

	// Use full encoding set (all 64 combinations) for comprehensive size analysis
	configs := meboEncodingsFull

	t.Log("\n╔═══════════════════════════════════════════════════════════════════════════════════════════════╗")
	t.Log("║                    Mebo Encoding Size Comparison (Isolated Factors)                          ║")
	t.Log("║  Format: timestamp_encoding-timestamp_compression-value_encoding-value_compression           ║")
	t.Log("╚═══════════════════════════════════════════════════════════════════════════════════════════════╝")

	for _, numPoints := range pointSizes {
		cfg := DefaultTestDataConfig(numMetrics, numPoints)
		testData := GenerateTestData(cfg)
		totalPoints := numMetrics * numPoints

		t.Logf("\n━━━ 200 Metrics × %d Points = %d Total Points ━━━", numPoints, totalPoints)
		t.Log("")
		t.Log("┌────────────────────┬─────────────┬──────────────┬─────────────┬─────────────┐")
		t.Log("│ Config             │ Total Bytes │ Bytes/Point  │ vs Baseline │ Savings %   │")
		t.Log("├────────────────────┼─────────────┼──────────────┼─────────────┼─────────────┤")

		// First pass: collect all data
		var baselineSize int
		results := make([]struct {
			name       string
			size       int
			bytesPerPt float64
			vsBaseline string
			savingsPct string
		}, len(configs))

		for i, config := range configs {
			blob, err := createMeboBlob(testData, config.tsEncoding, config.tsCompress, config.valEncoding, config.valCompress)
			if err != nil {
				t.Errorf("%s: encoding failed: %v", config.name, err)
				continue
			}

			size := len(blob)
			bytesPerPoint := float64(size) / float64(totalPoints)

			if config.name == "mebo/raw-none-raw-none" {
				baselineSize = size
			}

			results[i].name = config.name
			results[i].size = size
			results[i].bytesPerPt = bytesPerPoint
		}

		// Second pass: calculate comparisons and format output
		for i := range results {
			if baselineSize > 0 {
				diff := results[i].size - baselineSize
				diffPct := (float64(diff) / float64(baselineSize)) * 100
				savingsPct := -diffPct

				if diff == 0 {
					results[i].vsBaseline = "baseline"
					results[i].savingsPct = "0.0%"
				} else if diff > 0 {
					results[i].vsBaseline = "+"
					results[i].savingsPct = "—"
				} else {
					results[i].vsBaseline = "-"
					results[i].savingsPct = fmt.Sprintf("%.1f%%", savingsPct)
				}
			}

			t.Logf("│ %-18s │ %11s │ %12.2f │ %-11s │ %11s │",
				results[i].name,
				fmt.Sprintf("%d", results[i].size),
				results[i].bytesPerPt,
				results[i].vsBaseline,
				results[i].savingsPct)
		}

		t.Log("└────────────────────┴─────────────┴──────────────┴─────────────┴─────────────┘")
	}

	t.Log("")
	t.Log("Legend:")
	t.Log("  Format: timestamp_encoding-timestamp_compression-value_compression")
	t.Log("")
	t.Log("  Timestamp Encoding:")
	t.Log("    raw:   8 bytes per timestamp (fixed size)")
	t.Log("    delta: Delta-of-delta encoding (variable size)")
	t.Log("")
	t.Log("  Compression:")
	t.Log("    none: No compression")
	t.Log("    zstd: Zstandard compression")
	t.Log("    s2:   S2 (Snappy) compression")
	t.Log("    lz4:  LZ4 compression")
	t.Log("")
	t.Log("  Key Test Cases:")
	t.Log("    raw-none-none:   Baseline (no encoding, no compression)")
	t.Log("    delta-none-none: Pure delta-of-delta effect")
	t.Log("    delta-none-zstd: Delta encoding + value compression only")
	t.Log("    delta-zstd-none: Delta encoding + timestamp compression only")
	t.Log("    delta-zstd-zstd: Both payloads compressed")
}

// TestTextBlobSizes measures actual text blob sizes for different encoding/compression combinations
func TestTextBlobSizes(t *testing.T) {
	numMetrics := 200
	numPoints := 10
	cfg := DefaultTestDataConfig(numMetrics, numPoints)
	testData := GenerateTextTestData(cfg)

	configs := []struct {
		name        string
		tsEncoding  format.EncodingType
		compression format.CompressionType
	}{
		{"raw-none", format.TypeRaw, format.CompressionNone},
		{"raw-zstd", format.TypeRaw, format.CompressionZstd},
		{"raw-s2", format.TypeRaw, format.CompressionS2},
		{"raw-lz4", format.TypeRaw, format.CompressionLZ4},
		{"delta-none", format.TypeDelta, format.CompressionNone},
		{"delta-zstd", format.TypeDelta, format.CompressionZstd},
		{"delta-s2", format.TypeDelta, format.CompressionS2},
		{"delta-lz4", format.TypeDelta, format.CompressionLZ4},
	}

	t.Logf("Testing with %d metrics × %d points = %d total text values\n", numMetrics, numPoints, numMetrics*numPoints)

	for _, config := range configs {
		blob, err := createMeboTextBlobBench(testData, config.tsEncoding, config.compression)
		if err != nil {
			t.Errorf("%s: encoding failed: %v", config.name, err)
			continue
		}

		bytesPerPoint := float64(len(blob)) / float64(numMetrics*numPoints)
		t.Logf("%-12s: %7d bytes (%.2f bytes/point)", config.name, len(blob), bytesPerPoint)
	}
}
