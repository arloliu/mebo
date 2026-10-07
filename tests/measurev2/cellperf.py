#!/usr/bin/env python3
"""cellperf.py: per-cell hardware counters and machine state beside a measurev2 invocation, and the analysis of both.

validate.sh runs every pinned invocation under `perf stat -I 100` (100 ms buckets of a few hardware events),
samples the machine every 100 ms with `monitor`, and stamps the invocation's -verbose progress lines with `stamp`,
so that a slow cell can be explained after the fact without rerunning anything
(docs/specs/index-entry-by-pointer-design.md, "Validation without the exemptions").

Subcommands:
  events [--cpu N]        print the perf events to count, one argument for `perf stat -e`, or nothing when perf cannot count
  others [--cpu N]        print the CPU list observers may run on (every CPU but N and its SMT siblings), for taskset -c
  monitor OUT.csv --cpu N sample the machine every 100 ms until killed (busy shares, clock, Tctl, interrupts, sibling idle states)
  stamp                   copy stdin to stdout with the wall time in front of every line
  analyze DIR...          per invocation DIR (its JSON) with DIR.perf.csv, DIR.stderr.log and DIR.times.txt beside it:
                          every cell of the main data set, flagged when its throughput dips mid-cell or a counter shows
                          contention; flagged cells get their 100 ms rows (--all prints every cell's rows)

The counters tell the causes apart (one 100 ms row against its neighbours):
  smt_contention up, the sibling CPU busy                 another process on the SMT sibling
  task-clock under 100 ms, context switches up            another process on the pinned CPU
  cycles per task-clock down, instructions per cycle flat a clock change
  decoder-sourced ops up 50-100x, everything else flat    the core's op-cache fetch episode (the loop runs ~1/3 slower)
  branch misses per instruction up                        predictor state
"""
import argparse
import csv
import glob
import json
import os
import re
import statistics
import subprocess
import sys
import time

GENERIC_EVENTS = ['cycles:u', 'instructions:u', 'branches:u', 'branch-misses:u']
# Zen 5 names; five hardware events because the NMI watchdog holds one of the six core counters.
ZEN_EVENTS = ['cycles:u', 'instructions:u', 'branch-misses:u', 'de_src_op_disp.x86_decoder',
              'de_no_dispatch_per_slot.smt_contention']
SOFTWARE_EVENTS = ['task-clock', 'context-switches']
OPS = ['encode', 'decode', 'iter_seq', 'random_value_at', 'random_timestamp_at']
SHORT = {'encode': 'enc', 'decode': 'dec', 'iter_seq': 'iter', 'random_value_at': 'val', 'random_timestamp_at': 'ts'}
MAIN_PROFILE = 'mix_monitoring'


# ---------------------------------------------------------------- events

def perf_counts(events):
    """True when `perf stat` can count these events on a trivial command."""
    try:
        r = subprocess.run(['perf', 'stat', '-x,', '-e', ','.join(events), '--', 'true'],
                           capture_output=True, text=True, timeout=20)
    except (OSError, subprocess.TimeoutExpired):
        return False
    if r.returncode != 0:
        return False
    return not any(tag in r.stderr for tag in ('<not supported>', '<not counted>'))


def events_arg():
    """The event list to count: the Zen set, the generic set, or '' when perf cannot count at all."""
    for hw in (ZEN_EVENTS, GENERIC_EVENTS):
        if perf_counts(hw + SOFTWARE_EVENTS):
            return ','.join(hw + SOFTWARE_EVENTS)
    return ''


# ---------------------------------------------------------------- monitor

def sibling_of(cpu):
    try:
        with open(f'/sys/devices/system/cpu/cpu{cpu}/topology/thread_siblings_list') as f:
            cpus = []
            for part in f.read().strip().split(','):
                a, _, b = part.partition('-')
                cpus.extend(range(int(a), int(b or a) + 1))
    except OSError:
        return None
    others = [c for c in cpus if c != cpu]
    return others[0] if others else None


def observer_cpus(cpu):
    """The CPUs an observer may run on: every CPU this process may use except the measured one and its SMT siblings,
    as a taskset list (for example '0-5,7-21,23-31'); '' when nothing else is available."""
    try:
        allowed = set(os.sched_getaffinity(0))
    except (OSError, AttributeError):
        return ''
    sib = sibling_of(cpu)
    allowed -= {cpu} | ({sib} if sib is not None else set())
    if not allowed:
        return ''
    cpus, ranges = sorted(allowed), []
    start = prev = cpus[0]
    for c in cpus[1:] + [None]:
        if c is not None and c == prev + 1:
            prev = c
            continue
        ranges.append(f'{start}-{prev}' if prev > start else f'{start}')
        if c is not None:
            start = prev = c
    return ','.join(ranges)


def read_stat():
    out = {}
    with open('/proc/stat') as f:
        for line in f:
            if line.startswith('cpu') and line[3] != ' ':
                parts = line.split()
                v = list(map(int, parts[1:]))
                out[parts[0]] = (sum(v), v[3] + v[4])
    return out


IRQ_ROWS = ['LOC', 'RES', 'CAL', 'TLB', 'NMI']


def read_irqs(cpu):
    """Per-row counts of the CPU's column of /proc/interrupts: the kernel rows above, and 'DEV' for every device IRQ."""
    out = {k: 0 for k in IRQ_ROWS + ['DEV']}
    try:
        with open('/proc/interrupts') as f:
            lines = f.read().splitlines()
    except OSError:
        return out
    try:
        col = lines[0].split().index(f'CPU{cpu}') + 1
    except (ValueError, IndexError):
        return out
    for line in lines[1:]:
        parts = line.split()
        if len(parts) <= col:
            continue
        key = parts[0].rstrip(':')
        try:
            n = int(parts[col])
        except ValueError:
            continue
        if key in out:
            out[key] += n
        elif key.isdigit():
            out['DEV'] += n
    return out


def read_file(path):
    try:
        with open(path) as f:
            return f.read().strip()
    except OSError:
        return ''


def idle_states(cpu):
    return sorted(glob.glob(f'/sys/devices/system/cpu/cpu{cpu}/cpuidle/state*')) if cpu is not None else []


def read_idle(states):
    return [(int(read_file(f'{d}/time') or 0), int(read_file(f'{d}/usage') or 0)) for d in states]


def tctl_path():
    for h in glob.glob('/sys/class/hwmon/hwmon*'):
        if read_file(f'{h}/name') == 'k10temp':
            return f'{h}/temp1_input'
    return ''


def monitor(out_path, cpu, interval=0.1):
    sib = sibling_of(cpu)
    # Never sample from the benchmark's core or its sibling: a wakeup there costs the timed loop.
    try:
        others = os.sched_getaffinity(0) - {cpu} - ({sib} if sib is not None else set())
        if others:
            os.sched_setaffinity(0, others)
    except (OSError, AttributeError):
        pass
    states = idle_states(sib)
    tctl = tctl_path()
    me, sibname = f'cpu{cpu}', f'cpu{sib}' if sib is not None else ''
    prev, pirq, pidle = read_stat(), read_irqs(cpu), read_idle(states)
    with open(out_path, 'w') as w:
        cols = ['t', 'cpu', 'sibling', 'other_max', 'other_cpu', 'cur_khz', 'avg_khz', 'tctl']
        cols += ['d' + k for k in IRQ_ROWS + ['DEV']]
        cols += [f'sib_{os.path.basename(read_file(d + "/name") or d)}_{m}' for d in states for m in ('us', 'n')]
        w.write(','.join(cols) + '\n')
        while True:
            time.sleep(interval)
            cur, cirq, cidle, t = read_stat(), read_irqs(cpu), read_idle(states), time.time()
            busy = {}
            for c in cur:
                dt = cur[c][0] - prev[c][0]
                di = cur[c][1] - prev[c][1]
                busy[c] = (1 - di / dt) if dt else 0.0
            others = {c: b for c, b in busy.items() if c not in (me, sibname)}
            oc = max(others, key=others.get) if others else ''
            row = [f'{t:.3f}', f'{busy.get(me, 0):.2f}', f'{busy.get(sibname, 0):.2f}', f'{others.get(oc, 0):.2f}', oc,
                   read_file(f'/sys/devices/system/cpu/cpu{cpu}/cpufreq/scaling_cur_freq'),
                   read_file(f'/sys/devices/system/cpu/cpu{cpu}/cpufreq/cpuinfo_avg_freq'),
                   read_file(tctl) if tctl else '']
            row += [str(cirq[k] - pirq[k]) for k in IRQ_ROWS + ['DEV']]
            row += [str(c[i] - p[i]) for c, p in zip(cidle, pidle) for i in (0, 1)]
            w.write(','.join(row) + '\n')
            w.flush()
            prev, pirq, pidle = cur, cirq, cidle


# ---------------------------------------------------------------- stamp

def stamp():
    for line in sys.stdin:
        sys.stdout.write(f'{time.time():.3f} {line}')
        sys.stdout.flush()


# ---------------------------------------------------------------- analyze

def load_perf(path, t0):
    buckets = {}
    with open(path) as f:
        for line in f:
            if line.startswith('#') or not line.strip():
                continue
            parts = line.split(',')
            try:
                buckets.setdefault(float(parts[0]), {})[parts[3]] = float(parts[1])
            except (ValueError, IndexError):
                continue
    return [(t0 + t, b) for t, b in sorted(buckets.items())]


def load_monitor(path):
    if not os.path.exists(path):
        return []
    with open(path) as f:
        rows = list(csv.DictReader(f))
    for r in rows:
        r['t'] = float(r['t'])
    return rows


def main_rows(invdir):
    """{combo label: row} of the invocation's main data set, from main.json or profiles/matrix_<main>.json."""
    for cand in (os.path.join(invdir, 'main.json'), os.path.join(invdir, 'profiles', f'matrix_{MAIN_PROFILE}.json')):
        if os.path.exists(cand):
            with open(cand) as f:
                doc = json.load(f)
            return {r['label']: r for r in doc['matrix']}, doc['invocation']['order']
    raise FileNotFoundError(f'{invdir}: no main.json or profiles/matrix_{MAIN_PROFILE}.json')


def progress_marks(stderr_log):
    """(wall time, kind, name) of every stamped progress line: ('cell', cell id) for the harness's per-cell lines,
    ('combo', label) for the main data set's combo lines, ('other', '') for the rest."""
    marks, section = [], None
    with open(stderr_log) as f:
        for line in f:
            m = re.match(r'(\d+\.\d+)\s+(.*)', line.rstrip())
            if not m or not m.group(2):
                continue
            t, txt = float(m.group(1)), m.group(2)
            g = re.match(r'Generating test data \(profile (\S+)\)', txt)
            if g:
                section = g.group(1)
                marks.append((t, 'other', ''))
                continue
            k = re.match(r'cell (\S+)$', txt)
            if k:
                marks.append((t, 'cell', k.group(1)))
                continue
            c = re.match(r'\[(\d+)/\d+\] (\S+):', txt)
            marks.append((t, 'combo', c.group(2)) if c and section == MAIN_PROFILE else (t, 'other', ''))
    return marks


def cells_of(invdir, monitor_rows):
    """Every main-data-set cell of one invocation with its window, perf buckets and monitor rows."""
    rows, order = main_rows(invdir)
    with open(invdir + '.times.txt') as f:
        t0 = float(f.readline().split()[1])
    perf = load_perf(invdir + '.perf.csv', t0)
    marks = progress_marks(invdir + '.stderr.log')
    windows = []  # (label, op, w0, w1)
    if any(kind == 'cell' for _, kind, _ in marks):
        # Exact: each cell runs from its own line to the next line of any kind.
        for k, (t, kind, name) in enumerate(marks):
            if kind != 'cell' or not name.startswith(MAIN_PROFILE + '/'):
                continue
            _, lab, op = name.split('/')
            t_next = marks[k + 1][0] if k + 1 < len(marks) else t + rows[lab][op]['t_ns'] / 1e9 + 0.2
            windows.append((lab, op, t + 0.02, t_next - 0.005))
    else:
        # Older stderr without cell lines: lay the cells out from the combo line in run order.
        for t, kind, lab in marks:
            if kind != 'combo':
                continue
            start = t + 0.02
            for op in (list(reversed(OPS)) if order == 'reverse' else OPS):
                T = rows[lab][op]['t_ns'] / 1e9
                windows.append((lab, op, start, start + T + 0.05))
                start += T + 0.08
    cells = []
    for lab, op, w0, w1 in windows:
        raw = rows[lab][op]
        if raw['t_ns'] / 1e9 < 0.5:
            continue  # cells shorter than a few buckets cannot be read this way
        cells.append({'inv': os.path.basename(invdir), 'label': lab, 'op': op, 'ns': raw['ns_per_op'], 'n': raw['n'],
                      'T': raw['t_ns'] / 1e9, 'w': (w0, w1), 'perf': [(bt, b) for bt, b in perf if w0 <= bt <= w1],
                      'mon': [r for r in monitor_rows if w0 <= r['t'] <= w1]})
    return cells


def bucket_metrics(b):
    ins, cyc, tc = b.get('instructions:u', 0), b.get('cycles:u', 0), b.get('task-clock', 0)
    return {
        'minst': ins / 1e6, 'ipc': ins / cyc if cyc else 0.0, 'mhz': cyc / tc * 1e3 if tc else 0.0,
        'mpki': 1e3 * b.get('branch-misses:u', 0) / ins if ins else 0.0,
        'mdec': b.get('de_src_op_disp.x86_decoder', 0) / 1e6,
        'msmt': b.get('de_no_dispatch_per_slot.smt_contention', 0) / 1e6,
        'tclk': tc / 1e6, 'cs': b.get('context-switches', 0),
    }


SMT_CONTENTION_PER_KINST = 20  # a process on the sibling measured about 100 in every bucket; an idle sibling's
SMT_CONTENTION_BUCKETS = 3     # kernel wakeups spike one bucket to 70-140, so contention is three consecutive buckets


def judge(cell, dip=0.85):
    """Flags of one cell: 'episode' (two or more consecutive inner buckets below dip x the median bucket's instructions),
    'sibling' (smt_contention above SMT_CONTENTION_PER_KINST in SMT_CONTENTION_BUCKETS consecutive buckets,
    or the sibling CPU busy),
    'cpu' (task-clock below 95 ms in an inner bucket)."""
    flags = []
    inner = cell['perf'][1:-1]
    if len(inner) >= 5:
        ins = [bucket_metrics(b)['minst'] for _, b in inner]
        med = statistics.median(ins)
        low = [med > 0 and v < dip * med for v in ins]
        if any(low[i] and low[i + 1] for i in range(len(low) - 1)):
            flags.append('episode')
        if any(bucket_metrics(b)['tclk'] < 95 for _, b in inner):
            flags.append('cpu')
        hot = [b.get('de_no_dispatch_per_slot.smt_contention', 0) > SMT_CONTENTION_PER_KINST * 1e-3 * b.get('instructions:u', 0)
               for _, b in inner]
        if any(all(hot[i:i + SMT_CONTENTION_BUCKETS]) for i in range(len(hot) - SMT_CONTENTION_BUCKETS + 1)):
            flags.append('sibling')
    if any(float(r.get('sibling', 0) or 0) >= 0.5 for r in cell['mon']):
        if 'sibling' not in flags:
            flags.append('sibling')
    return flags


def print_rows(cell):
    print(f"\n--- {cell['inv']} {cell['label']} {SHORT[cell['op']]} {cell['ns']:.0f} ns/op N={cell['n']} T={cell['T']:.2f}s")
    print(f"{'t-rel':>6s} {'Minst':>7s} {'IPC':>5s} {'MPKI':>6s} {'MHz':>5s} {'Mdec':>6s} {'Msmt':>5s} {'tclk':>6s} {'cs':>4s} | "
          f"{'cpu':>4s} {'sib':>4s} {'oth':>4s} {'dDEV':>5s} {'dRES':>5s}")
    mon = cell['mon']
    for bt, b in cell['perf']:
        m = bucket_metrics(b)
        near = min(mon, key=lambda r: abs(r['t'] - bt), default=None)
        s = ''
        if near is not None and abs(near['t'] - bt) <= 0.1:
            s = f"{float(near['cpu']):4.2f} {float(near['sibling'] or 0):4.2f} {float(near['other_max']):4.2f} {near['dDEV']:>5s} {near['dRES']:>5s}"
        print(f"{bt - cell['w'][0]:6.2f} {m['minst']:7.0f} {m['ipc']:5.2f} {m['mpki']:6.2f} {m['mhz']:5.0f} {m['mdec']:6.1f} "
              f"{m['msmt']:5.1f} {m['tclk']:6.1f} {m['cs']:4.0f} | {s}")


def analyze(dirs, show_all=False, dip=0.85):
    flagged_any = False
    for invdir in dirs:
        invdir = invdir.rstrip('/')
        monitor_rows = load_monitor(os.path.join(os.path.dirname(os.path.dirname(invdir)), 'monitor.csv'))
        try:
            cells = cells_of(invdir, monitor_rows)
        except (OSError, KeyError, ValueError) as exc:
            print(f'cellperf: {invdir}: {exc}')
            continue
        flagged = [(c, judge(c, dip)) for c in cells]
        flagged = [(c, f) for c, f in flagged if f]
        print(f'== cellperf: {invdir}: {len(cells)} main-data-set cells, {len(flagged)} flagged')
        for c, f in flagged:
            flagged_any = True
            print(f"  {c['label']}/{SHORT[c['op']]}: {c['ns']:.0f} ns/op, {', '.join(f)}")
        for c in cells:
            if show_all or judge(c, dip):
                print_rows(c)
    return flagged_any


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest='cmd', required=True)
    p = sub.add_parser('events')
    p.add_argument('--cpu', type=int, default=6)
    p = sub.add_parser('others')
    p.add_argument('--cpu', type=int, default=6)
    p = sub.add_parser('monitor')
    p.add_argument('out')
    p.add_argument('--cpu', type=int, default=6)
    sub.add_parser('stamp')
    p = sub.add_parser('analyze')
    p.add_argument('dirs', nargs='+')
    p.add_argument('--all', action='store_true')
    p.add_argument('--dip', type=float, default=0.85)
    args = ap.parse_args(argv)
    if args.cmd == 'events':
        print(events_arg())
    elif args.cmd == 'others':
        print(observer_cpus(args.cpu))
    elif args.cmd == 'monitor':
        try:
            monitor(args.out, args.cpu)
        except KeyboardInterrupt:
            pass
    elif args.cmd == 'stamp':
        stamp()
    else:
        analyze(args.dirs, show_all=args.all, dip=args.dip)
    return 0


if __name__ == '__main__':
    sys.exit(main())
