# Contributing to Mebo

Thank you for your interest in contributing to Mebo! This document provides guidelines and instructions for contributing.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Coding Standards](#coding-standards)
- [Testing Guidelines](#testing-guidelines)
- [Pull Request Process](#pull-request-process)
- [Reporting Issues](#reporting-issues)
- [Documentation](#documentation)
- [License](#license)

## Code of Conduct

### Our Pledge

We are committed to providing a welcoming and inclusive environment for all contributors. Please:
- Be respectful and considerate
- Welcome diverse perspectives
- Focus on constructive feedback
- Help others learn and grow

### Unacceptable Behavior

- Harassment, discrimination, or intimidation
- Trolling, insulting comments, or personal attacks
- Publishing others' private information
- Other conduct inappropriate in a professional setting

Report issues to the project maintainers via GitHub Issues.

## Getting Started

### Prerequisites

- **Go**: Version 1.23 or later ([download](https://go.dev/dl/))
- **Git**: For version control
- **golangci-lint**: Version 2.5.0 for code quality checks
- **Make**: For build automation (optional but recommended)

### Quick Start

```bash
# Clone the repository
git clone https://github.com/arloliu/mebo.git
cd mebo

# Install dependencies
go mod download

# Run tests
make test

# Run linter
make lint

# Run benchmarks
make bench
```

## Development Setup

### 1. Fork and Clone

```bash
# Fork the repository on GitHub, then:
git clone https://github.com/YOUR_USERNAME/mebo.git
cd mebo

# Add upstream remote
git remote add upstream https://github.com/arloliu/mebo.git
```

### 2. Install Tools

#### golangci-lint v2.5.0

**macOS/Linux:**
```bash
make linter-update
```

**Manual Installation:**
```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v2.5.0
```

**Verify Installation:**
```bash
make linter-version
# Should output: golangci-lint has version 2.5.0
```

### 3. Create a Branch

```bash
# Update your fork
git fetch upstream
git checkout main
git merge upstream/main

# Create a feature branch
git checkout -b feat/your-feature-name
```

### Branch Naming Convention

- `feat/` - New features
- `fix/` - Bug fixes
- `docs/` - Documentation updates
- `chore/` - Maintenance tasks (dependencies, build, etc.)
- `test/` - Test improvements
- `refactor/` - Code refactoring without behavior changes
- `perf/` - Performance improvements

Examples:
- `feat/add-numeric-streaming`
- `fix/encoder-memory-leak`
- `docs/update-readme-examples`
- `perf/optimize-delta-encoding`

## Coding Standards

Mebo follows strict coding standards to ensure consistency, maintainability, and performance.

### Go Style Guidelines

- Follow the [official Go style guide](https://go.dev/doc/effective_go)
- Use `goimports` for formatting (handled by `golangci-lint`)
- All code must pass `make lint` with zero issues

### Key Conventions

#### Naming
- **Packages**: lowercase, short, descriptive (e.g., `blob`, `encoding`)
- **Functions**: `CamelCase` (exported) or `camelCase` (unexported)
- **Variables**: `camelCase`
- **Constants**: `CamelCase` for package-level constants
- **Receivers**: short and consistent (1-2 characters, e.g., `e` for encoder)

#### Code Organization

**File Declaration Order:**
1. Package declaration
2. Imports (standard lib → external → internal)
3. Constants (exported first)
4. Variables (exported first)
5. Types (exported first)
6. Factory functions (`NewX()` immediately after type)
7. Exported functions
8. Unexported functions
9. Exported methods (grouped by receiver type)
10. Unexported methods (grouped by receiver type)

**3-File Maximum Rule:**

Each component should have **at most 3 files**:
1. Implementation file (e.g., `numeric_encoder.go`)
2. Test file (e.g., `numeric_encoder_test.go`)
3. Benchmark file (optional, e.g., `numeric_encoder_bench_test.go`)

❌ **Don't Create:**
- `*_helper_test.go`
- `*_util_test.go`
- Cross-cutting test files

#### Function Guidelines

- **Length**: Maximum 100 lines, prefer <50 lines
- **Complexity**: Maximum cyclomatic complexity of 22
- **Naked returns**: Only in functions ≤40 lines
- **Loop patterns** (Go 1.22+):
  ```go
  // Use when you need the index
  for i := range items {
      process(i, items[i])
  }

  // Use when you don't need the index
  for range items {
      doSomething()
  }

  // For benchmarks (Go 1.24+)
  for b.Loop() {
      benchmarkedCode()
  }

  // Simple iteration (Go 1.22+)
  for range 10 {
      repeat()
  }
  ```

### Error Handling

```go
// Always handle errors explicitly
result, err := doSomething()
if err != nil {
    return fmt.Errorf("failed to do something: %w", err)
}

// Use early returns to reduce nesting
if err := validate(); err != nil {
    return err
}

// Check type assertions with comma ok
if config, ok := value.(Config); ok {
    // Use config
}
```

### Performance Best Practices

- **Pre-allocate slices/maps** when size is known
- **Prefer branchless code** in hot paths
- **Write for inlining** (small, simple functions)
- **Avoid unnecessary allocations** (use `sync.Pool` when appropriate)
- **Pass by value** for small structs (<24 bytes)

### Documentation

#### Godoc Format

All exported functions must have godoc comments:

```go
// FunctionName provides a brief one-line description.
//
// Optional detailed description explaining purpose, behavior, and usage.
//
// Parameters:
//   - param1: Description of the first parameter and its constraints
//   - param2: Description of the second parameter and its expected values
//
// Returns:
//   - returnType1: Description of what the first return value represents
//   - error: Description of error conditions
//
// Example:
//
//	encoder := NewEncoder()
//	data := []byte("example")
//	result, err := encoder.Process(data)
//	if err != nil {
//	    log.Fatal(err)
//	}
func FunctionName(param1 Type1, param2 Type2) (returnType1, error) {
    // implementation
}
```

**Key Requirements:**
- First line: Brief description starting with function name
- Parameters section for all parameters
- Returns section for all return values
- Example section showing realistic usage

See `encoding/metric_names.go` for canonical examples.

## Testing Guidelines

### Writing Tests

#### Test File Organization

- Place tests in `*_test.go` files in the same package
- Place benchmarks in `*_bench_test.go` files (optional)
- Follow the 3-file maximum rule

#### When to Use Table-Driven Tests

**Use table-driven tests** when you have **multiple test cases**:

```go
func TestEncoder(t *testing.T) {
    tests := []struct {
        name     string
        input    []byte
        expected int
        wantErr  bool
    }{
        {
            name:     "valid input",
            input:    []byte{1, 2, 3},
            expected: 3,
            wantErr:  false,
        },
        {
            name:     "empty input",
            input:    []byte{},
            expected: 0,
            wantErr:  true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := Process(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("Process() error = %v, wantErr %v", err, tt.wantErr)
            }
            if result != tt.expected {
                t.Errorf("Process() = %v, want %v", result, tt.expected)
            }
        })
    }
}
```

**Use simple tests** for **single test cases**:

```go
func TestGetEngine(t *testing.T) {
    engine := GetEngine()

    require.Equal(t, binary.LittleEndian, engine)
    require.Implements(t, (*Engine)(nil), engine)
}
```

#### Testing Best Practices

- Use `testify` for assertions: `require.Equal()`, `require.NoError()`
- Test edge cases and error scenarios
- Use `t.Parallel()` when tests are independent
- Use `t.Setenv()` instead of `os.Setenv()`
- Mock external dependencies for isolated tests

### Running Tests

```bash
# Run all tests
make test

# Run tests with race detector
make test-race

# Run specific package
go test ./blob/...

# Run specific test
go test -run TestNumericEncoder ./blob/

# Generate coverage report
make coverage
```

### Coverage Requirements

- **Target**: >80% code coverage for all packages
- **Critical paths**: 100% coverage for encoding/decoding logic
- **Check coverage**: `make coverage` generates HTML report

### Writing Benchmarks

```go
func BenchmarkEncoder(b *testing.B) {
    encoder := NewEncoder()
    data := []byte{1, 2, 3, 4, 5}

    b.ResetTimer()
    for b.Loop() {
        encoder.Write(data)
    }
}
```

**Benchmark Guidelines:**
- Use `b.Loop()` (Go 1.24+) instead of `for i := 0; i < b.N; i++`
- Use `b.ResetTimer()` after setup
- Report allocations: `b.ReportAllocs()`
- Run with: `make bench`

## Pull Request Process

### Before Submitting

1. **Ensure all tests pass**:
   ```bash
   make test
   make test-race
   ```

2. **Ensure linting passes**:
   ```bash
   make lint
   ```

3. **Ensure coverage is adequate**:
   ```bash
   make coverage
   # Check that coverage is >80%
   ```

4. **Update documentation**:
   - Update godoc comments for new/changed APIs
   - Update README.md if adding features
   - Update CHANGELOG.md (maintainers will guide you)

5. **Commit your changes**:
   ```bash
   git add .
   git commit -m "feat: add streaming support for numeric encoder"
   ```

### Commit Message Format

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <subject>

<body>

<footer>
```

**Types:**
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `test`: Test additions/changes
- `refactor`: Code refactoring
- `perf`: Performance improvements
- `chore`: Maintenance tasks
- `ci`: CI/CD changes

**Examples:**
```
feat(blob): add streaming support for numeric encoder

Implements streaming API for NumericEncoder to support
incremental encoding of large datasets without loading
all data into memory.

Closes #123
```

```
fix(encoding): correct delta-of-delta overflow handling

Fixed integer overflow in delta-of-delta encoding when
timestamp deltas exceed 2^31. Added test cases to prevent
regression.

Fixes #456
```

### Submitting Pull Request

1. **Push your branch**:
   ```bash
   git push origin feat/your-feature-name
   ```

2. **Open Pull Request** on GitHub:
   - Use a clear, descriptive title
   - Reference related issues (e.g., "Closes #123")
   - Provide context and motivation
   - Describe what was changed and why
   - Include any breaking changes or migration notes

3. **Pull Request Checklist**:
   - [ ] Tests pass (`make test`)
   - [ ] Linting passes (`make lint`)
   - [ ] Coverage is adequate (>80%)
   - [ ] Documentation updated
   - [ ] Commit messages follow convention
   - [ ] Branch is up-to-date with `main`

### Review Process

- Maintainers will review your PR within 2-3 business days
- Address feedback by pushing new commits
- Once approved, maintainers will merge your PR
- PR may be squashed into a single commit

## Reporting Issues

### Bug Reports

Use the GitHub issue tracker. Include:

1. **Description**: Clear summary of the bug
2. **Steps to Reproduce**:
   ```
   1. Create encoder with config X
   2. Write data Y
   3. Observe error Z
   ```
3. **Expected Behavior**: What should happen
4. **Actual Behavior**: What actually happens
5. **Environment**:
   - Mebo version
   - Go version
   - Operating system
6. **Minimal Reproduction**: Smallest code to reproduce the issue

### Feature Requests

1. **Use Case**: Why is this feature needed?
2. **Proposed API**: How should it work?
3. **Alternatives**: What alternatives did you consider?
4. **Additional Context**: Examples, references, etc.

### Questions

- For questions, use **GitHub Discussions** instead of issues
- Check existing documentation first (README, godoc, docs/)

## Documentation

### Types of Documentation

1. **Godoc**: All exported APIs must have godoc comments
2. **README.md**: User-facing documentation and examples
3. **docs/**: Design documents and technical details
4. **CHANGELOG.md**: Version history and release notes

### Documentation Standards

- Use clear, concise language
- Provide examples for complex features
- Keep documentation in sync with code
- Document breaking changes prominently

## License

By contributing to Mebo, you agree that your contributions will be licensed under the **Apache License 2.0**.

See [LICENSE](LICENSE) file for details.

## Questions?

- **GitHub Discussions**: For questions and discussions
- **GitHub Issues**: For bug reports and feature requests
- **Pull Requests**: For code contributions

Thank you for contributing to Mebo! 🎉
