import json
from pathlib import Path
import unittest

from plan import guarded_costs

ROOT = Path(__file__).resolve().parents[1]
PACKAGE = 'github.com/PorticoMediaServer/portico/server/internal/persistence'
TESTS = {'TestProductionStatementsPrepareAgainstTheBaseline': 'statements_prepare_test.go',
         'TestNoInsertOrIgnoreUpdateTriggerOnAnUpsertedTable': 'upsert_trigger_guard_test.go'}


class SourceCensusShort(unittest.TestCase):
    def test_source_censuses_keep_their_full_cost_in_short_plans(self):
        packages = [{'ImportPath': PACKAGE, 'Dir': str(ROOT / 'server/internal/persistence'),
                     'TestGoFiles': list(TESTS.values())}]
        durations = {PACKAGE + '.' + name: 12 for name in TESTS}
        self.assertEqual(guarded_costs(packages, durations), durations)

    def test_short_skip_inventory_does_not_exempt_source_censuses(self):
        skipped = json.loads((ROOT / 'ci/short-skips.json').read_text())['tests']
        self.assertFalse(any(t['package'] == PACKAGE and t['test'] in TESTS for t in skipped))
