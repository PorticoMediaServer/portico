import json
import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch

import plan
import update_merge_durations


def inventory(package, names):
    return [{'module': 'server', 'package': package, 'name': name} for name in names]


def all_names(tests):
    result = {}
    for test in tests:
        result.setdefault(test['package'], set()).add(test['name'])
    return result


class MergeShardBudget(unittest.TestCase):
    def heavy_package(self):
        # A writer-sized package: a few crash-replay parents dominate, the rest
        # are sub-second. Without measured weights these packed into one batch.
        names = ['TestLongA', 'TestLongB', 'TestLongC'] + [f'TestSmall{i}' for i in range(120)]
        durations = {f'w.{name}': 0.2 for name in names}
        durations.update({'w.TestLongA': 445.0, 'w.TestLongB': 436.0, 'w.TestLongC': 288.0})
        return inventory('w', names), durations

    def test_weighted_split_keeps_every_batch_under_the_budget(self):
        tests, durations = self.heavy_package()
        bins, costs = plan.balance_merge(tests, durations, all_names(tests))
        self.assertEqual(sorted((t['package'], t['name']) for b in bins for t in b),
                         sorted((t['package'], t['name']) for t in tests))
        rows = plan.assert_merge_shard_budget(bins, tests, all_names(tests), durations)
        for row in rows:
            for command in row['commands']:
                limit = plan.GO_TEST_DEADLINE if command['tests'] == 1 else plan.GO_TEST_BUDGET
                self.assertLessEqual(command['seconds'], limit)
        self.assertGreaterEqual(len(bins), 3)  # the two longest parents cannot share
        for name in ['TestLongA', 'TestLongB']:
            shard = next(b for b in bins if any(t['name'] == name for t in b))
            self.assertEqual(len(shard), 1)

    def test_plan_is_deterministic(self):
        tests, durations = self.heavy_package()
        first = plan.balance_merge(tests, durations, all_names(tests))
        second = plan.balance_merge(tests, durations, all_names(tests))
        self.assertEqual(first, second)

    def test_unknown_parent_uses_the_conservative_default(self):
        self.assertEqual(plan.UNMEASURED_PARENT_SECONDS, 20.0)
        self.assertEqual(plan.parent_seconds({'package': 'p', 'name': 'TestNew'}, {}), 20.0)
        tests = inventory('p', ['Test' + str(i) for i in range(60)])
        bins, _ = plan.balance_merge(tests, {}, all_names(tests))
        rows = plan.assert_merge_shard_budget(bins, tests, all_names(tests), {})
        self.assertTrue(all(row['seconds'] <= plan.GO_TEST_BUDGET for row in rows))

    def test_measured_table_overrides_baseline_for_full_tiers_only(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'ci').mkdir()
            (root / 'ci/merge_durations.json').write_text(json.dumps({'p.TestSlow': 300.0}))
            with patch('plan.ROOT', root):
                self.assertEqual(plan.observed_durations({'p.TestSlow': 1.0}, 'merge')['p.TestSlow'], 300.0)
                self.assertEqual(plan.observed_durations({'p.TestSlow': 1.0}, 'nightly')['p.TestSlow'], 300.0)
                self.assertEqual(plan.observed_durations({'p.TestSlow': 1.0}, 'short')['p.TestSlow'], 1.0)
            (root / 'ci/merge_durations.json').unlink()
            with patch('plan.ROOT', root):
                self.assertEqual(plan.observed_durations({'p.TestSlow': 1.0}, 'merge')['p.TestSlow'], 1.0)

    def test_over_budget_batch_and_duplicate_are_rejected(self):
        tests = inventory('p', ['TestA', 'TestB'])
        durations = {'p.TestA': 300.0, 'p.TestB': 300.0}
        split = {'p': {'TestA', 'TestB', 'TestOther'}}  # the package is split
        with self.assertRaisesRegex(ValueError, 'over its'):
            plan.assert_merge_shard_budget([tests], tests, split, durations)
        with self.assertRaisesRegex(ValueError, 'duplicates'):
            plan.assert_merge_shard_budget([tests[:1], tests[:1]], tests, split, durations)
        with self.assertRaisesRegex(ValueError, 'exactly once'):
            plan.assert_merge_shard_budget([tests[:1]], tests, split, durations)

    def test_single_parent_over_budget_is_reported_not_failed(self):
        tests = inventory('p', ['TestHuge'])
        durations = {'p.TestHuge': 505.0}
        split = {'p': {'TestHuge', 'TestOther'}}  # the package is split around it
        bins = [tests]
        rows = plan.assert_merge_shard_budget(bins, tests, split, durations)
        self.assertEqual(rows[0]['seconds'], 505.0)
        self.assertEqual(plan.oversized_single_parents(bins, split, durations),
                         [{'package': 'p', 'seconds': 505.0}])
        with self.assertRaisesRegex(ValueError, '780s limit'):
            plan.assert_merge_shard_budget(bins, tests, split, {'p.TestHuge': 781.0})


class MergeDurationImport(unittest.TestCase):
    def test_longest_parent_across_runs_wins_and_subtests_roll_up(self):
        events = [
            {'Package': 'p', 'Test': 'TestA', 'Action': 'pass', 'Elapsed': 2.0},
            {'Package': 'p', 'Test': 'TestA/sub', 'Action': 'pass', 'Elapsed': 1.5},
            {'Package': 'p', 'Test': 'TestB', 'Action': 'pass', 'Elapsed': 4.0},
        ]
        later = [{'Package': 'p', 'Test': 'TestA', 'Action': 'fail', 'Elapsed': 9.0}]
        with tempfile.TemporaryDirectory() as directory:
            first = Path(directory)/'go-0'; first.mkdir()
            (first/'p.jsonl').write_text('\n'.join(json.dumps(e) for e in events)+'\n')
            second = Path(directory)/'go-1'; second.mkdir()
            (second/'p.jsonl').write_text('\n'.join(json.dumps(e) for e in later)+'\n')
            table = {}
            update_merge_durations.read_results(first, table)
            update_merge_durations.read_results(second, table)
        self.assertEqual(table, {'p.TestA': 9.0, 'p.TestB': 4.0})


if __name__ == '__main__':
    unittest.main()