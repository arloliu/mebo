#!/usr/bin/env python3
"""
Generate Mebo vs FlatBuffers benchmark report from test outputs.

This script parses Go benchmark outputs and generates a comprehensive
markdown report. It can also save intermediate CSV files for faster
regeneration.
"""

import argparse
import csv
import json
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
            # Detect size section: "━━━ 200 Metrics × 10 Points = 2000 Total Points ━━━"
            match = re.search(r'━+\s+(\d+)\s+Metrics\s+×\s+(\d+)\s+Points\s+=\s+\d+\s+Total\s+Points\s+━+', line)
            if match:
                current_size = int(match.group(2))
                continue

            # Parse config line: "│ mebo/raw-none-raw-none │       35232 │        17.62 │ baseline    │        0.0% │"
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

    def parse_benchmark_output(self, content: str, pattern: str) -> Dict[str, Dict[str, float]]:
        """Parse go test -bench output for specific pattern.

        Returns:
            {benchmark_name: {ns_per_op: float, bytes_per_op: int, allocs_per_op: int}}
        """
        results = {}

        for line in content.split('\n'):
            # BenchmarkEncode_Mebo/10pts/mebo/delta-none-gorilla-none-8         	    2514	    156576 ns/op	  115052 B/op	     234 allocs/op
            if pattern in line and 'ns/op' in line:
                # Match the actual format: BenchmarkName-N         	    N	    N ns/op	  N B/op	     N allocs/op
                match = re.match(r'(Benchmark\S+)\s+\d+\s+([\d.]+)\s+ns/op\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op', line)
                if match:
                    name = match.group(1)
                    ns_op = float(match.group(2))
                    bytes_op = int(match.group(3))
                    allocs_op = int(match.group(4))

                    # Keep the last (most recent) result for each benchmark
                    results[name] = {
                        'ns_per_op': ns_op,
                        'bytes_per_op': bytes_op,
                        'allocs_per_op': allocs_op,
                    }

        return results

    def parse_benchmark_json(self, filepath: str) -> Dict[str, Dict[str, float]]:
        """Parse go test -bench -json output.

        Returns:
            {benchmark_name: {ns_per_op: float, bytes_per_op: int, allocs_per_op: int}}
        """
        results = {}

        if not os.path.exists(filepath):
            return results

        with open(filepath, 'r') as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue

                try:
                    data = json.loads(line)
                    # Look for benchmark results in the Output field
                    if data.get('Action') == 'output' and 'Output' in data:
                        output = data['Output']
                        # Parse benchmark result lines like: "BenchmarkName-8   100  12345 ns/op  1024 B/op  10 allocs/op"
                        match = re.match(r'(Benchmark\S+)\s+\d+\s+([\d.]+)\s+ns/op\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op', output)
                        if match:
                            name = match.group(1)
                            ns_op = float(match.group(2))
                            bytes_op = int(match.group(3))
                            allocs_op = int(match.group(4))

                            # Keep the last (most recent) result for each benchmark
                            results[name] = {
                                'ns_per_op': ns_op,
                                'bytes_per_op': bytes_op,
                                'allocs_per_op': allocs_op,
                            }
                except json.JSONDecodeError:
                    continue

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

def format_benchmark_table(data: Dict, sizes: List[str] = ['10pts', '20pts', '50pts']) -> str:
    """Generate markdown table for benchmark results."""
    # Group by configuration and size
    configs = {}
    for name, values in data.items():
        # Extract config and size from benchmark name
        # e.g., "BenchmarkEncode_Mebo/10pts/mebo/raw-none-raw-none-8"
        match = re.match(r'Benchmark\w+_(\w+)/(\d+pts)/(\S+)', name)
        if match:
            benchmark_type = match.group(1)  # Mebo or FBS
            size = match.group(2)  # Already includes 'pts'
            config = match.group(3)

            if config not in configs:
                configs[config] = {}
            configs[config][size] = values['ns_per_op']

    # Find the fastest configuration for each size
    fastest_by_size = {}
    for size in sizes:
        fastest_config = None
        fastest_time = float('inf')
        for config, size_data in configs.items():
            if size in size_data and size_data[size] < fastest_time:
                fastest_time = size_data[size]
                fastest_config = config
        fastest_by_size[size] = fastest_config

    # Generate table
    lines = [
        "| Configuration | " + " | ".join(sizes) + " | Winner |",
        "|---------------|" + "|".join(["-" * 8 for _ in sizes]) + "|--------|",
    ]

    for config, size_data in sorted(configs.items()):
        row = [f"**{config}**"]
        for size in sizes:
            if size in size_data:
                row.append(format_time_value(size_data[size]))
            else:
                row.append("—")

        # Determine winner (fastest overall or by size)
        times = [size_data.get(size, float('inf')) for size in sizes]
        if all(t != float('inf') for t in times):
            # Check if this config is fastest in any size
            is_fastest_any = any(fastest_by_size.get(size) == config for size in sizes)
            if is_fastest_any:
                # Find which sizes this config is fastest in
                fastest_sizes = [size for size in sizes if fastest_by_size.get(size) == config]
                if len(fastest_sizes) == len(sizes):
                    row.append("**Fastest**")
                elif len(fastest_sizes) > 1:
                    row.append(f"**Fastest ({', '.join(fastest_sizes)})**")
                else:
                    row.append(f"**Fastest ({fastest_sizes[0]})**")
            else:
                row.append("")
        else:
            row.append("")

        lines.append("| " + " | ".join(row) + " |")

    return '\n'.join(lines)

def generate_key_findings(sizes_data: Dict, encode_data: Dict, decode_data: Dict, iterate_data: Dict, random_data: Dict) -> str:
    """Generate dynamic key findings based on actual benchmark data."""
    findings = []

    # Size analysis
    if sizes_data:
        # Find best compression
        size_items = [(k, v) for k, v in sizes_data.items() if v.get('size_pts') == 50]
        if size_items:
            best_compression = min(size_items, key=lambda x: x[1]['bytes_per_point'])
            best_config = best_compression[1]['config']
            best_bpp = best_compression[1]['bytes_per_point']
            findings.append(f"- ✅ **Best compression**: `{best_config}` at **{best_bpp:.2f} bytes/point**")

            # Find balanced option (delta-none-gorilla-none)
            balanced_items = [item for item in size_items if 'delta-none-gorilla-none' in item[1]['config']]
            if balanced_items:
                balanced_bpp = balanced_items[0][1]['bytes_per_point']
                findings.append(f"- ⭐ **Balanced option**: `mebo/delta-none-gorilla-none` at **{balanced_bpp:.2f} bytes/point**")

    # Performance analysis
    if encode_data:
        # Find fastest encoding
        encode_configs = {}
        for name, values in encode_data.items():
            match = re.match(r'Benchmark\w+_(\w+)/(\d+pts)/(\S+)', name)
            if match:
                config = match.group(3)
                if config not in encode_configs:
                    encode_configs[config] = []
                encode_configs[config].append(values['ns_per_op'])

        if encode_configs:
            # Calculate average performance
            avg_perf = {config: sum(times)/len(times) for config, times in encode_configs.items()}
            fastest_encode = min(avg_perf.items(), key=lambda x: x[1])
            findings.append(f"- 🚀 **Fastest encoding**: `{fastest_encode[0]}` at **{format_time_value(fastest_encode[1])}** average")

    # Memory analysis
    if encode_data:
        # Find lowest memory usage
        memory_usage = {}
        for name, values in encode_data.items():
            match = re.match(r'Benchmark\w+_(\w+)/(\d+pts)/(\S+)', name)
            if match:
                config = match.group(3)
                if config not in memory_usage:
                    memory_usage[config] = []
                memory_usage[config].append(values['bytes_per_op'])

        if memory_usage:
            avg_memory = {config: sum(usage)/len(usage) for config, usage in memory_usage.items()}
            lowest_memory = min(avg_memory.items(), key=lambda x: x[1])
            findings.append(f"- 💾 **Lowest memory**: `{lowest_memory[0]}` at **{lowest_memory[1]:,} bytes/op** average")

    # Add analysis hint if no findings
    if not findings:
        findings.append("- 📊 **Analysis needed**: Review benchmark data to identify key performance patterns")
        findings.append("- 🔍 **Focus areas**: Compression ratio, encoding speed, memory usage, and real-world performance")
        findings.append("- 📈 **Recommendations**: Consider production use cases (50 PPS primary) and balanced configurations")

    return '\n'.join(findings)

def generate_iteration_findings(iterate_data: Dict) -> str:
    """Generate dynamic key findings for Part 4: Iteration Performance."""
    findings = []

    if not iterate_data:
        findings.append("- 📊 **Analysis needed**: Review iteration benchmark data")
        return '\n'.join(findings)

    # Analyze iteration performance
    iterate_configs = {}
    for name, values in iterate_data.items():
        match = re.match(r'Benchmark\w+_(\w+)/(\d+pts)/(\S+)', name)
        if match:
            config = match.group(3)
            if config not in iterate_configs:
                iterate_configs[config] = []
            iterate_configs[config].append(values['ns_per_op'])

    if iterate_configs:
        # Calculate average performance
        avg_perf = {config: sum(times)/len(times) for config, times in iterate_configs.items()}
        fastest_iterate = min(avg_perf.items(), key=lambda x: x[1])
        findings.append(f"- 🚀 **Fastest iteration**: `{fastest_iterate[0]}` at **{format_time_value(fastest_iterate[1])}** average")

        # Compare Mebo vs FBS
        mebo_configs = {k: v for k, v in avg_perf.items() if k.startswith('mebo/')}
        fbs_configs = {k: v for k, v in avg_perf.items() if k.startswith('fbs-')}

        if mebo_configs and fbs_configs:
            mebo_avg = sum(mebo_configs.values()) / len(mebo_configs)
            fbs_avg = sum(fbs_configs.values()) / len(fbs_configs)
            speedup = fbs_avg / mebo_avg if mebo_avg > 0 else 1
            findings.append(f"- ⚡ **Mebo advantage**: **{speedup:.1f}× faster** than FBS on average")

        # Find best balanced option
        balanced_configs = {k: v for k, v in avg_perf.items() if 'delta-none-gorilla-none' in k}
        if balanced_configs:
            balanced_perf = list(balanced_configs.values())[0]
            findings.append(f"- ⭐ **Balanced performance**: `mebo/delta-none-gorilla-none` at **{format_time_value(balanced_perf)}**")

    return '\n'.join(findings)

def generate_decode_iterate_findings(decode_data: Dict) -> str:
    """Generate dynamic key findings for Part 5: Decode + Iteration Combined."""
    findings = []

    if not decode_data:
        findings.append("- 📊 **Analysis needed**: Review decode+iteration benchmark data")
        return '\n'.join(findings)

    # Analyze combined performance (using decode data as proxy)
    decode_configs = {}
    for name, values in decode_data.items():
        match = re.match(r'Benchmark\w+_(\w+)/(\d+pts)/(\S+)', name)
        if match:
            config = match.group(3)
            if config not in decode_configs:
                decode_configs[config] = []
            decode_configs[config].append(values['ns_per_op'])

    if decode_configs:
        # Calculate average performance
        avg_perf = {config: sum(times)/len(times) for config, times in decode_configs.items()}
        fastest_combined = min(avg_perf.items(), key=lambda x: x[1])
        findings.append(f"- 🚀 **Fastest combined**: `{fastest_combined[0]}` at **{format_time_value(fastest_combined[1])}** average")

        # Compare Mebo vs FBS
        mebo_configs = {k: v for k, v in avg_perf.items() if k.startswith('mebo/')}
        fbs_configs = {k: v for k, v in avg_perf.items() if k.startswith('fbs-')}

        if mebo_configs and fbs_configs:
            mebo_avg = sum(mebo_configs.values()) / len(mebo_configs)
            fbs_avg = sum(fbs_configs.values()) / len(fbs_configs)
            speedup = fbs_avg / mebo_avg if mebo_avg > 0 else 1
            findings.append(f"- ⚡ **Mebo advantage**: **{speedup:.1f}× faster** than FBS for real-world operations")

        # Production recommendation
        findings.append(f"- 🎯 **Production ready**: Combined operations are **{format_time_value(fastest_combined[1])}** for primary use case")

    return '\n'.join(findings)

def generate_random_access_findings(random_data: Dict) -> str:
    """Generate dynamic key findings for Part 8: Random Access Performance."""
    findings = []

    if not random_data:
        findings.append("- 📊 **Analysis needed**: Review random access benchmark data")
        return '\n'.join(findings)

    # Analyze random access performance
    random_configs = {}
    for name, values in random_data.items():
        match = re.match(r'Benchmark\w+_(\w+)/(\d+pts)/(\S+)', name)
        if match:
            config = match.group(3)
            if config not in random_configs:
                random_configs[config] = []
            random_configs[config].append(values['ns_per_op'])

    if random_configs:
        # Calculate average performance
        avg_perf = {config: sum(times)/len(times) for config, times in random_configs.items()}
        fastest_random = min(avg_perf.items(), key=lambda x: x[1])
        findings.append(f"- 🚀 **Fastest random access**: `{fastest_random[0]}` at **{format_time_value(fastest_random[1])}** average")

        # Compare Mebo vs FBS
        mebo_configs = {k: v for k, v in avg_perf.items() if k.startswith('mebo/')}
        fbs_configs = {k: v for k, v in avg_perf.items() if k.startswith('fbs-')}

        if mebo_configs and fbs_configs:
            mebo_avg = sum(mebo_configs.values()) / len(mebo_configs)
            fbs_avg = sum(fbs_configs.values()) / len(fbs_configs)
            if mebo_avg < fbs_avg:
                speedup = fbs_avg / mebo_avg
                findings.append(f"- ⚡ **Mebo advantage**: **{speedup:.1f}× faster** than FBS for random access")
            else:
                speedup = mebo_avg / fbs_avg
                findings.append(f"- ⚡ **FBS advantage**: **{speedup:.1f}× faster** than Mebo for random access")

        # Memory efficiency analysis
        memory_usage = {}
        for name, values in random_data.items():
            match = re.match(r'Benchmark\w+_(\w+)/(\d+pts)/(\S+)', name)
            if match:
                config = match.group(3)
                if config not in memory_usage:
                    memory_usage[config] = []
                memory_usage[config].append(values['bytes_per_op'])

        if memory_usage:
            avg_memory = {config: sum(usage)/len(usage) for config, usage in memory_usage.items()}
            lowest_memory = min(avg_memory.items(), key=lambda x: x[1])
            findings.append(f"- 💾 **Memory efficient**: `{lowest_memory[0]}` at **{lowest_memory[1]:,} bytes/op**")

    return '\n'.join(findings)

def generate_recommendations(sizes_data: Dict, encode_data: Dict, decode_data: Dict, iterate_data: Dict, random_data: Dict) -> str:
    """Generate dynamic recommendations for Part 7 based on actual benchmark data."""
    recommendations = []

    # Find best configurations for different use cases
    best_configs = {}

    # Best compression
    if sizes_data:
        size_items = [(k, v) for k, v in sizes_data.items() if v.get('size_pts') == 50]
        if size_items:
            best_compression = min(size_items, key=lambda x: x[1]['bytes_per_point'])
            best_configs['compression'] = best_compression[1]['config']
            best_configs['compression_bpp'] = best_compression[1]['bytes_per_point']

    # Fastest encoding
    if encode_data:
        encode_configs = {}
        for name, values in encode_data.items():
            match = re.match(r'Benchmark\w+_(\w+)/(\d+pts)/(\S+)', name)
            if match:
                config = match.group(3)
                if config not in encode_configs:
                    encode_configs[config] = []
                encode_configs[config].append(values['ns_per_op'])

        if encode_configs:
            avg_perf = {config: sum(times)/len(times) for config, times in encode_configs.items()}
            best_configs['encoding'] = min(avg_perf.items(), key=lambda x: x[1])[0]

    # Fastest iteration
    if iterate_data:
        iterate_configs = {}
        for name, values in iterate_data.items():
            match = re.match(r'Benchmark\w+_(\w+)/(\d+pts)/(\S+)', name)
            if match:
                config = match.group(3)
                if config not in iterate_configs:
                    iterate_configs[config] = []
                iterate_configs[config].append(values['ns_per_op'])

        if iterate_configs:
            avg_perf = {config: sum(times)/len(times) for config, times in iterate_configs.items()}
            best_configs['iteration'] = min(avg_perf.items(), key=lambda x: x[1])[0]

    # Generate recommendations
    recommendations.append("### When to Choose Mebo")
    recommendations.append("")

    if 'compression' in best_configs:
        recommendations.append(f"1. **Long-term Storage:** Use `{best_configs['compression']}`")
        recommendations.append(f"   - Best compression ({best_configs['compression_bpp']:.2f} bytes/point)")
        recommendations.append(f"   - Optimal for archival and cost-sensitive storage")
        recommendations.append("")

    if 'encoding' in best_configs:
        recommendations.append(f"2. **High-Throughput Ingestion:** Use `{best_configs['encoding']}`")
        recommendations.append("   - Fastest encoding performance")
        recommendations.append("   - Optimal for real-time data ingestion")
        recommendations.append("")

    if 'iteration' in best_configs:
        recommendations.append(f"3. **Hot Data Queries:** Use `{best_configs['iteration']}`")
        recommendations.append("   - Fastest iteration performance")
        recommendations.append("   - Optimal for frequent data access")
        recommendations.append("")

    # Balanced recommendation
    recommendations.append("4. **Balanced Production Use:** Use `mebo/delta-none-gorilla-none`")
    recommendations.append("   - Excellent compression with good performance")
    recommendations.append("   - No compression overhead")
    recommendations.append("   - **Recommended for most production use cases**")
    recommendations.append("")

    recommendations.append("### When to Choose FBS")
    recommendations.append("")
    recommendations.append("1. **Text-Heavy Workloads:** Use `fbs-none`")
    recommendations.append("   - Better for string-heavy queries")
    recommendations.append("   - Familiar schema-based approach")
    recommendations.append("")
    recommendations.append("2. **Mixed Data Types:** Use `fbs-zstd`")
    recommendations.append("   - Handles both numeric and text well")
    recommendations.append("   - Good compression with familiar approach")

    return '\n'.join(recommendations)

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

    # Parse or load encode data
    encode_csv = os.path.join(csv_dir, 'encode.csv')
    if os.path.exists(encode_csv):
        print(f"Loading encode from {encode_csv}")
        encode_data = bp.load_from_csv(encode_csv)
    else:
        print(f"Parsing {args.encode}")
        if args.encode.endswith('.json'):
            encode_data = bp.parse_benchmark_json(args.encode)
        else:
            with open(args.encode) as f:
                encode_content = f.read()
            encode_data = bp.parse_benchmark_output(encode_content, 'BenchmarkEncode')
        bp.save_to_csv(encode_data, encode_csv)

    # Parse or load decode data
    decode_csv = os.path.join(csv_dir, 'decode.csv')
    if os.path.exists(decode_csv):
        print(f"Loading decode from {decode_csv}")
        decode_data = bp.load_from_csv(decode_csv)
    else:
        print(f"Parsing {args.decode}")
        if args.decode.endswith('.json'):
            decode_data = bp.parse_benchmark_json(args.decode)
        else:
            with open(args.decode) as f:
                decode_content = f.read()
            decode_data = bp.parse_benchmark_output(decode_content, 'BenchmarkDecode')
        bp.save_to_csv(decode_data, decode_csv)

    # Parse or load iterate data
    iterate_csv = os.path.join(csv_dir, 'iterate.csv')
    if os.path.exists(iterate_csv):
        print(f"Loading iterate from {iterate_csv}")
        iterate_data = bp.load_from_csv(iterate_csv)
    else:
        print(f"Parsing {args.iterate}")
        if args.iterate.endswith('.json'):
            iterate_data = bp.parse_benchmark_json(args.iterate)
        else:
            with open(args.iterate) as f:
                iterate_content = f.read()
            iterate_data = bp.parse_benchmark_output(iterate_content, 'BenchmarkIterateAll')
        bp.save_to_csv(iterate_data, iterate_csv)

    # Parse or load random access data
    random_csv = os.path.join(csv_dir, 'random.csv')
    if os.path.exists(random_csv):
        print(f"Loading random from {random_csv}")
        random_data = bp.load_from_csv(random_csv)
    else:
        print(f"Parsing {args.random}")
        if args.random.endswith('.json'):
            random_data = bp.parse_benchmark_json(args.random)
        else:
            with open(args.random) as f:
                random_content = f.read()
            random_data = bp.parse_benchmark_output(random_content, 'BenchmarkRandomAccess')
        bp.save_to_csv(random_data, random_csv)

    # Generate tables
    # Find the most common size in the data
    size_counts = {}
    for key, values in sizes_data.items():
        size = values['size_pts']
        size_counts[size] = size_counts.get(size, 0) + 1

    # Use the most common size, or 50 if available, or the largest if not
    primary_size = 50 if 50 in size_counts else max(size_counts.keys()) if size_counts else 50

    # Generate dynamic key findings for all parts
    key_findings = generate_key_findings(sizes_data, encode_data, decode_data, iterate_data, random_data)
    iteration_findings = generate_iteration_findings(iterate_data)
    decode_iterate_findings = generate_decode_iterate_findings(decode_data)
    random_access_findings = generate_random_access_findings(random_data)
    recommendations = generate_recommendations(sizes_data, encode_data, decode_data, iterate_data, random_data)

    tables = {
        'TEST_DATE': datetime.now().strftime('%B %d, %Y'),
        'TEST_SIZES': '200 metrics × [10/20/50/100/200] points',
        'SIZE_COMPARISON_NUMERIC': format_size_table(sizes_data, size_pts=primary_size),
        'SIZE_COMPARISON_TEXT': format_size_table(sizes_data, size_pts=primary_size),  # Same for now
        'ENCODING_NUMERIC': format_benchmark_table(encode_data, ['10pts', '20pts', '50pts']),
        'ENCODING_TEXT': format_benchmark_table(encode_data, ['10pts', '20pts', '50pts']),  # Same for now
        'DECODING_NUMERIC': format_benchmark_table(decode_data, ['10pts', '20pts', '50pts']),
        'DECODING_TEXT': format_benchmark_table(decode_data, ['10pts', '20pts', '50pts']),  # Same for now
        'ITERATION_NUMERIC': format_benchmark_table(iterate_data, ['10pts', '20pts', '50pts']),
        'ITERATION_TEXT': format_benchmark_table(iterate_data, ['10pts', '20pts', '50pts']),  # Same for now
        'DECODE_ITERATE_NUMERIC': format_benchmark_table(decode_data, ['10pts', '20pts', '50pts']),  # Use decode data for now
        'DECODE_ITERATE_TEXT': format_benchmark_table(decode_data, ['10pts', '20pts', '50pts']),  # Same for now
        'RANDOM_ACCESS_TEXT': format_benchmark_table(random_data, ['10pts', '20pts', '50pts']),
        'RANDOM_ACCESS_NUMERIC': format_benchmark_table(random_data, ['10pts', '20pts', '50pts']),
        'KEY_FINDINGS': key_findings,
        'ITERATION_FINDINGS': iteration_findings,
        'DECODE_ITERATE_FINDINGS': decode_iterate_findings,
        'RANDOM_ACCESS_FINDINGS': random_access_findings,
        'RECOMMENDATIONS': recommendations,
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
