---
description: "Testing guidelines and best practices for Go test files. Use when writing unit tests or benchmarks."
applyTo: "**/*_test.go"
---

# Testing Guidelines

## General Principles

- Write unit tests for all public functions
- Place tests in `_test.go` files in the same package
- Use the standard `testing` package
- Use `testify` for assertions and mocking
- Mock external dependencies for isolated tests
- Test edge cases and error scenarios
- Aim for high test coverage (>80%)
- Use meaningful test names that describe the scenario

## Table-Driven Tests: When to Use

**IMPORTANT**: Use table-driven tests ONLY when you have multiple test cases. Avoid over-engineering single test cases with table structure.

### Use Table-Driven Tests When:

- Testing the same function with multiple different inputs
- Testing boundary conditions with many edge cases
- You have 3+ similar test scenarios

### Example: Table-Driven Test (Multiple Cases)

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
        {
            name:     "edge case: empty data",
            input:    emptyInput,
            expected: emptyOutput,
            wantErr:  false,
        },
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

### Use Simple Tests When:

- Testing a single scenario
- Testing initialization or setup
- The function has a straightforward success case

### Example: Simple Test (Single Case)

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

## Benchmark Guidelines

### Use Go 1.24+ Loop Pattern

```go
func BenchmarkFunction(b *testing.B) {
    // Setup
    data := generateTestData()

    // Reset timer after setup
    b.ResetTimer()

    // Use b.Loop() for Go 1.24+
    for b.Loop() {
        _ = processData(data)
    }
}
```

### Avoid Old Pattern

```go
// OLD: Don't use this pattern anymore
func BenchmarkFunction(b *testing.B) {
    for i := 0; i < b.N; i++ {  // ❌ Old pattern
        _ = processData(data)
    }
}
```

## Testing Best Practices

### Use testify Assertions

```go
import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestExample(t *testing.T) {
    result := DoSomething()

    // Use assert for non-critical checks
    assert.NotNil(t, result)
    assert.Equal(t, expected, result)

    // Use require for critical checks (stops test on failure)
    require.NoError(t, err)
    require.NotEmpty(t, data)
}
```

### Environment Variables

Use `t.Setenv()` instead of `os.Setenv()`:

```go
func TestWithEnvVar(t *testing.T) {
    // GOOD: Automatically cleaned up
    t.Setenv("MY_VAR", "value")
}
```

### Parallel Tests

Use `t.Parallel()` for independent tests:

```go
func TestIndependentFeature(t *testing.T) {
    t.Parallel() // Runs concurrently with other parallel tests
    // Test code here
}
```

### Testable Examples

Examples must have expected output to be testable:

```go
func ExampleEncoder_Write() {
    encoder := NewEncoder()
    encoder.Write([]byte("hello"))
    fmt.Println(encoder.Len())
    // Output: 5
}
```
