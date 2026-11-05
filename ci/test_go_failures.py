import contextlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

from run import FAILURE_STATUSES, FailureOutput, classify, go, run_go


def events(action='pass'):
    return [{'Package': 'p', 'Test': 'TestA', 'Action': 'run'},
            {'Package': 'p', 'Test': 'TestA', 'Action': action},
            {'Package': 'p', 'Action': action}]


class Classification(unittest.TestCase):
    def test_failure_categories(self):
        cases = [
            ('assertion', events('fail'), 1, {}, 'test_failed'),
            ('subtest', [{'Test': 'TestA/child', 'Action': 'fail'}] + events('fail'), 1, {}, 'test_failed'),
            ('test timeout', [{'Output': 'panic: test timed out after 13m\n'}], 1, {}, 'timeout'),
            ('watchdog', [], -9, {'timed_out': True}, 'timeout'),
            ('panic', [{'Output': 'panic: broken\n'}] + events('fail'), 1, {}, 'panic_or_race'),
            ('fatal', [{'Output': 'fatal error: deadlock\n'}], 2, {}, 'panic_or_race'),
            ('race', [{'Output': 'WARNING: DATA RACE\n'}] + events(), 0, {}, 'panic_or_race'),
            ('stderr race', [], 66, {'stderr': 'WARNING: DATA RACE\n'}, 'panic_or_race'),
            ('build event', [{'Action': 'build-fail'}], 1, {}, 'build_failed'),
            ('build output', [{'Output': 'FAIL\tp [build failed]\n'}], 1, {}, 'build_failed'),
            ('compiler stderr', [], 1, {'stderr': '# p\na.go:1: undefined: x\n'}, 'build_failed'),
            ('missing package', events()[:-1], 0, {}, 'incomplete_events'),
            ('missing expected', [{'Action': 'pass'}], 0, {}, 'incomplete_events'),
            ('unfinished child', events() + [{'Test': 'TestA/child', 'Action': 'run'}], 0, {}, 'incomplete_events'),
            ('malformed stream', events(), 0, {'incomplete': True}, 'incomplete_events'),
            ('killed', events(), -9, {}, 'infrastructure'),
            ('tool failure', events(), 2, {}, 'infrastructure'),
            ('inconsistent exit', events('fail'), 0, {}, 'infrastructure'),
            ('unexpected package', events(), 0, {'infrastructure': True}, 'infrastructure'),
        ]
        for label, stream, code, options, want in cases:
            with self.subTest(label=label):
                self.assertEqual(classify(stream, ['TestA'], code, set(), **options), want)
        self.assertEqual(set(want for *_, want in cases), FAILURE_STATUSES)

    def test_quarantine_and_mandatory_pass_contracts_stay_strict(self):
        self.assertEqual(classify(events('fail'), ['TestA'], 1, {'TestA'}), 'quarantined')
        for options in ({'timed_out': True}, {'incomplete': True}, {'infrastructure': True}):
            self.assertIn(classify(events('fail'), ['TestA'], 1, {'TestA'}, **options), FAILURE_STATUSES)
        self.assertEqual(classify(events('skip'), ['TestA'], 0, set()), 'pass')
        for action in ('fail', 'skip', 'canceled'):
            self.assertIn(classify(events(action), ['TestA'], 1 if action == 'fail' else 0,
                                   {'TestA'}, required_pass={'TestA'}), FAILURE_STATUSES)
        for text in ('panic: expected timeout fixture', 'timed out', 'build failed logged', 'example WARNING: DATA RACE'):
            self.assertEqual(classify([{'Test': 'TestA', 'Output': text}] + events(), ['TestA'], 0, set()), 'pass')

    def test_every_category_gates_without_retry(self):
        cases = [
            (events('fail'), 1, '', False),
            ([], -9, '', True),
            ([{'Output': 'WARNING: DATA RACE'}] + events(), 0, '', False),
            ([{'Action': 'build-fail', 'Package': 'p'}], 1, '', False),
            ([{'Action': 'build-fail', 'ImportPath': 'dependency'}], 1, '', False),
            (events()[:-1], 0, '', False),
            (events(), 2, 'go: tool unavailable', False),
        ]
        tests = [{'module': 'server', 'package': 'p', 'name': 'TestA'}]
        plan = {'tags': 'capability', 'tier': 'merge', 'shards': [tests], 'all_inventory': tests}
        for stream, code, stderr, timeout in cases:
            with self.subTest(stream=stream, code=code), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / 'ci-tools').mkdir(); (root / 'ci-tools/portico-server').touch()
                (root / 'ci').mkdir(); (root / 'ci/quarantine.json').write_text('{"entries": []}')
                result = subprocess.CompletedProcess([], code, '\n'.join(map(json.dumps, stream)), stderr)
                result.timed_out = timeout
                with patch('run.ROOT', root), patch('run.validate_tags'), patch('run.run_go', return_value=result) as run, contextlib.redirect_stdout(io.StringIO()):
                    with self.assertRaises(SystemExit) as error: go(plan, 0)
                    self.assertEqual(error.exception.code, 1)
                    run.assert_called_once()


class LiveFailureReporting(unittest.TestCase):
    def test_failed_subtest_keeps_its_assertion_output(self):
        reporter = FailureOutput()
        with contextlib.redirect_stdout(io.StringIO()) as output:
            for event in [{'Package': 'p', 'Test': 'TestA/child', 'Output': '    x_test.go:42: got 3, want 4\n'},
                          {'Package': 'p', 'Test': 'TestA/child', 'Action': 'fail'}]:
                reporter.feed(json.dumps(event) + '\n')
            self.assertIn('p TestA/child failed:', output.getvalue())
            self.assertIn('x_test.go:42: got 3, want 4', output.getvalue())

    def test_build_events_print_compiler_output_by_import_path(self):
        reporter = FailureOutput()
        with contextlib.redirect_stdout(io.StringIO()) as output:
            reporter.feed(json.dumps({'ImportPath': 'dependency', 'Action': 'build-output', 'Output': 'x.go:3: undefined: symbol\n'}))
            reporter.feed(json.dumps({'ImportPath': 'dependency', 'Action': 'build-fail'}))
            self.assertIn('dependency failed:\nx.go:3: undefined: symbol', output.getvalue())

    def test_watchdog_keeps_budget_and_kills_process_group(self):
        process = Mock(pid=123, returncode=-9)
        process.stdout = iter(())
        # Use a closeable stdout as Popen does.
        process.stdout = io.StringIO('')
        timer = Mock()
        def make_timer(seconds, expire):
            self.assertEqual(seconds, 900)
            timer.start.side_effect = expire
            return timer
        with patch('run.subprocess.Popen', return_value=process), patch('run.threading.Timer', side_effect=make_timer), patch('run.os.killpg') as kill:
            result = run_go(['go', 'test'], Path('/example'))
        import signal
        kill.assert_called_once_with(123, signal.SIGKILL)
        self.assertTrue(result.timed_out)
        self.assertEqual(classify([], ['TestA'], result.returncode, set(), timed_out=result.timed_out), 'timeout')
        timer.cancel.assert_called_once()
        timer.join.assert_called_once()

    def test_process_reports_failure_before_reading_later_events(self):
        with contextlib.redirect_stdout(io.StringIO()) as output:
            def stream():
                yield json.dumps({'Package': 'p', 'Test': 'TestA', 'Output': 'assertion evidence\n'}) + '\n'
                yield json.dumps({'Package': 'p', 'Test': 'TestA', 'Action': 'fail'}) + '\n'
                self.assertIn('p TestA failed:\nassertion evidence', output.getvalue())
                yield json.dumps({'Package': 'sibling', 'Action': 'pass'}) + '\n'
            process = Mock(pid=123, returncode=1)
            process.stdout = Mock()
            process.stdout.__iter__ = Mock(side_effect=stream)
            with patch('run.subprocess.Popen', return_value=process) as popen:
                result = run_go(['go', 'test', '-json'], Path('/example'))
            self.assertTrue(popen.call_args.kwargs['start_new_session'])
            self.assertEqual(result.returncode, 1)
            self.assertEqual(len(result.stdout.splitlines()), 3)
            self.assertFalse(result.timed_out)
            process.stdout.close.assert_called_once()


if __name__ == '__main__':
    unittest.main()
