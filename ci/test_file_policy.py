import importlib.util
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / 'scripts/check-repo-binaries.py'
spec = importlib.util.spec_from_file_location('binary_policy', SCRIPT)
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)


def sparse_file(root, name, size, header=b'data'):
    path = root / name
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('wb') as stream:
        stream.write(header)
        stream.truncate(size)


class FilePolicyTests(unittest.TestCase):
    def test_exact_size_boundaries(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            for size in [policy.NOTICE_LIMIT - 1, policy.NOTICE_LIMIT, policy.NOTICE_LIMIT + 1,
                         policy.LIMIT - 1, policy.LIMIT, policy.LIMIT + 1]:
                with self.subTest(size=size):
                    sparse_file(root, 'asset.bin', size)
                    error, notice = policy.check_file(root, 'asset.bin')
                    self.assertEqual(error is not None, size > 50 * 1024 * 1024)
                    self.assertEqual(notice is not None, size > 1024 * 1024)

    def test_former_exemptions_follow_both_thresholds(self):
        names = ['fixtures/asset.bin', 'third_party/asset.bin', 'packages/contracts/src/types.ts',
                 'packages/contracts/src/schemas.ts', 'server/receiver/cast/contracts.js',
                 'server/api/portico.openapi.json', 'server/internal/persistence/migrations/0001_baseline.sql',
                 'server/internal/mediaexec/fonts/NotoSansCJKjp-Regular.otf']
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            for name in names:
                with self.subTest(name=name):
                    sparse_file(root, name, policy.NOTICE_LIMIT + 1)
                    error, notice = policy.check_file(root, name)
                    self.assertIsNone(error)
                    self.assertIsNotNone(notice)
                    sparse_file(root, name, policy.LIMIT + 1)
                    self.assertIsNotNone(policy.check_file(root, name)[0])

    def test_every_executable_magic_is_still_rejected_at_any_size(self):
        headers = [bytes.fromhex(v) for v in ['7f454c46', 'cffaedfe', 'cefaedfe', 'feedfacf', 'feedface', 'cafebabe']] + [b'MZ00']
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            for header in headers:
                for size in [4, policy.LIMIT + 1]:
                    with self.subTest(header=header, size=size):
                        sparse_file(root, 'fixtures/program', size, header)
                        error, _ = policy.check_file(root, 'fixtures/program')
                        self.assertEqual(error, 'built executable in Git: fixtures/program')

    def test_missing_tracked_file_is_ignored(self):
        with tempfile.TemporaryDirectory() as folder:
            self.assertEqual(policy.check_file(Path(folder), 'removed.bin'), (None, None))

    def fixture(self, root):
        script = root / 'scripts/check-repo-binaries.py'
        script.parent.mkdir()
        shutil.copyfile(SCRIPT, script)
        subprocess.run(['git', 'init', '-q'], cwd=root, check=True)
        return script

    def test_cli_notices_pass_and_append_safe_job_summary_in_both_modes(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            script = self.fixture(root)
            name = 'asset %<name>\nfile.bin'
            sparse_file(root, name, policy.NOTICE_LIMIT + 1)
            sparse_file(root, 'untracked-program', 4, b'\x7fELF')
            subprocess.run(['git', 'add', '--', name], cwd=root, check=True)
            summary = root / 'summary.md'
            summary.write_text('Existing step summary\n')
            env = dict(os.environ, GITHUB_STEP_SUMMARY=str(summary))
            for args in [[], ['--staged']]:
                result = subprocess.run([sys.executable, str(script), *args], env=env, text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn('::notice::large tracked file: asset %25<name>%0Afile.bin', result.stdout)
                self.assertIn('passed for 1 tracked paths', result.stdout)
                self.assertEqual(result.stderr, '')
            text = summary.read_text()
            self.assertTrue(text.startswith('Existing step summary\n'))
            self.assertEqual(text.count('### Tracked files over 1 MiB'), 2)
            self.assertIn('asset %&lt;name&gt;\nfile.bin', text)

    def test_cli_hard_limit_fails_but_still_reports_notices(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            script = self.fixture(root)
            sparse_file(root, 'large.bin', policy.LIMIT + 1)
            subprocess.run(['git', 'add', 'large.bin'], cwd=root, check=True)
            summary = root / 'summary.md'
            result = subprocess.run([sys.executable, str(script)], text=True, capture_output=True,
                                    env=dict(os.environ, GITHUB_STEP_SUMMARY=str(summary)))
            self.assertEqual(result.returncode, 1)
            self.assertIn('large.bin', result.stderr)
            self.assertIn('limit 52428800', result.stderr)
            self.assertIn('::notice::large tracked file: large.bin', result.stdout)
            self.assertIn('large.bin', summary.read_text())

    def test_local_notice_without_step_summary(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            script = self.fixture(root)
            sparse_file(root, 'asset.bin', policy.NOTICE_LIMIT + 1)
            subprocess.run(['git', 'add', 'asset.bin'], cwd=root, check=True)
            env = dict(os.environ)
            env.pop('GITHUB_STEP_SUMMARY', None)
            result = subprocess.run([sys.executable, str(script), '--staged'], env=env, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('::notice::large tracked file:', result.stdout)
