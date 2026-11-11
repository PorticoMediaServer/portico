import subprocess
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
                                 [call(['go', 'mod', 'download'], run.ROOT / 'server')] * 3 +
                                 [call(['go', 'mod', 'download'], run.ROOT / 'apikit')])
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
        error = subprocess.CalledProcessError(100, ['sudo', 'apt-get', 'update'])
        install = ['sudo', 'apt-get', 'install', '-y', '--no-install-recommends', 'ffmpeg']
        with patch.dict('os.environ', {}, clear=True), \
                patch('run.execute', side_effect=[error, error, error, None, None]) as execute, \
                patch('run.time.sleep') as sleep:
            run.install_ffmpeg()
            self.assertEqual(execute.call_args_list,
                             [call(install)] + [call(['sudo', 'apt-get', 'update'], run.ROOT)] * 3 +
                             [call(install)])
            self.assertEqual(sleep.call_args_list, [call(2), call(2)])
