import json
import tempfile
from pathlib import Path
import unittest

import refresh_go_durations as refresh
import update_merge_durations


HEAD = 'a' * 40
OTHER = 'b' * 40


def run(identifier, created, conclusion='success', status='completed'):
    return {'id': identifier, 'created_at': created, 'conclusion': conclusion, 'status': status}


class RunSelection(unittest.TestCase):
    def test_successful_runs_are_newest_first(self):
        runs = [run(1, '2026-11-09T07:00:00Z'), run(3, '2026-11-09T09:00:00Z'),
                run(2, '2026-11-09T08:00:00Z'), run(4, '2026-11-09T10:00:00Z', conclusion='failure'),
                run(5, '2026-11-09T11:00:00Z', status='in_progress'),
                run(6, '2026-11-09T12:00:00Z', conclusion='cancelled')]
        self.assertEqual([item['id'] for item in refresh.successful_runs(runs)], [4, 3, 2, 1])

    def test_newest_run_with_live_go_artifacts_wins(self):
        runs = [run(1, '2026-11-09T09:00:00Z'), run(2, '2026-11-09T08:00:00Z')]
        artifacts = {
            1: [{'name': 'js-web-0', 'expired': False}],
            2: [{'name': 'go-3', 'expired': False}, {'name': 'go-4', 'expired': True}],
        }
        selected = refresh.newest_go_run(runs, lambda identifier: artifacts[identifier])
        self.assertEqual(selected['id'], 2)
        self.assertEqual([item['name'] for item in refresh.go_artifacts(artifacts[2])], ['go-3'])

    def test_failed_hygiene_run_with_passing_go_tests_is_usable(self):
        runs = [run(7, '2026-11-09T09:00:00Z', conclusion='failure')]
        artifacts = {7: [{'name': 'go-0', 'expired': False}]}
        jobs = {7: [{'name': 'hygiene', 'conclusion': 'failure'},
                    {'name': 'go_tests (0)', 'conclusion': 'success'}]}
        selected = refresh.newest_go_run(runs, lambda identifier: artifacts[identifier],
                                         lambda identifier: jobs[identifier])
        self.assertEqual(selected['id'], 7)

    def test_failed_go_tests_run_is_rejected(self):
        runs = [run(8, '2026-11-09T09:00:00Z', conclusion='failure')]
        artifacts = {8: [{'name': 'go-0', 'expired': False}]}
        jobs = {8: [{'name': 'go_tests (0)', 'conclusion': 'failure'}]}
        self.assertIsNone(refresh.newest_go_run(runs, lambda identifier: artifacts[identifier],
                                                lambda identifier: jobs[identifier]))

    def test_head_without_passing_go_artifacts_fails_clearly(self):
        runs = [run(1, '2026-11-09T09:00:00Z')]
        artifacts = {1: [{'name': 'js-web-0', 'expired': False}]}
        with self.assertRaisesRegex(refresh.RefreshError,
                                    r'PR #1915 \(' + HEAD + r'\): no completed run whose go_tests passed'):
            refresh.resolve_runs([('PR #1915', HEAD)],
                                 lambda head: runs,
                                 lambda identifier: artifacts[identifier],
                                 lambda identifier: [])

    def test_every_target_resolves_to_its_own_run(self):
        runs = {HEAD: [run(10, '2026-11-09T09:00:00Z')],
                OTHER: [run(20, '2026-11-09T08:00:00Z')]}
        artifacts = {10: [{'name': 'go-0', 'expired': False}],
                     20: [{'name': 'go-1', 'expired': False}]}
        resolved = refresh.resolve_runs([('PR #1915', HEAD), ('PR #1921', OTHER)],
                                        lambda head: runs[head],
                                        lambda identifier: artifacts[identifier],
                                        lambda identifier: [])
        self.assertEqual([(label, item['id']) for label, _, item in resolved],
                         [('PR #1915', 10), ('PR #1921', 20)])


class TableMerge(unittest.TestCase):
    def test_existing_parents_survive_and_only_the_longest_time_changes(self):
        events = [
            {'Package': 'p', 'Test': 'TestKeep', 'Action': 'pass', 'Elapsed': 1.0},
            {'Package': 'p', 'Test': 'TestGrow', 'Action': 'pass', 'Elapsed': 7.0},
            {'Package': 'p', 'Test': 'TestNew', 'Action': 'pass', 'Elapsed': 3.0},
        ]
        with tempfile.TemporaryDirectory() as directory:
            results = Path(directory) / 'go-0'
            results.mkdir()
            (results / 'p.jsonl').write_text('\n'.join(json.dumps(event) for event in events) + '\n')
            before = {'p.TestKeep': 2.0, 'p.TestGrow': 2.0}
            table = dict(before)
            update_merge_durations.read_results(results, table)
            added, changed = refresh.summarize(before, table)
        self.assertEqual(table, {'p.TestKeep': 2.0, 'p.TestGrow': 7.0, 'p.TestNew': 3.0})
        self.assertEqual(added, ['p.TestNew'])
        self.assertEqual(changed, ['p.TestGrow'])

    def test_written_table_is_sorted_and_round_trips(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'merge_durations.json'
            output.write_text('{"z.TestZ": 1.0, "a.TestA": 2.0}\n')
            loaded = update_merge_durations.load_table(output)
            data = update_merge_durations.write_table(loaded, output)
            self.assertEqual(list(data), ['a.TestA', 'z.TestZ'])
            self.assertEqual(output.read_text(), '{\n "a.TestA": 2.0,\n "z.TestZ": 1.0\n}\n')
            self.assertEqual(update_merge_durations.load_table(output), data)


class ModeledShards(unittest.TestCase):
    def test_saved_inventory_is_priced_with_the_refreshed_table(self):
        tests = [{'module': 'server', 'package': 'p', 'name': 'TestOne'},
                 {'module': 'server', 'package': 'p', 'name': 'TestTwo'}]
        payload = {'inventory': tests, 'all_inventory': tests}
        with tempfile.TemporaryDirectory() as directory:
            plan_file = Path(directory) / 'ci-plan.json'
            plan_file.write_text(json.dumps(payload))
            durations_file = Path(directory) / 'durations.json'
            durations_file.write_text(json.dumps({'p.TestOne': 4.0, 'p.TestTwo': 4.0}))
            count = refresh.modeled_shard_count(plan_file, {'p.TestOne': 5.0}, durations_file)
        self.assertEqual(count, 1)

    def test_missing_inventory_lists_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            plan_file = Path(directory) / 'ci-plan.json'
            plan_file.write_text('{}')
            with self.assertRaisesRegex(refresh.RefreshError, 'not a planner output'):
                refresh.modeled_shard_count(plan_file, {}, Path(directory) / 'durations.json')


if __name__ == '__main__':
    unittest.main()
