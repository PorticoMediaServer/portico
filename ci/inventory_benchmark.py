"""Require every scheduled inventory benchmark and normalized fixture receipt."""
from collections import Counter
from pathlib import Path
import re
import sys


EXPECTED = Counter((size, operation) for size in ('1000', '1000000')
                   for operation in ('lookup128', 'page128'))


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
        if not match or fields[1] != '1' or not {'ns/op', 'counted-vm/object', 'ns/object', 'stmts/object', 'tx/page'} <= set(fields):
            raise ValueError('invalid inventory benchmark measurement')
        results[match.groups()] += 1
    fixtures = Counter(re.findall(r'INVENTORY_FIXTURE objects=(\d+) distinct_assets=(\d+) physical_sample=(\d+) construction_seconds=', text))
    expected_fixtures = Counter((size, size, '128') for size in ('1000', '1000000'))
    if results != EXPECTED or fixtures != expected_fixtures:
        raise ValueError('missing, skipped or duplicate inventory benchmark receipt')


if __name__ == '__main__':
    reconcile(Path(sys.argv[1]).read_text())
