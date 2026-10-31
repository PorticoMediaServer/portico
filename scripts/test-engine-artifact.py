#!/usr/bin/env python3
"""Exercise real pinned intake with disposable archives; no engine qualification."""
from copy import deepcopy
from datetime import datetime, timezone, timedelta
import io
import json
from contextlib import redirect_stdout
from pathlib import Path, PureWindowsPath
import runpy
import sys
import tarfile
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import zipfile

ROOT = Path(__file__).resolve().parents[1]
M = runpy.run_path(str(ROOT / 'scripts/fetch-engine.py'))
PLAN = runpy.run_path(str(ROOT / 'scripts/plan-packaging.py'))


def fixture(root):
    bundle = root / 'bundle'
    (bundle / 'bin').mkdir(parents=True)
    (bundle / 'LICENSES').mkdir()
    for name in ('ffmpeg.exe', 'ffprobe.exe', 'portico-disc.exe'):
        (bundle / 'bin' / name).write_bytes(b'Not executable; transport fixture only')
    for name in ('portico-disc.txt', 'dependency.txt'):
        (bundle / 'LICENSES' / name).write_text('test licence text')
    for name in ('NOTICE.md', 'sources.lock.json', 'portico-disc-build.json', 'portico-dovi-profile.v1.json'):
        (bundle / name).write_text('{}')
    (bundle / 'requirements.v1.json').write_bytes((ROOT / 'third_party/ffmpeg/requirements.v1.json').read_bytes())
    with tarfile.open(bundle / 'corresponding-source.tar.xz', 'w:xz') as archive:
        for name in ('provenance/contract/sources.lock.json', 'provenance/contract/build-source-authority.json',
                     'provenance/scripts/build-ffmpeg.sh', 'provenance/portico-disc/LICENSE',
                     'provenance/portico-disc/portico-disc.c', 'provenance/contract/patches/fixture.patch',
                     'recipe/build.sh', 'recipe/makeimage.sh', 'recipe/scripts.d/10-library.sh',
                     'recipe/.cache/downloads/10-library_fixture.tar.xz'):
            data = b'SCRIPT_REPO=fixture\n'
            info = tarfile.TarInfo(name)
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
    files = {p.relative_to(bundle).as_posix(): M['digest'](p) for p in bundle.rglob('*') if p.is_file()}
    (bundle / 'toolchain-manifest.json').write_text(json.dumps({'schemaVersion': 1, 'licenseMode': 'gpl3',
                                                             'target': 'windows-x64', 'files': files}))
    sha = '1' * 40
    name = 'ci-intermediate-ffmpeg-windows-x64-' + sha + '-NOT-FOR-USE'
    (root / 'README.txt').write_text(M['README'])
    (root / 'engine-receipt.json').write_text(json.dumps({'schemaVersion': 1, 'name': name,
        'target': 'windows-x64', 'engineSourceSHA': sha, 'release': False}))
    archive_path = root.parent / 'artifact.zip'
    with zipfile.ZipFile(archive_path, 'w') as archive:
        for path in sorted(root.rglob('*')):
            if path.is_file():
                archive.write(path, path.relative_to(root).as_posix())
    now = datetime.now(timezone.utc)
    pin = {'target': 'windows-x64', 'kind': 'portico-engine', 'purpose': 'qualification',
        'engineSourceSHA': sha, 'name': name, 'createdAt': (now - timedelta(days=2)).isoformat(),
        'expiresAt': (now + timedelta(days=12)).isoformat(),
        'archiveSHA256': M['digest'](archive_path), 'bundleManifestSHA256': M['digest'](bundle / 'toolchain-manifest.json'),
        'sourceArchiveSHA256': M['digest'](bundle / 'corresponding-source.tar.xz'),
        'licensesSHA256': M['json_digest']({p.relative_to(bundle).as_posix(): M['digest'](p) for p in (bundle / 'LICENSES').rglob('*')}),
        'origin': {'kind': 'github-actions', 'repository': 'PorticoMediaServer/portico', 'runId': 7, 'runAttempt': 2, 'artifactId': 42}}
    return {'schemaVersion': 1, 'maximumRetentionDays': 14, 'engines': {'windows-x64': pin}}, archive_path


class EngineIntake(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.lock, self.archive = fixture(self.root / 'input')
        self.pin = self.lock['engines']['windows-x64']

    def test_real_local_intake_preserves_bytes_and_native_status(self):
        # GitHub environment is irrelevant to an engine's producing commit.
        with patch.dict('os.environ', {'GITHUB_SHA': '2' * 40}):
            bundle = M['fetch'](self.lock, 'windows-x64', self.root / 'out', self.archive)
        self.assertEqual(M['digest'](bundle / 'toolchain-manifest.json'), self.pin['bundleManifestSHA256'])
        self.assertFalse((bundle / 'manifest.sig').exists())
        self.assertFalse((self.root / 'out/native-engine.json').exists())

    def test_wrong_raw_hash_does_not_extract(self):
        with self.archive.open('ab') as output:
            output.write(b'tamper')
        with self.assertRaisesRegex(ValueError, 'SHA-256'):
            M['fetch'](self.lock, 'windows-x64', self.root / 'out', self.archive)
        self.assertFalse((self.root / 'out').exists())

    def test_verifier_handles_windows_style_relative_paths_without_weakening_hashes(self):
        bundle = self.root / 'input/bundle'
        script = ROOT / 'scripts/verify-ffmpeg-manifest.py'
        source = script.read_text()

        class WindowsRelativeEntry:
            def __init__(self, path):
                self.path, self.name = path, path.name

            def is_file(self):
                return self.path.is_file()

            def relative_to(self, root):
                return PureWindowsPath(self.path.relative_to(bundle).as_posix())

        class InventoryRoot:
            def __fspath__(self):
                return str(bundle)

            def resolve(self):
                return self

            def __truediv__(self, name):
                return bundle / name

            def rglob(self, pattern):
                return (WindowsRelativeEntry(path) for path in bundle.rglob(pattern))

        def verify(code):
            with patch.dict(sys.modules, {'pathlib': SimpleNamespace(Path=lambda _: InventoryRoot())}), \
                    patch.object(sys, 'argv', [str(script), str(bundle)]), redirect_stdout(io.StringIO()):
                exec(compile(code, str(script), 'exec'), {'__name__': '__main__'})

        self.assertEqual(str(PureWindowsPath('bin/ffmpeg.exe')), 'bin\\ffmpeg.exe')
        # The previous comparison fails on the same real file inventory.
        previous = source.replace('p.relative_to(root).as_posix()', 'str(p.relative_to(root))', 1)
        with self.assertRaisesRegex(SystemExit, 'inventory mismatch'):
            verify(previous)
        verify(source)
        (bundle / 'bin/ffmpeg.exe').write_bytes(b'tampered executable')
        with self.assertRaisesRegex(SystemExit, 'Bundle verification failed'):
            verify(source)

    def test_pin_purpose_kind_and_target_are_checked(self):
        for field, value in [('purpose', 'release'), ('kind', 'stand-in'), ('target', 'windows-arm64'),
                             ('name', 'latest'), ('archiveSHA256', 'wrong')]:
            lock = deepcopy(self.lock)
            lock['engines']['windows-x64'][field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                M['select'](lock, 'windows-x64')

    def test_retention_and_expiry(self):
        M['select'](self.lock, 'windows-x64')  # Reuse after two days is allowed.
        with self.assertRaises(ValueError):
            M['select'](self.lock, 'windows-x64', datetime.now(timezone.utc) + timedelta(days=13))
        self.pin['expiresAt'] = (datetime.now(timezone.utc) + timedelta(days=14)).isoformat()
        with self.assertRaises(ValueError):
            M['select'](self.lock, 'windows-x64')

    def test_exact_remote_identity_not_consumer_run(self):
        origin = self.pin['origin']
        metadata = {'id': 42, 'name': self.pin['name'], 'workflow_run': {'id': 7, 'head_sha': self.pin['engineSourceSHA']},
            'expired': False, 'digest': 'sha256:' + self.pin['archiveSHA256'], 'size_in_bytes': self.archive.stat().st_size,
            'created_at': self.pin['createdAt'], 'expires_at': self.pin['expiresAt']}
        M['verify_metadata'](self.pin, metadata)
        for field, value in [('id', 41), ('name', 'latest'), ('expired', True), ('digest', 'sha256:' + '0' * 64),
                             ('size_in_bytes', M['MAX_ARCHIVE'] + 1)]:
            with self.subTest(field=field), self.assertRaises(ValueError):
                M['verify_metadata'](self.pin, dict(metadata, **{field: value}))
        metadata['workflow_run']['id'] = 99
        with self.assertRaises(ValueError):
            M['verify_metadata'](self.pin, metadata)
        self.assertEqual(origin['runId'], 7)

    def test_unsafe_zip_and_tar_never_extract(self):
        bad = self.root / 'bad.zip'
        for name in ('../escape', '/absolute', 'C:/absolute', 'folder\\escape', 'aux.txt', 'folder/../escape'):
            with zipfile.ZipFile(bad, 'w') as archive:
                archive.writestr(name, 'bad')
            with self.subTest(name=name), self.assertRaises(ValueError):
                M['extract'](bad, self.root / 'bad-out')
            self.assertFalse((self.root / 'bad-out').exists())
        for kind in (tarfile.SYMTYPE, tarfile.LNKTYPE, tarfile.CHRTYPE):
            with tarfile.open(self.root / 'bad.tar', 'w') as archive:
                info = tarfile.TarInfo('link')
                info.type, info.linkname = kind, '../escape'
                archive.addfile(info)
            with self.assertRaises(ValueError):
                M['extract'](self.root / 'bad.tar', self.root / 'bad-out')

    def test_case_duplicates_prefix_collision_and_expansion(self):
        for names in (('Same', 'same'), ('folder', 'folder/file')):
            with zipfile.ZipFile(self.root / 'bad.zip', 'w') as archive:
                for name in names:
                    archive.writestr(name, 'x')
            with self.assertRaises(ValueError):
                M['extract'](self.root / 'bad.zip', self.root / 'bad-out')
        with patch.dict(M['extract'].__globals__, {'MAX_EXPANDED': 1}):
            with self.assertRaises(ValueError):
                M['extract'](self.archive, self.root / 'bad-out')

    def test_manifest_source_and_licence_corruption(self):
        for field in ('bundleManifestSHA256', 'sourceArchiveSHA256', 'licensesSHA256'):
            pin = dict(self.pin, **{field: '0' * 64})
            with self.subTest(field=field), self.assertRaises(ValueError):
                M['verify_bundle'](self.root / 'input', pin)
        with tarfile.open(self.root / 'empty.tar.xz', 'w:xz'):
            pass
        with self.assertRaisesRegex(ValueError, 'incomplete'):
            M['verify_source'](self.root / 'empty.tar.xz', 'windows-x64')

    def test_no_engine_means_explicit_block_and_no_native_jobs(self):
        actual = json.loads((ROOT / 'third_party/ffmpeg/artifacts.lock.json').read_text())
        report = PLAN['plan'](dict(actual, engines={}))
        self.assertEqual(report['matrix'], {'include': []})
        self.assertEqual(set(report['blocked']), set(M['TARGETS']))
        self.assertFalse(report['packagingQualified'])
        for target in M['TARGETS']:
            self.assertFalse(actual['rejectedCandidates'][target]['acceptedForUse'])
        report = PLAN['plan'](self.lock)
        self.assertEqual(report['matrix']['include'], [{'target': 'windows-x64', 'runner': 'windows-2025'}])


if __name__ == '__main__':
    unittest.main()
