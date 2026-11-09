"""Go tests that read files outside their own package (source censuses, schema and contract guards).

Reverse Go imports cannot see these: a census test in one package scans the source text of
others, so a change there never selected it (#284 changed internal/ingestion and never ran the
persistence census). ci/source-readers.json declares each such test, and the files it reads; the
planner selects the declared tests whenever a changed path is one of those files. The manifest is
the authority; scan() is the check that nothing was left out (ci/test_source_readers.py).
"""
import fnmatch
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / 'ci/source-readers.json'
MODULES = ('server', 'apikit')

def load(path=MANIFEST):
    data = json.loads(Path(path).read_text())
    for entry in data['readers']:
        for key in ('package', 'tests', 'reads', 'files', 'why'):
            if not entry.get(key): raise ValueError('source reader entry lacks ' + key + ': ' + json.dumps(entry)[:120])
    return data

def reads(entry, path):
    """Whether a changed repo-relative path is a file this entry's tests read."""
    if any(fnmatch.fnmatchcase(path, pattern) for pattern in entry.get('ignore', [])): return False
    for pattern in entry['reads']:
        if pattern.endswith('/'):
            if path.startswith(pattern): return True
        elif path == pattern or fnmatch.fnmatchcase(path, pattern): return True
    return False

def select(paths, data=None):
    """{repo-relative package directory: {test name: [changed path, ...]}} for the changed paths."""
    data = data or load(); chosen = {}
    for entry in data['readers']:
        hit = sorted(p for p in paths if reads(entry, p))
        if not hit: continue
        tests = chosen.setdefault(entry['package'], {})
        for name in entry['tests']: tests.setdefault(name, []).extend(hit)
    return chosen

# --- the check: find the tests that read outside their package, from the test source itself ---

LITERAL = re.compile(r'"((?:[^"\\]|\\.)*)"')
PRIMITIVE = re.compile(r'\b(?:ReadFile|ReadDir|Walk|WalkDir|Glob|ParseFile|ParseDir|Abs|Join|Open|Stat|DirFS)\(')
FUNCTION = re.compile(r'^func\s+(?:\([^)]*\)\s*)?(\w+)\s*[\[(]', re.M)

def test_files(root=ROOT):
    for module in MODULES:
        yield from (p for p in sorted((root / module).rglob('*_test.go')) if not p.relative_to(root).as_posix().startswith('server/internal/store/'))

def functions(path):
    """[(name, first line, last line, body)] for the top-level functions of one Go source file."""
    source = path.read_text(); matches = list(FUNCTION.finditer(source)); result = []
    for i, match in enumerate(matches):
        end = matches[i + 1].start() if i + 1 < len(matches) else len(source)
        first = source.count('\n', 0, match.start()) + 1
        result.append((match[1], first, first + source.count('\n', match.start(), end), source[match.start():end]))
    return result

def marker_lines(path):
    """Lines that point a test at a file outside its own package directory.

    A literal that climbs out of the package ("../x", filepath.Join("..", "..", "x"), Abs("../..")) counts
    when it opens the path or climbs two levels or more, or when ../x names something that exists. A single
    ".." in the middle of a join ("../outside" traversal fixtures, Join(root, "..")) is not a read.
    """
    found = []
    for number, line in enumerate(path.read_text().splitlines(), 1):
        if line.lstrip().startswith('//'): continue
        literals = LITERAL.findall(line)
        if not any(x == '..' or x.startswith('../') for x in literals): continue
        opens = re.search(r'\(\s*"\.\.(?:/\.\.)*/?"', line) and PRIMITIVE.search(line)
        climbs = any(re.fullmatch(r'\.\.(?:/\.\.){1,}/?', x) for x in literals)
        names = any(re.match(r'(?:\.\./)+[^/.]', x) and (path.parent / x).exists() for x in literals)
        if opens or climbs or names: found.append(number)
    return found

def marked_tests(directory, files=None):
    """Test names in one package directory that reach a marker line, directly or through helpers."""
    parsed = {f: functions(f) for f in sorted(directory.glob('*_test.go'))}
    everything = [fn for fs in parsed.values() for fn in fs]
    names = set()
    for f in (files if files is not None else parsed):
        lines = marker_lines(f)
        names.update(name for name, first, last, body in parsed[f] if any(first <= n <= last for n in lines))
    grew = True
    while grew:
        grew = False
        for name, first, last, body in everything:
            if name not in names and any(re.search(r'\b' + re.escape(called) + r'\s*\(', body) for called in names):
                names.add(name); grew = True
    return {n for n in names if n.startswith(('Test', 'Fuzz'))}

def scan(root=ROOT):
    """{repo-relative test file: sorted marker lines} for every Go test file that reads outside its package."""
    return {str(f.relative_to(root)): lines for f in test_files(root) if (lines := marker_lines(f))}
