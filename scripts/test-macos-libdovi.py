#!/usr/bin/env python3
"""Offline controls for native source/tool custody; no compiler substitute proof."""
import hashlib
import io
import json
from pathlib import Path
import runpy
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
M = runpy.run_path(str(ROOT / 'scripts/build-macos-libdovi.py'))


class MacDovi(unittest.TestCase):
    def test_rust_and_cargo_c_cannot_drift_from_the_shared_media_engine_versions(self):
        pin = json.loads((ROOT / 'third_party/ffmpeg/native-rust-tools.lock.json').read_text())
        canonical = ROOT / 'third_party/media-engine/sources.lock.json'
        M['shared_versions'](pin, canonical)
        for tool, field in [('rust', 'version'), ('cargoC', 'version')]:
            changed = json.loads(json.dumps(pin)); changed[tool][field] = '999.0.0'
            with self.subTest(tool=tool), self.assertRaises(ValueError):
                M['shared_versions'](changed, canonical)

    def test_component_must_match_the_sealed_official_manifest_before_install(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); manifest = root / 'distribution.toml'
            manifest.write_text('[pkg.rustc]\ngit_commit_hash="checked-commit"\n'
                '[pkg.rustc.target.aarch64-apple-darwin]\nxz_url="https://example.invalid/checked"\nxz_hash="checked-hash"\n')
            pin = {'rust': {'commit': 'checked-commit', 'distributionManifest': {}, 'source': {},
                           'targets': {'aarch64-apple-darwin': {'rustc': {'url': 'https://example.invalid/replaced',
                                                                        'sha256': 'checked-hash'}}}}}
            with patch.dict(M['tools'].__globals__, download=lambda *args: manifest), patch('subprocess.run') as install:
                with self.assertRaises(ValueError):
                    M['tools'](pin, 'aarch64-apple-darwin', root / 'tools', root)
                install.assert_not_called()

    def test_wrong_native_architecture_refuses_before_creating_inputs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with patch('platform.system', return_value='Linux'), self.assertRaises(ValueError):
                M['build']('macos-arm64', root, root, root)
            self.assertEqual(list(root.iterdir()), [])

    def test_download_hash_failure_never_becomes_a_cached_source(self):
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary) / 'source.crate'
            with patch('urllib.request.urlopen', return_value=io.BytesIO(b'wrong source')):
                with self.assertRaises(ValueError):
                    M['download']({'url': 'https://example.invalid/source', 'sha256': '0' * 64}, destination)
            self.assertFalse(destination.exists())

    def test_existing_wrong_cache_refuses_without_network(self):
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary) / 'source.crate'; destination.write_bytes(b'changed')
            with patch('urllib.request.urlopen') as network, self.assertRaises(ValueError):
                M['download']({'url': 'https://example.invalid/source', 'sha256': '0' * 64}, destination)
            network.assert_not_called()

    def test_source_archive_cannot_escape_stage(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); archive = root / 'source.tar'
            with tarfile.open(archive, 'w') as stream:
                info = tarfile.TarInfo('../escaped'); info.size = 4
                stream.addfile(info, io.BytesIO(b'evil'))
            with self.assertRaises(tarfile.FilterError):
                M['extract'](archive, root / 'unpack')
            self.assertFalse((root / 'escaped').exists())

    def test_configure_preserves_exact_collector_source_and_pins(self):
        with tempfile.TemporaryDirectory() as temporary:
            recipe = Path(temporary); M['configure'](recipe)
            source = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
            inputs = recipe / 'portico-libdovi-inputs'
            self.assertEqual((inputs / 'shared-collector.py').read_bytes(), (ROOT / 'scripts/build-linux-libdovi.py').read_bytes())
            self.assertEqual((inputs / 'contract.json').read_bytes(), (ROOT / source['libdovi']['contract']).read_bytes())
            self.assertEqual(hashlib.sha256((recipe / 'portico-native-rust-tools.lock.json').read_bytes()).hexdigest(),
                             source['nativeRustTools']['sha256'])
            self.assertIn('sys.argv[4]', (inputs / 'collect.py').read_text())

    def test_vendor_uses_both_locked_manifests_and_keeps_the_complete_tree(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config = '[source.crates-io]\nreplace-with="vendor"\n'
            with patch('subprocess.check_output', return_value=config) as command:
                M['vendor'](root, root / 'vendor', {}, 'dolby_vision/Cargo.toml')
            self.assertEqual(command.call_args.args[0], ['cargo', 'vendor', '--locked', '--versioned-dirs',
                '--sync', 'dolby_vision/Cargo.toml', str(root / 'vendor')])
            self.assertEqual((root / '.cargo/config.toml').read_text(), config)


if __name__ == '__main__':
    unittest.main()
