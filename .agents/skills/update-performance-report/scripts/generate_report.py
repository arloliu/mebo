#!/usr/bin/env python3
"""Fill the deterministic tables of docs/performance.md from tests/measurev2 JSON.

The script only renders tables and facts.
The judgment sections (Quick Reference, observations, decision tree, configuration selection, ...)
are `{{LLM:NAME}}` placeholders that the agent writes afterwards, following SKILL.md.
A facts digest (rankings per data set) is written alongside to support that writing.

Usage:
    python3 generate_report.py --main MAIN.json --profiles DIR \
        --template PERFORMANCE_TEMPLATE.md --out docs/performance.md \
        --digest $TMPDIR/perf_digest.md
    python3 generate_report.py --check docs/performance.md

DIR holds one `matrix_<profile>.json` per data-shape profile (see SKILL.md Step 1).
With --check, the script fails if any `{{...}}` placeholder is left in the file.
"""
import argparse
import glob
import json
import os
import re
import sys


# At()-complexity per encoding, verified against the actual decoder implementations (internal/encoding/*.go),
# not assumed from the encoding's name.
# Raw is a direct offset (true O(1)).
# ALP is a windowed bit read (O(1)) plus a binary search over that column's exception sidecar
# (O(log k), k = exceptions in the column, not n),
# which is genuinely different from a plain O(1), so don't collapse it into "O(1)" either.
# ALP-RLE adds a rank over the run-start bitmap before the nested ALP lookup:
# a word-wise popcount of bits 0..index with no rank directory
# (alpRunsRank in internal/encoding/value/alp/alp_runs.go),
# so O(index/64), at most 3 popcounts at 150 points.
# Columns that stay plain (no runs layout) are exactly ALP.
# Gorilla/Chimp (values) and Delta/DeltaPacked (timestamps) must sequentially decode from the start of the column,
# so they're O(index), worst-case O(n).
# Shared timestamps (any encoding) are decoded once into a cache when the blob is opened
# (sharedTsCache, built in blob/numeric_decoder.go),
# so TimestampAt is O(1) for every shared-* combo regardless of its timestamp encoding;
# see ts_complexity().
AT_COMPLEXITY = {
    'raw': 'O(1)',
    'alp': 'O(1) + O(log k) exceptions',
    'alprle': 'O(index/64) bitmap rank + O(1) + O(log k) exceptions',
    'gorilla': 'O(index), sequential XOR decode from the start',
    'chimp': 'O(index), sequential XOR decode from the start',
    'delta': 'O(index), sequential decode from the start',
    'deltapacked': 'O(index), sequential decode from the start',
}

SHARED_TS_COMPLEXITY = 'O(1), cached when the blob is opened'

VALS = ['raw', 'gorilla', 'chimp', 'alp', 'alprle']
TSS = ['raw', 'delta', 'deltapacked']
VAL_NAMES = {'raw': 'Raw', 'gorilla': 'Gorilla', 'chimp': 'Chimp', 'alp': 'ALP', 'alprle': 'ALP-RLE'}
TS_NAMES = {'raw': 'Raw', 'delta': 'Delta', 'deltapacked': 'DeltaPacked'}

# The production-like configuration every data-shape table is read at,
# and the reference combos the digest compares against.
PROD_TS = 'shared-deltapacked'
REFERENCES = {
    'shared-deltapacked-chimp': 'production-like reference (Shared DeltaPacked + Chimp)',
    'delta-gorilla': 'NewDefaultNumericEncoder (Delta + Gorilla)',
}

LLM_PLACEHOLDER = re.compile(r'\{\{LLM:[A-Z_]+\}\}')
ANY_PLACEHOLDER = re.compile(r'\{\{[A-Z_:]+\}\}')


def parse_label(label):
    """Split a combo label into (shared, ts_key, val_key), e.g.
    'shared-delta-alp' -> (True, 'delta', 'alp')."""
    parts = label.split('-')
    if parts[0] == 'shared':
        return True, parts[1], parts[2]

    return False, parts[0], parts[1]


def fmt_label(label):
    """Convert label like 'shared-delta-chimp' to 'Shared Delta + Chimp'."""
    shared, ts, val = parse_label(label)
    name = f"{TS_NAMES[ts]} + {VAL_NAMES[val]}"
    if shared:
        return f"Shared {name}"

    return name


def ts_complexity(label):
    """TimestampAt complexity for a combo label; shared-* combos read a cache."""
    shared, ts_key, _ = parse_label(label)
    if shared:
        return SHARED_TS_COMPLEXITY

    return AT_COMPLEXITY.get(ts_key, 'unknown')


def by_label(matrix):
    return {r['label']: r for r in matrix}


def signed_pct(x):
    """Format a fraction as a signed percentage with a typographic minus."""
    return f"{x * 100:+.1f}%".replace('-', '−')


# ---------------------------------------------------------------- profile text

def describe_part(spec, show_interval=True):
    """One-line description of a single-kind profile spec."""
    if spec.get('legacy'):
        return 'full-precision ±0.5% random walk at 1 s, ±0.1% timestamp jitter (the pre-2026-10 default)'
    kind = spec.get('value_kind') or 'gauge'
    dec = spec.get('decimals', 0)
    if kind == 'counter':
        text = 'integer counter, +1 to +10 per point'
    elif kind == 'sparse':
        text = f'mostly-constant {dec}-decimal value, a small step on 5% of points'
    else:
        prec = 'full-precision' if dec < 0 else f'{dec}-decimal'
        step = spec.get('step_pct') or 0.5
        text = f'{prec} gauge, steps up to ±{step:g}%'
        if spec.get('hold'):
            text += f', {spec["hold"] * 100:.0f}% of points repeat the previous value'
    if show_interval and spec.get('interval_ms'):
        text += f', {spec["interval_ms"] / 1000:g} s'
    if spec.get('bursty_gaps'):
        text += ', a 5 s gap every 50 points'

    return text


def describe_profile(spec):
    """Markdown description of a profile spec, mixed parts included."""
    if not spec:
        return 'unknown profile (no profile_spec in the JSON)'
    if not spec.get('parts'):
        return describe_part(spec)
    lines = [f"Mixed blob, {spec.get('interval_ms', 0) / 1000:g} s scrape interval:", ""]
    for part in spec['parts']:
        lines.append(f"- {part['share'] * 100:.0f}% of metrics: {describe_part(part['profile'], show_interval=False)}")
    share = spec.get('ts_jitter_share', 0)
    lines.append(
        f"- Timestamps: {100 - share * 100:.0f}% exactly on the scrape grid, "
        f"the rest {spec.get('ts_jitter_min_ms', 0):g}–{spec.get('ts_jitter_max_ms', 0):g} ms off"
    )

    return '\n'.join(lines)


# ---------------------------------------------------------------- main tables

def gen_benchmark_metadata(meta):
    dc = meta['data_config']
    total = dc['num_metrics'] * dc['points_per_metric']
    return f"""| | |
|---|---|
| **Benchmark Date** | {meta['timestamp'][:10]} |
| **Platform** | {meta['os']}/{meta['arch']} ({meta['num_cpu']} CPUs), Go {meta['go_version']} |
| **Data** | `{dc.get('profile', '')}` profile: {dc['num_metrics']} metrics × {dc['points_per_metric']} points = {total:,} data points |
| **Compression Codecs** | None (encoding algorithms only) |"""


def gen_benchmark_metadata_detail(meta):
    dc = meta['data_config']
    return f"""| Parameter | Value |
|-----------|-------|
| **Go Version** | {meta['go_version']} |
| **OS / Arch** | {meta['os']}/{meta['arch']} |
| **CPU Cores** | {meta['num_cpu']} |
| **Profile** | `{dc.get('profile', '')}` |
| **Metrics** | {dc['num_metrics']} |
| **Points/Metric** | {dc['points_per_metric']} |
| **Seed** | {dc['seed']} |
| **Compression** | None |"""


def gen_encoding_matrix(matrix):
    lines = [
        "| Configuration | Bytes/Point | Space Savings | vs Raw | Encode (ns/op) | Decode (ns/op) | Iterate (ns/op) |",
        "|---|---:|---:|---:|---:|---:|---:|",
    ]
    for r in sorted(matrix, key=lambda x: x['bytes_per_point']):
        lines.append(
            f"| {fmt_label(r['label'])} | {r['bytes_per_point']:.3f} "
            f"| {r['space_savings_pct']:.1f}% | {r['vs_raw_ratio']:.3f}× "
            f"| {r['encode']['ns_per_op']:,.0f} | {r['decode']['ns_per_op']:,.0f} "
            f"| {r['iter_seq']['ns_per_op']:,.0f} |"
        )
    return '\n'.join(lines)


def gen_perf_table(matrix, field):
    lines = [
        "| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |",
        "|---|---:|---:|---:|",
    ]
    for r in sorted(matrix, key=lambda x: x[field]['ns_per_op']):
        m = r[field]
        lines.append(
            f"| {fmt_label(r['label'])} | {m['ns_per_op']:,.0f} "
            f"| {m['bytes_per_op']:,} | {m['allocs_per_op']} |"
        )
    return '\n'.join(lines)


def gen_random_access_table(matrix):
    """Measured ValueAt/TimestampAt at a uniformly random index per metric,
    sorted by combined cost, with each axis's verified At()-complexity class."""
    def combined(r):
        return r['random_value_at']['ns_per_op'] + r['random_timestamp_at']['ns_per_op']

    lines = [
        "| Configuration | ValueAt (ns/op) | Value complexity | TimestampAt (ns/op) | Timestamp complexity |",
        "|---|---:|---|---:|---|",
    ]
    for r in sorted(matrix, key=combined):
        _, _, val_key = parse_label(r['label'])
        lines.append(
            f"| {fmt_label(r['label'])} | {r['random_value_at']['ns_per_op']:,.0f} "
            f"| {AT_COMPLEXITY.get(val_key, 'unknown')} "
            f"| {r['random_timestamp_at']['ns_per_op']:,.0f} "
            f"| {ts_complexity(r['label'])} |"
        )
    return '\n'.join(lines)


def gen_scaling_table(scaling, shared):
    filtered = [s for s in scaling if s['label'].startswith('shared-') == shared]
    ppms = sorted({p['points_per_metric'] for s in filtered for p in s['points_series']})
    lines = [
        "| Points/Metric |" + "".join(f" {s['label']} |" for s in filtered),
        "|---:|" + "---:|" * len(filtered),
    ]
    for ppm in ppms:
        row = f"| {ppm} |"
        for s in filtered:
            bpp = next((p['bytes_per_point'] for p in s['points_series'] if p['points_per_metric'] == ppm), None)
            row += f" {bpp:.3f} |" if bpp is not None else " — |"
        lines.append(row)
    return '\n'.join(lines)


# ---------------------------------------------------------------- profile tables

def load_profiles(directory):
    """Return [(profile_name, matrix_by_label, metadata)]: mixed blobs first,
    then single-kind profiles, then the worst-case references."""
    out = []
    for path in sorted(glob.glob(os.path.join(directory, 'matrix_*.json'))):
        with open(path) as f:
            data = json.load(f)
        name = data['metadata']['data_config'].get('profile') or os.path.basename(path)[len('matrix_'):-len('.json')]
        out.append((name, by_label(data['matrix']), data['metadata']))

    def order(item):
        name = item[0]
        if name.startswith('mix_'):
            return (0, name)
        if name in ('worst_case', 'legacy_random_walk'):
            return (2, name)
        return (1, name)

    return sorted(out, key=order)


def gen_profile_reproduce(profiles):
    names = ' '.join(name for name, _, _ in profiles)
    return f"""```bash
cd tests/measurev2
for p in {names}; do
  go run . -profile "$p" -pretty -output "results/matrix_$p.json"
done
```"""


def gen_profile_descriptions(profiles):
    lines = ["| Profile | Data |", "|---|---|"]
    for name, _, meta in profiles:
        desc = describe_profile(meta.get('profile_spec'))
        desc = desc.replace(':\n\n- ', ': ').replace('\n- ', '; ').replace('\n', ' ')
        lines.append(f"| `{name}` | {desc} |")
    return '\n'.join(lines)


def gen_profile_production_sizes(profiles):
    lines = [
        "| Profile | " + " | ".join(VAL_NAMES[v] for v in VALS) + " | Smallest | Smallest vs Chimp |",
        "|---|" + "---:|" * len(VALS) + "---|---:|",
    ]
    for name, m, _ in profiles:
        sizes = {v: m[f'{PROD_TS}-{v}']['bytes_per_point'] for v in VALS}
        best = min(sizes, key=sizes.get)
        cells = [f"**{sizes[v]:.3f}**" if sizes[v] == sizes[best] else f"{sizes[v]:.3f}" for v in VALS]
        lines.append(
            f"| `{name}` | " + " | ".join(cells)
            + f" | {VAL_NAMES[best]} | {signed_pct(sizes[best] / sizes['chimp'] - 1)} |"
        )
    return '\n'.join(lines)


def gen_profile_grids(profiles):
    out = []
    rows = [(f"Shared {TS_NAMES[t]}", f"shared-{t}") for t in TSS] + [(TS_NAMES[t], t) for t in TSS]
    for name, m, _ in profiles:
        lo = min(m[f'{prefix}-{v}']['bytes_per_point'] for _, prefix in rows for v in VALS)
        out.append(f"#### {name}\n")
        out.append("| ts \\ val | " + " | ".join(VAL_NAMES[v] for v in VALS) + " |")
        out.append("|---|" + "---:|" * len(VALS))
        for title, prefix in rows:
            cells = []
            for v in VALS:
                x = m[f'{prefix}-{v}']['bytes_per_point']
                cells.append(f"**{x:.3f}**" if x == lo else f"{x:.3f}")
            out.append(f"| {title} | " + " | ".join(cells) + " |")
        out.append("")
    return '\n'.join(out).rstrip()


def gen_profile_speed(profiles):
    lines = [
        "| Profile | Codec | Encode ns/point | Iterate ns/point | ValueAt ns/op | Encode allocs/blob |",
        "|---|---|---:|---:|---:|---:|",
    ]
    for name, m, _ in profiles:
        rows = [(v, m[f'{PROD_TS}-{v}']) for v in VALS]
        fastest = min(r['iter_seq']['ns_per_op'] for _, r in rows)
        for i, (v, r) in enumerate(rows):
            tp = r['total_points']
            it = f"{r['iter_seq']['ns_per_op'] / tp:.2f}"
            if r['iter_seq']['ns_per_op'] == fastest:
                it = f"**{it}**"
            label = f"`{name}`" if i == 0 else ''
            lines.append(
                f"| {label} | {VAL_NAMES[v]} | {r['encode']['ns_per_op'] / tp:.2f} "
                f"| {it} | {r['random_value_at']['ns_per_op']:,.0f} | {r['encode']['allocs_per_op']} |"
            )
    return '\n'.join(lines)


# ---------------------------------------------------------------- facts digest

def pareto(matrix, x, y):
    """Combos not dominated on (x, y), both lower-is-better."""
    front = [
        r for r in matrix
        if not any(x(o) <= x(r) and y(o) <= y(r) and (x(o) < x(r) or y(o) < y(r)) for o in matrix)
    ]
    return sorted(front, key=x)


def digest_dataset(title, matrix):
    def bpp(r):
        return r['bytes_per_point']

    metrics = [
        ('Fastest encode', lambda r: r['encode']['ns_per_op'], 'ns'),
        ('Fastest decode (open)', lambda r: r['decode']['ns_per_op'], 'ns'),
        ('Fastest iterate', lambda r: r['iter_seq']['ns_per_op'], 'ns'),
        ('Fastest ValueAt', lambda r: r['random_value_at']['ns_per_op'], 'ns'),
        ('Fastest TimestampAt', lambda r: r['random_timestamp_at']['ns_per_op'], 'ns'),
    ]

    def show(rows, f, unit):
        if unit == 'B/pt':
            return ', '.join(f"{fmt_label(r['label'])} {f(r):.3f} B/pt" for r in rows)
        return ', '.join(f"{fmt_label(r['label'])} {f(r):,.0f} {unit}" for r in rows)

    shared = [r for r in matrix if r['label'].startswith('shared-')]
    plain = [r for r in matrix if not r['label'].startswith('shared-')]
    lines = [f"## {title}", ""]
    lines.append(f"- Smallest, shared timestamps: {show(sorted(shared, key=bpp)[:3], bpp, 'B/pt')}")
    lines.append(f"- Smallest, per-metric timestamps: {show(sorted(plain, key=bpp)[:3], bpp, 'B/pt')}")
    for name, f, unit in metrics:
        lines.append(f"- {name}: {show(sorted(matrix, key=f)[:3], f, unit)}")
    it = metrics[2][1]
    lines.append(f"- Size/iterate Pareto front: {show(pareto(matrix, bpp, it), bpp, 'B/pt')}")
    m = by_label(matrix)
    for ref, desc in REFERENCES.items():
        if ref not in m:
            continue
        r0 = m[ref]
        lines.append(
            f"- Against {desc}, {r0['bytes_per_point']:.3f} B/pt, "
            f"encode {r0['encode']['ns_per_op']:,.0f} ns, iterate {r0['iter_seq']['ns_per_op']:,.0f} ns:"
        )
        for r in sorted(matrix, key=bpp)[:6]:
            lines.append(
                f"  - {fmt_label(r['label'])}: size {signed_pct(bpp(r) / bpp(r0) - 1)}, "
                f"encode {r['encode']['ns_per_op'] / r0['encode']['ns_per_op']:.2f}×, "
                f"iterate {r['iter_seq']['ns_per_op'] / r0['iter_seq']['ns_per_op']:.2f}×, "
                f"ValueAt {r['random_value_at']['ns_per_op'] / r0['random_value_at']['ns_per_op']:.2f}×"
            )
    lines.append("")
    return lines


def gen_digest(main_data, profiles):
    meta = main_data['metadata']
    dc = meta['data_config']
    lines = [
        "# Performance report facts digest",
        "",
        f"Main data set: `{dc.get('profile')}`, {dc['num_metrics']} × {dc['points_per_metric']}, "
        f"{meta['timestamp'][:10]}, Go {meta['go_version']}.",
        "Sizes are deterministic; timings are single-run benchmarks that code placement alone can move by 20–40%.",
        "",
    ]
    lines += digest_dataset(f"Main: {dc.get('profile')}", main_data['matrix'])
    lines += ["## Value codecs per profile at Shared DeltaPacked, smallest first", ""]
    for name, m, _ in profiles:
        sizes = sorted((m[f'{PROD_TS}-{v}']['bytes_per_point'], v) for v in VALS)
        chimp = m[f'{PROD_TS}-chimp']['bytes_per_point']
        lines.append(
            f"- `{name}`: " + ', '.join(f"{VAL_NAMES[v]} {b:.3f}" for b, v in sizes)
            + f" (smallest vs Chimp {signed_pct(sizes[0][0] / chimp - 1)})"
        )
    lines.append("")
    for name, m, _ in profiles:
        lines += digest_dataset(f"Profile: {name}", list(m.values()))
    return '\n'.join(lines) + '\n'


# ---------------------------------------------------------------- driver

def render(main_data, profiles, template):
    meta = main_data['metadata']
    matrix = main_data['matrix']
    scaling = main_data['scaling']
    replacements = {
        '{{BENCHMARK_METADATA}}': gen_benchmark_metadata(meta),
        '{{BENCHMARK_METADATA_DETAIL}}': gen_benchmark_metadata_detail(meta),
        '{{DATASET_DESCRIPTION}}': describe_profile(meta.get('profile_spec')),
        '{{ENCODING_MATRIX}}': gen_encoding_matrix(matrix),
        '{{ENCODE_PERFORMANCE}}': gen_perf_table(matrix, 'encode'),
        '{{DECODE_PERFORMANCE}}': gen_perf_table(matrix, 'decode'),
        '{{ITERATION_PERFORMANCE}}': gen_perf_table(matrix, 'iter_seq'),
        '{{RANDOM_ACCESS_PERFORMANCE}}': gen_random_access_table(matrix),
        '{{SCALING_TABLE_STANDARD}}': gen_scaling_table(scaling, shared=False),
        '{{SCALING_TABLE_SHARED}}': gen_scaling_table(scaling, shared=True),
        '{{PROFILE_REPRODUCE}}': gen_profile_reproduce(profiles),
        '{{PROFILE_DESCRIPTIONS}}': gen_profile_descriptions(profiles),
        '{{PROFILE_PRODUCTION_SIZES}}': gen_profile_production_sizes(profiles),
        '{{PROFILE_GRIDS}}': gen_profile_grids(profiles),
        '{{PROFILE_SPEED}}': gen_profile_speed(profiles),
    }
    out = template
    for placeholder, content in replacements.items():
        if placeholder not in out:
            print(f"WARNING: placeholder {placeholder} not found in template", file=sys.stderr)
        out = out.replace(placeholder, content)
    unknown = [p for p in ANY_PLACEHOLDER.findall(out) if not LLM_PLACEHOLDER.fullmatch(p)]
    if unknown:
        print(f"ERROR: unfilled table placeholders: {unknown}", file=sys.stderr)
        sys.exit(1)
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--main', help='main benchmark JSON (the default profile)')
    ap.add_argument('--profiles', help='directory of matrix_<profile>.json files')
    ap.add_argument('--template', help='PERFORMANCE_TEMPLATE.md')
    ap.add_argument('--out', help='output markdown, usually docs/performance.md')
    ap.add_argument('--digest', help='write the facts digest here (keep it out of the repo)')
    ap.add_argument('--check', metavar='FILE', help='fail if FILE still has {{...}} placeholders')
    args = ap.parse_args()

    if args.check:
        with open(args.check) as f:
            left = ANY_PLACEHOLDER.findall(f.read())
        if left:
            print(f"ERROR: {args.check} still has placeholders: {left}", file=sys.stderr)
            sys.exit(1)
        print(f"{args.check}: no placeholders left")
        return

    if not (args.main and args.profiles and args.template and args.out):
        ap.error('--main, --profiles, --template and --out are required unless --check is given')

    with open(args.main) as f:
        main_data = json.load(f)
    with open(args.template) as f:
        template = f.read()
    profiles = load_profiles(args.profiles)
    if not profiles:
        print(f"ERROR: no matrix_*.json in {args.profiles}", file=sys.stderr)
        sys.exit(1)

    out = render(main_data, profiles, template)
    with open(args.out, 'w') as f:
        f.write(out)
    if args.digest:
        with open(args.digest, 'w') as f:
            f.write(gen_digest(main_data, profiles))

    left = sorted(set(LLM_PLACEHOLDER.findall(out)))
    print(f"Wrote {args.out}: {len(main_data['matrix'])} combos, {len(profiles)} profiles")
    if args.digest:
        print(f"Wrote the facts digest to {args.digest}")
    print("Sections for the agent to write (SKILL.md Step 3): " + ', '.join(left))


if __name__ == '__main__':
    main()
