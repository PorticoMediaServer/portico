from pathlib import Path
import unittest
from unittest.mock import patch

from run import aggregate
import decoder_sandbox as sandbox


class DecoderSandboxTests(unittest.TestCase):
    def events(self, action='pass'):
        events = []
        for package, tests in sandbox.REQUIRED.items():
            for name in tests:
                events += [{'Package': package, 'Test': name, 'Action': 'run'},
                           {'Package': package, 'Test': name, 'Action': action}]
            events.append({'Package': package, 'Action': 'pass'})
        return events

    def test_real_sandbox_and_pool_tests_must_pass(self):
        sandbox.verify(self.events(), 0)
        for action in ['skip', 'fail']:
            with self.subTest(action=action), self.assertRaises(ValueError):
                sandbox.verify(self.events(action), 0)

    def test_missing_test_package_terminal_and_tool_failure_block(self):
        events = self.events()
        for broken in [events[:-1], events[2:], []]:
            with self.assertRaises(ValueError):
                sandbox.verify(broken, 0)
        with self.assertRaises(ValueError):
            sandbox.verify(events, 1)

    def test_new_tests_cannot_disappear_or_fail_silently(self):
        events = self.events()
        extra = {'Package': next(iter(sandbox.REQUIRED)), 'Test': 'TestNewSandbox', 'Action': 'run'}
        with self.assertRaises(ValueError):
            sandbox.verify(events + [extra], 0)
        with self.assertRaises(ValueError):
            sandbox.verify(events + [extra, dict(extra, Action='fail')], 0)

    def test_setup_refuses_non_hosted_machines_before_any_command(self):
        with patch.dict('os.environ', {}, clear=True), patch.object(sandbox, 'command') as command:
            with self.assertRaises(ValueError):
                sandbox.setup()
            command.assert_not_called()

    def test_required_aggregate_rejects_missing_or_failed_sandbox_job(self):
        plan = {'packages': [{}], 'js': []}
        needs = {name: {'result': 'success'} for name in
                 ['planner', 'hygiene', 'go_build', 'go_vet_cross', 'go_tests', 'decoder_sandbox']}
        needs.update({name: {'result': 'skipped'} for name in ['js', 'js_static', 'android']})
        with patch('run.reconcile'):
            aggregate(plan, needs)
            for state in ['failure', 'cancelled', 'skipped', None]:
                with self.subTest(state=state), self.assertRaises(ValueError):
                    aggregate(plan, dict(needs, decoder_sandbox={'result': state}))
            missing = dict(needs)
            del missing['decoder_sandbox']
            with self.assertRaises(ValueError):
                aggregate(plan, missing)

    def test_both_required_gates_depend_on_hosted_sandbox_job(self):
        root = Path(__file__).resolve().parents[1]
        for file in ['merge.yml', 'nightly.yml']:
            text = (root / '.github/workflows' / file).read_text()
            self.assertIn('uses: ./.github/workflows/decoder-sandbox.yml', text)
            needs = [line for line in text.splitlines() if 'needs: [' in line and 'go_tests' in line]
            self.assertEqual(len(needs), 1)
            self.assertIn('decoder_sandbox', needs[0])


if __name__ == '__main__':
    unittest.main()
