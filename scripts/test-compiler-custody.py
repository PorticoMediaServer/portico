#!/usr/bin/env python3
"""Offline source-custody controls; no compiler or engine qualification."""
import json
import hashlib
from pathlib import Path
import runpy
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
M = runpy.run_path(str(ROOT / 'scripts/capture-engine-compiler.py'))
INPUTS = runpy.run_path(str(ROOT / 'scripts/engine_inputs.py'))
ADAPTER = runpy.run_path(str(ROOT / 'scripts/engine-recipe-adapter.py'))


class CompilerCustody(unittest.TestCase):
    def test_exported_context_must_be_the_completed_image_for_both_docker_id_formats(self):
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
            layout = Path(tmp)
            blobs = layout / 'blobs/sha256'
            blobs.mkdir(parents=True)
            def blob(value):
                data = json.dumps(value).encode()
                sha = hashlib.sha256(data).hexdigest()
                (blobs / sha).write_bytes(data)
                return {'digest': 'sha256:' + sha, 'size': len(data)}
            config = blob({'architecture': 'amd64', 'os': 'linux'})
            manifest = blob({'schemaVersion': 2, 'config': config, 'layers': []})
            index = layout / 'index.json'
            index.write_text(json.dumps({'schemaVersion': 2, 'manifests': [manifest]}))
            for identity in (manifest['digest'], config['digest']):
                self.assertEqual(INPUTS['compiler_layout'](layout, identity)['configDigest'], config['digest'])
            with self.assertRaisesRegex(ValueError, 'source-inventoried image'):
                INPUTS['compiler_layout'](layout, 'sha256:' + 'f' * 64)
            index.write_text(json.dumps({'schemaVersion': 2, 'manifests': [manifest, manifest]}))
            with self.assertRaisesRegex(ValueError, 'unambiguous'):
                INPUTS['compiler_layout'](layout, manifest['digest'])
            index.write_text(json.dumps({'schemaVersion': 2, 'manifests': [manifest]}))
            (blobs / config['digest'].removeprefix('sha256:')).write_text('changed config')
            with self.assertRaisesRegex(ValueError, 'metadata changed'):
                INPUTS['compiler_layout'](layout, manifest['digest'])

    def test_inventory_and_image_binding_refuse_modified_missing_or_wrong_target_sources(self):
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
            recipe = Path(tmp)
            root = recipe / 'portico-compiler-inputs'
            for name in ('crosstool-ng.commit', 'crosstool-ng.config', 'crosstool-ng.tar.xz',
                         'rust-library.tar.xz', 'LICENSES/rust/COPYRIGHT-library.html', 'capture.py',
                         'tarballs/gcc-fixture.tar.xz', 'tarballs/mingw-w64-fixture.tar.bz2'):
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b'1' * 40 if name == 'crosstool-ng.commit' else b'transport fixture; no real source')
            record = {'schemaVersion': 1, 'release': False, 'target': 'x86_64-pc-windows-gnu',
                'files': {p.relative_to(root).as_posix(): M['digest'](p) for p in root.rglob('*') if p.is_file()},
                'gccRuntimeArchives': {'libgcc.a': '2' * 64}, 'rustRuntimeArchives': {'libstd.rlib': '3' * 64}}
            path = root / 'compiler-inputs.json'
            path.write_text(json.dumps(record))
            report = {'target': 'windows-x64', 'compilerImageId': 'sha256:' + '4' * 64,
                      'compilerSourceInventorySHA256': M['digest'](path)}
            (recipe / 'portico-compiler-image.json').write_text(json.dumps({
                'compilerImageId': report['compilerImageId'], 'sourceInventorySHA256': M['digest'](path)}))
            INPUTS['compiler_inputs'](recipe, report)
            with self.assertRaisesRegex(ValueError, 'consumed image'):
                INPUTS['compiler_inputs'](recipe, dict(report, compilerImageId='sha256:' + '5' * 64))
            with self.assertRaisesRegex(ValueError, 'runtime target'):
                INPUTS['compiler_inputs'](recipe, dict(report, target='windows-arm64'))
            source = root / 'tarballs/gcc-fixture.tar.xz'
            source.write_bytes(b'changed source')
            with self.assertRaisesRegex(ValueError, 'inventory changed'):
                INPUTS['compiler_inputs'](recipe, report)
            source.unlink()
            with self.assertRaisesRegex(ValueError, 'inventory changed'):
                INPUTS['compiler_inputs'](recipe, report)
            source.symlink_to(recipe / 'outside')
            with self.assertRaisesRegex(ValueError, 'contains a link'):
                INPUTS['compiler_inputs'](recipe, report)

    def test_retention_happens_before_cleanup_without_updating_tools(self):
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
            root = Path(tmp)
            directory = root / 'images/base-win64'
            directory.mkdir(parents=True)
            path = directory / 'Dockerfile'
            path.write_text('FROM compiler\nRUN git clone upstream /ct-ng && cd /ct-ng && \\\n    ./ct-ng build && \\\n    cd / && \\\n    rm -rf ct-ng\nENV FFBUILD_RUST_TARGET=x86_64-pc-windows-gnu\n')
            ADAPTER['compiler_custody'](root)
            text = path.read_text()
            self.assertLess(text.index('cp -a build/tarballs/.'), text.index('rm -rf ct-ng'))
            self.assertIn('git archive -o /opt/portico-compiler-source/crosstool-ng.tar HEAD', text)
            self.assertIn('rustup component add rust-src', text)
            self.assertNotIn('rustup update', text)
            self.assertEqual((directory / 'portico-capture.py').read_bytes(),
                             (ROOT / 'scripts/capture-engine-compiler.py').read_bytes())


if __name__ == '__main__':
    unittest.main()
