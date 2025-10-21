package jsonstream

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type DataPoint struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
	A string  `json:"a"`
}

func TestStreamMetrics_ValidJSON(t *testing.T) {
	jsonData := `{
		"ToolID": "sensor-123",
		"ContextID": "12345678",
		"Key1": "value1",
		"MetricValues": [
			{
				"Name": "temperature",
				"Values": [
					{"t": 1234567890, "v": 23.5, "a": "celsius"},
					{"t": 1234567891, "v": 24.1, "a": "celsius"}
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
	config := StreamConfig{
		TopLevelKeys:    []string{"ToolID", "ContextID", "Key1"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	type metricValue struct {
		metricName string
		value      DataPoint
	}
	var collected []metricValue

	metadata, err := StreamMetrics(reader, config, func(metricName string, raw json.RawMessage) error {
		var value DataPoint
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		collected = append(collected, metricValue{metricName, value})

		return nil
	})

	require.NoError(t, err)

	// Verify metadata
	require.Equal(t, "sensor-123", metadata["ToolID"])
	require.Equal(t, "12345678", metadata["ContextID"])
	require.Equal(t, "value1", metadata["Key1"])

	// Verify collected values
	require.Len(t, collected, 4)

	// Temperature values
	require.Equal(t, "temperature", collected[0].metricName)
	require.Equal(t, int64(1234567890), collected[0].value.T)
	require.Equal(t, 23.5, collected[0].value.V)

	require.Equal(t, "temperature", collected[1].metricName)
	require.Equal(t, int64(1234567891), collected[1].value.T)
	require.Equal(t, 24.1, collected[1].value.V)

	// Humidity values
	require.Equal(t, "humidity", collected[2].metricName)
	require.Equal(t, int64(1234567890), collected[2].value.T)
	require.Equal(t, 45.2, collected[2].value.V)

	require.Equal(t, "humidity", collected[3].metricName)
	require.Equal(t, int64(1234567891), collected[3].value.T)
	require.Equal(t, 46.1, collected[3].value.V)
}

func TestStreamMetrics_EmptyMetricsArray(t *testing.T) {
	jsonData := `{
		"ToolID": "test-tool",
		"MetricValues": []
	}`

	reader := strings.NewReader(jsonData)
	config := StreamConfig{
		TopLevelKeys:    []string{"ToolID"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	count := 0
	metadata, err := StreamMetrics(reader, config, func(_ string, _ json.RawMessage) error {
		count++

		return nil
	})

	require.NoError(t, err)
	require.Equal(t, "test-tool", metadata["ToolID"])
	require.Equal(t, 0, count)
}

func TestStreamMetrics_EmptyValuesArray(t *testing.T) {
	jsonData := `{
		"ToolID": "test-tool",
		"MetricValues": [
			{
				"Name": "temperature",
				"Values": []
			}
		]
	}`

	reader := strings.NewReader(jsonData)
	config := StreamConfig{
		TopLevelKeys:    []string{"ToolID"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	count := 0
	metadata, err := StreamMetrics(reader, config, func(_ string, _ json.RawMessage) error {
		count++

		return nil
	})

	require.NoError(t, err)
	require.Equal(t, "test-tool", metadata["ToolID"])
	require.Equal(t, 0, count)
}

func TestStreamMetrics_MissingTopLevelKeys(t *testing.T) {
	jsonData := `{
		"ToolID": "test-tool",
		"MetricValues": [
			{
				"Name": "temperature",
				"Values": [{"t": 1, "v": 1.0, "a": ""}]
			}
		]
	}`

	reader := strings.NewReader(jsonData)
	config := StreamConfig{
		TopLevelKeys:    []string{"ToolID", "MissingKey"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	metadata, err := StreamMetrics(reader, config, func(_ string, _ json.RawMessage) error {
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, "test-tool", metadata["ToolID"])
	require.NotContains(t, metadata, "MissingKey")
}

func TestStreamMetrics_LargeDataset(t *testing.T) {
	// Generate JSON with many metrics and values
	var sb strings.Builder
	sb.WriteString(`{"ToolID": "load-test", "MetricValues": [`)

	metricCount := 100
	valuesPerMetric := 100

	for m := range metricCount {
		if m > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"Name": "metric_%d", "Values": [`, m)

		for v := range valuesPerMetric {
			if v > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `{"t": %d, "v": %d.5, "a": "test"}`, v, v)
		}

		sb.WriteString("]}")
	}

	sb.WriteString("]}")

	reader := strings.NewReader(sb.String())
	config := StreamConfig{
		TopLevelKeys:    []string{"ToolID"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	valueCount := 0
	metricNames := make(map[string]int)

	metadata, err := StreamMetrics(reader, config, func(metricName string, _ json.RawMessage) error {
		valueCount++
		metricNames[metricName]++

		return nil
	})

	require.NoError(t, err)
	require.Equal(t, "load-test", metadata["ToolID"])
	require.Equal(t, metricCount*valuesPerMetric, valueCount)
	require.Len(t, metricNames, metricCount)

	// Verify each metric has correct count
	for _, count := range metricNames {
		require.Equal(t, valuesPerMetric, count)
	}
}

func TestStreamMetrics_ProcessorError(t *testing.T) {
	jsonData := `{
		"ToolID": "test-tool",
		"MetricValues": [
			{
				"Name": "temperature",
				"Values": [
					{"t": 1, "v": 1.0, "a": ""},
					{"t": 2, "v": 2.0, "a": "error_trigger"},
					{"t": 3, "v": 3.0, "a": ""}
				]
			}
		]
	}`

	reader := strings.NewReader(jsonData)
	config := StreamConfig{
		TopLevelKeys:    []string{"ToolID"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	count := 0
	_, err := StreamMetrics(reader, config, func(_ string, raw json.RawMessage) error {
		var value DataPoint
		_ = json.Unmarshal(raw, &value)
		count++

		if value.A == "error_trigger" {
			return fmt.Errorf("triggered error")
		}

		return nil
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "triggered error")
	require.Equal(t, 2, count)
}

func TestStreamMetrics_InvalidConfig(t *testing.T) {
	reader := strings.NewReader(`{}`)

	tests := []struct {
		name   string
		config StreamConfig
		errMsg string
	}{
		{
			name: "empty MetricsArrayKey",
			config: StreamConfig{
				MetricsArrayKey: "",
				MetricNameKey:   "Name",
				ValuesArrayKey:  "Values",
			},
			errMsg: "MetricsArrayKey cannot be empty",
		},
		{
			name: "empty MetricNameKey",
			config: StreamConfig{
				MetricsArrayKey: "MetricValues",
				MetricNameKey:   "",
				ValuesArrayKey:  "Values",
			},
			errMsg: "MetricNameKey cannot be empty",
		},
		{
			name: "empty ValuesArrayKey",
			config: StreamConfig{
				MetricsArrayKey: "MetricValues",
				MetricNameKey:   "Name",
				ValuesArrayKey:  "",
			},
			errMsg: "ValuesArrayKey cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := StreamMetrics(reader, tt.config, func(string, json.RawMessage) error {
				return nil
			})

			require.Error(t, err)
			require.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestStreamMetrics_InvalidJSON(t *testing.T) {
	tests := []struct {
		name     string
		jsonData string
		errMsg   string
	}{
		{
			name:     "invalid json syntax",
			jsonData: `{invalid}`,
			errMsg:   "failed to read key",
		},
		{
			name: "missing metric name",
			jsonData: `{
				"MetricValues": [
					{
						"Values": [{"t": 1, "v": 1.0}]
					}
				]
			}`,
			errMsg: "metric name not found",
		},
		{
			name: "not an object",
			jsonData: `[
				{"Name": "test", "Values": []}
			]`,
			errMsg: "expected '{'",
		},
	}

	config := StreamConfig{
		TopLevelKeys:    []string{"ToolID"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.jsonData)
			_, err := StreamMetrics(reader, config, func(string, json.RawMessage) error {
				return nil
			})

			require.Error(t, err)
			require.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestStreamMetrics_ExtraFieldsIgnored(t *testing.T) {
	jsonData := `{
		"ToolID": "test-tool",
		"ExtraField1": "ignored",
		"ExtraField2": 12345,
		"MetricValues": [
			{
				"Name": "temperature",
				"ExtraMetricField": "also ignored",
				"Values": [
					{"t": 1, "v": 1.0, "a": "test"}
				]
			}
		]
	}`

	reader := strings.NewReader(jsonData)
	config := StreamConfig{
		TopLevelKeys:    []string{"ToolID"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	count := 0
	metadata, err := StreamMetrics(reader, config, func(metricName string, _ json.RawMessage) error {
		count++
		require.Equal(t, "temperature", metricName)

		return nil
	})

	require.NoError(t, err)
	require.Equal(t, "test-tool", metadata["ToolID"])
	require.NotContains(t, metadata, "ExtraField1")
	require.NotContains(t, metadata, "ExtraField2")
	require.Equal(t, 1, count)
}

func TestStreamMetrics_MultipleMetricsWithDifferentValueCounts(t *testing.T) {
	jsonData := `{
		"ToolID": "multi-test",
		"MetricValues": [
			{
				"Name": "metric1",
				"Values": [
					{"t": 1, "v": 1.0, "a": ""}
				]
			},
			{
				"Name": "metric2",
				"Values": [
					{"t": 1, "v": 1.0, "a": ""},
					{"t": 2, "v": 2.0, "a": ""},
					{"t": 3, "v": 3.0, "a": ""}
				]
			},
			{
				"Name": "metric3",
				"Values": [
					{"t": 1, "v": 1.0, "a": ""},
					{"t": 2, "v": 2.0, "a": ""}
				]
			}
		]
	}`

	reader := strings.NewReader(jsonData)
	config := StreamConfig{
		TopLevelKeys:    []string{"ToolID"},
		MetricsArrayKey: "MetricValues",
		MetricNameKey:   "Name",
		ValuesArrayKey:  "Values",
	}

	metricCounts := make(map[string]int)

	metadata, err := StreamMetrics(reader, config, func(metricName string, _ json.RawMessage) error {
		metricCounts[metricName]++

		return nil
	})

	require.NoError(t, err)
	require.Equal(t, "multi-test", metadata["ToolID"])
	require.Equal(t, 1, metricCounts["metric1"])
	require.Equal(t, 3, metricCounts["metric2"])
	require.Equal(t, 2, metricCounts["metric3"])
}

func TestStreamConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  StreamConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: StreamConfig{
				TopLevelKeys:    []string{"ToolID"},
				MetricsArrayKey: "MetricValues",
				MetricNameKey:   "Name",
				ValuesArrayKey:  "Values",
			},
			wantErr: false,
		},
		{
			name: "empty MetricsArrayKey",
			config: StreamConfig{
				MetricNameKey:  "Name",
				ValuesArrayKey: "Values",
			},
			wantErr: true,
			errMsg:  "MetricsArrayKey cannot be empty",
		},
		{
			name: "empty MetricNameKey",
			config: StreamConfig{
				MetricsArrayKey: "MetricValues",
				ValuesArrayKey:  "Values",
			},
			wantErr: true,
			errMsg:  "MetricNameKey cannot be empty",
		},
		{
			name: "empty ValuesArrayKey",
			config: StreamConfig{
				MetricsArrayKey: "MetricValues",
				MetricNameKey:   "Name",
			},
			wantErr: true,
			errMsg:  "ValuesArrayKey cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
