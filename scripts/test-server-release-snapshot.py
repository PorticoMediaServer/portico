#!/usr/bin/env python3
"""Qualify the real sealed layout, including every mandatory sibling test input."""
import hashlib
import importlib.util
import pathlib
import tempfile
import unittest
from unittest.mock import patch

ROOT = pathlib.Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('release_builder', ROOT / 'scripts/build-server-release.py')
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


class ServerSnapshotTests(unittest.TestCase):
    def test_component_module_identity(self):
        self.assertEqual(builder.module_path('server'), 'github.com/PorticoMediaServer/portico/server-next')
        self.assertEqual(builder.module_path('hosted'), 'portico.local/hosted')

    def test_actual_sibling_inputs_are_captured_hashed_and_tamper_evident(self):
        with tempfile.TemporaryDirectory() as directory:
            stage = pathlib.Path(directory) / 'sealed'
            digest, rows = builder.capture_source(ROOT / 'server-next', stage, b'fixture public root')
            inventory = {row['path']: row['sha256'] for row in rows}
            self.assertEqual((stage / 'source').resolve(), stage / 'server-next')
            for name in builder.TEST_INPUTS:
                base = ROOT / name
                paths = sorted(base.rglob('*')) if base.is_dir() else [base]
                for path in paths:
                    if path.is_file():
                        relative = str(path.relative_to(ROOT))
                        self.assertEqual((stage / relative).read_bytes(), path.read_bytes())
                        self.assertEqual(inventory['../' + relative], hashlib.sha256(path.read_bytes()).hexdigest())
            self.assertFalse((stage / 'apikit').exists())
            self.assertFalse((stage / 'go.work').exists())
            self.assertIn('../root-public-key.pem', inventory)
            for name in builder.TEST_INPUTS:
                base = stage / name
                target = next(path for path in sorted(base.rglob('*')) if path.is_file()) if base.is_dir() else base
                original = target.read_bytes()
                target.write_bytes(original + b'\nchanged')
                self.assertNotEqual(builder.snapshot_digest(stage / 'source')[0], digest, name)
                target.write_bytes(original)
            self.assertEqual(builder.snapshot_digest(stage / 'source')[0], digest)
            (stage / builder.TEST_INPUTS[0]).unlink()
            with self.assertRaisesRegex(ValueError, 'Missing release test input'):
                builder.snapshot_digest(stage / 'source')

    def test_concurrent_sibling_edit_aborts_capture(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            repo, stage = root / 'repo', root / 'sealed'
            (repo / 'server-next').mkdir(parents=True)
            (repo / 'server-next/go.mod').write_text('module github.com/PorticoMediaServer/portico/server-next\n')
            dependency = repo / 'writer.mjs'
            dependency.write_text('before')
            copy = builder.shutil.copyfile

            def edit_while_copying(source, destination):
                result = copy(source, destination)
                if source == dependency:
                    dependency.write_text('after')
                return result

            with patch.object(builder, 'ROOT', repo), patch.object(builder, 'TEST_INPUTS', ('writer.mjs',)), patch.object(builder.shutil, 'copyfile', edit_while_copying):
                with self.assertRaisesRegex(ValueError, 'changed during capture'):
                    builder.capture_source(repo / 'server-next', stage, b'public root')


if __name__ == '__main__':
    unittest.main()
