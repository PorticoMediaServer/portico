#!/usr/bin/env python3
"""Name every writer plan-test hash pin a pull request touches.

`server/internal/store/writer/*_plan_test.go` freezes a SHA-256 of each source,
test and fixture file (and sometimes of a single fixture function) that its
captured plans depend on. Editing a frozen file fails those tests late, in
review, instead of at the first PR run (PRs #1696, #1699 and #1726 each lost a
round to this). This check reads the plan tests at the PR head, records every
file and fixture function they pin, and names each changed file a pin covers,
with the exact plan test and line to re-record.

  python3 ci/plan_pins.py
  python3 ci/plan_pins.py --base origin/main --head HEAD
  python3 ci/plan_pins.py --changed-files changes.txt --strict
  python3 ci/plan_pins.py --json

Informational by default: exit 0, with a log line per pin, a GitHub warning
annotation and a job summary. `--strict` exits 1 when any changed file is
pinned, so the check can be tightened once it has been quiet for a while.

A changed-files file holds one entry per line: `M path`, `A path`, `D path`,
`R old new`, or a bare path (treated as modified).

Pins named indirectly are out of scope: a helper that hashes a fixed file for
every plan test that calls it (captureEffectiveFixture in
effective_dispatch_test.go) is not a `*_plan_test.go` reference, and a plan
test that hashes another plan test's fixture function through that helper is
reported only at its direct pin. The direct pin still names the plan test to
re-record, which is what the author needs.
"""
import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WRITER = Path('server/internal/store/writer')

STRING = re.compile(r'"(?:[^"\\\n]|\\.)*"|`[^`\n]*`')
GO_FILE = re.compile(r'^(?:[A-Za-z0-9_.\-]+/)*[A-Za-z0-9_.\-]+\.go$')
HEX64 = re.compile(r'^[0-9a-fA-F]{64}$')
IDENT = re.compile(r'^[A-Za-z_]\w*$')

RECORD = 'add a new file/wrapper or re-record the pin with a "no plan change" statement'


def literals(text):
    """(start, end, value, 1-based line) for every Go string literal."""
    for match in STRING.finditer(text):
        raw = match.group(0)
        yield match.start(), match.end(), raw[1:-1], text.count('\n', 0, match.start()) + 1


def line_span(text, offset):
    start = text.rfind('\n', 0, offset) + 1
    end = text.find('\n', offset)
    return start, len(text) if end < 0 else end


def paired_function(text, group, index):
    """The identifier this file literal is paired with inside one struct element.

    Plan tests write function pins as `{"file_test.go", "fixtureFunc"}`. Two
    adjacent elements on one line must not bleed into each other, so a closing
    brace between the literals breaks the pair.
    """
    own = group[index]
    for other_index in (index - 1, index + 1):
        if not 0 <= other_index < len(group):
            continue
        other = group[other_index]
        left, right = sorted([(own[0], own[1]), (other[0], other[1])])
        between = text[left[1]:right[0]]
        if '}' in between or '{' in between:
            continue
        if IDENT.match(other[2]) and not HEX64.match(other[2]):
            return other[2]
    return None


def extract_pins(text, plan_test):
    """Pins declared by one plan test, as dicts with a repo-relative path."""
    pins = []
    group = {}
    items = list(literals(text))
    for item in items:
        group.setdefault(item[3], []).append(item)
    for start, _end, value, line in items:
        if not GO_FILE.match(value):
            continue
        span_start, _span_end = line_span(text, start)
        if 'CallSites:' in text[span_start:start]:
            continue  # CallSites names the calling test; it is not a digest input
        on_line = group[line]
        index = next(i for i, item in enumerate(on_line) if item[0] == start)
        function = paired_function(text, on_line, index)
        target = value if '/' in value else str(WRITER / value)
        pins.append({'plan_test': plan_test, 'line': line, 'target': os.path.normpath(target),
                     'function': function or None})
    return pins


def scan_plan_tests(root, writer=WRITER):
    pins = []
    for path in sorted((root / writer).glob('*_plan_test.go')):
        relative = str(path.relative_to(root))
        pins.extend(extract_pins(path.read_text(), relative))
    return pins


def git(root, *args):
    return subprocess.check_output(['git', *args], cwd=root, text=True, timeout=120)


def event_payload():
    path = os.environ.get('GITHUB_EVENT_PATH')
    if not path:
        return {}, ''
    try:
        return json.loads(Path(path).read_text()), os.environ.get('GITHUB_EVENT_NAME', '')
    except (OSError, ValueError):
        return {}, os.environ.get('GITHUB_EVENT_NAME', '')


def rev_exists(root, ref):
    return subprocess.run(['git', 'rev-parse', '--verify', '--quiet', ref + '^{commit}'],
                          cwd=root, stdout=subprocess.DEVNULL,
                          stderr=subprocess.DEVNULL).returncode == 0


def base_ref(explicit, root=ROOT):
    """The comparison ref: the PR base, the queue target, CI_BASE, or main."""
    if explicit:
        return explicit
    if os.environ.get('CI_BASE'):
        return os.environ['CI_BASE']
    payload, name = event_payload()
    if name == 'pull_request':
        base = payload.get('pull_request', {}).get('base', {}).get('sha')
        if base:
            return base
    if name == 'merge_group':
        group = payload.get('merge_group', {})
        target = group.get('base_ref', '')
        if target.startswith('refs/heads/'):
            target = 'origin/' + target[len('refs/heads/'):]
            if rev_exists(root, target):
                return target
        if group.get('base_sha'):
            return group['base_sha']
    for candidate in ('origin/main', 'main'):
        if rev_exists(root, candidate):
            return candidate
    raise SystemExit('plan_pins: no comparison ref; pass --base')


def changes_from_git(base, head, root=ROOT):
    merge_base = git(root, 'merge-base', base, head).strip()
    fields = git(root, 'diff', '--name-status', '-z', '-M', merge_base, head).split('\0')
    changes, index = [], 0
    while index < len(fields) and fields[index]:
        status = fields[index][0]
        if status in 'RC':
            changes.append({'status': status, 'old': fields[index + 1], 'path': fields[index + 2]})
            index += 3
        else:
            changes.append({'status': status, 'path': fields[index + 1]})
            index += 2
    return merge_base, changes


def changes_from_file(path):
    changes = []
    for line in Path(path).read_text().splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        fields = line.split('\t') if '\t' in line else line.split()
        if len(fields) > 1 and fields[0][:1].isalpha() and fields[0][:1].isupper():
            status = fields[0][0]
            if status in 'RC':
                changes.append({'status': status, 'old': fields[1], 'path': fields[2]})
            else:
                changes.append({'status': status, 'path': fields[1]})
        else:
            changes.append({'status': 'M', 'path': line})
    return changes


def hits_for(pins, changes):
    """Pins touched by a changed file, including renamed and deleted files."""
    rows, seen = [], set()
    for change in changes:
        for pin in pins:
            if change.get('old') == pin['target'] and change['status'] == 'R':
                status = 'R'
            elif pin['target'] == change['path']:
                status = change['status']
            else:
                continue
            row = {'changed': change['path'], 'old': change.get('old'),
                   'plan_test': pin['plan_test'], 'line': pin['line'],
                   'function': pin['function'], 'status': status}
            key = (row['changed'], row['old'], row['plan_test'], row['line'], row['function'])
            if key not in seen:
                seen.add(key)
                rows.append(row)
    return sorted(rows, key=lambda row: (row['changed'], row['old'] or '',
                                         row['plan_test'], row['line']))


def message(row):
    where = row['plan_test'] + ':' + str(row['line'])
    function = ' (function ' + row['function'] + ')' if row['function'] else ''
    if row['status'] == 'R':
        return ('PINNED: %s was renamed from %s, which is hash-pinned by %s%s; %s'
                % (row['changed'], row['old'], where, function, RECORD))
    if row['status'] == 'D':
        return ('PINNED: %s is deleted but is hash-pinned by %s%s; %s'
                % (row['changed'], where, function, RECORD))
    return 'PINNED: %s is hash-pinned by %s%s; %s' % (row['changed'], where, function, RECORD)


def summary(rows, considered):
    lines = ['## Plan hash pins', '']
    if not rows:
        lines.append('None of the %d changed file(s) is hash-pinned by a `*_plan_test.go` file.' % considered)
        return '\n'.join(lines) + '\n'
    files = {row['changed'] for row in rows}
    lines.append('%d changed file(s) touch %d hash pin(s):' % (len(files), len(rows)))
    lines.append('')
    for row in rows:
        lines.append('- ' + message(row))
    return '\n'.join(lines) + '\n'


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split('\n')[0])
    parser.add_argument('--base', help='comparison ref; default is the PR base, queue target, CI_BASE or origin/main')
    parser.add_argument('--head', default='HEAD', help='head ref (default HEAD)')
    parser.add_argument('--changed-files', help='read changed files from this file instead of git diff')
    parser.add_argument('--root', default=str(ROOT), help='repository root (default: this checkout)')
    parser.add_argument('--strict', action='store_true', help='exit 1 when a changed file is pinned')
    parser.add_argument('--json', action='store_true', help='print the pins as JSON')
    parser.add_argument('--no-annotations', action='store_true',
                        help='print plain lines even when GITHUB_ACTIONS is set')
    parser.add_argument('--summary', nargs='?', const='', default='',
                        help='append a markdown summary to this file, or $GITHUB_STEP_SUMMARY by default')
    args = parser.parse_args(argv)

    root = Path(args.root).resolve()
    if args.changed_files:
        merge_base, changes = None, changes_from_file(args.changed_files)
    else:
        merge_base, changes = changes_from_git(base_ref(args.base, root), args.head, root)
    pins = scan_plan_tests(root)
    rows = hits_for(pins, changes)

    if args.json:
        print(json.dumps({'merge_base': merge_base, 'changed': changes, 'pins': rows}, indent=2))
    else:
        if not rows:
            print('Plan hash pins: none of the %d changed file(s) is hash-pinned by a *_plan_test.go file.'
                  % len(changes))
        else:
            files = {row['changed'] for row in rows}
            print('Plan hash pins: %d changed file(s) touch %d pin(s) in %d plan test(s).'
                  % (len(files), len(rows), len({row['plan_test'] for row in rows})))
            annotate = os.environ.get('GITHUB_ACTIONS') == 'true' and not args.no_annotations
            for row in rows:
                text = message(row)
                if annotate:
                    print('::warning file=%s,line=%d,title=Plan hash pin::%s'
                          % (row['plan_test'], row['line'], text.replace('%', '%25').replace('\n', '%0A')))
                else:
                    print(text)

    destination = args.summary
    if destination == '':
        destination = os.environ.get('GITHUB_STEP_SUMMARY')
    if destination and not args.json:
        with open(destination, 'a') as handle:
            handle.write(summary(rows, len(changes)))

    if args.strict and rows:
        print('plan_pins: %d hash pin(s) cover changed files (--strict)' % len(rows), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
