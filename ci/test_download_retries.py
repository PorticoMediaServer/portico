import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import call, patch

import run


class DownloadRetries(unittest.TestCase):
    def test_a_transient_download_can_succeed_on_the_third_attempt(self):
        for error in (subprocess.CalledProcessError(1, ['go', 'mod', 'download']),
                      subprocess.TimeoutExpired(['go', 'mod', 'download'], 900)):
            with self.subTest(error=type(error).__name__), \
                    patch('run.execute', side_effect=[error, error, None, None]) as execute, \
                    patch('run.time.sleep') as sleep:
                run.download()
                self.assertEqual(execute.call_args_list,
                                 [call(['go', 'mod', 'download'], run.ROOT / 'apikit')] * 3 +
                                 [call(['go', 'mod', 'download'], run.ROOT / 'server-next')])
                self.assertEqual(sleep.call_args_list, [call(2), call(2)])

    def test_three_failures_still_fail_the_job(self):
        error = subprocess.CalledProcessError(1, ['go', 'mod', 'download'])
        with patch('run.execute', side_effect=error) as execute, patch('run.time.sleep') as sleep:
            with self.assertRaises(subprocess.CalledProcessError) as raised:
                run.download()
            self.assertIs(raised.exception, error)
            self.assertEqual(execute.call_count, 3)
            self.assertEqual(sleep.call_args_list, [call(2), call(2)])

    def test_the_package_list_refresh_retries_without_changing_the_install_fallback(self):
        error = subprocess.CalledProcessError(100, ['sudo', 'apt-get', 'update', '-o', 'Acquire::Retries=1'])
        install = ['sudo', 'apt-get', 'install', '-y', '--no-install-recommends', '-o', 'Acquire::Retries=1', 'ffmpeg']
        with patch.dict('os.environ', {}, clear=True), \
                patch('run.execute', side_effect=[error, error, error, None, None]) as execute, \
                patch('run.time.sleep') as sleep:
            run.install_ffmpeg()
            self.assertEqual(execute.call_args_list,
                             [call(install)] + [call(['sudo', 'apt-get', 'update', '-o', 'Acquire::Retries=1'], run.ROOT)] * 3 +
                             [call(install)])
            self.assertEqual(sleep.call_args_list, [call(2), call(2)])

    def test_module_prefetch_does_not_need_a_selection_plan(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            go = root / 'go'
            go.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$FETCH_LOG"\n')
            go.chmod(0o755)
            log = root / 'fetch.log'
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'], FETCH_LOG=str(log))
            result = subprocess.run([sys.executable, str(run.ROOT / 'ci/run.py'), 'download',
                                     '--plan', str(root / 'no-plan.json')], env=env, cwd=root,
                                    text=True, capture_output=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(log.read_text().splitlines(), ['mod download', 'mod download'])
