#!/usr/bin/env python3
"""Self-test of the performance-report tools; SKILL.md runs it before any benchmark.

Usage:
    python3 check_report_tools.py            # run every check; exit status 1 on any failure
    python3 check_report_tools.py --update   # also rewrite testdata/v1/merged_counter.json
    python3 check_report_tools.py -k merge   # only checks whose name contains "merge"

It exercises report_schema.py, merge_layouts.py, generate_report.py, tests/measurev2/layouts.py (through stubs)
and tests/measurev2/acceptance.py with fixtures:
the 2026-10-04 legacy pair in testdata/legacy/, the Go-written raw file in testdata/v1/,
and synthetic raw and merged sets built here from the legacy sizes.
It writes only under a temporary directory (and testdata/v1/ with --update).
See docs/specs/measurev2-fast-report-runs-design.md ("Tests in make test", the check_report_tools.py list).
"""
import argparse
import contextlib
import copy
import io
import json
import math
import os
import re
import shutil
import subprocess
import sys
import tempfile
import traceback

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, '..', '..', '..', '..'))
MEASURE = os.path.join(REPO, 'tests', 'measurev2')
TESTDATA = os.path.join(HERE, 'testdata')
sys.path.insert(0, HERE)
sys.path.insert(0, MEASURE)

import acceptance  # noqa: E402
import generate_report  # noqa: E402
import layouts  # noqa: E402
import merge_layouts  # noqa: E402
import report_schema as rs  # noqa: E402

CHECKS = []
MERGED_FIXTURE = os.path.join(TESTDATA, 'v1', 'merged_counter.json')
RAW_FIXTURE = os.path.join(TESTDATA, 'v1', 'raw_mix_monitoring.json')


def check(fn):
    CHECKS.append(fn)
    return fn


def expect_error(fn, *args, match=None, exc=(rs.SchemaError,), **kw):
    try:
        fn(*args, **kw)
    except exc as e:
        if match and not re.search(match, str(e)):
            raise AssertionError(f'error {e!r} does not match {match!r}') from e
        return str(e)
    raise AssertionError(f'{fn.__name__} accepted invalid input')


def eq(got, want, what=''):
    if got != want:
        raise AssertionError(f'{what}: got {got!r}, want {want!r}')


def ok(cond, what):
    if not cond:
        raise AssertionError(what)


# ---------------------------------------------------------------- fixture factory

def load_legacy():
    main = rs.load_json(os.path.join(TESTDATA, 'legacy', 'main.json'))
    alias = rs.load_json(os.path.join(TESTDATA, 'legacy', 'matrix_mix_monitoring.json'))
    return main, alias


LEGACY_MAIN, LEGACY_ALIAS = load_legacy()
BASE_ROWS = {r['label']: r for r in LEGACY_MAIN['matrix']}
SIZE_KEYS = list(rs.SIZE_FIELDS)


def spec_for(profile):
    if profile == rs.MAIN_PROFILE:
        return copy.deepcopy(LEGACY_MAIN['metadata']['profile_spec'])
    return {'name': profile, 'decimals': 2, 'value_kind': 'gauge', 'interval_ms': 15000}


def data_config(profile):
    return dict(LEGACY_MAIN['metadata']['data_config'], profile=profile)


def make_common(profiles, cells='report', rounds=2, **over):
    c = {
        'run_id': 'run-test', 'source': 'head=0 status=0 tree=0', 'tools': 'sha256=0', 'goos': 'linux',
        'goarch': 'amd64', 'cpu_model': 'Test CPU', 'go_version': 'go1.26.7',
        'build_settings': [{'key': '-buildmode', 'value': 'exe'}], 'gomaxprocs': 1, 'cpu_affinity': [6],
        'gogc': '100', 'gomemlimit': '', 'godebug': '', 'benchtime': '50ms', 'rounds': rounds, 'cells': cells,
        'cells_sha256': rs.cells_digest(rs.manifest_cells(profiles, cells)), 'profiles': list(profiles),
        'data_configs': [data_config(p) for p in profiles],
    }
    c.update(over)
    return c


def raw_op(ns, allocs=10, nbytes=1000, n=100):
    t = int(round(ns * n))
    return {'ns_per_op': t / n, 'bytes_per_op': nbytes, 'allocs_per_op': allocs, 'n': n, 't_ns': t,
            'mem_allocs': allocs * n, 'mem_bytes': nbytes * n}


def base_ns(label, op):
    return BASE_ROWS[label][op]['ns_per_op']


def default_timing(profile, label, op, rnd, layout):
    return base_ns(label, op) * (1 + 0.01 * layout) * (1 + 0.002 * rnd)


def raw_doc(profile, common, rnd, layout, order, timing=default_timing, allocs=None, start=None):
    start = start or f'2026-10-05T10:{rnd:02d}:{layout:02d}+08:00'
    rows = []
    for label in rs.COMBOS:
        row = {k: BASE_ROWS[label][k] for k in SIZE_KEYS}
        for op in rs.timed_ops(profile, common['cells'], label):
            a = allocs(profile, label, op, rnd, layout) if allocs else 10
            row[op] = raw_op(timing(profile, label, op, rnd, layout), allocs=a)
        rows.append(row)
    return {
        'format_version': 1, 'method': rs.METHOD_RAW, 'run_id': common['run_id'], 'common': common,
        'invocation': {'layout': layout, 'round': rnd, 'order': order, 'start': start, 'end': start, 'timestamp': start},
        'raw_raw_bytes': BASE_ROWS['raw-raw']['encoded_bytes'],
        'metadata': {'go_version': 'go1.26.7', 'os': 'linux', 'arch': 'amd64', 'num_cpu': 1, 'timestamp': start,
                     'data_config': data_config(profile), 'profile_spec': spec_for(profile)},
        'matrix': rows,
        'scaling': copy.deepcopy(LEGACY_MAIN['scaling']),
    }


def write_json(path, doc):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'w') as f:
        json.dump(doc, f)


def write_invocation(target, docs, binary='bin', rss=1000):
    """Write one measurev2 -profiles output directory plus the wrapper's record."""
    for profile, doc in docs.items():
        write_json(os.path.join(target, 'profiles', f'matrix_{profile}.json'), doc)
        if profile == rs.MAIN_PROFILE:
            write_json(os.path.join(target, 'main.json'), doc)
    if binary is not None:
        write_json(os.path.join(target, 'wrapper.json'), {'binary_sha256': binary, 'peak_rss_kb': rss, 'cpu': 6})


def raw_set(root, profiles=('mix_monitoring', 'counter'), rounds=2, cells='report', timing=default_timing,
            allocs=None, mutate=None, common=None):
    """A complete raw directory for a layouts.sh schedule; mutate(rnd, layout, docs) may edit the docs."""
    raw = os.path.join(root, 'raw')
    common = common or make_common(list(profiles), cells=cells, rounds=rounds)
    for rnd, layout, order in rs.schedule(rounds):
        docs = {p: raw_doc(p, copy.deepcopy(common), rnd, layout, order, timing, allocs) for p in profiles}
        if mutate:
            mutate(rnd, layout, docs)
        write_invocation(os.path.join(raw, f'r{rnd}_L{layout}'), docs, binary=f'sha-{layout}')
    return raw


def merged_set(root, profiles=tuple(rs.REPORT_PROFILES), **kw):
    raw = raw_set(root, profiles=profiles, **kw)
    merged = merge_layouts.merge(raw)
    merge_layouts.publish(root, merged)
    return os.path.join(root, 'merged')


def legacy_set(root):
    """A complete legacy input set: the 2026-10-04 pair plus the other report profiles copied from its alias."""
    write_json(os.path.join(root, 'main.json'), LEGACY_MAIN)
    for p in rs.REPORT_PROFILES:
        doc = copy.deepcopy(LEGACY_ALIAS)
        doc['metadata']['data_config']['profile'] = p
        doc['metadata']['profile_spec'] = spec_for(p)
        write_json(os.path.join(root, 'profiles', f'matrix_{p}.json'), doc)
    return os.path.join(root, 'main.json'), os.path.join(root, 'profiles')


def quiet(fn, *args, **kw):
    """Run fn with stdout and stderr captured; return (result, text)."""
    buf = io.StringIO()
    with contextlib.redirect_stdout(buf), contextlib.redirect_stderr(buf):
        result = fn(*args, **kw)
    return result, buf.getvalue()


# ---------------------------------------------------------------- manifest and fixtures

@check
def manifest_matches_go():
    """report_schema's manifest equals reportProfiles and reportProfileCombos in tests/measurev2/manifest.go."""
    with open(os.path.join(MEASURE, 'manifest.go')) as f:
        src = f.read()

    def go_list(name):
        m = re.search(name + r' = \[\]string\{(.*?)\n\t\}', src, re.S)
        return re.findall(r'"([^"]+)"', m.group(1))

    eq(go_list('reportProfiles'), rs.REPORT_PROFILES, 'reportProfiles')
    eq(go_list('reportProfileCombos'), rs.REPORT_PROFILE_COMBOS, 'reportProfileCombos')
    eq(len(rs.manifest_cells(rs.REPORT_PROFILES, 'report')), 420, 'report cells')
    eq(len(rs.manifest_cells(rs.REPORT_PROFILES, 'wide')), 780, 'wide cells')
    eq(len(rs.manifest_cells(rs.REPORT_PROFILES, 'full')), 2400, 'full cells')


@check
def go_raw_fixture_validates():
    """The raw file Go writes (TestRawFixtureGolden) satisfies the Python schema: Go writes, Python reads."""
    doc = rs.load_json(RAW_FIXTURE)
    kind, profile = rs.check_doc(doc, RAW_FIXTURE, kind='raw')
    eq((kind, profile), ('raw', 'mix_monitoring'), 'kind')
    op = doc['matrix'][0]['encode']
    eq(op['t_ns'] / op['n'], op['ns_per_op'], 'ns_per_op recomputed from the totals')


@check
def merged_fixture_is_current(update=False):
    """testdata/v1/merged_counter.json is what merge_layouts.py writes today (Go decodes it in TestMergedFixtureDecodes)."""
    with tempfile.TemporaryDirectory() as tmp:
        raw = raw_set(tmp, profiles=('counter',))
        merged = merge_layouts.merge(raw)['profiles/matrix_counter.json']
    text = json.dumps(merged, indent=2) + '\n'
    if update:
        with open(MERGED_FIXTURE, 'w') as f:
            f.write(text)
    with open(MERGED_FIXTURE) as f:
        eq(f.read(), text, 'merged fixture (rerun with --update after a deliberate change)')


# ---------------------------------------------------------------- merge

@check
def merge_pooled_median_and_rounding():
    """ns_per_op is the median of every run, not the median of the layout medians; even counts average;
    allocations round half up; the runs keep their raw totals."""
    values = {(1, 0): 100, (2, 0): 101, (1, 1): 102, (2, 1): 103, (1, 2): 104, (2, 2): 400, (1, 4): 401, (2, 4): 402}

    def timing(profile, label, op, rnd, layout):
        return values[(rnd, layout)] if (label, op) == ('delta-gorilla', 'encode') else default_timing(profile, label, op, rnd, layout)

    def allocs(profile, label, op, rnd, layout):
        return 10 + (rnd + layout) % 2 if (label, op) == ('delta-gorilla', 'encode') else 10

    with tempfile.TemporaryDirectory() as tmp:
        merged = merge_layouts.merge(raw_set(tmp, timing=timing, allocs=allocs))
    doc = merged['profiles/matrix_counter.json']
    op = next(r for r in doc['matrix'] if r['label'] == 'delta-gorilla')['encode']
    eq(op['ns_per_op'], (103 + 104) / 2, 'pooled median of 8 runs (even count averages the middle two)')
    eq(op['layout_ns_per_op'], {'0': 100.5, '1': 102.5, '2': 252.0, '4': 401.5}, 'layout medians')
    ok(rs.median(list(op['layout_ns_per_op'].values())) == 177.25, 'the median of medians would differ')
    eq(op['allocs_per_op'], 11, 'median 10.5 rounds half up')
    ok('counter/delta-gorilla/encode' in doc['allocation_disagreements'], 'allocation disagreement listed')
    eq(len(op['runs']), 8, 'runs')
    eq({k for k in op['runs'][0]}, set(rs.RUN_FIELDS), 'raw totals kept in runs')
    ok(op['iqr_rel'] > 0, 'iqr_rel')
    ok(not doc['allocation_disagreements'] or all(c.startswith('counter/') for c in doc['allocation_disagreements']),
       'disagreements per file')


@check
def merge_rejects_bad_schedules():
    """A missing round, a duplicate (layout, round), a rounds value that disagrees with the schedule,
    and anything else under raw/ are errors."""
    with tempfile.TemporaryDirectory() as tmp:
        raw = raw_set(tmp)
        shutil.rmtree(os.path.join(raw, 'r2_L4'))
        expect_error(merge_layouts.merge, raw, match='schedule')
    with tempfile.TemporaryDirectory() as tmp:
        def dup(rnd, layout, docs):
            if (rnd, layout) == (1, 1):
                for d in docs.values():
                    d['invocation']['layout'] = 0
        expect_error(merge_layouts.merge, raw_set(tmp, mutate=dup), match='invocation is round')
    with tempfile.TemporaryDirectory() as tmp:
        raw = raw_set(tmp, common=make_common(['mix_monitoring', 'counter'], rounds=4))
        expect_error(merge_layouts.merge, raw, match='rounds')
    with tempfile.TemporaryDirectory() as tmp:
        raw = raw_set(tmp)
        os.makedirs(os.path.join(raw, 'notes'))
        expect_error(merge_layouts.merge, raw, match='not an r<R>_L<K>')
    with tempfile.TemporaryDirectory() as tmp:
        def wrong_order(rnd, layout, docs):
            if (rnd, layout) == (2, 1):
                for d in docs.values():
                    d['invocation']['order'] = 'forward'
        expect_error(merge_layouts.merge, raw_set(tmp, mutate=wrong_order), match='order')


@check
def merge_rejects_changed_binary():
    with tempfile.TemporaryDirectory() as tmp:
        raw = raw_set(tmp)
        write_json(os.path.join(raw, 'r2_L1', 'wrapper.json'), {'binary_sha256': 'other', 'peak_rss_kb': 1, 'cpu': 6})
        expect_error(merge_layouts.merge, raw, match='binary SHA-256')


@check
def merge_rejects_each_common_field():
    """Each common-metadata field changed alone in one input fails the merge."""
    changes = {
        'run_id': 'other', 'source': 'x', 'tools': 'x', 'goos': 'darwin', 'goarch': 'arm64', 'cpu_model': 'x',
        'go_version': 'go1.0', 'build_settings': [], 'gomaxprocs': 2, 'cpu_affinity': [7], 'gogc': '50',
        'gomemlimit': '1GiB', 'godebug': 'x=1', 'benchtime': '1s', 'rounds': 4, 'cells': 'wide',
        'cells_sha256': '0' * 64, 'profiles': ['mix_monitoring'], 'data_configs': [],
    }
    eq(set(changes), set(rs.COMMON_FIELDS), 'every common field covered')
    for field, value in changes.items():
        with tempfile.TemporaryDirectory() as tmp:
            def mutate(rnd, layout, docs, field=field, value=value):
                if (rnd, layout) == (2, 2):
                    for d in docs.values():
                        d['common'][field] = value
                        if field == 'run_id':
                            d['run_id'] = value
            expect_error(merge_layouts.merge, raw_set(tmp, mutate=mutate))


@check
def merge_accepts_differing_timestamps():
    with tempfile.TemporaryDirectory() as tmp:
        def mutate(rnd, layout, docs):
            for d in docs.values():
                t = f'2026-10-0{rnd}T0{layout}:00:00+08:00'
                d['invocation'].update(start=t, end=t, timestamp=t)
                d['metadata']['timestamp'] = t
        merged = merge_layouts.merge(raw_set(tmp, mutate=mutate))
    eq(len(merged['main.json']['invocations']), 8, 'invocations kept')


@check
def merge_rejects_bad_cells():
    """A missing cell, N == 0, a non-finite or negative timing, and ns_per_op that disagrees with t_ns / n."""
    def edit(fn):
        def mutate(rnd, layout, docs):
            if (rnd, layout) == (1, 2):
                fn(next(r for r in docs['counter']['matrix'] if r['label'] == 'delta-gorilla'))
        return mutate

    cases = {
        'missing': lambda r: r.pop('iter_seq'),
        'zero n': lambda r: r['encode'].update(n=0),
        'negative': lambda r: r['encode'].update(ns_per_op=-5.0, t_ns=-500),
        'infinite': lambda r: r['encode'].update(ns_per_op=float('inf')),
        'ns mismatch': lambda r: r['encode'].update(ns_per_op=r['encode']['ns_per_op'] * 1.001),
        'alloc totals': lambda r: r['encode'].update(allocs_per_op=r['encode']['allocs_per_op'] + 1),
    }
    for name, fn in cases.items():
        with tempfile.TemporaryDirectory() as tmp:
            expect_error(merge_layouts.merge, raw_set(tmp, mutate=edit(fn)))


@check
def merge_rejects_divergent_alias():
    with tempfile.TemporaryDirectory() as tmp:
        raw = raw_set(tmp)
        path = os.path.join(raw, 'r1_L0', 'main.json')
        doc = rs.load_json(path)
        doc['matrix'][0]['encode'] = raw_op(12345)
        write_json(path, doc)
        expect_error(merge_layouts.merge, raw, match='alias')


@check
def merge_publication_failure_leaves_nothing():
    with tempfile.TemporaryDirectory() as tmp:
        merged = merge_layouts.merge(raw_set(tmp))
        real = json.dump

        def failing(obj, f, **kw):
            if obj.get('metadata', {}).get('data_config', {}).get('profile') == 'counter':
                raise OSError('disk full')
            return real(obj, f, **kw)

        merge_layouts.json.dump = failing
        try:
            expect_error(merge_layouts.publish, tmp, merged, exc=(OSError,))
        finally:
            merge_layouts.json.dump = real
        ok(not os.path.exists(os.path.join(tmp, 'merged')), 'no merged/ after a failed publication')
        ok(not os.path.exists(os.path.join(tmp, '.partial')), 'partial output removed')
        _, out = quiet(merge_layouts.main, [os.path.join(tmp, 'raw')])
        ok(os.path.isdir(os.path.join(tmp, 'merged')), 'a clean retry publishes')
        eq(quiet(merge_layouts.main, [os.path.join(tmp, 'raw')])[0], 1, 'an existing merged/ is never overwritten')


@check
def merge_rejects_wrong_affinity_and_duplicate_dirs():
    """Every input's recorded affinity must be exactly the verified CPU, even when all inputs agree;
    directory names must be canonical, so two directories never claim one (round, layout)."""
    for affinity in ([6, 7], [], [7]):
        with tempfile.TemporaryDirectory() as tmp:
            raw = raw_set(tmp, common=make_common(['mix_monitoring', 'counter'], cpu_affinity=affinity))
            expect_error(merge_layouts.merge, raw, match='verified CPU')
    for name in ('r01_L0', 'r1_L00', 'r0_L0'):
        with tempfile.TemporaryDirectory() as tmp:
            raw = raw_set(tmp)
            shutil.copytree(os.path.join(raw, 'r1_L0'), os.path.join(raw, name))
            expect_error(merge_layouts.merge, raw, match='r<R>_L<K>')


@check
def merged_disagreements_match_runs():
    """allocation_disagreements must list exactly the cells whose runs' allocs/op differ."""
    def allocs(profile, label, op, rnd, layout):
        return 11 if (profile, label, op, layout) == ('counter', 'delta-gorilla', 'encode', 4) else 10

    with tempfile.TemporaryDirectory() as tmp:
        merged = merge_layouts.merge(raw_set(tmp, allocs=allocs))
    doc = merged['profiles/matrix_counter.json']
    eq(doc['allocation_disagreements'], ['counter/delta-gorilla/encode'], 'listed')
    rs.check_doc(doc, 'merged', kind='merged')
    dropped = copy.deepcopy(doc)
    dropped['allocation_disagreements'] = []
    expect_error(rs.check_doc, dropped, 'dropped', match='allocation_disagreements')


@check
def schedules_and_command_are_exact():
    """The two schedules and the pinned invocation, spelled out independently of the code under test."""
    eq(rs.schedule(4), [(1, 0, 'forward'), (1, 1, 'forward'), (1, 2, 'forward'), (1, 4, 'forward'),
                        (2, 1, 'reverse'), (2, 4, 'reverse'), (2, 0, 'reverse'), (2, 2, 'reverse'),
                        (3, 2, 'forward'), (3, 0, 'forward'), (3, 4, 'forward'), (3, 1, 'forward'),
                        (4, 4, 'reverse'), (4, 2, 'reverse'), (4, 1, 'reverse'), (4, 0, 'reverse')], '4 rounds')
    eq(rs.schedule(2), [(1, 0, 'forward'), (1, 1, 'forward'), (1, 2, 'forward'), (1, 4, 'forward'),
                        (2, 4, 'reverse'), (2, 2, 'reverse'), (2, 1, 'reverse'), (2, 0, 'reverse')], '2 rounds')
    args = layouts.parse_args(['-o', 'x'])
    cmd = layouts.invocation_cmd('/b/measurev2_2', 6, args, {'run_id': 'R', 'source': 'S', 'tools': 'T'},
                                 3, 2, 'forward', '/o/raw/r3_L2', '/o/logs/r3_L2.time')
    eq(cmd, ['/usr/bin/time', '-v', '-o', '/o/logs/r3_L2.time',
             'taskset', '-c', '6', 'env', '-u', 'GOMEMLIMIT', 'GOMAXPROCS=1', 'GOGC=100', 'GODEBUG=',
             '/b/measurev2_2', '-profiles', 'report', '-cells', 'report', '-benchtime', '50ms', '-order', 'forward',
             '-rounds', '4', '-run-id', 'R', '-source', 'S', '-tools', 'T', '-layout', '2', '-round', '3',
             '-outdir', '/o/raw/r3_L2'], 'pinned command')


@check
def layouts_four_round_order():
    """A stubbed 4-round run invokes the layouts in the Latin-square order with the round's direction."""
    with tempfile.TemporaryDirectory() as tmp:
        (status, _), runner = run_layouts(tmp, ['-o', os.path.join(tmp, 'out')])
        eq(status, 0, 'exit status')
    eq([os.path.basename(d) for d in runner.outdirs],
       ['r1_L0', 'r1_L1', 'r1_L2', 'r1_L4', 'r2_L1', 'r2_L4', 'r2_L0', 'r2_L2',
        'r3_L2', 'r3_L0', 'r3_L4', 'r3_L1', 'r4_L4', 'r4_L2', 'r4_L1', 'r4_L0'], 'invocation order')
    eq(runner.orders, ['forward'] * 4 + ['reverse'] * 4 + ['forward'] * 4 + ['reverse'] * 4, 'directions')


@check
def layouts_provenance_failure_publishes_nothing():
    with tempfile.TemporaryDirectory() as tmp:
        out = os.path.join(tmp, 'out')
        real = layouts.write_provenance

        def failing(outdir, record):
            if record.get('published'):
                raise OSError('disk full')
            return real(outdir, record)

        layouts.write_provenance = failing
        try:
            expect_error(run_layouts, tmp, ['-o', out, '-rounds', '2'], exc=(OSError,))
        finally:
            layouts.write_provenance = real
        ok(not os.path.exists(os.path.join(out, 'merged')), 'no merged/ when provenance cannot be written')
        eq(rs.load_json(os.path.join(out, 'provenance.json'))['published'], False, 'failure recorded')


@check
def acceptance_inputs_must_be_complete():
    """Gate inputs must be the complete report run the gate specifies: every profile file, no duplicate cells,
    the gate's benchtime and order; an isolated set needs one invocation per report profile."""
    with tempfile.TemporaryDirectory() as tmp:
        common = make_common(list(rs.REPORT_PROFILES), rounds=0)
        docs = {p: raw_doc(p, copy.deepcopy(common), -1, -1, 'forward') for p in rs.REPORT_PROFILES}
        good = os.path.join(tmp, 'good')
        write_invocation(good, docs, binary=None)
        eq(len(acceptance.raw_cells(good, benchtime='50ms', order='forward')), 420, 'complete run')
        expect_error(acceptance.raw_cells, good, benchtime='1s', match='benchtime')
        expect_error(acceptance.raw_cells, good, order='reverse', match='order')
        short = os.path.join(tmp, 'short')
        shutil.copytree(good, short)
        os.remove(os.path.join(short, 'profiles', 'matrix_counter.json'))
        expect_error(acceptance.raw_cells, short, match='one per profile')
        iso = os.path.join(tmp, 'iso')
        for p in rs.REPORT_PROFILES[:-1]:
            c = make_common([p], rounds=0)
            write_invocation(os.path.join(iso, p), {p: raw_doc(p, c, -1, -1, 'forward')}, binary=None)
        expect_error(acceptance.isolated_cells, iso, match='one directory per report profile')
        p = rs.REPORT_PROFILES[-1]
        write_invocation(os.path.join(iso, p), {p: raw_doc(p, make_common([p], rounds=0), -1, -1, 'forward')}, binary=None)
        eq(len(acceptance.isolated_cells(iso)), 420, 'complete isolated set')


@check
def acceptance_gate4_rules():
    """Gate 4 has no operation-median rule (a uniform 4% shift passes),
    and a comparison decided in opposite directions in the two runs fails it."""
    t = thresholds()

    def run(scale, flip=False):
        cells = {}
        for i in range(20):
            for op in ('encode', 'iter_seq'):
                ns = 1000.0 * (1 + i) * (scale if op == 'encode' else 1)
                cells[f'counter/c{i}/{op}'] = dict(mop(ns, [ns] * 4), bytes_per_op=0, allocs_per_op=0)
        if flip:
            for k, ns in (('counter/c0/iter_seq', 2000.0), ('counter/c1/iter_seq', 1000.0)):
                cells[k] = dict(mop(ns, [ns] * 4), bytes_per_op=0, allocs_per_op=0)
        return cells

    quiet(acceptance.gate4, run(1.0), run(1.04), set(), t)
    base = run(1.0)
    flipped = run(1.0, flip=True)
    eq(rs.compare(base['counter/c0/iter_seq'], base['counter/c1/iter_seq'])[:2], ('decided', 'a'), 'run 1 decides c0 faster')
    _, out = quiet(lambda: expect_error(acceptance.gate4, base, flipped, set(), thresholds(cell_max=1.0, cell_within=1.0),
                                        exc=(acceptance.GateFailure,)))
    ok('decided in opposite directions' in out, out)


@check
def acceptance_operation_variance():
    """An operation whose short-benchtime runs disagree with each other fails gate 2's per-operation stable share,
    with a message naming that operation only."""
    a, b_up, b_down = {}, {}, {}
    for op, n in (('encode', 40), ('iter_seq', 10)):
        for i in range(n):
            k = f'counter/c{i}/{op}'
            a[k] = raw_op(1000.0)
            b_up[k] = raw_op(1000.0 * (1.08 if op == 'iter_seq' else 1.0))
            b_down[k] = raw_op(1000.0 * (0.94 if op == 'iter_seq' else 1.0))
    for d, target in ((a, 1.0), (b_up, 0.05), (b_down, 0.05)):
        for v in d.values():
            v['t_ns'] = int(target * 1.1e9)
    buf = io.StringIO()
    with contextlib.redirect_stdout(buf):
        expect_error(acceptance.gate2, a, b_down, b_up, a, thresholds(), exc=(acceptance.GateFailure,))
    fails = [ln for ln in buf.getvalue().splitlines() if ln.startswith('FAIL')]
    ok(any('of iter_seq cells are stable' in f for f in fails), fails)
    ok(not any('encode' in f for f in fails), f'encode is not blamed: {fails}')


@check
def layouts_failure_after_publication_rolls_back():
    """A failure after merged/ was renamed into place removes it again; the stages recorded for gate 6 include publication."""
    with tempfile.TemporaryDirectory() as tmp:
        out = os.path.join(tmp, 'out')
        real = merge_layouts.publish

        def publish_then_fail(outdir, merged):
            real(outdir, merged)
            ok(os.path.isdir(os.path.join(outdir, 'merged')), 'published before the failure')
            raise OSError('stderr closed')

        merge_layouts.publish = publish_then_fail
        try:
            expect_error(run_layouts, tmp, ['-o', out, '-rounds', '2'], exc=(OSError,))
        finally:
            merge_layouts.publish = real
        ok(not os.path.exists(os.path.join(out, 'merged')), 'rolled back')
        eq(rs.load_json(os.path.join(out, 'provenance.json'))['published'], False, 'failure recorded')
    with tempfile.TemporaryDirectory() as tmp:
        out = os.path.join(tmp, 'out')
        run_layouts(tmp, ['-o', out, '-rounds', '2'])
        stages = [st['stage'] for st in rs.load_json(os.path.join(out, 'provenance.json'))['stages']]
        eq(stages[-2:], ['merge', 'publish'], 'publication is a timed stage')


@check
def acceptance_gate6_counts_the_whole_wrapper():
    """Gate 6 uses the wall time validate.sh measured around layouts.sh, start to exit, not the stage timestamps:
    a run whose stages end in time but whose wrapper exits after 15 minutes fails; exactly 15 minutes passes."""
    with tempfile.TemporaryDirectory() as tmp:
        run = os.path.join(tmp, 'run1')
        t = thresholds(max_minutes=15)
        stages = [{'stage': 'build', 'start': 0, 'end': 60, 'ok': True},
                  {'stage': 'publish', 'start': 890, 'end': 899, 'ok': True}]
        write_json(os.path.join(run, 'provenance.json'),
                   {'stages': stages, 'movement': 3, 'invocations': [], 'published': True})
        with open(run + '.wall', 'w') as f:
            f.write('1000.0 1905.0\n')
        expect_error(quiet, acceptance.gate6, [run], t, exc=(acceptance.GateFailure,))
        with open(run + '.wall', 'w') as f:
            f.write('1000.0 1900.0\n')
        quiet(acceptance.gate6, [run], t)
        for bad in ('0 nan\n', '0 inf\n', '1000 0\n', '1000\n'):
            with open(run + '.wall', 'w') as f:
                f.write(bad)
            expect_error(acceptance.gate6, [run], t)
        os.remove(run + '.wall')
        expect_error(acceptance.gate6, [run], t, match='wall time')


@check
def acceptance_inputs_must_be_one_pinned_invocation():
    """A combined directory is one invocation: a file from another invocation is rejected,
    and so is any input not pinned to the gate's CPU with GOMAXPROCS=1 and the wrapper's runtime environment."""
    with tempfile.TemporaryDirectory() as tmp:
        def invocation(path, run_id='run-a', **over):
            common = make_common(list(rs.REPORT_PROFILES), rounds=0, run_id=run_id, **over)
            docs = {p: raw_doc(p, copy.deepcopy(common), -1, -1, 'forward') for p in rs.REPORT_PROFILES}
            for d in docs.values():
                d['run_id'] = run_id
            write_invocation(path, docs, binary=None)

        invocation(os.path.join(tmp, 'a'))
        invocation(os.path.join(tmp, 'b'), run_id='run-b')
        eq(len(acceptance.raw_cells(os.path.join(tmp, 'a'), cpu=6)), 420, 'one complete pinned invocation')
        shutil.copy(os.path.join(tmp, 'b', 'profiles', 'matrix_counter.json'), os.path.join(tmp, 'a', 'profiles'))
        expect_error(acceptance.raw_cells, os.path.join(tmp, 'a'), match='differs from the other files')
        for name, over in (('procs', {'gomaxprocs': 2}), ('mask', {'cpu_affinity': [6, 7]}), ('empty', {'cpu_affinity': []}),
                           ('gogc', {'gogc': '50'}), ('limit', {'gomemlimit': '1GiB'}), ('debug', {'godebug': 'x=1'})):
            invocation(os.path.join(tmp, name), **over)
            expect_error(acceptance.raw_cells, os.path.join(tmp, name), cpu=6)
        invocation(os.path.join(tmp, 'cpu7'), cpu_affinity=[7])
        expect_error(acceptance.raw_cells, os.path.join(tmp, 'cpu7'), cpu=6, match='want exactly')


@check
def calibration_proposal_uses_gate3_medians():
    """op_median_within takes the largest per-operation median deviation over gates 2 and 3,
    and the proposal accounts for every threshold key."""
    cells = [f'counter/c{i}/encode' for i in range(10)]
    rows = [{'cell': c, 'r': 1.0, 'cA': 1.0, 'cB': 1.0} for c in cells]
    runs = [{c: {'t_ns': 1.1e9} for c in cells}]
    shorts = [{c: {'t_ns': 0.055e9} for c in cells}]
    proposal = acceptance.propose(rows, cells, [1.04] * 10, cells, [1.0] * 10, runs, shorts)
    eq(proposal['op_median_within'][0], 0.06, 'gate 3 alone sets the margin (4% × 1.5)')
    accounted = (set(proposal) - {'unchanged'}) | set(proposal['unchanged'])
    eq(accounted, acceptance.THRESHOLD_KEYS, 'the proposal proposes or keeps every threshold')


# ---------------------------------------------------------------- comparison rule

def mop(ns, layouts_ns=None):
    op = {'ns_per_op': ns}
    if layouts_ns is not None:
        op['layout_ns_per_op'] = {str(k): v for k, v in zip(rs.LAYOUTS, layouts_ns)}
    return op


@check
def comparison_rule():
    eq(rs.compare(mop(100, [90, 95, 99, 98]), mop(130, [80, 85, 89, 88]))[0], 'inconclusive',
       'every layout contradicts the pooled winner')
    eq(rs.compare(mop(100, [100, 100, 100, 100]), mop(130, [100, 140, 140, 140]))[0], 'inconclusive', 'one equal layout')
    eq(rs.compare(mop(100, [100, 100, 100, 150]), mop(130, [130, 130, 130, 120]))[0], 'inconclusive', 'one reversed layout')
    eq(rs.compare(mop(100, [100] * 4), mop(120, [120] * 4)), ('decided', 'a', rs.compare(mop(100), mop(120))[2]),
       'a gap at exactly 20% is decided')
    eq(rs.compare(mop(100), mop(119.9))[0], 'equivalent', 'under 20%')
    eq(rs.compare(mop(100), mop(125)), ('decided', 'a', 0.25), 'legacy above 20%')
    eq(rs.compare(mop(125), mop(100))[1], 'b', 'winner is the smaller')
    eq(rs.compare(mop(100), mop(110))[0], 'equivalent', 'legacy below 20%')
    expect_error(rs.compare, mop(100, [1, 1, 1, 1]), mop(200), match='merged')


def ds_with(rows, merged=True, name='counter', disagreements=()):
    doc = {'metadata': LEGACY_MAIN['metadata'], 'matrix': rows, 'allocation_disagreements': list(disagreements)}
    if merged:
        doc['format_version'] = 1
    return generate_report.Dataset(name, doc)


def row(label, size, **ops):
    r = {k: BASE_ROWS[label][k] for k in SIZE_KEYS}
    r['bytes_per_point'] = size
    r.update(ops)
    return r


@check
def rankings_compare_with_the_fastest_only():
    """A chain whose neighbours are equivalent but whose ends differ: each entry is compared with the fastest only.
    A noisy fastest cell makes its competitors inconclusive."""
    chain = ds_with([
        row('raw-raw', 1, encode=mop(100, [100] * 4)),
        row('raw-gorilla', 2, encode=mop(115, [115] * 4)),
        row('raw-chimp', 3, encode=mop(130, [130] * 4)),
    ])
    text = generate_report.ranking(chain, 'encode')
    ok('Raw + Gorilla 115 ns (equivalent to the fastest)' in text, text)
    ok('Raw + Chimp 130 ns (slower)' in text, text)
    noisy = ds_with([
        row('raw-raw', 1, encode=mop(100, [60, 70, 200, 210])),
        row('raw-gorilla', 2, encode=mop(130, [130] * 4)),
    ])
    ok('inconclusive against the fastest' in generate_report.ranking(noisy, 'encode'), 'noisy fastest')
    ok(generate_report.ranking(ds_with([row('raw-raw', 1)]), 'decode') == 'not timed', 'untimed ranking')


@check
def pareto_and_references():
    """Both fronts: equal sizes within 20% stay; an inconclusive comparison never beats; equivalent codecs
    that are both decisively slower; a point front that changes under a tiny perturbation."""
    rows = [
        row('shared-delta-alprle', 2.0, iter_seq=mop(200, [200] * 4)),
        row('shared-deltapacked-alprle', 2.0, iter_seq=mop(210, [210] * 4)),
        row('shared-raw-alp', 2.5, iter_seq=mop(100, [100, 100, 100, 200])),
        row('raw-alp', 9.0, iter_seq=mop(150, [150] * 4)),
        row('raw-raw', 16.0, iter_seq=mop(150, [150] * 4)),
    ]
    front = [r['label'] for r, _ in generate_report.decided_front(rows, 'iter_seq')]
    ok('shared-delta-alprle' in front and 'shared-deltapacked-alprle' in front, f'equal sizes within 20% both stay: {front}')
    ok('raw-alp' in front, f'an inconclusive comparison never beats: {front}')
    ok('raw-raw' not in front, f'beaten by a strictly smaller, equivalent combo: {front}')
    notes = dict((r['label'], n) for r, n in generate_report.decided_front(rows, 'iter_seq'))
    ok('shared-raw-alp' in front, 'the smaller, pooled-faster combo stays')
    eq(rs.compare(rows[2]['iter_seq'], rows[3]['iter_seq'])[0], 'inconclusive', 'fixture: Shared Raw + ALP vs Raw + ALP')
    eq(notes['raw-alp'], ['Shared Raw + ALP'], 'the larger, slower survivor is annotated')
    eq(notes['shared-raw-alp'], ['Shared Delta + ALP-RLE', 'Raw + ALP'],
       'and so is the smaller one, in both directions (layout 4 ties it with Shared Delta + ALP-RLE)')
    point = generate_report.pareto(rows, lambda r: r['bytes_per_point'], lambda r: r['iter_seq']['ns_per_op'])
    rows[1]['iter_seq'] = mop(199.9, [199.9] * 4)
    point2 = generate_report.pareto(rows, lambda r: r['bytes_per_point'], lambda r: r['iter_seq']['ns_per_op'])
    ok([r['label'] for r in point] != [r['label'] for r in point2], 'point front changes under a tiny perturbation')
    eq([r['label'] for r, _ in generate_report.decided_front(rows, 'iter_seq')], front, 'decided front is stable')

    slow = ds_with([
        row('raw-raw', 1, encode=mop(100, [100] * 4)),
        row('raw-gorilla', 2, encode=mop(200, [200] * 4)),
        row('raw-chimp', 3, encode=mop(210, [210] * 4)),
    ])
    eq(rs.compare(slow.rows['raw-gorilla']['encode'], slow.rows['raw-chimp']['encode'])[0], 'equivalent', 'the two slow ones')
    text = generate_report.ranking(slow, 'encode')
    ok(text.count('(slower)') == 2, f'both decisively slower than the fastest: {text}')

    refs = ds_with([
        row('shared-deltapacked-chimp', 3.8, encode=mop(200, [200] * 4), iter_seq=mop(130, [130] * 4),
            random_value_at=mop(40000, [40000] * 4)),
        row('delta-gorilla', 9.0, encode=mop(100, [100] * 4), iter_seq=mop(140, [140] * 4),
            random_value_at=mop(50000, [50000] * 4)),
        row('shared-delta-alprle', 2.5, encode=mop(300, [300] * 4), iter_seq=mop(90, [90] * 4),
            random_value_at=mop(2000, [2000] * 4)),
    ] + [row(lab, 20 + i) for i, lab in enumerate(['raw-raw', 'raw-gorilla', 'raw-chimp'])])
    lines = '\n'.join(generate_report.digest_dataset('t', refs))
    ok('encode 1.50× (slower)' in lines and 'encode 3.00× (slower)' in lines, f'both references compared: {lines}')
    ok('Against production-like reference' in lines and 'Against NewDefaultNumericEncoder' in lines, 'both labelled')
    ok('encode not timed' in lines, 'untimed rows reported as not timed')


# ---------------------------------------------------------------- schema

@check
def schema_rejections():
    doc = rs.load_json(RAW_FIXTURE)
    bad = copy.deepcopy(doc)
    bad['matrix'][0]['encode'] = None
    expect_error(rs.check_doc, bad, 'null', match='null')
    bad = copy.deepcopy(doc)
    bad['matrix'][0]['encode']['n'] = 2.0
    expect_error(rs.check_doc, bad, 'type', match='want int')
    bad = copy.deepcopy(doc)
    bad['matrix'][0]['encode']['bytes_per_op'] = True
    expect_error(rs.check_doc, bad, 'bool', match='want int')
    for field in rs.COMMON_FIELDS:
        bad = copy.deepcopy(doc)
        del bad['common'][field]
        expect_error(rs.check_doc, bad, field, match='missing field')
    bad = copy.deepcopy(doc)
    del bad['run_id']
    expect_error(rs.check_doc, bad, 'run_id')
    bad = copy.deepcopy(doc)
    bad['format_version'] = 2
    expect_error(rs.check_doc, bad, 'version', match='unknown format_version')


@check
def size_identities():
    """Each derived size field corrupted while encoded_bytes is kept, and a wrong total_points, are rejected;
    shared combos keep the per-metric raw-raw size as their baseline."""
    for field, factor in (('bytes_per_point', 1.0001), ('vs_raw_ratio', 1.0001), ('space_savings_pct', 1.0001)):
        bad = copy.deepcopy(LEGACY_MAIN)
        bad['matrix'][7][field] *= factor
        expect_error(rs.check_doc, bad, field, match=field)
    bad = copy.deepcopy(LEGACY_MAIN)
    bad['matrix'][3]['total_points'] += 1
    expect_error(rs.check_doc, bad, 'total', match='total_points')
    shared = BASE_ROWS['shared-raw-raw']
    ok(math.isclose(shared['vs_raw_ratio'], BASE_ROWS['raw-raw']['encoded_bytes'] / shared['encoded_bytes'], rel_tol=1e-12),
       'shared combos use the per-metric raw-raw size')
    rs.check_doc(LEGACY_MAIN, 'legacy main')


# ---------------------------------------------------------------- input sets

@check
def input_sets():
    with tempfile.TemporaryDirectory() as tmp:
        raw = raw_set(tmp, profiles=tuple(rs.REPORT_PROFILES))
        r1 = os.path.join(raw, 'r1_L0')
        expect_error(rs.load_input_set, os.path.join(r1, 'main.json'), os.path.join(r1, 'profiles'), match='raw')
        merge_layouts.publish(tmp, merge_layouts.merge(raw))
        m = os.path.join(tmp, 'merged')
        kind, _, docs = rs.load_input_set(os.path.join(m, 'main.json'), os.path.join(m, 'profiles'))
        eq((kind, len(docs)), ('merged', 16), 'an equal main.json and alias pair is accepted')

        def variant(name, fn, match=None):
            v = os.path.join(tmp, name)
            shutil.copytree(m, v)
            fn(v)
            expect_error(rs.load_input_set, os.path.join(v, 'main.json'), os.path.join(v, 'profiles'), match=match)

        def edit(path, fn):
            doc = rs.load_json(path)
            fn(doc)
            write_json(path, doc)

        variant('nolayout', lambda v: edit(os.path.join(v, 'profiles', 'matrix_counter.json'),
                                           lambda d: d['layouts'].pop('4')), match='layouts')
        variant('noround', lambda v: edit(os.path.join(v, 'profiles', 'matrix_counter.json'),
                                          lambda d: d['invocations'].pop()), match='invocations')
        variant('nolayoutns', lambda v: edit(os.path.join(v, 'profiles', 'matrix_counter.json'),
                                             lambda d: d['matrix'][-1]['encode'].pop('layout_ns_per_op')))
        other = os.path.join(tmp, 'other')
        os.makedirs(other)
        merge_layouts.publish(other, merge_layouts.merge(raw_set(
            other, profiles=tuple(rs.REPORT_PROFILES),
            timing=lambda *a: default_timing(*a) * 1.5)))
        rs.check_doc(rs.load_json(os.path.join(other, 'merged', 'main.json')), 'other main', kind='merged')
        variant('divergent', lambda v: shutil.copy(os.path.join(other, 'merged', 'main.json'), os.path.join(v, 'main.json')),
                match='differs from matrix_mix_monitoring')
        variant('allocs', lambda v: edit(os.path.join(v, 'profiles', 'matrix_counter.json'),
                                         lambda d: d['matrix'][-1]['encode'].update(allocs_per_op=0)), match='half-up')
        variant('bytes', lambda v: edit(os.path.join(v, 'profiles', 'matrix_counter.json'),
                                        lambda d: d['matrix'][-1]['encode'].update(bytes_per_op=-1)), match='half-up')
        variant('iqr', lambda v: edit(os.path.join(v, 'profiles', 'matrix_counter.json'),
                                      lambda d: d['matrix'][-1]['encode'].update(iqr_rel=0.5)), match='iqr_rel')
        variant('disagree+', lambda v: edit(os.path.join(v, 'profiles', 'matrix_counter.json'),
                                            lambda d: d['allocation_disagreements'].append('counter/raw-raw/encode')),
                match='allocation_disagreements')
        variant('affinity', lambda v: [edit(os.path.join(v, f), lambda d: d['common'].update(cpu_affinity=[6, 7]))
                                       for f in ['main.json'] + [f'profiles/matrix_{p}.json' for p in rs.REPORT_PROFILES]],
                match='exactly one CPU')
        variant('second', lambda v: shutil.copy(os.path.join(v, 'profiles', 'matrix_counter.json'),
                                                os.path.join(v, 'profiles', 'matrix_counter_copy.json')))
        variant('missing', lambda v: os.remove(os.path.join(v, 'profiles', 'matrix_worst_case.json')), match='missing')
        variant('dupcombo', lambda v: edit(os.path.join(v, 'profiles', 'matrix_counter.json'),
                                           lambda d: d['matrix'].append(copy.deepcopy(d['matrix'][0]))))
        variant('norunid', lambda v: edit(os.path.join(v, 'profiles', 'matrix_counter.json'), lambda d: d.pop('run_id')))
        variant('hash', lambda v: edit(os.path.join(v, 'profiles', 'matrix_counter.json'),
                                       lambda d: d['common'].update(cells_sha256='0' * 64)), match='cells_sha256')
        variant('mixed', lambda v: write_json(os.path.join(v, 'profiles', 'matrix_counter.json'),
                                              dict(copy.deepcopy(LEGACY_ALIAS), metadata=dict(
                                                  LEGACY_ALIAS['metadata'], data_config=data_config('counter')))),
                match='mixed')
        variant('version', lambda v: edit(os.path.join(v, 'main.json'), lambda d: d.update(format_version=9)),
                match='format_version')


@check
def legacy_and_sizes_only_inputs():
    """The 2026-10-04 legacy pair, whose main and mix_monitoring timings differ, renders;
    the same divergence under one run_id is rejected (see input_sets); -sizes-only output is rejected."""
    ok(LEGACY_MAIN['matrix'][0]['encode'] != LEGACY_ALIAS['matrix'][0]['encode'], 'fixture pair differs in timings')
    with tempfile.TemporaryDirectory() as tmp:
        main, profiles = legacy_set(tmp)
        kind, _, _ = rs.load_input_set(main, profiles)
        eq(kind, 'legacy', 'legacy pair accepted')
        sizes = copy.deepcopy(rs.load_json(RAW_FIXTURE))
        sizes['method'] = rs.METHOD_SIZES_ONLY
        for r in sizes['matrix']:
            for op in rs.OPS:
                r.pop(op, None)
        rs.check_doc(sizes, 'sizes-only')
        write_json(main, sizes)
        expect_error(rs.load_input_set, main, profiles, match='sizes-only')


# ---------------------------------------------------------------- rendering

def render_set(main_path, profiles_dir):
    main_ds, profiles = generate_report.load(main_path, profiles_dir)
    with open(os.path.join(HERE, '..', 'PERFORMANCE_TEMPLATE.md')) as f:
        template = f.read()
    return generate_report.render(main_ds, profiles, template), generate_report.gen_digest(main_ds, profiles)


@check
def sparse_rendering():
    """A complete merged report run: every main table, all 30-combo size grids, profile rankings without decode
    or TimestampAt timings, untimed reference rows, both fronts, and allocation disagreements marked."""
    def allocs(profile, label, op, rnd, layout):
        return 10 + (layout == 4) if (profile, label, op) == ('mix_monitoring', 'raw-raw', 'encode') else 10

    with tempfile.TemporaryDirectory() as tmp:
        m = merged_set(tmp, allocs=allocs)
        out, digest = render_set(os.path.join(m, 'main.json'), os.path.join(m, 'profiles'))
    ok(not [p for p in generate_report.ANY_PLACEHOLDER.findall(out) if not generate_report.LLM_PLACEHOLDER.fullmatch(p)],
       'every table placeholder filled')
    eq(out.count('#### '), 16, 'one size grid per profile')
    ok('Layout-averaged `testing.Benchmark`: 4 code layouts × 2 rounds' in out, 'methodology from metadata')
    ok('layouts.sh -o $TMPDIR/perf -cpu 6 -benchtime 50ms -cells report -rounds 2' in out, 'reproduction command')
    ok('| Raw + Raw |' in out and '10 ‡' in out, 'allocation disagreement marked in the encode table')
    ok('Fastest decode (open) (among timed combos): not timed' in digest, 'profiles without decode timings')
    ok('Fastest TimestampAt (among timed combos): not timed' in digest, 'profiles without TimestampAt timings')
    ok('iterate not timed' in digest or 'encode not timed' in digest, 'untimed reference rows')
    ok('point estimates' in digest and 'decided front' in digest, 'both fronts')
    section = digest.split('## Profile: counter')[1].split('## Profile:')[0]
    ok('Smallest, shared timestamps' in section, 'complete size rankings on a sparse profile')
    front = [ln for ln in section.splitlines() if 'decided front' in ln][0]
    ok('Shared Delta + ALP-RLE' not in front, 'an untimed smallest combo stays off the fronts')


@check
def zero_allocations_stay_distinct():
    ds = ds_with([row('raw-raw', 1, encode=dict(raw_op(100, allocs=0, nbytes=0)))])
    table = generate_report.gen_perf_table(ds, 'encode')
    ok('| Raw + Raw | 100 | 0 | 0 |' in table, table)
    ok('Raw + Raw' not in generate_report.gen_perf_table(ds, 'decode'), 'an untimed cell has no row')
    eq(generate_report.fmt_ns(row('raw-raw', 1), 'decode'), generate_report.UNTIMED, 'untimed marker')


@check
def legacy_rendering():
    with tempfile.TemporaryDirectory() as tmp:
        out, digest = render_set(*legacy_set(tmp))
    ok('Single `testing.Benchmark` runs at the default 1 s benchtime' in out, 'legacy described as single runs')
    ok('layout' not in out.split('### Running Benchmarks')[1].split('##')[0], 'no layout fields invented')
    ok('go run . -pretty -verbose -output results.json' in out, 'legacy reproduction block')


# ---------------------------------------------------------------- acceptance checker

def thresholds(**over):
    t = rs.load_json(os.path.join(MEASURE, 'acceptance_thresholds.json'))
    t.update(over)
    return t


def cells_map(ratios, base=1000.0, op='encode', allocs=10):
    return {f'counter/c{i}/{op}': dict(raw_op(base * r), allocs_per_op=allocs) for i, r in enumerate(ratios)}


@check
def acceptance_thresholds_and_boundaries():
    """Thresholds come from acceptance_thresholds.json; exactly 5%, 10%, 80% and 95% pass, just beyond fails."""
    t = acceptance.load_thresholds(os.path.join(MEASURE, 'acceptance_thresholds.json'))
    ok({'cell_within', 'cell_max', 'stable_share_min', 'cell_within_share_min'} <= set(t), 'threshold keys')
    expect_error(acceptance.load_thresholds, os.path.join(TESTDATA, 'legacy', 'main.json'), match='missing thresholds')

    def run4(ratios):
        run1 = {f'counter/x{i}/encode': mop(1000.0) for i in range(len(ratios))}
        run2 = {f'counter/x{i}/encode': mop(1000.0 * r) for i, r in enumerate(ratios)}
        for d in (run1, run2):
            for v in d.values():
                v.update(bytes_per_op=0, allocs_per_op=0)
        return acceptance.gate4(run1, run2, set(), bounds)

    # The boundaries are pinned here, independent of the approved file:
    # 19 of 20 cells within 5% is exactly 95%; the 20th at exactly 10%; medians stay near 1.
    bounds = thresholds(cell_within=0.05, cell_within_share_min=0.95, cell_max=0.10)
    passing = [1.05] + [1.0] * 9 + [0.95] + [1.0] * 8 + [1.10]
    quiet(run4, passing)
    expect_error(quiet, run4, [1.0] * 19 + [1.1001], exc=(acceptance.GateFailure,))
    expect_error(quiet, run4, [1.0] * 18 + [1.06, 1.06], exc=(acceptance.GateFailure,))

    # Gate 2's stable share at exactly 80% passes; one cell less fails.
    def run2(stable, total=10):
        a = {f'counter/x{i}/encode': raw_op(1000.0) for i in range(total)}
        a_down = {k: raw_op(1000.0 if i < stable else 1100.0) for i, k in enumerate(a)}
        b = {k: dict(raw_op(1000.0), n=v['n'], t_ns=v['t_ns']) for k, v in a.items()}
        for d, target in ((a, 1.0), (a_down, 1.0), (b, 0.05)):
            for v in d.values():
                v['t_ns'] = int(target * 1.1e9)
                v['n'] = max(1, int(v['t_ns'] / v['ns_per_op']))
        return acceptance.gate2(a, b, b, a_down, thresholds(cell_max=0.2, stable_share_min=0.80, stable_control=0.05))

    quiet(run2, 8)
    expect_error(quiet, run2, 7, exc=(acceptance.GateFailure,))


@check
def acceptance_operation_specific_failure():
    """An operation whose short-benchtime timings are systematically worse fails with a message naming it."""
    t = thresholds()
    a = {}
    for op in ('encode', 'iter_seq'):
        for i in range(10):
            a[f'counter/c{i}/{op}'] = raw_op(1000.0)
    b = {k: raw_op(1000.0 * (1.04 if k.endswith('iter_seq') else 1.0)) for k in a}
    for d, target in ((a, 1.0), (b, 0.05)):
        for v in d.values():
            v['t_ns'] = int(target * 1.1e9)
    buf = io.StringIO()
    with contextlib.redirect_stdout(buf):
        expect_error(acceptance.gate2, a, b, b, a, t, exc=(acceptance.GateFailure,))
    fails = [ln for ln in buf.getvalue().splitlines() if ln.startswith('FAIL')]
    ok(any('iter_seq: median r 1.0400 is outside ±3%' in f and 'biases iter_seq' in f for f in fails), fails)
    ok(not any(f.startswith('FAIL encode') for f in fails), f'encode is not blamed: {fails}')


@check
def acceptance_gate3_allocations():
    """Combined allocations one away from the isolated range pass gate 3
    (allocs/op truncates a mean that can sit either side of an integer); two away fail it."""
    t = thresholds(allocs_abs=1)
    iso = [cells_map([1.0] * 20) for _ in range(3)]
    comb = [cells_map([1.0] * 20) for _ in range(3)]
    comb[1]['counter/c3/encode']['allocs_per_op'] = 11
    quiet(acceptance.gate3, comb, iso, t)
    comb[1]['counter/c3/encode']['allocs_per_op'] = 12
    _, out = quiet(lambda: expect_error(acceptance.gate3, comb, iso, t, exc=(acceptance.GateFailure,)))
    ok('combined run 2 allocs/op 12 outside isolated [10, 10] ± 1' in out, out)


@check
def acceptance_gate2_allocations():
    """Short-benchtime allocations one away from A's range pass gate 2; two away fail it."""
    t = thresholds(allocs_abs=1)
    a = {f'counter/c{i}/encode': raw_op(1000.0) for i in range(20)}
    b = {k: raw_op(1000.0) for k in a}
    for d, target in ((a, 1.0), (b, 0.05)):
        for v in d.values():
            v['t_ns'] = int(target * 1.1e9)
    b['counter/c3/encode']['allocs_per_op'] = 9
    quiet(acceptance.gate2, a, b, b, a, t)
    b['counter/c3/encode']['allocs_per_op'] = 8
    _, out = quiet(lambda: expect_error(acceptance.gate2, a, b, b, a, t, exc=(acceptance.GateFailure,)))
    ok("allocs/op 8 outside A's [10, 10] ± 1" in out, out)


@check
def acceptance_gate2_holds_shared_timestamp_at():
    """Gate 2 holds every cell to cell_max, random_timestamp_at on shared timestamps included:
    the exemption for its two speeds ended with the pointer lookups of docs/specs/index-entry-by-pointer-design.md."""
    t = thresholds(cell_within=0.05, cell_within_share_min=0.95, cell_max=0.11)

    def run(*moved):
        a, a_down, b = {}, {}, {}
        for i in range(20):
            for combo in (f'shared-c{i}', f'c{i}'):
                cell = f'mix_monitoring/{combo}/random_timestamp_at'
                a[cell] = raw_op(1000.0 * (1.29 if cell in moved else 1.0))
                a_down[cell] = raw_op(1000.0)
                b[cell] = raw_op(1000.0)
        for d, target in ((a, 1.0), (a_down, 1.0), (b, 0.05)):
            for v in d.values():
                v['t_ns'] = int(target * 1.1e9)
        return acceptance.gate2(a, b, b, a_down, t)

    _, out = quiet(run)
    ok('worst |r - 1| over all cells: 0.00%' in out, out)
    for cell in ('mix_monitoring/shared-c0/random_timestamp_at', 'mix_monitoring/c0/random_timestamp_at'):
        _, out = quiet(lambda: expect_error(run, cell, exc=(acceptance.GateFailure,)))
        ok('1 cells beyond ±11%' in out and 'exempt' not in out, out)


@check
def acceptance_gate4_holds_shared_timestamp_at():
    """Gate 4 holds every cell to cell_max, random_timestamp_at on shared timestamps included."""
    t = thresholds(cell_within=0.05, cell_within_share_min=0.95, cell_max=0.11)

    def runs(moved):
        run1, run2 = {}, {}
        for i in range(40):
            for combo in (f'shared-c{i}', f'c{i}'):
                cell = f'mix_monitoring/{combo}/random_timestamp_at'
                ns = 1000.0 * (1 + i)
                ns2 = ns * (1.29 if cell in moved else 1.0)
                run1[cell] = dict(mop(ns, [ns] * 4), bytes_per_op=0, allocs_per_op=0)
                run2[cell] = dict(mop(ns2, [ns2] * 4), bytes_per_op=0, allocs_per_op=0)
        return run1, run2

    _, out = quiet(acceptance.gate4, *runs(set()), set(), t)
    ok('worst 0.00% (limit 11%)' in out and 'exempt' not in out, out)
    for cell in ('mix_monitoring/shared-c0/random_timestamp_at', 'mix_monitoring/c0/random_timestamp_at'):
        _, out = quiet(lambda: expect_error(acceptance.gate4, *runs({cell}), set(), t, exc=(acceptance.GateFailure,)))
        ok('1 cells beyond ±11%' in out, out)


# ---------------------------------------------------------------- layouts.sh through stubs

def nm_table(k, funcs):
    """A fake `go tool nm -n -size` listing: the padding (k > 0) shifts every repository function after it."""
    lines, addr = [], 0x4d3dc0
    entries = list(funcs)
    if k:
        entries = [(f'{layouts.MODULE}/format.init', 46), (f'{layouts.MODULE}/format.layoutPad', 24 * k + 1)] + entries
    for name, size in entries:
        lines.append(f'  {addr:x} {size:10d} T {name}')
        addr += -(-size // 32) * 32
    return '\n'.join(['  401000        222 T internal/abi.BoundsDecode'] + lines) + '\n'


FUNCS = [(f'{layouts.MODULE}/format.EncodingType.String', 150), (f'{layouts.MODULE}/blob.(*E).Add', 700),
         ('main.encodeBody.func1', 90)]

VERSION_OK = '\tbuild\t-buildmode=exe\n\tbuild\t-trimpath=true\n\tbuild\tCGO_ENABLED=0\n'


class StubRunner:
    """Stands in for git, go, taskset and the pinned invocations of layouts.py."""

    def __init__(self, tmp, mask='6', version=VERSION_OK, funcs=FUNCS, nm=None, fail=None, measure=True, pre_create=None):
        self.tmp, self.mask, self.version, self.funcs, self.nm, self.fail = tmp, mask, version, funcs, nm, fail
        self.measure, self.pre_create = measure, pre_create
        self.outdirs, self.orders = [], []

    def run(self, cmd, cwd=None, capture=True):
        if self.fail and self.fail in ' '.join(cmd):
            raise layouts.LayoutError(f'stub failure: {self.fail}')
        if cmd[0] == 'git':
            return subprocess.run(cmd, capture_output=True, text=True, check=True).stdout
        if cmd[:2] == ['taskset', '-c'] and cmd[3] == 'cat':
            return f'Name:\tcat\nCpus_allowed_list:\t{self.mask}\n'
        if cmd[0] == 'env' and 'build' in cmd:
            out = cmd[cmd.index('-o') + 1]
            k = self.pad_steps(cwd)
            with open(out, 'w') as f:
                f.write(f'binary layout {k}')
            return ''
        if cmd[:3] == ['go', 'version', '-m']:
            return self.version
        if cmd[:3] == ['go', 'tool', 'nm']:
            with open(cmd[-1]) as f:
                k = int(f.read().rsplit(' ', 1)[1])
            return (self.nm or nm_table)(k, self.funcs)
        if cmd[:2] == ['go', 'list']:
            tree = cwd.rsplit('/tests/measurev2', 1)[0]
            return f'{tree}/format\tformat\n'
        if cmd[0] == '/usr/bin/time':
            return self.invoke(cmd)
        raise AssertionError(f'unexpected command {cmd}')

    @staticmethod
    def pad_steps(pkgdir):
        pad = os.path.join(pkgdir.rsplit('/tests/measurev2', 1)[0], 'format', layouts.PAD_FILE)
        if not os.path.exists(pad):
            return 0
        with open(pad) as f:
            return f.read().count('x = (x^')

    def invoke(self, cmd):
        with open(cmd[3], 'w') as f:
            f.write('\tMaximum resident set size (kbytes): 4321\n')
        args = dict(zip(cmd[cmd.index('-profiles')::2], cmd[cmd.index('-profiles') + 1::2]))
        outdir = args['-outdir']
        self.outdirs.append(outdir)
        self.orders.append(args['-order'])
        os.mkdir(outdir)
        if self.pre_create:
            os.makedirs(os.path.join(os.path.dirname(outdir), self.pre_create), exist_ok=True)
        if not self.measure:
            return ''
        common = make_common(['mix_monitoring', 'counter'], cells=args['-cells'], rounds=int(args['-rounds']),
                             run_id=args['-run-id'], source=args['-source'], tools=args['-tools'],
                             benchtime=args['-benchtime'])
        common['profiles'] = rs.REPORT_PROFILES
        common['data_configs'] = [data_config(p) for p in rs.REPORT_PROFILES]
        common['cells_sha256'] = rs.cells_digest(rs.manifest_cells(rs.REPORT_PROFILES, args['-cells']))
        rnd, layout = int(args['-round']), int(args['-layout'])
        docs = {p: raw_doc(p, common, rnd, layout, args['-order']) for p in rs.REPORT_PROFILES}
        write_invocation(outdir, docs, binary=None)
        return ''


def stub_repo(tmp):
    """A small git repository with the tool files layouts.py hashes, an ignored Go file and a non-regular entry."""
    repo = os.path.join(tmp, 'repo')
    for rel in layouts.TOOL_FILES + ['format/format.go', 'tests/measurev2/main.go', 'go.mod', 'tmp/scratch.go']:
        os.makedirs(os.path.join(repo, os.path.dirname(rel)), exist_ok=True)
        with open(os.path.join(repo, rel), 'w') as f:
            f.write(f'// {rel}\n')
    with open(os.path.join(repo, '.gitignore'), 'w') as f:
        f.write('ignored.go\n')
    with open(os.path.join(repo, 'ignored.go'), 'w') as f:
        f.write('package mebo\n\nfunc Ignored() {}\n')
    os.symlink('format/format.go', os.path.join(repo, 'link.go'))
    subprocess.run(['git', 'init', '-q', repo], check=True)
    subprocess.run(['git', '-C', repo, 'add', '-A'], check=True)
    subprocess.run(['git', '-C', repo, '-c', 'user.email=t@t', '-c', 'user.name=t', 'commit', '-qm', 'x'], check=True)
    return repo


def run_layouts(tmp, argv, **stub):
    repo = stub_repo(tmp) if not os.path.exists(os.path.join(tmp, 'repo')) else os.path.join(tmp, 'repo')
    runner = StubRunner(tmp, **stub)
    old = os.environ.get('TMPDIR')
    os.environ['TMPDIR'] = os.path.join(tmp, 'work')
    try:
        return quiet(layouts.run, argv, runner=runner, repo=repo, online=set(range(32))), runner
    finally:
        if old is None:
            os.environ.pop('TMPDIR')
        else:
            os.environ['TMPDIR'] = old


@check
def layouts_arguments_and_affinity():
    for cpu in ('6,7', '6-7', '-1', 'x'):
        expect_error(layouts.parse_args, ['-o', 'x', '-cpu', cpu], exc=(layouts.LayoutError,), match='-cpu')
    expect_error(layouts.parse_args, ['-o', 'x', '-rounds', '3'], exc=(layouts.LayoutError,), match='rounds')
    with tempfile.TemporaryDirectory() as tmp:
        msg = expect_error(run_layouts, tmp, ['-o', os.path.join(tmp, 'out'), '-cpu', '6'], mask='6,7',
                           exc=(layouts.LayoutError,))
        ok('want exactly CPU 6' in msg, msg)
        expect_error(layouts.check_cpu, '40', StubRunner(tmp), online=set(range(32)), exc=(layouts.LayoutError,),
                     match='not an online CPU')
        eq(layouts.check_cpu('6', StubRunner(tmp), online=set(range(32))), 6, 'one verified CPU')
        os.makedirs(os.path.join(tmp, 'busy'))
        open(os.path.join(tmp, 'busy', 'x'), 'w').close()
        expect_error(run_layouts, tmp, ['-o', os.path.join(tmp, 'busy')], exc=(layouts.LayoutError,), match='not an empty')


@check
def layouts_staging():
    """Only git-reported files are staged (an ignored Go file and tmp/ are not; non-regular entries are skipped),
    and a staged tree whose hash differs from source aborts."""
    with tempfile.TemporaryDirectory() as tmp:
        repo = stub_repo(tmp)
        runner = StubRunner(tmp)
        source, _, details = layouts.provenance(repo, runner)
        ok('ignored.go' not in details['files'] and not any(p.startswith('tmp/') for p in details['files']), 'listing')
        dest = os.path.join(tmp, 'tree')
        skipped = layouts.stage(repo, dest, details['files'], details['tree_sha256'])
        ok(not os.path.exists(os.path.join(dest, 'ignored.go')), 'ignored Go file not staged')
        eq(skipped, ['link.go'], 'non-regular entries skipped')
        ok(f'tree={details["tree_sha256"]}' in source, 'source carries the tree hash')
        real = shutil.copy2

        def corrupting(src, dst):
            real(src, dst)
            if dst.endswith('.go'):
                with open(dst, 'a') as f:
                    f.write('// changed\n')

        layouts.shutil.copy2 = corrupting
        try:
            expect_error(layouts.stage, repo, os.path.join(tmp, 'tree2'), details['files'], details['tree_sha256'],
                         exc=(layouts.LayoutError,), match='differs from the source hash')
        finally:
            layouts.shutil.copy2 = real


@check
def layouts_build_contract():
    for bad, what in (('\tbuild\t-race=true\n', '-race'), ('\tbuild\t-cover=true\n', '-cover'),
                      ('\tbuild\t-pgo=/x/default.pgo\n', '-pgo'), ('\tbuild\t-asan=true\n', '-asan'),
                      ('\tbuild\t-msan=true\n', '-msan')):
        expect_error(layouts.check_build_settings, VERSION_OK + bad, 'x', exc=(layouts.LayoutError,), match=what)
    expect_error(layouts.check_build_settings, VERSION_OK.replace('exe', 'pie'), 'x', exc=(layouts.LayoutError,),
                 match='buildmode')
    expect_error(layouts.check_build_settings, VERSION_OK.replace('-trimpath=true', '-trimpath=false'), 'x',
                 exc=(layouts.LayoutError,), match='trimpath')
    layouts.check_build_settings(VERSION_OK + '\tbuild\t-pgo=off\n', 'x')
    with tempfile.TemporaryDirectory() as tmp:
        expect_error(run_layouts, tmp, ['-o', os.path.join(tmp, 'out'), '--build-only'],
                     version=VERSION_OK + '\tbuild\t-race=true\n', exc=(layouts.LayoutError,), match='-race')


@check
def layouts_padding_and_movement():
    nm0 = nm_table(0, [('main.x', 10), (f'{layouts.MODULE}/blob.F', 64)][::-1])
    eq(layouts.lowest_repo_package(nm0)[0], f'{layouts.MODULE}/blob', 'pad goes to the lowest repository symbol')
    eq(layouts.symbol_package(f'{layouts.MODULE}/internal/x/alp.(*E).F.func1'), f'{layouts.MODULE}/internal/x/alp', 'pkg')
    pkg = f'{layouts.MODULE}/format'
    tables = {k: layouts.parse_nm(nm_table(k, FUNCS)) for k in rs.LAYOUTS}
    res = layouts.movement_check(tables, pkg)
    eq(res['checked'], len(FUNCS), 'moved symbols')
    ok(f'{pkg}.init' in res['exempt'] and f'{pkg}.layoutPad' in res['exempt'], 'the padding adds the initializer')

    def with_init(k, funcs):
        return nm_table(k, [(f'{pkg}.init', 40 + 8 * bool(k))] + list(funcs))
    tables = {k: layouts.parse_nm(with_init(k, FUNCS)) for k in rs.LAYOUTS}
    layouts.movement_check(tables, pkg)

    def stuck(k, funcs):
        text = nm_table(k, funcs)
        return text + f'  900000         40 T {layouts.MODULE}/blob.Stuck\n'
    tables = {k: layouts.parse_nm(stuck(k, FUNCS)) for k in rs.LAYOUTS}
    expect_error(layouts.movement_check, tables, pkg, exc=(layouts.LayoutError,), match='mod 64 is 0 in every layout')
    tables = {k: layouts.parse_nm(nm_table(k, FUNCS if k != 2 else FUNCS[:-1])) for k in rs.LAYOUTS}
    expect_error(layouts.movement_check, tables, pkg, exc=(layouts.LayoutError,), match='missing')
    grown = [(n, s + 40 if n.startswith('main.') else s) for n, s in FUNCS]
    tables = {k: layouts.parse_nm(nm_table(k, FUNCS if k != 4 else grown)) for k in rs.LAYOUTS}
    expect_error(layouts.movement_check, tables, pkg, exc=(layouts.LayoutError,), match='size differs')


@check
def layouts_end_to_end():
    """A complete stubbed run: unique new -outdir per invocation, wrapper records, and a merged/ set."""
    with tempfile.TemporaryDirectory() as tmp:
        out = os.path.join(tmp, 'out')
        (status, _), runner = run_layouts(tmp, ['-o', out, '-rounds', '2'])
        eq(status, 0, 'exit status')
        eq(len(runner.outdirs), 8, 'invocations')
        eq(len(set(runner.outdirs)), 8, 'a unique -outdir per invocation')
        eq(sorted(os.listdir(os.path.join(out, 'raw'))), sorted(f'r{r}_L{k}' for r, k, _ in rs.schedule(2)),
           'exactly one complete set per (layout, round)')
        kind, main, _ = rs.load_input_set(os.path.join(out, 'merged', 'main.json'),
                                          os.path.join(out, 'merged', 'profiles'))
        eq(kind, 'merged', 'merged set')
        eq(main['invocations'][0]['peak_rss_kb'], 4321, 'peak RSS recorded')
        prov = rs.load_json(os.path.join(out, 'provenance.json'))
        ok(prov['movement'] == len(FUNCS) and prov['skipped'] == ['link.go'], 'provenance')
        eq(sorted(os.listdir(os.path.join(out, 'layouts'))),
           sorted([f'{n}_{k}.txt' for n in ('nm', 'version') for k in rs.LAYOUTS] + ['movement.json']),
           'symbol tables kept with the run')


@check
def layouts_failures_publish_nothing():
    """Build, affinity and invocation failures, and a pre-existing per-invocation directory, stop before merging."""
    for name, stub in (('build', {'fail': 'go build'}), ('nm', {'fail': 'go tool nm'}),
                       ('invocation', {'fail': '/usr/bin/time'}), ('affinity', {'mask': '0-31'}),
                       ('no output', {'measure': False})):
        with tempfile.TemporaryDirectory() as tmp:
            out = os.path.join(tmp, 'out')
            try:
                (status, _), _ = run_layouts(tmp, ['-o', out, '-rounds', '2'], **stub)
            except (layouts.LayoutError, rs.SchemaError, OSError):
                status = 1
            ok(status != 0, f'{name}: failure exit')
            ok(not os.path.exists(os.path.join(out, 'merged')), f'{name}: no merged/')
    with tempfile.TemporaryDirectory() as tmp:
        out = os.path.join(tmp, 'out')
        msg = expect_error(run_layouts, tmp, ['-o', out, '-rounds', '2'], pre_create='r1_L1', exc=(layouts.LayoutError,))
        ok('r1_L1 already exists' in msg, msg)
        ok(not os.path.exists(os.path.join(out, 'merged')), 'a pre-existing invocation directory stops before merging')
        eq(sorted(os.listdir(os.path.join(out, 'raw'))), ['r1_L0', 'r1_L1'], 'the run stopped at that invocation')


# ---------------------------------------------------------------- driver

def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--update', action='store_true', help='rewrite testdata/v1/merged_counter.json')
    ap.add_argument('-k', default='', help='run only checks whose name contains this')
    args = ap.parse_args(argv)
    failed = 0
    for fn in CHECKS:
        if args.k not in fn.__name__:
            continue
        try:
            if fn is merged_fixture_is_current:
                fn(update=args.update)
            else:
                fn()
            print(f'ok   {fn.__name__}')
        except Exception:  # noqa: BLE001 - report every failing check, then exit non-zero
            failed += 1
            print(f'FAIL {fn.__name__}\n{traceback.format_exc()}')
    print(f'check_report_tools: {failed} of {len(CHECKS)} checks failed' if failed else
          f'check_report_tools: all {len(CHECKS)} checks passed')
    return 1 if failed else 0


if __name__ == '__main__':
    sys.exit(main())
