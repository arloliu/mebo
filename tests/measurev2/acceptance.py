#!/usr/bin/env python3
"""Acceptance gates 2-6 of the measurev2 fast report runs (docs/specs/measurev2-fast-report-runs-design.md).

Usage:
    acceptance.py gate1 SIZES_DIR REFERENCE_DIR
    acceptance.py gate2 A_UP B_DOWN B_UP A_DOWN
    acceptance.py gate3 COMBINED1 ISOLATED1 COMBINED2 ISOLATED2 COMBINED3 ISOLATED3
    acceptance.py gate4 RUN1 RUN2
    acceptance.py gate6 RUN1 [RUN2 ...]
    acceptance.py calibrate GATE2DIR GATE3DIR GATE4DIR

A raw directory is one measurev2 -profiles -outdir; an isolated set is a directory of such directories, one per profile;
a run is a layouts.sh output directory (merged/, provenance.json).
Thresholds come from acceptance_thresholds.json next to this file (or --thresholds).
Each input must be the run the gate specifies (profiles, cells, benchtime, order, rounds);
VALIDATE_BENCHTIME_A and VALIDATE_BENCHTIME_B (defaults 1s and 50ms) change the expected benchtimes for a smoke run.
Every gate prints its numbers and exits 1 when it fails.
"""
import argparse
import json
import math
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(HERE)), '.agents', 'skills',
                                'update-performance-report', 'scripts'))
from report_schema import (  # noqa: E402
    OPS, REPORT_PROFILES, SchemaError, check_doc, compare, load_input_set, load_json, manifest_cells, median,
    percentile,
)

THRESHOLD_KEYS = {
    'stable_control', 'stable_share_min', 'stable_share_per_op_min', 'cell_within', 'cell_within_share_min',
    'cell_max', 'op_median_within', 'bytes_rel', 'bytes_abs', 'allocs_abs', 'benchtime_t_min', 'benchtime_t_max',
    'max_minutes',
}
# Operations, in report order, that a cell id may end with.
OP_ORDER = OPS
EPS = 1e-12
# The candidate configuration every gate uses (spec, "Acceptance gates"); validate.sh may override the benchtimes for a smoke run.
CANDIDATE = {'benchtime': os.environ.get('VALIDATE_BENCHTIME_B', '50ms'), 'rounds': 4, 'cells': 'report'}
# Gate 2's four runs in order: (directory, -benchtime, -order).
GATE2_RUNS = [('A_up', os.environ.get('VALIDATE_BENCHTIME_A', '1s'), 'forward'),
              ('B_down', CANDIDATE['benchtime'], 'reverse'),
              ('B_up', CANDIDATE['benchtime'], 'forward'),
              ('A_down', os.environ.get('VALIDATE_BENCHTIME_A', '1s'), 'reverse')]


class GateFailure(Exception):
    """A gate's numbers did not meet its thresholds."""


def load_thresholds(path):
    t = load_json(path)
    missing = THRESHOLD_KEYS - set(t)
    if missing:
        raise SchemaError(f'{path}: missing thresholds {sorted(missing)}')
    return t


def cell_op(cell):
    return cell.rsplit('/', 1)[1]


def raw_cells(outdir, profiles=None, benchtime=None, order=None, cells='report', cpu=None):
    """{cell id: raw op object} of one measurev2 -profiles output directory.

    Every file is validated and all of them must come from one invocation (one run_id, common and invocation),
    pinned to one CPU (cpu, when given) with the layouts.sh runtime environment;
    the invocation must have requested exactly profiles (default: the report manifest)
    with the given -cells, -benchtime and -order, every requested profile must have its file,
    and the cells must be exactly the manifest's."""
    profiles = list(REPORT_PROFILES if profiles is None else profiles)
    pdir = os.path.join(outdir, 'profiles')
    names = sorted(os.listdir(pdir))
    if names != sorted(f'matrix_{p}.json' for p in profiles):
        raise SchemaError(f'{pdir}: files {names}, want one per profile of {profiles}')
    found, identity = {}, None
    for name in names:
        doc = load_json(os.path.join(pdir, name))
        _, profile = check_doc(doc, os.path.join(pdir, name), kind='raw')
        c = doc['common']
        ident = (doc['run_id'], c, doc['invocation'])
        if identity is None:
            identity = ident
            check_pinned(c, cpu, f'{pdir}/{name}')
        elif ident != identity:
            raise SchemaError(f'{pdir}/{name}: run_id, common or invocation differs from the other files of this invocation')
        want = {'profiles': profiles, 'cells': cells}
        if benchtime is not None:
            want['benchtime'] = benchtime
        for k, v in want.items():
            if c[k] != v:
                raise SchemaError(f'{pdir}/{name}: common.{k} is {c[k]!r}, want {v!r}')
        if order is not None and doc['invocation']['order'] != order:
            raise SchemaError(f'{pdir}/{name}: order {doc["invocation"]["order"]!r}, want {order!r}')
        for row in doc['matrix']:
            for op in OPS:
                if op in row:
                    cid = f'{profile}/{row["label"]}/{op}'
                    if cid in found:
                        raise SchemaError(f'{pdir}/{name}: cell {cid} appears twice')
                    found[cid] = row[op]
    if sorted(found) != sorted(manifest_cells(profiles, cells)):
        raise SchemaError(f'{outdir}: cells are not the manifest cells of {profiles}')
    return found


def check_pinned(common, cpu, where):
    """The runtime environment every gate input must have: one CPU, GOMAXPROCS=1, GOGC=100, no GOMEMLIMIT or GODEBUG."""
    aff = common['cpu_affinity']
    if len(aff) != 1 or (cpu is not None and aff != [cpu]):
        raise SchemaError(f'{where}: CPU affinity {aff}, want exactly {[cpu] if cpu is not None else "one CPU"}')
    env = {'gomaxprocs': 1, 'gogc': '100', 'gomemlimit': '', 'godebug': ''}
    for k, v in env.items():
        if common[k] != v:
            raise SchemaError(f'{where}: {k} is {common[k]!r}, want {v!r}')


def isolated_cells(setdir, benchtime=None, order=None, cpu=None):
    """Cells of an isolated set: exactly one -profiles <p> directory, named p, per report profile."""
    if sorted(os.listdir(setdir)) != sorted(REPORT_PROFILES):
        raise SchemaError(f'{setdir}: want one directory per report profile')
    cells = {}
    for p in REPORT_PROFILES:
        cells.update(raw_cells(os.path.join(setdir, p), profiles=[p], benchtime=benchtime, order=order, cpu=cpu))
    return cells


def merged_cells(rundir, benchtime=None, rounds=None, cells='report', cpu=None):
    """{cell id: merged op object} and the allocation disagreements of a complete layouts.sh run."""
    mdir = os.path.join(rundir, 'merged')
    _, main, docs = load_input_set(os.path.join(mdir, 'main.json'), os.path.join(mdir, 'profiles'))
    c = main['common']
    check_pinned(c, cpu, mdir)
    for k, v in (('benchtime', benchtime), ('rounds', rounds), ('cells', cells)):
        if v is not None and c[k] != v:
            raise SchemaError(f'{mdir}: common.{k} is {c[k]!r}, want {v!r}')
    found, disagreements = {}, set()
    for profile, doc in docs.items():
        disagreements.update(doc['allocation_disagreements'])
        for row in doc['matrix']:
            for op in OPS:
                if op in row:
                    found[f'{profile}/{row["label"]}/{op}'] = row[op]
    if sorted(found) != sorted(manifest_cells(REPORT_PROFILES, c['cells'])):
        raise SchemaError(f'{mdir}: cells are not the manifest cells')
    return found, disagreements


def same_cells(*sets):
    keys = set(sets[0])
    for s in sets[1:]:
        if set(s) != keys:
            raise SchemaError(f'cell sets differ: {sorted(keys ^ set(s))[:5]}')
    return sorted(keys)


def bytes_ok(got, ref, t):
    return abs(got - ref) <= max(t['bytes_rel'] * ref, t['bytes_abs']) + EPS


def allocs_ok(got, lo, hi, t):
    """Gates 2 and 3: allocs/op within [lo, hi] widened by allocs_abs,
    since allocs/op truncates a mean that can sit just either side of an integer."""
    return lo - t['allocs_abs'] <= got <= hi + t['allocs_abs']


def share(values, pred):
    values = list(values)
    return sum(1 for v in values if pred(v)) / len(values) if values else 0.0


def per_op(cells, values):
    """{op: [values of that op's cells]}, keeping report order."""
    out = {op: [] for op in OP_ORDER}
    for c, v in zip(cells, values):
        out[cell_op(c)].append(v)
    return {op: v for op, v in out.items() if v}


def ratio_checks(name, cells, ratios, t, failures, lines, op_medians=True):
    """The shared ratio rule: |x - 1| within cell_within for a share of cells and within cell_max for every cell;
    with op_medians (gates 2 and 3, not gate 4), each operation's median within op_median_within."""
    dev = [abs(r - 1) for r in ratios]
    within = share(dev, lambda d: d <= t['cell_within'] + EPS)
    worst = max(dev, default=0.0)
    lines.append(f'{name}: {within:.1%} of {len(dev)} cells within ±{t["cell_within"]:.0%} '
                 f'(need {t["cell_within_share_min"]:.0%}); worst {worst:.2%} (limit {t["cell_max"]:.0%})')
    if within + EPS < t['cell_within_share_min']:
        failures.append(f'{name}: only {within:.1%} of cells within ±{t["cell_within"]:.0%}')
    over = [c for c, d in zip(cells, dev) if d > t['cell_max'] + EPS]
    if over:
        failures.append(f'{name}: {len(over)} cells beyond ±{t["cell_max"]:.0%}, e.g. {over[:3]}')
    for op, vals in per_op(cells, ratios).items():
        m = median(vals)
        lines.append(f'  {op}: median {m:.4f} over {len(vals)} cells')
        if op_medians and abs(m - 1) > t['op_median_within'] + EPS:
            failures.append(f'{name}: {op} median ratio {m:.4f} is outside ±{t["op_median_within"]:.0%} '
                            f'(the {op} timings are systematically off)')


def report(name, lines, failures):
    print(f'== {name}')
    for line in lines:
        print(line)
    if failures:
        for f in failures:
            print(f'FAIL {f}')
        raise GateFailure(f'{name} failed: {len(failures)} problems')
    print(f'PASS {name}')


# ---------------------------------------------------------------- gate 1

SIZE_KEYS = ('encoded_bytes', 'bytes_per_point', 'vs_raw_ratio', 'space_savings_pct', 'total_points')


def gate1(sizes_dir, ref_dir):
    """Gate 1: -sizes-only -profiles report reproduces every size and scaling value of the reference JSONs exactly."""
    lines, failures = [], []
    pairs = [('main.json', 'main.json')] + [(f'profiles/matrix_{p}.json', f'profiles/matrix_{p}.json')
                                            for p in REPORT_PROFILES]
    compared = 0
    for got_rel, ref_rel in pairs:
        got = load_json(os.path.join(sizes_dir, got_rel))
        check_doc(got, got_rel, kind='sizes-only')
        ref = load_json(os.path.join(ref_dir, ref_rel))
        check_doc(ref, ref_rel, kind='legacy')
        ref_rows = {r['label']: r for r in ref['matrix']}
        for row in got['matrix']:
            for k in SIZE_KEYS:
                compared += 1
                if row[k] != ref_rows[row['label']][k]:
                    failures.append(f'{got_rel} {row["label"]} {k}: {row[k]} vs reference {ref_rows[row["label"]][k]}')
        if sorted(got['scaling'], key=lambda x: x['label']) != sorted(ref['scaling'], key=lambda x: x['label']):
            failures.append(f'{got_rel}: scaling differs from the reference')
        if got['metadata']['data_config'] != ref['metadata']['data_config']:
            failures.append(f'{got_rel}: data_config differs from the reference')
    lines.append(f'{compared} size fields and {len(pairs)} scaling sets compared against {ref_dir}')
    report('gate 1 (sizes exact)', lines, failures)


# ---------------------------------------------------------------- gate 2

def gate2_numbers(a_up, b_down, b_up, a_down):
    cells = same_cells(a_up, b_down, b_up, a_down)
    rows = []
    for c in cells:
        a = (a_up[c]['ns_per_op'] + a_down[c]['ns_per_op']) / 2
        b = (b_up[c]['ns_per_op'] + b_down[c]['ns_per_op']) / 2
        rows.append({
            'cell': c, 'r': b / a,
            'cA': a_up[c]['ns_per_op'] / a_down[c]['ns_per_op'],
            'cB': b_up[c]['ns_per_op'] / b_down[c]['ns_per_op'],
        })
    return cells, rows


def gate2(a_up, b_down, b_up, a_down, t, target_a=1.0, target_b=0.05):
    """Gate 2: the short benchtime against 1 s on one binary, with order controls."""
    cells, rows = gate2_numbers(a_up, b_down, b_up, a_down)
    lines, failures = [], []
    stable = [r for r in rows if abs(r['cA'] - 1) <= t['stable_control'] + EPS and abs(r['cB'] - 1) <= t['stable_control'] + EPS]
    stable_share = len(stable) / len(rows)
    lines.append(f'stable cells: {len(stable)} of {len(rows)} ({stable_share:.1%}, need {t["stable_share_min"]:.0%})')
    if stable_share + EPS < t['stable_share_min']:
        failures.append(f'inconclusive: only {stable_share:.1%} of cells are stable')
    by_op_all = per_op(cells, rows)
    stable_ids = {r['cell'] for r in stable}
    for op, op_rows in by_op_all.items():
        s = share(op_rows, lambda r: r['cell'] in stable_ids)
        lines.append(f'  {op}: {s:.1%} stable')
        if s + EPS < t['stable_share_per_op_min']:
            failures.append(f'inconclusive: only {s:.1%} of {op} cells are stable '
                            f'(need {t["stable_share_per_op_min"]:.0%})')

    dev = [abs(r['r'] - 1) for r in stable]
    within = share(dev, lambda d: d <= t['cell_within'] + EPS)
    lines.append(f'stable cells within ±{t["cell_within"]:.0%}: {within:.1%} (need {t["cell_within_share_min"]:.0%})')
    if within + EPS < t['cell_within_share_min']:
        failures.append(f'only {within:.1%} of stable cells within ±{t["cell_within"]:.0%}')
    over = [r['cell'] for r in rows if abs(r['r'] - 1) > t['cell_max'] + EPS]
    lines.append(f'worst |r - 1| over all cells: {max(abs(r["r"] - 1) for r in rows):.2%} (limit {t["cell_max"]:.0%})')
    if over:
        failures.append(f'{len(over)} cells beyond ±{t["cell_max"]:.0%}, e.g. {over[:3]}')
    for op, op_rows in by_op_all.items():
        m = median([r['r'] for r in op_rows])
        lines.append(f'  {op}: median r {m:.4f}')
        if abs(m - 1) > t['op_median_within'] + EPS:
            failures.append(f'{op}: median r {m:.4f} is outside ±{t["op_median_within"]:.0%} '
                            f'(the short benchtime biases {op})')

    for c in cells:
        lo, hi = sorted((a_up[c]['allocs_per_op'], a_down[c]['allocs_per_op']))
        for name, b in (('B↑', b_up[c]), ('B↓', b_down[c])):
            if not allocs_ok(b['allocs_per_op'], lo, hi, t):
                failures.append(f'{c}: {name} allocs/op {b["allocs_per_op"]} outside A\'s [{lo}, {hi}] ± {t["allocs_abs"]}')
            if not bytes_ok(b['bytes_per_op'], a_up[c]['bytes_per_op'], t):
                failures.append(f'{c}: {name} B/op {b["bytes_per_op"]} vs A↑ {a_up[c]["bytes_per_op"]}')

    for name, run, target in (('A↑', a_up, target_a), ('A↓', a_down, target_a), ('B↑', b_up, target_b), ('B↓', b_down, target_b)):
        ratios = [run[c]['t_ns'] / 1e9 / target for c in cells]
        lines.append(f'{name} t_ns / benchtime: min {min(ratios):.2f}, median {median(ratios):.2f}, max {max(ratios):.2f}')
        if min(ratios) < t['benchtime_t_min'] - EPS or max(ratios) > t['benchtime_t_max'] + EPS:
            failures.append(f'{name}: t_ns does not follow -benchtime {target}s '
                            f'(range {min(ratios):.2f}-{max(ratios):.2f} of the target)')
    report('gate 2 (short benchtime is accurate)', lines, failures)
    return rows


# ---------------------------------------------------------------- gate 3

def gate3(combined, isolated, t):
    """Gate 3: one process for all data sets against one process per data set, three alternations each."""
    cells = same_cells(*combined, *isolated)
    ratios, lines, failures = [], [], []
    for c in cells:
        cm = median([run[c]['ns_per_op'] for run in combined])
        im = median([run[c]['ns_per_op'] for run in isolated])
        ratios.append(cm / im)
        iso_allocs = [run[c]['allocs_per_op'] for run in isolated]
        lo, hi = min(iso_allocs), max(iso_allocs)
        for i, run in enumerate(combined):
            if not allocs_ok(run[c]['allocs_per_op'], lo, hi, t):
                failures.append(f'{c}: combined run {i + 1} allocs/op {run[c]["allocs_per_op"]} '
                                f'outside isolated [{lo}, {hi}] ± {t["allocs_abs"]}')
            ref = median([r[c]['bytes_per_op'] for r in isolated])
            if not bytes_ok(run[c]['bytes_per_op'], ref, t):
                failures.append(f'{c}: combined run {i + 1} B/op {run[c]["bytes_per_op"]} vs isolated {ref:.0f}')
    ratio_checks('combined / isolated', cells, ratios, t, failures, lines)
    report('gate 3 (one process for all data sets)', lines, failures)
    return dict(zip(cells, ratios))


# ---------------------------------------------------------------- gate 4

def comparison_groups(cells):
    """Cells grouped by report comparison: same data set and operation."""
    groups = {}
    for c in cells:
        profile, _, op = c.split('/')
        groups.setdefault((profile, op), []).append(c)
    return groups


def gate4(run1, run2, disagreements, t):
    """Gate 4: two complete layouts.sh runs agree, and no comparison is decided in opposite directions."""
    cells = same_cells(run1, run2)
    ratios = [run2[c]['ns_per_op'] / run1[c]['ns_per_op'] for c in cells]
    lines, failures = [], []
    ratio_checks('run 2 / run 1', cells, ratios, t, failures, lines, op_medians=False)
    for c in cells:
        if run1[c]['allocs_per_op'] != run2[c]['allocs_per_op']:
            failures.append(f'{c}: allocs/op {run1[c]["allocs_per_op"]} vs {run2[c]["allocs_per_op"]}')
        if not bytes_ok(run2[c]['bytes_per_op'], run1[c]['bytes_per_op'], t):
            failures.append(f'{c}: B/op {run1[c]["bytes_per_op"]} vs {run2[c]["bytes_per_op"]}')
    if disagreements:
        lines.append(f'allocation disagreements in either run ({len(disagreements)}): {sorted(disagreements)}')

    flips, decided = [], 0
    for group in comparison_groups(cells).values():
        for i, a in enumerate(group):
            for b in group[i + 1:]:
                o1, w1, _ = compare(run1[a], run1[b])
                o2, w2, _ = compare(run2[a], run2[b])
                if o1 == 'decided' and o2 == 'decided':
                    decided += 1
                    if w1 != w2:
                        flips.append(f'{a} vs {b}')
    lines.append(f'pairs decided in both runs: {decided}; decided in opposite directions: {len(flips)}')
    if flips:
        failures.append(f'{len(flips)} comparisons decided in opposite directions, e.g. {flips[:3]}')
    report('gate 4 (reproducible)', lines, failures)
    return dict(zip(cells, ratios))


# ---------------------------------------------------------------- gates 5 and 6

def gate6(rundirs, t):
    """Gates 5 and 6: the movement check passed in each run, and each run finished within max_minutes.
    The time is the wrapper's wall time measured by validate.sh (<run>.wall: start and end, seconds since the epoch),
    from start to exit; provenance.json supplies the stage detail and peak RSS."""
    lines, failures = [], []
    for d in rundirs:
        prov = load_json(os.path.join(d, 'provenance.json'))
        try:
            with open(d.rstrip('/') + '.wall') as f:
                start, end = (float(x) for x in f.read().split())
        except (OSError, ValueError) as exc:
            raise SchemaError(f'{d}.wall: the wrapper wall time is missing or malformed ({exc})') from exc
        if not (math.isfinite(start) and math.isfinite(end) and end >= start):
            raise SchemaError(f'{d}.wall: start {start} and end {end} are not a finite, ordered interval')
        total = (end - start) / 60
        stages = prov['stages']
        lines.append(f'{d}: {total:.2f} min from start to exit; ' + ', '.join(
            f'{s["stage"]} {s["end"] - s["start"]:.0f} s' for s in stages if not s['stage'].startswith('invoke')))
        rss = [i['peak_rss_kb'] for i in prov.get('invocations', [])]
        if rss:
            lines.append(f'  peak RSS: max {max(rss) / 1024:.0f} MiB over {len(rss)} invocations')
        movement = prov.get('movement')
        lines.append(f'  movement check: {movement} repository symbols moved (gate 5)')
        if not movement or not prov.get('published'):
            failures.append(f'{d}: no movement check recorded, or the run did not publish')
        if total > t['max_minutes'] + EPS:
            failures.append(f'{d}: {total:.2f} min exceeds {t["max_minutes"]} min')
    report('gates 5 and 6 (layout movement, time)', lines, failures)


# ---------------------------------------------------------------- calibration

def dist(values):
    if not values:
        return 'no cells'
    return (f'n={len(values)} p50 {percentile(values, 0.5):.4f} p90 {percentile(values, 0.9):.4f} '
            f'p95 {percentile(values, 0.95):.4f} p99 {percentile(values, 0.99):.4f} max {max(values):.4f}')


def calibrate(gate2dir, gate3dir, gate4dir, cpu=None):
    """Per-operation distributions of the gate statistics, and proposed thresholds with margins over them."""
    a_up, b_down, b_up, a_down = (raw_cells(os.path.join(gate2dir, n), benchtime=bt, order=o, cpu=cpu)
                                  for n, bt, o in GATE2_RUNS)
    cells, rows = gate2_numbers(a_up, b_down, b_up, a_down)
    out = {'gate2': {}, 'gate3': {}, 'gate4': {}}
    print('== calibration: gate 2 (|cA - 1|, |cB - 1|, |r - 1| per operation)')
    for op, op_rows in per_op(cells, rows).items():
        ca = [abs(r['cA'] - 1) for r in op_rows]
        cb = [abs(r['cB'] - 1) for r in op_rows]
        rr = [abs(r['r'] - 1) for r in op_rows]
        med = median([r['r'] for r in op_rows])
        print(f'{op}:\n  |cA-1| {dist(ca)}\n  |cB-1| {dist(cb)}\n  |r-1|  {dist(rr)}\n  median r {med:.4f}')
        out['gate2'][op] = {'ca_p95': percentile(ca, 0.95), 'cb_p95': percentile(cb, 0.95),
                            'r_p95': percentile(rr, 0.95), 'r_max': max(rr), 'median_r': med}

    bt = CANDIDATE['benchtime']
    combined = [raw_cells(os.path.join(gate3dir, f'combined{i}'), benchtime=bt, order='forward', cpu=cpu) for i in (1, 2, 3)]
    isolated = [isolated_cells(os.path.join(gate3dir, f'isolated{i}'), benchtime=bt, order='forward', cpu=cpu)
                for i in (1, 2, 3)]
    cells3 = same_cells(*combined, *isolated)
    c3 = [median([r[c]['ns_per_op'] for r in combined]) / median([r[c]['ns_per_op'] for r in isolated]) for c in cells3]
    print('== calibration: gate 3 (|c - 1| per operation)')
    for op, vals in per_op(cells3, c3).items():
        dev = [abs(v - 1) for v in vals]
        print(f'{op}: {dist(dev)} median c {median(vals):.4f}')
        out['gate3'][op] = {'p95': percentile(dev, 0.95), 'max': max(dev), 'median': median(vals)}

    run1, _ = merged_cells(os.path.join(gate4dir, 'run1'), cpu=cpu, **CANDIDATE)
    run2, _ = merged_cells(os.path.join(gate4dir, 'run2'), cpu=cpu, **CANDIDATE)
    cells4 = same_cells(run1, run2)
    d4 = [run2[c]['ns_per_op'] / run1[c]['ns_per_op'] for c in cells4]
    print('== calibration: gate 4 (|run2 / run1 - 1| per operation)')
    for op, vals in per_op(cells4, d4).items():
        dev = [abs(v - 1) for v in vals]
        print(f'{op}: {dist(dev)} median {median(vals):.4f}')
        out['gate4'][op] = {'p95': percentile(dev, 0.95), 'max': max(dev), 'median': median(vals)}
    out['proposal'] = propose(rows, cells, c3, cells3, d4, [a_up, a_down], [b_up, b_down])
    print('== proposed thresholds (observed statistic × margin, rounded up; see the rule printed with each)')
    print(json.dumps(out['proposal'], indent=2))
    print('== raw calibration statistics')
    print(json.dumps({k: v for k, v in out.items() if k != 'proposal'}, indent=2))
    return out


def round_up(x, step):
    return math.ceil(x / step - 1e-9) * step


def propose(gate2_rows, gate2_cells, gate3_ratios, gate3_cells, gate4_ratios, a_runs, b_runs):
    """Thresholds proposed from the calibration pass, each with the rule that produced it.
    The owner approves or edits them before they are frozen in acceptance_thresholds.json."""
    deviations = [abs(r['r'] - 1) for r in gate2_rows] + [abs(c - 1) for c in gate3_ratios] + [abs(d - 1) for d in gate4_ratios]
    p95 = max(percentile([abs(r['r'] - 1) for r in gate2_rows], 0.95), percentile([abs(c - 1) for c in gate3_ratios], 0.95),
              percentile([abs(d - 1) for d in gate4_ratios], 0.95))
    controls = [max(abs(r['cA'] - 1), abs(r['cB'] - 1)) for r in gate2_rows]
    op_meds = ([abs(median(v) - 1) for v in per_op(gate2_cells, [r['r'] for r in gate2_rows]).values()]
               + [abs(median(v) - 1) for v in per_op(gate3_cells, gate3_ratios).values()])
    t_a = [run[c]['t_ns'] / 1e9 / parse_seconds(GATE2_RUNS[0][1]) for run in a_runs for c in run]
    t_b = [run[c]['t_ns'] / 1e9 / parse_seconds(CANDIDATE['benchtime']) for run in b_runs for c in run]
    return {
        'stable_control': [round_up(percentile(controls, 0.80) * 1.25, 0.005),
                           '80th percentile of max(|cA-1|, |cB-1|) × 1.25, rounded up to 0.5%'],
        'cell_within': [round_up(p95 * 1.25, 0.005), 'largest 95th percentile of |x-1| over gates 2-4 × 1.25, to 0.5%'],
        'cell_max': [round_up(max(deviations) * 1.25, 0.01), 'largest |x-1| over gates 2-4 × 1.25, to 1%'],
        'op_median_within': [round_up(max(op_meds) * 1.5, 0.005),
                             'largest |median - 1| per operation over gates 2 and 3 × 1.5, to 0.5%'],
        'benchtime_t_min': [math.floor(min(t_a + t_b) * 0.95 * 100) / 100, 'smallest t_ns / benchtime × 0.95'],
        'benchtime_t_max': [round_up(max(t_a + t_b) * 1.25, 0.05), 'largest t_ns / benchtime × 1.25, to 0.05'],
        'unchanged': ['stable_share_min', 'stable_share_per_op_min', 'cell_within_share_min', 'bytes_rel', 'bytes_abs',
                      'allocs_abs', 'max_minutes'],
    }


# ---------------------------------------------------------------- driver

def parse_seconds(s):
    m = re.fullmatch(r'(\d+(?:\.\d+)?)(ms|s)', s)
    if not m:
        raise SchemaError(f'benchtime {s!r}: want <n>ms or <n>s')
    return float(m.group(1)) / (1000 if m.group(2) == 'ms' else 1)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('gate', choices=['gate1', 'gate2', 'gate3', 'gate4', 'gate6', 'calibrate'])
    ap.add_argument('dirs', nargs='+')
    ap.add_argument('--thresholds', default=os.path.join(HERE, 'acceptance_thresholds.json'))
    ap.add_argument('--cpu', type=int, default=6, help='the CPU every gate input must be pinned to')
    args = ap.parse_args(argv)
    try:
        if args.gate == 'calibrate':
            calibrate(*args.dirs, cpu=args.cpu)
            return 0
        if args.gate == 'gate1':
            gate1(*args.dirs)
            return 0
        t = load_thresholds(args.thresholds)
        if args.gate == 'gate2':
            if len(args.dirs) != 4:
                raise SchemaError('gate2 takes A_UP B_DOWN B_UP A_DOWN')
            runs = [raw_cells(d, benchtime=bt, order=o, cpu=args.cpu) for d, (_, bt, o) in zip(args.dirs, GATE2_RUNS)]
            gate2(*runs, t, target_a=parse_seconds(GATE2_RUNS[0][1]), target_b=parse_seconds(CANDIDATE['benchtime']))
        elif args.gate == 'gate3':
            if len(args.dirs) != 6:
                raise SchemaError('gate3 takes three combined/isolated pairs')
            bt = CANDIDATE['benchtime']
            combined = [raw_cells(d, benchtime=bt, order='forward', cpu=args.cpu) for d in args.dirs[0::2]]
            isolated = [isolated_cells(d, benchtime=bt, order='forward', cpu=args.cpu) for d in args.dirs[1::2]]
            gate3(combined, isolated, t)
        elif args.gate == 'gate4':
            if len(args.dirs) != 2:
                raise SchemaError('gate4 takes RUN1 RUN2')
            (run1, dis1), (run2, dis2) = (merged_cells(d, cpu=args.cpu, **CANDIDATE) for d in args.dirs)
            gate4(run1, run2, dis1 | dis2, t)
        else:
            gate6(args.dirs, t)
    except GateFailure as exc:
        print(f'acceptance: {exc}', file=sys.stderr)
        return 1
    except (SchemaError, OSError, KeyError) as exc:
        print(f'acceptance: input error: {exc}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
