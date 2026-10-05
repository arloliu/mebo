#!/usr/bin/env bash
# layouts.sh: layout-averaged measurev2 run for the performance report.
# Usage: layouts.sh -o OUTDIR [-cpu 6] [-benchtime 50ms] [-cells report] [-rounds 4|2] [--build-only]
# The steps are implemented in layouts.py; see its docstring and
# docs/specs/measurev2-fast-report-runs-design.md ("Layout averaging").
set -euo pipefail
exec python3 "$(dirname "${BASH_SOURCE[0]}")/layouts.py" "$@"
