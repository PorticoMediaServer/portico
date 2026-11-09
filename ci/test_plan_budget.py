import inspect
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


class ShortTierBudget(unittest.TestCase):
    """A promoted PR's full Go inventory must not saturate the short shard cap.

    PRs #1926/#1927 planned ~10890 parents into ten LPT shards, each modeled at
    ~761s: twenty seconds under the 780s `go test -timeout` and certain to time
    out once compile, link and variance are added.
    """

    def writer_inventory(self):
        package = 'github.com/PorticoMediaServer/portico/server/internal/store/writer'
        heavies = {
            'TestCrashReplayInventoryBatchEveryStepWidth8A': 445.0,
            'TestCrashReplayInventoryBatchEveryStepWidth8B': 436.0,
            'TestCrashReplayInventoryBatchEveryStepWidth2': 288.0,
            'TestCrashReplayEnrichAccept': 285.0,
            'TestCrashReplayEffectiveNativeSteps': 231.0,
            'TestCrashReplayProgressComponents': 244.0,
            'TestCrashReplayIndependentReclaimer': 216.0,
            'TestCrashReplayPublisherFragmentAndFlip': 153.0,
            'TestCrashReplayBasicAccept': 196.0,
            'TestCrashReplayBasicResumeCopyEveryStep': 188.0,
            'TestCrashReplayN1DriverPublicPath': 180.0,
            'TestCrashReplayInventoryNativeHistory': 164.0,
        }
        small = {f'TestBatch{index:03d}': 50.0 for index in range(108)}
        durations = {package + '.' + name: seconds
                     for name, seconds in dict(heavies, **small).items()}
        tests = inventory(package, [key.split('.')[-1] for key in durations])
        return tests, durations

    def test_large_inventory_adds_shards_instead_of_stacking_them(self):
        tests, durations = self.writer_inventory()
        self.assertGreater(sum(durations.values()), 8000)
        names = all_names(tests)
        bins, costs = plan.balance_short_large(tests, durations, names)
        rows = plan.go_batch_model(bins, names, durations)
        # (a) the inventory needs more than the old 10-shard (20-shard) cap.
        self.assertGreater(len(bins), 20)
        # (b) every modeled command stays inside the merge budget.
        for row in rows:
            for command in row['commands']:
                self.assertLessEqual(command['seconds'], plan.GO_TEST_BUDGET)
        # (c) only a single over-target parent may push a shard past the target.
        for shard, seconds in zip(bins, costs):
            if seconds > plan.GO_TEST_TARGET:
                self.assertEqual(len(shard), 1)
                self.assertGreater(plan.parent_seconds(shard[0], durations), plan.GO_TEST_TARGET)
        self.assertEqual(costs, [row['seconds'] for row in rows])
        # The two longest parents cannot share a shard with anything.
        for name in ['TestCrashReplayInventoryBatchEveryStepWidth8A',
                     'TestCrashReplayInventoryBatchEveryStepWidth8B']:
            shard = next(shard for shard in bins if any(t['name'] == name for t in shard))
            self.assertEqual(len(shard), 1)

    def test_short_dispatch_promotes_the_large_inventory_to_the_budget_packing(self):
        tests, durations = self.writer_inventory()
        names = all_names(tests)
        self.assertEqual(plan.balance_short(tests, durations, names),
                         plan.balance_short_large(tests, durations, names))
        # A promoted inventory is priced by the measured full table the caller
        # supplies, not by the short baseline that omits most of its parents.
        promoted = dict(durations)
        promoted['github.com/PorticoMediaServer/portico/server/internal/store/writer.TestBatch000'] = 700.0
        self.assertEqual(plan.balance_short(tests, durations, names, large_durations=promoted),
                         plan.balance_short_large(tests, promoted, names))

    def test_split_package_partials_are_limited_by_their_summed_seconds(self):
        package = 'w'
        tests = inventory(package, ['TestA', 'TestB', 'TestC'])
        durations = {package + '.TestA': 250.0, package + '.TestB': 250.0,
                     package + '.TestC': 60.0}
        names = all_names(tests)
        bins, costs = plan.balance_short_large(tests, durations, names)
        rows = plan.go_batch_model(bins, names, durations)
        for row in rows:
            for command in row['commands']:
                self.assertLessEqual(command['seconds'], plan.GO_TEST_BUDGET)
        # 250s each is under the target, but their summed partial command is
        # 500s, so the two parents must land in different shards.
        owners = {t['name']: index for index, shard in enumerate(bins) for t in shard}
        self.assertNotEqual(owners['TestA'], owners['TestB'])

    def test_small_ordinary_plan_keeps_the_ten_shard_lpt_layout(self):
        package = 'p'
        names = [f'Test{index:02d}' for index in range(15)]
        tests = inventory(package, names)
        durations = {package + '.' + name: 100.0 for name in names}
        bins, costs = plan.balance_short(tests, durations, all_names(tests), max_count=10)
        expected = ([[{'module': 'server', 'package': package, 'name': f'Test{index:02d}'},
                      *([{'module': 'server', 'package': package, 'name': f'Test{index + 10:02d}'}]
                        if index < 5 else [])] for index in range(10)],
                    [200.0] * 5 + [100.0] * 5)
        self.assertEqual((bins, costs), expected)


class MergeShardCeiling(unittest.TestCase):
    """The imported trains grow the full inventory; 64 shards no longer fit it."""

    def wide_inventory(self, count):
        names = [f'TestWide{i}' for i in range(count)]
        tests = inventory('p', names)
        durations = {f'p.{name}': 300.0 for name in names}
        # Mark the package split so each parent is its own priced command.
        split = {'p': set(names) | {'TestOther'}}
        return tests, durations, split

    def test_merge_ceiling_is_128(self):
        self.assertEqual(
            inspect.signature(plan.balance_merge).parameters['max_count'].default, 128)

    def test_inventory_needing_65_to_128_shards_plans(self):
        tests, durations, split = self.wide_inventory(120)
        bins, _ = plan.balance_merge(tests, durations, split)
        self.assertGreaterEqual(len(bins), 65)
        self.assertLessEqual(len(bins), 128)
        plan.assert_merge_shard_budget(bins, tests, split, durations)

    def test_inventory_over_128_shards_fails_closed(self):
        tests, durations, split = self.wide_inventory(129)
        with self.assertRaisesRegex(ValueError, 'more than 128 shards'):
            plan.balance_merge(tests, durations, split)


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