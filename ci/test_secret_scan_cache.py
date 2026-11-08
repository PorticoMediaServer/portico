import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'ci/secret-scan.sh'
SHA256 = '551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb'
CACHE = '${{ runner.temp }}/gitleaks-cache/gitleaks.tar.gz'
PINNED = re.compile(r'^[0-9a-f]{40}$')
SETUP_GO = 'actions/setup-go@d35c59abb061a4a6fb18e82ac0862c26744d6ab5'
UNREACHABLE_TARBALL = 'file:///nonexistent/gitleaks.tar.gz'


def hygiene(name):
    text = (ROOT / '.github/workflows' / name).read_text()
    match = re.search(r'^  hygiene:\n(.*?)(?=^  [\w-]+:\n|\Z)', text, re.M | re.S)
    if match is None:
        raise AssertionError(name + ' has no hygiene job')
    return match[1]


def stub(directory, name, body):
    path = Path(directory) / name
    path.write_text(body)
    path.chmod(0o755)
    return path


def run_scan(bin_dir, env_extra=None):
    env = dict(os.environ)
    env.pop('GITLEAKS_TARBALL_CACHE', None)
    env['GITLEAKS_TARBALL_URL'] = UNREACHABLE_TARBALL
    env['PATH'] = str(bin_dir) + os.pathsep + os.environ['PATH']
    if env_extra:
        env.update(env_extra)
    return subprocess.run(
        ['bash', str(SCRIPT)], cwd=ROOT, env=env,
        capture_output=True, text=True, timeout=60)


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

    def test_every_hygiene_job_installs_the_pinned_go_before_the_scan(self):
        for name in ['merge.yml', 'nightly.yml']:
            with self.subTest(workflow=name):
                job = hygiene(name)
                self.assertRegex(
                    job,
                    r'- uses: ' + re.escape(SETUP_GO) + r'\n\s+with:\n'
                    r'\s+go-version-file: server/go\.mod\n\s+cache: false\n')
                self.assertLess(
                    job.index(SETUP_GO), job.index('bash ci/secret-scan.sh'),
                    'setup-go must run before the secret scan')

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
            self.assertNotIn('Go module fallback', result.stderr)

    def test_failed_downloads_are_retried_with_backoff(self):
        with tempfile.TemporaryDirectory() as directory:
            scratch = Path(directory)
            bin_dir = scratch / 'bin'
            bin_dir.mkdir()
            log = scratch / 'curl.log'
            stub(bin_dir, 'curl',
                 '#!/usr/bin/env bash\nprintf "attempt\\n" >> "$CURL_LOG"\nexit 22\n')
            stub(bin_dir, 'sleep', '#!/usr/bin/env bash\nexit 0\n')
            stub(bin_dir, 'go',
                 '#!/usr/bin/env bash\n'
                 'if [[ "${1:-}" == env && "${2:-}" == GOVERSION ]]; then echo go1.0.0; fi\n'
                 'exit 0\n')
            env = {'CURL_LOG': str(log)}
            result = run_scan(bin_dir, env)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(log.read_text().count('attempt'), 7)
            self.assertIn('retrying in 60s', result.stderr)
            self.assertIn('using verified Go module fallback', result.stderr)
            self.assertIn('pinned Go toolchain', result.stderr)

    def test_successful_download_never_reaches_the_fallback(self):
        with tempfile.TemporaryDirectory() as directory:
            scratch = Path(directory)
            bin_dir = scratch / 'bin'
            bin_dir.mkdir()
            stub(bin_dir, 'curl', (
                '#!/usr/bin/env bash\n'
                'out=\n'
                'while (( $# )); do\n'
                '  if [[ "$1" == -o ]]; then out=$2; shift 2; else shift; fi\n'
                'done\n'
                'printf "not the pinned tarball" > "$out"\n'
                'exit 0\n'))
            result = run_scan(bin_dir)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn('failed sha256 verification', result.stderr)
            self.assertNotIn('Go module fallback', result.stderr)

    def test_fallback_runs_only_after_the_retry_window_is_exhausted(self):
        text = SCRIPT.read_text()
        loop_start = text.index('while :; do')
        loop_end = text.index('\nfi\n', text.index('  done\n', loop_start)) + len('\nfi\n')
        self.assertIn('tarball_ready=true', text[loop_start:loop_end])
        install = text.index('go install github.com/zricethezav/gitleaks/v8@v8.30.1')
        self.assertGreater(install, loop_end, 'fallback install must follow the retry loop')
        self.assertEqual(text.count('go_fallback'), 2, 'fallback has one definition and one call')
        self.assertRegex(
            text,
            r'(?s)if \[\[ "\$tarball_ready" == true \]\]; then.*?'
            r'else\n\s+go_fallback\n\s+gitleaks_bin="\$scratch/gobin/gitleaks"\nfi',
        )

    def test_fallback_environment_stays_public_and_verified(self):
        text = SCRIPT.read_text()
        self.assertIn('GOPROXY=https://proxy.golang.org\n', text)
        self.assertNotRegex(text, r'GOPROXY[=:]\s*\S*direct')
        self.assertIn('GOTOOLCHAIN=local', text)
        self.assertIn('GOWORK=off', text)
        self.assertIn('GOBIN="$scratch/gobin"', text)
        self.assertIn(
            'unset GOFLAGS GONOSUMDB GONOSUMCHECK GOPRIVATE GONOPROXY GOINSECURE', text)
        self.assertNotRegex(text, r'GOSUMDB\s*=\s*off')
        self.assertIn(
            'go mod download -json github.com/zricethezav/gitleaks/v8@v8.30.1', text)
        self.assertIn('"$scratch/gobin/gitleaks" version', text)
        self.assertIn('go version -m "$scratch/gobin/gitleaks"', text)
        self.assertIn(
            '"$gitleaks_bin" dir . --config .gitleaks.toml --redact --no-banner', text)

    def test_fallback_installs_and_scans_with_the_verified_module_binary(self):
        with tempfile.TemporaryDirectory() as directory:
            bin_dir = Path(directory) / 'bin'
            bin_dir.mkdir()
            stub(bin_dir, 'go', '\n'.join([
                '#!/usr/bin/env bash',
                'case "${1:-}" in',
                '  env) echo go1.26.10 ;;',
                '  mod) printf \'{"Version":"v8.30.1","Sum":"h1:test-sum"}\\n\' ;;',
                '  install)',
                '    mkdir -p "$GOBIN"',
                '    printf \'#!/usr/bin/env bash\\n'
                'if [[ "${1:-}" == version ]]; then echo "version is set by build process"; fi\\n'
                'exit 0\\n\' > "$GOBIN/gitleaks"',
                '    chmod 755 "$GOBIN/gitleaks" ;;',
                '  version) printf \'\\tmod\\tgithub.com/zricethezav/gitleaks/v8\\tv8.30.1\\th1:test-sum\\n\' ;;',
                'esac',
                'exit 0',
                '']) + '\n')
            stub(bin_dir, 'sleep', '#!/usr/bin/env bash\nexit 0\n')
            result = run_scan(bin_dir)
            self.assertEqual(result.returncode, 0, result.stderr)
            notice = ('gitleaks tarball unavailable after retries; '
                      'using verified Go module fallback')
            self.assertIn(notice, result.stderr)
            self.assertIn('gitleaks module version: v8.30.1', result.stderr)
            self.assertIn('gitleaks module sum: h1:test-sum', result.stderr)
            self.assertIn('gitleaks fallback binary module version: v8.30.1', result.stderr)

    def test_fallback_rejects_a_binary_built_from_another_module_version(self):
        with tempfile.TemporaryDirectory() as directory:
            bin_dir = Path(directory) / 'bin'
            bin_dir.mkdir()
            stub(bin_dir, 'go', '\n'.join([
                '#!/usr/bin/env bash',
                'case "${1:-}" in',
                '  env) echo go1.26.10 ;;',
                '  mod) printf \'{"Version":"v8.30.1","Sum":"h1:test-sum"}\\n\' ;;',
                '  install)',
                '    mkdir -p "$GOBIN"',
                '    printf \'#!/usr/bin/env bash\\nexit 0\\n\' > "$GOBIN/gitleaks"',
                '    chmod 755 "$GOBIN/gitleaks" ;;',
                '  version) printf \'\\tmod\\tgithub.com/zricethezav/gitleaks/v8\\tv8.29.0\\th1:old\\n\' ;;',
                'esac',
                'exit 0',
                '']) + '\n')
            stub(bin_dir, 'sleep', '#!/usr/bin/env bash\nexit 0\n')
            result = run_scan(bin_dir)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn('is not the pinned v8.30.1 module', result.stderr)

    def test_fallback_without_go_fails_loudly(self):
        with tempfile.TemporaryDirectory() as directory:
            bin_dir = Path(directory) / 'bin'
            bin_dir.mkdir()
            for name in ['bash', 'curl', 'mktemp', 'cp', 'rm', 'mkdir', 'dirname',
                         'sha256sum', 'tar', 'awk', 'sed', 'git', 'cat', 'python3']:
                resolved = shutil.which(name)
                if resolved is None:
                    self.skipTest('host lacks ' + name)
                (bin_dir / name).symlink_to(resolved)
            stub(bin_dir, 'sleep', '#!/usr/bin/env bash\nexit 0\n')
            env = dict(os.environ)
            env.pop('GITLEAKS_TARBALL_CACHE', None)
            env['GITLEAKS_TARBALL_URL'] = UNREACHABLE_TARBALL
            env['PATH'] = str(bin_dir)
            result = subprocess.run(
                ['bash', str(SCRIPT)], cwd=ROOT, env=env,
                capture_output=True, text=True, timeout=60)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn('needs Go on PATH', result.stderr)

    def test_fallback_with_a_mismatched_go_fails_loudly(self):
        with tempfile.TemporaryDirectory() as directory:
            bin_dir = Path(directory) / 'bin'
            bin_dir.mkdir()
            stub(bin_dir, 'sleep', '#!/usr/bin/env bash\nexit 0\n')
            stub(bin_dir, 'go',
                 '#!/usr/bin/env bash\n'
                 'if [[ "${1:-}" == env && "${2:-}" == GOVERSION ]]; then echo go1.25.7; fi\n'
                 'exit 0\n')
            result = run_scan(bin_dir)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn('using verified Go module fallback', result.stderr)
            self.assertIn('go1.26.10', result.stderr)
            self.assertIn('go1.25.7', result.stderr)
            self.assertNotIn('gitleaks module version', result.stderr)


if __name__ == '__main__':
    unittest.main()
