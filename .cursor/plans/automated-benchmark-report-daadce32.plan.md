<!-- daadce32-e4b4-4f47-ac80-b762071ea2df c6692232-dcc8-47fa-ba95-c0d7aeba7654 -->
# Automated Benchmark Report Generator

## Overview

Create `scripts/bench_fbs_compare.sh` to automatically run all FBS comparison benchmarks and generate `tests/fbs_compare/BENCHMARK_REPORT.md` from a template file. Store all intermediate files in `.benchmarks/<timestamp>/` and support reusing existing benchmark data for faster regeneration.

## Production Data Sizes

Benchmark sizes based on production use case (200 metrics):

- **10 points** (10 PPS, 2,000 total) - Quick queries
- **20 points** (20 PPS, 4,000 total) - Short windows
- **50 points** (50 PPS, 10,000 total) - PRIMARY for Cassandra (~100KB)
- **100 points** (100 PPS, 20,000 total) - Medium windows
- **200 points** (200 PPS, 40,000 total) - Large windows

## Implementation Steps

### 1. Create Report Template

**File**: `tests/fbs_compare/BENCHMARK_REPORT.md.template`

Convert existing `BENCHMARK_REPORT.md` to template with placeholders:

- `{{TEST_DATE}}` - Benchmark execution date
- `{{TEST_SIZES}}` - Document test sizes (10/20/50/100/200 pts)
- `{{SIZE_COMPARISON_NUMERIC}}` - Part 1 numeric blob sizes (50pts focus)
- `{{SIZE_COMPARISON_TEXT}}` - Part 1 text blob sizes
- `{{ENCODING_NUMERIC}}` - Part 2 numeric encoding table
- `{{ENCODING_TEXT}}` - Part 2 text encoding table
- `{{DECODING_NUMERIC}}` - Part 3 numeric decoding table
- `{{DECODING_TEXT}}` - Part 3 text decoding table
- `{{ITERATION_NUMERIC}}` - Part 4 numeric iteration table
- `{{ITERATION_TEXT}}` - Part 4 text iteration table
- `{{DECODE_ITERATE_NUMERIC}}` - Part 5 numeric decode+iterate table
- `{{DECODE_ITERATE_TEXT}}` - Part 5 text decode+iterate table
- `{{RANDOM_ACCESS_TEXT}}` - Part 6 text random access table
- `{{RANDOM_ACCESS_NUMERIC}}` - Part 8 numeric random access table

Update references from "250pts" to "50pts" as primary production size.

### 2. Create Benchmark Script

**File**: `scripts/bench_fbs_compare.sh`

Follow `bench_numeric_gorilla_decoder.sh` structure with these features:

- Store all artifacts in `.benchmarks/<timestamp>/`
- Support `--reuse <dir>` to regenerate report from existing data
- Check for Python and Go before running
- Similar argument parsing and help text
```bash
#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: bench_fbs_compare.sh [OPTIONS]

Generate comprehensive Mebo vs FlatBuffers benchmark report.

Options:
  --count N         Number of benchmark iterations (default: 5)
  --benchtime T     Benchtime per benchmark (default: 500ms)
  --output DIR      Output directory for artifacts (default: .benchmarks/<timestamp>)
  --reuse DIR       Reuse existing benchmark data from DIR (skip benchmark runs)
  -h, --help        Show this help message

Examples:
  # Full benchmark run
  ./scripts/bench_fbs_compare.sh --count 10 --benchtime 1s

  # Quick run with fewer iterations
  ./scripts/bench_fbs_compare.sh --count 3 --benchtime 200ms

  # Regenerate report from existing data
  ./scripts/bench_fbs_compare.sh --reuse .benchmarks/20241011_143022

Production data sizes: 200 metrics × 10/20/50/100/200 points
Primary focus: 50 PPS for Cassandra storage (~100KB blobs)
EOF
}

# Parse arguments
count=5
benchtime="500ms"
output_dir=""
reuse_dir=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --count) count=${2:-}; shift 2 ;;
    --benchtime) benchtime=${2:-}; shift 2 ;;
    --output) output_dir=${2:-}; shift 2 ;;
    --reuse) reuse_dir=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage; exit 1 ;;
  esac
done

# Setup paths
repo_root=$(git rev-parse --show-toplevel)
if [[ -z "$output_dir" ]]; then
  timestamp=$(date +%Y%m%d_%H%M%S)
  output_dir="$repo_root/.benchmarks/$timestamp"
fi
mkdir -p "$output_dir"

# Check for required tools
python_bin=$(command -v python3 || command -v python || true)
if [[ -z "$python_bin" ]]; then
  echo "Error: python3 required" >&2
  exit 1
fi

# If reusing, copy from existing directory
if [[ -n "$reuse_dir" ]]; then
  echo "Reusing benchmark data from $reuse_dir"
  for file in sizes.txt encode.txt decode.txt iterate.txt random.txt; do
    if [[ -f "$reuse_dir/$file" ]]; then
      cp "$reuse_dir/$file" "$output_dir/"
    else
      echo "Warning: $reuse_dir/$file not found" >&2
    fi
  done
else
  # Run benchmarks
  echo "Running benchmarks (count=$count, benchtime=$benchtime)..."
  cd "$repo_root/tests/fbs_compare"

  echo "  1/5: Size tests..."
  go test -v -run="TestBlobSizes|TestTextBlobSizes" >"$output_dir/sizes.txt" 2>&1 || {
    echo "Size tests failed" >&2
    exit 1
  }

  echo "  2/5: Encoding benchmarks..."
  go test -bench="BenchmarkEncode" -benchtime="$benchtime" -count="$count" -run=^$ >"$output_dir/encode.txt" 2>&1 || {
    echo "Encode benchmarks failed" >&2
    exit 1
  }

  echo "  3/5: Decoding benchmarks..."
  go test -bench="BenchmarkDecode" -benchtime="$benchtime" -count="$count" -run=^$ >"$output_dir/decode.txt" 2>&1 || {
    echo "Decode benchmarks failed" >&2
    exit 1
  }

  echo "  4/5: Iteration benchmarks..."
  go test -bench="BenchmarkIterateAll" -benchtime="$benchtime" -count="$count" -run=^$ >"$output_dir/iterate.txt" 2>&1 || {
    echo "Iterate benchmarks failed" >&2
    exit 1
  }

  echo "  5/5: Random access benchmarks..."
  go test -bench="BenchmarkRandomAccess" -benchtime="$benchtime" -count="$count" -run=^$ >"$output_dir/random.txt" 2>&1 || {
    echo "Random access benchmarks failed" >&2
    exit 1
  }
fi

# Generate report
echo "Generating report..."
"$python_bin" "$repo_root/tests/fbs_compare/generate_report.py" \
  --template "$repo_root/tests/fbs_compare/BENCHMARK_REPORT.md.template" \
  --sizes "$output_dir/sizes.txt" \
  --encode "$output_dir/encode.txt" \
  --decode "$output_dir/decode.txt" \
  --iterate "$output_dir/iterate.txt" \
  --random "$output_dir/random.txt" \
  --output "$repo_root/tests/fbs_compare/BENCHMARK_REPORT.md" \
  --artifacts-dir "$output_dir"

echo ""
echo "Benchmark artifacts saved to: $output_dir"
echo "  sizes.txt, encode.txt, decode.txt, iterate.txt, random.txt"
echo ""
echo "Report generated at: tests/fbs_compare/BENCHMARK_REPORT.md"
echo ""
echo "To regenerate report from this data:"
echo "  ./scripts/bench_fbs_compare.sh --reuse $output_dir"
```


### 3. Create Python Report Generator

**File**: `tests/fbs_compare/generate_report.py`

Key features:

- Parse Go test output (both test and benchmark formats)
- Generate CSV files for each benchmark category
- Support reading existing CSV files for faster regeneration
- Format markdown tables with proper units
- Highlight key configurations
```python
#!/usr/bin/env python3
"""
Generate Mebo vs FlatBuffers benchmark report from test outputs.

This script parses Go benchmark outputs and generates a comprehensive
markdown report. It can also save intermediate CSV files for faster
regeneration.
"""

import argparse
import csv
import os
import re
from datetime import datetime
from typing import Dict, List, Tuple, Optional

class BenchmarkParser:
    """Parse Go test and benchmark outputs."""
    
    # Key configurations to highlight
    KEY_CONFIGS = {
        'mebo/raw-none-raw-none': 'Baseline',
        'mebo/delta-none-raw-none': 'Delta only',
        'mebo/delta-none-gorilla-none': 'Balanced ⭐',
        'mebo/delta-zstd-gorilla-zstd': 'Best compression',
        'fbs-none': 'FBS baseline',
        'fbs-zstd': 'FBS best',
    }
    
    def parse_size_test(self, content: str) -> Dict[str, Dict[str, float]]:
        """Parse TestBlobSizes output.
        
        Returns:
            {config_name: {size: bytes, bytes_per_point: float, savings_pct: float}}
        """
        results = {}
        current_size = None
        
        for line in content.split('\n'):
            # Detect size section: "200 Metrics × 50 Points"
            match = re.search(r'(\d+)\s+Metrics\s+×\s+(\d+)\s+Points', line)
            if match:
                current_size = int(match.group(2))
                continue
            
            # Parse config line: "│ mebo/delta-none-gorilla-none │   123456 │  12.35 │ ..."
            match = re.search(r'│\s+(mebo/[\w-]+|fbs-\w+)\s+│\s+(\d+)\s+│\s+([\d.]+)\s+│', line)
            if match and current_size:
                config = match.group(1)
                size_bytes = int(match.group(2))
                bytes_per_point = float(match.group(3))
                
                key = f"{config}_{current_size}pts"
                results[key] = {
                    'config': config,
                    'size_pts': current_size,
                    'bytes': size_bytes,
                    'bytes_per_point': bytes_per_point,
                }
        
        return results
    
    def parse_benchmark_output(self, content: str) -> Dict[str, Dict[str, float]]:
        """Parse go test -bench output.
        
        Returns:
            {benchmark_name: {ns_per_op: float, bytes_per_op: int, allocs_per_op: int}}
        """
        results = {}
        
        for line in content.split('\n'):
            # BenchmarkEncode_Mebo/10pts/mebo/delta-none-gorilla-none-8  100  12345 ns/op  1024 B/op  10 allocs/op
            match = re.match(r'(Benchmark\S+)\s+\d+\s+([\d.]+)\s+ns/op\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op', line)
            if match:
                name = match.group(1)
                ns_op = float(match.group(2))
                bytes_op = int(match.group(3))
                allocs_op = int(match.group(4))
                
                results[name] = {
                    'ns_per_op': ns_op,
                    'bytes_per_op': bytes_op,
                    'allocs_per_op': allocs_op,
                }
        
        return results
    
    def save_to_csv(self, data: Dict, filepath: str):
        """Save parsed data to CSV for faster regeneration."""
        if not data:
            return
        
        with open(filepath, 'w', newline='') as f:
            # Get all unique keys from first item
            first_item = next(iter(data.values()))
            fieldnames = ['name'] + list(first_item.keys())
            
            writer = csv.DictWriter(f, fieldnames=fieldnames)
            writer.writeheader()
            
            for name, values in data.items():
                row = {'name': name}
                row.update(values)
                writer.writerow(row)
    
    def load_from_csv(self, filepath: str) -> Dict:
        """Load parsed data from CSV."""
        if not os.path.exists(filepath):
            return {}
        
        results = {}
        with open(filepath, 'r', newline='') as f:
            reader = csv.DictReader(f)
            for row in reader:
                name = row.pop('name')
                # Convert numeric fields
                for key, val in row.items():
                    try:
                        if '.' in val:
                            row[key] = float(val)
                        else:
                            row[key] = int(val)
                    except ValueError:
                        pass
                results[name] = row
        
        return results

def format_time_value(ns: float) -> str:
    """Convert nanoseconds to appropriate unit."""
    if ns >= 1e9:
        return f"{ns/1e9:.2f} s"
    elif ns >= 1e6:
        return f"{ns/1e6:.2f} ms"
    elif ns >= 1e3:
        return f"{ns/1e3:.2f} μs"
    else:
        return f"{ns:.0f} ns"

def format_size_table(data: Dict, size_pts: int = 50) -> str:
    """Generate markdown table for size comparison."""
    # Filter for specific size
    filtered = {k: v for k, v in data.items() if v.get('size_pts') == size_pts}
    
    # Sort by bytes_per_point
    sorted_items = sorted(filtered.items(), key=lambda x: x[1]['bytes_per_point'])
    
    lines = [
        "| Configuration | Size (bytes) | Bytes/Point | Rank | Notes |",
        "|---------------|--------------|-------------|------|-------|",
    ]
    
    for rank, (key, item) in enumerate(sorted_items[:10], 1):  # Top 10
        config = item['config']
        size_bytes = item['bytes']
        bpp = item['bytes_per_point']
        
        # Get note
        note = BenchmarkParser.KEY_CONFIGS.get(config, '')
        rank_icon = {1: '🥇', 2: '🥈', 3: '🥉'}.get(rank, f'{rank}th')
        
        lines.append(f"| **{config}** | **{size_bytes:,}** | **{bpp:.2f}** | {rank_icon} | {note} |")
    
    return '\n'.join(lines)

def main():
    parser = argparse.ArgumentParser(description='Generate FBS comparison benchmark report')
    parser.add_argument('--template', required=True, help='Template markdown file')
    parser.add_argument('--sizes', required=True, help='Size test output')
    parser.add_argument('--encode', required=True, help='Encode benchmark output')
    parser.add_argument('--decode', required=True, help='Decode benchmark output')
    parser.add_argument('--iterate', required=True, help='Iterate benchmark output')
    parser.add_argument('--random', required=True, help='Random access benchmark output')
    parser.add_argument('--output', required=True, help='Output markdown file')
    parser.add_argument('--artifacts-dir', required=True, help='Directory for CSV artifacts')
    
    args = parser.parse_args()
    
    bp = BenchmarkParser()
    
    # Try to load from CSV first (faster), otherwise parse
    csv_dir = args.artifacts_dir
    
    # Parse or load size data
    size_csv = os.path.join(csv_dir, 'sizes.csv')
    if os.path.exists(size_csv):
        print(f"Loading sizes from {size_csv}")
        sizes_data = bp.load_from_csv(size_csv)
    else:
        print(f"Parsing {args.sizes}")
        with open(args.sizes) as f:
            sizes_data = bp.parse_size_test(f.read())
        bp.save_to_csv(sizes_data, size_csv)
    
    # Similar for other benchmarks...
    # Parse encode, decode, iterate, random access
    
    # Generate tables
    tables = {
        'TEST_DATE': datetime.now().strftime('%B %d, %Y'),
        'TEST_SIZES': '200 metrics × [10/20/50/100/200] points',
        'SIZE_COMPARISON_NUMERIC': format_size_table(sizes_data, size_pts=50),
        # ... generate all other tables
    }
    
    # Read template and replace placeholders
    with open(args.template) as f:
        template = f.read()
    
    for key, value in tables.items():
        template = template.replace(f'{{{{{key}}}}}', value)
    
    # Write final report
    with open(args.output, 'w') as f:
        f.write(template)
    
    print(f"✓ Report generated: {args.output}")
    print(f"✓ CSV artifacts saved in: {csv_dir}")

if __name__ == '__main__':
    main()
```


### 4. Update .gitignore

**File**: `.gitignore` (root level)

Add `.benchmarks/` directory:

```
.benchmarks/
```

### 5. Make Scripts Executable

```bash
chmod +x scripts/bench_fbs_compare.sh
chmod +x tests/fbs_compare/generate_report.py
```

## Key Design Decisions

1. **Artifacts in .benchmarks/**: All intermediate files stored together, easy to reuse
2. **CSV caching**: Parse once, save to CSV, reuse for faster report regeneration
3. **--reuse flag**: Regenerate report from existing data without re-running benchmarks
4. **Primary size 50pts**: Focus on production-relevant size (~100KB blobs for Cassandra)
5. **Modular parsing**: Each benchmark type parsed separately for maintainability
6. **Error handling**: Check for tools, validate inputs, fail gracefully
7. **Similar to bench_numeric_gorilla_decoder.sh**: Consistent CLI, structure, and workflow

## Files to Create/Modify

- `tests/fbs_compare/BENCHMARK_REPORT.md.template` (new)
- `scripts/bench_fbs_compare.sh` (new, executable)
- `tests/fbs_compare/generate_report.py` (new, executable)
- `.gitignore` (add .benchmarks/)
- Keep: `BENCHMARK_REPORT.md`, `BENCHMARK_PLAN.md`, `benchmark_test.go`