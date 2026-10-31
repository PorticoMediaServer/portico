#!/usr/bin/env python3
"""Construction boundaries and the actual portable launch contract."""
import json
import os
from pathlib import Path
import runpy
import struct
import tempfile
import unittest
from unittest.mock import patch
import zipfile

ROOT = Path(__file__).resolve().parents[1]
M = runpy.run_path(str(ROOT / 'scripts/ci-unsigned-payload.py'))
CONTEXT = runpy.run_path(str(ROOT / 'scripts/qualification-context.py'))
ENV = {'GITHUB_ACTIONS': 'true', 'GITHUB_WORKFLOW_REF':
       'PorticoMediaServer/portico/.github/workflows/packaging-qualification.yml@refs/heads/test',
       'GITHUB_EVENT_NAME': 'pull_request', 'GITHUB_REPOSITORY': 'PorticoMediaServer/portico',
       'GITHUB_RUN_ID': '2', 'GITHUB_RUN_ATTEMPT': '1', 'GITHUB_REF': 'refs/pull/24/merge'}


class Qualification(unittest.TestCase):
    def test_explicit_local_context_and_no_environment_spoof(self):
        self.assertEqual(M['guard']({}, 'local'), {'context': 'local', 'release': False})
        with self.assertRaises(ValueError):
            M['guard']({}, 'github-actions')
        for env in (ENV, {'GITHUB_ACTIONS': 'false'}, {'GITHUB_RUN_ID': '3'}):
            with self.assertRaises(ValueError):
                M['guard'](env, 'local')

    def test_only_qualification_workflow(self):
        M['guard'](ENV, 'github-actions')
        for env in ({}, dict(ENV, GITHUB_WORKFLOW_REF='PorticoMediaServer/portico/.github/workflows/release.yml@main'),
                    dict(ENV, GITHUB_EVENT_NAME='release'), dict(ENV, GITHUB_REF='refs/tags/v1')):
            with self.assertRaises(ValueError):
                M['guard'](env, 'github-actions')

    def test_even_empty_signing_configuration_refused_in_both_contexts(self):
        for key in ('PORTICO_RELEASE_SIGNING_KEY_FILE', 'PORTICO_MACOS_SIGNING_IDENTITY', 'CSC_LINK', 'PRODUCTKEY', 'SIGNINGCONFIG'):
            for env, context in (({}, 'local'), (ENV, 'github-actions')):
                with self.assertRaises(ValueError):
                    M['guard'](dict(env, **{key: ''}), context)

    def test_ci_head_and_repository_are_bound(self):
        with tempfile.TemporaryDirectory() as tmp:
            event = Path(tmp) / 'event.json'
            request = {'head': {'sha': '1' * 40, 'repo': {'full_name': 'PorticoMediaServer/portico'}}}
            event.write_text(json.dumps({'pull_request': request}))
            env = dict(ENV, GITHUB_EVENT_PATH=str(event))
            with patch('subprocess.check_output', return_value='1' * 40 + '\n'):
                self.assertEqual(CONTEXT['receipt'](env, 'github-actions')['installerSourceSHA'], '1' * 40)
            with patch('subprocess.check_output', return_value='2' * 40 + '\n'):
                with self.assertRaises(ValueError):
                    CONTEXT['receipt'](env, 'github-actions')
            request['head']['repo']['full_name'] = 'fork/portico'
            event.write_text(json.dumps({'pull_request': request}))
            with self.assertRaises(ValueError):
                CONTEXT['receipt'](env, 'github-actions')

    def test_executable_architecture(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'binary'
            for target, machine in [('windows-x64', 0x8664), ('windows-arm64', 0xaa64)]:
                data = bytearray(128)
                data[:2] = b'MZ'
                struct.pack_into('<I', data, 60, 80)
                data[80:84] = b'PE\0\0'
                struct.pack_into('<H', data, 84, machine)
                path.write_bytes(data)
                M['architecture'](path, target)
                with self.assertRaises(ValueError):
                    M['architecture'](path, 'windows-arm64' if target.endswith('x64') else 'windows-x64')

    def test_custody_and_symlink_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
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

    def test_portable_archive_contains_real_launcher_web_and_engine_bytes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            payload = root / 'payload'
            (payload / 'web').mkdir(parents=True)
            (payload / 'web/index.html').write_bytes(b'fixture web')
            (payload / 'portico-server.exe').write_bytes(b'fixture server')
            (payload / 'third_party/ffmpeg/bin').mkdir(parents=True)
            (payload / 'third_party/ffmpeg/bin/ffmpeg.exe').write_bytes(b'fixture engine')
            M['windows_portable'](payload, root / 'portable.zip')
            with zipfile.ZipFile(root / 'portable.zip') as archive:
                self.assertEqual(archive.read('start-portico.cmd'), (ROOT / 'packaging/portable/start-portico.cmd').read_bytes())
                self.assertEqual(archive.read('third_party/ffmpeg/bin/ffmpeg.exe'), b'fixture engine')
                self.assertEqual(archive.read('web/index.html'), b'fixture web')
                self.assertIn(b'NOT-FOR-USE', archive.read('NOT-FOR-USE.txt'))

    def test_workflow_consumes_only_pins_and_uploads_only_evidence(self):
        workflow = (ROOT / '.github/workflows/packaging-qualification.yml').read_text()
        self.assertIn('max-parallel: 2', workflow)
        for forbidden in ('build-ffmpeg.sh', 'ci-ffmpeg-native.py', 'source_x64:', 'pull_request_target:', 'secrets.'):
            self.assertNotIn(forbidden, workflow)
        self.assertIn('fetch-engine.py --target', workflow)
        self.assertIn("if: needs.plan.outputs.available == 'true'", workflow)
        self.assertNotIn('path: ${{ runner.temp }}/qualification/payload-build', workflow)


if __name__ == '__main__':
    unittest.main()
