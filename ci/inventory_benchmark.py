"""Require every scheduled inventory benchmark and normalized fixture receipt."""
from collections import Counter
from pathlib import Path
import math
import re
import sys


EXPECTED = Counter((size, operation) for size in ('1000', '1000000')
                   for operation in ('lookup128', 'page128'))

# The existing hosted Go command has -timeout=65m. These are receipt sanity
# bounds, not new runtime budgets; the producer's per-object ceilings stay put.
MAX_ELAPSED_SECONDS = 65 * 60
UNITS = {'ns/op', 'counted-vm/object', 'ns/object', 'stmts/object', 'tx/page'}


def number(token, low, high):
    if not re.fullmatch(r'[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?', token):
        raise ValueError('invalid inventory receipt number')
    value = float(token)
    if not math.isfinite(value) or not low <= value <= high:
        raise ValueError('inventory receipt number outside its valid range')
    return value


def reconcile(text):
    results = Counter()
    for line in text.splitlines():
        if not line.startswith('BenchmarkInventoryUnchangedDatabasePage/'):
            continue
        # Verbose Go output also prints the name when each child starts. Only
        # the completed measurement has its iteration count and metrics.
        fields = line.split()
        if len(fields) == 1:
            continue
        match = re.fullmatch(r'BenchmarkInventoryUnchangedDatabasePage/(\d+)/(lookup128|page128)-\d+', fields[0])
        if not match or fields[1] != '1' or len(fields) != 2 + 2 * len(UNITS):
            raise ValueError('invalid inventory benchmark measurement')
        metrics = dict(zip(fields[3::2], fields[2::2]))
        if set(metrics) != UNITS:
            raise ValueError('missing or duplicate inventory benchmark metric')
        statements, vm = (2, 40) if match[2] == 'lookup128' else (12, 256)
        bounds = {'ns/op': (0, MAX_ELAPSED_SECONDS * 1e9),
                  'ns/object': (0, MAX_ELAPSED_SECONDS * 1e9 / 128),
                  'stmts/object': (0, statements), 'counted-vm/object': (0, vm),
                  'tx/page': (1, 1)}
        for unit, token in metrics.items():
            number(token, *bounds[unit])
        results[match.groups()] += 1
    fixtures = Counter()
    for line in text.splitlines():
        if 'INVENTORY_FIXTURE' not in line:
            continue
        match = re.search(r'INVENTORY_FIXTURE objects=(\d+) distinct_assets=(\d+) physical_sample=(\d+) construction_seconds=(\S+) \(excluded\)\s*$', line)
        if not match:
            raise ValueError('invalid normalized inventory fixture receipt')
        number(match[4], 0, MAX_ELAPSED_SECONDS)
        fixtures[match.groups()[:3]] += 1
    expected_fixtures = Counter((size, size, '128') for size in ('1000', '1000000'))
    if results != EXPECTED or fixtures != expected_fixtures:
        raise ValueError('missing, skipped or duplicate inventory benchmark receipt')


if __name__ == '__main__':
    reconcile(Path(sys.argv[1]).read_text())
