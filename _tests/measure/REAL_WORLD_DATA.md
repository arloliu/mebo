# Real-World Data Input Implementation

This document describes the real-world data input feature added to the Compression Ratio Measurement Tool.

## Overview

The tool now accepts real-world time-series data in JSON format as input, allowing users to measure compression efficiency on actual production data instead of simulated data.

## Features Implemented

### 1. Input Data Parsing (`input.go`)

- **Column-based format**: Arrays of timestamps and values per metric
- **Row-based format**: Array of timestamp-value point objects per metric
- **Dual metric identification**: Support for both `id` (uint64) and `name` (string)
- **Auto-detection**: Automatically detects format based on JSON structure
- **Time unit conversion**: Converts timestamps from s/ms/us/ns to microseconds
- **Validation**: Ensures monotonic timestamps and proper data structure

### 2. Data Resampling (`resampler.go`)

- **Automatic point count calculation**: Generates appropriate test points based on data size
- **Resampling strategies**: First N points and evenly distributed sampling
- **Normalization**: Ensures all metrics have consistent point counts

### 3. CLI Integration (`main.go`)

- **`-input-file` flag**: Specify JSON input file path
- **`-time-unit` flag**: Specify timestamp unit (s, ms, us, ns), defaults to microseconds
- **Conditional logic**: Automatically switches between simulated and real-world data
- **Backward compatibility**: Existing simulated data mode still works

### 4. Configuration Display (`reporter.go`)

- **Data source display**: Shows whether data is simulated or from file
- **Time unit display**: Shows timestamp unit for real-world data
- **Conditional formatting**: Different display for simulated vs real-world data

## JSON Format Specification

### Column-based Format

```json
{
  "metrics": [
    {
      "name": "metric.name",
      "timestamps": [1700000001000000, 1700000002000000, ...],
      "values": [100.5, 101.2, ...]
    },
    {
      "id": 12345678901234,
      "timestamps": [1700000001000000, 1700000002000000, ...],
      "values": [200.5, 201.2, ...]
    }
  ]
}
```

### Row-based Format

```json
{
  "metrics": [
    {
      "name": "metric.name",
      "points": [
        {"timestamp": 1700000001000000, "value": 100.5},
        {"timestamp": 1700000002000000, "value": 101.2}
      ]
    },
    {
      "id": 12345678901234,
      "points": [
        {"timestamp": 1700000001000000, "value": 200.5},
        {"timestamp": 1700000002000000, "value": 201.2}
      ]
    }
  ]
}
```

## Rules and Constraints

1. **Metric Identification**: Each metric must have either `id` (uint64) or `name` (string), not both
2. **Timestamp Monotonicity**: Timestamps must be monotonically increasing within each metric
3. **Data Consistency**: In column-based format, timestamps and values arrays must have same length
4. **Time Units**: Supported units are `s` (seconds), `ms` (milliseconds), `us` (microseconds), `ns` (nanoseconds)
5. **Default Time Unit**: Microseconds (us) is the default

## Usage Examples

### Basic Usage
```bash
# With default microsecond timestamps
go run . -input-file metrics.json

# With millisecond timestamps
go run . -input-file metrics.json -time-unit ms

# With seconds timestamps and verbose output
go run . -input-file metrics.json -time-unit s -verbose
```

### With CSV Export
```bash
go run . -input-file production_metrics.json -output analysis.csv
```

## Implementation Details

### File Structure
- `input.go`: Input parsing and loading (320 lines)
- `resampler.go`: Data resampling logic (213 lines)
- `generator.go`: Updated Config struct
- `reporter.go`: Updated configuration display
- `main.go`: CLI flag handling and integration

### Key Functions
- `LoadInputData(filename, timeUnit)`: Main entry point for loading data
- `loadColumnBasedData()`: Parse column-based format
- `loadRowBasedData()`: Parse row-based format
- `convertToMicroseconds()`: Time unit conversion
- `ValidateTimeUnit()`: Time unit validation
- `CalculateTestPoints()`: Generate appropriate test point counts
- `ResampleTestData()`: Resample data to different point counts

## Testing

The implementation has been tested with:
1. ✅ Column-based format with metric names
2. ✅ Column-based format with metric IDs
3. ✅ Row-based format with metric names
4. ✅ Row-based format with metric IDs
5. ✅ Different time units (s, ms, us, ns)
6. ✅ Invalid time unit rejection
7. ✅ Backward compatibility with simulated data

## Sample Test Files

Three test files are included:
- `test_data_column.json`: Column-based format with metric names
- `test_data_row.json`: Row-based format with mixed name and ID
- `test_data_ms.json`: Data with millisecond timestamps

## Error Handling

The implementation includes comprehensive error handling for:
- File not found
- Invalid JSON format
- Missing required fields
- Invalid time units
- Non-monotonic timestamps
- Mismatched array lengths (column format)
- Empty metrics or data points

## Future Enhancements

Potential improvements:
1. Support for additional input formats (CSV, Parquet)
2. More sophisticated resampling strategies (time-weighted, aggregation)
3. Support for irregular metric point counts
4. Automatic time unit detection
5. Data quality validation and reporting

