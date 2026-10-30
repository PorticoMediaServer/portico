#!/usr/bin/env python3
"""A later qualification run must not replace a changed first working build."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('linux_ffmpeg', Path(__file__).with_name('build-linux-ffmpeg.py'))
build = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build)


class FirstBuildTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.output = Path(self.temporary.name)
        (self.output / 'bin').mkdir()
        self.inputs = {'compilerImage': 'locked-image', 'files': {'source': 'locked-source'}}
        self.source = 'original-source-archive'
        for name in ('ffmpeg', 'ffprobe', 'portico-disc'):
            (self.output / 'bin' / name).write_bytes(name.encode())
        self.record = {'round': 1, 'sourceArchiveSHA256': self.source,
                       'binaries': {name: build.digest(self.output / 'bin' / name)
                                    for name in ('ffmpeg', 'ffprobe', 'portico-disc')}}
        (self.output / 'linux-build-inputs.json').write_text(json.dumps(self.inputs))
        (self.output / 'first-build.json').write_text(json.dumps(self.record))

    def test_retains_matching_first_build(self):
        self.assertEqual(build.first_working_build(self.output, self.inputs, self.source), self.record)

    def test_changed_tool_is_refused(self):
        (self.output / 'bin/ffmpeg').write_bytes(b'replaced')
        with self.assertRaisesRegex(ValueError, 'build changed'):
            build.first_working_build(self.output, self.inputs, self.source)

    def test_changed_compiler_input_is_refused(self):
        with self.assertRaisesRegex(ValueError, 'inputs changed'):
            build.first_working_build(self.output, {'compilerImage': 'other'}, self.source)

    def test_changed_source_archive_is_refused(self):
        with self.assertRaisesRegex(ValueError, 'build changed'):
            build.first_working_build(self.output, self.inputs, 'other-source')

    def test_absent_first_build_requires_compilation(self):
        (self.output / 'first-build.json').unlink()
        self.assertIsNone(build.first_working_build(self.output, self.inputs, self.source))


if __name__ == '__main__':
    unittest.main()
