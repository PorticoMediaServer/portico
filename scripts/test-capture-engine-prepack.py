#!/usr/bin/env python3
"""Transport controls use inert fixtures, never substitute engine evidence."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('capture', ROOT / 'scripts/capture-engine-prepack.py')
capture = importlib.util.module_from_spec(spec); spec.loader.exec_module(capture)


class Prepack(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name); self.build = self.root / 'build'
        self.prefix = self.build / 'recipe/workspace'
        (self.prefix / 'bin').mkdir(parents=True)
        for name in ('ffmpeg', 'ffprobe', 'portico-disc'):
            path = self.prefix / 'bin' / name
            path.write_bytes(b'inert fixture --enable-gpl --enable-version3 --enable-libsvtav1\0')
            path.chmod(0o755)
        (self.build / 'corresponding-source.tar.xz').write_bytes(b'inert source archive')
        self.output = self.root / 'prepack'

    def perform(self):
        return capture.capture('macos-arm64', self.build, self.output,
                               dict(runId=12, runAttempt=1, engineSourceSHA='a' * 40,
                                    qualified=False, release=False, target='macos-arm64'), ROOT)

    def test_complete_capture_hashes_payload_source_inventory_and_internal_link(self):
        os.symlink(self.prefix / 'bin/ffmpeg', self.prefix / 'bin/link')
        result = self.perform()
        data = (self.output / 'payload.tar').read_bytes()
        self.assertEqual(result['payloadSHA256'], hashlib.sha256(data).hexdigest())
        receipt = json.loads((self.output / 'receipt.json').read_text())
        self.assertFalse(receipt['qualified']); self.assertFalse(receipt['readyForPublication'])
        self.assertFalse(receipt['nativeEngineChecks']); self.assertFalse(receipt['release'])
        self.assertEqual(receipt['runId'], 12)
        self.assertEqual(receipt['inputs']['corresponding-source.tar.xz'],
                         hashlib.sha256(b'inert source archive').hexdigest())
        self.assertEqual(receipt['files']['bin/ffmpeg']['mode'], 0o755)
        with tarfile.open(self.output / 'payload.tar') as archive:
            self.assertEqual(archive.getmember('prefix/bin/link').linkname, 'ffmpeg')
            self.assertEqual(archive.extractfile('prefix/bin/ffmpeg').read(),
                             (self.prefix / 'bin/ffmpeg').read_bytes())
            self.assertIn('sources.lock.json', archive.getnames())

    def test_incomplete_compilation_or_source_is_not_captured(self):
        source = self.build / 'corresponding-source.tar.xz'; source.unlink()
        self.assertIsNone(self.perform()); self.assertFalse(self.output.exists())
        source.write_bytes(b'source'); (self.prefix / 'bin/portico-disc').unlink()
        self.assertIsNone(self.perform()); self.assertFalse(self.output.exists())

    def test_external_link_and_nonfree_refuse_without_partial_output(self):
        outside = self.root / 'outside'; outside.write_text('outside')
        link = self.prefix / 'bin/link'; link.symlink_to(outside)
        with self.assertRaisesRegex(ValueError, 'internal file symlinks'):
            self.perform()
        link.unlink()
        (self.prefix / 'bin/ffmpeg').write_bytes(b'--enable-gpl --enable-version3 --enable-libfdk-aac')
        with self.assertRaisesRegex(ValueError, 'prohibited'):
            self.perform()
        self.assertFalse(self.output.exists())

    def test_write_failure_and_changed_input_leave_no_partial_capture(self):
        with patch.object(capture.tarfile, 'open', side_effect=OSError('injected write failure')):
            with self.assertRaises(OSError): self.perform()
        self.assertFalse(self.output.exists())
        real = capture.inventory; calls = []
        def changed(prefix):
            calls.append(1)
            value = real(prefix)
            if len(calls) == 2: value['changed'] = {'bytes': 1}
            return value
        with patch.object(capture, 'inventory', side_effect=changed):
            with self.assertRaisesRegex(ValueError, 'changed during capture'): self.perform()
        self.assertFalse(self.output.exists())
        self.assertEqual(list(self.root.glob('.prepack-*')), [])


if __name__ == '__main__':
    unittest.main()
