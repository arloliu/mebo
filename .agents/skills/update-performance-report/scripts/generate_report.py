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

MAIN.json and DIR come from one layouts.sh run (OUTDIR/merged/main.json and OUTDIR/merged/profiles),
or from the legacy single-run commands; DIR holds one `matrix_<profile>.json` per report profile.
The input set is validated before anything is written (report_schema.load_input_set):
publication accepts only all-legacy or all-merged input.
With --check, the script fails if any `{{...}}` placeholder is left in the file.
"""
import argparse
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from report_schema import (  # noqa: E402
    DECIDE_GAP, LAYOUTS, MAIN_PROFILE, REPORT_PROFILES, SchemaError, compare, load_input_set,
)

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

# Marks in rendered tables.
INCONCLUSIVE_MARK = '†'
ALLOC_MARK = '‡'
UNTIMED = '—'

LLM_PLACEHOLDER = re.compile(r'\{\{LLM:[A-Z_]+\}\}')
ANY_PLACEHOLDER = re.compile(r'\{\{[A-Z_:]+\}\}')


class Dataset:
    """One data set's matrix plus the context its tables need: whether it is merged, and its allocation disagreements."""

    def __init__(self, name, doc):
        self.name = name
        self.doc = doc
        self.meta = doc['metadata']
        self.matrix = doc['matrix']
        self.rows = by_label(doc['matrix'])
        self.merged = 'format_version' in doc
        self.disagreements = set(doc.get('allocation_disagreements', []))

    def alloc(self, row, op):
        """allocs/op of a timed cell, marked when its runs disagreed."""
        value = f"{row[op]['allocs_per_op']}"
        if f'{self.name}/{row["label"]}/{op}' in self.disagreements:
            value += f' {ALLOC_MARK}'
        return value


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


def ns(row, op):
    """ns/op of a timed cell, or None when the cell was not timed."""
    m = row.get(op)
    return None if m is None else m['ns_per_op']


def fmt_ns(row, op):
    v = ns(row, op)
    return UNTIMED if v is None else f'{v:,.0f}'


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


# ---------------------------------------------------------------- methodology

def timing_summary(main, sep=' '):
    """How the timings were measured, from the metadata, its clauses joined by sep
    (a space inside a table cell, a newline in prose so that each sentence starts a line)."""
    if not main.merged:
        return sep.join(['Single `testing.Benchmark` runs at the default 1 s benchtime, one process per data set;',
                         'the CPU affinity and GOMAXPROCS were not recorded.',
                         'Code placement alone can move these timings by 20–40%.'])
    c = main.doc['common']
    cpus = ', '.join(str(x) for x in c['cpu_affinity'])
    return sep.join([f'Layout-averaged `testing.Benchmark`: {len(LAYOUTS)} code layouts × {c["rounds"]} rounds,',
                     f'{len(LAYOUTS) * c["rounds"]} runs per cell at a {c["benchtime"]} benchtime,',
                     f'pinned to CPU {cpus} ({c["cpu_model"]}) with GOMAXPROCS={c["gomaxprocs"]};',
                     'every data set measured in one process per run.',
                     'The layouts vary the placement of the repository\'s code only, not the runtime\'s or the standard library\'s.'])


def gen_benchmark_metadata(main):
    meta = main.meta
    dc = meta['data_config']
    total = dc['num_metrics'] * dc['points_per_metric']
    # A pinned process sees one CPU, so merged input names its CPU instead of a count.
    cpus = main.doc['common']['cpu_model'] if main.merged else f"{meta['num_cpu']} CPUs"
    return f"""| | |
|---|---|
| **Benchmark Date** | {meta['timestamp'][:10]} |
| **Platform** | {meta['os']}/{meta['arch']} ({cpus}), Go {meta['go_version']} |
| **Data** | `{dc.get('profile', '')}` profile: {dc['num_metrics']} metrics × {dc['points_per_metric']} points = {total:,} data points |
| **Timing** | {timing_summary(main)} |
| **Compression Codecs** | None (encoding algorithms only) |"""


def gen_benchmark_metadata_detail(main):
    meta = main.meta
    dc = meta['data_config']
    rows = [
        "| Parameter | Value |",
        "|-----------|-------|",
        f"| **Go Version** | {meta['go_version']} |",
        f"| **OS / Arch** | {meta['os']}/{meta['arch']} |",
    ]
    if not main.merged:
        rows.append(f"| **CPU Cores** | {meta['num_cpu']} |")
    else:
        c = main.doc['common']
        rows += [
            f"| **CPU** | {c['cpu_model']} |",
            f"| **Pinned CPU** | {', '.join(str(x) for x in c['cpu_affinity'])} |",
            f"| **GOMAXPROCS** | {c['gomaxprocs']} |",
            f"| **Benchtime** | {c['benchtime']} |",
            f"| **Layouts × Rounds** | {len(LAYOUTS)} × {c['rounds']} |",
            f"| **Cells** | `{c['cells']}` |",
        ]
    rows += [
        f"| **Profile** | `{dc.get('profile', '')}` |",
        f"| **Metrics** | {dc['num_metrics']} |",
        f"| **Points/Metric** | {dc['points_per_metric']} |",
        f"| **Seed** | {dc['seed']} |",
        "| **Compression** | None |",
    ]
    return '\n'.join(rows)


def layouts_command(main):
    c = main.doc['common']
    cpu = c['cpu_affinity'][0] if len(c['cpu_affinity']) == 1 else 6
    return (f"tests/measurev2/layouts.sh -o $TMPDIR/perf -cpu {cpu} -benchtime {c['benchtime']} "
            f"-cells {c['cells']} -rounds {c['rounds']}")


def gen_timing_method(main, profiles):
    if not main.merged:
        return (timing_summary(main, '\n') + '\n'
                f'Treat gaps under about {DECIDE_GAP:.0%} as ties.')
    disagreements = any(d.disagreements for d in [main, *profiles])
    lines = [
        timing_summary(main, '\n'),
        'Each cell reports the median of its runs.',
        f'A speed comparison is **decided** only when the gap is at least {DECIDE_GAP:.0%} '
        'and every layout\'s median puts the same combo ahead;',
        f'a gap under {DECIDE_GAP:.0%} is **equivalent**, and a larger gap that not every layout supports is **inconclusive**.',
        'Pinning one core with GOMAXPROCS=1 puts the garbage collector on the measured core,',
        'so allocation-heavy operations read slower than in reports measured without pinning.',
        'Half of the runs go through the data sets in reverse order, where an encode benchmark starts with a one-off allocation,',
        'so B/op of some encode cells reads up to about 1.5% above the steady state.',
    ]
    if disagreements:
        lines.append(f'{ALLOC_MARK} marks a cell whose allocs/op differed between runs; the median is shown.')
    return '\n'.join(lines)


def gen_running_benchmarks(main):
    if not main.merged:
        return """```bash
# Main data set (mix_monitoring, 100 metrics × 150 points)
cd tests/measurev2 && go run . -pretty -verbose -output results.json

# One data-shape profile
cd tests/measurev2 && go run . -profile cal_2dp_hold50 -pretty -output results_hold50.json

# Via Makefile
make bench-measure
```"""
    return f"""```bash
# Every table of this report: four code layouts × {main.doc['common']['rounds']} rounds, pinned, merged into OUTDIR/merged/
{layouts_command(main)}

# Via Makefile (the same command with its defaults)
make bench-report
```"""


# ---------------------------------------------------------------- main tables

def gen_encoding_matrix(matrix):
    lines = [
        "| Configuration | Bytes/Point | Space Savings | vs Raw | Encode (ns/op) | Decode (ns/op) | Iterate (ns/op) |",
        "|---|---:|---:|---:|---:|---:|---:|",
    ]
    for r in sorted(matrix, key=lambda x: x['bytes_per_point']):
        lines.append(
            f"| {fmt_label(r['label'])} | {r['bytes_per_point']:.3f} "
            f"| {r['space_savings_pct']:.1f}% | {r['vs_raw_ratio']:.3f}× "
            f"| {fmt_ns(r, 'encode')} | {fmt_ns(r, 'decode')} "
            f"| {fmt_ns(r, 'iter_seq')} |"
        )
    return '\n'.join(lines)


def gen_perf_table(ds, field):
    lines = [
        "| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |",
        "|---|---:|---:|---:|",
    ]
    timed = [r for r in ds.matrix if r.get(field) is not None]
    for r in sorted(timed, key=lambda x: x[field]['ns_per_op']):
        m = r[field]
        lines.append(
            f"| {fmt_label(r['label'])} | {m['ns_per_op']:,.0f} "
            f"| {m['bytes_per_op']:,} | {ds.alloc(r, field)} |"
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
    timed = [r for r in matrix if r.get('random_value_at') is not None and r.get('random_timestamp_at') is not None]
    for r in sorted(timed, key=combined):
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

def order_profiles(docs):
    """[Dataset]: mixed blobs first, then single-kind profiles, then the worst-case references."""
    def order(name):
        if name.startswith('mix_'):
            return (0, name)
        if name in ('worst_case', 'legacy_random_walk'):
            return (2, name)
        return (1, name)

    return [Dataset(name, docs[name]) for name in sorted(docs, key=order)]


def gen_profile_reproduce(main, profiles):
    if main.merged:
        return f"""The profile tables come from the same run as the main tables:

```bash
{layouts_command(main)}
```"""
    names = ' '.join(p.name for p in profiles)
    return f"""Reproduce with (the JSON is gitignored):

```bash
cd tests/measurev2
for p in {names}; do
  go run . -profile "$p" -pretty -output "results/matrix_$p.json"
done
```"""


def gen_profile_descriptions(profiles):
    lines = ["| Profile | Data |", "|---|---|"]
    for p in profiles:
        desc = describe_profile(p.meta.get('profile_spec'))
        desc = desc.replace(':\n\n- ', ': ').replace('\n- ', '; ').replace('\n', ' ')
        lines.append(f"| `{p.name}` | {desc} |")
    return '\n'.join(lines)


def gen_profile_production_sizes(profiles):
    lines = [
        "| Profile | " + " | ".join(VAL_NAMES[v] for v in VALS) + " | Smallest | Smallest vs Chimp |",
        "|---|" + "---:|" * len(VALS) + "---|---:|",
    ]
    for p in profiles:
        sizes = {v: p.rows[f'{PROD_TS}-{v}']['bytes_per_point'] for v in VALS}
        best = min(sizes, key=sizes.get)
        cells = [f"**{sizes[v]:.3f}**" if sizes[v] == sizes[best] else f"{sizes[v]:.3f}" for v in VALS]
        lines.append(
            f"| `{p.name}` | " + " | ".join(cells)
            + f" | {VAL_NAMES[best]} | {signed_pct(sizes[best] / sizes['chimp'] - 1)} |"
        )
    return '\n'.join(lines)


def gen_profile_grids(profiles):
    out = []
    rows = [(f"Shared {TS_NAMES[t]}", f"shared-{t}") for t in TSS] + [(TS_NAMES[t], t) for t in TSS]
    for p in profiles:
        m = p.rows
        lo = min(m[f'{prefix}-{v}']['bytes_per_point'] for _, prefix in rows for v in VALS)
        out.append(f"#### {p.name}\n")
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


def gen_profile_speed_note(main):
    if not main.merged:
        return f"""This table comes from the profile runs, separate from the main tables' run.
**Bold** marks the fastest iteration for each profile and every codec within {DECIDE_GAP:.0%} of it.
These are single-run numbers, which can move by 20–40% with code placement alone;
where two codecs produce identical columns (for example ALP and ALP-RLE on a profile without repeats), a gap between them is noise."""
    return f"""This table comes from the same layout-averaged run as the main tables.
**Bold** marks the fastest iteration for each profile and every codec equivalent to it (a gap under {DECIDE_GAP:.0%});
{INCONCLUSIVE_MARK} marks a codec whose gap to the fastest is at least {DECIDE_GAP:.0%} but not supported by every layout.
Where two codecs produce identical columns (for example ALP and ALP-RLE on a profile without repeats), a gap between them is noise."""


def gen_profile_speed(profiles):
    lines = [
        "| Profile | Codec | Encode ns/point | Iterate ns/point | ValueAt ns/op | Encode allocs/blob |",
        "|---|---|---:|---:|---:|---:|",
    ]
    inconclusive = False
    for p in profiles:
        rows = [(v, p.rows[f'{PROD_TS}-{v}']) for v in VALS]
        fastest = min((r for _, r in rows), key=lambda r: r['iter_seq']['ns_per_op'])
        for i, (v, r) in enumerate(rows):
            tp = r['total_points']
            it = f"{r['iter_seq']['ns_per_op'] / tp:.2f}"
            if r is fastest:
                it = f"**{it}**"
            else:
                outcome, _, _ = compare(r['iter_seq'], fastest['iter_seq'])
                if outcome == 'equivalent':
                    it = f"**{it}**"
                elif outcome == 'inconclusive':
                    it = f"{it} {INCONCLUSIVE_MARK}"
                    inconclusive = True
            label = f"`{p.name}`" if i == 0 else ''
            lines.append(
                f"| {label} | {VAL_NAMES[v]} | {r['encode']['ns_per_op'] / tp:.2f} "
                f"| {it} | {r['random_value_at']['ns_per_op']:,.0f} | {p.alloc(r, 'encode')} |"
            )
    if inconclusive:
        lines += ["", f"{INCONCLUSIVE_MARK} At least {DECIDE_GAP:.0%} slower than the fastest by the medians, "
                      "but not in every layout: inconclusive, not decided."]
    return '\n'.join(lines)


# ---------------------------------------------------------------- facts digest

def ranking(ds, op, top=3):
    """The top entries of a fastest list among timed combos, each compared with the fastest only."""
    timed = [r for r in ds.matrix if r.get(op) is not None]
    timed.sort(key=lambda r: r[op]['ns_per_op'])
    if not timed:
        return 'not timed'
    best = timed[0]
    parts = [f"{fmt_label(best['label'])} {best[op]['ns_per_op']:,.0f} ns (fastest)"]
    for r in timed[1:top]:
        outcome, _, _ = compare(r[op], best[op])
        word = {'equivalent': 'equivalent to the fastest', 'decided': 'slower',
                'inconclusive': 'inconclusive against the fastest'}[outcome]
        parts.append(f"{fmt_label(r['label'])} {r[op]['ns_per_op']:,.0f} ns ({word})")
    return ', '.join(parts)


def pareto(matrix, x, y):
    """Combos not dominated on (x, y), both lower-is-better (point estimates)."""
    front = [
        r for r in matrix
        if not any(x(o) <= x(r) and y(o) <= y(r) and (x(o) < x(r) or y(o) < y(r)) for o in matrix)
    ]
    return sorted(front, key=x)


def decided_front(timed, op):
    """Timed combos that no other combo beats: o beats r if o is no larger and decided faster,
    or strictly smaller and equivalent in time.
    An inconclusive comparison never beats; each front member is annotated with the other members
    it is inconclusive against, in either direction.
    Returns [(row, [labels it is inconclusive against])], smallest first."""
    def size(r):
        return r['bytes_per_point']

    def beats(o, r):
        if o is r or size(o) > size(r):
            return False
        outcome, winner, _ = compare(o[op], r[op])
        return (outcome == 'decided' and winner == 'a') or (outcome == 'equivalent' and size(o) < size(r))

    members = [r for r in timed if not any(beats(o, r) for o in timed)]
    front = []
    for r in members:
        notes = [fmt_label(o['label']) for o in members
                 if o is not r and compare(o[op], r[op])[0] == 'inconclusive']
        front.append((r, notes))
    return sorted(front, key=lambda item: size(item[0]))


def digest_dataset(title, ds):
    def bpp(r):
        return r['bytes_per_point']

    metrics = [
        ('Fastest encode', 'encode'),
        ('Fastest decode (open)', 'decode'),
        ('Fastest iterate', 'iter_seq'),
        ('Fastest ValueAt', 'random_value_at'),
        ('Fastest TimestampAt', 'random_timestamp_at'),
    ]
    matrix = ds.matrix
    sparse = any(r.get(op) is None for r in matrix for _, op in metrics)
    among = ' (among timed combos)' if sparse else ''

    def show_sizes(rows):
        return ', '.join(f"{fmt_label(r['label'])} {bpp(r):.3f} B/pt" for r in rows)

    shared = [r for r in matrix if r['label'].startswith('shared-')]
    plain = [r for r in matrix if not r['label'].startswith('shared-')]
    lines = [f"## {title}", ""]
    lines.append(f"- Smallest, shared timestamps: {show_sizes(sorted(shared, key=bpp)[:3])}")
    lines.append(f"- Smallest, per-metric timestamps: {show_sizes(sorted(plain, key=bpp)[:3])}")
    for name, op in metrics:
        lines.append(f"- {name}{among}: {ranking(ds, op)}")

    timed = [r for r in matrix if r.get('iter_seq') is not None]
    point_front = pareto(timed, bpp, lambda r: r['iter_seq']['ns_per_op'])
    lines.append(f"- Size/iterate Pareto front{among}, point estimates: {show_sizes(point_front)}")
    front = decided_front(timed, 'iter_seq')
    shown = []
    for r, notes in front:
        text = f"{fmt_label(r['label'])} {bpp(r):.3f} B/pt"
        if notes:
            text += f" (inconclusive against {', '.join(notes)})"
        shown.append(text)
    lines.append(f"- Size/iterate decided front{among}: {', '.join(shown)}")

    for ref, desc in REFERENCES.items():
        r0 = ds.rows.get(ref)
        if r0 is None:
            continue
        lines.append(
            f"- Against {desc}, {bpp(r0):.3f} B/pt, "
            f"encode {fmt_ns(r0, 'encode')} ns, iterate {fmt_ns(r0, 'iter_seq')} ns:"
        )
        for r in sorted(matrix, key=bpp)[:6]:
            parts = [f"size {signed_pct(bpp(r) / bpp(r0) - 1)}"]
            for op, short in (('encode', 'encode'), ('iter_seq', 'iterate'), ('random_value_at', 'ValueAt')):
                if r.get(op) is None or r0.get(op) is None:
                    parts.append(f"{short} not timed")
                    continue
                ratio = r[op]['ns_per_op'] / r0[op]['ns_per_op']
                if r is r0:
                    parts.append(f"{short} {ratio:.2f}× (the reference)")
                    continue
                outcome, winner, _ = compare(r[op], r0[op])
                word = {'equivalent': 'equivalent', 'inconclusive': 'inconclusive'}.get(
                    outcome, 'faster' if winner == 'a' else 'slower')
                parts.append(f"{short} {ratio:.2f}× ({word})")
            lines.append(f"  - {fmt_label(r['label'])}: " + ', '.join(parts))
    if ds.disagreements:
        lines.append(f"- Allocation disagreements ({ALLOC_MARK}): " + ', '.join(sorted(ds.disagreements)))
    lines.append("")
    return lines


def gen_digest(main, profiles):
    meta = main.meta
    dc = meta['data_config']
    if main.merged:
        rule = (f"Comparisons: decided needs a gap of at least {DECIDE_GAP:.0%} that every layout's median supports; "
                f"under {DECIDE_GAP:.0%} is equivalent; a larger gap without layout support is inconclusive.")
    else:
        rule = (f"Comparisons on single runs: decided at a gap of at least {DECIDE_GAP:.0%}, otherwise equivalent; "
                "code placement alone can move these timings by 20–40%.")
    lines = [
        "# Performance report facts digest",
        "",
        f"Main data set: `{dc.get('profile')}`, {dc['num_metrics']} × {dc['points_per_metric']}, "
        f"{meta['timestamp'][:10]}, Go {meta['go_version']}.",
        timing_summary(main),
        "Sizes are deterministic.",
        rule,
        "",
    ]
    lines += digest_dataset(f"Main: {dc.get('profile')}", main)
    lines += ["## Value codecs per profile at Shared DeltaPacked, smallest first", ""]
    for p in profiles:
        sizes = sorted((p.rows[f'{PROD_TS}-{v}']['bytes_per_point'], v) for v in VALS)
        chimp = p.rows[f'{PROD_TS}-chimp']['bytes_per_point']
        lines.append(
            f"- `{p.name}`: " + ', '.join(f"{VAL_NAMES[v]} {b:.3f}" for b, v in sizes)
            + f" (smallest vs Chimp {signed_pct(sizes[0][0] / chimp - 1)})"
        )
    lines.append("")
    for p in profiles:
        lines += digest_dataset(f"Profile: {p.name}", p)
    return '\n'.join(lines) + '\n'


# ---------------------------------------------------------------- driver

def render(main, profiles, template):
    replacements = {
        '{{BENCHMARK_METADATA}}': gen_benchmark_metadata(main),
        '{{BENCHMARK_METADATA_DETAIL}}': gen_benchmark_metadata_detail(main),
        '{{DATASET_DESCRIPTION}}': describe_profile(main.meta.get('profile_spec')),
        '{{TIMING_METHOD}}': gen_timing_method(main, profiles),
        '{{RUNNING_BENCHMARKS}}': gen_running_benchmarks(main),
        '{{ENCODING_MATRIX}}': gen_encoding_matrix(main.matrix),
        '{{ENCODE_PERFORMANCE}}': gen_perf_table(main, 'encode'),
        '{{DECODE_PERFORMANCE}}': gen_perf_table(main, 'decode'),
        '{{ITERATION_PERFORMANCE}}': gen_perf_table(main, 'iter_seq'),
        '{{RANDOM_ACCESS_PERFORMANCE}}': gen_random_access_table(main.matrix),
        '{{SCALING_TABLE_STANDARD}}': gen_scaling_table(main.doc['scaling'], shared=False),
        '{{SCALING_TABLE_SHARED}}': gen_scaling_table(main.doc['scaling'], shared=True),
        '{{PROFILE_REPRODUCE}}': gen_profile_reproduce(main, profiles),
        '{{PROFILE_DESCRIPTIONS}}': gen_profile_descriptions(profiles),
        '{{PROFILE_PRODUCTION_SIZES}}': gen_profile_production_sizes(profiles),
        '{{PROFILE_GRIDS}}': gen_profile_grids(profiles),
        '{{PROFILE_SPEED_NOTE}}': gen_profile_speed_note(main),
        '{{PROFILE_SPEED}}': gen_profile_speed(profiles),
    }
    out = template
    for placeholder, content in replacements.items():
        if placeholder not in out:
            raise SchemaError(f"placeholder {placeholder} not found in template")
        out = out.replace(placeholder, content)
    unknown = [p for p in ANY_PLACEHOLDER.findall(out) if not LLM_PLACEHOLDER.fullmatch(p)]
    if unknown:
        raise SchemaError(f"unfilled table placeholders: {unknown}")
    return out


def load(main_path, profiles_dir):
    """Validate the input set and return (main Dataset, [profile Datasets in table order])."""
    _, main_doc, docs = load_input_set(main_path, profiles_dir)
    profiles = order_profiles(docs)
    if sorted(p.name for p in profiles) != sorted(REPORT_PROFILES):
        raise SchemaError('the profile set is not the report manifest')
    return Dataset(MAIN_PROFILE, main_doc), profiles


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--main', help='main benchmark JSON (the default profile)')
    ap.add_argument('--profiles', help='directory of matrix_<profile>.json files')
    ap.add_argument('--template', help='PERFORMANCE_TEMPLATE.md')
    ap.add_argument('--out', help='output markdown, usually docs/performance.md')
    ap.add_argument('--digest', help='write the facts digest here (keep it out of the repo)')
    ap.add_argument('--check', metavar='FILE', help='fail if FILE still has {{...}} placeholders')
    args = ap.parse_args(argv)

    if args.check:
        with open(args.check) as f:
            left = ANY_PLACEHOLDER.findall(f.read())
        if left:
            print(f"ERROR: {args.check} still has placeholders: {left}", file=sys.stderr)
            return 1
        print(f"{args.check}: no placeholders left")
        return 0

    if not (args.main and args.profiles and args.template and args.out):
        ap.error('--main, --profiles, --template and --out are required unless --check is given')

    try:
        main_ds, profiles = load(args.main, args.profiles)
        with open(args.template) as f:
            template = f.read()
        out = render(main_ds, profiles, template)
        digest = gen_digest(main_ds, profiles) if args.digest else None
    except SchemaError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1

    with open(args.out, 'w') as f:
        f.write(out)
    if digest is not None:
        with open(args.digest, 'w') as f:
            f.write(digest)

    left = sorted(set(LLM_PLACEHOLDER.findall(out)))
    print(f"Wrote {args.out}: {len(main_ds.matrix)} combos, {len(profiles)} profiles")
    if args.digest:
        print(f"Wrote the facts digest to {args.digest}")
    print("Sections for the agent to write (SKILL.md Step 3): " + ', '.join(left))
    return 0


if __name__ == '__main__':
    sys.exit(main())
