#!/usr/bin/env python3
"""Pure negative controls for the isolated qualification assembler."""
import os
import pathlib
import runpy
import struct
import tempfile
import unittest

M = runpy.run_path(str(pathlib.Path(__file__).with_name('ci-unsigned-payload.py')))
ENV = {'GITHUB_ACTIONS': 'true', 'GITHUB_WORKFLOW_REF':
       'PorticoMediaServer/portico/.github/workflows/packaging-qualification.yml@refs/heads/test',
       'GITHUB_EVENT_NAME': 'pull_request', 'GITHUB_REF': 'refs/pull/19/merge'}


class Qualification(unittest.TestCase):
    def test_only_qualification_workflow(self):
        M['guard'](ENV)
        for changed in ({}, dict(ENV, GITHUB_WORKFLOW_REF='PorticoMediaServer/portico/.github/workflows/release.yml@main'),
                        dict(ENV, GITHUB_EVENT_NAME='release'), dict(ENV, GITHUB_REF='refs/tags/v1')):
            with self.assertRaises(ValueError):
                M['guard'](changed)

    def test_even_empty_signing_configuration_refused(self):
        for key in ('PORTICO_RELEASE_SIGNING_KEY_FILE', 'PORTICO_MACOS_SIGNING_IDENTITY', 'CSC_LINK', 'PRODUCTKEY', 'SIGNINGCONFIG'):
            with self.assertRaises(ValueError):
                M['guard'](dict(ENV, **{key: ''}))

    def test_executable_architecture(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = pathlib.Path(tmp) / 'binary'
            for target, machine in [('windows-x64', 0x8664), ('windows-arm64', 0xaa64)]:
                raw = bytearray(128)
                raw[:2] = b'MZ'
                struct.pack_into('<I', raw, 60, 80)
                raw[80:84] = b'PE\0\0'
                struct.pack_into('<H', raw, 84, machine)
                p.write_bytes(raw)
                M['architecture'](p, target)
                with self.assertRaises(ValueError):
                    M['architecture'](p, 'windows-arm64' if target.endswith('x64') else 'windows-x64')
            p.write_bytes(b'fixture')
            with self.assertRaises(ValueError):
                M['architecture'](p, 'macos-arm64')

    def test_custody_and_symlink_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / 'manifest.sig').write_bytes(b'not a seal')
            with self.assertRaises(ValueError):
                M['inventory'](root)
            (root / 'manifest.sig').unlink()
            (root / 'file').write_bytes(b'input')
            self.assertEqual(len(M['inventory'](root)['file']), 64)
            if os.name != 'nt':
                (root / 'link').symlink_to('file')
                with self.assertRaises(ValueError):
                    M['inventory'](root)

    def test_transfer_paths_and_symlinks(self):
        import zipfile
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            archive = root / 'transfer.zip'
            for name in ('../escape', '/absolute', 'C:/absolute', 'folder\\escape'):
                with zipfile.ZipFile(archive, 'w') as zipped:
                    zipped.writestr(name, 'bad')
                with self.assertRaises(ValueError):
                    M['extract_transfer'](archive, root / 'out')
            with zipfile.ZipFile(archive, 'w') as zipped:
                entry = zipfile.ZipInfo('link')
                entry.external_attr = 0o120777 << 16
                zipped.writestr(entry, '../outside')
            with self.assertRaises(ValueError):
                M['extract_transfer'](archive, root / 'out')
            with zipfile.ZipFile(archive, 'w') as zipped:
                zipped.writestr('README.txt', 'qualification source intermediate')
            M['extract_transfer'](archive, root / 'out')
            self.assertTrue((root / 'out/README.txt').is_file())

    def test_workflow_boundaries(self):
        source = (pathlib.Path(__file__).resolve().parents[1] / '.github/workflows/packaging-qualification.yml').read_text()
        self.assertIn('max-parallel: 2', source)
        self.assertEqual(source.count('retention-days: 1'), 2)
        self.assertNotIn('pull_request_target:', source)
        self.assertNotIn('secrets.', source)
        self.assertNotIn('path: ${{ runner.temp }}/qualification/payload-build', source)
        self.assertIn('needs: [source_x64, source_arm64]', source)
        self.assertIn('artifact_digest:', source)
        self.assertIn('ci-ffmpeg-native.py consume', source)
        self.assertIn('ci-ffmpeg-native.py macos', source)


if __name__ == '__main__':
    unittest.main()
