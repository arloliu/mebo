package jsonstream_test

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"

	"github.com/arloliu/mebo/tests/measure/jsonstream"
)

// DataPoint represents a time-series data point
type DataPoint struct {
	T int64   `json:"t"` // timestamp
	V float64 `json:"v"` // value
	A string  `json:"a"` // attribute
}

// Example_streamMetrics demonstrates basic usage of StreamMetrics
func Example_streamMetrics() {
	jsonData := `{
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
	}`

	reader := strings.NewReader(jsonData)

	config := jsonstream.StreamConfig{
		TopLevelKeys:    []string{"ToolID", "ContextID", "Location"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	metadata, err := jsonstream.StreamMetrics(reader, config, func(metricName string, raw json.RawMessage) error {
		var value DataPoint
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		fmt.Printf("%s: t=%d, v=%.1f, a=%s\n", metricName, value.T, value.V, value.A)

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\nMetadata:\n")
	fmt.Printf("  ToolID: %s\n", metadata["ToolID"])
	fmt.Printf("  ContextID: %s\n", metadata["ContextID"])
	fmt.Printf("  Location: %s\n", metadata["Location"])

	// Output:
	// temperature: t=1234567890, v=23.5, a=celsius
	// temperature: t=1234567891, v=24.1, a=celsius
	// temperature: t=1234567892, v=23.8, a=celsius
	// humidity: t=1234567890, v=45.2, a=percent
	// humidity: t=1234567891, v=46.1, a=percent
	//
	// Metadata:
	//   ToolID: sensor-123
	//   ContextID: abc-xyz
	//   Location: Building-A
}

// Example_aggregateStatistics demonstrates computing per-metric statistics
func Example_aggregateStatistics() {
	jsonData := `{
		"ToolID": "stats-demo",
		"MetricValues": [
			{
				"Name": "temperature",
				"Values": [
					{"t": 1, "v": 20.0, "a": ""},
					{"t": 2, "v": 25.0, "a": ""},
					{"t": 3, "v": 22.0, "a": ""},
					{"t": 4, "v": 23.0, "a": ""}
				]
			},
			{
				"Name": "humidity",
				"Values": [
					{"t": 1, "v": 40.0, "a": ""},
					{"t": 2, "v": 50.0, "a": ""},
					{"t": 3, "v": 45.0, "a": ""}
				]
			}
		]
	}`

	reader := strings.NewReader(jsonData)

	config := jsonstream.StreamConfig{
		TopLevelKeys:    []string{"ToolID"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	type Stats struct {
		Count int
		Sum   float64
		Min   float64
		Max   float64
	}

	stats := make(map[string]*Stats)

	metadata, err := jsonstream.StreamMetrics(reader, config, func(metricName string, raw json.RawMessage) error {
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
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("ToolID: %s\n\n", metadata["ToolID"])

	for name, s := range stats {
		avg := s.Sum / float64(s.Count)
		fmt.Printf("%s:\n", name)
		fmt.Printf("  Count: %d\n", s.Count)
		fmt.Printf("  Average: %.1f\n", avg)
		fmt.Printf("  Min: %.1f\n", s.Min)
		fmt.Printf("  Max: %.1f\n", s.Max)
	}

	// Output:
	// ToolID: stats-demo
	//
	// temperature:
	//   Count: 4
	//   Average: 22.5
	//   Min: 20.0
	//   Max: 25.0
	// humidity:
	//   Count: 3
	//   Average: 45.0
	//   Min: 40.0
	//   Max: 50.0
}

// Example_filterByMetric demonstrates filtering specific metrics
func Example_filterByMetric() {
	jsonData := `{
		"ToolID": "filter-demo",
		"MetricValues": [
			{
				"Name": "temperature",
				"Values": [
					{"t": 1, "v": 23.5, "a": ""},
					{"t": 2, "v": 24.1, "a": ""}
				]
			},
			{
				"Name": "humidity",
				"Values": [
					{"t": 1, "v": 45.2, "a": ""},
					{"t": 2, "v": 46.1, "a": ""}
				]
			},
			{
				"Name": "pressure",
				"Values": [
					{"t": 1, "v": 1013.25, "a": ""},
					{"t": 2, "v": 1012.50, "a": ""}
				]
			}
		]
	}`

	reader := strings.NewReader(jsonData)

	config := jsonstream.StreamConfig{
		TopLevelKeys:    []string{"ToolID"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	// Only process temperature and humidity
	targetMetrics := map[string]bool{
		"temperature": true,
		"humidity":    true,
	}

	processed := 0

	_, err := jsonstream.StreamMetrics(reader, config, func(metricName string, raw json.RawMessage) error {
		if !targetMetrics[metricName] {
			return nil // Skip non-target metrics
		}

		var value DataPoint
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}

		processed++
		fmt.Printf("%s: v=%.1f\n", metricName, value.V)

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\nProcessed %d values (pressure was skipped)\n", processed)

	// Output:
	// temperature: v=23.5
	// temperature: v=24.1
	// humidity: v=45.2
	// humidity: v=46.1
	//
	// Processed 4 values (pressure was skipped)
}

// Example_collectByMetric demonstrates collecting values per metric
func Example_collectByMetric() {
	jsonData := `{
		"ToolID": "collect-demo",
		"MetricValues": [
			{
				"Name": "cpu_usage",
				"Values": [
					{"t": 1, "v": 45.2, "a": "percent"},
					{"t": 2, "v": 52.1, "a": "percent"},
					{"t": 3, "v": 48.5, "a": "percent"}
				]
			},
			{
				"Name": "memory_usage",
				"Values": [
					{"t": 1, "v": 62.3, "a": "percent"},
					{"t": 2, "v": 65.8, "a": "percent"}
				]
			}
		]
	}`

	reader := strings.NewReader(jsonData)

	config := jsonstream.StreamConfig{
		TopLevelKeys:    []string{"ToolID"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	// Collect values per metric
	metricValues := make(map[string][]float64)

	_, err := jsonstream.StreamMetrics(reader, config, func(metricName string, raw json.RawMessage) error {
		var value DataPoint
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}

		metricValues[metricName] = append(metricValues[metricName], value.V)

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	for name, values := range metricValues {
		fmt.Printf("%s: %v\n", name, values)
	}

	// Output:
	// cpu_usage: [45.2 52.1 48.5]
	// memory_usage: [62.3 65.8]
}

// Example_conditionalProcessing demonstrates conditional value processing
func Example_conditionalProcessing() {
	jsonData := `{
		"ToolID": "threshold-demo",
		"MetricValues": [
			{
				"Name": "temperature",
				"Values": [
					{"t": 1, "v": 18.5, "a": "normal"},
					{"t": 2, "v": 25.2, "a": "high"},
					{"t": 3, "v": 22.0, "a": "normal"},
					{"t": 4, "v": 28.5, "a": "high"},
					{"t": 5, "v": 31.0, "a": "critical"}
				]
			}
		]
	}`

	reader := strings.NewReader(jsonData)

	config := jsonstream.StreamConfig{
		TopLevelKeys:    []string{"ToolID"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	threshold := 25.0
	highValueCount := 0

	_, err := jsonstream.StreamMetrics(reader, config, func(metricName string, raw json.RawMessage) error {
		var value DataPoint
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}

		if value.V > threshold {
			highValueCount++
			fmt.Printf("Alert: %s exceeded threshold: %.1f (status: %s)\n",
				metricName, value.V, value.A)
		}

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\nTotal values above %.1f: %d\n", threshold, highValueCount)

	// Output:
	// Alert: temperature exceeded threshold: 25.2 (status: high)
	// Alert: temperature exceeded threshold: 28.5 (status: high)
	// Alert: temperature exceeded threshold: 31.0 (status: critical)
	//
	// Total values above 25.0: 3
}
