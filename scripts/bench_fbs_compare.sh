#!/bin/bash

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
  for file in sizes.txt encode.txt encode.json decode.txt decode.json iterate.txt iterate.json random.txt random.json; do
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
  go test -bench="BenchmarkEncode" -benchtime="$benchtime" -count="$count" -run=^$ -json >"$output_dir/encode.json" 2>"$output_dir/encode.txt" || {
    echo "Encode benchmarks failed" >&2
    exit 1
  }

  echo "  3/5: Decoding benchmarks..."
  go test -bench="BenchmarkDecode" -benchtime="$benchtime" -count="$count" -run=^$ -json >"$output_dir/decode.json" 2>"$output_dir/decode.txt" || {
    echo "Decode benchmarks failed" >&2
    exit 1
  }

  echo "  4/5: Iteration benchmarks..."
  go test -bench="BenchmarkIterateAll" -benchtime="$benchtime" -count="$count" -run=^$ -json >"$output_dir/iterate.json" 2>"$output_dir/iterate.txt" || {
    echo "Iterate benchmarks failed" >&2
    exit 1
  }

  echo "  5/5: Random access benchmarks..."
  go test -bench="BenchmarkRandomAccess" -benchtime="$benchtime" -count="$count" -run=^$ -json >"$output_dir/random.json" 2>"$output_dir/random.txt" || {
    echo "Random access benchmarks failed" >&2
    exit 1
  }
fi

# Generate report
echo "Generating report..."
"$python_bin" "$repo_root/tests/fbs_compare/generate_report.py" \
  --template "$repo_root/tests/fbs_compare/BENCHMARK_REPORT.md.template" \
  --sizes "$output_dir/sizes.txt" \
  --encode "$output_dir/encode.json" \
  --decode "$output_dir/decode.json" \
  --iterate "$output_dir/iterate.json" \
  --random "$output_dir/random.json" \
  --output "$repo_root/tests/fbs_compare/BENCHMARK_REPORT.md" \
  --artifacts-dir "$output_dir"

echo ""
echo "Benchmark artifacts saved to: $output_dir"
echo "  sizes.txt, encode.json, decode.json, iterate.json, random.json"
echo ""
echo "Report generated at: tests/fbs_compare/BENCHMARK_REPORT.md"
echo ""
echo "To regenerate report from this data:"
echo "  ./scripts/bench_fbs_compare.sh --reuse $output_dir"