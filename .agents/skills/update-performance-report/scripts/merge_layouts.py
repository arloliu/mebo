#!/usr/bin/env python3
"""Merge one layouts.sh run's raw invocations into layout-averaged version-1 files.

Usage:
    python3 merge_layouts.py OUTDIR/raw

RAW holds exactly one directory per (round, layout) of the run's schedule, named r<R>_L<K>,
each with measurev2's -profiles output (main.json, profiles/matrix_<profile>.json)
and the wrapper's invocation record (wrapper.json: binary_sha256, peak_rss_kb).
Every input must agree on the common metadata and on every size and scaling value;
every manifest cell must be timed in every input.
The merged files go to OUTDIR/.partial/ and are renamed to OUTDIR/merged/ only after every check passes;
on any failure nothing is published and the exit status is 1.
See docs/specs/measurev2-fast-report-runs-design.md ("Merge contract").
"""
import argparse
import json
import os
import re
import shutil
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from report_schema import (  # noqa: E402
    COMBOS, COMMON_FIELDS, INVOCATION_FIELDS, LAYOUTS, MAIN_PROFILE, METHOD_MERGED, OPS, RAW_OP_FIELDS,
    SchemaError, check_doc, load_json, measurements, median, percentile, round_half_up, schedule, sizes_of,
)

RAW_DIR_NAME = re.compile(r'^r([1-9]\d*)_L(0|[1-9]\d*)$')
WRAPPER_FIELDS = {'binary_sha256': str, 'peak_rss_kb': int, 'cpu': int}


def read_invocation_dir(path, rnd, layout):
    """Load and validate one invocation directory; return (files {relpath: doc}, wrapper record)."""
    entries = sorted(os.listdir(path))
    allowed = {'main.json', 'profiles', 'wrapper.json'}
    if set(entries) - allowed:
        raise SchemaError(f'{path}: unexpected entries {sorted(set(entries) - allowed)}')
    if 'wrapper.json' not in entries or 'profiles' not in entries:
        raise SchemaError(f'{path}: missing wrapper.json or profiles/')
    wrapper = load_json(os.path.join(path, 'wrapper.json'))
    if not isinstance(wrapper, dict) or set(wrapper) != set(WRAPPER_FIELDS) or any(
            not isinstance(wrapper[k], t) or isinstance(wrapper[k], bool) for k, t in WRAPPER_FIELDS.items()):
        raise SchemaError(f'{path}/wrapper.json: want exactly binary_sha256 (string), peak_rss_kb and cpu (integers)')

    files = {}
    for name in sorted(os.listdir(os.path.join(path, 'profiles'))):
        rel = f'profiles/{name}'
        doc = load_json(os.path.join(path, rel))
        _, profile = check_doc(doc, f'{path}/{rel}', kind='raw')
        if name != f'matrix_{profile}.json':
            raise SchemaError(f'{path}/{rel}: file name does not match profile {profile}')
        files[rel] = doc
    if 'main.json' in entries:
        doc = load_json(os.path.join(path, 'main.json'))
        check_doc(doc, f'{path}/main.json', kind='raw')
        files['main.json'] = doc

    first = next(iter(files.values()), None)
    if first is None:
        raise SchemaError(f'{path}: no profile files')
    profiles = first['common']['profiles']
    if sorted(files) != sorted([f'profiles/matrix_{p}.json' for p in profiles] + (
            ['main.json'] if MAIN_PROFILE in profiles else [])):
        raise SchemaError(f'{path}: files {sorted(files)} do not match the requested profiles {profiles}')
    if first['common']['cpu_affinity'] != [wrapper['cpu']]:
        raise SchemaError(f'{path}: recorded CPU affinity {first["common"]["cpu_affinity"]}, '
                          f'want exactly the verified CPU [{wrapper["cpu"]}]')
    for rel, doc in files.items():
        inv = doc['invocation']
        if (inv['round'], inv['layout']) != (rnd, layout):
            raise SchemaError(f'{path}/{rel}: invocation is round {inv["round"]} layout {inv["layout"]}')
        if doc['common'] != first['common'] or doc['invocation'] != first['invocation'] or doc['run_id'] != first['run_id']:
            raise SchemaError(f'{path}/{rel}: common or invocation differs within one invocation')
    if 'main.json' in files and measurements(files['main.json']) != measurements(files[f'profiles/matrix_{MAIN_PROFILE}.json']):
        raise SchemaError(f'{path}/main.json: differs from its {MAIN_PROFILE} alias')
    return files, wrapper


def scan_raw(raw):
    """Map (round, layout) to its directory; anything that is not r<R>_L<K> is an error."""
    found = {}
    for name in sorted(os.listdir(raw)):
        m = RAW_DIR_NAME.match(name)
        if not m or not os.path.isdir(os.path.join(raw, name)):
            raise SchemaError(f'{raw}/{name}: not an r<R>_L<K> invocation directory')
        key = (int(m.group(1)), int(m.group(2)))
        if key in found:
            raise SchemaError(f'{raw}/{name}: a second directory for round {key[0]} layout {key[1]}')
        found[key] = os.path.join(raw, name)
    if not found:
        raise SchemaError(f'{raw}: no invocation directories')
    rounds = max(r for r, _ in found)
    want = {(r, lay): order for r, lay, order in schedule(rounds)}
    if set(found) != set(want):
        raise SchemaError(f'{raw}: invocations {sorted(found)} are not the {rounds}-round schedule {sorted(want)}')
    return found, want, rounds


def check_agreement(inputs, rounds, orders):
    """Common metadata field by field, sizes and scaling, schedule rounds and orders, and per-layout binaries."""
    (r0, l0), (files0, _) = next(iter(inputs.items()))
    common0 = files0[next(iter(files0))]['common']
    if common0['rounds'] != rounds:
        raise SchemaError(f'r{r0}_L{l0}: common.rounds {common0["rounds"]} but the schedule has {rounds} rounds')
    binaries = {}
    for (rnd, layout), (files, wrapper) in inputs.items():
        where = f'r{rnd}_L{layout}'
        common = files[next(iter(files))]['common']
        for field in COMMON_FIELDS:
            if common[field] != common0[field]:
                raise SchemaError(f'{where}: common.{field} differs from r{r0}_L{l0}')
        inv = files[next(iter(files))]['invocation']
        if inv['order'] != orders[(rnd, layout)]:
            raise SchemaError(f'{where}: order {inv["order"]}, the schedule runs it {orders[(rnd, layout)]}')
        for rel, doc in files.items():
            if sizes_of(doc) != sizes_of(files0[rel]):
                raise SchemaError(f'{where}/{rel}: sizes or scaling differ from r{r0}_L{l0}')
        prev = binaries.setdefault(layout, wrapper['binary_sha256'])
        if prev != wrapper['binary_sha256']:
            raise SchemaError(f'{where}: binary SHA-256 differs from another round of layout {layout}')
    return binaries


def merge_op(runs):
    """One merged operation object from its raw runs (each tagged with layout and round)."""
    runs = sorted(runs, key=lambda r: (r['round'], r['layout']))
    ns = [r['ns_per_op'] for r in runs]
    med = median(ns)
    return {
        'ns_per_op': med,
        'bytes_per_op': round_half_up(median([r['bytes_per_op'] for r in runs])),
        'allocs_per_op': round_half_up(median([r['allocs_per_op'] for r in runs])),
        'runs': runs,
        'layout_ns_per_op': {str(lay): median([r['ns_per_op'] for r in runs if r['layout'] == lay]) for lay in LAYOUTS},
        'iqr_rel': (percentile(ns, 0.75) - percentile(ns, 0.25)) / med,
    }


def merge_file(rel, inputs, binaries, records):
    """Merge one profile file (or main.json) across every input."""
    docs = {key: files[rel] for key, (files, _) in inputs.items()}
    first = docs[min(docs)]
    profile = first['metadata']['data_config']['profile']
    rows_by_label = {key: {r['label']: r for r in doc['matrix']} for key, doc in docs.items()}
    matrix, disagreements = [], []
    for row0 in first['matrix']:
        label = row0['label']
        row = {k: v for k, v in row0.items() if k not in OPS}
        for op in OPS:
            if op not in row0:
                continue
            runs = []
            for (rnd, layout), rows in rows_by_label.items():
                raw = rows[label].get(op)
                if raw is None:
                    raise SchemaError(f'r{rnd}_L{layout}/{rel}: {label}/{op} missing')
                runs.append(dict({k: raw[k] for k in RAW_OP_FIELDS}, layout=layout, round=rnd))
            row[op] = merge_op(runs)
            if len({r['allocs_per_op'] for r in runs}) > 1:
                disagreements.append(f'{profile}/{label}/{op}')
        matrix.append(row)
    if sorted(r['label'] for r in matrix) != sorted(COMBOS):
        raise SchemaError(f'{rel}: combos are not the manifest combos')

    meta = dict(first['metadata'])
    meta['timestamp'] = min(r['start'] for r in records)
    return {
        'format_version': first['format_version'],
        'method': METHOD_MERGED,
        'run_id': first['run_id'],
        'common': first['common'],
        'invocations': records,
        'layouts': {str(lay): binaries[lay] for lay in LAYOUTS},
        'allocation_disagreements': disagreements,
        'raw_raw_bytes': first['raw_raw_bytes'],
        'metadata': meta,
        'matrix': matrix,
        'scaling': first['scaling'],
    }


def merge(raw):
    """Validate a raw directory and return {relpath: merged doc}."""
    found, orders, rounds = scan_raw(raw)
    inputs = {key: read_invocation_dir(path, *key) for key, path in sorted(found.items())}
    binaries = check_agreement(inputs, rounds, orders)

    records = []
    for (rnd, layout), (files, wrapper) in inputs.items():
        inv = files[next(iter(files))]['invocation']
        records.append(dict({k: inv[k] for k in INVOCATION_FIELDS},
                            binary_sha256=wrapper['binary_sha256'], peak_rss_kb=wrapper['peak_rss_kb']))
    order = {(r, lay): i for i, (r, lay, _) in enumerate(schedule(rounds))}
    records.sort(key=lambda rec: order[(rec['round'], rec['layout'])])

    rels = sorted(next(iter(inputs.values()))[0])
    merged = {rel: merge_file(rel, inputs, binaries, records) for rel in rels}
    for rel, doc in merged.items():
        check_doc(doc, f'merged {rel}', kind='merged')
    if 'main.json' in merged and merged['main.json'] != merged[f'profiles/matrix_{MAIN_PROFILE}.json']:
        raise SchemaError(f'merged main.json differs from its {MAIN_PROFILE} alias')
    return merged


def publish(outdir, merged):
    """Write merged files under OUTDIR/.partial/, then rename it to OUTDIR/merged/."""
    partial = os.path.join(outdir, '.partial')
    final = os.path.join(outdir, 'merged')
    if os.path.exists(final):
        raise SchemaError(f'{final} already exists')
    if os.path.exists(partial):
        shutil.rmtree(partial)
    os.makedirs(os.path.join(partial, 'profiles'))
    try:
        for rel, doc in merged.items():
            with open(os.path.join(partial, rel), 'w') as f:
                json.dump(doc, f, indent=2)
                f.write('\n')
        os.rename(partial, final)
    except BaseException:
        shutil.rmtree(partial, ignore_errors=True)
        raise


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('raw', help='OUTDIR/raw of a layouts.sh run')
    args = ap.parse_args(argv)
    raw = os.path.abspath(args.raw)
    outdir = os.path.dirname(raw)
    try:
        merged = merge(raw)
        publish(outdir, merged)
    except (SchemaError, OSError) as exc:
        print(f'merge_layouts: {exc}', file=sys.stderr)
        return 1
    print(f'merge_layouts: wrote {len(merged)} files to {os.path.join(outdir, "merged")}')
    return 0


if __name__ == '__main__':
    sys.exit(main())
