# API Stability Guarantee

This document outlines Mebo's commitment to API stability and backward compatibility.

## Semantic Versioning

Mebo follows [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html):

```
MAJOR.MINOR.PATCH (e.g., 1.2.3)
```

- **MAJOR** version: Breaking changes (incompatible API changes)
- **MINOR** version: New features (backward compatible additions)
- **PATCH** version: Bug fixes (backward compatible corrections)

### Version Increment Examples

**MAJOR (1.x.x → 2.0.0)**
- Removing or renaming exported functions, types, or methods
- Changing function signatures
- Changing behavior in breaking ways
- Removing support for older Go versions

**MINOR (1.0.x → 1.1.0)**
- Adding new exported functions, types, or methods
- Adding new features while maintaining backward compatibility
- Adding new optional parameters (via variadic functions or option patterns)
- Deprecating features (without removal)

**PATCH (1.0.0 → 1.0.1)**
- Bug fixes that don't change API
- Performance improvements
- Documentation updates
- Internal refactoring

## Stability Levels

### Stable APIs (v1.x+)

These packages are **stable** and will not break compatibility within the same major version:

#### Core Packages
- **`github.com/arloliu/mebo`** (root package)
  - All exported convenience functions
  - All exported helper functions
  - Type aliases and constants

- **`github.com/arloliu/mebo/blob`**
  - `NumericBlob`, `NumericBlobSet`
  - `TextBlob`, `TextBlobSet`
  - All `NumericEncoder`, `TextEncoder` types and methods
  - All `NumericDecoder`, `TextDecoder` types and methods
  - Configuration types and option functions
  - Materialization types and methods

- **`github.com/arloliu/mebo/compress`**
  - `Codec`, `Compressor`, and `Decompressor` interfaces
  - All codec implementations (`ZstdCompressor`, `S2Compressor`, `LZ4Compressor`, `NoOpCompressor`)
  - Their `New*Compressor` constructors
  - Codec lookup functions (`CreateCodec`, `GetCodec`)
  - `CompressionStats`

- **`github.com/arloliu/mebo/encoding`**
  - `ColumnarEncoder` and `ColumnarDecoder` interfaces (the only exports;
    the concrete timestamp, value, tag, and metric-name codecs live under `internal/` and are not part of the public API)

#### Supporting Packages
- **`github.com/arloliu/mebo/endian`**
  - `EndianEngine` interface
  - Endian engine implementations

- **`github.com/arloliu/mebo/section`**
  - All header types (`NumericHeader`, `TextHeader`)
  - All index entry types
  - Flag and constant definitions

- **`github.com/arloliu/mebo/errs`**
  - All exported error variables
  - Error creation functions

### Internal APIs (No Stability Guarantee)

Packages under `internal/` are **implementation details** and may change at any time:

- `github.com/arloliu/mebo/internal/collision`
- `github.com/arloliu/mebo/internal/hash`
- `github.com/arloliu/mebo/internal/options`
- `github.com/arloliu/mebo/internal/pool`

**Never import internal packages directly.** They are not covered by API stability guarantees.

### Experimental Features

Features marked as "experimental" in documentation:
- May change in minor versions
- Will be clearly labeled in godoc
- Will have migration path when stabilized
- Currently: None (all features are stable in v1.0.0)

## Deprecation Policy

### Deprecation Process

When a feature needs to be removed or changed incompatibly:

1. **Mark as Deprecated** (Minor Version)
   - Add `// Deprecated:` godoc comment
   - Document the recommended alternative
   - Keep functionality working

2. **Maintain for 2+ Minor Versions**
   - Keep deprecated features functional for at least 2 minor releases
   - Example: Deprecated in v1.1.0 → Removed earliest in v2.0.0

3. **Remove in Major Version**
   - Only remove in major version bump
   - Document in CHANGELOG with migration guide
   - Provide clear upgrade path

### Deprecation Example

```go
// Deprecated: Use NewNumericEncoder instead.
// This function will be removed in v2.0.0.
func LegacyEncoder() *Encoder {
    // Still works, calls new implementation
    return NewNumericEncoder(time.Now())
}
```

## Backward Compatibility Promises

### What We Promise

✅ **Source Compatibility**
- Code that compiles with v1.0.0 will compile with v1.x.x
- Function signatures won't change
- Types won't be removed or renamed

✅ **Behavior Compatibility**
- Existing functionality will continue to work
- Bug fixes won't break correct usage
- Performance improvements won't change semantics

✅ **Data Format Compatibility**
- Blobs created with v1.0.0 can be read by v1.x.x
- Encoding format is stable within major version
- Compression formats are stable

### What We Don't Promise

❌ **Internal Implementation**
- Internal package APIs may change
- Algorithm optimizations may occur
- Memory layout may change (but behavior won't)

❌ **Build-Time Dependencies**
- Dependency versions may be updated (following semver)
- Build tools may change
- Go version requirements may increase in minor versions

❌ **Performance Characteristics**
- Exact performance numbers may vary
- Memory usage may change
- CPU usage may change
(But we'll maintain competitive performance)

❌ **Undocumented Behavior**
- If it's not in godoc, it's not guaranteed
- Don't rely on implementation details
- Test against public APIs only

## Standardised Behaviour (v1.10.0)

v1.10.0 defines behaviour on inputs that were previously undefined or inconsistent across
surfaces. These are documented standardisations of undefined behaviour, not breaking changes to
any documented contract:

- **Collided metric ID (two distinct names hashing to one 64-bit ID).** Every ID-keyed surface
  (`GetByID`, `Len`, `MaterializeMetric(id)`, materialized `*At`) now deterministically resolves
  a collided ID to the **first entry in index order**. Name-keyed surfaces resolve each name to
  its own entry.
- **`MetricCount` / `MetricIDs()`** count/enumerate **one per index entry** (a collided ID
  appears twice), consistently across raw and materialized blobs. `MetricNames()` is index-order
  deterministic.
- **Blob-set identity is the metric name, not the ID** (when the set carries names; names-free
  sets fall back to ID identity). A set merges the same metric across time windows, so identity
  must survive that merge. Therefore, on a **set**, `MetricCount`/`MetricIDs`/`MetricNames`
  report one entry per **logical identity** — deliberately different from the per-index-entry
  counting used on a single **blob**. Two distinct names colliding on one ID are two set metrics;
  the same name across members is one. Every ID-keyed set surface resolves a collided ID to the
  **first colliding name in canonical order** (members by `StartTime`, caller slice order
  breaking ties) and returns that metric merged across windows; a stripped member's data attaches
  to that first colliding name only. This also repairs a real defect: materialized sets
  previously unioned members by ID and concatenated two colliding metrics into one interleaved
  series.
- **Unsorted V2/V2Ext input** is rejected at decode with `errs.ErrUnsortedIndex`. mebo's own
  encoder always emits sorted V2, so no output of any mebo version is affected; only foreign or
  crafted blobs with descending index IDs are rejected (they previously produced silent
  `GetByID` misses).
- **Duplicate metric name in a blob** is rejected at decode with `errs.ErrDuplicateMetricName`.
- **Collided input to `regression.Analyze`/`AnalyzeWithOptions`** is rejected with
  `errs.ErrCollisionNotSupported` rather than silently collapsed.

## Additive Symbols (v1.10.0)

These new symbols are purely additive; no existing signature changed.

- **`blob.NewNumericDecoderBorrowed(data []byte) (*NumericDecoder, error)`** and
  **`blob.NewTextDecoderBorrowed(data []byte) (*TextDecoder, error)`** — zero-copy metric-name
  decode. The decoded blob's metric names alias `data` instead of owning independent copies,
  removing the per-name allocations that dominate names-bearing decode cost. **Lifetime rule:**
  the backing array of `data` must not be mutated or reused while the decoded blob (or anything
  derived directly from its names) is live. Materialising such a blob **clones** its names, so
  materialized objects are always owning and the borrowed-lifetime rule never propagates past the
  blob. The existing `NewNumericDecoder` / `NewTextDecoder` constructors are unchanged and keep
  copying names; their function signatures are pinned by compile-time assertions so they stay
  storable in typed function variables.
- **`blob.WithMetricNames()`** (numeric encoder) and **`blob.WithoutMetricNames()`** (text
  encoder) — opt-in / opt-out of the metric-names payload independent of collision detection.
- **`blob.StripMetricNames(dst, src []byte) ([]byte, bool, error)`** and
  **`blob.StripMetricNamesInPlace(buf []byte) ([]byte, bool, error)`** — remove the metric-names
  payload from an encoded blob without a full decode/re-encode. Stripping drops enumeration and
  exact negative membership (a hash-colliding absent name may false-positive afterwards); see the
  doc comments.
- **`NumericBlobSet.MetricCount/MetricIDs/MetricNames/HasMetricID`** and the same four on
  **`TextBlobSet`**, plus **`MaterializedTextBlobSet.HasMetricName/DataPointCountByName`** —
  enumerate a set's **logical** metrics (see Standardised Behaviour above for the identity rule).
- **`blob.MaxMetricNamesCount`** (65535) — metric/names-count ceiling when a names payload must
  be written; tighter than the existing `blob.MaxMetricCount` (65536) because the on-wire count
  is a `uint16`.
- **`blob.MaxMetricNameLength`** (65535) — maximum byte length of one metric name, validated in
  `StartMetricName`'s preflight on both encoders so an over-long name is rejected before any
  state mutation rather than at `Finish`.

## Behaviour Changes (v1.11.0)

v1.11.0 adds no exported symbols and changes no signatures.
Every change below makes the implementation match its documentation.
Blobs written by earlier versions stay readable;
decoded results change only where earlier versions misread data.
Callers that relied on the previous behaviour should apply the listed migration.

- **Encoder defaults.**
  `blob.NewNumericEncoder` without options now writes Delta timestamps and Gorilla values with no value compression,
  and `blob.NewTextEncoder` writes Delta timestamps, as the option docs always stated.
  Default output bytes therefore differ from v1.10.0,
  and `ValueAt`/`TimestampAt` on default-encoded blobs cost O(index) instead of O(1).
  Migration: to keep the previous output and O(1) random access, pass
  `WithTimestampEncoding(format.TypeRaw)`, `WithValueEncoding(format.TypeRaw)` and
  `WithValueCompression(format.CompressionZstd)` to the numeric encoder,
  and `WithTextTimestampEncoding(format.TypeRaw)` to the text encoder.
- **`TimestampEncoding()`** on blobs returns the encoding stored in the header,
  including `format.TypeDeltaPacked`, instead of reporting DeltaPacked as `format.TypeDelta`.
- **`NumericEncoder.MaxDataPoints()`** reports lower limits, computed from true worst-case encoded sizes.
- **`DecodeBlobSet`** returns `errs.ErrInvalidMagicNumber` for an input that is neither a numeric nor a text blob,
  as its godoc states, instead of silently skipping it.
- **Set-level `AllTags`** (`NumericBlobSet`, `TextBlobSet`, `BlobSet` and their `ByName` forms)
  yields one empty tag per point for a tagless member when another member carries tags,
  so tag indexes align with `TagAt` and `Materialize`.
- **`BlobSet` numeric precedence.**
  `TimestampAt`, `TagAt`, `MetricDuration`, `MetricLen` and their `ByName` forms
  serve a metric found in numeric members from those members only, like `AllTimestamps` and `AllTags`.
- **Stricter decoding.**
  Blobs whose structure contradicts itself (counts beyond their payloads, overlapping sections,
  malformed ALP columns, overlong varints, text headers naming numeric-only encodings)
  now fail `Decode` with a wrapped `errs` sentinel instead of panicking or returning wrong data.
  mebo's own encoders never produce such blobs.

## Additions and Behaviour Changes (v1.12.0)

These additions are purely additive; no existing signature changed.

- **`format.TypeALPRLE`** (`EncodingType` 0x7): the ALP-RLE value encoding.
  `EncodingType(7).String()` now returns `"ALPRLE"` (it returned `"Unknown"` before),
  and numeric blob headers accept 0x7 as a value encoding.
  Readers up to v1.11.0 reject blobs that use it, so upgrade every consumer before any producer selects it
  ([Best Practices](docs/best_practices.md#alp-rle-upgrade-consumers-before-producers)).

Behaviour changes:

- **ALP output on fusing builds.**
  On arm64, ppc64x, s390x, riscv64, loong64, and amd64 with `GOAMD64=v3` or later,
  v1.9.0–v1.11.0 could encode rare values with a different exponent or digit than other builds,
  because the compiler fused a multiply and an add.
  v1.12.0 writes the same bytes on every build, matching `GOAMD64=v1` output, which is unchanged.
  Decoding is unchanged, so every existing blob reads back to the same values;
  only callers that compare encoded bytes (content hashes, deduplication) can see a difference, for those rare values.
- **`TimestampAt` on shared timestamps** is O(1) for every timestamp encoding.
- **`ForEachValues` on ALP columns** no longer allocates per metric.

## Go Version Compatibility

### Minimum Go Version

- **v1.x**: Requires Go 1.25 or later (`go 1.25.0` in `go.mod`)

### Go Version Policy

- We support the **last 2 major Go releases**; CI currently tests 1.25 and 1.26
- Minimum Go version may increase in **minor versions** (e.g., v1.6.0 raised it from Go 1.24 to Go 1.25)
- We test against latest stable Go versions in CI

### Version Support Matrix

| Mebo Version  | Minimum Go | Tested Go Versions |
|---------------|------------|--------------------|
| v1.0.x–v1.5.x | 1.24       | 1.24, 1.25         |
| v1.6.0+       | 1.25       | 1.25, 1.26         |

## Breaking Change Process

If we must make breaking changes:

### Planning Phase
1. Announce intent on GitHub Discussions
2. Gather community feedback
3. Design migration path
4. Document rationale

### Implementation Phase
1. Create migration guide
2. Provide automated migration tools if possible
3. Update CHANGELOG with detailed migration instructions
4. Create v2.0.0-beta for early testing

### Release Phase
1. Release v2.0.0 with clear breaking changes documentation
2. Maintain v1.x with security fixes for 6 months
3. Help community migrate

## Reporting Compatibility Issues

If you find a compatibility issue:

1. **Check if it's documented** - Review CHANGELOG and release notes
2. **Verify your usage** - Ensure you're using documented APIs
3. **Open an issue** - Provide:
   - Mebo version that worked
   - Mebo version that broke
   - Minimal reproduction code
   - Expected vs actual behavior

We treat unintended breaking changes as **critical bugs** and will:
- Fix immediately in patch release
- Or document as intended breaking change with apology

## Commitment

We take API stability seriously because:
- **Production use**: Mebo is designed for production systems
- **Long-term maintenance**: Your code should work for years
- **Upgrade confidence**: Updates should be safe and easy

If we can't maintain a feature compatibly, we'll:
- Be transparent about why
- Give you plenty of warning
- Provide migration tools
- Keep old versions working

## Questions?

If you're unsure whether a change would break compatibility:
- Open a GitHub Discussion
- Ask before upgrading
- Check the CHANGELOG

**When in doubt, assume it's stable unless documented otherwise.**

## References

- [Semantic Versioning 2.0.0](https://semver.org/)
- [Go 1 and the Future of Go Programs](https://go.dev/doc/go1compat)
- [Keep a Changelog](https://keepachangelog.com/)
