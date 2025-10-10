#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: bench_numeric_gorilla_decoder.sh --baseline <commit> [--count N] [--output DIR] [--cpuprofile] [--memprofile]

Options:
  --baseline <commit>   Git commit, tag, or ref to use for the baseline comparison (required).
  --count N             Number of benchmark iterations per run (default: 10).
  --output DIR          Directory to store the generated benchmark artifacts.
  --cpuprofile          Capture Go CPU profiles for both runs (saved in the output directory).
  --memprofile          Capture Go heap profiles for both runs (saved in the output directory).

The script will:
  1. Create a temporary git worktree at the specified baseline commit.
  2. Copy the decoder benchmark suite into the worktree so both runs share identical datasets.
  3. Execute the Numeric Gorilla decoder benchmarks for the baseline and current workspace.
  4. Compare the two runs using benchstat and store the text, CSV, and markdown summaries in the chosen directory.

Benchstat is installed automatically with `go install` when it is not already on PATH.
EOF
}

baseline_ref=""
count=10
output_dir=""
enable_cpuprofile=false
enable_memprofile=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --baseline)
      baseline_ref=${2:-}
      shift 2
      ;;
    --count)
      count=${2:-}
      shift 2
      ;;
    --output)
      output_dir=${2:-}
      shift 2
      ;;
    --cpuprofile)
      enable_cpuprofile=true
      shift 1
      ;;
    --memprofile)
      enable_memprofile=true
      shift 1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ -z "$baseline_ref" ]]; then
  echo "Error: --baseline is required" >&2
  usage
  exit 1
fi

repo_root=$(git rev-parse --show-toplevel)
if [[ -z "$output_dir" ]]; then
  timestamp=$(date +%Y%m%d_%H%M%S)
  output_dir="$repo_root/.benchmarks/$timestamp"
fi
mkdir -p "$output_dir"

current_out="$output_dir/current.txt"
baseline_out="$output_dir/baseline.txt"
comparison_out="$output_dir/comparison.txt"
comparison_csv="$output_dir/comparison.csv"
comparison_md="$output_dir/comparison.md"

current_cpu_profile=""
baseline_cpu_profile=""
if [[ "$enable_cpuprofile" == true ]]; then
  current_cpu_profile="$output_dir/current_cpu.pprof"
  baseline_cpu_profile="$output_dir/baseline_cpu.pprof"
  rm -f "$current_cpu_profile" "$baseline_cpu_profile"
fi

current_mem_profile=""
baseline_mem_profile=""
if [[ "$enable_memprofile" == true ]]; then
  current_mem_profile="$output_dir/current_mem.pprof"
  baseline_mem_profile="$output_dir/baseline_mem.pprof"
  rm -f "$current_mem_profile" "$baseline_mem_profile"
fi

bench_cmd=(go test -run=^$ -bench=NumericGorillaDecoder -benchmem -count="$count" ./internal/encoding)

current_cmd=("${bench_cmd[@]}")
baseline_cmd=("${bench_cmd[@]}")

if [[ "$enable_cpuprofile" == true ]]; then
  current_cmd+=(-cpuprofile="$current_cpu_profile")
  baseline_cmd+=(-cpuprofile="$baseline_cpu_profile")
fi

if [[ "$enable_memprofile" == true ]]; then
  current_cmd+=(-memprofile="$current_mem_profile")
  baseline_cmd+=(-memprofile="$baseline_mem_profile")
fi

benchstat_bin=$(command -v benchstat || true)
if [[ -z "$benchstat_bin" ]]; then
  echo "Installing benchstat..."
  GO111MODULE=on go install golang.org/x/perf/cmd/benchstat@latest
  benchstat_bin=$(command -v benchstat)
fi

python_bin=$(command -v python3 || command -v python || true)
if [[ -z "$python_bin" ]]; then
  echo "Error: python3 (or python) is required to render markdown output" >&2
  exit 1
fi

worktree_dir=$(mktemp -d)
cleanup() {
  git -C "$repo_root" worktree remove --force "$worktree_dir" >/dev/null 2>&1 || true
  rm -rf "$worktree_dir"
}
trap cleanup EXIT

echo "Creating baseline worktree at $baseline_ref..."
git -C "$repo_root" worktree add --force "$worktree_dir" "$baseline_ref" >/dev/null

# Ensure the benchmark suite matches between current and baseline runs.
mkdir -p "$worktree_dir/internal/encoding"
cp "$repo_root/internal/encoding/numeric_gorilla_bench_test.go" \
   "$worktree_dir/internal/encoding/numeric_gorilla_bench_test.go"

echo "Running current workspace benchmarks..."
("${current_cmd[@]}" >"$current_out") || {
  echo "Current benchmark run failed" >&2
  exit 1
}

echo "Running baseline benchmarks..."
(
  cd "$worktree_dir"
  "${baseline_cmd[@]}" >"$baseline_out"
) || {
  echo "Baseline benchmark run failed" >&2
  exit 1
}

echo "Comparing results with benchstat..."
"$benchstat_bin" "$baseline_out" "$current_out" >"$comparison_out"

"$benchstat_bin" -format csv "$baseline_out" "$current_out" >"$comparison_csv"

"$python_bin" - "$comparison_csv" "$comparison_md" <<'PY'
import csv
import sys

if len(sys.argv) != 3:
  raise SystemExit("expected CSV input and markdown output paths")

csv_path = sys.argv[1]
markdown_path = sys.argv[2]

tables = {}
order = []
current_metric = None

with open(csv_path, newline="", encoding="utf-8") as csv_file:
  reader = csv.reader(csv_file)
  for row in reader:
    if not row:
      continue

    if len(row) == 1:
      cell = row[0].strip()
      if not cell:
        continue
      if cell.startswith(("goos:", "goarch:", "pkg:", "cpu:")):
        continue
      if ":" in cell:
        # Warn / failure lines (e.g. F19: all samples are equal)
        continue

    if row[0] == "" and any(cell.endswith(".txt") for cell in row if cell):
      continue

    if len(row) >= 2 and row[1] in {"sec/op", "B/op", "allocs/op"}:
      current_metric = row[1]
      if current_metric not in tables:
        tables[current_metric] = {"rows": [], "geomean": None}
        order.append(current_metric)
      continue

    if current_metric is None:
      continue

    if row[0] == "geomean":
      tables[current_metric]["geomean"] = row
      continue

    benchmark = row[0].strip()
    if not benchmark:
      continue

    tables[current_metric]["rows"].append(row)

def format_seconds(value: str) -> str:
  try:
    seconds = float(value)
  except ValueError:
    return value

  units = [
    ("s", 1.0),
    ("ms", 1e-3),
    ("µs", 1e-6),
    ("ns", 1e-9),
  ]

  for unit, factor in units:
    if seconds >= factor or unit == "ns":
      scaled = seconds / factor
      if scaled >= 100:
        fmt = f"{scaled:.1f}"
      elif scaled >= 10:
        fmt = f"{scaled:.2f}"
      else:
        fmt = f"{scaled:.3f}"
      fmt = fmt.rstrip("0").rstrip(".")
      return f"{fmt} {unit}"

  return value

def format_generic(value: str) -> str:
  try:
    number = float(value)
  except ValueError:
    return value

  if number.is_integer():
    return str(int(number))

  fmt = f"{number:.3f}".rstrip("0").rstrip(".")
  return fmt

def format_metric_value(metric: str, value: str, ci: str) -> str:
  value = value.strip()
  ci = ci.strip()
  if not value:
    return "—"

  if metric == "sec/op":
    base = format_seconds(value)
  else:
    base = format_generic(value)

  if ci:
    return f"{base} ±{ci}"

  return base

def format_delta(delta: str) -> str:
  delta = delta.strip()
  if not delta:
    return "—"
  if delta.startswith("-"):
    return f"**{delta}**"
  return delta

def format_p(value: str) -> str:
  value = value.strip()
  if not value:
    return "—"
  if value == "~":
    return "~"

  parts = {}
  for token in value.split():
    if "=" in token:
      key, val = token.split("=", 1)
      parts[key] = val

  raw_p = parts.get("p")
  samples = parts.get("n")

  if raw_p is None:
    return value

  try:
    p_val = float(raw_p)
  except ValueError:
    return value

  if p_val < 0.001:
    p_str = "<0.001"
  else:
    p_str = f"{p_val:.3f}"

  if samples is not None:
    return f"{p_str} (n={samples})"

  return p_str

def format_benchmark(name: str) -> str:
  if "-" in name:
    prefix, suffix = name.rsplit("-", 1)
    if suffix.isdigit():
      name = prefix
  return f"`{name}`"

with open(markdown_path, "w", encoding="utf-8") as md_file:
  if not order:
    md_file.write("No benchmark results available.\n")
    raise SystemExit(0)

  md_file.write("# Numeric Gorilla Decoder Benchmark Comparison\n\n")

  for metric in order:
    table = tables.get(metric)
    if not table or not table["rows"]:
      continue

    md_file.write(f"## {metric}\n\n")
    md_file.write("| Benchmark | Baseline | Current | Δ vs baseline | p-value |\n")
    md_file.write("| --- | --- | --- | --- | --- |\n")

    for row in table["rows"]:
      baseline = format_metric_value(metric, row[1] if len(row) > 1 else "", row[2] if len(row) > 2 else "")
      current = format_metric_value(metric, row[3] if len(row) > 3 else "", row[4] if len(row) > 4 else "")
      delta = format_delta(row[5] if len(row) > 5 else "")
      p_value = format_p(row[6] if len(row) > 6 else "")
      md_file.write(
        f"| {format_benchmark(row[0])} | {baseline} | {current} | {delta} | {p_value} |\n"
      )

    geomean = table.get("geomean")
    if geomean:
      baseline_geo = format_metric_value(metric, geomean[1] if len(geomean) > 1 else "", geomean[2] if len(geomean) > 2 else "")
      current_geo = format_metric_value(metric, geomean[3] if len(geomean) > 3 else "", geomean[4] if len(geomean) > 4 else "")
      delta_geo = format_delta(geomean[5] if len(geomean) > 5 else "")
      summary = f"{baseline_geo} vs {current_geo}"
      if delta_geo != "—":
        summary = f"{summary} ({delta_geo})"
      md_file.write(f"\n**Geometric mean:** {summary}\n")

    md_file.write("\n")
PY

echo "Benchmark artifacts saved to $output_dir"
echo "  Baseline:   $baseline_out"
echo "  Current:    $current_out"
echo "  Text diff:  $comparison_out"
echo "  CSV diff:   $comparison_csv"
echo "  Markdown:   $comparison_md"
if [[ -n "$current_cpu_profile" ]]; then
  echo "  CPU profile (current):  $current_cpu_profile"
  echo "  CPU profile (baseline): $baseline_cpu_profile"
fi
if [[ -n "$current_mem_profile" ]]; then
  echo "  Heap profile (current):  $current_mem_profile"
  echo "  Heap profile (baseline): $baseline_mem_profile"
fi
