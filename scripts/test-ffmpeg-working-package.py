#!/usr/bin/env python3
"""A first working package must not impersonate a qualified release bundle."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class WorkingPackageTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.input = self.root / 'input'
        (self.input / 'bin').mkdir(parents=True)
        for name in ('ffmpeg', 'ffprobe', 'portico-disc'):
            (self.input / 'bin' / name).write_bytes(b'synthetic-executable-for-packaging-only')
        for name in ('portico-disc-build.json', 'dependency-inputs.json', 'compiler.txt',
                     'compiler-linked-inputs.json', 'license-inventory.json',
                     'linux-build-inputs.json', 'libdovi-build.json'):
            (self.input / name).write_text('{}')
        (self.input / 'portico-disc-LICENSE').write_text('Synthetic notice')
        (self.input / 'LICENSES').mkdir()
        (self.input / 'LICENSES/dependency.txt').write_text('Synthetic dependency notice')
        self.source = self.root / 'corresponding-source.tar.xz'
        self.source.write_bytes(b's' * 2048)
        self.output = self.root / 'output'

    def run_package(self, *options):
        return subprocess.run([sys.executable, str(Path(__file__).with_name('normalize-ffmpeg-bundle.py')),
                               str(self.input), 'linux-x64', str(self.output), str(self.source), *options],
                              capture_output=True, text=True)

    def test_working_package_cannot_be_automatically_admitted(self):
        result = self.run_package('--working')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.output / 'toolchain-manifest.json').exists())
        manifest = json.loads((self.output / 'working-manifest.json').read_text())
        self.assertIn('corresponding-source.tar.xz', manifest['files'])
        self.assertIn('LICENSES/dependency.txt', manifest['files'])
        self.assertIn('bin/ffmpeg', manifest['files'])

    def test_default_release_still_refuses_incomplete_qualification(self):
        result = self.run_package()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('repeat-build.json', result.stderr)
        self.assertFalse((self.output / 'toolchain-manifest.json').exists())


if __name__ == '__main__':
    unittest.main()
