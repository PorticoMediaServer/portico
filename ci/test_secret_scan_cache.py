import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SHA256 = '551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb'
CACHE = '${{ runner.temp }}/gitleaks-cache/gitleaks.tar.gz'
PINNED = re.compile(r'^[0-9a-f]{40}$')


def hygiene(name):
    text = (ROOT / '.github/workflows' / name).read_text()
    match = re.search(r'^  hygiene:\n(.*?)(?=^  [\w-]+:\n|\Z)', text, re.M | re.S)
    if match is None:
        raise AssertionError(name + ' has no hygiene job')
    return match[1]


class SecretScanCache(unittest.TestCase):
    def test_every_hygiene_job_caches_the_pinned_tarball(self):
        for name in ['merge.yml', 'nightly.yml']:
            with self.subTest(workflow=name):
                job = hygiene(name)
                self.assertIn('key: gitleaks-8.30.1-' + SHA256, job)
                self.assertIn('path: ' + CACHE, job)
                uses = re.search(r'uses: actions/cache@(\S+)', job)
                self.assertIsNotNone(uses)
                self.assertRegex(uses[1], PINNED)
                self.assertRegex(
                    job,
                    r'- env:\n\s+GITLEAKS_TARBALL_CACHE: ' + re.escape(CACHE) +
                    r'\n\s+run: bash ci/secret-scan\.sh')

    def test_corrupt_cached_tarball_is_removed_and_the_scan_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            cache = Path(directory) / 'gitleaks.tar.gz'
            cache.write_bytes(b'not the pinned gitleaks tarball')
            env = dict(os.environ, GITLEAKS_TARBALL_CACHE=str(cache))
            result = subprocess.run(
                ['bash', str(ROOT / 'ci/secret-scan.sh')],
                cwd=ROOT, env=env, capture_output=True, text=True, timeout=60)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertFalse(cache.exists())
            self.assertIn('cache removed', result.stderr)

    def test_failed_downloads_are_retried_with_backoff(self):
        with tempfile.TemporaryDirectory() as directory:
            scratch = Path(directory)
            bin_dir = scratch / 'bin'
            bin_dir.mkdir()
            log = scratch / 'curl.log'
            (bin_dir / 'curl').write_text(
                '#!/usr/bin/env bash\nprintf "attempt\\n" >> "$CURL_LOG"\nexit 22\n')
            (bin_dir / 'sleep').write_text('#!/usr/bin/env bash\nexit 0\n')
            for script in bin_dir.iterdir():
                script.chmod(0o755)
            env = dict(
                os.environ,
                GITLEAKS_TARBALL_CACHE=str(scratch / 'cache/gitleaks.tar.gz'),
                CURL_LOG=str(log),
                PATH=str(bin_dir) + os.pathsep + os.environ['PATH'])
            result = subprocess.run(
                ['bash', str(ROOT / 'ci/secret-scan.sh')],
                cwd=ROOT, env=env, capture_output=True, text=True, timeout=60)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(log.read_text().count('attempt'), 7)
            self.assertIn('retrying in 60s', result.stderr)


if __name__ == '__main__':
    unittest.main()
