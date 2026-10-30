#!/usr/bin/env python3
import importlib.util
import os
import pathlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

ROOT = pathlib.Path(__file__).resolve().parent.parent

class ReleaseSealTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.key = self.root / 'key'
        self.public = self.root / 'public'
        signer = Ed25519PrivateKey.generate()
        self.key.write_bytes(signer.private_bytes_raw())
        self.key.chmod(0o600)
        self.public.write_bytes(signer.public_key().public_bytes_raw())
        self.files = self.root / 'files'
        self.files.mkdir()
        (self.files / 'package.zip').write_bytes(b'package with web and server')
        (self.files / 'feed.json').write_text('{"channels":{}}')

    def seal(self, verify=False, public=None):
        command = ['python3', str(ROOT / 'scripts/seal-release-artifacts.py'), str(self.files),
                   '--public-key', str(public or self.public), '--source', 'fixture-sha']
        command += ['--verify'] if verify else ['--key', str(self.key)]
        return subprocess.run(command, capture_output=True, text=True)

    def test_final_packages_and_feed_are_authenticated(self):
        self.assertEqual(self.seal().returncode, 0)
        self.key.unlink()
        self.assertEqual(self.seal(verify=True).returncode, 0)
        (self.files / 'package.zip').write_bytes(b'replaced web bytes')
        self.assertNotEqual(self.seal(verify=True).returncode, 0)

    def test_unknown_signer_is_refused(self):
        other = self.root / 'other-public'
        other.write_bytes(Ed25519PrivateKey.generate().public_key().public_bytes_raw())
        self.assertNotEqual(self.seal(public=other).returncode, 0)
        self.assertFalse((self.files / 'release-manifest.json').exists())

    def test_builder_never_creates_a_missing_key(self):
        specification = importlib.util.spec_from_file_location('release_builder', ROOT / 'scripts/build-server-release.py')
        builder = importlib.util.module_from_spec(specification)
        specification.loader.exec_module(builder)
        missing = self.root / 'missing-private-key'
        with patch.dict(os.environ, {'PORTICO_RELEASE_SIGNING_KEY_FILE': str(missing)}, clear=False):
            with self.assertRaises(ValueError):
                builder.signing_key()
        self.assertFalse(missing.exists())

if __name__ == '__main__':
    unittest.main()
