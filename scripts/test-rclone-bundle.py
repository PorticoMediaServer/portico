#!/usr/bin/env python3
"""Exercise the real pinned payload, build failures and platform selection."""
import copy
import hashlib
import importlib.util
import json
import os
import pathlib
import shutil
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('rclone_bundle', ROOT / 'scripts/rclone-bundle.py')
bundle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bundle)


class RcloneBundleTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory(prefix='portico-2.09a-bundles-')
        cls.base = pathlib.Path(cls.temporary.name)
        cls.cache = pathlib.Path(os.environ.get('PORTICO_RCLONE_TEST_CACHE', ROOT / '.cache/rclone'))
        cls.lock = json.loads((bundle.CONTRACT / 'bundle.lock.json').read_text())
        for target in cls.lock['targets']:
            bundle.prepare(cls.base / target, target, cls.cache)

    @classmethod
    def tearDownClass(cls):
        cls.temporary.cleanup()

    def test_all_six_platform_payloads(self):
        self.assertEqual(set(self.lock['targets']), {o + '-' + a for o in ['linux', 'darwin', 'windows'] for a in ['amd64', 'arm64']})
        for target, item in self.lock['targets'].items():
            with self.subTest(target=target):
                path = self.base / target
                bundle.verify(path, target)
                self.assertTrue((path / item['binary']).is_file())
                self.assertIn('vendor/modules.txt', (path / 'DEPENDENCY-NOTICES.txt').read_text())
                self.assertIn('Permission is hereby granted', (path / 'LICENSE').read_text())

    def test_native_packaged_version(self):
        import platform
        target = {'Linux': 'linux', 'Darwin': 'darwin', 'Windows': 'windows'}[platform.system()] + '-' + {'x86_64': 'amd64', 'AMD64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}[platform.machine()]
        bundle.verify(self.base / target, target, execute=True)

    def test_failure_messages_and_modified_payload(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / 'rclone'
            shutil.copytree(self.base / 'linux-amd64', path)
            binary = path / 'rclone'
            with binary.open('r+b') as stream:
                stream.seek(1000)
                stream.write(b'changed')
            with self.assertRaisesRegex(ValueError, 'Missing or modified rclone payload: rclone'):
                bundle.verify(path, 'linux-amd64')
            # Updating the adjacent manifest must not bless changed distribution bytes.
            manifest = json.loads((path / 'bundle-manifest.json').read_text())
            manifest['files']['rclone'] = bundle.digest(binary)
            (path / 'bundle-manifest.json').write_text(json.dumps(manifest))
            with self.assertRaisesRegex(ValueError, 'does not match the pinned release'):
                bundle.verify(path, 'linux-amd64')

    def test_missing_notice_and_extra_files(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / 'rclone'
            shutil.copytree(self.base / 'linux-amd64', path)
            (path / 'LICENSE').unlink()
            with self.assertRaisesRegex(ValueError, 'inventory changed'):
                bundle.verify(path, 'linux-amd64')
            shutil.copyfile(bundle.CONTRACT / 'LICENSE', path / 'LICENSE')
            (path / 'unapproved').write_text('extra')
            with self.assertRaisesRegex(ValueError, 'inventory changed'):
                bundle.verify(path, 'linux-amd64')

    def test_wrong_platform_and_unsupported_target(self):
        with self.assertRaisesRegex(ValueError, 'target does not match'):
            bundle.verify(self.base / 'linux-amd64', 'linux-arm64')
        with self.assertRaisesRegex(ValueError, 'No bundled rclone for target'):
            bundle.load_target('linux-386')

    def test_tampered_cache_rejected_before_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary)
            name = self.lock['targets']['linux-amd64']['url'].rsplit('/', 1)[1]
            (path / name).write_bytes(b'not the signed archive')
            with self.assertRaisesRegex(ValueError, 'Cached rclone archive checksum mismatch'):
                bundle.prepare(path / 'out', 'linux-amd64', path)
            self.assertFalse((path / 'out').exists())

    def test_signer_is_pinned(self):
        lock = copy.deepcopy(self.lock)
        lock['signingFingerprint'] = '0' * 40
        with self.assertRaisesRegex(ValueError, 'wrong signer'):
            bundle.authenticate(lock)

    def test_packaging_binds_the_release_signature_and_rclone_payload(self):
        from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary)
            shutil.copytree(self.base / 'linux-amd64', path / 'third_party/rclone')
            (path / 'portico-server').write_bytes(b'compiled server fixture')
            key = Ed25519PrivateKey.generate()
            public = path / 'public'
            public.write_bytes(key.public_key().public_bytes_raw())
            manifest = {'component': 'server', 'target': 'linux-amd64', 'artifact': {'path': 'portico-server', 'sha256': bundle.digest(path / 'portico-server')}, 'rcloneManifestSha256': bundle.digest(path / 'third_party/rclone/bundle-manifest.json')}
            raw = json.dumps(manifest).encode()
            (path / 'manifest.json').write_bytes(raw)
            (path / 'manifest.sig').write_bytes(key.sign(raw))
            bundle.verify_release(path, 'linux-amd64', public)
            public.write_bytes(Ed25519PrivateKey.generate().public_key().public_bytes_raw())
            with self.assertRaisesRegex(ValueError, "Release manifest signature does not match the pinned key"):
                bundle.verify_release(path, 'linux-amd64', public)
            public.write_bytes(key.public_key().public_bytes_raw())
            (path / 'portico-server').write_bytes(b'replaced server')
            with self.assertRaisesRegex(ValueError, 'Release server artifact changed'):
                bundle.verify_release(path, 'linux-amd64', public)
            (path / 'portico-server').write_bytes(b'compiled server fixture')
            (path / 'third_party/rclone/bundle-manifest.json').write_bytes(b'replaced rclone manifest')
            with self.assertRaisesRegex(ValueError, 'Release rclone manifest changed'):
                bundle.verify_release(path, 'linux-amd64', public)

    def test_mac_derivative_requires_authenticated_release_context(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / 'rclone'
            shutil.copytree(self.base / 'darwin-arm64', path)
            with (path / 'rclone').open('ab') as stream:
                stream.write(b'platform signature fixture')
            manifest = json.loads((path / 'bundle-manifest.json').read_text())
            manifest['upstreamBinarySha256'] = manifest['files']['rclone']
            manifest['files']['rclone'] = bundle.digest(path / 'rclone')
            (path / 'bundle-manifest.json').write_text(json.dumps(manifest))
            with self.assertRaisesRegex(ValueError, 'does not match the pinned release'):
                bundle.verify(path, 'darwin-arm64')
            bundle.verify(path, 'darwin-arm64', codesigned=True)
            manifest['upstreamBinarySha256'] = '0' * 64
            (path / 'bundle-manifest.json').write_text(json.dumps(manifest))
            with self.assertRaisesRegex(ValueError, 'codesign provenance changed'):
                bundle.verify(path, 'darwin-arm64', codesigned=True)


if __name__ == '__main__':
    unittest.main()
