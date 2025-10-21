# JSON Stream Quick Reference

## Basic Usage

```go
import "github.com/arloliu/mebo/tests/measure/jsonstream"

// 1. Open file
file, _ := os.Open("data.json")
defer file.Close()

// 2. Configure structure
config := jsonstream.StreamConfig{
    TopLevelKeys:    []string{"ToolID", "ContextID"},
    MetricsArrayKey: "MetricValues",
    MetricNameKey:   "Name",
    ValuesArrayKey:  "Values",
}

// 3. Stream and process
metadata, err := jsonstream.StreamMetrics(file, config,
    func(metricName string, raw json.RawMessage) error {
        var value DataPoint
        json.Unmarshal(raw, &value)

        // Process value here
        fmt.Printf("%s: %v\n", metricName, value)
        return nil
    })
```

## Common Patterns

### Count Values per Metric

```go
counts := make(map[string]int)

jsonstream.StreamMetrics(reader, config,
    func(metricName string, _ json.RawMessage) error {
        counts[metricName]++
        return nil
    })
```

### Compute Average per Metric

```go
type Accumulator struct {
    Sum   float64
    Count int
}

acc := make(map[string]*Accumulator)

jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        var value DataPoint
        json.Unmarshal(raw, &value)

        if acc[metricName] == nil {
            acc[metricName] = &Accumulator{}
        }
        acc[metricName].Sum += value.V
        acc[metricName].Count++
        return nil
    })

// Calculate averages
for name, a := range acc {
    avg := a.Sum / float64(a.Count)
    fmt.Printf("%s: %.2f\n", name, avg)
}
```

### Filter by Metric Name

```go
targets := map[string]bool{"temperature": true, "humidity": true}

jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        if !targets[metricName] {
            return nil // Skip
        }

        var value DataPoint
        json.Unmarshal(raw, &value)
        // Process only target metrics
        return nil
    })
```

### Filter by Value Threshold

```go
threshold := 25.0

jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        var value DataPoint
        json.Unmarshal(raw, &value)

        if value.V > threshold {
            fmt.Printf("Alert: %s = %.2f\n", metricName, value.V)
        }
        return nil
    })
```

### Collect All Values

```go
values := make(map[string][]float64)

jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        var value DataPoint
        json.Unmarshal(raw, &value)

        values[metricName] = append(values[metricName], value.V)
        return nil
    })
```

### Find Min/Max per Metric

```go
type MinMax struct {
    Min float64
    Max float64
}

minmax := make(map[string]*MinMax)

jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        var value DataPoint
        json.Unmarshal(raw, &value)

        if minmax[metricName] == nil {
            minmax[metricName] = &MinMax{
                Min: math.MaxFloat64,
                Max: -math.MaxFloat64,
            }
        }

        mm := minmax[metricName]
        if value.V < mm.Min {
            mm.Min = value.V
        }
        if value.V > mm.Max {
            mm.Max = value.V
        }
        return nil
    })
```

### Stop Processing Early

```go
limit := 1000
processed := 0

jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        processed++
        if processed >= limit {
            return fmt.Errorf("limit reached")
        }

        // Process value...
        return nil
    })
```

### Convert to Mebo Blobs

```go
import "github.com/arloliu/mebo/blob"

blobSet := blob.NewNumericBlobSetMaterial()

jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        var value DataPoint
        json.Unmarshal(raw, &value)

        blobSet.Add(metricName, value.T, value.V)
        return nil
    })

encoded := blobSet.Encode()
```

## Error Handling

```go
metadata, err := jsonstream.StreamMetrics(reader, config, processor)
if err != nil {
    // Check error type
    if strings.Contains(err.Error(), "processor error") {
        // Processor returned error
    } else if strings.Contains(err.Error(), "failed to read") {
        // JSON parsing error
    } else if strings.Contains(err.Error(), "cannot be empty") {
        // Configuration error
    }
    log.Fatal(err)
}
```

## Configuration Examples

### Minimal Config

```go
config := jsonstream.StreamConfig{
    MetricsArrayKey: "metrics",
    MetricNameKey:   "name",
    ValuesArrayKey:  "data",
}
```

### Full Config

```go
config := jsonstream.StreamConfig{
    TopLevelKeys:    []string{"ToolID", "ContextID", "Location", "Timestamp"},
    MetricsArrayKey: "MetricValues",
    MetricNameKey:   "Name",
    ValuesArrayKey:  "Values",
}
```

### Validation

```go
if err := config.Validate(); err != nil {
    log.Fatal("Invalid config:", err)
}
```

## Data Structures

### Basic DataPoint

```go
type DataPoint struct {
    T int64   `json:"t"`
    V float64 `json:"v"`
    A string  `json:"a"`
}
```

### Complex Value

```go
type ComplexValue struct {
    Timestamp  int64              `json:"timestamp"`
    Value      float64            `json:"value"`
    Unit       string             `json:"unit"`
    Quality    int                `json:"quality"`
    Attributes map[string]string  `json:"attributes"`
}
```

## Performance Tips

1. **Reuse structures**: Don't allocate new structs in processor
2. **Skip unnecessary unmarshaling**: Check metric name first
3. **Use raw values**: Access `json.RawMessage` directly when possible
4. **Avoid buffering**: Process values immediately
5. **Handle errors early**: Return errors to stop processing

## Memory Usage

- Base: ~32KB (token buffer)
- Per value: Size of your struct + unmarshaling overhead
- Total: ~32KB + sizeof(YourStruct)

## Testing

```bash
# Run all tests
go test ./jsonstream/...

# Run with coverage
go test ./jsonstream/... -cover

# Run specific example
go test ./jsonstream/... -run Example_streamMetrics

# Verbose output
go test ./jsonstream/... -v
```

## Limitations

- Not thread-safe (use separate readers per goroutine)
- Top-level values must be strings
- No random access to values
- Cannot revisit processed values
- Processor errors stop entire stream

## Getting Help

- See [README.md](README.md) for detailed documentation
- See [DESIGN.md](DESIGN.md) for architecture details
- Run examples: `go test -v -run Example`
- Check [metric_stream_example_test.go](metric_stream_example_test.go) for more patterns
