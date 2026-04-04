#!/usr/bin/env python3
"""Generate PERFORMANCE_V2.md from benchmark JSON + template.

Usage:
    python3 generate_report.py <benchmark_json> <template_md> <output_md>

Example:
    python3 .agents/skills/update-performance-report/scripts/generate_report.py \
        /tmp/mebo_bench_results.json \
        .agents/skills/update-performance-report/PERFORMANCE_TEMPLATE.md \
        docs/PERFORMANCE_V2.md
"""
import json
import re
import sys


def fmt_label(label):
    """Convert label like 'shared-delta-chimp' to 'Shared Delta + Chimp'."""
    parts = label.split('-')
    if parts[0] == 'shared':
        ts = parts[1]
        val = parts[2]
        ts_name = {'raw': 'Raw', 'delta': 'Delta', 'deltapacked': 'DeltaPacked'}[ts]
        val_name = {'raw': 'Raw', 'gorilla': 'Gorilla', 'chimp': 'Chimp'}[val]
        return f"Shared {ts_name} + {val_name}"
    else:
        ts = parts[0]
        val = parts[1]
        ts_name = {'raw': 'Raw', 'delta': 'Delta', 'deltapacked': 'DeltaPacked'}[ts]
        val_name = {'raw': 'Raw', 'gorilla': 'Gorilla', 'chimp': 'Chimp'}[val]
        return f"{ts_name} + {val_name}"


def gen_benchmark_metadata(meta):
    dc = meta['data_config']
    total = dc['num_metrics'] * dc['points_per_metric']
    ts = meta['timestamp'][:10]  # Just the date
    return f"""| | |
|---|---|
| **Benchmark Date** | {ts} |
| **Platform** | {meta['os']}/{meta['arch']} ({meta['num_cpu']} CPUs), Go {meta['go_version']} |
| **Data** | {dc['num_metrics']} metrics × {dc['points_per_metric']} points = {total:,} total data points |
| **Value Jitter** | ±{dc['value_jitter_pct']}% per point (random walk) |
| **Timestamp Jitter** | ±{dc['ts_jitter_pct']}% of 1s interval |
| **Compression Codecs** | None (testing encoding algorithms only) |"""


def gen_benchmark_metadata_detail(meta):
    dc = meta['data_config']
    return f"""| Parameter | Value | Description |
|-----------|-------|-------------|
| **Go Version** | {meta['go_version']} | Compiler and runtime |
| **OS / Arch** | {meta['os']}/{meta['arch']} | Operating system and CPU architecture |
| **CPU Cores** | {meta['num_cpu']} | Available logical CPUs |
| **Metrics** | {dc['num_metrics']} | Number of independent sensor metrics |
| **Points/Metric** | {dc['points_per_metric']} | Data points per metric (for matrix benchmarks) |
| **Value Jitter** | ±{dc['value_jitter_pct']}% | Per-point random walk delta (models semiconductor sensor noise) |
| **Timestamp Jitter** | ±{dc['ts_jitter_pct']}% | Variation in 1-second sampling interval (models industrial protocol jitter) |
| **Sampling Interval** | 1 second | Base interval between data points |
| **Seed** | 42 | Fixed for reproducibility |
| **Compression** | None | No codec layer — testing encoding algorithms only |"""


def gen_quick_reference(matrix):
    by_bpp = sorted(matrix, key=lambda x: x['bytes_per_point'])
    by_enc = sorted(matrix, key=lambda x: x['encode']['ns_per_op'])
    raw_raw = next(r for r in matrix if r['label'] == 'raw-raw')

    best = by_bpp[0]
    second = by_bpp[1]
    fastest = by_enc[0]

    return f"""| Metric | Value | Configuration |
|--------|-------|---------------|
| **Best Compression** | {best['bytes_per_point']:.2f} bytes/point ({best['space_savings_pct']:.1f}% savings) | {fmt_label(best['label'])} |
| **Best Balance** | {second['bytes_per_point']:.2f} bytes/point ({second['space_savings_pct']:.1f}% savings) | {fmt_label(second['label'])} |
| **Fastest Encode** | {fastest['encode']['ns_per_op']:,.0f} ns/op | {fmt_label(fastest['label'])} |
| **Baseline** | {raw_raw['bytes_per_point']:.2f} bytes/point | Raw + Raw |"""


def gen_encoding_matrix(matrix):
    by_bpp = sorted(matrix, key=lambda x: x['bytes_per_point'])
    lines = [
        "| Configuration | Bytes/Point | Space Savings | vs Raw | Encode (ns/op) | Decode (ns/op) | Iterate (ns/op) |",
        "|---------------|-------------|---------------|--------|----------------|----------------|-----------------|",
    ]
    for r in by_bpp:
        lines.append(
            f"| {fmt_label(r['label'])} | {r['bytes_per_point']:.2f} "
            f"| {r['space_savings_pct']:.1f}% | {r['vs_raw_ratio']:.2f}× "
            f"| {r['encode']['ns_per_op']:,.0f} | {r['decode']['ns_per_op']:,.0f} "
            f"| {r['iter_seq']['ns_per_op']:,.0f} |"
        )
    return '\n'.join(lines)


def gen_encoding_observations(matrix):
    by_bpp = sorted(matrix, key=lambda x: x['bytes_per_point'])
    best = by_bpp[0]

    # Find specific combos for comparison
    shared_combos = [r for r in matrix if r['label'].startswith('shared-')]
    non_shared = [r for r in matrix if not r['label'].startswith('shared-')]
    non_shared_by_bpp = sorted(non_shared, key=lambda x: x['bytes_per_point'])
    best_ns = non_shared_by_bpp[0]

    # Chimp vs Gorilla (non-shared delta)
    d_chimp = next(r for r in matrix if r['label'] == 'delta-chimp')
    d_gorilla = next(r for r in matrix if r['label'] == 'delta-gorilla')
    chimp_vs_gorilla = (1 - d_chimp['bytes_per_point'] / d_gorilla['bytes_per_point']) * 100

    # DeltaPacked vs Delta
    dp_chimp = next(r for r in matrix if r['label'] == 'deltapacked-chimp')
    dp_vs_d = (dp_chimp['bytes_per_point'] / d_chimp['bytes_per_point'] - 1) * 100

    # Shared vs non-shared (best of each)
    shared_best = sorted(shared_combos, key=lambda x: x['bytes_per_point'])[0]
    shared_savings_over_ns = (1 - shared_best['bytes_per_point'] / best_ns['bytes_per_point']) * 100

    # Fastest encode
    by_enc = sorted(matrix, key=lambda x: x['encode']['ns_per_op'])
    fastest = by_enc[0]
    raw_raw = next(r for r in matrix if r['label'] == 'raw-raw')

    lines = []
    lines.append(
        f"- **Best compression**: {fmt_label(best['label'])} achieves "
        f"{best['bytes_per_point']:.2f} bytes/point ({best['space_savings_pct']:.1f}% savings "
        f"vs raw-raw baseline). Shared timestamp deduplication eliminates redundant timestamp "
        f"storage across 200 metrics."
    )
    lines.append(
        f"- **Shared timestamps**: Enabling `WithSharedTimestamps()` provides "
        f"{abs(shared_savings_over_ns):.0f}% additional savings over the best non-shared "
        f"configuration ({fmt_label(best_ns['label'])} at {best_ns['bytes_per_point']:.2f} "
        f"bytes/point). The savings come from storing the timestamp column once instead of "
        f"200 times."
    )
    lines.append(
        f"- **Chimp vs Gorilla**: Chimp consistently outperforms Gorilla by "
        f"~{chimp_vs_gorilla:.1f}% in compression. For example, Delta + Chimp "
        f"({d_chimp['bytes_per_point']:.2f} BPP) vs Delta + Gorilla "
        f"({d_gorilla['bytes_per_point']:.2f} BPP). Both use XOR-based floating-point encoding."
    )
    lines.append(
        f"- **DeltaPacked vs Delta**: DeltaPacked shows ~{abs(dp_vs_d):.1f}% "
        f"{'larger' if dp_vs_d > 0 else 'smaller'} encoded size than Delta "
        f"({dp_chimp['bytes_per_point']:.2f} vs {d_chimp['bytes_per_point']:.2f} BPP). "
        f"DeltaPacked's advantage is **decode/iteration speed** via Group Varint batch "
        f"decoding, not compression ratio."
    )
    lines.append(
        f"- **Encode speed tradeoff**: {fmt_label(fastest['label'])} encodes fastest at "
        f"{fastest['encode']['ns_per_op']:.0f} ns/op. Raw + Raw baseline "
        f"({raw_raw['encode']['ns_per_op']:.0f} ns/op) is not the fastest because larger "
        f"raw data requires more memory allocation ({raw_raw['encode']['bytes_per_op']:,} "
        f"B/op vs {fastest['encode']['bytes_per_op']:,} B/op)."
    )

    # Decode speed: shared vs non-shared
    shared_dec = sorted(shared_combos, key=lambda x: x['decode']['ns_per_op'])
    non_shared_dec = sorted(non_shared, key=lambda x: x['decode']['ns_per_op'])
    lines.append(
        f"- **Decode speed**: Shared-TS combos decode "
        f"~{(1 - shared_dec[0]['decode']['ns_per_op']/non_shared_dec[0]['decode']['ns_per_op'])*100:.0f}% "
        f"faster than non-shared ({shared_dec[0]['decode']['ns_per_op']:.0f} vs "
        f"{non_shared_dec[0]['decode']['ns_per_op']:.0f} ns/op) due to smaller blob size "
        f"and shared timestamp index."
    )

    return '\n'.join(lines)


def gen_perf_table(matrix, field):
    by_speed = sorted(matrix, key=lambda x: x[field]['ns_per_op'])
    lines = [
        "| Configuration | Speed (ns/op) | Memory (B/op) | Allocs/op |",
        "|---------------|---------------|---------------|-----------|",
    ]
    for r in by_speed:
        m = r[field]
        lines.append(
            f"| {fmt_label(r['label'])} | {m['ns_per_op']:,.0f} "
            f"| {m['bytes_per_op']:,} | {m['allocs_per_op']} |"
        )
    return '\n'.join(lines)


def gen_scaling_table(scaling, prefix_filter=None):
    """Generate a scaling pivot table.

    If prefix_filter is 'shared-', only shared combos.
    If prefix_filter is 'standard', only non-shared combos. If None, all.
    """
    if prefix_filter == 'shared-':
        filtered = [s for s in scaling if s['label'].startswith('shared-')]
    elif prefix_filter == 'standard':
        filtered = [s for s in scaling if not s['label'].startswith('shared-')]
    else:
        filtered = scaling

    # Get all PPMs and sort
    ppms = sorted(set(p['points_per_metric'] for s in filtered for p in s['points_series']))

    # Build header
    labels = [s['label'] for s in filtered]
    header = "| Points/Metric |"
    sep = "|---------------|"
    for lbl in labels:
        header += f" {lbl} |"
        sep += "---:|"

    lines = [header, sep]

    for ppm in ppms:
        row = f"| {ppm} |"
        for s in filtered:
            bpp = next(
                (p['bytes_per_point'] for p in s['points_series']
                 if p['points_per_metric'] == ppm),
                None,
            )
            row += f" {bpp:.2f} |" if bpp else " — |"
        lines.append(row)

    return '\n'.join(lines)


def gen_scaling_insights(scaling, matrix):
    # Best compression combo
    by_bpp = sorted(matrix, key=lambda x: x['bytes_per_point'])
    best_label = by_bpp[0]['label']
    best_scaling = next(s for s in scaling if s['label'] == best_label)
    best_series = {p['points_per_metric']: p['bytes_per_point'] for p in best_scaling['points_series']}

    # Converged value
    max_ppm = max(best_series.keys())
    converged = best_series[max_ppm]

    # Find raw-raw scaling for comparison
    rr_scaling = next(s for s in scaling if s['label'] == 'raw-raw')
    rr_series = {p['points_per_metric']: p['bytes_per_point'] for p in rr_scaling['points_series']}
    rr_converged = rr_series[max_ppm]

    # Shared vs non-shared comparison at different PPMs
    best_ns_label = sorted(
        [r for r in matrix if not r['label'].startswith('shared-')],
        key=lambda x: x['bytes_per_point'],
    )[0]['label']
    best_ns_scaling = next(s for s in scaling if s['label'] == best_ns_label)
    best_ns_series = {p['points_per_metric']: p['bytes_per_point'] for p in best_ns_scaling['points_series']}

    lines = []

    # Find threshold where BPP within 30% of converged
    for ppm in sorted(best_series.keys()):
        if best_series[ppm] <= converged * 1.3:
            lines.append(
                f"- **Overhead becomes acceptable at ~{ppm} PPM**: "
                f"{fmt_label(best_label)} reaches {best_series[ppm]:.2f} bytes/point "
                f"(within 30% of converged value {converged:.2f})."
            )
            break

    # Find threshold where BPP within 5%
    for ppm in sorted(best_series.keys()):
        if best_series[ppm] <= converged * 1.05:
            lines.append(
                f"- **Diminishing returns above ~{ppm} PPM**: BPP converges to "
                f"{converged:.2f} (within 5% threshold reached at {ppm} PPM with "
                f"{best_series[ppm]:.2f} BPP)."
            )
            break

    # Shared TS savings scale with metrics
    lines.append(
        f"- **Shared timestamps scale with metric count**: At {max_ppm} PPM, "
        f"{fmt_label(best_label)} achieves {converged:.2f} BPP vs "
        f"{fmt_label(best_ns_label)} at {best_ns_series[max_ppm]:.2f} BPP — a "
        f"{(1 - converged/best_ns_series[max_ppm])*100:.0f}% additional saving from "
        f"timestamp deduplication across 200 metrics."
    )

    # Low PPM overhead
    min_ppm = min(best_series.keys())
    lines.append(
        f"- **Fixed overhead dominates at low PPM**: At {min_ppm} PPM, even the best "
        f"combo ({fmt_label(best_label)}) costs {best_series[min_ppm]:.2f} bytes/point "
        f"vs {converged:.2f} converged — {best_series[min_ppm]/converged:.1f}× overhead "
        f"from per-metric headers."
    )

    # Raw-raw vs compressed convergence
    lines.append(
        f"- **Raw vs compressed convergence**: Raw + Raw overhead amortizes to "
        f"{rr_converged:.2f} BPP (16 bytes per point for 8-byte timestamp + 8-byte "
        f"float64). Compressed combos converge much lower because they also amortize "
        f"encoding metadata while compressing the data itself."
    )

    return '\n'.join(lines)


def gen_decision_tree(matrix):
    by_bpp = sorted(matrix, key=lambda x: x['bytes_per_point'])
    by_enc = sorted(matrix, key=lambda x: x['encode']['ns_per_op'])
    by_iter = sorted(matrix, key=lambda x: x['iter_seq']['ns_per_op'])

    fastest_enc = by_enc[0]
    fastest_iter = by_iter[0]

    # Best raw-ts combo (for random access)
    raw_ts = sorted(
        [r for r in matrix if r['label'].startswith(('raw-', 'shared-raw-'))],
        key=lambda x: x['bytes_per_point'],
    )
    best_raw = raw_ts[0]

    # Best non-shared
    non_shared = sorted(
        [r for r in matrix if not r['label'].startswith('shared-')],
        key=lambda x: x['bytes_per_point'],
    )
    best_ns = non_shared[0]

    best_comp = by_bpp[0]

    return f"""```
What is your priority?
├─ Smallest encoded size?
│  ├─ All metrics share timestamps? → {fmt_label(best_comp['label'])} ({best_comp['bytes_per_point']:.2f} BPP, {best_comp['space_savings_pct']:.1f}% savings)
│  └─ Independent timestamps?      → {fmt_label(best_ns['label'])} ({best_ns['bytes_per_point']:.2f} BPP, {best_ns['space_savings_pct']:.1f}% savings)
│
├─ Fastest encode?
│  └─ {fmt_label(fastest_enc['label'])} ({fastest_enc['encode']['ns_per_op']:,.0f} ns/op, {fastest_enc['bytes_per_point']:.2f} BPP)
│
├─ Fastest iteration / decode?
│  ├─ Sequential scan → {fmt_label(fastest_iter['label'])} ({fastest_iter['iter_seq']['ns_per_op']:,.0f} ns/op)
│  └─ Random access  → {fmt_label(best_raw['label'])} ({best_raw['bytes_per_point']:.2f} BPP, O(1) TimestampAt/ValueAt)
│
└─ Best balance (size + speed)?
   ├─ With shared TS → {fmt_label(by_bpp[0]['label'])} ({by_bpp[0]['bytes_per_point']:.2f} BPP, {by_bpp[0]['iter_seq']['ns_per_op']:,.0f} ns/op iter)
   └─ Without        → {fmt_label(best_ns['label'])} ({best_ns['bytes_per_point']:.2f} BPP, {best_ns['iter_seq']['ns_per_op']:,.0f} ns/op iter)
```"""


def gen_config_selection(matrix):
    by_bpp = sorted(matrix, key=lambda x: x['bytes_per_point'])
    by_enc = sorted(matrix, key=lambda x: x['encode']['ns_per_op'])
    by_iter = sorted(matrix, key=lambda x: x['iter_seq']['ns_per_op'])

    best = by_bpp[0]
    fastest_enc = by_enc[0]
    fastest_iter = by_iter[0]
    raw_raw = next(r for r in matrix if r['label'] == 'raw-raw')

    # Best raw-ts combo
    raw_ts = sorted(
        [r for r in matrix if r['label'].startswith(('raw-', 'shared-raw-'))],
        key=lambda x: x['bytes_per_point'],
    )
    best_raw = raw_ts[0]

    # Best balance: top 3 in both BPP and iteration
    top3_bpp = set(r['label'] for r in by_bpp[:5])
    top3_iter = set(r['label'] for r in by_iter[:5])
    balance_candidates = top3_bpp & top3_iter
    if balance_candidates:
        balance = next(r for r in by_bpp if r['label'] in balance_candidates)
    else:
        balance = by_bpp[1]

    return f"""| Use Case | Configuration | Key Metric | Rationale |
|----------|---------------|------------|-----------|
| **Best compression** | {fmt_label(best['label'])} | {best['bytes_per_point']:.2f} BPP ({best['space_savings_pct']:.1f}% savings) | Lowest bytes/point; shared timestamps eliminate redundant storage |
| **Fastest iteration** | {fmt_label(fastest_iter['label'])} | {fastest_iter['iter_seq']['ns_per_op']:,.0f} ns/op | Fastest sequential scan; raw values avoid decode overhead |
| **Fastest encode** | {fmt_label(fastest_enc['label'])} | {fastest_enc['encode']['ns_per_op']:,.0f} ns/op | Minimal encode computation; delta reduces buffer size |
| **Best balance** | {fmt_label(balance['label'])} | {balance['bytes_per_point']:.2f} BPP, {balance['iter_seq']['ns_per_op']:,.0f} ns/op iter | Top ranks in both compression and iteration speed |
| **Random access** | {fmt_label(best_raw['label'])} | {best_raw['bytes_per_point']:.2f} BPP | O(1) `TimestampAt`/`ValueAt`; raw timestamps support direct indexing |
| **Maximum throughput** | Raw + Raw | {raw_raw['encode']['ns_per_op']:,.0f} ns/op encode | Baseline; no encoding overhead but largest output |"""


def gen_ppm_guidelines(scaling, matrix):
    # Use best-compression combo's scaling series
    by_bpp = sorted(matrix, key=lambda x: x['bytes_per_point'])
    best_label = by_bpp[0]['label']
    best_scaling = next(s for s in scaling if s['label'] == best_label)
    series = sorted(best_scaling['points_series'], key=lambda x: x['points_per_metric'])

    # Converged = highest PPM
    converged = series[-1]['bytes_per_point']

    # Build zones
    zones = []
    for p in series:
        ratio = p['bytes_per_point'] / converged
        if ratio > 2.0:
            zone = "Poor"
        elif ratio > 1.3:
            zone = "Moderate"
        elif ratio > 1.05:
            zone = "Good"
        else:
            zone = "Optimal"
        zones.append((p['points_per_metric'], p['bytes_per_point'], ratio, zone))

    # Group into zone ranges
    zone_ranges = []
    current_zone = None
    for ppm, bpp, ratio, zone in zones:
        if zone != current_zone:
            zone_ranges.append({
                'zone': zone, 'start_ppm': ppm, 'end_ppm': ppm,
                'start_bpp': bpp, 'end_bpp': bpp,
            })
            current_zone = zone
        else:
            zone_ranges[-1]['end_ppm'] = ppm
            zone_ranges[-1]['end_bpp'] = bpp

    lines = [
        f"Using {fmt_label(best_label)} scaling data (converged: {converged:.2f} bytes/point):",
        "",
        "| Zone | PPM Range | BPP Range | Overhead | Recommendation |",
        "|------|-----------|-----------|----------|----------------|",
    ]

    recs = {
        'Poor': 'Batch more points if possible; fixed overhead dominates',
        'Moderate': 'Acceptable for low-frequency metrics',
        'Good': 'Good efficiency; recommended minimum for most use cases',
        'Optimal': 'Excellent efficiency; diminishing returns beyond this range',
    }

    for zr in zone_ranges:
        overhead_start = (zr['start_bpp'] / converged - 1) * 100
        overhead_end = (zr['end_bpp'] / converged - 1) * 100
        if zr['start_ppm'] == zr['end_ppm']:
            ppm_str = f"{zr['start_ppm']}"
        else:
            ppm_str = f"{zr['start_ppm']}–{zr['end_ppm']}"

        if zr['start_bpp'] == zr['end_bpp']:
            bpp_str = f"{zr['start_bpp']:.2f}"
        else:
            bpp_str = f"{zr['start_bpp']:.2f}–{zr['end_bpp']:.2f}"

        lines.append(
            f"| **{zr['zone']}** | {ppm_str} | {bpp_str} "
            f"| {overhead_end:.0f}–{overhead_start:.0f}% | {recs[zr['zone']]} |"
        )

    return '\n'.join(lines)


def main():
    if len(sys.argv) != 4:
        print(f"Usage: {sys.argv[0]} <benchmark_json> <template_md> <output_md>", file=sys.stderr)
        sys.exit(2)

    json_path = sys.argv[1]
    template_path = sys.argv[2]
    output_path = sys.argv[3]

    with open(json_path) as f:
        data = json.load(f)

    with open(template_path) as f:
        template = f.read()

    meta = data['metadata']
    matrix = data['matrix']
    scaling = data['scaling']

    replacements = {
        '{{BENCHMARK_METADATA}}': gen_benchmark_metadata(meta),
        '{{BENCHMARK_METADATA_DETAIL}}': gen_benchmark_metadata_detail(meta),
        '{{QUICK_REFERENCE}}': gen_quick_reference(matrix),
        '{{ENCODING_MATRIX}}': gen_encoding_matrix(matrix),
        '{{ENCODING_OBSERVATIONS}}': gen_encoding_observations(matrix),
        '{{ENCODE_PERFORMANCE}}': gen_perf_table(matrix, 'encode'),
        '{{DECODE_PERFORMANCE}}': gen_perf_table(matrix, 'decode'),
        '{{ITERATION_PERFORMANCE}}': gen_perf_table(matrix, 'iter_seq'),
        '{{SCALING_TABLE_STANDARD}}': gen_scaling_table(scaling, 'standard'),
        '{{SCALING_TABLE_SHARED}}': gen_scaling_table(scaling, 'shared-'),
        '{{SCALING_INSIGHTS}}': gen_scaling_insights(scaling, matrix),
        '{{DECISION_TREE}}': gen_decision_tree(matrix),
        '{{CONFIGURATION_SELECTION}}': gen_config_selection(matrix),
        '{{PPM_GUIDELINES}}': gen_ppm_guidelines(scaling, matrix),
    }

    output = template
    for placeholder, content in replacements.items():
        if placeholder not in output:
            print(f"WARNING: placeholder {placeholder} not found in template!", file=sys.stderr)
        output = output.replace(placeholder, content)

    # Verify no placeholders remain
    remaining = re.findall(r'\{\{[A-Z_]+\}\}', output)
    if remaining:
        print(f"ERROR: Unfilled placeholders: {remaining}", file=sys.stderr)
        sys.exit(1)

    with open(output_path, 'w') as f:
        f.write(output)

    print(f"Generated {output_path} successfully")
    print(f"Matrix entries: {len(matrix)}")
    print(f"Scaling entries: {len(scaling)}")

    # Quick sanity checks
    by_bpp = sorted(matrix, key=lambda x: x['bytes_per_point'])
    print(f"Best BPP: {by_bpp[0]['label']} = {by_bpp[0]['bytes_per_point']:.2f}")
    print(f"Worst BPP: {by_bpp[-1]['label']} = {by_bpp[-1]['bytes_per_point']:.2f}")


if __name__ == '__main__':
    main()
