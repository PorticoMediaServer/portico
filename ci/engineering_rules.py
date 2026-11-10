"""Ratchets for docs/ENGINEERING-RULES.md: per-file counts of known-bad patterns may fall, never rise.

python3 ci/engineering_rules.py            check tracked sources against baseline upper bounds
python3 ci/engineering_rules.py --base SHA also forbid growth against live merge-base counts
python3 ci/engineering_rules.py --update   optionally lower the baseline after a count fell
"""
import json
import argparse
import re
import sys
import subprocess
from pathlib import Path
from ratchet_base import merge_base, source_snapshot, check_queue, historical_json
from ratchet_runtime import RatchetRuntime, active_runtime, source_text

ROOT = Path(__file__).resolve().parent.parent
BASELINE = ROOT / 'ci' / 'engineering-rules-baseline.json'
SOURCES = re.compile(r'^(?:packages/[^/]+/src|web/src|apps/[^/]+/src(?:-next)?)/.+\.(?:[cm]?ts|tsx|[cm]?js|jsx)$')
TESTS = re.compile(r'(?:^|/)(?:test|tests|__tests__)/|\.(?:test|spec|browser|capture)\.(?:[cm]?ts|tsx|[cm]?js|jsx)$|\.d\.[cm]?ts$')

# (rule, files allowed to hold the pattern, pattern)
RULES = [
    ('token-compare', {'packages/client-core/src/session-authority.ts'},
     re.compile(r'\.accessToken\s*[!=]==|[!=]==\s*[\w.!?]*\.accessToken\b')),
    ('set-interval', set(), re.compile(r'\bsetInterval\(')),
    ('retry-after', {'packages/client-core/src/timer.ts', 'packages/client-core/src/http.ts'},
     re.compile(r'''['"]retry-after['"]''', re.IGNORECASE)),
    ('throwing-cleanup', {'packages/client-core/src/cleanup.ts'},
     re.compile(r'catch\s*\(\s*(\w+)\s*\)\s*\{\s*(?:await\s+)?[\w.?!]+\.(?:dispose|close|retire|release|revoke|cleanup|stop)\([^;{}()]*\)\s*;?\s*throw\s+\1\s*;?\s*\}')),
    # CLIENT-DATA-REVIEW §4.4: a fence is the server's; a client comparing two only learns "read again". Not counted
    # (step 7 item 5): a fence tested against a literal, null or undefined (`typeof raw.viewerFence !== 'string'`), a
    # shape or presence check; a property read through an object named fence (`playback.fence.sessionId`); and a line
    # that keeps its compare on purpose, marked `fence-compare: kept` with the reason (KEPT below).
    # A template literal with a substitution is not a literal, and a method or optional call on a fence
    # (`doc.viewerFence.trim()`) is still the fence: only a plain field read through it is spared.
    ('fence-compare', set(), re.compile(r'''\b\w*[Ff]ence\b\s*[!=]==(?!\s*(?:['"]|`[^`$]*`|undefined\b|null\b))'''
                                        r'''|[!=]==\s*[\w.!?]*[Ff]ence\b(?!\??\.\w+\b(?!\s*(?:\?\.)?\())''')),
    # §4.1: a screen's document lives in the viewer's query cache, not a per-route memory or seed.
    ('page-memory', set(), re.compile(r'\busePageMemory\(|\bpages\.(?:remember|recall)\(')),
    # §4.7: currency is the cache's to own; no new per-screen lease hooks re-checking it.
    ('lease-layer', set(), re.compile(r'\bfunction\s+use\w*Lease\b')),
    # §4 / step 7 slice 3: a screen reads a server document through a query function in the viewer's query cache
    # (packages/client-core/src/query/), not with its own GET or a service it builds. Counted by the TypeScript
    # checker, not a pattern (ci/query_reads.mjs says exactly what counts). A read that must stay outside the cache
    # (sign-in, the event feed, the cache's own transport) carries `// query-read: kept (reason)` on its line.
    ('query-read', {'packages/client-core/src/query/'}, None),
]
# A compare kept on purpose carries `// fence-compare: kept (reason)` as a real line comment on its own line, with a
# non-empty reason. The marker spares exactly one match on that line: a second compare there still counts. The text
# inside a string or template, or without a reason, spares nothing, and neither does a marker on the line above.
KEPT = {rule: re.compile(r'^//\s*' + re.escape(rule) + r': kept \(\S[^)]*\)') for rule in ('fence-compare', 'query-read')}


def line_comments(text):
    """The line numbers (0-based) whose `//` comment, outside any string, template or block comment, is returned
    with it: {line: comment text}. A rough scanner: enough for the marker, not a JavaScript parser."""
    found, line, i, n = {}, 0, 0, len(text)
    stack = []  # open quotes: "'", '"', '`', or '{' for a `${` substitution inside a template
    while i < n:
        c = text[i]
        if c == '\n':
            line += 1
        top = stack[-1] if stack else None
        if top in ("'", '"'):
            if c == '\\':
                i += 2
                continue
            if c == top or c == '\n':
                stack.pop()
        elif top == '`':
            if c == '\\':
                i += 2
                continue
            if c == '`':
                stack.pop()
            elif text.startswith('${', i):
                stack.append('{')
                i += 2
                continue
        else:
            if text.startswith('//', i):
                end = text.find('\n', i)
                end = n if end < 0 else end
                found[line] = text[i:end]
                i = end
                continue
            if text.startswith('/*', i):
                end = text.find('*/', i + 2)
                end = n if end < 0 else end + 2
                line += text.count('\n', i, end)
                i = end
                continue
            if c in ('"', "'", '`'):
                stack.append(c)
            elif c == '{' and top == '{':
                stack.append('{')
            elif c == '}' and top == '{':
                stack.pop()
        i += 1
    return found


def count(pattern, text, marker=None):
    return spare([text.count('\n', 0, m.start()) for m in pattern.finditer(text)], text, marker)


def spare(matches, text, marker=None):
    """How many of these matches (0-based line numbers) count once each marked line spares one."""
    matches = list(matches)
    if marker is None or not matches:
        return len(matches)
    spared = {line for line, comment in line_comments(text).items() if marker.match(comment)}
    for line in spared:
        if line in matches:
            matches.remove(line)
    return len(matches)
GUIDE = {
    'token-compare': 'compare sign-ins with compareSessionAuthority (packages/client-core/src/session-authority.ts)',
    'set-interval': 'poll with repeat() from packages/client-core/src/timer.ts',
    'retry-after': 'read Retry-After with parseRetryAfter() or serverFloorFrom() from packages/client-core/src/timer.ts',
    'throwing-cleanup': 'release with releaseQuietly() or releaseThenRethrow() from packages/client-core/src/cleanup.ts',
    'fence-compare': 'read through the viewer query cache (packages/client-core/src/query); a moved fence means read again, and only a revision of the same entity is compared',
    'page-memory': 'keep a screen\'s document in the viewer query cache (packages/client-core/src/query) instead of a page memory',
    'lease-layer': 'read through the viewer query cache, which owns cancellation and the viewer boundary, instead of a new lease hook',
    'query-read': 'read server documents through a query function in packages/client-core/src/query (viewer-queries.ts), not with a GET or a new service outside it',
}


def exempt(path, allowed):
    """A rule's own homes: an exact file, or every file under a directory named with a trailing slash."""
    return any(path == home or home.endswith('/') and path.startswith(home) for home in allowed)


def tracked(root=ROOT):
    import subprocess
    out = subprocess.run(['git', 'ls-files', '-z'], cwd=root, check=True, capture_output=True).stdout.decode()
    return [p for p in out.split('\0') if p and SOURCES.match(p) and not TESTS.search(p)]


# The TypeScript projects that hold every client source (the apps' first; ci/query-reads.tsconfig.json last).
QUERY_READ_CONFIGS = ['web/tsconfig.json', 'apps/native/tsconfig.json', 'apps/vega/tsconfig.json', 'apps/desktop/tsconfig.json',
                      'packages/client-core/tsconfig.json', 'packages/player-core/tsconfig.json', 'packages/reader-core/tsconfig.json',
                      'packages/contracts/tsconfig.json', 'ci/query-reads.tsconfig.json']


QUERY_READ_SKIP = {'node_modules', '.git', 'dist', 'build', 'android', 'ios', 'Pods', '.gradle'}


def _sha(path):
    import hashlib
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def query_reads(paths, root=ROOT, configs=QUERY_READ_CONFIGS):
    """{path: [1-based line of each server read]} from ci/query_reads.mjs, which needs the root `npm ci`.

    One run serves every caller over the same tree (hygiene's unit test, then the check), kept in the system temp
    directory. Its key covers everything the programs could load: every TypeScript and JSON file under the projects'
    directories (tracked or not, counted or exempt, and the configs they extend), the request, the counter and the
    lockfile (for node_modules). A file the programs loaded from anywhere else is recorded with the answer and
    checked again before the answer is reused."""
    runtime=active_runtime(root)
    if runtime:return runtime.query(runtime.current(root),paths,configs)
    import hashlib
    import os
    import subprocess
    import tempfile
    root = Path(root)
    request = json.dumps({'root': str(root), 'configs': list(configs), 'files': list(paths)})
    digest = hashlib.sha256(request.encode())
    for own in [ROOT / 'ci' / 'query_reads.mjs', ROOT / 'package-lock.json']:
        digest.update(b'\0' + own.read_bytes())
    homes = sorted({(root / config).parent.resolve() for config in configs} | ({(root / 'packages').resolve()} if (root / 'packages').is_dir() else set()))
    seen = set()
    for home in homes:
        for folder, dirs, files in os.walk(home):
            dirs[:] = sorted(d for d in dirs if d not in QUERY_READ_SKIP)
            for name in sorted(files):
                if not name.endswith(('.ts', '.tsx', '.mts', '.cts', '.json')):
                    continue
                path = Path(folder) / name
                if path in seen:
                    continue
                seen.add(path)
                digest.update(b'\0' + str(path).encode() + b'\0' + path.read_bytes())
    kept = Path(tempfile.gettempdir()) / f'portico-query-reads-{digest.hexdigest()}.json'
    if kept.exists():
        try:
            entry = json.loads(kept.read_text())
            if all(Path(path).is_file() and _sha(path) == sha for path, sha in entry['elsewhere'].items()):
                return entry['reads']
        except (ValueError, KeyError, OSError):
            pass
    try:
        run = subprocess.run(['node', str(ROOT / 'ci' / 'query_reads.mjs')], input=request, capture_output=True, text=True, cwd=root, timeout=600)
    except FileNotFoundError as error:
        raise SystemExit('query-read needs Node (ci/query_reads.mjs)') from error
    if run.returncode != 0:
        raise SystemExit('query-read counter failed (run `npm ci` at the repository root first):\n' + run.stderr)
    answer = json.loads(run.stdout)
    # Loaded from outside the projects' directories and node_modules: checked again before the answer is reused.
    elsewhere = {path: _sha(path) for path in answer['loaded']
                 if 'node_modules' not in Path(path).parts and Path(path) not in seen and Path(path).is_file()}
    kept.write_text(json.dumps({'elsewhere': elsewhere, 'reads': answer['reads']}))
    return answer['reads']


def counts(paths, read=lambda p: (ROOT / p).read_text(encoding='utf-8', errors='replace'), reads=None):
    """Per rule and file, the counts above. `reads` gives query-read's lines ({path: [line]}); without it they are
    found with the TypeScript checker, but only when the files are read from the tree (a test's `read` has none)."""
    found = {}
    paths = list(paths)
    if reads is None and read is counts.__defaults__[0]:
        reads = query_reads([p for p in paths if not exempt(p, dict((r, a) for r, a, _ in RULES)['query-read'])])
    for path in paths:
        text = read(path)
        for rule, allowed, pattern in RULES:
            if exempt(path, allowed):
                continue
            if pattern is None:
                n = spare([line - 1 for line in (reads or {}).get(path, [])], text, KEPT.get(rule))
            else:
                n = count(pattern, text, KEPT.get(rule))
            if n:
                found.setdefault(rule, {})[path] = n
    return found


# A file split into several moves its counts without adding any: the parts are counted together, under the
# name the file had, so the split neither needs nor gets a larger allowance.
SPLIT = {part: 'packages/client-core/src/index.ts' for part in (
    'packages/client-core/src/http.ts', 'packages/client-core/src/viewer-service.ts', 'packages/client-core/src/playback-service.ts')}


def together(files):
    out = {}
    for path, n in files.items():
        out[SPLIT.get(path, path)] = out.get(SPLIT.get(path, path), 0) + n
    return out


def compare(found, baseline):
    """Problems as (rule, path, now, allowed) for every count above the baseline, and a separate
    list of counts that fell below it (baseline refreshes are optional)."""
    risen, fallen = [], []
    for rule, _, _ in RULES:
        now, was = together(found.get(rule, {})), together(baseline.get(rule, {}))
        for path in sorted(set(now) | set(was)):
            a, b = now.get(path, 0), was.get(path, 0)
            if a > b:
                risen.append((rule, path, a, b))
            elif a < b:
                fallen.append((rule, path, a, b))
    return risen, fallen


def source_counts(root, paths=None, configs=QUERY_READ_CONFIGS):
    paths=tracked(root) if paths is None else [p for p in paths if SOURCES.match(p) and not TESTS.search(p)]
    reads=query_reads([p for p in paths if not exempt(p,dict((r,a)for r,a,_ in RULES)['query-read'])],root=root,configs=configs)
    runtime=active_runtime(root)
    if runtime:
        view=runtime.current(root);found={}
        paths=sorted(set(paths)|set(runtime.query_inventories.get(view.identity,[])))
        for path in paths:
            _,oid,data=view.records[path];key=(path,oid,tuple(reads.get(path,[])))
            if key not in runtime.rule_counts:
                runtime.rule_counts[key]=counts([path],read=lambda _:source_text(data,errors='replace'),reads=reads)
            for rule,files in runtime.rule_counts[key].items():found.setdefault(rule,{}).update(files)
        return found
    return counts(paths,read=lambda p:(root/p).read_text(encoding='utf-8',errors='replace'),reads=reads)


def _main(argv, root=ROOT, paths=None):
    write_root=root
    view=active_runtime(root).current(root,paths);root=view.root;paths=view.paths
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--update',action='store_true');parser.add_argument('--base');parser.add_argument('--predecessor');parser.add_argument('--queue-head')
    args=parser.parse_args(argv)
    if args.queue_head and not args.base:parser.error('--queue-head requires --base')
    for field in ['base','predecessor','queue_head']:
        if getattr(args,field):setattr(args,field,active_runtime(root).git.git(['rev-parse','--verify',getattr(args,field)+'^{commit}']).decode().strip())
    queue_errors=check_queue(root,args.base,args.queue_head,lambda parent,snapshot,paths:main(['--base',parent],root=snapshot,paths=paths)) if args.queue_head else []
    found=source_counts(root,paths)
    baseline_path=root/BASELINE.relative_to(ROOT)
    baseline=json.loads(baseline_path.read_text())
    risen,_=compare(found,baseline)
    for ref in dict.fromkeys(merge_base(root,ref)for ref in [args.base,args.predecessor]if ref):
        old_baseline=historical_json(root,ref,str(BASELINE.relative_to(ROOT)))
        for rule,path,a,b in compare(baseline,old_baseline)[0]:
            print(f'{path}: {rule} baseline increased {b} -> {a}; only decreases are allowed.')
            queue_errors.append('Engineering baseline growth is forbidden, including beneath a stale upper bound.')
        with source_snapshot(root,ref) as (snapshot,paths):
            previous=source_counts(snapshot,paths)
        risen+=compare(found,previous)[0]
    for rule, path, a, b in sorted(set(risen)):
        print(f'{path}: {rule} {a} (allowed {b}). See docs/ENGINEERING-RULES.md: {GUIDE[rule]}.')
    for error in queue_errors:print(error)
    if queue_errors:return 1
    if risen:return 1
    if args.update:
        (write_root/BASELINE.relative_to(ROOT)).write_text(json.dumps(found, indent=1, sort_keys=True) + '\n')
        print(f'engineering-rules baseline written ({sum(len(v) for v in found.values())} files)')
        return 0
    print('engineering rules hold')
    return 0


def main(argv, root=ROOT, paths=None):
    try:
        if runtime:=active_runtime(root):
            status=_main(argv,root,paths);runtime.ensure_head();return status
        with RatchetRuntime(root) as runtime:
            status=_main(argv,root,paths);runtime.ensure_head();return status
    except (ValueError,OSError,RuntimeError,subprocess.CalledProcessError) as error:
        print(f'Engineering ratchet failed closed: {error}',file=sys.stderr);return 1


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
