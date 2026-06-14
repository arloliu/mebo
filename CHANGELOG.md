# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `TypeALP = 0x6`: ALP (Adaptive Lossless floating-Point) value codec is now a first-class,
  user-selectable value encoding. Select it via `WithValueEncoding(format.TypeALP)` on the
  numeric encoder. ALP typically achieves 3–5× better compression than Chimp/Gorilla on
  low-decimal-precision gauge data (2–4 dp). **Forward-incompatible addition**: blobs written
  with `TypeALP` cannot be read by older mebo versions; blobs written with prior encodings are
  entirely unaffected.

## [1.7.0] - 2026-06-13

### Added
- `ForEach` callback iteration API on `NumericBlob` and `TextBlob` — zero-allocation alternative to `All()`
- `FinishInto` on encoders for buffer-reusing blob finalization (eliminates alloc on repeated encode cycles)

### Changed
- Rewrote Gorilla/Chimp bit-reader/writer with windowed reads and accumulator writes (~15% decode speedup)
- Eliminated iterator closure heap escapes in `All()` hot paths
- Eliminated payload buffer realloc churn in pool and blob encode paths

### Performance
- AVX-512 VBMI backend for packed timestamp decoding
- Fixed AVX-512 packed decoder tail guard (correctness fix under non-aligned lengths)

## [1.6.0] - 2026-04-11

### Added
- SIMD acceleration for `DeltaPacked` timestamps: AVX2 group-varint encode kernel and AVX-512 decode kernel
- `internal/arch` package for CPU/SIMD capability detection

### Changed
- Inlined varint serialization in `TimestampDeltaEncoder.WriteSlice`
- Inlined varint decode in `DecodeAll` and Chimp bulk decode paths

### Fixed
- AVX2 decode kernel: replaced AVX-512-only instructions that caused illegal instruction faults
- Security: capped decompression output size and guarded header offset casts against overflow

### Infrastructure
- CI matrix updated to test Go 1.25 and 1.26
- `GOEXPERIMENT=simd` gated on Go ≥ 1.26

## [1.5.0] - 2026-04-06

### Added
- **V2 blob format**: Chimp XOR encoding, `DeltaPacked` timestamp encoding, shared-timestamp section, sorted index
- `DecodeAll` batch decode method on all decoders
- Shared timestamp cache (`AllTimestamps`) for V2 blobs — single decode amortized across all metrics
- Adaptive index entries for V2 format
- Cross-version compatibility test harness
- `ErrEmptyBlobSet`, `ErrInvalidTimestampData`, `ErrDataSizeMismatch` sentinel errors

### Changed
- Fused multi-stream decoders to eliminate `iter.Pull` goroutine overhead
- `BlobSet.Materialize` now uses `ForEach` internally for correct tag alignment

### Fixed
- Integer overflow in `BlobSet` sort comparators
- Option precedence in `NewTaggedNumericEncoder` / `NewTaggedTextEncoder`
- Error propagation from `WithTextTimestampEncoding` and `WithTextDataCompression`
- Index offset validation in numeric and text decoders (guards against malformed blobs)
- Stale `TagOffset` deltas when dynamically disabling empty-tag encoding
- `TagAt` returns empty string (not `false`) for tagless blobs

## [1.4.3] - 2025-11-28

### Fixed
- `TimestampDeltaEncoder.Reset` did not reset internal state correctly

## [1.4.2] - 2025-11-25

### Changed
- Removed `gozstd` (cgo zstd) entirely; pure-Go zstd only

## [1.4.1] - 2025-11-25

### Changed
- Disabled cgo zstd build path in preparation for full removal

## [1.4.0] - 2025-11-03

### Added
- Helper methods on `BlobSet`: iteration, length, and accessor utilities

## [1.3.2] - 2025-10-21

### Added
- JSON stream parser for large metric datasets in `measure` tooling

## [1.3.1] - 2025-10-15

### Changed
- Optimized `TimestampDeltaEncoder` buffer estimation (fewer reallocations)
- Optimized `TimestampDeltaDecoder` inner iteration loop

## [1.3.0] - 2025-10-14

### Added
- Options API and `ChunkPPMs` metric to regression analysis package

## [1.2.0] - 2025-10-12

### Added
- `regression` package: re-encode-based compression regression analysis

## [1.1.1] - 2025-10-12

### Fixed
- `BlobSet` methods incorrectly handled tag support flags

## [1.1.0] - 2025-10-11

### Added
- Selective metric materialization for `BlobSet` (`MaterializeMetrics`)
- `AddFromRows` on encoders with encoder-level slice caching
- Typed slice pool (`internal/pool`) for efficient memory reuse

### Changed
- Optimized Gorilla decoder: batch unchanged-value detection
- Introduced varint decode fast path in timestamp delta decoder
- Timestamp delta encoder fast paths (reduced branch overhead)
- Optimized `NumericDecoder` performance
- `NumericBlob` struct field reordering for better CPU cache locality
- Removed redundant engine field from blob structs

### Fixed
- Empty tag payload handling when tags are disabled
- `TagAt` now correctly returns `true` with empty string for tagless blobs

## [1.0.0] - 2025-10-08

### Added - Core Features
- **Hash-based Metric Identification**: 64-bit xxHash64 for O(1) metric lookups
- **Columnar Storage**: Separate timestamp and value encoding for optimal compression
- **Multiple Encoding Strategies**:
  - Raw encoding for uncompressed data
  - Delta encoding for sequential data
  - Gorilla encoding for high compression ratios
- **Multiple Compression Codecs**:
  - Zstd (balanced compression and speed)
  - S2 (fast compression)
  - LZ4 (ultra-fast compression)
  - None (no compression)
- **Tag Support**: Optional metadata per data point
- **BlobSet Support**: Unified access across multiple blobs with global indexing
- **Type Support**:
  - NumericBlob for float64 metrics
  - TextBlob for string metrics

### Added - API & Packages
- `blob` package: Main encoding/decoding logic for NumericBlob and TextBlob
- `encoding` package: Timestamp and value encoding strategies
- `compress` package: Compression codec implementations
- `section` package: Internal format structures and headers
- `endian` package: Endian engine utilities
- Root package: Convenience wrappers and helper functions

### Added - Developer Experience
- **Comprehensive Examples**:
  - `blob_set_demo`: Multi-blob operations
  - `compress_demo`: Compression comparison
  - `options_demo`: Configuration patterns
- **Testing Infrastructure**:
  - Comprehensive test suite (9 packages)
  - Benchmark suite for performance validation
  - GitHub Actions CI/CD pipeline
- **Development Tools**:
  - Makefile with comprehensive targets
  - golangci-lint v2 integration
  - Automated linting and testing
- **Documentation**:
  - 723-line comprehensive README
  - Package-level godoc documentation
  - API examples with expected outputs

### Added - Performance Features
- **Zero-Allocation Iteration**: Decode and iterate without per-point allocations
- **Buffer Pooling**: Internal buffer reuse for reduced GC pressure
- **Immutable Blobs**: Thread-safe concurrent reads
- **Optimized Layouts**:
  - Empty tag optimization (saves 20-60 bytes per blob)
  - Grouped length bytes in TextBlob
  - Delta-of-delta timestamp compression

### Performance Characteristics
- **Encoding**: 25-50M operations/second
- **Decoding**: 40-100M operations/second
- **Space Efficiency**: 42% smaller than raw storage with Gorilla+Delta encoding
- **Memory Footprint**: Minimal allocations with buffer pooling

### Changed - Optimizations
- Optimized `Finish()` by removing unnecessary pooled buffer copy
- Consolidated test and benchmark cases for better maintainability
- Improved Makefile with comprehensive targets and better organization

### Changed - Refactoring
- BlobSet types now use value receivers for immutability
- Renamed `DecodeVarint` to `decodeVarint` (unexported)
- Renamed `MaterializedMetric` to `MaterializedNumericMetric` for clarity
- Changed endianness to non-exported data type for encapsulation
- Fixed buffer pool issue in ColumnarEncoder implementations

### Documentation
- Standardized godoc format across all packages
- Added comprehensive performance analysis
- Added metrics-to-points ratio analysis
- Enhanced README with design philosophy and use cases
- Added BlobSet introduction and examples
- Documented zero-allocation iteration feature

### Infrastructure
- GitHub Actions CI workflow with linting and testing
- golangci-lint v2.5.0 migration with comprehensive linting rules
- Makefile targets for test, lint, coverage, and benchmarks
- Automated version checking for linter consistency

### Dependencies
- `github.com/cespare/xxhash/v2` v2.3.0 - Hash function
- `github.com/klauspost/compress` v1.18.0 - Zstd and S2 compression
- `github.com/pierrec/lz4/v4` v4.1.22 - LZ4 compression
- `github.com/stretchr/testify` v1.10.0 - Testing utilities

### Design Philosophy
Mebo is designed for **batch processing of already-collected metrics**, not streaming ingestion:
1. Collect metrics in memory (from monitoring agents, APIs, etc.)
2. Pack metrics into blobs using Mebo encoders
3. Persist blobs to storage (databases, object stores, file systems)
4. Query blobs later by decoding on-demand

This design makes Mebo ideal for:
- Batch metric ingestion (10 seconds to 5 minutes intervals)
- Time-series databases with compressed storage
- Object storage (S3/GCS/Azure Blob)
- Metrics aggregation and ETL pipelines

### API Stability
This is the first stable release. The public API is now locked and will follow semantic versioning:
- **MAJOR**: Breaking changes (v2.0.0+)
- **MINOR**: New features, backward compatible (v1.x.0)
- **PATCH**: Bug fixes, backward compatible (v1.0.x)

The following packages have stable APIs:
- `github.com/arloliu/mebo` (root package)
- `github.com/arloliu/mebo/blob`
- `github.com/arloliu/mebo/compress`
- `github.com/arloliu/mebo/encoding`

Packages under `internal/` are not covered by stability guarantees.

### Known Limitations
- Metrics must declare data point count upfront (batch processing design)
- Maximum blob size depends on available memory
- No built-in persistence layer (bring your own storage)
- Limited to Go 1.23+ (requires latest language features)

### License
Apache License 2.0

[Unreleased]: https://github.com/arloliu/mebo/compare/v1.7.0...HEAD
[1.7.0]: https://github.com/arloliu/mebo/compare/v1.6.0...v1.7.0
[1.6.0]: https://github.com/arloliu/mebo/compare/v1.5.0...v1.6.0
[1.5.0]: https://github.com/arloliu/mebo/compare/v1.4.3...v1.5.0
[1.4.3]: https://github.com/arloliu/mebo/compare/v1.4.2...v1.4.3
[1.4.2]: https://github.com/arloliu/mebo/compare/v1.4.1...v1.4.2
[1.4.1]: https://github.com/arloliu/mebo/compare/v1.4.0...v1.4.1
[1.4.0]: https://github.com/arloliu/mebo/compare/v1.3.2...v1.4.0
[1.3.2]: https://github.com/arloliu/mebo/compare/v1.3.1...v1.3.2
[1.3.1]: https://github.com/arloliu/mebo/compare/v1.3.0...v1.3.1
[1.3.0]: https://github.com/arloliu/mebo/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/arloliu/mebo/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/arloliu/mebo/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/arloliu/mebo/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/arloliu/mebo/releases/tag/v1.0.0
