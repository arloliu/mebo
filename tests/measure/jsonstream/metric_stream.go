package jsonstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// StreamConfig defines the structure for parsing nested metric JSON.
//
// This configuration specifies how to navigate the JSON structure to extract
// top-level metadata and stream through nested metric values.
type StreamConfig struct {
	// TopLevelKeys are the field names to extract from the root object.
	// All values are assumed to be strings.
	// Example: []string{"ToolID", "ContextID", "Key1", "Key2"}
	TopLevelKeys []string

	// MetricsArrayKey is the field name of the metrics array in the root object.
	// Example: "MetricValues"
	MetricsArrayKey string

	// MetricNameKey is the field name within each metric object containing the metric name.
	// Example: "Name"
	MetricNameKey string

	// ValuesArrayKey is the field name within each metric object containing the values array.
	// Example: "Values"
	ValuesArrayKey string
}

// Validate checks if the configuration is valid.
//
// Returns:
//   - error: Validation error if configuration is invalid, nil otherwise
func (c StreamConfig) Validate() error {
	if c.MetricsArrayKey == "" {
		return errors.New("MetricsArrayKey cannot be empty")
	}
	if c.MetricNameKey == "" {
		return errors.New("MetricNameKey cannot be empty")
	}
	if c.ValuesArrayKey == "" {
		return errors.New("ValuesArrayKey cannot be empty")
	}

	return nil
}

// MetricValueProcessor is a callback function that processes individual metric values.
//
// The callback is invoked for each value in the nested values array, providing both
// the metric name context and the raw JSON value for flexible processing.
//
// Parameters:
//   - metricName: The name of the metric being processed (from MetricNameKey field)
//   - rawValue: The raw JSON message of a single value element
//
// Returns:
//   - error: Any error that occurred during processing, nil on success
type MetricValueProcessor func(metricName string, rawValue json.RawMessage) error

// StreamMetrics streams nested metric values from a JSON reader.
//
// This function efficiently processes large JSON files by:
//  1. Extracting top-level metadata fields specified in config.TopLevelKeys as strings
//  2. Streaming through the metrics array without loading all into memory
//  3. For each metric, extracting the metric name
//  4. Streaming through the values array for that metric
//  5. Invoking the processor callback for each value with metric name context
//
// The function maintains constant memory usage regardless of file size by streaming
// data and processing values one at a time through the callback.
//
// Parameters:
//   - r: The input reader containing JSON data
//   - config: Configuration specifying the JSON structure
//   - processor: Callback function invoked for each metric value
//
// Returns:
//   - metadata: Map of top-level key-value pairs (all values are strings)
//   - error: Any error during streaming or processing, nil on success
//
// Example:
//
//	config := StreamConfig{
//	    TopLevelKeys:    []string{"ToolID", "ContextID"},
//	    MetricsArrayKey: "MetricValues",
//	    MetricNameKey:   "Name",
//	    ValuesArrayKey:  "Values",
//	}
//
//	metadata, err := StreamMetrics(reader, config, func(metricName string, raw json.RawMessage) error {
//	    var value DataPoint
//	    if err := json.Unmarshal(raw, &value); err != nil {
//	        return err
//	    }
//	    fmt.Printf("Metric: %s, Value: %v\n", metricName, value)
//	    return nil
//	})
//
//	fmt.Printf("ToolID: %s\n", metadata["ToolID"])
func StreamMetrics(
	r io.Reader,
	config StreamConfig,
	processor MetricValueProcessor,
) (metadata map[string]string, err error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	decoder := json.NewDecoder(r)
	metadata = make(map[string]string)

	// Read object start
	t, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to read object start: %w", err)
	}
	if t != json.Delim('{') {
		return nil, fmt.Errorf("expected '{', got %v", t)
	}

	// Build map of top-level keys for quick lookup
	topLevelKeySet := make(map[string]bool)
	for _, key := range config.TopLevelKeys {
		topLevelKeySet[key] = true
	}

	// Iterate through object keys
	for decoder.More() {
		// Read key
		t, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("failed to read key: %w", err)
		}

		key, ok := t.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key, got %T", t)
		}

		switch {
		case topLevelKeySet[key]:
			// Extract top-level metadata field
			var value string
			if err := decoder.Decode(&value); err != nil {
				return nil, fmt.Errorf("failed to decode top-level field %s: %w", key, err)
			}
			metadata[key] = value
		case key == config.MetricsArrayKey:
			// Found the metrics array, stream it
			if err := streamMetricsArray(decoder, config, processor); err != nil {
				return nil, fmt.Errorf("failed to stream metrics array: %w", err)
			}
		default:
			// Skip non-target fields
			if err := skipValue(decoder); err != nil {
				return nil, fmt.Errorf("failed to skip field %s: %w", key, err)
			}
		}
	}

	return metadata, nil
} // streamMetricsArray processes the metrics array, streaming each metric's values.
func streamMetricsArray(
	decoder *json.Decoder,
	config StreamConfig,
	processor MetricValueProcessor,
) error {
	// Read array start
	t, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("failed to read array start: %w", err)
	}
	if t != json.Delim('[') {
		return fmt.Errorf("expected '[', got %v", t)
	}

	// Process each metric object
	for decoder.More() {
		if err := streamMetricObject(decoder, config, processor); err != nil {
			return err
		}
	}

	// Read array end
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("failed to read array end: %w", err)
	}

	return nil
}

// streamMetricObject processes a single metric object, extracting name and streaming values.
//
//nolint:cyclop // Metric object parsing requires this complexity
func streamMetricObject(
	decoder *json.Decoder,
	config StreamConfig,
	processor MetricValueProcessor,
) error {
	// Read object start
	t, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("failed to read metric object start: %w", err)
	}
	if t != json.Delim('{') {
		return fmt.Errorf("expected '{', got %v", t)
	}

	var metricName string
	foundName := false

	// Iterate through metric object keys
	for decoder.More() {
		// Read key
		t, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("failed to read metric field key: %w", err)
		}

		key, ok := t.(string)
		if !ok {
			return fmt.Errorf("expected string key, got %T", t)
		}

		switch {
		case key == config.MetricNameKey:
			// Extract metric name
			if err := decoder.Decode(&metricName); err != nil {
				return fmt.Errorf("failed to decode metric name: %w", err)
			}
			foundName = true
		case key == config.ValuesArrayKey:
			// Found the values array
			if !foundName {
				return fmt.Errorf("metric name not found before values array")
			}
			if err := streamValuesArray(decoder, metricName, processor); err != nil {
				return fmt.Errorf("failed to stream values for metric %s: %w", metricName, err)
			}
		default:
			// Skip other metric fields
			if err := skipValue(decoder); err != nil {
				return fmt.Errorf("failed to skip metric field %s: %w", key, err)
			}
		}
	} // Read object end
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("failed to read metric object end: %w", err)
	}

	if !foundName {
		return fmt.Errorf("metric name field %q not found in metric object", config.MetricNameKey)
	}

	return nil
}

// streamValuesArray processes the values array for a specific metric.
func streamValuesArray(
	decoder *json.Decoder,
	metricName string,
	processor MetricValueProcessor,
) error {
	// Read array start
	t, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("failed to read values array start: %w", err)
	}
	if t != json.Delim('[') {
		return fmt.Errorf("expected '[', got %v", t)
	}

	// Process each value element
	for decoder.More() {
		// Read raw JSON value
		var rawValue json.RawMessage
		if err := decoder.Decode(&rawValue); err != nil {
			return fmt.Errorf("failed to read value element: %w", err)
		}

		// Invoke processor callback
		if err := processor(metricName, rawValue); err != nil {
			return fmt.Errorf("processor error for metric %s: %w", metricName, err)
		}
	}

	// Read array end
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("failed to read values array end: %w", err)
	}

	return nil
}

// skipValue skips a single JSON value in the decoder.
//
// This function handles all JSON types: null, bool, number, string, array, and object.
// For arrays and objects, it recursively skips nested values.
//
// Parameters:
//   - decoder: JSON decoder positioned at the start of a value
//
// Returns:
//   - error: Error if skipping fails, nil otherwise
//
//nolint:cyclop // Recursive JSON navigation requires this complexity
func skipValue(decoder *json.Decoder) error {
	// Read the next token to determine value type
	t, err := decoder.Token()
	if err != nil {
		return err
	}

	switch t {
	case json.Delim('['):
		// Array: skip all elements
		for decoder.More() {
			if err := skipValue(decoder); err != nil {
				return err
			}
		}
		// Read closing ']'
		if _, err := decoder.Token(); err != nil {
			return err
		}

	case json.Delim('{'):
		// Object: skip all key-value pairs
		for decoder.More() {
			// Skip key
			if _, err := decoder.Token(); err != nil {
				return err
			}
			// Skip value
			if err := skipValue(decoder); err != nil {
				return err
			}
		}
		// Read closing '}'
		if _, err := decoder.Token(); err != nil {
			return err
		}

	default:
		// Primitive value (null, bool, number, string) - already consumed by Token()
	}

	return nil
}
