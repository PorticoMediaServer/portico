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

class CensusPassReceipts(unittest.TestCase):
    def events(self, action):
        name = next(iter(TESTS))
        events = [{'Package': PACKAGE, 'Test': name, 'Action': 'run'}]
        if action is not None:
            events.append({'Package': PACKAGE, 'Test': name, 'Action': action})
        return events + [{'Package': PACKAGE, 'Action': 'pass'}]

    def selection(self):
        test = {'module': 'server', 'package': PACKAGE, 'name': next(iter(TESTS))}
        return {'tier': 'short', 'tags': 'tags', 'packages': [{'module': 'server', 'package': PACKAGE}],
                'inventory': [test], 'all_inventory': [test], 'shards': [[test]], 'js': []}

    def test_runner_rejects_skipped_cancelled_or_missing_census_terminal(self):
        import subprocess, tempfile
        from unittest.mock import patch
        from run import go
        for action in ['skip', 'cancelled', None]:
            with self.subTest(action=action), tempfile.TemporaryDirectory() as directory:
                root = Path(directory); (root / 'ci').mkdir(); (root / 'ci-tools').mkdir()
                (root / 'ci/quarantine.json').write_text('{"entries": []}')
                (root / 'ci-tools/portico-server').touch()
                result = subprocess.CompletedProcess(['go', 'test'], 0,
                    '\n'.join(json.dumps(e) for e in self.events(action)), '')
                with patch('run.ROOT', root), patch('run.validate_tags'), patch('run.subprocess.run', return_value=result):
                    with self.assertRaises(SystemExit):
                        go(self.selection(), 0)

    def test_required_aggregate_checks_raw_census_pass_even_if_job_claims_success(self):
        import tempfile
        from unittest.mock import patch
        from run import aggregate
        needs = {name: {'result': 'success'} for name in ['planner', 'hygiene', 'go_build', 'go_vet_cross', 'go_tests', 'decoder_sandbox']}
        needs.update({name: {'result': 'skipped'} for name in ['js', 'js_static', 'android']})
        for action in ['pass', 'skip', 'cancelled', None]:
            with self.subTest(action=action), tempfile.TemporaryDirectory() as directory:
                root = Path(directory); receipt = root / 'ci-results/go/go-0/census.jsonl'
                receipt.parent.mkdir(parents=True)
                receipt.write_text('\n'.join(json.dumps(e) for e in self.events(action)))
                with patch('run.ROOT', root):
                    if action == 'pass':
                        aggregate(self.selection(), needs)
                        for state in ['skipped', 'cancelled', None]:
                            with self.assertRaises(ValueError):
                                aggregate(self.selection(), dict(needs, go_tests={'result': state}))
                    else:
                        with self.assertRaises(ValueError):
                            aggregate(self.selection(), needs)

    def test_ordinary_environment_skips_keep_their_existing_verdict(self):
        from run import classify
        events = [{'Test': 'TestOptionalHardware', 'Action': 'skip'}, {'Action': 'pass'}]
        self.assertEqual(classify(events, ['TestOptionalHardware'], 0, set()), 'pass')

    def test_both_aggregate_jobs_download_the_exact_raw_go_artifacts(self):
        for filename in ['merge.yml', 'nightly.yml']:
            gate = (ROOT / '.github/workflows' / filename).read_text().split('  merge:\n', 1)[1]
            self.assertIn('pattern: go-*', gate)
            self.assertIn('path: ci-results/go', gate)
            self.assertIn('merge-multiple: false', gate)
