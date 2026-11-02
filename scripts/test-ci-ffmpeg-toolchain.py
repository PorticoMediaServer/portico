#!/usr/bin/env python3
"""Refusal boundaries for the first hosted cold toolchain run."""
import importlib.util
from datetime import datetime, timezone
import json
import re
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
    def test_development_native_retry_keeps_original_identity_and_expires(self):
        pin = cold.native_retry_pin('windows-x64', datetime(2026, 11, 2, tzinfo=timezone.utc))
        producer = dict(engineSourceSHA=pin['engineSourceSHA'], runId=41346289584,
                        runAttempt=1, context='github-actions')
        sender = dict(producer, target='windows-x64', constructionPassed=True,
                      qualified=False, release=False, bundleArchiveSHA256='b' * 64)
        self.assertEqual(cold.check_retry_transfer(sender, pin, 'b' * 64), producer)
        for changed in (dict(sender, runId=12), dict(sender, engineSourceSHA=SHA),
                        dict(sender, qualified=True), dict(sender, release=True)):
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                cold.check_retry_transfer(changed, pin, 'b' * 64)
        with self.assertRaises(ValueError): cold.check_retry_transfer(sender, pin, 'c' * 64)
        with self.assertRaises(ValueError): cold.native_retry_pin('linux-x64')
        with self.assertRaises(ValueError):
            cold.native_retry_pin('windows-x64', datetime(2026, 11, 16, tzinfo=timezone.utc))
        with patch.dict('os.environ', {'PORTICO_ENGINE_BUILD_CONTEXT': 'release'}), self.assertRaises(ValueError):
            cold.native_retry('windows-x64', Path('/must-not-be-created'))

    def test_native_plan_skips_failed_construction_but_keeps_successful_targets(self):
        def artifact(target):
            return dict(id=123, name='engine-cold-' + target + '-' + SHA + '-NOT-FOR-USE',
                expired=False, size_in_bytes=100, digest='sha256:' + 'b' * 64,
                workflow_run=dict(id=12, head_sha=SHA))
        result = cold.native_matrix('all', [artifact('windows-x64')], SHA, 'failure', 12)
        self.assertEqual([r['target'] for r in result['matrix']['include']], ['windows-x64'])
        self.assertIn('macos-arm64', result['missingConstruction'])
        self.assertFalse(result['nativeEngineChecks'])
        with self.assertRaisesRegex(ValueError, 'missing its exact artifacts'):
            cold.native_matrix('all', [artifact('windows-x64')], SHA, 'success', 12)
        original = artifact('windows-x64')
        for bad in (dict(original, expired=True), dict(original, workflow_run=dict(id=11, head_sha=SHA)),
                    dict(original, digest='sha256:bad')):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                cold.native_matrix('windows-x64', [bad], SHA, 'success', 12)
        with self.assertRaises(ValueError):
            cold.native_matrix('windows-x64', [original, original], SHA, 'success', 12)

    def test_first_set_has_four_required_targets(self):
        rows = cold.matrix('all')['include']
        self.assertEqual({r['target'] for r in rows if not r['optional']},
                         {'windows-x64', 'linux-x64', 'linux-arm64', 'macos-arm64'})
        self.assertEqual([r['target'] for r in rows if r['optional']], [])
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

    def test_completed_bootstrap_no_longer_admits_push_context(self):
        with patch('subprocess.check_output', return_value=SHA):
            env = dict(environment(), GITHUB_EVENT_NAME='push', GITHUB_REF='refs/heads/rebuild/D-hosted-toolchain-r2')
            with self.assertRaises(ValueError):
                cold.context(env)

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
            self.assertIn('exact-run/target/producer-attempt mismatch', report['failure'])

    def test_oversized_compiler_log_keeps_first_and_terminal_diagnostics(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); state = root / 'state'; state.mkdir()
            log = state / 'dependency-portico-ffmpeg.log'
            content = b'configure start\n' + b'x' * (5 * 1024**2) + b'\nactual fatal compiler error\n'
            log.write_bytes(content)
            (state / 'ignored.bin').write_bytes(b'not a diagnostic')
            (state / 'symlink.log').symlink_to(log)
            evidence = root / 'evidence'
            cold.preserve_diagnostics([state], evidence)
            prefix = evidence / 'custody/state/dependency-portico-ffmpeg.log'
            self.assertFalse(prefix.exists())
            self.assertTrue(prefix.with_name(prefix.name + '.head.log').read_bytes().startswith(b'configure start'))
            self.assertTrue(prefix.with_name(prefix.name + '.tail.log').read_bytes().endswith(b'actual fatal compiler error\n'))
            ranges = json.loads(prefix.with_name(prefix.name + '.ranges.json').read_text())
            self.assertFalse(ranges['complete'])
            self.assertEqual(ranges['originalBytes'], len(content))
            self.assertEqual(ranges['originalSHA256'], cold.digest(log))
            self.assertEqual(ranges['tail'], [len(content) - 1024**2, len(content)])
            self.assertFalse((prefix.parent / 'symlink.log').exists())
            self.assertFalse((prefix.parent / 'ignored.bin').exists())
            workflow = (Path(__file__).resolve().parents[1] / '.github/workflows/ffmpeg-toolchain.yml').read_text()
            patterns = re.findall(r'\$\{\{ runner.temp \}\}/engine-cold/evidence/(\*\*/\*\.[a-z]+)', workflow)
            self.assertTrue(patterns, 'real Actions evidence upload patterns required')
            uploaded = {p for pattern in patterns for p in evidence.glob(pattern) if p.is_file()}
            generated = {p for p in evidence.rglob('*') if p.is_file()}
            self.assertEqual(generated, uploaded, 'retained bytes must actually survive Actions upload')
            self.assertIn(prefix.with_name(ranges['headFile']), uploaded)
            self.assertIn(prefix.with_name(ranges['tailFile']), uploaded)

    def test_disk_samples_retain_peak_pressure_after_space_is_reclaimed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            sampler = cold.DiskSampler(root, root / 'samples.json')
            readings = [SimpleNamespace(free=1000, used=100), SimpleNamespace(free=600, used=500),
                        SimpleNamespace(free=900, used=200)]
            with patch('shutil.disk_usage', side_effect=readings):
                for _ in readings:
                    sampler.observe()
            self.assertEqual(sampler.record['minimumFreeBytes'], 600)
            self.assertEqual(sampler.record['maximumUsedBytes'], 500)
            self.assertEqual(sampler.record['maximumAdditionalUsedBytes'], 400)
            self.assertEqual(sampler.record['samples'], 3)


if __name__ == '__main__':
    unittest.main()
