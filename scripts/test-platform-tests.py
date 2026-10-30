import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


platform = module('platform_tests', 'platform-tests.py')
android = module('android_harnesses', 'run-android-harnesses.py')


class PlatformInventoryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.tests = self.root / 'apps/native/test'
        self.tests.mkdir(parents=True)
        self.source = 'apps/native/test/apple-fixture-tests.m'
        (self.root / self.source).write_text('int main(void) { return 0; }\n')
        self.entries = [{'source': self.source, 'execution': 'owned-apple', 'owner': 'fixture', 'requirements': 'isolated simulator'}]
        self.manifest = self.root / 'platform-tests.json'
        self.manifest.write_text(json.dumps({'version': 1, 'apple': self.entries}))

    def test_each_platform_suite_requires_an_explicit_execution_owner(self):
        self.assertEqual(platform.inventory(self.root, self.manifest), self.entries)
        (self.tests / 'apple-new-tests.m').write_text('int main(void) { return 0; }\n')
        with self.assertRaisesRegex(ValueError, 'inventory mismatch'):
            platform.inventory(self.root, self.manifest)

    def test_duplicate_inventory_and_unknown_runner_are_refused(self):
        self.manifest.write_text(json.dumps({'version': 1, 'apple': self.entries * 2}))
        with self.assertRaises(ValueError):
            platform.inventory(self.root, self.manifest)
        self.entries[0]['execution'] = 'unowned'
        self.manifest.write_text(json.dumps({'version': 1, 'apple': self.entries}))
        with self.assertRaisesRegex(ValueError, 'Unknown test execution'):
            platform.inventory(self.root, self.manifest)

    def receipt(self):
        return {'revision': 'a' * 40, 'tests': [{
            'source': self.source, 'status': 'passed', 'assertions': 3,
            'platform': 'ios-simulator', 'deviceOrSimulatorSet': 'disposable-fixture',
            'resultArtifact': 'result.json',
            'testSha256': hashlib.sha256((self.root / self.source).read_bytes()).hexdigest(),
        }]}

    def test_owned_receipts_require_revision_source_and_positive_assertions(self):
        receipt = self.receipt()
        platform.owned_receipts(self.entries, receipt, 'a' * 40, self.root)
        for key, bad in [('status', 'skipped'), ('assertions', 0), ('assertions', True),
                         ('deviceOrSimulatorSet', ''), ('testSha256', 'b' * 64)]:
            with self.subTest(key=key, bad=bad):
                changed = self.receipt()
                changed['tests'][0][key] = bad
                with self.assertRaises(ValueError):
                    platform.owned_receipts(self.entries, changed, 'a' * 40, self.root)
        with self.assertRaises(ValueError):
            platform.owned_receipts(self.entries, receipt, 'b' * 40, self.root)
        with self.assertRaises(ValueError):
            platform.owned_receipts(self.entries, {'revision': 'a' * 40, 'tests': []}, 'a' * 40, self.root)

    def test_compile_failure_is_reported_and_remaining_suites_still_execute(self):
        entries = [{'source': self.source, 'execution': 'macos-foundation', 'frameworks': ['Foundation']},
                   {'source': 'apps/native/test/apple-second-tests.m', 'execution': 'macos-foundation', 'frameworks': ['Foundation']}]
        output = self.root / 'results'
        # First compile fails, second compile and executable succeed.
        answers = [1, 0, 0]
        with patch.object(platform, 'run_bounded', side_effect=answers) as run:
            self.assertEqual(platform.foundation(entries, output, self.root), 1)
        self.assertEqual(run.call_count, 3)
        report = json.loads((output / 'results.json').read_text())
        self.assertEqual([row['exit'] for row in report], [1, 0])
        self.assertEqual(len(list(output.glob('*.log'))), 2)

    def test_android_failure_keeps_other_harnesses_and_all_reports(self):
        for name, text in [('first.py', 'raise SystemExit(7)'), ('second.py', 'import sys\nprint("actual second suite executed", sys.argv[1:])')]:
            (self.root / name).write_text(text)
        output = self.root / 'android-results'
        with patch.object(android, 'HARNESSES', ('first.py', 'second.py')):
            self.assertEqual(android.run(self.root, output), 1)
        report = json.loads((output / 'results.json').read_text())
        self.assertEqual([row['exit'] for row in report], [7, 0])
        second = (output / 'second.log').read_text()
        self.assertIn('actual second suite executed', second)
        self.assertIn("'--scratch', '" + str(output / 'second-scratch') + "'", second)



class OwnedAppleRunnerTests(unittest.TestCase):
    """C03: the owned runner's verdicts and receipts, without a Mac."""

    def setUp(self):
        self.runner = module('run_owned_apple', 'run-owned-apple.py')
        self.platform = module('platform_tests_for_runner', 'platform-tests.py')

    def test_summary_requires_exit_zero_and_explicit_integer_failures(self):
        outcome = self.runner.outcome
        self.assertEqual(outcome('noise\n{"assertions":12,"failures":0}\n', 0), (True, 12))
        for text in ['{"assertions":12,"failures":1}', '{"assertions":0,"failures":0}',
                     '{"assertions":true,"failures":0}', '{"assertions":12}',
                     '{"assertions":12,"failures":false}', 'no summary']:
            self.assertFalse(outcome(text, 0)[0], text)
        self.assertEqual(outcome('{"assertions":12,"failures":0}', 1), (False, 0))

    def test_fields_accept_diagnostics_but_require_every_case(self):
        verdict = {'kind': 'fields', 'true': ['pass'], 'false': ['dropped'], 'cases': 13}
        row = '{"pass":true,"dropped":false,"physicalAcceptance":false}\n'
        self.assertEqual(self.runner.outcome(row * 13, 0, verdict), (True, 26))
        for text in [row * 12, row * 14, row * 12 + '{"pass":false,"dropped":false}',
                     row * 12 + '{"pass":true}', row * 12 + '{"pass":1,"dropped":false}']:
            self.assertFalse(self.runner.outcome(text, 0, verdict)[0])
        self.assertFalse(self.runner.outcome(row * 13, 1, verdict)[0])

    def test_output_matrix_cannot_repeat_one_case_in_place_of_another(self):
        recipe = json.loads(self.runner.RECIPES.read_text())['apps/native/test/apple-audio-output-tests.m']
        verdict = recipe['verdict']
        rows = [dict(row, **{'pass': True}) for row in verdict['expectedCases']]
        self.assertEqual(self.runner.outcome('\n'.join(json.dumps(row) for row in rows), 0, verdict), (True, 13))
        rows[-1] = rows[0]
        self.assertFalse(self.runner.outcome('\n'.join(json.dumps(row) for row in rows), 0, verdict)[0])

    def test_decode_requires_all_pretty_printed_fixture_results_and_album_joins(self):
        files = [f'fixture-{index}.flac' for index in range(7)]
        verdict = {'kind': 'decode', 'files': files}
        document = {'fixtures': [{'file': name, 'status': 'pass'} for name in files],
                    'albums': {'one': {'allTracksPass': True, 'worstBoundaryFrames': 0, 'tracks': 2}}}
        def check():
            return self.runner.outcome('decoder diagnostic\n' + json.dumps(document, indent=2), 0, verdict)
        self.assertEqual(check(), (True, 8))
        document['fixtures'][0]['status'] = 'fail'
        self.assertFalse(check()[0])
        document['fixtures'][0]['status'] = 'pass'
        document['albums']['one']['worstBoundaryFrames'] = 1
        self.assertFalse(check()[0])
        document['albums'] = {}
        document['fixtures'].pop()
        self.assertFalse(check()[0])
        document['fixtures'].append({'file': files[1], 'status': 'pass'})
        self.assertFalse(check()[0])

    def test_plain_text_reader_summary_is_exact(self):
        verdict = {'kind': 'reader'}
        self.assertEqual(self.runner.outcome('Apple reader assets: 36 checks, 0 failures\n', 0, verdict), (True, 36))
        for text in ['Apple reader assets: 35 checks, 0 failures', 'Apple reader assets: 36 checks, 1 failures']:
            self.assertFalse(self.runner.outcome(text, 0, verdict)[0])

    def test_every_owned_suite_has_a_runnable_recipe_and_explicit_verdict(self):
        recipes = json.loads(self.runner.RECIPES.read_text())
        self.assertEqual(set(recipes), set(self.runner.owned_sources()))
        for source, recipe in recipes.items():
            self.assertTrue(recipe.get('runnable'), source)
            self.assertIn('kind', recipe['verdict'])
        self.assertEqual(recipes['apps/native/test/apple-audio-output-tests.m']['verdict']['cases'], 13)

    def test_builds_use_debug_and_real_pods_and_tvos_target(self):
        import tempfile
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            pods, products = work / 'Pods', work / 'Products'
            (pods / 'Headers/Public/React-Core').mkdir(parents=True)
            (products / 'React-Core').mkdir(parents=True)
            (products / 'React-Core/libReact-Core.a').touch()
            recipes = json.loads(self.runner.RECIPES.read_text())
            for source, recipe in recipes.items():
                command, reason = self.runner.plan(source, recipe, work, work, products, pods)
                self.assertIsNone(reason)
                if source.endswith('.m'):
                    self.assertIn('-DDEBUG=1', command['build'])
                if recipe.get('pods'):
                    self.assertIn('-Wl,-force_load,' + str(products / 'React-Core/libReact-Core.a'), command['build'])
                if source.endswith('.swift'):
                    self.assertIn('arm64-apple-tvos15.1-simulator', command['build'])

    def test_simulator_boot_failure_is_registered_for_shutdown_and_delete(self):
        from unittest.mock import patch
        import subprocess
        def checked(argv, **kwargs):
            if 'runtimes' in argv:
                return json.dumps({'runtimes': [{'isAvailable': True, 'version': '27.0', 'identifier': 'com.apple.CoreSimulator.SimRuntime.iOS-27-0'}]})
            if 'devicetypes' in argv:
                return json.dumps({'devicetypes': [{'productFamily': 'iPhone', 'identifier': 'phone'}]})
            if 'create' in argv:
                return 'disposable-id'
            raise subprocess.CalledProcessError(1, argv)
        with patch.object(self.runner, 'checked', side_effect=checked), patch.object(self.runner.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, '', '')) as run:
            simulators = self.runner.Simulators()
            with self.assertRaises(subprocess.CalledProcessError):
                simulators.get('iphonesimulator')
            simulators.close()
            self.assertEqual([call.args[0][2:] for call in run.call_args_list],
                             [['shutdown', 'disposable-id'], ['delete', 'disposable-id']])

    def test_early_dirty_checkout_rejection_removes_all_scratch(self):
        import tempfile
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            with patch.object(self.runner.sys, 'argv', ['runner', '--output', directory]), \
                    patch.object(self.runner, 'checked', return_value='modified'), \
                    patch.object(self.runner, 'Simulators') as simulators:
                with self.assertRaisesRegex(SystemExit, 'uncommitted changes'):
                    self.runner.main()
                simulators.return_value.close.assert_called_once()
            self.assertEqual(list(output.iterdir()), [])

    def test_receipts_list_only_passed_suites_and_the_validator_refuses_an_incomplete_set(self):
        sources = self.runner.owned_sources()
        results = {source: {'status': 'passed', 'assertions': 3} for source in sources}
        revision = 'a' * 40
        document = self.runner.receipt(revision, results, 'macOS 26; iOS simulator', 'disposable', 'owned-apple.log')
        entries = self.platform.inventory()
        self.platform.owned_receipts(entries, document, revision)
        results[sources[0]] = {'status': 'not-run', 'reason': 'no recipe', 'assertions': 0}
        partial = self.runner.receipt(revision, results, 'macOS 26; iOS simulator', 'disposable', 'owned-apple.log')
        self.assertNotIn(sources[0], [row['source'] for row in partial['tests']])
        with self.assertRaisesRegex(ValueError, 'Missing owned Apple receipts'):
            self.platform.owned_receipts(entries, partial, revision)

if __name__ == '__main__':
    unittest.main()
