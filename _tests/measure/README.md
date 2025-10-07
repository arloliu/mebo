# Compression Ratio Measurement Tool

## Quick Start

```bash
cd _tests/measure
go run . -metrics 200 -max-points 200 -value-jitter 5 -ts-jitter 2
```

## Overview

This tool measures the relationship between **points-per-metric (PPM)** and **bytes-per-point (BPP)** for Delta+Gorilla encoding. It helps derive a mathematical formula to predict compression efficiency in production environments.

## Features

- 🎯 **Consistent Test Data**: Generate once, test multiple configurations fairly
- 📊 **Comprehensive Analysis**: Statistical analysis and formula fitting
- 📈 **Formula Derivation**: Derive predictive model for production planning
- 💾 **CSV Export**: Export results for external analysis (Excel, Python, R)
- ⚡ **Fast**: Completes in < 10 seconds for default settings
- 🔁 **Reproducible**: Fixed seed ensures consistent results

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-metrics` | 200 | Number of metrics to generate |
| `-max-points` | 200 | Maximum points per metric to test |
| `-value-jitter` | 5.0 | Value jitter percentage (e.g., 5 = 5%) |
| `-ts-jitter` | 2.0 | Timestamp jitter percentage (e.g., 2 = 2%) |
| `-output` | "" | Optional CSV output file |
| `-verbose` | false | Enable verbose output |

## How It Works

1. **Generate Test Data**: Create consistent dataset with max points per metric
2. **Measure Incrementally**: Test with 1, 2, 5, 10, 20, 50, 100, 150, 200 points per metric
3. **Fair Comparison**: Use same data (sliced) for all measurements
4. **Statistical Analysis**: Calculate mean, median, std dev, and fit formulas
5. **Formula Fitting**: Test logarithmic, power, hyperbolic, and exponential models
6. **Best Fit Selection**: Choose model with highest R² and lowest RMSE
7. **Generate Report**: Display results, analysis, and recommendations

## Measurement Results (Default Settings)

### Configuration
- **Metrics**: 200
- **Max Points**: 200
- **Value Jitter**: 5.0%
- **Timestamp Jitter**: 2.0%
- **Encoding**: Delta + Gorilla (no compression)

### Raw Measurements

| Points/Metric | Total Points | Blob Size | Bytes/Point | Compression | Savings |
|---------------|--------------|-----------|-------------|-------------|---------|
| 1 | 200 | 6,432 | 32.16 | 1.00x | -101.0% |
| 2 | 400 | 9,631 | 24.08 | 1.00x | -50.5% |
| 5 | 1,000 | 15,441 | 15.44 | 1.25x | 3.5% |
| 10 | 2,000 | 24,968 | 12.48 | 1.41x | 22.0% |
| 20 | 4,000 | 43,794 | 10.95 | 1.54x | 31.6% |
| 50 | 10,000 | 100,303 | 10.03 | 1.63x | 37.3% |
| 100 | 20,000 | 195,228 | 9.76 | 1.66x | 39.0% |
| 150 | 30,000 | 290,580 | 9.69 | 1.66x | 39.5% |
| 200 | 40,000 | 386,292 | 9.66 | 1.67x | 39.6% |

### Statistical Summary

- **Minimum BPP**: 9.66 bytes/point (at 200 PPM)
- **Maximum BPP**: 32.16 bytes/point (at 1 PPM)
- **Average BPP**: 14.92 bytes/point
- **Median BPP**: 10.95 bytes/point
- **Std Deviation**: 7.52

### Derived Formula

**Best Fit: Hyperbolic Model**

```
BPP = 9.98 + 23.50 / PPM
```

**Model Performance:**
- **R²**: 0.9829 (excellent fit)
- **RMSE**: 0.98 bytes/point
- **Prediction Accuracy**:
  - 1 PPM: 4.1% error
  - 10 PPM: 1.3% error
  - 100 PPM: 4.6% error
  - 200 PPM: 4.5% error

**Alternative Models Tested:**
- Logarithmic: R²=0.7676, RMSE=3.62
- Power: R²=0.8486, RMSE=2.93
- Hyperbolic: R²=0.9829, RMSE=0.98 ✅ **Winner**

## Key Insights

### Why This Matters

Understanding the PPM vs BPP relationship helps:

1. **Storage Planning**: Predict storage costs for production workloads
2. **Configuration Tuning**: Choose optimal blob sizes
3. **Cost Optimization**: Balance write frequency vs storage efficiency
4. **Capacity Planning**: Estimate infrastructure requirements

### Production Recommendations

Based on the formula `BPP = 9.98 + 23.50 / PPM`:

#### Efficiency Zones

| Zone | PPM Range | BPP Range | Efficiency | Use Case |
|------|-----------|-----------|------------|----------|
| **Poor** | 1-5 | 14-32 | ❌ Avoid | - |
| **Moderate** | 6-20 | 10-14 | ⚠️ Acceptable | Real-time, high freshness |
| **Good** | 21-100 | 9.8-10.2 | ✅ Recommended | Balanced |
| **Optimal** | 101-200+ | 9.6-9.8 | ✅ Best | Historical, batch |

#### Guidelines

- ✅ **Use at least 20 points per metric** (BPP < 11)
- ✅ **Optimal range**: 50-150 points per metric
- ⚠️ **Diminishing returns** after 150 points per metric
- ❌ **Avoid**: 1-5 points per metric (BPP > 14)

#### Real-World Examples

**For 1 metric @ 1 point/second (86,400 points/day):**

| PPM | Write Interval | Blobs/Day | BPP | Storage/Day |
|-----|----------------|-----------|-----|-------------|
| 60 | Every 60s | 1,440 | 10.4 | 899 KB |
| 300 | Every 5min | 288 | 10.1 | 873 KB |
| 1000 | Every 16min | 86.4 | 10.0 | 864 KB |
| 3600 | Every hour | 24 | 9.98 | 862 KB |

**For 10,000 metrics:**
- PPM=60: 8.99 GB/day
- PPM=1000: 8.64 GB/day
- **Savings**: 350 MB/day (4%)

**Trade-off**: Freshness (60s) vs Efficiency (16min)

