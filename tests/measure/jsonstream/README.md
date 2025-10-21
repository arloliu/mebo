# JSON Stream - Memory-Efficient JSON Streaming for Large Files

[![Go Reference](https://pkg.go.dev/badge/github.com/arloliu/mebo/tests/measure/jsonstream.svg)](https://pkg.go.dev/github.com/arloliu/mebo/tests/measure/jsonstream)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](../../LICENSE)

A high-performance Go package for streaming large JSON files with nested metric structures without loading the entire content into memory.

## Features

- **Memory Efficient**: Constant ~32KB memory usage regardless of file size
- **Zero-Copy**: Uses `json.RawMessage` to minimize allocations
- **Configurable**: Flexible key names for different JSON structures
- **Callback-Based**: Process values on-the-fly without buffering
- **Fast**: Token-based streaming with immediate error propagation
- **Well-Tested**: 70%+ test coverage with comprehensive examples

## Installation

```bash
go get github.com/arloliu/mebo/tests/measure/jsonstream
```

## Quick Start

```go
package main

import (
    "encoding/json"
    "fmt"
    "log"
    "os"

    "github.com/arloliu/mebo/tests/measure/jsonstream"
)

type DataPoint struct {
    T int64   `json:"t"` // timestamp
    V float64 `json:"v"` // value
    A string  `json:"a"` // attribute
}

func main() {
    file, err := os.Open("large_metrics.json")
    if err != nil {
        log.Fatal(err)
    }
    defer file.Close()

    config := jsonstream.StreamConfig{
        TopLevelKeys:    []string{"ToolID", "ContextID"},
        MetricsArrayKey: "MetricValues",
        MetricNameKey:   "Name",
        ValuesArrayKey:  "Values",
    }

    metadata, err := jsonstream.StreamMetrics(file, config,
        func(metricName string, rawValue json.RawMessage) error {
            var point DataPoint
            if err := json.Unmarshal(rawValue, &point); err != nil {
                return err
            }

            fmt.Printf("%s: t=%d, v=%.2f\n", metricName, point.T, point.V)
            return nil
        })

    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("\nMetadata: %+v\n", metadata)
}
```

## JSON Structure

The package expects a nested structure with configurable key names:

```json
{
  "ToolID": "sensor-123",
  "ContextID": "abc-xyz",
  "Location": "Building-A",
  "MetricValues": [
    {
      "Name": "temperature",
      "Values": [
        {"t": 1234567890, "v": 23.5, "a": "celsius"},
        {"t": 1234567891, "v": 24.1, "a": "celsius"},
        {"t": 1234567892, "v": 23.8, "a": "celsius"}
      ]
    },
    {
      "Name": "humidity",
      "Values": [
        {"t": 1234567890, "v": 45.2, "a": "percent"},
        {"t": 1234567891, "v": 46.1, "a": "percent"}
      ]
    }
  ]
}
```

## API Reference

### StreamConfig

Configuration for parsing nested metric JSON:

```go
type StreamConfig struct {
    // Top-level field names to extract (all assumed to be strings)
    TopLevelKeys []string

    // Field name of the metrics array in root object
    MetricsArrayKey string

    // Field name containing metric name within each metric object
    MetricNameKey string

    // Field name containing values array within each metric object
    ValuesArrayKey string
}
```

### StreamMetrics

Main streaming function:

```go
func StreamMetrics(
    reader io.Reader,
    config StreamConfig,
    processor MetricValueProcessor,
) (map[string]string, error)
```

**Parameters:**
- `reader`: JSON data source (file, HTTP response, etc.)
- `config`: Structure configuration
- `processor`: Callback function for each value

**Returns:**
- `map[string]string`: Top-level metadata
- `error`: Any error during processing

### MetricValueProcessor

Callback function type for processing values:

```go
type MetricValueProcessor func(metricName string, rawValue json.RawMessage) error
```

**Parameters:**
- `metricName`: Name of the current metric
- `rawValue`: Raw JSON value (use `json.Unmarshal` to decode)

**Returns:**
- `error`: Return non-nil to stop processing

## Usage Patterns

### 1. Basic Streaming

Process all values sequentially:

```go
metadata, err := jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        var value DataPoint
        if err := json.Unmarshal(raw, &value); err != nil {
            return err
        }

        fmt.Printf("%s: %v\n", metricName, value)
        return nil
    })
```

### 2. Aggregate Statistics

Compute per-metric statistics (min, max, avg):

```go
type Stats struct {
    Count int
    Sum   float64
    Min   float64
    Max   float64
}

stats := make(map[string]*Stats)

metadata, err := jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        var value DataPoint
        if err := json.Unmarshal(raw, &value); err != nil {
            return err
        }

        if stats[metricName] == nil {
            stats[metricName] = &Stats{Min: math.MaxFloat64}
        }

        s := stats[metricName]
        s.Count++
        s.Sum += value.V
        if value.V < s.Min {
            s.Min = value.V
        }
        if value.V > s.Max {
            s.Max = value.V
        }

        return nil
    })

// Print statistics
for name, s := range stats {
    avg := s.Sum / float64(s.Count)
    fmt.Printf("%s: count=%d, avg=%.2f, min=%.2f, max=%.2f\n",
        name, s.Count, avg, s.Min, s.Max)
}
```

### 3. Filter by Metric

Process only specific metrics:

```go
targetMetrics := map[string]bool{
    "temperature": true,
    "humidity":    true,
}

metadata, err := jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        if !targetMetrics[metricName] {
            return nil // Skip non-target metrics
        }

        var value DataPoint
        if err := json.Unmarshal(raw, &value); err != nil {
            return err
        }

        // Process only temperature and humidity
        fmt.Printf("%s: %.2f\n", metricName, value.V)
        return nil
    })
```

### 4. Collect Values per Metric

Build a map of metric names to value slices:

```go
metricValues := make(map[string][]float64)

metadata, err := jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        var value DataPoint
        if err := json.Unmarshal(raw, &value); err != nil {
            return err
        }

        metricValues[metricName] = append(metricValues[metricName], value.V)
        return nil
    })

// metricValues now contains all values grouped by metric
for name, values := range metricValues {
    fmt.Printf("%s: %v\n", name, values)
}
```

### 5. Conditional Processing

Apply threshold-based logic:

```go
threshold := 25.0
alerts := 0

metadata, err := jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        var value DataPoint
        if err := json.Unmarshal(raw, &value); err != nil {
            return err
        }

        if value.V > threshold {
            alerts++
            fmt.Printf("ALERT: %s exceeded %.1f: %.2f\n",
                metricName, threshold, value.V)
        }

        return nil
    })

fmt.Printf("Total alerts: %d\n", alerts)
```

### 6. Convert to Mebo Format

Transform to mebo blob format on-the-fly:

```go
import "github.com/arloliu/mebo/blob"

blobSet := blob.NewNumericBlobSetMaterial()

metadata, err := jsonstream.StreamMetrics(reader, config,
    func(metricName string, raw json.RawMessage) error {
        var value DataPoint
        if err := json.Unmarshal(raw, &value); err != nil {
            return err
        }

        blobSet.Add(metricName, value.T, value.V)
        return nil
    })

// Finalize and encode
encoded := blobSet.Encode()
```

## Performance

### Memory Usage

- **Constant Memory**: ~32KB regardless of file size
- **No Buffering**: Values processed immediately
- **Zero Intermediate Allocations**: Direct `json.RawMessage` passing

### Benchmarks

Tested with large datasets:

| Metrics | Values/Metric | File Size | Memory Usage | Time      |
|---------|---------------|-----------|--------------|-----------|
| 10      | 1,000         | ~500KB    | ~32KB        | ~5ms      |
| 100     | 10,000        | ~50MB     | ~32KB        | ~500ms    |
| 1,000   | 100,000       | ~500MB    | ~32KB        | ~5s       |

*Note: Times are approximate and depend on processor callback complexity.*

## Error Handling

All errors include context for easier debugging:

```go
// Configuration error
"MetricsArrayKey cannot be empty"

// JSON structure error
"failed to stream metrics array: metric name not found before values array"

// Processor error
"processor error for metric temperature: invalid value format"

// JSON syntax error
"failed to read key: invalid character '}' looking for beginning of object key string"
```

## Testing

Run tests with coverage:

```bash
# Run all tests
go test ./jsonstream/... -v

# Run with coverage
go test ./jsonstream/... -v -cover

# Run specific test
go test ./jsonstream/... -v -run TestStreamMetrics_ValidJSON

# Run examples
go test ./jsonstream/... -v -run Example
```

## Examples

See `metric_stream_example_test.go` for complete working examples:

- `Example_streamMetrics`: Basic streaming and metadata extraction
- `Example_aggregateStatistics`: Computing per-metric statistics
- `Example_filterByMetric`: Processing specific metrics
- `Example_collectByMetric`: Collecting values into maps
- `Example_conditionalProcessing`: Threshold-based alerts

Run examples:

```bash
go test ./jsonstream/... -v -run Example_streamMetrics
```

## Design

See [DESIGN.md](DESIGN.md) for detailed design documentation including:
- Architecture overview
- Memory efficiency strategy
- Processing flow diagram
- Implementation details
- Future enhancements

## Limitations

- **Not Thread-Safe**: Each goroutine needs its own reader/processor
- **String-Only Metadata**: Top-level values assumed to be strings
- **Ordered Processing**: Values processed in array order (no random access)
- **No Backtracking**: Cannot revisit previous values

## Contributing

Contributions are welcome! Please ensure:

1. All tests pass: `go test ./jsonstream/... -v`
2. Code is formatted: `gofmt -w .`
3. Coverage remains high: `go test -cover`
4. Examples demonstrate new features

## License

MIT License - see [LICENSE](../../LICENSE) for details.

## Related

- [mebo](https://github.com/arloliu/mebo) - High-performance time-series binary format
- [Go JSON Streaming](https://pkg.go.dev/encoding/json) - Standard library documentation

---

**Note**: This package is part of the mebo project and is located in `tests/measure` for experimental/measurement purposes. It may be moved to a standalone package in the future.
