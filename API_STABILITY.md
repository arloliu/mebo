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
  - `Codec` interface
  - All codec implementations (`ZstdCodec`, `S2Codec`, `LZ4Codec`, `NoopCodec`)
  - Codec creation functions
  - Codec configuration types

- **`github.com/arloliu/mebo/encoding`**
  - All timestamp encoders/decoders (`RawTimestamp`, `DeltaTimestamp`)
  - All value encoders/decoders (`RawValue`, `GorillaValue`)
  - All tag encoders/decoders
  - All metric name encoders/decoders
  - Columnar encoder/decoder utilities

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

## Go Version Compatibility

### Minimum Go Version

- **v1.x**: Requires Go 1.23 or later

### Go Version Policy

- We support the **last 2 major Go releases** (e.g., 1.24 and 1.25)
- Minimum Go version may increase in **minor versions** (e.g., v1.1.0 might require Go 1.24)
- We test against latest stable Go versions in CI

### Version Support Matrix

| Mebo Version | Minimum Go | Tested Go Versions |
|--------------|------------|-------------------|
| v1.0.x       | 1.23       | 1.23, 1.24, 1.25  |
| v1.1.x       | 1.24 (TBD) | 1.24, 1.25, 1.26  |

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
