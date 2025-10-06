# GitHub Copilot Instructions

This file provides context and guidelines for GitHub Copilot when working on the `mebo` project.

## Project Overview

Mebo is a high-performance, space-efficient binary format for storing time-series metric data. The design is optimized for scenarios with many metrics but relatively few data points per metric (e.g., 150 metrics × 10 points), providing excellent compression ratios and fast lookup performance.

**Key Features:**
- **Hash-Based Identification:** Metrics are identified by 64-bit xxHash64 hashes for fast lookups with zero collision risk
- **Columnar Storage:** Timestamps and values are stored separately for optimal compression and access patterns
- **Flexible Encoding:** Per-blob configurable encoding strategies for both timestamps and values
- **Memory Efficiency:** Fixed-size structures enable single-pass encoding and efficient binary search

**Project Details:**
- **Language:** Go >=1.24.0
- **Module:** github.com/arlolib/mebo
- **Project Structure:**
  ```
  .
  ├── .github/
  │   └── copilot-instructions.md
  ├── internal/                 # Private application and library code
  └── README.md
  ```

## Coding Standards & Conventions

### Go Style Guidelines
- Follow the [official Go style guide](https://golang.org/doc/effective_go.html)
- Use `goimports` for formatting and import management
- Use `golangci-lint` for comprehensive code quality checks
- Use `any` instead of `interface{}` for empty interfaces
- Use `slices` and `maps` packages from the standard library for common operations
- Use `sync` package for synchronization primitives
- Prefer atomic operations from `sync/atomic` for simple counters and flags
- Prefer `errors.Is` and `errors.As` for error handling
- Use `context` package for request-scoped values, cancellation, and timeouts
- Follow Go naming conventions:
  - Package names: lowercase, short, descriptive
  - Functions: CamelCase (exported) or camelCase (unexported)
  - Variables: camelCase
  - Constants: CamelCase for package-level constants
  - Receiver names: short and consistent (enforced by receiver-naming rule)

### File Content Order

**All Go source files MUST follow this declaration order:**

| Order | Code Element | Convention/Reason |
|-------|--------------|-------------------|
| 1 | **Package declaration** | `package name` |
| 2 | **Imports** | Grouped: standard library, external, internal |
| 3 | **Constants** (`const`) | Grouped together, exported first |
| 4 | **Variables** (`var`) | Grouped together, exported first |
| 5 | **Types** (`type`) | Structs, interfaces, custom types. Exported first |
| 6 | **Factory Functions** | `NewType() *Type` immediately after type declaration |
| 7 | **Exported Functions** | Public standalone functions (not methods) |
| 8 | **Unexported Functions** | Private helper functions (not methods) |
| 9 | **Exported Methods** | Methods on types `(t *Type) Method()`. Group by type |
| 10 | **Unexported Methods** | Private methods `(t *Type) helper()`. Group by type |

**Example Structure:**

```go
package blob

import (
    "fmt"          // standard library
    "time"

    "external/pkg" // external packages

    "github.com/arlolib/mebo/internal/hash" // internal packages
)

// Constants
const (
    MaxSize = 1024
    minSize = 64
)

// Variables
var (
    DefaultConfig = Config{...}
    cache = make(map[string]string)
)

// Exported Types
type Config struct {
    Size int
}

type Reader interface {
    Read() error
}

// Unexported Types
type internalState struct {
    data []byte
}

// Factory Functions (immediately after type)
func NewConfig() *Config {
    return &Config{}
}

// Exported Functions
func ProcessData(data []byte) error {
    return nil
}

// Unexported Functions
func validateData(data []byte) bool {
    return len(data) > 0
}

// Exported Methods (grouped by type)
func (c *Config) Validate() error {
    return c.validate()
}

func (c *Config) Size() int {
    return c.Size
}

// Unexported Methods (grouped by type)
func (c *Config) validate() error {
    return nil
}
```

**Key Rules:**
- ✅ Group related items together (all constants, all types, all methods for same receiver)
- ✅ Exported items come before unexported items within each category
- ✅ Factory functions (`NewX`) come immediately after the type they construct
- ✅ Methods are grouped by receiver type, not alphabetically
- ✅ Maintain logical grouping over strict alphabetical ordering

### Loop Patterns (forlooprange rule)
- Use `for i := range slice` when you need the index: `for i := range items { process(i, items[i]) }`
- Use `for range slice` when you don't need the index: `for range items { doSomething() }`
- Use `for b.Loop()` in benchmarks (Go 1.24+): `for b.Loop() { benchmarkedCode() }`
- Use `for range N` (Go 1.22+) for simple iteration: `for range 10 { repeat() }`
- **Key point:** If you're not using the index variable `i`, don't declare it

### Code Organization
- Keep functions small and focused (max 100 lines, prefer under 50)
- Function complexity should not exceed 22 (enforced by cyclop linter)
- Package average complexity should stay under 15.0
- Use meaningful variable and function names
- Group related functionality in the same package
- Separate concerns using interfaces
- Use dependency injection for better testability
- Avoid naked returns in functions longer than 40 lines
- Use struct field tags for marshaling/unmarshaling (enforced by musttag)

### File Organization: 3-File Maximum Rule

**Each type, struct, or logical component should have at most 3 Go source files:**

1. **Implementation file** - Contains the main logic, types, and methods
   - Example: `numeric_raw.go`, `ts_delta.go`, `blob.go`

2. **Test file** (`*_test.go`) - Contains unit tests for the implementation
   - Example: `numeric_raw_test.go`, `ts_delta_test.go`, `blob_test.go`

3. **Benchmark file** (`*_bench_test.go`) - Contains performance benchmarks (optional)
   - Example: `numeric_bench_test.go`, `ts_delta_bench_test.go`, `blob_bench_test.go`

**Key Principles:**
- ✅ Each encoder, decoder, or major component follows this 3-file pattern
- ✅ Tests belong with their implementation (no cross-cutting test files)
- ✅ Related code stays together by type/component
- ❌ Avoid creating additional files like `*_reuse_test.go`, `*_helper_test.go`, etc.
- ❌ No cross-cutting test files that test multiple unrelated types

**Benefits:**
- **Predictability:** Easy to find where code lives
- **Organization:** Related code stays together by component
- **Navigation:** Developers know exactly where to look
- **Maintainability:** Prevents file sprawl and confusion
- **Consistency:** Uniform structure across all packages

**Example Structure:**
```
encoding/
├── numeric_raw.go              # Implementation
├── numeric_raw_test.go         # Unit tests
├── numeric_bench_test.go       # Benchmarks
├── ts_delta.go                 # Implementation
├── ts_delta_test.go            # Unit tests
├── ts_delta_bench_test.go      # Benchmarks
└── tag.go                      # Implementation
    ├── tag_test.go             # Unit tests
    └── tag_bench_test.go       # Benchmarks
```

**Exceptions:**
- Utility files like `columnar.go`, `varstring.go` may exist for shared functionality
- Package-level constants/types file (e.g., `types.go`, `const.go`) is acceptable
- If a component doesn't need benchmarks, 2 files (impl + test) is fine

### Error Handling
- Always handle errors explicitly (enforced by errcheck)
- Check type assertions with comma ok idiom (check-type-assertions: true)
- Use the standard `error` interface
- Wrap errors with context using `fmt.Errorf` with %w verb for error wrapping
- Return errors as the last return value
- Use early returns to reduce nesting
- Prefix sentinel errors with "Err" and suffix error types with "Error" (errname linter)
- Handle specific error wrapping scenarios properly (errorlint)

Example:
```go
func processData(data []byte) (*Result, error) {
    if len(data) == 0 {
        return nil, fmt.Errorf("data cannot be empty")
    }

    result, err := parseData(data)
    if err != nil {
        return nil, fmt.Errorf("failed to parse data: %w", err)
    }

    // Type assertion with error checking
    if config, ok := result.Config.(MyConfig); ok {
        return &Result{Config: config}, nil
    }

    return nil, fmt.Errorf("invalid config type")
}
```

### Testing Guidelines
- Write unit tests for all public functions
- **Use table-driven tests ONLY when you have multiple test cases**
- **Avoid over-engineering:** Don't use table-driven structure for single test cases - write simple, direct tests instead
- Place tests in `_test.go` files in the same package
- Use the standard `testing` package
- Use `b.Loop()` which introduced in Go 1.24 for benchmarks
- Use `testify` for assertions and mocking
- Mock external dependencies for isolated tests
- Test edge cases and error scenarios
- Aim for high test coverage (>80%)
- Use meaningful test names that describe the scenario
- Use `t.Setenv()` instead of `os.Setenv()` in tests (tenv linter)
- Use `t.Parallel()` appropriately (tparallel linter)
- Ensure examples are testable with expected output (testableexamples linter)
- Test files are excluded from certain linters (bodyclose, dupl, gosec, noctx)

**Table-driven test (use when you have multiple test cases):**
```go
func TestFunctionName(t *testing.T) {
    tests := []struct {
        name     string
        input    InputType
        expected ExpectedType
        wantErr  bool
    }{
        {
            name:     "valid input",
            input:    validInput,
            expected: expectedOutput,
            wantErr:  false,
        },
        {
            name:     "invalid input",
            input:    invalidInput,
            expected: nil,
            wantErr:  true,
        },
        // more test cases...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := FunctionName(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("FunctionName() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if !reflect.DeepEqual(result, tt.expected) {
                t.Errorf("FunctionName() = %v, want %v", result, tt.expected)
            }
        })
    }
}
```

**Simple test (use for single test cases):**
```go
func TestGetLittleEndianEngine(t *testing.T) {
    engine := GetLittleEndianEngine()

    require.Equal(t, binary.LittleEndian, engine)
    require.Implements(t, (*EndianEngine)(nil), engine)

    // Test actual behavior
    var testValue uint16 = 0x0102
    bytes := make([]byte, 2)
    engine.PutUint16(bytes, testValue)
    require.Equal(t, byte(0x02), bytes[0]) // LSB first
    require.Equal(t, byte(0x01), bytes[1]) // MSB second
}
```

### Best Practices
- Prefer standard library when possible
- Pre-allocate slices when size is known
- Pre-allocate maps when size is known
- Try to pre-allocate as much as possible
- Use dependency injection for testability
- Document all exported items
- Validate all input data
- **Performance-focused coding:**
  - Prefer branchless code in critical/hot path functions
  - Write code aggressively for inlining potential (small, simple functions)
  - Avoid over-using interfaces when performance is critical (interface calls have overhead)
  - Avoid unnecessary pointer creation for small structs - pass by value unless you need pointer receivers

## Linting Rules & Quality Standards

This project uses a comprehensive `golangci-lint` configuration with strict rules:

### Code Quality Rules
- **Function length**: Maximum 100 lines (revive), prefer shorter functions
- **Cyclomatic complexity**: Maximum 22 per function (cyclop)
- **Package complexity**: Average should be under 15.0
- **Naked returns**: Allowed only in functions ≤40 lines (nakedret)
- **Context handling**: Always pass context as first parameter (context-as-argument)
- **Import shadowing**: Avoid shadowing package names (import-shadowing)

### Security & Safety
- **Type assertions**: Always use comma ok idiom: `val, ok := x.(Type)`
- **SQL operations**: Always close `sql.Rows` and `sql.Stmt` (sqlclosecheck, rowserrcheck)
- **HTTP responses**: Always close response bodies (bodyclose)
- **Nil checks**: Avoid returning nil error with invalid value (nilnil)
- **Unicode safety**: Check for dangerous unicode sequences (bidichk)

### Performance & Memory
- **Pre-allocation**: Consider pre-allocating slices when size is known (prealloc)
- **Unnecessary conversions**: Remove unnecessary type conversions (unconvert)
- **Wasted assignments**: Avoid assignments that are never used (wastedassign)
- **Duration arithmetic**: Be careful with duration multiplications (durationcheck)

### Code Style
- **Variable naming**: Follow Go conventions, avoid stuttering
- **Receiver naming**: Use consistent, short receiver names
- **Comment spacing**: Use proper spacing in comments (comment-spacings)
- **Standard library**: Use standard library variables/constants when available (usestdlibvars)
- **Printf functions**: Name printf-like functions with 'f' suffix (goprintffuncname)

## Documentation Standards

### Code Documentation
- Use clear and concise comments
- Document all exported functions, types, and constants
- Use Go doc comments (start with the name of the item being documented)
- Include examples in documentation when helpful

Example:
```go
// ProcessConfig parses and validates the configuration file.
// It returns a Config struct with validated settings or an error
// if the configuration is invalid.
//
// Example:
//   config, err := ProcessConfig("config.yaml")
//   if err != nil {
//       log.Fatal(err)
//   }
func ProcessConfig(filename string) (*Config, error) {
    // implementation
}
```

### README and Documentation
- Keep README.md up to date with installation and usage instructions
- Document API endpoints if this is a web service
- Include configuration examples
- Provide troubleshooting guides for common issues

## Dependencies

### Dependency Management
- Use Go modules for dependency management
- Keep dependencies minimal and well-maintained
- Prefer standard library when possible
- Pin major versions and update dependencies regularly
- Use `go mod tidy` to clean up unused dependencies
- **Blocked dependencies** (use alternatives):
  - `github.com/golang/protobuf` → use `google.golang.org/protobuf`
  - `github.com/satori/go.uuid` → use `github.com/google/uuid`
  - `github.com/gofrs/uuid` → use `github.com/google/uuid`

### Preferred Libraries
- **Testing:** testify for assertions and mocking
- **HTTP Router:** [specify preferred router, e.g., gorilla/mux, gin, chi]
- **Database:** [specify preferred database driver, e.g., lib/pq for PostgreSQL]
- **Logging:** [specify preferred logging library, e.g., logrus, zap]
- **Configuration:** [specify preferred config library, e.g., viper, envconfig]

## Security & Performance Guidelines

### Security Considerations
- Validate all input data
- Use proper authentication and authorization
- Handle sensitive data securely (no secrets in logs)
- Follow OWASP guidelines for web applications
- Use HTTPS for all external communications
- Implement proper rate limiting for APIs

### Performance Guidelines
- Profile code for performance bottlenecks
- Use goroutines for concurrent operations when appropriate
- Implement proper context handling for timeouts and cancellation
- Consider memory usage and garbage collection impact
- Use connection pooling for database operations
- Cache expensive operations when possible

## Development Workflow

### Branch Naming
- `feat/` - New features
- `fix/` - Bug fixes
- `docs/` - Documentation updates
- `chore/` - Maintenance tasks
- `test/` - Test-related changes

### Commit Messages
- Use conventional commit format
- Start with a verb in present tense (add, fix, update, remove)
- Keep the first line under 50 characters
- Include detailed description when necessary

### Code Review Guidelines
- Review for correctness, performance, and maintainability
- Check test coverage for new code
- Ensure documentation is updated
- Verify error handling is appropriate

## Environment-Specific Notes

### Development
- Use `go run` for quick testing
- Use `go build` for local builds
- Set up proper IDE configuration for Go development

### Production
- Use proper logging levels
- Implement health checks
- Set up monitoring and alerting
- Use graceful shutdown for services

---

**Note:** Update this file as the project evolves to keep Copilot's suggestions relevant and helpful.