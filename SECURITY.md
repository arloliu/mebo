# Security Policy

## Supported Versions

We actively support the following versions with security updates:

| Version | Supported          | Status                     |
| ------- | ------------------ | -------------------------- |
| 1.x.x   | :white_check_mark: | Active development         |
| < 1.0   | :x:                | Pre-release, not supported |

### Version Support Policy

- **Current major version (1.x)**: Full security support
- **Previous major version**: Security fixes for 6 months after new major release
- **Pre-release versions**: No security support (upgrade to stable release)

## Security Considerations

### Data Safety

Mebo is a **data format library** that processes time-series data. Security considerations:

#### Memory Safety
- **Go's Memory Safety**: Protected by Go's memory management and bounds checking
- **Buffer Overflows**: Not possible due to Go's runtime safety guarantees
- **Integer Overflows**: We use safe arithmetic and check for overflow conditions in critical paths

#### Input Validation
- **Malformed Data**: Decoders validate input data structure before processing
- **Resource Exhaustion**: Decoders check for reasonable sizes to prevent memory exhaustion
- **Hash Collisions**: We use xxHash64 (64-bit) which has astronomically low collision probability

### Usage Scenarios

#### Safe Usage ✅
- Processing trusted data from your own systems
- Internal time-series data pipelines
- Data storage for monitoring systems
- Performance benchmarking and testing

#### Use with Caution ⚠️
- **Untrusted Data Sources**: If processing data from untrusted sources:
  - Implement size limits on input data
  - Use timeouts for decoding operations
  - Validate data origin and integrity (use checksums/signatures)
  - Consider sandboxing decoders

- **Public-Facing Services**: If exposing Mebo in public APIs:
  - Enforce rate limiting
  - Implement request size limits
  - Add input sanitization layers
  - Monitor resource usage

### Known Limitations

#### Not Provided by Mebo
- **Encryption**: Mebo does not encrypt data (use TLS/encryption at transport/storage layer)
- **Authentication**: No authentication mechanism (implement at application layer)
- **Digital Signatures**: No data signing (use external signing if needed)
- **Access Control**: No authorization (implement at application layer)

#### Resource Usage
- **Memory**: Decoding allocates memory proportional to output size
- **CPU**: Compression codecs can be CPU-intensive
- **Decompression Bombs**: Highly compressed malicious data could expand to large size
  - **Mitigation**: Implement max output size limits in your application

## Reporting a Vulnerability

### When to Report

Report security vulnerabilities if you discover:
- Memory safety issues (buffer overflows, use-after-free, etc.)
- Denial of service vulnerabilities
- Resource exhaustion attacks
- Integer overflow leading to incorrect behavior
- Panic conditions that crash the application
- Compression bomb vulnerabilities

### When NOT to Report

**These are not security issues:**
- Feature requests
- Performance issues (unless DoS-related)
- Compatibility problems
- Documentation issues
- General bugs that don't have security implications

### How to Report

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please report security vulnerabilities via:

#### GitHub Security Advisories (Preferred)
1. Go to: https://github.com/arloliu/mebo/security/advisories
2. Click "Report a vulnerability"
3. Fill out the form with:
   - Description of the vulnerability
   - Steps to reproduce
   - Affected versions
   - Potential impact
   - Suggested fix (if known)

#### Email (Alternative)
If you cannot use GitHub Security Advisories:
- Email: [security@arlolib.dev] (Replace with actual security email)
- Subject: `[SECURITY] Mebo Vulnerability Report`
- Use encrypted email if possible (PGP key: [link to PGP key])

### What to Include

Please provide:

1. **Description**: Clear explanation of the vulnerability
2. **Steps to Reproduce**:
   ```go
   // Example code demonstrating the issue
   encoder := NewEncoder()
   // ... steps that trigger vulnerability
   ```
3. **Impact**: What an attacker could do with this vulnerability
4. **Affected Versions**: Which versions are affected
5. **Proof of Concept**: Minimal code to demonstrate the issue
6. **Suggested Fix**: If you have ideas for fixing it

### Example Report

```
Vulnerability: Decoder Panic on Malformed Input

Description:
The NumericDecoder panics when processing specially crafted input
with invalid header flags, causing service crashes.

Steps to Reproduce:
1. Create malformed blob with invalid flag combination (0xFF)
2. Call decoder.Decode(malformedData)
3. Application crashes with panic

Impact:
- Denial of service for applications processing untrusted data
- Service crashes requiring restart

Affected Versions:
- v1.0.0 through v1.2.3

Proof of Concept:
[Attached: poc.go]

Suggested Fix:
Add flag validation in parseHeader() before processing.
```

## Response Process

### Timeline

| Phase            | Timeline     | Actions                          |
|------------------|--------------|----------------------------------|
| Acknowledgment   | 48 hours     | Confirm receipt of report        |
| Initial Analysis | 3-5 days     | Assess severity and impact       |
| Fix Development  | 1-2 weeks    | Develop and test fix             |
| Security Release | ASAP         | Release patch with advisory      |
| Public Disclosure| 1-7 days after patch | Publish security advisory |

### Severity Levels

#### Critical (CVSSv3: 9.0-10.0)
- Remote code execution
- Arbitrary memory corruption
- **Response**: Fix within 48 hours, emergency release

#### High (CVSSv3: 7.0-8.9)
- Denial of service attacks
- Significant resource exhaustion
- **Response**: Fix within 1 week, expedited release

#### Medium (CVSSv3: 4.0-6.9)
- Local denial of service
- Minor information disclosure
- **Response**: Fix in next minor release (2-4 weeks)

#### Low (CVSSv3: 0.1-3.9)
- Edge case issues
- Theoretical vulnerabilities
- **Response**: Fix in next scheduled release

### What You Can Expect

✅ **We will:**
- Acknowledge your report within 48 hours
- Keep you informed of progress
- Credit you in the security advisory (unless you prefer anonymity)
- Coordinate disclosure timing with you
- Release a fix as quickly as possible

❌ **We will not:**
- Ignore your report
- Discourage responsible disclosure
- Take legal action against good-faith researchers

## Security Advisories

Published security advisories are available at:
- GitHub Security Advisories: https://github.com/arloliu/mebo/security/advisories
- CHANGELOG.md: Security fixes are documented in version history

## Security Best Practices

### For Mebo Users

If you're using Mebo in production:

#### 1. Keep Dependencies Updated
```bash
# Check for updates
go list -m -u github.com/arloliu/mebo

# Update to latest patch version
go get -u=patch github.com/arloliu/mebo

# Update to latest version (test thoroughly first)
go get -u github.com/arloliu/mebo
```

#### 2. Implement Size Limits
```go
const maxBlobSize = 10 * 1024 * 1024 // 10 MB

func decodeBlob(data []byte) error {
    if len(data) > maxBlobSize {
        return fmt.Errorf("blob too large: %d bytes", len(data))
    }

    decoder := blob.NewNumericDecoder()
    return decoder.Decode(data)
}
```

#### 3. Use Timeouts
```go
func decodeWithTimeout(data []byte, timeout time.Duration) error {
    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()

    done := make(chan error, 1)
    go func() {
        decoder := blob.NewNumericDecoder()
        done <- decoder.Decode(data)
    }()

    select {
    case err := <-done:
        return err
    case <-ctx.Done():
        return fmt.Errorf("decode timeout exceeded")
    }
}
```

#### 4. Monitor Resource Usage
- Set memory limits for processes using Mebo
- Monitor CPU usage during compression/decompression
- Log and alert on unusual resource consumption

#### 5. Validate Input Sources
```go
func processBlob(data []byte, trustedSource bool) error {
    if !trustedSource {
        // Extra validation for untrusted data
        if len(data) < 10 || len(data) > maxSize {
            return fmt.Errorf("invalid blob size")
        }

        // Verify checksum/signature if available
        if !verifyIntegrity(data) {
            return fmt.Errorf("integrity check failed")
        }
    }

    return decodeBlob(data)
}
```

## Dependency Security

### Monitoring Dependencies

Mebo depends on:
- **cespare/xxhash**: For hash computation
- **klauspost/compress**: For compression codecs (zstd, s2)
- **pierrec/lz4**: For LZ4 compression

We monitor these dependencies for security issues and update them promptly.

### Checking for Vulnerabilities

Use `govulncheck` to scan for known vulnerabilities:

```bash
# Install govulncheck
go install golang.org/x/vuln/cmd/govulncheck@latest

# Scan project
govulncheck ./...
```

We run `govulncheck` in CI and will address any discovered vulnerabilities promptly.

## Disclosure Policy

### Coordinated Disclosure

We follow **coordinated disclosure**:

1. **Private Reporting**: Researchers report vulnerabilities privately
2. **Fix Development**: We develop and test a fix (kept private)
3. **Coordinated Release**: We coordinate release timing with reporter
4. **Public Disclosure**: Advisory published after patch is available
5. **Credit**: Researcher credited (unless they prefer anonymity)

### Disclosure Timeline

- **Standard**: 90 days from report to public disclosure
- **Can be extended**: If fix is complex or affects many systems
- **Can be accelerated**: If vulnerability is actively exploited

### Safe Harbor

We support security research and will not pursue legal action against researchers who:
- Report vulnerabilities responsibly through proper channels
- Do not exploit vulnerabilities beyond what's needed for proof of concept
- Do not access or modify data beyond what's needed for research
- Give us reasonable time to fix issues before public disclosure

## Hall of Fame

We will recognize security researchers who responsibly disclose vulnerabilities:

<!-- This section will be updated with security researcher credits -->

Thank you for helping keep Mebo secure! 🔒

## Questions?

For security-related questions:
- **Vulnerabilities**: Use GitHub Security Advisories or security email
- **General security questions**: Open a GitHub Discussion
- **Implementation guidance**: See documentation or ask in Discussions

---

**Last Updated**: 2025-01-XX (Update with actual date at release)
