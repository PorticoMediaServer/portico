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
    # Queued commits can hold the HTTP client at either side of the file split.
    ('retry-after', {'packages/client-core/src/timer.ts', 'packages/client-core/src/index.ts', 'packages/client-core/src/http.ts'},
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
]
# A compare kept on purpose carries `// fence-compare: kept (reason)` as a real line comment on its own line, with a
# non-empty reason. The marker spares exactly one match on that line: a second compare there still counts. The text
# inside a string or template, or without a reason, spares nothing, and neither does a marker on the line above.
KEPT = {rule: re.compile(r'^//\s*' + re.escape(rule) + r': kept \(\S[^)]*\)') for rule in ('fence-compare',)}


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
}


def exempt(path, allowed):
    """A rule's own homes: an exact file, or every file under a directory named with a trailing slash."""
    return any(path == home or home.endswith('/') and path.startswith(home) for home in allowed)


def tracked(root=ROOT):
    import subprocess
    out = subprocess.run(['git', 'ls-files', '-z'], cwd=root, check=True, capture_output=True).stdout.decode()
    return [p for p in out.split('\0') if p and SOURCES.match(p) and not TESTS.search(p)]


def counts(paths, read=lambda p: (ROOT / p).read_text(encoding='utf-8', errors='replace')):
    """Per rule and file counts of the patterns above."""
    found = {}
    for path in paths:
        text = read(path)
        for rule, allowed, pattern in RULES:
            if exempt(path, allowed):
                continue
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


def source_counts(root, paths=None):
    paths=tracked(root) if paths is None else [p for p in paths if SOURCES.match(p) and not TESTS.search(p)]
    runtime=active_runtime(root)
    if runtime:
        view=runtime.current(root)
        # Reuse the census's production dependency inventory for imported sources.
        runtime.census(view)
        paths=sorted(set(paths)|{p for p in runtime.census_inventories[view.identity] if SOURCES.match(p)})
        found={}
        for path in paths:
            _,oid,data=view.records[path];key=(path,oid)
            if key not in runtime.rule_counts:
                runtime.rule_counts[key]=counts([path],read=lambda _:source_text(data,errors='replace'))
            for rule,files in runtime.rule_counts[key].items():found.setdefault(rule,{}).update(files)
        return found
    return counts(paths,read=lambda p:(root/p).read_text(encoding='utf-8',errors='replace'))


def _main(argv, root=ROOT, paths=None):
    write_root=root
    view=active_runtime(root).current(root,paths);root=view.root;paths=view.paths
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--update',action='store_true');parser.add_argument('--base');parser.add_argument('--predecessor');parser.add_argument('--queue-head')
    args=parser.parse_args(argv)
    if args.queue_head and not args.base:parser.error('--queue-head requires --base')
    for field in ['base','predecessor','queue_head']:
        if getattr(args,field):setattr(args,field,active_runtime(root).git.git(['rev-parse','--verify',getattr(args,field)+'^{commit}']).decode().strip())
    queue_errors=check_queue(root,args.predecessor or args.base,args.queue_head,lambda parent,snapshot,paths:main(['--base',parent],root=snapshot,paths=paths)) if args.queue_head else []
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
