#!/usr/bin/env python3
"""Controlled archive-custody cases, never an FFmpeg/media qualification."""
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('ffmpeg_runner_export',
    Path(__file__).with_name('export-ffmpeg-runner-tools.py'))
exporter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(exporter)


class ExportTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(dir=os.environ['TMPDIR'])
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.artifacts = self.root / 'artifacts/hardware-r29'
        self.output = self.root / 'outputs'
        self.output.mkdir()
        self.commit, self.job = 'a' * 40, 'a' * 12 + '-12345678'
        self.retained = self.artifacts / self.commit / self.job
        self.retained.mkdir(parents=True)
        self.archive = self.retained / exporter.ARCHIVE
        self.manifest = self.retained / 'toolchain-manifest.json'
        self.receipt = self.retained / 'artifact-receipt.json'

    def bundle(self, mutate_member=None, unqualified=False):
        files = {name: 'b' * 64 for name in ('corresponding-source.tar.xz', 'sources.lock.json',
                 'NOTICE.md', 'portico-disc-build.json')}
        with tarfile.open(self.archive, 'w:xz') as archive:
            for number, name in enumerate(exporter.TOOLS):
                # Synthetic structural ELF header, never executed or published.
                data = bytearray(64 + number)
                data[:7], data[16:18], data[18:20] = b'\x7fELF\x02\x01\x01', b'\x02\x00', b'\x3e\x00'
                files['bin/' + name] = hashlib.sha256(data).hexdigest()
                member = tarfile.TarInfo('./bin/' + name)
                member.size, member.mode = len(data), 0o755
                if mutate_member:
                    mutate_member(member)
                archive.addfile(member, io.BytesIO(data) if member.isfile() else None)
            ignored = tarfile.TarInfo('./unrequested-source.txt')
            ignored.size = 11
            archive.addfile(ignored, io.BytesIO(b'source-only'))
        self.manifest.write_text(json.dumps({'schemaVersion': 1, 'target': 'linux-x64',
            'licenseMode': 'gpl3', 'files': files}))
        expected = {'archive': hashlib.sha256(self.archive.read_bytes()).hexdigest(),
                    'manifest': hashlib.sha256(self.manifest.read_bytes()).hexdigest()}
        self.receipt.write_text(json.dumps({'schema': 1, 'buildSourceCommit': self.commit,
            'qualifiedTargetVerification': not unqualified, 'artifactPath': str(self.archive),
            'files': {exporter.ARCHIVE: expected['archive'], 'toolchain-manifest.json': expected['manifest']}}))
        expected['receipt'] = hashlib.sha256(self.receipt.read_bytes()).hexdigest()
        return expected

    def export(self, expected, job=None):
        return exporter.export_tools(self.artifacts, self.output, self.commit, job or self.job, expected)

    def test_exact_three_images_only(self):
        result = self.export(self.bundle())
        self.assertEqual(set(result['tools']), set(exporter.TOOLS))
        self.assertEqual(set(p.name for p in self.output.iterdir()), set(exporter.TOOLS))
        self.assertEqual(result['bundleManifest']['licenseMode'], 'gpl3')
        self.assertIn('corresponding-source.tar.xz', result['bundleManifest']['files'])
        self.assertEqual(result['originalBuildReceipt']['buildSourceCommit'], self.commit)
        for name in exporter.TOOLS:
            self.assertEqual((self.output / name).stat().st_mode & 0o777, 0o755)
            self.assertEqual(hashlib.sha256((self.output / name).read_bytes()).hexdigest(),
                             result['tools'][name]['sha256'])

    def test_changed_archive_is_refused_before_export(self):
        expected = self.bundle()
        self.archive.write_bytes(self.archive.read_bytes() + b'changed')
        with self.assertRaises(ValueError):
            self.export(expected)
        self.assertEqual(list(self.output.iterdir()), [])

    def test_changed_manifest_is_refused_before_export(self):
        expected = self.bundle()
        self.manifest.write_text('{}')
        with self.assertRaises(ValueError):
            self.export(expected)
        self.assertEqual(list(self.output.iterdir()), [])

    def test_unqualified_receipt_is_refused(self):
        with self.assertRaises(ValueError):
            self.export(self.bundle(unqualified=True))
        self.assertEqual(list(self.output.iterdir()), [])

    def test_foreign_lane_job_path_is_refused(self):
        with self.assertRaises(ValueError):
            self.export(self.bundle(), '../other-lane')
        self.assertEqual(list(self.output.iterdir()), [])

    def test_retained_directory_link_is_refused(self):
        expected = self.bundle()
        moved = self.retained.with_name('other-directory')
        self.retained.rename(moved)
        self.retained.symlink_to(moved, target_is_directory=True)
        with self.assertRaises(OSError):
            self.export(expected)
        self.assertEqual(list(self.output.iterdir()), [])

    def test_linked_tool_member_is_refused(self):
        def link(member):
            member.type, member.linkname = tarfile.SYMTYPE, '/unrelated'
        with self.assertRaises(ValueError):
            self.export(self.bundle(mutate_member=link))
        self.assertEqual(list(self.output.iterdir()), [])

    def test_existing_output_is_preserved(self):
        expected = self.bundle()
        (self.output / 'ffmpeg').write_text('preserve original')
        with self.assertRaises(FileExistsError):
            self.export(expected)
        self.assertEqual((self.output / 'ffmpeg').read_text(), 'preserve original')


if __name__ == '__main__':
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(ExportTests))
    output = Path(os.environ['PORTICO_RUNNER_OUTPUT']) / 'runner-export-tests.log'
    output.write_text(json.dumps({'tests': result.testsRun, 'failures': len(result.failures),
        'errors': len(result.errors), 'skips': len(result.skipped),
        'scope': 'Synthetic archive custody cases only; no real executable, FFmpeg build, media or hardware qualification.'}, indent=2) + '\n')
    raise SystemExit(0 if result.wasSuccessful() else 1)
