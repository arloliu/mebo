#!/usr/bin/env bash
# validate.sh: the acceptance gates of the measurev2 fast report runs
# (docs/specs/measurev2-fast-report-runs-design.md, "Acceptance gates").
#
# Usage: validate.sh -o OUTDIR [-cpu 6] [-ref DIR] [--calibrate]
#
# Runs, in order and never in parallel:
#   gate 5  layouts.sh --build-only: four layouts and the movement check, before any timing;
#   gate 1  -sizes-only -profiles report against the reference JSONs in -ref;
#           the data-set lifecycle test (go test without -short);
#   gate 2  layout 0, pinned: -benchtime 1s and 50ms, A↑ B↓ B↑ A↓;
#   gate 3  layout 0, pinned: three alternations of one combined and one isolated set of invocations;
#   gate 4  two complete layouts.sh runs (gate 6 reads their stage times).
# With --calibrate it runs gates 5, 2, 3 and 4 and prints the per-operation distributions
# that the thresholds in acceptance_thresholds.json are proposed from, instead of checking them.
# Every gate prints its numbers; the raw artifacts stay in OUTDIR. Exit status 1 if any gate fails.
# VALIDATE_BENCHTIME_A (default 1s) and VALIDATE_BENCHTIME_B (default 50ms, the candidate) shorten a smoke run
# of this script; the gates' verdicts only count at the defaults.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
out="" cpu=6 ref="$repo/tmp/measurev2-reference-2026-10-04" calibrate=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -cpu) cpu="$2"; shift 2 ;;
    -ref) ref="$2"; shift 2 ;;
    --calibrate) calibrate=1; shift ;;
    *) echo "validate.sh: unknown argument $1" >&2; exit 2 ;;
  esac
done
[[ -n "$out" ]] || { echo "validate.sh: -o OUTDIR is required" >&2; exit 2; }
[[ ! -e "$out" ]] || { echo "validate.sh: $out already exists" >&2; exit 2; }
mkdir -p "$out"
exec > >(tee "$out/validate.log") 2>&1

export VALIDATE_BENCHTIME_A="${VALIDATE_BENCHTIME_A:-1s}" VALIDATE_BENCHTIME_B="${VALIDATE_BENCHTIME_B:-50ms}"
bt_a=$VALIDATE_BENCHTIME_A bt_b=$VALIDATE_BENCHTIME_B
acceptance=(python3 "$here/acceptance.py" --cpu "$cpu")
profiles="$(python3 -c 'import sys; sys.path.insert(0, sys.argv[1]); import report_schema; print(" ".join(report_schema.REPORT_PROFILES))' \
  "$repo/.agents/skills/update-performance-report/scripts")"
status=0
stamp() { echo "== $(date '+%F %T') $*"; }

stamp "gate 5: build the four layouts and check movement"
"$here/layouts.sh" -o "$out/build" -cpu "$cpu" --build-only
bin="$out/build/bin/measurev2_0"

# pin runs layout 0 under the same environment as layouts.sh invocations.
pin() { taskset -c "$cpu" env -u GOMEMLIMIT GOMAXPROCS=1 GOGC=100 GODEBUG= "$bin" -cells report "$@"; }

if [[ $calibrate -eq 0 ]]; then
  stamp "gate 1: sizes exact"
  "$bin" -sizes-only -profiles report -outdir "$out/gate1"
  "${acceptance[@]}" gate1 "$out/gate1" "$ref" || status=1

  stamp "lifecycle test"
  (cd "$here" && go test -run '^TestDatasetLifecycle$' -count=1 -v .) || status=1
fi

stamp "gate 2: -benchtime $bt_a against $bt_b (A↑ B↓ B↑ A↓)"
mkdir -p "$out/gate2"
pin -profiles report -benchtime "$bt_a" -order forward -outdir "$out/gate2/A_up"
pin -profiles report -benchtime "$bt_b" -order reverse -outdir "$out/gate2/B_down"
pin -profiles report -benchtime "$bt_b" -order forward -outdir "$out/gate2/B_up"
pin -profiles report -benchtime "$bt_a" -order reverse -outdir "$out/gate2/A_down"

stamp "gate 3: one process for all data sets against one per data set"
mkdir -p "$out/gate3"
for i in 1 2 3; do
  pin -profiles report -benchtime "$bt_b" -order forward -outdir "$out/gate3/combined$i"
  mkdir "$out/gate3/isolated$i"
  for p in $profiles; do
    pin -profiles "$p" -benchtime "$bt_b" -order forward -outdir "$out/gate3/isolated$i/$p"
  done
done

stamp "gate 4: two complete layouts.sh runs"
mkdir -p "$out/gate4"
for run in run1 run2; do
  # Gate 6 times the whole wrapper from here, from start to exit; the stage times in provenance.json are detail.
  t0=$(date +%s.%N)
  "$here/layouts.sh" -o "$out/gate4/$run" -cpu "$cpu" -benchtime "$bt_b"
  echo "$t0 $(date +%s.%N)" > "$out/gate4/$run.wall"
done

if [[ $calibrate -eq 1 ]]; then
  stamp "calibration"
  "${acceptance[@]}" calibrate "$out/gate2" "$out/gate3" "$out/gate4" | tee "$out/calibration.txt"
  exit 0
fi

stamp "evaluate"
g2=$out/gate2 g3=$out/gate3
"${acceptance[@]}" gate2 "$g2/A_up" "$g2/B_down" "$g2/B_up" "$g2/A_down" || status=1
"${acceptance[@]}" gate3 "$g3/combined1" "$g3/isolated1" "$g3/combined2" "$g3/isolated2" \
  "$g3/combined3" "$g3/isolated3" || status=1
"${acceptance[@]}" gate4 "$out/gate4/run1" "$out/gate4/run2" || status=1
"${acceptance[@]}" gate6 "$out/gate4/run1" "$out/gate4/run2" || status=1
stamp "done: $([[ $status -eq 0 ]] && echo 'every gate passed' || echo 'a gate FAILED')"
exit $status
