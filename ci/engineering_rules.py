"""Ratchets for docs/ENGINEERING-RULES.md: per-file counts of known-bad patterns may fall, never rise.

python3 ci/engineering_rules.py            check tracked client sources against the baseline
python3 ci/engineering_rules.py --update   rewrite the baseline after a count fell
"""
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BASELINE = ROOT / 'ci' / 'engineering-rules-baseline.json'
SOURCES = re.compile(r'^(?:packages/[^/]+/src|web/src|apps/[^/]+/src(?:-next)?)/.+\.tsx?$')
TESTS = re.compile(r'(?:^|/)(?:test|tests|__tests__)/|\.test\.tsx?$')

# (rule, files allowed to hold the pattern, pattern)
RULES = [
    ('token-compare', {'packages/client-core/src/session-authority.ts'},
     re.compile(r'\.accessToken\s*[!=]==|[!=]==\s*[\w.!?]*\.accessToken\b')),
    ('set-interval', set(), re.compile(r'\bsetInterval\(')),
    ('retry-after', {'packages/client-core/src/timer.ts', 'packages/client-core/src/index.ts'},
     re.compile(r'''['"]retry-after['"]''', re.IGNORECASE)),
    ('throwing-cleanup', {'packages/client-core/src/cleanup.ts'},
     re.compile(r'catch\s*\(\s*(\w+)\s*\)\s*\{\s*(?:await\s+)?[\w.?!]+\.(?:dispose|close|retire|release|revoke|cleanup|stop)\([^;{}()]*\)\s*;?\s*throw\s+\1\s*;?\s*\}')),
]
GUIDE = {
    'token-compare': 'compare sign-ins with compareSessionAuthority (packages/client-core/src/session-authority.ts)',
    'set-interval': 'poll with repeat() from packages/client-core/src/timer.ts',
    'retry-after': 'read Retry-After with parseRetryAfter() or serverFloorFrom() from packages/client-core/src/timer.ts',
    'throwing-cleanup': 'release with releaseQuietly() or releaseThenRethrow() from packages/client-core/src/cleanup.ts',
}


def tracked():
    import subprocess
    out = subprocess.run(['git', 'ls-files', '-z'], cwd=ROOT, check=True, capture_output=True).stdout.decode()
    return [p for p in out.split('\0') if p and SOURCES.match(p) and not TESTS.search(p)]


def counts(paths, read=lambda p: (ROOT / p).read_text(encoding='utf-8', errors='replace')):
    found = {}
    for path in paths:
        text = read(path)
        for rule, allowed, pattern in RULES:
            if path in allowed:
                continue
            n = len(pattern.findall(text))
            if n:
                found.setdefault(rule, {})[path] = n
    return found


def compare(found, baseline):
    """Problems as (rule, path, now, allowed) for every count above the baseline, and a separate
    list of counts that fell below it (the baseline must then be lowered)."""
    risen, fallen = [], []
    for rule, _, _ in RULES:
        now, was = found.get(rule, {}), baseline.get(rule, {})
        for path in sorted(set(now) | set(was)):
            a, b = now.get(path, 0), was.get(path, 0)
            if a > b:
                risen.append((rule, path, a, b))
            elif a < b:
                fallen.append((rule, path, a, b))
    return risen, fallen


def main(argv):
    found = counts(tracked())
    if '--update' in argv:
        BASELINE.write_text(json.dumps(found, indent=1, sort_keys=True) + '\n')
        print(f'engineering-rules baseline written ({sum(len(v) for v in found.values())} files)')
        return 0
    baseline = json.loads(BASELINE.read_text())
    risen, fallen = compare(found, baseline)
    for rule, path, a, b in risen:
        print(f'{path}: {rule} {a} (baseline {b}). See docs/ENGINEERING-RULES.md: {GUIDE[rule]}.')
    for rule, path, a, b in fallen:
        print(f'{path}: {rule} fell to {a} (baseline {b}). Lock it in: python3 ci/engineering_rules.py --update')
    if risen or fallen:
        return 1
    print('engineering rules hold')
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
