#!/usr/bin/env python3
"""Refusal boundaries for the first hosted cold toolchain run."""
import importlib.util
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


def module(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + '.py'))
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


cold = module('ci-ffmpeg-toolchain')
engine = module('build-engine')
native = module('verify-native-engine')
SHA = 'a' * 40


def environment():
    return dict(GITHUB_ACTIONS='true', RUNNER_ENVIRONMENT='github-hosted',
        GITHUB_REPOSITORY='PorticoMediaServer/portico', GITHUB_EVENT_NAME='workflow_dispatch',
        GITHUB_REF='refs/heads/topic', GITHUB_WORKFLOW_REF=
        'PorticoMediaServer/portico/.github/workflows/ffmpeg-toolchain.yml@refs/heads/topic',
        GITHUB_SHA=SHA, GITHUB_RUN_ID='12', GITHUB_RUN_ATTEMPT='1')


class ColdToolchain(unittest.TestCase):
    def test_first_set_has_four_required_targets_and_optional_intel(self):
        rows = cold.matrix('all')['include']
        self.assertEqual({r['target'] for r in rows if not r['optional']},
                         {'windows-x64', 'linux-x64', 'linux-arm64', 'macos-arm64'})
        self.assertEqual([r['target'] for r in rows if r['optional']], ['macos-x64'])
        self.assertEqual(cold.matrix('linux-arm64')['include'][0]['nativeRunner'], 'ubuntu-24.04-arm')
        with self.assertRaises(ValueError):
            cold.matrix('windows-arm64')

    def test_wrong_host_repo_workflow_ref_event_or_commit_refuses(self):
        with patch('subprocess.check_output', return_value=SHA + '\n'):
            self.assertEqual(cold.context(environment())['runId'], 12)
            for key, value in [('RUNNER_ENVIRONMENT', 'self-hosted'), ('GITHUB_REPOSITORY', 'fork/portico'),
                               ('GITHUB_EVENT_NAME', 'pull_request_target'), ('GITHUB_REF', 'refs/tags/v1'),
                               ('GITHUB_WORKFLOW_REF', 'PorticoMediaServer/portico/.github/workflows/release.yml@refs/heads/main'),
                               ('GITHUB_SHA', 'b' * 40), ('GITHUB_RUN_ATTEMPT', '0')]:
                with self.subTest(key=key), self.assertRaises(ValueError):
                    cold.context(dict(environment(), **{key: value}))

    def test_bootstrap_push_is_one_exact_branch_only(self):
        with patch('subprocess.check_output', return_value=SHA):
            env = dict(environment(), GITHUB_EVENT_NAME='push', GITHUB_REF='refs/heads/rebuild/D-hosted-toolchain-r2')
            cold.context(env)
            with self.assertRaises(ValueError):
                cold.context(dict(env, GITHUB_REF='refs/heads/main'))

    def test_hosted_windows_producer_bypasses_no_guard_through_local_lock_flag(self):
        with patch('subprocess.check_output', return_value=SHA), patch('platform.system', return_value='Linux'), patch('platform.machine', return_value='x86_64'):
            self.assertEqual(engine.execution_context('github-actions', env=environment())['sourceSHA'], SHA)
            with self.assertRaises(ValueError):
                engine.execution_context('github-actions', env={'PORTICO_ENGINE_LOCKED': '1'})
            with self.assertRaises(ValueError):
                engine.execution_context('local', env=environment())
        with patch('platform.system', return_value='Linux'), patch('platform.machine', return_value='aarch64'):
            with self.assertRaises(ValueError):
                engine.execution_context('github-actions', env=environment())

    def test_linux_native_gate_checks_architecture_and_exact_workflow(self):
        with patch('platform.system', return_value='Linux'), patch('platform.machine', return_value='aarch64'):
            native.native('linux-arm64', 'github-actions', environment())
            with self.assertRaises(ValueError):
                native.native('linux-x64', 'github-actions', environment())
            with self.assertRaises(ValueError):
                native.native('linux-arm64', 'github-actions', dict(environment(), GITHUB_WORKFLOW_REF='other'))
            with self.assertRaises(ValueError):
                native.native('linux-arm64', 'github-actions', dict(environment(), PORTICO_SIGNING_KEY_FILE=''))

    def test_native_transfer_from_another_run_never_reaches_extraction_or_gates(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); transfer = root / 'transfer'; transfer.mkdir()
            sender = dict(engineSourceSHA=SHA, runId=11, runAttempt=1, context='github-actions',
                          target='linux-x64', constructionPassed=True, qualified=False, release=False)
            (transfer / 'transfer.json').write_text(json.dumps(sender))
            def guard(name):
                self.assertEqual(name, 'verify-native-engine')
                return SimpleNamespace(native=lambda *args: None)
            expected = dict(engineSourceSHA=SHA, runId=12, runAttempt=1, context='github-actions')
            with patch.object(cold, 'context', return_value=expected), patch.object(cold, 'module', side_effect=guard):
                self.assertEqual(cold.native('linux-x64', transfer, root / 'evidence'), 1)
            report = json.loads((root / 'evidence/cold-native.json').read_text())
            self.assertFalse(report['nativeEngineChecks'])
            self.assertIn('exact-run/target/hash mismatch', report['failure'])


if __name__ == '__main__':
    unittest.main()
