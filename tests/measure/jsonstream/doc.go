// Package jsonstream provides memory-efficient JSON streaming utilities for large files.
//
// This package enables processing GB-level JSON files without loading the entire
// content into memory, using token-based streaming with the standard encoding/json
// package.
//
// # Overview
//
// The package provides a generic streaming solution for nested metric JSON structures:
//
//	{
//	  "ToolID": "sensor-123",
//	  "ContextID": "abc-xyz",
//	  "MetricValues": [
//	    {
//	      "Name": "temperature",
//	      "Values": [
//	        {"t": 1234567890, "v": 23.5, "a": "celsius"},
//	        {"t": 1234567891, "v": 24.1, "a": "celsius"}
//	      ]
//	    },
//	    {
//	      "Name": "humidity",
//	      "Values": [
//	        {"t": 1234567890, "v": 45.2, "a": "percent"}
//	      ]
//	    }
//	  ]
//	}
//
// # Key Features
//
//   - Memory Efficiency: Constant ~32KB memory usage regardless of file size
//   - Zero-Copy: Uses json.RawMessage to pass values without intermediate allocations
//   - Configurable: Flexible key names for different JSON structures
//   - Callback-Based: Process values on-the-fly without buffering
//   - Fail-Fast: Immediate error propagation from processors
//
// # Usage
//
// Basic usage with StreamMetrics:
//
//	reader := strings.NewReader(jsonData)
//
//	config := jsonstream.StreamConfig{
//	    TopLevelKeys:    []string{"ToolID", "ContextID"},
//	    MetricsArrayKey: "MetricValues",
//	    MetricNameKey:   "Name",
//	    ValuesArrayKey:  "Values",
//	}
//
//	metadata, err := jsonstream.StreamMetrics(reader, config,
//	    func(metricName string, rawValue json.RawMessage) error {
//	        var value DataPoint
//	        if err := json.Unmarshal(rawValue, &value); err != nil {
//	            return err
//	        }
//	        // Process the value...
//	        return nil
//	    })
//
// # Processing Patterns
//
// The package supports various processing patterns:
//
// 1. Aggregation: Compute statistics per metric (min, max, avg, etc.)
// 2. Filtering: Process only specific metrics or values
// 3. Collection: Collect values per metric into slices/maps
// 4. Conditional: Apply threshold-based logic
// 5. Conversion: Transform to other formats (e.g., mebo blobs)
//
// # Performance
//
// Memory usage remains constant regardless of file size:
//   - ~32KB for token buffer and internal state
//   - Only one value decoded at a time
//   - No intermediate slices or arrays
//
// Tested with:
//   - Files up to GB size
//   - 100+ metrics with 10,000+ values each
//   - Various JSON structures
//
// # Error Handling
//
// All errors are propagated immediately with context:
//   - Configuration validation errors
//   - JSON syntax errors with location
//   - Processor errors with metric context
//   - Structural errors (missing keys, type mismatches)
//
// Example error messages:
//   - "MetricsArrayKey cannot be empty"
//   - "failed to stream metrics array: metric name not found before values array"
//   - "processor error for metric temperature: invalid value format"
//
// # Examples
//
// See the example tests for complete usage patterns:
//   - Example_streamMetrics: Basic streaming and metadata extraction
//   - Example_aggregateStatistics: Computing per-metric statistics
//   - Example_filterByMetric: Processing specific metrics
//   - Example_collectByMetric: Collecting values into maps
//   - Example_conditionalProcessing: Threshold-based alerts
//
// # Thread Safety
//
// This package is not thread-safe. Each goroutine should use its own reader
// and processor instance.
package jsonstream
