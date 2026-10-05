#!/usr/bin/env python3
"""Layout-averaged measurev2 run: the implementation behind layouts.sh.

Usage:
    layouts.sh -o OUTDIR [-cpu 6] [-benchtime 50ms] [-cells report] [-rounds 4|2] [--build-only]

Steps (docs/specs/measurev2-fast-report-runs-design.md, "Layout averaging"):
1. provenance: git HEAD, git status, a SHA-256 over the Go sources (source) and over the report tools (tools);
2. stage exactly the files `git ls-files -co --exclude-standard` reports (minus tmp/) and re-hash them;
3. build four binaries under the build contract, padding the package of the lowest-addressed repository symbol
   with K = 0, 1, 2 and 4 mixing steps;
4. movement check: every repository function must take both 32-byte residues mod 64 across the layouts;
5. run the schedule pinned to one verified CPU with GOMAXPROCS=1 (--build-only stops before this step);
6. merge with merge_layouts.py into OUTDIR/merged/.
Any failure exits non-zero and leaves no OUTDIR/merged/.
"""
import argparse
import hashlib
import json
import os
import re
import secrets
import shutil
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
SCRIPTS = os.path.join(REPO, '.agents', 'skills', 'update-performance-report', 'scripts')
sys.path.insert(0, SCRIPTS)
import merge_layouts  # noqa: E402
from report_schema import LAYOUTS, schedule  # noqa: E402

MODULE = 'github.com/arloliu/mebo'
SOURCE_PATTERN = re.compile(r'(\.go|\.s|(^|/)go\.mod|(^|/)go\.sum)$')
# The report tools whose hashes form `tools`, relative to the repository root.
TOOL_FILES = [
    'tests/measurev2/layouts.sh',
    'tests/measurev2/layouts.py',
    'tests/measurev2/manifest.go',
    'tests/measurev2/acceptance_thresholds.json',
    '.agents/skills/update-performance-report/scripts/report_schema.py',
    '.agents/skills/update-performance-report/scripts/merge_layouts.py',
    '.agents/skills/update-performance-report/scripts/generate_report.py',
    '.agents/skills/update-performance-report/scripts/check_report_tools.py',
]
BUILD_CMD = ['env', 'GOFLAGS=', 'CGO_ENABLED=0', 'go', 'build', '-trimpath', '-pgo=off']
PAD_FILE = 'aa_pad.go'


class LayoutError(Exception):
    """A layouts.sh step failed; the run stops and nothing is merged."""


class Runner:
    """Runs external commands; check_report_tools.py replaces it with a stub."""

    def run(self, cmd, cwd=None, capture=True):
        proc = subprocess.run(cmd, cwd=cwd, capture_output=capture, text=True, check=False)
        if proc.returncode != 0:
            detail = (proc.stderr or '').strip()[-2000:] if capture else ''
            raise LayoutError(f'{" ".join(cmd)} exited {proc.returncode}: {detail}')
        return proc.stdout if capture else ''


class Log:
    """Stage start and end times, printed and kept for provenance.json."""

    def __init__(self):
        self.stages = []

    def stage(self, name):
        log = self

        class _Stage:
            def __enter__(self):
                self.start = time.time()
                print(f'layouts: {name}...', file=sys.stderr, flush=True)
                return self

            def __exit__(self, *exc):
                end = time.time()
                log.stages.append({'stage': name, 'start': self.start, 'end': end, 'ok': exc[0] is None})
                print(f'layouts: {name} {"done" if exc[0] is None else "FAILED"} ({end - self.start:.1f} s)',
                      file=sys.stderr, flush=True)
                return False

        return _Stage()


# ---------------------------------------------------------------- arguments

def parse_args(argv):
    ap = argparse.ArgumentParser(prog='layouts.sh', description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('-o', dest='outdir', required=True, help='output directory; must not exist or be empty')
    ap.add_argument('-cpu', default='6', help='one online logical CPU to pin every invocation to')
    ap.add_argument('-benchtime', default='50ms')
    ap.add_argument('-cells', default='report', choices=['full', 'report', 'wide'])
    ap.add_argument('-rounds', type=int, default=4)
    ap.add_argument('--build-only', action='store_true', help='stop after the movement check (validate.sh gate 5)')
    args = ap.parse_args(argv)
    if args.rounds not in (2, 4):
        raise LayoutError(f'-rounds {args.rounds}: want 4 or 2')
    if not re.fullmatch(r'\d+', args.cpu):
        raise LayoutError(f'-cpu {args.cpu!r}: want one non-negative CPU number, not a list or range')
    return args


def online_cpus(path='/sys/devices/system/cpu/online'):
    with open(path) as f:
        spec = f.read().strip()
    cpus = set()
    for part in spec.split(','):
        lo, _, hi = part.partition('-')
        cpus.update(range(int(lo), int(hi or lo) + 1))
    return cpus


def check_cpu(cpu, runner, online=None):
    """The CPU must be online, and a probe under the same taskset must see exactly that CPU in its affinity mask."""
    online = online_cpus() if online is None else online
    if int(cpu) not in online:
        raise LayoutError(f'-cpu {cpu}: not an online CPU')
    status = runner.run(['taskset', '-c', cpu, 'cat', '/proc/self/status'])
    m = re.search(r'^Cpus_allowed_list:\s*(\S+)\s*$', status, re.M)
    if not m or m.group(1) != cpu:
        raise LayoutError(f'probe affinity {m.group(1) if m else "unknown"}, want exactly CPU {cpu}')
    return int(cpu)


def prepare_outdir(outdir):
    if os.path.exists(outdir):
        if not os.path.isdir(outdir) or os.listdir(outdir):
            raise LayoutError(f'{outdir}: exists and is not an empty directory')
    else:
        os.makedirs(outdir)


# ---------------------------------------------------------------- provenance and staging

def listed_files(repo, runner):
    """Files git reports as tracked or untracked-but-not-ignored, minus tmp/."""
    out = runner.run(['git', '-C', repo, 'ls-files', '-co', '--exclude-standard', '-z'])
    return sorted(p for p in out.split('\0') if p and not p.startswith('tmp/'))


def tree_hash(root, paths):
    """SHA-256 over the sorted list and contents of the Go source files among paths (regular files only)."""
    h = hashlib.sha256()
    for p in sorted(paths):
        full = os.path.join(root, p)
        if SOURCE_PATTERN.search(p) and os.path.isfile(full) and not os.path.islink(full):
            with open(full, 'rb') as f:
                data = f.read()
            h.update(f'{p}\0{len(data)}\0'.encode())
            h.update(data)
    return h.hexdigest()


def provenance(repo, runner):
    """(source string, tools string, details) of the working tree."""
    head = runner.run(['git', '-C', repo, 'rev-parse', 'HEAD']).strip()
    status = runner.run(['git', '-C', repo, 'status', '--porcelain'])
    files = listed_files(repo, runner)
    tree = tree_hash(repo, files)
    status_hash = hashlib.sha256(status.encode()).hexdigest()
    tools = {}
    for rel in TOOL_FILES:
        with open(os.path.join(repo, rel), 'rb') as f:
            tools[rel] = hashlib.sha256(f.read()).hexdigest()
    tools_hash = hashlib.sha256(''.join(f'{k} {v}\n' for k, v in sorted(tools.items())).encode()).hexdigest()
    source = f'head={head} status={status_hash[:16]} tree={tree}'
    details = {'head': head, 'status': status, 'tree_sha256': tree, 'tools': tools, 'files': files}
    return source, f'sha256={tools_hash}', details


def stage(repo, dest, files, want_tree):
    """Copy the listed regular files into dest; non-regular entries (sockets, device nodes, links) are skipped.
    The staged tree must hash to want_tree."""
    skipped = []
    for p in files:
        src = os.path.join(repo, p)
        if not os.path.isfile(src) or os.path.islink(src):
            skipped.append(p)
            continue
        os.makedirs(os.path.join(dest, os.path.dirname(p)), exist_ok=True)
        shutil.copy2(src, os.path.join(dest, p))
    got = tree_hash(dest, files)
    if got != want_tree:
        raise LayoutError(f'staged tree hash {got} differs from the source hash {want_tree}')
    return skipped


# ---------------------------------------------------------------- build

def pad_source(package, k):
    """The padding file: the package variable first, then a non-inlinable function of k mixing steps."""
    steps = ''.join(f'\tx = (x^(x>>7))*31 + {i}\n' for i in range(1, k + 1))
    return (f'package {package}\n\nimport "os"\n\n'
            '// layoutPadSink keeps layoutPad, which only exists to move the code after it, in the binary.\n'
            'var layoutPadSink = layoutPad(uint64(len(os.Args)))\n\n'
            '// layoutPad is padding generated by layouts.sh; it shifts the package\'s code by its size.\n'
            f'//go:noinline\nfunc layoutPad(x uint64) uint64 {{\n{steps}\treturn x\n}}\n')


def check_build_settings(version_m, where):
    """go version -m must show the build contract: -trimpath=true, CGO_ENABLED=0, -buildmode=exe,
    -pgo absent or off, and none of -race, -cover, -asan, -msan."""
    settings = {}
    for line in version_m.splitlines():
        parts = line.split('\t')
        if len(parts) >= 3 and parts[1] == 'build':
            key, _, value = parts[2].partition('=')
            settings[key] = value
    problems = []
    if settings.get('-trimpath') != 'true':
        problems.append('-trimpath is not true')
    if settings.get('CGO_ENABLED') != '0':
        problems.append('CGO_ENABLED is not 0')
    if settings.get('-buildmode') != 'exe':
        problems.append('-buildmode is not exe')
    if settings.get('-pgo', 'off') != 'off':
        problems.append(f'-pgo={settings["-pgo"]}')
    for flag in ('-race', '-cover', '-asan', '-msan'):
        if flag in settings:
            problems.append(f'{flag} set')
    if problems:
        raise LayoutError(f'{where}: build contract violated: {", ".join(problems)}')
    return settings


def parse_nm(text):
    """Text symbols of `go tool nm -n -size`: {name: [(address, size), ...]}."""
    syms = {}
    for line in text.splitlines():
        parts = line.split()
        if len(parts) >= 4 and parts[2] in ('T', 't'):
            syms.setdefault(' '.join(parts[3:]), []).append((int(parts[0], 16), int(parts[1])))
    return syms


def is_repo_symbol(name):
    return name.startswith(MODULE + '/') or name.startswith('main.')


def symbol_package(name):
    """Import path of a repository symbol: everything before the first '.' after the last '/'
    that precedes any receiver or type-argument bracket."""
    if name.startswith('main.'):
        return 'main'
    cut = min([i for i in (name.find('('), name.find('[')) if i >= 0] or [len(name)])
    slash = name.rfind('/', 0, cut)
    return name[:name.find('.', slash)]


def lowest_repo_package(nm_text):
    """The package of the lowest-addressed repository text symbol."""
    best = None
    for name, entries in parse_nm(nm_text).items():
        if is_repo_symbol(name):
            for addr, _ in entries:
                if best is None or addr < best[0]:
                    best = (addr, name)
    if best is None:
        raise LayoutError('no repository symbol in the layout-0 binary')
    return symbol_package(best[1]), best[1]


def movement_check(tables, pad_package):
    """Every repository text symbol must exist in every layout with one size and take both residues mod 64.
    The padding package's layoutPad and init, which the padding creates or extends, are exempt and reported."""
    exempt = {f'{pad_package}.layoutPad', f'{pad_package}.init'}
    names = set()
    for t in tables.values():
        names.update(n for n in t if is_repo_symbol(n))
    problems, checked, exempted = [], 0, {}
    for name in sorted(names):
        if name in exempt:
            exempted[name] = {str(k): tables[k].get(name, []) for k in tables}
            continue
        entries = {k: tables[k].get(name) for k in tables}
        if any(e is None for e in entries.values()):
            problems.append(f'{name}: missing from layouts {[k for k, e in entries.items() if e is None]}')
            continue
        if len({tuple(s for _, s in e) for e in entries.values()}) != 1:
            problems.append(f'{name}: size differs across layouts')
            continue
        for i in range(len(entries[min(entries)])):
            residues = {e[i][0] % 64 for e in entries.values()}
            if len(residues) < 2:
                problems.append(f'{name}: address mod 64 is {residues.pop()} in every layout')
        checked += 1
    if problems:
        raise LayoutError(f'movement check failed for {len(problems)} symbols: ' + '; '.join(problems[:20]))
    return {'checked': checked, 'exempt': exempted}


def build_one(pkgdir, bindir, layoutdir, k, runner):
    """Build one layout's binary; keep its go version -m and symbol table; return (sha256, nm text)."""
    out = os.path.join(bindir, f'measurev2_{k}')
    runner.run(BUILD_CMD + ['-o', out, '.'], cwd=pkgdir)
    version_m = runner.run(['go', 'version', '-m', out])
    check_build_settings(version_m, f'measurev2_{k}')
    nm_text = runner.run(['go', 'tool', 'nm', '-n', '-size', out])
    for name, text in (('nm', nm_text), ('version', version_m)):
        with open(os.path.join(layoutdir, f'{name}_{k}.txt'), 'w') as f:
            f.write(text)
    with open(out, 'rb') as f:
        return hashlib.sha256(f.read()).hexdigest(), nm_text


def locate_pad_package(nm_text, tree, pkgdir, runner):
    """(import path, directory, package name) of the lowest-addressed repository symbol's package."""
    pkg, lowest = lowest_repo_package(nm_text)
    listing = runner.run(['go', 'list', '-f', '{{.Dir}}\t{{.Name}}', '.' if pkg == 'main' else pkg], cwd=pkgdir)
    pad_dir, _, pad_name = listing.strip().partition('\t')
    if not os.path.realpath(pad_dir).startswith(os.path.realpath(tree) + os.sep):
        raise LayoutError(f'padding package {pkg} resolves outside the staged tree: {pad_dir}')
    print(f'layouts: padding {pkg} (lowest symbol {lowest})', file=sys.stderr)
    return pkg, pad_dir, pad_name


def build_layouts(tree, bindir, layoutdir, runner):
    """Build measurev2 in the four layouts and check that the padding moved every repository function."""
    pkgdir = os.path.join(tree, 'tests', 'measurev2')
    binaries, tables = {}, {}
    binaries[0], nm0 = build_one(pkgdir, bindir, layoutdir, 0, runner)
    tables[0] = parse_nm(nm0)
    pad_pkg, pad_dir, pad_name = locate_pad_package(nm0, tree, pkgdir, runner)
    pad_path = os.path.join(pad_dir, PAD_FILE)
    try:
        for k in LAYOUTS[1:]:
            with open(pad_path, 'w') as f:
                f.write(pad_source(pad_name, k))
            binaries[k], nm_text = build_one(pkgdir, bindir, layoutdir, k, runner)
            tables[k] = parse_nm(nm_text)
    finally:
        if os.path.exists(pad_path):
            os.remove(pad_path)
    movement = movement_check(tables, pad_pkg)
    movement['pad_package'] = pad_pkg
    with open(os.path.join(layoutdir, 'movement.json'), 'w') as f:
        json.dump(movement, f, indent=2)
    return binaries, movement


# ---------------------------------------------------------------- schedule

def peak_rss_kb(time_log):
    m = re.search(r'Maximum resident set size \(kbytes\):\s*(\d+)', time_log)
    if not m:
        raise LayoutError('no peak RSS in /usr/bin/time -v output')
    return int(m.group(1))


def invocation_cmd(binary, cpu, args, ctx, rnd, layout, order, outdir, time_log):
    return [
        '/usr/bin/time', '-v', '-o', time_log,
        'taskset', '-c', str(cpu), 'env', '-u', 'GOMEMLIMIT', 'GOMAXPROCS=1', 'GOGC=100', 'GODEBUG=',
        binary, '-profiles', 'report', '-cells', args.cells, '-benchtime', args.benchtime, '-order', order,
        '-rounds', str(args.rounds), '-run-id', ctx['run_id'], '-source', ctx['source'], '-tools', ctx['tools'],
        '-layout', str(layout), '-round', str(rnd), '-outdir', outdir,
    ]


def run_schedule(args, ctx, cpu, bindir, binaries, runner, log):
    raw = os.path.join(args.outdir, 'raw')
    logs = os.path.join(args.outdir, 'logs')
    os.makedirs(raw)
    os.makedirs(logs)
    invocations = []
    for rnd, layout, order in schedule(args.rounds):
        name = f'r{rnd}_L{layout}'
        target = os.path.join(raw, name)
        if os.path.exists(target):
            raise LayoutError(f'{target} already exists')
        time_log = os.path.join(logs, f'{name}.time')
        with log.stage(f'invoke {name} ({order})'):
            runner.run(invocation_cmd(os.path.join(bindir, f'measurev2_{layout}'), cpu, args, ctx, rnd, layout, order,
                                      target, time_log), capture=False)
        with open(time_log) as f:
            rss = peak_rss_kb(f.read())
        with open(os.path.join(target, 'wrapper.json'), 'w') as f:
            json.dump({'binary_sha256': binaries[layout], 'peak_rss_kb': rss, 'cpu': cpu}, f)
        invocations.append({'round': rnd, 'layout': layout, 'order': order, 'peak_rss_kb': rss})
    return invocations


# ---------------------------------------------------------------- driver

def write_provenance(outdir, record):
    with open(os.path.join(outdir, 'provenance.json'), 'w') as f:
        json.dump(record, f, indent=2, default=str)


def run(argv, runner=None, repo=REPO, online=None):
    """Run every step; any failure, even after OUTDIR/merged/ was published, removes it.
    provenance.json records every stage, publication included, which gate 6 times."""
    runner = runner or Runner()
    log = Log()
    args = parse_args(argv)
    args.outdir = os.path.abspath(args.outdir)
    prepare_outdir(args.outdir)
    record = {'args': vars(args), 'stages': log.stages, 'published': False}
    written = False
    try:
        with log.stage('affinity probe'):
            cpu = check_cpu(args.cpu, runner, online)
        with log.stage('provenance'):
            source, tools, details = provenance(repo, runner)
        run_id = secrets.token_hex(8)
        ctx = {'run_id': run_id, 'source': source, 'tools': tools}
        record.update(ctx, cpu=cpu, provenance=details)
        work = os.path.join(os.environ.get('TMPDIR', '/tmp'), 'measurev2-layouts', run_id)
        tree, bindir = os.path.join(work, 'tree'), os.path.join(args.outdir, 'bin')
        layoutdir = os.path.join(args.outdir, 'layouts')
        os.makedirs(tree)
        os.makedirs(bindir)
        os.makedirs(layoutdir)
        with log.stage('stage'):
            record['skipped'] = stage(repo, tree, details['files'], details['tree_sha256'])
        with log.stage('build'):
            binaries, movement = build_layouts(tree, bindir, layoutdir, runner)
        record.update(binaries={str(k): v for k, v in binaries.items()}, movement=movement['checked'])
        if args.build_only:
            return 0
        with log.stage('schedule'):
            record['invocations'] = run_schedule(args, ctx, cpu, bindir, binaries, runner, log)
        with log.stage('merge'):
            merged = merge_layouts.merge(os.path.join(args.outdir, 'raw'))
        print(f'layouts: publishing {len(merged)} files to {os.path.join(args.outdir, "merged")}', file=sys.stderr)
        with log.stage('publish'):
            merge_layouts.publish(args.outdir, merged)
        record['published'] = True
        write_provenance(args.outdir, record)
        written = True
        return 0
    except BaseException:
        # OUTDIR started empty, so a merged/ here is this run's and must not outlive its failure.
        shutil.rmtree(os.path.join(args.outdir, 'merged'), ignore_errors=True)
        record['published'] = False
        written = False
        raise
    finally:
        if not written:
            write_provenance(args.outdir, record)


def main(argv=None):
    try:
        return run(sys.argv[1:] if argv is None else argv)
    except (LayoutError, merge_layouts.SchemaError, OSError) as exc:
        print(f'layouts: {exc}', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
