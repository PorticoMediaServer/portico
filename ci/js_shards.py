"""Deterministic JS file allocation and fail-closed execution receipts."""
from collections import Counter
import hashlib
import json
import math
from pathlib import Path

SHARD_COUNTS = {'apps/native': 4, 'packages': 3, 'web': 2, 'apps/desktop': 1, 'apps/vega': 1}


def roots(files):
    return sorted({'/'.join(Path(f).parts[:2]) if Path(f).parts[0] in ('apps', 'packages')
                   else Path(f).parts[0] for f in files})


def balance_files(files, durations, count):
    if len(files) != len(set(files)):
        raise ValueError('duplicate JS selection')
    if not files:
        raise ValueError('empty JS selection')
    measured = list(durations.values())
    if any(isinstance(v, bool) or not isinstance(v, (int, float)) or not math.isfinite(v) or v < 0 for v in measured):
        raise ValueError('invalid JS duration')
    measured.sort()
    if not isinstance(count, int) or count < 1: raise ValueError('invalid JS shard count')
    fallback = measured[math.ceil(len(measured) * .9) - 1] if measured else 30.0
    fallback = max(1.0, fallback)
    costs = {f: max(.001, durations.get(f, fallback)) for f in files}
    bins = [[] for _ in range(min(count, len(files)))]; seconds = [0.0] * len(bins)
    for file in sorted(files, key=lambda f: (-costs[f], f)):
        index = min(range(len(bins)), key=lambda i: (seconds[i], i))
        bins[index].append(file); seconds[index] += costs[file]
    return bins, seconds


def extend_selection(items, durations):
    for item in items:
        component = item['component']; files = item['files']
        item['static_roots'] = roots(files) if component != 'website' else ['website']
        item['unmeasured_files'] = [f for f in files if f not in durations]
        if component == 'website':
            item['shards'], item['predicted_seconds'] = [], []
        else:
            item['shards'], item['predicted_seconds'] = balance_files(files, {f:v for f,v in durations.items() if f.startswith(component+'/')}, SHARD_COUNTS[component])
    validate_selection(items)
    return items


def validate_selection(items):
    components = [x['component'] for x in items]
    if len(components) != len(set(components)):
        raise ValueError('duplicate JS component')
    all_files = []
    for item in items:
        files = item['files']; shards = item['shards']
        expected_roots = roots(files) if item['component'] != 'website' else ['website']
        if item['static_roots'] != expected_roots:
            raise ValueError('static roots differ from original selection')
        if item['component'] == 'website':
            if shards:
                raise ValueError('website must run its check once')
        elif not files or not shards or Counter(files) != Counter(f for shard in shards for f in shard) or any(not shard for shard in shards):
            raise ValueError('JS shard union differs from selection')
        if len(files) != len(set(files)):
            raise ValueError('duplicate JS selection')
        all_files.extend(files)
        for file in files:
            if Path(file).is_absolute() or '..' in Path(file).parts:
                raise ValueError('invalid selected JS path')
    if len(all_files) != len(set(all_files)):
        raise ValueError('JS file has multiple component owners')


def digest(plan):
    return hashlib.sha256(json.dumps(plan, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()).hexdigest()


def receipt_name(component, index):
    return component.replace('/', '-') + '-' + str(index) + '.json'


def reconcile(plan, directory):
    validate_selection(plan['js'])
    expected = {(item['component'], i): files for item in plan['js'] for i, files in enumerate(item['shards'])}
    actual = {}
    for path in sorted(Path(directory).rglob('*.json')):
        record = json.loads(path.read_text())
        key = (record.get('component'), record.get('index'))
        if type(record.get('index')) is not int or key not in expected or key in actual:
            raise ValueError('unexpected or duplicate JS shard receipt: ' + str(key))
        if record.get('version') != 1 or record.get('head') != plan['head'] or record.get('plan_sha256') != digest(plan):
            raise ValueError('stale or invalid JS receipt: ' + str(key))
        files = expected[key]
        if record.get('assigned') != files or record.get('returncode') != 0:
            raise ValueError('JS shard failed or assignment differs: ' + str(key))
        terminals = record.get('files', [])
        if Counter(f.get('file') for f in terminals) != Counter(files):
            raise ValueError('missing, duplicate or unexpected JS file terminal: ' + str(key))
        for result in terminals:
            duration = result.get('seconds')
            if result.get('status') != 'pass' or isinstance(duration, bool) or not isinstance(duration, (int, float)) or not math.isfinite(duration) or duration < 0:
                raise ValueError('JS file did not pass: ' + str(result.get('file')))
        actual[key] = record
    if set(actual) != set(expected):
        raise ValueError('missing JS shard receipts: ' + str(sorted(set(expected) - set(actual))))
    return actual
