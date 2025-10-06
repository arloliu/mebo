# Encoding Configuration Reference

## Overview

The benchmark suite uses a 3-dimensional encoding configuration system that independently controls:
1. **Timestamp Encoding** - How timestamps are encoded
2. **Timestamp Compression** - How timestamp payload is compressed
3. **Value Compression** - How value payload is compressed

## Configuration Format

```
{timestamp_encoding}-{timestamp_compression}-{value_compression}
```

**Examples:**
- `delta-zstd-zstd` - Delta-of-delta encoding with Zstd compression on both payloads
- `delta-none-zstd` - Delta-of-delta encoding with no timestamp compression but Zstd on values
- `raw-none-none` - Raw encoding with no compression (baseline)

## Encoding Dimensions

### 1. Timestamp Encoding
- **`raw`** - 8 bytes per timestamp (fixed size, int64 microseconds)
- **`delta`** - Delta-of-delta encoding (variable size, varint compressed)

### 2. Timestamp Compression
Applied to the timestamp payload after encoding:
- **`none`** - No compression
- **`zstd`** - Zstandard compression (best ratio, slower)
- **`s2`** - S2/Snappy compression (balanced)
- **`lz4`** - LZ4 compression (fastest, moderate ratio)

### 3. Value Compression
Applied to the value payload (always raw float64):
- **`none`** - No compression
- **`zstd`** - Zstandard compression (best ratio, slower)
- **`s2`** - S2/Snappy compression (balanced)
- **`lz4`** - LZ4 compression (fastest, moderate ratio)

## Encoding Sets

### Essential Set (10 configs)
Used by **performance benchmarks** for faster execution:

```go
meboEncodings = generateMeboEncodings(false)
```

**Configs:**
1. `mebo/raw-none-none` - Baseline
2. `mebo/delta-none-none` - Pure DoD effect
3. `mebo/raw-zstd-none` - Isolate timestamp compression on raw
4. `mebo/raw-none-zstd` - Isolate value compression
5. `mebo/delta-none-zstd` - DoD + value compression
6. `mebo/delta-zstd-none` - DoD + timestamp compression
7. `mebo/raw-zstd-zstd` - Raw with both compressed
8. `mebo/delta-zstd-zstd` - Best overall
9. `mebo/delta-s2-s2` - S2 comparison
10. `mebo/delta-lz4-lz4` - LZ4 comparison

### Full Set (32 configs)
Used by **size comparison tests** for comprehensive analysis:

```go
meboEncodingsFull = generateMeboEncodings(true)
```

**Complete Cartesian Product:**
- 2 timestamp encodings (`raw`, `delta`)
- × 4 timestamp compressions (`none`, `zstd`, `s2`, `lz4`)
- × 4 value compressions (`none`, `zstd`, `s2`, `lz4`)
- = **32 total combinations**

## Usage Examples

### In Benchmarks
```go
for _, enc := range meboEncodings {
    name := fmt.Sprintf("%s/%s", size.name, enc.name)
    b.Run(name, func(b *testing.B) {
        blob, _ := createMeboBlob(testData,
            enc.tsEncoding,  // raw or delta
            enc.tsCompress,  // timestamp compression
            enc.valCompress) // value compression
    })
}
```

### In Size Tests
```go
configs := meboEncodingsFull // Use full set for comprehensive analysis
for _, config := range configs {
    blob, _ := createMeboBlob(testData,
        config.tsEncoding,
        config.tsCompress,
        config.valCompress)

    size := len(blob)
    bytesPerPoint := float64(size) / float64(totalPoints)
}
```

## Key Insights from Analysis

### Compression Factor Contributions (250 points)

| Configuration | Bytes/Point | Savings | Description |
|--------------|-------------|---------|-------------|
| `raw-none-none` | 16.06 | 0.0% | Baseline |
| `delta-none-none` | 10.93 | 32.0% | **Pure DoD effect** |
| `raw-none-zstd` | 15.52 | 3.4% | Value compression only |
| `delta-none-zstd` | 10.38 | 35.4% | DoD + value compression |
| `delta-zstd-none` | 10.59 | 34.1% | DoD + timestamp compression |
| `delta-zstd-zstd` | 10.04 | 37.5% | **Best overall** |

### Contribution Breakdown (37.5% total savings)
- **Delta-of-Delta**: 32.0% (85% of total benefit) 🏆
- **Value compression**: 3.4% (9% of total benefit)
- **Timestamp compression**: 2.1% (6% of total benefit)

## Recommendations

### Production Configurations

**1. Maximum Space Efficiency**
```
delta-zstd-zstd  (37.5% savings, slower)
```
Best for: Storage-constrained systems, archival data

**2. Balanced Performance**
```
delta-none-none  (32.0% savings, fast)
```
Best for: Real-time systems, high-throughput scenarios, CPU-constrained

**3. CPU vs Space Tradeoff**
```
delta-none-zstd  (35.4% savings, moderate)
```
Best for: When you need better compression but timestamps are hot path

### Why These Configs?

- **Always use delta-of-delta** - Provides 85% of total benefit with minimal CPU cost
- **Value compression is optional** - Only adds 3.4% savings, may not be worth CPU cost
- **Timestamp compression is marginal** - DoD already compresses timestamps efficiently
- **Skip S2/LZ4** - Minimal benefit over delta-none (~0-1% improvement)

## Implementation Details

The encoding generation is fully programmatic:

```go
func generateMeboEncodings(includeFull bool) []EncodingConfig {
    // Generate all 32 combinations
    for _, tsEnc := range []string{"raw", "delta"} {
        for _, tsComp := range []string{"none", "zstd", "s2", "lz4"} {
            for _, valComp := range []string{"none", "zstd", "s2", "lz4"} {
                // Create config...
            }
        }
    }

    if includeFull {
        return all // All 32 configs
    }
    return filterEssentialEncodings(all) // Curated 10 configs
}
```

### Benefits
- ✅ Single source of truth
- ✅ Easy to add new encodings/compressions
- ✅ Maintainable and consistent
- ✅ No manual listing errors
- ✅ Flexible filtering for different use cases

## Adding New Encodings

To add a new compression algorithm:

1. Add to `compressions` array in `generateMeboEncodings()`
2. No other changes needed - programmatic generation handles the rest
3. New configs automatically appear in both sets
4. Update `filterEssentialEncodings()` if needed

Example:
```go
compressions := []struct {
    name string
    typ  format.CompressionType
}{
    {"none", format.CompressionNone},
    {"zstd", format.CompressionZstd},
    {"s2", format.CompressionS2},
    {"lz4", format.CompressionLZ4},
    {"brotli", format.CompressionBrotli}, // New addition
}
```

This would automatically generate 40 total combinations (2 × 5 × 4).
