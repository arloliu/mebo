---
description: "Performance optimization and security guidelines for critical paths. Use when working on encoding, blob, or compression hot paths."
applyTo: "internal/encoding/**/*.go, blob/**/*.go, compress/**/*.go"
---

# Performance & Security Guidelines

## Performance Guidelines

### Profiling First

- **Always profile before optimizing**
- Use `pprof` for CPU and memory profiling
- Use benchmarks to measure improvements

```bash
go test -bench=. -cpuprofile=cpu.prof
go tool pprof cpu.prof

go test -bench=. -benchmem > old.txt
# Make changes
go test -bench=. -benchmem > new.txt
benchstat old.txt new.txt
```

### Hot Path Optimization

#### 1. Prefer Branchless Code

```go
// GOOD: Branchless
result := base + (offset * int(condition))
mask := -int(condition)  // 0 or -1
result := (value & mask) | (defaultValue & ^mask)

// AVOID in hot paths: Branches
if condition {
    result = base + offset
} else {
    result = base
}
```

#### 2. Write for Inlining

Keep functions small and simple. Check with:
```bash
go build -gcflags=-m 2>&1 | grep inline
```

#### 3. Minimize Interface Usage in Hot Paths

```go
// GOOD for hot paths: Direct type
type FastProcessor struct {
    encoder *ConcreteEncoder
}

func (p *FastProcessor) Process(data []byte) {
    p.encoder.Encode(data)  // Direct call, can inline
}
```

**Note**: Use interfaces where appropriate for testability. Only avoid them in proven hot paths.

#### 4. Pass Small Structs by Value

```go
// GOOD: Small struct passed by value
type Point struct {
    X, Y float64
}

func Distance(p1, p2 Point) float64 {  // Pass by value
    dx := p2.X - p1.X
    dy := p2.Y - p1.Y
    return math.Sqrt(dx*dx + dy*dy)
}
```

### Memory Management

- **Pre-allocation**: Pre-allocate slices/maps with known capacity
- **String concatenation**: Use `strings.Builder` with `Grow()` for multiple concatenations
- **Buffer pooling**: Use `sync.Pool` for reusable buffers

### Concurrency

- Use `sync.WaitGroup` for goroutine management
- Use buffered error channels for concurrent error collection
- Always handle context cancellation

## Security Guidelines

- Validate all input data at system boundaries
- Handle sensitive data securely (no secrets in logs)
- Follow OWASP guidelines
- Use HTTPS for all external communications
- Implement proper rate limiting for APIs
- Check for dangerous unicode sequences (bidichk)
