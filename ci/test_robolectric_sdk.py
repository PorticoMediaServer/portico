import hashlib
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from robolectric_sdk import provision


class RobolectricSdk(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.contents = b'controlled SDK bytes'
        self.sdk = {'version': 'fixture-i7', 'sha512': hashlib.sha512(self.contents).hexdigest()}
        self.target = self.directory / 'android-all-instrumented-fixture-i7.jar'

    def download(self, args, **kwargs):
        Path(args[args.index('--output') + 1]).write_bytes(self.contents)

    def test_checked_download_is_reusable_without_network(self):
        with patch('robolectric_sdk.subprocess.run', side_effect=self.download) as fetch:
            self.assertEqual(provision(self.directory, self.sdk), self.target)
            fetch.assert_called_once()
        with patch('robolectric_sdk.subprocess.run', side_effect=AssertionError('network used')):
            self.assertEqual(provision(self.directory, self.sdk), self.target)

    def test_corrupt_cache_is_refused_before_any_test_or_download(self):
        self.target.write_bytes(b'truncated SDK')
        with patch('robolectric_sdk.subprocess.run', side_effect=AssertionError('network used')):
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                provision(self.directory, self.sdk)

    def test_wrong_download_is_not_published(self):
        self.sdk['sha512'] = '0' * 128
        with patch('robolectric_sdk.subprocess.run', side_effect=self.download):
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                provision(self.directory, self.sdk)
        self.assertEqual(list(self.directory.iterdir()), [])

    def test_failed_transfer_leaves_no_partial_sdk(self):
        def failed(args, **kwargs):
            self.download(args)
            raise subprocess.CalledProcessError(22, args)
        with patch('robolectric_sdk.subprocess.run', side_effect=failed):
            with self.assertRaises(subprocess.CalledProcessError):
                provision(self.directory, self.sdk)
        self.assertEqual(list(self.directory.iterdir()), [])


if __name__ == '__main__':
    unittest.main()
