"""Shared schema, manifest and comparison rule of the measurev2 report tools.

merge_layouts.py, generate_report.py, check_report_tools.py and tests/measurev2/acceptance.py import this module.
The manifest constants mirror tests/measurev2/manifest.go; check_report_tools.py compares the two,
and every version-1 file's cells_sha256 must equal the digest computed here.
See docs/specs/measurev2-fast-report-runs-design.md ("Version-1 schema", "Comparison rule").
"""
import hashlib
import json
import math
import os

OPS = ['encode', 'decode', 'iter_seq', 'random_value_at', 'random_timestamp_at']

TS_ENCODINGS = {'raw': 'Raw', 'delta': 'Delta', 'deltapacked': 'DeltaPacked'}
VAL_ENCODINGS = {'raw': 'Raw', 'gorilla': 'Gorilla', 'chimp': 'Chimp', 'alp': 'ALP', 'alprle': 'ALPRLE'}

# Every combo measured on a data set, in run order: per-metric timestamps, then shared timestamps.
COMBOS = (
    [f'{t}-{v}' for t in TS_ENCODINGS for v in VAL_ENCODINGS]
    + [f'shared-{t}-{v}' for t in TS_ENCODINGS for v in VAL_ENCODINGS]
)

MAIN_PROFILE = 'mix_monitoring'

# The frozen report manifest (reportProfiles in tests/measurev2/manifest.go), in run order.
REPORT_PROFILES = [
    'mix_monitoring', 'mix_sensor', 'mix_integer', 'mix_fullprec',
    'decimal_gauge_2dp', 'decimal_gauge_4dp', 'counter', 'sparse_constant', 'worst_case',
    'cal_2dp_hold30', 'cal_2dp_hold50', 'cal_2dp_hold70', 'cal_2dp_step0.005',
    'cal_1dp_step0.03', 'cal_1dp_step0.01', 'legacy_random_walk',
]
# The rest of the generator catalog, in catalog order; with REPORT_PROFILES it is the manifest order of `-profiles all`.
OTHER_PROFILES = ['regular_scrape_60s', 'bursty_scrape']
MANIFEST_PROFILES = REPORT_PROFILES + OTHER_PROFILES

REPORT_PROFILE_COMBOS = [
    'shared-deltapacked-raw', 'shared-deltapacked-gorilla', 'shared-deltapacked-chimp',
    'shared-deltapacked-alp', 'shared-deltapacked-alprle', 'delta-gorilla',
]
REPORT_PROFILE_OPS = ['encode', 'iter_seq', 'random_value_at']

CELLS_MODES = ('full', 'report', 'wide')

FORMAT_VERSION = 1
METHOD_RAW = 'testing.Benchmark'
METHOD_MERGED = 'testing.Benchmark, layout-averaged'
METHOD_SIZES_ONLY = 'sizes-only'

LAYOUTS = [0, 1, 2, 4]
# Round r of a 4-round run runs the layouts in row r of this Latin square; a 2-round run uses rows 1 and 4.
LATIN_SQUARE = [[0, 1, 2, 4], [1, 4, 0, 2], [2, 0, 4, 1], [4, 2, 1, 0]]

# The comparison rule's threshold: a gap of at least 20% (see compare()).
DECIDE_GAP = 0.20

# Relative tolerance of derived size fields and of ns_per_op against t_ns / n.
SIZE_REL_TOL = 1e-9
NS_REL_TOL = 1e-12

SIZE_FIELDS = {
    'label': 'str', 'ts_encoding': 'str', 'val_encoding': 'str',
    'num_metrics': 'int', 'points_per_metric': 'int', 'total_points': 'int',
    'encoded_bytes': 'int', 'bytes_per_point': 'num', 'vs_raw_ratio': 'num', 'space_savings_pct': 'num',
}
LEGACY_OP_FIELDS = {'ns_per_op': 'num', 'bytes_per_op': 'int', 'allocs_per_op': 'int'}
RAW_OP_FIELDS = {
    'ns_per_op': 'num', 'bytes_per_op': 'int', 'allocs_per_op': 'int',
    'n': 'int', 't_ns': 'int', 'mem_allocs': 'int', 'mem_bytes': 'int',
}
RUN_FIELDS = dict(RAW_OP_FIELDS, layout='int', round='int')
MERGED_OP_FIELDS = {
    'ns_per_op': 'num', 'bytes_per_op': 'int', 'allocs_per_op': 'int',
    'runs': 'list', 'layout_ns_per_op': 'dict', 'iqr_rel': 'num',
}
COMMON_FIELDS = {
    'run_id': 'str', 'source': 'str', 'tools': 'str', 'goos': 'str', 'goarch': 'str', 'cpu_model': 'str',
    'go_version': 'str', 'build_settings': 'list', 'gomaxprocs': 'int', 'cpu_affinity': 'list',
    'gogc': 'str', 'gomemlimit': 'str', 'godebug': 'str', 'benchtime': 'str', 'rounds': 'int',
    'cells': 'str', 'cells_sha256': 'str', 'profiles': 'list', 'data_configs': 'list',
}
INVOCATION_FIELDS = {'layout': 'int', 'round': 'int', 'order': 'str', 'start': 'str', 'end': 'str', 'timestamp': 'str'}
INVOCATION_RECORD_FIELDS = dict(INVOCATION_FIELDS, binary_sha256='str', peak_rss_kb='int')
LEGACY_KEYS = {'metadata', 'matrix', 'scaling'}
RAW_KEYS = {'format_version', 'method', 'run_id', 'common', 'invocation', 'raw_raw_bytes', 'metadata', 'matrix', 'scaling'}
MERGED_KEYS = {
    'format_version', 'method', 'run_id', 'common', 'invocations', 'layouts', 'allocation_disagreements',
    'raw_raw_bytes', 'metadata', 'matrix', 'scaling',
}


class SchemaError(Exception):
    """An input file does not satisfy the schema, the manifest or a size identity."""


# ---------------------------------------------------------------- manifest

def timed_ops(profile, cells, combo):
    """Operations timed for one combo of one data set under a -cells mode, in run order."""
    if profile == MAIN_PROFILE or cells == 'full':
        return list(OPS)
    if combo in REPORT_PROFILE_COMBOS:
        return list(REPORT_PROFILE_OPS)
    if cells == 'wide':
        return ['iter_seq']
    return []


def cell_id(profile, combo, op):
    return f'{profile}/{combo}/{op}'


def manifest_cells(profiles, cells):
    """Cell ids timed for profiles under a -cells mode, in forward run order."""
    return [cell_id(p, c, op) for p in profiles for c in COMBOS for op in timed_ops(p, cells, c)]


def cells_digest(ids):
    """SHA-256 of the sorted cell ids, one per line, as measurev2 computes it."""
    return hashlib.sha256('\n'.join(sorted(ids)).encode()).hexdigest()


def schedule(rounds):
    """[(round, layout, order)] of a layouts.sh run with 4 or 2 rounds, in run order."""
    if rounds == 4:
        rows = [(1, 0, 'forward'), (2, 1, 'reverse'), (3, 2, 'forward'), (4, 3, 'reverse')]
    elif rounds == 2:
        rows = [(1, 0, 'forward'), (2, 3, 'reverse')]
    else:
        raise SchemaError(f'rounds {rounds!r}: want 4 or 2')
    return [(r, layout, order) for r, row, order in rows for layout in LATIN_SQUARE[row]]


# ---------------------------------------------------------------- type checks

def is_int(x):
    return isinstance(x, int) and not isinstance(x, bool)


def is_num(x):
    return (isinstance(x, (int, float)) and not isinstance(x, bool)) and math.isfinite(x)


def check_type(value, kind, where):
    ok = {
        'str': lambda v: isinstance(v, str),
        'int': is_int,
        'num': is_num,
        'list': lambda v: isinstance(v, list),
        'dict': lambda v: isinstance(v, dict),
    }[kind](value)
    if not ok:
        shown = 'null' if value is None else type(value).__name__
        raise SchemaError(f'{where}: want {kind}, got {shown}')


def check_fields(obj, fields, where, exact=True, extra=()):
    """Check that obj is an object holding every field of fields with its type; with exact, nothing else but extra."""
    if not isinstance(obj, dict):
        raise SchemaError(f'{where}: want an object')
    for name, kind in fields.items():
        if name not in obj:
            raise SchemaError(f'{where}: missing field {name}')
        check_type(obj[name], kind, f'{where}.{name}')
    if exact:
        unknown = set(obj) - set(fields) - set(extra)
        if unknown:
            raise SchemaError(f'{where}: unexpected fields {sorted(unknown)}')


def close(a, b, rel):
    return math.isclose(a, b, rel_tol=rel, abs_tol=1e-12)


# ---------------------------------------------------------------- statistics

def median(values):
    s = sorted(values)
    n = len(s)
    if n == 0:
        raise SchemaError('median of no values')
    mid = n // 2
    return float(s[mid]) if n % 2 else (s[mid - 1] + s[mid]) / 2


def percentile(values, q):
    """Linear-interpolation percentile, numpy's default: position (n - 1) * q."""
    s = sorted(values)
    pos = (len(s) - 1) * q
    lo = math.floor(pos)
    hi = min(lo + 1, len(s) - 1)
    return s[lo] + (s[hi] - s[lo]) * (pos - lo)


def round_half_up(x):
    return int(math.floor(x + 0.5))


# ---------------------------------------------------------------- operation objects

def check_raw_op(op, where):
    """A raw operation object: exact fields, N > 0, positive finite timings, ns_per_op == t_ns / n,
    and the per-op allocation fields as testing computes them from the totals."""
    check_fields(op, RAW_OP_FIELDS, where)
    if op['n'] <= 0:
        raise SchemaError(f'{where}: n = {op["n"]}, want > 0')
    if op['t_ns'] <= 0 or op['ns_per_op'] <= 0:
        raise SchemaError(f'{where}: non-positive timing')
    if min(op['mem_allocs'], op['mem_bytes'], op['bytes_per_op'], op['allocs_per_op']) < 0:
        raise SchemaError(f'{where}: negative allocation field')
    if not close(op['ns_per_op'], op['t_ns'] / op['n'], NS_REL_TOL):
        raise SchemaError(f'{where}: ns_per_op {op["ns_per_op"]} disagrees with t_ns / n = {op["t_ns"] / op["n"]}')
    if op['allocs_per_op'] != op['mem_allocs'] // op['n'] or op['bytes_per_op'] != op['mem_bytes'] // op['n']:
        raise SchemaError(f'{where}: per-op allocations disagree with the totals')


def check_legacy_op(op, where):
    check_fields(op, LEGACY_OP_FIELDS, where)
    if op['ns_per_op'] <= 0:
        raise SchemaError(f'{where}: non-positive timing')


def check_merged_op(op, where, layouts, rounds):
    """A merged operation object: its runs (one per layout and round), and the medians recomputed from them."""
    check_fields(op, MERGED_OP_FIELDS, where)
    seen = set()
    for i, run in enumerate(op['runs']):
        check_fields(run, RUN_FIELDS, f'{where}.runs[{i}]')
        check_raw_op({k: run[k] for k in RAW_OP_FIELDS}, f'{where}.runs[{i}]')
        seen.add((run['layout'], run['round']))
    want = {(lay, r) for lay in layouts for r in range(1, rounds + 1)}
    if seen != want or len(op['runs']) != len(want):
        raise SchemaError(f'{where}: runs cover {sorted(seen)}, want every (layout, round) of {sorted(want)} once')
    if set(op['layout_ns_per_op']) != {str(lay) for lay in layouts}:
        raise SchemaError(f'{where}: layout_ns_per_op keys {sorted(op["layout_ns_per_op"])}')
    for lay in layouts:
        got = op['layout_ns_per_op'][str(lay)]
        check_type(got, 'num', f'{where}.layout_ns_per_op.{lay}')
        if not close(got, median([r['ns_per_op'] for r in op['runs'] if r['layout'] == lay]), NS_REL_TOL):
            raise SchemaError(f'{where}: layout_ns_per_op[{lay}] is not the median of its runs')
    ns = [r['ns_per_op'] for r in op['runs']]
    if not close(op['ns_per_op'], median(ns), NS_REL_TOL):
        raise SchemaError(f'{where}: ns_per_op is not the median of its runs')
    for field in ('bytes_per_op', 'allocs_per_op'):
        if op[field] != round_half_up(median([r[field] for r in op['runs']])):
            raise SchemaError(f'{where}: {field} is not the half-up median of its runs')
    if not close(op['iqr_rel'], (percentile(ns, 0.75) - percentile(ns, 0.25)) / op['ns_per_op'], NS_REL_TOL):
        raise SchemaError(f'{where}: iqr_rel does not match its runs')


# ---------------------------------------------------------------- rows

def check_size_identities(row, raw_raw_bytes, where):
    """The derived size fields measurev2 computes, checked against encoded_bytes and the raw-raw size."""
    enc = row['encoded_bytes']
    if enc <= 0 or raw_raw_bytes <= 0:
        raise SchemaError(f'{where}: non-positive size')
    if row['total_points'] != row['num_metrics'] * row['points_per_metric']:
        raise SchemaError(f'{where}: total_points {row["total_points"]} != num_metrics × points_per_metric')
    derived = {
        'bytes_per_point': enc / row['total_points'],
        'vs_raw_ratio': raw_raw_bytes / enc,
        'space_savings_pct': (1 - enc / raw_raw_bytes) * 100,
    }
    for name, want in derived.items():
        if not close(row[name], want, SIZE_REL_TOL):
            raise SchemaError(f'{where}: {name} {row[name]} disagrees with encoded_bytes (want {want})')


def check_row_identity(row, data_config, where):
    """Combo label, encodings and metric counts against the manifest and the data configuration."""
    label = row['label']
    if label not in COMBOS:
        raise SchemaError(f'{where}: unknown combo {label}')
    parts = label.split('-')
    ts, val = (parts[1], parts[2]) if parts[0] == 'shared' else (parts[0], parts[1])
    if row['ts_encoding'] != TS_ENCODINGS[ts] or row['val_encoding'] != VAL_ENCODINGS[val]:
        raise SchemaError(f'{where}: encodings {row["ts_encoding"]}/{row["val_encoding"]} do not match {label}')
    if row['num_metrics'] != data_config['num_metrics'] or row['points_per_metric'] != data_config['points_per_metric']:
        raise SchemaError(f'{where}: metric counts disagree with data_config')


def check_scaling(scaling, data_config, where):
    check_type(scaling, 'list', where)
    labels = []
    for i, s in enumerate(scaling):
        w = f'{where}[{i}]'
        check_fields(s, {'label': 'str', 'ts_encoding': 'str', 'val_encoding': 'str', 'num_metrics': 'int',
                         'points_series': 'list'}, w)
        labels.append(s['label'])
        if s['num_metrics'] != data_config['num_metrics']:
            raise SchemaError(f'{w}: num_metrics disagrees with data_config')
        for j, p in enumerate(s['points_series']):
            check_fields(p, {'points_per_metric': 'int', 'encoded_bytes': 'int', 'bytes_per_point': 'num'}, f'{w}[{j}]')
            if not close(p['bytes_per_point'], p['encoded_bytes'] / (s['num_metrics'] * p['points_per_metric']),
                         SIZE_REL_TOL):
                raise SchemaError(f'{w}[{j}]: bytes_per_point disagrees with encoded_bytes')
    if sorted(labels) != sorted(COMBOS):
        raise SchemaError(f'{where}: scaling labels are not the 30 manifest combos')


# ---------------------------------------------------------------- documents

def doc_kind(doc, where):
    """'legacy', 'raw', 'merged' or 'sizes-only'; an unknown format_version or method is an error."""
    if not isinstance(doc, dict):
        raise SchemaError(f'{where}: want a JSON object')
    if 'format_version' not in doc:
        return 'legacy'
    if doc['format_version'] != FORMAT_VERSION or not is_int(doc['format_version']):
        raise SchemaError(f'{where}: unknown format_version {doc["format_version"]!r}')
    method = doc.get('method')
    kinds = {METHOD_RAW: 'raw', METHOD_MERGED: 'merged', METHOD_SIZES_ONLY: 'sizes-only'}
    if method not in kinds:
        raise SchemaError(f'{where}: unknown method {method!r}')
    return kinds[method]


def doc_profile(doc, where):
    try:
        name = doc['metadata']['data_config']['profile']
    except (KeyError, TypeError) as exc:
        raise SchemaError(f'{where}: no metadata.data_config.profile') from exc
    if name not in MANIFEST_PROFILES:
        raise SchemaError(f'{where}: profile {name!r} is not in the manifest')
    return name


def check_common(common, where):
    check_fields(common, COMMON_FIELDS, where)
    if common['cells'] not in CELLS_MODES:
        raise SchemaError(f'{where}.cells: {common["cells"]!r}')
    profiles = common['profiles']
    if not profiles or len(set(profiles)) != len(profiles) or any(p not in MANIFEST_PROFILES for p in profiles):
        raise SchemaError(f'{where}.profiles: {profiles!r}')
    if profiles != [p for p in MANIFEST_PROFILES if p in profiles]:
        raise SchemaError(f'{where}.profiles: not in manifest order')
    if any(not is_int(c) or c < 0 for c in common['cpu_affinity']):
        raise SchemaError(f'{where}.cpu_affinity: {common["cpu_affinity"]!r}')
    if [dc.get('profile') for dc in common['data_configs']] != profiles:
        raise SchemaError(f'{where}.data_configs: one per requested profile, in order')
    if common['cells_sha256'] != cells_digest(manifest_cells(profiles, common['cells'])):
        raise SchemaError(f'{where}.cells_sha256: does not match the manifest cells of {common["cells"]!r}')


def check_doc(doc, where, kind=None):
    """Validate one file completely and return (kind, profile).
    kind, when given, is the only kind accepted."""
    got = doc_kind(doc, where)
    if kind is not None and got != kind:
        raise SchemaError(f'{where}: a {got} file where {kind} input is required')
    keys = {'legacy': LEGACY_KEYS, 'raw': RAW_KEYS, 'merged': MERGED_KEYS, 'sizes-only': RAW_KEYS}[got]
    if set(doc) != keys:
        raise SchemaError(f'{where}: top-level fields {sorted(doc)}, want {sorted(keys)}')
    profile = doc_profile(doc, where)
    meta = doc['metadata']
    dc = meta['data_config']
    check_type(doc['matrix'], 'list', f'{where}.matrix')

    if got == 'legacy':
        raw_rows = [r for r in doc['matrix'] if isinstance(r, dict) and r.get('label') == 'raw-raw']
        if len(raw_rows) != 1:
            raise SchemaError(f'{where}: want exactly one raw-raw row')
        raw_raw = raw_rows[0].get('encoded_bytes')
        check_type(raw_raw, 'int', f'{where}: raw-raw encoded_bytes')
    else:
        check_type(doc['run_id'], 'str', f'{where}.run_id')
        if not doc['run_id']:
            raise SchemaError(f'{where}: empty run_id')
        check_common(doc['common'], f'{where}.common')
        if doc['common']['run_id'] != doc['run_id']:
            raise SchemaError(f'{where}: common.run_id differs from run_id')
        if profile not in doc['common']['profiles']:
            raise SchemaError(f'{where}: profile {profile} is not among common.profiles')
        if dc != doc['common']['data_configs'][doc['common']['profiles'].index(profile)]:
            raise SchemaError(f'{where}: metadata.data_config differs from common.data_configs')
        raw_raw = doc['raw_raw_bytes']
        check_type(raw_raw, 'int', f'{where}.raw_raw_bytes')
        if got in ('raw', 'sizes-only'):
            check_fields(doc['invocation'], INVOCATION_FIELDS, f'{where}.invocation')
        else:
            check_merged_header(doc, where)

    labels = []
    for i, row in enumerate(doc['matrix']):
        w = f'{where}: matrix[{i}]'
        check_fields(row, SIZE_FIELDS, w, exact=False)
        w = f'{where}: {row["label"]}'
        labels.append(row['label'])
        check_row_identity(row, dc, w)
        check_size_identities(row, raw_raw, w)
        check_row_ops(row, got, doc, profile, w)
    if len(labels) != len(set(labels)):
        raise SchemaError(f'{where}: duplicate combo labels')
    if sorted(labels) != sorted(COMBOS):
        raise SchemaError(f'{where}: combos are not the 30 manifest combos')
    if got == 'merged':
        want = sorted(cell_id(profile, r['label'], op) for r in doc['matrix'] for op in OPS
                      if op in r and len({run['allocs_per_op'] for run in r[op]['runs']}) > 1)
        if sorted(doc['allocation_disagreements']) != want:
            raise SchemaError(f'{where}: allocation_disagreements {doc["allocation_disagreements"]} '
                              f'do not match the runs ({want})')
    if got != 'legacy':
        raw_row = next(r for r in doc['matrix'] if r['label'] == 'raw-raw')
        if raw_row['encoded_bytes'] != raw_raw:
            raise SchemaError(f'{where}: raw_raw_bytes differs from the raw-raw row')
    check_scaling(doc['scaling'], dc, f'{where}.scaling')
    return got, profile


def check_merged_header(doc, where):
    rounds = doc['common']['rounds']
    if rounds not in (2, 4):
        raise SchemaError(f'{where}: merged input with rounds {rounds}')
    if len(doc['common']['cpu_affinity']) != 1:
        raise SchemaError(f'{where}: merged input pinned to {doc["common"]["cpu_affinity"]}, want exactly one CPU')
    check_type(doc['invocations'], 'list', f'{where}.invocations')
    seen = set()
    for i, inv in enumerate(doc['invocations']):
        check_fields(inv, INVOCATION_RECORD_FIELDS, f'{where}.invocations[{i}]')
        seen.add((inv['round'], inv['layout']))
    want = {(r, lay) for r, lay, _ in schedule(rounds)}
    if seen != want or len(doc['invocations']) != len(want):
        raise SchemaError(f'{where}: invocations cover {sorted(seen)}, want every layout and round of {rounds} rounds')
    check_type(doc['layouts'], 'dict', f'{where}.layouts')
    if set(doc['layouts']) != {str(lay) for lay in LAYOUTS}:
        raise SchemaError(f'{where}: layouts {sorted(doc["layouts"])}')
    check_type(doc['allocation_disagreements'], 'list', f'{where}.allocation_disagreements')


def check_row_ops(row, kind, doc, profile, where):
    present = [op for op in OPS if op in row]
    extra = set(row) - set(SIZE_FIELDS) - set(OPS)
    if extra:
        raise SchemaError(f'{where}: unexpected fields {sorted(extra)}')
    for op in present:
        if row[op] is None:
            raise SchemaError(f'{where}.{op}: null operation (an untimed operation is absent)')
    if kind == 'legacy':
        if present != OPS:
            raise SchemaError(f'{where}: legacy rows need every operation')
        for op in OPS:
            check_legacy_op(row[op], f'{where}.{op}')
        return
    if kind == 'sizes-only':
        if present:
            raise SchemaError(f'{where}: timings in sizes-only output')
        return
    want = timed_ops(profile, doc['common']['cells'], row['label'])
    if sorted(present) != sorted(want):
        missing = sorted(set(want) - set(present))
        if missing:
            raise SchemaError(f'{where}: missing manifest cells {missing}')
        raise SchemaError(f'{where}: operations {sorted(set(present) - set(want))} are not manifest cells')
    for op in present:
        if kind == 'raw':
            check_raw_op(row[op], f'{where}.{op}')
        else:
            check_merged_op(row[op], f'{where}.{op}', LAYOUTS, doc['common']['rounds'])


def load_json(path):
    try:
        with open(path) as f:
            return json.load(f)
    except (OSError, json.JSONDecodeError) as exc:
        raise SchemaError(f'{path}: {exc}') from exc


def measurements(doc):
    """The measured content of a file: sizes, timings and scaling (what an alias must reproduce)."""
    return {k: doc[k] for k in ('matrix', 'scaling', 'raw_raw_bytes') if k in doc}


def sizes_of(doc):
    """Sizes and scaling only: the measurement outputs every input of a merge must agree on."""
    rows = sorted(({k: r[k] for k in SIZE_FIELDS} for r in doc['matrix']), key=lambda r: r['label'])
    return {'matrix': rows, 'scaling': sorted(doc['scaling'], key=lambda s: s['label']), 'raw_raw_bytes': doc.get('raw_raw_bytes')}


# ---------------------------------------------------------------- input sets

def load_input_set(main_path, profiles_dir):
    """Load and validate a publishable input set: main.json plus one matrix_<profile>.json per profile.

    Accepted: all-legacy input (every operation present, no format_version)
    or all-merged version-1 input with one shared run_id and common.
    Raw invocations, sizes-only output and unknown versions are errors,
    and so are mixtures, duplicate profiles and missing manifest profiles.
    Returns (kind, main_doc, {profile: doc}).
    """
    main_doc = load_json(main_path)
    kind, main_profile = check_doc(main_doc, main_path)
    if kind not in ('legacy', 'merged'):
        raise SchemaError(f'{main_path}: {kind} input cannot be published; merge a layouts.sh run first')
    if main_profile != MAIN_PROFILE:
        raise SchemaError(f'{main_path}: main.json is profile {main_profile}, want {MAIN_PROFILE}')

    if not os.path.isdir(profiles_dir):
        raise SchemaError(f'{profiles_dir}: not a directory')
    docs = {}
    for name in sorted(os.listdir(profiles_dir)):
        if not (name.startswith('matrix_') and name.endswith('.json')):
            continue
        path = os.path.join(profiles_dir, name)
        doc = load_json(path)
        got, profile = check_doc(doc, path)
        if got != kind:
            raise SchemaError(f'{path}: a {got} file mixed with {kind} input')
        if profile in docs:
            raise SchemaError(f'{path}: a second file for profile {profile}')
        if name != f'matrix_{profile}.json':
            raise SchemaError(f'{path}: file name does not match profile {profile}')
        docs[profile] = doc

    want = REPORT_PROFILES if kind == 'legacy' else main_doc['common']['profiles']
    if kind == 'merged' and want != REPORT_PROFILES:
        raise SchemaError(f'{main_path}: the report needs -profiles report, got {want}')
    missing = [p for p in want if p not in docs]
    if missing:
        raise SchemaError(f'{profiles_dir}: missing profile files {missing}')
    extra = [p for p in docs if p not in want]
    if extra:
        raise SchemaError(f'{profiles_dir}: profiles outside the manifest run {extra}')

    if kind == 'merged':
        for p, doc in docs.items():
            if doc['run_id'] != main_doc['run_id'] or doc['common'] != main_doc['common']:
                raise SchemaError(f'{profiles_dir}/matrix_{p}.json: run_id or common differs from main.json')
        if measurements(docs[MAIN_PROFILE]) != measurements(main_doc):
            raise SchemaError(f'{main_path}: differs from matrix_{MAIN_PROFILE}.json; one merged run measures them once')
    return kind, main_doc, docs


# ---------------------------------------------------------------- comparison rule

def compare(a, b):
    """Compare two operation objects under the comparison rule.

    Returns (outcome, winner, gap): outcome is 'decided', 'equivalent' or 'inconclusive';
    winner is 'a' or 'b' (the smaller pooled point estimate) or None when they are equal;
    gap is max/min - 1. Merged objects (with layout_ns_per_op) also need layout support:
    every layout's median puts the pooled winner strictly ahead.
    """
    pa, pb = a['ns_per_op'], b['ns_per_op']
    gap = max(pa, pb) / min(pa, pb) - 1
    winner = 'a' if pa < pb else 'b' if pb < pa else None
    if gap < DECIDE_GAP - 1e-12:
        return 'equivalent', winner, gap
    la, lb = a.get('layout_ns_per_op'), b.get('layout_ns_per_op')
    if la is None and lb is None:
        return 'decided', winner, gap
    if la is None or lb is None or set(la) != set(lb):
        raise SchemaError('compare: one merged and one unmerged operation, or different layouts')
    win, lose = (la, lb) if winner == 'a' else (lb, la)
    if all(win[k] < lose[k] for k in win):
        return 'decided', winner, gap
    return 'inconclusive', winner, gap
