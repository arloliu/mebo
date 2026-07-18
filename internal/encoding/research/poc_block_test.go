package research

// PoC: Family-3 block codecs over mebo's small columnar blocks.
//
// Questions:
//  1. How badly does per-block zstd suffer on KB-scale blocks vs concatenated?
//  2. How much does a TRAINED zstd dictionary recover? (klauspost zstd.BuildDict)
//  3. Does FSE/huff0 entropy coding gain anything on already-XOR'd Gorilla/Chimp
//     output (expected: ~nothing, output is high-entropy) vs a skewed sidecar?
//
// Run: go test ./internal/encoding/research -run 'POCBlock' -v

import (
	"bytes"
	"math"
	"testing"

	"github.com/klauspost/compress/fse"
	"github.com/klauspost/compress/huff0"
	"github.com/klauspost/compress/zstd"

	"github.com/arloliu/mebo/internal/encoding/value/gorilla"
)

// buildBlocks encodes many realistic value columns into independent Gorilla blocks.
func buildBlocks(nBlocks, perBlock int) [][]byte {
	blocks := make([][]byte, 0, nBlocks)
	for b := range nBlocks {
		base := 20.0 + float64(b%50)*3.5
		vals := quantize(generateSmallJitterMetric(perBlock, base, 0.003, 0.001), 2)
		enc := gorilla.NewNumericGorillaEncoder()
		enc.WriteSlice(vals)
		blk := append([]byte(nil), enc.Bytes()...)
		enc.Finish()
		blocks = append(blocks, blk)
	}

	return blocks
}

func zstdEnc(t testing.TB, opts ...zstd.EOption) *zstd.Encoder {
	e, err := zstd.NewWriter(nil, opts...)
	if err != nil {
		t.Fatal(err)
	}

	return e
}

func generateSmallJitterMetric(count int, base, driftRate, noiseRate float64) []float64 {
	values := make([]float64, count)
	for i := range values {
		drift := math.Sin(float64(i)*0.07) * base * driftRate
		noise := math.Cos(float64(i)*0.23) * base * noiseRate
		values[i] = base + drift + noise
	}

	return values
}

func quantize(values []float64, decimals int) []float64 {
	result := make([]float64, len(values))
	scale := math.Pow10(decimals)
	for i, value := range values {
		result[i] = math.Round(value*scale) / scale
	}

	return result
}

func TestPOCBlockCodec(t *testing.T) {
	const nBlocks, perBlock = 200, 200
	blocks := buildBlocks(nBlocks, perBlock)

	var rawTotal int
	for _, b := range blocks {
		rawTotal += len(b)
	}
	avgRaw := float64(rawTotal) / float64(nBlocks)
	t.Logf("blocks=%d, avg raw Gorilla block = %.1f B (%.3f B/value)", nBlocks, avgRaw, avgRaw/perBlock)

	// (1) per-block zstd, no dict
	encNoDict := zstdEnc(t)
	defer encNoDict.Close()
	var zNoDict int
	for _, b := range blocks {
		zNoDict += len(encNoDict.EncodeAll(b, nil))
	}

	// (2) dictionary from the training half, applied per-block to the holdout half.
	// Prefer a trained dict (cover algorithm); fall back to a raw content dictionary
	// (concatenated samples) when training yields nothing on high-entropy blocks.
	trainSet := blocks[:nBlocks/2]
	var dict []byte
	var dictKind string
	if d, err := zstd.BuildDict(zstd.BuildDictOptions{ID: 1, Contents: trainSet, Level: zstd.SpeedBestCompression}); err == nil && len(d) >= 8 {
		dict = d
		dictKind = "trained"
	}
	var encDict *zstd.Encoder
	if dict != nil {
		encDict = zstdEnc(t, zstd.WithEncoderDict(dict))
	} else {
		// raw content dictionary: concatenate up to 32 representative training blocks
		var raw bytes.Buffer
		for i := 0; i < len(trainSet) && i < 32; i++ {
			raw.Write(trainSet[i])
		}
		dict = raw.Bytes()
		dictKind = "raw-content"
		encDict = zstdEnc(t, zstd.WithEncoderDictRaw(1, dict))
	}
	defer encDict.Close()
	var zDict int
	// measure only on the held-out half (fair: not used for training)
	holdout := blocks[nBlocks/2:]
	for _, b := range holdout {
		zDict += len(encDict.EncodeAll(b, nil))
	}

	// no-dict baseline on the same holdout for apples-to-apples
	var zNoDictHoldout int
	for _, b := range holdout {
		zNoDictHoldout += len(encNoDict.EncodeAll(b, nil))
	}

	// (3) concatenated zstd (upper bound of perfect cross-block sharing)
	var cat bytes.Buffer
	for _, b := range blocks {
		cat.Write(b)
	}
	zCat := len(encNoDict.EncodeAll(cat.Bytes(), nil))

	// (4) FSE / huff0 on a single concatenated block buffer (entropy-only)
	var fseSz, huffSz int
	in := cat.Bytes()
	var sc fse.Scratch
	if out, err := fse.Compress(in, &sc); err == nil {
		fseSz = len(out)
	} else {
		fseSz = len(in) // incompressible / not enough gain
	}
	var hsc huff0.Scratch
	if out, _, err := huff0.Compress1X(in, &hsc); err == nil {
		huffSz = len(out)
	} else {
		huffSz = len(in)
	}

	t.Logf("%s", "---- whole-set ratios (lower B/value better) ----")
	t.Logf("raw Gorilla                : %.3f B/value", avgRaw/perBlock)
	t.Logf("per-block zstd (no dict)   : %.3f B/value  (%.1fx vs raw)",
		float64(zNoDict)/float64(nBlocks)/perBlock, float64(rawTotal)/float64(zNoDict))
	t.Logf("concatenated zstd (all)    : %.3f B/value  (%.1fx vs raw)  [cross-block upper bound]",
		float64(zCat)/float64(nBlocks)/perBlock, float64(rawTotal)/float64(zCat))
	t.Logf("FSE  on concatenated bytes : %.3f B/value  (%.1fx vs raw)",
		float64(fseSz)/float64(nBlocks)/perBlock, float64(len(in))/float64(fseSz))
	t.Logf("huff0 on concatenated bytes: %.3f B/value  (%.1fx vs raw)",
		float64(huffSz)/float64(nBlocks)/perBlock, float64(len(in))/float64(huffSz))

	t.Logf("%s", "---- held-out half: trained-dict vs no-dict (the small-block fix) ----")
	t.Logf("per-block zstd  no-dict    : %.3f B/value", float64(zNoDictHoldout)/float64(len(holdout))/perBlock)
	t.Logf("per-block zstd  trained-dict: %.3f B/value  (%.1f%% smaller than no-dict)",
		float64(zDict)/float64(len(holdout))/perBlock,
		100*(1-float64(zDict)/float64(zNoDictHoldout)))
	t.Logf("dict kind = %s, dict size = %d B (amortized across all blocks using it)", dictKind, len(dict))
}
