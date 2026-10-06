#!/usr/bin/env python3
"""Check the Gorilla and Chimp encoders' compiler output for an inlined, barrier-free bit-spill hot path.

Usage:
    check_encoder_hotpath.py check CODEC SOURCE M_OUTPUT S_OUTPUT
    check_encoder_hotpath.py selftest

check reads the -gcflags=-m and -gcflags=-S output of one encoder package (scripts/check-encoder-hotpath.sh produces them)
and requires that:
  - every e.appendBits call in SOURCE is reported as inlined, matched by line and column;
  - the assembly holds the bodies of appendBits, writeValue, Write and WriteSlice;
  - appendBits and writeValue call no gcWriteBarrier;
  - Write and WriteSlice call gcWriteBarrier only from ByteBuffer.Grow (byte_buffer_pool.go).
Only instruction records count; relocation records ("rel ... gcWriteBarrier") repeat them without a source position.
"""
import re
import sys

ENCODER_TYPES = {'gorilla': 'NumericGorillaEncoder', 'chimp': 'NumericChimpEncoder'}
REQUIRED_FUNCS = ('appendBits', 'writeValue', 'Write', 'WriteSlice')
NO_BARRIER_FUNCS = ('appendBits', 'writeValue')
GROW_ONLY_FUNCS = ('Write', 'WriteSlice')
GROW_FILE = 'byte_buffer_pool.go'

# A text symbol header: "<pkg>.(*Type).Method STEXT ...".
STEXT = re.compile(r'^(\S+) STEXT\b')
# An instruction record: "\t0x004b 00075 (<file>:<line>)\tCALL\truntime.gcWriteBarrier2(SB)"; <file> may hold spaces.
BARRIER = re.compile(r'^\s+0x[0-9a-f]+\s+\d+\s+\((.*):(\d+)\)\s+CALL\s+runtime\.gcWriteBarrier\w*\(SB\)')


def call_sites(source_lines):
    """(line, column) of the '(' of each e.appendBits call, 1-based, columns in bytes as the compiler reports them."""
    sites = set()
    for lineno, line in enumerate(source_lines, 1):
        if line.lstrip().startswith('//'):
            continue
        for m in re.finditer(r'\be\.appendBits\(', line):
            sites.add((lineno, len(line[:m.end() - 1].encode()) + 1))
    return sites


def inlined_sites(m_lines, source, typ):
    pat = re.compile(rf'(?:^|/){re.escape(source)}:(\d+):(\d+): inlining call to \(\*{typ}\)\.appendBits\b')
    sites = set()
    for line in m_lines:
        m = pat.search(line)
        if m:
            sites.add((int(m.group(1)), int(m.group(2))))
    return sites


def method_of(symbol, typ):
    """The method name when symbol is a method of typ (pointer or value receiver), else None."""
    m = re.search(rf'\.\(\*?{typ}\)\.(\w+)$', symbol)
    return m.group(1) if m else None


def barrier_sites(s_lines, typ):
    """({method: [file:line, ...]}, set of methods seen) for typ's methods in the assembly."""
    sites, seen, method = {}, set(), None
    for line in s_lines:
        m = STEXT.match(line)
        if m:
            method = method_of(m.group(1), typ)
            if method:
                seen.add(method)
            continue
        if method is None:
            continue
        b = BARRIER.match(line)
        if b:
            sites.setdefault(method, []).append(f'{b.group(1).rsplit("/", 1)[-1]}:{b.group(2)}')
    return sites, seen


def check(codec, source, source_lines, m_lines, s_lines):
    """The list of failures for one encoder; empty when the hot path is inlined and barrier-free."""
    typ = ENCODER_TYPES[codec]
    fails = []
    calls = call_sites(source_lines)
    if not calls:
        fails.append(f'no e.appendBits calls found in {source}')
    missing = calls - inlined_sites(m_lines, source, typ)
    if missing:
        fails.append(f'appendBits not inlined at {source} line:column {sorted(missing)}')
    sites, seen = barrier_sites(s_lines, typ)
    for name in REQUIRED_FUNCS:
        if name not in seen:
            fails.append(f'no assembly for (*{typ}).{name}')
    for name in NO_BARRIER_FUNCS:
        for where in sites.get(name, []):
            fails.append(f'{name} calls gcWriteBarrier at {where}')
    for name in GROW_ONLY_FUNCS:
        for where in sites.get(name, []):
            if not where.startswith(GROW_FILE + ':'):
                fails.append(f'{name} calls gcWriteBarrier outside Grow at {where}')
    return fails, len(calls)


def selftest():
    """Parser fixtures: each case is (description, source, -m lines, -S lines, expect pass)."""
    src = 'x/gorilla.go'
    source = ['func (e *NumericGorillaEncoder) Write(v float64) {\n', '\te.appendBits(1, 2); e.appendBits(3, 4)\n',
              '\t// e.appendBits(5, 6)\n']
    m_ok = [f'{src}:2:14: inlining call to (*NumericGorillaEncoder).appendBits\n',
            f'{src}:2:34: inlining call to (*NumericGorillaEncoder).appendBits\n']
    pkg = 'github.com/arloliu/mebo/internal/encoding/value/gorilla'

    def asm(*bodies):
        out = []
        for name, records in bodies:
            out.append(f'{pkg}.(*NumericGorillaEncoder).{name} STEXT size=10 args=0x8 locals=0x0\n')
            out.extend(records)
        return out

    def wb(path, line):
        return f'\t0x0010 00016 ({path}:{line})\tCALL\truntime.gcWriteBarrier2(SB)\n'

    rel = '\trel 17+4 t=R_CALL runtime.gcWriteBarrier2+0\n'
    clean = [(n, []) for n in REQUIRED_FUNCS]
    grow = [('Write', [wb('/r/internal/pool/byte_buffer_pool.go', 130), rel])] + [(n, []) for n in REQUIRED_FUNCS[:2]] + \
        [('WriteSlice', [])]
    cases = [
        ('clean', m_ok, asm(*clean), True),
        ('Grow barrier and relocation record allowed in Write', m_ok, asm(*grow), True),
        ('empty assembly', m_ok, [], False),
        ('missing writeValue body', m_ok, asm(*[(n, []) for n in REQUIRED_FUNCS if n != 'writeValue']), False),
        ('one call not inlined', m_ok[:1], asm(*clean), False),
        ('barrier in appendBits', m_ok, asm(('appendBits', [wb('/r/gorilla.go', 9)]), *clean[1:]), False),
        ('autogenerated barrier in Write', m_ok, asm(('Write', [wb('<autogenerated>', 1)]), *clean[:2], ('WriteSlice', [])),
         False),
        ('codec path with spaces in WriteSlice', m_ok,
         asm(*clean[:3], ('WriteSlice', [wb('/my repo/value/gorilla.go', 315)])), False),
    ]
    bad = []
    for name, m_lines, s_lines, want in cases:
        fails, _ = check('gorilla', src, source, m_lines, s_lines)
        if (not fails) != want:
            bad.append(f'{name}: want {"pass" if want else "fail"}, got {fails or "pass"}')
    if call_sites(source) != {(2, 14), (2, 34)}:
        bad.append(f'call_sites: got {sorted(call_sites(source))}')
    for b in bad:
        print(f'FAIL selftest: {b}')
    if bad:
        sys.exit(1)
    print(f'ok   selftest: {len(cases)} parser fixtures')


def main(argv):
    if argv[1:] == ['selftest']:
        selftest()
        return
    if len(argv) != 6 or argv[1] != 'check' or argv[2] not in ENCODER_TYPES:
        sys.exit(__doc__)
    codec, source, mfile, sfile = argv[2:]
    with open(source) as f:
        source_lines = f.readlines()
    with open(mfile) as f:
        m_lines = f.readlines()
    with open(sfile) as f:
        s_lines = f.readlines()
    fails, ncalls = check(codec, source, source_lines, m_lines, s_lines)
    for f in fails:
        print(f'FAIL {codec}: {f}')
    if fails:
        sys.exit(1)
    print(f'ok   {codec}: appendBits inlined at {ncalls} call sites; no spill write barrier')


if __name__ == '__main__':
    main(sys.argv)
