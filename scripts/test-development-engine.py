#!/usr/bin/env python3
"""The development FFmpeg engine (scripts/development-engine.py), offline, with fake archives."""
import hashlib
import io
import json
import pathlib
import runpy
import subprocess
import tarfile
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent


def tar_xz(members):
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode='w:xz') as t:
        for name, data in members.items():
            info = tarfile.TarInfo(name); info.size = len(data)
            t.addfile(info, io.BytesIO(data))
    return out.getvalue()


def source_tar():
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode='w:gz') as t:
        for name, data in {'jellyfin-ffmpeg-8.1.3-1/COPYING.GPLv3': b'GPL text', 'jellyfin-ffmpeg-8.1.3-1/README': b'r'}.items():
            info = tarfile.TarInfo(name); info.size = len(data)
            t.addfile(info, io.BytesIO(data))
    return out.getvalue()


class Engine(unittest.TestCase):
    def setUp(self):
        self.dir = pathlib.Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.e = runpy.run_path(str(ROOT / 'scripts/development-engine.py'))
        archive = tar_xz({'ffmpeg': b'#!ffmpeg', 'ffprobe': b'#!ffprobe'})
        source = source_tar()
        self.archives = {'fake-linux.tar.xz': archive, self.e['SOURCE'][1]: source}
        pins = self.e['PINS']; pins.clear(); pins['linux-x64'] = ('fake-linux.tar.xz', hashlib.sha256(archive).hexdigest())
        g = self.e['fetch'].__globals__
        g['SOURCE'] = (self.e['SOURCE'][0], self.e['SOURCE'][1], hashlib.sha256(source).hexdigest())
        archives = self.archives

        def fetch_pinned(url, name, sha256, cache):
            data = archives[name]
            if hashlib.sha256(data).hexdigest() != sha256:
                raise SystemExit('pin mismatch')
            return data
        g['fetch_pinned'] = fetch_pinned

    def test_a_bundle_says_development_and_only_its_own_verifier_accepts_it(self):
        out = self.dir / 'engine'
        manifest = self.e['fetch']('linux-x64', out, None)
        self.assertTrue(manifest['development'])
        self.assertEqual(sorted(manifest['files']), ['LICENSES/ffmpeg-GPL-3.0.txt', 'NOTICE.md', 'bin/ffmpeg', 'bin/ffprobe'])
        self.assertEqual((out / 'LICENSES/ffmpeg-GPL-3.0.txt').read_bytes(), b'GPL text')
        self.assertIn('NOT FOR USE', (out / 'NOTICE.md').read_text())
        self.assertEqual(self.e['verify'](out, 'linux-x64')['target'], 'linux-x64')
        # The qualified verifier, and so every sealed release path, refuses it by name.
        result = subprocess.run(['python3', str(ROOT / 'scripts/verify-ffmpeg-manifest.py'), str(out)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('development engine', result.stderr)

    def test_a_changed_or_added_file_is_refused(self):
        out = self.dir / 'engine'
        self.e['fetch']('linux-x64', out, None)
        (out / 'bin/ffmpeg').write_bytes(b'replaced')
        with self.assertRaises(SystemExit):
            self.e['verify'](out)
        (out / 'bin/ffmpeg').write_bytes(b'#!ffmpeg')
        self.e['verify'](out)
        (out / 'bin/extra').write_bytes(b'x')
        with self.assertRaises(SystemExit):
            self.e['verify'](out)

    def test_an_archive_with_anything_else_is_refused(self):
        bad = tar_xz({'ffmpeg': b'a', 'ffprobe': b'b', '../escape': b'c'})
        self.archives['fake-linux.tar.xz'] = bad
        self.e['PINS']['linux-x64'] = ('fake-linux.tar.xz', hashlib.sha256(bad).hexdigest())
        with self.assertRaises(SystemExit):
            self.e['fetch']('linux-x64', self.dir / 'engine', None)

    def test_the_pin_is_checked(self):
        self.e['PINS']['linux-x64'] = ('fake-linux.tar.xz', '0' * 64)
        with self.assertRaises(SystemExit):
            self.e['fetch']('linux-x64', self.dir / 'engine', None)

    def test_real_pins_cover_every_shipped_server_target(self):
        real = runpy.run_path(str(ROOT / 'scripts/development-engine.py'))
        self.assertEqual(sorted(real['PINS']), ['linux-arm64', 'linux-x64', 'macos-arm64', 'windows-arm64', 'windows-x64'])
        for asset, sha in real['PINS'].values():
            self.assertRegex(sha, r'^[0-9a-f]{64}$'); self.assertIn('8.1.3-1', asset)


class BuilderRefusals(unittest.TestCase):
    def setUp(self):
        self.dir = pathlib.Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.builder = runpy.run_path(str(ROOT / 'scripts/build-server-release.py'))

    def test_a_development_engine_needs_an_unsealed_build(self):
        (self.dir / 'toolchain-manifest.json').write_text(json.dumps({'schemaVersion': 1, 'licenseMode': 'gpl3', 'development': True, 'files': {}}))
        with self.assertRaises(SystemExit) as refused:
            self.builder['check_engine'](self.dir, False)
        self.assertIn('--unsealed', str(refused.exception))

    def test_a_signed_manifest_that_says_unsealed_or_development_never_verifies(self):
        from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
        key = Ed25519PrivateKey.generate()
        for manifest in ({'sealed': False}, {'sealed': True, 'engine': {'development': True}}, {}):
            raw = json.dumps(manifest).encode()
            (self.dir / 'manifest.json').write_bytes(raw)
            (self.dir / 'manifest.sig').write_bytes(key.sign(raw))
            with self.assertRaises(ValueError, msg=manifest):
                self.builder['verify_manifest'](self.dir, 'x', key.public_key().public_bytes_raw())


    def test_a_trimpath_binary_carries_its_identity_in_one_proof_marker(self):
        # Go records no -ldflags for a -trimpath build (golang/go#52372); the proof marker carries it.
        import subprocess
        from unittest import mock
        source, build, version, root = 'a' * 64, '20261101T000000Z-abcdef12', '0.0.1', 'R' * 43
        marker = f'PORTICO_RELEASE_V2:{source}:{build}:{version}:{root}:END'.encode()
        artifact = self.dir / 'portico-server'
        info = json.dumps({'Path': 'github.com/PorticoMediaServer/portico/server/cmd/server',
                           'Settings': [{'Key': '-trimpath', 'Value': 'true'}, {'Key': '-tags', 'Value': 'release'}]})
        prefix = 'github.com/PorticoMediaServer/portico/server/internal/buildinfo.'
        with mock.patch.object(subprocess, 'check_output', return_value=info):
            # A marker alone isn't enough: the root must also be in the binary outside it.
            artifact.write_bytes(b'ELF...' + marker + b'...')
            with self.assertRaises(ValueError) as unrooted:
                self.builder['artifact_identity'](artifact)
            self.assertIn('trust root', str(unrooted.exception))
            artifact.write_bytes(b'ELF...' + root.encode() + b'...' + marker + b'...')
            embedded, _ = self.builder['artifact_identity'](artifact)
            self.assertEqual((embedded[prefix + 'SourceDigest'], embedded[prefix + 'BuildID'], embedded[prefix + 'Version']), (source, build, version))
            self.assertEqual(embedded['github.com/PorticoMediaServer/portico/server/internal/hostedtrust.OfficialRoot'], root)
            artifact.write_bytes(root.encode() + marker + marker.replace(b'0.0.1', b'0.0.2'))
            with self.assertRaises(ValueError):
                self.builder['artifact_identity'](artifact)


    def test_the_rclone_merge_takes_only_identical_files_the_bundle_carries(self):
        bundle, staged = self.dir / 'bundle', self.dir / 'stage'
        bundle.mkdir(); staged.mkdir()
        (bundle / 'bundle.lock.json').write_text('lock'); (bundle / 'rclone').write_text('binary')
        (staged / 'bundle.lock.json').write_text('lock')
        self.builder['merge_rclone'](bundle, staged)
        self.assertEqual(sorted(p.name for p in staged.iterdir()), ['bundle.lock.json', 'rclone'])
        (staged / 'bundle.lock.json').write_text('a different lock')
        with self.assertRaises(SystemExit) as differs:
            self.builder['merge_rclone'](bundle, staged)
        self.assertIn('differ', str(differs.exception))
        (staged / 'bundle.lock.json').write_text('lock'); (staged / 'stray').write_text('x')
        with self.assertRaises(SystemExit):
            self.builder['merge_rclone'](bundle, staged)


if __name__ == '__main__':
    unittest.main()
