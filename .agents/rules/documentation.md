---
description: "Documentation standards and godoc format for exported Go APIs. Use when documenting exported functions, types, or methods."
applyTo: "**/*.go"
---

# Documentation Standards

## General Principles

- Use clear and concise comments
- Document all exported functions, types, and constants
- Use Go doc comments (start with the name of the item being documented)
- Include examples in documentation when helpful
- Keep documentation up to date with code changes

## Godoc Format for Functions and Methods

**ALL exported functions and methods MUST follow this standardized format:**

```go
// FunctionName provides a brief one-line description of what the function does.
//
// Optional: More detailed description explaining the purpose, behavior, and usage.
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

## Documentation Structure Requirements

1. **First line**: Brief description starting with the function/method name
2. **Blank line**: Separates the summary from detailed description
3. **Detailed description**: Optional but recommended for complex functions
4. **Parameters section**: List all parameters with clear descriptions (use `-` bullets)
5. **Returns section**: List all return values with descriptions
6. **Example section**: Optional but highly recommended

## Common Patterns

- **No parameters**: Omit Parameters section (e.g., `Bytes()`, `Len()`, `Size()`)
- **No return values**: Omit Returns section (e.g., `Write()`, `Reset()`)
- **Error returns**: Always describe error conditions in Returns section
- **Simple getters**: Can have minimal documentation if self-explanatory
- **Complex algorithms**: Include algorithm description before Parameters section

## Type Documentation

```go
// Encoder provides efficient encoding of time-series data using columnar storage.
//
// The encoder supports multiple encoding strategies for both timestamps and values,
// allowing optimization for different data patterns.
type Encoder struct {
    // unexported fields
}
```

## Package Documentation

Every package should have a `doc.go` file with a package-level comment.
