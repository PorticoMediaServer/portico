import json
from pathlib import Path
import unittest

from plan import guarded_costs, balance, balance_short_censuses

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

class CensusIsolation(unittest.TestCase):
    def test_censuses_get_one_separate_shard_without_reshuffling_other_tests(self):
        census = [{'module': 'server', 'package': PACKAGE, 'name': name} for name in TESTS]
        ordinary = [{'module': 'server', 'package': 'p' + str(i % 3), 'name': 'Test' + str(i)} for i in range(20)]
        tests = ordinary + census
        before = {t['package'] + '.' + t['name']: 20 for t in ordinary}
        before.update({t['package'] + '.' + t['name']: 0 for t in census})
        after = dict(before, **{t['package'] + '.' + t['name']: 12 for t in census})
        baseline, costs = balance(tests, before)
        actual, predictions = balance_short_censuses(tests, after)
        self.assertEqual(actual[:-1], [[t for t in shard if t not in census] for shard in baseline])
        self.assertEqual(predictions[:-1], costs)
        self.assertEqual(actual[-1], census)
        self.assertEqual(predictions[-1], 24)
        self.assertEqual(sum(map(len, actual)), len(tests))

    def test_no_extra_empty_shard_when_no_census_is_selected(self):
        tests = [{'module': 'server', 'package': 'p', 'name': 'TestOrdinary'}]
        self.assertEqual(balance_short_censuses(tests, {}), balance(tests, {}))

    def test_census_only_selection_does_not_leave_an_empty_ordinary_job(self):
        census = [{'module': 'server', 'package': PACKAGE, 'name': name} for name in TESTS]
        bins, costs = balance_short_censuses(census, {})
        self.assertEqual(bins, [census])
        self.assertEqual(costs, [2])
