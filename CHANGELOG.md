# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/arloliu/mebo/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/arloliu/mebo/releases/tag/v1.0.0
