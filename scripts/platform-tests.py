#!/usr/bin/env python3
"""Apple test inventory, portable execution and owned-run receipt validation.

Foundation jobs never launch a simulator or mutate a developer's device set.
The remaining suites have explicit owners/fixture requirements in the manifest.
A receipt is evidence for a specific revision and must report every owned suite;
an absent or failed suite cannot be represented as a successful receipt.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

from platform_test_process import run_bounded

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = Path(__file__).with_suffix('.json')


def inventory(root=ROOT, manifest=MANIFEST):
    document = json.loads(manifest.read_text())
    if document.get('version') != 1:
        raise ValueError('Unsupported platform test manifest version')
    entries = document['apple']
    listed = [entry['source'] for entry in entries]
    actual = {str(path.relative_to(root)) for path in (root / 'apps/native/test').glob('apple-*-tests.*')
              if path.suffix in ('.m', '.swift')}
    if len(listed) != len(set(listed)) or set(listed) != actual:
        raise ValueError(f'Apple inventory mismatch: missing={sorted(actual-set(listed))}, stale={sorted(set(listed)-actual)}; no duplicates allowed')
    for entry in entries:
        if entry['execution'] == 'macos-foundation':
            if not entry.get('frameworks'):
                raise ValueError(f'Missing framework list: {entry["source"]}')
        elif entry['execution'] == 'macos-reading':
            if entry.get('runner') != 'apps/native/scripts/check-apple-reading-stream.sh':
                raise ValueError('Unknown Reading Foundation runner')
        elif entry['execution'] == 'owned-apple':
            if not entry.get('owner') or not entry.get('requirements'):
                raise ValueError(f'Missing owned-run requirements: {entry["source"]}')
        else:
            raise ValueError(f'Unknown test execution mode: {entry["execution"]}')
    return entries


def foundation(entries, output, root=ROOT):
    output.mkdir(parents=True, exist_ok=True)
    results = []
    for entry in entries:
        if entry['execution'] != 'macos-foundation':
            continue
        source = entry['source']
        name = Path(source).stem
        binary = output / name
        command = ['xcrun', 'clang', '-fobjc-arc', '-fblocks']
        for framework in entry['frameworks']:
            command.extend(['-framework', framework])
        command.extend([str(root / source), '-o', str(binary)])
        log = output / (name + '.log')
        with log.open('w') as stream:
            try:
                result = run_bounded(command, root, stream, 120)
                if result == 0:
                    result = run_bounded([str(binary)], root, stream, 60)
            except subprocess.TimeoutExpired:
                stream.write('\nFAIL: native compile/test exceeded its per-suite bound.\n')
                result = 124
            except OSError as error:
                stream.write(f'\nFAIL: {error}\n')
                result = 1
        results.append({'source': source, 'exit': result, 'log': log.name})
        (output / 'results.json').write_text(json.dumps(results, indent=2) + '\n')
        print(f'{"PASS" if result == 0 else "FAIL"} {source}: {log}', flush=True)
    return int(any(result['exit'] for result in results))


def owned_receipts(entries, document, revision, root=ROOT):
    if not re.fullmatch(r'[0-9a-f]{40}', revision) or document.get('revision') != revision:
        raise ValueError('Owned Apple receipts must match the exact reviewed commit')
    required = {entry['source'] for entry in entries if entry['execution'] == 'owned-apple'}
    receipts = document.get('tests', [])
    seen = set()
    for row in receipts:
        source = row.get('source')
        if source not in required or source in seen:
            raise ValueError(f'Unexpected or duplicate owned Apple receipt: {source}')
        seen.add(source)
        if row.get('status') != 'passed' or type(row.get('assertions')) is not int or row['assertions'] < 1:
            raise ValueError(f'No successful assertions recorded: {source}')
        if not row.get('platform') or not row.get('deviceOrSimulatorSet') or not row.get('resultArtifact'):
            raise ValueError(f'Owned runner/receipt evidence is incomplete: {source}')
        digest = hashlib.sha256((root / source).read_bytes()).hexdigest()
        if row.get('testSha256') != digest:
            raise ValueError(f'Owned receipt names different test source: {source}')
    if seen != required:
        raise ValueError(f'Missing owned Apple receipts: {sorted(required-seen)}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['inventory', 'foundation', 'owned-receipts'])
    parser.add_argument('--output', type=Path)
    parser.add_argument('--receipts', type=Path)
    parser.add_argument('--revision')
    args = parser.parse_args()
    entries = inventory()
    if args.mode == 'foundation':
        output = args.output or Path(tempfile.mkdtemp(prefix='portico-apple-tests-'))
        return foundation(entries, output)
    if args.mode == 'owned-receipts':
        if args.receipts is None or args.revision is None:
            parser.error('owned-receipts requires --receipts and --revision')
        owned_receipts(entries, json.loads(args.receipts.read_text()), args.revision)
        print('All owned Apple receipts match the reviewed revision and current test files.')
        return 0
    print(json.dumps(entries, indent=2))
    summary = os.environ.get('GITHUB_STEP_SUMMARY')
    if summary:
        with open(summary, 'a') as stream:
            stream.write('\n### Apple test inventory\n\n')
            for entry in entries:
                stream.write(f'- `{entry["source"]}`: {entry["execution"]}' +
                             (' — requires an owned-run receipt' if entry['execution'] == 'owned-apple' else '') + '\n')
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except (ValueError, KeyError) as error:
        print(f'FAIL: {error}', file=sys.stderr)
        raise SystemExit(1)
