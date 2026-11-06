from pathlib import Path
import subprocess
import unittest
from unittest.mock import Mock, call, patch

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
        hosted = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux'}
        environments = [{}, dict(hosted, GITHUB_ACTIONS='false'),
                        dict(hosted, RUNNER_ENVIRONMENT='self-hosted'), dict(hosted, RUNNER_OS='macOS')]
        for env in environments:
            with self.subTest(env=env), patch.dict('os.environ', env, clear=True), \
                 patch.object(sandbox, 'command') as command, \
                 patch.object(sandbox, 'wait_for_package_processes') as wait, \
                 patch.object(sandbox.subprocess, 'run') as run:
                with self.assertRaises(ValueError):
                    sandbox.setup()
                command.assert_not_called()
                wait.assert_not_called()
                run.assert_not_called()

    def test_command_starts_an_isolated_group_and_checks_exit_status(self):
        args = ['sudo', 'apt-get', 'update']
        for returncode in [0, 100]:
            with self.subTest(returncode=returncode), \
                 patch.object(sandbox.subprocess, 'Popen') as popen, \
                 patch.object(sandbox.subprocess, 'run') as kill:
                process = popen.return_value
                process.wait.return_value = returncode
                if returncode:
                    with self.assertRaises(subprocess.CalledProcessError) as raised:
                        sandbox.command(args, timeout=300)
                    self.assertEqual(raised.exception.returncode, returncode)
                    self.assertEqual(raised.exception.cmd, args)
                else:
                    sandbox.command(args, timeout=300)
                popen.assert_called_once_with(args, start_new_session=True)
                process.wait.assert_called_once_with(timeout=300)
                kill.assert_not_called()

    def test_command_kills_privileged_group_and_waits_before_propagating_timeout(self):
        args = ['sudo', 'apt-get', 'install', 'bubblewrap', 'ffmpeg']
        error = subprocess.TimeoutExpired(args, 600)
        with patch.object(sandbox.subprocess, 'Popen') as popen, \
             patch.object(sandbox.subprocess, 'run') as kill:
            process = popen.return_value
            process.pid = 2798
            process.wait.side_effect = [error, -9]
            events = Mock()
            events.attach_mock(process.wait, 'wait')
            events.attach_mock(kill, 'kill')
            with self.assertRaises(subprocess.TimeoutExpired) as raised:
                sandbox.command(args, timeout=600)
            self.assertIs(raised.exception, error)
            popen.assert_called_once_with(args, start_new_session=True)
            self.assertEqual(events.mock_calls, [
                call.wait(timeout=600),
                call.kill(['sudo', 'kill', '-KILL', '--', '-2798'], check=True, timeout=30),
                call.wait(timeout=30),
            ])

    def test_setup_retries_apt_sequence_with_apt_timeouts(self):
        update = call(['sudo', 'apt-get', '-o', 'Acquire::Retries=3',
                       '-o', 'DPkg::Lock::Timeout=60', 'update'],
                      timeout=300)
        install = call(['sudo', 'apt-get', '-o', 'Acquire::Retries=3',
                        '-o', 'DPkg::Lock::Timeout=60', 'install', '-y',
                        '--no-install-recommends', 'bubblewrap', 'ffmpeg'], timeout=600)
        checks = [call(['sudo', 'sysctl', '-w', setting]) for setting in
                  ['kernel.apparmor_restrict_unprivileged_userns=0', 'kernel.unprivileged_userns_clone=1']]
        checks.append(call(['bwrap', '--unshare-all', '--die-with-parent', '--ro-bind', '/', '/',
                            '--proc', '/proc', '--dev', '/dev', '--cap-drop', 'ALL', '/bin/true']))
        env = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux'}
        for error in [subprocess.TimeoutExpired('apt-get', 300),
                      subprocess.TimeoutExpired('apt-get', 600),
                      subprocess.CalledProcessError(1, 'apt-get'),
                      subprocess.CalledProcessError(100, 'apt-get')]:
            for failed_step in [0, 1]:
                with self.subTest(error=type(error).__name__, failed_step=failed_step), \
                     patch.dict('os.environ', env, clear=True), \
                     patch.object(Path, 'exists', return_value=True), \
                     patch.object(sandbox, 'wait_for_package_processes', return_value=True), \
                     patch.object(sandbox, 'command',
                                  side_effect=[None] * failed_step + [error] + [None] * 6) as run, \
                     patch.object(sandbox.time, 'sleep') as sleep:
                    sandbox.setup()
                    self.assertEqual(run.call_args_list,
                                     [update, install][:failed_step + 1]
                                     + [call(['sudo', 'dpkg', '--configure', '-a'], timeout=120)]
                                     + [update, install] + checks)
                    sleep.assert_called_once_with(5)

    def test_setup_stops_after_one_retry_and_does_not_retry_other_failures(self):
        env = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux'}
        for error in [subprocess.TimeoutExpired('setup', 180),
                      subprocess.CalledProcessError(1, 'setup'),
                      subprocess.CalledProcessError(100, 'apt-get'), OSError('setup failed')]:
            for failed_step in range(5):
                retry = failed_step < 2 and not isinstance(error, OSError)
                # A second failure must propagate, even when the first was retried.
                effects = [None] * failed_step + [error] + ([None, error] if retry else [])
                with self.subTest(error=type(error).__name__, failed_step=failed_step), \
                     patch.dict('os.environ', env, clear=True), \
                     patch.object(Path, 'exists', return_value=True), \
                     patch.object(sandbox, 'wait_for_package_processes', return_value=True), \
                     patch.object(sandbox, 'command', side_effect=effects) as run, \
                     patch.object(sandbox.time, 'sleep') as sleep:
                    with self.assertRaises(type(error)):
                        sandbox.setup()
                    self.assertEqual(run.call_count, failed_step + 1 + 2 * int(retry))
                    self.assertEqual(sleep.call_count, int(retry))

    def test_package_process_wait_checks_both_names_and_has_a_deadline(self):
        for codes, clock, expected, sleeps in [
                ([1, 1], [0], True, 0),
                ([0, 1, 1, 1], [0, 1], True, 1),
                ([1, 0], [0, 30], False, 0)]:
            with self.subTest(codes=codes), \
                 patch.object(sandbox.subprocess, 'run', side_effect=[
                     subprocess.CompletedProcess([], code) for code in codes]) as run, \
                 patch.object(sandbox.time, 'monotonic', side_effect=clock), \
                 patch.object(sandbox.time, 'sleep') as sleep:
                self.assertEqual(sandbox.wait_for_package_processes(), expected)
                self.assertEqual(run.call_args_list, [
                    call(['pgrep', '-x', name], stdout=subprocess.DEVNULL, timeout=5)
                    for name in ['apt-get', 'dpkg'] * (len(codes) // 2)])
                self.assertEqual(sleep.call_count, sleeps)

    def test_setup_repairs_only_after_package_processes_exit(self):
        env = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux'}
        for survivor, kill_code in [(False, 0), (True, 0), (True, 1)]:
            with self.subTest(survivor=survivor, kill_code=kill_code), \
                 patch.dict('os.environ', env, clear=True), \
                 patch.object(Path, 'exists', return_value=False), \
                 patch.object(sandbox, 'command', side_effect=[
                     subprocess.TimeoutExpired('apt-get', 600), None, None, None, None]) as command, \
                 patch.object(sandbox, 'wait_for_package_processes',
                              side_effect=[False, True] if survivor else [True]) as wait, \
                 patch.object(sandbox.subprocess, 'run',
                              return_value=subprocess.CompletedProcess([], kill_code)) as kill, \
                 patch.object(sandbox.time, 'sleep'):
                events = Mock()
                for name, mock in [('command', command), ('wait', wait), ('kill', kill)]:
                    events.attach_mock(mock, name)
                sandbox.setup()
                expected = [call.command(command.call_args_list[0].args[0], timeout=300), call.wait()]
                if survivor:
                    expected += [call.kill(['sudo', 'pkill', '-KILL', '-x', name], timeout=30)
                                 for name in ['apt-get', 'dpkg']]
                    expected.append(call.wait())
                expected.append(call.command(['sudo', 'dpkg', '--configure', '-a'], timeout=120))
                self.assertEqual(events.mock_calls[:len(expected)], expected)
                self.assertEqual(command.call_args_list[2], command.call_args_list[0])
                self.assertEqual(kill.call_count, 2 if survivor else 0)

    def test_setup_refuses_retry_when_survivors_or_process_checks_fail(self):
        env = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux'}
        for code, error in [(0, ValueError), (2, subprocess.CalledProcessError)]:
            with self.subTest(code=code), patch.dict('os.environ', env, clear=True), \
                 patch.object(sandbox, 'command', side_effect=subprocess.TimeoutExpired('apt-get', 600)) as command, \
                 patch.object(sandbox, 'wait_for_package_processes', side_effect=[False, False]), \
                 patch.object(sandbox.subprocess, 'run',
                              return_value=subprocess.CompletedProcess([], code)), \
                 patch.object(sandbox.time, 'sleep') as sleep:
                with self.assertRaises(error):
                    sandbox.setup()
                command.assert_called_once()
                sleep.assert_not_called()

    def test_setup_failed_dpkg_repair_prevents_retry(self):
        env = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux'}
        repair = ['sudo', 'dpkg', '--configure', '-a']
        for error in [subprocess.CalledProcessError(1, repair), subprocess.TimeoutExpired(repair, 120)]:
            with self.subTest(error=type(error).__name__), patch.dict('os.environ', env, clear=True), \
                 patch.object(sandbox, 'command', side_effect=[
                     subprocess.TimeoutExpired('apt-get', 600), error]) as command, \
                 patch.object(sandbox, 'wait_for_package_processes', return_value=True), \
                 patch.object(sandbox.time, 'sleep') as sleep:
                with self.assertRaises(type(error)):
                    sandbox.setup()
                self.assertEqual(command.call_count, 2)
                self.assertEqual(command.call_args_list[-1], call(repair, timeout=120))
                sleep.assert_not_called()

    def test_package_process_check_errors_propagate(self):
        with patch.object(sandbox.subprocess, 'run',
                          return_value=subprocess.CompletedProcess(['pgrep'], 2)), \
             self.assertRaises(subprocess.CalledProcessError):
            sandbox.wait_for_package_processes()

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
