# Generic Options Pattern

This package provides a generic, reusable implementation of the functional options pattern for Go. It allows you to create type-safe, composable configuration options for any struct type.

## Features

- **Generic**: Works with any type using Go generics
- **Type-safe**: Compile-time type checking ensures options match their target struct
- **Error handling**: Options can validate inputs and return meaningful errors
- **Composable**: Options can be stored, passed around, and combined
- **Clean API**: Provides a fluent, readable configuration interface

## Basic Usage

### 1. Define your configuration struct

```go
type Config struct {
    Host    string
    Port    int
    Enabled bool
}
```

### 2. Create an option type alias

```go
type ConfigOption = options.Option[*Config]
```

### 3. Create option constructors

```go
// For options that can't fail
func WithHost(host string) ConfigOption {
    return options.NoError(func(config *Config) {
        config.Host = host
    })
}

// For options that can fail with validation
func WithPort(port int) ConfigOption {
    return options.New(func(config *Config) error {
        if port <= 0 || port > 65535 {
            return fmt.Errorf("invalid port: %d", port)
        }
        config.Port = port
        return nil
    })
}
```

### 4. Create a constructor that accepts options

```go
func NewConfig(opts ...ConfigOption) (*Config, error) {
    config := &Config{
        Host: "localhost",
        Port: 8080,
        Enabled: false,
    }

    if err := options.Apply(config, opts...); err != nil {
        return nil, err
    }

    return config, nil
}
```

### 5. Use it

```go
config, err := NewConfig(
    WithHost("example.com"),
    WithPort(443),
    WithEnabled(true),
)
if err != nil {
    log.Fatal(err)
}
```

## API Reference

### Core Types

#### `Option[T any]` interface
The main interface that all options must implement:
```go
type Option[T any] interface {
    apply(T) error
}
```

#### `Func[T any]` struct
A generic wrapper that implements `Option[T]`:
```go
type Func[T any] struct {
    applyFunc func(T) error
}
```

### Constructor Functions

#### `New[T any](fn func(T) error) *Func[T]`
Creates a new option from a function that can return an error. Use this when your option needs to validate inputs or can fail.

```go
func WithTimeout(timeout int) Option[*Config] {
    return options.New(func(c *Config) error {
        if timeout < 0 {
            return fmt.Errorf("timeout cannot be negative")
        }
        c.Timeout = timeout
        return nil
    })
}
```

#### `NoError[T any](fn func(T)) *Func[T]`
Creates a new option from a function that cannot fail. This is a convenience function for simple setters.

```go
func WithDebug(debug bool) Option[*Config] {
    return options.NoError(func(c *Config) {
        c.Debug = debug
    })
}
```

### Utility Functions

#### `Apply[T any](target T, opts ...Option[T]) error`
Applies multiple options to a target object in order. Stops at the first error and returns it.

```go
config := &Config{}
err := options.Apply(config,
    WithHost("localhost"),
    WithPort(8080),
    WithDebug(true),
)
```

## Advanced Usage

### Conditional Options

You can create options that apply conditionally:

```go
func WithSSLIf(condition bool) ConfigOption {
    return options.NoError(func(config *Config) {
        if condition {
            config.SSL = true
            config.Port = 443
        }
    })
}
```

### Composite Options

Create options that apply multiple settings:

```go
func WithProductionDefaults() ConfigOption {
    return options.New(func(config *Config) error {
        config.Host = "prod.example.com"
        config.Port = 443
        config.SSL = true
        config.LogLevel = "warn"
        return nil
    })
}
```

### Option Validation

Options can validate the entire configuration:

```go
func WithValidation() ConfigOption {
    return options.New(func(config *Config) error {
        if config.SSL && config.Port == 80 {
            return fmt.Errorf("SSL cannot be used with port 80")
        }
        return nil
    })
}
```

## Best Practices

1. **Use type aliases**: Create a type alias for your option interface to improve readability
2. **Validate early**: Perform validation in option constructors rather than later
3. **Provide defaults**: Set sensible defaults in your constructor before applying options
4. **Use descriptive names**: Option constructor names should clearly indicate what they do
5. **Group related options**: Consider creating composite options for common combinations
6. **Document options**: Add godoc comments to explain what each option does

## Error Handling

The pattern supports comprehensive error handling:

- Options created with `New()` can return errors for validation
- `Apply()` stops at the first error and returns it
- This allows for fail-fast behavior with clear error messages

## Performance

The generic options pattern has minimal runtime overhead:
- Option creation is just wrapping a function
- Application is a simple function call
- No reflection or dynamic dispatch is used

## Comparison with Other Patterns

### vs. Config Structs
- ✅ More flexible and extensible
- ✅ Backward compatible when adding new options
- ✅ Cleaner API for consumers
- ❌ Slightly more complex to implement

### vs. Builder Pattern
- ✅ More functional style
- ✅ Immutable option objects
- ✅ Better composability
- ❌ Less familiar to some developers

### vs. Variadic Parameters
- ✅ Type-safe and validatable
- ✅ Self-documenting
- ✅ Extensible without breaking changes
- ❌ More setup required

## Examples

See `examples/options_example.go` for complete working examples demonstrating:
- Basic usage with database configuration
- Advanced validation and error handling
- Multiple option types working together
- Best practices for API design