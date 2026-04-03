---
description: "Linter rules and code quality standards (golangci-lint configuration). Use when writing Go code to ensure it passes linting."
applyTo: "**/*.go"
---

# Linting Rules & Quality Standards

## Running Linters

```bash
make lint          # Run all linters
make lint-fix      # Fix auto-fixable issues
```

## Code Quality Rules

### Function Complexity

- **Maximum function length**: 100 lines (revive), prefer under 50
- **Cyclomatic complexity**: Maximum 22 per function (cyclop)
- **Package complexity**: Average under 15.0
- **Naked returns**: Allowed only in functions ≤40 lines (nakedret)

### Context & Imports

- Always pass context as first parameter (context-as-argument)
- Avoid shadowing package names (import-shadowing)

## Security & Safety

### Type Assertions

**ALWAYS** use comma-ok idiom:

```go
// GOOD
if config, ok := value.(Config); ok {
    // Use config
}

// BAD - Will panic on wrong type
config := value.(Config)
```

### Resource Management

- Always close `sql.Rows` and `sql.Stmt` (sqlclosecheck, rowserrcheck)
- Always close HTTP response bodies (bodyclose)

### Error Handling

- Avoid returning nil error with invalid value (nilnil)
- Use `errors.Is` and `errors.As` for error comparison (errorlint)

## Performance & Memory

- Pre-allocate slices when size is known (prealloc)
- Remove unnecessary type conversions (unconvert)
- Avoid assignments that are never used (wastedassign)
- Be careful with duration multiplications (durationcheck)

## Code Style

- Follow Go naming conventions, avoid stuttering
- Use consistent, short receiver names
- Use proper spacing in comments (comment-spacings)
- Use standard library variables/constants when available (usestdlibvars)
- Name printf-like functions with 'f' suffix (goprintffuncname)
