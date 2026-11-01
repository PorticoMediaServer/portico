import json
from pathlib import Path
import subprocess
import signal
import tempfile
import unittest
from unittest.mock import patch

from race_report import RaceReport, inspect
from run import go


def stream(*events):
    return '\n'.join(json.dumps(e) for e in events) + '\n'


class RaceObserver(unittest.TestCase):
    def test_watchdog_kills_descendants_and_retains_output(self):
        with patch('race_report.subprocess.Popen') as popen, patch('race_report.os.killpg') as kill:
            process = popen.return_value
            process.pid = 123
            process.returncode = -9
            process.communicate.side_effect = [subprocess.TimeoutExpired(['go', 'test'], 900),
                                               ('partial events', 'detector stderr')]
            result, timed_out = RaceReport.run(['go', 'test'], Path('/example'))
            self.assertTrue(popen.call_args.kwargs['start_new_session'])
            kill.assert_called_once_with(123, signal.SIGKILL)
            self.assertTrue(timed_out)
            self.assertEqual((result.stdout, result.stderr), ('partial events', 'detector stderr'))

    def test_only_actual_detector_markers_are_findings(self):
        output = stream({'Package': 'p', 'Test': 'TestA', 'Action': 'output',
                         'Output': 'WARNING: DATA RACE\n'},
                        {'Package': 'p', 'Test': 'TestA', 'Action': 'fail'},
                        {'Package': 'p', 'Action': 'fail'})
        report = inspect(output, '', [('p', 'TestA')], 1)
        self.assertEqual(report['race_findings'], [{'package': 'p', 'test': 'TestA'}])
        self.assertEqual(report['missing_test_terminals'], [])
        self.assertEqual(inspect('', 'WARNING: DATA RACE\nstack\nWARNING: DATA RACE\n', [], 66)
                         ['race_findings'], [{'package': '', 'test': ''}] * 2)
        for text in ['race detected during execution of test', 'example WARNING: DATA RACE',
                     'context deadline exceeded', 'panic: test timed out after 13m']:
            self.assertEqual(inspect(text, '', [], 1)['race_findings'], [])

    def test_deadlines_other_failures_and_incomplete_results_are_separate(self):
        output = stream({'Package': 'p', 'Test': 'TestTimeout', 'Action': 'output',
                         'Output': 'request: context deadline exceeded\n'},
                        {'Package': 'p', 'Test': 'TestTimeout', 'Action': 'fail'},
                        {'Package': 'p', 'Test': 'TestAssertion', 'Action': 'fail'},
                        {'Package': 'p', 'Test': 'TestPassing', 'Action': 'output',
                         'Output': 'expected timeout\n'},
                        {'Package': 'p', 'Test': 'TestPassing', 'Action': 'pass'},
                        {'Package': 'p', 'Test': 'TestUnfinished', 'Action': 'run'},
                        {'Package': 'p', 'Action': 'output',
                         'Output': 'panic: test timed out after 13m0s\n'})
        report = inspect(output, '', [('p', 'TestNeverStarted')], 1, True)
        self.assertEqual(report['race_findings'], [])
        self.assertEqual(report['timing_failures'], [['p', 'TestTimeout']])
        self.assertEqual(report['other_test_failures'], [['p', 'TestAssertion']])
        self.assertEqual(report['package_timeouts'], ['p'])
        self.assertTrue(report['watchdog_timeout'])
        self.assertEqual(report['missing_test_terminals'],
                         [['p', 'TestNeverStarted'], ['p', 'TestUnfinished']])
        self.assertEqual(report['missing_package_terminals'], ['p'])

    def test_same_selection_race_flag_raw_logs_and_continuation_after_timeout(self):
        tests = [{'module': 'server', 'package': 'p', 'name': name}
                 for name in ['TestA', 'TestB']]
        plan = {'tags': 'capability', 'tier': 'nightly', 'all_inventory': tests, 'shards': [tests]}
        output = stream({'Package': 'p', 'Test': 'TestB', 'Action': 'pass'},
                        {'Package': 'p', 'Action': 'pass'})
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'ci-tools').mkdir()
            (root / 'ci-tools/portico-server').touch()
            (root / 'ci').mkdir()
            (root / 'ci/quarantine.json').write_text('{"entries":[]}')
            observer = RaceReport(plan, 0, root / 'race')
            results = [(subprocess.CompletedProcess(['go', 'test'], -9, '', 'watchdog stderr'), True),
                       (subprocess.CompletedProcess(['go', 'test'], 0, output, ''), False)]
            groups = [('server', [('p', [test])], False) for test in tests]
            with patch('run.ROOT', root), patch('run.validate_tags'), patch('run.batches', return_value=groups), \
                 patch.dict('os.environ', {'PORTICO_CI_GO_RACE': '1', 'PORTICO_CI_GO_PACKAGE_JOBS': '4'}), \
                 patch.object(observer, 'run', side_effect=results) as command:
                go(plan, 0, report=observer)
            self.assertEqual(command.call_count, 2)
            for call, name in zip(command.call_args_list, ['TestA', 'TestB']):
                args = call.args[0]
                self.assertIn('-race', args)
                self.assertEqual(args[args.index('-run') + 1], '^(?:' + name + ')$')
                self.assertEqual(args[args.index('-tags') + 1], 'capability')
                self.assertNotIn('-short', args)
            with patch.dict('os.environ', {'GITHUB_STEP_SUMMARY': str(root / 'summary')}):
                observer.finish()
            saved = json.loads((root / 'race/report.json').read_text())
            self.assertTrue(saved['complete'])
            self.assertEqual(saved['selected'], tests)
            self.assertEqual((root / 'race/batch-0.stderr').read_text(), 'watchdog stderr')
            self.assertEqual((root / 'race/batch-1.jsonl').read_text(), output)
            self.assertIn('Race findings (`WARNING: DATA RACE`): 0', (root / 'summary').read_text())

    def test_observer_does_not_relax_required_go_verdict(self):
        tests = [{'module': 'server', 'package': 'p', 'name': 'TestA'}]
        plan = {'tags': 'capability', 'tier': 'merge', 'all_inventory': tests, 'shards': [tests]}
        output = stream({'Package': 'p', 'Test': 'TestA', 'Action': 'output',
                         'Output': 'WARNING: DATA RACE\n'},
                        {'Package': 'p', 'Test': 'TestA', 'Action': 'pass'},
                        {'Package': 'p', 'Action': 'pass'})
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'ci-tools').mkdir()
            (root / 'ci-tools/portico-server').touch()
            (root / 'ci').mkdir()
            (root / 'ci/quarantine.json').write_text('{"entries":[]}')
            result = subprocess.CompletedProcess([], 0, output, '')
            with patch('run.ROOT', root), patch('run.validate_tags'), \
                 patch.dict('os.environ', {}, clear=True), patch('run.subprocess.run', return_value=result) as command:
                with self.assertRaises(SystemExit): go(plan, 0)
                self.assertNotIn('-race', command.call_args.args[0])

    def test_nightly_observer_reuses_matrix_and_is_outside_required_aggregate(self):
        workflow = (Path(__file__).resolve().parents[1] / '.github/workflows/nightly.yml').read_text()
        race = workflow.split('  go_race:\n')[1].split('  js:\n')[0]
        self.assertIn('continue-on-error: true', race)
        self.assertIn('matrix: ${{ fromJSON(needs.planner.outputs.go_matrix) }}', race)
        self.assertIn("PORTICO_CI_GO_RACE: '1'", race)
        self.assertIn("ci/race_report.py --index '${{ matrix.index }}'", race)
        self.assertIn('if: always()', race)
        self.assertNotIn('go_race', workflow.split('  merge:\n')[1])


if __name__ == '__main__':
    unittest.main()
